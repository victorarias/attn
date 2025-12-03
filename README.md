# claude-manager

Track and manage multiple Claude Code sessions across tmux.

## Problem

Running multiple Claude sessions means losing track of which ones need your input.

## Solution

- **tmux status bar:** Shows `2 waiting: drumstick, meta`
- **Dashboard:** Interactive TUI to see all sessions and jump to any pane
- **Hooks:** Uses Claude Code hooks to track state changes

## Usage

```bash
cm drumstick      # Start Claude with label "drumstick"
cm                # Start Claude with directory name as label
cm -d             # Open dashboard
cm status         # Output for tmux status bar
```

## tmux Setup

```tmux
set -g status-interval 5
set -g status-right '#(cm status)'
```

## Status

**Design phase** - see [design doc](docs/plans/2025-12-03-claude-manager-design.md)
