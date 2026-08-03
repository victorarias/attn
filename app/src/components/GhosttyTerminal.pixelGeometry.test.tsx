// What a fit tells the daemon about the pane's size in PIXELS.
//
// The grid alone does not let an image emitter size an image; it divides the
// pane's pixel area by the grid to get a cell. The renderer's cell metrics are
// CSS pixels — the canvas is styled `cols * cellWidth` and backed by that times
// devicePixelRatio — while the PTY's ws_xpixel/ws_ypixel are device pixels, so
// the fit is where the two units meet. A fit that reported CSS pixels on a
// retina display would tell every emitter the pane is half its real size.
import { act, render, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const CELL_W = 9;
const CELL_H = 23;
const FIT_COLS = 40;
const FIT_ROWS = 12;

const mocks = vi.hoisted(() => {
  const rendererConfig = { dpr: 2 };

  const createTerminal = () => ({
    cols: 80,
    rows: 24,
    write() {},
    resize(cols: number, rows: number) {
      this.cols = cols;
      this.rows = rows;
    },
    getMode: () => false,
    getScrollbackLength: () => 0,
    getViewport: () => [],
    getScrollbackLine: () => [],
    getGraphemeString: () => '',
    getScrollbackGraphemeString: () => '',
    getCursor: () => ({ x: 0, y: 0 }),
    hasResponse: () => false,
    readResponse: () => null,
    free: () => undefined,
    isAlternateScreen: () => false,
    hasMouseTracking: () => false,
  });

  class MockRenderer {
    // CSS pixels, as the real renderer reports them.
    readonly cellWidth = 9;
    readonly cellHeight = 23;
    readonly dpr = rendererConfig.dpr;

    fitDimensions() {
      return { cols: 40, rows: 12 };
    }

    resize() {}
    render() {
      return { quads: 0, cellsArrayLen: 0, printableSkippedNull: 0, printableSkippedZeroWidth: 0 };
    }
    invalidateGlyphCache() {}
    setFontSize() {}
    dispose() {}
  }

  return { MockRenderer, createTerminal, rendererConfig };
});

vi.mock('ghostty-web', () => ({
  CellFlags: {},
  Ghostty: { load: async () => ({ createTerminal: mocks.createTerminal }) },
  InputHandler: class { dispose() {} },
}));
vi.mock('../ghostty/wasm', () => ({ ghosttyWasmUrl: 'mock-wasm-url' }));
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

// The suite-wide ResizeObserver stub is a vi.fn(), which `new` does not build
// into an observer the component can hold — the pane never reaches onReady
// behind it. A real class is what the other GhosttyTerminal render tests use.
beforeEach(() => {
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver;
});

async function fitOnce(dpr: number) {
  mocks.rendererConfig.dpr = dpr;
  const onResize = vi.fn();
  let ready: GhosttyTerminalHandle | null = null;
  render(
    <GhosttyTerminal
      fontSize={14}
      debugName="pixel-geometry-test"
      onInput={vi.fn()}
      onReady={(terminal) => { ready = terminal; }}
      onResize={onResize}
    />,
  );
  await waitFor(() => expect(ready).not.toBeNull());
  await act(async () => {
    (ready as unknown as GhosttyTerminalHandle).fit();
  });
  await waitFor(() => expect(onResize).toHaveBeenCalled());
  return onResize;
}

describe('GhosttyTerminal fit pixel geometry', () => {
  it('reports the pane total in device pixels on a retina display', async () => {
    const onResize = await fitOnce(2);

    expect(onResize).toHaveBeenLastCalledWith(FIT_COLS, FIT_ROWS, expect.objectContaining({
      reason: 'ghostty_fit',
      xpixel: FIT_COLS * CELL_W * 2,
      ypixel: FIT_ROWS * CELL_H * 2,
    }));
  });

  it('reports CSS pixels unchanged on a 1x display', async () => {
    // The same arithmetic with nothing to scale — the guard against a fit that
    // hardcoded a doubling rather than reading the ratio.
    const onResize = await fitOnce(1);

    expect(onResize).toHaveBeenLastCalledWith(FIT_COLS, FIT_ROWS, expect.objectContaining({
      xpixel: FIT_COLS * CELL_W,
      ypixel: FIT_ROWS * CELL_H,
    }));
  });
});
