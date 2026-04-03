# Remote Daemon Hub: Multi-Machine Session Control

Date: 2026-02-21
Status: Implemented (SSH scope)
Owner: daemon/frontend

## Motivation

attn currently runs everything on one machine: daemon, agents, git, UI. The goal is to run agent workloads on remote machines (dev servers, VMs, cloud instances) while keeping a single local UI that shows all sessions across all machines.

## Current Baseline

This plan predates the preparatory refactors completed on 2026-04-02 in [docs/plans/2026-04-02-preparatory-refactors.md](/Users/victor.arias/projects/victor/attn/docs/plans/2026-04-02-preparatory-refactors.md).

Treat those items as completed prerequisites, not future work:

- compiled-in version string and `attn --version`
- Linux release artifacts for `linux/amd64` and `linux/arm64`
- `Session.endpoint_id` in the protocol/store/frontend types
- frontend WebSocket URL abstraction
- extracted websocket command scope metadata in `internal/daemon/command_meta.go`
- automatic local daemon restart on protocol mismatch

The core SSH-based remote-daemon-hub architecture from this plan is implemented. The remaining work is manual validation/polish from real use and the optional direct-connection hardening described in Phase E.

The picker UX target for this remaining work is explicit:

- local and remote daemons should behave the same from the user's perspective once a target is selected
- the picker should support arbitrary directory navigation on remote daemons, but start from that daemon's `projects_directory` when configured
- if a daemon has no `projects_directory`, the picker should start from `~`
- recent locations should be scoped per endpoint and populated only after successful session launches
- typed paths should validate and browse live; there should be no manual-entry fallback that bypasses daemon validation
- repo detection should use the daemon's real git probing path, not a picker-local heuristic
- repo probing should happen on explicit directory selection, matching the local flow today
- the picker should remain directory-only; file browsing is out of scope for this pass
- if a remote endpoint is disconnected or cannot browse/validate, the picker should fail closed rather than falling back to a degraded local-only path

## Current Status

Implemented and working:

- Phase A: SSH bootstrap, remote binary install/update, `attn ws-relay`, remote daemon start/restart, and transport retry handling.
- Phase B: persisted endpoints, endpoint lifecycle/status events, and Settings-based endpoint management.
- Phase C: merged remote sessions in the main app session list with endpoint badges and grouped visibility.
- Phase D: remote PTY attach/input/output, remote session creation from the app, remote reload, remote git/diff/review routing, remote review comment mutations, and remote review-loop routing/UI.
- Picker parity: local and remote new-session browsing now share the same daemon-backed browse/inspect path, remote browsing starts from `projects_directory` (or `~`), repo detection uses daemon git probing, per-endpoint recents are returned by the owning daemon, and remote repo/worktree flows match the local picker path.
- Packaged real-app smoke coverage: verified end-to-end against `ai-sandbox` with a fresh app instance and a fresh local daemon instance, including remote review-loop start, stop, awaiting-user, answer, and completion.
- Packaged picker smoke coverage: verified end-to-end against `ai-sandbox`, including remote directory browse, repo selection, per-endpoint recent locations, remote worktree creation, and worktree visibility on reopen.
- Stability pass: repeated fresh packaged-app runs against `ai-sandbox` now pass consistently after hardening transient worker attach races and harness failure reporting.

Implemented with intentional scope limits:

- Remote open-in-editor is supported only for Zed remote SSH targets.
- Remote fork is intentionally not supported.
- Branch switching is not a target for this plan because that UI path is expected to be removed later.

Still remaining:

- manual validation and UX polish from real use
- optional direct-connection hardening from Phase E (TLS/token/non-SSH flow)

## Topology

```
Local Mac                                Remote Server A
┌──────────┐      WS       ┌──────────┐      SSH       ┌─────────────────────┐
│ Tauri App │ ────────────→ │  Local   │ ──(ws-relay)─→ │   Remote Daemon     │
└──────────┘  localhost     │  Daemon  │               │  ├── PTY Workers    │
                            │  (Hub)   │               │  ├── Hooks (nc -U)  │
                            │          │               │  ├── Git ops        │
                            │  local   │               │  ├── Classifier     │
                            │  sessions│               │  └── SQLite         │
                            │  + proxy │               └─────────────────────┘
                            │          │      SSH       ┌─────────────────────┐
                            │          │ ──(ws-relay)─→ │   Remote Daemon     │
                            └──────────┘               │   (Server B)       │
                                                       └─────────────────────┘
```

The local daemon is the **hub**. It:
1. Manages local sessions as it does today (unchanged).
2. Connects as a WebSocket **client** to one or more remote daemons via SSH.
3. Merges remote sessions into the local session list.
4. Proxies commands and events between the Tauri app and remote daemons.

The Tauri app connects to one local WebSocket endpoint. It does not know about remote daemons directly.

## Why This Shape

### Hooks are not a problem

When the daemon is co-located with agents, `nc -U` hooks work as-is. The hooks IPC issue only exists if you try to run agents remotely from a local daemon. In this design, each remote daemon is a self-contained unit — agents, hooks, git, classifier, store — all local to each other.

### PTY workers stay untouched

Workers are intentionally stable and rarely change. This design adds zero new responsibilities to workers. All remote proxying happens at the daemon level.

### WebSocket protocol is already the right seam

The Tauri app already talks to the daemon exclusively over WebSocket. The hub daemon can use the same protocol as a client to remote daemons. This means:
- No new protocol to design for hub↔remote communication.
- The remote daemon doesn't need a special protocol — it speaks the same protocol the Tauri app uses.
- Every command that works locally works remotely, because the hub is just forwarding WebSocket messages.

