// The compositor owns everything that is NOT drawing: the terminal models, the
// rAF loop, layout/grid math, the animation clocks (zoom morph, reflow,
// attention pulse), focus, and hit-testing. Each frame it computes an animated
// TileFrame per tile and hands the array to the GridRenderer.
//
// In grid mode the models are fed from the live PTY stream (see GridView): bytes
// arrive via writeBytes(); the compositor is a pure sink and OBSERVER — it
// processes terminal responses but never echoes them back to the PTY (that would
// inject phantom input into the user's session).
import type { Ghostty, GhosttyTerminal } from 'ghostty-web';
import type { UISessionState } from '../../types/sessionState';
import type { GridRenderer, GridRenderStats, Rect, TileFrame } from './GridRenderer';
import { TILE_COLS, TILE_ROWS, type CellMetrics } from './gridConfig';
import type { GridStatePresentation } from './gridStatePresentation';

const GAP = 12;
const REFLOW_MS = 280;
const ZOOM_MS = 300;
const ATTENTION_RATE = 5; // per second toward target

// While a tile waits for its seed snapshot, live firehose chunks are buffered so
// none are lost and none double-paint over the snapshot. Seeding is one
// websocket round-trip (~ms), so this only fills for genuinely active sessions;
// the cap bounds memory if a session floods. On overflow the OLDEST chunk is
// dropped — it is the most likely to predate the snapshot watermark and be
// discarded by dedup anyway.
const MAX_SEED_BUFFER = 512;

type TerminalConfig = Parameters<Ghostty['createTerminal']>[2];

// Identity + presentation hint for one tile. `id` is the session's PTY runtimeId
// — the same id PTY output events are keyed by, so writeBytes() routes by it.
export interface GridTileSpec {
  id: string;
  attention: boolean;
  state: UISessionState;
}

export interface CompositorStats extends GridRenderStats {
  fps: number;
  frameMsP50: number;
  frameMsP95: number;
  frameMsMax: number;
  dropped: number;
  tileCount: number;
  rendered: boolean;
}

// Per-tile state for testing/introspection (no pixels required).
export interface GridTileSummary {
  id: string;
  attention: number;
  hidden: boolean;
  focused: boolean;
  cols: number;
  rows: number;
  nonEmpty: boolean;
}

interface Tile {
  id: string;
  model: GhosttyTerminal;
  state: UISessionState;
  attention: number;
  attentionTarget: number;
  hidden: boolean;
  // Seeding/seq state. A tile is 'live' by default (live firehose applies
  // immediately, preserving the pre-seeding behavior). GridView flips it to
  // 'seeding' while it fetches the snapshot; writeBytes then buffers into
  // `pending` until seedTile/cancelSeeding drains it. `lastSeq` is the sequence
  // watermark: live chunks with seq <= lastSeq are already baked into the
  // snapshot and dropped. -1 means "no watermark yet" (apply everything).
  phase: 'seeding' | 'live';
  lastSeq: number;
  pending: Array<{ data: Uint8Array; seq: number | undefined }>;
}

interface Layout {
  rows: number;
  cols: number;
}

const easeOutCubic = (t: number): number => 1 - (1 - t) ** 3;
const clamp01 = (t: number): number => (t < 0 ? 0 : t > 1 ? 1 : t);
const lerp = (a: number, b: number, t: number): number => a + (b - a) * t;
const lerpRect = (a: Rect, b: Rect, t: number): Rect => ({
  x: lerp(a.x, b.x, t),
  y: lerp(a.y, b.y, t),
  w: lerp(a.w, b.w, t),
  h: lerp(a.h, b.h, t),
});

export class GridCompositor {
  private readonly renderer: GridRenderer;
  private readonly ghostty: Ghostty;
  private readonly container: HTMLElement;
  private readonly metrics: CellMetrics;
  private readonly modelOptions: TerminalConfig;

  private tiles: Tile[] = [];
  private tileIndex = new Map<string, Tile>();
  private layout: Layout = { rows: 1, cols: 1 };

