# Session Review Loop Plan

Date: 2026-03-05
Status: Proposed
Owner: daemon/session orchestration

## Summary

Add a new session-level "review loop" feature that repeatedly prompts the main agent session to review and fix its own work until a configurable iteration limit is reached or the user stops the loop.

This feature is explicitly separate from the existing human-in-the-loop `ReviewPanel` and reviewer-agent workflow.

The loop is controlled by `attn`, not by the user manually copying review feedback into the session.

## Product decision

The review loop is not part of the existing review panel.

Reasons:

1. The current review panel is for human-in-the-loop review, manual comment triage, and optional reviewer-agent assistance.
2. The new feature is session automation with no required human review step after trigger.
3. Reusing the review panel would couple two different workflows and create confusing ownership of state.

## User workflow

1. User starts a review loop for a session from the main session UI.
2. User chooses:
- a saved review prompt preset or custom prompt
- an iteration limit
3. `attn` injects the review/fix prompt into the session PTY.
4. The agent performs a full review/fix pass.
5. When the agent decides the pass is complete, it runs `attn review-loop advance --session <id> --token <token>`.
6. `attn` marks that iteration complete.
7. If the loop is still active and below the iteration limit, `attn` waits for the session to become prompt-ready and injects the next pass.
8. The user can stop the loop at any time from the UI.
9. Stopping means:
- do not schedule another pass
- best-effort send `ESC` into the PTY to interrupt the current pass

## Goals

1. Automate the user's existing repeated "review + fix + polish" main-session workflow.
2. Allow a few saved review prompt presets plus custom prompts.
3. Allow the iteration limit to be configured per run and changed while the loop is active.
4. Let the agent explicitly signal pass completion with an `attn` CLI command.
5. Let the user stop the loop from the UI.
6. Keep loop state understandable when the user manually interacts with the session.

## Non-goals (MVP)

1. Integrating this into the existing `ReviewPanel`.
2. Using reviewer-agent comments as loop input.
3. Adding a separate pause concept distinct from stop.
4. Adding a manual "advance now" control.
5. Reliably forcing every agent to stop immediately on UI stop.
6. Trying to infer loop state from free-form transcript content alone.

## Existing implementation facts

These matter because they shape the clean implementation path.

1. PTY input is already supported over the WebSocket PTY path.
- `pty_input` is handled in [`internal/daemon/websocket.go:797`](/Users/victor.arias/projects/victor/attn/internal/daemon/websocket.go:797).

2. The app already sends text into the active session PTY.
- `handleSendToClaude()` writes directly to PTY in [`app/src/App.tsx:1423`](/Users/victor.arias/projects/victor/attn/app/src/App.tsx:1423).

3. The current review UI already has a one-shot send-to-session bridge.
- "Send unresolved" exists in [`app/src/components/ReviewPanel.tsx:1256`](/Users/victor.arias/projects/victor/attn/app/src/components/ReviewPanel.tsx:1256).

4. The current CLI/unix-socket client does not expose PTY input.
- `Client` supports state/stop/todos/session metadata in [`internal/client/client.go:92`](/Users/victor.arias/projects/victor/attn/internal/client/client.go:92).
- The daemon unix-socket command handler does not currently process PTY input in [`internal/daemon/daemon.go:1366`](/Users/victor.arias/projects/victor/attn/internal/daemon/daemon.go:1366).

5. The current reviewer-agent flow is a separate repo/branch-scoped system with its own fixed prompt and rerun state.
- prompt construction in [`internal/reviewer/reviewer.go:558`](/Users/victor.arias/projects/victor/attn/internal/reviewer/reviewer.go:558)
- review run state in [`internal/daemon/review.go:272`](/Users/victor.arias/projects/victor/attn/internal/daemon/review.go:272)

## Core model

The review loop is a session-owned automation state machine.

It is not a review comment system and not a repo/branch review artifact.

### Loop state

Each loop instance should track:

1. `session_id`
2. `status`
3. `preset_id`
4. `custom_prompt`
5. `resolved_prompt`
6. `iteration_count`
7. `iteration_limit`
8. `stop_requested`
9. `advance_token`
10. `last_prompt_at`
11. `last_advance_at`
12. `last_user_input_at`
13. `stop_reason`
14. `created_at`
15. `updated_at`

### Status values

Use a small, explicit state set:

1. `running`
2. `waiting_for_agent_advance`
3. `advance_received_waiting_prompt`
4. `stopped`
5. `completed`
6. `error`

