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
durable parent-child lineage or require you to monitor the new agent.

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

## Agent Selection

The source agent is used by default. Select another supported agent with:

    "$ATTN_WRAPPER_PATH" delegate --brief-file "$brief_file" --agent claude
    "$ATTN_WRAPPER_PATH" delegate --brief-file "$brief_file" --agent codex

Plugin agents work only when they declare delegated initial-prompt support.
Copilot delegation is currently unsupported.

## Placement

No placement flag adds the delegated session to the current workspace:

    "$ATTN_WRAPPER_PATH" delegate --brief-file "$brief_file"

Create a separate workspace using the source directory:

    "$ATTN_WRAPPER_PATH" delegate --brief-file "$brief_file" --new-workspace

Create a workspace at an existing directory:

    "$ATTN_WRAPPER_PATH" delegate --brief-file "$brief_file" --cwd /path/to/project

Join an existing workspace:

    "$ATTN_WRAPPER_PATH" delegate --brief-file "$brief_file" --workspace <workspace-id>

Create an isolated worktree and workspace:

    "$ATTN_WRAPPER_PATH" delegate --brief-file "$brief_file" \
      --worktree feat/delegated-task

Worktree options:

- `--repo <path>` chooses the main repository.
- `--from <ref>` chooses the starting branch or ref.
- `--worktree-path <path>` chooses an explicit worktree location.

When running outside the source session, add `--source-session <session-id>`.
Run `attn delegate --help` for the exact option combinations.
