package store

import (
	"errors"
	"testing"
	"time"
)

// A subscription is the third participation source: an identity that is neither the
// assignee nor an author becomes a participant by subscribing — reached by the
// notifier (TicketParticipants) and served the ticket's events (UnreadTicketEvents),
// including the backlog, since subscribing does not advance the cursor. Unsubscribing
// fully reverses both.
func TestTicketSubscriptionMakesParticipant(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	tick := eventBase
	next := func() time.Time { tick = tick.Add(time.Minute); return tick }

	if _, err := s.CreateTicket(Ticket{ID: "tk", Title: "work", Assignee: "agent7"}, "chief", next()); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if _, err := s.SetTicketStatus("tk", TicketStatusInReview, "agent7", "ready", next()); err != nil {
		t.Fatalf("SetTicketStatus: %v", err)
	}

	// A watcher with no prior tie to the ticket sees nothing yet.
	if got, err := s.UnreadTicketEvents("watcher"); err != nil || len(got) != 0 {
		t.Fatalf("pre-subscribe unread = %d (err %v), want 0", len(got), err)
	}

	if err := s.AddTicketSubscription("watcher", "tk", next()); err != nil {
		t.Fatalf("AddTicketSubscription: %v", err)
	}

	// Now a participant: in the notifier's set...
	parts, err := s.TicketParticipants("tk")
	if err != nil {
		t.Fatalf("TicketParticipants: %v", err)
	}
	if !contains(parts, "watcher") {
		t.Fatalf("participants = %v, want to include watcher", parts)
	}
	// ...and served the ticket's history (it authored none of these events).
	unread, err := s.UnreadTicketEvents("watcher")
	if err != nil {
		t.Fatalf("UnreadTicketEvents: %v", err)
	}
	if len(unread) == 0 {
		t.Fatal("subscriber has 0 unread events, want the ticket's backlog")
	}
	for _, e := range unread {
		if e.TicketID != "tk" {
			t.Fatalf("subscriber unread carried foreign ticket %q", e.TicketID)
		}
	}

	// Unsubscribing reverses both halves.
	if err := s.RemoveTicketSubscription("watcher", "tk"); err != nil {
		t.Fatalf("RemoveTicketSubscription: %v", err)
	}
	if parts, _ := s.TicketParticipants("tk"); contains(parts, "watcher") {
		t.Fatalf("participants = %v, want watcher removed after unsubscribe", parts)
	}
	if got, err := s.UnreadTicketEvents("watcher"); err != nil || len(got) != 0 {
		t.Fatalf("post-unsubscribe unread = %d (err %v), want 0", len(got), err)
	}
}

// Subscribe is idempotent and guards the ticket id; unsubscribe is a tolerant
// removal that never errors on a missing subscription.
func TestSubscribeIdempotentAndValidatesTicket(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	now := eventBase
	if _, err := s.CreateTicket(Ticket{ID: "tk", Title: "work"}, "chief", now); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	if err := s.AddTicketSubscription("watcher", "tk", now); err != nil {
		t.Fatalf("first subscribe: %v", err)
	}
	// Re-subscribing is a no-op, not an error or a duplicate.
	if err := s.AddTicketSubscription("watcher", "tk", now); err != nil {
		t.Fatalf("second subscribe: %v", err)
	}
	if ok, err := s.IsTicketSubscribed("watcher", "tk"); err != nil || !ok {
		t.Fatalf("IsTicketSubscribed = (%v, %v), want (true, nil)", ok, err)
	}

	// Subscribing to a phantom ticket is a clear error, not a silent dropped row.
	if err := s.AddTicketSubscription("watcher", "ghost", now); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("subscribe to missing ticket = %v, want ErrTicketNotFound", err)
	}

	// Unsubscribing twice — and from something never subscribed — both succeed.
	if err := s.RemoveTicketSubscription("watcher", "tk"); err != nil {
		t.Fatalf("first unsubscribe: %v", err)
	}
	if err := s.RemoveTicketSubscription("watcher", "tk"); err != nil {
		t.Fatalf("idempotent unsubscribe: %v", err)
	}
	if err := s.RemoveTicketSubscription("nobody", "ghost"); err != nil {
		t.Fatalf("unsubscribe from never-subscribed: %v", err)
	}
	if ok, err := s.IsTicketSubscribed("watcher", "tk"); err != nil || ok {
		t.Fatalf("IsTicketSubscribed after unsubscribe = (%v, %v), want (false, nil)", ok, err)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
