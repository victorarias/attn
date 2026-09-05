import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { MouseEvent as ReactMouseEvent } from 'react';
import { onOpenUrl, getCurrent } from '@tauri-apps/plugin-deep-link';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { getVersion } from '@tauri-apps/api/app';
import { openPath, openUrl } from '@tauri-apps/plugin-opener';
import { Sidebar, type SidebarHeaderAction, type DockItem, WorkflowIcon, EditorIcon, PRsIcon, NotebookIcon } from './components/Sidebar';
import { Dashboard } from './components/Dashboard';
import { activityStaleMs } from './utils/activitySettings';
import { crewDisplayName } from './utils/crewName';
import { AttentionDrawer } from './components/AttentionDrawer';
import { LocationPicker } from './components/LocationPicker';

import { UndoToast } from './components/UndoToast';
import { CloseSessionPrompt } from './components/CloseSessionPrompt';
import { ChiefOfStaffTransferPrompt } from './components/ChiefOfStaffTransferPrompt';
import { SessionContextCapPrompt } from './components/SessionContextCapPrompt';
import { AppViewParamsPrompt } from './components/appViews/AppViewParamsPrompt';
import { appViewTileKind } from './utils/appBundle';
import { GardenFrame, useDockSlotRect } from './components/GardenFrame';
import { WorkflowRunView } from './components/WorkflowRunView';
import { AutomationsPanel } from './components/AutomationsPanel';
import {
  useWorkflowRunsStore,
  selectLatestWorkflowRunForSession,
  workflowRunIdNeedingHydration,
} from './store/workflowRuns';
import { OpenPRLauncherProgress } from './components/OpenPRLauncherProgress';
import { SessionCreationProgress, type SessionCreationPhase } from './components/SessionCreationProgress';
import { RightDock } from './components/RightDock';
import { SessionTerminalWorkspace } from './components/SessionTerminalWorkspace';
import type { DockTarget } from './components/SessionTerminalWorkspace/dockTarget';
import { SettingsModal } from './components/SettingsModal';
import { ShortcutsModal } from './components/ShortcutsModal';
import { ShortcutEditorModal } from './components/ShortcutEditorModal';
import { WhatsNewModal } from './components/WhatsNewModal';
import { ActionMenu, type ActionMenuItem } from './components/ActionMenu';
import { SnoozeMenu } from './components/SnoozeMenu';
import { MarkdownOpener, OPENER_EXTENSIONS } from './components/palette/MarkdownOpener';
import { resolveMarkdownOpenerTarget } from './components/palette/openerTarget';
import { claimPaletteFocus } from './components/palette/paletteClaim';
import { NotebookBrowser } from './components/NotebookBrowser';
import { NotificationsPanel } from './components/NotificationsPanel';
import { ErrorToast, useErrorToast } from './components/ErrorToast';
import { useSavedFlash } from './components/useSavedFlash';
import { writeClipboardText } from './utils/clipboardBridge';
import { readTerminalInputDiagnostics } from './utils/terminalDiagnosticsLog';
import { ChordLeaderHud } from './components/ChordLeaderHud';
import { DaemonProvider } from './contexts/DaemonContext';
import { DaemonApiProvider, useDaemonApi } from './contexts/DaemonApiContext';
import { setMarkdownAnnotationsTransport } from './components/MarkdownReader/annotations/transport';
import { NotebookSurfaceProvider } from './contexts/NotebookSurfaceContext';
import { SettingsProvider } from './contexts/SettingsContext';
import { KeybindingsProvider, useKeybindings } from './contexts/KeybindingsContext';
import { useSessionStore, isSessionReloading, type Session, type TerminalWorkspaceState } from './store/sessions';
import {
  computeWarmWorkspaceIds,
  DEFAULT_WARM_WORKSPACE_LIMIT,
  readWarmWorkspaceLimit,
  writeWarmWorkspaceLimit,
} from './utils/terminalVirtualization';
import {
  persistWorkspaceSelectionStyle,
  readWorkspaceSelectionStyle,
  type WorkspaceSelectionStyle,
} from './utils/workspaceSelectionStyle';
import { useDaemonSocket, DaemonWorktree, DaemonSession, DaemonWorkspace, DaemonPR, DaemonEndpoint, DaemonPlugin, DaemonPluginIssue, GitStatusUpdate, SessionExitInfo, CriticalNotificationState, type SeedReviewActionContext } from './hooks/useDaemonSocket';
import type { Presentation } from './types/generated';
import { useSessionWorkspaceController } from './hooks/useSessionWorkspaceController';
import { useGardenPresentation } from './hooks/useGardenPresentation';
import { isAttentionSessionState, normalizeSessionState, type UISessionState } from './types/sessionState';
import { GridView, type GridSessionTile } from './components/grid/GridView';
import {
  type GridLayout,
  persistGridLayout,
  readGridLayout,
  resolveGridLayout,
} from './components/grid/gridLayout';
import { persistExcludedGridSessions, readExcludedGridSessions } from './components/grid/gridMembership';
import type { HiddenGridSession } from './components/grid/GridHiddenSessions';
import { normalizeSessionAgent, type SessionAgent } from './types/sessionAgent';
import { hasPane, workspaceSnapshotFromDaemonWorkspace, resolveEditorTileRoot, localWorkspaceDirectory, soleWorkspaceForId, serializeNotebookTileParams, type TerminalSplitDirection } from './types/workspace';
import { useDaemonStore } from './store/daemonSessions';
import { gardenPathToSeed, useGardenWalk } from './store/gardenWalk';
import { useConversationsStore } from './store/conversations';
import { usePRsNeedingAttention } from './hooks/usePRsNeedingAttention';
import { useKeyboardShortcuts } from './hooks/useKeyboardShortcuts';
import { useWhatsNew } from './hooks/useWhatsNew';
import { shortcutTokens, formatShortcut } from './shortcuts/formatShortcut';
import { dockShortcutLabel } from './shortcuts/metadata';
import type { ShortcutId } from './shortcuts/registry';
import {
  latestPresentationBySessionId,
  seedPresentationNotices,
  upsertPresentationNotice,
} from './utils/presentationNotices';
import {
  bumpFsChangeSignal,
  fsChangeSignalKey,
  fsIndexToNotebookEntries,
} from './utils/fsChangeSignals';
import { useUIScale } from './hooks/useUIScale';
import { useGardenScale } from './hooks/useGardenScale';
import { useTheme } from './hooks/useTheme';
import { useOpenPR, type OpenPRProgress } from './hooks/useOpenPR';
import { useUiAutomationBridge } from './hooks/useUiAutomationBridge';
import { useClientPresence } from './hooks/useClientPresence';
import { ptySpawn } from './pty/bridge';
import { clearBrowserHostFocus, controlBrowserHost, isBrowserHostOwnedTarget } from './browser/host';
import { probeUiAfterSwitch, UI_DIAGNOSTICS_FILE_DISPLAY } from './utils/uiDiagnosticsLog';
import { BannerStack } from './components/BannerStack';
import {
  agentLabel,
  conversationAgents,
  getAgentAvailability,
  getAgentExecutableSettings,
  hasAnyAvailableAgents,
  resolvePreferredAgent,
} from './utils/agentAvailability';
import { normalizeInstallChannel, shouldCheckForReleaseUpdates } from './utils/installChannel';
import { BUILD_PROFILE } from './utils/buildProfile';
import { buildWorkspaceViewModels, filterSessionsRepresentedInWorkspaceLayouts } from './utils/workspaceViewModels';
import {
  advanceAfterTurnClosed,
  buildQueueBands,
  headOfQueue,
  isCrewQueueEnabled,
  isQueueModeEnabled,
  oldestWantedTurn,
  QUEUE_CREW_SETTING,
  QUEUE_MODE_SETTING,
  sessionParticipatesInQueue,
  isAutoSettleEnabled,
  AUTO_SETTLE_ENABLED_SETTING,
} from './utils/queueBands';
import { useWorkspaceSelectionController } from './hooks/useWorkspaceSelectionController';
import { hideBootSplash } from './utils/bootSplash';
import { getTerminalAnsiPaletteColors, getTerminalTheme } from './utils/terminalSizing';
import './App.css';

const RELEASES_LATEST_API = 'https://api.github.com/repos/victorarias/attn/releases/latest';
const RELEASES_LATEST_WEB = 'https://github.com/victorarias/attn/releases/latest';
const RELEASE_CHECK_INTERVAL_MS = 6 * 60 * 60 * 1000;
const UPDATE_BANNER_DISMISSED_STORAGE_KEY = 'attn.update_banner.dismissed_version';
const DOCK_PANEL_EXIT_MS = 260;
const CHIEF_OF_STAFF_CLOSE_HINT = 'Chief of staff is protected — unset the chief role to close it.';

function crewMemberCloseHint(memberId: string): string {
  const name = crewDisplayName(memberId);
  return `${name} is protected — put ${name} to sleep to close the day.`;
}

function sessionCloseProtectionHint(sessions: DaemonSession[], id: string): string | null {
  const session = sessions.find((candidate) => candidate.id === id);
  if (session?.chief_of_staff === true) {
    return CHIEF_OF_STAFF_CLOSE_HINT;
  }
  return session?.crew_member ? crewMemberCloseHint(session.crew_member) : null;
}

const TERMINAL_AGENT: SessionAgent = 'shell';

type LocationPickerPurpose = 'workspace' | 'session';

function handleAppPointerDownCapture(event: { target: EventTarget | null }): void {
  if (!isBrowserHostOwnedTarget(event.target)) {
    clearBrowserHostFocus();
  }
}

function ContextActionIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M6 3.5h9l3 3V20a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1V4.5a1 1 0 0 1 1-1Z" />
      <path d="M15 3.5V7h3M8 11h7M8 14.5h7M8 18h4" />
    </svg>
  );
}

function AttentionActionIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M12 3.5a6 6 0 0 0-6 6v3.2L4.5 16h15L18 12.7V9.5a6 6 0 0 0-6-6Z" />
      <path d="M9.5 19a2.8 2.8 0 0 0 5 0" />
    </svg>
  );
}

function KeyboardActionIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <rect x="3" y="6" width="18" height="12" rx="2" />
      <path d="M7 10h.01M11 10h.01M15 10h.01M8 13.5h8" />
    </svg>
  );
}

function BoardActionIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <rect x="2.5" y="2.5" width="3" height="11" rx="0.8" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinejoin="round" />
      <rect x="6.5" y="2.5" width="3" height="7" rx="0.8" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinejoin="round" />
      <rect x="10.5" y="2.5" width="3" height="9" rx="0.8" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinejoin="round" />
    </svg>
  );
}

function NotificationsBellIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path
        d="M8 2.2c-2.1 0-3.5 1.6-3.5 3.7 0 2.6-.7 3.6-1.4 4.3-.3.3-.1.9.4.9h9c.5 0 .7-.6.4-.9-.7-.7-1.4-1.7-1.4-4.3 0-2.1-1.4-3.7-3.5-3.7Z"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.4"
        strokeLinejoin="round"
      />
      <path d="M6.6 12.6a1.5 1.5 0 0 0 2.8 0" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
    </svg>
  );
}

function AutomationsIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path
        d="M8 1.6 9.6 5l3.6.6-2.6 2.6.6 3.6-3.2-1.7-3.2 1.7.6-3.6L2.8 5.6 6.4 5 8 1.6Z"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.4"
        strokeLinejoin="round"
      />
      <circle cx="8" cy="8" r="1.3" fill="currentColor" stroke="none" />
    </svg>
  );
}

function GardenIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path d="M8 14V7.4" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
      <path
        d="M8 7.4C8 5.3 6.4 3.6 4.2 3.6c0 2.1 1.6 3.8 3.8 3.8Z"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.4"
        strokeLinejoin="round"
      />
      <path
        d="M8 7.4c0-2.1 1.6-3.8 3.8-3.8 0 2.1-1.6 3.8-3.8 3.8Z"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.4"
        strokeLinejoin="round"
      />
    </svg>
  );
}

interface SplitSessionOptions {
  baseSessionId?: string;
  cwd?: string;
  endpointId?: string | null;
  label?: string;
  yoloMode?: boolean;
  autoMode?: boolean;
}

function paneIdForSession(sessionId: string): string {
  return `pane-${sessionId}`;
}

interface GitHubReleaseResponse {
  tag_name?: string;
  html_url?: string;
  prerelease?: boolean;
  draft?: boolean;
}

interface LeafWorkspaceDragState {
  sourceWorkspaceId: string;
  sourceEndpointId?: string;
  leafId: string;
}

interface LeafDragPreviewState {
  draggingLeafId: string | null;
  dockTarget: DockTarget | null;
  ghostPos: { x: number; y: number } | null;
}

const SIDEBAR_LEAF_DROP_PLACEMENT = { anchorId: '', edge: 'left' as const, ratio: 0.32 };


function terminalStateForWorkspaceSessions(sessions: Session[]): TerminalWorkspaceState | null {
  let selected: TerminalWorkspaceState | null = null;
  for (const session of sessions) {
    const candidate = session.workspace;
    if (!candidate.layoutTree && candidate.agents.length === 0) {
      continue;
    }
    if (!selected || candidate.agents.length > selected.agents.length) {
      selected = candidate;
    }
  }
  return selected;
}

function activePaneIdForWorkspace(
  workspace: TerminalWorkspaceState,
  focusedSessionId: string | null,
): string {
  if (focusedSessionId) {
    const focusedPane = workspace.agents.find((pane) => pane.sessionId === focusedSessionId);
    if (focusedPane) {
      return focusedPane.id;
    }
  }
  return workspace.agents[0]?.id || '';
}

function activePaneIdForFocusedSession(
  workspace: TerminalWorkspaceState,
  session: Session | null,
  getActivePaneIdForSession: (session: Session | undefined | null) => string,
): string {
  const sessionActivePaneId = getActivePaneIdForSession(session);
  if (
    sessionActivePaneId
    && workspace.layoutTree
    && hasPane(workspace.layoutTree, sessionActivePaneId)
  ) {
    return sessionActivePaneId;
  }
  return activePaneIdForWorkspace(workspace, session?.id ?? null);
}

function parseSemver(version: string): [number, number, number] | null {
  const match = version.trim().match(/^v?(\d+)\.(\d+)\.(\d+)(?:[-+].*)?$/);
  if (!match) return null;
  return [Number(match[1]), Number(match[2]), Number(match[3])];
}

function isNewerVersion(currentVersion: string, latestVersion: string): boolean {
  const current = parseSemver(currentVersion);
  const latest = parseSemver(latestVersion);
  if (!current || !latest) return false;

  if (latest[0] !== current[0]) return latest[0] > current[0];
  if (latest[1] !== current[1]) return latest[1] > current[1];
  return latest[2] > current[2];
}

function getDismissedUpdateVersion(): string | null {
  try {
    return window.localStorage.getItem(UPDATE_BANNER_DISMISSED_STORAGE_KEY);
  } catch (err) {
    console.warn('[App] Failed to read dismissed update version:', err);
    return null;
  }
}

function persistDismissedUpdateVersion(version: string): void {
  try {
    window.localStorage.setItem(UPDATE_BANNER_DISMISSED_STORAGE_KEY, version);
  } catch (err) {
    console.warn('[App] Failed to persist dismissed update version:', err);
  }
}

const SHOW_SESSIONLESS_WORKSPACES_STORAGE_KEY = 'attn.sidebar.showSessionless';

function readShowSessionlessWorkspaces(): boolean {
  try {
    return window.localStorage.getItem(SHOW_SESSIONLESS_WORKSPACES_STORAGE_KEY) === '1';
  } catch {
    return false;
  }
}

function persistShowSessionlessWorkspaces(value: boolean): void {
  try {
    window.localStorage.setItem(SHOW_SESSIONLESS_WORKSPACES_STORAGE_KEY, value ? '1' : '0');
  } catch (err) {
    console.warn('[App] Failed to persist show-sessionless preference:', err);
  }
}

function toneForDockPanel(status?: string): 'default' | 'idle' | 'running' | 'awaiting_user' | 'completed' | 'stopped' | 'error' {
  switch (status) {
    case 'running':
    case 'awaiting_user':
    case 'completed':
    case 'stopped':
    case 'error':
      return status;
    default:
      return 'default';
  }
}

type OpenPRLauncherJob = {
  id: number;
  pr: DaemonPR;
  progress: OpenPRProgress;
};

type SessionCreationJob = {
  id: number;
  label: string;
  path: string;
  phase: SessionCreationPhase;
  sessionId?: string;
  error?: string | null;
};

