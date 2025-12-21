import { useCallback, useEffect, useRef, useState } from 'react';
import { Terminal as XTerm } from '@xterm/xterm';
import { onOpenUrl, getCurrent } from '@tauri-apps/plugin-deep-link';
import { invoke } from '@tauri-apps/api/core';
import { Terminal, TerminalHandle } from './components/Terminal';
import { Sidebar } from './components/Sidebar';
import { Dashboard } from './components/Dashboard';
import { AttentionDrawer } from './components/AttentionDrawer';
import { LocationPicker } from './components/LocationPicker';
import { BranchPicker } from './components/BranchPicker';
import { UndoToast } from './components/UndoToast';
import { WorktreeCleanupPrompt } from './components/WorktreeCleanupPrompt';
import { ChangesPanel } from './components/ChangesPanel';
import { DiffOverlay } from './components/DiffOverlay';
import { UtilityTerminalPanel } from './components/UtilityTerminalPanel';
import { DaemonProvider } from './contexts/DaemonContext';
import { useSessionStore } from './store/sessions';
import { useDaemonSocket, DaemonWorktree, GitStatusUpdate } from './hooks/useDaemonSocket';
import { normalizeSessionState } from './types/sessionState';
import { useDaemonStore } from './store/daemonSessions';
import { usePRsNeedingAttention } from './hooks/usePRsNeedingAttention';
import { useKeyboardShortcuts } from './hooks/useKeyboardShortcuts';
import { useUIScale } from './hooks/useUIScale';
import { getRepoName } from './utils/repo';
import './App.css';

