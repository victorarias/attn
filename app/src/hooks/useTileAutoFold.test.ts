import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { foldsForWidth, RAIL_FOLD_BELOW, TREE_FOLD_BELOW, useTileAutoFold } from './useTileAutoFold';

// A div whose clientWidth we control (happy-dom does no layout, so it's 0 otherwise).
function elementOfWidth(width: number): HTMLElement {
  const el = document.createElement('div');
  Object.defineProperty(el, 'clientWidth', { value: width, configurable: true });
  return el;
}

// Capture the latest ResizeObserver callback so a test can drive a resize.
let resizeCallback: ResizeObserverCallback | null = null;
class MockResizeObserver {
  constructor(cb: ResizeObserverCallback) { resizeCallback = cb; }
  observe() {}
  unobserve() {}
  disconnect() {}
}
function emitWidth(width: number) {
  act(() => {
    resizeCallback?.([{ contentRect: { width } } as ResizeObserverEntry], {} as ResizeObserver);
  });
}

const RealResizeObserver = globalThis.ResizeObserver;
beforeEach(() => {
  resizeCallback = null;
  globalThis.ResizeObserver = MockResizeObserver as unknown as typeof ResizeObserver;
});
afterEach(() => {
  globalThis.ResizeObserver = RealResizeObserver;
});

describe('foldsForWidth', () => {
  it('folds nothing at a wide width', () => {
    expect(foldsForWidth(RAIL_FOLD_BELOW)).toEqual({ treeAutoFold: false, railAutoFold: false });
    expect(foldsForWidth(1200)).toEqual({ treeAutoFold: false, railAutoFold: false });
  });

  it('folds the rail first as it narrows past the wide threshold', () => {
    expect(foldsForWidth(RAIL_FOLD_BELOW - 1)).toEqual({ treeAutoFold: false, railAutoFold: true });
    expect(foldsForWidth(TREE_FOLD_BELOW)).toEqual({ treeAutoFold: false, railAutoFold: true });
  });

  it('folds both rail and tree once narrow', () => {
    expect(foldsForWidth(TREE_FOLD_BELOW - 1)).toEqual({ treeAutoFold: true, railAutoFold: true });
    expect(foldsForWidth(320)).toEqual({ treeAutoFold: true, railAutoFold: true });
  });

  it('treats a non-positive (unmeasured) width as fold-nothing, so a tile never flashes collapsed', () => {
    expect(foldsForWidth(0)).toEqual({ treeAutoFold: false, railAutoFold: false });
    expect(foldsForWidth(-10)).toEqual({ treeAutoFold: false, railAutoFold: false });
  });
});

describe('useTileAutoFold', () => {
  it('stays at the manual-only baseline when disabled (the modal), never observing', () => {
    const ref = { current: elementOfWidth(320) };
    const { result } = renderHook(() => useTileAutoFold(ref, false));
    expect(result.current).toEqual({ treeAutoFold: false, railAutoFold: false });
    expect(resizeCallback).toBeNull();
  });

  it('measures synchronously on mount so a narrow tile is folded on the first frame', () => {
    const ref = { current: elementOfWidth(TREE_FOLD_BELOW - 50) };
    const { result } = renderHook(() => useTileAutoFold(ref, true));
    expect(result.current).toEqual({ treeAutoFold: true, railAutoFold: true });
  });

  it('refolds across breakpoints as the observed width changes', () => {
    const ref = { current: elementOfWidth(1200) };
    const { result } = renderHook(() => useTileAutoFold(ref, true));
    expect(result.current).toEqual({ treeAutoFold: false, railAutoFold: false });

    emitWidth(RAIL_FOLD_BELOW - 100); // medium: rail folds, tree stays
    expect(result.current).toEqual({ treeAutoFold: false, railAutoFold: true });

    emitWidth(TREE_FOLD_BELOW - 100); // narrow: both fold
    expect(result.current).toEqual({ treeAutoFold: true, railAutoFold: true });

    emitWidth(1100); // back to wide: both unfold
    expect(result.current).toEqual({ treeAutoFold: false, railAutoFold: false });
  });
});
