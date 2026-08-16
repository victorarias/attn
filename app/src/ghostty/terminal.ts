import {
  CELL_DATA_CODEPOINT,
  CELL_DATA_HAS_HYPERLINK,
  CELL_DATA_HAS_STYLING,
  CELL_DATA_WIDE,
  CELL_WIDE_WIDTH,
  CELLS_DATA_BG_COLOR,
  CELLS_DATA_FG_COLOR,
  CELLS_DATA_GRAPHEMES_LEN,
  CELLS_DATA_STYLE,
  COLORS_OFF_BACKGROUND,
  COLORS_OFF_CURSOR,
  COLORS_OFF_CURSOR_HAS_VALUE,
  COLORS_OFF_FOREGROUND,
  COLORS_SIZE,
  CURSOR_STYLE_BAR,
  CURSOR_STYLE_UNDERLINE,
  DIRTY_FALSE,
  DIRTY_FULL,
  GHOSTTY_NO_VALUE,
  GHOSTTY_SUCCESS,
  GRID_REF_SIZE,
  MOUSE_TRACKING_NONE,
  POINT_OFF_TAG,
  POINT_OFF_X,
  POINT_OFF_Y,
  POINT_SIZE,
  POINT_TAG_ACTIVE,
  POINT_TAG_HISTORY,
  RENDER_DATA_CURSOR_BLINKING,
  RENDER_DATA_CURSOR_VIEWPORT_HAS_VALUE,
  RENDER_DATA_CURSOR_VIEWPORT_X,
  RENDER_DATA_CURSOR_VIEWPORT_Y,
  RENDER_DATA_CURSOR_VISIBLE,
  RENDER_DATA_CURSOR_VISUAL_STYLE,
  RENDER_DATA_DIRTY,
  RENDER_DATA_ROW_ITERATOR,
  RENDER_OPT_DIRTY,
  ROW_DATA_CELLS,
  ROW_DATA_CELLS_RAW,
  ROW_DATA_DIRTY,
  ROW_DATA_RAW,
  ROW_OPT_DIRTY,
  ROW_RAW_DATA_GRAPHEME,
  ROW_RAW_DATA_WRAP,
  SCREEN_TYPE_ALTERNATE,
  SNAPSHOT_DATA_HISTORY_ROWS_PRIMARY,
  SNAPSHOT_DATA_PROGRESS_ROWS,
  STYLE_OFF_BLINK,
  STYLE_OFF_BOLD,
  STYLE_OFF_FAINT,
  STYLE_OFF_INVERSE,
  STYLE_OFF_INVISIBLE,
  STYLE_OFF_ITALIC,
  STYLE_OFF_STRIKETHROUGH,
  STYLE_OFF_UNDERLINE,
  STYLE_SIZE,
  TERMINAL_DATA_ACTIVE_SCREEN,
  TERMINAL_DATA_COLS,
  TERMINAL_DATA_COLOR_BACKGROUND,
  TERMINAL_DATA_COLOR_FOREGROUND,
  TERMINAL_DATA_MODE,
  TERMINAL_DATA_MOUSE_TRACKING,
  TERMINAL_DATA_ROWS,
  TERMINAL_DATA_SCROLLBACK_ROWS,
  TERMINAL_OPT_COLOR_BACKGROUND,
  TERMINAL_OPT_COLOR_CURSOR,
  TERMINAL_OPT_COLOR_FOREGROUND,
  TERMINAL_OPT_COLOR_PALETTE,
  TERMINAL_OPT_SCROLLBACK_MAX_BYTES,
  TERMINAL_OPT_WRITE_PTY,
  packMode,
  type GhosttyExports,
} from './abi';
import { installCallback } from './callback';

/** Attribute bits on GhosttyCell.flags. */
export const CellFlags = {
  BOLD: 1,
  ITALIC: 2,
  UNDERLINE: 4,
  STRIKETHROUGH: 8,
  INVERSE: 16,
  INVISIBLE: 32,
  BLINK: 64,
  FAINT: 128,
} as const;

/**
 * One rendered cell. Colors are always resolved: a cell without an explicit
 * color carries the terminal's default, so a renderer never has to know which
 * of the two it got.
 */
export interface GhosttyCell {
  codepoint: number;
  fg_r: number;
  fg_g: number;
  fg_b: number;
  bg_r: number;
  bg_g: number;
  bg_b: number;
  flags: number;
  /** Layout width in cells: 1 normal, 2 wide, 0 for a wide char's spacer. */
  width: number;
  hyperlink_id: number;
  /** Combining codepoints beyond the base one; 0 for a plain cell. */
  grapheme_len: number;
}

export interface RGB {
  r: number;
  g: number;
  b: number;
}

export interface RenderStateColors {
  background: RGB;
  foreground: RGB;
  cursor: RGB | null;
}

