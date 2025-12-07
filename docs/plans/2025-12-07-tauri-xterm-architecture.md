# Tauri + xterm.js Architecture

**Date:** 2025-12-07
**Status:** Design approved

## Goal

Replace the bubbletea TUI dashboard with a native macOS app that embeds terminals using xterm.js. This enables:
1. Full terminal emulation without Go VT100 limitations
2. Side-by-side dashboard + terminal view
3. Multiple Claude sessions in tabs
4. No tmux dependency for session management

## Architecture Overview

```
┌─────────────────────────────────────────────────┐
│ attn.app (Tauri)                                │
│ ┌─────────────────────────────────────────────┐ │
│ │ React + xterm.js                            │ │
│ │ ┌─────────────┬───────────────────────────┐ │ │
│ │ │ Sidebar     │ Terminal Pane (xterm.js)  │ │ │
│ │ │ - Sessions  │ ┌───────────────────────┐ │ │ │
│ │ │   - state   │ │ $ claude              │ │ │ │
│ │ │   - todos   │ │ > Working on...       │ │ │ │
│ │ │ - PRs       │ │                       │ │ │ │
│ │ │ - Repos     │ └───────────────────────┘ │ │ │
│ │ └─────────────┴───────────────────────────┘ │ │
│ └─────────────────────────────────────────────┘ │
│                    │ Tauri IPC                  │
│ ┌──────────────────┴──────────────────────────┐ │
│ │ Rust Backend                                │ │
│ │ - PTY spawning (portable-pty)              │ │
│ │ - Session lifecycle management              │ │
│ │ - Bridge PTY I/O to frontend               │ │
│ └─────────────────────────────────────────────┘ │
└───────────────────────┬─────────────────────────┘
                        │ WebSocket
          ┌─────────────┴─────────────┐
          ▼                           ▼
┌───────────────────┐       ┌───────────────────┐
│ Go Daemon         │       │ attn CLI wrapper  │
│ (add WebSocket)   │       │                   │
│                   │       │ - Focus app       │
│ - Session state   │       │ - Spawn session   │
│ - Todo lists      │       │ - Pass cwd/label  │
│ - GitHub PRs      │       │                   │
│ - Repo states     │       │                   │
└───────────────────┘       └───────────────────┘
```

## Key Design Decisions

### 1. No tmux dependency
- Tauri/Rust spawns Claude sessions directly via PTY
- No external multiplexer required
- Sessions managed entirely by the app

### 2. WebSocket for daemon communication
- Go daemon adds WebSocket endpoint
- Bidirectional: daemon pushes state changes in real-time
- No polling required
- Keeps Unix socket for backwards compatibility (cm binary)

### 3. Hooks system unchanged
- Claude sessions still use hooks to report state
- Hooks write to Go daemon via Unix socket (existing mechanism)
- Daemon pushes updates to Tauri app via WebSocket

### 4. macOS only (initially)
- Simplifies PTY handling (Unix PTY, no Windows ConPTY)
- macOS-specific IPC for CLI wrapper
- Linux support can be added later

### 5. GUI replaces TUI entirely
- `attn` binary becomes the GUI app
- No `--tui` mode
- CLI wrapper for spawning sessions from terminal

## Component Details

### Go Daemon Changes

Add WebSocket server alongside existing Unix socket:

```go
// internal/daemon/websocket.go

// WebSocket endpoint: ws://localhost:9849/ws
// Port configurable via ATTN_WS_PORT env var

// Message format: same JSON as current protocol
// Additional push events:
// - {"event": "session_state_changed", "session": {...}}
// - {"event": "session_registered", "session": {...}}
// - {"event": "session_unregistered", "id": "..."}
// - {"event": "todos_updated", "id": "...", "todos": [...]}
// - {"event": "pr_updated", "pr": {...}}
```

Changes to daemon:
1. Add `internal/daemon/websocket.go` with WebSocket handler
2. Broadcast state changes to all connected WebSocket clients
3. Keep Unix socket handlers unchanged

### Rust/Tauri Backend

Responsibilities:
- **PTY management**: Spawn `claude` processes via `portable-pty` crate
- **Session lifecycle**: Create, resize, destroy sessions
- **IPC bridge**: Stream PTY data to/from frontend xterm.js instances
- **Window management**: Handle focus requests from CLI wrapper

Key Tauri commands:
```rust
#[tauri::command]
fn spawn_session(cwd: String, label: Option<String>) -> SessionId

#[tauri::command]
fn kill_session(id: SessionId)

#[tauri::command]
fn resize_session(id: SessionId, cols: u16, rows: u16)

#[tauri::command]
fn write_to_session(id: SessionId, data: Vec<u8>)

// PTY output streamed via Tauri events
// event: "pty_output", payload: {id, data}
```

### React Frontend

