# Delegation

This file covers attn delegation mechanics. Confirm your role in SKILL.md first;
delegated agents also read [delegated-agent.md](delegated-agent.md).

A subagent is always a native runtime subagent, including in phrases such as
"delegate subagents" and "dispatch subagents."

Native subagents report to the calling agent. Attn delegation creates a visible,
full interactive agent session for the user: an agent they can inspect, converse
with, and steer directly. An explicit user request selects attn delegation;
otherwise, use native subagents.

Interpret the requested object first:

- "delegate this problem" or "delegate this to an agent" means attn delegation
- "dispatch an agent" means attn delegation
- "use a subagent" means a native subagent
- "delegate subagents to review" means native subagents
- "dispatch subagents to investigate" means native subagents

Attn delegation starts another agent with a focused brief; it does not create
durable parent-child lineage. If you are the chief of staff, attn binds a ticket
to the delegated session and your system-prompt guidance covers the follow-up path:
all runtimes receive the same ticket nudge when activity remains unread; Claude may
also arm a Monitor on `attn ticket inbox --watch` to consume updates sooner. Ordinary
delegation needs none of that.

For a chief-tracked delegation that returns a durable plan, read the ticket before
continuing: `attn ticket show <ticket-id>` lists the Markdown files currently in
the ticket's Notebook directory. Pass those canonical paths in follow-on briefs so
the next agent updates the same plan instead of creating a conversational copy.

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

A delegation brief *is* a ticket's description, so the fuller craft in
[tickets.md](tickets.md) applies here too — write the objective as a stop
condition, give a verification contract, and let the shape bend by deliverable
type.

Use `--brief <text>` only for short, simple tasks.

> The same brief is a ticket's description. To capture a backlog item *without* delegating — an unbound `todo` — use `attn ticket new` (see [tickets.md](tickets.md)); do this only when the user asks.

## Agent Selection

The source agent is used by default. Select another supported agent with:

    "$ATTN_WRAPPER_PATH" delegate --brief-file "$brief_file" --agent claude
    "$ATTN_WRAPPER_PATH" delegate --brief-file "$brief_file" --agent codex

Plugin agents work only when they declare delegated initial-prompt support.
Copilot delegation is currently unsupported.

`--model` and `--effort` pin the delegated agent's model and reasoning effort
for that delegation only; omitted, the agent uses its own defaults:

    "$ATTN_WRAPPER_PATH" delegate --brief-file "$brief_file" \
      --agent claude --model opus --effort high

`--model` takes an alias or a full model id. `--effort` takes the agent's
native levels (claude: low, medium, high, xhigh, max; codex: minimal, low,
medium, high, xhigh). Agents without a native mechanism (e.g. copilot) reject
these flags.

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

    # worktree of the repo at an existing directory
    "$ATTN_WRAPPER_PATH" delegate --brief-file "$brief_file" \
      --cwd /path/to/project --worktree feat/delegated-task

Worktree options:

- `--repo <path>` chooses the main repository (defaults to the workspace's repo).
- `--from <ref>` chooses the starting branch or ref.
- `--worktree-path <path>` chooses an explicit worktree location.

When running outside the source session, add `--source-session <session-id>`.
Run `attn delegate --help` for the exact option combinations.
