import { describe, expect, it, vi } from 'vitest';
import type { GhosttyTerminal } from 'ghostty-web';
import {
  graphemeAtViewportCell,
  nextAtlasSize,
  visibleOutlineEdges,
  INITIAL_ATLAS_SIZE,
  MAX_ATLAS_SIZE,
  WebGlTerminalRenderer,
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
    measureText(text: string) {
      return { width: text.length * 8 };
    },
    clearRect() {},
    fillRect() {},
    fillText(text: string, x: number, y: number) {
      this.fillTextCalls.push({ text, x, y, font: this.font });
    },
    getImageData(_x: number, _y: number, w: number, h: number) {
      return { data: new Uint8ClampedArray(Math.max(1, w * h * 4)), width: w, height: h };
    },
  };
}

type RecordingContext = ReturnType<typeof makeRecordingContext>;

// Any property accessed as a constant (gl.TEXTURE_2D) or called as a no-op
// method resolves to a throwaway function; only the handful of calls whose
// return value the renderer actually inspects get real-ish values.
function makeFakeGl() {
  const truthy = new Set(['getShaderParameter', 'getProgramParameter']);
  const handles = new Set([
    'createShader',
    'createProgram',
    'createBuffer',
    'createTexture',
    'getUniformLocation',
  ]);
  return new Proxy(
    {},
    {
      get(_target, prop: string) {
        if (truthy.has(prop)) return () => true;
        if (handles.has(prop)) return () => ({});
        if (prop === 'getAttribLocation') return () => 0;
        return () => undefined;
      },
    },
  );
}

function makeFakeCanvas() {
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
        return makeFakeGl();
      }
      return null;
    },
    get recordingContext() {
      return ctx2d;
    },
  };
}

function makeRenderer(fontSize = 14, fontFamily = 'monospace') {
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
  try {
    const mainCanvas = makeFakeCanvas() as unknown as HTMLCanvasElement;
    renderer = new WebGlTerminalRenderer(mainCanvas, fontSize, fontFamily, {
      background: '#000000',
      foreground: '#ffffff',
      cursor: '#ffffff',
    });
  } finally {
    spy.mockRestore();
  }

  const atlasContext = created[1].recordingContext as RecordingContext;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  return { renderer: renderer as any, atlasContext };
}

function makeFakeTerminal(cols: number, rows: number) {
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
    getCursor: () => ({ x: 0, y: 0, visible: false }),
    getViewport: () => Array.from({ length: cols * rows }, cell),
    getScrollbackLength: () => 0,
    getGraphemeString: () => '',
    getScrollbackGraphemeString: () => '',
  } as unknown as GhosttyTerminal;
}

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
