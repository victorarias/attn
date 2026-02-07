# Daemonize: Move Agent Process Management to the Daemon

## Problem

Today, the Tauri app owns the agent lifecycle:

```
Tauri PTY Manager (Rust)  →  spawns claude/codex in PTY
                          →  streams I/O via Tauri IPC events
                          →  kills process on session close
```

The daemon is a passive observer — it tracks sessions registered by the `attn` wrapper but has no control over the processes. This means:

1. **No session persistence.** If the Tauri app crashes or restarts, all running sessions die with it. There's no way to reconnect to a running claude session.
2. **Tight coupling to Tauri.** Terminal I/O flows through Tauri IPC (`pty-event` events, `pty_write` commands), making a web client or remote access impossible.
3. **Split brain.** Session state lives in two places: the daemon's SQLite store (session metadata, state) and Tauri's Zustand store (terminal instance, PTY handle). They can drift.

## Goal

Move agent process management into the Go daemon. The daemon spawns PTYs, manages process lifecycles, and streams terminal I/O to clients over WebSocket. Clients become thin renderers — they display output and forward keystrokes, but hold no critical state.

## Implementation Status (2026-02-07)

- [x] Moved plan file to `docs/plans/demonize.md`
- [x] Added daemon-side PTY manager package (`internal/pty`) with lifecycle, scrollback, and safe chunk boundaries
- [x] Added PTY protocol commands/events and wired daemon WebSocket handlers
- [x] Migrated frontend PTY transport to WebSocket (no Tauri PTY IPC path)
- [x] Added wrapper managed mode (`ATTN_DAEMON_MANAGED=1`) to avoid double registration
- [x] Hardened daemon start behavior (refuse startup when another daemon holds PID lock)
- [x] Disabled frontend daemon auto-restart loop; reconnect now waits for manual daemon recovery
- [x] Removed Rust PTY manager and Tauri PTY command wiring
- [x] Added WebSocket `command_error` response event for unknown/invalid commands
- [~] Daemon upgrade notification flow (version-mismatch message now includes manual restart guidance + active session count)
- [x] Ported codex output-based state detection heuristics into Go PTY path
- [x] Drafted Phase 8 shim-based restart-survival plan (implementation pending)
- [x] Made `unregister` a hard-stop lifecycle operation; `detach_session` remains detach-only
- [x] Moved `pty_input` onto the ordered WebSocket command path (removed bypass fast-path)
- [x] Added frontend PTY output buffering to prevent dropped prompt/scrollback before terminal readiness
- [x] Enforced stricter TypeSpec event coverage for daemon/reviewer WebSocket payloads and regenerated generated types
- [x] Fixed Tauri daemon socket detection path to match daemon default (`~/.attn/attn.sock`)

### Design constraint: remote-ready

The daemon will remain local-only for now, but every design decision must support a future where the daemon runs on machine A and the client runs on machine B connected over the network. This means:

- WebSocket is the **sole** channel for terminal I/O between daemon and client. No Tauri IPC for PTY data.
- All session state lives in the daemon. The client is stateless (terminal content is a cache that can be rebuilt from scrollback).
- The protocol must work over a network link with non-trivial latency (tens of ms).
- Authentication, TLS, and compression are out of scope but the protocol must not preclude them.

---

## Architecture: Before and After

### Before (current)

```
┌─────────────────────────────────┐
│         Tauri App               │
│  ┌───────────┐  ┌────────────┐ │        ┌──────────────┐
│  │ xterm.js  │←→│ PTY Mgr    │─│──PTY──→│ attn wrapper │→ claude
│  │           │  │ (Rust)     │ │        │  + hooks     │
│  └───────────┘  └────────────┘ │        └──────┬───────┘
│       ↕ Tauri IPC              │               │ unix socket
│  ┌────────────────────┐        │        ┌──────▼───────┐
│  │ useDaemonSocket.ts │←──WS──→│────────│   Daemon     │
│  └────────────────────┘        │        │  (passive)   │
└─────────────────────────────────┘        └──────────────┘
```

### After (proposed)

```
┌──────────────────────────────┐
│       Client (Tauri/Web)     │
│  ┌───────────┐               │
│  │ xterm.js  │               │        ┌────────────────────────┐
│  │           │               │        │        Daemon          │
│  └─────┬─────┘               │        │                        │
│        ↕                     │        │  ┌──────────────────┐  │
│  ┌─────────────────────┐     │        │  │  PTY Manager     │  │
│  │ useDaemonSocket.ts  │←─WS─│───────→│  │  (Go)            │──│──PTY──→ claude
│  │  (+ terminal cmds)  │     │        │  │  • scrollback    │  │         codex
│  └─────────────────────┘     │        │  │  • state detect   │  │
└──────────────────────────────┘        │  └──────────────────┘  │
                                        │                        │
                                        │  SQLite, hooks, git,   │
                                        │  GitHub polling, ...   │
                                        └────────────────────────┘
```

Key change: the PTY manager moves from Rust/Tauri into the Go daemon. The WebSocket carries terminal I/O alongside the existing control messages.

---

## Protocol Design

### New Commands (client → daemon)

