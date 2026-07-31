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
durable parent-child lineage. Every delegation binds a ticket to the delegated
session — the brief is its description, the session is its assignee — and you are a
participant on the ticket you created. That ticket is the channel in both
directions: the agent reports its work state onto it, and you reach the agent with
`attn ticket comment <ticket-id> -m "<note>"`. The chief of staff is also a
participant on every delegation ticket, whoever started it.

Follow-up: all runtimes receive the same ticket nudge when activity remains unread;
Claude may also arm a Monitor on `attn ticket inbox --watch` to consume updates
sooner.

For a delegation that returns a durable plan, read the ticket before
continuing. `attn ticket show <ticket-id>` lists its Notebook artifacts. If one is a
repository-reference card, pass its Git path, branch, and introducing commit in the
follow-on brief and say that Git remains canonical. Otherwise pass the canonical
Notebook path. The next agent edits that authority instead of creating a copy.

## Brief Workflow

Prefer a brief file so the task can be drafted and revised before submission:

    brief_file="$(mktemp "${TMPDIR:-/tmp}/attn-delegate.XXXXXX")"
    # Write a concise task, relevant context, constraints, and expected output.
    attn delegate --brief-file "$brief_file"

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

## Adopt an Existing Ticket

When the task already exists on the board, delegate that ticket instead of
copying its brief into a new delegation:

    attn delegate --ticket <ticket-id>

The existing ticket's description becomes the agent's initial task. Attn keeps
the ticket id and activity thread, binds it to the new session, and moves it to
`working`; it does not create another ticket. The description must be non-empty.
The session name defaults to the ticket title (shortened to the normal 16-character
limit), while `--name` still overrides it. Placement and worktree flags have their
normal meanings and the delegated session's resolved directory becomes the
ticket's current `cwd`.

An unassigned ticket can be adopted directly. A ticket assigned to a session is
also adopted directly when orphan reconciliation has stamped it, because no agent
is still working on it. A non-orphan assignee is protected from silent takeover:

    attn delegate --ticket <ticket-id> --confirm

Adoption clears the orphan stamp. The previous assignee remains a participant so
the reassignment appears in its ticket activity. `--ticket`, `--brief`, and
`--brief-file` are mutually exclusive task sources, and `--confirm` is valid only
with `--ticket`.

## Agent Selection

The source agent is used by default. Select another supported agent with:

    attn delegate --brief-file "$brief_file" --agent claude
    attn delegate --brief-file "$brief_file" --agent codex

Plugin agents work only when they declare delegated initial-prompt support.
Copilot delegation is currently unsupported.

`--model` and `--effort` pin the delegated agent's model and reasoning effort
for that delegation only; omitted, the agent uses its own defaults:

    attn delegate --brief-file "$brief_file" \
      --agent claude --model opus --effort high

`--model` takes an alias or a full model id. `--effort` takes the agent's
native levels (claude: low, medium, high, xhigh, max; codex: minimal, low,
medium, high, xhigh). Agents without a native mechanism (e.g. copilot) reject
these flags.

## Placement

For work involving a Git repository, `attn delegate` creates a new worktree by
default—even for read-only investigation or discussion. Use `--from` when the
task needs a particular starting branch; otherwise attn uses the repository's
default branch. Pass `--no-worktree` only when the user asks to reuse the current
checkout, the delegation clearly continues work already happening there, or more
specific repository or agent guidance requires it.

Before creating a new workspace, check whether an existing one already fits the
work. `attn list` returns sessions grouped by `workspace_id`; use the session
labels, directories, and workspace IDs to identify domain workspaces the user
already has (e.g. code reviews, goalie rotation, triage). When the delegated
task matches an existing workspace's domain, place it there with `--workspace`:

    attn delegate --brief-file "$brief_file" --workspace <workspace-id>

When delegating multiple independent items in parallel, route each agent to the
workspace that fits its domain rather than creating a new workspace per item.

If no existing workspace fits, use one of:

No placement flag — adds the session to the current workspace:

    attn delegate --brief-file "$brief_file"

Create a separate workspace using the source directory:

    attn delegate --brief-file "$brief_file" --new-workspace

Create a workspace at an existing directory:

    attn delegate --brief-file "$brief_file" --cwd /path/to/project

`attn list` marks sessions in hidden workspaces with `workspace_muted: true`.
When the source session is the chief of staff, delegating into a muted existing
workspace automatically unmutes it so the new agent is visible in the sidebar.
Ordinary delegation preserves the workspace's current mute state.

Git repositories get an isolated worktree with an automatically generated branch.
Use `--worktree` to choose the branch name explicitly. It combines with any
placement:

    # worktree in the current workspace
    attn delegate --brief-file "$brief_file" \
      --worktree feat/delegated-task

    # worktree in an existing workspace
    attn delegate --brief-file "$brief_file" \
      --workspace <workspace-id> --worktree feat/delegated-task

    # worktree in a new workspace
    attn delegate --brief-file "$brief_file" \
      --new-workspace --worktree feat/delegated-task

    # worktree of the repo at an existing directory
    attn delegate --brief-file "$brief_file" \
      --cwd /path/to/project --worktree feat/delegated-task

Worktree options:

- `--repo <path>` chooses the main repository. It defaults to the repository the
  target's existing sessions are in — the session you delegate alongside, not the
  workspace's recorded directory. Delegation fails and asks for `--repo` when
  those sessions span more than one repository.
- `--from <ref>` chooses the starting branch or ref. Delegating into an existing
  workspace starts from the repository's default branch — `origin/<default>` when
  that exists, otherwise the local one — because the workspace's sessions may sit
  in several worktrees of that repository and none of their branches is a better
  answer than the others. It does not depend on what the main checkout currently
  has checked out. Pass `--from` to start somewhere else. Other placements still
  start from the directory they are based on.
- `--worktree-path <path>` chooses an explicit worktree location.

When running outside the source session, add `--source-session <session-id>`.
Run `attn delegate --help` for the exact option combinations.
