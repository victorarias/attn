package store

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestSetSessionPinnedStampsAndClears(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "s1", protocol.SessionStateIdle)

	at := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)
	if !s.SetSessionPinned("s1", true, at) {
		t.Fatal("pin reported no change")
	}
	pinned := protocol.Deref(s.Get("s1").PinnedAt)
	if pinned != at.Format(time.RFC3339Nano) {
		t.Fatalf("pinned_at = %q, want the pin instant %q", pinned, at.Format(time.RFC3339Nano))
	}

	if !s.SetSessionPinned("s1", false, at.Add(time.Hour)) {
		t.Fatal("unpin reported no change")
	}
	if got := s.Get("s1").PinnedAt; got != nil {
		t.Fatalf("pinned_at = %q after unpin, want absent", *got)
	}
}

func TestSetSessionPinnedReportsAMissingSession(t *testing.T) {
	s := newTurnStore(t)
	if s.SetSessionPinned("nobody", true, time.Now()) {
		t.Error("pinning a session that does not exist reported a change")
	}
}

// The pin is the band's sort key, so re-pinning must move the row to the end
// rather than restore it to where it used to sit.
func TestRepinningTakesTheNewInstant(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "s1", protocol.SessionStateIdle)

	first := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)
	second := first.Add(2 * time.Hour)
	s.SetSessionPinned("s1", true, first)
	s.SetSessionPinned("s1", false, first.Add(time.Hour))
	s.SetSessionPinned("s1", true, second)

	if got := protocol.Deref(s.Get("s1").PinnedAt); got != second.Format(time.RFC3339Nano) {
		t.Fatalf("pinned_at = %q, want the later pin %q", got, second.Format(time.RFC3339Nano))
	}
}

// Pinning filters at read rather than settling, so the turn stamps go on
// accruing underneath it. This is what makes releasing a pin surface whatever
// was outstanding at its true age instead of restarting it from nothing.
func TestPinningLeavesTheTurnStampsAlone(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "s1", protocol.SessionStateWaitingInput)

	opened := time.Date(2026, 8, 5, 8, 0, 0, 0, time.UTC)
	if !s.OpenTurnIfClosed("s1", opened) {
		t.Fatal("open reported no change")
	}
	s.SetSessionPinned("s1", true, opened.Add(time.Minute))

	stamps := s.TurnStamps("s1")
	if !stamps.OpenedAt.Equal(opened) {
		t.Errorf("turn opened_at = %v after a pin, want it untouched at %v", stamps.OpenedAt, opened)
	}
	if !stamps.SettledAt.IsZero() {
		t.Errorf("turn settled_at = %v; pinning must not settle", stamps.SettledAt)
	}
}

// A respawn re-adds the session record, and that record carries no pin: the
// column is absent from the upsert precisely so the pin survives. Both store
// branches have to agree, or a memory-backed test passes while production
// silently drops the pin (or the reverse).
func TestRespawnKeepsThePin(t *testing.T) {
	for _, branch := range []struct {
		name  string
		build func(t *testing.T) *Store
	}{
		{"sqlite", newTurnStore},
		{"memory", func(t *testing.T) *Store { return &Store{sessions: map[string]*protocol.Session{}} }},
	} {
		t.Run(branch.name, func(t *testing.T) {
			s := branch.build(t)
			addTurnSession(t, s, "s1", protocol.SessionStateIdle)
			at := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)
			if !s.SetSessionPinned("s1", true, at) {
				t.Fatal("pin reported no change")
			}

			// The spawn pipeline's record for an existing session: fresh fields, no
			// pin, same id.
			addTurnSession(t, s, "s1", protocol.SessionStateLaunching)

			if got := protocol.Deref(s.Get("s1").PinnedAt); got != at.Format(time.RFC3339Nano) {
				t.Fatalf("pinned_at = %q after a re-add, want the pin to survive at %q", got, at.Format(time.RFC3339Nano))
			}
		})
	}
}

// The satellite link is spawn-time provenance and does ride the upsert, so a
// record that carries one writes it and a read gives it back.
func TestParentSessionIDRoundTrips(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "agent", protocol.SessionStateIdle)

	now := time.Now().Format(time.RFC3339Nano)
	if err := s.AddChecked(&protocol.Session{
		ID:              "shell",
		Label:           "shell",
		Agent:           protocol.SessionAgentShell,
		Directory:       "/tmp/shell",
		WorkspaceID:     "ws-1",
		State:           protocol.SessionStateIdle,
		StateSince:      now,
		StateUpdatedAt:  now,
		LastSeen:        now,
		ParentSessionID: protocol.Ptr("agent"),
	}); err != nil {
		t.Fatalf("add satellite: %v", err)
	}

	if got := protocol.Deref(s.Get("shell").ParentSessionID); got != "agent" {
		t.Fatalf("parent_session_id = %q, want %q", got, "agent")
	}
	// And it survives the list read too, which is the path the daemon broadcasts
	// from.
	for _, session := range s.List("") {
		if session.ID == "shell" && protocol.Deref(session.ParentSessionID) != "agent" {
			t.Fatalf("parent_session_id = %q from List, want %q", protocol.Deref(session.ParentSessionID), "agent")
		}
	}
}
