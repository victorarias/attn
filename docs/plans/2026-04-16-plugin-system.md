# Attn Plugin System

**Status**: IN PROGRESS — provider/plugin runtime and the generic agent-driver host surface are implemented; Snipe Slices 1 through 6 are implemented externally with repository-based installation plus operational diagnostics and no package publication. Remaining proposals are tracked in §Open Questions and §Deferred.
**Date**: 2026-04-16
**Owner**: victor

## Summary

Replace the current pattern of integrating coding agents via in-tree Go drivers (`internal/agent/claude.go`, `codex.go`, `copilot.go`, and previously proposed agent additions) with an out-of-process plugin system that can also provide daemon extension points for repository and workflow-specific behavior.

A third party can write a plugin in TypeScript (Bun runtime), install its local checkout via `attn plugin install --path <dir>`, and extend attn without touching its Go code. Agent plugins can work end-to-end — state reporting, classification, resume, session metadata — while provider plugins can claim daemon operations such as custom worktree creation and deletion. Git URL installation remains deferred.

### Goals

1. **Third-party contribution without forking.** Real unfulfilled demand for gemini-cli, opencode, and others the solo maintainer has no intent to ship in-tree.
2. **One extension mechanism.** Same system eventually serves: AI assistants that help operate attn, attn-as-agent experiments, external dashboards, user automation scripts, and daemon-owned operation providers. Explicitly avoid building a second mechanism later.
3. **Spark experimentation.** Writing a plugin should feel like a Saturday afternoon hack, not infrastructure work. "From `git init` to my plugin reacting to attn events in five minutes, in whatever language I felt like using."

### Non-goals for V1

Each earns its own design if pursued later:

- Custom UI extensions
- In-process scripting runtime (Lua, JS, Yaegi, WASM — all rejected for this phase)
- Sandboxing / plugin signing / vetting
- Auto-update
- Cross-daemon plugin composition (plugins are daemon-local in V1)
- Attn-as-agent itself (the API enables it; shipping it is separate)

## Design conversation — how we got here

The design evolved across a multi-turn conversation on 2026-04-16. Recording the iteration so the reasoning survives:

1. **Started with declarative-manifest pitch** (YAML describing arg mapping, staging rules, classifier command). Rejected: state reporting is the core value of attn and cannot be expressed in YAML. Every existing integration (claude hooks, pi extension, codex/copilot transcript watchers) invests seriously in real code for this layer.
2. **Subprocess with shell companion files.** Similar rejection — the split between declarative manifest and arbitrary companion scripts is awkward, and plugin authors end up needing real logic for ID resolution, state machine edge cases, freshness protection, etc.
3. **In-process options considered**:
   - Go `plugin` package: broken on Windows, version-lockstep fragility on macOS/Linux, no unload, plugin panic crashes daemon, HashiCorp abandoned the approach years ago for these reasons. Rejected.
   - Embedded scripting (goja / gopher-lua / starlark-go / Yaegi): for a solo maintainer, "own a runtime + host API forever" is a larger long-term load than "own a JSON schema." Every internal refactor risks breaking plugins depending on exposed Go types. Plugin crashes can take the daemon down without aggressive `recover()` discipline. Rejected.
   - WASM: language-agnostic but painful ABI for the state-reporting event volume; you still own the host API problem. Rejected.
4. **Minimum viable ("normalize schema + publish protocol + docs, no installer").** Rejected because the user wants a product that sparks creativity and grows an ecosystem, not infrastructure-for-future-infrastructure.

**Landed:** subprocess plugins in TypeScript (Bun runtime), JSON-RPC 2.0 over the existing unix socket, long-running connection per plugin, concrete declared surfaces for daemon→plugin calls, real CLI and dev mode.

## Locked decisions

These have been explicitly confirmed in conversation:

