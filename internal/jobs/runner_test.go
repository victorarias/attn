package jobs

import (
	"context"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// testPoll is the dispatch re-scan interval used throughout these tests. It is
// short so a time-gated requeue is picked up promptly once the fake clock moves;
// nothing here waits on real elapsed time.
const testPoll = 2 * time.Millisecond

// newTestRunner builds a started runner over a fresh in-memory store and a
// manually advanced clock, stopped on test cleanup.
func newTestRunner(t *testing.T, tune func(*Options)) (*Runner, *memStore, *fakeClock) {
	t.Helper()
	store := newMemStore()
	clock := newFakeClock()
	opts := Options{
		Store:        store,
		Now:          clock.now,
		PollInterval: testPoll,
		Log:          func(string, ...interface{}) {},
	}
	if tune != nil {
		tune(&opts)
	}
	r := New(opts)
	t.Cleanup(r.Stop)
	return r, store, clock
}

// newBubbleRunner builds a started runner for a synctest bubble. Inside a bubble
// the time package runs on a fake clock, so the runner needs no injected clock
// (time.Now IS the fake clock, moved by time.Sleep) and no shortened poll
// interval (production's 1s tick costs no wall-clock time). Stop is registered
// on the bubble's own T, whose cleanups run inside the bubble — the dispatch
// goroutines it joins live there.
func newBubbleRunner(t *testing.T, tune func(*Options)) (*Runner, *memStore) {
	t.Helper()
	store := newMemStore()
	opts := Options{
		Store: store,
		Log:   func(string, ...any) {},
	}
	if tune != nil {
		tune(&opts)
	}
	r := New(opts)
	t.Cleanup(r.Stop)
	return r, store
}

// waitFor polls cond until it holds or the deadline passes. Every wait in this
// file is on the dispatch loop reacting, never on wall-clock duration under
// test, so a generous deadline costs nothing when things work.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(testPoll)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func mustStart(t *testing.T, r *Runner) {
	t.Helper()
	if err := r.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
}

func mustRegister(t *testing.T, r *Runner, kind string, fn HandlerFunc) {
	t.Helper()
	if err := r.Register(kind, fn); err != nil {
		t.Fatalf("register %s: %v", kind, err)
	}
}

func mustGet(t *testing.T, r *Runner, id string) *Job {
	t.Helper()
	j, err := r.Get(id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	if j == nil {
		t.Fatalf("job %s is gone", id)
	}
	return j
}

func TestRunsAJobAndPersistsItsResult(t *testing.T) {
	r, _, _ := newTestRunner(t, nil)
	type in struct {
		Name string `json:"name"`
	}
	var seen in
	mustRegister(t, r, "greet", func(_ context.Context, job *Job) (any, error) {
		if err := job.DecodePayload(&seen); err != nil {
			return nil, err
		}
		return map[string]string{"greeting": "hello " + seen.Name}, nil
	})
	mustStart(t, r)

	job, err := r.Enqueue("greet", EnqueueOptions{Payload: in{Name: "victor"}})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	waitFor(t, "the job to finish", func() bool {
		return mustGet(t, r, job.ID).State == StateDone
	})

	done := mustGet(t, r, job.ID)
	if seen.Name != "victor" {
		t.Errorf("handler saw payload name %q, want victor", seen.Name)
	}
	if got, want := string(done.Result), `{"greeting":"hello victor"}`; got != want {
		t.Errorf("persisted result = %s, want %s", got, want)
	}
	if done.Attempts != 1 {
		t.Errorf("attempts = %d, want 1", done.Attempts)
	}
}

func TestJobsWithoutAUniqueKeyAreDistinct(t *testing.T) {
	r, store, _ := newTestRunner(t, nil)
	var mu sync.Mutex
	var payloads []string
	release := make(chan struct{})
	if err := r.RegisterWith("activity", func(_ context.Context, job *Job) (any, error) {
		var arg string
		if err := job.DecodePayload(&arg); err != nil {
			return nil, err
		}
		mu.Lock()
		payloads = append(payloads, arg)
		mu.Unlock()
		<-release
		return nil, nil
	}, HandlerConfig{MaxConcurrent: 2}); err != nil {
		t.Fatalf("register: %v", err)
	}
	mustStart(t, r)

	if _, err := r.Enqueue("activity", EnqueueOptions{Payload: "a"}); err != nil {
		t.Fatalf("enqueue a: %v", err)
	}
	if _, err := r.Enqueue("activity", EnqueueOptions{Payload: "b"}); err != nil {
		t.Fatalf("enqueue b: %v", err)
	}

	// Both must be in flight together: this is the property coalescing-by-default
	// would have made impossible, and the one durable activities need.
	waitFor(t, "both distinct jobs to be running", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(payloads) == 2
	})
	if store.count() != 2 {
		t.Errorf("store holds %d records, want 2 distinct jobs", store.count())
	}
	close(release)
}

