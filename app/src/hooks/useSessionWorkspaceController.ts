import { useCallback, useRef } from 'react';
import type { Session } from '../store/sessions';
import type { SessionTerminalWorkspaceHandle } from '../components/SessionTerminalWorkspace';
import { usePaneRuntimeEventRouter } from '../components/SessionTerminalWorkspace/paneRuntimeEventRouter';
import { useSessionWorkspaceViewState } from './useSessionWorkspaceViewState';
import { useWorkspaceDebugHarness } from './useWorkspaceDebugHarness';

interface SessionWorkspaceController {
  eventRouter: ReturnType<typeof usePaneRuntimeEventRouter>;
  getActivePaneIdForSession: (session: Session | undefined | null) => string;
  setActivePane: (sessionId: string, paneId: string) => void;
  setWorkspaceRef: (sessionId: string) => (ref: SessionTerminalWorkspaceHandle | null) => void;
  removeWorkspaceRef: (sessionId: string) => void;
  focusSessionPane: (sessionId: string, paneId: string, retries?: number) => void;
  typeInSessionPaneViaUI: (sessionId: string, paneId: string, text: string) => boolean;
  isSessionPaneInputFocused: (sessionId: string, paneId: string) => boolean;
  fitSessionActivePane: (sessionId: string) => void;
  getPaneText: (sessionId: string, paneId: string) => string;
  getPaneSize: (sessionId: string, paneId: string) => { cols: number; rows: number } | null;
}

export function useSessionWorkspaceController(
  sessions: Session[],
  activeSessionId: string | null
): SessionWorkspaceController {
  const workspaceRefs = useRef<Map<string, SessionTerminalWorkspaceHandle>>(new Map());
  const eventRouter = usePaneRuntimeEventRouter();
  const { getActivePaneIdForSession, setActivePane } = useSessionWorkspaceViewState(sessions);

  useWorkspaceDebugHarness({
    sessions,
    activeSessionId,
    workspaceRefs,
    getActivePaneIdForSession,
  });

  const setWorkspaceRef = useCallback(
    (sessionId: string) => (ref: SessionTerminalWorkspaceHandle | null) => {
      if (ref) {
        workspaceRefs.current.set(sessionId, ref);
        return;
      }
      workspaceRefs.current.delete(sessionId);
    },
    []
  );

  const removeWorkspaceRef = useCallback((sessionId: string) => {
    workspaceRefs.current.delete(sessionId);
  }, []);

  const focusSessionPane = useCallback((sessionId: string, paneId: string, retries = 20) => {
    workspaceRefs.current.get(sessionId)?.focusPane(paneId, retries);
  }, []);

  const typeInSessionPaneViaUI = useCallback((sessionId: string, paneId: string, text: string) => {
    return workspaceRefs.current.get(sessionId)?.typePaneTextViaUI(paneId, text) || false;
  }, []);

  const isSessionPaneInputFocused = useCallback((sessionId: string, paneId: string) => {
    return workspaceRefs.current.get(sessionId)?.isPaneInputFocused(paneId) || false;
  }, []);

  const fitSessionActivePane = useCallback((sessionId: string) => {
    workspaceRefs.current.get(sessionId)?.fitActivePane();
  }, []);

  const getPaneText = useCallback((sessionId: string, paneId: string) => {
    return workspaceRefs.current.get(sessionId)?.getPaneText(paneId) || '';
  }, []);

  const getPaneSize = useCallback((sessionId: string, paneId: string) => {
    return workspaceRefs.current.get(sessionId)?.getPaneSize(paneId) || null;
  }, []);

  return {
    eventRouter,
    getActivePaneIdForSession,
    setActivePane,
    setWorkspaceRef,
    removeWorkspaceRef,
    focusSessionPane,
    typeInSessionPaneViaUI,
    isSessionPaneInputFocused,
    fitSessionActivePane,
    getPaneText,
    getPaneSize,
  };
}
