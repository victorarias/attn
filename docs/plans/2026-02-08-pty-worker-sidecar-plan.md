# PTY Restart-Survival Design: Per-Session Worker Sidecars

Date: 2026-02-08  
Updated: 2026-02-11  
Status: Phases A-F implemented (worker backend default, embedded fallback retained)  
Owner: daemon/pty

## Phase F hardening targets (2026-02-11)

These are intentionally tracked as post-promotion hardening tasks and are now in active implementation:

1. Worker lifecycle/state propagation should be worker-pushed, not primarily poll-driven.
- Current issue:
  - backend poller currently performs frequent `info` RPCs for state/exit propagation.
  - this introduces control-plane load proportional to active session count and adds jitter around state transitions.
- Plan:
  - add a worker lifecycle watch stream (`watch`) that pushes state/exit events from worker to backend.
  - keep poller as liveness fallback (and compatibility path for older workers), not as primary state transport.

2. Worker backend default selection needs startup capability probe.
- Current issue:
  - backend selection proves directory/path setup but can defer runtime incompatibilities until first real spawn.
- Plan:
  - add worker startup probe in daemon boot path before committing to worker backend default.
  - on probe failure, surface warning and fall back to embedded backend immediately.

3. Ownership mismatch recovery should reclaim stale workers when safe.
- Current issue:
  - on `daemon_instance_id` ownership mismatch, recovery quarantines metadata but intentionally preserves the live worker process.
  - this avoids cross-daemon termination but can leave orphan worker processes after daemon crashes/restarts with rewritten identity metadata.
- Plan:
  - include daemon-owner lease metadata in worker registry (`owner_pid`, `owner_started_at`, `owner_nonce`).
  - during mismatch recovery, reclaim only when owner lease is provably stale; otherwise keep conservative quarantine-only behavior.
  - reclaim path should prefer authenticated worker RPC termination using the recorded owner identity/token, not blind PID kill.

## Progress update (2026-02-10)

Completed in this branch:

1. Added daemon-internal `internal/ptybackend` seam with an `embedded` adapter over existing `internal/pty.Manager`.
2. Switched daemon/websocket PTY command paths (`spawn`, `attach`, `input`, `resize`, `kill`, session listing/shutdown) to backend interface calls.
3. Added persistent `daemon_instance_id` stored at `<data_root>/daemon-id` and surfaced it in `initial_state`.
4. Added startup recovery-barrier scaffolding:
- daemon tracks `recovering` state
- PTY commands are rejected with `command_error` + `daemon_recovering` while recovering
- `initial_state` delivery is deferred until barrier lift
5. Added unit coverage for daemon ID persistence and recovery-barrier behavior.
6. Added `attn pty-worker` subcommand and worker runtime (`internal/ptyworker`) with:
- per-session PTY ownership
- JSONL RPC server (`hello/info/attach/detach/input/resize/signal/remove/health`)
- atomic registry file management
7. Added daemon-side worker backend adapter (`internal/ptybackend/worker.go`) and backend selection:
- `ATTN_PTY_BACKEND=worker` now activates worker mode
- default is now `worker` (`ATTN_PTY_BACKEND=embedded` override supported)
- unsupported/failed worker init falls back to embedded with warnings
8. Added worker backend recovery scan wiring via registry discovery in backend `Recover()`.
9. Added worker protocol/registry tests and an opt-in worker integration test scaffold (`ATTN_RUN_WORKER_INTEGRATION=1`).
10. Implemented Phase D recovery reconciliation for worker backend:
- startup reconciliation now restores/creates live sessions from recovered workers
- non-recovered running sessions are marked `idle` instead of hard-deleted
- waiting/approval states are preserved when workers are live
11. Added worker-side hardening:
- ownership mismatch registry files are quarantined
- worker RPC calls are context/time-bounded
- worker poller escalates persistent worker-unreachable failures
12. Fixed PTY stream robustness:
- stream closes on outbound send failure
- worker stream backpressure no longer blocks indefinitely
13. Completed Phase E hardening and promotion work:
- worker backend promoted to default with explicit embedded override/fallback window
- worker RPC handshake now enforces compatibility window (`rpc_major` + bounded `rpc_minor`)
- startup recovery now runs under a bounded timeout and surfaces transient recovery warnings
- recovery scan retries transient worker RPC failures before deferring/quarantining
- poller exit callbacks are asynchronous to avoid re-entrant remove deadlocks
- worker-side exited-session cleanup TTL (`45s`) is enforced when no daemon attachments remain
- session IDs are validated before worker path derivation to prevent unsafe path usage
- reconnect PTY reattach in frontend now waits for `initial_state` (post-recovery barrier)
- worker stdout/stderr is now captured per session under `<worker_root>/log/<session_id>.log`
- added opt-in integration coverage for backend restart recovery (`ATTN_RUN_WORKER_INTEGRATION=1`)

