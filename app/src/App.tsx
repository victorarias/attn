import { useCallback, useRef, useEffect } from 'react';
import { Terminal as XTerm } from '@xterm/xterm';
import { Terminal, TerminalHandle } from './components/Terminal';
import { Sidebar } from './components/Sidebar';
import { useSessionStore } from './store/sessions';
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

  const terminalRefs = useRef<Map<string, TerminalHandle>>(new Map());

  // Create initial session on mount
  useEffect(() => {
    if (sessions.length === 0) {
      createSession('claude-manager', '/');
    }
  }, []);

  const handleNewSession = useCallback(async () => {
    const label = `session-${sessions.length + 1}`;
    await createSession(label, '/');
  }, [createSession, sessions.length]);

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
      // Focus the terminal after a short delay
      setTimeout(() => {
        terminalRefs.current.get(id)?.focus();
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
        sessions={sessions}
        selectedId={activeSessionId}
        onSelectSession={handleSelectSession}
        onNewSession={handleNewSession}
        onCloseSession={handleCloseSession}
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
