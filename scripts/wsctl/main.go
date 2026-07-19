// wsctl is a tiny dev helper that drives the daemon's websocket directly.
//
// Until a real native UI lands with workspace + spawn affordances, the
// canvas spike (attn-spike5) has no way to create the workspaces or
// workspace-bound sessions it consumes. This script fills that gap.
//
// Target daemon resolution, in priority order:
//
//  1. ATTN_WS_URL — explicit URL, used verbatim (the only way to reach prod).
//  2. ATTN_PROFILE — the profile's derived WS port (same resolution as every
//     other attn entrypoint).
//  3. Fallback: the dev daemon (ws://localhost:29849/ws). wsctl never targets
//     prod implicitly.
//
// The resolved target is printed to stderr on every invocation.
//
// Usage:
//
//	go run ./scripts/wsctl add-workspace --title T --dir D [--id I]
//	go run ./scripts/wsctl rm-workspace --id I
//	go run ./scripts/wsctl add-session --workspace W --cwd D [--agent claude] [--label L] [--initial-prompt-file P] [--yolo] [--id I] [--cols 80] [--rows 24]
//	go run ./scripts/wsctl rm-session --id I
//	go run ./scripts/wsctl kill-session --id I [--reload]
//	go run ./scripts/wsctl screen --id I
//	go run ./scripts/wsctl input --id I --text T [--enter]
//	go run ./scripts/wsctl list
//	go run ./scripts/wsctl refresh-prs
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
	"nhooyr.io/websocket"
)

const defaultWSURL = "ws://localhost:29849/ws"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	fmt.Fprintf(os.Stderr, "[wsctl → %s]\n", wsURL())

	var err error
	switch cmd {
	case "add-workspace":
		err = addWorkspace(args)
	case "rm-workspace":
		err = rmWorkspace(args)
	case "add-session":
		err = addSession(args)
	case "rm-session":
		err = rmSession(args)
	case "kill-session":
		err = killSession(args)
	case "screen":
		err = screen(args)
	case "input":
		err = input(args)
	case "list":
		err = list(args)
	case "refresh-prs":
		err = refreshPRs(args)
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `wsctl — dev helper for driving the attn daemon over WebSocket.

URL: %s (ATTN_WS_URL > ATTN_PROFILE-derived port > dev; prod needs an explicit ATTN_WS_URL)

Commands:
  add-workspace --title T --dir D [--id I]
  rm-workspace --id I
  add-session --workspace W --cwd D [--agent claude] [--label L] [--initial-prompt-file P] [--yolo] [--id I] [--cols 80] [--rows 24]
  rm-session --id I
  kill-session --id I [--reload]
  screen --id I
  input --id I --text T [--enter]
  list
  refresh-prs
`, wsURL())
}

func wsURL() string {
	return resolveWSURL(os.Getenv("ATTN_WS_URL"), os.Getenv("ATTN_PROFILE"))
}

// resolveWSURL picks the daemon to talk to. An explicit ATTN_WS_URL always
// wins (and is the only way to target prod). Otherwise a named ATTN_PROFILE
// resolves to that profile's derived WS port, and with no profile at all we
// fall back to the dev daemon — never prod.
func resolveWSURL(explicitURL, profile string) string {
	if u := strings.TrimSpace(explicitURL); u != "" {
		return u
	}
	p := strings.ToLower(strings.TrimSpace(profile))
	if p != "" && p != "default" {
		return "ws://localhost:" + config.WSPortForProfile(p) + "/ws"
	}
	return defaultWSURL
}

// ── Subcommands ──────────────────────────────────────────────────────────────

func addWorkspace(args []string) error {
	fs := flag.NewFlagSet("add-workspace", flag.ExitOnError)
	title := fs.String("title", "", "workspace title (required)")
	dir := fs.String("dir", "", "workspace directory (required)")
	id := fs.String("id", "", "workspace id (defaults to a generated one)")
	fs.Parse(args)

	if *title == "" || *dir == "" {
		return errors.New("--title and --dir are required")
	}
	wsID := *id
	if wsID == "" {
		wsID = newID("ws")
	}
	abs, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	msg := map[string]any{
		"cmd":       "register_workspace",
		"id":        wsID,
		"title":     *title,
		"directory": abs,
	}
	if err := send(msg); err != nil {
		return err
	}
	fmt.Printf("workspace registered: id=%s title=%q dir=%s\n", wsID, *title, abs)
	return nil
}

