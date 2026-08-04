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

// A rectangular range overlay in viewport coordinates. Rows are inclusive of
// endRow; columns are exclusive of endCol. Spans follow selection semantics:
// rows strictly between startRow and endRow cover the full grid width.
// - background: solid fill drawn under glyphs (selection, find matches)
// - underline: bar at the text baseline drawn over glyphs (hovered links)
// - outline: thin border around the bounding rectangle (selected block)
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

// Which horizontal edges of an outline are real boundaries given the viewport.
// An edge that falls outside [0, rows) is not the region's true top/bottom — it
// only landed there because the region (e.g. a command block) extends past the
// visible area. Drawing it would clamp the border to the viewport edge and make
// the outline look like a box wrapping the whole terminal. Omitting it leaves
// side rails that read as "continues above/below".
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

// One kitty image's pixels, as the blob cache holds them. Identity is
// (imageId, generation): a retransmission of the same id mints a new
// generation, so a texture is never stale content under a live key.
export interface WebGlImageSource {
  imageId: number;
  generation: number;
  width: number;
  height: number;
  format: KittyPixelFormat;
  pixels: Uint8Array;
}

// One image to draw this frame, already clipped to the grid by the caller.
// Destination is CSS pixels relative to the grid's top-left; the source rect is
// texels of the image.
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
  // The placement's kitty z-index, which picks the pass this quad draws in:
  // over the text at z >= 0, under it below that, and under the cell
  // backgrounds too past KITTY_Z_UNDER_BACKGROUND.
  z: number;
}

// Kitty's deepest layer: "Negative z-index values below INT32_MIN/2
// (-1,073,741,824) will be drawn under cells with non-default background
// colors." Below is strict — a placement AT this value draws over the cell
// backgrounds and under the text, like any other negative z.
export const KITTY_Z_UNDER_BACKGROUND = -1_073_741_824;

// Live GPU textures per renderer. Receipt: a stored image measured 1.9-6.5MB of
// decoded pixels, so 16 textures is ~100MB of VRAM worst case and holds every
// image a real session has on screen several times over — a tripwire, not a
// budget. Textures die with the pane's GL context; the app-level blob cache is
// what survives a pane being virtualized away, and rebuilding one from it is a
// single upload.
export const IMAGE_TEXTURE_LIMIT = 16;

// Below this grid size, copying the cached rows into the complete staging
// buffer costs more than rebuilding the small viewport. Packaged A/Bs put the
// crossover below a 1,785-printable-cell split pane. Keep a wider safety margin
// around ordinary interaction-sized panes and enable it only above 2,048 cells;
// the packaged 3,212-cell case remains a measured win.
const ROW_VERTEX_CACHE_MIN_CELLS = 2_048;
// Ceiling on what the row cache itself retains. This governs the cache's
// marginal cost only: the frame buffer the rows concatenate into is a second
// full-frame copy, but a frame has to be staged somewhere whether or not the
// cache exists, so folding it in here would trip the gate on memory the direct
// path spends anyway. Both numbers are reported on every sample
// (retainedRowVertexBytes and retainedStagingBytes) so the receipt stays whole.
//
// Styled content emits several quads per cell, so the real cost is
// content-dependent and only knowable after a frame is built; a grid that
// crosses the ceiling releases its rows and stays on the direct path until it
// resizes.
const ROW_VERTEX_CACHE_MAX_BYTES = 2 * 1024 * 1024;

// Starting capacity per row, in quads per column, for each cell pass. Every
// printable cell contributes a foreground quad, so a full row is the honest
// start for that pass. How many cells carry a non-default background is a
// property of the content, not something to guess at: the background pass
// starts at the buffer minimum and grows into whatever the pane turns out to
// need, which costs a reallocation on a styled row and nothing on a plain one.
const ROW_FG_QUADS_PER_CELL = 1;
const ROW_BG_QUADS_PER_CELL = 0;

// ghostty-web does not export DirtyState, but its update() contract defines
// these values. PARTIAL is the only one the row cache can serve; anything else
// rebuilds the whole grid.
const DIRTY_NONE = 0;
const DIRTY_PARTIAL = 1;

