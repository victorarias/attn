import type { WorkspaceLayout as DaemonWorkspaceSnapshot, PaneElement } from './generated';

export type TerminalSplitDirection = 'vertical' | 'horizontal';
export type TerminalNavigationDirection = 'left' | 'right' | 'up' | 'down';
export type TerminalDockEdge = 'left' | 'right' | 'top' | 'bottom';

// PanelKind is the surface a docked panel renders. The daemon treats it as an
// opaque token (it persists where a panel sits, not what it shows), so adding a
// kind here is a frontend-only change. Kept open-ended for forward
// compatibility with panels a newer daemon might persist.
export type PanelKind = 'markdown' | (string & {});

export interface TerminalPaneLeaf {
  type: 'pane';
  paneId: string;
}

// A docked panel is a first-class layout leaf alongside terminal panes: it
// takes real space and resizes through the same split machinery. Unlike the
// slide-in overlays (diff/review/git-status), it lives in the daemon-owned
// layout tree and persists across restart and clients.
export interface PanelLeaf {
  type: 'panel';
  panelId: string;
  panelKind: PanelKind;
  // Opaque per-panel data persisted with the layout by the daemon. For markdown
  // panels this is the absolute path of the file the panel renders. Empty when
  // the daemon persisted no params.
  panelParams?: string;
}

export type TerminalLeaf = TerminalPaneLeaf | PanelLeaf;

// Daemon-served rendered content for a docked panel (the markdown file's text).
// Delivered as the reply to workspace_panel_content_get and re-pushed on every
// on-disk change (live reload). Absent from the store until the first reply.
export interface PanelContentState {
  path: string;
  content: string;
  error?: string;
}

// panelContentKey keys panel content by workspace + panel, since a panel id like
// `panel-markdown` is reused across workspaces.
export function panelContentKey(workspaceId: string, panelId: string): string {
  return `${workspaceId}::${panelId}`;
}

export interface TerminalSplitNode {
  type: 'split';
  splitId: string;
  direction: TerminalSplitDirection;
  ratio: number;
  children: [TerminalLayoutNode, TerminalLayoutNode];
}

export type TerminalLayoutNode = TerminalPaneLeaf | PanelLeaf | TerminalSplitNode;

// leafSlotId is the stable key a leaf occupies in bounds/path maps. Terminal
// panes key by paneId; panels key by panelId. The two id spaces are disjoint by
// construction (distinct daemon prefixes).
export function leafSlotId(node: TerminalLeaf): string {
  return node.type === 'pane' ? node.paneId : node.panelId;
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

// findPanelByKind returns the first docked panel of the given kind, or null.
// Used to drive UI toggles ("is markdown docked in this workspace?").
export function findPanelByKind(node: TerminalLayoutNode | null, kind: PanelKind): PanelLeaf | null {
  if (!node) {
    return null;
  }
  if (node.type === 'panel') {
    return node.panelKind === kind ? node : null;
  }
  if (node.type === 'split') {
    return findPanelByKind(node.children[0], kind) || findPanelByKind(node.children[1], kind);
  }
  return null;
}

export function hasPane(node: TerminalLayoutNode, paneId: string): boolean {
  if (node.type === 'pane') {
    return node.paneId === paneId;
  }
  if (node.type === 'panel') {
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

type LeafKind = 'pane' | 'panel';

interface LeafBounds {
  bounds: PaneBounds;
  kind: LeafKind;
}

// collectLeafBounds records the normalized rect of every leaf (pane and panel),
// keyed by its slot id, descending through splits with their ratios. Panels are
// recorded so the workspace can position them; the kind lets callers that only
// care about terminals (navigation) filter panels out.
function collectLeafBounds(
  node: TerminalLayoutNode,
  bounds: PaneBounds,
  result: Map<string, LeafBounds>
): void {
  if (node.type === 'pane') {
    result.set(node.paneId, { bounds, kind: 'pane' });
    return;
  }
  if (node.type === 'panel') {
    result.set(node.panelId, { bounds, kind: 'panel' });
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
// (terminal panes and docked panels), keyed by slot id.
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
  // Navigation only moves between terminal panes — docked panels are not focus
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
    value.type === 'panel' &&
    typeof value.panel_id === 'string' &&
    value.panel_id.length > 0 &&
    typeof value.panel_kind === 'string' &&
    value.panel_kind.length > 0
  ) {
    return {
      type: 'panel',
      panelId: value.panel_id,
      panelKind: value.panel_kind,
      panelParams: typeof value.panel_params === 'string' ? value.panel_params : undefined,
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

function parseLayoutJSON(layoutJSON: string): TerminalLayoutNode | null {
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
