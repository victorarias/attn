// DEC mode 2027 (Unicode grapheme clustering) management for the Ghostty model.
//
// attn's WebGL renderers rasterize one terminal cell at a time via canvas
// fillText, so a multi-codepoint emoji grapheme cluster — a ZWJ family
// (👨‍👩‍👧‍👦), a regional-indicator flag (🇺🇸), a skin-tone (👍🏽) or keycap (1️⃣)
// sequence — only forms its ligature when the model keeps the whole cluster in a
// single cell. That requires mode 2027 to be enabled.
//
// ghostty-web's model starts with 2027 on, but a RIS (ESC c, full reset) from
// the shell or a TUI resets it off; thereafter the model splits every cluster
// across cells and the renderer draws the component glyphs (four separate heads,
// two boxed flag letters, a thumb plus a colour swatch). attn therefore keeps
// the mode enabled. The subtlety: a single PTY chunk can carry a RIS *followed
// by* clustered emoji (a prompt/status redraw that resets then repaints), so we
// cannot just re-assert after the whole chunk is written — by then those emoji
// have already been parsed into split cells. writeReassertingClustering splits
// each chunk at the RIS and re-enables 2027 immediately after it, before the
// rest of the chunk is parsed.

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

// Unconditionally enable grapheme clustering. Used right after the model is
// created so emoji clusters render whole from the first frame regardless of the
// library's default.
export function enableGraphemeClustering(terminal: GraphemeModeTerminal): void {
  terminal.write(ENABLE_SEQUENCE);
}

// Re-enable grapheme clustering if it is currently off. Returns true when it had
// to re-assert the mode. Used as a backstop after each chunk to recover from an
// explicit DECRST 2027l (or any other mode-off) for subsequent output. The RIS
// case is handled precisely, mid-chunk, by writeReassertingClustering.
export function ensureGraphemeClustering(terminal: GraphemeModeTerminal): boolean {
  if (terminal.getMode(GRAPHEME_CLUSTERING_MODE)) return false;
  terminal.write(ENABLE_SEQUENCE);
  return true;
}

// Write `bytes` to the model, re-asserting grapheme clustering immediately after
// any RIS (ESC c) so emoji later in the SAME chunk are still parsed as whole
// clusters. RIS resets DEC 2027 off, so without this split a "reset then repaint
// a prompt with an emoji" chunk would decompose that emoji before any
// after-the-fact re-enable could run.
//
// `trailingEsc` carries a lone ESC left at the end of the previous chunk (a RIS
// can straddle a chunk boundary); pass the return value back in on the next
// call. Reset it (to false) whenever the model is recreated.
//
// Only ESC c triggers a re-assert. ESC c is unambiguously RIS at the top parser
// level, and the worst case of a false match (an ESC c buried in a string
// payload) is a harmless extra DECSET of a mode we want enabled anyway.
export function writeReassertingClustering(
  terminal: GraphemeModeTerminal,
  bytes: Uint8Array,
  trailingEsc: boolean,
): boolean {
  if (bytes.length === 0) return trailingEsc;

  let start = 0;
  // RIS straddling the boundary: previous chunk ended on a lone ESC and this one
  // opens with 'c'. The ESC is already in the model; writing the 'c' completes
  // the RIS, then we re-enable.
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
