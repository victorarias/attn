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
import { RightDock } from './components/RightDock';
import { SessionTerminalWorkspace } from './components/SessionTerminalWorkspace';
import { ThumbsModal } from './components/ThumbsModal';
import { ForkDialog } from './components/ForkDialog';
import { SettingsModal } from './components/SettingsModal';
import { CopyToast, useCopyToast } from './components/CopyToast';
import { ErrorToast, useErrorToast } from './components/ErrorToast';
import { DaemonProvider } from './contexts/DaemonContext';
import { SettingsProvider } from './contexts/SettingsContext';
import { MAIN_TERMINAL_PANE_ID, useSessionStore } from './store/sessions';
import { useDaemonSocket, DaemonWorktree, DaemonSession, DaemonWorkspace, DaemonPR, DaemonEndpoint, GitStatusUpdate, BranchDiffFile, DaemonWarning, ReviewLoopState } from './hooks/useDaemonSocket';
import { useSessionWorkspaceController } from './hooks/useSessionWorkspaceController';
import { isAttentionSessionState, normalizeSessionState } from './types/sessionState';
import { normalizeSessionAgent, type SessionAgent } from './types/sessionAgent';
import { useDaemonStore } from './store/daemonSessions';
import { usePRsNeedingAttention } from './hooks/usePRsNeedingAttention';
import { useKeyboardShortcuts } from './hooks/useKeyboardShortcuts';
import { useUIScale } from './hooks/useUIScale';
import { useTheme } from './hooks/useTheme';
import { useOpenPR } from './hooks/useOpenPR';
import { useUiAutomationBridge } from './hooks/useUiAutomationBridge';
import {
  getAgentAvailability,
  getAgentExecutableSettings,
  hasAnyAvailableAgents,
  resolvePreferredAgent,
} from './utils/agentAvailability';
import { normalizeInstallChannel, shouldCheckForReleaseUpdates } from './utils/installChannel';
import { groupSessionsByDirectory } from './utils/sessionGrouping';
import './App.css';

const RELEASES_LATEST_API = 'https://api.github.com/repos/victorarias/attn/releases/latest';
const RELEASES_LATEST_WEB = 'https://github.com/victorarias/attn/releases/latest';
const RELEASE_CHECK_INTERVAL_MS = 6 * 60 * 60 * 1000;
const UPDATE_BANNER_DISMISSED_STORAGE_KEY = 'attn.update_banner.dismissed_version';
const DOCK_PANEL_EXIT_MS = 260;

