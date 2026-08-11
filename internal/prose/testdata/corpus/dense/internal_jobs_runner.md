Tuning defaults. Each is overridable per-runner through the matching Options
field (a zero value falls back to the default, resolved in New).

DefaultMaxAttempts is the attempt cap: a job that has failed this many
times goes dead instead of auto-requeuing. A job may override it.

DefaultBackoffBase / DefaultBackoffCap bound the capped-exponential backoff
schedule (1m, 2m, 4m, … capped at 1h).

DefaultHandlerTimeout is the per-handler context.WithTimeout the runner
wraps around every invocation. A kind can override it via RegisterWith.

DefaultRetention is how long a successfully completed job is kept before
trimming, mirroring bus.DefaultRetention. Only done jobs are trimmed:
a dead job is the actionable record a failure notification points at, it
only exists when nobody has acted on it, and it is not what grows.

defaultPollInterval is how often the dispatch loop wakes to re-scan for a
job whose ScheduledAt has arrived. The loop is level-triggered, so this only
bounds scheduling latency for time-gated requeues.

eligiblePageSize bounds one dispatch pass's read of claimable jobs, so a
pathological queue cannot pull an unbounded result set into memory every
second. Receipt: the real task tables this queue replaces held 146 rows
(~/.attn) and 206 rows (~/.attn-dev), all but a handful of them terminal and
therefore never eligible; the eligible working set is single digits. A
thousand is three orders of magnitude past that, so only something broken
reaches it — and dispatch says so out loud when it does rather than quietly
serving a truncated page.

ErrDisabled is returned by mutating runner methods when the runner was
constructed without a store. A disabled runner never starts a dispatch loop
and persists nothing; it exists so a consumer can hold a non-nil Runner before
the real one is built.

ErrUnknownKind is returned by Enqueue when no handler is registered for the
job's kind.

HandlerFunc runs one job to completion. It returns the job's result (nil when
the work produces no value) or an error, in which case the job goes failed and
may auto-requeue. The runner owns the context.WithTimeout wrapper around the
invocation; the handler body owns the work and calls job.CommitGuard.Enter and
Leave around its single durable write.

A returned result must be JSON-marshallable. If it is not, the run is recorded
as FAILED with the marshal error: a result nobody can read is not a success,
and dropping it quietly would be exactly the silent truncation this queue
exists to avoid.

handler pairs a registered HandlerFunc with its per-kind timeout and
concurrency cap.

limit is the max number of jobs OF THIS KIND that may run at once. It is
always >= 1 after registration, so a kind is serialized with itself by
default while different kinds run in parallel.

interval is the recurrence for a cron kind (see cron.go) and zero for
every other kind, which is what tells finish() to re-arm rather than
retire the record.

HandlerConfig tunes a registered handler. The zero value is the default:
DefaultHandlerTimeout and a per-kind concurrency cap of 1.

Timeout is the per-invocation context.WithTimeout the runner wraps around
every call to this handler. Zero ⇒ DefaultHandlerTimeout.

MaxConcurrent bounds how many jobs of this kind may run simultaneously.
Zero ⇒ 1 (a kind serialized with itself); different kinds always run in
parallel regardless.

UniqueKey opts this job into coalescing within its kind (see Job.UniqueKey).
Leaving it empty enqueues a distinct job every time.

Payload is the run's inputs, marshalled to JSON. Nil enqueues no payload.

Delay pushes ScheduledAt forward from now. For a coalescing job this is the
debounce knob: re-enqueueing within the window keeps pushing the run later,
collapsing a burst of triggers into one run. Zero means "run as soon as
eligible".

RunNow forces ScheduledAt = now regardless of Delay, overriding a debounce
already pending on a coalescing record. This is how a caller says "the
window is over, run it".

MaxAttempts overrides the runner's attempt cap for this job. Zero ⇒ the
runner default.

Log receives runtime log lines. A nil Log is replaced with a no-op.

Now injects the clock for deterministic backoff/coalescing tests. nil ⇒
time.Now. The runner always normalizes to UTC.

PollInterval overrides the dispatch loop's re-scan interval (tests use a
short interval to avoid real-time waits). Zero ⇒ defaultPollInterval.

Retention / TrimInterval override the done-job retention window and how
often it runs. Zero ⇒ the defaults.

Runner is the durable job queue. A single dispatch loop selects eligible jobs
and launches each as its own goroutine, bounded by a per-kind concurrency cap
(default 1). Every record read-modify-write funnels through ioMu, so
persistence stays serialized even though handlers run concurrently.