- **Subprocess, not in-process.** Plugins run as their own processes. Crash isolation, language freedom, no runtime to maintain.
- **One mechanism covers agent drivers AND future non-driver plugins.** Plugins declare concrete surfaces they handle during the daemon handshake. No generic role taxonomy in the base protocol, and no second system for worktree customization, attn-as-agent, assistants, dashboards, etc.
- **Provider plugins are first-class.** Plugins may claim daemon-owned extension points that must run inside an attn operation, returning structured `handled`, `decline`, or `error` outcomes so attn can continue, fall back, or fail coherently.
- **TypeScript with Bun as the canonical runtime.**
- **Snipe is the first agent-driver plugin, not an in-tree driver.** See companion plan `2026-04-16-snipe-plugin.md`. Snipe is a Pi fork with its own session and permission capabilities; a future pure Pi integration remains a separate plugin. The first provider use case is user-owned worktree customization, proving that plugins are not limited to agent integration.
- **Trust model: user-installed = user-trusted.** Installing a plugin is equivalent to `curl | bash` from that repo. No sandboxing, no signing, no confirmation prompts, no catalog curation. Document clearly in the plugin-authoring README and move on.
- **No auth.** Unix socket filesystem permissions gate access. Consistent with existing frontend↔daemon and daemon↔daemon trust model in attn today.
- **Claude, codex, copilot stay in-tree.** No forcing function to migrate. Long-term they could become plugins but that's zero-pressure.
- **"Great product" target, not minimal infra.** Single maintainer, but the brief is build-it-well, not build-it-small.

## Open questions

These were proposed in conversation but not explicitly confirmed:

