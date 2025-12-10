import { useEffect, useRef, useImperativeHandle, forwardRef } from 'react';
import { Terminal as XTerm } from '@xterm/xterm';
import { WebLinksAddon } from '@xterm/addon-web-links';
import { WebglAddon } from '@xterm/addon-webgl';
import '@xterm/xterm/css/xterm.css';
import './Terminal.css';
import { diagLog, clearDiagLog } from '../utils/diagLog';

// Terminal font configuration (matches xterm options)
const FONT_FAMILY = 'Menlo, Monaco, "Courier New", monospace';
const FONT_SIZE = 14;

// VS Code limits canvas width to prevent performance issues with very wide terminals
// Source: Constants.MaxCanvasWidth in terminalInstance.ts (line 103)
const MAX_CANVAS_WIDTH = 4096;

/**
 * Measure font dimensions using DOM measurement.
 * This is VS Code's fallback when xterm renderer isn't ready.
 * Source: terminalConfigurationService.ts _measureFont()
 */
function measureFont(
  fontFamily: string,
  fontSize: number
): { charWidth: number; charHeight: number } {
  const span = document.createElement('span');
  span.style.fontFamily = fontFamily;
  span.style.fontSize = `${fontSize}px`;
  span.style.position = 'absolute';
  span.style.visibility = 'hidden';
  span.style.whiteSpace = 'pre';
  // Use a string of characters to get average width
  span.textContent = 'W'.repeat(50);

  document.body.appendChild(span);
  const rect = span.getBoundingClientRect();
  document.body.removeChild(span);

  return {
    charWidth: rect.width / 50,
    charHeight: rect.height,
  };
}

export interface TerminalHandle {
  terminal: XTerm | null;
  fit: () => void;
  focus: () => void;
}

interface TerminalProps {
  onReady?: (terminal: XTerm) => void;
  onResize?: (cols: number, rows: number) => void;
}

// Diagnostic logging counter
let diagLogId = 0;

/**
 * Calculate terminal dimensions exactly like VS Code does.
 * Source: vscode/src/vs/workbench/contrib/terminal/browser/terminalInstance.ts
 * Functions: _getDimension, getXtermScaledDimensions
 *
 * VS Code's flow:
 * 1. Get container dimensions
 * 2. Subtract padding from xterm element + scrollbar space
 * 3. Calculate cols/rows using devicePixelRatio
 */
function getScaledDimensions(
  container: HTMLElement,
  term: XTerm,
  letterSpacing = 0,
  lineHeight = 1,
  caller = 'unknown'
): { cols: number; rows: number } | null {
  const logId = ++diagLogId;
  const log = (step: string, data: Record<string, unknown>) => {
    diagLog(`${logId}-${caller}-${step}`, data);
  };

  // STEP 1: Get container dimensions
  const containerStyle = getComputedStyle(container);
  let width = parseFloat(containerStyle.width);
  let height = parseFloat(containerStyle.height);
  const originalWidth = width;
  const originalHeight = height;

  // VS Code: Limit canvas width to prevent performance issues
  width = Math.min(width, MAX_CANVAS_WIDTH);

  if (width <= 0 || height <= 0) {
    log('EARLY_EXIT', { reason: 'invalid container size', width, height });
    return null;
  }

  // STEP 2: Subtract padding from xterm element (like VS Code does)
  // Source: terminalInstance.ts _getDimension() line 730
  // VS Code ALWAYS uses 14px for scrollbar padding, regardless of scrollback or overviewRuler settings
  const xtermElement = term.element;
  const scrollbarWidth = 14;
  let horizontalPadding = scrollbarWidth;
  let verticalPadding = 0;

  if (xtermElement) {
    const xtermStyle = getComputedStyle(xtermElement);
    const paddingLeft = parseFloat(xtermStyle.paddingLeft || '0');
    const paddingRight = parseFloat(xtermStyle.paddingRight || '0');
    const paddingTop = parseFloat(xtermStyle.paddingTop || '0');
    const paddingBottom = parseFloat(xtermStyle.paddingBottom || '0');

    horizontalPadding = paddingLeft + paddingRight + scrollbarWidth;
    verticalPadding = paddingTop + paddingBottom;

    width = width - horizontalPadding;
    height = height - verticalPadding;
  }

  if (width <= 0 || height <= 0) {
    log('EARLY_EXIT', { reason: 'invalid size after padding', width, height });
    return null;
  }

  // STEP 3: Get char dimensions - try xterm first, fallback to DOM measurement
  // Source: terminalConfigurationService.ts getFont()
  const core = (term as any)._core;
  const cellDims = core?._renderService?.dimensions?.css?.cell;
  const dpr = window.devicePixelRatio;

  let charWidth: number;
  let charHeight: number;
  let charSource: string;

  if (cellDims?.width && cellDims?.height) {
    // Primary: Use xterm's renderer dimensions
    charWidth = cellDims.width - Math.round(letterSpacing) / dpr;
    charHeight = cellDims.height / lineHeight;
    charSource = 'xterm-renderer';
  } else {
    // Fallback: Measure font via DOM (VS Code's _measureFont approach)
    const measured = measureFont(FONT_FAMILY, FONT_SIZE);
    charWidth = measured.charWidth;
    charHeight = measured.charHeight;
    charSource = 'dom-fallback';
  }

  if (charWidth <= 0 || charHeight <= 0) {
    log('EARLY_EXIT', { reason: 'invalid char dimensions', charWidth, charHeight, charSource });
    return null;
  }

  // STEP 4: Calculate cols/rows with VS Code's formula
  // Source: xtermTerminal.ts getXtermScaledDimensions()
  const scaledWidthAvailable = width * dpr;
  const scaledCharWidth = charWidth * dpr + letterSpacing;
  const cols = Math.max(Math.floor(scaledWidthAvailable / scaledCharWidth), 1);

  const scaledHeightAvailable = height * dpr;
  const scaledCharHeight = Math.ceil(charHeight * dpr);
  const scaledLineHeight = Math.floor(scaledCharHeight * lineHeight);
  const rows = Math.max(Math.floor(scaledHeightAvailable / scaledLineHeight), 1);

  log('RESULT', {
    container: { original: { w: originalWidth, h: originalHeight }, afterPadding: { w: width, h: height } },
    padding: { horizontal: horizontalPadding, vertical: verticalPadding },
    char: { width: charWidth, height: charHeight, source: charSource },
    dpr,
    currentXterm: { cols: term.cols, rows: term.rows },
    calculated: { cols, rows },
  });

  return { cols, rows };
}

