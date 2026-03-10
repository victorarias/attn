import { useCallback, useEffect, useRef, useState } from 'react';
import type { Session } from '../store/sessions';
import { MAIN_TERMINAL_PANE_ID } from '../store/sessions';
import { hasPane } from '../types/workspace';

interface SessionWorkspaceViewStateController {
  getActivePaneIdForSession: (session: Session | undefined | null) => string;
  getDaemonPreferredPaneId: (session: Session | undefined | null) => string;
  getWorkspaceTopologySignature: (session: Session | undefined | null) => string;
  setActivePane: (sessionId: string, paneId: string) => void;
}

export function useSessionWorkspaceViewState(
  sessions: Session[]
): SessionWorkspaceViewStateController {
  const workspaceTopologyRef = useRef<Record<string, string>>({});
  const [localActivePaneBySessionId, setLocalActivePaneBySessionId] = useState<Record<string, string>>({});

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
        const localPaneId = prev[session.id];
        const localPaneValid = Boolean(localPaneId && hasPane(session.workspace.layoutTree, localPaneId));
        const nextPaneId = !localPaneValid || previousSignature !== signature
          ? getDaemonPreferredPaneId(session)
          : localPaneId;
        next[session.id] = nextPaneId;
        if (nextPaneId !== localPaneId) {
          changed = true;
        }
        workspaceTopologyRef.current[session.id] = signature;
      }

      for (const sessionId of Object.keys(workspaceTopologyRef.current)) {
        if (next[sessionId]) {
          continue;
        }
        delete workspaceTopologyRef.current[sessionId];
      }

      return changed ? next : prev;
    });
  }, [getDaemonPreferredPaneId, getWorkspaceTopologySignature, sessions]);

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
    setLocalActivePaneBySessionId((prev) => (
      prev[sessionId] === paneId ? prev : { ...prev, [sessionId]: paneId }
    ));
  }, []);

  return {
    getActivePaneIdForSession,
    getDaemonPreferredPaneId,
    getWorkspaceTopologySignature,
    setActivePane,
  };
}
