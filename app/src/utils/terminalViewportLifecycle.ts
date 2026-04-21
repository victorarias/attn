import type { Terminal as XTerm } from '@xterm/xterm';
import {
  isSuspiciousTerminalSize,
  type ResizeDiagnostics,
} from './terminalDebug';
import type { TerminalPerfStartupSnapshot } from './terminalPerf';
import { getScaledDimensions } from './terminalSizing';
import {
  planObservedTerminalResize,
  planVisibilityFlush,
  type TerminalViewportResizePlan,
  X_AXIS_DEBOUNCE_MS,
} from './terminalResizeLifecycle';

interface MeasuredTerminalDimensions {
  cols: number;
  rows: number;
  diagnostics: ResizeDiagnostics;
}

interface TerminalViewportLifecycleOptions {
  term: XTerm;
  container: HTMLElement;
  fontSizeRef: { current: number };
  visibleRef: { current: boolean };
  readyFiredRef: { current: boolean };
  startupSnapshotRef: { current: TerminalPerfStartupSnapshot };
  logTerminal: (level: 'log' | 'warn', message: string, details?: Record<string, unknown>) => void;
  getContainerDebugInfo: (container: HTMLElement) => Record<string, unknown>;
  applyMeasuredTerminalGeometry: (
    term: XTerm,
    dims: MeasuredTerminalDimensions,
    options: {
      readySource: 'resize_observer' | 'font_change' | 'fit_fallback';
      readyReason: string;
      resizeReason: string;
    },
  ) => void;
  resizeTerminal: (
    term: XTerm,
    cols: number,
    rows: number,
    reason: string,
    diagnostics?: ResizeDiagnostics | null,
  ) => void;
  onVisibilityChanged: (nowVisible: boolean, wasHidden: boolean) => void;
}

export interface TerminalViewportLifecycle {
  dispose: () => void;
}