func TestUniqueKeyCoalescesABurstIntoOneRun(t *testing.T) {
	r, store, clock := newTestRunner(t, nil)
	var runs atomic.Int32
	mustRegister(t, r, "narrate", func(context.Context, *Job) (any, error) {
		runs.Add(1)
		return nil, nil
	})
	mustStart(t, r)

	// Three triggers inside the debounce window, each pushing the run later.
	var last *Job
	for i := 0; i < 3; i++ {
		job, err := r.Enqueue("narrate", EnqueueOptions{UniqueKey: "ws-1", Delay: time.Minute})
		if err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
		last = job
	}
	if store.count() != 1 {
		t.Fatalf("store holds %d records, want 1 coalesced record", store.count())
	}
	if runs.Load() != 0 {
		t.Fatalf("job ran %d times before its debounce elapsed", runs.Load())
	}

	clock.advance(time.Minute)
	waitFor(t, "the coalesced job to run", func() bool {
		return mustGet(t, r, last.ID).State == StateDone
	})
	if got := runs.Load(); got != 1 {
		t.Errorf("handler ran %d times, want 1 — the burst should collapse", got)
	}
}

func TestRunNowOverridesAPendingDebounce(t *testing.T) {
	r, _, _ := newTestRunner(t, nil)
	mustRegister(t, r, "narrate", func(context.Context, *Job) (any, error) { return nil, nil })
	mustStart(t, r)

	if _, err := r.Enqueue("narrate", EnqueueOptions{UniqueKey: "ws-1", Delay: time.Hour}); err != nil {
		t.Fatalf("enqueue debounced: %v", err)
	}
	job, err := r.Enqueue("narrate", EnqueueOptions{UniqueKey: "ws-1", RunNow: true})
	if err != nil {
		t.Fatalf("enqueue run-now: %v", err)
	}
	// Without the override this would wait an hour of fake time, which never
	// arrives in this test.
	waitFor(t, "the run-now job to run", func() bool {
		return mustGet(t, r, job.ID).State == StateDone
	})
}

func TestATriggerArrivingMidRunRunsTheJobAgain(t *testing.T) {
	r, _, _ := newTestRunner(t, nil)
	var runs atomic.Int32
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	mustRegister(t, r, "narrate", func(context.Context, *Job) (any, error) {
		runs.Add(1)
		entered <- struct{}{}
		<-release
		return nil, nil
	})
	mustStart(t, r)

	job, err := r.Enqueue("narrate", EnqueueOptions{UniqueKey: "ws-1"})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	<-entered // the first run is in the handler

	// The trigger lands while the run is in flight. It must not be dropped, and it
	// must not tear the in-flight run.
	if _, err := r.Enqueue("narrate", EnqueueOptions{UniqueKey: "ws-1", RunNow: true}); err != nil {
		t.Fatalf("mid-run enqueue: %v", err)
	}
	if got := mustGet(t, r, job.ID); !got.Requeued {
		t.Fatalf("mid-run enqueue did not mark the record requeued (state %s)", got.State)
	}
	close(release)

	<-entered // the second run, honoring the coalesced trigger
	waitFor(t, "the re-run to finish", func() bool {
		return mustGet(t, r, job.ID).State == StateDone
	})
	if got := runs.Load(); got != 2 {
		t.Errorf("handler ran %d times, want 2 (the run plus the coalesced re-run)", got)
	}
}

