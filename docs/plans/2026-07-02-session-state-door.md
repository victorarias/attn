# Plan: One door for session state (`applyState`)

From the 2026-07-01 architecture review (top recommendation). Line references are
as of commit `80c62f6b` — always re-anchor by symbol name, not line number.

## Goal

Session state writes are smeared across four copy-pasted choreographies plus five
raw `store.UpdateState` calls, each applying a *different subset* of guards and
side effects. The stale-classifier guard (`UpdateStateWithTimestamp`) is a
convention with exactly one caller, documented as AGENTS.md Critical Pattern #3
instead of being enforced by an interface.

Deepen this into one module: every session state transition goes through a single
`applyState` door that owns the guards, side effects, and broadcast. A bug fixed
there is fixed for every writer. AGENTS.md #3 stops being tribal knowledge.

**This is a behavior-preserving refactor.** No state transition, guard, or
broadcast may change observably. The per-source differences below are the spec.

## Architecture Map

```text
Current (4 choreographies + 5 raw writes, all converging on store.UpdateState):

hook/socket path      -> handleState (daemon.go ~2104)
                           markRunStartedIfNeeded/clearLongRunTracking
                           store.UpdateState + store.Touch
                           cancelNudgeOnLeaveIdle, sendOK
                           broadcast + recomputeAndBroadcastWorkspaceForSession
PTY detector          -> handlePTYState (daemon.go ~1513)
                           guard: GetAgentDriverRun + pluginDriverReportsState
                           guard: agentdriver.ShouldApplyPTYState
                           same as above + go notifyTicketSessionWentIdle (on idle)
classifier            -> updateAndBroadcastStateWithTimestamp (daemon.go ~2775)
                           store.UpdateStateWithTimestamp (CAS)  <- the ONLY guarded door
transcript watcher    -> updateAndBroadcastState (daemon.go ~2750)
                           same but NO store.Touch
startup recovery      -> raw store.UpdateState (daemon.go ~844, ~1121, ~1132, ~1167)
session exit          -> raw store.UpdateState (daemon.go ~1398) + Touch + broadcast

Target:

every writer
  -> d.applyState(sessionID, state, stateChangeOpts{...})   // one module: session_state.go
       -> guards (per opts)
       -> run-tracking side effects
       -> store write (always timestamp-CAS)
       -> nudge cancel, ticket doorbell (per opts)
       -> broadcast + workspace recompute (per opts)
grep proof: store.UpdateState( appears ONLY inside session_state.go
```

## Data Model / Interfaces

New file `internal/daemon/session_state.go`:

```go
// stateChangeOpts encodes the (small, deliberate) differences between writers.
// The defaults reproduce updateAndBroadcastState. Every field maps 1:1 to a
// behavior in the Current tree above — do not invent new combinations.
type stateChangeOpts struct {
    observedAt   time.Time // zero => time.Now(); classifier passes its captured start time
    touch        bool      // hook, PTY, session-exit paths set true
    broadcast    bool      // default true; some recovery writes set false
    ticketNudge  bool      // PTY path only: go notifyTicketSessionWentIdle on idle
}

// applyState is the ONLY place allowed to call store.UpdateState*.
// Returns false when the CAS rejects a stale write.
func (d *Daemon) applyState(sessionID, state string, opts stateChangeOpts) bool
```

Store side: `UpdateState(id, state)` becomes a thin wrapper over
`UpdateStateWithTimestamp(id, state, time.Now())` (a fresh timestamp always wins,
so current callers keep identical behavior), then delete the wrapper once the
daemon only calls the timestamped method through `applyState`.

The PTY-specific guards (`GetAgentDriverRun`/`pluginDriverReportsState`,
`ShouldApplyPTYState`) stay in `handlePTYState` *before* the `applyState` call —
they are about whether the PTY observation is trustworthy, not about how a state
write happens. `handlePTYState` shrinks to guards + one `applyState` call.

## Boundaries

- `session_state.go` owns: run tracking (`markRunStartedIfNeeded`,
  `clearLongRunTracking`), the store write, `cancelNudgeOnLeaveIdle`, the ticket
  doorbell dispatch, the `EventSessionStateChanged` broadcast, and
  `recomputeAndBroadcastWorkspaceForSession`.
- Callers own: source-specific guards (PTY trust checks), transport replies
  (`sendOK` stays in `handleState`), and *when* to capture `observedAt`
  (classifier captures before slow work — keep `classificationStartTime` at the
  top of `classifySessionState`).
