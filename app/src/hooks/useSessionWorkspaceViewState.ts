import { useCallback, useEffect, useRef, useState } from 'react';
import type { Session } from '../store/sessions';
import { MAIN_TERMINAL_PANE_ID } from '../store/sessions';
import { findPaneInDirection, hasPane } from '../types/workspace';

interface SessionWorkspaceViewStateController {
  getActivePaneIdForSession: (session: Session | undefined | null) => string;
  setActivePane: (sessionId: string, paneId: string) => void;
  prepareClosePaneFocus: (session: Session | undefined | null, paneId: string) => string;
  clearPreparedClosePaneFocus: (sessionId: string) => void;
}

export function useSessionWorkspaceViewState(
  sessions: Session[]
): SessionWorkspaceViewStateController {
  const workspaceTopologyRef = useRef<Record<string, string>>({});
  const paneActivationHistoryRef = useRef<Record<string, string[]>>({});
  const pendingClosePaneFocusRef = useRef<Record<string, string>>({});
  const [localActivePaneBySessionId, setLocalActivePaneBySessionId] = useState<Record<string, string>>({});

  const rememberPaneActivation = useCallback((sessionId: string, paneId: string) => {
    if (!sessionId || !paneId) {
      return;
    }
    const existing = paneActivationHistoryRef.current[sessionId] || [];
    if (existing[existing.length - 1] === paneId) {
      return;
    }
    const next = existing.filter((entry) => entry !== paneId);
    next.push(paneId);
    paneActivationHistoryRef.current[sessionId] = next.slice(-8);
  }, []);

  const getWorkspaceTopologySignature = useCallback((session: Session | undefined | null) => {
    if (!session) {
      return '';
    }
    return JSON.stringify({
      layoutTree: session.workspace.layoutTree,
      terminals: session.workspace.terminals.map((terminal) => ({
        id: terminal.id,
        ptyId: terminal.ptyId,
        title: terminal.title,
      })),
    });
  }, []);

  const getDaemonPreferredPaneId = useCallback((session: Session | undefined | null) => {
    if (!session) {
      return MAIN_TERMINAL_PANE_ID;
    }
    return hasPane(session.workspace.layoutTree, session.daemonActivePaneId)
      ? session.daemonActivePaneId
      : MAIN_TERMINAL_PANE_ID;
  }, []);

  useEffect(() => {
    setLocalActivePaneBySessionId((prev) => {
      let changed = Object.keys(prev).length !== sessions.length;
      const next: Record<string, string> = {};

      for (const session of sessions) {
        const signature = getWorkspaceTopologySignature(session);
        const previousSignature = workspaceTopologyRef.current[session.id];
        const topologyChanged = previousSignature !== signature;
        const localPaneId = prev[session.id];
        const localPaneValid = Boolean(localPaneId && hasPane(session.workspace.layoutTree, localPaneId));
        const preparedClosePaneFocus = pendingClosePaneFocusRef.current[session.id];
        const preparedClosePaneValid = Boolean(
          topologyChanged &&
          preparedClosePaneFocus &&
          hasPane(session.workspace.layoutTree, preparedClosePaneFocus)
        );
        const nextPaneId = preparedClosePaneValid
          ? preparedClosePaneFocus
          : (!localPaneValid || topologyChanged
            ? getDaemonPreferredPaneId(session)
            : localPaneId);
        next[session.id] = nextPaneId;
        if (nextPaneId !== localPaneId) {
          changed = true;
        }
        rememberPaneActivation(session.id, nextPaneId);
        if (topologyChanged) {
          delete pendingClosePaneFocusRef.current[session.id];
        }
        workspaceTopologyRef.current[session.id] = signature;
      }

      for (const sessionId of Object.keys(workspaceTopologyRef.current)) {
        if (next[sessionId]) {
          continue;
        }
        delete workspaceTopologyRef.current[sessionId];
        delete paneActivationHistoryRef.current[sessionId];
        delete pendingClosePaneFocusRef.current[sessionId];
      }

      return changed ? next : prev;
    });
  }, [getDaemonPreferredPaneId, getWorkspaceTopologySignature, rememberPaneActivation, sessions]);

  const getActivePaneIdForSession = useCallback((session: Session | undefined | null) => {
    if (!session) {
      return MAIN_TERMINAL_PANE_ID;
    }
    const currentTopology = getWorkspaceTopologySignature(session);
    const previousTopology = workspaceTopologyRef.current[session.id];
    if (previousTopology !== currentTopology) {
      return getDaemonPreferredPaneId(session);
    }
    const localPaneId = localActivePaneBySessionId[session.id];
    if (localPaneId && hasPane(session.workspace.layoutTree, localPaneId)) {
      return localPaneId;
    }
    return getDaemonPreferredPaneId(session);
  }, [getDaemonPreferredPaneId, getWorkspaceTopologySignature, localActivePaneBySessionId]);

  const setActivePane = useCallback((sessionId: string, paneId: string) => {
    rememberPaneActivation(sessionId, paneId);
    setLocalActivePaneBySessionId((prev) => (
      prev[sessionId] === paneId ? prev : { ...prev, [sessionId]: paneId }
    ));
  }, [rememberPaneActivation]);

  const prepareClosePaneFocus = useCallback((session: Session | undefined | null, paneId: string) => {
    if (!session || !paneId) {
      return MAIN_TERMINAL_PANE_ID;
    }

    const activePaneId = getActivePaneIdForSession(session);
    if (activePaneId !== paneId && hasPane(session.workspace.layoutTree, activePaneId)) {
      pendingClosePaneFocusRef.current[session.id] = activePaneId;
      return activePaneId;
    }

    const priorPaneId = [...(paneActivationHistoryRef.current[session.id] || [])]
      .reverse()
      .find((candidate) => candidate !== paneId && hasPane(session.workspace.layoutTree, candidate));
    if (priorPaneId) {
      pendingClosePaneFocusRef.current[session.id] = priorPaneId;
      return priorPaneId;
    }

    const leftPaneId = findPaneInDirection(session.workspace.layoutTree, paneId, 'left');
    if (leftPaneId) {
      pendingClosePaneFocusRef.current[session.id] = leftPaneId;
      return leftPaneId;
    }

    pendingClosePaneFocusRef.current[session.id] = MAIN_TERMINAL_PANE_ID;
    return MAIN_TERMINAL_PANE_ID;
  }, [getActivePaneIdForSession]);

  const clearPreparedClosePaneFocus = useCallback((sessionId: string) => {
    delete pendingClosePaneFocusRef.current[sessionId];
  }, []);

  return {
    getActivePaneIdForSession,
    setActivePane,
    prepareClosePaneFocus,
    clearPreparedClosePaneFocus,
  };
}