| Command | Payload | Response Event | Description |
|---------|---------|----------------|-------------|
| `spawn_session` | `{ id, cwd, agent, cols, rows, label?, resume_session_id?, fork_session?, claude_executable?, codex_executable? }` | `spawn_result` | Daemon creates PTY, spawns agent process |
| `attach_session` | `{ id }` | `attach_result` + scrollback + `pty_output` stream | Client subscribes to terminal output for session |
| `detach_session` | `{ id }` | — | Client unsubscribes from terminal output |
| `pty_input` | `{ id, data }` | — | Forward keystrokes to PTY stdin |
| `pty_resize` | `{ id, cols, rows }` | — | Resize PTY |
| `kill_session` | `{ id, signal? }` | `session_exited` | Send signal to process (default SIGTERM) |

### New Events (daemon → client)

| Event | Payload | Description |
|-------|---------|-------------|
| `pty_output` | `{ id, data, seq }` | Terminal output chunk (base64-encoded in JSON) |
| `spawn_result` | `{ id, success, error? }` | Result of `spawn_session` |
| `attach_result` | `{ id, success, scrollback?, scrollback_truncated, last_seq, cols, rows, pid, running }` | Result of `attach_session`, includes scrollback buffer |
| `session_exited` | `{ id, exit_code, signal? }` | Process exited |
| `pty_desync` | `{ id, reason }` | Client fell behind; must re-attach to recover terminal state |

### Transport: JSON + base64 (binary frames deferred)

All messages use JSON text frames, including `pty_output` which base64-encodes terminal data. This adds ~33% overhead but keeps the protocol simple and debuggable.

**Why not binary frames now:** Binary frames add dual parsing paths, ordering concerns between text and binary writes, and testing complexity. The overhead is negligible for local use. Binary transport is a future optimization when remote use is real — the refactoring surface is small if we keep an `outboundMessage{kind, payload}` abstraction in the WebSocket hub now.

**Preparation for binary frames later:** The WebSocket hub's send path should use an internal `outboundMessage` type rather than raw `[]byte`. This makes adding binary frame support a localized change to the write goroutine.

### Input path: `pty_input`

Keyboard input is small (typically 1-few bytes per keystroke) and latency-sensitive. JSON encoding is fine:

```json
{"cmd": "pty_input", "id": "session-uuid", "data": "ls -la\r"}
```

For special keys, the client sends the terminal escape sequence (xterm.js `onData` already does this).

### Sequence numbers

Every `pty_output` event carries a monotonically increasing `seq` (uint32, per session). This enables:

- Client detection of dropped frames (gaps in sequence)
- `attach_result` includes `last_seq` so client knows where the stream picks up
- Future: selective retransmission for remote use

---

## Daemon-Side PTY Manager (`internal/pty/`)

New Go package that handles process lifecycle and I/O.

### Design Principle: Backend Interface

PTY implementation details (file descriptors, process handles) must stay private to `internal/pty`. The daemon and WebSocket layers interact through methods only. This ensures a clean evolution path to a future shim model (see "Future: Daemon Restart Survival") without leaking implementation details.

### Core Types

```go
package pty

type Manager struct {
    sessions map[string]*Session
    mu       sync.RWMutex
    logf     func(string, ...interface{})
}

type Session struct {
    // Public metadata (read via methods)
    id         string
    cwd        string
    agent      string // "claude" | "codex" | "shell"
    cols, rows uint16

    // Private: PTY and process (not exposed to callers)
    ptmx       *os.File
    cmd        *exec.Cmd

    // Scrollback
    scrollback *RingBuffer
    seqCounter uint32

    // Subscribers (attached clients)
    subscribers map[*Subscriber]bool
    subMu       sync.RWMutex

    // Lifecycle
    exitCode    *int
    exitSignal  *string
    exited      chan struct{}
}

type Subscriber struct {
    Send func(data []byte, seq uint32)  // callback to push data to client
}
```

### PTY Creation

Use `github.com/creack/pty` (mature, well-maintained Go PTY library):

```go
func (m *Manager) Spawn(opts SpawnOptions) error {
    cmd := exec.Command("attn", agentArgs...)
    cmd.Dir = opts.CWD
    cmd.Env = append(os.Environ(),
        "ATTN_INSIDE_APP=1",
        "ATTN_SESSION_ID="+opts.ID,
        "ATTN_AGENT="+opts.Agent,
        "ATTN_DAEMON_MANAGED=1",
        "TERM=xterm-256color",
    )
    cmd.SysProcAttr = &syscall.SysProcAttr{
        Setpgid: true, // New process group for clean shutdown
    }

    ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
        Rows: opts.Rows, Cols: opts.Cols,
    })
    // ... store session, start reader goroutine
}
```

### Reader Goroutine

One goroutine per session reads from the PTY master and distributes to subscribers:

```go
func (s *Session) readLoop() {
    buf := make([]byte, 16384) // 16KB read buffer
    var carryover []byte       // incomplete UTF-8/ANSI bytes from previous read

    for {
        n, err := s.ptmx.Read(buf)
        if n > 0 {
            chunk := append(carryover, buf[:n]...)
            boundary := findSafeBoundary(chunk)
            carryover = make([]byte, len(chunk)-boundary)
            copy(carryover, chunk[boundary:])
            data := chunk[:boundary]

            if len(data) > 0 {
                seq := atomic.AddUint32(&s.seqCounter, 1)
                s.scrollback.Write(data)
                s.fanOut(data, seq)
            }
        }
        if err != nil {
            break
        }
    }
    // Flush carryover
    if len(carryover) > 0 {
        seq := atomic.AddUint32(&s.seqCounter, 1)
        s.scrollback.Write(carryover)
        s.fanOut(carryover, seq)
    }
    // Collect exit status
    s.cmd.Wait()
    close(s.exited)
}
```

