# attn

Track multiple Claude Code sessions and surface which ones need your attention.

## Problem

Running multiple Claude sessions means constantly checking which ones are waiting for input.

## Solution

- **Desktop app**: Dashboard showing all sessions with real-time state updates
- **tmux integration**: Status bar shows waiting sessions
- **GitHub PR monitoring**: Tracks PRs needing review, with CI failures, or merge conflicts

## Installation

```bash
make install
```

This builds the CLI and installs it to `~/.local/bin/attn`.

## Usage

```bash
attn                # Start Claude with directory name as session label
attn -s myproject   # Start Claude with explicit session label
attn status         # Output for tmux status bar
attn list           # List all sessions (JSON)
attn daemon         # Run daemon in foreground (for debugging)
```

## tmux Setup

Add to `.tmux.conf`:

```tmux
set -g status-interval 5
set -g status-right '#(attn status)'
```

## How It Works

1. `attn` wraps `claude` and installs hooks that report state changes to a daemon
2. The daemon tracks session states: **working**, **waiting_input**, or **idle**
3. A Tauri desktop app connects via WebSocket for real-time updates
4. `attn status` queries the daemon for a tmux-friendly summary

## Session States

| State | Meaning |
|-------|---------|
| working | Claude is actively generating |
| waiting_input | Claude asked a question or needs permission |
| idle | Claude finished its task |

## Development

```bash
make build          # Build CLI to ./attn
make test           # Run Go tests
make install        # Build and install to ~/.local/bin

# Desktop app
cd app
pnpm install
pnpm run dev:all    # Start app in development mode
```

## Architecture

- **CLI** (`cmd/attn`): Wrapper that registers sessions and installs hooks
- **Daemon** (`internal/daemon`): Background process tracking all sessions via unix socket
- **Hooks** (`internal/hooks`): Claude Code hooks that report state changes
- **App** (`app/`): Tauri + React desktop dashboard with native Rust PTY
