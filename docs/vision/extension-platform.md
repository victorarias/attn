# Extension platform

Status: DIRECTION AGREED. Build sequence:
[docs/plans/2026-08-01-extension-platform-roadmap.md](../plans/2026-08-01-extension-platform-roadmap.md).

attn becomes self-extensible: agents running inside attn can extend attn itself
— custom UI, event-driven behavior, durable workflows — by writing files and
running a command. No clicking around, no daemon restart. Automations and the
plugin system were partial steps; this platform is the substrate underneath
them, built properly once.

## The authoring model

The platform is **authoring-time**, not chat-time. An agent writes a real
extension — React components, TypeScript handlers, workflow functions — once;
it loads into the running system and then executes repeatedly like normal
software. This is deliberately the opposite of the generative-UI pattern the
industry converged on (Tambo, CopilotKit, Vercel AI SDK: a chat-time model
selects from a registered component catalog and fills schema-validated props).
That pattern exists because chat-time models cannot ship code. Our authors can.

Two lessons drive this choice:

1. **Agents are excellent at React/TypeScript and bad at bespoke schemas.** A
   custom YAML/JSON data model has no training data and gets relearned from
   docs every time. Present is the in-house proof: its strict YAML manifest
   needed a 446-line authoring reference, and most of its validation surface
   is schema hygiene that real code would make unnecessary.
2. **The validation loop must be one agents are good at.** Replacing
   YAML-with-docs by TSX-with-types moves errors from "reread the reference"
   to "read the compiler error." `tsc` against the SDK types at apply time is
   the gate.

An extension is a directory: a small manifest plus any of {UI components,
event handlers, workflow functions, storage namespace, hook claims}.
Registration is `attn ext apply` — validate, build, register, effective
immediately. Chat-time props-filling over registered components may arrive
later as a convenience; it is not the platform.

## The six primitives

### 1. Event bus (durable log)

A SQLite-backed event log with per-consumer cursors and at-least-once
delivery. An extension that was down catches up on restart — this is the
reliability floor for code that runs persistently and unattended. The pattern
already exists in miniature (`ticket_events` + `ticket_event_cursors`);
the bus generalizes it.

The bus is attn's **internal spine, not an overlay**: the ~50 hand-written
`broadcastXxx` methods migrate onto it (staged mechanically, but committed
scope), and the WebSocket fan-out becomes one ephemeral consumer of the log.
Extensions and core features consume the same events.

### 2. Hook points (interception)

Pub/sub is observe-only; some compositions must *intercept* — e.g. parking a
delegation until a human approves its prompt. Hook points are a separate
primitive: named, deliberately added to core one by one, claimable by
extensions. Each claim declares its failure policy — **fail-open or
fail-closed plus a timeout** — so a dead gate extension parks delegations
(fail-closed, long timeout) while a dead cosmetic enricher is skipped in
seconds (fail-open). The provider-plugin `handled | decline | error` surface
is the precedent. The first hook point is delegation pre-start.

### 3. Durable job queue

A River/Sidekiq-style queue replaces `internal/tasks`: durable jobs, retries
with backoff, scheduling, priorities, cron. Existing task kinds
(`compact_context`, `summarize_session`, `narrate_workspace`, `reconcile`)
migrate onto it — the queue is core infrastructure that extensions also get.

### 4. Workflow layer (signals, timers, replay)

A thin layer on the queue for long-lived flows, reusing the deterministic
replay design proven in `internal/workflow` (ordinal-addressed journal,
hash-based cache identity), with the harness implemented in the shared Bun
runtime and journal authority in the daemon. Added on top: **signals**
(external input into a running flow), **durable timers**, daemon-supervised
**auto-resume** after a crash, and generic activities beyond `agent()` — the
queue executes activities. A saga is one linear JS function:

```
on delegation.requested → park via hook → write approval record →
await signal(verdict, timeout: 24h) → release or reject
```

Simple reactions don't need replay: plain event handlers are the cheaper
altitude, workflows are for genuinely stateful flows. The existing `attn
workflow` agent fan-out feature becomes one consumer of the generalized
engine, not the definition of it.

### 5. Storage (documents + live queries)

Per-extension namespaced JSON document store with secondary indexes. No
migrations for authors — record-shape evolution is handled at the read
boundary (zod parse, tolerant rendering). Writes automatically emit change
events on the bus, and UI subscribes to **live queries** that re-render on
change. This closes the loop that makes the platform compose:

> workflow writes a record → UI updates → user clicks → command/signal →
> workflow proceeds.

Namespaced SQL tables remain a documented escape hatch for later, once a real
extension proves the need.

### 6. UI host

The daemon bundles extension TSX (esbuild) at apply time; the app
dynamic-imports the bundle and mounts it in tiles/panels. The workspace
layout's `tile_kind` + `tile_params` are already daemon-opaque — only the
frontend's hardcoded tile renderer becomes a registry lookup. Each extension
mounts behind an error boundary; repeated runtime failures auto-disable the
extension in the registry.