Progress update (2026-02-11, completed):

14. Phase F implementation started:
- documenting lifecycle push architecture + fallback behavior in this plan
- implementing worker startup capability probe and early embedded fallback
- implementing worker-pushed lifecycle watch stream with poll compatibility fallback

15. Phase F polish and risk-reduction fixes:
- worker runtime no longer applies idle read deadlines to long-lived attach/watch streams, preventing periodic stream teardown on idle sessions
- deferred worker reconciliation now broadcasts updated session snapshots so connected clients immediately reflect state demotions/promotions
- `clear_sessions` now performs a recovery scan before termination, so registry-only worker sessions are included in termination attempts
- recovery quarantine now cleans up owned socket artifacts for ownership/socket-path mismatch registry entries
- recovery-mode frontend notice now includes `spawn_session` and `attach_session` command failures

16. Ownership-mismatch stale-owner reclaim hardening:
- worker registry now records daemon-owner lease metadata (`owner_pid`, `owner_started_at`, `owner_nonce`) for each worker session
- ownership mismatch recovery now attempts authenticated worker `remove` RPC only when the recorded owner lease is provably stale
- if stale-owner proof is unavailable (or reclaim fails), recovery remains conservative (quarantine + preserve process) to avoid unsafe cross-daemon termination

## Why this follow-up exists

`docs/plans/demonize.md` Phase 8 sketches restart survival via shim processes, but execution details were still open.

Current behavior is still daemon-owned PTY runtime (`internal/pty`). If daemon exits, active PTYs exit with it. This document defines the concrete follow-up design to make sessions survive daemon restarts without changing frontend PTY protocol.

## Decision

Adopt one worker process per session using `attn pty-worker` (subcommand in same binary).

- Daemon remains the only API surface for app/web clients.
- Worker owns PTY + child process lifecycle and output state (`seq`, scrollback, optional screen snapshot).
- Daemon restart rehydrates by discovering live workers and reconnecting.

This aligns with the existing `internal/pty` API shape and keeps the remote-ready daemon-centric architecture.

## Goals

1. Daemon restart does not kill active PTY sessions.
2. Daemon can deterministically recover sessions from workers on startup.
3. Frontend WebSocket PTY protocol remains unchanged.
4. Design remains compatible with daemon and workers running on remote host/VM.

## Non-goals (MVP)

1. Multi-user tenancy and cross-user isolation beyond same-UID local usage.
2. Public network exposure hardening (TLS/auth stays daemon-level future work).
3. Cross-host worker scheduling.
4. Guaranteed crash-proof durability of scrollback to disk.

## Terminology and path model

1. `data_root`:
- Daemon runtime root directory, default `~/.attn`.
- Multiple daemons on one host must use distinct `data_root` values.

2. `daemon_instance_id`:
- Stable ID stored at `<data_root>/daemon-id`.
- Identity is scoped to one daemon endpoint/profile, not globally to a host/user.

3. `worker_root`:
- `<data_root>/workers/<daemon_instance_id>/`
- Canonical subpaths:
  - registry: `<worker_root>/registry/<session_id>.json`
  - sockets: `<worker_root>/sock/<session_id>.sock`
  - quarantine: `<worker_root>/quarantine/<session_id>.json`

