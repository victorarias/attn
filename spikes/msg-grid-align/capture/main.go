// Command capture is SPIKE-ONLY throwaway tooling. It builds (rendered grid,
// transcript markdown) fixture pairs from live sessions so the aligner in the
// parent package can be iterated offline.
//
// It talks to the daemon over the WebSocket and only ever sends
// get_screen_snapshot, which is read-only: it registers no subscriber and
// claims no PTY geometry.
//
//	go run ./spikes/msg-grid-align/capture -list
//	go run ./spikes/msg-grid-align/capture -session <id> -name plain-prose
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/ghosttyvt"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

// Fixture is one captured case. ANSIB64 is kept so the grid can be re-rendered
// at other geometries for the stability perturbations.
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

type session struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Agent     string `json:"agent"`
	Directory string `json:"directory"`
	State     string `json:"state"`
}

type envelope struct {
	Event    string    `json:"event"`
	Sessions []session `json:"sessions"`

	// get_screen_snapshot_result
	ID             string  `json:"id"`
	Success        bool    `json:"success"`
	ScreenSnapshot *string `json:"screen_snapshot"`
	ScreenRows     *int    `json:"screen_rows"`
	ScreenCols     *int    `json:"screen_cols"`
	Cols           *int    `json:"cols"`
	Rows           *int    `json:"rows"`
	Running        *bool   `json:"running"`
	Error          *string `json:"error"`
}

func main() {
	list := flag.Bool("list", false, "list sessions and exit")
	sessionID := flag.String("session", "", "session id to snapshot")
	name := flag.String("name", "", "fixture name (defaults to <agent>-<short id>)")
	transcriptPath := flag.String("transcript", "", "override transcript path (needed for codex/copilot)")
	url := flag.String("ws", "ws://localhost:9849/ws", "daemon websocket url")
	outDir := flag.String("out", "spikes/msg-grid-align/testdata", "fixture output directory")
	flag.Parse()

	if err := run(*list, *sessionID, *name, *transcriptPath, *url, *outDir); err != nil {
		fmt.Fprintf(os.Stderr, "capture: %v\n", err)
		os.Exit(1)
	}
}

func run(list bool, sessionID, name, transcriptOverride, url, outDir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return fmt.Errorf("dial %s: %w", url, err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(64 << 20)

	// The daemon only accepts workspace-first clients; announce that before
	// anything else or it closes the connection with a policy violation.
	hello, err := json.Marshal(map[string]any{
		"cmd":          "client_hello",
		"client_kind":  "spike-msg-grid-align",
		"version":      "protocol-" + protocol.ProtocolVersion,
		"capabilities": []string{protocol.CapabilityWorkspaceSessions},
	})
	if err != nil {
		return err
	}
	if err := conn.Write(ctx, websocket.MessageText, hello); err != nil {
		return fmt.Errorf("send client_hello: %w", err)
	}

	sessions, err := waitForSessions(ctx, conn)
	if err != nil {
		return err
	}

	if list {
		printSessions(sessions)
		return nil
	}
	if sessionID == "" {
		printSessions(sessions)
		return fmt.Errorf("-session is required (or use -list)")
	}

	target, ok := findSession(sessions, sessionID)
	if !ok {
		return fmt.Errorf("session %s not found in daemon session list", sessionID)
	}

	snap, err := requestSnapshot(ctx, conn, sessionID)
	if err != nil {
		return err
	}

	ansi, err := base64.StdEncoding.DecodeString(*snap.ScreenSnapshot)
	if err != nil {
		return fmt.Errorf("decode screen_snapshot: %w", err)
	}

	cols, rows := derefOr(snap.ScreenCols, derefOr(snap.Cols, 120)), derefOr(snap.ScreenRows, derefOr(snap.Rows, 40))
	gridRows, viewportRows, err := renderGrid(ansi, cols, rows)
	if err != nil {
		return err
	}

	tPath := transcriptOverride
	if tPath == "" {
		tPath = resolveTranscript(target)
	}
	if tPath == "" {
		return fmt.Errorf("could not resolve a transcript for agent %q; pass -transcript", target.Agent)
	}
	markdown, err := lastAssistantMarkdown(tPath)
	if err != nil {
		return fmt.Errorf("read transcript %s: %w", tPath, err)
	}
	if strings.TrimSpace(markdown) == "" {
		return fmt.Errorf("transcript %s yielded no assistant message", tPath)
	}

	if name == "" {
		name = fmt.Sprintf("%s-%s", target.Agent, shortID(sessionID))
	}
	fx := Fixture{
		Name:           name,
		SessionID:      sessionID,
		Agent:          target.Agent,
		Label:          target.Label,
		Directory:      target.Directory,
		CapturedAt:     time.Now().UTC().Format(time.RFC3339),
		Cols:           cols,
		Rows:           rows,
		ANSIB64:        *snap.ScreenSnapshot,
		GridRows:       gridRows,
		ViewportRows:   viewportRows,
		Markdown:       markdown,
		TranscriptPath: tPath,
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

	fmt.Printf("wrote %s\n", out)
	fmt.Printf("  agent=%s geometry=%dx%d grid_rows=%d viewport_rows=%d markdown_chars=%d\n",
		fx.Agent, cols, rows, len(gridRows), len(viewportRows), len(markdown))
	return nil
}

func waitForSessions(ctx context.Context, conn *websocket.Conn) ([]session, error) {
	deadline, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		var env envelope
		raw, err := readMessage(deadline, conn)
		if err != nil {
			return nil, fmt.Errorf("waiting for sessions: %w", err)
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			continue
		}
		if len(env.Sessions) > 0 {
			return env.Sessions, nil
		}
	}
}

func requestSnapshot(ctx context.Context, conn *websocket.Conn, id string) (*envelope, error) {
	req, err := json.Marshal(map[string]string{"cmd": "get_screen_snapshot", "id": id})
	if err != nil {
		return nil, err
	}
	if err := conn.Write(ctx, websocket.MessageText, req); err != nil {
		return nil, fmt.Errorf("send get_screen_snapshot: %w", err)
	}

	deadline, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for {
		raw, err := readMessage(deadline, conn)
		if err != nil {
			return nil, fmt.Errorf("waiting for get_screen_snapshot_result: %w", err)
		}
		var env envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			continue
		}
		if env.Event != "get_screen_snapshot_result" || env.ID != id {
			continue
		}
		if !env.Success {
			msg := "unknown error"
			if env.Error != nil {
				msg = *env.Error
			}
			return nil, fmt.Errorf("snapshot failed: %s", msg)
		}
		if env.ScreenSnapshot == nil || *env.ScreenSnapshot == "" {
			return nil, fmt.Errorf("snapshot succeeded but carried no screen_snapshot (session may not have a live worker)")
		}
		return &env, nil
	}
}

