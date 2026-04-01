# Changelog

All notable changes to this project are documented in this file.

Format: `[YYYY-MM-DD]` entries with categories: Added, Changed, Fixed, Removed.

---

## [2026-04-01]

### Added
- **PTY stall diagnostics**: Add daemon and worker-side flow-control logging around websocket PTY acknowledgements, worker-stream buffering, and worker output forwarding so live terminal stalls can be traced to the exact handoff that stops delivering bytes.

### Fixed
- **Live PTY stream stalls after perf pacing**: Stop gating daemon PTY forwarding on frontend render acknowledgements and make worker-stream buffering backpressure instead of killing the stream after a short overflow timeout, so transient UI slowdowns no longer wedge a session until reload.

## [2026-03-31]

### Changed
- **PTY output backpressure**: Pace live terminal output with xterm write callbacks and websocket acknowledgements so the daemon stops flooding visible terminals ahead of what the frontend has actually rendered.
- **Terminal debug overlay cleanup**: Remove the coalescing-specific debug toggles from the terminal size badge after the experiment proved unstable in the visible terminal path.
- **Perf harness baseline**: Remove the old runtime coalescing toggle from the packaged-app perf harness so the baseline measurements reflect the restored direct terminal write path.
- **Terminal rendering structure**: Move terminal sizing and theme helpers out of the main terminal component and clean up a few small rendering-path footguns without changing scroll-pin behavior.
- **Scroll-pin structure**: Move the scroll-pin write interception, wheel tracking, and CSI suppression into a dedicated helper so the live terminal component stops owning that behavior inline and the remaining private xterm usage is easier to audit.
- **Frontend terminal stall tracing**: Add a low-volume frontend runtime log for pane binding changes, terminal mount/unmount, and throttled xterm write/render heartbeats so reload-only terminal stalls leave evidence after the page is gone.

### Removed
- **Visible-terminal coalescing experiment**: Remove the frontend terminal coalescing config and debug plumbing after it repeatedly caused rendering artifacts in both DOM and WebGL renderers.

## [2026-03-30]

### Added
- **Packaged-app UI perf harness**: Add a real-app bridge scenario that samples `attn` plus child WebKit processes for CPU and RSS while also capturing frontend terminal and diff/review perf snapshots at repeatable checkpoints.
- **PTY transport perf counters**: Track frontend websocket PTY message volume, base64 decode work, and terminal write activity so terminal profiling can distinguish transport overhead from rendering and compositor work.
- **PTY delivery A/B benchmark**: Add a packaged-app benchmark that replays terminal payloads through `json_base64`, `base64`, and raw `bytes` delivery modes so transport overhead can be measured directly before attempting a MsgPack migration.

### Changed
- **Coalesced PTY terminal writes**: Batch live pane output into short per-pane write buffers with ordered flush barriers for resets and process status lines, so xterm sees far fewer writes during bursty terminal output while remount safety keeps queued bytes replayable.
- **Backpressure-aware coalesced terminal drain**: Make coalesced terminal flushes wait for xterm's write callback between emitted chunks instead of front-loading the whole burst into xterm at once, so the experiment tests paced writes rather than blind queueing.
- **Perf harness terminal comparisons**: Record terminal command completion time plus delivered scrollback bytes/lines in the packaged-app perf harness, and add a runtime coalescing toggle so the same build can compare coalesced and uncoalesced terminal behavior.
- **Terminal debug overlay controls**: Add live renderer and write-coalescing toggles beside the terminal sizing badge so source installs can isolate xterm/WebGL artifacts without swapping builds.
- **Source `install-all` automation default**: Make `make install-all` install the source app with the local automation bridge enabled by default, while Homebrew and release builds stay unchanged.
- **Coalescing debug controls**: Add a `Strict barriers` terminal debug toggle plus coalescing flush telemetry to help isolate ordering bugs where batched writes might interact badly with resets, exits, or remounts.
- **Coalesced repaint experiment**: Add a `Refresh after flush` terminal debug toggle so coalesced writes can force an explicit xterm repaint after parsing, which helps test whether the remaining artifacts are render invalidation rather than buffer corruption.
- **Chunked coalesced writes**: Add a `Chunked flushes` terminal debug toggle so one coalesced batch can be emitted as smaller terminal writes, helping isolate whether the artifacts are caused by large single-write bursts.

### Removed
- **Unused Monaco frontend dependencies**: Remove stale Monaco editor packages from the Tauri app after the review UI fully moved to the CodeMirror-based unified diff editor.

### Fixed
- **Frontend architecture docs**: Update the app component map so diff/review docs point to `DiffDetailPanel` and `UnifiedDiffEditor` instead of the old Monaco-era overlay.

## [2026-03-16]

### Fixed
- **`attn` in PATH for cask-only installs**: Prepend the wrapper binary's directory to PATH when spawning agent sessions so Claude Code skills can find `attn` as a bare command even when installed only via the Homebrew cask.

## [2026-03-15]

### Removed
- **Ad-hoc code signing**: Remove `codesign -s -` from `make install` and always strip the bundled sidecar signature in app builds. Quarantine removal (`xattr -d`) is still applied.

## [2026-03-14]

### Changed
- **Pane Zoom Mode**: Add `Cmd+Shift+Z` as a transient workspace zoom that expands the active pane across nested splits without hiding the surrounding panes, allows you to arm zoom before a split exists, retargets automatically when you move focus to another pane, and lights up the sidebar shortcut hint while zoom is active.
- **PR Review Provider**: Switch the advisory Hodor GitHub workflow from Vertex Gemini to OpenRouter, now targeting `qwen/qwen3-coder-next` with the `open_router_api_key` secret.
- **Sidebar Dock Shortcut Hints**: Show the split-pane and pane-navigation shortcuts (`Cmd+D`, `Cmd+Shift+D`, `Cmd+Alt+Arrow`) in the left sidebar’s Dock footer so the new session workspace controls are visible where you browse sessions.
- **Hodor Review Runtime Budget**: Install `pnpm` and the app dependencies before Hodor runs, remove verbose logging, and lower review reasoning effort to medium so PR reviews spend fewer turns on missing-tooling dead ends.

### Fixed
- **Worker Binary Override Semantics**: Keep `ATTN_PTY_WORKER_BINARY` authoritative when explicitly configured, so a missing override now fails closed instead of silently spawning some other `attn` binary from fallback search paths.
- **Worker Binary Re-Resolution Safety**: Cache implicitly discovered worker binary paths behind a dedicated lock and keep the recovery path for daemon-owned installs covered by a unit test.
- **Claude Repaint After Closing Splits**: Closing the active split pane now re-fits the surviving session terminal after the workspace collapses, so the main Claude pane redraws immediately instead of staying visually stale until a window resize or focus-mode change.

## [2026-03-10]

### Changed
- **GitHub PR Review Automation**: Replaced the `victorarias/shitty-reviewing-agent` PR review workflow with Hodor on Vertex AI using `google-vertex/gemini-3-flash-preview`, while keeping the workflow advisory and fork-safe.

### Added
- **Hodor Review Guidance**: Added a repository-specific Hodor skill and maintainer docs for the PR review workflow, including the local patch required for Google/Vertex model parsing in upstream Hodor `v0.3.4`.
- **Real App Smoke Harness**: Add a packaged-macOS automation harness that launches `/Applications/attn.app`, creates a real session through the deep-link path, splits panes with `Cmd+D`, types into the utility shell, and verifies PTY scrollback through the production daemon websocket.
- **Older-Pane Writability Repro Harness**: Add a second packaged-app automation scenario that creates two split utility panes, refocuses the older pane with a real window click, and checks whether it still accepts shell input after the newer pane exists.
- **Dev-Only UI Automation Bridge**: Add a build-gated localhost automation bridge for the Tauri app so packaged-app tests can create sessions, split/focus panes, write to runtimes, and inspect workspace state without taking over the user’s keyboard and mouse.

### Changed
- **Bridge Repro Stability**: The packaged-app UI automation bridge now supports fresh app relaunches, server-side request logging, frontend responsiveness checks, pre-launch daemon cleanup of stale full-flow sessions, and diff-based shell-pane selection so the main-pane return repro fails much more consistently in the real bug region instead of drifting during bootstrap.

