import { useCallback, useEffect, useRef, useState } from 'react';
import type { Session } from '../store/sessions';
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
  const sessionWorkspaceIdRef = useRef<Record<string, string>>({});
  const [localActivePaneByWorkspaceId, setLocalActivePaneByWorkspaceId] = useState<Record<string, string>>({});

  const workspaceKeyForSession = useCallback((session: Session | undefined | null) => {
    return session?.workspaceId || session?.id || '';
  }, []);

  const workspaceKeyForSessionId = useCallback((sessionId: string) => {
    return sessionWorkspaceIdRef.current[sessionId] || sessionId;
  }, []);

  const rememberPaneActivation = useCallback((sessionId: string, paneId: string) => {
    if (!sessionId || !paneId) {
      return;
    }
    const workspaceKey = workspaceKeyForSessionId(sessionId);
    const existing = paneActivationHistoryRef.current[workspaceKey] || [];
    if (existing[existing.length - 1] === paneId) {
      return;
    }
    const next = existing.filter((entry) => entry !== paneId);
    next.push(paneId);
    paneActivationHistoryRef.current[workspaceKey] = next.slice(-8);
  }, [workspaceKeyForSessionId]);

  const getWorkspaceTopologySignature = useCallback((session: Session | undefined | null) => {
    if (!session) {
      return '';
    }
    return JSON.stringify({
      layoutTree: session.workspace.layoutTree,
      agents: session.workspace.agents.map((agent) => agent.id),
    });
  }, []);

  const getDaemonPreferredPaneId = useCallback((session: Session | undefined | null) => {
    if (!session || !session.workspace.layoutTree) {
      return '';
    }
    return hasPane(session.workspace.layoutTree, session.daemonActivePaneId)
      ? session.daemonActivePaneId
      : session.workspace.agents[0]?.id || '';
  }, []);

  useEffect(() => {
    setLocalActivePaneByWorkspaceId((prev) => {
      const next: Record<string, string> = {};
      let changed = false;
      const seenWorkspaceKeys = new Set<string>();

      for (const session of sessions) {
        const workspaceKey = workspaceKeyForSession(session);
        sessionWorkspaceIdRef.current[session.id] = workspaceKey;
        if (!workspaceKey || seenWorkspaceKeys.has(workspaceKey)) {
          continue;
        }
        seenWorkspaceKeys.add(workspaceKey);
        const signature = getWorkspaceTopologySignature(session);
        const previousSignature = workspaceTopologyRef.current[workspaceKey];
        const topologyChanged = previousSignature !== signature;
        const localPaneId = prev[workspaceKey];
        const localPaneValid = Boolean(localPaneId && session.workspace.layoutTree && hasPane(session.workspace.layoutTree, localPaneId));
        const preparedClosePaneFocus = pendingClosePaneFocusRef.current[workspaceKey];
        const preparedClosePaneValid = Boolean(
          topologyChanged &&
          preparedClosePaneFocus &&
          session.workspace.layoutTree &&
          hasPane(session.workspace.layoutTree, preparedClosePaneFocus)
        );
        const nextPaneId = preparedClosePaneValid
          ? preparedClosePaneFocus
          : (!localPaneValid || topologyChanged
            ? getDaemonPreferredPaneId(session)
            : localPaneId);
        next[workspaceKey] = nextPaneId;
        if (nextPaneId !== localPaneId) {
          changed = true;
        }
        rememberPaneActivation(session.id, nextPaneId);
        if (topologyChanged) {
          delete pendingClosePaneFocusRef.current[workspaceKey];
        }
        workspaceTopologyRef.current[workspaceKey] = signature;
      }

      for (const workspaceKey of Object.keys(workspaceTopologyRef.current)) {
        if (next[workspaceKey]) {
          continue;
        }
        delete workspaceTopologyRef.current[workspaceKey];
        delete paneActivationHistoryRef.current[workspaceKey];
        delete pendingClosePaneFocusRef.current[workspaceKey];
      }

      for (const sessionId of Object.keys(sessionWorkspaceIdRef.current)) {
        if (sessions.some((session) => session.id === sessionId)) {
          continue;
        }
        delete sessionWorkspaceIdRef.current[sessionId];
      }

      changed ||= Object.keys(prev).length !== Object.keys(next).length;
      return changed ? next : prev;
    });
  }, [getDaemonPreferredPaneId, getWorkspaceTopologySignature, rememberPaneActivation, sessions, workspaceKeyForSession]);

  const getActivePaneIdForSession = useCallback((session: Session | undefined | null) => {
    if (!session || !session.workspace.layoutTree) {
      return '';
    }
    const workspaceKey = workspaceKeyForSession(session);
    const currentTopology = getWorkspaceTopologySignature(session);
    const previousTopology = workspaceTopologyRef.current[workspaceKey];
    if (previousTopology !== currentTopology) {
      return getDaemonPreferredPaneId(session);
    }
    const localPaneId = localActivePaneByWorkspaceId[workspaceKey];
    if (localPaneId && hasPane(session.workspace.layoutTree, localPaneId)) {
      return localPaneId;
    }
    return getDaemonPreferredPaneId(session);
  }, [getDaemonPreferredPaneId, getWorkspaceTopologySignature, localActivePaneByWorkspaceId, workspaceKeyForSession]);

  const setActivePane = useCallback((sessionId: string, paneId: string) => {
    rememberPaneActivation(sessionId, paneId);
    const workspaceKey = workspaceKeyForSessionId(sessionId);
    setLocalActivePaneByWorkspaceId((prev) => (
      prev[workspaceKey] === paneId ? prev : { ...prev, [workspaceKey]: paneId }
    ));
  }, [rememberPaneActivation, workspaceKeyForSessionId]);

  const prepareClosePaneFocus = useCallback((session: Session | undefined | null, paneId: string) => {
    if (!session || !paneId || !session.workspace.layoutTree) {
      return '';
    }

    const workspaceKey = workspaceKeyForSession(session);
    const layoutTree = session.workspace.layoutTree;
    const activePaneId = getActivePaneIdForSession(session);
    if (activePaneId !== paneId && hasPane(layoutTree, activePaneId)) {
      pendingClosePaneFocusRef.current[workspaceKey] = activePaneId;
      return activePaneId;
    }

    const priorPaneId = [...(paneActivationHistoryRef.current[workspaceKey] || [])]
      .reverse()
      .find((candidate) => candidate !== paneId && hasPane(layoutTree, candidate));
    if (priorPaneId) {
      pendingClosePaneFocusRef.current[workspaceKey] = priorPaneId;
      return priorPaneId;
    }

    const leftPaneId = findPaneInDirection(layoutTree, paneId, 'left');
    if (leftPaneId) {
      pendingClosePaneFocusRef.current[workspaceKey] = leftPaneId;
      return leftPaneId;
    }

    const fallbackPaneId = session.workspace.agents[0]?.id || '';
    pendingClosePaneFocusRef.current[workspaceKey] = fallbackPaneId;
    return fallbackPaneId;
  }, [getActivePaneIdForSession, workspaceKeyForSession]);

  const clearPreparedClosePaneFocus = useCallback((sessionId: string) => {
    delete pendingClosePaneFocusRef.current[workspaceKeyForSessionId(sessionId)];
  }, [workspaceKeyForSessionId]);

  return {
    getActivePaneIdForSession,
    setActivePane,
    prepareClosePaneFocus,
    clearPreparedClosePaneFocus,
  };
}
