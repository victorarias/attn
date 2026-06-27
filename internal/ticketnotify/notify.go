// Package ticketnotify is the event-driven notification core of the work tracker
// (slice 2 of docs/plans/2026-06-26-work-tracker.md). It is the decoupled layer
// that sits above the store's append-only event log: every identity has a
// per-ticket cursor, events past it are unread, and two handlers turn unread
// events into a notification — a watch-consume for agents that self-monitor
// (Claude), and an idle pty-nudge for those that can't (codex).
//
// Identity is uniform: you, the chief, and each agent are all just identities (the
// chief is one of the agents). There is no special "sees everything" observer —
// an identity is involved with a ticket when it is assigned to it or has authored
// an event on it (the chief authors the created event when it delegates, so it
// stays aware of its own delegations without a special case). Each identity has
// its OWN cursor PER ticket, so a ticket newly assigned to an agent arrives with
// its full history (the brief, prior steers) and an agent's progress on one ticket
// never skips another it has not looked at yet.
//
// This package re-homes the dispatch gateway's settled mechanics:
//
//	gateway                         here
//	------------------------------  -----------------------------------------
//	ack = output / consume pending  per-(identity, ticket) cursors: events past
//	                                an identity's cursor on a ticket are unread
//	bundle by sender                Consume groups unread events by ticket
//	hardcoded chief id              ObserverChief, a well-known literal
//	two consumers (watch / nudge)   the two Delivery paths below
//	never stream content to a PTY   Nudger carries only a fixed trigger
//
// It knows nothing about live sessions or real Monitors — it is built against the
// store and proven by a simulation harness. Slice 3 wires it to real sessions.
package ticketnotify

import (
	"sort"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/store"
)

// ObserverChief is the well-known chief identity — the chief is just one of the
// agents, but its id is fixed (it has no spawned session id) so addressing it
// needs no lookup. It enjoys no special scope: like any identity it sees events on
// the tickets it is involved with (the ones it delegated/authored, plus any
// assigned to it). Slice 3 supplies the real chief session id.
const ObserverChief = "chief"

// EventStore is the store surface the notifier needs. The real *store.Store
// satisfies it; the harness uses the real store, so there is no mock to drift.
//
// UnreadTicketEvents folds the participant set, the per-(identity, ticket)
// cursors, and the self-author exclusion into one query; SetTicketCursor advances
// a single ticket's cursor for an identity.
type EventStore interface {
	UnreadTicketEvents(identity string) ([]store.TicketEvent, error)
	SetTicketCursor(identity, ticketID string, cursor int64, now time.Time) error
}

// Observer is a subscriber to ticket events. ID is ObserverChief or an agent's
// session id. HasSelfMonitor picks the delivery path: a self-monitoring agent's
// own watch consumes; others are nudged when idle.
type Observer struct {
	ID             string
	HasSelfMonitor bool
}

// ChiefObserver returns the well-known chief observer. The chief is a Claude
// session, so it self-monitors.
func ChiefObserver() Observer {
	return Observer{ID: ObserverChief, HasSelfMonitor: true}
}

// AgentObserver returns an observer for a delegated agent session.
func AgentObserver(sessionID, agent string) Observer {
	return Observer{ID: sessionID, HasSelfMonitor: HasSelfMonitor(agent)}
}

// HasSelfMonitor reports whether an agent can watch its own events (true push via
// a Monitor) or must be polled/nudged. Only Claude self-monitors today; codex and
// the rest fall back to the idle nudge — matching the vision's split.
func HasSelfMonitor(agent string) bool {
	return strings.EqualFold(strings.TrimSpace(agent), "claude")
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
	unread, err := Unread(es, obs)
	if err != nil {
		return DeliveryNone, err
	}
	if unread == 0 {
		return DeliveryNone, nil
	}
	if obs.HasSelfMonitor {
		return DeliveryWatch, nil
	}
	if !idle {
		return DeliveryDeferred, nil
	}
	if err := nudger.Nudge(obs.ID); err != nil {
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
	events, err := es.UnreadTicketEvents(obs.ID)
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
