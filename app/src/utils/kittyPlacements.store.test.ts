// @vitest-environment node
// The placement store against the model the app actually renders.
//
// The store's whole job is turning the worker's screen-relative rows into rows
// of the client's buffer, and the only thing that makes that hard is scrollback:
// a mapping that forgets the history offset looks perfect on a terminal that has
// never scrolled. So these run against the real wasm ghostty with real history
// behind it, and assert on the TEXT the mapped row holds — a store that lands an
// image one row off, or on the screen instead of in the scrollback, fails here.
//
// @types/node isn't a direct dependency of this package (only a transitive peer
// of vite/vitest), matching terminalOsc133.parity.test.ts's pattern.
// @ts-expect-error -- see above
import { readFileSync } from 'node:fs';
// @ts-expect-error -- see above
import { fileURLToPath } from 'node:url';
import { Ghostty, type GhosttyCell, type GhosttyTerminal } from '../ghostty';
import { beforeAll, describe, expect, it } from 'vitest';
import { KittyPlacementStore } from './kittyPlacements';
import type { PlacementElement } from '../types/generated';

const wasmPath = fileURLToPath(new URL('../../vendor/ghostty-vt/ghostty-vt.wasm', import.meta.url));

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

function rowText(cells: GhosttyCell[] | null): string {
  if (!cells) return '';
  let text = '';
  for (const cell of cells) {
    if (cell.width === 0) continue;
    text += cell.codepoint === 0 ? ' ' : String.fromCodePoint(cell.codepoint);
  }
  return text.replace(/ +$/, '');
}

/** One absolute buffer row's text, scrollback and screen alike. */
function textAtBufferRow(term: GhosttyTerminal, bufferRow: number): string {
  const history = term.getScrollbackLength();
  return bufferRow < history
    ? rowText(term.getScrollbackLine(bufferRow))
    : rowText(term.getLine(bufferRow - history));
}

// A placement carrying only what the store reads; everything else is what the
// worker sends for a plain, freshly transmitted image.
function placement(overrides: Partial<PlacementElement> = {}): PlacementElement {
  return {
    image_id: 1,
    placement_id: 1,
    image_generation: 10,
    virtual: false,
    z: 0,
    viewport_row: 0,
    viewport_col: 0,
    viewport_visible: true,
    grid_cols: 0,
    grid_rows: 0,
    pixel_width: 64,
    pixel_height: 64,
    source_x: 0,
    source_y: 0,
    source_width: 0,
    source_height: 0,
    ...overrides,
  };
}

describe('KittyPlacementStore against the real terminal model', () => {
  let ghostty: Ghostty;

  beforeAll(async () => {
    ghostty = await loadGhostty();
  });

  // 40 lines through a 10-row screen: nine tenths of the buffer is history, so
  // any mapping that ignores it is off by dozens of rows.
  function scrolledTerminal(): GhosttyTerminal {
    const term = ghostty.createTerminal(40, 10, { scrollbackLimit: 10000 });
    const lines = Array.from({ length: 40 }, (_, i) => `line ${i}`).join('\r\n');
    term.write(new TextEncoder().encode(lines));
    term.update();
    return term;
  }

  it('anchors a placement to the buffer row the worker put it on', () => {
    const term = scrolledTerminal();
    expect(term.getScrollbackLength()).toBeGreaterThan(0);
    const store = new KittyPlacementStore();

    store.apply(1, [placement({ viewport_row: 3 })], term.getScrollbackLength());

    const [placed] = store.placements();
    expect(textAtBufferRow(term, placed.bufferRow)).toBe(rowText(term.getLine(3)));
  });

  it('anchors a placement that scrolled off the top into the scrollback', () => {
    const term = scrolledTerminal();
    const history = term.getScrollbackLength();
    const store = new KittyPlacementStore();

    // viewport_visible is advisory: a negative row that reports itself invisible
    // still anchors into history, and scrolling up must reveal it.
    store.apply(1, [placement({ viewport_row: -5, viewport_visible: false })], history);

    const [placed] = store.placements();
    expect(placed.bufferRow).toBe(history - 5);
    expect(textAtBufferRow(term, placed.bufferRow)).toBe(rowText(term.getScrollbackLine(history - 5)));
  });

  it('culls a placement mapped above the history the client still holds', () => {
    const term = scrolledTerminal();
    const history = term.getScrollbackLength();
    const store = new KittyPlacementStore();

    store.apply(1, [
      placement({ placement_id: 1, viewport_row: -history - 1 }),
      placement({ placement_id: 2, viewport_row: -history }),
    ], history);

    // Correct or absent: the one that maps to row -1 is dropped, the one that
    // maps to row 0 is the first row the client still has.
    expect(store.placements().map((p) => p.placementId)).toEqual([2]);
    expect(store.placements()[0].bufferRow).toBe(0);
  });

  it('re-maps the same description against the scrollback of the moment', () => {
    const term = scrolledTerminal();
    const store = new KittyPlacementStore();
    store.apply(1, [placement({ viewport_row: 2 })], term.getScrollbackLength());
    const before = store.placements()[0].bufferRow;

    term.write(new TextEncoder().encode('\r\nmore\r\nmore\r\nmore'));
    term.update();
    store.apply(2, [placement({ viewport_row: 2 })], term.getScrollbackLength());

    // The image did not move on screen, so it moved down the buffer by exactly
    // the history those writes produced.
    expect(store.placements()[0].bufferRow).toBe(before + 3);
    expect(textAtBufferRow(term, store.placements()[0].bufferRow)).toBe(rowText(term.getLine(2)));
  });
});