interface GitHubReleaseResponse {
  tag_name?: string;
  html_url?: string;
  prerelease?: boolean;
  draft?: boolean;
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

function App() {
  // Settings state (must be declared before useDaemonSocket to pass as callback)
  const [settings, setSettings] = useState<Record<string, string>>({});
  const [settingError, setSettingError] = useState<string | null>(null);
  const [daemonEndpoints, setDaemonEndpoints] = useState<DaemonEndpoint[]>([]);

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

  // Connect to daemon WebSocket
  const {
    sendPRAction,
    sendMutePR,
    sendMuteRepo,
    sendMuteAuthor,
    sendPRVisited,
    sendRefreshPRs,
    sendUnregisterSession,
    sendSetSetting,
    sendCreateWorktree,
    sendDeleteWorktree,
    sendAddEndpoint,
    sendUpdateEndpoint,
    sendRemoveEndpoint,
    sendSetEndpointRemoteWeb,
    sendGetRecentLocations,
    sendBrowseDirectory,
    sendInspectPath,
    sendCreateWorktreeFromBranch,
    sendFetchRemotes,
    sendFetchPRDetails,
    sendEnsureRepo,
    sendSubscribeGitStatus,
    sendUnsubscribeGitStatus,
    sendSessionVisualized,
    sendWorkspaceSplitPane,
    sendWorkspaceClosePane,
    sendRuntimeInput,
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
    sendWontFixComment,
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
        sendPRVisited={sendPRVisited}
        sendRefreshPRs={sendRefreshPRs}
        sendUnregisterSession={sendUnregisterSession}
        sendSetSetting={sendSetSetting}
        sendCreateWorktree={sendCreateWorktree}
        sendDeleteWorktree={sendDeleteWorktree}
        sendAddEndpoint={sendAddEndpoint}
        sendUpdateEndpoint={sendUpdateEndpoint}
        sendRemoveEndpoint={sendRemoveEndpoint}
        sendSetEndpointRemoteWeb={sendSetEndpointRemoteWeb}
        sendGetRecentLocations={sendGetRecentLocations}
        sendBrowseDirectory={sendBrowseDirectory}
        sendInspectPath={sendInspectPath}
        sendCreateWorktreeFromBranch={sendCreateWorktreeFromBranch}
        sendFetchRemotes={sendFetchRemotes}
        sendFetchPRDetails={sendFetchPRDetails}
        sendEnsureRepo={sendEnsureRepo}
        sendSubscribeGitStatus={sendSubscribeGitStatus}
        sendUnsubscribeGitStatus={sendUnsubscribeGitStatus}
        sendSessionVisualized={sendSessionVisualized}
        sendWorkspaceSplitPane={sendWorkspaceSplitPane}
        sendWorkspaceClosePane={sendWorkspaceClosePane}
        sendRuntimeInput={sendRuntimeInput}
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
        sendWontFixComment={sendWontFixComment}
        sendDeleteComment={sendDeleteComment}
        sendGetComments={sendGetComments}
        sendStartReviewLoop={sendStartReviewLoop}
        sendStopReviewLoop={sendStopReviewLoop}
        answerReviewLoop={answerReviewLoop}
        setReviewLoopIterationLimit={setReviewLoopIterationLimit}
        setReviewLoopStateForSession={setReviewLoopStateForSession}
        clearGitStatus={clearGitStatus}
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
  sendPRVisited: ReturnType<typeof useDaemonSocket>['sendPRVisited'];
  sendRefreshPRs: ReturnType<typeof useDaemonSocket>['sendRefreshPRs'];
  sendUnregisterSession: ReturnType<typeof useDaemonSocket>['sendUnregisterSession'];
  sendSetSetting: ReturnType<typeof useDaemonSocket>['sendSetSetting'];
  sendCreateWorktree: ReturnType<typeof useDaemonSocket>['sendCreateWorktree'];
  sendDeleteWorktree: ReturnType<typeof useDaemonSocket>['sendDeleteWorktree'];
  sendAddEndpoint: ReturnType<typeof useDaemonSocket>['sendAddEndpoint'];
  sendUpdateEndpoint: ReturnType<typeof useDaemonSocket>['sendUpdateEndpoint'];
  sendRemoveEndpoint: ReturnType<typeof useDaemonSocket>['sendRemoveEndpoint'];
  sendSetEndpointRemoteWeb: ReturnType<typeof useDaemonSocket>['sendSetEndpointRemoteWeb'];
  sendGetRecentLocations: ReturnType<typeof useDaemonSocket>['sendGetRecentLocations'];
  sendBrowseDirectory: ReturnType<typeof useDaemonSocket>['sendBrowseDirectory'];
  sendInspectPath: ReturnType<typeof useDaemonSocket>['sendInspectPath'];
  sendCreateWorktreeFromBranch: ReturnType<typeof useDaemonSocket>['sendCreateWorktreeFromBranch'];
  sendFetchRemotes: ReturnType<typeof useDaemonSocket>['sendFetchRemotes'];
  sendFetchPRDetails: ReturnType<typeof useDaemonSocket>['sendFetchPRDetails'];
  sendEnsureRepo: ReturnType<typeof useDaemonSocket>['sendEnsureRepo'];
  sendSubscribeGitStatus: ReturnType<typeof useDaemonSocket>['sendSubscribeGitStatus'];
  sendUnsubscribeGitStatus: ReturnType<typeof useDaemonSocket>['sendUnsubscribeGitStatus'];
  sendSessionVisualized: ReturnType<typeof useDaemonSocket>['sendSessionVisualized'];
  sendWorkspaceSplitPane: ReturnType<typeof useDaemonSocket>['sendWorkspaceSplitPane'];
  sendWorkspaceClosePane: ReturnType<typeof useDaemonSocket>['sendWorkspaceClosePane'];
  sendRuntimeInput: ReturnType<typeof useDaemonSocket>['sendRuntimeInput'];
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
  sendWontFixComment: ReturnType<typeof useDaemonSocket>['sendWontFixComment'];
  sendDeleteComment: ReturnType<typeof useDaemonSocket>['sendDeleteComment'];
  sendGetComments: ReturnType<typeof useDaemonSocket>['sendGetComments'];
  sendStartReviewLoop: ReturnType<typeof useDaemonSocket>['sendStartReviewLoop'];
  sendStopReviewLoop: ReturnType<typeof useDaemonSocket>['sendStopReviewLoop'];
  answerReviewLoop: ReturnType<typeof useDaemonSocket>['answerReviewLoop'];
  setReviewLoopIterationLimit: ReturnType<typeof useDaemonSocket>['setReviewLoopIterationLimit'];
  setReviewLoopStateForSession: (sessionId: string, state: ReviewLoopState | null) => void;
  clearGitStatus: () => void;
}

function AppContent({
  daemonSessions,
  daemonWorkspaces,
  prs,
  daemonEndpoints,
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
  sendPRVisited,
  sendRefreshPRs,
  sendUnregisterSession,
  sendSetSetting,
  sendCreateWorktree,
  sendDeleteWorktree,
  sendAddEndpoint,
  sendUpdateEndpoint,
  sendRemoveEndpoint,
  sendSetEndpointRemoteWeb,
  sendGetRecentLocations,
  sendBrowseDirectory,
sendInspectPath,
    sendCreateWorktreeFromBranch,
    sendFetchRemotes,
sendFetchPRDetails,
  sendEnsureRepo,
  sendSubscribeGitStatus,
  sendUnsubscribeGitStatus,
  sendSessionVisualized,
  sendWorkspaceSplitPane,
  sendWorkspaceClosePane,
  sendRuntimeInput,
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
  sendWontFixComment,
  sendDeleteComment,
  sendGetComments,
  sendStartReviewLoop,
  sendStopReviewLoop,
  answerReviewLoop,
  setReviewLoopIterationLimit,
  setReviewLoopStateForSession,
  clearGitStatus,
}: AppContentProps) {
  const {
    connect,
    sessions,
    activeSessionId,
    createSession,
    closeSession,
    setActiveSession,
    takeSessionSpawnArgs,
    reloadSession,
    setForkParams,
    setLauncherConfig,
    syncFromDaemonSessions,
    syncFromDaemonWorkspaces,
  } = useSessionStore();

  // UI scale for font sizing (Cmd+/Cmd-) - now uses SettingsContext
  const { scale, increaseScale, decreaseScale, resetScale } = useUIScale();
  const terminalFontSize = Math.round(14 * scale);

  // Theme (dark/light/system)
  const { preference: themePreference, resolved: resolvedTheme, setTheme } = useTheme();

  // Settings modal (lifted from Dashboard for Cmd+, access)
  const [settingsOpen, setSettingsOpen] = useState(false);
  const { repoStates, authorStates } = useDaemonStore();
  const mutedRepos = useMemo(() =>
    repoStates.filter(r => r.muted).map(r => r.repo),
    [repoStates],
  );
  const mutedAuthors = useMemo(() =>
    authorStates.filter(a => a.muted).map(a => a.author),
    [authorStates],
  );
  const connectedHosts = useMemo(() => {
    const set = new Set<string>();
    for (const pr of prs) {
      if (pr.host) set.add(pr.host);
    }
    return Array.from(set).sort();
  }, [prs]);

  // Track PR refresh state for progress indicator
  const [isRefreshingPRs, setIsRefreshingPRs] = useState(false);
  const [refreshError, setRefreshError] = useState<string | null>(null);
  const [branchDiffFiles, setBranchDiffFiles] = useState<BranchDiffFile[]>([]);
  const [branchDiffBaseRef, setBranchDiffBaseRef] = useState('');
  const [branchDiffError, setBranchDiffError] = useState<string | null>(null);

  // Worktree cleanup prompt state
  const [closedWorktree, setClosedWorktree] = useState<{ path: string; branch?: string } | null>(null);
  const [pendingSessionClose, setPendingSessionClose] = useState<{
    id: string;
    label: string;
    splitCount: number;
  } | null>(null);
  const [alwaysKeepWorktrees, setAlwaysKeepWorktrees] = useState(false);

  const [initialReviewFile, setInitialReviewFile] = useState<string | null>(null);
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
            createSession(label, cwd);
          }
        }
      }
    } catch (e) {
      console.error('Failed to parse deep-link URL:', e);
    }
  }, [createSession, setActiveSession]);

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
    const reviewLoop = reviewLoopsBySessionId[s.id];
    const endpointId = daemonSession?.endpoint_id ?? s.endpointId;
    const endpoint = endpointId ? endpointById.get(endpointId) : undefined;
    return {
      ...s,
      state: normalizeSessionState(rawState),
      endpointId,
      endpointName: endpoint?.name,
      endpointStatus: endpoint?.status,
      branch: daemonSession?.branch ?? s.branch,
      isWorktree: daemonSession?.is_worktree ?? s.isWorktree,
      recoverable: daemonSession?.recoverable ?? false,
      reviewLoopStatus: reviewLoop?.status,
    };
  });

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

  // Thumbs (Quick Find) state
  const [thumbsOpen, setThumbsOpen] = useState(false);
  const [thumbsText, setThumbsText] = useState('');
  const [zoomModeBySessionId, setZoomModeBySessionId] = useState<Record<string, boolean>>({});
  const { message: copyMessage, showToast: showCopyToast, clearToast: clearCopyToast } = useCopyToast();
  const { message: errorMessage, showError, clearError } = useErrorToast();
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

  // Fork dialog state
  const [forkDialogOpen, setForkDialogOpen] = useState(false);
  const [forkTargetSession, setForkTargetSession] = useState<{
    id: string;
    label: string;
    cwd: string;
    daemonSessionId: string;
    agent: SessionAgent;
  } | null>(null);
  const [forkError, setForkError] = useState<string | null>(null);
  const [utilityFocusRequestToken, setUtilityFocusRequestToken] = useState(0);

  // No auto-creation - user clicks "+" to start a session

  const handleNewSession = useCallback(() => {
    setLocationPickerOpen(true);
  }, []);

  const handleNewWorktreeSession = useCallback(() => {
    setLocationPickerOpen(true);
  }, []);

  const handleLocationSelect = useCallback(
    async (path: string, agent: SessionAgent, endpointId?: string, yoloMode = false) => {
      if (!hasAvailableAgents) {
        showError('No supported agent CLI found in PATH.');
        return;
      }
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
      }
      // Note: Location is automatically tracked by daemon when session registers
      const folderName = path.split('/').pop() || 'session';
      const selectedAgent = resolvePreferredAgent(agent, agentAvailability, 'codex');
      await createSession(folderName, path, undefined, selectedAgent, endpointId, yoloMode);
    },
    [agentAvailability, createSession, daemonEndpoints, hasAvailableAgents, showError]
  );

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

  // Fork session handlers
  const handleOpenForkDialog = useCallback(() => {
    if (!activeSessionId) return;
    const localSession = sessions.find((s) => s.id === activeSessionId);
    if (!localSession) return;
    const daemonSession = daemonSessions.find((ds) => ds.id === localSession.id);
    if (!daemonSession) return;
    if (daemonSession.endpoint_id) {
      showError('Forking remote sessions is not implemented yet.');
      return;
    }

    setForkTargetSession({
      id: localSession.id,
      label: localSession.label,
      cwd: localSession.cwd,
      daemonSessionId: daemonSession.id,
      agent: localSession.agent,
    });
    setForkError(null);
    setForkDialogOpen(true);
  }, [activeSessionId, daemonSessions, sessions, showError]);

  const handleForkConfirm = useCallback(async (name: string, createWorktree: boolean) => {
    if (!forkTargetSession) return;

    setForkError(null);
    let worktreePath: string | null = null;

    try {
      let targetCwd = forkTargetSession.cwd;

      // Create worktree if requested
      if (createWorktree) {
        const branchName = `fork/${name}`;
        const result = await sendCreateWorktree(
          forkTargetSession.cwd,
          branchName
        );
        if (!result.success) {
          // Show error in dialog, don't close - user can retry or uncheck worktree
          setForkError(`Failed to create worktree: ${result.error || 'Unknown error'}`);
          return;
        }
        targetCwd = result.path!;
        worktreePath = result.path!;
      }

      // Pre-generate session ID so we can set fork params before the pane binder asks for spawn args.
      const sessionId = crypto.randomUUID();

      // Store fork params BEFORE creating session to avoid race condition
      setForkParams(sessionId, forkTargetSession.daemonSessionId);

      // Create the forked session with the pre-generated ID
      await createSession(name, targetCwd, sessionId, forkTargetSession.agent);

      setForkDialogOpen(false);
      setForkTargetSession(null);
      setForkError(null);

    } catch (err) {
      console.error('[App] Fork failed:', err);
      // Clean up worktree if it was created but downstream steps failed
      if (worktreePath) {
        sendDeleteWorktree(worktreePath).catch((e) =>
          console.error('[App] Failed to cleanup worktree:', e)
        );
      }
      setForkError(err instanceof Error ? err.message : 'Fork failed');
    }
  }, [createSession, forkTargetSession, sendCreateWorktree, sendDeleteWorktree, setForkParams]);

  const handleForkClose = useCallback(() => {
    setForkDialogOpen(false);
    setForkTargetSession(null);
    setForkError(null);
  }, []);

  const handleCloseSession = useCallback(
    async (id: string) => {
      // Check if session is a worktree and last in directory
      const session = enrichedLocalSessions.find(s => s.id === id);
      if (session?.isWorktree && session.cwd) {
        const sessionsInSameDir = enrichedLocalSessions.filter(s => s.cwd === session.cwd);
        const isLastSession = sessionsInSameDir.length === 1;

        if (isLastSession && !alwaysKeepWorktrees) {
          // Show cleanup prompt
          setClosedWorktree({ path: session.cwd, branch: session.branch });
        }
      }

      // Unregister from daemon by matching session ID
      const localDaemonSession = daemonSessions.find(ds => ds.id === session?.id);
      if (localDaemonSession) {
        await sendUnregisterSession(localDaemonSession.id);
      } else {
        closeSession(id);
      }

      removeWorkspaceRef(id);
    },
    [alwaysKeepWorktrees, closeSession, daemonSessions, enrichedLocalSessions, removeWorkspaceRef, sendUnregisterSession, showError]
  );

  const handleRequestCloseSession = useCallback((id: string) => {
    const session = sessions.find((entry) => entry.id === id);
    if (!session) {
      return;
    }
    const splitCount = session.workspace.terminals.length;
    if (splitCount > 0) {
      setPendingSessionClose({
        id: session.id,
        label: session.label,
        splitCount,
      });
      return;
    }
    void handleCloseSession(id);
  }, [handleCloseSession, sessions]);

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
      setActiveSession(id);
      setUtilityFocusRequestToken((token) => token + 1);
    },
    [setActiveSession]
  );

  const handleClosePane = useCallback((sessionId: string, paneId: string) => {
    prepareClosePaneFocus(sessionId, paneId);
    return sendWorkspaceClosePane(sessionId, paneId).catch((error) => {
      clearPreparedClosePaneFocus(sessionId);
      throw error;
    });
  }, [clearPreparedClosePaneFocus, prepareClosePaneFocus, sendWorkspaceClosePane]);

  useUiAutomationBridge({
    sessions,
    activeSessionId,
    daemonReady: hasReceivedInitialState && !connectionError,
    connectionError,
    getActivePaneIdForSession,
    createSession,
    selectSession: handleSelectSession,
    closeSession: handleCloseSession,
    reloadSession,
    splitPane: sendWorkspaceSplitPane,
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
    getReviewState,
    addComment: sendAddComment,
    updateComment: sendUpdateComment,
    resolveComment: sendResolveComment,
    wontFixComment: sendWontFixComment,
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
    createSession,
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
      const result = await openPR(pr, defaultAgent);
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
    setClosedWorktree(null);
  }, []);

  const handleWorktreeDelete = useCallback(async () => {
    if (closedWorktree) {
      try {
        await sendDeleteWorktree(closedWorktree.path);
        // Note: Deleted paths are automatically filtered by daemon on next fetch
      } catch (err) {
        console.error('[App] Failed to delete worktree:', err);
      }
    }
    setClosedWorktree(null);
  }, [closedWorktree, sendDeleteWorktree]);

  const handleWorktreeAlwaysKeep = useCallback(() => {
    setAlwaysKeepWorktrees(true);
    setClosedWorktree(null);
  }, []);

  // Calculate attention count for drawer badge
  const waitingLocalSessions = enrichedLocalSessions
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
    const waiting = enrichedLocalSessions.find((s) =>
      isAttentionSessionState(s.state) || s.reviewLoopStatus === 'awaiting_user' || s.reviewLoopStatus === 'error'
    );
    if (waiting) {
      handleSelectSession(waiting.id);
    }
  }, [enrichedLocalSessions, handleSelectSession]);

  const sessionGroups = useMemo(() => groupSessionsByDirectory(enrichedLocalSessions), [enrichedLocalSessions]);

  // Use visual (grouped) order so ⌘1-9 and prev/next match the sidebar
  const visualSessions = useMemo(() => sessionGroups.flatMap((group) => group.sessions), [sessionGroups]);
  const visualIndexBySessionId = useMemo(() => {
    return new Map(visualSessions.map((session, index) => [session.id, index]));
  }, [visualSessions]);

  const handleSelectSessionByIndex = useCallback(
    (index: number) => {
      const session = visualSessions[index];
      if (session) {
        handleSelectSession(session.id);
      }
    },
    [visualSessions, handleSelectSession]
  );

  const handlePrevSession = useCallback(() => {
    if (!activeSessionId || visualSessions.length === 0) return;
    const currentIndex = visualIndexBySessionId.get(activeSessionId);
    if (currentIndex === undefined) return;
    const prevIndex = currentIndex > 0 ? currentIndex - 1 : visualSessions.length - 1;
    handleSelectSession(visualSessions[prevIndex].id);
  }, [activeSessionId, visualSessions, visualIndexBySessionId, handleSelectSession]);

  const handleNextSession = useCallback(() => {
    if (!activeSessionId || visualSessions.length === 0) return;
    const currentIndex = visualIndexBySessionId.get(activeSessionId);
    if (currentIndex === undefined) return;
    const nextIndex = currentIndex < visualSessions.length - 1 ? currentIndex + 1 : 0;
    handleSelectSession(visualSessions[nextIndex].id);
  }, [activeSessionId, visualSessions, visualIndexBySessionId, handleSelectSession]);

  const handleNavigateOutOfSession = useCallback((direction: 'left' | 'right' | 'up' | 'down') => {
    if (direction === 'left' || direction === 'up') {
      handlePrevSession();
      return;
    }
    handleNextSession();
  }, [handleNextSession, handlePrevSession]);

  const handleCloseCurrentSessionShortcut = useCallback(() => {
    if (!activeSessionId) {
      return;
    }

    const activeSession = sessions.find((session) => session.id === activeSessionId);
    if (!activeSession) {
      return;
    }

    if (activeSession.workspace.terminals.length > 0) {
      const activePaneId = getActivePaneIdForSession(activeSession);
      if (activePaneId !== MAIN_TERMINAL_PANE_ID) {
        void handleClosePane(activeSessionId, activePaneId).catch((error) => {
          showError(error instanceof Error ? error.message : 'Failed to close split pane');
        });
        return;
      }
    }

    handleRequestCloseSession(activeSessionId);
  }, [activeSessionId, getActivePaneIdForSession, handleClosePane, handleRequestCloseSession, sessions, showError]);

  const handleReloadSession = useCallback((id: string) => {
    const size = getPaneSize(id, MAIN_TERMINAL_PANE_ID) || undefined;
    void reloadSession(id, size).catch((error) => {
      const message = error instanceof Error ? error.message : String(error);
      showError(`Failed to reload session: ${message}`);
    });
  }, [getPaneSize, reloadSession, showError]);

  // Open file in diff detail panel
  const handleFileSelect = useCallback((path: string, _staged: boolean) => {
    setInitialReviewFile(path);
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
  const refreshBranchDiff = useCallback(async (directory: string) => {
    const requestId = ++branchDiffRequestId.current;
    setBranchDiffError(null);
    try {
      const result = await sendGetBranchDiffFiles(directory);
      if (requestId !== branchDiffRequestId.current) return;
      if (result.success) {
        setBranchDiffFiles(result.files);
        setBranchDiffBaseRef(result.base_ref);
      } else {
        setBranchDiffFiles([]);
        setBranchDiffBaseRef(result.base_ref || '');
        setBranchDiffError(result.error || 'Failed to load branch diff');
      }
    } catch (err) {
      if (requestId !== branchDiffRequestId.current) return;
      setBranchDiffFiles([]);
      setBranchDiffBaseRef('');
      setBranchDiffError(err instanceof Error ? err.message : 'Failed to load branch diff');
    }
  }, [sendGetBranchDiffFiles]);

  useEffect(() => {
    branchDiffRequestId.current += 1;
    setBranchDiffFiles([]);
    setBranchDiffBaseRef('');
    setBranchDiffError(null);
  }, [activeRepoDaemonSession?.directory]);

  useEffect(() => {
    if (view !== 'session' || !activeRepoDaemonSession?.directory) {
      setBranchDiffFiles([]);
      setBranchDiffBaseRef('');
      setBranchDiffError(null);
      return;
    }

    refreshBranchDiff(activeRepoDaemonSession.directory);
    const intervalId = window.setInterval(() => {
      refreshBranchDiff(activeRepoDaemonSession.directory);
    }, 30000);

    return () => {
      window.clearInterval(intervalId);
    };
  }, [view, activeRepoDaemonSession?.directory, refreshBranchDiff]);

  useEffect(() => {
    if (!gitStatus || !activeRepoDaemonSession?.directory) return;
    if (gitStatus.directory !== activeRepoDaemonSession.directory) return;
    refreshBranchDiff(activeRepoDaemonSession.directory);
  }, [gitStatus, activeRepoDaemonSession?.directory, refreshBranchDiff]);

  // Diff detail panel handlers
  const handleOpenDiffDetailPanel = useCallback(() => {
    setInitialReviewFile(null); // No specific file, let the diff detail panel pick first
    openDockPanel('diffDetail');
  }, [openDockPanel]);

  const handleCloseDiffDetailPanel = useCallback(() => {
    closeDockPanel('diffDetail');
    setInitialReviewFile(null);
  }, [closeDockPanel]);


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
      shortcutHint: '⌘⇧E detail',
      onClick: handleOpenEditorForSession,
    },
    {
      id: 'reviewLoop',
      title: activeReviewLoopAvailable ? 'Review Loop' : 'Review Loop (No active session)',
      icon: <ReviewLoopIcon />,
      active: reviewLoopPanelOpen,
      disabled: !activeReviewLoopAvailable,
      toneClassName: activeReviewLoopState?.status ? `sidebar-tool-btn--loop-${activeReviewLoopState.status}` : undefined,
      shortcutHint: '⌘⇧R loop',
      onClick: () => toggleDockPanel('reviewLoop'),
    },
    {
      id: 'diff',
      title: diffPanelOpen ? 'Hide Diff Panel' : 'Show Diff Panel',
      icon: <DiffIcon />,
      active: diffPanelOpen,
      disabled: !activeSessionId,
      shortcutHint: '⌘⇧G diff',
      onClick: () => toggleDockPanel('diff'),
    },
    {
      id: 'attention',
      title: attentionPanelOpen ? 'Hide PRs Drawer' : 'Show PRs Drawer',
      icon: <PRsIcon />,
      active: attentionPanelOpen,
      badge: attentionCount > 0 ? attentionCount : undefined,
      shortcutHint: '⌘⇧P PRs',
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

  const activeSessionZoomed = activeSessionId ? Boolean(zoomModeBySessionId[activeSessionId]) : false;

  const sidebarFooterShortcuts = useMemo<FooterShortcut[]>(() => (
    activeSessionId
      ? [
          { label: '⌘D split v' },
          { label: '⌘⇧D split h' },
          { label: '⌘⇧Z zoom', active: activeSessionZoomed },
          { label: '⌘⌥←↑→↓ pane' },
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

  // Terminal panel handlers for active session
  // Use keyboard shortcuts hook
  useKeyboardShortcuts({
    onNewSession: handleNewSession,
    onNewWorktreeSession: handleNewWorktreeSession,
    onCloseSession: handleCloseCurrentSessionShortcut,
    onToggleDrawer: () => toggleDockPanel('attention'),
    onGoToDashboard: goToDashboard,
    onJumpToWaiting: handleJumpToWaiting,
    onSelectSession: handleSelectSessionByIndex,
    onPrevSession: handlePrevSession,
    onNextSession: handleNextSession,
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
    onForkSession: view === 'session' && !activeRemoteSession ? handleOpenForkDialog : undefined,
    onOpenSettings: useCallback(() => setSettingsOpen(prev => !prev), []),
    onIncreaseFontSize: increaseScale,
    onDecreaseFontSize: decreaseScale,
    onResetFontSize: resetScale,
    enabled: !locationPickerOpen && !thumbsOpen && !forkDialogOpen,
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
      {/* Dashboard - always rendered, shown/hidden via z-index */}
      <div className={`view-container ${view === 'dashboard' ? 'visible' : 'hidden'}`}>
        <Dashboard
          sessions={enrichedLocalSessions}
          prs={prs}
          isLoading={!hasReceivedInitialState}
          isRefreshing={isRefreshingPRs}
          refreshError={refreshError}
          rateLimit={rateLimit}
          onSelectSession={handleSelectSession}
          onNewSession={handleNewSession}
          onRefreshPRs={handleRefreshPRs}
          onOpenPR={handleOpenPR}
          onOpenSettings={() => setSettingsOpen(true)}
        />
      </div>

      {/* Session view - always rendered to keep terminals alive */}
      <div className={`view-container ${view === 'session' ? 'visible' : 'hidden'}`}>
        <Sidebar
          sessionGroups={sessionGroups}
          visualOrder={visualSessions}
          visualIndexBySessionId={visualIndexBySessionId}
          selectedId={activeSessionId}
          collapsed={sidebarCollapsed}
          headerActions={sidebarHeaderActions}
          footerShortcuts={sidebarFooterShortcuts}
          onSelectSession={handleSelectSession}
          onNewSession={handleNewSession}
          onCloseSession={handleRequestCloseSession}
          onReloadSession={handleReloadSession}
          onGoToDashboard={goToDashboard}
          onToggleCollapse={toggleSidebarCollapse}
        />
        <div className="terminal-pane">
          <div className="terminal-main-area">
            {sessions.map((session) => (
              <div
                key={session.id}
                className={`terminal-wrapper ${session.id === activeSessionId ? 'active' : ''}`}
              >
                <SessionTerminalWorkspace
                  ref={setWorkspaceRef(session.id)}
                  sessionId={session.id}
                  sessionLabel={session.label}
                  sessionAgent={session.agent}
                  sessionEndpointId={session.endpointId}
                  cwd={session.cwd}
                  workspace={session.workspace}
                  activePaneId={getActivePaneIdForSession(session)}
                  fontSize={terminalFontSize}
                  resolvedTheme={resolvedTheme}
                  focusRequestToken={utilityFocusRequestToken}
                  enabled={!locationPickerOpen}
                  isActiveSession={session.id === activeSessionId}
                  isSessionViewVisible={view === 'session'}
                  eventRouter={paneRuntimeEventRouter}
                  getMainPaneSpawnArgs={(cols, rows) => takeSessionSpawnArgs(session.id, cols, rows)}
                  onSplitPane={(targetPaneId, direction) => {
                    void sendWorkspaceSplitPane(session.id, targetPaneId, direction).catch(console.error);
                  }}
                  onClosePane={(paneId) => {
                    void handleClosePane(session.id, paneId).catch(console.error);
                  }}
                  onFocusPane={(paneId) => {
                    setActivePane(session.id, paneId);
                  }}
                  onZoomModeChange={(zoomed) => {
                    setZoomModeBySessionId((prev) => (
                      prev[session.id] === zoomed
                        ? prev
                        : { ...prev, [session.id]: zoomed }
                    ));
                  }}
                  onNavigateOutOfSession={handleNavigateOutOfSession}
                />
              </div>
            ))}
            {sessions.length === 0 && (
              <div className="no-sessions">
                <p>No active sessions</p>
                <p>Click "+" in the sidebar to start a new session</p>
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
                  wontFixComment={sendWontFixComment}
                  deleteComment={sendDeleteComment}
                  getComments={sendGetComments}
                  resolvedTheme={resolvedTheme}
                  initialSelectedFile={initialReviewFile || undefined}
                  onOpenEditor={handleOpenEditorForReview}
                />
              ),
            },
          ]}
        />
      </div>

      <LocationPicker
        isOpen={locationPickerOpen}
        onClose={closeLocationPicker}
        onSelect={handleLocationSelect}
        onGetRecentLocations={sendGetRecentLocations}
        onBrowseDirectory={sendBrowseDirectory}
        onInspectPath={sendInspectPath}
        onGetRepoInfo={getRepoInfo}
        onCreateWorktree={sendCreateWorktree}
        onDeleteWorktree={sendDeleteWorktree}
        onError={showError}
        projectsDirectory={settings.projects_directory}
        agentAvailability={agentAvailability}
        endpoints={daemonEndpoints}
      />
      <UndoToast />
      <WorktreeCleanupPrompt
        isVisible={closedWorktree !== null}
        worktreePath={closedWorktree?.path || ''}
        branchName={closedWorktree?.branch}
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
      <ForkDialog
        isOpen={forkDialogOpen}
        sessionLabel={forkTargetSession?.label || ''}
        existingLabels={sessions.map(s => s.label)}
        error={forkError}
        onClose={handleForkClose}
        onFork={handleForkConfirm}
      />
      <SettingsModal
        isOpen={settingsOpen}
        onClose={() => setSettingsOpen(false)}
        mutedRepos={mutedRepos}
        connectedHosts={connectedHosts}
        onUnmuteRepo={sendMuteRepo}
        mutedAuthors={mutedAuthors}
        onUnmuteAuthor={sendMuteAuthor}
        settings={settings}
        endpoints={daemonEndpoints}
        onAddEndpoint={sendAddEndpoint}
        onUpdateEndpoint={sendUpdateEndpoint}
        onRemoveEndpoint={sendRemoveEndpoint}
        onSetEndpointRemoteWeb={sendSetEndpointRemoteWeb}
        onSetSetting={sendSetSetting}
        themePreference={themePreference}
        onSetTheme={setTheme}
      />
    </div>
    </DaemonProvider>
  );
}

export default App;