### Incremental delivery

Each phase is independently valuable:
- Preparatory refactors (done) established the versioning, protocol, and routing baseline.
- Phase A (done) lets the hub reach and validate a remote daemon.
- Phase B (done) lets the local daemon aggregate remotes and manage endpoints.
- Phase C (done) shows remote sessions in the app.
- Phase D is done: interactive remote PTY, spawn, reload, repo/review routing, review-loop flows, and picker parity for browse/repo/worktree session creation are all implemented and packaged-smoked.
- Phase E (later) covers direct connections and hardening.

## Design Principles

1. **The hub is a WS client, not a custom protocol.** The hub connects to remote daemons using the existing WebSocket protocol. It receives `initial_state`, session events, PTY output — the same messages the Tauri app would receive.

2. **Session identity is endpoint-scoped.** Sessions are identified by `(endpoint_id, session_id)`. The `daemon_instance_id` already exists and is sent in `initial_state`. The hub uses this as the endpoint identifier.

3. **Commands are routed by endpoint.** When the UI sends a command targeting a session, the hub looks up which endpoint owns that session and forwards the command there. Local commands stay local.

4. **Events are tagged and merged.** Events from remote daemons are tagged with their endpoint ID and forwarded to the UI. The UI sees a unified stream.

5. **Each remote daemon is autonomous.** Remote daemons don't know they're "headless." They run identically to local daemons. The hub connects via SSH stdio — no special configuration needed on the remote.

## Key Architecture Decisions

### 1. Hub as WS client to remote daemons

The hub daemon opens a WebSocket connection to each configured remote daemon via SSH stdio (`attn ws-relay`). It receives `initial_state`, subscribes to events, and can send any command — just like the Tauri app does.

This is the simplest path because:
- The remote daemon needs zero protocol changes for the hub.
- Most commands/events can be forwarded as-is (see command scoping audit below for exceptions).
- The hub is essentially a "Tauri app" running inside the daemon process.

### 2. Session namespacing in the merged view

The UI needs to distinguish sessions from different endpoints. Options:

**Option A: Prefix session IDs** — The hub rewrites `session_id` → `{endpoint_id}:{session_id}` in all events before forwarding to the UI. The hub strips the prefix when routing commands back to remotes. 

**Option B: Add endpoint field** — Events and sessions gain an `endpoint_id` field. Session identity becomes a compound key.

**Recommendation: Option B.** It's cleaner, doesn't break ID formats, and it matches the current protocol direction. `Session.endpoint_id` already exists from the preparatory refactors, so the remaining work is to use that field consistently in hub merge/routing logic. Local sessions have no endpoint_id (or a sentinel like `"local"`).

### 3. State the hub must manage per remote endpoint

```go
type RemoteEndpoint struct {
    ID              string          // daemon_instance_id from remote's initial_state
    Name            string          // user-friendly name ("dev-server", "gpu-box")
    SSHTarget       string          // SSH connection target ("gpu-box", "user@10.0.0.5")
    
    // Runtime state (not persisted)
    Conn            *websocket.Conn
    Sessions        []Session       // last known session list from this endpoint
    Connected       bool
    LastSeen        time.Time
    ReconnectDelay  time.Duration
    SSHProcess      *exec.Cmd       // the ssh → attn ws-relay process
    LocalProxyPort  int             // local TCP port bridging ssh stdio ↔ WS
}
```

### 4. What the hub proxies vs. handles locally

| Concern | Local | Remote (proxied) | Notes |
|---|---|---|---|
| Session CRUD | ✓ | ✓ (forwarded) | Hub routes by endpoint_id |
| PTY I/O | ✓ | ✓ (forwarded) | Binary-efficient forwarding |
| Git operations | ✓ | ✓ (forwarded) | Git runs where the repo is |
| GitHub PR polling | ✓ | ✗ | Hub handles PRs centrally |
| Review comments | ✓ | ✓ (forwarded) | Reviews are per-session |
| Settings | ✓ | Per-endpoint | Some settings are global, some per-endpoint |
| Spawn session | ✓ | ✓ (forwarded) | Hub tells remote daemon to spawn |

### 5. Headless daemon configuration

A remote daemon is identical to a local daemon. No special "headless mode" — the daemon runs normally on the remote machine. The hub connects to it via SSH stdio (`attn ws-relay`), which bridges stdin/stdout to the daemon's localhost WS port.

The remote daemon stays fully localhost-only. No network exposure, no auth tokens needed — SSH provides the auth boundary.

```bash
# On remote machine — standard daemon, nothing special
attn daemon
```

The only remote-specific requirement: `attn` binary must be installed in `$PATH` (typically `~/.local/bin/attn`). The hub handles installation and updates automatically via SSH (see Bootstrap Flow below).

### 6. Authentication

SSH is the auth layer. The hub connects via `ssh <target> attn ws-relay`, which inherits the user's SSH credentials (keys, agent, config). The relay connects to the daemon's localhost WS — no additional auth needed.

For direct connections (non-SSH, e.g., Tailscale), Phase A supports optional bearer token auth (`ATTN_WS_AUTH_TOKEN`). But the primary flow is SSH-based and requires no daemon-side auth config.

### 7. TLS

Not needed for the primary SSH stdio transport — SSH provides encryption. TLS support (`ATTN_WS_TLS_CERT`, `ATTN_WS_TLS_KEY`) is available for direct connections (Phase A/E) but is not part of the core SSH-based flow.

### 8. Command scoping audit

Not all WS commands are simple session-scoped forwards. The hub must handle scoping correctly.

