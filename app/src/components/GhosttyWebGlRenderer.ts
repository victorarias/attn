import { CellFlags, type GhosttyCell, type GhosttyTerminal } from 'ghostty-web';
import { cursorRowInViewport, viewportBufferStart } from '../utils/ghosttyScroll';
import {
  GLYPH_MODE_COLOR,
  GLYPH_MODE_TINT,
  TERMINAL_GLYPH_FRAGMENT_SHADER,
  TERMINAL_GLYPH_VERTEX_SHADER,
  isColorGlyphBitmap,
} from './terminalGlyphProgram';
import { terminalGlyphFont } from './terminalGlyphFont';
import {
  KITTY_FORMAT_BYTES_PER_PIXEL,
  type KittyPixelFormat,
} from '../utils/kittyImageFormat';
import {
  TERMINAL_FLOATS_PER_QUAD,
  TERMINAL_FLOATS_PER_VERTEX,
  TerminalVertexBuffer,
} from './terminalVertexBuffer';
import {
  PACKED_WHITE,
  type PackedRgb,
  type Rgb,
  packColor,
  packRgb,
  parseColor,
  parsePackedColor,
} from './terminalColor';

interface RendererTheme {
  background: string;
  foreground: string;
  cursor: string;
}

// A rectangular range overlay in viewport coordinates: rows include endRow,
// columns exclude endCol, and rows strictly between them span the full width.
// background fills under glyphs, underline sits on the baseline over them, and
// outline is a thin border around the bounding rectangle.
export interface WebGlOverlay {
  startRow: number;
  startCol: number;
  endRow: number;
  endCol: number;
  color: string;
  alpha?: number;
  kind: 'background' | 'underline' | 'outline';
}

interface OverlaySpan {
  startCol: number;
  endCol: number;
  rgb: PackedRgb;
  alpha: number;
  kind: 'background' | 'underline';
}

// Which horizontal edges of an outline are real boundaries: one outside [0, rows)
// only landed there because the region extends past the visible area, and drawing
// it clamped makes the outline look like a box around the terminal.
export function visibleOutlineEdges(
  startRow: number,
  endRow: number,
  rows: number,
): { drawTop: boolean; drawBottom: boolean } {
  return {
    drawTop: startRow >= 0 && startRow < rows,
    drawBottom: endRow >= 0 && endRow < rows,
  };
}

// One kitty image's pixels, keyed (imageId, generation): a retransmission mints a
// new generation, so a texture is never stale content under a live key.
export interface WebGlImageSource {
  imageId: number;
  generation: number;
  width: number;
  height: number;
  format: KittyPixelFormat;
  pixels: Uint8Array;
}

// One image to draw this frame, already clipped by the caller. Destination is CSS
// pixels relative to the grid's top-left; source is texels.
export interface WebGlImageQuad {
  source: WebGlImageSource;
  x: number;
  y: number;
  width: number;
  height: number;
  sourceX: number;
  sourceY: number;
  sourceWidth: number;
  sourceHeight: number;
  // Picks the pass this quad draws in: over the text at z >= 0, under it below
  // that, under the cell backgrounds too past KITTY_Z_UNDER_BACKGROUND.
  z: number;
}

// Kitty's deepest layer: below INT32_MIN/2 a placement draws under cells with
// non-default backgrounds. Below is strict — AT this value it draws over them.
export const KITTY_Z_UNDER_BACKGROUND = -1_073_741_824;

// Live GPU textures per renderer. Measured: a stored image is 1.9-6.5MB, so 16 is
// ~100MB of VRAM worst case — a tripwire. Textures die with the pane's GL context;
// the app-level blob cache is what survives.
export const IMAGE_TEXTURE_LIMIT = 16;

// Below this grid size, copying cached rows costs more than rebuilding the
// viewport. Measured: the crossover is below a 1,785-cell split pane, so the gate
// sits at 2,048; the 3,212-cell case is a measured win.
const ROW_VERTEX_CACHE_MIN_CELLS = 2_048;
// Ceiling on the row cache's MARGINAL cost. The frame buffer the rows concatenate
// into is staged either way, so folding it in would gate on memory the direct path
// spends anyway; both are reported (retainedRowVertexBytes, retainedStagingBytes).
// A grid that crosses the ceiling releases its rows and stays on the direct path.
const ROW_VERTEX_CACHE_MAX_BYTES = 2 * 1024 * 1024;

// Starting capacity per row, per cell pass. Every printable cell adds a foreground
// quad; backgrounds are content-dependent, so that pass starts minimal and grows.
const ROW_FG_QUADS_PER_CELL = 1;
const ROW_BG_QUADS_PER_CELL = 0;

// ghostty-web does not export DirtyState; PARTIAL is the only value the row cache
// can serve.
const DIRTY_NONE = 0;
const DIRTY_PARTIAL = 1;

interface AtlasGlyph {
  u0: number;
  v0: number;
  u1: number;
  v1: number;
  width: number;
  height: number;
  // True when the bitmap carries its own colors (a color font): drawn straight from
  // the atlas rather than tinted.
  colored: boolean;
}

