import { describe, it, expect, beforeEach, vi } from 'vitest';
import { useSessionStore } from './sessions';
import { WorkspacePaneKind } from '../types/generated';

vi.mock('@tauri-apps/api/core', () => ({
  invoke: vi.fn().mockResolvedValue(undefined),
}));

vi.mock('@tauri-apps/api/event', () => ({
  listen: vi.fn().mockResolvedValue(() => {}),
}));

describe('sessions store', () => {
  beforeEach(() => {
    useSessionStore.setState({
      sessions: [],
      activeSessionId: null,
      connected: false,
      launcherConfig: { executables: {} },
    });
  });

  it('creates sessions with a default daemon-owned workspace view model', async () => {
    const sessionId = await useSessionStore.getState().createSession('test', '/tmp/test');
    const session = useSessionStore.getState().sessions.find((entry) => entry.id === sessionId);

    expect(session?.workspace).toEqual({
      terminals: [],
      layoutTree: { type: 'pane', paneId: 'main' },
    });
    expect(session?.daemonActivePaneId).toBe('main');
  });

  it('syncFromDaemonSessions hydrates canonical session data and preserves local workspace state', () => {
    useSessionStore.setState({
      sessions: [
        {
          id: 'sess-1',
          label: 'Old Label',
          state: 'working',
          cwd: '/tmp/old',
          agent: 'codex',
          transcriptMatched: false,
          daemonActivePaneId: 'pane-a',
          workspace: {
            terminals: [{ id: 'pane-a', ptyId: 'runtime-a', title: 'Shell 1' }],
            layoutTree: {
              type: 'split',
              splitId: 'root',
              direction: 'vertical',
              ratio: 0.5,
              children: [
                { type: 'pane', paneId: 'main' },
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
      branch: 'feature/workspace',
      isWorktree: true,
      daemonActivePaneId: 'pane-a',
    });
    expect(session.workspace).toMatchObject({
      terminals: [{ id: 'pane-a', ptyId: 'runtime-a', title: 'Shell 1' }],
    });
  });

  it('takeSessionSpawnArgs consumes pending fork params once and applies launcher overrides', async () => {
    const sessionId = await useSessionStore.getState().createSession('Spawn Test', '/tmp/workspace', 'sess-spawn', 'claude');
    useSessionStore.getState().setLauncherConfig({
      executables: { claude: '/opt/bin/claude-custom' },
    });
    useSessionStore.getState().setForkParams(sessionId, 'resume-123');

    const first = useSessionStore.getState().takeSessionSpawnArgs(sessionId, 120, 40);
    const second = useSessionStore.getState().takeSessionSpawnArgs(sessionId, 120, 40);

    expect(first).toMatchObject({
      id: sessionId,
      cwd: '/tmp/workspace',
      label: 'Spawn Test',
      cols: 120,
      rows: 40,
      agent: 'claude',
      executable: '/opt/bin/claude-custom',
      claude_executable: '/opt/bin/claude-custom',
      resume_session_id: 'resume-123',
      fork_session: true,
    });
    expect(second).toMatchObject({
      id: sessionId,
      resume_session_id: null,
      fork_session: null,
    });
  });

  it('syncFromDaemonWorkspaces replaces the local split workspace from daemon snapshots', async () => {
    const sessionId = await useSessionStore.getState().createSession('Workspace', '/tmp/workspace');

    useSessionStore.getState().syncFromDaemonWorkspaces([
      {
        session_id: sessionId,
        active_pane_id: 'pane-shell',
        layout_json: JSON.stringify({
          type: 'split',
          split_id: 'root',
          direction: 'vertical',
          ratio: 0.5,
          children: [
            { type: 'pane', pane_id: 'main' },
            { type: 'pane', pane_id: 'pane-shell' },
          ],
        }),
        panes: [
          { pane_id: 'main', kind: WorkspacePaneKind.Main, title: 'Session', runtime_id: sessionId },
          { pane_id: 'pane-shell', kind: WorkspacePaneKind.Shell, title: 'Shell 1', runtime_id: 'runtime-shell' },
        ],
      },
    ]);

    const session = useSessionStore.getState().sessions.find((entry) => entry.id === sessionId);
    expect(session?.workspace).toEqual({
      terminals: [
        { id: 'pane-shell', ptyId: 'runtime-shell', title: 'Shell 1' },
      ],
      layoutTree: {
        type: 'split',
        splitId: 'root',
        direction: 'vertical',
        ratio: 0.5,
        children: [
          { type: 'pane', paneId: 'main' },
          { type: 'pane', paneId: 'pane-shell' },
        ],
      },
    });
    expect(session?.daemonActivePaneId).toBe('pane-shell');
  });

  it('syncFromDaemonWorkspaces ignores unknown sessions and keeps defaults on invalid layout payloads', async () => {
    const sessionId = await useSessionStore.getState().createSession('Workspace', '/tmp/workspace');

    useSessionStore.getState().syncFromDaemonWorkspaces([
      {
        session_id: sessionId,
        active_pane_id: 'missing-pane',
        layout_json: '{not-json',
        panes: [
          { pane_id: 'main', kind: WorkspacePaneKind.Main, title: 'Session', runtime_id: sessionId },
        ],
      },
      {
        session_id: 'unknown-session',
        active_pane_id: 'pane-x',
        layout_json: '',
        panes: [
          { pane_id: 'pane-x', kind: WorkspacePaneKind.Shell, title: 'Shell X', runtime_id: 'runtime-x' },
        ],
      },
    ]);

    const session = useSessionStore.getState().sessions.find((entry) => entry.id === sessionId);
    expect(session?.workspace).toEqual({
      terminals: [],
      layoutTree: { type: 'pane', paneId: 'main' },
    });
    expect(session?.daemonActivePaneId).toBe('main');
  });
});
