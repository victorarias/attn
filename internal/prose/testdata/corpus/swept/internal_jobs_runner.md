DefaultRetention mirrors bus.DefaultRetention. Only done jobs are trimmed:
a dead job is the actionable record a failure notification points at.

eligiblePageSize bounds one dispatch pass's read of claimable jobs. Measured:
the task tables this queue replaced held 146 rows (~/.attn) and 206 rows
(~/.attn-dev), nearly all terminal; the eligible working set is single digits.
A tripwire three orders of magnitude past that; dispatch logs loudly when hit.

ErrDisabled is returned by mutating methods on a runner constructed without a
store; such a runner runs no loops and persists nothing.

ErrUnknownKind is returned by Enqueue when no handler is registered for the kind.

HandlerFunc runs one job to completion. The handler calls
job.CommitGuard.Enter/Leave around its single durable write. A result that is
not JSON-marshallable records the run as FAILED with the marshal error.

interval is the recurrence for a cron kind (cron.go), zero otherwise;
non-zero tells finish() to re-arm rather than retire the record.

HandlerConfig tunes a registered handler; the zero value means
DefaultHandlerTimeout and a per-kind concurrency cap of 1.

MaxConcurrent caps concurrent jobs of this kind; different kinds always
run in parallel regardless.

Delay pushes ScheduledAt forward from now; on a coalescing job it is the
debounce knob, each re-enqueue pushing the run later.

Runner is the durable job queue: one dispatch loop launching eligible jobs
under per-kind concurrency caps, with every record read-modify-write
serialized through ioMu.

onChange fires after every lifecycle transition, possibly concurrently;
it must be cheap and non-blocking.

onTerminalFailure fires once when a job reaches StateDead, always AFTER
ioMu is released and with a cloned record, so it may touch the store.

ioMu serializes every read-modify-write on a job record; without it a
concurrent Enqueue and a claim/finish write would lost-update each other.
When both locks are needed ioMu is ALWAYS the outer lock.

wake nudges the dispatch loop to re-scan; buffered depth 1 so a nudge
never blocks the caller.

activeRun tracks one in-flight job so Cancel can fence its commit and block
until its goroutine exits.

so the run goroutine can drop its per-kind in-flight slot on exit

New constructs a Runner. With a nil Store the runner is disabled: mutating
methods are no-ops returning ErrDisabled and nothing is persisted.

OnTerminalFailure registers a callback fired once when a job reaches
StateDead; it must be concurrency-safe, and nil clears it.

Register wires a handler with the default timeout and a concurrency cap of 1.

RegisterWith wires a handler with an explicit HandlerConfig; registering a
kind twice or passing a nil fn is an error.

Start recovers orphaned running jobs and launches the dispatch and retention
loops; a no-op when disabled, an error when called twice.

Exclusive ownership first: a second live Runner on the same store would
double-execute every job.

Stop signals the loops to exit, then cancels and drains every in-flight run,
honoring each commit fence so a committing run still writes untorn.

Flip under mu so a concurrent second Stop returns instead of double-closing done.

Order matters: stop the loop launching MORE runs first, then cancel and
join what is in flight, so cancelAll terminates.

Enqueue persists a job for kind. With opts.UniqueKey set it coalesces onto
the existing kind+key record (a RUNNING one gets Requeued instead of being
overwritten); without a key it always creates a new record.

Any other key on a cron kind mints a second self-perpetuating entry that
List hides and CronEntry never finds.

Overwriting an in-flight run tears its bookkeeping; Requeued makes
finish() re-queue instead of retiring.

A nil Payload LEAVES an existing payload intact: a bare re-trigger must
not wipe the inputs a prior enqueue stashed.

Retry forces a failed or dead job back to queued with ScheduledAt = now; any
other state is left as-is.

Cancel signals the running handler for id and DOES NOT RETURN until its
goroutine has fully exited. A run already inside its commit fence is not
canceled — Cancel waits for the durable write to finish untorn.

fenceAndWait cancels (unless already committing) and blocks until the run
exits. CALLER must hold r.mu; fenceAndWait RELEASES it before blocking.

Remove cancels the run if executing (blocking, honoring the commit fence) and
deletes the record. Between Cancel and the delete dispatch may re-claim it;
finish() then reloads, finds it gone, and discards the result.

RemoveByKey is Remove addressed by kind+key, since nothing outside the queue
knows a coalescing job's id.

List returns the work queue newest-updated first. Cron entries are excluded —
each is permanently queued for its next fire and would sit unresolvable at the
top forever; read one with CronEntry.

loop is the single dispatch goroutine, level-triggered: each pass drains every
eligible job with a free slot, then waits for a nudge or poll tick.

dispatch claims every eligible job under its per-kind cap and launches each in
its own goroutine, reporting whether it made progress. Selection and claim
writes hold ioMu (mu taken only inside it, ioMu-outer order); handlers launch
only AFTER both locks are released.

Reserve the slot under mu before persisting the claim, so a later
same-kind candidate in this pass cannot over-commit the cap.

Persist the claim under ioMu — never wrap store I/O in mu.

runnable re-checks the attempt cap the store cannot apply (it may be the
runner default), guarding a record whose MaxAttempts shrank under it.

attemptCap is the job's own cap when it set one, otherwise the runner default.

execute runs one already-claimed job through its handler inside a runner-owned
timeout, then records the outcome under ioMu. The run is registered in r.runs
before this goroutine is scheduled, so an early Cancel still fences it; the
CommitGuard fences the durable write against Cancel AND the timeout.

Record the terminal outcome BEFORE signaling exit: when Cancel's
<-run.done unblocks, the durable terminal record is already written.

A freed slot may admit a queued job of a now-uncapped kind.

finish records the terminal outcome of a run. It re-loads the record under
ioMu so a coalesced trigger that landed mid-run is honored, not clobbered.

A cron record goes back to queued whatever happened here (see RegisterCron).

A mid-run re-enqueue outranks backoff even on failure: keep its
ScheduledAt, reset Attempts (fresh demand).

recordFailureLocked persists failed-with-backoff, or dead at the attempt cap.
Caller holds ioMu. Reports a StateDead crossing so the caller can fire the
terminal-failure hook AFTER releasing ioMu (never under it).

recordPermanentFailureLocked marks a job dead without spending an attempt or
scheduling a retry; caller holds ioMu. An unclaimable job never increments
Attempts, so backoff would re-fail it forever.

notifyTerminalFailure invokes the hook with a cloned record and NO lock held,
so the callback may touch the store freely.

backoff returns the capped-exponential delay for the given attempt number
(1-based): base * 2^(attempt-1), clamped to cap.

`d <= 0` guards int64 overflow: a wrapped-negative delay yields a past
ScheduledAt and a hot retry loop.

cancelAll cancels every in-flight run (honoring each commit fence) and blocks
until all have exited. Only valid AFTER the dispatch loop has exited, so no
new run can register while it drains.

Trim runs one retention pass and reports how many completed jobs were removed.

notifyWorkChange reports a transition, dropped for cron kinds: a heartbeat
firing forever would append a durable event and re-push a snapshot per fire.
User actions call notifyChange directly, even for a cron entry.

marshalPayload encodes a payload or result. A nil value encodes to nil, not
"null" — Enqueue relies on nil meaning "leave what is there".
