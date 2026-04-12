import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import App from './App';

const mockUseSessionStore = vi.fn();
const mockUseDaemonStore = vi.fn();
const mockUseDaemonSocket = vi.fn();
const mockSendUnregisterSession = vi.fn();
const mockUseKeyboardShortcuts = vi.fn();
const mockSendWorkspaceClosePane = vi.fn(async () => ({ success: true }));
const mockCloseSession = vi.fn();

vi.mock('@tauri-apps/plugin-deep-link', () => ({
  onOpenUrl: vi.fn(async () => () => {}),
  getCurrent: vi.fn(async () => []),
}));

vi.mock('@tauri-apps/plugin-opener', () => ({
  openUrl: vi.fn(async () => {}),
}));

vi.mock('./components/Terminal', async () => {
  const React = await import('react');
  return {
    Terminal: React.forwardRef(function MockTerminal() {
      return null;
    }),
  };
});

vi.mock('./components/Sidebar', () => ({
  EditorIcon: () => null,
  ReviewLoopIcon: () => null,
  DiffIcon: () => null,
  PRsIcon: () => null,
  Sidebar: ({ visualOrder, onCloseSession }: { visualOrder: Array<{ id: string }>; onCloseSession: (id: string) => void }) => (
    <button data-testid="close-session" onClick={() => onCloseSession(visualOrder[0].id)}>
      Close Session
    </button>
  ),
}));

vi.mock('./components/Dashboard', () => ({ Dashboard: () => null }));
vi.mock('./components/AttentionDrawer', () => ({ AttentionDrawer: () => null }));
vi.mock('./components/LocationPicker', () => ({ LocationPicker: () => null }));
vi.mock('./components/UndoToast', () => ({ UndoToast: () => null }));
vi.mock('./components/ChangesPanel', () => ({ ChangesPanel: () => null }));
vi.mock('./components/DiffDetailPanel', () => ({ DiffDetailPanel: () => null }));
vi.mock('./components/SessionTerminalWorkspace', () => ({ SessionTerminalWorkspace: () => null }));
vi.mock('./components/ThumbsModal', () => ({ ThumbsModal: () => null }));
vi.mock('./components/ForkDialog', () => ({ ForkDialog: () => null }));

vi.mock('./components/CopyToast', () => ({
  CopyToast: () => null,
  useCopyToast: () => ({
    message: null,
    showToast: vi.fn(),
    clearToast: vi.fn(),
  }),
}));

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
  useOpenPR: () =>
    vi.fn(async () => ({
      success: true,
      worktreePath: '/tmp/wt',
      sessionId: 's1',
    })),
}));

vi.mock('./hooks/usePRsNeedingAttention', () => ({
  usePRsNeedingAttention: () => ({ needsAttention: [] }),
}));

vi.mock('./store/sessions', () => ({
  MAIN_TERMINAL_PANE_ID: 'main',
  useSessionStore: () => mockUseSessionStore(),
}));

vi.mock('./store/daemonSessions', () => ({
  useDaemonStore: () => mockUseDaemonStore(),
}));

vi.mock('./hooks/useDaemonSocket', () => ({
  useDaemonSocket: (args: unknown) => mockUseDaemonSocket(args),
}));

