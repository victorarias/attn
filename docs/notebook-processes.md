# Notebook processes

A code-grounded map of every automated process behind the Notebook: who writes
what, when it fires, and where the state lives. This is the "how it actually
works" companion to [glossary.md](glossary.md) (which defines the vocabulary).
Vocabulary terms below — *the keeper*, *the chief of staff*, *the journal*,
*memory notes*, *the raw tier* — are defined there.

The dreaming/`harvest_dream` feature described in older plan docs has been
**removed**; nothing in this document depends on it.

## The shape: two authors, two products

The Notebook is organized around two automated authors and two products they
serve:

| Author | Scope | Writes |
| --- | --- | --- |
| **The keeper** | one workspace | the journal (narration) + compacts `context.md` |
| **The chief of staff** | cross-workspace | the journal (altitude), memory notes, `inbox.md` |

| Product | Audience | How it is written |
| --- | --- | --- |
| **The journal** (`journal/<date>.md`) | humans | curated narrative, machine-raw never lands in it |
| **Memory notes** (`memory/…`) | agents (the chief) | hand-authored, grounded, timeless |

Everything machine-raw the keeper needs lives in the **raw tier**
(`.attn/raw/`), which is the keeper's *input* and is physically unreachable
through user-facing notebook APIs.

## Notebook layout

Reserved layout (`internal/notebook/layout.go`):

- `index.md` — bundle root (no frontmatter).
- `log.md` — change history.
- `inbox.md` — chief selections.
- `journal/<date>.md` — the curated, dated journal (append-only narrative).
- `memory/{decisions,gotchas,domain}/` — distilled memory notes, indexed by
  `memory/index.md`.
- `.attn/` — machine state (the durable task runner, the raw tier, the
  narrate-cron anchor). Skipped by `List`, by the watcher, and by any
  dotfile-aware external sync scanner; `CleanPath` rejects dotdir segments
  (`internal/notebook/layout.go` `CleanPath`), so `Store.Write/Read` cannot
  address it.

## The raw tier (`.attn/raw/`)

Machine-internal capture, written by the daemon/agent with **direct filesystem
I/O — never through `notebook.Store`** (`internal/notebook/raw_tier.go`,
`internal/daemon/notebook_raw_tier.go`). Three buckets:

- `sessions/<wsID>/<sessionID>.md` — per-session digests, **nested by owning
  workspace**; a session with no workspace lands in the reserved
  `sessions/_solo/<sessionID>.md` bucket
  (`internal/daemon/notebook_narration.go` `notebookSessionDigestPath`).
- `dispatches/<dispatchID>.md` — one file per captured chief-of-staff dispatch
  outcome.
- `context-snapshots/<wsID>.md` — the `context.md` overlay snapshotted
  synchronously at workspace removal, before the row is deleted
  (`snapshotWorkspaceContextOnRemove`), so the editorial overlay is never lost
  before the keeper can narrate it.

`writeRawAtomic` (`internal/daemon/notebook_raw_tier.go`) is the single atomic
writer for dispatches and context snapshots; it validates the client-controlled
id as one safe path segment (`rawTierFilename`/`rawTierSegment`) so a crafted id
cannot escape the raw dir. Capture is deterministic and always happens; the
keeper's narration is best-effort on top of it.

## The durable task engine (`internal/tasks`)

A **single-worker, file-backed** runner. One atomic JSON file per task under
`<root>/.attn/tasks/<kind:subject>.json`; the "queue" is `os.ReadDir` filtered
by state — no SQLite table (the burned-migration gotcha means a new table would
be silently skipped on real DBs). One shared runner, `d.compactRunner`, built in
`startCompactRunner` (`internal/daemon/workspace_keeper.go`).

- **States** (`internal/tasks/task.go`): `queued → running → {done | failed}`;
  `failed → queued` on backoff retry, `failed → dead` when attempts exhausted;
  `failed/dead → queued` on manual `Retry`.
