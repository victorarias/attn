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

/**
 * Auto-settle: attn closes a turn once you have steered the agent and it has
 * gone back to work. Off by default — it changes state nobody asked it to, so it
 * ships opt-in like the queue itself.
 *
 * The two windows answer different questions. The arm delay is how long the agent
 * must keep working before anything starts, which is what proves the steering
 * took; the countdown is the visible part on the terminal tile, and the only
 * window in which ⌘. can keep the turn. All three are daemon-owned key/value
 * strings and read through the helpers here, so Settings, the command menu, and
 * the indicator can never disagree about what is in effect.
 */
export const AUTO_SETTLE_ENABLED_SETTING = 'auto_settle_enabled';
export const AUTO_SETTLE_ARM_SETTING = 'auto_settle_arm_seconds';
export const AUTO_SETTLE_COUNTDOWN_SETTING = 'auto_settle_countdown_seconds';

export const DEFAULT_AUTO_SETTLE_ARM_SECONDS = 30;
export const DEFAULT_AUTO_SETTLE_COUNTDOWN_SECONDS = 15;

export function isAutoSettleEnabled(settings: Record<string, string>): boolean {
  return (settings[AUTO_SETTLE_ENABLED_SETTING] || 'false') === 'true';
}

/**
 * The effective seconds for one of the two windows. The daemon normalizes both to
 * concrete values in the settings payload, so the fallback is only a safety net
 * for a client that reads settings before the first broadcast lands.
 */
export function autoSettleSeconds(
  settings: Record<string, string>,
  key: typeof AUTO_SETTLE_ARM_SETTING | typeof AUTO_SETTLE_COUNTDOWN_SETTING,
): number {
  const parsed = Number.parseInt(settings[key] ?? '', 10);
  if (Number.isFinite(parsed) && parsed > 0) return parsed;
  return key === AUTO_SETTLE_ARM_SETTING
    ? DEFAULT_AUTO_SETTLE_ARM_SECONDS
    : DEFAULT_AUTO_SETTLE_COUNTDOWN_SECONDS;
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

/**
 * Queue order: how long the turn has been owed, oldest first, tie-broken by id
 * so the order is total and does not shuffle between renders. Exported because
 * home lists the same turns and must list them in the same order — two orders
 * for one queue is two queues.
 */
export function compareTurnOrder(a: QueueBandSession, b: QueueBandSession): number {
  const openedA = a.turnOpenedAt ?? '';
  const openedB = b.turnOpenedAt ?? '';
  if (openedA !== openedB) {
    return openedA < openedB ? -1 : 1;
  }
  return a.id < b.id ? -1 : 1;
}

/**
 * The jump-to-waiting (⌘J) target: of the sessions that want the user, the one
 * whose turn has been owed longest. Queue order, not list order — a jump that
 * follows workspace rank while the band on screen is sorted by age reads as
 * random. `wants` is passed in because each arrangement has its own notion of
 * wanting the user (turn_owed with the queue on, the state predicate with it
 * off) and that choice belongs to the caller.
 */
export function oldestWantedTurn<TSession extends QueueBandSession>(
  sessions: TSession[],
  wants: (session: TSession) => boolean,
): TSession | null {
  let oldest: TSession | null = null;
  for (const session of sessions) {
    if (!wants(session)) continue;
    if (!oldest || compareTurnOrder(session, oldest) < 0) {
      oldest = session;
    }
  }
  return oldest;
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

  turns.sort((a, b) => compareTurnOrder(a.session, b.session));

  return { chief, turns, settled };
}

/**
 * The row after `settledSessionId` in queue order, wrapping to the top, that is
 * still owed according to `stillOwed`.
 *
 * Two snapshots, two jobs. `turns` is the band as it stood while that turn was
 * still owed, and is consulted only for *order* — the settled row being present
 * is what gives the scan a position to continue from. `stillOwed` is the current
 * band, and decides *eligibility*: a broadcast can close several turns at once,
 * so a successor that looked owed in the old snapshot may have settled in the
 * very same update, and landing on it would hand the user another finished
 * agent.
 *
 * A session that is not in `turns` (already settled, pinned, muted, the chief)
 * has no position to move on from, so the scan simply starts at the top. Null
 * means the old snapshot holds nobody still owed — which is not the same as
 * nothing being owed at all; see advanceAfterTurnClosed.
 */
function nextOwedAfter<TSession extends QueueBandSession>(
  turns: QueueRow<TSession>[],
  settledSessionId: string,
  stillOwed: ReadonlySet<string>,
): QueueRow<TSession> | null {
  const current = turns.findIndex((row) => row.session.id === settledSessionId);
  const start = current === -1 ? 0 : current + 1;
  for (let offset = 0; offset < turns.length; offset += 1) {
    const row = turns[(start + offset) % turns.length];
    if (row.session.id !== settledSessionId && stillOwed.has(row.session.id)) {
      return row;
    }
  }
  return null;
}

/** Where selection goes when a turn closes: the next agent, or home. */
export type QueueAdvance<TSession extends QueueBandSession> =
  | { to: 'session'; row: QueueRow<TSession> }
  | { to: 'dashboard' };

/**
 * Where the user should land when the turn they were looking at closed without
 * them asking — an auto-settle countdown completing, or a settle from the
 * sidebar row. Null means stay put: nothing closed, or what changed was not a
 * settle.
 *
 * `previousTurns` is the turns band as it stood before `bands`, which is what
 * makes "closed" observable at all — the row is gone from the band by the time
 * anyone can react, so the two snapshots together are the only record that it
 * was ever there, and the earlier one is also the only place the user's
 * position in the queue still exists.
 *
 * The move that matters is turns → settled specifically. Pinning or muting a
 * workspace clears turn_owed on its sessions too, and those rows leave the
 * bands entirely rather than landing in settled — so requiring the arrival,
 * rather than just the departure, is what keeps a pin from carrying the user
 * away from the workspace they pinned to keep in view.
 *
 * Only the position comes from the old snapshot; whether a row is still worth
 * going to is always read from `bands`. One broadcast can close several turns —
 * an auto-settle countdown and a settle from another client landing together,
 * or two countdowns firing in the same tick — and a successor that was owed in
 * the old snapshot may be settled in this one. Answering from the old snapshot
 * alone would hand the user an agent that is already finished with them, which
 * is the very thing this function exists to avoid.
 *
 * Home is therefore reached from the *current* band being empty, never from the
 * old one running out. A turn that opened in the same broadcast that closed
 * this one has no position in the old snapshot, so the scan cannot find it; the
 * head of the current band is where the queue continues.
 */
export function advanceAfterTurnClosed<TSession extends QueueBandSession>(
  previousTurns: QueueRow<TSession>[],
  bands: QueueBands<TSession> | null,
  sessionId: string | null,
): QueueAdvance<TSession> | null {
  if (!sessionId || !bands) return null;
  const owedBefore = previousTurns.some((row) => row.session.id === sessionId);
  const settledNow = bands.settled.some((row) => row.session.id === sessionId);
  if (!owedBefore || !settledNow) return null;
  const stillOwed = new Set(bands.turns.map((row) => row.session.id));
  const next = nextOwedAfter(previousTurns, sessionId, stillOwed) ?? bands.turns[0] ?? null;
  return next ? { to: 'session', row: next } : { to: 'dashboard' };
}
