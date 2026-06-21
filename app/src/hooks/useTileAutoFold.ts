import { useLayoutEffect, useState, type RefObject } from 'react';

// Width thresholds (px) for the responsive fold ladder of a notebook tile. A tile
// can be narrow (docked beside terminals) or wide (a near-fullscreen pane), so it
// sheds its side panes as it shrinks: the context rail folds first, then the file
// tree, leaving the document pane with the most room.
//
//   width >= RAIL_FOLD_BELOW           → tree + document + rail   (wide)
//   TREE_FOLD_BELOW <= width < RAIL... → tree + document         (medium; rail folds)
//   width <  TREE_FOLD_BELOW           → document only           (narrow; both fold)
export const RAIL_FOLD_BELOW = 900;
export const TREE_FOLD_BELOW = 620;

export interface TileAutoFold {
  treeAutoFold: boolean;
  railAutoFold: boolean;
}

// foldsForWidth is the pure breakpoint decision, split out so the ladder is unit
// testable without a live ResizeObserver. A non-positive width is "not measured
// yet" — fold nothing, so a tile never flashes collapsed before its first real
// measurement.
export function foldsForWidth(width: number): TileAutoFold {
  if (width <= 0) return { treeAutoFold: false, railAutoFold: false };
  return {
    railAutoFold: width < RAIL_FOLD_BELOW,
    treeAutoFold: width < TREE_FOLD_BELOW,
  };
}

// useTileAutoFold drives a notebook surface's automatic edge-rail folds from the
// observed width of `ref` (the surface body). It feeds the fold seam's auto side
// (`folded = override ?? auto`), so a manual fold still wins; this only changes what
// "auto" resolves to.
//
// `enabled` gates the whole thing: the modal passes false and always gets
// {false,false} (its folds are manual-only, unchanged), so only a tile observes.
// The initial measurement runs in a layout effect — synchronously before paint — so
// a tile that mounts already-narrow renders folded on the first frame instead of
// flashing open then snapping shut.
export function useTileAutoFold(ref: RefObject<HTMLElement | null>, enabled: boolean): TileAutoFold {
  const [folds, setFolds] = useState<TileAutoFold>({ treeAutoFold: false, railAutoFold: false });

  useLayoutEffect(() => {
    if (!enabled) {
      // Keep a disabled surface (the modal) at its manual-only baseline.
      setFolds({ treeAutoFold: false, railAutoFold: false });
      return;
    }
    const el = ref.current;
    if (!el) return;

    // Apply a width immediately (before paint) and on every resize. We observe the
    // body CONTAINER, whose width is independent of how its inner panes fold, so
    // folding can't shrink the observed box and oscillate.
    const apply = (width: number) => {
      setFolds((prev) => {
        const next = foldsForWidth(width);
        return prev.treeAutoFold === next.treeAutoFold && prev.railAutoFold === next.railAutoFold
          ? prev
          : next;
      });
    };
    apply(el.clientWidth);

    if (typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver((entries) => {
      for (const entry of entries) {
        apply(entry.contentRect.width);
      }
    });
    observer.observe(el);
    return () => observer.disconnect();
  }, [ref, enabled]);

  return folds;
}
