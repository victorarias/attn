# Remote Daemon Hub: Multi-Machine Session Control

Date: 2026-02-21
Status: Planning
Owner: daemon/frontend

## Motivation

attn currently runs everything on one machine: daemon, agents, git, UI. The goal is to run agent workloads on remote machines (dev servers, VMs, cloud instances) while keeping a single local UI that shows all sessions across all machines.

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
- Phase A (expose WS) lets you SSH-tunnel to a remote daemon today.
- Phase B (hub client) lets the local daemon aggregate remotes.
- Phase C (UI) shows remote sessions in the app.
- Phase D (auth/TLS) makes it production-safe.

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

**Recommendation: Option B.** It's cleaner, doesn't break ID formats, and the `Session` model already has optional fields. Adding `endpoint_id?: string` is additive and backward-compatible. Local sessions have no endpoint_id (or a sentinel like `"local"`).

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

1. **`Session` model** — Add optional `endpoint_id` field:
   ```
   model Session {
     ...existing fields...
     endpoint_id?: string;  // Which daemon owns this session. Absent = local.
   }
   ```

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

4. **Command routing** — Commands that target a session (e.g., `pty_input`, `attach_session`, `kill_session`) gain an optional `endpoint_id` field. If present, the hub forwards to that endpoint. If absent, handled locally.

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
   ssh <target> 'attn daemon --status'
   If not running:
     ssh <target> 'nohup setsid attn daemon </dev/null >>~/.attn/daemon.log 2>&1 &'
     sleep 1
     ssh <target> 'attn daemon --status'   (verify it started)
   If running but wrong version (just updated binary):
     ssh <target> 'attn daemon --restart'   (or kill + start)

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

### Phase A: Remote bootstrap infrastructure

**Goal:** The hub can SSH into a remote machine, install attn, start the daemon, and connect via `ws-relay`. No UI yet — CLI/config-driven.

Changes:
1. Add compiled-in version string via `-ldflags "-X main.version=..."`. Update Makefile, homebrew formula, release workflow.
2. Add `attn ws-relay` subcommand (stdin/stdout ↔ localhost:9849 TCP bridge, ~30 lines).
3. Add `attn daemon --status` subcommand (prints running/stopped + version, ~10 lines).
4. Add linux/amd64 and linux/arm64 to release workflow (two more `GOOS/GOARCH go build` lines + `gh release upload`).
5. Add `internal/hub/bootstrap.go` — SSH-based bootstrap logic: detect platform, check/install binary, ensure daemon running.
6. Add `internal/hub/transport.go` — SSH stdio transport: spawn `ssh <target> attn ws-relay`, local TCP proxy, return `*websocket.Conn`.
7. Add `ATTN_WS_BIND` env var support (default: `127.0.0.1`). Optional for direct connections.
8. Add `ATTN_WS_AUTH_TOKEN` env var support. Optional for direct connections.

**Exit gate:** Can run `attn ws-relay` on a remote machine via SSH and get a working WebSocket connection. Binary auto-installs on remote. `attn --version` works.

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
1. Add `endpoint_id` field to `Session` TypeSpec model. Regenerate types. Local sessions have `endpoint_id = ""` (or omitted).
2. Hub merges remote sessions into the local session list on `initial_state` and on `session_registered`/`session_state_changed` events from remote. Tags each with `endpoint_id`.
3. Hub forwards relevant remote events to local UI clients: `session_registered`, `session_state_changed`, `session_removed`.
4. Frontend groups or labels sessions by endpoint (e.g., endpoint name badge on each card).
5. Endpoint status indicator visible in session list area (not just settings).

**Exit gate:** Remote sessions appear in the Tauri app session list with endpoint labels. State updates (working/idle/etc.) propagate in real-time. Sessions disappear when endpoint disconnects.

### Phase D: Full interactivity — PTY, spawn, git

