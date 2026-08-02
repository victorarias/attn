import { describe, expect, it } from 'vitest';
import {
  base64ToBytes,
  createGhosttyModelOpRing,
  MODEL_OP_RING_MAX_BYTES,
  MODEL_OP_RING_MAX_OPS,
  MODEL_OP_RING_MAX_SNAPSHOT_BYTES,
  type ModelOp,
} from './ghosttyModelOpRing';

function bytes(length: number, fill: number): Uint8Array {
  return new Uint8Array(length).fill(fill);
}

function writeOps(ops: ModelOp[]): Array<{ first: number; len: number }> {
  return ops
    .filter((op): op is Extract<ModelOp, { kind: 'write' }> => op.kind === 'write')
    .map((op) => ({ first: op.bytes[0], len: op.bytes.length }));
}

describe('ghosttyModelOpRing', () => {
  it('evicts oldest writes once retained bytes pass the cap', () => {
    const ring = createGhosttyModelOpRing();
    ring.beginEpoch(80, 24);
    const chunk = 64 * 1024;
    // 9 chunks of 64KB = 576KB against a 512KB cap.
    for (let i = 0; i < 9; i += 1) {
      ring.noteWrite(bytes(chunk, i));
    }

    const stats = ring.stats();
    expect(stats.retainedWriteBytes).toBeLessThanOrEqual(MODEL_OP_RING_MAX_BYTES);
    expect(stats.droppedOps).toBe(1);
    expect(stats.droppedWriteBytes).toBe(chunk);
    // The oldest chunk went; the newest is still there, in order.
    expect(writeOps(ring.ops()).map((op) => op.first)).toEqual([1, 2, 3, 4, 5, 6, 7, 8]);
  });

  it('keeps a single write larger than the whole byte budget rather than storing half of it', () => {
    const ring = createGhosttyModelOpRing();
    ring.beginEpoch(80, 24);
    ring.noteWrite(bytes(16, 1));
    ring.noteWrite(bytes(MODEL_OP_RING_MAX_BYTES + 1024, 2));

    const ops = ring.ops();
    expect(ops).toHaveLength(1);
    expect(writeOps(ops)[0]).toEqual({ first: 2, len: MODEL_OP_RING_MAX_BYTES + 1024 });
    expect(ring.stats().droppedOps).toBe(1);
  });

  it('evicts oldest ops once the op cap is passed, preserving order', () => {
    const ring = createGhosttyModelOpRing();
    ring.beginEpoch(80, 24);
    for (let i = 0; i < MODEL_OP_RING_MAX_OPS + 10; i += 1) {
      ring.noteResize(100 + i, 24, i % 2 === 0);
    }

    const ops = ring.ops();
    expect(ops).toHaveLength(MODEL_OP_RING_MAX_OPS);
    expect(ring.stats().droppedOps).toBe(10);
    const cols = ops.map((op) => (op.kind === 'resize' ? op.cols : -1));
    expect(cols[0]).toBe(110);
    expect(cols[cols.length - 1]).toBe(100 + MODEL_OP_RING_MAX_OPS + 9);
    // Strictly increasing: eviction must not rotate the ring's contents.
    expect(cols.every((value, index) => index === 0 || value === cols[index - 1] + 1)).toBe(true);
  });

  it('advances the start geometry to the last evicted resize so a replay starts at the right grid', () => {
    const ring = createGhosttyModelOpRing();
    ring.beginEpoch(80, 24);
    ring.noteResize(120, 40, true);
    for (let i = 0; i < MODEL_OP_RING_MAX_OPS + 1; i += 1) {
      ring.noteReset();
    }

    const capture = ring.capture();
    expect(capture.startCols).toBe(120);
    expect(capture.startRows).toBe(40);
  });

  it('preserves interleaved op order across a byte-cap eviction', () => {
    const ring = createGhosttyModelOpRing();
    ring.beginEpoch(80, 24);
    ring.noteWrite(bytes(MODEL_OP_RING_MAX_BYTES - 8, 1));
    ring.noteResize(90, 30, false);
    ring.noteReset();
    ring.noteWrite(bytes(64, 2));

    // The big first write had to go; everything after it kept its order.
    expect(ring.ops().map((op) => op.kind)).toEqual(['resize', 'reset', 'write']);
  });

  it('stores copies, so mutating the caller buffer after the push cannot poison the ring', () => {
    const ring = createGhosttyModelOpRing();
    ring.beginEpoch(80, 24);
    // The real producers hand over views into buffers they still own: a
    // WebSocket frame's ArrayBuffer, and one decoded attach-replay buffer shared
    // by every 16KB chunk cut from it.
    const frame = new Uint8Array([1, 2, 3, 4, 5, 6, 7, 8]);
    ring.noteWrite(frame.subarray(2, 6));
    frame.fill(0xff);

    const op = ring.ops()[0];
    expect(op.kind).toBe('write');
    expect(Array.from((op as Extract<ModelOp, { kind: 'write' }>).bytes)).toEqual([3, 4, 5, 6]);
  });

  it('stores snapshot copies too', () => {
    const ring = createGhosttyModelOpRing();
    ring.beginEpoch(80, 24);
    const frame = new Uint8Array([10, 11, 12, 13]);
    ring.noteRestoreChunk(frame.subarray(0, 4), 100, 30);
    frame.fill(0);

    const capture = ring.capture();
    expect(Array.from(base64ToBytes(capture.snapshot!.b64))).toEqual([10, 11, 12, 13]);
  });

  it('drops everything on a new epoch', () => {
    const ring = createGhosttyModelOpRing();
    ring.beginEpoch(80, 24);
    ring.noteWrite(bytes(1024, 1));
    ring.noteRestoreChunk(bytes(64, 2), 100, 30);

    ring.beginEpoch(90, 25);

    const capture = ring.capture();
    expect(capture.ops).toEqual([]);
    expect(capture.snapshot).toBeNull();
    expect(capture.startCols).toBe(90);
    expect(capture.startRows).toBe(25);
    expect(capture.droppedOps).toBe(0);
    expect(ring.stats().retainedWriteBytes).toBe(0);
  });

  it('treats a restore as a new base state: earlier ops go, the dump and its grid stay', () => {
    const ring = createGhosttyModelOpRing();
    ring.beginEpoch(80, 24);
    ring.noteWrite(bytes(128, 1));
    ring.noteReset();
    ring.noteRestoreChunk(new Uint8Array([1, 2, 3]), 134, 58);
    ring.noteRestoreChunk(new Uint8Array([4, 5]), 134, 58);
    ring.noteWrite(bytes(16, 9));

    const capture = ring.capture();
    expect(capture.ops.map((op) => op.kind)).toEqual(['write']);
    expect(capture.snapshot).toMatchObject({ cols: 134, rows: 58, len: 5, truncated: false, dropped: 0 });
    expect(Array.from(base64ToBytes(capture.snapshot!.b64))).toEqual([1, 2, 3, 4, 5]);
    expect(capture.startCols).toBe(134);
    expect(capture.startRows).toBe(58);
  });

  it('marks a snapshot that outgrew its cap instead of silently keeping a prefix', () => {
    const ring = createGhosttyModelOpRing();
    ring.beginEpoch(80, 24);
    ring.noteRestoreChunk(bytes(MODEL_OP_RING_MAX_SNAPSHOT_BYTES - 4, 1), 100, 30);
    ring.noteRestoreChunk(bytes(100, 2), 100, 30);
    ring.noteRestoreChunk(bytes(50, 3), 100, 30);

    const capture = ring.capture();
    expect(capture.snapshotTruncated).toBe(true);
    expect(capture.snapshot).toMatchObject({
      len: MODEL_OP_RING_MAX_SNAPSHOT_BYTES,
      dropped: 146,
      truncated: true,
    });
  });

  it('encodes ops in order with decodable bytes', () => {
    let clock = 1000;
    const ring = createGhosttyModelOpRing({ now: () => (clock += 1) });
    ring.beginEpoch(80, 24);
    ring.noteWrite(new Uint8Array([104, 105])); // "hi"
    ring.noteResize(81, 24, true);
    ring.noteReset();
    ring.noteWrite(new Uint8Array([27, 99])); // ESC c

    const capture = ring.capture();
    expect(capture.ops.map((op) => op.kind)).toEqual(['write', 'resize', 'reset', 'write']);
    expect(capture.ops.map((op) => op.t)).toEqual([1003, 1004, 1005, 1006]);
    expect(capture.ops[1]).toMatchObject({ cols: 81, rows: 24, noReflow: true });
    const first = capture.ops[0] as { b64: string };
    const last = capture.ops[3] as { b64: string };
    expect(Array.from(base64ToBytes(first.b64))).toEqual([104, 105]);
    expect(Array.from(base64ToBytes(last.b64))).toEqual([27, 99]);
    expect(capture.encodedBytesEstimate).toBeGreaterThan(0);
  });

  it('fits the record budget by dropping oldest ops, and says how many', () => {
    const ring = createGhosttyModelOpRing();
    ring.beginEpoch(80, 24);
    // The ring caps retained bytes at 512KB; base64 of that plus a full 512KB
    // snapshot stays under the 2MB record budget, so force the budget path with
    // an oversized single write (kept whole by design) plus a full snapshot.
    ring.noteRestoreChunk(bytes(MODEL_OP_RING_MAX_SNAPSHOT_BYTES, 1), 100, 30);
    ring.noteWrite(bytes(1024, 2));
    ring.noteWrite(bytes(2 * 1024 * 1024, 3));

    const capture = ring.capture();
    expect(capture.droppedForRecordBudget).toBeGreaterThan(0);
    expect(capture.encodedBytesEstimate).toBeLessThanOrEqual(2 * 1024 * 1024);
    expect(capture.snapshot).not.toBeNull();
  });
});
