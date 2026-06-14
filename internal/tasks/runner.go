package tasks

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Tuning defaults. Kept as package vars (not consts) so a kind config or a test
// can override them through Options without changing the source.
const (
	// DefaultMaxAttempts is the attempt cap: a task that has failed this many
	// times goes dead instead of auto-requeuing.
	DefaultMaxAttempts = 5
	// DefaultBackoffBase / DefaultBackoffCap bound the capped-exponential backoff
	// schedule (1m, 2m, 4m, … capped at 1h).
	DefaultBackoffBase = time.Minute
	DefaultBackoffCap  = time.Hour
	// DefaultExecutorTimeout is the per-executor context.WithTimeout the runner
	// wraps around every invocation. The janitor uses 5 minutes; a kind can
	// override it via RegisterWithTimeout.
	DefaultExecutorTimeout = 5 * time.Minute
	// defaultPollInterval is how often the worker wakes to re-scan the queue for a
	// task whose NextAttemptAt has arrived. The single worker is level-triggered,
	// so this only bounds scheduling latency for time-gated requeues.
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
// write. Modeled on workspaceContextJanitorExecutor (the function-pointer type at
// internal/daemon/workspace_context_janitor.go:41).
type ExecutorFunc func(ctx context.Context, task *Task) error

// executor pairs a registered ExecutorFunc with its per-kind timeout.
type executor struct {
	fn      ExecutorFunc
	timeout time.Duration
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
}

// Options configures a Runner at construction.
type Options struct {
	// Root is the notebook root dir. Empty/whitespace ⇒ the runner is disabled.
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

// Runner is the durable, file-backed, single-worker task runner.
type Runner struct {
	store    *store
	log      LogFunc
	now      func() time.Time
	disabled bool

	pollInterval time.Duration
	maxAttempts  int
	backoffBase  time.Duration
	backoffCap   time.Duration

	// onChange, if set, fires after every lifecycle transition (for the status
	// broadcast). It runs synchronously inside the worker, so it must be cheap and
	// non-blocking; the daemon's wiring just emits a websocket event.
	onChange func()

	mu        sync.Mutex
	executors map[string]executor
	started   bool

	// ioMu serializes every read-modify-write cycle on a task record. The single
	// worker plus arbitrary daemon goroutines calling Enqueue/Retry all mutate the
	// same on-disk records; without this a concurrent Enqueue and a worker state
	// write would lost-update each other. It is a coarse store-level lock — correct
	// and cheap because record I/O is fast and contention is low. It is ALWAYS
	// acquired without holding mu (and the executor never runs while it is held),
	// so there is no lock-ordering hazard with mu.
	ioMu sync.Mutex

	// run is the state of the currently-running task (nil when idle). Cancel reads
	// it under mu to coordinate the commit fence and the blocks-until-exit wait.
	run *activeRun

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

// activeRun tracks the in-flight task so Cancel can fence its commit and block
// until its goroutine exits. Ported from the janitor's cancel/done/committing
// fields (workspace_context_janitor.go:264-269).
type activeRun struct {
	id     string
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
		pollInterval: nonZeroDuration(opts.PollInterval, defaultPollInterval),
		maxAttempts:  nonZeroInt(opts.MaxAttempts, DefaultMaxAttempts),
		backoffBase:  nonZeroDuration(opts.BackoffBase, DefaultBackoffBase),
		backoffCap:   nonZeroDuration(opts.BackoffCap, DefaultBackoffCap),
		wake:         make(chan struct{}, 1),
	}
	if root := trimRoot(opts.Root); root == "" {
		r.disabled = true
		return r
	} else {
		r.store = newStore(root)
		r.store.log = log
	}
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

// Register wires an executor for a kind with the default per-kind timeout.
func (r *Runner) Register(kind string, fn ExecutorFunc) error {
	return r.RegisterWithTimeout(kind, fn, DefaultExecutorTimeout)
}

// RegisterWithTimeout wires an executor for a kind with an explicit timeout (the
// runner wraps every invocation in context.WithTimeout of this duration). It is
// an error to register the same kind twice or to pass a nil fn.
func (r *Runner) RegisterWithTimeout(kind string, fn ExecutorFunc, timeout time.Duration) error {
	if r.disabled {
		return ErrDisabled
	}
	if fn == nil {
		return errors.New("tasks: executor must not be nil")
	}
	if kind == "" {
		return errors.New("tasks: kind must not be empty")
	}
	if timeout <= 0 {
		timeout = DefaultExecutorTimeout
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.executors[kind]; exists {
		return fmt.Errorf("tasks: kind already registered: %s", kind)
	}
	r.executors[kind] = executor{fn: fn, timeout: timeout}
	return nil
}

// Start recovers orphaned running tasks (reset to queued) and launches the single
// worker goroutine. It is safe (no-op) on a disabled runner. Calling Start twice
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
	if err := r.store.init(); err != nil {
		r.mu.Unlock()
		return fmt.Errorf("tasks: init store: %w", err)
	}
	// Take exclusive ownership of the tasks dir before doing anything else: a
	// second live Runner on the same root would double-execute every task (its own
	// worker, its own in-memory CommitGuard). Acquiring the lock fails fast with
	// ErrAlreadyRunning rather than silently double-applying durable writes.
	lockPath, err := r.store.acquireLock()
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
	n, err := r.store.recoverOrphans(r.now())
	r.ioMu.Unlock()
	if err != nil {
		r.log("tasks: recover orphan running tasks: %v", err)
	} else if n > 0 {
		r.log("tasks: recovered %d orphan running task(s)", n)
	}

	go r.loop()
	return nil
}

// Stop signals the worker to exit and blocks until it has. A Stop concurrent with
// an in-flight executor lets that executor finish (the worker checks done only
// between tasks); callers that need a specific task aborted use Cancel first. Safe
// to call on a disabled or never-started runner.
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

	// Cancel any in-flight run so Stop does not block on a long executor; this
	// respects the commit fence (an already-committing run still finishes).
	r.cancelActive()

	close(done)
	<-exit

	// Release single-instance ownership only after the worker has fully exited, so
	// no other Runner can claim the root while ours is still draining.
	r.store.releaseLock(lockPath)
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

	existing, err := r.store.load(id)
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

	if err := r.store.save(task); err != nil {
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

	existing, err := r.store.load(id)
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
	if err := r.store.save(existing); err != nil {
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
// spirit from cancelWorkspaceContextJanitor (workspace_context_janitor.go:310).
func (r *Runner) Cancel(id string) {
	if r.disabled {
		return
	}
	r.mu.Lock()
	run := r.run
	if run == nil || run.id != id {
		r.mu.Unlock()
		return
	}
	// Only cancel the context if the run has not yet entered its commit fence.
	// If it is already committing, leave the context alone and just wait — the
	// blocks-until-exit contract finishes an in-progress durable write untorn.
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
	err := r.store.delete(id)
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
	all, err := r.store.list()
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
	return r.store.load(id)
}

// --- worker loop -----------------------------------------------------------

// loop is the single worker goroutine. It is level-triggered: each pass picks the
// one eligible task (if any), runs it, then waits for the next nudge or poll tick.
// There is no worker pool — serialization is the whole point (it removes the need
// for a per-task lock).
func (r *Runner) loop() {
	defer close(r.exit)
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()
	for {
		// Drain all currently-eligible work before sleeping, so a burst of enqueues
		// does not stall behind the poll interval.
		for {
			ran, err := r.runNext()
			if err != nil {
				r.log("tasks: worker pass: %v", err)
				break
			}
			if !ran {
				break
			}
			select {
			case <-r.done:
				return
			default:
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

// runNext selects the single most-eligible task and runs it. It reports whether a
// task ran (so the caller can keep draining). A task is eligible when it is queued
// (or a failed task past its NextAttemptAt with attempts under the cap) and its
// NextAttemptAt has arrived. Selection runs under ioMu so a concurrent Enqueue/
// Retry cannot make the chosen record stale before the run is claimed.
func (r *Runner) runNext() (bool, error) {
	r.ioMu.Lock()
	now := r.now()
	all, err := r.store.list()
	if err != nil {
		r.ioMu.Unlock()
		return false, err
	}

	var pick *Task
	for _, t := range all {
		if !r.eligible(t, now) {
			continue
		}
		// Earliest NextAttemptAt wins; ties broken by oldest CreatedAt for fairness.
		if pick == nil ||
			t.NextAttemptAt.Before(pick.NextAttemptAt) ||
			(t.NextAttemptAt.Equal(pick.NextAttemptAt) && t.CreatedAt.Before(pick.CreatedAt)) {
			pick = t
		}
	}
	if pick == nil {
		r.ioMu.Unlock()
		return false, nil
	}

	// Claim the task: mark it running and persist while still holding ioMu, so the
	// claim is atomic against Enqueue/Retry. We then release ioMu and run the
	// executor unlocked (it may take minutes; holding ioMu would block enqueues
	// and status reads). Concurrency past this point is handled by the commit
	// fence and the Requeued flag, not by ioMu.
	r.mu.Lock()
	exec, ok := r.executors[pick.Kind]
	r.mu.Unlock()
	if !ok {
		// No executor for this kind (e.g. a stale record from an old build). Fail it
		// in place so it surfaces rather than spinning the worker.
		r.recordFailureLocked(pick, fmt.Errorf("%w: %s", ErrUnknownKind, pick.Kind))
		r.ioMu.Unlock()
		r.notifyChange()
		return true, nil
	}

	pick.State = StateRunning
	pick.Attempts++
	pick.Requeued = false
	pick.UpdatedAt = now
	if err := r.store.save(pick); err != nil {
		r.ioMu.Unlock()
		r.log("tasks: persist running state for %s: %v", pick.ID, err)
		return false, nil
	}
	r.ioMu.Unlock()
	r.notifyChange()

	r.execute(pick, exec)
	return true, nil
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
// requeued / failed-with-backoff / dead) under ioMu. The executor's CommitGuard
// fences its durable write against a concurrent Cancel AND against the
// runner-owned timeout: once the executor has entered its commit, neither Cancel
// nor the deadline cancels the context, so the single durable write is never
// torn. This mirrors the janitor, whose commit (ApplyWorkspaceContextJanitorResult)
// is ctx-free and therefore timeout-immune by construction.
func (r *Runner) execute(t *Task, exec executor) {
	// Use WithCancel (not WithTimeout) so the deadline is enforced by a timer we
	// can route through the commit fence: a timeout that arrives mid-commit must
	// not cancel the context, exactly like a Cancel that arrives mid-commit.
	ctx, cancel := context.WithCancel(context.Background())
	guard := &CommitGuard{}
	runDone := make(chan struct{})

	// timeoutFired stops the deadline timer once the run has exited so it cannot
	// fire (and consult the guard) after teardown.
	timeoutStop := make(chan struct{})
	timer := time.NewTimer(exec.timeout)
	go func() {
		defer timer.Stop()
		select {
		case <-timer.C:
			// The deadline elapsed. Honor the commit fence: if the executor is
			// already committing, do NOT cancel — let the durable write finish
			// untorn (the worker still blocks on the executor returning).
			if guard.tryFence() {
				cancel()
			}
		case <-timeoutStop:
		}
	}()

	r.mu.Lock()
	r.run = &activeRun{id: t.ID, cancel: cancel, guard: guard, done: runDone}
	r.mu.Unlock()

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
	// written. This mirrors the janitor, where close(done) runs only after the
	// durable ApplyWorkspaceContextJanitorResult has settled.
	r.finish(t.ID, runErr)

	// Tear down the active-run registration and signal exit. From here, a Cancel
	// that was already blocked on run.done unblocks; a Cancel arriving after this
	// finds r.run == nil (or a different run) and returns immediately.
	r.mu.Lock()
	r.run = nil
	r.mu.Unlock()
	close(runDone)
}

// finish records the terminal outcome of a run. It re-loads the record under ioMu
// (a mid-run Enqueue may have flipped Requeued / pushed NextAttemptAt) so it
// honors any coalesced trigger that landed during the run rather than clobbering
// it. The record on disk is authoritative for everything except the run result.
func (r *Runner) finish(id string, runErr error) {
	r.ioMu.Lock()

	cur, err := r.store.load(id)
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
			if err := r.store.save(cur); err != nil {
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
		if err := r.store.save(cur); err != nil {
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
	if err := r.store.save(t); err != nil {
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

// cancelActive cancels the in-flight run (if any), honoring the commit fence and
// blocking until it exits. Used by Stop.
func (r *Runner) cancelActive() {
	r.mu.Lock()
	run := r.run
	if run == nil {
		r.mu.Unlock()
		return
	}
	if run.guard.tryFence() {
		run.cancel()
	}
	done := run.done
	r.mu.Unlock()
	<-done
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
