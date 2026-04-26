// wsctl is a tiny dev helper that drives the daemon's websocket directly.
//
// Until a real native UI lands with workspace + spawn affordances, the
// canvas spike (attn-spike5) has no way to create the workspaces or
// workspace-bound sessions it consumes. This script fills that gap.
//
// Defaults to the dev daemon (ws://localhost:29849/ws). Override with
// ATTN_WS_URL.
//
// Usage:
//
//	go run ./scripts/wsctl add-workspace --title T --dir D [--id I]
//	go run ./scripts/wsctl rm-workspace --id I
//	go run ./scripts/wsctl add-session --workspace W --cwd D [--agent claude] [--label L] [--id I] [--cols 80] [--rows 24]
//	go run ./scripts/wsctl rm-session --id I
//	go run ./scripts/wsctl list
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

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
	case "list":
		err = list(args)
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

URL: %s (override with ATTN_WS_URL)

Commands:
  add-workspace --title T --dir D [--id I]
  rm-workspace --id I
  add-session --workspace W --cwd D [--agent claude] [--label L] [--id I] [--cols 80] [--rows 24]
  rm-session --id I
  list
`, wsURL())
}

func wsURL() string {
	if u := os.Getenv("ATTN_WS_URL"); u != "" {
		return u
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

	// Spawn replies with a SpawnResult — wait briefly for it so we
	// can surface failures (bad cwd, unknown agent, etc.) instead of
	// printing "ok" and leaving the user to wonder why nothing
	// appeared.
	resp, err := sendAndWait(msg, "spawn_result", sessID, 2*time.Second)
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
	msg := map[string]any{
		"cmd": "kill_session",
		"id":  *id,
	}
	if err := send(msg); err != nil {
		return err
	}
	fmt.Printf("session killed: id=%s\n", *id)
	return nil
}

func list(_ []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(), nil)
	if err != nil {
		return fmt.Errorf("dial %s: %w", wsURL(), err)
	}
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
	pretty, _ := json.MarshalIndent(map[string]any{
		"workspaces": event["workspaces"],
		"sessions":   event["sessions"],
	}, "", "  ")
	fmt.Println(string(pretty))
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
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Daemon sends initial_state on connect; drain it before our write
	// so the socket buffer doesn't backlog.
	if _, _, err := conn.Read(ctx); err != nil {
		return fmt.Errorf("drain initial_state: %w", err)
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
	ctx, cancel := context.WithTimeout(context.Background(), timeout+3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(), nil)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", wsURL(), err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if _, _, err := conn.Read(ctx); err != nil {
		return nil, fmt.Errorf("drain initial_state: %w", err)
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
		if event["event"] == expectedEvent {
			if expectedID == "" {
				return event, nil
			}
			if id, _ := event["id"].(string); id == expectedID {
				return event, nil
			}
		}
	}
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
