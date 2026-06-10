// Decoder for the daemon's binary pty_output websocket frames, sent only to
// clients that advertised the `binary_pty_output` capability in client_hello.
// Must stay in sync with internal/protocol/binaryframe.go:
//
//   offset 0      frame type (1 byte) — 0x01 = pty_output
//   offset 1      session id length L (1 byte)
//   offset 2      session id (L bytes, UTF-8)
//   offset 2+L    seq (4 bytes, uint32 big-endian)
//   offset 6+L    raw PTY bytes (rest of frame)
//
// The point of this path is allocation discipline: no JSON envelope string, no
// base64 string, no atob — just one id string and a zero-copy view of the
// payload bytes.

export const BINARY_FRAME_TYPE_PTY_OUTPUT = 0x01;

const HEADER_BYTES = 1 + 1 + 4; // type + id length + seq

export interface BinaryPtyOutputFrame {
  id: string;
  seq: number;
  /** View into the frame's buffer — valid as long as the buffer is. */
  data: Uint8Array;
}

const utf8Decoder = new TextDecoder();

export function decodeBinaryPtyFrame(buffer: ArrayBuffer): BinaryPtyOutputFrame | null {
  if (buffer.byteLength < HEADER_BYTES + 1) {
    return null;
  }
  const view = new DataView(buffer);
  if (view.getUint8(0) !== BINARY_FRAME_TYPE_PTY_OUTPUT) {
    return null;
  }
  const idLength = view.getUint8(1);
  if (idLength === 0 || buffer.byteLength < HEADER_BYTES + idLength) {
    return null;
  }
  const id = utf8Decoder.decode(new Uint8Array(buffer, 2, idLength));
  const seq = view.getUint32(2 + idLength, false);
  const data = new Uint8Array(buffer, HEADER_BYTES + idLength);
  return { id, seq, data };
}
