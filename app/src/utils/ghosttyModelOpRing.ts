// Capture-on-fault ring for the Ghostty WASM terminal model: the raw inputs fed
// to a pane's model, dumped into the `model_fault` diagnostics record so a trap
// ships with its own repro (app/scripts/replay-ghostty-model-fault.mjs).
//
// Contract: one ring per pane per model (`beginEpoch` drops the previous
// model's ops); ops are recorded BEFORE they reach the model; every retained
// write is a COPY, since the bytes are views into buffers the app does not own
// and are written later on an async chain; a restore is the replay's base state,
// so it gets its own budget and restarts the epoch. Nothing re-encodes until a
// fault.
//
// Caps (tripwires), per pane:
//   - 512 KB of retained write bytes. Measured: live PTY chunks run 1–16 KB.
//   - 2048 ops. Measured: a divider drag emits ~1 resize per frame, ~30s of it.
//   - 512 KB of snapshot dump. Measured over 106 real attach snapshots
//     (`ghostty_snapshot_bytes=`): p50 11.2 KB, p90 12.5 KB, max 16.0 KB — 32x
//     the largest observed, so `snapshotTruncated` marks an abnormal capture.
//   - 2 MB of encoded capture per diagnostics record, past the largest the caps
//     above can produce (≈1.37 MB base64); the diagnostics log has no per-record
//     limit of its own.

export type ModelOp =
  | { t: number; kind: 'write'; bytes: Uint8Array }
  | { t: number; kind: 'resize'; cols: number; rows: number; noReflow: boolean }
  // A marker only: the reset's effect is the RIS write recorded right after it,
  // so a replay applies nothing for a `reset`.
  | { t: number; kind: 'reset' };

export const MODEL_OP_RING_MAX_BYTES = 512 * 1024;
export const MODEL_OP_RING_MAX_OPS = 2048;
export const MODEL_OP_RING_MAX_SNAPSHOT_BYTES = 512 * 1024;
export const MODEL_FAULT_CAPTURE_MAX_BYTES = 2 * 1024 * 1024;

export const MODEL_FAULT_CAPTURE_VERSION = 1;

export type EncodedModelOp =
  | { t: number; kind: 'write'; b64: string; len: number }
  | { t: number; kind: 'resize'; cols: number; rows: number; noReflow: boolean }
  | { t: number; kind: 'reset' };

export interface EncodedModelSnapshot {
  cols: number;
  rows: number;
  /** Base64 of the VT dump the model was restored from. */
  b64: string;
  /** Bytes retained (== the dump's length unless `truncated`). */
  len: number;
  /** Bytes the daemon sent that did not fit the cap. */
  dropped: number;
  /** A PREFIX of what the model received, so a replay diverges and is refused. */
  truncated: boolean;
}

export interface ModelFaultCapture {
  version: number;
  epochStartedAt: number;
  /** Geometry at the start of the RETAINED history, not of the model. */
  startCols: number | null;
  startRows: number | null;
  snapshot: EncodedModelSnapshot | null;
  /** Mirrors `snapshot.truncated` so a refusal check needs no nesting. */
  snapshotTruncated: boolean;
  ops: EncodedModelOp[];
  opCount: number;
  retainedWriteBytes: number;
  /** Evicted by the ring's caps since the epoch began. */
  droppedOps: number;
  droppedWriteBytes: number;
  /** Evicted at encode time to fit MODEL_FAULT_CAPTURE_MAX_BYTES. */
  droppedForRecordBudget: number;
  /** Size of this capture once serialized, within ~1%. */
  encodedBytesEstimate: number;
}

export interface GhosttyModelOpRing {
  /** Start (or restart) the ring for a freshly constructed model. */
  beginEpoch(cols: number, rows: number): void;
  /** Raw bytes about to be written to the model, pre-wrapper. */
  noteWrite(bytes: Uint8Array): void;
  /** A chunk of an attach-restore VT dump; the first clears the ring. */
  noteRestoreChunk(bytes: Uint8Array, cols: number, rows: number): void;
  noteResize(cols: number, rows: number, noReflow: boolean): void;
  /** Marker for a model reset; see ModelOp's `reset` variant. */
  noteReset(): void;
  clear(): void;
  /** Retained ops, oldest first. */
  ops(): ModelOp[];
  stats(): {
    opCount: number;
    retainedWriteBytes: number;
    droppedOps: number;
    droppedWriteBytes: number;
    snapshotBytes: number;
    snapshotTruncated: boolean;
  };
  /** Encode for a diagnostics record. Only called on the fault path. */
  capture(): ModelFaultCapture;
}

