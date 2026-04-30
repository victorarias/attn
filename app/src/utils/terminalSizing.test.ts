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

  it('returns valid dimensions for a normal-sized container without layout context', () => {
    const container = createContainer({ width: 1200, height: 800 });
    const term = createTermWithRendererCell(8.4, 21);
    const dims = getScaledDimensions(container, term, 14);
    expect(dims).not.toBeNull();
    expect(dims!.cols).toBeGreaterThan(20);
    expect(dims!.rows).toBeGreaterThan(10);
  });

  it('returns null on zero/negative container size', () => {
    const container = createContainer({ width: 0, height: 0 });
    const term = createTermWithRendererCell(8.4, 21);
    expect(getScaledDimensions(container, term, 14)).toBeNull();
  });

  it('without layout context, allows any ≥2×2 measurement (legitimate small splits init)', () => {
    // tr502-style 3-way split: shell pane lands at ~140×540px → ~17×25.
    // Without layout context, only zero/near-zero is filtered, so this
    // measurement passes — main panes' codex SIGWINCH protection lives at
    // the resize-send point, not here.
    const container = createContainer({ width: 140, height: 540 });
    const term = createTermWithRendererCell(8, 16);
    const dims = getScaledDimensions(container, term, 14);
    expect(dims).not.toBeNull();
    expect(dims!.cols).toBeGreaterThan(2);
    expect(dims!.rows).toBeGreaterThan(2);
  });

  it('with layout context, rejects layout-implausible transient (1-pane window, tiny measurement)', () => {
    // The codex transient bug: container reports 84×126px while window is
    // 1200×800 with 1 pane (mid-mount or panel animation). 10 cols on a
    // 1200px-wide single-pane window is 7% of fair share — clearly transient.
    const container = createContainer({ width: 84, height: 126 });
    const term = createTermWithRendererCell(8.4, 21);
    const dims = getScaledDimensions(container, term, 14, undefined, undefined, {
      windowWidth: 1200,
      windowHeight: 800,
      paneCount: 1,
    });
    expect(dims).toBeNull();
  });

  it('with layout context, accepts legitimate 3-way split measurement', () => {
    // tr502 case revisited: 800×600 window, 3 panes → fair share ~267px wide.
    // 30% floor = ~80px ≈ 10 cols. A 140px shell measurement (17 cols) is
    // well above the floor, so it should pass.
    const container = createContainer({ width: 140, height: 540 });
    const term = createTermWithRendererCell(8, 16);
    const dims = getScaledDimensions(container, term, 14, undefined, undefined, {
      windowWidth: 800,
      windowHeight: 600,
      paneCount: 3,
    });
    expect(dims).not.toBeNull();
    expect(dims!.cols).toBeGreaterThan(10);
  });

  it('layout context floor scales with pane count (4-way split admits smaller measurements)', () => {
    // 1200×800 window. With paneCount=1, fair share = 1200px → 30% floor = 360px.
    // With paneCount=4, fair share = 300px → 30% floor = 90px.
    // A 200px-wide measurement should reject under paneCount=1, accept under paneCount=4.
    const container = createContainer({ width: 200, height: 600 });
    const term = createTermWithRendererCell(8, 16);

    expect(
      getScaledDimensions(container, term, 14, undefined, undefined, {
        windowWidth: 1200,
        windowHeight: 800,
        paneCount: 1,
      }),
    ).toBeNull();

    expect(
      getScaledDimensions(container, term, 14, undefined, undefined, {
        windowWidth: 1200,
        windowHeight: 800,
        paneCount: 4,
      }),
    ).not.toBeNull();
  });

  it('with layout context, height-axis transient is also rejected', () => {
    // 1200×800 window, 1 pane, container reports full width but tiny height —
    // mid-animation collapse on the row axis. Reject as transient.
    const container = createContainer({ width: 1200, height: 60 });
    const term = createTermWithRendererCell(8, 16);
    const dims = getScaledDimensions(container, term, 14, undefined, undefined, {
      windowWidth: 1200,
      windowHeight: 800,
      paneCount: 1,
    });
    expect(dims).toBeNull();
  });
});
