import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent, within } from '../test/utils';
import {
  TicketBoardPanel,
  relativeTime,
  isSameLocalDay,
  applyFilter,
  groupTicketsByColumn,
  STATUS_COLUMNS,
} from './TicketBoardPanel';
import type { Ticket } from '../hooks/useDaemonSocket';
import { TicketStatus } from '../types/generated';

function makeTicket(overrides: Partial<Ticket> = {}): Ticket {
  return {
    id: 'tk-1',
    title: 'A ticket',
    description: '',
    status: TicketStatus.Todo,
    assignee: 'sess-1',
    cwd: '/repo',
    last_agent_id: 'codex',
    project_id: '',
    created_at: '2026-06-27T10:00:00Z',
    updated_at: '2026-06-27T10:05:00Z',
    activity: [],
    attachments: [],
    ...overrides,
  };
}

/** Find the column <section> for a given status. */
function column(status: string): HTMLElement {
  const sections = screen.getAllByTestId('ticket-board-column');
  const found = sections.find((s) => s.getAttribute('data-status') === status);
  if (!found) throw new Error(`no column for status ${status}`);
  return found;
}

describe('relativeTime', () => {
  const now = new Date('2026-06-27T12:00:00Z');
  it('formats seconds, minutes, hours, yesterday, days', () => {
    expect(relativeTime('2026-06-27T11:59:30Z', now)).toBe('30s');
    expect(relativeTime('2026-06-27T11:30:00Z', now)).toBe('30m');
    expect(relativeTime('2026-06-27T09:00:00Z', now)).toBe('3h');
    expect(relativeTime('2026-06-26T10:00:00Z', now)).toBe('yesterday');
    expect(relativeTime('2026-06-24T12:00:00Z', now)).toBe('3d');
  });
  it('floors sub-second to 1s and returns empty for garbage', () => {
    expect(relativeTime('2026-06-27T12:00:00Z', now)).toBe('1s');
    expect(relativeTime('not-a-date', now)).toBe('');
  });
});

describe('isSameLocalDay', () => {
  const now = new Date('2026-06-27T12:00:00Z');
  it('matches same local day, rejects other days and missing values', () => {
    // Noon UTC matches `now` (also noon UTC) in every timezone — avoids a
    // local-date flip that an early-UTC fixture would cause west of UTC.
    expect(isSameLocalDay('2026-06-27T12:00:00Z', now)).toBe(true);
    expect(isSameLocalDay('2026-06-25T12:00:00Z', now)).toBe(false);
    expect(isSameLocalDay(undefined, now)).toBe(false);
    expect(isSameLocalDay('garbage', now)).toBe(false);
  });
});

describe('applyFilter', () => {
  const now = new Date('2026-06-27T12:00:00Z');
  const tickets = [
    makeTicket({ id: 'a', status: TicketStatus.Working }),
    makeTicket({ id: 'b', status: TicketStatus.Blocked }),
    makeTicket({ id: 'c', status: TicketStatus.InReview }),
    makeTicket({ id: 'd', status: TicketStatus.Done, closed_at: '2026-06-27T11:00:00Z' }),
    makeTicket({ id: 'e', status: TicketStatus.Done, closed_at: '2026-06-20T08:00:00Z' }),
  ];

  it('all keeps everything', () => {
    expect(applyFilter(tickets, 'all', now).map((t) => t.id)).toEqual(['a', 'b', 'c', 'd', 'e']);
  });
  it('blocked / in_review narrow to status', () => {
    expect(applyFilter(tickets, 'blocked', now).map((t) => t.id)).toEqual(['b']);
    expect(applyFilter(tickets, 'in_review', now).map((t) => t.id)).toEqual(['c']);
  });
  it('closed_today keeps only tickets closed within today (by closed_at, not updated_at)', () => {
    expect(applyFilter(tickets, 'closed_today', now).map((t) => t.id)).toEqual(['d']);
  });
});

