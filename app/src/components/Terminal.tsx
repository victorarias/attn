import { useEffect, useRef, useImperativeHandle, forwardRef, useCallback, useState } from 'react';
import { Terminal as XTerm } from '@xterm/xterm';
import { WebLinksAddon } from '@xterm/addon-web-links';
import { WebglAddon } from '@xterm/addon-webgl';
import { Unicode11Addon } from '@xterm/addon-unicode11';
import { openUrl } from '@tauri-apps/plugin-opener';
import '@xterm/xterm/css/xterm.css';
import './Terminal.css';
import { isSuspiciousTerminalSize, isTerminalDebugEnabled, recordResizeEvent, formatResizeLog, type ResizeDiagnostics } from '../utils/terminalDebug';
import { activeElementSummary } from '../utils/paneRuntimeDebug';
import { cleanTerminalLines, bufferSelectionToMarkdown } from '../utils/terminalMarkdown';
import { registerTerminalPerfGetter } from '../utils/terminalPerf';
import {
  DEFAULT_FONT_SIZE,
  FONT_FAMILY,
  getScaledDimensions,
  getTerminalTheme,
  MAX_CANVAS_WIDTH,
  measureTerminalFont,
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

async function openExternalUri(uri: string): Promise<void> {
  try {
    await openUrl(uri);
  } catch (error) {
    console.error('[Terminal] Failed to open external URL:', uri, error);
  }
}

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
  onResize?: (cols: number, rows: number, options?: { forceRedraw?: boolean; reason?: string }) => void;
}