export interface RenderStateCursor {
  x: number;
  y: number;
  viewportX: number;
  viewportY: number;
  visible: boolean;
  blinking: boolean;
  style: 'block' | 'underline' | 'bar';
}

export interface GhosttyTerminalConfig {
  scrollbackLimit?: number;
  fgColor?: number;
  bgColor?: number;
  cursorColor?: number;
  palette?: number[];
}

/**
 * The scrollback half of a snapshot restore, decoded a page at a time so the
 * first paint does not wait on it.
 */
export interface SnapshotHistory {
  /** Scrollback rows the snapshot declares, before any page is applied. */
  readonly rows: number;
  /**
   * Prepend one page, returning the rows it added — zero when the page no
   * longer fits the live terminal — or null once there are none left.
   */
  next(): number | null;
  /** Give up on the rest. The terminal keeps what was already prepended. */
  close(): void;
}

const HYPERLINK_URI_CAP = 2048;
// Codepoints in one grapheme cluster. Unicode's longest legitimate clusters are
// well under this; anything longer is truncated rather than grown for.
const GRAPHEME_CAP = 64;
const SCRATCH_CAP = 1024;

function newCell(): GhosttyCell {
  return {
    codepoint: 0,
    fg_r: 0, fg_g: 0, fg_b: 0,
    bg_r: 0, bg_g: 0, bg_b: 0,
    flags: 0,
    width: 1,
    hyperlink_id: 0,
    grapheme_len: 0,
  };
}

/**
 * The browser-side terminal model: a libghostty-vt terminal plus the render
 * state a renderer reads each frame.
 *
 * Viewport reads go through the render state, which is the API built for a
 * render loop. Scrollback reads go through grid references, which upstream
 * warns are not render-loop material — measured at 0.72ms for a full 200x50
 * scrolled-back viewport, against 0.53ms for the same volume through the
 * render state. That is inside a frame, and only a scrolled-back pane pays it.
 */
export class GhosttyTerminal {
  private readonly e: GhosttyExports;
  private handle: number;
  private state: number;
  private iterator: number;
  private cells: number;

  private _cols: number;
  private _rows: number;

  // Scratch wasm memory, allocated once and reused. Every out-parameter the
  // C API writes lands in one of these.
  private readonly pScratch: number;
  private readonly pPoint: number;
  private readonly pRef: number;
  private readonly pStyle: number;
  private readonly pColors: number;
  private readonly pGraphemes: number;
  private readonly pUri: number;

  private writeBufPtr = 0;
  private writeBufLen = 0;

  private cellPool: GhosttyCell[] = [];
  private rowDirty = new Uint8Array(0);
  private dirty = DIRTY_FALSE;
  // Whether a write or resize has landed since the render state was synced.
  private stale = true;

  private responses: string[] = [];

  // Kept so a decoded handle comes up configured like the one it replaces.
  private readonly config: GhosttyTerminalConfig;
  private writePtyFn = 0;
  private history: SnapshotHistory | null = null;

  // A DataView is far more expensive to allocate than any wasm call it wraps,
  // so it is cached and only rebuilt when memory growth detaches the buffer.
  private view: DataView;
  private viewBuffer: ArrayBuffer;

  private readonly decoder = new TextDecoder();
  private readonly encoder = new TextEncoder();

  private freed = false;

  constructor(exports: GhosttyExports, cols = 80, rows = 24, config: GhosttyTerminalConfig = {}) {
    this.e = exports;
    this._cols = cols;
    this._rows = rows;
    this.viewBuffer = exports.memory.buffer;
    this.view = new DataView(this.viewBuffer);

    const out = exports.ghostty_wasm_alloc_opaque();
    if (exports.ghostty_terminal_new(0, out, cols, rows) !== GHOSTTY_SUCCESS) {
      exports.ghostty_wasm_free_opaque(out);
      throw new Error('ghostty_terminal_new failed');
    }
    this.handle = this.dv().getUint32(out, true);
    exports.ghostty_wasm_free_opaque(out);

    this.pScratch = exports.ghostty_wasm_alloc_u8_array(SCRATCH_CAP);
    this.pPoint = exports.ghostty_wasm_alloc_u8_array(POINT_SIZE);
    this.pRef = exports.ghostty_wasm_alloc_u8_array(GRID_REF_SIZE);
    this.pStyle = exports.ghostty_wasm_alloc_u8_array(STYLE_SIZE);
    this.pColors = exports.ghostty_wasm_alloc_u8_array(COLORS_SIZE);
    this.pGraphemes = exports.ghostty_wasm_alloc_u8_array(GRAPHEME_CAP * 4);
    this.pUri = exports.ghostty_wasm_alloc_u8_array(HYPERLINK_URI_CAP);

    this.config = config;
    this.applyConfig(config);

    // Query responses (DSR, DA, DECRQM) come back through this callback rather
    // than a poll, so hasResponse/readResponse drain a JS queue. The table entry
    // is never reclaimed, so it is installed once and re-pointed at whatever
    // handle this object currently owns.
    this.writePtyFn = installCallback(exports.__indirect_function_table, (_t, _u, ptr, len) => {
      if (len <= 0) return;
      this.responses.push(this.decoder.decode(new Uint8Array(this.e.memory.buffer, ptr, len)));
    });
    exports.ghostty_terminal_set(this.handle, TERMINAL_OPT_WRITE_PTY, this.writePtyFn);

    const stateOut = exports.ghostty_wasm_alloc_opaque();
    exports.ghostty_render_state_new(0, stateOut);
    this.state = this.dv().getUint32(stateOut, true);
    exports.ghostty_render_state_row_iterator_new(0, stateOut);
    this.iterator = this.dv().getUint32(stateOut, true);
    exports.ghostty_render_state_row_cells_new(0, stateOut);
    this.cells = this.dv().getUint32(stateOut, true);
    exports.ghostty_wasm_free_opaque(stateOut);

    this.resizePool();
  }

