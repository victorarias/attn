import type { WorkspaceLayout as DaemonWorkspaceSnapshot, PaneElement } from './generated';

export type TerminalSplitDirection = 'vertical' | 'horizontal';
export type TerminalNavigationDirection = 'left' | 'right' | 'up' | 'down';
export type TerminalDockEdge = 'left' | 'right' | 'top' | 'bottom';

// TileKind is the surface a docked tile renders. The layout layer persists it
// as an opaque token; individual kinds can still have daemon and native-host
// behavior. Kept open-ended for forward compatibility with newer tile hosts.
export type TileKind = 'markdown' | 'browser' | (string & {});

export interface TerminalPaneLeaf {
  type: 'pane';
  paneId: string;
}

// A docked tile is a first-class layout leaf alongside terminal panes: it
// takes real space and resizes through the same split machinery. Unlike the
// slide-in overlays (diff/review/git-status), it lives in the daemon-owned
// layout tree and persists across restart and clients.
export interface TileLeaf {
  type: 'tile';
  tileId: string;
  tileKind: TileKind;
  // Opaque per-tile data persisted with the layout by the daemon. For markdown
  // tiles this is the absolute path of the file the tile renders. Empty when
  // the daemon persisted no params.
  tileParams?: string;
  // Session the tile was opened from (markdown tiles bind to the session whose
  // terminal the file was cmd+clicked in). Absent when the tile has no binding.
  tileSessionId?: string;
}

export type TerminalLeaf = TerminalPaneLeaf | TileLeaf;

// Daemon-served rendered content for a docked tile (the markdown file's text).
// Delivered as the reply to workspace_tile_content_get and re-pushed on every
// on-disk change (live reload). Absent from the store until the first reply.
export interface TileContentState {
  path: string;
  content: string;
  error?: string;
}

// tileContentKey keys tile content by workspace + tile: tile ids are derived
// from content (markdown tiles hash the file path), so the same id can appear
// in more than one workspace.
export function tileContentKey(workspaceId: string, tileId: string): string {
  return `${workspaceId}::${tileId}`;
}

export interface TerminalSplitNode {
  type: 'split';
  splitId: string;
  direction: TerminalSplitDirection;
  ratio: number;
  children: [TerminalLayoutNode, TerminalLayoutNode];
}

export type TerminalLayoutNode = TerminalPaneLeaf | TileLeaf | TerminalSplitNode;

export function collectLayoutLeaves(node: TerminalLayoutNode | null): TerminalLeaf[] {
  if (!node) {
    return [];
  }
  if (node.type !== 'split') {
    return [node];
  }
  return [
    ...collectLayoutLeaves(node.children[0]),
    ...collectLayoutLeaves(node.children[1]),
  ];
}

// leafSlotId is the stable key a leaf occupies in bounds/path maps. Terminal
// panes key by paneId; tiles key by tileId. The two id spaces are disjoint by
// construction (distinct daemon prefixes).
export function leafSlotId(node: TerminalLeaf): string {
  return node.type === 'pane' ? node.paneId : node.tileId;
}

export interface AgentTerminal {
  id: string;
  runtimeId: string;
  sessionId: string;
  title: string;
  status?: 'spawning' | 'ready' | 'failed';
  error?: string;
}

export interface TerminalWorkspaceState {
  agents: AgentTerminal[];
  layoutTree: TerminalLayoutNode | null;
}

export interface TerminalWorkspaceSnapshot {
  workspace: TerminalWorkspaceState;
  daemonActivePaneId: string;
}

interface PaneBounds {
  left: number;
  top: number;
  right: number;
  bottom: number;
  centerX: number;
  centerY: number;
}

export interface NormalizedPaneBounds extends PaneBounds {
  width: number;
  height: number;
}

export function createDefaultWorkspaceState(): TerminalWorkspaceState {
  return {
    agents: [],
    layoutTree: null,
  };
}

// findTileByKind returns the first docked tile of the given kind, or null.
// Used to drive UI toggles ("is markdown docked in this workspace?").
export function findTileByKind(node: TerminalLayoutNode | null, kind: TileKind): TileLeaf | null {
  if (!node) {
    return null;
  }
  if (node.type === 'tile') {
    return node.tileKind === kind ? node : null;
  }
  if (node.type === 'split') {
    return findTileByKind(node.children[0], kind) || findTileByKind(node.children[1], kind);
  }
  return null;
}

export function hasPane(node: TerminalLayoutNode, paneId: string): boolean {
  if (node.type === 'pane') {
    return node.paneId === paneId;
  }
  if (node.type === 'tile') {
    return false;
  }
  return hasPane(node.children[0], paneId) || hasPane(node.children[1], paneId);
}