### Scrollback Buffer

A ring buffer that retains the last N bytes of terminal output. When a client attaches (or reconnects), it receives the scrollback to rebuild terminal state.

```go
type RingBuffer struct {
    buf  []byte
    size int
    pos  int
    full bool
}
```

**Size:** 1MB per session (configurable). This is ~500K characters of terminal output — more than enough for typical claude sessions. At 10 concurrent sessions, that's 10MB — trivial.

**Scrollback is best-effort replay, not a terminal snapshot.** The client replays raw bytes through xterm.js to rebuild state. This works well for Claude/Codex sessions (mostly text output). For utility terminals running TUIs (vim, less, alternate-screen apps), replay may produce visual artifacts after reconnect. This is acceptable — the session is still alive and functional, just needs a screen redraw (`Ctrl-L`).

**Metadata:** `attach_result` includes `scrollback_truncated: bool` (buffer wrapped) and `last_seq` so the client knows context.

### UTF-8 and ANSI Boundary Handling

The current Rust PTY manager has careful handling of both UTF-8 multibyte boundaries and incomplete ANSI escape sequences (CSI, OSC, DCS). The Go reader must port both:

```go
func findSafeBoundary(data []byte) int {
    n := len(data)
    if n == 0 {
        return 0
    }

    // Check for incomplete UTF-8 at the end
    for i := n - 1; i >= n-4 && i >= 0; i-- {
        if utf8.RuneStart(data[i]) {
            _, size := utf8.DecodeRune(data[i:])
            if i+size > n {
                n = i // Incomplete UTF-8, split before it
            }
            break
        }
    }

    // Check for incomplete ANSI escape sequences in last 32 bytes
    // (CSI sequences can be up to ~20 bytes)
    searchStart := n - 32
    if searchStart < 0 {
        searchStart = 0
    }
    for i := n - 1; i >= searchStart; i-- {
        if data[i] == 0x1b { // ESC
            // Check if this starts an incomplete sequence
            if !isCompleteEscape(data[i:n]) {
                n = i
            }
            break
        }
    }

    return n
}
```

### State Detection

**For Claude sessions:** Hooks handle state detection. No change needed — hooks still report via unix socket to daemon.

**For Codex sessions:** Move the output-based state detection from Rust to Go. The reader goroutine applies the same heuristics (scan for prompts, approval keywords, etc.) and updates session state directly. This is simpler than the current design since it's all in one process.

### Process Group Management

Each session spawns in its own process group (`SysProcAttr.Setpgid = true`). On kill:

```go
func (s *Session) Kill(sig syscall.Signal) error {
    // Signal the entire process group
    pgid := s.cmd.Process.Pid
    if err := syscall.Kill(-pgid, sig); err != nil {
        return err
    }

    // Wait for exit with timeout, then SIGKILL
    select {
    case <-s.exited:
        return nil
    case <-time.After(10 * time.Second):
        syscall.Kill(-pgid, syscall.SIGKILL)
        <-s.exited
        return nil
    }
}
```

**Gotchas:**
- `Setpgid` must not conflict with PTY library's `SysProcAttr` requirements. `creack/pty` doesn't set its own `SysProcAttr`, so this is safe.
- Children that call `setsid()` can escape the group — acceptable, since we also close the PTY master on teardown, which unblocks the reader and signals the child via SIGHUP.
- Always close PTY master FD after process exit to prevent FD leaks.

---

## Managed Mode: Wrapper Contract

When the daemon spawns a session, the `attn` wrapper runs in **managed mode** (detected via `ATTN_DAEMON_MANAGED=1`). In this mode:

### Wrapper does:
- Generate hooks config with correct session ID
- Exec claude/codex with `--settings` pointing to hooks
- Forward signals to claude (SIGTERM → cleanup hooks)
- Run classifier on Stop hook

### Wrapper does NOT:
- Register session with daemon (daemon already registered it at spawn time)
- Unregister session on exit (daemon detects exit via PTY close)
- Send heartbeats (daemon monitors process directly)
- Start daemon if not running (daemon is the one that started the wrapper)

This eliminates the lifecycle ownership conflict where both daemon and wrapper try to manage session registration.

**Long-term:** The daemon could generate hooks config itself and exec claude directly, eliminating the wrapper for managed sessions. Deferred because the wrapper currently owns flag parsing (`--resume`, `--fork-session`), hook wiring, and classifier invocation — non-trivial to replicate in the daemon.

---

## WebSocket Hub Changes

### Client-Session Subscription Model

Each `wsClient` gains a set of attached sessions:

```go
type wsClient struct {
    // ... existing fields ...

    // PTY subscriptions
    attachedSessions map[string]*pty.Subscriber
    attachMu         sync.Mutex
}
```

When a client sends `attach_session`, the hub:
1. Creates a `Subscriber` whose `Send` callback writes to the client's outbound queue
2. Registers the subscriber with the PTY session
3. Sends `attach_result` with scrollback data
4. Subsequent `pty_output` events flow through the subscriber callback

When a client disconnects, all its subscriptions are cleaned up. The PTY session keeps running.

### Outbound Message Abstraction

Replace the current raw `[]byte` send channel with a typed message:

