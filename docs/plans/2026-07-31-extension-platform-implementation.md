# Plan: the attn extension platform

## Goal

Make attn self-extensible: an agent inside attn creates a new event-driven
interaction — subscribe to something the daemon does, put up a purpose-built UI,
take the human's decision, act on it — **without a core PR**.

Grounded in [the brainstorm and spike](2026-07-31-extension-platform-brainstorm.md),
whose `spike-ext/` host proved the load-bearing mechanics: a goja handler can
block a core operation on a human verdict, a long block does not trip the
watchdog, runaway handlers are still interrupted, unanswered gates time out to
policy, and a cheap policy path costs the human nothing.

Two proving tenants, stressing different axes:

- **`delegation.before_start`** — the event and interception half. Review a
  delegation's prompt before the delegate starts; approve, or send it back with
  feedback.
- **Present's use cases** — the expressiveness half. If the catalog can host
  change review and document reading, it is a platform and not a form renderer.

Settled 2026-07-31: goja-first with plugins kept; component catalog with an
`html` escape hatch; extension-declared placement; all three interaction kinds;
Present's machinery reused but its manifest schema **not** inherited;
automations committed as a future tenant without touching the working v2 code.

## Architecture Map

```text
a daemon operation (delegate, worktree create, session state change, ...)
  -> emit(event, payload)                         [internal/extension: catalog]
    -> dispatch: subscribers, priority, kind      [internal/extension: dispatch]
      |
      +-- goja runtime — ONE VM PER INVOCATION    [internal/extension/runtime]
      |     host fns: ask() present() state() emit() agent()
      |     per-invocation budget; capability-gated
      |
      +-- out-of-process plugin (existing JSON-RPC path, same catalog)
      |
      v
    verdict: observe -> ignored
             decide  -> handled | decline   (chain of responsibility)
             gate    -> allow | deny(data)  (deadline + declared default)
  -> the daemon acts: proceed, or abort carrying the extension's feedback

ask(viewTree)
  -> validate against the component catalog      [internal/extension/view]
  -> persist a pending interaction               [internal/store]
  -> broadcast; placement declared BY THE TREE (drawer | tile | window)
  -> app renders natively; `html` renders sandboxed
  -> human answers -> response -> handler resumes -> verdict

Tests:
  internal/extension  unit tests, in-memory store fake + injected clock
  daemon integration  real SQLite via ScopeTestEnvironment
  frontend            view-tree renderer against the createMockDaemon pattern
  live                packaged scenarios on a throwaway profile
```

## Data Model

```text
extensions            id, name, source_path, script_hash, enabled,
                      capabilities_json, created_at, updated_at

extension_subscriptions  extension_id, event_name, interaction_kind, priority
                      -- cached from the script's registration pass; rebuilt on
                         load, never hand-maintained

extension_interactions   id, extension_id, event_name, correlation_id,
                      view_json, placement,
                      state: pending | answered | expired | cancelled,
                      response_json, created_at, deadline_at, answered_at
                      -- durable so a daemon restart does not strand a gate

extension_invocations    id, extension_id, event_name, state, error,
                      started_at, ended_at
                      -- observability: a wedged gate is diagnosable in one look

extension_state          extension_id, key, value_json
                      -- the small per-extension KV; long-lived state lives HERE,
                         never in a resident VM
```

The event catalog is code, not rows: `(event name, payload schema, interaction
kind)` versioned alongside `ProtocolVersion`, so a daemon upgrade cannot
silently change what a gate sees.

## Boundaries

- `internal/extension` owns the catalog, dispatch decisions, the runtime, and
  view validation. It never imports `internal/store` or `internal/daemon` — the
  daemon adapts concrete types to its interfaces, the same inversion
  `internal/automation`'s `BindingStore` already uses.
- The daemon owns emission and materialization, and is the only thing that acts
  on a verdict.
- The app never interprets extension semantics. It renders a validated view tree
  and posts a response; it does not know what a gate means.
