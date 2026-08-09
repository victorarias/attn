// Pure geometry and resize-policy helpers for the Ghostty terminal pane.

import { isSuspiciousTerminalSize } from '../utils/terminalDebug';
import type { TerminalDimensions } from '../utils/ghosttyResize';

export function isWorkspaceResizeDragActive(element: HTMLElement | null): boolean {
  if (document.documentElement.dataset.attnWorkspaceResizing === '1') {
    return true;
  }
  return Boolean(element?.closest('.session-terminal-panes[data-resizing-split-id]'));
}

export function fitRequiresTerminalResize(
  current: TerminalDimensions,
  next: TerminalDimensions,
): boolean {
  return current.cols !== next.cols || current.rows !== next.rows;
}

// True when the grid is taller than its container, so the last row(s) render
// past the overflow:hidden edge. Only daemon-authoritative geometry can do
// this; fit() floors. The 1px slack absorbs fractional container heights.
export function geometryOverflowsContainer(
  rows: number,
  cellHeight: number,
  clientHeight: number,
): boolean {
  if (rows <= 0 || cellHeight <= 0 || clientHeight <= 0) {
    return false;
  }
  return rows * cellHeight > clientHeight + 1;
}

// Whether fit() should suppress `dims` as a transient pre-layout measurement.
// The bail protects a healthy grid only: it is refused when the live model
// already overflows the container, or when the model is itself suspicious and
// `dims` would not shrink it.
export function fitShouldBailAsSuspicious(
  paneKind: string | undefined,
  dims: TerminalDimensions,
  modelCols: number,
  modelRows: number,
  cellWidth: number,
  cellHeight: number,
  clientWidth: number,
  clientHeight: number,
): boolean {
  if (paneKind !== 'agent') {
    return false;
  }
  if (!isSuspiciousTerminalSize(dims.cols, dims.rows)) {
    return false;
  }
  const overflows = geometryOverflowsContainer(modelRows, cellHeight, clientHeight)
    || geometryOverflowsContainer(modelCols, cellWidth, clientWidth);
  if (overflows) {
    return false;
  }
  const shrinksModel = dims.cols < modelCols || dims.rows < modelRows;
  return !(isSuspiciousTerminalSize(modelCols, modelRows) && !shrinksModel);
}

// Geometry the queued (not yet applied) historical replay will end at.
export interface PendingReplayGeometry extends TerminalDimensions {
  resizes: number;
  lastQueuedAt: number;
}

// Measured: a healthy write chain applies a queued replay resize in
// milliseconds; past this the promise is stale and stops suppressing.
export const REPLAY_GEOMETRY_STALE_MS = 3000;

// A live fit arriving mid-replay only cancels the queued history when it
// targets a different geometry than the replay already ends at; a stale
// queued replay suppresses nothing.
export function liveResizeConflictsWithQueuedReplay(
  pendingReplay: PendingReplayGeometry | null,
  target: TerminalDimensions,
  nowMs: number,
): 'skip' | 'cancel' | 'none' | 'stale' {
  if (!pendingReplay || pendingReplay.resizes <= 0) {
    return 'none';
  }
  if (nowMs - pendingReplay.lastQueuedAt > REPLAY_GEOMETRY_STALE_MS) {
    return 'stale';
  }
  return fitRequiresTerminalResize(pendingReplay, target) ? 'cancel' : 'skip';
}

// Backoff for WebGL renderer-recovery retries; `attempt` is 1-indexed. Null
// once exhausted, so a dead GPU/context falls back to the error state.
export function recoveryDelayMs(attempt: number): number | null {
  const schedule = [250, 1500, 5000];
  return schedule[attempt - 1] ?? null;
}
