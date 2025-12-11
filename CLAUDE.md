# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build Commands

```bash
make build          # Build binary to ./cm
make install        # Build and install to ~/.local/bin/cm
make build-attn     # Build experimental binary to ./attn
make install-attn   # Build and install to ~/.local/bin/attn
make test           # Run all tests
go test ./...       # Run all tests (alternative)
go test ./internal/store -run TestList  # Run single test
```

**Rule:** Always use `make install` after any code change, not just `make build`. The user runs `cm` from PATH.

## Debugging

Set `DEBUG=debug` or `DEBUG=trace` for verbose logging:
```bash
DEBUG=debug cm -s test
```

**Daemon logs:** `~/.cm/daemon.log` (path derived from binary name, e.g., `~/.attn/daemon.log` for attn)

## Architecture

Claude Manager (`cm`) tracks multiple Claude Code sessions and surfaces which ones need attention via tmux status bar or an interactive dashboard.

### Core Flow

1. **Wrapper** (`cmd/cm/main.go`): CLI entry point that wraps `claude` command. Parses cm-specific flags (`-s`, `-y`, `-d`), passes unknown flags through to claude. Registers session with daemon, writes temporary hooks config, executes claude with `--settings` pointing to hooks.

2. **Hooks** (`internal/hooks`): Generates Claude Code hooks JSON that reports state changes back to daemon via unix socket using `nc`. Three hooks: Stop (waiting), UserPromptSubmit (working), PostToolUse/TodoWrite (update todos).

3. **Daemon** (`internal/daemon`): Background process listening on `~/.{binary}.sock` (e.g., `~/.cm.sock`). Handles register/unregister/state/todos/query/heartbeat commands. Auto-started by wrapper if not running.

4. **Store** (`internal/store`): Thread-safe in-memory session storage with mutex protection.

5. **Client** (`internal/client`): Sends JSON messages to daemon over unix socket.

6. **Protocol** (`internal/protocol`): Message types and parsing. States: "working" or "waiting".

### GitHub PR Monitoring

The daemon polls GitHub every 90 seconds for PRs that need attention (using `gh` CLI):
- PRs where you're a requested reviewer
- Your PRs with review comments, CI failures, or merge conflicts

The store (`internal/store`) tracks both sessions and PRs with mute states. PR actions (approve, merge) are handled via WebSocket commands to the daemon.

### Protocol Versioning

**Rule:** When changing the protocol (adding/modifying commands, events, or message structures), increment `ProtocolVersion` in `internal/protocol/types.go`.

The app checks protocol version on WebSocket connect and shows an error banner if mismatched. This prevents silent failures when the daemon is running old code.

**After protocol changes:**
1. Increment `ProtocolVersion` in `internal/protocol/types.go`
2. Run `make install` to install new binary
3. Kill the running daemon: `pkill -f "cm daemon"` (or restart the app)
4. The app will auto-start a new daemon with updated code

**Why:** The daemon runs as a background process and survives `make install`. Without version checking, old daemon + new app = mysterious failures with no logs.

### Communication

All IPC uses JSON over unix socket at `~/.{binary}.sock` (paths derived from binary name via `internal/config`). Messages have a `cmd` field to identify type. Hooks use shell commands with `nc` to send state updates.

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
pnpm run dev:all    # Starts both pty-server and tauri dev
```

This runs two processes concurrently:
- **pty-server**: Node.js sidecar using node-pty over Unix socket (`~/.cm-pty.sock`)
- **tauri dev**: Vite + Tauri development server

### PTY Architecture

The app uses a Node.js sidecar (`pty-server/`) instead of tauri-pty because:
- Event-driven streaming vs polling-based IPC
- No fixed buffer size limitations (tauri-pty had 1024-byte limit)
- Full terminal width support (no rendering issues at wide widths)

Communication flow:
```
Frontend (React) → Tauri Commands → Rust pty_bridge → Unix Socket → pty-server (node-pty)
```

### Terminal Component (xterm.js)

When modifying `app/src/components/Terminal.tsx`:

1. **Wait for container dimensions** - Use ResizeObserver to wait for valid size before calling `onReady`
2. **Pre-calculate initial dimensions** - Measure font before creating XTerm to avoid 80x24 default
3. **Resize xterm first, then PTY** - Call `term.resize()` then notify PTY (sends SIGWINCH)
4. **Use VS Code's resize debouncing** - Y-axis immediate, X-axis 100ms debounced (text reflow is expensive)

See `docs/plans/2025-12-09-node-pty-sidecar-design.md` for architecture details.

### E2E Testing

```bash
cd app
pnpm run test:e2e          # Run all E2E tests
pnpm run test:e2e -- --ui  # Run with Playwright UI
```

## When Something Is Broken

1. **Diagnose WHY** before proposing fixes - understand the root cause
2. **Fix the root cause**, don't remove functionality to make the error go away
3. **If refactoring**, list preserved behaviors and verify each one survives
4. **Never remove user-requested functionality** without explicit approval

Ask yourself: "Am I fixing the problem or avoiding it?"
