# Node.js PTY Sidecar Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace tauri-pty with node-pty sidecar to fix terminal rendering issues at wide widths.

**Architecture:** Node.js server using node-pty communicates via Unix socket with length-prefixed JSON frames. Tauri spawns sidecar, Rust backend bridges socket to frontend events.

**Tech Stack:** Node.js, node-pty, TypeScript, Rust, Tauri sidecar API

---

## Task 1: Create pty-server Node.js Project

**Files:**
- Create: `app/pty-server/package.json`
- Create: `app/pty-server/tsconfig.json`
- Create: `app/pty-server/src/index.ts`

**Step 1: Create package.json**

```json
{
  "name": "pty-server",
  "version": "0.1.0",
  "type": "module",
  "main": "dist/index.js",
  "scripts": {
    "build": "tsc",
    "dev": "tsc && node dist/index.js"
  },
  "dependencies": {
    "node-pty": "^1.0.0"
  },
  "devDependencies": {
    "@types/node": "^22.0.0",
    "typescript": "^5.8.0"
  }
}
```

**Step 2: Create tsconfig.json**

```json
{
  "compilerOptions": {
    "target": "ES2022",
    "module": "NodeNext",
    "moduleResolution": "NodeNext",
    "outDir": "dist",
    "rootDir": "src",
    "strict": true,
    "esModuleInterop": true,
    "skipLibCheck": true
  },
  "include": ["src"]
}
```

**Step 3: Create minimal src/index.ts**

```typescript
import * as net from 'net';
import * as os from 'os';
import * as path from 'path';
import * as pty from 'node-pty';

const SOCKET_PATH = path.join(os.homedir(), '.cm-pty.sock');

interface Session {
  pty: pty.IPty;
}

const sessions = new Map<string, Session>();

// Frame protocol: [4-byte length][JSON payload]
function writeFrame(socket: net.Socket, data: object): void {
  const json = JSON.stringify(data);
  const buf = Buffer.alloc(4 + Buffer.byteLength(json));
  buf.writeUInt32BE(Buffer.byteLength(json), 0);
  buf.write(json, 4);
  socket.write(buf);
}

function handleMessage(socket: net.Socket, msg: any): void {
  switch (msg.cmd) {
    case 'spawn': {
      const shell = process.env.SHELL || '/bin/bash';
      const ptyProcess = pty.spawn(shell, [], {
        name: 'xterm-256color',
        cols: msg.cols || 80,
        rows: msg.rows || 24,
        cwd: msg.cwd || os.homedir(),
        env: process.env as { [key: string]: string },
      });

      sessions.set(msg.id, { pty: ptyProcess });

      ptyProcess.onData((data) => {
        writeFrame(socket, {
          event: 'data',
          id: msg.id,
          data: Buffer.from(data).toString('base64'),
        });
      });

      ptyProcess.onExit(({ exitCode }) => {
        writeFrame(socket, { event: 'exit', id: msg.id, code: exitCode });
        sessions.delete(msg.id);
      });

      writeFrame(socket, { event: 'spawned', id: msg.id, pid: ptyProcess.pid });
      break;
    }

    case 'write': {
      const session = sessions.get(msg.id);
      if (session) {
        session.pty.write(msg.data);
      }
      break;
    }

    case 'resize': {
      const session = sessions.get(msg.id);
      if (session) {
        session.pty.resize(msg.cols, msg.rows);
      }
      break;
    }

    case 'kill': {
      const session = sessions.get(msg.id);
      if (session) {
        session.pty.kill();
        sessions.delete(msg.id);
      }
      break;
    }
  }
}

// Remove stale socket
import * as fs from 'fs';
try { fs.unlinkSync(SOCKET_PATH); } catch {}

const server = net.createServer((socket) => {
  console.log('[pty-server] Client connected');
  let buffer = Buffer.alloc(0);

  socket.on('data', (chunk) => {
    buffer = Buffer.concat([buffer, chunk]);

    while (buffer.length >= 4) {
      const len = buffer.readUInt32BE(0);
      if (buffer.length < 4 + len) break;

      const json = buffer.subarray(4, 4 + len).toString();
      buffer = buffer.subarray(4 + len);

      try {
        const msg = JSON.parse(json);
        handleMessage(socket, msg);
      } catch (e) {
        console.error('[pty-server] Parse error:', e);
      }
    }
  });

  socket.on('close', () => {
    console.log('[pty-server] Client disconnected');
    // Kill all sessions for this socket
    for (const [id, session] of sessions) {
      session.pty.kill();
      sessions.delete(id);
    }
  });
});

server.listen(SOCKET_PATH, () => {
  console.log(`[pty-server] Listening on ${SOCKET_PATH}`);
});
```