function paneBounds(
  left: number,
  top: number,
  right: number,
  bottom: number
): PaneBounds {
  return {
    left,
    top,
    right,
    bottom,
    centerX: (left + right) / 2,
    centerY: (top + bottom) / 2,
  };
}

function withDimensions(bounds: PaneBounds): NormalizedPaneBounds {
  return {
    ...bounds,
    width: bounds.right - bounds.left,
    height: bounds.bottom - bounds.top,
  };
}

type LeafKind = 'pane' | 'tile';

interface LeafBounds {
  bounds: PaneBounds;
  kind: LeafKind;
}

// collectLeafBounds records the normalized rect of every leaf (pane and tile),
// keyed by its slot id, descending through splits with their ratios. Tiles are
// recorded so the workspace can position them; the kind lets callers that only
// care about terminals (navigation) filter tiles out.
function collectLeafBounds(
  node: TerminalLayoutNode,
  bounds: PaneBounds,
  result: Map<string, LeafBounds>
): void {
  if (node.type === 'pane') {
    result.set(node.paneId, { bounds, kind: 'pane' });
    return;
  }
  if (node.type === 'tile') {
    result.set(node.tileId, { bounds, kind: 'tile' });
    return;
  }

  const ratio = node.ratio > 0 && node.ratio < 1 ? node.ratio : 0.5;
  if (node.direction === 'vertical') {
    const splitX = bounds.left + (bounds.right - bounds.left) * ratio;
    collectLeafBounds(node.children[0], paneBounds(bounds.left, bounds.top, splitX, bounds.bottom), result);
    collectLeafBounds(node.children[1], paneBounds(splitX, bounds.top, bounds.right, bounds.bottom), result);
    return;
  }

  const splitY = bounds.top + (bounds.bottom - bounds.top) * ratio;
  collectLeafBounds(node.children[0], paneBounds(bounds.left, bounds.top, bounds.right, splitY), result);
  collectLeafBounds(node.children[1], paneBounds(bounds.left, splitY, bounds.right, bounds.bottom), result);
}

// getNormalizedPaneBounds returns the normalized rect of every leaf slot
// (terminal panes and docked tiles), keyed by slot id.
export function getNormalizedPaneBounds(node: TerminalLayoutNode): Map<string, NormalizedPaneBounds> {
  const rects = new Map<string, LeafBounds>();
  collectLeafBounds(node, paneBounds(0, 0, 1, 1), rects);
  return new Map(
    Array.from(rects.entries()).map(([slotId, { bounds }]) => [slotId, withDimensions(bounds)]),
  );
}

export interface SplitDivider {
  splitId: string;
  direction: TerminalSplitDirection;
  ratio: number;
  // Normalized (0..1) bounds of the split's container, used to convert a
  // pointer position into a ratio while dragging.
  left: number;
  top: number;
  right: number;
  bottom: number;
}

// getSplitDividers returns one entry per split node, describing the draggable
// divider between its two children in normalized coordinates.
export function getSplitDividers(node: TerminalLayoutNode): SplitDivider[] {
  const dividers: SplitDivider[] = [];
  const walk = (current: TerminalLayoutNode, left: number, top: number, right: number, bottom: number): void => {
    if (current.type !== 'split') {
      return;
    }
    const ratio = current.ratio > 0 && current.ratio < 1 ? current.ratio : 0.5;
    dividers.push({ splitId: current.splitId, direction: current.direction, ratio, left, top, right, bottom });
    if (current.direction === 'vertical') {
      const splitX = left + (right - left) * ratio;
      walk(current.children[0], left, top, splitX, bottom);
      walk(current.children[1], splitX, top, right, bottom);
    } else {
      const splitY = top + (bottom - top) * ratio;
      walk(current.children[0], left, top, right, splitY);
      walk(current.children[1], left, splitY, right, bottom);
    }
  };
  walk(node, 0, 0, 1, 1);
  return dividers;
}

// applyRatioOverrides returns a copy of the tree with the given split ratios
// replaced. Used for live (optimistic) divider dragging before the daemon
// echoes the persisted ratio back.
export function applyRatioOverrides(node: TerminalLayoutNode, overrides: Map<string, number>): TerminalLayoutNode {
  if (overrides.size === 0 || node.type !== 'split') {
    return node;
  }
  const override = overrides.get(node.splitId);
  return {
    ...node,
    ratio: override != null ? override : node.ratio,
    children: [
      applyRatioOverrides(node.children[0], overrides),
      applyRatioOverrides(node.children[1], overrides),
    ],
  };
}

