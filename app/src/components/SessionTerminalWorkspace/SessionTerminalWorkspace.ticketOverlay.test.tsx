import { afterEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, waitFor } from '@testing-library/react';
import { SessionTerminalWorkspace } from './index';
import { createPaneRuntimeEventRouterController } from './paneRuntimeEventRouter';
import type { TerminalWorkspaceState } from '../../types/workspace';
import type { Ticket } from '../../hooks/useDaemonSocket';
import { TicketStatus } from '../../types/generated';
import { _resetEscapeStackForTest } from '../../hooks/useEscapeStack';

// Records focus() calls so the focus-restore assertions can see that closing the
// overlay routed focus back through the active pane's GhosttyTerminal handle
// (critical pattern #6). The mock exposes an imperative handle whose focus()
// returns true so runtime.focusPane resolves on the first attempt.
const { focusState } = vi.hoisted(() => ({ focusState: { count: 0 } }));

vi.mock('../GhosttyTerminal', async () => {
  const React = await import('react');
  return {
    GhosttyTerminal: React.forwardRef(function MockTerminal(_props: unknown, ref: React.Ref<unknown>) {
      React.useImperativeHandle(ref, () => ({
        focus: () => {
          focusState.count += 1;
          return true;
        },
        getSize: () => ({ cols: 80, rows: 24 }),
        // No-op the rest of the handle surface the runtime touches on mount/fit.
        fit: () => {},
        openFind: () => {},
        typeTextViaInput: () => false,
        isInputFocused: () => false,
        write: async () => {},
        resizeLocal: async () => {},
        reset: () => {},
        scrollToTop: () => false,
        getText: () => '',
        hasMeasuredSize: () => true,
        overflowsContainer: () => false,
      }));
      return null;
    }),
  };
});

function makeTicket(overrides: Partial<Ticket> = {}): Ticket {
  return {
    id: 'store-migration',
    title: 'Migrate the store',
    description: 'Move the store to X',
    status: TicketStatus.Working,
    assignee: 'sess-1',
    cwd: '/repo',
    last_agent_id: 'codex',
    project_id: '',
    created_at: '2026-06-27T10:00:00Z',
    updated_at: '2026-06-27T10:05:00Z',
    activity: [],
    artifacts: [],
    ...overrides,
  };
}

function singlePaneWorkspace(): TerminalWorkspaceState {
  return {
    agents: [{ id: 'pane-term', runtimeId: 'rt-1', sessionId: 'sess-1', title: 'shell' }],
    layoutTree: { type: 'pane', paneId: 'pane-term' },
  };
}

