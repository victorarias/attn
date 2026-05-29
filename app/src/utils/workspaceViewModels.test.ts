import { describe, expect, it } from 'vitest';
import { buildWorkspaceViewModels, firstSessionIdForWorkspace } from './workspaceViewModels';

describe('workspaceViewModels', () => {
  it('orders workspaces by daemon order and nests sessions by workspace id', () => {
    const workspaces = [
      { id: 'workspace-a', title: 'A', directory: '/repo/a', status: 'idle' },
      { id: 'workspace-b', title: 'B', directory: '/repo/b', status: 'working' },
    ];
    const sessions = [
      { id: 'b1', label: 'B1', workspaceId: 'workspace-b', cwd: '/repo/b' },
      { id: 'a1', label: 'A1', workspaceId: 'workspace-a', cwd: '/repo/a' },
      { id: 'a2', label: 'A2', workspaceId: 'workspace-a', cwd: '/repo/a' },
    ];

    const viewModels = buildWorkspaceViewModels(workspaces, sessions);

    expect(viewModels.map((workspace) => workspace.id)).toEqual(['workspace-a', 'workspace-b']);
    expect(viewModels.map((workspace) => workspace.sessions.map((session) => session.id))).toEqual([
      ['a1', 'a2'],
      ['b1'],
    ]);
    expect(viewModels.map((workspace) => workspace.firstSessionId)).toEqual(['a1', 'b1']);
  });

  it('creates fallback workspaces for sessions missing daemon workspace snapshots', () => {
    const viewModels = buildWorkspaceViewModels([], [
      { id: 's1', label: 'One', workspaceId: 'workspace-s1', cwd: '/repo/one' },
      { id: 's2', label: 'Two', cwd: '/repo/two' },
    ]);

    expect(viewModels.map((workspace) => ({
      id: workspace.id,
      title: workspace.title,
      directory: workspace.directory,
      sessions: workspace.sessions.map((session) => session.id),
    }))).toEqual([
      { id: 'workspace-s1', title: 'one', directory: '/repo/one', sessions: ['s1'] },
      { id: 'workspace-s2', title: 'two', directory: '/repo/two', sessions: ['s2'] },
    ]);
  });

  it('uses remembered focused session when it still belongs to the workspace', () => {
    const [workspace] = buildWorkspaceViewModels(
      [{ id: 'workspace-a', title: 'A', directory: '/repo/a' }],
      [
        { id: 'a1', label: 'A1', workspaceId: 'workspace-a' },
        { id: 'a2', label: 'A2', workspaceId: 'workspace-a' },
      ],
      { focusedSessionIdByWorkspace: { 'workspace-a': 'a2' } },
    );

    expect(workspace.focusedSessionId).toBe('a2');
    expect(firstSessionIdForWorkspace(workspace)).toBe('a1');
  });

  it('falls back to the first child when remembered focus is stale', () => {
    const [workspace] = buildWorkspaceViewModels(
      [{ id: 'workspace-a', title: 'A', directory: '/repo/a' }],
      [{ id: 'a1', label: 'A1', workspaceId: 'workspace-a' }],
      { focusedSessionIdByWorkspace: { 'workspace-a': 'missing' } },
    );

    expect(workspace.focusedSessionId).toBe('a1');
  });
});