export const Terminal = forwardRef<TerminalHandle, TerminalProps>(
  function Terminal({ onReady, onResize }, ref) {
    const containerRef = useRef<HTMLDivElement>(null);
    const xtermRef = useRef<XTerm | null>(null);
    const webglAddonRef = useRef<WebglAddon | null>(null);

    // Store callbacks in refs to avoid re-running effect when they change
    const onReadyRef = useRef(onReady);
    const onResizeRef = useRef(onResize);

    useEffect(() => {
      onReadyRef.current = onReady;
      onResizeRef.current = onResize;
    });

    // Helper to resize terminal and notify PTY
    // VS Code pattern: xterm.resize() first, then PTY update
    const resizeTerminal = (term: XTerm, cols: number, rows: number) => {
      if (cols !== term.cols || rows !== term.rows) {
        diagLog('resizeTerminal', { from: { cols: term.cols, rows: term.rows }, to: { cols, rows } });
        // Step 1: Resize xterm first
        term.resize(cols, rows);
        // Step 2: Then notify PTY (sends SIGWINCH)
        if (onResizeRef.current) {
          onResizeRef.current(cols, rows);
        }
      }
    };

    useImperativeHandle(ref, () => ({
      terminal: xtermRef.current,
      fit: () => {
        const term = xtermRef.current;
        const container = containerRef.current;
        if (!term || !container) return;

        const dims = getScaledDimensions(container, term, 0, 1, 'imperative-fit');
        if (dims) {
          resizeTerminal(term, dims.cols, dims.rows);
        }
      },
      focus: () => {
        xtermRef.current?.focus();
      },
    }));

    useEffect(() => {
      if (!containerRef.current) return;

      // VS Code: Pre-calculate initial dimensions before creating terminal
      // This prevents xterm from initializing with default 80x24 and then resizing
      // Source: xtermTerminal.ts constructor receives cols/rows from options
      const containerStyle = getComputedStyle(containerRef.current);
      const containerWidth = parseFloat(containerStyle.width);
      const containerHeight = parseFloat(containerStyle.height);
      const measured = measureFont(FONT_FAMILY, FONT_SIZE);
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

      // Clear previous log and start fresh
      clearDiagLog();
      diagLog('INITIAL_DIMS', {
        container: { width: containerWidth, height: containerHeight },
        measured: { charWidth: measured.charWidth, charHeight: measured.charHeight },
        dpr,
        initialCols,
        initialRows,
      });

      // Create terminal with VS Code configuration
      // Source: vscode/src/vs/workbench/contrib/terminal/browser/xterm/xtermTerminal.ts constructor
      const term = new XTerm({
        cols: initialCols,
        rows: initialRows,
        allowProposedApi: true,
        cursorBlink: true,
        fontSize: FONT_SIZE,
        fontFamily: FONT_FAMILY,
        scrollback: 10000,
        // VS Code options
        fastScrollModifier: 'alt',
        windowOptions: {
          getWinSizePixels: true,
          getCellSizePixels: true,
          getWinSizeChars: true,
        },
        overviewRuler: {
          width: 14,
          showTopBorder: true,
        },
        theme: {
          background: '#1e1e1e',
          foreground: '#d4d4d4',
          cursor: '#d4d4d4',
          cursorAccent: '#1e1e1e',
          selectionBackground: '#264f78',
        },
      });

      // Load WebLinksAddon before open
      term.loadAddon(new WebLinksAddon());

      // VS Code: open() FIRST, then load WebGL
      // Source: xtermTerminal.ts attachToElement() - WebGL is loaded AFTER raw.open()
      term.open(containerRef.current);

      // Load WebGL addon for better performance (after open, like VS Code)
      let webglAddon: WebglAddon | null = null;
      try {
        webglAddon = new WebglAddon();
        webglAddon.onContextLoss(() => {
          console.info('[Terminal] WebGL context lost, disposing and falling back to DOM');
          webglAddon?.dispose();
          webglAddonRef.current = null;
          // VS Code: trigger dimension refresh since DOM renderer has different cell dimensions
          const container = containerRef.current;
          if (container && term) {
            requestAnimationFrame(() => {
              const dims = getScaledDimensions(container, term, 0, 1, 'webgl-context-loss');
              if (dims) {
                resizeTerminal(term, dims.cols, dims.rows);
              }
            });
          }
        });
        term.loadAddon(webglAddon);
        webglAddonRef.current = webglAddon;
      } catch (e) {
        console.warn('[Terminal] WebGL addon failed:', e);
      }

      // Store ref immediately
      xtermRef.current = term;

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
      let xResizeTimeout: number;
      let isVisible = true;
      let readyFired = false;

      // VS Code constants
      const START_DEBOUNCING_THRESHOLD = 200; // buffer lines
      const X_AXIS_DEBOUNCE_MS = 100;

      // Resize both dimensions immediately
      const resizeBoth = (cols: number, rows: number) => {
        lastCols = cols;
        lastRows = rows;
        diagLog('RESIZE_BOTH', { cols, rows });
        resizeTerminal(term, cols, rows);
      };

      // Resize X only (debounced)
      const resizeX = (cols: number) => {
        lastCols = cols;
        diagLog('RESIZE_X', { cols, currentRows: term.rows });
        resizeTerminal(term, cols, term.rows);
      };

      // Resize Y only (immediate)
      const resizeY = (rows: number) => {
        lastRows = rows;
        resizeTerminal(term, term.cols, rows);
      };

      const handleResize = () => {
        const container = containerRef.current;
        if (!container) return;

        const dims = getScaledDimensions(container, term, 0, 1, 'handleResize');
        if (!dims) return;

        const { cols, rows } = dims;
        latestX = cols;
        latestY = rows;

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
        diagLog('ResizeObserver', {
          contentRect: entry ? { w: entry.contentRect.width, h: entry.contentRect.height } : null,
          readyFired,
          currentXterm: { cols: term.cols, rows: term.rows },
        });
        if (!entry || entry.contentRect.width <= 0 || entry.contentRect.height <= 0) {
          return;
        }

        // First time we get valid dimensions: fire onReady
        if (!readyFired) {
          // Wait one frame for renderer to initialize cell dimensions
          requestAnimationFrame(() => {
            const dims = getScaledDimensions(containerRef.current!, term, 0, 1, 'ResizeObserver-onReady');
            if (dims && dims.cols > 0 && dims.rows > 0) {
              readyFired = true;
              lastCols = dims.cols;
              lastRows = dims.rows;

              // Resize to calculated dimensions
              term.resize(dims.cols, dims.rows);

              if (onResizeRef.current) {
                onResizeRef.current(dims.cols, dims.rows);
              }

              if (onReadyRef.current) {
                onReadyRef.current(term);
              }
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

          // VS Code pattern: flush pending resizes when becoming visible
          if (wasHidden && readyFired) {
            clearTimeout(xResizeTimeout);
            const container = containerRef.current;
            if (container) {
              const dims = getScaledDimensions(container, term, 0, 1, 'visibility-flush');
              if (dims) {
                lastCols = dims.cols;
                lastRows = dims.rows;
                resizeTerminal(term, dims.cols, dims.rows);
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
            const dims = getScaledDimensions(container, term, 0, 1, 'dpr-change');
            if (dims) {
              resizeTerminal(term, dims.cols, dims.rows);
            }
          }
        }
      };
      // matchMedia with resolution query triggers on DPI change
      const dprMediaQuery = window.matchMedia(`(resolution: ${window.devicePixelRatio}dppx)`);
      dprMediaQuery.addEventListener('change', handleDprChange);

      // Cleanup
      return () => {
        resizeObserver.disconnect();
        visibilityObserver.disconnect();
        clearTimeout(xResizeTimeout);
        dprMediaQuery.removeEventListener('change', handleDprChange);
        webglAddon?.dispose();
        term.dispose();
      };
    }, []);

    return <div ref={containerRef} className="terminal-container" />;
  }
);