interface SnapshotState {
  cols: number;
  rows: number;
  chunks: Uint8Array[];
  bytes: number;
  dropped: number;
  truncated: boolean;
}

// Per-op JSON overhead for sizing a capture; rounded up so it never undershoots.
const WRITE_OP_OVERHEAD_BYTES = 56;
const RESIZE_OP_OVERHEAD_BYTES = 84;
const RESET_OP_OVERHEAD_BYTES = 32;
const CAPTURE_ENVELOPE_BYTES = 512;

function base64Length(byteLength: number): number {
  return Math.ceil(byteLength / 3) * 4;
}

// Spreading a 512 KB string into btoa blows the argument limit; chunk instead.
const BASE64_CHUNK = 8192;

export function bytesToBase64(bytes: Uint8Array): string {
  let binary = '';
  for (let offset = 0; offset < bytes.length; offset += BASE64_CHUNK) {
    const chunk = bytes.subarray(offset, offset + BASE64_CHUNK);
    binary += String.fromCharCode(...chunk);
  }
  return btoa(binary);
}

export function base64ToBytes(value: string): Uint8Array {
  const binary = atob(value);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i += 1) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes;
}

export function createGhosttyModelOpRing(options?: { now?: () => number }): GhosttyModelOpRing {
  const now = options?.now ?? (() => Date.now());
  const slots: Array<ModelOp | undefined> = new Array(MODEL_OP_RING_MAX_OPS);
  let head = 0;
  let count = 0;
  let retainedWriteBytes = 0;
  let droppedOps = 0;
  let droppedWriteBytes = 0;
  let startCols: number | null = null;
  let startRows: number | null = null;
  let epochStartedAt = now();
  let snapshot: SnapshotState | null = null;
  // True while consecutive restore chunks arrive; any other op ends it.
  let restoreInProgress = false;

  const evictOldest = () => {
    const op = slots[head];
    slots[head] = undefined;
    head = (head + 1) % MODEL_OP_RING_MAX_OPS;
    count -= 1;
    droppedOps += 1;
    if (op?.kind === 'write') {
      retainedWriteBytes -= op.bytes.length;
      droppedWriteBytes += op.bytes.length;
    }
    // A dropped resize is the geometry the survivors ran at; carry it forward.
    if (op?.kind === 'resize') {
      startCols = op.cols;
      startRows = op.rows;
    }
  };

  const push = (op: ModelOp) => {
    if (count === MODEL_OP_RING_MAX_OPS) {
      evictOldest();
    }
    slots[(head + count) % MODEL_OP_RING_MAX_OPS] = op;
    count += 1;
    if (op.kind === 'write') {
      retainedWriteBytes += op.bytes.length;
    }
    // An op is atomic, so a write larger than the budget is kept: the ring can
    // exceed the cap by one op, and `droppedWriteBytes` says what it cost.
    while (retainedWriteBytes > MODEL_OP_RING_MAX_BYTES && count > 1) {
      evictOldest();
    }
  };

  const reset = (cols: number | null, rows: number | null) => {
    slots.fill(undefined);
    head = 0;
    count = 0;
    retainedWriteBytes = 0;
    droppedOps = 0;
    droppedWriteBytes = 0;
    startCols = cols;
    startRows = rows;
    snapshot = null;
    restoreInProgress = false;
    epochStartedAt = now();
  };

  return {
    beginEpoch(cols, rows) {
      reset(cols, rows);
    },

    clear() {
      reset(null, null);
    },

    noteWrite(bytes) {
      restoreInProgress = false;
      if (bytes.length === 0) return;
      push({ t: now(), kind: 'write', bytes: bytes.slice() });
    },

    noteRestoreChunk(bytes, cols, rows) {
      if (!restoreInProgress) {
        reset(cols, rows);
        restoreInProgress = true;
        snapshot = { cols, rows, chunks: [], bytes: 0, dropped: 0, truncated: false };
      }
      const state = snapshot;
      if (!state) return;
      const room = MODEL_OP_RING_MAX_SNAPSHOT_BYTES - state.bytes;
      if (room <= 0) {
        state.dropped += bytes.length;
        state.truncated = true;
        return;
      }
      if (bytes.length > room) {
        state.chunks.push(bytes.slice(0, room));
        state.bytes += room;
        state.dropped += bytes.length - room;
        state.truncated = true;
        return;
      }
      state.chunks.push(bytes.slice());
      state.bytes += bytes.length;
    },

    noteResize(cols, rows, noReflow) {
      restoreInProgress = false;
      push({ t: now(), kind: 'resize', cols, rows, noReflow });
    },

    noteReset() {
      restoreInProgress = false;
      push({ t: now(), kind: 'reset' });
    },

    ops() {
      const out: ModelOp[] = [];
      for (let i = 0; i < count; i += 1) {
        const op = slots[(head + i) % MODEL_OP_RING_MAX_OPS];
        if (op) out.push(op);
      }
      return out;
    },

    stats() {
      return {
        opCount: count,
        retainedWriteBytes,
        droppedOps,
        droppedWriteBytes,
        snapshotBytes: snapshot?.bytes ?? 0,
        snapshotTruncated: snapshot?.truncated ?? false,
      };
    },

    capture() {
      const encodedSnapshot: EncodedModelSnapshot | null = snapshot
        ? {
            cols: snapshot.cols,
            rows: snapshot.rows,
            b64: bytesToBase64(concat(snapshot.chunks, snapshot.bytes)),
            len: snapshot.bytes,
            dropped: snapshot.dropped,
            truncated: snapshot.truncated,
          }
        : null;

      const encoded: EncodedModelOp[] = [];
      let budget = CAPTURE_ENVELOPE_BYTES + (encodedSnapshot ? encodedSnapshot.b64.length : 0);
      for (let i = 0; i < count; i += 1) {
        const op = slots[(head + i) % MODEL_OP_RING_MAX_OPS];
        if (!op) continue;
        if (op.kind === 'write') {
          encoded.push({ t: op.t, kind: 'write', b64: bytesToBase64(op.bytes), len: op.bytes.length });
          budget += base64Length(op.bytes.length) + WRITE_OP_OVERHEAD_BYTES;
        } else if (op.kind === 'resize') {
          encoded.push({ t: op.t, kind: 'resize', cols: op.cols, rows: op.rows, noReflow: op.noReflow });
          budget += RESIZE_OP_OVERHEAD_BYTES;
        } else {
          encoded.push({ t: op.t, kind: 'reset' });
          budget += RESET_OP_OVERHEAD_BYTES;
        }
      }

      // Deterministic fit: drop oldest until it fits, reporting the count.
      let droppedForRecordBudget = 0;
      let captureStartCols = startCols;
      let captureStartRows = startRows;
      while (budget > MODEL_FAULT_CAPTURE_MAX_BYTES && encoded.length > 0) {
        const op = encoded.shift() as EncodedModelOp;
        droppedForRecordBudget += 1;
        if (op.kind === 'write') {
          budget -= base64Length(op.len) + WRITE_OP_OVERHEAD_BYTES;
        } else if (op.kind === 'resize') {
          budget -= RESIZE_OP_OVERHEAD_BYTES;
          captureStartCols = op.cols;
          captureStartRows = op.rows;
        } else {
          budget -= RESET_OP_OVERHEAD_BYTES;
        }
      }

      return {
        version: MODEL_FAULT_CAPTURE_VERSION,
        epochStartedAt,
        startCols: captureStartCols,
        startRows: captureStartRows,
        snapshot: encodedSnapshot,
        snapshotTruncated: encodedSnapshot?.truncated ?? false,
        ops: encoded,
        opCount: encoded.length,
        retainedWriteBytes,
        droppedOps,
        droppedWriteBytes,
        droppedForRecordBudget,
        encodedBytesEstimate: budget,
      };
    },
  };
}

function concat(chunks: Uint8Array[], total: number): Uint8Array {
  if (chunks.length === 1) return chunks[0];
  const out = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    out.set(chunk, offset);
    offset += chunk.length;
  }
  return out;
}

// Decode a capture back into replayable ops.
export function decodeModelFaultCapture(capture: ModelFaultCapture): {
  cols: number | null;
  rows: number | null;
  snapshot: Uint8Array | null;
  ops: ModelOp[];
} {
  const ops: ModelOp[] = capture.ops.map((op) => {
    if (op.kind === 'write') {
      return { t: op.t, kind: 'write', bytes: base64ToBytes(op.b64) };
    }
    if (op.kind === 'resize') {
      return { t: op.t, kind: 'resize', cols: op.cols, rows: op.rows, noReflow: op.noReflow };
    }
    return { t: op.t, kind: 'reset' };
  });
  return {
    cols: capture.snapshot ? capture.snapshot.cols : capture.startCols,
    rows: capture.snapshot ? capture.snapshot.rows : capture.startRows,
    snapshot: capture.snapshot ? base64ToBytes(capture.snapshot.b64) : null,
    ops,
  };
}
