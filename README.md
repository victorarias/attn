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
go install github.com/victorarias/claude-manager/cmd/attn@latest
```

Or build from source:

```bash
git clone https://github.com/victorarias/claude-manager.git
cd claude-manager
go build -o attn ./cmd/attn
mv attn ~/bin/  # or anywhere in your PATH
```

## Usage

```bash
attn                # Start Claude with directory name as label
attn -s drumstick   # Start Claude with explicit label
attn -d             # Open dashboard
attn status         # Output for tmux status bar
attn list           # List all sessions (JSON)
attn daemon         # Run daemon in foreground
```

## tmux Setup

Add to your `.tmux.conf`:

```tmux
set -g status-interval 5
set -g status-right '#(attn status)'
```

## How It Works

1. `attn` wraps `claude` and installs hooks that report state changes
2. A background daemon tracks all sessions
3. `attn status` queries the daemon for waiting sessions
4. `attn -d` opens an interactive dashboard

## Architecture

See [design doc](docs/plans/2025-12-03-claude-manager-design.md) for details.
