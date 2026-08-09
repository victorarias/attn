// Decoder for the daemon's binary websocket frames, sent only to clients that
// advertised the `binary_pty_output` capability. Must stay in sync with
// internal/protocol/binaryframe.go:
//
//   0x01 pty_output
//     offset 0      frame type (1 byte)
//     offset 1      session id length L (1 byte)
//     offset 2      session id (L bytes, UTF-8)
//     offset 2+L    seq (4 bytes, uint32 big-endian)
//     offset 6+L    raw PTY bytes (rest of frame)
//
//   0x02 kitty_image
//     offset 0      frame type (1 byte)
//     offset 1      session id length L (1 byte)
//     offset 2      session id (L bytes, UTF-8)
//     offset 2+L    image id (4 bytes, uint32 big-endian)
//     offset 6+L    image generation (8 bytes, uint64 big-endian)
//     offset 14+L   width in pixels (4 bytes, uint32 big-endian)
//     offset 18+L   height in pixels (4 bytes, uint32 big-endian)
//     offset 22+L   pixel format (1 byte)
//     offset 23+L   raw pixels (rest of frame)
//
// Allocation discipline: no envelope, no base64, one id string and a view.

import { kittyPixelFormatFromCode, type KittyPixelFormat } from '../utils/kittyImageFormat';

export const BINARY_FRAME_TYPE_PTY_OUTPUT = 0x01;
export const BINARY_FRAME_TYPE_KITTY_IMAGE = 0x02;

const PTY_HEADER_BYTES = 1 + 1 + 4; // type + id length + seq
const KITTY_HEADER_BYTES = 1 + 1 + 4 + 8 + 4 + 4 + 1; // + image id, generation, w, h, format

export interface BinaryPtyOutputFrame {
  kind: 'pty_output';
  id: string;
  seq: number;
  /** View into the frame's buffer — valid as long as the buffer is. */
  data: Uint8Array;
}

export interface BinaryKittyImageFrame {
  kind: 'kitty_image';
  id: string;
  imageId: number;
  generation: number;
  width: number;
  height: number;
  format: KittyPixelFormat;
  /** View into the frame's buffer — valid as long as the buffer is. */
  pixels: Uint8Array;
}

export type BinaryFrame = BinaryPtyOutputFrame | BinaryKittyImageFrame;

const utf8Decoder = new TextDecoder();

/** Decode one binary websocket frame, or null (always announced) when it is
 * malformed or of an unknown type. */
export function decodeBinaryFrame(buffer: ArrayBuffer): BinaryFrame | null {
  if (buffer.byteLength < 2) return null;
  const view = new DataView(buffer);
  const type = view.getUint8(0);
  switch (type) {
    case BINARY_FRAME_TYPE_PTY_OUTPUT:
      return decodePtyOutput(buffer, view);
    case BINARY_FRAME_TYPE_KITTY_IMAGE:
      return decodeKittyImage(buffer, view);
    default:
      console.warn(
        `[Daemon] Dropping binary frame of unknown type 0x${type.toString(16).padStart(2, '0')} (${buffer.byteLength} bytes)`,
      );
      return null;
  }
}

function decodePtyOutput(buffer: ArrayBuffer, view: DataView): BinaryPtyOutputFrame | null {
  if (buffer.byteLength < PTY_HEADER_BYTES + 1) return null;
  const idLength = view.getUint8(1);
  if (idLength === 0 || buffer.byteLength < PTY_HEADER_BYTES + idLength) return null;
  const id = utf8Decoder.decode(new Uint8Array(buffer, 2, idLength));
  const seq = view.getUint32(2 + idLength, false);
  const data = new Uint8Array(buffer, PTY_HEADER_BYTES + idLength);
  return { kind: 'pty_output', id, seq, data };
}

function decodeKittyImage(buffer: ArrayBuffer, view: DataView): BinaryKittyImageFrame | null {
  if (buffer.byteLength < KITTY_HEADER_BYTES + 1) return null;
  const idLength = view.getUint8(1);
  if (idLength === 0 || buffer.byteLength <= KITTY_HEADER_BYTES + idLength) return null;
  const offset = 2 + idLength;
  const formatCode = view.getUint8(offset + 20);
  const format = kittyPixelFormatFromCode(formatCode);
  if (!format) {
    // The wrong stride renders plausible garbage rather than failing, so an
    // unnamed layout is dropped instead of guessed at.
    console.warn(`[Daemon] Dropping kitty image frame with unknown pixel format ${formatCode}`);
    return null;
  }
  const generation = view.getBigUint64(offset + 4, false);
  if (generation > BigInt(Number.MAX_SAFE_INTEGER)) {
    console.warn(
      `[Daemon] Dropping kitty image frame with generation ${generation}, past the largest integer this client can key a cache by`,
    );
    return null;
  }
  return {
    kind: 'kitty_image',
    id: utf8Decoder.decode(new Uint8Array(buffer, 2, idLength)),
    imageId: view.getUint32(offset, false),
    generation: Number(generation),
    width: view.getUint32(offset + 12, false),
    height: view.getUint32(offset + 16, false),
    format,
    pixels: new Uint8Array(buffer, KITTY_HEADER_BYTES + idLength),
  };
}