- **Coalescing**: the derived id is `kind:subject`, so re-enqueuing the same
  workspace/session collapses onto one record. `EnqueueOptions.Debounce` pushes
  `NextAttemptAt` forward to coalesce bursts; `ZeroDebounce` overrides it to
  "now" (used by the removal-boundary final narrate).
- **Single-worker exclusivity**: a `.runner.lock` PID lockfile with a
  `processAlive` stale-reclaim (`internal/tasks/lock.go`).
- **Crash recovery**: `recoverOrphans` resets any `running` task back to
  `queued` at startup (`internal/tasks/store.go`).
- **Commit fence**: `CommitGuard.Enter/Leave` lets an executor fence its single
  durable write so a timeout/cancel cannot tear it
  (`internal/tasks/commit_guard.go`) — the same property the keeper's compaction
  relies on.
- **Backoff**: capped exponential (base 1m, cap 1h, default 5 attempts).

Three kinds are registered post-removal — `compact_context`,
`summarize_session`, `narrate_workspace`. `harvest_dream` no longer exists.

## The keeper

One persona, executed by the shared runner. It is persisted as the
`attn-keeper` updater id on compacted context (`internal/daemon/workspace_keeper.go`;
migration 51 realigned the legacy `attn-janitor` rows).

### Duty 1 — compact `context.md` (`compact_context`)

- **Trigger**: a durable context write that pushes `context.md` past the size
  threshold enqueues `compact_context` (`enqueueWorkspaceContextCompaction`). A
  disabled runner (no notebook root) falls back to an inline synchronous
  compaction.
- **Executor**: `compactContextExecutor` runs an agentic compaction in
  native-tools mode, validates the candidate, and applies it under the commit
  fence via `ApplyKeeperCompactResult`.
- **Setting**: `workspace_keeper_compact` (`{agent,model}` JSON; blank disables).

### Duty 2 — narrate into the journal (`summarize_session` + `narrate_workspace`)

A two-stage narration: cheap per-session digest → strong per-workspace
narrative.

- **`summarize_session`** (cheap tier, Claude Haiku default): triggered on
  session stop (`enqueueSummarizeSession`, 2-min debounce). Carries the
  transcript path + workspace id in `Meta` because the debounced run can fire
  after the session row is deleted. `summarizeSessionExecutor` writes a digest
  to the raw tier (`sessions/<wsID>/<sessionID>.md`). Success gate is
  *file-is-ledger*: the digest file must exist and have changed.
- **`narrate_workspace`** (strong tier, Claude Sonnet default): writes the
  curated `journal/<date>.md` entry. `narrateWorkspaceExecutor` gathers the
  context snapshot, the session digests, and the raw dispatch outcomes, and
  composes one entry per workspace per day. Success gate is *file-is-ledger*:
  the journal must carry the workspace marker and the block must have changed.
  Three enqueue sites:
  - `enqueueNarrateWorkspace` — on session stop while the workspace is live
    (2-min debounce, coalesces a burst of stops).
  - `enqueueDailyNarrateWorkspace` — the nightly backstop (see cron below);
    stamps `Meta` `daily_pass=1`, which relaxes the success gate so a no-op
    refresh is `done`, not a retried failure.
  - `enqueueFinalNarrateWorkspace` — the removal-boundary retrospective
    (`ZeroDebounce`, so it writes immediately even over a pending debounce).
- **Settings**: `notebook.summarize_session` / `notebook.narrate_workspace`
  (`{agent,model}` JSON; blank uses the tier default). Narration is always on.

## The notebook cron (`internal/daemon/notebook_cron.go`)

A single per-minute, timezone-aware tick — the **sole surviving consumer** of
the cron after dreaming removal. It drives the daily per-workspace narrate
backstop: the journal entry a long-lived workspace gets on a day it had no
session stop.

- **Loop**: `startNotebookCronEnqueuer` (launched from `Start` *after*
  `startCompactRunner`, so the narrate executor is registered before the first
  tick) → `notebookCronTick` → `enqueueDueDailyNarrates`.
