package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/wrapper"
	"nhooyr.io/websocket"
)

type harnessWriter struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (w *harnessWriter) send(command map[string]interface{}) error {
	payload, err := json.Marshal(command)
	if err != nil {
		return err
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return w.conn.Write(ctx, websocket.MessageText, payload)
}

func runHarness() {
	fs := flag.NewFlagSet("harness", flag.ContinueOnError)
	agent := fs.String("agent", "codex", "agent to run: codex|copilot|claude")
	cwdFlag := fs.String("cwd", "", "working directory (defaults to current dir)")
	label := fs.String("label", "", "session label")
	sessionID := fs.String("session-id", "", "session id (defaults to generated id)")
	cols := fs.Int("cols", 120, "terminal columns")
	rows := fs.Int("rows", 32, "terminal rows")
	wsURL := fs.String("ws-url", "", "daemon websocket URL (default ws://127.0.0.1:<ATTN_WS_PORT>/ws)")
	keepSession := fs.Bool("keep-session", false, "keep session running on harness exit")
	claudeExec := fs.String("claude-exec", "", "override claude executable")
	codexExec := fs.String("codex-exec", "", "override codex executable")
	copilotExec := fs.String("copilot-exec", "", "override copilot executable")
	if err := fs.Parse(os.Args[2:]); err != nil {
		os.Exit(2)
	}

	cwd := *cwdFlag
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "harness: getwd failed: %v\n", err)
			os.Exit(1)
		}
	}
	cwd = filepath.Clean(cwd)

	if *label == "" {
		*label = filepath.Base(cwd)
	}
	if *sessionID == "" {
		*sessionID = wrapper.GenerateSessionID()
	}

	normalizedAgent := protocol.NormalizeSpawnAgent(*agent, string(protocol.SessionAgentCodex))
	if normalizedAgent == protocol.AgentShellValue {
		fmt.Fprintln(os.Stderr, "harness: shell agent is not supported; use codex/copilot/claude")
		os.Exit(1)
	}

	if *wsURL == "" {
		port := os.Getenv("ATTN_WS_PORT")
		if port == "" {
			port = "9849"
		}
		*wsURL = "ws://127.0.0.1:" + port + "/ws"
	}

	c := client.New("")
	if !c.IsRunning() {
		if err := startDaemonBackground(); err != nil {
			fmt.Fprintf(os.Stderr, "harness: failed to start daemon: %v\n", err)
			os.Exit(1)
		}
		deadline := time.Now().Add(5 * time.Second)
		for !c.IsRunning() && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
		}
		if !c.IsRunning() {
			fmt.Fprintln(os.Stderr, "harness: daemon did not become ready")
			os.Exit(1)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	conn, _, err := websocket.Dial(ctx, *wsURL, nil)
	cancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "harness: websocket dial failed: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if !*keepSession {
		defer func() {
			if err := c.Unregister(*sessionID); err != nil {
				fmt.Fprintf(os.Stderr, "\nharness: cleanup unregister failed for %s: %v\n", *sessionID, err)
			}
		}()
	}

	fmt.Fprintf(
		os.Stderr,
		"harness: session=%s agent=%s cwd=%s ws=%s\n",
		*sessionID,
		normalizedAgent,
		cwd,
		*wsURL,
	)

	writer := &harnessWriter{conn: conn}
	spawnResult := make(chan error, 1)
	attachResult := make(chan error, 1)
	exited := make(chan struct{})

	go runHarnessReadLoop(conn, *sessionID, spawnResult, attachResult, exited)

	spawn := map[string]interface{}{
		"cmd":   protocol.CmdSpawnSession,
		"id":    *sessionID,
		"cwd":   cwd,
		"agent": normalizedAgent,
		"label": *label,
		"cols":  *cols,
		"rows":  *rows,
	}
	if *claudeExec != "" {
		spawn["claude_executable"] = *claudeExec
	}
	if *codexExec != "" {
		spawn["codex_executable"] = *codexExec
	}
	if *copilotExec != "" {
		spawn["copilot_executable"] = *copilotExec
	}

	if err := writer.send(spawn); err != nil {
		fmt.Fprintf(os.Stderr, "harness: spawn command failed: %v\n", err)
		os.Exit(1)
	}

	select {
	case err := <-spawnResult:
		if err != nil {
			fmt.Fprintf(os.Stderr, "harness: spawn failed: %v\n", err)
			os.Exit(1)
		}
	case <-time.After(30 * time.Second):
		fmt.Fprintln(os.Stderr, "harness: timed out waiting for spawn_result")
		os.Exit(1)
	}

	if err := writer.send(map[string]interface{}{
		"cmd": protocol.CmdAttachSession,
		"id":  *sessionID,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "harness: attach command failed: %v\n", err)
		os.Exit(1)
	}

	select {
	case err := <-attachResult:
		if err != nil {
			fmt.Fprintf(os.Stderr, "harness: attach failed: %v\n", err)
			os.Exit(1)
		}
	case <-time.After(15 * time.Second):
		fmt.Fprintln(os.Stderr, "harness: timed out waiting for attach_result")
		os.Exit(1)
	}

	go runHarnessInputLoop(writer, *sessionID)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case <-exited:
		fmt.Fprintln(os.Stderr, "\nharness: session exited")
	case sig := <-sigCh:
		fmt.Fprintf(os.Stderr, "\nharness: received signal %s\n", sig)
		_ = writer.send(map[string]interface{}{
			"cmd":    protocol.CmdKillSession,
			"id":     *sessionID,
			"signal": "TERM",
		})
		select {
		case <-exited:
		case <-time.After(3 * time.Second):
		}
	}
}

