# Tasks

## Session-level review loop automation

- Status: `open`
- Priority: `high`
- Area: `daemon/session orchestration`, `protocol`, `cli`, `frontend`

### Problem

Users currently have to manually copy reviewer feedback or manually re-prompt the main session to do repeated review/fix passes.

The existing `ReviewPanel` and reviewer-agent flow are human-in-the-loop tools. They do not automate the user's actual main-session workflow:

1. prompt the main agent to do a full review/fix pass
2. let it use subagents
3. repeat that pass several times
4. stop when the user decides the loop is done

### Goal

Add a session-level "review loop" feature that automates repeated review/fix passes in the main session PTY, with configurable presets, custom prompts, iteration limits, explicit agent handoff, and UI stop control.

### Plan

See [docs/plans/2026-03-05-review-loop.md](/Users/victor.arias/projects/victor/attn/docs/plans/2026-03-05-review-loop.md).

### Decisions locked

1. This feature is separate from `ReviewPanel`.
2. The loop is session-level, not repo/branch review state.
3. No separate `pause` state in MVP.
4. No manual "advance now" action in MVP.
5. Stop means:
- do not schedule another pass
- best-effort send `ESC` to the PTY
6. Manual user interaction with the session should stop the loop.
7. Agent completion is signaled by explicit `attn review-loop advance`, not transcript heuristics.
8. Iteration limit must be configurable per run and editable while active.

### Implementation checklist

1. Protocol and schema:
- Add review-loop protocol commands and result events in `internal/protocol/schema/main.tsp`
- Regenerate protocol types
- Add constants and parsing cases in `internal/protocol/constants.go`
- Increment `ProtocolVersion`

2. Store and migrations:
- Add a new runtime loop table, e.g. `session_review_loops`
- Add store methods for create/get/update/stop/complete loop state
- Add tests for loop persistence and recovery-safe reads

3. Daemon control plane:
- Add daemon handlers for `start_review_loop`, `stop_review_loop`, `advance_review_loop`, `get_review_loop_state`, and iteration-limit updates
- Add a daemon-owned loop manager/state machine
- Ensure daemon restart does not duplicate prompt injection

4. PTY input and source tagging:
- Add a first-class daemon PTY input command usable outside the browser WebSocket-only path
- Tag PTY writes with `source=user`, `source=review_loop`, or `source=system`
- Use best-effort `ESC` on stop

5. CLI:
- Add `attn review-loop start|stop|advance|status|set-iterations`
- Make `advance` validate session ownership and token freshness
- Add CLI coverage for request encoding/response handling

6. Hook and session integration:
- Use prompt-submit hook signals as submission confirmation
- Stop loops on manual user prompt submission
- Keep loop-owned submissions distinct from manual user submissions

7. Frontend:
- Add session-level UI entry point, not `ReviewPanel` integration
- Add preset picker, custom prompt input, and iteration-limit controls
- Show active loop status, stop button, current iteration count, and stop reason
- Support editing iteration limit while loop is active

8. Validation:
- Add daemon tests for start/advance/stop/recovery/session-exit behavior
- Add frontend tests for loop UI and status handling
- Add E2E coverage for prompt injection, agent advance, stop behavior, manual-input stop, and daemon restart

### Acceptance criteria

- A loop can run multiple review/fix passes without manual copy/paste.
- The user can change the iteration limit while the loop is active.
- Stop reliably prevents any further pass scheduling.
- Manual user interaction cleanly stops the loop.
- Daemon restart does not cause duplicate prompt injection.
- The feature works independently of `ReviewPanel`.

## DB migration failure can silently drop persistence

- Status: `open`
- Priority: `high`
- Area: `daemon/store`, `tauri UX`

### Problem

Daemon startup can fail to open `~/.attn/attn.db` during migrations and silently fall back to in-memory storage.

Observed log:

`Failed to open DB at /Users/.../.attn/attn.db: migration 20 (add host to prs and migrate ids): duplicate column name: host (using in-memory)`

### User impact