- Nothing outside `session_state.go` may call `d.store.UpdateState` or
  `d.store.UpdateStateWithTimestamp`. Reads (`store.Get`) are unrestricted.
- Hook-authoritative states must survive: do not weaken `ShouldApplyPTYState`
  (see AGENTS.md; the per-agent policy seam in `internal/agent/daemon_policy.go`
  is correct and stays).

## Implementation Steps

- [ ] PR 1 — the door. Add `session_state.go` with `applyState` + `stateChangeOpts`.
      Rewrite the four choreographies as calls into it (`handleState`,
      `handlePTYState`, `updateAndBroadcastState`,
      `updateAndBroadcastStateWithTimestamp` — keep the last two as one-line
      deprecated wrappers if that shrinks the diff, then inline them in PR 2).
      Add table-driven tests for `applyState` itself: per-source option sets ×
      (fresh timestamp, stale timestamp) asserting store state, broadcast
      emission (use `BroadcastRecorder` from `testharness.go`), touch, and
      nudge-cancel calls.
- [ ] PR 2 — fold the stragglers. Convert the five raw sites (recovery writes at
      daemon.go ~844/~1121/~1132/~1167 — keep `broadcast:false` to match today;
      session-exit at ~1398 with `touch:true`) and the transcript-watcher calls.
      Delete the wrapper methods. Add the structural guard: a test asserting
      `store.UpdateState` has no non-test callers outside `session_state.go`.
      While folding, grep the whole daemon package for other writers you may
      have missed (`grep -rn 'store\.UpdateState' internal/daemon`) — fold every
      hit, don't leave stragglers.
- [ ] PR 3 — shrink AGENTS.md #3. Replace the "Classifier Timestamp Protection"
      pattern text with two sentences pointing at `applyState` as the invariant
      owner.
- [ ] Optional PR 4 — `stopdecision`. Extract the non-LLM parts of
      `classifySessionState` (pending-todo fast path, capability gates,
      empty-message → idle, parse-error → unknown) into a pure
      `Decide(session, transcriptText, classifier) (state, ok)` module so the
      WAITING/DONE decision is unit-testable without driving the 110-line daemon
      method.

## Decisions

- Store keeps ONE write method (timestamped CAS); "un-guarded" callers pass a
  fresh `time.Now()`, which is semantically identical to today's unguarded write.
  Rejected: keeping two store methods — that is exactly the two-doors bug source.
- The recovery-path writes keep their no-broadcast behavior (`broadcast:false`)
  rather than being "fixed" to broadcast: startup reconcile runs before clients
  attach and batch-reports via its own path. Do not change it in this refactor.
- `sendOK`/transport replies stay out of `applyState` — the door is
  transport-agnostic.

## Verification

Run after each PR:

```bash
go build ./...
go test ./internal/daemon -run 'TestSessionStateDoor|State|Classif|Nudge|Recover|Reconcile' -count=1
go test ./internal/daemon -count=1          # full package; do NOT use -race on the
                                            # whole package (known pre-existing race
                                            # in TestGitStatusScheduler; scope -race
                                            # with -run if you need it)
go test ./internal/store ./internal/classifier ./internal/transcript -count=1
```

Structural asserts (all must hold when PR 2 lands):

```bash
# the door is the only writer:
grep -rn 'store\.UpdateState' internal/daemon --include='*.go' | grep -v _test | grep -v session_state.go
# -> no output
# the old choreographies are gone:
grep -rn 'updateAndBroadcastState' internal/daemon --include='*.go' | grep -v _test
# -> no output
```

Behaviors that must survive (spot-check via existing tests + manual `make dev`):

1. Stale classifier result never overwrites a fresher state (existing
   classifier tests must stay green unmodified — if one needs editing, the
   refactor changed behavior).
2. PTY detector still refuses to clobber hook-authoritative states
   (`ShouldApplyPTYState` tests unmodified).
3. Ticket doorbell still fires when a PTY-observed session goes idle, and only
   on that path.
4. Nudge countdown still cancels on leaving idle for hook AND PTY paths.
5. Long-run tracking: `working` starts a run; `idle`/`scheduled` clears it, on
   every path that did so before.
6. Manual: `make dev`, run a Claude session, confirm the sidebar state flow
   launching → working → waiting_input/idle behaves normally, and approval
   (pending_approval) still clears.

## Follow-ups

- The store change-event candidate (see
  `2026-07-02-store-single-implementation.md` follow-ups) generalizes this
  one-door pattern to all entities; `applyState` becomes its first client.
