import { useCallback, useRef } from 'react';
import type { Session } from '../store/sessions';
import type { SessionTerminalWorkspaceHandle } from '../components/SessionTerminalWorkspace';
import { usePaneRuntimeEventRouter } from '../components/SessionTerminalWorkspace/paneRuntimeEventRouter';
import { useSessionWorkspaceViewState } from './useSessionWorkspaceViewState';
import { useWorkspaceDebugHarness } from './useWorkspaceDebugHarness';
import {
  snapshotVisibleTerminalContent,
  type TerminalVisibleContentSnapshot,
} from '../utils/terminalVisibleContent';
import {
  emptyTerminalVisibleStyleSnapshot,
  type TerminalVisibleStyleSnapshot,
} from '../utils/terminalStyleSummary';

interface SessionWorkspaceController {
  eventRouter: ReturnType<typeof usePaneRuntimeEventRouter>;
  getActivePaneIdForSession: (session: Session | undefined | null) => string;
  setActivePane: (sessionId: string, paneId: string) => void;
  prepareClosePaneFocus: (sessionId: string, paneId: string) => string;
  clearPreparedClosePaneFocus: (sessionId: string) => void;
  setWorkspaceRef: (workspaceId: string) => (ref: SessionTerminalWorkspaceHandle | null) => void;
  removeWorkspaceRef: (workspaceId: string) => void;
  focusSessionPane: (sessionId: string, paneId: string, retries?: number) => void;
  typeInSessionPaneViaUI: (sessionId: string, paneId: string, text: string) => boolean;
  isSessionPaneInputFocused: (sessionId: string, paneId: string) => boolean;
  scrollSessionPaneToTop: (sessionId: string, paneId: string) => boolean;
  fitSessionActivePane: (sessionId: string) => void;
  getPaneText: (sessionId: string, paneId: string) => string;
  getPaneSize: (sessionId: string, paneId: string) => { cols: number; rows: number } | null;
  getPaneVisibleContent: (sessionId: string, paneId: string) => TerminalVisibleContentSnapshot;
  getPaneVisibleStyleSummary: (sessionId: string, paneId: string) => TerminalVisibleStyleSnapshot;
  resetSessionPaneTerminal: (sessionId: string, paneId: string) => boolean;
  injectSessionPaneBytes: (sessionId: string, paneId: string, bytes: Uint8Array) => Promise<boolean>;
  injectSessionPaneBase64: (sessionId: string, paneId: string, payload: string) => Promise<boolean>;
  drainSessionPaneTerminal: (sessionId: string, paneId: string) => Promise<boolean>;
}

