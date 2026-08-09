# A4 — App registry and shared runtime

Stage A4 of the extension-platform roadmap
([2026-08-01-extension-platform-roadmap.md](2026-08-01-extension-platform-roadmap.md)),
designed 2026-08-06 with Victor. The moment it delivers: an agent writes a
small automation, applies it live, watches it run, kills it, rolls it back —
without touching attn's source or restarting anything.

## The rename: apps, not extensions

"Plugin" and "extension" are synonyms in every ecosystem; neither word carries
attn's actual distinction. Decided: the user-land unit is an **app**.

- A **plugin** integrates the outside world into attn (agent drivers, worktree
  hooks): its own supervised process, dials the daemon, installed rarely,
  effectively part of the platform. A device driver.
- An **app** is automation built on attn's platform: consumes domain facts
  from the bus, keeps state in its own document-store namespace, later grows
  UI tiles (A5), hook claims (C1), workflows (B2). Runs inside the one shared
  supervised runtime with content-addressed versions, an invocation log, a
  kill switch, auto-disable. Written by agents, applied live, cheap and
  numerous.

A failing plugin takes an integration down; a failing app is auto-disabled
while everything else keeps running. Different trust, rate, and blast radius —
different mechanism, on purpose. This supersedes the 2026-04 plugin plan's
"one extension mechanism" promise. Both nouns get glossary entries in this
stage.

Ripple, applied consistently: CLI `attn app`, document-store owner segment
`app/<name>` (the reserved class shipped as `ext/` in A3 — renamed now, while
zero real namespaces exist), bus consumers `app:<name>`, tables `app_*`.
Plan-doc filenames keep the roadmap's `ext-` tag for lineage; new prose says
app.

## Gate answers

### Manifest — declares behavior; codegen types it; apply never executes

The manifest is TOML, `attn-app.toml`:

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
app code.** Declarations are static, so `attn app apply` fails before the
version flip — unknown event pattern, collection conflict, missing handler —
with nothing executed and nothing changed.

Codegen keeps creators in one typed place: `attn app dev`/`apply` regenerate
`src/generated.ts` from the manifest, and the entrypoint must satisfy it —

```ts
import type { Handlers } from "./generated"

export default {
  "delegation.*": onDelegationEvent,
  "session.state_changed": onStateChange,
} satisfies Handlers
```

— so a declared subscription without a handler, or a handler with the wrong
shape, is a tsc error with a filename and line, from the same tsc pass apply
already runs. Manifest↔code sync is enforced by the compiler, not convention.

Rejected: binding handlers in the manifest by string (`"src/handlers.ts#onX"`
— stringly-typed, invisible to refactors, two places per handler, cost lands
on every creator) and code-as-manifest via `defineExtension({...})` extraction
(best ergonomics, but apply must execute author code — sandboxing,
determinism rules, "my import hung the apply"). Codegen gives the same single
source of truth with the arrow reversed and zero evaluation.

Extensibility: A5 adds `[[tiles]]`, C1 `[[hooks]]`, B2 `[[workflows]]`, gated
by `attn_app_api`. An unknown table is a loud apply-time error, never ignored
— an app declaring capabilities the runtime cannot honor must not half-load.

### Repo and scaffold — a mechanism, not a location

`attn app new <path>` scaffolds a self-sufficient directory anywhere:
manifest, entrypoint stub, generated types, SDK dependency wired, a real
`AGENTS.md` (with `CLAUDE.md` symlink) that makes "write me an app" a
one-shot prompt. `attn app apply <path>` works from any directory. attn
stores no repo location; the scaffold docs suggest a conventional home purely
for muscle memory.

### Registry and versions — content-addressed, snapshot-frozen

Three tables (numbers taken at PR time; migrations currently at 95):

- `apps` — name (PK), current version id, timestamps. **No enabled column**:
  an app's enabled state *is* its bus consumer's enabled bit. Flipping
  `app:<name>`'s consumer bit is the single act that both stops delivery and
  releases the trim floor (`consumerFloor` counts enabled consumers). A
  mirrored registry column would be a drift class with no job, and the
  consumer bit already has the DB-only kill-switch property.
