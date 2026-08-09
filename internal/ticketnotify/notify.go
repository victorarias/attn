// Package ticketnotify is the event-driven notification core of the work
// tracker (slice 2 of docs/plans/2026-06-26-work-tracker.md): every identity
// has a per-ticket cursor over the store's append-only event log, events past
// it are unread, and a shared pty-nudge asks the owning session to consume
// them. Role identities keep a durable role's cursors across session changes;
// no identity sees beyond its participation scope.
package ticketnotify

import (
	"sort"
	"time"

	"github.com/victorarias/attn/internal/store"
)

// EventStore is the store surface the notifier needs; the real *store.Store
// satisfies it and the harness uses it, so there is no mock to drift.
type EventStore interface {
	UnreadTicketEventsFor(cursorIdentity, authorIdentity string) ([]store.TicketEvent, error)
	SetTicketCursor(identity, ticketID string, cursor int64, now time.Time) error
}

// Observer is one ticket-event view. ID owns the cursor, AuthorID is excluded as
// self-authored activity, and DeliveryID is the live session to nudge. All three
// are the session ID for ordinary agents; durable roles split them.
type Observer struct {
	ID         string
	AuthorID   string
	DeliveryID string
}

// Bundle is an observer's unread events for a single ticket.
type Bundle struct {
	TicketID string
	Events   []store.TicketEvent
}

// Consume returns the observer's unread events bundled by ticket and advances
// the cursor on each. Not atomic: two Consumes racing for the SAME observer —
// or a crash mid-ConsumeAll — can double-deliver, never lose. In practice an
// observer is one session with one Monitor, so its consumes are serialized.
func Consume(es EventStore, obs Observer, now time.Time) ([]Bundle, error) {
	bundles, advance, err := pending(es, obs)
	if err != nil {
		return nil, err
	}
	for ticketID, seq := range advance {
		if err := es.SetTicketCursor(obs.ID, ticketID, seq, now); err != nil {
			return nil, err
		}
	}
	return bundles, nil
}

// ConsumeAll consumes several effective identities and merges by event
// sequence; an event in more than one scope delivers once, every cursor moves.
func ConsumeAll(es EventStore, observers []Observer, now time.Time) ([]Bundle, error) {
	byTicket := map[string]map[int64]store.TicketEvent{}
	for _, obs := range observers {
		bundles, err := Consume(es, obs, now)
		if err != nil {
			return nil, err
		}
		for _, bundle := range bundles {
			if byTicket[bundle.TicketID] == nil {
				byTicket[bundle.TicketID] = map[int64]store.TicketEvent{}
			}
			for _, event := range bundle.Events {
				byTicket[bundle.TicketID][event.Seq] = event
			}
		}
	}
	merged := make([]Bundle, 0, len(byTicket))
	for ticketID, events := range byTicket {
		bundle := Bundle{TicketID: ticketID, Events: make([]store.TicketEvent, 0, len(events))}
		for _, event := range events {
			bundle.Events = append(bundle.Events, event)
		}
		sort.Slice(bundle.Events, func(i, j int) bool { return bundle.Events[i].Seq < bundle.Events[j].Seq })
		merged = append(merged, bundle)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].Events[0].Seq < merged[j].Events[0].Seq })
	return merged, nil
}

// Unread counts the observer's unread events without consuming them.
func Unread(es EventStore, obs Observer) (int, error) {
	bundles, _, err := pending(es, obs)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, b := range bundles {
		n += len(b.Events)
	}
	return n, nil
}

// UnreadAny is a delivery predicate: whether any effective identity has unread
// events. Exact deduplication happens in ConsumeAll.
func UnreadAny(es EventStore, observers []Observer) (int, error) {
	hasUnread := false
	for _, obs := range observers {
		n, err := Unread(es, obs)
		if err != nil {
			return 0, err
		}
		if n > 0 {
			hasUnread = true
		}
	}
	if hasUnread {
		return 1, nil
	}
	return 0, nil
}

// Delivery is how the notifier decided to reach an observer about pending events.
type Delivery int

const (
	// DeliveryNone means there was nothing unread.
	DeliveryNone Delivery = iota
	// DeliveryNudge means a nudge-eligible observer was asked to consume
	// (fixed trigger only, never event content).
	DeliveryNudge
	// DeliveryDeferred means the observer is waiting for approval — a doorbell
	// could answer that prompt. A later eligible state rechecks it.
	DeliveryDeferred
)

// Nudger delivers a fixed wake trigger. It carries NO event content — only the
// bounded "go consume your tickets" trigger, mirroring the daemon's doorbell rule.
type Nudger interface {
	Nudge(observerID string) error
}

// Notify decides how to deliver an observer's pending events, firing the
// Nudger on the nudge path. It never consumes — delivery only triggers that.
func Notify(es EventStore, obs Observer, nudgeEligible bool, nudger Nudger, now time.Time) (Delivery, error) {
	return NotifyAny(es, []Observer{obs}, obs, nudgeEligible, nudger, now)
}

// NotifyAny makes one delivery decision for a session observing through more
// than one identity; deliveryObserver supplies the nudge target.
func NotifyAny(es EventStore, observers []Observer, deliveryObserver Observer, nudgeEligible bool, nudger Nudger, now time.Time) (Delivery, error) {
	unread, err := UnreadAny(es, observers)
	if err != nil {
		return DeliveryNone, err
	}
	if unread == 0 {
		return DeliveryNone, nil
	}
	if !nudgeEligible {
		return DeliveryDeferred, nil
	}
	deliveryID := deliveryObserver.DeliveryID
	if deliveryID == "" {
		deliveryID = deliveryObserver.ID
	}
	if err := nudger.Nudge(deliveryID); err != nil {
		return DeliveryNone, err
	}
	return DeliveryNudge, nil
}

// pending groups the store's already-scoped unread events by ticket and
// computes each ticket's advance cursor. A ticket the identity has never
// looked at (cursor 0) delivers from the start, brief and all; bundles order
// by oldest unread event so cross-ticket order stays chronological.
func pending(es EventStore, obs Observer) (bundles []Bundle, advance map[string]int64, err error) {
	authorID := obs.AuthorID
	if authorID == "" {
		authorID = obs.ID
	}
	events, err := es.UnreadTicketEventsFor(obs.ID, authorID)
	if err != nil {
		return nil, nil, err
	}
	advance = map[string]int64{}
	index := map[string]int{}
	for _, e := range events {
		i, ok := index[e.TicketID]
		if !ok {
			i = len(bundles)
			index[e.TicketID] = i
			bundles = append(bundles, Bundle{TicketID: e.TicketID})
		}
		bundles[i].Events = append(bundles[i].Events, e)
		if e.Seq > advance[e.TicketID] {
			advance[e.TicketID] = e.Seq
		}
	}
	sort.SliceStable(bundles, func(i, j int) bool {
		return bundles[i].Events[0].Seq < bundles[j].Events[0].Seq
	})
	return bundles, advance, nil
}