## Future-proof constraints (multi-daemon and remote)

These constraints must hold during implementation even though multi-daemon UX is deferred.

1. Daemon remains the only network endpoint. Workers stay host-local and are never exposed to clients.
2. Worker RPC transport stays Unix-socket local. Remote access uses daemon transport (direct, SSH tunnel, Tailscale, VM port forward).
3. Session identity must be endpoint-scoped: treat `(daemon_instance_id, session_id)` as canonical identity for future client-side multi-daemon views.
4. PTY commands/events remain endpoint-local and protocol-stable so one client can hold multiple daemon connections without special PTY branching.
5. Recovery and registry logic are per-daemon-instance only; no cross-daemon worker adoption.
6. Any UI features that need filesystem/process access must route through daemon APIs, not local-client assumptions, to keep remote daemon support clean.

## Architecture decisions we lock now (to actively move toward the vision)

1. Persistent daemon identity:
- Each daemon instance has a stable `daemon_instance_id` stored at `<data_root>/daemon-id` (`data_root` defaults to `~/.attn`).
- ID survives daemon restarts and binary upgrades.
- If missing/corrupt, daemon regenerates and rewrites atomically.

2. Endpoint-scoped session identity:
- Client/runtime identity is `(daemon_instance_id, session_id)`, not `session_id` alone.
- Any in-memory maps (pending actions, terminal routing, attach subscriptions) must be keyed by endpoint + session.

3. Protocol foundation for multi-daemon clients:
- Daemon includes optional `daemon_instance_id` in initial handshake payload (`initial_state`) and warning/telemetry payloads where useful.
- Field addition is additive and backward-compatible.

4. Namespaced worker runtime state:
- Worker runtime paths use canonical `worker_root`:
  - `<data_root>/workers/<daemon_instance_id>/registry/*.json`
  - `<data_root>/workers/<daemon_instance_id>/sock/*.sock`
- Prevents collisions if multiple daemons run on one host (dev/stable, VM chroot variants, etc.).

5. Remote-transport neutrality:
- Frontend treats daemon endpoint as opaque connection profile (localhost, SSH tunnel endpoint, Tailscale IP, VM forward).
- No PTY-path branching by transport; same daemon WS protocol path everywhere.

6. Upgrade compatibility window:
- Worker RPC is additive-first.
- Daemon must support reconnecting to workers from the previous compatible worker RPC minor version.
- Breaking worker RPC changes require explicit drain/migration plan and are not allowed as silent rollout changes.

## Baseline assumptions from current code

1. Frontend already uses daemon WebSocket PTY commands/events (`spawn_session`, `attach_session`, `pty_input`, `pty_resize`, `kill_session`, `pty_output`, `attach_result`, `session_exited`, `pty_desync`).
2. `internal/pty` already provides session runtime features we need to preserve: scrollback, seq, snapshot, state detection.
3. Daemon currently prunes sessions with no live PTY on startup; this must be changed for recovery mode.

## Architecture

### Components

1. `attn daemon` (control plane)
- Owns WebSocket, store, git/GitHub, hooks ingestion.
- Routes PTY commands to backend interface.
- Performs worker discovery/recovery at boot.

2. `attn pty-worker` (data plane, one per session)
- Owns PTY master + child agent process group.
- Maintains output state and attach replay metadata.
- Serves local Unix-socket RPC.

3. Worker registry (`<worker_root>/registry/`)
- One metadata file per session.
- Enables daemon recovery after restart.

Use canonical layout:

- `<data_root>/workers/<daemon_instance_id>/registry/`
- `<data_root>/workers/<daemon_instance_id>/sock/`
- `<data_root>/workers/<daemon_instance_id>/quarantine/`

### Process model

1. Daemon spawns worker as detached child process.
2. Worker spawns wrapper/agent PTY child and starts RPC listener.
3. Daemon opens persistent RPC connection and proxies frontend PTY commands.
4. If daemon exits, worker keeps PTY child alive.
5. New daemon reconnects via registry and resumes proxying.

