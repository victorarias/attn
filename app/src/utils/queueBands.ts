import type { WorkspaceWithSessions, WorkspaceViewSession } from './workspaceViewModels';

/**
 * The daemon-owned setting selecting the sidebar arrangement: off (the default)
 * is the workspace tree alone, on adds the chief's anchored slot and the "Your
 * turn" band above it. Read it through isQueueModeEnabled so Settings, the
 * command menu, and the sidebar can never disagree about what is in effect.
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

export interface QueueBands<TSession extends QueueBandSession> {
  /** The chief's anchored slot. It never queues, so it is always its own row. */
  chief: QueueRow<TSession> | null;
  /** Turns the user owes, oldest first. */
  turns: QueueRow<TSession>[];
}

/**
 * Derive the queue bands that sit above the workspace tree.
 *
 * The tree itself is not an input to this: the queue is additive, so a promoted
 * agent appears in both the band and its workspace group and the tree below is
 * left exactly as queue-mode-off renders it. That duplication is the point — it
 * is the only defence against an agent that needs the user and never enters the
 * queue.
 *
 * Membership is read, never derived: `turnOwed` is the daemon's answer, which
 * already accounts for the exclusions a client cannot see. Ordering is by
 * `turnOpenedAt` ascending — how long the turn has been owed, which does not
 * move when the agent changes state under the user.
 */
export function buildQueueBands<TSession extends QueueBandSession>(
  workspaces: WorkspaceWithSessions<TSession>[],
): QueueBands<TSession> {
  let chief: QueueRow<TSession> | null = null;
  const turns: QueueRow<TSession>[] = [];

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
      if (session.turnOwed) {
        turns.push(row);
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

  return { chief, turns };
}
