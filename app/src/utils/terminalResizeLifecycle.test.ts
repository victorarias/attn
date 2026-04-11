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
      cancelPendingX: true,
      immediate: {
        axis: 'both',
        next: snapshot(120, 42),
        reason: 'resize_both',
      },
      debouncedX: null,
      idle: null,
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
      cancelPendingX: true,
      immediate: {
        axis: 'y',
        next: snapshot(120, 42),
        reason: 'resize_y',
      },
      debouncedX: {
        cols: 120,
        diagnostics: null,
      },
      idle: null,
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
      cancelPendingX: false,
      immediate: null,
      debouncedX: null,
      idle: {
        reason: 'resize_both',
      },
    });
  });

  it('noops when visibility returns at the same size', () => {
    expect(planVisibilityFlush({
      wasHidden: true,
      ready: true,
      next: snapshot(109, 50),
      currentCols: 109,
      currentRows: 50,
    })).toEqual({
      cancelPendingX: false,
      immediate: null,
      debouncedX: null,
      idle: null,
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
      cancelPendingX: true,
      immediate: {
        axis: 'both',
        next: snapshot(120, 52),
        reason: 'visibility_flush',
      },
      debouncedX: null,
      idle: null,
    });
  });

  it('preserves pending x work for row-only large-buffer changes', () => {
    expect(planObservedTerminalResize({
      next: snapshot(100, 42),
      lastCols: 100,
      lastRows: 40,
      bufferLength: 500,
      isVisible: true,
      hasIdleCallback: true,
    })).toEqual({
      cancelPendingX: false,
      immediate: {
        axis: 'y',
        next: snapshot(100, 42),
        reason: 'resize_y',
      },
      debouncedX: null,
      idle: null,
    });
  });
});
