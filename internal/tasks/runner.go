package tasks

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Tuning defaults. Each is overridable per-runner through the matching Options
// field (a zero value falls back to the default, resolved in New).
const (
	// DefaultMaxAttempts is the attempt cap: a task that has failed this many
	// times goes dead instead of auto-requeuing.
	DefaultMaxAttempts = 5
	// DefaultBackoffBase / DefaultBackoffCap bound the capped-exponential backoff
	// schedule (1m, 2m, 4m, … capped at 1h).
	DefaultBackoffBase = time.Minute
	DefaultBackoffCap  = time.Hour
	// DefaultExecutorTimeout is the per-executor context.WithTimeout the runner
	// wraps around every invocation. Keeper compaction uses 5 minutes; a kind can
	// override it via RegisterWithTimeout.
	DefaultExecutorTimeout = 5 * time.Minute
	// defaultPollInterval is how often the dispatch loop wakes to re-scan the queue
	// for a task whose NextAttemptAt has arrived. The loop is level-triggered, so
	// this only bounds scheduling latency for time-gated requeues.
	defaultPollInterval = time.Second
)

// ErrDisabled is returned by mutating runner methods when the runner was
// constructed without a notebook root. A disabled runner never starts a worker
// and persists nothing; the daemon consumer degrades to an inline fallback.
var ErrDisabled = errors.New("tasks: runner is disabled (no notebook root)")

// ErrUnknownKind is returned by Enqueue when no executor is registered for the
// task's kind.
var ErrUnknownKind = errors.New("tasks: no executor registered for kind")

// ExecutorFunc runs one task to completion. It returns nil on success (the task
// goes done) or an error (the task goes failed and may auto-requeue). The runner
// owns the context.WithTimeout wrapper around the invocation; the executor body
// owns the work and calls task.CommitGuard.Enter/Leave around its single durable
// write. Modeled on the keeper compaction executor in
// internal/daemon/workspace_keeper.go.
type ExecutorFunc func(ctx context.Context, task *Task) error

// executor pairs a registered ExecutorFunc with its per-kind timeout and
// concurrency cap.
type executor struct {
	fn      ExecutorFunc
	timeout time.Duration
	// limit is the max number of tasks OF THIS KIND that may run at once. It is
	// always >= 1 after registration (a zero MaxConcurrent resolves to 1), so a
	// kind is serialized with itself by default while different kinds run in
	// parallel.
	limit int
}

// ExecutorConfig tunes a registered executor. The zero value is the default:
// DefaultExecutorTimeout and a per-kind concurrency cap of 1.
type ExecutorConfig struct {
	// Timeout is the per-invocation context.WithTimeout the runner wraps around
	// every call to this executor. Zero ⇒ DefaultExecutorTimeout.
	Timeout time.Duration
	// MaxConcurrent bounds how many tasks of this kind may run simultaneously.
	// Zero ⇒ 1 (a kind serialized with itself, the pre-concurrency default);
	// different kinds always run in parallel regardless. The reconcile kind uses 2.
	MaxConcurrent int
}

// EnqueueOptions tunes a single Enqueue call.
type EnqueueOptions struct {
	// Debounce is how far forward NextAttemptAt is pushed from now when the task is
	// (re-)enqueued. Re-enqueueing within the window keeps pushing the run later —
	// this is the coalescing knob (collapse N triggers into one run). Zero means
	// "run as soon as eligible" (now), which is how the removal-boundary final task
	// overrides a pending debounce.
	Debounce time.Duration
	// ZeroDebounce, when true, forces NextAttemptAt = now regardless of Debounce,
	// overriding any pending forward-pushed debounce already on the record. This is
	// the explicit override the removal-boundary final task uses.
	ZeroDebounce bool
	// Meta carries kind-specific run inputs onto the durable record (see
	// Task.Meta). A non-nil Meta REPLACES the record's Meta on this Enqueue (a
	// re-enqueue with fresh inputs updates them); a nil Meta LEAVES an existing
	// Meta untouched (a bare re-trigger that carries no inputs must not wipe the
	// ones a prior enqueue stashed). Most callers leave this nil.
	Meta map[string]string
}