## Backend seam in daemon

Introduce daemon-internal backend interface and keep handlers unchanged.

```go
// internal/ptybackend/backend.go
type OutputEvent struct {
    Kind   string // "output" | "desync" | "exit"
    Data   []byte
    Seq    uint32
    Reason string
}

type Stream interface {
    Events() <-chan OutputEvent
    Close() error
}

type Backend interface {
    Spawn(ctx context.Context, opts SpawnOptions) error
    Attach(ctx context.Context, sessionID, subscriberID string) (AttachInfo, Stream, error)
    Input(ctx context.Context, sessionID string, data []byte) error
    Resize(ctx context.Context, sessionID string, cols, rows uint16) error
    Kill(ctx context.Context, sessionID string, sig syscall.Signal) error
    Remove(ctx context.Context, sessionID string) error
    SessionIDs(ctx context.Context) []string
    Recover(ctx context.Context) (RecoveryReport, error)
    Shutdown(ctx context.Context) error
}
```

Daemon (WebSocket layer) owns subscriber buffering/backpressure policy and maps backend `desync`/`exit` events to current frontend protocol events (`pty_desync`, `session_exited`).

Implementations:

1. `embedded` adapter over current `internal/pty.Manager` (no behavior change).
2. `worker` adapter over worker RPC.

Selection via env flag:

- `ATTN_PTY_BACKEND=embedded|worker`
- Default: `embedded` until rollout gate is met.

## Worker runtime contract

### Subcommand choice

Use `attn pty-worker` (not separate install artifact).

Why:

1. Existing install paths and update flow remain simple (`make install` / app packaging).
2. Daemon/worker versions are typically aligned because they ship in one binary, while compatibility rules still cover live-worker upgrade windows.
3. Lower operational complexity than shipping `attn-pty-worker` separately.

### Launch inputs

Daemon starts worker with required args:

- `--daemon-instance-id`
- `--session-id`
- `--agent`
- `--cwd`
- `--cols`
- `--rows`
- `--registry-path`
- `--socket-path`
- `--control-token`
- optional resume/fork/executable fields already present in spawn flow

### Registry schema

Path: `<data_root>/workers/<daemon_instance_id>/registry/<session-id>.json`

```json
{
  "version": 1,
  "daemon_instance_id": "d-123",
  "session_id": "uuid",
  "worker_pid": 12345,
  "child_pid": 12367,
  "socket_path": "<data_root>/workers/<daemon_instance_id>/sock/<session-id>.sock",
  "agent": "codex",
  "cwd": "/path/to/repo",
  "started_at": "2026-02-10T22:10:00Z",
  "control_token": "base64-random-32b"
}
```

Rules:

1. Write atomically (`.tmp` then rename).
2. Registry dir permissions `0700`; file permissions `0600`.
3. Worker removes metadata and socket on clean termination.

## Worker RPC protocol

Transport: JSON Lines (`\n` delimited) over Unix domain socket, full-duplex.

Envelope:

```json
{"type":"req","id":"r1","method":"attach","params":{...}}
{"type":"res","id":"r1","ok":true,"result":{...}}
{"type":"evt","event":"output","session_id":"...","seq":42,"data":"...base64..."}
{"type":"evt","event":"exit","session_id":"...","exit_code":0}
```

### Required methods

1. `hello`
- request: `{ "rpc_major": 1, "rpc_minor": 0, "daemon_instance_id": "...", "control_token": "..." }`
- response: `{ "worker_version": "...", "rpc_major": 1, "rpc_minor": 0, "daemon_instance_id": "...", "session_id": "..." }`

2. `info`
- response: `{ running, agent, cwd, cols, rows, worker_pid, child_pid, last_seq }`

3. `attach`
- response: `AttachInfo` equivalent:
  - `scrollback`, `scrollback_truncated`, `last_seq`
  - `screen_snapshot` and cursor metadata when available
  - `running`, `exit_code`, `exit_signal`, size/pid fields
- starts `output` event flow for this daemon connection

4. `detach`
- stop sending output events for this daemon connection