- Sessions are not persisted across daemon restarts.
- After app close/reopen, previously running sessions may not appear restored.
- UI does not clearly explain that persistence is degraded.

### Context captured from this branch

- Migration `20` was made idempotent to avoid one known duplicate-column failure mode.
- Reconnect behavior and stale socket handling were improved separately.
- Even with this fix, broader migration failure handling/UX still needs dedicated follow-up.

### Follow-up options for next PR

1. Harden migration execution:
- Make all non-trivial migrations idempotent or preflight-check columns/indexes.
- Add migration failure classification (`recoverable` vs `fatal`).

2. Improve user-facing guidance:
- Emit a daemon warning event when persistence falls back to in-memory.
- Show a persistent banner in app with remediation steps.
- Include exact DB path and failure message in UI.

3. Add a recovery workflow:
- Add a `doctor`/`repair` command (or equivalent maintenance action) to validate and repair schema metadata safely.
- Optionally backup and recreate DB when schema is unrecoverable.

4. Define strict policy:
- Decide whether daemon should refuse startup on DB migration failure (instead of silent degraded mode), or continue in degraded mode with explicit warning + guidance.

### Acceptance criteria (next PR)

- User can clearly see when daemon is running without durable persistence.
- Guidance includes concrete recovery steps.
- Migration failure path is covered by automated tests.
- Session restoration behavior is deterministic after app restart.

## Terminal visible-state restore for full-screen agents

- Status: `open`
- Priority: `high`
- Area: `daemon/pty`, `websocket`, `frontend terminal`

### Problem

Byte-tail scrollback replay is not enough for full-screen terminal UIs (Codex today, likely Claude Code soon). On app reconnect or daemon-managed reattach, users can lose most of what was visible and only see the current prompt/input region.

### Goal

Restore what was visibly on screen, not just raw output tail.

### Proposed direction

1. Build a daemon-side virtual terminal screen state (emulator-backed snapshot) per PTY session.
2. On `attach_session`, send a screen snapshot payload for supported agents before resuming live stream.
3. Keep existing byte scrollback as fallback for unsupported/legacy paths.
4. Start with Codex and design the interface so Claude/other full-screen agents can use the same restore mechanism.

### Follow-up options for next PR

1. Add a new attach payload mode:
- `screen_rows`, `screen_cols`, `cursor`, and serialized visible cells (plus minimal style attrs).
- Optional flag indicating snapshot freshness/capability.

2. Snapshot lifecycle:
- Update snapshot incrementally from PTY output.
- Bound memory usage and define eviction rules.
- Decide whether to persist snapshot across daemon restart (optional later phase).

3. Frontend apply path:
- On attach, prefer snapshot restore path for capable sessions.
- Fall back to scrollback replay when snapshot is unavailable.

4. Testing:
- Add harness test with alternate-screen/full-screen redraw sequences.
- Assert restored visible frame parity (not just presence of output bytes).
- Add reconnect tests for Codex first, then Claude once behavior matches.

### Acceptance criteria (next PR)

- Reattaching a Codex full-screen session restores the previously visible frame with high fidelity.
- No duplicate session creation is triggered by restore logic.
- Fallback behavior remains intact for non-snapshot sessions.
- E2E coverage protects reconnect + restore regressions for full-screen rendering.

## Decouple PTY lifecycle from daemon lifecycle (worker sidecars)

- Status: `open`
- Priority: `high`
- Area: `daemon/pty`, `process lifecycle`, `recovery`

### Problem

Daemon currently owns PTY process lifecycle directly. If daemon restarts, PTY sessions are disrupted.

### Direction

Adopt per-session worker sidecars (`Option 2`): daemon is control plane, each session PTY runs in its own worker process.

### Plan

See `docs/plans/2026-02-08-pty-worker-sidecar-plan.md` (updated 2026-02-10, implementation-ready).

### Acceptance criteria (MVP)

- Daemon restart does not kill active PTY sessions.
- Daemon rehydrates sessions by discovering and reattaching to live workers.
- Frontend protocol remains daemon-centric (no direct worker connections).
- Architecture remains compatible with daemon running on remote host/VM.
