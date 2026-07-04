import { create } from 'zustand';
import type { UISessionState } from '../types/sessionState';
import { normalizeSessionState } from '../types/sessionState';
import type { SessionAgent } from '../types/sessionAgent';
import { normalizeSessionAgent } from '../types/sessionAgent';
import type { DaemonWorkspace } from '../hooks/useDaemonSocket';
import { listenPtyEvents, ptyKill, ptySpawn, type PtySpawnArgs } from '../pty/bridge';
import {
  createDefaultWorkspaceState,
  workspaceSnapshotFromDaemonWorkspace,
  type TerminalWorkspaceState,
} from '../types/workspace';

export type { TerminalWorkspaceState };

// Sessions whose runtime is being reloaded in place (kill → respawn of the same
// id). The kill's exit event can look like a clean voluntary quit (code 0, no
// signal), which would trip the auto-close-on-clean-exit path in App.tsx and
// tear down the pane/workspace out from under the pending respawn. Consumers
// (the session-exit handler) check this before treating an exit as end-of-life.
const reloadingSessionIds = new Set<string>();

export function isSessionReloading(id: string): boolean {
  return reloadingSessionIds.has(id);
}

export interface Session {
  id: string;
  label: string;
  state: UISessionState;
  cwd: string;
  workspaceId: string;
  agent: SessionAgent;
  endpointId?: string;
  yoloMode?: boolean;
  // chiefOfStaff requests that this session be launched already holding the
  // chief-of-staff role so the notebook guidance is injected on its first boot
  // (the post-launch promote path can't resume a zero-turn session). Set only at
  // creation via the new-session dialog's "create as chief" toggle.
  chiefOfStaff?: boolean;
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
  workspace_id: string;
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
  // Previously-active session IDs, most recent first. Used to restore
  // selection when the active session disappears.
  recentSessionIds: string[];
  connected: boolean;
  launcherConfig: LauncherConfig;

  // Actions
  connect: () => Promise<void>;
  createSession: (
    label: string,
    cwd: string,
    id: string | undefined,
    agent: SessionAgent | undefined,
    endpointId: string | undefined,
    yoloMode: boolean | undefined,
    workspaceId: string,
    chiefOfStaff?: boolean,
  ) => Promise<string>;
  closeSession: (id: string) => void;
  removeSessionLocalState: (id: string) => void;
  setActiveSession: (id: string | null) => void;
  takeSessionSpawnArgs: (id: string, cols: number, rows: number) => PtySpawnArgs | null;
  reloadSession: (id: string, size?: { cols: number; rows: number }) => Promise<void>;
  setLauncherConfig: (config: LauncherConfig) => void;
  syncFromDaemonSessions: (daemonSessions: DaemonSessionSnapshot[]) => void;
  syncFromDaemonWorkspaces: (daemonWorkspaces: DaemonWorkspace[]) => void;
}

const MIN_STABLE_COLS = 20;
const MIN_STABLE_ROWS = 8;

