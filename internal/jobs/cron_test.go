package jobs

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

const cronKind = "heartbeat"

// A cron entry is armed by Start one interval out, fires when that interval
// elapses, and is immediately re-armed for the next one.
func TestACronEntryFiresOnItsIntervalAndRearms(t *testing.T) {
	r, _, clock := newTestRunner(t, nil)
	var fires atomic.Int64
	if err := r.RegisterCron(cronKind, time.Hour, func(context.Context, *Job) (any, error) {
		fires.Add(1)
		return nil, nil
	}, HandlerConfig{}); err != nil {
		t.Fatalf("register cron: %v", err)
	}
	mustStart(t, r)

	entry, err := r.CronEntry(cronKind)
	if err != nil || entry == nil {
		t.Fatalf("cron entry after start: %v (%+v)", err, entry)
	}
	armedFor := entry.ScheduledAt
	if want := clock.now().Add(time.Hour); !armedFor.Equal(want) {
		t.Fatalf("first fire armed for %s, want %s", armedFor, want)
	}
	if fires.Load() != 0 {
		t.Fatalf("cron fired %d times before its interval elapsed", fires.Load())
	}

	clock.advance(time.Hour)
	waitFor(t, "the cron entry to fire", func() bool { return fires.Load() == 1 })
	waitFor(t, "the cron entry to re-arm", func() bool {
		next, err := r.CronEntry(cronKind)
		return err == nil && next != nil && next.State == StateQueued && next.ScheduledAt.After(armedFor)
	})

	clock.advance(time.Hour)
	waitFor(t, "the cron entry to fire again", func() bool { return fires.Load() == 2 })
}

// A failing fire never retires the entry: it records the error and stays armed,
// because a heartbeat that gives up is a silent outage.
func TestAFailingCronEntryStaysArmed(t *testing.T) {
	r, _, clock := newTestRunner(t, func(o *Options) { o.MaxAttempts = 1 })
	var fires atomic.Int64
	if err := r.RegisterCron(cronKind, time.Minute, func(context.Context, *Job) (any, error) {
		fires.Add(1)
		return nil, errors.New("tick failed")
	}, HandlerConfig{}); err != nil {
		t.Fatalf("register cron: %v", err)
	}
	mustStart(t, r)

	for want := int64(1); want <= 3; want++ {
		clock.advance(time.Minute)
		waitFor(t, "the failing cron entry to fire", func() bool { return fires.Load() == want })
	}
	entry, err := r.CronEntry(cronKind)
	if err != nil || entry == nil {
		t.Fatalf("cron entry: %v (%+v)", err, entry)
	}
	if entry.State != StateQueued {
		t.Fatalf("failing cron entry state = %s, want queued (attempt cap must not retire it)", entry.State)
	}
	if entry.LastError != "tick failed" {
		t.Fatalf("failing cron entry last_error = %q, want the fire's error", entry.LastError)
	}
}

// A restart re-registers the same cron kind against a store that already holds
// its entry. The existing schedule must survive: re-arming on every boot would
// mean a daemon restarted more often than the interval never ticks at all.
func TestRestartKeepsAnExistingCronSchedule(t *testing.T) {
	store := newMemStore()
	clock := newFakeClock()
	opts := func() Options {
		return Options{Store: store, Now: clock.now, PollInterval: testPoll, Log: func(string, ...interface{}) {}}
	}
	noop := func(context.Context, *Job) (any, error) { return nil, nil }

	first := New(opts())
	if err := first.RegisterCron(cronKind, time.Hour, noop, HandlerConfig{}); err != nil {
		t.Fatalf("register cron: %v", err)
	}
	mustStart(t, first)
	armed, err := first.CronEntry(cronKind)
	if err != nil || armed == nil {
		t.Fatalf("cron entry: %v (%+v)", err, armed)
	}
	first.Stop()

	clock.advance(30 * time.Minute)

	second := New(opts())
	if err := second.RegisterCron(cronKind, time.Hour, noop, HandlerConfig{}); err != nil {
		t.Fatalf("re-register cron: %v", err)
	}
	mustStart(t, second)
	t.Cleanup(second.Stop)

	after, err := second.CronEntry(cronKind)
	if err != nil || after == nil {
		t.Fatalf("cron entry after restart: %v (%+v)", err, after)
	}
	if !after.ScheduledAt.Equal(armed.ScheduledAt) {
		t.Fatalf("restart moved the next fire from %s to %s", armed.ScheduledAt, after.ScheduledAt)
	}
	if after.ID != armed.ID {
		t.Fatalf("restart created a second cron entry (%s then %s)", armed.ID, after.ID)
	}
}