Structure:
```
src/
├── App.tsx              # Main layout
├── components/
│   ├── Sidebar/
│   │   ├── SessionList.tsx   # Sessions with state/todos
│   │   ├── PRList.tsx        # GitHub PRs
│   │   └── RepoList.tsx      # Repo groups
│   ├── Terminal/
│   │   ├── TerminalPane.tsx  # xterm.js wrapper
│   │   └── TerminalTabs.tsx  # Tab management
│   └── StatusBar.tsx         # Bottom status
├── hooks/
│   ├── useDaemonSocket.ts    # WebSocket to Go daemon
│   └── usePty.ts             # Tauri PTY commands
└── store/
    └── sessions.ts           # Session state management
```

xterm.js integration:
- One xterm.js instance per session tab
- Attach to Tauri PTY events for output
- Send keystrokes via Tauri command

### CLI Wrapper

```bash
attn                    # Focus app, spawn new session in $PWD
attn -s <label>         # Focus app, spawn session with label
attn -d                 # Just open/focus dashboard (no new session)
attn status             # Print tmux-style status line (for scripts)
```

Implementation (macOS):
```bash
# Focus app
open -a "attn"

# Send spawn request via URL scheme
open "attn://spawn?cwd=/path/to/dir&label=my-session"
```

Tauri registers URL scheme handler to receive spawn requests.

## Data Flow

### Session State Updates

```
1. Claude session runs in PTY (managed by Tauri/Rust)
2. Hook fires (e.g., state: working → waiting)
3. Hook writes to Go daemon via Unix socket
4. Daemon updates internal state
5. Daemon broadcasts via WebSocket: {"event": "session_state_changed", ...}
6. React frontend receives, updates sidebar
```

### Spawning a Session

```
1. User clicks "New Session" in app (or runs `attn` CLI)
2. React calls Tauri command: spawn_session(cwd, label)
3. Rust spawns PTY with: claude --settings <hooks-config>
4. Rust registers session with Go daemon (existing register flow)
5. Rust streams PTY output to frontend via events
6. xterm.js renders terminal output
```

## Implementation Phases

### Phase 1: Scaffold
- [ ] Create Tauri + React project in `app/` directory
- [ ] Basic window with hardcoded layout
- [ ] xterm.js rendering a simple shell (`/bin/zsh`)
- [ ] Rust PTY spawning with `portable-pty`
- [ ] Bidirectional I/O working

### Phase 2: Go Daemon WebSocket
- [ ] Add WebSocket endpoint to Go daemon
- [ ] Broadcast session state changes
- [ ] Broadcast PR updates
- [ ] Frontend connects and renders real data in sidebar

### Phase 3: Claude Integration
- [ ] Spawn `claude` instead of shell
- [ ] Generate hooks config (reuse existing Go code or port to Rust)
- [ ] Hooks report state back to daemon
- [ ] Session state/todos reflected in sidebar

### Phase 4: CLI Wrapper
- [ ] URL scheme registration in Tauri
- [ ] `attn` shell script/binary
- [ ] Spawn session with cwd/label from CLI
- [ ] Focus existing window

### Phase 5: Polish
- [ ] Multiple session tabs
- [ ] Session switching
- [ ] PR actions (approve, merge) from sidebar
- [ ] Keyboard shortcuts
- [ ] Visual polish

## File Structure

```
claude-manager/
├── cmd/cm/              # Existing CLI (unchanged)
├── internal/            # Existing Go packages
│   └── daemon/
│       └── websocket.go # NEW: WebSocket endpoint
├── app/                 # NEW: Tauri application
│   ├── src-tauri/       # Rust backend
│   │   ├── src/
│   │   │   ├── main.rs
│   │   │   ├── pty.rs   # PTY management
│   │   │   └── commands.rs
│   │   └── Cargo.toml
│   ├── src/             # React frontend
│   │   ├── App.tsx
│   │   └── ...
│   ├── package.json
│   └── tauri.conf.json
└── Makefile             # Add: build-app, install-app
```

## Dependencies

### Rust (Tauri backend)
- `tauri` - Application framework
- `portable-pty` - Cross-platform PTY
- `serde` / `serde_json` - Serialization

### JavaScript (React frontend)
- `react` - UI framework
- `xterm` - Terminal emulator
- `xterm-addon-fit` - Auto-resize
- `xterm-addon-webgl` - GPU rendering (optional)

### Go (Daemon)
- `gorilla/websocket` or `nhooyr.io/websocket` - WebSocket server

## Open Questions

1. **Hooks config generation**: Port to Rust or call Go binary?
   - Recommendation: Generate in Rust to avoid subprocess

2. **Session persistence**: What happens when app closes?
   - Options: Warn user, detach sessions (complex), or just kill them
   - Recommendation: Kill sessions on close (simple), add detach later if needed

3. **Multiple windows**: Support multiple app windows?
   - Recommendation: Single window with tabs (simpler)

## References

- [Tauri documentation](https://tauri.app/v1/guides/)
- [xterm.js documentation](https://xtermjs.org/docs/)
- [portable-pty crate](https://docs.rs/portable-pty/)
- [VS Code terminal architecture](https://code.visualstudio.com/docs/terminal/advanced)
