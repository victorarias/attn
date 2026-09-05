import type { SessionLedgerEntry, SessionLedgerFacets } from '../types/generated';
import type { PendingRequests } from './daemonPendingRequests';
import { settlePendingRequest } from './daemonPendingRequests';

/** The generator flattens the result embedded in a response to anonymous `Entry`
 * and `Facets` names, so the page is restated here against the standalone models. */
export interface SessionLedgerPage {
  entries: SessionLedgerEntry[];
  facets?: SessionLedgerFacets;
  next_before?: string;
  omitted: number;
}

/** The wire shape of session_list. Filters combine with AND; the daemon applies
 * them before paging, so `before` continues the same filtered list. */
export interface SessionLedgerQuery {
  closed?: boolean;
  all?: boolean;
  limit?: number;
  before?: string;
  workspace_id?: string;
  repository?: string;
  since?: string;
  until?: string;
}

export interface SessionLedgerEventContext {
  pending: PendingRequests;
  /** A session just closed: the row it became, so an open list updates in place. */
  onSessionClosed?: (entry: SessionLedgerEntry) => void;
}

type SessionLedgerEvent = {
  event: string;
  request_id?: unknown;
  success?: boolean;
  error?: string;
  result?: unknown;
  entry?: unknown;
  session_ledger_entry?: unknown;
};

export function handleSessionLedgerDaemonEvent(
  event: SessionLedgerEvent,
  context: SessionLedgerEventContext,
): boolean {
  switch (event.event) {
    case 'session_list_result':
      settlePendingRequest(
        context.pending,
        'session_list',
        event,
        (value) => value.result as SessionLedgerPage | undefined,
        'Reading the session ledger failed',
      );
      return true;
    case 'session_show_result':
      settlePendingRequest(
        context.pending,
        'session_show',
        event,
        (value) => value.entry as SessionLedgerEntry | undefined,
        'Reading that session failed',
      );
      return true;
    case 'session_closed': {
      const entry = event.session_ledger_entry as SessionLedgerEntry | undefined;
      if (entry) context.onSessionClosed?.(entry);
      return true;
    }
    default:
      return false;
  }
}