interface BlockRect {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface WebGlRenderSample {
  cpuSubmitMs: number;
  cells: number;
  paintedCells: number;
  paintedRows: number;
  fullPaint: boolean;
  submittedQuads: number;
  retainedRowVertexBytes: number;
  // Rows plus the frame buffer they concatenate into — everything held between
  // paints, including the staging the direct path pays anyway.
  retainedStagingBytes: number;
  modelPrintable: number;
  quads: number;
  glyphUploads: number;
  // TEMP (blank-on-split): diagnostics for drawn quads falling below the printable-
  // cell count after a resize. Remove with the render-trace instrumentation.
  cellsArrayLen: number;
  printableSkippedNull: number;
  printableSkippedZeroWidth: number;
}

export function graphemeAtViewportCell(
  terminal: GhosttyTerminal,
  row: number,
  col: number,
  viewportOffset: number,
): string {
  const history = terminal.getScrollbackLength();
  const bufferRow = viewportBufferStart(history, viewportOffset) + row;
  return bufferRow < history
    ? terminal.getScrollbackGraphemeString(bufferRow, col)
    : terminal.getGraphemeString(bufferRow - history, col);
}

// The atlas is allocated eagerly and in full, a fixed per-renderer cost. Most panes
// fit their glyph set in 1024² (≈8 MB vs ≈32 MB at 2048²), so start there and grow
// only on overflow. See growAtlas/resetAtlas.
export const INITIAL_ATLAS_SIZE = 1024;
export const MAX_ATLAS_SIZE = 2048;
// position(2) + texcoord(2) + color(4) + mode(1). Mode selects the fragment path:
// 0 = tinted atlas coverage, 1 = color glyph (atlas RGBA passed through).

// Next atlas size: double, capped. Idempotent at the cap, so growth converges.
export function nextAtlasSize(current: number, max: number = MAX_ATLAS_SIZE): number {
  return Math.min(current * 2, max);
}
const BLOCK_ELEMENT_RECTS: Readonly<Record<number, readonly BlockRect[]>> = {
  0x2580: [{ x: 0, y: 0, width: 1, height: 1 / 2 }],
  0x2581: [{ x: 0, y: 7 / 8, width: 1, height: 1 / 8 }],
  0x2582: [{ x: 0, y: 6 / 8, width: 1, height: 2 / 8 }],
  0x2583: [{ x: 0, y: 5 / 8, width: 1, height: 3 / 8 }],
  0x2584: [{ x: 0, y: 4 / 8, width: 1, height: 4 / 8 }],
  0x2585: [{ x: 0, y: 3 / 8, width: 1, height: 5 / 8 }],
  0x2586: [{ x: 0, y: 2 / 8, width: 1, height: 6 / 8 }],
  0x2587: [{ x: 0, y: 1 / 8, width: 1, height: 7 / 8 }],
  0x2588: [{ x: 0, y: 0, width: 1, height: 1 }],
  0x2589: [{ x: 0, y: 0, width: 7 / 8, height: 1 }],
  0x258a: [{ x: 0, y: 0, width: 6 / 8, height: 1 }],
  0x258b: [{ x: 0, y: 0, width: 5 / 8, height: 1 }],
  0x258c: [{ x: 0, y: 0, width: 1 / 2, height: 1 }],
  0x258d: [{ x: 0, y: 0, width: 3 / 8, height: 1 }],
  0x258e: [{ x: 0, y: 0, width: 2 / 8, height: 1 }],
  0x258f: [{ x: 0, y: 0, width: 1 / 8, height: 1 }],
  0x2590: [{ x: 1 / 2, y: 0, width: 1 / 2, height: 1 }],
  0x2594: [{ x: 0, y: 0, width: 1, height: 1 / 8 }],
  0x2595: [{ x: 7 / 8, y: 0, width: 1 / 8, height: 1 }],
  0x2596: [{ x: 0, y: 1 / 2, width: 1 / 2, height: 1 / 2 }],
  0x2597: [{ x: 1 / 2, y: 1 / 2, width: 1 / 2, height: 1 / 2 }],
  0x2598: [{ x: 0, y: 0, width: 1 / 2, height: 1 / 2 }],
  0x2599: [
    { x: 0, y: 0, width: 1 / 2, height: 1 },
    { x: 1 / 2, y: 1 / 2, width: 1 / 2, height: 1 / 2 },
  ],
  0x259a: [
    { x: 0, y: 0, width: 1 / 2, height: 1 / 2 },
    { x: 1 / 2, y: 1 / 2, width: 1 / 2, height: 1 / 2 },
  ],
  0x259b: [
    { x: 0, y: 0, width: 1, height: 1 / 2 },
    { x: 0, y: 1 / 2, width: 1 / 2, height: 1 / 2 },
  ],
  0x259c: [
    { x: 0, y: 0, width: 1, height: 1 / 2 },
    { x: 1 / 2, y: 1 / 2, width: 1 / 2, height: 1 / 2 },
  ],
  0x259d: [{ x: 1 / 2, y: 0, width: 1 / 2, height: 1 / 2 }],
  0x259e: [
    { x: 1 / 2, y: 0, width: 1 / 2, height: 1 / 2 },
    { x: 0, y: 1 / 2, width: 1 / 2, height: 1 / 2 },
  ],
  0x259f: [
    { x: 1 / 2, y: 0, width: 1 / 2, height: 1 },
    { x: 0, y: 1 / 2, width: 1 / 2, height: 1 / 2 },
  ],
};

// The format and byte view one stored image uploads with. RGB/RGBA go up untouched;
// grayscale is widened on the CPU rather than using WebGL2's legacy LUMINANCE —
// ghostty only produces it from a grayscale PNG, a path no measured emitter takes.
function imageUpload(
  gl: WebGL2RenderingContext,
  source: WebGlImageSource,
): { format: number; pixels: Uint8Array } {
  if (source.format === 'rgba') return { format: gl.RGBA, pixels: source.pixels };
  if (source.format === 'rgb') return { format: gl.RGB, pixels: source.pixels };
  const stride = KITTY_FORMAT_BYTES_PER_PIXEL[source.format];
  const pixelCount = source.width * source.height;
  const rgba = new Uint8Array(pixelCount * 4);
  for (let i = 0; i < pixelCount; i += 1) {
    const gray = source.pixels[i * stride];
    rgba[i * 4] = gray;
    rgba[i * 4 + 1] = gray;
    rgba[i * 4 + 2] = gray;
    rgba[i * 4 + 3] = stride === 2 ? source.pixels[i * stride + 1] : 255;
  }
  return { format: gl.RGBA, pixels: rgba };
}

function compileShader(gl: WebGL2RenderingContext, type: number, source: string): WebGLShader {
  const shader = gl.createShader(type);
  if (!shader) {
    throw new Error('Unable to allocate WebGL shader');
  }
  gl.shaderSource(shader, source);
  gl.compileShader(shader);
  if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS)) {
    const message = gl.getShaderInfoLog(shader) ?? 'Unknown shader compile error';
    gl.deleteShader(shader);
    throw new Error(message);
  }
  return shader;
}

function createProgram(gl: WebGL2RenderingContext): WebGLProgram {
  const vertexShader = compileShader(gl, gl.VERTEX_SHADER, TERMINAL_GLYPH_VERTEX_SHADER);
  const fragmentShader = compileShader(gl, gl.FRAGMENT_SHADER, TERMINAL_GLYPH_FRAGMENT_SHADER);
  const program = gl.createProgram();
  if (!program) {
    throw new Error('Unable to allocate WebGL program');
  }
  gl.attachShader(program, vertexShader);
  gl.attachShader(program, fragmentShader);
  gl.linkProgram(program);
  gl.deleteShader(vertexShader);
  gl.deleteShader(fragmentShader);
  if (!gl.getProgramParameter(program, gl.LINK_STATUS)) {
    const message = gl.getProgramInfoLog(program) ?? 'Unknown shader link error';
    gl.deleteProgram(program);
    throw new Error(message);
  }
  return program;
}

export class WebGlTerminalRenderer {
  // CSS pixels, not device pixels: the backing store is `* dpr`. Anything speaking
  // device pixels — ws_xpixel, a placement's rendered size — crosses explicitly.
  cellWidth: number;
  cellHeight: number;

