package pty

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	creackpty "github.com/creack/pty"
)

// Drives a real interactive shell through the startup overlay on a real PTY
// and asserts the injected OSC 133 integration emits marks the signal arbiter
// turns into state edges. This is the whole chain the poll cannot cover:
// command-start/end edges with exit codes, straight from the shell.
func runShellIntegrationScenario(t *testing.T, shellPath string, env []string) []Observation {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping real PTY spawn in short mode")
	}
	if _, err := os.Stat(shellPath); err != nil {
		t.Skipf("%s unavailable: %v", shellPath, err)
	}

	launch, err := prepareShellPaneLaunch(shellPath, env)
	if err != nil {
		t.Fatalf("prepare shell pane launch: %v", err)
	}
	t.Cleanup(launch.cleanup)
	launch.command.Env = launch.env
	launch.command.Dir = t.TempDir()

	ptmx, err := creackpty.StartWithSize(launch.command, &creackpty.Winsize{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("start %s in PTY: %v", shellPath, err)
	}
	t.Cleanup(func() {
		_ = ptmx.Close()
		if launch.command.Process != nil {
			_ = launch.command.Process.Kill()
		}
		_, _ = launch.command.Process.Wait()
	})

	arbiter := newShellSignalArbiter(launch.command.Process.Pid)
	var mu sync.Mutex
	var observations []Observation
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				obs := arbiter.ObserveOutput(buf[:n], time.Now())
				mu.Lock()
				observations = append(observations, obs...)
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	if _, err := ptmx.Write([]byte("/bin/echo attn-integration-probe\rfalse\rexit\r")); err != nil {
		t.Fatalf("write commands: %v", err)
	}
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("shell did not exit; integration output never completed")
	}

	mu.Lock()
	defer mu.Unlock()
	return append([]Observation(nil), observations...)
}

func requireObservation(t *testing.T, observations []Observation, claim, detail string) {
	t.Helper()
	for _, obs := range observations {
		if obs.Claim == claim && obs.Detail == detail {
			return
		}
	}
	t.Fatalf("no %s/%q observation in %+v", claim, detail, observations)
}

func TestZshIntegrationEmitsCommandEdges(t *testing.T) {
	root := t.TempDir()
	userZdotdir := filepath.Join(root, "user-zdotdir")
	if err := os.MkdirAll(userZdotdir, 0o755); err != nil {
		t.Fatalf("create user zdotdir: %v", err)
	}
	observations := runShellIntegrationScenario(t, "/bin/zsh", []string{
		"PATH=/usr/bin:/bin",
		"HOME=" + root,
		"ZDOTDIR=" + userZdotdir,
		"TERM=xterm-256color",
	})

	// The preexec hook marks the start of every command…
	requireObservation(t, observations, claimBusy, "command started")
	// …and the precmd hook closes it with the real exit code.
	requireObservation(t, observations, claimNotBusy, "command exited 0")
	requireObservation(t, observations, claimNotBusy, "command exited 1")
}

func TestBashIntegrationEmitsPromptAndExitMarks(t *testing.T) {
	root := t.TempDir()
	userHome := filepath.Join(root, "home")
	if err := os.MkdirAll(userHome, 0o755); err != nil {
		t.Fatalf("create user home: %v", err)
	}
	observations := runShellIntegrationScenario(t, "/bin/bash", []string{
		"PATH=/usr/bin:/bin",
		"HOME=" + userHome,
		"TERM=xterm-256color",
	})

	// bash's D mark carries the exit code of each command. (The C mark needs
	// PS0, absent on macOS's bash 3.2 — not asserted so the test holds on both
	// old and new bash.)
	requireObservation(t, observations, claimNotBusy, "shell at prompt")
	requireObservation(t, observations, claimNotBusy, "command exited 1")
}

// The user's opt-out: with ATTN_NO_SHELL_INTEGRATION set, the overlay never
// sources the integration and no marks are emitted.
func TestShellIntegrationOptOutEmitsNoMarks(t *testing.T) {
	root := t.TempDir()
	userZdotdir := filepath.Join(root, "user-zdotdir")
	if err := os.MkdirAll(userZdotdir, 0o755); err != nil {
		t.Fatalf("create user zdotdir: %v", err)
	}
	observations := runShellIntegrationScenario(t, "/bin/zsh", []string{
		"PATH=/usr/bin:/bin",
		"HOME=" + root,
		"ZDOTDIR=" + userZdotdir,
		"TERM=xterm-256color",
		"ATTN_NO_SHELL_INTEGRATION=1",
	})

	for _, obs := range observations {
		if strings.HasPrefix(obs.Detail, "command ") {
			t.Fatalf("opt-out still emitted %+v", obs)
		}
	}
}