No separate `paused` state in MVP.

## Stop and manual-input semantics

### Product rule

If the user manually interacts with the session while a loop is active, the loop should stop scheduling future passes.

This keeps ownership simple and prevents `attn` from fighting the user.

### Why there is no pause

In this workflow, the user can always type into the session. A distinct `pause` state would imply `attn` can reliably determine whether the loop should resume automatically after arbitrary user input. It cannot do that safely in MVP.

Therefore:

1. `Stop` means "do not schedule another pass."
2. A later restart creates or resumes a fresh loop decision by the user.

### Stop behavior

When the user presses stop in the UI:

1. Set `stop_requested=true`.
2. Transition the loop toward `stopped`.
3. Best-effort send `ESC` (`\x1b`) to the session PTY.
4. Do not inject another pass even if an advance signal arrives later.

`ESC` is best-effort only. It may cancel generation, dismiss a transient UI state, or do nothing depending on the agent. The real guarantee is "no next pass."

## Detecting manual user input

Do not use raw Return-key monitoring as the main signal. Return is too noisy and does not reliably mean "the user submitted a prompt."

### Required design: input source tagging

All PTY writes that originate from `attn` should carry an input source tag.

Proposed sources:

1. `user`
2. `review_loop`
3. `system`

This requires a first-class daemon command for PTY input rather than relying only on the existing WebSocket path.

### Why tagging matters

The session hooks can tell `attn` that a prompt was submitted, but not whether the prompt came from the user or from loop automation.

The useful rule is:

1. if a submitted prompt follows `source=review_loop`, the loop continues normally
2. if a submitted prompt follows `source=user`, the loop is stopped by user intervention

### Hook usage

Use the existing prompt-submit hook path as the "a prompt was actually submitted" confirmation.

This feature should not depend on heuristic PTY output parsing to decide whether a loop-owned prompt was accepted by the agent UI.

## Agent handoff protocol

### Prompt contract

Each injected loop prompt should instruct the agent to:

1. perform a full review and fix pass according to the preset/custom prompt
2. stop to ask questions if needed
3. when the pass is complete, run:

```bash
attn review-loop advance --session <session-id> --token <advance-token>
```

### Why explicit CLI advance is required

Do not try to infer completion from transcript text like "done", "all fixed", or "ready".

The explicit CLI advance:

1. is deterministic
2. reduces prompt brittleness
3. avoids output-parsing false positives
4. lets the daemon validate loop ownership and token freshness

### Advance token

Each active loop should have a random per-loop token.

Purpose:

1. tie the advance call to the loop instance
2. prevent accidental cross-session or stale-loop advancement
3. allow clean rejection of stale agent commands after stop/restart

## Prompt readiness gate

After receiving `review-loop advance`, the daemon should not inject the next prompt immediately.

Instead:

1. record the advance
2. transition to `advance_received_waiting_prompt`
3. wait for the session to become prompt-ready
4. only then inject the next pass

Prompt-ready should initially mean session state is one of:

1. `waiting_input`
2. `idle`

This avoids PTY input interleaving while the current turn is still active.

## Storage design

### Prompt presets

Persist presets in the existing settings store as JSON.

The settings store already supports arbitrary key/value records in [`internal/store/store.go:1172`](/Users/victor.arias/projects/victor/attn/internal/store/store.go:1172).

Suggested setting keys:

1. `review_loop_prompt_presets`
2. `review_loop_default_iteration_limit`
3. `review_loop_last_used_preset`

Suggested preset shape:

```json
[
  {
    "id": "full-review-fix",
    "name": "Full Review + Fix",
    "prompt": "Do a full review of these changes, use subagents, one normal and one system architect. Fix everything including polish items. Let's make a great PR. It's ok to stop to ask me questions.",
    "default_iteration_limit": 3
  }
]
```

### Runtime loop state

Do not store runtime loop state in the settings table.

Add a dedicated table, for example `session_review_loops`.

Suggested columns:

1. `session_id TEXT PRIMARY KEY`
2. `status TEXT NOT NULL`
3. `preset_id TEXT`
4. `custom_prompt TEXT`
5. `resolved_prompt TEXT NOT NULL`
6. `iteration_count INTEGER NOT NULL`
7. `iteration_limit INTEGER NOT NULL`
8. `stop_requested INTEGER NOT NULL`
9. `advance_token TEXT NOT NULL`
10. `stop_reason TEXT`
11. `last_prompt_at TEXT`
12. `last_advance_at TEXT`
13. `last_user_input_at TEXT`
14. `created_at TEXT NOT NULL`
15. `updated_at TEXT NOT NULL`

