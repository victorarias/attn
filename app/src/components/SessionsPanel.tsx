import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import FocusTrap from 'focus-trap-react';
import type { SessionLedgerEntry } from '../types/generated';
import type { SessionLedgerPage, SessionLedgerQuery } from '../hooks/daemonSessionLedgerEvents';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { useSessionLedger } from '../hooks/useSessionLedger';
import type { SessionLedgerFilters } from '../hooks/useSessionLedger';
import {
  SESSION_RANGE_CHOICES,
  closedBySomeone,
  isClosed,
  ledgerInstant,
  ledgerState,
  shortPath,
} from './sessionsLedger';
import type { SessionRangeId, SessionScope } from './sessionsLedger';
import './SessionsPanel.css';

/** One thing the surface can do to a closed session. The ids are the reopen
 * actions the daemon offers; the surface never invents one. */
export interface ReopenActionView {
  id: string;
  label: string;
}

export interface ReopenVerdictView {
  /** A background git check is still refining this verdict. */
  refreshing: boolean;
  summary: string;
  reopenable: boolean;
  actions: ReopenActionView[];
}

export interface SessionSeedLink {
  id: string;
  title: string;
}

export interface SessionsPanelProps {
  isOpen: boolean;
  onClose: () => void;
  listSessions: (query: SessionLedgerQuery) => Promise<SessionLedgerPage>;
  /** Display names for workspace ids; an id with no name shows as itself. */
  workspaceNames?: Record<string, string>;
  liveSessionIds?: Set<string>;
  seedForSession?: (sessionId: string) => SessionSeedLink | null;
  onFocusSession?: (sessionId: string) => void;
  onOpenSeed?: (seedId: string) => void;
  /** Absent until a verdict has been asked for; refreshing while git runs. */
  verdicts?: Record<string, ReopenVerdictView>;
  onRequestVerdict?: (sessionId: string) => void;
  onReopen?: (sessionId: string, actionId: string) => void;
  /** The freshest `session_closed` the socket delivered; the nonce makes a
   * repeat close of the same session a new notice. */
  closeNotice?: { entry: SessionLedgerEntry; nonce: number };
  now?: () => Date;
}

const SCOPES: { id: SessionScope; label: string }[] = [
  { id: 'live', label: 'Live' },
  { id: 'closed', label: 'Closed' },
  { id: 'all', label: 'All' },
];

