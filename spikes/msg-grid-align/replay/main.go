// Command replay is SPIKE-ONLY throwaway tooling. Live capture only sees the
// visible frame of a currently-running session, which left Codex unmeasured and
// gave us no real scrollback. The daemon's archived PTY captures
// (<data-dir>/workers/<instance>/captures/*.jsonl) hold raw output bytes around
// a state transition, so replaying them through the same VT core reconstructs a
// real grid WITH scrollback, for sessions that are long gone.
//
// Two things are unknown in an archived capture: the terminal width it was
// rendered at, and which transcript it belongs to. Both are recovered by search:
// every (width, transcript) candidate is aligned and the best-scoring pair wins.
// A wrong transcript scores near zero, so a high score validates the pairing
// rather than assuming it.
//
//	go run ./spikes/msg-grid-align/replay -capture <path.jsonl> -name codex-real
package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/ghosttyvt"
	"github.com/victorarias/attn/internal/transcript"
	msgalign "github.com/victorarias/attn/spikes/msg-grid-align"
)

type captureMeta struct {
	Agent     string `json:"agent"`
	Kind      string `json:"kind"`
	Reason    string `json:"reason"`
	SessionID string `json:"session_id"`
	Time      string `json:"time"`
}

type captureEvent struct {
	Kind    string `json:"kind"`
	DataB64 string `json:"data_b64"`
}

type Fixture struct {
	Name           string   `json:"name"`
	SessionID      string   `json:"session_id"`
	Agent          string   `json:"agent"`
	Label          string   `json:"label"`
	Directory      string   `json:"directory"`
	CapturedAt     string   `json:"captured_at"`
	Cols           int      `json:"cols"`
	Rows           int      `json:"rows"`
	ANSIB64        string   `json:"ansi_b64"`
	GridRows       []string `json:"grid_rows"`
	ViewportRows   []string `json:"viewport_rows"`
	Markdown       string   `json:"markdown"`
	TranscriptPath string   `json:"transcript_path"`
}

func main() {
	capturePath := flag.String("capture", "", "capture jsonl path")
	name := flag.String("name", "", "fixture name")
	outDir := flag.String("out", "spikes/msg-grid-align/testdata", "fixture output directory")
	rows := flag.Int("rows", 60, "replay terminal height")
	transcriptGlob := flag.String("transcripts", "", "override candidate transcript glob")
	minScore := flag.Float64("min-score", 0.5, "reject the best pairing below this recall")
	flag.Parse()

	if err := run(*capturePath, *name, *outDir, *rows, *transcriptGlob, *minScore); err != nil {
		fmt.Fprintf(os.Stderr, "replay: %v\n", err)
		os.Exit(1)
	}
}

var candidateWidths = []int{80, 100, 120, 140, 160, 180, 200, 209, 240, 307}

