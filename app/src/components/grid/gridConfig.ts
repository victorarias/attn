// Shared, grid-level constants for the unified renderer. Every tile uses the
// SAME font / size / dpr — a hard invariant for the unified renderer, whose
// single shared glyph atlas rasterizes each glyph once at the canonical size and
// shrinks it per-tile via the baked vertex transform.
import {
  FONT_FAMILY,
  TERMINAL_SCROLLBACK_BYTES,
} from '../../utils/terminalSizing';

export { FONT_FAMILY, TERMINAL_SCROLLBACK_BYTES };

export const FONT_SIZE = 13;

// Representative per-tile terminal geometry. Tiles are observer thumbnails, so a
// modest fixed grid keeps each live model cheap; the live screen geometry of the
// real session is whatever the daemon reports — we render it scaled to fit.
export const TILE_COLS = 80;
export const TILE_ROWS = 24;

export interface CellMetrics {
  cellWidth: number;
  cellHeight: number;
  baseline: number;
}

// Canonical (logical px) cell metrics, measured once and shared by all tiles.
// Mirrors WebGlTerminalRenderer's constructor math so glyph placement matches
// the production renderer.
export function measureCanonicalCell(
  fontSize: number = FONT_SIZE,
  fontFamily: string = FONT_FAMILY,
): CellMetrics {
  const canvas = document.createElement('canvas');
  const ctx = canvas.getContext('2d');
  if (!ctx) throw new Error('grid: unable to measure terminal font');
  ctx.font = `${fontSize}px ${fontFamily}`;
  return {
    cellWidth: Math.max(1, Math.ceil(ctx.measureText('M').width)),
    cellHeight: Math.max(1, Math.ceil(fontSize * 1.45)),
    baseline: Math.ceil(fontSize * 1.1),
  };
}

export function colorNumber(hex: string): number {
  return Number.parseInt(hex.slice(1), 16);
}
