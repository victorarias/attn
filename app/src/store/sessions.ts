import { create } from 'zustand';
import { Terminal } from '@xterm/xterm';
import type { UISessionState } from '../types/sessionState';
import { normalizeSessionState } from '../types/sessionState';
import type { SessionAgent } from '../types/sessionAgent';
import { normalizeSessionAgent } from '../types/sessionAgent';
import type { DaemonWorkspace } from '../hooks/useDaemonSocket';
import { listenPtyEvents, ptyKill, ptyResize, ptySpawn, ptyWrite, type PtyEventPayload } from '../pty/bridge';
import { triggerShortcut } from '../shortcuts/useShortcut';
import {
  MAIN_TERMINAL_PANE_ID,
  createDefaultPanelState,
  panelStateFromDaemonWorkspace,
  type TerminalPanelState,
} from '../types/workspace';

export { MAIN_TERMINAL_PANE_ID };
export type { TerminalPanelState };

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
  executables: Record<string, string>;
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
  reloadSession: (id: string) => Promise<void>;
  setForkParams: (sessionId: string, resumeSessionId: string) => void;
  setLauncherConfig: (config: LauncherConfig) => void;
  syncFromDaemonSessions: (daemonSessions: DaemonSessionSnapshot[]) => void;
  syncFromDaemonWorkspaces: (daemonWorkspaces: DaemonWorkspace[]) => void;
}

const pendingConnections = new Set<string>();
const pendingForkParams = new Map<string, { resumeSessionId?: string; forkSession?: boolean }>();
const pendingTerminalEvents = new Map<string, PtyEventPayload[]>();
const MAX_PENDING_TERMINAL_EVENTS = 256;
const MIN_STABLE_COLS = 20;
const MIN_STABLE_ROWS = 8;

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
    executables: {},
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
      state: 'launching',
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

    let cols = terminal.cols > 0 ? terminal.cols : 80;
    let rows = terminal.rows > 0 ? terminal.rows : 24;
    // Hidden terminals can report very small bootstrap sizes (e.g. 9x5).
    // Use sane defaults until a stable onResize arrives.
    if (cols < MIN_STABLE_COLS || rows < MIN_STABLE_ROWS) {
      cols = 80;
      rows = 24;
    }

    try {
      // Check for pending fork params
      const forkParams = pendingForkParams.get(id);
      pendingForkParams.delete(id);

      const selectedExecutable = launcherConfig.executables[session.agent] || '';
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
          ...(selectedExecutable ? { executable: selectedExecutable } : {}),
          // Backward compatibility fields for older daemons.
          ...(session.agent === 'claude' && selectedExecutable
            ? { claude_executable: selectedExecutable }
            : {}),
          ...(session.agent === 'codex' && selectedExecutable
            ? { codex_executable: selectedExecutable }
            : {}),
          ...(session.agent === 'copilot' && selectedExecutable
            ? { copilot_executable: selectedExecutable }
            : {}),
          ...(session.agent === 'pi' && selectedExecutable
            ? { pi_executable: selectedExecutable }
            : {}),
        },
      });

      // Terminal input -> PTY
      const sendToPty = (data: string) => {
        ptyWrite({ id, data, source: 'user' }).catch(console.error);
      };
      terminal.onData(sendToPty);

      // Shift+Enter sends newline for line breaks in Claude Code
      // Source: https://github.com/anthropics/claude-code/issues/1282
      terminal.attachCustomKeyEventHandler((ev) => {
        const accel = ev.metaKey || ev.ctrlKey;
        if (ev.type === 'keydown' && accel && !ev.altKey) {
          if (!ev.shiftKey && ev.key.toLowerCase() === 't') {
            return !triggerShortcut('terminal.new');
          }
          if (!ev.shiftKey && ev.key.toLowerCase() === 'd') {
            return !triggerShortcut('terminal.splitVertical');
          }
          if (ev.shiftKey && ev.key.toLowerCase() === 'd') {
            return !triggerShortcut('terminal.splitHorizontal');
          }
          if (ev.shiftKey && ev.key === 'Enter') {
            return !triggerShortcut('terminal.toggleMaximize');
          }
          if (!ev.shiftKey && ev.key.toLowerCase() === 'w') {
            return !triggerShortcut('terminal.close');
          }
        }
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

  reloadSession: async (id: string) => {
    const { sessions, launcherConfig } = get();
    const session = sessions.find((s) => s.id === id);
    if (!session) return;

    const terminal = session.terminal;
    let cols = terminal?.cols && terminal.cols > 0 ? terminal.cols : 80;
    let rows = terminal?.rows && terminal.rows > 0 ? terminal.rows : 24;
    if (cols < MIN_STABLE_COLS || rows < MIN_STABLE_ROWS) {
      cols = 80;
      rows = 24;
    }

    try {
      await ptyKill({ id });
    } catch (e) {
      console.warn('[Session] Reload kill failed, continuing to respawn:', e);
    }

    try {
      const selectedExecutable = launcherConfig.executables[session.agent] || '';
      await ptySpawn({
        args: {
          id,
          cwd: session.cwd,
          label: session.label,
          cols,
          rows,
          shell: false,
          agent: session.agent,
          ...(selectedExecutable ? { executable: selectedExecutable } : {}),
          ...(session.agent === 'claude' && selectedExecutable
            ? { claude_executable: selectedExecutable }
            : {}),
          ...(session.agent === 'codex' && selectedExecutable
            ? { codex_executable: selectedExecutable }
            : {}),
          ...(session.agent === 'copilot' && selectedExecutable
            ? { copilot_executable: selectedExecutable }
            : {}),
          ...(session.agent === 'pi' && selectedExecutable
            ? { pi_executable: selectedExecutable }
            : {}),
        },
      });
    } catch (e) {
      console.error('[Session] Reload spawn failed:', e);
      terminal?.write(`\r\n[Failed to reload PTY: ${e}]\r\n`);
    }
  },

  setForkParams: (sessionId: string, resumeSessionId: string) => {
    pendingForkParams.set(sessionId, { resumeSessionId, forkSession: true });
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

  syncFromDaemonWorkspaces: (daemonWorkspaces: DaemonWorkspace[]) => {
    const panelBySessionID = new Map(daemonWorkspaces.map((workspace) => [
      workspace.session_id,
      panelStateFromDaemonWorkspace(workspace),
    ]));

    set((state) => ({
      sessions: state.sessions.map((session) => ({
        ...session,
        terminalPanel: panelBySessionID.get(session.id) ?? session.terminalPanel,
      })),
    }));
  },
}));

declare global {
  interface Window {
    __TEST_GET_ACTIVE_UTILITY_PTY?: (sessionId: string) => string | null;
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

  window.__TEST_UPDATE_SESSION_STATE = (id: string, state: UISessionState) => {
    useSessionStore.setState((s) => ({
      sessions: s.sessions.map((session) =>
        session.id === id ? { ...session, state } : session
      ),
    }));
  };

  window.__TEST_GET_ACTIVE_UTILITY_PTY = (sessionId: string) => {
    const session = useSessionStore.getState().sessions.find((entry) => entry.id === sessionId);
    if (!session || session.terminalPanel.activePaneId === MAIN_TERMINAL_PANE_ID) {
      return null;
    }
    const active = session.terminalPanel.terminals.find((terminal) => terminal.id === session.terminalPanel.activePaneId);
    return active?.ptyId ?? null;
  };
}
