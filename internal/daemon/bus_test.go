package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// These tests exercise the bus against the REAL sqlBusStore, not the in-memory
// fake internal/bus uses. A green run in internal/bus only proves the delivery
// logic; this proves the SQLite adapter carries the same semantics.

// requireBus runs the bus's own safety-net poll out and then asks once. Inside a
// bubble that poll is the slowest thing that can still deliver, so a condition
// still false afterwards is false for good.
func requireBus(t *testing.T, what string, cond func() bool) {
	t.Helper()
	time.Sleep(2 * bus.DefaultPollInterval)
	synctest.Wait()
	if !cond() {
		t.Fatalf("timed out waiting for %s", what)
	}
}

// A ticket mutation publishes a durable, subject-carrying fact — and still
// produces exactly one board push, unchanged from before the migration.
func TestTicketMutationPublishesAFactAndPushesTheBoardOnce(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(d.stopEventBus)

	var boards int
	d.ticketsBroadcastHook = func([]protocol.Ticket) { boards++ }

	now := time.Now()
	if _, err := d.store.CreateTicket(store.Ticket{ID: "tk-1", Title: "work"}, "sess-a", now); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	d.publishTicketFact(FactTicketCommented, "tk-1")

	if boards != 1 {
		t.Fatalf("one fact produced %d board pushes, want exactly 1", boards)
	}

	events, err := d.store.BusEventsSince(0, 10)
	if err != nil {
		t.Fatalf("BusEventsSince: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("log holds %d events, want 1", len(events))
	}
	if events[0].Name != FactTicketCommented {
		t.Fatalf("fact name = %q, want %q", events[0].Name, FactTicketCommented)
	}
	// The subject is what separates a fact from a snapshot invalidation.
	if events[0].Subject != "tk-1" {
		t.Fatalf("fact subject = %q, want the ticket id", events[0].Subject)
	}
}

// The session-state migration kept its call sites, so the wire event it produces
// must be indistinguishable from the direct broadcast it replaced.
func TestSessionStateFactProjectsTheSameWireEvent(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(d.stopEventBus)

	d.store.Add(&protocol.Session{ID: "sess-1", Directory: "/tmp/x", State: "idle"})

	var events []string
	d.wsHub.broadcastListener = func(e *protocol.WebSocketEvent) { events = append(events, e.Event) }

	d.broadcastSessionStateChanged("sess-1")

	found := false
	for _, name := range events {
		if name == protocol.EventSessionStateChanged {
			found = true
		}
	}
	if !found {
		t.Fatalf("no session_state_changed on the wire; got %v", events)
	}

	logged, err := d.store.BusEventsSince(0, 10)
	if err != nil {
		t.Fatalf("BusEventsSince: %v", err)
	}
	if len(logged) != 1 || logged[0].Name != FactSessionStateChanged || logged[0].Subject != "sess-1" {
		t.Fatalf("expected one session.state.changed fact for sess-1, got %+v", logged)
	}
}

// A new activity line reaches clients the same way every other state change
// does: the generator publishes a subject-only fact and the projection re-reads
// the session and re-pushes it. The generator never writes to the hub itself.
func TestActivityFactProjectsTheSessionSnapshot(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(d.stopEventBus)

	d.store.Add(&protocol.Session{ID: "sess-1", Directory: "/tmp/x", State: "working"})
	d.store.UpdateSessionActivity("sess-1", "running the frontend test suite", time.Now(), "v1:abc:512:0")

	var pushed []*protocol.WebSocketEvent
	d.wsHub.broadcastListener = func(e *protocol.WebSocketEvent) { pushed = append(pushed, e) }

	d.publishFact(FactSessionActivityChanged, "sess-1", nil)

	// The line has to be on the pushed snapshot, not merely announced: a client
	// that has to ask for it after the event would render a stale row until it
	// answered.
	carried := false
	for _, event := range pushed {
		if event.Event != protocol.EventSessionStateChanged || event.Session == nil {
			continue
		}
		if event.Session.ID == "sess-1" && protocol.Deref(event.Session.Activity) == "running the frontend test suite" {
			carried = true
		}
	}
	if !carried {
		t.Fatalf("no pushed snapshot carried the new activity line; got %+v", pushed)
	}

	logged, err := d.store.BusEventsSince(0, 10)
	if err != nil {
		t.Fatalf("BusEventsSince: %v", err)
	}
	if len(logged) != 1 || logged[0].Name != FactSessionActivityChanged || logged[0].Subject != "sess-1" {
		t.Fatalf("expected one session.activity.changed fact for sess-1, got %+v", logged)
	}
}

// Over the real SQLite adapter: a durable consumer that was not running catches
// up from its persisted cursor when it comes back.
func TestDurableConsumerCatchesUpOverTheSQLiteAdapter(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		backing := d.newSQLBusStore()

		var (
			mu   sync.Mutex
			seen []string
		)
		record := func(_ context.Context, ev bus.Event) error {
			mu.Lock()
			seen = append(seen, ev.Name+":"+ev.Subject)
			mu.Unlock()
			return nil
		}
		snapshot := func() []string {
			mu.Lock()
			defer mu.Unlock()
			return append([]string(nil), seen...)
		}

		first := bus.New(bus.Options{Store: backing})
		if err := first.Register("ticket-watcher", bus.Filter{"ticket.*"}, record); err != nil {
			t.Fatalf("Register: %v", err)
		}
		if err := first.Start(); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if _, err := first.Publish(FactTicketCreated, "tk-1", nil); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		requireBus(t, "the first delivery", func() bool { return len(snapshot()) == 1 })
		first.Stop()

		// Facts published while the consumer is gone, including one it filters out.
		offline := bus.New(bus.Options{Store: backing})
		for _, fact := range []struct{ name, subject string }{
			{FactTicketCommented, "tk-1"},
			{FactSessionStateChanged, "sess-9"},
			{FactTicketStatusChanged, "tk-1"},
		} {
			if _, err := offline.Publish(fact.name, fact.subject, nil); err != nil {
				t.Fatalf("Publish(%s): %v", fact.name, err)
			}
		}

		second := bus.New(bus.Options{Store: backing})
		if err := second.Register("ticket-watcher", bus.Filter{"ticket.*"}, record); err != nil {
			t.Fatalf("Register: %v", err)
		}
		if err := second.Start(); err != nil {
			t.Fatalf("Start: %v", err)
		}
		t.Cleanup(second.Stop)

		requireBus(t, "catch-up", func() bool { return len(snapshot()) == 3 })
		got := snapshot()
		want := []string{
			FactTicketCreated + ":tk-1",
			FactTicketCommented + ":tk-1",
			FactTicketStatusChanged + ":tk-1",
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("delivered %v, want %v", got, want)
			}
		}

		// The cursor advanced past the filtered-out fact too, so a narrow consumer is
		// not permanently reported as lagging.
		rec, ok, err := d.store.GetBusConsumer("ticket-watcher")
		if err != nil || !ok {
			t.Fatalf("GetBusConsumer: %v (found=%v)", err, ok)
		}
		if rec.Cursor != 4 {
			t.Fatalf("persisted cursor is %d, want 4 (head)", rec.Cursor)
		}
		if rec.Filter != "ticket.*" {
			t.Fatalf("persisted filter is %q", rec.Filter)
		}
	})
}

