import { describe, it, expect, beforeEach, vi } from 'vitest';
import { useSessionStore } from './sessions';
import { WorkspaceLayoutPaneKind, WorkspaceLayoutPaneStatus, WorkspaceStatus } from '../types/generated';

const { mockPtySpawn, mockPtyKill } = vi.hoisted(() => ({
  mockPtySpawn: vi.fn(),
  mockPtyKill: vi.fn(),
}));

vi.mock('@tauri-apps/api/core', () => ({
  invoke: vi.fn().mockResolvedValue(undefined),
}));

vi.mock('@tauri-apps/api/event', () => ({
  listen: vi.fn().mockResolvedValue(() => {}),
}));

vi.mock('../pty/bridge', async () => {
  const actual = await vi.importActual<typeof import('../pty/bridge')>('../pty/bridge');
  return {
    ...actual,
    ptySpawn: mockPtySpawn,
    ptyKill: mockPtyKill,
  };
});

describe('sessions store', () => {
  beforeEach(() => {
    mockPtySpawn.mockReset();
    mockPtyKill.mockReset();
    useSessionStore.setState({
      sessions: [],
      activeSessionId: null,
      connected: false,
      launcherConfig: { executables: {} },
    });
  });

  it('creates sessions with a default daemon-owned workspace view model', async () => {
    const sessionId = await useSessionStore.getState().createSession('test', '/tmp/test', 'sess-test', 'codex', undefined, false, 'workspace-sess-test');
    const session = useSessionStore.getState().sessions.find((entry) => entry.id === sessionId);

    expect(session?.workspace).toEqual({
      agents: [],
      layoutTree: null,
    });
    expect(session?.daemonActivePaneId).toBe('');
  });

  it('syncFromDaemonSessions hydrates canonical session data and preserves local workspace state', () => {
    useSessionStore.setState({
      sessions: [
        {
          id: 'sess-1',
          label: 'Old Label',
          state: 'working',
          cwd: '/tmp/old',
          workspaceId: 'workspace-sess-1',
          agent: 'codex',
          transcriptMatched: false,
          daemonActivePaneId: 'pane-a',
          workspace: {
            agents: [{ id: 'pane-a', runtimeId: 'runtime-a', title: "Session", sessionId: 'session-1' }],
            layoutTree: {
              type: 'split',
              splitId: 'root',
              direction: 'vertical',
              ratio: 0.5,
              children: [
                { type: 'pane', paneId: 'pane-session' },
                { type: 'pane', paneId: 'pane-a' },
              ],
            },
          },
        },
      ],
    });

    useSessionStore.getState().syncFromDaemonSessions([
      {
        id: 'sess-1',
        label: 'New Label',
        agent: 'claude',
        directory: '/tmp/new',
        workspace_id: 'workspace-sess-1',
        endpoint_id: 'ep-1',
        state: 'idle',
        branch: 'feature/workspace',
        is_worktree: true,
      },
    ]);

    const session = useSessionStore.getState().sessions[0];
    expect(session).toMatchObject({
      id: 'sess-1',
      label: 'New Label',
      cwd: '/tmp/new',
      state: 'idle',
      agent: 'claude',
      endpointId: 'ep-1',
      branch: 'feature/workspace',
      isWorktree: true,
      daemonActivePaneId: 'pane-a',
    });
    expect(session.workspace).toMatchObject({
      agents: [{ id: 'pane-a', runtimeId: 'runtime-a', title: "Session", sessionId: 'session-1' }],
    });
  });

  it('syncFromDaemonSessions removes closed ready workspace sessions and restores recent selection', () => {
    useSessionStore.setState({
      activeSessionId: 'split-session',
      recentSessionIds: ['root-session'],
      sessions: [
        {
          id: 'root-session',
          label: 'Root',
          state: 'idle',
          cwd: '/tmp/workspace',
          workspaceId: 'workspace-root',
          agent: 'shell',
          transcriptMatched: true,
          daemonActivePaneId: 'pane-split',
          workspace: {
            agents: [
              { id: 'pane-root', runtimeId: 'root-session', title: 'Root', sessionId: 'root-session' },
              { id: 'pane-split', runtimeId: 'split-session', title: 'Split', sessionId: 'split-session' },
            ],
            layoutTree: {
              type: 'split',
              splitId: 'root',
              direction: 'vertical',
              ratio: 0.5,
              children: [
                { type: 'pane', paneId: 'pane-root' },
                { type: 'pane', paneId: 'pane-split' },
              ],
            },
          },
        },
        {
          id: 'split-session',
          label: 'Split',
          state: 'idle',
          cwd: '/tmp/workspace',
          workspaceId: 'workspace-root',
          agent: 'shell',
          transcriptMatched: true,
          daemonActivePaneId: 'pane-split',
          workspace: {
            agents: [
              { id: 'pane-root', runtimeId: 'root-session', title: 'Root', sessionId: 'root-session' },
              { id: 'pane-split', runtimeId: 'split-session', title: 'Split', sessionId: 'split-session' },
            ],
            layoutTree: {
              type: 'split',
              splitId: 'root',
              direction: 'vertical',
              ratio: 0.5,
              children: [
                { type: 'pane', paneId: 'pane-root' },
                { type: 'pane', paneId: 'pane-split' },
              ],
            },
          },
        },
      ],
    });

    useSessionStore.getState().syncFromDaemonSessions([
      {
        id: 'root-session',
        label: 'Root',
        agent: 'shell',
        directory: '/tmp/workspace',
        workspace_id: 'workspace-root',
        state: 'idle',
      },
    ]);

    const state = useSessionStore.getState();
    expect(state.sessions.map((session) => session.id)).toEqual(['root-session']);
    expect(state.activeSessionId).toBe('root-session');
    expect(state.recentSessionIds).toEqual([]);
  });

  it('syncFromDaemonSessions falls back to a remaining same-workspace session when recent selection is empty', () => {
    useSessionStore.setState({
      activeSessionId: 'split-session',
      recentSessionIds: [],
      sessions: [
        {
          id: 'root-session',
          label: 'Root',
          state: 'idle',
          cwd: '/tmp/workspace',
          workspaceId: 'workspace-root',
          agent: 'shell',
          transcriptMatched: true,
          daemonActivePaneId: 'pane-split',
          workspace: {
            agents: [
              { id: 'pane-root', runtimeId: 'root-session', title: 'Root', sessionId: 'root-session' },
              { id: 'pane-split', runtimeId: 'split-session', title: 'Split', sessionId: 'split-session' },
            ],
            layoutTree: null,
          },
        },
        {
          id: 'split-session',
          label: 'Split',
          state: 'idle',
          cwd: '/tmp/workspace',
          workspaceId: 'workspace-root',
          agent: 'shell',
          transcriptMatched: true,
          daemonActivePaneId: 'pane-split',
          workspace: {
            agents: [
              { id: 'pane-root', runtimeId: 'root-session', title: 'Root', sessionId: 'root-session' },
              { id: 'pane-split', runtimeId: 'split-session', title: 'Split', sessionId: 'split-session' },
            ],
            layoutTree: null,
          },
        },
      ],
    });

    useSessionStore.getState().syncFromDaemonSessions([
      {
        id: 'root-session',
        label: 'Root',
        agent: 'shell',
        directory: '/tmp/workspace',
        workspace_id: 'workspace-root',
        state: 'idle',
      },
    ]);

    const state = useSessionStore.getState();
    expect(state.sessions.map((session) => session.id)).toEqual(['root-session']);
    expect(state.activeSessionId).toBe('root-session');
  });

  it('syncFromDaemonSessions falls back to another remaining session when a whole workspace closes', () => {
    useSessionStore.setState({
      activeSessionId: 'closing-session',
      recentSessionIds: [],
      sessions: [
        {
          id: 'previous-session',
          label: 'Previous',
          state: 'idle',
          cwd: '/tmp/previous',
          workspaceId: 'workspace-previous',
          agent: 'shell',
          transcriptMatched: true,
          daemonActivePaneId: 'pane-previous',
          workspace: {
            agents: [{ id: 'pane-previous', runtimeId: 'previous-session', title: 'Previous', sessionId: 'previous-session' }],
            layoutTree: { type: 'pane', paneId: 'pane-previous' },
          },
        },
        {
          id: 'closing-session',
          label: 'Closing',
          state: 'idle',
          cwd: '/tmp/closing',
          workspaceId: 'workspace-closing',
          agent: 'shell',
          transcriptMatched: true,
          daemonActivePaneId: 'pane-closing',
          workspace: {
            agents: [{ id: 'pane-closing', runtimeId: 'closing-session', title: 'Closing', sessionId: 'closing-session' }],
            layoutTree: { type: 'pane', paneId: 'pane-closing' },
          },
        },
      ],
    });

    useSessionStore.getState().syncFromDaemonSessions([
      {
        id: 'previous-session',
        label: 'Previous',
        agent: 'shell',
        directory: '/tmp/previous',
        workspace_id: 'workspace-previous',
        state: 'idle',
      },
    ]);

    const state = useSessionStore.getState();
    expect(state.sessions.map((session) => session.id)).toEqual(['previous-session']);
    expect(state.activeSessionId).toBe('previous-session');
  });

  it('takeSessionSpawnArgs applies launcher overrides', async () => {
    const sessionId = await useSessionStore.getState().createSession('Spawn Test', '/tmp/workspace', 'sess-spawn', 'claude', 'ep-1', true, 'workspace-sess-spawn');
    useSessionStore.getState().setLauncherConfig({
      executables: { claude: '/opt/bin/claude-custom' },
    });

    const first = useSessionStore.getState().takeSessionSpawnArgs(sessionId, 120, 40);

    expect(first).toMatchObject({
      id: sessionId,
      cwd: '/tmp/workspace',
      endpoint_id: 'ep-1',
      label: 'Spawn Test',
      cols: 120,
      rows: 40,
      agent: 'claude',
      executable: '/opt/bin/claude-custom',
      claude_executable: '/opt/bin/claude-custom',
      resume_session_id: null,
      yolo_mode: true,
    });
  });

  it('syncFromDaemonWorkspaces replaces the local split workspace from daemon snapshots', async () => {
    const sessionId = await useSessionStore.getState().createSession('Workspace', '/tmp/workspace', 'sess-workspace', 'codex', undefined, false, 'workspace-sess-workspace');

    useSessionStore.getState().syncFromDaemonWorkspaces([
      {
        id: `workspace-${sessionId}`,
        title: 'Workspace',
        directory: '/tmp/workspace',
        status: WorkspaceStatus.Idle,
        layout: {
          workspace_id: `workspace-${sessionId}`,
          active_pane_id: 'pane-shell',
          layout_json: JSON.stringify({
            type: 'split',
            split_id: 'root',
            direction: 'vertical',
            ratio: 0.5,
            children: [
              { type: 'pane', pane_id: 'pane-session' },
              { type: 'pane', pane_id: 'pane-shell' },
            ],
          }),
          panes: [
            { workspace_id: `workspace-${sessionId}`, pane_id: 'pane-session', kind: WorkspaceLayoutPaneKind.Agent, title: 'Agent', runtime_id: sessionId, session_id: sessionId, status: WorkspaceLayoutPaneStatus.Ready },
            { workspace_id: `workspace-${sessionId}`, pane_id: 'pane-shell', kind: WorkspaceLayoutPaneKind.Agent, title: 'Shell 1', runtime_id: 'runtime-shell', session_id: 'sess-shell', status: WorkspaceLayoutPaneStatus.Ready },
          ],
        },
      },
    ]);

    const session = useSessionStore.getState().sessions.find((entry) => entry.id === sessionId);
    expect(session?.workspace).toEqual({
      agents: [
        { id: 'pane-session', runtimeId: sessionId, sessionId, title: 'Agent' },
        { id: 'pane-shell', runtimeId: 'runtime-shell', sessionId: 'sess-shell', title: 'Shell 1' },
      ],
      layoutTree: {
        type: 'split',
        splitId: 'root',
        direction: 'vertical',
        ratio: 0.5,
        children: [
          { type: 'pane', paneId: 'pane-session' },
          { type: 'pane', paneId: 'pane-shell' },
        ],
      },
    });
    expect(session?.daemonActivePaneId).toBe('pane-shell');
  });

  it('syncFromDaemonWorkspaces ignores unknown sessions and keeps defaults on invalid layout payloads', async () => {
    const sessionId = await useSessionStore.getState().createSession('Workspace', '/tmp/workspace', 'sess-workspace', 'codex', undefined, false, 'workspace-sess-workspace');

    useSessionStore.getState().syncFromDaemonWorkspaces([
      {
        id: `workspace-${sessionId}`,
        title: 'Workspace',
        directory: '/tmp/workspace',
        status: WorkspaceStatus.Idle,
        layout: {
          workspace_id: `workspace-${sessionId}`,
          active_pane_id: 'missing-pane',
          layout_json: '{not-json',
          panes: [
            { workspace_id: `workspace-${sessionId}`, pane_id: 'pane-session', kind: WorkspaceLayoutPaneKind.Agent, title: 'Agent', runtime_id: sessionId, session_id: sessionId, status: WorkspaceLayoutPaneStatus.Ready },
          ],
        },
      },
      {
        id: 'workspace-unknown-session',
        title: 'Unknown',
        directory: '/tmp/unknown',
        status: WorkspaceStatus.Idle,
        layout: {
          workspace_id: 'workspace-unknown-session',
          active_pane_id: 'pane-x',
          layout_json: '',
          panes: [
            { workspace_id: 'workspace-unknown-session', pane_id: 'pane-x', kind: WorkspaceLayoutPaneKind.Agent, title: 'Shell X', runtime_id: 'runtime-x', status: WorkspaceLayoutPaneStatus.Ready },
          ],
        },
      },
    ]);

    const session = useSessionStore.getState().sessions.find((entry) => entry.id === sessionId);
    expect(session?.workspace).toEqual({
      agents: [{ id: 'pane-session', runtimeId: sessionId, sessionId, title: 'Agent' }],
      layoutTree: null,
    });
    expect(session?.daemonActivePaneId).toBe('pane-session');
  });

  it('reloadSession preserves endpoint routing for remote sessions', async () => {
    await useSessionStore.getState().createSession('Remote', '/srv/repo', 'sess-remote', 'codex', 'ep-remote', true, 'workspace-sess-remote');

    await useSessionStore.getState().reloadSession('sess-remote', { cols: 120, rows: 40 });

    expect(mockPtyKill).toHaveBeenCalledWith({ id: 'sess-remote' });
    expect(mockPtySpawn).toHaveBeenCalledWith({
      args: expect.objectContaining({
        id: 'sess-remote',
        cwd: '/srv/repo',
        endpoint_id: 'ep-remote',
        reload: true,
        cols: 120,
        rows: 40,
        yolo_mode: true,
      }),
    });
  });
});
