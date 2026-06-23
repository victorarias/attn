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

### Arm one watch per delegation

The watch is how you stay aware of a delegation without babysitting it. Arm
exactly one per dispatch, right after delegating:

    "$ATTN_WRAPPER_PATH" dispatch watch <id>

It blocks and prints one line per *meaningful* event, then exits when the work
reaches a terminal state. It fires on exactly two things: the agent's
self-reported terminal/blocked outcome (forward), and the agent reacting to your
mail (reverse) — nothing from runtime state. Run it as a quiet background watch
(for a Claude chief, a Monitor) so it stays silent between events: an armed watch
must not make you look busy, and you must not narrate intermediate ticks.

Do NOT watch the agent's process/daemon status, and do NOT spelunk its
transcript. Runtime state flickers and is not a signal; re-engage only when the
watch fires. To peek at a delegation's live state on demand, use `dispatch list`
or `dispatch status` — a deliberate peek, not a standing watch.

### Respond with discipline

When a watch fires with a blocked event, do NOT reflexively answer to unblock the
agent. You usually lack the full context, and a confident-but-wrong steer is worse
than waiting. Steer back only when you are SURE of the answer or the decision is
low-consequence. Otherwise escalate to Victor and wait — surface the blocker (the
question, the agent's recommendation, what is at stake) and let him decide.

Answer a dispatch's one active decision request with:

    "$ATTN_WRAPPER_PATH" dispatch resolve \
      --dispatch <id> \
      --response "Use AisNoOperationV1." \
      --link "https://external-system.example/decision"

The response is stored on the dispatch and reaches the delegated agent through
`dispatch status`.

### Steer a running agent (the reverse channel)

Send durable instructions to a delegated agent with:

    "$ATTN_WRAPPER_PATH" dispatch message \
      --dispatch <id> \
      --message "Rebase onto main, rerun the focused tests, and report conflicts."

Mail is durable and acknowledgement-tracked. A delegated Claude agent watches its
own inbox, so a new message reaches it without a manual wake. attn also surfaces
pending mail ambiently — a per-session sidebar badge and an on-agent overlay — so
you (or Victor) see unread mail without opening a dashboard. The message content
never streams into the agent's terminal; it triggers a read of the durable mailbox.

Inspect sent messages, including read and acknowledgement state, with:

    "$ATTN_WRAPPER_PATH" dispatch messages --dispatch <id>

A report is not proof that the work is correct; it is an agent update. Verify
important results before relying on it.

Do not create reports on behalf of delegated agents or repeatedly interrupt
working agents just to request status.

## As a Dispatched Agent

If the initial task says the work is tracked, SELF-REPORT ONLY AT A TERMINAL OR
BLOCKED POINT — never progress, never status. Prefer silence between those points.

Declare the outcome with one flag — it is the chief's trigger:

    "$ATTN_WRAPPER_PATH" dispatch report --done    --message "<what landed>"
    "$ATTN_WRAPPER_PATH" dispatch report --review  --message "<what to review>"
    "$ATTN_WRAPPER_PATH" dispatch report --failed  --message "<what went wrong>"
    "$ATTN_WRAPPER_PATH" dispatch report --blocked \
      --question "<the decision the chief must make>" \
      --recommendation "<your pick>" --consequence "<what is at stake>"

Keep the message concrete: outcome, evidence, next action. For a long update, use
`--file <path>`. A bare `dispatch report --message "<text>"` with no state flag is
a silent note — stored and visible on demand, but it does not wake the chief.

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
inventing a location. Add a state flag (or `--coordination-file <json>`) to a
terminal handoff just as you would to a report.

### Watch your inbox

Arm a quiet watch on your inbox so the chief's steering reaches you without a
manual wake. If your agent supports background watches (for a Claude agent, a
Monitor), watch:

    "$ATTN_WRAPPER_PATH" dispatch inbox --unread

Keep it quiet — it should emit only when there is new mail. When mail arrives,
read it, act on it, then acknowledge:

    "$ATTN_WRAPPER_PATH" dispatch read --message-id <id>
    "$ATTN_WRAPPER_PATH" dispatch ack \
      --message-id <id> \
      --message "Rebased cleanly; focused tests pass."

After requesting a decision, read the durable chief response with:

    "$ATTN_WRAPPER_PATH" dispatch status

### Full structured reports (escape hatch)

The state flags cover the common cases. For a report that needs the full
coordination envelope — remaining scope, constraints, artifact identity, and
verification evidence — keep the narrative and attach a JSON envelope:

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

Reporting does not stop or transfer your session. Continue working after a blocked
report unless you are truly stuck; stop only when finished.
Do not use dispatch reporting for ordinary, untracked delegation.
