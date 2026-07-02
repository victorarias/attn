package tasks

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock is a deterministic, monotonic, race-safe clock. Tests advance it
// explicitly so backoff/coalescing assertions never depend on wall-clock timing.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// testRunner builds a runner rooted at a temp dir with a short poll interval (so
// the worker actually makes progress in tests) and the supplied fake clock.
func testRunner(t *testing.T, clock *fakeClock) *Runner {
	t.Helper()
	root := t.TempDir()
	r := New(Options{
		Root:         root,
		Now:          clock.now,
		PollInterval: 2 * time.Millisecond,
		Log:          func(string, ...interface{}) {},
	})
	return r
}

// storeRoot returns the on-disk root behind a file-backed runner. The runner's
// store is the Store interface now, so the on-disk assertions reach the concrete
// FileStore through this helper.
func storeRoot(t *testing.T, r *Runner) string {
	t.Helper()
	fs, ok := r.store.(*FileStore)
	if !ok {
		t.Fatalf("runner is not file-backed (%T)", r.store)
	}
	return fs.s.root
}

// waitFor polls cond until it is true or the deadline elapses. It is used ONLY to
// observe the worker's eventual durable state (the worker runs in its own
// goroutine on a short ticker); ordering guarantees in the cancel/commit tests use
// channels, not waitFor.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func loadTask(t *testing.T, r *Runner, id string) *Task {
	t.Helper()
	task, err := r.Get(id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	if task == nil {
		t.Fatalf("task %s not found", id)
	}
	return task
}

// --- disabled runner -------------------------------------------------------

func TestDisabledRunnerIsSafe(t *testing.T) {
	r := New(Options{Root: "   "}) // whitespace root ⇒ disabled
	if !r.Disabled() {
		t.Fatal("expected runner disabled with empty root")
	}
	if err := r.Register("k", func(context.Context, *Task) error { return nil }); err != ErrDisabled {
		t.Fatalf("Register on disabled runner: got %v want ErrDisabled", err)
	}
	if _, err := r.Enqueue("k", "s", EnqueueOptions{}); err != ErrDisabled {
		t.Fatalf("Enqueue on disabled runner: got %v want ErrDisabled", err)
	}
	if _, err := r.Retry("k:s"); err != ErrDisabled {
		t.Fatalf("Retry on disabled runner: got %v want ErrDisabled", err)
	}
	// Start/Stop/Cancel/List/Get must not panic and must be inert.
	if err := r.Start(); err != nil {
		t.Fatalf("Start on disabled runner: %v", err)
	}
	r.Cancel("k:s")
	if list, err := r.List(); err != nil || list != nil {
		t.Fatalf("List on disabled runner: got %v,%v", list, err)
	}
	r.Stop()
}

// --- derived-id idempotent enqueue ----------------------------------------

func TestDerivedIDEnqueueIsIdempotent(t *testing.T) {
	clock := newFakeClock()
	r := testRunner(t, clock)
	if err := r.Register("compact_context", func(context.Context, *Task) error { return nil }); err != nil {
		t.Fatal(err)
	}
	// Do NOT Start — we are asserting the on-disk record shape, not execution.

	id := TaskID("compact_context", "ws-1")
	for i := 0; i < 3; i++ {
		if _, err := r.Enqueue("compact_context", "ws-1", EnqueueOptions{}); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}

	all, err := r.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("expected exactly one record, got %d", len(all))
	}
	if all[0].ID != id {
		t.Fatalf("derived id: got %q want %q", all[0].ID, id)
	}
	// Exactly one file on disk.
	files, _ := os.ReadDir(stateDir(storeRoot(t, r)))
	jsonCount := 0
	for _, f := range files {
		if filepath.Ext(f.Name()) == ".json" {
			jsonCount++
		}
	}
	if jsonCount != 1 {
		t.Fatalf("expected one json file, got %d", jsonCount)
	}
}

// --- coalescing: forward-push and zero-debounce override -------------------

func TestEnqueueForwardPushDebounce(t *testing.T) {
	clock := newFakeClock()
	r := testRunner(t, clock)
	_ = r.Register("narrate_workspace", func(context.Context, *Task) error { return nil })

	debounce := 10 * time.Minute
	first, err := r.Enqueue("narrate_workspace", "ws-1", EnqueueOptions{Debounce: debounce})
	if err != nil {
		t.Fatal(err)
	}
	wantFirst := clock.now().Add(debounce)
	if !first.NextAttemptAt.Equal(wantFirst) {
		t.Fatalf("first NextAttemptAt: got %s want %s", first.NextAttemptAt, wantFirst)
	}

	// Advance partway, then re-enqueue: the debounce window pushes FORWARD from the
	// new now, not from the original schedule.
	clock.advance(3 * time.Minute)
	second, err := r.Enqueue("narrate_workspace", "ws-1", EnqueueOptions{Debounce: debounce})
	if err != nil {
		t.Fatal(err)
	}
	wantSecond := clock.now().Add(debounce)
	if !second.NextAttemptAt.Equal(wantSecond) {
		t.Fatalf("second NextAttemptAt: got %s want %s", second.NextAttemptAt, wantSecond)
	}
	if !second.NextAttemptAt.After(first.NextAttemptAt) {
		t.Fatalf("re-enqueue should push NextAttemptAt forward: first=%s second=%s",
			first.NextAttemptAt, second.NextAttemptAt)
	}
	// Same record, not a duplicate.
	if second.ID != first.ID {
		t.Fatalf("coalesce should keep same id: %s vs %s", first.ID, second.ID)
	}
	all, _ := r.List()
	if len(all) != 1 {
		t.Fatalf("expected one coalesced record, got %d", len(all))
	}
}

