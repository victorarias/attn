# Delegation

Attn delegation creates a visible, full interactive agent session for the user.
Use it when the user wants another agent they can inspect, converse with, and
steer directly while you continue coordinating the wider task.

Do not use attn delegation as an internal parallel-reasoning mechanism. For
research, adversarial analysis, verification, or other work that you alone will
synthesize for the user, use your native subagent or multi-agent tools instead.
Those workers are implementation details of your response; they should not
create attn sessions or appear in the user's workspace.

When the user's intent is unclear, default to native subagents. Use attn only
when the user explicitly asks for delegation, an interactive agent, a separate
workspace/session, or a collaborator they can steer themselves.

Attn delegation starts another agent with a focused brief; it does not create
durable parent-child lineage. If you are the chief of staff, attn binds a ticket
to the delegated session and your system-prompt guidance covers how you follow it
(arming a Monitor on `attn ticket inbox --watch`); ordinary delegation needs none
of that.

## Brief Workflow

Prefer a brief file so the task can be drafted and revised before submission:

    brief_file="$(mktemp "${TMPDIR:-/tmp}/attn-delegate.XXXXXX")"
    # Write a concise task, relevant context, constraints, and expected output.
    "$ATTN_WRAPPER_PATH" delegate --brief-file "$brief_file"

The brief should let the delegated agent start immediately. Include:

1. the concrete objective
2. relevant paths, decisions, or evidence
3. constraints and explicit non-goals
4. the expected deliverable or stopping condition

Use `--brief <text>` only for short, simple tasks.

> The same brief is a ticket's description. To capture a backlog item *without* delegating — an unbound `todo` — use `attn ticket new` (see [tickets.md](tickets.md)); do this only when the user asks.

## Agent Selection

The source agent is used by default. Select another supported agent with:

    "$ATTN_WRAPPER_PATH" delegate --brief-file "$brief_file" --agent claude
    "$ATTN_WRAPPER_PATH" delegate --brief-file "$brief_file" --agent codex

Plugin agents work only when they declare delegated initial-prompt support.
Copilot delegation is currently unsupported.

## Placement

Before creating a new workspace, check whether an existing one already fits the
work. `attn list` returns sessions grouped by `workspace_id`; use the session
labels, directories, and workspace IDs to identify domain workspaces the user
already has (e.g. code reviews, goalie rotation, triage). When the delegated
task matches an existing workspace's domain, place it there with `--workspace`:

    "$ATTN_WRAPPER_PATH" delegate --brief-file "$brief_file" --workspace <workspace-id>

When delegating multiple independent items in parallel, route each agent to the
workspace that fits its domain rather than creating a new workspace per item.

If no existing workspace fits, use one of:

No placement flag — adds the session to the current workspace:

    "$ATTN_WRAPPER_PATH" delegate --brief-file "$brief_file"

Create a separate workspace using the source directory:

    "$ATTN_WRAPPER_PATH" delegate --brief-file "$brief_file" --new-workspace

Create a workspace at an existing directory:

    "$ATTN_WRAPPER_PATH" delegate --brief-file "$brief_file" --cwd /path/to/project

`attn list` marks sessions in hidden workspaces with `workspace_muted: true`.
When the source session is the chief of staff, delegating into a muted existing
workspace automatically unmutes it so the new agent is visible in the sidebar.
Ordinary delegation preserves the workspace's current mute state.

`--worktree` creates an isolated git worktree for branch isolation. It combines
with any placement:

    # worktree in the current workspace
    "$ATTN_WRAPPER_PATH" delegate --brief-file "$brief_file" \
      --worktree feat/delegated-task

    # worktree in an existing workspace
    "$ATTN_WRAPPER_PATH" delegate --brief-file "$brief_file" \
      --workspace <workspace-id> --worktree feat/delegated-task

    # worktree in a new workspace
    "$ATTN_WRAPPER_PATH" delegate --brief-file "$brief_file" \
      --new-workspace --worktree feat/delegated-task

Worktree options:

- `--repo <path>` chooses the main repository (defaults to the workspace's repo).
- `--from <ref>` chooses the starting branch or ref.
- `--worktree-path <path>` chooses an explicit worktree location.

When running outside the source session, add `--source-session <session-id>`.
Run `attn delegate --help` for the exact option combinations.
