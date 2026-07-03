# Plan: Background-task errors → Notifications + Tasks-in-Settings

## Goal

Background/headless work (the keeper's compaction, session summaries, workspace
narration, and the orphaned-ticket reconcile classifier) fails too quietly. Its
errors are parked on the **notebook screen** — the wrong home, and one the user
rarely opens. Move that visibility to a new **global, daemon-level Notifications
surface** with read/unread state and a click-through error dialog, and give the
tasks themselves a management home in **Settings**. Along the way, converge the
one-off reconcile scheduler onto the durable task runner so there is a single
task substrate and a single error producer.

## Scope

**In:**
- Migrate the durable task runner's persistence from per-file JSON
  (`<notebookRoot>/.attn/tasks/*.json`) to a **SQLite table** in the profile DB
  (`~/.attn[-profile]/attn.db`); convert existing tasks once.
- Grow the runner from a strict single worker to **bounded, per-kind
  concurrency** (executors run concurrently; record mutations stay serialized).
- Fold the **reconcile classifier** (`internal/daemon/ticket_reconcile.go`) into
  the runner as a `reconcile` task kind.
- New **Notifications** concept: SQLite table, protocol (list / mark-read /
  broadcast + unread count), sidebar button with an unread counter, click →
  detail dialog (what was being done + error text + Retry). Producer = a task
  reaching terminal `dead`.
- **Tasks UI in Settings**; remove the Tasks panel from the notebook screen.

**Out:**
- The **workflow engine** (`internal/workflow`, `WorkflowRun*`) — a separate
  system, not a background task. Untouched.
- The **session-keyed attention/color system** (`isAttentionSessionState`,
  sidebar state dots, AttentionDrawer). Background tasks aren't tied to a session
  or workspace, so they get their own surface, not the eye-drawing machinery.
- The concurrent `fix/reconcile-classifier` work is now **PR #454** (In Review,
  +194/−22): the classifier runs with `--strict-mcp-config` and rule-7 comments
  carry the real bounded error text. It makes the classifier's *error text*
  better; that flows into whatever we build here. **PR3 must land after #454
  merges — rebase over main and build on its classifier body, do not port the old
  one.** No coordination needed beyond that ordering; PR1/PR2 are unaffected.

## Key insight — storage migration is load-bearing, not just cleanup

The runner is **disabled when there is no notebook root** (`Runner.Disabled()`)
*because its storage lives under that root*. The reconcile classifier, by
contrast, must run for any dead session regardless of notebook config — which is
exactly why it was built as a bespoke goroutine instead of a task kind.

Moving task storage to the **global profile DB** (PR1) dissolves that coupling:
the runner no longer needs a notebook root to persist, so the "disabled without
root" gate can drop, and reconcile can become an ordinary task kind (PR3).
Executors that genuinely need a notebook (compaction/narration) keep their own
internal no-op-when-no-root guards, which they already have. So PR1 is a
prerequisite for the reconcile convergence, not independent cleanup.

## Architecture Map

```text
Current:
  keeper/narration enqueue ─┐
                            ├─> compactRunner (tasks.Runner, single worker)
  task_list  ──────┘     └─> internal/tasks/store.go  (JSON files under
                                        <notebookRoot>/.attn/tasks/*.json)
                                  └─> LastError/state=dead ──> Task proto
                                        └─> NotebookSurface Tasks panel (notebook screen)

  reconcileTicketsOnSessionEnd / sweep (5-min ticker)
    └─> own goroutine + semaphore(2) + reconciled_at claim + repair pass
        └─> execTicketReconcileClassifier (headless claude -p)
            └─> 🩺 ticket comment  (the one good error surface today)

Target:
  keeper/narration enqueue ──┐
  reconcile enqueue ─────────┤─> Runner (bounded per-kind concurrency)
  (session-end + sweep)      │     └─> internal/tasks Store SEAM
                             │           └─> store.Tasks* (SQLite `tasks` table, global DB)
                             │     └─> OnTerminalFailure(task) ──> Daemon
                             │           └─> renderTaskDescription(kind,subject)
                             │           └─> store.AddNotification(...)
                             │           └─> broadcast notifications_updated{unread_count}
  reconcile executor still posts its 🩺 ticket comment (unchanged output)

Frontend:
  Sidebar tool button [🔔 badge=unread] ──> Notifications panel (list, read/unread)
                                              └─> click row ──> Notification dialog
                                                    (body="what was being done"
                                                     + error text + Retry ──> task_retry)
  SettingsModal ──> "Background Tasks" section (state/attempts/next/last_error/Retry)
  NotebookSurface Tasks panel ──> DELETED
```

