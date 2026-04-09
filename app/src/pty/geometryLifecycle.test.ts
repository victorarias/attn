import { describe, expect, it } from 'vitest';
import {
  isCommittedGeometryChanged,
  planDeferredGeometryRedraw,
  planGeometryFlush,
  planPendingGeometrySync,
} from './geometryLifecycle';

describe('geometryLifecycle', () => {
  it('merges pending geometry sync state and uses source-specific delays', () => {
    const planned = planPendingGeometrySync(
      {
        cols: 80,
        rows: 24,
        forceRedraw: true,
        reason: 'previous',
        xterm: 'old',
      },
      {
        cols: 120,
        rows: 40,
        xterm: 'new',
        source: 'resize',
        resizeDelayMs: 90,
        readyDelayMs: 60,
      },
    );

    expect(planned.delayMs).toBe(90);
    expect(planned.pending).toEqual({
      cols: 120,
      rows: 40,
      xterm: 'new',
      forceRedraw: true,
      reason: 'previous',
    });
  });

  it('detects committed geometry changes', () => {
    expect(isCommittedGeometryChanged(undefined, { cols: 80, rows: 24 })).toBe(true);
    expect(isCommittedGeometryChanged({ cols: 80, rows: 24 }, { cols: 80, rows: 24 })).toBe(false);
    expect(isCommittedGeometryChanged({ cols: 80, rows: 24 }, { cols: 81, rows: 24 })).toBe(true);
  });

  it('retries flush while runtime ensure is still in flight', () => {
    expect(planGeometryFlush({
      runtimeReady: false,
      ensureInFlight: true,
      hydratePending: false,
      pending: { cols: 80, rows: 24, forceRedraw: false, reason: 'ready' },
      spawnIsShell: true,
      source: 'ready',
    })).toEqual({
      kind: 'retry_after_pending_ensure',
      geometryChanged: true,
    });
  });

  it('hydrates remounted runtimes with same-app attach policy inputs', () => {
    expect(planGeometryFlush({
      runtimeReady: true,
      ensureInFlight: false,
      hydratePending: true,
      pending: { cols: 132, rows: 41, forceRedraw: false, reason: 'init_hydrate' },
      lastCommitted: { cols: 80, rows: 24 },
      spawnIsShell: false,
      source: 'ready',
    })).toEqual({
      kind: 'hydrate_runtime',
      geometryChanged: true,
      attach: {
        cols: 132,
        rows: 41,
        reason: 'init_hydrate',
        forceResizeBeforeAttach: true,
      },
      nextCommitted: { cols: 132, rows: 41 },
    });
  });

  it('requests authoritative redraw after non-shell resize changes', () => {
    expect(planGeometryFlush({
      runtimeReady: true,
      ensureInFlight: false,
      hydratePending: false,
      pending: { cols: 140, rows: 43, forceRedraw: false, reason: 'fit' },
      lastCommitted: { cols: 80, rows: 43 },
      spawnIsShell: false,
      source: 'resize',
    })).toEqual({
      kind: 'resize_runtime',
      geometryChanged: true,
      resize: {
        cols: 140,
        rows: 43,
        reason: 'fit',
      },
      nextCommitted: { cols: 140, rows: 43 },
      redrawAfterSettle: { axis: 'cols' },
    });
  });

  it('skips authoritative redraw for shell resize changes', () => {
    expect(planGeometryFlush({
      runtimeReady: true,
      ensureInFlight: false,
      hydratePending: false,
      pending: { cols: 140, rows: 43, forceRedraw: false, reason: 'fit' },
      lastCommitted: { cols: 80, rows: 43 },
      spawnIsShell: true,
      source: 'resize',
    })).toEqual({
      kind: 'resize_runtime',
      geometryChanged: true,
      resize: {
        cols: 140,
        rows: 43,
        reason: 'fit',
      },
      nextCommitted: { cols: 140, rows: 43 },
    });
  });

  it('requests redraw-only flush when geometry is unchanged but redraw is forced', () => {
    expect(planGeometryFlush({
      runtimeReady: true,
      ensureInFlight: false,
      hydratePending: false,
      pending: { cols: 80, rows: 24, forceRedraw: true, reason: 'visibility_flush_same_size' },
      lastCommitted: { cols: 80, rows: 24 },
      spawnIsShell: false,
      source: 'resize',
    })).toEqual({
      kind: 'redraw_only',
      geometryChanged: false,
      redraw: {
        cols: 80,
        rows: 24,
        reason: 'visibility_flush_same_size',
      },
    });
  });

  it('skips deferred redraw when fresh output already arrived', () => {
    expect(planDeferredGeometryRedraw({
      baselineWriteCount: 3,
      currentWriteCount: 4,
      reason: 'fit:resize_redraw',
      axis: 'cols',
    })).toEqual({
      kind: 'skip',
      baselineWriteCount: 3,
      currentWriteCount: 4,
      reason: 'fit:resize_redraw',
      axis: 'cols',
    });
  });
});
