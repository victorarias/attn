package store

import (
	"testing"
	"time"
)

// The machine-reconciliation flag (orphaned-ticket reconciliation): claim is a
// true set-if-unset, clears re-arm it, and the multi-ticket session query feeds
// the session-end seam.

func TestClaimTicketReconciliation_SetIfUnset(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	if _, err := s.CreateTicket(Ticket{ID: "orphan-1", Title: "t", Assignee: "sess-1", Status: TicketStatusInReview}, "chief", ticketBase); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	claimTime := ticketBase.Add(time.Hour)
	claimed, err := s.ClaimTicketReconciliation("orphan-1", claimTime)
	if err != nil {
		t.Fatalf("ClaimTicketReconciliation: %v", err)
	}
	if !claimed {
		t.Fatal("first claim = false, want true")
	}

	// Second claim (death-hook double-fire, or hook-vs-sweep race) loses.
	claimed, err = s.ClaimTicketReconciliation("orphan-1", claimTime.Add(time.Minute))
	if err != nil {
		t.Fatalf("second ClaimTicketReconciliation: %v", err)
	}
	if claimed {
		t.Fatal("second claim = true, want false (flag already set)")
	}

	got, err := s.GetTicket("orphan-1")
	if err != nil || got == nil {
		t.Fatalf("GetTicket: %v, %v", got, err)
	}
	if got.ReconciledAt == nil || !got.ReconciledAt.Equal(claimTime) {
		t.Fatalf("ReconciledAt = %v, want first claim time %v (loser must not overwrite)", got.ReconciledAt, claimTime)
	}
	// The claim is internal bookkeeping: no board churn.
	if !got.UpdatedAt.Equal(ticketBase) {
		t.Fatalf("UpdatedAt = %v, want untouched %v", got.UpdatedAt, ticketBase)
	}
}

func TestClaimTicketReconciliation_MissingTicket(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	claimed, err := s.ClaimTicketReconciliation("nope", ticketBase)
	if err != nil {
		t.Fatalf("ClaimTicketReconciliation: %v", err)
	}
	if claimed {
		t.Fatal("claim on missing ticket = true, want false")
	}
}

func TestClearTicketReconciliationForAssignee(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	for _, id := range []string{"flag-a", "flag-b"} {
		if _, err := s.CreateTicket(Ticket{ID: id, Title: "t", Assignee: "sess-1", Status: TicketStatusWorking}, "chief", ticketBase); err != nil {
			t.Fatalf("CreateTicket %s: %v", id, err)
		}
		if _, err := s.ClaimTicketReconciliation(id, ticketBase.Add(time.Hour)); err != nil {
			t.Fatalf("claim %s: %v", id, err)
		}
	}

	if err := s.ClearTicketReconciliationForAssignee("sess-1"); err != nil {
		t.Fatalf("ClearTicketReconciliationForAssignee: %v", err)
	}
	for _, id := range []string{"flag-a", "flag-b"} {
		got, err := s.GetTicket(id)
		if err != nil || got == nil {
			t.Fatalf("GetTicket %s: %v, %v", id, got, err)
		}
		if got.ReconciledAt != nil {
			t.Fatalf("%s ReconciledAt = %v, want cleared", id, got.ReconciledAt)
		}
	}

	// Cleared flags can be claimed again — the re-arm contract.
	claimed, err := s.ClaimTicketReconciliation("flag-a", ticketBase.Add(2*time.Hour))
	if err != nil || !claimed {
		t.Fatalf("re-claim after clear = %v, %v; want true, nil", claimed, err)
	}
}

func TestAssignTicketClearsReconciliation(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	if _, err := s.CreateTicket(Ticket{ID: "retaken", Title: "t", Assignee: "sess-1", Status: TicketStatusWorking}, "chief", ticketBase); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if _, err := s.ClaimTicketReconciliation("retaken", ticketBase.Add(time.Hour)); err != nil {
		t.Fatalf("claim: %v", err)
	}

	if err := s.AssignTicket("retaken", "sess-2", "sess-2", ticketBase.Add(2*time.Hour)); err != nil {
		t.Fatalf("AssignTicket: %v", err)
	}
	got, err := s.GetTicket("retaken")
	if err != nil || got == nil {
		t.Fatalf("GetTicket: %v, %v", got, err)
	}
	if got.Assignee != "sess-2" {
		t.Fatalf("Assignee = %q, want sess-2", got.Assignee)
	}
	if got.ReconciledAt != nil {
		t.Fatalf("ReconciledAt = %v, want cleared on reassign (re-arm)", got.ReconciledAt)
	}
}

func TestActiveTicketsForSession_ReturnsAllNonTerminal(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	// Two non-terminal, one terminal, one other-session: the seam must see
	// exactly the two non-terminal bound tickets, newest first.
	if _, err := s.CreateTicket(Ticket{ID: "older", Title: "t", Assignee: "sess-1", Status: TicketStatusWorking}, "chief", ticketBase); err != nil {
		t.Fatalf("CreateTicket older: %v", err)
	}
	if _, err := s.CreateTicket(Ticket{ID: "newer", Title: "t", Assignee: "sess-1", Status: TicketStatusInReview}, "chief", ticketBase.Add(time.Minute)); err != nil {
		t.Fatalf("CreateTicket newer: %v", err)
	}
	if _, err := s.CreateTicket(Ticket{ID: "settled", Title: "t", Assignee: "sess-1", Status: TicketStatusDone}, "chief", ticketBase.Add(2*time.Minute)); err != nil {
		t.Fatalf("CreateTicket settled: %v", err)
	}
	if _, err := s.CreateTicket(Ticket{ID: "other", Title: "t", Assignee: "sess-2", Status: TicketStatusWorking}, "chief", ticketBase.Add(3*time.Minute)); err != nil {
		t.Fatalf("CreateTicket other: %v", err)
	}

	tickets, err := s.ActiveTicketsForSession("sess-1")
	if err != nil {
		t.Fatalf("ActiveTicketsForSession: %v", err)
	}
	if len(tickets) != 2 {
		t.Fatalf("len = %d, want 2 (%+v)", len(tickets), tickets)
	}
	if tickets[0].ID != "newer" || tickets[1].ID != "older" {
		t.Fatalf("order = %s, %s; want newer, older", tickets[0].ID, tickets[1].ID)
	}

	if empty, err := s.ActiveTicketsForSession(""); err != nil || empty != nil {
		t.Fatalf("empty session id: %v, %v; want nil, nil", empty, err)
	}
}