  get cols(): number { return this._cols; }
  get rows(): number { return this._rows; }

  /**
   * Point the row iterator at the current render state and return it.
   *
   * ROW_ITERATOR populates a pre-allocated iterator rather than handing one
   * back, so the handle has to be in the out slot before the call — passing an
   * empty slot silently iterates nothing useful.
   */
  private rowIterator(): number {
    this.dv().setUint32(this.pScratch, this.iterator, true);
    this.e.ghostty_render_state_get(this.state, RENDER_DATA_ROW_ITERATOR, this.pScratch);
    return this.iterator;
  }

  /** Same contract as rowIterator(), for the current row's cells. */
  private rowCells(it: number): number {
    this.dv().setUint32(this.pScratch, this.cells, true);
    this.e.ghostty_render_state_row_get(it, ROW_DATA_CELLS, this.pScratch);
    return this.cells;
  }

  private dv(): DataView {
    if (this.viewBuffer !== this.e.memory.buffer) {
      this.viewBuffer = this.e.memory.buffer;
      this.view = new DataView(this.viewBuffer);
    }
    return this.view;
  }

  private applyConfig(config: GhosttyTerminalConfig): void {
    const { scrollbackLimit, fgColor, bgColor, cursorColor, palette } = config;
    if (scrollbackLimit !== undefined) {
      this.dv().setBigUint64(this.pScratch, BigInt(scrollbackLimit), true);
      this.e.ghostty_terminal_set(this.handle, TERMINAL_OPT_SCROLLBACK_MAX_BYTES, this.pScratch);
    }
    const setColor = (option: number, value: number) => {
      const bytes = new Uint8Array(this.e.memory.buffer, this.pScratch, 3);
      bytes[0] = (value >> 16) & 0xff;
      bytes[1] = (value >> 8) & 0xff;
      bytes[2] = value & 0xff;
      this.e.ghostty_terminal_set(this.handle, option, this.pScratch);
    };
    if (fgColor !== undefined) setColor(TERMINAL_OPT_COLOR_FOREGROUND, fgColor);
    if (bgColor !== undefined) setColor(TERMINAL_OPT_COLOR_BACKGROUND, bgColor);
    if (cursorColor !== undefined) setColor(TERMINAL_OPT_COLOR_CURSOR, cursorColor);
    if (palette && palette.length > 0) {
      const bytes = new Uint8Array(this.e.memory.buffer, this.pScratch, 768);
      for (let i = 0; i < 256; i += 1) {
        const value = palette[i] ?? 0;
        bytes[i * 3] = (value >> 16) & 0xff;
        bytes[i * 3 + 1] = (value >> 8) & 0xff;
        bytes[i * 3 + 2] = value & 0xff;
      }
      this.e.ghostty_terminal_set(this.handle, TERMINAL_OPT_COLOR_PALETTE, this.pScratch);
    }
  }

  private resizePool(): void {
    const wanted = this._cols * this._rows;
    while (this.cellPool.length < wanted) this.cellPool.push(newCell());
    if (this.cellPool.length > wanted) this.cellPool.length = wanted;
    if (this.rowDirty.length !== this._rows) this.rowDirty = new Uint8Array(this._rows);
  }

  write(data: string | Uint8Array): void {
    const bytes = typeof data === 'string' ? this.encoder.encode(data) : data;
    if (bytes.length === 0) return;
    if (this.writeBufLen < bytes.length) {
      if (this.writeBufPtr) this.e.ghostty_wasm_free_u8_array(this.writeBufPtr, this.writeBufLen);
      this.writeBufLen = Math.max(bytes.length, 4096);
      this.writeBufPtr = this.e.ghostty_wasm_alloc_u8_array(this.writeBufLen);
    }
    new Uint8Array(this.e.memory.buffer, this.writeBufPtr, bytes.length).set(bytes);
    this.e.ghostty_terminal_vt_write(this.handle, this.writeBufPtr, bytes.length);
    this.stale = true;
  }

