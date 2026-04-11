import { create } from 'zustand';
import type { UISessionState } from '../types/sessionState';
import { normalizeSessionState } from '../types/sessionState';
import type { SessionAgent } from '../types/sessionAgent';
import { normalizeSessionAgent } from '../types/sessionAgent';
import type { DaemonWorkspace } from '../hooks/useDaemonSocket';
import { listenPtyEvents, ptyKill, ptySpawn, type PtySpawnArgs } from '../pty/bridge';
import {
  MAIN_TERMINAL_PANE_ID,
  createDefaultWorkspaceState,
  workspaceSnapshotFromDaemonWorkspace,
  type TerminalWorkspaceState,
} from '../types/workspace';

export { MAIN_TERMINAL_PANE_ID };
export type { TerminalWorkspaceState };

export interface Session {
  id: string;
  label: string;
  state: UISessionState;
  cwd: string;
  agent: SessionAgent;
  endpointId?: string;
  yoloMode?: boolean;
  transcriptMatched: boolean;
  branch?: string;
  isWorktree?: boolean;
  workspace: TerminalWorkspaceState;
  daemonActivePaneId: string;
}

export interface DaemonSessionSnapshot {
  id: string;
  label: string;
  agent?: string;
  directory: string;
  endpoint_id?: string;
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
  createSession: (label: string, cwd: string, id?: string, agent?: SessionAgent, endpointId?: string, yoloMode?: boolean) => Promise<string>;
  closeSession: (id: string) => void;
  removeSessionLocalState: (id: string) => void;
  setActiveSession: (id: string | null) => void;
  takeSessionSpawnArgs: (id: string, cols: number, rows: number) => PtySpawnArgs | null;
  reloadSession: (id: string, size?: { cols: number; rows: number }) => Promise<void>;
  setForkParams: (sessionId: string, resumeSessionId: string) => void;
  setLauncherConfig: (config: LauncherConfig) => void;
  syncFromDaemonSessions: (daemonSessions: DaemonSessionSnapshot[]) => void;
  syncFromDaemonWorkspaces: (daemonWorkspaces: DaemonWorkspace[]) => void;
}

