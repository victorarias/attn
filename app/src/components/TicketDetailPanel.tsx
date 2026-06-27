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
  // Chief/user actions (slice 4c). Optional so the panel can render read-only
  // when an owner does not wire them. Each resolves once the daemon confirms;
  // the refreshed record arrives via the live re-fetch, not the resolve value.
  onChangeStatus?: (ticketId: string, status: Ticket['status'], comment?: string) => Promise<void>;
  onAddComment?: (ticketId: string, comment: string) => Promise<void>;
  onEditDescription?: (ticketId: string, description: string) => Promise<void>;
  // Reopen the ticket's agent session (in its stored cwd, resuming the prior
  // conversation). Optional; shown only when the ticket has a resumable agent.
  onResume?: (ticketId: string) => void;
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

// Statuses a human can move a ticket into from the board. `crashed` is excluded
// because it is an attn-authored signal, not a manual destination; the open
// ticket's own status is always added so the select can show it.
const SELECTABLE_STATUSES = ['todo', 'working', 'blocked', 'in_review', 'done', 'failed'];

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

export function TicketDetailPanel({
  isOpen,
  ticketId,
  ticketRow,
  fetchTicket,
  onChangeStatus,
  onAddComment,
  onEditDescription,
  onResume,
  onClose,
}: TicketDetailPanelProps) {
  const [ticket, setTicket] = useState<Ticket | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Action UI state. busyAction names the in-flight action (or null) so its
  // control can disable and the others can stay live; actionError surfaces a
  // failed mutation separately from a failed fetch.
  const [busyAction, setBusyAction] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [commentDraft, setCommentDraft] = useState('');
  const [editingDescription, setEditingDescription] = useState(false);
  const [descriptionDraft, setDescriptionDraft] = useState('');

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

  // Switching tickets clears any in-progress edit so a draft never bleeds from
  // one ticket onto another.
  useEffect(() => {
    setActionError(null);
    setCommentDraft('');
    setEditingDescription(false);
    setDescriptionDraft('');
  }, [ticketId]);

  // runAction wraps a mutation in the shared busy/error handling. It rethrows so
  // a caller can chain a success-only side effect (clearing a draft) without
  // duplicating the error path.
  const runAction = (name: string, fn: () => Promise<void>): Promise<void> => {
    setBusyAction(name);
    setActionError(null);
    return fn()
      .catch((err) => {
        setActionError(err instanceof Error ? err.message : 'Action failed');
        throw err;
      })
      .finally(() => setBusyAction(null));
  };

  if (!isOpen) {
    return null;
  }

  // Only trust the fetched record when it is for the ticket currently open; on an
  // id change the stale full record is ignored and the header falls back to the
  // bare row until the new fetch lands.
  const fullTicket = ticket && ticket.id === ticketId ? ticket : null;
  const header = fullTicket ?? ticketRow ?? null;
  // A ticket is resumable when it carries a bound agent session — a stored cwd and
  // agent id, both set only for delegated work. Works off the bare row too, so the
  // button is live before the full record loads.
  const canResume = Boolean(onResume && ticketId && header?.cwd && header?.last_agent_id);

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
        <div className="ticket-detail-header-actions">
          {canResume && (
            <button
              type="button"
              className="ticket-detail-resume"
              data-testid="ticket-resume"
              onClick={() => ticketId && onResume?.(ticketId)}
            >
              ↻ Resume
            </button>
          )}
          <button type="button" className="ticket-detail-close" onClick={onClose} aria-label="Close ticket detail">
            ✕
          </button>
        </div>
      </div>

      {error && <div className="ticket-detail-error" role="alert">{error}</div>}
      {loading && !fullTicket && <div className="ticket-detail-loading">Loading…</div>}

      {fullTicket && (
        <div className="ticket-detail-body">
          {onChangeStatus && (
            <div className="ticket-action-row">
              <label className="ticket-action-label" htmlFor="ticket-status-select">
                Status
              </label>
              <select
                id="ticket-status-select"
                data-testid="ticket-status-select"
                className="ticket-status-select"
                value={fullTicket.status}
                disabled={busyAction !== null}
                onChange={(event) => {
                  const next = event.target.value as Ticket['status'];
                  if (next === fullTicket.status) return;
                  runAction('status', () => onChangeStatus(fullTicket.id, next)).catch(() => {});
                }}
              >
                {(SELECTABLE_STATUSES.includes(fullTicket.status)
                  ? SELECTABLE_STATUSES
                  : [fullTicket.status, ...SELECTABLE_STATUSES]
                ).map((status) => (
                  <option key={status} value={status}>
                    {statusLabel(status)}
                  </option>
                ))}
              </select>
            </div>
          )}

          {actionError && (
            <div className="ticket-action-error" role="alert">
              {actionError}
            </div>
          )}

          <section className="ticket-detail-section">
            <div className="ticket-section-head">
              <h3 className="ticket-section-label">Description</h3>
              {onEditDescription && !editingDescription && (
                <button
                  type="button"
                  className="ticket-section-action"
                  data-testid="ticket-edit-description"
                  onClick={() => {
                    setDescriptionDraft(fullTicket.description);
                    setEditingDescription(true);
                  }}
                >
                  Edit
                </button>
              )}
            </div>
            {editingDescription ? (
              <div className="ticket-edit-block">
                <textarea
                  className="ticket-edit-textarea"
                  data-testid="ticket-description-input"
                  value={descriptionDraft}
                  onChange={(event) => setDescriptionDraft(event.target.value)}
                  rows={4}
                />
                <div className="ticket-edit-buttons">
                  <button
                    type="button"
                    className="ticket-edit-save"
                    data-testid="ticket-save-description"
                    disabled={busyAction === 'description'}
                    onClick={() => {
                      if (!onEditDescription) return;
                      runAction('description', () => onEditDescription(fullTicket.id, descriptionDraft))
                        .then(() => setEditingDescription(false))
                        .catch(() => {});
                    }}
                  >
                    {busyAction === 'description' ? 'Saving…' : 'Save'}
                  </button>
                  <button
                    type="button"
                    className="ticket-edit-cancel"
                    onClick={() => setEditingDescription(false)}
                  >
                    Cancel
                  </button>
                </div>
              </div>
            ) : fullTicket.description ? (
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
            {onAddComment && (
              <form
                className="ticket-comment-form"
                onSubmit={(event) => {
                  event.preventDefault();
                  const text = commentDraft.trim();
                  if (!text) return;
                  runAction('comment', () => onAddComment(fullTicket.id, text))
                    .then(() => setCommentDraft(''))
                    .catch(() => {});
                }}
              >
                <textarea
                  className="ticket-comment-input"
                  data-testid="ticket-comment-input"
                  placeholder="Add a comment…"
                  value={commentDraft}
                  onChange={(event) => setCommentDraft(event.target.value)}
                  rows={2}
                />
                <button
                  type="submit"
                  className="ticket-comment-submit"
                  data-testid="ticket-add-comment"
                  disabled={busyAction === 'comment' || commentDraft.trim() === ''}
                >
                  {busyAction === 'comment' ? 'Adding…' : 'Add comment'}
                </button>
              </form>
            )}
          </section>

          <section className="ticket-detail-section">
            <h3 className="ticket-section-label">Attachments</h3>
            {fullTicket.attachments.length > 0 ? (
              <ul className="ticket-attachment-list">
                {fullTicket.attachments.map((att) => (
                  <li key={att.id} className="ticket-attachment">
                    <span className="ticket-attachment-name" title={att.path || undefined}>
                      {att.filename}
                    </span>
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
