# Plan: One daemon door for session state

Re-anchored on 2026-07-13 at `7d3cb647`. The original 2026-07-02 diagnosis is
still correct, but its proposed single timestamp-CAS store method predates the
ticket-doorbell fence and sequenced plugin state reports.

## Why / Alignment

Session state is a daemon-level transition, not merely a store field update. A
successful transition may update long-run tracking, refresh `last_seen`, fence
ticket-doorbell delivery, broadcast the decorated session, and recompute its
workspace. Those responsibilities are still spread across several callers, so
new invariants have had to be patched into each choreography by convention.

We are aligned on preserving the original one-door goal with these corrections:

- The door lives in `internal/daemon/session_state.go` and is the only daemon
  module allowed to invoke a store session-state mutation.
- Callers identify a closed semantic cause rather than assembling a bag of
  boolean options. The cause selects one valid commit rule and effect profile.
- The store keeps its distinct atomic primitives: unconditional state write,
  classifier timestamp CAS, and plugin run/sequence CAS. They protect different
  invariants and must not be collapsed into a timestamp fiction.
- `applyStateAndSyncNudge` is absorbed into the door. For nudge-aware causes,
  the state commit remains serialized with the complete doorbell write via
  `doorbellMu`; nudge reconciliation still runs after releasing the lock.
- Startup recovery remains silent and process exit preserves its pre-clobber
  ticket reconciliation. Centralization must not homogenize intentionally
  different lifecycle behavior.

This chunk is a behavior-preserving daemon refactor. It does not change state
semantics, nudge eligibility, plugin sequencing, recovery broadcasts, or exit
classification. Typed store change events, the broader daemon broadcast pump,
and extraction of classifier decision logic are deferred.

## Architecture Map

```text
Current:
hook / PTY
  -> duplicated run tracking
  -> applyStateAndSyncNudge(UpdateState + Touch)
  -> duplicated session broadcast + workspace recompute

transcript / long-run review
  -> updateAndBroadcastState
  -> duplicated run tracking
  -> applyStateAndSyncNudge(UpdateState)
  -> duplicated broadcast

classifier
  -> updateAndBroadcastStateWithTimestamp
  -> applyStateAndSyncNudge(UpdateStateWithTimestamp)
  -> duplicated accepted-only effects

plugin report
  -> applyStateAndSyncNudge(ApplyAgentDriverState)
  -> duplicated accepted-only tracking + Touch + broadcast

startup recovery -> five raw UpdateState calls -> later batch workspace reseed
process exit      -> pre-clobber ticket reconcile -> raw UpdateState + broadcast

Target:
source-specific trust / lifecycle guard
  -> d.applyState(sessionStateChange{cause: <closed cause>})
       -> select the cause's atomic store commit
       -> if nudge-aware: commit under doorbellMu
       -> reject? stop; no side effects
       -> accepted effect profile
            Touch (where current source does)
            long-run tracking (for live observations)
            syncNudgeForState (for live/nudge-aware sources)
            broadcastSessionStateChanged + workspace recompute (unless recovery)

Tests:
session_state_test.go
  -> real in-memory SQLite store + daemon broadcast recorder
  -> table of causes x accepted/rejected commit
  -> existing concurrency, recovery, plugin, classifier, and lifecycle tests
```

## Transition Model

Use package-private cause variants so callers can express only meaningful
combinations. Exact names may move slightly during implementation, but preserve
this shape:

```go
type sessionStateChange struct {
    sessionID string
    state     string
    cause     sessionStateCause
}

type sessionStateCause interface { isSessionStateCause() }

type liveSignal struct{}                         // hook or trusted PTY signal
type daemonObservation struct{}                  // transcript watcher / long-run handoff
type classifierObservation struct{ observedAt time.Time }
type pluginReport struct{ runID string; seq uint64 }
type startupRecovery struct{}
type processExit struct{}

// applyState is the only daemon door for a persisted session-state transition.
// It returns false when the session is missing or the cause-specific CAS rejects.
func (d *Daemon) applyState(change sessionStateChange) bool
```

The variants are a Go sum-type approximation. Do not replace them with exported
flags such as `touch`, `broadcast`, or `ticketNudge`; allowing callers to invent
new combinations recreates the problem this module is meant to solve.

### Cause policy

| Cause | Current producers | Atomic store commit | Touch | Run tracking | Nudge fence + sync | State/workspace broadcast |
| --- | --- | --- | --- | --- | --- | --- |
| `liveSignal` | hook, trusted PTY | unconditional | yes | yes | yes | yes |
| `daemonObservation` | transcript watcher, immediate long-run-review handoff | unconditional | no | yes | yes | yes |
| `classifierObservation` | slow classifier result | timestamp CAS at `observedAt` | no | accepted only | accepted only | accepted only |
| `pluginReport` | authorized plugin state/stop report | active-run + increasing-sequence CAS | yes | accepted only | accepted only | accepted only |
| `startupRecovery` | prune and worker reconciliation | unconditional | no | no | no | no; batch reseed remains authoritative |
| `processExit` | PTY exit idle-clobber | unconditional | yes | no; exit cleanup already owns it | no; do not re-arm a dead PTY | yes |

