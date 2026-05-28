import { CellFlags, type GhosttyCell, type GhosttyTerminal } from 'ghostty-web';
import { cursorRowInViewport, viewportBufferStart } from '../utils/ghosttyScroll';

interface RendererTheme {
  background: string;
  foreground: string;
  cursor: string;
}

export interface WebGlSelection {
  startRow: number;
  startCol: number;
  endRow: number;
  endCol: number;
  color: string;
}

interface AtlasGlyph {
  u0: number;
  v0: number;
  u1: number;
  v1: number;
  width: number;
  height: number;
}

interface Rgb {
  r: number;
  g: number;
  b: number;
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
  quads: number;
  glyphUploads: number;
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

const ATLAS_SIZE = 2048;
const FLOATS_PER_VERTEX = 8;
const SOLID_TEXEL_CENTER = 0.5 / ATLAS_SIZE;
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
  const vertexShader = compileShader(gl, gl.VERTEX_SHADER, `#version 300 es
    in vec2 a_position;
    in vec2 a_texcoord;
    in vec4 a_color;
    uniform vec2 u_resolution;
    out vec2 v_texcoord;
    out vec4 v_color;

    void main() {
      vec2 clip = (a_position / u_resolution) * 2.0 - 1.0;
      gl_Position = vec4(clip * vec2(1.0, -1.0), 0.0, 1.0);
      v_texcoord = a_texcoord;
      v_color = a_color;
    }
  `);
  const fragmentShader = compileShader(gl, gl.FRAGMENT_SHADER, `#version 300 es
    precision mediump float;
    uniform sampler2D u_atlas;
    in vec2 v_texcoord;
    in vec4 v_color;
    out vec4 out_color;

    void main() {
      float mask = texture(u_atlas, v_texcoord).a;
      out_color = vec4(v_color.rgb, v_color.a * mask);
    }
  `);
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
  readonly cellWidth: number;
  readonly cellHeight: number;

  private readonly canvas: HTMLCanvasElement;
  private readonly gl: WebGL2RenderingContext;
  private readonly program: WebGLProgram;
  private readonly buffer: WebGLBuffer;
  private readonly texture: WebGLTexture;
  private readonly atlas: HTMLCanvasElement;
  private readonly atlasContext: CanvasRenderingContext2D;
  private readonly glyphs = new Map<string, AtlasGlyph>();
  private readonly dpr: number;
  private readonly baseline: number;
  private readonly fontSize: number;
  private readonly fontFamily: string;
  private readonly theme: RendererTheme;
  private atlasX = 2;
  private atlasY = 1;
  private atlasRowHeight = 0;
  private atlasGeneration = 0;
  private retryingAtlasFrame = false;
  private cols = 0;
  private rows = 0;

  constructor(canvas: HTMLCanvasElement, fontSize: number, fontFamily: string, theme: RendererTheme) {
    this.canvas = canvas;
    this.fontSize = fontSize;
    this.fontFamily = fontFamily;
    this.theme = theme;
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
    this.atlas.width = ATLAS_SIZE;
    this.atlas.height = ATLAS_SIZE;
    this.atlasContext = this.atlas.getContext('2d') ?? (() => { throw new Error('Unable to allocate glyph atlas'); })();
    this.atlasContext.fillStyle = '#ffffff';
    this.atlasContext.fillRect(0, 0, 1, 1);

    gl.bindTexture(gl.TEXTURE_2D, this.texture);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, ATLAS_SIZE, ATLAS_SIZE, 0, gl.RGBA, gl.UNSIGNED_BYTE, this.atlas);

