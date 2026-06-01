import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { onOpenUrl, getCurrent } from '@tauri-apps/plugin-deep-link';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { getVersion } from '@tauri-apps/api/app';
import { openUrl } from '@tauri-apps/plugin-opener';
import { Sidebar, type SidebarHeaderAction, type FooterShortcut, ReviewLoopIcon, EditorIcon, DiffIcon, PRsIcon } from './components/Sidebar';
import { Dashboard } from './components/Dashboard';
import { AttentionDrawer } from './components/AttentionDrawer';
import { LocationPicker } from './components/LocationPicker';

import { UndoToast } from './components/UndoToast';
import { WorktreeCleanupPrompt } from './components/WorktreeCleanupPrompt';
import { CloseSessionPrompt } from './components/CloseSessionPrompt';
import { ChangesPanel } from './components/ChangesPanel';
import { DiffDetailPanel } from './components/DiffDetailPanel';
import { SessionReviewLoopBar } from './components/SessionReviewLoopBar';
import { OpenPRLauncherProgress } from './components/OpenPRLauncherProgress';
import { SessionCreationProgress, type SessionCreationPhase } from './components/SessionCreationProgress';
import { RightDock } from './components/RightDock';
import { SessionTerminalWorkspace } from './components/SessionTerminalWorkspace';
import { ThumbsModal } from './components/ThumbsModal';
import { SettingsModal } from './components/SettingsModal';
import { ShortcutsModal } from './components/ShortcutsModal';
import { WhatsNewModal } from './components/WhatsNewModal';
import { CopyToast, useCopyToast } from './components/CopyToast';
import { ErrorToast, useErrorToast } from './components/ErrorToast';
import { DaemonProvider } from './contexts/DaemonContext';
import { SettingsProvider } from './contexts/SettingsContext';
import { useSessionStore, type Session, type TerminalWorkspaceState } from './store/sessions';
import { useDaemonSocket, DaemonWorktree, DaemonSession, DaemonWorkspace, DaemonPR, DaemonEndpoint, DaemonPlugin, DaemonPluginIssue, GitStatusUpdate, BranchDiffFile, DaemonWarning, ReviewLoopState, SessionExitInfo } from './hooks/useDaemonSocket';
import { useSessionWorkspaceController } from './hooks/useSessionWorkspaceController';
import { isAttentionSessionState, normalizeSessionState } from './types/sessionState';
import { normalizeSessionAgent, type SessionAgent } from './types/sessionAgent';
import { hasPane, type TerminalSplitDirection } from './types/workspace';
import { useDaemonStore } from './store/daemonSessions';
import { usePRsNeedingAttention } from './hooks/usePRsNeedingAttention';
import { useKeyboardShortcuts } from './hooks/useKeyboardShortcuts';
import { useWhatsNew } from './hooks/useWhatsNew';
import { formatShortcut, modifierTokens } from './shortcuts';
import { useUIScale } from './hooks/useUIScale';
import { useTheme } from './hooks/useTheme';
import { useOpenPR, type OpenPRProgress } from './hooks/useOpenPR';
import { useUiAutomationBridge } from './hooks/useUiAutomationBridge';
import { ptySpawn } from './pty/bridge';
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

interface SplitSessionOptions {
  baseSessionId?: string;
  cwd?: string;
  endpointId?: string;
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

