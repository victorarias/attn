// The unified renderer: ONE WebGL2 context, ONE 2048² glyph atlas, ONE vertex
// buffer, ONE drawArrays for the WHOLE grid, regardless of tile count. This is
// possible because ghostty-web's model is a pure VT state machine: we read each
// tile's cells via getViewport() and composite them into sub-rectangles of a
// single shared canvas.
//
// Derived from GhosttyWebGlRenderer.ts (shader, atlas, glyph, block-element logic
// reused verbatim) with two deliberate changes:
//   1. Per-tile scale+translate baked into a_position on the CPU (transform-in-
//      vertex), so the unchanged shader maps everything to one canvas.
//   2. The HOT PATH writes floats directly into a preallocated Float32Array via
//      an index cursor — NOT `number[].push(...)` + `new Float32Array(...)`,
//      which benchmarked at ~57ms/frame for 25 tiles (hard jank).
import { CellFlags, type GhosttyCell, type GhosttyTerminal } from 'ghostty-web';
import {
  GLYPH_MODE_COLOR,
  GLYPH_MODE_TINT,
  TERMINAL_GLYPH_FRAGMENT_SHADER,
  TERMINAL_GLYPH_VERTEX_SHADER,
  isColorGlyphBitmap,
} from '../terminalGlyphProgram';
import { terminalGlyphFont } from '../terminalGlyphFont';
import type { UISessionState } from '../../types/sessionState';
import type {
  GridRenderer,
  GridRenderStats,
  TileFrame,
  TileModel,
} from './GridRenderer';
import type { CellMetrics } from './gridConfig';

interface Rgb {
  r: number;
  g: number;
  b: number;
}

interface AtlasGlyph {
  u0: number;
  v0: number;
  u1: number;
  v1: number;
  width: number;
  height: number;
  // True when the rasterized bitmap carries its own colors (a color font such as
  // Apple Color Emoji): drawn directly (mode 1) instead of tinted with the cell
  // foreground.
  colored: boolean;
}

interface BlockRect {
  x: number;
  y: number;
  width: number;
  height: number;
}

const ATLAS_SIZE = 2048;
// position(2) + texcoord(2) + color(4) + mode(1); see terminalGlyphProgram.
const FLOATS_PER_VERTEX = 9;
const FLOATS_PER_QUAD = FLOATS_PER_VERTEX * 6;
const SOLID_TEXEL_CENTER = 0.5 / ATLAS_SIZE;
// Blue accent for the focused (zoomed/input-target) tile, so it is obvious which
// tile keyboard input is going to. Distinct from the semantic session state.
const FOCUS: Rgb = { r: 96, g: 165, b: 250 };
const STATE_COLORS: Record<UISessionState, Rgb> = {
  launching: { r: 96, g: 165, b: 250 },
  working: { r: 34, g: 197, b: 94 },
  waiting_input: { r: 245, g: 158, b: 11 },
  idle: { r: 107, g: 114, b: 128 },
  recoverable: { r: 107, g: 114, b: 128 },
  pending_approval: { r: 234, g: 179, b: 8 },
  // sky blue — calm and distinct from launching's periwinkle, the royal-blue
  // PR/focus accent, and the unknown purple.
  scheduled: { r: 14, g: 165, b: 233 },
  unknown: { r: 168, g: 85, b: 247 },
};
const FOCUS_BORDER_ALPHA = 0.95;
const WAITING_INPUT_FLASH_PERIOD_MS = 1_600;
const SCHEDULED_PULSE_PERIOD_MS = 3_200;

export function waitingInputFlash(now: number): number {
  const wave = 0.5 + 0.5 * Math.sin((now / WAITING_INPUT_FLASH_PERIOD_MS) * Math.PI * 2);
  return wave * wave;
}

