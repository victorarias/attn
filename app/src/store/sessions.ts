import { create } from 'zustand';
import { Terminal } from '@xterm/xterm';
import type { UISessionState } from '../types/sessionState';
import { normalizeSessionState } from '../types/sessionState';
import type { SessionAgent } from '../types/sessionAgent';
import { normalizeSessionAgent } from '../types/sessionAgent';
import { listenPtyEvents, ptyKill, ptyResize, ptySpawn, ptyWrite, type PtyEventPayload } from '../pty/bridge';

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
  agent: SessionAgent;
  transcriptMatched: boolean;
  branch?: string;
  isWorktree?: boolean;
  terminalPanel: TerminalPanelState;
}

export interface DaemonSessionSnapshot {
  id: string;
  label: string;
  agent?: string;
  directory: string;
  state: string;
  branch?: string;
  is_worktree?: boolean;
}

interface LauncherConfig {
  claudeExecutable: string;
  codexExecutable: string;
}

interface SessionStore {
  sessions: Session[];
  activeSessionId: string | null;
  connected: boolean;
  launcherConfig: LauncherConfig;

  // Actions
  connect: () => Promise<void>;
  createSession: (label: string, cwd: string, id?: string, agent?: SessionAgent) => Promise<string>;
  closeSession: (id: string) => void;
  setActiveSession: (id: string | null) => void;
  connectTerminal: (id: string, terminal: Terminal) => Promise<void>;
  resizeSession: (id: string, cols: number, rows: number) => void;
  setForkParams: (sessionId: string, resumeSessionId: string) => void;
  setResumePicker: (sessionId: string) => void;
  setLauncherConfig: (config: LauncherConfig) => void;
  syncFromDaemonSessions: (daemonSessions: DaemonSessionSnapshot[]) => void;

  // Terminal panel actions
  openTerminalPanel: (sessionId: string) => void;
  collapseTerminalPanel: (sessionId: string) => void;
  setTerminalPanelHeight: (sessionId: string, height: number) => void;
  addUtilityTerminal: (sessionId: string, ptyId: string) => string;
  removeUtilityTerminal: (sessionId: string, terminalId: string) => void;
  setActiveUtilityTerminal: (sessionId: string, terminalId: string) => void;
  renameUtilityTerminal: (sessionId: string, terminalId: string, title: string) => void;
}

const pendingConnections = new Set<string>();
const pendingForkParams = new Map<string, { resumeSessionId?: string; forkSession?: boolean; resumePicker?: boolean }>();
const pendingTerminalEvents = new Map<string, PtyEventPayload[]>();
const MAX_PENDING_TERMINAL_EVENTS = 256;

function decodePtyBytes(payload: string): Uint8Array {
  const binaryStr = atob(payload);
  return Uint8Array.from(binaryStr, (c) => c.charCodeAt(0));
}

function writePtyEventToTerminal(terminal: Terminal, msg: PtyEventPayload) {
  switch (msg.event) {
    case 'data': {
      const bytes = decodePtyBytes(msg.data);
      const termWithUtf8 = terminal as Terminal & { writeUtf8?: (data: Uint8Array) => void };
      if (typeof termWithUtf8.writeUtf8 === 'function') {
        termWithUtf8.writeUtf8(bytes);
      } else {
        terminal.write(new TextDecoder('utf-8').decode(bytes));
      }
      break;
    }
    case 'reset':
      terminal.reset();
      break;
    case 'exit':
      terminal.write(`\r\n[Process exited with code ${msg.code}]\r\n`);
      break;
    case 'error':
      terminal.write(`\r\n[Error: ${msg.error}]\r\n`);
      break;
    default:
      break;
  }
}

function queuePendingTerminalEvent(id: string, msg: PtyEventPayload) {
  const events = pendingTerminalEvents.get(id) || [];
  if (events.length >= MAX_PENDING_TERMINAL_EVENTS) {
    events.shift();
  }
  events.push(msg);
  pendingTerminalEvents.set(id, events);
}

function flushPendingTerminalEvents(id: string, terminal: Terminal) {
  const events = pendingTerminalEvents.get(id);
  if (!events || events.length === 0) {
    return;
  }
  for (const event of events) {
    writePtyEventToTerminal(terminal, event);
  }
  pendingTerminalEvents.delete(id);
}

// Test helper for E2E - allows injecting sessions without PTY
interface TestSession {
  id: string;
  label: string;
  state: UISessionState;
  cwd: string;
  agent?: SessionAgent;
  branch?: string;
  isWorktree?: boolean;
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
  launcherConfig: {
    claudeExecutable: '',
    codexExecutable: '',
  },