function App() {
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
  } = useSessionStore();

  const {
    daemonSessions,
    setDaemonSessions,
    prs,
    setPRs,
    setRepoStates,
  } = useDaemonStore();

  // UI scale for font sizing (Cmd+/Cmd-)
  const { scale, increaseScale, decreaseScale, resetScale } = useUIScale();
  const terminalFontSize = Math.round(14 * scale);
  const diffFontSize = Math.round(12 * scale);

  // Track PR refresh state for progress indicator
  const [isRefreshingPRs, setIsRefreshingPRs] = useState(false);
  const [refreshError, setRefreshError] = useState<string | null>(null);

  // Settings state
  const [settings, setSettings] = useState<Record<string, string>>({});

  // Worktrees state (used by WorktreeCleanupPrompt)
  const [, setWorktrees] = useState<DaemonWorktree[]>([]);

  // Worktree cleanup prompt state
  const [closedWorktree, setClosedWorktree] = useState<{ path: string; branch?: string } | null>(null);
  const [alwaysKeepWorktrees, setAlwaysKeepWorktrees] = useState(() => {
    const stored = localStorage.getItem('alwaysKeepWorktrees');
    return stored === 'true';
  });

  // Git status state
  const [gitStatus, setGitStatus] = useState<GitStatusUpdate | null>(null);

  // Diff overlay state
  const [diffOverlay, setDiffOverlay] = useState<{
    isOpen: boolean;
    path: string;
    staged: boolean;
    index: number;
  }>({ isOpen: false, path: '', staged: false, index: 0 });

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

  // Connect to daemon WebSocket
  const { sendPRAction, sendMutePR, sendMuteRepo, sendPRVisited, sendRefreshPRs, sendClearSessions, sendUnregisterSession, sendSetSetting, sendCreateWorktree, sendDeleteWorktree, sendGetRecentLocations, sendListBranches, sendSwitchBranch, sendCreateWorktreeFromBranch, sendCheckDirty, sendStash, sendStashPop, sendCheckAttnStash, sendCommitWIP, sendGetDefaultBranch, sendFetchRemotes, sendListRemoteBranches, sendSubscribeGitStatus, sendUnsubscribeGitStatus, sendGetFileDiff, getRepoInfo, connectionError, hasReceivedInitialState, rateLimit } = useDaemonSocket({
    onSessionsUpdate: setDaemonSessions,
    onPRsUpdate: setPRs,
    onReposUpdate: setRepoStates,
    onSettingsUpdate: setSettings,
    onWorktreesUpdate: setWorktrees,
    onGitStatusUpdate: setGitStatus,
  });

  // Clear stale daemon sessions on app start
  const hasClearedSessions = useRef(false);
  useEffect(() => {
    if (hasReceivedInitialState && !hasClearedSessions.current) {
      hasClearedSessions.current = true;
      sendClearSessions();
    }
  }, [hasReceivedInitialState, sendClearSessions]);

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
  const enrichedLocalSessions = sessions.map((s) => {
    const daemonSession = daemonSessions.find((ds) => ds.directory === s.cwd);
    const rawState = daemonSession?.state ?? s.state;
    return {
      ...s,
      state: normalizeSessionState(rawState),
      branch: daemonSession?.branch,
      isWorktree: daemonSession?.is_worktree,
    };
  });

  const terminalRefs = useRef<Map<string, TerminalHandle>>(new Map());

  // View state management
  const [view, setView] = useState<'dashboard' | 'session'>('dashboard');

  // When activeSessionId changes, update view
  useEffect(() => {
    if (activeSessionId) {
      setView('session');
    }
  }, [activeSessionId]);

  // Subscribe to git status for active session
  useEffect(() => {
    const activeLocalSession = sessions.find((s) => s.id === activeSessionId);
    if (activeLocalSession?.cwd && view === 'session') {
      const daemonSession = daemonSessions.find((ds) => ds.directory === activeLocalSession.cwd);
      if (daemonSession) {
        sendSubscribeGitStatus(daemonSession.directory);
        return () => {
          sendUnsubscribeGitStatus();
          setGitStatus(null);
        };
      }
    } else {
      setGitStatus(null);
    }
  }, [activeSessionId, sessions, daemonSessions, view, sendSubscribeGitStatus, sendUnsubscribeGitStatus]);

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

  // No auto-creation - user clicks "+" to start a session

  const handleNewSession = useCallback(() => {
    setLocationPickerOpen(true);
  }, []);

  const handleNewWorktreeSession = useCallback(() => {
    setLocationPickerOpen(true);
  }, []);

  const handleLocationSelect = useCallback(
    async (path: string) => {
      // Note: Location is automatically tracked by daemon when session registers
      const folderName = path.split('/').pop() || 'session';
      const sessionId = await createSession(folderName, path);
      // Fit terminal after view becomes visible
      setTimeout(() => {
        const handle = terminalRefs.current.get(sessionId);
        handle?.fit();
        handle?.focus();
      }, 100);
    },
    [createSession]
  );

  const closeLocationPicker = useCallback(() => {
    setLocationPickerOpen(false);
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

      // Unregister from daemon by finding the daemon session with matching directory
      if (session?.cwd) {
        const daemonSession = daemonSessions.find(ds => ds.directory === session.cwd);
        if (daemonSession) {
          sendUnregisterSession(daemonSession.id);
        }
      }

      terminalRefs.current.delete(id);
      closeSession(id);
    },
    [closeSession, enrichedLocalSessions, alwaysKeepWorktrees, daemonSessions, sendUnregisterSession]
  );

  const handleSelectSession = useCallback(
    (id: string) => {
      setActiveSession(id);
      // Fit and focus the terminal after a short delay (allows CSS to apply)
      setTimeout(() => {
        const handle = terminalRefs.current.get(id);
        handle?.fit();
        handle?.focus();
      }, 50);
    },
    [setActiveSession]
  );

  // Handle opening a PR in a worktree
  const handleOpenPR = useCallback(
    async (pr: { repo: string; number: number; title: string; head_branch?: string }) => {
      console.log(`[App] Open PR requested: ${pr.repo}#${pr.number} - ${pr.title}`);

      // Check if projects_directory is configured
      const projectsDir = settings.projects_directory;
      if (!projectsDir) {
        alert('Please configure your Projects Directory in Settings first.\n\nThis tells the app where to find your local git repositories.');
        return;
      }

      // Check if PR has head_branch
      if (!pr.head_branch) {
        alert('PR branch information not available.\n\nTry refreshing PRs (⌘R) to fetch branch details.');
        return;
      }

      const repoName = getRepoName(pr.repo);
      // Normalize path: remove trailing slash from projectsDir if present
      const normalizedProjectsDir = projectsDir.replace(/\/+$/, '');
      const localRepoPath = `${normalizedProjectsDir}/${repoName}`;

      console.log(`[App] Creating worktree for ${pr.repo}#${pr.number} branch ${pr.head_branch} in ${localRepoPath}`);

      try {
        // Fetch remote first to ensure the PR branch is available locally
        console.log(`[App] Fetching remotes for ${localRepoPath}`);
        await sendFetchRemotes(localRepoPath);

        // Create worktree from the remote branch (PR branches already exist on remote)
        // Using origin/<branch> to track the remote branch
        const remoteBranch = `origin/${pr.head_branch}`;
        console.log(`[App] Creating worktree from remote branch ${remoteBranch}`);
        const result = await sendCreateWorktreeFromBranch(localRepoPath, remoteBranch);

        if (result.success && result.path) {
          console.log(`[App] Worktree created at ${result.path}`);

          // Create a new session in the worktree directory
          const label = `${repoName}#${pr.number}`;
          const sessionId = await createSession(label, result.path);

          // Fit terminal after view becomes visible
          setTimeout(() => {
            const handle = terminalRefs.current.get(sessionId);
            handle?.fit();
            handle?.focus();
          }, 100);
        } else {
          throw new Error(result.error || 'Failed to create worktree');
        }
      } catch (err) {
        console.error('[App] Failed to open PR:', err);
        const errorMsg = err instanceof Error ? err.message : 'Unknown error';

        // Check for common errors
        if (errorMsg.includes('not a git repository') || errorMsg.includes('does not exist')) {
          alert(`Could not find local repository at:\n${localRepoPath}\n\nMake sure the repository is cloned in your Projects Directory.`);
        } else if (errorMsg.includes('already exists')) {
          // Worktree already exists - find it and create session there
          const worktreePath = `${localRepoPath}/../${repoName}-${pr.head_branch}`;
          console.log(`[App] Worktree may already exist, trying to create session at ${worktreePath}`);
          alert(`A worktree for this branch may already exist.\n\nError: ${errorMsg}`);
        } else {
          alert(`Failed to open PR: ${errorMsg}`);
        }
      }
    },
    [settings, sendFetchRemotes, sendCreateWorktreeFromBranch, createSession]
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
      resizeSession(sessionId, cols, rows);
    },
    [resizeSession]
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

  // Diff overlay handlers
  const handleFileSelect = useCallback((path: string, staged: boolean) => {
    const allFiles = [
      ...(gitStatus?.staged || []).map(f => ({ ...f, staged: true })),
      ...(gitStatus?.unstaged || []).map(f => ({ ...f, staged: false })),
      ...(gitStatus?.untracked || []).map(f => ({ ...f, staged: false })),
    ];
    const index = allFiles.findIndex(f => f.path === path && f.staged === staged);
    setDiffOverlay({ isOpen: true, path, staged, index: Math.max(0, index) });
  }, [gitStatus]);

  const handleDiffClose = useCallback(() => {
    setDiffOverlay({ isOpen: false, path: '', staged: false, index: 0 });
  }, []);

  const handleDiffNav = useCallback((direction: 'prev' | 'next') => {
    const allFiles = [
      ...(gitStatus?.staged || []).map(f => ({ ...f, staged: true })),
      ...(gitStatus?.unstaged || []).map(f => ({ ...f, staged: false })),
      ...(gitStatus?.untracked || []).map(f => ({ ...f, staged: false })),
    ];
    const newIndex = direction === 'prev'
      ? Math.max(0, diffOverlay.index - 1)
      : Math.min(allFiles.length - 1, diffOverlay.index + 1);
    const file = allFiles[newIndex];
    if (file) {
      setDiffOverlay({ isOpen: true, path: file.path, staged: file.staged, index: newIndex });
    }
  }, [gitStatus, diffOverlay.index]);

  const fetchDiff = useCallback(async () => {
    const activeLocalSession = sessions.find((s) => s.id === activeSessionId);
    if (!activeLocalSession?.cwd) throw new Error('No active session');
    const daemonSession = daemonSessions.find((ds) => ds.directory === activeLocalSession.cwd);
    if (!daemonSession) throw new Error('No daemon session found');
    return sendGetFileDiff(daemonSession.directory, diffOverlay.path, diffOverlay.staged);
  }, [sessions, activeSessionId, daemonSessions, diffOverlay.path, diffOverlay.staged, sendGetFileDiff]);

  const totalDiffFiles = (gitStatus?.staged?.length || 0) +
    (gitStatus?.unstaged?.length || 0) +
    (gitStatus?.untracked?.length || 0);

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
        const daemonSession = daemonSessions.find(ds => ds.directory === localSession.cwd);
        if (daemonSession && (daemonSession.branch || daemonSession.main_repo)) {
          setBranchPickerOpen(true);
        }
      }
    },
    onIncreaseFontSize: increaseScale,
    onDecreaseFontSize: decreaseScale,
    onResetFontSize: resetScale,
    enabled: !locationPickerOpen && !branchPickerOpen,
  });

  return (
    <DaemonProvider sendPRAction={sendPRAction} sendMutePR={sendMutePR} sendMuteRepo={sendMuteRepo} sendPRVisited={sendPRVisited}>
    <div className="app">
      {/* Error banner for version mismatch */}
      {connectionError && (
        <div className="connection-error-banner">
          {connectionError}
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
                enabled={!locationPickerOpen && !branchPickerOpen}
              />
            );
          })()}
        </div>
        <ChangesPanel
          gitStatus={gitStatus}
          attentionCount={attentionCount}
          selectedFile={diffOverlay.isOpen ? diffOverlay.path : null}
          onFileSelect={handleFileSelect}
          onAttentionClick={toggleDrawer}
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
      />
      <BranchPicker
        isOpen={branchPickerOpen}
        onClose={() => setBranchPickerOpen(false)}
        session={(() => {
          const localSession = sessions.find(s => s.id === activeSessionId);
          if (!localSession) return null;
          return daemonSessions.find(ds => ds.directory === localSession.cwd) || null;
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
      <DiffOverlay
        isOpen={diffOverlay.isOpen}
        filePath={diffOverlay.path}
        fileIndex={diffOverlay.index}
        totalFiles={totalDiffFiles}
        fontSize={diffFontSize}
        onClose={handleDiffClose}
        onPrev={() => handleDiffNav('prev')}
        onNext={() => handleDiffNav('next')}
        fetchDiff={fetchDiff}
      />
    </div>
    </DaemonProvider>
  );
}

export default App;