// Status is the operator surface: head, cursors, and the lag between them.
func TestBusStatusReportsLag(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(d.stopEventBus)

	if err := d.store.SaveBusConsumer(store.BusConsumer{
		Name: "watcher", Cursor: 1, Filter: "ticket.*", Enabled: true,
	}, time.Now()); err != nil {
		t.Fatalf("SaveBusConsumer: %v", err)
	}
	for i := 0; i < 3; i++ {
		d.publishTicketFact(FactTicketChanged, "tk-1")
	}

	st, err := d.BusStatus()
	if err != nil {
		t.Fatalf("BusStatus: %v", err)
	}
	if st.Head != 3 {
		t.Fatalf("head = %d, want 3", st.Head)
	}
	if len(st.Consumers) != 1 {
		t.Fatalf("consumers = %+v, want one", st.Consumers)
	}
	if st.Consumers[0].Lag != 2 {
		t.Fatalf("lag = %d, want 2", st.Consumers[0].Lag)
	}
	// Registered in the database but with no loop in this process.
	if st.Consumers[0].Live {
		t.Fatal("a consumer with no delivery loop reported Live")
	}
}

// failingAppendBusStore is the real adapter with a switchable append fault, so
// the test exercises the actual daemon wiring rather than a hand-built bus.
type failingAppendBusStore struct {
	bus.Store
	mu   sync.Mutex
	fail error
}

