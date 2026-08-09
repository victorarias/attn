// DEC mode 2027 (Unicode grapheme clustering) management for the Ghostty model.
//
// The renderers rasterize one cell at a time, so an emoji cluster only forms
// its ligature while 2027 keeps it in a single cell. RIS (ESC c) turns the mode
// off, and one PTY chunk can carry a RIS followed by clustered emoji — so the
// re-assert has to happen mid-chunk, not after it.

const ENCODER = new TextEncoder();

// DECSET 2027 — enable Unicode grapheme clustering.
const ENABLE_SEQUENCE = ENCODER.encode('\x1b[?2027h');

const ESC = 0x1b;
const RIS_FINAL = 0x63; // 'c'; ESC c is RIS (full reset).

export const GRAPHEME_CLUSTERING_MODE = 2027;

// The subset of the Ghostty terminal model this module touches.
export interface GraphemeModeTerminal {
  getMode(mode: number, isAnsi?: boolean): boolean;
  write(data: Uint8Array): void;
}

// Unconditionally enable grapheme clustering, regardless of the model default.
export function enableGraphemeClustering(terminal: GraphemeModeTerminal): void {
  terminal.write(ENABLE_SEQUENCE);
}

// Re-enable grapheme clustering if off, returning whether it re-asserted.
// Per-chunk backstop for an explicit DECRST 2027l; RIS is handled mid-chunk by
// writeReassertingClustering.
export function ensureGraphemeClustering(terminal: GraphemeModeTerminal): boolean {
  if (terminal.getMode(GRAPHEME_CLUSTERING_MODE)) return false;
  terminal.write(ENABLE_SEQUENCE);
  return true;
}

// Write `bytes`, re-asserting grapheme clustering immediately after any RIS so
// emoji later in the SAME chunk still cluster. `trailingEsc` carries a lone ESC
// from the previous chunk (a RIS can straddle the boundary); feed the return
// value back in, and reset it to false whenever the model is recreated.
export function writeReassertingClustering(
  terminal: GraphemeModeTerminal,
  bytes: Uint8Array,
  trailingEsc: boolean,
): boolean {
  if (bytes.length === 0) return trailingEsc;

  let start = 0;
  // RIS straddling the boundary: the ESC is already in the model, so writing
  // the 'c' completes it.
  if (trailingEsc && bytes[0] === RIS_FINAL) {
    terminal.write(bytes.subarray(0, 1));
    terminal.write(ENABLE_SEQUENCE);
    start = 1;
  }

  for (let i = start; i + 1 < bytes.length; i += 1) {
    if (bytes[i] === ESC && bytes[i + 1] === RIS_FINAL) {
      terminal.write(bytes.subarray(start, i + 2)); // through the RIS, inclusive
      terminal.write(ENABLE_SEQUENCE);
      start = i + 2;
      i += 1; // 'c' already consumed
    }
  }

  if (start < bytes.length) terminal.write(bytes.subarray(start));

  return bytes[bytes.length - 1] === ESC;
}
