// Continuous, prod-safe terminal diagnostics for hard-to-reproduce rendering
// issues around splitting / resizing / closing panes — most importantly the
// "agent pane goes blank when a split opens" bug, which only surfaces under
// live timing and cannot be reproduced in the harness.
//
// Design (so this can run in production indefinitely without harm):
//   - All events go into an in-memory ring buffer (O(1) push, no I/O). This is
//     the rich context that gets attached to any incident.
//   - Only LOW-FREQUENCY lifecycle events (mount/unmount, resize, reset, split,
//     focus, attach/desync) are appended to disk continuously. High-frequency
//     events (paint, write) live only in the ring buffer.
//   - A self-detecting watchdog inspects each agent pane shortly after a resize:
//     if the model holds content but the surface drew ~nothing, it writes an
//     INCIDENT record (with ring context) to a separate file. That captures the
//     blank automatically, so the user only has to keep using the app.
//   - A second sweep periodically checks each active pane for a grid that is
//     larger than its container. It records a `bottom_clip` incident (the
//     historical event name) when stale PTY geometry appears and a
//     `bottom_clip_resolved` marker when it clears.
//   - Disk writes are async/non-blocking and size-capped, so they never perturb
//     the render timing we are trying to diagnose.
//
// Read the results from:
//   $APPLOCALDATA/debug/terminal-diagnostics.jsonl   (lifecycle stream)
//   $APPLOCALDATA/debug/terminal-incidents.jsonl      (auto-captured blanks + grid overflow)
//
// Dump every mounted pane's live geometry on demand (DevTools console):
//   window.__ATTN_TERMINAL_GEOMETRY()
//
// Disable at runtime with localStorage['attn:terminal-diagnostics']='0'.
import { isTauri } from '@tauri-apps/api/core';

const DEBUG_DIR = 'debug';
const LIFECYCLE_FILE = `${DEBUG_DIR}/terminal-diagnostics.jsonl`;
const INCIDENT_FILE = `${DEBUG_DIR}/terminal-incidents.jsonl`;
const STORAGE_KEY = 'attn:terminal-diagnostics';
const RING_LIMIT = 3000;
const INCIDENT_CONTEXT_EVENTS = 400;
const FILE_SIZE_CAP_BYTES = 8 * 1024 * 1024;
// A pane is only considered "should be showing something" once its model holds
// at least this many printable cells; below it, an empty surface is expected.
const MIN_CONTENT_CELLS = 40;
// The surface is considered under-drawn if it rendered fewer quads than this
// fraction of the model's printable-cell count.
const UNDERDRAW_RATIO = 0.25;
// Watchdog sampling after a resize; spans the window where a post-resize redraw
// would normally land.
const WATCHDOG_DELAYS_MS = [1200, 3500];
// Do not emit more than one incident per pane within this window (avoids spam
// while a pane stays blank across several repaint attempts).
const INCIDENT_COOLDOWN_MS = 8000;
// Geometry watchdog: how often to sweep active panes for a model grid larger
// than its container. Low frequency because stale geometry persists until a
// refit; the probe uses client dimensions and does not force layout.
const BOTTOM_CLIP_SWEEP_MS = 1500;
// Pixel slack mirroring geometryOverflowsContainer: ignore sub-pixel container
// heights so we only act on a genuine extra row.
const BOTTOM_CLIP_SLACK_PX = 1;
// Wait this many consecutive clipping sweeps before the first repair
// (3 sweeps ≈ 4.5s at the sweep cadence) — beyond the longest observed
// legitimate attach-replay transient, so the watchdog never fights a
// clip that heals on its own.
const CLIP_REPAIR_AFTER_SWEEPS = 3;
// Wall-clock delay before the 2nd and 3rd repair attempts.
const CLIP_REPAIR_BACKOFF_MS = [3000, 10000];
// After this many failed repairs, stop and record a give-up incident;
// the pane is beyond a refit's reach (e.g. dead renderer).
const CLIP_REPAIR_MAX_ATTEMPTS = 3;

export type DiagKind =
  | 'pane_mount'
  | 'pane_unmount'
  | 'paint'
  | 'write'
  | 'resize'
  | 'reset'
  | 'layout'
  | 'focus'
  | 'attach'
  | 'desync'
  | 'watchdog'
  | 'incident'
  | 'recovery';