interface AtlasGlyph {
  u0: number;
  v0: number;
  u1: number;
  v1: number;
  width: number;
  height: number;
  // True when the rasterized bitmap carries its own colors (a color font such
  // as Apple Color Emoji). Such glyphs are drawn directly from the atlas
  // instead of being tinted with the cell's foreground color.
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
  // Rows plus the frame buffer they concatenate into: everything this renderer
  // holds between paints, including the staging a pane pays on the direct path.
  retainedStagingBytes: number;
  modelPrintable: number;
  quads: number;
  glyphUploads: number;
  // TEMP (blank-on-split): diagnostics to explain why drawn quads can fall
  // below the model's printable-cell count after a resize. Remove with the
  // render-trace instrumentation once the root cause is fixed.
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

// The glyph atlas is allocated eagerly and in full (a backing 2D canvas plus a
// GPU texture of the same dimensions), so its size is a fixed per-renderer
// memory cost paid up front regardless of how many glyphs a session actually
// uses. A terminal renders a small, mostly-fixed glyph set (ASCII + styles +
// box-drawing + the occasional Unicode/emoji), so most panes fit comfortably in
// 1024². We therefore start small and grow on demand up to the previous fixed
// size only when a glyph-heavy session (e.g. CJK/emoji) actually overflows the
// atlas — keeping the common case cheap (≈8 MB/renderer vs ≈32 MB at 2048²)
// without risking glyph-atlas thrash on heavy content. See growAtlas/resetAtlas.
export const INITIAL_ATLAS_SIZE = 1024;
export const MAX_ATLAS_SIZE = 2048;
// position(2) + texcoord(2) + color(4) + mode(1). The mode flag selects the
// fragment path: 0 = tinted coverage (text/box/overlays sample the atlas alpha
// and paint it in the quad's color), 1 = color glyph (emoji: sample the atlas
// RGBA directly so the glyph keeps its own colors).

// Next atlas size when the current one fills: double it, but never exceed the
// cap. Idempotent at the cap, so repeated growth always converges to
// MAX_ATLAS_SIZE and can never grow unbounded.
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

// The WebGL format and byte view one stored image uploads with. RGB and RGBA go
// up untouched (the common real layouts); the two grayscale layouts are widened
// to RGBA on the CPU rather than leaning on WebGL2's legacy LUMINANCE formats —
// ghostty only produces them from a grayscale PNG, so the copy is on a path no
// measured emitter takes.
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
  // CSS pixels, not device pixels: the canvas is sized `cols * cellWidth` in
  // style and `cols * cellWidth * dpr` in backing store, and every draw scales
  // by dpr on its way to the GPU. Anything talking to the outside world in
  // device pixels — the PTY's ws_xpixel, a kitty placement's rendered size —
  // has to cross that boundary explicitly.
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
  // Single-codepoint cells are overwhelmingly common. Their numeric key avoids
  // rebuilding a character string plus style-prefixed cache key for every cell
  // on every paint. It is bounded by the atlas's existing glyph set and clears
  // whenever that atlas is reseeded.
  private readonly codepointGlyphs = new Map<number, AtlasGlyph>();
  // One buffer per cell pass. They are separate because an image can draw
  // between them, and staying separate is also what lets each keep its own
  // grown capacity: backgrounds are one quad per non-default cell, foregrounds
  // several per styled cell, so a shared buffer would size to their sum.
  private readonly cellBgVertices = new TerminalVertexBuffer();
  private readonly cellFgVertices = new TerminalVertexBuffer();
  private readonly outlineVertices = new TerminalVertexBuffer(256);
  private readonly imageVertices = new TerminalVertexBuffer(64);
  // Insertion order is LRU order; a texture is re-inserted when it is drawn.
  private readonly imageTextures = new Map<string, WebGLTexture>();
  // Device pixels per CSS pixel, captured once at construction. Read by
  // everything that has to cross the CSS/device boundary this renderer sits on:
  // the backing store, the pixel geometry reported to the PTY, and the CSS size
  // a kitty placement's device-pixel box draws at.
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
  // Ghostty reports dirty rows until markClean(). Cache each rendered row in
  // the same CPU-side vertex format the complete frame already uses: partial
  // model updates only rebuild changed rows, then concatenate the row buffers
  // and still clear/draw a complete frame. That last part is deliberate — a
  // WebGL drawing buffer is not retained across composites by default, so
  // scissoring dirty pixels would trade correctness for speed unless we kept a
  // much larger Retina-sized framebuffer. This cache is visible-grid and
  // content proportional, with a hard retained-memory ceiling.
  private dirtyRowMask = new Uint8Array(0);
  // One entry per row per cell pass. Two arrays rather than one of pairs: the
  // frame concatenates every row's backgrounds, then every row's foregrounds,
  // so each is walked whole.
  private rowBgVertices: Array<TerminalVertexBuffer | null> = [];
  private rowFgVertices: Array<TerminalVertexBuffer | null> = [];
  private printableByRow = new Uint32Array(0);
  private rowCacheValid = false;
  private rowCacheOverBudget = false;
  private modelPrintable = 0;
  // Cursor position as the last frame drew it, so a partial paint can repaint
  // the row it left behind. null row means the cursor was hidden.
  private lastCursorRow: number | null = null;
  private lastCursorCol = -1;

