package bus

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// Runtime consumer lifecycle: a durable consumer can arrive and leave while the
// bus runs, because an app installs and uninstalls while the daemon runs.

// A consumer registered after Start is served immediately — an install must not
// wait for a daemon restart — and it starts at head, exactly as one registered
// before Start does. Registration is not a request to replay history.
func TestRegisterAfterStartDeliversFromHead(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Stop)

	if _, err := b.Publish("before.install", "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	rec := newRecorder()
	if err := b.Register("app:notes", All, rec.handle); err != nil {
		t.Fatalf("Register after Start: %v", err)
	}

	if _, err := b.Publish("after.install", "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	waitFor(t, "the post-registration fact", func() bool { return rec.count() >= 1 })

	names, _ := rec.snapshot()
	if len(names) != 1 || names[0] != "after.install" {
		t.Fatalf("delivered %v; a consumer installed at runtime must not replay history", names)
	}
	if _, ok, err := s.GetConsumer("app:notes"); err != nil || !ok {
		t.Fatalf("registration was not persisted (found=%v, err=%v)", ok, err)
	}
}

// Head is where a consumer NEW to the store starts. One whose row survives — the
// same daemon re-registering it after a restart, an app whose registration was
// never removed — resumes from its persisted cursor, so runtime registration
// carries the same catch-up guarantee startup registration does.
func TestRegisterAfterStartResumesAnExistingCursor(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Stop)

	// A row left at cursor 0 by an earlier run, and a backlog it never read.
	if err := s.SaveConsumer(Consumer{Name: "app:notes", Cursor: 0, Filter: "*", Enabled: true}, time.Now()); err != nil {
		t.Fatalf("seeding the consumer: %v", err)
	}
	for _, name := range []string{"a.happened", "b.happened"} {
		if _, err := b.Publish(name, "", nil); err != nil {
			t.Fatalf("Publish(%s): %v", name, err)
		}
	}

	rec := newRecorder()
	if err := b.Register("app:notes", All, rec.handle); err != nil {
		t.Fatalf("Register after Start: %v", err)
	}
	waitFor(t, "the backlog to be delivered", func() bool { return rec.count() >= 2 })

	names, _ := rec.snapshot()
	if len(names) != 2 || names[0] != "a.happened" || names[1] != "b.happened" {
		t.Fatalf("delivered %v; a runtime registration must resume its persisted cursor", names)
	}
}