export interface DiagEvent {
  at: number;
  kind: DiagKind;
  pane?: string;
  session?: string;
  [key: string]: unknown;
}

// Events cheap enough to stream to disk continuously. Paint/write are excluded
// (ring-buffer only) because an active agent paints many times per second.
const LIFECYCLE_KINDS = new Set<DiagKind>([
  'pane_mount',
  'pane_unmount',
  'resize',
  'reset',
  'layout',
  'focus',
  'attach',
  'desync',
  'watchdog',
  'incident',
  'recovery',
]);

export interface RenderProbe {
  cols: number;
  rows: number;
  modelPrintable: number;
  lastPaintAt: number;
  lastPaintQuads: number;
  // Whether this pane is currently allowed to paint at all. renderSurface
  // refuses to draw panes of an inactive session, so judging such a pane
  // "blank" is meaningless — it will paint on activation via the model's
  // accumulated dirty flag.
  active: boolean;
  // Geometry for the overflow detector / on-demand dump. Optional so other
  // probe producers and tests keep compiling; `null` means "not measured yet".
  session?: string;
  isActivePane?: boolean | null;
  hasMeasuredSize?: boolean;
  cellWidth?: number | null;
  cellHeight?: number | null;
  clientWidth?: number | null;
  clientHeight?: number | null;
}

export interface TerminalGeometrySnapshot {
  pane: string;
  session?: string;
  active: boolean;
  isActivePane: boolean | null;
  hasMeasuredSize: boolean | null;
  cols: number;
  rows: number;
  cellWidth: number | null;
  cellHeight: number | null;
  clientWidth: number | null;
  clientHeight: number | null;
  flooredCols: number | null;
  flooredRows: number | null;
  overflowPx: number | null;
  rightOverflowPx: number | null;
  clipping: boolean;
}

interface PaneHealth {
  pane: string;
  session?: string;
  cols: number;
  rows: number;
  lastResizeAt: number;
  lastPaintAt: number;
  lastPaintQuads: number;
  lastModelPrintable: number;
  lastIncidentAt: number;
}

declare global {
  interface Window {
    __ATTN_TERMINAL_DIAG?: DiagEvent[];
    __ATTN_TERMINAL_DIAG_DUMP?: () => DiagEvent[];
    __ATTN_TERMINAL_DIAG_FILES?: { lifecycle: string; incidents: string };
    __ATTN_TERMINAL_DIAG_ENABLE?: (enabled: boolean) => void;
    // On-demand: dump every mounted pane's live geometry (and persist a snapshot).
    __ATTN_TERMINAL_GEOMETRY?: () => TerminalGeometrySnapshot[];
    // Back-compat alias used by the split-blank e2e repro spec.
    __ATTN_RENDER_TRACE?: unknown[];
    __ATTN_RENDER_TRACE_ON?: boolean;
  }
}

const ring: DiagEvent[] = [];
let ringNextIndex = 0;
let ringWrapped = false;
const paneHealth = new Map<string, PaneHealth>();
const renderProbes = new Map<string, () => RenderProbe | null>();
// Optional per-pane repair callback (a refit) for the grid-overflow watchdog.
const repairHandlers = new Map<string, () => void>();
const watchdogTimers = new Map<string, ReturnType<typeof setTimeout>[]>();
// Per-pane overflow flag so the detector reports onset and resolution once,
// not on every sweep tick.
const clipState = new Map<string, boolean>();
// Per-pane grid-overflow repair progress: consecutive clipping sweeps, repair
// attempts made, the earliest wall-clock time the next attempt may fire, and
// whether the watchdog has given up on this continuous clip.
const clipRepairState = new Map<
  string,
  { clippingSweeps: number; attempts: number; nextAttemptAtMs: number; gaveUp: boolean }
>();
let clipSweepTimer: ReturnType<typeof setInterval> | null = null;

let lifecycleBytes = 0;
let incidentBytes = 0;
// The byte counters must reflect the real on-disk file size so the cap holds
// across app restarts. They reset to 0 each launch, so seed them from the
// existing file the first time we touch it — otherwise a file already near the
// cap would accept another full session's worth of writes before rotating, and
// across many short sessions could grow far past the promised bound.
let lifecycleSizeSeeded = false;
let incidentSizeSeeded = false;
let fileWriteChain: Promise<void> = Promise.resolve();