**Important:** The daemon has TWO IPC interfaces with different command sets:
- **Unix socket** (`daemon.go:handleConnection`) — used by hooks (`register`, `state`, `stop`, `todos`, `heartbeat`) and CLI (`query`, `mute`). These are local-only and never reach the hub. No routing needed.
- **WebSocket** (`websocket.go:handleClientMessage`) — used by the Tauri app. These are the commands the hub must route.

The hub only routes **WebSocket commands**:

| WS Command | Scope | Hub behavior |
|---|---|---|
| **PTY lifecycle** | | |
| `pty_input`, `pty_resize` | Session | Forward to owning endpoint |
| `attach_session`, `detach_session` | Session | Forward to owning endpoint |
| `spawn_session` | Endpoint | Forward to target endpoint (explicit `endpoint_id` required) |
| `kill_session` | Session | Forward to owning endpoint |
| **Session management** | | |
| `session_visualized` | Session | Forward to owning endpoint |
| `unregister` | Session | Forward to owning endpoint |
| `clear_sessions` | **Targeted** | Requires explicit `endpoint_id`. Never broadcast |
| `clear_warnings` | Hub-local | Hub warnings only |
| `get_recent_locations` | Hub-merge | Merge local + all remote results |
| **Git operations** | | |
| `list_branches`, `delete_branch`, `switch_branch`, `create_branch` | Directory | Forward by `endpoint_id` |
| `list_remote_branches`, `fetch_remotes`, `get_default_branch` | Directory | Forward by `endpoint_id` |
| `subscribe_git_status`, `unsubscribe_git_status` | Directory | Forward; hub tracks subscriptions per endpoint for re-establishment on reconnect |
| `get_file_diff`, `get_branch_diff_files`, `get_repo_info` | Directory | Forward by `endpoint_id` |
| `ensure_repo` | Directory | Forward by `endpoint_id` |
| **Stash/worktree** | | |
| `stash`, `stash_pop`, `check_attn_stash`, `check_dirty`, `commit_wip` | Directory | Forward by `endpoint_id` |
| `list_worktrees`, `create_worktree`, `create_worktree_from_branch`, `delete_worktree` | Directory | Forward by `endpoint_id` |
| **Review/comments** | | |
| `get_review_state`, `mark_file_viewed` | Session | Forward to owning endpoint |
| `add_comment`, `update_comment`, `resolve_comment`, `wontfix_comment`, `delete_comment`, `get_comments` | Session | Forward to owning endpoint |
| `start_review`, `cancel_review` | Session | Forward to owning endpoint |
| **PR commands** | | |
| `approve_pr`, `merge_pr`, `refresh_prs`, `fetch_pr_details` | **Hub-local** | PRs polled by hub only |
| `mute_pr`, `mute_repo`, `mute_author`, `collapse_repo` | **Hub-local** | PR state is hub-local |
| `pr_visited` | **Hub-local** | PR state is hub-local |
| **Settings** | | |
| `get_settings` | **Hub-local** | Returns hub settings |
| `set_setting` | **Hub-local** | Sets hub settings. Remote settings not exposed |
| **Test/debug** | | |
| `inject_test_pr`, `inject_test_session` | **Hub-local** | Test data is local only |
| **Endpoint management** | | |
| `add_endpoint`, `remove_endpoint`, `update_endpoint`, `list_endpoints` | **Hub-only** | New commands |

**Key rule:** Commands that could cause data loss or unintended side effects (`clear_sessions`, `delete_branch`) must never be broadcast to all endpoints. They require explicit targeting.

### 9. Request/response correlation

**Problem:** The current protocol has no request IDs. Result events (e.g., `spawn_result`, `switch_branch_result`) are broadcast to all WS clients. The frontend correlates them using local `pendingActionsRef` maps with type-based matching.

When the hub forwards a command to a remote daemon, the result event comes back on the hub's single WS connection. With only one UI client (v1), the hub can forward the result directly. But with multiple UI clients, the hub wouldn't know which client initiated the request.

**V1 approach (single UI client):** Forward all result events from remote daemons to the single connected UI client. The frontend's existing `pendingActionsRef` correlation works because there's only one consumer. No protocol changes needed.

**Future approach (multi-client):** Add optional `request_id` field to commands and result events. The hub stamps outgoing commands with a `request_id`, maps it to the originating UI client, and routes the result back. This is an additive protocol change that can be deferred.

### 10. PTY I/O efficiency

**Problem:** Naive proxying would parse every `pty_output` JSON message from the remote, tag it with `endpoint_id`, re-serialize, and forward. This adds latency and CPU overhead for high-throughput terminal output.

**Optimizations:**

1. **Raw message forwarding** — For `pty_output` events (the bulk of traffic), the hub can inject the `endpoint_id` field into the raw JSON bytes without full parse/serialize. A simple byte-level splice before the closing `}` avoids deserialization.

