// @vitest-environment node
// The first-party binding read against the real module: every accessor the app
// calls, exercised on content whose expected value is obvious from the VT that
// produced it. The struct offsets in abi.ts are asserted here by consequence —
// a layout that moved upstream reads the wrong byte and fails a case by name.
// @ts-expect-error -- @types/node is only a transitive peer here (see kittyWireRewrite.parity.test.ts)
import { readFileSync } from 'node:fs';
// @ts-expect-error -- see above
import { fileURLToPath } from 'node:url';
import { beforeAll, describe, expect, it } from 'vitest';
import { CellFlags, Ghostty, type GhosttyTerminal } from './index';

const wasmPath = fileURLToPath(new URL('../../vendor/ghostty-vt/ghostty-vt.wasm', import.meta.url));

let ghostty: Ghostty;

beforeAll(async () => {
  const mod = await WebAssembly.compile(readFileSync(wasmPath));
  let instance: WebAssembly.Instance;
  instance = await WebAssembly.instantiate(mod, {
    env: {
      log: (ptr: number, len: number) => {
        const memory = (instance.exports.memory as WebAssembly.Memory).buffer;
        console.log('[ghostty-vt]', new TextDecoder().decode(new Uint8Array(memory, ptr, len)));
      },
    },
  });
  ghostty = new Ghostty(instance);
});

function terminal(cols = 20, rows = 5, config = {}): GhosttyTerminal {
  return ghostty.createTerminal(cols, rows, config);
}

function rowText(t: GhosttyTerminal, y: number): string {
  const cells = t.getViewport();
  let text = '';
  for (let x = 0; x < t.cols; x += 1) {
    const cell = cells[y * t.cols + x];
    text += cell.codepoint ? String.fromCodePoint(cell.codepoint) : ' ';
  }
  return text.trimEnd();
}

