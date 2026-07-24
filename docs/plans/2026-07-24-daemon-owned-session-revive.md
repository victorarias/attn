# Daemon-owned session revive

Status: approved direction (Victor, 2026-07-24); phases 0–5 below
Date: 2026-07-24 (revised same day after design review)
Branch: fix/recovery

## Problem

After a machine restart, the daemon correctly marks dead-but-resumable sessions
`recoverable`, but the terminal pane dead-ends on
`[Failed to attach PTY: Error: session not found: <uuid>]` and the user must
manually reload each session.

The interim fix on this branch works (frontend detects the attach failure by
substring-matching the error string and calls `reloadSession`), but it is held
together by coincidences:

- The revive decision is made in the frontend, the layer with the least
  information, by parsing an error string the daemon flattened.
- Launch intent (model, effort, executable, yolo) is not durable. It lives in
  the client's spawn message and the per-daemon worker registry — exactly what a
  machine restart destroys.
- The `recoverable` flag is cleared by accident: a fresh spawn record leaves
  `Recoverable` nil, `omitempty` drops the field from the broadcast, and the
  frontend's wholesale session replace turns the omission into `false`.

## Guiding principles

1. **Refactor to make the fix easy, at every level.** Each phase leaves the
   codebase better than it found it; the revive feature lands on refactored
   ground, not on top of the existing lumps.
2. **The daemon owns session lifecycle state and launch intent.** Client state
   is UI-only; the client contributes to lifecycle operations only what it
   genuinely owns (terminal geometry, focus, viewport). No lifecycle decision
   may depend on client-side memory of how a session was launched.

New refactoring opportunities found mid-phase get noted in "Follow-ups" below
and discussed with Victor before acting.

## Verified facts this design rests on (2026-07-24)

- WebSocket commands are serial per client (one pump goroutine per connection)
  but concurrent across clients. Two clients can race spawn/attach on the same
  session id.
- `handleSpawnSession`'s "worker already exists" check is check-then-act with
  no lock spanning check and spawn. The worker backend masks this by reserving
  the id under its mutex; the embedded backend has a real double-launch race
  (`internal/pty/manager.go` checks, unlocks, launches, inserts late).
- The attach handler holds no lock across `ptyBackend.Attach`; there is no
  daemon-wide mutex. Calling spawn logic from the attach path is lock-safe.
- `AttachPolicy` is already a schema enum whose values collide with no other
  enum, so adding a value does not trigger the quicktype enum-merge trap.
- Migration numbering: branch and origin/main both tip at 77; prod and dev DBs
  verified at 77.
- The persisted `Session` row carries id/agent/cwd/workspace/label only; all
  other launch inputs live in the client spawn message or the per-daemon worker
  registry (which dies with the machine). `reload.go` aborts in-place reload
  when the registry is gone.

## Phases

Independently shippable, in order. Each phase = its own PR with its own tests
and verification tier.

### Phase 0 — spawn single-flight

Fix the pre-existing same-session spawn race before anything builds on spawn.

- Per-session in-flight guard in the daemon (map keyed by session id):
  concurrent same-id spawns join or wait for the first result instead of
  racing check-then-act into the backend.
- Fixes exposure for both backends; the embedded backend's late-insert race
  stops being reachable through the daemon.
- Tests: concurrency test that two simultaneous spawns of one id yield one
  backend spawn and two consistent results.
- Verification: Go tests; daemon-tier.

### Phase 1 — spawn pipeline refactor

`handleSpawnSession` (~400 lines) becomes a staged, client-free pipeline.
This is the load-bearing refactor; revive, reload, and future entry points
become alternate entries into a legible transaction.

- **Characterize first:** golden daemon-level test(s) capturing current spawn
  behavior (success path, live-worker early return, resume downgrade, failure
  restoration) BEFORE moving code. The behaviors-must-survive list is written
  by the lead and pinned in the phase brief.
- **Stages (names indicative):** validate/normalize → resolve intent (resume
  selection + downgrade, executable/model/effort, chief/plugin prep) →
  backend spawn → commit (persistence, watcher/workspace/layout effects,
  broadcasts). Each stage a named function with explicit inputs/outputs;
  failure paths restore state exactly as today.
- The websocket handler becomes a thin wrapper: parse message → run pipeline →
  send `SpawnResultMessage`. The pipeline sends nothing to any client.
- No behavior change in this phase. The self-id resume downgrade added on this
  branch rides along inside "resolve intent".
- Verification: golden tests green before and after; live smoke on a throwaway
  profile (spawn, reload, resume).

### Phase 2 — durable launch intent