2. **Binary WebSocket frames** — Consider sending PTY output as binary frames with a minimal header (endpoint_id + session_id prefix) instead of JSON. This eliminates base64 encoding overhead. Requires protocol extension but can be additive (clients that don't understand binary frames fall back to JSON).

3. **For v1:** Simple JSON forwarding with the `endpoint_id` splice optimization is sufficient. Full binary frame optimization is deferred unless latency testing shows problems.

### 11. Protocol version compatibility across daemons

**Problem:** The current protocol uses a strict version check — the daemon immediately closes the connection if `ProtocolVersion` doesn't match exactly. In a multi-machine deployment, daemons could temporarily run different versions during upgrades.

**Solution:** Keep strict version matching — it's the mechanism that triggers auto-update, not an obstacle. The hub↔remote handshake flow:

1. Hub connects via ws-relay, receives `initial_state` with `protocol_version`.
2. Version matches → proceed normally.
3. Version mismatches → hub disconnects, SCPs the matching binary to remote, restarts remote daemon, reconnects. By the time the next handshake happens, versions are in sync.
4. If auto-update fails (binary download fails, SCP fails, etc.) → hub reports the error to the UI via `endpoint_status_changed` with `status: "error"`. No silent degradation.

This means version skew is a transient state lasting only seconds during the update+restart cycle. No compatibility window, no major.minor versioning, no `ParseMessage` changes needed.

`ParseMessage()` currently hard-rejects unknown commands. This is correct under exact-match semantics — hub and remote always run the same version, so unknown commands can't occur in normal operation.

## Protocol Changes

### Additive changes to existing protocol

1. **`Session` model** — `endpoint_id` is already present from the preparatory refactors, so Phase C should reuse that field rather than introducing it again.

2. **`InitialStateMessage`** — Add optional `endpoints` field for hub to inform UI about connected remotes:
   ```
   model EndpointInfo {
     id: string;               // daemon_instance_id
     name: string;             // user-friendly label
     ssh_target: string;       // SSH connection target
     status: string;           // "bootstrapping" | "connecting" | "connected" | "disconnected" | "error"
     status_message?: string;  // human-readable progress ("Installing attn...", "Connected")
     session_count?: int32;
     capabilities?: EndpointCapabilities;
   }
   
   model EndpointCapabilities {
     protocol_version: string;
     agents_available: string[];    // ["claude", "codex"]
     projects_directory?: string;
     pty_backend_mode: string;      // "worker" | "embedded"
   }
   
   model InitialStateMessage {
     ...existing fields...
     endpoints?: EndpointInfo[];
   }
   ```

3. **New events:**
   - `endpoint_connected` — A remote endpoint came online.
   - `endpoint_disconnected` — A remote endpoint went offline.
   - `endpoints_updated` — Full endpoint list refresh.

4. **Command routing** — Commands that target a session (e.g., `pty_input`, `attach_session`, `kill_session`) already have the protocol model needed for endpoint-aware routing. The remaining work is hub-side routing logic and any missing endpoint-aware fields on endpoint-scoped commands.

### No breaking changes

All additions are optional fields and new commands — the protocol changes are additive in nature. However, **`ProtocolVersion` must be bumped** whenever new commands or events are introduced. This project relies on strict version checking at connect time to prevent stale background daemons from silently failing when the app sends commands they don't understand. A single version bump when hub support ships (covering all new endpoint commands/events) is sufficient — no need to bump per-command during development.

## Hub Endpoint Configuration

The hub needs to know which remote machines to connect to. Configuration is SSH-centric.

**Store in SQLite (via store):**

```sql
-- endpoints table
CREATE TABLE endpoints (
    id        TEXT PRIMARY KEY,  -- generated UUID
    name      TEXT NOT NULL,     -- user-friendly label ("gpu-box")
    ssh_target TEXT NOT NULL,    -- SSH connection target ("gpu-box", "user@10.0.0.5")
    enabled   INTEGER DEFAULT 1,
    created_at TEXT,
    updated_at TEXT
);
```

**Manageable via WS commands:**
- `add_endpoint { name, ssh_target }` → persists, triggers bootstrap + connect
- `remove_endpoint { endpoint_id }` → disconnects SSH, removes config
- `update_endpoint { endpoint_id, name?, ssh_target? }` → update, reconnect
- `list_endpoints` → returns all endpoints with current status + capabilities

This lets the UI manage endpoints without file editing.

## SSH Bootstrap Flow

When the hub connects to a new endpoint (or reconnects after version change):

```
1. Detect remote platform
   ssh -o BatchMode=yes -o ConnectTimeout=10 <target> 'uname -sm'
   → "Linux x86_64"  (reject non-Linux with clear error)

2. Check if attn is installed + version
   ssh <target> 'attn --version 2>/dev/null || echo NOT_FOUND'
   
3. Install or update if needed
   If NOT_FOUND or version mismatch:
     a. Find binary for target platform:
        - Check ~/.attn/remotes/binaries/attn-linux-amd64   (cached from last download)
        - Download from GitHub releases (matching local version)
        - Fallback: cross-compile from source (GOOS/GOARCH go build)
     b. scp binary to remote:
        scp <binary> <target>:~/.local/bin/attn
     c. ssh <target> 'chmod +x ~/.local/bin/attn'
   
4. Ensure remote daemon is running
   Use an internal hub-side SSH probe against the remote daemon socket/PID paths.
   The v1 plan does not add an `attn daemon --status` CLI command.
   If not running:
     ssh <target> 'nohup setsid attn daemon </dev/null >>~/.attn/daemon.log 2>&1 &'
     sleep 1
     repeat the health probe   (verify it started)
   If running but wrong version (just updated binary):
     restart from the hub via kill + start

5. Connect via ws-relay
   ssh <target> 'attn ws-relay'                           → stdin/stdout bridge
   Hub creates local TCP proxy, dials ws://localhost:<proxy>/ws
   Receives initial_state → capabilities extracted
```

**Binary distribution strategy:**

| Install source | How hub finds the right linux binary |
|---|---|
| Homebrew | `attn --version` → download matching release from GitHub (`gh release download`) |
| Source | `attn --version` → download release, or fallback to `GOOS=linux go build` |
| Release | Binary already has version baked in via `-ldflags` |

Requires: compiled-in version string (`-ldflags "-X main.version=v0.2.11"`). The homebrew formula and release workflow set this.

**Auto-update:** On every connect, compare local vs remote `attn --version`. If mismatch, SCP new binary, restart remote daemon, reconnect. This ensures hub and remote always run compatible versions.

## The `ws-relay` Subcommand

New `attn ws-relay` subcommand (~30 lines of Go). Runs on the remote machine, invoked by SSH:

```go
// cmd/attn/main.go — new case
case "ws-relay":
    runWSRelay()

func runWSRelay() {
    // Connect to local daemon's WS port (raw TCP — the HTTP upgrade
    // and WS framing happen end-to-end between hub and remote daemon)
    conn, err := net.Dial("tcp", "localhost:9849")
    if err != nil {
        fmt.Fprintf(os.Stderr, "cannot connect to daemon: %v\n", err)
        os.Exit(1)
    }
    defer conn.Close()
    
    // Bridge stdin/stdout ↔ TCP (raw bytes, bidirectional)
    // This is a dumb TCP pipe — WS framing passes through transparently
    go io.Copy(conn, os.Stdin)   // hub → daemon
    io.Copy(os.Stdout, conn)     // daemon → hub
}
```

**Why raw TCP works:** WebSocket is just HTTP upgrade + framed messages over TCP. The relay passes raw bytes — the HTTP upgrade request, the WS frames, everything — transparently between the SSH pipe and the daemon's TCP listener. The hub side performs the actual WS handshake through this transparent pipe. No WS awareness needed in the relay.

**Origin check:** The remote daemon's `isAllowedWSOrigin()` allows empty `Origin` headers (non-browser clients). The hub's WS dial through the relay won't set an Origin header, so this passes.

## Hub-Side Transport (Local TCP Proxy)

The hub needs to bridge the SSH process's stdin/stdout to a local TCP socket so the nhooyr.io/websocket library can `Dial()` a standard URL. This is `internal/hub/transport.go` (~60-80 lines):

```go
func connectViaSSH(ctx context.Context, sshTarget string) (*websocket.Conn, *exec.Cmd, error) {
    // 1. Spawn SSH process
    cmd := exec.CommandContext(ctx, "ssh",
        "-o", "BatchMode=yes",
        "-o", "StrictHostKeyChecking=accept-new",
        "-o", "ConnectTimeout=10",
        sshTarget, "attn", "ws-relay")
    stdin, _ := cmd.StdinPipe()
    stdout, _ := cmd.StdoutPipe()
    cmd.Start()
    
    // 2. Start local TCP listener on random port
    ln, _ := net.Listen("tcp", "127.0.0.1:0")
    port := ln.Addr().(*net.TCPAddr).Port
    
    // 3. Accept one connection, bridge to SSH stdin/stdout
    go func() {
        conn, err := ln.Accept()
        ln.Close()  // only need one connection
        if err != nil { return }
        
        // Bidirectional bridge: local TCP ↔ SSH stdio
        done := make(chan struct{})
        go func() { io.Copy(stdin, conn); close(done) }()   // WS → SSH → remote
        go func() { io.Copy(conn, stdout); <-done; conn.Close() }()  // remote → SSH → WS
    }()
    
    // 4. Dial WebSocket through the local proxy
    ws, _, err := websocket.Dial(ctx,
        fmt.Sprintf("ws://127.0.0.1:%d/ws", port), nil)
    if err != nil {
        cmd.Process.Kill()
        return nil, nil, err
    }
    
    return ws, cmd, nil
}
```

**Lifecycle management:**
- SSH process death → TCP connection closes → WS connection errors → hub detects disconnect → reconnect with backoff
- WS close → hub kills SSH process → clean shutdown
- Hub shutdown → kills all SSH processes

## Hub Session Merge Strategy

The hub maintains remote session state **in memory only** — remote sessions are NOT stored in the hub's SQLite database.

```go
// internal/hub/sessions.go
type endpointSessions struct {
    mu       sync.RWMutex
    sessions map[string][]protocol.Session  // endpoint_id → sessions
}
```

**On `initial_state` from remote:** Replace the entire session list for that endpoint. Tag each session with `endpoint_id`. Broadcast merged list to UI.

**On `session_registered` / `session_state_changed` from remote:** Update the specific session in the endpoint's list. Tag with `endpoint_id`. Forward the event to UI (with `endpoint_id` injected).

**On `sessions_updated` from remote:** The remote daemon sends its full session list via `broadcastSessionsUpdated()`. The hub replaces the endpoint's session list and re-broadcasts the merged list (local + all remotes) to the UI.

**On endpoint disconnect:** Remove all sessions for that endpoint from the in-memory map. Broadcast `sessions_updated` to UI so remote sessions disappear. Send `endpoint_status_changed` event.

**On endpoint reconnect:** Fresh `initial_state` rebuilds the session list. No stale state possible.

**Session ID collisions:** UUIDs are used for session IDs — collision probability is negligible. If it occurs, `endpoint_id` disambiguates (compound key).

## Hub Event Filtering

When the hub receives events from remote daemons, it must decide which to forward to the UI and which to ignore:

| Remote Event | Hub Action | Reason |
|---|---|---|
| `session_registered` | **Forward** (inject `endpoint_id`) | UI needs to see remote sessions |
| `session_state_changed` | **Forward** (inject `endpoint_id`) | UI needs state updates |
| `sessions_updated` | **Merge + re-broadcast** | Hub merges with local sessions |
| `pty_output`, `pty_desync` | **Forward** (inject `endpoint_id`) | PTY data for attached sessions |
| `spawn_result`, `attach_result` | **Forward** | UI waiting for result |
| `*_result` events (branch, review, etc.) | **Forward** | UI waiting for result |
| `git_status_update` | **Forward** | UI subscribed to git status |
| `prs_updated` | **Ignore** | PRs are hub-local |
| `repos_updated` | **Ignore** | PR repo state is hub-local |
| `authors_updated` | **Ignore** | PR author state is hub-local |
| `settings_updated` | **Ignore** | Settings are per-daemon |
| `warnings_updated` | **Ignore** | Warnings are per-daemon (hub has its own) |
| `command_error` | **Forward** | UI needs to see errors |

**Note:** `sendInitialState()` on the remote also triggers `go d.fetchAllPRDetails()` for every WS client connection. For v1 this is harmless (idempotent, one extra burst). If it becomes a problem, add a query parameter to the WS upgrade URL (e.g., `?hub=1`) that the remote can check to skip PR fetching.

## SSH Failure Handling

The bootstrap flow uses SSH with non-interactive flags to prevent hangs:

```bash
ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o ConnectTimeout=10 <target> ...
```

**`BatchMode=yes`** — Disables password prompts, passphrase prompts, and host key confirmation. If SSH can't authenticate non-interactively, it fails immediately. This prevents the hub's SSH process from hanging waiting for input that will never come.

**`StrictHostKeyChecking=accept-new`** — Automatically accepts host keys for new hosts (first connection). Rejects if a known host key changes (MITM protection). This avoids the interactive "Are you sure?" prompt.

**Error reporting:** Every SSH command checks exit code. Errors are reported to the UI via `endpoint_status_changed` with `status: "error"` and a descriptive `status_message`:

| Error | Status Message |
|---|---|
| SSH connection refused | "Cannot connect to <target>: connection refused" |
| SSH auth failure | "SSH authentication failed for <target>. Ensure SSH keys are configured." |
| SSH timeout | "Connection to <target> timed out after 10s" |
| Remote not Linux | "Unsupported remote platform: <uname output>. Only Linux is supported." |
| Binary install failed | "Failed to install attn on <target>: <error>" |
| Daemon start failed | "Failed to start daemon on <target>: <error>" |
| ws-relay connection failed | "Remote daemon not responding on <target>" |

**Supported remote platforms:** Linux only for v1 (amd64 and arm64). macOS remotes would require darwin binaries in releases — deferred.

**SSH agent:** The hub requires that SSH keys are loaded in an agent (or use keyless auth like `IdentityFile`). Passphrase-protected keys without an agent will fail with a clear error message due to `BatchMode=yes`.

## Implementation Phases

These phases assume the completed groundwork in [docs/plans/2026-04-02-preparatory-refactors.md](/Users/victor.arias/projects/victor/attn/docs/plans/2026-04-02-preparatory-refactors.md).

### Phase A: Remote bootstrap infrastructure

**Goal:** The hub can SSH into a remote machine, install attn, start the daemon, and connect via `ws-relay`. No UI yet — CLI/config-driven.

Changes:
1. Add `attn ws-relay` subcommand (stdin/stdout ↔ localhost:9849 TCP bridge, ~30 lines).
2. Add a remote daemon health/start probe inside `internal/hub/bootstrap.go`, using SSH to validate the remote socket/PID state directly rather than adding a `daemon --status` CLI command.
3. Add `internal/hub/bootstrap.go` — SSH-based bootstrap logic: detect platform, check/install binary, ensure daemon running.
4. Add `internal/hub/transport.go` — SSH stdio transport: spawn `ssh <target> attn ws-relay`, local TCP proxy, return `*websocket.Conn`.
5. Add `ATTN_WS_BIND` env var support (default: `127.0.0.1`). Optional for direct connections.
6. Add `ATTN_WS_AUTH_TOKEN` env var support. Optional for direct connections.

Inherited prerequisites from the completed preparatory refactors:
- `attn --version` already exists and is suitable for remote binary version checks
- Linux release binaries already exist for remote bootstrap/autoupdate
- frontend WS URL abstraction is already in place for future direct-connection support

**Exit gate:** Can run `attn ws-relay` on a remote machine via SSH and get a working WebSocket connection. Binary auto-installs on remote. Remote startup/version checks are automated.

### Phase B: Walking skeleton — connect, handshake, show capabilities

**Goal:** UI can add remote endpoints by SSH target, hub bootstraps and connects automatically, and displays status and capabilities. No session forwarding yet — just prove the end-to-end works.

**What `initial_state` already provides (no remote changes needed):**
- Protocol version — check compatibility
- `daemon_instance_id` — unique identity
- Agent availability (claude, codex, copilot) via `settings.claude_available` etc.
- Projects directory path via `settings.projects_directory`
- PTY backend mode (worker/embedded)
- Current sessions (count for display; not merged into local list yet)

Changes — Hub (Go):
1. Add endpoint config to store (SQLite): `name`, `ssh_target`, `enabled`.
2. Add `internal/hub/endpoint.go` — Per-endpoint lifecycle: bootstrap (from Phase A), connect via ws-relay, receive `initial_state`, store capabilities. Reconnect with backoff on disconnect. Auto-update binary on version mismatch.
3. New WS commands for the UI:
   - `add_endpoint { name, ssh_target }` — saves config, triggers bootstrap + connect.
   - `remove_endpoint { endpoint_id }` — kills SSH process, removes config.
   - `update_endpoint { endpoint_id, name?, ssh_target? }` — edit, reconnect.
   - `list_endpoints` — returns all endpoints with current status.
4. New WS event: `endpoint_status_changed` — broadcast to UI clients whenever an endpoint's status changes. Payload includes:
   - `endpoint_id`, `name`, `ssh_target`, `status` (bootstrapping/connecting/connected/disconnected/error)
   - `status_message` (e.g., "Installing attn...", "Starting daemon...", "Connected")
   - `capabilities` (when connected): agents available, projects dir, session count, protocol version, daemon instance ID, PTY backend mode.
5. Hub starts endpoint clients on daemon boot for all enabled endpoints.
6. Protocol version check on handshake: if versions differ, disconnect, auto-update remote binary via SCP, restart remote daemon, reconnect. Report progress via `endpoint_status_changed`.

Changes — UI:
1. Endpoint management panel (settings or sidebar section):
   - Add endpoint form: friendly name + SSH target (e.g., `gpu-box` or `user@10.0.0.5`).
   - Per-endpoint status indicator with bootstrap progress: ⏳ bootstrapping / 🟡 connecting / 🟢 connected / 🔴 disconnected / ⚠️ error.
   - Status message showing current step ("Installing attn...", "Starting daemon...", "Connected").
   - Expandable card showing capabilities: available agents, projects directory, session count, protocol version.
   - Edit / remove endpoint buttons.
2. No session list integration — remote sessions are not shown in the main session list yet.

**Exit gate:** Can type `gpu-box` into the UI, watch it bootstrap (install binary, start daemon), connect (green indicator), and show capabilities. Reconnects automatically. Auto-updates remote binary on version mismatch.

### Phase C: Session visibility — remote sessions in the list

**Goal:** Remote sessions appear in the main session list, tagged with their endpoint. Read-only initially (can see state, but can't attach/interact yet).

Changes:
1. Reuse the existing `Session.endpoint_id` field for merged remote session identity. Local sessions keep it empty/omitted.
2. Hub merges remote sessions into the local session list on `initial_state` and on `session_registered`/`session_state_changed` events from remote. Tags each with `endpoint_id`.
3. Hub forwards relevant remote events to local UI clients: `session_registered`, `session_state_changed`, `session_removed`.
4. Frontend groups or labels sessions by endpoint (e.g., endpoint name badge on each card).
5. Endpoint status indicator visible in session list area (not just settings).

**Exit gate:** Remote sessions appear in the Tauri app session list with endpoint labels. State updates (working/idle/etc.) propagate in real-time. Sessions disappear when endpoint disconnects.

### Phase D: Full interactivity — PTY, spawn, git

**Goal:** Full interactive usage of remote sessions from the Tauri app.

Changes:
1. Add `internal/hub/router.go` — Command routing by `endpoint_id` lookup, reusing the existing `internal/daemon/command_meta.go` scope registry instead of inventing a second scope table.
2. PTY attach/detach/input/resize proxied to remote endpoint. Use raw message forwarding for `pty_output` events (§10 efficiency).
3. Spawn session UI allows selecting target endpoint.
4. Git operations (branch, stash, worktree, diff) forwarded to the endpoint owning the session's directory.
5. Destructive/scoped commands (`clear_sessions`, `set_setting`) require explicit `endpoint_id` targeting — never broadcast (see §8).

Current state:

- implemented: attach/input/resize, remote spawn from the app, remote reload, git/diff/review routing, review comment mutations, and endpoint-aware packaged-app verification
- intentionally unsupported: remote fork
- supported with constraint: remote open-in-editor only for Zed remote SSH targets
- intentionally not pursued here: branch switching
- packaged verification is in place for remote review-loop start/stop/awaiting-user/answer flows

**Exit gate:** Can attach to a remote session's terminal, type commands, see output. Can spawn new sessions on remote endpoints. Git operations work. Review/comment mutations work. Remote review-loop flows work end to end.

### Phase E: Direct connections and hardening

**Goal:** Support non-SSH connections (Tailscale, direct) and production hardening.

Changes:
1. TLS support for direct WS connections (`ATTN_WS_TLS_CERT`, `ATTN_WS_TLS_KEY`).
2. Token-based auth with rotation for direct connections.
3. Endpoint health monitoring and reconnection telemetry.
4. Rate limiting on remote endpoint connections.
5. UI option to add endpoint by direct URL+token (alternative to SSH target).

**Exit gate:** Secure remote daemon usage over untrusted networks without SSH.

## Risks and Mitigations

### 1. PTY I/O latency over network
- **Risk:** Terminal feels sluggish for remote sessions.
- **Mitigation:** Use raw message forwarding (byte-level `endpoint_id` splice) to avoid full deserialization for `pty_output` — the highest-volume message type. Network latency is additive but typically <50ms on good networks. Binary WebSocket frames can eliminate base64 overhead if needed (deferred optimization). Measure latency before and after proxying to validate.

### 2. Hub reconnection complexity
- **Risk:** SSH process dies or remote daemon restarts; hub must cleanly reconnect and re-sync state.
- **Mitigation:** SSH process death is clean — hub detects it, kills local proxy, and restarts the full SSH→ws-relay flow with backoff. The remote daemon already sends `initial_state` on connect. Hub treats reconnect like a fresh connect — receives new state, diffs against last known, broadcasts updates. Same pattern the Tauri app already handles. Auto-update on reconnect ensures version consistency.

### 3. Split-brain session state
- **Risk:** Hub and remote daemon disagree about session state during network partition.
- **Mitigation:** Remote daemon is authoritative. Hub marks endpoint as disconnected during partition. When reconnected, `initial_state` from remote is truth. Hub never writes to remote store.

### 4. Command ordering during reconnect
- **Risk:** User sends command during momentary disconnect, command is lost.
- **Mitigation:** Hub returns error event immediately if endpoint is disconnected. Frontend shows endpoint status so user knows. No silent drops.

### 5. Scaling to many endpoints
- **Risk:** Too many remote daemons overwhelm the hub.
- **Mitigation:** Practical limit is ~5-10 endpoints (developer workload). Each endpoint is one WS connection with modest event traffic. Not a concern for v1.

### 6. ~~Protocol version skew across machines~~ (RESOLVED)
- **Risk:** Version differences during upgrades.
- **Resolution:** Auto-update via SSH bootstrap eliminates version skew. Hub always updates remote binary to match before connecting. Transient skew lasts only seconds during the update+restart cycle.

### 7. Request/response correlation with multiple UI clients
- **Risk:** With multiple UI clients connected to the hub, result events from remote daemons can't be routed to the correct originating client (no request IDs in protocol).
- **Mitigation:** V1 is single-client — forward all results to the one connected UI. For multi-client, add optional `request_id` field to commands/results (additive protocol change, deferred). See decision §9.

### 8. Command scoping accidents
- **Risk:** Hub-scoped commands like `clear_sessions` could be accidentally forwarded to all remote daemons, causing unintended data loss.
- **Mitigation:** Command audit (§8) classifies every command's scope. Destructive commands require explicit `endpoint_id` targeting and are never broadcast. Hub defaults to local-only for ambiguous commands.

## Decisions Locked

1. Hub topology: local daemon is hub, remotes are standard daemons.
2. Hub↔remote transport: SSH stdio via `attn ws-relay` (Zed-style). SSH is the auth + encryption layer.
3. Session identity: `(endpoint_id, session_id)` compound key.
4. Remote daemons are autonomous: no special "headless mode" code path.
5. Binary distribution: release binaries for linux/amd64 and linux/arm64. Auto-install and auto-update via SSH.
6. PTY workers are untouched by this work.
7. Hooks IPC is untouched (stays local unix socket on each machine).
8. GitHub PR polling stays on the hub (local) daemon.
9. Version matching: exact match, auto-update on mismatch.

## Deferred Decisions

1. Web client (non-Tauri) for browser-only access.
2. Cross-endpoint session migration (move a session from one machine to another).
3. Shared review state across endpoints.
4. Multi-user access to the same hub.
5. macOS remote targets (requires darwin release binaries).

## Acceptance Criteria (MVP — Phases A+B+C+D)

1. Local daemon connects to one remote daemon via SSH stdio (`attn ws-relay`).
2. Remote attn binary is auto-installed and auto-updated via SSH.
3. Remote sessions appear in the Tauri app alongside local sessions, labeled by endpoint.
4. PTY attach/input/output works for remote sessions with acceptable latency (<100ms added).
5. Git operations (branch list, diff, status) work for remote sessions.
6. Session state changes on remote daemon are reflected in UI within 1 second.
7. Remote daemon restart triggers reconnect and state re-sync without UI crash.
8. Disconnected endpoint shows clear status indicator in UI.
9. Local sessions continue working unchanged when no remote endpoints are configured.
10. Protocol version compatibility is maintained (additive changes only).

## Acceptance Criteria (Walking Skeleton — Phases A+B only)

1. `attn ws-relay` subcommand works (bridges stdin/stdout ↔ localhost WS).
2. Hub can use the existing `attn --version` output and released Linux binaries to install/update remotes.
3. UI can add/remove/edit remote endpoints by SSH target.
4. Hub automatically installs attn on remote if missing, updates if version differs.
5. Hub connects via SSH stdio and receives `initial_state`.
6. Each endpoint shows status with progress (⏳ bootstrapping / 🟢 connected / 🔴 error).
7. Connected endpoints display capabilities: available agents, projects directory, session count.
8. Reconnects automatically after SSH disconnect with backoff.
9. Local functionality is completely unaffected by remote endpoint configuration.

## Integrated Smoke Scenario (Current Phase D)

This is the highest-signal end-to-end verification path for the current implementation. It uses the real packaged Tauri app, the real local daemon, SSH bootstrap, and a real remote daemon, and it now exercises the real remote interactive path instead of seeding synthetic remote session state.

Example target: `ssh ai-sandbox`

Flow:

1. Launch the packaged app with the dev-only UI automation bridge enabled.
2. Reset the remote target before app startup so previously persisted local endpoints cannot race the bootstrap flow.
3. Add endpoint `ai-sandbox` to the local daemon and wait for `endpoint_status_changed` to report `connected`.
4. Create a brand-new remote session from the packaged app, targeting the connected endpoint.
5. Verify the session registers through the hub, appears in the main app UI, and shows the expected endpoint badge.
6. Attach to the remote session, split a utility pane, type into the remote PTY, and verify the output returns through the app.
7. Reload the remote session from the app and verify the main runtime is replaced rather than silently reattaching to stale state.
8. Create or reuse a real remote git repo, dirty a tracked file, and verify the packaged diff/review panel loads that remote change set.
9. Add, update, resolve, wont-fix, and delete a remote review comment through the app, and verify each mutation round-trips successfully.
10. Start a remote review loop, stop one run while it is active, then start another run that reaches `awaiting_user`, answer it, and verify completion in both daemon state and the packaged review-loop drawer.
11. Remove the session and endpoint and verify cleanup in both the observer and the packaged UI.

What this scenario proves now:

- SSH bootstrap/install/update works against a real host
- hub↔remote websocket handshake and reconnect state work
- remote sessions merge into the app session model correctly
- endpoint-aware grouping/labeling works in the real UI
- remote PTY interactivity works in the packaged app
- remote session creation and reload from the app work
- remote diff/review panel routing works
- remote review comment mutations work
- remote review-loop start/stop/awaiting-user/answer flows work in the packaged app

What it does not prove yet:

- Settings-modal CRUD driven entirely through UI automation