### Fixed
- **Main Pane Return After Split**: Returning from a split shell to the main Claude pane no longer forces a fresh PTY reattach on every remount, so typing in the main pane continues to render after the split instead of going visually dead until another reconnect.
- **Queued Pane Output on Remount**: Split panes now replay PTY data that arrived while their terminal view was temporarily unmounted, so output generated during layout switches or session hops is not silently dropped before the pane reattaches.
- **`Ctrl+W` Terminal Editing**: Terminal panes no longer treat `Ctrl+W` like the macOS close-panel shortcut, so shells and line editors can use it to delete the previous word as expected.

## [2026-03-09]

### Added
- **Persistent Split Workspaces**: Persist each session’s split-pane workspace in the daemon/store so nested layouts, active panes, pane titles, and shell-pane runtime mappings survive app relaunches and daemon recovery.

### Changed
- **Daemon-Owned Workspace Control Plane**: Move split-pane creation, close, focus, and rename authority into the daemon with dedicated workspace protocol messages and snapshot/update events, while the frontend now renders daemon snapshots instead of mutating the canonical split tree locally.
- **Attached Shell Runtime Recovery**: Reconcile recovered shell-pane PTY runtimes against persisted workspace metadata at startup, prune missing panes from saved layouts, and clean up orphaned shell runtimes that no longer belong to any workspace.
- **Spatial Pane Navigation**: `Cmd+Alt+Arrow` now moves focus by panel geometry instead of creation order, with `Up/Down` respecting stacked panes and `Left/Right` respecting side-by-side panes; when there is no pane in that direction, focus falls through to the previous or next session.
- **Unified Pane Runtime Binding**: Main session panes and split shell panes now bind xterm instances through the same workspace-level runtime binder, so remount, restore, resize, and keyboard wiring follow one terminal lifecycle path instead of separate store and UI implementations.
- **Centralized PTY Event Routing**: The session workspace UI now routes PTY output through one app-level runtime registry instead of letting every mounted workspace subscribe to the PTY event bus independently, which makes pane/runtime delivery a single explicit ownership graph.
- **Explicit Workspace vs View-State Model**: Frontend sessions now store daemon-owned workspace topology separately from the daemon’s last-active-pane hint, while live pane selection stays client-local in `App`, which makes the UI ownership boundary clearer for future remote or multi-client work.
- **Dedicated Workspace View Controller**: The client-side pane-selection and topology-reconciliation logic now lives in a dedicated frontend hook instead of being embedded inline in `App`, which gives the workspace UI a cleaner controller boundary and direct unit coverage.
- **Dedicated Workspace Debug Harness**: The workspace-specific test helpers and pane debug globals now live in their own hook instead of inside `App`, which keeps the top-level app component focused on orchestration rather than dev-only terminal diagnostics.
- **Composed Workspace Controller**: The frontend workspace layer now exposes one composed controller hook for pane selection, workspace handle registration, PTY event routing, fit/text/size access, and debug wiring, so `App` consumes the workspace system instead of owning its internals.

### Fixed
- **Main Session Focus After Split Return**: Clicking back into the main Claude/Codex pane after working in a split now reclaims keyboard focus immediately, without needing a session refresh or waiting on the daemon workspace round-trip.
- **Workspace Quick-Find and Reload Pane Context**: Session-level quick find and reload sizing now read from the active workspace pane/runtime instead of a store-owned main-terminal ref, keeping those actions aligned with split-pane focus.
- **Source Install Daemon Startup Path**: `make install` and `make install-app` now point both the local CLI and the packaged app sidecar at a stable installed daemon binary instead of a transient Tauri release artifact path that could disappear after rebuilds and leave the daemon unable to start.

## [2026-03-08]

### Changed
- **Split Session Workspace**: Let attached session terminals open directly inside the main session area as split panes beside the primary session terminal, with `Cmd+D` and `Cmd+Shift+D` creating new vertical or horizontal splits in the active session.
- **Shortcut Remap for Split Workflow**: Move dashboard navigation to `Cmd+Option+D` and the diff dock to `Cmd+Shift+G` so the split-terminal shortcuts can own the `D` key family in session view.
- **Right Dock Default Layout**: Start the session view with the compact layout by leaving the diff dock closed until you explicitly open it with `Cmd+Shift+G`.
- **Review Loop Summary Space**: Let the latest-summary card in the review-loop sidebar grow substantially taller before it starts scrolling, so longer round summaries are easier to read in place.

### Fixed
- **`install-all` Daemon Startup Race**: Stop restarting the local `~/.local/bin/attn daemon` as part of `make install-all`, so opening the packaged app no longer races between a transient local daemon and the bundled app daemon during worker-backend startup.
- **App Bundle Install Corruption**: Stop replacing `/Applications/attn.app` with a plain `cp -r` while the packaged app may still be running; `install-app` now shuts down the existing app/daemon first and uses `ditto` so the bundled `attn` sidecar is preserved in the installed app.
- **Source Install Binary Stability**: Source-based installs now symlink both `~/.local/bin/attn` and the packaged app’s `Contents/MacOS/attn` sidecar to the stable Tauri release binary, avoiding the disappearing or invalid copied binaries seen in direct install locations on this machine.
- **Worker PTY Startup Probe Flakiness**: Give worker-sidecar startup more time during daemon boot and add clearer worker startup logs so slow post-install launches are less likely to fall back to embedded mode and mark live sessions only as recoverable.
- **Diff Detail Panel Exit Animation**: Keep dock panels in the right-dock stack through their close transition, so the detailed diff/review panel now slides out smoothly on `Cmd+Shift+E` and `Esc` instead of disappearing abruptly.

## [2026-03-07]

### Changed
- **Session Sidebar Action Strip**: Move review-loop access into a new icon-based sidebar header tool row alongside editor, diff-panel, and PR-drawer controls, and let the diff panel be shown or hidden from that strip.
- **Review Loop Session Overlay**: Remove the full-width review-loop bar above the active session and keep the review-loop drawer as an app-controlled overlay with its primary actions in the drawer header.
- **Shared Sliding Side Panels**: Refactor the review-loop drawer and PR attention drawer onto one shared side-panel shell so they both anchor to the right edge, animate with the same slide-in/slide-out behavior, and stack beside the diff panel instead of overlapping it.
- **Unified Session Right Dock**: Replace the old mix of fixed layout panels, one-off drawers, and separate review view with a single dock-managed panel system for diff, review loop, PR attention, and the in-app review/editor panel.
- **Diff Detail Cleanup**: Rename the old review-oriented diff panel to a diff-detail surface, remove the legacy in-panel AI review workflow, and keep the main review loop as the only review automation path in the app UI.
- **Live Review Loop Detail Updates**: The review-loop panel now widens further, renders the latest summary as markdown, auto-opens the log while running, and updates the visible log and touched-file list incrementally during a live iteration instead of only after completion.

## [2026-03-06]

### Added
- **SDK Review Loop Data Model**: Add dedicated review-loop run, iteration, and interaction types plus SQLite tables so the SDK-based loop can persist append-only history without reusing the old session-scoped PTY loop table.
- **SDK Review Loop Execution Path**: Add daemon-side SDK review-loop orchestration with autonomous iteration, structured outcomes, `awaiting_user` pauses, and same-loop answer/resume handling.
- **Review Loop Handoff CLI Path**: Add `attn review-loop answer` plus `--handoff-file` and `ATTN_SESSION_ID` inference support for `attn review-loop start`, so the main agent can trigger loops without the old `advance` callback model.
- **Deterministic Review Loop Harness**: Add a scripted daemon-level review-loop harness with timeline capture so SDK loop scenarios can be exercised without real Claude or PTY automation.

### Changed
- **Review Loop Persistence Foundation**: Add store APIs and tests for run-oriented review-loop records, active-run lookup by source session, and same-loop question/answer interaction history to support the SDK pivot.
- **Review Loop App Contract**: Replace the PTY-era review-loop UI/socket status flow with run-oriented `running`, `awaiting_user`, `stopped`, `completed`, and `error` states, and add an in-app answer flow for blocked loops.
- **Claude attn Skill**: Update the installed Claude skill to use review-loop start and answer commands instead of the removed PTY-era `advance` instruction.

## [2026-03-05]

### Added
- **Session Review Loop**: Add daemon/store/CLI support for session-level review loops, including persisted loop state, iteration limits, explicit `attn review-loop advance` handoff, and best-effort stop via PTY `ESC`.
- **Session Review Loop Controls**: Add active-session UI for starting, stopping, inspecting, and editing review-loop iteration limits without using `ReviewPanel`.
- **Saved Review Loop Prompts**: Add prompt preset persistence in settings so custom review-loop prompts can be saved and reused from the active-session loop UI.
- **Review Loop Session Indicators**: Add loop-status badges in the session sidebar and dashboard so active/completed/stopped loops are visible without selecting the session.
- **Review Loop Planning Docs**: Add an implementation plan in `docs/plans/2026-03-05-review-loop.md` and track the work in `TASKS.md`.