const diagTextEncoder = typeof TextEncoder !== 'undefined' ? new TextEncoder() : null;
function byteLength(value: string): number {
  return diagTextEncoder ? diagTextEncoder.encode(value).length : value.length;
}

function isEnabled(): boolean {
  if (typeof window === 'undefined') {
    return false;
  }
  try {
    return window.localStorage.getItem(STORAGE_KEY) !== '0';
  } catch {
    return true;
  }
}

function ensureGlobals() {
  if (typeof window === 'undefined') {
    return;
  }
  window.__ATTN_TERMINAL_DIAG = ring;
  window.__ATTN_RENDER_TRACE = ring;
  window.__ATTN_RENDER_TRACE_ON = true;
  window.__ATTN_TERMINAL_DIAG_FILES = {
    lifecycle: `$APPLOCALDATA/${LIFECYCLE_FILE}`,
    incidents: `$APPLOCALDATA/${INCIDENT_FILE}`,
  };
  if (!window.__ATTN_TERMINAL_DIAG_DUMP) {
    window.__ATTN_TERMINAL_DIAG_DUMP = ringSnapshot;
  }
  if (!window.__ATTN_TERMINAL_DIAG_ENABLE) {
    window.__ATTN_TERMINAL_DIAG_ENABLE = (enabled: boolean) => {
      try {
        window.localStorage.setItem(STORAGE_KEY, enabled ? '1' : '0');
      } catch {
        // ignore
      }
    };
  }
  if (!window.__ATTN_TERMINAL_GEOMETRY) {
    window.__ATTN_TERMINAL_GEOMETRY = dumpTerminalGeometry;
  }
}

async function appendToFile(file: 'lifecycle' | 'incident', line: string) {
  if (!isTauri()) {
    return;
  }
  try {
    const { mkdir, stat, writeTextFile, BaseDirectory } = await import('@tauri-apps/plugin-fs');
    await mkdir(DEBUG_DIR, { baseDir: BaseDirectory.AppLocalData, recursive: true });
    const path = file === 'lifecycle' ? LIFECYCLE_FILE : INCIDENT_FILE;
    // First write this session: adopt the file's current on-disk size so the cap
    // is measured against what is already there, not from zero.
    const seeded = file === 'lifecycle' ? lifecycleSizeSeeded : incidentSizeSeeded;
    if (!seeded) {
      try {
        const info = await stat(path, { baseDir: BaseDirectory.AppLocalData });
        const existing = typeof info?.size === 'number' ? info.size : 0;
        if (file === 'lifecycle') lifecycleBytes = existing; else incidentBytes = existing;
      } catch {
        // No file yet (or stat unavailable) — leave the counter at 0.
      }
      if (file === 'lifecycle') lifecycleSizeSeeded = true; else incidentSizeSeeded = true;
    }
    // Truncate-and-restart when a file grows past the cap so prod usage over
    // days stays bounded. A rotate marker keeps the stream self-describing.
    const bytes = file === 'lifecycle' ? lifecycleBytes : incidentBytes;
    const willReset = bytes > FILE_SIZE_CAP_BYTES;
    const payload = willReset ? `${JSON.stringify({ at: Date.now(), kind: 'rotate' })}\n${line}` : line;
    await writeTextFile(path, payload, {
      baseDir: BaseDirectory.AppLocalData,
      append: !willReset,
      create: true,
    });
    const written = byteLength(payload);
    if (file === 'lifecycle') {
      lifecycleBytes = willReset ? written : lifecycleBytes + written;
    } else {
      incidentBytes = willReset ? written : incidentBytes + written;
    }
  } catch (error) {
    console.warn('[TerminalDiag] write failed:', error);
  }
}

function enqueueWrite(file: 'lifecycle' | 'incident', line: string) {
  fileWriteChain = fileWriteChain.catch(() => {}).then(() => appendToFile(file, line));
}

function pushRing(event: DiagEvent) {
  if (ring.length < RING_LIMIT) {
    ring.push(event);
    return;
  }
  ring[ringNextIndex] = event;
  ringNextIndex = (ringNextIndex + 1) % RING_LIMIT;
  ringWrapped = true;
}

