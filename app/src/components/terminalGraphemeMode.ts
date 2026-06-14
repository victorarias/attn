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
// the mode enabled: once at model creation, and re-asserted whenever a reset has
// turned it off.

const ENCODER = new TextEncoder();

// DECSET 2027 — enable Unicode grapheme clustering.
const ENABLE_SEQUENCE = ENCODER.encode('\x1b[?2027h');

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

// Re-enable grapheme clustering if a reset has turned it off. Returns true when
// it had to re-assert the mode. Cheap enough to call after every output chunk —
// one model mode query, and a write only on the rare chunk that contained a RIS.
export function ensureGraphemeClustering(terminal: GraphemeModeTerminal): boolean {
  if (terminal.getMode(GRAPHEME_CLUSTERING_MODE)) return false;
  terminal.write(ENABLE_SEQUENCE);
  return true;
}
