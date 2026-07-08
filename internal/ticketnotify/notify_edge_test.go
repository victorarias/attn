package ticketnotify

import (
	"errors"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/store"
)

var edgeBase = time.Date(2026, 6, 26, 8, 0, 0, 0, time.UTC)

// failingNudger reports a nudge attempt and fails.
type failingNudger struct{ called bool }

func (n *failingNudger) Nudge(string) error { n.called = true; return errors.New("pty gone") }

// erroringStore is an EventStore that fails on a chosen method, to exercise error
// propagation through Consume / Unread / Notify. UnreadTicketEvents returns one
// unread event (when not failing) so Consume reaches the cursor-write path.
type erroringStore struct {
	failUnread bool
	failSet    bool
}

var errBoom = errors.New("boom")

func (e erroringStore) UnreadTicketEventsFor(string, string) ([]store.TicketEvent, error) {
	if e.failUnread {
		return nil, errBoom
	}
	return []store.TicketEvent{{Seq: 1, TicketID: "tk", Kind: store.TicketEventCommented, Author: "agent7"}}, nil
}
func (e erroringStore) SetTicketCursor(string, string, int64, time.Time) error {
	if e.failSet {
		return errBoom
	}
	return nil
}

func TestNotifyNudgeError(t *testing.T) {
	h := newHarness(t)
	codex := Observer{ID: "codexbot", HasSelfMonitor: false}
	h.create("alpha", "codexbot", ObserverChief)
	h.comment("alpha", ObserverChief, "steer")

	n := &failingNudger{}
	d, err := Notify(h.s, codex, true, n, h.tick())
	if !n.called {
		t.Fatal("nudger was not called")
	}
	if err == nil {
		t.Fatal("Notify swallowed the nudge error")
	}
	if d != DeliveryNone {
		t.Fatalf("delivery on nudge failure = %v, want None", d)
	}
}

func TestNotifyAndConsumePropagateStoreErrors(t *testing.T) {
	obs := ChiefObserver()
	now := edgeBase

	if _, err := Consume(erroringStore{failUnread: true}, obs, now); !errors.Is(err, errBoom) {
		t.Fatalf("Consume(failUnread) error = %v, want boom", err)
	}
	// failSet exercises the cursor-write path: pending returns an unread event, so
	// Consume reaches SetTicketCursor, which errors.
	if _, err := Consume(erroringStore{failSet: true}, obs, now); !errors.Is(err, errBoom) {
		t.Fatalf("Consume(failSet) error = %v, want boom", err)
	}
	if _, err := Unread(erroringStore{failUnread: true}, obs); !errors.Is(err, errBoom) {
		t.Fatalf("Unread error = %v, want boom", err)
	}
	if _, err := Notify(erroringStore{failUnread: true}, obs, true, &failingNudger{}, now); !errors.Is(err, errBoom) {
		t.Fatalf("Notify error = %v, want boom", err)
	}
}

func TestConsumeEmptyStore(t *testing.T) {
	h := newHarness(t)
	chief := ChiefObserver()

	bundles, err := Consume(h.s, chief, h.tick())
	if err != nil || bundles != nil {
		t.Fatalf("empty Consume = %+v (err %v), want nil", bundles, err)
	}
	if n, err := Unread(h.s, chief); err != nil || n != 0 {
		t.Fatalf("empty Unread = %d (err %v), want 0", n, err)
	}
	d, err := Notify(h.s, chief, true, h, h.tick())
	if err != nil || d != DeliveryNone {
		t.Fatalf("empty Notify = %v (err %v), want None", d, err)
	}
	if len(h.nudges) != 0 {
		t.Fatalf("nudges on empty store = %v, want none", h.nudges)
	}
}

// The chief consumes the three event kinds the main harness never drives:
// assigned, description_edited, attachment_added (authored by an agent, so the
// chief — which excludes its own events — observes them).
func TestConsumeOtherEventKinds(t *testing.T) {
	h := newHarness(t)
	chief := ChiefObserver()

	h.create("alpha", "agent7", ObserverChief)
	if err := h.s.AssignTicket("alpha", "agent9", "agent7", h.tick()); err != nil {
		t.Fatalf("AssignTicket: %v", err)
	}
	if err := h.s.EditTicketDescription("alpha", "tighter brief", "agent7", h.tick()); err != nil {
		t.Fatalf("EditTicketDescription: %v", err)
	}
	if _, err := h.s.AddTicketAttachment(store.TicketAttachment{TicketID: "alpha", Filename: "diff.patch"}, "agent7", h.tick()); err != nil {
		t.Fatalf("AddTicketAttachment: %v", err)
	}

	bundles, err := Consume(h.s, chief, h.tick())
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if len(bundles) != 1 || bundles[0].TicketID != "alpha" {
		t.Fatalf("bundles = %+v, want one for alpha", bundles)
	}
	seen := map[store.TicketEventKind]string{}
	for _, e := range bundles[0].Events {
		seen[e.Kind] = e.Detail
	}
	if seen[store.TicketEventAssigned] != "agent9" {
		t.Fatalf("assigned not consumed with detail: %+v", seen)
	}
	if seen[store.TicketEventDescriptionEdited] != "tighter brief" {
		t.Fatalf("description_edited not consumed with detail: %+v", seen)
	}
	if seen[store.TicketEventAttachmentAdded] != "diff.patch" {
		t.Fatalf("attachment_added not consumed with detail: %+v", seen)
	}
}