func rmWorkspace(args []string) error {
	fs := flag.NewFlagSet("rm-workspace", flag.ExitOnError)
	id := fs.String("id", "", "workspace id (required)")
	fs.Parse(args)
	if *id == "" {
		return errors.New("--id is required")
	}
	msg := map[string]any{
		"cmd": "unregister_workspace",
		"id":  *id,
	}
	if err := send(msg); err != nil {
		return err
	}
	fmt.Printf("workspace unregistered: id=%s\n", *id)
	return nil
}

func addSession(args []string) error {
	fs := flag.NewFlagSet("add-session", flag.ExitOnError)
	workspace := fs.String("workspace", "", "owning workspace id (required)")
	cwd := fs.String("cwd", "", "session working directory (required)")
	agent := fs.String("agent", "claude", "agent: claude | codex | copilot | shell")
	label := fs.String("label", "", "session label (defaults to dir basename)")
	initialPromptFile := fs.String("initial-prompt-file", "", "file containing the initial agent prompt")
	yolo := fs.Bool("yolo", false, "launch with agent approval prompts bypassed")
	id := fs.String("id", "", "session id (defaults to a generated one)")
	cols := fs.Int("cols", 80, "initial PTY cols")
	rows := fs.Int("rows", 24, "initial PTY rows")
	fs.Parse(args)

	if *workspace == "" || *cwd == "" {
		return errors.New("--workspace and --cwd are required")
	}
	abs, err := filepath.Abs(*cwd)
	if err != nil {
		return err
	}
	sessID := *id
	if sessID == "" {
		// Claude Code (and likely other agents) reject non-UUID session
		// ids — the agent CLI uses them directly as its own session
		// identifier, which must be UUID-shaped.
		sessID = newUUID()
	}

	// The daemon guarantees a workspace layout pane for every spawned session
	// (spawn_session ensures one, adopting a pre-created pane when present),
	// so a bare spawn is all a script needs for the session to render.
	msg := map[string]any{
		"cmd":          "spawn_session",
		"id":           sessID,
		"cwd":          abs,
		"workspace_id": *workspace,
		"agent":        *agent,
		"cols":         *cols,
		"rows":         *rows,
	}
	if *label != "" {
		msg["label"] = *label
	}
	if *initialPromptFile != "" {
		content, err := os.ReadFile(*initialPromptFile)
		if err != nil {
			return fmt.Errorf("read initial prompt file: %w", err)
		}
		msg["initial_prompt"] = string(content)
	}
	if *yolo {
		msg["yolo_mode"] = true
	}

	// Spawn replies with a SpawnResult — wait briefly for it so we
	// can surface failures (bad cwd, unknown agent, etc.) instead of
	// printing "ok" and leaving the user to wonder why nothing
	// appeared. The pane only exists once spawn succeeds (the daemon
	// ensures it on the success path), so a failure here leaves nothing
	// to roll back.
	resp, err := sendAndWait(msg, "spawn_result", sessID, 30*time.Second)
	if err != nil {
		return err
	}
	if resp != nil {
		success, _ := resp["success"].(bool)
		if !success {
			errMsg, _ := resp["error"].(string)
			return fmt.Errorf("daemon rejected spawn: %s", errMsg)
		}
	}
	fmt.Printf("session spawned: id=%s workspace=%s agent=%s cwd=%s\n", sessID, *workspace, *agent, abs)
	return nil
}

