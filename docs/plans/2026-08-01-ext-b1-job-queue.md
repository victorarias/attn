# B1 — Durable job queue

Stage B1 of the [extension platform roadmap](2026-08-01-extension-platform-roadmap.md).
North star: [docs/vision/extension-platform.md](../vision/extension-platform.md).

Status: gate approved by Victor 2026-08-01. Shipped — `internal/jobs` is in
place and `internal/tasks` is deleted.

## Gate answers

### 1. Build ours, as a new package

A new `internal/jobs` on a new table, porting the mechanics `internal/tasks`
earned — the `CommitGuard` commit fence, cancel-blocks-until-exit, capped
exponential backoff with a `dead` terminal state, per-kind concurrency caps,
and the requeue-during-run flag — onto a generalized record.

Rejected alternatives, and why:

- **Adopt a third-party queue.** River is Postgres-only; the Go/SQLite options
  are thin. Any of them would know nothing about the commit-fence contract the
  existing kinds depend on, and the daemon is single-process, so the
  multi-worker leasing such libraries are built around is dead weight.
- **Extend `internal/tasks` in place.** The premises that must change — id is
  `kind:subject`, deliberately no payload — are the ones that package is built
  on. It would be a rewrite wearing the old name, performed underneath four
  live kinds with no parity seam.

The four kinds (`compact_context`, `summarize_session`, `narrate_workspace`,
`reconcile`) migrate one at a time against a parity checklist.
`internal/tasks` is deleted when the last one lands.

### 2. Jobs are distinct by default, with an optional unique key

A job is kind + payload + optional `unique_key` + priority + queue +
per-job `max_attempts`, and a successful job persists a result.

Setting `unique_key` reproduces today's coalescing exactly — the four existing
kinds set it to their subject and behave as they do now, including
coalesce-during-run. Leaving it unset gives B2 what activities require:
arguments in, a value out, and two jobs of the same kind in flight at once.

Keeping coalescing as a universal law was rejected precisely because it makes
`activity("fetch", a)` and `activity("fetch", b)` collide, which would force
B2 to build a second execution path.

### 3. The queue owns firing; automations keeps catch-up policy

The queue gains cron entries that enqueue a job at an instant, and both
existing schedulers — `internal/daemon`'s automation schedule loop and the
notebook cron enqueuer — become clients of that one timing mechanism.

What stays in `internal/automation`: which missed occurrences still matter.
The per-definition cursor, the five-minute skip grace, the one-million-instant
replay-storm cap, and the startup-recovery interlock are product semantics
tuned against real misfires, not queue mechanics. Generalizing them into cron
would either impose them on every future caller or quietly lose them.

### Assumptions accepted alongside the gate

- The queue is Go, in-daemon. Extension-authored jobs are executed via the Bun
  sidecar later (A4/B2); the queue itself does not move.
- Completed jobs are retained for a window and then trimmed, mirroring the bus
  retention decision in A1.

## Sequence note

B1 has no dependency on track A and runs in parallel with A1. B2 depends on
this stage plus A4's sidecar.

## Exit criteria

- The four task kinds run on the queue with equivalent behavior, verified
  against a written parity checklist.
- `internal/tasks` is removed.
- Periodic work fires from one mechanism; automations' catch-up behavior is
  unchanged.

## Implementation plan

Written at the start of implementation (2026-08-02), after A1 and A2 merged.

### The record

`internal/jobs` is a new leaf package (no `internal/daemon` import), same shape
of seam as `internal/tasks`: a `Runner` over a `Store` interface, with the
daemon supplying a SQLite-backed store.

```
Job{ ID, Kind, UniqueKey, Priority, Payload, Result,
     State, Attempts, MaxAttempts, ScheduledAt, LastError,
     Requeued, CreatedAt, UpdatedAt, CommitGuard }
```