Optional later table:

1. `session_review_loop_history`

This can store per-iteration timestamps and outcomes, but MVP can defer it.

## Protocol changes

Protocol additions must increment `ProtocolVersion`.

### New daemon control-plane commands

Add explicit loop commands on the normal daemon control path:

1. `start_review_loop`
2. `stop_review_loop`
3. `advance_review_loop`
4. `get_review_loop_state`
5. `set_review_loop_iteration_limit`

### New PTY input control-plane command

Add daemon-side PTY input command that works outside the browser WebSocket-only path.

Suggested command:

1. `send_session_input`

Suggested fields:

1. `session_id`
2. `data`
3. `source`

This can either reuse the shape of `pty_input` with a source field or define a separate command. A separate command is clearer because the control plane and WebSocket PTY stream have different roles.

### Result events

Use the existing async request/result pattern for operations that can fail.

Suggested result events:

1. `start_review_loop_result`
2. `stop_review_loop_result`
3. `advance_review_loop_result`
4. `get_review_loop_state_result`
5. `set_review_loop_iteration_limit_result`
6. `send_session_input_result`

### Broadcast events

Suggested daemon-to-UI events:

1. `review_loop_updated`
2. `review_loop_stopped`
3. `review_loop_completed`
4. `review_loop_error`

## CLI changes

Add new `attn` subcommands:

```bash
attn review-loop start --session <id> [--preset <id>] [--prompt <text>] [--iterations <n>]
attn review-loop stop --session <id>
attn review-loop advance --session <id> --token <token>
attn review-loop status --session <id>
attn review-loop set-iterations --session <id> --iterations <n>
```

### CLI role split

1. UI may use WebSocket commands for interactive control.
2. Agent-in-session uses the CLI `advance` subcommand.
3. The daemon owns the state machine and PTY injection.

Do not require a visible frontend client for the loop to continue once started.

## Daemon implementation

### New loop manager

Add a daemon-owned loop manager responsible for:

1. loading active loop state from store
2. validating start/stop/advance commands
3. injecting prompts into PTY
4. reacting to session state transitions
5. reacting to manual user input and prompt-submit hooks
6. broadcasting loop state updates

### Prompt injection

Daemon prompt injection should:

1. use the new daemon-side PTY input control path
2. mark the write as `source=review_loop`
3. append the required newline or submit sequence

For MVP, inject the full prompt plus `\n`.

### Stop interrupt

On stop:

1. mark `stop_requested=true`
2. write `\x1b` with `source=system`
3. transition to `stopped` once no further work is scheduled

### Session lifecycle interactions

If the session exits while a loop is active:

1. mark loop `error` or `stopped`
2. record reason `session_exited`
3. broadcast state

If the session is deleted/unregistered:

1. remove or archive loop runtime state

### Recovery

On daemon restart:

1. load any active loop state from the store
2. rebind active loops to live sessions if they still exist
3. do not blindly inject a new prompt on startup
4. wait for the next valid state transition or explicit user action

This avoids duplicate prompt injection after daemon restarts.

## Frontend implementation

### Entry point

The UI entry point should live on the session, not in the review panel.

Suggested locations:

1. session action menu
2. session header controls for the active session

### Start flow UI

Start-loop UI should allow:

1. selecting a preset
2. editing a custom prompt
3. setting iteration limit
4. starting the loop

### Active loop UI

Display:

1. active/stopped/completed/error status
2. current iteration count
3. current iteration limit
4. stop button
5. edit iteration limit control
6. stop reason if relevant

### Manual user interaction policy

If the user types into the session while a loop is active:

1. surface that the loop will stop
2. stop it automatically once manual input is detected or confirmed submitted

The exact UX can be simple in MVP:

1. stop immediately on manual prompt submission
2. show a small status note like "stopped due to manual session input"

### PTY write routing

Frontend PTY writes should stop being anonymous for loop-sensitive cases.

Manual terminal input should identify itself as `source=user`.

App-owned session injections should identify themselves as:

1. `review_loop`
2. `system`

## Implementation touch points

The likely code areas for MVP are:

### Protocol and generated types

1. `internal/protocol/schema/main.tsp`
2. `internal/protocol/constants.go`
3. `internal/protocol/generated.go`
4. `app/src/types/generated.ts`

### Daemon and client

