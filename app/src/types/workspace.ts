import type { WorkspaceSnapshot as DaemonWorkspaceSnapshot, PaneElement } from './generated';

export const MAIN_TERMINAL_PANE_ID = 'main';

export type TerminalSplitDirection = 'vertical' | 'horizontal';

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