`ID` is opaque and generated. It is **not** derived from kind+key, so nothing
parses it: identity for coalescing lives in `UniqueKey`, addressed through
`Runner.RemoveByKey(kind, key)` and a unique index on `(kind, unique_key)`.
`internal/tasks` conflated the two by making the id `kind:subject`, which is
why removing a task required the caller to know the id scheme.

`Payload` replaces `Subject` + `Meta`. The four kinds carry their subject in
the payload and set `UniqueKey` to it, reproducing today's coalescing exactly.

### Deliberately not built

- **A `queue` column.** The gate named it, but per-kind concurrency caps are
  strictly finer-grained than named queues, and nothing in B1 or B2 needs two
  worker pools — activities are ordinary jobs. An unused column that the
  dispatcher does not filter on is dead weight, and adding it later is a
  one-line migration. Revisit when something actually needs a second pool.
- **`Priority` is built**, and is a real term in the dispatch ordering rather
  than a stored-but-ignored field.

### Selection and its bound

Dispatch reads an ordered page of eligible jobs — `priority DESC, scheduled_at
ASC, created_at ASC` — instead of listing the whole table and sorting in Go,
which is what `internal/tasks` does. The page is bounded, and hitting the bound
is logged with the limit and the count so it can never truncate silently.

Receipt for the bound: the real task tables hold 146 rows (`~/.attn`) and 206
rows (`~/.attn-dev`), of which all but 2–6 are terminal `done`/`dead` and
therefore never eligible. Eligible rows at any instant are single digits. The
bound is set at 1000 — three orders of magnitude past the measured working set,
so only something broken reaches it.

### Retention

Terminal `done` jobs are trimmed past an age window (30 days, mirroring
`bus.DefaultRetention`), on the same trim-loop shape. `dead` jobs are **kept**:
they are the actionable "attn gave up" record a notification links to by id,
they only exist when a human has not acted, and the measured count is 2–6 —
they are not the unbounded growth trimming exists to bound.

### Slices

1. `internal/jobs` + the SQLite store + migration **87** — the package and its
   tests, no callers. (85 and 86 went to the session snooze deadline and the
   session annotation drafts table, both in flight alongside this and both since
   landed.)
2. The daemon builds a `jobs.Runner`, the four kinds move onto it kind by kind
   against the parity checklist below, `internal/tasks` is deleted, and
   existing `tasks` rows are imported once. The wire shape does not change:
   `protocol.Task.Subject` is populated from `UniqueKey`, so no protocol bump
   and no frontend work.
3. Cron entries. The queue owns firing; `startNotebookCronEnqueuer` and
   `startAutomationScheduleLoop` stop hand-rolling `time.NewTicker` and become
   cron entries (`Runner.RegisterCron`). `internal/automation` keeps its
   per-definition cursor, five-minute skip grace, million-instant storm cap,
   and startup-recovery interlock inside the fired job — those are product
   semantics, not queue mechanics.

### What a cron entry is

A recurring duty is an ordinary job that re-arms itself: one durable record per
kind, coalesced on a fixed key, returned to `queued` at `now + interval` when
its fire finishes. A ticker's next fire lives only in memory, so a restart
silently resets it and nothing can be asked when it stops; a cron entry's next
fire is a row.

Two properties are deliberate:

- **A cron entry never dies.** An ordinary job that keeps failing is abandoned
  at the attempt cap, because nobody wants a broken job retried forever. A
  heartbeat that stops is a silent outage instead, so a failing fire is logged
  and recorded on the record and the entry re-arms.
- **Arming does not reset an existing entry.** Re-arming on every boot would
  mean a daemon restarted more often than the interval never ticks at all.

Cron entries are excluded from `Runner.List()` — the work list answers "what
does the daemon owe", and two permanently-queued rows at the top of it answer
nothing — and their arming and fires do not go through the change hook.
Receipt for that second exclusion: two entries firing once a minute is ~5,800
change events a day, each one a durable bus row plus a snapshot re-push, for
the lifetime of the daemon. Operator reads go through `Runner.CronEntry(kind)`.

