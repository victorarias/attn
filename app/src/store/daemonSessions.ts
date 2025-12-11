import { create } from 'zustand';
import { DaemonSession, DaemonPR, RepoState } from '../hooks/useDaemonSocket';

interface DaemonStore {
  // Sessions from daemon (cm-tracked sessions)
  daemonSessions: DaemonSession[];
  setDaemonSessions: (sessions: DaemonSession[]) => void;

  // PRs from daemon
  prs: DaemonPR[];
  setPRs: (prs: DaemonPR[]) => void;

  // Repo states from daemon (muted, collapsed)
  repoStates: RepoState[];
  setRepoStates: (repos: RepoState[]) => void;

  // Helper to check if a repo is muted
  isRepoMuted: (repo: string) => boolean;

  // Connection status
  isConnected: boolean;
  setConnected: (connected: boolean) => void;
}

export const useDaemonStore = create<DaemonStore>((set, get) => ({
  daemonSessions: [],
  setDaemonSessions: (sessions) => set({ daemonSessions: sessions }),

  prs: [],
  setPRs: (prs) => set({ prs }),

  repoStates: [],
  setRepoStates: (repos) => set({ repoStates: repos }),

  isRepoMuted: (repo) => {
    const state = get().repoStates.find(r => r.repo === repo);
    return state?.muted ?? false;
  },

  isConnected: false,
  setConnected: (connected) => set({ isConnected: connected }),
}));
