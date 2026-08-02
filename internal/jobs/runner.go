package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Tuning defaults. Each is overridable per-runner through the matching Options
// field (a zero value falls back to the default, resolved in New).
const (
	// DefaultMaxAttempts is the attempt cap: a job that has failed this many
	// times goes dead instead of auto-requeuing. A job may override it.
	DefaultMaxAttempts = 5
	// DefaultBackoffBase / DefaultBackoffCap bound the capped-exponential backoff
	// schedule (1m, 2m, 4m, … capped at 1h).
	DefaultBackoffBase = time.Minute
	DefaultBackoffCap  = time.Hour
	// DefaultHandlerTimeout is the per-handler context.WithTimeout the runner
	// wraps around every invocation. A kind can override it via RegisterWith.
	DefaultHandlerTimeout = 5 * time.Minute
	// DefaultRetention is how long a successfully completed job is kept before
	// trimming, mirroring bus.DefaultRetention. Only done jobs are trimmed:
	// a dead job is the actionable record a failure notification points at, it
	// only exists when nobody has acted on it, and it is not what grows.
	DefaultRetention = 30 * 24 * time.Hour
	// DefaultTrimInterval is how often retention runs.
	DefaultTrimInterval = time.Hour
	// defaultPollInterval is how often the dispatch loop wakes to re-scan for a
	// job whose ScheduledAt has arrived. The loop is level-triggered, so this only
	// bounds scheduling latency for time-gated requeues.
	defaultPollInterval = time.Second
)

// eligiblePageSize bounds one dispatch pass's read of claimable jobs, so a
// pathological queue cannot pull an unbounded result set into memory every
// second. Receipt: the real task tables this queue replaces held 146 rows
// (~/.attn) and 206 rows (~/.attn-dev), all but a handful of them terminal and
// therefore never eligible; the eligible working set is single digits. A
// thousand is three orders of magnitude past that, so only something broken
// reaches it — and dispatch says so out loud when it does rather than quietly
// serving a truncated page.
const eligiblePageSize = 1000

// ErrDisabled is returned by mutating runner methods when the runner was
// constructed without a store. A disabled runner never starts a dispatch loop
// and persists nothing; it exists so a consumer can hold a non-nil Runner before
// the real one is built.
var ErrDisabled = errors.New("jobs: runner is disabled (no store)")

// ErrUnknownKind is returned by Enqueue when no handler is registered for the
// job's kind.
var ErrUnknownKind = errors.New("jobs: no handler registered for kind")

// HandlerFunc runs one job to completion. It returns the job's result (nil when
// the work produces no value) or an error, in which case the job goes failed and
// may auto-requeue. The runner owns the context.WithTimeout wrapper around the
// invocation; the handler body owns the work and calls job.CommitGuard.Enter and
// Leave around its single durable write.
//
// A returned result must be JSON-marshallable. If it is not, the run is recorded
// as FAILED with the marshal error: a result nobody can read is not a success,
// and dropping it quietly would be exactly the silent truncation this queue
// exists to avoid.
type HandlerFunc func(ctx context.Context, job *Job) (any, error)

// handler pairs a registered HandlerFunc with its per-kind timeout and
// concurrency cap.
type handler struct {
	fn      HandlerFunc
	timeout time.Duration
	// limit is the max number of jobs OF THIS KIND that may run at once. It is
	// always >= 1 after registration, so a kind is serialized with itself by
	// default while different kinds run in parallel.
	limit int
}

// HandlerConfig tunes a registered handler. The zero value is the default:
// DefaultHandlerTimeout and a per-kind concurrency cap of 1.
type HandlerConfig struct {
	// Timeout is the per-invocation context.WithTimeout the runner wraps around
	// every call to this handler. Zero ⇒ DefaultHandlerTimeout.
	Timeout time.Duration
	// MaxConcurrent bounds how many jobs of this kind may run simultaneously.
	// Zero ⇒ 1 (a kind serialized with itself); different kinds always run in
	// parallel regardless.
	MaxConcurrent int
}

// EnqueueOptions tunes a single Enqueue call.
type EnqueueOptions struct {
	// UniqueKey opts this job into coalescing within its kind (see Job.UniqueKey).
	// Leaving it empty enqueues a distinct job every time.
	UniqueKey string
	// Payload is the run's inputs, marshalled to JSON. Nil enqueues no payload.
	Payload any
	// Delay pushes ScheduledAt forward from now. For a coalescing job this is the
	// debounce knob: re-enqueueing within the window keeps pushing the run later,
	// collapsing a burst of triggers into one run. Zero means "run as soon as
	// eligible".
	Delay time.Duration
	// RunNow forces ScheduledAt = now regardless of Delay, overriding a debounce
	// already pending on a coalescing record. This is how a caller says "the
	// window is over, run it".
	RunNow bool
	// Priority orders this job against other eligible jobs; higher runs first.
	Priority int
	// MaxAttempts overrides the runner's attempt cap for this job. Zero ⇒ the
	// runner default.
	MaxAttempts int
}

