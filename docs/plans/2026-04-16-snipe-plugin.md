# Snipe as the first attn agent-driver plugin

**Status**: IMPLEMENTED - Slices 1 through 6 are implemented in `~/src/attn-snipe`; installation uses the private repository checkout without package publication.
**Date**: 2026-04-16, revised 2026-05-25
**Owner**: victor

## Summary

Build `~/src/attn-snipe` as an external attn plugin for `~/src/Snipe`, the Spotify fork of Pi. The plugin launches the `snipe` executable, loads a small companion Snipe extension with `-e`, and translates Snipe lifecycle and permission behavior into attn's generic driver protocol.

This is a full-integration plan delivered in useful slices. The end state includes launch, persisted identity, reload of an existing attn-owned session, real lifecycle state, classification, approval visibility, yolo/permission-mode behavior, configuration, installation, and operational diagnostics.

Snipe is the first concrete agent-driver consumer of the plugin system; it is not a replacement for a future pure Pi plugin. A later `attn-pi` plugin should use the same generic driver surfaces while implementing Pi's different session, permission, and distribution behavior independently.

## Why Snipe

The earlier Pi proposal proved the basic shape of an extension-backed driver, but Snipe is the agent intended for immediate use and its fork-specific capabilities change the correct integration:

- Snipe is installed and invoked as `snipe` / `@spotify/snipe`, with data in `~/.snipe/`.
- Snipe already provides `--session-id <id>` to create or open a persisted session under a caller-owned ID.
- Snipe supports permissions and sandbox controls, including `--permission-mode`, `--sandbox`, and `--yolo`.
- Snipe extensions import from `@spotify/snipe` and expose lifecycle events and session metadata APIs needed for attn reporting.

Building directly against verified Snipe behavior removes Pi-specific workarounds and pressures attn's driver protocol with a real, richer agent.

## Verified Snipe Surface

These are current implementation facts that this plan relies on:

| Need | Surface | Verification source |
|---|---|---|
| Load companion integration code | `snipe -e <extension.ts>` | `docs/extensions.md`, `src/cli/args.ts` |
| Lifecycle reporting | `session_start`, `input`, `before_agent_start`, `agent_start`, `agent_end`, `session_shutdown` | `docs/extensions.md`, `src/core/extensions/types.ts` |
| Agent metadata | `ctx.sessionManager.getSessionId()`, `getSessionFile()`, `getBranch()`; `snipe.appendEntry()` | `docs/session.md`, `src/core/extensions/types.ts` |
| Pinned persisted launch | `--session-id <id>` opens or creates `<id>.jsonl`; IDs allow attn UUIDs | Snipe `src/main.ts`, `src/core/session-manager.ts`; attn `internal/wrapper/wrapper.go` |
| Strict session reopen for attn reload | `--resume <id>` aliases strict `--session <id>` | `src/cli/args.ts`, `src/main.ts` |
| Runtime security controls | `--permission-mode <mode>`, `--sandbox <on|off>`, `--yolo` | `src/cli/args.ts`, `src/cli/runtime-flags.ts` |
| Native yolo semantics | `--yolo` is shorthand for bypassed permissions plus sandbox-off, but deliberately always starts a new session and therefore rejects session selection flags such as `--session-id` and `--resume` | `src/main.ts`, `src/cli/args.ts`, `src/cli/runtime-flags.ts` |
| Approval waiting | Read-only `approval_wait_start` / `approval_wait_end` extension events bracket native permission and plan-approval dialogs | `src/core/agent-session.ts`, `src/core/extensions/types.ts`, `docs/extensions.md` |

## Vision

From attn, Snipe behaves as a first-class external agent:

- Users select agent `snipe`, launch sessions, reload an existing session, use worktrees, and see accurate attn state without knowing a plugin is involved.
- A normal persisted attn launch creates a Snipe session tied to the attn session ID, while metadata also records Snipe's actual file and ID for reload and cross-directory operations.
- Snipe lifecycle events drive `working`, stopped-classification results, and shutdown state; native permission prompts drive `pending_approval`.
- Attn's `yolo` request is a behavioral intent: run without interactive permission prompts or sandbox enforcement. The plugin produces Snipe's equivalent behavior without losing session continuity; it is not required to pass a same-named Snipe flag.
- The attn daemon knows only generic driver capabilities and generic metadata. Snipe behavior stays in `attn-snipe`, except for a narrow event addition in Snipe needed to expose its own approval waits.
- A future `attn-pi` plugin can sit beside `attn-snipe`; neither plugin imports or impersonates the other.