### Deviation: `NarrateCronState` stays

The plan above said the notebook's nightly anchor would move onto the cron
entry. It does not, and this is the reason rather than an omission.

The queue fires on a fixed interval; the notebook's nightly pass is a
timezone-aware cron expression the user configures. The per-minute tick is
therefore still the thing that evaluates that expression against an anchor, and
the anchor still has to live somewhere. Moving it from its own file onto the
job record would trade one small file format for a subtler contract — the
anchor would ride in the job's `Result`, where any early return in the handler
silently erases it — and buys nothing the file does not already do correctly.
Retiring it is worth doing when the notebook's schedule and the queue's
scheduling model are the same shape; that is not this change.

**The four kinds do not run on two runners at once.** The gate says they
migrate one at a time against a parity checklist; that is the order of the
work, not a shipped dual-runner state. All four are reached through a single
`jobQueueRef()` and feed a single panel, so coexistence would mean two
locks, two dead-job notification paths, and a panel unioning two lists — all
of it thrown away at the end. The per-kind rigor is kept as one commit and one
checklist pass per kind.

### Parity checklist (applied per kind)

- Coalescing: re-enqueue within the debounce window collapses to one run.
- Coalesce-during-run: a re-enqueue mid-run re-runs after it instead of being
  lost, and a `RunNow` re-enqueue is not demoted to a backoff delay.
- Commit fence: cancel during the durable write waits rather than tearing it.
- Cancel blocks until the run goroutine has exited.
- Backoff: capped exponential, then `dead` at the attempt cap, with the
  terminal-failure notification firing exactly once.
- Crash recovery: a `running` row at startup returns to `queued`.
- Removal: a removed subject leaves no row behind.
- The kind's own inputs survive the move from `Subject`/`Meta` to `Payload` —
  notably `summarize_session`, whose run reads a transcript path recorded
  before the session and workspace rows were deleted.

### Parity result

The queue owns the mechanics, so the eight checklist items are proven once
against `internal/jobs` rather than re-proven per kind — re-running the same
assertions through four handlers would exercise the same code path four times.
What is checked per kind is what each kind actually brought with it: its inputs,
its enqueue sites, and its success gate.

Mechanics (`internal/jobs`):

| Item | Test |
| --- | --- |
| Coalescing | `TestUniqueKeyCoalescesABurstIntoOneRun` |
| Coalesce-during-run | `TestATriggerArrivingMidRunRunsTheJobAgain` |
| `RunNow` not demoted to a delay | `TestRunNowOverridesAPendingDebounce` |
| Commit fence | `TestCancelWaitsForTheCommitFence`, `TestCancelBeforeTheFenceStopsTheWrite` |
| Cancel blocks until exit | `TestCancelWaitsForTheCommitFence`, `TestStopDrainsInFlightRuns` |
| Backoff, then `dead` once | `TestFailuresBackOffThenGoDeadOnce` |
| Crash recovery | `TestStartRequeuesAJobLeftRunningByACrash` |
| Removal leaves no row | `TestRemoveByKeyForgetsTheJob` |
| Retention keeps `dead` | `TestRetentionTrimsCompletedJobsAndKeepsDeadOnes` |

Per kind (`internal/daemon`):

- **compact_context** — unique key is the workspace id, no payload. The fence is
  exercised against the real commit in
  `TestCompactContextCancellationWaitsForAdmittedCommit`; timeout and cancel in
  `TestCompactContextTimeoutAndCancellationProtectContext`; removal in
  `TestWorkspaceDeletionCancelsCompactionBeforeRemovingContext`; the
  re-check after the debounce window in
  `TestWorkspaceContextCompactionReChecksThresholdAfterDebounce`; the
  no-store fallback in
  `TestWorkspaceContextCompactionInlineFallbackWhenQueueDisabled`; the nil-safe
  pre-queue teardown in
  `TestWorkspaceTeardownDoesNotPanicBeforeTheJobQueueExists`.