onChange, if set, fires after every lifecycle transition. It may be called
CONCURRENTLY — from the dispatch goroutine, from each in-flight run's
finish(), and from Enqueue/Retry/Remove on arbitrary caller goroutines — so
it must be cheap, non-blocking, and safe to invoke from multiple goroutines.

onTerminalFailure, if set, fires exactly once when a job crosses into the
terminal StateDead (retries exhausted) — the actionable "this background
work gave up" signal. Like onChange it may be invoked from several
goroutines. It always runs AFTER ioMu is released with a cloned record, so
the callback may touch the store without lock-ordering risk.

ioMu serializes every read-modify-write cycle on a job record. The dispatch
loop, in-flight runs' finish(), and arbitrary callers' Enqueue/Retry all
mutate the same records; without this a concurrent Enqueue and a
claim/finish write would lost-update each other. When both locks are needed
ioMu is ALWAYS the outer lock: it is acquired without holding mu, so there
is no lock-ordering hazard.

runs holds every in-flight run keyed by job id. Cancel(id) fences and waits
on its entry; Stop drains them all. Guarded by mu.

inflight counts currently-running jobs per kind. dispatch reads it to
enforce each kind's concurrency cap; it is bumped when a run is claimed and
dropped when the run goroutine exits. Guarded by mu.

wake nudges the dispatch loop to re-scan immediately after an Enqueue or
Retry rather than waiting for the next poll tick. Buffered depth 1 so a
nudge never blocks the caller.

lockToken is the single-instance ownership marker acquired by Start and
released by Stop. Empty when not started.

activeRun tracks one in-flight job so Cancel can fence its commit and block
until its goroutine exits.

so the run goroutine can drop its per-kind in-flight slot on exit

New constructs a Runner. With a nil Store the runner is disabled: mutating
methods are safe no-ops returning ErrDisabled, Start/Stop do nothing, and
nothing is persisted.

OnChange registers a callback fired after every lifecycle transition. It is
optional and must be cheap; pass nil to clear.

OnTerminalFailure registers a callback fired once when a job reaches StateDead
(retries exhausted). It is optional and must be cheap and concurrency-safe;
pass nil to clear.

Register wires a handler for a kind with the default timeout and a
concurrency cap of 1.

RegisterWith wires a handler for a kind with an explicit timeout and per-kind
concurrency cap (see HandlerConfig). It is an error to register the same kind
twice or to pass a nil fn.

Start recovers orphaned running jobs (reset to queued) and launches the
dispatch and retention loops. It is safe (no-op) on a disabled runner. Calling
Start twice is an error.

Take exclusive ownership before doing anything else: a second live Runner on
the same store would double-execute every job (its own dispatch loop, its
own in-memory CommitGuard). Failing fast beats silently double-applying
durable writes.

recoverOrphans is a read-modify-write over the store, so it holds ioMu like
every other RMW path. It runs before the loops launch, so contention is nil.

Arm the recurring entries before the loop starts so a cron kind registered
on a fresh store has its first fire scheduled, not waiting on a trigger
that will never come.

Stop signals the loops to exit, then cancels and drains every in-flight run.
cancelAll honors each commit fence, so an already-committing run still
finishes its durable write untorn. Safe to call on a disabled or never-started
runner.

Flip started to false WHILE holding mu so a second concurrent Stop sees
started==false and returns instead of double-closing done (which panics and
would take the whole daemon down). Only the Stop that wins this transition
closes done and waits on exit.

Order matters: runs are detached goroutines the dispatch loop does not join.
First stop the loop launching MORE runs, THEN cancel and join everything
still in flight, so cancelAll is guaranteed to terminate.

Release ownership only after every run has exited, so no other Runner can
claim the store while ours is still draining.

Enqueue persists a job for kind. With opts.UniqueKey set it coalesces onto the
existing record for that kind+key: a queued/failed/dead/done record is reset to
queued with a fresh schedule, and a record that is currently RUNNING is left to
finish with its Requeued flag set so the coalesced trigger re-runs it rather
than being lost. Without a unique key it always creates a new record.

A cron kind has exactly one record, addressed by CronKey, and finish() sends
every record of that kind back around the recurrence. An enqueue carrying any
other key would therefore mint a SECOND self-perpetuating entry — one that
List hides and CronEntry never finds. Refuse instead: recurring work is
scheduled by registering it, not by enqueueing it.

Priority, MaxAttempts, and Payload are applied below, in the one place
that also handles a coalescing re-enqueue carrying fresh values.

The job is running right now. Overwriting it would tear the in-flight
run's bookkeeping, so instead record the demand: mark Requeued and push
ScheduledAt to the requested time. When the run finishes, finish() sees
Requeued and returns the job to queued instead of done, so this coalesced
trigger is honored rather than lost.