// scheduledPulse is a gentle breathing wave in [0,1], slower and softer than
// the waiting_input flash — it reads as "parked but alive, will auto-resume"
// rather than "needs you now".
export function scheduledPulse(now: number): number {
  return 0.5 + 0.5 * Math.sin((now / SCHEDULED_PULSE_PERIOD_MS) * Math.PI * 2);
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

function parseColor(value: string): Rgb {
  const normalized = value.replace('#', '');
  return {
    r: Number.parseInt(normalized.slice(0, 2), 16),
    g: Number.parseInt(normalized.slice(2, 4), 16),
    b: Number.parseInt(normalized.slice(4, 6), 16),
  };
}

function compileShader(gl: WebGL2RenderingContext, type: number, source: string): WebGLShader {
  const shader = gl.createShader(type);
  if (!shader) throw new Error('grid: unable to allocate shader');
  gl.shaderSource(shader, source);
  gl.compileShader(shader);
  if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS)) {
    const message = gl.getShaderInfoLog(shader) ?? 'shader compile error';
    gl.deleteShader(shader);
    throw new Error(message);
  }
  return shader;
}

function createProgram(gl: WebGL2RenderingContext): WebGLProgram {
  const vertexShader = compileShader(gl, gl.VERTEX_SHADER, TERMINAL_GLYPH_VERTEX_SHADER);
  const fragmentShader = compileShader(gl, gl.FRAGMENT_SHADER, TERMINAL_GLYPH_FRAGMENT_SHADER);
  const program = gl.createProgram();
  if (!program) throw new Error('grid: unable to allocate program');
  gl.attachShader(program, vertexShader);
  gl.attachShader(program, fragmentShader);
  gl.linkProgram(program);
  gl.deleteShader(vertexShader);
  gl.deleteShader(fragmentShader);
  if (!gl.getProgramParameter(program, gl.LINK_STATUS)) {
    const message = gl.getProgramInfoLog(program) ?? 'shader link error';
    gl.deleteProgram(program);
    throw new Error(message);
  }
  return program;
}

export class UnifiedGridRenderer implements GridRenderer {
  readonly name = 'unified';

  private readonly fontSize: number;
  private readonly fontFamily: string;
  private readonly metrics: CellMetrics;
  private readonly theme: { background: string; foreground: string; cursor: string };
  private readonly mipmaps: boolean;

  private container: HTMLElement | null = null;
  private canvas: HTMLCanvasElement | null = null;
  private gl: WebGL2RenderingContext | null = null;
  private program: WebGLProgram | null = null;
  private buffer: WebGLBuffer | null = null;
  private texture: WebGLTexture | null = null;
  private atlas: HTMLCanvasElement | null = null;
  private atlasContext: CanvasRenderingContext2D | null = null;
  private uResolution: WebGLUniformLocation | null = null;

  private readonly glyphs = new Map<string, AtlasGlyph>();
  private dpr = 1;
  private atlasX = 2;
  private atlasY = 1;
  private atlasRowHeight = 0;
  private atlasGeneration = 0;

  private models = new Map<string, GhosttyTerminal>();

  // The single shared vertex scratch. Grown (rarely) on demand; reused forever.
  private scratch = new Float32Array(1 << 18);
  private p = 0;
  private quads = 0;
  private glyphUploads = 0;
  private atlasResets = 0;
  private canvasW = 0;
  private canvasH = 0;

  constructor(
    fontSize: number,
    fontFamily: string,
    metrics: CellMetrics,
    theme: { background: string; foreground: string; cursor: string },
    options: { mipmaps?: boolean } = {},
  ) {
    this.fontSize = fontSize;
    this.fontFamily = fontFamily;
    this.metrics = metrics;
    this.theme = theme;
    this.mipmaps = options.mipmaps ?? false;
  }

