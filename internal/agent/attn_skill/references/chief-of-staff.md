# Chief of Staff Dispatches

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

When you delegate work that will produce a large durable artifact — a report, a
design doc, findings — designate where it should land in the Notebook in the brief
(for example a `projects/<slug>/` note). The dispatched agent hands the artifact off
with `dispatch handoff` and reports back a reference; you then decide whether to
leave it, move it, or promote it into the knowledge base.

Review your dispatched work with:

    "$ATTN_WRAPPER_PATH" dispatch list

Runtime status and delegated work state are separate. Use the target session's
live status plus the latest report and its structured `work_state`, `next_actor`,
`next_action`, actionable request, artifact, and verification to decide whether
to wait, inspect its work, answer a blocker, or give the user a summary.

Resolve the dispatch's one active decision request with:

    "$ATTN_WRAPPER_PATH" dispatch resolve \
      --dispatch <id> \
      --response "Use AisNoOperationV1." \
      --link "https://external-system.example/decision"

The response is stored on the dispatch and becomes available to the delegated
agent through `dispatch status`.

Send durable instructions to a delegated agent with:

    "$ATTN_WRAPPER_PATH" dispatch message \
      --dispatch <id> \
      --message "Rebase onto main, rerun the focused tests, and report conflicts."

Messages are mailbox entries, not terminal input. Do not assume a working agent
has seen one immediately. The dashboard shows unread mail and lets the user
explicitly wake an idle agent with a fixed inbox-check prompt.

Inspect sent messages, including read and acknowledgement state, with:

    "$ATTN_WRAPPER_PATH" dispatch messages --dispatch <id>

A report is not proof that the work is correct; it is an agent update. Verify
important results before relying on it.

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

A report is a small payload. When you build a large durable artifact — a report, a
design doc, findings, often built with the user — write it into the Notebook and
report the reference instead of inlining it:

    "$ATTN_WRAPPER_PATH" dispatch handoff \
      --file /tmp/auth-audit.md \
      --to projects/auth-audit/findings.md \
      --message "Auth audit complete; full findings in the Notebook."

`--to` is the Notebook path the chief (or user) designated; the daemon writes the
artifact there and the reference travels back in your report. If no destination was
designated and the artifact warrants one, ask the chief in a report rather than
inventing a location. Add `--coordination-file <json>` to a terminal handoff just as
you would to a report.

For actionable coordination, keep the narrative and attach a JSON envelope:

```json
{
  "report_type": "blocker",
  "summary": "Core freshness gate implemented locally",
  "work_state": "needs_input",
  "next_actor": "team",
  "next_action": "Decide the AisNoOperationV1 event contract",
  "remaining_scope": ["Emit the event", "Add the integration test"],
  "constraints": ["uncommitted", "no push", "no PR"],
  "request": {
    "question": "Should the worker emit AisNoOperationV1?",
    "recommendation": "Use AisNoOperationV1",
    "consequence": "Event emission remains blocked",
    "expected_responder": "team",
    "status": "pending"
  },
  "artifact": {
    "identity": "dirty:<stable-status-hash>",
    "branch": "feat/example",
    "dirty": true
  },
  "verification": [{
    "actor": "agent",
    "target": "go test ./internal/feature",
    "result": "passed",
    "timestamp": "2026-06-09T18:00:00Z",
    "artifact_identity": "dirty:<stable-status-hash>"
  }]
}
```

Submit it with:

    "$ATTN_WRAPPER_PATH" dispatch report \
      --message "Core implementation is ready; event emission needs a decision." \
      --coordination-file /tmp/dispatch-coordination.json

After requesting a decision, read the durable chief response with:

    "$ATTN_WRAPPER_PATH" dispatch status

Before reporting completion or entering a waiting state, check unread mail:

    "$ATTN_WRAPPER_PATH" dispatch inbox --unread

For each message, mark it read when you start handling it:

    "$ATTN_WRAPPER_PATH" dispatch read --message-id <id>

After acting on it, acknowledge it, optionally with a concise result:

    "$ATTN_WRAPPER_PATH" dispatch ack \
      --message-id <id> \
      --message "Rebased cleanly; focused tests pass."

Reporting does not stop or transfer your session. Continue working unless the
task is blocked or complete. Do not use dispatch reporting for ordinary,
untracked delegation.