  // UV of the center of the 1×1 solid white texel at atlas pixel (0,0). Depends
  // on the current atlas size, so it is recomputed rather than precomputed.
  private get solidTexelCenter(): number {
    return 0.5 / this.atlasSize;
  }
  private retryingAtlasFrame = false;
  private cols = 0;
  private rows = 0;
  // Set by setFontSize() so the next resize() call re-sizes the canvas even
  // when cols/rows are unchanged. Cell metrics can change independently of
  // grid dimensions (a font-size change with no container resize), and
  // resize()'s cols/rows guard alone would otherwise skip it, leaving the
  // canvas sized for the old font — invisible on the active pane (fit()
  // recomputes cols/rows too, so it rarely hits this path) but permanent on
  // hidden panes, which never fit() until revealed.
  private metricsDirty = false;

  constructor(canvas: HTMLCanvasElement, fontSize: number, fontFamily: string, theme: RendererTheme) {
    this.canvas = canvas;
    this.fontSize = fontSize;
    this.fontFamily = fontFamily;
    // defaultBg stays unpacked for gl.clearColor, which wants normalized floats.
    this.defaultBg = parseColor(theme.background);
    this.defaultBgPacked = packColor(this.defaultBg);
    this.cursorBgPacked = parsePackedColor(theme.cursor);
    this.dpr = Math.max(window.devicePixelRatio || 1, 1);

    const gl = canvas.getContext('webgl2', { alpha: false, antialias: false });
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

    // Premultiply alpha on upload so color-glyph (emoji) bitmaps filter cleanly:
    // straight-alpha color sampled with LINEAR bleeds the transparent border's
    // black into glyph edges, leaving dark fringes. Premultiplied source plus a
    // premultiplied blend (set below) interpolates correctly. Coverage-only
    // glyphs are unaffected — only their RGB is scaled, and the tinted path
    // reads just the alpha channel.
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
    // coverage, so source factor is ONE. This keeps tinted text identical to the
    // old SRC_ALPHA blend while letting color glyphs composite without fringing.
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
    this.canvas.width = Math.ceil(cols * this.cellWidth * this.dpr);
    this.canvas.height = Math.ceil(rows * this.cellHeight * this.dpr);
    this.canvas.style.width = `${cols * this.cellWidth}px`;
    this.canvas.style.height = `${rows * this.cellHeight}px`;
    this.gl.viewport(0, 0, this.canvas.width, this.canvas.height);
    this.resetRowCache(rows);
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

    // The cursor is drawn as an inverted cell, so where it sits is part of the
    // frame even when no cell content changed. A same-row move and a visibility
    // toggle both report DIRTY_NONE, and comparing the cursor against the last
    // frame here is the only thing standing between those and a cursor left
    // painted at its old column until unrelated output arrives.
    //
    // Only the drawn cursor counts. A hidden one paints nothing, so its column
    // cannot go stale, and a TUI moving a hidden cursor mid-redraw — which they
    // do constantly — would otherwise pass this check, find no row to mark, and
    // be escalated to a full-grid paint by the zero-row guard below.
    const cursorMoved = cursorRow !== this.lastCursorRow
      || (cursorRow !== null && cursor.x !== this.lastCursorCol);
    if (!force && dirty === DIRTY_NONE && !cursorMoved) {
      return null;
    }
    // A frame that exists only because the cursor moved has no dirty cells, but
    // it is still a two-row repaint rather than a whole-grid one. Without this
    // it would fall to `dirty !== DIRTY_PARTIAL` below and full-paint the grid
    // on every arrow key — the exact cost the row cache is here to avoid.
    const cursorOnlyPaint = dirty === DIRTY_NONE && cursorMoved;

    if (this.dirtyRowMask.length !== terminal.rows) {
      this.resetRowCache(terminal.rows);
    }
    // Scrolled viewports, overlays, and images are derived surfaces rather than
    // the model's active grid, so they bypass and invalidate the row cache.
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
        // Match ghostty-web's own canvas renderer: include neighboring rows so
        // a grapheme whose pixels cross a row boundary is cleared and rebuilt.
        rowsToPaint[row] = 1;
        if (row > 0) rowsToPaint[row - 1] = 1;
        if (row + 1 < terminal.rows) rowsToPaint[row + 1] = 1;
      };
      for (let row = 0; row < terminal.rows; row += 1) {
        if (!terminal.isRowDirty(row)) continue;
        markRow(row);
        dirtyRows += 1;
      }
      // The model's dirty set covers the cursor only when it changes row.
      // Measured with scripts/bench-terminal-dirty-rows.mjs against the
      // vendored WASM: a cross-row CUP reports both the vacated and the
      // arrived row, but a move within one row ("\x1b[2;9H", "\x1b[D") and a
      // visibility toggle ("\x1b[?25l") report DIRTY_NONE — no rows at all.
      //
      // A bare move (DIRTY_NONE, no other dirty rows) would leave the old
      // inverted cursor on screen until unrelated output triggers a paint. When
      // riding along with an unrelated dirty row in a PARTIAL frame, the
      // cursor's row is not in the set and the cursor stays baked into the
      // cached row at the old column. Repainting both the vacated and arrived
      // rows costs at most six rows and covers both cases. ghostty-web's own
      // canvas renderer compensates the same way.
      if (cursorMoved && (cursorRow !== null || this.lastCursorRow !== null)) {
        if (cursorRow !== null) markRow(cursorRow);
        if (this.lastCursorRow !== null) markRow(this.lastCursorRow);
        dirtyRows += 1;
      }
      // PARTIAL with no rows would leave an unknowable stale surface. It should
      // not occur, but a full paint is the safe failure mode if the ABI drifts.
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
    // Two cell passes, so an image can be drawn between them. bgVertices holds
    // ONLY the per-cell non-default background fills; everything else a cell
    // paints — selection/search tints, the cursor block, glyphs, underlines —
    // is foreground, and draws after the under-text images so neither the
    // cursor nor a selection can vanish behind one.
    const bgVertices = this.cellBgVertices;
    const fgVertices = this.cellFgVertices;
    bgVertices.reset();
    fgVertices.reset();
    const glyphCountBefore = this.glyphs.size;
    const atlasGenerationBefore = this.atlasGeneration;
    // Resolve overlays into per-row column spans once per frame so the cell
    // loop only checks the (typically 0-2) spans on its own row. Outlines are
    // geometric borders and render in a dedicated pass after the cells.
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
    // TEMP (blank-on-split): count printable cells dropped by the width/null
    // skip below, to distinguish "cells array too short" from "width===0".
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
      // A row caches its two passes separately, because the frame concatenates
      // every row's backgrounds before any row's foregrounds.
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
            // Only count as a "printable" miss if this index is in-bounds for
            // the model grid but absent from the cells array.
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