## Data Model / Interfaces

```go
// New SQLite `tasks` table (migration {61}) — mirrors internal/tasks.Task.
// Meta stays a JSON blob and is NEVER projected to the frontend (internal fs paths).
tasks( id TEXT PK, kind TEXT, subject TEXT, state TEXT, attempts INT,
       next_attempt_at TEXT, last_error TEXT, meta_json TEXT,
       created_at TEXT, updated_at TEXT )

// Persistence seam so runner logic is unchanged; only the backend swaps.
// internal/tasks/store.go's file store becomes one impl; a SQLite-backed impl
// (thin adapter over internal/store) becomes the production one.
type Store interface {
    List() ([]*Task, error)
    Get(id string) (*Task, error)
    Save(t *Task) error
    Delete(id string) error
}

// New `notifications` table (migration {62}). read_at NULL == unread.
notifications( id TEXT PK, kind TEXT, title TEXT, body TEXT, detail TEXT,
               source_kind TEXT, source_id TEXT, created_at TEXT, read_at TEXT NULL )
```

```ts
// Protocol (TypeSpec main.tsp → generate-types → constants.go; bump ProtocolVersion).
// Rename Task* -> Task* (the "notebook" prefix is a lie once tasks live in
// Settings and include reconcile).
task_list            -> task_list_result { tasks: Task[] }
task_retry {id}      -> task_retry_result { task? }
tasks_changed        (broadcast, payload-free)      // existing, renamed

notification_list          -> notification_list_result { notifications: Notification[] }
notification_mark_read {id|all} -> notification_mark_read_result { unread_count }
notifications_updated       (broadcast) { unread_count }   // drives the sidebar badge

type Notification = {
  id: string; kind: string;          // "task_failed" (extensible)
  title: string;                     // "Workspace narration failed"
  body: string;                      // "what was being done" (rendered from kind+subject)
  detail: string;                    // full error text (task.last_error)
  source_kind: string;               // "task"
  source_id: string;                 // task id — Retry deep-links here
  created_at: string; read_at?: string;
}
```

## Boundaries

- **`internal/tasks` Runner** owns lifecycle/retry/concurrency; knows nothing of
  notifications or tickets. It exposes `OnTerminalFailure(func(*Task))`; the
  daemon translates.
- **Daemon** owns kind→semantics: `renderTaskDescription(kind, subject)` for the
  human "what was being done" line, and the reconcile executor body (claim,
  drop-rule, 🩺 comment). It registers the runner callbacks and writes
  notifications.
- **`internal/store`** owns both new tables; the `tasks` SQLite impl is an
  adapter so `internal/tasks` need not import daemon/store types directly (keep
  the seam thin — pass a `Store` into `tasks.New`).
- **Frontend** never sees task `Meta`. The notifications badge reads only
  `unread_count` from the broadcast; the list/dialog fetch on open.

## The concurrency change (the one risky invariant)

Today "single worker" is load-bearing: it removes the need for per-task locks and
underpins orphan-`running` recovery ("one worker per root"). Reconcile wants 2
concurrent classifier subprocesses. Target design:

- Separate **executor concurrency** from **record-mutation serialization**: the
  worker may dispatch up to `N(kind)` executor bodies concurrently, but every
  state transition still happens under the runner mutex.
- **Per-kind cap**, default 1 (keeper/narration stay effectively serial);
  `reconcile` = 2.
