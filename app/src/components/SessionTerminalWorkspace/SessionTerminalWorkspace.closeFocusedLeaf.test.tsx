import { useState, type ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render } from '@testing-library/react';
import { SessionTerminalWorkspace } from './index';
import { createPaneRuntimeEventRouterController } from './paneRuntimeEventRouter';
import { tileContentKey, type TerminalWorkspaceState } from '../../types/workspace';
import { NotebookSurfaceProvider, type NotebookSurfaceContextValue } from '../../contexts/NotebookSurfaceContext';
import type { WorkspaceSelectionStyle } from '../../utils/workspaceSelectionStyle';

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
// graph stays light in jsdom. The stub still announces readiness and takes real
// DOM focus, because when a mounting terminal grabs focus is exactly what this
// spec is about.
const terminalFocusCalls = vi.hoisted(() => [] as string[]);
vi.mock('../GhosttyTerminal', async () => {
  const React = await import('react');
  return {
    GhosttyTerminal: React.forwardRef(function MockTerminal(
      props: { onReady?: (handle: unknown) => void; runtimeLogMeta?: { paneId?: string } },
      ref,
    ) {
      const nodeRef = React.useRef<HTMLDivElement | null>(null);
      const paneId = props.runtimeLogMeta?.paneId ?? 'pane';
      const handle = React.useMemo(() => ({
        focus: () => {
          terminalFocusCalls.push(paneId);
          nodeRef.current?.focus();
          return true;
        },
        getSize: () => null,
        fit: () => {},
        setSurfaceReleased: () => {},
      }), [paneId]);
      React.useImperativeHandle(ref, () => handle, [handle]);
      // Once per mount, like the real terminal: onReady's identity changes every
      // parent render, so firing on its identity would announce readiness forever.
      const onReadyRef = React.useRef(props.onReady);
      onReadyRef.current = props.onReady;
      React.useEffect(() => {
        onReadyRef.current?.(handle);
      }, [handle]);
      return <div ref={nodeRef} tabIndex={-1} data-testid={`mock-terminal-${paneId}`} />;
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

// The same workspace with the tile undocked — one terminal pane, no split.
function paneOnlyWorkspace(): TerminalWorkspaceState {
  return {
    agents: [{ id: 'pane-term', runtimeId: 'rt-1', sessionId: 'sess-1', title: 'shell' }],
    layoutTree: { type: 'pane', paneId: 'pane-term' },
  };
}

function renderSplit(overrides: {
  onClosePane?: () => void;
  onUndockTile?: (tileId: string) => void;
  onFocusPane?: (paneId: string) => void;
  workspaceSelectionStyle?: WorkspaceSelectionStyle;
} = {}) {
  const onClosePane = overrides.onClosePane ?? vi.fn();
  const onUndockTile = overrides.onUndockTile ?? vi.fn();
  const onFocusPane = overrides.onFocusPane ?? vi.fn();
  const eventRouter = createPaneRuntimeEventRouterController();
  // Zoom mode lives in App, so a standalone render needs a host to hold it.
  function ZoomHost({ workspace }: { workspace: TerminalWorkspaceState }) {
    const [zoomActive, setZoomActive] = useState(false);
    return (
      <SessionTerminalWorkspace
        workspaceId="workspace-split"
        workspaceSessions={[{ id: 'sess-1', label: 'shell', agent: 'shell', cwd: '/tmp/project' }]}
        workspace={workspace}
        workspaceSelectionStyle={overrides.workspaceSelectionStyle}
        activePaneId="pane-term"
        fontSize={13}
        enabled
        isActiveSession
        eventRouter={eventRouter}
        onSplitPane={vi.fn()}
        onClosePane={onClosePane}
        onFocusPane={onFocusPane}
        onNavigateOutOfSession={vi.fn()}
        onUndockTile={onUndockTile}
        zoomActive={zoomActive}
        onSetZoomActive={setZoomActive}
        tileContents={{
          [tileContentKey('workspace-split', 'tile-notes')]: {
            path: '/tmp/project/NOTES.md',
            content: '# Project notes',
          },
        }}
        onRequestTileContent={vi.fn()}
      />
    );
  }
  const element = (workspace: TerminalWorkspaceState) => <ZoomHost workspace={workspace} />;
  const utils = render(element(paneAndTileWorkspace()), { wrapper: NotebookSurfaceTestWrapper });
  const setWorkspace = (workspace: TerminalWorkspaceState) => utils.rerender(element(workspace));
  return { ...utils, setWorkspace, onClosePane, onUndockTile, onFocusPane };
}

function tileEl(container: HTMLElement): HTMLElement {
  return container.querySelector('[data-pane-kind="tile"]') as HTMLElement;
}

function paneEl(container: HTMLElement): HTMLElement {
  return container.querySelector('[data-pane-kind="agent"]') as HTMLElement;
}

describe('SessionTerminalWorkspace selection style', () => {
  it('marks the workspace with the selected treatment', () => {
    const { container } = renderSplit({ workspaceSelectionStyle: 'spotlight' });

    expect(container.querySelector('.session-terminal-workspace')).toHaveClass('workspace-selection--spotlight');
  });
});

describe('SessionTerminalWorkspace leaf focus', () => {
  // Clicking a tile must be enough — no programmatic focus() — for the tile to
  // become the focused leaf and take real DOM focus. Everything else here
  // (Cmd+W routing, the ⌘Enter send gate, keyboard scrolling) rides on it.
  it('makes a clicked tile the active leaf and gives its body DOM focus', () => {
    const { container } = renderSplit();

    const tile = tileEl(container);
    expect(tile.getAttribute('data-pane-id')).toBe('tile-notes');
    expect(tile.className).not.toContain('active');

    fireEvent.mouseDown(tile);

    expect(tile.className).toContain('active');
    expect(paneEl(container).className).not.toContain('active');
    expect(container.querySelector('.session-terminal-workspace')
      ?.getAttribute('data-active-leaf-id')).toBe('tile-notes');
    const tileBody = tile.querySelector('.workspace-dock-tile-body') as HTMLElement;
    expect(document.activeElement).toBe(tileBody);
  });

  // Zoom and maximize follow the focused leaf, so they land on a tile too —
  // ⌘⇧Z used to be able to target only a terminal pane.
  it('zooms and maximizes the focused tile', () => {
    const { container } = renderSplit();
    const surface = container.querySelector('.session-terminal-workspace') as HTMLElement;

    fireEvent.mouseDown(tileEl(container));
    fireEvent.keyDown(document.activeElement as HTMLElement, { key: 'z', metaKey: true, shiftKey: true });
    expect(surface.getAttribute('data-zoomed-pane-id')).toBe('tile-notes');

    fireEvent.keyDown(document.activeElement as HTMLElement, { key: 'Enter', metaKey: true, shiftKey: true });
    expect(surface.getAttribute('data-maximized-pane-id')).toBe('tile-notes');
    expect(surface.getAttribute('data-zoomed-pane-id')).toBe('');
    // The maximized tile renders alone, keeping its identity as a tile leaf.
    expect(container.querySelector('[data-pane-kind="agent"]')).toBeNull();
    expect(tileEl(container).getAttribute('data-pane-id')).toBe('tile-notes');
  });

  // A markdown tile's id is derived from its file path, so closing one and
  // reopening the same file produces the SAME leaf id. A maximized leaf that
  // leaves the layout must therefore be forgotten, or reopening the file would
  // silently come back maximized.
  it('forgets a maximized tile once it leaves the layout', () => {
    const { container, setWorkspace } = renderSplit();
    const surface = () => container.querySelector('.session-terminal-workspace') as HTMLElement;

    fireEvent.mouseDown(tileEl(container));
    fireEvent.keyDown(document.activeElement as HTMLElement, { key: 'Enter', metaKey: true, shiftKey: true });
    expect(surface().getAttribute('data-maximized-pane-id')).toBe('tile-notes');

    setWorkspace(paneOnlyWorkspace());
    setWorkspace(paneAndTileWorkspace());

    expect(surface().getAttribute('data-maximized-pane-id')).toBe('');
    expect(container.querySelector('[data-pane-kind="agent"]')).not.toBeNull();
  });

  // Clicking back onto the terminal returns the focus display to the pane. The
  // pane's own focus goes through onFocusPane (activePaneId is owned upstream),
  // so the assertion here is the callback plus the chrome.
  it('returns the active leaf to the terminal pane when it is clicked', () => {
    const { container, onFocusPane } = renderSplit();

    fireEvent.mouseDown(tileEl(container));
    expect(tileEl(container).className).toContain('active');

    fireEvent.mouseDown(paneEl(container));

    expect(onFocusPane).toHaveBeenCalledWith('pane-term');
    expect(tileEl(container).className).not.toContain('active');
    expect(paneEl(container).className).toContain('active');
  });
});

describe('SessionTerminalWorkspace Cmd+W closes the focused leaf', () => {
  // Regression: Cmd+W from inside a docked notebook tile used to close the
  // previously-active terminal pane (activePaneId still pointed at it), killing
  // the wrong leaf. It must undock the focused tile instead.
  it('undocks the focused tile instead of closing the active terminal pane', () => {
    const { container, onClosePane, onUndockTile } = renderSplit();

    const tile = tileEl(container);
    fireEvent.mouseDown(tile);

    fireEvent.keyDown(document.activeElement as HTMLElement, { key: 'w', metaKey: true });

    expect(onUndockTile).toHaveBeenCalledTimes(1);
    expect(onUndockTile).toHaveBeenCalledWith('tile-notes');
    expect(onClosePane).not.toHaveBeenCalled();
  });

  // Focus utility terminal (⌘`) focuses the pane without changing activePaneId —
  // the pane was already the session's active one. The focused tile has to be
  // released anyway, or the active leaf stays the tile and Cmd+W undocks the
  // document you just typed away from.
  it('closes the terminal pane after Focus utility terminal takes focus back from a tile', () => {
    const { container, onClosePane, onUndockTile } = renderSplit();

    fireEvent.mouseDown(tileEl(container));
    expect(tileEl(container).className).toContain('active');

    fireEvent.keyDown(document.activeElement as HTMLElement, { key: '`', metaKey: true });

    expect(tileEl(container).className).not.toContain('active');
    expect(paneEl(container).className).toContain('active');

    fireEvent.keyDown(paneEl(container), { key: 'w', metaKey: true });

    expect(onClosePane).toHaveBeenCalledTimes(1);
    expect(onClosePane).toHaveBeenCalledWith('pane-term');
    expect(onUndockTile).not.toHaveBeenCalled();
  });

  // A terminal that mounts while a tile holds focus must leave it alone. Maximizing
  // the tile unmounts the terminal and restoring remounts it, so its ready callback
  // fires with the tile still the focused leaf — if it grabbed focus there, DOM
  // focus and the active leaf would disagree, which is the state the Cmd+W cases
  // above are protecting against.
  it('leaves a focused tile alone when a terminal remounts and announces readiness', () => {
    const { container } = renderSplit();
    terminalFocusCalls.length = 0;

    fireEvent.mouseDown(tileEl(container));
    const tileBody = tileEl(container).querySelector('.workspace-dock-tile-body') as HTMLElement;
    expect(document.activeElement).toBe(tileBody);

    // Maximize the tile (terminal unmounts), then restore it (terminal remounts).
    fireEvent.keyDown(document.activeElement as HTMLElement, { key: 'Enter', metaKey: true, shiftKey: true });
    expect(container.querySelector('[data-pane-kind="agent"]')).toBeNull();
    fireEvent.keyDown(document.activeElement as HTMLElement, { key: 'Enter', metaKey: true, shiftKey: true });
    expect(container.querySelector('[data-pane-kind="agent"]')).not.toBeNull();

    expect(terminalFocusCalls).toEqual([]);
    expect(tileEl(container).className).toContain('active');
    expect(document.activeElement).toBe(tileEl(container).querySelector('.workspace-dock-tile-body'));
  });

  // The terminal-pane path is unchanged: Cmd+W with the pane focused closes that
  // pane, never the tile — including after a tile visit.
  it('closes the active terminal pane when the pane is the focused leaf', () => {
    const { container, onClosePane, onUndockTile } = renderSplit();

    const pane = paneEl(container);
    expect(pane.getAttribute('data-pane-id')).toBe('pane-term');
    fireEvent.mouseDown(tileEl(container));
    fireEvent.mouseDown(pane);

    fireEvent.keyDown(pane, { key: 'w', metaKey: true });

    expect(onClosePane).toHaveBeenCalledTimes(1);
    expect(onClosePane).toHaveBeenCalledWith('pane-term');
    expect(onUndockTile).not.toHaveBeenCalled();
  });
});
