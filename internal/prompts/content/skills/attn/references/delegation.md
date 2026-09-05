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

Attn delegation starts another agent with a focused brief and binds it to a
seed. A new seed stores the brief as its body; an existing seed keeps its body.
The session becomes the seed's tender. It reports on the log, and you reach it
with `attn agent msg <seed-id> "<note>"`. Read its work back with
`attn seed show <seed-id>`. See [garden.md](garden.md).

Follow-up: read the seed. Never park a blocking Monitor on attn activity: a
Monitor-blocked session reads as busy, which suppresses crew heartbeats and
auto-sleep. Monitors remain useful for external waits such as CI; they are a
helper, not an attn integration mechanism.

When you have what you needed from a delegate, close it:
`attn agent close <seed-id> -m "why it is done"`. You may close a session you
dispatched, and the reason is required. It is immediate, so read the seed first;
the reason lands there as a note, and the seed keeps its tender and its state.
See [converse-and-observe.md](converse-and-observe.md) for the rules and the
refusals. Leaving finished delegates running is the cost this avoids: each one
is a row the user has to judge later.

For a delegation that returns a durable plan, read the seed before continuing.
`attn seed show <seed-id>` renders its current artifacts. If one is a repository
path, pass that path and its repository in the follow-on brief and say that Git
remains canonical. Otherwise pass the canonical Notebook document. The next agent
edits that authority instead of creating a copy.

## Write the brief

The brief is the delegate's starting prompt. Write it for an agent without your
conversation: state the task and outcome, starting context, constraints and
scope, and how to verify and report completion. Follow `attn seed guide` for
body authoring. Use plain words and a sketch when it explains the work more
clearly ([showing.md](showing.md)).

For a plot child, name the parent sections, sibling results or artifacts the
delegate must read and why. Related seed bodies are not included automatically.
Check that the brief and those references give enough direction to start.

Prefer a file so you can read and revise the complete brief before dispatch:

    brief_file="$(mktemp "${TMPDIR:-/tmp}/attn-delegate.XXXXXX")"
    # Write the work prompt to this file.
    attn delegate --brief-file "$brief_file" --model <model>

Use `--brief <text>` for short tasks. To track the same work without starting
an agent, use `attn seed plant "<title>" -m "<brief>"`.

## Dispatch at an existing seed

Read the current body and log first:

    attn seed show <seed-id>
    attn delegate --brief-file "$brief_file" --plot <seed-id> --model <model>

`--plot` binds the existing seed, but the opening prompt uses the supplied brief;
it does not load or replace the stored body. Put the body's assignment and
required references in the brief. Keep it consistent with the body, updating
the body when the agreed task changes so a later handover gets that assignment.

The delegate becomes the seed's tender. A seed held by a live session refuses
dispatch before any worktree or agent is created, naming who holds it. When the
seed is a plot, `attn seed ready` in the delegate lists that plot's ready children.
`--name`, placement and worktree flags behave as usual.

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

Placement answers two independent questions. Each flag answers one of them.

### Where the pane appears

- no flag: the source session's workspace
- `--workspace <id>`: an existing workspace
- `--new-workspace`: a new workspace
- `--cwd <path>`: a new workspace at that directory (this flag also moves the
  checkout, see below)

Before creating a workspace, check whether one already fits. `attn list`
groups sessions by `workspace_id`; the labels and directories show the domain
workspaces the user keeps (code reviews, goalie rotation, triage). When the
task matches one, place it there:

    attn delegate --brief-file "$brief_file" --workspace <workspace-id> --model <model>

When delegating several independent items, route each to the workspace that
fits its domain instead of creating one per item.

`attn list` marks sessions in hidden workspaces with `workspace_muted: true`.
When the source session is the chief of staff, delegating into a muted
workspace unmutes it so the new agent shows in the sidebar. Ordinary
delegation leaves the mute state alone.

### Which checkout the agent edits

- no flag: a new worktree. Its repository is the source checkout's; with
  `--workspace`, it is the one that workspace's existing sessions are in, and
  delegation fails and asks for `--repo` when they span more than one.
- `--no-worktree`: the source session's checkout, whatever workspace the pane
  lands in.
- `--cwd <path>`: that directory; combined with the default worktree, a
  worktree of the repository at that path.

A workspace never chooses the checkout. `--workspace` with `--no-worktree`
moves only the pane, even when that workspace holds sessions from another
repository.

The worktree default applies even to read-only investigation or discussion.
Pass `--no-worktree` when the user asks to reuse the current checkout, the
delegation continues work already happening there, or repository or agent
guidance requires it.

A new worktree gets a generated branch; `--worktree <branch>` names it. It
starts from the repository's default branch (`origin/<default>` when that
exists, otherwise the local one), never from what the source or main checkout
has checked out. Pass `--from <ref>` to continue or stack on another branch.

When the source directory is not a Git repository (a crew home, for instance),
the flag-free default is refused rather than launching with no checkout. Any
placement flag or `--no-worktree` is taken as consent.

`attn delegate` refuses conflicting repository inputs before an agent starts
(`--cwd` and `--repo` in different repositories, or a `--worktree-path` that
already exists inside another one), and the error names both repositories.

When running outside the source session, add `--source-session <session-id>`.
Run `attn delegate --help` for the full flag list and the exact option
combinations.