func readMessage(ctx context.Context, conn *websocket.Conn) ([]byte, error) {
	_, raw, err := conn.Read(ctx)
	return raw, err
}

// renderGrid feeds the ANSI replay through the same VT core production uses, so
// the rows here are exactly the rows the app shows.
func renderGrid(ansi []byte, cols, rows int) (grid []string, viewport []string, err error) {
	term, err := ghosttyvt.New(cols, rows, ghosttyvt.Options{})
	if err != nil {
		return nil, nil, fmt.Errorf("ghosttyvt.New(%d,%d): %w", cols, rows, err)
	}
	defer term.Close()

	term.Write(ansi)
	return splitRows(term.PlainText()), splitRows(term.ViewportText()), nil
}

func splitRows(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func resolveTranscript(s session) string {
	switch s.Agent {
	case "claude":
		return transcript.FindClaudeTranscript(s.ID)
	case "codex":
		return transcript.FindCodexTranscript(s.Directory, time.Time{})
	case "copilot":
		return transcript.FindCopilotTranscript(s.Directory, time.Time{})
	default:
		return ""
	}
}

// lastAssistantMarkdown prefers the after-last-user reading (what an annotate
// action would actually target) and falls back to the plain last message so a
// mid-turn capture is still usable as a fixture.
func lastAssistantMarkdown(path string) (string, error) {
	const maxChars = 1 << 20
	turn, err := transcript.ExtractLastAssistantTurnAfterLastUserSince(path, maxChars, time.Time{})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(turn.Content) != "" {
		return turn.Content, nil
	}
	return transcript.ExtractLastAssistantMessage(path, maxChars)
}

func findSession(sessions []session, id string) (session, bool) {
	for _, s := range sessions {
		if s.ID == id {
			return s, true
		}
	}
	return session{}, false
}

func printSessions(sessions []session) {
	fmt.Printf("%-38s %-9s %-16s %s\n", "ID", "AGENT", "STATE", "LABEL")
	for _, s := range sessions {
		fmt.Printf("%-38s %-9s %-16s %s\n", s.ID, s.Agent, s.State, s.Label)
	}
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func derefOr(p *int, fallback int) int {
	if p != nil && *p > 0 {
		return *p
	}
	return fallback
}
