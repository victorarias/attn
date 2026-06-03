import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { SessionTerminalWorkspace } from './index';
import { createPaneRuntimeEventRouterController } from './paneRuntimeEventRouter';
import { tileContentKey, type TerminalWorkspaceState } from '../../types/workspace';

// The terminal surface pulls in the Ghostty WASM model; a tile-only workspace
// never mounts one, but stub it so the import graph stays light in jsdom.
vi.mock('../GhosttyTerminal', async () => {
  const React = await import('react');
  return {
    GhosttyTerminal: React.forwardRef(function MockTerminal() {
      return null;
    }),
  };
});

function tileOnlyWorkspace(): TerminalWorkspaceState {
  return {
    agents: [],
    layoutTree: {
      type: 'tile',
      tileId: 'tile-readme',
      tileKind: 'markdown',
      tileParams: '/tmp/project/README.md',
    },
  };
}

function renderTileOnly() {
  return render(
    <SessionTerminalWorkspace
      workspaceId="workspace-tiles"
      workspace={tileOnlyWorkspace()}
      activePaneId=""
      fontSize={13}
      enabled
      isActiveSession
      eventRouter={createPaneRuntimeEventRouterController()}
      onSplitPane={vi.fn()}
      onClosePane={vi.fn()}
      onFocusPane={vi.fn()}
      onNavigateOutOfSession={vi.fn()}
      tileContents={{
        [tileContentKey('workspace-tiles', 'tile-readme')]: {
          path: '/tmp/project/README.md',
          content: '# Project notes',
        },
      }}
      onRequestTileContent={vi.fn()}
    />,
  );
}

describe('SessionTerminalWorkspace tile-only (sessionless) rendering', () => {
  // Regression: a workspace with zero agent panes still has to render its docked
  // tile leaf. Previously the workspace collapsed to the empty placeholder once
  // the last terminal closed, leaving the tile invisible.
  // https://github.com/victorarias/attn/pull/257#pullrequestreview-4413194398
  it('renders the docked tile when the workspace has no agent panes', () => {
    const { container } = renderTileOnly();

    const tileSurface = container.querySelector('[data-pane-kind="tile"]');
    expect(tileSurface).not.toBeNull();
    expect(tileSurface?.getAttribute('data-pane-id')).toBe('tile-readme');
    expect(tileSurface?.getAttribute('data-tile-kind')).toBe('markdown');

    // The tile's markdown content is actually mounted, not just a placeholder.
    expect(screen.getByRole('heading', { name: 'Project notes' })).toBeInTheDocument();
  });

  it('does not fall back to the empty workspace placeholder', () => {
    const { container } = renderTileOnly();

    const workspaceRoot = container.querySelector('[data-session-terminal-workspace="workspace-tiles"]');
    expect(workspaceRoot).not.toBeNull();
    // The empty-state placeholder renders an element with no child surfaces; the
    // real layout renders the tile surface inside the panes container.
    expect(workspaceRoot?.querySelector('.session-terminal-panes')).not.toBeNull();
  });
});
