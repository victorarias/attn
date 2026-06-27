import { afterEach, describe, expect, it } from 'vitest';
import {
  fitRequiresTerminalResize,
  fitShouldBailAsSuspicious,
  geometryOverflowsContainer,
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

describe('geometryOverflowsContainer', () => {
  // The clipped-bottom-row bug: the daemon left the PTY one row taller than this
  // window fits, so the canvas spilled below the viewport. cellHeight 21.
  it('flags a grid one row taller than the container (the bug)', () => {
    // window fits floor(540/21)=25 rows, but the model is 26.
    expect(geometryOverflowsContainer(26, 21, 540)).toBe(true);
  });

  it('does not flag a grid that fits, including the floor() remainder gap', () => {
    // 25 rows * 21 = 525 <= 540 (a harmless 15px gap below the last row).
    expect(geometryOverflowsContainer(25, 21, 540)).toBe(false);
    // Exact fit.
    expect(geometryOverflowsContainer(25, 21, 525)).toBe(false);
  });

  it('tolerates a 1px sub-pixel container height without a spurious refit', () => {
    // 27 * 21 = 567; a 566px container is within the 1px slack.
    expect(geometryOverflowsContainer(27, 21, 566)).toBe(false);
    // Two px short is a genuine overflow.
    expect(geometryOverflowsContainer(27, 21, 565)).toBe(true);
  });

  it('never flags degenerate/zero dimensions (pre-measure, hidden pane)', () => {
    expect(geometryOverflowsContainer(27, 21, 0)).toBe(false);
    expect(geometryOverflowsContainer(0, 21, 540)).toBe(false);
    expect(geometryOverflowsContainer(27, 0, 540)).toBe(false);
  });
});

describe('fitShouldBailAsSuspicious', () => {
  // cellWidth 9, cellHeight 21 throughout, matching the captured incident.
  // Args: (paneKind, dims, modelCols, modelRows, cellWidth, cellHeight, clientWidth, clientHeight).
  it('does NOT bail when a small fit is required to stop the bottom-row clip', () => {
    // The bug: a deep stacked split fits ~7 rows (rows<=10 => "suspicious"), but
    // the live model is stranded at 13 rows, overflowing the 147px container. The
    // small fit MUST apply — refusing it leaves the model taller than the pane and
    // the last rows clip below the overflow:hidden edge.
    expect(fitShouldBailAsSuspicious('agent', { cols: 73, rows: 7 }, 73, 13, 9, 21, 720, 147)).toBe(false);
  });

  it('does NOT bail when a small fit is required to stop the right-column clip', () => {
    // The width analog: a narrow side-by-side split fits ~16 cols (cols<=20 =>
    // "suspicious"), but the model is stranded at 40 cols overflowing the 150px
    // container width. Height fits, so only the width check catches this.
    expect(fitShouldBailAsSuspicious('agent', { cols: 16, rows: 40 }, 40, 40, 9, 21, 150, 840)).toBe(false);
  });

  it('bails on a suspicious fit when the model does not overflow (transient measurement)', () => {
    // Pre-layout / first-show: container not yet measured (client ~0), so the
    // model cannot overflow it. The tiny dims are garbage and must be suppressed.
    expect(fitShouldBailAsSuspicious('agent', { cols: 2, rows: 1 }, 13, 13, 9, 21, 0, 0)).toBe(true);
    // A settled, comfortably-sized container with a transient tiny fit also bails.
    expect(fitShouldBailAsSuspicious('agent', { cols: 4, rows: 2 }, 24, 24, 9, 21, 720, 540)).toBe(true);
  });

  it('never bails for non-agent panes (utility terminals manage their own size)', () => {
    expect(fitShouldBailAsSuspicious('utility', { cols: 2, rows: 1 }, 24, 24, 9, 21, 720, 540)).toBe(false);
    expect(fitShouldBailAsSuspicious(undefined, { cols: 2, rows: 1 }, 24, 24, 9, 21, 720, 540)).toBe(false);
  });

  it('never bails when the floored fit is a normal usable size', () => {
    expect(fitShouldBailAsSuspicious('agent', { cols: 120, rows: 40 }, 120, 40, 9, 21, 1080, 840)).toBe(false);
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