export function installTerminalViewportLifecycle({
  term,
  container,
  fontSizeRef,
  visibleRef,
  readyFiredRef,
  startupSnapshotRef,
  logTerminal,
  getContainerDebugInfo,
  applyMeasuredTerminalGeometry,
  resizeTerminal,
  onVisibilityChanged,
}: TerminalViewportLifecycleOptions): TerminalViewportLifecycle {
  let lastCols = term.cols;
  let lastRows = term.rows;
  let latestX = term.cols;
  let latestY = term.rows;
  let latestDiagnostics: ResizeDiagnostics | null = null;
  let xResizeTimeout: number | undefined;
  let isVisible = true;

  const clearXResizeTimeout = () => {
    if (xResizeTimeout !== undefined) {
      window.clearTimeout(xResizeTimeout);
      xResizeTimeout = undefined;
    }
  };

  const applyImmediateResize = (resize: NonNullable<TerminalViewportResizePlan['immediate']>) => {
    if (resize.axis === 'both') {
      lastCols = resize.next.cols;
      lastRows = resize.next.rows;
      resizeTerminal(term, resize.next.cols, resize.next.rows, resize.reason, resize.next.diagnostics);
      return;
    }

    lastRows = resize.next.rows;
    resizeTerminal(term, term.cols, resize.next.rows, resize.reason, resize.next.diagnostics);
  };

  const scheduleDebouncedXResize = (resize: NonNullable<TerminalViewportResizePlan['debouncedX']>) => {
    xResizeTimeout = window.setTimeout(() => {
      lastCols = resize.cols;
      resizeTerminal(term, resize.cols, term.rows, 'resize_x', resize.diagnostics);
    }, X_AXIS_DEBOUNCE_MS);
  };

  const scheduleIdleResize = (resize: NonNullable<TerminalViewportResizePlan['idle']>) => {
    (window as Window & { requestIdleCallback?: (cb: () => void) => void }).requestIdleCallback?.(() => {
      lastCols = latestX;
      lastRows = latestY;
      resizeTerminal(term, latestX, latestY, resize.reason, latestDiagnostics);
    });
  };

  const executeResizePlan = (plan: TerminalViewportResizePlan) => {
    if (plan.cancelPendingX) {
      clearXResizeTimeout();
    }
    if (plan.immediate) {
      applyImmediateResize(plan.immediate);
    }
    if (plan.debouncedX) {
      scheduleDebouncedXResize(plan.debouncedX);
    }
    if (plan.idle) {
      scheduleIdleResize(plan.idle);
    }
  };

  const handleResize = () => {
    const dims = getScaledDimensions(container, term, fontSizeRef.current);
    if (!dims) {
      logTerminal('log', 'handleResize: no dimensions', {
        isVisible: visibleRef.current,
        container: getContainerDebugInfo(container),
      });
      return;
    }

    const { cols, rows, diagnostics } = dims;
    latestX = cols;
    latestY = rows;
    latestDiagnostics = diagnostics;

    if (isSuspiciousTerminalSize(cols, rows)) {
      logTerminal('warn', 'handleResize: suspicious dimensions detected', {
        cols,
        rows,
        lastCols,
        lastRows,
        bufferLength: term.buffer.normal.length,
        isVisible: visibleRef.current,
        container: getContainerDebugInfo(container),
      });
    }

    const plan = planObservedTerminalResize({
      next: {
        cols,
        rows,
        diagnostics,
      },
      lastCols,
      lastRows,
      bufferLength: term.buffer.normal.length,
      isVisible,
      hasIdleCallback: 'requestIdleCallback' in window,
    });
    executeResizePlan(plan);
  };

  const resizeObserver = new ResizeObserver((entries) => {
    const entry = entries[0];
    if (!entry || entry.contentRect.width <= 0 || entry.contentRect.height <= 0) {
      if (entry) {
        logTerminal('log', 'ResizeObserver ignored non-positive size', {
          contentRectWidth: entry.contentRect.width,
          contentRectHeight: entry.contentRect.height,
          isVisible: visibleRef.current,
        });
      }
      return;
    }

    if (!startupSnapshotRef.current.firstObservedContainer) {
      startupSnapshotRef.current.firstObservedContainer = {
        width: Math.round(entry.contentRect.width),
        height: Math.round(entry.contentRect.height),
      };
    }

    if (!readyFiredRef.current) {
      requestAnimationFrame(() => {
        if (!container.isConnected) {
          logTerminal('log', 'ready RAF bail: container disconnected', {});
          return;
        }
        const dims = getScaledDimensions(container, term, fontSizeRef.current);
        if (!dims) {
          logTerminal('log', 'ready RAF bail: dims null', {
            fontSize: fontSizeRef.current,
          });
          return;
        }
        if (dims.cols <= 0 || dims.rows <= 0) {
          logTerminal('log', 'ready RAF bail: zero dims', {
            cols: dims.cols,
            rows: dims.rows,
          });
          return;
        }
        lastCols = dims.cols;
        lastRows = dims.rows;
        applyMeasuredTerminalGeometry(term, dims, {
          readySource: 'resize_observer',
          readyReason: 'ready',
          resizeReason: 'ready',
        });
      });
      return;
    }

    handleResize();
  });
  resizeObserver.observe(container);

  const visibilityObserver = new IntersectionObserver(
    (entries) => {
      const nowVisible = entries[0]?.isIntersecting ?? true;
      const wasHidden = !isVisible && nowVisible;
      isVisible = nowVisible;
      visibleRef.current = nowVisible;
      logTerminal('log', 'Visibility changed', { nowVisible, wasHidden, readyFired: readyFiredRef.current });
      onVisibilityChanged(nowVisible, wasHidden);

      if (wasHidden && readyFiredRef.current) {
        const dims = getScaledDimensions(container, term, fontSizeRef.current);
        if (dims) {
          latestX = dims.cols;
          latestY = dims.rows;
          latestDiagnostics = dims.diagnostics;
        }
        const plan = planVisibilityFlush({
          wasHidden,
          ready: readyFiredRef.current,
          next: dims ? {
            cols: dims.cols,
            rows: dims.rows,
            diagnostics: dims.diagnostics,
          } : null,
          currentCols: term.cols,
          currentRows: term.rows,
        });
        executeResizePlan(plan);
      }
    },
    { threshold: 0 },
  );
  visibilityObserver.observe(container);

  let currentDpr = window.devicePixelRatio;
  const handleDprChange = () => {
    const newDpr = window.devicePixelRatio;
    if (newDpr !== currentDpr) {
      currentDpr = newDpr;
      const dims = getScaledDimensions(container, term, fontSizeRef.current);
      if (dims) {
        resizeTerminal(term, dims.cols, dims.rows, 'dpr_change');
      }
    }
  };
  const dprMediaQuery = window.matchMedia(`(resolution: ${window.devicePixelRatio}dppx)`);
  dprMediaQuery.addEventListener('change', handleDprChange);

  return {
    dispose: () => {
      resizeObserver.disconnect();
      visibilityObserver.disconnect();
      clearXResizeTimeout();
      dprMediaQuery.removeEventListener('change', handleDprChange);
    },
  };
}
