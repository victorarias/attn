package daemon

import (
	"encoding/json"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/protocol"
)

// The daemon's side of the event bus: the fact vocabulary, the lifecycle, and the
// projection table that turns facts into what WebSocket clients see.
//
// The migration pattern for A2 is the two halves below.
//
// PRODUCER. A `broadcastXxx` method stops touching the hub and publishes a fact
// instead. The fact must carry a subject — the entity it is about — because a
// subject-less "the list changed" fact is the snapshot invalidation this design
// exists to avoid. When the existing method already receives the entity id, it
// becomes a one-line publish and no call site changes (see
// broadcastSessionStateChanged). When it does not — broadcastTicketsUpdated took
// no arguments because it re-pushed the whole board — the call sites are the ones
// that know which fact occurred, and they are what changes.
//
// PROJECTION. The old broadcaster body moves into a `projectXxx` method and is
// registered in wireProjections. The hub subscribes ephemerally to every fact and
// runs the matching projections inline on the publishing goroutine, so a fact
// still produces exactly the same wire traffic, in the same order, as the direct
// call it replaced.
//
// What does NOT move: byte streams. PTY output, PTY desync, attach results,
// workspace tile content, and fs change bursts keep their direct paths. They are
// high volume, and attach traffic is routed by a per-client predicate
// (SendRawTextToMatchingClients), which pub/sub cannot express.
//
// See docs/plans/2026-08-01-ext-a1-event-bus.md.

// Fact names are dotted `domain.verb`. `ext.<extension>.*` is reserved for facts
// published by extensions.
const (
	// Session facts; subject is the session id.
	//
	// Registered and reregistered are separate facts rather than one fact with a
	// flag, because they are separate things: a session appearing for the first
	// time and a live session being re-announced by a reconnecting client. They
	// project to different wire events, which is how the daemon has always
	// treated them.
	FactSessionRegistered   = "session.registered"
	FactSessionReregistered = "session.reregistered"
	FactSessionStateChanged = "session.state.changed"
	FactSessionRenamed      = "session.renamed"
	FactSessionUnregistered = "session.unregistered"
	FactSessionTodosChanged = "session.todos.changed"
	FactSessionRespawned    = "session.respawned"
	FactSessionPTYResized   = "session.pty.resized"
	// FactSessionTerminated is a session going away as part of a bulk operation
	// (clear-all, a worktree deletion taking its sessions with it). It is not
	// FactSessionUnregistered: unregistering announces the individual session to
	// clients, while these paths have only ever re-pushed the list.
	FactSessionTerminated = "session.terminated"
	// FactSessionBranchChanged: the branch monitor saw this session's checkout move.
	FactSessionBranchChanged = "session.branch.changed"
	// FactSessionChiefRoleChanged: this session took or lost the chief-of-staff role.
	FactSessionChiefRoleChanged = "session.chief_role.changed"
	// FactSessionReconciled: startup/recovery reconciliation changed this session.
	FactSessionReconciled = "session.reconciled"

	// FactWorktreeSessionsRemoved: deleting this worktree took its sessions with
	// it. Subject is the worktree path.
	FactWorktreeSessionsRemoved = "worktree.sessions.removed"

	// FactEndpointSessionsChanged: a remote endpoint's session set changed.
	// Subject is the endpoint id — the entity the hub manager knows about, and
	// the one an extension watching a remote fleet would filter on.
	FactEndpointSessionsChanged = "endpoint.sessions.changed"

	// Ticket facts; subject is the ticket id.
	FactTicketCreated       = "ticket.created"
	FactTicketStatusChanged = "ticket.status_changed"
	FactTicketCommented     = "ticket.commented"
	FactTicketAssigned      = "ticket.assigned"
	FactTicketAttached      = "ticket.attached"
	// FactTicketChanged is the fallback for a mutation whose call site cannot
	// name a sharper fact. It is still about one ticket — that is the line
	// between a fact and an invalidation — but a sharper name is preferred
	// wherever the producer knows one.
	FactTicketChanged = "ticket.changed"
)

