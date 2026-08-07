# A4 — App registry and shared runtime

Stage A4 of the extension-platform roadmap
([2026-08-01-extension-platform-roadmap.md](2026-08-01-extension-platform-roadmap.md)),
designed 2026-08-06 with Victor. This document records the gate answers and the
build plan. The moment it delivers: an agent writes a small automation, applies
it live, watches it run, kills it, rolls it back — without touching attn's
source or restarting anything.

## The rename: apps, not extensions

"Plugin" and "extension" are synonyms in every ecosystem; nothing in the words
carries attn's actual distinction. Decided: the user-land unit is an **app**.

- A **plugin** integrates the outside world into attn: agent drivers,
  worktree hooks. Its own supervised process, dials the daemon, installed
  rarely, effectively part of the platform. A device driver.
- An **app** is automation built on attn's platform: consumes domain facts
  from the bus, keeps state in its own document-store namespace, later grows
  UI tiles (A5), hook claims (C1), workflows (B2). Runs inside the one shared
  supervised runtime, content-addressed versions, invocation log, kill
  switch, auto-disable. Written by agents, applied live, cheap and numerous.

A plugin failing takes an integration down; an app failing gets auto-disabled
while everything else keeps running. Different trust, different rate,
different blast radius — different mechanism, on purpose. The 2026-04 plugin
plan's "one extension mechanism" promise is deliberately superseded by this
document. Both nouns get glossary entries in this stage.

Ripple, applied consistently: CLI `attn app`, document-store owner segment
`app/<name>` (the reserved class shipped as `ext/` in A3 — renamed now, while
zero real namespaces exist), bus consumer names `app:<name>`, tables `app_*`.
Plan-doc filenames keep the roadmap's `ext-` tag for lineage; new prose says
app.

## Gate answers

Each A4 gate from the roadmap, with the decision and the reasoning that has to
survive.

### Manifest shape — manifest declares, codegen types, apply never executes

The manifest is TOML, `attn-app.toml`, and declares identity plus behavior:

```toml
name = "approval-gate"        # single identity: docstore namespace app/approval-gate,
                              # bus consumer app:approval-gate, registry key,
                              # directory convention. [a-z0-9-], unique.
description = "Blocks risky delegation actions until a human approves."
attn_app_api = 1              # runtime/SDK contract version; apply refuses a
                              # manifest newer than the daemon speaks.
entrypoint = "src/index.ts"

[[subscribe]]
events = ["delegation.*", "session.state_changed"]

[[collections]]
name = "decisions"
fields = ["status", "requested_by"]
```

The rule that decides everything else: **the apply pipeline never evaluates
app code.** Declarations are static so `attn app apply` can fail before the
version flip — unknown event pattern, collection conflict, missing handler —
with nothing executed and nothing changed.

Creators still write handlers in one typed place, because **codegen derives
the handler contract from the manifest** (the house typespec pattern):
`attn app dev`/`apply` regenerate `src/generated.ts`, and the entrypoint must
satisfy it —

```ts
import type { Handlers } from "./generated"

export default {
  "delegation.*": onDelegationEvent,
  "session.state_changed": onStateChange,
} satisfies Handlers
```

— so a declared subscription without a handler, or a handler with the wrong
shape, is a tsc error with a filename and line, produced by the same tsc pass
apply already runs. Manifest↔code sync is enforced by the compiler, not by
convention, and the sync failure class costs creators nothing.

Weighed and rejected: binding handlers in the manifest by string
(`"src/handlers.ts#onX"` — stringly-typed, invisible to refactors, two places
to keep in sync per handler, and the cost lands on every creator every time);
code-as-manifest via `defineExtension({...})` extraction (best ergonomics, but
apply must execute author code — sandbox, determinism rules, a whole failure
class of "my import hung the apply". Codegen gets the same one-source-of-truth
with the arrow reversed and zero evaluation).