  resize(cols: number, rows: number): void {
    if (cols === this._cols && rows === this._rows) return;
    if (this.e.ghostty_terminal_resize(this.handle, cols, rows) !== GHOSTTY_SUCCESS) return;
    this._cols = cols;
    this._rows = rows;
    this.resizePool();
    this.stale = true;
  }

  /**
   * Replace this terminal's state with the one a snapshot holds.
   *
   * Only the libghostty handle is swapped. The render state, the scratch
   * buffers, the cell pool, and every reference a renderer holds survive, so a
   * restore does not rebuild the pane around it.
   *
   * The renderable prefix lands before this returns; the returned history is
   * the part that scales with scrollback and belongs after the first paint.
   * Finish or close it — until then the decoder borrows both the snapshot bytes
   * and this terminal.
   */
  adoptSnapshot(snapshot: Uint8Array): SnapshotHistory {
    const e = this.e;
    this.history?.close();

    const src = e.ghostty_wasm_alloc_u8_array(snapshot.length);
    new Uint8Array(e.memory.buffer, src, snapshot.length).set(snapshot);
    const out = e.ghostty_wasm_alloc_opaque();
    const releaseSource = () => {
      e.ghostty_wasm_free_opaque(out);
      e.ghostty_wasm_free_u8_array(src, snapshot.length);
    };

    if (e.ghostty_snapshot_decoder_new_buf(0, out, src, snapshot.length) !== GHOSTTY_SUCCESS) {
      releaseSource();
      throw new Error('ghostty_snapshot_decoder_new_buf failed');
    }
    const decoder = this.dv().getUint32(out, true);
    if (e.ghostty_snapshot_decoder_ready(decoder, out) !== GHOSTTY_SUCCESS) {
      e.ghostty_snapshot_decoder_free(decoder);
      releaseSource();
      throw new Error('ghostty_snapshot_decoder_ready failed');
    }
    const handle = this.dv().getUint32(out, true);

    e.ghostty_terminal_free(this.handle);
    this.handle = handle;
    e.ghostty_terminal_set(handle, TERMINAL_OPT_WRITE_PTY, this.writePtyFn);
    this.applyConfig(this.config);
    // Anything the old handle queued answered a query nobody is waiting on any
    // more, and a decode of its own puts nothing on the pty.
    this.responses.length = 0;

    e.ghostty_terminal_get(handle, TERMINAL_DATA_COLS, this.pScratch);
    this._cols = this.dv().getUint16(this.pScratch, true);
    e.ghostty_terminal_get(handle, TERMINAL_DATA_ROWS, this.pScratch);
    this._rows = this.dv().getUint16(this.pScratch, true);
    this.resizePool();
    this.stale = true;

    e.ghostty_snapshot_decoder_get(decoder, SNAPSHOT_DATA_HISTORY_ROWS_PRIMARY, this.pScratch);
    const rows = Number(this.dv().getBigUint64(this.pScratch, true));

    let done = false;
    const history: SnapshotHistory = {
      rows,
      next: () => {
        if (done) return null;
        const rc = e.ghostty_snapshot_decoder_next(decoder);
        if (rc !== GHOSTTY_SUCCESS) {
          history.close();
          if (rc !== GHOSTTY_NO_VALUE) throw new Error(`ghostty_snapshot_decoder_next failed: ${rc}`);
          return null;
        }
        e.ghostty_snapshot_decoder_get(decoder, SNAPSHOT_DATA_PROGRESS_ROWS, this.pScratch);
        const prepended = this.dv().getUint32(this.pScratch, true);
        if (prepended > 0) this.stale = true;
        return prepended;
      },
      close: () => {
        if (done) return;
        done = true;
        if (this.history === history) this.history = null;
        e.ghostty_snapshot_decoder_free(decoder);
        releaseSource();
      },
    };
    this.history = history;
    return history;
  }