```go
type outboundMessage struct {
    kind    messageKind // text or binary (future)
    payload []byte
}
```

All messages go through a single outbound queue per client, preserving strict ordering. This is critical: `attach_result` (JSON) must arrive before the first `pty_output` (JSON), and control events must not interleave with output in unexpected ways.

### Coalescing (Output Batching)

Rather than sending every PTY read as a separate WebSocket frame, use a dual-trigger batcher per subscriber:

- **maxDelay = 12ms** — hard latency cap; flush no matter what after 12ms
- **idleDelay = 2ms** — if no new data arrives for 2ms, flush immediately (fast response after bursts)
- **sizeLimit = 8KB** — flush when buffered data exceeds 8KB (prevents large chunks from waiting)
- **Immediate flush** on session exit or client detach

This gives ~83fps max frame rate during bulk streaming and near-instant response during interactive typing, without prompt-detection heuristics.

### Backpressure and Desync Protocol

Each subscriber has a bounded output buffer. If a client falls behind:

1. Daemon detects buffer full (subscriber channel blocked)
2. Daemon sends a `pty_desync` event to the client with `reason: "buffer_overflow"`
3. Daemon drops subsequent output for this subscriber until re-attach
4. Client receives `pty_desync`, calls `terminal.reset()`, sends `attach_session` to re-sync from scrollback

This is a hard protocol contract. Partial output (with corrupted ANSI state) is worse than a clean reset + replay.

### Message Routing for `pty_input`

`pty_input` now goes through the same ordered WebSocket command path as `attach_session`, `pty_resize`, and `kill_session`. This avoids edge-case command reordering (for example, an input arriving after a kill/resize sent earlier by the client).

---

## Client Changes

### Removal of Rust PTY Manager

`app/src-tauri/src/pty_manager.rs` (1675 lines) gets removed entirely. The Tauri commands `pty_spawn`, `pty_write`, `pty_resize`, `pty_kill` are replaced by WebSocket messages.

### New Session Flow

```
User clicks "New Session"
  → LocationPicker dialog
  → sendSpawnSession({ id, cwd, agent, cols, rows })   [WebSocket]
  → daemon: spawn PTY + process
  → spawn_result event
  → sendAttachSession({ id })                           [WebSocket]
  → attach_result event + scrollback
  → terminal.write(scrollback)
  → pty_output events stream in → terminal.write(data)
```

### Terminal Component Changes

`Terminal.tsx` currently doesn't know about WebSocket — it gets data from PTY events via the session store. The change:

- **Input:** `terminal.onData()` → `sendPtyInput(id, data)` (WebSocket command)
- **Output:** `pty_output` WebSocket event → `terminal.write(data)` (new handler in `useDaemonSocket.ts`)
- **Resize:** `onResize` → `sendPtyResize(id, cols, rows)` (WebSocket command)
- **Reconnect:** On WebSocket reconnect → `sendAttachSession(id)` for each active terminal → replay scrollback
- **Desync:** On `pty_desync` event → `terminal.reset()` + `sendAttachSession(id)` → replay from scrollback

### Reconnection Logic

When the WebSocket connection drops and re-establishes:

1. Client sends `attach_session` for every session it was viewing
2. Daemon responds with `attach_result` containing scrollback
3. Client calls `terminal.reset()` + `terminal.write(scrollback)` to restore state
4. Output stream resumes from current `seq`

This gives session persistence for free. Even if the Tauri app crashes and restarts, sessions are still running in the daemon.

### Utility Terminals

The `UtilityTerminalPanel` currently spawns plain shell PTYs via Tauri. These also move to the daemon:

- `spawn_session` with `agent: "shell"`
- Same attach/detach/input/resize flow
- Utility terminal sessions are marked differently in the store (not shown in dashboard)
- Scrollback replay for utility terminals is best-effort (TUI apps may need `Ctrl-L` after reconnect)

---

## Session Lifecycle Changes

### Daemon-Owned Sessions

The daemon becomes the source of truth for session lifecycle:

| Action | Before | After |
|--------|--------|-------|
| Create session | Client creates in Zustand, then spawns PTY, then registers with daemon | Client sends `spawn_session` to daemon, daemon creates everything |
| View session | Client has PTY handle + xterm instance | Client sends `attach_session`, daemon streams output |
| Close session | Client calls `pty_kill`, sends `unregister` | Client sends `unregister` (hard-stop) and daemon terminates PTY/process + removes session |
| App restart | Sessions die | Client reconnects, re-attaches to running sessions |
| Session exit | PTY exit event via Tauri IPC | `session_exited` event via WebSocket |

### Session State Transitions

```
                    spawn_session
                         │
                         ▼
                    ┌──────────┐
           │       │  running  │
           │       └──────────┘
           │            │
      attach_session    │ process exits / kill_session / unregister
           │            ▼
           ▼       ┌──────────┐
     streaming     │  exited  │
     pty_output    └──────────┘
           │            │
           └────────────▼
                 ┌──────────┐
                 │ cleaned  │ (resources freed)
                 └──────────┘
```

After exit, the daemon emits `session_exited` and then removes the PTY session from the manager to avoid unbounded accumulation of exited PTY state in long-running daemon lifetimes.

### Hooks Integration

The `attn` wrapper still generates hooks and runs `claude` with `--settings`. The hooks still report state via unix socket. The difference:

