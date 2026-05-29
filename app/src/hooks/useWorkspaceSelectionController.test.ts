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
});
