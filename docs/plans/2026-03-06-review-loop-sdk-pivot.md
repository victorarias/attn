# Review Loop SDK Pivot Plan

Date: 2026-03-06
Status: Proposed
Owner: daemon/review-loop orchestration

## Summary

Pivot the review-loop feature away from PTY-driven execution inside the main Claude Code terminal session.

Instead:

1. the main session stays the interactive implementation session
2. the review loop runs as a separate daemon-owned Claude Agent SDK workflow
3. the main agent can trigger that workflow through the `attn` skill
4. rationale is passed through an explicit handoff payload rather than implicit terminal history

This replaces terminal-behavior automation with a controlled orchestration model that `attn` already has most of the building blocks to support.

## Why we are pivoting

Real-Claude testing and repeated failures showed that PTY-driven execution inside the main session is too fragile.

Specific fragility points observed:

1. prompt injection is not the same thing as a real submit in Claude Code
2. startup, trust, and workspace UI can block or absorb injected input
3. the `launching` state is important for transcript bootstrap and should not be redefined as prompt-ready
4. prompt-ready detection through PTY bytes is possible but brittle
5. terminal UI changes could silently break the feature again even if the current implementation is repaired

The implementation direction was drifting toward terminal automation hacks rather than stable orchestration.

## Product decisions

The following are now the intended product rules for the primary implementation path:

1. do not run the loop inside the main Claude session
2. run the loop as a separate SDK-managed workflow owned by `attn`
3. keep loop start and stop user-facing controls in the current session UI
4. make context handoff explicit and structured
5. identify the calling Claude session through wrapper-provided session environment, not cwd heuristics
6. use dedicated loop-run tables now rather than extending the PTY-era session table further
7. treat PTY-driven loop execution as legacy code to remove after SDK parity
8. if the loop needs human input, pause and resume the same loop run after the answer rather than starting over
9. design the SDK execution path so it can later support a fully SDK-managed Claude coding agent, not only the review loop

## Goals

1. make review-loop execution stable against Claude terminal UI changes
2. preserve implementation rationale by allowing the main agent to pass structured context into the loop
3. keep the user-facing workflow simple:
trigger loop, observe status, stop loop
4. make the loop easy to test without heavy manual verification
5. reuse existing `claude-agent-sdk-go` patterns already present in the repo where practical

## Non-goals

1. hijacking or ACP-controlling the existing main Claude Code terminal session
2. preserving the exact PTY-era state machine or prompt-readiness semantics
3. relying on implicit transcript inheritance from the main session
4. making PTY automation the primary fallback path

## Existing assets we should reuse

The pivot is not a greenfield rewrite. The repo already contains pieces that should inform the implementation:

1. `internal/reviewer/reviewer.go` already runs a Claude Agent SDK client with MCP tools and streamed events
2. `internal/classifier/classifier.go` already uses the SDK for non-interactive agent work
3. the current review-loop settings, UI controls, and WebSocket request/result pattern already exist
4. `internal/agent/claude_skill.go` already installs an `attn` skill into Claude Code
5. store and protocol types for review-loop state already exist, even if their exact shape will change

The right question is not "how do we replace everything?" but "which parts still fit once PTY injection is no longer the executor?"

## Architecture direction

### High-level flow

1. the user or the main agent triggers a review loop for a source session
2. `attn` persists a loop run record
3. `attn` builds a structured handoff payload from trigger input plus repo/session metadata
4. `attn` launches an SDK-managed review iteration in the repo working directory
5. the SDK agent reviews and fixes within that separate run context
6. `attn` records streamed progress and the iteration result
7. if the loop should continue and the limit is not reached, `attn` launches the next iteration
8. the UI reflects loop status independently of the main session PTY

### Execution model

The most important execution decision is this:

The loop should be modeled as a daemon-owned run with one SDK invocation per iteration, not as hidden state inside the main terminal session.

This should be treated as the first concrete consumer of a more general Claude SDK execution layer inside `attn`.

#### Why one SDK invocation per iteration is the right MVP

1. it avoids depending on durable hidden agent thread state
2. each iteration becomes a clean, testable unit with explicit inputs and outputs
3. stop behavior becomes context cancellation, not terminal interrupt heuristics
4. iteration replay and debugging become much easier because each pass has a clear boundary

If a later SDK thread model proves valuable, it can be added after the one-pass-per-iteration path is stable.