- **Before:** Wrapper runs in Tauri-managed PTY. Hooks → unix socket → daemon → WebSocket → client.
- **After:** Wrapper runs in daemon-managed PTY. Hooks → unix socket → daemon (same process). No change to hook mechanism, but the round-trip is shorter since the daemon is the direct PTY owner.

---

## Daemon Restart Hardening

With daemon-owned PTYs, daemon restart = session loss. We must make restarts extremely rare and never automatic.

### Changes required:

1. **Stop killing prior daemon on startup.** Currently `internal/daemon/daemon.go` sends SIGTERM/SIGKILL to existing daemon. Instead: detect existing daemon via PID file or socket probe. If alive, refuse to start (or print instructions). Two daemons must never run simultaneously.

2. **Stop frontend auto-restart.** Currently `app/src/hooks/useDaemonSocket.ts` auto-restarts daemon on WS failure. Instead: show "daemon disconnected" banner. Let the user restart manually or wait for daemon to come back. Auto-restart is only safe for the initial start (no existing sessions).

3. **Protocol version evolution.** Currently protocol version mismatch forces daemon restart. Instead: support backwards-compatible evolution. New fields are optional. Unknown commands get a generic error response. Only truly breaking changes (removed commands, changed semantics) require a version bump and daemon restart.

4. **Graceful daemon upgrade path.** When a new daemon binary is installed, do NOT auto-restart. Show a notification: "New daemon version available. Restart when ready (N active sessions will be lost)." User chooses when to restart.

### Future: Daemon Restart Survival (not in scope)

True restart survival requires a persistent PTY owner outside the daemon. The cleanest model is a thin **shim process** (one per session):

- Each shim owns one PTY + child process
- Daemon communicates with shims via per-session unix sockets (`~/.attn/shims/<id>.sock`)
- Shim protocol: `write`, `resize`, `signal`, `snapshot`, `subscribe_output`
- If daemon dies, shims keep running. New daemon discovers them and reconnects.

The current design (daemon directly owns PTY) is a **clean evolution path** to this model, provided PTY internals stay private to `internal/pty` and the daemon uses methods only. The shim would implement the same `Session` interface, and the daemon wouldn't know the difference.

**Alternative considered:** FD passing (`SCM_RIGHTS`) to transfer PTY file descriptors to a successor daemon. Technically feasible for graceful restarts but not for crash recovery, and requires transferring all runtime state (seq counters, scrollback, UTF-8 carryover). Shim model is cleaner.

---

## Security

The daemon currently accepts all WebSocket origins (`internal/daemon/websocket.go`). Existing commands (git operations, PR actions, settings changes) are already exposed without auth. `spawn_session` and `pty_input` increase the blast radius significantly.

### Phase 1 (with this migration):

- **Strict origin checking.** Reject WebSocket connections from origins other than the Tauri app.
- **Localhost binding.** WebSocket server binds to `127.0.0.1` only (already the case but should be enforced).

### Phase 1.5 (before remote access):

- **Per-user bearer token.** Daemon generates a random token on startup, writes to `~/.attn/auth-token`. Client reads it and sends in WebSocket upgrade request. Any local process can read the file, but this prevents drive-by attacks from web pages.
- **TLS.** Required for remote access. Self-signed cert or Let's Encrypt for LAN.

---

## Logging Safeguards

The daemon currently logs raw WebSocket payloads (`internal/daemon/websocket.go`). With PTY traffic, this would explode log size and CPU.

- **Never log `pty_input` or `pty_output` data payloads.** Log the command type and session ID only.
- **Add a `trace` log level** for PTY data debugging, disabled by default.
- **Log lifecycle events normally:** spawn, attach, detach, resize, exit, desync.

---

## Performance Analysis

### Latency Budget

Interactive typing requires <50ms round-trip to feel responsive. Let's trace the path:

**Before (Tauri IPC):**
```
Keystroke → xterm.js onData → Tauri invoke (pty_write) → Rust → PTY write
  ≈ 0.1ms total
```

**After (WebSocket, local):**
```
Keystroke → xterm.js onData → WebSocket send → Go daemon → PTY write
  ≈ 0.5-1ms total (WebSocket frame + Go goroutine scheduling)
```

**After (WebSocket, remote, future):**
```
Keystroke → xterm.js onData → WebSocket send → network → Go daemon → PTY write
  ≈ RTT/2 + 0.5ms (e.g., 15ms on LAN, 50ms cross-region)
```

Local WebSocket adds negligible latency. Remote adds network RTT but this is inherent and acceptable — SSH works the same way.

### Throughput

Claude streaming large code blocks can produce 100KB+/s of terminal output.

**JSON + base64 path:** 100KB/s → ~133KB/s on wire (33% base64 overhead) + JSON framing. Well within WebSocket capacity.

Even at peak output rates, this is trivial bandwidth for local use. For remote use, binary frames can be added later to eliminate the overhead.

### Coalescing Impact

With dual-trigger batching (12ms max, 2ms idle, 8KB size):
- Frames per second: max ~83 during bulk streaming, near-instant for interactive
- Perceptual difference: none (>60fps)
- CPU savings: significant (fewer WebSocket frames = fewer syscalls)

### Memory

Per session:
- Scrollback buffer: 1MB (configurable)
- PTY file descriptors: 2 (master + slave)
- Reader goroutine: ~8KB stack
- Coalescing buffer: ~8KB per attached client
- Subscriber state: negligible

