// @vitest-environment node
// The native-to-wasm half of the kitty wire-rewrite parity gate.
//
// internal/pty/testdata/kitty_rewrite_corpus.json records, per entry, the bytes
// the worker's feeder put on the wire in place of each kitty APC and the grid
// the worker itself ended up with. internal/pty/kittycorpus_test.go proves a
// native ghostty that cannot parse kitty reproduces that grid from those bytes.
// This file proves the same of the model the app actually renders: the shipped
// wasm build, which can never parse kitty at any ghostty pin.
//
// That distinction is the whole reason this layer exists. The worker grid backs
// approval classification and the restore dump, so a grid the wasm model cannot
// reproduce shows up as the screen changing under the user on reattach. Only
// this side proves the synthesized bytes MEAN the same thing to the real client.
//
// Regenerate the corpus with:
//   go test ./internal/pty -run TestKittyWireRewriteCorpus -update
// @types/node isn't a direct dependency of this package (only a transitive peer
// of vite/vitest), matching terminalOsc133.parity.test.ts's pattern.
// @ts-expect-error -- see above
import { readFileSync } from 'node:fs';
// @ts-expect-error -- see above
import { fileURLToPath } from 'node:url';
import { Ghostty, type GhosttyCell, type GhosttyTerminal } from '../ghostty';
import { describe, expect, it } from 'vitest';

interface CorpusEntry {
  name: string;
  cols: number;
  rows: number;
  chunks: string[];
  wire: string[];
  resync: string[];
  workerPlainText: string;
  workerViewportText: string;
  cursorCol: number;
  cursorRow: number;
}

const corpusPath = fileURLToPath(
  new URL('../../../internal/pty/testdata/kitty_rewrite_corpus.json', import.meta.url),
);
const corpus = JSON.parse(readFileSync(corpusPath, 'utf8')) as { entries: CorpusEntry[] };

const wasmPath = fileURLToPath(new URL('../../vendor/ghostty-vt/ghostty-vt.wasm', import.meta.url));

// Matches internal/ghosttyvt's DefaultMaxScrollback, so the two models retain
// the same history and a scrolled entry compares like for like.
const SCROLLBACK_LIMIT = 10000;

async function loadGhostty(): Promise<Ghostty> {
  const bytes = readFileSync(wasmPath);
  const mod = await WebAssembly.compile(bytes);
  let instance: WebAssembly.Instance;
  instance = await WebAssembly.instantiate(mod, {
    env: {
      log: (ptr: number, len: number) => {
        const memory = (instance.exports.memory as WebAssembly.Memory).buffer;
        console.log('[ghostty-vt]', new TextDecoder().decode(new Uint8Array(memory, ptr, len)));
      },
    },
  });
  return new Ghostty(instance);
}

// One row as text, up to the last cell the program actually wrote. A cell that
// was never written reads as codepoint 0; a cell written with a space reads as
// 32, and the two are NOT interchangeable at the end of a row — ghostty's plain
// formatter keeps the written space and drops the untouched cell. The corpus
// entries are ASCII, so a cell's codepoint is its whole grapheme.
function rowText(cells: GhosttyCell[]): string {
  let end = cells.length;
  while (end > 0 && cells[end - 1].codepoint === 0) end--;
  let text = '';
  for (let i = 0; i < end; i++) {
    const cell = cells[i];
    if (cell.width === 0) continue;
    text += cell.codepoint === 0 ? ' ' : String.fromCodePoint(cell.codepoint);
  }
  return text;
}

// Mirrors ghosttyvt.Terminal.ViewportText: one line per viewport row, trailing
// blanks trimmed per row (written spaces included, which is where this differs
// from the history below), every row terminated with \n.
function viewportText(term: GhosttyTerminal): string {
  let out = '';
  for (let y = 0; y < term.rows; y++) {
    out += `${rowText(term.getLine(y) ?? []).replace(/ +$/, '')}\n`;
  }
  return out;
}

// Mirrors ghostty's plain formatter, which ghosttyvt.Terminal.PlainText returns
// unnormalized: scrollback then viewport, each row cut at its last written cell,
// rows joined by \n, trailing untouched rows dropped, no terminating newline.
function plainText(term: GhosttyTerminal): string {
  const rows: string[] = [];
  for (let offset = 0; offset < term.getScrollbackLength(); offset++) {
    rows.push(rowText(term.getScrollbackLine(offset) ?? []));
  }
  for (let y = 0; y < term.rows; y++) {
    rows.push(rowText(term.getLine(y) ?? []));
  }
  while (rows.length > 0 && rows[rows.length - 1] === '') rows.pop();
  return rows.join('\n');
}

// atob rather than node:Buffer: this file already reaches for Node APIs the
// package does not type, and the wire chunks are bytes, not text.
function decodeBase64(encoded: string): Uint8Array {
  const binary = atob(encoded);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes;
}

function replayWire(term: GhosttyTerminal, entry: CorpusEntry): void {
  for (const encoded of entry.wire) {
    // "" is a chunk the feeder held: the read loop skips the fan-out for it, so
    // nothing reaches the client.
    if (encoded === '') continue;
    term.write(decodeBase64(encoded));
  }
  term.update();
}

describe('kitty wire rewrite replayed into the wasm model', () => {
  it('covers every corpus entry', () => {
    expect(corpus.entries.length).toBeGreaterThan(0);
  });

  for (const entry of corpus.entries) {
    // An entry that forced a resync deliberately puts nothing on the wire for
    // the chunk that failed observation; the snapshot re-push is what makes the
    // client whole, so there is no equality to assert here.
    const resynced = entry.resync.some((reason) => reason !== '');
    const run = resynced ? it.skip : it;

    run(entry.name, async () => {
      const ghostty = await loadGhostty();
      const term = ghostty.createTerminal(entry.cols, entry.rows, {
        scrollbackLimit: SCROLLBACK_LIMIT,
      });
      replayWire(term, entry);

      expect(viewportText(term)).toBe(entry.workerViewportText);
      expect(plainText(term)).toBe(entry.workerPlainText);
      const cursor = term.getCursor();
      expect({ col: cursor.x, row: cursor.y }).toEqual({
        col: entry.cursorCol,
        row: entry.cursorRow,
      });
    });
  }
});
