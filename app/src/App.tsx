import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Terminal as XTerm } from '@xterm/xterm';
import { onOpenUrl, getCurrent } from '@tauri-apps/plugin-deep-link';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { getVersion } from '@tauri-apps/api/app';
import { openUrl } from '@tauri-apps/plugin-opener';
import { Terminal, TerminalHandle } from './components/Terminal';
import { Sidebar } from './components/Sidebar';
import { Dashboard } from './components/Dashboard';
import { AttentionDrawer } from './components/AttentionDrawer';
import { LocationPicker } from './components/LocationPicker';
import { BranchPicker } from './components/BranchPicker';
import { UndoToast } from './components/UndoToast';
import { WorktreeCleanupPrompt } from './components/WorktreeCleanupPrompt';
import { ChangesPanel } from './components/ChangesPanel';
import { ReviewPanel } from './components/ReviewPanel';
import { UtilityTerminalPanel } from './components/UtilityTerminalPanel';
import { ThumbsModal } from './components/ThumbsModal';
import { ForkDialog } from './components/ForkDialog';
import { CopyToast, useCopyToast } from './components/CopyToast';
import { ErrorToast, useErrorToast } from './components/ErrorToast';
import { DaemonProvider } from './contexts/DaemonContext';
import { SettingsProvider } from './contexts/SettingsContext';
import { useSessionStore } from './store/sessions';
import { ptyWrite } from './pty/bridge';
import { useDaemonSocket, DaemonWorktree, DaemonSession, DaemonPR, GitStatusUpdate, ReviewerEvent, ReviewToolUse, BranchDiffFile, DaemonWarning } from './hooks/useDaemonSocket';
import { normalizeSessionState } from './types/sessionState';
import { normalizeSessionAgent, type SessionAgent } from './types/sessionAgent';
import { useDaemonStore } from './store/daemonSessions';
import { usePRsNeedingAttention } from './hooks/usePRsNeedingAttention';
import { useKeyboardShortcuts } from './hooks/useKeyboardShortcuts';
import { useUIScale } from './hooks/useUIScale';
import { useOpenPR } from './hooks/useOpenPR';
import { getAgentAvailability, hasAnyAvailableAgents, resolvePreferredAgent } from './utils/agentAvailability';
import './App.css';

const RELEASES_LATEST_API = 'https://api.github.com/repos/victorarias/attn/releases/latest';
const RELEASES_LATEST_WEB = 'https://github.com/victorarias/attn/releases/latest';
const RELEASE_CHECK_INTERVAL_MS = 6 * 60 * 60 * 1000;

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

