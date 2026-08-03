import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  BINARY_FRAME_TYPE_KITTY_IMAGE,
  BINARY_FRAME_TYPE_PTY_OUTPUT,
  decodeBinaryFrame,
} from './binaryPtyFrame';

// Mirrors the daemon encoder (internal/protocol/binaryframe.go):
// [type u8][idLen u8][id utf8][seq u32 BE][payload]
function buildPtyFrame(id: string, seq: number, payload: Uint8Array): ArrayBuffer {
  const idBytes = new TextEncoder().encode(id);
  const frame = new Uint8Array(2 + idBytes.length + 4 + payload.length);
  frame[0] = BINARY_FRAME_TYPE_PTY_OUTPUT;
  frame[1] = idBytes.length;
  frame.set(idBytes, 2);
  new DataView(frame.buffer).setUint32(2 + idBytes.length, seq, false);
  frame.set(payload, 2 + idBytes.length + 4);
  return frame.buffer;
}

// Mirrors EncodeKittyImageFrame:
// [type u8][idLen u8][id utf8][imageId u32][generation u64][w u32][h u32][format u8][pixels]
function buildKittyFrame(
  id: string,
  imageId: number,
  generation: bigint,
  width: number,
  height: number,
  format: number,
  pixels: Uint8Array,
): ArrayBuffer {
  const idBytes = new TextEncoder().encode(id);
  const frame = new Uint8Array(2 + idBytes.length + 4 + 8 + 4 + 4 + 1 + pixels.length);
  const view = new DataView(frame.buffer);
  frame[0] = BINARY_FRAME_TYPE_KITTY_IMAGE;
  frame[1] = idBytes.length;
  frame.set(idBytes, 2);
  const offset = 2 + idBytes.length;
  view.setUint32(offset, imageId, false);
  view.setBigUint64(offset + 4, generation, false);
  view.setUint32(offset + 12, width, false);
  view.setUint32(offset + 16, height, false);
  frame[offset + 20] = format;
  frame.set(pixels, offset + 21);
  return frame.buffer;
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('decodeBinaryFrame', () => {
  it('decodes a pty_output frame round-trip', () => {
    const payload = new Uint8Array([0x68, 0x69, 0x1b, 0x5b, 0x30, 0x6d, 0x00, 0xff]);
    const frame = decodeBinaryFrame(buildPtyFrame('sess-42', 123456789, payload));
    expect(frame?.kind).toBe('pty_output');
    expect(frame).toMatchObject({ id: 'sess-42', seq: 123456789 });
    expect(Array.from((frame as { data: Uint8Array }).data)).toEqual(Array.from(payload));
  });

  it('decodes a pty_output frame with an empty payload', () => {
    const frame = decodeBinaryFrame(buildPtyFrame('s', 0, new Uint8Array(0)));
    expect(frame).toMatchObject({ kind: 'pty_output', id: 's', seq: 0 });
    expect((frame as { data: Uint8Array }).data.byteLength).toBe(0);
  });

  it('rejects malformed pty_output frames', () => {
    expect(decodeBinaryFrame(new ArrayBuffer(0))).toBeNull();
    expect(decodeBinaryFrame(new ArrayBuffer(5))).toBeNull();

    const idOverrun = new Uint8Array([BINARY_FRAME_TYPE_PTY_OUTPUT, 200, 97, 98, 0, 0, 0, 1]);
    expect(decodeBinaryFrame(idOverrun.buffer)).toBeNull();

    const zeroIdLength = new Uint8Array([BINARY_FRAME_TYPE_PTY_OUTPUT, 0, 0, 0, 0, 1, 120]);
    expect(decodeBinaryFrame(zeroIdLength.buffer)).toBeNull();
  });

  it('decodes a kitty image frame round-trip', () => {
    // A 2x1 RGB image: the format byte, the dimensions, and the pixel bytes all
    // have to be read at the right offsets or the pixels are drawn with the
    // wrong stride, which renders as plausible garbage rather than failing.
    const pixels = new Uint8Array([1, 2, 3, 4, 5, 6]);
    const frame = decodeBinaryFrame(
      buildKittyFrame('sess-42', 7, 99n, 2, 1, 0, pixels),
    );
    expect(frame).toMatchObject({
      kind: 'kitty_image',
      id: 'sess-42',
      imageId: 7,
      generation: 99,
      width: 2,
      height: 1,
      format: 'rgb',
    });
    expect(Array.from((frame as { pixels: Uint8Array }).pixels)).toEqual(Array.from(pixels));
  });

  it('names each pixel layout by its code', () => {
    const codes: Array<[number, string]> = [[0, 'rgb'], [1, 'rgba'], [2, 'gray_alpha'], [3, 'gray']];
    for (const [code, name] of codes) {
      const frame = decodeBinaryFrame(
        buildKittyFrame('s', 1, 1n, 1, 1, code, new Uint8Array([9, 9, 9, 9])),
      );
      expect(frame).toMatchObject({ kind: 'kitty_image', format: name });
    }
  });

  it('drops a kitty image frame whose pixel layout it cannot name', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const frame = decodeBinaryFrame(
      buildKittyFrame('s', 1, 1n, 1, 1, 9, new Uint8Array([1, 2, 3])),
    );
    expect(frame).toBeNull();
    expect(warn).toHaveBeenCalledWith(expect.stringContaining('unknown pixel format 9'));
  });

  it('drops a kitty image frame whose generation cannot be a cache key', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const frame = decodeBinaryFrame(
      buildKittyFrame('s', 1, BigInt(Number.MAX_SAFE_INTEGER) + 1n, 1, 1, 0, new Uint8Array([1, 2, 3])),
    );
    expect(frame).toBeNull();
    expect(warn).toHaveBeenCalledWith(expect.stringContaining('generation'));
  });

  it('rejects a kitty image frame with no pixels after its header', () => {
    const headerOnly = new Uint8Array(buildKittyFrame('s', 1, 1n, 1, 1, 0, new Uint8Array([1])));
    expect(decodeBinaryFrame(headerOnly.slice(0, headerOnly.length - 1).buffer)).toBeNull();
  });

  it('drops an unknown frame type loudly', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const unknown = new Uint8Array(buildPtyFrame('abc', 7, new Uint8Array([1])));
    unknown[0] = 0x7f;
    expect(decodeBinaryFrame(unknown.buffer)).toBeNull();
    expect(warn).toHaveBeenCalledWith(expect.stringContaining('0x7f'));
  });
});
