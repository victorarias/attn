import type { ResizeDiagnostics } from './terminalDebug';

const START_DEBOUNCING_THRESHOLD = 200;

export const X_AXIS_DEBOUNCE_MS = 100;

export interface TerminalResizeSnapshot {
  cols: number;
  rows: number;
  diagnostics: ResizeDiagnostics | null;
}

export interface TerminalImmediateResizePlan {
  axis: 'both' | 'y';
  next: TerminalResizeSnapshot;
  reason: 'resize_both' | 'resize_y' | 'visibility_flush';
}

export interface TerminalDebouncedXResizePlan {
  cols: number;
  diagnostics: ResizeDiagnostics | null;
}

export interface TerminalIdleResizePlan {
  reason: 'resize_both';
}

export interface TerminalViewportResizePlan {
  cancelPendingX: boolean;
  immediate: TerminalImmediateResizePlan | null;
  debouncedX: TerminalDebouncedXResizePlan | null;
  idle: TerminalIdleResizePlan | null;
}

const NOOP_RESIZE_PLAN: TerminalViewportResizePlan = {
  cancelPendingX: false,
  immediate: null,
  debouncedX: null,
  idle: null,
};

export function planObservedTerminalResize(input: {
  next: TerminalResizeSnapshot;
  lastCols: number;
  lastRows: number;
  bufferLength: number;
  isVisible: boolean;
  hasIdleCallback: boolean;
}): TerminalViewportResizePlan {
  const { next, lastCols, lastRows, bufferLength, isVisible, hasIdleCallback } = input;
  const colsChanged = next.cols !== lastCols;
  const rowsChanged = next.rows !== lastRows;

  if (!colsChanged && !rowsChanged) {
    return NOOP_RESIZE_PLAN;
  }

  if (bufferLength < START_DEBOUNCING_THRESHOLD) {
    return {
      cancelPendingX: true,
      immediate: {
        axis: 'both',
        next,
        reason: 'resize_both',
      },
      debouncedX: null,
      idle: null,
    };
  }

  if (!isVisible && hasIdleCallback) {
    return {
      cancelPendingX: false,
      immediate: null,
      debouncedX: null,
      idle: {
        reason: 'resize_both',
      },
    };
  }

  if (colsChanged && rowsChanged) {
    return {
      cancelPendingX: true,
      immediate: {
        axis: 'y',
        next,
        reason: 'resize_y',
      },
      debouncedX: {
        cols: next.cols,
        diagnostics: next.diagnostics,
      },
      idle: null,
    };
  }

  if (rowsChanged) {
    return {
      cancelPendingX: false,
      immediate: {
        axis: 'y',
        next,
        reason: 'resize_y',
      },
      debouncedX: null,
      idle: null,
    };
  }

  return {
    cancelPendingX: true,
    immediate: null,
    debouncedX: {
      cols: next.cols,
      diagnostics: next.diagnostics,
    },
    idle: null,
  };
}

export function planVisibilityFlush(input: {
  wasHidden: boolean;
  ready: boolean;
  next: TerminalResizeSnapshot | null;
  currentCols: number;
  currentRows: number;
}): TerminalViewportResizePlan {
  const { wasHidden, ready, next, currentCols, currentRows } = input;

  if (!wasHidden || !ready || !next) {
    return NOOP_RESIZE_PLAN;
  }

  if (next.cols === currentCols && next.rows === currentRows) {
    return NOOP_RESIZE_PLAN;
  }

  return {
    cancelPendingX: true,
    immediate: {
      axis: 'both',
      next,
      reason: 'visibility_flush',
    },
    debouncedX: null,
    idle: null,
  };
}