  // Connect to daemon WebSocket
  const {
    sendPRAction,
    sendMutePR,
    sendMuteRepo,
    sendMuteAuthor,
    sendMuteWorkspace,
    sendPRVisited,
    sendRefreshPRs,
    sendRegisterWorkspace,
    sendUnregisterWorkspace,
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
    sendSessionVisualized,
    sendWorkspaceAddSessionPane,
    sendWorkspaceClosePane,
    sendWorkspaceSetSplitRatio,
    sendWorkspaceDockPanel,
    sendWorkspaceUndockPanel,
    panelContents,
    requestPanelContent,
    sendRuntimeInput,
    isRuntimeAttached,
    sendGetFileDiff,
    sendGetBranchDiffFiles,
    getRepoInfo,
    getReviewLoopRun,
    getReviewLoopState,
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

  // Wrap the app content with SettingsProvider so useUIScale can access settings
  return (
    <SettingsProvider settings={settings} setSetting={sendSetSetting}>
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
        sendMutePR={sendMutePR}
        sendMuteRepo={sendMuteRepo}
        sendMuteAuthor={sendMuteAuthor}
        sendMuteWorkspace={sendMuteWorkspace}
        sendPRVisited={sendPRVisited}
        sendRefreshPRs={sendRefreshPRs}
        sendRegisterWorkspace={sendRegisterWorkspace}
        sendUnregisterWorkspace={sendUnregisterWorkspace}
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
        sendSessionVisualized={sendSessionVisualized}
        sendWorkspaceAddSessionPane={sendWorkspaceAddSessionPane}
        sendWorkspaceClosePane={sendWorkspaceClosePane}
        sendWorkspaceSetSplitRatio={sendWorkspaceSetSplitRatio}
        sendWorkspaceDockPanel={sendWorkspaceDockPanel}
        sendWorkspaceUndockPanel={sendWorkspaceUndockPanel}
        panelContents={panelContents}
        requestPanelContent={requestPanelContent}
        sendRuntimeInput={sendRuntimeInput}
        isRuntimeAttached={isRuntimeAttached}
        sendGetFileDiff={sendGetFileDiff}
        sendGetBranchDiffFiles={sendGetBranchDiffFiles}
        getRepoInfo={getRepoInfo}
        getReviewLoopRun={getReviewLoopRun}
        getReviewLoopState={getReviewLoopState}
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
  sendMutePR: ReturnType<typeof useDaemonSocket>['sendMutePR'];
  sendMuteRepo: ReturnType<typeof useDaemonSocket>['sendMuteRepo'];
  sendMuteAuthor: ReturnType<typeof useDaemonSocket>['sendMuteAuthor'];
  sendMuteWorkspace: ReturnType<typeof useDaemonSocket>['sendMuteWorkspace'];
  sendPRVisited: ReturnType<typeof useDaemonSocket>['sendPRVisited'];
  sendRefreshPRs: ReturnType<typeof useDaemonSocket>['sendRefreshPRs'];
  sendRegisterWorkspace: ReturnType<typeof useDaemonSocket>['sendRegisterWorkspace'];
  sendUnregisterWorkspace: ReturnType<typeof useDaemonSocket>['sendUnregisterWorkspace'];
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
  sendSessionVisualized: ReturnType<typeof useDaemonSocket>['sendSessionVisualized'];
  sendWorkspaceAddSessionPane: ReturnType<typeof useDaemonSocket>['sendWorkspaceAddSessionPane'];
  sendWorkspaceClosePane: ReturnType<typeof useDaemonSocket>['sendWorkspaceClosePane'];
  sendWorkspaceSetSplitRatio: ReturnType<typeof useDaemonSocket>['sendWorkspaceSetSplitRatio'];
  sendWorkspaceDockPanel: ReturnType<typeof useDaemonSocket>['sendWorkspaceDockPanel'];
  sendWorkspaceUndockPanel: ReturnType<typeof useDaemonSocket>['sendWorkspaceUndockPanel'];
  panelContents: ReturnType<typeof useDaemonSocket>['panelContents'];
  requestPanelContent: ReturnType<typeof useDaemonSocket>['requestPanelContent'];
  sendRuntimeInput: ReturnType<typeof useDaemonSocket>['sendRuntimeInput'];
  isRuntimeAttached: ReturnType<typeof useDaemonSocket>['isRuntimeAttached'];
  sendGetFileDiff: ReturnType<typeof useDaemonSocket>['sendGetFileDiff'];
  sendGetBranchDiffFiles: ReturnType<typeof useDaemonSocket>['sendGetBranchDiffFiles'];
  getRepoInfo: ReturnType<typeof useDaemonSocket>['getRepoInfo'];
  getReviewLoopRun: ReturnType<typeof useDaemonSocket>['getReviewLoopRun'];
  getReviewLoopState: ReturnType<typeof useDaemonSocket>['getReviewLoopState'];
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
  sendMutePR,
  sendMuteRepo,
  sendMuteAuthor,
  sendMuteWorkspace,
  sendPRVisited,
  sendRefreshPRs,
  sendRegisterWorkspace,
  sendUnregisterWorkspace,
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
  sendSessionVisualized,
  sendWorkspaceAddSessionPane,
  sendWorkspaceClosePane,
  sendWorkspaceSetSplitRatio,
  sendWorkspaceDockPanel,
  sendWorkspaceUndockPanel,
  panelContents,
  requestPanelContent,
  sendRuntimeInput,
  isRuntimeAttached,
  sendGetFileDiff,
  sendGetBranchDiffFiles,
  getRepoInfo,
  getReviewLoopRun,
  getReviewLoopState,
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

  // Settings modal (lifted from Dashboard for Cmd+, access)
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [shortcutsOpen, setShortcutsOpen] = useState(false);
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
    };
  });

  const visibleEnrichedSessions = filterSessionsRepresentedInWorkspaceLayouts(daemonWorkspaces, enrichedLocalSessions);

  const {
    eventRouter: paneRuntimeEventRouter,
    getActivePaneIdForSession,
    setActivePane,
    prepareClosePaneFocus,
    clearPreparedClosePaneFocus,
    setWorkspaceRef,
    removeWorkspaceRef,
    focusSessionPane,
    typeInSessionPaneViaUI,
    isSessionPaneInputFocused,
    scrollSessionPaneToTop,
    fitSessionActivePane,
    getPaneText,
    getPaneSize,
    getPaneVisibleContent,
    getPaneVisibleStyleSummary,
    resetSessionPaneTerminal,
    injectSessionPaneBytes,
    injectSessionPaneBase64,
    drainSessionPaneTerminal,
  } = useSessionWorkspaceController(sessions, activeSessionId);

  useEffect(() => {
    void connect();
  }, [connect]);

  type DockPanelId = 'diff' | 'reviewLoop' | 'attention' | 'diffDetail';

  // Muted section expansion (controlled by Dashboard click)
  const [sidebarMutedExpanded, setSidebarMutedExpanded] = useState(false);

  // View state management
  const [view, setView] = useState<'dashboard' | 'session'>('dashboard');
  const [dockState, setDockState] = useState<{
    openPanels: Record<DockPanelId, boolean>;
    stack: DockPanelId[];
  }>({
    openPanels: {
        diff: false,
        reviewLoop: false,
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

  // Thumbs (Quick Find) state
  const [thumbsOpen, setThumbsOpen] = useState(false);
  const [thumbsText, setThumbsText] = useState('');
  const [zoomModeBySessionId, setZoomModeBySessionId] = useState<Record<string, boolean>>({});
  const { message: copyMessage, showToast: showCopyToast, clearToast: clearCopyToast } = useCopyToast();
  const { message: errorMessage, showError, clearError } = useErrorToast();

  const handleRebootstrapEndpoint = useCallback(async (endpointId: string) => {
    try {
      await sendBootstrapEndpoint(endpointId);
    } catch (err) {
      showError(err instanceof Error ? err.message : 'Sync failed.');
    }
  }, [sendBootstrapEndpoint, showError]);

  const activeReviewLoopState = useMemo(
    () => (activeSessionId ? reviewLoopsBySessionId[activeSessionId] ?? null : null),
    [activeSessionId, reviewLoopsBySessionId],
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
  const attentionPanelOpen = openDockPanels.attention;
  const diffDetailPanelOpen = openDockPanels.diffDetail;
  const changesPanelVisible = view === 'session' && diffPanelOpen && Boolean(activeRepoDaemonSession?.directory);
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
    let paneAdded = false;

    try {
      await createSession(
        label,
        options.cwd || activeSession.cwd,
        sessionId,
        agent,
        options.endpointId ?? activeSession.endpointId,
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
          endpointId,
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
  }, [createWorkspaceSession, sendCreateWorktree, showError]);

  const closeLocationPicker = useCallback(() => {
    setLocationPickerOpen(false);
  }, []);

  // Quick Find (thumbs) handlers
  const handleOpenQuickFind = useCallback(() => {
    if (!activeSessionId) return;
    const session = sessions.find((entry) => entry.id === activeSessionId);
    if (!session) return;
    const activePaneId = getActivePaneIdForSession(session);
    const paneText = getPaneText(activeSessionId, activePaneId);
    if (!paneText) return;
    const textLines = paneText.split('\n');
    setThumbsText(textLines.slice(-1000).join('\n'));
    setThumbsOpen(true);
  }, [activeSessionId, getActivePaneIdForSession, getPaneText, sessions]);

  const handleThumbsClose = useCallback(() => {
    setThumbsOpen(false);
  }, []);

  const handleThumbsCopy = useCallback((_value: string) => {
    showCopyToast('Copied to clipboard');
  }, [showCopyToast]);

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
    closeSession: handleCloseSession,
    reloadSession,
    setSetting: sendSetSetting,
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

  const sidebarWorkspaceViews = useMemo(
    () => unmutedWorkspaceViews,
    [unmutedWorkspaceViews],
  );
  const workspaceSelection = useWorkspaceSelectionController(workspaceViews, activeSessionId);
  const activeWorkspaceId = workspaceSelection.activeWorkspaceId;

  // Markdown panels are daemon-owned docked panels opened via `attn open <path>`
  // (and re-dockable by dragging). There is no empty "show panel" toggle: a
  // panel only exists once it points at a real file.

  // Use workspace order so ⌘1-9 and prev/next match the top-level sidebar rows.
  const visualWorkspaces = sidebarWorkspaceViews;
  const visualIndexByWorkspaceId = useMemo(() => {
    return new Map(visualWorkspaces.map((workspace, index) => [workspace.id, index]));
  }, [visualWorkspaces]);

  const handleSelectWorkspace = useCallback(
    (workspaceId: string) => {
      const workspace = sidebarWorkspaceViews.find((entry) => entry.id === workspaceId)
        || workspaceViews.find((entry) => entry.id === workspaceId);
      const sessionId = workspace?.firstSessionId;
      if (sessionId) {
        handleSelectSession(sessionId);
      }
    },
    [handleSelectSession, sidebarWorkspaceViews, workspaceViews],
  );

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
      shortcutHint: `${formatShortcut('dock.diffDetail')} detail`,
      onClick: handleOpenEditorForSession,
    },
    {
      id: 'reviewLoop',
      title: activeReviewLoopAvailable ? 'Review Loop' : 'Review Loop (No active session)',
      icon: <ReviewLoopIcon />,
      active: reviewLoopPanelOpen,
      disabled: !activeReviewLoopAvailable,
      toneClassName: activeReviewLoopState?.status ? `sidebar-tool-btn--loop-${activeReviewLoopState.status}` : undefined,
      shortcutHint: `${formatShortcut('dock.reviewLoop')} loop`,
      onClick: () => toggleDockPanel('reviewLoop'),
    },
    {
      id: 'diff',
      title: diffPanelOpen ? 'Hide Diff Panel' : 'Show Diff Panel',
      icon: <DiffIcon />,
      active: diffPanelOpen,
      disabled: !activeSessionId,
      shortcutHint: `${formatShortcut('dock.diff')} diff`,
      onClick: () => toggleDockPanel('diff'),
    },
    {
      id: 'attention',
      title: attentionPanelOpen ? 'Hide PRs Drawer' : 'Show PRs Drawer',
      icon: <PRsIcon />,
      active: attentionPanelOpen,
      badge: attentionCount > 0 ? attentionCount : undefined,
      shortcutHint: `${formatShortcut('dock.attention')} PRs`,
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
    toggleDockPanel,
  ]);

  const activeSessionZoomed = activeWorkspaceId ? Boolean(zoomModeBySessionId[activeWorkspaceId]) : false;

  const sidebarFooterShortcuts = useMemo<FooterShortcut[]>(() => (
    activeSessionId
      ? [
          { label: `${formatShortcut('terminal.splitVertical')} split v` },
          { label: `${formatShortcut('terminal.splitHorizontal')} split h` },
          { label: `${formatShortcut('session.newHorizontal')} session h` },
          { label: `${formatShortcut('terminal.toggleZoom')} zoom`, active: activeSessionZoomed },
          { label: `${modifierTokens('terminal.focusLeft').join('')}←↑→↓ pane` },
        ]
      : []
  ), [activeSessionId, activeSessionZoomed]);

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
    onToggleDrawer: () => toggleDockPanel('attention'),
    onGoToDashboard: goToDashboard,
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
    onQuickFind: view === 'session' ? handleOpenQuickFind : undefined,
    onOpenSettings: useCallback(() => setSettingsOpen(prev => !prev), []),
    onShowShortcuts: useCallback(() => setShortcutsOpen(prev => !prev), []),
    onIncreaseFontSize: increaseScale,
    onDecreaseFontSize: decreaseScale,
    onResetFontSize: resetScale,
    onQuit: handleQuitApp,
    enabled: !locationPickerOpen && !thumbsOpen && !whatsNew.isOpen,
  });

  return (
    <DaemonProvider sendPRAction={sendPRAction} sendMutePR={sendMutePR} sendMuteRepo={sendMuteRepo} sendMuteAuthor={sendMuteAuthor} sendPRVisited={sendPRVisited}>
    <div className="app">
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
          mutedWorkspaces={mutedWorkspaceViews}
          prs={prs}
          isLoading={!hasReceivedInitialState}
          isRefreshing={isRefreshingPRs}
          refreshError={refreshError}
          rateLimit={rateLimit}
          endpoints={daemonEndpoints}
          onRebootstrapEndpoint={handleRebootstrapEndpoint}
          onSelectSession={handleSelectSession}
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
          collapsed={sidebarCollapsed}
          headerActions={sidebarHeaderActions}
          footerShortcuts={sidebarFooterShortcuts}
          mutedWorkspaces={mutedWorkspaceViews}
          mutedExpanded={sidebarMutedExpanded}
          onMutedExpandedChange={setSidebarMutedExpanded}
          onMuteWorkspace={sendMuteWorkspace}
          onSelectSession={handleSelectSession}
          onSelectWorkspace={handleSelectWorkspace}
          onNewSession={() => handleNewSession('vertical')}
          onCloseSession={handleRequestCloseSession}
          onReloadSession={handleReloadSession}
          onGoToDashboard={goToDashboard}
          onToggleCollapse={toggleSidebarCollapse}
        />
        <div className="terminal-pane">
          <div className="terminal-main-area">
            {workspaceViews.map((workspace) => {
              const workspaceState = terminalStateForWorkspaceSessions(workspace.sessions);
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
                    enabled={!locationPickerOpen}
                    isActiveSession={isActiveWorkspace}
                    isSessionViewVisible={view === 'session'}
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
                    onDockPanel={(panelId, panelKind, anchorPaneId, edge) => {
                      void sendWorkspaceDockPanel(workspace.id, panelId, panelKind, { anchorPaneId, edge }).catch(() => {});
                    }}
                    onUndockPanel={(panelId) => {
                      void sendWorkspaceUndockPanel(workspace.id, panelId).catch(() => {});
                    }}
                    panelContents={panelContents}
                    allowLocalPanelTargets={!workspace.endpointId}
                    onRequestPanelContent={requestPanelContent}
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
      <ThumbsModal
        isOpen={thumbsOpen}
        terminalText={thumbsText}
        onClose={handleThumbsClose}
        onCopy={handleThumbsCopy}
      />
      <CopyToast message={copyMessage} onDone={clearCopyToast} />
      <ErrorToast message={errorMessage} onDone={clearError} />
      <ShortcutsModal
        isOpen={shortcutsOpen}
        onClose={() => setShortcutsOpen(false)}
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
