import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { noteResize, recordDiag, recordPaint, registerRenderProbe } from './terminalDiagnosticsLog';

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
