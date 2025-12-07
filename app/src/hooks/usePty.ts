import { useRef, useCallback, useEffect } from 'react';
import { spawn, IPty } from 'tauri-pty';
import { Terminal } from '@xterm/xterm';

interface UsePtyOptions {
  command?: string;
  args?: string[];
  cwd?: string;
}

export function usePty(options: UsePtyOptions = {}) {
  const ptyRef = useRef<IPty | null>(null);
  const terminalRef = useRef<Terminal | null>(null);

  const connect = useCallback(async (terminal: Terminal) => {
    terminalRef.current = terminal;

    const command = options.command || '/bin/zsh';
    const args = options.args || [];
    const cwd = options.cwd || '/';

    const pty = await spawn(command, args, {
      cols: terminal.cols,
      rows: terminal.rows,
      cwd,
    });

    ptyRef.current = pty;

    // PTY output -> terminal
    pty.onData((data: string) => {
      terminal.write(data);
    });

    // Terminal input -> PTY
    terminal.onData((data: string) => {
      pty.write(data);
    });

    // Handle PTY exit
    pty.onExit(() => {
      terminal.write('\r\n[Process exited]\r\n');
    });
  }, [options.command, options.args, options.cwd]);

  // Resize handler
  const resize = useCallback((cols: number, rows: number) => {
    ptyRef.current?.resize(cols, rows);
  }, []);

  // Cleanup on unmount
  useEffect(() => {
    return () => {
      ptyRef.current?.kill();
    };
  }, []);

  return { connect, resize };
}
