import { useEffect, useRef, useImperativeHandle, forwardRef, useCallback, useState } from 'react';
import { Terminal as XTerm } from '@xterm/xterm';
import { WebLinksAddon } from '@xterm/addon-web-links';
import { Unicode11Addon } from '@xterm/addon-unicode11';
import '@xterm/xterm/css/xterm.css';
import './Terminal.css';
import { isSuspiciousTerminalSize, isTerminalDebugEnabled, recordResizeEvent, formatResizeLog, type ResizeDiagnostics } from '../utils/terminalDebug';
import { activeElementSummary } from '../utils/paneRuntimeDebug';
import { cleanTerminalLines, bufferSelectionToMarkdown } from '../utils/terminalMarkdown';
import { registerTerminalPerfGetter } from '../utils/terminalPerf';
import {
  DEFAULT_FONT_SIZE,
  FONT_FAMILY,
  getInitialTerminalDimensions,
  getScaledDimensions,
  getTerminalTheme,
  TERMINAL_SCROLLBACK_LINES,
  type ResolvedTheme,
} from '../utils/terminalSizing';
import {
  cleanupTerminalScrollPin,
  installTerminalScrollPin,
  resetTerminalScrollPin,
} from '../utils/terminalScrollPin';
import {
  getTerminalRendererConfig,
  setTerminalRendererConfig,
  subscribeTerminalRendererConfig,
  type TerminalRendererMode,
} from '../utils/terminalRenderer';
import { installTerminalRendererLifecycle } from '../utils/terminalRendererLifecycle';
import { installTerminalViewportLifecycle } from '../utils/terminalViewportLifecycle';
import { recordTerminalRuntimeLog } from '../utils/terminalRuntimeLog';
import type { TerminalPerfStartupSnapshot } from '../utils/terminalPerf';
export type { ResolvedTheme } from '../utils/terminalSizing';

function getContainerDebugInfo(container: HTMLElement) {
  const rect = container.getBoundingClientRect();
  const style = getComputedStyle(container);
  const parent = container.parentElement;
  const parentRect = parent?.getBoundingClientRect();
  const parentStyle = parent ? getComputedStyle(parent) : null;

  return {
    containerRect: { width: Math.round(rect.width), height: Math.round(rect.height) },
    containerDisplay: style.display,
    containerVisibility: style.visibility,
    parentRect: parentRect ? { width: Math.round(parentRect.width), height: Math.round(parentRect.height) } : null,
    parentDisplay: parentStyle?.display ?? null,
    parentVisibility: parentStyle?.visibility ?? null,
    dpr: window.devicePixelRatio,
  };
}

function elementSizeSnapshot(element: Element | null) {
  if (!(element instanceof HTMLElement)) {
    return null;
  }
  const rect = element.getBoundingClientRect();
  return {
    width: Math.round(rect.width),
    height: Math.round(rect.height),
  };
}

function createEmptyStartupSnapshot(): TerminalPerfStartupSnapshot {
  return {
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
  };
}

// Link opening is handled by the Tauri opener plugin at the webview level.
// Our xterm handlers are no-ops — they exist only to suppress xterm's default
// confirm() dialog for OSC 8 hyperlinks and to enable WebLinksAddon decorations
// (underline + pointer cursor on hover).

export interface TerminalHandle {
  terminal: XTerm | null;
  fit: () => void;
  focus: () => boolean;
  typeTextViaInput: (text: string) => boolean;
  isInputFocused: () => boolean;
  resetScrollPin: () => void;
}

interface TerminalProps {
  fontSize?: number;
  resolvedTheme?: ResolvedTheme;
  debugName?: string;
  runtimeLogMeta?: {
    sessionId: string;
    paneId: string;
    runtimeId: string;
    paneKind: 'main' | 'shell';
    isActivePane: boolean;
    isActiveSession: boolean;
  };
  /** TUI apps (Ink) render their own cursor; hide xterm's after resize to prevent ghost cursor. */
  tuiCursor?: boolean;
  onInit?: (terminal: XTerm) => void;
  onReady?: (terminal: XTerm) => void;
  onResize?: (cols: number, rows: number, options?: { reason?: string }) => void;
}