**Step 4: Install dependencies and build**

Run:
```bash
cd /Users/victor.arias/projects/claude-manager/app/pty-server
npm install
npm run build
```

Expected: `dist/index.js` created successfully

**Step 5: Test pty-server runs**

Run:
```bash
cd /Users/victor.arias/projects/claude-manager/app/pty-server
node dist/index.js &
sleep 1
ls -la ~/.cm-pty.sock
kill %1
```

Expected: Socket file exists at `~/.cm-pty.sock`

**Step 6: Commit**

```bash
git add app/pty-server
git commit -m "feat(pty-server): add node-pty sidecar server"
```

---

## Task 2: Create Frontend PTY Client

**Files:**
- Create: `app/src/lib/ptyClient.ts`

**Step 1: Create ptyClient.ts**

```typescript
// PTY client that communicates with pty-server via Tauri commands
// For now, direct socket connection for dev; will use Tauri bridge in prod

type DataCallback = (data: string) => void;
type ExitCallback = (code: number) => void;

interface PtySession {
  id: string;
  onData: (cb: DataCallback) => void;
  onExit: (cb: ExitCallback) => void;
  write: (data: string) => void;
  resize: (cols: number, rows: number) => void;
  kill: () => void;
}

interface PtyClient {
  spawn: (cwd: string, cols: number, rows: number) => Promise<PtySession>;
  close: () => void;
}

// Frame protocol helpers
function encodeFrame(data: object): ArrayBuffer {
  const json = JSON.stringify(data);
  const jsonBytes = new TextEncoder().encode(json);
  const buf = new ArrayBuffer(4 + jsonBytes.length);
  const view = new DataView(buf);
  view.setUint32(0, jsonBytes.length, false); // big-endian
  new Uint8Array(buf, 4).set(jsonBytes);
  return buf;
}

export function createPtyClient(socketPath: string): Promise<PtyClient> {
  return new Promise((resolve, reject) => {
    // In browser/Tauri context, we'll use Tauri commands
    // This is a placeholder for the WebSocket-style interface
    // For dev, we'll expose this via a simple HTTP bridge or Tauri command

    const sessions = new Map<string, {
      dataCallbacks: DataCallback[];
      exitCallbacks: ExitCallback[];
    }>();

    let sessionCounter = 0;

    // This will be replaced with actual Tauri invoke calls
    const client: PtyClient = {
      spawn: async (cwd: string, cols: number, rows: number): Promise<PtySession> => {
        const id = `pty-${++sessionCounter}`;

        sessions.set(id, {
          dataCallbacks: [],
          exitCallbacks: [],
        });

        // Will call Tauri command: invoke('pty_spawn', { id, cwd, cols, rows })

        return {
          id,
          onData: (cb) => {
            sessions.get(id)?.dataCallbacks.push(cb);
          },
          onExit: (cb) => {
            sessions.get(id)?.exitCallbacks.push(cb);
          },
          write: (data) => {
            // Will call: invoke('pty_write', { id, data })
          },
          resize: (cols, rows) => {
            // Will call: invoke('pty_resize', { id, cols, rows })
          },
          kill: () => {
            // Will call: invoke('pty_kill', { id })
            sessions.delete(id);
          },
        };
      },
      close: () => {
        sessions.clear();
      },
    };

    resolve(client);
  });
}

// Export types for use elsewhere
export type { PtySession, PtyClient };
```

**Step 2: Commit**

```bash
git add app/src/lib/ptyClient.ts
git commit -m "feat(app): add pty client interface (placeholder)"
```

---

## Task 3: Add Rust PTY Bridge

**Files:**
- Create: `app/src-tauri/src/pty_bridge.rs`
- Modify: `app/src-tauri/src/lib.rs`

