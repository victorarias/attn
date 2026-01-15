# Configuration

attn stores most settings in the app. The sections below are the key knobs and how they map to behavior.

## In-app settings

- **Projects Directory**
  - Used for repo discovery and worktree creation.
  - Must be an absolute path.

- **Agent Executable Overrides**
  - Claude executable path (defaults to `claude`).
  - Codex executable path (defaults to `codex`).
  - Useful if you install from custom locations or use wrappers.

- **GitHub / PRs**
  - attn uses the `gh` CLI for PRs.
  - Authenticate with `gh auth status` / `gh auth login`.

- **UI Scale**
  - Adjust font size with Cmd+= / Cmd- / Cmd+0.

## Environment variables

These are mainly used by the app wrapper when launching agents:

- `ATTN_INSIDE_APP=1` – internal flag when launched from the app.
- `ATTN_AGENT=codex|claude` – selects which agent to run.
- `ATTN_SESSION_ID=<uuid>` – forces a specific session ID (used for tracking).
- `ATTN_CLAUDE_EXECUTABLE=/path/to/claude` – overrides Claude CLI path.
- `ATTN_CODEX_EXECUTABLE=/path/to/codex` – overrides Codex CLI path.

You generally do not need to set these manually unless you are debugging or integrating.
