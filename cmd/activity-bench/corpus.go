package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/victorarias/attn/internal/activity"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/transcript"
)

// Entry is one frozen benchmark case: a real transcript window plus the state
// the daemon actually reported for that session at capture time.
//
// State is captured rather than inferred. Reconstructing historical state from a
// transcript would be a guess, and the whole point of the state field is that it
// is ground truth the model must respect.
type Entry struct {
	ID          string `json:"id"`
	CapturedAt  string `json:"captured_at"`
	Agent       string `json:"agent"`
	State       string `json:"state"`
	StateReason string `json:"state_reason,omitempty"`
	// Previous is the line a prior activity run produced, when the corpus has
	// one for this session. Empty on the first capture, which is itself a case
	// worth benchmarking.
	Previous string `json:"previous,omitempty"`
	// Window is the rendered delta. Stored rendered rather than as raw events so
	// a corpus entry stays a stable benchmark input even if clip budgets change;
	// re-run `corpus` to pick up new budgets.
	Window string `json:"window"`
	Events int    `json:"events"`
	Chars  int    `json:"chars"`
	// Truncated carries the window's own report, so a case that hit a tripwire
	// is visible in the corpus rather than silently short.
	Truncated string `json:"truncated,omitempty"`
	// Cursor is where this window ended, so the next capture for this session
	// reads a genuine delta instead of the whole transcript.
	Cursor string `json:"cursor"`
}

func runCorpus(args []string) error {
	fs := flag.NewFlagSet("corpus", flag.ExitOnError)
	dir := fs.String("dir", defaultDir, "corpus directory")
	socket := fs.String("socket", "", "attn socket path (default: profile socket)")
	verbose := fs.Bool("v", false, "print each captured entry")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := ensureDir(*dir); err != nil {
		return err
	}

	socketPath := *socket
	if socketPath == "" {
		socketPath = config.SocketPath()
	}
	sessions, err := client.New(socketPath).Query("")
	if err != nil {
		return fmt.Errorf("query sessions (is the daemon running?): %w", err)
	}

	corpus, err := loadCorpus(*dir)
	if err != nil {
		return err
	}
	// Cursors are kept separately from the corpus so a session that has only
	// been seeded does not leave a junk entry behind.
	cursors, err := loadCursors(*dir)
	if err != nil {
		return err
	}
	previous := map[string]string{}
	for _, entry := range corpus {
		previous[entry.ID] = entry.Previous
	}

	added, seeded := 0, 0
	for _, session := range sessions {
		path := transcriptPathFor(session.ID, string(session.Agent))
		if path == "" {
			continue
		}
		cursor, seen := cursors[session.ID]
		if !seen {
			// Cold start: seed at head and capture nothing. Reading from byte 0
			// would produce a capped whole-transcript read, which is not the
			// input this feature ever sees in production — the daemon seeds at
			// head too and waits for real movement. Rare states accumulate by
			// running `corpus` repeatedly, which is the intended workflow.
			head, err := activity.SeedCursor(path, string(session.Agent))
			if err != nil {
				fmt.Fprintf(os.Stderr, "skip %s: %v\n", session.ID, err)
				continue
			}
			cursors[session.ID] = head
			seeded++
			continue
		}
		window, err := activity.Read(path, string(session.Agent), cursor)
		if err != nil {
			// A mismatched cursor means the transcript was rewritten (Claude
			// compaction does this). Re-seed at head and skip this pass, exactly
			// as the daemon will.
			head, seedErr := activity.SeedCursor(path, string(session.Agent))
			if seedErr != nil {
				fmt.Fprintf(os.Stderr, "skip %s: %v\n", session.ID, err)
				continue
			}
			cursors[session.ID] = head
			continue
		}
		cursors[session.ID] = window.NextCursor
		if window.Empty() {
			continue
		}
		rendered := window.Render()
		entry := Entry{
			ID:          session.ID,
			CapturedAt:  session.StateSince,
			Agent:       string(session.Agent),
			State:       string(session.State),
			StateReason: derefString(session.StateReason),
			Previous:    previous[session.ID],
			Window:      rendered,
			Events:      len(window.Events),
			Chars:       len(rendered),
			Truncated:   window.Report.String(),
			Cursor:      window.NextCursor,
		}
		corpus = append(corpus, entry)
		added++
		if *verbose {
			fmt.Printf("%-16s %-18s %3d events %6d chars\n", entry.State, truncate(session.Label, 18), entry.Events, entry.Chars)
		}
	}

	if err := saveCorpus(*dir, corpus); err != nil {
		return err
	}
	if err := saveCursors(*dir, cursors); err != nil {
		return err
	}
	fmt.Printf("captured %d new entries (%d total)", added, len(corpus))
	if seeded > 0 {
		fmt.Printf("; seeded %d session(s) at head — run again after they move to capture their first window", seeded)
	}
	fmt.Println()
	printStateCoverage(corpus)
	return nil
}

// printStateCoverage reports the corpus's shape, because a corpus that is 95%
// `working` benchmarks one case well and everything else not at all. Rare states
// accumulate by running `corpus` over time.
func printStateCoverage(corpus []Entry) {
	byState := map[string]int{}
	withoutProse := 0
	for _, entry := range corpus {
		byState[entry.State]++
		if !strings.Contains(entry.Window, "thinking:") && !strings.Contains(entry.Window, "assistant:") {
			withoutProse++
		}
	}
	states := make([]string, 0, len(byState))
	for state := range byState {
		states = append(states, state)
	}
	sort.Strings(states)
	fmt.Println("\nstate coverage:")
	for _, state := range states {
		fmt.Printf("  %-18s %d\n", state, byState[state])
	}
	if len(corpus) > 0 {
		fmt.Printf("  %-18s %d (%d%%)\n", "(no prose)", withoutProse, 100*withoutProse/len(corpus))
	}
}

// transcriptPathFor locates a session's transcript. Claude transcripts are keyed
// by session id; the other agents are discovered by cwd and start time, which
// this harness does not track, so they are resolved by id only.
func transcriptPathFor(sessionID, agent string) string {
	if strings.EqualFold(agent, "claude") {
		return transcript.FindClaudeTranscript(sessionID)
	}
	return ""
}

func corpusPath(dir string) string  { return filepath.Join(dir, "corpus.jsonl") }
func cursorsPath(dir string) string { return filepath.Join(dir, "cursors.json") }

func loadCursors(dir string) (map[string]string, error) {
	data, err := os.ReadFile(cursorsPath(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	cursors := map[string]string{}
	if err := json.Unmarshal(data, &cursors); err != nil {
		return nil, fmt.Errorf("cursors file is corrupt at %s: %w", cursorsPath(dir), err)
	}
	return cursors, nil
}

func saveCursors(dir string, cursors map[string]string) error {
	data, err := json.MarshalIndent(cursors, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cursorsPath(dir), data, 0o600)
}

func loadCorpus(dir string) ([]Entry, error) {
	data, err := os.ReadFile(corpusPath(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var corpus []Entry
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry Entry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, fmt.Errorf("corpus is corrupt at %s: %w", corpusPath(dir), err)
		}
		corpus = append(corpus, entry)
	}
	return corpus, nil
}

func saveCorpus(dir string, corpus []Entry) error {
	var b strings.Builder
	for _, entry := range corpus {
		line, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return os.WriteFile(corpusPath(dir), []byte(b.String()), 0o600)
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
