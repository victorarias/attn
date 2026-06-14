// Font selection for glyph rasterization, shared by attn's two WebGL terminal
// renderers (GhosttyWebGlRenderer, UnifiedGridRenderer) so they stay in lockstep.
//
// THE PROBLEM. A multi-codepoint emoji grapheme cluster — a ZWJ sequence
// (👨‍👩‍👧‍👦), a regional-indicator flag (🇺🇸), a skin-tone sequence (👍🏽), or a
// keycap (1️⃣) — must be shaped as ONE Apple Color Emoji run for its ligature to
// form. ghostty-web hands us the whole cluster in a single cell, but when we
// rasterize it with a text-first font-family (Iosevka/Menlo/… , monospace),
// WKWebView's canvas fallback resolves the cluster's joiner/scaffolding
// codepoints per-character instead of shaping the cluster cohesively, so the
// ligature never forms and the cluster decomposes into its component glyphs
// (four separate heads, two boxed flag letters, a thumb plus a colour swatch).
//
// THE FIX. For graphemes that are emoji clusters, put "Apple Color Emoji" FIRST
// so the entire cluster is shaped by it. Single emoji (which already render
// fine via system fallback), text, box-drawing, and bundled Nerd Font PUA icons
// are left on the normal text-first family untouched.

export const APPLE_COLOR_EMOJI_FAMILY = '"Apple Color Emoji"';

const ZWJ = 0x200d;
const VS16 = 0xfe0f;
const KEYCAP = 0x20e3;

function isRegionalIndicator(cp: number): boolean {
  return cp >= 0x1f1e6 && cp <= 0x1f1ff;
}
function isSkinToneModifier(cp: number): boolean {
  return cp >= 0x1f3fb && cp <= 0x1f3ff;
}
// The Supplementary Multilingual Plane emoji blocks (1F000–1FAFF). Deliberately
// excludes the Private Use Area planes (F0000+) the bundled Nerd Font occupies,
// so icon glyphs never get routed to the emoji font.
function isSupplementaryEmoji(cp: number): boolean {
  return cp >= 0x1f000 && cp <= 0x1faff;
}

// True when `text` is a multi-codepoint emoji cluster that must be shaped as a
// single Apple Color Emoji run. Intentionally conservative: a bare text-default
// symbol (❤ U+2764 without VS16), a single emoji, box-drawing, plain text, and
// PUA icons all return false and keep the normal text-first font. ZWJ is only
// treated as emoji intent when it joins actual emoji, so complex-script ZWJ
// usage (Devanagari, Arabic) is not misrouted.
export function graphemeNeedsEmojiShaping(text: string): boolean {
  let hasVs16 = false;
  let hasKeycap = false;
  let hasZwj = false;
  let hasEmojiBase = false;
  let codepoints = 0;

  for (const ch of text) {
    const cp = ch.codePointAt(0);
    if (cp === undefined) continue;
    codepoints += 1;
    if (cp === VS16) hasVs16 = true;
    else if (cp === KEYCAP) hasKeycap = true;
    else if (cp === ZWJ) hasZwj = true;
    if (isRegionalIndicator(cp) || isSkinToneModifier(cp) || isSupplementaryEmoji(cp)) {
      hasEmojiBase = true;
    }
  }

  if (hasVs16) return true; // explicit emoji presentation (❤️, ▶️, …)
  if (hasKeycap) return true; // keycap sequence (1️⃣, #️⃣)
  if (hasZwj && hasEmojiBase) return true; // emoji ZWJ sequence (family, rainbow flag)
  if (hasEmojiBase && codepoints > 1) return true; // regional flag / skin-tone sequence
  return false;
}

// Build the canvas `font` string for a glyph. `style` is the leading
// "italic "/"bold " prefix, `sizePx` the already-DPR-scaled pixel size, and
// `baseFamily` the terminal's normal text-first font-family list. Emoji
// clusters are shaped Apple-Color-Emoji-first; everything else uses baseFamily.
export function terminalGlyphFont(
  style: string,
  sizePx: number,
  baseFamily: string,
  text: string,
): string {
  const family = graphemeNeedsEmojiShaping(text)
    ? `${APPLE_COLOR_EMOJI_FAMILY}, ${baseFamily}`
    : baseFamily;
  return `${style}${sizePx}px ${family}`;
}
