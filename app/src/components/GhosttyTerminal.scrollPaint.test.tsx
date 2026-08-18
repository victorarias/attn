// What one wheel gesture costs while the viewport sits in scrollback.
//
// A macOS trackpad delivers wheel events far faster than the display refreshes
// and adds a momentum tail after the fingers lift, so the two things that must
// hold are: the paint happens once per frame no matter how many events landed,
// and reading a visible row reads that row rather than reassembling every
// visible cell.
import { act, render, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const COLS = 20;
const ROWS = 5;
const SCROLLBACK = 30;

const mocks = vi.hoisted(() => {
  const counts = { getViewport: 0, getActiveLine: 0, getScrollbackLine: 0, render: 0 };
  const renderViewportOffsets: number[] = [];
  const flags = { mouseTracking: false };

  const cellsFor = (text: string) => {
    const cells = [];
    for (let col = 0; col < 20; col += 1) {
      cells.push({
        codepoint: col < text.length ? text.codePointAt(col) ?? 32 : 32,
        fg_r: 0, fg_g: 0, fg_b: 0, bg_r: 0, bg_g: 0, bg_b: 0,
        flags: 0, width: 1, hyperlink_id: 0, grapheme_len: 0,
      });
    }
    return cells;
  };

  const createTerminal = () => ({
    cols: COLS,
    rows: ROWS,
    write() {},
    resize(cols: number, rows: number) {
      this.cols = cols;
      this.rows = rows;
    },
    getMode: () => false,
    getScrollbackLength: () => SCROLLBACK,
    getViewport() {
      counts.getViewport += 1;
      const cells = [];
      for (let row = 0; row < ROWS; row += 1) cells.push(...cellsFor(`active-${row}`));
      return cells;
    },
    getActiveLine(row: number) {
      counts.getActiveLine += 1;
      return cellsFor(`active-${row}`);
    },
    getScrollbackLine(offset: number) {
      counts.getScrollbackLine += 1;
      return cellsFor(`history-${offset}`);
    },
    rowWrapsIntoNext: () => false,
    getGraphemeString: () => '',
    getScrollbackGraphemeString: () => '',
    getCursor: () => ({ x: 0, y: 0 }),
    hasResponse: () => false,
    readResponse: () => null,
    free: () => undefined,
    isAlternateScreen: () => false,
    hasMouseTracking: () => flags.mouseTracking,
  });

  class MockRenderer {
    readonly cellWidth = 9;
    readonly cellHeight = 23;
    readonly dpr = 1;

    fitDimensions() {
      return { cols: COLS, rows: ROWS };
    }

    resize() {}
    render(
      _terminal: unknown,
      _force: boolean,
      _viewportCells: unknown,
      _overlays: unknown,
      viewportOffset: number,
    ) {
      counts.render += 1;
      renderViewportOffsets.push(viewportOffset);
      return { quads: 0, cellsArrayLen: 0, printableSkippedNull: 0, printableSkippedZeroWidth: 0 };
    }
    invalidateGlyphCache() {}
    setFontSize() {}
    dispose() {}
  }

  return { MockRenderer, createTerminal, counts, renderViewportOffsets, flags };
});

vi.mock('ghostty-web', () => ({
  CellFlags: {},
  Ghostty: { load: async () => ({ createTerminal: mocks.createTerminal }) },
  InputHandler: class { dispose() {} },
}));
vi.mock('../ghostty/wasm', () => ({ loadGhostty: async () => ({ createTerminal: mocks.createTerminal }) }));
vi.mock('./GhosttyWebGlRenderer', () => ({ WebGlTerminalRenderer: mocks.MockRenderer }));
vi.mock('../utils/terminalIconFont', () => ({
  ensureTerminalIconFont: () => new Promise<void>(() => undefined),
}));
vi.mock('../utils/terminalDiagnosticsLog', () => ({
  TERMINAL_DIAGNOSTICS_FILE: 'terminal-diagnostics.jsonl',
  disposePaneDiagnostics: () => undefined,
  noteModelFault: () => undefined,
  noteRecovery: () => undefined,
  noteResize: () => undefined,
  recordDiag: () => undefined,
  recordPaint: () => undefined,
  registerRenderProbe: () => undefined,
}));
vi.mock('../utils/uiDiagnosticsLog', () => ({
  captureUiSnapshot: () => ({}),
  recordUiDiag: () => undefined,
  UI_DIAGNOSTICS_FILE: 'diagnostics.jsonl',
}));
vi.mock('../utils/terminalPerf', () => ({ registerTerminalPerfGetter: () => () => undefined }));

import { GhosttyTerminal, type GhosttyTerminalHandle } from './GhosttyTerminal';

const frames: FrameRequestCallback[] = [];

function flushFrames() {
  const pending = frames.splice(0, frames.length);
  for (const callback of pending) callback(performance.now());
}

beforeEach(() => {
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver;
  frames.length = 0;
  globalThis.requestAnimationFrame = ((callback: FrameRequestCallback) => frames.push(callback)) as typeof requestAnimationFrame;
  globalThis.cancelAnimationFrame = (() => undefined) as typeof cancelAnimationFrame;
  mocks.counts.getViewport = 0;
  mocks.counts.getActiveLine = 0;
  mocks.counts.getScrollbackLine = 0;
  mocks.counts.render = 0;
  mocks.renderViewportOffsets.length = 0;
  mocks.flags.mouseTracking = false;
});

async function mountTerminal() {
  let ready: GhosttyTerminalHandle | null = null;
  const onInput = vi.fn();
  const view = render(
    <GhosttyTerminal
      fontSize={14}
      debugName="scroll-paint-test"
      onInput={onInput}
      onReady={(terminal) => { ready = terminal; }}
      onResize={vi.fn()}
    />,
  );
  await waitFor(() => expect(ready).not.toBeNull());
  const surface = view.container.querySelector('.terminal-container');
  if (!surface) throw new Error('terminal surface never rendered');
  return { handle: ready as unknown as GhosttyTerminalHandle, surface, onInput };
}

function wheelUp(surface: Element, times: number) {
  for (let i = 0; i < times; i += 1) {
    surface.dispatchEvent(new WheelEvent('wheel', {
      deltaY: -1,
      deltaMode: 1,
      bubbles: true,
      cancelable: true,
    }));
  }
}

describe('GhosttyTerminal scroll paint', () => {
  it('paints once for a burst of wheel events and keeps every row they scrolled', async () => {
    const { handle, surface } = await mountTerminal();
    flushFrames();
    mocks.counts.render = 0;
    mocks.renderViewportOffsets.length = 0;

    await act(async () => {
      wheelUp(surface, 12);
    });
    // Nothing painted yet: the events only moved the offset.
    expect(mocks.counts.render).toBe(0);

    await act(async () => {
      flushFrames();
    });

    expect(mocks.counts.render).toBe(1);
    // Every event counted — coalescing the paint must not drop scrolled rows.
    expect(mocks.renderViewportOffsets).toEqual([12]);
    expect(handle.getVisibleContent().viewportY).toBe(SCROLLBACK - 12);
  });

  it('reads one row per visible row while scrolled into history', async () => {
    const { handle, surface } = await mountTerminal();
    await act(async () => {
      wheelUp(surface, 12);
      flushFrames();
    });

    mocks.counts.getViewport = 0;
    mocks.counts.getScrollbackLine = 0;
    const visible = handle.getVisibleContent();

    expect(visible.lines).toEqual([
      'history-18', 'history-19', 'history-20', 'history-21', 'history-22',
    ]);
    expect(mocks.counts.getScrollbackLine).toBe(ROWS);
    expect(mocks.counts.getViewport).toBe(0);
  });

  it('reads one row per visible row at the bottom of the buffer', async () => {
    const { handle } = await mountTerminal();
    mocks.counts.getViewport = 0;
    mocks.counts.getActiveLine = 0;

    const visible = handle.getVisibleContent();

    expect(visible.lines).toEqual([
      'active-0', 'active-1', 'active-2', 'active-3', 'active-4',
    ]);
    expect(mocks.counts.getActiveLine).toBe(ROWS);
    expect(mocks.counts.getViewport).toBe(0);
  });
});

// Who owns the wheel. A program that asked for mouse reports gets them and the
// viewport stays put; every other pane scrolls. The model answers that question,
// and a wrong answer is invisible until you try to scroll and nothing moves.
describe('GhosttyTerminal wheel ownership', () => {
  it('hands the wheel to a program tracking the mouse', async () => {
    const { handle, surface, onInput } = await mountTerminal();
    flushFrames();
    mocks.counts.render = 0;
    mocks.flags.mouseTracking = true;

    await act(async () => {
      wheelUp(surface, 3);
      flushFrames();
    });

    expect(onInput).toHaveBeenCalled();
    expect(handle.getVisibleContent().viewportY).toBe(SCROLLBACK);
    expect(mocks.counts.render).toBe(0);
  });

  it('scrolls the viewport when no program is tracking the mouse', async () => {
    const { handle, surface, onInput } = await mountTerminal();
    flushFrames();
    mocks.counts.render = 0;
    onInput.mockClear();

    await act(async () => {
      wheelUp(surface, 3);
      flushFrames();
    });

    expect(onInput).not.toHaveBeenCalled();
    expect(handle.getVisibleContent().viewportY).toBe(SCROLLBACK - 3);
    expect(mocks.counts.render).toBe(1);
  });
});
