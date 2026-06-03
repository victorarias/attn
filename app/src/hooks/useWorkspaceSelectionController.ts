import { useMemo } from 'react';
import type { WorkspaceViewSession, WorkspaceWithSessions } from '../utils/workspaceViewModels';

export interface WorkspaceSelectionState {
  activeWorkspaceId: string | null;
  activeSessionId: string | null;
  focusedSessionIdByWorkspace: Record<string, string>;
}

interface BuildWorkspaceSelectionArgs<TSession extends WorkspaceViewSession> {
  workspaces: WorkspaceWithSessions<TSession>[];
  activeSessionId: string | null;
  // An explicitly selected workspace. Only takes effect for a sessionless
  // (tile-only) workspace, which has no session id to activate through. When the
  // selected workspace has sessions — or no longer exists — the session-derived
  // active workspace wins, so a stale selection can never shadow real sessions.
  selectedWorkspaceId?: string | null;
  previousFocusedSessionIdByWorkspace?: Record<string, string | null | undefined>;
}

export function buildWorkspaceSelectionState<TSession extends WorkspaceViewSession>({
  workspaces,
  activeSessionId,
  selectedWorkspaceId = null,
  previousFocusedSessionIdByWorkspace = {},
}: BuildWorkspaceSelectionArgs<TSession>): WorkspaceSelectionState {
  const sessionWorkspaceById = new Map<string, string>();
  const focusedSessionIdByWorkspace: Record<string, string> = {};

  for (const workspace of workspaces) {
    for (const session of workspace.sessions) {
      sessionWorkspaceById.set(session.id, workspace.id);
    }
  }

  const sessionDerivedWorkspaceId = activeSessionId
    ? sessionWorkspaceById.get(activeSessionId) ?? null
    : null;
  const selectedSessionlessWorkspaceId = selectedWorkspaceId
    && workspaces.some((workspace) => workspace.id === selectedWorkspaceId && workspace.sessions.length === 0)
    ? selectedWorkspaceId
    : null;
  const activeWorkspaceId = selectedSessionlessWorkspaceId ?? sessionDerivedWorkspaceId;

  for (const workspace of workspaces) {
    const previousFocus = previousFocusedSessionIdByWorkspace[workspace.id] || null;
    const validPreviousFocus = previousFocus && workspace.sessions.some((session) => session.id === previousFocus)
      ? previousFocus
      : null;
    const activeFocus = sessionDerivedWorkspaceId === workspace.id ? activeSessionId : null;
    const focus = activeFocus || validPreviousFocus || workspace.firstSessionId;
    if (focus) {
      focusedSessionIdByWorkspace[workspace.id] = focus;
    }
  }

  return {
    activeWorkspaceId,
    activeSessionId,
    focusedSessionIdByWorkspace,
  };
}

export function useWorkspaceSelectionController<TSession extends WorkspaceViewSession>(
  workspaces: WorkspaceWithSessions<TSession>[],
  activeSessionId: string | null,
  selectedWorkspaceId?: string | null,
  previousFocusedSessionIdByWorkspace?: Record<string, string | null | undefined>,
): WorkspaceSelectionState {
  return useMemo(
    () => buildWorkspaceSelectionState({
      workspaces,
      activeSessionId,
      selectedWorkspaceId,
      previousFocusedSessionIdByWorkspace,
    }),
    [activeSessionId, selectedWorkspaceId, previousFocusedSessionIdByWorkspace, workspaces],
  );
}