// Options configures a Runner at construction.
type Options struct {
	// Store injects the persistence backend. When set it takes precedence over Root
	// and the runner is enabled. Production passes a SQLite-backed Store; tests and
	// the legacy on-disk path leave it nil and use Root.
	Store Store
	// Root is the notebook root dir. Used only when Store is nil: a non-empty root
	// selects the file-backed FileStore; empty/whitespace ⇒ the runner is disabled.
	Root string
	// Log receives runtime log lines. A nil Log is replaced with a no-op.
	Log LogFunc
	// Now injects the clock for deterministic backoff/coalescing tests. nil ⇒
	// time.Now. The runner always normalizes to UTC.
	Now func() time.Time
	// PollInterval overrides the worker's queue re-scan interval (tests use a
	// short interval to avoid real-time waits). Zero ⇒ defaultPollInterval.
	PollInterval time.Duration
	// MaxAttempts overrides the attempt cap. Zero ⇒ DefaultMaxAttempts.
	MaxAttempts int
	// BackoffBase / BackoffCap override the backoff schedule. Zero ⇒ the defaults.
	BackoffBase time.Duration
	BackoffCap  time.Duration
}

// Runner is the durable task runner. A single dispatch loop selects eligible
// tasks and launches each as its own goroutine, bounded by a per-kind
// concurrency cap (default 1): a kind is serialized with itself while different
// kinds run in parallel. Every record read-modify-write still funnels through
// ioMu, so persistence stays serialized even though executors run concurrently.
type Runner struct {
	store    Store
	log      LogFunc
	now      func() time.Time
	disabled bool

	pollInterval time.Duration
	maxAttempts  int
	backoffBase  time.Duration
	backoffCap   time.Duration

	// onChange, if set, fires after every lifecycle transition (for the status
	// broadcast). It may be called CONCURRENTLY — from the dispatch goroutine, from
	// each in-flight run's finish(), and from Enqueue/Retry/Remove on arbitrary
	// daemon goroutines — so it must be cheap, non-blocking, and safe to invoke from
	// multiple goroutines; the daemon's wiring just emits a websocket event.
	onChange func()

	mu        sync.Mutex
	executors map[string]executor
	started   bool

	// ioMu serializes every read-modify-write cycle on a task record. The dispatch
	// loop, the in-flight run goroutines' finish(), and arbitrary daemon goroutines
	// calling Enqueue/Retry all mutate the same records; without this a concurrent
	// Enqueue and a claim/finish write would lost-update each other. It is a coarse
	// store-level lock — correct and cheap because record I/O is fast and
	// contention is low. When both locks are needed, ioMu is ALWAYS the outer lock:
	// it is acquired without holding mu (dispatch takes ioMu, then briefly mu to
	// reserve a slot), so there is no lock-ordering hazard with mu.
	ioMu sync.Mutex

	// runs holds every in-flight run keyed by task id (the single-worker design had
	// one *activeRun). Cancel(id) fences and waits on its entry; Stop drains them
	// all. Guarded by mu.
	runs map[string]*activeRun

	// inflight counts currently-running tasks per kind. dispatch reads it to enforce
	// each kind's concurrency cap; it is bumped when a run is claimed and dropped
	// when the run goroutine exits. Guarded by mu.
	inflight map[string]int

	// wake nudges the worker to re-scan the queue immediately after an Enqueue or
	// Retry, rather than waiting for the next poll tick. Buffered depth 1 so a
	// nudge never blocks the caller.
	wake chan struct{}
	done chan struct{} // closed by Stop to tell the worker to exit
	exit chan struct{} // closed by the worker when it has fully exited

	// lockPath is the single-instance ownership marker acquired by Start and
	// released by Stop. Empty when not started. It enforces "at most one live
	// worker per root", which the orphan-recovery story silently assumes.
	lockPath string
}

// activeRun tracks one in-flight task so Cancel can fence its commit and block
// until its goroutine exits. Ported from the keeper compaction cancel/done/committing
// fields (workspace_keeper.go).
type activeRun struct {
	id     string
	kind   string // so the run goroutine can drop its per-kind in-flight slot on exit
	cancel context.CancelFunc
	guard  *CommitGuard
	done   chan struct{} // closed when the run goroutine has fully exited
}