  connect: async () => {
    if (get().connected) return;

    try {
      // Listen for PTY events (no connect call needed - PTY is native now)
      await listenPtyEvents((event) => {
        const msg = event.payload;
        const { sessions } = get();
        const session = sessions.find((s) => s.id === msg.id);

        if (msg.event === 'transcript') {
          if (typeof msg.matched === 'boolean') {
            const updated = sessions.map((entry) =>
              entry.id === msg.id ? { ...entry, transcriptMatched: msg.matched } : entry,
            );
            set({ sessions: updated });
          }
          return;
        }

        if (!session) {
          return;
        }
        if (!session.terminal) {
          queuePendingTerminalEvent(session.id, msg);
          return;
        }
        flushPendingTerminalEvents(session.id, session.terminal);
        writePtyEventToTerminal(session.terminal, msg);
      });

      set({ connected: true });
    } catch (e) {
      console.error('[Session] Connect failed:', e);
    }
  },

  createSession: async (label: string, cwd: string, providedId?: string, agent?: SessionAgent) => {
    // Use provided ID or generate new one
    const id = providedId || crypto.randomUUID();
    const resolvedAgent: SessionAgent = agent ?? 'claude';
    const session: Session = {
      id,
      label,
      state: 'working',
      terminal: null,
      cwd,
      agent: resolvedAgent,
      transcriptMatched: resolvedAgent !== 'codex',
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
      // Kill main session PTY
      ptyKill({ id }).catch(console.error);
      session.terminal?.dispose();
      pendingTerminalEvents.delete(id);

      // Kill all utility terminal PTYs
      for (const utilTerminal of session.terminalPanel.terminals) {
        ptyKill({ id: utilTerminal.ptyId }).catch(console.error);
        pendingTerminalEvents.delete(utilTerminal.ptyId);
      }
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
    const { sessions, connected, launcherConfig } = get();
    const session = sessions.find((s) => s.id === id);
    if (!session) return;

    // Prevent double-connection
    if (pendingConnections.has(id)) return;
    pendingConnections.add(id);

    // Ensure connected to pty-server
    if (!connected) {
      await get().connect();
    }

    // Set terminal ref before spawn/attach to avoid dropping early output.
    set((state) => ({
      sessions: state.sessions.map((s) =>
        s.id === id ? { ...s, terminal } : s
      ),
    }));

    const cols = terminal.cols > 0 ? terminal.cols : 80;
    const rows = terminal.rows > 0 ? terminal.rows : 24;

    try {
      // Check for pending fork params
      const forkParams = pendingForkParams.get(id);
      pendingForkParams.delete(id);

      await ptySpawn({
        args: {
          id,
          cwd: session.cwd,
          label: session.label,
          cols,
          rows,
          shell: false,
          agent: session.agent,
          resume_session_id: forkParams?.resumeSessionId ?? null,
          fork_session: forkParams?.forkSession ?? null,
          resume_picker: forkParams?.resumePicker ?? null,
          ...(launcherConfig.claudeExecutable
            ? { claude_executable: launcherConfig.claudeExecutable }
            : {}),
          ...(launcherConfig.codexExecutable
            ? { codex_executable: launcherConfig.codexExecutable }
            : {}),
        },
      });

      // Terminal input -> PTY
      const sendToPty = (data: string) => {
        ptyWrite({ id, data }).catch(console.error);
      };
      terminal.onData(sendToPty);

      // Shift+Enter sends newline for line breaks in Claude Code
      // Source: https://github.com/anthropics/claude-code/issues/1282
      terminal.attachCustomKeyEventHandler((ev) => {
        if (ev.key === 'Enter' && ev.shiftKey && !ev.ctrlKey && !ev.altKey) {
          if (ev.type === 'keydown') {
            sendToPty('\n');
          }
          // Block both keydown and keyup to prevent any leakage
          return false;
        }
        return true;
      });

      flushPendingTerminalEvents(id, terminal);

      pendingConnections.delete(id);
    } catch (e) {
      console.error('[Session] Spawn failed:', e);
      terminal.write(`\r\n[Failed to spawn PTY: ${e}]\r\n`);
      pendingConnections.delete(id);
    }
  },

  resizeSession: (id: string, cols: number, rows: number) => {
    ptyResize({ id, cols, rows }).catch(console.error);
  },

  setForkParams: (sessionId: string, resumeSessionId: string) => {
    pendingForkParams.set(sessionId, { resumeSessionId, forkSession: true });
  },
  setResumePicker: (sessionId: string) => {
    pendingForkParams.set(sessionId, { resumePicker: true });
  },

  setLauncherConfig: (config: LauncherConfig) => {
    set({ launcherConfig: config });
  },

  syncFromDaemonSessions: (daemonSessions: DaemonSessionSnapshot[]) => {
    set((state) => {
      const existingByID = new Map(state.sessions.map((session) => [session.id, session]));

      const syncedSessions = daemonSessions.map((daemonSession) => {
        const existing = existingByID.get(daemonSession.id);
        const normalizedState = normalizeSessionState(daemonSession.state);
        const nextAgent: SessionAgent = normalizeSessionAgent(daemonSession.agent, existing?.agent ?? 'codex');
        const nextBranch = daemonSession.branch ?? existing?.branch;
        const nextIsWorktree = daemonSession.is_worktree ?? existing?.isWorktree;

        if (
          existing &&
          existing.label === daemonSession.label &&
          existing.agent === nextAgent &&
          existing.cwd === daemonSession.directory &&
          existing.state === normalizedState &&
          existing.branch === nextBranch &&
          existing.isWorktree === nextIsWorktree
        ) {
          return existing;
        }

        return {
          id: daemonSession.id,
          label: daemonSession.label,
          state: normalizedState,
          terminal: existing?.terminal ?? null,
          cwd: daemonSession.directory,
          agent: nextAgent,
          transcriptMatched: existing?.transcriptMatched ?? nextAgent !== 'codex',
          branch: nextBranch,
          isWorktree: nextIsWorktree,
          terminalPanel: existing?.terminalPanel ?? createDefaultPanelState(),
        } satisfies Session;
      });

      let nextActiveSessionID = state.activeSessionId;
      if (nextActiveSessionID && !syncedSessions.some((session) => session.id === nextActiveSessionID)) {
        nextActiveSessionID = null;
      }

      return {
        sessions: syncedSessions,
        activeSessionId: nextActiveSessionID,
      };
    });
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

declare global {
  interface Window {
    __TEST_OPEN_TERMINAL_PANEL?: (sessionId: string) => void;
    __TEST_ADD_UTILITY_TERMINAL?: (sessionId: string, terminalId: string, title: string) => void;
    __TEST_COLLAPSE_TERMINAL_PANEL?: (sessionId: string) => void;
    __TEST_SET_ACTIVE_TERMINAL?: (sessionId: string, terminalId: string) => void;
    __TEST_REMOVE_TERMINAL?: (sessionId: string, terminalId: string) => void;
    __TEST_RENAME_TERMINAL?: (sessionId: string, terminalId: string, title: string) => void;
  }
}

// Expose test helpers for E2E testing (only in development)
if (import.meta.env.DEV) {
  window.__TEST_INJECT_SESSION = (session: TestSession) => {
    useSessionStore.setState((state) => ({
      sessions: [
        ...state.sessions,
        {
          ...session,
          agent: session.agent ?? 'codex',
          transcriptMatched: (session.agent ?? 'codex') !== 'codex',
          terminal: null,
          terminalPanel: createDefaultPanelState(),
        },
      ],
    }));
  };

  window.__TEST_UPDATE_SESSION_STATE = (id: string, state: 'working' | 'waiting_input' | 'idle' | 'pending_approval') => {
    useSessionStore.setState((s) => ({
      sessions: s.sessions.map((session) =>
        session.id === id ? { ...session, state } : session
      ),
    }));
  };

  window.__TEST_OPEN_TERMINAL_PANEL = (sessionId: string) => {
    useSessionStore.getState().openTerminalPanel(sessionId);
  };

  window.__TEST_COLLAPSE_TERMINAL_PANEL = (sessionId: string) => {
    useSessionStore.getState().collapseTerminalPanel(sessionId);
  };

  window.__TEST_ADD_UTILITY_TERMINAL = (sessionId: string, terminalId: string, title: string) => {
    useSessionStore.setState((state) => ({
      sessions: state.sessions.map((s) => {
        if (s.id !== sessionId) return s;
        const newTerminal = { id: terminalId, ptyId: `mock-pty-${terminalId}`, title };
        return {
          ...s,
          terminalPanel: {
            ...s.terminalPanel,
            terminals: [...s.terminalPanel.terminals, newTerminal],
            activeTabId: terminalId,
            isOpen: true,
          },
        };
      }),
    }));
  };

  window.__TEST_SET_ACTIVE_TERMINAL = (sessionId: string, terminalId: string) => {
    useSessionStore.getState().setActiveUtilityTerminal(sessionId, terminalId);
  };

  window.__TEST_REMOVE_TERMINAL = (sessionId: string, terminalId: string) => {
    useSessionStore.getState().removeUtilityTerminal(sessionId, terminalId);
  };

  window.__TEST_RENAME_TERMINAL = (sessionId: string, terminalId: string, title: string) => {
    useSessionStore.getState().renameUtilityTerminal(sessionId, terminalId, title);
  };
}
