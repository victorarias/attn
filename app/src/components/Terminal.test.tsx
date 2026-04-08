import { act, render } from '@testing-library/react';
import { createRef } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Terminal, type TerminalHandle } from './Terminal';

const mockState = vi.hoisted(() => ({
  terminals: [] as Array<{
    cols: number;
    rows: number;
    resizeMock: ReturnType<typeof vi.fn>;
  }>,
  resizeObservers: [] as Array<{ trigger: (width: number, height: number) => void }>,
  intersectionObservers: [] as Array<{ trigger: (isIntersecting: boolean) => void }>,
}));

vi.mock('@xterm/xterm', () => {
  class MockTerminal {
    cols: number;
    rows: number;
    options: Record<string, unknown> = {};
    unicode = { activeVersion: '' };
    element: HTMLElement | null = null;
    buffer = {
      active: { length: 0, baseY: 0, viewportY: 0 },
      normal: { length: 0 },
    };
    _core = {
      coreService: { isCursorHidden: false },
      registerCsiHandler: () => ({ dispose() {} }),
      _renderService: {
        dimensions: {
          css: {
            cell: { width: 9, height: 18 },
          },
        },
      },
    };
    write = vi.fn();
    resizeMock = vi.fn((cols: number, rows: number) => {
      this.cols = cols;
      this.rows = rows;
    });

    constructor(options: { cols: number; rows: number }) {
      this.cols = options.cols;
      this.rows = options.rows;
      mockState.terminals.push(this);
    }

    open(container: HTMLElement) {
      this.element = document.createElement('div');
      this.element.className = 'xterm';
      container.appendChild(this.element);
    }

    loadAddon() {}
    resize(cols: number, rows: number) {
      this.resizeMock(cols, rows);
    }
    focus() {}
    blur() {}
    dispose() {}
    onSelectionChange() { return { dispose() {} }; }
    onRender() { return { dispose() {} }; }
    onWriteParsed() { return { dispose() {} }; }
    onScroll() { return { dispose() {} }; }
    onData() { return { dispose() {} }; }
    attachCustomKeyEventHandler() { return true; }
    hasSelection() { return false; }
    getSelection() { return ''; }
  }

  return { Terminal: MockTerminal };
});

vi.mock('@xterm/addon-web-links', () => ({
  WebLinksAddon: class MockWebLinksAddon {},
}));

vi.mock('@xterm/addon-webgl', () => ({
  WebglAddon: class MockWebglAddon {
    onContextLoss() {}
    dispose() {}
  },
}));

vi.mock('@xterm/addon-unicode11', () => ({
  Unicode11Addon: class MockUnicode11Addon {},
}));

vi.mock('@tauri-apps/plugin-opener', () => ({
  openUrl: vi.fn(),
}));

describe('Terminal', () => {
  beforeEach(() => {
    mockState.terminals.length = 0;
    mockState.resizeObservers.length = 0;
    mockState.intersectionObservers.length = 0;

    vi.useFakeTimers();
    vi.spyOn(window, 'requestAnimationFrame').mockImplementation((cb: FrameRequestCallback) => {
      cb(0);
      return 1;
    });

    const realGetComputedStyle = window.getComputedStyle.bind(window);
    vi.spyOn(window, 'getComputedStyle').mockImplementation((element: Element) => {
      const style = realGetComputedStyle(element);
      if ((element as HTMLElement).classList?.contains('terminal-container')) {
        return {
          ...style,
          width: '1000px',
          height: '900px',
          paddingLeft: '0px',
          paddingRight: '0px',
          paddingTop: '0px',
          paddingBottom: '0px',
          display: 'block',
          visibility: 'visible',
        } as CSSStyleDeclaration;
      }
      if ((element as HTMLElement).classList?.contains('xterm')) {
        return {
          ...style,
          paddingLeft: '0px',
          paddingRight: '0px',
          paddingTop: '0px',
          paddingBottom: '0px',
        } as CSSStyleDeclaration;
      }
      return style;
    });

    class MockResizeObserver {
      private readonly callback: ResizeObserverCallback;

      constructor(callback: ResizeObserverCallback) {
        this.callback = callback;
        mockState.resizeObservers.push({
          trigger: (width: number, height: number) => {
            this.callback([
              {
                contentRect: {
                  width,
                  height,
                },
              } as ResizeObserverEntry,
            ], this as unknown as ResizeObserver);
          },
        });
      }

      observe() {}
      unobserve() {}
      disconnect() {}
    }

    class MockIntersectionObserver {
      private readonly callback: IntersectionObserverCallback;

      constructor(callback: IntersectionObserverCallback) {
        this.callback = callback;
        mockState.intersectionObservers.push({
          trigger: (isIntersecting: boolean) => {
            this.callback([
              { isIntersecting } as IntersectionObserverEntry,
            ], this as unknown as IntersectionObserver);
          },
        });
      }

      observe() {}
      unobserve() {}
      disconnect() {}
      takeRecords() { return []; }
      root = null;
      rootMargin = '';
      thresholds = [];
    }

    vi.stubGlobal('ResizeObserver', MockResizeObserver);
    vi.stubGlobal('IntersectionObserver', MockIntersectionObserver);
  });

  afterEach(() => {
    vi.runOnlyPendingTimers();
    vi.useRealTimers();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it('waits for the first real ResizeObserver measurement before reporting readiness', () => {
    const handleRef = createRef<TerminalHandle>();
    const onResize = vi.fn();

    render(
      <Terminal
        ref={handleRef}
        fontSize={14}
        resolvedTheme="dark"
        tuiCursor
        onResize={onResize}
      />
    );

    expect(mockState.terminals).toHaveLength(1);
    expect(mockState.resizeObservers).toHaveLength(1);
    expect(onResize).not.toHaveBeenCalled();

    act(() => {
      mockState.resizeObservers[0].trigger(1000, 900);
    });

    const term = mockState.terminals[0];
    expect(onResize).toHaveBeenCalledTimes(1);
    expect(onResize).toHaveBeenLastCalledWith(109, 50, {
      reason: 'ready',
    });
    const baselineResizeCalls = term.resizeMock.mock.calls.length;

    act(() => {
      handleRef.current?.fit();
    });

    expect(term.resizeMock.mock.calls.length).toBe(baselineResizeCalls);
    expect(onResize).toHaveBeenLastCalledWith(109, 50, {
      forceRedraw: true,
      reason: 'fit_same_size',
    });
    expect(onResize.mock.calls).toEqual([
      [109, 50, { reason: 'ready' }],
      [109, 50, { forceRedraw: true, reason: 'fit_same_size' }],
    ]);
  });

  it('applies font-size changes after the terminal is ready', () => {
    const handleRef = createRef<TerminalHandle>();
    const onResize = vi.fn();

    const { rerender } = render(
      <Terminal
        ref={handleRef}
        fontSize={14}
        resolvedTheme="dark"
        tuiCursor
        onResize={onResize}
      />
    );

    act(() => {
      mockState.resizeObservers[0].trigger(1000, 900);
    });

    const term = mockState.terminals[0];
    const baselineResizeCalls = term.resizeMock.mock.calls.length;

    rerender(
      <Terminal
        ref={handleRef}
        fontSize={16}
        resolvedTheme="dark"
        tuiCursor
        onResize={onResize}
      />
    );

    expect(term.resizeMock.mock.calls.length).toBe(baselineResizeCalls + 1);
    expect(onResize).toHaveBeenLastCalledWith(109, 50, {
      reason: 'font-change',
    });
  });
});
