import { describe, expect, it } from 'vitest';
import { WorkspaceLayoutPaneKind, WorkspaceLayoutPaneStatus } from './generated';
import {
  applyRatioOverrides,
  collectSplitRatios,
  findPaneInDirection,
  findTileByKind,
  getNormalizedPaneBounds,
  getSplitDividers,
  hasPane,
  parseNotebookTileParams,
  serializeNotebookTileParams,
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

// a | md | b — a docked markdown tile sitting between two terminal panes.
const paneWithTile: TerminalLayoutNode = {
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
        { type: 'tile', tileId: 'md', tileKind: 'markdown' },
        { type: 'pane', paneId: 'b' },
      ],
    },
  ],
};

describe('docked tiles', () => {
  it('positions tile slots alongside panes', () => {
    const bounds = getNormalizedPaneBounds(paneWithTile);
    // a occupies the left half; md the next quarter; b the last quarter.
    expect(bounds.get('a')!.right).toBeCloseTo(0.5);
    expect(bounds.get('md')!.left).toBeCloseTo(0.5);
    expect(bounds.get('md')!.right).toBeCloseTo(0.75);
    expect(bounds.get('b')!.left).toBeCloseTo(0.75);
  });

  it('skips tiles when navigating between panes', () => {
    // Right from 'a' jumps over the markdown tile to the next terminal pane.
    expect(findPaneInDirection(paneWithTile, 'a', 'right')).toBe('b');
    expect(findPaneInDirection(paneWithTile, 'b', 'left')).toBe('a');
    // A tile is never itself a navigation target.
    expect(findPaneInDirection(paneWithTile, 'md', 'left')).toBeNull();
  });

  it('hasPane never matches a tile id', () => {
    expect(hasPane(paneWithTile, 'md')).toBe(false);
    expect(hasPane(paneWithTile, 'a')).toBe(true);
  });

  it('findTileByKind locates a docked tile', () => {
    expect(findTileByKind(paneWithTile, 'markdown')?.tileId).toBe('md');
    expect(findTileByKind(paneWithTile, 'diff')).toBeNull();
    expect(findTileByKind({ type: 'pane', paneId: 'a' }, 'markdown')).toBeNull();
  });

  it('parses tile leaves out of the daemon layout_json', () => {
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
          { type: 'tile', tile_id: 'tile-md', tile_kind: 'markdown' },
        ],
      }),
      panes: [
        { pane_id: 'pane-a', workspace_id: 'ws', kind: WorkspaceLayoutPaneKind.Agent, title: 'A', status: WorkspaceLayoutPaneStatus.Ready, runtime_id: 'r', session_id: 's' },
      ],
    });
    expect(findTileByKind(snapshot.workspace.layoutTree, 'markdown')?.tileId).toBe('tile-md');
    // The tile does not leak into agent bookkeeping.
    expect(snapshot.workspace.agents.map((agent) => agent.id)).toEqual(['pane-a']);
  });

  it('drops malformed tile leaves (missing kind)', () => {
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
          { type: 'tile', tile_id: 'tile-md' },
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

describe('notebook tile params (parse/serialize)', () => {
  it('round-trips the legacy bare-path format for a rootless tile', () => {
    const raw = 'knowledge/areas/foo.md';
    const parsed = parseNotebookTileParams(raw);
    expect(parsed).toEqual({ path: raw });
    expect(serializeNotebookTileParams(parsed)).toBe(raw);
  });

  it('round-trips the {root, path} JSON envelope for a root-bound tile', () => {
    const raw = serializeNotebookTileParams({ root: '/Users/victor/code/attn', path: 'README.md' });
    expect(raw.startsWith('{')).toBe(true);
    const parsed = parseNotebookTileParams(raw);
    expect(parsed).toEqual({ root: '/Users/victor/code/attn', path: 'README.md' });
    expect(serializeNotebookTileParams(parsed)).toBe(raw);
  });

  it('treats a malformed JSON-looking string as a legacy bare path', () => {
    const raw = '{not valid json';
    expect(parseNotebookTileParams(raw)).toEqual({ path: raw });
  });

  it('serializes a root with no open path as {root} only', () => {
    const raw = serializeNotebookTileParams({ root: '/tmp/some-root' });
    expect(JSON.parse(raw)).toEqual({ root: '/tmp/some-root' });
    expect(parseNotebookTileParams(raw)).toEqual({ root: '/tmp/some-root' });
  });

  it('treats empty/null/undefined raw as no params', () => {
    expect(parseNotebookTileParams(undefined)).toEqual({});
    expect(parseNotebookTileParams(null)).toEqual({});
    expect(parseNotebookTileParams('')).toEqual({});
  });

  it('preserves a tile\'s root across a path update (open-file round trip)', () => {
    // Simulates WorkspaceDockTile's onOpenFile: parse the current params, keep
    // `root`, and reserialize with the newly opened path.
    const initial = parseNotebookTileParams(serializeNotebookTileParams({ root: '/repo', path: 'a.md' }));
    const afterOpen = serializeNotebookTileParams({ root: initial.root, path: 'b.md' });
    expect(parseNotebookTileParams(afterOpen)).toEqual({ root: '/repo', path: 'b.md' });
  });

  it('ignores unknown fields in the JSON envelope', () => {
    const raw = JSON.stringify({ root: '/repo', path: 'a.md', bogus: 'nope' });
    expect(parseNotebookTileParams(raw)).toEqual({ root: '/repo', path: 'a.md' });
  });
});