At 20 concurrent sessions: ~22MB total. Negligible.

---

## Implementation Plan

### Phase 1: Go PTY Manager (behind feature flag) [x DONE]

Create `internal/pty/` package:

1. `manager.go` — Session lifecycle (spawn, kill, list), `SpawnOptions` struct with all fields
2. `session.go` — Reader goroutine, subscriber fan-out, scrollback, coalescing
3. `ringbuffer.go` — Scrollback ring buffer
4. `boundary.go` — UTF-8 and ANSI escape boundary detection (port from Rust)
5. `coalescer.go` — Dual-trigger output batching

Test with standalone harness using a test helper binary (not real claude). Test invariants:
- Byte order preserved across chunks
- `seq` monotonically increasing with no gaps
- No UTF-8 or ANSI corruption at chunk boundaries
- Kill is idempotent, resources cleaned up
- Process group kill prevents subprocess leaks

### Phase 2: Managed Mode + Protocol Extensions [x DONE]

1. Add `ATTN_DAEMON_MANAGED=1` support to wrapper (skip register/unregister/heartbeat)
2. Add new commands and events to TypeSpec schema
3. Generate Go + TypeScript types
4. Add command constants and parse cases
5. Add `outboundMessage` abstraction to WebSocket hub
6. Increment protocol version

### Phase 3: Daemon Integration (dual-path) [x DONE]

1. Wire `pty.Manager` into `Daemon` struct
2. Add WebSocket handlers for new commands (`spawn_session`, `attach_session`, etc.)
3. Route `pty_input` through ordered WebSocket command handling (no bypass path)
4. Implement coalescing in subscriber output path
5. Handle client disconnect → subscriber cleanup
6. Handle daemon shutdown → SIGTERM all managed processes (process group kill), wait, then SIGKILL
7. Add desync detection and `pty_desync` event
8. Logging safeguards: never log PTY data payloads

### Phase 4: Frontend Migration (dual-path with runtime switch) [x DONE]

1. Add new WebSocket commands to `useDaemonSocket.ts`
2. Add `pty_output` handler (base64 decode → terminal.write)
3. Add `pty_desync` handler (terminal.reset + re-attach)
4. Modify session store: add WebSocket-based PTY path alongside existing Tauri PTY path
5. Runtime switch: feature flag or daemon capability check to pick path
6. Modify `Terminal.tsx`: wire input/output through chosen path
7. Add reconnection logic: re-attach on WebSocket reconnect
8. Add scrollback replay on attach
9. **Soak test both paths** before removing old path

### Phase 5: Daemon Restart Hardening [~ PARTIAL]

1. Stop killing prior daemon on startup — refuse to start if existing daemon is alive
2. Stop frontend auto-restart — show "disconnected" banner instead
3. Add protocol backwards-compatibility (unknown commands → error response, optional fields)
4. Add "daemon upgrade available" notification

### Phase 6: Remove Rust PTY Manager [x DONE]

Only after Phase 4 soak testing confirms the WebSocket path is stable:

1. Remove `pty_manager.rs` and Tauri commands
2. Remove `portable-pty` dependency
3. Remove PTY-related Tauri event handling
4. Remove feature flag / runtime switch (WebSocket path becomes the only path)
5. Update `lib.rs` to remove PTY command registrations

### Phase 7: Codex State Detection [x DONE]

1. Port output-based state detection from Rust to Go
2. Integrate into reader goroutine
3. Port transcript matching logic if still needed

### Phase 8: Shim-Based Restart Survival [ ] PLANNED

Goal: keep sessions alive across daemon restart by moving PTY ownership to per-session shim processes.

#### 8.1 Shim runtime + IPC (foundation)
1. Add `attn shim` subcommand (new process type) that owns one PTY + one child process.
2. Shim listens on per-session Unix socket: `~/.attn/shims/<session-id>.sock`.
3. Shim persists minimal metadata file: `~/.attn/shims/<session-id>.json` (session id, cwd, agent, pid, started_at, protocol version).
4. Define shim protocol (JSON over Unix socket):
   - `hello` (version handshake)
   - `write`, `resize`, `signal`
   - `snapshot` (scrollback + seq + running/exit metadata)
   - `subscribe_output` / `unsubscribe_output`
5. Move scrollback ring buffer + `seq` counter into shim so replay survives daemon restart.

#### 8.2 Daemon shim backend (integration)
1. Keep `internal/pty` API stable; add a shim-backed implementation behind same manager methods.
2. On daemon start, discover shims from `~/.attn/shims/*.json` and reconnect.
3. Rehydrate daemon runtime state from shim metadata/snapshot:
   - Rebind `spawn_session` ids to existing live sessions when matching
   - Rebuild attachable terminal state (`last_seq`, scrollback, pid, running)
4. Route existing WebSocket PTY commands to shim backend without frontend protocol changes.

#### 8.3 Lifecycle + safety
1. Ownership checks: only same-UID daemon can attach to shim sockets.
2. Heartbeats between daemon and shim for stale-daemon detection.
3. Orphan cleanup rules:
   - If shim child exits and no subscribers for TTL, shim self-terminates and removes socket/metadata.
   - If metadata exists but process is dead, daemon prunes stale files.
4. Graceful stop semantics:
   - Normal daemon stop should not stop shims.
   - Explicit session kill should signal shim which then signals PTY process group.

