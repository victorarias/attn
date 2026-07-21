# Implementation guide: attn Automations

**Status:** implementation in progress.

## Current implementation state

- Slice 1 merged through [attn PR #597](https://github.com/victorarias/attn/pull/597)
  (`6c563d4a`).
- Slice 2 is implemented and live-verified in
  [attn PR #601](https://github.com/victorarias/attn/pull/601) (`95137511`).
- Slice 3 merged through [attn PR #608](https://github.com/victorarias/attn/pull/608)
  (`67dca139`). Packaged live verification completed before the
  behavior-preserving rebase at `aac6b7f2`; the rebased head was green and
  Figgyster-approved before merge.
- Slice 4 merged through [attn PR #614](https://github.com/victorarias/attn/pull/614)
  (`25a1c2ec`, 2026-07-20). The continuity path was live-verified at `b2a43445`;
  Figgyster found Codex resume availability accepted any non-empty rollout ID,
  fixed in `3402100f` with unit and daemon regressions for present and missing
  rollouts; the packaged continuity scenario was rerun green on the fixed code
  from a fresh `automations` profile before merge.
- Slice 5 merged through [attn PR #617](https://github.com/victorarias/attn/pull/617)
  (`69ba6978`, 2026-07-20): scheduled trigger with catch-up, storm guard, and
  singleton continuity, proven by the packaged serial scenario (see the
  checklist entry below).
- Later slices remain pending.

## Next steps

1. Slice 6 (profile-level Automations surface) is implemented on
   `slice/automations-surface` (see the checklist entry below); it merges
   through the ordinary review flow.
2. Then Slice 7 (self-service editing and lifecycle completion), followed by
   the final verification matrix.

This guide implements the [attn Automations vision](attn-automations.md)
as functional walking-skeleton slices. Each slice must leave a real capability
runnable through the non-production app. The initiative lives on one long-lived
integration branch; slice PRs target that branch, and the completed initiative
merges to `main` through one final integration PR.

## Settled decisions

- The engine owns durable definitions, occurrences, immutable run snapshots,
  idempotency, policy, and recovery. It does not know how tickets, workspaces, or
  PTYs are assembled.
- `Deliver(WorkRequest)` owns the visible attn work envelope: a chief-owned ticket,
  prepared location, workspace/pane, and agent session. It uses stable IDs and
  ensure/adopt operations so recovery can repeat it safely.
- Automations pin a `LaunchSpec`; they do not reference a new reusable
  agent-profile entity. There is no such entity in attn today.
- Automation sessions always use the selected driver's automatic approval mode.
  Approval is not configurable in the definition or CLI, and automation launch
  must not inherit the mutable profile-wide auto-approve setting.
- `LocationSpec` is part of the automation definition. Machine-specific source
  paths belong there, never in source code.
- PR review uses a detached worktree at the snapshotted PR head SHA. The worktree
  is unique per session, not per branch or PR.
- PR definitions consider every accessible repository by default. Repository
  include/exclude filtering is configurable independently from repository-source
  placement.
- Repository materialization uses a profile-owned managed clone by default. A
  definition may override a canonical `host/owner/repo` identity with an existing
  local clone, such as a large Bazel monorepo.
- Automation provenance is recorded in both directions: ticket events identify
  the automation definition as author, and the ticket's `automation_run_id`
  points to the exact run.
- The foundation is CLI/API first. A first-class Automations UI remains part of
  the initiative after the runtime behavior is proven.
- Agents prepare review results locally in their ticket/session: what the PR does,
  why, how, findings and risks, and a recommended reading order. They do not post,
  approve, or comment on GitHub unless a later user action explicitly authorizes
  that interaction.
- Continuity and catch-up remain configurable because the planned PR and schedule
  cases exercise their different values. Initial overlap behavior is always
  `coalesce`; `queue` and `parallel` wait for a concrete use case.
- Automatic GitHub review uses `per_subject + coalesce`. Polling duplication,
  daemon restart, or a later request cycle for the same PR must not create a
  second reviewer session.

## Branch and delivery model

Use `feature/automations` as the long-lived integration branch (the exact branch
name may follow repository convention). Every slice follows the same path:

```text
main
  └─ feature/automations                    long-lived integration branch
       ├─ slice/foundation-cli-delivery ──PR─┐
       ├─ slice/repository-materialization ─PR├─> feature/automations
       ├─ slice/github-review-trigger ─────PR─┤
       └─ ...                              ─PR─┘

feature/automations ── final integration PR ──> main
```

Rules for the branch:

- Start each slice branch from the current integration-branch tip and target its
  PR back to the integration branch.
- A slice is mergeable only when its end-to-end capability works in a packaged
  non-production attn app. Schema-only, daemon-only, and UI-only placeholders are
  not slices.
- Keep the integration branch green. Merge current `main` into it regularly; do
  not rewrite history after later slice branches depend on it.
- Each slice owns its migrations, protocol version bump, tests, user-facing docs,
  and live evidence. Do not defer broken upgrade paths to the final PR.
- The final PR reviews the initiative as one product change. Re-run migrations
  from a copy of a current `main` database, the full automated suite, and the
  complete non-production app journey before merging it.
- Do not install or restart production while developing or verifying slices.

## Current-system mental model

Today attn has most of the mechanical pieces, but no durable owner of an
automation run:

```text
manual delegation
  -> delegate() resolves source-session-relative placement
  -> optional worktree
  -> workspace + pane
  -> handleSpawnSession()
       -> spawn PTY worker
       -> persist session row
  -> create chief-owned ticket when the source was the chief
```

That path is not the automation delivery API:

- `delegate()` requires a live source session. Automations must run when no chief
  session is open.
- existing-workspace placement takes the workspace directory and cannot pin an
  independent working directory;
- session launch occurs before ticket creation, leaving a crash window in which
  work exists without its durable automation envelope;
- `handleSpawnSession()` spawns the PTY before `Store.AddChecked` persists the
  session; and
- launch currently reads the mutable `auto_approve_enabled` profile setting.

The useful seams are the lower-level behaviors behind delegation: driver
validation, worktree mechanics, workspace/pane adoption, spawn/recovery, durable
tickets, role ownership, ticket unread delivery, and session resume.

The target path reverses ownership:

```text
CLI / provider observation
  -> automation.Engine accepts one occurrence
       -> transactionally claims occurrence + snapshots definition + reserves IDs
       -> validates LaunchSpec and LocationSpec
       -> workdelivery.Deliver(WorkRequest)
            -> ensure chief-owned ticket with automation_run_id
            -> prepare/adopt location
            -> ensure workspace + pane
            -> launch/adopt visible session with fixed automatic approval
       -> persist ticket/session/location links and mark run delivered
```

The run is delivered when the durable ticket and visible session are linked.
Whether the agent later finds issues, needs input, or fails is ordinary
ticket/session state, not an automation-engine run state.

## Minimal ordered reading path

Read these in order before implementing the foundation. Later-slice files can wait
until their slice begins.

1. `docs/vision/attn-automations.md` — product invariants and scope. It is the
   authority when a mechanical shortcut conflicts with the intended behavior.
2. `internal/daemon/delegate.go`: `delegate`, `createDelegationWorktree`,
   `createDelegatedTicket` — the current end-to-end assembly path and the behavior
   to extract without reusing its source-session assumptions or side-effect order.
3. `internal/daemon/ws_pty.go`: `handleSpawnSession` — driver validation, launch
   pins, workspace requirements, PTY-before-store crash window, and current global
   auto-approve inheritance.
4. `internal/store/tickets.go`: `CreateRoleOwnedTicket`, plus
   `internal/store/ticket_events.go` and `internal/daemon/ticket_notify.go` — durable
   chief ownership, append-only authorship, unread cursors, and notification.
5. `internal/daemon/workspacelayout.go`: `addWorkspaceSessionPane` — existing
   pane-adoption behavior needed to make delivery retryable rather than additive.
6. `internal/agent/driver.go`, `codex.go`, and `claude.go` — capability validation
   and the concrete automatic modes. Codex maps automatic approval to
   `approval_policy="on-request"` plus `approvals_reviewer="auto_review"`; Claude
   maps it to `--permission-mode auto`.
7. `internal/store/sqlite.go` and representative store modules — migration and
   transaction conventions for definitions, occurrences, runs, and
   `automation_run_id`.
8. `internal/daemon/daemon.go`: `performStartupPTYRecovery` and worker-session
   reconciliation — automation recovery must run after PTY adoption stabilizes.
9. `internal/tasks/{task,store,runner}.go` — prior art for injected clocks,
   atomic claiming, orphan recovery, retry backoff, and coalescing. Reuse lessons,
   not the task record: an automation run has a definition snapshot, occurrence,
   external side effects, and long-lived delivery links that `kind + subject`
   cannot represent.
10. `internal/protocol/schema/main.tsp`, `internal/protocol/constants.go`,
    `internal/client`, and an existing CLI request/result command — the foundation
    CLI and later UI must share one daemon API and obey protocol versioning.

Before the repository/GitHub slices, add:

11. `internal/git/worktree.go`, `internal/daemon/worktree.go`, and
    `internal/git/resolve.go`: `ResolveMainRepoPath`, `OriginHostOwnerRepo` — reuse
    repository identity checks, but add an exact-revision detached-worktree
    operation rather than the current named-branch delegation behavior.
12. `internal/github/client.go`: `SearchReviewRequestedPRs`, `FetchPRDetails`, and
    `internal/daemon/daemon.go`: `pollPRs`/`doPRPoll` — consume the existing 90-second
    refresh result; do not add a second GitHub polling loop.

## Module boundaries

```text
internal/automation
  Definition + validated immutable snapshot
  Occurrence claim and dedupe
  Run state and visible failure
  RunNow, provider acceptance, recovery
  continuity/catch-up policy and coalescing as later slices exercise them
            │
            │ Deliverer interface only
            ▼
internal/workdelivery
  Deliver(WorkRequest) -> DeliveryResult
  stable ticket/session/workspace/pane identities
  ticket-first ensure/adopt lifecycle
  LocationPreparer + SessionLauncher ports
            │
            ├─ internal/git and repository materializer
            ├─ ticket/store operations
            └─ daemon workspace, pane, PTY adapters

internal/store
  SQLite rows, constraints, and transactions
  no trigger, retry, continuity, or launch policy

internal/daemon
  composes engine, delivery adapters, GitHub/schedule providers, recovery order
  translates protocol requests/results and broadcasts compact updates

cmd/attn + internal/client
  CLI presentation and transport only
```

The module interfaces should be usable with in-memory fakes. Neither
`internal/automation` nor the core of `internal/workdelivery` should require a
live daemon, WebSocket client, wall clock, UUID generator, filesystem, or GitHub
client in unit tests.

### Core interfaces

```go
type Engine struct {
    Store      AutomationStore
    Deliverer  Deliverer
    Clock      Clock
    IDs        IDGenerator
}

func (e *Engine) ApplyDefinition(ctx context.Context, spec DefinitionSpec) (Definition, error)
func (e *Engine) RunNow(ctx context.Context, definitionID, requestID string, input json.RawMessage) (Run, error)
func (e *Engine) Accept(ctx context.Context, observation Observation) (Run, error)
func (e *Engine) Recover(ctx context.Context) error

type Deliverer interface {
    Deliver(context.Context, WorkRequest) (DeliveryResult, error)
}

type LocationPreparer interface {
    Prepare(context.Context, LocationRequest) (PreparedLocation, error)
}

type SessionLauncher interface {
    EnsureSession(context.Context, SessionLaunchRequest) (SessionLaunchResult, error)
}
```

`EnsureSession` is an internal daemon operation, not a loopback WebSocket call. It
should reuse validation and launch helpers from `handleSpawnSession` while allowing
the caller to pass the automation invariant `AutoApprove: true` explicitly.

## Definition and boundary shapes

Definitions are profile-local YAML/JSON documents stored canonically in SQLite.
The initial CLI surface is intentionally small:

```text
attn automation apply --file review.yml
attn automation list
attn automation show pr-review
attn automation run pr-review [--input-file occurrence.json]
attn automation runs pr-review
```

The CLI generates one request ID per `run` invocation and reuses it if transport
delivery is retried. The engine turns it into occurrence key
`manual:<request-id>`. Repeating the same request ID returns the same run; a new
CLI invocation intentionally creates a fresh run.

There is no approval field:

```yaml
api_version: attn.dev/automations/v1alpha1
id: local-review
name: Local review
enabled: true

trigger:
  type: manual

prompt: |
  Review the supplied work. Present the result locally in this ticket and session.

launch:
  driver: codex
  model: gpt-5.5
  effort: high

location:
  type: directory
  path: /Users/example/src/repository

policy:
  continuity: fresh
```

The PR-review form keeps trigger filtering separate from object-source placement:

```yaml
api_version: attn.dev/automations/v1alpha1
id: requested-pr-review
name: Requested PR review
enabled: true

trigger:
  type: github_review_requested
  repositories:
    mode: all_accessible
    include: []
    exclude: []

prompt: |
  Review this pull request. Explain what it does, why it exists, and how it works.
  Report findings and risks, then give me the best file reading order. Present the
  result locally. Do not post, approve, comment, or otherwise modify GitHub.

launch:
  driver: codex
  model: gpt-5.5
  effort: high

location:
  type: repository_worktree
  repository_sources:
    default:
      type: managed_cache
    overrides:
      "github.example.com/platform/large-monorepo":
        type: local_clone
        path: /Users/example/src/large-monorepo

policy:
  continuity: per_subject
  catch_up: latest
  overlap: coalesce
```

The scheduled form nests the schedule under the trigger. `time_zone` is a
required IANA name so a definition means the same instants on any machine;
`TZ=`/`CRON_TZ=` prefixes inside the cron expression are rejected:

```yaml
api_version: attn.dev/automations/v1alpha1
id: worktree-cleanup
name: Merged-worktree cleanup
enabled: true

trigger:
  type: scheduled
  schedule:
    cron: "0 9 * * 1-5"
    time_zone: America/New_York

prompt: |
  Review the git worktrees under this directory. Remove each linked worktree
  whose branch is fully merged and whose tree is completely clean using
  `git worktree remove` (never --force). Never remove a worktree with staged,
  unstaged, or untracked changes; list preserved worktrees with reasons.

launch:
  driver: codex
  model: gpt-5.5
  effort: high

location:
  type: directory
  path: /Users/example/src/repository

policy:
  continuity: singleton
  catch_up: latest
  overlap: coalesce
```

Scheduled semantics decided at implementation time (Slice 5): occurrence keys
are the intended instant in UTC (`scheduled:<RFC3339>`), so identity is
machine-independent. Each observation tick fires at most the newest due
instant per definition — older missed instants never fire, which is the
replay-storm guard. `catch_up: latest` always fires the newest missed instant
after downtime; `catch_up: skip` fires only within a 5-minute grace of the
intended instant and otherwise discards it. The first observation of a new
definition anchors its cursor without firing retroactively. Cron follows
robfig/cron standard parsing, so DST is wall-clock literal: a slot inside the
spring-forward gap does not fire that day, and the fall-back repeated hour
fires twice with two distinct UTC occurrence keys. Slice 5 accepts
`continuity: singleton` (one continuity stream keyed `"singleton"` reusing the
same ticket/session across occurrences); scheduled definitions require
`catch_up` and a `directory` location. A rejected or failed claim holds the
cursor back so the missed instant stays eligible; the retry fires whatever
instant is newest-due at that time — identical for hourly-and-coarser
schedules, possibly a fresher instant on minutely-grade ones — so a claim
failure delays the appointment rather than dropping it, and never prefers a
stale instant over a fresher due one.

Two deliberate Slice 5 boundaries, both to revisit in Slice 7 (lifecycle):

- Editing a singleton scheduled definition's prompt, launch, or location makes
  subsequent occurrences fail visibly at delivery: the continuation contract
  check compares each run against the origin run's snapshot pinned to the
  continuity binding, and that origin never advances. This is the same
  fail-visible-on-changed-contract stance Slice 4 chose for per-PR
  continuations, made total by the single `"singleton"` binding. Recovery today
  is disabling the definition or recreating it under a new id; proper edit
  semantics (rotating the continuity binding to a new ticket/session when the
  contract changes) belong to Slice 7's edit/disable/delete work.
- Validation now rejects `catch_up` on manual and github triggers (it only ever
  had meaning for schedules). Stored definitions keep observing fine, but
  re-applying an old YAML that carried a stray `catch_up` on a non-scheduled
  trigger fails validation until the field is removed.

The example paths illustrate configuration shape only. No user's path belongs in
source, fixtures intended for shipping, or built-in defaults.

The first implementation accepts only `overlap: coalesce`. `fresh`,
`per_subject`, and `singleton` continuity plus `skip` and `latest` catch-up remain
configurable because the planned manual, PR-review, and scheduled cases exercise
them. Do not implement `queue` or `parallel` until a real automation needs them.

```go
type LaunchSpec struct {
    Driver     string
    Model      string
    Effort     string
    Executable string // optional explicit selection; empty means registered driver executable
}

// Automatic approval is an engine invariant, not a field above.
type EffectiveLaunch struct {
    LaunchSpec
    ApprovalProductMode string // "auto"
    ApprovalDriverMode  string // Codex auto_review or Claude auto
}

type LocationSpec struct {
    Type              "directory" | "repository_worktree"
    Path              string
    RepositorySources RepositorySources
}

type WorkRequest struct {
    RunID, DefinitionID, SubjectKey, ContinuityKey string
    Provider                                       string // occurrence provider; gates provider-specific continuation checks
    Prompt                                         string
    Context                                        json.RawMessage
    Launch                                         EffectiveLaunch
    Location                                       LocationSpec
    IDs                                            DeliveryIDs
    Provenance                                     Provenance
}

type DeliveryResult struct {
    TicketID, SessionID, WorkspaceID string
    Directory, Revision              string
    Mode                             "created" | "adopted" | "continued" | "resumed"
}
```

Provider context remains structured and clearly delimited from the configured
prompt. The first slices do not add general string templating: an untrusted PR
title or body must not become executable automation policy.

## Durable data model

Use explicit columns for identity, state, constraints, and links. JSON is
appropriate for versioned definition/run snapshots and provider context, not for
fields needed by recovery or uniqueness checks.

### `automation_definitions`

| Field | Purpose |
|---|---|
| `id` | Stable profile-local slug and event-author identity component. |
| `name`, `enabled`, `deleted_at` | Current management state; deletion is soft while history exists. |
| `revision` | Monotonic integer incremented by semantic definition edits. |
| `spec_json` | Canonical validated current specification. |
| `created_at`, `updated_at` | Audit timestamps. |

A separate revisions table is unnecessary initially: every accepted run stores
the complete immutable effective snapshot. Add revision history only if users need
to browse unexecuted historical edits.

### `automation_occurrences`

| Field | Purpose |
|---|---|
| `id`, `definition_id`, `provider` | Durable observation identity. |
| `occurrence_key` | Provider-defined dedupe key; unique with definition and provider. |
| `subject_key` | Stable work subject, such as `host/owner/repo#123`. |
| `observed_at`, `payload_json` | When and what was observed; payload is untrusted context. |
| `created_at` | Claim time. |

Unique constraint: `(definition_id, provider, occurrence_key)`. The occurrence
key answers whether work is new; it never selects a continuity session.

### `automation_runs`

| Field | Purpose |
|---|---|
| `id`, `definition_id`, `occurrence_id` | One run per accepted occurrence. |
| `definition_revision`, `snapshot_json` | Immutable effective prompt, launch, location, policy, and schema version. |
| `state`, `last_error` | Coarse lifecycle and visible failure. |
| `ticket_id`, `session_id`, `workspace_id` | Preallocated stable delivery IDs and durable links. |
| `resolved_location_json` | Configured source, resolved main repo/cache, worktree, and exact revision. |
| `created_at`, `updated_at`, `delivered_at` | Lifecycle timestamps. |

Persist only the lifecycle state the user or recovery coordinator needs:

```text
pending -> delivered
    └───> failed
```

The idempotent ensure operations and durable artifact links determine the next
recovery action; intermediate implementation phases are not persisted as a second
state machine. Do not mark a run delivered based only on a successful spawn call.
It is delivered after the session exists durably and the run, ticket, and session
links agree.

### Ticket provenance

Add one nullable `automation_run_id` field to tickets. Automation delivery sets:

```text
automation_run_id = <run id>
event author = automation:<definition id>
role owner   = chief_of_staff
```

A partial unique index on `automation_run_id` makes ticket creation an idempotent
ensure. The run stores the forward ticket link; `automation_run_id` enables reverse
navigation and proves which immutable snapshot created the work without inventing
a generic origin system before another origin type exists.

Continuity later adds `automation_continuity_bindings(definition_id,
continuity_key, ticket_id, session_id, updated_at)` with a unique definition/key
pair. Do not add that table to the foundation before a slice exercises reuse.

## Delivery lifecycle and recovery

### New run

1. In one transaction, claim the occurrence, snapshot the current definition,
   and reserve stable run, ticket, session, workspace, and pane IDs.
2. Validate the immutable snapshot before external side effects: driver exists,
   model/effort are supported, automatic approval is supported, location shape is
   valid, and required occurrence fields exist.
3. Ensure the chief-role-owned ticket using `automation_run_id` and author
   `automation:<definition-id>`. Assign it to the preallocated session ID even
   though the runtime does not exist yet.
4. Prepare or adopt the location. On failure, leave a visible failed ticket/run
   with the specific reason.
5. Ensure an unmuted workspace and session pane using the stable IDs.
6. Ensure the session with the snapshotted prompt, structured context, exact CWD,
   `LaunchSpec`, and explicit automatic approval. Advance the agent's ticket cursor
   past the creation event because the initial prompt carries the brief out of band.
7. Persist resolved links and mark the run delivered. Broadcast compact run and
   ticket updates; best-effort nudge the chief through the normal ticket path.

### Restart recovery

Automation recovery begins only after worker PTY recovery/adoption and workspace
layout reconciliation have stabilized. For each nonterminal run, replay the
following ensures with the reserved IDs:

```text
ticket by automation_run_id exists?    create or adopt
location exists at expected revision? prepare or adopt; mismatch fails visibly
workspace/pane exist?                  create or adopt
worker/session with session ID live?   adopt; otherwise safely spawn once
all durable links agree?               mark delivered
```

An existing worktree is adoptable only when its Git common directory matches the
resolved source and `HEAD` equals the snapshotted revision. A dirty or mismatched
worktree is evidence, not disposable debris; fail visibly and do not reset it.

Configuration and capability failures move the run to `failed`. Crash interruptions
leave it `pending` and reconcile automatically with the same snapshot and IDs.
After correcting a failed definition, Run now creates a new occurrence and run; it
never retries by mutating the old run's meaning.

## Repository materialization

For `repository_worktree`, resolution is deterministic:

1. Canonicalize the occurrence identity as lowercase host plus owner/repository for
   matching while retaining provider display values.
2. If an explicit local-clone override matches, require that the path exists, is a
   Git repository, resolves through `ResolveMainRepoPath` if it is itself a
   worktree, and that `OriginHostOwnerRepo` matches the requested identity.
3. If the explicit override is invalid, fail visibly. Do not ignore the user's
   configuration by falling back.
4. Otherwise ensure a non-bare managed clone below the active profile data root,
   for example `automations/repos/<safe-repo-key>/repo`. The exact internal path is
   derived by attn and is not stored as a source default in shipped configuration.
5. Fetch the provider-specified PR ref/revision under a per-repository lock, verify
   the snapshotted SHA resolves to a commit, and never substitute the moving branch
   head.
6. Create a detached worktree below
   `automations/worktrees/<session-id>/<repo-name>` using the stable session ID.
   Add a dedicated exact-revision Git helper; do not create or share a named branch.
7. Start the session with that worktree as its CWD and persist the configured source,
   resolved main repository, worktree path, and revision on the run.

The managed clone persists across runs. A per-session worktree persists while its
ticket/session is inspectable; agent idle is not a deletion signal. The initial
implementation provides explicit cleanup only. Automatic cleanup belongs in a
later lifecycle slice and must refuse to discard dirty worktrees.

**A continuity thread's runs share identity, so no lifecycle operation may treat a
run as privately owning its resources.** Within one thread every occurrence reuses
the binding's session id, and the worktree path is keyed on session id alone, so a
thread's oldest terminal run and its live current run resolve to the *same*
directory. Its ticket is likewise shared, and `tickets.automation_run_id` is written
once at thread creation and never updated, so it points permanently at the thread's
oldest run. Both facts are load-bearing: deleting an old run's worktree or its row
destroys the live thread, and both failures are permanent — the next occurrence
fails closed with "reviewer continuity worktree is missing" or "continuity origin
run missing" and never recovers on its own. Any per-run cleanup, retention, or
migration must therefore ask whether the run's thread is still bound before touching
disk or rows. A run's session row being absent does **not** mean its thread is dead;
a thread routinely outlives its session row, and the continuity binding is the
authoritative liveness signal.

That binding is also what gives a thread a *lifetime*, not just a liveness check at
a point in time. A binding survives indefinitely on its own — nothing ages it out by
elapsed time or occurrence count — so what actually bounds how long a bound thread's
worktree sticks around is its ticket's TTL: `SweepExpiredTickets` hard-deletes a
terminal (done/failed/crashed) ticket once `closed_at` is old enough, and cascades
that deletion to the ticket's continuity binding in the same transaction, releasing
the thread. An open ticket is never swept regardless of age, so a thread stays bound
for as long as its ticket stays open, however long that is; retention pruning
(A3/A4) only ever runs after that release, never instead of it.

The binding is not the only thing pinning that disk, though, and this is the easiest
mistake to make when reasoning about reclamation. `automationRunCleanupSafety` checks
a *live session row* before it checks the binding, and that check is bare row
existence (`store.Get(sessionID) != nil`), not PTY liveness. So a run's worktree is
reclaimable only once **both** pins lift: the session row is gone *and* the thread is
unbound. The two are independent — ageing out the ticket does nothing while the
session row survives. That matters in practice because automation-delivered agent
sessions do not reliably end: a codex-driven delivery is observed sitting in
`launching` indefinitely with its pty-worker and agent process still alive, and a
worker that reattaches to a restarted daemon never reaches the 12-hour idle
self-stop. Until session lifetime is bounded too, the ticket TTL bounds only one of
the two pins.

For large Bazel repositories, the local-clone override reuses the repository's Git
objects and existing machine configuration. Per-session paths still produce
distinct Bazel output bases, while repository/disk/remote caches configured outside
the worktree can remain shared. Verification must measure representative disk and
startup behavior rather than assuming the override removes all per-worktree cost.

## Functional walking-skeleton slices

### Slice 1 — Manual definition to visible agent

**User can:** apply a manual YAML definition with an explicit directory, run it
from the CLI, see one chief-owned ticket and visible agent session, inspect the
immutable run, safely repeat a transport request, and recover an interrupted
delivery after restarting the non-production daemon.

Build the minimal store, `internal/automation`, `internal/workdelivery`, daemon
composition, CLI/client commands, ticket `automation_run_id`, explicit
`LaunchSpec`, directory `LocationSpec`, fixed automatic approval, deterministic
delivery IDs, and startup reconciler. Use a fresh workspace/session for every run.
Do not add GitHub, schedules, continuity, a polished editor, or repository cloning.

Directory mode is a real generic automation location for manual and scheduled work,
not a temporary implementation of PR review. This slice uses it to isolate and prove
the engine-to-delivery/recovery spine before Git enters the path. PR definitions do
not use directory mode: Slice 2 adds their required exact-SHA per-session worktree
behind the same `LocationPreparer` interface.

**Exit evidence:** unit/store/protocol tests; every partial-failure checkpoint
converges to one delivery; `make dev` packaged app run with a real harmless agent;
restart during delivery; CLI shows the same run/ticket/session afterward.

**Implementation record (2026-07-19):** Slice 1 landed the planned vertical
spine through PR #597. `internal/automation` owns definition validation
and immutable effective snapshots; `internal/store/automations.go` and migration
73 own the atomic occurrence/run claim and provenance; `internal/workdelivery`
owns ticket-first delivery; and `internal/daemon/automations.go` composes stable-ID
delivery, recovery, and unattended launch verification. `cmd/attn/automation.go`
exposes `apply`, `list`, `show`, `run`, and `runs`. Session launch now persists a
complete row before spawning the PTY, and automatic approval/trust is available
only through an internal automation launch policy rather than the general spawn
protocol.

Automated verification passed (`go test ./...`, frontend unit tests, generated
types, diff checks, and the PR's daemon/frontend/E2E/release/benchmark checks).
Packaged live verification used the fixed `automations` profile and proved a real
happy-path ticket/session, reusable-request idempotency, invalid input rejected
before claim, and daemon-death recovery to exactly the same stable ticket,
workspace, pane, and session. The durable occurrence artifact keeps provider
payload separate from configured instructions, and one-shot model/effort/approval
transport values do not leak into the agent child environment.

The implementation preserves the planned product behavior. The delivery topology
has one recorded exception: Victor authorized this first slice PR to merge directly
to `main` after it was green and Figgyster-approved, rather than waiting for a final
integration PR. PR #597 merged as `6c563d4a` after the exact refreshed head passed
CI and received Figgyster approval. Reconfirm the branch topology before starting
Slice 2.

### Slice 2 — Manual PR review in an exact per-session worktree

**User can:** configure managed cache plus repository-specific local-clone
overrides, run the review automation manually for a PR, and receive a local review
brief from an agent working at the exact snapshotted head SHA in its own worktree.

Add `repository_worktree`, managed clone/fetch, override validation, detached
worktree ensure/adopt, structured PR input, and a CLI convenience that resolves a
PR URL into the typed input. Keep the trigger manual; this slice proves location,
prompt/context separation, and the actual review output before background
observation is allowed to launch agents.

**Exit evidence:** managed-cache and local-override paths both work; two runs for
the same PR create two different session worktrees at the same SHA; a changed PR
head produces a new snapshot; invalid/mismatched override fails without fallback;
large-monorepo Bazel cache behavior is measured; no GitHub mutation occurs.

**Implementation record (2026-07-19):** Slice 2 extends the same manual-run spine
with typed GitHub pull-request occurrences and `repository_worktree` locations.
`attn automation run <id> --pr-url <url>` performs one focused read-only PR GET,
validates and stores the provider payload before delivery, and keeps the title,
body, and other provider fields out of configured instructions. Definitions select
a profile-managed cache by default and may pin canonical repository identities to
validated local clones. Delivery resolves the immutable occurrence SHA, creates or
adopts a clean detached per-session worktree under the active profile, binds the
ticket CWD only after preparation, and records configured source, main repository,
worktree, provider ref, and revision in `resolved_location_json`. Existing dirty,
attached, foreign, or wrong-revision paths fail unchanged.

Automated evidence covers strict definition/PR validation, occurrence subject
provenance, one-GET/no-mutation GitHub resolution, managed-cache protection,
local-clone origin validation, moved-ref recovery using an already-present
snapshot, clean detached worktree creation/adoption, dirty/attached rejection
before launch, stable-session adoption after an agent has begun editing, delayed
recovery until initial GitHub host discovery, and retryable recovery when private
repository credentials are temporarily unavailable.
`go test ./...`, the 1,848-test frontend suite, generated types, and diff checks
passed. A final `gpt-5.6-terra` high autoreview reported no actionable findings.
Figgyster's first review identified a symlinked-profile publication edge: clone
validation canonicalized the staged path before rename, so the old implementation
could return the removed staging spelling. The refreshed head revalidates the
published target and includes the requested symlink-parent regression; the full Go
suite and a focused `gpt-5.6-terra` high autoreview pass on that fix.
The refreshed PR CI is green. E2E shard 3 needed two unchanged-head reruns: the
first two attempts failed in different pre-existing terminal-find and split-render
tests, and the third passed. Figgyster approved exact head
`951375115910de2e5d8f3a4f247dcdff2868f3a2` after verifying the requested fix.

Packaged live verification used the fixed `automations` profile and open docs-only
attn PR #404 at `67fc97c9974239492c9bfb6e7db4f47f0677e4e7`:

- managed-cache run `0929092c-703c-4b08-a73b-c18dfeebc47d` created ticket
  `auto-0929092c703c4b08` and session
  `bfab9ef1-3072-47ee-a0c5-5c58fc263d88`;
- local-override run `cb895816-6aa5-4e58-8253-f570f6b9e151` created ticket
  `auto-cb8958166aa54e58` and session
  `8a018788-d15e-4602-be9e-7360f78e42ae`;
- both tickets reached review, both worktrees were clean and detached at the exact
  same SHA, their Git common directories resolved to the managed cache and the
  configured local clone respectively, and replaying the local request ID returned
  the same run/session/worktree;
- process launch evidence pinned `gpt-5.6-terra`, high effort,
  `approval_policy=on-request`, and `approvals_reviewer=auto_review`; and
- a mismatched explicit override was rejected before persistence with no managed
  fallback.

After the recovery fixes, the same packaged profile completed a fresh managed
happy path as run `c9b2fa2f-1e06-4e5c-b051-d33b13fa50dc`, ticket
`auto-c9b2fa2f1e064e5c`, and session
`ddac6e6d-dac6-414a-89bb-63f02107ccdb`. It again resolved PR #404 to the exact
recorded SHA, produced a clean detached worktree, exposed the chief-owned working
ticket, and returned the same durable run and artifact IDs when its request ID was
replayed.

For the large-Bazel gate, launching an external model against the private
`services-pilot` repository was disallowed by the environment's data-boundary
policy. The local-only equivalent measured an exact detached worktree for PR
#147816 without exposing source: checkout was about 2.49 GiB while the shared Git
object store was about 15.28 GiB; `bazel info output_base` started in 3.17 seconds
and produced a worktree-specific output base distinct from the main checkout's
3.21-second startup/output base. The clean temporary measurement worktree was
removed afterward and is reproducible from the recorded SHA. Public-repository
live agent happy paths still cover both materialization modes.

Victor's per-slice PR instruction is treated as the current delivery topology:
Slice 2 targets `main` directly and stops after green CI plus exact-head Figgyster
approval for Victor's review.

### Slice 3 — Review-requested observation without a duplicate reviewer

**User can:** enable a GitHub review-request definition and have a newly requested
review automatically create the same prepared local result proven in Slice 2.

Add a GitHub observation adapter beside `doPRPoll`, repository all/include/exclude
filtering, a durable review-request edge ledger, occurrence keys, `latest` downtime
catch-up, and a `per_subject` binding created before delivery. Use `coalesce` so
every accepted occurrence for one PR is recorded on the same ticket/reviewer rather
than launching another session. Consume existing refreshed PR snapshots and fetch
details only to pin required evidence such as head SHA; do not add another poller.

**Exit evidence:** repeated polls produce one run; remove/re-request produces a new
occurrence on the same PR ticket/reviewer; daemon downtime with current demand
produces at most one latest run; excluded repositories do nothing; concurrent or
replayed observations for one PR never create a second reviewer session.

### Slice 4 — Return later PR cycles to the same reviewer

**User can:** choose `per_subject` continuity so a later review-request cycle for
the same PR returns to the existing ticket/reviewer when safe, while every accepted
occurrence and run remains visible.

Deepen the subject binding introduced with automatic GitHub delivery: add
automation-authored ticket occurrence events, live nudge, ticket resume, and
explicit fresh fallback/failure rules. Keep occurrence identity and continuity
identity separate. Decide behavior from evidence for archived tickets, missing or
dirty worktrees, unavailable transcripts, and changed head SHAs; never silently
bind a new revision to the wrong CWD.

**Exit evidence:** duplicate occurrence remains one run; a new cycle is a new run
linked to the same ticket; live, stopped/resumable, archived, missing-worktree, and
dirty-worktree cases each follow an explicit tested path.

Implemented in PR #614: a live reviewer keeps the nudge path; a stopped reviewer
resumes only with a recorded resume target under the immutable unattended launch
snapshot; successful continuation reopens an archived ticket; an automation-owned
dirty or branch-changed worktree is preserved; missing worktrees, unavailable
transcripts, and changed PR heads fail visibly. A serial packaged-app scenario on
the fixed `automations` profile copied an existing Codex rollout and completed the
happy path plus missing-worktree failure in 36 seconds. It proved the same ticket,
session, and worktree, preserved dirty review notes, passed the copied rollout to
`codex resume`, and retained `gpt-5.6-terra` with high effort and automatic review
approval. The scenario emits a machine-readable summary and restores the isolated
daemon after each run. Figgyster's exact-rollout finding is addressed by making
Codex implement `ResumeAvailabilityProvider` through
`transcript.FindCodexTranscriptForResume`. Unit and daemon regressions cover both
present and missing rollouts, and the packaged scenario was rerun green on the
fixed code (2026-07-20, fresh `automations` profile, 15s). That rerun also
hardened the scenario itself: a brand-new profile returns JSON `null` for an
empty `automation list`, which the daemon-readiness poll had misread as
not-ready.

### Slice 5 — Scheduled prompt with one real maintenance job

**User can:** define and run a scheduled prompt, survive daemon downtime according
to `skip` or `latest`, and use it for the second proving case: merged-worktree
cleanup prepared through an ordinary visible agent session.

Add a clock-driven schedule adapter, intended-instant occurrence keys, time-zone
validation, restart reconciliation, and one real cleanup definition. Provider code
only observes due instants; the existing engine and delivery path do the work.

**Exit evidence:** DST/time-zone table tests; restart before/after a due instant;
no replay storm; one visible maintenance ticket/session; dirty worktrees are never
silently deleted.

### Slice 6 — Profile-level Automations surface

**User can:** scan definitions and failures, enable/disable, Run now, inspect run
history, and navigate directly to the resulting ticket/session in the app.

Add request/result protocol mutations and compact update broadcasts, then a
profile-level UI. Keep canonical state in SQLite and re-read after broadcasts. The
first UI may leave definition authoring in YAML/CLI; it is still functional because
all operational actions and resulting work are visible.

**Exit evidence:** packaged-app create-via-CLI/manage-via-UI journey; request
failure is shown rather than optimistically hidden; restart preserves list and
navigation; no duplicate run from repeated UI response handling.

### Slice 7 — Self-service editing and lifecycle completion

**User can:** create and edit definitions in the app, choose supported continuity,
catch-up, repository filters, and source overrides, understand validation errors,
and manage retained history/worktrees.

Add the polished editor only after two providers have fixed the internal provider
seam. Complete definition edit/disable/delete semantics, bounded retention, and safe
explicit cleanup. Keep overlap fixed to `coalesce`; `queue`, `parallel`, and public
provider plugins remain deferred until concrete use cases supply their requirements.

**Exit evidence:** full end-to-end journeys for PR review and scheduled maintenance;
edits do not change old snapshots; disable stops new occurrences without killing
active work; delete retains provenance; cleanup preserves dirty worktrees; final
upgrade and packaged-app matrix pass from the integration branch.

## Verification strategy

### Idempotency

- Same definition/provider/occurrence key returns the existing occurrence and run.
- Same CLI request ID returns the same run, ticket, session, workspace, and pane.
- A new CLI request ID creates an independent fresh run.
- Ticket `automation_run_id` uniqueness prevents a second ticket even if delivery retries after
  losing the first response.
- A PR subject binding plus `coalesce` prevents repeated or concurrent observations
  from creating a second reviewer session for the same PR.
- Worktree adoption validates Git common directory and exact `HEAD`, not path
  existence alone.

### Partial failure

Inject a crash/error after each durable or external boundary and run recovery:

| Checkpoint | Required result after recovery |
|---|---|
| occurrence/run transaction commits | one pending run proceeds |
| ticket commits | same ticket is adopted by automation_run_id |
| managed clone/fetch completes | repository is reused under its lock |
| worktree is added | same correct worktree is adopted |
| workspace is registered | same workspace is adopted |
| pane is added | same pane/session slot is adopted |
| PTY spawns before session row persists | worker recovery/adoption settles before automation retries |
| session row persists before run link | links reconcile and run becomes delivered |
| delivered commit succeeds but response is lost | repeated request returns the delivered run |

Every case must finish with at most one ticket, worktree, workspace, pane, and live
session for the reserved delivery IDs. Mismatch or dirty evidence produces a
visible failure; tests must never make recovery pass by deleting evidence.

### Daemon restart

- Start automation reconciliation after `performStartupPTYRecovery`, including its
  worker recovery retries, not concurrently with it.
- Cover no chief session, chief starting later, worker already adopted, missing
  worker metadata, and a recoverable persisted session.
- Edit the definition before restart and prove the old pending run still uses
  its immutable snapshot.
- Restart repeatedly and prove convergence, not just a successful single restart.

### Live non-production app

For every slice that touches daemon lifecycle, protocol, PTY, Git, or UI:

1. Build/install the isolated dev app (`make dev`) outside the sandbox.
2. Point the CLI at the dev profile and apply a fixture definition containing only
   temporary/test paths.
3. Run the user journey with a real supported agent in automatic mode and confirm
   the workspace, ticket, session, CWD, prompt, and run links in the packaged app.
4. Restart only the dev daemon/app at the slice's interruption checkpoint and
   confirm one recovered delivery.
5. For repository slices, inspect `git rev-parse HEAD`, Git common directory, and
   worktree path from the live session. For GitHub slices, use a non-production or
   explicitly read-only PR scenario and verify no comment/review/approval was made.
6. Capture commands, run IDs, ticket/session IDs, relevant logs, and screenshots in
   the slice PR. Do not treat unit tests as a substitute for this evidence.

## Risks and implementation-time gates

- **Automatic approval is not a universal no-write policy.** Codex auto-review and
  Claude auto use different classifiers and runtime/account capabilities. The
  configured local-only prompt is necessary, but it does not mathematically prevent
  a tool from mutating GitHub. If stronger enforcement becomes a release
  requirement, design a driver/tool-policy boundary explicitly rather than calling
  the fixed `auto` mode sufficient.
- **Current spawn ordering has a real crash window.** Delivery recovery must account
  for a worker created before its session row; simply wrapping `handleSpawnSession`
  and retrying can duplicate or kill work.
- **Review-request cycles need evidence beyond the current PR row.** The GitHub
  slice must prove the edge key for remove/re-request and offline `latest` behavior
  before continuity relies on it.
- **Repository caches need concurrency and disk lifecycle rules.** Serialize
  clone/fetch per repository, retain inspectable worktrees, and add deletion only
  through an explicit dirty-safe lifecycle.
- **Large Bazel worktrees retain per-path cost.** Shared disk/repository/remote
  caches help, but every session has its own output-base identity. Measure the real
  monorepo path during Slice 2 and adjust retention without weakening per-session
  isolation.
- **A long feature branch magnifies drift.** Keep slice PRs green and functional,
  merge `main` regularly, and run the main-to-feature migration and final packaged
  app matrix before the integration PR.

## Implementation checklist

- [x] Create the long-lived feature branch and protect the slice-PR workflow.
- [x] Implement and live-verify Slice 1; update this guide with actual symbols and
      any conservative deviations forced by code evidence.
- [x] Implement and live-verify Slice 2, including the large-repository measurement.
- [x] Implement and live-verify Slice 3 before enabling background launches broadly.
- [x] Finish PR #614: exact-rollout regressions in place and packaged continuity
      verification rerun green on the fixed code (2026-07-20). Merge follows the
      ordinary review flow (green checks plus Figgyster approval on the head).
- [x] Add schedule and its real maintenance case through the same engine:
      scheduled trigger with catch-up, storm guard, and singleton continuity,
      proven by the packaged serial scenario
      `real-app:scenario-automation-scheduled-cleanup` (a real codex agent
      removing a merged-clean worktree while preserving a dirty one) — run
      `automation-scheduled-cleanup-2026-07-20T12-22-03-841Z`, all legs green
      against the app built from `45c7684c` (2026-07-20).
- [x] Add operational UI: a profile-level Automations panel listing definitions
      with enable/disable, run-now for manual definitions, and run history with
      ticket/session navigation, fed by `automations_changed` broadcasts and
      typed WS get commands (protocol 176), proven by the packaged serial
      scenario `real-app:scenario-automation-surface` — run
      `automation-surface-2026-07-20T15-48-06-271Z`, all four legs green
      (broadcast-driven listing, delivered navigable run with no duplicates,
      inline daemon rejection on a disabled definition, daemon restart
      preserving definitions and runs) against the app built from `91f899b5`
      (2026-07-20).
- [x] Complete the lifecycle half of Slice 7: contract-keyed continuity rotation
      on edit, provenance-retaining delete with resurrect, bounded retention,
      explicit dirty-safe cleanup reporting a three-way
      `cleaned`/`kept_dirty`/`kept_active` partition (protocol 179), and a
      bounded thread *lifetime* — the ticket TTL sweep is now actually wired up
      and releases a thread's continuity binding along with its ticket, so the
      binding stops pinning a reviewer automation's worktrees forever. Note the
      binding is only one of two independent pins: `automationRunCleanupSafety`
      blocks on a live session row first, so a worktree is reclaimable only once
      the session has also ended. Automation-delivered agent sessions do not
      reliably end today (see the thread-lifetime note above), so this slice
      bounds one pin, not both. Proven by the
      packaged serial scenario `real-app:scenario-automation-lifecycle` — run
      `automation-lifecycle-2026-07-20T21-53-29-423Z`, all three legs green
      (edit-rebind including the revert case, delete-resurrect, and a single
      cleanup call partitioning a clean, a dirty, and a still-bound worktree)
      against the app built from `458455f8` (2026-07-20). The TTL sweep itself
      is out of that scenario's reach (30-day TTL, hourly tick), so it was
      verified separately against a live daemon on the same build with
      `ATTN_TICKET_RETENTION_SWEEP_INTERVAL=5s`: a 40-day-old closed ticket and
      the continuity binding pointing at it both disappeared on the first tick,
      with `ticket retention sweep: removed 1 expired ticket(s)` in the profile
      daemon log.
      Two adversarial passes found three permanent-brick bugs and one unbounded
      worktree leak, all from treating a run as privately owning its resources
      or a thread as living forever; each is fixed with regression tests, and
      the shared-identity and thread-lifetime invariants are recorded above as
      domain context.
- [x] Add the self-service YAML editor and validate-without-apply (Slice 7 PR B).
      One buffer serves create and edit: create loads through the same
      `getDefinition('')` path and gets the starter template at revision 0, so
      there is no second code path to keep in step (D7). Validate runs the full
      definition check without writing anything, and Save is the only writer.
      Three mistakes are refused rather than silently absorbed — changing the
      `id` of the definition being edited (apply is keyed on the id inside the
      YAML, so a rename is a separate create), creating an automation whose
      `id` already belongs to a live definition (which would replace it
      wholesale), and saving over a definition that changed elsewhere, which
      offers Reload as the recovery path (D4/D5). The buffer is populated only
      on mount or an explicit Reload, never by an `automations_changed`
      broadcast, so a background refetch cannot stomp text being typed (D6).
      Proven by the packaged scenario `real-app:scenario-automation-editor`,
      ten legs (starter template, invalid-rejected-and-nothing-stored, create,
      create-collision refusal, comment round-trip, id-change refusal,
      stale-revision refusal followed by a successful Reload, a comment-only
      edit bumping the revision, a save refused after the definition was
      deleted elsewhere, and a panel toggle-off surviving a later edit), with
      the daemon's own state cross-checked through the bundled CLI at every
      leg — run `automation-editor-2026-07-21T01-31-48-079Z` against the app
      built from `09cfb0b6` (2026-07-21, protocol 180, profile `togglefix`),
      all ten green.
      D1 (comments survive)
      is proven by the stored `SpecYAML` still carrying the run's
      `# harness-marker:` line after the save/reload round-trip; definitions
      created before migration 75 have no stored YAML and re-render comment-free
      on first open, which the editor says in-line rather than hiding.
      Reviewing the product code directly — not the subagent reports — found
      three defects the tests had not caught: a Reload button offered while
      creating (it would have re-fetched the starter template over the draft
      being typed), validate running inline on the client read loop where
      `git.ValidateLocalClone` stats paths and shells out to git twice per
      override, and a create silently overwriting a live definition that shared
      its id. The Reload gate and the collision guard each carry a regression
      test, and the collision guard was mutation-verified: without it the create
      succeeds and bumps the victim to revision 2. The read-loop fix carries no
      test — it moves a handler onto its own goroutine, and a test at that seam
      would assert Go's scheduling rather than any behavior of ours.
      An adversarial gate over six dimensions then found five further defects,
      all fixed here with mutation-verified regression tests. Three were the
      same mistake in different clothes — trusting a value that another writer
      can move underneath you. The revision counter did not cover `spec_yaml`,
      so a comment-only save bumped nothing and the stale-save guard was blind
      to precisely the edits this editor exists to make. `DeleteAutomationDefinition`
      likewise leaves `revision` untouched, so a stale editor's Save passed the
      guard and silently resurrected a definition someone had deleted — live,
      enabled, and for a cron trigger firing unattended sessions again, reported
      to the user as a successful save. And a Validate response that arrived
      after further typing overwrote the cleared state, rendering "Looks good."
      against text the daemon never saw. The other two: edits typed during an
      in-flight Save were discarded when the editor closed, and
      `attn automation validate` printed `null` on success because
      `json.Marshal` of a nil payload yields the literal `null`, which defeats
      `omitempty` and leaves the client decoding a non-nil four-byte `Data`.
      Review then found a third instance of that same trusting-a-moving-value
      mistake, and this one had two writers rather than one stale reader:
      `enabled` was written both by the panel's toggle (the column alone) and
      by a Save (from the YAML), with nothing keeping them in step. Disabling
      an automation and then editing it — even only adding a comment — saved
      the stale `enabled: true` back and re-ran the real enable transition,
      reported as an ordinary successful save; for a cron trigger that meant
      unattended sessions firing after the operator had turned them off. Line
      501 of this guide already settles the authority question — `enabled` is
      current management state, so the column is the single authority — which
      ruled out a read-time overlay: that would leave the row permanently
      inconsistent and oblige every future reader to remember to overlay.
      `SetAutomationEnabled` now writes through on a real transition,
      re-marshaling `spec_json` and rewriting `spec_yaml` byte-for-byte via
      `automation.SetEnabledInYAML`, which locates the top-level `enabled`
      scalar by its parsed yaml.v3 Line/Column and replaces only that token, so
      comments, key order, and quoting survive — the guarantee D1 depends on.
      It bumps `revision` for the same reason the two earlier instances did.
      That bump was verified safe rather than assumed: occurrence keys are
      time- and subject-based and never embed the revision, and
      `Snapshot.DefinitionRevision` is run provenance only, so a bump can
      neither re-fire nor duplicate a run. Covered at three levels, each
      mutation-verified against a build with the write-through removed:
      `TestSetAutomationEnabledWritesThroughToStoredSpec` (store),
      `TestAutomationDefinitionGetWSAfterToggleShowsDisabledWithoutReenabling`
      (the daemon's real WS handlers), and scenario leg 10 above, where the
      leg's headline assertion — editing a disabled automation does not
      silently re-enable it — was isolated and confirmed red under that build
      (`automation-editor-2026-07-21T01-30-23-763Z`) rather than merely
      failing at an earlier diagnostic. A toggle landing while the same
      editor is open is deliberately not in the scenario: `AutomationsPanel`
      renders the editor as a full replacement of the panel body, so the
      toggle control is not in the DOM at all then, and there is no way to
      drive that race through the real UI.
- [ ] Run the final upgrade, failure-recovery, and packaged-app matrix from the
      integration branch and open the single integration PR to `main`.

When implementation forces a choice that changes the product behavior described
in the vision, stop and update the vision by agreement. When it only changes code
shape while preserving these invariants, record the reason here and continue.
