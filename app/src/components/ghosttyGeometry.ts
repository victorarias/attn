// Pure geometry and resize-policy helpers for the Ghostty terminal pane.
//
// They live beside GhosttyTerminal rather than inside it: they are ordinary
// functions over numbers, they are unit-tested on their own, and a component
// file that also exports non-components makes every consumer of one of these
// re-import the whole terminal.

import { isSuspiciousTerminalSize } from '../utils/terminalDebug';
import type { TerminalDimensions } from '../utils/ghosttyResize';

export function isWorkspaceResizeDragActive(element: HTMLElement | null): boolean {
  if (document.documentElement.dataset.attnWorkspaceResizing === '1') {
    return true;
  }
  return Boolean(element?.closest('.session-terminal-panes[data-resizing-split-id]'));
}

export function fitRequiresTerminalResize(
  current: TerminalDimensions,
  next: TerminalDimensions,
): boolean {
  return current.cols !== next.cols || current.rows !== next.rows;
}

// True when a grid of `rows` rows of `cellHeight` px is taller than the
// container that displays it — i.e. the rendered canvas would spill below the
// visible viewport and the bottom row(s) would be clipped.
//
// `fit()` derives rows from `Math.floor(clientHeight / cellHeight)`, so it can
// never overflow. But the daemon's authoritative PTY geometry (delivered via
// `resizeLocal`, source `pty_resized`) is NOT bounded by this client's window:
// when another client — or a prior, taller layout — left the PTY one row taller
// than this window fits, the canvas ends up taller than the container and the
// last line is cut at the window edge. Detecting that lets the active client
// re-assert its own (floored) geometry. A 1px slack absorbs sub-pixel
// fractional container heights so we only act on a genuine extra row.
export function geometryOverflowsContainer(
  rows: number,
  cellHeight: number,
  clientHeight: number,
): boolean {
  if (rows <= 0 || cellHeight <= 0 || clientHeight <= 0) {
    return false;
  }
  return rows * cellHeight > clientHeight + 1;
}

// Whether fit() should SUPPRESS applying `dims` because they look like a
// transient/garbage measurement (taken before layout settles on relaunch,
// first-show, or a split topology change) rather than a real container.
//
// The guard only fires for agent panes whose floored size is "suspicious"
// (MIN_USABLE_TERMINAL_*). But a genuinely small container — a deep stacked
// split, a narrow side-by-side split, or a short window — yields a legitimately
// small grid. Refusing it would leave the model LARGER than the container, so the
// overflowing edge (the bottom row(s) for a short pane, or the right column(s)
// for a narrow one) renders past the overflow:hidden viewport edge and cannot be
// scrolled into view. So when the live model already overflows the measured
// container in either axis, the small fit is required (a correctly sized small
// terminal beats a clipped one) and we do NOT bail. A not-yet-laid-out container
// (clientWidth/clientHeight ~0) never registers as overflowing (see
// geometryOverflowsContainer), so the transient case still bails.
//
// One more exception: when the live model is ITSELF already suspicious (e.g.
// stuck at 15 cols after a relaunch race) and `dims` would only grow it —
// never shrink either axis — the bail is refused even though `dims` is still
// in the suspicious range. The guard exists to protect a HEALTHY grid from
// transient small measurements; a model that's already at/below the usable
// floor has nothing left to protect, and refusing a non-shrinking fit would
// strand the pane smaller than its container forever. A shrink (including a
// not-yet-laid-out container, whose ~0 dims are smaller than any live model)
// still bails.
export function fitShouldBailAsSuspicious(
  paneKind: string | undefined,
  dims: TerminalDimensions,
  modelCols: number,
  modelRows: number,
  cellWidth: number,
  cellHeight: number,
  clientWidth: number,
  clientHeight: number,
): boolean {
  if (paneKind !== 'agent') {
    return false;
  }
  if (!isSuspiciousTerminalSize(dims.cols, dims.rows)) {
    return false;
  }
  // geometryOverflowsContainer is axis-agnostic (count * cellSize > client + 1);
  // apply it to height (rows) and width (cols) so a narrow split is covered too.
  const overflows = geometryOverflowsContainer(modelRows, cellHeight, clientHeight)
    || geometryOverflowsContainer(modelCols, cellWidth, clientWidth);
  if (overflows) {
    return false;
  }
  // The bail protects a HEALTHY grid from transient small measurements. A model
  // already at/below the usable floor has nothing left to protect — refusing a
  // fit that doesn't shrink it strands the pane smaller than its container
  // forever (shrinks INTO the suspicious range always apply via the overflow
  // path above, so the grow back out must apply too). A not-yet-laid-out
  // container still bails here: its dims are smaller than any live model,
  // which is a shrink.
  const shrinksModel = dims.cols < modelCols || dims.rows < modelRows;
  return !(isSuspiciousTerminalSize(modelCols, modelRows) && !shrinksModel);
}

// Geometry the queued (not yet applied) historical replay will end at.
// `resizes` counts replay resize operations still on the write chain.
export interface PendingReplayGeometry extends TerminalDimensions {
  resizes: number;
  lastQueuedAt: number;
}

// On a healthy write chain a queued replay resize applies within
// milliseconds; if it still hasn't applied after this long, the "replay will
// land there" promise is stale and must no longer suppress live geometry
// corrections.
export const REPLAY_GEOMETRY_STALE_MS = 3000;

// Replay segments march the model through historical geometries before
// landing on the live PTY size. A live fit or daemon resize echo arriving
// mid-replay would see a transient mismatch and cancel the queued history —
// but if it targets the geometry replay already ends at, it is not a
// conflict: skip it and let the replay land there. Only a genuinely
// different target cancels. If the queued replay has gone stale (its resize
// never applied — e.g. a generation-mismatch drop), the promise is broken
// and suppression must not hold forever.
export function liveResizeConflictsWithQueuedReplay(
  pendingReplay: PendingReplayGeometry | null,
  target: TerminalDimensions,
  nowMs: number,
): 'skip' | 'cancel' | 'none' | 'stale' {
  if (!pendingReplay || pendingReplay.resizes <= 0) {
    return 'none';
  }
  if (nowMs - pendingReplay.lastQueuedAt > REPLAY_GEOMETRY_STALE_MS) {
    return 'stale';
  }
  return fitRequiresTerminalResize(pendingReplay, target) ? 'cancel' : 'skip';
}

// Backoff schedule for WebGL renderer-recovery retries (context loss or a
// failed construction): `attempt` is 1-indexed (the first retry is attempt 1).
// Returns the delay before that attempt, or null once attempts are exhausted
// and the pane should fall back to the "reopen the pane" error state — PR B
// already removed most of the context-pool pressure that caused losses, so a
// handful of short, escalating retries covers the rare remaining cases
// without spinning forever on a genuinely dead GPU/context.
export function recoveryDelayMs(attempt: number): number | null {
  const schedule = [250, 1500, 5000];
  return schedule[attempt - 1] ?? null;
}
