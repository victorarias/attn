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
      forceRedrawReason?: string;
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
  onForceRedraw: (cols: number, rows: number, reason: string) => void;
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
  onForceRedraw,
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

  const resizeBoth = (cols: number, rows: number, diagnostics: ResizeDiagnostics | null) => {
    lastCols = cols;
    lastRows = rows;
    resizeTerminal(term, cols, rows, 'resize_both', diagnostics);
  };

  const resizeX = (cols: number, diagnostics: ResizeDiagnostics | null) => {
    lastCols = cols;
    resizeTerminal(term, cols, term.rows, 'resize_x', diagnostics);
  };

  const resizeY = (rows: number, diagnostics: ResizeDiagnostics | null) => {
    lastRows = rows;
    resizeTerminal(term, term.cols, rows, 'resize_y', diagnostics);
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

    switch (plan.type) {
      case 'none':
        return;
      case 'resize_both':
        clearXResizeTimeout();
        resizeBoth(plan.next.cols, plan.next.rows, plan.next.diagnostics);
        return;
      case 'idle_resize_both':
        (window as Window & { requestIdleCallback?: (cb: () => void) => void }).requestIdleCallback?.(() => {
          resizeBoth(latestX, latestY, latestDiagnostics);
        });
        return;
      case 'resize_y':
        resizeY(plan.rows, plan.diagnostics);
        return;
      case 'debounce_x':
        clearXResizeTimeout();
        xResizeTimeout = window.setTimeout(() => {
          resizeX(plan.cols, plan.diagnostics);
        }, X_AXIS_DEBOUNCE_MS);
        return;
      case 'resize_y_then_debounce_x':
        resizeY(plan.rows, plan.diagnostics);
        clearXResizeTimeout();
        xResizeTimeout = window.setTimeout(() => {
          resizeX(plan.cols, plan.diagnostics);
        }, X_AXIS_DEBOUNCE_MS);
        return;
    }
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
          return;
        }
        const dims = getScaledDimensions(container, term, fontSizeRef.current);
        if (dims && dims.cols > 0 && dims.rows > 0) {
          lastCols = dims.cols;
          lastRows = dims.rows;
          applyMeasuredTerminalGeometry(term, dims, {
            readySource: 'resize_observer',
            readyReason: 'ready',
            resizeReason: 'ready',
          });
        }
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
        clearXResizeTimeout();
        const dims = getScaledDimensions(container, term, fontSizeRef.current);
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

        if (plan.type === 'none') {
          return;
        }

        if (dims) {
          lastCols = dims.cols;
          lastRows = dims.rows;
        }

        if (plan.type === 'force_redraw') {
          onForceRedraw(plan.cols, plan.rows, 'visibility_flush_same_size');
        } else {
          resizeTerminal(term, plan.next.cols, plan.next.rows, 'visibility_flush', plan.next.diagnostics);
        }
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
