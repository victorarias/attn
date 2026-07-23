import type { PtyAttachPolicy } from './bridge';

export interface AttachRequestContext {
  requestedCols: number;
  requestedRows: number;
  policy: PtyAttachPolicy;
  shell: boolean;
  agent: string | null;
}

export interface AttachGhosttySnapshot {
  cols: number;
  rows: number;
  vt_dump_b64: string;
  scrollback_truncated?: boolean;
}

export interface AttachReplayData {
  cols?: number;
  rows?: number;
  // Server-authoritative terminal snapshot: the sole restore payload. A raw VT
  // byte stream (base64) that reconstructs the daemon worker's full parsed grid
  // + scrollback (primary and alt screen) when written into a fresh Ghostty
  // model. Absent when the policy omits restore or the worker has no
  // serialization to offer (ghostty construction failed, or a non-macOS build's
  // pure-Go stub); the client then keeps whatever it has and dedups the live
  // stream against last_seq.
  snapshot?: AttachGhosttySnapshot;
}

export type AttachResultData = AttachReplayData & {
  last_seq?: number;
};

export interface AttachRuntimeRequest {
  cols: number;
  rows: number;
  shell?: boolean;
  agent?: string | null;
}

export interface PendingAttachOutputChunk {
  /** Base64 (JSON pty_output event) or raw bytes (binary frame). */
  data: string | Uint8Array;
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

export function classifyAttachReplay(
  data: AttachReplayData,
  context?: AttachRequestContext,
) {
  const ghosttySnapshot = data.snapshot && data.snapshot.vt_dump_b64 ? data.snapshot : null;
  const hasGhosttySnapshot = ghosttySnapshot !== null;
  const hasReplayPayload = hasGhosttySnapshot;
  const replayKind: 'ghostty_snapshot' | 'none' = hasGhosttySnapshot ? 'ghostty_snapshot' : 'none';
  const attachedCols = typeof data.cols === 'number' ? data.cols : null;
  const attachedRows = typeof data.rows === 'number' ? data.rows : null;
  // A Ghostty snapshot carries its own authoritative grid (we resize the fresh
  // model to it before writing the dump). With no snapshot, geometry falls back
  // to the daemon's reported PTY size.
  const replayCols = hasGhosttySnapshot ? ghosttySnapshot.cols : attachedCols;
  const replayRows = hasGhosttySnapshot ? ghosttySnapshot.rows : attachedRows;
  const requestedCols = context?.requestedCols ?? null;
  const requestedRows = context?.requestedRows ?? null;
  const attachedGeometryMismatch = requestedCols !== null && requestedRows !== null && (
    attachedCols !== requestedCols || attachedRows !== requestedRows
  );
  const replayGeometryMismatch = requestedCols !== null && requestedRows !== null && hasReplayPayload && (
    replayCols !== requestedCols || replayRows !== requestedRows
  );
  // The daemon only serves a Ghostty snapshot when it decides the attach should
  // restore, and the snapshot carries its own authoritative geometry, so it is
  // always allowed and never skipped on a geometry mismatch. A snapshot-less
  // reattach still resets to dedup against last_seq under restore policies.
  const replayAllowedByPolicy = hasGhosttySnapshot
    || context?.policy === 'relaunch_restore'
    || context?.policy === 'same_app_remount';
  const replayApplied = hasReplayPayload;

  return {
    shell: context?.shell ?? false,
    agent: context?.agent ?? null,
    hasGhosttySnapshot,
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
    replaySkipped: false,
  };
}

export function planAttachedRuntimeGeometry(
  args: AttachRuntimeRequest,
  attachResult: AttachReplayData,
  options: {
    attachPolicy: PtyAttachPolicy;
    attachContext?: AttachRequestContext;
    requestedGeometryAuthoritative?: boolean;
  },
) {
  const replayPlan = classifyAttachReplay(attachResult, options.attachContext);
  const requestedCols = args.cols;
  const requestedRows = args.rows;
  const attachedCols = typeof attachResult.cols === 'number' ? attachResult.cols : null;
  const attachedRows = typeof attachResult.rows === 'number' ? attachResult.rows : null;
  const hasGhosttySnapshotReplay = replayPlan.replayApplied && replayPlan.hasGhosttySnapshot;
  const replayCols = replayPlan.replayCols;
  const replayRows = replayPlan.replayRows;
  const ptyGeometryMatches = attachedCols === requestedCols && attachedRows === requestedRows;
  // A Ghostty snapshot carries its own authoritative grid (replayCols/Rows).
  const replayGeometryMatches = hasGhosttySnapshotReplay
    ? replayCols === requestedCols && replayRows === requestedRows
    : false;
  // requestedGeometryAuthoritative === false means the client size is
  // provisional (never measured against a visible container): it must not
  // claim PTY geometry authority. Forcing the live PTY to a construction
  // default SIGWINCH-churns the shell and bounces every attached model's
  // width, invalidating a freshly replayed grid. The daemon's geometry stays
  // authoritative until a real fit produces an interactive resize.
  const preserveAttachedGeometry = options.attachPolicy === 'relaunch_restore'
    || options.requestedGeometryAuthoritative === false;
  const resizeRequired = !preserveAttachedGeometry && !ptyGeometryMatches;

  return {
    requestedCols,
    requestedRows,
    attachedCols,
    attachedRows,
    replayCols,
    replayRows,
    ptyGeometryMatches,
    replayGeometryMatches,
    hasGhosttySnapshotReplay,
    replayKind: replayPlan.replayKind,
    replayApplied: replayPlan.replayApplied,
    agent: replayPlan.agent,
    resizeRequired,
    strategy: resizeRequired ? 'resize' : preserveAttachedGeometry && !ptyGeometryMatches ? 'preserve_attached' : 'none',
    attachPolicy: options.attachPolicy,
  };
}

export function planAttachResultEffects({
  attachResult,
  replayPlan,
  previousSeq,
  queuedOutputs,
}: {
  attachResult: AttachResultData;
  replayPlan: ReturnType<typeof classifyAttachReplay>;
  previousSeq?: number;
  queuedOutputs?: PendingAttachOutputChunk[];
}) {
  const shouldReset = replayPlan.replayAllowedByPolicy && (
    replayPlan.replayApplied || typeof previousSeq === 'number'
  );
  const resetReason = shouldReset
    ? (replayPlan.hasGhosttySnapshot && replayPlan.replayApplied ? 'snapshot_restore' : 'reattach')
    : null;
  const replayAction = replayPlan.replayApplied && replayPlan.hasGhosttySnapshot && attachResult.snapshot?.vt_dump_b64
    ? {
        kind: 'ghostty_snapshot' as const,
        replayKind: 'ghostty_snapshot' as const,
        data: attachResult.snapshot.vt_dump_b64,
      }
    : {
        kind: 'none' as const,
        replayKind: replayPlan.replayKind,
      };

  // last_seq names the last chunk covered by the replay payload (see
  // Session.info in internal/pty/session.go). A queued chunk with
  // seq <= last_seq is already inside the replay; emitting it again would
  // double-apply those bytes. Live chunks resume at last_seq + 1, which
  // planLivePtyOutput's `incomingSeq <= lastSeq` stale rule lets through.
  let nextSeq = typeof attachResult.last_seq === 'number' ? attachResult.last_seq : 0;
  const queuedOutputsToEmit: PendingAttachOutputChunk[] = [];
  for (const chunk of queuedOutputs || []) {
    if (typeof chunk.seq === 'number' && chunk.seq <= nextSeq) {
      continue;
    }
    if (typeof chunk.seq === 'number') {
      nextSeq = chunk.seq;
    }
    queuedOutputsToEmit.push(chunk);
  }

  return {
    shouldReset,
    resetReason,
    replayAction,
    nextSeq,
    queuedOutputsToEmit,
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
