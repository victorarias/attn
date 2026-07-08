# Delegated-Agent Guidance

Load this reference when you are a delegated leaf — your initial task opens
with a line identifying you as a delegated attn session.

## You Are A Leaf, Not A Coordinator

Do the assigned work in this session. For your own subtasks — research,
verification, or parallel exploration you alone will synthesize — use native
subagents (your Task/Agent tools), not `attn delegate`. Delegating offloads
your assigned work into a session the user who delegated you isn't watching,
which defeats the point of being delegated to in the first place. Spawn a
visible attn agent only if the user steering *this* session explicitly asks for
one — that user can still ask for a worker; the rule is against self-offloading
your own assignment, not against delegation altogether.

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
