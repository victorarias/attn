# Plan: attn extension platform — the layers

## Goal

Build the infrastructure layers an agent inside attn composes to create
interactions attn does not have. None of these layers is novel; that is the
point. They are well-understood pieces, built properly once, so that the next
twenty ideas are compositions instead of core PRs.

Deliberately a bit over-built: a foundation to build on, not a minimum.

## The layers

```text
  ┌──────────────────────────────────────────────────────────────┐
  │ 6. HOST / PLUGIN SYSTEM                                       │
  │    processes, supervision, RPC, registration, hot reload      │
  │    — connects extension code to every layer below             │
  └──────────────────────────────────────────────────────────────┘
  ┌───────────────────────────┐  ┌───────────────────────────────┐
  │ 1. EVENT BUS              │  │ 4. RENDERING                  │
  │    named events, typed    │  │    bundle, load, mount        │
  │    payloads, subscriptions│  │    agent-authored React       │
  └───────────────────────────┘  ├───────────────────────────────┤
  ┌───────────────────────────┐  │ 5. COMPONENT LIBRARY          │
  │ 2. DURABLE WORKFLOW       │  │    provided React components  │
  │    (Temporal-style)       │  └───────────────────────────────┘
  │    workflows / activities │
  │    signals / timers       │
  └───────────────────────────┘
  ┌──────────────────────────────────────────────────────────────┐
  │ 3. STORAGE                                                    │
  │    extension state, workflow histories, interaction records   │
  └──────────────────────────────────────────────────────────────┘

  cross-cutting: STABILITY · OBSERVABILITY · HOT RELOAD
```

### 1. Event bus

Named events with typed payloads, an open subscription registry, and delivery
accounting. Today attn has ~100 daemon broadcasts addressed to the app, four
closed plugin surfaces (`validatePluginSurfaces`), and three closed automation
triggers — three private buses, none subscribable.

Owns: the catalog (name, payload schema, version), subscriptions, delivery,
per-event delivery records for replay and debugging.

Does **not** own: whether anything blocks. That is the workflow layer's
business — an event is a fact, not a request.

### 2. Durable workflow engine (Temporal-style)

The layer that makes the others compose, and the one attn is closest to already
having without realizing it.

- **Workflow** — deterministic, replayable orchestration. Survives daemon
  restarts by replaying its history. No clock, no randomness, no direct I/O.
- **Activity** — an effectful step: call an attn command, hit an API, spawn an
  agent. Unconstrained, retried with backoff, result journaled.
- **Signal** — external input that wakes a waiting workflow. *A human answering
  a question is a signal.*
- **Timer** — durable sleep. Timeouts, reminders, "wait three days."

**Why this is the keystone.** Without it, "wait for the human, then continue"
is a parked in-memory continuation that dies on every `make install-daemon-dev`.
With it, waiting is `await signal(...)` — restart-safe by construction, because
resume replays history and the await resolves from the journal. Every awkward
question in earlier drafts (what happens on restart, how a paused operation
resumes, where timeouts live, how to retry) is answered once, here, instead of
per feature.

**What attn already has.** `internal/workflow` is a durable execution engine:
the journal is keyed by *structural ordinal* rather than time; `DurableJournal`
is SQLite-backed and rebuilds from persisted rows "so a fresh process can resume
a prior run"; determinism bans keep replay faithful; the engine runs
out-of-process and journals to the daemon over IPC (`workflow_ipcjournal.go`) —
Temporal's worker model.

To be precise about the distinction: the `attn workflow` **feature** is agent
fan-out and is not what this layer is for. The **machinery** underneath it is
generic durable execution and is exactly what this layer needs.

**What is missing.** One activity type (`agent()`) and nothing else; no signals;
no timers; a journal entry schema that is agent-call-shaped (`PromptHash`,
`Model`, `Phase`) rather than a generic history. Generalizing the entry from
"agent call" to "activity result", then adding signals and timers, is the work.

**This corrects an earlier call.** A previous draft moved everything to bun and
called the determinism bans goja baggage. They are not — they are
durable-execution semantics, the same constraints Temporal places on workflow
code, for the same reason. The split is Temporal's own:

| Code | Runtime | Constraints |
| --- | --- | --- |
| Workflow (orchestration, awaits, signals) | goja + journal | deterministic, replayable |
| Activity (side effects) | bun | none — full npm, fetch, fs |
| Handler (stateless event reaction) | bun | none |

So goja *and* bun, each where its semantics are right.

### 3. Storage

Durable state per extension, plus the workflow histories and interaction
records the layers above depend on. SQLite via `internal/store`.

Extension-facing state needs serialized read-modify-write (two events in flight
doing get-then-set is the obvious first bug), namespacing, and a declared
version so an extension can migrate its own data.

