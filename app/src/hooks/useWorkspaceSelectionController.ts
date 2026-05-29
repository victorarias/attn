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
  previousFocusedSessionIdByWorkspace?: Record<string, string | null | undefined>;
}

export function buildWorkspaceSelectionState<TSession extends WorkspaceViewSession>({
  workspaces,
  activeSessionId,
  previousFocusedSessionIdByWorkspace = {},
}: BuildWorkspaceSelectionArgs<TSession>): WorkspaceSelectionState {
  const sessionWorkspaceById = new Map<string, string>();
  const focusedSessionIdByWorkspace: Record<string, string> = {};

  for (const workspace of workspaces) {
    for (const session of workspace.sessions) {
      sessionWorkspaceById.set(session.id, workspace.id);
    }
  }

  const activeWorkspaceId = activeSessionId
    ? sessionWorkspaceById.get(activeSessionId) ?? null
    : null;

  for (const workspace of workspaces) {
    const previousFocus = previousFocusedSessionIdByWorkspace[workspace.id] || null;
    const validPreviousFocus = previousFocus && workspace.sessions.some((session) => session.id === previousFocus)
      ? previousFocus
      : null;
    const activeFocus = activeWorkspaceId === workspace.id ? activeSessionId : null;
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
  previousFocusedSessionIdByWorkspace?: Record<string, string | null | undefined>,
): WorkspaceSelectionState {
  return useMemo(
    () => buildWorkspaceSelectionState({
      workspaces,
      activeSessionId,
      previousFocusedSessionIdByWorkspace,
    }),
    [activeSessionId, previousFocusedSessionIdByWorkspace, workspaces],
  );
}