func rmSession(args []string) error {
	fs := flag.NewFlagSet("rm-session", flag.ExitOnError)
	id := fs.String("id", "", "session id (required)")
	fs.Parse(args)
	if *id == "" {
		return errors.New("--id is required")
	}
	// `unregister` SIGTERMs the agent process AND removes the session
	// record from the daemon's store. `kill_session` only does the
	// first half — if the agent is already dead, kill_session is a
	// no-op and the session lingers as a ghost.
	msg := map[string]any{
		"cmd": "unregister",
		"id":  *id,
	}
	if err := send(msg); err != nil {
		return err
	}
	fmt.Printf("session removed: id=%s\n", *id)
	return nil
}

func killSession(args []string) error {
	fs := flag.NewFlagSet("kill-session", flag.ExitOnError)
	id := fs.String("id", "", "session id (required)")
	reload := fs.Bool("reload", false, "mark the kill as the first half of an in-place reload")
	fs.Parse(args)
	if *id == "" {
		return errors.New("--id is required")
	}
	msg := map[string]any{
		"cmd": "kill_session",
		"id":  *id,
	}
	if *reload {
		msg["reload"] = true
	}
	if err := send(msg); err != nil {
		return err
	}
	fmt.Printf("session killed: id=%s reload=%t\n", *id, *reload)
	return nil
}

func screen(args []string) error {
	fs := flag.NewFlagSet("screen", flag.ExitOnError)
	id := fs.String("id", "", "session id (required)")
	fs.Parse(args)
	if *id == "" {
		return errors.New("--id is required")
	}
	resp, err := sendAndWait(map[string]any{
		"cmd": "get_screen_snapshot",
		"id":  *id,
	}, "get_screen_snapshot_result", *id, 5*time.Second)
	if err != nil {
		return err
	}
	if success, _ := resp["success"].(bool); !success {
		errMsg, _ := resp["error"].(string)
		return fmt.Errorf("snapshot failed: %s", errMsg)
	}
	encoded, _ := resp["screen_snapshot"].(string)
	if encoded == "" {
		return errors.New("snapshot contained no screen")
	}
	content, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("decode screen snapshot: %w", err)
	}
	_, err = os.Stdout.Write(content)
	return err
}

func input(args []string) error {
	fs := flag.NewFlagSet("input", flag.ExitOnError)
	id := fs.String("id", "", "session id (required)")
	text := fs.String("text", "", "text to send")
	enter := fs.Bool("enter", false, "append carriage return")
	fs.Parse(args)
	if *id == "" {
		return errors.New("--id is required")
	}
	data := *text
	if *enter {
		data += "\r"
	}
	if data == "" {
		return errors.New("--text or --enter is required")
	}
	return send(map[string]any{
		"cmd":    "pty_input",
		"id":     *id,
		"data":   data,
		"source": "wsctl",
	})
}

func list(_ []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(), nil)
	if err != nil {
		return fmt.Errorf("dial %s: %w", wsURL(), err)
	}
	// The daemon broadcasts full state (sessions, PRs with details, tickets);
	// the library's 32 KiB default read limit kills the connection mid-frame.
	conn.SetReadLimit(16 << 20)
	defer conn.Close(websocket.StatusNormalClosure, "")

	// First message after connect is initial_state.
	_, data, err := conn.Read(ctx)
	if err != nil {
		return fmt.Errorf("read initial_state: %w", err)
	}
	var event map[string]any
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	if ev, _ := event["event"].(string); ev != "initial_state" {
		return fmt.Errorf("expected initial_state, got %q", ev)
	}
	if err := sendClientHello(ctx, conn); err != nil {
		return err
	}
	pretty, _ := json.MarshalIndent(map[string]any{
		"workspaces": event["workspaces"],
		"sessions":   event["sessions"],
	}, "", "  ")
	fmt.Println(string(pretty))
	return nil
}

func refreshPRs(args []string) error {
	if len(args) != 0 {
		return errors.New("refresh-prs takes no arguments")
	}
	response, err := sendAndWait(map[string]any{"cmd": "refresh_prs"}, "refresh_prs_result", "", 60*time.Second)
	if err != nil {
		return err
	}
	if success, _ := response["success"].(bool); !success {
		message, _ := response["error"].(string)
		return fmt.Errorf("refresh PRs failed: %s", message)
	}
	fmt.Println("PR refresh completed")
	return nil
}