// New constructs a Runner. With an empty root the runner is disabled: Enqueue,
// Retry, and Cancel are safe no-ops returning ErrDisabled (Cancel returns
// nothing), Start/Stop do nothing, and nothing is persisted. The daemon consumer
// then degrades to an inline in-process fallback.
func New(opts Options) *Runner {
	log := opts.Log
	if log == nil {
		log = func(string, ...interface{}) {}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	utcNow := func() time.Time { return now().UTC() }

	r := &Runner{
		log:          log,
		now:          utcNow,
		executors:    make(map[string]executor),
		runs:         make(map[string]*activeRun),
		inflight:     make(map[string]int),
		pollInterval: nonZeroDuration(opts.PollInterval, defaultPollInterval),
		maxAttempts:  nonZeroInt(opts.MaxAttempts, DefaultMaxAttempts),
		backoffBase:  nonZeroDuration(opts.BackoffBase, DefaultBackoffBase),
		backoffCap:   nonZeroDuration(opts.BackoffCap, DefaultBackoffCap),
		wake:         make(chan struct{}, 1),
	}
	if opts.Store != nil {
		r.store = opts.Store
		return r
	}
	root := trimRoot(opts.Root)
	if root == "" {
		r.disabled = true
		return r
	}
	r.store = NewFileStore(root, log)
	return r
}

// Disabled reports whether the runner is a no-op (no notebook root resolved).
func (r *Runner) Disabled() bool { return r.disabled }

// OnChange registers a callback fired after every lifecycle transition. It is
// optional (the status surface uses it) and must be cheap; pass nil to clear.
func (r *Runner) OnChange(fn func()) {
	r.mu.Lock()
	r.onChange = fn
	r.mu.Unlock()
}

// Register wires an executor for a kind with the default per-kind timeout and a
// concurrency cap of 1.
func (r *Runner) Register(kind string, fn ExecutorFunc) error {
	return r.RegisterWith(kind, fn, ExecutorConfig{})
}

// RegisterWithTimeout wires an executor for a kind with an explicit timeout (the
// runner wraps every invocation in context.WithTimeout of this duration) and a
// concurrency cap of 1.
func (r *Runner) RegisterWithTimeout(kind string, fn ExecutorFunc, timeout time.Duration) error {
	return r.RegisterWith(kind, fn, ExecutorConfig{Timeout: timeout})
}

// RegisterWith wires an executor for a kind with an explicit timeout and per-kind
// concurrency cap (see ExecutorConfig). It is an error to register the same kind
// twice or to pass a nil fn.
func (r *Runner) RegisterWith(kind string, fn ExecutorFunc, cfg ExecutorConfig) error {
	if r.disabled {
		return ErrDisabled
	}
	if fn == nil {
		return errors.New("tasks: executor must not be nil")
	}
	if kind == "" {
		return errors.New("tasks: kind must not be empty")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultExecutorTimeout
	}
	limit := cfg.MaxConcurrent
	if limit <= 0 {
		limit = 1
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.executors[kind]; exists {
		return fmt.Errorf("tasks: kind already registered: %s", kind)
	}
	r.executors[kind] = executor{fn: fn, timeout: timeout, limit: limit}
	return nil
}

// Start recovers orphaned running tasks (reset to queued) and launches the single
// dispatch goroutine. It is safe (no-op) on a disabled runner. Calling Start twice
// is an error.
func (r *Runner) Start() error {
	if r.disabled {
		return nil
	}
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return errors.New("tasks: runner already started")
	}
	if err := r.store.Init(); err != nil {
		r.mu.Unlock()
		return fmt.Errorf("tasks: init store: %w", err)
	}
	// Take exclusive ownership of the tasks dir before doing anything else: a
	// second live Runner on the same root would double-execute every task (its own
	// worker, its own in-memory CommitGuard). Acquiring the lock fails fast with
	// ErrAlreadyRunning rather than silently double-applying durable writes.
	lockPath, err := r.store.AcquireLock()
	if err != nil {
		r.mu.Unlock()
		return err
	}
	r.lockPath = lockPath
	r.started = true
	r.done = make(chan struct{})
	r.exit = make(chan struct{})
	r.mu.Unlock()

	// recoverOrphans is a read-modify-write over the store, so it must hold ioMu
	// like every other RMW path: an Enqueue/Retry racing Start could otherwise be
	// clobbered by recovery's stale in-memory copy (lost update). It runs before
	// the worker launches, so contention here is nil.
	r.ioMu.Lock()
	n, err := r.store.RecoverOrphans(r.now())
	r.ioMu.Unlock()
	if err != nil {
		r.log("tasks: recover orphan running tasks: %v", err)
	} else if n > 0 {
		r.log("tasks: recovered %d orphan running task(s)", n)
	}

	go r.loop()
	return nil
}

// Stop signals the dispatch loop to exit, then cancels and drains every in-flight
// run. cancelAll honors each commit fence, so an already-committing run still
// finishes its durable write untorn. Safe to call on a disabled or never-started
// runner.
func (r *Runner) Stop() {
	r.mu.Lock()
	if !r.started {
		r.mu.Unlock()
		return
	}
	// Flip started to false WHILE holding mu so a second concurrent Stop() sees
	// started==false and returns instead of double-close(done) (close of a closed
	// channel panics, which would crash the whole daemon). Only the Stop that wins
	// this transition closes done and waits on exit.
	r.started = false
	done, exit := r.done, r.exit
	lockPath := r.lockPath
	r.lockPath = ""
	r.mu.Unlock()

	// Order matters in the concurrent model: runs are detached goroutines the
	// dispatch loop does not join. First stop the loop launching MORE runs
	// (close done, wait for the loop to exit), THEN cancel and join everything
	// still in flight. Doing it in this order guarantees no new run can appear
	// while cancelAll drains, so it terminates.
	close(done)
	<-exit
	r.cancelAll()

	// Release single-instance ownership only after every run has exited, so no
	// other Runner can claim the store while ours is still draining.
	r.store.ReleaseLock(lockPath)
}

// Enqueue (re-)persists a task for kind+subject, coalescing onto the same record.
// A brand-new task is created queued; an existing queued/failed/dead record is
// reset to queued and its NextAttemptAt is recomputed from the options
// (forward-push debounce, or now when ZeroDebounce). A record that is currently
// running is left to finish — its in-flight run is not torn — but its
// NextAttemptAt is advanced so the worker re-runs it after the current pass if the
// caller asked for sooner work; see the in-flight handling below.
func (r *Runner) Enqueue(kind, subject string, opts EnqueueOptions) (*Task, error) {
	if r.disabled {
		return nil, ErrDisabled
	}
	if kind == "" || subject == "" {
		return nil, errors.New("tasks: kind and subject are required")
	}

	r.mu.Lock()
	_, known := r.executors[kind]
	r.mu.Unlock()
	if !known {
		return nil, fmt.Errorf("%w: %s", ErrUnknownKind, kind)
	}

	now := r.now()
	id := TaskID(kind, subject)
	next := now
	if !opts.ZeroDebounce && opts.Debounce > 0 {
		next = now.Add(opts.Debounce)
	}

	r.ioMu.Lock()
	defer r.ioMu.Unlock()

	existing, err := r.store.Load(id)
	if err != nil {
		return nil, err
	}

	var task *Task
	switch {
	case existing == nil:
		task = &Task{
			ID:            id,
			Kind:          kind,
			Subject:       subject,
			State:         StateQueued,
			Attempts:      0,
			NextAttemptAt: next,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
	case existing.State == StateRunning:
		// The task is running right now. Overwriting it would tear the in-flight
		// run's bookkeeping, so instead record the demand: mark Requeued and push
		// NextAttemptAt to the requested time. When the run finishes, the worker
		// sees Requeued and transitions to queued (re-run) instead of done, so this
		// coalesced trigger is honored rather than lost.
		existing.Requeued = true
		existing.NextAttemptAt = next
		existing.UpdatedAt = now
		task = existing
	default:
		// queued / failed / dead / done: coalesce by overwriting the same record
		// back to queued with a fresh schedule. Attempts reset because a new
		// enqueue is new logical demand, not a continuation of the old failure run.
		existing.State = StateQueued
		existing.Attempts = 0
		existing.LastError = ""
		existing.Requeued = false
		existing.NextAttemptAt = next
		existing.UpdatedAt = now
		task = existing
	}

	// Apply the carried Meta after the branch settled on `task`, so it lands the
	// same way on a brand-new record, a mid-run requeue (so the re-run reads the
	// fresh inputs), and a coalescing re-enqueue. A non-nil Meta REPLACES; a nil
	// Meta leaves any existing Meta intact (a bare re-trigger must not wipe inputs
	// a prior enqueue stashed). Store a fresh copy so the runner's record never
	// aliases the caller's map.
	if opts.Meta != nil {
		task.Meta = cloneStringMap(opts.Meta)
	}

	if err := r.store.Save(task); err != nil {
		return nil, err
	}
	r.notifyChange()
	r.nudge()
	return task.clone(), nil
}

// Retry forces a failed or dead task back to queued with NextAttemptAt = now. It
// is the manual-recovery action. A task that is queued/running/done is left as-is
// (retrying a non-terminal task is a no-op). Returns the updated task, or nil if
// no such task exists.
func (r *Runner) Retry(id string) (*Task, error) {
	if r.disabled {
		return nil, ErrDisabled
	}
	r.ioMu.Lock()
	defer r.ioMu.Unlock()

	existing, err := r.store.Load(id)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, nil
	}
	if existing.State != StateFailed && existing.State != StateDead {
		return existing.clone(), nil
	}
	now := r.now()
	existing.State = StateQueued
	existing.Attempts = 0
	existing.LastError = ""
	existing.Requeued = false
	existing.NextAttemptAt = now
	existing.UpdatedAt = now
	if err := r.store.Save(existing); err != nil {
		return nil, err
	}
	r.notifyChange()
	r.nudge()
	return existing.clone(), nil
}

// Cancel signals the running executor for id (if it is the one running) and DOES
// NOT RETURN until that task's goroutine has fully exited. If the run is already
// inside its commit fence, Cancel does not cancel the context — it waits for the
// goroutine to finish its durable write and exit, so the write is never torn.
// Cancel of a task that is not currently running returns immediately. Ported in
// spirit from the keeper compaction cancel path (workspace_keeper.go).
func (r *Runner) Cancel(id string) {
	if r.disabled {
		return
	}
	r.mu.Lock()
	run := r.runs[id]
	if run == nil {
		r.mu.Unlock()
		return
	}
	r.fenceAndWait(run)
}

// fenceAndWait performs the shared cancel-and-block core for Cancel and cancelActive.
// The CALLER must hold r.mu and pass the currently-running run (non-nil); fenceAndWait
// RELEASES r.mu before it blocks on the done channel. It cancels the run's context
// only if the run has not yet entered its commit fence — if the run is already
// committing, it leaves the context alone and just waits, so the blocks-until-exit
// contract finishes an in-progress durable write untorn.
func (r *Runner) fenceAndWait(run *activeRun) {
	if run.guard.tryFence() {
		run.cancel()
	}
	done := run.done
	r.mu.Unlock()
	<-done
}

// Remove forgets a task entirely: it cancels the run if that task is currently
// executing (blocking until the goroutine exits, honoring the commit fence) and
// then deletes the record file. It is the "the subject is gone" operation — e.g.
// a workspace was removed, so its compact_context task should leave nothing
// behind. Cancel alone is a no-op for a queued task and never deletes the
// record, so without Remove a removed subject would leak its record on disk
// forever. Safe (no-op) on a disabled runner.
//
// The delete runs under ioMu, serialized with every other record mutation. There
// is a narrow, data-safe window: between Cancel returning and the delete, the
// worker may claim a still-queued record and begin running it; the delete then
// removes the file mid-run and the run's finish() reloads, finds the record gone,
// and discards its result (a no-op). The executor's own durable write (if any) is
// guarded elsewhere by the store's optimistic-revision check, so a stale result
// cannot land. The window is tiny in practice (compaction is debounced minutes).
func (r *Runner) Remove(id string) {
	if r.disabled {
		return
	}
	r.Cancel(id)
	r.ioMu.Lock()
	err := r.store.Delete(id)
	r.ioMu.Unlock()
	if err != nil {
		r.log("tasks: remove %s: %v", id, err)
		return
	}
	r.notifyChange()
}

// List returns every persisted task, newest-updated first. Cheap os.ReadDir; safe
// (returns nil) on a disabled runner.
func (r *Runner) List() ([]*Task, error) {
	if r.disabled {
		return nil, nil
	}
	all, err := r.store.List()
	if err != nil {
		return nil, err
	}
	sort.SliceStable(all, func(i, j int) bool {
		return all[i].UpdatedAt.After(all[j].UpdatedAt)
	})
	return all, nil
}

// Get returns a single task by id, or nil if absent. Safe on a disabled runner.
func (r *Runner) Get(id string) (*Task, error) {
	if r.disabled {
		return nil, nil
	}
	return r.store.Load(id)
}

// --- dispatch loop ---------------------------------------------------------

// loop is the single dispatch goroutine. It is level-triggered: each pass claims
// and launches every currently-eligible task whose kind has a free concurrency
// slot (each as its own goroutine), draining until a pass can place nothing more,
// then waits for the next nudge or poll tick. Executors run concurrently; the loop
// itself never blocks on one.
func (r *Runner) loop() {
	defer close(r.exit)
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()
	for {
		// Drain: keep dispatching until a pass launches nothing (queue empty or
		// every eligible kind saturated), so a burst of enqueues does not stall
		// behind the poll interval.
		for {
			select {
			case <-r.done:
				return
			default:
			}
			progressed, err := r.dispatch()
			if err != nil {
				r.log("tasks: dispatch pass: %v", err)
				break
			}
			if !progressed {
				break
			}
		}
		select {
		case <-r.done:
			return
		case <-r.wake:
		case <-ticker.C:
		}
	}
}

// dispatch claims every currently-eligible task whose kind is under its per-kind
// concurrency cap and launches each in its own goroutine. It reports whether it
// made progress (launched a run, or failed an unknown-kind task in place) so the
// loop keeps draining until a pass can place nothing more. Selection order is
// earliest NextAttemptAt, then oldest CreatedAt, so under cap pressure the
// longest-waiting work claims the freed slot first.
//
// The store read and every per-task claim write run under ioMu, so a concurrent
// Enqueue/Retry cannot make a chosen record stale between selection and claim.
// The per-kind in-flight accounting and the active-run registry live under mu,
// which is only ever taken WHILE holding ioMu (never the reverse) — preserving the
// ioMu-outer lock order. Executors are launched only AFTER both locks are released,
// so an executor never runs while the runner holds a lock.
func (r *Runner) dispatch() (progressed bool, err error) {
	r.ioMu.Lock()

	now := r.now()
	all, err := r.store.List()
	if err != nil {
		r.ioMu.Unlock()
		return false, err
	}

	// Collect eligible tasks, earliest-scheduled first (ties: oldest created).
	eligible := make([]*Task, 0, len(all))
	for _, t := range all {
		if r.eligible(t, now) {
			eligible = append(eligible, t)
		}
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		if eligible[i].NextAttemptAt.Equal(eligible[j].NextAttemptAt) {
			return eligible[i].CreatedAt.Before(eligible[j].CreatedAt)
		}
		return eligible[i].NextAttemptAt.Before(eligible[j].NextAttemptAt)
	})

	type launchSpec struct {
		task *Task
		exec executor
		ctx  context.Context
		run  *activeRun
	}
	var launch []launchSpec
	failedUnknown := false

	for _, t := range eligible {
		// Decide + reserve under mu: the executor registry and in-flight counts both
		// live there. Reserving the slot before persisting the claim keeps a later
		// candidate of the same kind in this very pass from over-committing the cap.
		r.mu.Lock()
		exec, ok := r.executors[t.Kind]
		if !ok {
			r.mu.Unlock()
			// No executor for this kind (e.g. a stale record from an old build). Fail
			// it in place so it surfaces rather than being re-selected every pass.
			r.recordFailureLocked(t, fmt.Errorf("%w: %s", ErrUnknownKind, t.Kind))
			failedUnknown = true
			continue
		}
		if r.inflight[t.Kind] >= exec.limit {
			r.mu.Unlock()
			continue // this kind is saturated; a finishing run will re-nudge us
		}
		ctx, cancel := context.WithCancel(context.Background())
		run := &activeRun{
			id:     t.ID,
			kind:   t.Kind,
			cancel: cancel,
			guard:  &CommitGuard{},
			done:   make(chan struct{}),
		}
		r.inflight[t.Kind]++
		r.runs[t.ID] = run
		r.mu.Unlock()

		// Persist the claim (state running) under ioMu — NOT under mu, which must
		// never wrap store I/O.
		t.State = StateRunning
		t.Attempts++
		t.Requeued = false
		t.UpdatedAt = now
		if err := r.store.Save(t); err != nil {
			r.log("tasks: persist running state for %s: %v", t.ID, err)
			// Roll the reservation back so the per-kind slot is not leaked forever.
			r.mu.Lock()
			delete(r.runs, t.ID)
			r.inflight[t.Kind]--
			r.mu.Unlock()
			cancel()
			continue
		}
		launch = append(launch, launchSpec{task: t, exec: exec, ctx: ctx, run: run})
	}

	r.ioMu.Unlock()

	if failedUnknown || len(launch) > 0 {
		r.notifyChange()
	}
	for _, ls := range launch {
		go r.execute(ls.task, ls.exec, ls.ctx, ls.run)
	}
	return failedUnknown || len(launch) > 0, nil
}

