import { describe, expect, it, beforeEach, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import App from './App';

const mockUseSessionStore = vi.fn();
const mockUseDaemonStore = vi.fn();
const mockUseDaemonSocket = vi.fn();
const mockSendUnregisterSession = vi.fn();

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
  Sidebar: ({ sessions, onCloseSession }: { sessions: Array<{ id: string }>; onCloseSession: (id: string) => void }) => (
    <button data-testid="close-session" onClick={() => onCloseSession(sessions[0].id)}>
      Close Session
    </button>
  ),
}));

vi.mock('./components/Dashboard', () => ({ Dashboard: () => null }));
vi.mock('./components/AttentionDrawer', () => ({ AttentionDrawer: () => null }));
vi.mock('./components/LocationPicker', () => ({ LocationPicker: () => null }));
vi.mock('./components/BranchPicker', () => ({ BranchPicker: () => null }));
vi.mock('./components/UndoToast', () => ({ UndoToast: () => null }));
vi.mock('./components/ChangesPanel', () => ({ ChangesPanel: () => null }));
vi.mock('./components/ReviewPanel', () => ({ ReviewPanel: () => null }));
vi.mock('./components/UtilityTerminalPanel', () => ({ UtilityTerminalPanel: () => null }));
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
  useKeyboardShortcuts: vi.fn(),
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

    mockUseSessionStore.mockReturnValue({
      sessions: [
        {
          id: 's1',
          label: 'worktree-session',
          state: 'working',
          terminal: null,
          cwd: '/tmp/repo/.worktrees/feature-a',
          agent: 'claude',
          transcriptMatched: true,
          branch: 'feature-a',
          isWorktree: true,
          terminalPanel: {
            isOpen: false,
            height: 200,
            activeTabId: null,
            terminals: [],
            nextTerminalNumber: 1,
          },
        },
      ],
      activeSessionId: 's1',
      createSession: vi.fn(async () => 's1'),
      closeSession: vi.fn(),
      setActiveSession: vi.fn(),
      connectTerminal: vi.fn(),
      resizeSession: vi.fn(),
      openTerminalPanel: vi.fn(),
      collapseTerminalPanel: vi.fn(),
      setTerminalPanelHeight: vi.fn(),
      addUtilityTerminal: vi.fn(),
      removeUtilityTerminal: vi.fn(),
      setActiveUtilityTerminal: vi.fn(),
      renameUtilityTerminal: vi.fn(),
      setForkParams: vi.fn(),
      setResumePicker: vi.fn(),
      setLauncherConfig: vi.fn(),
      syncFromDaemonSessions: vi.fn(),
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
      sendDeleteBranch: fn,
      sendGetRecentLocations: vi.fn(async () => ({ success: true, locations: [] })),
      sendListBranches: vi.fn(async () => ({ success: true, branches: [] })),
      sendSwitchBranch: vi.fn(async () => ({ success: true })),
      sendCreateWorktreeFromBranch: vi.fn(async () => ({ success: true, path: '/tmp/new' })),
      sendCheckDirty: vi.fn(async () => ({ success: true, dirty: false })),
      sendStash: vi.fn(async () => ({ success: true })),
      sendStashPop: vi.fn(async () => ({ success: true })),
      sendCheckAttnStash: vi.fn(async () => ({ success: true, has_stash: false })),
      sendCommitWIP: vi.fn(async () => ({ success: true })),
      sendGetDefaultBranch: vi.fn(async () => ({ success: true, branch: 'main' })),
      sendFetchRemotes: vi.fn(async () => ({ success: true })),
      sendFetchPRDetails: vi.fn(async () => ({ success: true })),
      sendListRemoteBranches: vi.fn(async () => ({ success: true, branches: [] })),
      sendEnsureRepo: vi.fn(async () => ({ success: true, path: '/tmp/repo' })),
      sendSubscribeGitStatus: fn,
      sendUnsubscribeGitStatus: fn,
      sendSessionVisualized: fn,
      sendGetFileDiff: vi.fn(async () => ({ success: true, original: '', modified: '' })),
      sendGetBranchDiffFiles: vi.fn(async () => ({ success: true, base_ref: 'main', files: [] })),
      getRepoInfo: vi.fn(async () => ({ success: true, is_git_repo: true, branch: 'main' })),
      getReviewState: vi.fn(async () => ({ success: true })),
      markFileViewed: vi.fn(async () => ({ success: true })),
      sendAddComment: vi.fn(async () => ({ success: true })),
      sendUpdateComment: vi.fn(async () => ({ success: true })),
      sendResolveComment: vi.fn(async () => ({ success: true })),
      sendWontFixComment: vi.fn(async () => ({ success: true })),
      sendDeleteComment: vi.fn(async () => ({ success: true })),
      sendGetComments: vi.fn(async () => ({ success: true, comments: [] })),
      sendStartReview: vi.fn(async () => ({ success: true })),
      sendCancelReview: vi.fn(async () => ({ success: true })),
      connectionError: null,
      hasReceivedInitialState: true,
      rateLimit: null,
      warnings: [],
      clearWarnings: fn,
    });
  });

  it('shows worktree cleanup prompt when closing a worktree session', async () => {
    localStorage.setItem('alwaysKeepWorktrees', 'true');
    render(<App />);

    await userEvent.click(screen.getByTestId('close-session'));

    await waitFor(() => {
      expect(screen.getByRole('dialog')).toBeInTheDocument();
    });
  });
});