### Changed
- **Protocol Update**: Extend the protocol and generated types for review-loop commands, loop update/result events, PTY input source tagging, and persisted loop state, and bump protocol version to `34`.
- **Manual User Takeover Handling**: Manual user prompt submission now stops active review loops instead of letting automation schedule another pass behind the user's back.

## [2026-02-26]

### Added
- **Agent Transcript Watcher Behavior Interface**: Add `TranscriptWatcherBehaviorProvider` in the agent driver layer so each agent can define its own transcript lifecycle parsing, activity policy, dedupe behavior, and classification guard logic.
- **Agent Daemon Policy Interfaces**: Add driver-level policy hooks for startup recovery behavior, PTY state filtering, resume-ID lifecycle, transcript classification extraction, and executable-aware classifier dispatch.

### Changed
- **Watcher Loop Separation**: Daemon transcript watcher now runs a generic loop and delegates all agent-specific decisions to driver-provided watcher behaviors, removing hardcoded Claude/Codex/Copilot branches from daemon watcher code.
- **Built-in Agent Watcher Policies**: Move Claude hook-freshness classification guard, Codex lifecycle/working heuristics, and Copilot pending-approval turn policy into `internal/agent` behavior implementations.
- **Daemon Agent Branch Removal**: Move remaining daemon agent conditionals (recoverability, PTY-state acceptance, stop-hook resume extraction, and stop-time transcript/classifier strategy) behind agent policies and helpers so daemon logic remains agent-agnostic.

## [2026-02-24]

### Fixed
- **False Idle Classification During Claude Tool Execution**: Claude sessions running long tools (e.g., nolo production queries) were falsely classified as idle because the PTY working detector only recognized 4 specific glyphs. Expanded to the full Dingbats decorative star/asterisk range (U+2722–U+274B).
- **Transcript Watcher Guard for Active Claude Sessions**: The transcript watcher no longer triggers classification when hooks confirm a Claude session is actively working or pending approval, preventing the classifier from overriding authoritative hook state during tool execution.
- **Timestamp Precision Races**: State timestamps now use RFC3339Nano (nanosecond precision) instead of RFC3339, preventing same-second races where stale classifier results could overwrite fresher hook-driven state updates.

## [2026-02-22]

### Added
- **Agent Driver Abstraction**: Add a new `internal/agent` driver layer (registry + per-agent driver files) with opt-in capabilities for hooks, transcript discovery/watching, classifier, state detector, resume, and fork support.
- **Capability Env Overrides**: Add per-agent capability toggles via environment variables (for example `ATTN_AGENT_CLAUDE_TRANSCRIPT=0`) so features can be turned on/off without code changes.
- **Generic Launch Preparation Hook**: Add optional driver pre-launch setup (`LaunchPreparer`) so agent-specific prep (like Claude resume transcript copy) is encapsulated in the agent driver.
- **Generic Settings Writer**: Add `wrapper.WriteSettingsConfig()` for writing driver-provided settings/hook files (not Claude-specific anymore).
- **Minimal Pi Driver**: Add an initial `pi` driver with transcript/hook/classifier/state-detector capabilities disabled by default, so Pi can be integrated incrementally.
- **Dynamic Agent Settings Surface**: Settings now carry per-agent availability and executable keys for all registered drivers (for example `pi_available`, `pi_executable`, or future `<agent>_available/<agent>_executable`).
- **Copilot Resume Transcript Discovery API**: Add `transcript.FindCopilotTranscriptForResume()` to expose resume-ID transcript lookup as shared transcript package functionality.

### Changed
- **Protocol Update**: Expand protocol payloads for generic executable + `pi` compatibility fields, and align app/daemon handshake on protocol version `32`.
- **Unified In-App Agent Launcher**: Replace per-agent direct launch duplication in `cmd/attn/main.go` with a shared `runAgentDirectly()` path that uses driver capabilities.
- **Agent Selection UI is No Longer Hardcoded to 3 Agents**: New-session picker and settings modal now render agent choices dynamically from availability/settings keys rather than fixed Codex/Claude/Copilot button sets.
- **Dynamic Executable Wiring from UI to Spawn**: Frontend now sends agent-specific executable overrides through a generic spawn field, enabling non-hardcoded agents (including Pi) without bespoke frontend plumbing.
- **Transcript Watch Eligibility**: Daemon transcript watcher now checks driver capabilities instead of hard-coded agent-name allowlists.
- **Transcript Discovery + Bootstrap**: Transcript watcher now resolves transcript path and bootstrap tail size strictly through agent drivers.
- **Stop-Time Classification Gate**: When transcript capability is disabled for an agent, stop-time classification now skips transcript parsing and marks the session idle instead of forcing transcript-dependent logic.
- **PTY Spawn Executable Plumbing**: Add generic `Executable` plumbing through daemon -> PTY backend -> worker runtime so selected CLI paths are passed per agent, while keeping existing agent-specific executable fields for compatibility.
- **Agent Resolution for Spawn/Register**: Daemon now preserves/accepts registered agent-driver names instead of always coercing unknown values to built-in agents.
- **Crash-Recovery Session Handling**: After daemon restart recovery, stale sessions without a live PTY are now handled by agent capability: Claude sessions are marked recoverable and can be reopened, while non-recoverable sessions are automatically reaped.
- **New-Session Resume UI Simplification**: Remove the Location Picker resume toggle and shortcut so new sessions always start with a fresh attn-managed session ID; resume behavior remains dedicated to recoverable crash-restart flows.
- **Session Reload Control**: Sidebar session rows now show a small reload button on hover (stacked below close) to restart the underlying PTY for the same session ID.

### Fixed
- **Protocol Handshake Version Drift**: Frontend WebSocket protocol constant now matches daemon protocol `32`, preventing immediate disconnects after upgrading.
- **Classifier Capability Enforcement**: Stop-time classification now honors per-agent `classifier` capability toggles and skips LLM classification when disabled.
- **Direct Launch Resume Flag Parsing**: `attn --resume --fork-session` now correctly opens resume picker mode while preserving `--fork-session` instead of mis-parsing the flag as a resume ID.
- **Dead Agent Driver Abstractions**: Remove unused transcript-handler and state-detector provider interfaces from the driver layer to reduce indirection and avoid stale integration paths.
- **Agent Isolation Cleanup**: Remove legacy transcript helper wrappers in `cmd/attn/main.go` and remove daemon-side hardcoded transcript discovery/bootstrap fallbacks so transcript behavior now comes from drivers.
- **Executable Override Injection**: PTY spawn now avoids forcing default `ATTN_*_EXECUTABLE` env vars, preserving login-shell/env-based executable selection unless an explicit override is set.
- **Todo Priority over Transcript Capability**: Stop-time state classification now evaluates pending todos before transcript-capability short-circuits, so unfinished todo lists still surface as `waiting_input`.
- **Location Picker Shortcuts**: Agent keyboard shortcuts now index only available agents.
- **Claude Session Reopen After Crash**: Opening a recoverable Claude session now re-spawns it with the same session ID, allowing Claude to resume conversation history instead of failing with a missing-PTY error.
- **Claude Recoverable Resume Path**: Recoverable Claude sessions now respawn with `--resume <session-id>` (instead of a plain same-ID spawn), matching the first-run/resume contract and reducing same-ID startup conflicts.
- **Resume-Picker Recovery ID Drift**: Hook events now sync Claude’s actual `session_id` back to the daemon (`set_session_resume_id`), persist it in session state, and reuse it during recoverable spawns so restart recovery resumes the real Claude conversation even when attn ID and Claude ID differ.
- **Claude Reopen Guardrail**: Reopening known Claude sessions now attempts resume recovery even when a stale `recoverable=false` flag slips through after daemon churn, and spawn-time ID mapping now still prefers stored `resume_session_id`.
- **Recoverable Flag Consistency**: Recoverable markers are now cleared once a live worker session is confirmed, preventing stale recovery badges.
- **Worker Probe Early-Exit Detection**: Worker spawn now detects when the sidecar process exits before becoming ready and returns an explicit early-exit error instead of waiting for a socket timeout, making PTY backend probe failures faster and easier to diagnose.
- **Reload Kill/Spawn Race**: Session reload now waits for `session_exited` before resolving kill, preventing first-click reload from attaching to a stale PTY and immediately disconnecting.
- **Sidebar Session Actions Alignment**: Reload/close action stack now stays right-aligned in session rows.
- **Location Picker Agent Shortcut Stability**: Agent ordering and keybindings are now fixed in the picker (`Claude=⌥1`, `Codex=⌥2`, `Copilot=⌥3`) instead of reordering on selection changes.
- **Repo Options Keyboard Selection Freshness**: Repo-options keyboard handlers now use up-to-date selection callbacks, so agent changes made while choosing branches/worktrees are reflected in the final session launch.

