import { useMemo, useState } from 'react';
import type { Ticket } from '../hooks/useDaemonSocket';
import { TicketStatus } from '../types/generated';
import { isTicketOrphaned } from '../utils/ticketOrphan';
import './TicketBoardPanel.css';

export type BoardFilter = 'all' | 'blocked' | 'in_review' | 'closed_today';

type TicketStatusValue = Ticket['status'];

/** Statuses that own a flow column (terminal-bad statuses do not — they fold into Done). */
type FlowStatus =
  | TicketStatus.Todo
  | TicketStatus.Working
  | TicketStatus.Blocked
  | TicketStatus.InReview
  | TicketStatus.Done;

/**
 * Single source of truth for the flow columns and their order. Reordering or
 * relabeling a column is a one-line edit here. Terminal-bad statuses (failed,
 * crashed) deliberately have NO column — they fold into the Done column as a
 * "Closed" sub-lane (see groupTicketsByColumn / the Done render branch).
 */
export const STATUS_COLUMNS: ReadonlyArray<{
  status: FlowStatus;
  label: string;
  empty: string;
}> = [
  { status: TicketStatus.Todo, label: 'Todo', empty: 'Nothing here' },
  { status: TicketStatus.Working, label: 'Working', empty: 'Nothing here' },
  { status: TicketStatus.Blocked, label: 'Blocked', empty: 'None blocked' },
  { status: TicketStatus.InReview, label: 'In review', empty: 'Nothing in review' },
  { status: TicketStatus.Done, label: 'Done', empty: 'Nothing here' },
];

/** Statuses that fold into the Done column's CLOSED sub-lane. */
export const TERMINAL_BAD_STATUSES: ReadonlyArray<TicketStatusValue> = [
  TicketStatus.Failed,
  TicketStatus.Crashed,
];

export const STATUS_LABEL: Record<TicketStatusValue, string> = {
  [TicketStatus.Todo]: 'Todo',
  [TicketStatus.Working]: 'Working',
  [TicketStatus.Blocked]: 'Blocked',
  [TicketStatus.InReview]: 'In review',
  [TicketStatus.Done]: 'Done',
  [TicketStatus.Failed]: 'Failed',
  [TicketStatus.Crashed]: 'Crashed',
};

export const FILTERS: ReadonlyArray<{ id: BoardFilter; label: string }> = [
  { id: 'all', label: 'All' },
  { id: 'blocked', label: 'Blocked' },
  { id: 'in_review', label: 'In review' },
  { id: 'closed_today', label: 'Closed today' },
];

/** True when `iso` falls on the same local calendar day as `now`. */
export function isSameLocalDay(iso: string | undefined, now: Date = new Date()): boolean {
  if (!iso) return false;
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return false;
  return (
    d.getFullYear() === now.getFullYear() &&
    d.getMonth() === now.getMonth() &&
    d.getDate() === now.getDate()
  );
}

/**
 * Compact, deterministic relative time. `now` is injectable for tests.
 * now/Ns/Nm/Nh/yesterday/Nd.
 */
export function relativeTime(iso: string, now: Date = new Date()): string {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return '';
  const seconds = Math.max(0, (now.getTime() - t) / 1000);
  if (seconds < 60) return `${Math.floor(seconds) || 1}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h`;
  if (seconds < 172800) return 'yesterday';
  return `${Math.floor(seconds / 86400)}d`;
}

/** Narrow the ticket set per the active filter BEFORE bucketing, so per-column counts reflect what is shown. */
export function applyFilter(tickets: Ticket[], filter: BoardFilter, now: Date = new Date()): Ticket[] {
  return (tickets ?? []).filter((t) => {
    if (filter === 'blocked') return t.status === 'blocked';
    if (filter === 'in_review') return t.status === 'in_review';
    if (filter === 'closed_today') return isSameLocalDay(t.closed_at, now);
    return true;
  });
}

export interface BoardColumnData {
  status: (typeof STATUS_COLUMNS)[number]['status'];
  label: string;
  empty: string;
  /** Cards for this column's own status, preserving incoming order (created_at desc). */
  cards: Ticket[];
  /** Only populated for the Done column: terminal-bad tickets in the CLOSED sub-lane. */
  terminalBad: Ticket[];
}

/**
 * Bucket already-filtered tickets into the flow columns. Terminal-bad tickets
 * are attached to the Done column's `terminalBad` lane rather than getting a
 * column of their own. Pure and side-effect free.
 */
export function groupTicketsByColumn(visible: Ticket[]): BoardColumnData[] {
  const terminalBad = visible.filter((t) => TERMINAL_BAD_STATUSES.includes(t.status));
  return STATUS_COLUMNS.map((col) => ({
    status: col.status,
    label: col.label,
    empty: col.empty,
    cards: visible.filter((t) => t.status === col.status),
    terminalBad: col.status === 'done' ? terminalBad : [],
  }));
}

export interface TicketBoardPanelProps {
  isOpen: boolean;
  /** useDaemonStore().tickets — non-archived, created_at desc. Read-only. */
  tickets: Ticket[];
  /** App.tsx: setSelectedTicketId(id) + openDockPanel('ticketDetail'). */
  onOpenTicket: (ticketId: string) => void;
  /** closeDockPanel('board'). */
  onClose: () => void;
}

