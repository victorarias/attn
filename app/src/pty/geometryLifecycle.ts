export interface CommittedGeometry {
  cols: number;
  rows: number;
}

export interface PendingGeometrySync<TXterm = unknown> {
  cols: number;
  rows: number;
  forceRedraw: boolean;
  reason: string;
  xterm: TXterm;
}

export type GeometrySource = 'ready' | 'resize';

export function isCommittedGeometryChanged(
  committed: CommittedGeometry | undefined,
  pending: Pick<PendingGeometrySync, 'cols' | 'rows'>,
): boolean {
  return !committed || committed.cols !== pending.cols || committed.rows !== pending.rows;
}

export function planPendingGeometrySync<TXterm>(
  previous: PendingGeometrySync<TXterm> | undefined,
  input: {
    cols: number;
    rows: number;
    xterm: TXterm;
    source: GeometrySource;
    reason?: string;
    forceRedraw?: boolean;
    readyDelayMs: number;
    resizeDelayMs: number;
  },
): { pending: PendingGeometrySync<TXterm>; delayMs: number } {
  return {
    pending: {
      cols: input.cols,
      rows: input.rows,
      xterm: input.xterm,
      forceRedraw: Boolean(input.forceRedraw || previous?.forceRedraw),
      reason: input.reason || previous?.reason || input.source,
    },
    delayMs: input.source === 'ready' ? input.readyDelayMs : input.resizeDelayMs,
  };
}

export type GeometryFlushPlan =
  | { kind: 'retry_after_pending_ensure'; geometryChanged: boolean }
  | { kind: 'ensure_runtime'; geometryChanged: boolean }
  | {
      kind: 'hydrate_runtime';
      geometryChanged: boolean;
      attach: {
        cols: number;
        rows: number;
        reason: string;
        forceResizeBeforeAttach: boolean;
      };
      nextCommitted: CommittedGeometry;
    }
  | {
      kind: 'resize_runtime';
      geometryChanged: true;
      resize: {
        cols: number;
        rows: number;
        reason: string;
      };
      nextCommitted: CommittedGeometry;
      redrawAfterSettle?: { axis: 'cols' | 'rows' };
    }
  | {
      kind: 'redraw_only';
      geometryChanged: false;
      redraw: {
        cols: number;
        rows: number;
        reason: string;
      };
    }
  | { kind: 'noop'; geometryChanged: boolean };

export function planGeometryFlush(
  input: {
    runtimeReady: boolean;
    ensureInFlight: boolean;
    hydratePending: boolean;
    pending: Pick<PendingGeometrySync, 'cols' | 'rows' | 'forceRedraw' | 'reason'>;
    lastCommitted?: CommittedGeometry;
    spawnIsShell: boolean;
    source: GeometrySource;
  },
): GeometryFlushPlan {
  const geometryChanged = isCommittedGeometryChanged(input.lastCommitted, input.pending);

  if (!input.runtimeReady) {
    if (input.ensureInFlight) {
      return { kind: 'retry_after_pending_ensure', geometryChanged };
    }
    return { kind: 'ensure_runtime', geometryChanged };
  }

  if (input.hydratePending) {
    return {
      kind: 'hydrate_runtime',
      geometryChanged,
      attach: {
        cols: input.pending.cols,
        rows: input.pending.rows,
        reason: input.pending.reason,
        forceResizeBeforeAttach: geometryChanged,
      },
      nextCommitted: {
        cols: input.pending.cols,
        rows: input.pending.rows,
      },
    };
  }

  if (geometryChanged) {
    const shouldRequestAuthoritativeRedraw = Boolean(
      input.lastCommitted &&
      input.source === 'resize' &&
      !input.spawnIsShell,
    );
    return {
      kind: 'resize_runtime',
      geometryChanged: true,
      resize: {
        cols: input.pending.cols,
        rows: input.pending.rows,
        reason: input.pending.reason,
      },
      nextCommitted: {
        cols: input.pending.cols,
        rows: input.pending.rows,
      },
      ...(shouldRequestAuthoritativeRedraw
        ? {
            redrawAfterSettle: {
              axis: input.lastCommitted && input.lastCommitted.cols !== input.pending.cols ? 'cols' : 'rows',
            },
          }
        : {}),
    };
  }

  if (input.pending.forceRedraw) {
    return {
      kind: 'redraw_only',
      geometryChanged: false,
      redraw: {
        cols: input.pending.cols,
        rows: input.pending.rows,
        reason: input.pending.reason,
      },
    };
  }

  return { kind: 'noop', geometryChanged };
}

export function planDeferredGeometryRedraw(
  input: {
    baselineWriteCount: number;
    currentWriteCount: number;
    reason: string;
    axis: 'cols' | 'rows';
  },
): {
  kind: 'skip' | 'request';
  reason: string;
  axis: 'cols' | 'rows';
  baselineWriteCount: number;
  currentWriteCount: number;
} {
  if (input.currentWriteCount > input.baselineWriteCount) {
    return {
      kind: 'skip',
      reason: input.reason,
      axis: input.axis,
      baselineWriteCount: input.baselineWriteCount,
      currentWriteCount: input.currentWriteCount,
    };
  }

  return {
    kind: 'request',
    reason: input.reason,
    axis: input.axis,
    baselineWriteCount: input.baselineWriteCount,
    currentWriteCount: input.currentWriteCount,
  };
}