**Goal:** Full interactive usage of remote sessions from the Tauri app.

**Prerequisites from prep refactors:**
- Command routing extraction (§0) — command_meta.go scope classifications guide the hub router.
- Command scoping audit (§8 in Addenda) — determines which commands forward, which are hub-local.

Changes:
1. Add `internal/hub/router.go` — Command routing by `endpoint_id` lookup. Uses `CommandMeta` scope classifications from §0.
2. PTY attach/detach/input/resize proxied to remote endpoint. Use raw message forwarding for `pty_output` events (§10 efficiency).
3. Spawn session UI allows selecting target endpoint.
4. Git operations (branch, stash, worktree, diff) forwarded to the endpoint owning the session's directory.
5. Destructive/scoped commands (`clear_sessions`, `set_setting`) require explicit `endpoint_id` targeting — never broadcast (see §8).

**Exit gate:** Can attach to a remote session's terminal, type commands, see output. Can spawn new sessions on remote endpoints. Git operations work.

### Phase E: Direct connections and hardening

**Goal:** Support non-SSH connections (Tailscale, direct) and production hardening.

Changes:
1. TLS support for direct WS connections (`ATTN_WS_TLS_CERT`, `ATTN_WS_TLS_KEY`).
2. Token-based auth with rotation for direct connections.
3. Endpoint health monitoring and reconnection telemetry.
4. Rate limiting on remote endpoint connections.
5. UI option to add endpoint by direct URL+token (alternative to SSH target).

**Exit gate:** Secure remote daemon usage over untrusted networks without SSH.

## Preparatory Refactors (Can Start Now)

These changes are independently valuable and unblock later phases:

### 0. Extract command routing in websocket.go (Phase D prep, do first)

The `handleClientMessage` switch in `websocket.go` has 62 case arms across ~430 lines, with many handlers implemented inline. This makes it hard to reason about command routing, and the hub (Phase D) needs to intercept and forward commands by scope.

**Approach: Annotated switch + domain files + scope metadata (Option C)**

1. **Thin the switch** — Every case becomes a 1-line delegation to a method in a domain-specific file. This is already the pattern for `branch.go`, `stash.go`, and `worktree.go` — just finish the job for the remaining domains.

2. **Create domain files** — Move inline handler implementations out of `websocket.go`:

   | New file | Commands moved | Approx cases |
   |---|---|---|
   | `ws_pr.go` | approve, merge, mute_pr, mute_repo, mute_author, refresh, fetch_details, pr_visited, inject_test_pr, collapse, query_prs/repos/authors | ~13 |
   | `ws_session.go` | session_visualized, clear_sessions, clear_warnings, unregister, mute, get_recent_locations, inject_test_session | ~7 |
   | `ws_settings.go` | get_settings, set_setting + all validate* funcs | ~2 cases + 120 lines |
   | `ws_pty.go` | spawn, attach, detach, input, resize, kill + forwardPTYStreamEvents | ~6 cases + 140 lines |
   | `ws_git.go` | subscribe/unsubscribe git_status, get_file_diff, get_branch_diff_files + sendGitStatusUpdate | ~4 cases + 140 lines |
   | `ws_review.go` | review_state, mark_file_viewed, add/update/resolve/wontfix/delete comment, get_comments, start/cancel review | ~10 cases + 180 lines |

   Already done: `branch.go` (~5 cases), `stash.go` (~5 cases), `worktree.go` (~4 cases).

   Result: `websocket.go` shrinks from 1774 to ~400 lines (switch routing table + WS infrastructure).

