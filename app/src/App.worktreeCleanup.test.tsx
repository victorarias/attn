import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import App from './App';
import { WHATS_NEW_ID, WHATS_NEW_STORAGE_KEY } from './hooks/useWhatsNew';

const mockUseSessionStore = vi.fn();
const mockUseDaemonStore = vi.fn();
const mockUseDaemonSocket = vi.fn();
const mockSendUnregisterSession = vi.fn();
const mockSendRegisterWorkspace = vi.fn();
const mockSendUnregisterWorkspace = vi.fn(async () => {});
const mockUseKeyboardShortcuts = vi.fn();
const mockSendWorkspaceClosePane = vi.fn(async () => ({ success: true }));
const mockCloseSession = vi.fn();
const mockOpenPR = vi.hoisted(() => vi.fn());
const { mockPtySpawn } = vi.hoisted(() => ({
  mockPtySpawn: vi.fn(async () => {}),
}));
let mockSessionStoreReturn: Record<string, unknown>;
let mockDaemonStoreReturn: Record<string, unknown>;
let mockDaemonSocketReturn: Record<string, unknown>;

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((promiseResolve, promiseReject) => {
    resolve = promiseResolve;
    reject = promiseReject;
  });
  return { promise, resolve, reject };
}

function buildMockDaemonWorkspaces() {
  const sessions = (mockUseSessionStore()?.sessions ?? []) as Array<{
    id: string;
    label: string;
    cwd: string;
    workspaceId: string;
    workspace?: {
      agents?: Array<{ id: string; sessionId?: string }>;
    };
  }>;
  const byWorkspace = new Map<string, typeof sessions>();
  for (const session of sessions) {
    const current = byWorkspace.get(session.workspaceId) ?? [];
    current.push(session);
    byWorkspace.set(session.workspaceId, current);
  }
  return Array.from(byWorkspace.entries()).map(([workspaceId, workspaceSessions]) => {
    const firstSession = workspaceSessions[0];
    const paneIds = new Set<string>();
    const panes = workspaceSessions.flatMap((session) => (
      session.workspace?.agents ?? [{ id: `pane-${session.id}`, sessionId: session.id }]
    ))
      .filter((pane) => {
        if (paneIds.has(pane.id)) {
          return false;
        }
        paneIds.add(pane.id);
        return true;
      })
      .map((pane) => ({
        workspace_id: workspaceId,
        pane_id: pane.id,
        kind: 'agent',
        runtime_id: pane.sessionId ?? pane.id,
        session_id: pane.sessionId ?? pane.id,
        title: pane.sessionId ?? pane.id,
      }));
    return {
      id: workspaceId,
      title: firstSession.label,
      directory: firstSession.cwd,
      status: 'active',
      layout: {
        active_pane_id: panes[0]?.pane_id ?? '',
        layout_json: '',
        panes,
      },
    };
  });
}

vi.mock('@tauri-apps/plugin-deep-link', () => ({
  onOpenUrl: vi.fn(async () => () => {}),
  getCurrent: vi.fn(async () => []),
}));

vi.mock('@tauri-apps/plugin-opener', () => ({
  openUrl: vi.fn(async () => {}),
}));

vi.mock('./components/GhosttyTerminal', async () => {
  const React = await import('react');
  return {
    GhosttyTerminal: React.forwardRef(function MockTerminal() {
      return null;
    }),
  };
});

vi.mock('./components/Sidebar', () => ({
  EditorIcon: () => null,
  WorkflowIcon: () => null,
  DiffIcon: () => null,
  PRsIcon: () => null,
  NotebookIcon: () => null,
  MarkdownIcon: () => null,
  Sidebar: ({
    selectedId,
    visualOrder,
    onCloseSession,
  }: {
    selectedId: string | null;
    visualOrder: Array<{ firstSessionId: string | null }>;
    onCloseSession: (id: string) => void;
  }) => (
    <button data-testid="close-session" onClick={() => (visualOrder[0]?.firstSessionId ?? selectedId) && onCloseSession((visualOrder[0]?.firstSessionId ?? selectedId)!)}>
      Close Session
    </button>
  ),
}));

vi.mock('./components/Dashboard', () => ({
  Dashboard: ({ onOpenPR }: { onOpenPR?: (pr: Record<string, unknown>) => void }) => (
    <button
      data-testid="open-pr"
      onClick={() => onOpenPR?.({
        approved_by_me: false,
        author: 'octo',
        details_fetched: true,
        has_new_changes: false,
        head_branch: 'feature/slow-pr',
        host: 'github.com',
        id: 'pr-1',
        last_polled: '2026-05-16T00:00:00Z',
        last_updated: '2026-05-16T00:00:00Z',
        muted: false,
        number: 42,
        reason: 'review_requested',
        repo: 'acme/widgets',
        role: 'reviewer',
        state: 'open',
        title: 'Make widgets faster',
        url: 'https://github.com/acme/widgets/pull/42',
      })}
    >
      Open PR
    </button>
  ),
}));
vi.mock('./components/AttentionDrawer', () => ({ AttentionDrawer: () => null }));
vi.mock('./components/LocationPicker', () => ({
  LocationPicker: ({
    isOpen,
    onClose,
    onCreateWorktreeSession,
  }: {
    isOpen: boolean;
    onClose: () => void;
    onCreateWorktreeSession?: (
      mainRepo: string,
      branch: string,
      startingFrom: string,
      endpointId: string | undefined,
      agent: 'codex',
      yoloMode: boolean,
    ) => void;
  }) => (
    isOpen ? (
      <button
        data-testid="mock-create-worktree-session"
        onClick={() => {
          onCreateWorktreeSession?.('/tmp/repo', 'feat-async', 'main', undefined, 'codex', false);
          onClose();
        }}
      >
        Create Worktree Session
      </button>
    ) : null
  ),
}));
vi.mock('./components/UndoToast', () => ({ UndoToast: () => null }));
vi.mock('./components/ChangesPanel', () => ({ ChangesPanel: () => null }));
vi.mock('./components/DiffDetailPanel', () => ({ DiffDetailPanel: () => null }));
vi.mock('./components/SessionTerminalWorkspace', () => ({ SessionTerminalWorkspace: () => null }));