  private rafId: number | null = null;
  private lastNow = 0;

  // reflow animation
  private reflowFrom = new Map<string, Rect>();
  private reflowStart = -1;

  // zoom animation
  private zoomId: string | null = null;
  private zoomFrom = 0;
  private zoomTarget = 0;
  private zoomStart = -1;
  private zoomT = 0;

  private lastFrames: TileFrame[] = [];
  private frameMs: number[] = [];
  private lastStats: GridRenderStats | null = null;
  private lastCompositorStats: CompositorStats | null = null;
  private statsEmitAt = 0;
  private statePresentation: GridStatePresentation = 'border';
  private presentationDirty = true;

  onStats: ((stats: CompositorStats) => void) | null = null;

  constructor(
    renderer: GridRenderer,
    ghostty: Ghostty,
    container: HTMLElement,
    metrics: CellMetrics,
    modelOptions: TerminalConfig,
  ) {
    this.renderer = renderer;
    this.ghostty = ghostty;
    this.container = container;
    this.metrics = metrics;
    this.modelOptions = modelOptions;
    renderer.mount(container);
  }

  setLayout(rows: number, cols: number): void {
    if (rows === this.layout.rows && cols === this.layout.cols) return;
    // Snapshot current placement so tiles slide from where they are.
    this.beginReflow();
    this.layout = { rows, cols };
  }

  setStatePresentation(presentation: GridStatePresentation): void {
    if (presentation === this.statePresentation) return;
    this.statePresentation = presentation;
    this.presentationDirty = true;
  }

  // Snapshot every tile's resting rect under the CURRENT layout and start the
  // reflow clock, so the next layout/membership change animates from where tiles
  // are and — critically — forces the render-on-demand loop to paint. Call this
  // BEFORE mutating this.layout or this.tiles.
  private beginReflow(): void {
    this.reflowFrom.clear();
    this.tiles.forEach((tile, i) => {
      this.reflowFrom.set(tile.id, this.baseRect(i, this.layout, tile.model.cols, tile.model.rows).rect);
    });
    this.reflowStart = performance.now();
  }

  // Reconcile the live tile set against `specs` (in placement order). Existing
  // models are preserved by id; new sessions get a fresh model; gone sessions are
  // freed. Attention targets are refreshed every sync.
  syncTiles(specs: GridTileSpec[]): void {
    const nextIds = new Set(specs.map((s) => s.id));
    // A change to the tile set (removal, restore, reorder) must animate the
    // survivors into their new slots AND force at least one render: the rAF loop
    // is render-on-demand and a pure membership change with an unchanged grid
    // shape dirties nothing, so without this the removed tile's stale frame stays
    // on screen until the next dirtying event (e.g. another removal). Snapshot
    // from the current placement before we mutate the tile list. Skip the initial
    // mount (no prior tiles to slide from).
    const membershipChanged =
      this.tiles.length > 0 &&
      (specs.length !== this.tiles.length || specs.some((s, i) => this.tiles[i]?.id !== s.id));
    if (membershipChanged) this.beginReflow();

    for (const tile of this.tiles) {
      if (!nextIds.has(tile.id)) {
        tile.model.free();
        if (this.zoomId === tile.id) this.cancelZoom();
      }
    }

    const next: Tile[] = specs.map((spec) => {
      const existing = this.tileIndex.get(spec.id);
      if (existing) {
        if (existing.state !== spec.state || existing.attentionTarget !== (spec.attention ? 1 : 0)) {
          this.presentationDirty = true;
        }
        existing.state = spec.state;
        existing.attentionTarget = spec.attention ? 1 : 0;
        return existing;
      }
      this.presentationDirty = true;
      const model = this.ghostty.createTerminal(TILE_COLS, TILE_ROWS, this.modelOptions);
      return {
        id: spec.id,
        model,
        state: spec.state,
        attention: 0,
        attentionTarget: spec.attention ? 1 : 0,
        hidden: false,
        phase: 'live',
        lastSeq: -1,
        pending: [],
      };
    });

    this.tiles = next;
    this.tileIndex = new Map(next.map((t) => [t.id, t]));
    this.renderer.setTiles(this.tiles.map((t) => ({ id: t.id, model: t.model })));
  }

