import { useCallback, useRef } from 'react';
import type { Session } from '../store/sessions';
import type { SessionTerminalWorkspaceHandle } from '../components/SessionTerminalWorkspace';
import type { LeafDropSnapshot } from '../components/SessionTerminalWorkspace/leafDrag';
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
  getWorkspaceLeafDropSnapshot: (workspaceId: string | null | undefined) => LeafDropSnapshot | null;
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
    return sessions.find((entry) => entry.id === sessionId)?.workspaceId ?? null;
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

  const getWorkspaceLeafDropSnapshot = useCallback((workspaceId: string | null | undefined) => (
    workspaceId ? workspaceRefs.current.get(workspaceId)?.getLeafDropSnapshot() ?? null : null
  ), []);

  const prepareClosePaneFocus = useCallback((sessionId: string, paneId: string) => {
    const session = sessions.find((entry) => entry.id === sessionId);
    return prepareClosePaneFocusForSession(session, paneId);
  }, [prepareClosePaneFocusForSession, sessions]);

  const focusSessionPane = useCallback((sessionId: string, paneId: string, retries = 20) => {
    const workspaceId = workspaceIdForSession(sessionId);
    if (!workspaceId) return;
    workspaceRefs.current.get(workspaceId)?.focusPane(paneId, retries);
  }, [workspaceIdForSession]);

  const typeInSessionPaneViaUI = useCallback((sessionId: string, paneId: string, text: string) => {
    const workspaceId = workspaceIdForSession(sessionId);
    return workspaceId ? workspaceRefs.current.get(workspaceId)?.typePaneTextViaUI(paneId, text) || false : false;
  }, [workspaceIdForSession]);

  const isSessionPaneInputFocused = useCallback((sessionId: string, paneId: string) => {
    const workspaceId = workspaceIdForSession(sessionId);
    return workspaceId ? workspaceRefs.current.get(workspaceId)?.isPaneInputFocused(paneId) || false : false;
  }, [workspaceIdForSession]);

  const scrollSessionPaneToTop = useCallback((sessionId: string, paneId: string) => {
    const workspaceId = workspaceIdForSession(sessionId);
    return workspaceId ? workspaceRefs.current.get(workspaceId)?.scrollPaneToTop(paneId) || false : false;
  }, [workspaceIdForSession]);

  const fitSessionActivePane = useCallback((sessionId: string) => {
    const workspaceId = workspaceIdForSession(sessionId);
    if (!workspaceId) return;
    workspaceRefs.current.get(workspaceId)?.fitActivePane();
  }, [workspaceIdForSession]);

  const getPaneText = useCallback((sessionId: string, paneId: string) => {
    const workspaceId = workspaceIdForSession(sessionId);
    return workspaceId ? workspaceRefs.current.get(workspaceId)?.getPaneText(paneId) || '' : '';
  }, [workspaceIdForSession]);

  const getPaneSize = useCallback((sessionId: string, paneId: string) => {
    const workspaceId = workspaceIdForSession(sessionId);
    return workspaceId ? workspaceRefs.current.get(workspaceId)?.getPaneSize(paneId) || null : null;
  }, [workspaceIdForSession]);

  const getPaneVisibleContent = useCallback((sessionId: string, paneId: string) => {
    const workspaceId = workspaceIdForSession(sessionId);
    return (workspaceId ? workspaceRefs.current.get(workspaceId)?.getPaneVisibleContent(paneId) : null)
      || snapshotVisibleTerminalContent(null);
  }, [workspaceIdForSession]);

  const getPaneVisibleStyleSummary = useCallback((sessionId: string, paneId: string) => {
    const workspaceId = workspaceIdForSession(sessionId);
    return (workspaceId ? workspaceRefs.current.get(workspaceId)?.getPaneVisibleStyleSummary(paneId) : null)
      || emptyTerminalVisibleStyleSnapshot();
  }, [workspaceIdForSession]);

  const resetSessionPaneTerminal = useCallback((sessionId: string, paneId: string) => {
    const workspaceId = workspaceIdForSession(sessionId);
    return workspaceId ? workspaceRefs.current.get(workspaceId)?.resetPaneTerminal(paneId) || false : false;
  }, [workspaceIdForSession]);

  const injectSessionPaneBytes = useCallback((sessionId: string, paneId: string, bytes: Uint8Array) => {
    const workspaceId = workspaceIdForSession(sessionId);
    return workspaceId ? workspaceRefs.current.get(workspaceId)?.injectPaneBytes(paneId, bytes) || Promise.resolve(false) : Promise.resolve(false);
  }, [workspaceIdForSession]);

  const injectSessionPaneBase64 = useCallback((sessionId: string, paneId: string, payload: string) => {
    const workspaceId = workspaceIdForSession(sessionId);
    return workspaceId ? workspaceRefs.current.get(workspaceId)?.injectPaneBase64(paneId, payload) || Promise.resolve(false) : Promise.resolve(false);
  }, [workspaceIdForSession]);

  const drainSessionPaneTerminal = useCallback((sessionId: string, paneId: string) => {
    const workspaceId = workspaceIdForSession(sessionId);
    return workspaceId ? workspaceRefs.current.get(workspaceId)?.drainPaneTerminal(paneId) || Promise.resolve(false) : Promise.resolve(false);
  }, [workspaceIdForSession]);

  return {
    eventRouter,
    getActivePaneIdForSession,
    setActivePane,
    prepareClosePaneFocus,
    clearPreparedClosePaneFocus,
    setWorkspaceRef,
    removeWorkspaceRef,
    getWorkspaceLeafDropSnapshot,
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