  free(): void {
    if (this.freed) return;
    this.freed = true;
    this.history?.close();
    this.e.ghostty_render_state_row_cells_free(this.cells);
    this.e.ghostty_render_state_row_iterator_free(this.iterator);
    this.e.ghostty_render_state_free(this.state);
    this.e.ghostty_terminal_free(this.handle);
    this.handle = 0;
    this.state = 0;
    this.iterator = 0;
    this.cells = 0;
    if (this.writeBufPtr) this.e.ghostty_wasm_free_u8_array(this.writeBufPtr, this.writeBufLen);
    this.e.ghostty_wasm_free_u8_array(this.pScratch, SCRATCH_CAP);
    this.e.ghostty_wasm_free_u8_array(this.pPoint, POINT_SIZE);
    this.e.ghostty_wasm_free_u8_array(this.pRef, GRID_REF_SIZE);
    this.e.ghostty_wasm_free_u8_array(this.pStyle, STYLE_SIZE);
    this.e.ghostty_wasm_free_u8_array(this.pColors, COLORS_SIZE);
    this.e.ghostty_wasm_free_u8_array(this.pGraphemes, GRAPHEME_CAP * 4);
    this.e.ghostty_wasm_free_u8_array(this.pUri, HYPERLINK_URI_CAP);
    this.cellPool.length = 0;
  }

  /**
   * Bring the render state up to date with the terminal, if a write or resize
   * has landed since the last time.
   *
   * Every render-state read goes through this. The alternative — making the
   * caller sync — reads the cursor and the viewport as of the previous frame,
   * which is how command blocks came to record stale rows: the app reads the
   * cursor immediately after writing the bytes that moved it.
   *
   * Syncing mid-frame is safe for the renderer: the dirty state lives in the
   * terminal until markClean() clears it, so a read between frames re-reports
   * the same dirt rather than taking a repaint the renderer never made.
   */
  private sync(): void {
    if (!this.stale) return;
    this.stale = false;
    this.e.ghostty_render_state_update(this.state, this.handle);
    this.e.ghostty_render_state_get(this.state, RENDER_DATA_DIRTY, this.pScratch);
    this.dirty = this.dv().getUint32(this.pScratch, true);

    this.rowDirty.fill(this.dirty === DIRTY_FULL ? 1 : 0);
    if (this.dirty !== DIRTY_FULL) {
      const it = this.rowIterator();
      for (let y = 0; y < this._rows && this.e.ghostty_render_state_row_iterator_next(it); y += 1) {
        this.e.ghostty_render_state_row_get(it, ROW_DATA_DIRTY, this.pScratch);
        this.rowDirty[y] = this.dv().getUint8(this.pScratch);
      }
    }
  }

  /**
   * Sync and report everything that changed since the last markClean().
   */
  update(): number {
    this.sync();
    return this.dirty;
  }

  isRowDirty(y: number): boolean {
    this.sync();
    return y >= 0 && y < this.rowDirty.length && this.rowDirty[y] !== 0;
  }

  markClean(): void {
    this.dv().setUint32(this.pScratch, DIRTY_FALSE, true);
    this.e.ghostty_render_state_set(this.state, RENDER_OPT_DIRTY, this.pScratch);
    const it = this.rowIterator();
    const flag = this.pScratch + 8;
    this.dv().setUint8(flag, 0);
    while (this.e.ghostty_render_state_row_iterator_next(it)) {
      this.e.ghostty_render_state_row_set(it, ROW_OPT_DIRTY, flag);
    }
    this.rowDirty.fill(0);
    this.dirty = DIRTY_FALSE;
  }

  /**
   * Every viewport cell, row-major. The array is a reused pool: consume it
   * before the next getViewport() call.
   */
  getViewport(): GhosttyCell[] {
    this.sync();
    const pool = this.cellPool;
    const it = this.rowIterator();
    const defaults = this.defaultColors();
    let base = 0;
    let y = 0;
    while (y < this._rows && this.e.ghostty_render_state_row_iterator_next(it)) {
      this.readRow(it, pool, base, defaults);
      base += this._cols;
      y += 1;
    }
    // Rows the iterator did not reach (a shrunk viewport mid-frame) must not
    // show the previous frame's content.
    for (; base < pool.length; base += 1) this.blank(pool[base], defaults);
    return pool;
  }

  /**
   * A copy of one active-area row. Unlike getViewport() this syncs the render
   * state first and hands back cells the caller owns, for readers outside a
   * render loop that just want a row's content.
   */
  getLine(y: number): GhosttyCell[] | null {
    if (y < 0 || y >= this._rows) return null;
    const start = y * this._cols;
    return this.getViewport().slice(start, start + this._cols).map((cell) => ({ ...cell }));
  }

  private blank(cell: GhosttyCell, defaults: { fg: RGB; bg: RGB }): void {
    cell.codepoint = 0;
    cell.flags = 0;
    cell.width = 1;
    cell.hyperlink_id = 0;
    cell.grapheme_len = 0;
    cell.fg_r = defaults.fg.r; cell.fg_g = defaults.fg.g; cell.fg_b = defaults.fg.b;
    cell.bg_r = defaults.bg.r; cell.bg_g = defaults.bg.g; cell.bg_b = defaults.bg.b;
  }

