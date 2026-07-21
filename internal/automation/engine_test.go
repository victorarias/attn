package automation

import (
	"testing"
	"time"
)

// fakeBindingStore is the one sanctioned mock-only fake in this package
// (per AGENTS.md's "no mock-only tests except the one sanctioned
// ResolveContinuation fake"): it exists purely to drive ResolveContinuation's
// three branches without a real store.
type fakeBindingStore struct {
	binding       *Binding
	ticketExists  bool
	released      bool
	releaseReason string
}

func (f *fakeBindingStore) GetActiveContinuityBinding(definitionID, continuityKey string) (*Binding, error) {
	return f.binding, nil
}
func (f *fakeBindingStore) ReleaseContinuityBinding(definitionID, continuityKey, reason string, now time.Time) error {
	f.released = true
	f.releaseReason = reason
	return nil
}
func (f *fakeBindingStore) TicketExists(ticketID string) (bool, error) {
	return f.ticketExists, nil
}

func TestResolveContinuationNoActiveBindingIsFresh(t *testing.T) {
	fake := &fakeBindingStore{binding: nil}
	got, err := ResolveContinuation(fake, "def", "key", "own-ticket", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Fresh || got.Binding != nil || got.SelfHealedDanglingBinding {
		t.Fatalf("got %#v, want Fresh with no binding", got)
	}
	if fake.released {
		t.Fatal("no binding to release, but ReleaseContinuityBinding was called")
	}
}

func TestResolveContinuationActiveBindingWithLiveTicketContinues(t *testing.T) {
	binding := &Binding{TicketID: "t1", SessionID: "s1", WorkspaceID: "w1", PaneID: "p1"}
	fake := &fakeBindingStore{binding: binding, ticketExists: true}
	got, err := ResolveContinuation(fake, "def", "key", "own-ticket", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.Fresh || got.Binding == nil || *got.Binding != *binding || got.SelfHealedDanglingBinding {
		t.Fatalf("got %#v, want continuation of %#v", got, binding)
	}
	if fake.released {
		t.Fatal("live ticket, but ReleaseContinuityBinding was called")
	}
}

// TestResolveContinuationOwnBindingIsFreshWithoutRelease pins the fix for the
// hazard where a run's own claim already created the active binding (pointing
// at this run's own reserved, not-yet-created ticket) before
// ResolveContinuation runs: TicketExists on that ticket is false purely
// because it hasn't been created yet, not because it was swept. Releasing
// here would silently break singleton/per_subject continuity on every first
// occurrence, since the very next occurrence would find no binding and start
// a new one instead of continuing this one's still-being-born thread.
func TestResolveContinuationOwnBindingIsFreshWithoutRelease(t *testing.T) {
	binding := &Binding{TicketID: "own-ticket", SessionID: "s1", WorkspaceID: "w1", PaneID: "p1"}
	fake := &fakeBindingStore{binding: binding, ticketExists: false}
	got, err := ResolveContinuation(fake, "def", "key", "own-ticket", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Fresh || got.Binding != nil || got.SelfHealedDanglingBinding {
		t.Fatalf("got %#v, want plain Fresh (no self-heal flag)", got)
	}
	if fake.released {
		t.Fatal("own not-yet-created ticket, but ReleaseContinuityBinding was called")
	}
}

func TestResolveContinuationDanglingBindingSelfHeals(t *testing.T) {
	binding := &Binding{TicketID: "t1", SessionID: "s1", WorkspaceID: "w1", PaneID: "p1"}
	fake := &fakeBindingStore{binding: binding, ticketExists: false}
	got, err := ResolveContinuation(fake, "def", "key", "own-ticket", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Fresh || got.Binding != nil || !got.SelfHealedDanglingBinding {
		t.Fatalf("got %#v, want a self-healed Fresh delivery", got)
	}
	if !fake.released || fake.releaseReason != "ticket_swept" {
		t.Fatalf("release called=%v reason=%q, want ticket_swept", fake.released, fake.releaseReason)
	}
}
