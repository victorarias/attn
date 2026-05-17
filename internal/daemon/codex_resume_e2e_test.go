package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

func TestCodexResumeMappingEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY end-to-end test in short mode")
	}

	tmpDir := t.TempDir()
	repoRoot := findRepoRootForTest(t)
	attnBin := filepath.Join(tmpDir, "attn")
	build := exec.Command("go", "build", "-o", attnBin, "./cmd/attn")
	build.Dir = repoRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build attn test binary: %v\n%s", err, string(output))
	}

	fakeCodexLog := filepath.Join(tmpDir, "fake-codex.log")
	fakeCodex := filepath.Join(tmpDir, "fake-codex")
	nativeCodexID := "codex-native-session-123"
	fakeScript := fmt.Sprintf(`#!/bin/sh
set -eu
printf 'ARGS:%%s\n' "$*" >> %q
printf '{"session_id":%q,"transcript_path":"/tmp/fake-codex.jsonl"}' | "$ATTN_WRAPPER_PATH" _hook-session-start
trap 'exit 0' TERM INT
while :; do sleep 1; done
`, fakeCodexLog, nativeCodexID)
	if err := os.WriteFile(fakeCodex, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}

	port, err := freeTCPPort()
	if err != nil {
		t.Fatalf("allocate ws port: %v", err)
	}
	sockPath := filepath.Join(tmpDir, "attn.sock")
	t.Setenv("ATTN_WS_PORT", fmt.Sprintf("%d", port))
	t.Setenv("ATTN_SOCKET_PATH", sockPath)
	t.Setenv("ATTN_WRAPPER_PATH", attnBin)

	d := NewForTesting(sockPath)
	go func() {
		if err := d.Start(); err != nil {
			t.Logf("daemon exited: %v", err)
		}
	}()
	defer d.Stop()
	waitForSocket(t, sockPath, 5*time.Second)

	client := &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
	sessionID := "attn-codex-e2e"
	cwd := tmpDir

	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:             protocol.CmdSpawnSession,
		ID:              sessionID,
		Cwd:             cwd,
		Agent:           "codex",
		Label:           protocol.Ptr("codex-e2e"),
		CodexExecutable: protocol.Ptr(fakeCodex),
		Cols:            80,
		Rows:            24,
	})
	expectSpawnSuccess(t, client)

	waitForCondition(t, 5*time.Second, func() bool {
		return d.store.GetResumeSessionID(sessionID) == nativeCodexID
	}, "codex hook to store native session id")

	removePTYSession(t, d, sessionID)

	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:             protocol.CmdSpawnSession,
		ID:              sessionID,
		Cwd:             cwd,
		Agent:           "codex",
		Label:           protocol.Ptr("codex-e2e"),
		CodexExecutable: protocol.Ptr(fakeCodex),
		ResumeSessionID: protocol.Ptr(sessionID),
		Cols:            80,
		Rows:            24,
	})
	expectSpawnSuccess(t, client)
	defer removePTYSession(t, d, sessionID)

	waitForCondition(t, 5*time.Second, func() bool {
		lines := readFakeCodexLog(t, fakeCodexLog)
		if len(lines) < 2 {
			return false
		}
		first := lines[0]
		second := lines[len(lines)-1]
		return strings.Contains(first, "_hook-session-start") &&
			strings.Contains(first, "features.hooks=true") &&
			strings.Contains(first, `"/<session-flags>/config.toml:session_start:0:0"`) &&
			strings.Contains(first, "trusted_hash") &&
			strings.Contains(second, "resume "+nativeCodexID)
	}, "reload to invoke fake codex with native resume id")
}

func expectSpawnSuccess(t *testing.T, client *wsClient) {
	t.Helper()
	select {
	case outbound := <-client.send:
		var result protocol.SpawnResultMessage
		if err := json.Unmarshal(outbound.payload, &result); err != nil {
			t.Fatalf("decode spawn_result: %v", err)
		}
		if !result.Success {
			t.Fatalf("spawn_result success=false error=%q", protocol.Deref(result.Error))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for spawn_result")
	}
}

func removePTYSession(t *testing.T, d *Daemon, sessionID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = d.ptyBackend.Kill(ctx, sessionID, syscall.SIGTERM)
	cancel()

	ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := d.ptyBackend.Remove(ctx, sessionID); err != nil {
		t.Fatalf("remove pty session: %v", err)
	}
}

func readFakeCodexLog(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func waitForCondition(t *testing.T, timeout time.Duration, ok func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}

func findRepoRootForTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root")
		}
		dir = parent
	}
}
