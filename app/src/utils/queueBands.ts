import type { WorkspaceWithSessions, WorkspaceViewSession } from './workspaceViewModels';
import { isSnoozed } from './snoozeDurations';

/**
 * Daemon-owned setting selecting the sidebar arrangement: off (default) is the
 * workspace tree alone, on adds the chief's slot and the "Your turn" band.
 * Always read through isQueueModeEnabled so no surface disagrees.
 */
export const QUEUE_MODE_SETTING = 'queue_mode_enabled';

export function isQueueModeEnabled(settings: Record<string, string>): boolean {
  return (settings[QUEUE_MODE_SETTING] || 'false') === 'true';
}

/**
 * Auto-settle: closes a turn once the user steered the agent and it went back to
 * work. Off by default. The arm delay is how long the agent must keep working
 * before anything starts; the countdown is the visible window, and the only one
 * in which ⌘. can keep the turn. Always read through the helpers here.
 */
export const AUTO_SETTLE_ENABLED_SETTING = 'auto_settle_enabled';
export const AUTO_SETTLE_ARM_SETTING = 'auto_settle_arm_seconds';
export const AUTO_SETTLE_COUNTDOWN_SETTING = 'auto_settle_countdown_seconds';

export const DEFAULT_AUTO_SETTLE_ARM_SECONDS = 30;
export const DEFAULT_AUTO_SETTLE_COUNTDOWN_SECONDS = 15;

export function isAutoSettleEnabled(settings: Record<string, string>): boolean {
  return (settings[AUTO_SETTLE_ENABLED_SETTING] || 'false') === 'true';
}

/** The effective seconds for one of the two windows; the daemon normalizes
 * both, so the fallback only covers a read before the first broadcast. */
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
  turnSnoozedUntil?: string;
  /** Set while this session is individually pinned out of the queue. */
  pinnedAt?: string;
  /** Set on a shell: the agent session it was split from. */
  parentSessionId?: string;
  /** The crew member this session is living the day of, when it is one. */
  crewMember?: string;
}

export interface QueueRow<TSession extends QueueBandSession> {
  session: TSession;
  workspaceId: string;
  workspaceTitle: string;
}

/** How long a turn has been outstanding, in the coarsest unit that still reads
 * as an age. `now` is passed in so this stays a pure projection. */
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

/** Queue order: turn owed longest first, tie-broken by id so the order is
 * total. Home lists the same turns and must use the same order. */
export function compareTurnOrder(a: QueueBandSession, b: QueueBandSession): number {
  const openedA = a.turnOpenedAt ?? '';
  const openedB = b.turnOpenedAt ?? '';
  if (openedA !== openedB) {
    return openedA < openedB ? -1 : 1;
  }
  return a.id < b.id ? -1 : 1;
}

/**
 * The jump-to-waiting (⌘J) target, in queue order rather than list order.
 * `wants` is the caller's: each arrangement has its own notion of wanting the
 * user (turn_owed with the queue on, the state predicate with it off).
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
  /**
   * Sessions pinned out of the queue, in pin order — not state order, so a row
   * never moves because the agent in it started working.
   */
  pinned: QueueRow<TSession>[];
  /**
   * The days crew members are living right now, member id order. A member's row
   * is permanent, so it renders in the pinned region whether the member is awake
   * (this band) or asleep (from the roster, which has no session to band).
   */
  crew: QueueRow<TSession>[];
  /** Agents the user deferred, soonest wake first — the only question a snoozed
   * row answers is when it comes back. */
  snoozed: QueueRow<TSession>[];
}

/**
 * Derive the sidebar's standing order: the chief, the turns you owe, the settled
 * rest. Every agent lands in exactly one band; sessions of pinned or muted
 * workspaces land in none and keep the tree's grouped rendering. The chief holds
 * its anchored slot whatever its workspace, and the tree drops it there.
 *
 * Membership is read, never derived: `turnOwed` is the daemon's answer, which
 * accounts for exclusions a client cannot see. Settled keeps the tree's own
 * order (workspace rank, then session order), deliberately not a state order.
 */
export function buildQueueBands<TSession extends QueueBandSession>(
  workspaces: WorkspaceWithSessions<TSession>[],
  now: number = Date.now(),
): QueueBands<TSession> {
  let chief: QueueRow<TSession> | null = null;
  const turns: QueueRow<TSession>[] = [];
  const settled: QueueRow<TSession>[] = [];
  const pinned: QueueRow<TSession>[] = [];
  const snoozed: QueueRow<TSession>[] = [];
  const crew: QueueRow<TSession>[] = [];
  const attachedParents = liveParentIds(workspaces);

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
      // Before the workspace's own pin or mute: a member's row is permanent and
      // does not depend on where its day happens to be living.
      if (session.crewMember) {
        crew.push(row);
        continue;
      }
      if (workspace.pinned || workspace.muted) {
        continue;
      }
      // A pin outranks both the snooze and the turn check: it has no deadline to
      // come back from, and the daemon already withholds turnOwed from it.
      if (session.pinnedAt) {
        pinned.push(row);
        continue;
      }
      // A satellite is reached through its agent's pane, so it gets no row.
      // Losing that parent gives it a row back; nothing becomes unreachable.
      if (isAttachedSatellite(session, workspace.id, attachedParents)) {
        continue;
      }
      // Before the turn check, so the row's home does not depend on the daemon's
      // settle-as-it-snoozes invariant holding in a mid-broadcast snapshot.
      if (isSnoozed(session.turnSnoozedUntil, now)) {
        snoozed.push(row);
      } else if (session.turnOwed) {
        turns.push(row);
      } else {
        settled.push(row);
      }
    }
  }

  turns.sort((a, b) => compareTurnOrder(a.session, b.session));
  pinned.sort((a, b) => comparePinOrder(a.session, b.session));
  snoozed.sort((a, b) => compareWakeOrder(a.session, b.session));
  crew.sort((a, b) => compareCrewOrder(a.session, b.session));

  return { chief, turns, settled, pinned, snoozed, crew };
}