function App() {
  const [settings, setSettings] = useState<Record<string, string>>({});
  const [settingError, setSettingError] = useState<string | null>(null);
  const [daemonEndpoints, setDaemonEndpoints] = useState<DaemonEndpoint[]>([]);
  const [daemonPlugins, setDaemonPlugins] = useState<DaemonPlugin[]>([]);
  const [daemonPluginIssues, setDaemonPluginIssues] = useState<DaemonPluginIssue[]>([]);
  const [daemonGitHubHosts, setDaemonGitHubHosts] = useState<string[]>([]);
  const handlePluginsUpdate = useCallback((plugins: DaemonPlugin[], issues: DaemonPluginIssue[]) => {
    setDaemonPlugins(plugins);
    setDaemonPluginIssues(issues);
  }, []);

  const [daemonWorkspaces, setDaemonWorkspaces] = useState<DaemonWorkspace[]>([]);

  const [, setWorktrees] = useState<DaemonWorktree[]>([]);

  // The value is unread; only the subscribe/unsubscribe lifecycle still runs.
  const [, setGitStatus] = useState<GitStatusUpdate | null>(null);
  const [updateAvailableVersion, setUpdateAvailableVersion] = useState<string | null>(null);
  const [updateReleaseUrl, setUpdateReleaseUrl] = useState<string>(RELEASES_LATEST_WEB);
  const [dismissedUpdateVersion, setDismissedUpdateVersion] = useState<string | null>(() => getDismissedUpdateVersion());
  const installChannel = normalizeInstallChannel(import.meta.env.VITE_INSTALL_CHANNEL);

  const [presentationNotices, setPresentationNotices] = useState<Presentation[]>([]);

  const {
    daemonSessions,
    setDaemonSessions,
    setSeeds,
    setApps,
    setCrew,
    prs,
    setPRs,
    setRepoStates,
    setAuthorStates,
  } = useDaemonStore();

  useEffect(() => {
    hideBootSplash();
  }, []);

  useEffect(() => {
    async function ensureDaemon() {
      try {
        await invoke('ensure_daemon');
        console.log('[App] Daemon ensured');
      } catch (err) {
        console.error('[App] Failed to start daemon:', err);
      }
    }
    ensureDaemon();
  }, []);

  useEffect(() => {
    if (!isTauri()) return;
    if (!shouldCheckForReleaseUpdates(installChannel)) {
      setUpdateAvailableVersion(null);
      return;
    }

    let cancelled = false;
    let intervalId: ReturnType<typeof setInterval> | undefined;

    const checkLatestRelease = async () => {
      try {
        const currentVersion = await getVersion();
        const response = await fetch(RELEASES_LATEST_API, {
          headers: {
            Accept: 'application/vnd.github+json',
          },
        });

        if (!response.ok) {
          throw new Error(`GitHub API returned ${response.status}`);
        }

        const latest = await response.json() as GitHubReleaseResponse;
        if (cancelled) return;

        if (latest.draft || latest.prerelease || !latest.tag_name) {
          setUpdateAvailableVersion(null);
          setUpdateReleaseUrl(RELEASES_LATEST_WEB);
          return;
        }

        const releaseUrl = latest.html_url || RELEASES_LATEST_WEB;
        setUpdateReleaseUrl(releaseUrl);

        const latestVersion = latest.tag_name.replace(/^v/, '');
        if (isNewerVersion(currentVersion, latest.tag_name)) {
          if (dismissedUpdateVersion === latestVersion) {
            setUpdateAvailableVersion(null);
            return;
          }

          setUpdateAvailableVersion(latestVersion);
          return;
        }

        setUpdateAvailableVersion(null);
      } catch (err) {
        if (cancelled) return;
        console.warn('[App] Failed to check latest release:', err);
      }
    };

    void checkLatestRelease();
    intervalId = setInterval(() => {
      void checkLatestRelease();
    }, RELEASE_CHECK_INTERVAL_MS);

    return () => {
      cancelled = true;
      if (intervalId) clearInterval(intervalId);
    };
  }, [dismissedUpdateVersion, installChannel]);

  const handleOpenLatestRelease = useCallback(async () => {
    try {
      await openUrl(updateReleaseUrl);
    } catch (err) {
      console.error('[App] Failed to open release URL:', err);
    }
  }, [updateReleaseUrl]);

  const handleDismissLatestRelease = useCallback(() => {
    if (!updateAvailableVersion) return;

    persistDismissedUpdateVersion(updateAvailableVersion);
    setDismissedUpdateVersion(updateAvailableVersion);
    setUpdateAvailableVersion(null);
  }, [updateAvailableVersion]);

  const sessionExitHandlerRef = useRef<((info: SessionExitInfo) => void) | null>(null);
  const registerSessionExitHandler = useCallback((handler: ((info: SessionExitInfo) => void) | null) => {
    sessionExitHandlerRef.current = handler;
  }, []);
  const handleSessionExited = useCallback((info: SessionExitInfo) => {
    sessionExitHandlerRef.current?.(info);
  }, []);

  const [fsChangeSignals, setFsChangeSignals] = useState<Record<string, number>>({});
  const [notebookTaskChangeSignal, setTaskChangeSignal] = useState(0);
  const [notificationsUnread, setNotificationsUnread] = useState(0);
  const [notificationsChangeSignal, setNotificationsChangeSignal] = useState(0);
  const [criticalNotifications, setCriticalNotificationsState] =
    useState<CriticalNotificationState>({ count: 0, title: '' });
  const setCriticalNotifications = useCallback((next: CriticalNotificationState) => {
    setCriticalNotificationsState((prev) =>
      prev.count === next.count && prev.title === next.title ? prev : next,
    );
  }, []);

  const daemon = useDaemonSocket({
    onSessionsUpdate: (sessions) => {
      setDaemonSessions(sessions);
      useConversationsStore.getState().retainConversations(sessions.map((session) => session.id));
    },
    onPresentationAdded: (p) => setPresentationNotices((prev) => upsertPresentationNotice(prev, p)),
    onPresentationUpdated: (p) => setPresentationNotices((prev) => upsertPresentationNotice(prev, p)),
    onFsChanged: (_origin, _paths, root) => {
      setFsChangeSignals((prev) => bumpFsChangeSignal(prev, root, settings['notebook.root.effective'] || ''));
    },
    onTasksChanged: () => setTaskChangeSignal((n) => n + 1),
    onNotificationsUpdated: (unread, critical) => {
      setNotificationsUnread(unread);
      setCriticalNotifications(critical);
      setNotificationsChangeSignal((n) => n + 1);
    },
    onSeedsUpdate: setSeeds,
    onAppsUpdate: setApps,
    onCrewUpdate: setCrew,
    onWorkspacesUpdate: setDaemonWorkspaces,
    onPRsUpdate: setPRs,
    onEndpointsUpdate: setDaemonEndpoints,
    onPluginsUpdate: handlePluginsUpdate,
    onGitHubHostsUpdate: setDaemonGitHubHosts,
    onReposUpdate: setRepoStates,
    onAuthorsUpdate: setAuthorStates,
    onSettingsUpdate: setSettings,
    onSettingError: setSettingError,
    onWorktreesUpdate: setWorktrees,
    onGitStatusUpdate: setGitStatus,
    onSessionExited: handleSessionExited,
  });

  const {
    getMarkdownAnnotations,
    saveMarkdownAnnotations,
    clearMarkdownAnnotations,
    submitMarkdownAnnotations,
    sendSetSetting,
    sendNotificationList,
    getPresentations,
    hasReceivedInitialState,
  } = daemon;

  const clearGitStatus = useCallback(() => setGitStatus(null), []);

  useEffect(() => {
    setMarkdownAnnotationsTransport({
      getMarkdownAnnotations,
      saveMarkdownAnnotations,
      clearMarkdownAnnotations,
      submitMarkdownAnnotations,
    });
    return () => {
      setMarkdownAnnotationsTransport(null);
    };
  }, [getMarkdownAnnotations, saveMarkdownAnnotations, clearMarkdownAnnotations, submitMarkdownAnnotations]);

  useEffect(() => {
    if (!hasReceivedInitialState) return;
    let cancelled = false;
    sendNotificationList()
      .then((r) => {
        if (cancelled) return;
        setNotificationsUnread(r.unreadCount);
        setCriticalNotifications(r.critical);
      })
      .catch(() => {
        /* transient (not connected / timeout); the next broadcast reseeds */
      });
    return () => {
      cancelled = true;
    };
  }, [hasReceivedInitialState, sendNotificationList, setCriticalNotifications]);

  useEffect(() => {
    if (!hasReceivedInitialState) return;
    let cancelled = false;
    getPresentations()
      .then((all) => {
        if (!cancelled) setPresentationNotices(seedPresentationNotices(all));
      })
      .catch(() => {
        /* transient (not connected / timeout); the next broadcast reseeds */
      });
    return () => {
      cancelled = true;
    };
  }, [hasReceivedInitialState, getPresentations]);

  return (
    <SettingsProvider settings={settings} setSetting={sendSetSetting}>
      <KeybindingsProvider>
      <DaemonApiProvider api={daemon}>
        <AppContent
          daemonSessions={daemonSessions}
          daemonWorkspaces={daemonWorkspaces}
          prs={prs}
          daemonEndpoints={daemonEndpoints}
          daemonPlugins={daemonPlugins}
          daemonPluginIssues={daemonPluginIssues}
          daemonGitHubHosts={daemonGitHubHosts}
          settings={settings}
          updateAvailableVersion={updateAvailableVersion}
          onOpenLatestRelease={handleOpenLatestRelease}
          onDismissLatestRelease={handleDismissLatestRelease}
          presentationNotices={presentationNotices}
          settingError={settingError}
          clearSettingError={() => setSettingError(null)}
          notificationsUnread={notificationsUnread}
          criticalNotifications={criticalNotifications}
          notificationsChangeSignal={notificationsChangeSignal}
          fsChangeSignals={fsChangeSignals}
          notebookTaskChangeSignal={notebookTaskChangeSignal}
          clearGitStatus={clearGitStatus}
          registerSessionExitHandler={registerSessionExitHandler}
        />
      </DaemonApiProvider>
      </KeybindingsProvider>
    </SettingsProvider>
  );
}

interface AppContentProps {
  daemonSessions: DaemonSession[];
  daemonWorkspaces: DaemonWorkspace[];
  prs: DaemonPR[];
  daemonEndpoints: DaemonEndpoint[];
  daemonPlugins: DaemonPlugin[];
  daemonPluginIssues: DaemonPluginIssue[];
  daemonGitHubHosts: string[];
  settings: Record<string, string>;
  updateAvailableVersion: string | null;
  onOpenLatestRelease: () => Promise<void>;
  onDismissLatestRelease: () => void;
  presentationNotices: Presentation[];
  settingError: string | null;
  clearSettingError: () => void;
  notificationsUnread: number;
  criticalNotifications: CriticalNotificationState;
  notificationsChangeSignal: number;
  fsChangeSignals: Record<string, number>;
  notebookTaskChangeSignal: number;
  clearGitStatus: () => void;
  registerSessionExitHandler: (handler: ((info: SessionExitInfo) => void) | null) => void;
}