**Step 1: Create pty_bridge.rs**

```rust
use std::collections::HashMap;
use std::io::{Read, Write};
use std::os::unix::net::UnixStream;
use std::path::PathBuf;
use std::sync::{Arc, Mutex};
use tauri::{AppHandle, Emitter, Manager, State};

#[derive(Default)]
pub struct PtyState {
    stream: Mutex<Option<UnixStream>>,
    buffer: Mutex<Vec<u8>>,
}

fn socket_path() -> PathBuf {
    dirs::home_dir().unwrap().join(".cm-pty.sock")
}

fn write_frame(stream: &mut UnixStream, data: &serde_json::Value) -> std::io::Result<()> {
    let json = serde_json::to_vec(data)?;
    let len = (json.len() as u32).to_be_bytes();
    stream.write_all(&len)?;
    stream.write_all(&json)?;
    stream.flush()?;
    Ok(())
}

fn read_frame(stream: &mut UnixStream) -> std::io::Result<serde_json::Value> {
    let mut len_buf = [0u8; 4];
    stream.read_exact(&mut len_buf)?;
    let len = u32::from_be_bytes(len_buf) as usize;

    let mut json_buf = vec![0u8; len];
    stream.read_exact(&mut json_buf)?;

    serde_json::from_slice(&json_buf).map_err(|e| std::io::Error::new(std::io::ErrorKind::InvalidData, e))
}

#[tauri::command]
pub async fn pty_connect(state: State<'_, PtyState>) -> Result<(), String> {
    let path = socket_path();
    let stream = UnixStream::connect(&path).map_err(|e| format!("Connect failed: {}", e))?;
    stream.set_nonblocking(false).map_err(|e| format!("Set blocking failed: {}", e))?;

    *state.stream.lock().unwrap() = Some(stream);
    Ok(())
}

#[tauri::command]
pub async fn pty_spawn(
    state: State<'_, PtyState>,
    app: AppHandle,
    id: String,
    cwd: String,
    cols: u32,
    rows: u32,
) -> Result<(), String> {
    let mut guard = state.stream.lock().unwrap();
    let stream = guard.as_mut().ok_or("Not connected")?;

    let msg = serde_json::json!({
        "cmd": "spawn",
        "id": id,
        "cwd": cwd,
        "cols": cols,
        "rows": rows,
    });

    write_frame(stream, &msg).map_err(|e| e.to_string())?;

    // Start reader thread for this connection
    let stream_clone = stream.try_clone().map_err(|e| e.to_string())?;
    let app_clone = app.clone();

    std::thread::spawn(move || {
        let mut stream = stream_clone;
        loop {
            match read_frame(&mut stream) {
                Ok(msg) => {
                    let _ = app_clone.emit("pty-event", msg);
                }
                Err(_) => break,
            }
        }
    });

    Ok(())
}

#[tauri::command]
pub async fn pty_write(state: State<'_, PtyState>, id: String, data: String) -> Result<(), String> {
    let mut guard = state.stream.lock().unwrap();
    let stream = guard.as_mut().ok_or("Not connected")?;

    let msg = serde_json::json!({
        "cmd": "write",
        "id": id,
        "data": data,
    });

    write_frame(stream, &msg).map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn pty_resize(state: State<'_, PtyState>, id: String, cols: u32, rows: u32) -> Result<(), String> {
    let mut guard = state.stream.lock().unwrap();
    let stream = guard.as_mut().ok_or("Not connected")?;

    let msg = serde_json::json!({
        "cmd": "resize",
        "id": id,
        "cols": cols,
        "rows": rows,
    });

    write_frame(stream, &msg).map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn pty_kill(state: State<'_, PtyState>, id: String) -> Result<(), String> {
    let mut guard = state.stream.lock().unwrap();
    let stream = guard.as_mut().ok_or("Not connected")?;

    let msg = serde_json::json!({
        "cmd": "kill",
        "id": id,
    });

    write_frame(stream, &msg).map_err(|e| e.to_string())
}
```

**Step 2: Modify lib.rs to include pty_bridge**

Add to `app/src-tauri/src/lib.rs`:

