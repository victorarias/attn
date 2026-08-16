import { render, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => {
  const createTerminalCalls: unknown[][] = [];
  const noteModelFaultCalls: unknown[][] = [];
  const recordUiDiagCalls: Array<Record<string, unknown>> = [];
  const writes: string[] = [];

  const createTerminal = () => {
    createTerminalCalls.push([]);
    const terminal = {
      cols: 80,
      rows: 24,
      write: (data: string | Uint8Array) => {
        writes.push(typeof data === 'string' ? data : new TextDecoder().decode(data));
      },
      resize(cols: number, rows: number) {
        terminal.cols = cols;
        terminal.rows = rows;
      },
      adoptSnapshot: () => {
        throw new Error('ghostty_snapshot_decoder_ready failed');
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
    return terminal;
  };

  class MockRenderer {
    readonly cellWidth = 8;
    readonly cellHeight = 16;
    fitDimensions() { return { cols: 80, rows: 24 }; }
    resize() {}
    render() {
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
    createTerminal,
    createTerminalCalls,
    noteModelFault: (...args: unknown[]) => { noteModelFaultCalls.push(args); },
    noteModelFaultCalls,
    recordUiDiag: (event: Record<string, unknown>) => { recordUiDiagCalls.push(event); },
    recordUiDiagCalls,
    writes,
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

vi.mock('../ghostty/wasm', () => ({ loadGhostty: async () => ({ createTerminal: mocks.createTerminal }) }));
vi.mock('./GhosttyWebGlRenderer', () => ({ WebGlTerminalRenderer: mocks.MockRenderer }));
vi.mock('../utils/terminalIconFont', () => ({
  ensureTerminalIconFont: () => new Promise<void>(() => undefined),
}));
vi.mock('../utils/terminalDiagnosticsLog', () => ({
  TERMINAL_DIAGNOSTICS_FILE: 'terminal-diagnostics.jsonl',
  disposePaneDiagnostics: () => undefined,
  noteModelFault: mocks.noteModelFault,
  noteRecovery: () => undefined,
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

// Bytes a worker of another build encoded reach a decoder that rejects them.
// Treating that as a model fault replaced the model, remounted the pane,
// reattached, and was served the same bytes — 421 faults in six seconds on the
// build that shipped it. See docs/plans/2026-08-16-snapshot-format-skew.md.
describe('GhosttyTerminal snapshot decode', () => {
  it('records an undecodable snapshot without faulting the model', async () => {
    const originalResizeObserver = globalThis.ResizeObserver;
    globalThis.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    } as unknown as typeof ResizeObserver;

    const onReady = vi.fn();
    try {
      render(
        <GhosttyTerminal
          fontSize={14}
          debugName="snapshot-decode-test"
          onInput={vi.fn()}
          onReady={onReady}
          onResize={vi.fn()}
        />,
      );

      await waitFor(() => expect(onReady).toHaveBeenCalledTimes(1));
      const handle = onReady.mock.calls[0][0] as {
        restoreSnapshot: (bytes: Uint8Array) => Promise<void>;
        write: (data: string) => Promise<void>;
      };

      await handle.restoreSnapshot(new Uint8Array([1, 2, 3, 4]));

      const rejected = mocks.recordUiDiagCalls.filter((event) => event.kind === 'snapshot_decode_rejected');
      expect(rejected).toHaveLength(1);
      expect(rejected[0].bytes).toBe(4);
      expect(rejected[0].error).toBe('ghostty_snapshot_decoder_ready failed');
      expect(mocks.noteModelFaultCalls).toHaveLength(0);
      // No new epoch: one model built, and it still takes writes.
      expect(mocks.createTerminalCalls).toHaveLength(1);
      await handle.write('live output after the refused restore');
      expect(mocks.writes.join('')).toContain('live output after the refused restore');
    } finally {
      globalThis.ResizeObserver = originalResizeObserver;
    }
  });
});
