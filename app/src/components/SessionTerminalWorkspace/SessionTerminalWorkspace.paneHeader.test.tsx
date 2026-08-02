import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { SessionTerminalWorkspace } from './index';
import { createPaneRuntimeEventRouterController } from './paneRuntimeEventRouter';
import type { TerminalWorkspaceState } from '../../types/workspace';

// The terminal surface pulls in the Ghostty WASM model; stub it so the import
// graph stays light (this spec only cares about the pane header).
vi.mock('../GhosttyTerminal', async () => {
  const React = await import('react');
  return {
    GhosttyTerminal: React.forwardRef(function MockTerminal() {
      return null;
    }),
  };
});

/**
 * The pane header used to be conditional — a split-view drag handle that a
 * nudge, ticket, presentation, or settle countdown could also summon onto a lone
 * tile. It is unconditional now, because it carries the session's generated
 * name, and which agent you are looking at is not a detail to hide behind a
 * split.
 */

const GENERATED_NAME = 'judge yielded stops so background waits stay green';

function loneAgentWorkspace(): TerminalWorkspaceState {
  return {
    agents: [{ id: 'pane-1', runtimeId: 'rt-1', sessionId: 'sess-1', title: 'shell' }],
    layoutTree: { type: 'pane', paneId: 'pane-1' },
  };
}

function renderLonePane(props: Partial<React.ComponentProps<typeof SessionTerminalWorkspace>> = {}) {
  return render(
    <SessionTerminalWorkspace
      workspaceId="workspace-1"
      workspaceSessions={[{ id: 'sess-1', label: GENERATED_NAME, agent: 'claude', cwd: '/tmp/project' }]}
      workspace={loneAgentWorkspace()}
      activePaneId="pane-1"
      fontSize={13}
      enabled
      isActiveSession
      eventRouter={createPaneRuntimeEventRouterController()}
      onSplitPane={vi.fn()}
      onClosePane={vi.fn()}
      onFocusPane={vi.fn()}
      onNavigateOutOfSession={vi.fn()}
      {...props}
    />,
  );
}

describe('SessionTerminalWorkspace pane header', () => {
  it('names the session on a lone tile with nothing else to show', () => {
    const { container } = renderLonePane();

    expect(container.querySelector('.workspace-pane-title')?.textContent).toBe(GENERATED_NAME);
  });

  it('is not a drag handle on a lone tile, which has nowhere to move to', () => {
    const { container } = renderLonePane();

    const header = container.querySelector('.workspace-pane-header') as HTMLElement;
    expect(header.className).toContain('workspace-pane-header--static');
    expect(header.className).not.toContain('workspace-pane-header--draggable');
    expect(header).not.toHaveAttribute('title');
  });

  it('offers rename on a lone tile, where a wrong generated name is most visible', () => {
    // The name is generated, so the place it is shown is exactly the place a bad
    // one needs correcting — previously this button was split-view only.
    const onRenameSession = vi.fn();
    renderLonePane({ onRenameSession });

    const rename = screen.getByTestId('rename-pane-pane-1');
    fireEvent.click(rename);

    expect(screen.getByDisplayValue(GENERATED_NAME)).toBeInTheDocument();
  });

  it("shows the session's state beside its name, as the sidebar row does", () => {
    const { container } = renderLonePane({
      workspaceSessions: [{ id: 'sess-1', label: GENERATED_NAME, agent: 'claude', cwd: '/tmp/project', state: 'working' }],
    });

    const dot = container.querySelector('.workspace-pane-header .state-indicator');
    expect(dot?.className).toContain('state-indicator--working');
  });

  it('falls back to the pane title when the session carries no label', () => {
    const { container } = renderLonePane({ workspaceSessions: [] });

    expect(container.querySelector('.workspace-pane-title')?.textContent).toBe('shell');
  });
});
