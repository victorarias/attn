import type { Terminal as XTerm } from '@xterm/xterm';
import { type ResizeDiagnostics } from './terminalDebug';

export type ResolvedTheme = 'dark' | 'light';

export const FONT_FAMILY = 'Iosevka, Menlo, Monaco, "Courier New", monospace';
export const DEFAULT_FONT_SIZE = 14;
export const TERMINAL_SCROLLBACK_LINES = 50000;
export const DEFAULT_TERMINAL_COLS = 80;
export const DEFAULT_TERMINAL_ROWS = 24;
export const TERMINAL_SCROLLBAR_WIDTH = 14;

// VS Code limits canvas width to prevent performance issues with very wide terminals
// Source: Constants.MaxCanvasWidth in terminalInstance.ts (line 103)
export const MAX_CANVAS_WIDTH = 4096;

export const DARK_TERMINAL_THEME = {
  background: '#1e1e1e',
  foreground: '#d4d4d4',
  cursor: '#d4d4d4',
  cursorAccent: '#1e1e1e',
  selectionBackground: '#264f78',
};

export const LIGHT_TERMINAL_THEME = {
  background: '#ffffff',
  foreground: '#3b3b3b',
  cursor: '#3b3b3b',
  cursorAccent: '#ffffff',
  selectionBackground: '#add6ff',
  // ANSI colors tuned for white background contrast (VS Code light theme)
  black: '#000000',
  red: '#cd3131',
  green: '#00bc00',
  yellow: '#949800',
  blue: '#0451a5',
  magenta: '#bc05bc',
  cyan: '#0598bc',
  white: '#555555',
  brightBlack: '#666666',
  brightRed: '#cd3131',
  brightGreen: '#14ce14',
  brightYellow: '#b5ba00',
  brightBlue: '#0451a5',
  brightMagenta: '#bc05bc',
  brightCyan: '#0598bc',
  brightWhite: '#a5a5a5',
};

export function getTerminalTheme(resolvedTheme: ResolvedTheme) {
  return resolvedTheme === 'light' ? LIGHT_TERMINAL_THEME : DARK_TERMINAL_THEME;
}

interface GridDimensionInput {
  availableWidth: number;
  availableHeight: number;
  charWidth: number;
  charHeight: number;
  dpr: number;
  letterSpacing?: number;
  lineHeight?: number;
}

function computeGridDimensions({
  availableWidth,
  availableHeight,
  charWidth,
  charHeight,
  dpr,
  letterSpacing = 0,
  lineHeight = 1,
}: GridDimensionInput): { cols: number; rows: number } | null {
  if (availableWidth <= 0 || availableHeight <= 0 || charWidth <= 0 || charHeight <= 0) {
    return null;
  }

  const scaledWidthAvailable = availableWidth * dpr;
  const scaledCharWidth = charWidth * dpr + letterSpacing;
  const cols = Math.max(Math.floor(scaledWidthAvailable / scaledCharWidth), 1);

  const scaledHeightAvailable = availableHeight * dpr;
  const scaledCharHeight = Math.ceil(charHeight * dpr);
  const scaledLineHeight = Math.floor(scaledCharHeight * lineHeight);
  const rows = Math.max(Math.floor(scaledHeightAvailable / scaledLineHeight), 1);

  return { cols, rows };
}

/**
 * Measure font dimensions using DOM measurement.
 * This is VS Code's fallback when xterm renderer isn't ready.
 * Source: terminalConfigurationService.ts _measureFont()
 */
export function measureTerminalFont(
  fontFamily: string,
  fontSize: number,
): { charWidth: number; charHeight: number } {
  const span = document.createElement('span');
  span.style.fontFamily = fontFamily;
  span.style.fontSize = `${fontSize}px`;
  span.style.position = 'absolute';
  span.style.visibility = 'hidden';
  span.style.whiteSpace = 'pre';
  span.textContent = 'W'.repeat(50);

  document.body.appendChild(span);
  const rect = span.getBoundingClientRect();
  document.body.removeChild(span);

  return {
    charWidth: rect.width / 50,
    charHeight: rect.height,
  };
}

export function getInitialTerminalDimensions(
  containerWidth: number,
  containerHeight: number,
  fontSize: number,
  dpr = window.devicePixelRatio,
): { cols: number; rows: number } {
  const measured = measureTerminalFont(FONT_FAMILY, fontSize);
  const dims = computeGridDimensions({
    availableWidth: Math.min(containerWidth, MAX_CANVAS_WIDTH) - TERMINAL_SCROLLBAR_WIDTH,
    availableHeight: containerHeight,
    charWidth: measured.charWidth,
    charHeight: measured.charHeight,
    dpr,
  });

  return dims ?? {
    cols: DEFAULT_TERMINAL_COLS,
    rows: DEFAULT_TERMINAL_ROWS,
  };
}

/**
 * Snapshot of layout dimensions used to compute a "fair share per pane" floor.
 * Lets us reject layout-implausible measurements (e.g. 10 cols on a 1200px
 * window with one pane) without rejecting legitimately-small splits (e.g. 14
 * cols when there are 3 panes).
 */