## [2026-02-21]

### Changed
- **Long-Run Session Review Gate**: Sessions that run for 5+ minutes now finish in a review-required yellow state (`needs_review_after_long_run`) instead of immediately classifying to idle. Classification resumes only after the user visualizes that session (5s stable selection, or immediate when already focused at completion).

### Fixed
- **Review Diff Light Theme Support**: Unified diff editor now follows the app’s resolved dark/light theme, including syntax highlighting, gutters, added/deleted line backgrounds, inline comment widgets, and selection popup styling in light mode.
- **Contributors**: Thanks to @dakl for the PR that delivered theme toggle and light mode improvements.

## [2026-02-19]

### Fixed
- **Claude Working-State PTY Heartbeats**: Claude sessions now emit `working` pulses from the live animated status line (`✻ ... (Xm Ys · ...)`) so green/running state stays accurate during long turns.
- **Claude Final Summary Guard**: PTY state detection now excludes terminal final summary lines (`✻ <verb> for ...`) from working animation matching, avoiding false “still running” signals at turn completion.

## [2026-02-18]

### Changed
- **Unknown State Diagnostics**: Stop-time classification now logs explicit unknown reason codes (for example `transcript_parse_error`, `classifier_error`, and `classifier_unknown_response`) so purple-state transitions can be traced from runtime evidence.
- **Classifier SDK Dependency**: Upgrade `claude-agent-sdk-go` to include first-class `rate_limit_event` parsing and avoid aborting classifier queries on that stream event.
- **Restart Recovery Default State**: Worker session reconciliation after daemon restart now defaults live-running sessions to `launching` (emoji) instead of `working`, unless runtime metadata explicitly indicates `pending_approval` or `waiting_input`.

### Fixed
- **Classifier Flow Cleanup**: Remove daemon-side retry logic that depended on brittle `rate_limit_event` error-string matching, now that SDK parsing handles the event directly.

## [2026-02-17]

### Fixed
- **Terminal Emoji Width**: Add Unicode 11 addon to xterm.js so emojis and CJK characters are correctly treated as double-width, fixing misaligned columns in status bars and context displays.
- **Login Shell Environment Capture**: PTY sessions now source `.zshrc` when capturing the login shell environment, fixing missing PATH entries (e.g. Google Cloud SDK) that are configured in `.zshrc` rather than `.zprofile`.
- **Session List Ordering Stability**: Daemon session listing now sorts by `label` with `id` as a deterministic tie-breaker, preventing same-label sessions from swapping order between refreshes.

## [2026-02-16]

### Fixed
- **Codex Mid-Turn Idle Regression**: Codex transcript watching now uses turn/tool lifecycle events (`task_started`, `task_complete`, `turn_aborted`, tool call start/complete) to keep active turns in `working` and defer stop-time classification until turn-close quiet windows.
- **Codex No-Output Turn Handling**: Turns that end without assistant output now resolve to `waiting_input` instead of lingering in a stale running state.
- **Codex Watcher Bootstrap Gap**: Codex transcript watchers now bootstrap from a recent transcript tail instead of attaching strictly at EOF, so restored/reopened sessions can still classify to `idle`/`waiting_input` when no new assistant lines arrive.
- **Codex Bootstrap No-Output Guard**: The no-assistant-output `waiting_input` heuristic now requires an observed `turn_start` in the current watcher window, preventing bootstrap-tail truncation from falsely marking long in-progress turns as waiting.
- **Codex Working Animation Liveness**: PTY detector now treats ANSI carriage-return animation frames as `working` heartbeat pulses, and worker backend forwards throttled repeated `working` pulses so active Codex runs can recover quickly from accidental `idle` demotions.
- **Codex Pulse False Positives**: Working pulses now require explicit working-status keywords (`working`, `thinking`, `running`, `executing`) in animated redraw frames, reducing prompt-redraw misclassification as active work.
- **Codex Stop-Time Backend Selection**: Codex sessions now use the Codex CLI classifier path (instead of Claude SDK), matching the agent/runtime used by the session itself.
- **Codex Executable Consistency**: Codex classification now uses the same configured `codex_executable` setting as session launch (with `ATTN_CODEX_EXECUTABLE` env override still taking precedence), avoiding classifier failures when `codex` is not on `PATH`.
- **Codex JSON Mode Parsing**: Codex classifier now treats `--output-last-message` as the primary verdict source and falls back to JSONL `item.completed` parsing, so stderr rollout noise no longer pollutes verdict extraction.
- **Codex JSONL Large-Line Parsing**: Codex classifier JSONL parsing now handles lines beyond scanner token limits, preventing missed verdict/error extraction on large event payloads.
- **Codex Model Fallback**: Codex classifier now attempts configured models in order (default: `gpt-5.3-codex-spark` then `gpt-5.3-codex`) with low reasoning effort, and falls through automatically when the first model is unavailable.
- **Temporary PTY Capture for Work→Stop Debugging**: Worker runtime now records a rolling 90-second PTY stream window (output + input + state transitions) for Codex sessions and dumps JSONL captures automatically on `working -> waiting_input|idle`, plus on exit/shutdown, under `<data_root>/workers/<daemon_instance_id>/captures/`.

## [2026-02-15]

### Fixed
- **Claude Transcript Parsing**: Removed `bufio.Scanner` token-size limitations when reading JSONL transcripts, preventing stop-time classification from erroring and sessions from flashing/sticking `unknown` due to very long lines.
- **Worker PTY Restart Survival**: Worker backend recovery now accepts legacy socket-path filenames and restores prior `socket_path_mismatch` quarantine entries when they match supported formats, improving daemon restart resilience.
- **Worker PTY Recovery Safety**: Socket-path mismatch quarantine no longer unlinks the registry-reported worker socket path, preventing accidental orphaning of live worker sessions.
- **Worker Lifecycle Monitor CPU Spike**: Lifecycle watch no longer relies on socket read deadlines; it blocks on the watch stream and stops by closing the connection, preventing immediate-timeout loops that could peg CPU.
- **Source App Daemon Selection**: Source-built apps now prefer `~/.local/bin/attn daemon` (when it is at least as new as the bundled daemon) so `make install` daemon changes take effect without rebuilding the app.
- **macOS Shortcut Regression**: `Ctrl+W` no longer closes sessions; closing is now `Cmd+W` only (so terminals keep standard delete-word behavior).

## [2026-02-11]

### Changed
- **Source Build Update Checks**: Source-installed app builds now set `source` install channel and skip GitHub release update polling/banner noise, while tagged release builds keep update notifications.
- **Session Startup State**: New sessions now start in `launching` (emoji indicator) instead of immediately showing `working` green, then transition once runtime signals arrive.
- **Classifier Turn Budget**: Claude SDK classifier now runs with `maxTurns=2` for more reliable structured verdict extraction.
- **PTY Backend Visibility in Settings**: Settings now displays the active PTY runtime mode (`External worker sidecar` vs `Embedded in daemon`) so restart-survival behavior is visible in the UI.

