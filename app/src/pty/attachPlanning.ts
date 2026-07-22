import type { PtyAttachPolicy } from './bridge';

export type AttachReplayPreference =
  | 'default'
  | 'prefer_raw_scrollback';

export interface AttachReplaySegment {
  cols: number;
  rows: number;
  data: string;
}

export interface AttachRequestContext {
  requestedCols: number;
  requestedRows: number;
  policy: PtyAttachPolicy;
  shell: boolean;
  agent: string | null;
  replayPreference: AttachReplayPreference;
}

export interface AttachGhosttySnapshot {
  cols: number;
  rows: number;
  vt_dump_b64: string;
  scrollback_truncated?: boolean;
}

export interface AttachReplayData {
  scrollback?: string;
  replay_segments?: AttachReplaySegment[];
  screen_snapshot?: string;
  screen_snapshot_fresh?: boolean;
  screen_cols?: number;
  screen_rows?: number;
  cols?: number;
  rows?: number;
  // Server-authoritative terminal snapshot (Phase 2, ATTN_ATTACH_SNAPSHOT).
  // When present it is THE restore payload: a raw VT byte stream (base64) that
  // reconstructs the daemon worker's full parsed grid + scrollback (primary and
  // alt screen) when written into a fresh Ghostty model. The daemon zeroes the
  // raw-replay fields above when it serves a snapshot, so this supersedes them.
  snapshot?: AttachGhosttySnapshot;
}

export type AttachResultData = AttachReplayData & {
  last_seq?: number;
  scrollback_truncated?: boolean;
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
  return agent === 'codex' || agent === 'shell' ? 'prefer_raw_scrollback' : 'default';
}

