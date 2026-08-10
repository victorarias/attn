package store

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestTransitionSessionConversationCommitsBindingAndTranscriptScopedState(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "session-1", protocol.SessionStateWorking)
	if _, err := s.CreateTicket(Ticket{
		ID:       "ticket-1",
		Title:    "Continue the work",
		Assignee: "session-1",
	}, "chief", time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	s.SetResumeSessionID("session-1", "codex-old")
	if err := s.SetTicketResumeSessionID("session-1", "codex-old"); err != nil {
		t.Fatalf("SetTicketResumeSessionID: %v", err)
	}
	s.UpdateSessionActivity("session-1", "working in the old conversation", time.Now(), "old-cursor")

	changed, err := s.TransitionSessionConversation("session-1", "codex-new")
	if err != nil {
		t.Fatalf("TransitionSessionConversation: %v", err)
	}
	if !changed {
		t.Fatal("transition reported no change")
	}
	if got := s.GetResumeSessionID("session-1"); got != "codex-new" {
		t.Fatalf("session binding = %q, want codex-new", got)
	}
	if got := s.GetTicketResumeSessionID("session-1"); got != "codex-new" {
		t.Fatalf("ticket binding = %q, want codex-new", got)
	}
	if got := s.GetSessionActivity("session-1"); got != (SessionActivity{}) {
		t.Fatalf("activity after transition = %+v, want zero", got)
	}
	if session := s.Get("session-1"); session.Activity != nil || session.ActivityAt != nil {
		t.Fatalf("session still exposes old activity: %+v", session)
	}
}

func TestTransitionSessionConversationIgnoresRepeatedObservation(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "session-1", protocol.SessionStateWorking)
	s.SetResumeSessionID("session-1", "codex-current")
	s.UpdateSessionActivity("session-1", "still current", time.Now(), "current-cursor")

	changed, err := s.TransitionSessionConversation("session-1", "codex-current")
	if err != nil {
		t.Fatalf("TransitionSessionConversation: %v", err)
	}
	if changed {
		t.Fatal("repeated observation reported a transition")
	}
	if got := s.GetSessionActivity("session-1"); got.Line != "still current" || got.Cursor != "current-cursor" {
		t.Fatalf("repeated observation disturbed activity: %+v", got)
	}
}

func TestTransitionSessionConversationMirrorsBindingAfterSessionClose(t *testing.T) {
	s := newTurnStore(t)
	if _, err := s.CreateTicket(Ticket{
		ID:       "ticket-1",
		Title:    "Resume later",
		Assignee: "closed-session",
	}, "chief", time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	changed, err := s.TransitionSessionConversation("closed-session", "codex-current")
	if err != nil {
		t.Fatalf("TransitionSessionConversation: %v", err)
	}
	if changed {
		t.Fatal("ticket-only persistence reported a live-session transition")
	}
	if got := s.GetTicketResumeSessionID("closed-session"); got != "codex-current" {
		t.Fatalf("ticket binding = %q, want codex-current", got)
	}
}
