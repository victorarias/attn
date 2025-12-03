# claude-manager

Track and manage multiple Claude Code sessions across tmux.

## Problem

Running multiple Claude sessions means losing track of which ones need your input.

## Solution

- **tmux status bar:** Shows `2 waiting: drumstick, meta`
- **Dashboard:** Interactive TUI to see all sessions and jump to any pane
- **Hooks:** Uses Claude Code hooks to track state changes

## Installation

```bash
go install github.com/victorarias/claude-manager/cmd/cm@latest
```

Or build from source:

```bash
git clone https://github.com/victorarias/claude-manager.git
cd claude-manager
go build -o cm ./cmd/cm
mv cm ~/bin/  # or anywhere in your PATH
```

## Usage

```bash
cm                # Start Claude with directory name as label
cm -s drumstick   # Start Claude with explicit label
cm -d             # Open dashboard
cm status         # Output for tmux status bar
cm list           # List all sessions (JSON)
cm daemon         # Run daemon in foreground
```

## tmux Setup

Add to your `.tmux.conf`:

```tmux
set -g status-interval 5
set -g status-right '#(cm status)'
```

## How It Works

1. `cm` wraps `claude` and installs hooks that report state changes
2. A background daemon tracks all sessions
3. `cm status` queries the daemon for waiting sessions
4. `cm -d` opens an interactive dashboard

## Architecture

See [design doc](docs/plans/2025-12-03-claude-manager-design.md) for details.
