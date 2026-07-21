import { act, render, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => {
  const control = {
    failFirstRendererNextRender: false,
    useLargeFit: false,
  };
  const terminals: Array<{
    resizeCalls: Array<[number, number]>;
    resize: (cols: number, rows: number) => void;
  }> = [];
  const resizeCallbacks: ResizeObserverCallback[] = [];
  const createTerminalCalls: unknown[][] = [];
  const noteModelFaultCalls: unknown[][] = [];
  const noteRecoveryCalls: unknown[][] = [];
  const recordUiDiagCalls: unknown[][] = [];

  const createTerminal = () => {
    createTerminalCalls.push([]);
    const index = terminals.length;
    const terminal = {
      cols: 80,
      rows: 24,
      resizeCalls: [] as Array<[number, number]>,
      write: () => undefined,
      resize(cols: number, rows: number) {
        terminal.resizeCalls.push([cols, rows]);
        if (index === 1) throw new Error('replacement initial fit failed');
        terminal.cols = cols;
        terminal.rows = rows;
      },
      getMode: () => false,
      getScrollbackLength: () => 0,
      getViewport: () => [],
      getScrollbackLine: () => [],
      getGraphemeString: () => '',
      getScrollbackGraphemeString: () => '',
      free: () => undefined,
      isAlternateScreen: () => false,
      hasMouseTracking: () => false,
    };
    terminals.push(terminal);
    return terminal;
  };

  const renderers: MockRenderer[] = [];
  class MockRenderer {
    readonly id = renderers.length;
    readonly cellWidth = 8;
    readonly cellHeight = 16;

    constructor() {
      renderers.push(this);
    }

    fitDimensions() {
      return control.useLargeFit ? { cols: 81, rows: 24 } : { cols: 80, rows: 24 };
    }

    resize() {}
    render() {
      if (this.id === 0 && control.failFirstRendererNextRender) {
        control.failFirstRendererNextRender = false;
        throw new Error('original model render failed');
      }
      return {
        quads: 0,
        cellsArrayLen: 0,
        printableSkippedNull: 0,
        printableSkippedZeroWidth: 0,
      };
    }
    invalidateGlyphCache() {}
    setFontSize() {}
    dispose() {}
  }

  return {
    MockRenderer,
    control,
    createTerminal,
    createTerminalCalls,
    noteModelFault: (...args: unknown[]) => { noteModelFaultCalls.push(args); },
    noteModelFaultCalls,
    noteRecovery: (...args: unknown[]) => { noteRecoveryCalls.push(args); },
    noteRecoveryCalls,
    recordUiDiag: (...args: unknown[]) => { recordUiDiagCalls.push(args); },
    recordUiDiagCalls,
    renderers,
    resizeCallbacks,
    terminals,
  };
});

vi.mock('ghostty-web', () => ({
  CellFlags: {},
  Ghostty: {
    load: async () => ({ createTerminal: mocks.createTerminal }),
  },
  InputHandler: class {
    dispose() {}
  },
}));

vi.mock('../ghostty/wasm', () => ({ ghosttyWasmUrl: 'mock-wasm-url' }));
vi.mock('./GhosttyWebGlRenderer', () => ({ WebGlTerminalRenderer: mocks.MockRenderer }));
vi.mock('../utils/terminalIconFont', () => ({
  ensureTerminalIconFont: () => new Promise<void>(() => undefined),
}));
vi.mock('../utils/terminalDiagnosticsLog', () => ({
  disposePaneDiagnostics: () => undefined,
  noteModelFault: mocks.noteModelFault,
  noteRecovery: mocks.noteRecovery,
  noteResize: () => undefined,
  recordDiag: () => undefined,
  recordPaint: () => undefined,
  registerRenderProbe: () => undefined,
}));
vi.mock('../utils/uiDiagnosticsLog', () => ({
  captureUiSnapshot: () => ({}),
  recordUiDiag: mocks.recordUiDiag,
  UI_DIAGNOSTICS_FILE: 'diagnostics.jsonl',
}));
vi.mock('../utils/terminalPerf', () => ({
  registerTerminalPerfGetter: () => () => undefined,
}));

import { GhosttyTerminal } from './GhosttyTerminal';

describe('GhosttyTerminal model-fault containment', () => {
  it('rebuilds again when the replacement model fails its initial fit, then only announces a healthy recovery', async () => {
    const originalResizeObserver = globalThis.ResizeObserver;
    globalThis.ResizeObserver = class {
      constructor(callback: ResizeObserverCallback) {
        mocks.resizeCallbacks.push(callback);
      }

      observe() {}
      unobserve() {}
      disconnect() {}
    } as unknown as typeof ResizeObserver;

    const onReady = vi.fn();
    const onTerminalModelRecovered = vi.fn();
    try {
      render(
        <GhosttyTerminal
          fontSize={14}
          debugName="model-fault-test"
          onInput={vi.fn()}
          onReady={onReady}
          onResize={vi.fn()}
          onTerminalModelRecovered={onTerminalModelRecovered}
        />,
      );

      await waitFor(() => expect(onReady).toHaveBeenCalledTimes(1));
      expect(mocks.resizeCallbacks).toHaveLength(1);

      mocks.control.useLargeFit = true;
      mocks.control.failFirstRendererNextRender = true;
      await act(async () => {
        mocks.resizeCallbacks[0]([], {} as ResizeObserver);
      });

      await waitFor(() => {
        expect(mocks.createTerminalCalls).toHaveLength(3);
        expect(onReady).toHaveBeenCalledTimes(2);
        expect(onTerminalModelRecovered).toHaveBeenCalledTimes(1);
      });

      // The first replacement reached its initial fit and faulted there. It
      // never became ready; only the third, healthy model did.
      expect(mocks.terminals[1].resizeCalls).toEqual([[81, 24]]);
      expect(mocks.noteModelFaultCalls).toHaveLength(2);
      expect(mocks.noteRecoveryCalls.map(([, detail]) => (detail as { outcome: string }).outcome)).toEqual([
        'modelFault',
        'modelFault',
        'recovered',
      ]);
    } finally {
      globalThis.ResizeObserver = originalResizeObserver;
    }
  });
});