```rust
mod pty_bridge;

use pty_bridge::PtyState;
```

And update the builder:

```rust
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_deep_link::init())
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_pty::init())  // Keep for now, remove later
        .plugin(tauri_plugin_fs::init())
        .manage(PtyState::default())
        .invoke_handler(tauri::generate_handler![
            greet,
            pty_bridge::pty_connect,
            pty_bridge::pty_spawn,
            pty_bridge::pty_write,
            pty_bridge::pty_resize,
            pty_bridge::pty_kill,
        ])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
```

**Step 3: Add dirs dependency to Cargo.toml**

Add to `app/src-tauri/Cargo.toml`:

```toml
dirs = "5"
```

**Step 4: Build and verify Rust compiles**

Run:
```bash
cd /Users/victor.arias/projects/claude-manager/app/src-tauri
cargo check
```

Expected: Compilation succeeds

**Step 5: Commit**

```bash
git add app/src-tauri/src/pty_bridge.rs app/src-tauri/src/lib.rs app/src-tauri/Cargo.toml
git commit -m "feat(tauri): add pty bridge commands for sidecar communication"
```

---

## Task 4: Update Sessions Store to Use New PTY

**Files:**
- Modify: `app/src/store/sessions.ts`

**Step 1: Update sessions.ts to use Tauri commands**

Replace the tauri-pty import and usage with Tauri invoke calls:

```typescript
import { create } from 'zustand';
import { invoke } from '@tauri-apps/api/core';
import { listen } from '@tauri-apps/api/event';
import { Terminal } from '@xterm/xterm';
import { diagLog } from '../utils/diagLog';

export interface Session {
  id: string;
  label: string;
  state: 'working' | 'waiting';
  terminal: Terminal | null;
  cwd: string;
}

interface SessionStore {
  sessions: Session[];
  activeSessionId: string | null;
  connected: boolean;

  // Actions
  connect: () => Promise<void>;
  createSession: (label: string, cwd: string) => Promise<string>;
  closeSession: (id: string) => void;
  setActiveSession: (id: string | null) => void;
  connectTerminal: (id: string, terminal: Terminal) => Promise<void>;
  resizeSession: (id: string, cols: number, rows: number) => void;
}

let sessionCounter = 0;
const pendingConnections = new Set<string>();
let eventUnlisten: (() => void) | null = null;

export const useSessionStore = create<SessionStore>((set, get) => ({
  sessions: [],
  activeSessionId: null,
  connected: false,

  connect: async () => {
    if (get().connected) return;

    try {
      await invoke('pty_connect');

      // Listen for PTY events
      eventUnlisten = await listen<any>('pty-event', (event) => {
        const msg = event.payload;
        const { sessions } = get();
        const session = sessions.find((s) => s.id === msg.id);

        if (!session?.terminal) return;

        switch (msg.event) {
          case 'data': {
            // Decode base64 data
            const data = atob(msg.data);
            session.terminal.write(data);
            break;
          }
          case 'exit': {
            session.terminal.write(`\r\n[Process exited with code ${msg.code}]\r\n`);
            break;
          }
          case 'error': {
            session.terminal.write(`\r\n[Error: ${msg.error}]\r\n`);
            break;
          }
        }
      });

      set({ connected: true });
    } catch (e) {
      console.error('[Session] Connect failed:', e);
    }
  },

  createSession: async (label: string, cwd: string) => {
    const id = `session-${++sessionCounter}`;
    const session: Session = {
      id,
      label,
      state: 'working',
      terminal: null,
      cwd,
    };

    set((state) => ({
      sessions: [...state.sessions, session],
      activeSessionId: id,
    }));

    return id;
  },

  closeSession: (id: string) => {
    const { sessions, activeSessionId } = get();
    const session = sessions.find((s) => s.id === id);

    if (session) {
      invoke('pty_kill', { id }).catch(console.error);
      session.terminal?.dispose();
    }

    const newSessions = sessions.filter((s) => s.id !== id);
    let newActiveId = activeSessionId;

    if (activeSessionId === id) {
      newActiveId = newSessions.length > 0 ? newSessions[0].id : null;
    }

    set({
      sessions: newSessions,
      activeSessionId: newActiveId,
    });
  },

  setActiveSession: (id: string | null) => {
    set({ activeSessionId: id });
  },

  connectTerminal: async (id: string, terminal: Terminal) => {
    const { sessions, connected } = get();
    const session = sessions.find((s) => s.id === id);
    if (!session) return;

    // Prevent double-connection
    if (pendingConnections.has(id)) return;
    pendingConnections.add(id);

    // Ensure connected to pty-server
    if (!connected) {
      await get().connect();
    }

    const cols = terminal.cols > 0 ? terminal.cols : 80;
    const rows = terminal.rows > 0 ? terminal.rows : 24;

    diagLog('connectTerminal-spawn', {
      cols,
      rows,
      cwd: session.cwd,
    });

    try {
      await invoke('pty_spawn', {
        id,
        cwd: session.cwd,
        cols,
        rows,
      });

      // Terminal input -> PTY
      terminal.onData((data: string) => {
        invoke('pty_write', { id, data }).catch(console.error);
      });

      // Update session with terminal ref
      set((state) => ({
        sessions: state.sessions.map((s) =>
          s.id === id ? { ...s, terminal } : s
        ),
      }));

      pendingConnections.delete(id);
    } catch (e) {
      console.error('[Session] Spawn failed:', e);
      terminal.write(`\r\n[Failed to spawn PTY: ${e}]\r\n`);
      pendingConnections.delete(id);
    }
  },

  resizeSession: (id: string, cols: number, rows: number) => {
    diagLog('resizeSession', { id, cols, rows });
    invoke('pty_resize', { id, cols, rows }).catch(console.error);
  },
}));
```

