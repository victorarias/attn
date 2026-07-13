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

describe('grid-overflow detector', () => {
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

  it('flags an active pane whose grid is one column wider than its container', () => {
    const pane = 'pane-clip-right-overflow';
    // floor(720/9)=80 columns fit, but the model still has 81 → 9px spill.
    const unregister = registerRenderProbe(pane, () => probeWith({
      rows: 25, cellHeight: 21, clientHeight: 525,
      cols: 81, cellWidth: 9, clientWidth: 720,
    }));
    vi.advanceTimersByTime(1600);
    unregister();

    const incidents = ringEventsFor(pane).filter((event) => event.kind === 'incident');
    expect(incidents).toHaveLength(1);
    expect(incidents[0]?.reason).toBe('bottom_clip');
    expect(incidents[0]?.trigger).toEqual(['model_right']);
    expect(incidents[0]?.rightOverflowPx).toBe(9);
    expect(incidents[0]?.extraCols).toBe(1);
  });

  it('resolves once both row and column geometry fit', () => {
    const pane = 'pane-clip-all-clean-then-resolve';
    let clipping = true;
    const unregister = registerRenderProbe(pane, () => (clipping
      ? probeWith({
        rows: 26, cellHeight: 21, clientHeight: 540,
        cols: 81, cellWidth: 9, clientWidth: 720,
      })
      : probeWith({
        rows: 25, cellHeight: 21, clientHeight: 525,
        cols: 80, cellWidth: 9, clientWidth: 720,
      })));
    vi.advanceTimersByTime(1600); // onset
    clipping = false;
    vi.advanceTimersByTime(1600); // both model signals clean → resolution
    unregister();

    const reasons = ringEventsFor(pane)
      .filter((event) => event.kind === 'incident')
      .map((event) => event.reason);
    expect(reasons).toEqual(['bottom_clip', 'bottom_clip_resolved']);
  });
});