- Behaviors that MUST survive (verify each): orphan-`running` recovery on start,
  subject-derived coalescing + zero-debounce override, per-executor
  `context.WithTimeout`, `Cancel` blocks until exit, single-instance ownership
  lock, no lost/half-written records.

Isolate this in its own PR so the invariant change is reviewed alone.

## Implementation Steps (sliced for ~1k-line PRs)

- [x] **PR1 — Tasks persistence → SQLite.** Added `tasks` table (migration {61};
      real DBs verified at MAX(version)=60 first). Introduced the `tasks.Store`
      seam (exported interface + `FileStore` wrapper over the untouched file store);
      SQLite adapter (`internal/daemon/task_store.go`) mapping `Task`↔`store.TaskRecord`;
      single-instance lock extracted to shared dir-lock helpers and relocated to the
      profile data dir; import-once conversion from `.attn/tasks/*.json` (drops
      unparseable, retires the dir). `startCompactRunner` injects the SQLite store.
      Behavior-preserving: the enable gate stays keyed on the notebook root in PR1
      (the gate-drop moves to PR3 with reconcile). No protocol change. Tests:
      store CRUD/recover, adapter round-trip + runner-through-SQLite end-to-end,
      import-once migration. `go test` green for tasks/store/daemon; tasks race-clean.
      **Real-app smoke** (throwaway profile seeded with a copy of the real v60 dev
      DB): migration {61} applied cleanly (v60→v61, tasks table created, 20 tickets
      intact); daemon startup ran the import-once conversion (`tasks migrate:
      imported 1 legacy task(s)`, unparseable file dropped, dir retired to
      `.migrated`); the live SQLite-backed runner executed 3 real `narrate_workspace`
      tasks to `done`. Source dev DB untouched; profile torn down.
      **Merged** 2026-07-02 as PR #455 (merge commit `335f5e8d`): figgyster APPROVED
      on head, CI green. A follow-up commit gofmt-aligned `scanTaskRow`'s var block.
- [x] **PR2 — Runner bounded per-kind concurrency.** Replaced the single
      run-one-at-a-time worker with a dispatch loop that claims every eligible task
      whose kind is under its per-kind concurrency cap and launches each as its own
      goroutine. `executor` gained a `limit`; new `ExecutorConfig{Timeout,
      MaxConcurrent}` + `RegisterWith` (default cap 1 ⇒ a kind stays serialized with
      itself, different kinds now run in parallel; reconcile will use 2). Runner
      state: `run *activeRun` → `runs map[id]*activeRun` + `inflight map[kind]int`,
      both under `mu`; a claimed slot is reserved under `mu` then persisted under
      `ioMu` (ioMu stays the outer lock, no store I/O under `mu`). `Stop` reordered
      for detached runs: close done → wait loop exit → `cancelAll` (fence + join
      every in-flight run). All prior invariants preserved (commit fence, Requeued
      coalescing, backoff/dead, orphan recovery, single-instance lock,
      Cancel-blocks-until-terminal-record). No protocol/frontend change. Tests:
      cross-kind concurrency, per-kind cap serialization, configured cap 2, Stop
      drains multiple in-flight; existing suite + new ones race-clean at `-count=10`.
      **Live-app smoke** (throwaway profile, daemon built from the branch): fresh DB
      migrated to {61}; the live dispatch loop ran seeded `compact_context` (ws-A/ws-B)
      + `summarize_session` tasks — cross-kind ran concurrently, the two same-kind
      compacts serialized (next_attempt_at ~2ms apart, cap 1); both terminal paths
      (failed+backoff, done) persisted; a seeded `running` orphan was recovered
      (stale runner lock reclaimed) → re-dispatched → done on next boot. Graceful
      `runner.Stop()`/`cancelAll` is NOT reachable in prod (nothing calls
      `Daemon.Stop()` outside the test harness — the daemon is killed and relies on
      orphan recovery), so that path stays covered by `TestStopDrainsConcurrentInFlightRuns`.
