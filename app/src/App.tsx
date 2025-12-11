import { useCallback, useEffect, useRef, useState } from 'react';
import { Terminal as XTerm } from '@xterm/xterm';
import { onOpenUrl } from '@tauri-apps/plugin-deep-link';
import { invoke } from '@tauri-apps/api/core';
import { Terminal, TerminalHandle } from './components/Terminal';
import { Sidebar } from './components/Sidebar';
import { Dashboard } from './components/Dashboard';
import { AttentionDrawer } from './components/AttentionDrawer';
import { DrawerTrigger } from './components/DrawerTrigger';
import { LocationPicker } from './components/LocationPicker';
import { UndoToast } from './components/UndoToast';
import { DaemonProvider } from './contexts/DaemonContext';
import { useSessionStore } from './store/sessions';
import { useDaemonSocket } from './hooks/useDaemonSocket';
import { useDaemonStore } from './store/daemonSessions';
import { useKeyboardShortcuts } from './hooks/useKeyboardShortcuts';
import { useLocationHistory } from './hooks/useLocationHistory';
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
  } = useSessionStore();

  const {
    daemonSessions,
    setDaemonSessions,
    prs,
    setPRs,
    setRepoStates,
    isRepoMuted,
  } = useDaemonStore();

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
  const { sendPRAction, sendMutePR, sendMuteRepo, sendRefreshPRs, connectionError, hasReceivedInitialState } = useDaemonSocket({
    onSessionsUpdate: setDaemonSessions,
    onPRsUpdate: setPRs,
    onReposUpdate: setRepoStates,
  });

  // Handle deep-link spawn requests (attn://spawn?cwd=/path&label=name)
  useEffect(() => {
    const unlisten = onOpenUrl((urls) => {
      for (const urlStr of urls) {
        try {
          const url = new URL(urlStr);
          if (url.host === 'spawn') {
            const cwd = url.searchParams.get('cwd');
            const label = url.searchParams.get('label') || cwd?.split('/').pop() || 'session';
            if (cwd) {
              createSession(label, cwd);
            }
          }
        } catch (e) {
          console.error('Failed to parse deep-link URL:', e);
        }
      }
    });

    return () => {
      unlisten.then((fn) => fn());
    };
  }, [createSession]);

  // Filter out daemon sessions that match local sessions (by directory)
  // These are sessions we spawned ourselves
  const localDirs = new Set(sessions.map((s) => s.cwd));

  // Also deduplicate by directory (keep most recent by picking last)
  const seenDirs = new Map<string, typeof daemonSessions[0]>();
  for (const ds of daemonSessions) {
    seenDirs.set(ds.directory, ds);
  }

  const externalDaemonSessions = Array.from(seenDirs.values()).filter(
    (ds) => !localDirs.has(ds.directory)
  );

  // Enrich local sessions with daemon state (working/waiting from hooks)
  const enrichedLocalSessions = sessions.map((s) => {
    const daemonSession = daemonSessions.find((ds) => ds.directory === s.cwd);
    return {
      ...s,
      state: daemonSession?.state ?? s.state,
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

  // Location picker state management
  const [locationPickerOpen, setLocationPickerOpen] = useState(false);
  const { addToHistory } = useLocationHistory();

  // No auto-creation - user clicks "+" to start a session

  const handleNewSession = useCallback(() => {
    setLocationPickerOpen(true);
  }, []);

  const handleLocationSelect = useCallback(
    async (path: string) => {
      addToHistory(path);
      const folderName = path.split('/').pop() || 'session';
      const sessionId = await createSession(folderName, path);
      // Fit terminal after view becomes visible
      setTimeout(() => {
        const handle = terminalRefs.current.get(sessionId);
        handle?.fit();
        handle?.focus();
      }, 100);
    },
    [addToHistory, createSession]
  );

  const closeLocationPicker = useCallback(() => {
    setLocationPickerOpen(false);
  }, []);

  const handleCloseSession = useCallback(
    (id: string) => {
      terminalRefs.current.delete(id);
      closeSession(id);
    },
    [closeSession]
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
  const waitingLocalSessions = enrichedLocalSessions.filter((s) => s.state === 'waiting');
  const waitingExternalSessions = externalDaemonSessions.filter((s) => s.state === 'waiting');
  // Filter PRs using daemon mute state (individual PR mutes in p.muted, repo mutes via isRepoMuted)
  const activePRs = prs.filter((p) => !p.muted && !isRepoMuted(p.repo));
  const attentionCount = waitingLocalSessions.length + waitingExternalSessions.length + activePRs.length;

  // Keyboard shortcut handlers
  const handleJumpToWaiting = useCallback(() => {
    const waiting = enrichedLocalSessions.find((s) => s.state === 'waiting');
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

  // Use keyboard shortcuts hook
  useKeyboardShortcuts({
    onNewSession: handleNewSession,
    onCloseSession: handleCloseCurrentSession,
    onToggleDrawer: toggleDrawer,
    onGoToDashboard: goToDashboard,
    onJumpToWaiting: handleJumpToWaiting,
    onSelectSession: handleSelectSessionByIndex,
    onPrevSession: handlePrevSession,
    onNextSession: handleNextSession,
    onToggleSidebar: toggleSidebarCollapse,
    onRefreshPRs: sendRefreshPRs,
    enabled: true,
  });

  return (
    <DaemonProvider sendPRAction={sendPRAction} sendMutePR={sendMutePR} sendMuteRepo={sendMuteRepo}>
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
          daemonSessions={externalDaemonSessions}
          prs={prs}
          isLoading={!hasReceivedInitialState}
          onSelectSession={handleSelectSession}
          onNewSession={handleNewSession}
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
          {sessions.map((session) => (
            <div
              key={session.id}
              className={`terminal-wrapper ${session.id === activeSessionId ? 'active' : ''}`}
            >
              <Terminal
                ref={setTerminalRef(session.id)}
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
        <DrawerTrigger count={attentionCount} onClick={toggleDrawer} />
        <AttentionDrawer
          isOpen={drawerOpen}
          onClose={closeDrawer}
          waitingSessions={waitingLocalSessions}
          daemonSessions={externalDaemonSessions}
          prs={prs}
          onSelectSession={handleSelectSession}
        />
      </div>

      <LocationPicker
        isOpen={locationPickerOpen}
        onClose={closeLocationPicker}
        onSelect={handleLocationSelect}
      />
      <UndoToast />
    </div>
    </DaemonProvider>
  );
}

export default App;