// Test helper for E2E - allows injecting sessions without PTY
interface TestSession {
  id: string;
  label: string;
  state: UISessionState;
  cwd: string;
  agent?: SessionAgent;
  workspaceId: string;
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

function pushRecent(recent: string[], id: string | null): string[] {
  if (!id) return recent;
  const filtered = recent.filter((entry) => entry !== id);
  filtered.unshift(id);
  return filtered;
}

function pickFallbackActive(
  removedId: string,
  remainingSessions: Session[],
  recent: string[],
  removedSession?: Session | null,
): string | null {
  const existing = new Set(remainingSessions.map((entry) => entry.id));
  for (const candidate of recent) {
    if (candidate !== removedId && existing.has(candidate)) {
      return candidate;
    }
  }
  if (removedSession?.workspaceId) {
    const sameWorkspace = remainingSessions.find((entry) => entry.workspaceId === removedSession.workspaceId);
    if (sameWorkspace) {
      return sameWorkspace.id;
    }
  }
  return remainingSessions[0]?.id ?? null;
}

export const useSessionStore = create<SessionStore>((set, get) => ({
  sessions: [],
  activeSessionId: null,
  recentSessionIds: [],
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

  createSession: async (
    label: string,
    cwd: string,
    providedId: string | undefined,
    agent: SessionAgent | undefined,
    endpointId: string | undefined,
    yoloMode: boolean | undefined,
    providedWorkspaceId: string,
    chiefOfStaff?: boolean,
  ) => {
    // Use provided ID or generate new one
    const id = providedId || crypto.randomUUID();
    if (!providedWorkspaceId) {
      throw new Error('createSession requires workspaceId');
    }
    const workspaceId = providedWorkspaceId;
    const resolvedAgent: SessionAgent = agent ?? 'claude';
    const session: Session = {
      id,
      label,
      state: 'launching',
      cwd,
      workspaceId,
      agent: resolvedAgent,
      endpointId,
      yoloMode: yoloMode ?? false,
      chiefOfStaff: chiefOfStaff ?? false,
      transcriptMatched: resolvedAgent !== 'codex',
      workspace: createDefaultWorkspaceState(),
      daemonActivePaneId: '',
    };

    set((state) => ({
      sessions: [...state.sessions, session],
      activeSessionId: id,
      recentSessionIds:
        state.activeSessionId && state.activeSessionId !== id
          ? pushRecent(state.recentSessionIds, state.activeSessionId)
          : state.recentSessionIds.filter((entry) => entry !== id),
    }));

    return id;
  },

  removeSessionLocalState: (id: string) => {
    const { sessions, activeSessionId, recentSessionIds } = get();
    const removedSession = sessions.find((session) => session.id === id) ?? null;
    const newSessions = sessions.filter((s) => s.id !== id);
    const newRecent = recentSessionIds.filter((entry) => entry !== id);
    let newActiveId = activeSessionId;

    if (activeSessionId === id) {
      newActiveId = pickFallbackActive(id, newSessions, recentSessionIds, removedSession);
    }

    set({
      sessions: newSessions,
      activeSessionId: newActiveId,
      recentSessionIds: newRecent,
    });
  },

  closeSession: (id: string) => {
    get().removeSessionLocalState(id);
  },

  setActiveSession: (id: string | null) => {
    set((state) => {
      if (state.activeSessionId === id) {
        return state;
      }
      const nextRecent = pushRecent(state.recentSessionIds, state.activeSessionId);
      return {
        activeSessionId: id,
        recentSessionIds: id ? nextRecent.filter((entry) => entry !== id) : nextRecent,
      };
    });
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
    const selectedExecutable = launcherConfig.executables[session.agent] || '';
    return {
      id,
      cwd: session.cwd,
      workspace_id: session.workspaceId,
      ...(session.endpointId ? { endpoint_id: session.endpointId } : {}),
      intent: 'create',
      label: session.label,
      cols: resolvedCols,
      rows: resolvedRows,
      shell: false,
      agent: session.agent,
      resume_session_id: null,
      yolo_mode: session.yoloMode ?? null,
      ...(session.chiefOfStaff ? { chief_of_staff: true } : {}),
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

    reloadingSessionIds.add(id);
    try {
      // reload:true tells the daemon this kill is a lifecycle transition (the
      // same id respawns just below), not a crash — bound tickets stay put.
      await ptyKill({ id, reload: true });
    } catch (e) {
      console.warn('[Session] Reload kill failed, continuing to respawn:', e);
    }

    try {
      const selectedExecutable = launcherConfig.executables[session.agent] || '';
      await ptySpawn({
        args: {
          id,
          cwd: session.cwd,
          workspace_id: session.workspaceId,
          ...(session.endpointId ? { endpoint_id: session.endpointId } : {}),
          intent: 'reload',
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
        },
      });
    } catch (e) {
      console.error('[Session] Reload spawn failed:', e);
      throw e;
    } finally {
      reloadingSessionIds.delete(id);
    }
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
        const nextWorkspaceId = daemonSession.workspace_id;
        const nextBranch = daemonSession.branch ?? existing?.branch;
        const nextIsWorktree = daemonSession.is_worktree ?? existing?.isWorktree;

        if (
          existing &&
          existing.label === daemonSession.label &&
          existing.agent === nextAgent &&
          existing.cwd === daemonSession.directory &&
          existing.workspaceId === nextWorkspaceId &&
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
          workspaceId: nextWorkspaceId,
          agent: nextAgent,
          endpointId: nextEndpointId,
          yoloMode: existing?.yoloMode,
          transcriptMatched: existing?.transcriptMatched ?? nextAgent !== 'codex',
          branch: nextBranch,
          isWorktree: nextIsWorktree,
          workspace: existing?.workspace ?? createDefaultWorkspaceState(),
          daemonActivePaneId: existing?.daemonActivePaneId ?? '',
        } satisfies Session;
      });

      const pendingLayoutSessions = state.sessions.filter((session) => (
        !syncedSessions.some((synced) => synced.id === session.id)
        && session.state === 'launching'
        && session.workspace.agents.some((pane) => pane.sessionId === session.id && pane.status === 'spawning')
      ));
      const allSessions = [...syncedSessions, ...pendingLayoutSessions];
      const syncedIds = new Set(allSessions.map((session) => session.id));
      const prunedRecent = state.recentSessionIds.filter((entry) => syncedIds.has(entry));

      let nextActiveSessionID = state.activeSessionId;
      let nextRecent = prunedRecent;
      if (nextActiveSessionID && !syncedIds.has(nextActiveSessionID)) {
        const removedSession = state.sessions.find((session) => session.id === nextActiveSessionID) ?? null;
        const fallback = pickFallbackActive(nextActiveSessionID, allSessions, prunedRecent, removedSession);
        nextActiveSessionID = fallback;
        nextRecent = fallback
          ? prunedRecent.filter((entry) => entry !== fallback)
          : prunedRecent;
      }

      return {
        sessions: allSessions,
        activeSessionId: nextActiveSessionID,
        recentSessionIds: nextRecent,
      };
    });
  },

