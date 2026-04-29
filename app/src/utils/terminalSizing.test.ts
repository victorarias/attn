import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { getScaledDimensions } from './terminalSizing';

interface FakeContainerOptions {
  width: number;
  height: number;
}

function createContainer({ width, height }: FakeContainerOptions): HTMLElement {
  const el = document.createElement('div');
  Object.defineProperty(el, 'getBoundingClientRect', {
    value: () => ({
      width,
      height,
      x: 0, y: 0, top: 0, left: 0, right: width, bottom: height, toJSON: () => ({}),
    }),
  });
  el.style.width = `${width}px`;
  el.style.height = `${height}px`;
  return el;
}

function createTermWithRendererCell(cellWidth: number, cellHeight: number) {
  const xtermElement = document.createElement('div');
  xtermElement.style.padding = '0px';
  document.body.appendChild(xtermElement);
  return {
    element: xtermElement,
    _core: {
      _renderService: {
        dimensions: {
          css: {
            cell: { width: cellWidth, height: cellHeight },
          },
        },
      },
    },
  } as any;
}

describe('getScaledDimensions', () => {
  beforeEach(() => {
    Object.defineProperty(window, 'devicePixelRatio', {
      configurable: true,
      get: () => 2,
    });
    vi.spyOn(window, 'getComputedStyle').mockImplementation((el: Element) => {
      const styled = el as HTMLElement;
      return {
        width: styled.style.width || '0px',
        height: styled.style.height || '0px',
        paddingLeft: '0px',
        paddingRight: '0px',
        paddingTop: '0px',
        paddingBottom: '0px',
      } as CSSStyleDeclaration;
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
    document.body.innerHTML = '';
  });

  it('returns valid dimensions for a normal-sized container', () => {
    const container = createContainer({ width: 1200, height: 800 });
    const term = createTermWithRendererCell(8.4, 21);
    const dims = getScaledDimensions(container, term, 14);
    expect(dims).not.toBeNull();
    expect(dims!.cols).toBeGreaterThan(20);
    expect(dims!.rows).toBeGreaterThan(10);
  });

  it('returns null when computed grid is below the minimum usable threshold (≤20 cols)', () => {
    // Container width that produces ~10 cols: ~10 * 8.4 + scrollbar ≈ 100px.
    const container = createContainer({ width: 100, height: 400 });
    const term = createTermWithRendererCell(8.4, 21);
    const dims = getScaledDimensions(container, term, 14);
    expect(dims).toBeNull();
  });

  it('returns null when computed grid is below the minimum usable threshold (≤10 rows)', () => {
    // Container height that produces ~6 rows: ~6 * 21 ≈ 126px.
    const container = createContainer({ width: 1200, height: 126 });
    const term = createTermWithRendererCell(8.4, 21);
    const dims = getScaledDimensions(container, term, 14);
    expect(dims).toBeNull();
  });

  it('returns null when both axes are sub-usable (the live bug repro: ~84×126px → 10×6)', () => {
    // Captured from daemon log: pty_resize 10×6 = ~84px wide × ~126px tall.
    const container = createContainer({ width: 84, height: 126 });
    const term = createTermWithRendererCell(8.4, 21);
    const dims = getScaledDimensions(container, term, 14);
    expect(dims).toBeNull();
  });

  it('returns null on zero/negative container size', () => {
    const container = createContainer({ width: 0, height: 0 });
    const term = createTermWithRendererCell(8.4, 21);
    expect(getScaledDimensions(container, term, 14)).toBeNull();
  });

  it('returns valid dimensions just above the minimum threshold', () => {
    // ~22 cols × ~12 rows: above the 20×10 floor.
    const container = createContainer({ width: 220, height: 280 });
    const term = createTermWithRendererCell(8.4, 21);
    const dims = getScaledDimensions(container, term, 14);
    expect(dims).not.toBeNull();
    expect(dims!.cols).toBeGreaterThan(20);
    expect(dims!.rows).toBeGreaterThan(10);
  });
});
