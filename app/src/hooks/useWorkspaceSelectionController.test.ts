import { describe, expect, it } from 'vitest';
import { buildWorkspaceSelectionState } from './useWorkspaceSelectionController';
import type { WorkspaceWithSessions } from '../utils/workspaceViewModels';

function workspace(id: string, sessionIds: string[]): WorkspaceWithSessions<{ id: string; label: string; workspaceId: string }> {
  return {
    id,
    title: id,
    directory: `/tmp/${id}`,
    sessions: sessionIds.map((sessionId) => ({ id: sessionId, label: sessionId, workspaceId: id })),
    firstSessionId: sessionIds[0] ?? null,
    focusedSessionId: sessionIds[0] ?? null,
  };
}

describe('buildWorkspaceSelectionState', () => {
  it('derives the active workspace from the active session', () => {
    const selection = buildWorkspaceSelectionState({
      workspaces: [workspace('workspace-a', ['a1']), workspace('workspace-b', ['b1', 'b2'])],
      activeSessionId: 'b2',
    });

    expect(selection.activeWorkspaceId).toBe('workspace-b');
    expect(selection.focusedSessionIdByWorkspace).toEqual({
      'workspace-a': 'a1',
      'workspace-b': 'b2',
    });
  });

  it('keeps remembered focus for inactive workspaces when the session still exists', () => {
    const selection = buildWorkspaceSelectionState({
      workspaces: [workspace('workspace-a', ['a1', 'a2']), workspace('workspace-b', ['b1'])],
      activeSessionId: 'b1',
      previousFocusedSessionIdByWorkspace: {
        'workspace-a': 'a2',
      },
    });

    expect(selection.focusedSessionIdByWorkspace).toEqual({
      'workspace-a': 'a2',
      'workspace-b': 'b1',
    });
  });

  it('drops stale remembered focus and falls back to the first session', () => {
    const selection = buildWorkspaceSelectionState({
      workspaces: [workspace('workspace-a', ['a1'])],
      activeSessionId: null,
      previousFocusedSessionIdByWorkspace: {
        'workspace-a': 'missing',
      },
    });

    expect(selection.activeWorkspaceId).toBeNull();
    expect(selection.focusedSessionIdByWorkspace).toEqual({
      'workspace-a': 'a1',
    });
  });

  it('activates a sessionless workspace by id when no session can reach it', () => {
    const selection = buildWorkspaceSelectionState({
      workspaces: [workspace('workspace-a', ['a1']), workspace('tiles-only', [])],
      activeSessionId: null,
      selectedWorkspaceId: 'tiles-only',
    });

    expect(selection.activeWorkspaceId).toBe('tiles-only');
    // The tile-only workspace has no session to focus.
    expect(selection.focusedSessionIdByWorkspace).toEqual({
      'workspace-a': 'a1',
    });
  });

  it('lets an explicitly selected sessionless workspace win over a stale active session', () => {
    const selection = buildWorkspaceSelectionState({
      workspaces: [workspace('workspace-a', ['a1']), workspace('tiles-only', [])],
      // A session elsewhere is still the "active session" in the background.
      activeSessionId: 'a1',
      selectedWorkspaceId: 'tiles-only',
    });

    expect(selection.activeWorkspaceId).toBe('tiles-only');
  });

  it('ignores a selected workspace that gained sessions and falls back to the active session', () => {
    const selection = buildWorkspaceSelectionState({
      workspaces: [workspace('workspace-a', ['a1']), workspace('was-tiles-only', ['x1'])],
      activeSessionId: 'a1',
      // Stale selection from when 'was-tiles-only' had no sessions.
      selectedWorkspaceId: 'was-tiles-only',
    });

    expect(selection.activeWorkspaceId).toBe('workspace-a');
  });

  it('ignores a selected workspace that no longer exists', () => {
    const selection = buildWorkspaceSelectionState({
      workspaces: [workspace('workspace-a', ['a1'])],
      activeSessionId: 'a1',
      selectedWorkspaceId: 'deleted-workspace',
    });

    expect(selection.activeWorkspaceId).toBe('workspace-a');
  });
});
