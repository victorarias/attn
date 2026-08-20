import { create } from 'zustand';

// Where the reader is in the garden, shared by every surface that draws it. The
// dock and the fullscreen surface are the same walk at two sizes — one draws the
// place you are in, the other draws as many levels as its width holds beside it —
// so the trail cannot belong to either of them. Moving between the sizes keeps
// your depth and your place instead of restarting the walk at the root.
interface GardenWalk {
  /** Seed ids from the garden down to the place you are in. Empty is the root. */
  trail: string[];
  setTrail: (next: string[] | ((prev: string[]) => string[])) => void;
}

export const useGardenWalk = create<GardenWalk>((set) => ({
  trail: [],
  setTrail: (next) =>
    set((state) => ({ trail: typeof next === 'function' ? next(state.trail) : next })),
}));

// Scroll offsets, keyed by place. Not store state: nothing renders from it, it is
// read once in a layout effect and written on every scroll event, so putting it
// through the reducer would repaint the panel on every wheel tick.
export const gardenScrollMemory = new Map<string, number>();