func (f *failingAppendBusStore) Append(e bus.Event, now time.Time) (int64, error) {
	f.mu.Lock()
	err := f.fail
	f.mu.Unlock()
	if err != nil {
		return 0, err
	}
	return f.Store.Append(e, now)
}

func (f *failingAppendBusStore) setFail(err error) {
	f.mu.Lock()
	f.fail = err
	f.mu.Unlock()
}

// rewireBusWithFailingAppend swaps the daemon's bus for one whose durable append
// can be made to fail, keeping the hub subscription that ensureEventBus installs.
func rewireBusWithFailingAppend(t *testing.T, d *Daemon) *failingAppendBusStore {
	t.Helper()
	d.stopEventBus()
	backing := &failingAppendBusStore{Store: d.newSQLBusStore()}
	d.eventBus = bus.New(bus.Options{Store: backing, Log: d.logf})
	d.busUnsubscribe = d.eventBus.Subscribe(bus.All, d.projectToClients)
	t.Cleanup(d.stopEventBus)
	return backing
}

// A ticket mutation has already committed by the time its fact is published. If
// the durable append then fails, the board push must still go out: before the
// bus existed this was a direct broadcast, and a client cannot be allowed to
// miss a committed mutation because the event log had a bad night.
func TestBoardStillPushesWhenTheBusAppendFails(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backing := rewireBusWithFailingAppend(t, d)

	var boards int
	d.ticketsBroadcastHook = func([]protocol.Ticket) { boards++ }

	now := time.Now()
	if _, err := d.store.CreateTicket(store.Ticket{ID: "tk-1", Title: "work"}, "sess-a", now); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	backing.setFail(errors.New("disk had a bad night"))
	d.publishTicketFact(FactTicketStatusChanged, "tk-1")

	if boards != 1 {
		t.Fatalf("a committed ticket mutation produced %d board pushes while the bus append was failing, want 1", boards)
	}
	// The fact is genuinely lost — only its durability, not the wire.
	logged, err := d.store.BusEventsSince(0, 10)
	if err != nil {
		t.Fatalf("BusEventsSince: %v", err)
	}
	if len(logged) != 0 {
		t.Fatalf("append was supposed to fail, but the log holds %d event(s)", len(logged))
	}

	// Recovery: once the store is healthy the next fact is durable again, and the
	// board still pushes exactly once for it.
	backing.setFail(nil)
	d.publishTicketFact(FactTicketStatusChanged, "tk-1")
	if boards != 2 {
		t.Fatalf("board pushes = %d after recovery, want 2", boards)
	}
	logged, err = d.store.BusEventsSince(0, 10)
	if err != nil {
		t.Fatalf("BusEventsSince: %v", err)
	}
	if len(logged) != 1 {
		t.Fatalf("log holds %d event(s) after recovery, want 1", len(logged))
	}
}

// The same guarantee for session state. This is the sharper case: the nudge and
// auto-settle countdowns broadcast without any preceding store write, so nothing
// upstream gates the push — the bus is the only thing that could drop it.
func TestSessionStateStillReachesTheWireWhenTheBusAppendFails(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backing := rewireBusWithFailingAppend(t, d)

	d.store.Add(&protocol.Session{ID: "sess-1", Directory: "/tmp/x", State: "idle"})

	var events []string
	d.wsHub.broadcastListener = func(e *protocol.WebSocketEvent) { events = append(events, e.Event) }

	backing.setFail(errors.New("disk had a bad night"))
	d.broadcastSessionStateChanged("sess-1")

	found := false
	for _, name := range events {
		if name == protocol.EventSessionStateChanged {
			found = true
		}
	}
	if !found {
		t.Fatalf("a failing bus append silenced session_state_changed; wire saw %v", events)
	}
}
