import { describe, it, expect } from 'vitest';
import { GridCompositor, type GridTileSpec } from './GridCompositor';
import { EMPTY_STATS, type GridRenderer, type TileFrame, type TileModel } from './GridRenderer';

// Minimal fakes: the compositor takes its renderer + ghostty + container as
// injected deps, so we can exercise all of its logic without WebGL or the WASM
// VT engine. Only the handful of model methods the compositor actually calls are
// implemented.

interface FakeCell {
  codepoint: number;
  width: number;
}

function viewportFromText(cols: number, rows: number, text: string): FakeCell[] {
  const lines = text.split('\n');
  const cells: FakeCell[] = [];
  for (let r = 0; r < rows; r += 1) {
    const line = lines[r] ?? '';
    for (let c = 0; c < cols; c += 1) {
      const ch = line[c];
      cells.push({ codepoint: ch ? (ch.codePointAt(0) ?? 32) : 32, width: 1 });
    }
  }
  return cells;
}

class FakeModel {
  writes: Uint8Array[] = [];
  responses: string[] = [];
  resizes: Array<{ cols: number; rows: number }> = [];
  modes: Record<number, boolean> = {};
  freed = false;
  viewport: FakeCell[];

  constructor(public cols: number, public rows: number, text = '') {
    this.viewport = viewportFromText(cols, rows, text);
  }

  setText(text: string) {
    this.viewport = viewportFromText(this.cols, this.rows, text);
  }

  write(data: Uint8Array | string) {
    this.writes.push(data as Uint8Array);
  }
  resize(cols: number, rows: number) {
    this.cols = cols;
    this.rows = rows;
    this.resizes.push({ cols, rows });
    this.viewport = viewportFromText(cols, rows, '');
  }
  update() {
    return 0;
  }
  markClean() {}
  getViewport() {
    return this.viewport;
  }
  getCursor() {
    return { x: 0, y: 0, visible: false };
  }
  getMode(mode: number) {
    return this.modes[mode] ?? false;
  }
  hasResponse() {
    return this.responses.length > 0;
  }
  readResponse() {
    return this.responses.shift() ?? null;
  }
  getGraphemeString() {
    return '';
  }
  getScrollbackLength() {
    return 0;
  }
  free() {
    this.freed = true;
  }
}

class FakeRenderer implements GridRenderer {
  readonly name = 'fake';
  tiles: TileModel[] = [];
  frames: TileFrame[][] = [];
  disposed = false;
  mount() {}
  setTiles(tiles: TileModel[]) {
    this.tiles = tiles;
  }
  frame(frames: TileFrame[]) {
    this.frames.push(frames);
    return EMPTY_STATS;
  }
  dispose() {
    this.disposed = true;
  }
}

function makeCompositor() {
  const created: FakeModel[] = [];
  const ghostty = {
    createTerminal(cols: number, rows: number) {
      const model = new FakeModel(cols, rows);
      created.push(model);
      return model;
    },
  };
  const container = {
    clientWidth: 800,
    clientHeight: 600,
    getBoundingClientRect: () => ({ left: 0, top: 0, right: 800, bottom: 600, width: 800, height: 600 }),
  };
  const metrics = { cellWidth: 8, cellHeight: 16, baseline: 12 };
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const comp = new GridCompositor(new FakeRenderer(), ghostty as any, container as any, metrics, {} as any);
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const renderer = (comp as any).renderer as FakeRenderer;
  return { comp, renderer, created };
}

function tileSpec(
  id: string,
  overrides: Partial<Omit<GridTileSpec, 'id'>> = {},
): GridTileSpec {
  return { id, attention: false, state: 'working', ...overrides };
}

