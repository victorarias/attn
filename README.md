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
| [Codex](https://developers.openai.com/codex) | Hooks + classifier | No |
| [Copilot CLI](https://docs.github.com/en/copilot/using-github-copilot/using-github-copilot-in-the-command-line) | PTY heuristics + transcript classifier | No |
| OpenCode (opt-in bundled plugin) | Native events | Yes |

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
- **[Chief of staff](#put-a-manager-in-charge-of-your-agents)** — a manager for your agents. It helps shape the plan and keeps one coherent picture; you choose the handoffs.
- **In-app browser** — `attn browser open <url>` docks a real browser an agent can drive; log in once and it persists.

### Put a manager in charge of your agents

Running five agents shouldn't turn you into a full-time dispatcher. The chief of staff is the manager of your agent team: you work with it to shape focused missions, you decide what gets delegated, and the chief keeps the threads connected as the work develops.

Instead of reconciling five conversations yourself, you get one coherent picture. The chief follows what each agent reports, surfaces blockers and collisions, and tees up the decisions that need your judgment. It reduces the coordination load while you remain the person directing the work.

The chief is an awareness layer you work alongside, not a gatekeeper or end-to-end autopilot. You can open any delegated session, talk to the agent, and steer the work directly. The chief keeps the shared picture intact, so you can move between the big picture and the details without becoming the human message bus.

That continuity is not tied to one agent or harness. You might ask Fable in Claude Code to brainstorm and produce a plan, bring that handoff back through the chief, then have the chief prepare a follow-on delegation to GLM 5.2 in OpenCode. You choose each handoff; the chief preserves the context and artifacts between them so every stage can use the specialist that fits it best.

#### Give the chief an office

This home should eventually be built into attn and scaffolded for you. It is not yet, so for now we recommend creating a dedicated coordination folder rather than running your chief from a random code repository. This is where you describe how you work and keep the plans, responsibilities, and knowledge that span all your repos.

```text
chief-of-staff/
├── .gitignore            # ignore attn's machine state if this is a private repo
├── AGENTS.md             # instructions for Codex or another compatible agent
├── CLAUDE.md             # instructions for Claude Code
├── projects/
│   └── index.md          # active initiatives, outcomes, and why they matter
├── areas/
│   └── index.md          # ongoing responsibilities and standards to maintain
├── resources/
│   └── index.md          # durable reference material
├── archive/
│   └── index.md          # finished or inactive material
└── notebook/             # optional: keep attn's Notebook in the same home
```

Use `AGENTS.md` / `CLAUDE.md` as the chief's constitution. Capture durable preferences there: what good support looks like, how you make decisions, when to explore versus execute, how the chief should work alongside you, how it should propose and structure delegations, and where your important code and systems live. Keep current project state in the folders, not in the instructions.

Use the guidance file your chosen agent loads. If you use both Codex and Claude, keep both entry points. One simple shared setup is to put the common instructions in `AGENTS.md` and import them from `CLAUDE.md`:

```md
@AGENTS.md
```

A useful starter looks like this:

```md
# My chief of staff

This is my coordination and knowledge workspace. Help me manage my agents and
act as my thinking partner.

## Working relationship

- Reduce my cognitive load. Surface what matters and make the next move clear.
- Gather evidence before recommending action.
- When a choice is ambiguous, frame the decision and its tradeoffs.

## Delegation

- I decide every delegation; a goal is not approval to run it end to end.
- Propose a delegation, harness, and model when a separate specialist would help.
- Give each agent a clear outcome, context, constraints, and handoff.
- Track what the agents report and bring me blockers, collisions, and consequential decisions.

## Organization

- Read projects/index.md before discussing priorities or incoming work.
- projects/ holds time-bound efforts with a clear outcome.
- areas/ holds ongoing responsibilities and standards.
- resources/ holds reference material useful across projects.
- archive/ holds inactive material. Move completed projects here.
- Use a short index.md as the entry point for every folder.

## Boundaries

- Planning, research, decisions, and coordination live here.
- Implementation, tests, commits, and pull requests live in the owning repo.
```

This is a starting point, not a personality preset. Add your preferred tone, authorship boundaries, recurring responsibilities, delegation defaults, and location map. The more the file captures your working relationship, the more the chief feels like *your* manager rather than a generic agent.

If you want the whole system in one syncable or private git-backed folder, open **Settings → Notebook Folder** and point it at `chief-of-staff/notebook`. attn will keep its journal and durable cross-workspace knowledge there while your top-level folders remain the operating system you and the chief curate together. Add `notebook/.attn/` to `.gitignore`; it is machine state, not knowledge. Changing the Notebook Folder does not move existing notes, so move or sync them separately when adopting this layout later.

1. **Start your manager from its home.** Press **Cmd+T**, choose Claude or Codex, select the `chief-of-staff` folder, and turn **Chief of Staff** on. Or open an existing session in that folder and choose **Make chief of staff** from its session menu.
2. **Give it a mission.** Describe the outcome, the context, and what matters. For example:

   > We need to ship the new import flow this week. Help me understand what's already in flight, suggest how to break it down, and prepare delegation options. As the agents work, keep the threads connected and bring me the blockers, collisions, and decisions that need my judgment.

3. **Choose the handoffs.** The chief can recommend where a specialist would help and which harness and model fit the stage; you choose every delegation to start. Each one opens a visible session with a focused brief and a durable ticket carrying its progress, decisions, and artifacts.
4. **Work at any altitude.** Ask the chief for the state of the whole mission, or drop into an agent and work beside it. When something finishes, fails, or needs input, the chief brings back what changed and the natural next step.

The board and Notebook preserve that shared understanding across restarts, workspaces, and even a new chief session. Tomorrow, the chief still knows where everything stands.

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