Extensions import **only** the versioned `@attn/ext` SDK: storage, events,
commands, live-query hooks, and a curated slice of the design system
(buttons, lists, markdown renderer, diff viewer, …) so extensions look
native. Extensions never import app internals — app refactors must not break
extensions silently, and the SDK is the one package an authoring agent needs
to read.

The protocol stays rigid: a small set of generic envelopes (extension event,
extension command, extension UI payload) is added once; extension-specific
typing lives in the SDK layer. Extensions never touch TypeSpec.

## Execution model

Hybrid, defaulting to cheap:

- **Extensions-as-data (default).** Components, handlers, and workflows are
  registered artifacts executed inside one shared, supervised runtime. No
  per-extension process; creating an extension is writing files and running
  `attn ext apply`.
- **The shared runtime is a daemon-supervised Bun sidecar.** Agent-written
  code never executes inside the daemon process — the same reasoning that
  puts production PTYs in worker processes. Bun gives extensions real
  TypeScript, npm, and Web/Node APIs (a goja dialect would recreate the
  bespoke-schema problem in runtime form), and it hosts the apply toolchain
  too (`tsc` typecheck + `Bun.build` bundling). The workflow replay harness
  is implemented in TypeScript inside this runtime; **journal authority
  stays in the daemon** (as it already does via the IPC journal), so a
  sidecar crash costs nothing — the supervisor restarts it and replay
  resumes from the journal.
- **Dedicated process (escape hatch).** Heavy extensions opt into the
  existing plugin chassis — supervised out-of-process, JSON-RPC 2.0 over the
  unix socket, restart with backoff, manifest + API version. The chassis is
  retained, not replaced; its agent-driver surface stays as-is.

Local-only in v1: extensions run on the local daemon. Remote sessions remain
visible because the hub relay feeds their events into the local bus; running
extension code on remote endpoints is designed-for but deferred.

## Versioning and reload

The core rule: **versions are immutable and content-addressed; state lives in
storage, never in code.**

- `attn ext apply` builds a new immutable version (tsc → esbuild →
  content-addressed artifacts → version row) and **atomically flips** the
  "current" pointer. Any failure before the flip leaves the old version
  live — a broken edit cannot take down a working extension.
- `attn ext rollback` flips the pointer back. The invocation log stamps every
  invocation with the version that served it.
- **UI: remount, not HMR.** Bundles are served at content-hashed URLs (no ESM
  cache games); the app unmounts old, imports new, remounts. Because UI is a
  projection of live queries, remount re-hydrates in milliseconds. No
  react-refresh across dynamic bundles.
- **Handlers drain.** New deliveries dispatch to the new version; in-flight
  invocations finish on the old one (bounded grace, then cancel — the queue
  redelivers to new code). Subscription and hook changes are a manifest diff;
  new subscriptions start their cursor at apply time, not from history.
- **Workflow runs pin to the version they started on.** Replay against
  changed code diverges (the Temporal versioning problem); pinning sidesteps
  it. Old runs finish on their pinned bundle (content-addressing makes this
  free), new runs start on current. No live-migration API in v1 — cancel and
  restart a run to move it.
- **Dev loop:** `attn ext dev <name>` watches the extension directory,
  auto-applies on save, and streams the invocation log and errors to the
  terminal. Agent iteration is edit → live in the app in ~a second → error in
  its own terminal. The app shows a reload badge when a tile swaps.

## Reliability and observability — not a security model

Agents inside attn already have a shell; routing through an extension grants
a compromised agent nothing new. The risk that matters is **reliability**: an
extension runs persistently and automatically, so a broken one misbehaves
longer than a session-bound agent. The posture:

- **Invocation log** — every handler/workflow/UI invocation recorded with
  extension, version, duration, outcome, error.
- **Kill switch** — disable an extension instantly; the enabled bit lives
  only in the DB (the automations discipline: never in the files, so an
  errant `apply` cannot re-enable).
- **Auto-disable** — repeated runtime failures mark the extension unhealthy
  and stop mounting/dispatching it, surfaced in the app.
- **Error boundaries** — a crashing component dies at the tile, not the app.

No approval gate before an agent's extension goes live; log + kill switch is
the contract.

## Teachability — knowledge delivery to future agents

The platform's primary developers are future agents, not humans, so how the
principles reach them is a first-class design surface. The delivery ladder,
strongest first — **every rule lives in the layer that can enforce it, and
prose only gets what nothing can enforce**:

1. **SDK type surface.** Encode principles as API shape: the SDK offers no
   persistence except the doc store; the workflow context permits side
   effects only through activities (`ctx.run`, `ctx.signal`, `ctx.sleep`), so
   determinism is the path of least resistance; live-query hooks are the
   obvious way to feed a component. A principle enforced by the API doesn't
   need to be known.