## Ownership Boundaries

### `attn` core

- Defines generic driver requests/results and session reporting methods. Each launch gets a daemon-owned `run_id`; reports carry a monotonically increasing `seq` within that run.
- Spawns the returned command in the managed PTY.
- Persists opaque `agent_metadata`, stores the accepted report cursor for the active run, rejects stale or ended-run reports, tells the plugin when the PTY closes, and displays driver-reported state.
- Contains no Snipe session paths, CLI flag mapping, extension source, or classifier subprocess logic.

### `~/src/attn-snipe`

- Owns the plugin manifest, JSON-RPC client, Snipe CLI argument mapping, extension staging, bridge transport, metadata shape, classification policy, and diagnostics.
- Registers agent `snipe` and its real capabilities.
- Is the only process that speaks attn's plugin protocol on behalf of Snipe.

### `~/src/Snipe`

- Remains the agent/runtime source of truth.
- Requires only a narrowly scoped extension-event addition if native permission/plan approval waits cannot otherwise be observed.
- Does not gain attn protocol or attn socket dependencies.

## Target Architecture

```
attn daemon
  |  generic JSON-RPC: driver.* / session.report_*
  v
attn-snipe plugin process (Bun, one per daemon/plugin instance)
  |  private authenticated local bridge, keyed by attn session ID
  v
staged attn-snipe-extension.ts loaded by `snipe -e <path>`
  |
  v
snipe interactive process inside attn-owned PTY
```

The companion Snipe extension relays to the `attn-snipe` process, not directly to `~/.attn/attn.sock`. This keeps the plugin as the single translator and ensures future Snipe changes do not turn into attn protocol clients scattered across staged extensions.

The bridge must disambiguate concurrent Snipe sessions and replacement processes for one session. The daemon supplies `run_id` in `driver.spawn`/`driver.resume`; the plugin supplies that run identity and a private per-launch token to the extension. Each forwarded metadata/state/stop report consumes an increasing `seq` within that run. For asynchronous classification, the stop report reserves its sequence when `agent_end` is observed, before later reports can advance the cursor. On `driver.session_closed`, the plugin invalidates that token and cancels any pending classifier for that run.

## Driver Behavior

### Registration

The plugin registers:

```json
{
  "agent": "snipe",
  "capabilities": {
    "resume": true,
    "yolo": true,
    "classifier": true,
    "state_reporting": true,
    "pending_approval": true
  }
}
```

Slice 5 enables `pending_approval` only because the required Snipe event surface is now available. Earlier slices correctly reported the capability as false rather than silently missing approval waits.

### Launch And Identity

For a normal persisted new session, return a command equivalent to:

```bash
snipe --session-id <attn-session-uuid> -e <staged-extension-path>
```

Attn session IDs are UUIDs and satisfy Snipe's safe session-ID validation. On `session_start`, the extension relays:

```json
{
  "snipe_session_id": "<ctx.sessionManager.getSessionId()>",
  "snipe_session_file": "<ctx.sessionManager.getSessionFile()>",
  "persistence": "persisted",
  "materialized": false
}
```

The plugin sends that as generic `session.report_metadata` and writes an `attn.link` custom Snipe session entry containing the attn session ID. Matching IDs make ordinary reload simple; stored metadata remains necessary for Snipe-native session changes, directory changes, and diagnostics.

Snipe assigns `getSessionFile()` immediately but deliberately defers writing a
new JSONL file until the first assistant message exists, so untouched launches
do not pollute its resume list. The plugin records that as
`materialized: false` while the assigned file does not exist. At `agent_end`,
after Snipe has flushed its first assistant turn, the plugin refreshes metadata
to `materialized: true`. This lets reload distinguish an intentionally empty
unwritten session from a missing persisted conversation.