  private readonly canvas: HTMLCanvasElement;
  private readonly gl: WebGL2RenderingContext;
  private readonly program: WebGLProgram;
  private readonly buffer: WebGLBuffer;
  private readonly texture: WebGLTexture;
  private readonly uResolution: WebGLUniformLocation | null;
  private readonly atlas: HTMLCanvasElement;
  private readonly atlasContext: CanvasRenderingContext2D;
  private readonly glyphs = new Map<string, AtlasGlyph>();
  // Single-codepoint cells dominate; their numeric key avoids rebuilding a
  // style-prefixed string per cell per paint. Cleared when the atlas is reseeded.
  private readonly codepointGlyphs = new Map<number, AtlasGlyph>();
  // One buffer per cell pass: an image draws between them, and each keeps its own
  // grown capacity rather than sizing to their sum.
  private readonly cellBgVertices = new TerminalVertexBuffer();
  private readonly cellFgVertices = new TerminalVertexBuffer();
  private readonly outlineVertices = new TerminalVertexBuffer(256);
  private readonly imageVertices = new TerminalVertexBuffer(64);
  // Insertion order is LRU order; a texture is re-inserted when it is drawn.
  private readonly imageTextures = new Map<string, WebGLTexture>();
  // Device pixels per CSS pixel, captured once. Read by everything crossing the
  // CSS/device boundary this renderer sits on.
  readonly dpr: number;
  private baseline: number;
  private fontSize: number;
  private readonly fontFamily: string;
  private readonly defaultBg: Rgb;
  private readonly defaultBgPacked: PackedRgb;
  private readonly cursorBgPacked: PackedRgb;
  private atlasSize = INITIAL_ATLAS_SIZE;
  private atlasX = 2;
  private atlasY = 1;
  private atlasRowHeight = 0;
  private atlasGeneration = 0;
  // Ghostty reports dirty rows until markClean(), so a partial update rebuilds only
  // changed rows and concatenates. The frame is still drawn whole: a WebGL drawing
  // buffer is not retained across composites, so scissoring would cost correctness.
  private dirtyRowMask = new Uint8Array(0);
  // One entry per row per cell pass; two arrays, since the frame concatenates every
  // row's backgrounds and then every row's foregrounds.
  private rowBgVertices: Array<TerminalVertexBuffer | null> = [];
  private rowFgVertices: Array<TerminalVertexBuffer | null> = [];
  private printableByRow = new Uint32Array(0);
  private rowCacheValid = false;
  private rowCacheOverBudget = false;
  private modelPrintable = 0;
  // Cursor position as the last frame drew it, so a partial paint can repaint the
  // row it left. A null row means it was hidden.
  private lastCursorRow: number | null = null;
  private lastCursorCol = -1;

  // UV of the 1×1 white texel at atlas pixel (0,0); depends on the atlas size.
  private get solidTexelCenter(): number {
    return 0.5 / this.atlasSize;
  }
  private retryingAtlasFrame = false;
  private cols = 0;
  private rows = 0;
  // Set by setFontSize() so the next resize() re-sizes the canvas even when cols/rows
  // are unchanged — otherwise a hidden pane keeps the old font's canvas until fit().
  private metricsDirty = false;
  // True between releaseDrawingBuffer() and restoreDrawingBuffer(): the canvas is
  // 1×1 and resize() records geometry without re-allocating it.
  private bufferReleased = false;

  constructor(canvas: HTMLCanvasElement, fontSize: number, fontFamily: string, theme: RendererTheme) {
    this.canvas = canvas;
    this.fontSize = fontSize;
    this.fontFamily = fontFamily;
    // defaultBg stays unpacked for gl.clearColor, which wants normalized floats.
    this.defaultBg = parseColor(theme.background);
    this.defaultBgPacked = packColor(this.defaultBg);
    this.cursorBgPacked = parsePackedColor(theme.cursor);
    this.dpr = Math.max(window.devicePixelRatio || 1, 1);

    // `depth` defaults to on for a WebGL2 context and is a drawing-buffer-sized
    // allocation per pane. This renderer draws 2D text in draw order -- it never
    // enables a depth or a stencil test -- so it was allocated and never read.
    // `stencil` already defaults to off; asking is insurance.
    const gl = canvas.getContext('webgl2', {
      alpha: false,
      antialias: false,
      depth: false,
      stencil: false,
    });
    if (!gl) {
      throw new Error('WebGL2 is unavailable; the Ghostty terminal cannot render');
    }
    this.gl = gl;
    this.program = createProgram(gl);

    const metricsCanvas = document.createElement('canvas');
    const metricsContext = metricsCanvas.getContext('2d');
    if (!metricsContext) {
      throw new Error('Unable to measure terminal font');
    }
    metricsContext.font = `${fontSize}px ${fontFamily}`;
    this.cellWidth = Math.max(1, Math.ceil(metricsContext.measureText('M').width));
    this.cellHeight = Math.max(1, Math.ceil(fontSize * 1.45));
    this.baseline = Math.ceil(fontSize * 1.1);

    this.buffer = gl.createBuffer() ?? (() => { throw new Error('Unable to allocate WebGL buffer'); })();
    this.texture = gl.createTexture() ?? (() => { throw new Error('Unable to allocate glyph texture'); })();
    this.atlas = document.createElement('canvas');
    this.atlas.width = this.atlasSize;
    this.atlas.height = this.atlasSize;
    this.atlasContext = this.atlas.getContext('2d') ?? (() => { throw new Error('Unable to allocate glyph atlas'); })();
    this.atlasContext.fillStyle = '#ffffff';
    this.atlasContext.fillRect(0, 0, 1, 1);

    // Premultiply alpha on upload so color-glyph bitmaps filter cleanly: straight alpha
    // under LINEAR bleeds the transparent border's black into glyph edges.
    gl.pixelStorei(gl.UNPACK_PREMULTIPLY_ALPHA_WEBGL, true);
    gl.bindTexture(gl.TEXTURE_2D, this.texture);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, this.atlasSize, this.atlasSize, 0, gl.RGBA, gl.UNSIGNED_BYTE, this.atlas);