// ── Wire helpers ─────────────────────────────────────────────────────────────

// send opens a connection, drains the initial_state event, sends the
// payload, and closes. No response read — fire-and-forget.
func send(payload map[string]any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(), nil)
	if err != nil {
		return fmt.Errorf("dial %s: %w", wsURL(), err)
	}
	// The daemon broadcasts full state (sessions, PRs with details, tickets);
	// the library's 32 KiB default read limit kills the connection mid-frame.
	conn.SetReadLimit(16 << 20)
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Daemon sends initial_state on connect; drain it before our write
	// so the socket buffer doesn't backlog.
	if _, _, err := conn.Read(ctx); err != nil {
		return fmt.Errorf("drain initial_state: %w", err)
	}
	if err := sendClientHello(ctx, conn); err != nil {
		return err
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err := conn.Write(ctx, websocket.MessageText, body); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	// Tiny grace window so the daemon has time to enqueue the work
	// before we hang up. Without this, fast-fire calls can race the
	// daemon's read loop on connection close.
	time.Sleep(150 * time.Millisecond)
	return nil
}

// sendAndWait sends the payload, then reads frames until it sees an
// event of the given type (filtered by id when provided) or the
// timeout elapses. Other frames are silently dropped.
func sendAndWait(payload map[string]any, expectedEvent, expectedID string, timeout time.Duration) (map[string]any, error) {
	return sendAndWaitMatch(payload, expectedEvent, func(ev map[string]any) bool {
		if expectedID == "" {
			return true
		}
		id, _ := ev["id"].(string)
		return id == expectedID
	}, timeout)
}

// sendAndWaitMatch is sendAndWait with an arbitrary predicate over events of
// the expected type, for results that carry no top-level "id" field (e.g.
// workspace_layout_action_result).
func sendAndWaitMatch(payload map[string]any, expectedEvent string, match func(map[string]any) bool, timeout time.Duration) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout+3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(), nil)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", wsURL(), err)
	}
	conn.SetReadLimit(16 << 20)
	defer conn.Close(websocket.StatusNormalClosure, "")

	if _, _, err := conn.Read(ctx); err != nil {
		return nil, fmt.Errorf("drain initial_state: %w", err)
	}
	if err := sendClientHello(ctx, conn); err != nil {
		return nil, err
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if err := conn.Write(ctx, websocket.MessageText, body); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}

	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, fmt.Errorf("timed out waiting for %s", expectedEvent)
		}
		readCtx, readCancel := context.WithTimeout(ctx, remaining)
		_, data, err := conn.Read(readCtx)
		readCancel()
		if err != nil {
			return nil, fmt.Errorf("read: %w", err)
		}
		var event map[string]any
		if err := json.Unmarshal(data, &event); err != nil {
			continue
		}
		if os.Getenv("WSCTL_TRACE") != "" {
			snippet := string(data)
			if len(snippet) > 300 {
				snippet = snippet[:300]
			}
			fmt.Fprintf(os.Stderr, "<- %s\n", snippet)
		}
		if event["event"] == expectedEvent && match(event) {
			return event, nil
		}
	}
}

func sendClientHello(ctx context.Context, conn *websocket.Conn) error {
	body, err := json.Marshal(map[string]any{
		"cmd":          "client_hello",
		"client_kind":  "wsctl",
		"version":      "protocol-" + protocol.ProtocolVersion,
		"capabilities": []string{protocol.CapabilityWorkspaceSessions},
	})
	if err != nil {
		return err
	}
	if err := conn.Write(ctx, websocket.MessageText, body); err != nil {
		return fmt.Errorf("write client_hello: %w", err)
	}
	return nil
}

func newID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

// newUUID returns a random RFC 4122 v4 UUID string. We can't use a
// timestamp-prefixed id for sessions because the agent CLI we hand the
// id to (e.g. claude) parses it as a UUID and rejects anything else.
func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Fall back to a timestamp-derived id; agent CLI will then
		// reject the spawn, which is a clearer failure than silently
		// generating a non-random "uuid".
		return newID("sess")
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
