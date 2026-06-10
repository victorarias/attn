import { describe, expect, it } from 'vitest';
import { BINARY_FRAME_TYPE_PTY_OUTPUT, decodeBinaryPtyFrame } from './binaryPtyFrame';

// Mirrors the daemon encoder (internal/protocol/binaryframe.go):
// [type u8][idLen u8][id utf8][seq u32 BE][payload]
function buildFrame(id: string, seq: number, payload: Uint8Array): ArrayBuffer {
  const idBytes = new TextEncoder().encode(id);
  const frame = new Uint8Array(2 + idBytes.length + 4 + payload.length);
  frame[0] = BINARY_FRAME_TYPE_PTY_OUTPUT;
  frame[1] = idBytes.length;
  frame.set(idBytes, 2);
  new DataView(frame.buffer).setUint32(2 + idBytes.length, seq, false);
  frame.set(payload, 2 + idBytes.length + 4);
  return frame.buffer;
}

describe('decodeBinaryPtyFrame', () => {
  it('decodes a frame round-trip', () => {
    const payload = new Uint8Array([0x68, 0x69, 0x1b, 0x5b, 0x30, 0x6d, 0x00, 0xff]);
    const frame = decodeBinaryPtyFrame(buildFrame('sess-42', 123456789, payload));
    expect(frame).not.toBeNull();
    expect(frame!.id).toBe('sess-42');
    expect(frame!.seq).toBe(123456789);
    expect(Array.from(frame!.data)).toEqual(Array.from(payload));
  });

  it('decodes an empty payload', () => {
    const frame = decodeBinaryPtyFrame(buildFrame('s', 0, new Uint8Array(0)));
    expect(frame).not.toBeNull();
    expect(frame!.id).toBe('s');
    expect(frame!.seq).toBe(0);
    expect(frame!.data.byteLength).toBe(0);
  });

  it('rejects malformed frames', () => {
    expect(decodeBinaryPtyFrame(new ArrayBuffer(0))).toBeNull();
    expect(decodeBinaryPtyFrame(new ArrayBuffer(5))).toBeNull();

    const wrongType = new Uint8Array(buildFrame('abc', 7, new Uint8Array([1])));
    wrongType[0] = 0x7f;
    expect(decodeBinaryPtyFrame(wrongType.buffer)).toBeNull();

    const idOverrun = new Uint8Array([BINARY_FRAME_TYPE_PTY_OUTPUT, 200, 97, 98, 0, 0, 0, 1]);
    expect(decodeBinaryPtyFrame(idOverrun.buffer)).toBeNull();

    const zeroIdLength = new Uint8Array([BINARY_FRAME_TYPE_PTY_OUTPUT, 0, 0, 0, 0, 1, 120]);
    expect(decodeBinaryPtyFrame(zeroIdLength.buffer)).toBeNull();
  });
});
