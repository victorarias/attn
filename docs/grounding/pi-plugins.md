# Grounding: pi plugins (driver + suite)

2026-07-19, pi at v0.80.10, commit f1c587dd.

## The question

Three blindspots gated the first chunk of `docs/vision/pi-attn-plugins.md`:
(1) pi's extension runtime under long-lived sessions, (2) pi's release
cadence / pinnable API contract, (3) pi's TUI under attn's PTY geometry rules
vs claude/codex. All three grounded 2026-07-19; none invalidates the vision.

## Blindspot 1 — extension runtime

- Extensions load per-session via factory `(pi: ExtensionAPI) => void|Promise<void>`;
  discovery from `<cwd>/.pi/extensions/`, `<agentDir>/extensions/`, configured
  paths (core/extensions/loader.ts:673-721). Module factory cached per
  process+cwd (loader.ts:142-164); factory re-invoked with fresh
  Extension/API on every transition (loader.ts:454-480).
- Full teardown + re-instantiation on resume: switchSession emits
  `session_before_switch` (cancellable) → `session_shutdown` → dispose →
  fresh createRuntime → `session_start` reason "resume" with
  `previousSessionFile` (agent-session-runtime.ts:193-221). Stale contexts
  throw by design (runner.ts:539-552).
- Persistence only via explicit session-JSONL entries: `appendEntry`
  (LLM-invisible) / `sendMessage` (LLM-visible, `display:false` hides from
  UI). Session file is append-only JSONL, tree-structured via `parentId`.
- Steering is first-class: `sendUserMessage`/`sendMessage` with `deliverAs`
  `steer`|`followUp`|`nextTurn` injects into a live streaming turn
  (types.ts:1267-1280); `isIdle()` exists (types.ts:319); full event list
  includes `agent_start`/`agent_end`/`turn_start`/`turn_end`/
  `tool_execution_*`/`session_*`.
- Extensions are full-trust in-process Node/Bun code — no sandbox,
  unrestricted net/fs/exec. Dialing attn's unix socket is trivial.
- Handler exceptions are contained (a throwing `tool_call` handler fails that
  one tool call, not the session), though `tool_call`'s containment lives one
  layer above the runner unlike other events.
- Crash asymmetry: SIGTERM/SIGHUP/clean quit fire `session_shutdown` reason
  "quit" before exit (interactive-mode.ts:3487-3594, pinned by regression
  test 5080); uncaught exception exits with NO `session_shutdown`
  (uncaughtCrash, interactive-mode.ts:3548); SIGKILL likewise. PTY exit is the
  authoritative liveness signal.

## Blindspot 2 — release cadence and pinning

- 303 tags / ~247 GitHub releases since 2025-08; peak 86/month (Dec 2025), now
  ~1-2/week; latest v0.80.10 (2026-07-16). Rolling release, 0.x, no LTS, no
  1.0.
- All 5 packages (`@earendil-works/pi-{agent-core,ai,coding-agent,
  orchestrator,tui}`) version-locked; extension API types live in
  pi-coding-agent (`src/core/extensions/types.ts`, ~1700 lines, 37 commits in
  3 months).
- Breaking changes every ~2-4 releases, maintainer-curated under "### Breaking
  Changes" with migration prose (42 sections in coding-agent CHANGELOG). Ad
  hoc compat shims soften some breaks (legacy npm-scope aliases, deprecated-
  field projections) — best-effort, not a guarantee.
- No extension compat mechanism whatsoever: no manifest version, no
  load-time check. Pin exact version; gate upgrades on reading the
  changelog; suite self-checks pi version at `session_start` and declares
  degraded state to attn on mismatch.

## Blindspot 3 — TUI under attn PTY

- attn contract recap with citations: worker is the single terminal-query
  responder (CPR from live vt10x cursor, DA1 static `ESC[?1;2c`, OSC
  10/11/12 per-occurrence in ask order from daemon-pushed theme) —
  internal/pty/session.go:730-935; `pty_resize` authoritative, replay
  provisional; #537 replay-geometry race bound + #541 bottom-clip watchdog
  are the invariants a hosted TUI must tolerate.
- pi: custom TUI (packages/tui), no alt-screen anywhere (zero mode-1049
  uses), differential renderer, every frame wrapped in DEC 2026 (tui.ts:
  1286+), full clear+redraw on width/height change, self-fires SIGWINCH after
  enabling raw mode (terminal.ts:153-156).
- Queries: only OSC 11 once (tui.ts:1684) + kitty negotiation
  `ESC[>7u ESC[?u ESC[c` (terminal.ts:17,225); attn's DA1 matcher
  (session.go:872) answers the sentinel → pi falls back to modifyOtherKeys
  deterministically. pi never sends CPR/OSC10/OSC12.
- Open (needs the phase-2 live smoke run): resize races under a real attn
  PTY; Kitty graphics images through vt10x snapshot/replay; behavior of the
  unanswered `ESC[?u` in practice.

## Driver pattern (opencode walkthrough)

- `attn-plugin.toml` with `attn_api_version=4` hard gate
  (internal/plugins/plugins.go:83); `driver.register` with fixed capability
  vocabulary (internal/daemon/plugin_driver.go:165-186), response returns
  `active_runs` for crash/restart reconciliation; `driver.spawn`/`resume`
  return argv+env+cwd that the daemon launches in the attn-owned PTY
  (plugin_driver.go:478-505); reports (state/stop/metadata) carry
  `run_id`+`seq`, ownership-checked, flow through `applyState`; metadata is
  the resume token; `driver.session_closed` on end.
- pi simplifications vs opencode: no side-channel server/monitor/classifier
  needed for rock 1; identity minted at spawn via `--session-id` (args.ts:108)
  instead of opencode's SSE `session.created` dance; likely no launcher
  script (no port/password bootstrap).

## Decisions taken (Victor, 2026-07-19)

- Rock 1 = pure driver, dumb state. No screen-scraping detector, no state
  stub; declared state is rock 2.
- Live pi-under-attn smoke run deferred to phase 2 as its opening spike.

## Open questions

- pi flag mapping for yolo / model+effort pins / initial prompt.
- Exact on-disk layout `pi install local:` expects at extension-discovery
  time (package-manager.ts resolve path not traced).
- Daemon/UI behavior for a driver registered without `state_reporting` —
  what the session shows day-to-day.