describe('KittyPlacementStore apply rules', () => {
  it('rejects a set older than the one already applied', () => {
    const store = new KittyPlacementStore();
    store.apply(5, [placement({ placement_id: 1 })], 0);

    expect(store.apply(4, [placement({ placement_id: 2 })], 0)).toBe(false);
    expect(store.placements().map((p) => p.placementId)).toEqual([1]);
    expect(store.lastAppliedSeq()).toBe(5);
  });

  it('accepts a set at the same seq, which is what a resize re-describes at', () => {
    const store = new KittyPlacementStore();
    store.apply(5, [placement({ placement_id: 1, viewport_row: 0 })], 0);

    expect(store.apply(5, [placement({ placement_id: 1, viewport_row: 4 })], 0)).toBe(true);
    expect(store.placements()[0].bufferRow).toBe(4);
  });

  it('replaces the set wholesale rather than merging', () => {
    const store = new KittyPlacementStore();
    store.apply(1, [
      placement({ placement_id: 1, image_id: 1 }),
      placement({ placement_id: 2, image_id: 2 }),
    ], 0);

    store.apply(2, [placement({ placement_id: 3, image_id: 3 })], 0);

    expect(store.placements().map((p) => p.imageId)).toEqual([3]);
  });

  it('clears on the empty set, which is how a program says the image is gone', () => {
    const store = new KittyPlacementStore();
    store.apply(1, [placement()], 0);

    store.apply(2, [], 0);

    expect(store.placements()).toHaveLength(0);
  });

  it('skips a virtual placement, which the program draws itself', () => {
    const store = new KittyPlacementStore();
    store.apply(1, [
      placement({ placement_id: 1, virtual: true }),
      placement({ placement_id: 2 }),
    ], 0);

    expect(store.placements().map((p) => p.placementId)).toEqual([2]);
  });

  it('orders the set by z, then by placement id', () => {
    const store = new KittyPlacementStore();
    store.apply(1, [
      placement({ placement_id: 9, z: 5 }),
      placement({ placement_id: 3, z: -1 }),
      placement({ placement_id: 1, z: 5 }),
    ], 0);

    expect(store.placements().map((p) => p.placementId)).toEqual([3, 1, 9]);
  });

  it('drops what a restore does not carry, so no image outlives a reattach', () => {
    const store = new KittyPlacementStore();
    store.apply(7, [placement()], 0);

    store.seed([], 0);

    expect(store.placements()).toHaveLength(0);
    // The live stream resumes from the attach watermark; holding the old seq
    // would reject its first descriptions.
    expect(store.apply(1, [placement()], 0)).toBe(true);
  });

  it('takes the restore snapshot as the whole truth', () => {
    const store = new KittyPlacementStore();
    store.apply(7, [placement({ placement_id: 1 })], 0);

    store.seed([placement({ placement_id: 2, viewport_row: 1 })], 12);

    expect(store.placements().map((p) => p.placementId)).toEqual([2]);
    expect(store.placements()[0].bufferRow).toBe(13);
  });
});
