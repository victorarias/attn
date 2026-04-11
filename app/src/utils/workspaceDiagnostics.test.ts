import { describe, expect, it } from 'vitest';
import { collectWorkspaceLayoutDiagnostics, projectWorkspaceBounds } from './workspaceDiagnostics';
import type { TerminalLayoutNode } from '../types/workspace';

describe('workspaceDiagnostics', () => {
  it('collects pane paths and normalized bounds for nested splits', () => {
    const layoutTree: TerminalLayoutNode = {
      type: 'split',
      splitId: 'root-split',
      direction: 'vertical',
      ratio: 0.25,
      children: [
        { type: 'pane', paneId: 'main' },
        {
          type: 'split',
          splitId: 'right-split',
          direction: 'horizontal',
          ratio: 0.4,
          children: [
            { type: 'pane', paneId: 'shell-a' },
            { type: 'pane', paneId: 'shell-b' },
          ],
        },
      ],
    };

    const snapshot = collectWorkspaceLayoutDiagnostics(layoutTree);

    expect(snapshot.paneCount).toBe(3);
    expect(snapshot.splitCount).toBe(2);
    expect(snapshot.panes).toEqual([
      {
        paneId: 'main',
        path: 'root/0',
        depth: 1,
        bounds: { left: 0, top: 0, right: 0.25, bottom: 1, width: 0.25, height: 1 },
      },
      {
        paneId: 'shell-a',
        path: 'root/1/0',
        depth: 2,
        bounds: { left: 0.25, top: 0, right: 1, bottom: 0.4, width: 0.75, height: 0.4 },
      },
      {
        paneId: 'shell-b',
        path: 'root/1/1',
        depth: 2,
        bounds: { left: 0.25, top: 0.4, right: 1, bottom: 1, width: 0.75, height: 0.6 },
      },
    ]);
    expect(snapshot.splits[0]).toMatchObject({
      splitId: 'root-split',
      path: 'root',
      direction: 'vertical',
      ratio: 0.25,
      spanCount: 2,
      firstChildSpan: 1,
      secondChildSpan: 1,
      firstChildPath: 'root/0',
      secondChildPath: 'root/1',
    });
    expect(snapshot.splits[1]).toMatchObject({
      splitId: 'right-split',
      path: 'root/1',
      direction: 'horizontal',
      ratio: 0.4,
      spanCount: 2,
      firstChildSpan: 1,
      secondChildSpan: 1,
      firstChildPath: 'root/1/0',
      secondChildPath: 'root/1/1',
    });
  });

  it('tracks same-direction span counts for chained splits', () => {
    const layoutTree: TerminalLayoutNode = {
      type: 'split',
      splitId: 'root',
      direction: 'vertical',
      ratio: 2 / 3,
      children: [
        {
          type: 'split',
          splitId: 'left',
          direction: 'vertical',
          ratio: 0.5,
          children: [
            { type: 'pane', paneId: 'main' },
            { type: 'pane', paneId: 'shell-a' },
          ],
        },
        { type: 'pane', paneId: 'shell-b' },
      ],
    };

    const snapshot = collectWorkspaceLayoutDiagnostics(layoutTree);

    expect(snapshot.splits[0]).toMatchObject({
      splitId: 'root',
      spanCount: 3,
      firstChildSpan: 2,
      secondChildSpan: 1,
    });
    expect(snapshot.splits[1]).toMatchObject({
      splitId: 'left',
      spanCount: 2,
      firstChildSpan: 1,
      secondChildSpan: 1,
    });
  });

  it('projects normalized bounds into workspace pixels', () => {
    expect(projectWorkspaceBounds(
      { left: 0.25, top: 0.4, right: 1, bottom: 1, width: 0.75, height: 0.6 },
      1000,
      500,
    )).toEqual({
      x: 250,
      y: 200,
      width: 750,
      height: 300,
    });
  });
});