func TestEnqueueZeroDebounceOverridesPendingDebounce(t *testing.T) {
	clock := newFakeClock()
	r := testRunner(t, clock)
	_ = r.Register("narrate_workspace", func(context.Context, *Task) error { return nil })

	// A pending debounced task.
	if _, err := r.Enqueue("narrate_workspace", "ws-1", EnqueueOptions{Debounce: time.Hour}); err != nil {
		t.Fatal(err)
	}
	// The removal-boundary final task overrides with zero-debounce.
	final, err := r.Enqueue("narrate_workspace", "ws-1", EnqueueOptions{Debounce: time.Hour, ZeroDebounce: true})
	if err != nil {
		t.Fatal(err)
	}
	if !final.NextAttemptAt.Equal(clock.now()) {
		t.Fatalf("zero-debounce should set NextAttemptAt=now: got %s want %s",
			final.NextAttemptAt, clock.now())
	}
}

// --- requeue + capped-exponential backoff schedule -------------------------

func TestFailedTaskBackoffScheduleAndDeadCap(t *testing.T) {
	clock := newFakeClock()
	root := t.TempDir()
	var attempts int64
	r := New(Options{
		Root:         root,
		Now:          clock.now,
		PollInterval: 2 * time.Millisecond,
		MaxAttempts:  4,
		BackoffBase:  time.Minute,
		BackoffCap:   10 * time.Minute,
		Log:          func(string, ...interface{}) {},
	})
	failErr := context.DeadlineExceeded
	_ = r.Register("flaky", func(context.Context, *Task) error {
		atomic.AddInt64(&attempts, 1)
		return failErr
	})
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()

	id := TaskID("flaky", "s")
	if _, err := r.Enqueue("flaky", "s", EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}

	// Expected delays for attempts 1..3 (the 4th -> dead): base*2^(n-1), capped.
	// attempt1 fail -> 1m, attempt2 -> 2m, attempt3 -> 4m, attempt4 -> dead.
	wantDelays := []time.Duration{1 * time.Minute, 2 * time.Minute, 4 * time.Minute}

	for n, want := range wantDelays {
		expectedAttempts := int64(n + 1)
		waitFor(t, "attempt to be spent and task to land failed", func() bool {
			task, _ := r.Get(id)
			return task != nil && task.State == StateFailed &&
				task.Attempts == int(expectedAttempts) &&
				atomic.LoadInt64(&attempts) == expectedAttempts
		})
		task := loadTask(t, r, id)
		gotDelay := task.NextAttemptAt.Sub(clock.now())
		if gotDelay != want {
			t.Fatalf("after attempt %d: backoff delay got %s want %s", expectedAttempts, gotDelay, want)
		}
		if task.LastError != failErr.Error() {
			t.Fatalf("last_error got %q want %q", task.LastError, failErr.Error())
		}
		// Advance the clock past the backoff so the next auto-requeue becomes eligible.
		clock.advance(want)
	}

	// The 4th attempt hits the cap and goes dead.
	waitFor(t, "task to go dead at the attempt cap", func() bool {
		task, _ := r.Get(id)
		return task != nil && task.State == StateDead
	})
	dead := loadTask(t, r, id)
	if dead.Attempts != 4 {
		t.Fatalf("dead at attempts: got %d want 4", dead.Attempts)
	}
	if atomic.LoadInt64(&attempts) != 4 {
		t.Fatalf("executor invoked %d times, want 4", atomic.LoadInt64(&attempts))
	}
}

func TestBackoffIsCapped(t *testing.T) {
	r := New(Options{Root: t.TempDir(), BackoffBase: time.Minute, BackoffCap: time.Hour})
	// 1m,2m,4m,8m,16m,32m,64m->cap. attempt 7 would be 64m > 60m cap.
	cases := map[int]time.Duration{
		1:  time.Minute,
		2:  2 * time.Minute,
		3:  4 * time.Minute,
		7:  time.Hour, // capped
		20: time.Hour, // still capped, no overflow
	}
	for attempt, want := range cases {
		if got := r.backoff(attempt); got != want {
			t.Fatalf("backoff(%d): got %s want %s", attempt, got, want)
		}
	}
}

// --- backoff overflow guard: a near-MaxInt64 cap with many attempts must not
// wrap to a negative (past) delay. ---------------------------------------------

func TestBackoffNeverNegativeOnOverflow(t *testing.T) {
	r := New(Options{
		Root:        t.TempDir(),
		BackoffBase: time.Minute,
		BackoffCap:  time.Duration(math.MaxInt64),
		MaxAttempts: 100,
	})
	for attempt := 1; attempt <= 100; attempt++ {
		got := r.backoff(attempt)
		if got <= 0 {
			t.Fatalf("backoff(%d) = %s: must never be <= 0 (would set NextAttemptAt in "+
				"the past and hot-loop retries)", attempt, got)
		}
	}
}

// --- successful run reaches done -------------------------------------------

func TestSuccessfulRunReachesDone(t *testing.T) {
	clock := newFakeClock()
	r := testRunner(t, clock)
	ran := make(chan struct{}, 1)
	_ = r.Register("ok", func(_ context.Context, task *Task) error {
		if task.CommitGuard == nil {
			t.Errorf("executor received nil CommitGuard")
		}
		if task.Subject != "s" {
			t.Errorf("subject got %q want s", task.Subject)
		}
		ran <- struct{}{}
		return nil
	})
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()

	id, err := r.Enqueue("ok", "s", EnqueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-ran:
	case <-time.After(3 * time.Second):
		t.Fatal("executor never ran")
	}
	waitFor(t, "task to reach done", func() bool {
		task, _ := r.Get(id.ID)
		return task != nil && task.State == StateDone && task.Attempts == 1
	})
}

// --- orphan-running recovery after a simulated crash -----------------------

