export const TERMINAL_FLOATS_PER_VERTEX = 9;
export const TERMINAL_FLOATS_PER_QUAD = TERMINAL_FLOATS_PER_VERTEX * 6;

interface Rgb {
  r: number;
  g: number;
  b: number;
}

type TerminalVertexColor = Rgb | number;

// Reusable CPU-side vertex staging for terminal glyphs and solid quads. A
// number[] plus `new Float32Array(vertices)` allocates and copies the complete
// frame on every paint; this buffer writes the GPU's native representation once
// and only grows when a larger frame actually needs it.
export class TerminalVertexBuffer {
  private scratch: Float32Array;
  private cursor = 0;

  constructor(initialCapacity = 1 << 15) {
    this.scratch = new Float32Array(Math.max(initialCapacity, TERMINAL_FLOATS_PER_QUAD));
  }

  get length(): number {
    return this.cursor;
  }

  get quadCount(): number {
    return this.cursor / TERMINAL_FLOATS_PER_QUAD;
  }

  reset(): void {
    this.cursor = 0;
  }

  view(): Float32Array {
    return this.scratch.subarray(0, this.cursor);
  }

  pushQuad(
    x: number,
    y: number,
    width: number,
    height: number,
    u0: number,
    v0: number,
    u1: number,
    v1: number,
    color: TerminalVertexColor,
    alpha: number,
    mode: number,
  ): void {
    this.ensureCapacity(TERMINAL_FLOATS_PER_QUAD);
    const vertices = this.scratch;
    const r = (typeof color === 'number' ? color >>> 16 & 0xff : color.r) / 255;
    const g = (typeof color === 'number' ? color >>> 8 & 0xff : color.g) / 255;
    const b = (typeof color === 'number' ? color & 0xff : color.b) / 255;
    let offset = this.cursor;

    vertices[offset++] = x; vertices[offset++] = y; vertices[offset++] = u0; vertices[offset++] = v0; vertices[offset++] = r; vertices[offset++] = g; vertices[offset++] = b; vertices[offset++] = alpha; vertices[offset++] = mode;
    vertices[offset++] = x + width; vertices[offset++] = y; vertices[offset++] = u1; vertices[offset++] = v0; vertices[offset++] = r; vertices[offset++] = g; vertices[offset++] = b; vertices[offset++] = alpha; vertices[offset++] = mode;
    vertices[offset++] = x; vertices[offset++] = y + height; vertices[offset++] = u0; vertices[offset++] = v1; vertices[offset++] = r; vertices[offset++] = g; vertices[offset++] = b; vertices[offset++] = alpha; vertices[offset++] = mode;
    vertices[offset++] = x; vertices[offset++] = y + height; vertices[offset++] = u0; vertices[offset++] = v1; vertices[offset++] = r; vertices[offset++] = g; vertices[offset++] = b; vertices[offset++] = alpha; vertices[offset++] = mode;
    vertices[offset++] = x + width; vertices[offset++] = y; vertices[offset++] = u1; vertices[offset++] = v0; vertices[offset++] = r; vertices[offset++] = g; vertices[offset++] = b; vertices[offset++] = alpha; vertices[offset++] = mode;
    vertices[offset++] = x + width; vertices[offset++] = y + height; vertices[offset++] = u1; vertices[offset++] = v1; vertices[offset++] = r; vertices[offset++] = g; vertices[offset++] = b; vertices[offset++] = alpha; vertices[offset++] = mode;

    this.cursor = offset;
  }

  private ensureCapacity(additional: number): void {
    const required = this.cursor + additional;
    if (required <= this.scratch.length) return;
    let capacity = this.scratch.length;
    while (capacity < required) capacity *= 2;
    const grown = new Float32Array(capacity);
    grown.set(this.scratch.subarray(0, this.cursor));
    this.scratch = grown;
  }
}
