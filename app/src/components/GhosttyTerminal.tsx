import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
} from 'react';
import { Ghostty, InputHandler, CellFlags, type GhosttyCell, type GhosttyTerminal as GhosttyModel } from 'ghostty-web';
import { ghosttyWasmUrl } from '../ghostty/wasm';
import { openPath, openUrl } from '@tauri-apps/plugin-opener';
import { exists } from '@tauri-apps/plugin-fs';
import { homeDir } from '@tauri-apps/api/path';
import {
  fragmentAtColumn,
  logicalIndexForCell,
  logicalLineAt,
  pathCandidatesForFragment,
  resolveDetectedPath,
  spanFromLogicalRange,
  urlAtColumn,
  type DetectedTerminalLink,
  type LogicalLine,
  type LogicalSpan,
} from '../utils/terminalLinks';
import {
  initialFocusedMatch,
  startFindScan,
  visibleMatches,
  type FindMatch,
  type FindScanHandle,
} from '../utils/terminalFind';
import { emptyOsc133State, parseOsc133, type Osc133State } from '../utils/terminalOsc133';
import {
  extractBlock,
  reanchorDelta,
  TerminalBlockStore,
  type BlockRowAccess,
  type TerminalBlock,
} from '../utils/terminalBlocks';
import {
  filterBlockOutputLines,
  lineSegments,
  type FilteredBlockLine,
} from '../utils/terminalBlockFilter';
import { TerminalContextMenu, type TerminalContextMenuItem } from './TerminalContextMenu';
import {
  cleanTerminalLines,
  terminalStyledSelectionToMarkdown,
  type TerminalMarkdownLine,
  type TerminalMarkdownRun,
} from '../utils/terminalMarkdown';
import { readClipboardText, writeClipboardText } from '../utils/clipboardBridge';
import { parseOsc52Writes, type Osc52State } from '../utils/terminalOsc';
import {
  parseSynchronizedOutput,
  type SynchronizedOutputState,
} from '../utils/terminalSynchronizedOutput';
import {
  FONT_FAMILY,
  TERMINAL_SCROLLBACK_BYTES,
  getTerminalAnsiPalette,
  getTerminalTheme,
  type ResolvedTheme,
} from '../utils/terminalSizing';
import {
  createResizeCoalescer,
  resizeGhosttyWithoutReflow,
  type ResizeCoalescer,
  type TerminalDimensions,
} from '../utils/ghosttyResize';
import { buildTerminalQueryResponses, stripDaemonOwnedResponses } from '../utils/terminalQueryResponses';
import { isSuspiciousTerminalSize } from '../utils/terminalDebug';
import { recordTerminalLinkHitTestEvent } from '../utils/terminalLinkHitTestLog';
import {
  recordDiag,
  recordPaint,
  noteResize,
  registerRenderProbe,
  disposePaneDiagnostics,
} from '../utils/terminalDiagnosticsLog';
import type { TerminalVisibleContentSnapshot } from '../utils/terminalVisibleContent';
import { analyzeTerminalVisibleLines } from '../utils/terminalVisibleContent';
import type { TerminalVisibleStyleSnapshot, TerminalVisibleStyleLineSnapshot } from '../utils/terminalStyleSummary';
import { registerTerminalPerfGetter, type TerminalPerfStartupSnapshot } from '../utils/terminalPerf';
import {
  applicationMouseInput,
  applicationWheelInput,
  bufferRowFromViewportRow,
  consumeWheelRows,
  createApplicationSelectionAnchor,
  offsetAfterWrite,
  relocateApplicationSelection,
  shouldReportApplicationMouseMove,
  viewportBufferStart,
  viewportRowFromBufferRow,
  type ApplicationSelectionAnchor,
} from '../utils/ghosttyScroll';
import { installTerminalKeyHandler } from './SessionTerminalWorkspace/terminalKeyHandler';
import {
  WebGlTerminalRenderer,
  type WebGlOverlay,
} from './GhosttyWebGlRenderer';
import './GhosttyTerminal.css';

interface GhosttyTerminalProps {
  fontSize: number;
  resolvedTheme?: ResolvedTheme;
  debugName: string;
  // Working directory used to resolve relative file paths detected in output.
  cwd?: string;
  runtimeLogMeta?: {
    sessionId: string;
    paneId: string;
    runtimeId: string;
    paneKind: 'agent';
    isActivePane: boolean;
    isActiveSession: boolean;
    paneCount: number;
  };
  onInput: (data: string) => void;
  onReady: (terminal: GhosttyTerminalHandle) => void;
  onResize: (cols: number, rows: number, options?: { reason?: string }) => void;
}

export interface GhosttyTerminalHandle {
  fit: () => void;
  openFind: () => void;
  focus: () => boolean;
  typeTextViaInput: (text: string) => boolean;
  isInputFocused: () => boolean;
  write: (
    data: string | Uint8Array,
    options?: {
      suppressResponses?: boolean;
      yieldBefore?: boolean;
      deferRender?: boolean;
      historicalReplay?: boolean;
    },
  ) => Promise<void>;
  resizeLocal: (
    cols: number,
    rows: number,
    options?: { historicalReplay?: boolean },
  ) => Promise<void>;
  reset: () => void;
  scrollToTop: () => boolean;
  getText: () => string;
  getSize: () => { cols: number; rows: number } | null;
  getVisibleContent: () => TerminalVisibleContentSnapshot;
  getVisibleStyleSummary: () => TerminalVisibleStyleSnapshot;
  drain: () => Promise<void>;
}

interface SelectionRange {
  startRow: number;
  startCol: number;
  endRow: number;
  endCol: number;
}

// ghostty-web's low-level model exposes hyperlink IDs but currently returns
// null for hyperlink URIs, so OSC 8 labels cannot be opened without API work.
// Ghostty's native renderer resets synchronized-output mode after 1000ms so
// one bad producer cannot freeze rendering indefinitely.
const SYNCHRONIZED_OUTPUT_RENDER_TIMEOUT_MS = 1000;

// Hover-time link detection state (Warp's fragment-boundary model): the word
// fragment under the pointer is analyzed once and cached; pointer movement
// inside the fragment costs nothing. The generation counter invalidates the
// cache when content or the viewport shifts under the pointer. The fragment
// lives on a logical line — the hovered row joined with its soft-wrapped
// neighbors — so links spanning visual rows detect and underline whole.
interface HoverLinkState {
  generation: number;
  line: LogicalLine;
  // Fragment span as logical indexes into line.text.
  startIndex: number;
  endIndex: number;
  // link.startCol/endCol are logical indexes; linkSpan is the same range
  // mapped to viewport rows for the underline overlay.
  link: DetectedTerminalLink | null;
  linkSpan: LogicalSpan | null;
}

const utf8Encoder = new TextEncoder();

function isWorkspaceResizeActive(element: HTMLElement | null): boolean {
  if (document.documentElement.dataset.attnWorkspaceResizing === '1') {
    return true;
  }
  const suppressUntil = Number(document.documentElement.dataset.attnWorkspaceMouseSuppressUntil || 0);
  if (Number.isFinite(suppressUntil) && suppressUntil > Date.now()) {
    return true;
  }
  return Boolean(element?.closest('.session-terminal-panes[data-resizing-split-id]'));
}

export function isWorkspaceResizeDragActive(element: HTMLElement | null): boolean {
  if (document.documentElement.dataset.attnWorkspaceResizing === '1') {
    return true;
  }
  return Boolean(element?.closest('.session-terminal-panes[data-resizing-split-id]'));
}

export function fitRequiresTerminalResize(
  current: TerminalDimensions,
  next: TerminalDimensions,
): boolean {
  return current.cols !== next.cols || current.rows !== next.rows;
}


function wordRangeAtColumn(line: string, col: number): { startCol: number; endCol: number } | null {
  const isWordCharacter = (character: string | undefined) => Boolean(character && /[\w-]/.test(character));
  if (!isWordCharacter(line[col])) return null;
  let startCol = col;
  while (startCol > 0 && isWordCharacter(line[startCol - 1])) startCol -= 1;
  let endCol = col + 1;
  while (endCol < line.length && isWordCharacter(line[endCol])) endCol += 1;
  return { startCol, endCol };
}

function rectSnapshot(rect: DOMRect | null) {
  if (!rect) return null;
  return {
    x: rect.x,
    y: rect.y,
    width: rect.width,
    height: rect.height,
    top: rect.top,
    left: rect.left,
    right: rect.right,
    bottom: rect.bottom,
  };
}

function cellFromRect(
  event: { clientX: number; clientY: number },
  rect: DOMRect | null,
  cellWidth: number,
  cellHeight: number,
  rows: number,
  cols: number,
) {
  if (!rect) return null;
  if (
    event.clientX < rect.left
    || event.clientX >= rect.right
    || event.clientY < rect.top
    || event.clientY >= rect.bottom
  ) {
    return null;
  }
  return {
    row: Math.max(0, Math.min(rows - 1, Math.floor((event.clientY - rect.top) / cellHeight))),
    col: Math.max(0, Math.min(cols, Math.floor((event.clientX - rect.left) / cellWidth))),
  };
}

function colorNumber(value: string): number {
  return Number.parseInt(value.slice(1), 16);
}

// Count printable cells in the live viewport window. getViewport() returns a
// fixed-capacity buffer whose tail can hold stale cells from a larger pre-resize
// grid, so only the first cols*rows entries are counted.
function countModelPrintable(terminal: GhosttyModel): number {
  const viewport = terminal.getViewport();
  const windowLen = terminal.cols * terminal.rows;
  let printable = 0;
  for (let i = 0; i < windowLen && i < viewport.length; i += 1) {
    const cell = viewport[i];
    if (cell && cell.codepoint > 32) printable += 1;
  }
  return printable;
}