func TestOrphanRunningRecoveredOnStart(t *testing.T) {
	clock := newFakeClock()
	root := t.TempDir()

	// Simulate a crash: pre-seed a record stuck in running directly on disk, as if
	// the daemon died mid-run.
	s := newStore(root)
	if err := s.init(); err != nil {
		t.Fatal(err)
	}
	orphan := &Task{
		ID:            TaskID("compact_context", "ws-1"),
		Kind:          "compact_context",
		Subject:       "ws-1",
		State:         StateRunning,
		Attempts:      1,
		NextAttemptAt: clock.now().Add(time.Hour), // stale future schedule from the crashed run
		CreatedAt:     clock.now(),
		UpdatedAt:     clock.now(),
	}
	if err := s.save(orphan); err != nil {
		t.Fatal(err)
	}

	ran := make(chan struct{}, 1)
	r := New(Options{
		Root:         root,
		Now:          clock.now,
		PollInterval: 2 * time.Millisecond,
		Log:          func(string, ...interface{}) {},
	})
	_ = r.Register("compact_context", func(context.Context, *Task) error {
		ran <- struct{}{}
		return nil
	})
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()

	// Recovery resets running -> queued with NextAttemptAt = now, so the worker
	// re-runs the orphan immediately (it must NOT honor the stale future schedule).
	select {
	case <-ran:
	case <-time.After(3 * time.Second):
		t.Fatal("orphan running task was not recovered and re-run")
	}
	waitFor(t, "recovered orphan to reach done", func() bool {
		task, _ := r.Get(orphan.ID)
		return task != nil && task.State == StateDone
	})
}

// --- Cancel blocks until the executor goroutine exits ----------------------

