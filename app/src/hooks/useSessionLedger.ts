import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { SessionLedgerEntry, SessionLedgerFacets } from '../types/generated';
import type { SessionLedgerPage, SessionLedgerQuery } from './daemonSessionLedgerEvents';
import {
  customSessionRange,
  isRangeError,
  ledgerInstant,
  sessionRangeWindow,
} from '../components/sessionsLedger';
import type { SessionRangeId, SessionScope } from '../components/sessionsLedger';

export interface SessionLedgerFilters {
  scope: SessionScope;
  range: SessionRangeId;
  /** Only read when range is 'custom'; both days count. */
  customFrom: string;
  customTo: string;
  workspaceId: string;
  repository: string;
}

export const EMPTY_SESSION_FILTERS: SessionLedgerFilters = {
  scope: 'all',
  range: 'any',
  customFrom: '',
  customTo: '',
  workspaceId: '',
  repository: '',
};

// A page the eye can scan and the daemon answers in one query. Load-more is one
// keystroke away, so a bigger first page would only cost time nobody asked for.
export const SESSION_PAGE_SIZE = 50;

export interface UseSessionLedgerOptions {
  enabled: boolean;
  list: (query: SessionLedgerQuery) => Promise<SessionLedgerPage>;
  pageSize?: number;
  now?: () => Date;
}

export interface SessionLedgerView {
  filters: SessionLedgerFilters;
  setFilters: (next: SessionLedgerFilters) => void;
  entries: SessionLedgerEntry[];
  facets: SessionLedgerFacets | null;
  omitted: number;
  loading: boolean;
  loadingMore: boolean;
  error: string | null;
  /** Set when the custom range itself is wrong, so nothing is asked of the daemon. */
  filterError: string | null;
  reload: () => void;
  loadMore: () => void;
  recordClose: (entry: SessionLedgerEntry) => void;
}

export function sessionLedgerQuery(
  filters: SessionLedgerFilters,
  now: Date,
): SessionLedgerQuery | { error: string } {
  const query: SessionLedgerQuery = {};
  if (filters.scope === 'closed') query.closed = true;
  if (filters.scope === 'all') query.all = true;

  const range = filters.range === 'custom'
    ? customSessionRange(filters.customFrom, filters.customTo)
    : sessionRangeWindow(filters.range, now);
  if (isRangeError(range)) return range;
  if (range.since) query.since = range.since;
  if (range.until) query.until = range.until;

  if (filters.workspaceId) query.workspace_id = filters.workspaceId;
  if (filters.repository) query.repository = filters.repository;
  return query;
}

/** Whether a row the daemon just closed belongs in the list as it stands, so the
 * projection can place it without a re-read that would lose the user's scroll. */
export function closeBelongsInView(
  entry: SessionLedgerEntry,
  filters: SessionLedgerFilters,
  now: Date,
): boolean {
  if (filters.scope === 'live') return false;
  if (filters.workspaceId && entry.workspace_id !== filters.workspaceId) return false;
  if (filters.repository && (entry.repository ?? '') !== filters.repository) return false;
  const range = filters.range === 'custom'
    ? customSessionRange(filters.customFrom, filters.customTo)
    : sessionRangeWindow(filters.range, now);
  if (isRangeError(range)) return false;
  const at = ledgerInstant(entry);
  if (range.since && at < range.since) return false;
  if (range.until && at >= range.until) return false;
  return true;
}

export function useSessionLedger({
  enabled,
  list,
  pageSize = SESSION_PAGE_SIZE,
  now = () => new Date(),
}: UseSessionLedgerOptions): SessionLedgerView {
  const [filters, setFilters] = useState<SessionLedgerFilters>(EMPTY_SESSION_FILTERS);
  const [entries, setEntries] = useState<SessionLedgerEntry[]>([]);
  const [facets, setFacets] = useState<SessionLedgerFacets | null>(null);
  const [omitted, setOmitted] = useState(0);
  const [nextBefore, setNextBefore] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [reloadNonce, setReloadNonce] = useState(0);
  // Only the newest read may write: a slow first page must not land on top of the
  // filters the user has moved on to.
  const readSeq = useRef(0);
  const filtersRef = useRef(filters);
  filtersRef.current = filters;

  const query = useMemo(() => sessionLedgerQuery(filters, now()), [filters, now]);
  const filterError = 'error' in query ? query.error : null;

  useEffect(() => {
    if (!enabled || filterError) return;
    const seq = ++readSeq.current;
    setLoading(true);
    setError(null);
    list({ ...(query as SessionLedgerQuery), limit: pageSize })
      .then((page) => {
        if (seq !== readSeq.current) return;
        setEntries(page.entries ?? []);
        setFacets(page.facets ?? null);
        setOmitted(page.omitted ?? 0);
        setNextBefore(page.next_before ?? null);
      })
      .catch((failure: Error) => {
        if (seq !== readSeq.current) return;
        setEntries([]);
        setFacets(null);
        setOmitted(0);
        setNextBefore(null);
        setError(failure.message);
      })
      .finally(() => {
        if (seq === readSeq.current) setLoading(false);
      });
    // `query` is derived from filters; depending on it directly would refetch on
    // every render, since it holds a fresh `now`.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [enabled, filters, filterError, list, pageSize, reloadNonce]);

  const reload = useCallback(() => setReloadNonce((n) => n + 1), []);

  const loadMore = useCallback(() => {
    if (!nextBefore || loadingMore || filterError) return;
    const seq = readSeq.current;
    setLoadingMore(true);
    list({ ...(sessionLedgerQuery(filtersRef.current, now()) as SessionLedgerQuery), limit: pageSize, before: nextBefore })
      .then((page) => {
        if (seq !== readSeq.current) return;
        setEntries((current) => [...current, ...(page.entries ?? [])]);
        setOmitted(page.omitted ?? 0);
        setNextBefore(page.next_before ?? null);
      })
      .catch((failure: Error) => {
        if (seq === readSeq.current) setError(failure.message);
      })
      .finally(() => {
        if (seq === readSeq.current) setLoadingMore(false);
      });
  }, [nextBefore, loadingMore, filterError, list, pageSize, now]);

  const recordClose = useCallback((entry: SessionLedgerEntry) => {
    setEntries((current) => {
      const at = current.findIndex((row) => row.id === entry.id);
      if (at >= 0) {
        const next = current.slice();
        next[at] = entry;
        // A live row that just closed leaves a Live-only list.
        return filtersRef.current.scope === 'live' ? next.filter((row) => row.id !== entry.id) : next;
      }
      if (!closeBelongsInView(entry, filtersRef.current, now())) return current;
      return [entry, ...current];
    });
  }, [now]);

  return {
    filters,
    setFilters,
    entries,
    facets,
    omitted,
    loading,
    loadingMore,
    error,
    filterError,
    reload,
    loadMore,
    recordClose,
  };
}
