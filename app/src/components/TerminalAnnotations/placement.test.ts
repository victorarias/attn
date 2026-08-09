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

// The pane the popup belongs to: a terminal beside a 240px sidebar. Fitting
// the window is what let a popup anchored near the pane's first column sit on
// top of that sidebar, where it is unreachable.
const PANE = { left: 240, top: 0, width: 960, height: 800 };

describe('placePopup within a pane', () => {
  it('keeps a popup anchored at the pane’s leading column inside the pane', () => {
    const at = placePopup({ x: PANE.left + 12, y: 400 }, POPUP, VIEWPORT, { bounds: PANE });

    expect(at.left).toBeGreaterThanOrEqual(PANE.left);
    expect(at.left + POPUP.width).toBeLessThanOrEqual(PANE.left + PANE.width);
  });

  it('keeps a popup anchored at the pane’s trailing column inside the pane', () => {
    const at = placePopup({ x: PANE.left + PANE.width - 4, y: 400 }, POPUP, VIEWPORT, { bounds: PANE });

    expect(at.left + POPUP.width).toBeLessThanOrEqual(PANE.left + PANE.width);
  });

  it('spills out of a pane too narrow to hold the popup, rather than off-screen', () => {
    // A three-way split leaves panes narrower than the popup. There is no
    // placement inside such a pane, and refusing to draw one would cost the
    // user the surface entirely — so the window becomes the region again.
    const narrow = { left: 240, top: 0, width: 200, height: 800 };
    const at = placePopup({ x: 300, y: 400 }, POPUP, VIEWPORT, { bounds: narrow });

    expect(at.left).toBeGreaterThanOrEqual(0);
    expect(at.left + POPUP.width).toBeLessThanOrEqual(VIEWPORT.width);
  });
});

describe('placePopup around the annotation panel', () => {
  // The panel's resting place: 300 wide, pinned to the window's bottom-right.
  const PANEL = { left: VIEWPORT.width - 320, top: VIEWPORT.height - 420, width: 300, height: 400 };

  it('steps off the panel rather than covering the list it is editing', () => {
    const at = placePopup(
      { x: VIEWPORT.width - 200, y: VIEWPORT.height - 200 },
      POPUP,
      VIEWPORT,
      { bounds: PANE, avoid: PANEL },
    );

    const covers = at.left < PANEL.left + PANEL.width
      && at.left + POPUP.width > PANEL.left
      && at.top < PANEL.top + PANEL.height
      && at.top + POPUP.height > PANEL.top;
    expect(covers).toBe(false);
  });

  it('leaves a popup that already clears the panel where it is', () => {
    const at = placePopup({ x: 600, y: 400 }, POPUP, VIEWPORT, { bounds: PANE, avoid: PANEL });

    expect(at).toEqual(placePopup({ x: 600, y: 400 }, POPUP, VIEWPORT, { bounds: PANE }));
  });

  it('covers the panel when nothing else fits, rather than leaving the region', () => {
    // A panel filling the pane leaves no clear side. The popup is what the
    // user just opened; the panel is a list they can scroll back to.
    const whole = { left: PANE.left, top: 0, width: PANE.width, height: PANE.height };
    const at = placePopup({ x: 700, y: 400 }, POPUP, VIEWPORT, { bounds: PANE, avoid: whole });

    expect(at.left).toBeGreaterThanOrEqual(PANE.left);
    expect(at.left + POPUP.width).toBeLessThanOrEqual(PANE.left + PANE.width);
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
