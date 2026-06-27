import { describe, it, expect, vi } from 'vitest';
import { render, screen, waitFor, sleep } from '../test/utils';
import { TicketDetailPanel } from './TicketDetailPanel';
import type { Ticket } from '../hooks/useDaemonSocket';
import { TicketStatus, TicketActivityKind } from '../types/generated';

function makeTicket(overrides: Partial<Ticket> = {}): Ticket {
  return {
    id: 'store-migration',
    title: 'Migrate the store',
    description: 'Move the store to X',
    status: TicketStatus.InReview,
    assignee: 'sess-1',
    cwd: '/repo',
    last_agent_id: 'codex',
    project_id: '',
    created_at: '2026-06-27T10:00:00Z',
    updated_at: '2026-06-27T10:05:00Z',
    activity: [
      {
        id: 1,
        kind: TicketActivityKind.StatusChange,
        author: 'sess-1',
        from_status: TicketStatus.Working,
        to_status: TicketStatus.InReview,
        comment: 'ready for a look',
        created_at: '2026-06-27T10:05:00Z',
      },
      {
        id: 2,
        kind: TicketActivityKind.Comment,
        author: 'chief-1',
        comment: 'looks good',
        created_at: '2026-06-27T10:06:00Z',
      },
    ],
    attachments: [
      { id: 1, filename: 'report.md', path: '/repo/report.md', note: 'the findings', created_at: '2026-06-27T10:07:00Z' },
    ],
    ...overrides,
  };
}

describe('TicketDetailPanel', () => {
  it('fetches the full record exactly once on open and renders it', async () => {
    const full = makeTicket();
    const fetchTicket = vi.fn().mockResolvedValue(full);

    render(
      <TicketDetailPanel
        isOpen
        ticketId="store-migration"
        ticketRow={makeTicket()}
        fetchTicket={fetchTicket}
        onClose={() => {}}
      />,
    );

    await waitFor(() => screen.getByText('Move the store to X'));
    // history (status change + comment) and attachments are the full-record detail
    expect(screen.getByText('ready for a look')).toBeTruthy();
    expect(screen.getByText('looks good')).toBeTruthy();
    expect(screen.getByText('report.md')).toBeTruthy();

    expect(fetchTicket).toHaveBeenCalledTimes(1);
    expect(fetchTicket).toHaveBeenCalledWith('store-migration');
    // No fetch loop after settle.
    await sleep(60);
    expect(fetchTicket).toHaveBeenCalledTimes(1);
  });

  it('does not fetch while closed', async () => {
    const fetchTicket = vi.fn().mockResolvedValue(makeTicket());
    const { container } = render(
      <TicketDetailPanel
        isOpen={false}
        ticketId="store-migration"
        ticketRow={makeTicket()}
        fetchTicket={fetchTicket}
        onClose={() => {}}
      />,
    );
    await sleep(40);
    expect(fetchTicket).not.toHaveBeenCalled();
    // Closed panel renders nothing.
    expect(container.querySelector('[data-testid="ticket-detail-panel"]')).toBeNull();
  });

  it('re-fetches when the ticket changes under an open panel (live refresh)', async () => {
    const fetchTicket = vi.fn().mockResolvedValue(makeTicket());
    const row = makeTicket();
    const { rerender } = render(
      <TicketDetailPanel
        isOpen
        ticketId="store-migration"
        ticketRow={row}
        fetchTicket={fetchTicket}
        onClose={() => {}}
      />,
    );
    await waitFor(() => expect(fetchTicket).toHaveBeenCalledTimes(1));

    // A mutation bumps updated_at on the board row → the open panel re-fetches.
    rerender(
      <TicketDetailPanel
        isOpen
        ticketId="store-migration"
        ticketRow={{ ...row, updated_at: '2026-06-27T11:00:00Z' }}
        fetchTicket={fetchTicket}
        onClose={() => {}}
      />,
    );
    await waitFor(() => expect(fetchTicket).toHaveBeenCalledTimes(2));
    await sleep(60);
    expect(fetchTicket).toHaveBeenCalledTimes(2);
  });

  it('shows the bare-row header while the full record is still loading', async () => {
    // A fetch that never resolves: the header must still appear from the bare row.
    const fetchTicket = vi.fn().mockReturnValue(new Promise<Ticket>(() => {}));
    render(
      <TicketDetailPanel
        isOpen
        ticketId="store-migration"
        ticketRow={makeTicket({ title: 'Bare row title' })}
        fetchTicket={fetchTicket}
        onClose={() => {}}
      />,
    );
    await waitFor(() => screen.getByText('Bare row title'));
    expect(screen.getByText('Loading…')).toBeTruthy();
  });

  it('surfaces a fetch error', async () => {
    const fetchTicket = vi.fn().mockRejectedValue(new Error('ticket not found: store-migration'));
    render(
      <TicketDetailPanel
        isOpen
        ticketId="store-migration"
        ticketRow={undefined}
        fetchTicket={fetchTicket}
        onClose={() => {}}
      />,
    );
    await waitFor(() => screen.getByRole('alert'));
    expect(screen.getByText('ticket not found: store-migration')).toBeTruthy();
  });

  it('calls onClose when the close button is clicked', async () => {
    const onClose = vi.fn();
    const fetchTicket = vi.fn().mockResolvedValue(makeTicket());
    render(
      <TicketDetailPanel
        isOpen
        ticketId="store-migration"
        ticketRow={makeTicket()}
        fetchTicket={fetchTicket}
        onClose={onClose}
      />,
    );
    await waitFor(() => screen.getByText('Move the store to X'));
    screen.getByLabelText('Close ticket detail').click();
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
