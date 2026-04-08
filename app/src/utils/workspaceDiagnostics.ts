import type { TerminalLayoutNode, TerminalSplitDirection } from '../types/workspace';

export interface WorkspaceNormalizedBounds {
  left: number;
  top: number;
  right: number;
  bottom: number;
  width: number;
  height: number;
}

export interface WorkspaceProjectedBounds {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface WorkspacePaneLayoutDiagnostic {
  paneId: string;
  path: string;
  depth: number;
  bounds: WorkspaceNormalizedBounds;
}

export interface WorkspaceSplitLayoutDiagnostic {
  splitId: string;
  path: string;
  depth: number;
  direction: TerminalSplitDirection;
  ratio: number;
  spanCount: number;
  firstChildSpan: number;
  secondChildSpan: number;
  bounds: WorkspaceNormalizedBounds;
  firstChildPath: string;
  secondChildPath: string;
  firstChildBounds: WorkspaceNormalizedBounds;
  secondChildBounds: WorkspaceNormalizedBounds;
}

export interface WorkspaceLayoutDiagnosticSnapshot {
  paneCount: number;
  splitCount: number;
  panes: WorkspacePaneLayoutDiagnostic[];
  splits: WorkspaceSplitLayoutDiagnostic[];
}

function clampSplitRatio(ratio: number): number {
  if (ratio > 0 && ratio < 1) {
    return ratio;
  }
  return 0.5;
}

function normalizedBounds(
  left: number,
  top: number,
  right: number,
  bottom: number,
): WorkspaceNormalizedBounds {
  return {
    left,
    top,
    right,
    bottom,
    width: right - left,
    height: bottom - top,
  };
}

function childPath(path: string, index: 0 | 1) {
  return `${path}/${index}`;
}

function chainSpanCount(node: TerminalLayoutNode, direction: TerminalSplitDirection): number {
  if (node.type !== 'split' || node.direction !== direction) {
    return 1;
  }
  return chainSpanCount(node.children[0], direction) + chainSpanCount(node.children[1], direction);
}

export function projectWorkspaceBounds(
  bounds: WorkspaceNormalizedBounds,
  rootWidth: number,
  rootHeight: number,
): WorkspaceProjectedBounds {
  return {
    x: Math.round(bounds.left * rootWidth),
    y: Math.round(bounds.top * rootHeight),
    width: Math.round(bounds.width * rootWidth),
    height: Math.round(bounds.height * rootHeight),
  };
}

export function collectWorkspaceLayoutDiagnostics(
  layoutTree: TerminalLayoutNode,
): WorkspaceLayoutDiagnosticSnapshot {
  const panes: WorkspacePaneLayoutDiagnostic[] = [];
  const splits: WorkspaceSplitLayoutDiagnostic[] = [];

  const walk = (
    node: TerminalLayoutNode,
    path: string,
    depth: number,
    bounds: WorkspaceNormalizedBounds,
  ) => {
    if (node.type === 'pane') {
      panes.push({
        paneId: node.paneId,
        path,
        depth,
        bounds,
      });
      return;
    }

    const ratio = clampSplitRatio(node.ratio);
    const firstPath = childPath(path, 0);
    const secondPath = childPath(path, 1);
    const firstChildSpan = chainSpanCount(node.children[0], node.direction);
    const secondChildSpan = chainSpanCount(node.children[1], node.direction);
    const firstChildBounds = node.direction === 'vertical'
      ? normalizedBounds(bounds.left, bounds.top, bounds.left + bounds.width * ratio, bounds.bottom)
      : normalizedBounds(bounds.left, bounds.top, bounds.right, bounds.top + bounds.height * ratio);
    const secondChildBounds = node.direction === 'vertical'
      ? normalizedBounds(firstChildBounds.right, bounds.top, bounds.right, bounds.bottom)
      : normalizedBounds(bounds.left, firstChildBounds.bottom, bounds.right, bounds.bottom);

    splits.push({
      splitId: node.splitId,
      path,
      depth,
      direction: node.direction,
      ratio,
      spanCount: firstChildSpan + secondChildSpan,
      firstChildSpan,
      secondChildSpan,
      bounds,
      firstChildPath: firstPath,
      secondChildPath: secondPath,
      firstChildBounds,
      secondChildBounds,
    });

    walk(node.children[0], firstPath, depth + 1, firstChildBounds);
    walk(node.children[1], secondPath, depth + 1, secondChildBounds);
  };

  walk(layoutTree, 'root', 0, normalizedBounds(0, 0, 1, 1));

  return {
    paneCount: panes.length,
    splitCount: splits.length,
    panes,
    splits,
  };
}