    // Settle the printable count before the budget check below can release the
    // per-row totals it is derived from.
    const sampleModelPrintable = canCacheRows ? this.modelPrintable : frameModelPrintable;

    let retainedRowVertexBytes = 0;
    let retainedStagingBytes = this.cellBgVertices.capacityBytes + this.cellFgVertices.capacityBytes;
    if (canCacheRows) {
      // Concatenate in pass order, not row order: every row's backgrounds form
      // the first draw, every row's foregrounds the second, so an image tier can
      // be drawn between them exactly as it is on the direct path.
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
      // A styled cell can emit several quads. If real content grows the cache
      // past the guardrail, release the rows and keep this grid on the direct
      // renderer until its next resize. The frame already built above is
      // unaffected: it was concatenated into the two cell buffers the draw
      // reads from. Releasing here rather than after the draw is what makes it
      // happen exactly once — canCacheRows requires !rowCacheOverBudget, so
      // this branch is unreachable on every later frame of the episode.
      if (retainedRowVertexBytes > ROW_VERTEX_CACHE_MAX_BYTES) {
        this.rowCacheOverBudget = true;
        this.releaseRowCache(terminal.rows);
        retainedRowVertexBytes = 0;
        retainedStagingBytes = this.cellBgVertices.capacityBytes + this.cellFgVertices.capacityBytes;
      }
    }

