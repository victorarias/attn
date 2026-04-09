import type { PtyAttachPolicy } from './bridge';

export interface AttachRequestContext {
  requestedCols: number;
  requestedRows: number;
  policy: PtyAttachPolicy;
}

export interface AttachReplayData {
  scrollback?: string;
  screen_snapshot?: string;
  screen_snapshot_fresh?: boolean;
  screen_cols?: number;
  screen_rows?: number;
  cols?: number;
  rows?: number;
}

export interface AttachGeometryData {
  cols?: number;
  rows?: number;
  screen_snapshot?: string;
  scrollback?: string;
  screen_cols?: number;
  screen_rows?: number;
}

export interface AttachRuntimeRequest {
  cols: number;
  rows: number;
  shell?: boolean;
}

export interface PendingAttachOutputChunk {
  data: string;
  seq?: number;
}

export function enqueuePendingAttachOutput(
  queuedOutputs: PendingAttachOutputChunk[],
  chunk: PendingAttachOutputChunk,
  maxPendingOutputs: number,
): PendingAttachOutputChunk[] {
  const nextQueue = [...queuedOutputs];
  if (nextQueue.length >= maxPendingOutputs) {
    nextQueue.shift();
  }
  nextQueue.push(chunk);
  return nextQueue;
}

export function createAttachRequestContext(
  args: Pick<AttachRuntimeRequest, 'cols' | 'rows'>,
  policy: PtyAttachPolicy,
): AttachRequestContext {
  return {
    requestedCols: args.cols,
    requestedRows: args.rows,
    policy,
  };
}

export function classifyAttachReplay(
  data: AttachReplayData,
  context?: AttachRequestContext,
) {
  const hasScreenSnapshot = Boolean(data.screen_snapshot && data.screen_snapshot_fresh !== false);
  const hasReplayPayload = Boolean((hasScreenSnapshot && data.screen_snapshot) || data.scrollback);
  const replayKind = hasScreenSnapshot
    ? 'screen_snapshot'
    : data.scrollback
      ? 'scrollback'
      : 'none';
  const attachedCols = typeof data.cols === 'number' ? data.cols : null;
  const attachedRows = typeof data.rows === 'number' ? data.rows : null;
  const replayCols = typeof data.screen_cols === 'number' ? data.screen_cols : attachedCols;
  const replayRows = typeof data.screen_rows === 'number' ? data.screen_rows : attachedRows;
  const requestedCols = context?.requestedCols ?? null;
  const requestedRows = context?.requestedRows ?? null;
  const attachedGeometryMismatch = requestedCols !== null && requestedRows !== null && (
    attachedCols !== requestedCols || attachedRows !== requestedRows
  );
  const replayGeometryMismatch = requestedCols !== null && requestedRows !== null && hasReplayPayload && (
    replayCols !== requestedCols || replayRows !== requestedRows
  );
  const replayAllowedByPolicy = context?.policy === 'relaunch_restore';
  const replaySkipped = hasReplayPayload && (
    !replayAllowedByPolicy ||
    attachedGeometryMismatch ||
    replayGeometryMismatch
  );
  const replayApplied = hasReplayPayload && !replaySkipped;

  return {
    hasScreenSnapshot,
    hasReplayPayload,
    replayKind,
    attachedCols,
    attachedRows,
    replayCols,
    replayRows,
    requestedCols,
    requestedRows,
    attachedGeometryMismatch,
    replayGeometryMismatch,
    replayAllowedByPolicy,
    replayApplied,
    replaySkipped,
  };
}