  private readRow(it: number, pool: GhosttyCell[], base: number, defaults: { fg: RGB; bg: RGB }): void {
    const e = this.e;

    e.ghostty_render_state_row_get(it, ROW_DATA_CELLS_RAW, this.pScratch);
    const ptr = this.dv().getUint32(this.pScratch, true);
    const len = Math.min(this.dv().getUint32(this.pScratch + 4, true), this._cols);
    const cells = this.rowCells(it);

    // Row-level flags let whole rows skip the per-cell style and grapheme
    // queries, which is the common case for terminal output.
    e.ghostty_render_state_row_get(it, ROW_DATA_RAW, this.pScratch);
    const rawRow = this.dv().getBigUint64(this.pScratch, true);
    const hasGraphemes = this.rowFlag(rawRow, ROW_RAW_DATA_GRAPHEME);

    const raw = new BigUint64Array(this.e.memory.buffer, ptr, len);
    for (let x = 0; x < len; x += 1) {
      const cell = pool[base + x];
      const value = raw[x];
      this.blank(cell, defaults);

      e.ghostty_cell_get(value, CELL_DATA_CODEPOINT, this.pScratch);
      cell.codepoint = this.dv().getUint32(this.pScratch, true);
      e.ghostty_cell_get(value, CELL_DATA_WIDE, this.pScratch);
      cell.width = CELL_WIDE_WIDTH[this.dv().getUint32(this.pScratch, true)] ?? 1;
      e.ghostty_cell_get(value, CELL_DATA_HAS_HYPERLINK, this.pScratch);
      cell.hyperlink_id = this.dv().getUint8(this.pScratch) ? 1 : 0;
      e.ghostty_cell_get(value, CELL_DATA_HAS_STYLING, this.pScratch);
      const styled = this.dv().getUint8(this.pScratch) !== 0;

      e.ghostty_render_state_row_cells_select(cells, x);
      if (styled) {
        this.dv().setUint32(this.pStyle, STYLE_SIZE, true);
        if (e.ghostty_render_state_row_cells_get(cells, CELLS_DATA_STYLE, this.pStyle) === GHOSTTY_SUCCESS) {
          cell.flags = this.styleFlags(this.pStyle);
        }
      }
      if (e.ghostty_render_state_row_cells_get(cells, CELLS_DATA_FG_COLOR, this.pScratch) === GHOSTTY_SUCCESS) {
        const rgb = new Uint8Array(this.e.memory.buffer, this.pScratch, 3);
        cell.fg_r = rgb[0]; cell.fg_g = rgb[1]; cell.fg_b = rgb[2];
      }
      if (e.ghostty_render_state_row_cells_get(cells, CELLS_DATA_BG_COLOR, this.pScratch) === GHOSTTY_SUCCESS) {
        const rgb = new Uint8Array(this.e.memory.buffer, this.pScratch, 3);
        cell.bg_r = rgb[0]; cell.bg_g = rgb[1]; cell.bg_b = rgb[2];
      }
      if (hasGraphemes) {
        e.ghostty_render_state_row_cells_get(cells, CELLS_DATA_GRAPHEMES_LEN, this.pScratch);
        cell.grapheme_len = Math.max(0, this.dv().getUint32(this.pScratch, true) - 1);
      }
    }
    for (let x = len; x < this._cols; x += 1) this.blank(pool[base + x], defaults);
  }

  private rowFlag(row: bigint, data: number): boolean {
    this.e.ghostty_row_get(row, data, this.pScratch);
    return this.dv().getUint8(this.pScratch) !== 0;
  }

  private styleFlags(ptr: number): number {
    const s = new Uint8Array(this.e.memory.buffer, ptr, STYLE_SIZE);
    return (s[STYLE_OFF_BOLD] ? CellFlags.BOLD : 0)
      | (s[STYLE_OFF_ITALIC] ? CellFlags.ITALIC : 0)
      | (s[STYLE_OFF_FAINT] ? CellFlags.FAINT : 0)
      | (s[STYLE_OFF_BLINK] ? CellFlags.BLINK : 0)
      | (s[STYLE_OFF_INVERSE] ? CellFlags.INVERSE : 0)
      | (s[STYLE_OFF_INVISIBLE] ? CellFlags.INVISIBLE : 0)
      | (s[STYLE_OFF_STRIKETHROUGH] ? CellFlags.STRIKETHROUGH : 0)
      | (this.dv().getInt32(ptr + STYLE_OFF_UNDERLINE, true) !== 0 ? CellFlags.UNDERLINE : 0);
  }

  private defaultColors(): { fg: RGB; bg: RGB } {
    this.e.ghostty_terminal_get(this.handle, TERMINAL_DATA_COLOR_FOREGROUND, this.pScratch);
    const fgBytes = new Uint8Array(this.e.memory.buffer, this.pScratch, 3);
    const fg = { r: fgBytes[0], g: fgBytes[1], b: fgBytes[2] };
    this.e.ghostty_terminal_get(this.handle, TERMINAL_DATA_COLOR_BACKGROUND, this.pScratch);
    const bgBytes = new Uint8Array(this.e.memory.buffer, this.pScratch, 3);
    return { fg, bg: { r: bgBytes[0], g: bgBytes[1], b: bgBytes[2] } };
  }