function ringSnapshot(): DiagEvent[] {
  if (!ringWrapped) {
    return [...ring];
  }
  return [...ring.slice(ringNextIndex), ...ring.slice(0, ringNextIndex)];
}

export function recordDiag(event: Omit<DiagEvent, 'at'>): void {
  if (typeof window === 'undefined') {
    return;
  }
  ensureGlobals();
  if (!isEnabled()) {
    return;
  }
  const entry = { ...event, at: Date.now() } as DiagEvent;
  pushRing(entry);
  if (LIFECYCLE_KINDS.has(entry.kind)) {
    enqueueWrite('lifecycle', `${JSON.stringify(entry)}\n`);
  }
}

// Focus is the most useful "where did keyboard input go" signal for the blank /
// wrong-PTY bugs, but focusPane's retry loop calls it ~10x in a burst for the
// same pane. Collapse consecutive same-pane focus events within a short window
// so the stream shows focus *transitions*, not retry spam.
const FOCUS_DEDUP_MS = 400;
let lastFocus: { pane?: string; at: number } = { at: 0 };

export function recordFocus(pane: string, retries: number): void {
  const now = Date.now();
  if (lastFocus.pane === pane && now - lastFocus.at < FOCUS_DEDUP_MS) {
    lastFocus.at = now;
    return;
  }
  lastFocus = { pane, at: now };
  recordDiag({ kind: 'focus', pane, retries });
}

// Command-block geometry is no longer streamed to disk. It is inspected live
// and on demand through the get_pane_block_state bridge action (which returns
// both the raw stored rows and the live re-anchor delta / drawable span), so a
// stale disk stream cannot mislead a diagnosis.

const lastLayoutSig = new Map<string, string>();

// Records a workspace layout snapshot, deduped on its pane set + split count so
// continuous split-ratio drags (which fire many layout updates with the same
// panes) do not flood the log, while real splits/closes are always recorded.
export function recordLayout(workspace: string, paneIds: string[], splitCount: number): void {
  const sig = `${[...paneIds].sort().join(',')}|${splitCount}`;
  if (lastLayoutSig.get(workspace) === sig) {
    return;
  }
  lastLayoutSig.set(workspace, sig);
  recordDiag({ kind: 'layout', workspace, paneCount: paneIds.length, splitCount, paneIds });
}

export interface PaintSample {
  pane: string;
  session?: string;
  cols: number;
  rows: number;
  force: boolean;
  offset: number;
  modelPrintable: number;
  quads: number | null;
  cellsArrayLen: number | null;
  skipNull: number | null;
  skipZeroWidth: number | null;
}

export function recordPaint(sample: PaintSample): void {
  if (typeof window === 'undefined' || !isEnabled()) {
    ensureGlobals();
    return;
  }
  ensureGlobals();
  const now = Date.now();
  pushRing({ at: now, kind: 'paint', ...sample });

  // quads === null means the renderer SKIPPED this frame (nothing dirty, not
  // forced) and left the canvas exactly as the previous draw painted it. That
  // is not a draw: folding it into pane health as "0 quads" poisons the
  // watchdog, and running the anomaly check on it flags a perfectly painted
  // idle pane as under-drawn (the source of every paint_underdraw false
  // positive captured in prod).
  if (sample.quads === null) {
    return;
  }

  const health = paneHealth.get(sample.pane) ?? {
    pane: sample.pane,
    session: sample.session,
    cols: sample.cols,
    rows: sample.rows,
    lastResizeAt: 0,
    lastPaintAt: 0,
    lastPaintQuads: 0,
    lastModelPrintable: 0,
    lastIncidentAt: 0,
  };
  health.cols = sample.cols;
  health.rows = sample.rows;
  health.lastPaintAt = now;
  health.lastPaintQuads = sample.quads;
  health.lastModelPrintable = sample.modelPrintable;
  health.session = sample.session ?? health.session;
  paneHealth.set(sample.pane, health);

  // Immediate anomaly: a real draw that painted far less than the model holds,
  // or dropped many printable cells at the renderer's cell skip.
  const quads = sample.quads;
  const underdrawn = sample.modelPrintable >= MIN_CONTENT_CELLS
    && quads < sample.modelPrintable * UNDERDRAW_RATIO;
  const droppedCells = (sample.skipZeroWidth ?? 0) + (sample.skipNull ?? 0)
    >= sample.modelPrintable * UNDERDRAW_RATIO && sample.modelPrintable >= MIN_CONTENT_CELLS;
  if (underdrawn || droppedCells) {
    maybeFlushIncident(sample.pane, underdrawn ? 'paint_underdraw' : 'paint_dropped_cells', {
      modelPrintable: sample.modelPrintable,
      quads,
      skipNull: sample.skipNull,
      skipZeroWidth: sample.skipZeroWidth,
      cellsArrayLen: sample.cellsArrayLen,
      cols: sample.cols,
      rows: sample.rows,
      force: sample.force,
    });
  }
}