// Unregister is the way out: the loop stops and the row goes. The row matters as
// much as the loop — while it exists and is enabled it holds the retention floor
// down against a consumer nobody serves.
func TestUnregisterStopsDeliveryAndDeletesTheRow(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)

	// A second consumer that stays registered is the signal that the bus has
	// delivered a later fact, so "the unregistered one received nothing more" is
	// asserted against a real receipt rather than a wait.
	witness := newRecorder()
	if err := b.Register("witness", All, witness.handle); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Stop)

	rec := newRecorder()
	if err := b.Register("app:notes", All, rec.handle); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := b.Publish("one.happened", "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	waitFor(t, "the first delivery", func() bool { return rec.count() == 1 })

	if err := b.Unregister("app:notes"); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if _, ok, err := s.GetConsumer("app:notes"); err != nil || ok {
		t.Fatalf("the consumer row survived Unregister (found=%v, err=%v)", ok, err)
	}

	if _, err := b.Publish("two.happened", "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	waitFor(t, "the witness to receive the later fact", func() bool { return witness.count() == 2 })
	if n := rec.count(); n != 1 {
		t.Fatalf("the unregistered consumer received %d events, want 1: its loop is still delivering", n)
	}
}

// Unregister must interrupt a loop parked in its retry sleep. A consumer stalled
// at the retry cap sits in that wait for two minutes; an uninstall that had to
// wait it out would look like a hang.
func TestUnregisterInterruptsAConsumerParkedInRetryBackoff(t *testing.T) {
	s := newMemStore()
	// A retry sleep far longer than any correct run needs: if Unregister waits for
	// the timer, this test hangs to its tripwire instead of passing slowly.
	b := New(Options{
		Store:        s,
		Log:          func(string, ...interface{}) {},
		PollInterval: 5 * time.Millisecond,
		RetryBase:    time.Hour,
		RetryCap:     time.Hour,
		TrimInterval: time.Hour,
	})

	var attempts atomic.Int64
	handler := func(context.Context, Event) error {
		attempts.Add(1)
		return errors.New("handler boom")
	}
	if err := b.Register("app:stuck", All, handler); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Stop)

	if _, err := b.Publish("work.happened", "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	waitFor(t, "the handler to fail once", func() bool { return attempts.Load() >= 1 })

	done := make(chan error, 1)
	go func() { done <- b.Unregister("app:stuck") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Unregister: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Unregister blocked behind the consumer's retry sleep")
	}
}

// Cancel, wait for the loop to exit, then delete the row — in that order. Deleting
// first leaves the live loop reading a registration that disappeared, which is an
// error path that records a failure and retries forever: a zombie consumer.
func TestUnregisterDeletesTheRowOnlyAfterTheLoopExits(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)

	entered := make(chan struct{})
	release := make(chan struct{})
	// The handler deliberately ignores cancellation: the case under test is a
	// handler still running when Unregister lands.
	handler := func(context.Context, Event) error {
		close(entered)
		<-release
		return nil
	}
	if err := b.Register("app:slow", All, handler); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Stop)

	d := b.durables[0]
	var deletedWhileRunning atomic.Bool
	s.onDelete = func(string) {
		select {
		case <-d.done:
		default:
			deletedWhileRunning.Store(true)
		}
	}

	if _, err := b.Publish("work.happened", "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	<-entered

	done := make(chan error, 1)
	go func() { done <- b.Unregister("app:slow") }()

	// Wait for Unregister to have retired the consumer, so releasing the handler
	// really is a result arriving after the unregister began.
	waitFor(t, "the consumer to be retired", d.isRetired)
	close(release)

	if err := <-done; err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if deletedWhileRunning.Load() {
		t.Fatal("the row was deleted while the delivery loop was still running")
	}
	select {
	case <-d.done:
	default:
		t.Fatal("Unregister returned while the delivery loop was still running")
	}
	// The handler completed after the unregister; its cursor advance is dropped,
	// so nothing recreated or moved the row it no longer owns.
	if _, ok, err := s.GetConsumer("app:slow"); err != nil || ok {
		t.Fatalf("a late handler result wrote to the deleted registration (found=%v, err=%v)", ok, err)
	}
}

// A retired consumer writes nothing. Its handler may still be in flight when the
// registration goes, and a cursor advance or failure record against a row that no
// longer exists is a no-op — not an error, not a failure streak.
func TestRetiredConsumerDropsLateResults(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)

	d := b.newDurable("app:gone", All, func(context.Context, Event) error { return nil })
	t.Cleanup(d.cancel)
	d.retire()

	if err := b.advance(d, 7); err != nil {
		t.Fatalf("a late cursor advance must be dropped silently, got %v", err)
	}
	if _, ok, err := s.GetConsumer("app:gone"); err != nil || ok {
		t.Fatalf("a retired consumer moved or recreated its row (found=%v, err=%v)", ok, err)
	}

	d.recordFailure("handler boom", 3)
	if reason, failures := d.stallReason(), d.drainFailures(); reason != "" || failures != 0 {
		t.Fatalf("a retired consumer recorded a stall (%q, %d attempts)", reason, failures)
	}
}

// The orphan case the row lifecycle exists for: a stalled enabled consumer pins
// retention, and nothing above its cursor can be trimmed while it is registered.
// Unregistering it is what lets the floor move again.
func TestUnregisteringAStalledConsumerReleasesTheRetentionFloor(t *testing.T) {
	s := newMemStore()
	clk := &testClock{now: time.Now()}
	b := New(Options{
		Store:        s,
		Log:          func(string, ...interface{}) {},
		Now:          clk.get,
		Retention:    time.Hour,
		TrimInterval: time.Hour,
		PollInterval: 5 * time.Millisecond,
		RetryBase:    5 * time.Millisecond,
		RetryCap:     20 * time.Millisecond,
	})

	var attempts atomic.Int64
	handler := func(context.Context, Event) error {
		attempts.Add(1)
		return errors.New("handler boom")
	}
	if err := b.Register("app:stuck", All, handler); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Stop)

	// Two facts well outside the retention window, neither of which the stalled
	// consumer will ever get past.
	clk.advance(-4 * time.Hour)
	for _, name := range []string{"a.happened", "b.happened"} {
		if _, err := b.Publish(name, "", nil); err != nil {
			t.Fatalf("Publish(%s): %v", name, err)
		}
	}
	clk.advance(4 * time.Hour)
	waitFor(t, "the consumer to stall on the first fact", func() bool { return attempts.Load() >= 2 })

	if n, err := b.Trim(); err != nil || n != 0 {
		t.Fatalf("trimmed %d event(s) (err=%v); a stalled enabled consumer must pin the log", n, err)
	}

	if err := b.Unregister("app:stuck"); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if n, err := b.Trim(); err != nil || n != 2 {
		t.Fatalf("trimmed %d event(s) (err=%v) after the consumer was unregistered, want 2: the floor is still pinned", n, err)
	}
}

