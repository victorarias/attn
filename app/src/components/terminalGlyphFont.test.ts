import { describe, expect, it } from 'vitest';
import { graphemeNeedsEmojiShaping, terminalGlyphFont } from './terminalGlyphFont';

describe('graphemeNeedsEmojiShaping', () => {
  it('routes multi-codepoint emoji clusters to emoji-first shaping', () => {
    expect(graphemeNeedsEmojiShaping('👨‍👩‍👧‍👦')).toBe(true); // family ZWJ
    expect(graphemeNeedsEmojiShaping('🏳️‍🌈')).toBe(true); // rainbow flag ZWJ
    expect(graphemeNeedsEmojiShaping('🇺🇸')).toBe(true); // regional-indicator flag
    expect(graphemeNeedsEmojiShaping('👍🏽')).toBe(true); // skin-tone modifier
    expect(graphemeNeedsEmojiShaping('1️⃣')).toBe(true); // keycap
    expect(graphemeNeedsEmojiShaping('❤️')).toBe(true); // VS16 emoji presentation
  });

  it('leaves single emoji on the normal font (they already shape correctly)', () => {
    expect(graphemeNeedsEmojiShaping('😀')).toBe(false);
    expect(graphemeNeedsEmojiShaping('🔥')).toBe(false);
    expect(graphemeNeedsEmojiShaping('✅')).toBe(false);
  });

  it('leaves text, box-drawing, and bare text-default symbols untouched', () => {
    expect(graphemeNeedsEmojiShaping('A')).toBe(false);
    expect(graphemeNeedsEmojiShaping('한')).toBe(false);
    expect(graphemeNeedsEmojiShaping('│')).toBe(false); // box drawing
    expect(graphemeNeedsEmojiShaping('❤')).toBe(false); // U+2764 without VS16 -> text default
  });

  it('does not route Private Use Area Nerd Font icons to the emoji font', () => {
    expect(graphemeNeedsEmojiShaping('')).toBe(false); // BMP PUA folder icon
    expect(graphemeNeedsEmojiShaping('\u{f0300}')).toBe(false); // Plane-15 PUA icon
  });

  it('does not treat complex-script ZWJ usage as emoji', () => {
    // Devanagari kssha with ZWJ: ZWJ present but no emoji base codepoint.
    expect(graphemeNeedsEmojiShaping('क‍ष')).toBe(false);
  });
});

describe('terminalGlyphFont', () => {
  const base = 'Iosevka, Menlo, monospace';

  it('prepends Apple Color Emoji for emoji clusters', () => {
    expect(terminalGlyphFont('', 28, base, '🇺🇸')).toBe(`28px "Apple Color Emoji", ${base}`);
    expect(terminalGlyphFont('bold ', 14, base, '👍🏽')).toBe(`bold 14px "Apple Color Emoji", ${base}`);
  });

  it('uses the normal text-first family for everything else', () => {
    expect(terminalGlyphFont('', 28, base, 'A')).toBe(`28px ${base}`);
    expect(terminalGlyphFont('italic ', 14, base, '😀')).toBe(`italic 14px ${base}`);
  });
});
