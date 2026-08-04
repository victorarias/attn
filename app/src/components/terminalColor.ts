// Colors shared by the terminal and mission-control grid renderers. Both stage
// vertices through TerminalVertexBuffer, which stores a color as a single
// packed integer: a per-cell {r,g,b} object allocates on the hottest loop in
// the app, and the GPU only ever sees three normalized floats anyway.

export interface Rgb {
  r: number;
  g: number;
  b: number;
}

// 0xRRGGBB. Channels are 0-255; the buffer normalizes on write.
export type PackedRgb = number;

export function packRgb(r: number, g: number, b: number): PackedRgb {
  return r << 16 | g << 8 | b;
}

export function packColor(color: Rgb): PackedRgb {
  return packRgb(color.r, color.g, color.b);
}

export function parseColor(value: string): Rgb {
  const normalized = value.replace('#', '');
  return {
    r: Number.parseInt(normalized.slice(0, 2), 16),
    g: Number.parseInt(normalized.slice(2, 4), 16),
    b: Number.parseInt(normalized.slice(4, 6), 16),
  };
}

export function parsePackedColor(value: string): PackedRgb {
  return packColor(parseColor(value));
}

export const PACKED_WHITE: PackedRgb = 0xffffff;
