import { describe, expect, it } from 'vitest';
import { WorkspaceLayoutPaneKind, WorkspaceLayoutPaneStatus } from './generated';
import {
  applyRatioOverrides,
  collectSplitRatios,
  findPaneInDirection,
  findPanelByKind,
  getNormalizedPaneBounds,
  getSplitDividers,
  hasPane,
  workspaceSnapshotFromDaemonWorkspace,
  type TerminalLayoutNode,
} from './workspace';
const SESSION_PANE_ID = 'pane-session';

describe('findPaneInDirection', () => {
  it('moves horizontally across sibling panes', () => {
    const layout: TerminalLayoutNode = {
      type: 'split',
      splitId: 'root',
      direction: 'vertical',
      ratio: 0.5,
      children: [
        { type: 'pane', paneId: SESSION_PANE_ID },
        { type: 'pane', paneId: 'right' },
      ],
    };

    expect(findPaneInDirection(layout, SESSION_PANE_ID, 'right')).toBe('right');
    expect(findPaneInDirection(layout, 'right', 'left')).toBe(SESSION_PANE_ID);
  });

  it('moves vertically inside a nested split', () => {
    const layout: TerminalLayoutNode = {
      type: 'split',
      splitId: 'root',
      direction: 'vertical',
      ratio: 0.5,
      children: [
        { type: 'pane', paneId: SESSION_PANE_ID },
        {
          type: 'split',
          splitId: 'right',
          direction: 'horizontal',
          ratio: 0.5,
          children: [
            { type: 'pane', paneId: 'top-right' },
            { type: 'pane', paneId: 'bottom-right' },
          ],
        },
      ],
    };

    expect(findPaneInDirection(layout, 'top-right', 'down')).toBe('bottom-right');
    expect(findPaneInDirection(layout, 'bottom-right', 'up')).toBe('top-right');
  });

  it('returns the nearest pane in the requested direction', () => {
    const layout: TerminalLayoutNode = {
      type: 'split',
      splitId: 'root',
      direction: 'horizontal',
      ratio: 0.5,
      children: [
        {
          type: 'split',
          splitId: 'top',
          direction: 'vertical',
          ratio: 0.5,
          children: [
            { type: 'pane', paneId: 'top-left' },
            { type: 'pane', paneId: 'top-right' },
          ],
        },
        { type: 'pane', paneId: 'bottom' },
      ],
    };

    expect(findPaneInDirection(layout, 'top-left', 'right')).toBe('top-right');
    expect(findPaneInDirection(layout, 'top-right', 'down')).toBe('bottom');
  });

  it('returns null when there is no pane in that direction', () => {
    const layout: TerminalLayoutNode = {
      type: 'split',
      splitId: 'root',
      direction: 'vertical',
      ratio: 0.5,
      children: [
        { type: 'pane', paneId: SESSION_PANE_ID },
        { type: 'pane', paneId: 'right' },
      ],
    };

    expect(findPaneInDirection(layout, SESSION_PANE_ID, 'left')).toBeNull();
    expect(findPaneInDirection(layout, 'right', 'right')).toBeNull();
    expect(findPaneInDirection(layout, 'missing', 'right')).toBeNull();
  });
});

const verticalSplit: TerminalLayoutNode = {
  type: 'split',
  splitId: 'root',
  direction: 'vertical',
  ratio: 0.6,
  children: [
    { type: 'pane', paneId: 'a' },
    {
      type: 'split',
      splitId: 'inner',
      direction: 'horizontal',
      ratio: 0.5,
      children: [
        { type: 'pane', paneId: 'b' },
        { type: 'pane', paneId: 'c' },
      ],
    },
  ],
};

describe('getSplitDividers', () => {
  it('returns one divider per split with its container bounds', () => {
    const dividers = getSplitDividers(verticalSplit);
    expect(dividers).toHaveLength(2);

    const root = dividers.find((d) => d.splitId === 'root')!;
    expect(root.direction).toBe('vertical');
    expect(root.ratio).toBeCloseTo(0.6);
    expect(root).toMatchObject({ left: 0, top: 0, right: 1, bottom: 1 });

    // The inner split lives in the right 40% of the workspace.
    const inner = dividers.find((d) => d.splitId === 'inner')!;
    expect(inner.direction).toBe('horizontal');
    expect(inner.left).toBeCloseTo(0.6);
    expect(inner.right).toBeCloseTo(1);
  });

  it('returns no dividers for a single pane', () => {
    expect(getSplitDividers({ type: 'pane', paneId: 'only' })).toEqual([]);
  });
});

