import { describe, expect, it } from 'vitest';
import { isColorGlyphBitmap } from './terminalGlyphProgram';

function img(pixels: Array<[number, number, number, number]>): ImageData {
  const data = new Uint8ClampedArray(pixels.length * 4);
  pixels.forEach((p, i) => {
    data[i * 4] = p[0];
    data[i * 4 + 1] = p[1];
    data[i * 4 + 2] = p[2];
    data[i * 4 + 3] = p[3];
  });
  return { data, width: pixels.length, height: 1 } as unknown as ImageData;
}

describe('isColorGlyphBitmap', () => {
  it('treats neutral (r===g===b) opaque pixels as monochrome', () => {
    expect(isColorGlyphBitmap(img([[255, 255, 255, 255], [0, 0, 0, 255], [128, 128, 128, 255]]))).toBe(false);
  });

  it('ignores fully transparent chromatic pixels (antialiased emoji border)', () => {
    expect(isColorGlyphBitmap(img([[255, 0, 0, 0], [0, 255, 0, 0]]))).toBe(false);
  });

  it('is exclusive at the threshold: a channel spread of exactly 12 stays monochrome', () => {
    // |112 - 100| === 12, which is NOT > 12.
    expect(isColorGlyphBitmap(img([[112, 100, 100, 255]]))).toBe(false);
  });

  it('flags an opaque pixel whose channel spread exceeds the threshold', () => {
    // |113 - 100| === 13 > 12.
    expect(isColorGlyphBitmap(img([[113, 100, 100, 255]]))).toBe(true);
  });
});