// projection maps facts to the wire traffic they produce.
type projection struct {
	filter bus.Filter
	apply  func(*Daemon, bus.Event)
}

// wireProjections is the whole fact -> WebSocket mapping. A2 grows this table as
// each broadcaster migrates.
var wireProjections = []projection{
	{
		filter: bus.Filter{FactSessionStateChanged},
		apply:  func(d *Daemon, ev bus.Event) { d.projectSessionStateChanged(ev.Subject) },
	},
	{
		filter: bus.Filter{FactSessionRegistered},
		apply: func(d *Daemon, ev bus.Event) {
			d.projectSessionEvent(protocol.EventSessionRegistered, ev.Subject)
		},
	},
	{
		// A re-announced session and a renamed one both reach clients as a state
		// change carrying the session; neither recomputes the workspace, which is
		// what separates them from FactSessionStateChanged.
		filter: bus.Filter{FactSessionReregistered, FactSessionRenamed},
		apply: func(d *Daemon, ev bus.Event) {
			d.projectSessionEvent(protocol.EventSessionStateChanged, ev.Subject)
		},
	},
	{
		filter: bus.Filter{FactSessionTodosChanged},
		apply: func(d *Daemon, ev bus.Event) {
			d.projectSessionEvent(protocol.EventSessionTodosUpdated, ev.Subject)
		},
	},
	{
		filter: bus.Filter{FactSessionUnregistered},
		apply:  func(d *Daemon, ev bus.Event) { d.projectSessionUnregistered(ev) },
	},
	{
		filter: bus.Filter{FactSessionRespawned},
		apply: func(d *Daemon, ev bus.Event) {
			d.wsHub.Broadcast(&protocol.WebSocketEvent{
				Event: protocol.EventRuntimeRespawned,
				ID:    protocol.Ptr(ev.Subject),
			})
		},
	},
	{
		filter: bus.Filter{FactSessionPTYResized},
		apply:  func(d *Daemon, ev bus.Event) { d.projectSessionPTYResized(ev) },
	},
	{
		// Everything whose only client-visible effect is "the session list moved".
		// Each is a real fact about one session; the wire sees one list push.
		filter: bus.Filter{
			FactSessionTerminated,
			FactSessionBranchChanged,
			FactSessionChiefRoleChanged,
			FactSessionReconciled,
			FactWorktreeSessionsRemoved,
			FactEndpointSessionsChanged,
		},
		apply: func(d *Daemon, _ bus.Event) { d.projectSessionsUpdated() },
	},
	{
		// Every ticket fact re-pushes the board. The board push is the wire
		// shape clients already expect; the facts are what a consumer can
		// actually reason about.
		filter: bus.Filter{"ticket.*"},
		apply:  func(d *Daemon, _ bus.Event) { d.projectTicketsUpdated() },
	},
}

// ensureEventBus constructs the bus over the profile store and registers the hub
// as an ephemeral consumer. It is idempotent and runs at construction rather than
// at Start, because the hub subscription is what makes a published fact reach
// clients: a daemon that publishes without having started (every test that
// exercises a broadcaster directly) must still project.
func (d *Daemon) ensureEventBus() {
	if d.eventBus != nil {
		return
	}
	var backing bus.Store
	if d.store != nil {
		backing = d.newSQLBusStore()
	}
	d.eventBus = bus.New(bus.Options{Store: backing, Log: d.logf})
	d.busUnsubscribe = d.eventBus.Subscribe(bus.All, d.projectToClients)
}

// startEventBus begins durable delivery. It runs early in Start, before any
// subsystem that publishes.
func (d *Daemon) startEventBus() error {
	d.ensureEventBus()
	return d.eventBus.Start()
}

func (d *Daemon) stopEventBus() {
	if d.busUnsubscribe != nil {
		d.busUnsubscribe()
		d.busUnsubscribe = nil
	}
	if d.eventBus != nil {
		d.eventBus.Stop()
	}
}

// projectToClients is the hub's ephemeral handler.
func (d *Daemon) projectToClients(ev bus.Event) {
	for _, p := range wireProjections {
		if p.filter.Matches(ev.Name) {
			p.apply(d, ev)
		}
	}
}