`working` starts long-run tracking when needed. `idle` and `scheduled` clear it.
Other states leave it unchanged, matching current behavior. Effects occur only
after an accepted commit; a stale classifier result, stale plugin sequence, or
missing session produces no touch, tracking, nudge, or broadcast effects.

### Apply order and concurrency

```text
derive commit + effect profile from cause
if cause is nudge-aware:
    doorbellMu.Lock
commit state using the cause-specific store primitive
if cause is nudge-aware:
    doorbellMu.Unlock
if rejected: return false

Touch if required
update long-run tracking if required
syncNudgeForState if required       # outside doorbellMu; may inspect tickets/arm timer
broadcastSessionStateChanged if required
return true
```

Only the state commit participates in the doorbell fence. Once
`pending_approval` is committed, a competing `typeDoorbell` acquires the same
lock, observes the new state, and refuses to type the prompt. Touch, tracking,
ticket reads, timer work, and broadcasts stay outside the critical section.

Change `store.UpdateState(id, state)` to return `bool`: true when a row was
updated, false for a missing session or database error. Existing callers may
ignore the result, while the state door can guarantee that rejected/missing
writes have no follow-on effects. Keep `UpdateStateWithTimestamp` and
`ApplyAgentDriverState` as separate bool-returning atomic primitives.

## Boundaries

- `session_state.go` owns cause-to-policy mapping, store state mutation,
  transition-derived long-run effects, nudge fencing/reconciliation, Touch, and
  invocation of the normal state/workspace broadcast.
- `handlePTYState` keeps `GetAgentDriverRun` / `pluginDriverReportsState` and
  `agentdriver.ShouldApplyPTYState` guards. They decide whether a PTY observation
  is trustworthy, before a transition exists.
- Plugin handlers keep connection authorization, run ownership validation, and
  payload validation. `pluginReport` carries the already-validated run/sequence
  cursor into the atomic commit.
- `classifySessionState` captures `classificationStartTime` before slow work and
  passes it as `classifierObservation.observedAt`.
- `handleState` keeps socket logging and `sendOK`; transport concerns do not
  enter the state module.
- `handlePTYExit` keeps teardown and `reconcileTicketsOnSessionEnd` before the
  idle transition, because ticket outcome depends on the pre-clobber state. Its
  unconditional lifecycle cleanup may clear long-run state independently of
  whether a session row still exists.
- Startup recovery still calls `reseedWorkspaceStatuses` after silent state
  rewrites and before clients cross the recovery barrier. Per-session recovery
  broadcasts remain forbidden.
- `broadcastSessionStateChanged` remains reusable by nudge-indicator changes;
  the state door owns calling it for accepted state transitions, not the helper
  itself.
- Tests may call store primitives directly to construct state. Non-test daemon
  code outside `session_state.go` may not call `UpdateState`,
  `UpdateStateWithTimestamp`, or `ApplyAgentDriverState`.

## Implementation Steps

- [x] Before changing production code, add characterization tests through the
      existing hook, PTY, long-run handoff, classifier, plugin, and exit entry
      points. Assert the full stored/effect outcome and make this suite pass on
      the pre-refactor implementation. During migration, do not weaken or
      rewrite these expectations to fit `applyState`.
- [x] Make `store.UpdateState` return whether it updated a session. Cover a
      successful write and a missing-session rejection without changing its
      unconditional timestamp semantics.
- [x] Add `internal/daemon/session_state.go` with the cause variants, one
      `applyState` entry point, cause-specific commits, accepted-only effects,
      and concise source-aware logging for rejected guarded writes.
- [x] Add `internal/daemon/session_state_test.go` with table-driven coverage for
      every cause profile. Assert stored state, `LastSeen`, long-run tracking,
      session/workspace broadcasts, and that rejected classifier/plugin writes
      produce no effects.
- [x] Fold the nudge-aware producers through the door:
  - [x] `handleState` and `handlePTYState` use `liveSignal`; the PTY path keeps
        only its trust guards before the call, while duplicated transition
        effects move into the door.
  - [x] Transcript-watcher line/tick results and the immediate long-run-review
        handoff use `daemonObservation`.
  - [x] Every classifier outcome uses `classifierObservation` with the captured
        start time.
  - [x] Plugin state and stop reports use `pluginReport`; preserve stale
        sequence/run rejection and handler return behavior.
- [x] Delete `updateAndBroadcastState`,
      `updateAndBroadcastStateWithTimestamp`, and
      `applyStateAndSyncNudge` once their last production callers are gone.
      Keep `syncNudgeForState` as the ticket-policy seam called by the door.
- [x] Fold lifecycle stragglers through explicit causes:
  - [x] All prune/recovery writes use `startupRecovery` and remain silent.
  - [x] The PTY-exit idle write uses `processExit` after pre-clobber ticket
        reconciliation and backend teardown.
