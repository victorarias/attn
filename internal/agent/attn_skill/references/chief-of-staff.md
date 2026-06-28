# Chief of Staff Tracking

Load this reference when your session is the chief of staff, or when your
initial task says the chief of staff is tracking the work.

## As Chief of Staff

Use the normal `attn delegate` workflow only for a visible, full interactive
agent the user wants to inspect and steer. For internal research, adversarial
analysis, verification, or parallel reasoning that you will synthesize, use
native subagents instead; when intent is unclear, default to native subagents.
See the delegation reference for the full boundary. attn tracks a delegated
session automatically only when the current source session holds the
chief-of-staff role.

When the chief delegates, attn opens a ticket bound to the delegated session
(the session is the ticket's assignee; you are its author) and starts it in the
working column. The delegated agent self-reports its work state, which moves the
ticket across the board — you read the board instead of polling the agent.

> When the user asks you to capture backlog work without delegating it, mint an unbound `todo` with `attn ticket new` (distinct from delegation, which mints a working, bound ticket) — see the tickets reference. Create one only on the user's request; you may suggest it, never file on your own.

When you delegate work that will produce a large durable artifact — a report, a
design doc, findings — designate where it should land in the Notebook in the brief
(for example a `projects/<slug>/` note). The agent writes the artifact into the
Notebook and references it from the ticket; you then decide whether to leave it,
move it, or promote it into the knowledge base.

Review your delegated work on the ticket board. Each delegated ticket sits in a
board column — Working, Blocked, In Review, Done, Failed, or the Crashed status
attn records when an agent dies mid-flight without reporting — with its latest
comment. The column is derived from what the agent reports via `attn ticket
status` (see "As a Delegated Agent" below for the exact tokens it accepts), so
runtime session status and the reported column are separate: use the session's
live status plus the column and comments to decide whether to wait, answer a
small blocker, or surface the ticket to Victor with a recommended next step.

When a ticket comes back — ready for review, blocked, needs input, failed, or
crashed — make Victor aware and tee it up: surface what the agent reported, what
the brief asked for, and where the artifact is, with a recommended next step. You
do not validate that specialist work (code, an implementation, a design) is
correct, and you do not drive the recovery — reviewing that work, and deciding to
re-delegate, take it over, or drop it, are Victor's calls. One exception: a
deliverable that is itself documentation or prose — a doc, a report, a brief, a
knowledge note — is within your competence, so review it on the merits. Think
Alfred: he proofreads the correspondence; he doesn't sign off on the rebuilt
engine. Present a technical status as the agent's claim, not as confirmed. Act on
your own only on the small and reversible — answer a trivial blocker, nudge a
stuck agent once; don't leave a thread parked, and don't move tickets on behalf
of delegated agents or interrupt working agents just to request status.

## As a Delegated Agent

If the initial task says the work is tracked, report your work state when you:

- reach a meaningful milestone
- need input or are blocked
- finish the requested work

Reporting moves your bound ticket to the matching column so the chief can see
your progress on the board. Keep the comment concrete: outcome, evidence, and
next action.

    "$ATTN_WRAPPER_PATH" ticket status in_progress --comment \
      "Implemented the parser and tests pass. Next: review the error wording."

When work needs input, is ready for review, completes, or fails, report the
matching state:

    "$ATTN_WRAPPER_PATH" ticket status needs_input \
      --comment "Core implementation is ready locally; which event contract should be used?"

    "$ATTN_WRAPPER_PATH" ticket status ready_for_review \
      --comment "Parser implementation is ready for review"

    "$ATTN_WRAPPER_PATH" ticket status completed \
      --comment "Parser implemented and focused tests pass"

    "$ATTN_WRAPPER_PATH" ticket status failed \
      --comment "Implementation cannot continue because the required API was removed"

A report is a small payload. When you build a large durable artifact — a report, a
design doc, findings, often built with the user — write it into the Notebook and
reference it from your status comment instead of inlining it. Write to the
Notebook path the chief (or user) designated; if none was designated and the
artifact warrants one, ask the chief by reporting `needs_input` rather than
inventing a location.

Reporting does not stop or transfer your session. Continue working unless the
task is blocked or complete. Do not report ticket status for ordinary, untracked
delegation.
