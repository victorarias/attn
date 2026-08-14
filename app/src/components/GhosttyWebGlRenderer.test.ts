import { describe, expect, it, vi } from 'vitest';
import { CellFlags, type GhosttyCell, type GhosttyTerminal } from 'ghostty-web';
import { TERMINAL_FLOATS_PER_QUAD } from './terminalVertexBuffer';
import {
  graphemeAtViewportCell,
  nextAtlasSize,
  visibleOutlineEdges,
  INITIAL_ATLAS_SIZE,
  KITTY_Z_UNDER_BACKGROUND,
  MAX_ATLAS_SIZE,
  WebGlTerminalRenderer,
  type WebGlImageQuad,
} from './GhosttyWebGlRenderer';

function terminalWithHistory(history: number) {
  return {
    getScrollbackLength: () => history,
    getScrollbackGraphemeString: vi.fn((row: number, col: number) => `history:${row}:${col}`),
    getGraphemeString: vi.fn((row: number, col: number) => `live:${row}:${col}`),
  } as unknown as GhosttyTerminal;
}

describe('graphemeAtViewportCell', () => {
  it('reads graphemes from scrollback rows in a scrolled viewport', () => {
    const terminal = terminalWithHistory(5);

    expect(graphemeAtViewportCell(terminal, 0, 2, 2)).toBe('history:3:2');
    expect(terminal.getScrollbackGraphemeString).toHaveBeenCalledWith(3, 2);
  });

  it('reads live graphemes after a mixed scrolled viewport reaches the active screen', () => {
    const terminal = terminalWithHistory(5);

    expect(graphemeAtViewportCell(terminal, 2, 4, 1)).toBe('live:1:4');
    expect(terminal.getGraphemeString).toHaveBeenCalledWith(1, 4);
  });
});

// Regression for the "selected command block's box covers the whole terminal"
// bug: a block taller than the viewport has its top above row 0 and its bottom
// below the last row, so neither is a real boundary and must not be drawn.
describe('visibleOutlineEdges', () => {
  const ROWS = 24;

  it('draws both edges for a block fully inside the viewport', () => {
    expect(visibleOutlineEdges(3, 10, ROWS)).toEqual({ drawTop: true, drawBottom: true });
  });

  it('omits the top edge when the block starts above the viewport', () => {
    expect(visibleOutlineEdges(-5, 10, ROWS)).toEqual({ drawTop: false, drawBottom: true });
  });

  it('omits the bottom edge when the block ends below the viewport', () => {
    expect(visibleOutlineEdges(3, 40, ROWS)).toEqual({ drawTop: true, drawBottom: false });
  });

  it('omits both edges for a block taller than the viewport (no full-screen box)', () => {
    expect(visibleOutlineEdges(-8, 40, ROWS)).toEqual({ drawTop: false, drawBottom: false });
  });

  it('treats the last visible row as inside the viewport', () => {
    expect(visibleOutlineEdges(0, ROWS - 1, ROWS)).toEqual({ drawTop: true, drawBottom: true });
  });
});

describe('nextAtlasSize (grow-on-demand policy)', () => {
  it('starts at 1024² and doubles to the 2048² cap', () => {
    expect(INITIAL_ATLAS_SIZE).toBe(1024);
    expect(MAX_ATLAS_SIZE).toBe(2048);
    expect(nextAtlasSize(INITIAL_ATLAS_SIZE)).toBe(2048);
  });

  it('is idempotent at the cap (never grows unbounded)', () => {
    expect(nextAtlasSize(MAX_ATLAS_SIZE)).toBe(MAX_ATLAS_SIZE);
  });

  it('always converges to the cap and never exceeds it under repeated growth', () => {
    let size = INITIAL_ATLAS_SIZE;
    for (let i = 0; i < 16; i += 1) {
      const grown = nextAtlasSize(size);
      expect(grown).toBeLessThanOrEqual(MAX_ATLAS_SIZE);
      expect(grown).toBeGreaterThanOrEqual(size);
      size = grown;
    }
    expect(size).toBe(MAX_ATLAS_SIZE);
  });
});

// --- grow-path regression harness -------------------------------------------
// The renderer needs a real WebGL2 context plus a 2D canvas, neither of which
// happy-dom provides. We stub both: a no-op WebGL2 proxy, and a recording 2D
// context that faithfully reproduces the one browser behavior the bug hinges on
// -- assigning canvas.width/height resets ALL 2D context state (font included)
// back to defaults. That lets us drive the real getGlyph() grow path and assert
// the glyph that triggers a grow is rasterized with the intended font rather
// than the post-resize default.

interface FillTextCall {
  text: string;
  x: number;
  y: number;
  font: string;
}

