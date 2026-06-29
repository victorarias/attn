# Delegated-Agent Ticket Reporting

Load this reference when your initial task says your work is tracked — the chief
of staff delegated it to you and is following a ticket bound to your session.

When the chief delegates, attn opens a ticket bound to your session (you are its
assignee) and starts it in the working column. You self-report your work state,
which moves the ticket across the board so the chief can follow your progress
without interrupting you to ask. Report when you:

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

A report is a small payload. When you build a large durable artifact — a report, a
design doc, findings, often built with the user — write it into the Notebook and
reference it from your status comment instead of inlining it. Write to the Notebook
path the chief (or user) designated; if none was designated and the artifact
warrants one, ask the chief by reporting `needs_input` rather than inventing a
location.

Reporting does not stop or transfer your session. Continue working unless the task
is blocked or complete. Do not report ticket status for ordinary, untracked
delegation.
