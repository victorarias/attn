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
	if updated != nil || !outcome.Blocked || len(outcome.CatchUp) != 1 || outcome.CatchUp[0].Comment != "new target context" {
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
	if err != nil || updated == nil || outcome.Blocked || len(outcome.CatchUp) != 0 || updated.Status != TicketStatusDone {
		t.Fatalf("retry updated=%+v outcome=%+v err=%v", updated, outcome, err)
	}
}

// attn's own bookkeeping is delivered, not charged for: an agent whose ticket
// carries only attn-authored unread events writes on the first attempt and gets
// those events back beside the result.
func TestTicketMutationAppliesThroughAttnAuthoredCatchUp(t *testing.T) {
	s := New()
	defer s.Close()
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	if _, err := s.CreateTicket(Ticket{ID: "target", Title: "Target", Assignee: "worker", Status: TicketStatusWorking}, "chief", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetTicketStatus("target", TicketStatusCrashed, TicketAuthorAttn, "agent process ended mid-flight without reporting", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetTicketStatus("target", TicketStatusWorking, TicketAuthorAttn, "session was reloaded and is running again", now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	options := TicketMutationOptions{
		Observers:    []TicketMutationObserver{{CursorIdentity: "worker", AuthorIdentity: "worker"}},
		AttentionKey: "worker",
	}

	updated, outcome, err := s.SetTicketStatusWithOptions("target", TicketStatusInReview, "worker", "PR is up", options, now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Blocked || updated == nil || updated.Status != TicketStatusInReview {
		t.Fatalf("first attempt updated=%+v outcome=%+v", updated, outcome)
	}
	if len(outcome.CatchUp) != 2 {
		t.Fatalf("catch-up = %+v, want the crash and revive records delivered", outcome.CatchUp)
	}
	unread, err := s.UnreadTicketEventsFor("worker", "worker")
	if err != nil {
		t.Fatal(err)
	}
	if len(unread) != 0 {
		t.Fatalf("unread after delivery = %+v, want the cursor advanced", unread)
	}
}

// A peer's word in the same unread batch still costs the write: the agent must
// read it before it may report over it.
func TestTicketMutationBlocksWhenAttnCatchUpCarriesAPeerEvent(t *testing.T) {
	s := New()
	defer s.Close()
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	if _, err := s.CreateTicket(Ticket{ID: "target", Title: "Target", Assignee: "worker", Status: TicketStatusWorking}, "chief", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetTicketStatus("target", TicketStatusCrashed, TicketAuthorAttn, "agent process ended mid-flight without reporting", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTicketComment("target", "chief", "scope changed while you were down", now.Add(2*time.Second)); err != nil {
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
	if updated != nil || !outcome.Blocked || len(outcome.CatchUp) != 2 {
		t.Fatalf("updated=%+v outcome=%+v, want blocked with both records shown", updated, outcome)
	}
	target, _ := s.GetTicket("target")
	if target.Status != TicketStatusCrashed {
		t.Fatalf("blocked mutation changed status to %s", target.Status)
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