- **Schedule**: `notebook.cron.frequency` (default `0 3 * * *`) evaluated in
  `notebook.cron.timezone` (default machine-local). Validated by
  `validateNotebookCron{Frequency,Timezone}` (rejects embedded `TZ=`/`CRON_TZ=`
  prefixes and impossible dates).
- **Anchor state**: `NarrateCronState` at `.attn/narrate/state.json`
  (`internal/notebook/narrate_cron_state.go`) — its **own** state file, a single
  `ScheduledFrom` anchor. First observation records "now" and does *not* fire
  (so startup never narrates immediately); the first real pass lands at the next
  slot.
- **Catch-up**: a fire advances the anchor to "now" (not to the next slot), so
  several slots missed while the daemon was down collapse into a single
  catch-up pass.
- **Activity gate**: an in-memory, non-persisted set
  (`markNotebookWorkspaceActivity` / `drainNotebookNarrateActivity`). A workspace
  is marked active by a session end (`handleStop`) or a content-changing context
  write (`updateWorkspaceContext`, `changed=true`). The fire drains-and-clears
  the set, so idle workspaces are skipped and a removed workspace is skipped (its
  removal-boundary retrospective already ran). The set is lost on restart by
  design — the persistent session-end trigger is the primary path; the cron is
  only the backstop.
- **Settings migration**: `migrateNotebookCronSettingKeys` (idempotent, runs at
  `Start`) copies any persisted `notebook.dreaming.{frequency,timezone}` forward
  to `notebook.cron.*` and reaps the orphaned `notebook.dreaming.enabled` row.

## The journal

The curated, dated, human-facing log. Three writers, and **nothing machine-raw
lands in it**:

1. **The keeper** — per-workspace narratives (the `narrate_workspace` executor,
   the primary author).
2. **The chief of staff** — cross-workspace altitude entries.
3. **Humans** — direct edits.

Entries are safe (one workspace per day, secret-free) and continue the narrative
across days. The narrate agent reads the **raw tier**, never the curated journal
itself, so the journal stays editorial.

### Dispatch capture is raw-tier, not journal

When a chief-of-staff dispatch reaches a terminal report (or its session ends),
`journalDispatchOutcome` (`internal/daemon/notebook_dispatch_journal.go`) writes
one deterministic file to `.attn/raw/dispatches/<dispatchID>.md` — **not** to
`journal/<date>.md`. The per-dispatch file plus a hidden
`<!-- attn:dispatch:<id> -->` marker is the exactly-once ledger (a retried
capture overwrites the identical file). The keeper later narrates those raw
outcomes into the curated journal.

> The older `AppendJournalEntryOnce` in-file-marker path is **legacy for
> dispatches** — it still exists as an API but is no longer the dispatch capture
> path. `dispatchDecisionText` / `dispatchVerificationLine` render the decision
> and verification lines inside the raw dispatch block.

## Memory notes

Distilled, timeless, agent-facing knowledge under `memory/{decisions,gotchas,
domain}/`, indexed by `memory/index.md`.

- **Hand-authored only.** There is no automated writer or promote pass today —
  the dreaming harvest that would have fed one was removed. The sole write path
  is the user-facing `attn notebook memory write` CLI →
  `handleNotebookWrite`/`Store.Write` (`cmd/attn/main.go`,
  `internal/daemon/notebook.go`).
- **Grounding is a hard rule.** Every note must carry resolvable `sources:`
  (journal anchors, `dispatch:<id>`, or URLs) — enforced in the notebook
  guidance (`internal/hooks/hooks.go`) and the `memory/index.md` scaffold
  (`internal/notebook/layout.go`). No authoring from paraphrase alone.
- **The chief of staff consumes them.** The activation guidance directs the
  chief to orient via `memory/index.md` and `attn notebook list memory` before
  substantive work.

The split is deliberate: the **journal** is a dated log of *what happened* (for
humans); a **memory note** is a timeless statement of *what is known* (for
agents).
