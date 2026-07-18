import type { ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render } from '@testing-library/react';
import { SessionTerminalWorkspace } from './index';
import { createPaneRuntimeEventRouterController } from './paneRuntimeEventRouter';
import { tileContentKey, type TerminalWorkspaceState } from '../../types/workspace';
import { NotebookSurfaceProvider, type NotebookSurfaceContextValue } from '../../contexts/NotebookSurfaceContext';

// The docked markdown tile below reads effectiveNotebookRoot unconditionally
// via useNotebookSurfaceContext — real usage is always under App's provider.
const testSurfaceValue: NotebookSurfaceContextValue = {
  makeDaemon: () => ({
    listDir: vi.fn(),
    readFile: vi.fn(),
    writeFile: vi.fn(),
    existsFile: vi.fn(),
    readAsset: vi.fn(),
    backlinksNotebook: vi.fn(),
    sendToChief: vi.fn(),
    listFiles: vi.fn(),
    changeSignal: 0,
  }),
  effectiveNotebookRoot: '',
  sendFsWatch: vi.fn(),
  sendFsUnwatch: vi.fn(),
  connectionGeneration: 0,
};
function NotebookSurfaceTestWrapper({ children }: { children: ReactNode }) {
  return <NotebookSurfaceProvider value={testSurfaceValue}>{children}</NotebookSurfaceProvider>;
}

// The terminal surface pulls in the Ghostty WASM model; stub it so the import
// graph stays light in jsdom (this spec only cares about Cmd+W routing).
vi.mock('../GhosttyTerminal', async () => {
  const React = await import('react');
  return {
    GhosttyTerminal: React.forwardRef(function MockTerminal() {
      return null;
    }),
  };
});

// A split workspace: one terminal pane beside a docked markdown tile. This is the
// shape that produces the reported bug — the tile lives inside
// .session-terminal-workspace, so its Cmd+W is dispatched as terminal.close.
function paneAndTileWorkspace(): TerminalWorkspaceState {
  return {
    agents: [{ id: 'pane-term', runtimeId: 'rt-1', sessionId: 'sess-1', title: 'shell' }],
    layoutTree: {
      type: 'split',
      splitId: 'split-1',
      direction: 'vertical',
      ratio: 0.6,
      children: [
        { type: 'pane', paneId: 'pane-term' },
        { type: 'tile', tileId: 'tile-notes', tileKind: 'markdown', tileParams: '/tmp/project/NOTES.md' },
      ],
    },
  };
}

function renderSplit(overrides: { onClosePane?: () => void; onUndockTile?: (tileId: string) => void } = {}) {
  const onClosePane = overrides.onClosePane ?? vi.fn();
  const onUndockTile = overrides.onUndockTile ?? vi.fn();
  const utils = render(
    <SessionTerminalWorkspace
      workspaceId="workspace-split"
      workspaceSessions={[{ id: 'sess-1', label: 'shell', agent: 'shell', cwd: '/tmp/project' }]}
      workspace={paneAndTileWorkspace()}
      activePaneId="pane-term"
      fontSize={13}
      enabled
      isActiveSession
      eventRouter={createPaneRuntimeEventRouterController()}
      onSplitPane={vi.fn()}
      onClosePane={onClosePane}
      onFocusPane={vi.fn()}
      onNavigateOutOfSession={vi.fn()}
      onUndockTile={onUndockTile}
      tileContents={{
        [tileContentKey('workspace-split', 'tile-notes')]: {
          path: '/tmp/project/NOTES.md',
          content: '# Project notes',
        },
      }}
      onRequestTileContent={vi.fn()}
    />,
    { wrapper: NotebookSurfaceTestWrapper },
  );
  return { ...utils, onClosePane, onUndockTile };
}

describe('SessionTerminalWorkspace Cmd+W closes the focused leaf', () => {
  // Regression: Cmd+W from inside a docked notebook tile used to close the
  // previously-active terminal pane (activePaneId still pointed at it), killing
  // the wrong leaf. It must undock the focused tile instead.
  it('undocks the focused tile instead of closing the active terminal pane', () => {
    const { container, onClosePane, onUndockTile } = renderSplit();

    const tile = container.querySelector('[data-pane-kind="tile"]');
    expect(tile?.getAttribute('data-pane-id')).toBe('tile-notes');
    const tileBody = tile?.querySelector('.workspace-dock-tile-body') as HTMLElement;
    expect(tileBody).toBeTruthy();
    tileBody.focus();

    fireEvent.keyDown(tileBody, { key: 'w', metaKey: true });

    expect(onUndockTile).toHaveBeenCalledTimes(1);
    expect(onUndockTile).toHaveBeenCalledWith('tile-notes');
    expect(onClosePane).not.toHaveBeenCalled();
  });

  // The terminal-pane path is unchanged: Cmd+W with focus on a terminal pane
  // closes that pane (activePaneId), never the tile.
  it('closes the active terminal pane when focus is not inside a tile', () => {
    const { container, onClosePane, onUndockTile } = renderSplit();

    const pane = container.querySelector('[data-pane-kind="agent"]') as HTMLElement;
    expect(pane?.getAttribute('data-pane-id')).toBe('pane-term');

    fireEvent.keyDown(pane, { key: 'w', metaKey: true });

    expect(onClosePane).toHaveBeenCalledTimes(1);
    expect(onClosePane).toHaveBeenCalledWith('pane-term');
    expect(onUndockTile).not.toHaveBeenCalled();
  });
});
