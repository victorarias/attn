import { describe, expect, it, beforeEach, vi } from 'vitest';
import { act, render } from '@testing-library/react';
import App from './App';
import { WHATS_NEW_ID, WHATS_NEW_STORAGE_KEY } from './hooks/useWhatsNew';
import type { CriticalNotificationState } from './hooks/useDaemonSocket';

// notifications_updated fires on every notification write and carries a fresh
// critical-state object each time, so an unchanged pair still arrives with a new
// identity. App holds it behind an equality guard so the identity below the
// socket changes only when the pair does — the witness is the identity of the
// criticalNotifications prop the Sidebar receives.
//
// This does not claim the header actions stop rebuilding. That memo has other
// deps that are fresh on every render, so it rebuilds regardless; what is pinned
// here is the one identity this code owns.

const mockUseSessionStore = vi.fn();
const mockUseDaemonStore = vi.fn();
const mockUseDaemonSocket = vi.fn();

const { criticalSeen } = vi.hoisted(() => ({ criticalSeen: [] as unknown[] }));

let notifyCritical: ((unread: number, critical: CriticalNotificationState) => void) | null = null;

vi.mock('@tauri-apps/plugin-deep-link', () => ({
  onOpenUrl: vi.fn(async () => () => {}),
  getCurrent: vi.fn(async () => []),
}));
vi.mock('@tauri-apps/plugin-opener', () => ({ openUrl: vi.fn(async () => {}) }));
vi.mock('./components/GhosttyTerminal', async () => {
  const React = await import('react');
  return { GhosttyTerminal: React.forwardRef(function MockTerminal() { return null; }) };
});

// Sidebar stub that records the critical-state identity it is handed on each
// render. Reading identity rather than a render count is deliberate: App
// re-renders on every broadcast regardless — the change signal an open panel
// needs — so a render count cannot tell a repeat from a real change.
vi.mock('./components/Sidebar', () => ({
  EditorIcon: () => null,
  WorkflowIcon: () => null,
  DiffIcon: () => null,
  PRsIcon: () => null,
  NotebookIcon: () => null,
  MarkdownIcon: () => null,
  Sidebar: ({ criticalNotifications }: { criticalNotifications: unknown }) => {
    criticalSeen.push(criticalNotifications);
    return null;
  },
}));

vi.mock('./components/Dashboard', () => ({ Dashboard: () => null }));
vi.mock('./components/AttentionDrawer', () => ({ AttentionDrawer: () => null }));
vi.mock('./components/LocationPicker', () => ({ LocationPicker: () => null }));
vi.mock('./components/UndoToast', () => ({ UndoToast: () => null }));
vi.mock('./components/SessionTerminalWorkspace', () => ({ SessionTerminalWorkspace: () => null }));
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
vi.mock('./hooks/useDaemonSocket', () => ({
  useDaemonSocket: (args: {
    onNotificationsUpdated?: (unread: number, critical: CriticalNotificationState) => void;
  }) => {
    notifyCritical = args.onNotificationsUpdated ?? null;
    return mockUseDaemonSocket(args);
  },
}));
vi.mock('./pty/bridge', async () => {
  const actual = await vi.importActual<typeof import('./pty/bridge')>('./pty/bridge');
  return { ...actual, ptySpawn: vi.fn(async () => {}) };
});

function broadcast(unread: number, critical: CriticalNotificationState) {
  act(() => {
    notifyCritical?.(unread, critical);
  });
}

function latestCritical() {
  return criticalSeen[criticalSeen.length - 1];
}

describe('critical notification cadence', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    criticalSeen.length = 0;
    notifyCritical = null;
    localStorage.clear();
    localStorage.setItem(WHATS_NEW_STORAGE_KEY, WHATS_NEW_ID);

    mockUseSessionStore.mockReturnValue({
      sessions: [],
      activeSessionId: null,
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

    mockUseDaemonStore.mockImplementation(() => ({
      daemonSessions: [],
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
      sendNotificationList: vi.fn(async () => ({
        notifications: [], unreadCount: 0, critical: { count: 0, title: '' },
      })),
      sendNotificationMarkRead: vi.fn(async () => 0),
      rateLimit: null,
      warnings: [],
      clearWarnings: fn,
      sendSetTerminalTheme: fn,
    });
  });

  it('keeps one identity when a broadcast repeats the same critical state', () => {
    render(<App />);

    broadcast(4, { count: 2, title: 'Plugin stopped' });
    const afterFirst = latestCritical();

    broadcast(4, { count: 2, title: 'Plugin stopped' });

    expect(latestCritical()).toBe(afterFirst);
  });

  it('takes the new one when the surface has to clear', () => {
    render(<App />);

    broadcast(4, { count: 2, title: 'Plugin stopped' });
    const withCritical = latestCritical();

    broadcast(2, { count: 0, title: '' });

    expect(latestCritical()).not.toBe(withCritical);
    expect(latestCritical()).toEqual({ count: 0, title: '' });
  });

  it('takes the new one when only the newest critical title changes', () => {
    render(<App />);

    broadcast(4, { count: 1, title: 'Plugin stopped' });
    const first = latestCritical();

    broadcast(5, { count: 2, title: 'App runtime parked' });

    expect(latestCritical()).not.toBe(first);
    expect(latestCritical()).toEqual({ count: 2, title: 'App runtime parked' });
  });
});
