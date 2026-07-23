package pty

import (
	"strings"
	"syscall"
	"testing"
	"time"
)

// waitForKillReady polls the session's rendered screen until it contains the
// readiness marker the spawned script emits right after installing its
// signal traps. Without this, kill() could race the child's own startup and
// send its signal before the trap is installed, which would make a shell
// exit to the raw signal for the wrong reason and silently pass the test.
func waitForKillReady(t *testing.T, s *Session, marker string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(s.screen.renderedText(), marker) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for readiness marker %q", marker)
}

func spawnKillTestSession(t *testing.T, id, script string) *Session {
	t.Helper()
	m := NewManager(nil)
	t.Cleanup(m.Shutdown)

	if err := m.Spawn(SpawnOptions{
		ID:              id,
		CWD:             t.TempDir(),
		Agent:           "probe-kill",
		ExternalCommand: []string{"/bin/bash", "-c", script},
		Cols:            80,
		Rows:            24,
	}); err != nil {
		t.Fatalf("Spawn() error: %v", err)
	}

	s, err := m.getSession(id)
	if err != nil {
		t.Fatalf("getSession() error: %v", err)
	}
	waitForKillReady(t, s, "__KILLREADY__")
	return s
}

// TestKill_SIGTERMIgnoringShellExitsViaSIGHUP covers a shell that ignores
// SIGTERM (as interactive/login shells commonly do) but honors SIGHUP. This
// fails on the pre-fix kill(), which rides the full waitTimeout to SIGKILL
// (~8s elapsed, exit signal "killed") instead of escalating to SIGHUP.
func TestKill_SIGTERMIgnoringShellExitsViaSIGHUP(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real PTY spawn in short mode")
	}

	s := spawnKillTestSession(t, "kill-term-ignored", `trap '' TERM; echo __KILLREADY__; while :; do sleep 0.1; done`)

	start := time.Now()
	if err := s.kill(syscall.SIGTERM, 8*time.Second); err != nil {
		t.Fatalf("kill() error: %v", err)
	}
	elapsed := time.Since(start)

	if elapsed >= 6*time.Second {
		t.Fatalf("kill() took %v, want well under the 8s deadline (should escalate to SIGHUP)", elapsed)
	}

	info := s.info()
	if info.ExitSignal == nil || *info.ExitSignal != syscall.SIGHUP.String() {
		got := "<nil>"
		if info.ExitSignal != nil {
			got = *info.ExitSignal
		}
		t.Fatalf("ExitSignal = %s, want %s", got, syscall.SIGHUP.String())
	}
}

// TestKill_TERMAndHUPIgnoringChildFallsBackToSIGKILL covers a child that
// ignores both SIGTERM and SIGHUP, so kill() must still fall back to SIGKILL
// once waitTimeout expires. Catches a bug where SIGHUP escalation replaced
// the SIGKILL backstop instead of supplementing it.
func TestKill_TERMAndHUPIgnoringChildFallsBackToSIGKILL(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real PTY spawn in short mode")
	}

	s := spawnKillTestSession(t, "kill-term-hup-ignored", `trap '' TERM HUP; echo __KILLREADY__; while :; do sleep 0.1; done`)

	start := time.Now()
	if err := s.kill(syscall.SIGTERM, 1500*time.Millisecond); err != nil {
		t.Fatalf("kill() error: %v", err)
	}
	elapsed := time.Since(start)

	info := s.info()
	if info.ExitSignal == nil || *info.ExitSignal != syscall.SIGKILL.String() {
		got := "<nil>"
		if info.ExitSignal != nil {
			got = *info.ExitSignal
		}
		t.Fatalf("ExitSignal = %s, want %s", got, syscall.SIGKILL.String())
	}
	if elapsed < 1200*time.Millisecond {
		t.Fatalf("kill() took %v, want it to ride the full ladder (>= ~1.2s)", elapsed)
	}
}

// TestKill_CooperativeChildExitsOnSIGTERMBeforeGrace covers the common case:
// a child that exits promptly on SIGTERM should not wait out
// sigtermToHUPGrace. Catches a regression where kill always waits out the
// grace period regardless of whether the child already exited.
func TestKill_CooperativeChildExitsOnSIGTERMBeforeGrace(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real PTY spawn in short mode")
	}

	s := spawnKillTestSession(t, "kill-term-cooperative", `echo __KILLREADY__; while :; do sleep 0.1; done`)

	start := time.Now()
	if err := s.kill(syscall.SIGTERM, 8*time.Second); err != nil {
		t.Fatalf("kill() error: %v", err)
	}
	elapsed := time.Since(start)

	if elapsed >= 1500*time.Millisecond {
		t.Fatalf("kill() took %v, want well under sigtermToHUPGrace (should not wait out the grace)", elapsed)
	}

	info := s.info()
	if info.ExitSignal == nil || *info.ExitSignal != syscall.SIGTERM.String() {
		got := "<nil>"
		if info.ExitSignal != nil {
			got = *info.ExitSignal
		}
		t.Fatalf("ExitSignal = %s, want %s", got, syscall.SIGTERM.String())
	}
}
