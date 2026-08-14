import { create } from 'zustand';
import { DaemonSession, DaemonPR, RepoState, AuthorState, TicketRow, Seed, AppRegistryEntry } from '../hooks/useDaemonSocket';

interface DaemonStore {
  // Sessions from daemon (attn-tracked sessions)
  daemonSessions: DaemonSession[];
  setDaemonSessions: (sessions: DaemonSession[]) => void;

  // Work-tracker board: non-archived tickets (bare rows). The detail view fetches
  // the full record on demand via get_ticket.
  tickets: TicketRow[];
  setTickets: (tickets: TicketRow[]) => void;

  // The garden: every seed the home daemon holds, newest first. The panel scopes
  // to the workspace it shows, so switching workspaces needs no round trip.
  // `seedsTotal` is how many the garden holds; it exceeds seeds.length only when
  // the garden outgrew one push, and then the panel says what it is missing.
  seeds: Seed[];
  seedsTotal: number;
  setSeeds: (seeds: Seed[], total: number) => void;

  // PRs from daemon
  prs: DaemonPR[];
  setPRs: (prs: DaemonPR[]) => void;

  // Every app this daemon has installed, with the views its serving version
  // declares. Two surfaces far apart read it — the dock picker offers the views,
  // and a docked tile resolves its bundle URL and watches for the content hash
  // to move — so it lives here rather than travelling down as a prop.
  apps: AppRegistryEntry[];
  setApps: (apps: AppRegistryEntry[]) => void;

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

  seeds: [],
  seedsTotal: 0,
  setSeeds: (seeds, total) => set({ seeds, seedsTotal: total }),

  prs: [],
  setPRs: (prs) => set({ prs }),

  apps: [],
  setApps: (apps) => set({ apps }),

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
