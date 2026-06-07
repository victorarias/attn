// Grid layout selection: the shape of the grid is either AUTO (a near-square
// that fits every live tile — the zero-config default) or a FIXED rows×cols the
// user picked from the sidebar square-picker. A fixed shape smaller than the
// live tile count shows only the first rows×cols tiles; the rest are off-board
// until the user removes a tile or enlarges the grid (the chosen overflow model).
//
// This module owns the layout type, the auto shape math (lifted out of GridView
// so App is the single place that decides the concrete shape), the resolve/slice
// helper, and localStorage persistence (mirrors the sidebar preference helpers).

export type GridLayout =
  | { mode: 'auto' }
  | { mode: 'fixed'; rows: number; cols: number };

export const AUTO_LAYOUT: GridLayout = { mode: 'auto' };

// Picker bounds. The unified renderer was proven to ~25 live tiles (5×5) in the
// grid spike, so that is the offered ceiling.
export const MAX_GRID_ROWS = 5;
export const MAX_GRID_COLS = 5;

// The near-square grid that fits every tile (today's default behavior).
export function autoGrid(n: number): { rows: number; cols: number } {
  if (n <= 1) return { rows: 1, cols: 1 };
  const cols = Math.ceil(Math.sqrt(n));
  const rows = Math.ceil(n / cols);
  return { rows, cols };
}

export interface ResolvedGridLayout {
  rows: number;
  cols: number;
  // How many leading tiles the grid can actually show (auto fits everything;
  // fixed caps at rows×cols).
  capacity: number;
}

// Resolve a layout against the live tile count into a concrete shape plus how
// many tiles fit. Auto sizes to fit; fixed caps at rows×cols.
export function resolveGridLayout(tileCount: number, layout: GridLayout): ResolvedGridLayout {
  if (layout.mode === 'fixed') {
    const rows = clampDim(layout.rows, MAX_GRID_ROWS);
    const cols = clampDim(layout.cols, MAX_GRID_COLS);
    return { rows, cols, capacity: rows * cols };
  }
  const { rows, cols } = autoGrid(tileCount);
  return { rows, cols, capacity: tileCount };
}

function clampDim(value: number, max: number): number {
  if (!Number.isFinite(value)) return 1;
  return Math.max(1, Math.min(max, Math.round(value)));
}

const GRID_LAYOUT_STORAGE_KEY = 'attn.grid.layout';

export function readGridLayout(): GridLayout {
  try {
    const raw = window.localStorage.getItem(GRID_LAYOUT_STORAGE_KEY);
    if (!raw) return AUTO_LAYOUT;
    const parsed = JSON.parse(raw) as Partial<GridLayout> | null;
    if (parsed && parsed.mode === 'fixed') {
      return {
        mode: 'fixed',
        rows: clampDim(Number(parsed.rows), MAX_GRID_ROWS),
        cols: clampDim(Number(parsed.cols), MAX_GRID_COLS),
      };
    }
    return AUTO_LAYOUT;
  } catch {
    return AUTO_LAYOUT;
  }
}

export function persistGridLayout(layout: GridLayout): void {
  try {
    window.localStorage.setItem(GRID_LAYOUT_STORAGE_KEY, JSON.stringify(layout));
  } catch (err) {
    console.warn('[grid] Failed to persist layout preference:', err);
  }
}
