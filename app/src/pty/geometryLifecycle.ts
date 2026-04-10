export interface CommittedGeometry {
  cols: number;
  rows: number;
}

export interface PendingGeometrySync<TXterm = unknown> {
  cols: number;
  rows: number;
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
    readyDelayMs: number;
    resizeDelayMs: number;
  },
): { pending: PendingGeometrySync<TXterm>; delayMs: number } {
  return {
    pending: {
      cols: input.cols,
      rows: input.rows,
      xterm: input.xterm,
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
    }
  | { kind: 'noop'; geometryChanged: boolean };

export function planGeometryFlush(
  input: {
    runtimeReady: boolean;
    ensureInFlight: boolean;
    hydratePending: boolean;
    pending: Pick<PendingGeometrySync, 'cols' | 'rows' | 'reason'>;
    lastCommitted?: CommittedGeometry;
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
    };
  }

  return { kind: 'noop', geometryChanged };
}
