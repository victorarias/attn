# Node.js PTY Sidecar Design

**Date:** 2025-12-09
**Status:** Approved
**Problem:** tauri-pty's polling-based IPC with fixed 1024-byte buffer causes terminal rendering issues (extra line breaks, flicker) that VS Code's node-pty doesn't have.

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                    Tauri App                            │
│  ┌─────────────┐      ┌─────────────────────────────┐  │
│  │  Frontend   │      │      Rust Backend           │  │
│  │  (React)    │◄────►│  - Spawns sidecar           │  │
│  │  - xterm.js │      │  - Routes socket messages   │  │
│  │  - Terminal │      │  - Manages lifecycle        │  │
│  └─────────────┘      └──────────────┬──────────────┘  │
└──────────────────────────────────────┼──────────────────┘
                                       │ Unix Socket
                                       │ ~/.cm-pty.sock
                                       ▼
                          ┌────────────────────────┐
                          │   Node.js Sidecar      │
                          │   (pty-server)         │
                          │   - node-pty           │
                          │   - Multi-session      │
                          │   - Length-prefixed    │
                          │     binary frames      │
                          └────────────────────────┘
```

### Key Components

- **pty-server**: Node.js binary bundled with app, manages all PTY sessions
- **Rust backend**: Spawns sidecar, bridges frontend ↔ socket
- **Frontend**: Same xterm.js, sends/receives via Tauri commands instead of tauri-pty

### Message Flow

1. Frontend calls `spawn_pty(cwd, cols, rows)` → Rust → Socket → pty-server
2. pty-server creates node-pty instance, returns session ID
3. PTY output streams back: pty-server → Socket → Rust → Frontend event
4. User input: Frontend → Rust → Socket → pty-server → PTY

## Protocol Design

### Frame Format (length-prefixed binary)

```
┌──────────────┬─────────────────────────────────┐
│ 4 bytes      │ N bytes                         │
│ (length N)   │ (JSON message)                  │
└──────────────┴─────────────────────────────────┘
```

### Message Types

```typescript
// Frontend → pty-server
{ cmd: "spawn", id: string, cwd: string, cols: number, rows: number }
{ cmd: "write", id: string, data: string }
{ cmd: "resize", id: string, cols: number, rows: number }
{ cmd: "kill", id: string }

// pty-server → Frontend
{ event: "spawned", id: string, pid: number }
{ event: "data", id: string, data: string }  // base64 for binary safety
{ event: "exit", id: string, code: number }
{ event: "error", id: string, error: string }
```

### Why base64 for data?

- PTY output can contain any byte sequence
- JSON can't safely encode raw binary
- Overhead is acceptable (~33%) for correctness

### Session Lifecycle

1. `spawn` → server creates node-pty, stores in Map by ID
2. node-pty `onData` → server sends `data` events
3. `write`/`resize` → server routes to correct session
4. `kill` or PTY exit → server cleans up, sends `exit`

## File Structure

```
app/
├── pty-server/                    # Node.js sidecar project
│   ├── package.json               # node-pty dependency
│   ├── src/
│   │   └── index.ts               # Server implementation
│   └── build.sh                   # Bundle with pkg
│
├── src-tauri/
│   ├── src/
│   │   ├── lib.rs                 # Add pty_bridge module
│   │   └── pty_bridge.rs          # Socket client, Tauri commands
│   ├── binaries/                  # Bundled sidecar binaries
│   │   └── pty-server-aarch64-apple-darwin
│   └── tauri.conf.json            # Add sidecar config
│
└── src/
    ├── hooks/
    │   └── usePtySidecar.ts       # Replace tauri-pty usage
    └── store/
        └── sessions.ts            # Update to use new hook
```

### Changes to Existing Files

- `sessions.ts`: Replace `spawn()` from tauri-pty with new Tauri commands
- `Terminal.tsx`: No changes needed (still uses same interface)
- Remove `tauri-pty` and `tauri-plugin-pty` dependencies

### Build Process

1. `cd pty-server && npm run build` → produces platform binary
2. Copy binary to `src-tauri/binaries/` with correct suffix
3. Normal `cargo tauri build`

## Development Workflow

### Local Development

1. Run pty-server directly: `cd pty-server && npm run dev`
2. It listens on `~/.cm-pty.sock`
3. Tauri dev mode connects to existing socket (no need to bundle)

### Production

1. Bundle pty-server with `pkg` to single binary
2. Tauri spawns sidecar automatically on app start
3. Sidecar exits when app exits (Tauri manages lifecycle)

## Testing Strategy

1. **Unit test pty-server** standalone with mock PTY
2. **Integration test** the socket protocol with real node-pty
3. **Manual test** with Claude Code at full terminal width

### Verification Criteria

- Run Claude Code at 200+ columns
- Trigger "thinking" animation
- Verify: no extra line breaks, no flicker, resize works

## Rollback Plan

- Keep tauri-pty code commented, not deleted
- If sidecar approach fails, can revert quickly
