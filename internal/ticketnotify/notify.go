// Package ticketnotify is the event-driven notification core of the work tracker
// (slice 2 of docs/plans/2026-06-26-work-tracker.md). It is the decoupled layer
// that sits above the store's append-only event log: observers subscribe, their
// cursors express "unread", and two handlers turn pending events into a
// notification — a watch-consume for agents that self-monitor (Claude), and an
// idle pty-nudge for those that can't (codex).
//
// This package re-homes the dispatch gateway's settled mechanics:
//
//	gateway                         here
//	------------------------------  -----------------------------------------
//	ack = output / consume pending  cursor: events since the observer's cursor
//	bundle by sender                Consume groups pending events by ticket
//	hardcoded chief id              ObserverChief, a well-known literal
//	two consumers (watch / nudge)   the two Delivery paths below
//	never stream content to a PTY   Nudger carries only a fixed trigger
//
// It knows nothing about live sessions or real Monitors — it is built against the
// store and proven by a simulation harness. Slice 3 wires it to real sessions.
package ticketnotify

import (
	"strings"
	"time"

	"github.com/victorarias/attn/internal/store"
)

// ObserverChief is the well-known chief observer — the awareness layer that
// observes every ticket. Re-homes the gateway's hardcoded chief id as a literal,
// so addressing it needs no lookup.
const ObserverChief = "chief"

// EventStore is the store surface the notifier needs. The real *store.Store
// satisfies it; the harness uses the real store, so there is no mock to drift.
type EventStore interface {
	TicketEventsSince(cursor int64) ([]store.TicketEvent, error)
	GetObserverCursor(observerID string) (int64, error)
	SetObserverCursor(observerID string, cursor int64, now time.Time) error
	GetTicket(id string) (*store.Ticket, error)
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

// Bundle is an observer's pending events for a single ticket. Consume returns one
// Bundle per ticket, in first-seen order — the gateway's "bundle by sender",
// re-homed to "bundle by ticket".
type Bundle struct {
	TicketID string
	Events   []store.TicketEvent
}

// Consume returns the observer's unread events (those after its cursor that it
// should see), bundled by ticket, and advances the cursor past everything
// examined. This is the watch-consume handler: a self-monitoring observer calls it
// when its Monitor fires; a nudged observer calls it after the nudge lands.
func Consume(es EventStore, obs Observer, now time.Time) ([]Bundle, error) {
	matched, newCursor, err := pending(es, obs)
	if err != nil {
		return nil, err
	}
	if err := es.SetObserverCursor(obs.ID, newCursor, now); err != nil {
		return nil, err
	}
	return bundleByTicket(matched), nil
}

// Unread counts the observer's pending events without consuming them.
func Unread(es EventStore, obs Observer) (int, error) {
	matched, _, err := pending(es, obs)
	if err != nil {
		return 0, err
	}
	return len(matched), nil
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

// pending returns the observer's unread, relevant events and the cursor to advance
// to. The cursor advances past every event examined (matched or not), so unrelated
// events are never re-scanned. An observer never sees events it authored.
//
// Scope: the chief observes every ticket; an agent observes only tickets currently
// assigned to it. (Slice 3 narrows the chief to tickets it delegated/owns once
// delegation exists.)
func pending(es EventStore, obs Observer) (matched []store.TicketEvent, newCursor int64, err error) {
	cursor, err := es.GetObserverCursor(obs.ID)
	if err != nil {
		return nil, 0, err
	}
	events, err := es.TicketEventsSince(cursor)
	if err != nil {
		return nil, 0, err
	}

	newCursor = cursor
	assigneeOf := map[string]string{}
	for _, e := range events {
		if e.Seq > newCursor {
			newCursor = e.Seq
		}
		if e.Author == obs.ID {
			continue // never notify an observer of its own action
		}
		if obs.ID == ObserverChief {
			matched = append(matched, e)
			continue
		}
		assignee, ok := assigneeOf[e.TicketID]
		if !ok {
			ticket, err := es.GetTicket(e.TicketID)
			if err != nil {
				return nil, 0, err
			}
			if ticket != nil {
				assignee = ticket.Assignee
			}
			assigneeOf[e.TicketID] = assignee
		}
		if assignee == obs.ID {
			matched = append(matched, e)
		}
	}
	return matched, newCursor, nil
}

// bundleByTicket groups events by ticket, preserving first-seen ticket order and
// per-ticket event order.
func bundleByTicket(events []store.TicketEvent) []Bundle {
	if len(events) == 0 {
		return nil
	}
	index := map[string]int{}
	var bundles []Bundle
	for _, e := range events {
		i, ok := index[e.TicketID]
		if !ok {
			i = len(bundles)
			index[e.TicketID] = i
			bundles = append(bundles, Bundle{TicketID: e.TicketID})
		}
		bundles[i].Events = append(bundles[i].Events, e)
	}
	return bundles
}