describe('GridCompositor', () => {
  it('creates one model per tile spec and registers them with the renderer', () => {
    const { comp, renderer, created } = makeCompositor();
    comp.syncTiles([tileSpec('a'), tileSpec('b', { attention: true, state: 'waiting_input' })]);

    expect(created).toHaveLength(2);
    expect(renderer.tiles.map((t) => t.id)).toEqual(['a', 'b']);
    expect(comp.hasTile('a')).toBe(true);
    expect(comp.hasTile('b')).toBe(true);
  });

  it('preserves existing models across syncs and frees only removed ones', () => {
    const { comp, created } = makeCompositor();
    comp.syncTiles([tileSpec('a'), tileSpec('b')]);
    const [modelA, modelB] = created;

    comp.syncTiles([tileSpec('a')]); // drop b

    expect(created).toHaveLength(2); // no new model created for the kept tile
    expect(modelA.freed).toBe(false);
    expect(modelB.freed).toBe(true);
    expect(comp.hasTile('b')).toBe(false);
  });

  it('forces a render when a tile is removed even if the grid shape is unchanged', () => {
    // Regression: the rAF loop is render-on-demand (it paints only when a model
    // is dirty or an animation is live). Fake models never report dirty, so this
    // isolates the membership path: removing a tile while the resolved shape stays
    // 2×2 must still trigger a render, or the removed tile's stale frame lingers
    // until the next dirtying event ("only every other hide actually hides").
    const { comp, renderer } = makeCompositor();
    const spec = (id: string) => tileSpec(id);
    comp.syncTiles(['a', 'b', 'c', 'd'].map(spec));
    comp.setLayout(2, 2);

    // Settle the mount/layout reflow so the renderer is idle, then confirm an idle
    // tick draws nothing (the render-on-demand baseline this bug violated).
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const internal = comp as any;
    internal.reflowStart = -1;
    internal.tick();
    const before = renderer.frames.length;
    internal.tick();
    expect(renderer.frames.length).toBe(before);

    // Remove one tile; the resolved shape is still 2×2, so setLayout no-ops.
    comp.syncTiles(['a', 'b', 'c'].map(spec));
    comp.setLayout(2, 2);
    internal.tick();

    expect(renderer.frames.length).toBeGreaterThan(before);
    const last = renderer.frames[renderer.frames.length - 1];
    expect(last.map((f) => f.id)).toEqual(['a', 'b', 'c']);
  });

  it('renders state-only changes even when the terminal model is clean', () => {
    const { comp, renderer } = makeCompositor();
    const internal = comp as any;
    comp.syncTiles([tileSpec('a')]);
    internal.reflowStart = -1;
    internal.tick();
    const before = renderer.frames.length;

    comp.syncTiles([tileSpec('a', { state: 'idle' })]);
    internal.tick();

    expect(renderer.frames.length).toBe(before + 1);
    expect(renderer.frames[renderer.frames.length - 1]?.[0]).toMatchObject({
      state: 'idle',
      statePresentation: 'border',
    });
  });

  it('renders immediately when the state presentation changes', () => {
    const { comp, renderer } = makeCompositor();
    const internal = comp as any;
    comp.syncTiles([tileSpec('a')]);
    internal.reflowStart = -1;
    internal.tick();
    const before = renderer.frames.length;

    comp.setStatePresentation('background');
    internal.tick();

    expect(renderer.frames.length).toBe(before + 1);
    expect(renderer.frames[renderer.frames.length - 1]?.[0].statePresentation).toBe('background');
  });

  it('keeps rendering while a visible session is waiting for input', () => {
    const { comp, renderer } = makeCompositor();
    const internal = comp as any;
    comp.syncTiles([tileSpec('a', { state: 'waiting_input', attention: false })]);
    internal.reflowStart = -1;
    internal.tick();
    const before = renderer.frames.length;

    internal.tick();

    expect(renderer.frames.length).toBe(before + 1);
  });

  it('routes live bytes to the matching model and drains responses without echoing them', () => {
    const { comp, created } = makeCompositor();
    comp.syncTiles([tileSpec('a')]);
    const model = created[0];
    model.responses = ['\x1b[0n', 'extra'];

    comp.writeBytes('a', new Uint8Array([104, 105]));

    expect(model.writes).toHaveLength(1);
    expect(Array.from(model.writes[0])).toEqual([104, 105]);
    // Observer must consume responses (so the engine doesn't buffer) but never
    // send them anywhere — the real pane answers terminal queries.
    expect(model.responses).toEqual([]);

    expect(() => comp.writeBytes('unknown', new Uint8Array([1]))).not.toThrow();
  });

  it('exposes a tile\'s visible screen text and reports emptiness', () => {
    const { comp, created } = makeCompositor();
    comp.syncTiles([tileSpec('a')]);
    const model = created[0];

    expect(comp.getTileText('a')).toBe('');
    expect(comp.tileSummaries()[0].nonEmpty).toBe(false);

    model.setText('hello world');
    expect(comp.getTileText('a')).toBe('hello world');
    expect(comp.getTileText('missing')).toBeNull();

    const summary = comp.tileSummaries()[0];
    expect(summary).toMatchObject({ id: 'a', cols: 80, rows: 24, nonEmpty: true });
  });

  it('tracks zoom state and cancels zoom when the zoomed tile disappears', () => {
    const { comp } = makeCompositor();
    comp.syncTiles([tileSpec('a'), tileSpec('b')]);

    expect(comp.isZoomed()).toBe(false);
    comp.zoomTo('a');
    expect(comp.isZoomed()).toBe(true);
    expect(comp.zoomedId()).toBe('a');

    comp.syncTiles([tileSpec('b')]); // remove the zoomed tile
    expect(comp.zoomedId()).toBeNull();
    expect(comp.isZoomed()).toBe(false);
  });

  it('reports the zoomed tile\'s terminal mode for input encoding (off when nothing is zoomed)', () => {
    const { comp, created } = makeCompositor();
    comp.syncTiles([tileSpec('a'), tileSpec('b')]);
    created[0].modes[1] = true; // tile a: application cursor keys on; tile b: off

    // Overview: no input target, so modes read off regardless of any model.
    expect(comp.getMode(1)).toBe(false);

    comp.zoomTo('a');
    expect(comp.getMode(1)).toBe(true);

    comp.zoomTo('b'); // switching the zoom switches the input target
    expect(comp.getMode(1)).toBe(false);
  });

  it('toggles tile visibility', () => {
    const { comp } = makeCompositor();
    comp.syncTiles([tileSpec('a')]);
    expect(comp.tileSummaries()[0].hidden).toBe(false);
    comp.toggleHide('a');
    expect(comp.tileSummaries()[0].hidden).toBe(true);
  });

  it('resolves the resting tile + rect under a pointer, on demand without a render loop', () => {
    const { comp } = makeCompositor(); // container 800×600, cell 8×16, models 80×24
    comp.syncTiles([tileSpec('a')]);
    comp.setLayout(1, 1);

    // A single 80×24 tile fits 800×600 at scale 1.25 → 800×480, centred vertically
    // (y inset (600-480)/2 = 60). Centre of the grid hits it.
    const hit = comp.tileAt(400, 300);
    expect(hit?.id).toBe('a');
    expect(hit?.rect).toMatchObject({ x: 0, w: 800, h: 480 });
    expect(hit?.rect.y).toBeCloseTo(60);

    // Above the letterboxed tile (y < 60) is empty space.
    expect(comp.tileAt(400, 30)).toBeNull();
  });

  it('frees every model on dispose', () => {
    const { comp, renderer, created } = makeCompositor();
    comp.syncTiles([tileSpec('a'), tileSpec('b')]);
    comp.dispose();
    expect(created.every((m) => m.freed)).toBe(true);
    expect(renderer.disposed).toBe(true);
  });
});

