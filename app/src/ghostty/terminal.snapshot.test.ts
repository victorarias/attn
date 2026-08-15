// @vitest-environment node
// The production restore path in miniature: a snapshot the native worker
// encoded, decoded by the browser module. The two are different builds of the
// same library configured differently — the worker stores kitty images, this
// one has kitty compiled out — so the fixture is what pins them to one format.
// Regenerate it with ATTN_UPDATE_FIXTURES=1 go test ./internal/ghosttyvt.
// @ts-expect-error -- @types/node is only a transitive peer here
import { readFileSync } from 'node:fs';
// @ts-expect-error -- see above
import { fileURLToPath } from 'node:url';
import { beforeAll, describe, expect, it } from 'vitest';
import { CellFlags, Ghostty, type GhosttyCell, type GhosttyTerminal } from './index';

const wasmPath = fileURLToPath(new URL('../../vendor/ghostty-vt/ghostty-vt.wasm', import.meta.url));
const fixturePath = fileURLToPath(new URL('./testdata/native-snapshot.bin', import.meta.url));

let ghostty: Ghostty;
let snapshot: Uint8Array;

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
  snapshot = new Uint8Array(readFileSync(fixturePath));
});

function text(cells: GhosttyCell[] | null, cols: number): string {
  if (!cells) return '';
  let out = '';
  for (let x = 0; x < cols; x += 1) out += cells[x].codepoint ? String.fromCodePoint(cells[x].codepoint) : ' ';
  return out.trimEnd();
}

function viewportRows(t: GhosttyTerminal): string[] {
  const cells = t.getViewport();
  const rows: string[] = [];
  for (let y = 0; y < t.rows; y += 1) rows.push(text(cells.slice(y * t.cols, (y + 1) * t.cols), t.cols));
  return rows;
}

/** A terminal of a deliberately different size, so adoption has to move it. */
function adopted(): { terminal: GhosttyTerminal; history: ReturnType<GhosttyTerminal['adoptSnapshot']> } {
  const terminal = ghostty.createTerminal(80, 24, {});
  terminal.write('this content belongs to the session being replaced\r\n');
  return { terminal, history: terminal.adoptSnapshot(snapshot) };
}

describe('adoptSnapshot', () => {
  it('takes the snapshot grid, not the one it replaced', () => {
    const { terminal } = adopted();
    expect([terminal.cols, terminal.rows]).toEqual([40, 6]);
    expect(viewportRows(terminal)).toEqual([
      'row-1199 tail',
      'row-1200 tail',
      'STYLED',
      'wrapwrapwrapwrapwrapwrapwrapwrapwrapwrap',
      'wrapwrapwrapwrapwrap',
      'prompt$',
    ]);
    terminal.free();
  });

  it('carries styling, not just codepoints', () => {
    const { terminal } = adopted();
    const styled = terminal.getLine(2)![0];
    expect(styled.flags & CellFlags.BOLD).toBeTruthy();
    expect(styled.flags & CellFlags.UNDERLINE).toBeTruthy();
    terminal.free();
  });

  it('puts nothing on the pty', () => {
    // The fixture's parser sits mid-CSI, which is the case a replay-based
    // restore would answer queries from.
    const { terminal } = adopted();
    expect(terminal.hasResponse()).toBe(false);
    terminal.free();
  });

  it('leaves scrollback to the history pages', () => {
    const { terminal, history } = adopted();
    expect(history.rows).toBeGreaterThan(0);
    expect(terminal.getScrollbackLength()).toBeLessThan(history.rows);

    let pages = 0;
    while (history.next() !== null) pages += 1;
    expect(pages).toBeGreaterThan(0);
    expect(terminal.getScrollbackLength()).toBe(history.rows);
    expect(text(terminal.getScrollbackLine(0), terminal.cols)).toBe('row-0001 tail');
    terminal.free();
  });

  it('is done once, and closing twice is harmless', () => {
    const { terminal, history } = adopted();
    while (history.next() !== null) { /* drain */ }
    expect(history.next()).toBeNull();
    history.close();
    history.close();
    terminal.free();
  });

  it('rejects bytes it cannot decode without touching the terminal', () => {
    // The containment in GhosttyTerminal.restoreSnapshot rests on this: a
    // refused decode must leave the model it declined to replace intact, so a
    // snapshot written by another build costs the restore and nothing else.
    const terminal = ghostty.createTerminal(80, 24, {});
    terminal.write('content that survives a refused restore\r\n');

    expect(() => terminal.adoptSnapshot(new Uint8Array([0, 1, 2, 3, 4, 5, 6, 7]))).toThrow();

    expect([terminal.cols, terminal.rows]).toEqual([80, 24]);
    expect(viewportRows(terminal)[0]).toBe('content that survives a refused restore');
    terminal.write('and still takes input');
    expect(viewportRows(terminal)[1]).toBe('and still takes input');
    terminal.free();
  });

  it('keeps the terminal usable for live input', () => {
    const { terminal, history } = adopted();
    while (history.next() !== null) { /* drain */ }
    // The fixture's parser stopped mid-CSI; 'm' completes that sequence rather
    // than printing, which is only true if the continuation came back with it.
    terminal.write('mok');
    expect(viewportRows(terminal)[5]).toBe('prompt$ ok');
    terminal.free();
  });
});