#### Future-facing implication

If `attn` later supports a coding agent that is implemented directly on top of the Claude Agent SDK rather than the Claude Code TUI, this loop architecture should slot naturally into that world.

That future direction would benefit from the same kinds of control this pivot is already pursuing:

1. first-class permission handling
2. explicit idle/running/waiting state
3. deterministic automation hooks
4. daemon-owned lifecycle and cancellation

So the review-loop runner should not be designed as a special-case one-off. It should look like the first operation built on a reusable SDK execution substrate.

### Trigger model

There are two first-class trigger surfaces:

1. UI-triggered start from the active session
2. main-agent-triggered start via the `attn` skill

The second path is a feature, not a fallback. The plan should support the implementation agent explicitly telling `attn`:

1. what changed
2. why it changed
3. which plan file matters
4. which constraints or tradeoffs should govern the review loop

### Main-agent trigger contract

The existing skill only teaches Claude Code how to call:

`attn review-loop advance --session <id> --token <token>`

That is a PTY-era contract. The SDK pivot requires a different primary contract:

1. the main agent should trigger loop start, not loop advance
2. the handoff payload should be passed structurally, not embedded in shell-quoted prose
3. the loop runner itself should not need to call back into `attn` for each iteration

#### Proposed CLI shape

Prefer a file-based handoff interface to avoid shell-quoting problems:

```bash
attn review-loop start --preset <preset-id> --iterations 3 --handoff-file /tmp/attn-loop-handoff.json
```

When this command is invoked from inside an `attn`-managed Claude session, `attn` should infer `source_session_id` from `ATTN_SESSION_ID`.

Optional follow-on flags can still exist for manual or external usage:

```bash
attn review-loop start --session <session-id> --preset <preset-id> --iterations 3 --handoff-file /tmp/attn-loop-handoff.json
```

The skill path should prefer `--handoff-file` plus implicit session identity.

#### Caller identity contract

This gap needs an explicit rule because the daemon cannot infer "which Claude" from cwd alone.

Proposed rule:

1. `attn`-managed Claude sessions already run with `ATTN_SESSION_ID=<session-id>` in their environment
2. shell commands launched by Claude inherit that environment
3. `attn review-loop start` should default `source_session_id` from `ATTN_SESSION_ID`
4. if `--session` is provided, it must either match `ATTN_SESSION_ID` or the command should fail
5. if neither `ATTN_SESSION_ID` nor `--session` is present, the command should fail

This gives us a concrete caller identity mechanism without trying to reverse-map process trees, PTYs, or working directories.

#### Proposed handoff payload

```json
{
  "repo_path": "/path/to/repo",
  "base_ref": "main",
  "summary": "Implemented the new session review loop controls in the dashboard.",
  "reasoning_context": "Chose explicit websocket result events to avoid optimistic UI failure cases.",
  "plan_file": "docs/plans/2026-03-06-review-loop-sdk-pivot.md",
  "constraints": [
    "Do not repurpose the human ReviewPanel",
    "Preserve existing async request/result websocket semantics"
  ],
  "custom_prompt": "Review for robustness and polish. Fix any safe local issues.",
  "preset_id": "full-review-fix",
  "iteration_limit": 3
}
```

This should be the core context boundary for the loop.

### Loop runner design

#### Responsibilities

The SDK runner should own:

1. prompt construction from preset plus handoff payload
2. SDK client lifecycle
3. iteration counting
4. cancellation and stop handling
5. streamed status updates into the store and UI
6. final result and error capture

The lower-level SDK execution layer beneath the runner should ideally be reusable for future daemon-owned Claude workflows beyond review loops.

#### Reuse direction

The runner should reuse ideas from the reviewer implementation instead of inventing a second unrelated SDK integration style.

Likely reuse points:

1. client construction and connection lifecycle from `internal/reviewer/reviewer.go`
2. streamed message handling patterns from reviewer/classifier
3. optional custom transport support for deterministic tests

This does not mean literally forcing review-loop into the reviewer package, but it does mean we should avoid two divergent SDK orchestration patterns in the same repo.

#### Prompt and tool model

The loop runner should begin with a narrow tool surface.

Initial expectation:

1. repo working directory set to the source session repo path
2. normal file/code tools enabled
3. no dependency on the main session transcript or PTY
4. no required callback command for normal iteration progression

The daemon should decide whether to start the next iteration based on the iteration result, not on a shell command emitted by the loop agent.

