import { useCallback, useState } from 'react';
import { Terminal as XTerm } from '@xterm/xterm';
import { Terminal } from './components/Terminal';
import { Sidebar } from './components/Sidebar';
import { usePty } from './hooks/usePty';
import './App.css';

// Mock sessions for now
const mockSessions = [
  { id: '1', label: 'claude-manager', state: 'waiting' as const },
  { id: '2', label: 'other-project', state: 'working' as const },
];

function App() {
  const [selectedSession, setSelectedSession] = useState<string | null>('1');

  const { connect, resize } = usePty({
    command: 'claude',
    args: [],
    cwd: '/',
  });

  const handleTerminalReady = useCallback((terminal: XTerm) => {
    connect(terminal);
  }, [connect]);

  const handleResize = useCallback((cols: number, rows: number) => {
    resize(cols, rows);
  }, [resize]);

  return (
    <div className="app">
      <Sidebar
        sessions={mockSessions}
        selectedId={selectedSession}
        onSelectSession={setSelectedSession}
      />
      <div className="terminal-pane">
        <Terminal onReady={handleTerminalReady} onResize={handleResize} />
      </div>
    </div>
  );
}

export default App;
