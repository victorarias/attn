# Changelog

All notable changes to this project are documented in this file.

Format: `[YYYY-MM-DD]` entries with categories: Added, Changed, Fixed, Removed.

---

## [2026-02-16]

### Fixed
- **Codex Mid-Turn Idle Regression**: Codex transcript watching now uses turn/tool lifecycle events (`task_started`, `task_complete`, `turn_aborted`, tool call start/complete) to keep active turns in `working` and defer stop-time classification until turn-close quiet windows.
- **Codex No-Output Turn Handling**: Turns that end without assistant output now resolve to `waiting_input` instead of lingering in a stale running state.
- **Codex Watcher Bootstrap Gap**: Codex transcript watchers now bootstrap from a recent transcript tail instead of attaching strictly at EOF, so restored/reopened sessions can still classify to `idle`/`waiting_input` when no new assistant lines arrive.
- **Codex Working Animation Liveness**: PTY detector now treats ANSI carriage-return animation frames as `working` heartbeat pulses, and worker backend forwards throttled repeated `working` pulses so active Codex runs can recover quickly from accidental `idle` demotions.
- **Codex Pulse False Positives**: Working pulses now require explicit working-status keywords (`working`, `thinking`, `running`, `executing`) in animated redraw frames, reducing prompt-redraw misclassification as active work.
- **Codex Stop-Time Backend Selection**: Codex sessions now use the Codex CLI classifier path (instead of Claude SDK), matching the agent/runtime used by the session itself.
- **Codex Executable Consistency**: Codex classification now uses the same configured `codex_executable` setting as session launch (with `ATTN_CODEX_EXECUTABLE` env override still taking precedence), avoiding classifier failures when `codex` is not on `PATH`.
- **Codex JSON Mode Parsing**: Codex classifier now treats `--output-last-message` as the primary verdict source and falls back to JSONL `item.completed` parsing, so stderr rollout noise no longer pollutes verdict extraction.
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
