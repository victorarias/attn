# Changelog

All notable changes to this project are documented in this file.

Format: `[YYYY-MM-DD]` entries with categories: Added, Changed, Fixed, Removed.

---

## [2026-02-08]

### Added
- **Copilot Session Agent**: Add first-class `copilot` session support across protocol, daemon PTY spawn, wrapper launch flow, and session picker/default-agent settings.
- **Copilot Executable Override**: Add `copilot_executable` setting and plumb it through frontend spawn requests, daemon validation, and PTY environment (`ATTN_COPILOT_EXECUTABLE`).
- **Copilot Transcript Parsing**: Add support for parsing Copilot `events.jsonl` (`assistant.message`) in transcript extraction.

### Changed
- **Classifier Backend**: Add Copilot CLI classifier support (`copilot -p ... --model claude-haiku-4.5`) while keeping Claude SDK classification for Claude/Codex sessions.
- **Classifier Backend Selection**: Classifier backend is now selected by session agent:
  - Claude/Codex sessions classify with Claude SDK (Haiku)
  - Copilot sessions classify with Copilot CLI (Haiku model)
- **PTY Live State Detection**: Extend PTY output state heuristics to Copilot sessions (in addition to Codex) for color/state updates during active runs.
- **Protocol Version**: Bump daemon/app protocol version to `27`.

### Fixed
- **Copilot Stop Classification Path**: Add Copilot transcript discovery under `~/.copilot/session-state/*/events.jsonl` (matched by cwd + recent activity) so Copilot sessions classify on stop without hooks.
- **Copilot Classifier Safety Isolation**: Copilot classification now disables custom instructions and avoids tool auto-approval, and runs from an isolated temp cwd so classifier sessions do not contaminate cwd-based transcript matching.
- **Copilot Transcript Selection Robustness**: Copilot transcript discovery now prefers session-state candidates whose `session.start` timestamp is closest to the launched session time, with safe modtime fallback.

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