5. `input`
- request contains base64 bytes

6. `resize`
- request contains `cols`, `rows`

7. `signal`
- request contains signal name; worker signals child process group

8. `remove`
- explicit terminal cleanup signal from daemon after session fully done

9. `health`
- lightweight liveness check

### Versioning and upgrade compatibility

1. Worker RPC uses `(rpc_major, rpc_minor)`.
2. Compatibility rule:
- `rpc_major` mismatch is incompatible.
- Daemon must accept worker `rpc_minor` within a defined compatibility window (current and previous minor for this track).
3. RPC evolution rule:
- New fields/methods must be optional/additive first.
- Removing/changing semantics requires major bump and explicit migration plan.
4. Upgrade-safety requirement:
- A freshly upgraded daemon must be able to reconnect to still-running workers from the previous compatible version.

### Error model

Each response may include structured error:

```json
{"type":"res","id":"r2","ok":false,"error":{"code":"session_not_running","message":"..."}}
```

Canonical codes:

- `bad_request`
- `unsupported_version`
- `unauthorized`
- `session_not_found`
- `session_not_running`
- `io_error`
- `internal_error`

## Session/output semantics

1. Worker owns `seq` counter and scrollback ring buffer; daemon does not regenerate either.
2. Output ordering is guaranteed per session (`seq` monotonic, no rewrites).
3. On daemon backpressure, daemon may drop a subscriber and send existing `pty_desync` to frontend; worker remains source of truth.
4. `session_exited` remains daemon-originated WS event, sourced from worker `exit` event.

## Recovery algorithm on daemon start

### Startup order change

Current startup prunes stale store sessions before any PTY recovery. With worker backend this must be reordered:

1. Start daemon infra (store, pid lock, listeners) but keep WebSocket command handling in `recovering` barrier mode.
2. Initialize selected PTY backend.
3. Run backend recovery (`Recover`).
4. Reconcile store against recovered live sessions.
5. Prune only confirmed stale entries.
6. Lift recovery barrier and serve `initial_state` only after reconciliation completes.

### Recovery barrier rules

1. While recovering:
- WS clients may connect, but daemon does not emit final `initial_state` and rejects PTY commands (`spawn/attach/input/resize/kill/unregister`) with retryable `daemon_recovering` error.
2. After recovery:
- Daemon emits a single coherent `initial_state` snapshot.
- Normal PTY command handling begins.

### Worker discovery and validation

For each `<data_root>/workers/<daemon_instance_id>/registry/*.json`:

1. Parse JSON and validate `version`, `session_id`, `socket_path`.
2. Check PID liveness (`kill(pid, 0)`).
3. Verify socket exists and is connectable.
4. Connect and call `hello` with token + version.
5. Call `info`; if valid, mark session recovered.
6. Apply failure classification:
- stale: dead PID, missing socket, malformed metadata => prune entry.
- transient: connect timeout / temporary I/O => retry with bounded backoff, then quarantine.
- ownership/version mismatch: live worker with unexpected owner/version => quarantine and warn; do not delete.

### Ownership enforcement (no cross-daemon adoption)

1. Worker metadata includes `daemon_instance_id` and `control_token`.
2. Worker `hello` requires both fields and rejects mismatches (`unauthorized`).
3. Daemon only scans its own namespaced registry path.
4. Mismatch cases are quarantined for operator inspection, not auto-adopted.

### Store reconciliation rules

For each recovered live worker:

1. If store session exists: keep metadata (`label`, branch info, etc.), update runtime fields (`LastSeen`, state baseline).
2. If store session is missing: create minimal session row from worker metadata (`id`, `cwd`, `agent`, state=`working`) and set label to `basename(cwd)`.
3. If worker reports exited: emit exit event and remove runtime entry.

For each store session not recovered:

1. If already terminal/idle and no worker, keep.
2. If expected running but no worker, mark idle and emit warning (`stale_session_missing_worker`).

Recovery output should include counts: recovered, pruned, missing, failed.

### Runtime authority model