- Migration **78**: `ALTER TABLE sessions ADD COLUMN launch_intent TEXT NOT
  NULL DEFAULT ''` — a single JSON column mirroring a Go struct (decided:
  JSON over discrete columns; evolvable without further migrations).

  ```go
  // LaunchIntent captures the per-spawn parameters the daemon needs to
  // relaunch a session without a client. Geometry is deliberately absent:
  // the attaching client owns it.
  type LaunchIntent struct {
      YoloMode     bool   `json:"yolo_mode,omitempty"`
      Executable   string `json:"executable,omitempty"`
      Model        string `json:"model,omitempty"`
      Effort       string `json:"effort,omitempty"`
      ChiefOfStaff bool   `json:"chief_of_staff,omitempty"`
  }

  func (s *Store) SetLaunchIntent(id string, intent LaunchIntent)
  func (s *Store) LaunchIntent(id string) (LaunchIntent, bool)
  ```

- The pipeline's commit stage persists intent on spawn success. Store becomes
  the durable source of truth; the worker registry remains a cache for live
  in-place reload. `reload.go`'s "abort when registry gone" becomes "fall back
  to the store".
- Not exposed on `protocol.Session`; daemon-internal per principle 2.
- Resume ids already persist (`sessions.resume_session_id`); unchanged.
- Tests: store round-trip incl. empty-column backward compat; daemon test that
  spawn persists intent and a store reopen reads it back; reload-falls-back-
  to-store test.
- Verification: Go tests; daemon-tier plus live reload smoke.

### Phase 3 — `recoverable` becomes a real state

Decided: recoverable is a lifecycle state, not a flag riding beside `idle`.

- **Protocol (version bump):** `SessionState` enum gains `recoverable`;
  `Session.recoverable` bool is REMOVED in the same bump (skew fails
  explicitly, per protocol contract).
- **Daemon:** worker reconciliation (startup + deferred) transitions sessions
  to `state: recoverable` via `applyState` — the single writer. The spawn
  pipeline's normal `state: launching` transition replaces it on revive or
  manual reload; **there is no "clear" operation to get wrong anymore.**
- **Classifier guard:** `applyState` must reject classifier results arriving
  for a session in `recoverable` (a stale stop-classification must not
  overwrite it). Extends the existing stale-observation guard; add the state
  to every mutation-boundary guard surface.
- **Migration 79:** convert rows with `recoverable=1` to `state='recoverable'`;
  drop (or stop reading) the `recoverable` column.
- **Frontend:** sidebar badge and pane gating derive from
  `state === 'recoverable'`; delete the parallel `recoverable` prop threading.
- **Store:** `SetRecoverable` deleted; reconciliation uses `applyState` like
  every other transition.
- Tests: state-transition tests incl. classifier-rejection; migration test on
  a fixture DB with recoverable rows; frontend badge from state.
- Verification: protocol change — full non-production install on a throwaway
  profile (never dev), restart-with-live-sessions smoke.

### Phase 4 — revive as an attach policy

