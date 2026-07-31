# Plan: the attn extension platform

## Goal

Make attn self-extensible: an agent inside attn creates a new event-driven
interaction — subscribe to something the daemon does, put up a purpose-built UI,
take the human's decision, act on it — **without a core PR and without a human
clicking anything**.

Grounded in [the brainstorm and spike](2026-07-31-extension-platform-brainstorm.md),
whose `spike-ext/` host proved the load-bearing mechanics: a goja handler can
block a core operation on a human verdict, a long block does not trip the
watchdog, runaway handlers are still interrupted, unanswered gates time out to
policy, and a cheap policy path costs the human nothing.

Two proving tenants, stressing different axes:

- **`delegation.before_start`** — the event and interception half. Review a
  delegation's prompt before the delegate starts; approve, or send it back with
  feedback.
- **Present's use cases** — the expressiveness half. If extensions can host
  change review and document reading, this is a platform and not a form
  renderer.

This is a foundation, not a minimal feature. Where a choice is between "smaller
now" and "right to build on," it goes to the latter.

### Settled

1. **Runtime — goja-first, plugins kept.** Handlers run in-daemon; the existing
   out-of-process plugin path stays for anything needing filesystem, network or
   real dependencies, sharing one catalog.
2. **UI — real React, not a data model.** Extensions ship `.tsx` importing a
   provided `@attn/ui` component library. Supersedes the earlier view-tree
   catalog: a bespoke schema has no training data and would reproduce the
   authoring cost that stalled Present.
3. **Placement — the extension declares it** (drawer / tile / window).
4. **All three interaction kinds** — `observe`, `decide`, `gate`.
5. **Present** — machinery reused, manifest schema not inherited; not adopted,
   so not a constraint.
6. **Automations** — committed as a future tenant; v2 code untouched here.
7. **Registration is agent-driven** — CLI over the socket, hot reload, no
   clicking, no daemon restart.
8. **State preservation is a platform primitive** — durable, transactional,
   per-extension, reachable from handler and UI alike.

## Architecture Map

```text
AUTHORING (an agent, no human in the loop)
  writes  my-extension/{extension.toml, handler.ts, ui.tsx}
  runs    attn ext register ./my-extension
    -> bun bundles: handler.ts -> plain JS (goja)
                    ui.tsx     -> ESM, react/react-dom externalized
    -> registered, hot-reloaded, live. No restart, no UI ceremony.

BACKEND
  a daemon operation (delegate, worktree create, session state change, ...)
  -> emit(event, payload)                          [internal/extension: catalog]
    -> dispatch: subscribers, priority, kind       [internal/extension: dispatch]
      |
      +-- goja runtime — ONE VM PER INVOCATION     [internal/extension/runtime]
      |     host fns: ui() state() emit() agent() log()
      |     per-invocation budget; capability-gated
      |
      +-- out-of-process plugin (existing JSON-RPC path, same catalog)
      |
      v
    verdict: observe -> ignored
             decide  -> handled | decline    (chain of responsibility)
             gate    -> allow | deny(data)   (deadline + declared default)
  -> the daemon acts: proceed, or abort carrying the extension's feedback

FRONTEND
  ui(props) from a handler
  -> persist a pending interaction               [internal/store]
  -> broadcast; placement declared by the extension
  -> app dynamically imports the extension's ESM module
  -> renders <ExtensionUI/> inside a per-extension error boundary
     importing @attn/ui (themed components) and @attn/host (typed daemon SDK)
  -> human acts -> response -> handler resumes -> verdict

STATE
  extension_state: durable, transactional, namespaced per extension
  reachable from the handler (host fn) and the UI (host SDK) alike.
  Long-lived state lives HERE — the runtime keeps no resident VM.

Tests:
  internal/extension  unit tests, in-memory store fake + injected clock
  daemon integration  real SQLite via ScopeTestEnvironment
  frontend            extension host + error boundary via createMockDaemon
  live                packaged scenarios on a throwaway profile
```

## Data Model

```text
extensions            id, name, source_path, bundle_hash, schema_version,
                      enabled, capabilities_json, created_at, updated_at

extension_subscriptions  extension_id, event_name, interaction_kind, priority
                      -- rebuilt from the handler's registration pass on every
                         load; never hand-maintained

extension_interactions   id, extension_id, event_name, correlation_id,
                      props_json, placement,
                      state: pending | answered | expired | cancelled,
                      response_json, created_at, deadline_at, answered_at
                      -- durable so a daemon restart does not strand a gate

extension_invocations    id, extension_id, event_name, state, error,
                      started_at, ended_at
                      -- observability: a wedged gate is diagnosable in one look

extension_state          extension_id, key, value_json, updated_at
                      -- the durable primitive; transactional get/set/delete/list
```