function renderWorkspace(options: {
  ticket?: Ticket;
  withActions?: boolean;
} = {}) {
  const { ticket, withActions = true } = options;
  const fetchTicket = vi.fn().mockResolvedValue(ticket ?? makeTicket());
  const onChangeStatus = vi.fn().mockResolvedValue(undefined);
  const onAddComment = vi.fn().mockResolvedValue(undefined);
  const onEditDescription = vi.fn().mockResolvedValue(undefined);
  const onResume = vi.fn();
  const ticketActions = withActions
    ? { fetchTicket, onChangeStatus, onAddComment, onEditDescription, onResume }
    : undefined;
  const utils = render(
    <SessionTerminalWorkspace
      workspaceId="workspace-1"
      workspaceSessions={[{
        id: 'sess-1',
        label: 'shell',
        agent: 'shell',
        cwd: '/repo',
        state: 'idle',
        ticketUnread: false,
        isActive: true,
        ticket,
      }]}
      ticketActions={ticketActions}
      workspace={singlePaneWorkspace()}
      activePaneId="pane-term"
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
  return { ...utils, fetchTicket, onChangeStatus, onResume };
}

describe('SessionTerminalWorkspace ticket overlay', () => {
  afterEach(() => {
    _resetEscapeStackForTest();
    focusState.count = 0;
  });

  it('renders the chip and an ambient header for a session with a bound ticket', () => {
    const { container } = renderWorkspace({ ticket: makeTicket() });
    // Single-pane workspace: the header is normally hidden, but a bound ticket
    // makes it ambient.
    expect(container.querySelector('.workspace-pane-header')).not.toBeNull();
    expect(container.querySelector('.workspace-pane-header-hidden')).toBeNull();
    expect(container.querySelector('[data-testid="ticket-chip-sess-1"]')).not.toBeNull();
  });

  it('does not render the chip for a session without a bound ticket', () => {
    const { container } = renderWorkspace({ ticket: undefined });
    expect(container.querySelector('[data-testid="ticket-chip-sess-1"]')).toBeNull();
    // Header stays hidden with nothing ambient to show.
    expect(container.querySelector('.workspace-pane-header-hidden')).not.toBeNull();
  });

  it('opens the overlay on chip click and fetches the ticket once', async () => {
    const { container, getByTestId, fetchTicket } = renderWorkspace({ ticket: makeTicket() });
    expect(container.querySelector('[data-testid="ticket-overlay-pane-term"]')).toBeNull();

    fireEvent.click(getByTestId('ticket-chip-sess-1'));

    await waitFor(() => expect(getByTestId('ticket-detail-panel')).toBeTruthy());
    expect(getByTestId('ticket-overlay-pane-term')).toBeTruthy();
    expect(fetchTicket).toHaveBeenCalledTimes(1);
    expect(fetchTicket).toHaveBeenCalledWith('store-migration');
  });

  it('routes a status change through onChangeStatus (wiring proof)', async () => {
    const { getByTestId, onChangeStatus } = renderWorkspace({ ticket: makeTicket() });
    fireEvent.click(getByTestId('ticket-chip-sess-1'));
    await waitFor(() => getByTestId('ticket-detail-panel'));

    fireEvent.change(getByTestId('ticket-status-select'), { target: { value: 'done' } });

    await waitFor(() => expect(onChangeStatus).toHaveBeenCalledTimes(1));
    expect(onChangeStatus.mock.calls[0][0]).toBe('store-migration');
    expect(onChangeStatus.mock.calls[0][1]).toBe('done');
  });

  it('closes on Escape and restores focus to the active pane', async () => {
    const { container, getByTestId } = renderWorkspace({ ticket: makeTicket() });
    fireEvent.click(getByTestId('ticket-chip-sess-1'));
    await waitFor(() => getByTestId('ticket-detail-panel'));

    const before = focusState.count;
    fireEvent.keyDown(window, { key: 'Escape' });

    await waitFor(() => expect(container.querySelector('[data-testid="ticket-overlay-pane-term"]')).toBeNull());
    expect(focusState.count).toBeGreaterThan(before);
  });

  it('closes on chip re-click and restores focus', async () => {
    const { container, getByTestId } = renderWorkspace({ ticket: makeTicket() });
    fireEvent.click(getByTestId('ticket-chip-sess-1'));
    await waitFor(() => getByTestId('ticket-detail-panel'));

    const before = focusState.count;
    fireEvent.click(getByTestId('ticket-chip-sess-1'));

    await waitFor(() => expect(container.querySelector('[data-testid="ticket-overlay-pane-term"]')).toBeNull());
    expect(focusState.count).toBeGreaterThan(before);
  });

  it('closes on the panel ✕ and restores focus', async () => {
    const { container, getByTestId, getByLabelText } = renderWorkspace({ ticket: makeTicket() });
    fireEvent.click(getByTestId('ticket-chip-sess-1'));
    await waitFor(() => getByTestId('ticket-detail-panel'));

    const before = focusState.count;
    fireEvent.click(getByLabelText('Close ticket detail'));

    await waitFor(() => expect(container.querySelector('[data-testid="ticket-overlay-pane-term"]')).toBeNull());
    expect(focusState.count).toBeGreaterThan(before);
  });
});
