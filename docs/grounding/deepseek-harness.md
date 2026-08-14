# Grounding: DeepSeek Harness vs attn's extension platform

2026-08-14, `deepseek-ai/deepseek-harness` at `47f9438` (shallow clone of
the public repo). Read against
[docs/vision/extension-platform.md](../vision/extension-platform.md) and the
[roadmap](../plans/2026-08-01-extension-platform-roadmap.md), with A4 (apps:
registry + shared runtime) landed and A5/B1/B2/C1–C3 ahead.

## The question

Another team building an agent harness shipped a full extensibility model in
the open. What did they answer that attn has left open — and what did they
build that attn deliberately does not want?

## What it is

A TypeScript monorepo (54 package groups under `packages/`) on **Cordis**, a
vendored DI/plugin framework. Every part of the product is a plugin: the
model adapter, the tool registry, the session log, the agent loop itself. A
running `dsh` is a plugin tree composed at boot from ordered config layers
(bundles → profile patch → home patch → `--patch` overlay); any row is
replaceable, and `dsh --profile web --dump-config` prints the effective tree.

Cordis in five ideas, from their own primer: a plugin is an object
implementing `Service`; a context is a repository of services claiming stable
`ctx.<key>`; dependencies are declared with `inject` (load order falls out of
service requirements); communication is typed events via declaration merging;
**registrations are reversible effects** installed through `ctx.effect()` /
`ctx.on()`.

## The structural difference

**dsh has no privileged core. attn deliberately has one.**

Extension is their construction method, not a product feature — their
architecture doc states it plainly: "There is no privileged core to patch."
attn's daemon is authoritative by design (`applyState`, `wireProjections`,
"complexity belongs at the boundary"), and apps are user-land on top.

That is the right call for a Go daemon owning PTYs, SQLite and a Tauri app,
and their model has a visible cost: nobody can see what is actually running
without dumping the config tree.

**On the primitives themselves attn is ahead, not behind.** Their event
system is in-process `emit`; `storage-domain` lists "single-process change
visibility" as a known limitation, and the jobs contract says outright: *"The
contract is in-process — a durable or cross-process backend must reshape
identity, restart, ownership, and observation semantics before it can
implement this seam."* attn's bus and queue are durable with per-consumer
cursors from day one. They have no versioning at all: no content addressing,
no atomic flip, no rollback, no version-stamped invocation log, no
auto-disable, no retention-floor accounting.

So this is harvest, not catch-up.

## Findings

### 1. Feed the generated catalog to the model, not just to humans

The sharpest idea in the repo. `cordis_inspect` — a model-facing tool —
renders the **same generated catalog the docs render**, from the same AST
walk, "so the data a model reads and the rendered docs cannot diverge."

Then `packages/extensions/tool-cordis/src/curation.ts` classifies every
service key as `injectable | not-a-service | other-face`, and only
`injectable` keys reach a model report. Their stated reason: *"naming a key a
package cannot reach advertises a call that cannot be made."* A gate
(`verify-cordis-catalog`) pins the classified set, so a newly declared key
stops the build rather than quietly inviting a model to `inject` something
that will never arrive.

Where it lands in attn: the **C2 gate left open** — "app capability
discoverability: how an agent in an unrelated session learns an installed
app's surface exists" — and A4's "the SDK ships types for the facts it
documents and `unknown` for the rest." One generated fact catalog (subjects +
payload shapes) with three renderings: the SDK `.d.ts`, the docs, and an
inspect report an authoring agent reads. Freshness-gated, one source.

### 2. Interception is a declared dispatch mode, with `next()`

Cordis has four dispatch modes — `emit`, `waterfall`, `parallel`, `serial` —
and **the mode is part of the event's public contract**, tagged `@mode` in
JSDoc and machine-checked against dispatch sites by the catalog generator.
Interception is just `waterfall`: around-middleware where a listener receives
`(...args, next)`, calls `next()` to delegate, or returns without it to own
the decision outright.

Not an argument for merging hooks into attn's bus — their unified model works
*because* it is ephemeral and in-process, and an interception cannot be
replayed off a durable cursor. But C1's gate asks "single vs multiple
claimants per hook," and this answers it with reasoning: **multiple, ordered,
each may delegate or short-circuit** — a richer contract than fail-open /
fail-closed + timeout alone. Their rule: a policy listener may short-circuit
when it owns the decision; a listener that only annotates must delegate.

Worth taking: the vocabulary, and declaring each hook point's mode as a
machine-checked part of its contract.

### 3. Every registration returns its disposer — and a test proves it

Root `AGENTS.md`: *"Registrations are effects: every contribution goes through
`ctx.effect()` / `ctx.on()`; a registry's `register()` returns the disposer."*
Backed by a test class, from `docs/testing.md`: *"Every registry gets an
HMR-safety test (dispose the contributing fiber, assert cleanup)."*

Where it lands: A5 adds tiles, C1 hook claims, B2 workflows — each a new
registration class that must unwind on disable, rollback, remove, and version
flip. attn already has drain-on-flip and consumer `Unregister` per surface;
what is missing is the uniform rule plus the test class that proves it. This
is attn's own "if you added a way in, add the way out" mechanized, and it is
cheapest to adopt *before* the surfaces multiply.

### 4. The approval panel is frame-wide, not inside the session

