import { create } from 'zustand';
import { DaemonSession, DaemonPR } from '../hooks/useDaemonSocket';

interface DaemonStore {
  // Sessions from daemon (cm-tracked sessions)
  daemonSessions: DaemonSession[];
  setDaemonSessions: (sessions: DaemonSession[]) => void;

  // PRs from daemon
  prs: DaemonPR[];
  setPRs: (prs: DaemonPR[]) => void;

  // Connection status
  isConnected: boolean;
  setConnected: (connected: boolean) => void;
}

export const useDaemonStore = create<DaemonStore>((set) => ({
  daemonSessions: [],
  setDaemonSessions: (sessions) => set({ daemonSessions: sessions }),

  prs: [],
  setPRs: (prs) => set({ prs }),

  isConnected: false,
  setConnected: (connected) => set({ isConnected: connected }),
}));