const pendingForkParams = new Map<string, { resumeSessionId?: string; forkSession?: boolean }>();
const MIN_STABLE_COLS = 20;
const MIN_STABLE_ROWS = 8;

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
    __TEST_SET_SESSION_WORKSPACE?: (sessionId: string, workspace: TerminalWorkspaceState, daemonActivePaneId?: string) => void;
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

        if (msg.event === 'transcript') {
          if (typeof msg.matched === 'boolean') {
            const updated = sessions.map((entry) =>
              entry.id === msg.id ? { ...entry, transcriptMatched: msg.matched } : entry,
            );
            set({ sessions: updated });
          }
          return;
        }
      });

      set({ connected: true });
    } catch (e) {
      console.error('[Session] Connect failed:', e);
    }
  },

  createSession: async (label: string, cwd: string, providedId?: string, agent?: SessionAgent, endpointId?: string, yoloMode = false) => {
    // Use provided ID or generate new one
    const id = providedId || crypto.randomUUID();
    const resolvedAgent: SessionAgent = agent ?? 'claude';
    const session: Session = {
      id,
      label,
      state: 'launching',
      cwd,
      agent: resolvedAgent,
      endpointId,
      yoloMode,
      transcriptMatched: resolvedAgent !== 'codex',
      workspace: createDefaultWorkspaceState(),
      daemonActivePaneId: MAIN_TERMINAL_PANE_ID,
    };

    set((state) => ({
      sessions: [...state.sessions, session],
      activeSessionId: id,
    }));

    return id;
  },

  removeSessionLocalState: (id: string) => {
    const { sessions, activeSessionId } = get();
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

  closeSession: (id: string) => {
    const { sessions, removeSessionLocalState } = get();
    const session = sessions.find((s) => s.id === id);

    if (session) {
      // Legacy local-only fallback for sessions that never became daemon-managed.
      ptyKill({ id }).catch(console.error);
    }

    removeSessionLocalState(id);
  },

  setActiveSession: (id: string | null) => {
    set({ activeSessionId: id });
  },

  takeSessionSpawnArgs: (id: string, cols: number, rows: number) => {
    const { sessions, launcherConfig } = get();
    const session = sessions.find((entry) => entry.id === id);
    if (!session) {
      return null;
    }
    let resolvedCols = cols > 0 ? cols : 80;
    let resolvedRows = rows > 0 ? rows : 24;
    if (resolvedCols < MIN_STABLE_COLS || resolvedRows < MIN_STABLE_ROWS) {
      resolvedCols = 80;
      resolvedRows = 24;
    }
    const forkParams = pendingForkParams.get(id);
    pendingForkParams.delete(id);
    const selectedExecutable = launcherConfig.executables[session.agent] || '';
    return {
      id,
      cwd: session.cwd,
      ...(session.endpointId ? { endpoint_id: session.endpointId } : {}),
      label: session.label,
      cols: resolvedCols,
      rows: resolvedRows,
      shell: false,
      agent: session.agent,
      resume_session_id: forkParams?.resumeSessionId ?? null,
      fork_session: forkParams?.forkSession ?? null,
      yolo_mode: session.yoloMode ?? null,
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
    };
  },

  reloadSession: async (id: string, size?: { cols: number; rows: number }) => {
    const { sessions, launcherConfig } = get();
    const session = sessions.find((s) => s.id === id);
    if (!session) return;
    let cols = size?.cols && size.cols > 0 ? size.cols : 80;
    let rows = size?.rows && size.rows > 0 ? size.rows : 24;
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
          ...(session.endpointId ? { endpoint_id: session.endpointId } : {}),
          reload: true,
          label: session.label,
          cols,
          rows,
          shell: false,
          agent: session.agent,
          yolo_mode: session.yoloMode ?? null,
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
      throw e;
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
        const nextEndpointId = daemonSession.endpoint_id ?? existing?.endpointId;
        const nextBranch = daemonSession.branch ?? existing?.branch;
        const nextIsWorktree = daemonSession.is_worktree ?? existing?.isWorktree;

        if (
          existing &&
          existing.label === daemonSession.label &&
          existing.agent === nextAgent &&
          existing.cwd === daemonSession.directory &&
          existing.endpointId === nextEndpointId &&
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
          cwd: daemonSession.directory,
          agent: nextAgent,
          endpointId: nextEndpointId,
          yoloMode: existing?.yoloMode,
          transcriptMatched: existing?.transcriptMatched ?? nextAgent !== 'codex',
          branch: nextBranch,
          isWorktree: nextIsWorktree,
          workspace: existing?.workspace ?? createDefaultWorkspaceState(),
          daemonActivePaneId: existing?.daemonActivePaneId ?? MAIN_TERMINAL_PANE_ID,
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
    const workspaceBySessionID = new Map(daemonWorkspaces.map((workspace) => [
      workspace.session_id,
      workspaceSnapshotFromDaemonWorkspace(workspace),
    ]));

    set((state) => ({
      sessions: state.sessions.map((session) => ({
        ...session,
        workspace: workspaceBySessionID.get(session.id)?.workspace ?? session.workspace,
        daemonActivePaneId: workspaceBySessionID.get(session.id)?.daemonActivePaneId ?? session.daemonActivePaneId,
      })),
    }));
  },
}));

declare global {
  interface Window {
    __TEST_GET_SESSION_INPUT_EVENTS?: (sessionId: string) => Array<{ event: 'connect_terminal' | 'send_to_pty'; data?: string }>;
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
          workspace: createDefaultWorkspaceState(),
          daemonActivePaneId: MAIN_TERMINAL_PANE_ID,
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

  window.__TEST_SET_SESSION_WORKSPACE = (sessionId: string, workspace: TerminalWorkspaceState, daemonActivePaneId = MAIN_TERMINAL_PANE_ID) => {
    useSessionStore.setState((state) => ({
      sessions: state.sessions.map((session) =>
        session.id === sessionId
          ? { ...session, workspace, daemonActivePaneId }
          : session
      ),
    }));
  };

  window.__TEST_GET_SESSION_INPUT_EVENTS = (sessionId: string) =>
    ((window as Window & {
      __TEST_SESSION_INPUT_EVENTS?: Array<{ sessionId: string; event: 'connect_terminal' | 'send_to_pty'; data?: string }>;
    }).__TEST_SESSION_INPUT_EVENTS || [])
      .filter((entry) => entry.sessionId === sessionId)
      .map(({ event, data }) => ({ event, data }));
}
