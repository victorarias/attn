// Fitting the annotation surface into the window. Both the popup and the panel
// are positioned in viewport coordinates, so nothing in the layout stops them
// from being placed half off-screen; these are the rules that do.

import { describe, expect, it } from 'vitest';
import { clampToViewport, placePopup } from './placement';

const VIEWPORT = { width: 1200, height: 800 };
const POPUP = { width: 320, height: 120 };

describe('placePopup', () => {
  it('sits above the anchor and centred on it when there is room', () => {
    const at = placePopup({ x: 600, y: 400 }, POPUP, VIEWPORT);

    expect(at.left).toBe(600 - POPUP.width / 2);
    expect(at.top).toBeLessThan(400);
    expect(at.top + POPUP.height).toBeLessThan(400);
  });

  it('keeps a popup anchored at the right edge fully on screen', () => {
    // A highlight in the last column would otherwise open a popup whose right
    // half is outside the window, with the labels on that half unreachable.
    const at = placePopup({ x: VIEWPORT.width - 4, y: 400 }, POPUP, VIEWPORT);

    expect(at.left + POPUP.width).toBeLessThanOrEqual(VIEWPORT.width);
    expect(at.left).toBeGreaterThanOrEqual(0);
  });

  it('keeps a popup anchored at the left edge fully on screen', () => {
    const at = placePopup({ x: 2, y: 400 }, POPUP, VIEWPORT);

    expect(at.left).toBeGreaterThanOrEqual(0);
  });

  it('flips below the anchor when the popup will not fit above it', () => {
    // Annotating the first line of the message puts the anchor near the top of
    // the window, where "above" is off-screen.
    const at = placePopup({ x: 600, y: 12 }, POPUP, VIEWPORT);

    expect(at.top).toBeGreaterThan(12);
    expect(at.top + POPUP.height).toBeLessThanOrEqual(VIEWPORT.height);
  });

  it('stays on screen when the popup fits neither above nor below', () => {
    // A tall composed popup in a short window: there is no good side, but there
    // is still a wrong answer, which is hanging off an edge.
    const tall = { width: 320, height: 300 };
    const at = placePopup({ x: 600, y: 180 }, tall, { width: 1200, height: 360 });

    expect(at.top).toBeGreaterThanOrEqual(0);
    expect(at.top + tall.height).toBeLessThanOrEqual(360);
  });
});

describe('clampToViewport', () => {
  it('leaves a box that already fits where it is', () => {
    expect(clampToViewport({ left: 300, top: 200 }, POPUP, VIEWPORT)).toEqual({ left: 300, top: 200 });
  });

  it('pulls a box dragged past an edge back into view', () => {
    const at = clampToViewport({ left: 1400, top: 900 }, POPUP, VIEWPORT);

    expect(at.left + POPUP.width).toBeLessThanOrEqual(VIEWPORT.width);
    expect(at.top + POPUP.height).toBeLessThanOrEqual(VIEWPORT.height);
  });

  it('pulls a box dragged past the top-left back into view', () => {
    const at = clampToViewport({ left: -200, top: -80 }, POPUP, VIEWPORT);

    expect(at.left).toBeGreaterThanOrEqual(0);
    expect(at.top).toBeGreaterThanOrEqual(0);
  });
});
