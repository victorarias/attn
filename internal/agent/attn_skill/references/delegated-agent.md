# Delegated-Agent Guidance

Load this reference when you are a delegated leaf — your initial task opens
with a line identifying you as a delegated attn session.

## You Are A Leaf, Not A Coordinator

Do the assigned work in this session. A subagent is always a native runtime
subagent, including when the user says to delegate or dispatch subagents. An
explicit request from the user steering this session selects attn delegation;
otherwise, use native subagents.

An attn delegation creates a visible agent session the user can inspect,
converse with, and steer directly. Native subagents report to you.

## If Your Work Is Tracked, Report Your State

If the chief of staff delegated this and is following a ticket bound to your
session, self-report your work state so the ticket moves across the board and
the chief can follow your progress without interrupting you to ask. When the
chief delegates, attn opens that ticket (you are its assignee) and starts it in
the working column. Report when you:

- reach a meaningful milestone
- need input or are blocked
- finish the requested work

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

Reporting moves your bound ticket to the matching column so the chief sees your
progress on the board. Keep the comment concrete: outcome, evidence, and next
action.

To move a ticket other than your own, add `--ticket <id>` (any ticket, no
ownership gate) — same as `ticket comment <id>` reaching across tickets.

A report is a small payload. When you build a large durable artifact — a report, a
design doc, findings, often built with the user — write it into the Notebook and
reference it from your status comment instead of inlining it. Write to the Notebook
path the chief (or user) designated; if none was designated and the artifact
warrants one, ask the chief by reporting `needs_input` rather than inventing a
location.

Reporting does not stop or transfer your session. Continue working unless the task
is blocked or complete. Do not report ticket status for ordinary, untracked
delegation.

## Hand Over Durable Artifacts

When tracked work produces a plan, design, or other artifact that must
outlive this session, hand it over to the ticket:

    "$ATTN_WRAPPER_PATH" ticket attach \
      --file docs/plans/design.md \
      --file docs/plans/rollout.md \
      --state ready_for_review \
      --comment "The plan and decision context are ready."

`--file` is repeatable. `--state` and `--comment` are optional. The command copies
the files into the ticket's visible Notebook directory, records one durable
attach event, and returns the canonical paths. A matching retry returns the same
receipt; a same-name file with different bytes is preserved and must be renamed
before retrying.

After success, edit the returned files directly. They are ordinary files
and the filesystem is the current artifact index. When you make a meaningful edit,
rename, or deletion, report it with `ticket status --comment` or `ticket comment`
so the chief knows to re-read the ticket.