#### 8.4 Versioning + compatibility
1. Add shim protocol version constant and handshake validation.
2. Reject incompatible daemon↔shim pairs with clear error telemetry.
3. Keep shim protocol additive-first (new optional fields, no required removals).

#### 8.5 Rollout plan
1. Feature-flagged rollout (`ATTN_PTY_BACKEND=direct|shim`), default `direct`.
2. Internal soak with `shim` backend enabled.
3. Promote `shim` to default after soak; keep `direct` fallback for one release window.

#### 8.6 Testing plan for restart survival
1. Unit tests (shim package): protocol, snapshot correctness, cleanup behavior.
2. Integration tests (daemon): start shim-backed session, kill daemon process, start new daemon, re-attach, verify terminal continuity.
3. E2E test: spawn session, emit output, restart daemon, confirm output replay + live input still works.

#### Estimated effort
1. Planned-restart survival (no crash-recovery guarantees): ~3-5 days.
2. Robust restart/crash survival + cleanup hardening: ~8-14 days.

---

## Testing Strategy

### Layer 1: PTY Manager Unit Tests (Go)

Test `internal/pty/` in isolation using a test helper binary (a simple Go program that echoes input, responds to resize, etc.):

- **Spawn + read:** Spawn helper, verify banner output arrives with correct seq
- **Write + echo:** Send input, verify echoed output
- **Resize:** Send resize, verify helper detects new terminal size (via SIGWINCH)
- **Kill:** Kill session, verify `exited` channel closes, exit code captured
- **Kill idempotent:** Kill already-exited session, no panic
- **UTF-8 boundary:** Feed multi-byte characters at chunk boundaries, verify no corruption
- **ANSI boundary:** Feed incomplete escape sequences at boundaries, verify carryover
- **Scrollback:** Write >1MB, verify ring buffer wraps correctly
- **Subscriber fan-out:** Multiple subscribers, verify all receive same data
- **Coalescing:** Verify batching respects max delay, idle delay, and size limit

### Layer 2: Daemon WebSocket Integration Tests (Go)

Extend existing `internal/daemon/daemon_test.go` harness:

- **Full lifecycle:** `spawn_session` → `spawn_result` → `attach_session` → `attach_result` + scrollback → `pty_input` → `pty_output` → `pty_resize` → `kill_session` → `session_exited`
- **Reconnect:** Spawn, attach, disconnect WebSocket, reconnect, re-attach, verify scrollback replay
- **Multi-client:** Two clients attach same session, both receive output, both can type
- **Desync:** Simulate slow client, verify `pty_desync` sent, re-attach recovers
- **Managed mode:** Verify wrapper doesn't double-register when `ATTN_DAEMON_MANAGED=1`

Use a mock/test command, not real claude.

### Layer 3: Frontend Unit Tests (Vitest)

Extend `MockDaemonProvider` with PTY commands:

- `spawnSession`, `attachSession`, `detachSession`, `ptyInput`, `ptyResize`, `killSession`
- Mock `pty_output` event emitter for pushing deterministic output
- Mock `pty_desync` event for testing recovery flow
- Verify terminal.write calls, reconnect behavior, desync handling

### Layer 4: E2E Tests (Playwright) — Minimal

A few true end-to-end tests through daemon → PTY → WebSocket → xterm:

- Output appears in terminal after spawn + attach
- Input round-trips (type command, see output)
- Reconnect replays scrollback (refresh page, session still shows output)
- Session persists across UI refresh

Use existing `app/e2e/fixtures.ts` test controls. Keep these tests minimal — most behavior should be covered by layers 1-3.

---

## Open Questions

1. **Multi-client terminal access.** If two clients attach to the same session, should both be able to type? Current thinking: yes, same as tmux shared sessions. The terminal is a shared resource. If this causes issues, we can add an "observer" mode later.

2. **Scrollback size.** 1MB is a starting point. Should this be configurable per session? Should older sessions (idle for hours) have their scrollback trimmed to save memory?

3. **Shell environment.** The Rust PTY manager uses `dscl` to find the user's login shell and spawns via login shell with `-l`. The Go equivalent needs to replicate this (e.g., `user.Current()` + `os/user` or reading `/etc/passwd`).

4. **Graceful shutdown ordering.** On daemon shutdown: send SIGTERM to all managed processes (via process group), wait up to 10s for graceful exit, then SIGKILL. Claude Code uses SIGTERM to run cleanup hooks — this must be preserved.

5. **Terminal recording/replay.** With the daemon holding all output, we could record entire sessions for replay. Out of scope but the scrollback buffer is a natural starting point.

---

## Potential Next Steps (After Real Usage)

These are intentionally deferred until there is real usage feedback:

1. **Upgrade UX policy.**
   - Option A: strictly manual daemon restart only.
   - Option B: one-click restart with explicit warning when active sessions exist.

2. **Upgrade notification timing.**
   - Option A: check only on app launch.
   - Option B: periodic background checks while app is open.

3. **In-app restart affordance.**
   - Add a visible `Restart daemon` action with confirmation dialog and active session count.
   - Keep action disabled or strongly warned when critical sessions are running.

4. **Session-protection guardrails.**
   - Require explicit confirmation text for restart when `N > 0` active sessions.
   - Optional “remind me later” snooze state for upgrade prompts.

5. **Post-soak hardening.**
   - Gather reconnect/desync telemetry and decide whether coalescing/backpressure thresholds need tuning.

---

