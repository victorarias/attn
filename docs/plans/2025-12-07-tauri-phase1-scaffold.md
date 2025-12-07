# Tauri + xterm.js Phase 1: Scaffold Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Create a working Tauri app with React frontend that embeds xterm.js connected to a real PTY shell.

**Architecture:** Tauri app with React + TypeScript frontend. Uses `tauri-plugin-pty` for PTY management (spawns shell, streams I/O). xterm.js renders terminal in webview. Tauri IPC bridges PTY ↔ xterm.js.

**Tech Stack:** Tauri 2.0, React 18, TypeScript, xterm.js (@xterm/xterm), tauri-plugin-pty

**References:**
- [Tauri Create Project](https://v2.tauri.app/start/create-project/)
- [tauri-plugin-pty](https://crates.io/crates/tauri-plugin-pty)
- [@xterm/xterm](https://www.npmjs.com/package/@xterm/xterm)

---

## Task 1: Create Tauri Project

**Files:**
- Create: `app/` directory (entire Tauri project)

**Step 1: Create the Tauri app with React + TypeScript**

Run from repository root:
```bash
cd /Users/victor.arias/projects/claude-manager
npm create tauri-app@latest app -- --template react-ts --manager pnpm
```

When prompted:
- Project name: `app` (already specified)
- Identifier: `com.attn.app`
- Template: react-ts (already specified)

**Step 2: Verify project structure**

```bash
ls -la app/
```

Expected structure:
```
app/
├── src/              # React frontend
├── src-tauri/        # Rust backend
├── package.json
├── tsconfig.json
├── vite.config.ts
└── index.html
```

**Step 3: Install dependencies and verify it runs**

```bash
cd app && pnpm install && pnpm tauri dev
```

Expected: A window opens showing the default Tauri + React template.

**Step 4: Commit**

```bash
cd /Users/victor.arias/projects/claude-manager
git add app/
git commit -m "feat(app): scaffold Tauri + React + TypeScript project"
```

---

## Task 2: Add tauri-plugin-pty

**Files:**
- Modify: `app/src-tauri/Cargo.toml`
- Modify: `app/src-tauri/src/lib.rs`
- Modify: `app/src-tauri/capabilities/default.json`

**Step 1: Add the Rust dependency**

Edit `app/src-tauri/Cargo.toml`, add to `[dependencies]`:

```toml
tauri-plugin-pty = "0.1"
```

**Step 2: Register the plugin in Tauri**

Edit `app/src-tauri/src/lib.rs`:

```rust
#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_pty::init())
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
```

**Step 3: Add PTY permissions to capabilities**

Edit `app/src-tauri/capabilities/default.json`:

```json
{
  "$schema": "../gen/schemas/desktop-schema.json",
  "identifier": "default",
  "description": "Default capabilities for the main window",
  "windows": ["main"],
  "permissions": [
    "core:default",
    "pty:default"
  ]
}
```

**Step 4: Install the JS package**

```bash
cd app && pnpm add tauri-pty
```

**Step 5: Verify build works**

```bash
cd app && pnpm tauri build --debug
```

Expected: Build completes without errors.

**Step 6: Commit**

```bash
git add app/
git commit -m "feat(app): add tauri-plugin-pty for terminal spawning"
```

---

## Task 3: Add xterm.js

**Files:**
- Modify: `app/package.json`
- Create: `app/src/components/Terminal.tsx`
- Create: `app/src/components/Terminal.css`

**Step 1: Install xterm.js packages**

```bash
cd app && pnpm add @xterm/xterm @xterm/addon-fit @xterm/addon-web-links
```

**Step 2: Create Terminal component**

Create `app/src/components/Terminal.tsx`:

```tsx
import { useEffect, useRef } from 'react';
import { Terminal as XTerm } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { WebLinksAddon } from '@xterm/addon-web-links';
import '@xterm/xterm/css/xterm.css';
import './Terminal.css';

interface TerminalProps {
  onData?: (data: string) => void;
  terminalRef?: React.MutableRefObject<XTerm | null>;
}

export function Terminal({ onData, terminalRef }: TerminalProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const xtermRef = useRef<XTerm | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);

  useEffect(() => {
    if (!containerRef.current) return;

    // Create terminal
    const term = new XTerm({
      cursorBlink: true,
      fontSize: 14,
      fontFamily: 'Menlo, Monaco, "Courier New", monospace',
      theme: {
        background: '#1e1e1e',
        foreground: '#d4d4d4',
        cursor: '#d4d4d4',
      },
    });

    // Add addons
    const fitAddon = new FitAddon();
    term.loadAddon(fitAddon);
    term.loadAddon(new WebLinksAddon());

    // Open terminal in container
    term.open(containerRef.current);
    fitAddon.fit();

    // Store refs
    xtermRef.current = term;
    fitAddonRef.current = fitAddon;
    if (terminalRef) {
      terminalRef.current = term;
    }

    // Handle input
    if (onData) {
      term.onData(onData);
    }

    // Handle resize
    const handleResize = () => {
      fitAddon.fit();
    };
    window.addEventListener('resize', handleResize);

    // Cleanup
    return () => {
      window.removeEventListener('resize', handleResize);
      term.dispose();
    };
  }, [onData, terminalRef]);

  return <div ref={containerRef} className="terminal-container" />;
}
```

**Step 3: Create Terminal CSS**

Create `app/src/components/Terminal.css`:

```css
.terminal-container {
  width: 100%;
  height: 100%;
  background: #1e1e1e;
}

.terminal-container .xterm {
  height: 100%;
  padding: 8px;
}
```

**Step 4: Verify component compiles**

```bash
cd app && pnpm build
```

Expected: Build succeeds.

**Step 5: Commit**

```bash
git add app/
git commit -m "feat(app): add xterm.js Terminal component"
```

---

## Task 4: Connect PTY to xterm.js

**Files:**
- Create: `app/src/hooks/usePty.ts`
- Modify: `app/src/App.tsx`
- Modify: `app/src/App.css`

**Step 1: Create usePty hook**

Create `app/src/hooks/usePty.ts`:

```tsx
import { useEffect, useRef, useCallback } from 'react';
import { spawn, Pty } from 'tauri-pty';
import { Terminal } from '@xterm/xterm';

interface UsePtyOptions {
  terminal: Terminal | null;
  cols?: number;
  rows?: number;
}

export function usePty({ terminal, cols = 80, rows = 24 }: UsePtyOptions) {
  const ptyRef = useRef<Pty | null>(null);

  // Spawn PTY when terminal is ready
  useEffect(() => {
    if (!terminal) return;

    const initPty = async () => {
      // Get user's default shell
      const shell = process.env.SHELL || '/bin/zsh';

      const pty = await spawn(shell, [], {
        cols: terminal.cols,
        rows: terminal.rows,
        cwd: process.env.HOME,
      });

      ptyRef.current = pty;

      // PTY output -> terminal
      pty.onData((data: string) => {
        terminal.write(data);
      });

      // Terminal input -> PTY
      terminal.onData((data: string) => {
        pty.write(data);
      });

      // Handle PTY exit
      pty.onExit(() => {
        terminal.write('\r\n[Process exited]\r\n');
      });
    };

    initPty();

    return () => {
      ptyRef.current?.kill();
    };
  }, [terminal]);

  // Resize handler
  const resize = useCallback((cols: number, rows: number) => {
    ptyRef.current?.resize(cols, rows);
  }, []);

  return { resize };
}
```

**Step 2: Update App.tsx**

Replace `app/src/App.tsx` with:

```tsx
import { useRef } from 'react';
import { Terminal as XTerm } from '@xterm/xterm';
import { Terminal } from './components/Terminal';
import { usePty } from './hooks/usePty';
import './App.css';

function App() {
  const terminalRef = useRef<XTerm | null>(null);

  usePty({ terminal: terminalRef.current });

  return (
    <div className="app">
      <div className="terminal-pane">
        <Terminal terminalRef={terminalRef} />
      </div>
    </div>
  );
}

export default App;
```

**Step 3: Update App.css**

Replace `app/src/App.css` with:

```css
* {
  margin: 0;
  padding: 0;
  box-sizing: border-box;
}

html, body, #root {
  height: 100%;
  width: 100%;
  overflow: hidden;
}

.app {
  height: 100%;
  width: 100%;
  display: flex;
  background: #1e1e1e;
}

.terminal-pane {
  flex: 1;
  height: 100%;
  overflow: hidden;
}
```

**Step 4: Run and test**

```bash
cd app && pnpm tauri dev
```

Expected: Window opens with a working terminal (your default shell). You can type commands and see output.

**Step 5: Commit**

```bash
git add app/
git commit -m "feat(app): connect xterm.js to PTY - working terminal"
```

---

## Task 5: Add Terminal Resize Support

**Files:**
- Modify: `app/src/components/Terminal.tsx`
- Modify: `app/src/hooks/usePty.ts`
- Modify: `app/src/App.tsx`

**Step 1: Update Terminal component to expose resize**

Update `app/src/components/Terminal.tsx`:

```tsx
import { useEffect, useRef, useCallback, useImperativeHandle, forwardRef } from 'react';
import { Terminal as XTerm } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { WebLinksAddon } from '@xterm/addon-web-links';
import '@xterm/xterm/css/xterm.css';
import './Terminal.css';

export interface TerminalHandle {
  terminal: XTerm | null;
  fit: () => { cols: number; rows: number } | null;
}

interface TerminalProps {
  onResize?: (cols: number, rows: number) => void;
}

export const Terminal = forwardRef<TerminalHandle, TerminalProps>(
  function Terminal({ onResize }, ref) {
    const containerRef = useRef<HTMLDivElement>(null);
    const xtermRef = useRef<XTerm | null>(null);
    const fitAddonRef = useRef<FitAddon | null>(null);

    useImperativeHandle(ref, () => ({
      terminal: xtermRef.current,
      fit: () => {
        if (fitAddonRef.current && xtermRef.current) {
          fitAddonRef.current.fit();
          return {
            cols: xtermRef.current.cols,
            rows: xtermRef.current.rows,
          };
        }
        return null;
      },
    }));

    useEffect(() => {
      if (!containerRef.current) return;

      const term = new XTerm({
        cursorBlink: true,
        fontSize: 14,
        fontFamily: 'Menlo, Monaco, "Courier New", monospace',
        theme: {
          background: '#1e1e1e',
          foreground: '#d4d4d4',
          cursor: '#d4d4d4',
        },
      });

      const fitAddon = new FitAddon();
      term.loadAddon(fitAddon);
      term.loadAddon(new WebLinksAddon());

      term.open(containerRef.current);
      fitAddon.fit();

      xtermRef.current = term;
      fitAddonRef.current = fitAddon;

      // Notify initial size
      if (onResize) {
        onResize(term.cols, term.rows);
      }

      // Handle window resize
      const handleResize = () => {
        fitAddon.fit();
        if (onResize) {
          onResize(term.cols, term.rows);
        }
      };
      window.addEventListener('resize', handleResize);

      return () => {
        window.removeEventListener('resize', handleResize);
        term.dispose();
      };
    }, [onResize]);

    return <div ref={containerRef} className="terminal-container" />;
  }
);
```

**Step 2: Update usePty to handle resize**

Update `app/src/hooks/usePty.ts`:

```tsx
import { useEffect, useRef, useCallback, useState } from 'react';
import { spawn, Pty } from 'tauri-pty';
import { Terminal } from '@xterm/xterm';

interface UsePtyOptions {
  shell?: string;
  cwd?: string;
}

export function usePty(options: UsePtyOptions = {}) {
  const [pty, setPty] = useState<Pty | null>(null);
  const terminalRef = useRef<Terminal | null>(null);

  const connect = useCallback(async (terminal: Terminal) => {
    terminalRef.current = terminal;

    const shell = options.shell || '/bin/zsh';
    const cwd = options.cwd || process.env.HOME || '/';

    const newPty = await spawn(shell, [], {
      cols: terminal.cols,
      rows: terminal.rows,
      cwd,
    });

    // PTY output -> terminal
    newPty.onData((data: string) => {
      terminal.write(data);
    });

    // Terminal input -> PTY
    terminal.onData((data: string) => {
      newPty.write(data);
    });

    // Handle PTY exit
    newPty.onExit(() => {
      terminal.write('\r\n[Process exited]\r\n');
    });

    setPty(newPty);
    return newPty;
  }, [options.shell, options.cwd]);

  const resize = useCallback((cols: number, rows: number) => {
    pty?.resize(cols, rows);
  }, [pty]);

  const kill = useCallback(() => {
    pty?.kill();
    setPty(null);
  }, [pty]);

  // Cleanup on unmount
  useEffect(() => {
    return () => {
      pty?.kill();
    };
  }, [pty]);

  return { pty, connect, resize, kill };
}
```

**Step 3: Update App.tsx to wire everything**

Update `app/src/App.tsx`:

```tsx
import { useRef, useEffect, useCallback } from 'react';
import { Terminal, TerminalHandle } from './components/Terminal';
import { usePty } from './hooks/usePty';
import './App.css';

function App() {
  const terminalRef = useRef<TerminalHandle>(null);
  const { connect, resize } = usePty();

  // Connect PTY when terminal mounts
  useEffect(() => {
    const terminal = terminalRef.current?.terminal;
    if (terminal) {
      connect(terminal);
    }
  }, [connect]);

  // Handle resize
  const handleResize = useCallback((cols: number, rows: number) => {
    resize(cols, rows);
  }, [resize]);

  return (
    <div className="app">
      <div className="terminal-pane">
        <Terminal ref={terminalRef} onResize={handleResize} />
      </div>
    </div>
  );
}

export default App;
```

**Step 4: Test resize**

```bash
cd app && pnpm tauri dev
```

Expected: Terminal resizes properly when window is resized. Run `stty size` to verify cols/rows update.

**Step 5: Commit**

```bash
git add app/
git commit -m "feat(app): add terminal resize support"
```

---

## Task 6: Spawn Claude Instead of Shell

**Files:**
- Modify: `app/src/App.tsx`
- Modify: `app/src/hooks/usePty.ts`

**Step 1: Update usePty to accept command**

Update `app/src/hooks/usePty.ts`, change `spawn` call:

```tsx
interface UsePtyOptions {
  command?: string;
  args?: string[];
  cwd?: string;
}

export function usePty(options: UsePtyOptions = {}) {
  // ... existing code ...

  const connect = useCallback(async (terminal: Terminal) => {
    terminalRef.current = terminal;

    const command = options.command || '/bin/zsh';
    const args = options.args || [];
    const cwd = options.cwd || process.env.HOME || '/';

    const newPty = await spawn(command, args, {
      cols: terminal.cols,
      rows: terminal.rows,
      cwd,
    });

    // ... rest unchanged ...
  }, [options.command, options.args, options.cwd]);

  // ... rest unchanged ...
}
```

**Step 2: Update App.tsx to spawn claude**

Update `app/src/App.tsx`:

```tsx
import { useRef, useEffect, useCallback } from 'react';
import { Terminal, TerminalHandle } from './components/Terminal';
import { usePty } from './hooks/usePty';
import './App.css';

function App() {
  const terminalRef = useRef<TerminalHandle>(null);
  const { connect, resize } = usePty({
    command: 'claude',
    args: [],
    cwd: process.env.HOME,
  });

  useEffect(() => {
    const terminal = terminalRef.current?.terminal;
    if (terminal) {
      connect(terminal);
    }
  }, [connect]);

  const handleResize = useCallback((cols: number, rows: number) => {
    resize(cols, rows);
  }, [resize]);

  return (
    <div className="app">
      <div className="terminal-pane">
        <Terminal ref={terminalRef} onResize={handleResize} />
      </div>
    </div>
  );
}

export default App;
```

**Step 3: Test with Claude**

```bash
cd app && pnpm tauri dev
```

Expected: Claude CLI starts in the terminal window. Full interactivity works.

**Step 4: Commit**

```bash
git add app/
git commit -m "feat(app): spawn claude command in terminal"
```

---

## Task 7: Add Basic Sidebar Layout

**Files:**
- Create: `app/src/components/Sidebar.tsx`
- Create: `app/src/components/Sidebar.css`
- Modify: `app/src/App.tsx`
- Modify: `app/src/App.css`

**Step 1: Create Sidebar component**

Create `app/src/components/Sidebar.tsx`:

```tsx
import './Sidebar.css';

interface Session {
  id: string;
  label: string;
  state: 'working' | 'waiting';
}

interface SidebarProps {
  sessions: Session[];
  selectedId: string | null;
  onSelectSession: (id: string) => void;
}

export function Sidebar({ sessions, selectedId, onSelectSession }: SidebarProps) {
  return (
    <div className="sidebar">
      <div className="sidebar-header">
        <h2>Sessions</h2>
      </div>
      <div className="session-list">
        {sessions.length === 0 ? (
          <div className="empty-state">No sessions</div>
        ) : (
          sessions.map((session) => (
            <div
              key={session.id}
              className={`session-item ${selectedId === session.id ? 'selected' : ''}`}
              onClick={() => onSelectSession(session.id)}
            >
              <span className={`state-indicator ${session.state}`} />
              <span className="session-label">{session.label}</span>
            </div>
          ))
        )}
      </div>
    </div>
  );
}
```

**Step 2: Create Sidebar CSS**

Create `app/src/components/Sidebar.css`:

```css
.sidebar {
  width: 240px;
  height: 100%;
  background: #252526;
  border-right: 1px solid #3c3c3c;
  display: flex;
  flex-direction: column;
}

.sidebar-header {
  padding: 16px;
  border-bottom: 1px solid #3c3c3c;
}

.sidebar-header h2 {
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  color: #cccccc;
  text-transform: uppercase;
  letter-spacing: 0.5px;
}

.session-list {
  flex: 1;
  overflow-y: auto;
  padding: 8px;
}

.empty-state {
  padding: 16px;
  color: #808080;
  font-size: 13px;
  text-align: center;
}

.session-item {
  display: flex;
  align-items: center;
  padding: 8px 12px;
  border-radius: 4px;
  cursor: pointer;
  color: #cccccc;
  font-size: 13px;
}

.session-item:hover {
  background: #2a2d2e;
}

.session-item.selected {
  background: #094771;
}

.state-indicator {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  margin-right: 8px;
}

.state-indicator.working {
  background: #4ec9b0;
}

.state-indicator.waiting {
  background: #dcdcaa;
}

.session-label {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
```

**Step 3: Update App.tsx with sidebar**

Update `app/src/App.tsx`:

```tsx
import { useRef, useEffect, useCallback, useState } from 'react';
import { Terminal, TerminalHandle } from './components/Terminal';
import { Sidebar } from './components/Sidebar';
import { usePty } from './hooks/usePty';
import './App.css';

// Mock sessions for now
const mockSessions = [
  { id: '1', label: 'claude-manager', state: 'waiting' as const },
  { id: '2', label: 'other-project', state: 'working' as const },
];

function App() {
  const terminalRef = useRef<TerminalHandle>(null);
  const [selectedSession, setSelectedSession] = useState<string | null>('1');

  const { connect, resize } = usePty({
    command: 'claude',
    args: [],
    cwd: process.env.HOME,
  });

  useEffect(() => {
    const terminal = terminalRef.current?.terminal;
    if (terminal) {
      connect(terminal);
    }
  }, [connect]);

  const handleResize = useCallback((cols: number, rows: number) => {
    resize(cols, rows);
  }, [resize]);

  return (
    <div className="app">
      <Sidebar
        sessions={mockSessions}
        selectedId={selectedSession}
        onSelectSession={setSelectedSession}
      />
      <div className="terminal-pane">
        <Terminal ref={terminalRef} onResize={handleResize} />
      </div>
    </div>
  );
}

export default App;
```

**Step 4: Update App.css for layout**

Update `app/src/App.css`:

```css
* {
  margin: 0;
  padding: 0;
  box-sizing: border-box;
}

html, body, #root {
  height: 100%;
  width: 100%;
  overflow: hidden;
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
}

.app {
  height: 100%;
  width: 100%;
  display: flex;
  background: #1e1e1e;
}

.terminal-pane {
  flex: 1;
  height: 100%;
  overflow: hidden;
}
```

**Step 5: Test layout**

```bash
cd app && pnpm tauri dev
```

Expected: Sidebar on left, terminal on right. Clicking sessions highlights them (doesn't switch terminals yet).

**Step 6: Commit**

```bash
git add app/
git commit -m "feat(app): add sidebar layout with session list"
```

---

## Task 8: Final Cleanup and Verification

**Files:**
- Modify: `app/src-tauri/tauri.conf.json`

**Step 1: Update window title**

Edit `app/src-tauri/tauri.conf.json`, find the `windows` section:

```json
{
  "windows": [
    {
      "title": "attn",
      "width": 1200,
      "height": 800,
      "minWidth": 800,
      "minHeight": 600
    }
  ]
}
```

**Step 2: Build release version**

```bash
cd app && pnpm tauri build
```

Expected: Creates `app/src-tauri/target/release/bundle/macos/attn.app`

**Step 3: Test the built app**

```bash
open app/src-tauri/target/release/bundle/macos/attn.app
```

Expected: App opens, terminal works, resize works.

**Step 4: Final commit**

```bash
git add app/
git commit -m "feat(app): Phase 1 complete - working Tauri + xterm.js scaffold"
```

---

## Verification Checklist

After completing all tasks, verify:

- [ ] `pnpm tauri dev` launches the app
- [ ] Terminal renders with shell prompt
- [ ] Typing works (characters appear)
- [ ] Commands execute (try `ls`, `pwd`)
- [ ] Colors work (try `ls -G` or a colored prompt)
- [ ] Resize works (drag window, run `stty size` to confirm)
- [ ] Claude spawns (change command to `claude`)
- [ ] Sidebar renders with mock sessions
- [ ] Release build works (`pnpm tauri build`)

---

## Next Phase Preview

Phase 2 will add:
- WebSocket connection to Go daemon
- Real session data in sidebar
- Multiple terminal tabs