For `--no-session` passthrough, report `persistence: "memory"` and no file. Ephemeral sessions cannot promise resume.

### Reload

Attn exposes reload for an existing session; it does not expose "create a new session and select an old Snipe conversation to resume." The integration therefore implements only the product behavior users can reach:

| Attn intent | Snipe invocation strategy | Notes |
|---|---|---|
| New persisted session | `--session-id <attn-session-id>` | Opens or creates a same-ID Snipe session |
| Reload existing attn session with a written conversation | `--session <stored-snipe-file-or-id>` | Strict open; a missing transcript fails rather than silently opening a new session |
| Reload explicitly unmaterialized empty session | `--session-id <stored-snipe-id>` | Safe exception: Snipe has not yet written any conversation that could be lost |

The existing reload path already supplies the required generic primitives: when the UI reloads an existing session, the daemon calls `driver.resume` for that session with a fresh `run_id` and its stored opaque `metadata`. The plugin consumes that metadata to reopen the precise Snipe session. It must not add a Snipe-specific daemon lookup.

The protocol capability is still named `resume`, because `driver.resume` is
the generic replacement-PTY method used by reload. Advertising `resume: true`
does not imply a separate Snipe picker action exists in the attn UI.

Attn no longer exposes an agent-level fork operation, so `attn-snipe` does not register a fork capability or require a fork driver method. If the user runs Snipe's own `/fork` or another native session replacement inside the terminal, the extension observes the resulting `session_start` and reports the new active identity through the same generic metadata path. That keeps attn attached to what is actually on screen without reintroducing fork as an attn feature.

### Yolo And Permission Modes

In the attn driver protocol, `yolo: true` means the user requested attn's bypass behavior. It does not mean the plugin must append a literal `--yolo` argument to the agent command. Each agent driver maps that intent onto the safest equivalent runtime controls its agent provides.

Snipe's native `--yolo` is intentionally a new-session convenience flag: Snipe validates that it is not combined with `--session-id`, `--session`/`--resume`, `--continue`, `--fork`, or `--plan`. Attn requires a pinned Snipe ID for new managed sessions and an exact stored identity for reload, so forwarding native `--yolo` would either be rejected or require dropping attn's identity guarantee.

For attn yolo launches or reloads, `attn-snipe` maps the intent to:

```bash
snipe --permission-mode bypassPermissions --sandbox off <session flags> -e <extension>
```

This works because Snipe documents and implements native `--yolo` as shorthand for exactly these two runtime settings, while the explicit flags are compatible with pinned and reloaded sessions. Native `--yolo` is not used by the plugin unless a future deliberately ephemeral launch mode explicitly requests Snipe's new-session-only behavior.

### Lifecycle And State Mapping

| Snipe observation | Plugin report | Purpose |
|---|---|---|
| `session_start` after the process is ready | metadata plus `idle` | Session can accept input and is no longer merely launching |
| `input`, `before_agent_start`, or `agent_start` | `working` | Work begins promptly even before model output |
| Native permission/plan approval prompt begins | `pending_approval` | Surface genuine user blockage |
| Approval prompt resolves while turn continues | `working` | Restore active state |
| `agent_end` | assign the next report sequence, run classification, then report stop verdict with that same sequence | Distinguish done from waiting for user direction without overwriting newer activity |
| `session_shutdown` or child exit | `idle` or terminal failure state as appropriate | Do not leave stale working state |
| Bridge/extension unavailable after launch | `unknown` with diagnostic | Missing observability must be visible |

Snipe publishes read-only `approval_wait_start` / `approval_wait_end` extension events around native permission and plan-approval waits. `attn-snipe` consumes those events without receiving any ability to approve or deny the underlying prompt.

### Classification

The plugin owns Snipe classification end-to-end:

1. At `agent_end`, the extension extracts relevant assistant output from `event.messages` or the current branch and relays it to the plugin.
2. The plugin invokes Snipe non-interactively with tools and extensions disabled. It defaults to Anthropic Haiku (`claude-haiku-4-5-20251001`) and accepts an `ATTN_SNIPE_CLASSIFIER_MODEL` override:

