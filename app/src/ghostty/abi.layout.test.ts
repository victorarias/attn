// @vitest-environment node
// The receipt behind abi.ts.
//
// Every struct offset and size in abi.ts was read off include/ghostty/vt/*.h by
// hand. A hand-copied offset is only right until upstream moves a field, and a
// wrong one does not throw: it reads the neighbouring bytes and renders a wrong
// colour or a wrong flag, quietly, forever.
//
// libghostty-vt describes its own layout, so this asks the shipped module
// instead of trusting the transcription. A pin bump that moves a field fails
// here, by name, with both numbers.
//
// It found one on the first run: COLORS_SIZE was 782, the struct's content, not
// the 784 the ABI pads it to.
// @types/node isn't a direct dependency of this package (only a transitive peer
// of vite/vitest), matching kittyWireRewrite.parity.test.ts's pattern.
// @ts-expect-error -- see above
import { readFileSync } from 'node:fs';
// @ts-expect-error -- see above
import { fileURLToPath } from 'node:url';
import { beforeAll, describe, expect, it } from 'vitest';
import {
  COLORS_OFF_BACKGROUND,
  COLORS_OFF_CURSOR,
  COLORS_OFF_CURSOR_HAS_VALUE,
  COLORS_OFF_FOREGROUND,
  COLORS_OFF_PALETTE,
  COLORS_SIZE,
  GRID_REF_SIZE,
  STYLE_OFF_BG,
  STYLE_OFF_BG_KIND,
  STYLE_OFF_BLINK,
  STYLE_OFF_BOLD,
  STYLE_OFF_FAINT,
  STYLE_OFF_FG,
  STYLE_OFF_FG_KIND,
  STYLE_OFF_INVERSE,
  STYLE_OFF_INVISIBLE,
  STYLE_OFF_ITALIC,
  STYLE_OFF_OVERLINE,
  STYLE_OFF_STRIKETHROUGH,
  STYLE_OFF_UNDERLINE,
  STYLE_SIZE,
} from './abi';

const wasmPath = fileURLToPath(new URL('../../vendor/ghostty-vt/ghostty-vt.wasm', import.meta.url));

interface TypeField {
  offset: number;
  size: number;
}

interface TypeInfo {
  size: number;
  align: number;
  fields: Record<string, TypeField>;
}

let types: Record<string, TypeInfo>;
let abi: { pointer_size: number; usize_size: number };

beforeAll(async () => {
  const mod = await WebAssembly.compile(readFileSync(wasmPath));
  let instance: WebAssembly.Instance;
  instance = await WebAssembly.instantiate(mod, {
    env: { log: () => {} },
  });
  const exports = instance.exports as {
    memory: WebAssembly.Memory;
    ghostty_type_json(): number;
  };
  const ptr = exports.ghostty_type_json();
  const bytes = new Uint8Array(exports.memory.buffer);
  let end = ptr;
  while (bytes[end] !== 0) end += 1;
  const doc = JSON.parse(new TextDecoder().decode(bytes.subarray(ptr, end)));
  types = doc.types;
  abi = doc.abi;
});

describe('the ABI abi.ts transcribes', () => {
  // Every offset below is a wasm32 offset. On a 64-bit ABI the leading size_t
  // alone would shift all of them, so this is the assumption the rest rests on.
  it('is the wasm32 one', () => {
    expect(abi.pointer_size).toBe(4);
    expect(abi.usize_size).toBe(4);
  });

  it('lays GhosttyRenderStateColors out where getColors reads it', () => {
    const t = types.GhosttyRenderStateColors;
    expect(t.size).toBe(COLORS_SIZE);
    expect(t.fields.background.offset).toBe(COLORS_OFF_BACKGROUND);
    expect(t.fields.foreground.offset).toBe(COLORS_OFF_FOREGROUND);
    expect(t.fields.cursor.offset).toBe(COLORS_OFF_CURSOR);
    expect(t.fields.cursor_has_value.offset).toBe(COLORS_OFF_CURSOR_HAS_VALUE);
    expect(t.fields.palette.offset).toBe(COLORS_OFF_PALETTE);
    expect(t.fields.palette.size).toBe(256 * 3);
  });

  it('lays GhosttyStyle out where the cell attributes are read', () => {
    const t = types.GhosttyStyle;
    expect(t.size).toBe(STYLE_SIZE);
    expect(t.fields.bold.offset).toBe(STYLE_OFF_BOLD);
    expect(t.fields.italic.offset).toBe(STYLE_OFF_ITALIC);
    expect(t.fields.faint.offset).toBe(STYLE_OFF_FAINT);
    expect(t.fields.blink.offset).toBe(STYLE_OFF_BLINK);
    expect(t.fields.inverse.offset).toBe(STYLE_OFF_INVERSE);
    expect(t.fields.invisible.offset).toBe(STYLE_OFF_INVISIBLE);
    expect(t.fields.strikethrough.offset).toBe(STYLE_OFF_STRIKETHROUGH);
    expect(t.fields.overline.offset).toBe(STYLE_OFF_OVERLINE);
    expect(t.fields.underline.offset).toBe(STYLE_OFF_UNDERLINE);
  });

  // The scrollback path reads a colour slot as two u32s at absolute offsets,
  // so it depends on where the slot sits AND on its own inner layout.
  it('lays the style colour slots out where the scrollback path reads them', () => {
    const slot = types.GhosttyStyleColor;
    const fg = types.GhosttyStyle.fields.fg_color.offset;
    const bg = types.GhosttyStyle.fields.bg_color.offset;
    expect(fg + slot.fields.tag.offset).toBe(STYLE_OFF_FG_KIND);
    expect(fg + slot.fields.value.offset).toBe(STYLE_OFF_FG);
    expect(bg + slot.fields.tag.offset).toBe(STYLE_OFF_BG_KIND);
    expect(bg + slot.fields.value.offset).toBe(STYLE_OFF_BG);
  });

  it('sizes GhosttyGridRef as the scratch buffer assumes', () => {
    expect(types.GhosttyGridRef.size).toBe(GRID_REF_SIZE);
  });
});