// A cron entry can be killed by a build that does not register its kind:
// dispatch finds no handler and retires the record. Nothing selects a terminal
// row and List hides cron entries, so unless arming revives it the heartbeat is
// gone for good — silently — even after the kind comes back.
func TestArmingRevivesACronEntryAPriorBuildKilled(t *testing.T) {
	store := newMemStore()
	clock := newFakeClock()
	opts := func() Options {
		return Options{Store: store, Now: clock.now, PollInterval: testPoll, Log: func(string, ...interface{}) {}}
	}
	noop := func(context.Context, *Job) (any, error) { return nil, nil }

	armed := New(opts())
	if err := armed.RegisterCron(cronKind, time.Hour, noop, HandlerConfig{}); err != nil {
		t.Fatalf("register cron: %v", err)
	}
	mustStart(t, armed)
	armed.Stop()

	// A build with no handler for the kind: the due entry is retired.
	clock.advance(2 * time.Hour)
	stranger := New(opts())
	if err := stranger.Register("something-else", noop); err != nil {
		t.Fatalf("register other kind: %v", err)
	}
	mustStart(t, stranger)
	waitFor(t, "the unregistered cron entry to be retired", func() bool {
		j, _ := store.LoadByKey(cronKind, CronKey)
		return j != nil && j.State.Terminal()
	})
	stranger.Stop()

	// The kind is back. The heartbeat must be beating again.
	clock.advance(2 * time.Hour)
	fired := make(chan struct{}, 4)
	revived := New(opts())
	if err := revived.RegisterCron(cronKind, time.Hour, func(context.Context, *Job) (any, error) {
		fired <- struct{}{}
		return nil, nil
	}, HandlerConfig{}); err != nil {
		t.Fatalf("re-register cron: %v", err)
	}
	mustStart(t, revived)
	t.Cleanup(revived.Stop)

	entry, err := revived.CronEntry(cronKind)
	if err != nil || entry == nil {
		t.Fatalf("cron entry after revive: %v (%+v)", err, entry)
	}
	if entry.State.Terminal() {
		t.Fatalf("cron entry left in a terminal state (%s); the heartbeat is dead for good", entry.State)
	}
	clock.advance(time.Hour)
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		j, _ := store.LoadByKey(cronKind, CronKey)
		t.Fatalf("the revived cron entry never fired; it sits at state=%q scheduled=%s", j.State, j.ScheduledAt)
	}
}

// Enqueueing ordinary work onto a recurring kind would mint a second record that
// finish() also re-arms forever — a duplicate heartbeat that List hides and
// CronEntry never finds.
func TestEnqueueRefusesACronKind(t *testing.T) {
	r, _, _ := newTestRunner(t, nil)
	noop := func(context.Context, *Job) (any, error) { return nil, nil }
	if err := r.RegisterCron(cronKind, time.Hour, noop, HandlerConfig{}); err != nil {
		t.Fatalf("register cron: %v", err)
	}
	for name, key := range map[string]string{"no key": "", "some other key": "ws-1"} {
		t.Run(name, func(t *testing.T) {
			if _, err := r.Enqueue(cronKind, EnqueueOptions{UniqueKey: key}); !errors.Is(err, ErrCronKind) {
				t.Fatalf("Enqueue(%q) = %v, want ErrCronKind", key, err)
			}
		})
	}
	// Arming itself goes through Enqueue, so the guard must not block it.
	if _, err := r.Enqueue(cronKind, EnqueueOptions{UniqueKey: CronKey, Delay: time.Hour}); err != nil {
		t.Fatalf("arming a cron entry was refused: %v", err)
	}
}

// A zero or negative interval is a registration error, not a kind that fires
// continuously or never.
func TestRegisterCronRejectsANonPositiveInterval(t *testing.T) {
	r, _, _ := newTestRunner(t, nil)
	noop := func(context.Context, *Job) (any, error) { return nil, nil }
	for name, interval := range map[string]time.Duration{"zero": 0, "negative": -time.Minute} {
		t.Run(name, func(t *testing.T) {
			if err := r.RegisterCron(cronKind+name, interval, noop, HandlerConfig{}); err == nil {
				t.Fatalf("RegisterCron accepted a %s interval", name)
			}
		})
	}
}

// CronEntry names the mistake when a kind is not recurring, rather than
// returning an empty result a caller would read as "not armed yet".
func TestCronEntryRejectsANonCronKind(t *testing.T) {
	r, _, _ := newTestRunner(t, nil)
	mustRegister(t, r, "plain", func(context.Context, *Job) (any, error) { return nil, nil })
	if _, err := r.CronEntry("plain"); !errors.Is(err, ErrNotCron) {
		t.Fatalf("CronEntry on a plain kind = %v, want ErrNotCron", err)
	}
}

// The work queue is what something asked for. A cron entry is the queue's own
// scheduler, permanently queued for its next fire, so it must not sit at the top
// of every list of outstanding work.
func TestListExcludesCronEntries(t *testing.T) {
	r, _, _ := newTestRunner(t, nil)
	noop := func(context.Context, *Job) (any, error) { return nil, nil }
	if err := r.RegisterCron(cronKind, time.Hour, noop, HandlerConfig{}); err != nil {
		t.Fatalf("register cron: %v", err)
	}
	mustRegister(t, r, "work", noop)
	mustStart(t, r)

	job, err := r.Enqueue("work", EnqueueOptions{Delay: time.Hour})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	list, err := r.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ID != job.ID {
		t.Fatalf("list = %+v, want only the enqueued work job", list)
	}
	// The entry still exists — it is hidden from the work list, not unarmed.
	if entry, err := r.CronEntry(cronKind); err != nil || entry == nil {
		t.Fatalf("cron entry after a List that omitted it: %v (%+v)", err, entry)
	}
}

// A heartbeat firing every minute forever is not a lifecycle transition worth
// reporting: routing it through the change hook would publish a durable event and
// re-push a snapshot every minute for as long as the daemon runs.
func TestCronFiringDoesNotReportAChange(t *testing.T) {
	r, _, clock := newTestRunner(t, nil)
	var changes atomic.Int64
	r.OnChange(func(string) { changes.Add(1) })
	var fires atomic.Int64
	if err := r.RegisterCron(cronKind, time.Minute, func(context.Context, *Job) (any, error) {
		fires.Add(1)
		return nil, nil
	}, HandlerConfig{}); err != nil {
		t.Fatalf("register cron: %v", err)
	}
	mustStart(t, r)

	clock.advance(time.Minute)
	waitFor(t, "the cron entry to fire", func() bool { return fires.Load() == 1 })
	clock.advance(time.Minute)
	waitFor(t, "the cron entry to fire again", func() bool { return fires.Load() == 2 })

	if got := changes.Load(); got != 0 {
		t.Fatalf("cron firing reported %d change(s), want 0", got)
	}
}
