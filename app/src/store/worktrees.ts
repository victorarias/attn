import { create } from 'zustand';
import type { Worktree, WorktreeRepository, WorktreeSweepEntry } from '../types/generated';

// Pushes land here rather than in App: only the Worktrees panel subscribes, so a
// refresh pass over a large registry cannot repaint the shell.
interface WorktreeStore {
  worktrees: Worktree[];
  repositories: WorktreeRepository[];
  sweepLog: WorktreeSweepEntry[];
  loaded: boolean;
  replace: (worktrees: Worktree[], repositories: WorktreeRepository[]) => void;
  replaceSweepLog: (entries: WorktreeSweepEntry[]) => void;
  observe: (worktree: Worktree) => void;
  swept: (entry: WorktreeSweepEntry) => void;
  clear: () => void;
}

// A removal arrives as one sweep entry, so the log grows one row at a time between
// fetches; the panel pages the rest from the daemon.
const maxPushedSweepEntries = 200;

export const useWorktreeStore = create<WorktreeStore>((set) => ({
  worktrees: [],
  repositories: [],
  sweepLog: [],
  loaded: false,
  replace: (worktrees, repositories) => set({ worktrees, repositories, loaded: true }),
  replaceSweepLog: (entries) => set({ sweepLog: entries }),
  observe: (worktree) =>
    set((store) => {
      const index = store.worktrees.findIndex((row) => row.path === worktree.path);
      if (index < 0) {
        return { worktrees: [...store.worktrees, worktree] };
      }
      const next = store.worktrees.slice();
      next[index] = worktree;
      return { worktrees: next };
    }),
  swept: (entry) =>
    set((store) => ({
      worktrees: store.worktrees.filter((row) => row.path !== entry.path),
      sweepLog: [entry, ...store.sweepLog.filter((row) => row.id !== entry.id)]
        .slice(0, maxPushedSweepEntries),
    })),
  clear: () => set({ worktrees: [], repositories: [], sweepLog: [], loaded: false }),
}));
