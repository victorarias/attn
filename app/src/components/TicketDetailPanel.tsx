import { useEffect, useState } from 'react';
import type { Ticket } from '../hooks/useDaemonSocket';
import './TicketDetailPanel.css';

interface TicketDetailPanelProps {
  isOpen: boolean;
  ticketId: string | null;
  // The bare board row for this ticket (from the store). It gives an instant
  // header while the full record loads, and its updated_at drives a live
  // re-fetch when the ticket changes under an open panel.
  ticketRow?: Ticket;
  fetchTicket: (ticketId: string) => Promise<Ticket>;
  onClose: () => void;
}

const STATUS_LABELS: Record<string, string> = {
  todo: 'Todo',
  working: 'Working',
  blocked: 'Blocked',
  in_review: 'In review',
  done: 'Done',
  failed: 'Failed',
  crashed: 'Crashed',
};

function statusLabel(status: string): string {
  return STATUS_LABELS[status] ?? status;
}

function formatTimestamp(iso: string | undefined): string {
  if (!iso) return '';
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  return date.toLocaleString();
}

// A single history entry: a status change (column move, optional note) or a
// freeform comment.
function ActivityEntry({ entry }: { entry: Ticket['activity'][number] }) {
  const when = formatTimestamp(entry.created_at);
  if (entry.kind === 'status_change') {
    const from = entry.from_status ? statusLabel(entry.from_status) : '—';
    const to = entry.to_status ? statusLabel(entry.to_status) : '—';
    return (
      <li className="ticket-activity-entry" data-kind="status_change">
        <div className="ticket-activity-head">
          <span className="ticket-activity-author">{entry.author}</span>
          <span className="ticket-activity-move">
            {from} → {to}
          </span>
          {when && <span className="ticket-activity-when">{when}</span>}
        </div>
        {entry.comment && <div className="ticket-activity-comment">{entry.comment}</div>}
      </li>
    );
  }
  return (
    <li className="ticket-activity-entry" data-kind="comment">
      <div className="ticket-activity-head">
        <span className="ticket-activity-author">{entry.author}</span>
        {when && <span className="ticket-activity-when">{when}</span>}
      </div>
      {entry.comment && <div className="ticket-activity-comment">{entry.comment}</div>}
    </li>
  );
}

export function TicketDetailPanel({ isOpen, ticketId, ticketRow, fetchTicket, onClose }: TicketDetailPanelProps) {
  const [ticket, setTicket] = useState<Ticket | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // updated_at moves on every mutation; using it as a dep makes the open panel
  // re-fetch the full record live when the ticket changes (a status move, a new
  // comment), without polling.
  const refreshKey = ticketRow?.updated_at ?? '';

  useEffect(() => {
    if (!isOpen || !ticketId) {
      return;
    }
    let ignore = false;
    setLoading(true);
    setError(null);
    fetchTicket(ticketId)
      .then((full) => {
        if (ignore) return;
        setTicket(full);
        setLoading(false);
      })
      .catch((err) => {
        if (ignore) return;
        setError(err instanceof Error ? err.message : 'Could not load ticket');
        setLoading(false);
      });
    return () => {
      // A newer fetch (id change / live refresh) supersedes this one, so its
      // late result must not overwrite fresher state.
      ignore = true;
    };
  }, [isOpen, ticketId, refreshKey, fetchTicket]);

  if (!isOpen) {
    return null;
  }

  // Only trust the fetched record when it is for the ticket currently open; on an
  // id change the stale full record is ignored and the header falls back to the
  // bare row until the new fetch lands.
  const fullTicket = ticket && ticket.id === ticketId ? ticket : null;
  const header = fullTicket ?? ticketRow ?? null;

  return (
    <div className="ticket-detail-panel" data-testid="ticket-detail-panel">
      <div className="ticket-detail-header">
        <div className="ticket-detail-titles">
          {header ? (
            <>
              <h2 className="ticket-detail-title">{header.title}</h2>
              <div className="ticket-detail-meta">
                <span className={`ticket-status-badge ticket-status-${header.status}`}>
                  {statusLabel(header.status)}
                </span>
                <span className="ticket-detail-id">{header.id}</span>
                {header.assignee && (
                  <span className="ticket-detail-assignee">assignee: {header.assignee}</span>
                )}
              </div>
            </>
          ) : (
            <h2 className="ticket-detail-title">Ticket</h2>
          )}
        </div>
        <button type="button" className="ticket-detail-close" onClick={onClose} aria-label="Close ticket detail">
          ✕
        </button>
      </div>

      {error && <div className="ticket-detail-error" role="alert">{error}</div>}
      {loading && !fullTicket && <div className="ticket-detail-loading">Loading…</div>}

      {fullTicket && (
        <div className="ticket-detail-body">
          <section className="ticket-detail-section">
            <h3 className="ticket-section-label">Description</h3>
            {fullTicket.description ? (
              <p className="ticket-detail-description">{fullTicket.description}</p>
            ) : (
              <p className="ticket-detail-empty">No description.</p>
            )}
          </section>

          <section className="ticket-detail-section">
            <h3 className="ticket-section-label">History</h3>
            {fullTicket.activity.length > 0 ? (
              <ul className="ticket-activity-list">
                {fullTicket.activity.map((entry) => (
                  <ActivityEntry key={entry.id} entry={entry} />
                ))}
              </ul>
            ) : (
              <p className="ticket-detail-empty">No activity yet.</p>
            )}
          </section>

          <section className="ticket-detail-section">
            <h3 className="ticket-section-label">Attachments</h3>
            {fullTicket.attachments.length > 0 ? (
              <ul className="ticket-attachment-list">
                {fullTicket.attachments.map((att) => (
                  <li key={att.id} className="ticket-attachment">
                    <span className="ticket-attachment-name">{att.filename}</span>
                    {att.note && <span className="ticket-attachment-note">{att.note}</span>}
                  </li>
                ))}
              </ul>
            ) : (
              <p className="ticket-detail-empty">No attachments.</p>
            )}
          </section>
        </div>
      )}
    </div>
  );
}
