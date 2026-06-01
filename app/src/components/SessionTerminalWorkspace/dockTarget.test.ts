import { describe, it, expect } from 'vitest';
import { computeDockTarget, computeContainerSides, type DockEdgeFlags } from './dockTarget';
import type { NormalizedPaneBounds } from '../../types/workspace';

function bounds(left: number, top: number, right: number, bottom: number): NormalizedPaneBounds {
  return {
    left,
    top,
    right,
    bottom,
    width: right - left,
    height: bottom - top,
    centerX: (left + right) / 2,
    centerY: (top + bottom) / 2,
  };
}

// 1000x1000 container so clientX/Y map 1:1 to normalized * 1000.
const RECT = { left: 0, top: 0, width: 1000, height: 1000 };
const NO_SIDES: DockEdgeFlags = { left: false, right: false, top: false, bottom: false };
const FULL = bounds(0, 0, 1, 1);

describe('computeDockTarget — depth sizes the incoming leaf', () => {
  const leaf = [{ leafId: 'B', bounds: FULL }];

  it('is a thin strip near the edge', () => {
    const t = computeDockTarget(150, 500, RECT, leaf, NO_SIDES);
    expect(t).toMatchObject({ anchorId: 'B', edge: 'left' });
    expect(t!.ratio).toBeCloseTo(0.15, 5);
    expect(t!.rect).toMatchObject({ left: 0, top: 0, height: 1 });
    expect(t!.rect.width).toBeCloseTo(0.15, 5);
  });

  it('snaps to one third', () => {
    const t = computeDockTarget(340, 500, RECT, leaf, NO_SIDES);
    expect(t!.edge).toBe('left');
    expect(t!.ratio).toBeCloseTo(1 / 3, 5);
  });

  it('snaps to half near the center', () => {
    const t = computeDockTarget(480, 500, RECT, leaf, NO_SIDES);
    expect(t!.ratio).toBeCloseTo(0.5, 5);
  });

  it('picks the nearest edge via the X-pattern', () => {
    expect(computeDockTarget(500, 120, RECT, leaf, NO_SIDES)!.edge).toBe('top');
    expect(computeDockTarget(500, 880, RECT, leaf, NO_SIDES)!.edge).toBe('bottom');
    expect(computeDockTarget(880, 500, RECT, leaf, NO_SIDES)!.edge).toBe('right');
  });
});

describe('computeDockTarget — no target', () => {
  it('returns null when the only leaf is excluded (self-drop)', () => {
    expect(computeDockTarget(500, 500, RECT, [], NO_SIDES)).toBeNull();
  });

  it('returns null for a degenerate container', () => {
    expect(computeDockTarget(5, 5, { left: 0, top: 0, width: 0, height: 0 }, [{ leafId: 'B', bounds: FULL }], NO_SIDES)).toBeNull();
  });
});

describe('computeDockTarget — container frame', () => {
  const leaf = [{ leafId: 'B', bounds: FULL }];

  it('docks against the whole workspace in the perimeter band when the side has 2+ leaves', () => {
    const t = computeDockTarget(8, 500, RECT, leaf, { ...NO_SIDES, left: true });
    expect(t).toMatchObject({ anchorId: '', edge: 'left', ratio: 0.5 });
    expect(t!.rect).toMatchObject({ left: 0, top: 0, width: 0.5, height: 1 });
  });

  it('is suppressed on a side with a single full-span leaf — falls through to the leaf edge', () => {
    const t = computeDockTarget(8, 500, RECT, leaf, NO_SIDES);
    expect(t!.anchorId).toBe('B');
    expect(t!.edge).toBe('left');
  });
});

describe('computeContainerSides', () => {
  it('flags a side only when 2+ leaves touch it', () => {
    const sides = computeContainerSides([
      bounds(0, 0, 0.5, 0.5),
      bounds(0, 0.5, 0.5, 1),
      bounds(0.5, 0, 1, 1),
    ]);
    expect(sides).toEqual({ left: true, right: false, top: true, bottom: true });
  });

  it('flags nothing for a single full-span leaf', () => {
    expect(computeContainerSides([FULL])).toEqual({ left: false, right: false, top: false, bottom: false });
  });
});