export function classifyAttachReplay(
  data: AttachReplayData,
  context?: AttachRequestContext,
) {
  const ghosttySnapshot = data.snapshot && data.snapshot.vt_dump_b64 ? data.snapshot : null;
  const hasGhosttySnapshot = ghosttySnapshot !== null;
  const availableScreenSnapshot = Boolean(data.screen_snapshot && data.screen_snapshot_fresh !== false);
  const availableRawScrollback = Boolean(data.scrollback || (data.replay_segments && data.replay_segments.length > 0));
  const replayPreference = context?.replayPreference ?? 'default';
  // A same-app remount creates a fresh Ghostty model and therefore must
  // rehydrate the visible state and available history. Codex fresh-spawn is
  // separate: its startup query bytes must pass through the parser so the
  // frontend can answer them before the TUI draws.
  const policyEligibleForCodexRawReplay = context?.policy === 'fresh_spawn';
  const codexRawReplayBootstrap = replayPreference === 'prefer_raw_scrollback'
    && policyEligibleForCodexRawReplay;
  // A Ghostty snapshot is written into the fresh model with suppressResponses:
  // the daemon worker already answered CPR/DA1/OSC during live parsing and
  // forwarded the query gap over the wire, so the frontend must not re-answer
  // queries embedded in the replayed dump.
  const respondToTerminalQueries = !hasGhosttySnapshot && codexRawReplayBootstrap;
  const prefersRawScrollback = replayPreference === 'prefer_raw_scrollback'
    && (context?.policy === 'relaunch_restore' || context?.policy === 'same_app_remount' || policyEligibleForCodexRawReplay);
  // A Ghostty snapshot supersedes the legacy screen-snapshot/raw-scrollback
  // payloads entirely; the daemon never sends both at once.
  const screenSnapshotSuppressed = !hasGhosttySnapshot
    && availableScreenSnapshot && availableRawScrollback && prefersRawScrollback;
  const hasScreenSnapshot = !hasGhosttySnapshot && availableScreenSnapshot && !screenSnapshotSuppressed;
  const hasReplayPayload = hasGhosttySnapshot || Boolean(
    (hasScreenSnapshot && data.screen_snapshot)
    || data.scrollback
    || (data.replay_segments && data.replay_segments.length > 0),
  );
  const replayKind: 'ghostty_snapshot' | 'screen_snapshot' | 'scrollback' | 'none' = hasGhosttySnapshot
    ? 'ghostty_snapshot'
    : hasScreenSnapshot
      ? 'screen_snapshot'
      : (data.scrollback || (data.replay_segments && data.replay_segments.length > 0))
        ? 'scrollback'
        : 'none';
  const attachedCols = typeof data.cols === 'number' ? data.cols : null;
  const attachedRows = typeof data.rows === 'number' ? data.rows : null;
  const replayCols = hasGhosttySnapshot
    ? ghosttySnapshot.cols
    : hasScreenSnapshot
      ? (typeof data.screen_cols === 'number' ? data.screen_cols : attachedCols)
      : attachedCols;
  const replayRows = hasGhosttySnapshot
    ? ghosttySnapshot.rows
    : hasScreenSnapshot
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
  // Restore payloads describe the daemon's current terminal grid. Reconstruct
  // the fresh frontend model at that grid first; the interactive client's
  // subsequent fit/pty_resize remains authoritative.
  const replayAtAttachedGeometry = context?.policy === 'relaunch_restore'
    || context?.policy === 'same_app_remount';
  // The daemon only serves a Ghostty snapshot when it decides the attach should
  // restore, and the snapshot carries its own authoritative geometry (we resize
  // to replayCols/replayRows before writing it), so it is always allowed and
  // never skipped on a geometry mismatch.
  const replayAllowedByPolicy = hasGhosttySnapshot
    || context?.policy === 'relaunch_restore'
    || context?.policy === 'same_app_remount'
    || codexRawReplayBootstrap;
  const geometryMismatchSkipsReplay = !replayAtAttachedGeometry
    && hasScreenSnapshot
    && (attachedGeometryMismatch || replayGeometryMismatch);
  const replaySkipped = !hasGhosttySnapshot && hasReplayPayload && (
    !replayAllowedByPolicy ||
    geometryMismatchSkipsReplay
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
    respondToTerminalQueries,
    replayApplied,
    replaySkipped,
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
  const hasScreenSnapshotReplay = replayPlan.replayApplied && replayPlan.hasScreenSnapshot;
  const hasGhosttySnapshotReplay = replayPlan.replayApplied && replayPlan.hasGhosttySnapshot;
  const hasRawScrollbackReplay = replayPlan.replayApplied && replayPlan.replayKind === 'scrollback';
  const replayCols = replayPlan.replayCols;
  const replayRows = replayPlan.replayRows;
  const ptyGeometryMatches = attachedCols === requestedCols && attachedRows === requestedRows;
  // A Ghostty snapshot carries its own authoritative grid (replayCols/Rows),
  // exactly like a screen snapshot.
  const replayGeometryMatches = hasScreenSnapshotReplay || hasGhosttySnapshotReplay
    ? replayCols === requestedCols && replayRows === requestedRows
    : hasRawScrollbackReplay
      ? ptyGeometryMatches
      : false;
  // requestedGeometryAuthoritative === false means the client size is
  // provisional (never measured against a visible container): it must not
  // claim PTY geometry authority. Forcing the live PTY to a construction
  // default SIGWINCH-churns the shell and bounces every attached model's
  // width, invalidating freshly replayed command blocks. The daemon's
  // geometry stays authoritative until a real fit produces an interactive
  // resize.
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
    hasScreenSnapshotReplay,
    hasGhosttySnapshotReplay,
    hasRawScrollbackReplay,
    replayKind: replayPlan.replayKind,
    replayApplied: replayPlan.replayApplied,
    availableScreenSnapshot: replayPlan.availableScreenSnapshot,
    availableRawScrollback: replayPlan.availableRawScrollback,
    screenSnapshotSuppressed: replayPlan.screenSnapshotSuppressed,
    replayPreference: replayPlan.replayPreference,
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
  sessionAgent,
}: {
  attachResult: AttachResultData;
  replayPlan: ReturnType<typeof classifyAttachReplay>;
  previousSeq?: number;
  queuedOutputs?: PendingAttachOutputChunk[];
  sessionAgent?: string | null;
}) {
  const shouldReset = replayPlan.replayAllowedByPolicy && (
    replayPlan.replayApplied || typeof previousSeq === 'number'
  );
  const resetReason = shouldReset
    ? ((replayPlan.hasScreenSnapshot || replayPlan.hasGhosttySnapshot) && replayPlan.replayApplied
        ? 'snapshot_restore'
        : 'reattach')
    : null;
  const replayAction = replayPlan.replaySkipped
    ? {
        kind: 'skipped' as const,
        replayKind: replayPlan.replayKind,
      }
    : replayPlan.replayApplied && replayPlan.hasGhosttySnapshot && attachResult.snapshot?.vt_dump_b64
      ? {
          kind: 'ghostty_snapshot' as const,
          replayKind: 'ghostty_snapshot' as const,
          data: attachResult.snapshot.vt_dump_b64,
        }
    : replayPlan.replayApplied && replayPlan.hasScreenSnapshot && attachResult.screen_snapshot
      ? {
          kind: 'screen_snapshot' as const,
          replayKind: 'screen_snapshot' as const,
          data: attachResult.screen_snapshot,
        }
      : replayPlan.replayApplied && attachResult.replay_segments && attachResult.replay_segments.length > 0
        ? {
            kind: 'scrollback_segments' as const,
            replayKind: 'scrollback' as const,
            segments: attachResult.replay_segments,
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

  // The daemon only returns segmented replay after verifying it reconstructs
  // the fresh worker screen. Truncation then means older history was omitted.
  const hasVerifiedSegmentedReplay = Boolean(
    attachResult.replay_segments && attachResult.replay_segments.length > 0,
  );
  const shouldWarnTruncatedRestore = Boolean(
    !replayPlan.hasScreenSnapshot &&
    !replayPlan.hasGhosttySnapshot &&
    attachResult.scrollback_truncated &&
    !hasVerifiedSegmentedReplay &&
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
