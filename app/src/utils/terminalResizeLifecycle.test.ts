import { describe, expect, it } from 'vitest';
import {
  planObservedTerminalResize,
  planVisibilityFlush,
  type TerminalResizeSnapshot,
} from './terminalResizeLifecycle';

function snapshot(cols: number, rows: number): TerminalResizeSnapshot {
  return {
    cols,
    rows,
    diagnostics: null,
  };
}

describe('terminalResizeLifecycle', () => {
  it('resizes both axes immediately for small buffers', () => {
    expect(planObservedTerminalResize({
      next: snapshot(120, 42),
      lastCols: 100,
      lastRows: 40,
      bufferLength: 50,
      isVisible: true,
      hasIdleCallback: true,
    })).toEqual({
      type: 'resize_both',
      next: snapshot(120, 42),
    });
  });

  it('splits row and column work for visible large buffers', () => {
    expect(planObservedTerminalResize({
      next: snapshot(120, 42),
      lastCols: 100,
      lastRows: 40,
      bufferLength: 500,
      isVisible: true,
      hasIdleCallback: true,
    })).toEqual({
      type: 'resize_y_then_debounce_x',
      rows: 42,
      cols: 120,
      diagnostics: null,
    });
  });

  it('defers hidden large-buffer resizes to idle work when available', () => {
    expect(planObservedTerminalResize({
      next: snapshot(120, 42),
      lastCols: 100,
      lastRows: 40,
      bufferLength: 500,
      isVisible: false,
      hasIdleCallback: true,
    })).toEqual({
      type: 'idle_resize_both',
      next: snapshot(120, 42),
    });
  });

  it('requests a redraw when visibility returns at the same size', () => {
    expect(planVisibilityFlush({
      wasHidden: true,
      ready: true,
      next: snapshot(109, 50),
      currentCols: 109,
      currentRows: 50,
    })).toEqual({
      type: 'force_redraw',
      cols: 109,
      rows: 50,
    });
  });

  it('resizes when visibility returns at a different size', () => {
    expect(planVisibilityFlush({
      wasHidden: true,
      ready: true,
      next: snapshot(120, 52),
      currentCols: 109,
      currentRows: 50,
    })).toEqual({
      type: 'resize',
      next: snapshot(120, 52),
    });
  });
});