#### Result model

Each iteration should produce a persisted summary with enough structure to explain what happened.

The primary control signal should come from SDK structured output via JSON schema, not from parsing assistant prose.

Why this is the right contract:

1. the SDK already supports schema-constrained output via `OutputFormat`
2. the repo already reads `ResultMessage.StructuredOutput` in the classifier path
3. loop progression is too important to drive from free-form text

#### Proposed iteration outcome schema

Each iteration should end with a structured payload like:

```json
{
  "loop_decision": "continue",
  "summary": "Fixed one websocket timeout path and found no remaining high-confidence issues.",
  "changes_made": true,
  "files_touched": [
    "internal/daemon/websocket.go",
    "app/src/hooks/useDaemonSocket.ts"
  ],
  "questions_for_user": [],
  "blocking_reason": "",
  "suggested_next_focus": "Run one more pass for polish and naming consistency."
}
```

Suggested structured-output fields:

1. `loop_decision`
2. `summary`
3. `changes_made`
4. `files_touched`
5. `questions_for_user`
6. `blocking_reason`
7. `suggested_next_focus`

Daemon-owned persisted metadata should be recorded separately:

1. `iteration_number`
2. `error`
3. `started_at`
4. `completed_at`

Required enum for `loop_decision`:

1. `continue`
2. `converged`
3. `needs_user_input`
4. `error`

Semantics:

1. `continue`: another autonomous pass is worthwhile if the iteration limit has not been reached
2. `converged`: the loop believes the work is done enough to stop
3. `needs_user_input`: the loop cannot safely continue without user direction
4. `error`: the loop could not complete the iteration reliably

#### Daemon decision mapping

The daemon should map structured outcomes to loop state like this:

1. `continue` and below limit: launch next iteration
2. `continue` at limit: mark `completed` with stop reason `iteration_limit_reached`
3. `converged`: mark `completed`
4. `needs_user_input`: mark `awaiting_user`
5. `error`: mark `error`

If the SDK run ends without valid structured output, treat that as an error in the loop runner contract rather than falling back to prose parsing for control flow.

Assistant text and tool-use traces are still useful for debugging and UI display, but they are secondary artifacts, not the progression contract.

#### Human-input resume semantics

`needs_user_input` should not terminate or replace the loop run.

It should mean:

1. persist the blocking question or reason on the current `loop_id`
2. transition the run to `awaiting_user`
3. allow the user to answer that question explicitly
4. resume the same loop with the user answer added to the next iteration context

This preserves loop continuity, iteration history, and ownership of the original handoff.

### Persistence model

The current PTY-era `session_review_loops` table is too session-shaped for the new design.

The SDK path should persist a run-oriented model in dedicated new tables now, even if the UI still renders "the active loop for this session."

Do not extend the old table as an intermediate step.

#### Suggested run-level fields

1. `loop_id`
2. `source_session_id`
3. `repo_path`
4. `status`
5. `preset_id`
6. `custom_prompt`
7. `resolved_prompt`
8. `handoff_payload`
9. `iteration_count`
10. `iteration_limit`
11. `awaiting_user_question`
12. `awaiting_user_answer`
13. `last_result_summary`
14. `last_error`
15. `created_at`
16. `updated_at`
17. `completed_at`

#### Suggested iteration-level fields

1. `loop_id`
2. `iteration_number`
3. `status`
4. `summary`
5. `question_for_user`
6. `user_answer`
7. `error`
8. `started_at`
9. `completed_at`

MVP can still enforce one active loop per source session, but persisted identity should be `loop_id`, not only `session_id`.

### Status model

The PTY-specific readiness states should not be preserved as core concepts.

Likely loop statuses:

1. `running`
2. `awaiting_user`
3. `stopped`
4. `completed`
5. `error`

Iteration-specific progress belongs in iteration records or streamed events, not in terminal-readiness flags.

### UI and protocol implications

The current start/stop/status/set-iterations surfaces can remain, but their meaning changes.

#### UI

The active-session UI can still present:

1. start loop
2. stop loop
3. iteration limit
4. prompt selection
5. answer loop question when the run is awaiting input

The difference is that these controls now manage a daemon-owned SDK run, not the selected session PTY.

#### WebSocket and protocol

The current `review_loop_result` and `review_loop_updated` pattern is still the right frontend contract style.

However:

