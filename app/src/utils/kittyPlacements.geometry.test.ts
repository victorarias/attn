// Where a placement lands on screen, and which part of its texture that shows.
//
// These are the two numbers a wrong image is made of: a quad at the wrong place
// draws over the wrong text, and a source rect that does not track the clip
// squashes the image instead of cropping it. Both are pure functions so they can
// be checked without a GL context — the actual upload and draw are not unit
// scope, and pretending otherwise with a mocked context would test the mock.

import { describe, expect, it } from 'vitest';
import { placementQuad, placementSourceRect, type PlacedKittyImage } from './kittyPlacements';

const CELL_W = 10;
const CELL_H = 20;
const GRID_W = 800; // 80 cols
const GRID_H = 480; // 24 rows
// A 1x display: the placement's device pixels and the grid's CSS pixels agree,
// so these cases read as pure geometry. The dPR=2 case below is where they part.
const DPR = 1;

function placed(overrides: Partial<PlacedKittyImage> = {}): PlacedKittyImage {
  return {
    imageId: 1,
    placementId: 1,
    generation: 5,
    z: 0,
    bufferRow: 100,
    col: 0,
    pixelWidth: 100,
    pixelHeight: 80,
    sourceX: 0,
    sourceY: 0,
    sourceWidth: 0,
    sourceHeight: 0,
    ...overrides,
  };
}

describe('placementQuad', () => {
  it('places a fully visible image at its cell origin, at its rendered size', () => {
    const quad = placementQuad(placed({ bufferRow: 103, col: 4 }), 100, CELL_W, CELL_H, GRID_W, GRID_H, DPR);

    expect(quad).toMatchObject({ x: 40, y: 60, width: 100, height: 80 });
    expect(quad).toMatchObject({ u0: 0, v0: 0, u1: 1, v1: 1 });
  });

  it('clips an image that starts above the viewport, and crops rather than squashes', () => {
    // Two rows (40px) of an 80px-tall image are above the top edge.
    const quad = placementQuad(placed({ bufferRow: 98 }), 100, CELL_W, CELL_H, GRID_W, GRID_H, DPR);

    expect(quad).toMatchObject({ y: 0, height: 40 });
    expect(quad?.v0).toBeCloseTo(0.5);
    expect(quad?.v1).toBe(1);
  });

  it('clips an image that runs past the right edge of the grid', () => {
    // Starts 60px from the right edge with a 100px-wide image.
    const quad = placementQuad(placed({ bufferRow: 100, col: 74 }), 100, CELL_W, CELL_H, GRID_W, GRID_H, DPR);

    expect(quad).toMatchObject({ x: 740, width: 60 });
    expect(quad?.u0).toBe(0);
    expect(quad?.u1).toBeCloseTo(0.6);
  });

  it('clips an image that runs past the bottom of the grid', () => {
    const quad = placementQuad(placed({ bufferRow: 123 }), 100, CELL_W, CELL_H, GRID_W, GRID_H, DPR);

    expect(quad).toMatchObject({ y: 460, height: 20 });
    expect(quad?.v1).toBeCloseTo(0.25);
  });

  it('reports no quad for an image scrolled entirely out of view', () => {
    expect(placementQuad(placed({ bufferRow: 95 }), 100, CELL_W, CELL_H, GRID_W, GRID_H, DPR)).toBeNull();
    expect(placementQuad(placed({ bufferRow: 124 }), 100, CELL_W, CELL_H, GRID_W, GRID_H, DPR)).toBeNull();
  });

  it('keeps an image that only just pokes into the viewport', () => {
    // Its last 20 pixels sit on the first visible row.
    const quad = placementQuad(placed({ bufferRow: 97 }), 100, CELL_W, CELL_H, GRID_W, GRID_H, DPR);

    expect(quad).toMatchObject({ y: 0, height: 20 });
    expect(quad?.v0).toBeCloseTo(0.75);
  });

  it('draws a retina placement at half its device-pixel size', () => {
    // The emitter sized this image against the pane's DEVICE pixels, and the
    // quad is consumed in CSS pixels. Drawn without the division a 100x80 image
    // covers 10 columns and 4 rows instead of 5 and 2 — the "twice as large as
    // intended" symptom that motivated reporting pixel geometry at all.
    const quad = placementQuad(placed({ bufferRow: 103, col: 4 }), 100, CELL_W, CELL_H, GRID_W, GRID_H, 2);

    expect(quad).toMatchObject({ x: 40, y: 60, width: 50, height: 40 });
    // Nothing was clipped, so the whole texture still shows.
    expect(quad).toMatchObject({ u0: 0, v0: 0, u1: 1, v1: 1 });
  });

  it('clips a retina placement against the grid in CSS pixels', () => {
    // 50 CSS px wide starting 30 CSS px from the right edge: the clip has to
    // measure the halved box, not the device-pixel one.
    const quad = placementQuad(placed({ bufferRow: 100, col: 77 }), 100, CELL_W, CELL_H, GRID_W, GRID_H, 2);

    expect(quad).toMatchObject({ x: 770, width: 30 });
    expect(quad?.u1).toBeCloseTo(0.6);
  });
});

describe('placementSourceRect', () => {
  it('samples the whole texture when the placement names no source rect', () => {
    const quad = placementQuad(placed(), 100, CELL_W, CELL_H, GRID_W, GRID_H, DPR)!;

    expect(placementSourceRect(quad, 200, 160)).toEqual({ x: 0, y: 0, width: 200, height: 160 });
  });

  it('samples the sub-rect a placement names', () => {
    const quad = placementQuad(
      placed({ sourceX: 10, sourceY: 20, sourceWidth: 50, sourceHeight: 40 }),
      100, CELL_W, CELL_H, GRID_W, GRID_H, DPR,
    )!;

    expect(placementSourceRect(quad, 200, 160)).toEqual({ x: 10, y: 20, width: 50, height: 40 });
  });

  it('crops the named sub-rect when the quad was clipped', () => {
    // Half the image is above the viewport, so half of its source rect is too.
    const quad = placementQuad(
      placed({ bufferRow: 98, sourceX: 10, sourceY: 20, sourceWidth: 50, sourceHeight: 40 }),
      100, CELL_W, CELL_H, GRID_W, GRID_H, DPR,
    )!;

    const rect = placementSourceRect(quad, 200, 160);
    expect(rect.x).toBe(10);
    expect(rect.width).toBe(50);
    expect(rect.y).toBeCloseTo(40);
    expect(rect.height).toBeCloseTo(20);
  });

  it('crops the whole texture when a clipped placement names no source rect', () => {
    const quad = placementQuad(placed({ bufferRow: 98 }), 100, CELL_W, CELL_H, GRID_W, GRID_H, DPR)!;

    const rect = placementSourceRect(quad, 200, 160);
    expect(rect.y).toBeCloseTo(80);
    expect(rect.height).toBeCloseTo(80);
  });
});
