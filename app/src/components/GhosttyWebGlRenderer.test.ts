import { describe, expect, it, vi } from 'vitest';
import type { GhosttyTerminal } from 'ghostty-web';
import { graphemeAtViewportCell } from './GhosttyWebGlRenderer';

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