## What This Enables (Future)

With terminal I/O flowing over WebSocket:

- **Remote daemon:** Run the daemon on a beefy dev server, connect from a laptop. All compute happens remotely. Just needs WebSocket over the network (add TLS + auth).
- **Web client:** Replace Tauri with a plain web app. xterm.js + WebSocket = terminal in the browser. No native app needed.
- **Session persistence:** Laptop sleeps, sessions keep running. Open laptop, reconnect, pick up where you left off.
- **Mobile monitoring:** A lightweight mobile client could show session states and scrollback without managing any processes.
- **Session sharing:** Multiple people can attach to the same session for pair programming or review.

---

## Review Notes

This plan was reviewed by Codex (gpt-5.3-codex) across three rounds. Key improvements incorporated from review:

1. **Lifecycle ownership consistency:** Added managed mode (`ATTN_DAEMON_MANAGED=1`) to prevent wrapper and daemon from fighting over session registration.
2. **Daemon restart hardening:** Added explicit section on making restarts rare and never automatic. Documented future shim model for true restart survival.
3. **Deferred binary frames:** Switched from hybrid binary+JSON to JSON-only with `outboundMessage` abstraction for future binary support. Reduces initial complexity.
4. **Dual-trigger coalescing:** Replaced fixed 16ms timer with adaptive batching (12ms max, 2ms idle, 8KB size).
5. **Process group management:** Added `Setpgid` + negative PID kill for clean subprocess shutdown.
6. **Explicit desync protocol:** Added `pty_desync` event with mandatory client re-attach, instead of vague "resync marker."
7. **ANSI boundary handling:** Expanded from UTF-8-only to include ANSI escape sequence boundaries.
8. **Missing spawn fields:** Added `fork_session`, `claude_executable`, `codex_executable` to `spawn_session` payload.
9. **Logging safeguards:** Added section on never logging PTY data payloads.
10. **Testing strategy:** Added four-layer testing plan with specific invariants per layer.
11. **Dual-path migration:** Added feature flag and runtime switch for gradual rollout with soak testing before removing Rust PTY manager.
12. **Security:** Added origin checking and bearer token plan.

---

## Update Log

- 2026-02-07 11:48 UTC — Moved `demonize.md` from repo root to `docs/plans/demonize.md`.
- 2026-02-07 11:53 UTC — Added protocol schema entries for PTY session spawn/attach/detach/input/resize/kill and PTY events.
- 2026-02-07 11:58 UTC — Implemented `internal/pty` manager, session lifecycle, ring scrollback, and UTF-8/ANSI-safe boundary handling.
- 2026-02-07 12:00 UTC — Integrated daemon PTY manager into WebSocket handlers and daemon lifecycle; added PTY exit event broadcasting.
- 2026-02-07 12:01 UTC — Added managed wrapper mode (`ATTN_DAEMON_MANAGED=1`) and daemon PID lock behavior to refuse replacing a running daemon.
- 2026-02-07 12:02 UTC — Migrated frontend PTY bridge to WebSocket backend and removed startup session clearing to preserve daemon sessions.
- 2026-02-07 12:03 UTC — Removed Rust PTY manager (`app/src-tauri/src/pty_manager.rs`), command registrations, and PTY-only Rust deps.
- 2026-02-07 12:04 UTC — Added PTY unit tests and verified Go tests, TypeScript checks, frontend tests, and Tauri `cargo check`.
- 2026-02-07 12:30 UTC — Added codex output-based state detection in Go PTY reader path and broadcasted session state updates from daemon.
- 2026-02-07 12:30 UTC — Added `command_error` WebSocket event for unknown/invalid commands and improved mismatch upgrade messaging with active-session warning.
- 2026-02-07 12:35 UTC — Added deferred “Potential Next Steps” section for post-usage upgrade/restart UX decisions.
- 2026-02-07 12:37 UTC — Added detailed Phase 8 shim-based restart-survival implementation plan (IPC, discovery, lifecycle, rollout, and testing).
- 2026-02-07 13:09 UTC — Marked `unregister` hard-stop implementation as complete and documented `detach_session` as the detach-only path.
- 2026-02-07 13:09 UTC — Updated PTY input routing notes to reflect ordered WebSocket processing (fast-path bypass removed).
- 2026-02-07 13:09 UTC — Documented frontend PTY output buffering and readiness race fixes for main and utility terminals.
- 2026-02-07 13:09 UTC — Recorded strict protocol schema coverage expansion and regenerated Go/TypeScript types.
- 2026-02-07 13:09 UTC — Recorded Tauri daemon socket path fix (`~/.attn/attn.sock`) to match daemon defaults.
- 2026-02-07 13:10 UTC — Updated lifecycle table to reflect current close-session behavior (`unregister` as hard-stop).
- 2026-02-07 13:10 UTC — Updated session lifecycle diagram to match current exit cleanup semantics (no post-exit reattach).
- 2026-02-07 13:44 UTC — Hardened E2E daemon binary selection (`ATTN_E2E_BIN` + repo-local fallback) and updated CI to run E2E against the just-built `/tmp/attn`.
- 2026-02-07 13:44 UTC — Fixed WebSocket origin handling in daemon to accept Playwright localhost origin format and host-pattern authorization.
- 2026-02-07 13:44 UTC — Fixed worktree-close prompt regression by preserving local `isWorktree`/`branch` metadata during session enrichment.
