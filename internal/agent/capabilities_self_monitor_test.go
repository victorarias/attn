package agent

import "testing"

// The agent->self-monitor mapping is a driver capability used for optional chief
// guidance. It does not change the daemon's shared nudge eligibility.
func TestCapabilitiesSelfMonitor(t *testing.T) {
	if !MustGet("claude").Capabilities().HasSelfMonitor {
		t.Fatal("claude should self-monitor (watch its own ticket stream)")
	}
	if MustGet("codex").Capabilities().HasSelfMonitor {
		t.Fatal("codex should not advertise an optional ticket monitor")
	}
}
