package jobs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

const cronKind = "heartbeat"

// A cron entry is armed by Start one interval out, fires when that interval
// elapses, and is immediately re-armed for the next one.
//
// Converted to synctest (spike leg 1). Recurrence is the one thing worth
// testing against a clock that really moves: sleeping the entry's own interval
// in the bubble exercises the arm/fire/re-arm cycle at its real cadence, and
// costs no wall-clock time.
func TestACronEntryFiresOnItsIntervalAndRearms(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, _ := newBubbleRunner(t, nil)
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
		if want := time.Now().UTC().Add(time.Hour); !armedFor.Equal(want) {
			t.Fatalf("first fire armed for %s, want %s", armedFor, want)
		}
		if fires.Load() != 0 {
			t.Fatalf("cron fired %d times before its interval elapsed", fires.Load())
		}

		time.Sleep(time.Hour)
		synctest.Wait()
		if got := fires.Load(); got != 1 {
			t.Fatalf("cron fired %d times after one interval, want 1", got)
		}
		next, err := r.CronEntry(cronKind)
		if err != nil || next == nil {
			t.Fatalf("cron entry after the fire: %v (%+v)", err, next)
		}
		if next.State != StateQueued || !next.ScheduledAt.After(armedFor) {
			t.Fatalf("cron entry did not re-arm: state %s scheduled %s (was %s)", next.State, next.ScheduledAt, armedFor)
		}

		time.Sleep(time.Hour)
		synctest.Wait()
		if got := fires.Load(); got != 2 {
			t.Fatalf("cron fired %d times after two intervals, want 2", got)
		}
	})
}

// A failing fire never retires the entry: it records the error and stays armed,
// because a heartbeat that gives up is a silent outage.
func TestAFailingCronEntryStaysArmed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, _ := newBubbleRunner(t, func(o *Options) { o.MaxAttempts = 1 })
		var fires atomic.Int64
		if err := r.RegisterCron(cronKind, time.Minute, func(context.Context, *Job) (any, error) {
			fires.Add(1)
			return nil, errors.New("tick failed")
		}, HandlerConfig{}); err != nil {
			t.Fatalf("register cron: %v", err)
		}
		mustStart(t, r)

		// Three intervals at their real length. synctest.Wait settles each fire and
		// its re-arm before the clock moves again, so the sequencing the fake-clock
		// version had to reason about explicitly — never advance past a schedule the
		// last fire has not written yet — is now a property of the bubble.
		for want := int64(1); want <= 3; want++ {
			time.Sleep(time.Minute)
			synctest.Wait()
			if got := fires.Load(); got != want {
				t.Fatalf("cron fired %d times after %d intervals, want %d", got, want, want)
			}
			next, err := r.CronEntry(cronKind)
			if err != nil || next == nil {
				t.Fatalf("cron entry after fire %d: %v (%+v)", want, err, next)
			}
			if next.State != StateQueued || !next.ScheduledAt.After(time.Now()) {
				t.Fatalf("cron entry did not re-arm after fire %d: state %s scheduled %s", want, next.State, next.ScheduledAt)
			}
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
	})
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
	synctest.Test(t, func(t *testing.T) {
		store := newMemStore()
		opts := func() Options {
			return Options{Store: store, Log: func(string, ...any) {}}
		}
		noop := func(context.Context, *Job) (any, error) { return nil, nil }

		armed := New(opts())
		if err := armed.RegisterCron(cronKind, time.Hour, noop, HandlerConfig{}); err != nil {
			t.Fatalf("register cron: %v", err)
		}
		mustStart(t, armed)
		armed.Stop()

		// A build with no handler for the kind: the due entry is retired.
		time.Sleep(2 * time.Hour)
		stranger := New(opts())
		if err := stranger.Register("something-else", noop); err != nil {
			t.Fatalf("register other kind: %v", err)
		}
		mustStart(t, stranger)
		synctest.Wait()
		if j, _ := store.LoadByKey(cronKind, CronKey); j == nil || !j.State.Terminal() {
			t.Fatalf("the unregistered cron entry was not retired (%+v)", j)
		}
		stranger.Stop()

		// The kind is back. The heartbeat must be beating again.
		time.Sleep(2 * time.Hour)
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
		time.Sleep(time.Hour)
		synctest.Wait()
		select {
		case <-fired:
		default:
			j, _ := store.LoadByKey(cronKind, CronKey)
			t.Fatalf("the revived cron entry never fired; it sits at state=%q scheduled=%s", j.State, j.ScheduledAt)
		}
	})
}

