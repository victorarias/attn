# PTY Decoupling Plan: Per-Session Worker Sidecars

Date: 2026-02-08  
Status: Draft  
Owner: daemon/pty

## Why

Today PTY lifecycle is owned by the daemon process. If daemon restarts, active PTYs are lost.  
We want PTYs to survive daemon restarts and be recoverable, while keeping a path for remote/VM deployment.

## Decision

Use **Option 2**: one lightweight `attn-pty-worker` process per session.

- Daemon remains control plane and API surface for frontend.
- Worker owns one PTY lifecycle (`spawn`, `attach`, `input`, `resize`, `kill`, replay/snapshot).
- Daemon can crash/restart and reconnect to live workers via registry discovery.

This is chosen for simplicity and incremental delivery.  
If needed later, this can evolve into a single supervisor (`Option 1`) behind the same daemon-facing worker RPC shape.

## Goals

1. Daemon restart does not kill active PTYs.
2. Daemon can reconstruct session/PTy state from workers on boot.
3. Frontend protocol remains daemon-centric (no direct worker client connections).
4. Design remains compatible with daemon on remote host/VM.

## Non-Goals (Phase 1)

1. Multi-user shared daemon tenancy.
2. Full transport/auth hardening for public network exposure.
3. Cross-host worker scheduling.

## Architecture

## Components

1. `attn-daemon` (control plane)
- WebSocket/API for app clients.
- Store/session metadata, git/GitHub logic, warnings, policy.
- Worker lifecycle management + restart recovery.

2. `attn-pty-worker` (data plane, one per session)
- Owns PTY + child agent process.
- Maintains `seq`, replay buffer, optional visible snapshot.
- Serves local RPC over Unix socket.

3. Worker registry (`~/.attn/workers/`)
- One file per session ID with:
  - `session_id`
  - `pid`
  - `socket_path`
  - `created_at`
  - `agent`
  - `cwd`
- Atomic writes (`.tmp` + rename), removed on clean worker exit.

## Data Flow

1. Spawn session
- Daemon starts worker process with launch args/env.
- Worker creates PTY + starts agent.
- Daemon marks session active and attaches frontend stream.

2. Runtime
- Frontend sends PTY commands to daemon.
- Daemon forwards to worker RPC.
- Worker streams output/replay metadata to daemon; daemon emits existing WS events.

3. Daemon restart
- Workers continue running.
- New daemon scans registry + validates PID/socket.
- Daemon reattaches to workers, rehydrates session state, emits initial state.

## Worker RPC (stable contract)

Minimal RPC (JSON over Unix domain socket; request/response + stream):

1. `info`
- returns `running`, `pid`, `agent`, `cwd`, `cols`, `rows`, `last_seq`

2. `attach`
- returns replay payload (`scrollback`, `last_seq`, optional `screen_snapshot`)
- starts output stream subscription

3. `detach`
- stop subscription for caller

4. `input`
- write bytes to PTY stdin

5. `resize`
- set PTY size

6. `kill`
- terminate session process

7. `health`
- readiness and worker version

## Phased Implementation

## Phase 0: Contract + flags

1. Add backend flag:
- `ATTN_PTY_BACKEND=embedded|worker` (default `embedded` initially).
2. Define worker RPC structs and version field.
3. Add compatibility tests for contract encoding/decoding.

Exit criteria:
- Daemon compiles with dual backend interface and no behavior change by default.

## Phase 1: Worker binary (single-session)

1. Add `cmd/attn-pty-worker`.
2. Move/reuse PTY session logic from `internal/pty/session.go` into worker-owned runtime package.
3. Implement registry file create/update/remove.
4. Implement RPC server and attach/replay output stream.

Exit criteria:
- Worker can spawn agent PTY, accept input/resize, and serve replay/stream with seq.

## Phase 2: Daemon worker backend integration

1. Add `internal/ptybackend` interface used by websocket handlers.
2. Implement worker-backed adapter in daemon:
- spawn worker process
- connect RPC
- forward attach/input/resize/kill
3. Preserve existing WS protocol to frontend (no app contract changes).

Exit criteria:
- App behavior unchanged under `ATTN_PTY_BACKEND=worker` for normal session usage.

## Phase 3: Restart recovery

1. On daemon startup, scan `~/.attn/workers/*.json`.
2. Validate each worker:
- PID alive
- Unix socket reachable
- `info` succeeds
3. Rehydrate session state in store and mark stale registry entries for cleanup.
4. If store says session exists but no live worker exists, emit stale-session warning.

Exit criteria:
- Kill daemon, keep workers alive, restart daemon: sessions recover and reattach.

## Phase 4: Hardening

1. Heartbeats between daemon and workers.
2. Timeouts/circuit breakers for blocked worker RPC.
3. Backpressure handling and bounded attach queue.
4. Robust orphan cleanup:
- missing PID
- stale socket
- stale registry file

Exit criteria:
- Repeated daemon restarts and partial crashes do not leak broken workers indefinitely.

## Phase 5: Remote/VM readiness

1. Keep worker RPC local-only (Unix socket on daemon host).
2. Add daemon network mode for client access (later):
- TCP/TLS WebSocket or SSH-tunnel model.
3. Ensure no frontend dependency on local filesystem/sockets.

Exit criteria:
- Daemon + workers run on remote host/VM; local UI connects over daemon endpoint.

## Testing Plan

1. Unit
- Worker RPC handlers (`attach`, `input`, `resize`, `kill`, `info`)
- Registry lifecycle and atomic write behavior
- Recovery scanner validation logic

2. Integration
- Spawn session via daemon(worker backend), attach, type, resize, close.
- Daemon crash/restart while worker stays alive; verify reattach + visible restore.

3. E2E (Playwright)
- Start session, produce output, restart daemon, verify output continuity.
- Utility terminal `Cmd+T` path under worker backend.
- Codex visible snapshot restore after daemon restart.

## Operational Model

1. Local dev
- Default `embedded` until worker backend is stable.
- CI matrix can run selected suites with `ATTN_PTY_BACKEND=worker`.

2. Production rollout
- Gate with feature flag.
- Enable for dev builds first, then opt-in for regular usage.

## Risks and Mitigations

1. Too many processes
- Risk: high session counts create overhead.
- Mitigation: cap concurrent sessions, monitor RSS/CPU, later graduate to shared supervisor if needed.

2. Worker/daemon protocol drift
- Risk: incompatibility after upgrades.
- Mitigation: worker RPC version negotiation + explicit mismatch error.

3. Registry corruption/staleness
- Risk: false recovery or leaked workers.
- Mitigation: validate with live PID + socket + `info`, never trust file alone.

4. Complex shutdown semantics
- Risk: daemon exit accidentally kills workers.
- Mitigation: spawn workers detached from daemon process group; explicit kill only on session close.

## Open Questions

1. Should worker binary be separate install artifact or subcommand (`attn pty-worker`)?
2. Keep replay buffer only in worker memory, or add optional disk-backed snapshot?
3. Should daemon enforce max worker count per user/config?
4. For remote mode, prefer first-class TLS listener or documented SSH tunnel workflow first?

## Acceptance Criteria (MVP)

1. With worker backend enabled, daemon restart preserves active sessions.
2. Reattached session keeps terminal continuity (replay/snapshot + live output).
3. No frontend protocol changes required for MVP.
4. Works when daemon and workers run inside a VM and client connects to daemon endpoint.
