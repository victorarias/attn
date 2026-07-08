// Package ticketnotify is the event-driven notification core of the work tracker
// (slice 2 of docs/plans/2026-06-26-work-tracker.md). It is the decoupled layer
// that sits above the store's append-only event log: every identity has a
// per-ticket cursor, events past it are unread, and two handlers turn unread
// events into a notification — a watch-consume for agents that self-monitor
// (Claude), and an idle pty-nudge for those that can't (codex).
//
// Every observer reads through one or more identities. Ordinary assignment,
// authorship, and explicit subscription use session identities. Durable product
// roles use role identities, so their per-ticket cursors survive a change in the
// session filling the role. There is no special "sees everything" observer: each
// identity sees only tickets in its participation scope.
//
// This package re-homes the dispatch gateway's settled mechanics:
//
//	gateway                         here
//	------------------------------  -----------------------------------------
//	ack = output / consume pending  per-(identity, ticket) cursors: events past
//	                                an identity's cursor on a ticket are unread
//	bundle by sender                Consume groups unread events by ticket
//	hardcoded chief id              observer IDs supplied by the caller
//	two consumers (watch / nudge)   the two Delivery paths below
//	never stream content to a PTY   Nudger carries only a fixed trigger
//
// It knows nothing about live sessions or real Monitors — it is built against the
// store and proven by a simulation harness. Slice 3 wires it to real sessions.
package ticketnotify

import (
	"sort"
	"time"

	"github.com/victorarias/attn/internal/store"
)

// ObserverChief is the original simulation-harness identity. Production uses the
// store's durable role identity plus the current chief session as AuthorID and
// DeliveryID.
const ObserverChief = "chief"

// EventStore is the store surface the notifier needs. The real *store.Store
// satisfies it; the harness uses the real store, so there is no mock to drift.
//
// UnreadTicketEvents folds the participant set, the per-(identity, ticket)
// cursors, and the self-author exclusion into one query; SetTicketCursor advances
// a single ticket's cursor for an identity.
type EventStore interface {
	UnreadTicketEventsFor(cursorIdentity, authorIdentity string) ([]store.TicketEvent, error)
	SetTicketCursor(identity, ticketID string, cursor int64, now time.Time) error
}

// Observer is one ticket-event view. ID owns the cursor, AuthorID is excluded as
// self-authored activity, and DeliveryID is the live session to nudge. All three
// are the session ID for ordinary agents; durable roles split them. HasSelfMonitor
// selects watch versus nudge delivery.
type Observer struct {
	ID             string
	AuthorID       string
	DeliveryID     string
	HasSelfMonitor bool
}

// ChiefObserver returns the harness-only chief observer.
func ChiefObserver() Observer {
	return Observer{ID: ObserverChief, AuthorID: ObserverChief, DeliveryID: ObserverChief, HasSelfMonitor: true}
}

// Bundle is an observer's unread events for a single ticket. Consume returns one
// Bundle per ticket, in oldest-activity-first order — the gateway's "bundle by
// sender", re-homed to "bundle by ticket".
type Bundle struct {
	TicketID string
	Events   []store.TicketEvent
}

// Consume returns the observer's unread events, bundled by ticket, and advances
// the observer's cursor on each of those tickets past everything just delivered.
// This is the watch-consume handler: a self-monitoring observer calls it when its
// Monitor fires; a nudged observer calls it after the nudge lands.
//
// Single-consumer-per-observer assumption: Consume reads unread events then writes
// each touched ticket's cursor as separately-locked store calls, so two Consume
// calls racing for the SAME observer can double-deliver. In practice an observer is
// one session with one Monitor, so its consumes are serialized. Slice 3 makes this
// atomic if real concurrency appears.
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

// ConsumeAll consumes several effective identities and merges their output by
// event sequence. A chief session uses this for its ordinary session identity and
// the durable chief role identity; an event in both scopes is delivered once while
// both cursors advance.
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

// UnreadAny counts the union only as a delivery predicate. Callers use it to
// decide whether one session should be notified for any of its effective
// identities; exact deduplication happens when ConsumeAll returns the events.
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
	// DeliveryWatch means a self-monitoring observer's own watch will consume it
	// (true push) — nothing is injected.
	DeliveryWatch
	// DeliveryNudge means an idle, non-self-monitoring observer was pty-nudged to
	// go consume. Only a fixed trigger is sent, never event content.
	DeliveryNudge
	// DeliveryDeferred means a non-self-monitoring observer is busy; the nudge
	// waits until it goes idle.
	DeliveryDeferred
)

// Nudger delivers a fixed wake trigger to an observer that can't self-monitor. It
// carries NO event content — only the bounded "go consume your tickets" trigger,
// mirroring the daemon's doorbell rule (the agent then reads its own queue).
type Nudger interface {
	Nudge(observerID string) error
}

// Notify decides how to deliver an observer's pending events and, for the nudge
// path, fires the Nudger. It does not consume (advance the cursor) — delivery only
// triggers the observer to consume:
//
//   - nothing unread            -> DeliveryNone
//   - self-monitors             -> DeliveryWatch (its own watch consumes)
//   - idle, can't self-monitor  -> DeliveryNudge (fixed trigger; it then consumes)
//   - busy, can't self-monitor  -> DeliveryDeferred (wait for idle)
func Notify(es EventStore, obs Observer, idle bool, nudger Nudger, now time.Time) (Delivery, error) {
	return NotifyAny(es, []Observer{obs}, obs, idle, nudger, now)
}

// NotifyAny makes one delivery decision for a session that observes through more
// than one identity. deliveryObserver supplies the session capability and target;
// the observed identities only determine whether anything is unread.
func NotifyAny(es EventStore, observers []Observer, deliveryObserver Observer, idle bool, nudger Nudger, now time.Time) (Delivery, error) {
	unread, err := UnreadAny(es, observers)
	if err != nil {
		return DeliveryNone, err
	}
	if unread == 0 {
		return DeliveryNone, nil
	}
	if deliveryObserver.HasSelfMonitor {
		return DeliveryWatch, nil
	}
	if !idle {
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

// pending returns the observer's unread events, already bundled by ticket, and the
// per-ticket cursor each bundle's ticket should advance to. The store's
// UnreadTicketEvents has already applied the scope (tickets the identity is
// assigned to or has authored an event on), the per-(identity, ticket) cursors, and
// the self-author exclusion — so this only groups and orders the result.
//
// Because each ticket is compared against the identity's own cursor on THAT ticket
// (default 0), a ticket the identity has never looked at — a fresh assignment, or a
// reassignment to it — is delivered from the start, brief and all. Bundles are
// ordered by their oldest unread event so cross-ticket order stays chronological.
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