export const Terminal = forwardRef<TerminalHandle, TerminalProps>(
  function Terminal({ fontSize = DEFAULT_FONT_SIZE, resolvedTheme = 'dark', debugName, runtimeLogMeta, tuiCursor, onInit, onReady, onResize }, ref) {
    const containerRef = useRef<HTMLDivElement>(null);
    const xtermRef = useRef<XTerm | null>(null);
    const debugNameRef = useRef(debugName || 'unknown');
    const visibleRef = useRef(true);
    const rendererModeRef = useRef<'webgl' | 'dom'>('dom');
    const writeQueueChunksRef = useRef(0);
    const writeQueueBytesRef = useRef(0);
    const perfRegistryIdRef = useRef(`terminal-${Math.random().toString(16).slice(2)}`);
    const readyFiredRef = useRef(false);
    const appliedFontSizeRef = useRef<number | null>(null);
    const startupSnapshotRef = useRef<TerminalPerfStartupSnapshot>(createEmptyStartupSnapshot());
    const lastResizeSnapshotRef = useRef<{
      at: number;
      trigger: string;
      cols: number;
      rows: number;
      prevCols: number;
      prevRows: number;
      diagnostics: ResizeDiagnostics;
    } | null>(null);

    // Store callbacks and values in refs to avoid re-running effect when they change
    const onReadyRef = useRef(onReady);
    const onInitRef = useRef(onInit);
    const onResizeRef = useRef(onResize);
    const fontSizeRef = useRef(fontSize);
    const tuiCursorRef = useRef(tuiCursor);
    const runtimeLogMetaRef = useRef(runtimeLogMeta);

    useEffect(() => {
      onReadyRef.current = onReady;
      onInitRef.current = onInit;
      onResizeRef.current = onResize;
      fontSizeRef.current = fontSize;
      debugNameRef.current = debugName || 'unknown';
      tuiCursorRef.current = tuiCursor;
      runtimeLogMetaRef.current = runtimeLogMeta;
    });

    useEffect(() => {
      if (!runtimeLogMeta) {
        return;
      }
      recordTerminalRuntimeLog({
        category: 'terminal',
        event: 'terminal.activity_target_changed',
        sessionId: runtimeLogMeta.sessionId,
        paneId: runtimeLogMeta.paneId,
        runtimeId: runtimeLogMeta.runtimeId,
        debugName: debugName || 'unknown',
        message: 'terminal activity target changed',
        details: {
          paneKind: runtimeLogMeta.paneKind,
          isActivePane: runtimeLogMeta.isActivePane,
          isActiveSession: runtimeLogMeta.isActiveSession,
        },
      });
    }, [
      debugName,
      runtimeLogMeta?.isActivePane,
      runtimeLogMeta?.isActiveSession,
      runtimeLogMeta?.paneId,
      runtimeLogMeta?.paneKind,
      runtimeLogMeta?.runtimeId,
      runtimeLogMeta?.sessionId,
    ]);

    // Update xterm theme at runtime when resolved theme changes
    useEffect(() => {
      const term = xtermRef.current;
      if (!term) return;
      term.options.theme = getTerminalTheme(resolvedTheme);
    }, [resolvedTheme]);

    // Debug overlay state — always record to ring buffer, only render overlay when debug enabled
    const [debugDisplay, setDebugDisplay] = useState<{
      cols: number;
      rows: number;
      containerWidth: number;
      containerHeight: number;
      cellWidth: number;
      cellHeight: number;
      cellSource: 'renderer' | 'measured';
      fontSize: number;
      dpr: number;
      trigger: string;
    } | null>(null);
    const [rendererConfig, setRendererConfigState] = useState(() => getTerminalRendererConfig());
    const [rendererDisplayMode, setRendererDisplayMode] = useState<TerminalRendererMode>('dom');

    const recordDiags = useCallback((
      trigger: string,
      cols: number,
      rows: number,
      prevCols: number,
      prevRows: number,
      diagnostics: ResizeDiagnostics,
    ) => {
      const recordedAt = Date.now();
      recordResizeEvent({
        timestamp: recordedAt,
        terminalName: debugNameRef.current,
        trigger,
        fontSize: fontSizeRef.current,
        cols,
        rows,
        prevCols,
        prevRows,
        isVisible: visibleRef.current,
        diagnostics,
      });
      lastResizeSnapshotRef.current = {
        at: recordedAt,
        trigger,
        cols,
        rows,
        prevCols,
        prevRows,
        diagnostics,
      };
      if (isTerminalDebugEnabled()) {
        setDebugDisplay({
          cols,
          rows,
          containerWidth: diagnostics.containerWidth,
          containerHeight: diagnostics.containerHeight,
          cellWidth: diagnostics.cellWidth,
          cellHeight: diagnostics.cellHeight,
          cellSource: diagnostics.cellSource,
          fontSize: fontSizeRef.current,
          dpr: diagnostics.dpr,
          trigger,
        });
      }
    }, []);

    const logTerminal = useCallback((
      level: 'log' | 'warn',
      message: string,
      details?: Record<string, unknown>
    ) => {
      if (level === 'log' && !isTerminalDebugEnabled()) {
        return;
      }
      const prefix = `[Terminal:${debugNameRef.current}] ${message}`;
      if (level === 'warn') {
        if (details) {
          console.warn(prefix, details);
        } else {
          console.warn(prefix);
        }
        return;
      }
      if (details) {
        console.log(prefix, details);
      } else {
        console.log(prefix);
      }
    }, []);

    const markTerminalReady = useCallback((
      term: XTerm,
      cols: number,
      rows: number,
      diagnostics: ResizeDiagnostics,
      source: 'resize_observer' | 'font_change' | 'fit_fallback',
      reason: string,
    ) => {
      if (readyFiredRef.current) {
        return;
      }
      readyFiredRef.current = true;
      startupSnapshotRef.current.firstReadySource = startupSnapshotRef.current.firstReadySource || source;
      startupSnapshotRef.current.firstReadyAt = startupSnapshotRef.current.firstReadyAt || Date.now();
      startupSnapshotRef.current.firstReadyCols = startupSnapshotRef.current.firstReadyCols ?? cols;
      startupSnapshotRef.current.firstReadyRows = startupSnapshotRef.current.firstReadyRows ?? rows;

      if (isSuspiciousTerminalSize(cols, rows)) {
        const container = containerRef.current;
        logTerminal('warn', 'ready path produced suspicious dimensions', {
          source,
          cols,
          rows,
          isVisible: visibleRef.current,
          container: container ? getContainerDebugInfo(container) : null,
        });
      } else {
        logTerminal('log', 'terminal ready', {
          source,
          cols,
          rows,
          isVisible: visibleRef.current,
        });
      }

      recordDiags(reason, cols, rows, term.cols, term.rows, diagnostics);
      term.resize(cols, rows);
      onResizeRef.current?.(cols, rows, { reason });

      const meta = runtimeLogMetaRef.current;
      if (meta) {
        recordTerminalRuntimeLog({
          category: 'terminal',
          event: 'terminal.ready',
          sessionId: meta.sessionId,
          paneId: meta.paneId,
          runtimeId: meta.runtimeId,
          debugName: debugNameRef.current,
          message: 'terminal ready',
          details: {
            paneKind: meta.paneKind,
            cols,
            rows,
            renderer: rendererModeRef.current,
            source,
          },
        });
      }

      onReadyRef.current?.(term);
    }, [logTerminal, recordDiags]);

    useEffect(() => {
      return subscribeTerminalRendererConfig(() => {
        setRendererConfigState(getTerminalRendererConfig());
      });
    }, []);

    const focusTerminal = useCallback((): boolean => {
      const term = xtermRef.current;
      const container = containerRef.current;
      if (!term || !container || !container.isConnected) {
        return false;
      }

      term.focus();
      return container.contains(document.activeElement);
    }, []);

    const typeTextViaInput = useCallback((text: string): boolean => {
      const container = containerRef.current;
      if (!container) {
        return false;
      }

      const textarea = container.querySelector('textarea.xterm-helper-textarea') as HTMLTextAreaElement | null;
      if (!textarea || document.activeElement !== textarea) {
        return false;
      }

      const descriptor = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value');
      const setValue = descriptor?.set;
      for (const char of text) {
        if (setValue) {
          setValue.call(textarea, char);
        } else {
          textarea.value = char;
        }
        textarea.dispatchEvent(new InputEvent('input', {
          bubbles: true,
          composed: true,
          data: char,
          inputType: 'insertText',
        }));
        if (setValue) {
          setValue.call(textarea, '');
        } else {
          textarea.value = '';
        }
      }

      return true;
    }, []);

    // Helper to resize terminal and notify PTY
    const resizeTerminal = useCallback((term: XTerm, cols: number, rows: number, reason: string, diagnostics?: ResizeDiagnostics | null) => {
      const suspiciousResize = isSuspiciousTerminalSize(cols, rows);
      if (suspiciousResize) {
        const container = containerRef.current;
        logTerminal('warn', 'Applying suspicious resize', {
          reason,
          nextCols: cols,
          nextRows: rows,
          prevCols: term.cols,
          prevRows: term.rows,
          isVisible: visibleRef.current,
          container: container ? getContainerDebugInfo(container) : null,
        });
      } else {
        logTerminal('log', 'Applying resize', {
          reason,
          nextCols: cols,
          nextRows: rows,
          prevCols: term.cols,
          prevRows: term.rows,
          isVisible: visibleRef.current,
        });
      }

      // Record to ring buffer before resize (captures prevCols/prevRows)
      if (diagnostics) {
        recordDiags(reason, cols, rows, term.cols, term.rows, diagnostics);
      }

      if (cols !== term.cols || rows !== term.rows) {
        term.resize(cols, rows);
        onResizeRef.current?.(cols, rows, { reason });
      }
      // Same-size case: no refresh needed. The fit() bounce sends SIGWINCH
      // which triggers the app to redraw. A term.refresh() here would reveal
      // xterm's cursor at the wrong buffer position, creating a ghost cursor
      // in TUI apps like Ink that render their own cursor.

      // TUI apps (Ink) render their own visual cursor and don't use DECTCEM.
      // After resize, xterm's cursor appears at the wrong buffer position as
      // a ghost. Force-hide it; the app's next redraw covers the position.
      if (tuiCursorRef.current) {
        const coreService = (term as any)._core?.coreService;
        if (coreService) {
          coreService.isCursorHidden = true;
        }
      }
    }, [logTerminal, recordDiags]);

    const applyMeasuredTerminalGeometry = useCallback((
      term: XTerm,
      dims: { cols: number; rows: number; diagnostics: ResizeDiagnostics },
      options: {
        readySource: 'resize_observer' | 'font_change' | 'fit_fallback';
        readyReason: string;
        resizeReason: string;
      },
    ) => {
      if (!readyFiredRef.current) {
        markTerminalReady(term, dims.cols, dims.rows, dims.diagnostics, options.readySource, options.readyReason);
        return;
      }

      resizeTerminal(term, dims.cols, dims.rows, options.resizeReason, dims.diagnostics);
    }, [markTerminalReady, resizeTerminal]);

    useImperativeHandle(ref, () => ({
      get terminal() {
        return xtermRef.current;
      },
      fit: () => {
        const term = xtermRef.current;
        const container = containerRef.current;
        if (!term || !container) return;

        const dims = getScaledDimensions(container, term, fontSizeRef.current);
        if (!dims) {
          logTerminal('warn', 'fit() produced no dimensions', {
            fontSize: fontSizeRef.current,
            isVisible: visibleRef.current,
            container: getContainerDebugInfo(container),
          });
          return;
        }
        if (isSuspiciousTerminalSize(dims.cols, dims.rows)) {
          logTerminal('warn', 'fit() produced suspicious dimensions', {
            cols: dims.cols,
            rows: dims.rows,
            fontSize: fontSizeRef.current,
            isVisible: visibleRef.current,
            container: getContainerDebugInfo(container),
          });
        }
        applyMeasuredTerminalGeometry(term, dims, {
          readySource: 'fit_fallback',
          readyReason: 'ready_fallback',
          resizeReason: 'fit',
        });
      },
      focus: () => {
        return focusTerminal();
      },
      typeTextViaInput: (text: string) => {
        return typeTextViaInput(text);
      },
      isInputFocused: () => {
        const container = containerRef.current;
        const textarea = container?.querySelector('textarea.xterm-helper-textarea') as HTMLTextAreaElement | null;
        return !!textarea && document.activeElement === textarea;
      },
      resetScrollPin: () => {
        const term = xtermRef.current;
        if (term) {
          resetTerminalScrollPin(term);
        }
      },
    }), [applyMeasuredTerminalGeometry, focusTerminal, logTerminal, typeTextViaInput]);

    useEffect(() => {
      if (!containerRef.current) return;

      // VS Code: Pre-calculate initial dimensions before creating terminal
      // This prevents xterm from initializing with default 80x24 and then resizing
      // Source: xtermTerminal.ts constructor receives cols/rows from options
      const containerWidth = containerRef.current.offsetWidth;
      const containerHeight = containerRef.current.offsetHeight;
      const initialFontSize = fontSizeRef.current;
      const { cols: initialCols, rows: initialRows } = getInitialTerminalDimensions(
        containerWidth,
        containerHeight,
        initialFontSize,
      );

      // Create terminal with VS Code configuration
      // Source: vscode/src/vs/workbench/contrib/terminal/browser/xterm/xtermTerminal.ts constructor
      const term = new XTerm({
        cols: initialCols,
        rows: initialRows,
        allowProposedApi: true,
        cursorBlink: true,
        fontSize: initialFontSize,
        fontFamily: FONT_FAMILY,
        scrollback: TERMINAL_SCROLLBACK_LINES,
        windowOptions: {
          getWinSizePixels: true,
          getCellSizePixels: true,
          getWinSizeChars: true,
        },
        overviewRuler: {
          width: 14,
          showTopBorder: true,
        },
        theme: getTerminalTheme(resolvedTheme),
        // Suppress xterm's default confirm() dialog for OSC 8 hyperlinks.
        // Actual opening is handled at the webview level.
        linkHandler: { activate: () => {} },
      });
      appliedFontSizeRef.current = initialFontSize;
      startupSnapshotRef.current = {
        initialContainer: {
          width: Math.round(containerWidth),
          height: Math.round(containerHeight),
        },
        initialCols,
        initialRows,
        firstObservedContainer: null,
        firstReadySource: null,
        firstReadyAt: null,
        firstReadyCols: null,
        firstReadyRows: null,
        fontEffectAppliedBeforeReady: false,
        skippedInitialFontEffect: false,
      };

      // WebLinksAddon decorates URLs (underline + pointer on hover).
      // No-op handler — actual opening is handled at the webview level.
      term.loadAddon(new WebLinksAddon(() => {}));

      // Enable Unicode 11 for correct emoji/CJK width calculation
      term.loadAddon(new Unicode11Addon());
      term.unicode.activeVersion = '11';

      // VS Code: open() FIRST, then load WebGL
      // Source: xtermTerminal.ts attachToElement() - WebGL is loaded AFTER raw.open()
      term.open(containerRef.current);
      const rendererLifecycle = installTerminalRendererLifecycle({
        term,
        rendererModeRef,
        setRendererDisplayMode,
        onContextLoss: () => {
          const container = containerRef.current;
          if (!container) {
            return;
          }
          requestAnimationFrame(() => {
            const dims = getScaledDimensions(container, term, fontSizeRef.current);
            if (dims) {
              resizeTerminal(term, dims.cols, dims.rows, 'webgl_context_loss');
            }
          });
        },
      });

      // Copy-on-select: copy selected text to clipboard automatically.
      // Markdown copy (Cmd+Shift+C) suppresses plain copy briefly to prevent races.
      let mdCopyUntil = 0;
      term.onSelectionChange(() => {
        if (performance.now() < mdCopyUntil) return;
        const selection = term.getSelection();
        if (selection) {
          const lines = selection.split('\n').map(line => line.trimEnd());
          navigator.clipboard.writeText(cleanTerminalLines(lines).join('\n'));
        }
      });

      // Cmd+Shift+C: copy selection as markdown (bold → **text**, etc.)
      // Uses window capture listener because xterm.js v6 doesn't invoke
      // attachCustomKeyEventHandler for meta-key combos.
      const handleMdCopy = (e: KeyboardEvent) => {
        if (e.key.toLowerCase() === 'c' && e.metaKey && e.shiftKey && !e.altKey && !e.ctrlKey) {
          if (term.hasSelection()) {
            mdCopyUntil = performance.now() + 200;
            navigator.clipboard.writeText(bufferSelectionToMarkdown(term));
            e.preventDefault();
            e.stopPropagation();
          }
        }
      };
      window.addEventListener('keydown', handleMdCopy, true);

      // Store ref immediately
      xtermRef.current = term;
      const runtimeMeta = runtimeLogMetaRef.current;
      if (runtimeMeta) {
        recordTerminalRuntimeLog({
          category: 'terminal',
          event: 'terminal.mounted',
          sessionId: runtimeMeta.sessionId,
          paneId: runtimeMeta.paneId,
          runtimeId: runtimeMeta.runtimeId,
          debugName: debugNameRef.current,
          message: 'xterm mounted',
          details: {
            paneKind: runtimeMeta.paneKind,
            cols: term.cols,
            rows: term.rows,
            renderer: rendererModeRef.current,
          },
        });
      }
      const activity = {
        renderCount: 0,
        writeParsedCount: 0,
        loggedRenderCount: 0,
        loggedWriteParsedCount: 0,
        lastRenderAt: 0,
        lastWriteParsedAt: 0,
        lastRenderRange: null as { start: number; end: number } | null,
        firstRenderLogged: false,
        firstWriteParsedLogged: false,
      };
      const renderDisposable = term.onRender((event) => {
        activity.renderCount += 1;
        activity.lastRenderAt = Date.now();
        activity.lastRenderRange = event;
        if (!activity.firstRenderLogged) {
          activity.firstRenderLogged = true;
          const meta = runtimeLogMetaRef.current;
          if (meta) {
            recordTerminalRuntimeLog({
              category: 'terminal',
              event: 'terminal.first_render',
              sessionId: meta.sessionId,
              paneId: meta.paneId,
              runtimeId: meta.runtimeId,
              debugName: debugNameRef.current,
              message: 'xterm rendered first frame',
              details: {
                paneKind: meta.paneKind,
                renderer: rendererModeRef.current,
              },
            });
          }
        }
      });
      const writeParsedDisposable = term.onWriteParsed(() => {
        activity.writeParsedCount += 1;
        activity.lastWriteParsedAt = Date.now();
        if (!activity.firstWriteParsedLogged) {
          activity.firstWriteParsedLogged = true;
          const meta = runtimeLogMetaRef.current;
          if (meta) {
            recordTerminalRuntimeLog({
              category: 'terminal',
              event: 'terminal.first_write_parsed',
              sessionId: meta.sessionId,
              paneId: meta.paneId,
              runtimeId: meta.runtimeId,
              debugName: debugNameRef.current,
              message: 'xterm parsed first write',
              details: {
                paneKind: meta.paneKind,
                renderer: rendererModeRef.current,
              },
            });
          }
        }
      });
      const heartbeatInterval = window.setInterval(() => {
        const meta = runtimeLogMetaRef.current;
        if (!meta || !meta.isActiveSession || !meta.isActivePane) {
          return;
        }
        const now = Date.now();
        const renderDelta = activity.renderCount - activity.loggedRenderCount;
        const writeParsedDelta = activity.writeParsedCount - activity.loggedWriteParsedCount;
        const msSinceRender = activity.lastRenderAt > 0 ? now - activity.lastRenderAt : null;
        const msSinceWriteParsed = activity.lastWriteParsedAt > 0 ? now - activity.lastWriteParsedAt : null;
        if (
          renderDelta === 0 &&
          writeParsedDelta === 0 &&
          (msSinceRender === null || msSinceRender < 4000) &&
          (msSinceWriteParsed === null || msSinceWriteParsed < 4000)
        ) {
          return;
        }
        const buffer = term.buffer.active;
        recordTerminalRuntimeLog({
          category: 'activity',
          sessionId: meta.sessionId,
          paneId: meta.paneId,
          runtimeId: meta.runtimeId,
          debugName: debugNameRef.current,
          message: 'xterm renderer heartbeat',
          details: () => ({
            paneKind: meta.paneKind,
            renderer: rendererModeRef.current,
            visible: visibleRef.current,
            cols: term.cols,
            rows: term.rows,
            bufferLength: buffer.length,
            baseY: buffer.baseY,
            viewportY: buffer.viewportY,
            renderDelta,
            writeParsedDelta,
            msSinceRender,
            msSinceWriteParsed,
            lastRenderRange: activity.lastRenderRange,
            writeQueueChunks: writeQueueChunksRef.current,
            writeQueueBytes: writeQueueBytesRef.current,
            ...activeElementSummary(),
          }),
        });
        activity.loggedRenderCount = activity.renderCount;
        activity.loggedWriteParsedCount = activity.writeParsedCount;
      }, 3000);
      const unregisterTerminalPerf = registerTerminalPerfGetter(perfRegistryIdRef.current, () => {
        const currentTerm = xtermRef.current;
        if (!currentTerm) {
          return null;
        }
        const buffer = currentTerm.buffer.active;
        const meta = runtimeLogMetaRef.current;
        const container = containerRef.current;
        const xterm = container?.querySelector('.xterm') || null;
        const xtermScreen = container?.querySelector('.xterm-screen') || null;
        const canvas = container?.querySelector('.xterm-screen canvas') || null;
        return {
          terminalName: debugNameRef.current,
          sessionId: meta?.sessionId ?? null,
          paneId: meta?.paneId ?? null,
          runtimeId: meta?.runtimeId ?? null,
          paneKind: meta?.paneKind ?? null,
          isActivePane: meta?.isActivePane ?? null,
          isActiveSession: meta?.isActiveSession ?? null,
          cols: currentTerm.cols,
          rows: currentTerm.rows,
          bufferLength: buffer.length,
          baseY: buffer.baseY,
          viewportY: buffer.viewportY,
          scrollbackLimit: TERMINAL_SCROLLBACK_LINES,
          renderer: rendererModeRef.current,
          visible: visibleRef.current,
          writeQueueChunks: writeQueueChunksRef.current,
          writeQueueBytes: writeQueueBytesRef.current,
          renderCount: activity.renderCount,
          writeParsedCount: activity.writeParsedCount,
          lastRenderAt: activity.lastRenderAt,
          lastWriteParsedAt: activity.lastWriteParsedAt,
          lastRenderRange: activity.lastRenderRange,
          ready: readyFiredRef.current,
          startup: startupSnapshotRef.current,
          lastResize: lastResizeSnapshotRef.current,
          dom: {
            container: elementSizeSnapshot(container),
            xterm: elementSizeSnapshot(xterm),
            xtermScreen: elementSizeSnapshot(xtermScreen),
            canvas: elementSizeSnapshot(canvas),
          },
        };
      });
      onInitRef.current?.(term);

      installTerminalScrollPin(term, containerRef.current, {
        onQueueStatsChange: ({ chunks, bytes }) => {
          writeQueueChunksRef.current = chunks;
          writeQueueBytesRef.current = bytes;
        },
      });
      const viewportLifecycle = installTerminalViewportLifecycle({
        term,
        container: containerRef.current,
        fontSizeRef,
        visibleRef,
        readyFiredRef,
        startupSnapshotRef,
        logTerminal,
        getContainerDebugInfo,
        applyMeasuredTerminalGeometry,
        resizeTerminal,
        onVisibilityChanged: (nowVisible, wasHidden) => {
          const meta = runtimeLogMetaRef.current;
          if (meta && meta.isActiveSession && meta.isActivePane) {
            recordTerminalRuntimeLog({
              category: 'terminal',
              event: 'terminal.visibility_changed',
              sessionId: meta.sessionId,
              paneId: meta.paneId,
              runtimeId: meta.runtimeId,
              debugName: debugNameRef.current,
              message: 'terminal visibility changed',
              details: {
                paneKind: meta.paneKind,
                nowVisible,
                wasHidden,
                readyFired: readyFiredRef.current,
              },
            });
          }
        },
      });

      // Cleanup
      return () => {
          const meta = runtimeLogMetaRef.current;
          if (meta) {
            const buffer = term.buffer.active;
            recordTerminalRuntimeLog({
              category: 'terminal',
              event: 'terminal.unmounted',
              sessionId: meta.sessionId,
              paneId: meta.paneId,
              runtimeId: meta.runtimeId,
              debugName: debugNameRef.current,
              message: 'xterm unmounted',
              details: {
                paneKind: meta.paneKind,
                renderer: rendererModeRef.current,
                cols: term.cols,
                rows: term.rows,
                bufferLength: buffer.length,
                baseY: buffer.baseY,
                viewportY: buffer.viewportY,
                renderCount: activity.renderCount,
                writeParsedCount: activity.writeParsedCount,
              },
            });
          }
          renderDisposable.dispose();
          writeParsedDisposable.dispose();
          window.clearInterval(heartbeatInterval);
          viewportLifecycle.dispose();
        window.removeEventListener('keydown', handleMdCopy, true);
        cleanupTerminalScrollPin(term);
        unregisterTerminalPerf();
          rendererLifecycle.dispose();
        readyFiredRef.current = false;
        lastResizeSnapshotRef.current = null;
        writeQueueChunksRef.current = 0;
        writeQueueBytesRef.current = 0;
        appliedFontSizeRef.current = null;
        startupSnapshotRef.current = createEmptyStartupSnapshot();
        xtermRef.current = null;
        term.dispose();
      };
    }, [applyMeasuredTerminalGeometry, logTerminal, resizeTerminal]);

    // Handle fontSize changes after terminal is created
    useEffect(() => {
      const term = xtermRef.current;
      const container = containerRef.current;
      if (!term || !container) return;

      if (appliedFontSizeRef.current === fontSize) {
        if (!readyFiredRef.current) {
          startupSnapshotRef.current.skippedInitialFontEffect = true;
        }
        return;
      }

      // Update xterm font size
      term.options.fontSize = fontSize;
      appliedFontSizeRef.current = fontSize;

      // Recalculate dimensions with new font size
      const dims = getScaledDimensions(container, term, fontSize);
      if (dims) {
        if (isSuspiciousTerminalSize(dims.cols, dims.rows)) {
          logTerminal('warn', 'fontSize resize produced suspicious dimensions', {
            cols: dims.cols,
            rows: dims.rows,
            fontSize,
            isVisible: visibleRef.current,
            container: getContainerDebugInfo(container),
          });
        }
        if (!readyFiredRef.current) {
          startupSnapshotRef.current.fontEffectAppliedBeforeReady = true;
        }
        applyMeasuredTerminalGeometry(term, dims, {
          readySource: 'font_change',
          readyReason: 'font-change',
          resizeReason: 'font-change',
        });
      }
    }, [applyMeasuredTerminalGeometry, fontSize, logTerminal]);

    const handleCopyDebugLog = useCallback(() => {
      const log = formatResizeLog();
      navigator.clipboard.writeText(log).then(
        () => logTerminal('log', 'Terminal debug log copied to clipboard'),
        (err) => logTerminal('warn', 'Failed to copy resize log', { error: String(err) }),
      );
    }, [logTerminal]);

    const handleToggleRenderer = useCallback(() => {
      const nextMode = getTerminalRendererConfig().mode === 'webgl' ? 'dom' : 'webgl';
      setTerminalRendererConfig(nextMode);
    }, []);

    return (
      <div ref={containerRef} className="terminal-container">
        {debugDisplay && (
          <div className="terminal-debug-badge">
            <span className="terminal-debug-dims">{debugDisplay.cols}×{debugDisplay.rows}</span>
            <span className="terminal-debug-sep">|</span>
            <span>ctr:{Math.round(debugDisplay.containerWidth)}×{Math.round(debugDisplay.containerHeight)}</span>
            <span className="terminal-debug-sep">|</span>
            <span>cell:{debugDisplay.cellWidth.toFixed(1)} ({debugDisplay.cellSource})</span>
            <span className="terminal-debug-sep">|</span>
            <span>font:{debugDisplay.fontSize}</span>
            <span className="terminal-debug-sep">|</span>
            <span>rend:{rendererDisplayMode}</span>
            <span className="terminal-debug-sep">|</span>
            <span className="terminal-debug-trigger">{debugDisplay.trigger}</span>
            <button className="terminal-debug-toggle" onClick={handleToggleRenderer}>
                Renderer {rendererConfig.mode === 'webgl' ? 'WebGL' : 'DOM'}
              </button>
            <button className="terminal-debug-copy" onClick={handleCopyDebugLog}>Copy</button>
          </div>
        )}
      </div>
    );
  }
);