- [x] Add a structural test using `go/parser`/`go/ast` that scans non-test Go
      files in `internal/daemon` and rejects calls to the three store state
      mutation methods outside `session_state.go`. This catches the plugin CAS
      path that the original grep proof missed.
- [x] Replace AGENTS.md "Classifier Timestamp Protection" with the stronger
      invariant: all daemon state transitions use `applyState`; classifier
      callers must still capture and pass the observation timestamp.
- [x] Run automated verification and packaged-app process lifecycle verification.
      The Codex nudge scenario reached `working` twice but could not complete
      because Codex emitted `task_complete` with `last_agent_message: null`, so
      no normal stop hook existed for attn to observe. No changelog entry is
      expected because the refactor has no intended user-visible behavior change.

Implement this as one PR with reviewable commits rather than landing a partial
door across several PRs. The invariant is only real once every production writer
and the structural guard land together.

## Decisions

- One daemon authority, multiple store commit primitives. Timestamp ordering and
  plugin run/sequence ordering are different concurrency models; forcing both
  through one store method would weaken correctness.
- Closed causes over caller-supplied effect flags. The source semantics choose
  the effect profile, so future writers must make an explicit architectural
  choice instead of copying a nearby boolean combination.
- Recovery and exit are first-class causes, not exceptions hidden in raw store
  calls. Their intentionally reduced effects are documented policy.
- The doorbell mutex protects only the state commit. A committed approval state
  is sufficient to block a competing doorbell; holding the mutex during DB
  Touch, ticket reads, timers, or broadcasts adds contention without safety.
- Accepted-only effects are the module invariant. Guarded writes already behave
  this way; making unconditional writes report missing-session failure prevents
  ghost tracking or broadcasts for a transition that never existed.

## Verification

```bash
go build ./...
go test ./internal/store -run 'UpdateState|ApplyAgentDriverState' -count=1
go test ./internal/daemon -run 'SessionStateDoor|DoorbellWrite|NudgeCountdown|Plugin.*State|StateWithTimestamp|ScheduledClears|Recover|Reseed|PTYState' -count=1
go test ./internal/daemon -count=1
go test ./internal/store ./internal/classifier ./internal/transcript -count=1
make test
```

Structural proof after migration:

```bash
rg -n 'd\.store\.(UpdateState|UpdateStateWithTimestamp|ApplyAgentDriverState)\(' \
  internal/daemon --glob '*.go' --glob '!*_test.go'
# -> only internal/daemon/session_state.go

rg -n 'updateAndBroadcastState|applyStateAndSyncNudge' \
  internal/daemon --glob '*.go' --glob '!*_test.go'
# -> no output
```

Behaviors that must survive:

1. A stale classifier result cannot overwrite a newer hook, PTY, transcript, or
   plugin state and cannot clear long-run-review tracking.
2. A stale/wrong-run plugin report changes neither state nor sequence and emits
   no transition effects.
3. A `pending_approval` commit cannot interleave into the middle of a complete
   bracketed-paste + Enter doorbell write; after commit, new doorbells are
   blocked. Leaving approval rechecks unread activity.
4. PTY detection still cannot clobber hook- or plugin-authoritative states.
5. Hook/PTY/plugin signals still Touch; transcript/classifier/recovery do not.
6. Recovery changes appear in the first corrected workspace snapshot without
   per-session recovery broadcasts.
7. PTY exit reconciles tickets from the pre-idle state, then broadcasts idle and
   `session_exited` in the existing lifecycle.
8. `working` starts long-run tracking; `idle`/`scheduled` clear it on the same
   live paths as today.

Live verification (non-prod profile):

1. Run `make dev` outside the sandbox.
2. Exercise a hook-backed Codex or Claude session through
   `launching -> working -> waiting_input/idle`, including an approval prompt.
3. Exercise an OpenCode/plugin-backed session and confirm sequenced native state
   reports update the sidebar while PTY-derived noise remains ignored.
4. Arm a ticket nudge, enter/leave approval, and confirm the countdown cancels
   and re-arms without typing into the approval prompt.
5. Restart the dev daemon and confirm recovery produces the correct initial
   session/workspace state without transient per-session recovery churn.

Recorded verification on 2026-07-13:

- `go build ./...`, the targeted store/daemon suites, the full daemon/store/
  classifier/transcript suites, repeated characterization/door tests, the
  scoped race suite, and `make test` passed.
- `make dev` built, signed, installed, and launched the isolated dev app.
- `real-app:scenario-workspace-shell-lifecycle` passed against the packaged dev
  app, covering live workspace panes and process-exit cleanup.
- `real-app:scenario-nudge-trigger` reached the live `working` transition on two
  runs. Both stopped before the nudge assertions because the spawned Codex CLI
  recorded `task_complete` with no assistant message; this is retained as an
  explicit live-verification gap rather than treated as passing evidence.

## Follow-ups

- Typed store change events plus a single daemon broadcast pump remain a separate
  design problem after the store single-implementation cleanup.
- Extracting the pure, non-LLM part of `classifySessionState` into a
  `stopdecision` module remains independent of the state door.
