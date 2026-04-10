import type { PtyAttachPolicy } from './bridge';

export type AttachReplayPreference =
  | 'default'
  | 'prefer_raw_scrollback';

export interface AttachRequestContext {
  requestedCols: number;
  requestedRows: number;
  policy: PtyAttachPolicy;
  shell: boolean;
  agent: string | null;
  replayPreference: AttachReplayPreference;
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
  screen_snapshot_fresh?: boolean;
  scrollback?: string;
  screen_cols?: number;
  screen_rows?: number;
}

export interface AttachRuntimeRequest {
  cols: number;
  rows: number;
  shell?: boolean;
  agent?: string | null;
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
  args: Pick<AttachRuntimeRequest, 'cols' | 'rows' | 'shell' | 'agent'>,
  policy: PtyAttachPolicy,
): AttachRequestContext {
  const normalizedAgent = normalizeAttachAgent(args.agent, args.shell);
  return {
    requestedCols: args.cols,
    requestedRows: args.rows,
    policy,
    shell: normalizedAgent === 'shell',
    agent: normalizedAgent,
    replayPreference: deriveAttachReplayPreference(normalizedAgent),
  };
}

function normalizeAttachAgent(agent?: string | null, shell?: boolean): string | null {
  if (shell) {
    return 'shell';
  }
  if (typeof agent !== 'string') {
    return null;
  }
  const normalized = agent.trim().toLowerCase();
  return normalized.length > 0 ? normalized : null;
}

function deriveAttachReplayPreference(agent: string | null): AttachReplayPreference {
  return agent === 'codex' ? 'prefer_raw_scrollback' : 'default';
}

export function classifyAttachReplay(
  data: AttachReplayData,
  context?: AttachRequestContext,
) {
  const availableScreenSnapshot = Boolean(data.screen_snapshot && data.screen_snapshot_fresh !== false);
  const availableRawScrollback = Boolean(data.scrollback);
  const replayPreference = context?.replayPreference ?? 'default';
  const prefersRawScrollback = replayPreference === 'prefer_raw_scrollback'
    && context?.policy === 'relaunch_restore';
  const screenSnapshotSuppressed = availableScreenSnapshot && availableRawScrollback && prefersRawScrollback;
  const hasScreenSnapshot = availableScreenSnapshot && !screenSnapshotSuppressed;
  const hasReplayPayload = Boolean((hasScreenSnapshot && data.screen_snapshot) || data.scrollback);
  const replayKind = hasScreenSnapshot
    ? 'screen_snapshot'
    : data.scrollback
      ? 'scrollback'
      : 'none';
  const attachedCols = typeof data.cols === 'number' ? data.cols : null;
  const attachedRows = typeof data.rows === 'number' ? data.rows : null;
  const replayCols = hasScreenSnapshot
    ? (typeof data.screen_cols === 'number' ? data.screen_cols : attachedCols)
    : attachedCols;
  const replayRows = hasScreenSnapshot
    ? (typeof data.screen_rows === 'number' ? data.screen_rows : attachedRows)
    : attachedRows;
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
    shell: context?.shell ?? false,
    agent: context?.agent ?? null,
    replayPreference,
    prefersRawScrollback,
    availableScreenSnapshot,
    availableRawScrollback,
    screenSnapshotSuppressed,
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
  options: {
    attachPolicy: PtyAttachPolicy;
    attachContext?: AttachRequestContext;
  },
) {
  const replayPlan = classifyAttachReplay(attachResult, options.attachContext);
  const requestedCols = args.cols;
  const requestedRows = args.rows;
  const attachedCols = typeof attachResult.cols === 'number' ? attachResult.cols : null;
  const attachedRows = typeof attachResult.rows === 'number' ? attachResult.rows : null;
  const hasScreenSnapshotReplay = replayPlan.replayApplied && replayPlan.hasScreenSnapshot;
  const hasRawScrollbackReplay = replayPlan.replayApplied && replayPlan.replayKind === 'scrollback';
  const replayCols = replayPlan.replayCols;
  const replayRows = replayPlan.replayRows;
  const ptyGeometryMatches = attachedCols === requestedCols && attachedRows === requestedRows;
  const replayGeometryMatches = hasScreenSnapshotReplay
    ? replayCols === requestedCols && replayRows === requestedRows
    : hasRawScrollbackReplay
      ? ptyGeometryMatches
      : false;
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
    replayKind: replayPlan.replayKind,
    replayApplied: replayPlan.replayApplied,
    availableScreenSnapshot: replayPlan.availableScreenSnapshot,
    availableRawScrollback: replayPlan.availableRawScrollback,
    screenSnapshotSuppressed: replayPlan.screenSnapshotSuppressed,
    replayPreference: replayPlan.replayPreference,
    agent: replayPlan.agent,
    resizeRequired,
    strategy: resizeRequired ? 'resize' : 'none',
    attachPolicy: options.attachPolicy,
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