describe('GridCompositor seeding + sequence dedup', () => {
  const bytes = (...vals: number[]) => new Uint8Array(vals);
  const written = (model: FakeModel) => model.writes.map((w) => Array.from(w));

  it('applies live bytes immediately and drops chunks at or below the watermark', () => {
    const { comp, created } = makeCompositor();
    comp.syncTiles([tileSpec('a')]);
    const model = created[0];

    comp.writeBytes('a', bytes(1), 5); // first seq sets the watermark
    comp.writeBytes('a', bytes(2), 3); // stale: already painted, dropped
    comp.writeBytes('a', bytes(3), 6); // advances past watermark, applied
    comp.writeBytes('a', bytes(4)); // seq-less: cannot be proven stale, applied

    expect(written(model)).toEqual([[1], [3], [4]]);
  });

  it('buffers live bytes while seeding, then paints the snapshot before flushing newer ones', () => {
    const { comp, created } = makeCompositor();
    comp.syncTiles([tileSpec('a')]);
    const model = created[0];

    comp.beginSeeding('a');
    comp.writeBytes('a', bytes(9), 8); // will be <= seed watermark -> dropped
    comp.writeBytes('a', bytes(11), 11); // newer than the snapshot -> kept
    expect(model.writes).toHaveLength(0); // nothing painted until the seed lands

    comp.seedTile('a', bytes(100, 101), 10, 80, 24);

    // Snapshot paints first; only the buffered chunk past seq 10 follows.
    expect(written(model)).toEqual([[100, 101], [11]]);
  });

  it('resizes the model to the snapshot geometry so an absolute repaint is not clamped', () => {
    const { comp, created } = makeCompositor();
    comp.syncTiles([tileSpec('a')]);
    const model = created[0];
    expect([model.cols, model.rows]).toEqual([80, 24]);

    comp.beginSeeding('a');
    comp.seedTile('a', bytes(1), 0, 120, 40);

    expect([model.cols, model.rows]).toEqual([120, 40]);
    expect(model.resizes).toContainEqual({ cols: 120, rows: 40 });
  });

  it('flushes buffered bytes best-effort when seeding is cancelled (no snapshot)', () => {
    const { comp, created } = makeCompositor();
    comp.syncTiles([tileSpec('a')]);
    const model = created[0];

    comp.beginSeeding('a');
    comp.writeBytes('a', bytes(1));
    comp.writeBytes('a', bytes(2));
    expect(model.writes).toHaveLength(0);

    comp.cancelSeeding('a');

    expect(written(model)).toEqual([[1], [2]]);
  });

  it('ignores seedTile unless the tile is awaiting a seed', () => {
    const { comp, created } = makeCompositor();
    comp.syncTiles([tileSpec('a')]);
    const model = created[0];

    // Tiles default to live; a stray seed must not clobber live content.
    comp.seedTile('a', bytes(1), 5);
    expect(model.writes).toHaveLength(0);
  });

  it('tracks live geometry via resizeTile and no-ops on unchanged or invalid sizes', () => {
    const { comp, created } = makeCompositor();
    comp.syncTiles([tileSpec('a')]);
    const model = created[0];

    comp.resizeTile('a', 100, 30);
    expect([model.cols, model.rows]).toEqual([100, 30]);

    comp.resizeTile('a', 100, 30); // unchanged
    comp.resizeTile('a', 0, 0); // invalid
    expect(model.resizes).toHaveLength(1);
  });
});