- [x] **PR3 — Reconcile → `reconcile` task kind.** *Land after PR #454 merges;
      rebase over main and build on its classifier body — do not port the old
      one.* Register the executor (body = classifier incl. drop-rule/🩺 comment);
      enqueue on session-end and from the sweep (sweep enqueues, no longer runs
      inline); per-kind cap 2; delete the semaphore + the bespoke `go
      d.runTicketReconciliation` goroutines + `maybeRepairAbandonedReconcileClaim`
      (runner orphan-recovery replaces it). Backend + tests.

      **Refinement vs the original plan (settled during implementation):**
      - **Keep `reconciled_at`, do NOT retire it.** It is not merely an internal
        claim flag — non-terminal + `reconciled_at` set drives the *user-visible
        orphan badge* on the board (`app/src/utils/ticketOrphan.ts`,
        `ticket_board.go` projection, `main.tsp`). Retiring it is a
        protocol/frontend change, out of scope for a backend-only PR3.
      - **`ClaimTicketReconciliation` stays the trigger gate; only the inline
        `go` becomes an `Enqueue`.** Session-end still claims (set-if-unset), which
        (a) lights the badge immediately, exactly as today, and (b) dedupes the
        handlePTYExit+dropSessionRecord double-fire so the FIRST fire — the one
        with the session row still present, hence the freshest inputs — wins and
        the loser exits. On a winning claim it enqueues `reconcile:<ticketID>`
        (inputs serialized into `Task.Meta`) instead of spawning a goroutine. The
        executor runs the classifier + drop-rule + 🩺 comment; it does NOT stamp
        `reconciled_at` (the claim already did).
      - **The durable task record is the sweep's dedup ledger.** The sweep enqueues
        only when `runner.Get("reconcile:<id>") == nil`. That both replaces the old
        `reconciled_at`-gated skip AND recovers the one gap the claim-then-enqueue
        split opens: a daemon death after the claim but before the enqueue persists
        leaves `reconciled_at` set with no task — the sweep sees no task and
        enqueues a real reconciliation (what the old maybeRepair pass hand-rolled a
        generic note for). The sweep still claims (badge) before it enqueues.
      - **maybeRepair deleted:** its "verdict never ran" role is now covered by the
        durable task (queued/running survives restart; orphan-recovery re-runs a
        run that died mid-flight) and by the sweep's task-existence enqueue.
      - **Accepted micro-windows (both negligible, both strictly better than
        today):** (1) a daemon kill in the microseconds between the executor's
        `AddTicketComment` committing and the runner marking the task done → next
        boot's orphan-recovery re-runs and posts one duplicate 🩺 comment (today's
        equivalent crash produces *no* verdict at all). (2) a reassign + new-session
        death whose seam is *also* lost to a crash won't be swept if a done
        `reconcile:<id>` task from the prior cycle still exists — vanishingly rare,
        and the common reassign+redeath is handled by the session-end seam
        (coalesces onto the record with fresh inputs).

      Backend + tests only — no protocol/frontend change. Deletes the bespoke
      semaphore, the two `go d.runTicketReconciliation` goroutines, and
      `maybeRepairAbandonedReconcileClaim`; drops the runner's notebook-root enable
      gate (enabled whenever the store exists). `go test ./internal/{daemon,tasks,
      store}` green; tasks race-clean. **Live-app smoke** (throwaway profile, real
      daemon from the branch, fast sweep/grace env): inserted an orphaned
      dead-owner ticket → the sweep discovered it after grace, claimed
      (`reconciled_at` stamped = orphan badge), enqueued `reconcile:<id>` onto the
      SQLite-backed runner, and the registered executor deserialized its Meta
      inputs and posted the attn-authored 🩺 note (no-transcript path, no real
      `claude` spawn); the task reached `done` (attempts=1, one-shot). Repeated
      sweep passes left exactly one comment / one task / an unchanged badge — the
      durable task record is the "already handled" ledger.