### Fixed
- **Release Banner Dismissal**: Added an explicit dismiss control (`×`) for the GitHub release banner and persist dismissal per release version, so a dismissed banner stays hidden until a newer release is published.
- **Worktree Close Cleanup Prompt**: Restored the delete/keep prompt when closing worktree sessions even if an old persisted "always keep" preference exists; "always keep" now applies only for the current app run.
- **Copilot Permission Prompt State**: Copilot numbered command-approval dialogs (for example, "Do you want to run this command?" with `1/2/3` choices) are now recognized as `pending_approval` instead of falling through to stale idle/gray state.
- **Copilot Transcript Pending Latch**: Transcript watcher now tracks unresolved Copilot tool calls and keeps sessions in `pending_approval` while a stalled approval-gated tool call is outstanding, then clears back to `working` on completion.
- **Copilot Mid-Turn Idle Regression**: Transcript watcher now treats `assistant.turn_start`/`assistant.turn_end` as authoritative turn boundaries and suppresses stop-time classification while a turn is open, preventing active Copilot sessions from flashing/sticking gray during ongoing tool work.
- **Copilot Pending Approval Stability**: While Copilot is in `pending_approval`, noisy PTY redraws that heuristically look like `working` no longer override state; pending now clears only when transcript evidence indicates the approval gate has resolved.
- **Copilot Long-Running Tool Stability**: Transcript-based pending promotion now only elevates non-working states (`idle`, `waiting_input`, `launching`, `unknown`), preventing long-running approved tools from being mislabeled as pending approval.
- **Claude Classifier Diagnostics**: When Claude classifier output cannot be parsed into a verdict, daemon logs now include a structured dump of returned SDK messages (or explicit empty-response marker) to diagnose false `waiting_input` fallbacks.
- **Claude Structured Result Parsing Compatibility**: Bumped `claude-agent-sdk-go` to include merged parser fixes on `main` so classifier flows can reliably consume structured/result payload fields from SDK `result` messages.
- **Unknown Classification Handling**: Added explicit `unknown` session state (purple) for transcript/classifier uncertainty or errors; removed implicit fallback to `waiting_input`.
- **Claude Stop-Time Transcript Race**: Claude classification now ignores stale assistant messages that occur before the latest user turn, preventing off-by-one misclassification when the newest assistant response has not flushed to transcript yet.
- **Claude Stop-Time Freshness Guard**: Claude classification now also enforces assistant-message recency relative to the current stop event, so a previous turn cannot be reused when the latest turn has not fully flushed to transcript.
- **Claude Turn-Scoped Classification Idempotency**: Stop-time classification now tracks and de-duplicates by Claude assistant turn UUID, preventing repeated LLM classification on the same assistant message when hooks fire faster than transcript flush.
- **Claude Concurrent Classification De-Duplication**: Added an in-flight Claude turn guard so stop-hook and transcript-watcher triggers cannot classify the same assistant turn concurrently, eliminating duplicate classifier calls for a single turn.
- **Claude Transcript Watcher**: Claude sessions now use transcript-tail quiet-window monitoring (like Codex/Copilot) as a second classification trigger, so delayed transcript flushes still converge to the correct post-turn state even if stop hooks arrive early.
- **Local Install Daemon Restart**: `make install` now always `pkill`s existing daemon processes, restarts `~/.local/bin/attn daemon`, and fails fast if the local daemon process is not detected.
- **E2E Port Cleanup on macOS**: `make test-e2e` now prefers `lsof` on Darwin when clearing stale Vite port `1421`, avoiding noisy `fuser` usage output from incompatible flags.
- **Ownership-Mismatch Worker Reclaim Safety**: Worker registry entries now include daemon owner-lease metadata (`owner_pid`, `owner_started_at`, `owner_nonce`), and recovery now reclaims ownership-mismatched workers only when the recorded owner is provably stale via authenticated worker RPC removal. Conservative quarantine-only behavior remains when ownership cannot be proven stale.

## [2026-02-10]

### Fixed
- **Terminal Cmd+Click Link Open**: Terminal hyperlinks now open directly via Tauri opener for both plain URLs and OSC 8 links, removing the xterm warning prompt and fixing links that previously failed to open after confirmation.

### Added
- **Worker PTY Sidecar Runtime (Feature Rollout)**: Add restart-survivable PTY execution by moving session runtime into per-session worker sidecars, with daemon recovery/reconnect flow and embedded-backend fallback for compatibility.
- **PTY Backend Abstraction (Phase A)**: Introduce `internal/ptybackend` with an embedded adapter so daemon PTY flows route through a backend interface instead of directly through the in-process PTY manager.
- **Persistent Daemon Instance Identity**: Daemon now creates and reuses `<data_root>/daemon-id` and includes `daemon_instance_id` in `initial_state`.
- **Recovery Barrier Scaffold**: Daemon now tracks a startup recovery barrier, defers `initial_state` until recovery completes, and returns `command_error` (`daemon_recovering`) for PTY commands during the barrier window.
- **Per-Session PTY Worker Runtime (Phase B)**: Add `attn pty-worker` and `internal/ptyworker` with JSONL RPC (`hello`, `info`, `attach`, `detach`, `input`, `resize`, `signal`, `remove`, `health`) plus atomic worker registry files.
- **Worker Backend Adapter (Phase C)**: Add daemon-side worker backend implementation (`internal/ptybackend/worker.go`) with worker spawn/attach/input/resize/kill/remove routing and registry-based recovery scan.
- **Worker Cleanup TTL Coverage**: Add worker runtime tests to verify exited-session cleanup timing when daemon attachments are absent.
- **Worker Restart-Recovery Integration Coverage**: Add an opt-in integration test that simulates backend restart and verifies recovered worker sessions remain attachable and interactive.

### Changed
- **Protocol Version**: Bump daemon/app protocol version to `28`.
- **Daemon PTY Routing**: PTY command handling (`spawn`, `attach`, `input`, `resize`, `kill`) and startup PTY session reconciliation now route through the backend seam.
- **Backend Selection Defaults (Phase E)**: Worker backend is now the default startup mode; `ATTN_PTY_BACKEND=embedded` remains available as fallback/override.
- **Worker Recovery Reconciliation**: On worker backend startup, daemon now reconciles recovered runtime sessions into store state (create missing live sessions, preserve waiting/approval states, mark missing-running sessions idle).
- **Classifier SDK Runtime**: Upgrade Claude Agent SDK dependency to `v1.0.0-beta`.

### Fixed
- **Worker Backend Selection Ordering**: Daemon instance ID is now initialized before worker backend selection, so worker backend activation is deterministic.
- **Worker Poller Exit Deadlock Risk**: Poller exit callbacks are now asynchronous, preventing re-entrant `Remove()`/`stopPoller()` deadlocks.
- **Attach Stream Deadline Handling**: Worker attach handshake now clears per-RPC connection deadlines before long-lived stream forwarding to avoid premature idle disconnects.
- **PTY Stream Cleanup on Backpressure**: PTY forwarder now closes streams when client outbound buffers overflow, preventing orphaned worker attachments.
- **Worker Stream Backpressure Deadlock**: Worker stream event publishing now handles overflow without blocking indefinitely.
- **Worker RPC Hang Risk**: Worker backend RPC calls now run with context/time bounds to avoid indefinite blocking on stalled sockets.
- **Worker Recovery Ownership Handling**: Ownership-mismatched worker registry entries are quarantined instead of left in the active registry path.
- **Worker Recovery Transient Handling**: Recovery now retries transient worker RPC failures before deferring them and surfaces partial-recovery warnings.
- **Recovery Startup Bound**: Daemon recovery scan now runs with a bounded startup timeout to avoid unbounded barrier delays.
- **Worker Session ID Path Safety**: Session IDs are validated before worker registry/socket path derivation to avoid unsafe path traversal patterns.
- **Reconnect Reattach Race**: Frontend PTY reattach now waits for `initial_state`, avoiding `attach_session` failures during recovery barrier windows.
- **Daemon Identity Reset Hygiene**: Frontend clears PTY runtime caches when `daemon_instance_id` changes to avoid stale stream replay after endpoint identity changes.
- **Daemon Identity Reattach Continuity**: Frontend now preserves the attached-session set across daemon instance changes so terminal streams reattach automatically after recovery.
- **Worker Runtime Observability**: Worker stdout/stderr is now captured to per-session logs under `<data_root>/workers/<daemon_instance_id>/log/`.
- **Worker Session Reattach Idempotency**: Re-attaching an already attached session now closes the previous stream first, preventing duplicate PTY subscriptions and repeated output delivery.
- **Reattach Failure Safety**: PTY re-attach now keeps the existing stream if replacement attach fails, avoiding transient detach/data-loss windows.
- **Recovered Session State Accuracy**: Worker reconciliation now treats recovered sessions with non-running child processes as `idle` instead of incorrectly forcing `working`.
- **Embedded Stream Close Safety**: Embedded PTY stream close/publish path is now synchronized to prevent close/send races during detach and shutdown.
- **Worker Recovery Stabilization**: Startup now performs bounded recovery retries before demoting sessions, reducing false `idle` transitions during transient worker unavailability.
- **Worker Stream Close Boundedness**: Worker stream detach now uses a short write deadline so close/shutdown paths do not hang when the peer socket is stalled.
- **Spawn Failure Worker Cleanup**: Worker backend now terminates and reaps unready worker sidecars when spawn readiness fails/timeouts, preventing orphaned worker processes.
- **Registry Socket Path Validation**: Recovery and lazy session lookup now reject/quarantine registry entries with unexpected socket paths and avoid deleting arbitrary filesystem paths from untrusted metadata.
- **Deferred Recovery Convergence**: Daemon now runs deferred recovery reconciliation after partial startup recovery so stale sessions eventually converge to accurate idle/running state.
- **Forced-Demotion Safety Check**: Session demotion now probes worker liveness signals (registry + PID + managed socket path) to avoid incorrectly idling sessions during prolonged control-plane outages.
- **Liveness Uncertainty Handling**: Ambiguous liveness probe failures now defer idle demotion instead of treating unknown as dead, reducing false idle transitions during transient worker/socket failures.
- **Recovery Demotion Cutoff**: Startup reconciliation now skips idle demotion for sessions updated after recovery began, reducing startup state flapping for freshly active sessions.
- **Recovery Deferred Reconcile Triggering**: Missing worker metadata now triggers deferred reconciliation retries, improving eventual convergence for transient info-read failures.
- **Recovery Clear Sessions Semantics**: `clear_sessions` is now blocked during startup recovery barrier to prevent worker-recovered sessions from immediately reappearing after a clear.
- **Startup Recovery Flow Decomposition**: Daemon startup now delegates PTY recovery/reconciliation into focused helpers, reducing coupling in `Start()` while preserving behavior.
- **Terminal Cmd+Click Link Open**: Terminal hyperlinks now open directly via Tauri opener for both plain URLs and OSC 8 links, removing the xterm warning prompt and fixing links that previously failed to open after confirmation.
- **Classifier WAITING/DONE Parsing**: Stop-time state classification now handles multiline/model-explanatory outputs correctly (including responses that start with `WAITING` and then add rationale), preventing false `idle` states when user input is still required.
- **Classifier Structured Output Handling**: Claude classifier requests a JSON-schema verdict (`WAITING`/`DONE`) and consumes structured/result payloads when available, with robust fallback parsing for plain-text outputs.