  // Feed live PTY bytes into one tile's model. `seq` is the firehose sequence
  // number (undefined for seq-less events like resets/replays, which can't be
  // proven stale and so always apply). While seeding, bytes are buffered; once
  // live, chunks at or below the seed watermark are dropped as already-painted.
  // OBSERVER: responses are drained, never echoed — the real pane answers
  // terminal queries.
  writeBytes(id: string, data: Uint8Array, seq?: number): void {
    const tile = this.tileIndex.get(id);
    if (!tile) return;
    if (tile.phase === 'seeding') {
      tile.pending.push({ data, seq });
      if (tile.pending.length > MAX_SEED_BUFFER) tile.pending.shift();
      return;
    }
    this.applyBytes(tile, data, seq);
  }

  // Mark a tile as awaiting its seed snapshot. Subsequent writeBytes buffer
  // instead of painting, so the snapshot lands on an empty model and no live
  // bytes are lost or double-applied. Idempotent.
  beginSeeding(id: string): void {
    const tile = this.tileIndex.get(id);
    if (!tile || tile.phase === 'seeding') return;
    tile.phase = 'seeding';
    tile.lastSeq = -1;
    tile.pending = [];
  }

  // Paint a session's current screen into a tile, then go live. The model is
  // resized to the snapshot geometry first (the snapshot is an absolute repaint
  // at the session's real cols/rows; a mismatched model would clamp it). Buffered
  // chunks newer than `lastSeq` are then flushed in order.
  seedTile(id: string, snapshot: Uint8Array, lastSeq: number, cols?: number, rows?: number): void {
    const tile = this.tileIndex.get(id);
    if (!tile || tile.phase !== 'seeding') return;
    if (cols && rows && (tile.model.cols !== cols || tile.model.rows !== rows)) {
      tile.model.resize(cols, rows);
    }
    this.applyBytes(tile, snapshot, undefined);
    tile.lastSeq = lastSeq;
    tile.phase = 'live';
    this.flushPending(tile);
  }

  // Abandon seeding (snapshot unavailable: disconnected, session gone, or an
  // old worker). Go live and flush whatever buffered, best-effort — there is no
  // watermark to dedup against, matching the pre-seeding apply-everything path.
  cancelSeeding(id: string): void {
    const tile = this.tileIndex.get(id);
    if (!tile || tile.phase !== 'seeding') return;
    tile.phase = 'live';
    this.flushPending(tile);
  }

  // Track a session's live geometry (from firehose local_resize) so the tile
  // model keeps matching the source and the layout math scales it correctly.
  resizeTile(id: string, cols: number, rows: number): void {
    const tile = this.tileIndex.get(id);
    if (!tile || cols <= 0 || rows <= 0) return;
    if (tile.model.cols === cols && tile.model.rows === rows) return;
    tile.model.resize(cols, rows);
  }

  hasTile(id: string): boolean {
    return this.tileIndex.has(id);
  }

  toggleHide(id: string): void {
    const tile = this.tileIndex.get(id);
    if (tile) tile.hidden = !tile.hidden;
  }

  zoomTo(id: string | null): void {
    const now = performance.now();
    if (id) {
      this.zoomId = id;
      this.zoomFrom = this.zoomT;
      this.zoomTarget = 1;
    } else {
      this.zoomFrom = this.zoomT;
      this.zoomTarget = 0;
    }
    this.zoomStart = now;
  }

  isZoomed(): boolean {
    return this.zoomId !== null && this.zoomTarget === 1;
  }

  zoomedId(): string | null {
    return this.zoomTarget === 1 ? this.zoomId : null;
  }

