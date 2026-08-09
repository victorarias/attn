# Extension platform — build roadmap

Status: AGREED. North star: [docs/vision/extension-platform.md](../vision/extension-platform.md).

This is the stage-level roadmap: what gets built, in what order, and which
existing feature consumes each primitive as it lands. Each stage gets its own
detailed implementation plan when it starts; this document fixes sequence,
scope boundaries, and exit criteria.

## Sequencing decisions

- **Two parallel tracks.** The dependency graph splits into a spine track
  (bus → storage → registry → UI) and an execution track (queue → workflow
  layer). They converge at the delegation approval gate.
- **The gate is built once, on the full stack.** No degraded interim version
  on plain handlers; the gate waits for hooks, workflows, storage, and UI to
  all exist.
- **UI host stays late in track A.** Nothing mounts in the app until the
  registry underneath it is real — the first mounted tile is already a
  versioned, killable extension, not demo-ware.
- **Broadcaster migration happens all at once**, as a dedicated stage
  immediately after bus core — not spread across the project. The bus is the
  spine from early on, and the claim is honest before anything else builds
  on it.

## Stage gates — questions Victor approves before implementation

Every stage starts with a design discussion, not code. The questions below
(plus any direction-level question that surfaces later) are answered **by
discussing with Victor and getting his approval** before that stage's
implementation begins. The stage's detailed plan records the answers.

- **A1 (bus core):** event envelope and naming taxonomy; cursor semantics
  (per-consumer registration, lag visibility); log retention/compaction
  policy; what "ephemeral consumer" means for wsHub concretely.
- **A2 (broadcaster migration):** none expected — pattern is fixed by A1;
  escalate only if a broadcaster doesn't fit the pattern.
- **A3 (doc store):** index design; the query surface (filter/sort/limit
  shape); live-query subscription contract; namespace naming.
- **A4 (registry + runtime):** manifest shape; `attn ext` CLI surface;
  extensions repo name/location; invocation-log schema; auto-disable
  thresholds; sidecar supervision/restart policy.
- **A5 (UI host + SDK):** which design-system components the SDK re-exports;
  `@attn/ext` packaging and how its types reach the extensions repo;
  import-map management for shared React; the generic protocol envelopes;
  where extension UI can mount (tiles only, or panels/windows too).
- **B1 (queue):** build vs adapt (River is Postgres — SQLite likely means
  our own); job semantics (priorities, per-kind concurrency, uniqueness/
  coalescing — what carries over from `internal/tasks`); cron ownership vs
  the automations scheduler.
