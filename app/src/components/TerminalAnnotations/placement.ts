// Keeping the annotation surface inside the window.
//
// Both the popup and the panel are positioned in viewport coordinates, so
// nothing in the layout stops them from being placed half off-screen: the popup
// follows the pointer, which is often near an edge, and the panel follows a
// drag. Placement is pure arithmetic over measured sizes, kept here so it can be
// tested without a browser.
//
// Fitting inside the window is necessary and not sufficient. A popup anchored
// near the left column of a pane fits the window perfectly while sitting on top
// of the sidebar, and one anchored near the right column lands on the
// annotation panel. So placement takes two more inputs than a viewport: the
// region the popup belongs in (the pane whose text it describes) and a surface
// it must not cover.

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

export interface Rect {
  left: number;
  top: number;
  width: number;
  height: number;
}

export interface PlaceOptions {
  // Where the popup belongs: the rect of the terminal grid it is annotating.
  // Preferred, not obeyed — a pane too narrow to hold the popup falls back to
  // the window, because a popup that cannot fit its pane still has to be
  // usable.
  bounds?: Rect | null;
  // A surface the popup must not cover, in viewport coordinates. The annotation
  // panel: it lists the marks the popup is editing, and a popup that lands on
  // it hides the thing being worked on.
  avoid?: Rect | null;
}

// Breathing room against the region edge.
const MARGIN = 8;
// Distance between the anchor point and the popup, so the popup never sits on
// the words it is about to describe. Also the clearance kept from an avoided
// surface, so "not covering it" reads as deliberate rather than as a near miss.
const GAP = 10;

function clamp(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), Math.max(min, max));
}

function sizeToRect(size: Size): Rect {
  return { left: 0, top: 0, width: size.width, height: size.height };
}

// Fits a box fully inside a rect, preferring the requested position.
function clampToRect(at: Placement, size: Size, region: Rect): Placement {
  return {
    left: clamp(at.left, region.left + MARGIN, region.left + region.width - size.width - MARGIN),
    top: clamp(at.top, region.top + MARGIN, region.top + region.height - size.height - MARGIN),
  };
}

// Fits a box fully inside the viewport, preferring the requested position.
export function clampToViewport(at: Placement, size: Size, viewport: Size): Placement {
  return clampToRect(at, size, sizeToRect(viewport));
}

// The region a box may occupy. The preferred rect when the box fits inside it
// with its margins, the whole window otherwise — a pane narrower than the popup
// is a reason to spill out of the pane, never a reason to be unreachable.
function regionFor(size: Size, viewport: Size, preferred?: Rect | null): Rect {
  if (!preferred) return sizeToRect(viewport);
  const fits = preferred.width >= size.width + MARGIN * 2
    && preferred.height >= size.height + MARGIN * 2;
  return fits ? preferred : sizeToRect(viewport);
}

function overlaps(at: Placement, size: Size, other: Rect): boolean {
  return at.left < other.left + other.width
    && at.left + size.width > other.left
    && at.top < other.top + other.height
    && at.top + size.height > other.top;
}

function insideRect(at: Placement, size: Size, region: Rect): boolean {
  return at.left >= region.left + MARGIN
    && at.top >= region.top + MARGIN
    && at.left + size.width <= region.left + region.width - MARGIN
    && at.top + size.height <= region.top + region.height - MARGIN;
}

// Steps a placement off a surface it lands on, to the nearest side that clears
// it and still fits the region.
//
// When no side does, the requested placement stands: the popup is what the user
// just opened and the panel is a list they can scroll to afterwards, so in the
// one case where something has to be covered, it is the panel.
function stepOff(at: Placement, size: Size, region: Rect, avoid?: Rect | null): Placement {
  if (!avoid || !overlaps(at, size, avoid)) return at;
  const candidates: Placement[] = [
    { left: avoid.left - size.width - GAP, top: at.top },
    { left: avoid.left + avoid.width + GAP, top: at.top },
    { left: at.left, top: avoid.top - size.height - GAP },
    { left: at.left, top: avoid.top + avoid.height + GAP },
  ];
  const moved = candidates
    .filter((candidate) => insideRect(candidate, size, region) && !overlaps(candidate, size, avoid))
    .sort((a, b) => (
      Math.abs(a.left - at.left) + Math.abs(a.top - at.top)
      - (Math.abs(b.left - at.left) + Math.abs(b.top - at.top))
    ));
  return moved[0] ?? at;
}

// Where to draw a popup anchored to a point on the grid.
//
// Above the anchor and centred on it by default: the anchor is the text being
// annotated, and covering it defeats the point. Flips below only when there is
// no room above, and falls back to vertical centring when the popup is taller
// than either side of the anchor. Horizontally it is always clamped, which is
// what stops a highlight near the right edge from opening a popup that runs off
// the region.
export function placePopup(
  at: Point,
  size: Size,
  viewport: Size,
  options: PlaceOptions = {},
): Placement {
  const region = regionFor(size, viewport, options.bounds);
  const left = clamp(
    at.x - size.width / 2,
    region.left + MARGIN,
    region.left + region.width - size.width - MARGIN,
  );
  const above = at.y - size.height - GAP;
  if (above >= region.top + MARGIN) {
    return stepOff({ left, top: above }, size, region, options.avoid);
  }
  const below = at.y + GAP;
  if (below + size.height + MARGIN <= region.top + region.height) {
    return stepOff({ left, top: below }, size, region, options.avoid);
  }
  return stepOff(
    clampToRect({ left, top: at.y - size.height / 2 }, size, region),
    size,
    region,
    options.avoid,
  );
}
