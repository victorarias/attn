// Keeping the annotation surface inside the window: pure arithmetic over
// measured sizes. Fitting the viewport is not enough, so placement also takes
// the region the popup belongs in and a surface it must not cover.

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
  // The annotated grid's rect. Preferred, not obeyed: a pane too narrow for
  // the popup falls back to the window.
  bounds?: Rect | null;
  // A surface the popup must not cover, in viewport coordinates.
  avoid?: Rect | null;
}

// Breathing room against the region edge.
const MARGIN = 8;
// Clearance from the anchor point, and from an avoided surface.
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

// The region a box may occupy: the preferred rect when the box fits it with
// margins, the whole window otherwise.
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
// it and still fits the region. When no side does, the request stands — the
// covered panel is the lesser loss.
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

// Where to draw a popup anchored to a point on the grid: above and centred by
// default (covering the annotated text defeats the point), below when there is
// no room above, vertically centred when neither side fits.
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
