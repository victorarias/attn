import { create } from 'zustand';
import { spawn, IPty } from 'tauri-pty';
import { Terminal } from '@xterm/xterm';

export interface Session {
  id: string;
  label: string;
  state: 'working' | 'waiting';
  pty: IPty | null;
  terminal: Terminal | null;
  cwd: string;
}

interface SessionStore {
  sessions: Session[];
  activeSessionId: string | null;

  // Actions
  createSession: (label: string, cwd: string) => Promise<string>;
  closeSession: (id: string) => void;
  setActiveSession: (id: string) => void;
  connectTerminal: (id: string, terminal: Terminal) => Promise<void>;
  resizeSession: (id: string, cols: number, rows: number) => void;
}

let sessionCounter = 0;

export const useSessionStore = create<SessionStore>((set, get) => ({
  sessions: [],
  activeSessionId: null,

  createSession: async (label: string, cwd: string) => {
    const id = `session-${++sessionCounter}`;
    const session: Session = {
      id,
      label,
      state: 'working',
      pty: null,
      terminal: null,
      cwd,
    };

    set((state) => ({
      sessions: [...state.sessions, session],
      activeSessionId: id,
    }));

    return id;
  },

  closeSession: (id: string) => {
    const { sessions, activeSessionId } = get();
    const session = sessions.find((s) => s.id === id);

    if (session?.pty) {
      session.pty.kill();
    }
    if (session?.terminal) {
      session.terminal.dispose();
    }

    const newSessions = sessions.filter((s) => s.id !== id);
    let newActiveId = activeSessionId;

    if (activeSessionId === id) {
      newActiveId = newSessions.length > 0 ? newSessions[0].id : null;
    }

    set({
      sessions: newSessions,
      activeSessionId: newActiveId,
    });
  },

  setActiveSession: (id: string) => {
    set({ activeSessionId: id });
  },

  connectTerminal: async (id: string, terminal: Terminal) => {
    const { sessions } = get();
    const session = sessions.find((s) => s.id === id);
    if (!session) return;

    // Spawn PTY using cm wrapper (registers with daemon, sets up hooks)
    // -y flag auto-accepts prompts
    const pty = await spawn('cm', ['-y'], {
      cols: terminal.cols,
      rows: terminal.rows,
      cwd: session.cwd,
    });

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
      // Optionally auto-close session
      // get().closeSession(id);
    });

    // Update session with PTY and terminal refs
    set((state) => ({
      sessions: state.sessions.map((s) =>
        s.id === id ? { ...s, pty, terminal } : s
      ),
    }));
  },

  resizeSession: (id: string, cols: number, rows: number) => {
    const { sessions } = get();
    const session = sessions.find((s) => s.id === id);
    session?.pty?.resize(cols, rows);
  },
}));
