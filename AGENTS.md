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
make test-e2e       # Run Playwright E2E tests (browser, mock PTY)
make test-harness   # Run Go + frontend + E2E tests
make test-all       # Run Go + frontend tests
go test ./internal/store -run TestList  # Run single test
```

**Dev workflow:** Use `make install` for daemon changes (fast iteration). Use `make install-app` when you need to test the full packaged app.

## Testing Principles

- Do not add tests that only re-check compile-time guarantees (types, lint-only checks, build-only coverage).
- Do not copy production code into tests; tests should exercise behavior, not mirror implementation.

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

## Logging

### Daemon (Go)

The daemon uses a custom logger that writes to `~/.attn/daemon.log`.

**In daemon package:** Use `d.logf(format, args...)`:
```go
func (d *Daemon) someHandler() {
    d.logf("Processing request: %s", requestID)
}
```

**In sub-packages (e.g., reviewer):** Pass a `LogFunc` from the daemon:
```go
// In reviewer package - define LogFunc type
type LogFunc func(format string, args ...interface{})

// Add WithLogger method
func (r *Reviewer) WithLogger(logf LogFunc) *Reviewer {
    r.logf = logf
    return r
}

// Use r.log() internally (checks if logger is set)
func (r *Reviewer) log(format string, args ...interface{}) {
    if r.logf != nil {
        r.logf(format, args...)
    }
}

// In daemon - wire it up
reviewer.New(d.store).WithLogger(d.logf)
```

**DO NOT** use `log.Printf()` - it goes to stderr which is `/dev/null` when daemon runs in background.

### Frontend (TypeScript)

Use `console.log/warn/error` with a prefix for easy filtering:
```typescript
console.log('[DaemonSocket] Connected');
console.error('[ReviewPanel] Failed to load diff:', error);
```

View in browser DevTools (Tauri: right-click → Inspect).

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

The daemon polls GitHub every 90 seconds for PRs that need attention (using `gh` CLI v2.81.0+):
- PRs where you're a requested reviewer
- Your PRs with review comments, CI failures, or merge conflicts

Multi-host is supported via `gh auth status --json hosts` and per-host clients.

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

### 6. Review Comments Have Multiple Consumers

When modifying the `ReviewComment` data model (adding fields, changing behavior), update ALL consumers:

1. **TypeSpec schema** (`internal/protocol/schema/main.tsp`) - source of truth for types
2. **Store** (`internal/store/review.go`) - database schema and queries
3. **Daemon handlers** (`internal/daemon/websocket.go`) - WebSocket commands
4. **Frontend hooks** (`app/src/hooks/useDaemonSocket.ts`) - client-side API
5. **Frontend UI** - components that display/edit comments
6. **Reviewer agent MCP tools** (`internal/reviewer/mcp/tools.go`) - `CommentInfo` struct and `ListComments()`
7. **Reviewer filtering** (`internal/reviewer/reviewer.go`) - logic that filters comments for re-reviews

**Easy to miss:** The reviewer agent reads comments through its own MCP tools, not the WebSocket protocol. When adding fields like `wont_fix`, you must update both the `CommentInfo` struct in `mcp/tools.go` AND any filtering logic (e.g., excluding won't-fix comments from "needs attention" lists).

### 7. Terminal Focus Ownership (Main vs Utility)

When switching sessions, do not blindly refocus the main session terminal. If the selected session has an open utility terminal tab, keep focus in the utility terminal.

Why this matters:
- `Cmd+T` and utility workflows depend on keyboard input reaching the utility PTY.
- A delayed `mainTerminal.focus()` can silently steal focus after utility terminal mount.
- Symptom: blinking cursor in utility panel, but typed characters are invisible or go to the wrong PTY.

Implementation rule:
- In `app/src/App.tsx`, session selection should `fit()` main terminal but only `focus()` it when utility panel is not open/active for that session.
- In `app/src/components/UtilityTerminalPanel/index.tsx`, prefer focusing the concrete xterm instance (`xterm.focus()`) before fallback handle focus, with retry.

Verification:
- Run `app/e2e/utility-terminal-realpty.spec.ts` with `VITE_MOCK_PTY=0 VITE_FORCE_REAL_PTY=1`.
- Required coverage:
  - `Cmd+T` terminal accepts typing without extra click.
  - Switch to another session and back; utility terminal still accepts typing without extra click.

## Communication

All IPC uses JSON over unix socket at `~/.attn/attn.sock`. Messages have a `cmd` field to identify type. Hooks use shell commands with `nc` to send state updates. WebSocket at `ws://localhost:9849` for real-time updates to the Tauri app.

**WebSocket buffer limit:** Each client has 256-message buffer. If frontend sends commands faster than daemon processes them, messages can be dropped. Slow clients get disconnected after 3 failed sends.

## Tauri App Development

Frontend documentation is in `app/CLAUDE.md`. Key commands:

```bash
cd app
pnpm run dev            # Dev mode with hot reload
pnpm test               # Unit tests (vitest)
pnpm run e2e            # E2E tests (playwright)
```

## Changelog

Maintain `CHANGELOG.md` at the project root. Uses timestamps (not versions) since this is a personal project.

**When to update:**
- After completing a feature or significant change
- When making commits (include in same commit)
- After fixing notable bugs
- When removing or deprecating functionality

**Format:**
```markdown
## [YYYY-MM-DD]

### Added
- **Feature Name**: Brief description of what it does

### Changed
- Description of behavior changes

### Fixed
- Description of bug fixes

### Removed
- Description of removed functionality
```

**Guidelines:**
- Write for users, not developers (focus on what changed, not how)
- Group related changes under a single bullet with sub-points if needed
- Use present tense ("Add" not "Added")
- Link to relevant docs/plans if helpful

Use `TASKS.md` to see unblocked work.

## When Something Is Broken

1. **Diagnose WHY** before proposing fixes - understand the root cause
2. **Fix the root cause**, don't remove functionality to make the error go away
3. **If refactoring**, list preserved behaviors and verify each one survives
4. **Never remove user-requested functionality** without explicit approval

Ask yourself: "Am I fixing the problem or avoiding it?". You have autonomy to fix the problem or act on it immediately.
