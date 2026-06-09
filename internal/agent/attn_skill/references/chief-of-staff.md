# Chief of Staff Dispatches

Load this reference when your session is the chief of staff, or when your
initial task says the chief of staff is tracking the work.

## As Chief of Staff

Delegate with the normal `attn delegate` workflow. attn tracks the new session
automatically only when the current source session holds the chief-of-staff
role.

Review your dispatched work with:

    "$ATTN_WRAPPER_PATH" dispatch list

Use the target session's live status and latest report to decide whether to
wait, inspect its work, answer a blocker, or give the user a summary. A report
is an agent update, not proof that the work is correct; verify important
results before relying on them.

Do not create reports on behalf of delegated agents or repeatedly interrupt
working agents just to request status.

## As a Dispatched Agent

If the initial task says the work is tracked, report when you:

- reach a meaningful milestone
- need input or are blocked
- finish the requested work

Keep a short report concrete: outcome, evidence, and next action.

    "$ATTN_WRAPPER_PATH" dispatch report --message \
      "Implemented the parser and tests pass. Next: review the error wording."

For a longer report, write it to a file and submit the file:

    "$ATTN_WRAPPER_PATH" dispatch report --file /tmp/dispatch-report.md

Reporting does not stop or transfer your session. Continue working unless the
task is blocked or complete. Do not use dispatch reporting for ordinary,
untracked delegation.