export function registerRenderProbe(
  pane: string,
  probe: () => RenderProbe | null,
  repair?: () => void,
): () => void {
  renderProbes.set(pane, probe);
  if (repair) {
    repairHandlers.set(pane, repair);
  }
  ensureClipSweep();
  return () => {
    renderProbes.delete(pane);
    repairHandlers.delete(pane);
    clipState.delete(pane);
    clipRepairState.delete(pane);
    stopClipSweepIfIdle();
  };
}

export function noteResize(
  pane: string,
  info: { session?: string; source: string; fromCols?: number; fromRows?: number; toCols?: number; toRows?: number; bail?: string; noop?: boolean; paneKind?: string; historicalReplay?: boolean; cw?: number; ch?: number },
): void {
  recordDiag({ kind: 'resize', pane, ...info });
  // A WebGL canvas only clears its drawing buffer when its pixel dimensions
  // change, i.e. on a real grid-geometry change. A no-op/same-size resize leaves
  // the surface intact, so "no repaint afterwards" is benign — arming the
  // watchdog there produces false blanks on an idle pane that already painted
  // correctly. Only a genuine geometry change invalidates the surface and can
  // therefore leave it blank if no repaint follows.
  const geometryChanged =
    info.bail === undefined &&
    !info.noop &&
    !info.historicalReplay &&
    info.toCols != null &&
    info.toRows != null &&
    (info.fromCols !== info.toCols || info.fromRows !== info.toRows);
  if (!geometryChanged) {
    return;
  }
  const health = paneHealth.get(pane);
  if (health) {
    health.lastResizeAt = Date.now();
  }
  // Only agent panes exhibit the blank-on-resize bug; shells redraw trivially.
  if (info.paneKind === 'agent') {
    armWatchdog(pane, info.session);
  }
}

// WebGL context-loss recovery lifecycle: a lost context or a failed renderer
// construction schedules an epoch-rebuild retry with backoff (see
// GhosttyTerminal's rendererEpoch effect); this makes that retry sequence
// traceable after the fact instead of only visible as a silently-fixed pane.
export function noteRecovery(
  pane: string,
  info: {
    session?: string;
    paneKind?: string;
    attempt: number;
    outcome: 'contextLost' | 'constructFailed' | 'scheduled' | 'recovered' | 'giveUp';
    delayMs?: number;
    error?: string;
  },
): void {
  recordDiag({ kind: 'recovery', pane, ...info });
}

function armWatchdog(pane: string, session?: string) {
  clearWatchdog(pane);
  const timers = WATCHDOG_DELAYS_MS.map((delay) => setTimeout(() => runWatchdog(pane, session, delay), delay));
  watchdogTimers.set(pane, timers);
}

function clearWatchdog(pane: string) {
  const timers = watchdogTimers.get(pane);
  if (timers) {
    timers.forEach((t) => clearTimeout(t));
    watchdogTimers.delete(pane);
  }
}