/**
 * Index every session by its workspace, so a satellite's parent is confirmed
 * present *and* co-located in one lookup — a moved pane gets its row back.
 */
function liveParentIds(workspaces: WorkspaceWithSessions<QueueBandSession>[]): Map<string, string> {
  const byId = new Map<string, string>();
  for (const workspace of workspaces) {
    for (const session of workspace.sessions) {
      byId.set(session.id, workspace.id);
    }
  }
  return byId;
}

/**
 * Whether this is a shell whose parent agent is present in the same workspace —
 * the one case that earns no row. An orphan keeps its settled row: the queue
 * reorders and never hides.
 */
function isAttachedSatellite(
  session: QueueBandSession,
  workspaceId: string,
  parents: Map<string, string>,
): boolean {
  const parentId = session.parentSessionId;
  if (!parentId) return false;
  return parents.get(parentId) === workspaceId;
}

/** Member order: by name, so a member's row is where it was yesterday. */
function compareCrewOrder(a: QueueBandSession, b: QueueBandSession): number {
  const memberA = a.crewMember ?? '';
  const memberB = b.crewMember ?? '';
  if (memberA !== memberB) {
    return memberA < memberB ? -1 : 1;
  }
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
}

/** Pin order: earliest pin first, tie-broken by id so the order is total. */
function comparePinOrder(a: QueueBandSession, b: QueueBandSession): number {
  const pinnedA = a.pinnedAt ?? '';
  const pinnedB = b.pinnedAt ?? '';
  if (pinnedA !== pinnedB) {
    return pinnedA < pinnedB ? -1 : 1;
  }
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
}

/** Soonest wake first, tie-broken by id so the order is total. Home lists the
 * same deferred agents in the same order as the sidebar. */
export function compareWakeOrder(a: QueueBandSession, b: QueueBandSession): number {
  const untilA = a.turnSnoozedUntil ?? '';
  const untilB = b.turnSnoozedUntil ?? '';
  if (untilA !== untilB) {
    return untilA < untilB ? -1 : 1;
  }
  return a.id < b.id ? -1 : 1;
}

/**
 * The row after `settledSessionId` in queue order, wrapping, that is still owed.
 * Two snapshots, two jobs: `turns` supplies only *order* (and the position to
 * continue from), `stillOwed` decides *eligibility*, because one broadcast can
 * close several turns. A session absent from `turns` starts the scan at the top.
 * Null means the old snapshot holds nobody still owed — not that nothing is.
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

/**
 * The turn at the head of the queue. Exported because the handover and home both
 * send the user there and must not disagree about which agent "next" means.
 */
export function headOfQueue<TSession extends QueueBandSession>(
  bands: QueueBands<TSession> | null,
): QueueRow<TSession> | null {
  return bands?.turns[0] ?? null;
}

/** Where selection goes when a turn closes: the next agent, or home. */
export type QueueAdvance<TSession extends QueueBandSession> =
  | { to: 'session'; row: QueueRow<TSession> }
  | { to: 'dashboard' };

/**
 * Where the user lands when the turn they were looking at closed without them
 * asking. Null means stay put. `previousTurns` is the only record the row was
 * ever there, and the only place the user's position still exists.
 *
 * The move that counts is turns → settled or turns → snoozed. Requiring the
 * ARRIVAL, not just the departure, is what keeps pinning or muting a workspace
 * from carrying the user away from it. Arrival in the *pinned* band is
 * deliberately not a close — pinning is the user saying they will come to this
 * one themselves — so its absence from `closedNow` is not an omission to fix.
 *
 * Only the position comes from the old snapshot; eligibility always comes from
 * `bands`, since one broadcast can close several turns. Home is therefore
 * reached from the *current* band being empty, never from the old one running
 * out: a turn opened in the same broadcast has no position in the old snapshot.
 */
export function advanceAfterTurnClosed<TSession extends QueueBandSession>(
  previousTurns: QueueRow<TSession>[],
  bands: QueueBands<TSession> | null,
  sessionId: string | null,
): QueueAdvance<TSession> | null {
  if (!sessionId || !bands) return null;
  const owedBefore = previousTurns.some((row) => row.session.id === sessionId);
  const closedNow =
    bands.settled.some((row) => row.session.id === sessionId) ||
    bands.snoozed.some((row) => row.session.id === sessionId);
  if (!owedBefore || !closedNow) return null;
  const stillOwed = new Set(bands.turns.map((row) => row.session.id));
  const next = nextOwedAfter(previousTurns, sessionId, stillOwed) ?? headOfQueue(bands);
  return next ? { to: 'session', row: next } : { to: 'dashboard' };
}
