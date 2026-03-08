# Review Loop Testing Harness Plan

Date: 2026-03-06
Status: Proposed
Owner: daemon/review-loop reliability

## Summary

Build a dedicated testing strategy for the SDK-pivoted review loop with two complementary lanes:

1. a deterministic SDK execution harness for broad orchestration and race-condition coverage
2. an opt-in real-Claude integration lane for validating assumptions that only real Claude execution can prove

The goal is to reduce manual verification and make review-loop regressions observable, reproducible, and debuggable without depending on terminal automation.

## Why this needs its own plan

The review-loop feature now depends on interactions across:

1. structured handoff payload construction
2. SDK client lifecycle
3. structured iteration output validation
4. loop run persistence
5. `awaiting_user` pause and resume behavior
6. WebSocket/UI reflection
7. real Claude behavior under the Agent SDK

These bugs are not well-covered by normal unit tests alone, and ad hoc manual checks are too expensive and too easy to forget.

## Testing philosophy

Use different test layers for different questions.

### Deterministic harness answers

1. Did the daemon state machine transition correctly?
2. Did structured-output validation drive the right next step?
3. Did stop/cancel logic converge correctly?
4. Did `awaiting_user` resume preserve the same `loop_id` and history?
5. Did UI state reflect daemon loop state correctly?

### Real-Claude integration answers

1. Does the `attn` skill get used as intended for structured loop start?
2. Does Claude produce valid structured result output under the SDK path?
3. Does resume-after-human-answer work under real Claude?
4. Does the actual SDK execution path behave like our deterministic harness assumes?

The deterministic harness should be the primary safety net. The real-Claude lane should be a truth test, not the whole strategy.

## Goals

1. catch review-loop regressions without relying on manual repro
2. make race conditions reproducible
3. preserve fast local feedback for most tests
4. add at least one real-Claude integration test that validates end-to-end handoff behavior
5. produce useful artifacts when tests fail

## Non-goals

1. running paid real-Claude integration tests on every CI build by default
2. replacing all deterministic tests with real-Claude tests
3. simulating every Claude internal behavior perfectly in the fake harness

## Current state

Existing coverage already includes:

1. daemon unit tests around the PTY-era review-loop implementation
2. frontend unit tests for loop UI and settings
3. some real-Claude/PTTY-oriented truth-test learnings

This is useful historical context, but the primary execution model has changed. The new harness should target SDK orchestration first.

## Proposed test pyramid

### 1. Unit tests

Keep fast unit tests for:

1. handoff payload rendering
2. structured-output parsing and validation
3. loop persistence CRUD and state transitions
4. resume-after-answer context construction
5. prompt preset parsing/storage

These should remain cheap and default-on.

### 2. Daemon integration harness

Add a dedicated harness that launches:

1. a real daemon
2. a fake SDK execution backend or controllable SDK transport
3. a control client for unix-socket commands
4. a WebSocket observer for UI-facing events

This becomes the main review-loop correctness layer.

### 3. App E2E

Keep a smaller number of UI-focused Playwright tests:

1. session-level loop controls
2. sidebar/dashboard badge reflection
3. answering a loop question and resuming
4. one normal progression test

These should validate UI reflection, not own every backend scenario.

### 4. Real-Claude truth tests

Add opt-in integration tests that use actual Claude execution in a disposable git repo.

These should be sparse, high-signal, and artifact-rich.

## Deterministic SDK harness

### Shape

Create a small harness around a fake or scripted Claude SDK backend from `attn`'s perspective.

Suggested location:

1. `internal/reviewloopharness`
2. or a reusable lower-level package under the future SDK execution layer, such as `internal/claudeexec/testharness`

The key point is that the deterministic harness should operate at the SDK message/result boundary, not the PTY boundary.

### Required behaviors

The fake SDK harness should support emitting:

1. assistant text chunks
2. tool-use events
3. permission request / permission denial cases
4. structured result payloads
5. invalid or missing structured result payloads
6. delayed completion
7. cancellation during execution
8. question-for-user outcomes

### Scenario scripting

Represent scenarios as small JSON or Go struct fixtures.

Example:

```json
{
  "name": "needs-user-input-then-resume",
  "iterations": [
    {
      "assistant_chunks": [
        "Reviewing the change set.",
        "I need one clarification before I can continue."
      ],
      "result": {
        "loop_decision": "needs_user_input",
        "summary": "Blocked on intended retry behavior.",
        "changes_made": false,
        "files_touched": [],
        "questions_for_user": ["Should retry exhaustion surface in the UI or remain daemon-only?"],
        "blocking_reason": "Retry behavior is under-specified.",
        "suggested_next_focus": ""
      }
    },
    {
      "requires_user_answer": true,
      "expected_answer": "Surface it in the UI.",
      "result": {
        "loop_decision": "converged",
        "summary": "Applied the clarification and found no further issues.",
        "changes_made": true,
        "files_touched": ["internal/daemon/websocket.go"],
        "questions_for_user": [],
        "blocking_reason": "",
        "suggested_next_focus": ""
      }
    }
  ]
}
```

This makes orchestration and resume cases easy to add without hand-coding each one.

### Timeline recording

The harness should record a timestamped event timeline for each run, including:

