# Usage Guide

This guide focuses on day-to-day workflows in attn. For installation and build steps, see the root README.

## Start a session

1. Open the app.
2. Click **+** (or press Cmd+N) to open the new session dialog.
3. Choose an agent:
   - Option-1: Codex
   - Option-2: Claude
4. (Optional) Toggle **Resume** with Option-3 to open the agent's resume picker.
5. Enter a path (or pick from recent locations) and press Enter.

## Resume sessions

When Resume is enabled, attn invokes the agent's native picker:

- Claude: `claude -r`
- Codex: `codex resume`

Use the picker to select the session to resume.

## Worktrees

- Cmd+Shift+N opens the new session dialog and prompts to create a worktree if the path is a repo.
- You can also create a worktree from the Repo Options panel after selecting a repo path.

## Fork a Claude session

- Cmd+Shift+F opens the fork dialog for the active session.
- Forking resumes the parent session context and (optionally) creates a worktree.

Note: Codex forking is not supported.

## Quick Find (terminal)

- Cmd+F opens Quick Find for the active session terminal.
- It extracts a searchable view of recent terminal output for fast copying of URLs, paths, and hashes.

## Terminal panel

- Cmd+` toggles the terminal panel.
- Cmd+T opens a new utility shell tab.
- Cmd+W closes the active session; Cmd+Shift+W closes the active utility tab.

## GitHub PR attention

attn can show PRs that need attention (review requested, CI failing, merge conflicts).
Make sure `gh auth status` is configured so attn can query PRs.

## CLI

Basic CLI usage:

```
attn
attn -s myproject
attn --resume
attn list
```

## Known limitations

- Only tested on macOS (Apple Silicon).
- Resume uses the agent's picker; attn does not yet store its own resume history.
- Codex session forking is not supported.