- **summarize_session** — the transcript path and workspace bucket moved from
  `Meta` to `summarizeSessionPayload`:
  `TestHandleStopCarriesTranscriptAndWorkspaceOnThePayload` (written at enqueue)
  and `TestSummarizeSessionExecutorUsesCarriedPayloadWhenRowGone` (read after
  the rows are deleted). Enqueue sites:
  `TestHandleStopEnqueuesNarrationForWorkspaceSession`,
  `TestHandleStopEnqueuesOnlyDigestForSoloSession`. Success gate unchanged:
  `TestSummarizeSessionExecutorVerifiesDigestLedger`,
  `…FailsWhenDigestMissing`, `…RequiresFreshDigest`.
- **narrate_workspace** — the daily-pass flag moved from a `"1"` meta string to
  `narrateWorkspacePayload.DailyPass`, and it still selects the relaxed gate:
  `TestNarrateWorkspaceDailyPassUnchangedIsDone`,
  `TestNarrateWorkspaceDailyPassAbsentIsDone`,
  `TestNarrateWorkspaceDailyFlagRemovalPassStillStrict`,
  `TestNarrateWorkspaceRoutinePassUnchangedStillFails`. The removal boundary
  now uses `RunNow` instead of a zero debounce:
  `TestWorkspaceRemovalEnqueuesFinalNarrate`.
- **reconcile** — the inputs moved from a JSON-in-a-meta-string to the payload
  (`reconcileInputsFromJob`), covered by
  `TestRunTicketReconciliationPostsVerdict` and its sibling verdict tests; the
  sweep's "already enqueued" check moved from an id guess to
  `GetByKey`: `TestSweepSkipsTicketWithExistingTask`. Its concurrency cap of
  two is a `HandlerConfig` value; the per-kind bound itself is proven in
  `TestAKindIsSerializedWithItselfButNotWithOthers`.

Handover and wire:

- One-time import of owed `tasks` rows, inputs and ids preserved:
  `TestImportCarriesEachKindsInputsOntoItsPayload`,
  `TestImportKeepsARowWithUnreadableMeta`,
  `TestDrainLegacyTasksReturnsOwedWorkAndEmptiesTheTable`,
  `TestDrainLegacyTasksDropsCompletedRows`.
- Wire shape unchanged, and neither `Payload` nor `Result` reaches a client:
  `TestTaskToProtocolMapsFieldsAndOmitsPayload`, `TestSendTaskListWSResult`,
  `TestSendTaskRetryWSResultRequeuesDeadTask`,
  `TestTasksChangedBroadcastReachesClient`.
- Both periodic duties are armed by the queue rather than by a ticker:
  `TestStartJobQueueArmsThePeriodicTicks`.

### Verification

Daemon-tier is not enough: task state reaches the app through the background
tasks panel, the notification feed, and the bus facts A2 routed. Live
verification runs on a throwaway profile with the panel open — enqueue, watch
a kind run, force a failure to `dead`, retry from the notification, restart the
daemon mid-run and confirm recovery.

Done on 2026-08-02 against a throwaway profile with a full app install:

- Both cron entries arm at first boot and re-arm every minute; `attempts` stays
  0 across fires and `scheduled_at` advances by the interval.
- Stopping and restarting the daemon left both entries' `scheduled_at`
  untouched — a restart does not push the next fire out.
- Three rows seeded into the retired `tasks` table were drained at boot: the
  two owed ones became jobs with their ids intact and their meta translated to
  typed payloads (`daily_pass: "1"` → `true`), the `done` one was dropped, and
  the table was left empty.
- The panel listed both imported jobs with state, attempts, next-attempt
  countdown, and error text. Neither cron entry appeared in it.
- A job driven to the attempt cap reached `dead` and produced exactly one
  notification, carrying the job id as its source so the feed's Retry
  deep-links to it. The feed showed it.