function emptyStartup(): TerminalPerfStartupSnapshot {
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

function normalizeSelection(range: SelectionRange): SelectionRange {
  if (range.startRow < range.endRow || (range.startRow === range.endRow && range.startCol <= range.endCol)) {
    return range;
  }
  return {
    startRow: range.endRow,
    startCol: range.endCol,
    endRow: range.startRow,
    endCol: range.startCol,
  };
}

function cellText(
  terminal: GhosttyModel,
  cells: GhosttyCell[],
  row: number,
  startCol = 0,
  scrollback = false,
  trimEnd = true,
): string {
  let text = '';
  for (let offset = 0; offset < cells.length; offset += 1) {
    const col = startCol + offset;
    const cell = cells[offset];
    if (!cell || cell.width === 0) continue;
    text += cell.grapheme_len > 0
      ? scrollback
        ? terminal.getScrollbackGraphemeString(row, col)
        : terminal.getGraphemeString(row, col)
      : cell.codepoint > 0 ? String.fromCodePoint(cell.codepoint) : ' ';
  }
  return trimEnd ? text.trimEnd() : text;
}

export const GhosttyTerminal = forwardRef<GhosttyTerminalHandle, GhosttyTerminalProps>(
  function GhosttyTerminal({ fontSize, resolvedTheme = 'dark', debugName, cwd, runtimeLogMeta, onInput, onReady, onResize }, ref) {
    const containerRef = useRef<HTMLDivElement>(null);
    const canvasRef = useRef<HTMLCanvasElement>(null);
    const terminalRef = useRef<GhosttyModel | null>(null);
    const rendererRef = useRef<WebGlTerminalRenderer | null>(null);
    const inputRef = useRef<InputHandler | null>(null);
    const modelSizeRef = useRef({ cols: 80, rows: 24 });
    const viewportOffsetRef = useRef(0);
    const wheelRemainderRowsRef = useRef(0);
    const selectionRef = useRef<SelectionRange | null>(null);
    const selectedTextRef = useRef<string | null>(null);
    const applicationSelectionAnchorRef = useRef<ApplicationSelectionAnchor | null>(null);
    const selectingRef = useRef(false);
    const selectionPointerStartRef = useRef<{ clientX: number; clientY: number } | null>(null);
    const selectionDragThresholdMetRef = useRef(false);
    const selectionDragCleanupRef = useRef<(() => void) | null>(null);
    const trackedMouseButtonRef = useRef<number | null>(null);
    const hoveredCellRef = useRef<{ row: number; col: number } | null>(null);
    const acceleratorHeldRef = useRef(false);
    const cwdRef = useRef(cwd);
    const hoverGenerationRef = useRef(0);
    const hoverLinkRef = useRef<HoverLinkState | null>(null);
    // undefined = not fetched yet; null = unavailable (non-Tauri host).
    const homeDirRef = useRef<string | null | undefined>(undefined);
    const pathExistsCacheRef = useRef(new Map<string, boolean | Promise<boolean>>());
    const findOpenRef = useRef(false);
    const findQueryRef = useRef('');
    const findCaseSensitiveRef = useRef(false);
    const findMatchesRef = useRef<FindMatch[]>([]);
    const findFocusedIndexRef = useRef(-1);
    const findScanRef = useRef<FindScanHandle | null>(null);
    const findRescanTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
    const findInputRef = useRef<HTMLInputElement>(null);
    const runFindScanRef = useRef<(() => void) | null>(null);
    const osc133StateRef = useRef<Osc133State>(emptyOsc133State());
    const blockStoreRef = useRef(new TerminalBlockStore());
    const selectedBlockIdRef = useRef<number | null>(null);
    const writeChainRef = useRef(Promise.resolve());
    const historicalReplayGenerationRef = useRef(0);
    const fitResizeCoalescerRef = useRef<ResizeCoalescer | null>(null);
    const applyFitDimensionsRef = useRef<(dimensions: TerminalDimensions) => void>(() => undefined);
    const osc52StateRef = useRef<Osc52State>({ pending: '' });
    const synchronizedOutputStateRef = useRef<SynchronizedOutputState>({ active: false, pending: '' });
    const synchronizedOutputRenderTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
    const scheduledOutputRenderRef = useRef<number | null>(null);
    const renderCountRef = useRef(0);
    const writeCountRef = useRef(0);
    // Diagnostics: model instance (increments on rebuild) and last paint quads,
    // used by the blank-on-split watchdog to tell a fresh empty model and an
    // under-drawn surface apart.
    const modelInstanceRef = useRef(0);
    const lastPaintQuadsRef = useRef(0);
    const lastRenderAtRef = useRef(0);
    const lastWriteAtRef = useRef(0);
    const readyRef = useRef(false);
    const startupRef = useRef(emptyStartup());
    const onInputRef = useRef(onInput);
    const onReadyRef = useRef(onReady);
    const onResizeRef = useRef(onResize);
    const runtimeMetaRef = useRef(runtimeLogMeta);
    const debugNameRef = useRef(debugName);
    const diagKeyRef = useRef<string>(runtimeLogMeta?.paneId ?? runtimeLogMeta?.sessionId ?? debugName);
    const [error, setError] = useState<string | null>(null);
    const [linkCursorActive, setLinkCursorActive] = useState(false);
    const [findUi, setFindUi] = useState({ open: false, matchCount: 0, focusedIndex: -1, scanning: false, caseSensitive: false });
    const [contextMenu, setContextMenu] = useState<{ x: number; y: number; blockId: number | null } | null>(null);
    const [filterUi, setFilterUi] = useState<{ open: boolean; blockId: number | null; caseSensitive: boolean }>({ open: false, blockId: null, caseSensitive: false });
    const [filterMatches, setFilterMatches] = useState<FilteredBlockLine[]>([]);
    const filterQueryRef = useRef('');
    const filterInputRef = useRef<HTMLInputElement>(null);
    const filterRescanTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

    onInputRef.current = onInput;
    onReadyRef.current = onReady;
    onResizeRef.current = onResize;
    runtimeMetaRef.current = runtimeLogMeta;
    debugNameRef.current = debugName;
    cwdRef.current = cwd;
    // Stable diagnostics key for the per-pane watchdog/probe registries. paneId
    // is stable for a pane's life and correlates with daemon/workspace logs;
    // debugName can change (its agent/title segment is reassigned on relabel).
    // A ref keeps the key out of callback dependency arrays.
    diagKeyRef.current = runtimeLogMeta?.paneId ?? runtimeLogMeta?.sessionId ?? debugName;

    const getViewportCells = useCallback((): GhosttyCell[] | undefined => {
      const terminal = terminalRef.current;
      if (!terminal || viewportOffsetRef.current === 0) return undefined;
      const active = terminal.getViewport();
      const history = terminal.getScrollbackLength();
      const start = viewportBufferStart(history, viewportOffsetRef.current);
      const cells: GhosttyCell[] = [];
      for (let row = start; row < start + terminal.rows; row += 1) {
        const line = row < history ? terminal.getScrollbackLine(row) : active.slice((row - history) * terminal.cols, (row - history + 1) * terminal.cols);
        if (line) cells.push(...line);
      }
      return cells;
    }, []);

    const renderSurface = useCallback((force = false) => {
      const terminal = terminalRef.current;
      const renderer = rendererRef.current;
      if (!terminal || !renderer) return;
      if (runtimeMetaRef.current && !runtimeMetaRef.current.isActiveSession) return;
      const range = selectionRef.current ? normalizeSelection(selectionRef.current) : null;
      const scrollbackLength = terminal.getScrollbackLength();
      const overlays: WebGlOverlay[] = [];
      if (range) {
        overlays.push({
          startRow: viewportRowFromBufferRow(range.startRow, scrollbackLength, viewportOffsetRef.current),
          startCol: range.startCol,
          endRow: viewportRowFromBufferRow(range.endRow, scrollbackLength, viewportOffsetRef.current),
          endCol: range.endCol,
          color: getTerminalTheme(resolvedTheme).selectionBackground,
          kind: 'background',
        });
      }
      const hover = hoverLinkRef.current;
      if (hover?.link && hover.linkSpan && hover.generation === hoverGenerationRef.current) {
        overlays.push({
          startRow: hover.linkSpan.startRow,
          startCol: hover.linkSpan.startCol,
          endRow: hover.linkSpan.endRow,
          endCol: hover.linkSpan.endCol,
          color: getTerminalTheme(resolvedTheme).foreground,
          alpha: 0.8,
          kind: 'underline',
        });
      }
      if (selectedBlockIdRef.current !== null) {
        const block = blockStoreRef.current.blockById(selectedBlockIdRef.current);
        if (block && block.endRow !== undefined) {
          const firstRow = viewportBufferStart(scrollbackLength, viewportOffsetRef.current);
          const startRow = block.promptRow - firstRow;
          const endRow = block.endRow - 1 - firstRow;
          if (endRow >= 0 && startRow < terminal.rows) {
            // A faint accent wash behind the block, plus the crisp border. The
            // wash gives the selection weight without competing with the text;
            // the border keeps the bounds legible where the wash is too subtle.
            overlays.push({
              startRow,
              startCol: 0,
              endRow,
              endCol: terminal.cols,
              color: '#4d9de0',
              alpha: 0.08,
              kind: 'background',
            });
            overlays.push({
              startRow,
              startCol: 0,
              endRow,
              endCol: terminal.cols,
              color: '#4d9de0',
              alpha: 0.9,
              kind: 'outline',
            });
          }
        } else {
          selectedBlockIdRef.current = null;
        }
      }
      if (findOpenRef.current && findMatchesRef.current.length > 0) {
        const firstRow = viewportBufferStart(scrollbackLength, viewportOffsetRef.current);
        const focused = findFocusedIndexRef.current >= 0
          ? findMatchesRef.current[findFocusedIndexRef.current]
          : null;
        for (const match of visibleMatches(findMatchesRef.current, firstRow, terminal.rows)) {
          overlays.push({
            startRow: match.bufferRow - firstRow,
            startCol: match.startCol,
            endRow: match.bufferRow - firstRow,
            endCol: match.endCol,
            color: '#f5c542',
            alpha: match === focused ? 0.6 : 0.28,
            kind: 'background',
          });
        }
      }
      const sample = renderer.render(terminal, force, getViewportCells(), overlays, viewportOffsetRef.current);
      if (sample) {
        renderCountRef.current += 1;
        lastRenderAtRef.current = Date.now();
        lastPaintQuadsRef.current = sample.quads;
      }
      recordPaint({
        pane: diagKeyRef.current,
        session: runtimeMetaRef.current?.sessionId ?? undefined,
        cols: terminal.cols,
        rows: terminal.rows,
        force,
        offset: viewportOffsetRef.current,
        modelPrintable: countModelPrintable(terminal),
        quads: sample ? sample.quads : null,
        cellsArrayLen: sample ? sample.cellsArrayLen : null,
        skipNull: sample ? sample.printableSkippedNull : null,
        skipZeroWidth: sample ? sample.printableSkippedZeroWidth : null,
      });
    }, [getViewportCells, resolvedTheme]);

    const clearSynchronizedOutputRenderTimer = useCallback(() => {
      if (!synchronizedOutputRenderTimerRef.current) return;
      clearTimeout(synchronizedOutputRenderTimerRef.current);
      synchronizedOutputRenderTimerRef.current = null;
    }, []);

    const cancelScheduledOutputRender = useCallback(() => {
      if (scheduledOutputRenderRef.current === null) return;
      cancelAnimationFrame(scheduledOutputRenderRef.current);
      scheduledOutputRenderRef.current = null;
    }, []);

    const scheduleOutputRender = useCallback(() => {
      if (scheduledOutputRenderRef.current !== null) return;
      scheduledOutputRenderRef.current = requestAnimationFrame(() => {
        scheduledOutputRenderRef.current = null;
        renderSurface(true);
      });
    }, [renderSurface]);

    const flushSynchronizedOutputRender = useCallback(() => {
      clearSynchronizedOutputRenderTimer();
      scheduleOutputRender();
    }, [clearSynchronizedOutputRenderTimer, scheduleOutputRender]);

    const scheduleSynchronizedOutputRenderFallback = useCallback(() => {
      if (synchronizedOutputRenderTimerRef.current) return;
      synchronizedOutputRenderTimerRef.current = setTimeout(() => {
        synchronizedOutputRenderTimerRef.current = null;
        synchronizedOutputStateRef.current = { active: false, pending: '' };
        scheduleOutputRender();
      }, SYNCHRONIZED_OUTPUT_RENDER_TIMEOUT_MS);
    }, [scheduleOutputRender]);

    const lineAtVisibleRow = useCallback((row: number): string => {
      const terminal = terminalRef.current;
      if (!terminal) return '';
      const history = terminal.getScrollbackLength();
      const bufferRow = bufferRowFromViewportRow(row, history, viewportOffsetRef.current);
      const scrollback = bufferRow < history;
      const cells = getViewportCells() ?? terminal.getViewport();
      return cellText(
        terminal,
        cells.slice(row * terminal.cols, (row + 1) * terminal.cols),
        scrollback ? bufferRow : bufferRow - history,
        0,
        scrollback,
      );
    }, [getViewportCells]);

    const selectionLineAtBufferRow = useCallback((row: number, startCol: number, endCol: number): string => {
      const terminal = terminalRef.current;
      if (!terminal) return '';
      const history = terminal.getScrollbackLength();
      if (row < history) {
        const line = terminal.getScrollbackLine(row);
        return line ? cellText(terminal, line.slice(startCol, endCol), row, startCol, true, false) : '';
      }
      const viewportRow = row - history;
      if (viewportRow < 0 || viewportRow >= terminal.rows) return '';
      const active = terminal.getViewport();
      return cellText(
        terminal,
        active.slice(viewportRow * terminal.cols + startCol, viewportRow * terminal.cols + endCol),
        viewportRow,
        startCol,
        false,
        false,
      );
    }, []);

    const textForSelectionRange = useCallback((selection: SelectionRange | null) => {
      const terminal = terminalRef.current;
      const range = selection ? normalizeSelection(selection) : null;
      if (!terminal || !range) return '';
      const lines: string[] = [];
      for (let row = range.startRow; row <= range.endRow; row += 1) {
        const start = row === range.startRow ? range.startCol : 0;
        const end = row === range.endRow ? range.endCol : terminal.cols;
        lines.push(selectionLineAtBufferRow(row, start, end));
      }
      return cleanTerminalLines(lines).join('\n');
    }, [selectionLineAtBufferRow]);

    const selectedMarkdown = useCallback(() => {
      const terminal = terminalRef.current;
      const range = selectionRef.current ? normalizeSelection(selectionRef.current) : null;
      if (!terminal || !range) return '';
      const history = terminal.getScrollbackLength();
      const defaultForeground = colorNumber(getTerminalTheme(resolvedTheme).foreground);
      const lines: TerminalMarkdownLine[] = [];
      for (let row = range.startRow; row <= range.endRow; row += 1) {
        const start = row === range.startRow ? range.startCol : 0;
        const end = row === range.endRow ? range.endCol : terminal.cols;
        const scrollback = row < history;
        const activeRow = row - history;
        const cells = scrollback
          ? terminal.getScrollbackLine(row)?.slice(start, end) ?? []
          : terminal.getViewport().slice(activeRow * terminal.cols + start, activeRow * terminal.cols + end);
        const runs: TerminalMarkdownRun[] = [];
        for (let offset = 0; offset < cells.length; offset += 1) {
          const cell = cells[offset];
          if (!cell || cell.width === 0) continue;
          const text = cell.grapheme_len > 0
            ? scrollback
              ? terminal.getScrollbackGraphemeString(row, start + offset)
              : terminal.getGraphemeString(activeRow, start + offset)
            : cell.codepoint > 0 ? String.fromCodePoint(cell.codepoint) : ' ';
          const run = {
            text,
            bold: Boolean(cell.flags & CellFlags.BOLD),
            italic: Boolean(cell.flags & CellFlags.ITALIC),
            strikethrough: Boolean(cell.flags & CellFlags.STRIKETHROUGH),
            underline: Boolean(cell.flags & CellFlags.UNDERLINE),
            colored: (cell.fg_r << 16 | cell.fg_g << 8 | cell.fg_b) !== defaultForeground,
          };
          const current = runs[runs.length - 1];
          if (current
            && current.bold === run.bold
            && current.italic === run.italic
            && current.strikethrough === run.strikethrough
            && current.underline === run.underline
            && current.colored === run.colored) {
            current.text += run.text;
          } else {
            runs.push(run);
          }
        }
        lines.push({ runs, wrapped: !scrollback && terminal.isRowWrapped(activeRow) });
      }
      return terminalStyledSelectionToMarkdown(lines);
    }, [resolvedTheme]);

    const getText = useCallback(() => {
      const terminal = terminalRef.current;
      if (!terminal) return '';
      const lines: string[] = [];
      for (let row = 0; row < terminal.getScrollbackLength(); row += 1) {
        const line = terminal.getScrollbackLine(row);
        if (line) lines.push(cellText(terminal, line, row, 0, true));
      }
      const active = terminal.getViewport();
      for (let row = 0; row < terminal.rows; row += 1) {
        lines.push(cellText(terminal, active.slice(row * terminal.cols, (row + 1) * terminal.cols), row));
      }
      return lines.join('\n');
    }, []);

    const getVisibleContent = useCallback((): TerminalVisibleContentSnapshot => {
      const terminal = terminalRef.current;
      if (!terminal) return { cols: null, viewportY: null, lineCount: 0, lines: [], lineMetrics: [], summary: analyzeTerminalVisibleLines([]) };
      const lines = Array.from({ length: terminal.rows }, (_, row) => lineAtVisibleRow(row));
      return {
        cols: terminal.cols,
        viewportY: Math.max(0, terminal.getScrollbackLength() - viewportOffsetRef.current),
        lineCount: lines.length,
        lines,
        lineMetrics: lines.map((text, rowOffset) => ({
          rowOffset,
          text,
          occupiedColumns: text.length,
          occupiedWidthRatio: terminal.cols > 0 ? text.length / terminal.cols : 0,
          nonEmpty: text.trim().length > 0,
        })),
        summary: analyzeTerminalVisibleLines(lines, terminal.cols),
      };
    }, [lineAtVisibleRow]);

    const getVisibleStyleSummary = useCallback((): TerminalVisibleStyleSnapshot => {
      const terminal = terminalRef.current;
      if (!terminal) {
        return { cols: null, rows: null, viewportY: null, lineCount: 0, lines: [], summary: { styledCellCount: 0, styledLineCount: 0, boldCellCount: 0, italicCellCount: 0, underlineCellCount: 0, inverseCellCount: 0, fgPaletteCellCount: 0, fgRgbCellCount: 0, bgPaletteCellCount: 0, bgRgbCellCount: 0, uniqueStyleCount: 0 } };
      }
      const cells = getViewportCells() ?? terminal.getViewport();
      const lines: TerminalVisibleStyleLineSnapshot[] = [];
      const summary = { styledCellCount: 0, styledLineCount: 0, boldCellCount: 0, italicCellCount: 0, underlineCellCount: 0, inverseCellCount: 0, fgPaletteCellCount: 0, fgRgbCellCount: 0, bgPaletteCellCount: 0, bgRgbCellCount: 0, uniqueStyleCount: 0 };
      const styles = new Set<string>();
      for (let row = 0; row < terminal.rows; row += 1) {
        const rowCells = cells.slice(row * terminal.cols, (row + 1) * terminal.cols);
        const line: TerminalVisibleStyleLineSnapshot = { rowOffset: row, text: lineAtVisibleRow(row), styledCellCount: 0, boldCellCount: 0, italicCellCount: 0, underlineCellCount: 0, inverseCellCount: 0, fgPaletteCellCount: 0, fgRgbCellCount: 0, bgPaletteCellCount: 0, bgRgbCellCount: 0 };
        rowCells.forEach((cell) => {
          if (!cell || cell.width === 0 || cell.codepoint === 0 || cell.codepoint === 32) return;
          const bold = Boolean(cell.flags & CellFlags.BOLD);
          const italic = Boolean(cell.flags & CellFlags.ITALIC);
          const underline = Boolean(cell.flags & CellFlags.UNDERLINE);
          const inverse = Boolean(cell.flags & CellFlags.INVERSE);
          const colored = cell.fg_r !== 0 || cell.fg_g !== 0 || cell.fg_b !== 0 || cell.bg_r !== 0 || cell.bg_g !== 0 || cell.bg_b !== 0;
          if (!bold && !italic && !underline && !inverse && !colored) return;
          line.styledCellCount += 1; summary.styledCellCount += 1;
          if (bold) { line.boldCellCount += 1; summary.boldCellCount += 1; }
          if (italic) { line.italicCellCount += 1; summary.italicCellCount += 1; }
          if (underline) { line.underlineCellCount += 1; summary.underlineCellCount += 1; }
          if (inverse) { line.inverseCellCount += 1; summary.inverseCellCount += 1; }
          if (cell.fg_r || cell.fg_g || cell.fg_b) { line.fgRgbCellCount += 1; summary.fgRgbCellCount += 1; }
          if (cell.bg_r || cell.bg_g || cell.bg_b) { line.bgRgbCellCount += 1; summary.bgRgbCellCount += 1; }
          styles.add(`${cell.flags}:${cell.fg_r},${cell.fg_g},${cell.fg_b}:${cell.bg_r},${cell.bg_g},${cell.bg_b}`);
        });
        if (line.styledCellCount > 0) summary.styledLineCount += 1;
        lines.push(line);
      }
      summary.uniqueStyleCount = styles.size;
      return { cols: terminal.cols, rows: terminal.rows, viewportY: viewportOffsetRef.current, lineCount: lines.length, lines, summary };
    }, [getViewportCells, lineAtVisibleRow]);

    const scrollToFindMatch = useCallback((match: FindMatch) => {
      const terminal = terminalRef.current;
      if (!terminal) return;
      const history = terminal.getScrollbackLength();
      const firstVisible = viewportBufferStart(history, viewportOffsetRef.current);
      if (match.bufferRow >= firstVisible && match.bufferRow < firstVisible + terminal.rows) return;
      viewportOffsetRef.current = Math.max(0, Math.min(history, history - match.bufferRow));
      wheelRemainderRowsRef.current = 0;
      hoverGenerationRef.current += 1;
    }, []);

    const runFindScan = useCallback(() => {
      findScanRef.current?.cancel();
      findScanRef.current = null;
      const terminal = terminalRef.current;
      if (!terminal || !findOpenRef.current) return;
      const query = findQueryRef.current;
      if (!query) {
        findMatchesRef.current = [];
        findFocusedIndexRef.current = -1;
        setFindUi((ui) => ({ ...ui, matchCount: 0, focusedIndex: -1, scanning: false }));
        renderSurface(true);
        return;
      }
      setFindUi((ui) => ({ ...ui, scanning: true }));
      const access = {
        totalRows: () => {
          const model = terminalRef.current;
          return model ? model.getScrollbackLength() + model.rows : 0;
        },
        rowText: (bufferRow: number) => {
          const model = terminalRef.current;
          return model ? selectionLineAtBufferRow(bufferRow, 0, model.cols) : '';
        },
      };
      findScanRef.current = startFindScan(
        access,
        query,
        { caseSensitive: findCaseSensitiveRef.current },
        (progress) => {
          findMatchesRef.current = progress;
          setFindUi((ui) => ({ ...ui, matchCount: progress.length }));
          renderSurface(true);
        },
        (matches) => {
          findScanRef.current = null;
          findMatchesRef.current = matches;
          const focused = initialFocusedMatch(matches);
          findFocusedIndexRef.current = focused;
          setFindUi((ui) => ({ ...ui, scanning: false, matchCount: matches.length, focusedIndex: focused }));
          if (focused >= 0) scrollToFindMatch(matches[focused]);
          renderSurface(true);
        },
      );
    }, [renderSurface, scrollToFindMatch, selectionLineAtBufferRow]);
    runFindScanRef.current = runFindScan;

    const findNavigate = useCallback((direction: 1 | -1) => {
      const matches = findMatchesRef.current;
      if (matches.length === 0) return;
      const current = findFocusedIndexRef.current;
      const next = current < 0
        ? matches.length - 1
        : (current + direction + matches.length) % matches.length;
      findFocusedIndexRef.current = next;
      setFindUi((ui) => ({ ...ui, focusedIndex: next }));
      scrollToFindMatch(matches[next]);
      renderSurface(true);
    }, [renderSurface, scrollToFindMatch]);

    const openFind = useCallback(() => {
      findOpenRef.current = true;
      setFindUi((ui) => ({ ...ui, open: true }));
      requestAnimationFrame(() => {
        const input = findInputRef.current;
        // If the user already started typing into the input before this frame
        // fired, leave their caret alone — select() would make the next
        // keystroke replace what they typed.
        if (!input || document.activeElement === input) return;
        input.focus();
        input.select();
      });
      if (findQueryRef.current) runFindScan();
    }, [runFindScan]);

    const blockRowAccess = useCallback((): BlockRowAccess | null => {
      const terminal = terminalRef.current;
      if (!terminal) return null;
      return {
        totalRows: () => terminal.getScrollbackLength() + terminal.rows,
        rowText: (row) => selectionLineAtBufferRow(row, 0, terminal.cols),
      };
    }, [selectionLineAtBufferRow]);

    const selectedBlock = useCallback((): TerminalBlock | null => {
      if (selectedBlockIdRef.current === null) return null;
      return blockStoreRef.current.blockById(selectedBlockIdRef.current);
    }, []);

    // Copy a selected block: whole = command + output, otherwise command only.
    // Extraction re-anchors against the live buffer and refuses (returns
    // false) when the block's content has been trimmed away.
    const selectedBlockCopyText = useCallback((whole: boolean): string | null => {
      const block = selectedBlock();
      const access = blockRowAccess();
      if (!block || !access) return null;
      if (!whole) return block.command || null;
      const extracted = extractBlock(block, access);
      if (!extracted) return null;
      return extracted.output ? `${extracted.command}\n${extracted.output}` : extracted.command;
    }, [blockRowAccess, selectedBlock]);

    const copySelectedBlock = useCallback((whole: boolean): boolean => {
      const text = selectedBlockCopyText(whole);
      if (!text) return false;
      void writeClipboardText(text);
      return true;
    }, [selectedBlockCopyText]);

    const blockOutputText = useCallback((blockId: number): string | null => {
      const block = blockStoreRef.current.blockById(blockId);
      const access = blockRowAccess();
      if (!block || !access) return null;
      return extractBlock(block, access)?.output ?? null;
    }, [blockRowAccess]);

    const scrollToBufferRow = useCallback((bufferRow: number) => {
      const terminal = terminalRef.current;
      if (!terminal) return;
      const history = terminal.getScrollbackLength();
      viewportOffsetRef.current = Math.max(0, Math.min(history, history - bufferRow));
      wheelRemainderRowsRef.current = 0;
      hoverGenerationRef.current += 1;
      renderSurface(true);
    }, [renderSurface]);

    const scrollToBlockEdge = useCallback((blockId: number, edge: 'top' | 'bottom') => {
      const terminal = terminalRef.current;
      const block = blockStoreRef.current.blockById(blockId);
      if (!terminal || !block || block.endRow === undefined) return;
      const access = blockRowAccess();
      const delta = access ? reanchorDelta(block, access) ?? 0 : 0;
      const target = edge === 'top'
        ? block.promptRow + delta
        // Last block row at the bottom of the viewport (endRow is exclusive).
        : Math.max(block.promptRow + delta, block.endRow + delta - terminal.rows);
      scrollToBufferRow(target);
    }, [blockRowAccess, scrollToBufferRow]);

    const pasteFromClipboard = useCallback(async () => {
      let text = '';
      try {
        text = await readClipboardText();
      } catch {
        return;
      }
      if (!text) return;
      const normalized = text.replace(/\r\n/g, '\r').replace(/\n/g, '\r');
      onInputRef.current(terminalRef.current?.hasBracketedPaste()
        ? `\x1b[200~${normalized}\x1b[201~`
        : normalized);
    }, []);

    const runBlockFilter = useCallback((blockId: number | null, caseSensitive: boolean) => {
      const query = filterQueryRef.current;
      const output = blockId !== null && query ? blockOutputText(blockId) : null;
      setFilterMatches(output ? filterBlockOutputLines(output, query, caseSensitive) : []);
    }, [blockOutputText]);

    const openBlockFilter = useCallback((blockId: number) => {
      filterQueryRef.current = '';
      setFilterMatches([]);
      setFilterUi({ open: true, blockId, caseSensitive: false });
      requestAnimationFrame(() => filterInputRef.current?.focus());
    }, []);

    const closeBlockFilter = useCallback(() => {
      if (filterRescanTimerRef.current) {
        clearTimeout(filterRescanTimerRef.current);
        filterRescanTimerRef.current = null;
      }
      filterQueryRef.current = '';
      setFilterMatches([]);
      setFilterUi({ open: false, blockId: null, caseSensitive: false });
      containerRef.current?.focus();
    }, []);

    const closeFind = useCallback(() => {
      findOpenRef.current = false;
      findScanRef.current?.cancel();
      findScanRef.current = null;
      if (findRescanTimerRef.current) {
        clearTimeout(findRescanTimerRef.current);
        findRescanTimerRef.current = null;
      }
      findMatchesRef.current = [];
      findFocusedIndexRef.current = -1;
      setFindUi((ui) => ({ ...ui, open: false, matchCount: 0, focusedIndex: -1, scanning: false }));
      renderSurface(true);
      containerRef.current?.focus();
    }, [renderSurface]);

    const enqueueOperation = useCallback((operation: () => void | Promise<void>) => {
      writeChainRef.current = writeChainRef.current
        .catch(() => undefined)
        .then(async () => {
          try {
            await operation();
          } catch (reason) {
            setError(`Ghostty terminal update failed: ${String(reason)}`);
          }
        });
      return writeChainRef.current;
    }, []);

    const write = useCallback((
      data: string | Uint8Array,
      options?: {
        suppressResponses?: boolean;
        yieldBefore?: boolean;
        deferRender?: boolean;
        historicalReplay?: boolean;
      },
    ) => {
      const historicalReplayGeneration = historicalReplayGenerationRef.current;
      return enqueueOperation(async () => {
        if (
          options?.historicalReplay
          && historicalReplayGeneration !== historicalReplayGenerationRef.current
        ) {
          return;
        }
        if (options?.yieldBefore) {
          await new Promise<void>((resolve) => window.setTimeout(resolve, 0));
        }
        if (
          options?.historicalReplay
          && historicalReplayGeneration !== historicalReplayGenerationRef.current
        ) {
          return;
        }
        const terminal = terminalRef.current;
        if (!terminal) return;
        const searchableOutput = typeof data === 'string' ? data : new TextDecoder().decode(data);
        if (options?.historicalReplay) {
          // Replay reconstructs the terminal model; it must not re-execute
          // stale host integrations such as OSC 52 clipboard writes.
          osc52StateRef.current = { pending: '' };
        } else if (searchableOutput) {
          // Preserve the existing terminal contract: OSC 52 writes copy text
          // to the host clipboard; clipboard read queries are not answered.
          const parsed = parseOsc52Writes(osc52StateRef.current, searchableOutput);
          osc52StateRef.current = parsed.state;
          for (const payload of parsed.payloads) {
            try {
              const bytes = Uint8Array.from(atob(payload), (char) => char.charCodeAt(0));
              void writeClipboardText(new TextDecoder().decode(bytes));
            } catch {
              // Ignore malformed clipboard output.
            }
          }
        }
        const scrollbackBefore = terminal.getScrollbackLength();
        const viewportOffsetBefore = viewportOffsetRef.current;
        // Segment the stream at OSC 133 markers so each marker's buffer
        // position can be read from the model cursor right after the bytes
        // preceding it are applied. Without markers this degenerates to a
        // single write of the original bytes.
        const chunkBytes = typeof data === 'string' ? utf8Encoder.encode(data) : data;
        const osc133 = parseOsc133(osc133StateRef.current, chunkBytes);
        osc133StateRef.current = osc133.state;
        for (const segment of osc133.segments) {
          if (segment.bytes.length > 0) terminal.write(segment.bytes);
          if (segment.marker) {
            const cursor = terminal.getCursor();
            blockStoreRef.current.applyMarker(
              segment.marker,
              { row: terminal.getScrollbackLength() + cursor.y, col: cursor.x },
              (row) => selectionLineAtBufferRow(row, 0, terminal.cols),
            );
          }
        }
        const responses: string[] = [];
        while (terminal.hasResponse()) {
          const response = terminal.readResponse();
          if (response) responses.push(response);
        }
        if (!options?.suppressResponses) {
          for (const response of responses) {
            // CPR (cursor position) and DA1 (device attributes) replies are
            // owned by the daemon, the geometry/capability authority. Answering
            // here too would double-reply and the shell reads the extra
            // ESC[r;cR / ESC[?...c as stray input — and after a reattach the
            // frontend can miss them entirely, stalling fish's prompt. Strip
            // both; forward everything else (DSR, OSC color, etc.) the model
            // produced.
            const forwarded = stripDaemonOwnedResponses(response);
            if (forwarded) onInputRef.current(forwarded);
          }
          for (const response of buildTerminalQueryResponses(data, resolvedTheme, responses)) {
            onInputRef.current(response);
          }
        }
        viewportOffsetRef.current = offsetAfterWrite(
          viewportOffsetBefore,
          scrollbackBefore,
          terminal.getScrollbackLength(),
        );
        // Content changed under the pointer: drop the hover-link fragment cache.
        hoverGenerationRef.current += 1;
        if (findOpenRef.current && findQueryRef.current) {
          // New output while find is open: refresh matches once writes settle.
          if (findRescanTimerRef.current) clearTimeout(findRescanTimerRef.current);
          findRescanTimerRef.current = setTimeout(() => {
            findRescanTimerRef.current = null;
            runFindScanRef.current?.();
          }, 300);
        }
        if (viewportOffsetRef.current === 0) {
          wheelRemainderRowsRef.current = 0;
        }
        const applicationSelectionAnchor = applicationSelectionAnchorRef.current;
        if (applicationSelectionAnchor) {
          const visibleLines = Array.from({ length: terminal.rows }, (_, row) => lineAtVisibleRow(row));
          selectionRef.current = relocateApplicationSelection(
            applicationSelectionAnchor,
            visibleLines,
            viewportBufferStart(terminal.getScrollbackLength(), viewportOffsetRef.current),
            terminal.cols,
          );
        }
        writeCountRef.current += 1;
        lastWriteAtRef.current = Date.now();
        const synchronizedOutput = parseSynchronizedOutput(
          synchronizedOutputStateRef.current,
          searchableOutput,
        );
        synchronizedOutputStateRef.current = synchronizedOutput.state;
        recordDiag({
          kind: 'write',
          pane: diagKeyRef.current,
          session: runtimeMetaRef.current?.sessionId ?? undefined,
          model: modelInstanceRef.current,
          len: searchableOutput.length,
          syncActive: synchronizedOutput.state.active,
          shouldRender: synchronizedOutput.shouldRender,
          cols: terminal.cols,
          rows: terminal.rows,
        });
        if (options?.deferRender && synchronizedOutput.shouldRender) {
          return;
        }
        if (synchronizedOutput.shouldRender) {
          flushSynchronizedOutputRender();
        } else {
          scheduleSynchronizedOutputRenderFallback();
        }
      });
    }, [enqueueOperation, flushSynchronizedOutputRender, lineAtVisibleRow, scheduleSynchronizedOutputRenderFallback, selectionLineAtBufferRow]);

    // Replay segments alternate resize and bytes; both must be applied on one
    // chain or all historical bytes are parsed at the final geometry.
    const resizeLocal = useCallback((
      cols: number,
      rows: number,
      options?: { historicalReplay?: boolean },
    ) => {
      const historicalReplayGeneration = historicalReplayGenerationRef.current;
      const currentTerminal = terminalRef.current;
      if (
        !options?.historicalReplay
        && currentTerminal
        && fitRequiresTerminalResize(
          { cols: currentTerminal.cols, rows: currentTerminal.rows },
          { cols, rows },
        )
      ) {
        historicalReplayGenerationRef.current += 1;
      }
      return enqueueOperation(() => {
        if (
          options?.historicalReplay
          && historicalReplayGeneration !== historicalReplayGenerationRef.current
        ) {
          return;
        }
        const terminal = terminalRef.current;
        const renderer = rendererRef.current;
        if (!terminal || !renderer) return;
        const fromCols = terminal.cols;
        const fromRows = terminal.rows;
        if (fromCols === cols && fromRows === rows) {
          modelSizeRef.current = { cols, rows };
          noteResize(diagKeyRef.current, {
            session: runtimeMetaRef.current?.sessionId ?? undefined,
            paneKind: runtimeMetaRef.current?.paneKind ?? undefined,
            source: 'resizeLocal', fromCols, fromRows, toCols: cols, toRows: rows,
            noop: true,
          });
          return;
        }
        if (options?.historicalReplay) {
          resizeGhosttyWithoutReflow(terminal, cols, rows);
        } else {
          terminal.resize(cols, rows);
        }
        modelSizeRef.current = { cols, rows };
        renderer.resize(cols, rows);
        hoverGenerationRef.current += 1;
        noteResize(diagKeyRef.current, {
          session: runtimeMetaRef.current?.sessionId ?? undefined,
          paneKind: runtimeMetaRef.current?.paneKind ?? undefined,
          source: 'resizeLocal', fromCols, fromRows, toCols: cols, toRows: rows,
          noop: false,
          historicalReplay: options?.historicalReplay ?? false,
        });
        if (!options?.historicalReplay) {
          renderSurface(true);
        }
      });
    }, [enqueueOperation, renderSurface]);

    const applyFitDimensions = useCallback((dims: TerminalDimensions) => {
      const terminal = terminalRef.current;
      const renderer = rendererRef.current;
      if (!terminal || !renderer) return;
      // Inactive session wrappers use display:none. Resizing the Ghostty
      // model from that hidden geometry discards an idle alternate-screen
      // frame before the session becomes visible again.
      const paneKind = runtimeMetaRef.current?.paneKind ?? undefined;
      const session = runtimeMetaRef.current?.sessionId ?? undefined;
      if (runtimeMetaRef.current && !runtimeMetaRef.current.isActiveSession) {
        noteResize(diagKeyRef.current, { session, paneKind, source: 'fit', bail: 'inactiveSession' });
        return;
      }
      if (!fitRequiresTerminalResize({ cols: terminal.cols, rows: terminal.rows }, dims)) {
        noteResize(diagKeyRef.current, { session, paneKind, source: 'fit', bail: 'sameSize', toCols: dims.cols, toRows: dims.rows });
        renderSurface(false);
        return;
      }
      // A no-op fit can arrive while cooperative replay is yielding between
      // chunks. Only a real geometry change conflicts with the queued history.
      historicalReplayGenerationRef.current += 1;
      const fromCols = terminal.cols;
      const fromRows = terminal.rows;
      resizeGhosttyWithoutReflow(terminal, dims.cols, dims.rows);
      modelSizeRef.current = dims;
      renderer.resize(dims.cols, dims.rows);
      hoverGenerationRef.current += 1;
      noteResize(diagKeyRef.current, {
        session, paneKind, source: 'fit', fromCols, fromRows, toCols: dims.cols, toRows: dims.rows,
      });
      renderSurface(true);
      onResizeRef.current(dims.cols, dims.rows, { reason: 'ghostty_fit' });
    }, [renderSurface]);

    applyFitDimensionsRef.current = applyFitDimensions;

    const fit = useCallback(() => {
      const container = containerRef.current;
      const renderer = rendererRef.current;
      if (!container || !renderer) return;
      const paneKind = runtimeMetaRef.current?.paneKind ?? undefined;
      const session = runtimeMetaRef.current?.sessionId ?? undefined;
      if (runtimeMetaRef.current && !runtimeMetaRef.current.isActiveSession) {
        noteResize(diagKeyRef.current, { session, paneKind, source: 'fit', bail: 'inactiveSession' });
        return;
      }
      const dims = renderer.fitDimensions(container.clientWidth, container.clientHeight);
      if (runtimeMetaRef.current?.paneKind === 'agent' && isSuspiciousTerminalSize(dims.cols, dims.rows)) {
        noteResize(diagKeyRef.current, { session, paneKind, source: 'fit', bail: 'suspiciousSize', toCols: dims.cols, toRows: dims.rows });
        return;
      }
      if (!fitResizeCoalescerRef.current) {
        fitResizeCoalescerRef.current = createResizeCoalescer(
          (dimensions) => applyFitDimensionsRef.current(dimensions),
        );
      }
      fitResizeCoalescerRef.current.submit(dims, isWorkspaceResizeDragActive(container));
    }, []);

    useImperativeHandle(ref, () => ({
      fit,
      openFind,
      focus: () => {
        // The find bar / block filter own keyboard focus while open: a
        // deferred pane focus (e.g. focusPane's retry loop after a session
        // switch) must not steal it and leak keystrokes into the PTY.
        if (filterInputRef.current) {
          if (document.activeElement !== filterInputRef.current) filterInputRef.current.focus();
          return true;
        }
        if (findOpenRef.current && findInputRef.current) {
          if (document.activeElement !== findInputRef.current) findInputRef.current.focus();
          return true;
        }
        const container = containerRef.current;
        if (!container) return false;
        container.focus();
        return document.activeElement === container;
      },
      typeTextViaInput: (text: string) => { onInputRef.current(text.replace(/\n/g, '\r')); return true; },
      isInputFocused: () => document.activeElement === containerRef.current,
      write,
      resizeLocal,
      reset: () => { recordDiag({ kind: 'reset', pane: diagKeyRef.current, session: runtimeMetaRef.current?.sessionId ?? undefined, model: modelInstanceRef.current }); blockStoreRef.current.clear(); selectedBlockIdRef.current = null; void write('\x1bc'); },
      scrollToTop: () => {
        const terminal = terminalRef.current;
        if (!terminal) return false;
        viewportOffsetRef.current = terminal.getScrollbackLength();
        wheelRemainderRowsRef.current = 0;
        hoverGenerationRef.current += 1;
        renderSurface(true);
        return true;
      },
      getText,
      getSize: () => terminalRef.current ? { cols: terminalRef.current.cols, rows: terminalRef.current.rows } : null,
      getVisibleContent,
      getVisibleStyleSummary,
      drain: () => writeChainRef.current,
    }), [fit, getText, getVisibleContent, getVisibleStyleSummary, openFind, renderSurface, resizeLocal, write]);

    useEffect(() => {
      let active = true;
      let observer: ResizeObserver | null = null;
      const container = containerRef.current;
      const canvas = canvasRef.current;
      if (!container || !canvas) return;
      const perfId = `ghostty-${debugNameRef.current}`;
      void Ghostty.load(ghosttyWasmUrl).then((ghostty) => {
        if (!active) return;
        const theme = getTerminalTheme(resolvedTheme);
        const initialSize = modelSizeRef.current;
        const terminal = ghostty.createTerminal(initialSize.cols, initialSize.rows, {
          scrollbackLimit: TERMINAL_SCROLLBACK_BYTES,
          fgColor: colorNumber(theme.foreground),
          bgColor: colorNumber(theme.background),
          cursorColor: colorNumber(theme.cursor),
          palette: getTerminalAnsiPalette(resolvedTheme),
        });
        synchronizedOutputStateRef.current = { active: false, pending: '' };
        clearSynchronizedOutputRenderTimer();
        modelInstanceRef.current += 1;
        // Fresh model: buffer rows from any previous find or command blocks
        // are meaningless.
        osc133StateRef.current = emptyOsc133State();
        blockStoreRef.current.clear();
        selectedBlockIdRef.current = null;
        findScanRef.current?.cancel();
        findScanRef.current = null;
        findOpenRef.current = false;
        findMatchesRef.current = [];
        findFocusedIndexRef.current = -1;
        setFindUi((ui) => ({ ...ui, open: false, matchCount: 0, focusedIndex: -1, scanning: false }));
        recordDiag({
          kind: 'pane_mount',
          pane: diagKeyRef.current,
          label: debugNameRef.current,
          session: runtimeMetaRef.current?.sessionId ?? undefined,
          paneKind: runtimeMetaRef.current?.paneKind ?? undefined,
          model: modelInstanceRef.current,
          cols: initialSize.cols,
          rows: initialSize.rows,
        });
        const renderer = new WebGlTerminalRenderer(canvas, fontSize, FONT_FAMILY, {
          background: theme.background,
          foreground: theme.foreground,
          cursor: theme.cursor,
        });
        terminalRef.current = terminal;
        rendererRef.current = renderer;
        // Live probe for the blank-on-resize watchdog: lets the diagnostics
        // module read the current model fill vs the last paint's quad count.
        registerRenderProbe(diagKeyRef.current, () => {
          const model = terminalRef.current;
          if (!model) return null;
          return {
            cols: model.cols,
            rows: model.rows,
            modelPrintable: countModelPrintable(model),
            lastPaintAt: lastRenderAtRef.current,
            lastPaintQuads: lastPaintQuadsRef.current,
          };
        });
        inputRef.current = new InputHandler(
          ghostty,
          container,
          (data) => onInputRef.current(data),
          () => undefined,
          undefined,
          (event) => !installTerminalKeyHandler((data) => onInputRef.current(data))(event),
          (mode) => terminal.getMode(mode),
        );
        fit();
        readyRef.current = true;
        startupRef.current.firstReadyAt = Date.now();
        startupRef.current.firstReadyCols = terminal.cols;
        startupRef.current.firstReadyRows = terminal.rows;
        observer = new ResizeObserver(fit);
        observer.observe(container);
        onReadyRef.current({
          fit,
          openFind,
          focus: () => {
            // Same find-bar/filter focus ownership rule as the imperative handle.
            if (filterInputRef.current) {
              if (document.activeElement !== filterInputRef.current) filterInputRef.current.focus();
              return true;
            }
            if (findOpenRef.current && findInputRef.current) {
              if (document.activeElement !== findInputRef.current) findInputRef.current.focus();
              return true;
            }
            container.focus();
            return document.activeElement === container;
          },
          typeTextViaInput: (text) => { onInputRef.current(text.replace(/\n/g, '\r')); return true; },
          isInputFocused: () => document.activeElement === container,
          write,
          resizeLocal,
          reset: () => { recordDiag({ kind: 'reset', pane: diagKeyRef.current, session: runtimeMetaRef.current?.sessionId ?? undefined, model: modelInstanceRef.current }); blockStoreRef.current.clear(); selectedBlockIdRef.current = null; void write('\x1bc'); },
          scrollToTop: () => { viewportOffsetRef.current = terminal.getScrollbackLength(); wheelRemainderRowsRef.current = 0; hoverGenerationRef.current += 1; renderSurface(true); return true; },
          getText,
          getSize: () => ({ cols: terminal.cols, rows: terminal.rows }),
          getVisibleContent,
          getVisibleStyleSummary,
          drain: () => writeChainRef.current,
        });
      }).catch((reason) => {
        if (active) setError(String(reason));
      });
      const handleContextLost = (event: Event) => {
        event.preventDefault();
        setError('Ghostty WebGL context lost. Reopen the pane to rebuild the renderer.');
      };
      canvas.addEventListener('webglcontextlost', handleContextLost);
      const unregister = registerTerminalPerfGetter(perfId, () => {
        const terminal = terminalRef.current;
        const meta = runtimeMetaRef.current;
        if (!terminal) return null;
        return {
          terminalName: debugNameRef.current,
          sessionId: meta?.sessionId ?? null,
          paneId: meta?.paneId ?? null,
          runtimeId: meta?.runtimeId ?? null,
          paneKind: meta?.paneKind ?? null,
          isActivePane: meta?.isActivePane ?? null,
          isActiveSession: meta?.isActiveSession ?? null,
          cols: terminal.cols,
          rows: terminal.rows,
          bufferLength: terminal.rows + terminal.getScrollbackLength(),
          baseY: terminal.getScrollbackLength(),
          viewportY: viewportOffsetRef.current,
          scrollbackLimit: TERMINAL_SCROLLBACK_BYTES,
          alternateScreen: terminal.isAlternateScreen(),
          mouseTracking: terminal.hasMouseTracking(),
          renderer: 'ghostty-webgl',
          visible: true,
          writeQueueChunks: 0,
          writeQueueBytes: 0,
          renderCount: renderCountRef.current,
          writeParsedCount: writeCountRef.current,
          lastRenderAt: lastRenderAtRef.current,
          lastWriteParsedAt: lastWriteAtRef.current,
          lastRenderRange: null,
          ready: readyRef.current,
          startup: startupRef.current,
          lastResize: null,
          dom: { container: null, surface: null, canvas: canvas ? { width: canvas.clientWidth, height: canvas.clientHeight } : null },
        };
      });
      return () => {
        active = false;
        recordDiag({
          kind: 'pane_unmount',
          pane: diagKeyRef.current,
          label: debugNameRef.current,
          session: runtimeMetaRef.current?.sessionId ?? undefined,
          model: modelInstanceRef.current,
        });
        disposePaneDiagnostics(diagKeyRef.current);
        observer?.disconnect();
        canvas.removeEventListener('webglcontextlost', handleContextLost);
        unregister();
        clearSynchronizedOutputRenderTimer();
        cancelScheduledOutputRender();
        fitResizeCoalescerRef.current?.cancel();
        fitResizeCoalescerRef.current = null;
        inputRef.current?.dispose();
        rendererRef.current?.dispose();
        terminalRef.current?.free();
        inputRef.current = null;
        rendererRef.current = null;
        terminalRef.current = null;
      };
    // Ghostty cells contain their resolved default RGB values, so theme
    // changes require a fresh model. The pane runtime rehydrates this model
    // from verified replay without sending historical replies to the live PTY.
    }, [cancelScheduledOutputRender, clearSynchronizedOutputRenderTimer, fit, fontSize, getText, getVisibleContent, getVisibleStyleSummary, openFind, renderSurface, resizeLocal, resolvedTheme, write]);

    // Release this pane's WebGL2 context when the pane unmounts. Browsers cap the
    // number of simultaneously-live WebGL contexts (WKWebView's cap is low), and
    // attn keeps every workspace's panes mounted, so a closed or remounted pane
    // whose context lingers until non-deterministic GC can starve new panes — the
    // engine then forcibly loses the oldest context, which surfaces as a frozen
    // pane or a hard UI freeze when opening a new agent/terminal. loseContext()
    // reclaims the context deterministically at unmount.
    //
    // This lives in its own unmount-only effect (empty deps) on purpose: the init
    // effect above reuses this same <canvas> when fontSize/theme change, and a
    // canvas keeps returning the *same* context object from getContext() even
    // after it is lost. Losing it in the per-init cleanup would hand the rebuilt
    // renderer a dead context. Capturing the canvas here closes over the element
    // so the cleanup is order-independent of the init effect's teardown.
    useEffect(() => {
      const canvas = canvasRef.current;
      return () => {
        canvas?.getContext('webgl2')?.getExtension('WEBGL_lose_context')?.loseContext();
      };
    }, []);

    const cellFromPointer = (event: React.MouseEvent | React.WheelEvent | MouseEvent) => {
      const renderer = rendererRef.current;
      const rect = canvasRef.current?.getBoundingClientRect();
      if (!renderer || !rect) return null;
      if (
        event.clientX < rect.left
        || event.clientX >= rect.right
        || event.clientY < rect.top
        || event.clientY >= rect.bottom
      ) {
        return null;
      }
      return {
        row: Math.max(0, Math.min((terminalRef.current?.rows ?? 1) - 1, Math.floor((event.clientY - rect.top) / renderer.cellHeight))),
        col: Math.max(0, Math.min((terminalRef.current?.cols ?? 1), Math.floor((event.clientX - rect.left) / renderer.cellWidth))),
      };
    };

    const recordPointerHitTest = useCallback((
      eventName: string,
      event: React.MouseEvent | MouseEvent,
      extra: Record<string, unknown> = {},
    ) => {
      const terminal = terminalRef.current;
      const renderer = rendererRef.current;
      const container = containerRef.current;
      const canvas = canvasRef.current;
      if (!terminal || !renderer || !container || !canvas) return;
      const containerRect = container.getBoundingClientRect();
      const canvasRect = canvas.getBoundingClientRect();
      const containerCell = cellFromRect(event, containerRect, renderer.cellWidth, renderer.cellHeight, terminal.rows, terminal.cols);
      const canvasCell = cellFromRect(event, canvasRect, renderer.cellWidth, renderer.cellHeight, terminal.rows, terminal.cols);
      const selectedText = selectedTextRef.current;
      const selection = selectionRef.current;
      const containerLine = containerCell ? lineAtVisibleRow(containerCell.row) : '';
      const canvasLine = canvasCell ? lineAtVisibleRow(canvasCell.row) : '';
      const containerUri = containerCell ? urlAtColumn(containerLine, containerCell.col)?.uri ?? null : null;
      const canvasUri = canvasCell ? urlAtColumn(canvasLine, canvasCell.col)?.uri ?? null : null;
      recordTerminalLinkHitTestEvent({
        event: eventName,
        debugName: debugNameRef.current,
        sessionId: runtimeMetaRef.current?.sessionId,
        paneId: runtimeMetaRef.current?.paneId,
        runtimeId: runtimeMetaRef.current?.runtimeId,
        details: {
          pointer: {
            clientX: event.clientX,
            clientY: event.clientY,
            offsetFromContainer: {
              x: event.clientX - containerRect.left,
              y: event.clientY - containerRect.top,
            },
            offsetFromCanvas: {
              x: event.clientX - canvasRect.left,
              y: event.clientY - canvasRect.top,
            },
            metaKey: event.metaKey,
            ctrlKey: event.ctrlKey,
            altKey: event.altKey,
            shiftKey: event.shiftKey,
            button: event.button,
            buttons: event.buttons,
          },
          cells: {
            container: containerCell,
            canvas: canvasCell,
          },
          detected: {
            containerUri,
            canvasUri,
            containerLinePreview: containerLine,
            canvasLinePreview: canvasLine,
          },
          selection: {
            selecting: selectingRef.current,
            dragThresholdMet: selectionDragThresholdMetRef.current,
            range: selection,
            selectedTextPreview: selectedText,
            selectedTextLength: selectedText?.length ?? 0,
          },
          terminal: {
            cols: terminal.cols,
            rows: terminal.rows,
            scrollbackLength: terminal.getScrollbackLength(),
            viewportOffset: viewportOffsetRef.current,
            alternateScreen: terminal.isAlternateScreen(),
            mouseTracking: terminal.hasMouseTracking(),
          },
          geometry: {
            containerRect: rectSnapshot(containerRect),
            canvasRect: rectSnapshot(canvasRect),
            containerClient: {
              width: container.clientWidth,
              height: container.clientHeight,
            },
            canvasClient: {
              width: canvas.clientWidth,
              height: canvas.clientHeight,
            },
            canvasBacking: {
              width: canvas.width,
              height: canvas.height,
            },
            devicePixelRatio: window.devicePixelRatio,
            cellWidth: renderer.cellWidth,
            cellHeight: renderer.cellHeight,
          },
          ...extra,
        },
      });
    }, [lineAtVisibleRow]);

    const mouseModifiers = (event: React.MouseEvent) =>
      (event.shiftKey ? 4 : 0) + (event.altKey ? 8 : 0) + (event.ctrlKey ? 16 : 0);

    const mouseButton = (button: number) => {
      if (button === 1) return 1;
      if (button === 2) return 2;
      return 0;
    };

    const hoverLinkAtCell = useCallback((cell: { row: number; col: number } | null): DetectedTerminalLink | null => {
      const hover = hoverLinkRef.current;
      if (!cell || !hover?.link || hover.generation !== hoverGenerationRef.current) return null;
      const index = logicalIndexForCell(hover.line, cell.row, cell.col);
      if (index !== null && index >= hover.link.startCol && index < hover.link.endCol) {
        return hover.link;
      }
      return null;
    }, []);

    const updateLinkCursor = useCallback((cell: { row: number; col: number } | null, acceleratorHeld: boolean) => {
      setLinkCursorActive(Boolean(hoverLinkAtCell(cell) && acceleratorHeld));
    }, [hoverLinkAtCell]);

    const cachedPathExists = useCallback((absolutePath: string): Promise<boolean> => {
      const cache = pathExistsCacheRef.current;
      const cached = cache.get(absolutePath);
      if (cached !== undefined) return Promise.resolve(cached);
      if (cache.size > 512) cache.clear();
      const pending = exists(absolutePath)
        .catch(() => false)
        .then((result) => {
          cache.set(absolutePath, result);
          return result;
        });
      cache.set(absolutePath, pending);
      return pending;
    }, []);

    const ensureHomeDir = useCallback(async (): Promise<string | null> => {
      if (homeDirRef.current === undefined) {
        try {
          homeDirRef.current = await homeDir();
        } catch {
          homeDirRef.current = null;
        }
      }
      return homeDirRef.current;
    }, []);

    // Does this viewport row continue the line started on the row above it?
    // Active-screen rows have an authoritative wrap flag; ghostty-web exposes
    // no flag for scrollback rows, so a completely full previous row is
    // treated as wrapping. False joins are filtered downstream: path
    // candidates must pass the existence check before anything links.
    const isContinuationRow = useCallback((viewportRow: number): boolean => {
      const terminal = terminalRef.current;
      if (!terminal) return false;
      const history = terminal.getScrollbackLength();
      const bufferRow = bufferRowFromViewportRow(viewportRow, history, viewportOffsetRef.current);
      if (bufferRow <= 0) return false;
      if (bufferRow >= history) return terminal.isRowWrapped(bufferRow - history);
      return selectionLineAtBufferRow(bufferRow - 1, 0, terminal.cols).length === terminal.cols;
    }, [selectionLineAtBufferRow]);

    // Analyze the word fragment under the pointer on its logical line (the
    // hovered row joined with its soft-wrapped neighbors). Movement inside the
    // cached fragment exits immediately; URLs resolve synchronously; file
    // paths are validated asynchronously against the filesystem (cached).
    const detectHoverLink = useCallback((cell: { row: number; col: number } | null) => {
      const generation = hoverGenerationRef.current;
      const current = hoverLinkRef.current;
      if (cell && current && current.generation === generation) {
        const cachedIndex = logicalIndexForCell(current.line, cell.row, cell.col);
        if (cachedIndex !== null && cachedIndex >= current.startIndex && cachedIndex < current.endIndex) {
          return;
        }
      }
      const hadUnderline = Boolean(current?.link && current.generation === generation);
      const clearHover = () => {
        hoverLinkRef.current = null;
        if (hadUnderline) {
          renderSurface(true);
          setLinkCursorActive(false);
        }
      };
      const terminal = terminalRef.current;
      if (!cell || !terminal) {
        clearHover();
        return;
      }
      const logical = logicalLineAt(lineAtVisibleRow, isContinuationRow, cell.row, terminal.cols, terminal.rows);
      const index = logicalIndexForCell(logical, cell.row, cell.col);
      if (index === null) {
        clearHover();
        return;
      }
      const url = urlAtColumn(logical.text, index);
      if (url) {
        hoverLinkRef.current = {
          generation,
          line: logical,
          startIndex: url.startCol,
          endIndex: url.endCol,
          link: { kind: 'url', uri: url.uri, startCol: url.startCol, endCol: url.endCol },
          linkSpan: spanFromLogicalRange(logical, url.startCol, url.endCol),
        };
        renderSurface(true);
        updateLinkCursor(cell, acceleratorHeldRef.current);
        return;
      }
      const fragment = fragmentAtColumn(logical.text, index);
      if (!fragment) {
        clearHover();
        return;
      }
      const entry: HoverLinkState = {
        generation,
        line: logical,
        startIndex: fragment.startCol,
        endIndex: fragment.endCol,
        link: null,
        linkSpan: null,
      };
      hoverLinkRef.current = entry;
      if (hadUnderline) {
        renderSurface(true);
        setLinkCursorActive(false);
      }
      const candidates = pathCandidatesForFragment(
        logical.text.slice(fragment.startCol, fragment.endCol),
        fragment.startCol,
      );
      if (candidates.length === 0) return;
      void (async () => {
        const home = await ensureHomeDir();
        for (const candidate of candidates) {
          const absolutePath = resolveDetectedPath(candidate.path, cwdRef.current, home ?? undefined);
          if (!absolutePath) continue;
          if (!(await cachedPathExists(absolutePath))) continue;
          if (hoverLinkRef.current !== entry || hoverGenerationRef.current !== generation) return;
          entry.link = {
            kind: 'path',
            absolutePath,
            line: candidate.line,
            column: candidate.column,
            startCol: candidate.startCol,
            endCol: candidate.endCol,
          };
          entry.linkSpan = spanFromLogicalRange(logical, candidate.startCol, candidate.endCol);
          renderSurface(true);
          updateLinkCursor(hoveredCellRef.current, acceleratorHeldRef.current);
          return;
        }
      })();
    }, [cachedPathExists, ensureHomeDir, isContinuationRow, lineAtVisibleRow, renderSurface, updateLinkCursor]);

    // Link under a cell for click handling: prefer the resolved hover state
    // (paths require it — existence was already validated), fall back to a
    // synchronous URL scan for clicks that arrive before any hover.
    const linkAtCell = useCallback((cell: { row: number; col: number } | null): DetectedTerminalLink | null => {
      const hovered = hoverLinkAtCell(cell);
      if (hovered) return hovered;
      if (!cell) return null;
      const url = urlAtColumn(lineAtVisibleRow(cell.row), cell.col);
      return url ? { kind: 'url', uri: url.uri, startCol: url.startCol, endCol: url.endCol } : null;
    }, [hoverLinkAtCell, lineAtVisibleRow]);

    const openLink = useCallback((link: DetectedTerminalLink) => {
      if (link.kind === 'url' && link.uri) {
        void openUrl(link.uri);
      } else if (link.kind === 'path' && link.absolutePath) {
        void openPath(link.absolutePath);
      }
    }, []);


    useEffect(() => {
      const handleModifierChange = (event: KeyboardEvent) => {
        if (event.key !== 'Meta' && event.key !== 'Control') return;
        acceleratorHeldRef.current = event.metaKey || event.ctrlKey;
        updateLinkCursor(hoveredCellRef.current, acceleratorHeldRef.current);
      };
      window.addEventListener('keydown', handleModifierChange);
      window.addEventListener('keyup', handleModifierChange);
      return () => {
        window.removeEventListener('keydown', handleModifierChange);
        window.removeEventListener('keyup', handleModifierChange);
      };
    }, [updateLinkCursor]);

    useEffect(() => () => {
      selectionDragCleanupRef.current?.();
      selectionDragCleanupRef.current = null;
    }, []);

    const sendTrackedMouse = (
      action: 'press' | 'move' | 'release',
      event: React.MouseEvent,
    ): boolean => {
      if (isWorkspaceResizeActive(containerRef.current)) return false;
      const terminal = terminalRef.current;
      const cell = cellFromPointer(event);
      if (!terminal || !cell || !terminal.hasMouseTracking()) return false;
      const activeButton = trackedMouseButtonRef.current;
      if (action === 'move') {
        const shouldReport = shouldReportApplicationMouseMove({
          anyEventMouseTracking: terminal.getMode(1003),
          dragMouseTracking: terminal.getMode(1002),
          activeButton,
          buttons: event.buttons,
        });
        if (!shouldReport) {
          if (activeButton !== null && event.buttons === 0) {
            trackedMouseButtonRef.current = null;
          }
          return true;
        }
      } else if (action === 'release' && activeButton === null) {
        return true;
      }
      const button = action === 'press' ? mouseButton(event.button) : activeButton ?? 0;
      onInputRef.current(applicationMouseInput(
        action,
        button,
        cell.col + 1,
        cell.row + 1,
        terminal.getMode(1006),
        mouseModifiers(event),
      ));
      if (action === 'press') trackedMouseButtonRef.current = button;
      if (action === 'release') trackedMouseButtonRef.current = null;
      event.preventDefault();
      return true;
    };

    const stopSelectionDrag = () => {
      selectionDragCleanupRef.current?.();
      selectionDragCleanupRef.current = null;
    };

    const finishSelectionDrag = async (event: MouseEvent) => {
      stopSelectionDrag();
      if (!selectingRef.current) return;
      selectingRef.current = false;
      selectionPointerStartRef.current = null;
      const wasClick = !selectionDragThresholdMetRef.current;
      if (wasClick) {
        selectionRef.current = null;
        renderSurface(true);
      }
      const text = textForSelectionRange(selectionRef.current);
      selectedTextRef.current = text || null;
      if (text) await writeClipboardText(text);
      const cell = cellFromPointer(event);
      const link = linkAtCell(cell);
      recordPointerHitTest('mouseup', event, {
        activeCell: cell,
        activeUri: link ? link.uri ?? link.absolutePath ?? null : null,
        opensUri: Boolean(link && !text && (event.metaKey || event.ctrlKey)),
        copiedTextLength: text.length,
        phase: 'after-selection',
      });
      if (link && !text && (event.metaKey || event.ctrlKey)) {
        openLink(link);
        return;
      }
      // A plain click inside a completed command block selects the block;
      // clicking its command line additionally highlights the command and
      // arms Cmd+C with the exact command text from the pre-exec marker.
      if (wasClick && !text && !(event.metaKey || event.ctrlKey)) {
        const terminal = terminalRef.current;
        // mousedown already cleared any previous block selection.
        let nextBlockId: number | null = null;
        if (terminal && cell && blockStoreRef.current.hasBlocks()) {
          const bufferRow = bufferRowFromViewportRow(cell.row, terminal.getScrollbackLength(), viewportOffsetRef.current);
          const block = blockStoreRef.current.blockAt(bufferRow);
          if (block) {
            nextBlockId = block.id;
            if (block.outputStartRow !== undefined && bufferRow < block.outputStartRow && block.inputStart) {
              const lastCommandRow = block.outputStartRow - 1;
              const lineLength = selectionLineAtBufferRow(lastCommandRow, 0, terminal.cols).trimEnd().length;
              selectionRef.current = {
                startRow: block.inputStart.row,
                startCol: block.inputStart.col,
                endRow: lastCommandRow,
                endCol: Math.max(lineLength, block.inputStart.col + 1),
              };
              selectedTextRef.current = block.command || null;
            }
          }
        }
        if (nextBlockId !== null || selectionRef.current) {
          selectedBlockIdRef.current = nextBlockId;
          renderSurface(true);
        }
      }
    };

    // Track an in-progress selection on the document rather than the terminal
    // element. The drag must keep updating and finalize even when the pointer
    // crosses a sibling overlay (e.g. a split divider sitting above the pane
    // edge), which would otherwise steal the mousemove/mouseup and strand the
    // selection without ever copying it.
    const startSelectionDrag = () => {
      stopSelectionDrag();
      const onMove = (event: MouseEvent) => {
        if (!selectingRef.current || !selectionRef.current) return;
        // The button was released without a mouseup we observed (e.g. focus
        // loss while over another window): finalize so we never get stuck.
        if ((event.buttons & 1) === 0) {
          void finishSelectionDrag(event);
          return;
        }
        const terminal = terminalRef.current;
        const renderer = rendererRef.current;
        const cell = cellFromPointer(event);
        const pointerStart = selectionPointerStartRef.current;
        if (!terminal || !renderer || !cell || !pointerStart) return;
        if (!selectionDragThresholdMetRef.current) {
          const deltaX = event.clientX - pointerStart.clientX;
          const deltaY = event.clientY - pointerStart.clientY;
          const threshold = renderer.cellWidth * 0.5;
          if (deltaX * deltaX + deltaY * deltaY < threshold * threshold) return;
          selectionDragThresholdMetRef.current = true;
        }
        recordPointerHitTest('mousemove', event, {
          activeCell: cell,
          phase: 'selection-drag',
        });
        const row = bufferRowFromViewportRow(cell.row, terminal.getScrollbackLength(), viewportOffsetRef.current);
        selectionRef.current = { ...selectionRef.current, endRow: row, endCol: cell.col + 1 };
        renderSurface(true);
      };
      const onUp = (event: MouseEvent) => {
        void finishSelectionDrag(event);
      };
      document.addEventListener('mousemove', onMove);
      document.addEventListener('mouseup', onUp);
      selectionDragCleanupRef.current = () => {
        document.removeEventListener('mousemove', onMove);
        document.removeEventListener('mouseup', onUp);
      };
    };

    const contextMenuBlock = contextMenu?.blockId != null
      ? blockStoreRef.current.blockById(contextMenu.blockId)
      : null;
    const contextMenuItems: TerminalContextMenuItem[] = contextMenu ? [
      { id: 'copy', label: 'Copy', shortcut: '⌘C', disabled: !selectedTextRef.current && !contextMenuBlock },
      { id: 'copy-command', label: 'Copy command', shortcut: '⇧⌘C', disabled: !contextMenuBlock?.command },
      { id: 'copy-output', label: 'Copy output', disabled: !contextMenuBlock },
      { id: 'paste', label: 'Paste', shortcut: '⌘V', separatorBefore: true },
      { id: 'filter-block', label: 'Filter block output', separatorBefore: true, disabled: !contextMenuBlock },
      { id: 'find', label: 'Find', shortcut: '⌘F' },
      { id: 'scroll-block-top', label: 'Scroll to top of block', separatorBefore: true, disabled: !contextMenuBlock },
      { id: 'scroll-block-bottom', label: 'Scroll to bottom of block', disabled: !contextMenuBlock },
    ] : [];

    const handleContextMenuSelect = (id: string) => {
      const blockId = contextMenu?.blockId ?? null;
      setContextMenu(null);
      const refocusTerminal = () => containerRef.current?.focus();
      switch (id) {
        case 'copy': {
          const text = selectedTextRef.current ?? selectedBlockCopyText(true);
          if (text) void writeClipboardText(text);
          refocusTerminal();
          break;
        }
        case 'copy-command': {
          copySelectedBlock(false);
          refocusTerminal();
          break;
        }
        case 'copy-output': {
          const output = blockId !== null ? blockOutputText(blockId) : null;
          if (output) void writeClipboardText(output);
          refocusTerminal();
          break;
        }
        case 'paste': {
          void pasteFromClipboard();
          refocusTerminal();
          break;
        }
        case 'filter-block': {
          if (blockId !== null) openBlockFilter(blockId);
          break;
        }
        case 'find': {
          openFind();
          break;
        }
        case 'scroll-block-top': {
          if (blockId !== null) scrollToBlockEdge(blockId, 'top');
          refocusTerminal();
          break;
        }
        case 'scroll-block-bottom': {
          if (blockId !== null) scrollToBlockEdge(blockId, 'bottom');
          refocusTerminal();
          break;
        }
        default:
          break;
      }
    };

    const scrollToFilteredLine = (lineOffset: number) => {
      const blockId = filterUi.blockId;
      const block = blockId !== null ? blockStoreRef.current.blockById(blockId) : null;
      const access = blockRowAccess();
      if (!block || block.outputStartRow === undefined || !access) return;
      const delta = reanchorDelta(block, access);
      if (delta === null) return;
      scrollToBufferRow(block.outputStartRow + delta + lineOffset);
    };

    return (
      <div className="ghostty-terminal-frame">
      <div
        ref={containerRef}
        className={`terminal-container ghostty-terminal${linkCursorActive ? ' ghostty-terminal-link-hover' : ''}`}
        data-terminal-renderer="ghostty-webgl"
        tabIndex={0}
        contentEditable
        suppressContentEditableWarning
        role="textbox"
        aria-label="Terminal input"
        aria-multiline="true"
        spellCheck={false}
        onBeforeInput={(event) => event.preventDefault()}
        onPasteCapture={(event) => {
          const hasImage = Array.from(event.clipboardData.items).some((item) => (
            item.kind === 'file' && item.type.startsWith('image/')
          ));
          if (!hasImage) return;
          // Browser paste events cannot send image bytes through a PTY. Both
          // supported agent TUIs handle Ctrl+V by reading the native clipboard.
          event.preventDefault();
          event.stopPropagation();
          onInputRef.current('\x16');
        }}
        onWheel={(event) => {
          if (isWorkspaceResizeActive(containerRef.current)) {
            event.preventDefault();
            event.stopPropagation();
            return;
          }
          if (event.defaultPrevented) return;
          const terminal = terminalRef.current;
          const renderer = rendererRef.current;
          if (!terminal || !renderer) return;
          event.preventDefault();
          const wheel = consumeWheelRows(
            event.deltaY,
            event.deltaMode,
            renderer.cellHeight,
            terminal.rows,
            wheelRemainderRowsRef.current,
          );
          wheelRemainderRowsRef.current = wheel.remainderRows;
          if (wheel.lines === 0) return;
          const mouseTracking = terminal.hasMouseTracking();
          if (mouseTracking || terminal.isAlternateScreen()) {
            if (selectionRef.current && !applicationSelectionAnchorRef.current) {
              const range = normalizeSelection(selectionRef.current);
              const text = textForSelectionRange(range);
              if (text) {
                selectedTextRef.current = text;
                applicationSelectionAnchorRef.current = createApplicationSelectionAnchor(
                  range,
                  (row) => selectionLineAtBufferRow(row, 0, terminal.cols).trimEnd(),
                );
              }
            }
            const cell = cellFromPointer(event);
            onInputRef.current(applicationWheelInput(
              wheel.lines,
              (cell?.col ?? 0) + 1,
              (cell?.row ?? 0) + 1,
              mouseTracking,
              terminal.getMode(1006),
            ));
            return;
          }
          const scrollbackLength = terminal.getScrollbackLength();
          const nextOffset = Math.max(0, Math.min(
            scrollbackLength,
            viewportOffsetRef.current - wheel.lines,
          ));
          if (nextOffset !== viewportOffsetRef.current) {
            hoverGenerationRef.current += 1;
          }
          viewportOffsetRef.current = nextOffset;
          if ((nextOffset === 0 && wheel.lines > 0) || (nextOffset === scrollbackLength && wheel.lines < 0)) {
            wheelRemainderRowsRef.current = 0;
          }
          renderSurface(true);
        }}
        onMouseDown={(event) => {
          if (isWorkspaceResizeActive(containerRef.current)) {
            event.preventDefault();
            event.stopPropagation();
            return;
          }
          containerRef.current?.focus();
          const terminal = terminalRef.current;
          const cell = cellFromPointer(event);
          if (!terminal || !cell) return;
          if (event.button === 2) {
            // Right-click belongs to onContextMenu (or to a mouse-tracking
            // TUI): it must not clear the selection or start a drag.
            sendTrackedMouse('press', event);
            return;
          }
          const link = linkAtCell(cell);
          const opensUri = Boolean(link && (event.metaKey || event.ctrlKey));
          recordPointerHitTest('mousedown', event, {
            activeCell: cell,
            activeUri: link ? link.uri ?? link.absolutePath ?? null : null,
            opensUri,
            phase: 'before-selection',
          });
          if (!opensUri && !event.altKey && sendTrackedMouse('press', event)) return;
          const row = bufferRowFromViewportRow(cell.row, terminal.getScrollbackLength(), viewportOffsetRef.current);
          if (event.detail === 3) {
            // Triple click selects the visual row.
            selectionRef.current = { startRow: row, startCol: 0, endRow: row, endCol: terminal.cols };
            applicationSelectionAnchorRef.current = null;
            selectedBlockIdRef.current = null;
            selectingRef.current = false;
            const rowText = textForSelectionRange(selectionRef.current);
            selectedTextRef.current = rowText || null;
            renderSurface(true);
            if (rowText) void writeClipboardText(rowText);
            return;
          }
          selectedTextRef.current = null;
          applicationSelectionAnchorRef.current = null;
          selectedBlockIdRef.current = null;
          selectingRef.current = true;
          selectionPointerStartRef.current = { clientX: event.clientX, clientY: event.clientY };
          selectionDragThresholdMetRef.current = false;
          selectionRef.current = { startRow: row, startCol: cell.col, endRow: row, endCol: cell.col };
          renderSurface(true);
          startSelectionDrag();
        }}
        onMouseMove={(event) => {
          if (isWorkspaceResizeActive(containerRef.current)) {
            event.preventDefault();
            event.stopPropagation();
            return;
          }
          // While selecting, the drag is owned by the document listeners in
          // startSelectionDrag() so it survives crossing sibling overlays.
          if (selectingRef.current) return;
          const hoveredCell = cellFromPointer(event);
          hoveredCellRef.current = hoveredCell;
          acceleratorHeldRef.current = event.metaKey || event.ctrlKey;
          detectHoverLink(hoveredCell);
          updateLinkCursor(hoveredCell, acceleratorHeldRef.current);
          const hoveredLink = hoverLinkAtCell(hoveredCell);
          const hoveredUri = hoveredLink ? hoveredLink.uri ?? hoveredLink.absolutePath ?? null : null;
          if (acceleratorHeldRef.current || hoveredUri) {
            recordPointerHitTest('mousemove', event, {
              activeCell: hoveredCell,
              activeUri: hoveredUri,
              phase: 'hover',
            });
          }
          sendTrackedMouse('move', event);
        }}
        onMouseLeave={() => {
          hoveredCellRef.current = null;
          detectHoverLink(null);
          setLinkCursorActive(false);
        }}
        onContextMenu={(event) => {
          // Always suppress the webview's own menu inside the terminal.
          event.preventDefault();
          const terminal = terminalRef.current;
          if (!terminal) return;
          // TUI apps that track the mouse own right-click.
          if (terminal.hasMouseTracking()) return;
          const cell = cellFromPointer(event);
          let blockId: number | null = null;
          if (cell && blockStoreRef.current.hasBlocks()) {
            const bufferRow = bufferRowFromViewportRow(cell.row, terminal.getScrollbackLength(), viewportOffsetRef.current);
            blockId = blockStoreRef.current.blockAt(bufferRow)?.id ?? null;
          }
          // Right-clicking a block selects it (outline + arms ⌘C/⇧⌘C), same
          // as a plain click, but without clearing an existing text selection.
          if (blockId !== null && selectedBlockIdRef.current !== blockId) {
            selectedBlockIdRef.current = blockId;
            renderSurface(true);
          }
          const frameRect = containerRef.current?.parentElement?.getBoundingClientRect();
          setContextMenu({
            x: event.clientX - (frameRect?.left ?? 0),
            y: event.clientY - (frameRect?.top ?? 0),
            blockId,
          });
        }}
        onMouseUp={(event) => {
          if (isWorkspaceResizeActive(containerRef.current)) {
            event.preventDefault();
            event.stopPropagation();
            return;
          }
          // A selection release is finalized by the document mouseup listener so
          // it fires even when the pointer ends over a sibling overlay.
          if (selectingRef.current) return;
          recordPointerHitTest('mouseup', event, {
            phase: 'tracked-mouse-release',
          });
          sendTrackedMouse('release', event);
        }}
        onDoubleClick={async (event) => {
          const terminal = terminalRef.current;
          const cell = cellFromPointer(event);
          recordPointerHitTest('doubleclick', event, {
            activeCell: cell,
            phase: 'before-word-selection',
          });
          if (!terminal || !cell || (terminal.hasMouseTracking() && !event.altKey)) return;
          const range = wordRangeAtColumn(lineAtVisibleRow(cell.row), cell.col);
          if (!range) return;
          const row = bufferRowFromViewportRow(cell.row, terminal.getScrollbackLength(), viewportOffsetRef.current);
          selectionRef.current = {
            startRow: row,
            startCol: range.startCol,
            endRow: row,
            endCol: range.endCol,
          };
          applicationSelectionAnchorRef.current = null;
          selectedBlockIdRef.current = null;
          const text = textForSelectionRange(selectionRef.current);
          selectedTextRef.current = text || null;
          renderSurface(true);
          if (text) await writeClipboardText(text);
        }}
        onCopy={(event) => {
          // In the packaged app plain Cmd+C never reaches keydown: the native
          // Edit > Copy menu intercepts the key equivalent and WebKit fires
          // this clipboard event instead. Serve terminal selections and
          // selected blocks from here so both the shortcut and the menu work.
          const text = selectedTextRef.current ?? selectedBlockCopyText(true);
          if (!text) return;
          event.preventDefault();
          if (event.clipboardData) {
            event.clipboardData.setData('text/plain', text);
          } else {
            void writeClipboardText(text);
          }
        }}
        onKeyDown={(event) => {
          if (!event.metaKey || event.key.toLowerCase() !== 'c') return;
          if (event.shiftKey) {
            // Styled-markdown copy of a text selection keeps priority; with a
            // block selected and no text selection, copy just the command.
            const text = selectedMarkdown();
            if (text) {
              void writeClipboardText(text);
              event.preventDefault();
            } else if (copySelectedBlock(false)) {
              event.preventDefault();
            }
            return;
          }
          // Cmd+C: a text selection (e.g. a clicked command) wins; otherwise a
          // selected block copies command + output.
          if (selectedTextRef.current) {
            void writeClipboardText(selectedTextRef.current);
            event.preventDefault();
          } else if (copySelectedBlock(true)) {
            event.preventDefault();
          }
        }}
      >
        <canvas ref={canvasRef} />
        {error && <div className="ghostty-terminal-error">{error}</div>}
      </div>
      {findUi.open && (
        <div className="ghostty-find-bar" data-testid="ghostty-find-bar">
          <input
            ref={findInputRef}
            className="ghostty-find-input"
            data-testid="ghostty-find-input"
            type="text"
            placeholder="Find"
            spellCheck={false}
            autoComplete="off"
            defaultValue={findQueryRef.current}
            onChange={(event) => {
              findQueryRef.current = event.target.value;
              if (findRescanTimerRef.current) clearTimeout(findRescanTimerRef.current);
              findRescanTimerRef.current = setTimeout(() => {
                findRescanTimerRef.current = null;
                runFindScan();
              }, 150);
            }}
            onKeyDown={(event) => {
              if (event.key === 'Escape') {
                event.preventDefault();
                closeFind();
              } else if (event.key === 'Enter') {
                event.preventDefault();
                findNavigate(event.shiftKey ? 1 : -1);
              }
              event.stopPropagation();
            }}
          />
          <button
            type="button"
            className={`ghostty-find-button ghostty-find-case${findUi.caseSensitive ? ' ghostty-find-case-active' : ''}`}
            aria-label="Match case"
            aria-pressed={findUi.caseSensitive}
            title="Match case"
            onClick={() => {
              findCaseSensitiveRef.current = !findCaseSensitiveRef.current;
              setFindUi((ui) => ({ ...ui, caseSensitive: findCaseSensitiveRef.current }));
              runFindScan();
              findInputRef.current?.focus();
            }}
          >
            Aa
          </button>
          <span className="ghostty-find-count" data-testid="ghostty-find-count">
            {findUi.matchCount > 0 ? `${findUi.focusedIndex + 1}/${findUi.matchCount}` : '0/0'}
          </span>
          <button
            type="button"
            className="ghostty-find-button"
            aria-label="Previous match"
            title="Previous match (Enter)"
            onClick={() => { findNavigate(-1); findInputRef.current?.focus(); }}
          >
            ▲
          </button>
          <button
            type="button"
            className="ghostty-find-button"
            aria-label="Next match"
            title="Next match (Shift+Enter)"
            onClick={() => { findNavigate(1); findInputRef.current?.focus(); }}
          >
            ▼
          </button>
          <button
            type="button"
            className="ghostty-find-button"
            aria-label="Close find"
            title="Close (Esc)"
            onClick={closeFind}
          >
            ✕
          </button>
        </div>
      )}
      {filterUi.open && (
        <div className="ghostty-filter-panel" data-testid="ghostty-filter-panel">
          <div className="ghostty-filter-bar">
            <input
              ref={filterInputRef}
              className="ghostty-find-input ghostty-filter-input"
              data-testid="ghostty-filter-input"
              type="text"
              placeholder="Filter block output"
              spellCheck={false}
              autoComplete="off"
              defaultValue=""
              onChange={(event) => {
                filterQueryRef.current = event.target.value;
                if (filterRescanTimerRef.current) clearTimeout(filterRescanTimerRef.current);
                filterRescanTimerRef.current = setTimeout(() => {
                  filterRescanTimerRef.current = null;
                  runBlockFilter(filterUi.blockId, filterUi.caseSensitive);
                }, 150);
              }}
              onKeyDown={(event) => {
                if (event.key === 'Escape') {
                  event.preventDefault();
                  closeBlockFilter();
                }
                event.stopPropagation();
              }}
            />
            <button
              type="button"
              className={`ghostty-find-button ghostty-find-case${filterUi.caseSensitive ? ' ghostty-find-case-active' : ''}`}
              aria-label="Match case"
              aria-pressed={filterUi.caseSensitive}
              title="Match case"
              onClick={() => {
                const caseSensitive = !filterUi.caseSensitive;
                setFilterUi((ui) => ({ ...ui, caseSensitive }));
                runBlockFilter(filterUi.blockId, caseSensitive);
                filterInputRef.current?.focus();
              }}
            >
              Aa
            </button>
            <span className="ghostty-find-count" data-testid="ghostty-filter-count">
              {filterMatches.length} {filterMatches.length === 1 ? 'line' : 'lines'}
            </span>
            <button
              type="button"
              className="ghostty-find-button"
              aria-label="Close filter"
              title="Close (Esc)"
              onClick={closeBlockFilter}
            >
              ✕
            </button>
          </div>
          {filterQueryRef.current && (
            <div className="ghostty-filter-results" data-testid="ghostty-filter-results">
              {filterMatches.length === 0 ? (
                <div className="ghostty-filter-empty">No matching lines</div>
              ) : (
                filterMatches.map((line) => (
                  <button
                    key={line.lineOffset}
                    type="button"
                    className="ghostty-filter-line"
                    title="Scroll to line"
                    onClick={() => scrollToFilteredLine(line.lineOffset)}
                  >
                    {lineSegments(line).map((segment, index) => (
                      segment.match
                        ? <mark key={index} className="ghostty-filter-match">{segment.text}</mark>
                        : <span key={index}>{segment.text}</span>
                    ))}
                  </button>
                ))
              )}
            </div>
          )}
        </div>
      )}
      {contextMenu && (
        <TerminalContextMenu
          position={{ x: contextMenu.x, y: contextMenu.y }}
          items={contextMenuItems}
          onSelect={handleContextMenuSelect}
          onClose={() => setContextMenu(null)}
        />
      )}
      </div>
    );
  },
);
