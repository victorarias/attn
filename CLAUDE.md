# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build Commands

```bash
# Daemon only (fast, ~2s)
make build          # Build binary to ./attn
make install        # Build and install daemon to ~/.local/bin/attn (restarts daemon)

# Full app
make build-app      # Build Go daemon + Tauri app
make install-app    # Build and install .app to /Applications
make install-all    # Install both daemon and app

# Distribution
make dist           # Create DMG at app/src-tauri/target/release/bundle/dmg/

# Testing
make test           # Run Go tests
make test-frontend  # Run frontend tests (vitest)
make test-all       # Run Go + frontend tests
go test ./internal/store -run TestList  # Run single test
```

**Dev workflow:** Use `make install` for daemon changes (fast iteration). Use `make install-app` when you need to test the full packaged app.

## CLI Usage

```bash
attn                # Open app and create session (label = directory name)
attn -s <label>     # Open app and create session with explicit label
attn daemon         # Run daemon in foreground
attn status         # Output for tmux status bar
attn list           # List all sessions (JSON)
```

## Debugging

Set `DEBUG=debug` or `DEBUG=trace` for verbose logging:
```bash
DEBUG=debug attn -s test
```

**Daemon logs:** `~/.attn/daemon.log`

## Architecture

Attention Manager (`attn`) tracks multiple Claude Code sessions and surfaces which ones need attention via an interactive dashboard.

### Core Flow

1. **Wrapper** (`cmd/attn/main.go`): CLI entry point that wraps `claude` command. Parses attn-specific flags (`-s`, `--resume`, `--fork`), passes unknown flags through to claude. Registers session with daemon, writes temporary hooks config, executes claude with `--settings` pointing to hooks. Handles signal forwarding (SIGTERM allows claude cleanup hooks to run).

2. **Hooks** (`internal/hooks`): Generates Claude Code hooks JSON that reports state changes back to daemon via unix socket using `nc`. Key hooks:
   - `UserPromptSubmit` → state: working
   - `AskUserQuestion` (PreToolUse) → state: waiting_input
   - `PermissionRequest` → state: pending_approval
   - `PostToolUse` (any) → state: working (resets from approval)
   - `Stop` → triggers classifier
   - `TodoWrite` → updates todo list

3. **Daemon** (`internal/daemon`): Background process listening on `~/.attn/attn.sock` (unix socket) and `ws://localhost:9849` (WebSocket). Handles session lifecycle, git operations, GitHub PR polling, and real-time updates to the app.

4. **Store** (`internal/store`): SQLite-backed storage with thread-safe in-memory caching. Uses RWMutex for concurrent reads. Always `defer mu.Unlock()` immediately after lock, before any I/O.

5. **Classifier** (`internal/classifier`): Uses `claude -p` with Haiku to classify Claude's final message as WAITING (needs input) or DONE (idle). Called via Stop hook when Claude stops generating.

6. **Transcript Parser** (`internal/transcript`): Parses Claude Code JSONL transcripts to extract the last assistant message. Handles both string content (old format) and `[]contentBlock` (Claude Code format).

### Git Operations

The daemon provides comprehensive git functionality via protocol commands:

- **Branch Management:** `list_branches`, `delete_branch`, `switch_branch`, `create_branch`, `list_remote_branches`, `fetch_remotes`, `get_default_branch`
- **Worktree Management:** `create_worktree`, `create_worktree_from_branch`, `list_worktrees`
- **Stash Operations:** `stash`, `stash_pop`, `check_attn_stash`, `commit_wip`, `check_dirty`
- **Git Status:** `subscribe_git_status`, `unsubscribe_git_status` (real-time polling every 5s per client)
- **File Operations:** `get_file_diff`, `get_repo_info`

Implementation in `internal/git/` (branch.go, stash.go, worktree.go) and `internal/daemon/` handlers.

### GitHub PR Monitoring

The daemon polls GitHub every 90 seconds for PRs that need attention (using `gh` CLI):
- PRs where you're a requested reviewer
- Your PRs with review comments, CI failures, or merge conflicts

PR actions (approve, merge, mute) are handled via WebSocket commands with Promise-based responses.

## Critical Patterns

### 1. Protocol Versioning (REQUIRED)

**Rule:** When changing the protocol (adding/modifying commands, events, or message structures), increment `ProtocolVersion` in `internal/protocol/constants.go`.

The app checks protocol version on WebSocket connect and **immediately closes the connection** if mismatched, showing an error banner.

**After protocol changes:**
1. Increment `ProtocolVersion` in `internal/protocol/constants.go`
2. Run `make install` (kills daemon automatically)
3. The app will auto-start a new daemon with updated code

**Why:** The daemon runs as a background process and survives `make install`. Without version checking, old daemon + new app = silent failures with no logs.

### 2. Generated Files (NEVER HAND-EDIT)

Types are defined once in TypeSpec and generated for Go and TypeScript.

**Source of truth:** `internal/protocol/schema/main.tsp`

**Generated files (DO NOT EDIT):**
- `internal/protocol/generated.go`
- `app/src/types/generated.ts`

**Workflow for adding a new command/event:**

1. Define types in TypeSpec (`internal/protocol/schema/main.tsp`)
2. Run `make generate-types`
3. Add command constant in `internal/protocol/constants.go`
4. Add parse case in `ParseMessage()` in `internal/protocol/constants.go`
5. Increment protocol version if breaking change
6. Run `make install`

**CI check:** `make check-types` verifies generated files match the schema.

### 3. Async WebSocket Pattern (REQUIRED for operations that can fail)

