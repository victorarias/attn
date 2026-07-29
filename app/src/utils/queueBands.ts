import type { WorkspaceWithSessions, WorkspaceViewSession } from './workspaceViewModels';

/**
 * The daemon-owned setting selecting the sidebar arrangement: off (the default)
 * is the workspace tree alone, on adds the chief's anchored slot and the "Your
 * turn" band above it. Read it through isQueueModeEnabled so the sidebar's
 * display popover, the command menu, and the sidebar itself can never disagree
 * about what is in effect.
 */
export const QUEUE_MODE_SETTING = 'queue_mode_enabled';

export function isQueueModeEnabled(settings: Record<string, string>): boolean {
  return (settings[QUEUE_MODE_SETTING] || 'false') === 'true';
}

export interface QueueBandSession extends WorkspaceViewSession {
  chiefOfStaff?: boolean;
  turnOwed?: boolean;
  turnOpenedAt?: string;
}

export interface QueueRow<TSession extends QueueBandSession> {
  session: TSession;
  workspaceId: string;
  workspaceTitle: string;
}

/**
 * How long a turn has been outstanding, in the coarsest unit that still reads
 * as an age. `now` is passed in rather than read here so the caller owns the
 * clock and the function stays a pure projection of the two timestamps.
 */
export function formatTurnAge(openedAt: string | undefined, now: number): string {
  if (!openedAt) return '';
  const opened = Date.parse(openedAt);
  if (Number.isNaN(opened)) return '';
  const seconds = Math.max(0, Math.round((now - opened) / 1000));
  if (seconds < 60) return 'now';
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours}h`;
  return `${Math.round(hours / 24)}d`;
}

export interface QueueBands<TSession extends QueueBandSession> {
  /** The chief's anchored slot. It never queues, so it is always its own row. */
  chief: QueueRow<TSession> | null;
  /** Turns the user owes, oldest first. */
  turns: QueueRow<TSession>[];
  /** Everything else you could go and look at, in a stable order. */
  settled: QueueRow<TSession>[];
}

/**
 * Derive the sidebar's standing order: the chief, the turns you owe, and the
 * settled rest.
 *
 * Every agent lands in exactly one band, which is what makes a row's position
 * mean something. Pinned and muted workspaces are in none of them — they keep
 * the tree's grouped rendering, because a pinned workspace is a place you go
 * and get work rather than a list handed to you, and a muted one is out of
 * sight by definition.
 *
 * The chief is the one exception: it holds its anchored slot whatever its
 * workspace is, since it is the seat you always want to reach rather than a
 * piece of work you filed away. The tree drops it from a workspace it still
 * draws, so it is never in two places at once either.
 *
 * Membership is read, never derived: `turnOwed` is the daemon's answer, which
 * already accounts for the exclusions a client cannot see. Turn ordering is by
 * `turnOpenedAt` ascending — how long the turn has been owed, which does not
 * move when the agent changes state under the user. Settled keeps the tree's
 * own order — workspace rank, then the workspace's session order — which is
 * deliberately not a state order: an agent nobody is asking you about should
 * not jump around the list as it works.
 */
export function buildQueueBands<TSession extends QueueBandSession>(
  workspaces: WorkspaceWithSessions<TSession>[],
): QueueBands<TSession> {
  let chief: QueueRow<TSession> | null = null;
  const turns: QueueRow<TSession>[] = [];
  const settled: QueueRow<TSession>[] = [];

  for (const workspace of workspaces) {
    for (const session of workspace.sessions) {
      const row: QueueRow<TSession> = {
        session,
        workspaceId: workspace.id,
        workspaceTitle: workspace.title,
      };
      if (session.chiefOfStaff) {
        if (!chief) {
          chief = row;
        }
        continue;
      }
      if (workspace.pinned || workspace.muted) {
        continue;
      }
      if (session.turnOwed) {
        turns.push(row);
      } else {
        settled.push(row);
      }
    }
  }

  turns.sort((a, b) => {
    const openedA = a.session.turnOpenedAt ?? '';
    const openedB = b.session.turnOpenedAt ?? '';
    if (openedA !== openedB) {
      return openedA < openedB ? -1 : 1;
    }
    return a.session.id < b.session.id ? -1 : 1;
  });

  return { chief, turns, settled };
}

/**
 * The agent to land on once `settledSessionId`'s turn is closed: the next row in
 * queue order, wrapping to the top, and never the row just settled.
 *
 * The target is read from the queue as it stands *before* the settle. The settle
 * is a round trip to the daemon and the band only loses the row when the
 * broadcast comes back, so anything read after the call still contains the row
 * that was settled — deciding here keeps the jump off that race entirely.
 *
 * A session that is not in the band (already settled, pinned, muted, the chief)
 * has no position to move on from, so the scan simply starts at the top. Null
 * means nothing is left to go to and selection should stay where it is.
 */
export function nextTurnAfterSettle<TSession extends QueueBandSession>(
  turns: QueueRow<TSession>[],
  settledSessionId: string | null,
): QueueRow<TSession> | null {
  const current = turns.findIndex((row) => row.session.id === settledSessionId);
  const start = current === -1 ? 0 : current + 1;
  for (let offset = 0; offset < turns.length; offset += 1) {
    const row = turns[(start + offset) % turns.length];
    if (row.session.id !== settledSessionId) {
      return row;
    }
  }
  return null;
}
