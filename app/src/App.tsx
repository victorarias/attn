import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { onOpenUrl, getCurrent } from '@tauri-apps/plugin-deep-link';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { getVersion } from '@tauri-apps/api/app';
import { openUrl } from '@tauri-apps/plugin-opener';
import { Sidebar, type SidebarHeaderAction, type DockItem, ReviewLoopIcon, WorkflowIcon, EditorIcon, DiffIcon, PRsIcon } from './components/Sidebar';
import { Dashboard } from './components/Dashboard';
import { AttentionDrawer } from './components/AttentionDrawer';
import { LocationPicker } from './components/LocationPicker';

import { UndoToast } from './components/UndoToast';
import { WorktreeCleanupPrompt } from './components/WorktreeCleanupPrompt';
import { CloseSessionPrompt } from './components/CloseSessionPrompt';
import { ChiefOfStaffTransferPrompt } from './components/ChiefOfStaffTransferPrompt';
import { ChangesPanel } from './components/ChangesPanel';
import { DiffDetailPanel } from './components/DiffDetailPanel';
import { SessionReviewLoopBar } from './components/SessionReviewLoopBar';
import { WorkflowRunView } from './components/WorkflowRunView';
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
import { WorkspaceContextNavigator, type WorkspaceContextView } from './components/WorkspaceContextNavigator';
import { NotebookBrowser } from './components/NotebookBrowser';
import { ErrorToast, useErrorToast } from './components/ErrorToast';
import { ChordLeaderHud } from './components/ChordLeaderHud';
import { DaemonProvider } from './contexts/DaemonContext';
import { SettingsProvider } from './contexts/SettingsContext';
import { KeybindingsProvider, useKeybindings } from './contexts/KeybindingsContext';
import { useSessionStore, type Session, type TerminalWorkspaceState } from './store/sessions';
import {
  computeWarmWorkspaceIds,
  DEFAULT_WARM_WORKSPACE_LIMIT,
  readWarmWorkspaceLimit,
  writeWarmWorkspaceLimit,
} from './utils/terminalVirtualization';
import { useDaemonSocket, DaemonWorktree, DaemonSession, DaemonWorkspace, DaemonPR, DaemonEndpoint, DaemonPlugin, DaemonPluginIssue, GitStatusUpdate, BranchDiffFile, DaemonWarning, ReviewLoopState, SessionExitInfo } from './hooks/useDaemonSocket';
import { useSessionWorkspaceController } from './hooks/useSessionWorkspaceController';
import { isAttentionSessionState, normalizeSessionState } from './types/sessionState';
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
import { hasPane, workspaceSnapshotFromDaemonWorkspace, type TerminalSplitDirection } from './types/workspace';
import { useDaemonStore } from './store/daemonSessions';
import { usePRsNeedingAttention } from './hooks/usePRsNeedingAttention';
import { useKeyboardShortcuts } from './hooks/useKeyboardShortcuts';
import { useWhatsNew } from './hooks/useWhatsNew';
import { shortcutTokens, formatShortcut, dockShortcutLabel } from './shortcuts';
import type { ShortcutId } from './shortcuts';
import { useUIScale } from './hooks/useUIScale';
import { useTheme } from './hooks/useTheme';
import { useOpenPR, type OpenPRProgress } from './hooks/useOpenPR';
import { useUiAutomationBridge } from './hooks/useUiAutomationBridge';
import { ptySpawn } from './pty/bridge';
import { clearBrowserHostFocus, controlBrowserHost, isBrowserHostOwnedTarget } from './browser/host';
import {
  agentLabel,
  getAgentAvailability,
  getAgentExecutableSettings,
  hasAnyAvailableAgents,
  resolvePreferredAgent,
} from './utils/agentAvailability';
import { normalizeInstallChannel, shouldCheckForReleaseUpdates } from './utils/installChannel';
import { buildWorkspaceViewModels, filterSessionsRepresentedInWorkspaceLayouts } from './utils/workspaceViewModels';
import { useWorkspaceSelectionController } from './hooks/useWorkspaceSelectionController';
import './App.css';

const RELEASES_LATEST_API = 'https://api.github.com/repos/victorarias/attn/releases/latest';
const RELEASES_LATEST_WEB = 'https://github.com/victorarias/attn/releases/latest';
const RELEASE_CHECK_INTERVAL_MS = 6 * 60 * 60 * 1000;
const UPDATE_BANNER_DISMISSED_STORAGE_KEY = 'attn.update_banner.dismissed_version';
const DOCK_PANEL_EXIT_MS = 260;
const CHANGES_BRANCH_DIFF_INTERVAL_MS = 30_000;
const CHANGES_BRANCH_DIFF_STATUS_DEBOUNCE_MS = 750;
const CHANGES_BRANCH_DIFF_SLOW_THRESHOLD_MS = 5_000;
const CHANGES_BRANCH_DIFF_SLOW_COOLDOWN_MS = 30_000;
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

interface SplitSessionOptions {
  baseSessionId?: string;
  cwd?: string;
  endpointId?: string | null;
  label?: string;
  yoloMode?: boolean;
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

// Placement for a leaf relocated by a sidebar drag — merging into a workspace or
// splitting into a new one. Dock against the whole target (empty anchor), left
// edge, taking ~a third of the resulting split.
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

// Sessionless (tile-only) workspaces — those kept alive by a docked tile after
// their last terminal closed — are hidden from the sidebar unless the user opts
// in via the sidebar display popover. The preference persists across launches.
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
  // Settings state (must be declared before useDaemonSocket to pass as callback)
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

  const [reviewLoopsBySessionId, setReviewLoopsBySessionId] = useState<Record<string, ReviewLoopState>>({});
  const [daemonWorkspaces, setDaemonWorkspaces] = useState<DaemonWorkspace[]>([]);

  // Worktrees state (used by WorktreeCleanupPrompt)
  const [, setWorktrees] = useState<DaemonWorktree[]>([]);

  // Git status state
  const [gitStatus, setGitStatus] = useState<GitStatusUpdate | null>(null);
  const [updateAvailableVersion, setUpdateAvailableVersion] = useState<string | null>(null);
  const [updateReleaseUrl, setUpdateReleaseUrl] = useState<string>(RELEASES_LATEST_WEB);
  const [dismissedUpdateVersion, setDismissedUpdateVersion] = useState<string | null>(() => getDismissedUpdateVersion());
  const installChannel = normalizeInstallChannel(import.meta.env.VITE_INSTALL_CHANNEL);

  const {
    daemonSessions,
    setDaemonSessions,
    setChiefOfStaffDispatches,
    prs,
    setPRs,
    setRepoStates,
    setAuthorStates,
  } = useDaemonStore();

  // Hide loading screen on mount
  useEffect(() => {
    const loadingScreen = document.getElementById('loading-screen');
    if (loadingScreen) {
      loadingScreen.classList.add('hidden');
      // Remove from DOM after transition completes
      setTimeout(() => loadingScreen.remove(), 300);
    }
  }, []);

  // Ensure daemon is running before connecting
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

  // Check latest GitHub release periodically and notify when newer than local app.
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

  // Bridge daemon session-exit events to AppContent's pane-aware close handler.
  // useDaemonSocket lives here in the outer App, but the close logic (worktree
  // cleanup, focus fallback, pane vs session) lives in AppContent, so AppContent
  // registers its handler into this ref and we forward exits through it.
  const sessionExitHandlerRef = useRef<((info: SessionExitInfo) => void) | null>(null);
  const registerSessionExitHandler = useCallback((handler: ((info: SessionExitInfo) => void) | null) => {
    sessionExitHandlerRef.current = handler;
  }, []);
  const handleSessionExited = useCallback((info: SessionExitInfo) => {
    sessionExitHandlerRef.current?.(info);
  }, []);

  // Bumped on every fs_changed event so the Notebook browser's filesystem tree
  // re-lists and the open file reloads (covers agent writes and external edits to
  // any file under the root). The browser reads via the generic fs surface, so
  // fs_changed — not notebook_changed — is its refresh signal.
  const [fsChangeSignal, setFsChangeSignal] = useState(0);
  // Bumped on every notebook_tasks_changed broadcast so an open Tasks panel
  // re-fetches the durable runner's task list (covers any lifecycle transition).
  const [notebookTaskChangeSignal, setNotebookTaskChangeSignal] = useState(0);

  // Connect to daemon WebSocket
  const {
    sendPRAction,
    getScreenSnapshot,
    sendMutePR,
    sendMuteRepo,
    sendMuteAuthor,
    sendMuteWorkspace,
    sendPRVisited,
    sendRefreshPRs,
    sendRegisterWorkspace,
    sendUnregisterWorkspace,
    sendRenameSession,
    sendRenameWorkspace,
    sendSetChiefOfStaff,
    sendWakeDispatchAgent,
    sendUnregisterSession,
    sendSetSetting,
    sendCreateWorktree,
    sendDeleteWorktree,
    sendListPlugins,
    sendInstallPlugin,
    sendRemovePlugin,
    sendSetPluginPriority,
    sendAddEndpoint,
    sendUpdateEndpoint,
    sendRemoveEndpoint,
    sendSetEndpointRemoteWeb,
    sendBootstrapEndpoint,
    sendListWorkspaceContexts,
    sendFsList,
    sendFsRead,
    sendFsWrite,
    sendFsExists,
    sendNotebookTaskList,
    sendNotebookTaskRetry,
    sendNotebookBacklinks,
    sendNotebookToChief,
    sendGetRecentLocations,
    sendBrowseDirectory,
    sendInspectPath,
    sendCreateWorktreeFromBranch,
    sendFetchRemotes,
    sendFetchPRDetails,
    sendEnsureRepo,
    sendSubscribeGitStatus,
    sendUnsubscribeGitStatus,
    sendSessionSelected,
    sendWorkspaceSelected,
    sendSessionVisualized,
    sendWorkspaceAddSessionPane,
    sendWorkspaceClosePane,
    sendWorkspaceSetSplitRatio,
    sendWorkspaceUndockTile,
    sendWorkspaceUpdateTile,
    sendWorkspaceMoveLeaf,
    sendWorkspaceMoveLeafToWorkspace,
    sendWorkspaceMoveLeafToNewWorkspace,
    sendSetWorkspaceRank,
    tileContents,
    requestTileContent,
    sendRuntimeInput,
    isRuntimeAttached,
    sendGetFileDiff,
    sendGetBranchDiffFiles,
    getRepoInfo,
    getReviewLoopRun,
    getReviewLoopState,
    listWorkflowRuns,
    getWorkflowRun,
    getReviewState,
    markFileViewed,
    sendAddComment,
    sendUpdateComment,
    sendResolveComment,
    sendDeleteComment,
    sendGetComments,
    sendStartReviewLoop,
    sendStopReviewLoop,
    answerReviewLoop,
    setReviewLoopIterationLimit,
    connectionError,
    hasReceivedInitialState,
    rateLimit,
    warnings,
    clearWarnings,
  } = useDaemonSocket({
    onSessionsUpdate: setDaemonSessions,
    // The daemon tags each fs_changed with an origin ("ui"/"agent"/"external"), but
    // every origin is treated the same here: bump a signal so an open browser
    // re-lists its tree and reloads the open file. Covers any file under the root.
    onFsChanged: () => setFsChangeSignal((n) => n + 1),
    // A task lifecycle transition broadcast bumps the signal so an open Tasks panel
    // refetches the runner's list (the broadcast itself is payload-free).
    onNotebookTasksChanged: () => setNotebookTaskChangeSignal((n) => n + 1),
    onChiefOfStaffDispatchesUpdate: setChiefOfStaffDispatches,
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
    onReviewLoopUpdate: (state) => {
      if (!state) return;
      setReviewLoopsBySessionId(prev => ({ ...prev, [state.source_session_id]: state }));
    },
    onSessionExited: handleSessionExited,
  });