function makeRecordingContext() {
  return {
    font: '10px sans-serif',
    fillStyle: '#000000',
    textBaseline: 'alphabetic',
    fillTextCalls: [] as FillTextCall[],
    // When set, getImageData fills every pixel with this RGBA so tests can
    // simulate a color-font raster (e.g. emoji) vs the default transparent one.
    nextPixel: null as null | { r: number; g: number; b: number; a: number },
    measureText(text: string) {
      // Scale with the current font's px size (default 14) so tests can exercise
      // cellWidth recomputation on a font-size change; existing 14px-fixture
      // tests are unaffected since the factor is 1 at the default size.
      const sizeMatch = /^(\d+(?:\.\d+)?)px/.exec(this.font);
      const size = sizeMatch ? Number.parseFloat(sizeMatch[1]) : 14;
      return { width: text.length * 8 * (size / 14) };
    },
    clearRect() {},
    fillRect() {},
    fillText(text: string, x: number, y: number) {
      this.fillTextCalls.push({ text, x, y, font: this.font });
    },
    getImageData(_x: number, _y: number, w: number, h: number) {
      const data = new Uint8ClampedArray(Math.max(1, w * h * 4));
      const px = this.nextPixel;
      if (px) {
        for (let i = 0; i < data.length; i += 4) {
          data[i] = px.r;
          data[i + 1] = px.g;
          data[i + 2] = px.b;
          data[i + 3] = px.a;
        }
      }
      return { data, width: w, height: h };
    },
  };
}

type RecordingContext = ReturnType<typeof makeRecordingContext>;

// An ordered log of the GL calls that tell the render passes apart. The glyph
// atlas texture is the first one the renderer creates, so a draw is an image
// draw exactly when a different texture is bound in front of it — which is how
// a test reads the pass order out of a frame without owning renderer internals.
interface RecordedDraw {
  kind: 'atlas' | 'image';
  floats: number;
  vertices: number;
  firstFloat: number | null;
}

function makeGlRecorder() {
  const textures: object[] = [];
  const calls: Array<
    | { op: 'bufferData'; floats: number; firstFloat: number | null }
    | { op: 'drawArrays'; vertices: number }
    | { op: 'bindTexture'; atlas: boolean }
  > = [];
  return {
    textures,
    calls,
    reset() {
      calls.length = 0;
    },
    // One entry per drawArrays, tagged with the texture bound at the time and
    // the buffer uploaded just before it.
    draws(): RecordedDraw[] {
      const draws: RecordedDraw[] = [];
      let atlasBound = true;
      let pending: { floats: number; firstFloat: number | null } = { floats: 0, firstFloat: null };
      for (const call of calls) {
        if (call.op === 'bindTexture') atlasBound = call.atlas;
        else if (call.op === 'bufferData') pending = { floats: call.floats, firstFloat: call.firstFloat };
        else {
          draws.push({
            kind: atlasBound ? 'atlas' : 'image',
            floats: pending.floats,
            vertices: call.vertices,
            firstFloat: pending.firstFloat,
          });
        }
      }
      return draws;
    },
  };
}

type GlRecorder = ReturnType<typeof makeGlRecorder>;

// Any property accessed as a constant (gl.TEXTURE_2D) or called as a no-op
// method resolves to a throwaway function; only the handful of calls whose
// return value the renderer actually inspects get real-ish values.
function makeFakeGl(recorder?: GlRecorder) {
  const truthy = new Set(['getShaderParameter', 'getProgramParameter']);
  const handles = new Set([
    'createShader',
    'createProgram',
    'createBuffer',
    'getUniformLocation',
  ]);
  return new Proxy(
    {},
    {
      get(_target, prop: string) {
        if (prop === 'createTexture') {
          return () => {
            const texture = {};
            recorder?.textures.push(texture);
            return texture;
          };
        }
        if (recorder && prop === 'bufferData') {
          return (_target2: unknown, data: Float32Array) => {
            recorder.calls.push({
              op: 'bufferData',
              floats: data.length,
              firstFloat: data.length > 0 ? data[0] : null,
            });
          };
        }
        if (recorder && prop === 'drawArrays') {
          return (_mode: unknown, _first: number, count: number) => {
            recorder.calls.push({ op: 'drawArrays', vertices: count });
          };
        }
        if (recorder && prop === 'bindTexture') {
          return (_target2: unknown, texture: object) => {
            recorder.calls.push({ op: 'bindTexture', atlas: texture === recorder.textures[0] });
          };
        }
        if (truthy.has(prop)) return () => true;
        if (handles.has(prop)) return () => ({});
        if (prop === 'getAttribLocation') return () => 0;
        return () => undefined;
      },
    },
  );
}

function makeFakeCanvas(recorder?: GlRecorder) {
  let ctx2d: RecordingContext | null = null;
  return {
    _w: 0,
    _h: 0,
    style: {} as Record<string, string>,
    get width() {
      return this._w;
    },
    set width(v: number) {
      this._w = v;
      if (ctx2d) ctx2d.font = '10px sans-serif'; // resize resets 2D context state
    },
    get height() {
      return this._h;
    },
    set height(v: number) {
      this._h = v;
      if (ctx2d) ctx2d.font = '10px sans-serif';
    },
    getContext(type: string) {
      if (type === '2d') {
        ctx2d = ctx2d ?? makeRecordingContext();
        return ctx2d;
      }
      if (type === 'webgl2') {
        return makeFakeGl(recorder);
      }
      return null;
    },
    get recordingContext() {
      return ctx2d;
    },
  };
}

