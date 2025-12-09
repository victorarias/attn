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

## Terminal Component (xterm.js + PTY)

When modifying `app/src/components/Terminal.tsx`:

1. **Never spawn PTY until terminal has correct dimensions** - Use ResizeObserver to wait for container to have real size before calling `onReady`
2. **On resize: PTY first, then xterm.js** - Use `proposeDimensions()` to get new size, resize PTY (sends SIGWINCH), wait ~50ms, then call `fit()`
3. **xterm.js defaults to 80x24** - Don't trust `term.cols/rows` until after `fit()` has been called

See `docs/plans/2025-12-08-xterm-tui-rendering-bug.md` for full investigation.

## When Something Is Broken

1. **Diagnose WHY** before proposing fixes - understand the root cause
2. **Fix the root cause**, don't remove functionality to make the error go away
3. **If refactoring**, list preserved behaviors and verify each one survives
4. **Never remove user-requested functionality** without explicit approval

Ask yourself: "Am I fixing the problem or avoiding it?"