// collectSplitRatios maps each split id to its current ratio. Used to reconcile
// local drag overrides against the authoritative daemon layout.
export function collectSplitRatios(node: TerminalLayoutNode): Map<string, number> {
  const ratios = new Map<string, number>();
  const walk = (current: TerminalLayoutNode): void => {
    if (current.type !== 'split') {
      return;
    }
    ratios.set(current.splitId, current.ratio);
    walk(current.children[0]);
    walk(current.children[1]);
  };
  walk(node);
  return ratios;
}

function overlapSize(aStart: number, aEnd: number, bStart: number, bEnd: number): number {
  return Math.max(0, Math.min(aEnd, bEnd) - Math.max(aStart, bStart));
}

export function findPaneInDirection(
  node: TerminalLayoutNode,
  fromPaneId: string,
  direction: TerminalNavigationDirection
): string | null {
  const leaves = new Map<string, LeafBounds>();
  collectLeafBounds(node, paneBounds(0, 0, 1, 1), leaves);
  // Navigation only moves between terminal panes — docked tiles are not focus
  // targets.
  const rects = new Map<string, PaneBounds>();
  for (const [slotId, { bounds, kind }] of leaves) {
    if (kind === 'pane') {
      rects.set(slotId, bounds);
    }
  }
  const current = rects.get(fromPaneId);
  if (!current) {
    return null;
  }

  const candidates = Array.from(rects.entries())
    .filter(([paneId]) => paneId !== fromPaneId)
    .map(([paneId, candidate]) => {
      switch (direction) {
        case 'left': {
          const primary = current.left - candidate.right;
          if (primary < -1e-6) return null;
          const overlap = overlapSize(current.top, current.bottom, candidate.top, candidate.bottom);
          if (overlap <= 0) return null;
          return { paneId, primary, secondary: Math.abs(current.centerY - candidate.centerY) };
        }
        case 'right': {
          const primary = candidate.left - current.right;
          if (primary < -1e-6) return null;
          const overlap = overlapSize(current.top, current.bottom, candidate.top, candidate.bottom);
          if (overlap <= 0) return null;
          return { paneId, primary, secondary: Math.abs(current.centerY - candidate.centerY) };
        }
        case 'up': {
          const primary = current.top - candidate.bottom;
          if (primary < -1e-6) return null;
          const overlap = overlapSize(current.left, current.right, candidate.left, candidate.right);
          if (overlap <= 0) return null;
          return { paneId, primary, secondary: Math.abs(current.centerX - candidate.centerX) };
        }
        case 'down': {
          const primary = candidate.top - current.bottom;
          if (primary < -1e-6) return null;
          const overlap = overlapSize(current.left, current.right, candidate.left, candidate.right);
          if (overlap <= 0) return null;
          return { paneId, primary, secondary: Math.abs(current.centerX - candidate.centerX) };
        }
      }
    })
    .filter((value): value is { paneId: string; primary: number; secondary: number } => Boolean(value))
    .sort((a, b) => {
      if (a.primary !== b.primary) {
        return a.primary - b.primary;
      }
      if (a.secondary !== b.secondary) {
        return a.secondary - b.secondary;
      }
      return a.paneId.localeCompare(b.paneId);
    });

  return candidates[0]?.paneId ?? null;
}

function parseLayoutNode(raw: unknown): TerminalLayoutNode | null {
  if (!raw || typeof raw !== 'object') {
    return null;
  }
  const value = raw as Record<string, unknown>;
  if (value.type === 'pane' && typeof value.pane_id === 'string') {
    return {
      type: 'pane',
      paneId: value.pane_id,
    };
  }
  if (
    value.type === 'tile' &&
    typeof value.tile_id === 'string' &&
    value.tile_id.length > 0 &&
    typeof value.tile_kind === 'string' &&
    value.tile_kind.length > 0
  ) {
    return {
      type: 'tile',
      tileId: value.tile_id,
      tileKind: value.tile_kind,
      tileParams: typeof value.tile_params === 'string' ? value.tile_params : undefined,
      tileSessionId: typeof value.tile_session_id === 'string' && value.tile_session_id.length > 0
        ? value.tile_session_id
        : undefined,
    };
  }
  if (
    value.type === 'split' &&
    typeof value.split_id === 'string' &&
    (value.direction === 'vertical' || value.direction === 'horizontal') &&
    Array.isArray(value.children) &&
    value.children.length >= 2
  ) {
    const first = parseLayoutNode(value.children[0]);
    const second = parseLayoutNode(value.children[1]);
    if (!first || !second) {
      return null;
    }
    const ratio = typeof value.ratio === 'number' && value.ratio > 0 && value.ratio < 1 ? value.ratio : 0.5;
    return {
      type: 'split',
      splitId: value.split_id,
      direction: value.direction,
      ratio,
      children: [first, second],
    };
  }
  return null;
}

