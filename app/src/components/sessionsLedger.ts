import type { SessionLedgerEntry } from '../types/generated';

export type SessionScope = 'live' | 'closed' | 'all';

export type SessionRangeId = 'any' | 'today' | 'yesterday' | '7d' | '30d' | 'custom';

/** Half-open, as the daemon reads it: since inclusive, until exclusive. */
export interface SessionRange {
  since?: string;
  until?: string;
}

export interface SessionRangeChoice {
  id: SessionRangeId;
  label: string;
}

export const SESSION_RANGE_CHOICES: SessionRangeChoice[] = [
  { id: 'any', label: 'Any time' },
  { id: 'today', label: 'Today' },
  { id: 'yesterday', label: 'Yesterday' },
  { id: '7d', label: 'Last 7 days' },
  { id: '30d', label: 'Last 30 days' },
  { id: 'custom', label: 'Custom range' },
];

function midnight(now: Date, daysBack: number): Date {
  const day = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  day.setDate(day.getDate() - daysBack);
  return day;
}

/** Calendar days in the viewer's own timezone, matching `attn session list --last`:
 * only the client knows what "today" means, so the daemon is only ever told instants. */
export function sessionRangeWindow(id: SessionRangeId, now: Date): SessionRange {
  switch (id) {
    case 'today':
      return { since: midnight(now, 0).toISOString() };
    case 'yesterday':
      return { since: midnight(now, 1).toISOString(), until: midnight(now, 0).toISOString() };
    case '7d':
      return { since: midnight(now, 6).toISOString() };
    case '30d':
      return { since: midnight(now, 29).toISOString() };
    default:
      return {};
  }
}

/** A custom range is two days the user picked, and both of them count: the end
 * day runs to its last instant, so the exclusive `until` is the day after it. */
export function customSessionRange(from: string, to: string): SessionRange | { error: string } {
  const start = parseDay(from);
  if (!start) return { error: `${from || 'The start date'} is not a date like 2026-09-05` };
  const end = parseDay(to);
  if (!end) return { error: `${to || 'The end date'} is not a date like 2026-09-05` };
  if (end < start) return { error: 'The range ends before it starts; swap the two dates' };
  const exclusive = new Date(end);
  exclusive.setDate(exclusive.getDate() + 1);
  return { since: start.toISOString(), until: exclusive.toISOString() };
}

export function isRangeError(range: SessionRange | { error: string }): range is { error: string } {
  return 'error' in range;
}

function parseDay(value: string): Date | null {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value.trim());
  if (!match) return null;
  const day = new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3]));
  return Number.isNaN(day.getTime()) ? null : day;
}

export function isClosed(entry: SessionLedgerEntry): boolean {
  return !!entry.closed_at;
}

/** A closed row keeps the state it held when it closed, which would read as live. */
export function ledgerState(entry: SessionLedgerEntry): string {
  return isClosed(entry) ? 'closed' : entry.state;
}

export function ledgerInstant(entry: SessionLedgerEntry): string {
  return entry.closed_at || entry.last_seen;
}

export function closedBySomeone(entry: SessionLedgerEntry): string {
  const by = entry.closed_by ?? '';
  return by === 'user' ? 'you' : by;
}

/** Paths are long and their tail is what identifies them, so a narrow column
 * keeps the end rather than the root every repository shares. */
export function shortPath(path: string, segments = 2): string {
  const parts = path.split('/').filter(Boolean);
  if (parts.length <= segments) return path;
  return `…/${parts.slice(-segments).join('/')}`;
}

/** The reopen actions `session_show` offers, in the words a button can wear.
 * The ids are the daemon's; an id it adds later shows as itself. */
const REOPEN_ACTION_LABELS: Record<string, string> = {
  reopen: 'Reopen',
  recreate_worktree_and_reopen: 'Recreate the worktree',
  fetch_recreate_and_reopen: 'Fetch, recreate, reopen',
  start_fresh_same_place: 'Start fresh here',
  start_fresh_elsewhere: 'Start fresh elsewhere',
  start_fresh_default_branch: 'Start fresh on the default branch',
};

export function reopenActionLabel(id: string): string {
  return REOPEN_ACTION_LABELS[id] ?? id;
}