### 4. Rendering

How agent-authored React gets into attn: bundling, loading into the packaged
Tauri app with one React instance, mounting at a declared placement, error
isolation, hot reload.

**The only layer with genuine unknowns.** Dynamic `import()` of a bundled ESM
module through the asset protocol in a packaged WKWebView, with `react`
externalized so extension hooks share the app's instance, is unproven.
`build:browser-runtime` is a first-party bundle built at app build time, which
is not the same thing. Spike before building.

### 5. Component library

Provided React components so extension UI looks and behaves like attn without
each extension reinventing it. Starts tiny and grows from real use — a
component library with no consumers is how the Present manifest failed, in
library form.

### 6. Host / plugin system

Process lifecycle, supervision, RPC, registration, hot reload — and the wiring
that gives extension code access to layers 1–5.

Largely exists: `plugin_rpc.go` (JSON-RPC, handshake, generation tokens,
priority-ordered registry), `plugin_supervisor.go` (crash restart with bounded
backoff, which is hot reload), `sdk/plugin` (authoring SDK). What it lacks is
reach: today it connects to four worktree surfaces and nothing else.

### Cross-cutting principles

- **Stability.** Nothing an extension does takes attn down. Extension code runs
  out-of-process (handlers, activities) or sandboxed-by-semantics (workflows);
  UI faults are isolated per extension; every wait has a declared timeout and
  default; a kill switch is reachable without the extension's cooperation.
- **Observability.** Every event delivery, workflow step, activity retry,
  signal and UI mount is recorded and inspectable. A stuck extension is
  diagnosable in one look, never inferred from a stalled operation.
- **Hot reload.** Edit, and it is live — no daemon restart, no app rebuild, no
  clicking. This is a platform requirement, not a convenience: an agent
  iterating on an extension is the primary authoring loop.

## What exists, per layer

| Layer | Today | Work |
| --- | --- | --- |
| Event bus | 3 private closed buses | open + unify |
| Durable workflow | replay core, durable journal, out-of-process worker | generalize activities; add signals, timers |
| Storage | SQLite store | extension tables |
| Rendering | tiles, `PresentRoot` window | build it (spike first) |
| Components | Present's diff rendering | build it, small |
| Host/plugin | RPC, supervision, SDK | widen its reach |
| Observability | per-subsystem logs | unify across layers |

Two of seven exist and are good. One is ~60% there and is the keystone.

## Composition check

The layers are right if what attn already has falls out of them, and the new
ideas do too:

```text
automations            = events + activity(spawn agent)
worktree plugins       = events + workflow(blocking activity)
present                = rendering + components + storage
delegation approval    = events + workflow(signal) + rendering + components
nightly digest         = events + timer + activity + storage
"wait for CI, then ask,
 then delegate"        = events + timers + signals + activities
```

The last one is the tell: it is trivial with a durable workflow layer and
essentially impossible without it.

## Implementation Steps

- [ ] **PR1 — event bus.** Catalog, open subscription registry, delivery
      records, `attn ext` registration on the existing host, hot reload.
      Handlers in bun. *An extension reacts to things and does things.*
- [ ] **PR2 — storage.** Extension state with serialized read-modify-write,
      namespacing, versioning.
- [ ] **PR3 — durable workflow, part 1.** Generalize the journal entry from
      agent-call to activity-result; general activities; workflow start/resume
      driven by events. *An extension survives a daemon restart mid-task.*
- [ ] **PR4 — durable workflow, part 2.** Signals and timers. *An extension can
      wait — for a human, for a clock, for anything — durably.*
- [ ] **PR5 — rendering.** After the spike: bundling, loading, mounting,
      placement, error isolation, hot reload.
- [ ] **PR6 — component library v0.** Grown from what PR5's first consumers
      actually need.
- [ ] **PR7 — blocking events.** Generalize `dispatchWorktreeCreateProvider` so
      an event can await a workflow's verdict; verdicts carry data
      (`continue` | `continue_with(data)` | `stop(data)`), since
      `worktree.create` already returns `(path, branch)` and prompt approval
      wants edit-and-approve.
- [ ] **PR8 — observability surface.** One view across events, workflows,
      activities and extensions.

The delegation prompt approval is the composition that falls out once PR7
lands. It is the proof, not the plan.

## Testing

`attn ext invoke <ext> --event <name> --payload <file>` runs a handler or
workflow against a fixture without firing a real event, and workflow histories
replay deterministically by construction — a recorded history is a test case.
Otherwise an agent developing against `delegation.before_start` debugs by
spawning real delegations with real worktrees.

## Verification

Touches daemon lifecycle, protocol, persisted state and UI, so live verification
in a running non-production app per AGENTS.md from PR1 on.