export function parseLayoutJSON(layoutJSON: string): TerminalLayoutNode | null {
  if (!layoutJSON.trim()) {
    return null;
  }
  try {
    const parsed = JSON.parse(layoutJSON);
    return parseLayoutNode(parsed);
  } catch {
    return null;
  }
}

export function tileIdsFromLayoutJSON(layoutJSON: string, kind?: TileKind): string[] {
  return collectLayoutLeaves(parseLayoutJSON(layoutJSON))
    .filter((leaf): leaf is TileLeaf => leaf.type === 'tile')
    .filter((tile) => !kind || tile.tileKind === kind)
    .map((tile) => tile.tileId);
}

// A notebook tile's parsed tileParams: `root` is set only for a tile pinned to
// an arbitrary filesystem root (editor-over-arbitrary-roots); its absence
// means the tile renders the notebook-storage root, same as before this
// shape existed. `path` is the tile's currently open file, relative to
// whichever root applies.
export interface NotebookTileParams {
  root?: string;
  path?: string;
}

// parseNotebookTileParams decodes a persisted notebook tile's tileParams.
// Two encodings coexist on purpose: pre-arbitrary-roots tiles persisted a bare
// path string (no JSON envelope), and every tile still round-trips through
// that legacy shape when it has no root — see serializeNotebookTileParams.
// A JSON-looking string that fails to parse is treated as a (verbatim, if
// unusual) legacy path rather than dropped, so a malformed persisted value
// degrades to "reopen this path" instead of "lose the tile's file".
export function parseNotebookTileParams(raw: string | undefined | null): NotebookTileParams {
  if (!raw) {
    return {};
  }
  if (raw.startsWith('{')) {
    try {
      const parsed = JSON.parse(raw) as Record<string, unknown>;
      const result: NotebookTileParams = {};
      if (typeof parsed.root === 'string' && parsed.root) {
        result.root = parsed.root;
      }
      if (typeof parsed.path === 'string' && parsed.path) {
        result.path = parsed.path;
      }
      return result;
    } catch {
      return { path: raw };
    }
  }
  return { path: raw };
}

// serializeNotebookTileParams is the inverse of parseNotebookTileParams. A
// rootless tile (the common case today) keeps persisting the legacy bare-path
// format so its params stay readable by, and round-trip through, code that
// still expects the pre-arbitrary-roots shape.
export function serializeNotebookTileParams(params: NotebookTileParams): string {
  if (params.root) {
    return JSON.stringify(params.path ? { root: params.root, path: params.path } : { root: params.root });
  }
  return params.path || '';
}

// resolveEditorTileRoot picks the root a freshly-docked ⌘⌥N editor tile should
// open at: the active workspace's directory, so the tile browses the tree the
// user is actually working in rather than forcing a jump to notebook storage.
// Returns undefined (rootless — notebook-rooted) when the directory is unset
// or already equals the notebook root: those tiles keep the always-on
// notebook watcher and notebook-only affordances (backlinks, etc.) instead of
// being pinned to a redundant explicit root.
export function resolveEditorTileRoot(
  workspaceDirectory: string | undefined,
  effectiveNotebookRoot: string,
): string | undefined {
  const trimmed = workspaceDirectory?.trim();
  if (!trimmed || trimmed === effectiveNotebookRoot) {
    return undefined;
  }
  return trimmed;
}

function agentTerminalsFromPanes(panes: PaneElement[]): AgentTerminal[] {
  return panes
    .filter((pane) => pane.kind === 'agent' && typeof pane.runtime_id === 'string' && typeof pane.session_id === 'string')
    .map((pane) => {
      const status = pane.status && pane.status !== 'ready' ? pane.status : undefined;
      return {
        id: pane.pane_id,
        runtimeId: pane.runtime_id as string,
        sessionId: pane.session_id as string,
        title: pane.title || pane.pane_id,
        ...(status ? { status } : {}),
        ...(pane.error ? { error: pane.error } : {}),
      };
    });
}

export function workspaceSnapshotFromDaemonWorkspace(workspace: DaemonWorkspaceSnapshot): TerminalWorkspaceSnapshot {
  const agents = agentTerminalsFromPanes(workspace.panes || []);
  const firstAgentPaneId = agents[0]?.id || '';
  const nextWorkspace: TerminalWorkspaceState = {
    agents,
    layoutTree: parseLayoutJSON(workspace.layout_json || ''),
  };
  const daemonActivePaneId = workspace.active_pane_id || firstAgentPaneId;

  return {
    workspace: nextWorkspace,
    daemonActivePaneId: nextWorkspace.layoutTree && hasPane(nextWorkspace.layoutTree, daemonActivePaneId)
      ? daemonActivePaneId
      : firstAgentPaneId,
  };
}