describe('GhosttyTerminal', () => {
  it('writes text and reads it back through the viewport', () => {
    const t = terminal();
    t.write('hello\r\nworld');
    t.update();
    expect(rowText(t, 0)).toBe('hello');
    expect(rowText(t, 1)).toBe('world');
    t.free();
  });

  it('decodes every SGR attribute into cell flags', () => {
    const t = terminal();
    t.write('\x1b[1mB\x1b[0m\x1b[3mI\x1b[0m\x1b[4mU\x1b[0m\x1b[9mS\x1b[0m'
      + '\x1b[7mV\x1b[0m\x1b[8mH\x1b[0m\x1b[5mK\x1b[0m\x1b[2mF\x1b[0m');
    t.update();
    const cells = t.getViewport();
    const flagAt = (x: number) => cells[x].flags;
    expect(flagAt(0)).toBe(CellFlags.BOLD);
    expect(flagAt(1)).toBe(CellFlags.ITALIC);
    expect(flagAt(2)).toBe(CellFlags.UNDERLINE);
    expect(flagAt(3)).toBe(CellFlags.STRIKETHROUGH);
    expect(flagAt(4)).toBe(CellFlags.INVERSE);
    expect(flagAt(5)).toBe(CellFlags.INVISIBLE);
    expect(flagAt(6)).toBe(CellFlags.BLINK);
    expect(flagAt(7)).toBe(CellFlags.FAINT);
    t.free();
  });

  it('resolves cell colors, falling back to the configured defaults', () => {
    const t = terminal(20, 5, { fgColor: 0x445566, bgColor: 0x112233 });
    t.write('a\x1b[38;2;10;20;30m\x1b[48;2;40;50;60mb');
    t.update();
    const cells = t.getViewport();
    expect(cells[0]).toMatchObject({ fg_r: 0x44, fg_g: 0x55, fg_b: 0x66, bg_r: 0x11, bg_g: 0x22, bg_b: 0x33 });
    expect(cells[1]).toMatchObject({ fg_r: 10, fg_g: 20, fg_b: 30, bg_r: 40, bg_g: 50, bg_b: 60 });
    t.free();
  });

  it('preserves the extended indexed palette when applying an ANSI theme', () => {
    const palette = Array.from({ length: 16 }, (_, index) => index === 3 ? 0x123456 : 0);
    const t = terminal(20, 5, { fgColor: 0x445566, bgColor: 0x112233, palette });
    t.write('\x1b[33mA\x1b[38;5;46mG\x1b[38;5;196mR\x1b[38;5;231mW\x1b[48;5;21mB');

    const cells = t.getViewport();
    expect(cells[0]).toMatchObject({ fg_r: 0x12, fg_g: 0x34, fg_b: 0x56 });
    expect(cells[1]).toMatchObject({ fg_r: 0x00, fg_g: 0xff, fg_b: 0x00 });
    expect(cells[2]).toMatchObject({ fg_r: 0xff, fg_g: 0x00, fg_b: 0x00 });
    expect(cells[3]).toMatchObject({ fg_r: 0xff, fg_g: 0xff, fg_b: 0xff });
    expect(cells[4]).toMatchObject({ bg_r: 0x00, bg_g: 0x00, bg_b: 0xff });
    t.free();
  });

  it('reports wide cells and their spacer', () => {
    const t = terminal();
    t.write('漢x');
    t.update();
    const cells = t.getViewport();
    expect(cells[0].width).toBe(2);
    expect(cells[1].width).toBe(0);
    expect(cells[2].codepoint).toBe('x'.codePointAt(0));
    t.free();
  });

  it('exposes a combining cluster as one grapheme string', () => {
    const t = terminal();
    // Mode 2027 keeps the cluster in a single cell.
    t.write('\x1b[?2027h');
    t.write('é');
    t.update();
    const cells = t.getViewport();
    expect(cells[0].grapheme_len).toBe(1);
    expect(t.getGraphemeString(0, 0)).toBe('é');
    t.free();
  });

  it('tracks the cursor and the render-state colors', () => {
    const t = terminal(20, 5, { fgColor: 0x445566, bgColor: 0x112233 });
    t.write('abc');
    t.update();
    expect(t.getCursor()).toMatchObject({ x: 3, y: 0, visible: true, style: 'block' });
    expect(t.getColors()).toMatchObject({
      foreground: { r: 0x44, g: 0x55, b: 0x66 },
      background: { r: 0x11, g: 0x22, b: 0x33 },
      cursor: null,
    });
    t.free();
  });

  // The app reads the cursor and the viewport straight after writing the bytes
  // that moved them — OSC 133 segmentation records a command block's row that
  // way, and a restore seeds block anchors that way. Reading the render state
  // without syncing answers as of the previous frame, which put every block on
  // a stale row and broke click-to-select after an attach.
  it('reads back a write without an explicit update', () => {
    const t = terminal(20, 5);
    t.write('abc');
    expect(t.getCursor()).toMatchObject({ x: 3, y: 0 });
    t.write('\r\nsecond');
    expect(t.getCursor()).toMatchObject({ x: 6, y: 1 });
    expect(rowText(t, 1).trimEnd()).toBe('second');
    t.free();
  });

  // A read between frames must not eat the repaint: ghostty hands over the
  // dirty set once and clears it, so the sync those reads trigger has to
  // accumulate rather than consume.
  // Reads sync the render state, and only markClean() ends a frame. A read
  // between frames must therefore leave the dirty set intact — otherwise a
  // block-anchor or cursor read would silently eat somebody's repaint.
  it('keeps dirty rows through a read that syncs the render state', () => {
    const t = terminal(20, 5);
    t.update();
    t.markClean();
    t.write('one');
    t.getCursor();
    t.getViewport();
    expect(t.update()).not.toBe(0);
    expect(t.isRowDirty(0)).toBe(true);
    t.markClean();
    expect(t.isRowDirty(0)).toBe(false);
    t.free();
  });

  it('reports dirty rows and clears them on markClean', () => {
    const t = terminal();
    t.write('one\r\ntwo');
    expect(t.update()).not.toBe(0);
    expect(t.isRowDirty(0)).toBe(true);
    t.markClean();
    expect(t.isRowDirty(0)).toBe(false);
    expect(t.update()).toBe(0);
    t.write('\r\nthree');
    t.update();
    expect(t.isRowDirty(2)).toBe(true);
    t.free();
  });

  it('answers DSR through the write_pty callback', () => {
    const t = terminal();
    t.write('\x1b[6n');
    expect(t.hasResponse()).toBe(true);
    expect(t.readResponse()).toBe('\x1b[1;1R');
    expect(t.hasResponse()).toBe(false);
    expect(t.readResponse()).toBeNull();
    t.free();
  });

  it('reads DEC modes, including the ones the app gates behavior on', () => {
    const t = terminal();
    expect(t.getMode(7)).toBe(true); // wraparound, on by default
    expect(t.hasBracketedPaste()).toBe(false);
    t.write('\x1b[?2004h\x1b[?1006h\x1b[?7l');
    expect(t.hasBracketedPaste()).toBe(true);
    expect(t.getMode(1006)).toBe(true);
    expect(t.getMode(7)).toBe(false);
    t.free();
  });

  it('follows the alternate screen and mouse tracking', () => {
    const t = terminal();
    expect(t.isAlternateScreen()).toBe(false);
    expect(t.hasMouseTracking()).toBe(false);
    t.write('\x1b[?1049h\x1b[?1002h');
    expect(t.isAlternateScreen()).toBe(true);
    expect(t.hasMouseTracking()).toBe(true);
    t.write('\x1b[?1049l\x1b[?1002l');
    expect(t.isAlternateScreen()).toBe(false);
    expect(t.hasMouseTracking()).toBe(false);
    t.free();
  });

  // Every scalar getter shares one wasm scratch buffer, so each must read
  // exactly the width the ABI declares for its field. rowWrapsIntoNext leaves a
  // 64-bit row handle there, and hover detection calls it on the row above the
  // pointer before the next wheel event asks about mouse tracking.
  it('answers scalar reads truthfully after a wide write left a handle in the scratch buffer', () => {
    const t = terminal(20, 5, { scrollbackLimit: 1 << 20 });
    for (let i = 0; i < 40; i += 1) t.write(`line${i}\r\n`);
    t.write('\x1b[?7h');
    t.update();

    expect(t.rowWrapsIntoNext(1)).toBe(false);
    expect(t.hasMouseTracking()).toBe(false);
    expect(t.isAlternateScreen()).toBe(false);
    expect(t.getMode(7)).toBe(true);
    expect(t.getScrollbackLength()).toBe(36);

    t.write('\x1b[?1002h');
    t.update();
    t.rowWrapsIntoNext(1);
    expect(t.hasMouseTracking()).toBe(true);
    t.free();
  });

  it('reads scrollback rows by history offset', () => {
    const t = terminal(20, 3, { scrollbackLimit: 1 << 20 });
    for (let i = 0; i < 10; i += 1) t.write(`row${i}\r\n`);
    t.update();
    expect(t.getScrollbackLength()).toBe(8);
    const line = t.getScrollbackLine(0);
    expect(line).not.toBeNull();
    expect(line!.slice(0, 4).map((c) => String.fromCodePoint(c.codepoint)).join('')).toBe('row0');
    expect(t.getScrollbackGraphemeString(0, 0)).toBe('r');
    expect(t.getScrollbackLine(8)).toBeNull();
    t.free();
  });

  // Readers that want one line must not pay for the grid. The row this hands
  // back has to be indistinguishable from getViewport()'s slice of it,
  // including the blank tail past the row's last written cell.
  it('reads one active row without decoding the rest of the viewport', () => {
    const t = terminal(20, 4);
    t.write('alpha\r\nbravo\r\ncharlie');
    t.update();
    const viewport = t.getViewport();
    for (let row = 0; row < 4; row += 1) {
      const line = t.getActiveLine(row);
      expect(line).not.toBeNull();
      expect(line!.map((c) => c.codepoint)).toEqual(
        viewport.slice(row * 20, (row + 1) * 20).map((c) => c.codepoint),
      );
    }
    expect(t.getActiveLine(-1)).toBeNull();
    expect(t.getActiveLine(4)).toBeNull();
    t.free();
  });

  // The flag marks the row the text wraps OUT of, not the one it continues
  // into. Callers ask the opposite question, and reading it in the wrong
  // direction silently drops a link that soft-wraps across rows.
  it('marks the row a soft wrap starts on, not the one it continues onto', () => {
    const t = terminal(10, 5);
    t.write('0123456789abc');
    t.update();
    expect(t.rowWrapsIntoNext(0)).toBe(true);
    expect(t.rowWrapsIntoNext(1)).toBe(false);
    t.free();
  });

  it('returns OSC 8 hyperlink URIs by position', () => {
    const t = terminal();
    t.write('\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\ x');
    t.update();
    const cells = t.getViewport();
    expect(cells[0].hyperlink_id).toBe(1);
    expect(t.getHyperlinkUri(0, 0)).toBe('https://example.com');
    expect(cells[6].hyperlink_id).toBe(0);
    expect(t.getHyperlinkUri(0, 6)).toBeNull();
    t.free();
  });

  it('reflows on resize and keeps the pool sized to the viewport', () => {
    const t = terminal(10, 5);
    t.write('0123456789abcde');
    t.resize(20, 5);
    t.update();
    expect(t.cols).toBe(20);
    expect(t.getViewport()).toHaveLength(100);
    expect(rowText(t, 0)).toBe('0123456789abcde');
    t.free();
  });

  it('is safe to free twice', () => {
    const t = terminal();
    t.free();
    expect(() => t.free()).not.toThrow();
  });
});
