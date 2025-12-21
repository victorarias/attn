# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build Commands

```bash
make build          # Build binary to ./attn
make install        # Build and install to ~/.local/bin/attn (kills daemon)
make test           # Run Go tests
make test-frontend  # Run frontend tests (vitest)
make test-all       # Run Go + frontend tests
go test ./internal/store -run TestList  # Run single test
```

**Rule:** Always use `make install` after any code change. The daemon is auto-killed and restarted by the app.

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

1. **Wrapper** (`cmd/attn/main.go`): CLI entry point that wraps `claude` command. Parses attn-specific flags (`-s`), passes unknown flags through to claude. Registers session with daemon, writes temporary hooks config, executes claude with `--settings` pointing to hooks.

2. **Hooks** (`internal/hooks`): Generates Claude Code hooks JSON that reports state changes back to daemon via unix socket using `nc`. Three hooks: Stop (waiting), UserPromptSubmit (working), PostToolUse/TodoWrite (update todos).

3. **Daemon** (`internal/daemon`): Background process listening on `~/.attn.sock`. Handles register/unregister/state/todos/query/heartbeat commands. Auto-started by app if not running.

4. **Store** (`internal/store`): Thread-safe in-memory session storage with mutex protection.

5. **Client** (`internal/client`): Sends JSON messages to daemon over unix socket.

6. **Protocol** (`internal/protocol`): Message types and parsing. Three states: "working" (actively generating), "waiting_input" (needs user attention), and "idle" (completed task).

7. **Classifier** (`internal/classifier`): Uses `claude -p` to classify Claude's final message and determine if it's waiting for input or idle. Called via Stop hook when Claude stops generating.

8. **Transcript Parser** (`internal/transcript`): Parses Claude Code JSONL transcripts to extract the last assistant message for classification.

### GitHub PR Monitoring

The daemon polls GitHub every 90 seconds for PRs that need attention (using `gh` CLI):
- PRs where you're a requested reviewer
- Your PRs with review comments, CI failures, or merge conflicts

The store (`internal/store`) tracks both sessions and PRs with mute states. PR actions (approve, merge) are handled via WebSocket commands to the daemon.

### Protocol Versioning

**Rule:** When changing the protocol (adding/modifying commands, events, or message structures), increment `ProtocolVersion` in `internal/protocol/constants.go`.

The app checks protocol version on WebSocket connect and shows an error banner if mismatched. This prevents silent failures when the daemon is running old code.

**After protocol changes:**
1. Increment `ProtocolVersion` in `internal/protocol/constants.go`
2. Run `make install` (kills daemon automatically)
3. The app will auto-start a new daemon with updated code

**Why:** The daemon runs as a background process and survives `make install`. Without version checking, old daemon + new app = mysterious failures with no logs.

### Adding New Protocol Types (TypeSpec Workflow)

Types are defined once in TypeSpec and generated for Go and TypeScript. **Never hand-edit generated files.**

**Source of truth:** `internal/protocol/schema/main.tsp`

**Generated files:**
- `internal/protocol/generated.go` (Go structs)
- `app/src/types/generated.ts` (TypeScript interfaces)

**Prerequisites (first time only):**
```bash
cd internal/protocol/schema && pnpm install
go install github.com/atombender/go-jsonschema/cmd/go-jsonschema@latest
npm install -g quicktype
```

**Workflow for adding a new command/event:**

1. **Define types in TypeSpec** (`internal/protocol/schema/main.tsp`):
   ```tsp
   // Command message (client → daemon)
   model GetRecentLocationsMessage {
     cmd: "get_recent_locations";
     limit?: int32;
   }

   // Result event (daemon → client)
   model RecentLocationsResultMessage {
     event: "recent_locations_result";
     locations: RecentLocation[];
     success: boolean;
     error?: string;
   }
   ```

2. **Generate types:**
   ```bash
   make generate-types
   ```

3. **Add command constant** (`internal/protocol/constants.go`):
   ```go
   const CmdGetRecentLocations = "get_recent_locations"
   const EventRecentLocationsResult = "recent_locations_result"
   ```