## [2026-02-09]

### Fixed
- **Dashboard Session Visibility**: Home dashboard now renders `pending_approval` sessions in a dedicated "Pending approval" group, so active sessions waiting on tool/permission approval are no longer hidden.

## [2026-02-08]

### Added
- **Copilot Session Agent**: Add first-class `copilot` session support across protocol, daemon PTY spawn, wrapper launch flow, and session picker/default-agent settings.
- **Copilot Executable Override**: Add `copilot_executable` setting and plumb it through frontend spawn requests, daemon validation, and PTY environment (`ATTN_COPILOT_EXECUTABLE`).
- **Copilot Transcript Parsing**: Add support for parsing Copilot `events.jsonl` (`assistant.message`) in transcript extraction.
- **No-UI Real-Agent Harness Test**: Add opt-in integration harness test that spawns and attaches real agent sessions over daemon WebSocket, streams PTY output, and prints live `session_state_changed` transitions without opening the app UI.
- **Homebrew Formula**: Added `Formula/attn.rb` so `attn` can be installed via Homebrew tap.
- **Homebrew Cask**: Added `Casks/attn.rb` so `attn.app` can be installed via `brew install --cask`.
- **Release Workflow**: Added `.github/workflows/release.yml` to build Apple Silicon macOS release artifacts on tags.
- **Release Script**: Added `scripts/release.sh` and `make release VERSION_TAG=vX.Y.Z` to automate version bump, commit/tag/push, and Homebrew formula refresh.
- **GitHub Release Checker**: App now periodically checks GitHub latest release and surfaces a non-automatic update notice in the UI.
- **Release Docs**: Added `docs/RELEASE.md` with the maintainer release runbook.
- **Agent Availability Detection**: Daemon now checks `claude`/`codex`/`copilot` availability in `PATH` (respecting executable overrides) and publishes `claude_available`, `codex_available`, and `copilot_available` in settings events.

### Changed
- **Classifier Backend**: Add Copilot CLI classifier support (`copilot -p ... --model claude-haiku-4.5`) while keeping Claude SDK classification for Claude/Codex sessions.
- **Classifier Backend Selection**: Classifier backend is now selected by session agent:
  - Claude/Codex sessions classify with Claude SDK (Haiku)
  - Copilot sessions classify with Copilot CLI (Haiku model)
- **PTY Live State Detection**: Extend PTY output state heuristics to Copilot sessions (in addition to Codex) for color/state updates during active runs.
- **Codex/Copilot Turn Completion Source**: Daemon-managed Codex/Copilot sessions now use transcript-tail quiet-window detection (instead of PTY prompt heuristics) to trigger stop-time classification during active sessions.
- **Protocol Version**: Bump daemon/app protocol version to `27`.
- **App Update UX**: Replaced one-click in-app auto-update install with a **View Release** banner that links to GitHub releases.
- **Release Artifacts**: Release workflow now uploads a stable `attn_aarch64.dmg` alias so Homebrew cask can target a fixed latest-download path.
- **Docs IA**: README now includes full install, update, and build-from-source guidance directly (self-sufficient setup instructions), while release procedure lives in `docs/RELEASE.md`.
- **Agent Picker UX**: Location picker and settings default-agent controls now disable unavailable agents and show PATH availability status; PR-open fallback now selects an available agent when the configured default is unavailable.
- **Agent Fallback Persistence**: Availability fallback now applies at runtime for session launch/open flows without silently rewriting the saved default agent setting.

### Fixed
- **Bundled Daemon Preference in App Runtime**: Desktop app startup now prefers the bundled `attn` daemon binary by default (with `ATTN_PREFER_LOCAL_DAEMON=1` opt-in for local dev), preventing stale `~/.local/bin/attn` installs from breaking cask-launched sessions.
- **Cask Runtime Wrapper Resolution**: Daemon-managed session spawn and Claude hooks now use an explicit wrapper path (`ATTN_WRAPPER_PATH`) instead of relying on `attn` being in shell `PATH`, so Homebrew cask installs work without separately installing the formula.
- **Release Artifact macOS Signature Integrity**: Release workflow now re-signs and verifies the built `attn.app`, rebuilds the DMG from the signed app, and replaces uploaded release assets from CI before publishing cask artifacts.
- **Release CI Reliability**: Release workflow now installs `pnpm` before enabling pnpm cache in `setup-node`, and supports manual `workflow_dispatch` retries for existing tags so failed tagged runs can be rebuilt and published entirely from CI.
- **Copilot Stop Classification Path**: Add Copilot transcript discovery under `~/.copilot/session-state/*/events.jsonl` (matched by cwd + recent activity) so Copilot sessions classify on stop without hooks.
- **Copilot Resume Transcript Matching**: When launching Copilot with `--resume <session-id>`, stop-time classification now first checks `~/.copilot/session-state/<session-id>/events.jsonl` before falling back to heuristic cwd/timing discovery.
- **Copilot Classifier Safety Isolation**: Copilot classification now disables custom instructions and avoids tool auto-approval, and runs from an isolated temp cwd so classifier sessions do not contaminate cwd-based transcript matching.
- **Copilot Transcript Selection Robustness**: Copilot transcript discovery now prefers session-state candidates whose `session.start` timestamp is closest to the launched session time, with safe modtime fallback.
- **Session Indicator Reliability**: Remove stale Codex-only "unknown transcript" indicator fallback so Codex/Copilot sessions render normal color-based states in sidebar/drawer.
- **Classifier Audit Logging**: Classifier logs now include full input text and full model output text so classification decisions can be reviewed later in daemon logs.
- **PTY Live-State Stability**: Prompt remnants in recent terminal output no longer force `idle` while new assistant output is still streaming, improving Codex/Copilot working-state transitions.
- **Codex/Copilot State Source-of-Truth**: PTY-derived `waiting_input`/`idle` transitions are now ignored for Codex/Copilot sessions so final idle/waiting colors come from transcript + classifier, reducing noisy false transitions.
- **Session Restore E2E Coverage**: Session restore/reconnect Playwright assertions now validate actual sidebar session state/selection markers instead of removed `state unknown` indicators.
- **Settings Validation Feedback**: Invalid executable settings now surface an explicit UI error toast, and the client re-syncs to daemon settings after validation failure instead of leaving stale optimistic values.
- **Git Status/Review Command Chatter**: Frontend now avoids redundant git-status subscribe/unsubscribe cycles when the active session directory is unchanged, and de-duplicates in-flight branch-diff requests for the same repo directory.
- **Claude First-Turn State Detection**: Stop-time classification now retries Claude transcript reads briefly and falls back to transcript discovery by session ID when the provided path is missing/stale, preventing first-turn empty-transcript misclassification.