**DO NOT** fire-and-forget with optimistic UI for operations that can fail.

**DO** implement the request/result event pattern:

1. **Daemon side** (`internal/daemon/websocket.go`):
   - Handle the command
   - Send a `*_result` event when complete
   - Include `success: bool` and `error?: string` in the result

2. **Protocol** (`internal/protocol/constants.go`):
   - Define result event constant
   - Define result message struct with success/error fields

3. **Frontend hook** (`app/src/hooks/useDaemonSocket.ts`):
   - Return a `Promise` from the send function
   - Store pending request in `pendingActionsRef` Map with unique key
   - Listen for result event, resolve/reject the Promise
   - Set timeout (30s typical)

**Example**: See `sendPRAction` in `useDaemonSocket.ts` for the canonical implementation.

**Fire-and-forget OK for**: Simple toggles that rarely fail (`sendMutePR`, `sendMuteRepo`)

### 4. Classifier Timestamp Protection

The classifier runs asynchronously and can take 30+ seconds. To prevent stale results from overwriting newer state:

- Capture timestamp BEFORE classification starts
- Use `UpdateStateWithTimestamp()` instead of `UpdateState()`
- Only updates if classification timestamp is newer than current `StateUpdatedAt`

See `internal/daemon/daemon.go` around line 460.

### 5. Type Conversion Helpers

Store uses pointers `[]*Session`, but API returns value slices `[]Session`. Use helpers in `internal/protocol/helpers.go`:

- `Ptr[T](v T) *T` - convert value to pointer
- `Deref[T](p *T) T` - convert pointer to value (returns zero if nil)
- `SessionsToValues()`, `PRsToValues()` - batch conversions

## Communication

All IPC uses JSON over unix socket at `~/.attn/attn.sock`. Messages have a `cmd` field to identify type. Hooks use shell commands with `nc` to send state updates. WebSocket at `ws://localhost:9849` for real-time updates to the Tauri app.

**WebSocket buffer limit:** Each client has 256-message buffer. If frontend sends commands faster than daemon processes them, messages can be dropped. Slow clients get disconnected after 3 failed sends.

## Tauri App Development

### Running the App

```bash
cd app
pnpm run dev    # Starts tauri dev with hot reload
```

### Frontend Architecture

Key components:
- **App.tsx**: Main layout, state orchestration
- **Sidebar.tsx**: Session/PR list with state indicators
- **Dashboard.tsx**: Terminal tabs and main content area
- **Terminal.tsx**: xterm.js integration with PTY bridge
- **LocationPicker.tsx**: Path selection with filesystem suggestions
- **NewSessionDialog/**: Session creation (PathInput, RepoOptions subcomponents)
- **ChangesPanel.tsx**: Git changes display
- **DiffOverlay.tsx**: Monaco-based diff viewing
- **BranchPicker.tsx**: Branch selection UI
- **AttentionDrawer.tsx**: Quick view of items needing attention

State management:
- **store/daemonSessions.ts**: Zustand store for session/PR state from daemon
- **store/sessions.ts**: Local terminal session management
- **hooks/useDaemonSocket.ts**: WebSocket connection with circuit breaker (3 reconnects → 2 daemon restarts → circuit opens 30s)

### Terminal Component (xterm.js)

When modifying `app/src/components/Terminal.tsx`:

1. **Wait for container dimensions** - Use ResizeObserver to wait for valid size before calling `onReady`
2. **Pre-calculate initial dimensions** - Measure font before creating XTerm to avoid 80x24 default
3. **Resize xterm first, then PTY** - Call `term.resize()` then notify PTY (sends SIGWINCH)
4. **Use VS Code's resize debouncing** - Y-axis immediate, X-axis 100ms debounced (text reflow is expensive)

### PTY Architecture

Native Rust PTY handling via `portable-pty` (`src-tauri/src/pty_manager.rs`):
- Direct PTY management in Rust, no separate process
- Event-driven streaming to frontend via Tauri events
- Handles UTF-8 boundary splits for proper terminal rendering

### E2E Testing

```bash
cd app
pnpm run e2e               # Run all E2E tests
pnpm run e2e:headed        # Run with browser visible
pnpm run e2e -- --ui       # Run with Playwright UI
```

## Known Gotchas

1. **Worktree action key collision**: `sendCreateWorktree()` and `sendCreateWorktreeFromBranch()` use the same pending action key. Don't call both simultaneously.

2. **Timeout vs completion race**: Async operations timeout after 30s. If daemon responds after timeout, the operation completed but UI shows error.

3. **Git status subscription**: Only 1 subscription per client. New subscription replaces old one.

4. **Circuit breaker auto-reset**: Opens after failed reconnects, auto-resets after 30s even without user action.

## Task Tracking

Use `bd` (beads) for tracking work items, not inline markdown TODOs or TodoWrite.

- **Epics:** Use for any change requiring 3+ tasks
- **Plan references:** Link tasks to their plan section in `--description` (e.g., `docs/plans/foo.md#section`)
- **Dependencies:** Use `bd dep add` when order matters
- **Discovery:** Add tasks as work progresses - don't plan everything upfront

Use `bd ready` to see unblocked work.

## When Something Is Broken

1. **Diagnose WHY** before proposing fixes - understand the root cause
2. **Fix the root cause**, don't remove functionality to make the error go away
3. **If refactoring**, list preserved behaviors and verify each one survives
4. **Never remove user-requested functionality** without explicit approval

Ask yourself: "Am I fixing the problem or avoiding it?". You have autonomy to make beads to fix the problem or act on it immediately.