  syncFromDaemonWorkspaces: (daemonWorkspaces: DaemonWorkspace[]) => {
    const workspaceByID = new Map(daemonWorkspaces
      .filter((workspace) => workspace.layout)
      .map((workspace) => [
        workspace.id,
        workspaceSnapshotFromDaemonWorkspace(workspace.layout!),
      ]));

    set((state) => {
      const sessions = state.sessions.map((session) => ({
        ...session,
        workspace: workspaceByID.get(session.workspaceId)?.workspace ?? session.workspace,
        daemonActivePaneId: workspaceByID.get(session.workspaceId)?.daemonActivePaneId ?? session.daemonActivePaneId,
      }));
      const existingIDs = new Set(sessions.map((session) => session.id));

      for (const workspace of daemonWorkspaces) {
        if (!workspace.layout) {
          continue;
        }
        const snapshot = workspaceByID.get(workspace.id);
        if (!snapshot) {
          continue;
        }
        for (const pane of snapshot.workspace.agents) {
          if (existingIDs.has(pane.sessionId) || pane.status === 'ready') {
            continue;
          }
          existingIDs.add(pane.sessionId);
          sessions.push({
            id: pane.sessionId,
            label: pane.title || workspace.title,
            state: pane.status === 'failed' ? 'unknown' : 'launching',
            cwd: workspace.directory,
            workspaceId: workspace.id,
            agent: 'codex',
            endpointId: workspace.endpoint_id,
            transcriptMatched: true,
            workspace: snapshot.workspace,
            daemonActivePaneId: snapshot.daemonActivePaneId,
          });
        }
      }

      return { sessions };
    });
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
    if (!session.workspaceId) {
      throw new Error('__TEST_INJECT_SESSION requires workspaceId');
    }
    const workspaceId = session.workspaceId;
    useSessionStore.setState((state) => ({
      sessions: [
        ...state.sessions,
        {
          ...session,
          workspaceId,
          agent: session.agent ?? 'codex',
          transcriptMatched: (session.agent ?? 'codex') !== 'codex',
          workspace: createDefaultWorkspaceState(),
          daemonActivePaneId: '',
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

  window.__TEST_SET_SESSION_WORKSPACE = (sessionId: string, workspace: TerminalWorkspaceState, daemonActivePaneId = workspace.agents[0]?.id || '') => {
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