  // Query a terminal mode (e.g. application cursor keys) of the current input
  // target — the zoomed tile — so an InputHandler can encode keys the way that
  // session expects. No target (overview) reports modes off.
  getMode(mode: number): boolean {
    const id = this.zoomedId();
    const tile = id ? this.tileIndex.get(id) : null;
    return tile ? tile.model.getMode(mode) : false;
  }

  // --- introspection (testing / automation; no pixels required) -------------

  getStats(): CompositorStats | null {
    return this.lastCompositorStats;
  }

  currentLayout(): Layout {
    return { ...this.layout };
  }

  tileSummaries(): GridTileSummary[] {
    const focusedById = new Map(this.lastFrames.map((f) => [f.id, f.focused]));
    return this.tiles.map((tile) => ({
      id: tile.id,
      attention: tile.attention,
      hidden: tile.hidden,
      focused: focusedById.get(tile.id) ?? false,
      cols: tile.model.cols,
      rows: tile.model.rows,
      nonEmpty: this.modelNonEmpty(tile.model),
    }));
  }

  // The visible screen text of one tile's model, for content assertions.
  getTileText(id: string): string | null {
    const tile = this.tileIndex.get(id);
    if (!tile) return null;
    return this.modelText(tile.model);
  }

  // Pointer (client coords) -> the resting tile at that point, with its
  // container-space rect. Unlike hitTest (which reads the last animated frame),
  // this recomputes the static grid placement on demand, so it is correct for the
  // non-animating overview — where hover affordances like the per-tile remove
  // button live — and is deterministic without a running render loop.
  tileAt(clientX: number, clientY: number): { id: string; rect: Rect } | null {
    const box = this.container.getBoundingClientRect();
    const x = clientX - box.left;
    const y = clientY - box.top;
    for (let i = 0; i < this.tiles.length; i += 1) {
      const tile = this.tiles[i];
      if (tile.hidden) continue;
      const { rect } = this.baseRect(i, this.layout, tile.model.cols, tile.model.rows);
      if (x >= rect.x && x <= rect.x + rect.w && y >= rect.y && y <= rect.y + rect.h) {
        return { id: tile.id, rect };
      }
    }
    return null;
  }

  // Pointer (client coords) -> tile id, using the last rendered frame placement.
  hitTest(clientX: number, clientY: number): string | null {
    const box = this.container.getBoundingClientRect();
    const x = clientX - box.left;
    const y = clientY - box.top;
    // Iterate in reverse so the zoomed/topmost tile wins.
    for (let i = this.lastFrames.length - 1; i >= 0; i -= 1) {
      const f = this.lastFrames[i];
      if (f.hidden || f.alpha <= 0.02) continue;
      if (x >= f.rect.x && x <= f.rect.x + f.rect.w && y >= f.rect.y && y <= f.rect.y + f.rect.h) {
        return f.id;
      }
    }
    return null;
  }

  start(): void {
    if (this.rafId !== null) return;
    this.lastNow = performance.now();
    const loop = () => {
      this.tick();
      this.rafId = requestAnimationFrame(loop);
    };
    this.rafId = requestAnimationFrame(loop);
  }

  dispose(): void {
    if (this.rafId !== null) cancelAnimationFrame(this.rafId);
    this.rafId = null;
    this.tiles.forEach((tile) => tile.model.free());
    this.tiles = [];
    this.tileIndex.clear();
    this.renderer.dispose();
  }

  // --- internals -----------------------------------------------------------

  private cancelZoom(): void {
    this.zoomId = null;
    this.zoomT = 0;
    this.zoomTarget = 0;
  }

  // Write to the model with seq dedup, advancing the watermark. A seq-bearing
  // chunk at or below lastSeq is already on screen, so it is dropped.
  private applyBytes(tile: Tile, data: Uint8Array, seq: number | undefined): void {
    if (seq !== undefined && tile.lastSeq >= 0 && seq <= tile.lastSeq) return;
    tile.model.write(data);
    while (tile.model.hasResponse()) tile.model.readResponse();
    if (seq !== undefined) tile.lastSeq = seq;
  }

