package daemon

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// These tests exercise the bus against the REAL sqlBusStore, not the in-memory
// fake internal/bus uses. A green run in internal/bus only proves the delivery
// logic; this proves the SQLite adapter carries the same semantics.

func waitForBus(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
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

// Over the real SQLite adapter: a durable consumer that was not running catches
// up from its persisted cursor when it comes back.
func TestDurableConsumerCatchesUpOverTheSQLiteAdapter(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
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

	first := bus.New(bus.Options{Store: backing, PollInterval: 5 * time.Millisecond})
	if err := first.Register("ticket-watcher", bus.Filter{"ticket.*"}, record); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := first.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := first.Publish(FactTicketCreated, "tk-1", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	waitForBus(t, "the first delivery", func() bool { return len(snapshot()) == 1 })
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

	second := bus.New(bus.Options{Store: backing, PollInterval: 5 * time.Millisecond})
	if err := second.Register("ticket-watcher", bus.Filter{"ticket.*"}, record); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := second.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(second.Stop)

	waitForBus(t, "catch-up", func() bool { return len(snapshot()) == 3 })
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