export function planAttachedRuntimeGeometry(
  args: AttachRuntimeRequest,
  attachResult: AttachGeometryData,
  options: { attachPolicy: PtyAttachPolicy; forceShellRedraw?: boolean },
) {
  const requestedCols = args.cols;
  const requestedRows = args.rows;
  const attachedCols = typeof attachResult.cols === 'number' ? attachResult.cols : null;
  const attachedRows = typeof attachResult.rows === 'number' ? attachResult.rows : null;
  const hasScreenSnapshotReplay = Boolean(attachResult.screen_snapshot);
  const hasRawScrollbackReplay = Boolean(attachResult.scrollback) && !hasScreenSnapshotReplay;
  const replayCols = typeof attachResult.screen_cols === 'number' ? attachResult.screen_cols : null;
  const replayRows = typeof attachResult.screen_rows === 'number' ? attachResult.screen_rows : null;
  const ptyGeometryMatches = attachedCols === requestedCols && attachedRows === requestedRows;
  const replayGeometryMatches = hasScreenSnapshotReplay
    ? replayCols === requestedCols && replayRows === requestedRows
    : hasRawScrollbackReplay
      ? ptyGeometryMatches
      : false;
  const shouldRedrawNonShellAttach = !args.shell && (
    options.attachPolicy === 'same_app_remount'
    || (options.attachPolicy === 'relaunch_restore' && hasScreenSnapshotReplay)
  );
  const redrawRequired = Boolean(
    options.forceShellRedraw ||
    shouldRedrawNonShellAttach
  );
  const resizeRequired = !ptyGeometryMatches;

  return {
    requestedCols,
    requestedRows,
    attachedCols,
    attachedRows,
    replayCols,
    replayRows,
    ptyGeometryMatches,
    replayGeometryMatches,
    hasScreenSnapshotReplay,
    hasRawScrollbackReplay,
    resizeRequired,
    redrawRequired,
    strategy: resizeRequired ? 'resize' : redrawRequired ? 'redraw' : 'none',
    attachPolicy: options.attachPolicy,
    forceShellRedraw: options.forceShellRedraw ?? false,
  };
}

export function planAttachResultEffects({
  attachResult,
  replayPlan,
  previousSeq,
  queuedOutputs,
  sessionAgent,
}: {
  attachResult: {
    last_seq?: number;
    screen_snapshot?: string;
    scrollback?: string;
    scrollback_truncated?: boolean;
  };
  replayPlan: ReturnType<typeof classifyAttachReplay>;
  previousSeq?: number;
  queuedOutputs?: PendingAttachOutputChunk[];
  sessionAgent?: string | null;
}) {
  const shouldReset = replayPlan.replayAllowedByPolicy && (
    replayPlan.replayApplied || typeof previousSeq === 'number'
  );
  const resetReason = shouldReset
    ? (replayPlan.hasScreenSnapshot && replayPlan.replayApplied ? 'snapshot_restore' : 'reattach')
    : null;
  const replayAction = replayPlan.replaySkipped
    ? {
        kind: 'skipped' as const,
        replayKind: replayPlan.replayKind,
      }
    : replayPlan.replayApplied && replayPlan.hasScreenSnapshot && attachResult.screen_snapshot
      ? {
          kind: 'screen_snapshot' as const,
          replayKind: 'screen_snapshot' as const,
          data: attachResult.screen_snapshot,
        }
      : replayPlan.replayApplied && attachResult.scrollback
        ? {
            kind: 'scrollback' as const,
            replayKind: 'scrollback' as const,
            data: attachResult.scrollback,
          }
        : {
            kind: 'none' as const,
            replayKind: replayPlan.replayKind,
          };

  let nextSeq = typeof attachResult.last_seq === 'number' ? attachResult.last_seq : 0;
  const queuedOutputsToEmit: PendingAttachOutputChunk[] = [];
  for (const chunk of queuedOutputs || []) {
    if (typeof chunk.seq === 'number' && chunk.seq < nextSeq) {
      continue;
    }
    if (typeof chunk.seq === 'number') {
      nextSeq = chunk.seq;
    }
    queuedOutputsToEmit.push(chunk);
  }

  const shouldWarnTruncatedRestore = Boolean(
    !replayPlan.hasScreenSnapshot &&
    attachResult.scrollback_truncated &&
    String(sessionAgent || '').toLowerCase() === 'codex',
  );

  return {
    shouldReset,
    resetReason,
    replayAction,
    nextSeq,
    queuedOutputsToEmit,
    shouldWarnTruncatedRestore,
  };
}

export function planLivePtyOutput({
  incomingSeq,
  lastSeq,
}: {
  incomingSeq?: number;
  lastSeq?: number;
}) {
  const shouldDropAsStale = typeof incomingSeq === 'number'
    && typeof lastSeq === 'number'
    && incomingSeq <= lastSeq;

  if (shouldDropAsStale) {
    return {
      shouldDropAsStale: true,
      nextSeq: lastSeq,
    };
  }

  return {
    shouldDropAsStale: false,
    nextSeq: typeof incomingSeq === 'number' ? incomingSeq : lastSeq,
  };
}