```bash
snipe -p --no-tools --no-extensions --no-mcp --no-session --mode text --model <model> '<classifier prompt>'
```

3. The implementation supplies the prompt through a structured argument array, never a shell-interpolated command string.
4. The plugin reports `session.report_stop { run_id, seq: <agent_end sequence>, verdict: "idle" | "waiting_input" }`. It does not allocate a sequence when classification finishes: a prompt, approval wait, or new turn observed while classification runs must win.

Slice 2 requires Snipe's `--no-mcp` support (to be upstreamed in Snipe) so
short-lived classifier processes do not initialize configured MCP servers.
Snipe print mode writes assistant text without rendering it; Haiku may still
Markdown-wrap its JSON output. The plugin therefore accepts a strict verdict
object inside Markdown fencing and terminates the classifier child as soon as
that valid verdict is received as a defensive lifecycle bound.

This prevents attn core from learning how to invoke Snipe. If classification fails or yields no defensible verdict, the plugin reports `unknown` with diagnostics rather than guessing.

## Repository Layout

```
~/src/attn-snipe/
|-- attn-plugin.toml
|-- package.json
|-- src/
|   |-- index.ts                 # handshake, registration, request routing
|   |-- driver.ts                # spawn/resume and capability mapping
|   |-- bridge.ts                # private extension-to-plugin relay
|   |-- state.ts                 # lifecycle/state translation
|   |-- classifier.ts            # Snipe-based stop classification
|   |-- metadata.ts              # opaque Snipe metadata contract
|   `-- snipe-extension.ts       # staged into launches through `-e`
|-- test/
|-- README.md
`-- CHANGELOG.md
```

## Success Criteria

- `attn` can install and load `~/src/attn-snipe` without any in-tree Snipe driver.
- New persisted Snipe sessions launched by attn are recoverably associated with their attn session and recorded Snipe metadata.
- Reload reopens the existing attn session's exact stored Snipe conversation without accidentally creating a fresh session when its stored identity is missing.
- Attn accurately shows Snipe working, stopped/waiting, approval-waiting, shutdown, and observability-failure states.
- Attn yolo behavior launches Snipe with bypassed permissions and disabled sandbox while preserving reload/session semantics.
- Classification is implemented in the plugin and does not introduce Snipe subprocess knowledge into attn core.
- Installation from the private `attn-snipe` repository works by pasting its Git URL in Settings or through local development checkout installation with `attn plugin install --path`, and incompatible Snipe CLI installations are reported as unhealthy.
- Both `attn-snipe` and a later independent `attn-pi` can register through the same generic driver protocol.

## Incremental Delivery Slices

Each slice must be working within its stated scope; later slices expand supported behavior without replacing a knowingly broken earlier implementation.

### Slice 1 - Launch, Bridge, And Real Lifecycle

**Status:** Implemented.

- Create `~/src/attn-snipe` with manifest and raw JSON-RPC driver registration.
- Stage/load `snipe-extension.ts`; connect it to the plugin through the private bridge.
- Support normal persisted launches with `--session-id <attn-session-id>`.
- Report metadata, ready/idle, working, shutdown, and explicit `unknown` on bridge failure with monotonically sequenced observations for the current run.

**Useful result:** users can run Snipe from attn and see trustworthy basic active/idle lifecycle state with durable identity.

### Slice 2 - Stop Classification

**Status:** Implemented.

- Extract end-of-turn assistant content defensively.
- Run Snipe-owned classification and emit sequence-safe stop verdicts.
- Log classifier failure without corrupting session state.

**Useful result:** notifications and attention state distinguish a finished Snipe task from one awaiting user direction.

### Slice 3 - Reload Existing Session

**Status:** Implemented.

- Consume persisted opaque metadata already supplied by attn in external `driver.resume` requests issued during reload.
- Implement strict reopen by stored Snipe file/ID when the existing attn session is reloaded.
- Reopen a session that is explicitly still empty and unmaterialized through its pinned Snipe ID, without weakening strict reopen once conversation data exists.
- Cover Snipe session switches occurring inside the TUI, including Snipe's own `/fork`, by replacing current metadata on `session_start`.

