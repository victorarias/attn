import { afterEach, describe, expect, it } from 'vitest';
import {
  fitRequiresTerminalResize,
  isWorkspaceResizeDragActive,
} from './GhosttyTerminal';

describe('GhosttyTerminal resize policy', () => {
  afterEach(() => {
    delete document.documentElement.dataset.attnWorkspaceResizing;
  });

  it('detects when a workspace split drag is active', () => {
    const panes = document.createElement('div');
    panes.className = 'session-terminal-panes';
    panes.dataset.resizingSplitId = 'root';
    const terminal = document.createElement('div');
    panes.appendChild(terminal);

    expect(isWorkspaceResizeDragActive(terminal)).toBe(true);

    delete panes.dataset.resizingSplitId;
    expect(isWorkspaceResizeDragActive(terminal)).toBe(false);

    document.documentElement.dataset.attnWorkspaceResizing = '1';
    expect(isWorkspaceResizeDragActive(terminal)).toBe(true);
  });

  it('treats identical fit geometry as a no-op', () => {
    expect(fitRequiresTerminalResize(
      { cols: 120, rows: 40 },
      { cols: 120, rows: 40 },
    )).toBe(false);
    expect(fitRequiresTerminalResize(
      { cols: 120, rows: 40 },
      { cols: 121, rows: 40 },
    )).toBe(true);
  });
});