- **B2 (workflow layer):** the workflow `ctx` API surface; determinism
  enforcement (what's blocked vs linted vs documented); signal addressing
  and delivery guarantees; timer granularity.
- **C1 (hook points):** hook claim/registry semantics; single vs multiple
  claimants per hook; the delegation parked-state UX (what the app shows
  while parked).
- **C2 (gate):** the approval UX itself (where the panel lives, what
  reject-with-feedback delivers back to the delegating agent); **app
  capability discoverability** — how an agent in an unrelated session
  learns an installed app's surface exists. Leading idea: a
  manifest-declared agent entry (one-liner + instructions doc, frozen in
  the version snapshot, apply still evaluates nothing) that attn
  materializes into the harness's native skill mechanism at session launch
  — one-liner always in context, body on demand, removed on
  disable/remove. With it, the CLI shape for invoking an app's actions
  (something like `attn app run <name> <action>`; undesigned), and a
  scoping question that needs a receipt before apps are numerous: do all
  enabled apps' one-liners belong in every session's context, or does the
  index scope per workspace/repo?
- **C3 (Present v2):** record shapes for presentations/stops; parity
  checklist defining what "the parts actually used" means before old
  Present is deleted.

## Track A — the spine

### A1. Event bus core

Durable SQLite-backed event log; publish API; per-consumer cursors with
at-least-once delivery; subscribe surface for in-daemon consumers and (later)
the shared extension runtime. The WebSocket hub becomes an ephemeral consumer
of the log rather than the publishing path. A handful of broadcasters migrate
inside this stage to prove the pattern end to end.

- Consumes it: wsHub (as consumer), the first migrated broadcasters.
- Exit: log + cursors durable across daemon restart; a downed consumer
  catches up; the migration pattern is documented for A2.

### A2. Broadcaster migration (all of it)

Migrate the remaining ~50 `broadcastXxx` methods onto the bus and delete the
direct wsHub publishing paths. Mechanical, high-volume, independently
verifiable per broadcaster — suited to fanned-out delegations under one
tracked stage.

- Exit: no daemon code publishes to clients except through the bus. Frontend
  behavior unchanged (same events on the wire).

### A3. Document store + live queries

Per-extension namespaced JSON document store. Writes emit change events on the
bus. Live-query subscription surface: a client (or extension UI) subscribes to
a query and receives updates on change.

- Depends on: A1 (change events ride the bus).
- Exit: write → change event → subscribed query update, durable across
  restart; namespace isolation enforced.
- Plan: [2026-08-03-ext-a3-doc-store.md](2026-08-03-ext-a3-doc-store.md). The
  gate answered indexes with **declared fields, scanned**; measurement then
  overturned scan-first before anything wrote to the store, and
  **A3.1** rebuilds the physical schema — a table per collection with an
  indexed virtual column per declared field, inside `attn.db` — with the
  query surface unchanged:
  [2026-08-03-ext-a3.1-doc-store-physical-schema.md](2026-08-03-ext-a3.1-doc-store-physical-schema.md).
  A3.1 lands before A4, because A4 is where extensions start declaring
  collections.

### A4. Extension registry + shared runtime

The moment "an agent can extend attn" becomes literally true, headless:

- Extension manifest; `attn ext` CLI: `new`, `apply`, `rollback`,
  `enable`/`disable`, `logs`, `dev` (watch + auto-apply + streamed log).
- Apply pipeline: tsc against SDK types → `Bun.build` → content-addressed
  immutable version → atomic current-pointer flip. Failure before the flip
  changes nothing.
- Shared supervised runtime executing handler code; event subscriptions from
  the manifest; handler drain on version flip; cursor-from-now default.
- Invocation log (version-stamped), kill switch (enabled bit DB-only),
  auto-disable on repeated failure.
- Extensions repo scaffold: `AGENTS.md` (real file) + `CLAUDE.md` symlink.

- Depends on: A1, A3 (handlers consume events, persist to the doc store).
- Substrate (decided): a daemon-supervised **Bun sidecar** hosts the shared
  runtime and the apply toolchain (`tsc` + `Bun.build`). Agent-written code
  never executes inside the daemon process — the PTY-worker reasoning
  applied to extensions.
- Exit: an agent writes a headless extension in the extensions repo, applies
  it live, sees invocations in the log, kills it, rolls it back.

### A5. UI host + `@attn/ext` SDK

Tile renderer becomes a registry lookup; daemon serves content-hashed
extension bundles; app dynamic-imports and mounts behind per-extension error
boundaries; remount-on-reload; reload badge. The SDK package: storage,
events, commands, live-query hooks, curated design-system slice. Generic
protocol envelopes (extension event / command / UI payload) added once.

- Depends on: A4 (bundles are versions in the registry), A3 (live-query
  hooks).
- Exit: an agent-authored tile renders live data via a live query, hot
  reloads on `attn ext dev` save, and dies at its boundary without harming
  the app.

## Track B — execution

### B1. Durable job queue

River-style queue replacing `internal/tasks`: durable jobs, retries with
backoff, scheduling, priorities, cron. The four existing task kinds
(`compact_context`, `summarize_session`, `narrate_workspace`, `reconcile`)
migrate; `internal/tasks` is deleted at parity.

- No dependency on track A; can start immediately in parallel with A1.
- Exit: task kinds run on the queue with equivalent behavior;
  `internal/tasks` removed.

### B2. Workflow layer

Signals, durable timers, daemon-supervised auto-resume, and generic
activities (executed by the queue). The deterministic-replay harness is
implemented in TypeScript inside the Bun runtime, reusing the journal design
proven in `internal/workflow` (ordinals, hashes, divergence); journal
authority stays in the daemon (the IPC-journal pattern). Workflow runs pin
to the code version they started on. Migrating the `attn workflow` fan-out
feature off the Go/goja engine is a follow-up after this stage, not part of
it — the Go engine keeps serving fan-out until then.

- Depends on: B1 (activities), A4's Bun sidecar.
- Exit: a workflow parks on `await signal` with a durable timeout, survives
  daemon restart, resumes automatically, and completes on signal delivery.

## Convergence

### C1. Hook points + delegation pre-start hook

The hook-point primitive: named interceptable points, claims declaring
fail-open/fail-closed + timeout. First hook: delegation pre-start — the
delegation operation state machine gains a durable parked state behind the
hook (it is already crash-resumable; the WS delegate path must return the
parked operation instead of polling to completion).

- Depends on: A4 (extensions claim hooks).
- Exit: a claimed hook parks a delegation durably across daemon restart;
  an unclaimed or timed-out hook follows its declared failure policy.

### C2. Proof 1 — delegation approval gate

Built once, on the full stack: pre-start hook (fail-closed) → workflow parks
the operation and writes an approval record → live-queried panel shows
pending prompts → approve/reject-with-feedback is a signal → workflow
releases or rejects. Lives in the extensions repo as the first canonical
example.

- Depends on: A5, B2, C1.
- Exit: a delegated prompt is held, shown, and released/rejected end to end;
  the gate survives daemon restart while parked; killing the gate extension
  applies its declared hook policy.

### C3. Proof 2 — Present v2 (absorb and replace)

Presentation extension per the vision: records + viewer component; keeps SHA
pinning/drift detection, rounds/verdict, agent handback, `--wait`,
Tour/Other/Skipped. Old Present deleted at parity on the actually-used parts.

- Depends on: C2 shaking out the platform.
- Exit: real reviews run on Present v2; old Present code, tables, and the
  446-line authoring reference are gone; the platform's `ext` skill
  reference stays small.

## Cross-cutting

- **Teachability lands with each stage**, not at the end: SDK types (A5),
  scaffold + repo instructions (A4), teaching errors (every stage), the
  short `ext` skill reference (C2, once the gate proves the surface). All
  of that teaches *authoring*; how a consuming agent discovers an installed
  app's capability is its own surface, gated at C2 (see Stage gates).
- **Verification** per repo policy: every stage that touches daemon
  lifecycle, protocol, or UI needs live verification from a non-production
  profile; A2 additionally needs evidence that wire behavior is unchanged.
- **Changelog** entries only where behavior is user-visible (A5 onward,
  C2, C3).