- `app_versions` — immutable rows: app name, content hash of the built
  artifact (the version's identity), the frozen declaration snapshot (what
  the manifest said at apply time — the `automation_runs` pattern), artifact
  path, created_at. Never updated, never deleted by apply; rollback is a
  pointer move.
- `app_invocations` — version-stamped like `workflow_agent_calls`: app,
  version id (what actually ran, not the pointer), event seq + subject,
  handler, status, error, duration, started_at. Retention: age window, the
  bus-retention shape.

Apply pipeline: parse manifest → codegen → tsc against SDK types →
`Bun.build` → hash → write artifact under a content-addressed store dir →
insert `app_versions` row → flip `apps.current_version` in one transaction.
Failure anywhere before the flip changes nothing (the plugin installer's
stage-then-rename discipline, with real versions). Re-applying byte-identical
content mints no new row: the hash is the version's identity, so apply flips
the pointer to the existing row — a long `attn app dev` session leaves one
row per distinct build, and the invocation log's "what actually ran" stays
honest. The flip publishes a fact; the runtime drains that app's in-flight
handlers and loads the new version. `attn app rollback` is the same flip to
a prior version row.

### Bus consumption — real per-app consumers (Fork A)

Each app is its own durable bus consumer, `app:<name>`: own cursor, own
DB-backed enabled bit, own stall isolation — the exact semantics
`internal/bus` already gives. Multiplexing all apps behind one runtime-level
consumer was rejected: per-app cursors, kill switches, and stall isolation
would be rebuilt one layer above where they already exist.

The bus grows the missing lifecycle, as its own PR:

- **Runtime register/unregister.** Registration is startup-only today
  (`ErrAlreadyStarted`); apps install and uninstall while the daemon runs.
  `Register` becomes callable after `Start` (spinning up a delivery loop);
  `Unregister(name)` stops the loop and deletes the consumer row.
- **Row lifecycle.** An orphaned enabled row pins the log against trim and
  compaction forever; uninstall deletes the row. `attn bus status` keeps
  showing registered-but-dead consumers so stragglers stay visible.
