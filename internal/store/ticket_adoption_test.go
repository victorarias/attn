package store

import (
	"errors"
	"testing"
	"time"
)

func TestAdoptTicketForDelegationMovesAndBindsExistingTicket(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	now := ticketBase.Add(time.Hour)
	if _, err := s.CreateTicket(Ticket{
		ID: "planned", Title: "Planned work", Description: "Complete implementation brief.",
		Status: TicketStatusDone,
	}, "you", ticketBase); err != nil {
		t.Fatal(err)
	}

	adopted, err := s.AdoptTicketForDelegation(
		"planned", "session-new", "/repo/worktree", "codex", "session-chief",
		TicketRoleChiefOfStaff, []string{"session-chief"}, false, now,
	)
	if err != nil {
		t.Fatalf("AdoptTicketForDelegation: %v", err)
	}
	if adopted.ID != "planned" || adopted.Assignee != "session-new" || adopted.Status != TicketStatusWorking {
		t.Fatalf("adopted = %+v", adopted)
	}
	if adopted.Cwd != "/repo/worktree" || adopted.LastAgentID != "codex" || adopted.ClosedAt != nil {
		t.Fatalf("session metadata = %+v", adopted)
	}
	cursor, err := s.GetTicketCursor("session-new", "planned")
	if err != nil || cursor != adopted.LatestEventSeq {
		t.Fatalf("cursor = %d, latest = %d, err = %v", cursor, adopted.LatestEventSeq, err)
	}
	participants, err := s.TicketParticipants("planned")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"session-new": true, "session-chief": true, TicketRoleIdentity(TicketRoleChiefOfStaff): true}
	for _, participant := range participants {
		delete(want, participant)
	}
	if len(want) != 0 {
		t.Fatalf("participants = %v, missing %v", participants, want)
	}
}

func TestAdoptTicketForDelegationUsesOrphanStampAsConfirmation(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.CreateTicket(Ticket{
		ID: "orphan", Title: "Orphan", Description: "Resume this work.",
		Status: TicketStatusInReview, Assignee: "session-old",
	}, "session-old", ticketBase); err != nil {
		t.Fatal(err)
	}

	_, err := s.AdoptTicketForDelegation("orphan", "session-new", "/repo", "codex", "chief", "", nil, false, ticketBase.Add(time.Minute))
	if !errors.Is(err, ErrTicketAdoptionConfirmRequired) {
		t.Fatalf("live takeover error = %v, want ErrTicketAdoptionConfirmRequired", err)
	}
	if claimed, err := s.ClaimTicketReconciliation("orphan", ticketBase.Add(2*time.Minute)); err != nil || !claimed {
		t.Fatalf("ClaimTicketReconciliation = %v, %v", claimed, err)
	}
	adopted, err := s.AdoptTicketForDelegation("orphan", "session-new", "/repo", "codex", "chief", "", nil, false, ticketBase.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("orphan adoption: %v", err)
	}
	if adopted.Assignee != "session-new" || adopted.ReconciledAt != nil || adopted.Status != TicketStatusWorking {
		t.Fatalf("adopted orphan = %+v", adopted)
	}
	subscribed, err := s.IsTicketSubscribed("session-old", "orphan")
	if err != nil || !subscribed {
		t.Fatalf("previous assignee subscription = %v, %v", subscribed, err)
	}
}

func TestAdoptTicketForDelegationConfirmAndDescriptionValidation(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.CreateTicket(Ticket{ID: "live", Title: "Live", Description: "Take over.", Assignee: "session-old"}, "you", ticketBase); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdoptTicketForDelegation("live", "session-new", "/repo", "codex", "chief", "", nil, true, ticketBase.Add(time.Minute)); err != nil {
		t.Fatalf("confirmed adoption: %v", err)
	}
	if _, err := s.CreateTicket(Ticket{ID: "empty", Title: "Empty"}, "you", ticketBase); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdoptTicketForDelegation("empty", "session-new", "/repo", "codex", "chief", "", nil, false, ticketBase.Add(time.Minute)); err == nil {
		t.Fatal("empty description adoption succeeded")
	}
}