1. daemon review-loop state changes
2. review-loop iteration record changes
3. SDK assistant chunks
4. SDK tool-use and permission events
5. structured result payloads
6. user-answer submissions
7. WebSocket events

Suggested artifact:

1. JSON timeline file
2. optional plain-text pretty summary

This is the most important debugging tool for racey or stateful failures.

### Fault injection

The fake harness should be able to intentionally:

1. delay result emission
2. emit invalid structured output
3. omit structured output entirely
4. emit `needs_user_input` with multiple questions
5. emit permission denial or tool failure
6. finish after cancellation was requested
7. close the stream unexpectedly mid-iteration

This is how we make the daemon’s convergence behavior trustworthy.

### Invariants to assert

The deterministic harness should assert sequences, not only final state.

Examples:

1. `running -> awaiting_user -> running -> completed` on a same-loop resume path
2. stopped loops never launch another iteration
3. invalid or missing structured output becomes loop `error`
4. answering a question preserves the same `loop_id`
5. iteration history remains append-only across resume
6. no duplicate iteration launch after a single valid `continue`

### Candidate deterministic scenarios

1. `start-loop-happy-path`
2. `continue-until-limit`
3. `converged-on-first-pass`
4. `needs-user-input-then-resume`
5. `needs-user-input-multiple-rounds`
6. `invalid-structured-output`
7. `missing-structured-output`
8. `permission-denied-during-iteration`
9. `cancel-while-running`
10. `cancel-after-awaiting-user`
11. `daemon-restart-mid-awaiting-user`
12. `result-arrives-after-cancel-request`

## Real-Claude integration lane

### Purpose

Use a very small number of opt-in tests against real Claude execution to validate assumptions the deterministic harness cannot prove.

### Execution policy

Local:

1. enabled only when explicitly requested
2. assumes Claude is already installed and logged in

CI:

1. skipped by default for now
2. can later move to a dedicated nightly lane if desired

Suggested gate:

1. `ATTN_RUN_REAL_CLAUDE_REVIEW_LOOP=1`

Suggested additional guard:

1. skip if `claude` is not on `PATH`

### Repository setup

Each real-Claude test should create a disposable git repo with:

1. one very small source file
2. one tiny uncommitted defect or polish issue

Keep the repo trivial so Claude finishes quickly and safely.

Example:

1. `main.go`
2. single small naming, error-handling, or polish issue

### First real-Claude tests

#### Test 1: Real structured-output path

Name:

1. `TestRealClaudeReviewLoop_ProducesStructuredOutcome`

Flow:

1. create tiny temp git repo
2. start real daemon
3. trigger review loop through the intended SDK-backed path
4. wait for iteration completion
5. assert the loop result contains valid structured output
6. assert the daemon maps it to the correct loop state

#### Test 2: Real skill start path

Name:

1. `TestRealClaudeReviewLoop_UsesAttnSkillForStart`

Flow:

1. ensure Claude skill exists
2. spawn real Claude session through `attn`
3. instruct Claude to trigger loop start with structured handoff
4. assert the loop run is created with the expected `source_session_id`

#### Test 3: Real await-user resume path

Name:

1. `TestRealClaudeReviewLoop_AwaitUserThenResume`

Flow:

1. create tiny temp repo with an under-specified requirement
2. start loop
3. wait for loop to enter `awaiting_user`
4. submit a user answer through the explicit answer/resume path
5. assert the same `loop_id` resumes and completes or advances correctly

### Real-Claude artifacts on failure

Always dump:

1. daemon log tail
2. loop run snapshot
3. iteration snapshots
4. structured result payload
5. relevant assistant-message tail
6. git diff of temp repo

Without this, real-Claude failures will be too slow to debug.

## Ownership split

### Deterministic harness owns

1. correctness under race conditions
2. structured-output validation behavior
3. state convergence
4. same-loop resume guarantees
5. failure mode simulation

### Real-Claude lane owns

1. actual Claude behavior under the SDK path
2. skill usage assumptions
3. actual structured-output reliability
4. actual resume-after-human-answer behavior

## Rollout order

1. update this harness plan to match the SDK pivot
2. add one opt-in real-Claude truth test first
3. define a minimal timeline artifact format for harness runs
4. scaffold the fake SDK harness second
5. migrate race-heavy backend cases to that harness
6. keep Playwright E2E small and focused on UI reflection

Reason:

1. the real-Claude test validates the basic contract immediately
2. the deterministic harness then scales coverage without manual verification cost

## Acceptance criteria

We consider the harness work successful when:

1. one local opt-in real-Claude test proves the SDK path yields valid structured loop results in a tiny repo
2. one local opt-in real-Claude test proves a loop can enter `awaiting_user` and resume on the same `loop_id`
3. the deterministic harness can reproduce and assert the core review-loop orchestration scenarios
4. failures produce enough artifacts to debug without rerunning manually
5. new review-loop logic changes can be validated primarily by tests, not by manual terminal poking

## Immediate next steps

1. scaffold one opt-in real-Claude SDK-path test in `internal/daemon/real_claude_review_loop_test.go`
2. define a minimal timeline artifact format for harness runs
3. scaffold the fake SDK harness
4. then migrate the stateful backend cases to that harness