**Step 2: Remove tauri-pty from package.json**

This will be done after testing. For now, keep it as fallback.

**Step 3: Commit**

```bash
git add app/src/store/sessions.ts
git commit -m "feat(app): update sessions store to use pty bridge"
```

---

## Task 5: Remove Column Cap and Test

**Files:**
- Modify: `app/src/components/Terminal.tsx`

**Step 1: Remove MAX_COLS cap**

In `app/src/components/Terminal.tsx`, remove the column cap:

```typescript
// Before:
const MAX_COLS = 80;
const calculatedCols = Math.max(Math.floor(scaledWidthAvailable / scaledCharWidth), 1);
const cols = Math.min(calculatedCols, MAX_COLS);

// After:
const cols = Math.max(Math.floor(scaledWidthAvailable / scaledCharWidth), 1);
```

**Step 2: Start pty-server manually for dev**

Run:
```bash
cd /Users/victor.arias/projects/claude-manager/app/pty-server
node dist/index.js
```

**Step 3: Start Tauri dev**

Run (in another terminal):
```bash
cd /Users/victor.arias/projects/claude-manager/app
npm run tauri dev
```

**Step 4: Manual test**

1. Open app
2. Create new session
3. Run `claude` at full terminal width
4. Trigger "thinking" animation
5. Verify: no extra line breaks, no flicker

**Step 5: Commit if successful**

```bash
git add app/src/components/Terminal.tsx
git commit -m "fix(terminal): remove column cap, now using node-pty sidecar"
```

---

## Task 6: Clean Up Old PTY Dependencies

**Files:**
- Modify: `app/package.json`
- Modify: `app/src-tauri/Cargo.toml`
- Modify: `app/src-tauri/src/lib.rs`

**Step 1: Remove tauri-pty from package.json**

Remove from dependencies:
```json
"tauri-pty": "^0.1.1"
```

**Step 2: Remove tauri-plugin-pty from Cargo.toml**

Remove:
```toml
tauri-plugin-pty = "0.1"
```

**Step 3: Remove from lib.rs**

Remove:
```rust
.plugin(tauri_plugin_pty::init())
```

**Step 4: Reinstall and rebuild**

Run:
```bash
cd /Users/victor.arias/projects/claude-manager/app
npm install
cd src-tauri && cargo build
```

**Step 5: Commit**

```bash
git add app/package.json app/pnpm-lock.yaml app/src-tauri/Cargo.toml app/src-tauri/Cargo.lock app/src-tauri/src/lib.rs
git commit -m "chore: remove tauri-pty dependency, fully migrated to sidecar"
```