### Removed
- **Tauri Updater Runtime Wiring**: Removed updater/process plugin wiring and updater signing requirements from the desktop app release path.

## [2026-02-07]

### Added
- **Daemon PTY Manager**: PTY session lifecycle now lives in Go (`internal/pty`) with spawn, attach/detach, input, resize, kill, scrollback ring buffer, per-session sequence numbers, and UTF-8/ANSI-safe output chunking.
- **Codex Live State Detection in Daemon**: Ported output-based codex prompt/approval heuristics into Go PTY reader path so codex sessions update `working` / `waiting_input` / `pending_approval` without Rust PTY code.
- **Codex Visible-Frame Snapshot Restore**: Daemon now maintains a virtual terminal screen for codex sessions and includes a rendered screen snapshot in `attach_result`, so reconnect/reattach restores what was visible (including alternate-screen UIs) before live stream resumes.
- **PTY WebSocket Protocol**: Added daemon commands/events for terminal transport:
  - Commands: `spawn_session`, `attach_session`, `detach_session`, `pty_input`, `pty_resize`, `kill_session`
  - Events: `spawn_result`, `attach_result`, `pty_output`, `session_exited`, `pty_desync`
- **WebSocket Command Error Event**: Unknown/invalid WebSocket commands now return a structured `command_error` event instead of failing silently.
- **Managed Wrapper Mode**: `ATTN_DAEMON_MANAGED=1` support in the wrapper to skip daemon auto-start and register/unregister side effects when sessions are daemon-spawned.
- **Session Recovery Test Harness**: Added Playwright daemon lifecycle controls (`start/stop/restart`) plus a dedicated session restore/reconnect spec to guard app restart + daemon restart behavior.
- **Real-PTY Utility Focus Regression Coverage**: Added Playwright real-PTY checks for utility terminal keyboard input on `Cmd+T` and after switching sessions away/back.

### Changed
- **Terminal Transport Path**: Frontend terminal I/O now routes through daemon WebSocket PTY commands/events instead of Tauri PTY IPC.
- **Session Persistence Behavior**: App no longer clears daemon sessions on startup; existing daemon-managed sessions can survive UI restart and be reattached.
- **Session Agent Typing**: Protocol schema now models session agent as a strict `claude|codex` enum, with shared normalization helpers for register/spawn/store paths.
- **Restore Scrollback Depth**: Increased daemon PTY replay buffer to `8 MiB` per session and frontend terminal scrollback to `50,000` lines so restored sessions recover much deeper history.
- **PTY Restore Semantics**: Frontend now attempts `attach_session` first and only spawns when the daemon does not already know the session ID, avoiding accidental respawn of missing PTYs after daemon restarts.
- **Daemon Startup Safety**: Daemon now refuses to replace an already-running daemon instance instead of SIGTERM/SIGKILL takeover.
- **Connection Recovery**: Frontend removed daemon auto-restart on WebSocket failure; it now reconnects and surfaces a manual-retry path if daemon stays offline.
- **Upgrade Messaging**: Version-mismatch banner now includes active-session impact guidance for manual daemon restart timing.
- **Unregister Semantics**: `unregister` is now a hard-stop path (terminate process/session resources, then remove session metadata); `detach_session` remains the keep-running path.
- **Spawn Consistency**: Session registration now happens only after PTY spawn succeeds, avoiding temporary/stale session entries on spawn failures.
- **PTY Shell Startup**: PTY spawn now captures login-shell environment (`shell -l -c 'env -0'`) and reuses it for session commands, so daemon-spawned sessions better match the user's interactive shell environment.
- **WebSocket Ordering**: `pty_input` now follows the same ordered command path as other WebSocket commands.
- **Protocol Schema Coverage**: TypeSpec now explicitly models all daemon WebSocket events and reviewer streaming payloads used in runtime.
- **Protocol Version**: Bumped daemon/app protocol version to `26`.

### Fixed
- **Daemon Spawn Wrapper Path Resolution**: PTY-launched sessions now validate candidate `attn` executable paths before invoking them, preventing `fish: Unknown command` failures when a stale or missing binary path is discovered.
- **Utility Terminal Shell Bootstrap**: `Cmd+T` utility terminals now wire xterm input before replaying buffered PTY output, so early terminal capability queries receive responses and interactive login shells (like fish) initialize their prompt correctly.
- **Daemon Socket Detection (Tauri)**: Frontend daemon health/start checks now use `~/.attn/attn.sock` (and `ATTN_SOCKET_PATH` override), matching daemon defaults.
- **Stale Daemon Socket Recovery**: App startup now verifies the daemon socket is connectable (not just present), removes stale socket files, and waits for a live socket before reporting daemon startup success.
- **Persistence Degraded Visibility**: When SQLite open/migrations fail and daemon falls back to in-memory state, the app now receives a persistent warning banner that includes the DB path and points to daemon logs for recovery details.
- **Terminal Output Races**: Buffered PTY output until terminals are ready to prevent dropped initial prompt/scrollback in main and utility terminals.
- **Reconnect Attach Hygiene**: Exited/unregistered sessions are removed from frontend reattach tracking to avoid repeated failed `attach_session` attempts.
- **Exited PTY Cleanup**: Daemon now removes exited PTY sessions from the manager to prevent stale in-memory session accumulation.
- **Login Shell Exec Failures**: PTY spawn now retries with safe fallback shells (`/bin/zsh`, `/bin/bash`, `/bin/sh`) when the preferred login shell cannot be executed (e.g. macOS `operation not permitted`).
- **macOS PTY Launch EPERM**: PTY spawn no longer requests `Setpgid` on Darwin with `forkpty`, fixing `fork/exec ... operation not permitted` for all shells.
- **Daemon PTY Log Noise**: WebSocket command logging now skips high-frequency PTY traffic (`pty_input`, `pty_resize`, `attach_session`, `detach_session`), and expected PTY `session not found` races are no longer logged as errors.
- **SQLite Migration Resilience**: Migration 20 (`prs.host`) is now idempotent when the column already exists, preventing DB-open failure and in-memory fallback that caused session/PR state loss after daemon restarts.
- **Session Restore on App Reopen**: UI session store now hydrates from daemon sessions after initial state, so tracked sessions reappear after closing/reopening the app.
- **Reattach Existing PTYs**: Frontend PTY spawn path now treats `session already exists` as attachable, preventing restore sessions from failing when terminal views reconnect.
- **Session Agent Persistence**: Sessions now persist and restore `agent` (`claude`/`codex`) across daemon/app restarts, including wrapper-registered sessions and daemon-spawned sessions.
- **Session Duplication on Reconnect**: Session lists are now upserted/deduplicated by ID, and daemon re-register/spawn updates emit `session_state_changed` for existing IDs instead of duplicate `session_registered` events.
- **Stale PTY Auto-Respawn**: Restored sessions with missing PTYs no longer auto-create fresh agents with the same ID, preventing confusing "blank restored terminal" behavior and inconsistent close flows.
- **Codex Replay Robustness**: Terminal write path now uses `writeUtf8` when available and surfaces a warning when Codex replay is truncated.
- **Ghost Sessions After Daemon Restart**: Daemon startup now prunes persisted sessions that have no live PTY and surfaces a warning, preventing stale sessions from reappearing after daemon restarts.
- **Empty State Sync on Reconnect**: Frontend now treats missing `sessions/prs/repos/authors/settings` in `initial_state` as empty values, preventing stale UI data when daemon responses omit empty arrays/maps.
- **Utility Terminal Focus After Session Switch**: Selecting a session no longer forcibly steals focus back to the main terminal when that session already has an open utility tab, preventing “blinking cursor but no visible typing” regressions.
- **Utility Terminal Output Restore After Dashboard Roundtrip**: Returning from dashboard/home now re-attaches existing utility PTYs and replays scrollback into the remounted terminal view, so prior output remains visible instead of showing an empty prompt.

### Removed
- **Rust PTY Manager**: Removed `app/src-tauri/src/pty_manager.rs` and PTY Tauri command registrations (`pty_spawn`, `pty_write`, `pty_resize`, `pty_kill`).
- **Rust PTY-Only Dependencies**: Removed `portable-pty`, `base64`, and `nix` from Tauri dependencies.
- **Unused Tauri Greeting Command**: Removed unused Rust `greet` command wiring.

