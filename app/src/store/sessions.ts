import { create } from 'zustand';
import { invoke } from '@tauri-apps/api/core';
import { listen } from '@tauri-apps/api/event';
import { Terminal } from '@xterm/xterm';
import type { UISessionState } from '../types/sessionState';

export interface UtilityTerminal {
  id: string;
  ptyId: string;
  title: string;
}

export interface TerminalPanelState {
  isOpen: boolean;
  height: number;
  activeTabId: string | null;
  terminals: UtilityTerminal[];
  nextTerminalNumber: number;
}

const DEFAULT_PANEL_HEIGHT = 200;

function createDefaultPanelState(): TerminalPanelState {
  return {
    isOpen: false,
    height: DEFAULT_PANEL_HEIGHT,
    activeTabId: null,
    terminals: [],
    nextTerminalNumber: 1,
  };
}

export interface Session {
  id: string;
  label: string;
  state: UISessionState;
  terminal: Terminal | null;
  cwd: string;
  branch?: string;
  isWorktree?: boolean;
  terminalPanel: TerminalPanelState;
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

  // Terminal panel actions
  openTerminalPanel: (sessionId: string) => void;
  collapseTerminalPanel: (sessionId: string) => void;
  setTerminalPanelHeight: (sessionId: string, height: number) => void;
  addUtilityTerminal: (sessionId: string, ptyId: string) => string;
  removeUtilityTerminal: (sessionId: string, terminalId: string) => void;
  setActiveUtilityTerminal: (sessionId: string, terminalId: string) => void;
  renameUtilityTerminal: (sessionId: string, terminalId: string, title: string) => void;
}

let sessionCounter = 0;
const pendingConnections = new Set<string>();

// Test helper for E2E - allows injecting sessions without PTY
interface TestSession {
  id: string;
  label: string;
  state: UISessionState;
  cwd: string;
}

declare global {
  interface Window {
    __TEST_INJECT_SESSION?: (session: TestSession) => void;
    __TEST_UPDATE_SESSION_STATE?: (id: string, state: UISessionState) => void;
  }
}

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
      terminalPanel: createDefaultPanelState(),
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

  openTerminalPanel: (sessionId: string) => {
    set((state) => ({
      sessions: state.sessions.map((s) =>
        s.id === sessionId ? { ...s, terminalPanel: { ...s.terminalPanel, isOpen: true } } : s
      ),
    }));
  },

  collapseTerminalPanel: (sessionId: string) => {
    set((state) => ({
      sessions: state.sessions.map((s) =>
        s.id === sessionId ? { ...s, terminalPanel: { ...s.terminalPanel, isOpen: false } } : s
      ),
    }));
  },

  setTerminalPanelHeight: (sessionId: string, height: number) => {
    set((state) => ({
      sessions: state.sessions.map((s) =>
        s.id === sessionId ? { ...s, terminalPanel: { ...s.terminalPanel, height } } : s
      ),
    }));
  },

  addUtilityTerminal: (sessionId: string, ptyId: string) => {
    const terminalId = `util-${Date.now()}`;
    set((state) => ({
      sessions: state.sessions.map((s) => {
        if (s.id !== sessionId) return s;
        const title = `Shell ${s.terminalPanel.nextTerminalNumber}`;
        const newTerminal: UtilityTerminal = { id: terminalId, ptyId, title };
        return {
          ...s,
          terminalPanel: {
            ...s.terminalPanel,
            terminals: [...s.terminalPanel.terminals, newTerminal],
            activeTabId: terminalId,
            nextTerminalNumber: s.terminalPanel.nextTerminalNumber + 1,
          },
        };
      }),
    }));
    return terminalId;
  },

  removeUtilityTerminal: (sessionId: string, terminalId: string) => {
    set((state) => ({
      sessions: state.sessions.map((s) => {
        if (s.id !== sessionId) return s;
        const terminals = s.terminalPanel.terminals.filter((t) => t.id !== terminalId);
        let activeTabId = s.terminalPanel.activeTabId;

        // If we removed the active tab, select another
        if (activeTabId === terminalId) {
          const removedIndex = s.terminalPanel.terminals.findIndex((t) => t.id === terminalId);
          if (terminals.length > 0) {
            // Select next tab, or previous if we removed the last
            activeTabId = terminals[Math.min(removedIndex, terminals.length - 1)].id;
          } else {
            activeTabId = null;
          }
        }

        return {
          ...s,
          terminalPanel: {
            ...s.terminalPanel,
            terminals,
            activeTabId,
            // If no more terminals, close the panel
            isOpen: terminals.length > 0 ? s.terminalPanel.isOpen : false,
          },
        };
      }),
    }));
  },

  setActiveUtilityTerminal: (sessionId: string, terminalId: string) => {
    set((state) => ({
      sessions: state.sessions.map((s) =>
        s.id === sessionId
          ? { ...s, terminalPanel: { ...s.terminalPanel, activeTabId: terminalId } }
          : s
      ),
    }));
  },

  renameUtilityTerminal: (sessionId: string, terminalId: string, title: string) => {
    set((state) => ({
      sessions: state.sessions.map((s) =>
        s.id === sessionId
          ? {
              ...s,
              terminalPanel: {
                ...s.terminalPanel,
                terminals: s.terminalPanel.terminals.map((t) =>
                  t.id === terminalId ? { ...t, title: title || t.title } : t
                ),
              },
            }
          : s
      ),
    }));
  },
}));

// Expose test helpers for E2E testing (only in development)
if (import.meta.env.DEV) {
  window.__TEST_INJECT_SESSION = (session: TestSession) => {
    useSessionStore.setState((state) => ({
      sessions: [...state.sessions, { ...session, terminal: null, terminalPanel: createDefaultPanelState() }],
    }));
  };

  window.__TEST_UPDATE_SESSION_STATE = (id: string, state: 'working' | 'waiting_input' | 'idle') => {
    useSessionStore.setState((s) => ({
      sessions: s.sessions.map((session) =>
        session.id === id ? { ...session, state } : session
      ),
    }));
  };
}