4. **Add parse case** in `ParseMessage()` (`internal/protocol/constants.go`):
   ```go
   case CmdGetRecentLocations:
       var msg GetRecentLocationsMessage
       if err := json.Unmarshal(data, &msg); err != nil {
           return "", nil, err
       }
       return peek.Cmd, &msg, nil
   ```

5. **Increment protocol version** if breaking change (`internal/protocol/constants.go`)

6. **Run `make install`** to rebuild and restart daemon

**CI check:** `make check-types` verifies generated files match the schema.

### Communication

All IPC uses JSON over unix socket at `~/.attn.sock`. Messages have a `cmd` field to identify type. Hooks use shell commands with `nc` to send state updates. WebSocket at `ws://localhost:21152` for real-time updates to the Tauri app.

### Async WebSocket Pattern (REQUIRED for any async operation)

When the frontend triggers an async operation via WebSocket that expects a response:

**DO NOT** fire-and-forget with optimistic UI or silent timeouts.

**DO** implement the request/result event pattern:

1. **Daemon side** (`internal/daemon/websocket.go`):
   - Handle the command (e.g., `refresh_prs`)
   - Send a `*_result` event when complete (e.g., `refresh_prs_result`)
   - Include `success: bool` and `error?: string` in the result

2. **Protocol** (`internal/protocol/types.go`):
   - Define the result event constant (e.g., `EventRefreshPRsResult`)
   - Define the result message struct with success/error fields

3. **Frontend hook** (`app/src/hooks/useDaemonSocket.ts`):
   - Return a `Promise` from the send function
   - Store pending request in `pendingActionsRef` Map with unique key
   - Listen for result event, resolve/reject the Promise
   - Set timeout (e.g., 30s) that rejects with error

4. **Frontend UI**:
   - Show loading state while Promise is pending
   - Show error toast/message if Promise rejects
   - Clear loading state on resolve or reject

**Example**: See `sendPRAction` in `useDaemonSocket.ts` for the canonical implementation.

**Why**: Silent failures frustrate users. Every async operation must have clear success/failure feedback.

## Tauri App Development

### Running the App

```bash
cd app
pnpm run dev:all    # Starts tauri dev with hot reload
```

### PTY Architecture

The app uses native Rust PTY handling via `portable-pty` (`src-tauri/src/pty_manager.rs`):
- Direct PTY management in Rust, no separate process
- Event-driven streaming to frontend via Tauri events
- Handles UTF-8 boundary splits for proper terminal rendering

Communication flow:
```
Frontend (React) → Tauri Commands → Rust pty_manager → portable-pty
```

### Frontend Architecture

Key app components:
- **App.tsx**: Main layout with sidebar and dashboard
- **Sidebar.tsx**: Session/PR list with state indicators (green=working, orange=waiting_input, gray=idle)
- **Dashboard.tsx**: Central area with terminal tabs
- **Terminal.tsx**: xterm.js integration with PTY bridge
- **AttentionDrawer.tsx**: Quick view of items needing attention

State management:
- **store/daemonSessions.ts**: Zustand store for session/PR state from daemon
- **store/sessions.ts**: Local terminal session management
- **hooks/useDaemonSocket.ts**: WebSocket connection to daemon

### Terminal Component (xterm.js)

When modifying `app/src/components/Terminal.tsx`:

1. **Wait for container dimensions** - Use ResizeObserver to wait for valid size before calling `onReady`
2. **Pre-calculate initial dimensions** - Measure font before creating XTerm to avoid 80x24 default
3. **Resize xterm first, then PTY** - Call `term.resize()` then notify PTY (sends SIGWINCH)
4. **Use VS Code's resize debouncing** - Y-axis immediate, X-axis 100ms debounced (text reflow is expensive)

### E2E Testing

```bash
cd app
pnpm run e2e               # Run all E2E tests
pnpm run e2e:headed        # Run with browser visible
pnpm run e2e -- --ui       # Run with Playwright UI
```

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

Ask yourself: "Am I fixing the problem or avoiding it?"