    // Outlines are the last thing on the frame, after every image pass, so a
    // selected block's border stays legible over an image drawn above the text.
    const outlineVertices = this.outlineVertices;
    outlineVertices.reset();

    for (const outline of outlines) {
      const top = Math.max(0, outline.startRow) * this.cellHeight * scale;
      const bottom = (Math.min(terminal.rows - 1, outline.endRow) + 1) * this.cellHeight * scale;
      const left = Math.max(0, outline.startCol) * this.cellWidth * scale;
      const right = Math.min(terminal.cols, outline.endCol) * this.cellWidth * scale;
      if (right <= left || bottom <= top) continue;
      const thickness = scale;
      // Only draw an edge that is a real boundary of the outlined region (see
      // visibleOutlineEdges): a block taller than the screen has its top/bottom
      // off-screen, and drawing them clamped to the viewport makes the outline
      // look like a box wrapping the whole terminal.
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

    // Kitty's three z tiers, in draw order. The set is tiny (0 to a handful),
    // so it is filtered rather than assumed sorted; within a tier the caller's
    // order is kept, which is the placement store's z-then-id order.
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

  // One textured quad per image, each with its own texture. Called once per z
  // tier, so it must leave the GL state exactly as it found it — the cell and
  // outline passes run between the calls. Placements are rare (0 to a handful),
  // so a few extra tiny draw calls cost less than threading a second sampler
  // through the shader the grid renderer shares. Returns how many actually drew.
  //
  // The image pass composites with STRAIGHT alpha: the glyph pipeline is
  // premultiplied, but UNPACK_PREMULTIPLY_ALPHA_WEBGL only applies to uploads
  // from DOM elements, and these pixels arrive as a raw byte view. The blend and
  // the bound atlas are both restored before returning.
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
        // The color-glyph path passes the texel through, scaled by quad alpha:
        // exactly "draw this texture" once the alpha is 1.
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
    // The cell and outline passes sample the glyph atlas, so hand the unit back
    // before returning.
    gl.bindTexture(gl.TEXTURE_2D, this.texture);
    return drawn;
  }

  // The GPU texture for one image, uploaded on first use. Null when the blob
  // cannot be drawn — it says why rather than uploading a texture whose stride
  // is wrong, which renders as plausible garbage instead of failing.
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
    // Rows of an RGB image are 3 bytes per pixel and land on any byte boundary;
    // the default 4-byte unpack alignment would shear every odd-width image.
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
  // document fonts. Used when a web font (e.g. the bundled Nerd Font) finishes
  // loading after the first glyphs were already rasterized with a fallback face:
  // those stale bitmaps would otherwise be served from the cache forever. The
  // caller is responsible for forcing a redraw afterwards.
  invalidateGlyphCache(): void {
    this.reseedAtlas();
  }