3. **Add `command_meta.go`** — A scope registry that classifies every command for hub routing:

   ```go
   type CommandScope int
   const (
       ScopeSession  CommandScope = iota  // forward to owning endpoint
       ScopeEndpoint                       // forward to target endpoint
       ScopeHubLocal                       // handle on hub only
       ScopeHubMerge                       // merge results from all endpoints
   )
   
   var CommandMeta = map[string]CommandScope{
       protocol.CmdPtyInput:      ScopeSession,
       protocol.CmdClearSessions: ScopeHubLocal,
       protocol.CmdQueryPRs:      ScopeHubLocal,
       // ... all 60+ commands classified
   }
   ```

   This directly enables Phase B hub routing: `scope := CommandMeta[cmd]; if scope == ScopeSession { forward to endpoint }`.

**Exit gate:** All existing tests pass. `websocket.go` contains only WS infrastructure and the thin switch. Every command has a scope classification in `command_meta.go`.

### 1. Add compiled-in version string (Phase A prerequisite — do second)

**This is a hard prerequisite for Phase A and B.** Nothing in the bootstrap or auto-update flow works without it.

Currently no version string exists anywhere: no `-ldflags`, no `--version` subcommand, not in Makefile, Formula, or release workflow.

Changes needed:
- Add `var version = "dev"` in `cmd/attn/main.go`
- Add `attn --version` subcommand that prints the version
- Add `-ldflags "-X main.version=$(VERSION)"` to Makefile `build` and `install` targets
- Update `Formula/attn.rb` to pass ldflags: `system "go", "build", *std_go_args(ldflags: "-X main.version=#{version}"), "./cmd/attn"`
- Update `.github/workflows/release.yml` to include ldflags in Go build step
- Version is derived from the git tag in CI, from `package.json` or Makefile var locally

### 2. Add linux targets to release workflow (Phase A prerequisite)

The current release workflow only produces `darwin/arm64` and is tightly coupled to the Tauri build action (which creates the GitHub release). Adding Linux requires careful coordination:

1. Add a `linux-binaries` job that runs on `ubuntu-latest` (or in the existing `tauri` job after the Tauri action creates the release)
2. Build `GOOS=linux GOARCH=amd64` and `GOOS=linux GOARCH=arm64` with ldflags
3. Upload binaries via `gh release upload $TAG attn-linux-amd64 attn-linux-arm64 --clobber`
4. **CGO consideration:** Check if the SQLite dependency requires CGO. If so, either use `CGO_ENABLED=0` with a pure-Go SQLite driver, or set up cross-compilation toolchain. Test that the resulting binary runs correctly.
5. Name binaries consistently: `attn-linux-amd64`, `attn-linux-arm64` (matching the `uname -sm` detection in bootstrap)

### 3. Add `endpoint_id` to Session model (Phase C prep)

Additive field in TypeSpec, regenerate. Frontend ignores it until Phase C. Store can persist it.

### 4. Abstract the frontend WS URL (Phase D prep)

Currently hardcoded to `ws://127.0.0.1:${port}/ws`. Make it configurable via settings or an endpoint profile, preparing for multi-endpoint client.

### 5. ~~Introduce major.minor protocol versioning~~ (DROPPED)

No longer needed. Exact version matching + auto-update via SSH bootstrap means hub and remote always run the same version. The compatibility window was needed for manual deployments; SSH-based auto-update eliminates the problem. See §11.

## Risks and Mitigations

### 1. PTY I/O latency over network
- **Risk:** Terminal feels sluggish for remote sessions. Every output chunk goes through JSON parse → route → JSON serialize → WS send, all base64-encoded.
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
2. `attn --version` returns compiled-in version string.
3. Release workflow produces linux/amd64 and linux/arm64 binaries.
4. UI can add/remove/edit remote endpoints by SSH target.
5. Hub automatically installs attn on remote if missing, updates if version differs.
6. Hub connects via SSH stdio and receives `initial_state`.
7. Each endpoint shows status with progress (⏳ bootstrapping / 🟢 connected / 🔴 error).
8. Connected endpoints display capabilities: available agents, projects directory, session count.
9. Reconnects automatically after SSH disconnect with backoff.
10. Local functionality is completely unaffected by remote endpoint configuration.