function runWatchdog(pane: string, session: string | undefined, delay: number) {
  const probe = renderProbes.get(pane)?.();
  if (!probe) {
    return;
  }
  if (!probe.active) {
    // Hidden panes cannot paint by design (renderSurface bails for inactive
    // sessions); they repaint on activation from the model's dirty flag. Note
    // the skip so the lifecycle stream stays self-describing, but do not judge.
    recordDiag({ kind: 'watchdog', pane, session, delay, skipped: 'inactive' });
    return;
  }
  const health = paneHealth.get(pane);
  const resizeAt = health?.lastResizeAt ?? 0;
  const paintedSinceResize = probe.lastPaintAt >= resizeAt;
  const blank = probe.modelPrintable >= MIN_CONTENT_CELLS
    && (!paintedSinceResize || probe.lastPaintQuads < probe.modelPrintable * UNDERDRAW_RATIO);
  recordDiag({
    kind: 'watchdog',
    pane,
    session,
    delay,
    blank,
    modelPrintable: probe.modelPrintable,
    lastPaintQuads: probe.lastPaintQuads,
    paintedSinceResize,
    cols: probe.cols,
    rows: probe.rows,
  });
  if (blank) {
    maybeFlushIncident(pane, 'blank_after_resize', {
      delay,
      modelPrintable: probe.modelPrintable,
      lastPaintQuads: probe.lastPaintQuads,
      paintedSinceResize,
      cols: probe.cols,
      rows: probe.rows,
    });
  }
}

function maybeFlushIncident(pane: string, reason: string, detail: Record<string, unknown>) {
  const health = paneHealth.get(pane);
  const now = Date.now();
  if (health && now - health.lastIncidentAt < INCIDENT_COOLDOWN_MS) {
    return;
  }
  if (health) {
    health.lastIncidentAt = now;
  }
  const marker: DiagEvent = { at: now, kind: 'incident', pane, session: health?.session, reason, ...detail };
  pushRing(marker);
  enqueueWrite('lifecycle', `${JSON.stringify(marker)}\n`);
  // Full incident record carries the surrounding ring context for diagnosis.
  const record = {
    at: now,
    kind: 'incident',
    pane,
    session: health?.session,
    reason,
    detail,
    context: ringSnapshot().slice(-INCIDENT_CONTEXT_EVENTS),
  };
  enqueueWrite('incident', `${JSON.stringify(record)}\n`);
}

// --- Grid-overflow detector ------------------------------------------------
// The daemon's authoritative geometry can temporarily outlive the client
// viewport that produced it, especially while panes are inactive. This sweep
// notices persistent extra rows or columns after activation, captures the
// resize trail, and asks `fit()` to reassert container-owned geometry. Event
// names retain `bottom_clip` for compatibility with existing incident logs.

function bottomClipOverflowPx(probe: RenderProbe): number | null {
  const cellHeight = probe.cellHeight ?? 0;
  const clientHeight = probe.clientHeight ?? 0;
  if (cellHeight <= 0 || clientHeight <= 0 || probe.rows <= 0) {
    return null;
  }
  return probe.rows * cellHeight - clientHeight;
}

function rightClipOverflowPx(probe: RenderProbe): number | null {
  const cellWidth = probe.cellWidth ?? 0;
  const clientWidth = probe.clientWidth ?? 0;
  if (cellWidth <= 0 || clientWidth <= 0 || probe.cols <= 0) {
    return null;
  }
  return probe.cols * cellWidth - clientWidth;
}

function recordBottomClipIncident(pane: string, detail: Record<string, unknown>): void {
  const now = Date.now();
  const session = typeof detail.session === 'string' ? detail.session : undefined;
  const marker: DiagEvent = { at: now, kind: 'incident', pane, session, reason: 'bottom_clip', ...detail };
  pushRing(marker);
  enqueueWrite('lifecycle', `${JSON.stringify(marker)}\n`);
  // Full record carries the surrounding ring context (resizes, paints, layout)
  // that produced the clip — the whole point of capturing it in the wild.
  const record = {
    at: now,
    kind: 'incident',
    pane,
    session,
    reason: 'bottom_clip',
    detail,
    context: ringSnapshot().slice(-INCIDENT_CONTEXT_EVENTS),
  };
  enqueueWrite('incident', `${JSON.stringify(record)}\n`);
}

function recordBottomClipRepairIncident(
  pane: string,
  reason: 'bottom_clip_repair' | 'bottom_clip_repair_gave_up',
  detail: Record<string, unknown>,
  session?: string,
): void {
  const now = Date.now();
  const marker: DiagEvent = { at: now, kind: 'incident', pane, session, reason, ...detail };
  pushRing(marker);
  enqueueWrite('lifecycle', `${JSON.stringify(marker)}\n`);
  const record = {
    at: now,
    kind: 'incident',
    pane,
    session,
    reason,
    detail,
    context: ringSnapshot().slice(-INCIDENT_CONTEXT_EVENTS),
  };
  enqueueWrite('incident', `${JSON.stringify(record)}\n`);
}

