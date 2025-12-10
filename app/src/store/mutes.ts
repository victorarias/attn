// app/src/store/mutes.ts
import { create } from 'zustand';
import { persist } from 'zustand/middleware';

interface MuteState {
  mutedPRs: Set<string>;    // PR IDs like "owner/repo#123"
  mutedRepos: Set<string>;  // Repo names like "owner/repo"
  undoStack: Array<{ type: 'pr' | 'repo'; id: string; timestamp: number }>;
}

interface MuteActions {
  mutePR: (prId: string) => void;
  unmutePR: (prId: string) => void;
  muteRepo: (repo: string) => void;
  unmuteRepo: (repo: string) => void;
  isPRMuted: (prId: string, repo: string) => boolean;
  isRepoMuted: (repo: string) => boolean;
  processUndo: () => { type: 'pr' | 'repo'; id: string } | null;
}

const UNDO_WINDOW_MS = 5000;

export const useMuteStore = create<MuteState & MuteActions>()(
  persist(
    (set, get) => ({
      mutedPRs: new Set(),
      mutedRepos: new Set(),
      undoStack: [],

      mutePR: (prId: string) => {
        const now = Date.now();
        set((state) => ({
          mutedPRs: new Set([...state.mutedPRs, prId]),
          // Filter out expired entries before adding new one
          undoStack: [
            ...state.undoStack.filter(u => now - u.timestamp < UNDO_WINDOW_MS),
            { type: 'pr', id: prId, timestamp: now }
          ],
        }));
      },

      unmutePR: (prId: string) => {
        set((state) => ({
          mutedPRs: new Set([...state.mutedPRs].filter(id => id !== prId)),
        }));
      },

      muteRepo: (repo: string) => {
        const now = Date.now();
        set((state) => ({
          mutedRepos: new Set([...state.mutedRepos, repo]),
          // Filter out expired entries before adding new one
          undoStack: [
            ...state.undoStack.filter(u => now - u.timestamp < UNDO_WINDOW_MS),
            { type: 'repo', id: repo, timestamp: now }
          ],
        }));
      },

      unmuteRepo: (repo: string) => {
        set((state) => ({
          mutedRepos: new Set([...state.mutedRepos].filter(r => r !== repo)),
        }));
      },

      isPRMuted: (prId: string, repo: string) => {
        const state = get();
        return state.mutedPRs.has(prId) || state.mutedRepos.has(repo);
      },

      isRepoMuted: (repo: string) => {
        return get().mutedRepos.has(repo);
      },

      processUndo: () => {
        const state = get();
        const now = Date.now();

        // Find most recent undo within window
        const validUndo = [...state.undoStack]
          .reverse()
          .find(u => now - u.timestamp < UNDO_WINDOW_MS);

        if (validUndo) {
          // Remove from undo stack and unmute
          set((s) => ({
            undoStack: s.undoStack.filter(u => u !== validUndo),
            mutedPRs: validUndo.type === 'pr'
              ? new Set([...s.mutedPRs].filter(id => id !== validUndo.id))
              : s.mutedPRs,
            mutedRepos: validUndo.type === 'repo'
              ? new Set([...s.mutedRepos].filter(r => r !== validUndo.id))
              : s.mutedRepos,
          }));
          return validUndo;
        }
        return null;
      },
    }),
    {
      name: 'attn-mutes',
      // Custom serialization for Sets
      storage: {
        getItem: (name) => {
          const str = localStorage.getItem(name);
          if (!str) return null;
          const data = JSON.parse(str);
          return {
            state: {
              ...data.state,
              mutedPRs: new Set(data.state.mutedPRs || []),
              mutedRepos: new Set(data.state.mutedRepos || []),
              // Clear expired undo items on load - they're only valid for 5 seconds
              undoStack: [],
            },
          };
        },
        setItem: (name, value) => {
          const data = {
            state: {
              ...value.state,
              mutedPRs: [...value.state.mutedPRs],
              mutedRepos: [...value.state.mutedRepos],
              undoStack: value.state.undoStack,
            },
          };
          localStorage.setItem(name, JSON.stringify(data));
        },
        removeItem: (name) => localStorage.removeItem(name),
      },
    }
  )
);
