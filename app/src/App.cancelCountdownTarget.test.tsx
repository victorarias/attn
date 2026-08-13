import { describe, expect, it, beforeEach, vi } from 'vitest';
import { act, render } from '@testing-library/react';
import App from './App';
import { WHATS_NEW_ID, WHATS_NEW_STORAGE_KEY } from './hooks/useWhatsNew';

// ⌘. answers one of two different things depending on what is on screen, and
// App is what picks which. A countdown running in front of the user is answered
// wherever it is — every visible one at once, because each pill announced this
// key. With nothing counting down the same press gets ahead of the next
// auto-settle, and that one goes to the focused session alone: nothing asked for
// it, so it belongs where the user is looking.
//
// Both halves are the same wire command; the daemon decides between cancelling,
// arming, and disarming. What is asserted here is only who the press names.

const mockUseSessionStore = vi.fn();
const mockUseDaemonStore = vi.fn();
const mockUseDaemonSocket = vi.fn();
const mockUseKeyboardShortcuts = vi.fn();

const { mockSetActiveSession, mockSendCancelCountdown } = vi.hoisted(() => ({
  mockSetActiveSession: vi.fn(),
  mockSendCancelCountdown: vi.fn(),
}));

// The daemon's auto-settle view of the two panes, mutated per step.
let autoSettleFiresAt: Record<string, string | undefined>;
let activeSessionId: string | null;

vi.mock('@tauri-apps/plugin-deep-link', () => ({
  onOpenUrl: vi.fn(async () => () => {}),
  getCurrent: vi.fn(async () => []),
}));
vi.mock('@tauri-apps/plugin-opener', () => ({ openUrl: vi.fn(async () => {}) }));

vi.mock('./components/GhosttyTerminal', async () => {
  const React = await import('react');
  return { GhosttyTerminal: React.forwardRef(function MockTerminal() { return null; }) };
});

vi.mock('./components/Sidebar', () => ({
  EditorIcon: () => null,
  WorkflowIcon: () => null,
  DiffIcon: () => null,
  PRsIcon: () => null,
  NotebookIcon: () => null,
  MarkdownIcon: () => null,
  Sidebar: () => null,
}));

