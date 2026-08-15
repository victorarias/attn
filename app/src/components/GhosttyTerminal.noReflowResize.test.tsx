// Every resize of the local model takes the no-reflow path.
//
// `resizeLocal` is where the daemon's `pty_resized` echo lands, and a non-owner
// client (a hub mirror, a second window) reaches the model through nothing else.
// The worker resizes its authoritative terminal without reflow, and so do fit
// and historical replay — so a client that re-wrapped its history here would
// hold a different frame from the worker whose rows every placement and block on
// the wire is numbered in.
//
// This fails if the live branch goes back to a plain `terminal.resize(...)`: the
// mode-7 dance disappears from the model's op log.
import { act, render, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

type ModelOp = { kind: 'write'; data: string } | { kind: 'resize'; cols: number; rows: number };

const mocks = vi.hoisted(() => {
  const control = { wraparound: true };
  const terminals: Array<{ ops: ModelOp[] }> = [];

  const createTerminal = () => {
    const decoder = new TextDecoder();
    const terminal = {
      cols: 80,
      rows: 24,
      ops: [] as ModelOp[],
      write(data: string | Uint8Array) {
        terminal.ops.push({
          kind: 'write',
          data: typeof data === 'string' ? data : decoder.decode(data),
        });
      },
      resize(cols: number, rows: number) {
        terminal.cols = cols;
        terminal.rows = rows;
        terminal.ops.push({ kind: 'resize', cols, rows });
      },
      // DEC mode 7. The no-reflow recipe reads it and only dances when it is on.
      getMode: (mode: number) => (mode === 7 ? control.wraparound : false),
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
    };
    terminals.push(terminal);
    return terminal;
  };

  class MockRenderer {
    readonly cellWidth = 8;
    readonly cellHeight = 16;

    fitDimensions() {
      return { cols: 80, rows: 24 };
    }

    resize() {}
    render() {
      return { quads: 0, cellsArrayLen: 0, printableSkippedNull: 0, printableSkippedZeroWidth: 0 };
    }
    invalidateGlyphCache() {}
    setFontSize() {}
    dispose() {}
  }

  return { MockRenderer, control, createTerminal, terminals };
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

// This suite drives resizes through the observer itself, so it installs one it
// can hold a handle on rather than using the inert class from setup.ts.
beforeEach(() => {
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver;
});

// Mounts one pane and hands back its model with the mount-time writes (the
// grapheme-cluster mode set) cleared, so the op log holds only the resize.
async function mountTerminal(): Promise<{
  handle: GhosttyTerminalHandle;
  model: { ops: ModelOp[] };
}> {
  mocks.terminals.length = 0;
  let ready: GhosttyTerminalHandle | null = null;
  render(
    <GhosttyTerminal
      fontSize={14}
      debugName="no-reflow-resize-test"
      onInput={vi.fn()}
      onReady={(terminal) => { ready = terminal; }}
      onResize={vi.fn()}
    />,
  );
  await waitFor(() => expect(ready).not.toBeNull());
  const model = mocks.terminals[0];
  model.ops.length = 0;
  return { handle: ready as unknown as GhosttyTerminalHandle, model };
}

const noReflowRecipe = (cols: number, rows: number): ModelOp[] => [
  { kind: 'write', data: '\x1b[?7l' },
  { kind: 'resize', cols, rows },
  { kind: 'write', data: '\x1b[?7h' },
];

describe('GhosttyTerminal no-reflow resize', () => {
  it('drives the daemon resize echo through the mode-7 recipe', async () => {
    mocks.control.wraparound = true;
    const { handle, model } = await mountTerminal();

    await act(async () => {
      await handle.resizeLocal(100, 30);
    });

    expect(model.ops).toEqual(noReflowRecipe(100, 30));
  });

  it('resizes plainly when the program already turned wraparound off', async () => {
    // Writing the mode back on would enable wrapping the program disabled.
    mocks.control.wraparound = false;
    const { handle, model } = await mountTerminal();

    await act(async () => {
      await handle.resizeLocal(100, 30);
    });

    expect(model.ops).toEqual([{ kind: 'resize', cols: 100, rows: 30 }]);
  });

  it('keeps the restore resize on the same path', async () => {
    mocks.control.wraparound = true;
    const { handle, model } = await mountTerminal();

    await act(async () => {
      await handle.resizeLocal(90, 20, { restore: true });
    });

    expect(model.ops).toEqual(noReflowRecipe(90, 20));
  });
});
