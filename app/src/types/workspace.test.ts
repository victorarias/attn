import { describe, expect, it } from 'vitest';
import { findPaneInDirection, type TerminalLayoutNode } from './workspace';
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
