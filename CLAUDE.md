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

## Architecture

Claude Manager (`cm`) tracks multiple Claude Code sessions and surfaces which ones need attention via tmux status bar or an interactive dashboard.

### Core Flow

1. **Wrapper** (`cmd/cm/main.go`): CLI entry point that wraps `claude` command. Parses cm-specific flags (`-s`, `-y`, `-d`), passes unknown flags through to claude. Registers session with daemon, writes temporary hooks config, executes claude with `--settings` pointing to hooks.

2. **Hooks** (`internal/hooks`): Generates Claude Code hooks JSON that reports state changes back to daemon via unix socket using `nc`. Three hooks: Stop (waiting), UserPromptSubmit (working), PostToolUse/TodoWrite (update todos).

3. **Daemon** (`internal/daemon`): Background process listening on `~/.{binary}.sock` (e.g., `~/.cm.sock`). Handles register/unregister/state/todos/query/heartbeat commands. Auto-started by wrapper if not running.

4. **Store** (`internal/store`): Thread-safe in-memory session storage with mutex protection.

5. **Client** (`internal/client`): Sends JSON messages to daemon over unix socket.

6. **Protocol** (`internal/protocol`): Message types and parsing. States: "working" or "waiting".

### Communication

All IPC uses JSON over unix socket at `~/.{binary}.sock` (paths derived from binary name via `internal/config`). Messages have a `cmd` field to identify type. Hooks use shell commands with `nc` to send state updates.

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

## When Something Is Broken

1. **Diagnose WHY** before proposing fixes - understand the root cause
2. **Fix the root cause**, don't remove functionality to make the error go away
3. **If refactoring**, list preserved behaviors and verify each one survives
4. **Never remove user-requested functionality** without explicit approval

Ask yourself: "Am I fixing the problem or avoiding it?"