// Options configures a Runner at construction.
type Options struct {
	// Store injects the persistence backend. A nil Store disables the runner.
	Store Store
	// Log receives runtime log lines. A nil Log is replaced with a no-op.
	Log LogFunc
	// Now injects the clock for deterministic backoff/coalescing tests. nil ⇒
	// time.Now. The runner always normalizes to UTC.
	Now func() time.Time
	// PollInterval overrides the dispatch loop's re-scan interval (tests use a
	// short interval to avoid real-time waits). Zero ⇒ defaultPollInterval.
	PollInterval time.Duration
	// MaxAttempts overrides the default attempt cap. Zero ⇒ DefaultMaxAttempts.
	MaxAttempts int
	// BackoffBase / BackoffCap override the backoff schedule. Zero ⇒ the defaults.
	BackoffBase time.Duration
	BackoffCap  time.Duration
	// Retention / TrimInterval override the done-job retention window and how
	// often it runs. Zero ⇒ the defaults.
	Retention    time.Duration
	TrimInterval time.Duration
}

// Runner is the durable job queue. A single dispatch loop selects eligible jobs
// and launches each as its own goroutine, bounded by a per-kind concurrency cap
// (default 1). Every record read-modify-write funnels through ioMu, so
// persistence stays serialized even though handlers run concurrently.
type Runner struct {
	store    Store
	log      LogFunc
	now      func() time.Time
	disabled bool

	pollInterval time.Duration
	maxAttempts  int
	backoffBase  time.Duration
	backoffCap   time.Duration
	retention    time.Duration
	trimInterval time.Duration

	// onChange, if set, fires after every lifecycle transition. It may be called
	// CONCURRENTLY — from the dispatch goroutine, from each in-flight run's
	// finish(), and from Enqueue/Retry/Remove on arbitrary caller goroutines — so
	// it must be cheap, non-blocking, and safe to invoke from multiple goroutines.
	onChange func(jobID string)

	// onTerminalFailure, if set, fires exactly once when a job crosses into the
	// terminal StateDead (retries exhausted) — the actionable "this background
	// work gave up" signal. Like onChange it may be invoked from several
	// goroutines. It always runs AFTER ioMu is released with a cloned record, so
	// the callback may touch the store without lock-ordering risk.
	onTerminalFailure func(*Job)

	mu       sync.Mutex
	handlers map[string]handler
	started  bool

	// ioMu serializes every read-modify-write cycle on a job record. The dispatch
	// loop, in-flight runs' finish(), and arbitrary callers' Enqueue/Retry all
	// mutate the same records; without this a concurrent Enqueue and a
	// claim/finish write would lost-update each other. When both locks are needed
	// ioMu is ALWAYS the outer lock: it is acquired without holding mu, so there
	// is no lock-ordering hazard.
	ioMu sync.Mutex

	// runs holds every in-flight run keyed by job id. Cancel(id) fences and waits
	// on its entry; Stop drains them all. Guarded by mu.
	runs map[string]*activeRun

	// inflight counts currently-running jobs per kind. dispatch reads it to
	// enforce each kind's concurrency cap; it is bumped when a run is claimed and
	// dropped when the run goroutine exits. Guarded by mu.
	inflight map[string]int

	// wake nudges the dispatch loop to re-scan immediately after an Enqueue or
	// Retry rather than waiting for the next poll tick. Buffered depth 1 so a
	// nudge never blocks the caller.
	wake chan struct{}
	done chan struct{} // closed by Stop to tell the loops to exit
	exit chan struct{} // closed by the dispatch loop when it has fully exited

	// lockToken is the single-instance ownership marker acquired by Start and
	// released by Stop. Empty when not started.
	lockToken string
}

// activeRun tracks one in-flight job so Cancel can fence its commit and block
// until its goroutine exits.
type activeRun struct {
	id     string
	kind   string // so the run goroutine can drop its per-kind in-flight slot on exit
	cancel context.CancelFunc
	guard  *CommitGuard
	done   chan struct{} // closed when the run goroutine has fully exited
}