    gl.useProgram(this.program);
    gl.bindBuffer(gl.ARRAY_BUFFER, this.buffer);
    const stride = TERMINAL_FLOATS_PER_VERTEX * Float32Array.BYTES_PER_ELEMENT;
    this.configureAttribute('a_position', 2, stride, 0);
    this.configureAttribute('a_texcoord', 2, stride, 2 * Float32Array.BYTES_PER_ELEMENT);
    this.configureAttribute('a_color', 4, stride, 4 * Float32Array.BYTES_PER_ELEMENT);
    this.configureAttribute('a_mode', 1, stride, 8 * Float32Array.BYTES_PER_ELEMENT);
    gl.uniform1i(gl.getUniformLocation(this.program, 'u_atlas'), 0);
    this.uResolution = gl.getUniformLocation(this.program, 'u_resolution');
    gl.enable(gl.BLEND);
    // Premultiplied-alpha blending: the shader emits color already multiplied by
    // coverage, so the source factor is ONE.
    gl.blendFunc(gl.ONE, gl.ONE_MINUS_SRC_ALPHA);
  }

  fitDimensions(width: number, height: number): { cols: number; rows: number } {
    return {
      cols: Math.max(1, Math.floor(width / this.cellWidth)),
      rows: Math.max(1, Math.floor(height / this.cellHeight)),
    };
  }

  resize(cols: number, rows: number): void {
    if (cols === this.cols && rows === this.rows && !this.metricsDirty) {
      return;
    }
    this.metricsDirty = false;
    this.cols = cols;
    this.rows = rows;
    if (this.bufferReleased) {
      // The pane is off-screen and holds no drawing buffer; restoreDrawingBuffer()
      // allocates at this geometry when it is revealed.
      this.resetRowCache(rows);
      return;
    }
    this.applyCanvasGeometry();
  }

  // A pane hidden behind another session keeps its drawing buffer — two
  // window-sized surfaces, ~45MB per pane at 2x DPR — for a frame nobody can
  // see. Shrinking the canvas to 1×1 frees them while the GL context, its
  // program, and the glyph atlas stay alive, so waking costs one full repaint
  // instead of the context rebuild that WKWebView's small context pool
  // punishes. The caller must restore before the pane is painted again.
  releaseDrawingBuffer(): void {
    if (this.bufferReleased) return;
    this.bufferReleased = true;
    this.canvas.width = 1;
    this.canvas.height = 1;
    // CSS size is left alone: it is layout, and the pane's container measures
    // itself, not the canvas.
    this.resetRowCache(this.rows);
  }

  restoreDrawingBuffer(): void {
    if (!this.bufferReleased) return;
    this.bufferReleased = false;
    this.applyCanvasGeometry();
  }

  private applyCanvasGeometry(): void {
    this.canvas.width = Math.ceil(this.cols * this.cellWidth * this.dpr);
    this.canvas.height = Math.ceil(this.rows * this.cellHeight * this.dpr);
    this.canvas.style.width = `${this.cols * this.cellWidth}px`;
    this.canvas.style.height = `${this.rows * this.cellHeight}px`;
    this.gl.viewport(0, 0, this.canvas.width, this.canvas.height);
    this.resetRowCache(this.rows);
  }

  render(
    terminal: GhosttyTerminal,
    force = false,
    viewportCells?: GhosttyCell[],
    overlays?: readonly WebGlOverlay[] | null,
    viewportOffset = 0,
    images?: readonly WebGlImageQuad[] | null,
  ): WebGlRenderSample | null {
    const startedAt = performance.now();
    const dirty = terminal.update();
    const cursor = terminal.getCursor();
    const cursorRow = cursor.visible
      ? cursorRowInViewport(cursor.y, viewportOffset, terminal.rows)
      : null;

    // The cursor is an inverted cell, so where it sits is part of the frame even when
    // no cell changed: a same-row move and a visibility toggle both report DIRTY_NONE.
    // Only the DRAWN cursor counts — a hidden one moved mid-redraw would pass here,
    // find no row to mark, and escalate to a full-grid paint below.
    const cursorMoved = cursorRow !== this.lastCursorRow
      || (cursorRow !== null && cursor.x !== this.lastCursorCol);
    if (!force && dirty === DIRTY_NONE && !cursorMoved) {
      return null;
    }
    // A cursor-only frame has no dirty cells but is still a two-row repaint; without
    // this it would full-paint the grid on every arrow key.
    const cursorOnlyPaint = dirty === DIRTY_NONE && cursorMoved;

    if (this.dirtyRowMask.length !== terminal.rows) {
      this.resetRowCache(terminal.rows);
    }
    // Scrolled viewports, overlays and images are derived surfaces, so they bypass and
    // invalidate the row cache.
    const gridCells = terminal.cols * terminal.rows;
    const canCacheRows = gridCells >= ROW_VERTEX_CACHE_MIN_CELLS
      && !this.rowCacheOverBudget
      && viewportCells === undefined
      && viewportOffset === 0
      && (overlays?.length ?? 0) === 0
      && (images?.length ?? 0) === 0;

    let fullPaint = force
      || (dirty !== DIRTY_PARTIAL && !cursorOnlyPaint)
      || !canCacheRows
      || !this.rowCacheValid;
    const rowsToPaint = this.dirtyRowMask;
    rowsToPaint.fill(fullPaint ? 1 : 0);
    if (!fullPaint) {
      let dirtyRows = 0;
      const markRow = (row: number) => {
        if (row < 0 || row >= terminal.rows) return;
        // Include neighboring rows, as ghostty-web's canvas renderer does, so a grapheme
        // crossing a row boundary is cleared and rebuilt.
        rowsToPaint[row] = 1;
        if (row > 0) rowsToPaint[row - 1] = 1;
        if (row + 1 < terminal.rows) rowsToPaint[row + 1] = 1;
      };
      for (let row = 0; row < terminal.rows; row += 1) {
        if (!terminal.isRowDirty(row)) continue;
        markRow(row);
        dirtyRows += 1;
      }
      // The model's dirty set covers the cursor only on a row change. Measured with
      // scripts/bench-terminal-dirty-rows.mjs: a within-row move and a visibility toggle
      // report DIRTY_NONE, so repaint both the vacated and arrived rows, at most six.
      if (cursorMoved && (cursorRow !== null || this.lastCursorRow !== null)) {
        if (cursorRow !== null) markRow(cursorRow);
        if (this.lastCursorRow !== null) markRow(this.lastCursorRow);
        dirtyRows += 1;
      }
      // PARTIAL with no rows should not occur; a full paint is the safe failure mode.
      if (dirtyRows === 0) {
        fullPaint = true;
        rowsToPaint.fill(1);
      }
    }
    this.lastCursorRow = cursorRow;
    this.lastCursorCol = cursor.x;
    if (!canCacheRows) {
      this.rowCacheValid = false;
    }

    const gl = this.gl;
    const scale = this.dpr;
    const defaultBg = this.defaultBg;
    const cursorBg = this.cursorBgPacked;
    const cursorFg = this.defaultBgPacked;
    const cells = viewportCells ?? terminal.getViewport();
    // Two cell passes, so an image can draw between them. bgVertices holds ONLY
    // non-default background fills; everything else a cell paints is foreground.
    const bgVertices = this.cellBgVertices;
    const fgVertices = this.cellFgVertices;
    bgVertices.reset();
    fgVertices.reset();
    const glyphCountBefore = this.glyphs.size;
    const atlasGenerationBefore = this.atlasGeneration;
    // Resolve overlays into per-row spans once per frame; outlines get their own pass.
    const spansByRow: Array<OverlaySpan[] | undefined> = new Array(terminal.rows);
    const outlines: Array<{ startRow: number; startCol: number; endRow: number; endCol: number; rgb: PackedRgb; alpha: number }> = [];
    for (const overlay of overlays ?? []) {
      const rgb = parsePackedColor(overlay.color);
      const alpha = overlay.alpha ?? 1;
      if (overlay.kind === 'outline') {
        outlines.push({ ...overlay, rgb, alpha });
        continue;
      }
      const firstRow = Math.max(0, overlay.startRow);
      const lastRow = Math.min(terminal.rows - 1, overlay.endRow);
      for (let row = firstRow; row <= lastRow; row += 1) {
        const startCol = row === overlay.startRow ? overlay.startCol : 0;
        const endCol = row === overlay.endRow ? overlay.endCol : terminal.cols;
        if (endCol <= startCol) continue;
        (spansByRow[row] ??= []).push({ startCol, endCol, rgb, alpha, kind: overlay.kind });
      }
    }
    // TEMP (blank-on-split): distinguishes "cells array too short" from "width===0".
    let printableSkippedNull = 0;
    let printableSkippedZeroWidth = 0;

    gl.clearColor(defaultBg.r / 255, defaultBg.g / 255, defaultBg.b / 255, 1);
    gl.clear(gl.COLOR_BUFFER_BIT);

    let paintedRows = 0;
    let frameModelPrintable = 0;
    for (let row = 0; row < terminal.rows; row += 1) {
      if (rowsToPaint[row] === 0) continue;
      paintedRows += 1;
      const rowSpans = spansByRow[row];
      // A row caches its two passes separately: the frame concatenates every row's
      // backgrounds before any row's foregrounds.
      const rowBgTarget = canCacheRows
        ? this.rowVertexBuffer(this.rowBgVertices, row, terminal.cols, ROW_BG_QUADS_PER_CELL)
        : bgVertices;
      const rowFgTarget = canCacheRows
        ? this.rowVertexBuffer(this.rowFgVertices, row, terminal.cols, ROW_FG_QUADS_PER_CELL)
        : fgVertices;
      if (canCacheRows) {
        rowBgTarget.reset();
        rowFgTarget.reset();
      }
      let printableInRow = 0;
      for (let col = 0; col < terminal.cols; col += 1) {
        const cell = cells[row * terminal.cols + col];
        if (cell && cell.codepoint > 32) printableInRow += 1;
        if (!cell || cell.width === 0) {
          if (cell && cell.codepoint > 32) {
            printableSkippedZeroWidth += 1;
          } else if (!cell) {
            // A miss only counts when the index is in-bounds for the model grid.
            if (row * terminal.cols + col < terminal.cols * terminal.rows) {
              printableSkippedNull += 1;
            }
          }
          continue;
        }
        const width = Math.max(cell.width, 1) * this.cellWidth * scale;
        const x = col * this.cellWidth * scale;
        const y = row * this.cellHeight * scale;
        const isCursor = cursorRow !== null && cursor.x === col && cursorRow === row;
        const fg = isCursor ? cursorFg : this.cellForeground(cell);
        const bg = this.cellBackground(cell);

        if (bg !== this.defaultBgPacked) {
          this.pushSolidQuad(rowBgTarget, x, y, width, this.cellHeight * scale, bg, 1);
        }
        if (rowSpans) {
          for (const span of rowSpans) {
            if (span.kind === 'background' && col >= span.startCol && col < span.endCol) {
              this.pushSolidQuad(rowFgTarget, x, y, width, this.cellHeight * scale, span.rgb, span.alpha);
            }
          }
        }
        if (isCursor) {
          this.pushSolidQuad(rowFgTarget, x, y, width, this.cellHeight * scale, cursorBg, 1);
        }
        if ((cell.flags & CellFlags.INVISIBLE) === 0 && cell.codepoint !== 0 && cell.codepoint !== 32) {
          const alpha = (cell.flags & CellFlags.FAINT) !== 0 ? 0.5 : 1;
          if (!this.pushBlockElement(rowFgTarget, cell.codepoint, x, y, width, this.cellHeight * scale, fg, alpha)) {
            const glyph = cell.grapheme_len > 0
              ? this.getGlyph(graphemeAtViewportCell(terminal, row, col, viewportOffset), cell.flags)
              : this.getCodepointGlyph(cell.codepoint, cell.flags);
            this.pushTexturedQuad(rowFgTarget, x, y, glyph.width, glyph.height, glyph, fg, alpha);
          }
        }
        if ((cell.flags & CellFlags.UNDERLINE) !== 0) {
          this.pushSolidQuad(rowFgTarget, x, y + (this.baseline + 2) * scale, width, scale, fg, 1);
        }
        if ((cell.flags & CellFlags.STRIKETHROUGH) !== 0) {
          this.pushSolidQuad(rowFgTarget, x, y + Math.floor(this.cellHeight / 2) * scale, width, scale, fg, 1);
        }
        if (rowSpans) {
          for (const span of rowSpans) {
            if (span.kind === 'underline' && col >= span.startCol && col < span.endCol) {
              this.pushSolidQuad(rowFgTarget, x, y + (this.baseline + 2) * scale, width, scale, span.rgb, span.alpha);
            }
          }
        }
      }
      if (canCacheRows) {
        this.modelPrintable += printableInRow - this.printableByRow[row];
        this.printableByRow[row] = printableInRow;
      } else {
        frameModelPrintable += printableInRow;
      }
    }

    // Settle the printable count before the budget check releases its inputs.
    const sampleModelPrintable = canCacheRows ? this.modelPrintable : frameModelPrintable;

    let retainedRowVertexBytes = 0;
    let retainedStagingBytes = this.cellBgVertices.capacityBytes + this.cellFgVertices.capacityBytes;
    if (canCacheRows) {
      // Concatenate in pass order, not row order, so an image tier can be drawn between
      // the two draws exactly as on the direct path.
      bgVertices.reset();
      fgVertices.reset();
      for (const row of this.rowBgVertices) {
        if (row) bgVertices.append(row.view());
      }
      for (const row of this.rowFgVertices) {
        if (row) fgVertices.append(row.view());
      }
      retainedRowVertexBytes = this.retainedRowVertexBytes();
      retainedStagingBytes = this.retainedStagingBytes();
      // Content crossing the guardrail releases the rows and keeps this grid on the
      // direct renderer until its next resize; the frame built above is unaffected.
      // Releasing here happens exactly once — canCacheRows requires !rowCacheOverBudget.
      if (retainedRowVertexBytes > ROW_VERTEX_CACHE_MAX_BYTES) {
        this.rowCacheOverBudget = true;
        this.releaseRowCache(terminal.rows);
        retainedRowVertexBytes = 0;
        retainedStagingBytes = this.cellBgVertices.capacityBytes + this.cellFgVertices.capacityBytes;
      }
    }

    // Outlines are last, after every image pass, so a selected block's border stays
    // legible over an image drawn above the text.
    const outlineVertices = this.outlineVertices;
    outlineVertices.reset();

    for (const outline of outlines) {
      const top = Math.max(0, outline.startRow) * this.cellHeight * scale;
      const bottom = (Math.min(terminal.rows - 1, outline.endRow) + 1) * this.cellHeight * scale;
      const left = Math.max(0, outline.startCol) * this.cellWidth * scale;
      const right = Math.min(terminal.cols, outline.endCol) * this.cellWidth * scale;
      if (right <= left || bottom <= top) continue;
      const thickness = scale;
      // Only real boundaries of the region (see visibleOutlineEdges): a block taller
      // than the screen would otherwise draw a box around the terminal.
      const { drawTop, drawBottom } = visibleOutlineEdges(outline.startRow, outline.endRow, terminal.rows);
      if (drawTop) {
        this.pushSolidQuad(outlineVertices, left, top, right - left, thickness, outline.rgb, outline.alpha);
      }
      if (drawBottom) {
        this.pushSolidQuad(outlineVertices, left, bottom - thickness, right - left, thickness, outline.rgb, outline.alpha);
      }
      this.pushSolidQuad(outlineVertices, left, top, thickness, bottom - top, outline.rgb, outline.alpha);
      this.pushSolidQuad(outlineVertices, right - thickness, top, thickness, bottom - top, outline.rgb, outline.alpha);
    }

    if (this.atlasGeneration !== atlasGenerationBefore && !this.retryingAtlasFrame) {
      this.retryingAtlasFrame = true;
      try {
        return this.render(terminal, true, viewportCells, overlays, viewportOffset, images);
      } finally {
        this.retryingAtlasFrame = false;
      }
    }

    // Kitty's three z tiers, in draw order; filtered rather than assumed sorted, and
    // the caller's z-then-id order is kept within a tier.
    const imageQuads = images ?? [];
    const underBackground = imageQuads.filter((q) => q.z < KITTY_Z_UNDER_BACKGROUND);
    const underText = imageQuads.filter((q) => q.z >= KITTY_Z_UNDER_BACKGROUND && q.z < 0);
    const overText = imageQuads.filter((q) => q.z >= 0);

    gl.useProgram(this.program);
    gl.bindTexture(gl.TEXTURE_2D, this.texture);
    gl.bindBuffer(gl.ARRAY_BUFFER, this.buffer);
    gl.uniform2f(this.uResolution, this.canvas.width, this.canvas.height);
    let imageQuadsDrawn = 0;
    imageQuadsDrawn += this.drawImages(underBackground, scale);
    gl.bufferData(gl.ARRAY_BUFFER, bgVertices.view(), gl.DYNAMIC_DRAW);
    gl.drawArrays(gl.TRIANGLES, 0, bgVertices.length / TERMINAL_FLOATS_PER_VERTEX);
    imageQuadsDrawn += this.drawImages(underText, scale);
    gl.bufferData(gl.ARRAY_BUFFER, fgVertices.view(), gl.DYNAMIC_DRAW);
    gl.drawArrays(gl.TRIANGLES, 0, fgVertices.length / TERMINAL_FLOATS_PER_VERTEX);
    imageQuadsDrawn += this.drawImages(overText, scale);
    if (outlineVertices.length > 0) {
      gl.bufferData(gl.ARRAY_BUFFER, outlineVertices.view(), gl.DYNAMIC_DRAW);
      gl.drawArrays(gl.TRIANGLES, 0, outlineVertices.length / TERMINAL_FLOATS_PER_VERTEX);
    }
    const submittedQuads = bgVertices.quadCount + fgVertices.quadCount + outlineVertices.quadCount + imageQuadsDrawn;
    if (canCacheRows && !this.rowCacheOverBudget) this.rowCacheValid = true;
    terminal.markClean();
    return {
      cpuSubmitMs: performance.now() - startedAt,
      cells: terminal.cols * terminal.rows,
      paintedCells: paintedRows * terminal.cols,
      paintedRows,
      fullPaint,
      submittedQuads,
      retainedRowVertexBytes,
      retainedStagingBytes,
      modelPrintable: sampleModelPrintable,
      quads: submittedQuads,
      glyphUploads: this.glyphs.size - glyphCountBefore,
      cellsArrayLen: cells.length,
      printableSkippedNull,
      printableSkippedZeroWidth,
    };
  }

  dispose(): void {
    this.gl.deleteBuffer(this.buffer);
    this.gl.deleteTexture(this.texture);
    for (const texture of this.imageTextures.values()) {
      this.gl.deleteTexture(texture);
    }
    this.imageTextures.clear();
    this.gl.deleteProgram(this.program);
    this.glyphs.clear();
  }

  // One textured quad per image, once per z tier, so it must leave the GL state
  // exactly as it found it. Composites with STRAIGHT alpha, since the premultiply
  // flag only applies to DOM-element uploads. Returns how many drew.
  private drawImages(quads: readonly WebGlImageQuad[], scale: number): number {
    if (quads.length === 0) return 0;
    const gl = this.gl;
    gl.blendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA);
    let drawn = 0;
    for (const quad of quads) {
      const texture = this.imageTexture(quad.source);
      if (!texture) continue;
      const { width, height } = quad.source;
      if (width <= 0 || height <= 0) continue;
      const vertices = this.imageVertices;
      vertices.reset();
      this.pushQuad(
        vertices,
        quad.x * scale,
        quad.y * scale,
        quad.width * scale,
        quad.height * scale,
        quad.sourceX / width,
        quad.sourceY / height,
        (quad.sourceX + quad.sourceWidth) / width,
        (quad.sourceY + quad.sourceHeight) / height,
        // The color-glyph path passes the texel through, scaled by quad alpha.
        PACKED_WHITE,
        1,
        GLYPH_MODE_COLOR,
      );
      gl.bindTexture(gl.TEXTURE_2D, texture);
      gl.bufferData(gl.ARRAY_BUFFER, vertices.view(), gl.DYNAMIC_DRAW);
      gl.drawArrays(gl.TRIANGLES, 0, vertices.length / TERMINAL_FLOATS_PER_VERTEX);
      drawn += 1;
    }
    gl.blendFunc(gl.ONE, gl.ONE_MINUS_SRC_ALPHA);
    // The cell and outline passes sample the glyph atlas; hand the unit back.
    gl.bindTexture(gl.TEXTURE_2D, this.texture);
    return drawn;
  }

  // The GPU texture for one image, uploaded on first use. Null when the blob cannot
  // be drawn: a wrong stride renders plausible garbage instead of failing.
  private imageTexture(source: WebGlImageSource): WebGLTexture | null {
    const key = `${source.imageId}:${source.generation}`;
    const existing = this.imageTextures.get(key);
    if (existing) {
      this.imageTextures.delete(key);
      this.imageTextures.set(key, existing);
      return existing;
    }
    const expected = source.width * source.height * KITTY_FORMAT_BYTES_PER_PIXEL[source.format];
    if (source.pixels.byteLength < expected) {
      console.error(
        `[kitty] image ${source.imageId} generation ${source.generation} carries ${source.pixels.byteLength} bytes for a ${source.width}x${source.height} ${source.format} image needing ${expected}`,
      );
      return null;
    }
    const gl = this.gl;
    const texture = gl.createTexture();
    if (!texture) {
      console.error(`[kitty] no GPU texture available for image ${source.imageId}`);
      return null;
    }
    const upload = imageUpload(gl, source);
    gl.bindTexture(gl.TEXTURE_2D, texture);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
    // RGB rows are 3 bytes per pixel; the default 4-byte unpack alignment would shear
    // every odd-width image.
    gl.pixelStorei(gl.UNPACK_ALIGNMENT, 1);
    gl.texImage2D(
      gl.TEXTURE_2D,
      0,
      upload.format,
      source.width,
      source.height,
      0,
      upload.format,
      gl.UNSIGNED_BYTE,
      upload.pixels,
    );
    gl.pixelStorei(gl.UNPACK_ALIGNMENT, 4);
    while (this.imageTextures.size >= IMAGE_TEXTURE_LIMIT) {
      const oldest = this.imageTextures.keys().next().value as string;
      const evicted = this.imageTextures.get(oldest);
      this.imageTextures.delete(oldest);
      if (evicted) gl.deleteTexture(evicted);
      console.warn(
        `[kitty] texture limit ${IMAGE_TEXTURE_LIMIT} reached (asked for image ${source.imageId} generation ${source.generation}) — evicted ${oldest}`,
      );
    }
    this.imageTextures.set(key, texture);
    return texture;
  }

  // Drop every cached glyph so the next render re-rasterizes against the current
  // document fonts; a late web font would otherwise serve fallbacks forever.
  invalidateGlyphCache(): void {
    this.reseedAtlas();
  }

  // Re-metric in place rather than reconstructing the WASM model + WebGL context:
  // WKWebView's small context pool can permanently break a rebuilt pane. The caller
  // re-asserts cols/rows via resize().
  setFontSize(fontSize: number): void {
    this.fontSize = fontSize;
    const metricsCanvas = document.createElement('canvas');
    const metricsContext = metricsCanvas.getContext('2d');
    if (!metricsContext) {
      throw new Error('Unable to measure terminal font');
    }
    metricsContext.font = `${fontSize}px ${this.fontFamily}`;
    this.cellWidth = Math.max(1, Math.ceil(metricsContext.measureText('M').width));
    this.cellHeight = Math.max(1, Math.ceil(fontSize * 1.45));
    this.baseline = Math.ceil(fontSize * 1.1);
    this.metricsDirty = true;
    // Cached row vertices carry geometry from the old cell metrics; a reseeded atlas
    // drops them too, but geometry is its own reason.
    this.rowCacheValid = false;
    this.invalidateGlyphCache();
  }

  private configureAttribute(name: string, size: number, stride: number, offset: number): void {
    const location = this.gl.getAttribLocation(this.program, name);
    this.gl.enableVertexAttribArray(location);
    this.gl.vertexAttribPointer(location, size, this.gl.FLOAT, false, stride, offset);
  }

  // `quadsPerCell` only sets the starting capacity, so an underestimate costs one
  // reallocation. The passes differ on purpose: a foreground cell may add a tint,
  // underline or strikethrough; a background draws at most one quad. Sizing both at
  // a full row is what doubled retained bytes.
  private rowVertexBuffer(
    rows: Array<TerminalVertexBuffer | null>,
    row: number,
    cols: number,
    quadsPerCell: number,
  ): TerminalVertexBuffer {
    const existing = rows[row];
    if (existing) return existing;
    const created = new TerminalVertexBuffer(Math.max(
      TERMINAL_FLOATS_PER_QUAD,
      Math.ceil(cols * quadsPerCell) * TERMINAL_FLOATS_PER_QUAD,
    ));
    rows[row] = created;
    return created;
  }

  // What the row cache retains: zero on the direct path, so it stays the legible
  // "is the cache allocated" signal.
  private retainedRowVertexBytes(): number {
    let bytes = 0;
    for (const row of this.rowBgVertices) bytes += row?.capacityBytes ?? 0;
    for (const row of this.rowFgVertices) bytes += row?.capacityBytes ?? 0;
    return bytes;
  }

  // Everything kept alive between paints: retained rows plus the frame buffers they
  // concatenate into. Reported, not gated — see ROW_VERTEX_CACHE_MAX_BYTES.
  private retainedStagingBytes(): number {
    return this.cellBgVertices.capacityBytes
      + this.cellFgVertices.capacityBytes
      + this.retainedRowVertexBytes();
  }

  private resetRowCache(rows: number): void {
    this.dirtyRowMask = new Uint8Array(rows);
    this.rowCacheOverBudget = false;
    this.releaseRowCache(rows);
  }

  // Drop the retained rows but keep the over-budget verdict, so the grid stays on
  // the direct path until it resizes.
  private releaseRowCache(rows: number): void {
    this.rowBgVertices = Array.from({ length: rows }, () => null);
    this.rowFgVertices = Array.from({ length: rows }, () => null);
    this.printableByRow = new Uint32Array(rows);
    this.rowCacheValid = false;
    this.modelPrintable = 0;
  }

  private cellForeground(cell: GhosttyCell): PackedRgb {
    if ((cell.flags & CellFlags.INVERSE) !== 0) {
      return packRgb(cell.bg_r, cell.bg_g, cell.bg_b);
    }
    return packRgb(cell.fg_r, cell.fg_g, cell.fg_b);
  }

  private cellBackground(cell: GhosttyCell): PackedRgb {
    if ((cell.flags & CellFlags.INVERSE) !== 0) {
      return packRgb(cell.fg_r, cell.fg_g, cell.fg_b);
    }
    return packRgb(cell.bg_r, cell.bg_g, cell.bg_b);
  }

  private getCodepointGlyph(codepoint: number, flags: number): AtlasGlyph {
    const style = (flags & CellFlags.ITALIC ? 1 : 0) | (flags & CellFlags.BOLD ? 2 : 0);
    const key = codepoint * 4 + style;
    const existing = this.codepointGlyphs.get(key);
    if (existing) return existing;
    const glyph = this.getGlyph(String.fromCodePoint(codepoint), flags);
    this.codepointGlyphs.set(key, glyph);
    return glyph;
  }

  private getGlyph(text: string, flags: number): AtlasGlyph {
    const style = `${flags & CellFlags.ITALIC ? 'italic ' : ''}${flags & CellFlags.BOLD ? 'bold ' : ''}`;
    const key = `${style}${text}`;
    const existing = this.glyphs.get(key);
    if (existing) {
      return existing;
    }

    const context = this.atlasContext;
    const scale = this.dpr;
    // Emoji clusters must be shaped Apple-Color-Emoji-first or WKWebView's canvas
    // fallback decomposes them; see terminalGlyphFont.
    const font = terminalGlyphFont(style, this.fontSize * scale, this.fontFamily, text);
    context.font = font;
    const width = Math.max(Math.ceil(context.measureText(text).width) + 4, this.cellWidth * scale);
    const height = this.cellHeight * scale;
    if (this.atlasX + width >= this.atlasSize) {
      this.atlasX = 2;
      this.atlasY += this.atlasRowHeight + 1;
      this.atlasRowHeight = 0;
    }
    if (this.atlasY + height >= this.atlasSize) {
      // Atlas full: grow (doubling, capped); only clear-and-reuse once at the cap.
      if (this.atlasSize < MAX_ATLAS_SIZE) {
        this.growAtlas();
      } else {
        this.resetAtlas();
      }
      // Resizing a canvas resets ALL 2D context state, font included. Re-apply it, or
      // the wrong bitmap is cached and the render() retry reuses it forever.
      context.font = font;
    }

    const x = this.atlasX;
    const y = this.atlasY;
    context.clearRect(x, y, width, height);
    context.fillStyle = '#ffffff';
    context.textBaseline = 'alphabetic';
    context.fillText(text, x, y + this.baseline * scale);
    const bitmap = context.getImageData(x, y, width, height);
    this.gl.bindTexture(this.gl.TEXTURE_2D, this.texture);
    this.gl.texSubImage2D(this.gl.TEXTURE_2D, 0, x, y, width, height, this.gl.RGBA, this.gl.UNSIGNED_BYTE, bitmap);

    const glyph = {
      u0: x / this.atlasSize,
      v0: y / this.atlasSize,
      u1: (x + width) / this.atlasSize,
      v1: (y + height) / this.atlasSize,
      width,
      height,
      colored: isColorGlyphBitmap(bitmap),
    };
    this.atlasX += width + 1;
    this.atlasRowHeight = Math.max(this.atlasRowHeight, height);
    this.glyphs.set(key, glyph);
    return glyph;
  }

  // Double the atlas (capped) and re-seed; glyph-light sessions never call this.
  private growAtlas(): void {
    this.atlasSize = nextAtlasSize(this.atlasSize, MAX_ATLAS_SIZE);
    this.reseedAtlas();
  }

  // Clear the glyph cache and reuse the atlas; only reached once it is at the cap.
  private resetAtlas(): void {
    this.reseedAtlas();
  }

  // Clear the glyph cache and (re)initialize the backing canvas + GPU texture at the
  // current size, re-seeding the white texel at (0,0) and bumping the generation so
  // an in-flight frame re-rasterizes (see render()'s retry).
  private reseedAtlas(): void {
    this.glyphs.clear();
    this.codepointGlyphs.clear();
    // Cached vertices carry UVs into this atlas: once it moves, every row must be
    // rebuilt before it can join another frame.
    this.rowCacheValid = false;
    this.atlasX = 2;
    this.atlasY = 1;
    this.atlasRowHeight = 0;
    this.atlasGeneration += 1;
    this.atlas.width = this.atlasSize;
    this.atlas.height = this.atlasSize;
    this.atlasContext.fillStyle = '#ffffff';
    this.atlasContext.fillRect(0, 0, 1, 1);
    this.gl.bindTexture(this.gl.TEXTURE_2D, this.texture);
    this.gl.texImage2D(
      this.gl.TEXTURE_2D,
      0,
      this.gl.RGBA,
      this.atlasSize,
      this.atlasSize,
      0,
      this.gl.RGBA,
      this.gl.UNSIGNED_BYTE,
      this.atlas,
    );
  }

  private pushSolidQuad(vertices: TerminalVertexBuffer, x: number, y: number, width: number, height: number, color: PackedRgb, alpha: number): void {
    // Keep samples inside the white texel: LINEAR on its edges blends into
    // transparent neighbours and leaves seams. Mode 0 tints its coverage.
    this.pushQuad(vertices, x, y, width, height, this.solidTexelCenter, this.solidTexelCenter, this.solidTexelCenter, this.solidTexelCenter, color, alpha, GLYPH_MODE_TINT);
  }

  private pushBlockElement(vertices: TerminalVertexBuffer, codepoint: number, x: number, y: number, width: number, height: number, color: PackedRgb, alpha: number): boolean {
    const rects = BLOCK_ELEMENT_RECTS[codepoint];
    if (!rects) {
      return false;
    }
    for (const rect of rects) {
      this.pushSolidQuad(
        vertices,
        x + width * rect.x,
        y + height * rect.y,
        width * rect.width,
        height * rect.height,
        color,
        alpha,
      );
    }
    return true;
  }

  private pushTexturedQuad(vertices: TerminalVertexBuffer, x: number, y: number, width: number, height: number, glyph: AtlasGlyph, color: PackedRgb, alpha: number): void {
    // Color glyphs use mode 1 (atlas RGBA passed through); monochrome use mode 0.
    this.pushQuad(vertices, x, y, width, height, glyph.u0, glyph.v0, glyph.u1, glyph.v1, color, alpha, glyph.colored ? GLYPH_MODE_COLOR : GLYPH_MODE_TINT);
  }

  private pushQuad(
    vertices: TerminalVertexBuffer,
    x: number,
    y: number,
    width: number,
    height: number,
    u0: number,
    v0: number,
    u1: number,
    v1: number,
    color: PackedRgb,
    alpha: number,
    mode: number,
  ): void {
    vertices.pushQuad(x, y, width, height, u0, v0, u1, v1, color, alpha, mode);
  }
}