export const Terminal = forwardRef<TerminalHandle, TerminalProps>(
  function Terminal({ fontSize = DEFAULT_FONT_SIZE, resolvedTheme = 'dark', debugName, runtimeLogMeta, tuiCursor, onInit, onReady, onResize }, ref) {
    const containerRef = useRef<HTMLDivElement>(null);
    const xtermRef = useRef<XTerm | null>(null);
    const webglAddonRef = useRef<WebglAddon | null>(null);
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
    const resolvedThemeRef = useRef(resolvedTheme);
    const tuiCursorRef = useRef(tuiCursor);
    const runtimeLogMetaRef = useRef(runtimeLogMeta);

    useEffect(() => {
      onReadyRef.current = onReady;
      onInitRef.current = onInit;
      onResizeRef.current = onResize;
      fontSizeRef.current = fontSize;
      resolvedThemeRef.current = resolvedTheme;
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
        if (!readyFiredRef.current) {
          markTerminalReady(term, dims.cols, dims.rows, dims.diagnostics, 'fit_fallback', 'ready_fallback');
          return;
        }
        const sizeChanged = dims.cols !== term.cols || dims.rows !== term.rows;
        resizeTerminal(term, dims.cols, dims.rows, 'fit', dims.diagnostics);
        if (!sizeChanged) {
          onResizeRef.current?.(dims.cols, dims.rows, {
            forceRedraw: true,
            reason: 'fit_same_size',
          });
        }
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
    }), [focusTerminal, logTerminal, markTerminalReady, resizeTerminal, typeTextViaInput]);

    useEffect(() => {
      if (!containerRef.current) return;

      // VS Code: Pre-calculate initial dimensions before creating terminal
      // This prevents xterm from initializing with default 80x24 and then resizing
      // Source: xtermTerminal.ts constructor receives cols/rows from options
      const containerWidth = containerRef.current.offsetWidth;
      const containerHeight = containerRef.current.offsetHeight;
      const initialFontSize = fontSizeRef.current;
      const measured = measureTerminalFont(FONT_FAMILY, initialFontSize);
      const dpr = window.devicePixelRatio;

      // Calculate initial cols/rows using same logic as getScaledDimensions
      // but without needing the terminal reference
      let initialCols = 80; // fallback
      let initialRows = 24; // fallback

      if (containerWidth > 0 && containerHeight > 0 && measured.charWidth > 0 && measured.charHeight > 0) {
        const availableWidth = Math.min(containerWidth, MAX_CANVAS_WIDTH) - 14; // scrollbar
        const availableHeight = containerHeight;

        const scaledWidthAvailable = availableWidth * dpr;
        const scaledCharWidth = measured.charWidth * dpr;
        initialCols = Math.max(Math.floor(scaledWidthAvailable / scaledCharWidth), 1);

        const scaledHeightAvailable = availableHeight * dpr;
        const scaledCharHeight = Math.ceil(measured.charHeight * dpr);
        initialRows = Math.max(Math.floor(scaledHeightAvailable / scaledCharHeight), 1);
      }

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
        // Handle OSC 8 hyperlinks without the default confirm prompt + window.open fallback.
        linkHandler: {
          activate: (event, text) => {
            if (event.metaKey || event.ctrlKey) {
              void openExternalUri(text);
            }
          },
        },
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

      // Load WebLinksAddon before open - Cmd/Ctrl+click to open URLs
      term.loadAddon(new WebLinksAddon(async (event, uri) => {
        if (event.metaKey || event.ctrlKey) {
          await openExternalUri(uri);
        }
      }));

      // Enable Unicode 11 for correct emoji/CJK width calculation
      term.loadAddon(new Unicode11Addon());
      term.unicode.activeVersion = '11';

      // VS Code: open() FIRST, then load WebGL
      // Source: xtermTerminal.ts attachToElement() - WebGL is loaded AFTER raw.open()
      term.open(containerRef.current);

      const detachWebglRenderer = () => {
        webglAddonRef.current?.dispose();
        webglAddonRef.current = null;
        rendererModeRef.current = 'dom';
        setRendererDisplayMode('dom');
      };

      const attachWebglRenderer = () => {
          if (webglAddonRef.current) {
            return;
          }
        try {
          const nextWebglAddon = new WebglAddon();
          nextWebglAddon.onContextLoss(() => {
            console.info('[Terminal] WebGL context lost, disposing and falling back to DOM');
              detachWebglRenderer();
            // VS Code: trigger dimension refresh since DOM renderer has different cell dimensions
            const container = containerRef.current;
            if (container && term) {
              requestAnimationFrame(() => {
                const dims = getScaledDimensions(container, term, fontSizeRef.current);
                if (dims) {
                  resizeTerminal(term, dims.cols, dims.rows, 'webgl_context_loss');
                }
              });
            }
          });
          term.loadAddon(nextWebglAddon);
          webglAddonRef.current = nextWebglAddon;
          rendererModeRef.current = 'webgl';
            setRendererDisplayMode('webgl');
        } catch (e) {
          console.warn('[Terminal] WebGL addon failed:', e);
            detachWebglRenderer();
        }
      };

        if (getTerminalRendererConfig().mode === 'webgl') {
          attachWebglRenderer();
        } else {
          detachWebglRenderer();
        }

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
          details: {
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
          },
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
      // Resize strategy from VS Code's TerminalResizeDebouncer:
      // - Y-axis (rows): immediate (cheap operation)
      // - X-axis (cols): 100ms debounce (expensive text reflow)
      // - Small buffers (<200 lines): immediate for both
      // - Hidden terminals: use requestIdleCallback
      let lastCols = term.cols;
      let lastRows = term.rows;
      // Store latest requested values (VS Code pattern)
      let latestX = term.cols;
      let latestY = term.rows;
      let latestDiagnostics: ResizeDiagnostics | null = null;
      let xResizeTimeout: number;
      let isVisible = true;
      // VS Code constants
      const START_DEBOUNCING_THRESHOLD = 200; // buffer lines
      const X_AXIS_DEBOUNCE_MS = 100;

      // Resize both dimensions immediately
      const resizeBoth = (cols: number, rows: number) => {
        lastCols = cols;
        lastRows = rows;
        resizeTerminal(term, cols, rows, 'resize_both', latestDiagnostics);
      };

      // Resize X only (debounced)
      const resizeX = (cols: number) => {
        lastCols = cols;
        resizeTerminal(term, cols, term.rows, 'resize_x', latestDiagnostics);
      };

      // Resize Y only (immediate)
      const resizeY = (rows: number) => {
        lastRows = rows;
        resizeTerminal(term, term.cols, rows, 'resize_y', latestDiagnostics);
      };

      const handleResize = () => {
        const container = containerRef.current;
        if (!container) return;

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

        const colsChanged = cols !== lastCols;
        const rowsChanged = rows !== lastRows;

        if (!colsChanged && !rowsChanged) return;

        // VS Code: Immediate resize for small buffers
        const bufferLength = term.buffer.normal.length;
        if (bufferLength < START_DEBOUNCING_THRESHOLD) {
          clearTimeout(xResizeTimeout);
          resizeBoth(cols, rows);
          return;
        }

        // VS Code: If terminal is not visible, defer to idle callback
        if (!isVisible && 'requestIdleCallback' in window) {
          (window as any).requestIdleCallback(() => {
            resizeBoth(latestX, latestY);
          });
          return;
        }

        // VS Code split resize strategy:
        // Y-axis is immediate (cheap), X-axis is debounced (expensive reflow)
        if (rowsChanged) {
          resizeY(rows);
        }

        if (colsChanged) {
          clearTimeout(xResizeTimeout);
          xResizeTimeout = window.setTimeout(() => {
            resizeX(latestX);
          }, X_AXIS_DEBOUNCE_MS);
        }
      };

      // CRITICAL FIX: Use ResizeObserver on container for ALL resize detection
      // This catches: window resize, sidebar collapse, display changes, parent layout changes
      // VS Code uses a top-down layout system; we use ResizeObserver as the equivalent
      const resizeObserver = new ResizeObserver((entries) => {
        const entry = entries[0];
        if (!entry || entry.contentRect.width <= 0 || entry.contentRect.height <= 0) {
          if (entry && isTerminalDebugEnabled()) {
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

        // First time we get valid dimensions: fire onReady
        if (!readyFiredRef.current) {
          // Wait one frame for renderer to initialize cell dimensions
          requestAnimationFrame(() => {
            const dims = getScaledDimensions(containerRef.current!, term, fontSizeRef.current);
            if (dims && dims.cols > 0 && dims.rows > 0) {
              lastCols = dims.cols;
              lastRows = dims.rows;
              markTerminalReady(term, dims.cols, dims.rows, dims.diagnostics, 'resize_observer', 'ready');
            }
          });
          return;
        }

        // After ready: handle resize with VS Code debouncing strategy
        handleResize();
      });
      resizeObserver.observe(containerRef.current);

      // VS Code: Track visibility to defer resizes when hidden
      // Source: terminalInstance.ts setVisible() - flushes pending resizes when becoming visible
      const visibilityObserver = new IntersectionObserver(
        (entries) => {
          const nowVisible = entries[0]?.isIntersecting ?? true;
          const wasHidden = !isVisible && nowVisible;
          isVisible = nowVisible;
          visibleRef.current = nowVisible;
          logTerminal('log', 'Visibility changed', { nowVisible, wasHidden, readyFired: readyFiredRef.current });
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

          // VS Code pattern: flush pending resizes when becoming visible
          if (wasHidden && readyFiredRef.current) {
            clearTimeout(xResizeTimeout);
            const container = containerRef.current;
            if (container) {
              const dims = getScaledDimensions(container, term, fontSizeRef.current);
              if (dims) {
                lastCols = dims.cols;
                lastRows = dims.rows;
                if (dims.cols === term.cols && dims.rows === term.rows) {
                  onResizeRef.current?.(dims.cols, dims.rows, {
                    forceRedraw: true,
                    reason: 'visibility_flush_same_size',
                  });
                } else {
                  resizeTerminal(term, dims.cols, dims.rows, 'visibility_flush', dims.diagnostics);
                }
              }
            }
          }
        },
        { threshold: 0 }
      );
      visibilityObserver.observe(containerRef.current);

      // VS Code: Listen for DPI changes (when window moves between displays)
      // Source: terminalInstance.ts - uses matchMedia for resolution changes
      let currentDpr = window.devicePixelRatio;
      const handleDprChange = () => {
        const newDpr = window.devicePixelRatio;
        if (newDpr !== currentDpr) {
          currentDpr = newDpr;
          const container = containerRef.current;
          if (container) {
            const dims = getScaledDimensions(container, term, fontSizeRef.current);
            if (dims) {
              resizeTerminal(term, dims.cols, dims.rows, 'dpr_change');
            }
          }
        }
      };
      // matchMedia with resolution query triggers on DPI change
      const dprMediaQuery = window.matchMedia(`(resolution: ${window.devicePixelRatio}dppx)`);
      dprMediaQuery.addEventListener('change', handleDprChange);

        const unsubscribeRendererConfig = subscribeTerminalRendererConfig(() => {
          const nextMode = getTerminalRendererConfig().mode;
          if (nextMode === 'webgl') {
            attachWebglRenderer();
            return;
          }
          detachWebglRenderer();
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
          unsubscribeRendererConfig();
          resizeObserver.disconnect();
          visibilityObserver.disconnect();
          clearTimeout(xResizeTimeout);
          dprMediaQuery.removeEventListener('change', handleDprChange);
        window.removeEventListener('keydown', handleMdCopy, true);
        cleanupTerminalScrollPin(term);
        unregisterTerminalPerf();
          detachWebglRenderer();
        readyFiredRef.current = false;
        lastResizeSnapshotRef.current = null;
        writeQueueChunksRef.current = 0;
        writeQueueBytesRef.current = 0;
        appliedFontSizeRef.current = null;
        startupSnapshotRef.current = createEmptyStartupSnapshot();
        xtermRef.current = null;
        term.dispose();
      };
    }, [logTerminal, markTerminalReady, resizeTerminal]);

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
        recordDiags('font-change', dims.cols, dims.rows, term.cols, term.rows, dims.diagnostics);
        term.resize(dims.cols, dims.rows);
        onResizeRef.current?.(dims.cols, dims.rows, { reason: 'font-change' });
        if (!readyFiredRef.current) {
          startupSnapshotRef.current.fontEffectAppliedBeforeReady = true;
          markTerminalReady(term, dims.cols, dims.rows, dims.diagnostics, 'font_change', 'font-change');
        }
      }
    }, [fontSize, markTerminalReady, recordDiags]);

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
