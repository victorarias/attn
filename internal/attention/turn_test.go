package attention

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
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
		// idle joins the vocabulary in slice 2; until then a finished run that
		// has no turn open leaves without asking for anyone.
		{protocol.SessionStateIdle, false},
	}

	for _, tt := range tests {
		if got := OpensTurn(tt.state); got != tt.want {
			t.Errorf("OpensTurn(%q) = %v, want %v", tt.state, got, tt.want)
		}
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

// A shell sitting in idle is the case slice 2 turns live: once idle opens a
// turn, the exclusion is the only thing keeping every terminal pane out of the
// queue forever.
func TestOwedExcludesIdleShell(t *testing.T) {
	opened := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	if Owed(Input{OpenedAt: opened, IsShell: true}) {
		t.Error("an idle shell with an open turn is owed; want excluded")
	}
}
