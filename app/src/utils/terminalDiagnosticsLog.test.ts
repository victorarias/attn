import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  dumpTerminalGeometry,
  noteRecovery,
  noteResize,
  recordDiag,
  recordPaint,
  registerRenderProbe,
  type RenderProbe,
} from './terminalDiagnosticsLog';

function ringEventsFor(pane: string) {
  return (window.__ATTN_TERMINAL_DIAG_DUMP?.() ?? []).filter((event) => event.pane === pane);
}

describe('terminal diagnostics ring', () => {
  it('keeps the newest events in chronological order after wrapping', () => {
    window.localStorage.setItem('attn:terminal-diagnostics', '1');
    for (let sequence = 0; sequence < 3005; sequence += 1) {
      recordDiag({ kind: 'write', sequence });
    }

    const events = window.__ATTN_TERMINAL_DIAG_DUMP?.() ?? [];
    expect(events).toHaveLength(3000);
    expect(events[0]?.sequence).toBe(5);
    expect(events[events.length - 1]?.sequence).toBe(3004);
  });
});

describe('paint anomaly detection', () => {
  beforeEach(() => {
    window.localStorage.setItem('attn:terminal-diagnostics', '1');
  });

  const baseSample = {
    session: 's-test',
    cols: 80,
    rows: 24,
    force: false,
    offset: 0,
    cellsArrayLen: null,
    skipNull: null,
    skipZeroWidth: null,
  };

  it('does not flag a renderer skip (quads null) as an under-drawn paint', () => {
    const pane = 'pane-skip-not-underdraw';
    // A skip means "nothing dirty, canvas untouched" — the surface still shows
    // the previous draw. It must never be judged against model content.
    recordPaint({ ...baseSample, pane, modelPrintable: 500, quads: null });

    expect(ringEventsFor(pane).filter((event) => event.kind === 'incident')).toHaveLength(0);
  });

  it('flags a real draw that paints far less than the model holds', () => {
    const pane = 'pane-real-underdraw';
    recordPaint({ ...baseSample, pane, modelPrintable: 500, quads: 3 });

    const incidents = ringEventsFor(pane).filter((event) => event.kind === 'incident');
    expect(incidents).toHaveLength(1);
    expect(incidents[0]?.reason).toBe('paint_underdraw');
  });

  it('a trailing skip after a healthy draw leaves the pane healthy', () => {
    const pane = 'pane-skip-after-draw';
    recordPaint({ ...baseSample, pane, modelPrintable: 500, quads: 500 });
    recordPaint({ ...baseSample, pane, modelPrintable: 500, quads: null });

    expect(ringEventsFor(pane).filter((event) => event.kind === 'incident')).toHaveLength(0);
  });
});

describe('noteRecovery', () => {
  beforeEach(() => {
    window.localStorage.setItem('attn:terminal-diagnostics', '1');
  });

  it('records the recovery lifecycle as lifecycle events, in order', () => {
    const pane = 'pane-recovery';
    noteRecovery(pane, { attempt: 1, outcome: 'contextLost' });
    noteRecovery(pane, { attempt: 1, outcome: 'scheduled', delayMs: 250 });
    noteRecovery(pane, { attempt: 1, outcome: 'recovered' });

    const events = ringEventsFor(pane).filter((event) => event.kind === 'recovery');
    expect(events.map((event) => event.outcome)).toEqual(['contextLost', 'scheduled', 'recovered']);
    expect(events[1]?.delayMs).toBe(250);
  });
});

