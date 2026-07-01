import type { Ticket } from '../hooks/useDaemonSocket';

/** Statuses where a dead owner no longer matters — the ticket is closed. */
const TERMINAL_STATUSES = new Set<string>(['done', 'failed', 'crashed']);

/**
 * True when the ticket is orphaned: reconciliation stamped it (its owning
 * session died while the ticket was still open) and nothing has cleared the
 * stamp since. The daemon clears reconciled_at when the ticket is reassigned
 * or its assignee session respawns, so the flag disappearing means the ticket
 * has a live owner again. Terminal tickets are never shown as orphaned — a
 * closed ticket does not need an owner.
 */
export function isTicketOrphaned(
  t: Pick<Ticket, 'status' | 'reconciled_at'> | null | undefined,
): boolean {
  if (!t) return false;
  return Boolean(t.reconciled_at) && !TERMINAL_STATUSES.has(t.status);
}