vi.mock('./components/ErrorToast', () => ({
  ErrorToast: () => null,
  useErrorToast: () => ({
    message: null,
    showError: vi.fn(),
    clearError: vi.fn(),
  }),
}));

vi.mock('./hooks/useKeyboardShortcuts', () => ({
  useKeyboardShortcuts: (args: unknown) => mockUseKeyboardShortcuts(args),
}));

vi.mock('./hooks/useUIScale', () => ({
  useUIScale: () => ({
    scale: 1,
    increaseScale: vi.fn(),
    decreaseScale: vi.fn(),
    resetScale: vi.fn(),
  }),
}));

vi.mock('./hooks/useOpenPR', () => ({
  useOpenPR: () => mockOpenPR,
}));

vi.mock('./hooks/usePRsNeedingAttention', () => ({
  usePRsNeedingAttention: () => ({ needsAttention: [] }),
}));

vi.mock('./store/sessions', () => ({
  useSessionStore: () => mockUseSessionStore(),
}));

vi.mock('./store/daemonSessions', () => ({
  useDaemonStore: () => mockUseDaemonStore(),
}));

vi.mock('./hooks/useDaemonSocket', async () => {
  const React = await import('react');
  return {
    useDaemonSocket: (args: { onWorkspacesUpdate?: (workspaces: ReturnType<typeof buildMockDaemonWorkspaces>) => void }) => {
      React.useEffect(() => {
        args.onWorkspacesUpdate?.(buildMockDaemonWorkspaces());
      }, []);
      return mockUseDaemonSocket(args);
    },
  };
});

vi.mock('./pty/bridge', async () => {
  const actual = await vi.importActual<typeof import('./pty/bridge')>('./pty/bridge');
  return {
    ...actual,
    ptySpawn: mockPtySpawn,
  };
});