// Converted to synctest (spike leg 1). time.Sleep moves the bubble's fake clock,
// so the backoff windows are advanced by sleeping exactly the interval under
// test; synctest.Wait replaces every poll loop by blocking until the dispatch
// loop and the run it launched are done, which is what "the attempt failed"
// actually means.
func TestFailuresBackOffThenGoDeadOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, _ := newBubbleRunner(t, func(o *Options) {
			o.MaxAttempts = 3
			o.BackoffBase = time.Minute
		})
		var deadCalls atomic.Int32
		var deadState atomic.Value
		r.OnTerminalFailure(func(j *Job) {
			deadCalls.Add(1)
			deadState.Store(string(j.State))
		})
		mustRegister(t, r, "flaky", func(context.Context, *Job) (any, error) {
			return nil, errors.New("boom")
		})
		mustStart(t, r)

		job, err := r.Enqueue("flaky", EnqueueOptions{})
		if err != nil {
			t.Fatalf("enqueue: %v", err)
		}

		// Attempt 1 fails and schedules a retry one base interval out.
		synctest.Wait()
		after1 := mustGet(t, r, job.ID)
		if after1.State != StateFailed {
			t.Fatalf("state after the first attempt = %s, want failed", after1.State)
		}
		if got, want := after1.ScheduledAt, time.Now().UTC().Add(time.Minute); !got.Equal(want) {
			t.Errorf("retry scheduled at %s, want %s (one base interval)", got, want)
		}

		// Attempt 2 fails and doubles the delay.
		time.Sleep(time.Minute)
		synctest.Wait()
		after2 := mustGet(t, r, job.ID)
		if after2.State != StateFailed || after2.Attempts != 2 {
			t.Fatalf("after the second attempt: state %s attempts %d, want failed/2", after2.State, after2.Attempts)
		}
		if got, want := after2.ScheduledAt, time.Now().UTC().Add(2*time.Minute); !got.Equal(want) {
			t.Errorf("second retry scheduled at %s, want %s (doubled)", got, want)
		}

		// Attempt 3 hits the cap and the job dies.
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		dead := mustGet(t, r, job.ID)
		if dead.State != StateDead {
			t.Fatalf("state after the third attempt = %s, want dead", dead.State)
		}
		if dead.Attempts != 3 {
			t.Errorf("attempts = %d, want 3", dead.Attempts)
		}
		if dead.LastError != "boom" {
			t.Errorf("last error = %q, want boom", dead.LastError)
		}

		// The terminal hook is the notification surface's signal: it must fire once on
		// the crossing, not on every transient failure and not again afterwards.
		if got := deadCalls.Load(); got != 1 {
			t.Errorf("terminal-failure hook fired %d times, want exactly 1", got)
		}
		if got := deadState.Load(); got != string(StateDead) {
			t.Errorf("terminal-failure hook saw state %v, want dead", got)
		}

		// A dead job stays dead: nothing re-selects it once the cap is spent. An hour
		// of fake time really does pass here — every poll tick in it runs a dispatch
		// pass — so this is the claim itself, not a sleep standing in for it.
		time.Sleep(time.Hour)
		synctest.Wait()
		if got := mustGet(t, r, job.ID); got.Attempts != 3 {
			t.Errorf("dead job was retried (attempts = %d)", got.Attempts)
		}
	})
}

func TestAJobCanRaiseItsOwnAttemptCap(t *testing.T) {
	r, _, clock := newTestRunner(t, func(o *Options) {
		o.MaxAttempts = 1
		o.BackoffBase = time.Minute
	})
	mustRegister(t, r, "flaky", func(context.Context, *Job) (any, error) {
		return nil, errors.New("boom")
	})
	mustStart(t, r)

	job, err := r.Enqueue("flaky", EnqueueOptions{MaxAttempts: 2})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	// With the runner default of 1 this would already be dead.
	waitFor(t, "the first attempt to fail", func() bool {
		return mustGet(t, r, job.ID).State == StateFailed
	})
	clock.advance(time.Minute)
	waitFor(t, "the job to die at its own cap", func() bool {
		return mustGet(t, r, job.ID).State == StateDead
	})
	if got := mustGet(t, r, job.ID).Attempts; got != 2 {
		t.Errorf("attempts = %d, want 2 (the job's own cap)", got)
	}
}