describe('worktree cleanup prompt', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    vi.useRealTimers();
    mockSendWorkspaceClosePane.mockResolvedValue({ success: true });

    mockUseSessionStore.mockReturnValue({
      sessions: [
        {
          id: 's1',
          label: 'worktree-session',
          state: 'working',
          cwd: '/tmp/repo/.worktrees/feature-a',
          agent: 'claude',
          transcriptMatched: true,
          branch: 'feature-a',
          isWorktree: true,
          daemonActivePaneId: 'main',
          workspace: {
            terminals: [],
            layoutTree: { type: 'pane', paneId: 'main' },
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
      setForkParams: vi.fn(),
      setLauncherConfig: vi.fn(),
      syncFromDaemonSessions: vi.fn(),
      syncFromDaemonWorkspaces: vi.fn(),
    });

    mockUseDaemonStore.mockReturnValue({
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
    });

    const fn = vi.fn();
    mockUseDaemonSocket.mockReturnValue({
      sendPRAction: fn,
      sendMutePR: fn,
      sendMuteRepo: fn,
      sendMuteAuthor: fn,
      sendPRVisited: fn,
      sendRefreshPRs: vi.fn(async () => ({ success: true })),
      sendUnregisterSession: mockSendUnregisterSession,
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
      sendSessionVisualized: fn,
      sendWorkspaceClosePane: mockSendWorkspaceClosePane,
      sendWorkspaceSplitPane: fn,
      sendGetFileDiff: vi.fn(async () => ({ success: true, original: '', modified: '' })),
      sendGetBranchDiffFiles: vi.fn(async () => ({ success: true, base_ref: 'main', files: [] })),
      getRepoInfo: vi.fn(async () => ({ success: true, is_git_repo: true, branch: 'main' })),
      getReviewLoopRun: vi.fn(async () => ({ success: true, state: null })),
      getReviewLoopState: vi.fn(async () => ({ success: true, state: null })),
      getReviewState: vi.fn(async () => ({ success: true })),
      markFileViewed: vi.fn(async () => ({ success: true })),
      sendAddComment: vi.fn(async () => ({ success: true })),
      sendUpdateComment: vi.fn(async () => ({ success: true })),
      sendResolveComment: vi.fn(async () => ({ success: true })),
      sendWontFixComment: vi.fn(async () => ({ success: true })),
      sendDeleteComment: vi.fn(async () => ({ success: true })),
      sendGetComments: vi.fn(async () => ({ success: true, comments: [] })),
      sendStartReviewLoop: vi.fn(async () => ({ success: true, state: null })),
      sendStopReviewLoop: vi.fn(async () => ({ success: true, state: null })),
      setReviewLoopIterationLimit: vi.fn(async () => ({ success: true, state: null })),
      connectionError: null,
      hasReceivedInitialState: true,
      rateLimit: null,
      warnings: [],
      clearWarnings: fn,
    });
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

  it('marks remote long-run sessions as visualized after the visibility delay', async () => {
    vi.useFakeTimers();
    const sendSessionVisualized = vi.fn();
    const fn = vi.fn();

    mockUseSessionStore.mockReturnValue({
      sessions: [
        {
          id: 'remote-1',
          label: 'remote-session',
          state: 'waiting_input',
          cwd: '/srv/repo',
          agent: 'claude',
          transcriptMatched: true,
          branch: 'main',
          daemonActivePaneId: 'main',
          endpointId: 'ep-1',
          workspace: {
            terminals: [],
            layoutTree: { type: 'pane', paneId: 'main' },
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
      setForkParams: vi.fn(),
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
      sendSessionVisualized,
      sendWorkspaceClosePane: mockSendWorkspaceClosePane,
      sendWorkspaceSplitPane: fn,
      sendGetFileDiff: vi.fn(async () => ({ success: true, original: '', modified: '' })),
      sendGetBranchDiffFiles: vi.fn(async () => ({ success: true, base_ref: 'main', files: [] })),
      getRepoInfo: vi.fn(async () => ({ success: true, is_git_repo: true, branch: 'main' })),
      getReviewLoopRun: vi.fn(async () => ({ success: true, state: null })),
      getReviewLoopState: vi.fn(async () => ({ success: true, state: null })),
      getReviewState: vi.fn(async () => ({ success: true })),
      markFileViewed: vi.fn(async () => ({ success: true })),
      sendAddComment: vi.fn(async () => ({ success: true })),
      sendUpdateComment: vi.fn(async () => ({ success: true })),
      sendResolveComment: vi.fn(async () => ({ success: true })),
      sendWontFixComment: vi.fn(async () => ({ success: true })),
      sendDeleteComment: vi.fn(async () => ({ success: true })),
      sendGetComments: vi.fn(async () => ({ success: true, comments: [] })),
      sendStartReviewLoop: vi.fn(async () => ({ success: true, state: null })),
      sendStopReviewLoop: vi.fn(async () => ({ success: true, state: null })),
      setReviewLoopIterationLimit: vi.fn(async () => ({ success: true, state: null })),
      connectionError: null,
      hasReceivedInitialState: true,
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

    expect(sendSessionVisualized).toHaveBeenCalledWith('remote-1');
  });

  it('prompts before closing a split session from the main pane with Cmd+W', async () => {
    mockUseSessionStore.mockReturnValue({
      sessions: [
        {
          id: 's1',
          label: 'session-with-split',
          state: 'working',
          cwd: '/tmp/repo',
          agent: 'claude',
          transcriptMatched: true,
          daemonActivePaneId: 'main',
          workspace: {
            terminals: [{ id: 'pane-shell-1', ptyId: 'runtime-shell-1', title: 'Shell 1' }],
            layoutTree: {
              type: 'split',
              splitId: 'root',
              direction: 'vertical',
              ratio: 0.5,
              children: [
                { type: 'pane', paneId: 'main' },
                { type: 'pane', paneId: 'pane-shell-1' },
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
      setForkParams: vi.fn(),
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

    const keyboardCall = mockUseKeyboardShortcuts.mock.calls[mockUseKeyboardShortcuts.mock.calls.length - 1];
    const keyboardArgs = keyboardCall?.[0] as { onCloseSession: () => void } | undefined;
    expect(keyboardArgs).toBeDefined();

    act(() => {
      keyboardArgs?.onCloseSession();
    });

    const dialog = screen.getByRole('dialog');
    expect(dialog).toBeInTheDocument();
    expect(mockCloseSession).not.toHaveBeenCalled();

    fireEvent.keyDown(dialog, { key: 'y' });

    await waitFor(() => {
      expect(mockCloseSession).toHaveBeenCalledWith('s1');
    });
    expect(mockSendWorkspaceClosePane).not.toHaveBeenCalled();
  });

  it('cancels split-session close from the sidebar with Escape', async () => {
    mockUseSessionStore.mockReturnValue({
      sessions: [
        {
          id: 's1',
          label: 'session-with-split',
          state: 'working',
          cwd: '/tmp/repo',
          agent: 'claude',
          transcriptMatched: true,
          daemonActivePaneId: 'main',
          workspace: {
            terminals: [{ id: 'pane-shell-1', ptyId: 'runtime-shell-1', title: 'Shell 1' }],
            layoutTree: {
              type: 'split',
              splitId: 'root',
              direction: 'vertical',
              ratio: 0.5,
              children: [
                { type: 'pane', paneId: 'main' },
                { type: 'pane', paneId: 'pane-shell-1' },
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
      setForkParams: vi.fn(),
      setLauncherConfig: vi.fn(),
      syncFromDaemonSessions: vi.fn(),
      syncFromDaemonWorkspaces: vi.fn(),
    });

    render(<App />);

    await userEvent.click(screen.getByTestId('close-session'));

    const dialog = screen.getByRole('dialog');
    expect(dialog).toBeInTheDocument();

    fireEvent.keyDown(dialog, { key: 'Escape' });

    expect(mockCloseSession).not.toHaveBeenCalled();
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('uses Cmd+W to close the active utility pane before closing the session', async () => {
    mockUseSessionStore.mockReturnValue({
      sessions: [
        {
          id: 's1',
          label: 'session-with-split',
          state: 'working',
          cwd: '/tmp/repo',
          agent: 'claude',
          transcriptMatched: true,
          daemonActivePaneId: 'pane-shell-1',
          workspace: {
            terminals: [{ id: 'pane-shell-1', ptyId: 'runtime-shell-1', title: 'Shell 1' }],
            layoutTree: {
              type: 'split',
              splitId: 'root',
              direction: 'vertical',
              ratio: 0.5,
              children: [
                { type: 'pane', paneId: 'main' },
                { type: 'pane', paneId: 'pane-shell-1' },
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
      setForkParams: vi.fn(),
      setLauncherConfig: vi.fn(),
      syncFromDaemonSessions: vi.fn(),
      syncFromDaemonWorkspaces: vi.fn(),
    });

    render(<App />);

    const keyboardCall = mockUseKeyboardShortcuts.mock.calls[mockUseKeyboardShortcuts.mock.calls.length - 1];
    const keyboardArgs = keyboardCall?.[0] as { onCloseSession: () => void } | undefined;
    expect(keyboardArgs).toBeDefined();

    act(() => {
      keyboardArgs?.onCloseSession();
    });

    expect(screen.queryByRole('dialog')).toBeNull();
    expect(mockSendWorkspaceClosePane).toHaveBeenCalledWith('s1', 'pane-shell-1');
    expect(mockCloseSession).not.toHaveBeenCalled();
  });
});