1. Worker is authoritative for runtime PTY facts:
- `running`, `child_pid`, `cols/rows`, `last_seq`, `scrollback`, `screen_snapshot`, `exit_code/signal`.
2. Store is authoritative for user/domain metadata:
- label, directory, branch/worktree metadata, review/git/GitHub annotations.
3. Session state reconciliation on recovery:
- if worker is running and store state is `waiting_input` or `pending_approval`, preserve store state.
- if worker is running and store state is other/non-runtime value, set `working`.
- if worker is exited, set `idle` and emit `session_exited` once.
4. Daemon never fabricates `seq`; it relays worker sequence authority.

## Lifecycle rules

1. Normal daemon stop must not signal workers under worker backend.
2. Explicit session close/unregister must call worker `signal` then `remove`.
3. Worker child exit should trigger registry/socket cleanup after `45s` TTL if no active daemon attachment.
4. Orphan policy:
- Dead worker PID + stale file => prune immediately.
- Live worker + unreadable info => quarantine and warn; do not kill automatically.

## Security and local isolation

1. Unix sockets under `<data_root>/workers/<daemon_instance_id>/sock` with `0700` directory.
2. Worker RPC accepts only local same-UID connections (filesystem perms + explicit UID check where available).
3. `control_token` required in handshake for ownership correlation and anti-misroute.
4. Do not expose worker sockets to frontend; daemon remains only bridge.
5. `control_token` is not a substitute for network authentication. Remote auth/TLS is enforced at daemon endpoint layer, not worker RPC.

## Observability

Add low-noise lifecycle logs (no PTY payload logs):

1. Worker spawn/attach/detach/exit.
2. Recovery scan summary.
3. Registry prune actions.
4. Version mismatch failures.

Add daemon warning events for:

- `worker_recovery_partial`
- `worker_registry_corrupt`
- `worker_protocol_mismatch`

For future multi-daemon operations, include `daemon_instance_id` in recovery and warning logs so host/endpoint attribution is unambiguous.

## File-level implementation map

1. `cmd/attn/main.go`
- Add `pty-worker` subcommand entrypoint.

2. `internal/ptybackend/`
- `backend.go` interface and shared types.
- `embedded.go` adapter over existing `internal/pty` manager.
- `worker.go` daemon-side worker RPC adapter.
- `recovery.go` discovery + reconciliation helpers.

3. `internal/ptyworker/`
- `runtime.go` worker session runtime (reusing existing PTY session logic where possible).
- `rpc.go` JSONL RPC server/client helpers.
- `registry.go` atomic registry file management.
- `protocol.go` RPC envelope/types/version.

4. `internal/daemon/daemon.go`
- Select backend via env.
- Move prune logic to run after backend recovery.
- Add recovery barrier before serving coherent initial state.
- Keep shutdown semantics backend-aware.

5. `internal/daemon/websocket.go`
- Replace direct `d.ptyManager` calls with backend interface calls.
- Preserve frontend message schema and event timing.

6. `internal/protocol/` and frontend
- No breaking PTY protocol schema changes required for MVP.
- Additive protocol metadata is required: optional `daemon_instance_id` in initial handshake/state payloads.

## Rollout plan

### Phase A: Backend seam (no behavior change)

1. Introduce `ptybackend.Backend`.
2. Wire daemon to `embedded` adapter only.
3. Add persistent `daemon_instance_id` generation/load.
4. Include additive `daemon_instance_id` in initial handshake payload.
5. Define helper key format for endpoint-scoped runtime identity (`<daemon_instance_id>:<session_id>`).
6. Add recovery barrier scaffold (even in embedded mode).
7. Ensure tests pass unchanged for PTY behavior.

Exit gate:

- Existing PTY tests green.
- No PTY protocol diffs.
- Additive handshake field is tolerated by older clients.
- Initial state is not emitted before recovery barrier lift.

### Phase B: Worker runtime + RPC

1. Add worker subcommand and runtime package.
2. Implement registry + hello handshake.
3. Implement daemon-ID namespaced worker paths.
4. Add unit tests for RPC and registry behavior.
5. Add compatibility tests for `rpc_minor` window behavior.