2. **Scaffold + canonical examples.** `attn ext new <name>` generates a
   working skeleton with the patterns baked in. The proof compositions
   (below) double as canonical examples in the extensions repo.
3. **The extensions repo's own agent instructions.** Extensions live in a
   dedicated git repo; agents author inside it, and agent harnesses load the
   repo's instructions automatically. The scaffold creates `AGENTS.md` (the
   real file, read by Codex) plus a **`CLAUDE.md` symlink to it** (read by
   Claude). This is the highest-leverage placement: the guide lives where the
   authoring happens — no skill invocation, no memory recall.
4. **Errors that teach.** Apply-time refusals and runtime failures explain
   the principle at the moment of violation — e.g. a replay divergence says
   "workflow code changed under a running run; runs pin to their start
   version — cancel and restart to move it," not a bare hash mismatch.
5. **Prose, last and small.** A short `ext` reference in the embedded attn
   skill stating only the principles the layers above can't enforce
   (state-in-storage, remount-not-HMR, run pinning, cursor-from-now), plus a
   pointer to the scaffold. Hard budget: if it trends toward Present's 446
   lines, that is the API telling us it has a sharp edge to file down — fix
   the API, not the doc.

## The extensions repo

One dedicated git repo holds all extensions. `attn ext apply` registers from
it; the DB holds registered state (enabled bit, current version, version
history). Git gives diffing, revert, and after-the-fact review for free —
consistent with the log-and-kill-switch posture. `attn ext new` scaffolds a
new extension directory including the repo-level agent instructions if
missing.

## Proof compositions

Compositions validate the primitives; the platform is not organized around
either of them.

1. **Delegation approval gate (first).** Any agent can delegate with a
   backing ticket, and agent-written initial prompts often go wrong. The gate
   shows the prompt before the delegated session starts: approve, or reject
   with feedback. Composition: delegation pre-start hook (fail-closed,
   timeout) → workflow parks the operation and writes an approval record →
   live-queried UI panel shows pending prompts → verdict is a signal → the
   workflow releases or rejects. Exercises hooks, workflows, signals, timers,
   storage, live queries, and UI — with almost no bespoke plumbing. Requires
   one small core change: the delegation operation state machine (already
   durable and crash-resumable) gains a parked state behind the hook point.
2. **Present v2 (second — absorb and replace).** What survives from Present
   is thin and portable: SHA pinning + drift detection, the round/verdict
   loop, the handback to the agent (ticket comment or PTY doorbell),
   `--wait`, and the Tour/Other/Skipped grouping. On the platform, a
   presentation stops being a strict YAML manifest: an agent writes the
   *presentation extension* once (viewer component + record shapes), and
   presenting becomes writing content records into storage. "Only does
   diffs" stops being structural — a stop is whatever a registered stop
   renderer can draw (diff, file, markdown, image, HTML artifact) — and a
   driver agent can append/modify stops live while the user watches. Old
   Present is deleted at parity on the parts actually used.

## Relationship to existing features

| Existing | Fate |
|---|---|
| `internal/tasks` | Replaced by the durable job queue; task kinds migrate. |
| `internal/workflow` engine | Generalized (signals, timers, auto-resume, generic activities); the agent fan-out feature becomes one consumer. |
| `broadcastXxx` / wsHub | Broadcasters migrate onto the bus; wsHub becomes an ephemeral consumer of the log. |
| Plugin system | Retained as the dedicated-process escape hatch and the agent-driver surface. Surfaces gain registries instead of hardcoded switch arms. |
| Automations | Not deprecated — they work and stay. Longer term the scheduler can publish onto the bus and automations become bus consumers, but nothing forces that migration. |
| Present | Absorbed and replaced (proof composition 2). |
| Docked tiles | The persistence seam is already generic; the frontend renderer becomes a registry lookup and hosts extension UI. |

## Non-goals

- Chat-time generative UI (props-filling catalog) — at most a later
  convenience on top.
- Security sandboxing of extensions — see reliability posture above.
- Marketplace, multi-tenancy, third-party distribution.
- Remote execution of extension code in v1.
- HMR state preservation for extension UI.
- Live migration of running workflow runs across code versions.

## To be decided in stage plans

Build sequence is decided (see the roadmap linked above), and the shared
runtime is decided (Bun sidecar, above). Still open, owned by the stage that
needs each answer — settled by discussion with Victor and his approval
before that stage's implementation begins (the roadmap's "Stage gates"
section lists them per stage):

- Doc-store index design and query surface.
- `@attn/ext` packaging and how the SDK types are delivered into the
  extensions repo.
- Import-map management for shared React across host and extension bundles.
- The exact generic protocol envelopes.
- Migration of the `attn workflow` fan-out feature from the Go/goja engine
  to the TypeScript replay harness — a follow-up after the workflow layer,
  not part of it.
