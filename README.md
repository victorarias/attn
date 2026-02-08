<p align="center">
  <img src="docs/banner.png" alt="attn — attention hub" width="100%" />
</p>

# attn

**attention hub** — because your head shouldn't feel like concrete by 3pm.

I built this after noticing a pattern: I'd start the day sharp, spin up 4-5 AI agents across different repos, and by mid-afternoon my brain was soup. Not from the coding — from the *managing*. Which terminal has the agent that's stuck? Did that one finish? Wait, who asked me a question 20 minutes ago? Alt-tab, alt-tab, alt-tab, scroll, squint, repeat.

attn fixes the dumbest part of multi-agent workflows: knowing what needs you right now.

It's a desktop app that wraps your agent CLIs — Claude Code, Codex, Copilot — and puts them all in one window with color-coded status. Green means working. Yellow means "hey, I need you." Gray means done. No more tab-hunting. No more heavy head.

**There is no custom agent UI.** attn wraps each CLI directly. You get the real, native experience of every agent — just organized.

<!-- ![Demo](docs/demo.gif) -->

## What you get

**One sidebar to rule them all** — Every agent session, live state at a glance. The one that needs you glows. Click it. Done.

**Embedded terminals** — No more "which terminal was that in?" Run agents inside the app. Open utility shells next to them. Resume sessions, fork them, never leave the window.

**Code review** — Full diff viewer with inline comments and AI-assisted review. Claude reads your branch, leaves comments, you resolve them. All without opening a browser. *(Claude only)*

**PR dashboard** — Your PRs, your review requests, CI failures, merge conflicts — one place. Works across GitHub.com and GitHub Enterprise. Open a PR directly into a worktree.

**Git worktrees & branches** — Parallel agents need parallel branches. Create, switch, and manage worktrees from the app. Stop stepping on your own feet.

**Quick Find** — Cmd+F to yank URLs, paths, and hashes out of terminal output.

## Supported agents

| Agent | State detection | Review & forking |
|---|---|---|
| [Claude Code](https://claude.ai/code) | Hooks + classifier | Yes |
| [Codex](https://developers.openai.com/codex) | PTY heuristics + transcript classifier | No |
| [Copilot CLI](https://docs.github.com/en/copilot/using-github-copilot/using-github-copilot-in-the-command-line) | PTY heuristics + transcript classifier | No |

## Install

### Homebrew cask (desktop app)

```bash
brew tap victorarias/attn https://github.com/victorarias/attn
brew install --cask victorarias/attn/attn
```

### Homebrew formula (CLI + daemon only)

```bash
brew install victorarias/attn/attn
```

### Direct DMG

Grab the [latest release](https://github.com/victorarias/attn/releases/latest), open the DMG, drag to Applications.

### Updating

```bash
# Formula
brew update && brew upgrade victorarias/attn/attn

# Cask
brew update && brew upgrade --cask victorarias/attn/attn
```

The app nudges you when a new release exists. No auto-install — you pick when.

## Prerequisites

- macOS (Apple Silicon)
- At least one agent CLI installed
- [GitHub CLI](https://cli.github.com/) (`gh`) v2.81.0+ for PR features

## Quick start

1. Launch **attn** from Applications (or just type `attn`).
2. **Cmd+N** — pick an agent, pick a directory, go.
3. Watch the sidebar. Colors tell you who needs you.

## Session states

| Color | What it means |
|---|---|
| 🟢 Green | Agent is working — leave it alone |
| 🟡 Yellow | Agent has a question — go help |
| 🟡 Flashing | Agent wants tool approval — go approve |
| ⚫ Gray | Agent is done — move on |

## Shortcuts

| Shortcut | What it does |
|---|---|
| Cmd+N | New session |
| Cmd+Shift+N | New session in a worktree |
| Cmd+Shift+F | Fork session (Claude) |
| Cmd+F | Quick Find |
| Cmd+K | Attention drawer (who needs me?) |
| Cmd+\` | Utility terminal |
| Cmd+Up / Down | Jump between sessions |
| Cmd+R | Refresh PRs |

## CLI

```bash
attn                 # Open app, start session
attn -s myproject    # Session with a label
attn --resume        # Resume via agent's native picker
attn list            # All sessions as JSON
attn daemon          # Run daemon in foreground
```

## How it works

1. `attn` wraps your agent CLI and installs hooks (Claude) or reads PTY output (Codex, Copilot) to detect state.
2. A background daemon tracks sessions via unix socket (`~/.attn/attn.sock`).
3. The desktop app connects over WebSocket for real-time updates.
4. A lightweight classifier figures out if Claude stopped because it's done or because it's waiting for you.
5. `gh` polls PRs across all your authenticated GitHub hosts.

## Build from source

Requires Go 1.25+, Rust (stable), Node.js 20+, pnpm, and [Tauri prerequisites](https://v2.tauri.app/start/prerequisites/).

```bash
git clone https://github.com/victorarias/attn.git && cd attn
```

| Command | What it does |
|---|---|
| `make build` | Build Go daemon binary |
| `make install` | Install daemon CLI (~2s iteration) |
| `make build-app` | Build daemon + Tauri app |
| `make install-app` | Install app to /Applications |
| `make install-all` | Both |
| `make dist` | Create DMG |
| `make test` | Go tests |
| `make test-frontend` | Frontend tests (vitest) |
| `make test-harness` | Go + frontend + E2E |

## Docs

| | |
|---|---|
| [Usage](docs/USAGE.md) | App and CLI usage |
| [CLI reference](docs/CLI.md) | Commands and flags |
| [Configuration](docs/CONFIGURATION.md) | Settings |
| [Troubleshooting](docs/TROUBLESHOOTING.md) | When things go sideways |
| [Release](docs/RELEASE.md) | Maintainer runbook |

## Status

Alpha — I use it every day. It's still evolving.

## Built with

[Tauri](https://tauri.app) / [React](https://react.dev) / [Go](https://go.dev) / [SQLite](https://sqlite.org)

## License

[GPL-3.0](LICENSE)
