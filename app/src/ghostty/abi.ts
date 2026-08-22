// libghostty-vt's C ABI as the browser sees it: the exports we call, the enum
// values we pass, and the struct offsets we read. Everything here is derived
// from include/ghostty/vt/*.h at the commit in ghostty-vt.pin.
//
// Struct offsets are wasm32 (size_t = 4 bytes) and are asserted at runtime by
// abi.layout.test.ts against the real module, so a layout change upstream fails
// a test rather than silently misreading memory.

/** GhosttyResult. Negative values are errors. */
export const GHOSTTY_SUCCESS = 0;
/** Not an error: the asked-for value does not exist in the current state. */
export const GHOSTTY_NO_VALUE = -4;

/** ghostty_terminal_set options. */
export const TERMINAL_OPT_WRITE_PTY = 1;
export const TERMINAL_OPT_COLOR_FOREGROUND = 11;
export const TERMINAL_OPT_COLOR_BACKGROUND = 12;
export const TERMINAL_OPT_COLOR_CURSOR = 13;
export const TERMINAL_OPT_COLOR_PALETTE = 14;
export const TERMINAL_OPT_SCROLLBACK_MAX_BYTES = 27;

/** ghostty_terminal_get data kinds. */
export const TERMINAL_DATA_COLS = 1;
export const TERMINAL_DATA_ROWS = 2;
export const TERMINAL_DATA_ACTIVE_SCREEN = 6;
export const TERMINAL_DATA_MOUSE_TRACKING = 11;
export const TERMINAL_DATA_SCROLLBACK_ROWS = 15;
export const TERMINAL_DATA_COLOR_FOREGROUND = 18;
export const TERMINAL_DATA_COLOR_BACKGROUND = 19;
export const TERMINAL_DATA_COLOR_PALETTE_DEFAULT = 25;
export const TERMINAL_DATA_MODE = 37;

/** ghostty_snapshot_decoder_get data kinds. */
export const SNAPSHOT_DATA_HISTORY_ROWS_PRIMARY = 3;
export const SNAPSHOT_DATA_PROGRESS_ROWS = 6;
export const SNAPSHOT_DATA_PROGRESS_REMAINING = 7;

/** GhosttyScreenType for TERMINAL_DATA_ACTIVE_SCREEN. */
export const SCREEN_TYPE_ALTERNATE = 1;

/** GhosttyMouseTrackingMode for TERMINAL_DATA_MOUSE_TRACKING. */
export const MOUSE_TRACKING_NONE = 0;

/** ghostty_render_state_get data kinds. */
export const RENDER_DATA_DIRTY = 3;
export const RENDER_DATA_ROW_ITERATOR = 4;
export const RENDER_DATA_CURSOR_VISUAL_STYLE = 10;
export const RENDER_DATA_CURSOR_VISIBLE = 11;
export const RENDER_DATA_CURSOR_BLINKING = 12;
export const RENDER_DATA_CURSOR_VIEWPORT_HAS_VALUE = 14;
export const RENDER_DATA_CURSOR_VIEWPORT_X = 15;
export const RENDER_DATA_CURSOR_VIEWPORT_Y = 16;
export const RENDER_DATA_COLORS = 19;

/** ghostty_render_state_set options. */
export const RENDER_OPT_DIRTY = 0;

/** GhosttyRenderStateDirty, and the DirtyState the app's renderers switch on. */
export const DIRTY_FALSE = 0;
export const DIRTY_PARTIAL = 1;
export const DIRTY_FULL = 2;

/** GhosttyRenderStateCursorVisualStyle. */
export const CURSOR_STYLE_BAR = 0;
export const CURSOR_STYLE_BLOCK = 1;
export const CURSOR_STYLE_UNDERLINE = 2;
export const CURSOR_STYLE_BLOCK_HOLLOW = 3;

/** ghostty_render_state_row_get data kinds. */
export const ROW_DATA_DIRTY = 1;
export const ROW_DATA_RAW = 2;
export const ROW_DATA_CELLS = 3;
export const ROW_DATA_CELLS_RAW = 5;

/** ghostty_render_state_row_set options. */
export const ROW_OPT_DIRTY = 0;

/** ghostty_render_state_row_cells_get data kinds. */
export const CELLS_DATA_STYLE = 2;
export const CELLS_DATA_GRAPHEMES_LEN = 3;
export const CELLS_DATA_BG_COLOR = 5;
export const CELLS_DATA_FG_COLOR = 6;

/** ghostty_cell_get data kinds. */
export const CELL_DATA_CODEPOINT = 1;
export const CELL_DATA_WIDE = 3;
export const CELL_DATA_HAS_STYLING = 5;
export const CELL_DATA_HAS_HYPERLINK = 7;

/** GhosttyCellWide → the `width` a renderer lays out with. */
export const CELL_WIDE_WIDTH = [1, 2, 0, 0] as const;

/** ghostty_row_get data kinds (a raw GhosttyRow, not the render-state row). */
export const ROW_RAW_DATA_WRAP = 1;
export const ROW_RAW_DATA_GRAPHEME = 3;

/** GhosttyPointTag. */
export const POINT_TAG_ACTIVE = 0;
export const POINT_TAG_HISTORY = 3;

/**
 * GhosttyPoint (16 bytes): tag at 0, then the coordinate union at 8 — u16 x,
 * u32 y. The union carries a u64 for ABI padding, which is what pushes the
 * coordinate to offset 8.
 */
