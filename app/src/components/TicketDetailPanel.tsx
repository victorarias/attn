import { useEffect, useState } from 'react';
import { open } from '@tauri-apps/plugin-dialog';
import type { Ticket } from '../hooks/useDaemonSocket';
import { isTicketOrphaned } from '../utils/ticketOrphan';
import { writeClipboardText } from '../utils/clipboardBridge';
import { Markdown } from './Markdown';
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
  onHandover?: (ticketId: string, paths: string[], state?: string, comment?: string) => Promise<unknown>;
  onRenameArtifact?: (path: string, newPath: string) => Promise<unknown>;
  onDeleteArtifact?: (path: string) => Promise<unknown>;
  onOpenArtifact?: (path: string) => void;
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
            <span className="ticket-activity-move">
            {from} → {to}
          </span>
          {when && <span className="ticket-activity-when">{when}</span>}
        </div>
        {entry.comment && <Markdown className="ticket-activity-comment" breaks>{entry.comment}</Markdown>}
      </li>
    );
  }
  return (
    <li className="ticket-activity-entry" data-kind={entry.kind}>
      <div className="ticket-activity-head">
        {when && <span className="ticket-activity-when">{when}</span>}
      </div>
      {entry.comment && <Markdown className="ticket-activity-comment" breaks>{entry.comment}</Markdown>}
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
  onHandover,
  onRenameArtifact,
  onDeleteArtifact,
  onOpenArtifact,
  onResume,
  onClose,
}: TicketDetailPanelProps) {
  const [ticket, setTicket] = useState<Ticket | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Action UI state. busyAction names the in-flight action (or null). Only one
  // mutation runs at a time: every mutating control disables while any action is
  // in flight, so a sibling's settle (which clears the single slot) can never
  // re-enable or relabel another still-pending control. actionError surfaces a
  // failed mutation separately from a failed fetch.
  const [busyAction, setBusyAction] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [commentDraft, setCommentDraft] = useState('');
  const [editingDescription, setEditingDescription] = useState(false);
  const [descriptionDraft, setDescriptionDraft] = useState('');
  const [handoverFiles, setHandoverFiles] = useState<string[]>([]);
  const [handoverState, setHandoverState] = useState('');
  const [handoverComment, setHandoverComment] = useState('');
  const [renamingArtifact, setRenamingArtifact] = useState<string | null>(null);
  const [renameDraft, setRenameDraft] = useState('');

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
    setHandoverFiles([]);
    setHandoverState('');
    setHandoverComment('');
    setRenamingArtifact(null);
    setRenameDraft('');
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

  const refreshTicket = async () => {
    if (!ticketId) return;
    setTicket(await fetchTicket(ticketId));
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

  // Post the current comment draft. Shared by the form's submit button and the
  // textarea's ⌘/Ctrl+Return shortcut so both paths trim, guard against an empty
  // draft or an in-flight action, clear on success, and surface errors identically.
  const submitComment = () => {
    if (!onAddComment || !fullTicket) return;
    const text = commentDraft.trim();
    if (!text || busyAction !== null) return;
    runAction('comment', () => onAddComment(fullTicket.id, text))
      .then(() => setCommentDraft(''))
      .catch(() => {});
  };

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
                {isTicketOrphaned(header) && (
                  <span
                    className="ticket-orphan-badge"
                    data-testid="ticket-detail-orphan-badge"
                    title={`Owning session gone since ${formatTimestamp(header.reconciled_at)}`}
                  >
                    ⚠ Orphaned
                  </span>
                )}
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
                    disabled={busyAction !== null}
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
              <Markdown className="ticket-detail-description" breaks>{fullTicket.description}</Markdown>
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
                  submitComment();
                }}
              >
                <textarea
                  className="ticket-comment-input"
                  data-testid="ticket-comment-input"
                  placeholder="Add a comment… (⌘⏎ to send)"
                  value={commentDraft}
                  onChange={(event) => setCommentDraft(event.target.value)}
                  onKeyDown={(event) => {
                    // ⌘Return (mac) / Ctrl+Return (elsewhere) submits without
                    // leaving the field; plain Enter still inserts a newline.
                    if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') {
                      event.preventDefault();
                      submitComment();
                    }
                  }}
                  rows={2}
                />
                <button
                  type="submit"
                  className="ticket-comment-submit"
                  data-testid="ticket-add-comment"
                  disabled={busyAction !== null || commentDraft.trim() === ''}
                >
                  {busyAction === 'comment' ? 'Adding…' : 'Add comment'}
                </button>
              </form>
            )}
          </section>

          <section className="ticket-detail-section">
            <div className="ticket-section-head">
              <h3 className="ticket-section-label">Artifacts</h3>
              {onHandover && (
                <button
                  type="button"
                  className="ticket-section-action"
                  data-testid="ticket-choose-handover"
                  disabled={busyAction !== null}
                  onClick={() => {
                    void open({ multiple: true, filters: [{ name: 'Markdown', extensions: ['md'] }] })
                      .then((selected) => {
                        const paths = Array.isArray(selected) ? selected : selected ? [selected] : [];
                        if (paths.length > 0) setHandoverFiles(paths);
                      })
                      .catch((err) => setActionError(err instanceof Error ? err.message : 'Could not choose files'));
                  }}
                >
                  Hand over…
                </button>
              )}
            </div>
            {handoverFiles.length > 0 && onHandover && (
              <div className="ticket-handover-form" data-testid="ticket-handover-form">
                <div className="ticket-handover-files">
                  {handoverFiles.map((path) => <span key={path}>{path.split(/[\\/]/).pop()}</span>)}
                </div>
                <select
                  aria-label="Resulting ticket state"
                  className="ticket-status-select"
                  value={handoverState}
                  onChange={(event) => setHandoverState(event.target.value)}
                >
                  <option value="">Keep current state</option>
                  <option value="in_progress">Working</option>
                  <option value="needs_input">Blocked</option>
                  <option value="ready_for_review">In review</option>
                  <option value="completed">Done</option>
                  <option value="failed">Failed</option>
                </select>
                <textarea
                  className="ticket-comment-input"
                  placeholder="Decision context (optional)"
                  value={handoverComment}
                  onChange={(event) => setHandoverComment(event.target.value)}
                  rows={2}
                />
                <div className="ticket-edit-buttons">
                  <button
                    type="button"
                    className="ticket-edit-save"
                    data-testid="ticket-submit-handover"
                    disabled={busyAction !== null}
                    onClick={() => {
                      runAction('handover', async () => {
                        await onHandover(fullTicket.id, handoverFiles, handoverState || undefined, handoverComment.trim() || undefined);
                        setHandoverFiles([]);
                        setHandoverState('');
                        setHandoverComment('');
                        await refreshTicket();
                      }).catch(() => {});
                    }}
                  >
                    {busyAction === 'handover' ? 'Handing over…' : 'Hand over'}
                  </button>
                  <button type="button" className="ticket-edit-cancel" onClick={() => setHandoverFiles([])}>Cancel</button>
                </div>
              </div>
            )}
            {fullTicket.artifacts.length > 0 ? (
              <ul className="ticket-artifact-list">
                {fullTicket.artifacts.map((artifact) => {
                  const parent = artifact.notebook_path.slice(0, artifact.notebook_path.lastIndexOf('/') + 1);
                  const isRenaming = renamingArtifact === artifact.notebook_path;
                  return (
                    <li key={artifact.notebook_path} className="ticket-artifact">
                      {isRenaming ? (
                        <div className="ticket-artifact-rename">
                          <input
                            value={renameDraft}
                            aria-label={`Rename ${artifact.filename}`}
                            onChange={(event) => setRenameDraft(event.target.value)}
                          />
                          <button
                            type="button"
                            disabled={busyAction !== null || !renameDraft.trim().endsWith('.md') || renameDraft.includes('/')}
                            onClick={() => {
                              if (!onRenameArtifact) return;
                              runAction('rename-artifact', async () => {
                                await onRenameArtifact(artifact.notebook_path, parent + renameDraft.trim());
                                setRenamingArtifact(null);
                                await refreshTicket();
                              }).catch(() => {});
                            }}
                          >Save</button>
                          <button type="button" onClick={() => setRenamingArtifact(null)}>Cancel</button>
                        </div>
                      ) : (
                        <>
                          <button
                            type="button"
                            className="ticket-artifact-name"
                            title={artifact.path}
                            onClick={() => onOpenArtifact?.(artifact.notebook_path)}
                          >
                            {artifact.filename}
                          </button>
                          <div className="ticket-artifact-actions">
                            <button type="button" onClick={() => runAction('copy-artifact', () => writeClipboardText(artifact.path)).catch(() => {})}>Copy path</button>
                            {onRenameArtifact && <button type="button" onClick={() => { setRenamingArtifact(artifact.notebook_path); setRenameDraft(artifact.filename); }}>Rename</button>}
                            {onDeleteArtifact && (
                              <button
                                type="button"
                                onClick={() => {
                                  if (!window.confirm(`Delete ${artifact.filename}?`)) return;
                                  runAction('delete-artifact', async () => {
                                    await onDeleteArtifact(artifact.notebook_path);
                                    await refreshTicket();
                                  }).catch(() => {});
                                }}
                              >Delete</button>
                            )}
                          </div>
                        </>
                      )}
                    </li>
                  );
                })}
              </ul>
            ) : (
              <p className="ticket-detail-empty">No artifacts.</p>
            )}
          </section>
        </div>
      )}
    </div>
  );
}
