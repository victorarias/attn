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
  snapshotVisibleTerminalStyleSummary,
  type TerminalVisibleStyleSnapshot,
} from '../utils/terminalStyleSummary';

interface SessionWorkspaceController {
  eventRouter: ReturnType<typeof usePaneRuntimeEventRouter>;
  getActivePaneIdForSession: (session: Session | undefined | null) => string;
  setActivePane: (sessionId: string, paneId: string) => void;
  prepareClosePaneFocus: (sessionId: string, paneId: string) => string;
  clearPreparedClosePaneFocus: (sessionId: string) => void;
  setWorkspaceRef: (sessionId: string) => (ref: SessionTerminalWorkspaceHandle | null) => void;
  removeWorkspaceRef: (sessionId: string) => void;
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

  const prepareClosePaneFocus = useCallback((sessionId: string, paneId: string) => {
    const session = sessions.find((entry) => entry.id === sessionId);
    return prepareClosePaneFocusForSession(session, paneId);
  }, [prepareClosePaneFocusForSession, sessions]);

  const focusSessionPane = useCallback((sessionId: string, paneId: string, retries = 20) => {
    workspaceRefs.current.get(sessionId)?.focusPane(paneId, retries);
  }, []);

  const typeInSessionPaneViaUI = useCallback((sessionId: string, paneId: string, text: string) => {
    return workspaceRefs.current.get(sessionId)?.typePaneTextViaUI(paneId, text) || false;
  }, []);

  const isSessionPaneInputFocused = useCallback((sessionId: string, paneId: string) => {
    return workspaceRefs.current.get(sessionId)?.isPaneInputFocused(paneId) || false;
  }, []);

  const scrollSessionPaneToTop = useCallback((sessionId: string, paneId: string) => {
    return workspaceRefs.current.get(sessionId)?.scrollPaneToTop(paneId) || false;
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

  const getPaneVisibleContent = useCallback((sessionId: string, paneId: string) => {
    return workspaceRefs.current.get(sessionId)?.getPaneVisibleContent(paneId)
      || snapshotVisibleTerminalContent(null);
  }, []);

  const getPaneVisibleStyleSummary = useCallback((sessionId: string, paneId: string) => {
    return workspaceRefs.current.get(sessionId)?.getPaneVisibleStyleSummary(paneId)
      || snapshotVisibleTerminalStyleSummary(null);
  }, []);

  const resetSessionPaneTerminal = useCallback((sessionId: string, paneId: string) => {
    return workspaceRefs.current.get(sessionId)?.resetPaneTerminal(paneId) || false;
  }, []);

  const injectSessionPaneBytes = useCallback((sessionId: string, paneId: string, bytes: Uint8Array) => {
    return workspaceRefs.current.get(sessionId)?.injectPaneBytes(paneId, bytes) || Promise.resolve(false);
  }, []);

  const injectSessionPaneBase64 = useCallback((sessionId: string, paneId: string, payload: string) => {
    return workspaceRefs.current.get(sessionId)?.injectPaneBase64(paneId, payload) || Promise.resolve(false);
  }, []);

  const drainSessionPaneTerminal = useCallback((sessionId: string, paneId: string) => {
    return workspaceRefs.current.get(sessionId)?.drainPaneTerminal(paneId) || Promise.resolve(false);
  }, []);

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
