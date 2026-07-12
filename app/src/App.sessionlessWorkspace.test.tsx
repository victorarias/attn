import { describe, expect, it, beforeEach, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import App from './App';
import { WHATS_NEW_ID, WHATS_NEW_STORAGE_KEY } from './hooks/useWhatsNew';
import type { TerminalLayoutNode } from './types/workspace';
import { WARM_WORKSPACE_LIMIT_STORAGE_KEY } from './utils/terminalVirtualization';

// Regression coverage for figgy's review on PR #257: a revealed tile-only
// (sessionless) workspace must be selectable and must render its docked tile,
// even though it has no session to route selection or layout through.
// https://github.com/victorarias/attn/pull/257#pullrequestreview-4413194398

const mockUseSessionStore = vi.fn();
const mockUseDaemonStore = vi.fn();
const mockUseDaemonSocket = vi.fn();
const SHOW_SESSIONLESS_KEY = 'attn.sidebar.showSessionless';

let mockDaemonWorkspaces: Array<Record<string, unknown>>;
let mockSendWorkspaceSelected: ReturnType<typeof vi.fn>;

function collectTileIds(node: TerminalLayoutNode | null): string[] {
  if (!node) {
    return [];
  }
  if (node.type === 'split') {
    return [...collectTileIds(node.children[0]), ...collectTileIds(node.children[1])];
  }
  return node.type === 'tile' ? [node.tileId] : [];
}

vi.mock('@tauri-apps/plugin-deep-link', () => ({
  onOpenUrl: vi.fn(async () => () => {}),
  getCurrent: vi.fn(async () => []),
}));
vi.mock('@tauri-apps/plugin-opener', () => ({ openUrl: vi.fn(async () => {}) }));

vi.mock('./components/GhosttyTerminal', async () => {
  const React = await import('react');
  return { GhosttyTerminal: React.forwardRef(function MockTerminal() { return null; }) };
});

// Sidebar stub: one select button per visible workspace, plus the reveal toggle.
vi.mock('./components/Sidebar', () => ({
  EditorIcon: () => null,
  WorkflowIcon: () => null,
  DiffIcon: () => null,
  PRsIcon: () => null,
  NotebookIcon: () => null,
  MarkdownIcon: () => null,
  Sidebar: ({
    visualOrder,
    selectedWorkspaceId,
    onSelectWorkspace,
    onSelectGridLayout,
  }: {
    visualOrder: Array<{ id: string; sessions: unknown[] }>;
    selectedWorkspaceId: string | null;
    onSelectWorkspace: (id: string) => void;
    onSelectGridLayout?: (layout: { mode: 'auto' }) => void;
  }) => (
    <div data-testid="sidebar" data-selected-workspace={selectedWorkspaceId ?? ''}>
      {visualOrder.map((workspace) => (
        <button
          key={workspace.id}
          data-testid={`select-${workspace.id}`}
          onClick={() => onSelectWorkspace(workspace.id)}
        >
          {workspace.id}
        </button>
      ))}
      <button
        type="button"
        data-testid="open-grid"
        onClick={() => onSelectGridLayout?.({ mode: 'auto' })}
      >
        grid
      </button>
    </div>
  ),
}));

vi.mock('./components/grid/GridView', () => ({
  GridView: ({ tiles }: { tiles: Array<{ runtimeId: string }> }) => (
    <div data-testid="grid-view" data-runtime-ids={tiles.map((tile) => tile.runtimeId).join(',')} />
  ),
}));

// SessionTerminalWorkspace stub: surface just enough to assert which workspace is
// active and what layout it was handed.
vi.mock('./components/SessionTerminalWorkspace', () => ({
  SessionTerminalWorkspace: ({
    workspaceId,
    workspace,
    isActiveSession,
    terminalsLive,
  }: {
    workspaceId: string;
    workspace: { agents: unknown[]; layoutTree: TerminalLayoutNode | null };
    isActiveSession: boolean;
    terminalsLive?: boolean;
  }) => (
    <div
      data-testid={`workspace-${workspaceId}`}
      data-active={isActiveSession ? '1' : '0'}
      data-live={terminalsLive === false ? '0' : '1'}
      data-agent-count={workspace.agents.length}
      data-tile-ids={collectTileIds(workspace.layoutTree).join(',')}
    />
  ),
}));

vi.mock('./components/Dashboard', () => ({ Dashboard: () => null }));
vi.mock('./components/AttentionDrawer', () => ({ AttentionDrawer: () => null }));
vi.mock('./components/LocationPicker', () => ({ LocationPicker: () => null }));
vi.mock('./components/UndoToast', () => ({ UndoToast: () => null }));
vi.mock('./components/ErrorToast', () => ({
  ErrorToast: () => null,
  useErrorToast: () => ({ message: null, showError: vi.fn(), clearError: vi.fn() }),
}));
vi.mock('./hooks/useKeyboardShortcuts', () => ({ useKeyboardShortcuts: vi.fn() }));
vi.mock('./hooks/useUIScale', () => ({
  useUIScale: () => ({ scale: 1, increaseScale: vi.fn(), decreaseScale: vi.fn(), resetScale: vi.fn() }),
}));
vi.mock('./hooks/useOpenPR', () => ({ useOpenPR: () => vi.fn() }));
vi.mock('./hooks/usePRsNeedingAttention', () => ({ usePRsNeedingAttention: () => ({ needsAttention: [] }) }));
vi.mock('./store/sessions', () => ({ useSessionStore: () => mockUseSessionStore() }));
vi.mock('./store/daemonSessions', () => ({ useDaemonStore: () => mockUseDaemonStore() }));
vi.mock('./hooks/useDaemonSocket', async () => {
  const React = await import('react');
  return {
    useDaemonSocket: (args: { onWorkspacesUpdate?: (workspaces: unknown[]) => void }) => {
      React.useEffect(() => {
        args.onWorkspacesUpdate?.(mockDaemonWorkspaces);
      }, []);
      return mockUseDaemonSocket(args);
    },
  };
});
vi.mock('./pty/bridge', async () => {
  const actual = await vi.importActual<typeof import('./pty/bridge')>('./pty/bridge');
  return { ...actual, ptySpawn: vi.fn(async () => {}) };
});

const TILE_LAYOUT_JSON = JSON.stringify({
  type: 'tile',
  tile_id: 'tile-readme',
  tile_kind: 'markdown',
  tile_params: '/tmp/project/README.md',
});

describe('tile-only (sessionless) workspace selection and render', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(WHATS_NEW_STORAGE_KEY, WHATS_NEW_ID);
    // Reveal sessionless workspaces in the sidebar (hidden by default).
    localStorage.setItem(SHOW_SESSIONLESS_KEY, '1');
    mockSendWorkspaceSelected = vi.fn();

    mockDaemonWorkspaces = [
      {
        id: 'ws-session',
        title: 'working-session',
        directory: '/tmp/repo',
        status: 'active',
        layout: {
          active_pane_id: 'pane-s1',
          layout_json: JSON.stringify({ type: 'pane', pane_id: 'pane-s1' }),
          panes: [{
            workspace_id: 'ws-session',
            pane_id: 'pane-s1',
            kind: 'agent',
            runtime_id: 's1',
            session_id: 's1',
            title: 'working-session',
          }],
        },
      },
      {
        id: 'ws-tiles',
        title: 'Notes',
        directory: '/tmp/repo',
        status: 'active',
        layout: {
          active_pane_id: '',
          layout_json: TILE_LAYOUT_JSON,
          panes: [],
        },
      },
    ];

    mockUseSessionStore.mockReturnValue({
      sessions: [{
        id: 's1',
        label: 'working-session',
        state: 'working',
        cwd: '/tmp/repo',
        workspaceId: 'ws-session',
        agent: 'claude',
        transcriptMatched: true,
        daemonActivePaneId: 'pane-s1',
        workspace: {
          agents: [{ id: 'pane-s1', runtimeId: 's1', sessionId: 's1', title: 'working-session' }],
          layoutTree: { type: 'pane', paneId: 'pane-s1' },
        },
      }],
      activeSessionId: 's1',
      connect: vi.fn(async () => {}),
      connected: true,
      launcherConfig: { executables: {} },
      createSession: vi.fn(async () => 's1'),
      closeSession: vi.fn(),
      setActiveSession: vi.fn(),
      takeSessionSpawnArgs: vi.fn(() => null),
      reloadSession: vi.fn(async () => {}),
      setLauncherConfig: vi.fn(),
      syncFromDaemonSessions: vi.fn(),
      syncFromDaemonWorkspaces: vi.fn(),
    });

    mockUseDaemonStore.mockReturnValue({
      daemonSessions: [{ id: 's1', label: 'working-session', directory: '/tmp/repo', state: 'working' }],
      setDaemonSessions: vi.fn(),
      prs: [], setPRs: vi.fn(),
      repoStates: [], setRepoStates: vi.fn(),
      authorStates: [], setAuthorStates: vi.fn(),
    });

    const fn = vi.fn();
    mockUseDaemonSocket.mockReturnValue({
      sendPRAction: fn, sendMutePR: fn, sendMuteRepo: fn, sendMuteAuthor: fn, sendPRVisited: fn,
      sendRefreshPRs: vi.fn(async () => ({ success: true })),
      sendUnregisterSession: fn, sendRegisterWorkspace: fn,
      sendUnregisterWorkspace: vi.fn(async () => {}),
      sendMuteWorkspace: vi.fn(async () => ({ success: true })),
      sendSetSetting: fn,
      sendCreateWorktree: vi.fn(async () => ({ success: true, path: '/tmp/new' })),
      sendDeleteWorktree: vi.fn(async () => ({ success: true })),
      sendGetRecentLocations: vi.fn(async () => ({ success: true, locations: [] })),
      sendCreateWorktreeFromBranch: vi.fn(async () => ({ success: true, path: '/tmp/new' })),
      sendFetchRemotes: vi.fn(async () => ({ success: true })),
      sendFetchPRDetails: vi.fn(async () => ({ success: true })),
      sendEnsureRepo: vi.fn(async () => ({ success: true, path: '/tmp/repo' })),
      sendSubscribeGitStatus: fn, sendUnsubscribeGitStatus: fn,
      sendSessionSelected: fn, sendWorkspaceSelected: mockSendWorkspaceSelected, sendSessionVisualized: fn,
      sendWorkspaceClosePane: vi.fn(async () => ({ success: true })),
      sendWorkspaceAddSessionPane: vi.fn(async () => ({ success: true })),
      requestTileContent: fn,
      sendGetFileDiff: vi.fn(async () => ({ success: true, original: '', modified: '' })),
      getRepoInfo: vi.fn(async () => ({ success: true, is_git_repo: true, branch: 'main' })),
      listWorkflowRuns: vi.fn(async () => ({ success: true, runs: [] })),
      getPresentations: vi.fn(async () => []),
      connectionError: null,
      hasReceivedInitialState: true,
      sendNotificationList: vi.fn(async () => ({ notifications: [], unreadCount: 0 })),
      sendNotificationMarkRead: vi.fn(async () => 0),
      rateLimit: null,
      warnings: [],
      clearWarnings: fn,
      sendSetTerminalTheme: fn,
    });
  });

  it('renders the tile-only workspace layout but leaves it inactive until selected', async () => {
    render(<App />);

    // The tile-only workspace renders from the daemon layout (no agent panes,
    // one docked tile) even though no session carries it.
    const tileWorkspace = await screen.findByTestId('workspace-ws-tiles');
    expect(tileWorkspace.getAttribute('data-agent-count')).toBe('0');
    expect(tileWorkspace.getAttribute('data-tile-ids')).toBe('tile-readme');

    // The session-backed workspace is the active one to start.
    expect(screen.getByTestId('workspace-ws-session').getAttribute('data-active')).toBe('1');
    expect(tileWorkspace.getAttribute('data-active')).toBe('0');
  });

  it('activates the tile-only workspace when it is selected from the sidebar', async () => {
    render(<App />);
    await screen.findByTestId('workspace-ws-tiles');

    await userEvent.click(screen.getByTestId('select-ws-tiles'));

    await waitFor(() => {
      expect(screen.getByTestId('workspace-ws-tiles').getAttribute('data-active')).toBe('1');
    });
    // The previously active session workspace yields.
    expect(screen.getByTestId('workspace-ws-session').getAttribute('data-active')).toBe('0');
    expect(screen.getByTestId('sidebar').getAttribute('data-selected-workspace')).toBe('ws-tiles');
    expect(mockSendWorkspaceSelected).toHaveBeenLastCalledWith('ws-tiles');
    // Still rendering its docked tile.
    expect(screen.getByTestId('workspace-ws-tiles').getAttribute('data-tile-ids')).toBe('tile-readme');
  });

  it('keeps visible grid workspaces mounted even when they are cold and idle', async () => {
    localStorage.setItem(WARM_WORKSPACE_LIMIT_STORAGE_KEY, '0');
    mockDaemonWorkspaces = [
      {
        id: 'ws-one',
        title: 'one',
        directory: '/tmp/repo',
        status: 'active',
        layout: {
          active_pane_id: 'pane-one',
          layout_json: JSON.stringify({ type: 'pane', pane_id: 'pane-one' }),
          panes: [{ workspace_id: 'ws-one', pane_id: 'pane-one', kind: 'agent', runtime_id: 's1', session_id: 's1', title: 'one' }],
        },
      },
      {
        id: 'ws-two',
        title: 'two',
        directory: '/tmp/repo',
        status: 'active',
        layout: {
          active_pane_id: 'pane-two',
          layout_json: JSON.stringify({ type: 'pane', pane_id: 'pane-two' }),
          panes: [{ workspace_id: 'ws-two', pane_id: 'pane-two', kind: 'agent', runtime_id: 's2', session_id: 's2', title: 'two' }],
        },
      },
      {
        id: 'ws-three',
        title: 'three',
        directory: '/tmp/repo',
        status: 'active',
        layout: {
          active_pane_id: 'pane-three',
          layout_json: JSON.stringify({ type: 'pane', pane_id: 'pane-three' }),
          panes: [{ workspace_id: 'ws-three', pane_id: 'pane-three', kind: 'agent', runtime_id: 's3', session_id: 's3', title: 'three' }],
        },
      },
    ];
    const sessions = ['one', 'two', 'three'].map((name, index) => {
      const sessionId = `s${index + 1}`;
      const paneId = `pane-${name}`;
      const workspaceId = `ws-${name}`;
      return {
        id: sessionId,
        label: name,
        state: 'idle',
        cwd: '/tmp/repo',
        workspaceId,
        agent: 'claude',
        transcriptMatched: true,
        daemonActivePaneId: paneId,
        workspace: {
          agents: [{ id: paneId, runtimeId: sessionId, sessionId, title: name }],
          layoutTree: { type: 'pane' as const, paneId },
        },
      };
    });
    mockUseSessionStore.mockReturnValue({
      ...mockUseSessionStore(),
      sessions,
      activeSessionId: 's1',
    });
    mockUseDaemonStore.mockReturnValue({
      ...mockUseDaemonStore(),
      daemonSessions: sessions.map((session) => ({
        id: session.id,
        label: session.label,
        directory: session.cwd,
        state: 'idle',
      })),
    });

    render(<App />);

    const coldWorkspace = await screen.findByTestId('workspace-ws-two');
    expect(coldWorkspace.getAttribute('data-live')).toBe('0');

    await userEvent.click(screen.getByTestId('open-grid'));

    await waitFor(() => {
      expect(screen.getByTestId('grid-view').getAttribute('data-runtime-ids')).toContain('s2');
      expect(screen.getByTestId('workspace-ws-two').getAttribute('data-live')).toBe('1');
      expect(screen.getByTestId('workspace-ws-three').getAttribute('data-live')).toBe('1');
    });
  });

  // The daemon-side worker answers OSC 10/11/12 color queries from the theme
  // the app pushes down, so App must push it once the socket handshake
  // completes. Unrelated to the sessionless-workspace regression above, but
  // reuses the same App-rendering harness.
  it('sends the resolved terminal theme once the daemon handshake completes', async () => {
    render(<App />);

    await waitFor(() => {
      expect(mockUseDaemonSocket.mock.results[0]?.value.sendSetTerminalTheme).toHaveBeenCalledWith({
        foreground: '#d4d4d4',
        background: '#1e1e1e',
        cursor: '#d4d4d4',
      });
    });
  });
});