func runHarnessReadLoop(
	conn *websocket.Conn,
	sessionID string,
	spawnResult chan<- error,
	attachResult chan<- error,
	exited chan<- struct{},
) {
	var (
		spawnSent  bool
		attachSent bool
		exitOnce   sync.Once
	)

	closeExited := func() {
		exitOnce.Do(func() {
			close(exited)
		})
	}

	for {
		_, payload, err := conn.Read(context.Background())
		if err != nil {
			closeExited()
			return
		}

		var evt map[string]interface{}
		if err := json.Unmarshal(payload, &evt); err != nil {
			continue
		}
		eventName := asString(evt["event"])
		switch eventName {
		case protocol.EventSpawnResult:
			if asString(evt["id"]) != sessionID {
				continue
			}
			if !spawnSent {
				spawnSent = true
				if asBool(evt["success"]) {
					spawnResult <- nil
				} else {
					spawnResult <- fmt.Errorf("%s", asString(evt["error"]))
				}
			}
		case protocol.EventAttachResult:
			if asString(evt["id"]) != sessionID {
				continue
			}
			if !attachSent {
				attachSent = true
				if asBool(evt["success"]) {
					attachResult <- nil
				} else {
					attachResult <- fmt.Errorf("%s", asString(evt["error"]))
				}
			}
		case protocol.EventPtyOutput:
			if asString(evt["id"]) != sessionID {
				continue
			}
			raw := asString(evt["data"])
			if raw == "" {
				continue
			}
			decoded, err := base64.StdEncoding.DecodeString(raw)
			if err != nil {
				continue
			}
			_, _ = os.Stdout.Write(decoded)
		case protocol.EventSessionStateChanged:
			session, _ := evt["session"].(map[string]interface{})
			if session == nil {
				continue
			}
			if asString(session["id"]) != sessionID {
				continue
			}
			state := asString(session["state"])
			agent := asString(session["agent"])
			if state != "" {
				fmt.Fprintf(os.Stderr, "\n[harness state] session=%s agent=%s state=%s\n", sessionID, agent, state)
			}
		case protocol.EventSessionExited:
			if asString(evt["id"]) != sessionID {
				continue
			}
			code := asInt(evt["exit_code"])
			signal := asString(evt["signal"])
			if signal != "" {
				fmt.Fprintf(os.Stderr, "\n[harness exit] code=%d signal=%s\n", code, signal)
			} else {
				fmt.Fprintf(os.Stderr, "\n[harness exit] code=%d\n", code)
			}
			closeExited()
			return
		case protocol.EventCommandError:
			cmd := asString(evt["cmd"])
			errMsg := asString(evt["error"])
			fmt.Fprintf(os.Stderr, "\n[harness command_error] cmd=%s error=%s\n", cmd, errMsg)
		}
	}
}

func runHarnessInputLoop(writer *harnessWriter, sessionID string) {
	buf := make([]byte, 8192)
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			data := string(buf[:n])
			_ = writer.send(map[string]interface{}{
				"cmd":  protocol.CmdPtyInput,
				"id":   sessionID,
				"data": data,
			})
		}
		if err != nil {
			return
		}
	}
}

func asString(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	default:
		return ""
	}
}

func asBool(v interface{}) bool {
	b, ok := v.(bool)
	return ok && b
}

func asInt(v interface{}) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	case string:
		n, _ := strconv.Atoi(t)
		return n
	default:
		return 0
	}
}