function sweepBottomClip(): void {
  if (typeof window === 'undefined' || !isEnabled()) {
    return;
  }
  for (const [pane, probeFn] of renderProbes) {
    let probe: RenderProbe | null = null;
    try {
      probe = probeFn();
    } catch {
      probe = null;
    }
    const wasClipping = clipState.get(pane) ?? false;
    // Only judge active (visible) panes. An inactive pane legitimately holds the
    // daemon's geometry until it re-fits on activation, and its container is
    // display:none (height 0). Clear the flag so a later activation that still
    // clips re-reports the onset.
    if (!probe || !probe.active) {
      if (wasClipping) clipState.set(pane, false);
      clipRepairState.delete(pane);
      continue;
    }
    const overflowPx = bottomClipOverflowPx(probe);
    const rightOverflowPx = rightClipOverflowPx(probe);
    if (overflowPx == null && rightOverflowPx == null) {
      clipRepairState.delete(pane);
      continue;
    }
    const trigger: string[] = [];
    if (overflowPx != null && overflowPx > BOTTOM_CLIP_SLACK_PX) trigger.push('model');
    if (rightOverflowPx != null && rightOverflowPx > BOTTOM_CLIP_SLACK_PX) trigger.push('model_right');
    const clipping = trigger.length > 0;
    const cellHeight = probe.cellHeight ?? 0;
    const cellWidth = probe.cellWidth ?? 0;
    const clientHeight = probe.clientHeight ?? 0;
    const clientWidth = probe.clientWidth ?? 0;
    const flooredRows = cellHeight > 0 ? Math.floor(clientHeight / cellHeight) : 0;
    const flooredCols = cellWidth > 0 ? Math.floor(clientWidth / cellWidth) : 0;

    if (!clipping) {
      // Full reset: the clip is gone (or never started this sweep). Keep the
      // existing edge-triggered incident/resolve logging as-is, but attach how
      // many repairs were attempted during the episode that just ended.
      const priorState = clipRepairState.get(pane);
      clipRepairState.delete(pane);
      if (clipping === wasClipping) {
        continue;
      }
      clipState.set(pane, clipping);
      recordDiag({
        kind: 'incident',
        pane,
        session: probe.session,
        reason: 'bottom_clip_resolved',
        rows: probe.rows,
        cols: probe.cols,
        flooredRows,
        flooredCols,
        overflowPx: overflowPx == null ? null : Math.round(overflowPx),
        rightOverflowPx: rightOverflowPx == null ? null : Math.round(rightOverflowPx),
        cellHeight,
        cellWidth,
        clientHeight,
        clientWidth,
        repairAttempts: priorState?.attempts ?? 0,
      });
      continue;
    }

    // Clipping is true on this sweep. Edge-triggered incident logging fires
    // only on the onset (matching the pre-watchdog behavior), but repair
    // evaluation runs on EVERY clipping sweep so backoff/attempts advance
    // while the clip persists.
    clipState.set(pane, clipping);
    if (clipping !== wasClipping) {
      recordBottomClipIncident(pane, {
        rows: probe.rows,
        cols: probe.cols,
        flooredRows,
        flooredCols,
        extraRows: probe.rows - flooredRows,
        extraCols: probe.cols - flooredCols,
        overflowPx: overflowPx == null ? null : Math.round(overflowPx),
        rightOverflowPx: rightOverflowPx == null ? null : Math.round(rightOverflowPx),
        cellHeight,
        cellWidth,
        clientHeight,
        clientWidth,
        hasMeasuredSize: probe.hasMeasuredSize ?? null,
        isActivePane: probe.isActivePane ?? null,
        session: probe.session,
        trigger,
        dpr: window.devicePixelRatio,
        winInnerWidth: window.innerWidth,
        winInnerHeight: window.innerHeight,
      });
    }

    const repair = repairHandlers.get(pane);
    const state = clipRepairState.get(pane) ?? {
      clippingSweeps: 0,
      attempts: 0,
      nextAttemptAtMs: 0,
      gaveUp: false,
    };
    state.clippingSweeps += 1;
    clipRepairState.set(pane, state);

    if (
      repair
      && document.visibilityState === 'visible'
      && state.clippingSweeps >= CLIP_REPAIR_AFTER_SWEEPS
      && !state.gaveUp
      && Date.now() >= state.nextAttemptAtMs
    ) {
      if (state.attempts >= CLIP_REPAIR_MAX_ATTEMPTS) {
        state.gaveUp = true;
        recordBottomClipRepairIncident(pane, 'bottom_clip_repair_gave_up', {
          rows: probe.rows,
          cols: probe.cols,
          attempts: state.attempts,
        }, probe.session);
      } else {
        state.attempts += 1;
        const backoff = CLIP_REPAIR_BACKOFF_MS[state.attempts - 1] ?? CLIP_REPAIR_BACKOFF_MS[CLIP_REPAIR_BACKOFF_MS.length - 1];
        state.nextAttemptAtMs = Date.now() + backoff;
        recordBottomClipRepairIncident(pane, 'bottom_clip_repair', {
          attempt: state.attempts,
          rows: probe.rows,
          cols: probe.cols,
          overflowPx: overflowPx == null ? null : Math.round(overflowPx),
          rightOverflowPx: rightOverflowPx == null ? null : Math.round(rightOverflowPx),
        }, probe.session);
        try {
          repair();
        } catch {
          // The next sweep re-evaluates; a repair handler throwing must not
          // break the sweep loop for other panes.
        }
      }
    }
  }
}

