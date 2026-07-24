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

export interface AttachRestoreData {
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

export type AttachResultData = AttachRestoreData & {
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

// classifyAttachRestore resolves an attach result to the single restore
// decision that remains: does the daemon hand us a Ghostty snapshot to
// reconstruct from, and at what grid? There is exactly one restore payload
// (the VT dump) or none — the raw-replay-vs-snapshot decision tree is gone.
export function classifyAttachRestore(
  data: AttachRestoreData,
  context?: AttachRequestContext,
) {
  const ghosttySnapshot = data.snapshot && data.snapshot.vt_dump_b64 ? data.snapshot : null;
  const hasSnapshot = ghosttySnapshot !== null;
  const attachedCols = typeof data.cols === 'number' ? data.cols : null;
  const attachedRows = typeof data.rows === 'number' ? data.rows : null;
  // A Ghostty snapshot carries its own authoritative grid (we resize the fresh
  // model to it before writing the dump). With no snapshot, geometry falls back
  // to the daemon's reported PTY size.
  const restoreCols = hasSnapshot ? ghosttySnapshot.cols : attachedCols;
  const restoreRows = hasSnapshot ? ghosttySnapshot.rows : attachedRows;

  return {
    agent: context?.agent ?? null,
    hasSnapshot,
    restoreCols,
    restoreRows,
  };
}

export function planAttachedRuntimeGeometry(
  args: AttachRuntimeRequest,
  attachResult: AttachRestoreData,
  options: {
    attachPolicy: PtyAttachPolicy;
    attachContext?: AttachRequestContext;
    requestedGeometryAuthoritative?: boolean;
  },
) {
  const restorePlan = classifyAttachRestore(attachResult, options.attachContext);
  const requestedCols = args.cols;
  const requestedRows = args.rows;
  const attachedCols = typeof attachResult.cols === 'number' ? attachResult.cols : null;
  const attachedRows = typeof attachResult.rows === 'number' ? attachResult.rows : null;
  const restoreCols = restorePlan.restoreCols;
  const restoreRows = restorePlan.restoreRows;
  const ptyGeometryMatches = attachedCols === requestedCols && attachedRows === requestedRows;
  // A Ghostty snapshot carries its own authoritative grid (restoreCols/Rows).
  const restoreGeometryMatches = restorePlan.hasSnapshot
    ? restoreCols === requestedCols && restoreRows === requestedRows
    : false;
  // requestedGeometryAuthoritative === false means the client size is
  // provisional (never measured against a visible container): it must not
  // claim PTY geometry authority. Forcing the live PTY to a construction
  // default SIGWINCH-churns the shell and bounces every attached model's
  // width, invalidating a freshly restored grid. The daemon's geometry stays
  // authoritative until a real fit produces an interactive resize.
  const preserveAttachedGeometry = options.attachPolicy === 'relaunch_restore'
    || options.requestedGeometryAuthoritative === false;
  const resizeRequired = !preserveAttachedGeometry && !ptyGeometryMatches;

  return {
    requestedCols,
    requestedRows,
    attachedCols,
    attachedRows,
    restoreCols,
    restoreRows,
    ptyGeometryMatches,
    restoreGeometryMatches,
    hasSnapshot: restorePlan.hasSnapshot,
    agent: restorePlan.agent,
    resizeRequired,
    strategy: resizeRequired ? 'resize' : preserveAttachedGeometry && !ptyGeometryMatches ? 'preserve_attached' : 'none',
    attachPolicy: options.attachPolicy,
  };
}

export function planAttachResultEffects({
  attachResult,
  restorePlan,
  previousSeq,
  queuedOutputs,
}: {
  attachResult: AttachResultData;
  restorePlan: ReturnType<typeof classifyAttachRestore>;
  previousSeq?: number;
  queuedOutputs?: PendingAttachOutputChunk[];
}) {
  // Reset is only safe when a snapshot redraws the whole grid. Without one the
  // server has no serialized state to hand us, so the client's existing model is
  // the ONLY rendered terminal: resetting it (e.g. a same_app_remount after a
  // ghostty construction failure) clears the screen with nothing to repaint it,
  // leaving an idle shell blank until it next prints. Per AGENTS.md a
  // snapshot-less attach keeps whatever the client already has.
  const shouldReset = restorePlan.hasSnapshot;
  const resetReason = shouldReset ? 'snapshot_restore' : null;
  const restoreAction = restorePlan.hasSnapshot && attachResult.snapshot?.vt_dump_b64
    ? {
        kind: 'ghostty_snapshot' as const,
        data: attachResult.snapshot.vt_dump_b64,
      }
    : {
        kind: 'none' as const,
      };

  // The dedup baseline is the highest seq the client has already rendered.
  // With a snapshot the dump covers everything through the server's last_seq
  // (see Session.info in internal/pty/session.go), so that is the baseline and a
  // queued chunk with seq <= last_seq is already inside the dump. Without a
  // snapshot nothing was repainted for the client, so the baseline is the
  // client's OWN watermark (previousSeq): advancing to the server's last_seq
  // would silently drop queued chunks between previousSeq and last_seq that the
  // client never rendered. Live chunks resume just past the baseline, which
  // planLivePtyOutput's `incomingSeq <= lastSeq` stale rule lets through.
  let nextSeq = restorePlan.hasSnapshot
    ? (typeof attachResult.last_seq === 'number' ? attachResult.last_seq : 0)
    : (typeof previousSeq === 'number' ? previousSeq : 0);
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
    restoreAction,
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