- New-app cursor default: **head** (`initConsumer`'s existing behavior). Apps
  react to what happens after they exist; backfill is a read of current
  state, not a replay.

At-least-once, stall-don't-skip, cursor-after-handler semantics are untouched
— apps are the first production durable consumers, and the point is to
consume that machinery, not fork it.

**Slice 1, as built.** `Register` is callable after `Start` and launches that
consumer's delivery loop at once; `Unregister(name)` cancels the consumer, waits
for its loop to exit, and only then deletes the row. Each consumer holds its own
context, a child of the bus context, and both of the loop's waits — the idle
select and the retry sleep — watch it, so unregistering a consumer stalled at the
retry cap does not wait that cap out. The delete-last order is what keeps a live
loop from reading a registration that disappeared, an error path that would retry
forever; a handler that completes after the unregister has its cursor advance and
its failure record dropped silently. `Unregister` is idempotent and deletes rows
this process never registered, so an orphan an earlier daemon left behind — the
thing that pins retention against a consumer nobody serves — is clearable.

Per-handler cursors: considered, deferred. Per-app gives up independent
progress across an app's subscriptions and partial survival of a poisoned
event. Per-handler costs cross-handler ordering — one cursor means handlers
sharing the app's collections are ordered for free, while per-handler lanes
run at different log positions and make every app internally a distributed
system — plus handler identity becoming load-bearing across versions (a
renamed handler orphans its cursor) and a policy surface the design has no
vocabulary for. The rule: **the unit of isolation is the app; a workload
that needs two lanes is two apps** — apps are cheap by design, and splitting
moves the isolation boundary and the data-ownership boundary together.
Consumer names are strings, so `app:<name>/<handler>` remains a mechanical
extension if a real receipt ever demands it; the reverse is a migration.

### Shared runtime — one Bun sidecar, extracted supervisor (Fork B)

All app handler code executes in **one daemon-supervised Bun sidecar** —
never in the daemon (the PTY-worker reasoning applied to apps), and not
process-per-app (apps are numerous and mostly idle; a process each is memory
the platform pays all day; the sidecar is one ~tens-of-MB Bun process).
Isolation between apps is handler-level failure attribution, not OS
boundaries: an app that throws feeds its own auto-disable counter and cannot
corrupt the daemon. A sidecar-wide crash is a *runtime* failure class handled
by supervision, never attributed to an app — a whole-process death cannot
name a culprit, and blaming whichever app was running would disable
innocents.

The supervisor extracts from `internal/daemon/plugin_supervisor.go` into
`internal/supervise`, scoped to what its two consumers need today:

- Existing, proven: restart with capped exponential backoff and generation
  fencing (250ms→30s over 8 steps); 60s stability window resetting backoff;
  5s disconnect grace for a child that starts but never calls back.
- **Give-up state** (new — today it retries forever): a child that keeps
  dying without reaching stability gets parked, loudly — fact + durable
  notification + `attn app status`. Initial tripwire: parked after 10
  consecutive restarts with no stability window. Tripwire, not a receipt —
  recalibrate if a legitimate child ever reaches it. **Slice 2 correction to
  the estimate:** ten restarts at the pinned backoff cost 121.75s of waiting
  (0.25+0.5+1+2+4+8+16+30+30+30), plus up to the 5s disconnect grace per
  attempt for a child that starts and never calls back — so a crash-looping
  child is parked after roughly two to three minutes, not ~3.5.
- **Log capture** (new — plugin stdout/stderr goes to /dev/null today, a
  hole `attn app logs` cannot live with): per-child append-only log file
  (`<data-dir>/apps/log/runtime.log`, the ptyworker per-session pattern),
  surfaced by `attn app logs`.

`pluginSupervisor` moves onto the extracted package in the same PR, so the
extraction is verified by a consumer that already works. pi-host adoption is
out of scope (the pi plan's Slice 4).

Sidecar protocol: the runtime dials the daemon socket like a plugin does,
authenticates as the app runtime, receives handler dispatches (event + app +
version), and calls back over the same socket for SDK operations. Byte
streams stay off the bus per the standing rule; dispatch and results are
socket RPC, and only domain facts ride the log.

**How the daemon finds the sidecar (decided 2026-08-09, spiked):** the host
ships as a `bun build --compile` standalone binary, built by attn's own
pipeline with the repo-pinned bun — the bundled-plugin mechanism
(`scripts/build-bundled-plugins.sh`, which carries the bun ≥1.3.14
signability guard). The Bun runtime is embedded in the executable, so a
GUI-spawned daemon needs no PATH resolution and a user's machine needs no
toolchain to *run* apps (bun is required only by `apply`, which builds
CLI-side — slice 4's receipt that `~/.asdf/shims/bun` is invisible to
`pathutil.EnsureGUIPath()` is what forced the choice). There is no
PATH-resolution fallback, deliberately: one mechanism, no second failure
class. Spike receipts: a compiled host dynamic-imports arbitrary absolute
bundle paths (two in sequence — the hot-reload shape; content-addressed
version paths make a stale module cache structurally impossible) and
bundles using node builtins, all under `env -i`.

**The host binary is per-platform, and Linux is in scope for A4**: the
daemon runs on Linux remotes, so the stage is not done until the host
cross-compiles (`bun build --compile --target=bun-linux-x64|arm64`) beside
the Go daemon's existing `build-linux-{amd64,arm64}` targets and the
runtime is witnessed on a Linux remote (the OrbStack VM) before the
epic→main merge. A silently darwin-only runtime is a defect, not a
deferral.

### Handler contract and SDK (runtime half only)

A handler receives the event and a context scoped to its app:

- `ctx.collections.<name>` — one-shot document reads and writes against the
  app's own namespace (A3 surface). A handler that needs "what changed"
  reads current state — the fact is an invalidation, not a payload, same
  rule as every bus consumer.
- Payload typing is progressive: the SDK ships types for the facts it
  documents and `unknown` for the rest — honest, and tightens over time.

**No live queries in the A4 SDK.** Windowed subscriptions (A3.4 Stage 2) are
the UI-host story; the A5 SDK builds on their delivery shape. Handlers are
wake-and-read consumers and need neither.

Failure semantics, pinned: a handler that throws fails that delivery; the
bus's stall-don't-skip retry applies (1s→2m capped backoff, per-event
streak); every attempt lands in `app_invocations`. **Auto-disable** (new
policy): when one event has stalled an app past the retry cap for a
sustained stretch, the platform flips the app's consumer bit off, publishes
the fact, and writes a durable notification naming the app, the event, and
the last error. The running delivery loop observes the flip on its existing
re-read bounds (per drain pass, 5s mid-burst) — no new wake machinery.
Initial tripwire: 15 minutes stalled on the same event (~5 rounds at the 2m
retry cap; a failure-count clause was considered and dropped — at the pinned
backoff, 25 failures is ~38 minutes of wall time, so a 15-minute clock always
fires first and the count is dead policy); pending real receipts, measured
during verification and recorded here. Auto-disable is
load-bearing, not hygiene: an enabled-but-stalled consumer pins the entire
event log against trim, and a platform where one broken app freezes
retention for everyone is structurally broken.

Re-enable is explicit: `attn app enable <name>` clears the streak and, if
the log moved past the parked cursor, resumes at head with the existing
logged-gap behavior.

### CLI — `attn app`

`new <path>`, `apply <path>`, `rollback <name> [version]`, `enable <name>`,
`disable <name>`, `remove <name>`, `list`, `status <name>` (current version,
enabled, cursor lag, parked/stalled state, last failures), `logs <name>`,
`dev <path>` (watch → codegen → apply → streamed invocations and logs). Thin
WS RPCs to the daemon, like `attn plugin` and `attn bus`. Every limit and
failure surfaced here names the limit, the value, and the ask — agents read
errors, not code.

`remove <name>` uninstalls: stop and delete the bus consumer, delete the
registry row. Version history and the invocation log survive removal — they
are history — and so does the `app/<name>` docstore namespace: deleting user
data is a separate, explicit act, never an uninstall side effect. That act
has no surface in A4, deliberately: a wipe verb (`attn app wipe <name>` or
`remove --purge-data`) is deferred until a later stage, and the deferral is
the decision — remove never grows a data-deleting default.

## Delivery — epic branch

All A4 PRs target **`epic/ext-a4`** (created 2026-08-06 from main, since
fast-forwarded to `09ca4e28` — the A3.4 Stage 2 + timestamp-fix integration
merge). Slices merge promptly without betting main; main takes one
fully-CI'd, fully-reviewed merge at the end.

- Rebase the epic onto main after each landed slice — protocol bumps flow in
  from other lanes, and the final merge must not be a conflict monster.
- Per-slice review on PRs into the epic still happens when reviewer/CI are
  available; the epic decouples merging from those gates, not the review.
- The epic→main PR gets the full treatment: CI green, figgyster on the whole
  diff, live-verification receipts.

### Slices (PRs into the epic)

1. **Bus consumer lifecycle** — post-`Start` register, `Unregister`, row
   deletion, tests for the pin-the-log orphan case.
2. **Supervisor extraction** — `internal/supervise`, pluginSupervisor moves
   onto it, give-up state + log capture. *Shipped:* a consumer names a child
   and hands over a `StartFunc`, so the package carries no manifest, no
   environment and no protocol; `Ensure` doubles as the un-park (it revives a
   parked child with a clean restart budget), which is why no separate restart
   entry point exists yet. `parked` is a new `runtime_phase` value — the field
   is a free-form string on the wire, so no protocol bump. Plugin logs land in
   `<data-dir>/plugin-log/<name>.log`, deliberately outside
   `<data-dir>/plugins` because everything under there is scanned for
   manifests.
3. **Registry + store + CLI skeleton** — `app_*` tables,
   list/status/enable/disable/remove against them, glossary entries (app,
   plugin), `ext/` → `app/` namespace rename.
4. **Apply pipeline + scaffold + codegen** — manifest parse, codegen, tsc,
   `Bun.build`, content-addressed versions, pointer flip,
   `new`/`apply`/`rollback`/`dev`. *Shipped:* the build runs **CLI-side**, in
   `internal/appbuild`, and the daemon only records. bun lives on a developer
   PATH (`~/.asdf/shims/bun` here) that `pathutil.EnsureGUIPath()` does not
   reconstruct, so a daemon spawned by the app cannot reliably find it — and a
   build is a foreground activity with output a person is waiting on, not
   daemon work. The daemon keeps the atomicity it must own: `app_apply` carries
   only `(name, content_hash, declaration)`, never a path. It re-derives the
   artifact path from the name and the hash, re-hashes the bytes on disk, and
   refuses the apply if they disagree — so a lying client cannot point the
   registry at a file the pipeline did not produce. The content hash covers the
   **declaration and the bundle**, not the bundle alone: a manifest-only edit
   changes what the version means, and hashing only code would reuse a row
   whose frozen declaration is stale. Typechecking uses a pinned TypeScript
   (5.8.3) installed lazily on first apply under `<data-dir>/apps/toolchain`
   and shared by every app, behind an `flock` so concurrent applies install it
   once; invoking `tsc` directly rather than through `bun x` was measured at
   0.77s against 2.1s. The SDK ships as an **ambient module declaration**
   written into the app (`src/attn-app.d.ts`, declaring
   `@victorarias/attn-app`), which gives handlers a typed surface with no
   package published and no npm dependency in a scaffolded app — the eventual
   published package can take over the same specifier without touching app
   code. Staging lives inside the artifact store (`apps/.staging`) because a
   rename out of `/tmp` crosses filesystems on Linux. The protocol gains
   `app_apply`/`app_rollback` (shipped as 219; the epic's protocol and
   migration numbers renumber at each main sync — the tree is authoritative); the flip publishes `app.version.changed`
   (payload carries the previous id so the slice-5 runtime need not race the
   pointer), and like the other app facts it has no projection. `attn app dev`
   streams apply results and build errors only — invocation streaming needs
   slice 5, and the command's banner says so.
5. **Runtime sidecar** — dispatch, handler context, invocation log, per-app
   consumers wired to slice 1, auto-disable, `logs`. The compiled host
   binary cross-compiles for linux amd64/arm64 beside the Go daemon's
   existing cross targets — tracked here so the stage cannot close
   darwin-only.
6. **Exit proof** — the roadmap's exit, run live: an agent writes a real app
   in a scaffolded directory, applies it, sees invocations in the log,
   breaks it and watches auto-disable park it, fixes and re-enables it,
   rolls it back. Recorded as receipts on the epic→main PR. Includes the
   Linux witness: the runtime dispatching on a Linux remote (the OrbStack
   VM), so the cross-compiled host is proven, not just built.

Verification: A4 touches daemon lifecycle, protocol, and background runners
— live verification in a running non-production app is mandatory for slices
3–6; slices 1–2 are daemon-internal and carry harness tests plus the live
proof at slice 6.

## Out of scope

- UI tiles, panels, the `@attn/app` UI SDK, import maps — A5, which builds
  on A3.4 Stage 2's delivery shape.
- Hook claims (C1), workflows (B2), the C2 gate.
- pi-host adoption of the shared supervisor (pi plan Slice 4).
- Apps as fact producers. Consumption first; producing needs its own
  naming/authority conversation.
- Marketplace/distribution. Apps are directories; distribution is git.