---

## [2026-02-05]

### Added
- **Review Panel Harness Coverage**: New Playwright harness spec validates non-blocking review loading, failed remote sync fallback, and selection persistence across background refreshes.

### Changed
- **Review Panel Remote Sync**: Opening review now shows branch diff from local refs immediately, then refreshes in the background after remote fetch completes.
- **Review Panel Sync Feedback**: Header now shows `Syncing with origin...` during background refresh and a non-blocking warning when remote sync fails.

### Fixed
- **Fork Worktree Naming**: Creating a fork worktree from inside an existing worktree now resolves to the main repo before generating branch/worktree paths, so custom names like `fun` no longer get appended to an existing generated suffix.

---

## [2026-02-02]

### Added
- **PATH Recovery for GUI App Launches**: New `pathutil` package ensures external tools like `gh` can be found when app is launched from Finder/Dock (macOS only)

---

## [2026-02-01]

### Changed
- **Location Picker Search**: Directory search now uses "contains" matching instead of "starts with", so typing "proxy" matches "metadata-proxy"
- **Location Picker Sort Order**: Directories starting with the search term appear first, followed by directories that contain it elsewhere
- **Location Picker Navigation**: Arrow key navigation now scrolls the selected item into view

---

## [2026-01-31]

### Added
- **Multi-Host GitHub Support**: Discover authenticated gh hosts and poll PRs across github.com + GHES
- **Host Badges + Connected Hosts**: Show host badges when a repo spans multiple hosts and list detected hosts in Settings
- **Mute by Author**: Hide all PRs from specific authors (e.g., dependabot, renovate)
  - 👤 button on PR rows to mute author (🤖 for bot authors)
  - Muted Authors section in Settings to view and unmute
  - Undo toast supports author mutes

### Changed
- **PR ID Format**: IDs now include host prefixes (e.g., github.com:owner/repo#123) for correct routing
- **PR Actions Routing**: Approve/merge/fetch details route by PR ID to the correct host
- **GitHub CLI Requirement**: Requires gh v2.81.0+ for host discovery

### Fixed
- **Per-Host Rate Limits**: Rate limiting is isolated per host so one host doesn't block others
- **PR Detail Refresh**: Detail refresh runs per host to avoid cross-host mixups

### Removed
- **GitHub Env Overrides**: `GITHUB_API_URL`/`GITHUB_TOKEN` configuration removed (gh discovery only)

---

## [2026-01-19]

### Added
- **PRs Panel Harness**: Playwright test harness for the dashboard PRs panel
- **PRs Harness Scenarios**: Additional test cases for PR action wiring and error flows (fetch details, missing projects dir, fetch remotes, worktree creation)
- **Default Session Agent Setting**: Configure Codex/Claude in Settings and use it for PR opens
- **Claude Default Agent**: Default to Claude when no session agent setting exists

### Fixed
- **Open PR Worktrees**: Fetch missing PR branch details on demand before creating worktrees
- **macOS PATH Recovery**: Rebuild PATH via `path_helper` for Finder-launched daemon so `gh`/`git` are available
- **Fetch Remotes Errors**: Surface underlying git error details when fetch fails
- **Projects Directory Fallback**: Resolve repos one level deeper under the projects directory when needed
- **Repo Safety Checks**: Validate git worktree status and prefer matches whose `origin` repo name matches the PR repo
- **PR Title Links**: Open PR URLs from the dashboard title click
- **PTY Mock Detection**: Use Tauri runtime detection to avoid accidental mock PTY sessions

---

## [2026-01-17]

### Added
- **Mock PTY Mode**: Optional PTY stub for tests and development when real agent terminals aren't available

### Fixed
- **Session Agent Persistence**: "New session" agent choice (Codex/Claude) now saves in daemon settings so it survives app restarts

### Changed
- **Review Mode**: Review panel now opens as a full-screen focus view with animated transition and clearer keyboard dismissal

---

## [2026-01-06]

### Added
- **Won't Fix Action**: New comment action for marking comments as "won't fix"
  - Mutually exclusive with Resolved (setting one clears the other)
  - Visual indicator with amber styling
  - Available in both Review Panel and reviewer agent
- **Markdown Support**: Comment content now renders Markdown
  - Supports code blocks, links, lists, bold/italic, blockquotes
  - Uses ReactMarkdown for saved comments, marked for CodeMirror widgets
- **Session Agent Picker**: Choose Codex or Claude when starting a new session
  - Codex is the default selection
  - Keyboard shortcuts for quick switching
- **PTY State Detection**: Infer session states from PTY output for non-hook agents (e.g. Codex)

### Changed
- **PR-like Branch Diff**: Review Panel now shows all changes vs origin/main instead of just uncommitted changes
  - File list shows all files changed on the branch (committed + uncommitted)
  - Diffs compare against base branch, not HEAD
  - After committing, panel still shows all branch work
  - Files with uncommitted changes marked with indicator
  - Auto-fetches remotes before computing diff
- **New Session Agent**: The Codex/Claude selection now persists across app restarts
- Default to Codex for in-app sessions while testing

### Fixed
- **Font Size Shortcuts**: Cmd+/- no longer loses collapsed regions or comments
  - Added fontSize to effect dependencies so decorations rebuild after editor recreation
- **Font Size Scaling**: Comment UI elements now scale with font size changes
  - Author badges, action buttons, textarea, collapsed regions all respect zoom level
- **Git Status Parsing**: Fixed bug where file paths were truncated in uncommitted changes detection

---

## [2026-01-05]

### Added
- **Reviewer Agent**: AI-powered code review using Claude Agent SDK
  - Streams tool calls in real-time as agent reviews code
  - MCP tools: `get_changed_files`, `get_diff`, `list_comments`, `add_comment`, `resolve_comment`
  - Re-review context: agent sees previous comments and their resolution status
  - "Resolved by Claude/you" badges on comments
- **Selection Actions**: Select code in diff to send to Claude or add comment
  - Popup appears on text selection with "Send to Claude" and "Add Comment" buttons
- **Clickable File References**: File paths in reviewer output are clickable
  - Supports backtick-wrapped filenames, table entries, and suffix matching
  - Clicking jumps to file diff and scrolls to relevant line
- **UI Improvements**:
  - Auto-scroll review brief as content streams in
  - Font size persists across sessions
  - Animated progress line during review
  - Centered loading spinner

### Fixed
- **Comment Interaction**: Keyboard events in comment textarea no longer trigger panel shortcuts
- **Tool Call Navigation**: Clicking add_comment tool call switches to correct file and scrolls to line
- **Cursor in Read-only Editor**: Cursor no longer appears in diff view

---

## [2026-01-04]

### Added
- **Reviewer Agent Foundation**: Phase 3 implementation
  - Walking skeleton with daemon integration
  - Mock transport for testing without real Claude API
  - Resolution tracking via MCP tools

## [2026-01-03]

### Added
- **UnifiedDiffEditor**: New diff component replacing DiffOverlay
  - Deleted lines are real document lines (not DOM injected)
  - Single comment mechanism works for all line types
  - Visual hunks mode with collapsible unchanged regions
- **Keyboard Shortcuts**: `⌘Enter` to save, `Escape` to cancel comments
- **Component Test Harness**: Playwright-based testing for CodeMirror components
  - Real browser environment for accurate DOM testing
  - Mock API for isolated component testing

### Fixed
- **Daemon Race Condition**: flock-based PID lock prevents multiple daemons
- **Scroll Position**: Preserved when saving/canceling comments
- **Editor Performance**: Eliminated flash on comment state changes
- **Deleted Line Comments**: Now appear at correct position in diff

---

## [2026-01-02]

### Added
- **Review Panel**: New full-screen diff review interface
  - File list with "NEEDS REVIEW" and "AUTO-SKIP" sections
  - CodeMirror 6 with One Dark theme for syntax highlighting
  - Unified diff view with clear red/green highlighting
  - Auto-skip detection for lockfiles (pnpm-lock.yaml, package-lock.json, etc.)
  - Hunks/Full toggle to collapse unchanged regions
  - Keyboard navigation: `j`/`k` navigate, `]` next unreviewed, `e`/`E` expand
  - Font size controls: `⌘+`/`⌘-` zoom, `⌘0` reset
  - Entry point: "Review" button in Changes panel header
- **Inline Comments**: Add comments on any line in the diff
  - Delete button for removing comments
  - Correct positioning for deleted line comments