// eligible reports whether a task may run now. Auto-requeue of a failed task is
// expressed here: a failed task with attempts under the cap whose NextAttemptAt
// has arrived is treated as runnable (and transitioned to queued at execute time).
func (r *Runner) eligible(t *Task, now time.Time) bool {
	if now.Before(t.NextAttemptAt) {
		return false
	}
	switch t.State {
	case StateQueued:
		return true
	case StateFailed:
		return t.Attempts < r.maxAttempts
	default:
		return false
	}
}

// execute runs one already-claimed task (state == running on disk) through its
// executor inside a runner-owned timeout, then records the outcome (done /
// requeued / failed-with-backoff / dead) under ioMu. The ctx, cancel, guard, and
// activeRun are all built by dispatch at claim time and the run is already
// registered in r.runs, so a Cancel arriving before this goroutine is scheduled
// still finds and fences it. The executor's CommitGuard fences its durable write
// against a concurrent Cancel AND against the runner-owned timeout: once the
// executor has entered its commit, neither Cancel nor the deadline cancels the
// context, so the single durable write is never torn. This mirrors keeper
// compaction, whose commit (ApplyKeeperCompactResult) is ctx-free and therefore
// timeout-immune by construction.
func (r *Runner) execute(t *Task, exec executor, ctx context.Context, run *activeRun) {
	guard := run.guard
	cancel := run.cancel

	// timeoutStop stops the deadline timer once the run has exited so it cannot
	// fire (and consult the guard) after teardown.
	timeoutStop := make(chan struct{})
	timer := time.NewTimer(exec.timeout)
	go func() {
		defer timer.Stop()
		select {
		case <-timer.C:
			// The deadline elapsed. Honor the commit fence: if the executor is
			// already committing, do NOT cancel — let the durable write finish
			// untorn (this goroutine still waits for the executor to return).
			if guard.tryFence() {
				cancel()
			}
		case <-timeoutStop:
		}
	}()

	// Attach the guard so the executor body can fence its durable write.
	taskForExec := t.clone()
	taskForExec.CommitGuard = guard

	runErr := func() (err error) {
		defer func() {
			if rec := recover(); rec != nil {
				err = fmt.Errorf("tasks: executor panic: %v", rec)
			}
		}()
		return exec.fn(ctx, taskForExec)
	}()

	close(timeoutStop)
	cancel()

	// Record the terminal outcome BEFORE signaling exit, so Cancel's
	// blocks-until-exit contract holds: when Cancel's <-run.done unblocks, the
	// goroutine has fully exited AND the durable terminal record is already
	// written. This mirrors keeper compaction, where close(done) runs only after the
	// durable ApplyKeeperCompactResult has settled.
	r.finish(t.ID, runErr)

	// Deregister the run and free its per-kind slot, THEN signal exit. From here, a
	// Cancel that was already blocked on run.done unblocks; a Cancel arriving after
	// this finds no entry for the id and returns immediately.
	r.mu.Lock()
	delete(r.runs, run.id)
	r.inflight[run.kind]--
	r.mu.Unlock()
	close(run.done)

	// A freed slot may admit a queued task of a now-uncapped kind; re-dispatch.
	r.nudge()
}

