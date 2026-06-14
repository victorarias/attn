<p align="center">
  <img src="docs/banner.png" alt="attn — attention hub" width="100%" />
</p>

# attn

**attention hub** — because your head shouldn't feel like concrete by 3pm.

attn is a macOS app that wraps your agent CLIs — Claude Code, Codex, Copilot — in one window with color-coded status, so you always know which one needs you. Green = working. Yellow = "hey, I need you." Gray = done.

I built it after noticing a pattern: I'd start the day sharp, spin up 4-5 AI agents across different repos, and by mid-afternoon my brain was soup. Not from the coding — from the *managing*. Which terminal has the agent that's stuck? Did that one finish? Wait, who asked me a question 20 minutes ago? Alt-tab, alt-tab, alt-tab, scroll, squint, repeat.

attn fixes the dumbest part of multi-agent workflows: knowing what needs you right now.

**There is no custom agent UI** — attn wraps each CLI directly, so you get the real thing, just organized.

## What you get

**Workspaces in one sidebar** — The sidebar groups your work into workspaces, each holding the sessions and terminals for one task. The one that needs you glows. Click it. Done. Drag to reorder, drag a session out into its own workspace, rename anything inline.

**Grid view — mission control** — Hit Cmd+G to see every session as a live terminal tile at once; the ones waiting on you flash. Click a tile to zoom in and type straight into it. Pick the layout, drop tiles you don't care about — it sticks across restarts.

**Panes, splits, and first-class shells** — A workspace can hold several sessions side by side. Split a pane, open a plain shell as its own session from the same dialog you use for agents, and move focus between panes with the keyboard. Never leave the window.

**A terminal that does more** — Cmd+F finds across the full scrollback. Cmd-click a path or URL to open it. When your shell marks commands (fish does by default), click a command's output to grab the whole block — copy the command with its output in one go, or filter a long block down to just the lines you want.

**Remote daemons over SSH** — Keep sessions on a GPU box, Linux host, or local VM and manage them from the same app. Spawn new sessions remotely, browse remote repos, and open remote worktrees without juggling another terminal window.

**PR dashboard** — Your PRs, your review requests, CI failures, merge conflicts — one place. Works across GitHub.com and GitHub Enterprise. Open a PR directly into a worktree.

**Git worktrees & branches** — Parallel agents need parallel branches. Spin them up from the app — stop stepping on your own feet.

## Supported agents

| Agent | State detection | Resume |
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

This installs `attn.app` with its bundled daemon/runtime binary.

### Direct DMG

