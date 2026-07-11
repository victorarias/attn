# Plan: OpenCode native waiting state

## Why / Alignment

OpenCode already exposes explicit question and permission lifecycle events, so
attn should use those native facts before introducing an LLM classifier. This
chunk is done when an OpenCode session with a pending question appears as
`waiting_input`, a pending permission appears as `pending_approval`, answering
either returns it to `working`, and reconnect reconciliation restores the same
state without guessing from `session.idle`.

We are aligned that native pending requests are authoritative. An idle event is
only reported as `idle` when the linked session has no pending question or
permission. Classifying an ordinary prose question, generic installed-plugin
supervision, and chief/workspace-context parity remain deferred.

## Architecture Map

```text
OpenCode 1.17.16 / 1.17.18 HTTP + SSE
  -> OpenCodeHTTP normalizes question(.v2).* / permission(.v2).* events
  -> OpenCodeDriver filters the linked native session
     -> question asked       -> session.report_state(waiting_input)
     -> permission asked     -> session.report_state(pending_approval)
     -> replied / rejected   -> session.report_state(working)
     -> idle                 -> reconcile pending requests
        -> pending question  -> waiting_input
        -> pending permission-> pending_approval
        -> neither           -> session.report_stop(idle)

Reconnect / initial binding
  -> subscribe first
  -> fetch status + pending questions + pending permissions
  -> enqueue one ordered attn report for the linked session

Tests
  -> FakeOpenCode pending-request registries and SSE events
  -> adapter tests for filtering, ordering, idle races, and reconnect recovery
  -> isolated attn-dev live question + permission proof
```

## Data Model / Interfaces

```ts
type NativeAttention =
  | "question"
  | "permission"
  | undefined

type ServerEvent = {
  type: string // normalized without optional `.v2`
  sessionID?: string
  status?: string
}
```

Pending request IDs remain owned by OpenCode. The plugin reads only enough of
`GET /question` and `GET /permission` to determine whether the linked native
session has an outstanding request; attn stores only the resulting session
state and the existing monotonic report cursor.

## Implementation Steps

- [x] Normalize legacy and v2 question/permission event names and add
  authenticated pending-request queries.
- [x] Map linked-session lifecycle events to ordered attn state reports.
- [x] Reconcile pending attention before reporting idle on initial connection,
  reconnect, and idle events.
- [x] Add deterministic adapter tests for auth, filtering, ordering, idle races,
  reconnect recovery, and no-pending idle behavior.
- [x] Update user-facing plugin documentation and changelog.
- [x] Run plugin, Go, frontend, and type-generation checks.
- [x] Verify question and permission transitions in the isolated `attn-dev`
  app; do not install or test production.

## Decisions

- Native pending requests outrank `session.idle`; idle describes the model loop,
  not whether OpenCode is awaiting a human action.
- Reconcile from OpenCode's list endpoints after subscribing so missed events do
  not leave attn in a false resting state.
- Support both legacy and v2 event discriminants because the verified OpenCode
  compatibility interval exposes both wire families.
- Keep `classifier: false`; this slice does not claim to recognize prose-only
  questions.

## Verification Evidence

- `bun test` in `plugins/attn-opencode`: 23 passed, 59 assertions, including
  overlapping question/permission arrival and resolution ordering.
- `go test ./... -count=1`: passed. An earlier run hit the existing flaky
  `TestCodexResumeMappingEndToEnd`; its isolated rerun and the final full-tree
  run both passed.
- `make test-frontend`: passed.
- `make check-types` found no schema change, but the current quicktype 23.3.1
  reorders pre-existing ticket declarations in `app/src/types/generated.ts`
  relative to `origin/main`. The generated output was restored because this
  slice does not change the protocol wire shape.
- Fresh `attn-dev` profile: a real OpenCode question rendered in the TUI and
  moved attn to `waiting_input`, then returned through `working` to `idle` when
  answered. A real shell permission rendered in the TUI and moved attn to
  `pending_approval`, then returned through `working` to `idle` after approval
  and completion. The test session/workspace was removed; production was not
  installed, restarted, or tested.
- Codex autoreview accepted two P2 overlap findings: resolving one request could
  hide another, and a later question could hide an existing permission. Both
  were fixed by reconciling OpenCode's pending lists while preserving permission
  priority. The final branch review returned no actionable findings and judged
  the patch correct with 0.9 confidence.

## Follow-ups

- Evaluate a classifier only for idle responses that ask for input without
  invoking OpenCode's question tool.
- Add generic installed-plugin supervision separately.
- Add chief/workspace-context parity separately.
