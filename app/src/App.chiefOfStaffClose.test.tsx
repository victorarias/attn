import { describe, expect, it, beforeEach, vi } from 'vitest';
import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import App from './App';
import { WHATS_NEW_ID, WHATS_NEW_STORAGE_KEY } from './hooks/useWhatsNew';

// The chief-of-staff session is the profile-wide orchestrator and must be
// protected from accidental close: ⌘W and the close action no-op on it (with a
// hint), while ordinary sessions keep closing. Both user paths funnel through the
// same App close handlers, so these tests drive the close button (UI) and the
// session.close shortcut (⌘W) and assert no daemon close command is sent.

const mockUseSessionStore = vi.fn();
const mockUseDaemonStore = vi.fn();
const mockUseDaemonSocket = vi.fn();
const mockUseKeyboardShortcuts = vi.fn();

const { mockShowError, mockSendWorkspaceClosePane, mockSendUnregisterSession } = vi.hoisted(() => ({
  mockShowError: vi.fn(),
  mockSendWorkspaceClosePane: vi.fn(async () => ({ success: true })),
  mockSendUnregisterSession: vi.fn(async () => {}),
}));

let chiefOfStaff: boolean;

vi.mock('@tauri-apps/plugin-deep-link', () => ({
  onOpenUrl: vi.fn(async () => () => {}),
  getCurrent: vi.fn(async () => []),
}));
vi.mock('@tauri-apps/plugin-opener', () => ({ openUrl: vi.fn(async () => {}) }));

vi.mock('./components/GhosttyTerminal', async () => {
  const React = await import('react');
  return { GhosttyTerminal: React.forwardRef(function MockTerminal() { return null; }) };
});

// Sidebar stub: a single close button wired to the same prop the real close
// control uses (handleRequestCloseSession).
vi.mock('./components/Sidebar', () => ({
  EditorIcon: () => null,
  WorkflowIcon: () => null,
  DiffIcon: () => null,
  PRsIcon: () => null,
  NotebookIcon: () => null,
  MarkdownIcon: () => null,
  Sidebar: ({ onCloseSession }: { onCloseSession: (id: string) => void }) => (
    <button data-testid="close-session" onClick={() => onCloseSession('s1')}>
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
vi.mock('./components/ErrorToast', () => ({
  ErrorToast: () => null,
  useErrorToast: () => ({ message: null, showError: mockShowError, clearError: vi.fn() }),
}));
vi.mock('./hooks/useKeyboardShortcuts', () => ({
  useKeyboardShortcuts: (args: unknown) => mockUseKeyboardShortcuts(args),
}));
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
        args.onWorkspacesUpdate?.([
          {
            id: 'workspace-s1',
            title: 'orchestrator',
            directory: '/tmp/repo',
            status: 'active',
            layout: {
              active_pane_id: 'pane-s1',
              layout_json: JSON.stringify({ type: 'pane', pane_id: 'pane-s1' }),
              panes: [{
                workspace_id: 'workspace-s1',
                pane_id: 'pane-s1',
                kind: 'agent',
                runtime_id: 's1',
                session_id: 's1',
                title: 'orchestrator',
              }],
            },
          },
        ]);
      }, []);
      return mockUseDaemonSocket(args);
    },
  };
});
vi.mock('./pty/bridge', async () => {
  const actual = await vi.importActual<typeof import('./pty/bridge')>('./pty/bridge');
  return { ...actual, ptySpawn: vi.fn(async () => {}) };
});

function triggerCmdW() {
  const calls = mockUseKeyboardShortcuts.mock.calls;
  const args = calls[calls.length - 1]?.[0] as { onCloseSession?: () => void };
  act(() => {
    args.onCloseSession?.();
  });
}

describe('chief-of-staff session is protected from close', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(WHATS_NEW_STORAGE_KEY, WHATS_NEW_ID);
    chiefOfStaff = false;

    mockUseSessionStore.mockReturnValue({
      sessions: [{
        id: 's1',
        label: 'orchestrator',
        state: 'working',
        cwd: '/tmp/repo',
        workspaceId: 'workspace-s1',
        agent: 'claude',
        transcriptMatched: true,
        daemonActivePaneId: 'pane-s1',
        workspace: {
          agents: [{ id: 'pane-s1', runtimeId: 's1', sessionId: 's1', title: 'orchestrator' }],
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

    // daemonSessions is the source the close guard consults for chief_of_staff;
    // a getter lets each test flip the flag before render.
    mockUseDaemonStore.mockImplementation(() => ({
      daemonSessions: [{
        id: 's1',
        label: 'orchestrator',
        directory: '/tmp/repo',
        state: 'working',
        chief_of_staff: chiefOfStaff,
      }],
      setDaemonSessions: vi.fn(),
      prs: [], setPRs: vi.fn(),
      repoStates: [], setRepoStates: vi.fn(),
      authorStates: [], setAuthorStates: vi.fn(),
    }));

    const fn = vi.fn();
    mockUseDaemonSocket.mockReturnValue({
      sendPRAction: fn, sendMutePR: fn, sendMuteRepo: fn, sendMuteAuthor: fn, sendPRVisited: fn,
      sendRefreshPRs: vi.fn(async () => ({ success: true })),
      sendUnregisterSession: mockSendUnregisterSession,
      sendRegisterWorkspace: fn,
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
      sendSessionSelected: fn, sendWorkspaceSelected: fn, sendSessionVisualized: fn,
      sendWorkspaceClosePane: mockSendWorkspaceClosePane,
      sendWorkspaceAddSessionPane: vi.fn(async () => ({ success: true })),
      requestTileContent: fn,
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
      getPresentations: vi.fn(async () => []),
      connectionError: null,
      hasReceivedInitialState: true,
      sendNotificationList: vi.fn(async () => ({ notifications: [], unreadCount: 0 })),
      sendNotificationMarkRead: vi.fn(async () => 0),
      rateLimit: null,
      warnings: [],
      clearWarnings: fn,
    });
  });

  it('no-ops the close button on the chief session and shows the protected hint', async () => {
    chiefOfStaff = true;
    render(<App />);

    await userEvent.click(screen.getByTestId('close-session'));

    expect(mockSendWorkspaceClosePane).not.toHaveBeenCalled();
    expect(mockSendUnregisterSession).not.toHaveBeenCalled();
    expect(mockShowError).toHaveBeenCalledWith(expect.stringContaining('Chief of staff is protected'));
  });

  it('no-ops the ⌘W shortcut on the chief session and shows the protected hint', async () => {
    chiefOfStaff = true;
    render(<App />);

    triggerCmdW();

    expect(mockSendWorkspaceClosePane).not.toHaveBeenCalled();
    expect(mockSendUnregisterSession).not.toHaveBeenCalled();
    expect(mockShowError).toHaveBeenCalledWith(expect.stringContaining('Chief of staff is protected'));
  });

  it('closes an ordinary session normally and shows no hint', async () => {
    chiefOfStaff = false;
    render(<App />);

    await userEvent.click(screen.getByTestId('close-session'));

    expect(mockSendWorkspaceClosePane).toHaveBeenCalledTimes(1);
    expect(mockShowError).not.toHaveBeenCalled();
  });
});