// finish records the terminal outcome of a run. It re-loads the record under ioMu
// (a mid-run Enqueue may have flipped Requeued / pushed NextAttemptAt) so it
// honors any coalesced trigger that landed during the run rather than clobbering
// it. The record on disk is authoritative for everything except the run result.
func (r *Runner) finish(id string, runErr error) {
	r.ioMu.Lock()

	cur, err := r.store.Load(id)
	if err != nil {
		r.ioMu.Unlock()
		r.log("tasks: reload %s before finish: %v", id, err)
		return
	}
	if cur == nil {
		// The record was deleted out from under the run; nothing to persist.
		r.ioMu.Unlock()
		return
	}

	requeue := false
	if runErr != nil {
		if cur.Requeued {
			// A re-enqueue arrived mid-run (e.g. the removal-boundary final task
			// enqueued with ZeroDebounce to run NOW). Even though this run failed,
			// honoring the explicit re-enqueue demand takes precedence over backoff:
			// re-queue at the NextAttemptAt that Enqueue set (now, for ZeroDebounce)
			// instead of demoting the run-now demand to a backoff delay. Attempts
			// reset because the re-enqueue is fresh logical demand, not a
			// continuation of the failed run. Without this, a mid-run ZeroDebounce
			// would be silently downgraded to a 1m/2m/... retry and the immediacy
			// the API guarantees would be lost.
			now := r.now()
			cur.State = StateQueued
			cur.Requeued = false
			cur.Attempts = 0
			cur.LastError = runErr.Error()
			cur.UpdatedAt = now
			if err := r.store.Save(cur); err != nil {
				r.ioMu.Unlock()
				r.log("tasks: persist requeued-after-failure state for %s: %v", id, err)
				return
			}
			requeue = true
		} else {
			r.recordFailureLocked(cur, runErr)
		}
	} else {
		now := r.now()
		cur.LastError = ""
		cur.UpdatedAt = now
		if cur.Requeued {
			// A re-enqueue arrived mid-run: honor it by re-queuing instead of marking
			// done, so the coalesced trigger is not lost. NextAttemptAt was set by
			// that Enqueue; preserve it. Attempts reset (fresh logical work).
			cur.State = StateQueued
			cur.Requeued = false
			cur.Attempts = 0
			requeue = true
		} else {
			cur.State = StateDone
		}
		if err := r.store.Save(cur); err != nil {
			r.ioMu.Unlock()
			r.log("tasks: persist done state for %s: %v", id, err)
			return
		}
	}
	r.ioMu.Unlock()

	r.notifyChange()
	if requeue {
		r.nudge()
	}
}

