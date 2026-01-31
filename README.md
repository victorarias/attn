# attn

Keep multiple AI coding sessions under control without losing your mind. **attn** is a desktop app + CLI
that wraps [Claude Code](https://claude.ai/code) and [Codex](https://developers.openai.com/codex) to help
you orchestrate sessions: see state, jump to the right terminal, and manage worktrees, diffs, and PRs
from one place.

![Demo](docs/demo.gif)

## Why

You are running multiple Claude/Codex sessions across repos. One is waiting for approval, another
hit an error, and a third has a question. You are focused on the fourth and have no idea the others
need you. attn is the polite, slightly relentless friend who taps you on the shoulder.

## What attn does

**attn** tracks all your sessions in one place:

- **Real-time status** - Working, waiting, idle, approval required
- **Built-in terminal** - Run Claude/Codex sessions without leaving the app; open utility shells too
- **Resume sessions** - Use the agent's built-in picker (Option-3 in the new-session dialog)
- **Git worktrees** - Create and manage worktrees for parallel work (Cmd+N)
- **Git diffs** - View changes and send diff snippets as context
- **Session forking** - Fork a Claude session to explore alternatives (Cmd+Shift+F)
- **Quick Find** - Extract URLs, paths, and hashes from terminal output (Cmd+F)
- **GitHub PR integration** - Track PRs that need review, have CI failures, or merge conflicts across github.com + GHES
- **tmux status bar** - See session status from your terminal

Session tracking uses Claude hooks plus a lightweight classifier to detect when the agent is waiting
for input (even if it stops without explicit approval requests).

## Quickstart

```bash
git clone https://github.com/victorarias/attn.git
cd attn
make install-all
```

Launch **attn** from Applications and click **+** to start a session.

## Key flows (fast + fun)

- **Start**: Cmd+N → pick agent → pick path → go.
- **Resume**: Cmd+N → Option-3 → use the agent picker → back in business.
- **Fork (Claude)**: Cmd+Shift+F → name it → optionally create a worktree → explore.
- **Worktree**: Cmd+Shift+N → pick repo → choose branch → parallel universe unlocked.
- **Review**: open a session on a branch that differs from origin/main → click **Review**.

## Installation

> **Alpha**: No pre-built releases yet. You'll need to build from source.

### Requirements

- macOS (Apple Silicon tested; other platforms unverified)
- Go 1.25+ (matches `go.mod`)
- Rust (stable) + Tauri prerequisites
- Node.js 20+ and pnpm
- Git + GitHub CLI (`gh`) v2.81.0+ authenticated (multi-host support)
- Claude Code and/or Codex installed

### Build + install

Only tested on macOS (Apple Silicon).

```bash
make install-all   # Installs daemon + desktop app
```

## Usage

### Desktop App

Launch **attn** from Applications. The app lists all active sessions with their current state.

**New session dialog**

- **Agent toggle**: Option-1 (Codex) / Option-2 (Claude)
- **Resume toggle**: Option-3 to open the agent's resume picker
- **Location**: type a path or pick from recent locations

**Session controls**

- Cmd+N: New session
- Cmd+Shift+N: New session (worktree)
- Cmd+Shift+F: Fork Claude session
- Cmd+F: Quick Find in terminal

See `app/src/shortcuts/registry.ts` for the full list. Keyboard-first is the intended experience.

### Review panel (review dialog)

Open a session on a branch that differs from `origin/main`. The **Review** button appears in the
header; click it to open the review panel. From there you can:

- Browse changed files and diffs
- Add inline comments
- Mark files as viewed
- Resolve or delete comments
- Trigger a Claude Code review that leaves comments automatically
- Send review comments to the main Claude session as a pre-written prompt

It’s designed to keep review context in the same place as your sessions, so you can bounce between
coding and reviewing without losing your flow.

## Shortcut cheat sheet (most useful)

- Cmd+N: new session
- Cmd+Shift+N: new session (worktree)
- Option-1 / Option-2 / Option-3: Codex / Claude / Resume
- Cmd+Shift+F: fork Claude session
- Cmd+F: Quick Find
- Cmd+K: attention drawer (PRs + sessions needing input)
- Cmd+` : toggle utility terminal panel
- Cmd+ArrowUp / Cmd+ArrowDown: previous / next session
- Cmd+R: refresh PRs

### CLI

```bash
attn                 # Open the app (or run inside the app wrapper)
attn -s myproject    # Start a session with explicit label
attn --resume        # Resume with agent picker (inside app)
attn status          # Output for tmux status bar
attn list            # List all sessions (JSON)
```

### Agents

- **Claude Code**: sessions are launched with hooks for state tracking.
- **Codex**: sessions run via the Codex CLI.

When you toggle **Resume**, attn invokes:

- `claude -r` (interactive picker)
- `codex resume` (interactive picker)

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
| Waiting | Yellow | Claude needs your input |
| Idle | Gray | Claude finished its task |
| Waiting for approval | Flashing yellow | Claude is waiting for approval |

## How It Works

1. `attn` wraps the agent CLI (Claude or Codex) and installs hooks (Claude) to report state changes
2. A background daemon tracks session state via a local socket
3. The desktop app connects via WebSocket for real-time updates
4. GitHub integration uses the `gh` CLI to query PRs (multi-host via `gh auth status`)

## Configuration

You can configure defaults from **Settings** in the app, including:

- Projects directory for repo discovery
- GitHub CLI authentication, connected hosts, and rate limit info
- Agent executable overrides (if you want custom paths)

## Development

```bash
# Daemon only (fast iteration, ~2s)
make install        # Build and install daemon

# Desktop app (with hot reload)
cd app && pnpm install && pnpm run dev

# Full build
make build-app      # Build daemon + Tauri app
make install-app    # Install to /Applications
make dist           # Create distributable DMG
make install-all    # Install daemon + app

# Testing
make test-all       # Run Go + frontend tests
```

## Docs

- `docs/USAGE.md`
- `docs/TROUBLESHOOTING.md`
- `docs/CONFIGURATION.md`
- `docs/CLI.md`

## Troubleshooting

- **No sessions showing**: make sure the daemon is running (`attn status`).
- **Resume doesn't open a picker**: verify your agent CLI is installed and on PATH.
- **GitHub PRs empty**: run `gh auth status` and sign in if needed.
- **GH CLI too old**: upgrade to `gh` v2.81.0+ (e.g., `brew upgrade gh`).
- **Worktree creation fails**: ensure your repo has a clean git state.

## FAQ

**Q: Does attn replace my agent?**  
A: No. It orchestrates your sessions and shows state. You still interact with the agent directly.

**Q: Can I run it without GitHub integration?**  
A: Yes. PR features just won't show anything until `gh` is authenticated.

**Q: Why is resume a picker instead of a dropdown?**  
A: It uses the agent's native picker so you get consistent behavior and search.

## Status

**Alpha** - I use attn daily for my own work, but expect rough edges.

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