- **Protocol (can share Phase 3's bump if shipped together):**
  `AttachPolicy` gains `revive`; `AttachSessionMessage` gains optional
  `cols`/`rows` (required with `revive`); `AttachResultMessage` gains optional
  `revived: boolean`.
- **Daemon attach handler:** on backend attach failure with
  `pty.ErrSessionNotFound`, when policy is `revive` and the stored session is
  in `state: recoverable`: build the spawn from the stored record +
  `LaunchIntent` + attach geometry, run the spawn pipeline (single-flight from
  Phase 0 absorbs concurrent revives; resume downgrade applies), attach again,
  reply `attach_result{success: true, revived: true}`. Any other failure or
  policy: today's behavior unchanged.
- **Passive attachers unchanged:** harness scrollback observer, pty-desync
  repair, reconnect reattach, daemon tests — attach-only unless they opt in.
- Tests: daemon-level revive on recoverable+dead session (respawn, zero-turn
  downgrade, attach success, geometry reaches backend); no-revive without
  policy; no-revive when not recoverable; concurrent revive single-flight.
- Verification: full non-production install; daemon-restart-then-attach smoke.

### Phase 5 — frontend simplification + live proof

- Mounted pane attaches with `attach_policy: 'revive'` + measured geometry.
- Delete the interim recovery apparatus: `isSessionNotFoundError`'s revive
  branch, `recoveringRef`, `onRecoverSession` threading through `App.tsx` →
  `SessionTerminalWorkspace` → `useGhosttyPaneRuntime`. The pane states
  intent and renders results; recovery strategy lives in the daemon.
- Harness: `scenario-recoverable-auto-revive.mjs` asserts
  `state === 'recoverable'` before revive and the healthy pane + state
  transition after; promote to the scenario catalog.
- Verification: full packaged-app scenario green on a fresh
  `make install PROFILE=<throwaway>` (harness fingerprint needs full install);
  manual machine-restart-equivalent check (kill workers + daemon restart).

## Follow-ups (noted, not in scope; discuss with Victor)

- Route the manual Reload button through daemon revive (after Phase 2 the
  daemon owns everything reload needs except geometry). Resolved: new `reload_session`
  command (protocol 183); the daemon reuses the in-place reload composite for live
  workers and the stored-intent spawn pipeline for dead sessions, broadcasting
  `runtime_respawned`; the frontend Reload button now sends only `{id, cols, rows}`.
- `buildSpawnSessionRecord` does not populate `Session.EndpointID` from the
  spawn message (pre-existing gap). Resolved: the spawn record now takes the
  explicit message value or preserves the existing binding.
- `commitSpawn`'s final `AddChecked` rewrites the state snapshotted before Spawn,
  clobbering any state applied mid-spawn (e.g. the PTY working signal). Benign
  today only because fresh records carry launching; consider re-reading current
  state before the commit upsert. Resolved: commitSpawn re-reads current state
  before the commit upsert so mid-spawn transitions (e.g. the PTY working signal)
  survive; mutation-verified by test.
- Embedded backend's late-insert double-launch race in
  `internal/pty/manager.go` — masked via Phase 0 at the daemon layer; the
  backend-level race itself may deserve its own fix. Resolved: Spawn now reserves
  the session id in a pendingSpawns set under the manager lock before forking,
  so a concurrent same-id Spawn fails fast instead of double-launching.
- `runtimeLifecycle.ts` still encodes other recovery special cases
  (`relaunch_restore`, resume-after-attach-failure); candidates for the same
  daemon-owned treatment as revive. Resolved: the client-side resume-after-attach-failure
  branch and the kill_session `reload` flag were deleted (protocol 184);
  `spawnPtyRuntime` reduces to spawn + fresh attach, and reload/revive strategy
  lives entirely in the daemon. `relaunch_restore` remains client-side by design —
  it is a rendering policy (replay handling), not a recovery strategy.
- Live-verification footgun (2026-07-24): in a non-fish shell, a failed
  `profile-env` selection left `make install PROFILE=` (empty expansion)
  targeting production twice (bundle overwritten; daemon untouched). Resolved:
  the Makefile now rejects an explicitly-passed empty `PROFILE=` at parse time
  for every goal; bare prod installs are unchanged.
- `LaunchIntent` gained an `Unattended bool` field beyond the approved surface:
  a store-fallback relaunch must refuse unattended sessions rather than relaunch
  them without their worker-registry launch contract. Flagged to Victor. Resolved:
  the interim refusal was replaced by persisting the full
  `launchcontract.UnattendedLaunchSpec` in `LaunchIntent` (approved by Victor
  2026-07-24), so store-fallback relaunches carry the complete launch contract
  instead of refusing; the refusals were removed.

## Branch state note

The uncommitted interim fix on this branch (frontend revive hook + self-id
resume downgrade + harness scenario) is scaffolding: the downgrade and the
scenario survive into the phases; the frontend hook is deleted in Phase 5.

## Progress ledger

- [x] Phase 0 — spawn single-flight (2026-07-24): refcounted per-session lock in
  `internal/daemon/spawn_lock.go`; one call site in `handleSpawnSessionWithPolicy`
  after workspace validation. Red-proven (`TestConcurrentSameSessionSpawnsSpawnOnce`
  fails 5/5 without the lock), `-race` green, live-verified with two concurrent
  WS clients on a throwaway profile.
- [x] Phase 1 — spawn pipeline refactor (2026-07-24): handler decomposed into
  client-free stages in `internal/daemon/spawn_pipeline.go`
  (`validateSpawnPrelock` → `normalizeSpawnRequest` → `resolveSpawnIntent` →
  `executeSpawn` → `commitSpawn`); handler is a thin wrapper mapping
  `spawnRejection`/`spawnOutcome` to client sends. 17-test golden
  characterization suite (`spawn_characterization_test.go`) written first;
  review caught and fixed one ordering regression (plugin prep must stay after
  the live-worker check — now pinned by
  `TestSpawnCharacterizationAlreadyLivePluginRespawnSkipsPluginPrep`). Live
  smoke on throwaway profile: fresh spawn, reload, real Claude turn +
  `--resume` pass-through, duplicate-spawn no-op all verified.
- [x] Phase 2 — durable launch intent: migration 78 + LaunchIntent API + commitSpawn persist + reload store-fallback with unattended refusal; unit boundary tests pin the dead-worker (ErrSessionNotFound → fallback) and corrupt-registry (abort) routes; live smoke on profile smoke2li proved persist → daemon restart → reload fallback log line → respawn with --model sonnet --effort high. Note: the live run exercised the unrecorded-params fallback route, not the dead-worker route (worker kept alive; registry params stripped); the dead-worker route is unit-pinned and the full machine-restart end-to-end lands in Phase 5's packaged-app scenario.
- [x] Phase 3 — recoverable as a real state: unit + live scenario PASS
  (`recoverable-auto-revive-2026-07-24T13-12-26-343Z`), including the
  revive-state spawn-record fix found by the live run.
- [x] Phase 4 — revive attach policy: 5 unit tests + scripted WS revive smoke
  `revived:true` + protocol-182 scenario pass (2026-07-24 13:31).
- [x] Phase 5 — frontend simplification + live proof: TypeScript + 181-file
  frontend suite green; packaged `recoverable-auto-revive` passed after a full
  `p3-recoverable` install (2026-07-24 15:49), with Cmd+T and utility-focus
  scenarios also passing.
