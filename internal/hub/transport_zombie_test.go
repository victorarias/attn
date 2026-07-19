//go:build !windows

package hub

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestConnectViaSSHOnceReapsChildOnDialFailure exercises the websocket.Dial
// failure path in connectViaSSHOnce. Before the fix, each failed attempt left
// the ssh child as a <defunct> zombie because cmd.Process.Kill was called
// without a matching cmd.Wait. On macOS these accumulate until the per-user
// process limit is hit.
func TestConnectViaSSHOnceReapsChildOnDialFailure(t *testing.T) {
	// Shim "ssh" to return an invalid WebSocket upgrade immediately, then stay
	// alive until connectViaSSHOnce kills it. The dial error path is the one that
	// leaked zombies in the bug; a real timeout only made this regression slow.
	shimDir := t.TempDir()
	shim := filepath.Join(shimDir, "ssh")
	script := "#!/bin/sh\nprintf 'HTTP/1.1 502 Bad Gateway\\r\\nContent-Length: 0\\r\\n\\r\\n'\nsleep 10\n"
	if err := os.WriteFile(shim, []byte(script), 0o755); err != nil {
		t.Fatalf("write shim: %v", err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	ws, cmd, err := connectViaSSHOnce(ctx, "fake-target", "", "")
	cancel()
	if err == nil {
		// Unexpected success — clean up and fail.
		if ws != nil {
			_ = ws.CloseNow()
		}
		killAndReap(cmd)
		t.Fatal("expected dial failure via shim, got success")
	}

	zombies := zombieChildrenOf(t, os.Getpid())
	if zombies > 0 {
		t.Fatalf("after failed dial: found %d zombie child ssh processes (expected 0)", zombies)
	}
}

// zombieChildrenOf returns the number of <defunct> processes whose PPID equals
// parent. Uses `ps -A` because `ps` without -A scopes to the controlling tty
// and will miss detached children in CI.
func zombieChildrenOf(t *testing.T, parent int) int {
	t.Helper()
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=,stat=").Output()
	if err != nil {
		t.Fatalf("ps failed: %v", err)
	}
	count := 0
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 3 {
			continue
		}
		var pid, ppid int
		if _, err := fmt.Sscanf(fields[0], "%d", &pid); err != nil {
			continue
		}
		if _, err := fmt.Sscanf(fields[1], "%d", &ppid); err != nil {
			continue
		}
		if ppid == parent && strings.HasPrefix(fields[2], "Z") {
			count++
		}
	}
	return count
}