func TestRetryRevivesADeadJob(t *testing.T) {
	r, _, _ := newTestRunner(t, func(o *Options) { o.MaxAttempts = 1 })
	var fail atomic.Bool
	fail.Store(true)
	mustRegister(t, r, "flaky", func(context.Context, *Job) (any, error) {
		if fail.Load() {
			return nil, errors.New("boom")
		}
		return nil, nil
	})
	mustStart(t, r)

	job, err := r.Enqueue("flaky", EnqueueOptions{})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	waitFor(t, "the job to die", func() bool {
		return mustGet(t, r, job.ID).State == StateDead
	})

	fail.Store(false)
	if _, err := r.Retry(job.ID); err != nil {
		t.Fatalf("retry: %v", err)
	}
	waitFor(t, "the retried job to succeed", func() bool {
		return mustGet(t, r, job.ID).State == StateDone
	})
	if got := mustGet(t, r, job.ID).LastError; got != "" {
		t.Errorf("last error = %q, want it cleared by the successful retry", got)
	}
}

func TestCancelWaitsForTheCommitFence(t *testing.T) {
	r, _, _ := newTestRunner(t, nil)
	committing := make(chan struct{})
	finishCommit := make(chan struct{})
	var wrote atomic.Bool
	mustRegister(t, r, "commits", func(ctx context.Context, job *Job) (any, error) {
		if !job.CommitGuard.Enter() {
			return nil, errors.New("fenced before commit")
		}
		defer job.CommitGuard.Leave()
		close(committing)
		<-finishCommit
		// A cancel that arrived while we were inside the fence must not have
		// cancelled the context out from under the write.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		wrote.Store(true)
		return nil, nil
	})
	mustStart(t, r)

	job, err := r.Enqueue("commits", EnqueueOptions{})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	<-committing

	cancelReturned := make(chan struct{})
	go func() {
		r.Cancel(job.ID)
		close(cancelReturned)
	}()

	// Cancel must still be blocked: the run is inside its commit.
	select {
	case <-cancelReturned:
		t.Fatal("Cancel returned while the handler was inside its commit fence")
	case <-time.After(20 * testPoll):
	}

	close(finishCommit)
	<-cancelReturned
	if !wrote.Load() {
		t.Error("the durable write was torn by the cancel")
	}
	if got := mustGet(t, r, job.ID).State; got != StateDone {
		t.Errorf("state = %s, want done — the fenced run completed", got)
	}
}

func TestCancelBeforeTheFenceStopsTheWrite(t *testing.T) {
	r, _, _ := newTestRunner(t, nil)
	started := make(chan struct{})
	var wrote atomic.Bool
	mustRegister(t, r, "commits", func(ctx context.Context, job *Job) (any, error) {
		close(started)
		<-ctx.Done() // wait to be cancelled, then try to commit anyway
		if !job.CommitGuard.Enter() {
			return nil, errors.New("cancelled before commit")
		}
		defer job.CommitGuard.Leave()
		wrote.Store(true)
		return nil, nil
	})
	mustStart(t, r)

	job, err := r.Enqueue("commits", EnqueueOptions{})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	<-started
	r.Cancel(job.ID) // blocks until the run goroutine has fully exited

	if wrote.Load() {
		t.Error("the handler committed after being fenced")
	}
	// Cancel's contract is that the durable outcome is already recorded when it
	// returns, with no polling needed here.
	if got := mustGet(t, r, job.ID).State; got != StateFailed {
		t.Errorf("state = %s, want failed — the cancelled run recorded its outcome", got)
	}
}