export function TicketBoardPanel({ isOpen, tickets = [], onOpenTicket, onClose }: TicketBoardPanelProps) {
  const [filter, setFilter] = useState<BoardFilter>('all');

  const visible = useMemo(() => applyFilter(tickets, filter), [tickets, filter]);
  const columns = useMemo(() => groupTicketsByColumn(visible), [visible]);

  if (!isOpen) return null;

  const boardEmpty = tickets.length === 0; // truly empty, filter-independent
  const noMatch = !boardEmpty && visible.length === 0; // filter matched nothing

  return (
    <div className="tb-panel" data-testid="ticket-board-panel">
      <div className="tb-header">
        <h2 className="tb-title">Board</h2>
        <button
          type="button"
          className="ticket-detail-close"
          onClick={onClose}
          aria-label="Close board"
        >
          ✕
        </button>
      </div>

      <div className="tb-filters" role="radiogroup" aria-label="Filter tickets">
        {FILTERS.map((f) => (
          <button
            key={f.id}
            type="button"
            role="radio"
            aria-checked={filter === f.id}
            className="tb-filter"
            data-testid={`ticket-board-filter-${f.id}`}
            onClick={() => setFilter(f.id)}
          >
            {f.label}
          </button>
        ))}
      </div>

      {boardEmpty ? (
        <div className="tb-board-empty" data-testid="ticket-board-empty">
          No tickets yet — delegate work to populate the board.
        </div>
      ) : noMatch ? (
        <div className="tb-board-empty" data-testid="ticket-board-no-match">
          No tickets match this filter.
          <button
            type="button"
            className="tb-show-all"
            data-testid="ticket-board-show-all"
            onClick={() => setFilter('all')}
          >
            Show all
          </button>
        </div>
      ) : (
        <div className="tb-board" data-testid="ticket-board">
          {columns.map((col, colI) => {
            const badCount = col.terminalBad.length;
            const headCount =
              col.status === 'done' && badCount > 0
                ? `${col.cards.length} · ${badCount}`
                : `${col.cards.length}`;
            const showColEmpty = col.cards.length === 0 && badCount === 0;
            return (
              <section
                key={col.status}
                className="tb-column"
                data-testid="ticket-board-column"
                data-status={col.status}
                style={{ ['--col-i' as string]: colI }}
                aria-label={`${col.label} (${col.cards.length + badCount})`}
              >
                <header className="tb-col-head">
                  <span className="tb-col-label">{col.label}</span>
                  <span className="tb-count" data-testid="ticket-board-count">
                    {headCount}
                  </span>
                </header>
                <div className="tb-col-body">
                  {showColEmpty ? (
                    <p className="tb-col-empty" data-testid="ticket-board-column-empty">
                      {col.empty}
                    </p>
                  ) : (
                    <>
                      {col.cards.map((t, i) => (
                        <BoardCard key={t.id} ticket={t} index={i} onOpen={onOpenTicket} />
                      ))}
                      {col.status === 'done' && badCount > 0 && (
                        <>
                          <div className="tb-closed-divider" data-testid="ticket-board-closed-divider">
                            Closed
                          </div>
                          {col.terminalBad.map((t, i) => (
                            <BoardCard
                              key={t.id}
                              ticket={t}
                              index={col.cards.length + i}
                              onOpen={onOpenTicket}
                              terminalBad
                            />
                          ))}
                        </>
                      )}
                    </>
                  )}
                </div>
              </section>
            );
          })}
        </div>
      )}
    </div>
  );
}

interface BoardCardProps {
  ticket: Ticket;
  index: number;
  onOpen: (ticketId: string) => void;
  terminalBad?: boolean;
}

function BoardCard({ ticket, index, onOpen, terminalBad }: BoardCardProps) {
  const when = relativeTime(ticket.updated_at);
  const active = ticket.status === 'working' || ticket.status === 'blocked';
  const closedToday = isSameLocalDay(ticket.closed_at);
  const orphaned = isTicketOrphaned(ticket);
  return (
    <button
      type="button"
      className="tb-card"
      data-testid="ticket-board-card"
      data-ticket-id={ticket.id}
      data-status={ticket.status}
      data-terminal={terminalBad ? 'bad' : undefined}
      data-active={active ? 'true' : undefined}
      data-closed-today={closedToday ? 'true' : undefined}
      data-orphaned={orphaned ? 'true' : undefined}
      style={{ ['--card-i' as string]: Math.min(index, 7) }}
      onClick={() => onOpen(ticket.id)}
      aria-label={`${ticket.title}, ${STATUS_LABEL[ticket.status] ?? ticket.status}${
        orphaned ? ', orphaned' : ''
      }, ${ticket.assignee || 'unassigned'}, updated ${when}. Open detail.`}
    >
      {active && <span className="tb-pulse" aria-hidden="true" />}
      {orphaned && (
        <span
          className="tb-orphan"
          data-testid="ticket-board-orphan-badge"
          title={`Owning session gone since ${new Date(ticket.reconciled_at!).toLocaleString()}`}
        >
          ⚠ orphaned
        </span>
      )}
      <span className="tb-card-title">{ticket.title}</span>
      <span className="tb-card-meta">
        <span className="tb-dot" aria-hidden="true" />
        <span className="tb-card-id">{ticket.id}</span>
        <span className="tb-when" title={new Date(ticket.updated_at).toLocaleString()}>
          {when}
        </span>
      </span>
      {ticket.assignee && <span className="tb-card-assignee">@{ticket.assignee}</span>}
    </button>
  );
}
