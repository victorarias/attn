import { useEffect } from 'react';
import type { RefObject } from 'react';
import type { Session } from '../store/sessions';
import type { SessionTerminalWorkspaceHandle } from '../components/SessionTerminalWorkspace';
import { activeElementSummary } from '../utils/paneRuntimeDebug';

declare global {
  interface Window {
    __TEST_GET_SESSION_PANE_TEXT?: (sessionId: string) => string;
    __TEST_GET_ACTIVE_SESSION_PANE_TEXT?: () => string;
    __TEST_GET_ACTIVE_SESSION_PANE_RUNTIME?: (sessionId: string) => string | null;
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

    window.__TEST_GET_SESSION_PANE_TEXT = (sessionId: string) => {
      const session = sessions.find((entry) => entry.id === sessionId);
      const paneId = session?.workspace.agents.find((entry) => entry.sessionId === sessionId)?.id || '';
      return workspaceRefs.current.get(sessionId)?.getPaneText(paneId) || '';
    };

    window.__TEST_GET_ACTIVE_SESSION_PANE_TEXT = () => {
      if (!activeSessionId) {
        return '';
      }
      const session = sessions.find((entry) => entry.id === activeSessionId);
      const activePaneId = getActivePaneIdForSession(session);
      if (!session || !activePaneId) {
        return '';
      }
      return workspaceRefs.current.get(activeSessionId)?.getPaneText(activePaneId) || '';
    };

    window.__TEST_GET_ACTIVE_SESSION_PANE_RUNTIME = (sessionId: string) => {
      const session = sessions.find((entry) => entry.id === sessionId);
      const activePaneId = getActivePaneIdForSession(session);
      if (!session || !activePaneId) {
        return null;
      }
      return session.workspace.agents.find((entry) => entry.id === activePaneId)?.runtimeId ?? null;
    };

    window.__ATTN_PANE_DEBUG_STATE = () => ({
      activeSessionId,
      sessions: sessions.map((session) => ({
        id: session.id,
        label: session.label,
        activePaneId: getActivePaneIdForSession(session),
        daemonActivePaneId: session.daemonActivePaneId,
        paneIds: session.workspace.agents.map((agent) => agent.id),
      })),
      ...activeElementSummary(),
    });

    return () => {
      delete window.__TEST_GET_SESSION_PANE_TEXT;
      delete window.__TEST_GET_ACTIVE_SESSION_PANE_TEXT;
      delete window.__TEST_GET_ACTIVE_SESSION_PANE_RUNTIME;
      delete window.__ATTN_PANE_DEBUG_STATE;
    };
  }, [activeSessionId, getActivePaneIdForSession, sessions, workspaceRefs]);
}