describe('clip repair watchdog', () => {
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

  // A grid one row taller than its container — 26 rows at 21px, 540px client
  // height, so floor(540/21)=25 rows fit and there is a persistent 6px spill.
  const clippingGeometry: Partial<RenderProbe> = {
    rows: 26, cellHeight: 21, clientHeight: 540, cellWidth: 9, clientWidth: 720,
    session: 's-repair',
  };
  const fittingGeometry: Partial<RenderProbe> = {
    rows: 25, cellHeight: 21, clientHeight: 525, cellWidth: 9, clientWidth: 720,
    session: 's-repair',
  };

  it('waits for the 3rd consecutive clipping sweep before the first repair', () => {
    const pane = 'pane-repair-persistent';
    const repair = vi.fn();
    const unregister = registerRenderProbe(pane, () => probeWith(clippingGeometry), repair);
    try {
      vi.advanceTimersByTime(1600);
      vi.advanceTimersByTime(1600);
      expect(repair).toHaveBeenCalledTimes(0);

      vi.advanceTimersByTime(1600);
      expect(repair).toHaveBeenCalledTimes(1);
    } finally {
      unregister();
    }
  });

  it('never repairs a clip that clears on the 2nd sweep', () => {
    const pane = 'pane-repair-transient';
    const repair = vi.fn();
    let clipping = true;
    const unregister = registerRenderProbe(
      pane,
      () => probeWith(clipping ? clippingGeometry : fittingGeometry),
      repair,
    );
    try {
      vi.advanceTimersByTime(1600); // sweep 1: clipping
      clipping = false;
      vi.advanceTimersByTime(1600); // sweep 2: clear
      vi.advanceTimersByTime(20000); // long tail, in case a stray timer fires late

      expect(repair).toHaveBeenCalledTimes(0);
    } finally {
      unregister();
    }
  });

  // Sweep ticks land on multiples of BOTTOM_CLIP_SWEEP_MS (1500ms). A repair
  // only fires once Date.now() >= nextAttemptAtMs AT a sweep tick, so the
  // observed delay is the backoff rounded UP to the next tick, never short of it.
  it('backs off 3000ms before the 2nd repair and 10000ms before the 3rd, with the clip persisting', () => {
    const pane = 'pane-repair-backoff';
    const repair = vi.fn();
    const unregister = registerRenderProbe(pane, () => probeWith(clippingGeometry), repair);
    try {
      vi.advanceTimersByTime(4500); // t=4500: 3rd sweep -> 1st repair fires
      expect(repair).toHaveBeenCalledTimes(1);

      vi.advanceTimersByTime(1500); // t=6000, only 1500ms after the 1st repair
      expect(repair).toHaveBeenCalledTimes(1);

      vi.advanceTimersByTime(1500); // t=7500, 3000ms after the 1st repair
      expect(repair).toHaveBeenCalledTimes(2);

      vi.advanceTimersByTime(9000); // t=16500, 9000ms after the 2nd repair (< 10000ms)
      expect(repair).toHaveBeenCalledTimes(2);

      vi.advanceTimersByTime(1500); // t=18000, first tick >= 10000ms after the 2nd repair
      expect(repair).toHaveBeenCalledTimes(3);
    } finally {
      unregister();
    }
  });

  it('gives up after 3 failed repairs and records exactly one give-up incident', () => {
    const pane = 'pane-repair-give-up';
    const repair = vi.fn();
    const unregister = registerRenderProbe(pane, () => probeWith(clippingGeometry), repair);
    try {
      vi.advanceTimersByTime(4500); // t=4500: 1st repair
      vi.advanceTimersByTime(3000); // t=7500: 2nd repair (3000ms backoff)
      vi.advanceTimersByTime(10500); // t=18000: first tick >= 10000ms after the 2nd -> 3rd repair
      expect(repair).toHaveBeenCalledTimes(3);

      vi.advanceTimersByTime(60000); // clip persists well past the 3rd attempt's backoff
      expect(repair).toHaveBeenCalledTimes(3); // no 4th attempt — the watchdog gave up

      const giveUps = ringEventsFor(pane)
        .filter((event) => event.kind === 'incident' && event.reason === 'bottom_clip_repair_gave_up');
      expect(giveUps).toHaveLength(1);
      expect(giveUps[0]?.attempts).toBe(3);
    } finally {
      unregister();
    }
  });

  it('carries repairAttempts on the resolution incident after one repair fired', () => {
    const pane = 'pane-repair-resolved';
    const repair = vi.fn();
    let clipping = true;
    const unregister = registerRenderProbe(
      pane,
      () => probeWith(clipping ? clippingGeometry : fittingGeometry),
      repair,
    );
    try {
      vi.advanceTimersByTime(4500); // 3 sweeps: 1st repair fires
      expect(repair).toHaveBeenCalledTimes(1);
      clipping = false;
      vi.advanceTimersByTime(1600); // clip clears

      const resolved = ringEventsFor(pane)
        .filter((event) => event.kind === 'incident' && event.reason === 'bottom_clip_resolved');
      expect(resolved).toHaveLength(1);
      expect(resolved[0]?.repairAttempts).toBe(1);
    } finally {
      unregister();
    }
  });

  it('never repairs an inactive pane even with overflowing geometry', () => {
    const pane = 'pane-repair-inactive';
    const repair = vi.fn();
    const unregister = registerRenderProbe(
      pane,
      () => probeWith({ ...clippingGeometry, active: false }),
      repair,
    );
    try {
      vi.advanceTimersByTime(20000);
      expect(repair).toHaveBeenCalledTimes(0);
    } finally {
      unregister();
    }
  });

  it('still logs bottom_clip incidents (and does not throw) for a pane with no repair handler', () => {
    const pane = 'pane-repair-none';
    const unregister = registerRenderProbe(pane, () => probeWith(clippingGeometry));
    try {
      expect(() => {
        vi.advanceTimersByTime(20000);
      }).not.toThrow();

      const incidents = ringEventsFor(pane).filter((event) => event.kind === 'incident' && event.reason === 'bottom_clip');
      expect(incidents).toHaveLength(1);
    } finally {
      unregister();
    }
  });
});