Grab the [latest release](https://github.com/victorarias/attn/releases/latest), open the DMG, drag to Applications.

### Updating

```bash
brew update && brew upgrade --cask victorarias/attn/attn
```

The app nudges you when a new release exists. No auto-install — you pick when.

## Prerequisites

- macOS (Apple Silicon)
- At least one agent CLI installed
- [GitHub CLI](https://cli.github.com/) (`gh`) v2.81.0+ for PR features

## Quick start

1. Launch **attn** from Applications.
2. **Cmd+T** — start a workspace: pick an agent (or a plain shell), pick a directory, go. **Cmd+N** adds another session to the workspace you're in.
3. Watch the sidebar. Colors tell you who needs you.
4. Press **Cmd+/** any time for the full shortcuts list.
5. Optional: add an SSH endpoint in Settings and run remote or VM sessions from the same picker.

## Session states

| Color | What it means |
|---|---|
| 🟢 Green | Agent is working — leave it alone |
| 🟡 Yellow | Agent needs you: asked a question, or finished a long run (5+ min) and is waiting for your review |
| 🟡 Flashing | Agent wants tool approval — go approve |
| 🔵 Blue (slow pulse) | Parked on a `/loop` or schedule — it'll resume itself, no action needed |
| 🟣 Purple | State couldn't be read reliably — worth a glance |
| ⚪ Gray | Done, or a plain shell — move on |

## Shortcuts

| Shortcut | What it does |
|---|---|
| Cmd+T | New workspace (with an initial session) |
| Cmd+N | New session in current workspace |
| Cmd+Shift+N | New session, split sideways |
| Cmd+D / Cmd+Shift+D | Split pane down / sideways |
| Cmd+Option+←↑→↓ | Move between panes (cross into the next workspace at an edge) |
| Cmd+1–9 | Jump to a workspace |
| Cmd+Up / Down | Jump between sessions |
| Cmd+G | Grid view |
| Cmd+F | Find in terminal |
| Cmd+K | Action menu |
| Cmd+Shift+P | Attention drawer (who needs me?) |
| Cmd+\` | Utility terminal |
| Cmd+R | Refresh PRs |
| Cmd+/ | All keyboard shortcuts |

Every binding is customizable — press **Cmd+/** in the app for the full, always-current list, and "Edit shortcuts" there to remap any of them.

### Selecting text in agent terminals

Agents like Claude Code enable terminal mouse tracking, which means a normal click-drag is forwarded to the agent instead of creating a selection. **Hold Option while dragging** to bypass mouse tracking and make a selection you can copy — this is the same convention iTerm2, Terminal.app, and kitty use.

If the agent explicitly copies text for you (e.g. "copy this to my clipboard"), attn honors the terminal's OSC 52 clipboard sequence and writes to your Mac clipboard directly. No xclip / X server needed on the remote.

## Multi-agent coordination

Your agents can work as a team, not just side by side:

- **Shared workspace context** — a living brief agents read and keep current, so a new session orients itself without you re-explaining the task.
- **Delegation** — an agent can spin up a fresh, visible session with a focused brief (`attn delegate`) instead of cramming everything into one context.
- **Chief of staff** — promote one session to track and hand off work to the agents it delegates, through a dispatch + mailbox you watch from the dashboard.
- **In-app browser** — `attn browser open <url>` docks a real browser an agent can drive; log in once and it persists.

## How it works

1. The bundled attn runtime wraps your agent CLI and installs hooks (Claude) or reads PTY output (Codex, Copilot) to detect state — a classifier decides whether a stop means "done" or "waiting for you."
2. A background daemon tracks every session, local and SSH-remote, in one list; the desktop app connects over WebSocket for real-time updates.
3. `gh` polls PRs across all your authenticated GitHub hosts.

## Build from source

Requires Go 1.25+, Rust (stable), Node.js 20+, pnpm, and [Tauri prerequisites](https://v2.tauri.app/start/prerequisites/).

Source builds intentionally disable GitHub release update banners by default.

```bash
git clone https://github.com/victorarias/attn.git && cd attn
```

| Command | What it does |
|---|---|
| `make build` | Build the Go daemon binary |
| `make` | Install attn.app and launch it — the one-command inner loop |
| `make install` | Install the app bundle without launching (scripts / CI) |
| `make dev` | Install and launch the isolated `attn-dev.app` sibling — iterate without touching your live install |
| `make dist` | Create a DMG |
| `make test` | Go tests |

Developing attn while running attn? `make dev` gives you a fully isolated dev sibling (own bundle, data dir, and port) so rebuilds never touch your live copy. The full dev-loop, profile, and harness targets live in **[docs/profiles.md](docs/profiles.md)** and [AGENTS.md](AGENTS.md).

## Docs

| | |
|---|---|
| [Profiles](docs/profiles.md) | Run multiple isolated attn worlds side by side |
| [Release](docs/RELEASE.md) | Maintainer runbook |

## Status

Alpha — I use it every day. It's rough around the edges, but it works really well and it's genuinely powerful once you get going.

## A note about this project

I built attn because I needed it. Managing multiple agents was the bottleneck, not the coding itself.

I work full time, and I have 2 dogs and 4 cats at home. Free time is not abundant. attn is open source, but if you need changes I can't get to — **fork it**. That's encouraged, not rude.

**Issues are welcome**, but please be detailed:

- Describe what happened and what you expected.
- Include the tail of `~/.attn/daemon.log` from right after you reproduced the issue.
- For visual bugs, attach a screenshot.

Vague "it doesn't work" reports aren't actionable and will sit there forever.

I'm a friendly person and happy to help when I can. But don't be entitled about it — that's the one thing I can't deal with. Entitled behavior gets ignored, and if I'm having a bad day, maybe banned.

## Built with

[Tauri](https://tauri.app) / [React](https://react.dev) / [Go](https://go.dev) / [SQLite](https://sqlite.org)

## License

[GPL-3.0](LICENSE)