// The store can refuse the arming write — a busy SQLite under load is the case
// that happens. A kind that fails to arm has no record at all: dispatch never
// selects it and finish() never re-arms it, so noting the error and moving on
// retires the duty until someone restarts the daemon. Arming keeps trying, and
// while it has not landed it says so both in the log and to a caller asking.
func TestArmingRetriesUntilTheStoreTakesTheCronEntry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var logMu sync.Mutex
		var logged []string
		r, store := newBubbleRunner(t, func(o *Options) {
			o.Log = func(format string, args ...any) {
				logMu.Lock()
				logged = append(logged, fmt.Sprintf(format, args...))
				logMu.Unlock()
			}
		})
		var fires atomic.Int64
		if err := r.RegisterCron(cronKind, time.Hour, func(context.Context, *Job) (any, error) {
			fires.Add(1)
			return nil, nil
		}, HandlerConfig{}); err != nil {
			t.Fatalf("register cron: %v", err)
		}

		store.refuseSaves(errors.New("database is locked"))
		mustStart(t, r)
		synctest.Wait()

		entry, err := r.CronEntry(cronKind)
		if entry != nil {
			t.Fatalf("a store that refused every write still produced a cron entry: %+v", entry)
		}
		if err == nil {
			t.Fatal("an unarmed cron entry reported no error, which reads exactly like a healthy one that has not been written yet")
		}
		for _, want := range []string{cronKind, "database is locked"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("CronEntry error %q does not name %q", err, want)
			}
		}
		logMu.Lock()
		lines := strings.Join(logged, "\n")
		logMu.Unlock()
		if !strings.Contains(lines, cronKind) || !strings.Contains(lines, "database is locked") {
			t.Fatalf("nothing in the log names the kind and the cause:\n%s", lines)
		}

		store.healSaves()
		// The retry is one backoff away and the dispatch loop is what runs it.
		time.Sleep(DefaultBackoffBase + time.Second)
		synctest.Wait()

		armed, err := r.CronEntry(cronKind)
		if err != nil || armed == nil {
			t.Fatalf("cron entry after the store recovered: %v (%+v)", err, armed)
		}
		if armed.State != StateQueued {
			t.Fatalf("re-armed cron entry state = %s, want queued", armed.State)
		}
		// Armed is not the same as beating: let the interval elapse and watch it fire.
		time.Sleep(time.Hour)
		synctest.Wait()
		if got := fires.Load(); got != 1 {
			t.Fatalf("the re-armed cron fired %d times over its interval, want 1", got)
		}
	})
}

// A runner whose Start failed arms nothing at all, so every kind it registered is
// missing for a reason it alone knows. Losing the single-instance lock to another
// runner is how that happens.
func TestCronEntryReportsARunnerThatNeverStarted(t *testing.T) {
	store := newMemStore()
	opts := Options{Store: store, PollInterval: testPoll, Log: func(string, ...any) {}}

	holder := New(opts)
	mustStart(t, holder)
	t.Cleanup(holder.Stop)

	blocked := New(opts)
	if err := blocked.RegisterCron(cronKind, time.Hour, func(context.Context, *Job) (any, error) {
		return nil, nil
	}, HandlerConfig{}); err != nil {
		t.Fatalf("register cron: %v", err)
	}
	if err := blocked.Start(); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second Start = %v, want ErrAlreadyRunning", err)
	}

	entry, err := blocked.CronEntry(cronKind)
	if entry != nil {
		t.Fatalf("a runner that never started produced a cron entry: %+v", entry)
	}
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("CronEntry on a runner that never started = %v, want it to carry %v", err, ErrAlreadyRunning)
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
	synctest.Test(t, func(t *testing.T) {
		r, _ := newBubbleRunner(t, nil)
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

		for want := int64(1); want <= 2; want++ {
			time.Sleep(time.Minute)
			synctest.Wait()
			if got := fires.Load(); got != want {
				t.Fatalf("cron fired %d times after %d intervals, want %d", got, want, want)
			}
		}

		// The negative that matters: no change was reported, on a system that has
		// nothing left to do rather than one that has merely not got round to it.
		if got := changes.Load(); got != 0 {
			t.Fatalf("cron firing reported %d change(s), want 0", got)
		}
	})
}
