import type { TerminalDockEdge } from '../../types/workspace';
import type { NormalizedPaneBounds } from '../../types/workspace';

// Drag-to-dock geometry: pure functions that turn a pointer position into a drop
// target (which leaf or the whole workspace, which edge, and how big). Kept out
// of the component so the math is unit-testable in isolation.

// Drop sizing: how deep the pointer sits in the target leaf sets the incoming
// leaf's fraction — thin at the edge (DOCK_F_MIN), up to half at the center —
// magnetically snapping to clean splits when close.
const DOCK_F_MIN = 0.15;
const DOCK_F_MAX = 0.5;
const DOCK_SNAP_POINTS = [0.25, 1 / 3, 0.5];
const DOCK_SNAP_TOLERANCE = 0.045;
// A thin perimeter band docks against the whole workspace (the root) instead of
// one leaf. It splits the workspace in half; refine with the divider afterward.
const DOCK_CONTAINER_FRAME_PX = 28;
const DOCK_CONTAINER_FRACTION = 0.5;
// A leaf counts as touching a container edge within this normalized slack.
const DOCK_EDGE_EPSILON = 0.01;

export type DockEdgeFlags = Record<TerminalDockEdge, boolean>;

export interface DockNormalizedRect {
  left: number;
  top: number;
  width: number;
  height: number;
}

export interface DockTarget {
  // The leaf the incoming leaf docks beside, or '' to dock against the whole
  // workspace (a container dock).
  anchorId: string;
  edge: TerminalDockEdge;
  // The incoming leaf's fraction of the new split (snapped).
  ratio: number;
  rect: DockNormalizedRect;
}

function snapDockFraction(fraction: number): number {
  for (const point of DOCK_SNAP_POINTS) {
    if (Math.abs(fraction - point) <= DOCK_SNAP_TOLERANCE) {
      return point;
    }
  }
  return fraction;
}

function leafBandRect(bounds: NormalizedPaneBounds, edge: TerminalDockEdge, fraction: number): DockNormalizedRect {
  const bandW = bounds.width * fraction;
  const bandH = bounds.height * fraction;
  switch (edge) {
    case 'left':
      return { left: bounds.left, top: bounds.top, width: bandW, height: bounds.height };
    case 'right':
      return { left: bounds.right - bandW, top: bounds.top, width: bandW, height: bounds.height };
    case 'top':
      return { left: bounds.left, top: bounds.top, width: bounds.width, height: bandH };
    default:
      return { left: bounds.left, top: bounds.bottom - bandH, width: bounds.width, height: bandH };
  }
}

function containerBandRect(edge: TerminalDockEdge, fraction: number): DockNormalizedRect {
  switch (edge) {
    case 'left':
      return { left: 0, top: 0, width: fraction, height: 1 };
    case 'right':
      return { left: 1 - fraction, top: 0, width: fraction, height: 1 };
    case 'top':
      return { left: 0, top: 0, width: 1, height: fraction };
    default:
      return { left: 0, top: 1 - fraction, width: 1, height: fraction };
  }
}

// computeContainerSides reports which workspace edges hold 2+ leaves, so a
// container dock is only offered where it would differ from the leaf edge
// already on that border (a single full-span leaf makes them coincide).
export function computeContainerSides(boundsList: NormalizedPaneBounds[]): DockEdgeFlags {
  let left = 0;
  let right = 0;
  let top = 0;
  let bottom = 0;
  for (const bounds of boundsList) {
    if (bounds.left <= DOCK_EDGE_EPSILON) left += 1;
    if (bounds.right >= 1 - DOCK_EDGE_EPSILON) right += 1;
    if (bounds.top <= DOCK_EDGE_EPSILON) top += 1;
    if (bounds.bottom >= 1 - DOCK_EDGE_EPSILON) bottom += 1;
  }
  return { left: left >= 2, right: right >= 2, top: top >= 2, bottom: bottom >= 2 };
}

function containerDockTarget(nx: number, ny: number, frameX: number, frameY: number, sides: DockEdgeFlags): DockTarget | null {
  const candidates: Array<{ edge: TerminalDockEdge; dist: number }> = [];
  if (sides.left && nx <= frameX) candidates.push({ edge: 'left', dist: nx });
  if (sides.right && 1 - nx <= frameX) candidates.push({ edge: 'right', dist: 1 - nx });
  if (sides.top && ny <= frameY) candidates.push({ edge: 'top', dist: ny });
  if (sides.bottom && 1 - ny <= frameY) candidates.push({ edge: 'bottom', dist: 1 - ny });
  if (candidates.length === 0) {
    return null;
  }
  candidates.sort((a, b) => a.dist - b.dist);
  const edge = candidates[0].edge;
  return {
    anchorId: '',
    edge,
    ratio: DOCK_CONTAINER_FRACTION,
    rect: containerBandRect(edge, DOCK_CONTAINER_FRACTION),
  };
}

// computeDockTarget maps a pointer position to a drop target: a container dock
// when the pointer is in the workspace's perimeter frame (on a side that holds
// 2+ leaves), otherwise the leaf it's over plus the nearest edge (X-pattern),
// sized by how deep the pointer sits. The dragged leaf is excluded from
// leafRects, so hovering it returns null — a self-drop no-op.
export function computeDockTarget(
  clientX: number,
  clientY: number,
  containerRect: { left: number; top: number; width: number; height: number },
  leafRects: Array<{ leafId: string; bounds: NormalizedPaneBounds }>,
  containerSides: DockEdgeFlags,
): DockTarget | null {
  if (containerRect.width <= 0 || containerRect.height <= 0) {
    return null;
  }
  const nx = (clientX - containerRect.left) / containerRect.width;
  const ny = (clientY - containerRect.top) / containerRect.height;

  if (nx >= 0 && nx <= 1 && ny >= 0 && ny <= 1) {
    const container = containerDockTarget(
      nx,
      ny,
      DOCK_CONTAINER_FRAME_PX / containerRect.width,
      DOCK_CONTAINER_FRAME_PX / containerRect.height,
      containerSides,
    );
    if (container) {
      return container;
    }
  }

  for (const { leafId, bounds } of leafRects) {
    if (nx < bounds.left || nx > bounds.right || ny < bounds.top || ny > bounds.bottom) {
      continue;
    }
    const px = (nx - bounds.left) / Math.max(bounds.width, 1e-6);
    const py = (ny - bounds.top) / Math.max(bounds.height, 1e-6);
    const distances: Array<{ edge: TerminalDockEdge; dist: number }> = [
      { edge: 'left', dist: px },
      { edge: 'right', dist: 1 - px },
      { edge: 'top', dist: py },
      { edge: 'bottom', dist: 1 - py },
    ];
    distances.sort((a, b) => a.dist - b.dist);
    const { edge, dist } = distances[0];
    const fraction = snapDockFraction(Math.min(Math.max(dist, DOCK_F_MIN), DOCK_F_MAX));
    return { anchorId: leafId, edge, ratio: fraction, rect: leafBandRect(bounds, edge, fraction) };
  }
  return null;
}