vi.mock('./components/Dashboard', () => ({ Dashboard: () => null }));
vi.mock('./components/grid/GridView', () => ({ GridView: () => null }));
vi.mock('./components/AttentionDrawer', () => ({ AttentionDrawer: () => null }));
vi.mock('./components/LocationPicker', () => ({ LocationPicker: () => null }));
vi.mock('./components/UndoToast', () => ({ UndoToast: () => null }));
vi.mock('./components/SessionTerminalWorkspace', () => ({ SessionTerminalWorkspace: () => null }));
vi.mock('./components/ErrorToast', () => ({
  ErrorToast: () => null,
  useErrorToast: () => ({ message: null, showError: vi.fn(), clearError: vi.fn() }),
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
vi.mock('./hooks/useDaemonSocket', () => ({
  useDaemonSocket: (args: unknown) => mockUseDaemonSocket(args),
}));
vi.mock('./pty/bridge', async () => {
  const actual = await vi.importActual<typeof import('./pty/bridge')>('./pty/bridge');
  return { ...actual, ptySpawn: vi.fn(async () => {}) };
});

type SocketArgs = {
  onWorkspacesUpdate?: (workspaces: unknown[]) => void;
  onSettingsUpdate?: (settings: Record<string, string>) => void;
};

function socketArgs(): SocketArgs {
  const calls = mockUseDaemonSocket.mock.calls;
  return calls[calls.length - 1]?.[0] as SocketArgs;
}

/** The shortcut handlers App registered on its last render. */
function shortcutHandlers<T>(): T {
  const calls = mockUseKeyboardShortcuts.mock.calls;
  return calls[calls.length - 1]?.[0] as T;
}

// One workspace split between two panes, so both sessions are on screen at once
// — the only arrangement where "every visible countdown" and "the focused one"
// can disagree.
const PANES = ['s1', 's2'];

function workspacePayload() {
  return [{
    id: 'workspace-main',
    title: 'main',
    directory: '/tmp/main',
    status: 'active',
    layout: {
      active_pane_id: 'pane-s1',
      layout_json: JSON.stringify({
        type: 'split',
        split_id: 'split-1',
        direction: 'horizontal',
        ratio: 0.5,
        first: { type: 'pane', pane_id: 'pane-s1' },
        second: { type: 'pane', pane_id: 'pane-s2' },
      }),
      panes: PANES.map((id) => ({
        workspace_id: 'workspace-main',
        pane_id: `pane-${id}`,
        kind: 'agent',
        runtime_id: id,
        session_id: id,
        title: id,
      })),
    },
  }];
}

function broadcast() {
  act(() => {
    socketArgs().onSettingsUpdate?.({ queue_mode_enabled: 'true' });
    socketArgs().onWorkspacesUpdate?.(workspacePayload());
  });
}

/** The sessions named by the last ⌘. press, in the order the app named them. */
function pressCancelCountdown(): string[] {
  mockSendCancelCountdown.mockClear();
  const shortcuts = shortcutHandlers<{ onCancelCountdown?: () => void }>();
  act(() => { shortcuts.onCancelCountdown?.(); });
  return mockSendCancelCountdown.mock.calls.map((call) => call[0] as string);
}

function shortcutRegistered(): boolean {
  return Boolean(shortcutHandlers<{ onCancelCountdown?: () => void }>().onCancelCountdown);
}

describe('who ⌘. names', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(WHATS_NEW_STORAGE_KEY, WHATS_NEW_ID);
    autoSettleFiresAt = {};
    activeSessionId = null;

    mockSetActiveSession.mockImplementation((id: string | null) => { activeSessionId = id; });

    mockUseSessionStore.mockImplementation(() => ({
      sessions: PANES.map((id) => ({
        id,
        label: id,
        state: 'working',
        cwd: '/tmp/main',
        workspaceId: 'workspace-main',
        agent: 'claude',
        transcriptMatched: true,
        daemonActivePaneId: 'pane-s1',
        workspace: {
          agents: PANES.map((paneSession) => ({
            id: `pane-${paneSession}`,
            runtimeId: paneSession,
            sessionId: paneSession,
            title: paneSession,
          })),
          layoutTree: {
            type: 'split',
            splitId: 'split-1',
            direction: 'horizontal',
            ratio: 0.5,
            children: [
              { type: 'pane', paneId: 'pane-s1' },
              { type: 'pane', paneId: 'pane-s2' },
            ],
          },
        },
      })),
      activeSessionId,
      connect: vi.fn(async () => {}),
      connected: true,
      launcherConfig: { executables: {} },
      createSession: vi.fn(async () => 's1'),
      closeSession: vi.fn(),
      setActiveSession: mockSetActiveSession,
      takeSessionSpawnArgs: vi.fn(() => null),
      reloadSession: vi.fn(async () => {}),
      setLauncherConfig: vi.fn(),
      syncFromDaemonSessions: vi.fn(),
      syncFromDaemonWorkspaces: vi.fn(),
    }));

    mockUseDaemonStore.mockImplementation(() => ({
      daemonSessions: PANES.map((id) => ({
        id,
        label: id,
        directory: '/tmp/main',
        state: 'working',
        turn_owed: true,
        turn_opened_at: '2026-08-03T09:00:00Z',
        auto_settle_fires_at: autoSettleFiresAt[id],
      })),
      setDaemonSessions: vi.fn(),
      prs: [], setPRs: vi.fn(),
      repoStates: [], setRepoStates: vi.fn(),
      authorStates: [], setAuthorStates: vi.fn(),
      seeds: [], setSeeds: vi.fn(),
    }));

    const fn = vi.fn();
    mockUseDaemonSocket.mockReturnValue({
      sendPRAction: fn, sendMutePR: fn, sendMuteRepo: fn, sendMuteAuthor: fn, sendPRVisited: fn,
      sendRefreshPRs: vi.fn(async () => ({ success: true })),
      sendUnregisterSession: vi.fn(async () => {}),
      sendRegisterWorkspace: fn,
      sendUnregisterWorkspace: vi.fn(async () => {}),
      sendMuteWorkspace: vi.fn(async () => ({ success: true })),
      sendSetSetting: fn,
      sendSetClientPresence: fn,
      sendCancelCountdown: mockSendCancelCountdown,
      sendCreateWorktree: vi.fn(async () => ({ success: true, path: '/tmp/new' })),
      sendDeleteWorktree: vi.fn(async () => ({ success: true })),
      sendGetRecentLocations: vi.fn(async () => ({ success: true, locations: [] })),
      sendCreateWorktreeFromBranch: vi.fn(async () => ({ success: true, path: '/tmp/new' })),
      sendFetchRemotes: vi.fn(async () => ({ success: true })),
      sendFetchPRDetails: vi.fn(async () => ({ success: true })),
      sendEnsureRepo: vi.fn(async () => ({ success: true, path: '/tmp/repo' })),
      sendSubscribeGitStatus: fn, sendUnsubscribeGitStatus: fn,
      sendSessionSelected: fn, sendWorkspaceSelected: fn,
      sendWorkspaceClosePane: vi.fn(async () => ({ success: true })),
      sendWorkspaceAddSessionPane: vi.fn(async () => ({ success: true })),
      requestTileContent: fn,
      sendGetFileDiff: vi.fn(async () => ({ success: true, original: '', modified: '' })),
      getRepoInfo: vi.fn(async () => ({ success: true, is_git_repo: true, branch: 'main' })),
      listWorkflowRuns: vi.fn(async () => ({ success: true, runs: [] })),
      getPresentations: vi.fn(async () => []),
      connectionError: null,
      hasReceivedInitialState: true,
      sendNotificationList: vi.fn(async () => ({ notifications: [], unreadCount: 0, critical: { count: 0, title: '' } })),
      sendNotificationMarkRead: vi.fn(async () => 0),
      rateLimit: null,
      warnings: [],
      clearWarnings: fn,
      sendSetTerminalTheme: fn,
    });
  });

  /** Renders and puts the user on s1 with both panes on screen. */
  function focusFirstPane() {
    render(<App />);
    broadcast();
    act(() => { mockSetActiveSession('s1'); });
    broadcast();
    expect(activeSessionId).toBe('s1');
  }

  it('names the focused session alone when nothing is counting down', () => {
    focusFirstPane();

    // s2 is on screen and owes a turn too, but nothing on it is counting: only
    // the pane the user is in gets a dismissal armed against it.
    expect(pressCancelCountdown()).toEqual(['s1']);
  });

  it('names every visible countdown instead, wherever it is running', () => {
    focusFirstPane();
    autoSettleFiresAt.s2 = '2999-01-01T00:00:00.000Z';
    broadcast();

    // The pill is on the pane the user is not in, and the press still answers it
    // — a pill naming a key that does nothing is worse than no pill.
    expect(pressCancelCountdown()).toEqual(['s2']);
  });

  it('names nothing when no tile is on screen at all', () => {
    focusFirstPane();

    // Home: no terminal is rendered, so there is nothing announcing the key and
    // nothing the user can see to arm. The shortcut unregisters rather than
    // firing at a session picked by guesswork.
    const shortcuts = shortcutHandlers<{ onGoToDashboard: () => void }>();
    act(() => { shortcuts.onGoToDashboard(); });
    broadcast();
    expect(activeSessionId).toBeNull();

    expect(shortcutRegistered()).toBe(false);
  });
});