describe('groupTicketsByColumn', () => {
  it('buckets each status into its flow column preserving order', () => {
    const newer = makeTicket({ id: 'w-new', status: TicketStatus.Working, created_at: '2026-06-27T11:00:00Z' });
    const older = makeTicket({ id: 'w-old', status: TicketStatus.Working, created_at: '2026-06-27T09:00:00Z' });
    const cols = groupTicketsByColumn([newer, older, makeTicket({ id: 't', status: TicketStatus.Todo })]);
    const working = cols.find((c) => c.status === 'working')!;
    expect(working.cards.map((t) => t.id)).toEqual(['w-new', 'w-old']); // incoming (created_at desc) order preserved
    const todo = cols.find((c) => c.status === 'todo')!;
    expect(todo.cards.map((t) => t.id)).toEqual(['t']);
  });

  it('folds failed/crashed into the Done column terminalBad lane, not their own column', () => {
    const cols = groupTicketsByColumn([
      makeTicket({ id: 'd1', status: TicketStatus.Done }),
      makeTicket({ id: 'f1', status: TicketStatus.Failed }),
      makeTicket({ id: 'c1', status: TicketStatus.Crashed }),
    ]);
    // Only the five flow columns exist.
    expect(cols.map((c) => c.status)).toEqual(['todo', 'working', 'blocked', 'in_review', 'done']);
    const done = cols.find((c) => c.status === 'done')!;
    expect(done.cards.map((t) => t.id)).toEqual(['d1']);
    expect(done.terminalBad.map((t) => t.id)).toEqual(['f1', 'c1']);
  });
});