func TestCancelBlocksUntilExecutorExits(t *testing.T) {
	clock := newFakeClock()
	r := testRunner(t, clock)

	entered := make(chan struct{}) // executor has started
	release := make(chan struct{}) // test lets the executor proceed
	var exited int32               // set just before the executor returns

	_ = r.Register("blocking", func(ctx context.Context, _ *Task) error {
		close(entered)
		<-release // block until the test releases us (NOT a sleep — a sync point)
		atomic.StoreInt32(&exited, 1)
		return nil
	})
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()

	if _, err := r.Enqueue("blocking", "s", EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	<-entered // executor is now running and parked on release

	cancelReturned := make(chan struct{})
	go func() {
		r.Cancel(TaskID("blocking", "s"))
		close(cancelReturned)
	}()

	// Cancel MUST NOT have returned while the executor goroutine is still alive.
	select {
	case <-cancelReturned:
		t.Fatal("Cancel returned before the executor goroutine exited")
	case <-time.After(50 * time.Millisecond):
		// expected: still blocked
	}
	if atomic.LoadInt32(&exited) != 0 {
		t.Fatal("executor reported exited while still parked")
	}

	// Release the executor; now Cancel must return promptly, and only after exit.
	close(release)
	select {
	case <-cancelReturned:
	case <-time.After(3 * time.Second):
		t.Fatal("Cancel did not return after the executor exited")
	}
	if atomic.LoadInt32(&exited) != 1 {
		t.Fatal("Cancel returned but executor had not set its exit flag")
	}
}

// --- commit fence: Cancel during commit does not tear the durable write ----

func TestCommitFenceProtectsDurableWrite(t *testing.T) {
	clock := newFakeClock()
	r := testRunner(t, clock)

	committing := make(chan struct{}) // executor entered its commit latch
	proceed := make(chan struct{})    // test lets the executor finish its write
	const committedContent = "committed-untorn"
	artifact := filepath.Join(t.TempDir(), "durable.txt")

	_ = r.Register("committer", func(ctx context.Context, task *Task) error {
		// Cancellable work would honor ctx.Done() here; we go straight to commit.
		if !task.CommitGuard.Enter() {
			// Fenced before committing — for THIS test that is a failure, because
			// Cancel arrives only after we are already inside the latch.
			return context.Canceled
		}
		defer task.CommitGuard.Leave()

		close(committing)
		<-proceed // hold the latch open while Cancel races us

		// The durable write the fence must protect. Even though a Cancel fired, the
		// fence held the context (it must NOT be cancelled), so this real on-disk
		// write completes untorn. We assert both the content and that ctx is live.
		if ctx.Err() != nil {
			return ctx.Err() // would mean the fence failed to hold the context
		}
		return os.WriteFile(artifact, []byte(committedContent), 0o600)
	})
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()

	id := TaskID("committer", "s")
	if _, err := r.Enqueue("committer", "s", EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	<-committing // executor is inside the commit latch

	cancelReturned := make(chan struct{})
	go func() {
		r.Cancel(id)
		close(cancelReturned)
	}()

	// Cancel must block (the run is committing) and must NOT cancel the ctx.
	select {
	case <-cancelReturned:
		t.Fatal("Cancel returned while the committing executor was still parked")
	case <-time.After(50 * time.Millisecond):
	}

	// Let the executor finish its durable write.
	close(proceed)
	select {
	case <-cancelReturned:
	case <-time.After(3 * time.Second):
		t.Fatal("Cancel did not return after the committing run finished")
	}

	// The committed artifact must exist with the exact content — the write was not
	// torn by the racing Cancel.
	got, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatalf("committed artifact missing — the fence failed to protect the write: %v", err)
	}
	if string(got) != committedContent {
		t.Fatalf("committed artifact content got %q want %q", got, committedContent)
	}

	// And the run reached done cleanly (committed, not cancelled).
	waitFor(t, "committed run to land done", func() bool {
		task, _ := r.Get(id)
		return task != nil && task.State == StateDone
	})
	task := loadTask(t, r, id)
	if task.Attempts != 1 {
		t.Fatalf("committed run attempts got %d want 1", task.Attempts)
	}
}

// --- commit fence: Cancel BEFORE commit fences the run cleanly -------------

func TestCancelBeforeCommitFencesCleanly(t *testing.T) {
	clock := newFakeClock()
	r := testRunner(t, clock)

	entered := make(chan struct{})
	gotFenced := make(chan bool, 1)

	_ = r.Register("preempt", func(ctx context.Context, task *Task) error {
		close(entered)
		<-ctx.Done() // wait until Cancel cancels us (a sync point, not a sleep)
		// Now attempt to enter the commit latch: it must report "fenced" (false),
		// telling the executor to abandon its durable write.
		ok := task.CommitGuard.Enter()
		gotFenced <- !ok
		if !ok {
			return ctx.Err()
		}
		task.CommitGuard.Leave()
		return nil
	})
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()

	id := TaskID("preempt", "s")
	if _, err := r.Enqueue("preempt", "s", EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	<-entered

	r.Cancel(id) // blocks until exit; cancels the ctx because we are pre-commit

	select {
	case fenced := <-gotFenced:
		if !fenced {
			t.Fatal("Enter should have reported the run fenced after a pre-commit Cancel")
		}
	default:
		t.Fatal("executor never reached its Enter() check")
	}
}

// --- re-enqueue while running coalesces into one record and re-runs --------

func TestReenqueueWhileRunningReRunsAndDoesNotDuplicate(t *testing.T) {
	clock := newFakeClock()
	r := testRunner(t, clock)

	// The first run parks until released; later runs return immediately. Counting
	// runs proves the mid-run re-enqueue caused a second execution (the Requeued
	// trigger was honored, not lost).
	runs := make(chan int, 8)
	var runCount int64
	firstEntered := make(chan struct{})
	release := make(chan struct{})
	_ = r.Register("long", func(ctx context.Context, _ *Task) error {
		n := atomic.AddInt64(&runCount, 1)
		runs <- int(n)
		if n == 1 {
			close(firstEntered)
			<-release // hold the first run open so we can re-enqueue mid-run
		}
		return nil
	})
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()

	if _, err := r.Enqueue("long", "s", EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	<-firstEntered // first run is now parked

	// Re-enqueue while running: must not duplicate the record, must record demand.
	if _, err := r.Enqueue("long", "s", EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	all, _ := r.List()
	if len(all) != 1 {
		t.Fatalf("re-enqueue while running duplicated the record: got %d", len(all))
	}
	mid := loadTask(t, r, TaskID("long", "s"))
	if !mid.Requeued {
		t.Fatal("re-enqueue while running should set Requeued so the trigger is not lost")
	}

	// Release the first run; the worker must honor Requeued by re-running once more.
	close(release)
	if got := <-runs; got != 1 {
		t.Fatalf("first run number: got %d want 1", got)
	}
	if got := <-runs; got != 2 {
		t.Fatalf("the coalesced mid-run re-enqueue should trigger exactly one re-run; got run #%d", got)
	}
	waitFor(t, "re-run to land done", func() bool {
		task, _ := r.Get(TaskID("long", "s"))
		return task != nil && task.State == StateDone && !task.Requeued
	})
	if got := atomic.LoadInt64(&runCount); got != 2 {
		t.Fatalf("executor ran %d times, want exactly 2 (one original + one coalesced re-run)", got)
	}
}

// --- concurrency stress: Enqueue/Retry/Cancel against a live worker --------

// This test exists to drive the race detector across the runner's ioMu/mu
// coordination under real contention; it asserts only that the runner survives
// the storm with a coherent record (no panic, no torn JSON), not exact ordering.
func TestConcurrentEnqueueRetryCancelUnderRace(t *testing.T) {
	clock := newFakeClock()
	root := t.TempDir()
	r := New(Options{
		Root:         root,
		Now:          clock.now,
		PollInterval: time.Millisecond,
		MaxAttempts:  3,
		BackoffBase:  time.Millisecond,
		BackoffCap:   time.Millisecond,
		Log:          func(string, ...interface{}) {},
	})
	var fail atomic.Bool
	_ = r.Register("storm", func(ctx context.Context, _ *Task) error {
		if fail.Load() {
			return context.DeadlineExceeded
		}
		return nil
	})
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()

	id := TaskID("storm", "s")
	var wg sync.WaitGroup
	for g := 0; g < 6; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 80; i++ {
				switch (g + i) % 4 {
				case 0:
					_, _ = r.Enqueue("storm", "s", EnqueueOptions{})
				case 1:
					_, _ = r.Enqueue("storm", "s", EnqueueOptions{ZeroDebounce: true})
				case 2:
					_, _ = r.Retry(id)
				case 3:
					r.Cancel(id)
				}
				if i%10 == 0 {
					fail.Store(!fail.Load())
				}
			}
		}(g)
	}
	wg.Wait()

	// The runner must still produce a readable, coherent record.
	fail.Store(false)
	_, _ = r.Enqueue("storm", "s", EnqueueOptions{ZeroDebounce: true})
	waitFor(t, "the stormed task to settle into a terminal-or-pending state", func() bool {
		task, _ := r.Get(id)
		if task == nil {
			return false
		}
		switch task.State {
		case StateQueued, StateRunning, StateFailed, StateDone, StateDead:
			return task.ID == id
		default:
			return false
		}
	})
	all, err := r.List()
	if err != nil {
		t.Fatalf("List after storm: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("storm should leave exactly one coalesced record, got %d", len(all))
	}
}

// --- manual Retry forces failed/dead back to queued at now -----------------

func TestRetryForcesFailedToQueued(t *testing.T) {
	clock := newFakeClock()
	root := t.TempDir()
	var shouldFail atomic.Bool
	shouldFail.Store(true)
	runs := make(chan struct{}, 8)
	r := New(Options{
		Root:         root,
		Now:          clock.now,
		PollInterval: 2 * time.Millisecond,
		MaxAttempts:  2,
		BackoffBase:  time.Hour, // huge backoff so it will NOT auto-requeue on its own
		BackoffCap:   time.Hour,
		Log:          func(string, ...interface{}) {},
	})
	_ = r.Register("retryable", func(context.Context, *Task) error {
		runs <- struct{}{}
		if shouldFail.Load() {
			return context.DeadlineExceeded
		}
		return nil
	})
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()

	id := TaskID("retryable", "s")
	if _, err := r.Enqueue("retryable", "s", EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	<-runs // first attempt runs and fails (backoff = 1h, won't auto-retry)
	waitFor(t, "task to land failed", func() bool {
		task, _ := r.Get(id)
		return task != nil && task.State == StateFailed
	})

	// Now make the executor succeed and manually Retry: it must flip to queued at
	// now and the worker must pick it up immediately despite the 1h backoff.
	shouldFail.Store(false)
	retried, err := r.Retry(id)
	if err != nil {
		t.Fatal(err)
	}
	if retried.State != StateQueued {
		t.Fatalf("Retry should set state queued, got %s", retried.State)
	}
	if !retried.NextAttemptAt.Equal(clock.now()) {
		t.Fatalf("Retry should set NextAttemptAt=now, got %s want %s", retried.NextAttemptAt, clock.now())
	}
	if retried.Attempts != 0 {
		t.Fatalf("Retry should reset attempts, got %d", retried.Attempts)
	}

	<-runs // second attempt runs because of the manual retry
	waitFor(t, "retried task to reach done", func() bool {
		task, _ := r.Get(id)
		return task != nil && task.State == StateDone
	})
}

func TestRetryOnDeadTask(t *testing.T) {
	clock := newFakeClock()
	r := testRunner(t, clock)
	_ = r.Register("k", func(context.Context, *Task) error { return nil })

	// Seed a dead record directly (no need to burn real attempts).
	dead := &Task{
		ID:            TaskID("k", "s"),
		Kind:          "k",
		Subject:       "s",
		State:         StateDead,
		Attempts:      5,
		LastError:     "boom",
		NextAttemptAt: clock.now(),
		CreatedAt:     clock.now(),
		UpdatedAt:     clock.now(),
	}
	if err := r.store.Save(dead); err != nil {
		t.Fatal(err)
	}
	got, err := r.Retry(dead.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateQueued {
		t.Fatalf("Retry on dead: state got %s want queued", got.State)
	}
	if got.LastError != "" {
		t.Fatalf("Retry should clear last_error, got %q", got.LastError)
	}
}

func TestRetryUnknownTaskReturnsNil(t *testing.T) {
	clock := newFakeClock()
	r := testRunner(t, clock)
	got, err := r.Retry("nope:x")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("Retry on missing task: got %+v want nil", got)
	}
}

// --- registry guards -------------------------------------------------------

func TestEnqueueUnknownKind(t *testing.T) {
	clock := newFakeClock()
	r := testRunner(t, clock)
	if _, err := r.Enqueue("never_registered", "s", EnqueueOptions{}); err == nil {
		t.Fatal("expected error enqueuing an unregistered kind")
	}
}

func TestRegisterRejectsDuplicateKind(t *testing.T) {
	clock := newFakeClock()
	r := testRunner(t, clock)
	fn := func(context.Context, *Task) error { return nil }
	if err := r.Register("k", fn); err != nil {
		t.Fatal(err)
	}
	if err := r.Register("k", fn); err == nil {
		t.Fatal("expected error registering a kind twice")
	}
}

// --- per-kind timeout is owned by the runner -------------------------------

func TestExecutorTimeoutIsRunnerOwned(t *testing.T) {
	clock := newFakeClock()
	root := t.TempDir()
	r := New(Options{
		Root:         root,
		Now:          clock.now,
		PollInterval: 2 * time.Millisecond,
		MaxAttempts:  1, // one shot -> dead so we observe the failure
		Log:          func(string, ...interface{}) {},
	})
	deadlineHit := make(chan struct{}, 1)
	if err := r.RegisterWithTimeout("slow", func(ctx context.Context, _ *Task) error {
		<-ctx.Done() // the runner's WithTimeout must fire
		deadlineHit <- struct{}{}
		return ctx.Err()
	}, 15*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()

	if _, err := r.Enqueue("slow", "s", EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-deadlineHit:
	case <-time.After(3 * time.Second):
		t.Fatal("runner-owned context.WithTimeout never cancelled the executor")
	}
	waitFor(t, "timed-out task to go dead (maxAttempts=1)", func() bool {
		task, _ := r.Get(TaskID("slow", "s"))
		return task != nil && task.State == StateDead
	})
}

// --- Hole 1: Cancel must not return until finish() has written the terminal
// record. The old code closed runDone before finish(), so Cancel could return
// while the on-disk record still read StateRunning. ----------------------------

func TestCancelReturnsOnlyAfterTerminalRecordWritten(t *testing.T) {
	clock := newFakeClock()
	r := testRunner(t, clock)

	entered := make(chan struct{})
	_ = r.Register("park", func(ctx context.Context, _ *Task) error {
		close(entered)
		<-ctx.Done() // pre-commit: Cancel will cancel the context
		return ctx.Err()
	})
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()

	id := TaskID("park", "s")
	if _, err := r.Enqueue("park", "s", EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	<-entered

	r.Cancel(id) // blocks until the goroutine fully exits

	// The instant Cancel returns, the durable record MUST already be terminal.
	// Before the fix it could still be StateRunning (finish() ran after Cancel
	// unblocked), breaking the "goroutine has fully exited" contract.
	task := loadTask(t, r, id)
	if task.State == StateRunning {
		t.Fatalf("Cancel returned while record still StateRunning; finish() had not "+
			"written the terminal state (got state=%s)", task.State)
	}
	if task.State != StateFailed && task.State != StateDead {
		t.Fatalf("after a pre-commit Cancel the record should be failed/dead, got %s", task.State)
	}
}

// --- Hole 1 (corollary): r.run is torn down and the terminal record is written
// only BEFORE Cancel returns, so a Retry issued right after Cancel always sees a
// terminal record. We loop the Cancel→Retry sequence many times: with Hole 1
// present, at least one iteration's Retry wins the ioMu race against finish() and
// observes a still-StateRunning record (Retry then silently no-ops). With the fix
// every iteration observes a terminal record and Retry re-queues. ------------

func TestCancelThenRetrySeesTerminalRecordUnderRace(t *testing.T) {
	clock := newFakeClock()
	r := testRunner(t, clock)

	parkUntilCancelled := make(chan struct{}, 1)
	entered := make(chan struct{}, 1)
	_ = r.Register("park", func(ctx context.Context, _ *Task) error {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-parkUntilCancelled:
			// Make this run park (and fail on cancel) only when the test asks.
			<-ctx.Done()
			return ctx.Err()
		default:
			return nil
		}
	})
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()

	id := TaskID("park", "s")
	for iter := 0; iter < 40; iter++ {
		// Arm one parked-then-cancelled run.
		parkUntilCancelled <- struct{}{}
		// Drain any stale "entered" signal from a previous fast run.
		select {
		case <-entered:
		default:
		}
		if _, err := r.Enqueue("park", "s", EnqueueOptions{ZeroDebounce: true}); err != nil {
			t.Fatal(err)
		}
		<-entered // this run is parked on ctx.Done()

		r.Cancel(id) // with the fix, returns only after the terminal record is written

		// Immediately Retry. The record MUST be terminal (failed/dead) here, so Retry
		// re-queues it rather than no-opping against a stale StateRunning record.
		got, err := r.Retry(id)
		if err != nil {
			t.Fatal(err)
		}
		if got == nil {
			t.Fatalf("iter %d: Retry after Cancel returned nil", iter)
		}
		if got.State == StateRunning {
			t.Fatalf("iter %d: Retry observed StateRunning after Cancel returned — "+
				"finish() had not written the terminal record (Hole 1)", iter)
		}
		if got.State != StateQueued {
			t.Fatalf("iter %d: Retry after Cancel should re-queue the terminal task, "+
				"got state=%s", iter, got.State)
		}
		// Let the now-queued run complete fast (parkUntilCancelled is empty) so the
		// next iteration starts clean.
		waitFor(t, "re-queued run to settle", func() bool {
			task, _ := r.Get(id)
			return task != nil && task.State == StateDone
		})
		select {
		case <-entered:
		default:
		}
	}
}

// --- Holes 2 & 5: a mid-run ZeroDebounce re-enqueue on a run that then FAILS
// must be honored as a run-now re-queue, not demoted to a backoff delay. ------

func TestFailedRunHonorsMidRunZeroDebounceRequeue(t *testing.T) {
	clock := newFakeClock()
	root := t.TempDir()
	r := New(Options{
		Root:         root,
		Now:          clock.now,
		PollInterval: 2 * time.Millisecond,
		MaxAttempts:  5,
		BackoffBase:  time.Minute, // a backoff that would clearly NOT be "run now"
		BackoffCap:   time.Hour,
		Log:          func(string, ...interface{}) {},
	})

	var shouldFail atomic.Bool
	shouldFail.Store(true)
	firstEntered := make(chan struct{})
	release := make(chan struct{})
	runs := make(chan struct{}, 8)
	_ = r.Register("compact_context", func(ctx context.Context, _ *Task) error {
		runs <- struct{}{}
		if shouldFail.Load() {
			close(firstEntered)
			<-release // hold the run open so we can inject the mid-run demand
			return context.DeadlineExceeded
		}
		return nil
	})
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()

	id := TaskID("compact_context", "ws-1")
	if _, err := r.Enqueue("compact_context", "ws-1", EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	<-runs         // first run started
	<-firstEntered // parked, about to fail when released

	// Inject the removal-boundary final task while the run is in flight: record is
	// StateRunning, so this sets Requeued=true and NextAttemptAt=now (run NOW).
	if _, err := r.Enqueue("compact_context", "ws-1", EnqueueOptions{ZeroDebounce: true}); err != nil {
		t.Fatal(err)
	}
	mid := loadTask(t, r, id)
	if !mid.Requeued || !mid.NextAttemptAt.Equal(clock.now()) {
		t.Fatalf("mid-run ZeroDebounce should set Requeued + NextAttemptAt=now, got "+
			"requeued=%v next=%s", mid.Requeued, mid.NextAttemptAt)
	}

	// Release: the first run FAILS. The pre-fix code took the failure branch,
	// ignored Requeued, and overwrote NextAttemptAt with now+1m backoff — demoting
	// the run-now demand. The fix re-queues at the requested NextAttemptAt=now.
	shouldFail.Store(false)
	close(release)

	// The task must re-run immediately (run #2 succeeds), NOT sit on a 1m backoff.
	select {
	case <-runs:
	case <-time.After(3 * time.Second):
		settled, _ := r.Get(id)
		t.Fatalf("the mid-run ZeroDebounce demand was dropped: the task did not re-run "+
			"immediately after the failed run (settled=%+v)", settled)
	}
	waitFor(t, "the re-queued run-now task to land done", func() bool {
		task, _ := r.Get(id)
		return task != nil && task.State == StateDone && !task.Requeued
	})
}

// --- Hole 3: concurrent Stop() must not double-close r.done and panic. -------

func TestConcurrentStopDoesNotPanic(t *testing.T) {
	clock := newFakeClock()
	r := testRunner(t, clock)
	_ = r.Register("noop", func(context.Context, *Task) error { return nil })
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}

	const n = 8
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Stop() // pre-fix: two concurrent Stops -> close of closed channel panic
		}()
	}
	wg.Wait()
}

// --- Hole 4: the runner-owned timeout must NOT tear a durable write that is
// already inside its commit fence. The deadline must honor the same fence that
// Cancel/Stop honor. -----------------------------------------------------------

func TestRunnerTimeoutDoesNotTearCommit(t *testing.T) {
	clock := newFakeClock()
	root := t.TempDir()
	r := New(Options{
		Root:         root,
		Now:          clock.now,
		PollInterval: 2 * time.Millisecond,
		MaxAttempts:  1,
		Log:          func(string, ...interface{}) {},
	})

	const committedContent = "untorn-by-timeout"
	artifact := filepath.Join(t.TempDir(), "durable.txt")
	committing := make(chan struct{})
	ctxErrInsideCommit := make(chan error, 1)

	// 20ms timeout; the executor enters its commit, then deliberately spends longer
	// than the remaining deadline performing its write. The deadline fires WHILE
	// committing — it must not cancel the context.
	if err := r.RegisterWithTimeout("committer", func(ctx context.Context, task *Task) error {
		if !task.CommitGuard.Enter() {
			return context.Canceled
		}
		defer task.CommitGuard.Leave()
		close(committing)

		// Outlast the deadline inside the fence.
		select {
		case <-time.After(120 * time.Millisecond):
		case <-ctx.Done():
			// If the fence works, this branch must NOT be taken by the deadline.
		}
		if ctx.Err() != nil {
			ctxErrInsideCommit <- ctx.Err()
			return ctx.Err()
		}
		// Perform the durable write BEFORE signaling completion, so the test reads a
		// settled artifact and never races the write.
		werr := os.WriteFile(artifact, []byte(committedContent), 0o600)
		ctxErrInsideCommit <- nil
		return werr
	}, 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()

	id := TaskID("committer", "s")
	if _, err := r.Enqueue("committer", "s", EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	<-committing

	select {
	case err := <-ctxErrInsideCommit:
		if err != nil {
			t.Fatalf("ctx was cancelled INSIDE the commit fence by the runner-owned "+
				"timeout: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("executor never finished its commit")
	}

	got, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatalf("committed artifact missing — the timeout tore the write: %v", err)
	}
	if string(got) != committedContent {
		t.Fatalf("committed artifact content got %q want %q", got, committedContent)
	}
	waitFor(t, "committed run to land done", func() bool {
		task, _ := r.Get(id)
		return task != nil && task.State == StateDone
	})
}

// --- Hole 6: one undecodable .json record must not wedge the whole worker. ----

func TestPoisonRecordDoesNotWedgeWorker(t *testing.T) {
	clock := newFakeClock()
	r := testRunner(t, clock)

	ran := make(chan struct{}, 1)
	_ = r.Register("compact_context", func(context.Context, *Task) error {
		ran <- struct{}{}
		return nil
	})

	// Place a corrupt record that sorts BEFORE the valid one (ReadDir is sorted),
	// so list() visits it first. The old code returned on the parse error and the
	// worker never reached the valid task.
	if err := r.store.Init(); err != nil {
		t.Fatal(err)
	}
	poison := filepath.Join(stateDir(storeRoot(t, r)), "aaa_corrupt.json")
	if err := os.WriteFile(poison, []byte("{ this is not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()

	if _, err := r.Enqueue("compact_context", "ws-1", EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}

	select {
	case <-ran:
	case <-time.After(3 * time.Second):
		t.Fatal("WEDGED: a single corrupt record prevented a valid eligible task from " +
			"ever running")
	}
	waitFor(t, "valid task to land done despite the poison record", func() bool {
		task, _ := r.Get(TaskID("compact_context", "ws-1"))
		return task != nil && task.State == StateDone
	})
	// The poison file must still be on disk (we skip, not delete it).
	if _, err := os.Stat(poison); err != nil {
		t.Fatalf("poison record should be left in place (skipped, not deleted): %v", err)
	}
}

// --- Hole 7: a second Runner on the same root must be refused, not allowed to
// double-execute. --------------------------------------------------------------

func TestSecondRunnerOnSameRootIsRefused(t *testing.T) {
	clock := newFakeClock()
	root := t.TempDir()
	mk := func() *Runner {
		r := New(Options{
			Root:         root,
			Now:          clock.now,
			PollInterval: 2 * time.Millisecond,
			Log:          func(string, ...interface{}) {},
		})
		_ = r.Register("k", func(context.Context, *Task) error { return nil })
		return r
	}

	first := mk()
	if err := first.Start(); err != nil {
		t.Fatalf("first Start should succeed: %v", err)
	}

	second := mk()
	if err := second.Start(); err == nil {
		second.Stop()
		t.Fatal("second Runner on the same root should be refused (double-execution guard)")
	} else if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second Start error got %v want ErrAlreadyRunning", err)
	}

	// After the first stops and releases its lock, a fresh Runner can claim the root.
	first.Stop()
	third := mk()
	if err := third.Start(); err != nil {
		t.Fatalf("third Start after first released the lock should succeed: %v", err)
	}
	third.Stop()
}

// TestRemoveDeletesQueuedRecord proves Remove deletes a queued task's record (the
// orphan-leak case: Cancel alone is a no-op for a queued task and never deletes
// the record). The worker is never started, so the task stays queued.
func TestRemoveDeletesQueuedRecord(t *testing.T) {
	clock := newFakeClock()
	r := testRunner(t, clock)
	if err := r.Register("k", func(context.Context, *Task) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Enqueue("k", "ws-1", EnqueueOptions{Debounce: time.Hour}); err != nil {
		t.Fatal(err)
	}
	id := TaskID("k", "ws-1")
	if got, _ := r.Get(id); got == nil {
		t.Fatal("precondition: queued record should exist before Remove")
	}

	r.Remove(id)

	got, err := r.Get(id)
	if err != nil {
		t.Fatalf("Get after Remove: %v", err)
	}
	if got != nil {
		t.Fatalf("Remove left a queued record behind: %+v", got)
	}
}

// TestRemoveCancelsRunningThenDeletes proves Remove of a running task blocks until
// the executor goroutine exits (it wraps Cancel) and then deletes the record.
func TestRemoveCancelsRunningThenDeletes(t *testing.T) {
	clock := newFakeClock()
	r := testRunner(t, clock)

	entered := make(chan struct{})
	release := make(chan struct{})
	var exited int32

	if err := r.Register("blocking", func(ctx context.Context, _ *Task) error {
		close(entered)
		<-release
		atomic.StoreInt32(&exited, 1)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()

	if _, err := r.Enqueue("blocking", "ws-1", EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	<-entered // running and parked

	id := TaskID("blocking", "ws-1")
	removeReturned := make(chan struct{})
	go func() {
		r.Remove(id)
		close(removeReturned)
	}()

	select {
	case <-removeReturned:
		t.Fatal("Remove returned before the running executor exited")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	select {
	case <-removeReturned:
	case <-time.After(3 * time.Second):
		t.Fatal("Remove did not return after the executor exited")
	}
	if atomic.LoadInt32(&exited) != 1 {
		t.Fatal("Remove returned but executor had not exited")
	}

	got, err := r.Get(id)
	if err != nil {
		t.Fatalf("Get after Remove: %v", err)
	}
	if got != nil {
		t.Fatalf("Remove left the record behind after cancelling the run: %+v", got)
	}
}

// --- Meta: carried run inputs survive save/load and re-enqueue correctly ------

// TestMetaRoundTripsThroughSaveAndLoad proves Meta persists to disk and reloads
// intact. summarize_session depends on this: the carried transcript path and
// workspace id must survive a daemon crash between enqueue and the debounced run.
func TestMetaRoundTripsThroughSaveAndLoad(t *testing.T) {
	clock := newFakeClock()
	r := testRunner(t, clock)
	_ = r.Register("summarize_session", func(context.Context, *Task) error { return nil })

	meta := map[string]string{"transcript": "/home/u/.claude/t.jsonl", "workspace": "ws-9"}
	if _, err := r.Enqueue("summarize_session", "session-1", EnqueueOptions{Meta: meta}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// Reload from disk (Get -> store.load -> json.Unmarshal), bypassing the in-call
	// clone, so this asserts the on-disk JSON carried Meta, not just an in-memory copy.
	got := loadTask(t, r, TaskID("summarize_session", "session-1"))
	if got.Meta["transcript"] != "/home/u/.claude/t.jsonl" || got.Meta["workspace"] != "ws-9" {
		t.Fatalf("Meta did not round-trip through save/load: %+v", got.Meta)
	}
}

// TestEnqueueMetaReplaceAndPreserve proves the Meta application rules: a non-nil
// Meta REPLACES on re-enqueue (fresh inputs win), and a nil Meta PRESERVES an
// existing Meta (a bare re-trigger must not wipe inputs a prior enqueue stashed).
func TestEnqueueMetaReplaceAndPreserve(t *testing.T) {
	clock := newFakeClock()
	r := testRunner(t, clock)
	_ = r.Register("summarize_session", func(context.Context, *Task) error { return nil })

	// First enqueue stashes Meta.
	if _, err := r.Enqueue("summarize_session", "s", EnqueueOptions{Meta: map[string]string{"transcript": "/a.jsonl", "workspace": "ws-1"}}); err != nil {
		t.Fatalf("first enqueue: %v", err)
	}

	// Re-enqueue WITHOUT Meta: the prior Meta must survive untouched.
	if _, err := r.Enqueue("summarize_session", "s", EnqueueOptions{Debounce: time.Minute}); err != nil {
		t.Fatalf("bare re-enqueue: %v", err)
	}
	got := loadTask(t, r, TaskID("summarize_session", "s"))
	if got.Meta["transcript"] != "/a.jsonl" || got.Meta["workspace"] != "ws-1" {
		t.Fatalf("bare re-enqueue wiped Meta: %+v", got.Meta)
	}

	// Re-enqueue WITH fresh Meta: it replaces the prior Meta entirely.
	if _, err := r.Enqueue("summarize_session", "s", EnqueueOptions{Meta: map[string]string{"transcript": "/b.jsonl", "workspace": "ws-2"}}); err != nil {
		t.Fatalf("re-enqueue with fresh Meta: %v", err)
	}
	got = loadTask(t, r, TaskID("summarize_session", "s"))
	if got.Meta["transcript"] != "/b.jsonl" || got.Meta["workspace"] != "ws-2" {
		t.Fatalf("re-enqueue with Meta did not replace: %+v", got.Meta)
	}
}

// TestCloneReturnsIsolatedMeta proves clone() deep-copies Meta: mutating the
// returned task's map does not reach back into the record the runner returned (and
// vice-versa). Without the deep copy a caller holding an Enqueue/Get result would
// race-mutate the worker's underlying map.
func TestCloneReturnsIsolatedMeta(t *testing.T) {
	clock := newFakeClock()
	r := testRunner(t, clock)
	_ = r.Register("summarize_session", func(context.Context, *Task) error { return nil })

	first, err := r.Enqueue("summarize_session", "s", EnqueueOptions{Meta: map[string]string{"workspace": "ws-1"}})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	// Mutating the returned clone must not affect the stored record.
	first.Meta["workspace"] = "tampered"
	if got := loadTask(t, r, TaskID("summarize_session", "s")); got.Meta["workspace"] != "ws-1" {
		t.Fatalf("mutating a clone's Meta leaked into the stored record: %+v", got.Meta)
	}

	// And two separate reads return independent maps.
	a := loadTask(t, r, TaskID("summarize_session", "s"))
	b := loadTask(t, r, TaskID("summarize_session", "s"))
	a.Meta["workspace"] = "x"
	if b.Meta["workspace"] != "ws-1" {
		t.Fatalf("two reads shared a Meta map: a=%+v b=%+v", a.Meta, b.Meta)
	}
}
