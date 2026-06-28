# Plan: Reload the agent when chief-of-staff is assigned or removed

## Goal

ChiefGuidance (the chief operating system prompt: delegate → arm
`attn ticket inbox --watch` → backstop) only reaches an agent at **agent-launch
time** (`internal/agent/claude.go` `BuildCommand` → `--append-system-prompt`;
Codex → `developer_instructions`). A session promoted to chief **after** its
agent launched never re-runs that path, so a freshly-promoted chief runs
**without** its guidance — and a demoted chief keeps it. Force a **resume-
preserving agent reload** on chief assign **and** remove so the launch path
re-runs with the correct chief status, while keeping the conversation.

This is the delivery mechanism that makes the delegated-ticket-awareness work
(PR #436) actually reach a promoted chief. Without it the guidance is present
only by luck (whenever the agent next happens to relaunch).

## Decisions (settled with Victor 2026-06-28)

- **Full daemon-side reload.** The daemon performs kill + resume-respawn itself
  and tells the frontend to reattach via a NEW protocol event. Chosen over
  "daemon-decides / frontend-executes" with eyes open: it adds a protocol event
  + version bump + a frontend reattach handler + daemon geometry capture, but
  keeps the reload authoritative in the daemon (works for any/no client, future
  headless/cron promotion).
- **Reload immediately, even mid-turn.** No idle/waiting guard. Killing a working
  agent loses its in-flight turn; resume restores the transcript up to the last
  completed turn. Accepted.
- **Symmetric: reload on assign AND demote.** Assign injects ChiefGuidance;
  demote drops it (agent falls back to normal workspace-context guidance).
- **Resume, never cold-restart.** Promotion/demotion must not wipe context.
- **Silent reload — no nudge** (Victor 2026-06-28). Drop the doorbell entirely;
  `activateChiefGuidanceLive` is retired, not replaced. Guidance lives in the
  re-injected system prompt, so the agent knows it is chief; it acts chief-ly (and
  arms its watch) on the next user turn. No typed orient prompt on toggle.
- **No duplication.** Extract a shared spawn-runtime builder from
  `handleSpawnSession` and call it from both the WS handler and the reload path.
- **Source launch params from the worker registry, not the client.** The daemon
  does NOT persist `YoloMode` or the executable paths — they arrive on each spawn
  via the client `SpawnSessionMessage` and otherwise live only in the live worker.
  Resume (`-r`) restores the transcript, NOT launch flags, so a defaulted respawn
  silently drops `--dangerously-skip-permissions` (a yolo chief comes back asking
  permission on everything) — the exact "hide a symptom" failure AGENTS.md forbids.
  Fix: extend `ptyworker.RegistryEntry` (already worker-written, daemon-read on
  recovery) with `YoloMode` + the executable paths; the worker populates them from
  its `runtime.Config`; `reloadSessionAgent` reads them back. Single source,
  restart-safe, client-independent (so headless/cron promotion works — which is
  exactly Victor's daemon-side rationale). No in-memory cache (it loses the params
  across a daemon restart — the common long-lived-chief case).
- **Abort on opts miss; never respawn with defaults.** If the registry entry is
  missing or required launch params can't be resolved, `reloadSessionAgent` LOGS and
  ABORTS — it does not kill the live worker. A failed reload that preserves the
  working agent beats a "successful" one that drops yolo.
- **Serialize reloads per session (`reloadLockFor`).** The `kill → remove → spawn`
  composite is held under a per-session mutex. Without it, two concurrent reloads of
  the same id (a rapid double-toggle, or a role transfer reloading both chiefs)
  interleave and the Spawn loser hits "already exists", whose failure path tears down
  the freshly-respawned agent. Latest-wins falls out for free: the later reload kills
  the in-flight respawn and re-spawns with the session's current chief status.
- **A role transfer reloads BOTH chiefs.** Promoting B while A still holds the role
  demotes A via the single-holder upsert; `handleSetChiefOfStaff` reloads A *and* B
  so the displaced chief drops its guidance now, not whenever it next restarts
  (the symmetric-demote intent applied to the transfer case). Different ids ⇒
  different reload locks ⇒ they run concurrently.
- **Ship as one PR** (Victor 2026-06-28: minimize PR count; the <1k cap does not
  apply here). The spawn refactor, `reloadSessionAgent`, the protocol bump, and the
  frontend reattach handler land together. The refactor is the first commit (kept a
  separate, reviewable commit with green spawn/resume tests) but not a separate PR.

## Why this happens (confirmed)

```
spawn_session (ws_pty.go:428 handleSpawnSession)
  -> ptyBackend.Spawn (worker backend)
    -> pty.Manager.Spawn (manager.go:154)  builds: sh -l -c "exec attn [--resume ID] ..."
      -> cmd/attn runAgentDirectly (main.go:1923)
        -> resolveChiefNotebookRoot(c, sessionID)  (main.go:2014)  <-- chief check
          -> opts.NotebookRoot set IFF session is chief NOW
            -> BuildCommand injects ChiefGuidance --append-system-prompt  (claude.go:92)
```

- The `attn pty-worker` (and the `claude`/`codex` inside its PTY) is owned by the
  **daemon**, not the app; it **survives app quit**. App relaunch re-attaches, it
  does not re-spawn. (Verified: chief worker pid survived an app quit, parent =
  daemon.)
- Daemon-restart recovery **RE-ATTACHES** to the surviving worker
  (`internal/ptybackend/worker.go:824-856`); it does not re-run the launch path.
- `handleSpawnSession` **short-circuits to success without respawning** when the
  worker is already live (`ws_pty.go:544-556`). So a reload is necessarily
  **kill → respawn**.
- `handleSetChiefOfStaff` (chief_of_staff.go:135) sets the role then fires
  `activateChiefGuidanceLive` (notebook.go:221), which only **types a doorbell**
  pointing at the notebook — its own comment (notebook.go:206-214) admits "a live
  promotion can't reach the system prompt." That is the gap this plan closes.
- A re-spawn of an existing session **auto-resolves the resume id**
  (`ws_pty.go:483-491` `ResolveSpawnResumeSessionID`), preserving the transcript
  (agent-agnostic).

## Frontend reattach constraint (why a new event is needed)

- Frontend PTY attach is **always frontend-initiated** via `spawnPtyRuntime`
  (`app/src/pty/runtimeLifecycle.ts:43`); there is no daemon→frontend "runtime
  replaced" signal today.
- The only lifecycle event is `session_exited` (`constants.go`), which the
  frontend handles by `clearRuntime(id)` (`useDaemonSocket.ts:2046`) — dropping it
  from the attached set and showing a dead pane. App-reconnect won't recover it.
- `pty_desync` is the only existing auto-reattach, and it's stream-sync recovery,
  not process replacement.
- ⇒ A daemon kill+respawn leaves the frontend unaware. Full daemon-side reload
  therefore REQUIRES a new `runtime_respawned` event + a frontend handler, and the
  reload-kill's `session_exited` must be suppressed to avoid a dead-pane flash.

## Architecture Map

```text
Target:
user toggles chief in UI
  -> sendSetChiefOfStaff(id, on/off)            (useDaemonSocket.ts:3532)
    -> daemon handleSetChiefOfStaff (chief_of_staff.go:135):
         set/clear role; broadcastSessionsUpdated; reply chief_of_staff_result
         go d.reloadSessionAgent(id)            <-- NEW (replaces activateChiefGuidanceLive)

daemon reloadSessionAgent(id):                  <-- NEW (internal)
  0. GUARD: only agents whose launch path injects chief guidance (claude, codex —
     i.e. non-plugin agentdriver agents). Plugin-driver agents gain nothing from a
     reload (no append-system-prompt / developer_instructions) and carry the
     LifecycleID exit-gating complexity — skip + log for them.
  1. session := store.Get(id); abort+log if nil.
  2. opts, err := buildReloadSpawnOptions(session)   <-- reads ptyworker registry for
       YoloMode + executable(s) (NOT in the store); agent/cwd from session/registry;
       cols,rows from SessionInfoProvider.SessionInfo(id); resume id via
       ResolveSpawnResumeSessionID(...). If err (registry miss / unresolved params):
       ABORT — log, do NOT kill the live worker (Decisions: never respawn defaulted).
  3. markReloading(id)   // set BEFORE kill so the async old-worker exit is suppressed
  4. ptyBackend.Kill(id, SIGTERM)   // BLOCKS until child dead (backend.go:124; SIGKILL @10s)
  5. ptyBackend.Remove(id)          // synchronous, before Spawn (idempotent; mirrors
                                    // terminateSession). The async exit's later Remove no-ops.
  6. ptyBackend.Spawn(opts):        // resolveChiefNotebookRoot re-runs in the new `attn` child
       on SUCCESS: broadcast runtime_respawned{ id }.   <-- NEW event
                   Do NOT clear the flag here — handlePTYExit consumes it when the old
                   exit fires (see below). Safety: time.AfterFunc(5s) clearReloading(id)
                   so a never-arriving exit can't wedge the flag.
                   (Store row is untouched by Kill/Remove; suppressing the exit's
                   idle-clobber preserves prior State — the detector re-derives it.)
       on FAILURE: clearReloading(id); broadcast the REAL session_exited{ id } + minimal
                   cleanup so the UI degrades to the dead-pane state (Blocker 2: never
                   leave a live pane over a dead session). The old exit was already
                   suppressed, so this is the sole session_exited.

frontend on runtime_respawned (useDaemonSocket.ts):     <-- NEW handler, MIRRORS pty_desync
  // pty_desync sends attach_session DIRECTLY over the ws (not via spawnPtyRuntime), so
  // it never hits the alreadyAttached fast-path (runtimeLifecycle.ts:51) — Blocker 1
  // is sidestepped, not just avoided. clearRuntimeStream keeps the runtime ATTACHED
  // (resets seq/replay cache only), so the re-attach re-establishes the stream.
  -> recordDiag({kind:'runtime_respawned', ...}); emitPtyEvent({event:'reset', id, reason:'respawn'});
     ptyTransport.clearRuntimeStream(id);
     ws.send({cmd:'attach_session', id, attach_policy:'relaunch_restore'})  // explicit replay

handlePTYExit (daemon.go:1351):                 <-- consume-on-suppress guard
  if d.consumeReloading(info.ID) {   // atomic check-and-delete
    log "suppressed exit for reloading session"; return   // skip ALL exit processing:
  }                                  // no idle-clobber, no Remove (reload owns it), no
                                     // session_exited. Placed near the top.
```

## Refactor (no duplication)

`handleSpawnSession` (ws_pty.go:428-700ish) currently inlines: agent normalize,
resume-id resolution, initial-prompt file, `SpawnOptions` construction, the live
short-circuit, the post-spawn `store.AddChecked` + state seeding. Extract the
reusable core so the reload path reuses it:

```go
// new: builds SpawnOptions for an EXISTING session id (resume-preserving) and
// seeds the store row. No client, no workspace-pane status. Used by reload.
func (d *Daemon) buildExistingSessionSpawnOptions(sessionID string, cols, rows uint16) (ptybackend.SpawnOptions, *protocol.Session, error)
func (d *Daemon) reloadSessionAgent(sessionID string)   // kill + spawn + restore row + broadcast
```

`handleSpawnSession` keeps its WS-specific concerns (client replies, workspace
pane status, plugin drivers) and delegates the option-building/store-seeding to
the shared helper where they overlap.

## Data Model / Protocol

New event (CLAUDE.md critical pattern #1 — protocol versioning):

```tsp
// internal/protocol/schema/main.tsp
// daemon -> frontend: a session's agent process was replaced in place (reload);
// the client should reattach to the new runtime.
model RuntimeRespawnedMessage { event: "runtime_respawned"; id: string; }
```

Checklist: edit main.tsp → `make generate-types` → add `EventRuntimeRespawned`
to `internal/protocol/constants.go` → **increment `ProtocolVersion`** → `make install`.

Daemon reload state: `map[string]bool` `reloadingSessions` + mutex.
`markReloading`/`consumeReloading` (atomic check-and-delete)/`clearReloading`.
`handlePTYExit` calls `consumeReloading` to suppress + own the old exit.

Worker registry extension (`internal/ptyworker/registry.go` `RegistryEntry`):
the worker already writes a per-session registry the daemon reads on recovery
(`worker.go` recover path). It carries agent+cwd+PIDs+controlToken but NOT the
launch flags. Add the launch params the daemon can't otherwise source:

```go
type RegistryEntry struct {
  // ...existing: DaemonInstanceID, SessionID, WorkerPID, ChildPID, SocketPath,
  //              Agent, CWD, ControlToken...
  YoloMode          bool
  Executable        string   // selected CLI path for Agent
  ClaudeExecutable  string
  CodexExecutable   string
  CopilotExecutable string
}
```

Worker populates these from its `runtime.Config` (`ptyworker/runtime.go` already
holds YoloMode/Executable/…) when it writes the entry. `reloadSessionAgent` reads
the entry by the same registry-path resolution recovery uses. Registry miss ⇒
abort the reload (Decisions).

## Boundaries

- Daemon owns the chief **role**, the reload orchestration (kill+respawn), geometry
  capture (`SessionInfoProvider`), and the `runtime_respawned` broadcast.
- Frontend owns reattach + geometry reconciliation on receiving the event (reuses
  `relaunch_restore` attach policy + `pty_resize` authority).
- Reload **must resume** (transcript preserved) and re-run the launch path so chief
  status is re-evaluated.

## Implementation Steps

- [x] Protocol: add `RuntimeRespawnedMessage` + `EventRuntimeRespawned`; regen
      types; bump `ProtocolVersion` (131→132, incl. frontend `PROTOCOL_VERSION`).
- [x] Registry: extend `ptyworker.RegistryEntry` with `LaunchParamsRecorded` +
      yolo/executable(s); worker populates from `runtime.Config`. Kept `Version: 1`
      (recovery hard-requires it); `LaunchParamsRecorded` distinguishes new entries.
      Backend accessor `WorkerBackend.SessionLaunchParams` reads it.
- [x] Daemon refactor: NOT a big extraction — `buildReloadSpawnOptions` sources
      from the registry (not a client msg), reusing existing shared helpers
      (`ResolveSpawnResumeSessionID`, `normalizeSpawnAgent`); no `handleSpawnSession`
      body duplicated.
- [x] Daemon: `reloadSessionAgent(id)` (`internal/daemon/reload.go`) — guard
      (non-plugin agent + live worker), build opts (abort on opts miss), mark
      reloading, `Kill` → `Remove` (sync) → `Spawn` (resume), broadcast
      `runtime_respawned`; respawn-failure path emits the real `session_exited`.
- [x] Daemon: `handlePTYExit` `consumeReloading` (atomic) suppresses ALL exit
      processing for a reloading session.
- [x] Daemon: `handleSetChiefOfStaff` calls `reloadSessionAgent(id)` on BOTH
      assign and demote; **deleted** `activateChiefGuidanceLive` +
      `notebookActivationPrompt` (silent reload — Decisions) and their tests.
- [x] Frontend: `runtime_respawned` handler mirrors `pty_desync` (reset +
      `clearRuntimeStream` + direct `attach_session` w/ `relaunch_restore`).
- [x] Tests:
      - daemon (`reload_test.go`): resume + yolo + executable + geometry preserved;
        kill→remove→spawn order; `runtime_respawned` emitted; `session_exited`
        suppressed; abort on unrecorded params / dead worker / unsupported agent;
        respawn-failure emits `session_exited`; assign AND demote reload; concurrent
        same-session reloads serialized (no tear-down, exactly one live worker —
        deterministic via a Spawn rendezvous); role transfer reloads both chiefs.
        Race-clean.
      - frontend (`useDaemonSocket.test.tsx`): `runtime_respawned` → `attach_session`
        w/ `relaunch_restore`, `onSessionExited` NOT called.
      - real-agent benchmark (`scenario-chief-ticket-watch.mjs`): DROPPED the
        app-relaunch hack — polls `chiefGuidanceProcesses()` after promote.

## Verification (turn assumptions into assertions)

- **Mid-turn resume — assert, don't hope.** SIGTERM during an in-flight turn then
  `-r` resume: capture what the resumed transcript actually contains. The benchmark
  must assert the resumed pane redraws cleanly via replay (no manual interaction)
  AND record whether the killed turn's last user message comes back answered or
  unanswered. Victor accepted losing the in-flight turn; this proves what "losing
  it" concretely is, rather than assuming.

## Follow-ups

- **(Deferred, conscious) Per-spawn exit-suppression key.** The reloading flag is
  session-keyed (`map[string]bool`), so a theoretical cross-talk window survives the
  per-session lock: if the old worker's `onExit` is starved past reload-1's entire
  Remove+Spawn+unlock AND into a reload-2 `Kill(new-A)`, new-A's exit could find the
  flag already consumed → unsuppressed → `removePTYSession` tears down new-B. Requires
  GOMAXPROCS pressure + a multi-toggle, and `Spawn` is hundreds of ms of scheduling
  points, so accepted for now. Principled fix when warranted: gate the exit on a
  per-spawn generation/`LifecycleID` instead of a session-keyed boolean — the codebase
  already does exactly this for plugin runs.
- Folded into PR #436 (Victor 2026-06-28: minimize PR count). The reload is #436's
  delivery mechanism — #436's guidance is dead-on-arrival for the promote-a-running-
  session case without it — so they ship as one complete feature on the
  `feat/delegated-ticket-awareness` branch. Sequence as readable commits: spawn
  refactor first (green spawn/resume tests), then reload + protocol bump + frontend.
- The real-agent benchmark + the focus-free PTY readiness helper
  (`ensureClaudePromptReadyViaPty` / `ensureCodexPromptReadyViaPty`) are uncommitted
  on this branch; fold them into this PR (own commit) and drop the app-relaunch hack
  once reload works. They validate the end-to-end behavior.
```