function App() {
  // Settings state (must be declared before useDaemonSocket to pass as callback)
  const [settings, setSettings] = useState<Record<string, string>>({});
  const [settingError, setSettingError] = useState<string | null>(null);

  // Reviewer agent state (unified events for ordered rendering)
  const [reviewerEvents, setReviewerEvents] = useState<ReviewerEvent[]>([]);
  const [reviewerRunning, setReviewerRunning] = useState(false);
  const [reviewerError, setReviewerError] = useState<string | undefined>();

  // Comments added by reviewer agent (passed to ReviewPanel)
  const [pendingAgentComments, setPendingAgentComments] = useState<import('./types/generated').ReviewComment[]>([]);

  // Comment IDs resolved by reviewer agent (passed to ReviewPanel)
  const [agentResolvedCommentIds, setAgentResolvedCommentIds] = useState<string[]>([]);

  // Worktrees state (used by WorktreeCleanupPrompt)
  const [, setWorktrees] = useState<DaemonWorktree[]>([]);

  // Git status state
  const [gitStatus, setGitStatus] = useState<GitStatusUpdate | null>(null);
  const [updateAvailableVersion, setUpdateAvailableVersion] = useState<string | null>(null);
  const [updateReleaseUrl, setUpdateReleaseUrl] = useState<string>(RELEASES_LATEST_WEB);

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
        const isRunning = await invoke<boolean>('is_daemon_running');
        if (!isRunning) {
          console.log('[App] Daemon not running, starting...');
          await invoke('start_daemon');
          console.log('[App] Daemon started');
        }
      } catch (err) {
        console.error('[App] Failed to start daemon:', err);
      }
    }
    ensureDaemon();
  }, []);

  // Check latest GitHub release periodically and notify when newer than local app.
  useEffect(() => {
    if (!isTauri()) return;

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

        if (isNewerVersion(currentVersion, latest.tag_name)) {
          setUpdateAvailableVersion(latest.tag_name.replace(/^v/, ''));
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
  }, []);

  const handleOpenLatestRelease = useCallback(async () => {
    try {
      await openUrl(updateReleaseUrl);
    } catch (err) {
      console.error('[App] Failed to open release URL:', err);
    }
  }, [updateReleaseUrl]);

  // Reviewer callbacks for streaming events
  const reviewerCallbacks = useMemo(() => ({
    onReviewStarted: () => {
      setReviewerEvents([]);
      setReviewerRunning(true);
      setReviewerError(undefined);
      setPendingAgentComments([]); // Clear pending comments when new review starts
      setAgentResolvedCommentIds([]); // Clear resolved IDs when new review starts
    },
    onReviewChunk: (_reviewId: string, content: string) => {
      // Consolidate consecutive chunks for efficient rendering
      setReviewerEvents(prev => {
        if (prev.length > 0 && prev[prev.length - 1].type === 'chunk') {
          // Append to existing chunk
          const updated = [...prev];
          const lastChunk = updated[updated.length - 1] as { type: 'chunk'; content: string };
          updated[updated.length - 1] = { type: 'chunk', content: lastChunk.content + content };
          return updated;
        }
        // Create new chunk event
        return [...prev, { type: 'chunk', content }];
      });
    },
    onReviewFinding: (_reviewId: string, finding: { filepath: string; line_start: number; content: string }, comment?: import('./types/generated').ReviewComment) => {
      console.log('[App] Review finding:', finding.filepath, finding.line_start);
      // Add the comment to pending list so ReviewPanel can pick it up
      if (comment) {
        setPendingAgentComments(prev => [...prev, comment]);
      }
    },
    onReviewCommentResolved: (_reviewId: string, commentId: string) => {
      console.log('[App] Review comment resolved:', commentId);
      setAgentResolvedCommentIds(prev => [...prev, commentId]);
    },
    onReviewToolUse: (_reviewId: string, toolUse: ReviewToolUse) => {
      console.log('[App] Review tool use:', toolUse.name);
      // Add tool use event - this breaks the chunk consolidation, creating a new text block after
      setReviewerEvents(prev => [...prev, { type: 'tool_use', ...toolUse }]);
    },
    onReviewComplete: (_reviewId: string, success: boolean, error?: string) => {
      setReviewerRunning(false);
      setReviewerError(success ? undefined : error);
    },
    onReviewCancelled: () => {
      setReviewerRunning(false);
    },
  }), []);

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
    sendDeleteBranch,
    sendGetRecentLocations,
    sendListBranches,
    sendSwitchBranch,
    sendCreateWorktreeFromBranch,
    sendCheckDirty,
    sendStash,
    sendStashPop,
    sendCheckAttnStash,
    sendCommitWIP,
    sendGetDefaultBranch,
    sendFetchRemotes,
    sendFetchPRDetails,
    sendListRemoteBranches,
    sendEnsureRepo,
    sendSubscribeGitStatus,
    sendUnsubscribeGitStatus,
    sendGetFileDiff,
    sendGetBranchDiffFiles,
    getRepoInfo,
    getReviewState,
    markFileViewed,
    sendAddComment,
    sendUpdateComment,
    sendResolveComment,
    sendWontFixComment,
    sendDeleteComment,
    sendGetComments,
    sendStartReview,
    sendCancelReview,
    connectionError,
    hasReceivedInitialState,
    rateLimit,
    warnings,
    clearWarnings,
  } = useDaemonSocket({
    onSessionsUpdate: setDaemonSessions,
    onPRsUpdate: setPRs,
    onReposUpdate: setRepoStates,
    onAuthorsUpdate: setAuthorStates,
    onSettingsUpdate: setSettings,
    onSettingError: setSettingError,
    onWorktreesUpdate: setWorktrees,
    onGitStatusUpdate: setGitStatus,
    reviewer: reviewerCallbacks,
  });

  // Memoize clearGitStatus to prevent subscription effect from re-running
  const clearGitStatus = useCallback(() => setGitStatus(null), []);

  // Wrap the app content with SettingsProvider so useUIScale can access settings
  return (
    <SettingsProvider settings={settings} setSetting={sendSetSetting}>
      <AppContent
        daemonSessions={daemonSessions}
        prs={prs}
        settings={settings}
        gitStatus={gitStatus}
        reviewerEvents={reviewerEvents}
        reviewerRunning={reviewerRunning}
        reviewerError={reviewerError}
        pendingAgentComments={pendingAgentComments}
        agentResolvedCommentIds={agentResolvedCommentIds}
        connectionError={connectionError}
        hasReceivedInitialState={hasReceivedInitialState}
        rateLimit={rateLimit}
        warnings={warnings}
        clearWarnings={clearWarnings}
        updateAvailableVersion={updateAvailableVersion}
        onOpenLatestRelease={handleOpenLatestRelease}
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
        sendDeleteBranch={sendDeleteBranch}
        sendGetRecentLocations={sendGetRecentLocations}
        sendListBranches={sendListBranches}
        sendSwitchBranch={sendSwitchBranch}
        sendCreateWorktreeFromBranch={sendCreateWorktreeFromBranch}
        sendCheckDirty={sendCheckDirty}
        sendStash={sendStash}
        sendStashPop={sendStashPop}
        sendCheckAttnStash={sendCheckAttnStash}
        sendCommitWIP={sendCommitWIP}
        sendGetDefaultBranch={sendGetDefaultBranch}
        sendFetchRemotes={sendFetchRemotes}
        sendFetchPRDetails={sendFetchPRDetails}
        sendListRemoteBranches={sendListRemoteBranches}
        sendEnsureRepo={sendEnsureRepo}
        sendSubscribeGitStatus={sendSubscribeGitStatus}
        sendUnsubscribeGitStatus={sendUnsubscribeGitStatus}
        sendGetFileDiff={sendGetFileDiff}
        sendGetBranchDiffFiles={sendGetBranchDiffFiles}
        getRepoInfo={getRepoInfo}
        getReviewState={getReviewState}
        markFileViewed={markFileViewed}
        sendAddComment={sendAddComment}
        sendUpdateComment={sendUpdateComment}
        sendResolveComment={sendResolveComment}
        sendWontFixComment={sendWontFixComment}
        sendDeleteComment={sendDeleteComment}
        sendGetComments={sendGetComments}
        sendStartReview={sendStartReview}
        sendCancelReview={sendCancelReview}
        clearGitStatus={clearGitStatus}
      />
    </SettingsProvider>
  );
}

