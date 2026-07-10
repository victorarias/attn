import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { PaneTicketChip } from './PaneTicketChip';
import type { Ticket } from '../hooks/useDaemonSocket';
import { TicketStatus } from '../types/generated';

function makeTicket(overrides: Partial<Ticket> = {}): Ticket {
  return {
    id: 'store-migration',
    title: 'Migrate the store',
    description: '',
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

describe('PaneTicketChip', () => {
  it('renders the ticket title and status', () => {
    render(
      <PaneTicketChip ticket={makeTicket()} unread={false} open={false} sessionId="sess-1" onToggle={vi.fn()} />,
    );
    const chip = screen.getByTestId('ticket-chip-sess-1');
    expect(chip.getAttribute('data-status')).toBe('working');
    expect(screen.getByText('Migrate the store')).toBeTruthy();
  });

  it('shows the unread dot only when there is unread activity', () => {
    const { rerender } = render(
      <PaneTicketChip ticket={makeTicket()} unread={false} open={false} sessionId="sess-1" onToggle={vi.fn()} />,
    );
    expect(screen.queryByTestId('ticket-chip-unread-sess-1')).toBeNull();

    rerender(
      <PaneTicketChip ticket={makeTicket()} unread open={false} sessionId="sess-1" onToggle={vi.fn()} />,
    );
    expect(screen.getByTestId('ticket-chip-unread-sess-1')).toBeTruthy();
  });

  it('reflects the open state via aria-pressed', () => {
    const { rerender } = render(
      <PaneTicketChip ticket={makeTicket()} unread={false} open={false} sessionId="sess-1" onToggle={vi.fn()} />,
    );
    expect(screen.getByTestId('ticket-chip-sess-1').getAttribute('aria-pressed')).toBe('false');
    rerender(
      <PaneTicketChip ticket={makeTicket()} unread={false} open sessionId="sess-1" onToggle={vi.fn()} />,
    );
    expect(screen.getByTestId('ticket-chip-sess-1').getAttribute('aria-pressed')).toBe('true');
  });

  it('fires onToggle on click without bubbling to the pane header', () => {
    const onToggle = vi.fn();
    const onPaneClick = vi.fn();
    const onPanePointerDown = vi.fn();
    render(
      <div onClick={onPaneClick} onPointerDown={onPanePointerDown}>
        <PaneTicketChip ticket={makeTicket()} unread={false} open={false} sessionId="sess-1" onToggle={onToggle} />
      </div>,
    );
    const chip = screen.getByTestId('ticket-chip-sess-1');
    // The pane header is a leaf-drag handle (beginLeafDrag); a press on the chip
    // must not reach it or a sloppy click would relocate the pane.
    fireEvent.pointerDown(chip);
    expect(onPanePointerDown).not.toHaveBeenCalled();
    fireEvent.click(chip);
    expect(onToggle).toHaveBeenCalledTimes(1);
    expect(onPaneClick).not.toHaveBeenCalled();
  });
});