  const setReviewLoopStateForSession = useCallback((sessionId: string, state: ReviewLoopState | null) => {
    setReviewLoopsBySessionId(prev => {
      if (!state) {
        const next = { ...prev };
        delete next[sessionId];
        return next;
      }
      return { ...prev, [sessionId]: state };
    });
  }, []);

  // Memoize clearGitStatus to prevent subscription effect from re-running
  const clearGitStatus = useCallback(() => setGitStatus(null), []);

  // Wrap the app content with SettingsProvider so useUIScale can access settings.
  // KeybindingsProvider (inside it) syncs shortcut overrides into the resolver.
  return (
    <SettingsProvider settings={settings} setSetting={sendSetSetting}>
      <KeybindingsProvider>
      <AppContent
        daemonSessions={daemonSessions}
        daemonWorkspaces={daemonWorkspaces}
        prs={prs}
        daemonEndpoints={daemonEndpoints}
        daemonPlugins={daemonPlugins}
        daemonPluginIssues={daemonPluginIssues}
        daemonGitHubHosts={daemonGitHubHosts}
        settings={settings}
        gitStatus={gitStatus}
        reviewLoopsBySessionId={reviewLoopsBySessionId}
        connectionError={connectionError}
        hasReceivedInitialState={hasReceivedInitialState}
        rateLimit={rateLimit}
        warnings={warnings}
        clearWarnings={clearWarnings}
        updateAvailableVersion={updateAvailableVersion}
        onOpenLatestRelease={handleOpenLatestRelease}
        onDismissLatestRelease={handleDismissLatestRelease}
        settingError={settingError}
        clearSettingError={() => setSettingError(null)}
        // Daemon socket functions
        sendPRAction={sendPRAction}
        getScreenSnapshot={getScreenSnapshot}
        sendMutePR={sendMutePR}
        sendMuteRepo={sendMuteRepo}
        sendMuteAuthor={sendMuteAuthor}
        sendMuteWorkspace={sendMuteWorkspace}
        sendPRVisited={sendPRVisited}
        sendRefreshPRs={sendRefreshPRs}
        sendRegisterWorkspace={sendRegisterWorkspace}
        sendUnregisterWorkspace={sendUnregisterWorkspace}
        sendRenameSession={sendRenameSession}
        sendRenameWorkspace={sendRenameWorkspace}
        sendSetChiefOfStaff={sendSetChiefOfStaff}
        sendWakeDispatchAgent={sendWakeDispatchAgent}
        sendUnregisterSession={sendUnregisterSession}
        sendSetSetting={sendSetSetting}
        sendCreateWorktree={sendCreateWorktree}
        sendDeleteWorktree={sendDeleteWorktree}
        sendListPlugins={sendListPlugins}
        sendInstallPlugin={sendInstallPlugin}
        sendRemovePlugin={sendRemovePlugin}
        sendSetPluginPriority={sendSetPluginPriority}
        sendAddEndpoint={sendAddEndpoint}
        sendUpdateEndpoint={sendUpdateEndpoint}
        sendRemoveEndpoint={sendRemoveEndpoint}
        sendSetEndpointRemoteWeb={sendSetEndpointRemoteWeb}
        sendBootstrapEndpoint={sendBootstrapEndpoint}
        sendListWorkspaceContexts={sendListWorkspaceContexts}
        sendFsList={sendFsList}
        sendFsRead={sendFsRead}
        sendFsWrite={sendFsWrite}
        sendFsExists={sendFsExists}
        sendNotebookTaskList={sendNotebookTaskList}
        sendNotebookTaskRetry={sendNotebookTaskRetry}
        sendNotebookBacklinks={sendNotebookBacklinks}
        sendNotebookToChief={sendNotebookToChief}
        fsChangeSignal={fsChangeSignal}
        notebookTaskChangeSignal={notebookTaskChangeSignal}
        sendGetRecentLocations={sendGetRecentLocations}
        sendBrowseDirectory={sendBrowseDirectory}
        sendInspectPath={sendInspectPath}
        sendCreateWorktreeFromBranch={sendCreateWorktreeFromBranch}
        sendFetchRemotes={sendFetchRemotes}
        sendFetchPRDetails={sendFetchPRDetails}
        sendEnsureRepo={sendEnsureRepo}
        sendSubscribeGitStatus={sendSubscribeGitStatus}
        sendUnsubscribeGitStatus={sendUnsubscribeGitStatus}
        sendSessionSelected={sendSessionSelected}
        sendWorkspaceSelected={sendWorkspaceSelected}
        sendSessionVisualized={sendSessionVisualized}
        sendWorkspaceAddSessionPane={sendWorkspaceAddSessionPane}
        sendWorkspaceClosePane={sendWorkspaceClosePane}
        sendWorkspaceSetSplitRatio={sendWorkspaceSetSplitRatio}
        sendWorkspaceUndockTile={sendWorkspaceUndockTile}
        sendWorkspaceUpdateTile={sendWorkspaceUpdateTile}
        sendWorkspaceMoveLeaf={sendWorkspaceMoveLeaf}
        sendWorkspaceMoveLeafToWorkspace={sendWorkspaceMoveLeafToWorkspace}
        sendWorkspaceMoveLeafToNewWorkspace={sendWorkspaceMoveLeafToNewWorkspace}
        sendSetWorkspaceRank={sendSetWorkspaceRank}
        tileContents={tileContents}
        requestTileContent={requestTileContent}
        sendRuntimeInput={sendRuntimeInput}
        isRuntimeAttached={isRuntimeAttached}
        sendGetFileDiff={sendGetFileDiff}
        sendGetBranchDiffFiles={sendGetBranchDiffFiles}
        getRepoInfo={getRepoInfo}
        getReviewLoopRun={getReviewLoopRun}
        getReviewLoopState={getReviewLoopState}
        listWorkflowRuns={listWorkflowRuns}
        getWorkflowRun={getWorkflowRun}
        getReviewState={getReviewState}
        markFileViewed={markFileViewed}
        sendAddComment={sendAddComment}
        sendUpdateComment={sendUpdateComment}
        sendResolveComment={sendResolveComment}
        sendDeleteComment={sendDeleteComment}
        sendGetComments={sendGetComments}
        sendStartReviewLoop={sendStartReviewLoop}
        sendStopReviewLoop={sendStopReviewLoop}
        answerReviewLoop={answerReviewLoop}
        setReviewLoopIterationLimit={setReviewLoopIterationLimit}
        setReviewLoopStateForSession={setReviewLoopStateForSession}
        clearGitStatus={clearGitStatus}
        registerSessionExitHandler={registerSessionExitHandler}
      />
      </KeybindingsProvider>
    </SettingsProvider>
  );
}