1. `internal/client/client.go`
2. `cmd/attn/main.go`
3. `internal/daemon/daemon.go`
4. `internal/daemon/websocket.go`
5. `internal/daemon/` new review-loop manager file, for example `review_loop.go`

### Store and migrations

1. `internal/store/sqlite.go`
2. `internal/store/store.go`
3. `internal/store/` new review-loop store file, for example `review_loop.go`

### Hooks and session state integration

1. `internal/hooks/hooks.go`
2. transcript/state handling paths that currently react to prompt submission and stop events

### Frontend

1. `app/src/hooks/useDaemonSocket.ts`
2. `app/src/App.tsx`
3. `app/src/store/sessions.ts`
4. `app/src/pty/bridge.ts`
5. new session-level UI component(s) for loop start/status controls

## Suggested prompt preset examples

The initial preset library can be small.

### Full Review + Fix

```text
Do a full review of these changes. Use subagents: one implementation-focused reviewer and one system architect. Fix everything you find, including polish items, unless the tradeoff is unclear. If something is ambiguous or risky, stop and ask me. When you believe this pass is complete, run: attn review-loop advance --session {{session_id}} --token {{advance_token}}
```

### Full Review + Fix + PR polish

```text
Do a full review of these changes. Use subagents: one implementation-focused reviewer and one system architect. Fix bugs, design issues, and polish items. Aim for a strong PR, not just correctness. If a change needs product judgment or is risky, stop and ask me. When this pass is complete, run: attn review-loop advance --session {{session_id}} --token {{advance_token}}
```

## Validation plan

### Daemon unit tests

Add tests for:

1. starting a loop stores state and injects first prompt
2. advance with valid token increments iteration and waits for prompt-ready state
3. advance with invalid token is rejected
4. advance after stop is ignored or rejected
5. stop sets `stop_requested` and sends `ESC`
6. session exit during active loop stops the loop with reason
7. daemon restart does not duplicate prompt injection
8. changing iteration limit while active updates the next-loop decision correctly

### Protocol tests

Add coverage for:

1. new command parsing
2. new result event parsing
3. protocol version bump behavior

### CLI tests

Add tests for:

1. `review-loop advance` encoding correct daemon message
2. `review-loop stop` encoding correct daemon message
3. `review-loop status` decoding daemon response

### Frontend unit tests

Add tests for:

1. start-loop form populates presets and custom prompt
2. active loop status renders correctly
3. iteration limit edits send the correct command
4. stop button disables future scheduling state in UI
5. manual user prompt submission marks the loop as stopped in UI state

### End-to-end tests

Add E2E coverage with mock PTY / harness support for:

1. start loop on a session and verify first prompt is injected
2. agent runs `attn review-loop advance` and next prompt is injected only after prompt-ready state
3. stop loop prevents next iteration even if advance arrives later
4. stop loop sends `ESC` to PTY
5. manual user prompt submission stops the loop
6. daemon restart preserves loop state without duplicate injection

## Validation criteria

This feature is ready for use when:

1. a loop can run for multiple iterations without manual copy/paste
2. the user can change iteration limit while it is active
3. user stop reliably prevents any further pass scheduling
4. manual user interaction cleanly stops the loop
5. daemon restart does not cause duplicate prompt injection
6. the feature operates without depending on `ReviewPanel`

## Rollout strategy

Implement in this order:

1. store schema and daemon loop manager
2. daemon control-plane commands and CLI subcommands
3. daemon-side PTY input with source tagging
4. session UI for start/stop/status/iteration limit
5. prompt-submit/manual-input stop logic
6. restart recovery hardening and E2E coverage

## Risks

1. `ESC` behavior is agent-dependent.
- Mitigation: treat it as best-effort only and make "no next pass" the real stop guarantee.

2. Prompt-ready state may not perfectly match every agent UI timing.
- Mitigation: gate on existing session state first and add harness coverage for real agent behavior.

3. Manual user input detection can be ambiguous without source tagging.
- Mitigation: make source tagging part of MVP, not follow-up work.

4. Daemon restart could accidentally duplicate prompts if recovery is naive.
- Mitigation: do not auto-inject on restart without a fresh state transition or explicit resume condition.

## Decisions locked for MVP

1. The feature is session-level and separate from `ReviewPanel`.
2. No pause state.
3. No manual "advance now".
4. Stop means "no next pass" plus best-effort `ESC`.
5. Manual user interaction stops the loop.
6. Iteration limit is configurable per run and editable while active.
7. Agent completion is signaled by explicit `attn review-loop advance`, not transcript heuristics.
