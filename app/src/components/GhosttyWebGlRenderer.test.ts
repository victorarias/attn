import { describe, expect, it, vi } from 'vitest';
import type { GhosttyTerminal } from 'ghostty-web';
import {
  graphemeAtViewportCell,
  nextAtlasSize,
  INITIAL_ATLAS_SIZE,
  MAX_ATLAS_SIZE,
} from './GhosttyWebGlRenderer';

function terminalWithHistory(history: number) {
  return {
    getScrollbackLength: () => history,
    getScrollbackGraphemeString: vi.fn((row: number, col: number) => `history:${row}:${col}`),
    getGraphemeString: vi.fn((row: number, col: number) => `live:${row}:${col}`),
  } as unknown as GhosttyTerminal;
}

describe('graphemeAtViewportCell', () => {
  it('reads graphemes from scrollback rows in a scrolled viewport', () => {
    const terminal = terminalWithHistory(5);

    expect(graphemeAtViewportCell(terminal, 0, 2, 2)).toBe('history:3:2');
    expect(terminal.getScrollbackGraphemeString).toHaveBeenCalledWith(3, 2);
  });

  it('reads live graphemes after a mixed scrolled viewport reaches the active screen', () => {
    const terminal = terminalWithHistory(5);

    expect(graphemeAtViewportCell(terminal, 2, 4, 1)).toBe('live:1:4');
    expect(terminal.getGraphemeString).toHaveBeenCalledWith(1, 4);
  });
});

describe('nextAtlasSize (grow-on-demand policy)', () => {
  it('starts at 1024² and doubles to the 2048² cap', () => {
    expect(INITIAL_ATLAS_SIZE).toBe(1024);
    expect(MAX_ATLAS_SIZE).toBe(2048);
    expect(nextAtlasSize(INITIAL_ATLAS_SIZE)).toBe(2048);
  });

  it('is idempotent at the cap (never grows unbounded)', () => {
    expect(nextAtlasSize(MAX_ATLAS_SIZE)).toBe(MAX_ATLAS_SIZE);
  });

  it('always converges to the cap and never exceeds it under repeated growth', () => {
    let size = INITIAL_ATLAS_SIZE;
    for (let i = 0; i < 16; i += 1) {
      const grown = nextAtlasSize(size);
      expect(grown).toBeLessThanOrEqual(MAX_ATLAS_SIZE);
      expect(grown).toBeGreaterThanOrEqual(size);
      size = grown;
    }
    expect(size).toBe(MAX_ATLAS_SIZE);
  });
});
