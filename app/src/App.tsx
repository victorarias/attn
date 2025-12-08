import { useCallback, useEffect, useRef, useState } from 'react';
import { Terminal as XTerm } from '@xterm/xterm';
import { open } from '@tauri-apps/plugin-dialog';
import { onOpenUrl } from '@tauri-apps/plugin-deep-link';
import { Terminal, TerminalHandle } from './components/Terminal';
import { Sidebar } from './components/Sidebar';
import { useSessionStore } from './store/sessions';
import { useDaemonSocket } from './hooks/useDaemonSocket';
import { useDaemonStore } from './store/daemonSessions';
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
    isConnected,
  } = useDaemonStore();

  // Connect to daemon WebSocket
  useDaemonSocket({
    onSessionsUpdate: setDaemonSessions,
    onPRsUpdate: setPRs,
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

  // View state management (will be used in Task 3)
  // @ts-expect-error - will be used in Task 3
  const [view, setView] = useState<'dashboard' | 'session'>('dashboard');

  // When activeSessionId changes, update view
  useEffect(() => {
    if (activeSessionId) {
      setView('session');
    }
  }, [activeSessionId]);

  // Add function to go to dashboard (will be used in Task 3)
  // @ts-expect-error - will be used in Task 3
  const goToDashboard = useCallback(() => {
    setActiveSession(null);
    setView('dashboard');
  }, [setActiveSession]);

  // No auto-creation - user clicks "+" to start a session

  const handleNewSession = useCallback(async () => {
    // Open folder picker
    const folder = await open({
      directory: true,
      multiple: false,
      title: 'Select working directory for new session',
    });

    if (!folder) return; // User cancelled

    // Use folder name as label
    const folderName = folder.split('/').pop() || 'session';
    const label = folderName;
    await createSession(label, folder);
  }, [createSession]);

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

  return (
    <div className="app">
      <Sidebar
        localSessions={enrichedLocalSessions}
        selectedId={activeSessionId}
        onSelectSession={handleSelectSession}
        onNewSession={handleNewSession}
        onCloseSession={handleCloseSession}
        daemonSessions={externalDaemonSessions}
        prs={prs}
        isConnected={isConnected}
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
    </div>
  );
}

export default App;