Exit gate:

- Worker passes standalone spawn/input/resize/kill tests.

### Phase C: Worker backend integration (flagged)

1. Add daemon worker adapter.
2. Route PTY commands through backend interface.
3. Keep default `embedded`.

Exit gate:

- `ATTN_PTY_BACKEND=worker` works for normal interactive usage.

### Phase D: Restart recovery

1. Implement boot discovery/recovery path.
2. Reconcile store/runtime deterministically.
3. Add startup warnings for partial recovery.

Exit gate (core requirement):

- Kill daemon, keep worker alive, restart daemon, reattach, continue typing in same session.

### Phase E: Hardening + promotion

Status: completed on 2026-02-10.

1. Backpressure and timeout tuning.
2. Crash/orphan cleanup hardening.
3. Soak in dev builds.
4. Promote default to `worker`, retain `embedded` fallback for one release window.

## Test plan

### Unit

1. Worker registry atomic write/read/prune.
2. RPC encode/decode and version negotiation.
3. Worker runtime attach/replay/input/resize/signal.

### Integration (Go daemon)

1. Spawn with worker backend, attach, type, resize, kill.
2. Daemon restart recovery against live worker.
3. Recovery with stale metadata and dead PID.
4. Mixed state reconciliation (store has missing/extra sessions).
5. Recovery barrier behavior (no stale initial state leak).
6. Ownership mismatch and version mismatch quarantine behavior.

### E2E (Playwright)

1. Start session, produce output, restart daemon, confirm continuity.
2. `Cmd+T` utility terminal path with worker backend.
3. Codex visible snapshot restore after restart.

## Risks and mitigations

1. Process count overhead at high session counts.
- Mitigation: configurable max sessions + metrics; revisit supervisor model only if needed.

2. Protocol drift between daemon and worker.
- Mitigation: explicit `(rpc_major, rpc_minor)` compatibility policy, additive-first evolution, and mismatch quarantine warnings.

3. Registry staleness leading to false recovery.
- Mitigation: never trust file alone; require PID + socket + successful `hello/info`.

4. Shutdown semantic regressions.
- Mitigation: backend-specific shutdown tests; daemon stop must not kill worker sessions.

## Decisions locked

1. Exited worker TTL: `45s` grace window before cleanup.
2. Recovery-created label when store row is missing: `basename(cwd)`.
3. Snapshot persistence model for this track: memory-only in worker (no disk-backed snapshot).
4. Daemon identity is persistent and surfaced as additive protocol metadata.
5. Worker registry/socket paths are daemon-ID namespaced.
6. Session runtime identity is endpoint-scoped (`daemon_instance_id + session_id`).
7. Daemon startup uses a recovery barrier before emitting coherent initial state.
8. Worker RPC follows additive-first evolution with compatibility window and explicit migration for breaking changes.

## Deferred decisions (not blocking this implementation)

1. Multi-daemon client UX model (single window with many endpoints vs endpoint switcher).
2. Endpoint trust/auth strategy for non-localhost usage (token, mTLS, SSH-only posture).
3. Whether to support federated search/actions across endpoints or keep per-endpoint isolation in v1.

## Acceptance criteria (MVP)

1. With `ATTN_PTY_BACKEND=worker`, daemon restart preserves active PTY sessions in integration tests (100% across at least 30 restart cycles).
2. Recovery completes and coherent `initial_state` is emitted within 3 seconds for up to 25 live workers on reference dev hardware.
3. Reattached sessions restore continuity (replay/snapshot + live stream) with no duplicate session rows and no cross-endpoint ID collisions.
4. No cross-daemon worker adoption occurs: ownership/version mismatch cases are quarantined and surfaced as warnings.
5. Frontend protocol remains PTY-compatible (no breaking PTY schema changes); additive `daemon_instance_id` metadata is handled by old and new clients.
6. Recovery behavior, barrier behavior, and mismatch handling are covered by automated unit/integration/E2E tests.