// publishFact is the producer half. It is nil-safe: a Daemon assembled in a test
// without a bus still runs its projections directly, so migrating a broadcaster
// does not silently disconnect the behavior every existing test asserts on.
//
// The bus-less path marshals the payload exactly as Publish would, because a
// projection that reads its payload must behave the same either way — otherwise
// a fact whose data lives in the payload rather than in the store would project
// an empty message in every test that skips the bus.
func (d *Daemon) publishFact(name, subject string, payload any) {
	if d == nil {
		return
	}
	if d.eventBus == nil {
		ev := bus.Event{Name: name, Subject: subject}
		if payload != nil {
			raw, err := json.Marshal(payload)
			if err != nil {
				d.logf("bus: marshaling payload for %s (%s): %v", name, subject, err)
				return
			}
			ev.Payload = raw
		}
		d.projectToClients(ev)
		return
	}
	if _, err := d.eventBus.Publish(name, subject, payload); err != nil {
		d.logf("bus: publishing %s (%s): %v", name, subject, err)
	}
}

// decodeFact reads a fact's payload, reporting a decode failure rather than
// projecting a half-built message from it.
func decodeFact[T any](d *Daemon, ev bus.Event) (T, bool) {
	var out T
	if err := ev.Decode(&out); err != nil {
		d.logf("bus: decoding %s payload for %s: %v", ev.Name, ev.Subject, err)
		return out, false
	}
	return out, true
}

// Snapshot projections re-push a whole list — every session, every ticket. A
// bulk operation publishes one fact per entity, because that is what makes the
// log worth subscribing to, but the wire must not carry one full list per
// entity: clearing twenty sessions was one message before the bus and has to
// stay one message after it.
//
// coalesceSnapshots runs fn with snapshot projections deferred and de-duplicated
// by key, then flushes each one once, in first-request order. Per-entity
// projections (the ones that name the entity on the wire) are unaffected and
// still fire inline, in publish order.
//
// The window is process-wide rather than goroutine-scoped, so a snapshot pushed
// by an unrelated goroutine during a bulk operation is deferred to the flush
// too. That is a few microseconds of delay for one message, and it is still
// delivered exactly once, in order relative to the batch.
func (d *Daemon) coalesceSnapshots(fn func()) {
	d.snapshotMu.Lock()
	d.snapshotDepth++
	d.snapshotMu.Unlock()

	defer func() {
		d.snapshotMu.Lock()
		d.snapshotDepth--
		if d.snapshotDepth > 0 {
			d.snapshotMu.Unlock()
			return
		}
		pending := d.pendingSnapshots
		order := d.pendingSnapshotOrder
		d.pendingSnapshots = nil
		d.pendingSnapshotOrder = nil
		d.snapshotMu.Unlock()

		for _, key := range order {
			pending[key]()
		}
	}()

	fn()
}

// projectSnapshot runs a whole-list re-push, or records it for the end of the
// enclosing coalesceSnapshots window. Every projection that re-pushes a list
// goes through this rather than broadcasting directly.
func (d *Daemon) projectSnapshot(key string, push func()) {
	d.snapshotMu.Lock()
	if d.snapshotDepth == 0 {
		d.snapshotMu.Unlock()
		push()
		return
	}
	if d.pendingSnapshots == nil {
		d.pendingSnapshots = map[string]func(){}
	}
	if _, seen := d.pendingSnapshots[key]; !seen {
		d.pendingSnapshotOrder = append(d.pendingSnapshotOrder, key)
	}
	d.pendingSnapshots[key] = push
	d.snapshotMu.Unlock()
}

// Snapshot keys. One per whole-list wire message.
const (
	snapshotSessions = "sessions_updated"
	snapshotTickets  = "tickets_updated"
)

// BusStatus snapshots the bus for operator inspection.
func (d *Daemon) BusStatus() (bus.Status, error) {
	if d.eventBus == nil {
		return bus.Status{}, nil
	}
	return d.eventBus.Status()
}
