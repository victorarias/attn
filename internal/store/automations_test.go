package store

import (
	"testing"
	"time"
)

func TestAutomationClaimIsIdempotentAndSnapshotsRevision(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("cleanup", "Cleanup", `{"id":"cleanup"}`, true, now)
	if err != nil {
		t.Fatal(err)
	}
	ids := AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "auto-run-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"}
	first, created, err := s.ClaimManualAutomationRun("cleanup", "request-1", "github.com/owner/repo#42", `{"scope":"tmp"}`, def.Revision, `{"prompt":"first"}`, now, ids)
	if err != nil || !created {
		t.Fatalf("first claim created=%v err=%v", created, err)
	}
	other := AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2", TicketID: "auto-run-2", SessionID: "session-2", WorkspaceID: "workspace-2", PaneID: "pane-2"}
	second, created, err := s.ClaimManualAutomationRun("cleanup", "request-1", "", `{"scope":"changed"}`, def.Revision, `{"prompt":"changed"}`, now.Add(time.Minute), other)
	if err != nil || created {
		t.Fatalf("duplicate claim created=%v err=%v", created, err)
	}
	if second.ID != first.ID || second.TicketID != first.TicketID || second.SnapshotJSON != `{"prompt":"first"}` {
		t.Fatalf("duplicate returned different run: %#v", second)
	}
	occurrence, err := s.GetAutomationOccurrence(first.OccurrenceID)
	if err != nil || occurrence == nil || occurrence.SubjectKey != "github.com/owner/repo#42" || occurrence.PayloadJSON != `{"scope":"tmp"}` {
		t.Fatalf("occurrence = %#v err=%v", occurrence, err)
	}
}

func TestEnsureAutomationTicketAdoptsByRun(t *testing.T) {
	s := New()
	now := time.Now()
	ticket := Ticket{ID: "auto-run-one", Title: "Run", Status: TicketStatusWorking, Assignee: "session-1", AutomationRunID: "run-1"}
	first, err := s.EnsureAutomationTicket(ticket, "automation:cleanup", TicketRoleChiefOfStaff, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.EnsureAutomationTicket(ticket, "automation:cleanup", TicketRoleChiefOfStaff, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("adopted %q want %q", second.ID, first.ID)
	}
	events, err := s.TicketEventsSince(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Author != "automation:cleanup" {
		t.Fatalf("events=%#v", events)
	}
	got, err := s.GetTicketByAutomationRunID("run-1")
	if err != nil || got == nil || got.Assignee != "session-1" {
		t.Fatalf("reverse lookup=%#v err=%v", got, err)
	}
}
