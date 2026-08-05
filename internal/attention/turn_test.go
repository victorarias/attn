package attention

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionstate"
)

func TestOpensTurn(t *testing.T) {
	tests := []struct {
		state protocol.SessionState
		want  bool
	}{
		{protocol.SessionStateWaitingInput, true},
		{protocol.SessionStatePendingApproval, true},
		{protocol.SessionStateUnknown, true},
		{protocol.SessionStateLaunching, false},
		{protocol.SessionStateWorking, false},
		{protocol.SessionStateScheduled, false},
		{protocol.SessionStateRecoverable, false},
		// A finished run is the user's to read, so idle opens a turn too.
		{protocol.SessionStateIdle, true},
	}

	for _, tt := range tests {
		if got := OpensTurn(tt.state); got != tt.want {
			t.Errorf("OpensTurn(%q) = %v, want %v", tt.state, got, tt.want)
		}
	}
}

// Snoozing says "not now", and the set below is everything that overrides it.
// It is deliberately narrow: every ordinary way an agent asks for the user is a
// thing the user was deferring when they pressed snooze.
func TestBreaksSnooze(t *testing.T) {
	tests := []struct {
		name   string
		state  protocol.SessionState
		reason string
		want   bool
	}{
		{"stuck is the daemon admitting it cannot tell", protocol.SessionStateUnknown, string(sessionstate.ReasonStuck), true},
		{"no evidence, same admission", protocol.SessionStateUnknown, string(sessionstate.ReasonNoEvidence), true},
		{"unknown breaks through whatever the reason", protocol.SessionStateUnknown, "", true},
		{"the agent's process is gone", protocol.SessionStateIdle, string(sessionstate.ReasonProcessExited), true},

		// The deferral holds through every ordinary way an agent stops.
		{"a run that merely ended", protocol.SessionStateIdle, string(sessionstate.ReasonClassifierVerdict), false},
		{"a session sitting at its prompt", protocol.SessionStateIdle, string(sessionstate.ReasonAtPrompt), false},
		{"a question", protocol.SessionStateWaitingInput, string(sessionstate.ReasonQuestionOpen), false},
		{"an approval", protocol.SessionStatePendingApproval, string(sessionstate.ReasonApprovalOpen), false},
		{"working", protocol.SessionStateWorking, string(sessionstate.ReasonHeartbeatFresh), false},
		{"scheduled", protocol.SessionStateScheduled, string(sessionstate.ReasonCronPending), false},
		{"recoverable, which the daemon revives unattended", protocol.SessionStateRecoverable, "", false},

		// process_exited is idle's alone. The pairing is what keeps a dead agent
		// from being confused with one that simply finished.
		{"a waiting session carrying the exited reason", protocol.SessionStateWaitingInput, string(sessionstate.ReasonProcessExited), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BreaksSnooze(tt.state, tt.reason); got != tt.want {
				t.Errorf("BreaksSnooze(%q, %q) = %v, want %v", tt.state, tt.reason, got, tt.want)
			}
		})
	}
}

func TestOwedComparesStamps(t *testing.T) {
	early := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	late := early.Add(time.Hour)

	tests := []struct {
		name    string
		opened  time.Time
		settled time.Time
		want    bool
	}{
		{"never opened", time.Time{}, time.Time{}, false},
		{"opened, never settled", early, time.Time{}, true},
		{"settled after opening", early, late, false},
		{"re-opened after settling", late, early, true},
		{"settled at the same instant it opened", early, early, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Owed(Input{OpenedAt: tt.opened, SettledAt: tt.settled})
			if got != tt.want {
				t.Errorf("Owed = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOwedExclusions(t *testing.T) {
	opened := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		in   Input
	}{
		{"shell", Input{OpenedAt: opened, IsShell: true}},
		{"chief of staff", Input{OpenedAt: opened, ChiefOfStaff: true}},
		{"pinned session", Input{OpenedAt: opened, SessionPinned: true}},
		{"pinned workspace", Input{OpenedAt: opened, WorkspacePinned: true}},
		{"muted workspace", Input{OpenedAt: opened, WorkspaceMuted: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if Owed(tt.in) {
				t.Errorf("Owed = true for an excluded session; want false")
			}
			// The same session without the exclusion is owed, so the test is
			// pinning the exclusion rather than an unstamped input.
			bare := Input{OpenedAt: tt.in.OpenedAt}
			if !Owed(bare) {
				t.Fatalf("Owed = false without the exclusion; the case proves nothing")
			}
		})
	}
}

// A shell is registered idle at birth and left there, so the exclusion is the
// only thing keeping every terminal pane out of the queue forever.
func TestOwedExcludesIdleShell(t *testing.T) {
	opened := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	if Owed(Input{OpenedAt: opened, IsShell: true}) {
		t.Error("an idle shell with an open turn is owed; want excluded")
	}
}
