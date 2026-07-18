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

When work needs input, is ready for review, clearly completes, or fails, report
the matching state:

    "$ATTN_WRAPPER_PATH" ticket status needs_input \
      --comment "Core implementation is ready locally; which event contract should be used?"

    "$ATTN_WRAPPER_PATH" ticket status ready_for_review \
      --comment "Parser implementation is ready for review"

    "$ATTN_WRAPPER_PATH" ticket status completed \
      --comment "The requested PR merged and no follow-up remains"

    "$ATTN_WRAPPER_PATH" ticket status failed \
      --comment "Implementation cannot continue because the required API was removed"

Reporting moves your bound ticket to the matching column so the chief sees your
progress on the board. Keep the comment concrete: outcome, evidence, and next
action.

Use `completed` when strong terminal evidence shows the requested outcome is done
and no review or decision remains — for example, Victor accepted the work, the
requested PR merged, or an equivalent objective completion signal is clear. A
separate confirmation ritual is unnecessary when that evidence already exists. If
you merely finished implementation but acceptance, review, or another decision is
still pending, use `ready_for_review`.

To move a ticket other than your own, add `--ticket <id>` (any ticket, no
ownership gate) — same as `ticket comment <id>` reaching across tickets.

A report is a small payload. Put large durable reasoning in an artifact and reference
it from the status comment rather than inlining it. For plans and designs, use the
canonical-source workflow below. For other prose, write to a path the chief or user
designated; if none was designated and the location materially changes ownership,
ask by reporting `needs_input`.

Reporting does not stop or transfer your session. Continue working unless the task
is blocked or complete. Do not report ticket status for ordinary, untracked
delegation.

## Hand Over Durable Artifacts

When tracked work produces a Markdown plan or design that must outlive this
session, let attn choose its one canonical home:

    "$ATTN_WRAPPER_PATH" ticket attach-plan \
      --file docs/plans/design.md \
      --state ready_for_review \
      --comment "The design is ready."

The default `--authority auto` checks the applicable repository convention. In a
monorepo, pass `--scope <affected-component>` so an unrelated sibling's docs do not
decide ownership. Explicit user and repository guidance wins; use `--authority
repository` or `--authority notebook` to record that choice when auto-detection is
not the right signal.

- If that scope keeps plans or designs in Git, commit the plan first. The repository
  file remains canonical and attn attaches a Notebook reference containing its path,
  branch, and introducing commit. When migrating an older attachment, attn retires
  the old Notebook copy only if it is byte-identical; a divergent copy is preserved
  for explicit reconciliation.
- Otherwise, attn copies the plan into the ticket's Notebook directory, verifies the
  copy, and retires the untracked staging source. It refuses to delete a tracked file.

Use ordinary `ticket attach` for other artifact types and for deliberate snapshots;
it copies each source into the Notebook and does not retire it.

After success, edit only the reported canonical source: the Git file named by a
repository reference, or the returned Notebook file. When you make a meaningful
edit, rename, or deletion, report it with `ticket status --comment` or `ticket
comment` so the chief knows to re-read the ticket.