describe('TicketBoardPanel', () => {
  const baseProps = {
    isOpen: true as const,
    onOpenTicket: () => {},
    onClose: () => {},
  };

  it('renders a column for each flow status with the correct label', () => {
    render(<TicketBoardPanel {...baseProps} tickets={[makeTicket()]} />);
    for (const col of STATUS_COLUMNS) {
      const section = column(col.status);
      expect(within(section).getByText(col.label)).toBeTruthy();
    }
    // no extra columns for failed/crashed
    expect(screen.getAllByTestId('ticket-board-column')).toHaveLength(5);
  });

  it('groups a working ticket into Working and not Todo', () => {
    render(
      <TicketBoardPanel
        {...baseProps}
        tickets={[makeTicket({ id: 'w', status: TicketStatus.Working, title: 'Run it' })]}
      />,
    );
    expect(within(column('working')).getByText('Run it')).toBeTruthy();
    expect(within(column('todo')).queryByText('Run it')).toBeNull();
  });

  it('per-column header count reflects the cards shown', () => {
    render(
      <TicketBoardPanel
        {...baseProps}
        tickets={[
          makeTicket({ id: 'w1', status: TicketStatus.Working }),
          makeTicket({ id: 'w2', status: TicketStatus.Working }),
          makeTicket({ id: 'w3', status: TicketStatus.Working }),
        ]}
      />,
    );
    expect(within(column('working')).getByTestId('ticket-board-count').textContent).toBe('3');
    expect(within(column('todo')).getByTestId('ticket-board-count').textContent).toBe('0');
  });

  it('renders terminal-bad cards under a Closed divider in the Done column with data-terminal=bad', () => {
    render(
      <TicketBoardPanel
        {...baseProps}
        tickets={[
          makeTicket({ id: 'd1', status: TicketStatus.Done }),
          makeTicket({ id: 'f1', status: TicketStatus.Failed }),
        ]}
      />,
    );
    const done = column('done');
    expect(within(done).getByTestId('ticket-board-closed-divider')).toBeTruthy();
    const bad = within(done)
      .getAllByTestId('ticket-board-card')
      .find((c) => c.getAttribute('data-ticket-id') === 'f1')!;
    expect(bad.getAttribute('data-terminal')).toBe('bad');
  });

  it('shows a split "done · bad" count when terminal-bad exist, single number otherwise', () => {
    const { rerender } = render(
      <TicketBoardPanel
        {...baseProps}
        tickets={[
          makeTicket({ id: 'd1', status: TicketStatus.Done }),
          makeTicket({ id: 'd2', status: TicketStatus.Done }),
          makeTicket({ id: 'f1', status: TicketStatus.Failed }),
        ]}
      />,
    );
    expect(within(column('done')).getByTestId('ticket-board-count').textContent).toBe('2 · 1');

    rerender(<TicketBoardPanel {...baseProps} tickets={[makeTicket({ id: 'd1', status: TicketStatus.Done })]} />);
    expect(within(column('done')).getByTestId('ticket-board-count').textContent).toBe('1');
  });

  it('preserves incoming (created_at desc) card order within a column', () => {
    render(
      <TicketBoardPanel
        {...baseProps}
        tickets={[
          makeTicket({ id: 'newer', status: TicketStatus.Todo, created_at: '2026-06-27T11:00:00Z' }),
          makeTicket({ id: 'older', status: TicketStatus.Todo, created_at: '2026-06-27T09:00:00Z' }),
        ]}
      />,
    );
    const ids = within(column('todo'))
      .getAllByTestId('ticket-board-card')
      .map((c) => c.getAttribute('data-ticket-id'));
    expect(ids).toEqual(['newer', 'older']);
  });

  it('clicking a card calls onOpenTicket exactly once with the ticket id', () => {
    const onOpenTicket = vi.fn();
    render(
      <TicketBoardPanel
        {...baseProps}
        onOpenTicket={onOpenTicket}
        tickets={[makeTicket({ id: 'tk-42', status: TicketStatus.Todo })]}
      />,
    );
    const card = within(column('todo')).getByTestId('ticket-board-card');
    fireEvent.click(card);
    expect(onOpenTicket).toHaveBeenCalledTimes(1);
    expect(onOpenTicket).toHaveBeenCalledWith('tk-42');
  });

  it('cards are <button type=button> with a descriptive aria-label', () => {
    render(
      <TicketBoardPanel
        {...baseProps}
        tickets={[makeTicket({ id: 'tk-1', title: 'Wire it up', status: TicketStatus.InReview, assignee: 'sess-9' })]}
      />,
    );
    const card = within(column('in_review')).getByTestId('ticket-board-card');
    expect(card.tagName).toBe('BUTTON');
    expect(card.getAttribute('type')).toBe('button');
    const label = card.getAttribute('aria-label') ?? '';
    expect(label).toContain('Wire it up');
    expect(label).toContain('In review');
    expect(label).toContain('sess-9');
    expect(label).toContain('Open detail');
  });

  it('aria-label says "unassigned" when there is no assignee', () => {
    render(
      <TicketBoardPanel
        {...baseProps}
        tickets={[makeTicket({ id: 'tk-1', status: TicketStatus.Todo, assignee: '' })]}
      />,
    );
    const card = within(column('todo')).getByTestId('ticket-board-card');
    expect(card.getAttribute('aria-label')).toContain('unassigned');
  });

  it('filter "Blocked" narrows to blocked and empties the other columns (count 0)', () => {
    render(
      <TicketBoardPanel
        {...baseProps}
        tickets={[
          makeTicket({ id: 'b1', status: TicketStatus.Blocked }),
          makeTicket({ id: 'w1', status: TicketStatus.Working }),
          makeTicket({ id: 't1', status: TicketStatus.Todo }),
        ]}
      />,
    );
    fireEvent.click(screen.getByTestId('ticket-board-filter-blocked'));
    expect(within(column('blocked')).getByTestId('ticket-board-count').textContent).toBe('1');
    expect(within(column('working')).getByTestId('ticket-board-count').textContent).toBe('0');
    expect(within(column('working')).getByTestId('ticket-board-column-empty')).toBeTruthy();
    expect(within(column('todo')).getByTestId('ticket-board-count').textContent).toBe('0');
  });

  it('filter "In review" hides terminal-bad cards (no Closed sub-lane)', () => {
    render(
      <TicketBoardPanel
        {...baseProps}
        tickets={[
          makeTicket({ id: 'r1', status: TicketStatus.InReview }),
          makeTicket({ id: 'f1', status: TicketStatus.Failed }),
        ]}
      />,
    );
    fireEvent.click(screen.getByTestId('ticket-board-filter-in_review'));
    expect(within(column('in_review')).getByTestId('ticket-board-count').textContent).toBe('1');
    expect(screen.queryByTestId('ticket-board-closed-divider')).toBeNull();
  });

  it('the active filter pill carries aria-checked and only one is checked at a time', () => {
    render(<TicketBoardPanel {...baseProps} tickets={[makeTicket()]} />);
    const all = screen.getByTestId('ticket-board-filter-all');
    const blocked = screen.getByTestId('ticket-board-filter-blocked');
    expect(all.getAttribute('aria-checked')).toBe('true');
    expect(blocked.getAttribute('aria-checked')).toBe('false');
    fireEvent.click(blocked);
    expect(all.getAttribute('aria-checked')).toBe('false');
    expect(blocked.getAttribute('aria-checked')).toBe('true');
  });

  it('empty board renders the placeholder message and no columns', () => {
    render(<TicketBoardPanel {...baseProps} tickets={[]} />);
    expect(screen.getByTestId('ticket-board-empty').textContent).toContain(
      'No tickets yet — delegate work to populate the board.',
    );
    expect(screen.queryByTestId('ticket-board')).toBeNull();
  });

  it('filter matches nothing but tickets exist: shows the no-match state and "Show all" restores the board', () => {
    render(
      <TicketBoardPanel
        {...baseProps}
        tickets={[makeTicket({ id: 'open', status: TicketStatus.Working, closed_at: undefined })]}
      />,
    );
    fireEvent.click(screen.getByTestId('ticket-board-filter-closed_today'));
    expect(screen.getByTestId('ticket-board-no-match')).toBeTruthy();
    expect(screen.queryByTestId('ticket-board-empty')).toBeNull(); // distinct from truly-empty

    fireEvent.click(screen.getByTestId('ticket-board-show-all'));
    expect(screen.getByTestId('ticket-board')).toBeTruthy();
    expect(within(column('working')).getByTestId('ticket-board-count').textContent).toBe('1');
  });

  it('a column with zero matching cards keeps its header and shows a quiet placeholder', () => {
    render(<TicketBoardPanel {...baseProps} tickets={[makeTicket({ status: TicketStatus.Working })]} />);
    const todo = column('todo');
    expect(within(todo).getByText('Todo')).toBeTruthy();
    expect(within(todo).getByTestId('ticket-board-count').textContent).toBe('0');
    expect(within(todo).getByTestId('ticket-board-column-empty')).toBeTruthy();
  });

  it('mounts read-only with only the four contract props (no mutation handlers)', () => {
    const { container } = render(
      <TicketBoardPanel isOpen tickets={[makeTicket()]} onOpenTicket={() => {}} onClose={() => {}} />,
    );
    expect(container.querySelector('[data-testid="ticket-board-panel"]')).toBeTruthy();
  });

  it('isOpen=false renders nothing', () => {
    const { container } = render(
      <TicketBoardPanel isOpen={false} tickets={[makeTicket()]} onOpenTicket={() => {}} onClose={() => {}} />,
    );
    expect(container.querySelector('[data-testid="ticket-board-panel"]')).toBeNull();
  });

  it('calls onClose when the close button is clicked', () => {
    const onClose = vi.fn();
    render(<TicketBoardPanel {...baseProps} onClose={onClose} tickets={[makeTicket()]} />);
    fireEvent.click(screen.getByLabelText('Close board'));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('shows the orphan badge only on open tickets whose reconciled_at is set', () => {
    render(
      <TicketBoardPanel
        {...baseProps}
        tickets={[
          // orphaned: open + stamped
          makeTicket({ id: 'orphan', status: TicketStatus.Working, reconciled_at: '2026-06-27T10:30:00Z' }),
          // open but not stamped
          makeTicket({ id: 'owned', status: TicketStatus.Working }),
          // stamped but terminal — a closed ticket does not need an owner
          makeTicket({ id: 'closed', status: TicketStatus.Crashed, reconciled_at: '2026-06-27T10:30:00Z' }),
        ]}
      />,
    );
    const badges = screen.getAllByTestId('ticket-board-orphan-badge');
    expect(badges).toHaveLength(1);
    const cards = screen.getAllByTestId('ticket-board-card');
    const byId = (id: string) => cards.find((c) => c.getAttribute('data-ticket-id') === id)!;
    expect(byId('orphan').getAttribute('data-orphaned')).toBe('true');
    expect(byId('owned').getAttribute('data-orphaned')).toBeNull();
    expect(byId('closed').getAttribute('data-orphaned')).toBeNull();
  });
});
