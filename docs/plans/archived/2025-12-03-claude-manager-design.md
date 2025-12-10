# Claude Manager - Design Document

**Date:** 2025-12-03
**Author:** Victor Arias
**Status:** Draft

## Problem

When running multiple Claude Code sessions across tmux panes and sessions, it's easy to lose track of which sessions are waiting for input. This leads to Claude sessions sitting idle while you forget they need attention.

## Goals

1. Visual indicator in tmux status bar when Claude sessions are waiting for input
2. Show count and names of waiting sessions at a glance
3. Interactive dashboard to see all sessions and jump to any pane
4. "Parallel AI engineering manager" - awareness of all concurrent AI work

## Non-Goals

- Managing non-Claude processes
- Complex workflow orchestration
- Remote/distributed session management

## Design Overview

### Components

```
┌─────────────────────────────────────────────────────────────────┐
│                         tmux status bar                          │
│                    "2 waiting: drumstick, meta"                  │
└─────────────────────────────────────────────────────────────────┘
        ▲                                              ▲
        │ cm status                                    │
        │                                              │
┌───────┴───────┐                            ┌────────┴────────┐
│   cm daemon   │◄───── Unix socket ────────►│  cm dashboard   │
│               │                            │    (TUI)        │
└───────┬───────┘                            └─────────────────┘
        ▲
        │ register/state updates
        │
┌───────┴───────┐
│  cm <label>   │──────► Claude Code (with hooks)
│   (wrapper)   │
└───────────────┘
```

### 1. Daemon (`cm daemon`)

A lightweight background service that tracks all registered Claude sessions.

**Responsibilities:**
- Listen on Unix socket (`~/.claude-manager.sock`)
- Track sessions: ID, label, directory, tmux location, state, timestamps
- Handle events: register, state-change, heartbeat, unregister
- Answer queries: list all, list waiting, get session details
- Cleanup: detect dead sessions via missed heartbeats or tmux pane gone

**Session State:**
```json
{
  "id": "abc123",
  "label": "drumstick",
  "directory": "/Users/victor/projects/drumstick",
  "tmux_session": "projects",
  "tmux_window": 2,
  "tmux_pane": "%42",
  "state": "waiting",
  "state_since": "2025-12-03T10:15:00Z",
  "todos": ["Deploy to prod", "Connect to API"]
}
```

**Protocol:** JSON over Unix socket

Register:
```json
{"cmd": "register", "id": "abc123", "label": "drumstick", "dir": "...", "tmux": "projects:2.%42"}
```

State update:
```json
{"cmd": "state", "id": "abc123", "state": "waiting"}
```

Query:
```json
{"cmd": "query", "filter": "waiting"}
```

Todos update:
```json
{"cmd": "todos", "id": "abc123", "todos": ["Task 1", "Task 2"]}
```

Unregister:
```json
{"cmd": "unregister", "id": "abc123"}
```

### 2. Wrapper Command (`cm <label>`)

Starts Claude Code with tracking enabled.

**Usage:**
```bash
cm                  # Use directory basename as label
cm -s drumstick     # Use explicit label
```

**Startup sequence:**
1. Generate unique session ID
2. Capture tmux location: `tmux display -p '#{session_name}:#{window_index}.#{pane_id}'`
3. Check if daemon running, start if not
4. Register with daemon
5. Install Claude hooks (write to temp hooks config)
6. Start Claude with hooks active
7. On exit: unregister from daemon

**Hooks installed:**
| Hook | Action |
|------|--------|
| `SessionStart` | Register with daemon |
| `Stop` | Report state = "waiting" |
| `UserPromptSubmit` | Report state = "working" |
| `SessionEnd` | Unregister from daemon |
| `PostToolUse` (TodoWrite) | Send current todos to daemon (stretch goal) |

### 3. Status Bar (`cm status`)

Simple command that queries daemon and outputs formatted text for tmux.

**Output examples:**
- Nothing waiting: `` (empty string)
- One waiting: `1 waiting: drumstick`
- Multiple waiting: `2 waiting: drumstick, hurdy-gurdy`
- Many waiting: `4 waiting: drumstick, hurdy...` (truncate)