The event catalog is code, not rows: `(event name, payload schema, interaction
kind)`, versioned alongside `ProtocolVersion`, so a daemon upgrade cannot
silently change what a gate sees.

## Boundaries

- `internal/extension` owns the catalog, dispatch, the runtime and bundling. It
  never imports `internal/store` or `internal/daemon` — the daemon adapts
  concrete types to its interfaces, the inversion `internal/automation`'s
  `BindingStore` already uses.
- The daemon owns emission and materialization, and is the only thing that acts
  on a verdict.
- The app hosts extension modules; it never interprets extension semantics.
- **One catalog for both runtimes**, so an extension can graduate from script to
  plugin without the platform changing shape.
- **No bespoke UI data model, ever.** If a need arises that React plus
  `@attn/ui` cannot express, the answer is a new component in the library — not
  a schema. This is the constraint that keeps the platform out of Present's
  failure mode.
- `@attn/ui` is the easy path, not a cage. Consistency comes from the components
  being convenient, not from rejecting anything else.
- Gates are **policy-first**: decide cheaply in code, reach for the human only
  on the expensive path. This is how a gate mechanism keeps faith with
  *"autonomy over approval."*

## Implementation Steps

- [ ] **PR1 — the authoring spine.** `internal/extension`: catalog, surface
      registry (replacing `validatePluginSurfaces`'s closed switch), goja runtime
      (one VM per invocation, per-invocation budget, capability gating),
      `extension_state` with its host fn, and bun-backed bundling. CLI:
      `attn ext register|reload|list|enable|disable|logs`. Hot reload, no
      restart. `observe` kind only — nothing can block, nothing can wedge.
      *Live-verify: an agent authors an extension end to end with no human
      interaction, it logs on a real daemon event, and its state survives a
      daemon restart.*
- [ ] **PR2 — the `gate` kind, headless.** Emit `delegation.before_start`; a
      handler returns allow/deny from policy alone, no UI. Deadline, declared
      default, per-extension kill switch, `extension_invocations` observability.
      *Live-verify: a real delegation is blocked then released by a script, and
      an unanswered gate falls through to its default.*
- [ ] **PR3 — the React host.** Extension module loading in the packaged app
      (asset protocol, cache-busted reload, React externalization), the
      per-extension error boundary, `@attn/ui` v0 and the `@attn/host` typed
      SDK, durable `extension_interactions`, protocol bump, drawer placement.
      *Live-verify: the delegation prompt gate end to end — approve, and send
      back with feedback that reaches the delegating agent.*

  **← foundation complete.** The motivating feature works, built entirely on
  platform seams, and an agent can author the next one unaided.

- [ ] **PR4 — remaining placements.** Tile and window (window reuses the
      `PresentRoot` shell — a solved Tauri multi-window blindspot). Sandboxed
      webview placement for untrusted or heavyweight content.
- [ ] **PR5 — the `decide` kind.** Migrate the four worktree surfaces onto the
      catalog and delete the private path, so one mechanism serves everything.
- [ ] **PR6 — presenter agent.** A host fn handing raw material and intent to an
      agent whose only job is composing the UI. Additive: the platform still
      just renders a React module.
- [ ] **PR7 — Present's use cases as tenants.** Change review and document
      reading as extensions, reusing Present's diff rendering. Decide
      `internal/present`'s fate once the replacement is genuinely better.

## Risks

- **Blast radius.** Code that can block core operations needs capability grants,
  a kill switch, and observability from PR1 — not retrofitted at PR3. A wedged
  gate must be diagnosable in one look, never inferred from a stalled
  delegation.
- **Same-context React has no sandbox.** An extension module imported into the
  app runs with app privileges. Mitigated by an error boundary and per-extension
  disable; the webview placement (PR4) is the containment answer when it is
  actually needed. **Open decision — see below.**
- **React version lock.** Extensions build against the app's React. Externalizing
  `react`/`react-dom` keeps one instance; a major bump is a compatibility event
  and needs the same fail-explicitly discipline as `ProtocolVersion`.
- **`bun` becomes load-bearing.** Already true for plugins, but registration
  failing because bun is missing must be a clear, actionable error.
- **A third mechanism.** PR5 folds plugins' surfaces in; automations are
  committed as a future tenant. If neither lands, attn has grown a third
  half-overlapping way to do the same thing.
- **Porting Present's failure.** PR7 must not reproduce the authoring cost that
  stalled Present. PR6 lands first for exactly that reason.

## Verification

This touches daemon lifecycle, protocol, persisted state, and UI surfaces, so
every PR from PR2 on needs live verification in a running non-production app per
AGENTS.md — daemon-tier installs are not sufficient once events reach the app.
PR1 is daemon-internal and may verify at the daemon tier plus its unit tests.
Run the bundled preflight before each evidence run.