// recordFailureLocked persists a failed outcome with capped-exponential backoff,
// or dead once the attempt cap is reached. The caller holds ioMu.
func (r *Runner) recordFailureLocked(t *Task, cause error) {
	now := r.now()
	t.LastError = cause.Error()
	t.UpdatedAt = now
	if t.Attempts >= r.maxAttempts {
		t.State = StateDead
		t.NextAttemptAt = now
		r.log("tasks: %s dead after %d attempts: %v", t.ID, t.Attempts, cause)
	} else {
		t.State = StateFailed
		t.NextAttemptAt = now.Add(r.backoff(t.Attempts))
		r.log("tasks: %s failed (attempt %d/%d), retry at %s: %v",
			t.ID, t.Attempts, r.maxAttempts, t.NextAttemptAt.Format(time.RFC3339), cause)
	}
	if err := r.store.Save(t); err != nil {
		r.log("tasks: persist failure state for %s: %v", t.ID, err)
	}
}

// backoff returns the capped-exponential delay for the given attempt number
// (1-based): base * 2^(attempt-1), clamped to cap. attempt 1 ⇒ base; attempt 2 ⇒
// 2*base; … capped at backoffCap.
func (r *Runner) backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := r.backoffBase
	for i := 1; i < attempt; i++ {
		d *= 2
		// `d <= 0` guards int64 overflow: with a near-MaxInt64 cap and a large
		// MaxAttempts the doubling can wrap negative before reaching the cap, which
		// would otherwise yield a negative (past) NextAttemptAt and a hot retry loop.
		if d <= 0 || d >= r.backoffCap {
			return r.backoffCap
		}
	}
	if d > r.backoffCap {
		return r.backoffCap
	}
	return d
}

// cancelAll cancels every in-flight run (honoring each commit fence) and blocks
// until they have all exited. Used by Stop AFTER the dispatch loop has exited, so
// no new run can be registered while it drains. It snapshots r.runs under mu and
// then fences+waits without holding mu: a run that finishes between the snapshot
// and the fence closes its done channel (tryFence on a settled guard is a safe
// no-op) so <-run.done returns immediately.
func (r *Runner) cancelAll() {
	r.mu.Lock()
	runs := make([]*activeRun, 0, len(r.runs))
	for _, run := range r.runs {
		runs = append(runs, run)
	}
	r.mu.Unlock()
	for _, run := range runs {
		if run.guard.tryFence() {
			run.cancel()
		}
		<-run.done
	}
}

// --- helpers ---------------------------------------------------------------

func (r *Runner) nudge() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *Runner) notifyChange() {
	r.mu.Lock()
	fn := r.onChange
	r.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func nonZeroDuration(v, fallback time.Duration) time.Duration {
	if v > 0 {
		return v
	}
	return fallback
}

func nonZeroInt(v, fallback int) int {
	if v > 0 {
		return v
	}
	return fallback
}
