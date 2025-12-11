import { create } from 'zustand';
import { invoke } from '@tauri-apps/api/core';
import { listen } from '@tauri-apps/api/event';
import { Terminal } from '@xterm/xterm';

export interface Session {
  id: string;
  label: string;
  state: 'working' | 'waiting_input' | 'idle';
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

export const useSessionStore = create<SessionStore>((set, get) => ({
  sessions: [],
  activeSessionId: null,
  connected: false,

  connect: async () => {
    if (get().connected) return;

    try {
      await invoke('pty_connect');

      // Listen for PTY events
      await listen<any>('pty-event', (event) => {
        const msg = event.payload;
        const { sessions } = get();
        const session = sessions.find((s) => s.id === msg.id);

        if (!session?.terminal) return;

        switch (msg.event) {
          case 'data': {
            // Decode base64 data to UTF-8
            // atob() returns Latin-1, need to decode as UTF-8
            const binaryStr = atob(msg.data);
            const bytes = Uint8Array.from(binaryStr, (c) => c.charCodeAt(0));
            const data = new TextDecoder('utf-8').decode(bytes);
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
    invoke('pty_resize', { id, cols, rows }).catch(console.error);
  },
}));