func run(capturePath, name, outDir string, rows int, transcriptGlob string, minScore float64) error {
	if capturePath == "" {
		return fmt.Errorf("-capture is required")
	}
	meta, outputs, err := readCapture(capturePath)
	if err != nil {
		return err
	}
	fmt.Printf("capture: agent=%s reason=%s time=%s output_events=%d\n",
		meta.Agent, meta.Reason, meta.Time, len(outputs))

	candidates, err := candidateTranscripts(meta, transcriptGlob)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		return fmt.Errorf("no candidate transcripts found for %s", meta.Time)
	}
	fmt.Printf("candidate transcripts: %d\n", len(candidates))

	// Pre-extract each candidate's last assistant message once.
	type cand struct {
		path     string
		markdown string
	}
	var cands []cand
	for _, p := range candidates {
		md, err := transcript.ExtractLastAssistantMessage(p, 1<<20)
		if err != nil || len(strings.TrimSpace(md)) < 200 {
			continue // too short to measure anything with
		}
		cands = append(cands, cand{path: p, markdown: md})
	}
	if len(cands) == 0 {
		return fmt.Errorf("no candidate transcript had a substantial last assistant message")
	}
	fmt.Printf("candidates with a substantial message: %d\n", len(cands))

	type best struct {
		score      float64
		width      int
		markdown   string
		path       string
		grid       []string
		viewport   []string
		matched    int
		srcTokens  int
		gridTokens int
	}
	var b best

	for _, w := range candidateWidths {
		grid, viewport, err := renderGrid(outputs, w, rows)
		if err != nil {
			return err
		}
		for _, c := range cands {
			a := msgalign.Align(c.markdown, grid)
			if len(a.Src) == 0 {
				continue
			}
			// Score on source recall: how much of the message we located.
			score := float64(len(a.Pairs)) / float64(len(a.Src))
			if score > b.score {
				b = best{
					score: score, width: w, markdown: c.markdown, path: c.path,
					grid: grid, viewport: viewport,
					matched: len(a.Pairs), srcTokens: len(a.Src), gridTokens: len(a.Grid),
				}
			}
		}
	}

	fmt.Printf("best pairing: width=%d score=%.1f%% (%d/%d source tokens located)\n  transcript=%s\n",
		b.width, b.score*100, b.matched, b.srcTokens, b.path)

	if b.score < minScore {
		return fmt.Errorf("best pairing scored %.1f%%, below -min-score %.1f%%; treating codex as unmeasured rather than publishing a guess",
			b.score*100, minScore*100)
	}

	if name == "" {
		name = fmt.Sprintf("%s-replay", meta.Agent)
	}
	joined := strings.Join(outputs, "")
	fx := Fixture{
		Name:           name,
		SessionID:      meta.SessionID,
		Agent:          meta.Agent,
		Label:          "replayed from " + filepath.Base(capturePath),
		CapturedAt:     meta.Time,
		Cols:           b.width,
		Rows:           rows,
		ANSIB64:        base64.StdEncoding.EncodeToString([]byte(joined)),
		GridRows:       b.grid,
		ViewportRows:   b.viewport,
		Markdown:       b.markdown,
		TranscriptPath: b.path,
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	out := filepath.Join(outDir, name+".json")
	blob, err := json.MarshalIndent(fx, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, blob, 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s (grid_rows=%d incl scrollback, markdown_chars=%d)\n", out, len(b.grid), len(b.markdown))
	return nil
}

func readCapture(path string) (captureMeta, []string, error) {
	blob, err := os.ReadFile(path)
	if err != nil {
		return captureMeta{}, nil, err
	}
	var meta captureMeta
	var outputs []string
	for i, line := range strings.Split(string(blob), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if i == 0 {
			if err := json.Unmarshal([]byte(line), &meta); err != nil {
				return captureMeta{}, nil, fmt.Errorf("parse capture meta: %w", err)
			}
			continue
		}
		var ev captureEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if ev.Kind != "output" || ev.DataB64 == "" {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(ev.DataB64)
		if err != nil {
			continue
		}
		outputs = append(outputs, string(raw))
	}
	return meta, outputs, nil
}

func candidateTranscripts(meta captureMeta, override string) ([]string, error) {
	if override != "" {
		return filepath.Glob(override)
	}
	ts, err := time.Parse(time.RFC3339Nano, meta.Time)
	if err != nil {
		return nil, fmt.Errorf("parse capture time %q: %w", meta.Time, err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	var globs []string
	switch meta.Agent {
	case "codex":
		// The capture window can straddle midnight; take the day and the one before.
		for _, d := range []time.Time{ts, ts.AddDate(0, 0, -1)} {
			globs = append(globs, filepath.Join(home, ".codex", "sessions",
				d.Format("2006"), d.Format("01"), d.Format("02"), "*.jsonl"))
		}
	case "claude":
		globs = append(globs, filepath.Join(home, ".claude", "projects", "*", "*.jsonl"))
	default:
		return nil, fmt.Errorf("no transcript location known for agent %q", meta.Agent)
	}

	var out []string
	for _, g := range globs {
		matches, err := filepath.Glob(g)
		if err != nil {
			return nil, err
		}
		out = append(out, matches...)
	}
	sort.Strings(out)
	return out, nil
}

func renderGrid(outputs []string, cols, rows int) (grid, viewport []string, err error) {
	term, err := ghosttyvt.New(cols, rows, ghosttyvt.Options{})
	if err != nil {
		return nil, nil, err
	}
	defer term.Close()
	for _, chunk := range outputs {
		term.Write([]byte(chunk))
	}
	return splitRows(term.PlainText()), splitRows(term.ViewportText()), nil
}

func splitRows(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