  mount(container: HTMLElement): void {
    this.container = container;
    this.dpr = Math.max(window.devicePixelRatio || 1, 1);

    const canvas = document.createElement('canvas');
    canvas.style.position = 'absolute';
    canvas.style.inset = '0';
    canvas.style.width = '100%';
    canvas.style.height = '100%';
    container.appendChild(canvas);
    this.canvas = canvas;

    // The single context. If this class ever creates more than one, the whole
    // point of grid mode (composite N terminals into 1 GPU context) is void — so
    // it is created exactly here, once.
    const gl = canvas.getContext('webgl2', { alpha: false, antialias: false });
    if (!gl) throw new Error('grid: WebGL2 unavailable');
    this.gl = gl;

    this.program = createProgram(gl);
    this.buffer = gl.createBuffer();
    this.texture = gl.createTexture();

    this.atlas = document.createElement('canvas');
    this.atlas.width = ATLAS_SIZE;
    this.atlas.height = ATLAS_SIZE;
    const atlasContext = this.atlas.getContext('2d', { willReadFrequently: true });
    if (!atlasContext) throw new Error('grid: unable to allocate glyph atlas');
    this.atlasContext = atlasContext;
    atlasContext.fillStyle = '#ffffff';
    atlasContext.fillRect(0, 0, 1, 1);

    // Premultiply alpha on upload so color-glyph (emoji) bitmaps filter cleanly;
    // coverage-only glyphs are unaffected (the tinted path reads only the alpha).
    // See terminalGlyphProgram for the shared pipeline contract.
    gl.pixelStorei(gl.UNPACK_PREMULTIPLY_ALPHA_WEBGL, true);
    const minFilter = this.mipmaps ? gl.LINEAR_MIPMAP_LINEAR : gl.LINEAR;
    gl.bindTexture(gl.TEXTURE_2D, this.texture);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, minFilter);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, ATLAS_SIZE, ATLAS_SIZE, 0, gl.RGBA, gl.UNSIGNED_BYTE, this.atlas!);
    if (this.mipmaps) gl.generateMipmap(gl.TEXTURE_2D);

    gl.useProgram(this.program);
    gl.bindBuffer(gl.ARRAY_BUFFER, this.buffer);
    const stride = FLOATS_PER_VERTEX * Float32Array.BYTES_PER_ELEMENT;
    this.configureAttribute('a_position', 2, stride, 0);
    this.configureAttribute('a_texcoord', 2, stride, 2 * Float32Array.BYTES_PER_ELEMENT);
    this.configureAttribute('a_color', 4, stride, 4 * Float32Array.BYTES_PER_ELEMENT);
    this.configureAttribute('a_mode', 1, stride, 8 * Float32Array.BYTES_PER_ELEMENT);
    gl.uniform1i(gl.getUniformLocation(this.program, 'u_atlas'), 0);
    this.uResolution = gl.getUniformLocation(this.program, 'u_resolution');
    gl.enable(gl.BLEND);
    // Premultiplied-alpha blending (source factor ONE): identical on-screen result
    // to the old SRC_ALPHA blend for tinted quads, and lets color glyphs composite
    // without dark edge fringing.
    gl.blendFunc(gl.ONE, gl.ONE_MINUS_SRC_ALPHA);
  }

  setTiles(tiles: TileModel[]): void {
    this.models = new Map(tiles.map((t) => [t.id, t.model]));
  }

  frame(frames: TileFrame[], now: number): GridRenderStats {
    const gl = this.gl;
    const canvas = this.canvas;
    const container = this.container;
    if (!gl || !canvas || !container || gl.isContextLost()) {
      return { drawCalls: 0, quads: 0, atlasUploads: 0, atlasResets: 0, liveContexts: 0, cpuSubmitMs: 0 };
    }

    const start = performance.now();
    this.syncCanvasSize(container, canvas, gl);

    const bg = parseColor(this.theme.background);
    gl.clearColor(bg.r / 255, bg.g / 255, bg.b / 255, 1);
    gl.clear(gl.COLOR_BUFFER_BIT);

    this.glyphUploads = 0;
    this.atlasResets = 0;

    // Walk every visible tile, baking its transform. If the atlas resets mid-walk
    // (it filled and was nuked), every quad written before the reset references
    // stale UVs — so re-walk the whole grid once against the fresh atlas.
    const genBefore = this.atlasGeneration;
    this.walkAll(frames, now);
    if (this.atlasGeneration !== genBefore) {
      this.walkAll(frames, now);
    }

    gl.useProgram(this.program);
    gl.bindTexture(gl.TEXTURE_2D, this.texture);
    gl.bindBuffer(gl.ARRAY_BUFFER, this.buffer);
    gl.bufferData(gl.ARRAY_BUFFER, this.scratch.subarray(0, this.p), gl.DYNAMIC_DRAW);
    gl.uniform2f(this.uResolution, canvas.width, canvas.height);
    gl.drawArrays(gl.TRIANGLES, 0, this.p / FLOATS_PER_VERTEX);

    return {
      drawCalls: 1,
      quads: this.quads,
      atlasUploads: this.glyphUploads,
      atlasResets: this.atlasResets,
      liveContexts: 1,
      cpuSubmitMs: performance.now() - start,
    };
  }

  dispose(): void {
    const gl = this.gl;
    if (gl) {
      gl.deleteBuffer(this.buffer);
      gl.deleteTexture(this.texture);
      gl.deleteProgram(this.program);
      // Deterministic GPU release — grid mode must not leak contexts (mirrors
      // GhosttyTerminal.tsx's loseContext-on-unmount).
      gl.getExtension('WEBGL_lose_context')?.loseContext();
    }
    this.canvas?.remove();
    this.glyphs.clear();
    this.models.clear();
    this.gl = null;
    this.canvas = null;
  }

  // --- internals -----------------------------------------------------------

  private syncCanvasSize(container: HTMLElement, canvas: HTMLCanvasElement, gl: WebGL2RenderingContext): void {
    const w = Math.max(1, Math.floor(container.clientWidth * this.dpr));
    const h = Math.max(1, Math.floor(container.clientHeight * this.dpr));
    if (w === this.canvasW && h === this.canvasH) return;
    canvas.width = w;
    canvas.height = h;
    this.canvasW = w;
    this.canvasH = h;
    gl.viewport(0, 0, w, h);
  }

  private walkAll(frames: TileFrame[], now: number): void {
    this.p = 0;
    this.quads = 0;
    for (const frame of frames) {
      if (frame.hidden || frame.alpha <= 0.001) continue;
      const model = this.models.get(frame.id);
      if (!model) continue;
      model.update();
      this.walkTile(model, frame, now);
      model.markClean();
    }
  }

  private walkTile(model: GhosttyTerminal, frame: TileFrame, now: number): void {
    const m = this.metrics;
    const s = frame.scale;
    const gs = this.dpr * s; // cell-geometry multiplier (logical metrics -> backing px)
    const ox = frame.rect.x * this.dpr;
    const oy = frame.rect.y * this.dpr;
    const alpha = frame.alpha;

    const cols = model.cols;
    const rows = model.rows;
    const cells = model.getViewport(); // reused pool — consume before any other getViewport()
    const cursor = model.getCursor();

    const defaultBg = parseColor(this.theme.background);
    const cursorBg = parseColor(this.theme.cursor);
    const cursorFg = defaultBg;

    const cellW = m.cellWidth * gs;
    const cellH = m.cellHeight * gs;
    const w = cols * cellW;
    const h = rows * cellH;
    const stateColor = STATE_COLORS[frame.state];
    const waitingFlash = frame.state === 'waiting_input' ? waitingInputFlash(now) : 0;
    const scheduledWave = frame.state === 'scheduled' ? scheduledPulse(now) : 0;

    const pulse = frame.state === 'waiting_input'
      ? 0.02 + 0.14 * waitingFlash
      : frame.state === 'scheduled'
        ? 0.015 + 0.05 * scheduledWave
        : frame.attention > 0.001
          ? frame.attention * (0.04 + 0.08 * (0.5 + 0.5 * Math.sin(now / 320)))
          : 0;
    this.pushSolid(
      ox,
      oy,
      w,
      h,
      stateColor,
      Math.min(0.24, this.stateBackgroundAlpha(frame.state) + pulse) * alpha,
    );

    for (let row = 0; row < rows; row += 1) {
      for (let col = 0; col < cols; col += 1) {
        const cell = cells[row * cols + col];
        if (!cell || cell.width === 0) continue;

        const x = ox + col * cellW;
        const y = oy + row * cellH;
        const w = Math.max(cell.width, 1) * cellW;
        const isCursor = cursor.visible && cursor.x === col && cursor.y === row;
        const fg = isCursor ? cursorFg : this.cellForeground(cell, defaultBg);
        const bg = this.cellBackground(cell);

        if (bg.r !== defaultBg.r || bg.g !== defaultBg.g || bg.b !== defaultBg.b) {
          this.pushSolid(x, y, w, cellH, bg, alpha);
        }
        if (isCursor) {
          this.pushSolid(x, y, w, cellH, cursorBg, alpha * 0.9);
        }
        if ((cell.flags & CellFlags.INVISIBLE) === 0 && cell.codepoint !== 0 && cell.codepoint !== 32) {
          const glyphAlpha = ((cell.flags & CellFlags.FAINT) !== 0 ? 0.5 : 1) * alpha;
          if (!this.pushBlockElement(cell.codepoint, x, y, w, cellH, fg, glyphAlpha)) {
            const text = cell.grapheme_len > 0
              ? model.getGraphemeString(row, col)
              : String.fromCodePoint(cell.codepoint);
            const glyph = this.getGlyph(text, cell.flags);
            this.pushTextured(x, y, glyph.width * s, glyph.height * s, glyph, fg, glyphAlpha);
          }
        }
        if ((cell.flags & CellFlags.UNDERLINE) !== 0) {
          this.pushSolid(x, y + (m.baseline + 2) * gs, w, gs, fg, alpha);
        }
        if ((cell.flags & CellFlags.STRIKETHROUGH) !== 0) {
          this.pushSolid(x, y + Math.floor(m.cellHeight / 2) * gs, w, gs, fg, alpha);
        }
      }
    }

    // Always outline the tile so it reads as a panel; the focused tile gets a
    // brighter accent so the keyboard-input target is unambiguous. Both fade with
    // the frame alpha during a zoom morph.
    if (frame.focused) {
      this.pushBorder(ox, oy, w, h, FOCUS, FOCUS_BORDER_ALPHA * alpha, Math.max(2 * this.dpr, 2));
    } else {
      this.pushBorder(
        ox,
        oy,
        w,
        h,
        stateColor,
        (
          frame.state === 'waiting_input'
            ? 0.28 + 0.72 * waitingFlash
            : frame.state === 'scheduled'
              ? 0.4 + 0.35 * scheduledWave
              : this.stateBorderAlpha(frame.state)
        ) * alpha,
        Math.max(2 * this.dpr, 1.5),
      );
    }
    if (
      frame.state !== 'waiting_input'
      && frame.attention > 0.001
    ) {
      this.pushAttentionBorder(ox, oy, w, h, stateColor, frame.attention, now);
    }
  }

  private stateBorderAlpha(state: UISessionState): number {
    if (state === 'idle') return 0.5;
    if (state === 'working') return 0.72;
    return 0.82;
  }

  private stateBackgroundAlpha(state: UISessionState): number {
    switch (state) {
      case 'idle':
      case 'recoverable':
        return 0.055;
      case 'working':
        return 0.07;
      case 'launching':
      case 'scheduled':
        return 0.085;
      case 'waiting_input':
      case 'pending_approval':
        return 0.12;
      case 'unknown':
        return 0.1;
    }
  }

  private pushAttentionBorder(
    x: number,
    y: number,
    w: number,
    h: number,
    color: Rgb,
    intensity: number,
    now: number,
  ): void {
    const pulse = 0.45 + 0.55 * (0.5 + 0.5 * Math.sin(now / 320));
    this.pushBorder(x, y, w, h, color, Math.min(1, intensity) * pulse, Math.max(2 * this.dpr, 1.5));
  }

  // Draw a 1-quad-per-edge rectangular outline inset to sit on the content box.
  private pushBorder(x: number, y: number, w: number, h: number, color: Rgb, alpha: number, t: number): void {
    this.pushSolid(x, y, w, t, color, alpha);
    this.pushSolid(x, y + h - t, w, t, color, alpha);
    this.pushSolid(x, y, t, h, color, alpha);
    this.pushSolid(x + w - t, y, t, h, color, alpha);
  }

  private cellForeground(cell: GhosttyCell, _defaultBg: Rgb): Rgb {
    if ((cell.flags & CellFlags.INVERSE) !== 0) return { r: cell.bg_r, g: cell.bg_g, b: cell.bg_b };
    return { r: cell.fg_r, g: cell.fg_g, b: cell.fg_b };
  }

  private cellBackground(cell: GhosttyCell): Rgb {
    if ((cell.flags & CellFlags.INVERSE) !== 0) return { r: cell.fg_r, g: cell.fg_g, b: cell.fg_b };
    return { r: cell.bg_r, g: cell.bg_g, b: cell.bg_b };
  }

  private getGlyph(text: string, flags: number): AtlasGlyph {
    const style = `${flags & CellFlags.ITALIC ? 'italic ' : ''}${flags & CellFlags.BOLD ? 'bold ' : ''}`;
    const key = `${style}${text}`;
    const existing = this.glyphs.get(key);
    if (existing) return existing;

    const context = this.atlasContext!;
    const scale = this.dpr;
    // Emoji clusters (ZWJ/flag/skin-tone/keycap) must be shaped Apple-Color-Emoji
    // -first or WKWebView's canvas fallback decomposes them; see terminalGlyphFont.
    context.font = terminalGlyphFont(style, this.fontSize * scale, this.fontFamily, text);
    const width = Math.max(Math.ceil(context.measureText(text).width) + 4, this.metrics.cellWidth * scale);
    const height = this.metrics.cellHeight * scale;
    if (this.atlasX + width >= ATLAS_SIZE) {
      this.atlasX = 2;
      this.atlasY += this.atlasRowHeight + 1;
      this.atlasRowHeight = 0;
    }
    if (this.atlasY + height >= ATLAS_SIZE) {
      this.resetAtlas();
    }

    const x = this.atlasX;
    const y = this.atlasY;
    context.clearRect(x, y, width, height);
    context.fillStyle = '#ffffff';
    context.textBaseline = 'alphabetic';
    context.fillText(text, x, y + this.metrics.baseline * scale);
    const bitmap = context.getImageData(x, y, width, height);
    const gl = this.gl!;
    gl.bindTexture(gl.TEXTURE_2D, this.texture);
    gl.texSubImage2D(gl.TEXTURE_2D, 0, x, y, width, height, gl.RGBA, gl.UNSIGNED_BYTE, bitmap);
    if (this.mipmaps) gl.generateMipmap(gl.TEXTURE_2D);
    this.glyphUploads += 1;

    const glyph: AtlasGlyph = {
      u0: x / ATLAS_SIZE,
      v0: y / ATLAS_SIZE,
      u1: (x + width) / ATLAS_SIZE,
      v1: (y + height) / ATLAS_SIZE,
      width,
      height,
      colored: isColorGlyphBitmap(bitmap),
    };
    this.atlasX += width + 1;
    this.atlasRowHeight = Math.max(this.atlasRowHeight, height);
    this.glyphs.set(key, glyph);
    return glyph;
  }

  // Drop every cached glyph so the next frame re-rasterizes against the current
  // document fonts. Used when the bundled Nerd Font finishes loading after some
  // tiles already cached blank icon glyphs (grid runs a continuous RAF loop, so
  // no explicit repaint is needed). No-op before mount, when there is nothing to
  // invalidate.
  invalidateGlyphCache(): void {
    if (!this.gl || !this.atlasContext) return;
    this.resetAtlas();
  }

  private resetAtlas(): void {
    this.glyphs.clear();
    this.atlasX = 2;
    this.atlasY = 1;
    this.atlasRowHeight = 0;
    this.atlasGeneration += 1;
    this.atlasResets += 1;
    const context = this.atlasContext!;
    context.clearRect(0, 0, ATLAS_SIZE, ATLAS_SIZE);
    context.fillStyle = '#ffffff';
    context.fillRect(0, 0, 1, 1);
    const gl = this.gl!;
    gl.bindTexture(gl.TEXTURE_2D, this.texture);
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, ATLAS_SIZE, ATLAS_SIZE, 0, gl.RGBA, gl.UNSIGNED_BYTE, this.atlas!);
  }

  private configureAttribute(name: string, size: number, stride: number, offset: number): void {
    const gl = this.gl!;
    const location = gl.getAttribLocation(this.program!, name);
    gl.enableVertexAttribArray(location);
    gl.vertexAttribPointer(location, size, gl.FLOAT, false, stride, offset);
  }

  private pushSolid(x: number, y: number, w: number, h: number, color: Rgb, alpha: number): void {
    this.pushQuad(x, y, w, h, SOLID_TEXEL_CENTER, SOLID_TEXEL_CENTER, SOLID_TEXEL_CENTER, SOLID_TEXEL_CENTER, color, alpha, GLYPH_MODE_TINT);
  }

  private pushTextured(x: number, y: number, w: number, h: number, glyph: AtlasGlyph, color: Rgb, alpha: number): void {
    // Color glyphs (emoji) pass the atlas RGBA through; monochrome glyphs tint.
    this.pushQuad(x, y, w, h, glyph.u0, glyph.v0, glyph.u1, glyph.v1, color, alpha, glyph.colored ? GLYPH_MODE_COLOR : GLYPH_MODE_TINT);
  }

  private pushBlockElement(codepoint: number, x: number, y: number, w: number, h: number, color: Rgb, alpha: number): boolean {
    const rects = BLOCK_ELEMENT_RECTS[codepoint];
    if (!rects) return false;
    for (const rect of rects) {
      this.pushSolid(x + w * rect.x, y + h * rect.y, w * rect.width, h * rect.height, color, alpha);
    }
    return true;
  }

  // The hot path: write 54 floats (6 verts × 9) straight into the preallocated
  // scratch. The trailing float per vertex is a_mode (see terminalGlyphProgram).
  private pushQuad(
    x: number,
    y: number,
    w: number,
    h: number,
    u0: number,
    v0: number,
    u1: number,
    v1: number,
    color: Rgb,
    alpha: number,
    mode: number,
  ): void {
    if (this.p + FLOATS_PER_QUAD > this.scratch.length) {
      const next = new Float32Array(this.scratch.length * 2);
      next.set(this.scratch.subarray(0, this.p));
      this.scratch = next;
    }
    const s = this.scratch;
    const r = color.r / 255;
    const g = color.g / 255;
    const b = color.b / 255;
    const a = alpha;
    const e = mode;
    let p = this.p;
    // tri 1
    s[p++] = x; s[p++] = y; s[p++] = u0; s[p++] = v0; s[p++] = r; s[p++] = g; s[p++] = b; s[p++] = a; s[p++] = e;
    s[p++] = x + w; s[p++] = y; s[p++] = u1; s[p++] = v0; s[p++] = r; s[p++] = g; s[p++] = b; s[p++] = a; s[p++] = e;
    s[p++] = x; s[p++] = y + h; s[p++] = u0; s[p++] = v1; s[p++] = r; s[p++] = g; s[p++] = b; s[p++] = a; s[p++] = e;
    // tri 2
    s[p++] = x; s[p++] = y + h; s[p++] = u0; s[p++] = v1; s[p++] = r; s[p++] = g; s[p++] = b; s[p++] = a; s[p++] = e;
    s[p++] = x + w; s[p++] = y; s[p++] = u1; s[p++] = v0; s[p++] = r; s[p++] = g; s[p++] = b; s[p++] = a; s[p++] = e;
    s[p++] = x + w; s[p++] = y + h; s[p++] = u1; s[p++] = v1; s[p++] = r; s[p++] = g; s[p++] = b; s[p++] = a; s[p++] = e;
    this.p = p;
    this.quads += 1;
  }
}