// Unregister is idempotent, and it deletes rows this process never registered —
// the orphan an earlier daemon left behind is exactly what an uninstall has to be
// able to clear.
func TestUnregisterIsIdempotentAndClearsOrphanRows(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Stop)

	if err := s.SaveConsumer(Consumer{Name: "app:ghost", Cursor: 0, Filter: "*", Enabled: true}, time.Now()); err != nil {
		t.Fatalf("seeding the orphan row: %v", err)
	}

	if err := b.Unregister("app:ghost"); err != nil {
		t.Fatalf("Unregister of an orphan row: %v", err)
	}
	if _, ok, err := s.GetConsumer("app:ghost"); err != nil || ok {
		t.Fatalf("the orphan row survived (found=%v, err=%v)", ok, err)
	}
	if err := b.Unregister("app:ghost"); err != nil {
		t.Fatalf("second Unregister: %v; an uninstall path must be re-runnable", err)
	}
	if err := b.Unregister("never-registered"); err != nil {
		t.Fatalf("Unregister of an unknown name: %v", err)
	}
}

// A row that could not be deleted is reported, not swallowed: the loop is already
// stopped, so the caller is left with an orphan row it has to be able to see and
// retry.
func TestUnregisterReportsAFailedRowDelete(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)

	rec := newRecorder()
	if err := b.Register("app:notes", All, rec.handle); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Stop)
	waitFor(t, "the registration to be persisted", func() bool {
		_, ok, _ := s.GetConsumer("app:notes")
		return ok
	})

	s.setDeleteErr(errors.New("database is having a bad night"))
	if err := b.Unregister("app:notes"); err == nil {
		t.Fatal("Unregister reported success while the row could not be deleted")
	}

	s.setDeleteErr(nil)
	if err := b.Unregister("app:notes"); err != nil {
		t.Fatalf("retried Unregister: %v", err)
	}
	if _, ok, err := s.GetConsumer("app:notes"); err != nil || ok {
		t.Fatalf("the row survived the retry (found=%v, err=%v)", ok, err)
	}
}

// Reinstalling under the same name starts at head again: the cursor left with the
// row, so an app that was removed and put back reacts to what happens next rather
// than to the backlog it missed.
func TestReinstallAfterUnregisterStartsAtHead(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Stop)

	first := newRecorder()
	if err := b.Register("app:notes", All, first.handle); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := b.Publish("one.happened", "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	waitFor(t, "the first delivery", func() bool { return first.count() == 1 })
	if err := b.Unregister("app:notes"); err != nil {
		t.Fatalf("Unregister: %v", err)
	}

	if _, err := b.Publish("while.gone", "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	second := newRecorder()
	if err := b.Register("app:notes", All, second.handle); err != nil {
		t.Fatalf("re-Register: %v", err)
	}
	if _, err := b.Publish("after.reinstall", "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	waitFor(t, "the post-reinstall fact", func() bool { return second.count() >= 1 })

	names, _ := second.snapshot()
	if len(names) != 1 || names[0] != "after.reinstall" {
		t.Fatalf("delivered %v; a reinstalled consumer must start at head", names)
	}
}

// A registration that cannot be persisted leaves nothing behind: no consumer in
// the set, no loop, and the name is free to try again.
func TestRegisterAfterStartRollsBackWhenThePersistFails(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Stop)

	s.setBoundsErr(errors.New("database is having a bad night"))
	if err := b.Register("app:notes", All, func(context.Context, Event) error { return nil }); err == nil {
		t.Fatal("Register reported success while the log could not be read")
	}
	s.setBoundsErr(nil)

	rec := newRecorder()
	if err := b.Register("app:notes", All, rec.handle); err != nil {
		t.Fatalf("Register after a failed attempt: %v; the name was left claimed", err)
	}
	if _, err := b.Publish("after.install", "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	waitFor(t, "the retried registration to deliver", func() bool { return rec.count() >= 1 })
}
