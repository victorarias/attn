import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
} from 'react';
import { Ghostty, InputHandler, CellFlags, type GhosttyCell, type GhosttyTerminal as GhosttyModel } from 'ghostty-web';
import ghosttyWasmUrl from 'ghostty-web/ghostty-vt.wasm?url';
import { openUrl } from '@tauri-apps/plugin-opener';
import { cleanTerminalLines } from '../utils/terminalMarkdown';
import { writeClipboardText } from '../utils/clipboardBridge';
import {
  FONT_FAMILY,
  TERMINAL_SCROLLBACK_LINES,
  getTerminalTheme,
  type ResolvedTheme,
} from '../utils/terminalSizing';
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
  viewportBufferStart,
  viewportRowFromBufferRow,
  type ApplicationSelectionAnchor,
} from '../utils/ghosttyScroll';
import { installTerminalKeyHandler } from './SessionTerminalWorkspace/terminalKeyHandler';
import {
  WebGlTerminalRenderer,
  type WebGlSelection,
} from './GhosttyWebGlRenderer';
import './GhosttyTerminal.css';

interface GhosttyTerminalProps {
  fontSize: number;
  resolvedTheme?: ResolvedTheme;
  debugName: string;
  runtimeLogMeta?: {
    sessionId: string;
    paneId: string;
    runtimeId: string;
    paneKind: 'main' | 'shell';
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
  focus: () => boolean;
  typeTextViaInput: (text: string) => boolean;
  isInputFocused: () => boolean;
  write: (data: string | Uint8Array, options?: { suppressResponses?: boolean }) => Promise<void>;
  resizeLocal: (cols: number, rows: number) => void;
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

const URL_RE = /\b(?:https?:\/\/|mailto:|ftp:\/\/|ssh:\/\/|git:\/\/|tel:|magnet:|gemini:\/\/|gopher:\/\/|news:)[^\s<>()]+/g;

function colorNumber(value: string): number {
  return Number.parseInt(value.slice(1), 16);
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

function cellText(terminal: GhosttyModel, cells: GhosttyCell[], row: number): string {
  let text = '';
  for (let col = 0; col < terminal.cols; col += 1) {
    const cell = cells[col];
    if (!cell || cell.width === 0) continue;
    text += cell.grapheme_len > 0
      ? terminal.getGraphemeString(row, col)
      : cell.codepoint > 0 ? String.fromCodePoint(cell.codepoint) : ' ';
  }
  return text.trimEnd();
}

export const GhosttyTerminal = forwardRef<GhosttyTerminalHandle, GhosttyTerminalProps>(
  function GhosttyTerminal({ fontSize, resolvedTheme = 'dark', debugName, runtimeLogMeta, onInput, onReady, onResize }, ref) {
    const containerRef = useRef<HTMLDivElement>(null);
    const canvasRef = useRef<HTMLCanvasElement>(null);
    const terminalRef = useRef<GhosttyModel | null>(null);
    const rendererRef = useRef<WebGlTerminalRenderer | null>(null);
    const inputRef = useRef<InputHandler | null>(null);
    const viewportOffsetRef = useRef(0);
    const wheelRemainderRowsRef = useRef(0);
    const selectionRef = useRef<SelectionRange | null>(null);
    const selectedTextRef = useRef<string | null>(null);
    const applicationSelectionAnchorRef = useRef<ApplicationSelectionAnchor | null>(null);
    const selectingRef = useRef(false);
    const trackedMouseButtonRef = useRef<number | null>(null);
    const writeChainRef = useRef(Promise.resolve());
    const renderCountRef = useRef(0);
    const writeCountRef = useRef(0);
    const lastRenderAtRef = useRef(0);
    const lastWriteAtRef = useRef(0);
    const readyRef = useRef(false);
    const startupRef = useRef(emptyStartup());
    const onInputRef = useRef(onInput);
    const onReadyRef = useRef(onReady);
    const onResizeRef = useRef(onResize);
    const runtimeMetaRef = useRef(runtimeLogMeta);
    const fontSizeRef = useRef(fontSize);
    const resolvedThemeRef = useRef(resolvedTheme);
    const debugNameRef = useRef(debugName);
    const [error, setError] = useState<string | null>(null);

    onInputRef.current = onInput;
    onReadyRef.current = onReady;
    onResizeRef.current = onResize;
    runtimeMetaRef.current = runtimeLogMeta;
    fontSizeRef.current = fontSize;
    resolvedThemeRef.current = resolvedTheme;
    debugNameRef.current = debugName;

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
      const range = selectionRef.current ? normalizeSelection(selectionRef.current) : null;
      const scrollbackLength = terminal.getScrollbackLength();
      const overlay: WebGlSelection | null = range ? {
        startRow: viewportRowFromBufferRow(range.startRow, scrollbackLength, viewportOffsetRef.current),
        startCol: range.startCol,
        endRow: viewportRowFromBufferRow(range.endRow, scrollbackLength, viewportOffsetRef.current),
        endCol: range.endCol,
        color: getTerminalTheme(resolvedThemeRef.current).selectionBackground,
      } : null;
      const sample = renderer.render(terminal, force, getViewportCells(), overlay, viewportOffsetRef.current);
      if (sample) {
        renderCountRef.current += 1;
        lastRenderAtRef.current = Date.now();
      }
    }, [getViewportCells]);

    const lineAtVisibleRow = useCallback((row: number): string => {
      const terminal = terminalRef.current;
      if (!terminal) return '';
      const cells = getViewportCells() ?? terminal.getViewport();
      return cellText(terminal, cells.slice(row * terminal.cols, (row + 1) * terminal.cols), row);
    }, [getViewportCells]);

    const lineAtBufferRow = useCallback((row: number): string => {
      const terminal = terminalRef.current;
      if (!terminal) return '';
      const history = terminal.getScrollbackLength();
      if (row < history) {
        const line = terminal.getScrollbackLine(row);
        return line ? cellText(terminal, line, row) : '';
      }
      const viewportRow = row - history;
      if (viewportRow < 0 || viewportRow >= terminal.rows) return '';
      const active = terminal.getViewport();
      return cellText(
        terminal,
        active.slice(viewportRow * terminal.cols, (viewportRow + 1) * terminal.cols),
        viewportRow,
      );
    }, []);

    const textForSelectionRange = useCallback((selection: SelectionRange | null) => {
      const terminal = terminalRef.current;
      const range = selection ? normalizeSelection(selection) : null;
      if (!terminal || !range) return '';
      const lines: string[] = [];
      for (let row = range.startRow; row <= range.endRow; row += 1) {
        const text = lineAtBufferRow(row);
        const start = row === range.startRow ? range.startCol : 0;
        const end = row === range.endRow ? range.endCol : terminal.cols;
        lines.push(text.slice(start, end));
      }
      return cleanTerminalLines(lines).join('\n');
    }, [lineAtBufferRow]);

    const selectedText = useCallback(() => {
      return selectedTextRef.current ?? textForSelectionRange(selectionRef.current);
    }, [textForSelectionRange]);

    const getText = useCallback(() => {
      const terminal = terminalRef.current;
      if (!terminal) return '';
      const lines: string[] = [];
      for (let row = 0; row < terminal.getScrollbackLength(); row += 1) {
        const line = terminal.getScrollbackLine(row);
        if (line) lines.push(cellText(terminal, line, row));
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

    const write = useCallback((data: string | Uint8Array, options?: { suppressResponses?: boolean }) => {
      writeChainRef.current = writeChainRef.current.then(async () => {
        const terminal = terminalRef.current;
        if (!terminal) return;
        const searchableOutput = typeof data === 'string' ? data : new TextDecoder().decode(data);
        if (searchableOutput) {
          const osc52 = /\x1b\]52;[^;]*;([A-Za-z0-9+/=]+)(?:\x07|\x1b\\)/g;
          for (const match of searchableOutput.matchAll(osc52)) {
            try {
              const bytes = Uint8Array.from(atob(match[1]), (char) => char.charCodeAt(0));
              void writeClipboardText(new TextDecoder().decode(bytes));
            } catch {
              // Ignore malformed clipboard output.
            }
          }
        }
        const scrollbackBefore = terminal.getScrollbackLength();
        const viewportOffsetBefore = viewportOffsetRef.current;
        terminal.write(data);
        while (terminal.hasResponse()) {
          const response = terminal.readResponse();
          if (response && !options?.suppressResponses) onInputRef.current(response);
        }
        viewportOffsetRef.current = offsetAfterWrite(
          viewportOffsetBefore,
          scrollbackBefore,
          terminal.getScrollbackLength(),
        );
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
        renderSurface(true);
      });
      return writeChainRef.current;
    }, [lineAtVisibleRow, renderSurface]);

    const fit = useCallback(() => {
      const container = containerRef.current;
      const terminal = terminalRef.current;
      const renderer = rendererRef.current;
      if (!container || !terminal || !renderer) return;
      // Inactive session wrappers use display:none. Resizing the Ghostty
      // model from that hidden geometry discards an idle alternate-screen
      // frame before the session becomes visible again.
      if (runtimeMetaRef.current && !runtimeMetaRef.current.isActiveSession) return;
      const dims = renderer.fitDimensions(container.clientWidth, container.clientHeight);
      if (dims.cols === terminal.cols && dims.rows === terminal.rows) return;
      terminal.resize(dims.cols, dims.rows);
      renderer.resize(dims.cols, dims.rows);
      renderSurface(true);
      onResizeRef.current(dims.cols, dims.rows, { reason: 'ghostty_fit' });
    }, [renderSurface]);

    useImperativeHandle(ref, () => ({
      fit,
      focus: () => {
        const container = containerRef.current;
        if (!container) return false;
        container.focus();
        return document.activeElement === container;
      },
      typeTextViaInput: (text: string) => { onInputRef.current(text.replace(/\n/g, '\r')); return true; },
      isInputFocused: () => document.activeElement === containerRef.current,
      write,
      resizeLocal: (cols, rows) => {
        const terminal = terminalRef.current;
        const renderer = rendererRef.current;
        if (!terminal || !renderer) return;
        terminal.resize(cols, rows);
        renderer.resize(cols, rows);
        renderSurface(true);
      },
      reset: () => { void write('\x1bc'); },
      scrollToTop: () => {
        const terminal = terminalRef.current;
        if (!terminal) return false;
        viewportOffsetRef.current = terminal.getScrollbackLength();
        wheelRemainderRowsRef.current = 0;
        renderSurface(true);
        return true;
      },
      getText,
      getSize: () => terminalRef.current ? { cols: terminalRef.current.cols, rows: terminalRef.current.rows } : null,
      getVisibleContent,
      getVisibleStyleSummary,
      drain: () => writeChainRef.current,
    }), [fit, getText, getVisibleContent, getVisibleStyleSummary, renderSurface, write]);

    useEffect(() => {
      let active = true;
      let observer: ResizeObserver | null = null;
      const container = containerRef.current;
      const canvas = canvasRef.current;
      if (!container || !canvas) return;
      const perfId = `ghostty-${debugNameRef.current}`;
      void Ghostty.load(ghosttyWasmUrl).then((ghostty) => {
        if (!active) return;
        const theme = getTerminalTheme(resolvedThemeRef.current);
        const terminal = ghostty.createTerminal(80, 24, {
          scrollbackLimit: TERMINAL_SCROLLBACK_LINES,
          fgColor: colorNumber(theme.foreground),
          bgColor: colorNumber(theme.background),
          cursorColor: colorNumber(theme.cursor),
        });
        const renderer = new WebGlTerminalRenderer(canvas, fontSizeRef.current, FONT_FAMILY, {
          background: theme.background,
          foreground: theme.foreground,
          cursor: theme.cursor,
        });
        terminalRef.current = terminal;
        rendererRef.current = renderer;
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
          focus: () => { container.focus(); return document.activeElement === container; },
          typeTextViaInput: (text) => { onInputRef.current(text.replace(/\n/g, '\r')); return true; },
          isInputFocused: () => document.activeElement === container,
          write,
          resizeLocal: (cols, rows) => { terminal.resize(cols, rows); renderer.resize(cols, rows); renderSurface(true); },
          reset: () => { void write('\x1bc'); },
          scrollToTop: () => { viewportOffsetRef.current = terminal.getScrollbackLength(); wheelRemainderRowsRef.current = 0; renderSurface(true); return true; },
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
          scrollbackLimit: TERMINAL_SCROLLBACK_LINES,
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
        observer?.disconnect();
        canvas.removeEventListener('webglcontextlost', handleContextLost);
        unregister();
        inputRef.current?.dispose();
        rendererRef.current?.dispose();
        terminalRef.current?.free();
        inputRef.current = null;
        rendererRef.current = null;
        terminalRef.current = null;
      };
    }, [fit, getText, getVisibleContent, getVisibleStyleSummary, renderSurface, write]);

    useEffect(() => {
      const terminal = terminalRef.current;
      const canvas = canvasRef.current;
      if (!terminal || !canvas || !readyRef.current) return;
      const theme = getTerminalTheme(resolvedTheme);
      rendererRef.current?.dispose();
      const renderer = new WebGlTerminalRenderer(canvas, fontSize, FONT_FAMILY, {
        background: theme.background,
        foreground: theme.foreground,
        cursor: theme.cursor,
      });
      rendererRef.current = renderer;
      renderer.resize(terminal.cols, terminal.rows);
      fit();
      renderSurface(true);
    }, [fit, fontSize, renderSurface, resolvedTheme]);

    const cellFromPointer = (event: React.MouseEvent) => {
      const renderer = rendererRef.current;
      const rect = containerRef.current?.getBoundingClientRect();
      if (!renderer || !rect) return null;
      return {
        row: Math.max(0, Math.min((terminalRef.current?.rows ?? 1) - 1, Math.floor((event.clientY - rect.top) / renderer.cellHeight))),
        col: Math.max(0, Math.min((terminalRef.current?.cols ?? 1), Math.floor((event.clientX - rect.left) / renderer.cellWidth))),
      };
    };

    const mouseModifiers = (event: React.MouseEvent) =>
      (event.shiftKey ? 4 : 0) + (event.altKey ? 8 : 0) + (event.ctrlKey ? 16 : 0);

    const mouseButton = (button: number) => {
      if (button === 1) return 1;
      if (button === 2) return 2;
      return 0;
    };

    const sendTrackedMouse = (
      action: 'press' | 'move' | 'release',
      event: React.MouseEvent,
    ): boolean => {
      const terminal = terminalRef.current;
      const cell = cellFromPointer(event);
      if (!terminal || !cell || !terminal.hasMouseTracking()) return false;
      const activeButton = trackedMouseButtonRef.current;
      if (action === 'move') {
        if (!terminal.getMode(1003) && !(activeButton !== null && terminal.getMode(1002))) {
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

    return (
      <div
        ref={containerRef}
        className="terminal-container ghostty-terminal"
        data-terminal-renderer="ghostty-webgl"
        tabIndex={0}
        onWheel={(event) => {
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
                applicationSelectionAnchorRef.current = createApplicationSelectionAnchor(range, lineAtBufferRow);
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
          viewportOffsetRef.current = nextOffset;
          if ((nextOffset === 0 && wheel.lines > 0) || (nextOffset === scrollbackLength && wheel.lines < 0)) {
            wheelRemainderRowsRef.current = 0;
          }
          renderSurface(true);
        }}
        onMouseDown={(event) => {
          containerRef.current?.focus();
          const terminal = terminalRef.current;
          const cell = cellFromPointer(event);
          if (!terminal || !cell) return;
          if (!event.shiftKey && sendTrackedMouse('press', event)) return;
          const row = bufferRowFromViewportRow(cell.row, terminal.getScrollbackLength(), viewportOffsetRef.current);
          selectedTextRef.current = null;
          applicationSelectionAnchorRef.current = null;
          selectingRef.current = true;
          selectionRef.current = { startRow: row, startCol: cell.col, endRow: row, endCol: cell.col };
          renderSurface(true);
        }}
        onMouseMove={(event) => {
          if (!selectingRef.current || !selectionRef.current) {
            sendTrackedMouse('move', event);
            return;
          }
          const terminal = terminalRef.current;
          const cell = cellFromPointer(event);
          if (!terminal || !cell) return;
          const row = bufferRowFromViewportRow(cell.row, terminal.getScrollbackLength(), viewportOffsetRef.current);
          selectionRef.current = { ...selectionRef.current, endRow: row, endCol: cell.col + 1 };
          renderSurface(true);
        }}
        onMouseUp={async (event) => {
          if (!selectingRef.current) {
            sendTrackedMouse('release', event);
            return;
          }
          selectingRef.current = false;
          const text = textForSelectionRange(selectionRef.current);
          selectedTextRef.current = text || null;
          if (text) await writeClipboardText(text);
          if ((event.metaKey || event.ctrlKey) && !text) {
            const cell = cellFromPointer(event);
            const line = cell ? lineAtVisibleRow(cell.row) : '';
            for (const match of line.matchAll(URL_RE)) {
              const start = match.index ?? -1;
              const uri = match[0].replace(/[.,;:!?]+$/, '');
              if (cell && cell.col >= start && cell.col < start + uri.length) {
                void openUrl(uri);
                break;
              }
            }
          }
        }}
        onKeyDown={(event) => {
          if (event.metaKey && event.shiftKey && event.key.toLowerCase() === 'c') {
            const text = selectedText();
            if (text) {
              void writeClipboardText(text);
              event.preventDefault();
            }
          }
        }}
      >
        <canvas ref={canvasRef} />
        {error && <div className="ghostty-terminal-error">{error}</div>}
      </div>
    );
  },
);