queued / failed / dead / done: coalesce by resetting the same record back
to queued with a fresh schedule. Attempts reset because a new enqueue is
new logical demand, not a continuation of the old failure run.

Apply the carried inputs after the branch settled on `job`, so they land the
same way on a brand-new record, a mid-run requeue (so the re-run reads the
fresh inputs), and a coalescing re-enqueue. A nil Payload LEAVES an existing
payload intact: a bare re-trigger that carries no inputs must not wipe the
ones a prior enqueue stashed.

Retry forces a failed or dead job back to queued with ScheduledAt = now. It is
the manual-recovery action. A job that is queued/running/done is left as-is.
Returns the updated job, or nil if no such job exists.

Cancel signals the running handler for id (if it is the one running) and DOES
NOT RETURN until that job's goroutine has fully exited. If the run is already
inside its commit fence, Cancel does not cancel the context — it waits for the
goroutine to finish its durable write and exit, so the write is never torn.
Cancel of a job that is not currently running returns immediately.

fenceAndWait performs the shared cancel-and-block core. The CALLER must hold
r.mu and pass a non-nil run; fenceAndWait RELEASES r.mu before it blocks on the
done channel. It cancels the run's context only if the run has not yet entered
its commit fence — if the run is already committing it leaves the context alone
and just waits, so the blocks-until-exit contract finishes an in-progress
durable write untorn.

Remove forgets a job entirely: it cancels the run if that job is currently
executing (blocking until the goroutine exits, honoring the commit fence) and
then deletes the record. It is the "the subject is gone" operation — a
workspace was removed, so its coalesced job should leave nothing behind.

There is a narrow, data-safe window: between Cancel returning and the delete,
dispatch may claim a still-queued record and begin running it; the delete then
removes the row mid-run and finish() reloads, finds the record gone, and
discards its result.

RemoveByKey is Remove addressed by what the job is about rather than by its
generated id. It is the surface a caller uses when a subject disappears, since
nothing outside the queue knows a coalescing job's id.

List returns the work queue — every job something enqueued because it needed
doing — newest-updated first. Safe (returns nil) on a disabled runner.

Cron entries are excluded. They are the queue's own scheduler rather than work
anyone is owed: each is permanently queued for its next fire, so listing them
alongside real jobs would put two rows that never resolve at the top of every
"what is the daemon working on" answer. Read one with CronEntry.

Get returns a single job by id, or nil if absent. Safe on a disabled runner.

GetByKey returns a coalescing job by kind+key, or nil if absent. Safe on a
disabled runner.

loop is the single dispatch goroutine. It is level-triggered: each pass claims
and launches every currently-eligible job whose kind has a free concurrency
slot, draining until a pass can place nothing more, then waits for the next
nudge or poll tick. Handlers run concurrently; the loop never blocks on one.

Drain: keep dispatching until a pass launches nothing (queue empty or
every eligible kind saturated), so a burst of enqueues does not stall
behind the poll interval.

dispatch claims every currently-eligible job whose kind is under its per-kind
concurrency cap and launches each in its own goroutine. It reports whether it
made progress (launched a run, or failed an unknown-kind job in place) so the
loop keeps draining until a pass can place nothing more.

The store read and every per-job claim write run under ioMu, so a concurrent
Enqueue/Retry cannot make a chosen record stale between selection and claim.
The per-kind in-flight accounting and the active-run registry live under mu,
which is only ever taken WHILE holding ioMu — preserving the ioMu-outer lock
order. Handlers are launched only AFTER both locks are released.

A full page means the queue is deeper than any healthy case. Say so: the
page IS truncated, and a caller staring at a job that never runs needs to
know the reason is backlog, not a lost enqueue.

Decide + reserve under mu: the handler registry and in-flight counts both
live there. Reserving the slot before persisting the claim keeps a later
candidate of the same kind in this very pass from over-committing the cap.

No handler for this kind (e.g. a stale record from an old build). Kill
it outright rather than sending it around the retry schedule.

Persist the claim (state running) under ioMu — NOT under mu, which must
never wrap store I/O.

Roll the reservation back so the per-kind slot is not leaked forever.

runnable re-checks the attempt cap on a job the store offered as eligible. The
store cannot apply it: the cap may be the runner's default rather than the
job's own. A failed job at or past its cap should already be dead, so this is
the guard for a record whose MaxAttempts shrank under it.

attemptCap is the job's own cap when it set one, otherwise the runner default.

