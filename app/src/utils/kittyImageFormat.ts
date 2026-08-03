// Pixel layouts of a stored kitty image, as the daemon names and codes them.
//
// One table indexed by the wire code, mirroring kittyImageFormatNames in
// internal/protocol/binaryframe.go: the byte in a binary 0x02 frame and the
// string in a kitty_image_result come from the same array there, so they must
// come from the same array here. PNG is absent by construction — ghostty
// decodes PNG and inflates zlib before storing, so a stored image is always one
// of these four raw layouts.

export type KittyPixelFormat = 'rgb' | 'rgba' | 'gray_alpha' | 'gray';

const FORMATS_BY_CODE: readonly KittyPixelFormat[] = ['rgb', 'rgba', 'gray_alpha', 'gray'];

/** Bytes per pixel, so a blob can be checked against the size its header claims. */
export const KITTY_FORMAT_BYTES_PER_PIXEL: Readonly<Record<KittyPixelFormat, number>> = {
  rgb: 3,
  rgba: 4,
  gray_alpha: 2,
  gray: 1,
};

/** The layout a binary frame's format byte names, or null for a code we cannot draw. */
export function kittyPixelFormatFromCode(code: number): KittyPixelFormat | null {
  return FORMATS_BY_CODE[code] ?? null;
}

/** The layout a kitty_image_result's `format` string names, or null if unknown. */
export function kittyPixelFormatFromName(name: string): KittyPixelFormat | null {
  return (FORMATS_BY_CODE as readonly string[]).includes(name)
    ? (name as KittyPixelFormat)
    : null;
}