describe('blank-after-resize watchdog', () => {
  beforeEach(() => {
    window.localStorage.setItem('attn:terminal-diagnostics', '1');
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  function armAgentResize(pane: string) {
    // The watchdog only arms on a real geometry change of an agent pane.
    noteResize(pane, {
      source: 'fit',
      paneKind: 'agent',
      fromCols: 80,
      fromRows: 24,
      toCols: 100,
      toRows: 30,
    });
  }

  it('flags an active pane whose last draw painted nothing despite model content', () => {
    const pane = 'pane-watchdog-blank';
    const unregister = registerRenderProbe(pane, () => ({
      cols: 100,
      rows: 30,
      modelPrintable: 500,
      lastPaintAt: Date.now(),
      lastPaintQuads: 0,
      active: true,
    }));
    // Seed pane health so the watchdog has a resize timestamp to compare with.
    recordPaint({
      pane,
      session: 's-test',
      cols: 80,
      rows: 24,
      force: true,
      offset: 0,
      modelPrintable: 10,
      quads: 10,
      cellsArrayLen: null,
      skipNull: null,
      skipZeroWidth: null,
    });
    armAgentResize(pane);
    vi.advanceTimersByTime(1300);
    unregister();

    const incidents = ringEventsFor(pane).filter((event) => event.kind === 'incident');
    expect(incidents.length).toBeGreaterThan(0);
    expect(incidents[0]?.reason).toBe('blank_after_resize');
  });

  it('skips judgement for an inactive pane, which cannot paint by design', () => {
    const pane = 'pane-watchdog-inactive';
    const unregister = registerRenderProbe(pane, () => ({
      cols: 100,
      rows: 30,
      modelPrintable: 500,
      lastPaintAt: 0,
      lastPaintQuads: 0,
      active: false,
    }));
    armAgentResize(pane);
    vi.advanceTimersByTime(4000);
    unregister();

    const events = ringEventsFor(pane);
    expect(events.filter((event) => event.kind === 'incident')).toHaveLength(0);
    expect(events.some((event) => event.kind === 'watchdog' && event.skipped === 'inactive')).toBe(true);
  });
});

describe('bottom-clip detector', () => {
  beforeEach(() => {
    window.localStorage.setItem('attn:terminal-diagnostics', '1');
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  const probeWith = (over: Partial<RenderProbe>): RenderProbe => ({
    cols: 80,
    rows: 24,
    modelPrintable: 100,
    lastPaintAt: Date.now(),
    lastPaintQuads: 100,
    active: true,
    ...over,
  });

  it('flags an active pane whose grid is one row taller than its container', () => {
    const pane = 'pane-clip-overflow';
    // floor(540/21)=25 rows fit, but the daemon left the model at 26 → 6px spill.
    const unregister = registerRenderProbe(pane, () => probeWith({
      rows: 26, cellHeight: 21, clientHeight: 540, cellWidth: 9, clientWidth: 720,
      session: 's-clip', isActivePane: true, hasMeasuredSize: true,
    }));
    vi.advanceTimersByTime(1600);
    unregister();

    const incidents = ringEventsFor(pane).filter((event) => event.kind === 'incident');
    expect(incidents).toHaveLength(1);
    expect(incidents[0]?.reason).toBe('bottom_clip');
    expect(incidents[0]?.flooredRows).toBe(25);
    expect(incidents[0]?.extraRows).toBe(1);
    expect(incidents[0]?.overflowPx).toBe(6);
  });

  it('does not flag a grid that fits within the container', () => {
    const pane = 'pane-clip-fits';
    const unregister = registerRenderProbe(pane, () => probeWith({
      rows: 25, cellHeight: 21, clientHeight: 540,
    }));
    vi.advanceTimersByTime(1600);
    unregister();
    expect(ringEventsFor(pane).filter((event) => event.kind === 'incident')).toHaveLength(0);
  });

  it('does not flag an inactive pane even when its grid overflows', () => {
    const pane = 'pane-clip-inactive';
    const unregister = registerRenderProbe(pane, () => probeWith({
      active: false, rows: 26, cellHeight: 21, clientHeight: 540,
    }));
    vi.advanceTimersByTime(1600);
    unregister();
    expect(ringEventsFor(pane).filter((event) => event.kind === 'incident')).toHaveLength(0);
  });

  it('reports the clip once, then a resolution when it clears', () => {
    const pane = 'pane-clip-resolve';
    let rows = 26;
    const unregister = registerRenderProbe(pane, () => probeWith({
      rows, cellHeight: 21, clientHeight: 540,
    }));
    vi.advanceTimersByTime(1600); // onset
    vi.advanceTimersByTime(1600); // still clipping → edge-triggered, no repeat
    rows = 25; // a refit floored it back
    vi.advanceTimersByTime(1600); // resolution
    unregister();

    const reasons = ringEventsFor(pane)
      .filter((event) => event.kind === 'incident')
      .map((event) => event.reason);
    expect(reasons).toEqual(['bottom_clip', 'bottom_clip_resolved']);
  });

  it('dumpTerminalGeometry computes overflow and floored dims per pane', () => {
    const pane = 'pane-clip-dump';
    const unregister = registerRenderProbe(pane, () => probeWith({
      rows: 26, cols: 80, cellHeight: 21, cellWidth: 9, clientHeight: 540, clientWidth: 720,
    }));
    const snapshot = dumpTerminalGeometry().find((entry) => entry.pane === pane);
    unregister();

    expect(snapshot).toMatchObject({
      rows: 26, flooredRows: 25, flooredCols: 80, overflowPx: 6, clipping: true,
    });
  });

  it('flags DOM-truth bottom overflow even when the model math is clean', () => {
    const pane = 'pane-clip-dom-overflow';
    // Model math is clean (rows*cellHeight == clientHeight), but the real
    // canvas rect spills 5px past the container's bottom edge.
    const unregister = registerRenderProbe(pane, () => probeWith({
      rows: 25, cellHeight: 21, clientHeight: 525,
      containerTop: 0, containerBottom: 525, canvasTop: 0, canvasBottom: 530,
    }));
    vi.advanceTimersByTime(1600);
    unregister();

    const incidents = ringEventsFor(pane).filter((event) => event.kind === 'incident');
    expect(incidents).toHaveLength(1);
    expect(incidents[0]?.reason).toBe('bottom_clip');
    expect(incidents[0]?.trigger).toEqual(['dom_overflow']);
    expect(incidents[0]?.domOverflowPx).toBe(5);
    expect(incidents[0]?.domOffsetTopPx).toBe(0);
  });

  it('flags a DOM-truth top offset even when the model math is clean', () => {
    const pane = 'pane-clip-dom-offset';
    const unregister = registerRenderProbe(pane, () => probeWith({
      rows: 25, cellHeight: 21, clientHeight: 525,
      containerTop: 0, containerBottom: 525, canvasTop: 4, canvasBottom: 525,
    }));
    vi.advanceTimersByTime(1600);
    unregister();

    const incidents = ringEventsFor(pane).filter((event) => event.kind === 'incident');
    expect(incidents).toHaveLength(1);
    expect(incidents[0]?.reason).toBe('bottom_clip');
    expect(incidents[0]?.trigger).toEqual(['dom_offset']);
    expect(incidents[0]?.domOffsetTopPx).toBe(4);
    expect(incidents[0]?.domOverflowPx).toBe(0);
  });

  it('stays clean and resolves a previously-clipping pane when all three signals agree', () => {
    const pane = 'pane-clip-all-clean-then-resolve';
    let clipping = true;
    const unregister = registerRenderProbe(pane, () => (clipping
      ? probeWith({
        rows: 26, cellHeight: 21, clientHeight: 540,
        containerTop: 0, containerBottom: 540, canvasTop: 0, canvasBottom: 546,
      })
      : probeWith({
        rows: 25, cellHeight: 21, clientHeight: 525,
        containerTop: 0, containerBottom: 525, canvasTop: 0, canvasBottom: 525,
      })));
    vi.advanceTimersByTime(1600); // onset (model + dom overflow agree)
    clipping = false;
    vi.advanceTimersByTime(1600); // all three signals clean → resolution
    unregister();

    const reasons = ringEventsFor(pane)
      .filter((event) => event.kind === 'incident')
      .map((event) => event.reason);
    expect(reasons).toEqual(['bottom_clip', 'bottom_clip_resolved']);
  });

  it('flags a right-edge-only overflow that the bottom-only signals miss', () => {
    const pane = 'pane-clip-right-overflow';
    // Bottom is clean (even slightly under, i.e. negative overflow), but the
    // canvas spills 7px past the container's right edge — the field repro
    // this detector was blind to before the right/left signals were added.
    const unregister = registerRenderProbe(pane, () => probeWith({
      rows: 25, cellHeight: 21, clientHeight: 525,
      containerTop: 0, containerBottom: 525, canvasTop: 0, canvasBottom: 525,
      containerLeft: 0, containerRight: 720, canvasLeft: 0, canvasRight: 727,
    }));
    vi.advanceTimersByTime(1600);
    unregister();

    const incidents = ringEventsFor(pane).filter((event) => event.kind === 'incident');
    expect(incidents).toHaveLength(1);
    expect(incidents[0]?.reason).toBe('bottom_clip');
    expect(incidents[0]?.trigger).toEqual(['dom_overflow_right']);
    expect(incidents[0]?.domOverflowRightPx).toBe(7);
    expect(incidents[0]?.domOffsetLeftPx).toBe(0);
  });

  it('stays clean when left/right rects agree with the container', () => {
    const pane = 'pane-clip-lr-clean';
    const unregister = registerRenderProbe(pane, () => probeWith({
      rows: 25, cellHeight: 21, clientHeight: 525,
      containerTop: 0, containerBottom: 525, canvasTop: 0, canvasBottom: 525,
      containerLeft: 0, containerRight: 720, canvasLeft: 0, canvasRight: 720,
    }));
    vi.advanceTimersByTime(1600);
    unregister();

    expect(ringEventsFor(pane).filter((event) => event.kind === 'incident')).toHaveLength(0);
  });
});