Their `cordis_run` suspends on a human verdict with **no timeout** — the only
other exit is the asking turn's `AbortSignal`. The UI decision, from
`packages/extensions/ui-cordis/README.md`: the panel is a frame-wide overlay,
never filtered by session, because a blocked request can name a session
nobody is looking at — *"an approval reachable only inside that session's
transcript would be unreachable exactly when it blocks the model."*

Rows blocking a model sort first; the selected session's rows group first and
everyone else's stay listed below.

Where it lands: **C2's gate asks where the approval panel lives.** That is
the answer, with the reason, from someone who shipped it.

### 5. "Model Experience" as a required, gated README section

Every package README carries a `Model Experience` section with three fixed
subheads: **What the model sees / Token effect / KV Cache effect** — enforced
by `scripts/verify-package-readme-model-experience.ts`. A sibling gate
(`verify-package-readme-limitations.ts`) requires a "Known Limitations and
Deferred Work" section.

The KV-cache answers are specific, not boilerplate: one package records that
"a host half that registers tools changes the next request's tool view, which
invalidates prefix reuse from the first changed schema token; running or
stopping a package with no tool registrations is prefix-neutral."

Where it lands: attn's docs describe what the **user** sees. For a platform
whose apps will inject context, claim hooks and write records agents read,
"what does this cost the model's context, and does it invalidate the prefix"
is a real question nothing in attn currently asks. A required section with a
CI gate is a rule living in the layer that can enforce it — the vision's own
teachability ladder, applied to docs.

### 6. Generated architecture maps, freshness-gated

Generated from the TypeScript program and gated on drift: the event
producer/consumer matrix (`docs/event-producer-consumer.md` — every event's
mode, declaration site, dispatchers and listeners), the capability-seam graph,
`module-graph.md` (1,638 lines), `tool-catalog.md` (1,873),
`config-catalog.md` (3,151), `persistence-catalog.md` (944). Roughly 15
`gen-*` and 35 `verify-*` scripts in `scripts/`.

**Receipt that this matters:** their root `AGENTS.md` still lists
`packages/self-modification/`; the directory is `packages/extensions/`.
`packages/README.md` — closer to generated territory — has it right. The
hand-written tier drifted anyway, in a repo with ~50 verify gates.

Where it lands: attn already enforces *behavior* this way
(`TestWireTrafficComesFromProjections`, `TestEveryProjectedFactReachesTheWire`
— both driven by the live table so a new projection cannot go unnoticed).
What is missing is the generated **map**: which fact, which projection, which
app consumer, which hook claim.

## Two smaller notes

**Independent convergence on post-settle failure reporting.** A dsh browser
half can load cleanly, answer `cordis_run` with `ok`, and only then throw when
React renders it — so the model is told "fine" and never learns. Their fix is
a separate report path keyed on component identity, carrying no settle
authority, surfaced through `cordis_inspect`. attn hit the same class in A4
and solved it the same way (`installCrashReporter` matching content-addressed
bundle paths against the crash stack). Validation — and a heads-up that **A5
needs the render-crash half**: an error boundary that dies at the tile is not
enough, the authoring agent has to be told.

**Runtime invariants, not just tests.** `ctx.invariants` is a package-owned
registry asserting relationships in production over authoritative event
streams — their "model-visible ⟺ logged" rule is a *runtime* assertion, not
only a CI one. Probably not worth adopting broadly, but it is the stronger
form for a rule like "every persisted state change goes through `applyState`."

## What not to copy

- **Cordis-style DI for attn.** Wrong language, wrong process model, and attn
  wants the privileged daemon.
- **Per-file 100% coverage as the gating run.** Affordable only pre-release
  with no external consumers; their root `AGENTS.md` carries an explicit
  "remove this section at the first tagged release" pre-release stance.
- **Chat-time in-memory self-extension.** `cordis_define` / `cordis_run` keeps
  its registry in process memory only, survives no restart, and cannot be
  promoted; their own README ends at *"To keep an experiment, ask the Agent to
  implement a normal local, project, or repository Plugin through the regular
  development workflow."* A serious team built the pattern attn's vision
  rejects and still lands on "throw it away or rewrite it properly." Good
  evidence the authoring-time bet is right. (Their trust stance also matches
  attn's word for word: the `node:vm` sandbox "is not a security boundary…
  treat a dynamic package like bash access.")
- **Bilingual doc pairing.** Not attn's problem.

## The one flagged as possible ceremony

Doc word budgets: `scripts/doc-budgets.manifest.json` sets per-file ceilings
(root `AGENTS.md` 1,900 words; `docs/architecture.md` 2,400) and
`verify-doc-budgets` rejects excess, with a documented escalation — relocate,
condense, or raise the ceiling and justify the manifest diff.

Behind it is a tier taxonomy with one home per fact: standing orders (root
`AGENTS.md`) → ordered architecture map → per-subsystem references →
decision records → cookbooks → package READMEs, plus a "slop checklist"
naming the failure modes (the same rule in two homes, narrated history,
status annotations that rot, hand-restated catalogs).

Measured: attn's `AGENTS.md` is **5,364 words**, and it is the file that must
be in context every session. Their tiering is a real answer to a problem attn
has. It is also the finding most likely to be ceremony — noted, not
recommended.
