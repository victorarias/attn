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
durable parent-child lineage. Every delegation binds a **seed** to the delegated
session — the brief is its body, the session is its tender — and the seed is the
channel in both directions: the agent writes what happened onto its log, and you
reach the agent by seed id with `attn agent msg <seed-id> -m "<note>"`. Read the
work back with `attn seed show <seed-id>`. See [garden.md](garden.md).

Follow-up: read the seed. Never park a blocking Monitor on attn activity: a
Monitor-blocked session reads as busy, which suppresses crew heartbeats and
auto-sleep. Monitors remain useful for external waits such as CI; they are a
helper, not an attn integration mechanism.

For a delegation that returns a durable plan, read the seed before continuing.
`attn seed show <seed-id>` renders its current artifacts. If one is a repository
path, pass that path and its repository in the follow-on brief and say that Git
remains canonical. Otherwise pass the canonical Notebook document. The next agent
edits that authority instead of creating a copy.

## Brief Workflow

Prefer a brief file so the task can be drafted and revised before submission:

    brief_file="$(mktemp "${TMPDIR:-/tmp}/attn-delegate.XXXXXX")"
    # Write a concise task, relevant context, constraints, and expected output.
    attn delegate --brief-file "$brief_file" --model <model>

The brief should let the delegated agent start immediately. Include:

1. the concrete objective
2. relevant paths, decisions, or evidence
3. constraints and explicit non-goals
4. the expected deliverable or stopping condition

A delegation brief *is* the seed's body, so the fuller craft in
[garden.md](garden.md) applies here too — write the objective as a stop
condition, give a verification contract, and let the shape bend by deliverable
type. Write it to be easy to read without losing precision — plain words,
short paragraphs, sketches where they say more ([showing.md](showing.md)).

Use `--brief <text>` only for short, simple tasks.

> The same brief is the seed's body. To capture work *without* delegating, plant a seed — `attn seed plant "<title>" -m "<brief>"` (see [garden.md](garden.md)); do this only when the user asks.

## Dispatch at an Existing Seed

When the work is already planted, dispatch at it instead of planting a duplicate:

    attn delegate --brief-file "$brief_file" --plot <seed-id> --model <model>

The delegate binds that seed rather than a new one, and launches knowing it.
Aimed at a **crown**, a flag-free `attn seed ready` inside the delegate answers
with that plot's ready seeds. `--name`, placement, and worktree flags behave as
usual.

## Agent Selection

The source agent is used by default; `--agent` selects another supported one.
Plugin agents work only when they declare delegated initial-prompt support.
Copilot delegation is currently unsupported.

`--model` is required and pins the delegated agent's model for that delegation
only. `--effort` takes the agent's native levels (claude: low, medium, high,
xhigh, max; codex: minimal, low, medium, high, xhigh) and defaults to medium.
Agents without a native effort mechanism reject an explicit `--effort`; the
default is not applied to them.

## Placement

For work involving a Git repository, `attn delegate` creates a new worktree by
default—even for read-only investigation or discussion. Use `--from` when the
task needs a particular starting branch; otherwise attn uses the repository's
default branch. Pass `--no-worktree` only when the user asks to reuse the current
checkout, the delegation clearly continues work already happening there, or more
specific repository or agent guidance requires it.

When the source directory is not a Git repository (a crew home, for instance),
the flag-free default is refused rather than launching the delegate with no
checkout. Place it with `--cwd <repo>` or `--workspace <id>`, or pass
`--no-worktree` to delegate without one. Any explicit placement flag is taken as
consent, so none of them is refused.

Before creating a new workspace, check whether an existing one already fits the
work. `attn list` returns sessions grouped by `workspace_id`; use the session
labels, directories, and workspace IDs to identify domain workspaces the user
already has (e.g. code reviews, goalie rotation, triage). When the delegated
task matches an existing workspace's domain, place it there with `--workspace`:

    attn delegate --brief-file "$brief_file" --workspace <workspace-id> --model <model>

When delegating multiple independent items in parallel, route each agent to the
workspace that fits its domain rather than creating a new workspace per item.

If no existing workspace fits: no placement flag adds the session to the
current workspace, `--new-workspace` creates a separate workspace from the
source directory, and `--cwd` creates one at an existing directory.

`attn list` marks sessions in hidden workspaces with `workspace_muted: true`.
When the source session is the chief of staff, delegating into a muted existing
workspace automatically unmutes it so the new agent is visible in the sidebar.
Ordinary delegation preserves the workspace's current mute state.

Git repositories get an isolated worktree with an automatically generated
branch; `--worktree` chooses the branch name explicitly and combines with any
placement. Two worktree defaults matter beyond what `attn delegate --help`
states:

- `--repo` defaults to the repository the target's *existing sessions* are in —
  the session you delegate alongside, not the workspace's recorded directory.
  Delegation fails and asks for `--repo` when those sessions span more than one
  repository.
- Without `--from`, every placement starts from the repository's default branch
  — `origin/<default>` when that exists, otherwise the local one. It never
  depends on what the source or main checkout currently has checked out. Pass
  `--from` to deliberately continue or stack on another branch.

When running outside the source session, add `--source-session <session-id>`.
Run `attn delegate --help` for the full flag list and the exact option
combinations.