// Props interface for AppContent - receives daemon state and functions from App
interface AppContentProps {
  daemonSessions: DaemonSession[];
  prs: DaemonPR[];
  settings: Record<string, string>;
  gitStatus: GitStatusUpdate | null;
  reviewerEvents: ReviewerEvent[];
  reviewerRunning: boolean;
  reviewerError: string | undefined;
  pendingAgentComments: import('./types/generated').ReviewComment[];
  agentResolvedCommentIds: string[];
  connectionError: string | null;
  hasReceivedInitialState: boolean;
  rateLimit: import('./hooks/useDaemonSocket').RateLimitState | null;
  warnings: DaemonWarning[];
  clearWarnings: () => void;
  updateAvailableVersion: string | null;
  onOpenLatestRelease: () => Promise<void>;
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
  sendDeleteBranch: ReturnType<typeof useDaemonSocket>['sendDeleteBranch'];
  sendGetRecentLocations: ReturnType<typeof useDaemonSocket>['sendGetRecentLocations'];
  sendListBranches: ReturnType<typeof useDaemonSocket>['sendListBranches'];
  sendSwitchBranch: ReturnType<typeof useDaemonSocket>['sendSwitchBranch'];
  sendCreateWorktreeFromBranch: ReturnType<typeof useDaemonSocket>['sendCreateWorktreeFromBranch'];
  sendCheckDirty: ReturnType<typeof useDaemonSocket>['sendCheckDirty'];
  sendStash: ReturnType<typeof useDaemonSocket>['sendStash'];
  sendStashPop: ReturnType<typeof useDaemonSocket>['sendStashPop'];
  sendCheckAttnStash: ReturnType<typeof useDaemonSocket>['sendCheckAttnStash'];
  sendCommitWIP: ReturnType<typeof useDaemonSocket>['sendCommitWIP'];
  sendGetDefaultBranch: ReturnType<typeof useDaemonSocket>['sendGetDefaultBranch'];
  sendFetchRemotes: ReturnType<typeof useDaemonSocket>['sendFetchRemotes'];
  sendFetchPRDetails: ReturnType<typeof useDaemonSocket>['sendFetchPRDetails'];
  sendListRemoteBranches: ReturnType<typeof useDaemonSocket>['sendListRemoteBranches'];
  sendEnsureRepo: ReturnType<typeof useDaemonSocket>['sendEnsureRepo'];
  sendSubscribeGitStatus: ReturnType<typeof useDaemonSocket>['sendSubscribeGitStatus'];
  sendUnsubscribeGitStatus: ReturnType<typeof useDaemonSocket>['sendUnsubscribeGitStatus'];
  sendGetFileDiff: ReturnType<typeof useDaemonSocket>['sendGetFileDiff'];
  sendGetBranchDiffFiles: ReturnType<typeof useDaemonSocket>['sendGetBranchDiffFiles'];
  getRepoInfo: ReturnType<typeof useDaemonSocket>['getRepoInfo'];
  getReviewState: ReturnType<typeof useDaemonSocket>['getReviewState'];
  markFileViewed: ReturnType<typeof useDaemonSocket>['markFileViewed'];
  sendAddComment: ReturnType<typeof useDaemonSocket>['sendAddComment'];
  sendUpdateComment: ReturnType<typeof useDaemonSocket>['sendUpdateComment'];
  sendResolveComment: ReturnType<typeof useDaemonSocket>['sendResolveComment'];
  sendWontFixComment: ReturnType<typeof useDaemonSocket>['sendWontFixComment'];
  sendDeleteComment: ReturnType<typeof useDaemonSocket>['sendDeleteComment'];
  sendGetComments: ReturnType<typeof useDaemonSocket>['sendGetComments'];
  sendStartReview: ReturnType<typeof useDaemonSocket>['sendStartReview'];
  sendCancelReview: ReturnType<typeof useDaemonSocket>['sendCancelReview'];
  clearGitStatus: () => void;
}