    gl.useProgram(this.program);
    gl.bindBuffer(gl.ARRAY_BUFFER, this.buffer);
    const stride = FLOATS_PER_VERTEX * Float32Array.BYTES_PER_ELEMENT;
    this.configureAttribute('a_position', 2, stride, 0);
    this.configureAttribute('a_texcoord', 2, stride, 2 * Float32Array.BYTES_PER_ELEMENT);
    this.configureAttribute('a_color', 4, stride, 4 * Float32Array.BYTES_PER_ELEMENT);
    gl.uniform1i(gl.getUniformLocation(this.program, 'u_atlas'), 0);
    gl.enable(gl.BLEND);
    gl.blendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA);
  }

  fitDimensions(width: number, height: number): { cols: number; rows: number } {
    return {
      cols: Math.max(1, Math.floor(width / this.cellWidth)),
      rows: Math.max(1, Math.floor(height / this.cellHeight)),
    };
  }

  resize(cols: number, rows: number): void {
    if (cols === this.cols && rows === this.rows) {
      return;
    }
    this.cols = cols;
    this.rows = rows;
    this.canvas.width = Math.ceil(cols * this.cellWidth * this.dpr);
    this.canvas.height = Math.ceil(rows * this.cellHeight * this.dpr);
    this.canvas.style.width = `${cols * this.cellWidth}px`;
    this.canvas.style.height = `${rows * this.cellHeight}px`;
    this.gl.viewport(0, 0, this.canvas.width, this.canvas.height);
  }

  render(
    terminal: GhosttyTerminal,
    force = false,
    viewportCells?: GhosttyCell[],
    selection?: WebGlSelection | null,
    viewportOffset = 0,
  ): WebGlRenderSample | null {
    const startedAt = performance.now();
    const dirty = terminal.update();
    if (!force && dirty === 0) {
      return null;
    }

    const gl = this.gl;
    const scale = this.dpr;
    const defaultBg = parseColor(this.theme.background);
    const cursorBg = parseColor(this.theme.cursor);
    const cursorFg = parseColor(this.theme.background);
    const cursor = terminal.getCursor();
    const cursorRow = cursor.visible
      ? cursorRowInViewport(cursor.y, viewportOffset, terminal.rows)
      : null;
    const cells = viewportCells ?? terminal.getViewport();
    const vertices: number[] = [];
    const glyphCountBefore = this.glyphs.size;
    const atlasGenerationBefore = this.atlasGeneration;

    gl.clearColor(defaultBg.r / 255, defaultBg.g / 255, defaultBg.b / 255, 1);
    gl.clear(gl.COLOR_BUFFER_BIT);

    for (let row = 0; row < terminal.rows; row += 1) {
      for (let col = 0; col < terminal.cols; col += 1) {
        const cell = cells[row * terminal.cols + col];
        if (!cell || cell.width === 0) {
          continue;
        }
        const width = Math.max(cell.width, 1) * this.cellWidth * scale;
        const x = col * this.cellWidth * scale;
        const y = row * this.cellHeight * scale;
        const isCursor = cursorRow !== null && cursor.x === col && cursorRow === row;
        const fg = isCursor ? cursorFg : this.cellForeground(cell);
        const bg = this.cellBackground(cell);
        const isSelected = selection
          ? row > selection.startRow && row < selection.endRow
            || row === selection.startRow && row === selection.endRow && col >= selection.startCol && col < selection.endCol
            || row === selection.startRow && row < selection.endRow && col >= selection.startCol
            || row === selection.endRow && row > selection.startRow && col < selection.endCol
          : false;

        if (bg.r !== defaultBg.r || bg.g !== defaultBg.g || bg.b !== defaultBg.b) {
          this.pushSolidQuad(vertices, x, y, width, this.cellHeight * scale, bg, 1);
        }
        if (isSelected && selection) {
          this.pushSolidQuad(vertices, x, y, width, this.cellHeight * scale, parseColor(selection.color), 1);
        }
        if (isCursor) {
          this.pushSolidQuad(vertices, x, y, width, this.cellHeight * scale, cursorBg, 1);
        }
        if ((cell.flags & CellFlags.INVISIBLE) === 0 && cell.codepoint !== 0 && cell.codepoint !== 32) {
          const alpha = (cell.flags & CellFlags.FAINT) !== 0 ? 0.5 : 1;
          if (!this.pushBlockElement(vertices, cell.codepoint, x, y, width, this.cellHeight * scale, fg, alpha)) {
            const text = cell.grapheme_len > 0
              ? graphemeAtViewportCell(terminal, row, col, viewportOffset)
              : String.fromCodePoint(cell.codepoint);
            const glyph = this.getGlyph(text, cell.flags);
            this.pushTexturedQuad(vertices, x, y, glyph.width, glyph.height, glyph, fg, alpha);
          }
        }
        if ((cell.flags & CellFlags.UNDERLINE) !== 0) {
          this.pushSolidQuad(vertices, x, y + (this.baseline + 2) * scale, width, scale, fg, 1);
        }
        if ((cell.flags & CellFlags.STRIKETHROUGH) !== 0) {
          this.pushSolidQuad(vertices, x, y + Math.floor(this.cellHeight / 2) * scale, width, scale, fg, 1);
        }
      }
    }

    if (this.atlasGeneration !== atlasGenerationBefore && !this.retryingAtlasFrame) {
      this.retryingAtlasFrame = true;
      try {
        return this.render(terminal, true, viewportCells, selection, viewportOffset);
      } finally {
        this.retryingAtlasFrame = false;
      }
    }

    gl.useProgram(this.program);
    gl.bindTexture(gl.TEXTURE_2D, this.texture);
    gl.bindBuffer(gl.ARRAY_BUFFER, this.buffer);
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array(vertices), gl.DYNAMIC_DRAW);
    gl.uniform2f(gl.getUniformLocation(this.program, 'u_resolution'), this.canvas.width, this.canvas.height);
    gl.drawArrays(gl.TRIANGLES, 0, vertices.length / FLOATS_PER_VERTEX);
    terminal.markClean();
    return {
      cpuSubmitMs: performance.now() - startedAt,
      cells: terminal.cols * terminal.rows,
      quads: vertices.length / FLOATS_PER_VERTEX / 6,
      glyphUploads: this.glyphs.size - glyphCountBefore,
    };
  }

  dispose(): void {
    this.gl.deleteBuffer(this.buffer);
    this.gl.deleteTexture(this.texture);
    this.gl.deleteProgram(this.program);
    this.glyphs.clear();
  }

  private configureAttribute(name: string, size: number, stride: number, offset: number): void {
    const location = this.gl.getAttribLocation(this.program, name);
    this.gl.enableVertexAttribArray(location);
    this.gl.vertexAttribPointer(location, size, this.gl.FLOAT, false, stride, offset);
  }

  private cellForeground(cell: GhosttyCell): Rgb {
    if ((cell.flags & CellFlags.INVERSE) !== 0) {
      return this.readColor(cell.bg_r, cell.bg_g, cell.bg_b);
    }
    return this.readColor(cell.fg_r, cell.fg_g, cell.fg_b);
  }

  private cellBackground(cell: GhosttyCell): Rgb {
    if ((cell.flags & CellFlags.INVERSE) !== 0) {
      return this.readColor(cell.fg_r, cell.fg_g, cell.fg_b);
    }
    return this.readColor(cell.bg_r, cell.bg_g, cell.bg_b);
  }

  private readColor(r: number, g: number, b: number): Rgb {
    return { r, g, b };
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
    context.font = `${style}${this.fontSize * scale}px ${this.fontFamily}`;
    const width = Math.max(Math.ceil(context.measureText(text).width) + 4, this.cellWidth * scale);
    const height = this.cellHeight * scale;
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
    context.fillText(text, x, y + this.baseline * scale);
    this.gl.bindTexture(this.gl.TEXTURE_2D, this.texture);
    this.gl.texSubImage2D(this.gl.TEXTURE_2D, 0, x, y, width, height, this.gl.RGBA, this.gl.UNSIGNED_BYTE, context.getImageData(x, y, width, height));

    const glyph = {
      u0: x / ATLAS_SIZE,
      v0: y / ATLAS_SIZE,
      u1: (x + width) / ATLAS_SIZE,
      v1: (y + height) / ATLAS_SIZE,
      width,
      height,
    };
    this.atlasX += width + 1;
    this.atlasRowHeight = Math.max(this.atlasRowHeight, height);
    this.glyphs.set(key, glyph);
    return glyph;
  }

  private resetAtlas(): void {
    this.glyphs.clear();
    this.atlasX = 2;
    this.atlasY = 1;
    this.atlasRowHeight = 0;
    this.atlasGeneration += 1;
    this.atlasContext.clearRect(0, 0, ATLAS_SIZE, ATLAS_SIZE);
    this.atlasContext.fillStyle = '#ffffff';
    this.atlasContext.fillRect(0, 0, 1, 1);
    this.gl.bindTexture(this.gl.TEXTURE_2D, this.texture);
    this.gl.texImage2D(
      this.gl.TEXTURE_2D,
      0,
      this.gl.RGBA,
      ATLAS_SIZE,
      ATLAS_SIZE,
      0,
      this.gl.RGBA,
      this.gl.UNSIGNED_BYTE,
      this.atlas,
    );
  }

  private pushSolidQuad(vertices: number[], x: number, y: number, width: number, height: number, color: Rgb, alpha: number): void {
    // Keep all samples within the white texel. Sampling its edges with LINEAR
    // filtering blends into transparent atlas neighbours and leaves seams.
    this.pushQuad(vertices, x, y, width, height, SOLID_TEXEL_CENTER, SOLID_TEXEL_CENTER, SOLID_TEXEL_CENTER, SOLID_TEXEL_CENTER, color, alpha);
  }

  private pushBlockElement(vertices: number[], codepoint: number, x: number, y: number, width: number, height: number, color: Rgb, alpha: number): boolean {
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

  private pushTexturedQuad(vertices: number[], x: number, y: number, width: number, height: number, glyph: AtlasGlyph, color: Rgb, alpha: number): void {
    this.pushQuad(vertices, x, y, width, height, glyph.u0, glyph.v0, glyph.u1, glyph.v1, color, alpha);
  }

  private pushQuad(
    vertices: number[],
    x: number,
    y: number,
    width: number,
    height: number,
    u0: number,
    v0: number,
    u1: number,
    v1: number,
    color: Rgb,
    alpha: number,
  ): void {
    const rgba = [color.r / 255, color.g / 255, color.b / 255, alpha];
    vertices.push(
      x, y, u0, v0, ...rgba,
      x + width, y, u1, v0, ...rgba,
      x, y + height, u0, v1, ...rgba,
      x, y + height, u0, v1, ...rgba,
      x + width, y, u1, v0, ...rgba,
      x + width, y + height, u1, v1, ...rgba,
    );
  }
}