func TestRemoveByKeyForgetsTheJob(t *testing.T) {
	r, store, _ := newTestRunner(t, nil)
	mustRegister(t, r, "compact", func(context.Context, *Job) (any, error) { return nil, nil })
	mustStart(t, r)

	if _, err := r.Enqueue("compact", EnqueueOptions{UniqueKey: "ws-1", Delay: time.Hour}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if store.count() != 1 {
		t.Fatalf("store holds %d records, want 1", store.count())
	}

	// The caller knows the subject, never the generated id — which is the whole
	// reason this surface exists.
	r.RemoveByKey("compact", "ws-1")
	if store.count() != 0 {
		t.Errorf("store holds %d records after removal, want 0", store.count())
	}
	r.RemoveByKey("compact", "ws-1") // removing what is already gone is a no-op
}

func TestStartRequeuesAJobLeftRunningByACrash(t *testing.T) {
	store := newMemStore()
	clock := newFakeClock()
	// A record left mid-run by a daemon that died.
	orphan := &Job{
		ID:          "orphan",
		Kind:        "compact",
		State:       StateRunning,
		Attempts:    1,
		ScheduledAt: clock.now().Add(-time.Hour),
		CreatedAt:   clock.now().Add(-time.Hour),
		UpdatedAt:   clock.now().Add(-time.Hour),
	}
	if err := store.Save(orphan); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}

	r := New(Options{Store: store, Now: clock.now, PollInterval: testPoll, Log: func(string, ...interface{}) {}})
	t.Cleanup(r.Stop)
	var ran atomic.Bool
	mustRegister(t, r, "compact", func(context.Context, *Job) (any, error) {
		ran.Store(true)
		return nil, nil
	})
	mustStart(t, r)

	waitFor(t, "the recovered job to run", func() bool { return ran.Load() })
	waitFor(t, "the recovered job to finish", func() bool {
		return mustGet(t, r, "orphan").State == StateDone
	})
}

func TestPriorityOrdersTheQueue(t *testing.T) {
	r, _, _ := newTestRunner(t, nil)
	var mu sync.Mutex
	var order []string
	mustRegister(t, r, "ordered", func(_ context.Context, job *Job) (any, error) {
		var name string
		if err := job.DecodePayload(&name); err != nil {
			return nil, err
		}
		mu.Lock()
		order = append(order, name)
		mu.Unlock()
		return nil, nil
	})

	// Enqueue before starting so all three are eligible in the same first pass;
	// the per-kind cap of 1 then serializes them in selection order.
	if _, err := r.Enqueue("ordered", EnqueueOptions{Payload: "low", Priority: 1}); err != nil {
		t.Fatalf("enqueue low: %v", err)
	}
	if _, err := r.Enqueue("ordered", EnqueueOptions{Payload: "high", Priority: 10}); err != nil {
		t.Fatalf("enqueue high: %v", err)
	}
	if _, err := r.Enqueue("ordered", EnqueueOptions{Payload: "mid", Priority: 5}); err != nil {
		t.Fatalf("enqueue mid: %v", err)
	}
	mustStart(t, r)

	waitFor(t, "all three jobs to run", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(order) == 3
	})
	mu.Lock()
	defer mu.Unlock()
	want := []string{"high", "mid", "low"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("ran in order %v, want %v", order, want)
		}
	}
}