function AppContent({
  daemonSessions,
  daemonWorkspaces,
  prs,
  daemonEndpoints,
  daemonPlugins,
  daemonPluginIssues,
  daemonGitHubHosts,
  settings,
  updateAvailableVersion,
  onOpenLatestRelease,
  onDismissLatestRelease,
  presentationNotices,
  settingError,
  clearSettingError,
  notificationsUnread,
  criticalNotifications,
  notificationsChangeSignal,
  fsChangeSignals,
  notebookTaskChangeSignal,
  clearGitStatus,
  registerSessionExitHandler,
}: AppContentProps) {
  const hasCriticalNotification = criticalNotifications.count > 0;

  const {
    connectionError,
    disconnectExplanation,
    clearDisconnectExplanation,
    connectionGeneration,
    hasReceivedInitialState,
    rateLimit,
    warnings,
    clearWarnings,
    sendPRAction,
    getScreenSnapshot,
    sendMutePR,
    sendMuteRepo,
    sendMuteAuthor,
    sendMuteWorkspace,
    sendPinWorkspace,
    sendPinSession,
    sendPRVisited,
    sendRefreshPRs,
    sendRegisterWorkspace,
    sendUnregisterWorkspace,
    sendRenameSession,
    sendRenameWorkspace,
    sendSetChiefOfStaff,
    sendSetSessionContextWindowCap,
    sendUnregisterSession,
    sendSetSetting,
    sendCreateWorktree,
    sendDeleteWorktree,
    sendListPlugins,
    sendInstallPlugin,
    sendInstallBundledPlugin,
    sendUninstallPlugin,
    sendRemovePlugin,
    sendSetPluginPriority,
    sendAddEndpoint,
    sendUpdateEndpoint,
    sendRemoveEndpoint,
    sendSetEndpointRemoteWeb,
    sendBootstrapEndpoint,
    sendFsList,
    sendFsRead,
    sendFsWrite,
    sendFsExists,
    sendFsReadAsset,
    sendFsWatch,
    sendFsUnwatch,
    sendFsIndex,
    sendRecentFiles,
    sendTaskList,
    sendTaskRetry,
    sendNotificationList,
    sendNotificationMarkRead,
    sendNotebookBacklinks,
    sendNotebookToChief,
    sendGetRecentLocations,
    sendListPastConversations,
    sendBrowseDirectory,
    sendInspectPath,
    sendCreateWorktreeFromBranch,
    sendFetchPRDetails,
    sendEnsureRepo,
    sendSubscribeGitStatus,
    sendUnsubscribeGitStatus,
    sendSessionSelected,
    sendTriggerNudge,
    sendSettleTurn,
    sendSnoozeTurn,
    sendWakeTurn,
    sendCancelCountdown,
    sendWorkspaceSelected,
    sendWorkspaceAddSessionPane,
    sendWorkspaceClosePane,
    sendWorkspaceSetSplitRatio,
    sendWorkspaceDockTile,
    sendWorkspaceUndockTile,
    sendWorkspaceUpdateTile,
    sendOpenMarkdown,
    sendOpenSeed,
    sendSeedDocumentGet,
    sendSeedTransition,
    sendSeedNote,
    sendSessionMessagesGet,
    subscribeSessionMessagesChanged,
    sendSessionAnnotationsGet,
    sendSessionAnnotationsSave,
    sendSessionAnnotationsClear,
    sendSessionAnnotationsSubmit,
    sendWorkspaceMoveLeaf,
    sendWorkspaceMoveLeafToWorkspace,
    sendWorkspaceMoveLeafToNewWorkspace,
    sendSetWorkspaceRank,
    tileContents,
    requestTileContent,
    sendRuntimeInput,
    sendTerminalPointerActivity,
    sendSetClientPresence,
    sendSetTerminalTheme,
    isRuntimeAttached,
    getRepoInfo,
    listWorkflowRuns,
    getWorkflowRun,
    listAutomationDefinitions,
    listAutomationRuns,
    setAutomationEnabled,
    runAutomationNow,
    getAutomationDefinition,
    applyAutomationDefinition,
    deleteAutomationDefinition,
    sendSeedHandover,
    sendSeedToChief,
    sendSeedResume,
    seedReviewOverview,
    sendSeedReviewShow,
    sendSeedReviewStart,
    sendSeedReviewRetry,
    sendSeedReviewKeep,
    sendSeedReviewDraft,
    sendCrewWake,
    sendCrewSleep,
  } = useDaemonApi();

  const presentationBySessionId = useMemo(
    () => latestPresentationBySessionId(presentationNotices),
    [presentationNotices],
  );
  const handleOpenPresentationWindow = useCallback((presentationId: string) => {
    void invoke('open_presentation_window', { presentationId }).catch((err) => {
      console.error('[App] Failed to open presentation window:', err);
    });
  }, []);

  const [openPRLauncherJob, setOpenPRLauncherJob] = useState<OpenPRLauncherJob | null>(null);
  const openPRLauncherIdRef = useRef(0);
  const [sessionCreationJob, setSessionCreationJob] = useState<SessionCreationJob | null>(null);
  const sessionCreationJobIdRef = useRef(0);
  const worktreeSessionCreateEndpointsRef = useRef<Set<string>>(new Set());
  const {
    connect,
    sessions,
    activeSessionId,
    createSession,
    closeSession,
    setActiveSession,
    takeSessionSpawnArgs,
    reloadSession,
    setLauncherConfig,
    syncFromDaemonSessions,
    syncFromDaemonWorkspaces,
  } = useSessionStore();

  const [selectedSessionlessWorkspaceId, setSelectedSessionlessWorkspaceId] = useState<string | null>(null);
  const [selectedTile, setSelectedTile] = useState<{ workspaceId: string; tileId: string } | null>(null);
  const selectWorkspaceRef = useRef<(workspaceId: string) => void>(() => {});

  const rollbackSessionCreation = useCallback(async ({
    sessionId,
    workspaceId,
    paneId,
    unregisterWorkspace,
  }: {
    sessionId: string;
    workspaceId: string;
    paneId?: string;
    unregisterWorkspace?: boolean;
  }) => {
    if (paneId) {
      await sendWorkspaceClosePane(workspaceId, paneId).catch((error) => {
        console.error('[App] Failed to rollback workspace pane:', error);
      });
    }
    closeSession(sessionId);
    if (unregisterWorkspace) {
      await sendUnregisterWorkspace(workspaceId).catch((error) => {
        console.error('[App] Failed to rollback workspace:', error);
      });
    }
  }, [closeSession, sendUnregisterWorkspace, sendWorkspaceClosePane]);

  const createWorkspaceSession = useCallback(async (
    label: string,
    cwd: string,
    providedSessionId?: string,
    agent?: SessionAgent,
    endpointId?: string,
    yoloMode = false,
    options?: { chiefOfStaff?: boolean; resumeConversationFile?: string; autoMode?: boolean },
  ) => {
    const sessionId = providedSessionId || crypto.randomUUID();
    const workspaceId = `workspace-${sessionId}`;
    const paneId = paneIdForSession(sessionId);
    let localCreated = false;
    let paneAdded = false;
    try {
      await sendRegisterWorkspace(workspaceId, label, cwd, endpointId);
      const createdSessionId = await createSession(label, cwd, sessionId, agent, endpointId, yoloMode, workspaceId, options?.chiefOfStaff, options?.resumeConversationFile, options?.autoMode);
      localCreated = true;
      const spawnArgs = takeSessionSpawnArgs(sessionId, 80, 24);
      if (!spawnArgs) {
        throw new Error('Session spawn arguments were not prepared.');
      }
      await sendWorkspaceAddSessionPane(workspaceId, sessionId, label, { paneId });
      paneAdded = true;
      await ptySpawn({ args: spawnArgs });
      return createdSessionId;
    } catch (error) {
      if (localCreated) {
        await rollbackSessionCreation({
          sessionId,
          workspaceId,
          paneId: paneAdded ? paneId : undefined,
          unregisterWorkspace: true,
        });
      } else {
        await sendUnregisterWorkspace(workspaceId).catch(console.error);
      }
      throw error;
    }
  }, [
    createSession,
    rollbackSessionCreation,
    sendRegisterWorkspace,
    sendWorkspaceAddSessionPane,
    sendUnregisterWorkspace,
    takeSessionSpawnArgs,
  ]);

  useEffect(() => {
    if (!sessionCreationJob?.sessionId || sessionCreationJob.error) {
      return;
    }
    if (daemonSessions.some((session) => session.id === sessionCreationJob.sessionId)) {
      setSessionCreationJob((current) => (
        current?.id === sessionCreationJob.id ? null : current
      ));
      return;
    }
    const timeoutId = window.setTimeout(() => {
      setSessionCreationJob((current) => (
        current?.id === sessionCreationJob.id
          ? { ...current, error: 'Session startup timed out.' }
          : current
      ));
    }, 35_000);
    return () => window.clearTimeout(timeoutId);
  }, [daemonSessions, sessionCreationJob]);

  const { scale, increaseScale, decreaseScale, resetScale } = useUIScale();
  const terminalFontSize = Math.round(14 * scale);

  const gardenScale = useGardenScale(scale);

  const { preference: themePreference, resolved: resolvedTheme, setTheme } = useTheme();
  const keybindings = useKeybindings();

  // The daemon worker answers OSC 10/11/12 on the frontend's behalf, so it needs the resolved theme; hasReceivedInitialState doubles as the reconnect signal.
  useEffect(() => {
    if (!hasReceivedInitialState) return;
    const theme = getTerminalTheme(resolvedTheme);
    sendSetTerminalTheme({
      foreground: theme.foreground,
      background: theme.background,
      cursor: theme.cursor,
      ansi_palette: getTerminalAnsiPaletteColors(resolvedTheme),
    });
  }, [hasReceivedInitialState, resolvedTheme, sendSetTerminalTheme]);

  const [settingsOpen, setSettingsOpen] = useState(false);
  const [shortcutsOpen, setShortcutsOpen] = useState(false);
  const [shortcutEditorOpen, setShortcutEditorOpen] = useState(false);
  const [actionMenuOpen, setActionMenuOpen] = useState(false);
  const [seedPopoverRequest, setSeedPopoverRequest] = useState<{ sessionId: string; nonce: number }>();
  const [usagePopoverRequest, setUsagePopoverRequest] = useState<{ sessionId: string; nonce: number }>();
  const [notebookOpen, setNotebookOpen] = useState(false);
  const [notebookRequestedPath, setNotebookRequestedPath] = useState<string | null>(null);
  const [notificationsPanelOpen, setNotificationsPanelOpen] = useState(false);
  const whatsNew = useWhatsNew();
  const { repoStates, authorStates, seeds, seedsTotal, apps, crew } = useDaemonStore();
  const mutedRepos = useMemo(() =>
    repoStates.filter(r => r.muted).map(r => r.repo),
    [repoStates],
  );
  const mutedAuthors = useMemo(() =>
    authorStates.filter(a => a.muted).map(a => a.author),
    [authorStates],
  );
  const [isRefreshingPRs, setIsRefreshingPRs] = useState(false);
  const [refreshError, setRefreshError] = useState<string | null>(null);

  const [pendingSessionClose, setPendingSessionClose] = useState<{
    id: string;
    label: string;
    splitCount: number;
  } | null>(null);

  const agentAvailability = useMemo(() => getAgentAvailability(settings), [settings]);
  const hasAvailableAgents = useMemo(
    () => hasAnyAvailableAgents(agentAvailability),
    [agentAvailability],
  );

  const conversationPaneAgents = useMemo(() => conversationAgents(settings), [settings]);

  useEffect(() => {
    setLauncherConfig({
      executables: getAgentExecutableSettings(settings),
    });
  }, [settings, setLauncherConfig]);

  useEffect(() => {
    if (!hasReceivedInitialState) {
      return;
    }
    syncFromDaemonSessions(daemonSessions);
  }, [daemonSessions, hasReceivedInitialState, syncFromDaemonSessions]);

  useEffect(() => {
    if (!hasReceivedInitialState) {
      return;
    }
    syncFromDaemonWorkspaces(daemonWorkspaces);
  }, [daemonWorkspaces, hasReceivedInitialState, syncFromDaemonWorkspaces]);

  const handleRefreshPRs = useCallback(async () => {
    setIsRefreshingPRs(true);
    setRefreshError(null);
    try {
      const result = await sendRefreshPRs();
      if (!result.success) {
        setRefreshError(result.error || 'Refresh failed');
      }
    } catch (err) {
      setRefreshError(err instanceof Error ? err.message : 'Refresh failed');
    } finally {
      setIsRefreshingPRs(false);
    }
  }, [sendRefreshPRs]);

  const processedDeepLinks = useRef(new Set<string>());

  const handleDeepLinkUrl = useCallback((urlStr: string) => {
    if (processedDeepLinks.current.has(urlStr)) {
      return;
    }
    processedDeepLinks.current.add(urlStr);

    try {
      const url = new URL(urlStr);
      if (url.host === 'spawn') {
        const cwd = url.searchParams.get('cwd');
        const label = url.searchParams.get('label') || cwd?.split('/').pop() || 'session';
        if (cwd) {
          const currentSessions = useSessionStore.getState().sessions;
          const existingSession = currentSessions.find((s) => s.cwd === cwd);
          if (existingSession) {
            setActiveSession(existingSession.id);
          } else {
            void createWorkspaceSession(label, cwd);
          }
        }
      }
    } catch (e) {
      console.error('Failed to parse deep-link URL:', e);
    }
  }, [createWorkspaceSession, setActiveSession]);

  useEffect(() => {
    getCurrent().then((urls) => {
      if (urls && urls.length > 0) {
        console.log('[DeepLink] Cold start URLs:', urls);
        for (const urlStr of urls) {
          handleDeepLinkUrl(urlStr);
        }
      }
    }).catch((err) => {
      console.error('[DeepLink] getCurrent failed:', err);
    });
  }, [handleDeepLinkUrl]);

  useEffect(() => {
    const unlisten = onOpenUrl((urls) => {
      for (const urlStr of urls) {
        handleDeepLinkUrl(urlStr);
      }
    });

    return () => {
      unlisten.then((fn) => fn());
    };
  }, [handleDeepLinkUrl]);

  const endpointById = useMemo(
    () => new Map(daemonEndpoints.map((endpoint) => [endpoint.id, endpoint])),
    [daemonEndpoints],
  );

  const enrichedLocalSessions = sessions.map((s) => {
    const daemonSession = daemonSessions.find((ds) => ds.id === s.id);
    const rawState = daemonSession?.state ?? s.state;
    const paneStatus = s.workspace.agents.find((pane) => pane.sessionId === s.id)?.status;
    const paneState = paneStatus === 'failed'
      ? 'unknown'
      : paneStatus === 'spawning'
        ? 'launching'
        : null;
    const endpointId = daemonSession?.endpoint_id ?? s.endpointId;
    const endpoint = endpointId ? endpointById.get(endpointId) : undefined;
    return {
      ...s,
      state: paneState || normalizeSessionState(rawState),
      endpointId,
      endpointName: endpoint?.name,
      endpointStatus: endpoint?.status,
      branch: daemonSession?.branch ?? s.branch,
      isWorktree: daemonSession?.is_worktree ?? s.isWorktree,
      chiefOfStaff: daemonSession?.chief_of_staff ?? false,
      delegatedFromChief: daemonSession?.delegated_from_chief ?? false,
      ticketUnread: daemonSession?.ticket_unread ?? false,
      seedId: daemonSession?.seed_id,
      nudgeFiresAt: daemonSession?.nudge_fires_at,
      turnOwed: daemonSession?.turn_owed ?? false,
      turnOpenedAt: daemonSession?.turn_opened_at,
      turnSnoozedUntil: daemonSession?.turn_snoozed_until,
      activity: daemonSession?.activity,
      activityAt: daemonSession?.activity_at,
      pinnedAt: daemonSession?.pinned_at,
      crewMember: daemonSession?.crew_member,
      contextWindowCap: daemonSession?.context_window_cap,
      parentSessionId: daemonSession?.parent_session_id,
      autoSettleFiresAt: daemonSession?.auto_settle_fires_at,
      autoSettleHeld: daemonSession?.auto_settle_held ?? false,
      autoSettleDismissArmed: daemonSession?.auto_settle_dismiss_armed ?? false,
      terminalBuildStale: daemonSession?.terminal_build_stale ?? false,
      usage: daemonSession?.usage,
      automation: daemonSession?.automation ?? s.automation,
      pullRequests: daemonSession?.pull_requests ?? s.pullRequests,
      state_reason: paneState ? undefined : daemonSession?.state_reason,
    };
  });

  const visibleEnrichedSessions = filterSessionsRepresentedInWorkspaceLayouts(daemonWorkspaces, enrichedLocalSessions);

  const notebookChiefSession = enrichedLocalSessions.find((session) => session.chiefOfStaff);
  const notebookChiefActive = notebookChiefSession ? notebookChiefSession.state === 'working' : undefined;

  const {
    eventRouter: paneRuntimeEventRouter,
    getActivePaneIdForSession,
    setActivePane,
    prepareClosePaneFocus,
    clearPreparedClosePaneFocus,
    setWorkspaceRef,
    removeWorkspaceRef,
    getWorkspaceLeafDropSnapshot,
    focusWorkspaceLeaf,
    focusSessionPane,
    typeInSessionPaneViaUI,
    isSessionPaneInputFocused,
    scrollSessionPaneToTop,
    fitSessionActivePane,
    getPaneText,
    getPaneSize,
    getPaneVisibleContent,
    getPaneVisibleStyleSummary,
    getPaneBlockState,
    getPanePlacementState,
    resetSessionPaneTerminal,
    injectSessionPaneBytes,
    injectSessionPaneBase64,
    drainSessionPaneTerminal,
  } = useSessionWorkspaceController(sessions, activeSessionId);

  useEffect(() => {
    void connect();
  }, [connect]);

  type DockPanelId = 'workflowRun' | 'attention' | 'automations' | 'garden';

  const [sidebarMutedExpanded, setSidebarMutedExpanded] = useState(false);

  const [view, setView] = useState<'dashboard' | 'session' | 'grid'>('dashboard');

  useClientPresence(sendSetClientPresence, {
    dashboardVisible: view === 'dashboard',
    connected: hasReceivedInitialState,
  });
  const appShellRef = useRef<HTMLDivElement>(null);
  const [dockState, setDockState] = useState<{
    openPanels: Record<DockPanelId, boolean>;
    stack: DockPanelId[];
  }>({
    openPanels: {
        workflowRun: false,
        attention: false,
        automations: false,
        garden: false,
    },
    stack: [],
  });
  const dockPanelCloseTimersRef = useRef<Partial<Record<DockPanelId, number>>>({});
  const gitStatusSubscribedDirRef = useRef<string | null>(null);

  useEffect(() => {
    if (activeSessionId) {
      setView('session');
    }
  }, [activeSessionId]);

  useEffect(() => {
    if (view === 'session' && !activeSessionId && sessions.length > 0) {
      setActiveSession(sessions[0].id);
    }
  }, [activeSessionId, sessions, setActiveSession, view]);

  useEffect(() => {
    if (view === 'session' && activeSessionId) {
      sendSessionSelected(activeSessionId);
    }
  }, [activeSessionId, sendSessionSelected, view]);

  useEffect(() => {
    const activeLocalSession = sessions.find((s) => s.id === activeSessionId);
    const nextDirectory =
      activeLocalSession?.cwd && view === 'session'
        ? daemonSessions.find((ds: { id: string; directory: string }) => ds.id === activeLocalSession.id)?.directory || null
        : null;
    const currentDirectory = gitStatusSubscribedDirRef.current;

    if (nextDirectory === currentDirectory) {
      if (!nextDirectory) {
        clearGitStatus();
      }
      return;
    }

    if (currentDirectory) {
      sendUnsubscribeGitStatus();
      gitStatusSubscribedDirRef.current = null;
    }

    if (!nextDirectory) {
      clearGitStatus();
      return;
    }

    sendSubscribeGitStatus(nextDirectory);
    gitStatusSubscribedDirRef.current = nextDirectory;
  }, [activeSessionId, sessions, daemonSessions, view, sendSubscribeGitStatus, sendUnsubscribeGitStatus, clearGitStatus]);

  useEffect(() => {
    return () => {
      if (gitStatusSubscribedDirRef.current) {
        sendUnsubscribeGitStatus();
        gitStatusSubscribedDirRef.current = null;
      }
      clearGitStatus();
    };
  }, [sendUnsubscribeGitStatus, clearGitStatus]);

  const [followNextTurn, setFollowNextTurn] = useState(false);
  const enterHome = useCallback((awaitingNextTurn: boolean) => {
    setActiveSession(null);
    setView('dashboard');
    setFollowNextTurn(awaitingNextTurn);
  }, [setActiveSession]);

  const goToDashboard = useCallback(() => enterHome(false), [enterHome]);

  const goHomeAwaitingNextTurn = useCallback(() => enterHome(true), [enterHome]);

  useEffect(() => {
    if (view !== 'dashboard') setFollowNextTurn(false);
  }, [view]);

  const toggleGridMode = useCallback(() => {
    setView((prev) => (prev === 'grid' ? (activeSessionId ? 'session' : 'dashboard') : 'grid'));
  }, [activeSessionId]);


  const clearDockPanelCloseTimer = useCallback((panelId: DockPanelId) => {
    const closeTimer = dockPanelCloseTimersRef.current[panelId];
    if (closeTimer) {
      window.clearTimeout(closeTimer);
      delete dockPanelCloseTimersRef.current[panelId];
    }
  }, []);

  const scheduleDockPanelStackRemoval = useCallback((panelId: DockPanelId) => {
    clearDockPanelCloseTimer(panelId);
    dockPanelCloseTimersRef.current[panelId] = window.setTimeout(() => {
      setDockState((prev) => {
        if (prev.openPanels[panelId]) {
          return prev;
        }
        return {
          openPanels: prev.openPanels,
          stack: prev.stack.filter((id) => id !== panelId),
        };
      });
      delete dockPanelCloseTimersRef.current[panelId];
    }, DOCK_PANEL_EXIT_MS);
  }, [clearDockPanelCloseTimer]);

  const toggleDockPanel = useCallback((panelId: DockPanelId) => {
    let nextOpen = false;
    clearDockPanelCloseTimer(panelId);
    setDockState((prev) => {
      nextOpen = !prev.openPanels[panelId];
      return {
        openPanels: {
          ...prev.openPanels,
          [panelId]: nextOpen,
        },
        stack: nextOpen
          ? [...prev.stack.filter((id) => id !== panelId), panelId]
          : prev.stack.includes(panelId)
            ? prev.stack
            : [...prev.stack, panelId],
      };
    });
    if (!nextOpen) {
      scheduleDockPanelStackRemoval(panelId);
    }
  }, [clearDockPanelCloseTimer, scheduleDockPanelStackRemoval]);

  const openDockPanel = useCallback((panelId: DockPanelId) => {
    clearDockPanelCloseTimer(panelId);
    setDockState((prev) => {
      if (prev.openPanels[panelId]) {
        return prev;
      }
      return {
        openPanels: {
          ...prev.openPanels,
          [panelId]: true,
        },
        stack: [...prev.stack.filter((id) => id !== panelId), panelId],
      };
    });
  }, [clearDockPanelCloseTimer]);

  const closeDockPanel = useCallback((panelId: DockPanelId) => {
    clearDockPanelCloseTimer(panelId);
    setDockState((prev) => {
      if (!prev.openPanels[panelId]) {
        return prev;
      }
      return {
        openPanels: {
          ...prev.openPanels,
          [panelId]: false,
        },
        stack: prev.stack.includes(panelId) ? prev.stack : [...prev.stack, panelId],
      };
    });
    scheduleDockPanelStackRemoval(panelId);
  }, [clearDockPanelCloseTimer, scheduleDockPanelStackRemoval]);

  useEffect(() => {
    return () => {
      Object.values(dockPanelCloseTimersRef.current).forEach((timerId) => {
        if (timerId) {
          window.clearTimeout(timerId);
        }
      });
      dockPanelCloseTimersRef.current = {};
    };
  }, []);

  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);

  const toggleSidebarCollapse = useCallback(() => {
    setSidebarCollapsed((prev) => !prev);
  }, []);


  const prevSessionCountRef = useRef(sessions.length);
  useEffect(() => {
    const prevCount = prevSessionCountRef.current;
    const currentCount = sessions.length;
    prevSessionCountRef.current = currentCount;

    if (currentCount === 0) {
      setSidebarCollapsed(true);
    } else if (prevCount === 0 && currentCount > 0) {
      setSidebarCollapsed(false);
    }
  }, [sessions.length]);

  const [locationPickerOpen, setLocationPickerOpen] = useState(false);
  const [locationPickerPurpose, setLocationPickerPurpose] = useState<LocationPickerPurpose>('workspace');
  const [locationPickerSessionDirection, setLocationPickerSessionDirection] = useState<TerminalSplitDirection>('vertical');

  const [zoomModeBySessionId, setZoomModeBySessionId] = useState<Record<string, boolean>>({});
  const { message: errorMessage, durationMs: errorDurationMs, showError, clearError } = useErrorToast();
  const inputDiagnosticsCopied = useSavedFlash();
  const handleCopyInputDiagnostics = useCallback(async () => {
    try {
      const dump = await readTerminalInputDiagnostics();
      if (!dump) {
        showError('No terminal input diagnostics yet. Try typing in a terminal, then copy again.');
        return;
      }
      await writeClipboardText(dump);
      inputDiagnosticsCopied.flash('copied');
    } catch (error) {
      showError(`Could not copy terminal input diagnostics: ${String(error)}`);
    }
  }, [inputDiagnosticsCopied.flash, showError]);
  const [chiefTransferTarget, setChiefTransferTarget] = useState<{
    sessionId: string;
    targetLabel: string;
    currentLabel: string;
  } | null>(null);
  const [chiefTransferSaving, setChiefTransferSaving] = useState(false);
  const [contextCapPromptSession, setContextCapPromptSession] = useState<{
    id: string;
    label: string;
    currentCap?: number;
  } | null>(null);

  const [appViewParamsPrompt, setAppViewParamsPrompt] = useState<{
    app: string;
    view: string;
    viewTitle: string;
    label: string;
    placeholder?: string;
  } | null>(null);

  const handleTerminalModelRecovered = useCallback(() => {
    showError(
      `Terminal issue recovered. We reloaded it for you. Diagnostics were saved to ${UI_DIAGNOSTICS_FILE_DISPLAY}; please send this file to Victor so he can troubleshoot it.`,
      { durationMs: 12_000 },
    );
  }, [showError]);

  const handleRebootstrapEndpoint = useCallback(async (endpointId: string) => {
    try {
      await sendBootstrapEndpoint(endpointId);
    } catch (err) {
      showError(err instanceof Error ? err.message : 'Sync failed.');
    }
  }, [sendBootstrapEndpoint, showError]);

  const applyChiefOfStaffChange = useCallback(async (sessionId: string, enabled: boolean) => {
    try {
      await sendSetChiefOfStaff(sessionId, enabled);
    } catch (err) {
      showError(err instanceof Error ? err.message : 'Chief of staff update failed.');
      throw err;
    }
  }, [sendSetChiefOfStaff, showError]);

  const handleChangeChiefOfStaff = useCallback((sessionId: string, enabled: boolean) => {
    const target = enrichedLocalSessions.find((session) => session.id === sessionId);
    if (!target) {
      showError('Session not found.');
      return;
    }
    if (!enabled) {
      void applyChiefOfStaffChange(sessionId, false).catch(() => {});
      return;
    }
    const current = enrichedLocalSessions.find((session) => session.chiefOfStaff);
    if (current && current.id !== sessionId) {
      setChiefTransferTarget({
        sessionId,
        targetLabel: target.label,
        currentLabel: current.label,
      });
      return;
    }
    void applyChiefOfStaffChange(sessionId, true).catch(() => {});
  }, [applyChiefOfStaffChange, enrichedLocalSessions, showError]);

  const handleConfirmChiefTransfer = useCallback(async () => {
    if (!chiefTransferTarget || chiefTransferSaving) return;
    setChiefTransferSaving(true);
    try {
      await applyChiefOfStaffChange(chiefTransferTarget.sessionId, true);
      setChiefTransferTarget(null);
    } catch {
    } finally {
      setChiefTransferSaving(false);
    }
  }, [applyChiefOfStaffChange, chiefTransferSaving, chiefTransferTarget]);

  const workflowRunsMap = useWorkflowRunsStore((s) => s.workflowRuns);
  const activeWorkflowRun = useMemo(
    () => selectLatestWorkflowRunForSession(workflowRunsMap, activeSessionId),
    [workflowRunsMap, activeSessionId],
  );
  const activeLocalSession = useMemo(
    () => (activeSessionId ? sessions.find((s) => s.id === activeSessionId) ?? null : null),
    [activeSessionId, sessions],
  );
  const activeDaemonSession = useMemo(() => {
    if (!activeSessionId) {
      return null;
    }
    return daemonSessions.find((session) => session.id === activeSessionId) || null;
  }, [activeSessionId, daemonSessions]);
  const activeRemoteSession = Boolean(activeDaemonSession?.endpoint_id);
  const activeEndpoint = useMemo(
    () => {
      const endpointId = activeDaemonSession?.endpoint_id;
      if (!endpointId) {
        return null;
      }
      return endpointById.get(endpointId) ?? null;
    },
    [activeDaemonSession?.endpoint_id, endpointById],
  );
  const openDockPanels = dockState.openPanels;
  const dockPanelStack = dockState.stack;
  const workflowRunPanelOpen = openDockPanels.workflowRun;
  const attentionPanelOpen = openDockPanels.attention;
  const automationsPanelOpen = openDockPanels.automations;
  const gardenPanelOpen = openDockPanels.garden;
  const openGardenDock = useCallback(() => openDockPanel('garden'), [openDockPanel]);
  const closeGardenDock = useCallback(() => closeDockPanel('garden'), [closeDockPanel]);
  const {
    mode: gardenMode,
    holdsWindow: gardenHoldsWindow,
    toggleFrame: toggleGardenFrame,
    toggleFromIcon: toggleGardenFromIcon,
    close: closeGarden,
  } = useGardenPresentation({
    dockOpen: gardenPanelOpen,
    openDock: openGardenDock,
    closeDock: closeGardenDock,
  });
  const [gardenSlotRef, gardenDockRect] = useDockSlotRect();
  const blockingOverlayOpen = locationPickerOpen
    || whatsNew.isOpen
    || settingsOpen
    || shortcutsOpen
    || shortcutEditorOpen
    || actionMenuOpen
    || notebookOpen
    || gardenHoldsWindow
    || chiefTransferTarget !== null
    || contextCapPromptSession !== null
    || appViewParamsPrompt !== null
    || pendingSessionClose !== null
    || sessionCreationJob !== null
    || openPRLauncherJob !== null;

  // Views with nothing focusable (dashboard, empty workspaces) can leave the WebView off first responder, killing EVERY shortcut until the user clicks the window.
  useEffect(() => {
    const claimShellFocus = () => {
      if (activeSessionId) return;
      if (blockingOverlayOpen) return;
      const shell = appShellRef.current;
      if (!shell) return;
      const active = document.activeElement;
      if (active && active !== document.body) return;
      shell.focus({ preventScroll: true });
    };
    claimShellFocus();
    window.addEventListener('focus', claimShellFocus);
    return () => window.removeEventListener('focus', claimShellFocus);
  }, [activeSessionId, blockingOverlayOpen, view]);

  const openNotebookBrowser = useCallback(() => {
    setNotebookOpen(true);
  }, []);

  const liveGardenSessions = useMemo(
    () => new Set(daemonSessions.map((session) => session.id)),
    [daemonSessions],
  );
  const gardenSessionLabels = useMemo(
    () => new Map(daemonSessions.map((session) => [session.id, session.label])),
    [daemonSessions],
  );

  const toggleNotificationsPanel = useCallback(() => {
    setNotificationsPanelOpen((open) => !open);
  }, []);
  const openNotificationsPanel = useCallback(() => {
    setNotificationsPanelOpen(true);
  }, []);
  const closeNotificationsPanel = useCallback(() => {
    setNotificationsPanelOpen(false);
  }, []);

  const activeWorkspaceIdRef = useRef<string | null>(null);

  const daemonWorkspacesRef = useRef<DaemonWorkspace[]>([]);

  // A unique tile id every time: the daemon treats a duplicate id as a move.
  const [markdownOpenerOpen, setMarkdownOpenerOpen] = useState(false);
  const handleOpenMarkdownFile = useCallback(() => {
    if (claimPaletteFocus()) return;
    setMarkdownOpenerOpen(true);
  }, []);

  const handleToggleQueueMode = useCallback(() => {
    sendSetSetting(QUEUE_MODE_SETTING, isQueueModeEnabled(settings) ? 'false' : 'true');
  }, [sendSetSetting, settings]);
  const handleToggleCrewQueue = useCallback(() => {
    sendSetSetting(QUEUE_CREW_SETTING, isCrewQueueEnabled(settings) ? 'false' : 'true');
  }, [sendSetSetting, settings]);
  const markdownOpenerTarget = useMemo(
    () => resolveMarkdownOpenerTarget(
      sessions.find((session) => session.id === activeSessionId),
      settings['notebook.root.effective'],
    ),
    [sessions, activeSessionId, settings],
  );
  const loadOpenerRecents = useCallback(
    () => sendRecentFiles(50, markdownOpenerTarget.root || undefined)
      .then((files) => files.map((file) => ({ path: file.path, lastAt: file.lastAt }))),
    [sendRecentFiles, markdownOpenerTarget.root],
  );
  const loadOpenerIndex = useCallback(
    (root: string) => sendFsIndex(root, OPENER_EXTENSIONS),
    [sendFsIndex],
  );

  const handleOpenNotebookTile = useCallback(() => {
    const workspaceId = activeWorkspaceIdRef.current;
    if (!workspaceId) return;
    const tileId = `notebook-tile-${crypto.randomUUID()}`;
    // A twin across endpoints forfeits the workspace-dir default rather than adopt a remote directory: the active id carries no endpoint identity.
    const workspace = soleWorkspaceForId(daemonWorkspacesRef.current, workspaceId);
    const localDirectory = localWorkspaceDirectory(workspace);
    const effectiveNotebookRoot = settings['notebook.root.effective'] || '';
    const root = resolveEditorTileRoot(localDirectory, effectiveNotebookRoot);
    void sendWorkspaceDockTile(workspaceId, tileId, 'notebook', {
      edge: 'right',
      ratio: 0.4,
      tileParams: root ? serializeNotebookTileParams({ root }) : undefined,
    })
      .catch((error) => {
        console.warn('[App] Failed to dock notebook tile:', error);
      });
  }, [sendWorkspaceDockTile, settings]);

  // A fresh tile id every time: the daemon reads a duplicate id as a move.
  const dockAppViewTile = useCallback((app: string, view: string, params: string) => {
    const workspaceId = activeWorkspaceIdRef.current;
    if (!workspaceId) return;
    void sendWorkspaceDockTile(
      workspaceId,
      `app-view-tile-${crypto.randomUUID()}`,
      appViewTileKind(app, view),
      { edge: 'right', ratio: 0.4, ...(params ? { tileParams: params } : {}) },
    ).catch((error) => {
      console.warn('[App] Failed to dock app view tile:', error);
    });
  }, [sendWorkspaceDockTile]);

  const appViewMenuItems = useMemo<ActionMenuItem[]>(() => {
    const items: ActionMenuItem[] = [];
    for (const app of apps ?? []) {
      if (!app.enabled) continue;
      for (const view of app.views ?? []) {
        items.push({
          id: `app-view-${app.name}-${view.name}`,
          title: `${view.title} — ${app.name}`,
          description: app.description || `Dock ${app.name}'s ${view.name} view`,
          keywords: ['app', 'view', 'dock', 'tile', app.name, view.name],
          icon: <ContextActionIcon />,
          run: () => {
            if (view.params_label) {
              setAppViewParamsPrompt({
                app: app.name,
                view: view.name,
                viewTitle: `${app.name}/${view.name}`,
                label: view.params_label,
                ...(view.params_placeholder ? { placeholder: view.params_placeholder } : {}),
              });
              return;
            }
            dockAppViewTile(app.name, view.name, '');
          },
        });
      }
    }
    return items;
  }, [apps, dockAppViewTile]);

  const actionMenuItems = useMemo<ActionMenuItem[]>(() => [
    {
      id: 'open-markdown-file',
      title: 'Open a markdown file',
      description: 'Recently opened documents, then a fuzzy search of this session\u2019s folder',
      keywords: ['open', 'file', 'markdown', 'md', 'recent', 'doc', 'find'],
      icon: <ContextActionIcon />,
      shortcut: [shortcutTokens('file.open')],
      run: () => setMarkdownOpenerOpen(true),
    },
    {
      id: 'notebook-tile',
      title: 'Open Editor tile',
      description: 'Dock an editor beside your terminals, opened on any folder',
      keywords: ['notebook', 'editor', 'tile', 'knowledge', 'journal', 'dock', 'split'],
      icon: <ContextActionIcon />,
      shortcut: [shortcutTokens('notebook.openTile')],
      run: handleOpenNotebookTile,
    },
    {
      id: 'attention',
      title: 'Open attention drawer',
      description: 'Show sessions and pull requests that need a response',
      keywords: ['waiting', 'pull requests', 'prs', 'notifications'],
      icon: <AttentionActionIcon />,
      shortcut: [shortcutTokens('dock.attention')],
      run: () => openDockPanel('attention'),
    },
    {
      id: 'garden-frame',
      title: gardenMode === 'full'
        ? 'Move the garden to the sidebar'
        : gardenMode === 'dock'
          ? 'Open the garden fullscreen'
          : 'Open the garden',
      description: 'Seeds and plots, the trail beside what you are reading',
      keywords: ['garden', 'seed', 'seeds', 'plot', 'board', 'expand', 'fullscreen'],
      icon: <BoardActionIcon />,
      shortcut: [shortcutTokens('board.open')],
      run: () => toggleGardenFrame(),
    },
    {
      id: 'toggle-queue-mode',
      title: isQueueModeEnabled(settings) ? 'Turn off the agent queue' : 'Turn on the agent queue',
      description: 'Show the turns you owe above the workspace tree',
      keywords: ['queue', 'turn', 'settle', 'attention', 'sidebar'],
      icon: <AttentionActionIcon />,
      run: handleToggleQueueMode,
    },
    {
      id: 'toggle-auto-settle',
      title: isAutoSettleEnabled(settings) ? 'Turn off auto-settle' : 'Turn on auto-settle',
      description: 'Settle a turn once you have steered the agent and it goes back to work',
      keywords: ['auto', 'settle', 'turn', 'countdown', 'queue', 'attention'],
      icon: <AttentionActionIcon />,
      run: () => sendSetSetting(AUTO_SETTLE_ENABLED_SETTING, isAutoSettleEnabled(settings) ? 'false' : 'true'),
    },
    {
      id: 'customize-shortcuts',
      title: 'Customize keyboard shortcuts',
      description: 'Rebind shortcuts and restore defaults',
      keywords: ['keybindings', 'shortcuts', 'keyboard', 'rebind', 'hotkeys'],
      icon: <KeyboardActionIcon />,
      run: () => setShortcutEditorOpen(true),
    },
    {
      id: 'copy-terminal-input-diagnostics',
      title: 'Copy terminal input diagnostics',
      description: 'Copy a troubleshooting dump to share when a terminal stops accepting input',
      keywords: ['debug', 'logs', 'dump', 'keyboard', 'typing', 'stuck', 'frozen'],
      icon: <KeyboardActionIcon />,
      run: () => { void handleCopyInputDiagnostics(); },
    },
  ], [openDockPanel, handleOpenNotebookTile, toggleGardenFrame, gardenMode, settings, handleToggleQueueMode, sendSetSetting, handleCopyInputDiagnostics]);

  const handleToggleActionMenu = useCallback(() => {
    if (actionMenuOpen) {
      setActionMenuOpen(false);
      return;
    }
    if (settingsOpen || shortcutsOpen || locationPickerOpen || whatsNew.isOpen
      || notebookOpen || gardenHoldsWindow
      || chiefTransferTarget !== null || contextCapPromptSession !== null
      || appViewParamsPrompt !== null || pendingSessionClose !== null
      || sessionCreationJob !== null || openPRLauncherJob !== null) {
      return;
    }
    setActionMenuOpen(true);
  }, [
    actionMenuOpen,
    chiefTransferTarget,
    contextCapPromptSession,
    appViewParamsPrompt,
    locationPickerOpen,
    openPRLauncherJob,
    pendingSessionClose,
    sessionCreationJob,
    settingsOpen,
    shortcutsOpen,
    whatsNew.isOpen,
    notebookOpen,
    gardenHoldsWindow,
  ]);
  useEffect(() => {
    if (!settingError) {
      return;
    }
    showError(settingError);
    clearSettingError();
  }, [clearSettingError, settingError, showError]);

  useEffect(() => {
    if (!disconnectExplanation) {
      return;
    }
    showError(disconnectExplanation, { durationMs: 8000 });
    clearDisconnectExplanation();
  }, [clearDisconnectExplanation, disconnectExplanation, showError]);

  useEffect(() => {
    if (!activeSessionId) {
      return;
    }
    listWorkflowRuns(activeSessionId).catch((error) => {
      console.error('[App] Failed to list workflow runs:', error);
    });
  }, [activeSessionId, listWorkflowRuns]);

  // listWorkflowRuns omits agent_calls and a completed run sees no further broadcasts, so without this fetch it stays call-less forever after a reload.
  const workflowRunIdToHydrate = workflowRunIdNeedingHydration(
    workflowRunPanelOpen,
    activeWorkflowRun,
  );
  useEffect(() => {
    if (!workflowRunIdToHydrate) {
      return;
    }
    getWorkflowRun(workflowRunIdToHydrate).catch((error) => {
      console.error('[App] Failed to hydrate workflow run:', error);
    });
  }, [workflowRunIdToHydrate, getWorkflowRun]);

  const [utilityFocusRequestToken, setUtilityFocusRequestToken] = useState(0);


  const handleNewWorkspace = useCallback(() => {
    setLocationPickerPurpose('workspace');
    setLocationPickerOpen(true);
  }, []);

  const nextSplitSessionLabel = useCallback((workspaceId: string, agent: SessionAgent) => {
    const normalizedAgent = normalizeSessionAgent(agent, 'codex');
    const base = normalizedAgent === 'shell' ? 'shell' : normalizedAgent;
    const matchingCount = sessions.filter((session) => (
      session.workspaceId === workspaceId
      && normalizeSessionAgent(session.agent, 'codex') === normalizedAgent
    )).length;
    return matchingCount === 0 ? base : `${base} ${matchingCount + 1}`;
  }, [sessions]);

  const createSplitSession = useCallback(async (
    agent: SessionAgent,
    direction: 'vertical' | 'horizontal',
    targetPaneId?: string,
    options: SplitSessionOptions = {},
  ) => {
    const activeSession = options.baseSessionId
      ? sessions.find((session) => session.id === options.baseSessionId)
      : activeLocalSession;
    if (!activeSession?.workspaceId) {
      handleNewWorkspace();
      return;
    }
    const sessionId = crypto.randomUUID();
    const workspaceId = activeSession.workspaceId;
    const paneId = targetPaneId || getActivePaneIdForSession(activeSession);
    const newPaneId = paneIdForSession(sessionId);
    const label = options.label || nextSplitSessionLabel(workspaceId, agent);
    const endpointId = options.endpointId === null
      ? undefined
      : options.endpointId ?? activeSession.endpointId;
    let paneAdded = false;

    try {
      await createSession(
        label,
        options.cwd || activeSession.cwd,
        sessionId,
        agent,
        endpointId,
        agent === 'shell' ? false : options.yoloMode ?? activeSession.yoloMode,
        workspaceId,
        undefined,
        undefined,
        agent === 'shell' ? undefined : options.autoMode,
      );
      const spawnArgs = takeSessionSpawnArgs(sessionId, 80, 24);
      await sendWorkspaceAddSessionPane(workspaceId, sessionId, label, { paneId: newPaneId, targetPaneId: paneId, direction });
      paneAdded = true;
      if (spawnArgs) {
        await ptySpawn({ args: { ...spawnArgs, spawned_from: activeSession.id } });
      } else {
        throw new Error('Session spawn arguments were not prepared.');
      }
      setView('session');
      setActiveSession(sessionId);
      setUtilityFocusRequestToken((token) => token + 1);
    } catch (error) {
      await rollbackSessionCreation({
        sessionId,
        workspaceId,
        paneId: paneAdded ? newPaneId : undefined,
      });
      showError(error instanceof Error ? error.message : 'Failed to create session split');
    }
  }, [
    activeLocalSession,
    createSession,
    getActivePaneIdForSession,
    handleNewWorkspace,
    nextSplitSessionLabel,
    rollbackSessionCreation,
    sendWorkspaceAddSessionPane,
    sessions,
    setActiveSession,
    showError,
    takeSessionSpawnArgs,
  ]);

  const handleNewSession = useCallback((direction: TerminalSplitDirection = 'vertical') => {
    if (!activeLocalSession?.workspaceId) {
      handleNewWorkspace();
      return;
    }
    setLocationPickerPurpose('session');
    setLocationPickerSessionDirection(direction);
    setLocationPickerOpen(true);
  }, [activeLocalSession?.workspaceId, handleNewWorkspace]);

  const handleLocationSelect = useCallback(
    async (
      path: string,
      agent: SessionAgent,
      endpointId?: string,
      yoloMode = false,
      chiefOfStaff = false,
      resumeConversationFile?: string,
      autoMode?: boolean,
    ) => {
      const jobId = sessionCreationJobIdRef.current + 1;
      sessionCreationJobIdRef.current = jobId;
      let selectedAgent: SessionAgent;
      if (endpointId) {
        const endpoint = daemonEndpoints.find((entry) => entry.id === endpointId);
        if (!endpoint) {
          showError('Selected endpoint no longer exists.');
          return;
        }
        if (endpoint.status !== 'connected') {
          showError(`Endpoint ${endpoint.name} is ${endpoint.status}.`);
          return;
        }
        if (agent !== TERMINAL_AGENT && !endpoint.capabilities?.agents_available.includes(agent)) {
          showError(`${agentLabel(agent)} is not available on ${endpoint.name}.`);
          return;
        }
        selectedAgent = agent;
      } else {
        if (agent !== TERMINAL_AGENT && !hasAvailableAgents) {
          showError('No supported agent CLI found in PATH.');
          return;
        }
        selectedAgent = agent === TERMINAL_AGENT
          ? TERMINAL_AGENT
          : resolvePreferredAgent(agent, agentAvailability, 'codex');
      }
      const folderName = path.split('/').pop() || 'session';
      if (locationPickerPurpose === 'session' && activeLocalSession?.workspaceId) {
        await createSplitSession(selectedAgent, locationPickerSessionDirection, undefined, {
          cwd: path,
          endpointId: endpointId ?? null,
          label: folderName,
          yoloMode,
          autoMode,
        });
        return;
      }
      setSessionCreationJob({
        id: jobId,
        label: folderName,
        path,
        phase: 'starting_session',
        error: null,
      });
      try {
        const sessionId = await createWorkspaceSession(
          folderName,
          path,
          undefined,
          selectedAgent,
          endpointId,
          yoloMode,
          { chiefOfStaff, resumeConversationFile, autoMode },
        );
        setSessionCreationJob((current) => (
          current?.id === jobId
            ? { ...current, sessionId, phase: 'starting_session' }
            : current
        ));
      } catch (err) {
        setSessionCreationJob((current) => (
          current?.id === jobId
            ? { ...current, error: err instanceof Error ? err.message : 'Failed to create session' }
            : current
        ));
      }
    },
    [activeLocalSession?.workspaceId, agentAvailability, createSplitSession, createWorkspaceSession, daemonEndpoints, hasAvailableAgents, locationPickerPurpose, locationPickerSessionDirection, showError]
  );

  const handleCreateWorktreeSession = useCallback((
    mainRepo: string,
    branchName: string,
    startingFrom: string,
    endpointId: string | undefined,
    agent: SessionAgent,
    yoloMode: boolean,
    autoMode?: boolean,
  ) => {
    const endpointKey = endpointId || 'local';
    if (worktreeSessionCreateEndpointsRef.current.has(endpointKey)) {
      showError('A worktree session is already being created for this target.');
      return;
    }
    worktreeSessionCreateEndpointsRef.current.add(endpointKey);
    const jobId = sessionCreationJobIdRef.current + 1;
    sessionCreationJobIdRef.current = jobId;
    setSessionCreationJob({
      id: jobId,
      label: branchName,
      path: mainRepo,
      phase: 'creating_worktree',
      error: null,
    });

    void (async () => {
      try {
        const result = await sendCreateWorktree(mainRepo, branchName, undefined, startingFrom, endpointId);
        if (!result.success || !result.path) {
          throw new Error(result.error || 'Failed to create worktree');
        }
        const worktreePath = result.path;
        setSessionCreationJob((current) => (
          current?.id === jobId
            ? { ...current, path: worktreePath, phase: 'starting_session' }
            : current
        ));
        const folderName = worktreePath.split('/').pop() || branchName || 'session';
        if (locationPickerPurpose === 'session' && activeLocalSession?.workspaceId) {
          await createSplitSession(agent, locationPickerSessionDirection, undefined, {
            cwd: worktreePath,
            endpointId: endpointId ?? null,
            label: folderName,
            yoloMode,
            autoMode,
          });
          setSessionCreationJob((current) => (
            current?.id === jobId ? null : current
          ));
          return;
        }
        const sessionId = await createWorkspaceSession(folderName, worktreePath, undefined, agent, endpointId, yoloMode, { autoMode });
        setSessionCreationJob((current) => (
          current?.id === jobId
            ? { ...current, label: folderName, path: worktreePath, phase: 'starting_session', sessionId }
            : current
        ));
      } catch (err) {
        setSessionCreationJob((current) => (
          current?.id === jobId
            ? { ...current, error: err instanceof Error ? err.message : 'Failed to create session' }
            : current
        ));
      } finally {
        worktreeSessionCreateEndpointsRef.current.delete(endpointKey);
      }
    })();
  }, [activeLocalSession?.workspaceId, createSplitSession, createWorkspaceSession, locationPickerPurpose, locationPickerSessionDirection, sendCreateWorktree, showError]);

  const closeLocationPicker = useCallback(() => {
    setLocationPickerOpen(false);
  }, []);

  const hasChiefOfStaff = useMemo(
    () => daemonSessions.some((ds) => ds.chief_of_staff === true),
    [daemonSessions]
  );

  const handleCloseSession = useCallback(
    async (id: string) => {
      const closeProtection = sessionCloseProtectionHint(daemonSessions, id);
      if (closeProtection) {
        showError(closeProtection);
        return;
      }
      const session = enrichedLocalSessions.find(s => s.id === id);

      const localDaemonSession = daemonSessions.find(ds => ds.id === session?.id);
      if (localDaemonSession && session) {
        await sendUnregisterSession(session.id);
      } else {
        closeSession(id);
      }

      if (session) {
        removeWorkspaceRef(session.workspaceId);
      }
    },
    [closeSession, daemonSessions, enrichedLocalSessions, removeWorkspaceRef, sendUnregisterSession, showError]
  );

  const handleClosePane = useCallback((sessionId: string, paneId: string) => {
    const closeProtection = sessionCloseProtectionHint(daemonSessions, sessionId);
    if (closeProtection) {
      showError(closeProtection);
      return Promise.resolve();
    }
    const session = enrichedLocalSessions.find((entry) => entry.id === sessionId);
    const fallbackPaneId = prepareClosePaneFocus(sessionId, paneId);
    const fallbackSessionId = session?.workspace.agents.find((pane) => (
      pane.id === fallbackPaneId && pane.id !== paneId
    ))?.sessionId;
    const workspaceId = sessions.find((session) => session.id === sessionId)?.workspaceId;
    if (!workspaceId) {
      return Promise.reject(new Error(`Cannot close pane ${paneId}: session ${sessionId} has no workspace`));
    }
    return sendWorkspaceClosePane(workspaceId, paneId)
      .then((result) => {
        if (fallbackSessionId) {
          setActiveSession(fallbackSessionId);
        }
        return result;
      })
      .catch((error) => {
        clearPreparedClosePaneFocus(sessionId);
        throw error;
      });
  }, [clearPreparedClosePaneFocus, daemonSessions, enrichedLocalSessions, prepareClosePaneFocus, sendWorkspaceClosePane, sessions, setActiveSession, showError]);

  const handleRequestCloseSession = useCallback((id: string) => {
    const session = sessions.find((entry) => entry.id === id);
    if (!session) {
      return;
    }

    const sessionPane = session.workspace.agents.find((pane) => pane.sessionId === session.id);
    if (sessionPane) {
      void handleClosePane(session.id, sessionPane.id).catch(console.error);
      return;
    }

    void handleCloseSession(id);
  }, [handleClosePane, handleCloseSession, sessions]);

  const handleSessionProcessExit = useCallback((info: SessionExitInfo) => {
    if (info.exitCode !== 0 || info.signal) {
      return;
    }
    // A reload's kill can surface as a clean exit (code 0, no signal); the same id is about to respawn in place, so closing the pane here would tear the workspace down under the pending spawn.
    if (isSessionReloading(info.id)) {
      return;
    }
    handleRequestCloseSession(info.id);
  }, [handleRequestCloseSession]);

  useEffect(() => {
    registerSessionExitHandler(handleSessionProcessExit);
    return () => registerSessionExitHandler(null);
  }, [registerSessionExitHandler, handleSessionProcessExit]);

  const handleCancelSessionClose = useCallback(() => {
    setPendingSessionClose(null);
  }, []);

  const handleConfirmSessionClose = useCallback(() => {
    if (!pendingSessionClose) {
      return;
    }
    const sessionID = pendingSessionClose.id;
    setPendingSessionClose(null);
    void handleCloseSession(sessionID);
  }, [handleCloseSession, pendingSessionClose]);

  const handleSelectSession = useCallback(
    (id: string) => {
      setSelectedTile(null);
      setSelectedSessionlessWorkspaceId(null);
      const session = sessions.find((entry) => entry.id === id);
      const sessionPane = session?.workspace.agents.find((pane) => pane.sessionId === id);
      if (sessionPane) {
        setActivePane(id, sessionPane.id);
      }
      setActiveSession(id);
      setUtilityFocusRequestToken((token) => token + 1);
    },
    [sessions, setActivePane, setActiveSession]
  );

  useUiAutomationBridge({
    sessions,
    activeSessionId,
    daemonReady: hasReceivedInitialState && !connectionError,
    connectionError,
    getActivePaneIdForSession,
    createSession: createWorkspaceSession,
    selectSession: handleSelectSession,
    selectWorkspace: (workspaceId: string) => selectWorkspaceRef.current(workspaceId),
    moveWorkspaceLeafToWorkspace: sendWorkspaceMoveLeafToWorkspace,
    closeSession: handleCloseSession,
    reloadSession,
    setSetting: sendSetSetting,
    openDockPanel: (panelId: string) => openDockPanel(panelId as DockPanelId),
    openShortcutEditor: () => setShortcutEditorOpen(true),
    splitPane: (sessionId, paneId, direction) => {
      return createSplitSession('shell', direction, paneId, { baseSessionId: sessionId });
    },
    closePane: handleClosePane,
    focusPane: (sessionId: string, paneId: string) => {
      setActiveSession(sessionId);
      setUtilityFocusRequestToken((token) => token + 1);
      setActivePane(sessionId, paneId);
      focusSessionPane(sessionId, paneId, 40);
    },
    typeInSessionPaneViaUI,
    isSessionPaneInputFocused,
    scrollSessionPaneToTop,
    getPaneText,
    getPaneSize,
    getPaneVisibleContent,
    getPaneVisibleStyleSummary,
    getPaneBlockState,
    getPanePlacementState,
    fitSessionActivePane,
    sendRuntimeInput,
    isRuntimeAttached,
    openAutomationsPanel: () => openDockPanel('automations'),
    presentationNotices,
    resetSessionPaneTerminal,
    injectSessionPaneBytes,
    injectSessionPaneBase64,
    drainSessionPaneTerminal,
  });

  const openPR = useOpenPR({
    settings,
    sendFetchPRDetails,
    sendEnsureRepo,
    sendCreateWorktreeFromBranch,
    createSession: createWorkspaceSession,
  });

  const handleOpenPR = useCallback(
    async (pr: DaemonPR) => {
      console.log(`[App] Open PR requested: ${pr.repo}#${pr.number} - ${pr.title}`);

      if (!hasAvailableAgents) {
        alert('No supported agent CLI found in PATH.');
        return;
      }
      const configuredDefaultAgent = normalizeSessionAgent(settings.new_session_agent, 'claude');
      const defaultAgent = resolvePreferredAgent(configuredDefaultAgent, agentAvailability, 'codex');
      const launcherId = openPRLauncherIdRef.current + 1;
      openPRLauncherIdRef.current = launcherId;
      const isActiveLauncher = () => openPRLauncherIdRef.current === launcherId;
      const updateLauncherProgress = (progress: OpenPRProgress) => {
        setOpenPRLauncherJob((current) => current?.id === launcherId ? { ...current, progress } : current);
      };

      setOpenPRLauncherJob({
        id: launcherId,
        pr,
        progress: { step: pr.head_branch ? 'ensuring_repo' : 'fetching_pr_details' },
      });
      const result = await openPR(pr, defaultAgent, { onProgress: updateLauncherProgress }).finally(() => {
        if (isActiveLauncher()) {
          setOpenPRLauncherJob(null);
        }
      });
      if (!isActiveLauncher()) {
        return;
      }
      if (result.success) {
        console.log(`[App] Worktree created at ${result.worktreePath}`);
        return;
      }

      const errorMsg = result.error.message || '';
      switch (result.error.kind) {
        case 'missing_projects_directory':
          alert('Please configure your Projects Directory in Settings first.\n\nThis tells the app where to find your local git repositories.');
          break;
        case 'missing_head_branch':
          alert(`PR branch information not available.\n\nTry refreshing PRs (${formatShortcut('session.refreshPRs')}) to fetch branch details.`);
          break;
        case 'fetch_pr_details_failed':
          alert(`Failed to fetch PR details.\n\n${errorMsg || `Try refreshing PRs (${formatShortcut('session.refreshPRs')}) and try again.`}`);
          break;
        case 'ensure_repo_failed':
        case 'create_worktree_failed':
        case 'create_session_failed':
        case 'unknown': {
          if (errorMsg.includes('clone failed')) {
            alert(`Failed to clone repository ${pr.repo}.\n\nError: ${errorMsg}\n\nCheck your network connection and GitHub access.`);
          } else if (errorMsg.includes('already exists')) {
            alert(`A worktree for this branch may already exist.\n\nError: ${errorMsg}`);
          } else {
            alert(`Failed to open PR: ${errorMsg || 'Unknown error'}`);
          }
          break;
        }
      }
    },
    [agentAvailability, hasAvailableAgents, openPR, settings.new_session_agent]
  );

  const workspaceViews = useMemo(
    () => buildWorkspaceViewModels(daemonWorkspaces, visibleEnrichedSessions),
    [daemonWorkspaces, visibleEnrichedSessions],
  );
  const unmutedWorkspaceViews = useMemo(
    () => workspaceViews.filter((workspace) => !workspace.muted && (workspace.pinned || workspace.sessions.length > 0)),
    [workspaceViews],
  );
  const mutedWorkspaceViews = useMemo(
    () => workspaceViews.filter((workspace) => workspace.muted && (workspace.pinned || workspace.sessions.length > 0)),
    [workspaceViews],
  );
  const unmutedEnrichedSessions = useMemo(
    () => unmutedWorkspaceViews.flatMap((workspace) => workspace.sessions),
    [unmutedWorkspaceViews],
  );

  const queueModeEnabled = isQueueModeEnabled(settings);
  const crewQueueEnabled = isCrewQueueEnabled(settings);
  const queueBands = useMemo(
    () => (queueModeEnabled
      ? buildQueueBands(unmutedWorkspaceViews, { crewInQueue: crewQueueEnabled })
      : null),
    [queueModeEnabled, crewQueueEnabled, unmutedWorkspaceViews],
  );

  const activeWorkspaceForCommands = useMemo(
    () => workspaceViews.find((workspace) => workspace.sessions.some((session) => session.id === activeSessionId)) ?? null,
    [workspaceViews, activeSessionId],
  );
  const activeSessionForCommands = useMemo(
    () => activeWorkspaceForCommands?.sessions.find((session) => session.id === activeSessionId) ?? null,
    [activeWorkspaceForCommands, activeSessionId],
  );
  const activeSessionQueueEligible = Boolean(
    activeSessionForCommands && sessionParticipatesInQueue(activeSessionForCommands, crewQueueEnabled),
  );

  const actionMenuItemsWithWorkspaceActions = useMemo<ActionMenuItem[]>(() => {
    const workspace = activeWorkspaceForCommands;
    if (!workspace) return [...actionMenuItems, ...appViewMenuItems];
    const activeSession = activeSessionForCommands;
    const sessionPinItems: ActionMenuItem[] = activeSession && activeSessionQueueEligible && !activeSession.chiefOfStaff
      ? [{
        id: 'pin-active-session',
        title: activeSession.pinnedAt ? `Unpin ${activeSession.label}` : `Pin ${activeSession.label}`,
        description: activeSession.pinnedAt
          ? 'Put this agent back in the queue'
          : 'Take this agent out of the queue and keep it in view',
        keywords: ['pin', 'unpin', 'agent', 'session', 'queue'],
        icon: <AttentionActionIcon />,
        run: () => sendPinSession(activeSession.id, !activeSession.pinnedAt),
      }]
      : [];
    const sessionSeedItems: ActionMenuItem[] = activeSession
      && (activeSession.seedId || seeds.some((seed) => seed.tender_session === activeSession.id))
      ? [{
        id: 'show-tended-seeds',
        title: `Show ${activeSession.label}'s seeds`,
        description: 'The seeds this agent is tending, and what it reports to',
        keywords: ['seed', 'seeds', 'tend', 'tending', 'garden', 'plot', 'agent', 'session'],
        icon: <GardenIcon />,
        run: () => setSeedPopoverRequest((prev) => ({ sessionId: activeSession.id, nonce: (prev?.nonce ?? 0) + 1 })),
      }]
      : [];
    const sessionUsageItems: ActionMenuItem[] = activeSession?.usage
      && !activeSession.usage.measurement_incomplete
      && activeSession.usage.total_tokens > 0
      ? [{
        id: 'show-session-usage',
        title: `Show ${activeSession.label}'s usage`,
        description: 'Token and cost totals for each model in this session',
        keywords: ['usage', 'tokens', 'cost', 'models', 'agent', 'session'],
        icon: <ContextActionIcon />,
        run: () => setUsagePopoverRequest((prev) => ({ sessionId: activeSession.id, nonce: (prev?.nonce ?? 0) + 1 })),
      }]
      : [];
    const sessionCapItems: ActionMenuItem[] = activeSession && ['claude', 'codex'].includes((activeSession.agent ?? '').toLowerCase())
      ? [{
        id: 'set-session-context-cap',
        title: activeSession.contextWindowCap
          ? `Change ${activeSession.label}'s context window cap`
          : `Cap ${activeSession.label}'s context window`,
        description: activeSession.contextWindowCap
          ? `Compacts at ${activeSession.contextWindowCap.toLocaleString()} tokens — change or clear the cap`
          : 'Make this agent compact at a token budget you choose',
        keywords: ['context', 'window', 'cap', 'compact', 'autocompact', 'tokens', 'agent', 'session'],
        icon: <ContextActionIcon />,
        run: () => setContextCapPromptSession({
          id: activeSession.id,
          label: activeSession.label,
          currentCap: activeSession.contextWindowCap,
        }),
      }]
      : [];
    return [
      ...actionMenuItems,
      ...appViewMenuItems,
      ...sessionPinItems,
      ...sessionSeedItems,
      ...sessionUsageItems,
      ...sessionCapItems,
      {
        id: 'pin-active-workspace',
        title: workspace.pinned ? `Unpin ${workspace.title}` : `Pin ${workspace.title}`,
        description: workspace.pinned
          ? 'Put this workspace back in the queue'
          : 'Take this workspace out of the queue and keep it in view',
        keywords: ['pin', 'unpin', 'workspace', 'queue'],
        icon: <AttentionActionIcon />,
        run: () => sendPinWorkspace(workspace.id, !workspace.pinned),
      },
      {
        id: 'mute-active-workspace',
        title: workspace.muted ? `Unmute ${workspace.title}` : `Mute ${workspace.title}`,
        description: workspace.muted
          ? 'Let this workspace ask for you again'
          : 'Nothing from this workspace reaches you',
        keywords: ['mute', 'unmute', 'workspace', 'silence'],
        icon: <AttentionActionIcon />,
        run: () => sendMuteWorkspace(workspace.id, workspace.endpointId),
      },
    ];
  }, [actionMenuItems, appViewMenuItems, activeWorkspaceForCommands, activeSessionForCommands, activeSessionQueueEligible, seeds, sendPinWorkspace, sendPinSession, sendMuteWorkspace]);


  const wantsAttention = useCallback(
    (session: {
      state: UISessionState;
      turnOwed?: boolean;
      crewMember?: string;
      automation?: { definition_id: string };
    }) => (
      queueModeEnabled
        ? sessionParticipatesInQueue(session, crewQueueEnabled) && Boolean(session.turnOwed)
        : isAttentionSessionState(session.state)
    ),
    [queueModeEnabled, crewQueueEnabled],
  );

  const gridSessionTiles = useMemo<GridSessionTile[]>(() => {
    const result: GridSessionTile[] = [];
    for (const s of unmutedEnrichedSessions) {
      const pane = s.workspace.agents.find((agent) => agent.sessionId === s.id);
      if (!pane) continue;
      const state = s.state;
      result.push({
        runtimeId: pane.runtimeId,
        sessionId: s.id,
        title: pane.title,
        state,
        attention: wantsAttention(s),
      });
    }
    return result;
  }, [unmutedEnrichedSessions, wantsAttention]);

  const [gridLayout, setGridLayout] = useState<GridLayout>(readGridLayout);
  const handleSelectGridLayout = useCallback((layout: GridLayout) => {
    setGridLayout(layout);
    persistGridLayout(layout);
    setView('grid');
  }, []);

  const [excludedGridSessions, setExcludedGridSessions] = useState<Set<string>>(readExcludedGridSessions);
  const gridMembers = useMemo(
    () => gridSessionTiles.filter((t) => !excludedGridSessions.has(t.sessionId)),
    [gridSessionTiles, excludedGridSessions],
  );
  const hiddenGridSessions = useMemo<HiddenGridSession[]>(
    () => gridSessionTiles
      .filter((t) => excludedGridSessions.has(t.sessionId))
      .map((t) => ({ sessionId: t.sessionId, title: t.title })),
    [gridSessionTiles, excludedGridSessions],
  );
  const handleRemoveFromGrid = useCallback((sessionId: string) => {
    setExcludedGridSessions((prev) => {
      if (prev.has(sessionId)) return prev;
      const next = new Set(prev);
      next.add(sessionId);
      persistExcludedGridSessions(next);
      return next;
    });
  }, []);
  const handleRestoreToGrid = useCallback((sessionId: string) => {
    setExcludedGridSessions((prev) => {
      if (!prev.has(sessionId)) return prev;
      const next = new Set(prev);
      next.delete(sessionId);
      persistExcludedGridSessions(next);
      return next;
    });
  }, []);

  const resolvedGridLayout = useMemo(
    () => resolveGridLayout(gridMembers.length, gridLayout),
    [gridMembers.length, gridLayout],
  );
  const visibleGridTiles = useMemo(
    () => gridMembers.slice(0, resolvedGridLayout.capacity),
    [gridMembers, resolvedGridLayout.capacity],
  );
  const gridOffBoardCount = gridMembers.length - visibleGridTiles.length;

  const waitingLocalSessions = unmutedEnrichedSessions.filter(wantsAttention);
  const { needsAttention: prsNeedingAttention } = usePRsNeedingAttention(prs);
  const attentionCount = waitingLocalSessions.length + prsNeedingAttention.length;

  const handleSettleActiveTurn = useMemo(
    () => (queueModeEnabled && activeSessionQueueEligible
      ? () => {
        if (!activeSessionId) return;
        sendSettleTurn(activeSessionId);
      }
      : undefined),
    [queueModeEnabled, activeSessionQueueEligible, activeSessionId, sendSettleTurn],
  );

  const [snoozeMenu, setSnoozeMenu] = useState<
    { session: { id: string; label: string }; anchor: { top: number; left: number } } | null
  >(null);

  const openSnoozeMenu = useCallback(
    (session: { id: string; label: string }, event: ReactMouseEvent) => {
      const rect = (event.currentTarget as HTMLElement).getBoundingClientRect();
      setSnoozeMenu({ session, anchor: { top: rect.bottom + 4, left: rect.left } });
    },
    [],
  );

  const handleSnoozeActiveSession = useMemo(
    () => (queueModeEnabled && activeSessionQueueEligible
      ? () => {
        if (!activeSessionId) return;
        const session = enrichedLocalSessions.find((s) => s.id === activeSessionId);
        if (!session) return;
        const row = document.querySelector<HTMLElement>(`[data-testid$="-${activeSessionId}"].queue-row`);
        const rect = row?.getBoundingClientRect();
        setSnoozeMenu({
          session: { id: session.id, label: session.label },
          anchor: rect ? { top: rect.bottom + 4, left: rect.left } : { top: 72, left: 72 },
        });
      }
      : undefined),
    [queueModeEnabled, activeSessionQueueEligible, activeSessionId, enrichedLocalSessions],
  );

  const activeSessionSnoozedUntil = activeSessionQueueEligible
    ? activeSessionForCommands?.turnSnoozedUntil
    : undefined;
  const actionMenuItemsWithQueueActions = useMemo<ActionMenuItem[]>(() => {
    if (!queueModeEnabled || !activeSessionId || !activeSessionQueueEligible) {
      return actionMenuItemsWithWorkspaceActions;
    }
    const items = [...actionMenuItemsWithWorkspaceActions];
    if (activeSessionSnoozedUntil) {
      items.push({
        id: 'wake-active-session',
        title: 'Wake this agent now',
        description: 'End the snooze and let it back into the queue',
        keywords: ['wake', 'snooze', 'defer', 'queue', 'turn'],
        icon: <AttentionActionIcon />,
        run: () => sendWakeTurn(activeSessionId),
      });
    } else if (handleSnoozeActiveSession) {
      items.push({
        id: 'snooze-active-session',
        title: 'Snooze this agent…',
        description: 'Take it off your plate until a time you choose',
        keywords: ['snooze', 'defer', 'later', 'queue', 'turn'],
        icon: <AttentionActionIcon />,
        shortcut: [shortcutTokens('session.snooze')],
        run: handleSnoozeActiveSession,
      });
    }
    return items;
  }, [
    actionMenuItemsWithWorkspaceActions,
    queueModeEnabled,
    activeSessionId,
    activeSessionQueueEligible,
    activeSessionSnoozedUntil,
    handleSnoozeActiveSession,
    sendWakeTurn,
  ]);

  const previousQueueTurnsRef = useRef<NonNullable<typeof queueBands>['turns']>([]);
  useEffect(() => {
    const previousTurns = previousQueueTurnsRef.current;
    previousQueueTurnsRef.current = queueBands?.turns ?? [];
    if (!queueModeEnabled || view !== 'session') return;
    const advance = advanceAfterTurnClosed(previousTurns, queueBands, activeSessionId);
    if (!advance) return;
    if (advance.to === 'session') {
      handleSelectSession(advance.row.session.id);
    } else {
      goHomeAwaitingNextTurn();
    }
  }, [queueBands, queueModeEnabled, view, activeSessionId, handleSelectSession, goHomeAwaitingNextTurn]);

  useEffect(() => {
    if (!followNextTurn || !queueModeEnabled || view !== 'dashboard') return;
    const next = headOfQueue(queueBands);
    if (next) handleSelectSession(next.session.id);
  }, [followNextTurn, queueModeEnabled, view, queueBands, handleSelectSession]);

  const handleJumpToWaiting = useCallback(() => {
    const waiting = oldestWantedTurn(unmutedEnrichedSessions, wantsAttention);
    if (waiting) {
      handleSelectSession(waiting.id);
    }
  }, [unmutedEnrichedSessions, handleSelectSession, wantsAttention]);

  const [showSessionlessWorkspaces, setShowSessionlessWorkspaces] = useState<boolean>(readShowSessionlessWorkspaces);
  const [workspaceSelectionStyle, setWorkspaceSelectionStyle] = useState<WorkspaceSelectionStyle>(readWorkspaceSelectionStyle);
  const handleWorkspaceSelectionStyleChange = useCallback((style: WorkspaceSelectionStyle) => {
    persistWorkspaceSelectionStyle(style);
    setWorkspaceSelectionStyle(style);
  }, []);
  const handleToggleShowSessionlessWorkspaces = useCallback(() => {
    setShowSessionlessWorkspaces((prev) => {
      const next = !prev;
      persistShowSessionlessWorkspaces(next);
      return next;
    });
  }, []);
  const sidebarWorkspaceViews = useMemo(
    () => workspaceViews.filter(
      (workspace) => !workspace.muted && (workspace.pinned || workspace.sessions.length > 0 || showSessionlessWorkspaces),
    ),
    [workspaceViews, showSessionlessWorkspaces],
  );
  const workspaceSelection = useWorkspaceSelectionController(
    workspaceViews,
    activeSessionId,
    selectedSessionlessWorkspaceId,
  );
  const activeWorkspaceId = workspaceSelection.activeWorkspaceId;

  useEffect(() => {
    probeUiAfterSwitch({
      sessionId: activeSessionId,
      workspaceId: activeWorkspaceId,
      view,
    });
  }, [activeSessionId, activeWorkspaceId, view]);

  useEffect(() => {
    activeWorkspaceIdRef.current = activeWorkspaceId;
  }, [activeWorkspaceId]);

  useEffect(() => {
    daemonWorkspacesRef.current = daemonWorkspaces;
  }, [daemonWorkspaces]);
  useEffect(() => {
    if (view === 'session' && activeWorkspaceId) {
      sendWorkspaceSelected(activeWorkspaceId);
    }
  }, [activeWorkspaceId, sendWorkspaceSelected, view]);

  const [warmWorkspaceLimit, setWarmWorkspaceLimit] = useState<number>(() => readWarmWorkspaceLimit());
  useEffect(() => {
    const w = window as Window & { attnSetWarmWorkspaces?: (n: number) => number };
    w.attnSetWarmWorkspaces = (n: number) => {
      const next = Number.isFinite(n) ? Math.trunc(n) : DEFAULT_WARM_WORKSPACE_LIMIT;
      writeWarmWorkspaceLimit(next);
      setWarmWorkspaceLimit(next);
      console.log(
        `[attn] warm workspace limit = ${next} `
        + (next < 0 ? '(virtualization disabled; all workspaces live)' : `(active + ${next} recent kept live)`),
      );
      return next;
    };
    return () => { delete w.attnSetWarmWorkspaces; };
  }, []);
  const [recentWorkspaceIds, setRecentWorkspaceIds] = useState<string[]>([]);
  useEffect(() => {
    if (!activeWorkspaceId) return;
    setRecentWorkspaceIds((prev) => (
      prev[0] === activeWorkspaceId
        ? prev
        : [activeWorkspaceId, ...prev.filter((id) => id !== activeWorkspaceId)].slice(0, 32)
    ));
  }, [activeWorkspaceId]);
  const allWorkspaceIds = useMemo(() => workspaceViews.map((w) => w.id), [workspaceViews]);
  const visibleGridSessionIds = useMemo(
    () => new Set(visibleGridTiles.map((tile) => tile.sessionId)),
    [visibleGridTiles],
  );
  const gridVisibleWorkspaceIds = useMemo(
    () => view === 'grid'
      ? workspaceViews
        .filter((workspace) => workspace.sessions.some((session) => visibleGridSessionIds.has(session.id)))
        .map((workspace) => workspace.id)
      : [],
    [view, visibleGridSessionIds, workspaceViews],
  );
  const onScreenSessionIds = useMemo(() => {
    if (view === 'grid') return visibleGridSessionIds;
    if (view !== 'session' || !activeWorkspaceId) return new Set<string>();
    const workspace = workspaceViews.find((w) => w.id === activeWorkspaceId);
    return new Set((workspace?.sessions ?? []).map((session) => session.id));
  }, [view, visibleGridSessionIds, activeWorkspaceId, workspaceViews]);

  const visibleCountdownSessionIds = useMemo(() => {
    const ids: string[] = [];
    for (const session of enrichedLocalSessions) {
      if (!onScreenSessionIds.has(session.id)) continue;
      if (session.autoSettleFiresAt || session.autoSettleHeld || session.nudgeFiresAt) ids.push(session.id);
    }
    return ids;
  }, [enrichedLocalSessions, onScreenSessionIds]);
  const armDismissSessionId = useMemo(
    () => (activeSessionId && onScreenSessionIds.has(activeSessionId) ? activeSessionId : undefined),
    [activeSessionId, onScreenSessionIds],
  );
  const handleCancelCountdown = useMemo(() => {
    if (visibleCountdownSessionIds.length > 0) {
      return () => visibleCountdownSessionIds.forEach(sendCancelCountdown);
    }
    if (!armDismissSessionId) return undefined;
    return () => sendCancelCountdown(armDismissSessionId);
  }, [visibleCountdownSessionIds, armDismissSessionId, sendCancelCountdown]);

  const warmWorkspaceIds = useMemo(
    () => computeWarmWorkspaceIds(
      allWorkspaceIds,
      recentWorkspaceIds,
      activeWorkspaceId,
      warmWorkspaceLimit,
      gridVisibleWorkspaceIds,
    ),
    [allWorkspaceIds, recentWorkspaceIds, activeWorkspaceId, warmWorkspaceLimit, gridVisibleWorkspaceIds],
  );

  const getActiveLeafDropSnapshot = useCallback(
    () => getWorkspaceLeafDropSnapshot(activeWorkspaceIdRef.current),
    [getWorkspaceLeafDropSnapshot],
  );
  const [leafWorkspaceDrag, setLeafWorkspaceDrag] = useState<LeafWorkspaceDragState | null>(null);
  const [leafDragPreview, setLeafDragPreview] = useState<LeafDragPreviewState | null>(null);
  const [dragHoverWorkspaceId, setDragHoverWorkspaceId] = useState<string | null>(null);
  const leafWorkspaceDragRef = useRef<LeafWorkspaceDragState | null>(null);
  const dragHoverTimerRef = useRef<number | null>(null);

  const clearWorkspaceDragHoverTimer = useCallback(() => {
    if (dragHoverTimerRef.current != null) {
      window.clearTimeout(dragHoverTimerRef.current);
      dragHoverTimerRef.current = null;
    }
  }, []);

  const handleLeafDragStart = useCallback((sourceWorkspaceId: string, sourceEndpointId: string | undefined, leafId: string) => {
    clearWorkspaceDragHoverTimer();
    const next = { sourceWorkspaceId, sourceEndpointId, leafId };
    leafWorkspaceDragRef.current = next;
    setLeafWorkspaceDrag(next);
    setLeafDragPreview({ draggingLeafId: leafId, dockTarget: null, ghostPos: null });
    setDragHoverWorkspaceId(null);
  }, [clearWorkspaceDragHoverTimer]);

  const handleLeafDragGhostMove = useCallback((x: number, y: number) => {
    setLeafDragPreview((prev) => (
      prev ? { ...prev, ghostPos: { x, y } } : prev
    ));
  }, []);

  const handleLeafDragPreview = useCallback((target: DockTarget | null) => {
    setLeafDragPreview((prev) => (
      prev ? { ...prev, dockTarget: target } : prev
    ));
  }, []);

  const handleLeafDragEnd = useCallback(() => {
    clearWorkspaceDragHoverTimer();
    window.setTimeout(() => {
      leafWorkspaceDragRef.current = null;
      setLeafWorkspaceDrag(null);
      setLeafDragPreview(null);
      setDragHoverWorkspaceId(null);
    }, 0);
  }, [clearWorkspaceDragHoverTimer]);

  useEffect(() => () => {
    clearWorkspaceDragHoverTimer();
  }, [clearWorkspaceDragHoverTimer]);

  const sessionlessWorkspaceStateById = useMemo(() => {
    const map = new Map<string, TerminalWorkspaceState>();
    for (const workspace of daemonWorkspaces) {
      if (!workspace.layout) {
        continue;
      }
      const { workspace: state } = workspaceSnapshotFromDaemonWorkspace(workspace.layout);
      if (state.layoutTree && state.agents.length === 0) {
        map.set(workspace.id, state);
      }
    }
    return map;
  }, [daemonWorkspaces]);


  const visualWorkspaces = sidebarWorkspaceViews;
  const visualIndexByWorkspaceId = useMemo(() => {
    return new Map(visualWorkspaces.map((workspace, index) => [workspace.id, index]));
  }, [visualWorkspaces]);

  const handleSelectWorkspace = useCallback(
    (workspaceId: string) => {
      const workspace = sidebarWorkspaceViews.find((entry) => entry.id === workspaceId)
        || workspaceViews.find((entry) => entry.id === workspaceId);
      if (!workspace) {
        return;
      }
      const sessionId = workspace.firstSessionId;
      if (sessionId) {
        handleSelectSession(sessionId);
        return;
      }
      setSelectedSessionlessWorkspaceId(workspace.id);
      setView('session');
      setUtilityFocusRequestToken((token) => token + 1);
    },
    [handleSelectSession, setView, sidebarWorkspaceViews, workspaceViews],
  );
  selectWorkspaceRef.current = handleSelectWorkspace;

  const handleSelectTile = useCallback((workspaceId: string, tileId: string) => {
    handleSelectWorkspace(workspaceId);
    setSelectedTile({ workspaceId, tileId });
  }, [handleSelectWorkspace]);

  const handleCloseTile = useCallback((workspaceId: string, tileId: string) => {
    setSelectedTile((current) => (
      current?.workspaceId === workspaceId && current.tileId === tileId ? null : current
    ));
    void sendWorkspaceUndockTile(workspaceId, tileId).catch(() => {});
  }, [sendWorkspaceUndockTile]);

  const handleReloadTile = useCallback((workspaceId: string, tileId: string) => {
    void controlBrowserHost(workspaceId, tileId, 'reload').catch((error) => {
      console.warn('[App] Failed to reload browser tile:', error);
    });
  }, []);

  useEffect(() => {
    setSelectedTile((current) => {
      if (!current) {
        return null;
      }
      const workspace = workspaceViews.find((entry) => entry.id === current.workspaceId);
      const stillExists = workspace?.children.some(
        (child) => child.kind === 'tile' && child.tile.tileId === current.tileId,
      );
      return stillExists ? current : null;
    });
  }, [workspaceViews]);

  const canMoveDraggedLeafToWorkspace = useCallback((workspace: { id: string; endpointId?: string }) => {
    const drag = leafWorkspaceDragRef.current;
    return Boolean(
      drag
        && workspace.id !== drag.sourceWorkspaceId
        && (workspace.endpointId || '') === (drag.sourceEndpointId || ''),
    );
  }, []);

  const handleWorkspaceDragEnter = useCallback((workspace: { id: string; endpointId?: string }) => {
    if (!canMoveDraggedLeafToWorkspace(workspace)) {
      return;
    }
    clearWorkspaceDragHoverTimer();
    setDragHoverWorkspaceId(workspace.id);
    dragHoverTimerRef.current = window.setTimeout(() => {
      dragHoverTimerRef.current = null;
      handleSelectWorkspace(workspace.id);
    }, 320);
  }, [canMoveDraggedLeafToWorkspace, clearWorkspaceDragHoverTimer, handleSelectWorkspace]);

  const handleWorkspaceDragLeave = useCallback((workspace: { id: string; endpointId?: string }) => {
    if (dragHoverWorkspaceId !== workspace.id) {
      return;
    }
    clearWorkspaceDragHoverTimer();
    setDragHoverWorkspaceId(null);
  }, [clearWorkspaceDragHoverTimer, dragHoverWorkspaceId]);

  const handleWorkspaceDragDrop = useCallback((workspace: { id: string; endpointId?: string }) => {
    const drag = leafWorkspaceDragRef.current;
    if (!drag || !canMoveDraggedLeafToWorkspace(workspace)) {
      return;
    }
    clearWorkspaceDragHoverTimer();
    setDragHoverWorkspaceId(null);
    handleSelectWorkspace(workspace.id);
    void sendWorkspaceMoveLeafToWorkspace(drag.sourceWorkspaceId, workspace.id, drag.leafId, SIDEBAR_LEAF_DROP_PLACEMENT).catch(() => {});
  }, [canMoveDraggedLeafToWorkspace, clearWorkspaceDragHoverTimer, handleSelectWorkspace, sendWorkspaceMoveLeafToWorkspace]);

  const handleNewWorkspaceDrop = useCallback(() => {
    const drag = leafWorkspaceDragRef.current;
    if (!drag) {
      return;
    }
    clearWorkspaceDragHoverTimer();
    setDragHoverWorkspaceId(null);
    void sendWorkspaceMoveLeafToNewWorkspace(drag.sourceWorkspaceId, drag.leafId, SIDEBAR_LEAF_DROP_PLACEMENT).catch(() => {});
  }, [clearWorkspaceDragHoverTimer, sendWorkspaceMoveLeafToNewWorkspace]);

  const handleWorkspaceReorder = useCallback((args: {
    workspaceId: string;
    prevWorkspaceId?: string;
    nextWorkspaceId?: string;
  }) => {
    void sendSetWorkspaceRank(args.workspaceId, args.prevWorkspaceId, args.nextWorkspaceId).catch(() => {});
  }, [sendSetWorkspaceRank]);

  const handleSelectWorkspaceByIndex = useCallback(
    (index: number) => {
      const workspace = visualWorkspaces[index];
      if (workspace) {
        handleSelectWorkspace(workspace.id);
      }
    },
    [visualWorkspaces, handleSelectWorkspace]
  );

  const handlePrevWorkspace = useCallback(() => {
    if (!activeWorkspaceId || visualWorkspaces.length === 0) return;
    const currentIndex = visualIndexByWorkspaceId.get(activeWorkspaceId);
    if (currentIndex === undefined) return;
    const prevIndex = currentIndex > 0 ? currentIndex - 1 : visualWorkspaces.length - 1;
    handleSelectWorkspace(visualWorkspaces[prevIndex].id);
  }, [activeWorkspaceId, visualWorkspaces, visualIndexByWorkspaceId, handleSelectWorkspace]);

  const handleNextWorkspace = useCallback(() => {
    if (!activeWorkspaceId || visualWorkspaces.length === 0) return;
    const currentIndex = visualIndexByWorkspaceId.get(activeWorkspaceId);
    if (currentIndex === undefined) return;
    const nextIndex = currentIndex < visualWorkspaces.length - 1 ? currentIndex + 1 : 0;
    handleSelectWorkspace(visualWorkspaces[nextIndex].id);
  }, [activeWorkspaceId, visualWorkspaces, visualIndexByWorkspaceId, handleSelectWorkspace]);

  const handleNavigateOutOfSession = useCallback((direction: 'left' | 'right' | 'up' | 'down') => {
    if (direction === 'left' || direction === 'up') {
      handlePrevWorkspace();
      return;
    }
    handleNextWorkspace();
  }, [handleNextWorkspace, handlePrevWorkspace]);

  const handleCloseCurrentSessionShortcut = useCallback(() => {
    // The packaged app's native "Close Pane" item claims Cmd+W and dispatches session.close, so a focused docked tile must be closed here, not the session.
    const focused = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const tileId = focused?.closest('[data-pane-kind="tile"]')?.getAttribute('data-pane-id')
      ?? focused?.closest('.session-terminal-workspace')?.getAttribute('data-active-leaf-id')
      ?? '';
    const isTile = !!tileId
      && !!document.querySelector(`[data-pane-kind="tile"][data-pane-id="${CSS.escape(tileId)}"]`);
    if (isTile && activeWorkspaceId) {
      handleCloseTile(activeWorkspaceId, tileId);
      return;
    }

    if (!activeSessionId) {
      return;
    }

    const activeSession = sessions.find((session) => session.id === activeSessionId);
    if (!activeSession) {
      return;
    }

    const activePaneId = getActivePaneIdForSession(activeSession);
    if (activePaneId) {
      void handleClosePane(activeSessionId, activePaneId);
      return;
    }

    handleRequestCloseSession(activeSessionId);
  }, [activeSessionId, activeWorkspaceId, getActivePaneIdForSession, handleCloseTile, handleClosePane, handleRequestCloseSession, sessions]);

  const handleReloadSession = useCallback((id: string) => {
    const session = sessions.find((entry) => entry.id === id);
    const paneId = session?.workspace.agents.find((pane) => pane.sessionId === id)?.id;
    const size = paneId ? getPaneSize(id, paneId) || undefined : undefined;
    void reloadSession(id, size).catch((error) => {
      const message = error instanceof Error ? error.message : String(error);
      showError(`Failed to reload session: ${message}`);
    });
  }, [getPaneSize, reloadSession, sessions, showError]);

  const handleOpenSeedTile = useCallback((seedId: string) => {
    void sendOpenSeed(seedId, activeSessionId || '')
      .then(({ workspaceId, tileId }) => {
        if (workspaceId && tileId) focusWorkspaceLeaf(workspaceId, tileId);
      })
      .catch((error) => {
        showError(error instanceof Error ? error.message : 'Could not open the seed');
      });
  }, [sendOpenSeed, activeSessionId, focusWorkspaceLeaf, showError]);

  const handleRevealSeedInGarden = useCallback((seedId: string) => {
    const trail = gardenPathToSeed(seeds, seedId);
    if (trail.length === 0) {
      showError(`Could not find ${seedId} in the Garden`);
      return;
    }
    useGardenWalk.getState().setTrail(trail);
    openDockPanel('garden');
  }, [openDockPanel, seeds, showError]);

  const checkArtifactPath = useCallback((path: string) => {
    const slash = path.lastIndexOf('/');
    if (slash <= 0) return Promise.resolve(true);
    return sendFsExists(path.slice(slash + 1), path.slice(0, slash)).then((result) => result.exists);
  }, [sendFsExists]);

  const handleOpenMarkdownArtifact = useCallback((path: string) => {
    void sendOpenMarkdown(path, '')
      .then(({ workspaceId, tileId }) => {
        if (workspaceId && tileId) focusWorkspaceLeaf(workspaceId, tileId);
      })
      .catch((error) => {
        showError(error instanceof Error ? error.message : 'Could not open the document');
      });
  }, [focusWorkspaceLeaf, sendOpenMarkdown, showError]);

  const handleResumeSeed = useCallback((seedId: string, review?: SeedReviewActionContext) => {
    const resume = sendSeedResume(seedId, review)
      .then((result) => {
        handleSelectSession(result.sessionId);
        return result;
      });
    if (review) return resume;
    return resume.catch((error) => {
      showError(error instanceof Error ? error.message : 'Failed to resume the agent');
      throw error;
    });
  }, [sendSeedResume, handleSelectSession, showError]);

  const handleHandoverSeed = useCallback((options: Parameters<typeof sendSeedHandover>[0]) => (
    sendSeedHandover({ ...options, sourceSessionId: activeSessionId || undefined })
      .then((result) => {
        handleSelectSession(result.session_id);
        return result;
      })
  ), [activeSessionId, handleSelectSession, sendSeedHandover]);

  const handleSendSeedToChief = useCallback((options: Parameters<typeof sendSeedToChief>[0]) => (
    sendSeedToChief({ ...options, sourceSessionId: activeSessionId || undefined })
  ), [activeSessionId, sendSeedToChief]);

  const handleWakeCrewMember = useCallback((member: string) => {
    sendCrewWake(member)
      .then((result) => handleSelectSession(result.sessionId))
      .catch((error) => showError(error instanceof Error ? error.message : `Failed to wake ${crewDisplayName(member)}`));
  }, [sendCrewWake, handleSelectSession, showError]);

  const handleSleepCrewMember = useCallback((member: string) => {
    sendCrewSleep(member)
      .catch((error) => showError(error instanceof Error ? error.message : `Failed to ask ${crewDisplayName(member)} to sleep`));
  }, [sendCrewSleep, showError]);

  // One stable object: the surface re-fetches on identity change.
  const annotationApi = useMemo(() => ({
    fetchMessages: sendSessionMessagesGet,
    subscribeMessagesChanged: subscribeSessionMessagesChanged,
    fetchAnnotations: sendSessionAnnotationsGet,
    saveAnnotations: sendSessionAnnotationsSave,
    clearAnnotations: sendSessionAnnotationsClear,
    submitAnnotations: sendSessionAnnotationsSubmit,
  }), [sendSessionMessagesGet, subscribeSessionMessagesChanged, sendSessionAnnotationsGet, sendSessionAnnotationsSave, sendSessionAnnotationsClear, sendSessionAnnotationsSubmit]);

  const isZedEditorConfigured = useMemo(() => {
    const editor = (settings.editor_executable || '').trim().toLowerCase();
    if (!editor) {
      return false;
    }
    return editor.includes('zed');
  }, [settings.editor_executable]);

  const handleOpenEditor = useCallback(async (cwd: string, filePath?: string, remoteTarget?: string) => {
    try {
      await invoke('open_in_editor', {
        cwd,
        filePath,
        editor: settings.editor_executable || '',
        remoteTarget,
      });
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      showError(message || 'Failed to open editor');
    }
  }, [settings.editor_executable, showError]);

  const handleOpenEditorForSession = useCallback(() => {
    const activeSession = sessions.find((s) => s.id === activeSessionId);
    if (!activeSession?.cwd) {
      showError('No active session directory');
      return;
    }
    if (activeSession.endpointId) {
      if (!activeEndpoint) {
        showError('Remote endpoint not available.');
        return;
      }
      if (!isZedEditorConfigured) {
        showError('Remote open-in-editor currently requires Zed.');
        return;
      }
      handleOpenEditor(activeSession.cwd, undefined, activeEndpoint.ssh_target);
      return;
    }
    handleOpenEditor(activeSession.cwd);
  }, [sessions, activeSessionId, activeEndpoint, handleOpenEditor, isZedEditorConfigured, showError]);

  const remoteEditorAvailable = Boolean(activeRemoteSession && activeEndpoint && isZedEditorConfigured);

  const sidebarHeaderActions = useMemo<SidebarHeaderAction[]>(() => ([
    {
      id: 'editor',
      title: !activeSessionId
        ? 'Open in Editor (No active session)'
        : activeRemoteSession
          ? remoteEditorAvailable
            ? 'Open in Zed Remote'
            : 'Open in Editor (Remote requires Zed)'
          : 'Open in Editor',
      icon: <EditorIcon />,
      disabled: !activeSessionId || (activeRemoteSession && !remoteEditorAvailable),
      onClick: handleOpenEditorForSession,
    },
    {
      id: 'workflowRun',
      title: activeSessionId ? 'Workflow Runs' : 'Workflow Runs (No active session)',
      icon: <WorkflowIcon />,
      active: workflowRunPanelOpen,
      disabled: !activeSessionId,
      onClick: () => toggleDockPanel('workflowRun'),
    },
    {
      id: 'attention',
      title: attentionPanelOpen ? 'Hide PRs Drawer' : 'Show PRs Drawer',
      icon: <PRsIcon />,
      active: attentionPanelOpen,
      badge: attentionCount > 0 ? attentionCount : undefined,
      onClick: () => toggleDockPanel('attention'),
    },
    {
      id: 'notebook',
      title: `Open Notebook (${formatShortcut('notebook.openFullscreen')})`,
      icon: <NotebookIcon />,
      active: notebookOpen,
      onClick: openNotebookBrowser,
    },
    {
      id: 'notifications',
      title: notificationsPanelOpen ? 'Hide Notifications' : 'Show Notifications',
      icon: <NotificationsBellIcon />,
      active: notificationsPanelOpen,
      badge: notificationsUnread > 0 ? notificationsUnread : undefined,
      toneClassName: hasCriticalNotification ? 'has-critical' : undefined,
      onClick: toggleNotificationsPanel,
    },
    {
      id: 'automations',
      title: automationsPanelOpen ? 'Hide Automations' : 'Show Automations',
      icon: <AutomationsIcon />,
      active: automationsPanelOpen,
      onClick: () => toggleDockPanel('automations'),
    },
    {
      id: 'garden',
      title: gardenMode === 'closed' ? 'Show the garden' : 'Hide the garden',
      icon: <GardenIcon />,
      active: gardenMode !== 'closed',
      onClick: toggleGardenFromIcon,
    },
  ]), [
    activeSessionId,
    activeRemoteSession,
    remoteEditorAvailable,
    attentionCount,
    attentionPanelOpen,
    handleOpenEditorForSession,
    workflowRunPanelOpen,
    toggleDockPanel,
    notebookOpen,
    openNotebookBrowser,
    gardenMode,
    toggleGardenFromIcon,
    notificationsPanelOpen,
    notificationsUnread,
    hasCriticalNotification,
    automationsPanelOpen,
    gardenPanelOpen,
    toggleNotificationsPanel,
  ]);

  const activeSessionZoomed = activeWorkspaceId ? Boolean(zoomModeBySessionId[activeWorkspaceId]) : false;

  const dockActions = useMemo<Partial<Record<ShortcutId, {
    run?: () => void;
    isActive?: boolean;
    available?: boolean;
  }>>>(() => ({
    'dock.attention': {
      run: () => toggleDockPanel('attention'),
      isActive: attentionPanelOpen,
    },
    'terminal.splitVertical': { available: Boolean(activeSessionId) },
    'terminal.splitHorizontal': { available: Boolean(activeSessionId) },
    'session.newHorizontal': { available: Boolean(activeSessionId) },
    'terminal.toggleZoom': { isActive: activeSessionZoomed, available: Boolean(activeSessionId) },
  }), [
    activeSessionId,
    attentionPanelOpen,
    activeSessionZoomed,
    toggleDockPanel,
  ]);

  const dockItems = useMemo<DockItem[]>(() => (
    keybindings.dock.items.flatMap((id) => {
      const action = dockActions[id];
      if (action && action.available === false) return [];
      const keys = formatShortcut(id);
      if (!keys) return [];
      return [{
        id,
        label: dockShortcutLabel(id),
        keys,
        active: action?.isActive ?? false,
        onClick: action?.run,
      }];
    })
  ), [keybindings.dock.items, keybindings.config, dockActions]);

  const handleQuitApp = useCallback(() => {
    if (isTauri()) {
      void invoke('quit_app');
      return;
    }
    window.close();
  }, []);

  const appShortcutsEnabled = !locationPickerOpen
    && !whatsNew.isOpen
    && !actionMenuOpen
    && !markdownOpenerOpen
    && !shortcutEditorOpen
    && !notebookOpen;
  useKeyboardShortcuts({
    onNewSession: () => handleNewSession('vertical'),
    onNewSessionHorizontal: () => handleNewSession('horizontal'),
    onNewWorkspace: handleNewWorkspace,
    onCloseSession: handleCloseCurrentSessionShortcut,
    onToggleActionMenu: handleToggleActionMenu,
    onGoToDashboard: goToDashboard,
    onToggleGridMode: toggleGridMode,
    onJumpToWaiting: handleJumpToWaiting,
    onSettleTurn: handleSettleActiveTurn,
    onSnoozeTurn: handleSnoozeActiveSession,
    onCancelCountdown: handleCancelCountdown,
    onSelectWorkspaceByIndex: handleSelectWorkspaceByIndex,
    onPrevSession: handlePrevWorkspace,
    onNextSession: handleNextWorkspace,
    onToggleSidebar: toggleSidebarCollapse,
    onRefreshPRs: handleRefreshPRs,
    onToggleAttentionPanel: () => toggleDockPanel('attention'),
    onOpenSettings: useCallback(() => setSettingsOpen(prev => !prev), []),
    onShowShortcuts: useCallback(() => setShortcutsOpen(prev => !prev), []),
    onIncreaseFontSize: increaseScale,
    onDecreaseFontSize: decreaseScale,
    onResetFontSize: resetScale,
    onOpenFile: handleOpenMarkdownFile,
    onOpenNotebookTile: handleOpenNotebookTile,
    onOpenNotebookFullscreen: openNotebookBrowser,
    onOpenGarden: toggleGardenFrame,
    onQuit: handleQuitApp,
    enabled: appShortcutsEnabled && !gardenHoldsWindow,
    gardenShortcutEnabled: appShortcutsEnabled,
  });

  const effectiveNotebookRoot = settings['notebook.root.effective'] || '';

  const makeNotebookSurfaceDaemon = useCallback((root?: string) => ({
    listDir: (path: string) => sendFsList(path, root),
    readFile: (path: string) => sendFsRead(path, root),
    writeFile: (path: string, content: string, baseHash?: string) => sendFsWrite(path, content, baseHash, root),
    existsFile: (path: string) => sendFsExists(path, root),
    readAsset: (path: string) => sendFsReadAsset(path, root),
    backlinksNotebook: sendNotebookBacklinks,
    sendToChief: sendNotebookToChief,
    listFiles: () => sendFsIndex(root).then(fsIndexToNotebookEntries),
    changeSignal: fsChangeSignals[fsChangeSignalKey(root || '', effectiveNotebookRoot)] || 0,
  }), [
    sendFsList,
    sendFsRead,
    sendFsWrite,
    sendFsExists,
    sendFsReadAsset,
    sendNotebookBacklinks,
    sendNotebookToChief,
    sendFsIndex,
    fsChangeSignals,
    effectiveNotebookRoot,
  ]);

  const notebookSurfaceContextValue = useMemo(() => ({
    makeDaemon: makeNotebookSurfaceDaemon,
    effectiveNotebookRoot,
    sendFsWatch,
    sendFsUnwatch,
    connectionGeneration,
  }), [makeNotebookSurfaceDaemon, effectiveNotebookRoot, sendFsWatch, sendFsUnwatch, connectionGeneration]);

  const notebookRootChangeSignal = fsChangeSignals[fsChangeSignalKey('', effectiveNotebookRoot)] || 0;

  const notebookBrowserListFiles = useCallback(
    () => sendFsIndex().then(fsIndexToNotebookEntries),
    [sendFsIndex],
  );

  return (
    <DaemonProvider sendPRAction={sendPRAction} sendMutePR={sendMutePR} sendMuteRepo={sendMuteRepo} sendMuteAuthor={sendMuteAuthor} sendPRVisited={sendPRVisited}>
    <NotebookSurfaceProvider value={notebookSurfaceContextValue}>
    <div className="app" ref={appShellRef} tabIndex={-1} style={{ outline: 'none' }} onPointerDownCapture={handleAppPointerDownCapture}>
      <BannerStack
        connectionError={connectionError}
        warnings={warnings}
        updateAvailableVersion={updateAvailableVersion}
        onOpenWarningUrl={(url) => {
          openUrl(url).catch((err) => {
            console.error('[App] Failed to open warning link:', err);
          });
        }}
        onClearWarnings={clearWarnings}
        onOpenLatestRelease={onOpenLatestRelease}
        onDismissLatestRelease={onDismissLatestRelease}
      />
      {openPRLauncherJob && (
        <OpenPRLauncherProgress
          repo={openPRLauncherJob.pr.repo}
          number={openPRLauncherJob.pr.number}
          title={openPRLauncherJob.pr.title}
          step={openPRLauncherJob.progress.step}
        />
      )}
      <div className="app-frame">
        <Sidebar
          workspaces={sidebarWorkspaceViews}
          visualOrder={visualWorkspaces}
          visualIndexByWorkspaceId={visualIndexByWorkspaceId}
          selectedId={activeSessionId}
          selectedWorkspaceId={activeWorkspaceId}
          selectedTile={selectedTile}
          tileContents={tileContents}
          collapsed={sidebarCollapsed}
          profile={BUILD_PROFILE}
          headerActions={sidebarHeaderActions}
          criticalNotifications={criticalNotifications}
          onOpenNotifications={openNotificationsPanel}
          gridLayout={gridLayout}
          onSelectGridLayout={handleSelectGridLayout}
          dockItems={dockItems}
          dockCollapsed={keybindings.dock.collapsed}
          onToggleDockCollapsed={() => keybindings.setDockCollapsed(!keybindings.dock.collapsed)}
          mutedWorkspaces={mutedWorkspaceViews}
          mutedExpanded={sidebarMutedExpanded}
          onMutedExpandedChange={setSidebarMutedExpanded}
          onMuteWorkspace={sendMuteWorkspace}
          onPinWorkspace={sendPinWorkspace}
          onPinSession={sendPinSession}
          onRenameSession={sendRenameSession}
          onRenameWorkspace={sendRenameWorkspace}
          onChangeChiefOfStaff={handleChangeChiefOfStaff}
          showSessionless={showSessionlessWorkspaces}
          onToggleShowSessionless={handleToggleShowSessionlessWorkspaces}
          crew={crew}
          onWakeCrewMember={handleWakeCrewMember}
          onSleepCrewMember={handleSleepCrewMember}
          queueModeEnabled={queueModeEnabled}
          onToggleQueueMode={handleToggleQueueMode}
          crewQueueEnabled={crewQueueEnabled}
          onToggleCrewQueue={handleToggleCrewQueue}
          workspaceSelectionStyle={workspaceSelectionStyle}
          onWorkspaceSelectionStyleChange={handleWorkspaceSelectionStyleChange}
          leafDrag={leafWorkspaceDrag ? {
            sourceWorkspaceId: leafWorkspaceDrag.sourceWorkspaceId,
            endpointId: leafWorkspaceDrag.sourceEndpointId,
          } : null}
          dragHoverWorkspaceId={dragHoverWorkspaceId}
          onWorkspaceDragEnter={handleWorkspaceDragEnter}
          onWorkspaceDragLeave={handleWorkspaceDragLeave}
          onWorkspaceDragDrop={handleWorkspaceDragDrop}
          onNewWorkspaceDrop={handleNewWorkspaceDrop}
          onSessionDragStart={handleLeafDragStart}
          onSessionDragEnd={handleLeafDragEnd}
          onWorkspaceReorder={handleWorkspaceReorder}
          queue={queueBands}
          onSettleTurn={sendSettleTurn}
          onOpenSnooze={openSnoozeMenu}
          onWakeTurn={sendWakeTurn}
          onScreenSessionIds={onScreenSessionIds}
          onSelectSession={handleSelectSession}
          onTriggerNudge={sendTriggerNudge}
          onSelectWorkspace={handleSelectWorkspace}
          onSelectTile={handleSelectTile}
          onCloseTile={handleCloseTile}
          onReloadTile={handleReloadTile}
          onNewSession={() => handleNewSession('vertical')}
          onCloseSession={handleRequestCloseSession}
          onReloadSession={handleReloadSession}
          onGoToDashboard={goToDashboard}
          homeActive={view === 'dashboard'}
          onToggleCollapse={toggleSidebarCollapse}
        />
        <div className="view-stack">
      {/* Always rendered; shown/hidden via z-index. */}
      <div className={`view-container ${view === 'dashboard' ? 'visible' : 'hidden'}`}>
        <Dashboard
          sessions={unmutedEnrichedSessions}
          mutedWorkspaces={mutedWorkspaceViews}
          prs={prs}
          isLoading={!hasReceivedInitialState}
          isRefreshing={isRefreshingPRs}
          refreshError={refreshError}
          rateLimit={rateLimit}
          endpoints={daemonEndpoints}
          onRebootstrapEndpoint={handleRebootstrapEndpoint}
          queueModeEnabled={queueModeEnabled}
          crewQueueEnabled={crewQueueEnabled}
          activityStaleMs={activityStaleMs(settings)}
          followNextTurn={followNextTurn}
          onToggleFollowNextTurn={() => setFollowNextTurn((armed) => !armed)}
          onSelectSession={handleSelectSession}
          onNewSession={() => handleNewSession('vertical')}
          onWakeTurn={sendWakeTurn}
          onRefreshPRs={handleRefreshPRs}
          onOpenPR={handleOpenPR}
          onOpenSettings={() => setSettingsOpen(true)}
          onMutedGroupClick={() => {
            setSidebarCollapsed(false);
            setSidebarMutedExpanded(true);
            setView('session');
          }}
        />
      </div>

      {/* Always rendered, to keep terminals alive. */}
      <div className={`view-container ${view === 'session' ? 'visible' : 'hidden'}`}>
        <div className="terminal-pane">
          <div className="terminal-main-area">
            {workspaceViews.map((workspace) => {
              const workspaceState = terminalStateForWorkspaceSessions(workspace.sessions)
                ?? sessionlessWorkspaceStateById.get(workspace.id)
                ?? null;
              if (!workspaceState) {
                return null;
              }
              const focusedSessionId = workspaceSelection.focusedSessionIdByWorkspace[workspace.id]
                ?? workspace.focusedSessionId;
              const focusedSession = focusedSessionId
                ? workspace.sessions.find((session) => session.id === focusedSessionId) ?? null
                : null;
              const activePaneId = activePaneIdForFocusedSession(
                workspaceState,
                focusedSession,
                getActivePaneIdForSession,
              );
              const isActiveWorkspace = workspace.id === activeWorkspaceId;
              const terminalsLive = warmWorkspaceIds === null
                || isActiveWorkspace
                || warmWorkspaceIds.has(workspace.id);
              return (
                <div
                  key={`${workspace.endpointId || 'local'}:${workspace.id}`}
                  className={`terminal-wrapper ${isActiveWorkspace ? 'active' : ''}`}
                >
                  <SessionTerminalWorkspace
                    ref={setWorkspaceRef(workspace.id)}
                    workspaceId={workspace.id}
                    workspaceDirectory={localWorkspaceDirectory({ directory: workspace.directory, endpoint_id: workspace.endpointId })}
                    workspaceSessions={workspace.sessions.map((entry) => ({
                      id: entry.id,
                      label: entry.label,
                      agent: entry.agent,
                      cwd: entry.cwd,
                      endpointId: entry.endpointId,
                      state: entry.state,
                      ticketUnread: entry.ticketUnread,
                      nudgeFiresAt: entry.nudgeFiresAt,
                      autoSettleFiresAt: entry.autoSettleFiresAt,
                      autoSettleHeld: entry.autoSettleHeld,
                      autoSettleDismissArmed: entry.autoSettleDismissArmed,
                      terminalBuildStale: entry.terminalBuildStale,
                      usage: entry.usage,
                      isActive: entry.id === activeSessionId,
                      presentation: presentationBySessionId.get(entry.id),
                      seedId: entry.seedId,
                      crewMember: entry.crewMember,
                      automation: entry.automation,
                      pullRequests: entry.pullRequests,
                    }))}
                    seedTargetSessions={daemonSessions.map((session) => ({
                      sessionId: session.id,
                      label: session.label || session.id,
                      state: session.state,
                    }))}
                    gardenSeeds={seeds}
                    onOpenSeed={handleOpenSeedTile}
                    onRevealSeedInGarden={handleRevealSeedInGarden}
                    seedPopoverRequest={seedPopoverRequest}
                    usagePopoverRequest={usagePopoverRequest}
                    conversationAgents={conversationPaneAgents}
                    annotationApi={annotationApi}
                    onTriggerNudge={sendTriggerNudge}
                    onCancelCountdown={sendCancelCountdown}
                    onTerminalPointerActivity={sendTerminalPointerActivity}
                    onOpenPresentation={handleOpenPresentationWindow}
                    onOpenMarkdown={(path, sessionId) => {
                      void sendOpenMarkdown(path, sessionId)
                        .then(({ workspaceId, tileId }) => {
                          if (workspaceId && tileId) focusWorkspaceLeaf(workspaceId, tileId);
                        })
                        .catch((error) => {
                          console.error('[Markdown] in-app open failed, falling back to OS open:', error);
                          void openPath(path).catch((openError) => {
                            console.error('[Markdown] OS open fallback failed:', openError);
                          });
                        });
                    }}
                    onTerminalModelRecovered={handleTerminalModelRecovered}
                    workspace={workspaceState}
                    workspaceSelectionStyle={workspaceSelectionStyle}
                    activePaneId={activePaneId}
                    fontSize={terminalFontSize}
                    resolvedTheme={resolvedTheme}
                    focusRequestToken={utilityFocusRequestToken}
                    enabled={!blockingOverlayOpen}
                    isActiveSession={isActiveWorkspace}
                    isSessionViewVisible={view === 'session'}
                    terminalsLive={terminalsLive}
                    eventRouter={paneRuntimeEventRouter}
                    onSplitPane={(targetPaneId, direction) => {
                      void createSplitSession('shell', direction, targetPaneId);
                    }}
                    onClosePane={(paneId) => {
                      const paneSessionId = workspaceState.agents.find((pane) => pane.id === paneId)?.sessionId;
                      if (paneSessionId) {
                        void handleClosePane(paneSessionId, paneId).catch(console.error);
                      }
                    }}
                    onRenameSession={sendRenameSession}
                    onResizeSplit={(splitId, ratio) => {
                      return sendWorkspaceSetSplitRatio(workspace.id, splitId, ratio);
                    }}
                    onFocusPane={(paneId) => {
                      const agentPane = workspaceState.agents.find((pane) => pane.id === paneId);
                      const paneSessionId = agentPane?.sessionId;
                      if (!paneSessionId) {
                        return;
                      }
                      setActivePane(paneSessionId, paneId);
                      if (paneSessionId !== activeSessionId) {
                        setActiveSession(paneSessionId);
                      }
                    }}
                    zoomActive={Boolean(zoomModeBySessionId[workspace.id])}
                    onSetZoomActive={(active) => {
                      setZoomModeBySessionId((prev) => (
                        prev[workspace.id] === active
                          ? prev
                          : { ...prev, [workspace.id]: active }
                      ));
                    }}
                    onNavigateOutOfSession={handleNavigateOutOfSession}
                    onUndockTile={(tileId) => {
                      handleCloseTile(workspace.id, tileId);
                    }}
                    onUpdateTile={(tileId, tileParams, tileSessionId) => (
                      sendWorkspaceUpdateTile(workspace.id, tileId, tileParams, tileSessionId)
                    )}
                    onMoveLeaf={(leafId, anchorId, edge, ratio) => {
                      const targetWorkspaceId = activeWorkspaceIdRef.current || workspace.id;
                      if (targetWorkspaceId !== workspace.id) {
                        void sendWorkspaceMoveLeafToWorkspace(workspace.id, targetWorkspaceId, leafId, { anchorId, edge, ratio }).catch(() => {});
                        return;
                      }
                      void sendWorkspaceMoveLeaf(workspace.id, leafId, { anchorId, edge, ratio }).catch(() => {});
                    }}
                    getActiveLeafDropSnapshot={getActiveLeafDropSnapshot}
                    onLeafDragStart={(leafId) => handleLeafDragStart(workspace.id, workspace.endpointId, leafId)}
                    onLeafDragGhostMove={handleLeafDragGhostMove}
                    onLeafDragPreview={handleLeafDragPreview}
                    onLeafDragEnd={handleLeafDragEnd}
                    leafDragPreview={leafDragPreview}
                    tileContents={tileContents}
                    allowLocalTileTargets={!workspace.endpointId}
                    onRequestTileContent={requestTileContent}
                  />
                </div>
              );
            })}
            {sessions.length === 0 && (
              <div className="no-sessions">
                <p>No active sessions</p>
                <p>Press {formatShortcut('session.newWorkspace')} to start a new workspace</p>
              </div>
            )}
          </div>
        </div>
        <RightDock
          panelOrder={dockPanelStack}
          panels={[
            {
              id: 'workflowRun',
              isOpen: workflowRunPanelOpen && Boolean(activeSessionId),
              width: 'clamp(420px, 50vw, 680px)',
              tone: activeWorkflowRun ? toneForDockPanel(activeWorkflowRun.status) : 'default',
              className: 'dock-panel dock-panel--workflow-run',
              children: activeSessionId ? (
                <WorkflowRunView
                  run={activeWorkflowRun}
                  onClose={() => closeDockPanel('workflowRun')}
                />
              ) : null,
            },
            {
              id: 'attention',
              isOpen: attentionPanelOpen,
              width: 'clamp(360px, 48vw, 600px)',
              className: 'dock-panel dock-panel--attention attention-drawer',
              children: (
                <AttentionDrawer
                  onClose={() => closeDockPanel('attention')}
                  waitingSessions={waitingLocalSessions}
                  prs={prs}
                  onSelectSession={handleSelectSession}
                />
              ),
            },
            {
              id: 'automations',
              isOpen: automationsPanelOpen,
              width: 'clamp(420px, 42vw, 640px)',
              className: 'dock-panel dock-panel--automations',
              children: (
                <AutomationsPanel
                  isOpen={automationsPanelOpen}
                  onClose={() => closeDockPanel('automations')}
                  fetchDefinitions={listAutomationDefinitions}
                  fetchRuns={listAutomationRuns}
                  setEnabled={setAutomationEnabled}
                  runNow={runAutomationNow}
                  getDefinition={getAutomationDefinition}
                  applyDefinition={applyAutomationDefinition}
                  deleteDefinition={deleteAutomationDefinition}
                  onSelectSession={handleSelectSession}
                  onFocusPane={(sessionId, paneId) => focusSessionPane(sessionId, paneId, 40)}
                />
              ),
            },
            {
              id: 'garden',
              width: 'clamp(380px, 34vw, 560px)',
              isOpen: gardenPanelOpen && !gardenHoldsWindow,
              detached: gardenSlotRef,
              children: null,
            },
          ]}
        />
      </div>
        </div>
      </div>

      {/* Mounted only while active, so its WebGL context is released on exit. */}
      {view === 'grid' && (
        <div className="view-container visible">
          <GridView
            tiles={visibleGridTiles}
            layout={{ rows: resolvedGridLayout.rows, cols: resolvedGridLayout.cols }}
            offBoardCount={gridOffBoardCount}
            hiddenSessions={hiddenGridSessions}
            onRemoveTile={handleRemoveFromGrid}
            onRestoreTile={handleRestoreToGrid}
            resolvedTheme={resolvedTheme}
            getScreenSnapshot={getScreenSnapshot}
          />
        </div>
      )}

      <LocationPicker
        isOpen={locationPickerOpen}
        purpose={locationPickerPurpose}
        onClose={closeLocationPicker}
        onSelect={handleLocationSelect}
        onGetRecentLocations={sendGetRecentLocations}
        onBrowseDirectory={sendBrowseDirectory}
        onInspectPath={sendInspectPath}
        onGetRepoInfo={getRepoInfo}
        onCreateWorktree={sendCreateWorktree}
        onCreateWorktreeSession={handleCreateWorktreeSession}
        onDeleteWorktree={sendDeleteWorktree}
        onError={showError}
        projectsDirectory={settings.projects_directory}
        agentAvailability={agentAvailability}
        endpoints={daemonEndpoints}
        chiefExists={hasChiefOfStaff}
        conversationAgents={conversationPaneAgents}
        onListPastConversations={sendListPastConversations}
      />
      <UndoToast />
      <SessionCreationProgress
        isVisible={sessionCreationJob !== null}
        label={sessionCreationJob?.label || ''}
        path={sessionCreationJob?.path || ''}
        phase={sessionCreationJob?.phase || 'starting_session'}
        error={sessionCreationJob?.error}
        onDismiss={() => setSessionCreationJob(null)}
      />
      <CloseSessionPrompt
        isVisible={pendingSessionClose !== null}
        sessionLabel={pendingSessionClose?.label || ''}
        splitCount={pendingSessionClose?.splitCount || 0}
        onConfirm={handleConfirmSessionClose}
        onCancel={handleCancelSessionClose}
      />
      <ChiefOfStaffTransferPrompt
        isVisible={chiefTransferTarget !== null}
        currentLabel={chiefTransferTarget?.currentLabel ?? ''}
        targetLabel={chiefTransferTarget?.targetLabel ?? ''}
        isSaving={chiefTransferSaving}
        onConfirm={() => void handleConfirmChiefTransfer()}
        onCancel={() => {
          if (!chiefTransferSaving) {
            setChiefTransferTarget(null);
          }
        }}
      />
      {appViewParamsPrompt && (
        <AppViewParamsPrompt
          viewTitle={appViewParamsPrompt.viewTitle}
          label={appViewParamsPrompt.label}
          placeholder={appViewParamsPrompt.placeholder}
          onSubmit={(params) => dockAppViewTile(appViewParamsPrompt.app, appViewParamsPrompt.view, params)}
          onClose={() => setAppViewParamsPrompt(null)}
        />
      )}
      {contextCapPromptSession && (
        <SessionContextCapPrompt
          sessionLabel={contextCapPromptSession.label}
          currentCap={contextCapPromptSession.currentCap}
          onSubmit={(cap) => sendSetSessionContextWindowCap(contextCapPromptSession.id, cap)}
          onClose={() => setContextCapPromptSession(null)}
        />
      )}
      <ErrorToast message={errorMessage} durationMs={errorDurationMs} onDone={clearError} />
      {inputDiagnosticsCopied.saved('copied') && (
        <div className="input-diagnostics-copied" role="status">Terminal input diagnostics copied</div>
      )}
      <ChordLeaderHud />
      <NotebookBrowser
        isOpen={notebookOpen}
        initialPath={notebookRequestedPath}
        onClose={() => {
          setNotebookOpen(false);
          setNotebookRequestedPath(null);
        }}
        listDir={sendFsList}
        readFile={sendFsRead}
        writeFile={sendFsWrite}
        existsFile={sendFsExists}
        readAsset={sendFsReadAsset}
        backlinksNotebook={sendNotebookBacklinks}
        sendToChief={sendNotebookToChief}
        listFiles={notebookBrowserListFiles}
        changeSignal={notebookRootChangeSignal}
        chiefActive={notebookChiefActive}
      />
      <GardenFrame
        mode={gardenMode}
        dockRect={gardenDockRect}
        onToggleFrame={toggleGardenFrame}
        onEscapeFloor={closeGarden}
        onClose={closeGarden}
        seeds={seeds}
        seedsTotal={seedsTotal}
        liveSessions={liveGardenSessions}
        tenderSessionLabels={gardenSessionLabels}
        loaded={hasReceivedInitialState}
        moveSeed={sendSeedTransition}
        noteSeed={sendSeedNote}
        fetchSeedDocument={sendSeedDocumentGet}
        onOpenAsTile={(seedId) => {
          closeGarden();
          handleOpenSeedTile(seedId);
        }}
        onOpenMarkdownArtifact={handleOpenMarkdownArtifact}
        checkArtifactPath={checkArtifactPath}
        onResumeSeed={handleResumeSeed}
        onHandoverSeed={handleHandoverSeed}
        onSendSeedToChief={handleSendSeedToChief}
        chiefAvailable={hasChiefOfStaff}
        reviewOverview={seedReviewOverview}
        showReview={sendSeedReviewShow}
        startReview={sendSeedReviewStart}
        retryReviewItem={sendSeedReviewRetry}
        keepReviewItem={sendSeedReviewKeep}
        draftReviewHandover={sendSeedReviewDraft}
      />
      <NotificationsPanel
        open={notificationsPanelOpen}
        onClose={closeNotificationsPanel}
        listNotifications={sendNotificationList}
        markRead={sendNotificationMarkRead}
        retryTask={sendTaskRetry}
        changeSignal={notificationsChangeSignal}
      />
      {markdownOpenerOpen && (
        <MarkdownOpener
          root={markdownOpenerTarget.root}
          loadRecents={loadOpenerRecents}
          loadIndex={loadOpenerIndex}
          browseDirectory={sendBrowseDirectory}
          onClose={() => setMarkdownOpenerOpen(false)}
          onPick={(path) => {
            setMarkdownOpenerOpen(false);
            const bindTo = markdownOpenerTarget.sessionId;
            if (bindTo === null) {
              // The selected session lives on another machine; docking a tile for this local file would bind it there.
              void openPath(path).catch((openError) => {
                console.error('[MarkdownOpener] OS open failed:', openError);
              });
              return;
            }
            void sendOpenMarkdown(path, bindTo)
              .then(({ workspaceId, tileId }) => {
                if (workspaceId && tileId) focusWorkspaceLeaf(workspaceId, tileId);
              })
              .catch((error) => {
                console.error('[MarkdownOpener] in-app open failed, falling back to OS open:', error);
                void openPath(path).catch((openError) => {
                  console.error('[MarkdownOpener] OS open fallback failed:', openError);
                });
              });
          }}
        />
      )}
      {snoozeMenu && (
        <SnoozeMenu
          sessionLabel={snoozeMenu.session.label}
          anchor={snoozeMenu.anchor}
          onSnooze={(until) => sendSnoozeTurn(snoozeMenu.session.id, until)}
          onClose={() => setSnoozeMenu(null)}
        />
      )}
      <ActionMenu
        isOpen={actionMenuOpen}
        actions={actionMenuItemsWithQueueActions}
        onClose={() => setActionMenuOpen(false)}
      />
      <ShortcutsModal
        isOpen={shortcutsOpen}
        onClose={() => setShortcutsOpen(false)}
        onEdit={() => {
          setShortcutsOpen(false);
          setShortcutEditorOpen(true);
        }}
      />
      <ShortcutEditorModal
        isOpen={shortcutEditorOpen}
        onClose={() => setShortcutEditorOpen(false)}
      />
      <WhatsNewModal
        isOpen={whatsNew.isOpen}
        onClose={whatsNew.dismiss}
        onViewShortcuts={() => {
          whatsNew.dismiss();
          setShortcutsOpen(true);
        }}
      />
      <SettingsModal
        isOpen={settingsOpen}
        onClose={() => setSettingsOpen(false)}
        mutedRepos={mutedRepos}
        githubHosts={daemonGitHubHosts}
        onUnmuteRepo={sendMuteRepo}
        mutedAuthors={mutedAuthors}
        onUnmuteAuthor={sendMuteAuthor}
        settings={settings}
        endpoints={daemonEndpoints}
        plugins={daemonPlugins}
        pluginIssues={daemonPluginIssues}
        onAddEndpoint={sendAddEndpoint}
        onUpdateEndpoint={sendUpdateEndpoint}
        onRemoveEndpoint={sendRemoveEndpoint}
        onSetEndpointRemoteWeb={sendSetEndpointRemoteWeb}
        onListPlugins={sendListPlugins}
        onInstallPlugin={sendInstallPlugin}
        onInstallBundledPlugin={sendInstallBundledPlugin}
        onUninstallPlugin={sendUninstallPlugin}
        onRemovePlugin={sendRemovePlugin}
        onSetPluginPriority={sendSetPluginPriority}
        onSetSetting={sendSetSetting}
        themePreference={themePreference}
        onSetTheme={setTheme}
        uiScale={scale}
        onIncreaseUIScale={increaseScale}
        onDecreaseUIScale={decreaseScale}
        onResetUIScale={resetScale}
        gardenScale={gardenScale.scale}
        effectiveGardenScale={gardenScale.effectiveScale}
        onIncreaseGardenScale={gardenScale.increaseScale}
        onDecreaseGardenScale={gardenScale.decreaseScale}
        onMatchAppGardenScale={gardenScale.matchApp}
        listTasks={sendTaskList}
        retryTask={sendTaskRetry}
        taskChangeSignal={notebookTaskChangeSignal}
      />
    </div>
    </NotebookSurfaceProvider>
    </DaemonProvider>
  );
}

export default App;