  // Drain the seed buffer through applyBytes (which dedups against lastSeq), then
  // clear it. Order is preserved.
  private flushPending(tile: Tile): void {
    const pending = tile.pending;
    tile.pending = [];
    for (const chunk of pending) this.applyBytes(tile, chunk.data, chunk.seq);
  }

  private modelText(model: GhosttyTerminal): string {
    model.update();
    const { cols, rows } = model;
    const cells = model.getViewport(); // reused pool — consume before any other getViewport()
    const lines: string[] = [];
    for (let row = 0; row < rows; row += 1) {
      let line = '';
      for (let col = 0; col < cols; col += 1) {
        const cell = cells[row * cols + col];
        if (!cell || cell.width === 0) continue;
        line += cell.codepoint && cell.codepoint !== 0 ? String.fromCodePoint(cell.codepoint) : ' ';
      }
      lines.push(line.replace(/\s+$/, ''));
    }
    return lines.join('\n').replace(/\n+$/, '');
  }

  private modelNonEmpty(model: GhosttyTerminal): boolean {
    model.update();
    const { cols, rows } = model;
    const cells = model.getViewport();
    for (let i = 0; i < rows * cols; i += 1) {
      const cell = cells[i];
      if (cell && cell.width > 0 && cell.codepoint && cell.codepoint !== 32) return true;
    }
    return false;
  }

  private tick(): void {
    const now = performance.now();
    const dt = Math.min(0.1, (now - this.lastNow) / 1000);
    this.lastNow = now;

    // Advance attention smoothing every frame (cheap, keeps pulses alive).
    let attentionAnimating = false;
    for (const tile of this.tiles) {
      const next = tile.attention + (tile.attentionTarget - tile.attention) * Math.min(1, dt * ATTENTION_RATE);
      if (Math.abs(next - tile.attention) > 0.001) attentionAnimating = true;
      tile.attention = next;
      if (tile.attention > 0.01) attentionAnimating = true; // keep pulsing
      if (!tile.hidden && tile.state === 'waiting_input') attentionAnimating = true;
    }

    // Advance zoom clock.
    if (this.zoomStart >= 0) {
      const t = clamp01((now - this.zoomStart) / ZOOM_MS);
      this.zoomT = lerp(this.zoomFrom, this.zoomTarget, easeOutCubic(t));
      if (t >= 1) {
        this.zoomStart = -1;
        if (this.zoomTarget === 0) this.zoomId = null;
      }
    }
    const reflowActive = this.reflowStart >= 0 && now - this.reflowStart < REFLOW_MS;
    if (this.reflowStart >= 0 && !reflowActive) this.reflowStart = -1;
    const zoomActive = this.zoomStart >= 0 || (this.zoomId !== null && this.zoomT > 0);

    let anyDirty = false;
    for (const tile of this.tiles) {
      if (!tile.hidden && tile.model.update() !== 0) anyDirty = true;
    }

    const shouldRender = anyDirty || reflowActive || zoomActive || attentionAnimating || this.presentationDirty;
    let rendered = false;
    if (shouldRender) {
      const frames = this.computeFrames(now, reflowActive);
      this.lastFrames = frames;
      this.lastStats = this.renderer.frame(frames, now);
      this.presentationDirty = false;
      rendered = true;
    }

    // Frame-time ring buffer for FPS / drops.
    this.frameMs.push(dt * 1000);
    if (this.frameMs.length > 120) this.frameMs.shift();

    if (now - this.statsEmitAt > 200) {
      this.statsEmitAt = now;
      this.lastCompositorStats = this.summarizeStats(rendered);
      this.onStats?.(this.lastCompositorStats);
    }
  }