// Props interface for AppContent - receives daemon state and functions from App
interface AppContentProps {
  daemonSessions: DaemonSession[];
  daemonWorkspaces: DaemonWorkspace[];
  prs: DaemonPR[];
  daemonEndpoints: DaemonEndpoint[];
  daemonPlugins: DaemonPlugin[];
  daemonPluginIssues: DaemonPluginIssue[];
  daemonGitHubHosts: string[];
  settings: Record<string, string>;
  gitStatus: GitStatusUpdate | null;
  reviewLoopsBySessionId: Record<string, ReviewLoopState>;
  connectionError: string | null;
  hasReceivedInitialState: boolean;
  rateLimit: import('./hooks/useDaemonSocket').RateLimitState | null;
  warnings: DaemonWarning[];
  clearWarnings: () => void;
  updateAvailableVersion: string | null;
  onOpenLatestRelease: () => Promise<void>;
  onDismissLatestRelease: () => void;
  settingError: string | null;
  clearSettingError: () => void;
  // All the daemon socket functions
  sendPRAction: ReturnType<typeof useDaemonSocket>['sendPRAction'];
  getScreenSnapshot: ReturnType<typeof useDaemonSocket>['getScreenSnapshot'];
  sendMutePR: ReturnType<typeof useDaemonSocket>['sendMutePR'];
  sendMuteRepo: ReturnType<typeof useDaemonSocket>['sendMuteRepo'];
  sendMuteAuthor: ReturnType<typeof useDaemonSocket>['sendMuteAuthor'];
  sendMuteWorkspace: ReturnType<typeof useDaemonSocket>['sendMuteWorkspace'];
  sendPRVisited: ReturnType<typeof useDaemonSocket>['sendPRVisited'];
  sendRefreshPRs: ReturnType<typeof useDaemonSocket>['sendRefreshPRs'];
  sendRegisterWorkspace: ReturnType<typeof useDaemonSocket>['sendRegisterWorkspace'];
  sendUnregisterWorkspace: ReturnType<typeof useDaemonSocket>['sendUnregisterWorkspace'];
  sendRenameSession: ReturnType<typeof useDaemonSocket>['sendRenameSession'];
  sendRenameWorkspace: ReturnType<typeof useDaemonSocket>['sendRenameWorkspace'];
  sendSetChiefOfStaff: ReturnType<typeof useDaemonSocket>['sendSetChiefOfStaff'];
  sendWakeDispatchAgent: ReturnType<typeof useDaemonSocket>['sendWakeDispatchAgent'];
  sendUnregisterSession: ReturnType<typeof useDaemonSocket>['sendUnregisterSession'];
  sendSetSetting: ReturnType<typeof useDaemonSocket>['sendSetSetting'];
  sendCreateWorktree: ReturnType<typeof useDaemonSocket>['sendCreateWorktree'];
  sendDeleteWorktree: ReturnType<typeof useDaemonSocket>['sendDeleteWorktree'];
  sendListPlugins: ReturnType<typeof useDaemonSocket>['sendListPlugins'];
  sendInstallPlugin: ReturnType<typeof useDaemonSocket>['sendInstallPlugin'];
  sendRemovePlugin: ReturnType<typeof useDaemonSocket>['sendRemovePlugin'];
  sendSetPluginPriority: ReturnType<typeof useDaemonSocket>['sendSetPluginPriority'];
  sendAddEndpoint: ReturnType<typeof useDaemonSocket>['sendAddEndpoint'];
  sendUpdateEndpoint: ReturnType<typeof useDaemonSocket>['sendUpdateEndpoint'];
  sendRemoveEndpoint: ReturnType<typeof useDaemonSocket>['sendRemoveEndpoint'];
  sendSetEndpointRemoteWeb: ReturnType<typeof useDaemonSocket>['sendSetEndpointRemoteWeb'];
  sendBootstrapEndpoint: ReturnType<typeof useDaemonSocket>['sendBootstrapEndpoint'];
  sendListWorkspaceContexts: ReturnType<typeof useDaemonSocket>['sendListWorkspaceContexts'];
  sendFsList: ReturnType<typeof useDaemonSocket>['sendFsList'];
  sendFsRead: ReturnType<typeof useDaemonSocket>['sendFsRead'];
  sendFsWrite: ReturnType<typeof useDaemonSocket>['sendFsWrite'];
  sendFsExists: ReturnType<typeof useDaemonSocket>['sendFsExists'];
  sendNotebookTaskList: ReturnType<typeof useDaemonSocket>['sendNotebookTaskList'];
  sendNotebookTaskRetry: ReturnType<typeof useDaemonSocket>['sendNotebookTaskRetry'];
  sendNotebookBacklinks: ReturnType<typeof useDaemonSocket>['sendNotebookBacklinks'];
  sendNotebookToChief: ReturnType<typeof useDaemonSocket>['sendNotebookToChief'];
  fsChangeSignal: number;
  notebookTaskChangeSignal: number;
  sendGetRecentLocations: ReturnType<typeof useDaemonSocket>['sendGetRecentLocations'];
  sendBrowseDirectory: ReturnType<typeof useDaemonSocket>['sendBrowseDirectory'];
  sendInspectPath: ReturnType<typeof useDaemonSocket>['sendInspectPath'];
  sendCreateWorktreeFromBranch: ReturnType<typeof useDaemonSocket>['sendCreateWorktreeFromBranch'];
  sendFetchRemotes: ReturnType<typeof useDaemonSocket>['sendFetchRemotes'];
  sendFetchPRDetails: ReturnType<typeof useDaemonSocket>['sendFetchPRDetails'];
  sendEnsureRepo: ReturnType<typeof useDaemonSocket>['sendEnsureRepo'];
  sendSubscribeGitStatus: ReturnType<typeof useDaemonSocket>['sendSubscribeGitStatus'];
  sendUnsubscribeGitStatus: ReturnType<typeof useDaemonSocket>['sendUnsubscribeGitStatus'];
  sendSessionSelected: ReturnType<typeof useDaemonSocket>['sendSessionSelected'];
  sendWorkspaceSelected: ReturnType<typeof useDaemonSocket>['sendWorkspaceSelected'];
  sendSessionVisualized: ReturnType<typeof useDaemonSocket>['sendSessionVisualized'];
  sendWorkspaceAddSessionPane: ReturnType<typeof useDaemonSocket>['sendWorkspaceAddSessionPane'];
  sendWorkspaceClosePane: ReturnType<typeof useDaemonSocket>['sendWorkspaceClosePane'];
  sendWorkspaceSetSplitRatio: ReturnType<typeof useDaemonSocket>['sendWorkspaceSetSplitRatio'];
  sendWorkspaceUndockTile: ReturnType<typeof useDaemonSocket>['sendWorkspaceUndockTile'];
  sendWorkspaceUpdateTile: ReturnType<typeof useDaemonSocket>['sendWorkspaceUpdateTile'];
  sendWorkspaceMoveLeaf: ReturnType<typeof useDaemonSocket>['sendWorkspaceMoveLeaf'];
  sendWorkspaceMoveLeafToWorkspace: ReturnType<typeof useDaemonSocket>['sendWorkspaceMoveLeafToWorkspace'];
  sendWorkspaceMoveLeafToNewWorkspace: ReturnType<typeof useDaemonSocket>['sendWorkspaceMoveLeafToNewWorkspace'];
  sendSetWorkspaceRank: ReturnType<typeof useDaemonSocket>['sendSetWorkspaceRank'];
  tileContents: ReturnType<typeof useDaemonSocket>['tileContents'];
  requestTileContent: ReturnType<typeof useDaemonSocket>['requestTileContent'];
  sendRuntimeInput: ReturnType<typeof useDaemonSocket>['sendRuntimeInput'];
  isRuntimeAttached: ReturnType<typeof useDaemonSocket>['isRuntimeAttached'];
  sendGetFileDiff: ReturnType<typeof useDaemonSocket>['sendGetFileDiff'];
  sendGetBranchDiffFiles: ReturnType<typeof useDaemonSocket>['sendGetBranchDiffFiles'];
  getRepoInfo: ReturnType<typeof useDaemonSocket>['getRepoInfo'];
  getReviewLoopRun: ReturnType<typeof useDaemonSocket>['getReviewLoopRun'];
  getReviewLoopState: ReturnType<typeof useDaemonSocket>['getReviewLoopState'];
  listWorkflowRuns: ReturnType<typeof useDaemonSocket>['listWorkflowRuns'];
  getWorkflowRun: ReturnType<typeof useDaemonSocket>['getWorkflowRun'];
  getReviewState: ReturnType<typeof useDaemonSocket>['getReviewState'];
  markFileViewed: ReturnType<typeof useDaemonSocket>['markFileViewed'];
  sendAddComment: ReturnType<typeof useDaemonSocket>['sendAddComment'];
  sendUpdateComment: ReturnType<typeof useDaemonSocket>['sendUpdateComment'];
  sendResolveComment: ReturnType<typeof useDaemonSocket>['sendResolveComment'];
  sendDeleteComment: ReturnType<typeof useDaemonSocket>['sendDeleteComment'];
  sendGetComments: ReturnType<typeof useDaemonSocket>['sendGetComments'];
  sendStartReviewLoop: ReturnType<typeof useDaemonSocket>['sendStartReviewLoop'];
  sendStopReviewLoop: ReturnType<typeof useDaemonSocket>['sendStopReviewLoop'];
  answerReviewLoop: ReturnType<typeof useDaemonSocket>['answerReviewLoop'];
  setReviewLoopIterationLimit: ReturnType<typeof useDaemonSocket>['setReviewLoopIterationLimit'];
  setReviewLoopStateForSession: (sessionId: string, state: ReviewLoopState | null) => void;
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
  gitStatus,
  reviewLoopsBySessionId,
  connectionError,
  hasReceivedInitialState,
  rateLimit,
  warnings,
  clearWarnings,
  updateAvailableVersion,
  onOpenLatestRelease,
  onDismissLatestRelease,
  settingError,
  clearSettingError,
  sendPRAction,
  getScreenSnapshot,
  sendMutePR,
  sendMuteRepo,
  sendMuteAuthor,
  sendMuteWorkspace,
  sendPRVisited,
  sendRefreshPRs,
  sendRegisterWorkspace,
  sendUnregisterWorkspace,
  sendRenameSession,
  sendRenameWorkspace,
  sendSetChiefOfStaff,
  sendWakeDispatchAgent,
  sendUnregisterSession,
  sendSetSetting,
  sendCreateWorktree,
  sendDeleteWorktree,
  sendListPlugins,
  sendInstallPlugin,
  sendRemovePlugin,
  sendSetPluginPriority,
  sendAddEndpoint,
  sendUpdateEndpoint,
  sendRemoveEndpoint,
  sendSetEndpointRemoteWeb,
  sendBootstrapEndpoint,
  sendListWorkspaceContexts,
  sendFsList,
  sendFsRead,
  sendFsWrite,
  sendFsExists,
  sendNotebookTaskList,
  sendNotebookTaskRetry,
  sendNotebookBacklinks,
  sendNotebookToChief,
  fsChangeSignal,
  notebookTaskChangeSignal,
  sendGetRecentLocations,
  sendBrowseDirectory,
sendInspectPath,
    sendCreateWorktreeFromBranch,
    sendFetchRemotes,
sendFetchPRDetails,
  sendEnsureRepo,
  sendSubscribeGitStatus,
  sendUnsubscribeGitStatus,
  sendSessionSelected,
  sendWorkspaceSelected,
  sendSessionVisualized,
  sendWorkspaceAddSessionPane,
  sendWorkspaceClosePane,
  sendWorkspaceSetSplitRatio,
  sendWorkspaceUndockTile,
  sendWorkspaceUpdateTile,
  sendWorkspaceMoveLeaf,
  sendWorkspaceMoveLeafToWorkspace,
  sendWorkspaceMoveLeafToNewWorkspace,
  sendSetWorkspaceRank,
  tileContents,
  requestTileContent,
  sendRuntimeInput,
  isRuntimeAttached,
  sendGetFileDiff,
  sendGetBranchDiffFiles,
  getRepoInfo,
  getReviewLoopRun,
  getReviewLoopState,
  listWorkflowRuns,
  getWorkflowRun,
  getReviewState,
  markFileViewed,
  sendAddComment,
  sendUpdateComment,
  sendResolveComment,
  sendDeleteComment,
  sendGetComments,
  sendStartReviewLoop,
  sendStopReviewLoop,
  answerReviewLoop,
  setReviewLoopIterationLimit,
  setReviewLoopStateForSession,
  clearGitStatus,
  registerSessionExitHandler,
}: AppContentProps) {
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

  // Explicit selection of a sessionless (tile-only) workspace. The selection
  // model is otherwise session-centric — the active workspace is derived from
  // the active session — but a tile-only workspace has no session to activate
  // through, so it needs its own selection anchor. Cleared whenever a session is
  // selected; the selection controller also ignores it once the workspace gains
  // sessions or disappears.
  const [selectedSessionlessWorkspaceId, setSelectedSessionlessWorkspaceId] = useState<string | null>(null);
  const [selectedTile, setSelectedTile] = useState<{ workspaceId: string; tileId: string } | null>(null);
  // handleSelectWorkspace is defined far below (it depends on the workspace view
  // models); the automation bridge above it reaches the live handler through
  // this ref so test scenarios can select a workspace by id.
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
  ) => {
    const sessionId = providedSessionId || crypto.randomUUID();
    const workspaceId = `workspace-${sessionId}`;
    const paneId = paneIdForSession(sessionId);
    let localCreated = false;
    let paneAdded = false;
    try {
      await sendRegisterWorkspace(workspaceId, label, cwd, endpointId);
      const createdSessionId = await createSession(label, cwd, sessionId, agent, endpointId, yoloMode, workspaceId);
      localCreated = true;
      await sendWorkspaceAddSessionPane(workspaceId, sessionId, label, { paneId });
      paneAdded = true;
      const spawnArgs = takeSessionSpawnArgs(sessionId, 80, 24);
      if (!spawnArgs) {
        throw new Error('Session spawn arguments were not prepared.');
      }
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

  // UI scale for font sizing (Cmd+/Cmd-) - now uses SettingsContext
  const { scale, increaseScale, decreaseScale, resetScale } = useUIScale();
  const terminalFontSize = Math.round(14 * scale);

  // Theme (dark/light/system)
  const { preference: themePreference, resolved: resolvedTheme, setTheme } = useTheme();
  const keybindings = useKeybindings();

  // Settings modal (lifted from Dashboard for Cmd+, access)
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [shortcutsOpen, setShortcutsOpen] = useState(false);
  const [shortcutEditorOpen, setShortcutEditorOpen] = useState(false);
  const [actionMenuOpen, setActionMenuOpen] = useState(false);
  const [workspaceContextsOpen, setWorkspaceContextsOpen] = useState(false);
  const [notebookOpen, setNotebookOpen] = useState(false);
  const [workspaceContextsLoading, setWorkspaceContextsLoading] = useState(false);
  const [workspaceContextsError, setWorkspaceContextsError] = useState<string | null>(null);
  const [workspaceContexts, setWorkspaceContexts] = useState<Awaited<ReturnType<typeof sendListWorkspaceContexts>>>([]);
  const whatsNew = useWhatsNew();
  const { repoStates, authorStates } = useDaemonStore();
  const mutedRepos = useMemo(() =>
    repoStates.filter(r => r.muted).map(r => r.repo),
    [repoStates],
  );
  const mutedAuthors = useMemo(() =>
    authorStates.filter(a => a.muted).map(a => a.author),
    [authorStates],
  );
  // Track PR refresh state for progress indicator
  const [isRefreshingPRs, setIsRefreshingPRs] = useState(false);
  const [refreshError, setRefreshError] = useState<string | null>(null);
  const [branchDiffFiles, setBranchDiffFiles] = useState<BranchDiffFile[]>([]);
  const [branchDiffBaseRef, setBranchDiffBaseRef] = useState('');
  const [branchDiffError, setBranchDiffError] = useState<string | null>(null);
  const [branchDiffLoaded, setBranchDiffLoaded] = useState(false);
  const [branchDiffLoading, setBranchDiffLoading] = useState(false);
  const [branchDiffRefreshing, setBranchDiffRefreshing] = useState(false);

  // Worktree cleanup prompt state
  const cleanupRequestIdRef = useRef(0);
  const [closedWorktree, setClosedWorktree] = useState<{ id: number; path: string; branch?: string } | null>(null);
  const [worktreeCleanupState, setWorktreeCleanupState] = useState<{
    requestId: number | null;
    isDeleting: boolean;
    error: string | null;
    forceable: boolean;
  }>({ requestId: null, isDeleting: false, error: null, forceable: false });
  const [pendingSessionClose, setPendingSessionClose] = useState<{
    id: string;
    label: string;
    splitCount: number;
  } | null>(null);
  const [alwaysKeepWorktrees, setAlwaysKeepWorktrees] = useState(false);

  // Owning the diff panel's selected file here keeps external triggers
  // (ChangesPanel clicks, shortcut open) and internal navigation using
  // the same setter, so re-clicking the same path always lands on it.
  const [diffSelectedFilePath, setDiffSelectedFilePath] = useState<string | null>(null);
  const agentAvailability = useMemo(() => getAgentAvailability(settings), [settings]);
  const hasAvailableAgents = useMemo(
    () => hasAnyAvailableAgents(agentAvailability),
    [agentAvailability],
  );

  useEffect(() => {
    setLauncherConfig({
      executables: getAgentExecutableSettings(settings),
    });
  }, [settings, setLauncherConfig]);

  // Keep local UI sessions in sync with daemon's canonical session list.
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

  // Refresh PRs with proper async handling
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

  // Track processed deep links to avoid duplicates (persists across re-renders)
  const processedDeepLinks = useRef(new Set<string>());

  // Handle a deep-link URL (used by both cold start and runtime handlers)
  const handleDeepLinkUrl = useCallback((urlStr: string) => {
    // Deduplicate: only process each unique URL once
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
          // Check if session for this cwd already exists (read current state)
          const currentSessions = useSessionStore.getState().sessions;
          const existingSession = currentSessions.find((s) => s.cwd === cwd);
          if (existingSession) {
            // Just activate the existing session
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

  // Handle cold-start deep links (app opened via URL when not running)
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

  // Handle deep links while app is running
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

  // Enrich local sessions with daemon state (working/waiting from hooks)
  // Match by session ID (UUID) - not directory - to handle multiple sessions per directory
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
    const reviewLoop = reviewLoopsBySessionId[s.id];
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
      recoverable: daemonSession?.recoverable ?? false,
      reviewLoopStatus: reviewLoop?.status,
      chiefOfStaff: daemonSession?.chief_of_staff ?? false,
    };
  });

  const visibleEnrichedSessions = filterSessionsRepresentedInWorkspaceLayouts(daemonWorkspaces, enrichedLocalSessions);

  // The Notebook top-bar chief pulse: undefined when no chief-of-staff session exists
  // (indicator hidden), else true while it is working. Derived locally from sessions
  // already in hand — no extra socket call or threaded daemon function.
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
    resetSessionPaneTerminal,
    injectSessionPaneBytes,
    injectSessionPaneBase64,
    drainSessionPaneTerminal,
  } = useSessionWorkspaceController(sessions, activeSessionId);

  useEffect(() => {
    void connect();
  }, [connect]);

  type DockPanelId = 'diff' | 'reviewLoop' | 'workflowRun' | 'attention' | 'diffDetail';

  // Muted section expansion (controlled by Dashboard click)
  const [sidebarMutedExpanded, setSidebarMutedExpanded] = useState(false);

  // View state management
  const [view, setView] = useState<'dashboard' | 'session' | 'grid'>('dashboard');
  const [dockState, setDockState] = useState<{
    openPanels: Record<DockPanelId, boolean>;
    stack: DockPanelId[];
  }>({
    openPanels: {
        diff: false,
        reviewLoop: false,
        workflowRun: false,
        attention: false,
        diffDetail: false,
    },
    stack: ['diff'],
  });
  const dockPanelCloseTimersRef = useRef<Partial<Record<DockPanelId, number>>>({});
  const gitStatusSubscribedDirRef = useRef<string | null>(null);
  const activeSessionVisibleSinceRef = useRef<{ id: string; at: number } | null>(null);
  const pendingSessionVisualizedRef = useRef<{ key: string | null; timeoutId: number | null }>({
    key: null,
    timeoutId: null,
  });

  // When activeSessionId changes, update view
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

  // Track when the currently-selected session became visible.
  useEffect(() => {
    if (view !== 'session' || !activeSessionId) {
      activeSessionVisibleSinceRef.current = null;
      return;
    }
    const current = activeSessionVisibleSinceRef.current;
    if (!current || current.id !== activeSessionId) {
      activeSessionVisibleSinceRef.current = { id: activeSessionId, at: Date.now() };
    }
  }, [activeSessionId, view]);

  // For long runs, defer classification until the user has visualized the session long enough.
  useEffect(() => {
    const tracker = pendingSessionVisualizedRef.current;
    const activeSession =
      view === 'session' && activeSessionId
        ? daemonSessions.find((session) => session.id === activeSessionId)
        : undefined;
    const needsReview = Boolean(activeSession?.needs_review_after_long_run);
    const key = needsReview && activeSession ? `${activeSession.id}:${activeSession.state_updated_at}` : null;

    if (tracker.key === key) {
      return;
    }

    if (tracker.timeoutId !== null) {
      clearTimeout(tracker.timeoutId);
      tracker.timeoutId = null;
    }
    tracker.key = key;

    if (!activeSession || !needsReview || !key) {
      return;
    }

    let delayMs = 5000;
    const visibleSince = activeSessionVisibleSinceRef.current;
    const stateUpdatedAtMs = Date.parse(activeSession.state_updated_at);
    const userAlreadyViewingWhenFinished =
      visibleSince?.id === activeSession.id &&
      Number.isFinite(stateUpdatedAtMs) &&
      visibleSince.at <= stateUpdatedAtMs;
    if (userAlreadyViewingWhenFinished) {
      delayMs = 0;
    }

    tracker.timeoutId = window.setTimeout(() => {
      sendSessionVisualized(activeSession.id);
      tracker.timeoutId = null;
    }, delayMs);
  }, [activeSessionId, daemonSessions, sendSessionVisualized, view]);

  useEffect(() => {
    return () => {
      const tracker = pendingSessionVisualizedRef.current;
      if (tracker.timeoutId !== null) {
        clearTimeout(tracker.timeoutId);
        tracker.timeoutId = null;
      }
      tracker.key = null;
    };
  }, []);

  // Subscribe to git status for active session
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

  // Function to go to dashboard
  const goToDashboard = useCallback(() => {
    setActiveSession(null);
    setView('dashboard');
  }, [setActiveSession]);

  // Cmd+G toggles the global grid view on/off; leaving grid returns to wherever
  // the user was (a session if one is active, otherwise the dashboard).
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

  // Sidebar collapse state
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);

  const toggleSidebarCollapse = useCallback(() => {
    setSidebarCollapsed((prev) => !prev);
  }, []);


  // Auto-collapse sidebar when no sessions, auto-expand when first session is created
  const prevSessionCountRef = useRef(sessions.length);
  useEffect(() => {
    const prevCount = prevSessionCountRef.current;
    const currentCount = sessions.length;
    prevSessionCountRef.current = currentCount;

    if (currentCount === 0) {
      // No sessions - collapse
      setSidebarCollapsed(true);
    } else if (prevCount === 0 && currentCount > 0) {
      // First session created - expand
      setSidebarCollapsed(false);
    }
    // Otherwise, respect user's manual toggle
  }, [sessions.length]);

  // Location picker state management
  const [locationPickerOpen, setLocationPickerOpen] = useState(false);
  const [locationPickerPurpose, setLocationPickerPurpose] = useState<LocationPickerPurpose>('workspace');
  const [locationPickerSessionDirection, setLocationPickerSessionDirection] = useState<TerminalSplitDirection>('vertical');

  const [zoomModeBySessionId, setZoomModeBySessionId] = useState<Record<string, boolean>>({});
  const { message: errorMessage, showError, clearError } = useErrorToast();
  const [chiefTransferTarget, setChiefTransferTarget] = useState<{
    sessionId: string;
    targetLabel: string;
    currentLabel: string;
  } | null>(null);
  const [chiefTransferSaving, setChiefTransferSaving] = useState(false);

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
      // The shared error toast already contains the daemon error.
    } finally {
      setChiefTransferSaving(false);
    }
  }, [applyChiefOfStaffChange, chiefTransferSaving, chiefTransferTarget]);

  const activeReviewLoopState = useMemo(
    () => (activeSessionId ? reviewLoopsBySessionId[activeSessionId] ?? null : null),
    [activeSessionId, reviewLoopsBySessionId],
  );
  // Read-only workflow runs: the slice is hydrated by useDaemonSocket on the
  // workflow_run_updated broadcast; listWorkflowRuns backfills the active
  // session's existing runs on selection (broadcasts only fire on change).
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
  const activeRepoDaemonSession = useMemo(
    () => activeDaemonSession,
    [activeDaemonSession],
  );
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
  const activeReviewLoopAvailable = useMemo(
    () => Boolean(activeLocalSession && activeDaemonSession),
    [activeDaemonSession, activeLocalSession],
  );
  const openDockPanels = dockState.openPanels;
  const dockPanelStack = dockState.stack;
  const diffPanelOpen = openDockPanels.diff;
  const reviewLoopPanelOpen = openDockPanels.reviewLoop;
  const workflowRunPanelOpen = openDockPanels.workflowRun;
  const attentionPanelOpen = openDockPanels.attention;
  const diffDetailPanelOpen = openDockPanels.diffDetail;
  const changesPanelVisible = view === 'session' && diffPanelOpen && Boolean(activeRepoDaemonSession?.directory);
  const blockingOverlayOpen = locationPickerOpen
    || whatsNew.isOpen
    || settingsOpen
    || shortcutsOpen
    || shortcutEditorOpen
    || actionMenuOpen
    || workspaceContextsOpen
    || notebookOpen
    || chiefTransferTarget !== null
    || closedWorktree !== null
    || pendingSessionClose !== null
    || sessionCreationJob !== null
    || openPRLauncherJob !== null;

  const workspaceContextViews = useMemo<WorkspaceContextView[]>(() => {
    const workspacesById = new Map(
      daemonWorkspaces
        .filter((workspace) => !workspace.endpoint_id)
        .map((workspace) => [workspace.id, workspace]),
    );
    const sessionsById = new Map(daemonSessions.map((session) => [session.id, session]));
    return workspaceContexts.map((context) => {
      const workspace = workspacesById.get(context.workspace_id);
      const updatedBy = sessionsById.get(context.updated_by_session_id);
      // 'attn-keeper' is the keeper's compaction-updater sentinel (a PERSISTED
      // value; migration 51 realigned existing rows off the old 'attn-janitor').
      const updatedByLabel = context.updated_by_session_id === 'attn-keeper'
        ? 'Attn Keeper'
        : updatedBy?.label;
      return {
        context,
        title: workspace?.title || context.workspace_id,
        directory: workspace?.directory || 'Workspace no longer registered',
        updatedByLabel,
      };
    });
  }, [daemonSessions, daemonWorkspaces, workspaceContexts]);

  const loadWorkspaceContexts = useCallback(async () => {
    setWorkspaceContextsLoading(true);
    setWorkspaceContextsError(null);
    try {
      setWorkspaceContexts(await sendListWorkspaceContexts());
    } catch (error) {
      setWorkspaceContextsError(error instanceof Error ? error.message : 'Failed to load workspace contexts');
    } finally {
      setWorkspaceContextsLoading(false);
    }
  }, [sendListWorkspaceContexts]);

  const openWorkspaceContextNavigator = useCallback(() => {
    setWorkspaceContextsOpen(true);
    void loadWorkspaceContexts();
  }, [loadWorkspaceContexts]);

  const openNotebookBrowser = useCallback(() => {
    setNotebookOpen(true);
  }, []);

  const actionMenuItems = useMemo<ActionMenuItem[]>(() => [
    {
      id: 'notebook',
      title: 'Browse the Notebook',
      description: 'Read the durable, profile-wide markdown knowledge base',
      keywords: ['notebook', 'knowledge', 'journal', 'decisions', 'chief'],
      icon: <ContextActionIcon />,
      run: openNotebookBrowser,
    },
    {
      id: 'workspace-contexts',
      title: 'Browse workspace contexts',
      description: 'Navigate shared contexts stored on this Mac',
      keywords: ['memory', 'shared', 'agents', 'context'],
      icon: <ContextActionIcon />,
      run: openWorkspaceContextNavigator,
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
      id: 'customize-shortcuts',
      title: 'Customize keyboard shortcuts',
      description: 'Rebind shortcuts and restore defaults',
      keywords: ['keybindings', 'shortcuts', 'keyboard', 'rebind', 'hotkeys'],
      icon: <KeyboardActionIcon />,
      run: () => setShortcutEditorOpen(true),
    },
  ], [openDockPanel, openWorkspaceContextNavigator, openNotebookBrowser]);

  const handleToggleActionMenu = useCallback(() => {
    if (actionMenuOpen) {
      setActionMenuOpen(false);
      return;
    }
    if (settingsOpen || shortcutsOpen || locationPickerOpen || whatsNew.isOpen
      || workspaceContextsOpen || notebookOpen
      || chiefTransferTarget !== null || closedWorktree !== null || pendingSessionClose !== null
      || sessionCreationJob !== null || openPRLauncherJob !== null) {
      return;
    }
    setActionMenuOpen(true);
  }, [
    actionMenuOpen,
    chiefTransferTarget,
    closedWorktree,
    locationPickerOpen,
    openPRLauncherJob,
    pendingSessionClose,
    sessionCreationJob,
    settingsOpen,
    shortcutsOpen,
    whatsNew.isOpen,
    workspaceContextsOpen,
    notebookOpen,
  ]);
  const waitingReviewSessions = useMemo(
    () => sessions
      .map((session) => ({
        sessionId: session.id,
        label: session.label,
        loopState: reviewLoopsBySessionId[session.id],
      }))
      .filter((item): item is { sessionId: string; label: string; loopState: ReviewLoopState } =>
        Boolean(item.loopState && item.loopState.status === 'awaiting_user')
      ),
    [reviewLoopsBySessionId, sessions],
  );

  useEffect(() => {
    if (!activeReviewLoopAvailable) {
      closeDockPanel('reviewLoop');
    }
  }, [activeReviewLoopAvailable, closeDockPanel]);

  useEffect(() => {
    if (activeReviewLoopState?.status === 'awaiting_user' && activeReviewLoopAvailable) {
      openDockPanel('reviewLoop');
    }
  }, [activeReviewLoopAvailable, activeReviewLoopState?.status, openDockPanel]);

  useEffect(() => {
    if (!settingError) {
      return;
    }
    showError(settingError);
    clearSettingError();
  }, [clearSettingError, settingError, showError]);

  useEffect(() => {
    if (!activeSessionId) {
      return;
    }
    let cancelled = false;
    getReviewLoopState(activeSessionId)
      .then((result) => {
        if (cancelled) return;
        setReviewLoopStateForSession(activeSessionId, result.state);
      })
      .catch((error) => {
        if (cancelled) return;
        console.error('[App] Failed to fetch review loop state:', error);
      });
    return () => {
      cancelled = true;
    };
  }, [activeSessionId, getReviewLoopState, setReviewLoopStateForSession]);

  // Backfill the active session's workflow runs into the global slice. The
  // useDaemonSocket workflow_run_updated handler keeps it fresh after this; the
  // store is the single source the read-only WorkflowRunView renders from.
  useEffect(() => {
    if (!activeSessionId) {
      return;
    }
    listWorkflowRuns(activeSessionId).catch((error) => {
      console.error('[App] Failed to list workflow runs:', error);
    });
  }, [activeSessionId, listWorkflowRuns]);

  // Hydrate the run the panel is actually showing. listWorkflowRuns intentionally
  // omits each run's agent_calls (the list is a summary surface), so a run sourced
  // only from that backfill renders "0/0 calls" with no journal. Live runs get
  // their calls from workflow_run_updated broadcasts, but a completed run sees no
  // further broadcasts — after a reload it would stay call-less forever. Fetch the
  // single hydrated run (which includes the journal) the first time the open panel
  // shows a run with no calls; getWorkflowRun upserts it into the store, and live
  // broadcasts own freshness from there.
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

  // No auto-creation - user clicks "+" to start a session

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
      );
      const spawnArgs = takeSessionSpawnArgs(sessionId, 80, 24);
      await sendWorkspaceAddSessionPane(workspaceId, sessionId, label, { paneId: newPaneId, targetPaneId: paneId, direction });
      paneAdded = true;
      if (spawnArgs) {
        await ptySpawn({ args: spawnArgs });
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
    async (path: string, agent: SessionAgent, endpointId?: string, yoloMode = false) => {
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
      // Note: Location is automatically tracked by daemon when session registers
      const folderName = path.split('/').pop() || 'session';
      if (locationPickerPurpose === 'session' && activeLocalSession?.workspaceId) {
        await createSplitSession(selectedAgent, locationPickerSessionDirection, undefined, {
          cwd: path,
          endpointId: endpointId ?? null,
          label: folderName,
          yoloMode,
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
        const sessionId = await createWorkspaceSession(folderName, path, undefined, selectedAgent, endpointId, yoloMode);
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
          });
          setSessionCreationJob((current) => (
            current?.id === jobId ? null : current
          ));
          return;
        }
        const sessionId = await createWorkspaceSession(folderName, worktreePath, undefined, agent, endpointId, yoloMode);
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

  const prepareWorktreeCleanupPrompt = useCallback((session: (typeof enrichedLocalSessions)[number] | undefined) => {
    if (!session?.isWorktree || !session.cwd) {
      return;
    }

    const sessionsInSameDir = enrichedLocalSessions.filter(s => s.cwd === session.cwd);
    const isLastSession = sessionsInSameDir.length === 1;
    if (!isLastSession || alwaysKeepWorktrees) {
      return;
    }

    const cleanupRequestId = cleanupRequestIdRef.current + 1;
    cleanupRequestIdRef.current = cleanupRequestId;
    setWorktreeCleanupState({ requestId: cleanupRequestId, isDeleting: false, error: null, forceable: false });
    setClosedWorktree({ id: cleanupRequestId, path: session.cwd, branch: session.branch });
  }, [alwaysKeepWorktrees, enrichedLocalSessions]);

  const handleCloseSession = useCallback(
    async (id: string) => {
      const session = enrichedLocalSessions.find(s => s.id === id);
      prepareWorktreeCleanupPrompt(session);

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
    [closeSession, daemonSessions, enrichedLocalSessions, prepareWorktreeCleanupPrompt, removeWorkspaceRef, sendUnregisterSession]
  );

  const handleClosePane = useCallback((sessionId: string, paneId: string) => {
    const session = enrichedLocalSessions.find((entry) => entry.id === sessionId);
    prepareWorktreeCleanupPrompt(session);
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
  }, [clearPreparedClosePaneFocus, enrichedLocalSessions, prepareClosePaneFocus, prepareWorktreeCleanupPrompt, sendWorkspaceClosePane, sessions, setActiveSession]);

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

  // Auto-close a session when its process exits cleanly. A clean voluntary exit
  // (code 0, no signal) means the user quit the agent/shell and there's nothing
  // left to do in that pane. Non-zero exits and signal kills (crashes, reloads,
  // explicit closes) keep the pane open so the error and exit code stay visible.
  const handleSessionProcessExit = useCallback((info: SessionExitInfo) => {
    if (info.exitCode !== 0 || info.signal) {
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
    fitSessionActivePane,
    sendRuntimeInput,
    isRuntimeAttached,
    getReviewState,
    addComment: sendAddComment,
    updateComment: sendUpdateComment,
    resolveComment: sendResolveComment,
    deleteComment: sendDeleteComment,
    getComments: sendGetComments,
    startReviewLoop: async (prompt: string, iterationLimit: number, presetId?: string) => {
      if (!activeSessionId) {
        throw new Error('No active session');
      }
      await sendStartReviewLoop(activeSessionId, prompt, iterationLimit, presetId);
    },
    stopReviewLoop: async () => {
      if (!activeSessionId) {
        throw new Error('No active session');
      }
      await sendStopReviewLoop(activeSessionId);
    },
    getReviewLoopState,
    answerReviewLoop,
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

  // Handle opening a PR in a worktree
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
          alert('PR branch information not available.\n\nTry refreshing PRs (⌘R) to fetch branch details.');
          break;
        case 'fetch_pr_details_failed':
          alert(`Failed to fetch PR details.\n\n${errorMsg || 'Try refreshing PRs (⌘R) and try again.'}`);
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

  // Worktree cleanup prompt handlers
  const handleWorktreeKeep = useCallback(() => {
    setWorktreeCleanupState({ requestId: null, isDeleting: false, error: null, forceable: false });
    setClosedWorktree(null);
  }, []);

  const handleWorktreeDelete = useCallback(async () => {
    if (
      !closedWorktree
      || worktreeCleanupState.requestId !== closedWorktree.id
      || worktreeCleanupState.isDeleting
    ) {
      return;
    }
    const deleteTarget = closedWorktree;
    const force = worktreeCleanupState.forceable;
    setWorktreeCleanupState((current) => (
      current.requestId === deleteTarget.id
        ? { requestId: deleteTarget.id, isDeleting: true, error: null, forceable: false }
        : current
    ));
    try {
      if (force) {
        await sendDeleteWorktree(deleteTarget.path, undefined, { force: true });
      } else {
        await sendDeleteWorktree(deleteTarget.path);
      }
      // Note: Deleted paths are automatically filtered by daemon on next fetch
      setWorktreeCleanupState((current) => (
        current.requestId === deleteTarget.id
          ? { requestId: null, isDeleting: false, error: null, forceable: false }
          : current
      ));
      setClosedWorktree((current) => (
        current?.id === deleteTarget.id ? null : current
      ));
    } catch (err) {
      const message = err instanceof Error ? err.message : 'Failed to delete worktree';
      console.error('[App] Failed to delete worktree:', err);
      setWorktreeCleanupState((current) => (
        current.requestId === deleteTarget.id
          ? { requestId: deleteTarget.id, isDeleting: false, error: message, forceable: Boolean((err as { forceable?: boolean }).forceable) }
          : current
      ));
    }
  }, [closedWorktree, sendDeleteWorktree, worktreeCleanupState.forceable, worktreeCleanupState.isDeleting, worktreeCleanupState.requestId]);

  const handleWorktreeAlwaysKeep = useCallback(() => {
    setAlwaysKeepWorktrees(true);
    setWorktreeCleanupState({ requestId: null, isDeleting: false, error: null, forceable: false });
    setClosedWorktree(null);
  }, []);

  const workspaceViews = useMemo(
    () => buildWorkspaceViewModels(daemonWorkspaces, visibleEnrichedSessions),
    [daemonWorkspaces, visibleEnrichedSessions],
  );
  const unmutedWorkspaceViews = useMemo(
    () => workspaceViews.filter((workspace) => !workspace.muted && workspace.sessions.length > 0),
    [workspaceViews],
  );
  const mutedWorkspaceViews = useMemo(
    () => workspaceViews.filter((workspace) => workspace.muted && workspace.sessions.length > 0),
    [workspaceViews],
  );
  const unmutedEnrichedSessions = useMemo(
    () => unmutedWorkspaceViews.flatMap((workspace) => workspace.sessions),
    [unmutedWorkspaceViews],
  );

  // Global grid tiles: one per live agent pane across all (unmuted) workspaces,
  // keyed by the PTY runtimeId that grid mode feeds from / routes input to.
  const gridSessionTiles = useMemo<GridSessionTile[]>(() => {
    const result: GridSessionTile[] = [];
    for (const s of unmutedEnrichedSessions) {
      const pane = s.workspace.agents.find((agent) => agent.sessionId === s.id);
      if (!pane) continue;
      const state = s.reviewLoopStatus === 'error'
        ? 'unknown'
        : s.reviewLoopStatus === 'awaiting_user'
          ? 'waiting_input'
          : s.state;
      result.push({
        runtimeId: pane.runtimeId,
        sessionId: s.id,
        title: pane.title,
        state,
        attention: isAttentionSessionState(state),
      });
    }
    return result;
  }, [unmutedEnrichedSessions]);

  // Grid shape: a manual rows×cols picked from the sidebar square-picker, or Auto
  // (a near-square that fits every tile — today's default). Persists across
  // launches. Selecting a shape also opens grid mode (the picker is the launcher).
  const [gridLayout, setGridLayout] = useState<GridLayout>(readGridLayout);
  const handleSelectGridLayout = useCallback((layout: GridLayout) => {
    setGridLayout(layout);
    persistGridLayout(layout);
    setView('grid');
  }, []);

  // Grid membership: every session is on the grid by default; removed (excluded)
  // sessions persist across launches by stable sessionId. Members = all minus
  // excluded; hidden = the excluded ones (surfaced for restore).
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

  // Resolve the chosen layout against the member count: Auto fits everything; a
  // fixed shape shows only the first rows×cols members (extras are off-board until
  // removed or the grid is enlarged). GridView is handed a concrete shape plus the
  // members that fit, so it stays layout-dumb.
  const resolvedGridLayout = useMemo(
    () => resolveGridLayout(gridMembers.length, gridLayout),
    [gridMembers.length, gridLayout],
  );
  const visibleGridTiles = useMemo(
    () => gridMembers.slice(0, resolvedGridLayout.capacity),
    [gridMembers, resolvedGridLayout.capacity],
  );
  const gridOffBoardCount = gridMembers.length - visibleGridTiles.length;

  // Calculate attention count for drawer badge (muted workspaces excluded)
  const waitingLocalSessions = unmutedEnrichedSessions
    .filter((s) => isAttentionSessionState(s.state) || s.reviewLoopStatus === 'awaiting_user' || s.reviewLoopStatus === 'error')
    .map((s) => ({
      ...s,
      state: s.reviewLoopStatus === 'error'
        ? 'unknown'
        : s.reviewLoopStatus === 'awaiting_user'
          ? 'waiting_input'
          : s.state,
    }));
  const { needsAttention: prsNeedingAttention } = usePRsNeedingAttention(prs);
  const attentionCount = waitingLocalSessions.length + prsNeedingAttention.length;

  // Keyboard shortcut handlers
  const handleJumpToWaiting = useCallback(() => {
    const waiting = unmutedEnrichedSessions.find((s) =>
      isAttentionSessionState(s.state) || s.reviewLoopStatus === 'awaiting_user' || s.reviewLoopStatus === 'error'
    );
    if (waiting) {
      handleSelectSession(waiting.id);
    }
  }, [unmutedEnrichedSessions, handleSelectSession]);

  // Sessionless (tile-only) workspaces are revealed via the sidebar display
  // popover; the preference is the single source of truth for every derived list
  // below (sidebar render order, ⌘1–9 order, prev/next navigation) so they stay
  // consistent. They never contribute to unmutedWorkspaceViews, which feeds
  // session/attention counts.
  const [showSessionlessWorkspaces, setShowSessionlessWorkspaces] = useState<boolean>(readShowSessionlessWorkspaces);
  const handleToggleShowSessionlessWorkspaces = useCallback(() => {
    setShowSessionlessWorkspaces((prev) => {
      const next = !prev;
      persistShowSessionlessWorkspaces(next);
      return next;
    });
  }, []);
  const sidebarWorkspaceViews = useMemo(
    () => workspaceViews.filter(
      (workspace) => !workspace.muted && (workspace.sessions.length > 0 || showSessionlessWorkspaces),
    ),
    [workspaceViews, showSessionlessWorkspaces],
  );
  const workspaceSelection = useWorkspaceSelectionController(
    workspaceViews,
    activeSessionId,
    selectedSessionlessWorkspaceId,
  );
  const activeWorkspaceId = workspaceSelection.activeWorkspaceId;
  const activeWorkspaceIdRef = useRef<string | null>(null);
  useEffect(() => {
    activeWorkspaceIdRef.current = activeWorkspaceId;
  }, [activeWorkspaceId]);
  useEffect(() => {
    if (view === 'session' && activeWorkspaceId) {
      sendWorkspaceSelected(activeWorkspaceId);
    }
  }, [activeWorkspaceId, sendWorkspaceSelected, view]);

  // --- Off-screen terminal virtualization (memory) ---
  // Keep only the active workspace + the N most-recently-used workspaces "warm"
  // (terminals mounted). Other workspaces render a placeholder and rehydrate
  // from daemon replay (same_app_remount) when next made visible. N is runtime-
  // configurable (localStorage + window.attnSetWarmWorkspaces); see
  // utils/terminalVirtualization.
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
  // Most-recently-active workspace ids, most-recent-first. Drives the warm set.
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
  // Written synchronously alongside setLeafWorkspaceDrag by handleLeafDragStart
  // and handleLeafDragEnd so the drop handlers (which read .current at event time)
  // always see the latest drag without a mirror effect.
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

  // Renderable terminal state for tile-only workspaces, built straight from the
  // daemon's broadcast layout (which keeps the docked tile after the last
  // terminal closes). The session-derived path can't produce this — there is no
  // session to carry the layout — so the render loop falls back to this map for
  // any workspace whose layout has tiles and zero agent panes.
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

  // Markdown tiles are daemon-owned docked tiles opened via `attn open <path>`
  // (and re-dockable by dragging). There is no empty "show tile" toggle: a
  // tile only exists once it points at a real file.

  // Use workspace order so ⌘1-9 and prev/next match the top-level sidebar rows.
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
      // Tile-only workspace: activate it by id (it has no session to route
      // through) and surface the terminal view so its docked layout renders.
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

  // Drop the dragged leaf onto the "New workspace" zone at the foot of the list:
  // split it out into a fresh workspace. The daemon registers the workspace (rank
  // seeded to the end of the order), moves the leaf into it, and tears down the
  // source if it ends up empty, then broadcasts the authoritative layout — so this
  // is fire-and-forget like the merge path.
  const handleNewWorkspaceDrop = useCallback(() => {
    const drag = leafWorkspaceDragRef.current;
    if (!drag) {
      return;
    }
    clearWorkspaceDragHoverTimer();
    setDragHoverWorkspaceId(null);
    void sendWorkspaceMoveLeafToNewWorkspace(drag.sourceWorkspaceId, drag.leafId, SIDEBAR_LEAF_DROP_PLACEMENT).catch(() => {});
  }, [clearWorkspaceDragHoverTimer, sendWorkspaceMoveLeafToNewWorkspace]);

  // Persist a workspace reorder. The Sidebar computes the drop's neighbour ids
  // from the seam; the daemon derives the fractional rank key and broadcasts the
  // authoritative order, so this is fire-and-forget (a rejected/timed-out write
  // just leaves the existing order in place).
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
  }, [activeSessionId, getActivePaneIdForSession, handleClosePane, handleRequestCloseSession, sessions]);

  const handleReloadSession = useCallback((id: string) => {
    const session = sessions.find((entry) => entry.id === id);
    const paneId = session?.workspace.agents.find((pane) => pane.sessionId === id)?.id;
    const size = paneId ? getPaneSize(id, paneId) || undefined : undefined;
    void reloadSession(id, size).catch((error) => {
      const message = error instanceof Error ? error.message : String(error);
      showError(`Failed to reload session: ${message}`);
    });
  }, [getPaneSize, reloadSession, sessions, showError]);

  // Open file in diff detail panel
  const handleFileSelect = useCallback((path: string, _staged: boolean) => {
    setDiffSelectedFilePath(path);
    openDockPanel('diffDetail');
  }, [openDockPanel]);

  // Fetch diff for diff detail panel
  // Options: staged (deprecated), baseRef (for PR-like branch diffs)
  const fetchDiffForReview = useCallback(async (path: string, options?: { staged?: boolean; baseRef?: string }) => {
    if (!activeRepoDaemonSession?.directory) {
      throw new Error('No repo directory available');
    }
    return sendGetFileDiff(activeRepoDaemonSession.directory, path, options);
  }, [activeRepoDaemonSession?.directory, sendGetFileDiff]);

  const branchDiffRequestId = useRef(0);
  const branchDiffLoadedRef = useRef(false);
  const branchDiffInFlightRef = useRef(false);
  const branchDiffDirtyAfterCurrentRef = useRef(false);
  const branchDiffVisibleRef = useRef(false);
  const branchDiffDirectoryRef = useRef<string | null>(null);
  const branchDiffCooldownUntilRef = useRef(0);
  const branchDiffScheduledTimerRef = useRef<number | null>(null);

  useEffect(() => {
    branchDiffVisibleRef.current = changesPanelVisible;
    branchDiffDirectoryRef.current = activeRepoDaemonSession?.directory ?? null;
  }, [activeRepoDaemonSession?.directory, changesPanelVisible]);

  const clearScheduledBranchDiffRefresh = useCallback(() => {
    if (branchDiffScheduledTimerRef.current !== null) {
      window.clearTimeout(branchDiffScheduledTimerRef.current);
      branchDiffScheduledTimerRef.current = null;
    }
  }, []);

  const refreshBranchDiff = useCallback(async (directory: string) => {
    if (branchDiffInFlightRef.current) {
      branchDiffDirtyAfterCurrentRef.current = true;
      return;
    }

    branchDiffInFlightRef.current = true;
    branchDiffDirtyAfterCurrentRef.current = false;
    const requestId = ++branchDiffRequestId.current;
    const hadLoadedBranchDiff = branchDiffLoadedRef.current;
    const startedAt = Date.now();
    setBranchDiffLoading(!hadLoadedBranchDiff);
    setBranchDiffRefreshing(hadLoadedBranchDiff);
    setBranchDiffError(null);
    try {
      const result = await sendGetBranchDiffFiles(directory);
      if (requestId !== branchDiffRequestId.current) return;
      if (result.success) {
        setBranchDiffFiles(result.files);
        setBranchDiffBaseRef(result.base_ref);
        setBranchDiffError(null);
        branchDiffLoadedRef.current = true;
        setBranchDiffLoaded(true);
      } else {
        if (!branchDiffLoadedRef.current) {
          setBranchDiffFiles([]);
          setBranchDiffBaseRef(result.base_ref || '');
          setBranchDiffLoaded(false);
        }
        setBranchDiffError(result.error || 'Failed to load branch diff');
      }
    } catch (err) {
      if (requestId !== branchDiffRequestId.current) return;
      if (!branchDiffLoadedRef.current) {
        setBranchDiffFiles([]);
        setBranchDiffBaseRef('');
        setBranchDiffLoaded(false);
      }
      setBranchDiffError(err instanceof Error ? err.message : 'Failed to load branch diff');
    } finally {
      if (requestId === branchDiffRequestId.current) {
        const durationMs = Date.now() - startedAt;
        if (durationMs >= CHANGES_BRANCH_DIFF_SLOW_THRESHOLD_MS) {
          branchDiffCooldownUntilRef.current = Date.now() + CHANGES_BRANCH_DIFF_SLOW_COOLDOWN_MS;
        }
        branchDiffInFlightRef.current = false;
        setBranchDiffLoading(false);
        setBranchDiffRefreshing(false);
        if (
          branchDiffDirtyAfterCurrentRef.current &&
          branchDiffVisibleRef.current &&
          branchDiffDirectoryRef.current === directory
        ) {
          const delayMs = Math.max(0, branchDiffCooldownUntilRef.current - Date.now());
          clearScheduledBranchDiffRefresh();
          branchDiffScheduledTimerRef.current = window.setTimeout(() => {
            branchDiffScheduledTimerRef.current = null;
            void refreshBranchDiff(directory);
          }, delayMs);
        }
      }
    }
  }, [clearScheduledBranchDiffRefresh, sendGetBranchDiffFiles]);

  const scheduleBranchDiffRefresh = useCallback((options?: { force?: boolean; debounceMs?: number }) => {
    const directory = branchDiffDirectoryRef.current;
    if (!directory) {
      return;
    }

    if (!branchDiffVisibleRef.current) {
      branchDiffDirtyAfterCurrentRef.current = true;
      return;
    }

    if (branchDiffInFlightRef.current) {
      branchDiffDirtyAfterCurrentRef.current = true;
      return;
    }

    const cooldownDelayMs = options?.force
      ? 0
      : Math.max(0, branchDiffCooldownUntilRef.current - Date.now());
    const delayMs = Math.max(options?.debounceMs ?? 0, cooldownDelayMs);

    clearScheduledBranchDiffRefresh();
    branchDiffScheduledTimerRef.current = window.setTimeout(() => {
      branchDiffScheduledTimerRef.current = null;
      void refreshBranchDiff(directory);
    }, delayMs);
  }, [clearScheduledBranchDiffRefresh, refreshBranchDiff]);

  useEffect(() => {
    clearScheduledBranchDiffRefresh();
    branchDiffRequestId.current += 1;
    branchDiffLoadedRef.current = false;
    branchDiffInFlightRef.current = false;
    branchDiffDirtyAfterCurrentRef.current = false;
    branchDiffCooldownUntilRef.current = 0;
    setBranchDiffLoaded(false);
    setBranchDiffFiles([]);
    setBranchDiffBaseRef('');
    setBranchDiffError(null);
    setBranchDiffLoading(false);
    setBranchDiffRefreshing(false);
  }, [activeRepoDaemonSession?.directory, clearScheduledBranchDiffRefresh]);

  useEffect(() => {
    if (view !== 'session' || !activeRepoDaemonSession?.directory) {
      clearScheduledBranchDiffRefresh();
      branchDiffLoadedRef.current = false;
      setBranchDiffLoaded(false);
      setBranchDiffFiles([]);
      setBranchDiffBaseRef('');
      setBranchDiffError(null);
      setBranchDiffLoading(false);
      setBranchDiffRefreshing(false);
      return;
    }

    if (!changesPanelVisible) {
      clearScheduledBranchDiffRefresh();
      return;
    }

    scheduleBranchDiffRefresh({ force: true });
    const intervalId = window.setInterval(() => {
      scheduleBranchDiffRefresh();
    }, CHANGES_BRANCH_DIFF_INTERVAL_MS);

    return () => {
      window.clearInterval(intervalId);
      clearScheduledBranchDiffRefresh();
    };
  }, [
    activeRepoDaemonSession?.directory,
    changesPanelVisible,
    clearScheduledBranchDiffRefresh,
    scheduleBranchDiffRefresh,
    view,
  ]);

  useEffect(() => {
    if (!gitStatus || !activeRepoDaemonSession?.directory) return;
    if (gitStatus.directory !== activeRepoDaemonSession.directory) return;
    branchDiffDirtyAfterCurrentRef.current = true;
    if (!changesPanelVisible) return;
    scheduleBranchDiffRefresh({ debounceMs: CHANGES_BRANCH_DIFF_STATUS_DEBOUNCE_MS });
  }, [activeRepoDaemonSession?.directory, changesPanelVisible, gitStatus, scheduleBranchDiffRefresh]);

  // Diff detail panel handlers
  const handleOpenDiffDetailPanel = useCallback(() => {
    setDiffSelectedFilePath(null); // Let the panel pick the first reviewable file.
    openDockPanel('diffDetail');
  }, [openDockPanel]);

  const handleCloseDiffDetailPanel = useCallback(() => {
    closeDockPanel('diffDetail');
    setDiffSelectedFilePath(null);
  }, [closeDockPanel]);

  const handleSendToClaude = useCallback((reference: string) => {
    if (!activeSessionId) return;
    sendRuntimeInput(activeSessionId, reference, 'user');
  }, [activeSessionId, sendRuntimeInput]);


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

  const handleOpenEditorForReview = useCallback((filePath?: string) => {
    if (!activeRepoDaemonSession?.directory) {
      showError('No repo directory available');
      return;
    }
    if (activeRemoteSession) {
      if (!activeEndpoint) {
        showError('Remote endpoint not available.');
        return;
      }
      if (!isZedEditorConfigured) {
        showError('Remote open-in-editor currently requires Zed.');
        return;
      }
      handleOpenEditor(activeRepoDaemonSession.directory, filePath, activeEndpoint.ssh_target);
      return;
    }
    handleOpenEditor(activeRepoDaemonSession.directory, filePath);
  }, [activeEndpoint, activeRemoteSession, activeRepoDaemonSession?.directory, handleOpenEditor, isZedEditorConfigured, showError]);

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
      id: 'reviewLoop',
      title: activeReviewLoopAvailable ? 'Review Loop' : 'Review Loop (No active session)',
      icon: <ReviewLoopIcon />,
      active: reviewLoopPanelOpen,
      disabled: !activeReviewLoopAvailable,
      toneClassName: activeReviewLoopState?.status ? `sidebar-tool-btn--loop-${activeReviewLoopState.status}` : undefined,
      onClick: () => toggleDockPanel('reviewLoop'),
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
      id: 'diff',
      title: diffPanelOpen ? 'Hide Diff Panel' : 'Show Diff Panel',
      icon: <DiffIcon />,
      active: diffPanelOpen,
      disabled: !activeSessionId,
      onClick: () => toggleDockPanel('diff'),
    },
    {
      id: 'attention',
      title: attentionPanelOpen ? 'Hide PRs Drawer' : 'Show PRs Drawer',
      icon: <PRsIcon />,
      active: attentionPanelOpen,
      badge: attentionCount > 0 ? attentionCount : undefined,
      onClick: () => toggleDockPanel('attention'),
    },
  ]), [
    activeReviewLoopAvailable,
    activeReviewLoopState?.status,
    activeSessionId,
    remoteEditorAvailable,
    attentionCount,
    attentionPanelOpen,
    diffPanelOpen,
    handleOpenEditorForSession,
    reviewLoopPanelOpen,
    workflowRunPanelOpen,
    toggleDockPanel,
  ]);

  const activeSessionZoomed = activeWorkspaceId ? Boolean(zoomModeBySessionId[activeWorkspaceId]) : false;

  // Interactive/contextual behavior for dock chips, keyed by shortcut id. Ids
  // not present here render as informational chips (keys + label, no click).
  // `available: false` hides the chip in the current context (e.g. no session).
  const dockActions = useMemo<Partial<Record<ShortcutId, {
    run?: () => void;
    isActive?: boolean;
    available?: boolean;
  }>>>(() => ({
    'dock.diffDetail': {
      // Must match the dock.diffDetail keyboard shortcut, which toggles the
      // diff-detail panel — not the external-editor action (that stays on the
      // sidebar's "Open in Editor" tool button).
      run: () => toggleDockPanel('diffDetail'),
      isActive: diffDetailPanelOpen,
      available: Boolean(activeSessionId),
    },
    'dock.reviewLoop': {
      run: () => toggleDockPanel('reviewLoop'),
      isActive: reviewLoopPanelOpen,
      available: activeReviewLoopAvailable,
    },
    'dock.diff': {
      run: () => toggleDockPanel('diff'),
      isActive: diffPanelOpen,
      available: Boolean(activeSessionId),
    },
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
    reviewLoopPanelOpen,
    activeReviewLoopAvailable,
    diffPanelOpen,
    diffDetailPanelOpen,
    attentionPanelOpen,
    activeSessionZoomed,
    toggleDockPanel,
  ]);

  // The dock is rebuilt from config on every relevant change, so rebinds and
  // membership/order edits reflect live. Unbound ids and context-unavailable
  // ids are filtered out.
  const dockItems = useMemo<DockItem[]>(() => (
    keybindings.dock.items.flatMap((id) => {
      const action = dockActions[id];
      if (action && action.available === false) return [];
      const keys = formatShortcut(id);
      if (!keys) return []; // unbound -> nothing to show
      return [{
        id,
        label: dockShortcutLabel(id),
        keys,
        active: action?.isActive ?? false,
        onClick: action?.run,
      }];
    })
  ), [keybindings.dock.items, keybindings.config, dockActions]);

  const handleStartReviewLoop = useCallback(async (prompt: string, iterationLimit: number, presetId?: string) => {
    if (!activeSessionId) return;
    try {
      const result = await sendStartReviewLoop(activeSessionId, prompt, iterationLimit, presetId);
      setReviewLoopStateForSession(activeSessionId, result.state);
    } catch (error) {
      showError(error instanceof Error ? error.message : 'Failed to start review loop');
      throw error;
    }
  }, [activeSessionId, sendStartReviewLoop, setReviewLoopStateForSession, showError]);

  const handleStopReviewLoop = useCallback(async () => {
    if (!activeSessionId) return;
    try {
      const result = await sendStopReviewLoop(activeSessionId);
      setReviewLoopStateForSession(activeSessionId, result.state);
    } catch (error) {
      showError(error instanceof Error ? error.message : 'Failed to stop review loop');
      throw error;
    }
  }, [activeSessionId, sendStopReviewLoop, setReviewLoopStateForSession, showError]);

  const handleSetReviewLoopIterations = useCallback(async (iterationLimit: number) => {
    if (!activeSessionId) return;
    try {
      const result = await setReviewLoopIterationLimit(activeSessionId, iterationLimit);
      setReviewLoopStateForSession(activeSessionId, result.state);
    } catch (error) {
      showError(error instanceof Error ? error.message : 'Failed to update review loop iterations');
      throw error;
    }
  }, [activeSessionId, setReviewLoopIterationLimit, setReviewLoopStateForSession, showError]);

  const handleAnswerReviewLoop = useCallback(async (loopId: string, interactionId: string, answer: string) => {
    if (!activeSessionId) return;
    try {
      const result = await answerReviewLoop(loopId, interactionId, answer);
      setReviewLoopStateForSession(activeSessionId, result.state);
    } catch (error) {
      showError(error instanceof Error ? error.message : 'Failed to answer review loop question');
      throw error;
    }
  }, [activeSessionId, answerReviewLoop, setReviewLoopStateForSession, showError]);

  const handleQuitApp = useCallback(() => {
    if (isTauri()) {
      void invoke('quit_app');
      return;
    }
    window.close();
  }, []);

  // Terminal panel handlers for active session
  // Use keyboard shortcuts hook
  useKeyboardShortcuts({
    onNewSession: () => handleNewSession('vertical'),
    onNewSessionHorizontal: () => handleNewSession('horizontal'),
    onNewWorkspace: handleNewWorkspace,
    onCloseSession: handleCloseCurrentSessionShortcut,
    onToggleActionMenu: handleToggleActionMenu,
    onGoToDashboard: goToDashboard,
    onToggleGridMode: toggleGridMode,
    onJumpToWaiting: handleJumpToWaiting,
    onSelectWorkspaceByIndex: handleSelectWorkspaceByIndex,
    onPrevSession: handlePrevWorkspace,
    onNextSession: handleNextWorkspace,
    onToggleSidebar: toggleSidebarCollapse,
    onRefreshPRs: handleRefreshPRs,
    onToggleDiffPanel: () => {
      toggleDockPanel('diff');
    },
    onToggleReviewLoopPanel: () => {
      toggleDockPanel('reviewLoop');
    },
    onToggleDiffDetailPanel: () => {
      toggleDockPanel('diffDetail');
    },
    onToggleAttentionPanel: () => toggleDockPanel('attention'),
    onOpenSettings: useCallback(() => setSettingsOpen(prev => !prev), []),
    onShowShortcuts: useCallback(() => setShortcutsOpen(prev => !prev), []),
    onIncreaseFontSize: increaseScale,
    onDecreaseFontSize: decreaseScale,
    onResetFontSize: resetScale,
    onQuit: handleQuitApp,
    enabled: !locationPickerOpen
      && !whatsNew.isOpen
      && !actionMenuOpen
      && !shortcutEditorOpen
      && !workspaceContextsOpen
      && !notebookOpen,
  });

  return (
    <DaemonProvider sendPRAction={sendPRAction} sendMutePR={sendMutePR} sendMuteRepo={sendMuteRepo} sendMuteAuthor={sendMuteAuthor} sendPRVisited={sendPRVisited}>
    <div className="app" onPointerDownCapture={handleAppPointerDownCapture}>
      {/* Error banner for version mismatch */}
      {connectionError && (
        <div className="connection-error-banner">
          {connectionError}
        </div>
      )}
      {/* Warning banner for non-critical issues */}
      {warnings.length > 0 && (
        <div className={`warning-banner ${connectionError ? 'with-connection-error' : ''}`}>
          <span>{warnings.map(w => w.message).join(' ')}</span>
          <button className="warning-dismiss" onClick={clearWarnings} title="Dismiss">×</button>
        </div>
      )}
      {/* New release banner */}
      {updateAvailableVersion && (
        <div className={`update-banner ${connectionError ? 'with-connection-error' : ''} ${warnings.length > 0 ? 'with-warning' : ''}`}>
          <span>
            Version {updateAvailableVersion} is available on GitHub.
          </span>
          <button
            className="update-install"
            onClick={() => void onOpenLatestRelease()}
          >
            View Release
          </button>
          <button
            className="update-dismiss"
            onClick={onDismissLatestRelease}
            title="Dismiss"
            aria-label="Dismiss update banner"
          >
            ×
          </button>
        </div>
      )}
      {openPRLauncherJob && (
        <OpenPRLauncherProgress
          repo={openPRLauncherJob.pr.repo}
          number={openPRLauncherJob.pr.number}
          title={openPRLauncherJob.pr.title}
          step={openPRLauncherJob.progress.step}
        />
      )}
      {/* Dashboard - always rendered, shown/hidden via z-index */}
      <div className={`view-container ${view === 'dashboard' ? 'visible' : 'hidden'}`}>
        <Dashboard
          sessions={unmutedEnrichedSessions}
          dispatchSessions={visibleEnrichedSessions}
          mutedWorkspaces={mutedWorkspaceViews}
          prs={prs}
          isLoading={!hasReceivedInitialState}
          isRefreshing={isRefreshingPRs}
          refreshError={refreshError}
          rateLimit={rateLimit}
          endpoints={daemonEndpoints}
          onRebootstrapEndpoint={handleRebootstrapEndpoint}
          onSelectSession={handleSelectSession}
          onWakeDispatch={sendWakeDispatchAgent}
          onNewSession={() => handleNewSession('vertical')}
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

      {/* Session view - always rendered to keep terminals alive */}
      <div className={`view-container ${view === 'session' ? 'visible' : 'hidden'}`}>
        <Sidebar
          workspaces={sidebarWorkspaceViews}
          visualOrder={visualWorkspaces}
          visualIndexByWorkspaceId={visualIndexByWorkspaceId}
          selectedId={activeSessionId}
          selectedWorkspaceId={activeWorkspaceId}
          selectedTile={selectedTile}
          tileContents={tileContents}
          collapsed={sidebarCollapsed}
          headerActions={sidebarHeaderActions}
          gridLayout={gridLayout}
          onSelectGridLayout={handleSelectGridLayout}
          dockItems={dockItems}
          dockCollapsed={keybindings.dock.collapsed}
          onToggleDockCollapsed={() => keybindings.setDockCollapsed(!keybindings.dock.collapsed)}
          mutedWorkspaces={mutedWorkspaceViews}
          mutedExpanded={sidebarMutedExpanded}
          onMutedExpandedChange={setSidebarMutedExpanded}
          onMuteWorkspace={sendMuteWorkspace}
          onRenameSession={sendRenameSession}
          onRenameWorkspace={sendRenameWorkspace}
          onChangeChiefOfStaff={handleChangeChiefOfStaff}
          showSessionless={showSessionlessWorkspaces}
          onToggleShowSessionless={handleToggleShowSessionlessWorkspaces}
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
          onSelectSession={handleSelectSession}
          onSelectWorkspace={handleSelectWorkspace}
          onSelectTile={handleSelectTile}
          onCloseTile={handleCloseTile}
          onReloadTile={handleReloadTile}
          onNewSession={() => handleNewSession('vertical')}
          onCloseSession={handleRequestCloseSession}
          onReloadSession={handleReloadSession}
          onGoToDashboard={goToDashboard}
          onToggleCollapse={toggleSidebarCollapse}
        />
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
              // Mount this workspace's terminals only when it is active or in the
              // warm set; otherwise it renders placeholders and rehydrates on return.
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
                    workspaceSessions={workspace.sessions.map((entry) => ({
                      id: entry.id,
                      label: entry.label,
                      agent: entry.agent,
                      cwd: entry.cwd,
                      endpointId: entry.endpointId,
                    }))}
                    workspace={workspaceState}
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
                    onZoomModeChange={(zoomed) => {
                      setZoomModeBySessionId((prev) => (
                        prev[workspace.id] === zoomed
                          ? prev
                          : { ...prev, [workspace.id]: zoomed }
                      ));
                    }}
                    onNavigateOutOfSession={handleNavigateOutOfSession}
                    onUndockTile={(tileId) => {
                      handleCloseTile(workspace.id, tileId);
                    }}
                    onUpdateTile={(tileId, tileParams) => (
                      sendWorkspaceUpdateTile(workspace.id, tileId, tileParams)
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
                <p>Press ⌘T to start a new workspace</p>
              </div>
            )}
          </div>
        </div>
        <RightDock
          panelOrder={dockPanelStack}
          panels={[
            {
              id: 'diff',
              isOpen: diffPanelOpen,
              width: 'clamp(280px, 30vw, 380px)',
              className: 'dock-panel dock-panel--diff',
              children: (
                <ChangesPanel
                  branchDiffFiles={branchDiffFiles}
                  branchDiffBaseRef={branchDiffBaseRef}
                  branchDiffError={branchDiffError}
                  branchDiffLoaded={branchDiffLoaded}
                  branchDiffLoading={branchDiffLoading}
                  branchDiffRefreshing={branchDiffRefreshing}
                  gitStatusLimited={Boolean(gitStatus?.limited && gitStatus.directory === activeRepoDaemonSession?.directory)}
                  gitStatusLimitedReason={gitStatus?.limited_reason}
                  selectedFile={null}
                  onFileSelect={handleFileSelect}
                  onOpenDiffClick={handleOpenDiffDetailPanel}
                />
              ),
            },
            {
              id: 'reviewLoop',
              isOpen: reviewLoopPanelOpen && Boolean(activeSessionId && activeReviewLoopAvailable && activeLocalSession),
              width: 'clamp(420px, 50vw, 680px)',
              tone: activeReviewLoopState ? toneForDockPanel(activeReviewLoopState.status) : 'default',
              className: 'dock-panel dock-panel--review-loop',
              children: activeSessionId && activeReviewLoopAvailable && activeLocalSession ? (
                <SessionReviewLoopBar
                  sessionId={activeSessionId}
                  sessionLabel={activeLocalSession.label}
                  loopState={activeReviewLoopState}
                  getReviewLoopRun={getReviewLoopRun}
                  onClose={() => closeDockPanel('reviewLoop')}
                  waitingReviewSessions={waitingReviewSessions}
                  onSelectSession={handleSelectSession}
                  onStart={handleStartReviewLoop}
                  onStop={handleStopReviewLoop}
                  onSetIterations={handleSetReviewLoopIterations}
                  onAnswer={handleAnswerReviewLoop}
                />
              ) : null,
            },
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
              id: 'diffDetail',
              isOpen: diffDetailPanelOpen,
              width: 'clamp(60vw, 72vw, 100vw)',
              className: 'dock-panel dock-panel--diff-detail',
              children: (
                <DiffDetailPanel
                  isOpen={diffDetailPanelOpen}
                  gitStatus={gitStatus}
                  repoPath={activeRepoDaemonSession?.directory || ''}
                  branch={activeRepoDaemonSession?.branch || ''}
                  onClose={handleCloseDiffDetailPanel}
                  fetchDiff={fetchDiffForReview}
                  sendGetBranchDiffFiles={sendGetBranchDiffFiles}
                  sendFetchRemotes={sendFetchRemotes}
                  getReviewState={getReviewState}
                  markFileViewed={markFileViewed}
                  addComment={sendAddComment}
                  updateComment={sendUpdateComment}
                  resolveComment={sendResolveComment}
                  deleteComment={sendDeleteComment}
                  getComments={sendGetComments}
                  resolvedTheme={resolvedTheme}
                  selectedFilePath={diffSelectedFilePath}
                  onSelectFilePath={setDiffSelectedFilePath}
                  onOpenEditor={handleOpenEditorForReview}
                  onSendToClaude={activeSessionId ? handleSendToClaude : undefined}
                  scale={scale}
                />
              ),
            },
          ]}
        />
      </div>

      {/* Grid view — global mission control. Mounted only while active so its
          single WebGL context is released on exit (mirrors the pane path). */}
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
      <WorktreeCleanupPrompt
        isVisible={closedWorktree !== null}
        worktreePath={closedWorktree?.path || ''}
        branchName={closedWorktree?.branch}
        isDeleting={worktreeCleanupState.isDeleting}
        deleteError={worktreeCleanupState.error}
        deleteForceable={worktreeCleanupState.forceable}
        onKeep={handleWorktreeKeep}
        onDelete={handleWorktreeDelete}
        onAlwaysKeep={handleWorktreeAlwaysKeep}
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
      <ErrorToast message={errorMessage} onDone={clearError} />
      <ChordLeaderHud />
      <WorkspaceContextNavigator
        isOpen={workspaceContextsOpen}
        contexts={workspaceContextViews}
        isLoading={workspaceContextsLoading}
        error={workspaceContextsError}
        onClose={() => setWorkspaceContextsOpen(false)}
        onRetry={() => void loadWorkspaceContexts()}
      />
      <NotebookBrowser
        isOpen={notebookOpen}
        onClose={() => setNotebookOpen(false)}
        listDir={sendFsList}
        readFile={sendFsRead}
        writeFile={sendFsWrite}
        existsFile={sendFsExists}
        backlinksNotebook={sendNotebookBacklinks}
        sendToChief={sendNotebookToChief}
        changeSignal={fsChangeSignal}
        listTasks={sendNotebookTaskList}
        retryTask={sendNotebookTaskRetry}
        taskChangeSignal={notebookTaskChangeSignal}
        chiefActive={notebookChiefActive}
      />
      <ActionMenu
        isOpen={actionMenuOpen}
        actions={actionMenuItems}
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
        onRemovePlugin={sendRemovePlugin}
        onSetPluginPriority={sendSetPluginPriority}
        onSetSetting={sendSetSetting}
        themePreference={themePreference}
        onSetTheme={setTheme}
      />
    </div>
    </DaemonProvider>
  );
}

export default App;