  getColors(): RenderStateColors {
    this.sync();
    this.dv().setUint32(this.pColors, COLORS_SIZE, true);
    this.e.ghostty_render_state_colors_get(this.state, this.pColors);
    const b = new Uint8Array(this.e.memory.buffer, this.pColors, COLORS_SIZE);
    return {
      background: { r: b[COLORS_OFF_BACKGROUND], g: b[COLORS_OFF_BACKGROUND + 1], b: b[COLORS_OFF_BACKGROUND + 2] },
      foreground: { r: b[COLORS_OFF_FOREGROUND], g: b[COLORS_OFF_FOREGROUND + 1], b: b[COLORS_OFF_FOREGROUND + 2] },
      cursor: b[COLORS_OFF_CURSOR_HAS_VALUE]
        ? { r: b[COLORS_OFF_CURSOR], g: b[COLORS_OFF_CURSOR + 1], b: b[COLORS_OFF_CURSOR + 2] }
        : null,
    };
  }

  getCursor(): RenderStateCursor {
    this.sync();
    const read = (data: number): number => {
      this.e.ghostty_render_state_get(this.state, data, this.pScratch);
      return this.dv().getUint16(this.pScratch, true);
    };
    const readBool = (data: number): boolean => {
      this.e.ghostty_render_state_get(this.state, data, this.pScratch);
      return this.dv().getUint8(this.pScratch) !== 0;
    };
    const visible = readBool(RENDER_DATA_CURSOR_VISIBLE) && readBool(RENDER_DATA_CURSOR_VIEWPORT_HAS_VALUE);
    const x = visible ? read(RENDER_DATA_CURSOR_VIEWPORT_X) : 0;
    const y = visible ? read(RENDER_DATA_CURSOR_VIEWPORT_Y) : 0;
    this.e.ghostty_render_state_get(this.state, RENDER_DATA_CURSOR_VISUAL_STYLE, this.pScratch);
    const visual = this.dv().getUint32(this.pScratch, true);
    return {
      x, y, viewportX: x, viewportY: y,
      visible,
      blinking: readBool(RENDER_DATA_CURSOR_BLINKING),
      style: visual === CURSOR_STYLE_BAR ? 'bar' : visual === CURSOR_STYLE_UNDERLINE ? 'underline' : 'block',
    };
  }

  isAlternateScreen(): boolean {
    this.e.ghostty_terminal_get(this.handle, TERMINAL_DATA_ACTIVE_SCREEN, this.pScratch);
    return this.dv().getUint32(this.pScratch, true) === SCREEN_TYPE_ALTERNATE;
  }

  hasMouseTracking(): boolean {
    this.e.ghostty_terminal_get(this.handle, TERMINAL_DATA_MOUSE_TRACKING, this.pScratch);
    return this.dv().getUint32(this.pScratch, true) !== MOUSE_TRACKING_NONE;
  }

  hasBracketedPaste(): boolean {
    return this.getMode(2004);
  }

  getMode(mode: number, isAnsi = false): boolean {
    const dv = this.dv();
    dv.setUint16(this.pScratch, packMode(mode, isAnsi), true);
    dv.setUint8(this.pScratch + 2, 0);
    if (this.e.ghostty_terminal_get(this.handle, TERMINAL_DATA_MODE, this.pScratch) !== GHOSTTY_SUCCESS) {
      return false;
    }
    return this.dv().getUint8(this.pScratch + 2) !== 0;
  }

  getScrollbackLength(): number {
    this.e.ghostty_terminal_get(this.handle, TERMINAL_DATA_SCROLLBACK_ROWS, this.pScratch);
    return this.dv().getUint32(this.pScratch, true);
  }

  hasResponse(): boolean {
    return this.responses.length > 0;
  }

  readResponse(): string | null {
    return this.responses.shift() ?? null;
  }

  /**
   * Point a grid reference at (x, y), returning false when the position does
   * not resolve. `tag` picks the coordinate space: active area or history.
   */
  private ref(tag: number, x: number, y: number): boolean {
    const dv = this.dv();
    dv.setUint32(this.pPoint + POINT_OFF_TAG, tag, true);
    dv.setUint16(this.pPoint + POINT_OFF_X, x, true);
    dv.setUint32(this.pPoint + POINT_OFF_Y, y, true);
    dv.setUint32(this.pRef, GRID_REF_SIZE, true);
    return this.e.ghostty_terminal_grid_ref(this.handle, this.pPoint, this.pRef) === GHOSTTY_SUCCESS;
  }