**Useful result:** the exposed attn reload action keeps the same Snipe conversation, including after Snipe-native session navigation.

### Slice 4 - Security Modes And Yolo

**Status:** Implemented.

- Advertise support for attn's `yolo` behavior capability.
- Translate the attn `yolo` intent to `--permission-mode bypassPermissions --sandbox off`, including reloaded sessions.
- Document and test why the plugin does not forward native `--yolo` for persisted workflows.

**Useful result:** users can choose the established attn bypass workflow without sacrificing Snipe session reload.

### Slice 5 - Native Pending Approval

**Status:** Implemented.

- Add the minimal read-only Snipe extension lifecycle signal for permission/plan approval wait begin/end.
- Consume it in `attn-snipe` and advertise `pending_approval: true`.
- Verify denial, approval, abort, and shutdown while prompting.

**Useful result:** attn can reliably notify the user when Snipe is actually blocked on approval.

### Slice 6 - Distribution And Operational Polish

**Status:** Implemented.

- Document repository-based installation through a pasted Git URL in attn Settings, while retaining local `attn plugin install --path ~/src/attn-snipe` for development; package/tagged publication is intentionally deferred.
- Report plugin health failures when the configured Snipe executable cannot launch or lacks the CLI flags required for managed identity, reload, yolo mapping, or MCP-free classification; show a temporary healthy-state advisory because approval-event compatibility cannot yet be CLI-probed.
- Document configuration for executable/model overrides, the currently non-probeable approval-event prerequisite, changelogs, and coexistence with the eventual pure Pi plugin.
- Verify installation from the private Git repository through Settings and local installation from a checkout; later add update/tagged distribution only when desired.

**Useful result:** integration is maintainable and installable outside the development checkout.

## Shortcuts And Slicing

The delivery slices above are intentional vertical slices. These shortcuts are forbidden:

- Do not build Snipe logic into an in-tree Go driver as a temporary bridge.
- Do not rename Pi constants or reuse a Pi plugin identity for Snipe; pure Pi remains a separate future plugin.
- Do not have the staged Snipe extension speak attn's public protocol directly; lifecycle translation belongs to `attn-snipe`.
- Do not call `--session-id` for a reload after Snipe has materialized conversation data: its create-on-miss behavior can hide missing data by opening a new session. The only accepted reload exception is stored `materialized: false` metadata for a session that never wrote a first turn.
- Do not interpret attn's `yolo` capability as a same-named flag passthrough: for Snipe managed or reloaded sessions, map the requested behavior to `--permission-mode bypassPermissions --sandbox off`.
- Do not infer `pending_approval` from generic tool activity; advertise it only from native Snipe approval-wait events.
- Do not silently treat a missing bridge, failed extension load, or failed classifier as idle.
- Do not add Snipe-specific columns, protocol messages, or metadata parsing to attn core.
- Do not extract a shared `attn-pi`/`attn-snipe` package before both implementations prove actual duplication; share protocol types through the generic SDK only when earned.

The intentionally deferred items are bounded:

| Deferred from earlier slice | Extension point already preserved | Completion signal |
|---|---|---|
| Classification after lifecycle-only launch | `agent_end` relay and `session.report_stop` surface | Stops classify into idle/waiting with failure coverage |
| Reload after initial launch or Snipe-native identity change | Opaque metadata reported from slice 1 | Strict and explicitly unmaterialized-empty reload integration tests pass |
| Yolo | Central CLI mapping in `driver.ts` | Persisted and reloaded bypass flows pass |
| Approval visibility | Generic state reporting plus Snipe extension event addition | Native prompt transitions to/from `pending_approval` |
| Pure Pi integration | Generic driver protocol has no Snipe names | Independent `attn-pi` plan/plugin can register separately |

## Non-Goals

- Making Snipe an in-tree agent driver.
- Modifying Snipe to speak attn JSON-RPC directly.
- Migrating Claude, Codex, or Copilot out of tree as part of this work.
- Treating the future pure Pi plugin as an alias or build variant of `attn-snipe`.
- Surfacing Snipe-specific session metadata in attn UI unless a later usability need justifies it.