execute runs one already-claimed job (state == running in the store) through
its handler inside a runner-owned timeout, then records the outcome (done /
requeued / failed-with-backoff / dead) under ioMu. The ctx, cancel, guard, and
activeRun are all built by dispatch at claim time and the run is already
registered in r.runs, so a Cancel arriving before this goroutine is scheduled
still finds and fences it. The CommitGuard fences the handler's durable write
against a concurrent Cancel AND against the runner-owned timeout: once the
handler has entered its commit, neither cancels the context, so the single
durable write is never torn.

timeoutStop stops the deadline timer once the run has exited so it cannot
fire (and consult the guard) after teardown.

The deadline elapsed. Honor the commit fence: if the handler is already
committing, do NOT cancel — let the durable write finish untorn (this
goroutine still waits for the handler to return).

Attach the guard so the handler body can fence its durable write.

Record the terminal outcome BEFORE signaling exit, so Cancel's
blocks-until-exit contract holds: when Cancel's <-run.done unblocks, the
goroutine has fully exited AND the durable terminal record is already
written.

Deregister the run and free its per-kind slot, THEN signal exit. From here a
Cancel that was already blocked on run.done unblocks; a Cancel arriving
after this finds no entry for the id and returns immediately.

A freed slot may admit a queued job of a now-uncapped kind; re-dispatch.

finish records the terminal outcome of a run. It re-loads the record under
ioMu (a mid-run Enqueue may have flipped Requeued / pushed ScheduledAt) so it
honors any coalesced trigger that landed during the run rather than clobbering
it. The stored record is authoritative for everything except the run result.

The record was deleted out from under the run; nothing to persist.

A cron kind's record is a recurring entry, not a unit of work that
completes: it goes back to queued for its next fire whatever happened here,
including a failure (see RegisterCron).

A re-enqueue arrived mid-run. Even though this run failed, honoring the
explicit demand takes precedence over backoff: re-queue at the
ScheduledAt that Enqueue set (now, for RunNow) instead of demoting a
run-now demand to a backoff delay. Attempts reset because the re-enqueue
is fresh logical demand, not a continuation of the failed run.

A re-enqueue arrived mid-run: honor it by re-queuing instead of marking
done, so the coalesced trigger is not lost. ScheduledAt was set by that
Enqueue; preserve it. Attempts reset (fresh logical work).

recordFailureLocked persists a failed outcome with capped-exponential backoff,
or dead once the attempt cap is reached. The caller holds ioMu. It reports
whether this transition crossed into StateDead so the caller can fire the
terminal-failure hook AFTER releasing ioMu (never under it).

recordPermanentFailureLocked marks a job dead immediately, without spending an
attempt or scheduling a retry. The caller holds ioMu.

Some failures cannot be retried into success: a job whose kind this binary has
no handler for fails identically on every pass. Sending those around the
backoff schedule is worse than useless — a job that is never claimed never
increments Attempts, so it never reaches the attempt cap, and it re-fails and
re-logs the same error on every backoff for as long as the daemon lives. Dying
once, loudly, with the kind named is the actionable outcome.

notifyTerminalFailure invokes the terminal-failure hook (if set) with a cloned
record. Mirrors notifyChange: it snapshots the callback under mu and calls it
WITHOUT holding mu or ioMu, so the callback may touch the store freely.

backoff returns the capped-exponential delay for the given attempt number
(1-based): base * 2^(attempt-1), clamped to cap.

`d <= 0` guards int64 overflow: with a near-MaxInt64 cap and a large
attempt count the doubling can wrap negative before reaching the cap,
which would yield a past ScheduledAt and a hot retry loop.

cancelAll cancels every in-flight run (honoring each commit fence) and blocks
until they have all exited. Used by Stop AFTER the dispatch loop has exited, so
no new run can be registered while it drains. It snapshots r.runs under mu and
then fences+waits without holding mu: a run that finishes between the snapshot
and the fence closes its done channel (tryFence on a settled guard is a safe
no-op) so <-run.done returns immediately.

Trim runs one retention pass and reports how many completed jobs were removed.
It is exported so a test (or an operator surface) can run retention on demand
instead of waiting for the interval.

notifyWorkChange reports a transition on the two paths a cron entry travels —
its arming and each fire — and drops it for cron kinds. A heartbeat beating
every minute forever is not news, and routing it through the change hook would
append a durable event and re-push a snapshot per minute per entry for as long
as the daemon lives. Every other transition (retry, cancel, removal) is an
action someone took, so those report through notifyChange even for a cron
entry.

notifyChange reports one job's lifecycle transition. The id is required: a
consumer that is only told "something changed" has to re-list to find out
what, which is the whole problem the event bus behind this callback removes.

marshalPayload encodes a payload or result. A nil value encodes to nil rather
than the four bytes "null", so "carries nothing" and "carries the JSON null"
stay distinguishable — Enqueue relies on nil meaning "leave what is there".
