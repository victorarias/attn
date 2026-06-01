import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { startLeafDrag, type LeafDragHandlers } from './leafDrag';
import type { DockTarget } from './dockTarget';
import type { NormalizedPaneBounds } from '../../types/workspace';

// These tests drive the real pointerdown → pointermove → pointerup wiring with
// synthetic events against a mocked 1000x1000 container, instead of a real OS
// drag. The pure geometry (computeDockTarget) is covered in dockTarget.test.ts;
// here we verify the gesture state machine: window listeners fire the preview,
// the drop reports the final target, and teardown unbinds everything.

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

// A 1000x1000 container at the origin so clientX/Y map 1:1 to normalized * 1000.
function makeContainer(): HTMLElement {
  const el = document.createElement('div');
  el.className = 'session-terminal-panes';
  el.getBoundingClientRect = () =>
    ({ left: 0, top: 0, right: 1000, bottom: 1000, width: 1000, height: 1000, x: 0, y: 0, toJSON() {} }) as DOMRect;
  document.body.appendChild(el);
  return el;
}

function pointer(type: string, clientX: number, clientY: number): Event {
  // happy-dom dispatches by event-type string, so a MouseEvent carrying
  // clientX/clientY triggers the 'pointermove'/'pointerup' listeners.
  return new MouseEvent(type, { clientX, clientY, bubbles: true });
}

function spyHandlers(): LeafDragHandlers & {
  previews: Array<DockTarget | null>;
  drops: Array<{ leafId: string; target: DockTarget }>;
  cleanups: number;
} {
  const previews: Array<DockTarget | null> = [];
  const drops: Array<{ leafId: string; target: DockTarget }> = [];
  let cleanups = 0;
  return {
    previews,
    drops,
    get cleanups() {
      return cleanups;
    },
    onPreview: (t) => previews.push(t),
    onGhostMove: () => {},
    onDrop: (leafId, target) => drops.push({ leafId, target }),
    onCleanup: () => {
      cleanups += 1;
    },
  };
}

describe('startLeafDrag — synthesized pointer gesture', () => {
  let container: HTMLElement;

  beforeEach(() => {
    container = makeContainer();
  });

  afterEach(() => {
    container.remove();
    vi.restoreAllMocks();
  });

  it('previews a leaf edge on move and drops there on pointerup', () => {
    // Two side-by-side panes. Drag A; hover over B near its right edge.
    const paneBounds = new Map<string, NormalizedPaneBounds>([
      ['A', bounds(0, 0, 0.5, 1)],
      ['B', bounds(0.5, 0, 1, 1)],
    ]);
    const h = spyHandlers();

    startLeafDrag('A', 250, 500, container, paneBounds, h);

    window.dispatchEvent(pointer('pointermove', 900, 500));
    const preview = h.previews[h.previews.length - 1];
    expect(preview).toMatchObject({ anchorId: 'B', edge: 'right' });
    expect(preview!.ratio).toBeCloseTo(0.2, 5);

    window.dispatchEvent(pointer('pointerup', 900, 500));
    expect(h.drops).toHaveLength(1);
    expect(h.drops[0].leafId).toBe('A');
    expect(h.drops[0].target).toMatchObject({ anchorId: 'B', edge: 'right' });
    expect(h.cleanups).toBe(1);
  });

  it('unbinds listeners after drop so later moves do nothing', () => {
    const paneBounds = new Map<string, NormalizedPaneBounds>([
      ['A', bounds(0, 0, 0.5, 1)],
      ['B', bounds(0.5, 0, 1, 1)],
    ]);
    const h = spyHandlers();

    startLeafDrag('A', 250, 500, container, paneBounds, h);
    window.dispatchEvent(pointer('pointerup', 900, 500));
    const previewsAfterDrop = h.previews.length;

    window.dispatchEvent(pointer('pointermove', 700, 500));
    expect(h.previews.length).toBe(previewsAfterDrop);
  });

  it('hovering the dragged leaf itself previews nothing and drops nowhere (self-drop)', () => {
    const paneBounds = new Map<string, NormalizedPaneBounds>([
      ['A', bounds(0, 0, 0.5, 1)],
      ['B', bounds(0.5, 0, 1, 1)],
    ]);
    const h = spyHandlers();

    startLeafDrag('A', 250, 500, container, paneBounds, h);
    // Pointer stays over A's own area; A is excluded from drop targets and the
    // left side has only one leaf, so no container edge either.
    window.dispatchEvent(pointer('pointermove', 100, 500));
    expect(h.previews[h.previews.length - 1]).toBeNull();

    window.dispatchEvent(pointer('pointerup', 100, 500));
    expect(h.drops).toHaveLength(0);
    expect(h.cleanups).toBe(1);
  });

  it('docks against the whole workspace when a side holds 2+ leaves', () => {
    // Left column has two stacked panes; drag the right pane to the left frame.
    const paneBounds = new Map<string, NormalizedPaneBounds>([
      ['A', bounds(0, 0, 0.5, 0.5)],
      ['B', bounds(0, 0.5, 0.5, 1)],
      ['C', bounds(0.5, 0, 1, 1)],
    ]);
    const h = spyHandlers();

    startLeafDrag('C', 750, 500, container, paneBounds, h);
    window.dispatchEvent(pointer('pointermove', 10, 500));
    expect(h.previews[h.previews.length - 1]).toMatchObject({ anchorId: '', edge: 'left', ratio: 0.5 });

    window.dispatchEvent(pointer('pointerup', 10, 500));
    expect(h.drops).toHaveLength(1);
    expect(h.drops[0].target).toMatchObject({ anchorId: '', edge: 'left' });
  });

  it('pointercancel ends the gesture without a drop', () => {
    const paneBounds = new Map<string, NormalizedPaneBounds>([
      ['A', bounds(0, 0, 0.5, 1)],
      ['B', bounds(0.5, 0, 1, 1)],
    ]);
    const h = spyHandlers();

    startLeafDrag('A', 250, 500, container, paneBounds, h);
    window.dispatchEvent(pointer('pointermove', 900, 500));
    window.dispatchEvent(new Event('pointercancel'));

    expect(h.drops).toHaveLength(0);
    expect(h.cleanups).toBe(1);

    // Listeners are gone: a subsequent up does nothing.
    window.dispatchEvent(pointer('pointerup', 900, 500));
    expect(h.drops).toHaveLength(0);
  });
});