// New constructs a Runner. With a nil Store the runner is disabled: mutating
// methods are safe no-ops returning ErrDisabled, Start/Stop do nothing, and
// nothing is persisted.
func New(opts Options) *Runner {
	log := opts.Log
	if log == nil {
		log = func(string, ...interface{}) {}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	r := &Runner{
		store:        opts.Store,
		log:          log,
		now:          func() time.Time { return now().UTC() },
		disabled:     opts.Store == nil,
		handlers:     make(map[string]handler),
		runs:         make(map[string]*activeRun),
		inflight:     make(map[string]int),
		pollInterval: nonZeroDuration(opts.PollInterval, defaultPollInterval),
		maxAttempts:  nonZeroInt(opts.MaxAttempts, DefaultMaxAttempts),
		backoffBase:  nonZeroDuration(opts.BackoffBase, DefaultBackoffBase),
		backoffCap:   nonZeroDuration(opts.BackoffCap, DefaultBackoffCap),
		retention:    nonZeroDuration(opts.Retention, DefaultRetention),
		trimInterval: nonZeroDuration(opts.TrimInterval, DefaultTrimInterval),
		wake:         make(chan struct{}, 1),
	}
	return r
}

// Disabled reports whether the runner is a no-op (no store).
func (r *Runner) Disabled() bool { return r.disabled }

// OnChange registers a callback fired after every lifecycle transition. It is
// optional and must be cheap; pass nil to clear.
func (r *Runner) OnChange(fn func(jobID string)) {
	r.mu.Lock()
	r.onChange = fn
	r.mu.Unlock()
}

// OnTerminalFailure registers a callback fired once when a job reaches StateDead
// (retries exhausted). It is optional and must be cheap and concurrency-safe;
// pass nil to clear.
func (r *Runner) OnTerminalFailure(fn func(*Job)) {
	r.mu.Lock()
	r.onTerminalFailure = fn
	r.mu.Unlock()
}

// Register wires a handler for a kind with the default timeout and a
// concurrency cap of 1.
func (r *Runner) Register(kind string, fn HandlerFunc) error {
	return r.RegisterWith(kind, fn, HandlerConfig{})
}

// RegisterWith wires a handler for a kind with an explicit timeout and per-kind
// concurrency cap (see HandlerConfig). It is an error to register the same kind
// twice or to pass a nil fn.
func (r *Runner) RegisterWith(kind string, fn HandlerFunc, cfg HandlerConfig) error {
	if r.disabled {
		return ErrDisabled
	}
	if fn == nil {
		return errors.New("jobs: handler must not be nil")
	}
	if kind == "" {
		return errors.New("jobs: kind must not be empty")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultHandlerTimeout
	}
	limit := cfg.MaxConcurrent
	if limit <= 0 {
		limit = 1
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.handlers[kind]; exists {
		return fmt.Errorf("jobs: kind already registered: %s", kind)
	}
	r.handlers[kind] = handler{fn: fn, timeout: timeout, limit: limit}
	return nil
}

// Start recovers orphaned running jobs (reset to queued) and launches the
// dispatch and retention loops. It is safe (no-op) on a disabled runner. Calling
// Start twice is an error.
func (r *Runner) Start() error {
	if r.disabled {
		return nil
	}
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return errors.New("jobs: runner already started")
	}
	if err := r.store.Init(); err != nil {
		r.mu.Unlock()
		return fmt.Errorf("jobs: init store: %w", err)
	}
	// Take exclusive ownership before doing anything else: a second live Runner on
	// the same store would double-execute every job (its own dispatch loop, its
	// own in-memory CommitGuard). Failing fast beats silently double-applying
	// durable writes.
	token, err := r.store.AcquireLock()
	if err != nil {
		r.mu.Unlock()
		return err
	}
	r.lockToken = token
	r.started = true
	r.done = make(chan struct{})
	r.exit = make(chan struct{})
	done := r.done
	r.mu.Unlock()

	// recoverOrphans is a read-modify-write over the store, so it holds ioMu like
	// every other RMW path. It runs before the loops launch, so contention is nil.
	r.ioMu.Lock()
	n, err := r.store.RecoverOrphans(r.now())
	r.ioMu.Unlock()
	if err != nil {
		r.log("jobs: recover orphan running jobs: %v", err)
	} else if n > 0 {
		r.log("jobs: recovered %d orphan running job(s)", n)
	}

	go r.loop()
	go r.retentionLoop(done)
	return nil
}

// Stop signals the loops to exit, then cancels and drains every in-flight run.
// cancelAll honors each commit fence, so an already-committing run still
// finishes its durable write untorn. Safe to call on a disabled or never-started
// runner.
func (r *Runner) Stop() {
	r.mu.Lock()
	if !r.started {
		r.mu.Unlock()
		return
	}
	// Flip started to false WHILE holding mu so a second concurrent Stop sees
	// started==false and returns instead of double-closing done (which panics and
	// would take the whole daemon down). Only the Stop that wins this transition
	// closes done and waits on exit.
	r.started = false
	done, exit := r.done, r.exit
	token := r.lockToken
	r.lockToken = ""
	r.mu.Unlock()

	// Order matters: runs are detached goroutines the dispatch loop does not join.
	// First stop the loop launching MORE runs, THEN cancel and join everything
	// still in flight, so cancelAll is guaranteed to terminate.
	close(done)
	<-exit
	r.cancelAll()

	// Release ownership only after every run has exited, so no other Runner can
	// claim the store while ours is still draining.
	r.store.ReleaseLock(token)
}

// Enqueue persists a job for kind. With opts.UniqueKey set it coalesces onto the
// existing record for that kind+key: a queued/failed/dead/done record is reset to
// queued with a fresh schedule, and a record that is currently RUNNING is left to
// finish with its Requeued flag set so the coalesced trigger re-runs it rather
// than being lost. Without a unique key it always creates a new record.
func (r *Runner) Enqueue(kind string, opts EnqueueOptions) (*Job, error) {
	if r.disabled {
		return nil, ErrDisabled
	}
	if kind == "" {
		return nil, errors.New("jobs: kind is required")
	}

	r.mu.Lock()
	_, known := r.handlers[kind]
	r.mu.Unlock()
	if !known {
		return nil, fmt.Errorf("%w: %s", ErrUnknownKind, kind)
	}

	payload, err := marshalPayload(opts.Payload)
	if err != nil {
		return nil, fmt.Errorf("jobs: encode payload for %s: %w", kind, err)
	}

	now := r.now()
	scheduled := now
	if !opts.RunNow && opts.Delay > 0 {
		scheduled = now.Add(opts.Delay)
	}

	r.ioMu.Lock()
	defer r.ioMu.Unlock()

	var existing *Job
	if opts.UniqueKey != "" {
		existing, err = r.store.LoadByKey(kind, opts.UniqueKey)
		if err != nil {
			return nil, err
		}
	}

	var job *Job
	switch {
	case existing == nil:
		// Priority, MaxAttempts, and Payload are applied below, in the one place
		// that also handles a coalescing re-enqueue carrying fresh values.
		job = &Job{
			ID:          uuid.NewString(),
			Kind:        kind,
			UniqueKey:   opts.UniqueKey,
			State:       StateQueued,
			ScheduledAt: scheduled,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
	case existing.State == StateRunning:
		// The job is running right now. Overwriting it would tear the in-flight
		// run's bookkeeping, so instead record the demand: mark Requeued and push
		// ScheduledAt to the requested time. When the run finishes, finish() sees
		// Requeued and returns the job to queued instead of done, so this coalesced
		// trigger is honored rather than lost.
		existing.Requeued = true
		existing.ScheduledAt = scheduled
		existing.UpdatedAt = now
		job = existing
	default:
		// queued / failed / dead / done: coalesce by resetting the same record back
		// to queued with a fresh schedule. Attempts reset because a new enqueue is
		// new logical demand, not a continuation of the old failure run.
		existing.State = StateQueued
		existing.Attempts = 0
		existing.LastError = ""
		existing.Result = nil
		existing.Requeued = false
		existing.ScheduledAt = scheduled
		existing.UpdatedAt = now
		job = existing
	}

	// Apply the carried inputs after the branch settled on `job`, so they land the
	// same way on a brand-new record, a mid-run requeue (so the re-run reads the
	// fresh inputs), and a coalescing re-enqueue. A nil Payload LEAVES an existing
	// payload intact: a bare re-trigger that carries no inputs must not wipe the
	// ones a prior enqueue stashed.
	if payload != nil {
		job.Payload = payload
	}
	if opts.Priority != 0 {
		job.Priority = opts.Priority
	}
	if opts.MaxAttempts != 0 {
		job.MaxAttempts = opts.MaxAttempts
	}

	if err := r.store.Save(job); err != nil {
		return nil, err
	}
	r.notifyChange(job.ID)
	r.nudge()
	return job.clone(), nil
}

// Retry forces a failed or dead job back to queued with ScheduledAt = now. It is
// the manual-recovery action. A job that is queued/running/done is left as-is.
// Returns the updated job, or nil if no such job exists.
func (r *Runner) Retry(id string) (*Job, error) {
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
	existing.ScheduledAt = now
	existing.UpdatedAt = now
	if err := r.store.Save(existing); err != nil {
		return nil, err
	}
	r.notifyChange(existing.ID)
	r.nudge()
	return existing.clone(), nil
}

// Cancel signals the running handler for id (if it is the one running) and DOES
// NOT RETURN until that job's goroutine has fully exited. If the run is already
// inside its commit fence, Cancel does not cancel the context — it waits for the
// goroutine to finish its durable write and exit, so the write is never torn.
// Cancel of a job that is not currently running returns immediately.
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

// fenceAndWait performs the shared cancel-and-block core. The CALLER must hold
// r.mu and pass a non-nil run; fenceAndWait RELEASES r.mu before it blocks on the
// done channel. It cancels the run's context only if the run has not yet entered
// its commit fence — if the run is already committing it leaves the context alone
// and just waits, so the blocks-until-exit contract finishes an in-progress
// durable write untorn.
func (r *Runner) fenceAndWait(run *activeRun) {
	if run.guard.tryFence() {
		run.cancel()
	}
	done := run.done
	r.mu.Unlock()
	<-done
}

// Remove forgets a job entirely: it cancels the run if that job is currently
// executing (blocking until the goroutine exits, honoring the commit fence) and
// then deletes the record. It is the "the subject is gone" operation — a
// workspace was removed, so its coalesced job should leave nothing behind.
//
// There is a narrow, data-safe window: between Cancel returning and the delete,
// dispatch may claim a still-queued record and begin running it; the delete then
// removes the row mid-run and finish() reloads, finds the record gone, and
// discards its result.
func (r *Runner) Remove(id string) {
	if r.disabled {
		return
	}
	r.Cancel(id)
	r.ioMu.Lock()
	err := r.store.Delete(id)
	r.ioMu.Unlock()
	if err != nil {
		r.log("jobs: remove %s: %v", id, err)
		return
	}
	r.notifyChange(id)
}

// RemoveByKey is Remove addressed by what the job is about rather than by its
// generated id. It is the surface a caller uses when a subject disappears, since
// nothing outside the queue knows a coalescing job's id.
func (r *Runner) RemoveByKey(kind, uniqueKey string) {
	if r.disabled || uniqueKey == "" {
		return
	}
	r.ioMu.Lock()
	existing, err := r.store.LoadByKey(kind, uniqueKey)
	r.ioMu.Unlock()
	if err != nil {
		r.log("jobs: remove %s/%s: %v", kind, uniqueKey, err)
		return
	}
	if existing == nil {
		return
	}
	r.Remove(existing.ID)
}

// List returns every persisted job, newest-updated first. Safe (returns nil) on
// a disabled runner.
func (r *Runner) List() ([]*Job, error) {
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

// Get returns a single job by id, or nil if absent. Safe on a disabled runner.
func (r *Runner) Get(id string) (*Job, error) {
	if r.disabled {
		return nil, nil
	}
	return r.store.Load(id)
}

// GetByKey returns a coalescing job by kind+key, or nil if absent. Safe on a
// disabled runner.
func (r *Runner) GetByKey(kind, uniqueKey string) (*Job, error) {
	if r.disabled || uniqueKey == "" {
		return nil, nil
	}
	return r.store.LoadByKey(kind, uniqueKey)
}

// --- dispatch loop ---------------------------------------------------------

// loop is the single dispatch goroutine. It is level-triggered: each pass claims
// and launches every currently-eligible job whose kind has a free concurrency
// slot, draining until a pass can place nothing more, then waits for the next
// nudge or poll tick. Handlers run concurrently; the loop never blocks on one.
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
				r.log("jobs: dispatch pass: %v", err)
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

// dispatch claims every currently-eligible job whose kind is under its per-kind
// concurrency cap and launches each in its own goroutine. It reports whether it
// made progress (launched a run, or failed an unknown-kind job in place) so the
// loop keeps draining until a pass can place nothing more.
//
// The store read and every per-job claim write run under ioMu, so a concurrent
// Enqueue/Retry cannot make a chosen record stale between selection and claim.
// The per-kind in-flight accounting and the active-run registry live under mu,
// which is only ever taken WHILE holding ioMu — preserving the ioMu-outer lock
// order. Handlers are launched only AFTER both locks are released.
func (r *Runner) dispatch() (progressed bool, err error) {
	r.ioMu.Lock()

	now := r.now()
	eligible, err := r.store.Eligible(now, eligiblePageSize)
	if err != nil {
		r.ioMu.Unlock()
		return false, err
	}
	if len(eligible) >= eligiblePageSize {
		// A full page means the queue is deeper than any healthy case. Say so: the
		// page IS truncated, and a caller staring at a job that never runs needs to
		// know the reason is backlog, not a lost enqueue.
		r.log("jobs: eligible page full at limit %d; the queue is backlogged and this pass sees only the first %d",
			eligiblePageSize, eligiblePageSize)
	}

	type launchSpec struct {
		job  *Job
		exec handler
		ctx  context.Context
		run  *activeRun
	}
	var launch []launchSpec
	var deadJobs []*Job
	var failedUnknownIDs []string

	for _, j := range eligible {
		if !r.runnable(j) {
			continue
		}
		// Decide + reserve under mu: the handler registry and in-flight counts both
		// live there. Reserving the slot before persisting the claim keeps a later
		// candidate of the same kind in this very pass from over-committing the cap.
		r.mu.Lock()
		exec, ok := r.handlers[j.Kind]
		if !ok {
			r.mu.Unlock()
			// No handler for this kind (e.g. a stale record from an old build). Kill
			// it outright rather than sending it around the retry schedule.
			r.recordPermanentFailureLocked(j, fmt.Errorf("%w: %s", ErrUnknownKind, j.Kind))
			deadJobs = append(deadJobs, j)
			failedUnknownIDs = append(failedUnknownIDs, j.ID)
			continue
		}
		if r.inflight[j.Kind] >= exec.limit {
			r.mu.Unlock()
			continue // this kind is saturated; a finishing run will re-nudge us
		}
		ctx, cancel := context.WithCancel(context.Background())
		run := &activeRun{
			id:     j.ID,
			kind:   j.Kind,
			cancel: cancel,
			guard:  &CommitGuard{},
			done:   make(chan struct{}),
		}
		r.inflight[j.Kind]++
		r.runs[j.ID] = run
		r.mu.Unlock()

		// Persist the claim (state running) under ioMu — NOT under mu, which must
		// never wrap store I/O.
		j.State = StateRunning
		j.Attempts++
		j.Requeued = false
		j.UpdatedAt = now
		if err := r.store.Save(j); err != nil {
			r.log("jobs: persist running state for %s: %v", j.ID, err)
			// Roll the reservation back so the per-kind slot is not leaked forever.
			r.mu.Lock()
			delete(r.runs, j.ID)
			r.inflight[j.Kind]--
			r.mu.Unlock()
			cancel()
			continue
		}
		launch = append(launch, launchSpec{job: j, exec: exec, ctx: ctx, run: run})
	}

	r.ioMu.Unlock()

	for _, id := range failedUnknownIDs {
		r.notifyChange(id)
	}
	for _, ls := range launch {
		r.notifyChange(ls.job.ID)
	}
	for _, dj := range deadJobs {
		r.notifyTerminalFailure(dj)
	}
	for _, ls := range launch {
		go r.execute(ls.job, ls.exec, ls.ctx, ls.run)
	}
	return len(failedUnknownIDs) > 0 || len(launch) > 0, nil
}

// runnable re-checks the attempt cap on a job the store offered as eligible. The
// store cannot apply it: the cap may be the runner's default rather than the
// job's own. A failed job at or past its cap should already be dead, so this is
// the guard for a record whose MaxAttempts shrank under it.
func (r *Runner) runnable(j *Job) bool {
	if j.State != StateFailed {
		return true
	}
	return j.Attempts < r.attemptCap(j)
}

// attemptCap is the job's own cap when it set one, otherwise the runner default.
func (r *Runner) attemptCap(j *Job) int {
	if j.MaxAttempts > 0 {
		return j.MaxAttempts
	}
	return r.maxAttempts
}

// execute runs one already-claimed job (state == running in the store) through
// its handler inside a runner-owned timeout, then records the outcome (done /
// requeued / failed-with-backoff / dead) under ioMu. The ctx, cancel, guard, and
// activeRun are all built by dispatch at claim time and the run is already
// registered in r.runs, so a Cancel arriving before this goroutine is scheduled
// still finds and fences it. The CommitGuard fences the handler's durable write
// against a concurrent Cancel AND against the runner-owned timeout: once the
// handler has entered its commit, neither cancels the context, so the single
// durable write is never torn.
func (r *Runner) execute(j *Job, exec handler, ctx context.Context, run *activeRun) {
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
			// The deadline elapsed. Honor the commit fence: if the handler is already
			// committing, do NOT cancel — let the durable write finish untorn (this
			// goroutine still waits for the handler to return).
			if guard.tryFence() {
				cancel()
			}
		case <-timeoutStop:
		}
	}()

	// Attach the guard so the handler body can fence its durable write.
	jobForHandler := j.clone()
	jobForHandler.CommitGuard = guard

	result, runErr := func() (result any, err error) {
		defer func() {
			if rec := recover(); rec != nil {
				result = nil
				err = fmt.Errorf("jobs: handler panic: %v", rec)
			}
		}()
		return exec.fn(ctx, jobForHandler)
	}()

	var encoded json.RawMessage
	if runErr == nil {
		encoded, runErr = marshalPayload(result)
		if runErr != nil {
			runErr = fmt.Errorf("jobs: encode result for %s (%s): %w", j.ID, j.Kind, runErr)
		}
	}

	close(timeoutStop)
	cancel()

	// Record the terminal outcome BEFORE signaling exit, so Cancel's
	// blocks-until-exit contract holds: when Cancel's <-run.done unblocks, the
	// goroutine has fully exited AND the durable terminal record is already
	// written.
	r.finish(j.ID, encoded, runErr)

	// Deregister the run and free its per-kind slot, THEN signal exit. From here a
	// Cancel that was already blocked on run.done unblocks; a Cancel arriving
	// after this finds no entry for the id and returns immediately.
	r.mu.Lock()
	delete(r.runs, run.id)
	r.inflight[run.kind]--
	r.mu.Unlock()
	close(run.done)

	// A freed slot may admit a queued job of a now-uncapped kind; re-dispatch.
	r.nudge()
}

// finish records the terminal outcome of a run. It re-loads the record under
// ioMu (a mid-run Enqueue may have flipped Requeued / pushed ScheduledAt) so it
// honors any coalesced trigger that landed during the run rather than clobbering
// it. The stored record is authoritative for everything except the run result.
func (r *Runner) finish(id string, result json.RawMessage, runErr error) {
	r.ioMu.Lock()

	cur, err := r.store.Load(id)
	if err != nil {
		r.ioMu.Unlock()
		r.log("jobs: reload %s before finish: %v", id, err)
		return
	}
	if cur == nil {
		// The record was deleted out from under the run; nothing to persist.
		r.ioMu.Unlock()
		return
	}

	requeue := false
	wentDead := false
	if runErr != nil {
		if cur.Requeued {
			// A re-enqueue arrived mid-run. Even though this run failed, honoring the
			// explicit demand takes precedence over backoff: re-queue at the
			// ScheduledAt that Enqueue set (now, for RunNow) instead of demoting a
			// run-now demand to a backoff delay. Attempts reset because the re-enqueue
			// is fresh logical demand, not a continuation of the failed run.
			now := r.now()
			cur.State = StateQueued
			cur.Requeued = false
			cur.Attempts = 0
			cur.LastError = runErr.Error()
			cur.UpdatedAt = now
			if err := r.store.Save(cur); err != nil {
				r.ioMu.Unlock()
				r.log("jobs: persist requeued-after-failure state for %s: %v", id, err)
				return
			}
			requeue = true
		} else {
			wentDead = r.recordFailureLocked(cur, runErr)
		}
	} else {
		now := r.now()
		cur.LastError = ""
		cur.Result = result
		cur.UpdatedAt = now
		if cur.Requeued {
			// A re-enqueue arrived mid-run: honor it by re-queuing instead of marking
			// done, so the coalesced trigger is not lost. ScheduledAt was set by that
			// Enqueue; preserve it. Attempts reset (fresh logical work).
			cur.State = StateQueued
			cur.Requeued = false
			cur.Attempts = 0
			requeue = true
		} else {
			cur.State = StateDone
		}
		if err := r.store.Save(cur); err != nil {
			r.ioMu.Unlock()
			r.log("jobs: persist done state for %s: %v", id, err)
			return
		}
	}
	r.ioMu.Unlock()

	r.notifyChange(id)
	if wentDead {
		r.notifyTerminalFailure(cur)
	}
	if requeue {
		r.nudge()
	}
}

// recordFailureLocked persists a failed outcome with capped-exponential backoff,
// or dead once the attempt cap is reached. The caller holds ioMu. It reports
// whether this transition crossed into StateDead so the caller can fire the
// terminal-failure hook AFTER releasing ioMu (never under it).
func (r *Runner) recordFailureLocked(j *Job, cause error) (wentDead bool) {
	now := r.now()
	limit := r.attemptCap(j)
	j.LastError = cause.Error()
	j.UpdatedAt = now
	if j.Attempts >= limit {
		j.State = StateDead
		j.ScheduledAt = now
		wentDead = true
		r.log("jobs: %s (%s) dead after %d attempts: %v", j.ID, j.Kind, j.Attempts, cause)
	} else {
		j.State = StateFailed
		j.ScheduledAt = now.Add(r.backoff(j.Attempts))
		r.log("jobs: %s (%s) failed (attempt %d/%d), retry at %s: %v",
			j.ID, j.Kind, j.Attempts, limit, j.ScheduledAt.Format(time.RFC3339), cause)
	}
	if err := r.store.Save(j); err != nil {
		r.log("jobs: persist failure state for %s: %v", j.ID, err)
	}
	return wentDead
}

// recordPermanentFailureLocked marks a job dead immediately, without spending an
// attempt or scheduling a retry. The caller holds ioMu.
//
// Some failures cannot be retried into success: a job whose kind this binary has
// no handler for fails identically on every pass. Sending those around the
// backoff schedule is worse than useless — a job that is never claimed never
// increments Attempts, so it never reaches the attempt cap, and it re-fails and
// re-logs the same error on every backoff for as long as the daemon lives. Dying
// once, loudly, with the kind named is the actionable outcome.
func (r *Runner) recordPermanentFailureLocked(j *Job, cause error) {
	now := r.now()
	j.State = StateDead
	j.LastError = cause.Error()
	j.ScheduledAt = now
	j.UpdatedAt = now
	r.log("jobs: %s (%s) dead, cannot be retried: %v", j.ID, j.Kind, cause)
	if err := r.store.Save(j); err != nil {
		r.log("jobs: persist permanent failure for %s: %v", j.ID, err)
	}
}

// notifyTerminalFailure invokes the terminal-failure hook (if set) with a cloned
// record. Mirrors notifyChange: it snapshots the callback under mu and calls it
// WITHOUT holding mu or ioMu, so the callback may touch the store freely.
func (r *Runner) notifyTerminalFailure(j *Job) {
	r.mu.Lock()
	fn := r.onTerminalFailure
	r.mu.Unlock()
	if fn != nil {
		fn(j.clone())
	}
}

// backoff returns the capped-exponential delay for the given attempt number
// (1-based): base * 2^(attempt-1), clamped to cap.
func (r *Runner) backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := r.backoffBase
	for i := 1; i < attempt; i++ {
		d *= 2
		// `d <= 0` guards int64 overflow: with a near-MaxInt64 cap and a large
		// attempt count the doubling can wrap negative before reaching the cap,
		// which would yield a past ScheduledAt and a hot retry loop.
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

// --- retention -------------------------------------------------------------

// retentionLoop trims completed jobs on an interval until done is closed.
func (r *Runner) retentionLoop(done <-chan struct{}) {
	ticker := time.NewTicker(r.trimInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			r.Trim()
		}
	}
}

// Trim runs one retention pass and reports how many completed jobs were removed.
// It is exported so a test (or an operator surface) can run retention on demand
// instead of waiting for the interval.
func (r *Runner) Trim() int {
	if r.disabled {
		return 0
	}
	r.ioMu.Lock()
	n, err := r.store.TrimDone(r.now().Add(-r.retention))
	r.ioMu.Unlock()
	if err != nil {
		r.log("jobs: retention pass failed: %v", err)
		return 0
	}
	if n > 0 {
		r.log("jobs: trimmed %d completed job(s) older than %s", n, r.retention)
	}
	return n
}

// --- helpers ---------------------------------------------------------------

func (r *Runner) nudge() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// notifyChange reports one job's lifecycle transition. The id is required: a
// consumer that is only told "something changed" has to re-list to find out
// what, which is the whole problem the event bus behind this callback removes.
func (r *Runner) notifyChange(jobID string) {
	r.mu.Lock()
	fn := r.onChange
	r.mu.Unlock()
	if fn != nil {
		fn(jobID)
	}
}

// marshalPayload encodes a payload or result. A nil value encodes to nil rather
// than the four bytes "null", so "carries nothing" and "carries the JSON null"
// stay distinguishable — Enqueue relies on nil meaning "leave what is there".
func marshalPayload(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	if raw, ok := v.(json.RawMessage); ok {
		if len(raw) == 0 {
			return nil, nil
		}
		return cloneBytes(raw), nil
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return encoded, nil
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
