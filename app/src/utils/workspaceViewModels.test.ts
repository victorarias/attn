import { describe, expect, it } from 'vitest';
import {
  buildWorkspaceViewModels,
  filterSessionsRepresentedInWorkspaceLayouts,
  firstSessionIdForWorkspace,
} from './workspaceViewModels';

describe('workspaceViewModels', () => {
  it('orders workspaces by daemon order and nests sessions by workspace id', () => {
    const workspaces = [
      { id: 'workspace-a', title: 'A', directory: '/repo/a', status: 'idle', muted: false },
      { id: 'workspace-b', title: 'B', directory: '/repo/b', status: 'working', muted: false },
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

  it('does not invent workspaces when daemon workspace snapshots are missing', () => {
    const viewModels = buildWorkspaceViewModels([], [
      { id: 's1', label: 'One', workspaceId: 'workspace-s1', cwd: '/repo/one' },
      { id: 's2', label: 'Two', workspaceId: 'workspace-s2', cwd: '/repo/two' },
    ]);

    expect(viewModels).toEqual([]);
  });

  it('keeps daemon workspaces renderable before their sessions arrive', () => {
    const viewModels = buildWorkspaceViewModels(
      [{ id: 'workspace-pending', title: 'Pending', directory: '/repo/pending', status: 'launching', muted: false }],
      [],
    );

    expect(viewModels).toEqual([
      {
        id: 'workspace-pending',
        title: 'Pending',
        directory: '/repo/pending',
        status: 'launching',
        muted: false,
        endpointId: undefined,
        sessions: [],
        firstSessionId: null,
        focusedSessionId: null,
      },
    ]);
  });

  it('nests remote sessions under endpoint-less daemon workspace snapshots', () => {
    const viewModels = buildWorkspaceViewModels(
      [{ id: 'workspace-remote', title: 'Remote', directory: '/srv/repo' }],
      [
        {
          id: 'remote-session',
          label: 'Remote Session',
          workspace_id: 'workspace-remote',
          endpoint_id: 'ep-remote',
          directory: '/srv/repo',
        },
      ],
    );

    expect(viewModels).toHaveLength(1);
    expect(viewModels[0].endpointId).toBe('ep-remote');
    expect(viewModels[0].sessions.map((session) => session.id)).toEqual(['remote-session']);
  });

  it('pairs duplicate endpoint-less workspace snapshots with distinct endpoint session groups', () => {
    const viewModels = buildWorkspaceViewModels(
      [
        { id: 'workspace-shared', title: 'Remote A', directory: '/srv/a' },
        { id: 'workspace-shared', title: 'Remote B', directory: '/srv/b' },
      ],
      [
        { id: 'a1', label: 'A1', workspace_id: 'workspace-shared', endpoint_id: 'ep-a', directory: '/srv/a' },
        { id: 'b1', label: 'B1', workspace_id: 'workspace-shared', endpoint_id: 'ep-b', directory: '/srv/b' },
      ],
    );

    expect(viewModels.map((workspace) => workspace.sessions.map((session) => session.id))).toEqual([
      ['a1'],
      ['b1'],
    ]);
    expect(viewModels.map((workspace) => workspace.endpointId)).toEqual(['ep-a', 'ep-b']);
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

  it('filters sessions that are no longer represented in an authoritative workspace layout', () => {
    const sessions = [
      { id: 'a1', label: 'Agent 1', workspaceId: 'workspace-a' },
      { id: 'a2', label: 'Agent 2', workspaceId: 'workspace-a' },
      { id: 'stale', label: 'Stale', workspaceId: 'workspace-a' },
      { id: 'pending', label: 'Pending', workspaceId: 'workspace-pending' },
    ];

    const filtered = filterSessionsRepresentedInWorkspaceLayouts(
      [{
        id: 'workspace-a',
        title: 'A',
        directory: '/repo/a',
        layout: {
          panes: [
            { session_id: 'a1' },
            { session_id: 'a2' },
          ],
        },
      }],
      sessions,
    );

    expect(filtered.map((session) => session.id)).toEqual(['a1', 'a2', 'pending']);
  });
});