  private computeFrames(now: number, reflowActive: boolean): TileFrame[] {
    const reflowT = reflowActive ? easeOutCubic(clamp01((now - this.reflowStart) / REFLOW_MS)) : 1;
    const zoomT = easeOutCubic(clamp01(this.zoomT));

    return this.tiles.map((tile, i): TileFrame => {
      const tileCols = tile.model.cols;
      const tileRows = tile.model.rows;
      const base = this.baseRect(i, this.layout, tileCols, tileRows);
      let rect = base.rect;
      let scale = base.scale;

      if (reflowActive) {
        const from = this.reflowFrom.get(tile.id);
        if (from) {
          rect = lerpRect(from, base.rect, reflowT);
          // approximate scale from width ratio so glyphs track the box
          const native = tileCols * this.metrics.cellWidth;
          scale = rect.w / native;
        }
      }

      let alpha = 1;
      let focused = false;
      if (this.zoomId) {
        if (tile.id === this.zoomId) {
          const full = this.fullRect(tileCols, tileRows);
          rect = lerpRect(base.rect, full.rect, zoomT);
          scale = lerp(base.scale, full.scale, zoomT);
          focused = zoomT > 0.5;
        } else {
          alpha = lerp(1, 0.16, zoomT);
        }
      }

      return {
        id: tile.id,
        rect,
        scale,
        alpha,
        attention: tile.id === this.zoomId ? 0 : tile.attention,
        state: tile.state,
        statePresentation: this.statePresentation,
        hidden: tile.hidden,
        focused,
      };
    });
  }

  // `nativeCols`/`nativeRows` are the tile model's geometry (which may differ
  // per tile once seeded to the session's real screen size), so each tile scales
  // to fit its slot without distortion.
  private baseRect(index: number, layout: Layout, nativeCols: number, nativeRows: number): { rect: Rect; scale: number } {
    const { rows, cols } = layout;
    const W = this.container.clientWidth;
    const H = this.container.clientHeight;
    const slotW = (W - (cols - 1) * GAP) / cols;
    const slotH = (H - (rows - 1) * GAP) / rows;
    const r = Math.floor(index / cols);
    const c = index % cols;
    const slotLeft = c * (slotW + GAP);
    const slotTop = r * (slotH + GAP);
    return this.fitInto(slotLeft, slotTop, slotW, slotH, nativeCols, nativeRows);
  }

  private fullRect(nativeCols: number, nativeRows: number): { rect: Rect; scale: number } {
    return this.fitInto(0, 0, this.container.clientWidth, this.container.clientHeight, nativeCols, nativeRows);
  }

  private fitInto(
    left: number,
    top: number,
    slotW: number,
    slotH: number,
    nativeCols: number,
    nativeRows: number,
  ): { rect: Rect; scale: number } {
    const nativeW = Math.max(1, nativeCols) * this.metrics.cellWidth;
    const nativeH = Math.max(1, nativeRows) * this.metrics.cellHeight;
    const scale = Math.max(0.01, Math.min(slotW / nativeW, slotH / nativeH));
    const w = nativeW * scale;
    const h = nativeH * scale;
    return {
      rect: { x: left + (slotW - w) / 2, y: top + (slotH - h) / 2, w, h },
      scale,
    };
  }

  private summarizeStats(rendered: boolean): CompositorStats {
    const sorted = [...this.frameMs].sort((a, b) => a - b);
    const n = sorted.length;
    const pct = (p: number) => (n ? sorted[Math.min(n - 1, Math.floor(p * n))] : 0);
    const avg = n ? sorted.reduce((s, v) => s + v, 0) / n : 0;
    const stats = this.lastStats ?? {
      drawCalls: 0, quads: 0, atlasUploads: 0, atlasResets: 0, liveContexts: 0, cpuSubmitMs: 0,
    };
    return {
      ...stats,
      fps: avg > 0 ? Math.round(1000 / avg) : 0,
      frameMsP50: pct(0.5),
      frameMsP95: pct(0.95),
      frameMsMax: sorted[n - 1] ?? 0,
      dropped: this.frameMs.filter((v) => v > 32).length,
      tileCount: this.tiles.filter((t) => !t.hidden).length,
      rendered,
    };
  }
}
