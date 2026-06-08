# Review Loops

Use review-loop commands only when the user explicitly asks to start, monitor,
or answer a review loop. The loop is autonomous; its findings do not authorize
you to make further code changes unless the user asks.

## Start

Commit the implementation first so the reviewer evaluates a stable snapshot.
Write a concise JSON handoff containing known context such as:

```json
{
  "summary": "What changed.",
  "reasoning_context": "Why the change was made.",
  "plan_file": "docs/plans/example.md",
  "constraints": ["Relevant constraint"]
}
```

Then run:

    "$ATTN_WRAPPER_PATH" review-loop start \
      --prompt "<review prompt>" \
      --iterations <n> \
      --handoff-file <path>

Use two iterations when the user does not specify a limit. Pass
`--session <session-id>` only when the current session is not the target.

After starting, stop unless the user explicitly asked you to monitor or
continue other work.

## Monitor

    "$ATTN_WRAPPER_PATH" review-loop show --loop <loop-id>

Or:

    "$ATTN_WRAPPER_PATH" review-loop show --session <session-id>

Poll at modest intervals. Report status changes, pending questions, final
output, or errors without acting on findings automatically.

## Answer A Pending Question

Answer only with the user's supplied answer or when the user explicitly tells
you to choose:

    "$ATTN_WRAPPER_PATH" review-loop answer \
      --loop <loop-id> \
      --interaction <interaction-id> \
      --answer "<answer>"