function AppContent({
  daemonSessions,
  prs,
  settings,
  gitStatus,
  reviewerEvents,
  reviewerRunning,
  reviewerError,
  pendingAgentComments,
  agentResolvedCommentIds,
  connectionError,
  hasReceivedInitialState,
  rateLimit,
  warnings,
  clearWarnings,
  updateAvailableVersion,
  onOpenLatestRelease,
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
  sendDeleteBranch,
  sendGetRecentLocations,
  sendListBranches,
  sendSwitchBranch,
  sendCreateWorktreeFromBranch,
  sendCheckDirty,
  sendStash,
  sendStashPop,
  sendCheckAttnStash,
  sendCommitWIP,
  sendGetDefaultBranch,
  sendFetchRemotes,
  sendFetchPRDetails,
  sendListRemoteBranches,
  sendEnsureRepo,
  sendSubscribeGitStatus,
  sendUnsubscribeGitStatus,
  sendGetFileDiff,
  sendGetBranchDiffFiles,
  getRepoInfo,
  getReviewState,
  markFileViewed,
  sendAddComment,
  sendUpdateComment,
  sendResolveComment,
  sendWontFixComment,
  sendDeleteComment,
  sendGetComments,
  sendStartReview,
  sendCancelReview,
  clearGitStatus,
}: AppContentProps) {
  const {
    sessions,
    activeSessionId,
    createSession,
    closeSession,
    setActiveSession,
    connectTerminal,
    resizeSession,
    openTerminalPanel,
    collapseTerminalPanel,
    setTerminalPanelHeight,
    addUtilityTerminal,
    removeUtilityTerminal,
    setActiveUtilityTerminal,
    renameUtilityTerminal,
    setForkParams,
    setResumePicker,
    setLauncherConfig,
    syncFromDaemonSessions,
  } = useSessionStore();

  // UI scale for font sizing (Cmd+/Cmd-) - now uses SettingsContext
  const { scale, increaseScale, decreaseScale, resetScale } = useUIScale();
  const terminalFontSize = Math.round(14 * scale);

  // Track PR refresh state for progress indicator
  const [isRefreshingPRs, setIsRefreshingPRs] = useState(false);
  const [refreshError, setRefreshError] = useState<string | null>(null);
  const [branchDiffFiles, setBranchDiffFiles] = useState<BranchDiffFile[]>([]);
  const [branchDiffBaseRef, setBranchDiffBaseRef] = useState('');
  const [branchDiffError, setBranchDiffError] = useState<string | null>(null);

  // Worktree cleanup prompt state
  const [closedWorktree, setClosedWorktree] = useState<{ path: string; branch?: string } | null>(null);
  const [alwaysKeepWorktrees, setAlwaysKeepWorktrees] = useState(() => {
    const stored = localStorage.getItem('alwaysKeepWorktrees');
    return stored === 'true';
  });

  // Review panel state
  const [reviewPanelOpen, setReviewPanelOpen] = useState(false);
  const [initialReviewFile, setInitialReviewFile] = useState<string | null>(null);
  const agentAvailability = useMemo(() => getAgentAvailability(settings), [settings]);
  const hasAvailableAgents = useMemo(
    () => hasAnyAvailableAgents(agentAvailability),
    [agentAvailability],
  );

  useEffect(() => {
    setLauncherConfig({
      claudeExecutable: settings.claude_executable || '',
      codexExecutable: settings.codex_executable || '',
      copilotExecutable: settings.copilot_executable || '',
    });
  }, [settings, setLauncherConfig]);

  // Keep local UI sessions in sync with daemon's canonical session list.
  useEffect(() => {
    if (!hasReceivedInitialState) {
      return;
    }
    syncFromDaemonSessions(daemonSessions);
  }, [daemonSessions, hasReceivedInitialState, syncFromDaemonSessions]);

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
  const enrichedLocalSessions = sessions.map((s) => {
    const daemonSession = daemonSessions.find((ds) => ds.id === s.id);
    const rawState = daemonSession?.state ?? s.state;
    return {
      ...s,
      state: normalizeSessionState(rawState),
      branch: daemonSession?.branch ?? s.branch,
      isWorktree: daemonSession?.is_worktree ?? s.isWorktree,
    };
  });

  const terminalRefs = useRef<Map<string, TerminalHandle>>(new Map());

  // View state management
  const [view, setView] = useState<'dashboard' | 'session' | 'review'>('dashboard');
  const previousViewRef = useRef<'dashboard' | 'session'>('session');
  const gitStatusSubscribedDirRef = useRef<string | null>(null);

  // When activeSessionId changes, update view
  useEffect(() => {
    if (activeSessionId) {
      setView('session');
    }
  }, [activeSessionId]);

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

  // Drawer state management
  const [drawerOpen, setDrawerOpen] = useState(false);

  const toggleDrawer = useCallback(() => {
    setDrawerOpen((prev) => !prev);
  }, []);

  const closeDrawer = useCallback(() => {
    setDrawerOpen(false);
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

  // Branch picker state management
  const [branchPickerOpen, setBranchPickerOpen] = useState(false);

  // Thumbs (Quick Find) state
  const [thumbsOpen, setThumbsOpen] = useState(false);
  const [thumbsText, setThumbsText] = useState('');
  const { message: copyMessage, showToast: showCopyToast, clearToast: clearCopyToast } = useCopyToast();
  const { message: errorMessage, showError, clearError } = useErrorToast();

  useEffect(() => {
    if (!settingError) {
      return;
    }
    showError(settingError);
    clearSettingError();
  }, [clearSettingError, settingError, showError]);

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
    async (path: string, agent: SessionAgent, resumeEnabled?: boolean) => {
      if (!hasAvailableAgents) {
        showError('No supported agent CLI found in PATH (codex, claude, copilot).');
        return;
      }
      // Note: Location is automatically tracked by daemon when session registers
      const folderName = path.split('/').pop() || 'session';
      const selectedAgent = resolvePreferredAgent(agent, agentAvailability, 'codex');
      let sessionId: string;
      if (resumeEnabled) {
        sessionId = crypto.randomUUID();
        setResumePicker(sessionId);
        await createSession(folderName, path, sessionId, selectedAgent);
      } else {
        sessionId = await createSession(folderName, path, undefined, selectedAgent);
      }
      // Fit terminal after view becomes visible
      setTimeout(() => {
        const handle = terminalRefs.current.get(sessionId);
        handle?.fit();
        handle?.focus();
      }, 100);
    },
    [agentAvailability, createSession, hasAvailableAgents, setResumePicker, showError]
  );

  const closeLocationPicker = useCallback(() => {
    setLocationPickerOpen(false);
  }, []);

  // Quick Find (thumbs) handlers
  const handleOpenQuickFind = useCallback(() => {
    if (!activeSessionId) return;
    const handle = terminalRefs.current.get(activeSessionId);
    const terminal = handle?.terminal;
    if (!terminal) return;

    // Extract last 1000 lines from terminal buffer
    const buffer = terminal.buffer.active;
    if (!buffer) return;
    const lines = 1000;
    const startLine = Math.max(0, buffer.length - lines);
    const textLines: string[] = [];

    for (let i = startLine; i < buffer.length; i++) {
      const line = buffer.getLine(i);
      if (line) textLines.push(line.translateToString(true));
    }

    setThumbsText(textLines.join('\n'));
    setThumbsOpen(true);
  }, [activeSessionId]);

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

    setForkTargetSession({
      id: localSession.id,
      label: localSession.label,
      cwd: localSession.cwd,
      daemonSessionId: daemonSession.id,
      agent: localSession.agent,
    });
    setForkError(null);
    setForkDialogOpen(true);
  }, [activeSessionId, sessions, daemonSessions]);

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

      // Pre-generate session ID so we can set fork params BEFORE creating session
      // (createSession triggers re-render which mounts Terminal and calls connectTerminal)
      const sessionId = crypto.randomUUID();

      // Store fork params BEFORE creating session to avoid race condition
      setForkParams(sessionId, forkTargetSession.daemonSessionId);

      // Create the forked session with the pre-generated ID
      await createSession(name, targetCwd, sessionId, forkTargetSession.agent);

      setForkDialogOpen(false);
      setForkTargetSession(null);
      setForkError(null);

      // Fit terminal after view becomes visible
      setTimeout(() => {
        const handle = terminalRefs.current.get(sessionId);
        handle?.fit();
        handle?.focus();
      }, 100);
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
  }, [forkTargetSession, sendCreateWorktree, sendDeleteWorktree, createSession, setForkParams]);

  const handleForkClose = useCallback(() => {
    setForkDialogOpen(false);
    setForkTargetSession(null);
    setForkError(null);
  }, []);

  const handleCloseSession = useCallback(
    (id: string) => {
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
      const daemonSession = daemonSessions.find(ds => ds.id === session?.id);
      if (daemonSession) {
        sendUnregisterSession(daemonSession.id);
      }

      terminalRefs.current.delete(id);
      closeSession(id);
    },
    [closeSession, enrichedLocalSessions, alwaysKeepWorktrees, daemonSessions, sendUnregisterSession]
  );

  const handleSelectSession = useCallback(
    (id: string) => {
      const nextSession = sessions.find((session) => session.id === id);
      const utilityOpen =
        nextSession?.terminalPanel.isOpen === true &&
        nextSession.terminalPanel.terminals.length > 0 &&
        nextSession.terminalPanel.activeTabId !== null;

      setActiveSession(id);
      if (utilityOpen) {
        setUtilityFocusRequestToken((token) => token + 1);
      }
      // Fit and focus the terminal after a short delay (allows CSS to apply)
      setTimeout(() => {
        const handle = terminalRefs.current.get(id);
        handle?.fit();
        // Keep utility terminal focus when an existing utility tab is open.
        if (!utilityOpen) {
          handle?.focus();
        }
      }, 50);
    },
    [sessions, setActiveSession]
  );

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
        alert('No supported agent CLI found in PATH (codex, claude, copilot).');
        return;
      }
      const configuredDefaultAgent = normalizeSessionAgent(settings.new_session_agent, 'claude');
      const defaultAgent = resolvePreferredAgent(configuredDefaultAgent, agentAvailability, 'codex');
      const result = await openPR(pr, defaultAgent);
      if (result.success) {
        console.log(`[App] Worktree created at ${result.worktreePath}`);
        // Fit terminal after view becomes visible
        setTimeout(() => {
          const handle = terminalRefs.current.get(result.sessionId);
          handle?.fit();
          handle?.focus();
        }, 100);
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
    localStorage.setItem('alwaysKeepWorktrees', 'true');
    setClosedWorktree(null);
  }, []);

  const handleTerminalReady = useCallback(
    (sessionId: string) => (terminal: XTerm) => {
      connectTerminal(sessionId, terminal);
    },
    [connectTerminal]
  );

  const handleResize = useCallback(
    (sessionId: string) => (cols: number, rows: number) => {
      // Ignore resize callbacks from hidden/non-active terminals.
      // Hidden terminals can transiently report invalid dimensions while unmounted.
      if (sessionId !== activeSessionId || view !== 'session') {
        return;
      }
      // Allow legitimately small visible terminals (narrow windows / large fonts).
      if (cols <= 0 || rows <= 0) {
        return;
      }
      resizeSession(sessionId, cols, rows);
    },
    [activeSessionId, resizeSession, view]
  );

  const setTerminalRef = useCallback(
    (sessionId: string) => (ref: TerminalHandle | null) => {
      if (ref) {
        terminalRefs.current.set(sessionId, ref);
      }
    },
    []
  );

  // Calculate attention count for drawer badge
  const waitingLocalSessions = enrichedLocalSessions.filter((s) => s.state === 'waiting_input');
  const { needsAttention: prsNeedingAttention } = usePRsNeedingAttention(prs);
  const attentionCount = waitingLocalSessions.length + prsNeedingAttention.length;

  // Keyboard shortcut handlers
  const handleJumpToWaiting = useCallback(() => {
    const waiting = enrichedLocalSessions.find((s) => s.state === 'waiting_input');
    if (waiting) {
      handleSelectSession(waiting.id);
    }
  }, [enrichedLocalSessions, handleSelectSession]);

  const handleSelectSessionByIndex = useCallback(
    (index: number) => {
      const session = sessions[index];
      if (session) {
        handleSelectSession(session.id);
      }
    },
    [sessions, handleSelectSession]
  );

  const handlePrevSession = useCallback(() => {
    if (!activeSessionId || sessions.length === 0) return;
    const currentIndex = sessions.findIndex((s) => s.id === activeSessionId);
    const prevIndex = currentIndex > 0 ? currentIndex - 1 : sessions.length - 1;
    handleSelectSession(sessions[prevIndex].id);
  }, [activeSessionId, sessions, handleSelectSession]);

  const handleNextSession = useCallback(() => {
    if (!activeSessionId || sessions.length === 0) return;
    const currentIndex = sessions.findIndex((s) => s.id === activeSessionId);
    const nextIndex = currentIndex < sessions.length - 1 ? currentIndex + 1 : 0;
    handleSelectSession(sessions[nextIndex].id);
  }, [activeSessionId, sessions, handleSelectSession]);

  const handleCloseCurrentSession = useCallback(() => {
    if (activeSessionId) {
      handleCloseSession(activeSessionId);
    }
  }, [activeSessionId, handleCloseSession]);

  // Open file in review panel
  const handleFileSelect = useCallback((path: string, _staged: boolean) => {
    setInitialReviewFile(path);
    setReviewPanelOpen(true);
    if (view !== 'review') {
      previousViewRef.current = view === 'dashboard' ? 'dashboard' : 'session';
    }
    setView('review');
  }, [view]);

  // Fetch diff for ReviewPanel
  // Options: staged (deprecated), baseRef (for PR-like branch diffs)
  const fetchDiffForReview = useCallback(async (path: string, options?: { staged?: boolean; baseRef?: string }) => {
    const activeLocalSession = sessions.find((s) => s.id === activeSessionId);
    if (!activeLocalSession?.cwd) throw new Error('No active session');
    const daemonSession = daemonSessions.find((ds) => ds.id === activeLocalSession.id);
    if (!daemonSession) throw new Error('No daemon session found');
    return sendGetFileDiff(daemonSession.directory, path, options);
  }, [sessions, activeSessionId, daemonSessions, sendGetFileDiff]);

  // Get active daemon session info for ReviewPanel
  const activeDaemonSession = useMemo(() => {
    const activeLocalSession = sessions.find((s) => s.id === activeSessionId);
    if (!activeLocalSession?.cwd) return null;
    return daemonSessions.find((ds) => ds.id === activeLocalSession.id) || null;
  }, [sessions, activeSessionId, daemonSessions]);

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
  }, [activeDaemonSession?.directory]);

  useEffect(() => {
    if (view !== 'session' || !activeDaemonSession?.directory) {
      setBranchDiffFiles([]);
      setBranchDiffBaseRef('');
      setBranchDiffError(null);
      return;
    }

    refreshBranchDiff(activeDaemonSession.directory);
    const intervalId = window.setInterval(() => {
      refreshBranchDiff(activeDaemonSession.directory);
    }, 30000);

    return () => {
      window.clearInterval(intervalId);
    };
  }, [view, activeDaemonSession?.directory, refreshBranchDiff]);

  useEffect(() => {
    if (!gitStatus || !activeDaemonSession?.directory) return;
    if (gitStatus.directory !== activeDaemonSession.directory) return;
    refreshBranchDiff(activeDaemonSession.directory);
  }, [gitStatus, activeDaemonSession?.directory, refreshBranchDiff]);

  // Review panel handlers
  const handleOpenReviewPanel = useCallback(() => {
    if (view !== 'review') {
      previousViewRef.current = view === 'dashboard' ? 'dashboard' : 'session';
    }
    setInitialReviewFile(null); // No specific file, let ReviewPanel pick first
    setReviewPanelOpen(true);
    setView('review');
  }, [view]);

  const handleCloseReviewPanel = useCallback(() => {
    setReviewPanelOpen(false);
    setInitialReviewFile(null);
    setView(previousViewRef.current);
  }, []);

  useEffect(() => {
    if (!reviewPanelOpen) return;
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        handleCloseReviewPanel();
      }
    };
    window.addEventListener('keydown', handleKeyDown, true);
    return () => {
      window.removeEventListener('keydown', handleKeyDown, true);
    };
  }, [reviewPanelOpen, handleCloseReviewPanel]);

  const handleOpenEditor = useCallback(async (cwd: string, filePath?: string) => {
    try {
      await invoke('open_in_editor', {
        cwd,
        filePath,
        editor: settings.editor_executable || '',
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
    handleOpenEditor(activeSession.cwd);
  }, [sessions, activeSessionId, handleOpenEditor, showError]);

  const handleOpenEditorForReview = useCallback((filePath?: string) => {
    if (!activeDaemonSession?.directory) {
      showError('No repo directory available');
      return;
    }
    handleOpenEditor(activeDaemonSession.directory, filePath);
  }, [activeDaemonSession?.directory, handleOpenEditor, showError]);

  // Send code reference to the active Claude terminal
  const handleSendToClaude = useCallback((reference: string) => {
    if (!activeSessionId) return;
    ptyWrite({ id: activeSessionId, data: reference }).catch(console.error);
    // Focus the terminal so user can start typing
    setTimeout(() => {
      const handle = terminalRefs.current.get(activeSessionId);
      handle?.focus();
    }, 50);
  }, [activeSessionId]);

  // Terminal panel handlers for active session
  const handleOpenTerminalPanel = useCallback(() => {
    if (activeSessionId) openTerminalPanel(activeSessionId);
  }, [activeSessionId, openTerminalPanel]);

  const handleCollapseTerminalPanel = useCallback(() => {
    if (activeSessionId) collapseTerminalPanel(activeSessionId);
  }, [activeSessionId, collapseTerminalPanel]);

  const handleSetTerminalPanelHeight = useCallback((height: number) => {
    if (activeSessionId) setTerminalPanelHeight(activeSessionId, height);
  }, [activeSessionId, setTerminalPanelHeight]);

  const handleAddUtilityTerminal = useCallback((ptyId: string) => {
    if (activeSessionId) return addUtilityTerminal(activeSessionId, ptyId);
    return '';
  }, [activeSessionId, addUtilityTerminal]);

  const handleRemoveUtilityTerminal = useCallback((terminalId: string) => {
    if (activeSessionId) removeUtilityTerminal(activeSessionId, terminalId);
  }, [activeSessionId, removeUtilityTerminal]);

  const handleSetActiveUtilityTerminal = useCallback((terminalId: string) => {
    if (activeSessionId) setActiveUtilityTerminal(activeSessionId, terminalId);
  }, [activeSessionId, setActiveUtilityTerminal]);

  const handleRenameUtilityTerminal = useCallback((terminalId: string, title: string) => {
    if (activeSessionId) renameUtilityTerminal(activeSessionId, terminalId, title);
  }, [activeSessionId, renameUtilityTerminal]);

  // Use keyboard shortcuts hook
  useKeyboardShortcuts({
    onNewSession: handleNewSession,
    onNewWorktreeSession: handleNewWorktreeSession,
    onCloseSession: handleCloseCurrentSession,
    onToggleDrawer: toggleDrawer,
    onGoToDashboard: goToDashboard,
    onJumpToWaiting: handleJumpToWaiting,
    onSelectSession: handleSelectSessionByIndex,
    onPrevSession: handlePrevSession,
    onNextSession: handleNextSession,
    onToggleSidebar: toggleSidebarCollapse,
    onRefreshPRs: handleRefreshPRs,
    onOpenBranchPicker: () => {
      // Only open if we have an active session with git
      const localSession = sessions.find(s => s.id === activeSessionId);
      if (localSession) {
        const daemonSession = daemonSessions.find(ds => ds.id === localSession.id);
        if (daemonSession && (daemonSession.branch || daemonSession.main_repo)) {
          setBranchPickerOpen(true);
        }
      }
    },
    onQuickFind: view === 'session' ? handleOpenQuickFind : undefined,
    onForkSession: view === 'session' ? handleOpenForkDialog : undefined,
    onIncreaseFontSize: increaseScale,
    onDecreaseFontSize: decreaseScale,
    onResetFontSize: resetScale,
    enabled: !locationPickerOpen && !branchPickerOpen && !thumbsOpen && !forkDialogOpen && !reviewPanelOpen,
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
          settings={settings}
          onSelectSession={handleSelectSession}
          onNewSession={handleNewSession}
          onRefreshPRs={handleRefreshPRs}
          onOpenPR={handleOpenPR}
          onSetSetting={sendSetSetting}
        />
      </div>

      {/* Session view - always rendered to keep terminals alive */}
      <div className={`view-container ${view === 'session' ? 'visible' : 'hidden'}`}>
        <Sidebar
          sessions={enrichedLocalSessions}
          selectedId={activeSessionId}
          collapsed={sidebarCollapsed}
          onSelectSession={handleSelectSession}
          onNewSession={handleNewSession}
          onCloseSession={handleCloseSession}
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
                <Terminal
                  ref={setTerminalRef(session.id)}
                  fontSize={terminalFontSize}
                  onReady={handleTerminalReady(session.id)}
                  onResize={handleResize(session.id)}
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
          {activeSessionId && (() => {
            const activeSession = sessions.find(s => s.id === activeSessionId);
            if (!activeSession) return null;
            return (
              <UtilityTerminalPanel
                cwd={activeSession.cwd}
                panel={activeSession.terminalPanel}
                fontSize={terminalFontSize}
                onOpen={handleOpenTerminalPanel}
                onCollapse={handleCollapseTerminalPanel}
                onSetHeight={handleSetTerminalPanelHeight}
                onAddTerminal={handleAddUtilityTerminal}
                onRemoveTerminal={handleRemoveUtilityTerminal}
                onSetActiveTerminal={handleSetActiveUtilityTerminal}
                onRenameTerminal={handleRenameUtilityTerminal}
                focusRequestToken={utilityFocusRequestToken}
                enabled={!locationPickerOpen && !branchPickerOpen}
              />
            );
          })()}
        </div>
        <ChangesPanel
          branchDiffFiles={branchDiffFiles}
          branchDiffBaseRef={branchDiffBaseRef}
          branchDiffError={branchDiffError}
          attentionCount={attentionCount}
          selectedFile={null}
          onFileSelect={handleFileSelect}
          onAttentionClick={toggleDrawer}
          onReviewClick={handleOpenReviewPanel}
          onOpenEditor={activeSessionId ? handleOpenEditorForSession : undefined}
        />
        <AttentionDrawer
          isOpen={drawerOpen}
          onClose={closeDrawer}
          waitingSessions={waitingLocalSessions}
          prs={prs}
          onSelectSession={handleSelectSession}
        />
      </div>

      <LocationPicker
        isOpen={locationPickerOpen}
        onClose={closeLocationPicker}
        onSelect={handleLocationSelect}
        onGetRecentLocations={sendGetRecentLocations}
        onGetRepoInfo={getRepoInfo}
        onCreateWorktree={sendCreateWorktree}
        onDeleteWorktree={sendDeleteWorktree}
        onDeleteBranch={sendDeleteBranch}
        onError={showError}
        projectsDirectory={settings.projects_directory}
        agentAvailability={agentAvailability}
      />
      <BranchPicker
        isOpen={branchPickerOpen}
        onClose={() => setBranchPickerOpen(false)}
        session={(() => {
          const localSession = sessions.find(s => s.id === activeSessionId);
          if (!localSession) return null;
          return daemonSessions.find(ds => ds.id === localSession.id) || null;
        })()}
        onListBranches={sendListBranches}
        onListRemoteBranches={sendListRemoteBranches}
        onFetchRemotes={sendFetchRemotes}
        onSwitchBranch={sendSwitchBranch}
        onCheckDirty={sendCheckDirty}
        onStash={sendStash}
        onCommitWIP={sendCommitWIP}
        onCheckAttnStash={sendCheckAttnStash}
        onStashPop={sendStashPop}
        onGetDefaultBranch={sendGetDefaultBranch}
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
      <div className={`view-container review-view ${view === 'review' ? 'visible' : 'hidden'}`}>
        <ReviewPanel
          isOpen={reviewPanelOpen}
          gitStatus={gitStatus}
          repoPath={activeDaemonSession?.directory || ''}
          branch={activeDaemonSession?.branch || ''}
          baseBranch={branchDiffBaseRef || undefined}
          onClose={handleCloseReviewPanel}
          fetchDiff={fetchDiffForReview}
          sendGetBranchDiffFiles={sendGetBranchDiffFiles}
          sendFetchRemotes={sendFetchRemotes}
          getReviewState={getReviewState}
          markFileViewed={markFileViewed}
          onSendToClaude={activeSessionId ? handleSendToClaude : undefined}
          addComment={sendAddComment}
          updateComment={sendUpdateComment}
          resolveComment={sendResolveComment}
          wontFixComment={sendWontFixComment}
          deleteComment={sendDeleteComment}
          getComments={sendGetComments}
          sendStartReview={sendStartReview}
          sendCancelReview={sendCancelReview}
          reviewerEvents={reviewerEvents}
          reviewerRunning={reviewerRunning}
          reviewerError={reviewerError}
          agentComments={pendingAgentComments}
          agentResolvedCommentIds={agentResolvedCommentIds}
          initialSelectedFile={initialReviewFile || undefined}
          onOpenEditor={handleOpenEditorForReview}
        />
      </div>
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
    </div>
    </DaemonProvider>
  );
}

export default App;