- **One catalog for both runtimes.** A goja extension and an out-of-process
  plugin register against the same surface registry, so an extension can
  graduate from script to plugin without the platform changing shape.
- The component catalog stays **small and forgiving** — few required fields,
  strong defaults, derive what can be derived. Present's manifest rigidity is
  the thing being fixed; a big rigid schema with more kinds is the same failure
  with a wider surface.
- The view tree is the contract; **authorship is free**. Whether the extension
  emits the tree or delegates composition to a presenter agent, the platform
  sees the same validated tree.
- Gates are **policy-first**: the documented default shape decides cheaply in
  code and reaches for the human only on the expensive path. This is how the
  platform keeps faith with *"autonomy over approval"* while still being a
  gate mechanism.

## Implementation Steps

- [ ] **PR1 — catalog + dispatch + runtime, `observe` only.** `internal/extension`
      with the event catalog, the surface registry (replacing
      `validatePluginSurfaces`'s closed switch), and the goja runtime: one VM per
      invocation, per-invocation budget, capability-gated `log`/`state` host fns.
      Emit two or three real events. `attn ext list|enable|disable|logs`. No UI,
      no blocking, nothing can wedge. *Live-verify: a hand-written extension logs
      on a real daemon event.*
- [ ] **PR2 — the `gate` kind, headless.** Emit `delegation.before_start`; a
      handler returns allow/deny from policy alone, no UI. Deadline, declared
      default, per-extension kill switch, `extension_invocations` observability.
      *Live-verify: a real delegation is blocked and then released by a script,
      and an unanswered gate falls through to its default.*
- [ ] **PR3 — component catalog + `ask()` + first placement.** View-tree schema
      and validation, the zod mirror in the frontend, durable
      `extension_interactions`, protocol bump, drawer placement. *Live-verify:
      the delegation prompt gate end to end — approve, and send back with
      feedback that reaches the delegating agent.*

  **← first release boundary.** PR1–PR3 is the honest MVP: the motivating
  feature working, built entirely on platform seams.

- [ ] **PR4 — remaining placements + the escape hatch.** Tile and window
      placements (window reuses the `PresentRoot` shell — a solved Tauri
      multi-window blindspot). `html` component: strict CSP, capability-scoped
      bridge, no ambient daemon access.
- [ ] **PR5 — the `decide` kind.** Migrate the four worktree surfaces onto the
      catalog and delete the private path, so one mechanism serves everything.
- [ ] **PR6 — presenter agent.** A host fn that hands raw material and intent to
      an agent whose only job is composing the view tree. Additive by
      construction: the platform still just sees a validated tree.
- [ ] **PR7 — Present's use cases as tenants.** `diff` and `markdown`
      components; change review and document reading on the platform. Decide
      `internal/present`'s fate once the replacement is genuinely better —
      not before.

## Risks

- **Blast radius.** Agent-authored code that can block core operations needs
  capability grants, a kill switch, and observability from PR1 — not retrofitted
  in PR3. A wedged gate must be diagnosable in one look, never inferred from a
  stalled delegation.
- **A third mechanism.** Plugins, automations, extensions. PR5 folds plugins'
  surfaces in; automations are committed as a future tenant. If neither lands,
  attn has grown a third half-overlapping way to do the same thing.
- **Catalog sprawl.** The escape hatch exists so the catalog can stay small.
  Promote a component to native only when the hatch keeps being used for the
  same thing.
- **Porting Present's failure.** PR7 must not reproduce the authoring cost that
  stalled Present. The presenter agent (PR6) lands first for exactly that reason.

## Verification

This touches daemon lifecycle, protocol, persisted state, and UI surfaces, so
every PR from PR2 on needs live verification in a running non-production app per
AGENTS.md — daemon-tier installs are not sufficient once events reach the app.
PR1 is daemon-internal and may verify at the daemon tier with its unit tests.
Run the bundled preflight before each evidence run.
