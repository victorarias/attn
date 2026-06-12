import { afterEach, describe, expect, it } from 'vitest';
import {
  fitRequiresTerminalResize,
  isWorkspaceResizeDragActive,
  liveResizeConflictsWithQueuedReplay,
} from './GhosttyTerminal';

describe('GhosttyTerminal resize policy', () => {
  afterEach(() => {
    delete document.documentElement.dataset.attnWorkspaceResizing;
  });

  it('detects when a workspace split drag is active', () => {
    const panes = document.createElement('div');
    panes.className = 'session-terminal-panes';
    panes.dataset.resizingSplitId = 'root';
    const terminal = document.createElement('div');
    panes.appendChild(terminal);

    expect(isWorkspaceResizeDragActive(terminal)).toBe(true);

    delete panes.dataset.resizingSplitId;
    expect(isWorkspaceResizeDragActive(terminal)).toBe(false);

    document.documentElement.dataset.attnWorkspaceResizing = '1';
    expect(isWorkspaceResizeDragActive(terminal)).toBe(true);
  });

  it('treats identical fit geometry as a no-op', () => {
    expect(fitRequiresTerminalResize(
      { cols: 120, rows: 40 },
      { cols: 120, rows: 40 },
    )).toBe(false);
    expect(fitRequiresTerminalResize(
      { cols: 120, rows: 40 },
      { cols: 121, rows: 40 },
    )).toBe(true);
  });
});

describe('liveResizeConflictsWithQueuedReplay', () => {
  // The relaunch blank-pane regression: replay marches the model through a
  // historical 80x24 segment; a selection fit (and the pre-attach resize echo)
  // target the live 62x27 geometry the replay ends at. Cancelling the queued
  // history for those leaves the pane permanently empty.
  it('skips a live resize that targets the geometry the queued replay ends at', () => {
    expect(liveResizeConflictsWithQueuedReplay(
      { cols: 62, rows: 27, resizes: 1 },
      { cols: 62, rows: 27 },
    )).toBe('skip');
  });

  it('cancels the queued replay for a genuinely different live geometry', () => {
    expect(liveResizeConflictsWithQueuedReplay(
      { cols: 62, rows: 27, resizes: 1 },
      { cols: 95, rows: 53 },
    )).toBe('cancel');
  });

  it('does not interfere once all queued replay resizes have applied', () => {
    expect(liveResizeConflictsWithQueuedReplay(
      { cols: 62, rows: 27, resizes: 0 },
      { cols: 95, rows: 53 },
    )).toBe('none');
    expect(liveResizeConflictsWithQueuedReplay(null, { cols: 62, rows: 27 })).toBe('none');
  });
});