export const POINT_SIZE = 16;
export const POINT_OFF_TAG = 0;
export const POINT_OFF_X = 8;
export const POINT_OFF_Y = 12;

/** GhosttyGridRef (12 bytes): sized struct, then an opaque node and x/y. */
export const GRID_REF_SIZE = 12;

/**
 * GhosttyStyle (72 bytes). Two self-describing color slots (fg, bg) sit ahead
 * of the attribute booleans: a u32 kind (default / palette index / direct RGB)
 * followed by a u32 value. The render-state path resolves colors for us via
 * CELLS_DATA_FG_COLOR / BG_COLOR; grid refs (scrollback) read the slots.
 */
export const STYLE_SIZE = 72;
export const STYLE_OFF_FG_KIND = 8;
export const STYLE_OFF_FG = 16;
export const STYLE_OFF_BG_KIND = 24;
export const STYLE_OFF_BG = 32;
export const STYLE_COLOR_DEFAULT = 0;
export const STYLE_COLOR_PALETTE = 1;
export const STYLE_COLOR_RGB = 2;
export const STYLE_OFF_BOLD = 56;
export const STYLE_OFF_ITALIC = 57;
export const STYLE_OFF_FAINT = 58;
export const STYLE_OFF_BLINK = 59;
export const STYLE_OFF_INVERSE = 60;
export const STYLE_OFF_INVISIBLE = 61;
export const STYLE_OFF_STRIKETHROUGH = 62;
export const STYLE_OFF_OVERLINE = 63;
export const STYLE_OFF_UNDERLINE = 64;

/**
 * GhosttyRenderStateColors: size, bg, fg, cursor, cursor_has_value,
 * palette[256]. 784, not the 782 those fields add up to: the struct is padded
 * to its alignment, and the size field must carry sizeof, not the content.
 */
export const COLORS_SIZE = 784;
export const COLORS_OFF_BACKGROUND = 4;
export const COLORS_OFF_FOREGROUND = 7;
export const COLORS_OFF_CURSOR = 10;
export const COLORS_OFF_CURSOR_HAS_VALUE = 13;
export const COLORS_OFF_PALETTE = 14;

/**
 * GhosttyMode packs the DEC/ANSI distinction into the high bit: ANSI modes set
 * it, DEC private modes (the ones a terminal app actually asks about) clear it.
 */
export function packMode(value: number, ansi: boolean): number {
  return (value & 0x7fff) | (ansi ? 0x8000 : 0);
}

export interface GhosttyExports extends WebAssembly.Exports {
  memory: WebAssembly.Memory;
  __indirect_function_table: WebAssembly.Table;

  ghostty_wasm_alloc_opaque(): number;
  ghostty_wasm_free_opaque(ptr: number): void;
  ghostty_wasm_alloc(len: number): number;
  ghostty_wasm_free(ptr: number, len: number): void;

  ghostty_terminal_new(allocator: number, out: number, cols: number, rows: number): number;
  ghostty_terminal_free(terminal: number): void;
  ghostty_terminal_resize(terminal: number, cols: number, rows: number): number;
  ghostty_terminal_set(terminal: number, option: number, value: number): number;
  ghostty_terminal_get(terminal: number, data: number, out: number): number;
  ghostty_terminal_vt_write(terminal: number, data: number, len: number): void;
  ghostty_terminal_grid_ref(terminal: number, point: number, outRef: number): number;

  ghostty_snapshot_decoder_new_buf(allocator: number, out: number, ptr: number, len: number): number;
  ghostty_snapshot_decoder_free(decoder: number): void;
  ghostty_snapshot_decoder_ready(decoder: number, outTerminal: number): number;
  ghostty_snapshot_decoder_next(decoder: number): number;
  ghostty_snapshot_decoder_get(decoder: number, data: number, out: number): number;

  ghostty_grid_ref_cell(ref: number, outCell: number): number;
  ghostty_grid_ref_row(ref: number, outRow: number): number;
  ghostty_grid_ref_style(ref: number, outStyle: number): number;
  ghostty_grid_ref_graphemes(ref: number, buf: number, bufLen: number, outLen: number): number;
  ghostty_grid_ref_hyperlink_uri(ref: number, buf: number, bufLen: number, outLen: number): number;

  ghostty_cell_get(cellLo: bigint, data: number, out: number): number;
  ghostty_row_get(row: bigint, data: number, out: number): number;

  ghostty_render_state_new(allocator: number, out: number): number;
  ghostty_render_state_free(state: number): void;
  ghostty_render_state_update(state: number, terminal: number): number;
  ghostty_render_state_get(state: number, data: number, out: number): number;
  ghostty_render_state_set(state: number, option: number, value: number): number;

  ghostty_render_state_row_iterator_new(allocator: number, out: number): number;
  ghostty_render_state_row_iterator_free(iterator: number): void;
  ghostty_render_state_row_iterator_next(iterator: number): number;
  ghostty_render_state_row_get(iterator: number, data: number, out: number): number;
  ghostty_render_state_row_set(iterator: number, option: number, value: number): number;

  ghostty_render_state_row_cells_new(allocator: number, out: number): number;
  ghostty_render_state_row_cells_free(cells: number): void;
  ghostty_render_state_row_cells_select(cells: number, x: number): number;
  ghostty_render_state_row_cells_get(cells: number, data: number, out: number): number;
}
