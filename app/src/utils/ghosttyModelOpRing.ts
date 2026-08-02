// Capture-on-fault ring for the Ghostty WASM terminal model.
//
// The vendored ghostty-vt core occasionally traps (wasm `unreachable` on write,
// out-of-bounds on render). Recovery works — the pane rebuilds from the daemon's
// server-authoritative snapshot — but the replacement model knows nothing about
// the inputs that corrupted its predecessor, so every production trap so far has
// arrived without a repro. This module is the missing evidence: each pane keeps a
// bounded, in-memory ring of the raw inputs fed to its model, and the fault path
// dumps it into the `model_fault` diagnostics record. A fault then ships with its
// own replayable repro (see app/scripts/replay-ghostty-model-fault.mjs).
//
// Contract:
//   - One ring per pane, per model. `beginEpoch` is called at model construction
//     and drops everything the previous model saw.
//   - Ops are recorded BEFORE they reach the model, so the op that traps is in
//     the ring.
//   - Every retained write is a COPY. The bytes handed to the write chain are
//     views into buffers the app does not own (a WebSocket frame's ArrayBuffer;
//     one decoded attach-replay buffer shared by all of its 16KB chunks) and the
//     model write happens later on an async chain — retaining a view would both
//     pin those buffers for the ring's lifetime and record whatever the producer
//     later put there.
//   - A restore (attach snapshot) is the base state a replay starts from, so it
//     is kept in its own budget rather than competing with live writes, and it
//     restarts the epoch: everything before a restore describes a screen the
//     dump has just overwritten.
//
// Caps (tripwires, see receipts below), per pane:
//   - 512 KB of retained write bytes. Live PTY chunks run 1–16 KB, so this holds
//     hundreds of writes — far more than the "burst of resizes then one write"
//     window every recorded trap fired in.
//   - 2048 ops. A divider drag emits ~1 resize per frame; 2048 ops covers ~30s
//     of continuous dragging.
//   - 512 KB of snapshot dump. Measured against 106 real attach snapshots in a
//     production daemon log (`ghostty_snapshot_bytes=`): p50 11.2 KB, p90 12.5
//     KB, max 16.0 KB — the cap is 32x the largest observed dump, so
//     `snapshotTruncated` marks a genuinely abnormal capture, never routine
//     operation.
//   - 2 MB of encoded capture per diagnostics record. The diagnostics log
//     enforces no per-record limit (only an 8 MB per-file cap with
//     truncate-and-restart rotation), so an unbounded record could blow away the
//     lifecycle stream around the fault it is documenting. 2 MB is past the
//     largest capture the caps above can produce (512 KB writes + 512 KB dump ≈
//     1 MB raw ≈ 1.37 MB base64), so a healthy capture never feels it.
//
// Nothing here re-encodes until a fault: writes cost one copy and one array
// slot.

export type ModelOp =
  | { t: number; kind: 'write'; bytes: Uint8Array }
  | { t: number; kind: 'resize'; cols: number; rows: number; noReflow: boolean }
  // A marker only. attn's model reset writes RIS (ESC c) through the same write
  // chain, so the reset's actual effect on the model is the write op recorded
  // immediately after this one; a replay applies nothing for a `reset`.
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
  /**
   * The retained dump is a PREFIX of what the model actually received. A replay
   * from it diverges; the replay tool refuses such a record outright.
   */
  truncated: boolean;
}

export interface ModelFaultCapture {
  version: number;
  /** When the current model's ring started (ms epoch). */
  epochStartedAt: number;
  /** Model geometry at the start of the RETAINED history, not of the model. */
  startCols: number | null;
  startRows: number | null;
  snapshot: EncodedModelSnapshot | null;
  /** Mirrors `snapshot.truncated` so a refusal check needs no nesting. */
  snapshotTruncated: boolean;
  ops: EncodedModelOp[];
  opCount: number;
  retainedWriteBytes: number;
  /** Ops evicted by the ring's caps since the epoch began. */
  droppedOps: number;
  droppedWriteBytes: number;
  /** Ops evicted at encode time to fit MODEL_FAULT_CAPTURE_MAX_BYTES. */
  droppedForRecordBudget: number;
  /** Size of this capture once serialized, within ~1%. */
  encodedBytesEstimate: number;
}

export interface GhosttyModelOpRing {
  /** Start (or restart) the ring for a freshly constructed model. */
  beginEpoch(cols: number, rows: number): void;
  /** Raw bytes about to be written to the model, pre-wrapper. */
  noteWrite(bytes: Uint8Array): void;
  /**
   * A chunk of an attach-restore VT dump, plus the model geometry it is being
   * written at. The first chunk of a restore clears the ring: the dump is the
   * new base state, and the ops before it describe a screen it overwrites.
   */
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

// Per-op JSON overhead used to size a capture without materializing it. Keys +
// punctuation + a 13-digit timestamp; measured against the emitted shapes and
// rounded up, so the estimate never undershoots.
const WRITE_OP_OVERHEAD_BYTES = 56;
const RESIZE_OP_OVERHEAD_BYTES = 84;
const RESET_OP_OVERHEAD_BYTES = 32;
const CAPTURE_ENVELOPE_BYTES = 512;

function base64Length(byteLength: number): number {
  return Math.ceil(byteLength / 3) * 4;
}

// btoa over a 512 KB string built by spreading would blow the argument limit,
// so encode in chunks. Only ever runs on the fault path.
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
  // True while consecutive restore chunks are arriving; any other op ends it, so
  // a later restore starts a fresh base state instead of appending to a stale one.
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
    // The retained window now starts after this op. A dropped resize is the
    // geometry the survivors ran at, so carrying it forward keeps the replay's
    // starting grid exact instead of stale.
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
    // A single write larger than the whole budget is kept rather than dropped:
    // an op is atomic, and half a byte stream replays as garbage. The ring can
    // therefore exceed the cap by at most one op, and `droppedWriteBytes` says
    // what that cost.
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

      // Deterministic fit: drop oldest until the record fits its budget. The
      // count is reported, never silent.
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

// Decode a capture back into replayable ops. Shared by the replay round-trip
// test and anything else that reads a `model_fault` record.
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