export interface LayoutContext {
  /** Window inner width in CSS px. */
  windowWidth: number;
  /** Window inner height in CSS px. */
  windowHeight: number;
  /** Total panes currently rendered in this terminal's workspace. */
  paneCount: number;
}

/**
 * Fraction of the per-pane fair share we accept as a legitimate measurement.
 * Anything below is treated as a transient layout state (panel animation,
 * grid track resolving, sidebar collapse mid-reflow) and rejected. 0.3 is a
 * pragmatic balance — allows asymmetric splits down to ~30/70 while still
 * catching the codex-class transients (e.g. 10 cols on a 1-pane 1200px window
 * = 7% of fair share, well below the floor).
 */
const LAYOUT_FAIR_SHARE_FLOOR = 0.3;

/**
 * Calculate terminal dimensions exactly like VS Code does.
 * Source: vscode/src/vs/workbench/contrib/terminal/browser/terminalInstance.ts
 *
 * When `layoutContext` is provided, sub-usable measurements relative to the
 * pane's fair share of the window are rejected as transient layout state
 * (returns null so the existing retry/debounce paths wait for layout to
 * settle). When no context is provided, only zero/near-zero is filtered.
 *
 * Codex's TUI fragility (don't poke with sub-20×10 SIGWINCH) is enforced at
 * the SIGWINCH-send point, not here — see Terminal.tsx `resizeTerminal`.
 */
export function getScaledDimensions(
  container: HTMLElement,
  term: XTerm,
  fontSize: number,
  letterSpacing = 0,
  lineHeight = 1,
  layoutContext?: LayoutContext,
): { cols: number; rows: number; diagnostics: ResizeDiagnostics } | null {
  const containerStyle = getComputedStyle(container);
  const containerWidth = Math.min(parseFloat(containerStyle.width), MAX_CANVAS_WIDTH);
  const containerHeight = parseFloat(containerStyle.height);
  let width = containerWidth;
  let height = containerHeight;

  if (width <= 0 || height <= 0) {
    return null;
  }

  const xtermElement = term.element;

  if (xtermElement) {
    const xtermStyle = getComputedStyle(xtermElement);
    width -= parseFloat(xtermStyle.paddingLeft || '0') + parseFloat(xtermStyle.paddingRight || '0') + TERMINAL_SCROLLBAR_WIDTH;
    height -= parseFloat(xtermStyle.paddingTop || '0') + parseFloat(xtermStyle.paddingBottom || '0');
  } else {
    width -= TERMINAL_SCROLLBAR_WIDTH;
  }

  if (width <= 0 || height <= 0) {
    return null;
  }

  const core = (term as any)._core;
  const cellDims = core?._renderService?.dimensions?.css?.cell;
  const dpr = window.devicePixelRatio;

  let charWidth: number;
  let charHeight: number;
  let cellSource: 'renderer' | 'measured';

  if (cellDims?.width && cellDims?.height) {
    charWidth = cellDims.width - Math.round(letterSpacing) / dpr;
    charHeight = cellDims.height / lineHeight;
    cellSource = 'renderer';
  } else {
    const measured = measureTerminalFont(FONT_FAMILY, fontSize);
    charWidth = measured.charWidth;
    charHeight = measured.charHeight;
    cellSource = 'measured';
  }

  if (charWidth <= 0 || charHeight <= 0) {
    return null;
  }

  const gridDimensions = computeGridDimensions({
    availableWidth: width,
    availableHeight: height,
    charWidth,
    charHeight,
    dpr,
    letterSpacing,
    lineHeight,
  });
  if (!gridDimensions) {
    return null;
  }

  // Always filter zero/near-zero garbage (display:none transients, broken
  // renderer cell dims). Anything ≥2×2 might be a legitimate small split.
  if (gridDimensions.cols < 2 || gridDimensions.rows < 2) {
    return null;
  }

  // Layout-aware floor: when we know window size and pane count, reject
  // measurements that are implausibly small relative to this pane's "fair
  // share" of the workspace. Catches transient layout states (grid tracks
  // resolving, panel animation, sidebar collapse mid-reflow) without
  // false-rejecting legitimate small splits (3-way → ~14 cols). See
  // LAYOUT_FAIR_SHARE_FLOOR for tuning rationale.
  if (layoutContext && layoutContext.paneCount > 0) {
    const fairShareWidth = layoutContext.windowWidth / layoutContext.paneCount;
    const fairShareHeight = layoutContext.windowHeight / layoutContext.paneCount;
    const minAcceptableCols = Math.max(2, Math.floor((fairShareWidth * LAYOUT_FAIR_SHARE_FLOOR) / charWidth));
    const minAcceptableRows = Math.max(2, Math.floor((fairShareHeight * LAYOUT_FAIR_SHARE_FLOOR) / charHeight));
    if (gridDimensions.cols < minAcceptableCols || gridDimensions.rows < minAcceptableRows) {
      return null;
    }
  }

  return {
    cols: gridDimensions.cols,
    rows: gridDimensions.rows,
    diagnostics: {
      containerWidth,
      containerHeight,
      availableWidth: width,
      availableHeight: height,
      cellWidth: charWidth,
      cellHeight: charHeight,
      cellSource,
      dpr,
    },
  };
}