describe('applyRatioOverrides', () => {
  it('overrides a matching split ratio and recomputes pane bounds', () => {
    const overridden = applyRatioOverrides(verticalSplit, new Map([['root', 0.25]]));
    const bounds = getNormalizedPaneBounds(overridden);
    // Pane 'a' now occupies the left 25% instead of 60%.
    expect(bounds.get('a')!.right).toBeCloseTo(0.25);
    // Original tree is untouched.
    expect((verticalSplit as { ratio: number }).ratio).toBe(0.6);
  });

  it('returns the same reference when there are no overrides', () => {
    const overrides = new Map<string, number>();
    expect(applyRatioOverrides(verticalSplit, overrides)).toBe(verticalSplit);
  });
});

describe('collectSplitRatios', () => {
  it('maps each split id to its ratio', () => {
    const ratios = collectSplitRatios(verticalSplit);
    expect(ratios.get('root')).toBeCloseTo(0.6);
    expect(ratios.get('inner')).toBeCloseTo(0.5);
    expect(ratios.size).toBe(2);
  });
});

// a | md | b — a docked markdown panel sitting between two terminal panes.
const paneWithPanel: TerminalLayoutNode = {
  type: 'split',
  splitId: 'root',
  direction: 'vertical',
  ratio: 0.5,
  children: [
    { type: 'pane', paneId: 'a' },
    {
      type: 'split',
      splitId: 'inner',
      direction: 'vertical',
      ratio: 0.5,
      children: [
        { type: 'panel', panelId: 'md', panelKind: 'markdown' },
        { type: 'pane', paneId: 'b' },
      ],
    },
  ],
};

describe('docked panels', () => {
  it('positions panel slots alongside panes', () => {
    const bounds = getNormalizedPaneBounds(paneWithPanel);
    // a occupies the left half; md the next quarter; b the last quarter.
    expect(bounds.get('a')!.right).toBeCloseTo(0.5);
    expect(bounds.get('md')!.left).toBeCloseTo(0.5);
    expect(bounds.get('md')!.right).toBeCloseTo(0.75);
    expect(bounds.get('b')!.left).toBeCloseTo(0.75);
  });

  it('skips panels when navigating between panes', () => {
    // Right from 'a' jumps over the markdown panel to the next terminal pane.
    expect(findPaneInDirection(paneWithPanel, 'a', 'right')).toBe('b');
    expect(findPaneInDirection(paneWithPanel, 'b', 'left')).toBe('a');
    // A panel is never itself a navigation target.
    expect(findPaneInDirection(paneWithPanel, 'md', 'left')).toBeNull();
  });

  it('hasPane never matches a panel id', () => {
    expect(hasPane(paneWithPanel, 'md')).toBe(false);
    expect(hasPane(paneWithPanel, 'a')).toBe(true);
  });

  it('findPanelByKind locates a docked panel', () => {
    expect(findPanelByKind(paneWithPanel, 'markdown')?.panelId).toBe('md');
    expect(findPanelByKind(paneWithPanel, 'diff')).toBeNull();
    expect(findPanelByKind({ type: 'pane', paneId: 'a' }, 'markdown')).toBeNull();
  });

  it('parses panel leaves out of the daemon layout_json', () => {
    const snapshot = workspaceSnapshotFromDaemonWorkspace({
      workspace_id: 'ws',
      active_pane_id: 'pane-a',
      layout_json: JSON.stringify({
        type: 'split',
        split_id: 'root',
        direction: 'vertical',
        ratio: 0.68,
        ratio_locked: true,
        children: [
          { type: 'pane', pane_id: 'pane-a' },
          { type: 'panel', panel_id: 'panel-md', panel_kind: 'markdown' },
        ],
      }),
      panes: [
        { pane_id: 'pane-a', workspace_id: 'ws', kind: WorkspaceLayoutPaneKind.Agent, title: 'A', status: WorkspaceLayoutPaneStatus.Ready, runtime_id: 'r', session_id: 's' },
      ],
    });
    expect(findPanelByKind(snapshot.workspace.layoutTree, 'markdown')?.panelId).toBe('panel-md');
    // The panel does not leak into agent bookkeeping.
    expect(snapshot.workspace.agents.map((agent) => agent.id)).toEqual(['pane-a']);
  });

  it('drops malformed panel leaves (missing kind)', () => {
    const snapshot = workspaceSnapshotFromDaemonWorkspace({
      workspace_id: 'ws',
      active_pane_id: 'pane-a',
      layout_json: JSON.stringify({
        type: 'split',
        split_id: 'root',
        direction: 'vertical',
        ratio: 0.5,
        children: [
          { type: 'pane', pane_id: 'pane-a' },
          { type: 'panel', panel_id: 'panel-md' },
        ],
      }),
      panes: [
        { pane_id: 'pane-a', workspace_id: 'ws', kind: WorkspaceLayoutPaneKind.Agent, title: 'A', status: WorkspaceLayoutPaneStatus.Ready, runtime_id: 'r', session_id: 's' },
      ],
    });
    // A split whose second child fails to parse yields no usable tree.
    expect(snapshot.workspace.layoutTree).toBeNull();
  });
});
