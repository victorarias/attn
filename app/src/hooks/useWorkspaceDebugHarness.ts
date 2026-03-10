import { useEffect } from 'react';
import type { RefObject } from 'react';
import type { Session } from '../store/sessions';
import { MAIN_TERMINAL_PANE_ID } from '../store/sessions';
import type { SessionTerminalWorkspaceHandle } from '../components/SessionTerminalWorkspace';
import { activeElementSummary } from '../utils/paneRuntimeDebug';

declare global {
  interface Window {
    __TEST_GET_MAIN_TERMINAL_TEXT?: (sessionId: string) => string;
    __TEST_GET_ACTIVE_UTILITY_TEXT?: () => string;
    __TEST_GET_ACTIVE_UTILITY_PTY?: (sessionId: string) => string | null;
    __ATTN_PANE_DEBUG_STATE?: () => Record<string, unknown>;
  }
}

interface UseWorkspaceDebugHarnessArgs {
  sessions: Session[];
  activeSessionId: string | null;
  workspaceRefs: RefObject<Map<string, SessionTerminalWorkspaceHandle>>;
  getActivePaneIdForSession: (session: Session | undefined | null) => string;
}

export function useWorkspaceDebugHarness({
  sessions,
  activeSessionId,
  workspaceRefs,
  getActivePaneIdForSession,
}: UseWorkspaceDebugHarnessArgs) {
  useEffect(() => {
    if (!import.meta.env.DEV) {
      return;
    }

    window.__TEST_GET_MAIN_TERMINAL_TEXT = (sessionId: string) => {
      return workspaceRefs.current.get(sessionId)?.getPaneText(MAIN_TERMINAL_PANE_ID) || '';
    };

    window.__TEST_GET_ACTIVE_UTILITY_TEXT = () => {
      if (!activeSessionId) {
        return '';
      }
      const session = sessions.find((entry) => entry.id === activeSessionId);
      const activePaneId = getActivePaneIdForSession(session);
      if (!session || activePaneId === MAIN_TERMINAL_PANE_ID) {
        return '';
      }
      return workspaceRefs.current.get(activeSessionId)?.getPaneText(activePaneId) || '';
    };

    window.__TEST_GET_ACTIVE_UTILITY_PTY = (sessionId: string) => {
      const session = sessions.find((entry) => entry.id === sessionId);
      const activePaneId = getActivePaneIdForSession(session);
      if (!session || activePaneId === MAIN_TERMINAL_PANE_ID) {
        return null;
      }
      const terminal = session.workspace.terminals.find((entry) => entry.id === activePaneId);
      return terminal?.ptyId ?? null;
    };

    window.__ATTN_PANE_DEBUG_STATE = () => ({
      activeSessionId,
      sessions: sessions.map((session) => ({
        id: session.id,
        label: session.label,
        activePaneId: getActivePaneIdForSession(session),
        daemonActivePaneId: session.daemonActivePaneId,
        paneIds: [MAIN_TERMINAL_PANE_ID, ...session.workspace.terminals.map((terminal) => terminal.id)],
      })),
      ...activeElementSummary(),
    });

    return () => {
      delete window.__TEST_GET_MAIN_TERMINAL_TEXT;
      delete window.__TEST_GET_ACTIVE_UTILITY_TEXT;
      delete window.__TEST_GET_ACTIVE_UTILITY_PTY;
      delete window.__ATTN_PANE_DEBUG_STATE;
    };
  }, [activeSessionId, getActivePaneIdForSession, sessions, workspaceRefs]);
}
