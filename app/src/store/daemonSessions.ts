import { create } from 'zustand';
import { DaemonSession, DaemonPR, RepoState, AuthorState, Ticket } from '../hooks/useDaemonSocket';

interface DaemonStore {
  // Sessions from daemon (attn-tracked sessions)
  daemonSessions: DaemonSession[];
  setDaemonSessions: (sessions: DaemonSession[]) => void;

  // Work-tracker board: non-archived tickets (bare rows). The detail view fetches
  // the full record on demand via get_ticket.
  tickets: Ticket[];
  setTickets: (tickets: Ticket[]) => void;

  // PRs from daemon
  prs: DaemonPR[];
  setPRs: (prs: DaemonPR[]) => void;

  // Repo states from daemon (muted, collapsed)
  repoStates: RepoState[];
  setRepoStates: (repos: RepoState[]) => void;

  // Author states from daemon (muted PR authors like bots)
  authorStates: AuthorState[];
  setAuthorStates: (authors: AuthorState[]) => void;

  // Helper to check if a repo is muted
  isRepoMuted: (repo: string) => boolean;

  // Helper to check if a PR author is muted
  isAuthorMuted: (author: string) => boolean;

  // Connection status
  isConnected: boolean;
  setConnected: (connected: boolean) => void;
}

export const useDaemonStore = create<DaemonStore>((set, get) => ({
  daemonSessions: [],
  setDaemonSessions: (sessions) => set({ daemonSessions: sessions }),

  tickets: [],
  setTickets: (tickets) => set({ tickets }),

  prs: [],
  setPRs: (prs) => set({ prs }),

  repoStates: [],
  setRepoStates: (repos) => set({ repoStates: repos }),

  authorStates: [],
  setAuthorStates: (authors) => set({ authorStates: authors }),

  isRepoMuted: (repo) => {
    const state = get().repoStates.find(r => r.repo === repo);
    return state?.muted ?? false;
  },

  isAuthorMuted: (author) => {
    const state = get().authorStates.find(a => a.author === author);
    return state?.muted ?? false;
  },

  isConnected: false,
  setConnected: (connected) => set({ isConnected: connected }),
}));