**tmux configuration:**
```tmux
set -g status-interval 5
set -g status-right '#(cm status)'
```

**Optional:** Color coding based on wait time (yellow > 2min, red > 5min).

### 4. Dashboard (`cm dashboard` or `cm -d`)

Interactive TUI showing all tracked sessions.

**Display:**
```
┌─ Claude Sessions ──────────────────────────────────────────┐
│                                                            │
│  ● drumstick      waiting  2m 13s   Deploy to prod         │
│  ○ hurdy-gurdy    working  0m 45s   Running tests          │
│  ● meta           waiting  5m 02s   (no todos)             │
│                                                            │
│  ● = waiting (needs input)    ○ = working                  │
│                                                            │
│  [Enter] Jump to pane   [r] Refresh   [q] Quit             │
└────────────────────────────────────────────────────────────┘
```

**Columns:**
- Status indicator: `●` waiting (needs input), `○` working
- Label
- State text
- Time in current state
- Current todo (first item, if available from TodoWrite hook)

**Interactions:**
- Arrow keys / j/k: Navigate
- Enter: Jump to selected session's tmux pane
- r: Refresh
- q: Quit

**Jump implementation:**
```bash
tmux switch-client -t '=projects:2.%42'
```

## Implementation

### Language & Dependencies

**Go** for all components:
- Single binary distribution
- Fast startup
- `bubbletea` for TUI
- Standard library for Unix socket, JSON

### Project Structure

```
claude-manager/
├── cmd/
│   └── cm/
│       └── main.go         # CLI entrypoint
├── internal/
│   ├── daemon/
│   │   ├── daemon.go       # Core daemon logic
│   │   ├── session.go      # Session state management
│   │   └── protocol.go     # JSON protocol handling
│   ├── client/
│   │   └── client.go       # Client for talking to daemon
│   ├── wrapper/
│   │   └── wrapper.go      # Claude wrapper logic
│   ├── hooks/
│   │   └── hooks.go        # Hook generation
│   ├── status/
│   │   └── status.go       # Status bar output
│   └── dashboard/
│       └── dashboard.go    # Bubbletea TUI
├── docs/
│   └── plans/
│       └── 2025-12-03-claude-manager-design.md
├── go.mod
├── go.sum
└── README.md
```

### CLI Interface

```bash
cm                     # Start Claude with tracking (label = directory name)
cm -s <label>          # Start Claude with explicit label
cm daemon              # Run daemon in foreground
cm daemon --background # Run daemon in background
cm status              # Output for tmux status bar
cm -d                  # Open dashboard
cm dashboard           # Open dashboard (alias)
cm list                # List all sessions (JSON)
cm kill <label>        # Unregister a session
```

### File Locations

| File | Purpose |
|------|---------|
| `~/.claude-manager.sock` | Unix socket for IPC |
| `~/.claude-manager.pid` | Daemon PID file |
| `~/.claude-manager.log` | Daemon logs |
| `~/.claude-manager-state.json` | Optional state persistence |

### Auto-start Daemon

When `cm` or `cm -s <label>` runs:
1. Check if socket exists and daemon responds
2. If not, fork daemon in background
3. Wait for socket to be ready
4. Continue with registration

## Future Enhancements

- **Notifications:** macOS notification when session waits > N minutes
- **History:** Track completed sessions and their durations
- **Metrics:** How much time spent waiting vs working
- **Multi-machine:** Sync state across machines (stretch)

## Decisions

1. **Dashboard auto-refresh:** Yes - live updates
2. **Hook installation:** `--hooks` flag - less intrusive, easier to install/update, no file pollution
3. **Sessions without `cm`:** Not tracked - opt-in only, keeps it simple

## References

- [Claude Code Hooks Documentation](https://docs.anthropic.com/en/docs/claude-code/hooks)
- [Bubbletea TUI Framework](https://github.com/charmbracelet/bubbletea)
