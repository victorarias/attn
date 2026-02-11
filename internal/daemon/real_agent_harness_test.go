package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/wrapper"
	"nhooyr.io/websocket"
)

func TestRealAgentHarness(t *testing.T) {
	if strings.TrimSpace(os.Getenv("ATTN_REAL_AGENT_HARNESS")) != "1" {
		t.Skip("set ATTN_REAL_AGENT_HARNESS=1 to run real-agent harness")
	}

	agent := strings.ToLower(strings.TrimSpace(os.Getenv("ATTN_REAL_AGENT")))
	if agent == "" {
		agent = "codex"
	}
	if agent != "codex" && agent != "copilot" && agent != "claude" {
		t.Fatalf("invalid ATTN_REAL_AGENT=%q (expected codex|copilot|claude)", agent)
	}

	cwd := strings.TrimSpace(os.Getenv("ATTN_REAL_CWD"))
	if cwd == "" {
		wd, err := os.Getwd()
		if err != nil {
			t.Fatalf("getwd: %v", err)
		}
		cwd = wd
	}
	cwd = filepath.Clean(cwd)

	prompt := strings.TrimSpace(os.Getenv("ATTN_REAL_PROMPT"))
	if prompt == "" {
		prompt = "hello"
	}

	timeout := 120 * time.Second
	if timeoutEnv := strings.TrimSpace(os.Getenv("ATTN_REAL_TIMEOUT_SECONDS")); timeoutEnv != "" {
		n, err := strconv.Atoi(timeoutEnv)
		if err != nil || n <= 0 {
			t.Fatalf("invalid ATTN_REAL_TIMEOUT_SECONDS=%q", timeoutEnv)
		}
		timeout = time.Duration(n) * time.Second
	}

	port, err := freeTCPPort()
	if err != nil {
		t.Fatalf("allocate ws port: %v", err)
	}
	t.Setenv("ATTN_WS_PORT", strconv.Itoa(port))

	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "attn.sock")
	d := NewForTesting(sockPath)
	go func() {
		if err := d.Start(); err != nil {
			t.Logf("daemon exited: %v", err)
		}
	}()
	defer d.Stop()

	waitForSocket(t, sockPath, 5*time.Second)

	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/ws", port)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	cancel()
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	c := client.New(sockPath)
	sessionID := wrapper.GenerateSessionID()
	t.Logf("harness session=%s agent=%s cwd=%s", sessionID, agent, cwd)

	stateCh := make(chan string, 64)
	errCh := make(chan error, 1)
	spawnCh := make(chan error, 1)
	attachCh := make(chan error, 1)
	exitCh := make(chan struct{})
	doneRead := make(chan struct{})

	go readHarnessEvents(t, conn, sessionID, spawnCh, attachCh, stateCh, errCh, exitCh, doneRead)

	spawn := map[string]interface{}{
		"cmd":   protocol.CmdSpawnSession,
		"id":    sessionID,
		"cwd":   cwd,
		"agent": agent,
		"label": filepath.Base(cwd),
		"cols":  120,
		"rows":  32,
	}
	if v := strings.TrimSpace(os.Getenv("ATTN_REAL_CLAUDE_EXECUTABLE")); v != "" {
		spawn["claude_executable"] = v
	}
	if v := strings.TrimSpace(os.Getenv("ATTN_REAL_CODEX_EXECUTABLE")); v != "" {
		spawn["codex_executable"] = v
	}
	if v := strings.TrimSpace(os.Getenv("ATTN_REAL_COPILOT_EXECUTABLE")); v != "" {
		spawn["copilot_executable"] = v
	}

	if err := writeWS(conn, spawn); err != nil {
		t.Fatalf("spawn write failed: %v", err)
	}
	select {
	case err := <-spawnCh:
		if err != nil {
			t.Fatalf("spawn failed: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("timeout waiting for spawn_result")
	}

	if err := writeWS(conn, map[string]interface{}{
		"cmd": protocol.CmdAttachSession,
		"id":  sessionID,
	}); err != nil {
		t.Fatalf("attach write failed: %v", err)
	}
	select {
	case err := <-attachCh:
		if err != nil {
			t.Fatalf("attach failed: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for attach_result")
	}

	if err := writeWS(conn, map[string]interface{}{
		"cmd":  protocol.CmdPtyInput,
		"id":   sessionID,
		"data": prompt + "\n",
	}); err != nil {
		t.Fatalf("pty_input write failed: %v", err)
	}
	t.Logf("sent prompt: %q", prompt)

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	seenInterestingState := false
	for !seenInterestingState {
		select {
		case st := <-stateCh:
			if st == protocol.StateWaitingInput || st == protocol.StateIdle || st == protocol.StatePendingApproval || st == protocol.StateUnknown {
				seenInterestingState = true
				t.Logf("harness reached state=%s", st)
			}
		case err := <-errCh:
			t.Fatalf("harness error: %v", err)
		case <-exitCh:
			t.Log("session exited before target state")
			seenInterestingState = true
		case <-deadline.C:
			t.Fatalf("timeout (%v) waiting for state transition after prompt", timeout)
		}
	}

	if err := writeWS(conn, map[string]interface{}{
		"cmd":    protocol.CmdKillSession,
		"id":     sessionID,
		"signal": "TERM",
	}); err != nil {
		t.Logf("kill write failed: %v", err)
	}
	_ = c.Unregister(sessionID)
	<-doneRead
}

func readHarnessEvents(
	t *testing.T,
	conn *websocket.Conn,
	sessionID string,
	spawnCh chan<- error,
	attachCh chan<- error,
	stateCh chan<- string,
	errCh chan<- error,
	exitCh chan<- struct{},
	done chan<- struct{},
) {
	defer close(done)

	var (
		spawnOnce  sync.Once
		attachOnce sync.Once
		exitOnce   sync.Once
	)

	sendExit := func() {
		exitOnce.Do(func() { close(exitCh) })
	}

	for {
		_, payload, err := conn.Read(context.Background())
		if err != nil {
			sendExit()
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
			spawnOnce.Do(func() {
				if asBool(evt["success"]) {
					spawnCh <- nil
					return
				}
				spawnCh <- fmt.Errorf("%s", asString(evt["error"]))
			})

		case protocol.EventAttachResult:
			if asString(evt["id"]) != sessionID {
				continue
			}
			attachOnce.Do(func() {
				if asBool(evt["success"]) {
					attachCh <- nil
					return
				}
				attachCh <- fmt.Errorf("%s", asString(evt["error"]))
			})

		case protocol.EventPtyOutput:
			if asString(evt["id"]) != sessionID {
				continue
			}
			encoded := asString(evt["data"])
			if encoded == "" {
				continue
			}
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				continue
			}
			_, _ = os.Stdout.Write(decoded)

		case protocol.EventSessionStateChanged:
			session, _ := evt["session"].(map[string]interface{})
			if session == nil || asString(session["id"]) != sessionID {
				continue
			}
			state := asString(session["state"])
			agent := asString(session["agent"])
			t.Logf("state_changed session=%s agent=%s state=%s", sessionID, agent, state)
			select {
			case stateCh <- state:
			default:
			}

		case protocol.EventSessionExited:
			if asString(evt["id"]) != sessionID {
				continue
			}
			t.Logf("session_exited code=%d signal=%s", asInt(evt["exit_code"]), asString(evt["signal"]))
			sendExit()
			return

		case protocol.EventCommandError:
			cmd := asString(evt["cmd"])
			msg := asString(evt["error"])
			select {
			case errCh <- fmt.Errorf("command_error cmd=%s error=%s", cmd, msg):
			default:
			}
		}
	}
}

func writeWS(conn *websocket.Conn, msg map[string]interface{}) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return conn.Write(ctx, websocket.MessageText, payload)
}

func freeTCPPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected addr type %T", listener.Addr())
	}
	return addr.Port, nil
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
