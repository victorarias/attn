import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { SessionTerminalWorkspace } from './index';
import { createPaneRuntimeEventRouterController } from './paneRuntimeEventRouter';
import type { TerminalWorkspaceState } from '../../types/workspace';
import type { Presentation } from '../../types/generated';

// The terminal surface pulls in the Ghostty WASM model; stub it so the import
// graph stays light in jsdom (this spec only cares about the pane header).
vi.mock('../GhosttyTerminal', async () => {
  const React = await import('react');
  return {
    GhosttyTerminal: React.forwardRef(function MockTerminal() {
      return null;
    }),
  };
});

function loneAgentWorkspace(): TerminalWorkspaceState {
  return {
    agents: [{ id: 'pane-1', runtimeId: 'rt-1', sessionId: 'sess-1', title: 'shell' }],
    layoutTree: { type: 'pane', paneId: 'pane-1' },
  };
}

function makePresentation(overrides: Partial<Presentation> = {}): Presentation {
  return {
    id: 'pres-1',
    created_at: '2026-07-01T00:00:00Z',
    kind: 'pr',
    latest_round_seq: 1,
    latest_round_submitted: false,
    repo_path: '/repo',
    session_id: 'sess-1',
    status: 'open',
    title: 'My presentation',
    ...overrides,
  };
}

describe('SessionTerminalWorkspace presentation chip', () => {
  // A lone (unsplit) pane normally hides its header entirely (it's not a
  // drag handle when there's nothing to drag against). A session with an
  // open, unsubmitted presentation must still get its header rectangle to
  // host the chip, same as the nudge indicator does.
  it('forces the header visible and renders the chip when the pane session has a presentation', () => {
    const onOpenPresentation = vi.fn();
    render(
      <SessionTerminalWorkspace
        workspaceId="workspace-1"
        workspaceSessions={[{
          id: 'sess-1',
          label: 'shell',
          agent: 'shell',
          cwd: '/tmp/project',
          presentation: makePresentation(),
        }]}
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
        onOpenPresentation={onOpenPresentation}
      />,
    );

    const header = document.querySelector('.workspace-pane-header');
    expect(header?.className).not.toContain('workspace-pane-header-hidden');

    const chip = screen.getByRole('button', { name: /review/i });
    expect(chip).toHaveAttribute('title', 'My presentation');
    fireEvent.click(chip);
    expect(onOpenPresentation).toHaveBeenCalledWith('pres-1');
  });

  it('hides the header when the pane session has no presentation and no nudge', () => {
    render(
      <SessionTerminalWorkspace
        workspaceId="workspace-1"
        workspaceSessions={[{ id: 'sess-1', label: 'shell', agent: 'shell', cwd: '/tmp/project' }]}
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
      />,
    );

    const header = document.querySelector('.workspace-pane-header');
    expect(header?.className).toContain('workspace-pane-header-hidden');
    expect(screen.queryByRole('button', { name: /review/i })).not.toBeInTheDocument();
  });
});
