import type { ResizeDiagnostics } from './terminalDebug';

const START_DEBOUNCING_THRESHOLD = 200;

export const X_AXIS_DEBOUNCE_MS = 100;

export interface TerminalResizeSnapshot {
  cols: number;
  rows: number;
  diagnostics: ResizeDiagnostics | null;
}

export type ObservedTerminalResizePlan =
  | { type: 'none' }
  | { type: 'resize_both'; next: TerminalResizeSnapshot }
  | { type: 'resize_y'; rows: number; diagnostics: ResizeDiagnostics | null }
  | { type: 'debounce_x'; cols: number; diagnostics: ResizeDiagnostics | null }
  | { type: 'resize_y_then_debounce_x'; rows: number; cols: number; diagnostics: ResizeDiagnostics | null }
  | { type: 'idle_resize_both'; next: TerminalResizeSnapshot };

export function planObservedTerminalResize(input: {
  next: TerminalResizeSnapshot;
  lastCols: number;
  lastRows: number;
  bufferLength: number;
  isVisible: boolean;
  hasIdleCallback: boolean;
}): ObservedTerminalResizePlan {
  const { next, lastCols, lastRows, bufferLength, isVisible, hasIdleCallback } = input;
  const colsChanged = next.cols !== lastCols;
  const rowsChanged = next.rows !== lastRows;

  if (!colsChanged && !rowsChanged) {
    return { type: 'none' };
  }

  if (bufferLength < START_DEBOUNCING_THRESHOLD) {
    return { type: 'resize_both', next };
  }

  if (!isVisible && hasIdleCallback) {
    return { type: 'idle_resize_both', next };
  }

  if (colsChanged && rowsChanged) {
    return {
      type: 'resize_y_then_debounce_x',
      rows: next.rows,
      cols: next.cols,
      diagnostics: next.diagnostics,
    };
  }

  if (rowsChanged) {
    return {
      type: 'resize_y',
      rows: next.rows,
      diagnostics: next.diagnostics,
    };
  }

  return {
    type: 'debounce_x',
    cols: next.cols,
    diagnostics: next.diagnostics,
  };
}

export type VisibilityFlushPlan =
  | { type: 'none' }
  | { type: 'resize'; next: TerminalResizeSnapshot };

export function planVisibilityFlush(input: {
  wasHidden: boolean;
  ready: boolean;
  next: TerminalResizeSnapshot | null;
  currentCols: number;
  currentRows: number;
}): VisibilityFlushPlan {
  const { wasHidden, ready, next, currentCols, currentRows } = input;

  if (!wasHidden || !ready || !next) {
    return { type: 'none' };
  }

  if (next.cols === currentCols && next.rows === currentRows) {
    return { type: 'none' };
  }

  return {
    type: 'resize',
    next,
  };
}