// Converted to synctest (spike leg 1). The load-bearing assertion here is a
// negative — the second serial job must NOT start — which a real sleep can only
// make probable. synctest.Wait blocks until the dispatch loop has nothing left
// to do, so "it never started" is a fact about a settled system rather than
// about 40ms of waiting.
func TestAKindIsSerializedWithItselfButNotWithOthers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, _ := newBubbleRunner(t, nil)
		var serialInflight, serialPeak atomic.Int32
		release := make(chan struct{})
		bothKinds := make(chan string, 2)

		mustRegister(t, r, "serial", func(context.Context, *Job) (any, error) {
			n := serialInflight.Add(1)
			for {
				peak := serialPeak.Load()
				if n <= peak || serialPeak.CompareAndSwap(peak, n) {
					break
				}
			}
			bothKinds <- "serial"
			<-release
			serialInflight.Add(-1)
			return nil, nil
		})
		mustRegister(t, r, "other", func(context.Context, *Job) (any, error) {
			bothKinds <- "other"
			<-release
			return nil, nil
		})
		mustStart(t, r)

		for i := range 2 {
			if _, err := r.Enqueue("serial", EnqueueOptions{}); err != nil {
				t.Fatalf("enqueue serial %d: %v", i, err)
			}
		}
		if _, err := r.Enqueue("other", EnqueueOptions{}); err != nil {
			t.Fatalf("enqueue other: %v", err)
		}

		// One serial job and the other kind run together; the second serial job waits.
		synctest.Wait()
		got := map[string]bool{}
		for range 2 {
			select {
			case kind := <-bothKinds:
				got[kind] = true
			default:
				t.Fatalf("only %v started once dispatch settled, want both serial and other", got)
			}
		}
		if !got["serial"] || !got["other"] {
			t.Fatalf("kinds running together = %v, want both serial and other", got)
		}
		if peak := serialPeak.Load(); peak != 1 {
			t.Errorf("peak concurrent serial runs = %d, want 1", peak)
		}
		close(release)
	})
}

func TestAnUnregisteredKindFailsInPlace(t *testing.T) {
	store := newMemStore()
	clock := newFakeClock()
	// A record from an older build whose kind this binary no longer knows.
	stale := &Job{
		ID:          "stale",
		Kind:        "retired_kind",
		State:       StateQueued,
		ScheduledAt: clock.now(),
		CreatedAt:   clock.now(),
		UpdatedAt:   clock.now(),
	}
	if err := store.Save(stale); err != nil {
		t.Fatalf("seed stale: %v", err)
	}

	r := New(Options{Store: store, Now: clock.now, PollInterval: testPoll, MaxAttempts: 1, Log: func(string, ...interface{}) {}})
	t.Cleanup(r.Stop)
	mustStart(t, r)

	// It must surface as a failure rather than being silently re-selected forever.
	waitFor(t, "the unknown-kind job to die", func() bool {
		return mustGet(t, r, "stale").State == StateDead
	})
	if got := mustGet(t, r, "stale").LastError; got == "" {
		t.Error("unknown-kind failure recorded no error to read")
	}
}

func TestEnqueueRejectsAnUnregisteredKind(t *testing.T) {
	r, _, _ := newTestRunner(t, nil)
	mustStart(t, r)
	if _, err := r.Enqueue("nope", EnqueueOptions{}); !errors.Is(err, ErrUnknownKind) {
		t.Errorf("enqueue error = %v, want ErrUnknownKind", err)
	}
}

func TestAnUnmarshallableResultFailsTheRun(t *testing.T) {
	r, _, _ := newTestRunner(t, func(o *Options) { o.MaxAttempts = 1 })
	mustRegister(t, r, "bad_result", func(context.Context, *Job) (any, error) {
		// math.Inf has no JSON representation. The work "succeeded", but a result
		// nobody can read must not be reported as success.
		return math.Inf(1), nil
	})
	mustStart(t, r)

	job, err := r.Enqueue("bad_result", EnqueueOptions{})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	waitFor(t, "the run to fail on its result", func() bool {
		return mustGet(t, r, job.ID).State == StateDead
	})
	if got := mustGet(t, r, job.ID).LastError; got == "" {
		t.Error("the marshal failure was not recorded")
	}
}

