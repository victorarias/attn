import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { installTerminalViewportLifecycle } from './terminalViewportLifecycle';

const mockState = vi.hoisted(() => ({
  resizeObservers: [] as Array<{ trigger: (width: number, height: number) => void }>,
  intersectionObservers: [] as Array<{ trigger: (isIntersecting: boolean) => void }>,
  dimsQueue: [] as Array<{ cols: number; rows: number; diagnostics: any } | null>,
  matchMediaListener: null as null | (() => void),
  devicePixelRatio: 2,
}));

vi.mock('./terminalSizing', () => ({
  getScaledDimensions: () => mockState.dimsQueue.shift() ?? null,
}));

describe('installTerminalViewportLifecycle', () => {
  beforeEach(() => {
    mockState.resizeObservers.length = 0;
    mockState.intersectionObservers.length = 0;
    mockState.dimsQueue.length = 0;
    mockState.matchMediaListener = null;
    mockState.devicePixelRatio = 2;

    vi.useFakeTimers();
    vi.spyOn(window, 'requestAnimationFrame').mockImplementation((cb: FrameRequestCallback) => {
      cb(0);
      return 1;
    });

    Object.defineProperty(window, 'devicePixelRatio', {
      configurable: true,
      get: () => mockState.devicePixelRatio,
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
    vi.stubGlobal('requestIdleCallback', (cb: () => void) => cb());
    vi.spyOn(window, 'matchMedia').mockImplementation(() => ({
      addEventListener: (_event: string, listener: () => void) => {
        mockState.matchMediaListener = listener;
      },
      removeEventListener: vi.fn(),
      matches: false,
      media: '',
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }) as unknown as MediaQueryList);
  });

  afterEach(() => {
    vi.runOnlyPendingTimers();
    vi.useRealTimers();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it('uses the first resize observer measurement to mark the terminal ready', () => {
    const container = document.createElement('div');
    document.body.appendChild(container);
    const applyMeasuredTerminalGeometry = vi.fn();

    mockState.dimsQueue.push({
      cols: 118,
      rows: 48,
      diagnostics: { containerWidth: 960, containerHeight: 772 },
    });

    installTerminalViewportLifecycle({
      term: { cols: 80, rows: 24, buffer: { normal: { length: 0 } } } as any,
      container,
      fontSizeRef: { current: 14 },
      visibleRef: { current: true },
      readyFiredRef: { current: false },
      startupSnapshotRef: { current: {
        initialContainer: null,
        initialCols: null,
        initialRows: null,
        firstObservedContainer: null,
        firstReadySource: null,
        firstReadyAt: null,
        firstReadyCols: null,
        firstReadyRows: null,
        fontEffectAppliedBeforeReady: false,
        skippedInitialFontEffect: false,
      } },
      logTerminal: vi.fn(),
      getContainerDebugInfo: vi.fn(() => ({})),
      applyMeasuredTerminalGeometry,
      resizeTerminal: vi.fn(),
      onVisibilityChanged: vi.fn(),
    });

    mockState.resizeObservers[0].trigger(960, 772);

    expect(applyMeasuredTerminalGeometry).toHaveBeenCalledWith(
      expect.anything(),
      expect.objectContaining({ cols: 118, rows: 48 }),
      expect.objectContaining({
        readySource: 'resize_observer',
        readyReason: 'ready',
        resizeReason: 'ready',
      }),
    );
    container.remove();
  });

  it('does nothing when visibility returns without a geometry change', () => {
    const resizeTerminal = vi.fn();

    mockState.dimsQueue.push({
      cols: 118,
      rows: 48,
      diagnostics: { containerWidth: 960, containerHeight: 772 },
    });

    installTerminalViewportLifecycle({
      term: { cols: 118, rows: 48, buffer: { normal: { length: 0 } } } as any,
      container: document.createElement('div'),
      fontSizeRef: { current: 14 },
      visibleRef: { current: true },
      readyFiredRef: { current: true },
      startupSnapshotRef: { current: {
        initialContainer: null,
        initialCols: null,
        initialRows: null,
        firstObservedContainer: null,
        firstReadySource: null,
        firstReadyAt: null,
        firstReadyCols: null,
        firstReadyRows: null,
        fontEffectAppliedBeforeReady: false,
        skippedInitialFontEffect: false,
      } },
      logTerminal: vi.fn(),
      getContainerDebugInfo: vi.fn(() => ({})),
      applyMeasuredTerminalGeometry: vi.fn(),
      resizeTerminal,
      onVisibilityChanged: vi.fn(),
    });

    mockState.intersectionObservers[0].trigger(false);
    mockState.intersectionObservers[0].trigger(true);

    expect(resizeTerminal).not.toHaveBeenCalled();
  });

  it('resizes on dpr changes using current measured dimensions', () => {
    const resizeTerminal = vi.fn();

    mockState.dimsQueue.push({
      cols: 120,
      rows: 50,
      diagnostics: { containerWidth: 960, containerHeight: 772 },
    });

    installTerminalViewportLifecycle({
      term: { cols: 118, rows: 48, buffer: { normal: { length: 0 } } } as any,
      container: document.createElement('div'),
      fontSizeRef: { current: 14 },
      visibleRef: { current: true },
      readyFiredRef: { current: true },
      startupSnapshotRef: { current: {
        initialContainer: null,
        initialCols: null,
        initialRows: null,
        firstObservedContainer: null,
        firstReadySource: null,
        firstReadyAt: null,
        firstReadyCols: null,
        firstReadyRows: null,
        fontEffectAppliedBeforeReady: false,
        skippedInitialFontEffect: false,
      } },
      logTerminal: vi.fn(),
      getContainerDebugInfo: vi.fn(() => ({})),
      applyMeasuredTerminalGeometry: vi.fn(),
      resizeTerminal,
      onVisibilityChanged: vi.fn(),
    });

    mockState.devicePixelRatio = 1;
    mockState.matchMediaListener?.();

    expect(resizeTerminal).toHaveBeenCalledWith(expect.anything(), 120, 50, 'dpr_change');
  });
});