  // Re-metric this renderer for a new font size in place, instead of tearing
  // down and reconstructing the WASM model + WebGL context on every font-size
  // change. WKWebView has a small pool of live WebGL contexts; rebuilding every
  // mounted pane (active + warm hidden) on a font change pressures that pool
  // and can permanently break panes with a lost/failed context. This does not
  // touch canvas sizing — the caller re-asserts cols/rows via resize() so
  // hidden panes' canvases stay consistent with the model without needing a
  // container measurement.
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
    // Cached row vertices carry pixel geometry built from the old cell metrics.
    // invalidateGlyphCache() happens to drop them too (a reseeded atlas moves
    // every UV), but geometry is its own reason and has to survive someone
    // making the atlas survivable.
    this.rowCacheValid = false;
    this.invalidateGlyphCache();
  }

  private configureAttribute(name: string, size: number, stride: number, offset: number): void {
    const location = this.gl.getAttribLocation(this.program, name);
    this.gl.enableVertexAttribArray(location);
    this.gl.vertexAttribPointer(location, size, this.gl.FLOAT, false, stride, offset);
  }

  // `quadsPerCell` is how many quads a cell is expected to contribute to this
  // pass, and only sets the starting capacity — the buffer grows on demand, so
  // an underestimate costs one reallocation rather than a wrong frame. The two
  // passes are estimated differently on purpose: a foreground cell draws a glyph
  // and can add a tint, an underline, or a strikethrough, while a background
  // draws at most one quad and only for a non-default color, which most rows
  // have none of. Sizing both at a full row is what doubled retained bytes.
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

  // What the row cache itself retains: zero on the direct path, so this stays
  // the legible "is the cache allocated" signal.
  private retainedRowVertexBytes(): number {
    let bytes = 0;
    for (const row of this.rowBgVertices) bytes += row?.capacityBytes ?? 0;
    for (const row of this.rowFgVertices) bytes += row?.capacityBytes ?? 0;
    return bytes;
  }

  // Everything this renderer keeps alive between paints: the retained rows plus
  // the frame buffers they concatenate into, which hold a second full-frame
  // copy of the same content. Reported, not gated — see
  // ROW_VERTEX_CACHE_MAX_BYTES — so a memory receipt covers both halves instead
  // of the rows alone.
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

  // Drop the retained rows but keep the over-budget verdict, so a grid that
  // crossed the ceiling stays on the direct path until it resizes.
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
    // Emoji clusters (ZWJ/flag/skin-tone/keycap) must be shaped Apple-Color-Emoji
    // -first or WKWebView's canvas fallback decomposes them; see terminalGlyphFont.
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
      // Atlas is full: grow it (doubling, up to the cap) so glyph-heavy sessions
      // get more room instead of thrashing; only clear-and-reuse once at the cap.
      if (this.atlasSize < MAX_ATLAS_SIZE) {
        this.growAtlas();
      } else {
        this.resetAtlas();
      }
      // growAtlas/resetAtlas resize the backing canvas, and resizing a canvas
      // resets ALL 2D context state (font included) to defaults. Re-apply the
      // font so THIS glyph -- the one that triggered the grow -- is rasterized
      // with the intended font instead of the browser default. Without this the
      // wrong bitmap is cached, and the render() retry reuses the cache so it
      // never self-corrects.
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

  // Double the atlas (up to the cap) when a glyph-heavy session fills it, then
  // re-seed at the new size. Glyph-light sessions never call this and stay at
  // INITIAL_ATLAS_SIZE.
  private growAtlas(): void {
    this.atlasSize = nextAtlasSize(this.atlasSize, MAX_ATLAS_SIZE);
    this.reseedAtlas();
  }

  // Clear the glyph cache and reuse the atlas at its current size. Only reached
  // once the atlas has already grown to the cap.
  private resetAtlas(): void {
    this.reseedAtlas();
  }

  // Clear the glyph cache and (re)initialize the backing canvas + GPU texture at
  // the current atlas size, re-seeding the solid white texel at (0,0). Setting
  // the canvas dimensions also clears its bitmap, so this covers both same-size
  // resets and post-grow reallocations. Bumps the generation so an in-flight
  // frame re-rasterizes against the fresh atlas (see render()'s retry). Resetting
  // the 2D context state here is recovered by getGlyph, which re-applies the font
  // both before measuring and again after a grow, before drawing.
  private reseedAtlas(): void {
    this.glyphs.clear();
    this.codepointGlyphs.clear();
    // Cached vertices carry UVs into this atlas. Once it moves, every row has
    // to be rebuilt before it can be concatenated into another frame.
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
    // Keep all samples within the white texel. Sampling its edges with LINEAR
    // filtering blends into transparent atlas neighbours and leaves seams. Mode
    // 0: tint the (fully-opaque) texel coverage with the quad color.
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
    // Color glyphs (emoji) carry their own colors and use mode 1 so the shader
    // passes the atlas RGBA through; monochrome glyphs use mode 0 and are tinted.
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
