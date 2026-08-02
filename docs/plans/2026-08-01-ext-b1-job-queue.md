# B1 — Durable job queue

Stage B1 of the [extension platform roadmap](2026-08-01-extension-platform-roadmap.md).
North star: [docs/vision/extension-platform.md](../vision/extension-platform.md).

Status: gate approved by Victor 2026-08-01. Not started.

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