export function SessionsPanel({
  isOpen,
  onClose,
  listSessions,
  workspaceNames = {},
  liveSessionIds,
  seedForSession,
  onFocusSession,
  onOpenSeed,
  verdicts = {},
  onRequestVerdict,
  onReopen,
  closeNotice,
  now = () => new Date(),
}: SessionsPanelProps) {
  const ledger = useSessionLedger({ enabled: isOpen, list: listSessions, now });
  const { filters, setFilters, entries } = ledger;
  const [selectedId, setSelectedId] = useState<string | null>(null);
  // An action taken while a git check was still running: it fires against the
  // verdict that lands, never against the stale one that was on screen.
  const [awaiting, setAwaiting] = useState<{ sessionId: string; actionId: string } | null>(null);
  const [awaitingRefusal, setAwaitingRefusal] = useState<string | null>(null);
  const rowsRef = useRef<HTMLTableSectionElement>(null);

  useEscapeStack(onClose, isOpen);

  const { recordClose } = ledger;
  useEffect(() => {
    if (!isOpen || !closeNotice) return;
    recordClose(closeNotice.entry);
  }, [isOpen, closeNotice, recordClose]);

  const selected = entries.find((entry) => entry.id === selectedId) ?? entries[0] ?? null;

  useEffect(() => {
    if (!isOpen) {
      setSelectedId(null);
      setAwaiting(null);
      setAwaitingRefusal(null);
    }
  }, [isOpen]);

  // Verdicts are asked for only for the closed rows actually on screen, so a
  // ledger of thousands never starts thousands of git checks.
  useEffect(() => {
    if (!isOpen || !onRequestVerdict) return;
    for (const entry of entries) {
      if (isClosed(entry) && !verdicts[entry.id]) onRequestVerdict(entry.id);
    }
  }, [isOpen, entries, verdicts, onRequestVerdict]);

  useEffect(() => {
    if (!awaiting) return;
    const verdict = verdicts[awaiting.sessionId];
    if (!verdict || verdict.refreshing) return;
    const stillOffered = verdict.actions.some((action) => action.id === awaiting.actionId);
    if (stillOffered) {
      onReopen?.(awaiting.sessionId, awaiting.actionId);
    } else {
      setAwaitingRefusal(`The check finished and that is no longer possible: ${verdict.summary}`);
    }
    setAwaiting(null);
  }, [awaiting, verdicts, onReopen]);

  const runAction = useCallback((entry: SessionLedgerEntry, actionId: string) => {
    setAwaitingRefusal(null);
    const verdict = verdicts[entry.id];
    if (verdict && !verdict.refreshing) {
      onReopen?.(entry.id, actionId);
      return;
    }
    setAwaiting({ sessionId: entry.id, actionId });
  }, [verdicts, onReopen]);

  const moveSelection = useCallback((offset: number) => {
    if (entries.length === 0) return;
    const current = Math.max(0, entries.findIndex((entry) => entry.id === selected?.id));
    const next = Math.min(entries.length - 1, Math.max(0, current + offset));
    setSelectedId(entries[next].id);
    rowsRef.current?.querySelectorAll<HTMLTableRowElement>('tr')[next]?.focus();
  }, [entries, selected]);

  const update = useCallback((patch: Partial<SessionLedgerFilters>) => {
    setFilters({ ...filters, ...patch });
  }, [filters, setFilters]);

  const repositories = ledger.facets?.repositories ?? [];
  const workspaces = ledger.facets?.workspaces ?? [];
  const workspaceLabel = useCallback(
    (id: string) => workspaceNames[id] ?? id,
    [workspaceNames],
  );

  const isLive = useCallback(
    (entry: SessionLedgerEntry) => !isClosed(entry) && (liveSessionIds?.has(entry.id) ?? true),
    [liveSessionIds],
  );

  const body = useMemo(() => {
    if (ledger.filterError) {
      return <p className="sessions-state sessions-state-error">{ledger.filterError}</p>;
    }
    if (ledger.loading && entries.length === 0) {
      return <p className="sessions-state">Reading the ledger…</p>;
    }
    if (ledger.error) {
      return (
        <p className="sessions-state sessions-state-error">
          {ledger.error}
          <button type="button" onClick={ledger.reload}>Try again</button>
        </p>
      );
    }
    if (entries.length === 0) {
      return <p className="sessions-state">{emptyMessage(filters)}</p>;
    }
    return null;
  }, [ledger, entries.length, filters]);

  if (!isOpen) return null;

  return (
    <div className="sessions-shell">
      <FocusTrap focusTrapOptions={{ escapeDeactivates: false }}>
        <div className="sessions-panel" role="dialog" aria-modal="true" aria-labelledby="sessions-title">
          <header className="sessions-header">
            <h1 id="sessions-title">Sessions</h1>
            <div className="sessions-filters">
              <div className="sessions-scope" role="group" aria-label="Which sessions">
                {SCOPES.map((scope) => (
                  <button
                    key={scope.id}
                    type="button"
                    className={filters.scope === scope.id ? 'is-selected' : undefined}
                    aria-pressed={filters.scope === scope.id}
                    onClick={() => update({ scope: scope.id })}
                  >
                    {scope.label}
                  </button>
                ))}
              </div>

              <label className="sessions-filter">
                <span>When</span>
                <select
                  value={filters.range}
                  onChange={(event) => update({ range: event.target.value as SessionRangeId })}
                >
                  {SESSION_RANGE_CHOICES.map((choice) => (
                    <option key={choice.id} value={choice.id}>{choice.label}</option>
                  ))}
                </select>
              </label>

              {filters.range === 'custom' && (
                <div className="sessions-custom-range">
                  <label>
                    <span>From</span>
                    <input
                      type="date"
                      value={filters.customFrom}
                      onChange={(event) => update({ customFrom: event.target.value })}
                    />
                  </label>
                  <label>
                    <span>To</span>
                    <input
                      type="date"
                      value={filters.customTo}
                      onChange={(event) => update({ customTo: event.target.value })}
                    />
                  </label>
                </div>
              )}

              <label className="sessions-filter">
                <span>Workspace</span>
                <select
                  value={filters.workspaceId}
                  onChange={(event) => update({ workspaceId: event.target.value })}
                >
                  <option value="">Every workspace</option>
                  {workspaces.map((facet) => (
                    <option key={facet.value} value={facet.value}>
                      {workspaceLabel(facet.value)} ({facet.count})
                    </option>
                  ))}
                </select>
              </label>

              <label className="sessions-filter">
                <span>Repository</span>
                <select
                  value={filters.repository}
                  onChange={(event) => update({ repository: event.target.value })}
                >
                  <option value="">Every repository</option>
                  {repositories.map((facet) => (
                    <option key={facet.value} value={facet.value}>
                      {shortPath(facet.value, 1)} ({facet.count})
                    </option>
                  ))}
                </select>
              </label>
            </div>
            <button type="button" className="sessions-close" onClick={onClose}>
              <span>Close</span><kbd>esc</kbd>
            </button>
          </header>

          {awaitingRefusal && (
            <p className="sessions-state sessions-state-error" role="status">{awaitingRefusal}</p>
          )}

          <div className="sessions-body">
            {body}
            {entries.length > 0 && (
              <table className="sessions-table">
                <thead>
                  <tr>
                    <th scope="col">Session</th>
                    <th scope="col">Agent</th>
                    <th scope="col">State</th>
                    <th scope="col">Workspace</th>
                    <th scope="col">Where</th>
                    <th scope="col">Seed</th>
                    <th scope="col">When</th>
                    <th scope="col">Reopen</th>
                    <th scope="col"><span className="sessions-visually-hidden">Actions</span></th>
                  </tr>
                </thead>
                <tbody ref={rowsRef}>
                  {entries.map((entry) => {
                    const seed = seedForSession?.(entry.id) ?? null;
                    const verdict = isClosed(entry) ? verdicts[entry.id] : undefined;
                    const waiting = awaiting?.sessionId === entry.id;
                    return (
                      <tr
                        key={entry.id}
                        tabIndex={0}
                        aria-selected={entry.id === selected?.id}
                        className={entry.id === selected?.id ? 'is-selected' : undefined}
                        onFocus={() => setSelectedId(entry.id)}
                        onKeyDown={(event) => {
                          if (event.key === 'ArrowDown') {
                            event.preventDefault();
                            moveSelection(1);
                          } else if (event.key === 'ArrowUp') {
                            event.preventDefault();
                            moveSelection(-1);
                          } else if (event.key === 'Enter') {
                            event.preventDefault();
                            if (isLive(entry)) onFocusSession?.(entry.id);
                            else if (verdict?.actions[0]) runAction(entry, verdict.actions[0].id);
                          }
                        }}
                      >
                        <td>
                          <span className="sessions-label">{entry.label || entry.id}</span>
                          <span className="sessions-id">{entry.id}</span>
                        </td>
                        <td>{entry.agent}</td>
                        <td><span className={`sessions-state-chip is-${ledgerState(entry)}`}>{ledgerState(entry)}</span></td>
                        <td>{workspaceLabel(entry.workspace_id) || '—'}</td>
                        <td>
                          <span title={entry.directory}>{shortPath(entry.directory)}</span>
                          {entry.branch && <span className="sessions-branch">{entry.branch}</span>}
                        </td>
                        <td>
                          {seed
                            ? <button type="button" className="sessions-link" onClick={() => onOpenSeed?.(seed.id)}>{seed.title}</button>
                            : '—'}
                        </td>
                        <td>
                          <span title={ledgerInstant(entry)}>{shortStamp(ledgerInstant(entry))}</span>
                          {isClosed(entry) && (
                            <span className="sessions-closed-by">
                              closed by {closedBySomeone(entry)}
                              {entry.close_reason ? `: ${entry.close_reason}` : ''}
                            </span>
                          )}
                        </td>
                        <td>{renderVerdict(verdict, isClosed(entry), waiting, !!onRequestVerdict)}</td>
                        <td className="sessions-actions">
                          {isLive(entry) && (
                            <button type="button" onClick={() => onFocusSession?.(entry.id)}>Focus</button>
                          )}
                          {isClosed(entry) && verdict?.actions.map((action) => (
                            <button
                              key={action.id}
                              type="button"
                              onClick={() => runAction(entry, action.id)}
                            >
                              {action.label}
                            </button>
                          ))}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            )}
          </div>

          <footer className="sessions-footer">
            <span>
              {entries.length === 0
                ? 'Nothing to show'
                : `showing ${entries.length}${ledger.omitted > 0 ? `, ${ledger.omitted} older` : ''}`}
            </span>
            {ledger.omitted > 0 && (
              <button type="button" onClick={ledger.loadMore} disabled={ledger.loadingMore}>
                {ledger.loadingMore ? 'Loading…' : 'Load more'}
              </button>
            )}
          </footer>
        </div>
      </FocusTrap>
    </div>
  );
}

function renderVerdict(
  verdict: ReopenVerdictView | undefined,
  closed: boolean,
  waiting: boolean,
  checksAvailable: boolean,
): React.ReactNode {
  if (!closed) return <span className="sessions-verdict-none">—</span>;
  // Nothing is checking, so saying "checking…" would be a lie the row never resolves.
  if (!checksAvailable) return <span className="sessions-verdict-none">—</span>;
  if (!verdict) return <span className="sessions-verdict-refreshing">checking…</span>;
  if (verdict.refreshing) {
    return (
      <span className="sessions-verdict-refreshing" title={verdict.summary}>
        {verdict.summary} <em>refreshing…</em>
        {waiting && <em>waiting for the check…</em>}
      </span>
    );
  }
  return (
    <span className={verdict.reopenable ? 'sessions-verdict-ok' : 'sessions-verdict-no'}>
      {verdict.summary}
      {waiting && <em> waiting for the check…</em>}
    </span>
  );
}

function emptyMessage(filters: SessionLedgerFilters): string {
  if (filters.workspaceId || filters.repository || filters.range !== 'any') {
    return 'No sessions match those filters — widen one to see more.';
  }
  if (filters.scope === 'closed') return 'No closed sessions yet. Closing one records it here.';
  if (filters.scope === 'live') return 'No live sessions right now.';
  return 'The ledger is empty.';
}

function shortStamp(iso: string): string {
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return iso;
  return at.toLocaleString(undefined, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}