Extensibility: A5 adds `[[tiles]]`, C1 adds `[[hooks]]`, B2 adds
`[[workflows]]` as new tables gated by `attn_app_api`. An unknown table is a
loud apply-time error, never ignored — an app declaring capabilities the
runtime cannot honor must not half-load.

### Repo and scaffold — a mechanism, not a location

`attn app new <path>` scaffolds a self-sufficient directory anywhere:
manifest, entrypoint stub, generated types, SDK dependency wired, a real
`AGENTS.md` (with `CLAUDE.md` symlink) that makes "write me an app" a one-shot
prompt. `attn app apply <path>` works from any directory. Whether apps share
one home repo or each has its own is the user's filesystem's business; the
scaffold docs suggest a conventional home purely for muscle memory. Nothing in
attn stores or assumes a repo location.

### Registry and versions — content-addressed, snapshot-frozen

Three tables (numbers taken at PR time; migrations currently at 93):

- `apps` — name (PK), enabled bit (DB-only, the kill-switch pattern from the
  bus), current version id, timestamps. The enabled bit is deliberately
  database-only so the kill switch does not depend on the runtime it kills.
- `app_versions` — immutable rows: app name, content hash of the built
  artifact (the version's identity), the **frozen declaration snapshot**
  (subscriptions, collections — what the manifest said at apply time, the
  `automation_runs` snapshot pattern), artifact path, created_at. Never
  updated, never deleted by apply; rollback is a pointer move.
- `app_invocations` — the invocation log, version-stamped like
  `workflow_agent_calls`: app, version id (what actually ran, not the
  pointer), event seq + subject, handler, status, error, duration,
  started_at. Retention: age window, same shape as bus retention.

Apply pipeline: parse manifest → codegen → tsc against SDK types →
`Bun.build` → hash → write artifact under a content-addressed store dir →
insert `app_versions` row → flip `apps.current_version` in one transaction.
Failure anywhere before the flip changes nothing (the plugin installer's
stage-then-rename discipline, upgraded with real versions). The flip publishes
a fact; the runtime drains in-flight handlers for that app and loads the new
version. `attn app rollback` is the same flip to a prior version row.

### Bus consumption — real per-app consumers (Fork A)

Each app registers as its own durable bus consumer, `app:<name>`, with its own
cursor, its own DB-backed enabled bit, its own stall isolation — the exact
semantics `internal/bus` already gives registered consumers. Multiplexing all
apps behind one runtime-level consumer was rejected because it removes the new
machinery from apps precisely where they need it: per-app cursors, kill
switches, and stall isolation would all be rebuilt one layer above where they
already exist.

The bus grows the missing lifecycle, as its own PR:

- **Runtime register/unregister.** Registration is startup-only today
  (`ErrAlreadyStarted`); apps install and uninstall while the daemon runs.
  `Register` becomes callable after `Start` (spinning up a delivery loop),
  and `Unregister(name)` stops the loop and deletes the consumer row.
- **Row lifecycle.** Consumer rows outlive registrations today, and an
  orphaned enabled row pins the log against trim and compaction forever.
  Uninstall deletes the row; `attn bus status` keeps showing
  registered-but-dead consumers so the operator can see any stragglers.
- Cursor default for a newly installed app: **head** (`cursor-from-now`),
  which is `initConsumer`'s existing behavior. Apps react to what happens
  after they exist; backfill is a read of current state, not a replay.

The at-least-once, stall-don't-skip, cursor-after-handler semantics are
untouched — apps are the first production durable consumers, and the point is
to consume that machinery, not fork it.

A third granularity — one cursor per handler within an app — was considered
and deferred. What per-app gives up: independent progress across an app's
subscriptions (one slow handler gates the cursor for all), partial survival
of a poisoned event (one bad event × one bad handler stalls the whole app),
and per-concern pause. What per-handler would cost: cross-handler ordering —
one cursor means an app observes the log as one coherent sequence, so
handlers sharing the app's collections ("on created insert, on closed
update") are ordered for free, while per-handler lanes run at different log
positions and make every app internally a distributed system, taxing every
creator with consistency reasoning; plus handler identity becoming
load-bearing across versions (a renamed handler orphans its cursor), and a
policy surface (per-handler streaks, park-handler-vs-app) the rest of the
design has no vocabulary for. The rule: **the unit of isolation is the app;
if a workload needs two lanes, it is two apps** — apps are cheap by design,
and splitting moves the isolation boundary and the data-ownership boundary
together, which is exactly where per-handler cursors would let them
diverge. Consumer names are strings, so `app:<name>/<handler>` remains a
mechanical extension if a real receipt ever demands it; the reverse would be
a migration.

### Shared runtime — one Bun sidecar, supervised by an extracted supervisor (Fork B)

All app handler code executes in **one daemon-supervised Bun sidecar** — never
in the daemon (the PTY-worker reasoning applied to apps), and not
one-process-per-app (apps are numerous and mostly idle; a process each is
memory the platform pays all day; the sidecar is one ~tens-of-MB Bun process
total). In-process isolation between apps is by handler-level failure
attribution, not OS boundaries — an app that throws feeds its own
auto-disable counter; it cannot corrupt the daemon, and a sidecar-wide crash
is a *runtime* failure class handled by supervision, not attributed to an app
(a whole-process death cannot name a culprit; blaming whichever app was
running would auto-disable innocents).

The supervisor is extracted from `internal/daemon/plugin_supervisor.go` into a
shared package (`internal/supervise`), scoped to what its two consumers need
today — nothing speculative:

- Restart with capped exponential backoff and generation fencing against
  stale connections/timers (exists, proven: 250ms→30s over 8 steps).
- A stability window that resets backoff (exists: 60s connected).
- A disconnect grace killing a child that started but never called back
  (exists: 5s).
- **A give-up state** (new — the supervisor today retries forever): a child
  that keeps dying without ever reaching stability gets parked, loudly —
  fact + durable notification + `attn app status` showing it. Initial
  tripwire: parked after 10 consecutive restarts with no stability window
  reached (~3.5 minutes of crash-looping at the existing backoff numbers).
  Tripwire, not a receipt — recalibrate against real behavior during this
  stage's verification, and record the measurement here.
- **Log capture** (new — plugin stdout/stderr goes to /dev/null today, a hole
  `attn app logs` cannot live with): per-child append-only log file, the
  ptyworker per-session pattern (`<data-dir>/apps/log/runtime.log`), surfaced
  by `attn app logs`.

`pluginSupervisor` moves onto the extracted package in the same PR, so the
extraction is verified by a consumer that already works. pi-host adoption is
explicitly out of scope (its no-supervision gap is the pi plan's Slice 4).

Sidecar protocol: the runtime dials the daemon socket like a plugin does
(supervisor's existing wiring), authenticates as the app runtime, receives
handler dispatches (event + app + version), and calls back over the same
socket for SDK operations. Byte streams stay off the bus per the standing
rule; dispatch and results are socket RPC, and only domain facts ride the
log.

### Handler contract and SDK (runtime half only)

A handler receives the event and a context scoped to its app:

- `ctx.collections.<name>` — one-shot document reads and writes against the
  app's own namespace (A3 surface, shipped through Stage 1; a handler that
  needs "what changed" reads current state — the fact is an invalidation, not
  a payload, same rule as every bus consumer).
- Event payload: typed progressively. The platform's fact payloads are small
  and owned by the daemon; the SDK ships types for the facts it documents and
  `unknown` for the rest — honest, and tightens per-fact over time.

**A4's SDK deliberately excludes live queries.** Windowed subscriptions
(A3.4 Stage 2, in flight as PR #776) are the UI-host story; the A5 SDK builds
on their delivery shape. Handlers are wake-and-read consumers and need
neither.

Failure semantics, pinned: a handler that throws fails that delivery; the
bus's stall-don't-skip retry applies (1s→2m capped backoff, per-event streak);
every attempt lands in `app_invocations`. **Auto-disable** (new policy,
nothing like it exists in the codebase): the app's consumer failure streak is
persisted, and when one event has stalled an app's consumer past the retry
cap for a sustained stretch, the platform flips the app's enabled bit off,
publishes the fact, and writes a durable notification naming the app, the
event, and the last error. Initial tripwire: 25 consecutive failures on the
same event or 15 minutes stalled, whichever first (~the point where retries
have been at the 2m cap for several rounds). Tripwires pending real receipts
— measured during verification, recorded here. Auto-disable is
load-bearing, not hygiene: an enabled-but-stalled consumer pins the entire
event log against trim and compaction; a platform where one broken app
freezes log retention for everyone is structurally broken.

Re-enable is explicit: `attn app enable <name>` (the way out beside the way
in), which clears the streak and — if the log moved past the parked cursor —
resumes at head with the existing logged-gap behavior.

### CLI — `attn app`

`new <path>`, `apply <path>`, `rollback <name> [version]`, `enable <name>`,
`disable <name>`, `list`, `status <name>` (current version, enabled, cursor
lag, parked/stalled state, last failures), `logs <name>`, `dev <path>` (watch
→ codegen → apply → streamed invocations and logs). Thin WS RPCs to the
daemon, like `attn plugin` and `attn bus`. Every limit and failure surfaced
here names the limit, the value, and the ask — agents read errors, not code.

## Delivery — epic branch

All A4 PRs target **`epic/ext-a4`** (created 2026-08-06 from main, since
fast-forwarded to `09ca4e28` — the A3.4 Stage 2 + timestamp-fix integration
merge), so slices merge promptly without betting main; main takes one
fully-CI'd, fully-reviewed merge at the end. Discipline that keeps the epic
honest:

- Rebase the epic onto main after each landed slice — protocol bumps flow
  into main from other lanes, and the final merge must not be a conflict
  monster.
- Per-slice review on PRs into the epic still happens when reviewer/CI are
  available; the epic decouples *merging* from those gates, not the review
  itself.
- The epic→main PR gets the full treatment: CI green, figgyster on the whole
  diff, live verification receipts.

### Slices (PRs into the epic)

1. **Bus consumer lifecycle** — post-`Start` register, `Unregister`, row
   deletion, tests for the pin-the-log orphan case. Small, independently
   valuable, unblocks everything.
2. **Supervisor extraction** — `internal/supervise`, pluginSupervisor moves
   onto it, give-up state + log capture added. Verified by the existing
   plugin runtime.
3. **Registry + store + CLI skeleton** — `app_*` tables, `attn app`
   list/status/enable/disable against them, glossary entries (app, plugin),
   `ext/` → `app/` namespace rename.
4. **Apply pipeline + scaffold + codegen** — manifest parse, codegen, tsc,
   `Bun.build`, content-addressed versions, pointer flip, `new`/`apply`/
   `rollback`/`dev`.
5. **Runtime sidecar** — dispatch, handler context, invocation log,
   per-app consumers wired to slice 1, auto-disable, `logs`.
6. **Exit proof** — the roadmap's exit, run live: an agent writes a real
   app in a scaffolded directory, applies it, sees invocations in the log,
   breaks it and watches auto-disable park it, fixes and re-enables it,
   rolls it back. Recorded as receipts in the epic→main PR.

Verification tier: A4 touches daemon lifecycle, protocol, and background
runners — live verification in a running non-production app is mandatory for
slices 3–6; slices 1–2 are daemon-internal and carry their own harness tests
plus the live proof at slice 6.

## Out of scope

- UI tiles, panels, the `@attn/app` UI SDK, import maps — A5, which builds on
  A3.4 Stage 2's delivery shape.
- Hook claims (C1), workflows (B2), the C2 gate.
- pi-host adoption of the shared supervisor (pi plan Slice 4).
- App-published bus facts (apps as producers). Consumption first; producing
  needs its own naming/authority conversation.
- Any marketplace/distribution story. Apps are directories; distribution is
  git.
