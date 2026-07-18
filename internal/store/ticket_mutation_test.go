package store

import (
	"errors"
	"testing"
	"time"
)

func TestTicketMutationConsumesTargetUnreadBeforeMutating(t *testing.T) {
	s := New()
	defer s.Close()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	if _, err := s.CreateTicket(Ticket{ID: "target", Title: "Target", Assignee: "worker", Status: TicketStatusWorking}, "chief", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTicket(Ticket{ID: "other", Title: "Other", Assignee: "worker", Status: TicketStatusWorking}, "chief", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTicketComment("target", "chief", "new target context", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTicketComment("other", "chief", "unrelated context", now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	options := TicketMutationOptions{
		Observers:    []TicketMutationObserver{{CursorIdentity: "worker", AuthorIdentity: "worker"}},
		AttentionKey: "worker",
	}

	updated, outcome, err := s.SetTicketStatusWithOptions("target", TicketStatusDone, "worker", "done", options, now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if updated != nil || len(outcome.ConflictEvents) != 1 || outcome.ConflictEvents[0].Comment != "new target context" {
		t.Fatalf("first attempt updated=%+v outcome=%+v", updated, outcome)
	}
	target, _ := s.GetTicket("target")
	if target.Status != TicketStatusWorking {
		t.Fatalf("conflicting attempt changed status to %s", target.Status)
	}
	otherUnread, err := s.UnreadTicketEventsFor("worker", "worker")
	if err != nil {
		t.Fatal(err)
	}
	if len(otherUnread) != 1 || otherUnread[0].TicketID != "other" {
		t.Fatalf("unrelated unread = %+v", otherUnread)
	}
	attention, found, err := s.TicketDeliveryAttention("worker")
	if err != nil || !found || !attention.LastAttentionAt.Equal(now.Add(3*time.Second)) {
		t.Fatalf("attention=%+v found=%v err=%v", attention, found, err)
	}

	updated, outcome, err = s.SetTicketStatusWithOptions("target", TicketStatusDone, "worker", "done", options, now.Add(4*time.Second))
	if err != nil || updated == nil || len(outcome.ConflictEvents) != 0 || updated.Status != TicketStatusDone {
		t.Fatalf("retry updated=%+v outcome=%+v err=%v", updated, outcome, err)
	}
}

func TestTicketMutationRejectsStaleExpectedEventSeq(t *testing.T) {
	s := New()
	defer s.Close()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	if _, err := s.CreateTicket(Ticket{ID: "target", Title: "Target", Status: TicketStatusWorking}, "chief", now); err != nil {
		t.Fatal(err)
	}
	detail, err := s.GetTicket("target")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTicketComment("target", "worker", "landed after detail", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	expected := detail.LatestEventSeq
	_, _, err = s.SetTicketStatusWithOptions(
		"target", TicketStatusDone, TicketAuthorYou, "",
		TicketMutationOptions{ExpectedEventSeq: &expected}, now.Add(2*time.Second),
	)
	if !errors.Is(err, ErrStaleTicketEventSeq) {
		t.Fatalf("error = %v, want stale sequence", err)
	}
	got, _ := s.GetTicket("target")
	if got.Status != TicketStatusWorking {
		t.Fatalf("stale mutation changed status to %s", got.Status)
	}
}