  /** Cells of a scrollback row, oldest row at offset 0. */
  getScrollbackLine(offset: number): GhosttyCell[] | null {
    if (offset < 0 || offset >= this.getScrollbackLength()) return null;
    const defaults = this.defaultColors();
    const line: GhosttyCell[] = new Array(this._cols);
    for (let x = 0; x < this._cols; x += 1) {
      const cell = newCell();
      this.blank(cell, defaults);
      line[x] = cell;
      if (!this.ref(POINT_TAG_HISTORY, x, offset)) continue;
      this.e.ghostty_grid_ref_cell(this.pRef, this.pScratch);
      const value = this.dv().getBigUint64(this.pScratch, true);
      this.e.ghostty_cell_get(value, CELL_DATA_CODEPOINT, this.pScratch);
      cell.codepoint = this.dv().getUint32(this.pScratch, true);
      this.e.ghostty_cell_get(value, CELL_DATA_WIDE, this.pScratch);
      cell.width = CELL_WIDE_WIDTH[this.dv().getUint32(this.pScratch, true)] ?? 1;
      this.e.ghostty_cell_get(value, CELL_DATA_HAS_HYPERLINK, this.pScratch);
      cell.hyperlink_id = this.dv().getUint8(this.pScratch) ? 1 : 0;
      this.e.ghostty_cell_get(value, CELL_DATA_HAS_STYLING, this.pScratch);
      if (this.dv().getUint8(this.pScratch)) {
        this.dv().setUint32(this.pStyle, STYLE_SIZE, true);
        if (this.e.ghostty_grid_ref_style(this.pRef, this.pStyle) === GHOSTTY_SUCCESS) {
          cell.flags = this.styleFlags(this.pStyle);
        }
      }
      const graphemes = this.graphemeCodepoints(this.pRef);
      cell.grapheme_len = Math.max(0, graphemes - 1);
    }
    return line;
  }

  /**
   * Whether an active-area row soft-wraps into the row below it.
   *
   * This is ghostty's own direction, and the opposite of the question callers
   * usually ask ("does this row continue the one above?") — hence the name.
   */
  rowWrapsIntoNext(row: number): boolean {
    if (!this.ref(POINT_TAG_ACTIVE, 0, row)) return false;
    if (this.e.ghostty_grid_ref_row(this.pRef, this.pScratch) !== GHOSTTY_SUCCESS) return false;
    return this.rowFlag(this.dv().getBigUint64(this.pScratch, true), ROW_RAW_DATA_WRAP);
  }

  private graphemeCodepoints(ref: number): number {
    if (this.e.ghostty_grid_ref_graphemes(ref, this.pGraphemes, GRAPHEME_CAP, this.pScratch) !== GHOSTTY_SUCCESS) {
      return 0;
    }
    return Math.min(this.dv().getUint32(this.pScratch, true), GRAPHEME_CAP);
  }

  private graphemeStringFromRef(): string {
    const count = this.graphemeCodepoints(this.pRef);
    if (count === 0) return ' ';
    const points = new Uint32Array(this.e.memory.buffer, this.pGraphemes, count);
    return String.fromCodePoint(...points);
  }

  /** The full grapheme cluster at an active-area cell, or a space if empty. */
  getGraphemeString(row: number, col: number): string {
    if (!this.ref(POINT_TAG_ACTIVE, col, row)) return ' ';
    return this.graphemeStringFromRef();
  }

  /** The full grapheme cluster at a scrollback cell, or a space if empty. */
  getScrollbackGraphemeString(offset: number, col: number): string {
    if (!this.ref(POINT_TAG_HISTORY, col, offset)) return ' ';
    return this.graphemeStringFromRef();
  }

  private hyperlinkUriFromRef(): string | null {
    if (this.e.ghostty_grid_ref_hyperlink_uri(this.pRef, this.pUri, HYPERLINK_URI_CAP, this.pScratch) !== GHOSTTY_SUCCESS) {
      return null;
    }
    const len = this.dv().getUint32(this.pScratch, true);
    if (len === 0) return null;
    return this.decoder.decode(new Uint8Array(this.e.memory.buffer, this.pUri, len));
  }

  /** OSC 8 URI of an active-area cell, or null when it carries no hyperlink. */
  getHyperlinkUri(row: number, col: number): string | null {
    if (!this.ref(POINT_TAG_ACTIVE, col, row)) return null;
    return this.hyperlinkUriFromRef();
  }

  /** OSC 8 URI of a scrollback cell, oldest row at offset 0. */
  getScrollbackHyperlinkUri(offset: number, col: number): string | null {
    if (!this.ref(POINT_TAG_HISTORY, col, offset)) return null;
    return this.hyperlinkUriFromRef();
  }
}
