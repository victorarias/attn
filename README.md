# attn

A desktop app for managing multiple [Claude Code](https://claude.ai/code) sessions. Know which ones need your attention.

![Demo](docs/demo.gif)

## The Problem

You're running 5 Claude Code sessions across different projects. One finished and is waiting for approval. Another hit an error. A third is asking a clarifying question. But you're focused on the fourth one and have no idea the others need you.

## The Solution

**attn** tracks all your Claude Code sessions in one place:

- **Real-time status** - See which sessions are working, waiting, or idle
- **Desktop notifications** - Get alerted when sessions need input
- **Built-in terminal** - Manage sessions without leaving the app
- **GitHub PR integration** - Track PRs needing review, with CI failures, or merge conflicts
- **tmux status bar** - Quick glance at session states from your terminal

## Installation

### Download

Download the latest `.dmg` from [Releases](https://github.com/victorarias/attn/releases).

### Build from source

Requires: Go 1.21+, Rust, Node.js, pnpm

```bash
git clone https://github.com/victorarias/attn.git
cd attn
make install-all   # Installs daemon + desktop app
```

## Usage

### Desktop App

Launch **attn** from Applications. The app shows all active Claude Code sessions with their current state.

### CLI

```bash
attn                # Start Claude with directory name as session label
attn -s myproject   # Start Claude with explicit session label
attn status         # Output for tmux status bar
attn list           # List all sessions (JSON)
```

### tmux Integration

Add to `.tmux.conf`:

```tmux
set -g status-interval 5
set -g status-right '#(attn status)'
```

## Session States

| State | Indicator | Meaning |
|-------|-----------|---------|
| Working | Green | Claude is actively generating |
| Waiting | Orange | Claude needs your input |
| Idle | Gray | Claude finished its task |

## Requirements

- **macOS** (Apple Silicon or Intel)
- **Claude Code** CLI installed

## How It Works

1. `attn` wraps the `claude` command and installs hooks that report state changes
2. A background daemon tracks all session states via unix socket
3. The desktop app connects via WebSocket for real-time updates
4. GitHub integration polls for PRs using the `gh` CLI

## Development

```bash
# Daemon only (fast iteration, ~2s)
make install        # Build and install daemon

# Desktop app (with hot reload)
cd app && pnpm install && pnpm run dev:all

# Full build
make build-app      # Build daemon + Tauri app
make install-app    # Install to /Applications
make dist           # Create distributable DMG

# Testing
make test-all       # Run Go + frontend tests
```

## Status

**Beta** - I use attn daily for my own work, but offer no guarantees. Expect rough edges.

## Built With

- [Claude Code](https://claude.ai/code) - The AI coding assistant this tool wraps
- [Tauri](https://tauri.app) - Desktop app framework
- [React](https://react.dev) - UI framework
- [Go](https://go.dev) - Daemon and CLI
- [SQLite](https://sqlite.org) - Local storage

## License

[GPL-3.0](LICENSE)

## Contributing

Contributions welcome! Please open an issue first to discuss what you'd like to change.