function ensureClipSweep(): void {
  if (typeof window === 'undefined' || clipSweepTimer != null) {
    return;
  }
  clipSweepTimer = setInterval(sweepBottomClip, BOTTOM_CLIP_SWEEP_MS);
}

function stopClipSweepIfIdle(): void {
  if (clipSweepTimer != null && renderProbes.size === 0) {
    clearInterval(clipSweepTimer);
    clipSweepTimer = null;
  }
}

// On-demand: snapshot every mounted pane's live geometry, persist it, and (in a
// console) table it. Useful to inspect the moment the clip is on screen.
export function dumpTerminalGeometry(): TerminalGeometrySnapshot[] {
  const snapshots: TerminalGeometrySnapshot[] = [];
  for (const [pane, probeFn] of renderProbes) {
    let probe: RenderProbe | null = null;
    try {
      probe = probeFn();
    } catch {
      probe = null;
    }
    if (!probe) {
      continue;
    }
    const cellHeight = probe.cellHeight ?? null;
    const cellWidth = probe.cellWidth ?? null;
    const clientHeight = probe.clientHeight ?? null;
    const clientWidth = probe.clientWidth ?? null;
    const overflowPx = cellHeight && clientHeight ? probe.rows * cellHeight - clientHeight : null;
    const rightOverflowPx = cellWidth && clientWidth ? probe.cols * cellWidth - clientWidth : null;
    snapshots.push({
      pane,
      session: probe.session,
      active: probe.active,
      isActivePane: probe.isActivePane ?? null,
      hasMeasuredSize: probe.hasMeasuredSize ?? null,
      cols: probe.cols,
      rows: probe.rows,
      cellWidth,
      cellHeight,
      clientWidth,
      clientHeight,
      flooredCols: cellWidth && clientWidth ? Math.floor(clientWidth / cellWidth) : null,
      flooredRows: cellHeight && clientHeight ? Math.floor(clientHeight / cellHeight) : null,
      overflowPx: overflowPx == null ? null : Math.round(overflowPx),
      rightOverflowPx: rightOverflowPx == null ? null : Math.round(rightOverflowPx),
      clipping: (overflowPx != null && overflowPx > BOTTOM_CLIP_SLACK_PX)
        || (rightOverflowPx != null && rightOverflowPx > BOTTOM_CLIP_SLACK_PX),
    });
  }
  enqueueWrite('incident', `${JSON.stringify({ at: Date.now(), kind: 'geometry_dump', snapshots })}\n`);
  if (typeof console !== 'undefined' && typeof console.table === 'function') {
    console.table(snapshots);
  }
  return snapshots;
}

export function disposePaneDiagnostics(pane: string): void {
  clearWatchdog(pane);
  paneHealth.delete(pane);
  renderProbes.delete(pane);
  repairHandlers.delete(pane);
  clipState.delete(pane);
  clipRepairState.delete(pane);
  stopClipSweepIfIdle();
}

ensureGlobals();
