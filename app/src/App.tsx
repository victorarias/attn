import { useCallback } from 'react';
import { Terminal as XTerm } from '@xterm/xterm';
import { Terminal } from './components/Terminal';
import { usePty } from './hooks/usePty';
import './App.css';

function App() {
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
      <div className="terminal-pane">
        <Terminal onReady={handleTerminalReady} onResize={handleResize} />
      </div>
    </div>
  );
}

export default App;