1. **Bun-only vs Bun-preferred SDK.** Proposed: Bun-only. Simpler, cleaner, bets on Bun. Alternative: Bun-preferred with Node fallback.
2. **Plugin distribution format.** Proposed: plugins ship TypeScript source + `package.json`, `attn plugin install` runs `bun install`, Bun is a prereq on the user's machine. Alternative: plugins ship `bun build --compile` binaries, no runtime dependency.
3. **Plugin lifecycle.** Proposed: attn spawns installed plugins at daemon startup and reconciles on plugin-dir change. User-managed plugins (dev mode, ad-hoc scripts) spawn themselves and connect. One-per-plugin vs one-per-session not fully decided.
4. **Remote daemons.** Proposed: plugins are daemon-local. Each daemon has its own plugin set. Install per host (SSH to remote, run `attn plugin install` there). Cross-daemon control plugins are deferred — likely exposing the same JSON-RPC over the hub's existing WebSocket later. User raised this as an open question worth capturing; design sketched but not confirmed.
5. **SDK shape and timing.** User pushed back on designing SDK top-down. **Revised approach: the first real plugins are written against raw JSON-RPC first. After the provider path proved out, extract a small SDK from those patterns; let richer plugin surfaces wait until their concrete implementations exist.** The protocol must still stay ergonomic enough to consume without a wrapper.
6. **Manifest format.** Probably TOML — minimal, human-readable, Bun ecosystem-neutral. Alternatives: JSON, YAML. Not confirmed.
7. **Install sources.** `attn plugin install <git-url>` as the headline, `--path <dir>` for local development. Not confirmed.
8. **API versioning discipline.** Proposed: strict `attn_api_version: 1` refuses to load on mismatch. Additive protocol changes don't bump version. Not confirmed.
9. **Socket coexistence.** Existing one-message-per-connection hook protocol (claude hooks, whatever remains of the old pi plan's pattern) must keep working. Proposed: detect JSON-RPC handshake by first-message shape and switch the connection's mode. Not confirmed.
10. **JSON-RPC framing.** Proposed: newline-delimited JSON. Alternative: LSP-style Content-Length headers. Unix socket + JSON makes newline-delimited the simpler choice.

## Architecture overview

```
┌─────────────────┐          ┌────────────────────────────┐
│  attn daemon    │          │  plugin (Bun process)      │
│                 │          │                            │
│  ~/.attn/       │◄─────────┤  long-running JSON-RPC     │
│  attn.sock      │ unix     │  connection                │
│                 │ socket   │                            │
│                 │          │  ┌──────────────────────┐  │
│                 │          │  │ user-facing agent    │  │
│                 │          │  │ (snipe, pi, ...)     │  │
│                 │          │  └──────────────────────┘  │
│                 │          │   spawned by plugin via    │
│                 │          │   driver.spawn response    │
│                 │          │   (attn puts it in a PTY)  │
└─────────────────┘          └────────────────────────────┘
```

### Plugin lifecycle — driver flow

1. attn spawns the plugin binary at daemon startup (or user runs it in dev mode).
2. Plugin connects to `~/.attn/attn.sock`, sends `hello { name, version, attn_api_version, surfaces? }`.
3. attn replies with accepted capabilities. An agent plugin then calls `driver.register { agent, capabilities }`; the base handshake does not carry a speculative role field.
4. When a session starts with agent = the registered name, attn calls `driver.spawn { session_id, run_id, cwd, label, yolo }` on the plugin. On reload of that same session, it calls `driver.resume` with the stored opaque metadata. Capability inputs such as `yolo` express attn-level behavior, not required command-line flag names; each driver returns the agent-specific equivalent argv.
5. Plugin responds `{ argv, env, cwd }`. attn spawns that command in a PTY under the session. **Attn owns the PTY. Plugin owns agent-specific semantics.**
6. Plugin stages companion files (extensions, hooks) however it likes. Plugin monitors the agent however it likes.
7. Plugin reports state via `session.report_state`, `session.report_stop`, `session.report_metadata`. The daemon supplies a fresh `run_id` per launched PTY, and the plugin sequences reports within that run so asynchronous work cannot overwrite newer status or a replacement run.
8. When an attn-owned plugin agent PTY exits or is killed, attn calls `driver.session_closed` so the plugin can dispose bridge tokens, watchers, classifiers, and staged launch resources for that run.
9. Plugin exits when attn shuts down or its own lifecycle ends.

### Provider and lifecycle surfaces

A provider plugin claims daemon-owned extension points that must run *inside* an attn operation before built-in behavior completes. Worktree creation also exposes lifecycle hooks for additive customization that should not own Git creation itself:

- `worktree.before_create`
- `worktree.create`
- `worktree.after_create`
- `worktree.delete`

This exists to support user-owned policies such as "services-pilot worktrees must be created through `spt git:worktree ...` rather than plain `git worktree add`", while keeping attn generic.

Provider dispatch contract:

- `worktree.before_create` runs every registered handler in deterministic order before create dispatch; an RPC error aborts before mutation
- attn asks eligible providers in deterministic order whether they want to handle the operation
- provider priority is user-owned attn configuration, not plugin-declared metadata; higher user priority runs first and ties remain deterministic
- a provider returns `handled`, `decline`, or `error`
- `handled` includes the structured result attn needs to continue
- `decline` lets attn ask the next provider or fall back to its built-in implementation
- `error` fails the operation and surfaces the provider's message to the user
- attn validates provider results before committing them to core state
- `worktree.after_create` runs every registered handler after the created worktree is recorded; hook failures surface to the caller with the created path still preserved

Example `worktree.create` request:

```json
{
  "jsonrpc": "2.0",
  "id": 41,
  "method": "worktree.create",
  "params": {
    "main_repo": "/Users/victora/src/services-pilot/master",
    "branch": "feat/worktree-driver",
    "starting_from": "origin/master",
    "requested_path": null
  }
}
```

Handled response:

```json
{
  "jsonrpc": "2.0",
  "id": 41,
  "result": {
    "status": "handled",
    "path": "/Users/victora/src/services-pilot/master/.worktrees/feat-worktree-driver",
    "branch": "feat/worktree-driver"
  }
}
```

Decline response:

```json
{
  "jsonrpc": "2.0",
  "id": 41,
  "result": {
    "status": "decline"
  }
}
```

## V1 scope

### 1. Schema normalization (prerequisite)

Remove agent-specific fields from core schema and protocol. Replaces the agent-specific additions proposed during the earlier Pi exploration and keeps Snipe and future Pi plugins independent.

- Generic `agent_metadata TEXT` column on `sessions` (JSON blob, opaque to core) replacing any proposed `pi_session_file` / `pi_session_id` columns.
- Use generic `session.report_metadata` rather than any agent-specific linkage command.
- Use `session.report_stop { verdict }`; classifiers stay plugin-owned and emit only their ordered result.
- Store: `ApplyAgentDriverMetadata(sessionID, runID, seq, jsonBlob)` / `GetAgentMetadata(sessionID)`, so metadata writes follow the same stale-report ordering rule as state.

### 2. Control API over unix socket

Extend the daemon's existing unix socket handler to recognize a JSON-RPC 2.0 handshake and switch the connection into long-running RPC mode. Legacy one-message-per-connection hook traffic keeps working.

**Handshake:**
```json
→ {"jsonrpc":"2.0","id":1,"method":"hello","params":{
    "name":"snipe-driver","version":"0.1.0",
    "attn_api_version":1}}
← {"jsonrpc":"2.0","id":1,"result":{"ok":true}}
```

**Driver-role methods (minimum):**

| Method                        | Direction       | Purpose                                                                 |
|-------------------------------|-----------------|-------------------------------------------------------------------------|
| `driver.register`             | plugin → attn   | Declare `{ agent, capabilities }`                                       |
| `driver.spawn`                | attn → plugin   | Request `{ session_id, run_id, ... }`, returning `{ argv, env, cwd }` for a new session |
| `driver.resume`               | attn → plugin   | Same, for resume; receives a fresh `run_id` for the replacement PTY      |
| `driver.session_closed`       | attn → plugin   | `{ session_id, run_id, reason, exit_code?, signal? }`; dispose resources for the ended PTY run |
| `session.report_state`        | plugin → attn   | `{ session_id, run_id, seq, state }`; stale or ended-run status is discarded |
| `session.report_stop`         | plugin → attn   | `{ session_id, run_id, seq, verdict }`; classification reuses the `seq` assigned when stop was observed |
| `session.report_metadata`     | plugin → attn   | `{ session_id, run_id, seq, metadata }`; persists opaque native-session data |
| `pty.request_spawn`           | plugin → attn   | Deferred: optionally spawn additional processes under the session       |

`seq` is monotonic across reports within one `run_id`. When classification begins after an observed stop, it reserves its report sequence at that observation; any later lifecycle report receives a higher sequence and therefore wins even if classification finishes afterward.

**Worktree surfaces (initial):**

| Method                   | Direction     | Purpose                                                                 |
|--------------------------|---------------|-------------------------------------------------------------------------|
| `worktree.before_create` | attn → plugin | Run a pre-create lifecycle hook before provider/default worktree logic |
| `worktree.create`        | attn → plugin | Offer a worktree creation operation to a provider                       |
| `worktree.after_create`  | attn → plugin | Run a post-create lifecycle hook with the actual created worktree      |
| `worktree.delete`        | attn → plugin | Offer a worktree deletion operation to a provider                       |

Surfaces are declared once in the `hello` handshake via `surfaces`. Provider
methods return structured `handled`, `decline`, or `error` results. Lifecycle
hooks complete successfully by returning a normal JSON-RPC response and fail by
returning an RPC error. For `worktree.create`, `handled` must include the
actual created `path` and resulting `branch`. Attn validates that the returned
path is a real worktree of the expected main repo before storing it or launching
a session.

### 3. Plugin CLI + installer + dev mode

Commands:

- `attn plugin install <git-url>` — clone to `~/.attn/plugins/<name>/`, validate manifest, run `bun install`, register, spawn.
- `attn plugin install --path <dir>` — same, from local directory (development).
- `attn plugin list` — installed plugins and status.
- `attn plugin remove <name>` — uninstall + stop.
- Settings exposes the same installed-plugin inventory plus provider priority controls so users can resolve competing plugins without editing plugin code.
- `attn plugin update <name>` — `git pull && bun install`, restart.
- `attn plugin dev --path <dir>` — run attn with plugin spawned foreground, auto-restart on source change, stderr piped to terminal.
- `attn plugin inspect <name>` — print live JSON-RPC traffic for a plugin.

Manifest (`attn-plugin.toml` at repo root):
```toml
name = "snipe-driver"
version = "0.1.0"
attn_api_version = 1
description = "Snipe coding agent driver for attn"

[plugin]
entrypoint = "src/index.ts"
# bun runs this with: bun run <entrypoint>
```

## Phases

Rough phasing — to refine:

### Phase 0 — Schema normalization (implemented)

Core sessions carry opaque `agent_metadata`, and `SessionAgent` is an open identifier so external driver IDs survive storage and protocol transport.

### Phase 1 — Protocol + socket handler (implemented)

Extend daemon socket handler to accept JSON-RPC handshake and maintain a long-running plugin connection. Exercise request/response flow end-to-end against throwaway test plugins so both daemon-initiated calls and plugin-initiated calls are proven before real provider or driver work lands. Document the wire protocol.

### Phase 2 — Provider registration + worktree proving use case (implemented)

Add concrete surface declaration and provider dispatch in the daemon. Ship `worktree.before_create`, `worktree.create`, `worktree.after_create`, and `worktree.delete` as the first extension points, with fallthrough to attn's built-in Git behavior when create providers decline.

The proving plugin is a user-owned worktree provider that mirrors the existing shell-level `wt-create` / `wt-remove` policy:

- special-case services-pilot by routing creation and deletion through `spt git:worktree ...`
- decline for repos it does not own so attn keeps its default Git flow
- return structured create results so attn can register the actual worktree path

This phase proves that plugins can participate in daemon-owned operations, not only launch agents or react after the fact.

### Phase 3 — CLI + installer + dev mode (partially implemented)

Implemented: manifest parsing, installed-plugin spawn/reconcile in the daemon, and
the local development path needed for the first real plugins:
`attn plugin install --path <dir>`, `attn plugin list`, and
`attn plugin remove <name>`.

Still to ship: git URL install, update, foreground dev/auto-restart mode, and
live traffic inspection. None blocks building or exercising `attn-snipe`
locally through `--path`.

### Phase 4 — Provider SDK foundation (implemented)

Extract the proven raw JSON-RPC provider client into a small TypeScript SDK:

- connection + hello handshake
- surface registration derived from registered SDK handlers
- daemon-request routing
- typed provider result helpers
- typed worktree provider request/result contracts
- one checked-in example provider plugin that consumes the SDK end to end

Keep broader surfaces out until their first concrete plugins exercise those paths.

### Phase 5 — Snipe as the first agent-driver plugin (Slices 1-6 implemented)

See companion plan `2026-04-16-snipe-plugin.md`. Snipe forces driver-protocol gaps to surface after the provider model has already been validated against a non-agent extension point. Its eventual full integration includes state, classification, reload of the existing attn-owned session through the generic `driver.resume` method, mapping attn's `yolo` behavioral intent onto Snipe-compatible permission/sandbox flags, and native approval visibility. Snipe-native session replacement such as `/fork` is observed through generic metadata updates; it does not require an attn driver capability.

`~/src/attn-snipe` now provides all six planned vertical slices: raw driver
registration, persisted `--session-id` launch, a private token-scoped
extension bridge, sequenced metadata/basic lifecycle reports, visible
`unknown` state if bridge startup or an active bridge connection fails, and
Haiku-backed sequence-safe stop classification. Generic `driver.resume` now
reloads a written Snipe conversation strictly from stored opaque metadata,
while an explicitly unmaterialized empty launch may safely reopen through its
pinned ID because there is no conversation to discard. Attn yolo behavior is
mapped onto Snipe's explicit bypass-permissions and sandbox-off flags so
persisted identity and strict reload remain intact. Read-only native Snipe
approval-wait events are translated to generic `pending_approval` state.
Repository-checkout installation is documented without publishing an
artifact, and plugin health now rejects missing or CLI-incompatible Snipe
executables through the existing attn Settings health surface while warning
about approval-event compatibility until Snipe publishes a probeable contract.
Attn does not expose
a new-session native Snipe picker workflow, so it is not part of the plugin
scope.

#### Driver host verification harness (implemented)

The driver host is gated by a deterministic process-boundary test, not only mocked RPC unit tests. The test starts an installed fixture plugin through the plugin process manager, registers a dynamic driver over the real Unix JSON-RPC socket, creates and resumes sessions through the real WebSocket API, and launches the returned command through the production worker PTY path.

The harness verifies:

- registration makes the fixture agent selectable and plugin disconnect removes it
- `driver.spawn` receives the requested `yolo` behavioral intent
- returned `argv`, `env`, and `cwd` determine what actually runs inside the PTY
- state, stop verdict, and opaque native-session metadata survive sequenced reports sent immediately during launch
- after a session is live, plugin reports produce visible `working` then `waiting_input` WebSocket status transitions
- delayed state and stop/classification reports cannot overwrite a newer sequence or a replacement run
- `driver.resume` receives persisted metadata from the prior run and its immediate reports survive resumed PTY startup
- PTY exit produces `driver.session_closed` with the specific ended `run_id`

This is the minimum regression gate before wiring Snipe itself: a plugin that works in the harness has crossed the same daemon, socket, WebSocket, session-store, and PTY boundaries it will cross in production.

### Phase 6 — Broader plugin surfaces

Provider dispatch and future driver work are the tightest V1 requirements. Assistant, automation, and observer-style surfaces should be designed only when their first concrete plugins are in hand. No second mechanism, just more explicit surfaces on the same protocol.

### Phase 7 — Documentation + templates

"Write your first plugin" tutorial. `attn plugin new driver <name>` scaffolding. Example plugins repo. README for external plugin authors.

## Runtime health foundation (implemented)

The runtime-health and Settings gaps exposed by installed plugins were closed before the generic agent-driver host:

1. **Plugin healthcheck, end to end.**
   - The daemon uses an explicit health protocol instead of treating an open socket as sufficient proof of health.
   - Process running, socket connected, and healthy/unhealthy/unknown plugin state are distinguished in inventory and Settings.

2. **Settings panel redesign.**
   - Settings is organized as a workbench with separate areas for plugins, agents, connectivity, and related preferences instead of one mixed panel.

## Deferred — post-V1

In priority order of what might come next:

- **SDK expansion.** Extend `@victorarias/attn-plugin` beyond the initial provider foundation once broader surfaces have real plugin implementations. Reconnect logic and richer typed helpers belong here, not in the first extraction.
- **Cross-daemon control API.** Expose the same JSON-RPC surface over the hub's existing WebSocket at `:9849` so future control plugins can span daemons. Useful for attn-as-agent and dashboards.
- **Custom UI extensions.** Separate design.
- **Plugin install over SSH / hub** — `attn --endpoint <server> plugin install <url>`. Nice-to-have, requires proxying through the hub.
- **Pure Pi agent-driver plugin.** Build `attn-pi` independently after the generic driver surface is proven by Snipe; do not couple its session or permission behavior to the fork.
- **Claude / codex / copilot migration** to plugins. Zero pressure; only if there's a reason.

## References

- **Deferred pure Pi exploration:** `docs/plans/2026-04-07-pi-integration.md` — reference material for a later independent Pi plugin, not the Snipe design.
- **Snipe plugin plan:** `docs/plans/2026-04-16-snipe-plugin.md`.
- **Existing agent driver interface:** `internal/agent/driver.go`.
- **Existing socket handler:** `internal/daemon/daemon.go` (`handleConnection`).
- **Hub module (cross-daemon context):** `internal/hub/`.
- **Bun:** https://bun.sh — runtime, bundler, test runner. Direct TS execution, `Bun.connect()` for unix sockets, `bun build --compile` for single-file binaries.
- **JSON-RPC 2.0 spec:** https://www.jsonrpc.org/specification
