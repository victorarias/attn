package agent

import "testing"

// The agent->self-monitor mapping that used to live as a magic string in
// internal/ticketnotify (HasSelfMonitor("claude")) is now a driver capability.
// This is its home: only Claude self-monitors today; codex (and the rest, which
// rely on the zero value) take the idle-nudge path.
func TestCapabilitiesSelfMonitor(t *testing.T) {
	if !MustGet("claude").Capabilities().HasSelfMonitor {
		t.Fatal("claude should self-monitor (watch its own ticket stream)")
	}
	if MustGet("codex").Capabilities().HasSelfMonitor {
		t.Fatal("codex should not self-monitor (it is pty-nudged when idle)")
	}
}