function makeRenderer(fontSize = 14, fontFamily = 'monospace', recorder?: GlRecorder) {
  // Constructor creates two canvases via document.createElement: [0] metrics,
  // [1] atlas. Intercept those; the main canvas is supplied directly.
  const created: ReturnType<typeof makeFakeCanvas>[] = [];
  const realCreate = document.createElement.bind(document);
  const spy = vi.spyOn(document, 'createElement').mockImplementation(((tag: string) => {
    if (tag === 'canvas') {
      const canvas = makeFakeCanvas();
      created.push(canvas);
      return canvas;
    }
    return realCreate(tag);
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any);

  let renderer: WebGlTerminalRenderer;
  const mainCanvas = makeFakeCanvas(recorder);
  try {
    renderer = new WebGlTerminalRenderer(mainCanvas as unknown as HTMLCanvasElement, fontSize, fontFamily, {
      background: '#000000',
      foreground: '#ffffff',
      cursor: '#ffffff',
    });
  } finally {
    spy.mockRestore();
  }

  const atlasContext = created[1].recordingContext as RecordingContext;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  return { renderer: renderer as any, atlasContext, canvas: mainCanvas };
}

function makeFakeTerminal(
  cols: number,
  rows: number,
  options: {
    cell?: (row: number, col: number) => Partial<GhosttyCell>;
    cursor?: { x: number; y: number; visible: boolean };
  } = {},
) {
  const cell = () => ({
    codepoint: 0,
    grapheme_len: 0,
    width: 1,
    flags: 0,
    fg_r: 255, fg_g: 255, fg_b: 255,
    bg_r: 0, bg_g: 0, bg_b: 0,
  });
  return {
    cols,
    rows,
    update: () => 1,
    markClean: () => {},
    getCursor: () => options.cursor ?? { x: 0, y: 0, visible: false },
    getViewport: () => Array.from({ length: cols * rows }, (_unused, index) => ({
      ...cell(),
      ...(options.cell?.(Math.floor(index / cols), index % cols) ?? {}),
    })),
    getScrollbackLength: () => 0,
    getGraphemeString: () => '',
    getScrollbackGraphemeString: () => '',
  } as unknown as GhosttyTerminal;
}

// Floats one quad contributes to a vertex buffer: 6 vertices of
// position(2) + texcoord(2) + color(4) + mode(1).
const FLOATS_PER_QUAD = 6 * 9;

function makeImageQuad(z: number, x: number, imageId: number): WebGlImageQuad {
  const width = 2;
  const height = 2;
  return {
    source: {
      imageId,
      generation: 1,
      width,
      height,
      format: 'rgba',
      // byteLength has to match width*height*bytes-per-pixel or the renderer
      // refuses the blob instead of uploading a texture with a wrong stride.
      pixels: new Uint8Array(width * height * 4),
    },
    x,
    y: 0,
    width: 8,
    height: 8,
    sourceX: 0,
    sourceY: 0,
    sourceWidth: width,
    sourceHeight: height,
    z,
  };
}

// Kitty's three z layers, read off the frame's draw order. Each case names the
// bug it catches; all of them are invisible to a quad-count assertion, because
// the same quads are drawn either way — only the order differs.
describe('WebGlTerminalRenderer kitty z layering', () => {
  it('draws a z<0 image between the cell backgrounds and the text', () => {
    const recorder = makeGlRecorder();
    const { renderer } = makeRenderer(14, 'monospace', recorder);
    // One glyph cell and one cell with a non-default background, so both cell
    // passes carry exactly one quad and the image has to land between them.
    const terminal = makeFakeTerminal(2, 1, {
      cell: (_row, col) => (col === 0 ? { codepoint: 65 } : { bg_r: 40, bg_g: 40, bg_b: 40 }),
    });
    renderer.resize(2, 1);
    recorder.reset();

    renderer.render(terminal, true, undefined, undefined, 0, [makeImageQuad(-1, 7, 1)]);

    const draws = recorder.draws();
    expect(draws.map((draw) => draw.kind)).toEqual(['atlas', 'image', 'atlas']);
    expect(draws[0].floats).toBe(FLOATS_PER_QUAD); // background pass: the colored cell
    expect(draws[2].floats).toBe(FLOATS_PER_QUAD); // foreground pass: the glyph
  });

  it('splits the two deepest layers at the spec constant, exclusive', () => {
    const recorder = makeGlRecorder();
    const { renderer } = makeRenderer(14, 'monospace', recorder);
    const terminal = makeFakeTerminal(1, 1);
    renderer.resize(1, 1);
    recorder.reset();

    // Distinct x per quad so the two image draws are told apart by their
    // vertex data rather than by the order under test.
    renderer.render(terminal, true, undefined, undefined, 0, [
      makeImageQuad(KITTY_Z_UNDER_BACKGROUND - 1, 11, 1),
      makeImageQuad(KITTY_Z_UNDER_BACKGROUND, 22, 2),
    ]);

    const draws = recorder.draws();
    expect(draws.map((draw) => draw.kind)).toEqual(['image', 'atlas', 'image', 'atlas']);
    // Only the one strictly BELOW the constant goes under the backgrounds; the
    // one at it draws over them, like any other negative z.
    expect(draws[0].firstFloat).toBe(11 * renderer.dpr);
    expect(draws[2].firstFloat).toBe(22 * renderer.dpr);
  });

  it('keeps a z>=0 image above both cell passes', () => {
    const recorder = makeGlRecorder();
    const { renderer } = makeRenderer(14, 'monospace', recorder);
    const terminal = makeFakeTerminal(1, 1);
    renderer.resize(1, 1);
    recorder.reset();

    renderer.render(terminal, true, undefined, undefined, 0, [makeImageQuad(0, 0, 1)]);

    expect(recorder.draws().map((draw) => draw.kind)).toEqual(['atlas', 'atlas', 'image']);
  });

  it('draws the cursor after an under-text image covering its cell', () => {
    const recorder = makeGlRecorder();
    const { renderer } = makeRenderer(14, 'monospace', recorder);
    const terminal = makeFakeTerminal(1, 1, { cursor: { x: 0, y: 0, visible: true } });
    renderer.resize(1, 1);
    recorder.reset();

    renderer.render(terminal, true, undefined, undefined, 0, [makeImageQuad(-1, 0, 1)]);

    const draws = recorder.draws();
    expect(draws.map((draw) => draw.kind)).toEqual(['atlas', 'image', 'atlas']);
    // The cursor block is a foreground quad, not a cell background: in the
    // background buffer it would be painted over by the image and disappear.
    expect(draws[0].floats).toBe(0);
    expect(draws[2].floats).toBe(FLOATS_PER_QUAD);
  });

  it('draws a selection tint after an under-text image covering the selection', () => {
    const recorder = makeGlRecorder();
    const { renderer } = makeRenderer(14, 'monospace', recorder);
    const terminal = makeFakeTerminal(1, 1);
    renderer.resize(1, 1);
    recorder.reset();

    renderer.render(
      terminal,
      true,
      undefined,
      [{ startRow: 0, startCol: 0, endRow: 0, endCol: 1, color: '#3366ff', alpha: 0.4, kind: 'background' }],
      0,
      [makeImageQuad(-1, 0, 1)],
    );

    const draws = recorder.draws();
    expect(draws.map((draw) => draw.kind)).toEqual(['atlas', 'image', 'atlas']);
    // The tint is an overlay, not a cell background — it has to survive on top
    // of the image the selection was dragged across.
    expect(draws[0].floats).toBe(0);
    expect(draws[2].floats).toBe(FLOATS_PER_QUAD);
  });
});

// The row cache keeps a buffer per row per cell pass. This fixture's cells all
// carry the default background, so every foreground row fills to one quad per
// column while every background row stays at the one-quad minimum it was
// allocated with.
const QUAD_BYTES = TERMINAL_FLOATS_PER_QUAD * Float32Array.BYTES_PER_ELEMENT;
const ROW_FG_BYTES = 50 * 50 * QUAD_BYTES;
const ROW_BG_MIN_BYTES = 50 * QUAD_BYTES;

function makeControllableTerminal(cols: number, rows: number) {
  const cells = Array.from({ length: cols * rows }, () => ({
    codepoint: 65,
    grapheme_len: 0,
    width: 1,
    flags: 0,
    fg_r: 255, fg_g: 255, fg_b: 255,
    bg_r: 0, bg_g: 0, bg_b: 0,
  }));
  const state = {
    dirty: 2,
    rows: new Set(Array.from({ length: rows }, (_, row) => row)),
    cursor: { x: 0, y: 0, visible: false },
  };
  const markClean = vi.fn();
  const terminal = {
    cols,
    rows,
    update: () => state.dirty,
    isRowDirty: (row: number) => state.rows.has(row),
    markClean,
    getCursor: () => state.cursor,
    getViewport: () => cells,
    getScrollbackLength: () => 0,
    getGraphemeString: () => '',
    getScrollbackGraphemeString: () => '',
  } as unknown as GhosttyTerminal;
  return { terminal, cells, state, markClean };
}

// A pane hidden behind another session holds two window-sized GPU surfaces for
// a frame nobody can see. Release hands them back without touching the context,
// the glyph atlas, or the model — so the only thing a reveal owes is a repaint.
describe('WebGlTerminalRenderer off-screen drawing buffer', () => {
  it('hands the drawing buffer back and takes it again at the same geometry', () => {
    const { renderer, canvas } = makeRenderer();
    renderer.resize(50, 40);
    const { width, height } = canvas;
    expect(width).toBeGreaterThan(1);

    renderer.releaseDrawingBuffer();
    expect({ width: canvas.width, height: canvas.height }).toEqual({ width: 1, height: 1 });

    renderer.restoreDrawingBuffer();
    expect({ width: canvas.width, height: canvas.height }).toEqual({ width, height });
  });

  it('records a resize that lands while released and allocates at that geometry on restore', () => {
    const { renderer, canvas } = makeRenderer();
    renderer.resize(50, 40);
    renderer.releaseDrawingBuffer();

    renderer.resize(20, 10);
    expect({ width: canvas.width, height: canvas.height }).toEqual({ width: 1, height: 1 });

    renderer.restoreDrawingBuffer();
    expect(canvas.width).toBe(20 * renderer.cellWidth * renderer.dpr);
    expect(canvas.height).toBe(10 * renderer.cellHeight * renderer.dpr);
  });

  it('repaints the whole grid on the first frame after a restore', () => {
    const { renderer } = makeRenderer();
    const { terminal, state } = makeControllableTerminal(50, 50);
    renderer.resize(50, 50);
    renderer.render(terminal);

    state.dirty = 1;
    state.rows = new Set([25]);
    expect(renderer.render(terminal)).toMatchObject({ fullPaint: false });

    renderer.releaseDrawingBuffer();
    renderer.restoreDrawingBuffer();

    state.dirty = 1;
    state.rows = new Set([25]);
    expect(renderer.render(terminal)).toMatchObject({ fullPaint: true, paintedRows: 50 });
  });

  it('ignores a repeated release and a restore that was never released', () => {
    const { renderer, canvas } = makeRenderer();
    renderer.resize(50, 40);
    const { width, height } = canvas;

    renderer.restoreDrawingBuffer();
    expect({ width: canvas.width, height: canvas.height }).toEqual({ width, height });

    renderer.releaseDrawingBuffer();
    renderer.releaseDrawingBuffer();
    renderer.restoreDrawingBuffer();
    expect({ width: canvas.width, height: canvas.height }).toEqual({ width, height });
  });
});

describe('WebGlTerminalRenderer dirty rows', () => {
  it('rebuilds small grids directly instead of paying to copy a row cache', () => {
    const { renderer } = makeRenderer();
    const { terminal, state } = makeControllableTerminal(4, 5);
    renderer.resize(4, 5);
    renderer.render(terminal);

    state.dirty = 1;
    state.rows = new Set([2]);

    expect(renderer.render(terminal)).toMatchObject({
      fullPaint: true,
      paintedRows: 5,
      retainedRowVertexBytes: 0,
    });
  });

  it('rebuilds only a dirty row and its neighbors while submitting a complete cached frame', () => {
    const { renderer } = makeRenderer();
    const { terminal, cells, state, markClean } = makeControllableTerminal(50, 50);
    renderer.resize(50, 50);

    const initial = renderer.render(terminal);
    expect(initial).toMatchObject({
      fullPaint: true,
      paintedRows: 50,
      submittedQuads: 2500,
      retainedRowVertexBytes: ROW_FG_BYTES + ROW_BG_MIN_BYTES,
      modelPrintable: 2500,
      quads: 2500,
    });

    // Clear the middle row. Ghostty marks that row dirty; the renderer expands
    // by one row on either side for cross-row grapheme pixels.
    for (let col = 0; col < 50; col += 1) cells[25 * 50 + col].codepoint = 0;
    state.dirty = 1;
    state.rows = new Set([25]);
    const partial = renderer.render(terminal);

    expect(partial).toMatchObject({
      fullPaint: false,
      paintedRows: 3,
      paintedCells: 150,
      submittedQuads: 2450,
      retainedRowVertexBytes: ROW_FG_BYTES + ROW_BG_MIN_BYTES,
      modelPrintable: 2450,
      quads: 2450,
    });
    expect(markClean).toHaveBeenCalledTimes(2);
  });

  it('keeps overlays on the full-frame path even when Ghostty is partially dirty', () => {
    const { renderer } = makeRenderer();
    const { terminal, state } = makeControllableTerminal(50, 50);
    renderer.resize(50, 50);
    renderer.render(terminal);

    state.dirty = 1;
    state.rows = new Set([25]);
    const sample = renderer.render(terminal, false, undefined, [
      { startRow: 25, startCol: 0, endRow: 25, endCol: 1, color: '#ffffff', kind: 'underline' },
    ]);

    expect(sample).toMatchObject({ fullPaint: true, paintedRows: 50 });

    // Removing an overlay also changes pixels outside the model's dirty set.
    // One full frame retires that composited surface before partial paints resume.
    expect(renderer.render(terminal)).toMatchObject({ fullPaint: true, paintedRows: 50 });
    expect(renderer.render(terminal)).toMatchObject({ fullPaint: false, paintedRows: 3 });
  });

  // Defensive rather than observed: the real WASM reports a cross-row move as
  // PARTIAL with *both* rows already in the set. This pins the row selection
  // against a model that says PARTIAL and names no rows at all. The truly bare
  // moves — the ones the WASM reports as DIRTY_NONE — are covered further down.
  it('repaints both cursor rows when a partial frame names no dirty rows', () => {
    const { renderer } = makeRenderer();
    const { terminal, state } = makeControllableTerminal(50, 50);
    renderer.resize(50, 50);
    state.cursor = { x: 0, y: 10, visible: true };
    renderer.render(terminal);

    state.dirty = 1;
    state.rows = new Set();
    state.cursor = { x: 0, y: 30, visible: true };

    // Rows 9-11 (vacated) and 29-31 (arrived), each expanded by one neighbor.
    expect(renderer.render(terminal)).toMatchObject({ fullPaint: false, paintedRows: 6 });
  });

  // The regressing case: Ghostty reports DIRTY_NONE for a move within one row,
  // so such a move only reaches the renderer riding along with an unrelated
  // dirty row. The cursor's own row is absent from that set.
  it('repaints the cursor row on a same-row move that rides along with another dirty row', () => {
    const { renderer } = makeRenderer();
    const { terminal, state } = makeControllableTerminal(50, 50);
    renderer.resize(50, 50);
    state.cursor = { x: 0, y: 10, visible: true };
    renderer.render(terminal);

    state.dirty = 1;
    state.rows = new Set([40]);
    state.cursor = { x: 7, y: 10, visible: true };

    // Rows 39-41 for the dirty row, 9-11 for the cursor's unmarked row.
    expect(renderer.render(terminal)).toMatchObject({ fullPaint: false, paintedRows: 6 });
  });

  it('repaints the row a hidden cursor was drawn on', () => {
    const { renderer } = makeRenderer();
    const { terminal, state } = makeControllableTerminal(50, 50);
    renderer.resize(50, 50);
    state.cursor = { x: 0, y: 10, visible: true };
    renderer.render(terminal);

    state.dirty = 1;
    state.rows = new Set([40]);
    state.cursor = { x: 0, y: 10, visible: false };

    expect(renderer.render(terminal)).toMatchObject({ fullPaint: false, paintedRows: 6 });
  });

  // A same-row move and a visibility toggle report DIRTY_NONE, so nothing else
  // wakes the renderer. Without a cursor comparison ahead of the early return,
  // the old inverted cell survives until unrelated output happens to arrive.
  it('paints a bare same-row cursor move that the model reports as not dirty', () => {
    const { renderer } = makeRenderer();
    const { terminal, state } = makeControllableTerminal(50, 50);
    renderer.resize(50, 50);
    state.cursor = { x: 0, y: 10, visible: true };
    renderer.render(terminal);

    state.dirty = 0;
    state.rows = new Set();
    state.cursor = { x: 7, y: 10, visible: true };

    // Rows 9-11: the cursor vacated and arrived on the same row. Partial, not
    // full — a cursor move must not cost a whole-grid repaint.
    expect(renderer.render(terminal)).toMatchObject({ fullPaint: false, paintedRows: 3 });
  });

  it('paints a bare cursor visibility toggle that the model reports as not dirty', () => {
    const { renderer } = makeRenderer();
    const { terminal, state } = makeControllableTerminal(50, 50);
    renderer.resize(50, 50);
    state.cursor = { x: 4, y: 10, visible: true };
    renderer.render(terminal);

    state.dirty = 0;
    state.rows = new Set();
    state.cursor = { x: 4, y: 10, visible: false };

    expect(renderer.render(terminal)).toMatchObject({ fullPaint: false, paintedRows: 3 });
  });

  it('still returns null when nothing changed and the cursor stayed put', () => {
    const { renderer } = makeRenderer();
    const { terminal, state } = makeControllableTerminal(50, 50);
    renderer.resize(50, 50);
    state.cursor = { x: 4, y: 10, visible: true };
    renderer.render(terminal);

    state.dirty = 0;
    state.rows = new Set();

    expect(renderer.render(terminal)).toBeNull();
  });

  // A hidden cursor paints nothing, so moving it changes no pixels. Treating
  // that as a render reason is worse than missing it: the frame has no row to
  // mark, so the zero-row guard escalates it to a full-grid paint — on the one
  // path (a TUI redrawing with the cursor hidden) that must stay cheap.
  it('does not paint at all when a hidden cursor moves with nothing else dirty', () => {
    const { renderer } = makeRenderer();
    const { terminal, state } = makeControllableTerminal(50, 50);
    renderer.resize(50, 50);
    state.cursor = { x: 4, y: 10, visible: false };
    renderer.render(terminal);

    state.dirty = 0;
    state.rows = new Set();
    state.cursor = { x: 31, y: 10, visible: false };

    expect(renderer.render(terminal)).toBeNull();
  });

  it('does not paint when a hidden cursor changes row with nothing else dirty', () => {
    const { renderer } = makeRenderer();
    const { terminal, state } = makeControllableTerminal(50, 50);
    renderer.resize(50, 50);
    state.cursor = { x: 4, y: 10, visible: false };
    renderer.render(terminal);

    state.dirty = 0;
    state.rows = new Set();
    state.cursor = { x: 4, y: 44, visible: false };

    expect(renderer.render(terminal)).toBeNull();
  });

  it('paints nothing extra when a hidden cursor moves', () => {
    const { renderer } = makeRenderer();
    const { terminal, state } = makeControllableTerminal(50, 50);
    renderer.resize(50, 50);
    renderer.render(terminal);

    state.dirty = 1;
    state.rows = new Set([25]);
    state.cursor = { x: 40, y: 40, visible: false };

    expect(renderer.render(terminal)).toMatchObject({ fullPaint: false, paintedRows: 3 });
  });

  it('releases a row cache that grows beyond the two MiB guardrail', () => {
    const { renderer } = makeRenderer();
    const { terminal, cells, state } = makeControllableTerminal(100, 30);
    for (const cell of cells) {
      cell.bg_r = 64;
      cell.flags = CellFlags.UNDERLINE | CellFlags.STRIKETHROUGH;
    }
    renderer.resize(100, 30);

    const built = renderer.render(terminal);
    expect(built).toMatchObject({
      fullPaint: true,
      retainedRowVertexBytes: 0,
      modelPrintable: 3000,
    });

    state.dirty = 1;
    state.rows = new Set([15]);
    const afterRelease = renderer.render(terminal);
    expect(afterRelease).toMatchObject({
      fullPaint: true,
      paintedRows: 30,
      retainedRowVertexBytes: 0,
    });
    // The frame buffer is what a direct-path paint stages regardless, so the
    // reported total stays non-zero after the cache is gone.
    expect(afterRelease!.retainedRowVertexBytes).toBe(0);
    expect(afterRelease!.retainedStagingBytes).toBeGreaterThan(0);
  });
});

describe('WebGlTerminalRenderer overlays', () => {
  it('emits one background quad per covered cell with full-width middle rows', () => {
    const { renderer } = makeRenderer();
    const terminal = makeFakeTerminal(4, 3);
    renderer.resize(4, 3);

    const sample = renderer.render(terminal, true, undefined, [
      // row 0: cols 1..4 (3 cells), row 1: full width (4), row 2: cols 0..2 (2)
      { startRow: 0, startCol: 1, endRow: 2, endCol: 2, color: '#3366ff', kind: 'background' },
    ]);
    expect(sample?.quads).toBe(9);
  });

  it('emits underline quads only on covered columns and outline as four border quads', () => {
    const { renderer } = makeRenderer();
    const terminal = makeFakeTerminal(4, 3);
    renderer.resize(4, 3);

    const underlined = renderer.render(terminal, true, undefined, [
      { startRow: 1, startCol: 0, endRow: 1, endCol: 3, color: '#ffffff', kind: 'underline' },
    ]);
    expect(underlined?.quads).toBe(3);

    const outlined = renderer.render(terminal, true, undefined, [
      { startRow: 0, startCol: 0, endRow: 2, endCol: 4, color: '#ffffff', kind: 'outline' },
    ]);
    expect(outlined?.quads).toBe(4);
  });

  it('clamps overlays that extend past the viewport and renders nothing for empty ranges', () => {
    const { renderer } = makeRenderer();
    const terminal = makeFakeTerminal(4, 2);
    renderer.resize(4, 2);

    const clamped = renderer.render(terminal, true, undefined, [
      // rows -3..9 clamp to 0..1 (full grid: 8 cells)
      { startRow: -3, startCol: 0, endRow: 9, endCol: 4, color: '#3366ff', kind: 'background' },
      { startRow: 0, startCol: 2, endRow: 0, endCol: 2, color: '#3366ff', kind: 'background' },
    ]);
    expect(clamped?.quads).toBe(8);
  });
});

describe('WebGlTerminalRenderer color glyphs', () => {
  it('flags chromatic rasters as color glyphs and neutral ones as tinted', () => {
    const { renderer, atlasContext } = makeRenderer();

    // Default raster is fully transparent -> a monochrome/tinted glyph.
    expect(renderer.getGlyph('A', 0).colored).toBe(false);

    // A color font ignores the white fill and paints its own colors: chromatic
    // opaque pixels mark the glyph as one to draw directly instead of tinting.
    atlasContext.nextPixel = { r: 240, g: 40, b: 30, a: 255 };
    expect(renderer.getGlyph('🔥', 0).colored).toBe(true);

    // A white coverage glyph (r === g === b) stays tinted even when opaque.
    atlasContext.nextPixel = { r: 255, g: 255, b: 255, a: 255 };
    expect(renderer.getGlyph('B', 0).colored).toBe(false);
  });
});

describe('WebGlTerminalRenderer glyph cache invalidation', () => {
  it('re-rasterizes a cached glyph after invalidateGlyphCache (font finished loading)', () => {
    const { renderer, atlasContext } = makeRenderer(14, 'monospace');

    // First request rasterizes; a second identical request is served from cache.
    renderer.getGlyph('', 0);
    const afterFirst = atlasContext.fillTextCalls.length;
    renderer.getGlyph('', 0);
    expect(atlasContext.fillTextCalls.length).toBe(afterFirst);

    // After invalidation the same glyph must be drawn again so a now-loaded web
    // font replaces the stale fallback raster instead of being served forever.
    renderer.invalidateGlyphCache();
    renderer.getGlyph('', 0);
    expect(atlasContext.fillTextCalls.length).toBe(afterFirst + 1);
  });
});

// setFontSize() must re-metric an existing renderer in place (no rebuild) so a
// font-size change doesn't tear down every mounted pane's WASM model/WebGL
// context. document.createElement is re-mocked around the call because
// setFontSize creates its own metrics canvas, just like the constructor does.
describe('WebGlTerminalRenderer.setFontSize', () => {
  function withMockedCanvas<T>(fn: () => T): T {
    const realCreate = document.createElement.bind(document);
    const spy = vi.spyOn(document, 'createElement').mockImplementation(((tag: string) => {
      if (tag === 'canvas') return makeFakeCanvas();
      return realCreate(tag);
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    }) as any);
    try {
      return fn();
    } finally {
      spy.mockRestore();
    }
  }

  it('grows cell metrics for a larger font size and invalidates the glyph cache', () => {
    const { renderer, atlasContext } = makeRenderer(14, 'monospace');
    const smallWidth = renderer.cellWidth;
    const smallHeight = renderer.cellHeight;
    const smallBaseline = renderer.baseline;

    renderer.getGlyph('A', 0);
    const rasterizedBeforeResize = atlasContext.fillTextCalls.length;
    renderer.getGlyph('A', 0);
    expect(atlasContext.fillTextCalls.length).toBe(rasterizedBeforeResize); // served from cache

    withMockedCanvas(() => renderer.setFontSize(28));

    expect(renderer.cellWidth).toBeGreaterThan(smallWidth);
    expect(renderer.cellHeight).toBeGreaterThan(smallHeight);
    expect(renderer.baseline).toBeGreaterThan(smallBaseline);

    // The pre-resize glyph must not be served from a stale cache at the new size.
    renderer.getGlyph('A', 0);
    expect(atlasContext.fillTextCalls.length).toBe(rasterizedBeforeResize + 1);
  });

  it('resizes the canvas to the new cell metrics even when cols/rows are unchanged', () => {
    const { renderer } = makeRenderer(14, 'monospace');
    const canvas = renderer.canvas as ReturnType<typeof makeFakeCanvas>;
    renderer.resize(80, 24);
    const widthBefore = canvas.width;
    const heightBefore = canvas.height;

    withMockedCanvas(() => renderer.setFontSize(28));
    // Same grid dimensions as before the font-size change.
    renderer.resize(80, 24);

    expect(canvas.width).toBeGreaterThan(widthBefore);
    expect(canvas.height).toBeGreaterThan(heightBefore);
  });
});

describe('WebGlTerminalRenderer glyph atlas grow path', () => {
  it('keeps the intended font on the glyph that triggers a grow and an at-cap reset', () => {
    const { renderer, atlasContext } = makeRenderer(14, 'monospace');
    const intendedFont = `${14 * renderer.dpr}px monospace`;

    // A normal (non-grow) glyph draws with the intended font.
    renderer.getGlyph('A', 0);
    const normalDraw = atlasContext.fillTextCalls[atlasContext.fillTextCalls.length - 1];
    expect(normalDraw?.text).toBe('A');
    expect(normalDraw?.font).toBe(intendedFont);

    // Force a vertical overflow so the next glyph triggers a real 1024->2048 grow.
    expect(renderer.atlasSize).toBe(INITIAL_ATLAS_SIZE);
    renderer.atlasY = INITIAL_ATLAS_SIZE;
    atlasContext.fillTextCalls.length = 0;

    renderer.getGlyph('B', 0);

    // The grow happened...
    expect(renderer.atlasSize).toBe(MAX_ATLAS_SIZE);
    // ...and the glyph that triggered it was drawn with the intended font, NOT
    // the '10px sans-serif' that resizing the backing canvas reset it to. This
    // is the regression: a cached glyph drawn with the default font survives the
    // render() retry forever.
    const growDraw = atlasContext.fillTextCalls[atlasContext.fillTextCalls.length - 1];
    expect(growDraw?.text).toBe('B');
    expect(growDraw?.font).toBe(intendedFont);
    expect(growDraw?.font).not.toBe('10px sans-serif');

    // At the cap, the next overflow takes the resetAtlas() branch (clear & reuse
    // at 2048) instead of growing. It resizes the backing canvas the same way,
    // so the font has to survive that path too.
    renderer.atlasY = MAX_ATLAS_SIZE;
    atlasContext.fillTextCalls.length = 0;
    renderer.getGlyph('C', 0);
    expect(renderer.atlasSize).toBe(MAX_ATLAS_SIZE); // capped: reset, not grown
    const resetDraw = atlasContext.fillTextCalls[atlasContext.fillTextCalls.length - 1];
    expect(resetDraw?.text).toBe('C');
    expect(resetDraw?.font).toBe(intendedFont);
  });
});
