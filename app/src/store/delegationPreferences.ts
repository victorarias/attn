import { create } from 'zustand';

export const useDelegationPreferencesPush = create<{
  revision: number | null;
  version: number;
  push: (revision: number) => void;
  clear: () => void;
}>((set) => ({
  revision: null,
  version: 0,
  push: (revision) => set((state) => ({ revision, version: state.version + 1 })),
  clear: () => set((state) => ({ revision: null, version: state.version + 1 })),
}));
