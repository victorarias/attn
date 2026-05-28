import { describe, expect, it } from 'vitest';
import {
  applicationMouseInput,
  applicationWheelInput,
  bufferRowFromViewportRow,
  consumeWheelRows,
  createApplicationSelectionAnchor,
  cursorRowInViewport,
  offsetAfterWrite,
  relocateApplicationSelection,
  viewportRowFromBufferRow,
} from './ghosttyScroll';

describe('applicationWheelInput', () => {
  it('uses SGR mouse wheel reports for a mouse-tracking program', () => {
    expect(applicationWheelInput(-2, 7, 9, true)).toBe('\x1b[<64;7;9M\x1b[<64;7;9M');
    expect(applicationWheelInput(1, 7, 9, true)).toBe('\x1b[<65;7;9M');
    expect(applicationWheelInput(1, 1, 1, true, false)).toBe('\x1b[Ma!!');
  });

  it('uses arrow input for alternate screens without mouse tracking and limits burst size', () => {
    expect(applicationWheelInput(-8, 1, 1, false)).toBe('\x1b[A'.repeat(5));
    expect(applicationWheelInput(2, 1, 1, false)).toBe('\x1b[B\x1b[B');
  });
});

describe('applicationMouseInput', () => {
  it('encodes SGR mouse presses, drags and releases', () => {
    expect(applicationMouseInput('press', 0, 7, 9, true)).toBe('\x1b[<0;7;9M');
    expect(applicationMouseInput('move', 0, 8, 9, true)).toBe('\x1b[<32;8;9M');
    expect(applicationMouseInput('release', 0, 8, 9, true)).toBe('\x1b[<3;8;9m');
  });

  it('encodes legacy mouse reports when SGR mode is disabled', () => {
    expect(applicationMouseInput('press', 0, 1, 1, false)).toBe('\x1b[M !!');
    expect(applicationMouseInput('release', 0, 1, 1, false)).toBe('\x1b[M#!!');
  });
});

describe('application-owned selection', () => {
  it('relocates a visible selected range after an application redraw scrolls its content', () => {
    const before = ['heading', 'selected first', 'selected second', 'footer'];
    const anchor = createApplicationSelectionAnchor(
      { startRow: 1, startCol: 0, endRow: 2, endCol: 15 },
      (row) => before[row] ?? '',
    );

    expect(anchor).not.toBeNull();
    expect(relocateApplicationSelection(
      anchor!,
      ['older', 'heading', 'selected first', 'selected second', 'footer'],
      100,
      80,
    )).toEqual({ startRow: 102, startCol: 0, endRow: 103, endCol: 15 });
  });

  it('hides only the overlay when selected application content is offscreen', () => {
    const anchor = createApplicationSelectionAnchor(
      { startRow: 1, startCol: 0, endRow: 1, endCol: 8 },
      (row) => ['header', 'selected'][row] ?? '',
    );

    expect(relocateApplicationSelection(anchor!, ['different content'], 0, 80)).toBeNull();
  });
});

describe('consumeWheelRows', () => {
  it('accumulates trackpad pixel deltas until they cross a terminal row', () => {
    const first = consumeWheelRows(-4, 0, 20, 40, 0);
    const second = consumeWheelRows(-8, 0, 20, 40, first.remainderRows);
    const third = consumeWheelRows(-9, 0, 20, 40, second.remainderRows);

    expect(first.lines).toBe(0);
    expect(first.remainderRows).toBeCloseTo(-0.2);
    expect(second.lines).toBe(0);
    expect(second.remainderRows).toBeCloseTo(-0.6);
    expect(third.lines).toBe(-1);
    expect(third.remainderRows).toBeCloseTo(-0.05);
  });

  it('preserves native line and page wheel units', () => {
    expect(consumeWheelRows(3, 1, 20, 40, 0).lines).toBe(3);
    expect(consumeWheelRows(-1, 2, 20, 40, 0).lines).toBe(-40);
  });
});

describe('offsetAfterWrite', () => {
  it('keeps a bottom-following viewport pinned to new output', () => {
    expect(offsetAfterWrite(0, 100, 104)).toBe(0);
  });

  it('anchors a manually scrolled viewport while output adds scrollback', () => {
    expect(offsetAfterWrite(12, 100, 104)).toBe(16);
  });

  it('clamps an anchored viewport when scrollback is cleared', () => {
    expect(offsetAfterWrite(12, 100, 0)).toBe(0);
  });
});

describe('cursorRowInViewport', () => {
  it('moves the cursor with its live buffer line as the viewport scrolls upward', () => {
    expect(cursorRowInViewport(10, 0, 25)).toBe(10);
    expect(cursorRowInViewport(10, 3, 25)).toBe(13);
  });

  it('hides the cursor once its live buffer line is below the historical viewport', () => {
    expect(cursorRowInViewport(20, 5, 25)).toBeNull();
  });
});

describe('selection buffer coordinates', () => {
  it('stores selected rows against the buffer and projects them as the viewport moves', () => {
    const selectedBufferRow = bufferRowFromViewportRow(8, 100, 0);

    expect(selectedBufferRow).toBe(108);
    expect(viewportRowFromBufferRow(selectedBufferRow, 100, 0)).toBe(8);
    expect(viewportRowFromBufferRow(selectedBufferRow, 100, 4)).toBe(12);
  });

  it('projects selected rows outside the visible viewport instead of pinning the highlight', () => {
    const selectedBufferRow = bufferRowFromViewportRow(8, 100, 0);

    expect(viewportRowFromBufferRow(selectedBufferRow, 100, 20)).toBe(28);
  });
});