## Cleanup Checklist

- Remove or update remaining documentation that says Pi is the first agent-driver plugin.
- Keep obsolete Pi-specific proposed core fields/commands unimplemented; use only generic metadata/reporting.
- Remove any temporary bridge diagnostics once equivalent structured plugin diagnostics are shipped.
- Upstream the approval-wait event addition to Snipe; the plugin depends on that public event contract rather than development-only instrumentation.
- Replace the temporary health advisory with a released-version or explicit capability probe once Snipe exposes a stable way to identify approval-event support.
- After the generic driver SDK is extracted from real plugins, replace hand-written protocol repetition in `attn-snipe` only where the SDK covers it without losing Snipe-specific behavior.

## Automated Verification

### In `attn`

- Driver protocol tests for registration, per-launch run identity, sequenced metadata/state/stop reporting, PTY-close callbacks, and disconnect/failure behavior.
- Store/protocol tests proving generic opaque metadata round trips without Snipe-specific schema.
- Daemon tests proving stale classifier or bridge reports cannot overwrite fresher state.

### In `attn-snipe`

- Command mapping tests for new, strict persisted reload, guarded unmaterialized-empty reload, ephemeral, and yolo-with-persisted-session cases.
- Extension metadata tests for Snipe-native session replacement, including `/fork`, without an attn fork driver method.
- Bridge tests for token/session isolation, concurrent Snipe sessions, disconnect, malformed messages, and extension reconnection/failure behavior.
- Extension tests using a fake bridge and Snipe lifecycle fixtures.
- Classifier tests for idle, waiting, empty content, subprocess failure, and model-selection fallback.
- Health tests for a usable Snipe executable, missing executable, missing required CLI flags, and daemon `attn.health` routing.

### In `Snipe`

- Existing Snipe checks remain green for any required event addition.
- New tests prove approval-wait lifecycle events fire exactly once for allow, deny, abort, and plan approval paths without changing permission enforcement.

## Manual Verification

1. Clone the private `attn-snipe` repository, install it by local path into an isolated attn profile, and confirm plugin health reports the configured compatible Snipe executable as healthy with the temporary approval-event advisory.
2. Confirm the reported Snipe session ID/file metadata corresponds to the attn session; before a turn, confirm metadata is unmaterialized and immediate reload preserves the empty pinned session; after one completed turn, confirm the JSONL exists, contains the buffered `attn.link` entry, and metadata becomes materialized.
3. Send a prompt that finishes and one that requests input; confirm attn shows the classified outcomes.
4. Reload the existing attn session after a completed turn and confirm the same Snipe conversation reopens strictly; within Snipe, run `/fork`, reload again, and confirm the updated active metadata is honored without any attn-level fork command.
5. Launch and reload with attn yolo enabled; confirm Snipe is in bypass-permissions/sandbox-off mode while the same persisted session is retained.
6. Trigger a native Snipe permission or plan approval prompt; confirm attn enters `pending_approval` only while the prompt is unresolved.
7. Break the bridge or extension intentionally in development and confirm attn surfaces `unknown`/diagnostics rather than an apparently successful idle state.
8. When reinstalling a locally edited extension, clear or reapprove only the affected Snipe extension-integrity hash entry before retrying; Snipe correctly rejects changed extension contents previously approved at the same installed path.

## References

- Plugin system plan: `docs/plans/2026-04-16-plugin-system.md`
- Prior pure Pi behavioral exploration: `docs/plans/2026-04-07-pi-integration.md`
- Snipe source checkout: `~/src/Snipe`
- Snipe extension API: `~/src/Snipe/docs/extensions.md`
- Snipe session API: `~/src/Snipe/docs/session.md`
- Snipe CLI/session mapping: `~/src/Snipe/src/cli/args.ts`, `~/src/Snipe/src/main.ts`
- Snipe permissions: `~/src/Snipe/src/core/agent-session.ts`, `~/src/Snipe/src/core/agent-session-tool-hooks.ts`