describe('worktree cleanup prompt', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    // Keep the one-time "what's new" announcement suppressed; it is unrelated
    // to worktree cleanup and would otherwise render over the app under test.
    localStorage.setItem(WHATS_NEW_STORAGE_KEY, WHATS_NEW_ID);
    vi.useRealTimers();
    mockOpenPR.mockResolvedValue({
      success: true,
      worktreePath: '/tmp/wt',
      sessionId: 's1',
    });
    mockSendWorkspaceClosePane.mockResolvedValue({ success: true });

    mockSessionStoreReturn = {
      sessions: [
        {
          id: 's1',
          label: 'worktree-session',
          state: 'working',
          cwd: '/tmp/repo/.worktrees/feature-a',
          workspaceId: 'workspace-s1',
          agent: 'claude',
          transcriptMatched: true,
          branch: 'feature-a',
          isWorktree: true,
          daemonActivePaneId: 'main',
          workspace: {
            agents: [{ id: 'pane-session', runtimeId: 's1', sessionId: 's1', title: 'worktree-session' }],
            layoutTree: { type: 'pane', paneId: 'pane-session' },
          },
        },
      ],
      activeSessionId: 's1',
      connect: vi.fn(async () => {}),
      connected: true,
      launcherConfig: { executables: {} },
      createSession: vi.fn(async () => 's1'),
      closeSession: mockCloseSession,
      setActiveSession: vi.fn(),
      takeSessionSpawnArgs: vi.fn(() => null),
      reloadSession: vi.fn(async () => {}),
      setLauncherConfig: vi.fn(),
      syncFromDaemonSessions: vi.fn(),
      syncFromDaemonWorkspaces: vi.fn(),
    };
    mockUseSessionStore.mockReturnValue(mockSessionStoreReturn);

    mockDaemonStoreReturn = {
      daemonSessions: [
        {
          id: 's1',
          label: 'worktree-session',
          directory: '/tmp/repo/.worktrees/feature-a',
          state: 'working',
          branch: 'feature-a',
          is_worktree: true,
        },
      ],
      setDaemonSessions: vi.fn(),
      prs: [],
      setPRs: vi.fn(),
      repoStates: [],
      setRepoStates: vi.fn(),
      authorStates: [],
      setAuthorStates: vi.fn(),
    };
    mockUseDaemonStore.mockReturnValue(mockDaemonStoreReturn);

    const fn = vi.fn();
    mockDaemonSocketReturn = {
      sendPRAction: fn,
      sendMutePR: fn,
      sendMuteRepo: fn,
      sendMuteAuthor: fn,
      sendPRVisited: fn,
      sendRefreshPRs: vi.fn(async () => ({ success: true })),
      sendUnregisterSession: mockSendUnregisterSession,
      sendRegisterWorkspace: mockSendRegisterWorkspace,
      sendUnregisterWorkspace: mockSendUnregisterWorkspace,
      sendSetSetting: fn,
      sendCreateWorktree: vi.fn(async () => ({ success: true, path: '/tmp/new' })),
      sendDeleteWorktree: vi.fn(async () => ({ success: true })),
      sendGetRecentLocations: vi.fn(async () => ({ success: true, locations: [] })),
      sendCreateWorktreeFromBranch: vi.fn(async () => ({ success: true, path: '/tmp/new' })),
      sendFetchRemotes: vi.fn(async () => ({ success: true })),
      sendFetchPRDetails: vi.fn(async () => ({ success: true })),
      sendEnsureRepo: vi.fn(async () => ({ success: true, path: '/tmp/repo' })),
      sendSubscribeGitStatus: fn,
      sendUnsubscribeGitStatus: fn,
      sendSessionSelected: fn,
      sendWorkspaceSelected: fn,
      sendSessionVisualized: fn,
      sendWorkspaceClosePane: mockSendWorkspaceClosePane,
      sendWorkspaceAddSessionPane: vi.fn(async () => ({ success: true })),
      sendGetFileDiff: vi.fn(async () => ({ success: true, original: '', modified: '' })),
      sendGetBranchDiffFiles: vi.fn(async () => ({ success: true, base_ref: 'main', files: [] })),
      getRepoInfo: vi.fn(async () => ({ success: true, is_git_repo: true, branch: 'main' })),
      listWorkflowRuns: vi.fn(async () => ({ success: true, runs: [] })),
      getReviewState: vi.fn(async () => ({ success: true })),
      markFileViewed: vi.fn(async () => ({ success: true })),
      sendAddComment: vi.fn(async () => ({ success: true })),
      sendUpdateComment: vi.fn(async () => ({ success: true })),
      sendResolveComment: vi.fn(async () => ({ success: true })),
      sendDeleteComment: vi.fn(async () => ({ success: true })),
      sendGetComments: vi.fn(async () => ({ success: true, comments: [] })),
      connectionError: null,
      hasReceivedInitialState: true,
      sendNotificationList: vi.fn(async () => ({ notifications: [], unreadCount: 0 })),
      sendNotificationMarkRead: vi.fn(async () => 0),
      rateLimit: null,
      warnings: [],
      clearWarnings: fn,
    };
    mockUseDaemonSocket.mockReturnValue(mockDaemonSocketReturn);
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('shows worktree cleanup prompt when closing a worktree session', async () => {
    localStorage.setItem('alwaysKeepWorktrees', 'true');
    render(<App />);

    await userEvent.click(screen.getByTestId('close-session'));

    await waitFor(() => {
      expect(screen.getByRole('dialog')).toBeInTheDocument();
    });
  });

  it('moves worktree-backed session creation out of the picker', async () => {
    const createWorktree = deferred<{ success: true; path: string }>();
    const createSession = vi.fn(async () => 'created-1');
    mockSessionStoreReturn.sessions = [];
    mockSessionStoreReturn.activeSessionId = null;
    mockSessionStoreReturn.createSession = createSession;
    mockSessionStoreReturn.takeSessionSpawnArgs = vi.fn((id: string) => ({
      id,
      cwd: '/tmp/repo--feat-async',
      cols: 80,
      rows: 24,
      label: 'repo--feat-async',
      agent: 'codex',
      workspace_id: `workspace-${id}`,
    }));
    mockDaemonStoreReturn.daemonSessions = [];
    mockDaemonSocketReturn.sendCreateWorktree = vi.fn(() => createWorktree.promise);

    const { rerender } = render(<App />);

    const keyboardArgs = mockUseKeyboardShortcuts.mock.calls[mockUseKeyboardShortcuts.mock.calls.length - 1]?.[0] as {
      onNewSession?: () => void;
    };
    act(() => {
      keyboardArgs.onNewSession?.();
    });

    await userEvent.click(screen.getByTestId('mock-create-worktree-session'));

    expect(screen.queryByTestId('mock-create-worktree-session')).not.toBeInTheDocument();
    expect(screen.getByRole('dialog')).toHaveTextContent('Creating worktree');
    expect(mockDaemonSocketReturn.sendCreateWorktree).toHaveBeenCalledWith('/tmp/repo', 'feat-async', undefined, 'main', undefined);

    act(() => {
      keyboardArgs.onNewSession?.();
    });
    await userEvent.click(screen.getByTestId('mock-create-worktree-session'));
    expect(mockDaemonSocketReturn.sendCreateWorktree).toHaveBeenCalledTimes(1);

    await act(async () => {
      createWorktree.resolve({ success: true, path: '/tmp/repo--feat-async' });
      await createWorktree.promise;
    });

    await waitFor(() => {
      expect(createSession).toHaveBeenCalledWith(
        'repo--feat-async',
        '/tmp/repo--feat-async',
        expect.any(String),
        'codex',
        undefined,
        false,
        expect.stringMatching(/^workspace-/),
        undefined,
      );
    });
    expect(screen.getByRole('dialog')).toHaveTextContent('Starting session');

    mockDaemonStoreReturn.daemonSessions = [{ id: 'created-1', label: 'repo--feat-async', directory: '/tmp/repo--feat-async', state: 'launching' }];
    rerender(<App />);

    await waitFor(() => {
      expect(screen.queryByText('Starting session')).not.toBeInTheDocument();
    });
  });

  it('keeps Cmd+N worktree-backed sessions in the current workspace', async () => {
    const createSession = vi.fn(async (_label: string, _cwd: string, id: string) => id);
    (mockSessionStoreReturn.sessions as Array<{ endpointId?: string }>)[0].endpointId = 'ep-remote';
    mockSessionStoreReturn.createSession = createSession;
    mockSessionStoreReturn.takeSessionSpawnArgs = vi.fn((id: string) => ({
      id,
      cwd: '/tmp/repo--feat-async',
      cols: 80,
      rows: 24,
      label: 'repo--feat-async',
      agent: 'codex',
      workspace_id: 'workspace-s1',
    }));
    mockDaemonSocketReturn.sendCreateWorktree = vi.fn(async () => ({
      success: true,
      path: '/tmp/repo--feat-async',
    }));

    render(<App />);

    const keyboardArgs = mockUseKeyboardShortcuts.mock.calls[mockUseKeyboardShortcuts.mock.calls.length - 1]?.[0] as {
      onNewSession?: () => void;
    };
    act(() => {
      keyboardArgs.onNewSession?.();
    });

    await userEvent.click(screen.getByTestId('mock-create-worktree-session'));

    await waitFor(() => {
      expect(createSession).toHaveBeenCalledWith(
        'repo--feat-async',
        '/tmp/repo--feat-async',
        expect.any(String),
        'codex',
        undefined,
        false,
        'workspace-s1',
      );
    });
    const sessionId = createSession.mock.calls[0][2];
    expect(mockSendRegisterWorkspace).not.toHaveBeenCalled();
    expect(mockDaemonSocketReturn.sendWorkspaceAddSessionPane).toHaveBeenCalledWith(
      'workspace-s1',
      sessionId,
      'repo--feat-async',
      {
        paneId: `pane-${sessionId}`,
        targetPaneId: 'pane-session',
        direction: 'vertical',
      },
    );
    await waitFor(() => {
      expect(screen.queryByText('Starting session')).not.toBeInTheDocument();
    });
  });

  it('disables app shortcuts while the first-run whats-new modal is open', async () => {
    localStorage.removeItem(WHATS_NEW_STORAGE_KEY);

    render(<App />);

    await waitFor(() => {
      expect(screen.getByRole('dialog')).toHaveTextContent('attn is organized around workspaces');
    });

    await waitFor(() => {
      const keyboardArgs = mockUseKeyboardShortcuts.mock.calls[mockUseKeyboardShortcuts.mock.calls.length - 1]?.[0] as {
        enabled?: boolean;
      };
      expect(keyboardArgs.enabled).toBe(false);
    });
  });

  it('does not refresh Changes branch diff while the Changes panel is closed', async () => {
    const sendGetBranchDiffFiles = vi.fn(async () => ({ success: true, base_ref: 'main', files: [] }));
    mockDaemonSocketReturn.sendGetBranchDiffFiles = sendGetBranchDiffFiles;

    render(<App />);
    await act(async () => {
      await Promise.resolve();
    });

    expect(sendGetBranchDiffFiles).not.toHaveBeenCalled();

    const daemonSocketArgs = mockUseDaemonSocket.mock.calls[mockUseDaemonSocket.mock.calls.length - 1]?.[0] as {
      onGitStatusUpdate?: (status: { directory: string; staged: unknown[]; unstaged: unknown[]; untracked: unknown[] }) => void;
    };
    act(() => {
      daemonSocketArgs.onGitStatusUpdate?.({
        directory: '/tmp/repo/.worktrees/feature-a',
        staged: [],
        unstaged: [],
        untracked: [],
      });
    });

    await act(async () => {
      await Promise.resolve();
    });

    expect(sendGetBranchDiffFiles).not.toHaveBeenCalled();
  });

  it('refreshes Changes branch diff on open and coalesces status updates while in flight', async () => {
    const firstBranchDiff = deferred<{ success: true; base_ref: string; files: [] }>();
    const sendGetBranchDiffFiles = vi.fn()
      .mockReturnValueOnce(firstBranchDiff.promise)
      .mockResolvedValue({ success: true, base_ref: 'main', files: [] });
    mockDaemonSocketReturn.sendGetBranchDiffFiles = sendGetBranchDiffFiles;

    render(<App />);

    const keyboardArgs = mockUseKeyboardShortcuts.mock.calls[mockUseKeyboardShortcuts.mock.calls.length - 1]?.[0] as {
      onToggleDiffPanel?: () => void;
    };
    act(() => {
      keyboardArgs.onToggleDiffPanel?.();
    });

    await waitFor(() => {
      expect(sendGetBranchDiffFiles).toHaveBeenCalledTimes(1);
    });

    const daemonSocketArgs = mockUseDaemonSocket.mock.calls[mockUseDaemonSocket.mock.calls.length - 1]?.[0] as {
      onGitStatusUpdate?: (status: { directory: string; staged: unknown[]; unstaged: unknown[]; untracked: unknown[] }) => void;
    };
    act(() => {
      daemonSocketArgs.onGitStatusUpdate?.({
        directory: '/tmp/repo/.worktrees/feature-a',
        staged: [],
        unstaged: [{ path: 'app/src/App.tsx' }],
        untracked: [],
      });
      daemonSocketArgs.onGitStatusUpdate?.({
        directory: '/tmp/repo/.worktrees/feature-a',
        staged: [],
        unstaged: [{ path: 'app/src/App.tsx' }, { path: 'internal/git/command.go' }],
        untracked: [],
      });
    });

    expect(sendGetBranchDiffFiles).toHaveBeenCalledTimes(1);

    await act(async () => {
      firstBranchDiff.resolve({ success: true, base_ref: 'main', files: [] });
      await firstBranchDiff.promise;
    });

    await waitFor(() => {
      expect(sendGetBranchDiffFiles).toHaveBeenCalledTimes(2);
    });
  });

  it('ignores stale delete completion after another worktree prompt becomes active', async () => {
    const deleteA = deferred<{ success: true }>();
    const sendDeleteWorktree = vi.fn((path: string) => {
      if (path === '/tmp/repo/.worktrees/feature-a') {
        return deleteA.promise;
      }
      return Promise.resolve({ success: true });
    });
    mockDaemonSocketReturn.sendDeleteWorktree = sendDeleteWorktree;

    const { rerender } = render(<App />);

    await userEvent.click(screen.getByTestId('close-session'));
    await waitFor(() => {
      expect(screen.getByText('/tmp/repo/.worktrees/feature-a')).toBeInTheDocument();
    });

    await userEvent.click(screen.getByRole('button', { name: 'Delete worktree' }));
    await waitFor(() => {
      expect(sendDeleteWorktree).toHaveBeenCalledWith('/tmp/repo/.worktrees/feature-a');
    });

    mockSessionStoreReturn.sessions = [
      {
        id: 's2',
        label: 'second-worktree-session',
        state: 'working',
        cwd: '/tmp/repo/.worktrees/feature-b',
        workspaceId: 'workspace-s2',
        agent: 'claude',
        transcriptMatched: true,
        branch: 'feature-b',
        isWorktree: true,
        daemonActivePaneId: 'main',
        workspace: {
          agents: [{ id: 'pane-session', runtimeId: 's2', sessionId: 's2', title: 'second-worktree-session' }],
          layoutTree: { type: 'pane', paneId: 'pane-session' },
        },
      },
    ];
    mockSessionStoreReturn.activeSessionId = 's2';
    mockDaemonStoreReturn.daemonSessions = [
      {
        id: 's2',
        label: 'second-worktree-session',
        directory: '/tmp/repo/.worktrees/feature-b',
        state: 'working',
        branch: 'feature-b',
        is_worktree: true,
      },
    ];

    rerender(<App />);

    await userEvent.click(screen.getByTestId('close-session'));
    await waitFor(() => {
      expect(screen.getByText('/tmp/repo/.worktrees/feature-b')).toBeInTheDocument();
    });

    await act(async () => {
      deleteA.resolve({ success: true });
      await deleteA.promise;
    });

    expect(screen.getByText('/tmp/repo/.worktrees/feature-b')).toBeInTheDocument();
    expect(screen.queryByText('/tmp/repo/.worktrees/feature-a')).not.toBeInTheDocument();
  });

  it('retries cleanup delete with force after a forceable failure', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
    const forceableError = Object.assign(new Error('contains modified or untracked files'), {
      forceable: true,
    });
    const sendDeleteWorktree = vi.fn((_path: string, _endpointId?: string, options?: { force?: boolean }) => {
      if (!options?.force) {
        return Promise.reject(forceableError);
      }
      return Promise.resolve({ success: true });
    });
    mockDaemonSocketReturn.sendDeleteWorktree = sendDeleteWorktree;

    render(<App />);

    await userEvent.click(screen.getByTestId('close-session'));
    await waitFor(() => {
      expect(screen.getByText('/tmp/repo/.worktrees/feature-a')).toBeInTheDocument();
    });

    await userEvent.click(screen.getByRole('button', { name: 'Delete worktree' }));

    expect(await screen.findByRole('button', { name: 'Force delete' })).toBeInTheDocument();
    expect(screen.getByText(/contains modified or untracked files/)).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: 'Force delete' }));

    await waitFor(() => {
      expect(sendDeleteWorktree).toHaveBeenLastCalledWith('/tmp/repo/.worktrees/feature-a', undefined, { force: true });
    });
    consoleError.mockRestore();
  });

  it('marks remote long-run sessions as visualized after the visibility delay', async () => {
    vi.useFakeTimers();
    const sendSessionVisualized = vi.fn();
    const sendSessionSelected = vi.fn();
    const fn = vi.fn();

    mockUseSessionStore.mockReturnValue({
      sessions: [
        {
          id: 'remote-1',
          label: 'remote-session',
          state: 'waiting_input',
          cwd: '/srv/repo',
          workspaceId: 'workspace-remote-1',
          agent: 'claude',
          transcriptMatched: true,
          branch: 'main',
          daemonActivePaneId: 'main',
          endpointId: 'ep-1',
          workspace: {
            agents: [{ id: 'pane-session', runtimeId: 'remote-1', sessionId: 'remote-1', title: 'remote-session' }],
            layoutTree: { type: 'pane', paneId: 'pane-session' },
          },
        },
      ],
      activeSessionId: 'remote-1',
      connect: vi.fn(async () => {}),
      connected: true,
      launcherConfig: { executables: {} },
      createSession: vi.fn(async () => 'remote-1'),
      closeSession: mockCloseSession,
      setActiveSession: vi.fn(),
      takeSessionSpawnArgs: vi.fn(() => null),
      reloadSession: vi.fn(async () => {}),
      setLauncherConfig: vi.fn(),
      syncFromDaemonSessions: vi.fn(),
      syncFromDaemonWorkspaces: vi.fn(),
    });

    mockUseDaemonStore.mockReturnValue({
      daemonSessions: [
        {
          id: 'remote-1',
          label: 'remote-session',
          directory: '/srv/repo',
          state: 'waiting_input',
          endpoint_id: 'ep-1',
          needs_review_after_long_run: true,
          state_updated_at: '2026-04-03T12:00:00Z',
        },
      ],
      setDaemonSessions: vi.fn(),
      prs: [],
      setPRs: vi.fn(),
      repoStates: [],
      setRepoStates: vi.fn(),
      authorStates: [],
      setAuthorStates: vi.fn(),
    });

    mockUseDaemonSocket.mockReturnValue({
      sendPRAction: fn,
      sendMutePR: fn,
      sendMuteRepo: fn,
      sendMuteAuthor: fn,
      sendPRVisited: fn,
      sendRefreshPRs: vi.fn(async () => ({ success: true })),
      sendUnregisterSession: mockSendUnregisterSession,
      sendRegisterWorkspace: mockSendRegisterWorkspace,
      sendUnregisterWorkspace: mockSendUnregisterWorkspace,
      sendSetSetting: fn,
      sendCreateWorktree: vi.fn(async () => ({ success: true, path: '/tmp/new' })),
      sendDeleteWorktree: vi.fn(async () => ({ success: true })),
      sendGetRecentLocations: vi.fn(async () => ({ success: true, locations: [] })),
      sendCreateWorktreeFromBranch: vi.fn(async () => ({ success: true, path: '/tmp/new' })),
      sendFetchRemotes: vi.fn(async () => ({ success: true })),
      sendFetchPRDetails: vi.fn(async () => ({ success: true })),
      sendEnsureRepo: vi.fn(async () => ({ success: true, path: '/tmp/repo' })),
      sendSubscribeGitStatus: fn,
      sendUnsubscribeGitStatus: fn,
      sendSessionSelected,
      sendWorkspaceSelected: vi.fn(),
      sendSessionVisualized,
      sendWorkspaceClosePane: mockSendWorkspaceClosePane,
      sendWorkspaceAddSessionPane: vi.fn(async () => ({ success: true })),
      sendGetFileDiff: vi.fn(async () => ({ success: true, original: '', modified: '' })),
      sendGetBranchDiffFiles: vi.fn(async () => ({ success: true, base_ref: 'main', files: [] })),
      getRepoInfo: vi.fn(async () => ({ success: true, is_git_repo: true, branch: 'main' })),
      listWorkflowRuns: vi.fn(async () => ({ success: true, runs: [] })),
      getReviewState: vi.fn(async () => ({ success: true })),
      markFileViewed: vi.fn(async () => ({ success: true })),
      sendAddComment: vi.fn(async () => ({ success: true })),
      sendUpdateComment: vi.fn(async () => ({ success: true })),
      sendResolveComment: vi.fn(async () => ({ success: true })),
      sendDeleteComment: vi.fn(async () => ({ success: true })),
      sendGetComments: vi.fn(async () => ({ success: true, comments: [] })),
      connectionError: null,
      hasReceivedInitialState: true,
      sendNotificationList: vi.fn(async () => ({ notifications: [], unreadCount: 0 })),
      sendNotificationMarkRead: vi.fn(async () => 0),
      rateLimit: null,
      warnings: [],
      clearWarnings: fn,
    });

    await act(async () => {
      render(<App />);
      await Promise.resolve();
    });

    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });

    expect(sendSessionSelected).toHaveBeenCalledWith('remote-1');
    expect(sendSessionVisualized).toHaveBeenCalledWith('remote-1');
  });

  it('only lets the active PR launcher request update or clear progress', async () => {
    const progressHandlers: Array<(progress: { step: string }) => void> = [];
    const resolvers: Array<(result: { success: true; worktreePath: string; sessionId: string }) => void> = [];

    mockUseSessionStore.mockReturnValue({
      sessions: [],
      activeSessionId: null,
      connect: vi.fn(async () => {}),
      connected: true,
      launcherConfig: { executables: {} },
      createSession: vi.fn(async () => 's1'),
      closeSession: mockCloseSession,
      setActiveSession: vi.fn(),
      takeSessionSpawnArgs: vi.fn(() => null),
      reloadSession: vi.fn(async () => {}),
      setLauncherConfig: vi.fn(),
      syncFromDaemonSessions: vi.fn(),
      syncFromDaemonWorkspaces: vi.fn(),
    });

    mockOpenPR.mockImplementation((_pr, _agent, options) => new Promise((resolve) => {
      progressHandlers.push(options.onProgress);
      resolvers.push(resolve);
    }));

    render(<App />);

    await userEvent.click(screen.getByTestId('open-pr'));
    expect(screen.getByRole('status')).toHaveTextContent('Ensuring local repository');

    await userEvent.click(screen.getByTestId('open-pr'));

    act(() => {
      progressHandlers[0]({ step: 'creating_worktree' });
    });
    expect(screen.getByRole('status')).toHaveTextContent('Ensuring local repository');

    act(() => {
      progressHandlers[1]({ step: 'starting_session' });
    });
    expect(screen.getByRole('status')).toHaveTextContent('Starting session');

    await act(async () => {
      resolvers[0]({ success: true, worktreePath: '/tmp/first', sessionId: 's1' });
      await Promise.resolve();
    });
    expect(screen.getByRole('status')).toHaveTextContent('Starting session');

    await act(async () => {
      resolvers[1]({ success: true, worktreePath: '/tmp/second', sessionId: 's2' });
      await Promise.resolve();
    });
    await waitFor(() => {
      expect(screen.queryByRole('status')).not.toBeInTheDocument();
    });
  });

  it('closes the active pane before closing the workspace from Cmd+W', async () => {
    mockUseSessionStore.mockReturnValue({
      sessions: [
        {
          id: 's1',
          label: 'session-with-split',
          state: 'working',
          cwd: '/tmp/repo',
          workspaceId: 'workspace-s1',
          agent: 'claude',
          transcriptMatched: true,
          daemonActivePaneId: 'main',
          workspace: {
            agents: [
              { id: 'pane-session', runtimeId: 's1', sessionId: 's1', title: 'session-with-split' },
              { id: 'pane-session-1', runtimeId: 'runtime-session-1', sessionId: 'runtime-session-1', title: 'session pane 1' },
            ],
            layoutTree: {
              type: 'split',
              splitId: 'root',
              direction: 'vertical',
              ratio: 0.5,
              children: [
                { type: 'pane', paneId: 'pane-session' },
                { type: 'pane', paneId: 'pane-session-1' },
              ],
            },
          },
        },
        {
          id: 'runtime-session-1',
          label: 'session pane 1',
          state: 'working',
          cwd: '/tmp/repo',
          workspaceId: 'workspace-s1',
          agent: 'shell',
          transcriptMatched: true,
          daemonActivePaneId: 'pane-session-1',
          workspace: {
            agents: [
              { id: 'pane-session', runtimeId: 's1', sessionId: 's1', title: 'session-with-split' },
              { id: 'pane-session-1', runtimeId: 'runtime-session-1', sessionId: 'runtime-session-1', title: 'session pane 1' },
            ],
            layoutTree: {
              type: 'split',
              splitId: 'root',
              direction: 'vertical',
              ratio: 0.5,
              children: [
                { type: 'pane', paneId: 'pane-session' },
                { type: 'pane', paneId: 'pane-session-1' },
              ],
            },
          },
        },
      ],
      activeSessionId: 's1',
      connect: vi.fn(async () => {}),
      connected: true,
      launcherConfig: { executables: {} },
      createSession: vi.fn(async () => 's1'),
      closeSession: mockCloseSession,
      setActiveSession: vi.fn(),
      takeSessionSpawnArgs: vi.fn(() => null),
      reloadSession: vi.fn(async () => {}),
      setLauncherConfig: vi.fn(),
      syncFromDaemonSessions: vi.fn(),
      syncFromDaemonWorkspaces: vi.fn(),
    });
    mockUseDaemonStore.mockReturnValue({
      daemonSessions: [],
      setDaemonSessions: vi.fn(),
      prs: [],
      setPRs: vi.fn(),
      repoStates: [],
      setRepoStates: vi.fn(),
      authorStates: [],
      setAuthorStates: vi.fn(),
    });

    render(<App />);

    await waitFor(() => {
      expect(mockUseKeyboardShortcuts.mock.calls.length).toBeGreaterThan(1);
    });
    const keyboardCall = mockUseKeyboardShortcuts.mock.calls[mockUseKeyboardShortcuts.mock.calls.length - 1];
    const keyboardArgs = keyboardCall?.[0] as { onCloseSession: () => void } | undefined;
    expect(keyboardArgs).toBeDefined();

    await act(async () => {
      keyboardArgs?.onCloseSession();
      await Promise.resolve();
    });

    expect(screen.queryByRole('dialog')).toBeNull();
    expect(mockCloseSession).not.toHaveBeenCalled();
    expect(mockSendWorkspaceClosePane).toHaveBeenCalledWith('workspace-s1', 'pane-session');
  });

  it('closes a session pane from the sidebar without showing the split terminal prompt', async () => {
    mockUseSessionStore.mockReturnValue({
      sessions: [
        {
          id: 's1',
          label: 'session-with-split',
          state: 'working',
          cwd: '/tmp/repo',
          workspaceId: 'workspace-s1',
          agent: 'claude',
          transcriptMatched: true,
          daemonActivePaneId: 'main',
          workspace: {
            agents: [
              { id: 'pane-session', runtimeId: 's1', sessionId: 's1', title: 'session-with-split' },
              { id: 'pane-session-1', runtimeId: 'runtime-session-1', sessionId: 'runtime-session-1', title: 'session pane 1' },
            ],
            layoutTree: {
              type: 'split',
              splitId: 'root',
              direction: 'vertical',
              ratio: 0.5,
              children: [
                { type: 'pane', paneId: 'pane-session' },
                { type: 'pane', paneId: 'pane-session-1' },
              ],
            },
          },
        },
        {
          id: 'runtime-session-1',
          label: 'session pane 1',
          state: 'working',
          cwd: '/tmp/repo',
          workspaceId: 'workspace-s1',
          agent: 'shell',
          transcriptMatched: true,
          daemonActivePaneId: 'pane-session-1',
          workspace: {
            agents: [
              { id: 'pane-session', runtimeId: 's1', sessionId: 's1', title: 'session-with-split' },
              { id: 'pane-session-1', runtimeId: 'runtime-session-1', sessionId: 'runtime-session-1', title: 'session pane 1' },
            ],
            layoutTree: {
              type: 'split',
              splitId: 'root',
              direction: 'vertical',
              ratio: 0.5,
              children: [
                { type: 'pane', paneId: 'pane-session' },
                { type: 'pane', paneId: 'pane-session-1' },
              ],
            },
          },
        },
      ],
      activeSessionId: 's1',
      connect: vi.fn(async () => {}),
      connected: true,
      launcherConfig: { executables: {} },
      createSession: vi.fn(async () => 's1'),
      closeSession: mockCloseSession,
      setActiveSession: vi.fn(),
      takeSessionSpawnArgs: vi.fn(() => null),
      reloadSession: vi.fn(async () => {}),
      setLauncherConfig: vi.fn(),
      syncFromDaemonSessions: vi.fn(),
      syncFromDaemonWorkspaces: vi.fn(),
    });

    render(<App />);

    await userEvent.click(screen.getByTestId('close-session'));

    expect(screen.queryByRole('dialog')).toBeNull();
    expect(mockSendWorkspaceClosePane).toHaveBeenCalledWith('workspace-s1', 'pane-session');
    expect(mockCloseSession).not.toHaveBeenCalled();
  });

  it('closes a single-pane session through the workspace pane path', async () => {
    mockUseSessionStore.mockReturnValue({
      sessions: [
        {
          id: 'shell-1',
          label: 'shell',
          state: 'working',
          cwd: '/tmp/repo',
          agent: 'shell',
          workspaceId: 'workspace-shell-1',
          transcriptMatched: true,
          daemonActivePaneId: 'pane-shell-1',
          workspace: {
            agents: [
              { id: 'pane-shell-1', runtimeId: 'shell-1', sessionId: 'shell-1', title: 'shell' },
            ],
            layoutTree: { type: 'pane', paneId: 'pane-shell-1' },
          },
        },
      ],
      activeSessionId: 'shell-1',
      connect: vi.fn(async () => {}),
      connected: true,
      launcherConfig: { executables: {} },
      createSession: vi.fn(async () => 'shell-1'),
      closeSession: mockCloseSession,
      setActiveSession: vi.fn(),
      takeSessionSpawnArgs: vi.fn(() => null),
      reloadSession: vi.fn(async () => {}),
      setLauncherConfig: vi.fn(),
      syncFromDaemonSessions: vi.fn(),
      syncFromDaemonWorkspaces: vi.fn(),
    });

    render(<App />);

    await userEvent.click(screen.getByTestId('close-session'));

    expect(mockSendWorkspaceClosePane).toHaveBeenCalledWith('workspace-shell-1', 'pane-shell-1');
    expect(mockCloseSession).not.toHaveBeenCalled();
  });

  it('uses Cmd+W to close the active session pane before closing the session', async () => {
    mockUseSessionStore.mockReturnValue({
      sessions: [
        {
          id: 's1',
          label: 'session-with-split',
          state: 'working',
          cwd: '/tmp/repo',
          workspaceId: 'workspace-s1',
          agent: 'claude',
          transcriptMatched: true,
          daemonActivePaneId: 'pane-session-1',
          workspace: {
            agents: [
              { id: 'pane-session', runtimeId: 's1', sessionId: 's1', title: 'session-with-split' },
              { id: 'pane-session-1', runtimeId: 'runtime-session-1', sessionId: 'runtime-session-1', title: 'session pane 1' },
            ],
            layoutTree: {
              type: 'split',
              splitId: 'root',
              direction: 'vertical',
              ratio: 0.5,
              children: [
                { type: 'pane', paneId: 'pane-session' },
                { type: 'pane', paneId: 'pane-session-1' },
              ],
            },
          },
        },
        {
          id: 'runtime-session-1',
          label: 'session pane 1',
          state: 'working',
          cwd: '/tmp/repo',
          workspaceId: 'workspace-s1',
          agent: 'shell',
          transcriptMatched: true,
          daemonActivePaneId: 'pane-session-1',
          workspace: {
            agents: [
              { id: 'pane-session', runtimeId: 's1', sessionId: 's1', title: 'session-with-split' },
              { id: 'pane-session-1', runtimeId: 'runtime-session-1', sessionId: 'runtime-session-1', title: 'session pane 1' },
            ],
            layoutTree: {
              type: 'split',
              splitId: 'root',
              direction: 'vertical',
              ratio: 0.5,
              children: [
                { type: 'pane', paneId: 'pane-session' },
                { type: 'pane', paneId: 'pane-session-1' },
              ],
            },
          },
        },
      ],
      activeSessionId: 's1',
      connect: vi.fn(async () => {}),
      connected: true,
      launcherConfig: { executables: {} },
      createSession: vi.fn(async () => 's1'),
      closeSession: mockCloseSession,
      setActiveSession: vi.fn(),
      takeSessionSpawnArgs: vi.fn(() => null),
      reloadSession: vi.fn(async () => {}),
      setLauncherConfig: vi.fn(),
      syncFromDaemonSessions: vi.fn(),
      syncFromDaemonWorkspaces: vi.fn(),
    });

    render(<App />);

    const keyboardCall = mockUseKeyboardShortcuts.mock.calls[mockUseKeyboardShortcuts.mock.calls.length - 1];
    const keyboardArgs = keyboardCall?.[0] as { onCloseSession: () => void } | undefined;
    expect(keyboardArgs).toBeDefined();

    await act(async () => {
      keyboardArgs?.onCloseSession();
      await Promise.resolve();
    });

    expect(screen.queryByRole('dialog')).toBeNull();
    expect(mockSendWorkspaceClosePane).toHaveBeenCalledWith('workspace-s1', 'pane-session-1');
    expect(mockCloseSession).not.toHaveBeenCalled();
  });
});