1. payloads will likely need `loop_id` and richer run status
2. result semantics will no longer be tied to PTY injection or advance-token flow
3. the protocol will likely need an explicit "answer loop question / resume loop" command
4. if protocol message shapes change, `ProtocolVersion` must be incremented

### Stop semantics

Stop should no longer mean "best-effort send ESC and hope Claude's TUI cooperates."

Stop should mean:

1. mark the loop as `stopped`
2. cancel the active SDK context if one is running
3. do not schedule another iteration
4. persist the stop reason

This is materially stronger and easier to reason about than PTY interrupt behavior.

### Skill changes

The `attn` skill needs to evolve from a one-command helper into a structured loop-trigger helper.

Minimum skill changes:

1. explain how to start a loop with `attn review-loop start`
2. prefer a handoff file for structured context
3. keep the old advance command only while the PTY path still exists

Once the SDK path is primary, `advance` should become legacy compatibility, not the centerpiece of the skill.

## Cleanup decision

Do not "start fresh" by deleting everything.

Do a selective cleanup.

## Keep

These parts are still useful:

1. prompt preset management in settings
2. loop controls in the session UI
3. loop status/result events
4. real-Claude truth-test learnings
5. the `attn` skill installation path
6. some existing store and protocol plumbing, if reshaped carefully

## Adapt

These parts should remain but change meaning:

1. `attn review-loop ...` CLI commands
2. review-loop UI controls
3. loop persistence schema and table ownership
4. loop websocket payloads
5. real-Claude tests

## Remove

These parts should be removed after SDK parity:

1. PTY-driven loop execution as the primary mechanism
2. prompt injection into the main session as the core loop engine
3. prompt-ready retry logic that exists only to wait for terminal readiness
4. terminal bootstrap or interrupt hacks added only for PTY automation
5. advance-token flow once no supported path depends on it

## Important cleanup rule

Do not delete PTY-specific loop code until the SDK path is implemented and covered by tests.

Migration order should be:

1. implement SDK path
2. switch UI and CLI to use it
3. verify deterministic and real-Claude coverage
4. then remove obsolete PTY-path logic

## Testing strategy after the pivot

The testing harness plan in `docs/plans/2026-03-06-review-loop-testing-harness.md` remains relevant, but the center of gravity changes.

### Deterministic tests

These should primarily validate:

1. handoff payload construction
2. loop orchestration state transitions
3. structured-output parsing and validation
4. cancellation and stop behavior
5. iteration result persistence
6. UI reflection of run state

The deterministic harness should target SDK-run orchestration, not PTY prompt-readiness races.

### Real-Claude truth tests

These should validate:

1. the main agent can trigger loop start through the `attn` skill
2. the SDK loop can review and safely fix in a trivial disposable repo
3. iteration progression works under real Claude without terminal injection

These tests should no longer depend on main-session PTY injection behavior.

## Rollout order

1. mark the PTY-driven execution direction as superseded
2. define the run-oriented persistence shape
3. implement a minimal SDK runner for a single iteration
4. add `awaiting_user` persistence plus explicit answer/resume flow
5. wrap that runner in loop orchestration with stop handling and iteration limits
6. wire existing UI and CLI surfaces to the new runner
7. extend the `attn` skill to support structured loop start
8. add one opt-in real-Claude truth test for the SDK path
9. remove PTY-specific loop-execution code after parity is achieved

## Acceptance criteria

This pivot is successful when:

1. a review loop can be started without depending on main-session PTY automation
2. the main agent can pass rationale and plan context into the loop using a structured payload
3. loop iteration progresses under real Claude without terminal UI hacks
4. when the loop needs human input, the user can answer and resume the same `loop_id`
5. stopping the loop cancels SDK work and prevents further iterations
6. the primary tests exercise SDK orchestration rather than PTY timing

## Open questions

These are the main design questions still worth resolving before implementation gets deep:

1. should the first SDK iteration be resumable from a previous loop's last summary, or should every new loop start clean?
2. should user answers be stored only on the run record, or also as first-class iteration artifacts for auditability?
3. how much of the current `attn` skill should remain user-facing while both PTY and SDK paths temporarily coexist?

## Supersession note

This plan supersedes the PTY-driven execution direction in:

1. `docs/plans/2026-03-05-review-loop.md`

That earlier plan still contains useful UI and lifecycle ideas, but its core execution model is now considered too fragile for the primary implementation path.