- Retry from the panel revived the dead job, and the row moved
  `dead → running → failed` in the UI without a manual refresh — the change
  hook, the bus fact, and the snapshot re-push all still connect.
- A row left in `running` (a killed daemon) was returned to `queued` at the
  next boot and re-dispatched immediately rather than staying stuck.

### Review: three silent-failure paths, closed

A pass over the queue as a long-lived primitive turned up three ways it could
stop working without saying so. Each is fixed with a regression test, and each
test was mutation-checked — the fix reverted, the named assertion failed, the
fix restored.

1. **A failed `Start` left a queue that accepted work and ran none.** The
   daemon discarded `runner.Start()`'s error. A queue that fails to start is
   not inert: `Disabled()` is false, so `Enqueue` succeeds and writes rows, but
   there is no dispatch loop to read them and no cron entry is armed. Every
   background duty and both periodic ticks stop, and the only evidence is work
   that never happens. `Start` can fail for real — the single-instance lock
   refuses when its recorded pid is alive, and pids get recycled after a crash.
   Now logged loudly, naming the error and what it costs.
2. **A cron entry that reached a terminal state never came back.** Run a build
   that does not register a cron kind — a rename, a rollback, a `RegisterCron`
   that errored and was only logged — and dispatch retires its due entry as an
   unknown kind. Nothing selects a terminal row, and `List` hides cron entries,
   so the heartbeat was gone for good even after the kind returned. `armCron`
   now revives a terminal entry while still leaving a queued/failed/running one
   exactly as it found it, so the arm-once property is intact.
3. **Enqueueing onto a cron kind minted a second heartbeat.** `finish()` sends
   every record of a recurring kind back around the recurrence, so an enqueue
   carrying any key other than `CronKey` created a second self-perpetuating
   entry that `List` hides and `CronEntry` never finds. `Enqueue` now refuses
   with `ErrCronKind`. No caller did this; the guard is so none can.

Live re-verification of (2) on a throwaway profile: a cron entry forced to
`dead` was revived at the next daemon start, logged the revive, and resumed
firing on its interval, while the healthy sibling entry's next fire was left
untouched across the same restart.

Two things were considered and left alone. A handler that hangs inside its
commit fence blocks shutdown without bound — the fence deliberately disarms the
run's timeout, and the only fence user commits a store write, so the exposure
is theoretical. And `Retry` on a job that no longer exists reports success and
does nothing; that is the retired runner's behavior carried across unchanged,
and a dead job is never trimmed, so the notification deep-link always has its
record.

### Review: the handover into the queue is one transaction

The first version of the handover read the owed rows and deleted them in one
transaction, then wrote the translated jobs in another. That is not equivalent
to doing both together, and the difference is lost work: a crash between the two
commits, or a single failing job write, left the old rows deleted and their
replacements never made. This path runs exactly once per installation and the
work it carries — pending compaction, narration, reconcile — has no other copy,
so a partial handover is unrecoverable.

`Store.MigrateLegacyTasks` now does the whole handover in one transaction: read,
translate, write every job, delete the old rows, commit. Translation moved into
a callback (`Daemon.legacyTaskToJob`) so it can run inside that transaction
while the store stays a leaf package. The callback cannot fail — a row it cannot
read still becomes a job, because an unreadable payload is diagnosable and a
dropped row is not.

Any failure rolls back to a table that still holds everything, and the daemon
logs that nothing moved and the next start will retry.
`TestAFailedJobWriteLeavesEveryLegacyRowIntact` proves it: three owed rows, a
translation that collides two of them on one coalescing key, and afterwards all
three legacy rows are still there, zero job rows were written, and a second
handover moves all three. Mutation-checked against the two-phase version, which
fails that test with "0 legacy rows left after a failed handover".

Live on a throwaway profile seeded with a copy of the production database
(schema 85, 150 legacy task rows): migrated to 86, moved the 3 owed rows
(2 queued, 1 dead) with their ids, dropped the 147 completed ones, and emptied
the old table.
