// Keeping the annotation surface inside the window.
//
// Both the popup and the panel are positioned in viewport coordinates, so
// nothing in the layout stops them from being placed half off-screen: the popup
// follows the pointer, which is often near an edge, and the panel follows a
// drag. Placement is pure arithmetic over measured sizes, kept here so it can be
// tested without a browser.

export interface Size {
  width: number;
  height: number;
}

export interface Point {
  x: number;
  y: number;
}

export interface Placement {
  left: number;
  top: number;
}

// Breathing room against the window edge.
const MARGIN = 8;
// Distance between the anchor point and the popup, so the popup never sits on
// the words it is about to describe.
const GAP = 10;

function clamp(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), Math.max(min, max));
}

// Fits a box fully inside the viewport, preferring the requested position.
export function clampToViewport(at: Placement, size: Size, viewport: Size): Placement {
  return {
    left: clamp(at.left, MARGIN, viewport.width - size.width - MARGIN),
    top: clamp(at.top, MARGIN, viewport.height - size.height - MARGIN),
  };
}

// Where to draw a popup anchored to a point on the grid.
//
// Above the anchor and centred on it by default: the anchor is the text being
// annotated, and covering it defeats the point. Flips below only when there is
// no room above, and falls back to vertical centring when the popup is taller
// than either side of the anchor. Horizontally it is always clamped, which is
// what stops a highlight near the right edge from opening a popup that runs off
// the window.
export function placePopup(at: Point, size: Size, viewport: Size): Placement {
  const left = clamp(
    at.x - size.width / 2,
    MARGIN,
    viewport.width - size.width - MARGIN,
  );
  const above = at.y - size.height - GAP;
  if (above >= MARGIN) return { left, top: above };
  const below = at.y + GAP;
  if (below + size.height + MARGIN <= viewport.height) return { left, top: below };
  return clampToViewport({ left, top: at.y - size.height / 2 }, size, viewport);
}