- [ ] **PR4 — Notifications + Tasks-in-Settings (backend + frontend).** One PR:
      - *Backend:* `notifications` table (migration {62}) + store methods;
        protocol (`notification_list` / `notification_mark_read` /
        `notifications_updated` + unread count; rename `Task*` → `Task*`;
        bump version); daemon `OnTerminalFailure` → `renderTaskDescription` →
        `AddNotification` → broadcast.
      - *Frontend:* sidebar tool button reusing `SidebarAction.badge`
        (`sidebar-tool-badge`, caps at 9+); notifications list panel (read/unread
        rows); detail dialog (body + error + Retry via `task_retry`); mark-read on
        open; "Background Tasks" section in `SettingsModal.tsx`
        (state/attempts/next/last_error/Retry); delete the `NotebookSurface` Tasks
        panel + wiring.

      This one is larger than the ~1k target; if it runs long, split the frontend
      (sidebar/dialog vs Settings section) as the natural seam.

Dependency order: PR1 → PR2 → PR3 → PR4. PR4's producer needs only PR1, but lands
last so reconcile failures (PR3) flow through the notifications it introduces.

## Decisions

- **SQLite over JSON (reversal).** Supersedes `docs/plans/2026-06-14-notebook-narration.md`'s
  "no SQLite" call. Its reason — a burned migration counter meant a new table
  would be silently skipped — is obsolete: migrations now flow to `{60}` (the
  ticket tables `{55}`–`{60}` landed after that plan). Read/unread + a global feed
  want a table anyway. Still re-verify real-DB `MAX(version)` before numbering.
- **Notify on terminal `dead`, not first `failed`.** `failed` auto-retries and may
  self-heal; a notification per transient blip is noise. `dead` = retries
  exhausted = actionable. Live `failed`/retrying state stays visible in the
  Settings Tasks UI for anyone who looks.
- **Bounded per-kind concurrency, not a worker pool.** Keeps record mutations
  serialized under the existing mutex (preserving the "no per-task lock"
  simplicity and orphan-recovery), while letting reconcile run 2 at once.
- **Reconcile keeps its 🩺 ticket comment.** That path is good and ticket-scoped;
  the notification is an *additional*, global signal, not a replacement.
- **Global, not session/workspace-scoped.** Notifications live in the profile DB
  and the sidebar chrome, deliberately outside the attention/color system.

## Resolved (were open questions)

- **Existing-task conversion — import-once, drop unparseable.** On first boot
  after upgrade, read `.attn/tasks/*.json` into the table and retire the dir. A
  file that fails to parse is dropped (logged), never a startup blocker.
- **Reconcile crash-recovery — runner replaces the bespoke repair; keep the sweep
  as the discovery/enqueue source.** Today reconcile has two nets for "daemon
  crashed mid-reconcile, ticket stuck claimed-but-unhandled": the 5-min sweep and
  `maybeRepairAbandonedReconcileClaim`. The runner already ships that net —
  orphan-`running` recovery re-runs a task whose process died mid-execution — so
  in PR3 we **delete `maybeRepairAbandonedReconcileClaim`** and lean on runner
  recovery. The **sweep stays** but for its *other* job: discovering orphaned
  tickets (a session that ended without firing the seam) and **enqueuing** a
  `reconcile:<ticketID>` task instead of running the classifier inline. Task
  the durable *task record* (not a comment scan) is the sweep's "already handled"
  ledger — it enqueues only when no `reconcile:<ticketID>` task exists yet.
  **Settled during PR3:** the `reconciled_at` column is **kept** (not retired) —
  it drives the user-visible orphan badge — and `ClaimTicketReconciliation` stays
  the trigger-time gate (badge + session-end double-fire dedup), so only the
  inline goroutine became a durable `Enqueue`. See the PR3 refinement note above.
- **Notification retention — unbounded for v1.** No cap/prune. Revisit only if
  growth becomes a real problem (listed under Follow-ups).

## Follow-ups

- Extend notification `kind` beyond `task_failed` (the "other things in the
  future" Victor flagged) — e.g. long-run-finished, update-available.
- Consider a notification for reconcile *verdicts* (not just failures) once the
  surface exists.
- Optional macOS-native notification when the window is unfocused (currently the
  app is accessory-mode with the dock tile hidden — larger, deferred).
- Notification retention (cap/prune) if the unbounded table ever grows painfully.
