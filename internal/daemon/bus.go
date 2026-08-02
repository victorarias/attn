package daemon

import "github.com/victorarias/attn/internal/bus"

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
	// FactSessionStateChanged: subject is the session id.
	FactSessionStateChanged = "session.state.changed"

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
func (d *Daemon) publishFact(name, subject string, payload any) {
	if d == nil {
		return
	}
	if d.eventBus == nil {
		d.projectToClients(bus.Event{Name: name, Subject: subject})
		return
	}
	if _, err := d.eventBus.Publish(name, subject, payload); err != nil {
		d.logf("bus: publishing %s (%s): %v", name, subject, err)
	}
}

// BusStatus snapshots the bus for operator inspection.
func (d *Daemon) BusStatus() (bus.Status, error) {
	if d.eventBus == nil {
		return bus.Status{}, nil
	}
	return d.eventBus.Status()
}
