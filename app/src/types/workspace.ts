import type { WorkspaceSnapshot as DaemonWorkspaceSnapshot, PaneElement } from './generated';

export const MAIN_TERMINAL_PANE_ID = 'main';

export type TerminalSplitDirection = 'vertical' | 'horizontal';
export type TerminalNavigationDirection = 'left' | 'right' | 'up' | 'down';

export interface TerminalPaneLeaf {
  type: 'pane';
  paneId: string;
}

export interface TerminalSplitNode {
  type: 'split';
  splitId: string;
  direction: TerminalSplitDirection;
  ratio: number;
  children: [TerminalLayoutNode, TerminalLayoutNode];
}

export type TerminalLayoutNode = TerminalPaneLeaf | TerminalSplitNode;

export interface UtilityTerminal {
  id: string;
  ptyId: string;
  title: string;
}

export interface TerminalPanelState {
  activePaneId: string;
  terminals: UtilityTerminal[];
  layoutTree: TerminalLayoutNode;
}

interface PaneBounds {
  left: number;
  top: number;
  right: number;
  bottom: number;
  centerX: number;
  centerY: number;
}

export function createDefaultPanelState(): TerminalPanelState {
  return {
    activePaneId: MAIN_TERMINAL_PANE_ID,
    terminals: [],
    layoutTree: { type: 'pane', paneId: MAIN_TERMINAL_PANE_ID },
  };
}

export function hasPane(node: TerminalLayoutNode, paneId: string): boolean {
  if (node.type === 'pane') {
    return node.paneId === paneId;
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

function collectPaneBounds(
  node: TerminalLayoutNode,
  bounds: PaneBounds,
  result: Map<string, PaneBounds>
): void {
  if (node.type === 'pane') {
    result.set(node.paneId, bounds);
    return;
  }

  const ratio = node.ratio > 0 && node.ratio < 1 ? node.ratio : 0.5;
  if (node.direction === 'vertical') {
    const splitX = bounds.left + (bounds.right - bounds.left) * ratio;
    collectPaneBounds(node.children[0], paneBounds(bounds.left, bounds.top, splitX, bounds.bottom), result);
    collectPaneBounds(node.children[1], paneBounds(splitX, bounds.top, bounds.right, bounds.bottom), result);
    return;
  }

  const splitY = bounds.top + (bounds.bottom - bounds.top) * ratio;
  collectPaneBounds(node.children[0], paneBounds(bounds.left, bounds.top, bounds.right, splitY), result);
  collectPaneBounds(node.children[1], paneBounds(bounds.left, splitY, bounds.right, bounds.bottom), result);
}

function overlapSize(aStart: number, aEnd: number, bStart: number, bEnd: number): number {
  return Math.max(0, Math.min(aEnd, bEnd) - Math.max(aStart, bStart));
}

export function findPaneInDirection(
  node: TerminalLayoutNode,
  fromPaneId: string,
  direction: TerminalNavigationDirection
): string | null {
  const rects = new Map<string, PaneBounds>();
  collectPaneBounds(node, paneBounds(0, 0, 1, 1), rects);
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

function parseLayoutJSON(layoutJSON: string): TerminalLayoutNode {
  if (!layoutJSON.trim()) {
    return createDefaultPanelState().layoutTree;
  }
  try {
    const parsed = JSON.parse(layoutJSON);
    return parseLayoutNode(parsed) ?? createDefaultPanelState().layoutTree;
  } catch {
    return createDefaultPanelState().layoutTree;
  }
}

function shellTerminalsFromPanes(panes: PaneElement[]): UtilityTerminal[] {
  return panes
    .filter((pane) => pane.kind === 'shell' && typeof pane.runtime_id === 'string' && pane.runtime_id.length > 0)
    .map((pane) => ({
      id: pane.pane_id,
      ptyId: pane.runtime_id as string,
      title: pane.title || pane.pane_id,
    }));
}

export function panelStateFromDaemonWorkspace(workspace: DaemonWorkspaceSnapshot): TerminalPanelState {
  const panel: TerminalPanelState = {
    activePaneId: workspace.active_pane_id || MAIN_TERMINAL_PANE_ID,
    terminals: shellTerminalsFromPanes(workspace.panes || []),
    layoutTree: parseLayoutJSON(workspace.layout_json || ''),
  };

  if (!hasPane(panel.layoutTree, panel.activePaneId)) {
    panel.activePaneId = MAIN_TERMINAL_PANE_ID;
  }
  return panel;
}
