package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func pinnedAt(t *testing.T, d *Daemon, id string) string {
	t.Helper()
	session := d.sessionForBroadcast(d.store.Get(id))
	if session == nil {
		t.Fatalf("session %s not found", id)
	}
	return protocol.Deref(session.PinnedAt)
}

// The point of the individual pin: one agent leaves the queue and its siblings
// stay in it. Pinning the workspace was the only way to do this before, and it
// took everything with it.
func TestPinningOneSessionLeavesItsSiblingsInTheQueue(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "pinned", protocol.SessionAgentCodex, "ws1")
	addTurnSession(t, d, "sibling", protocol.SessionAgentCodex, "ws1")
	moveTo(d, "pinned", protocol.StateWaitingInput)
	moveTo(d, "sibling", protocol.StateWaitingInput)

	if errMsg := d.setSessionPinned("pinned", true); errMsg != "" {
		t.Fatalf("pin failed: %s", errMsg)
	}

	if owed(t, d, "pinned") {
		t.Error("a pinned session still owes a turn")
	}
	if !owed(t, d, "sibling") {
		t.Error("pinning one session took its sibling out of the queue too")
	}
	if pinnedAt(t, d, "pinned") == "" {
		t.Error("pinned_at is absent on the wire, so the sidebar cannot place the row")
	}
	if pinnedAt(t, d, "sibling") != "" {
		t.Error("pinned_at is set on a session nobody pinned")
	}
}

// Pinning filters at read, so the turn goes on accruing underneath. Releasing
// the pin has to surface it at the age it has really been owed — not restart the
// clock, which would tell the user a two-hour-old turn is new.
func TestUnpinningSurfacesTheOutstandingTurnAtItsTrueAge(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")

	moveTo(d, "s1", protocol.StateWaitingInput)
	openedAt := protocol.Deref(d.sessionForBroadcast(d.store.Get("s1")).TurnOpenedAt)
	if openedAt == "" {
		t.Fatal("no turn to pin over")
	}

	if errMsg := d.setSessionPinned("s1", true); errMsg != "" {
		t.Fatalf("pin failed: %s", errMsg)
	}
	if owed(t, d, "s1") {
		t.Fatal("a pinned session still owes a turn")
	}

	if errMsg := d.setSessionPinned("s1", false); errMsg != "" {
		t.Fatalf("unpin failed: %s", errMsg)
	}
	if !owed(t, d, "s1") {
		t.Fatal("unpinning lost the outstanding turn")
	}
	if got := protocol.Deref(d.sessionForBroadcast(d.store.Get("s1")).TurnOpenedAt); got != openedAt {
		t.Errorf("turn_opened_at = %q after unpin, want the original %q", got, openedAt)
	}
}

// A state that would open a turn still opens it while pinned; the pin only
// hides it. That is what makes the age above real rather than reconstructed.
func TestAPinnedSessionStillAccumulatesTurns(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")

	if errMsg := d.setSessionPinned("s1", true); errMsg != "" {
		t.Fatalf("pin failed: %s", errMsg)
	}
	moveTo(d, "s1", protocol.StateWaitingInput)
	if owed(t, d, "s1") {
		t.Fatal("a pinned session owes a turn on the wire")
	}
	if d.store.TurnStamps("s1").OpenedAt.IsZero() {
		t.Fatal("no turn was stamped while pinned; unpinning would surface nothing")
	}
}

func TestPinningTheChiefIsRefused(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "chief", protocol.SessionAgentCodex, "ws1")
	// The role registry is the authority; chief_of_staff on a session is a
	// broadcast-time decoration and setting it on a stored record proves nothing.
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, "chief"); err != nil {
		t.Fatalf("assign the chief role: %v", err)
	}

	if errMsg := d.setSessionPinned("chief", true); errMsg == "" {
		t.Fatal("pinning the chief was accepted; it is already anchored above the queue")
	}
	if pinnedAt(t, d, "chief") != "" {
		t.Error("the refused pin was written anyway")
	}
}

func TestPinningReportsAMissingSession(t *testing.T) {
	d := newTurnDaemon(t)
	if errMsg := d.setSessionPinned("nobody", true); errMsg == "" {
		t.Fatal("pinning a session that does not exist was accepted")
	}
}

// Resolution is spawn-time and always lands on an agent, never on another
// shell, so nothing downstream has a chain to walk.
func TestResolveSpawnParent(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "agent", protocol.SessionAgentCodex, "ws1")
	addTurnSession(t, d, "shell1", protocol.SessionAgentShell, "ws1")
	shell := d.store.Get("shell1")
	shell.ParentSessionID = protocol.Ptr("agent")
	d.store.Add(shell)
	addTurnSession(t, d, "orphan-shell", protocol.SessionAgentShell, "ws1")
	addTurnSession(t, d, "elsewhere", protocol.SessionAgentCodex, "ws2")

	tests := []struct {
		name        string
		spawnedFrom string
		workspaceID string
		isShell     bool
		want        string
	}{
		{"split from an agent", "agent", "ws1", true, "agent"},
		{"split from a shell inherits that shell's agent", "shell1", "ws1", true, "agent"},
		{"split from a shell with no agent of its own", "orphan-shell", "ws1", true, ""},
		{"an agent is never a satellite", "agent", "ws1", false, ""},
		{"no base at all", "", "ws1", true, ""},
		{"a base that is gone", "vanished", "ws1", true, ""},
		{"landing in another workspace", "elsewhere", "ws1", true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := d.resolveSpawnParent(tt.spawnedFrom, tt.workspaceID, tt.isShell); got != tt.want {
				t.Errorf("resolveSpawnParent(%q, %q, %v) = %q, want %q",
					tt.spawnedFrom, tt.workspaceID, tt.isShell, got, tt.want)
			}
		})
	}
}

// A shell is excluded from the queue by its agent whether or not it has a
// parent, so the satellite link never has to carry that weight as well.
func TestASatelliteNeverOwesATurn(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "agent", protocol.SessionAgentCodex, "ws1")
	addTurnSession(t, d, "shell1", protocol.SessionAgentShell, "ws1")
	shell := d.store.Get("shell1")
	shell.ParentSessionID = protocol.Ptr("agent")
	d.store.Add(shell)

	moveTo(d, "shell1", protocol.StateIdle)
	if owed(t, d, "shell1") {
		t.Fatal("a shell owes a turn")
	}
	if got := protocol.Deref(d.sessionForBroadcast(d.store.Get("shell1")).ParentSessionID); got != "agent" {
		t.Errorf("parent_session_id = %q on the wire, want %q — the app cannot place the row without it", got, "agent")
	}
}