func TestRetentionTrimsCompletedJobsAndKeepsDeadOnes(t *testing.T) {
	r, store, clock := newTestRunner(t, func(o *Options) {
		o.MaxAttempts = 1
		o.Retention = 24 * time.Hour
	})
	mustRegister(t, r, "ok", func(context.Context, *Job) (any, error) { return nil, nil })
	mustRegister(t, r, "bad", func(context.Context, *Job) (any, error) {
		return nil, errors.New("boom")
	})
	mustStart(t, r)

	done, err := r.Enqueue("ok", EnqueueOptions{})
	if err != nil {
		t.Fatalf("enqueue ok: %v", err)
	}
	dead, err := r.Enqueue("bad", EnqueueOptions{})
	if err != nil {
		t.Fatalf("enqueue bad: %v", err)
	}
	waitFor(t, "both jobs to settle", func() bool {
		return mustGet(t, r, done.ID).State == StateDone && mustGet(t, r, dead.ID).State == StateDead
	})

	if got := r.Trim(); got != 0 {
		t.Errorf("trimmed %d fresh jobs, want 0", got)
	}

	clock.advance(48 * time.Hour)
	if got := r.Trim(); got != 1 {
		t.Errorf("trimmed %d jobs, want 1 (the completed one)", got)
	}
	if j, _ := r.Get(done.ID); j != nil {
		t.Error("the completed job survived retention")
	}
	// The dead job is the record a failure notification points at, and it only
	// exists because nobody acted on it. Retention must not swallow it.
	if j, _ := r.Get(dead.ID); j == nil {
		t.Error("the dead job was trimmed; it is the actionable record")
	}
	if store.count() != 1 {
		t.Errorf("store holds %d records, want 1", store.count())
	}
}

func TestAFailedClaimReleasesItsConcurrencySlot(t *testing.T) {
	r, store, clock := newTestRunner(t, nil)
	var runs atomic.Int32
	mustRegister(t, r, "compact", func(context.Context, *Job) (any, error) {
		runs.Add(1)
		return nil, nil
	})
	mustStart(t, r)

	// Park the job behind a debounce so the enqueue's own write lands first and
	// the armed failure is guaranteed to hit the dispatch claim instead.
	job, err := r.Enqueue("compact", EnqueueOptions{UniqueKey: "ws-1", Delay: time.Minute})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	// The claim write fails once. If the reserved per-kind slot leaked, the kind
	// would be saturated forever and nothing of it would ever run again.
	store.failNextSave(errors.New("disk on fire"))
	clock.advance(time.Minute)
	waitFor(t, "the job to run despite the failed claim", func() bool {
		return mustGet(t, r, job.ID).State == StateDone
	})
	if got := runs.Load(); got != 1 {
		t.Errorf("handler ran %d times, want 1", got)
	}
}

func TestASecondRunnerRefusesTheSameStore(t *testing.T) {
	r, store, clock := newTestRunner(t, nil)
	mustStart(t, r)

	second := New(Options{Store: store, Now: clock.now, PollInterval: testPoll, Log: func(string, ...interface{}) {}})
	if err := second.Start(); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second Start error = %v, want ErrAlreadyRunning", err)
	}
}

func TestADisabledRunnerIsASafeNoOp(t *testing.T) {
	r := New(Options{})
	if !r.Disabled() {
		t.Fatal("a runner with no store should be disabled")
	}
	if err := r.Start(); err != nil {
		t.Errorf("Start on a disabled runner = %v, want nil", err)
	}
	if _, err := r.Enqueue("anything", EnqueueOptions{}); !errors.Is(err, ErrDisabled) {
		t.Errorf("Enqueue = %v, want ErrDisabled", err)
	}
	if err := r.Register("anything", func(context.Context, *Job) (any, error) { return nil, nil }); !errors.Is(err, ErrDisabled) {
		t.Errorf("Register = %v, want ErrDisabled", err)
	}
	list, err := r.List()
	if err != nil || list != nil {
		t.Errorf("List = (%v, %v), want (nil, nil)", list, err)
	}
	r.Cancel("anything")
	r.Remove("anything")
	r.RemoveByKey("anything", "key")
	r.Stop()
}

func TestStopDrainsInFlightRuns(t *testing.T) {
	r, _, _ := newTestRunner(t, nil)
	started := make(chan struct{})
	var exited atomic.Bool
	mustRegister(t, r, "slow", func(ctx context.Context, _ *Job) (any, error) {
		close(started)
		<-ctx.Done()
		exited.Store(true)
		return nil, ctx.Err()
	})
	mustStart(t, r)

	if _, err := r.Enqueue("slow", EnqueueOptions{}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	<-started
	r.Stop() // cancels the run and blocks until it has exited
	if !exited.Load() {
		t.Error("Stop returned before the in-flight run exited")
	}
	r.Stop() // a second Stop must not panic on the closed channel
}