export function useSessionWorkspaceController(
  sessions: Session[],
  activeSessionId: string | null
): SessionWorkspaceController {
  const workspaceRefs = useRef<Map<string, SessionTerminalWorkspaceHandle>>(new Map());
  const eventRouter = usePaneRuntimeEventRouter();
  const {
    getActivePaneIdForSession,
    setActivePane,
    prepareClosePaneFocus: prepareClosePaneFocusForSession,
    clearPreparedClosePaneFocus,
  } = useSessionWorkspaceViewState(sessions);

  useWorkspaceDebugHarness({
    sessions,
    activeSessionId,
    workspaceRefs,
    getActivePaneIdForSession,
  });

  const workspaceIdForSession = useCallback((sessionId: string) => {
    return sessions.find((entry) => entry.id === sessionId)?.workspaceId || sessionId;
  }, [sessions]);

  const setWorkspaceRef = useCallback(
    (workspaceId: string) => (ref: SessionTerminalWorkspaceHandle | null) => {
      if (ref) {
        workspaceRefs.current.set(workspaceId, ref);
        return;
      }
      workspaceRefs.current.delete(workspaceId);
    },
    []
  );

  const removeWorkspaceRef = useCallback((workspaceId: string) => {
    workspaceRefs.current.delete(workspaceId);
  }, []);

  const prepareClosePaneFocus = useCallback((sessionId: string, paneId: string) => {
    const session = sessions.find((entry) => entry.id === sessionId);
    return prepareClosePaneFocusForSession(session, paneId);
  }, [prepareClosePaneFocusForSession, sessions]);

  const focusSessionPane = useCallback((sessionId: string, paneId: string, retries = 20) => {
    workspaceRefs.current.get(workspaceIdForSession(sessionId))?.focusPane(paneId, retries);
  }, [workspaceIdForSession]);

  const typeInSessionPaneViaUI = useCallback((sessionId: string, paneId: string, text: string) => {
    return workspaceRefs.current.get(workspaceIdForSession(sessionId))?.typePaneTextViaUI(paneId, text) || false;
  }, [workspaceIdForSession]);

  const isSessionPaneInputFocused = useCallback((sessionId: string, paneId: string) => {
    return workspaceRefs.current.get(workspaceIdForSession(sessionId))?.isPaneInputFocused(paneId) || false;
  }, [workspaceIdForSession]);

  const scrollSessionPaneToTop = useCallback((sessionId: string, paneId: string) => {
    return workspaceRefs.current.get(workspaceIdForSession(sessionId))?.scrollPaneToTop(paneId) || false;
  }, [workspaceIdForSession]);

  const fitSessionActivePane = useCallback((sessionId: string) => {
    workspaceRefs.current.get(workspaceIdForSession(sessionId))?.fitActivePane();
  }, [workspaceIdForSession]);

  const getPaneText = useCallback((sessionId: string, paneId: string) => {
    return workspaceRefs.current.get(workspaceIdForSession(sessionId))?.getPaneText(paneId) || '';
  }, [workspaceIdForSession]);

  const getPaneSize = useCallback((sessionId: string, paneId: string) => {
    return workspaceRefs.current.get(workspaceIdForSession(sessionId))?.getPaneSize(paneId) || null;
  }, [workspaceIdForSession]);

  const getPaneVisibleContent = useCallback((sessionId: string, paneId: string) => {
    return workspaceRefs.current.get(workspaceIdForSession(sessionId))?.getPaneVisibleContent(paneId)
      || snapshotVisibleTerminalContent(null);
  }, [workspaceIdForSession]);

  const getPaneVisibleStyleSummary = useCallback((sessionId: string, paneId: string) => {
    return workspaceRefs.current.get(workspaceIdForSession(sessionId))?.getPaneVisibleStyleSummary(paneId)
      || emptyTerminalVisibleStyleSnapshot();
  }, [workspaceIdForSession]);

  const resetSessionPaneTerminal = useCallback((sessionId: string, paneId: string) => {
    return workspaceRefs.current.get(workspaceIdForSession(sessionId))?.resetPaneTerminal(paneId) || false;
  }, [workspaceIdForSession]);

  const injectSessionPaneBytes = useCallback((sessionId: string, paneId: string, bytes: Uint8Array) => {
    return workspaceRefs.current.get(workspaceIdForSession(sessionId))?.injectPaneBytes(paneId, bytes) || Promise.resolve(false);
  }, [workspaceIdForSession]);

  const injectSessionPaneBase64 = useCallback((sessionId: string, paneId: string, payload: string) => {
    return workspaceRefs.current.get(workspaceIdForSession(sessionId))?.injectPaneBase64(paneId, payload) || Promise.resolve(false);
  }, [workspaceIdForSession]);

  const drainSessionPaneTerminal = useCallback((sessionId: string, paneId: string) => {
    return workspaceRefs.current.get(workspaceIdForSession(sessionId))?.drainPaneTerminal(paneId) || Promise.resolve(false);
  }, [workspaceIdForSession]);

  return {
    eventRouter,
    getActivePaneIdForSession,
    setActivePane,
    prepareClosePaneFocus,
    clearPreparedClosePaneFocus,
    setWorkspaceRef,
    removeWorkspaceRef,
    focusSessionPane,
    typeInSessionPaneViaUI,
    isSessionPaneInputFocused,
    scrollSessionPaneToTop,
    fitSessionActivePane,
    getPaneText,
    getPaneSize,
    getPaneVisibleContent,
    getPaneVisibleStyleSummary,
    resetSessionPaneTerminal,
    injectSessionPaneBytes,
    injectSessionPaneBase64,
    drainSessionPaneTerminal,
  };
}
