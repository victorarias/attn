import { type MouseEvent as ReactMouseEvent } from 'react';
import { StateIndicator } from './StateIndicator';
import { SessionLabel } from './SessionLabel';
import { ChiefOfStaffBadge } from './ChiefOfStaffBadge';
import { SidebarSettlingBar } from './SettlingIndicator';
import { formatShortcut } from '../shortcuts/formatShortcut';
import type { UISessionState } from '../types/sessionState';
import { formatTurnAge, type QueueBands as QueueBandsModel, type QueueRow } from '../utils/queueBands';
import { formatWakeTime } from '../utils/snoozeDurations';
import { useNow, TURN_AGE_TICK_MS } from '../hooks/useNow';

export interface QueueBandSessionView {
  id: string;
  label: string;
  state: UISessionState;
  state_reason?: string;
  chiefOfStaff?: boolean;
  turnOwed?: boolean;
  turnOpenedAt?: string;
  turnSnoozedUntil?: string;
  autoSettleFiresAt?: string;
  autoSettleHeld?: boolean;
  /** The crew member whose day this session is, when it is one. */
  crewMember?: string;
}

/** One registered crew member, as the sidebar draws it. */
export interface CrewMemberView {
  id: string;
  /** The session living this member's day. Absent means asleep. */
  binding_session?: string;
}

interface QueueBandsProps {
  bands: QueueBandsModel<QueueBandSessionView>;
  /**
   * The whole roster. Members are permanent rows: an awake one renders from its
   * live session (bands.crew), a sleeping one from this list alone, so nobody
   * has to go and find a member that is not running.
   */
  crew?: CrewMemberView[];
  /** Start a sleeping member's day. Resolves once its session exists. */
  onWakeCrewMember?: (member: string) => void;
  selectedId: string | null;
  onSelectSession: (id: string) => void;
  onSettleTurn: (id: string) => void;
  /**
   * Sessions whose terminal tile is on screen. A band row draws the auto-settle
   * countdown only for the ones that are NOT, since a visible tile already
   * carries it and two copies of the same countdown read as two events.
   */
  onScreenSessionIds?: ReadonlySet<string>;
  /**
   * Pinning one agent out of the queue, and releasing it again.
   *
   * A gesture aimed at a row acts on that row: pinning here takes the single
   * agent out and leaves its workspace and every sibling in. Pinning the whole
   * workspace is still reachable — from its group header in the tree, and from
   * ⌘K — but it is a different act and it is no longer what this button does.
   */
  onPinSession?: (sessionId: string, pinned: boolean) => void;
  /**
   * The per-session menu — chief of staff, close, reload — which the workspace
   * tree row owns when the queue is off. Queue mode does not draw that row for
   * these agents, so without it here everything on that menu would be out of
   * reach for anything in a band.
   */
  onOpenActions?: (session: { id: string; label: string; chiefOfStaff?: boolean }, event: ReactMouseEvent) => void;
  /**
   * Open the duration menu for a row. Offered on turns and on settled rows
   * alike: deferring a run before it finishes — so the turn it would open never
   * opens — is the case snooze exists for, and that row is by definition not in
   * the turns band yet.
   */
  onOpenSnooze?: (session: { id: string; label: string }, event: ReactMouseEvent) => void;
}

function QueueRowView({
  row,
  selected,
  age,
  wake,
  onSelect,
  onSettle,
  onSnooze,
  onWake,
  onPin,
  onUnpin,
  onOpenActions,
  showSettling,
  testIdPrefix,
}: {
  row: QueueRow<QueueBandSessionView>;
  selected: boolean;
  age?: string;
  /** When a deferred agent comes back. Only snoozed rows carry one. */
  wake?: string;
  onSelect: () => void;
  onSettle?: () => void;
  onSnooze?: (event: ReactMouseEvent) => void;
  onWake?: () => void;
  onPin?: () => void;
  onUnpin?: () => void;
  onOpenActions?: (event: ReactMouseEvent) => void;
  showSettling?: boolean;
  testIdPrefix: string;
}) {
  const { session } = row;
  return (
    <div
      className={`session-item queue-row ${selected ? 'selected' : ''}`.trim()}
      data-testid={`${testIdPrefix}-${session.id}`}
      data-state={session.state}
      data-workspace-id={row.workspaceId}
    >
      {/*
        Opening the session is a real button so it is reachable by Tab and
        pressed by Enter or Space, not a click handler on the row. It fills the
        row from behind rather than wrapping its contents, which keeps a click
        anywhere on the row opening the session and leaves every other child a
        direct child of .session-item — the row's flex layout and the `>`
        selectors that style it are untouched. The settle, pin, and actions
        controls are lifted above it so they stay independently clickable and
        do not sit inside an interactive ancestor.
      */}
      <button
        type="button"
        className="queue-row-select"
        data-testid={`queue-select-${session.id}`}
        aria-label={`Open ${session.label}`}
        onClick={onSelect}
      />
      <StateIndicator state={session.state} size="md" seed={session.id} reason={session.state_reason} />
      {/*
        No workspace name in a band row: the row is about the agent, the label
        needs every column the sidebar has, and the pin button's tooltip still
        says which workspace the row would leave the queue by. The age anchors
        to the right edge; the hover controls float above it (see .queue-row-
        controls) rather than reserving row width for buttons that are usually
        invisible.
      */}
      <SessionLabel label={session.label} />
      {session.chiefOfStaff && <ChiefOfStaffBadge />}
      {age && <span className="queue-row-age">{age}</span>}
      {wake && <span className="queue-row-wake-at">{wake}</span>}
      {(onOpenActions || onPin || onUnpin || onSettle || onSnooze || onWake) && (
        <div className="queue-row-controls">
          {onWake && (
            <button
              type="button"
              className="queue-row-wake"
              data-testid={`queue-wake-${session.id}`}
              title="Wake now — bring it back to the queue"
              aria-label={`Wake ${session.label}`}
              onClick={(event) => {
                event.stopPropagation();
                onWake();
              }}
            >
              ↩
            </button>
          )}
          {onSnooze && (
            <button
              type="button"
              className="queue-row-snooze"
              data-testid={`queue-snooze-${session.id}`}
              title={`Snooze this agent (${formatShortcut('session.snooze')})`}
              aria-label={`Snooze ${session.label}`}
              onClick={(event) => {
                event.stopPropagation();
                onSnooze(event);
              }}
            >
              ☾
            </button>
          )}
          {onOpenActions && (
            <div className="session-actions">
              <button
                type="button"
                className="session-action-btn session-more-btn"
                data-testid={`session-actions-${session.id}`}
                onClick={onOpenActions}
                title="Session actions"
                aria-label={`Actions for ${session.label}`}
              >
                •••
              </button>
            </div>
          )}
          {onPin && (
            <button
              type="button"
              className="queue-row-pin"
              data-testid={`queue-pin-${session.id}`}
              title="Pin this agent — keep it in view, out of the queue"
              aria-label={`Pin ${session.label}`}
              onClick={(event) => {
                event.stopPropagation();
                onPin();
              }}
            >
              📍
            </button>
          )}
          {onUnpin && (
            <button
              type="button"
              className="queue-row-pin"
              data-testid={`queue-unpin-${session.id}`}
              title="Unpin — put this agent back in the queue"
              aria-label={`Unpin ${session.label}`}
              onClick={(event) => {
                event.stopPropagation();
                onUnpin();
              }}
            >
              📌
            </button>
          )}
          {onSettle && (
            <button
              type="button"
              className="queue-row-settle"
              data-testid={`queue-settle-${session.id}`}
              title={`Settle this turn (${formatShortcut('session.settle')})`}
              aria-label={`Settle ${session.label}`}
              onClick={(event) => {
                event.stopPropagation();
                onSettle();
              }}
            >
              ✓
            </button>
          )}
        </div>
      )}
      {showSettling && (session.autoSettleFiresAt || session.autoSettleHeld) && (
        <SidebarSettlingBar firesAt={session.autoSettleFiresAt} held={session.autoSettleHeld} />
      )}
    </div>
  );
}

/**
 * The sidebar's standing order: the chief in its own anchored slot, the turns
 * the user owes oldest first, the settled rest, then the agents pinned out of
 * the queue one at a time.
 *
 * An agent appears in exactly one of these, which is what lets its position
 * carry meaning. Rows carry their live state in every band, because none of them
 * is about whether an agent is running — an agent you steered is still yours
 * until you settle it, a settled one may well be working, and a pinned one is
 * usually the one you are working in.
 *
 * Pinned sits last so the queue and the draining of it stay at the top of the
 * eye's path; the pinned workspaces render as groups below it, and muted below
 * those. Both kinds of pinned thing are places you go and get work rather than a
 * list handed to you, which is why they neighbour each other.
 */
export function QueueBands({
  bands,
  crew,
  onWakeCrewMember,
  selectedId,
  onSelectSession,
  onSettleTurn,
  onScreenSessionIds,
  onPinSession,
  onOpenActions,
  onOpenSnooze,
}: QueueBandsProps) {
  const now = useNow(TURN_AGE_TICK_MS);
  const offScreen = (id: string) => !onScreenSessionIds?.has(id);
  const snoozeHandler = (session: QueueBandSessionView) =>
    onOpenSnooze && ((event: ReactMouseEvent) => onOpenSnooze(session, event));
  const crewRows = buildCrewRows(crew, bands.crew);

  return (
    <div className="queue-bands" data-testid="sidebar-queue">
      {bands.chief && (
        <QueueRowView
          row={bands.chief}
          selected={selectedId === bands.chief.session.id}
          onSelect={() => onSelectSession(bands.chief!.session.id)}
          onOpenActions={onOpenActions && ((event) => onOpenActions(bands.chief!.session, event))}
          testIdPrefix="queue-chief"
        />
      )}
      <div className="queue-band-header">
        <span>Your turn</span>
        {bands.turns.length > 0 && <span className="queue-band-count">{bands.turns.length}</span>}
      </div>
      {bands.turns.length === 0 ? (
        <div className="queue-band-empty" data-testid="queue-empty">Nothing owed.</div>
      ) : (
        bands.turns.map((row) => (
          <QueueRowView
            key={row.session.id}
            row={row}
            selected={selectedId === row.session.id}
            age={formatTurnAge(row.session.turnOpenedAt, now)}
            onSelect={() => onSelectSession(row.session.id)}
            onSettle={() => onSettleTurn(row.session.id)}
            onSnooze={snoozeHandler(row.session)}
            onPin={onPinSession && (() => onPinSession(row.session.id, true))}
            onOpenActions={onOpenActions && ((event) => onOpenActions(row.session, event))}
            showSettling={offScreen(row.session.id)}
            testIdPrefix="queue-turn"
          />
        ))
      )}
      {bands.settled.length > 0 && (
        <>
          <div className="queue-band-header">
            <span>Settled</span>
          </div>
          {bands.settled.map((row) => (
            // No settle button: it is already settled, and offering the act
            // again would suggest there is something here to discharge.
            <QueueRowView
              key={row.session.id}
              row={row}
              selected={selectedId === row.session.id}
              onSelect={() => onSelectSession(row.session.id)}
              // Snooze is offered here too: deferring an agent that is still
              // running, so the turn it would open on finishing never opens, is
              // the reach the verb was designed for — and that agent is settled,
              // not owed.
              onSnooze={snoozeHandler(row.session)}
              onPin={onPinSession && (() => onPinSession(row.session.id, true))}
              onOpenActions={onOpenActions && ((event) => onOpenActions(row.session, event))}
              testIdPrefix="queue-settled"
            />
          ))}
        </>
      )}
      {(bands.pinned.length > 0 || crewRows.length > 0) && (
        <>
          <div className="queue-band-header">
            <span>Pinned</span>
            <span className="queue-band-count">{bands.pinned.length + crewRows.length}</span>
          </div>
          {/*
            The crew, first and permanent. A member is pin-shaped — it stepped
            out of the queue the same way a pinned agent did — but it is not a
            pin: nobody put it here and there is no unpin. The rail down its left
            edge is what says so at a glance, and a sleeping member keeps its row
            so waking it is one click rather than a hunt.
          */}
          {crewRows.map((crewRow) => (
            <CrewRowView
              key={crewRow.member}
              member={crewRow.member}
              row={crewRow.row}
              selected={crewRow.row ? selectedId === crewRow.row.session.id : false}
              onSelect={crewRow.row ? () => onSelectSession(crewRow.row!.session.id) : undefined}
              onWake={onWakeCrewMember && (() => onWakeCrewMember(crewRow.member))}
              onOpenActions={
                crewRow.row && onOpenActions
                  ? (event) => onOpenActions(crewRow.row!.session, event)
                  : undefined
              }
            />
          ))}
          {bands.pinned.map((row) => (
            // No settle and no snooze: both are ways of answering "whose turn is
            // it", and a pinned agent has stepped out of that question entirely.
            // Unpinning is the only act here, and it is the way back in.
            <QueueRowView
              key={row.session.id}
              row={row}
              selected={selectedId === row.session.id}
              onSelect={() => onSelectSession(row.session.id)}
              onUnpin={onPinSession && (() => onPinSession(row.session.id, false))}
              onOpenActions={onOpenActions && ((event) => onOpenActions(row.session, event))}
              testIdPrefix="queue-pinned"
            />
          ))}
        </>
      )}
    </div>
  );
}

/**
 * The roster and the live days, merged into one permanent row per member, in
 * member-id order. A member with a live day carries its session; one without
 * carries none and is asleep. The roster is the authority on who exists — a
 * bound session whose member left the roster still gets a row, because the
 * session is real and dropping it would hide a running agent.
 */
export function buildCrewRows(
  crew: CrewMemberView[] | undefined,
  awake: QueueRow<QueueBandSessionView>[],
): { member: string; row?: QueueRow<QueueBandSessionView> }[] {
  const byMember = new Map<string, QueueRow<QueueBandSessionView>>();
  for (const row of awake) {
    const member = row.session.crewMember;
    if (member && !byMember.has(member)) byMember.set(member, row);
  }
  const members = new Set<string>([...(crew ?? []).map((entry) => entry.id), ...byMember.keys()]);
  return [...members]
    .sort((a, b) => (a < b ? -1 : a > b ? 1 : 0))
    .map((member) => ({ member, row: byMember.get(member) }));
}

/**
 * One crew member's row. Awake, it is the session's row with the crew rail;
 * asleep, it is the member itself with one act on it — wake — and no state to
 * report, because there is nothing running to have a state.
 */
function CrewRowView({
  member,
  row,
  selected,
  onSelect,
  onWake,
  onOpenActions,
}: {
  member: string;
  row?: QueueRow<QueueBandSessionView>;
  selected: boolean;
  onSelect?: () => void;
  onWake?: () => void;
  onOpenActions?: (event: ReactMouseEvent) => void;
}) {
  const awake = Boolean(row);
  const label = row?.session.label || member;
  return (
    <div
      className={`session-item queue-row queue-row--crew ${selected ? 'selected' : ''}`.trim()}
      data-testid={`queue-crew-${member}`}
      data-crew-member={member}
      data-crew-state={awake ? 'awake' : 'asleep'}
      data-state={row?.session.state}
      data-workspace-id={row?.workspaceId}
    >
      <button
        type="button"
        className="queue-row-select"
        data-testid={`queue-crew-select-${member}`}
        aria-label={awake ? `Open ${label}` : `Wake ${member}`}
        onClick={awake ? onSelect : onWake}
      />
      {awake ? (
        <StateIndicator state={row!.session.state} size="md" seed={row!.session.id} reason={row!.session.state_reason} />
      ) : (
        // No state to draw: nothing is running. The hollow ring is the same size
        // as an indicator so every crew row's label starts on the same column.
        <span className="crew-asleep-dot" aria-hidden="true" />
      )}
      <SessionLabel label={label} />
      <span className="crew-row-mark" title={awake ? `${member} is awake` : `${member} is asleep`}>
        {awake ? 'crew' : 'asleep'}
      </span>
      {!awake && onWake && (
        <div className="queue-row-controls">
          <button
            type="button"
            className="queue-row-wake"
            data-testid={`queue-crew-wake-${member}`}
            title={`Wake ${member} — start its day`}
            aria-label={`Wake ${member}`}
            onClick={(event) => {
              event.stopPropagation();
              onWake();
            }}
          >
            ☀
          </button>
        </div>
      )}
      {awake && onOpenActions && (
        <div className="queue-row-controls">
          <button
            type="button"
            className="session-actions session-more-btn"
            data-testid={`session-actions-${row!.session.id}`}
            title="Session actions"
            aria-label={`Actions for ${label}`}
            onClick={(event) => {
              event.stopPropagation();
              onOpenActions(event);
            }}
          >
            •••
          </button>
        </div>
      )}
    </div>
  );
}

interface QueueSnoozedSectionProps {
  rows: QueueRow<QueueBandSessionView>[];
  selectedId: string | null;
  expanded: boolean;
  onToggleExpanded: () => void;
  onSelectSession: (id: string) => void;
  onWakeTurn: (id: string) => void;
}

/**
 * Deferred agents, collapsed at the foot of the sidebar above the muted
 * workspaces.
 *
 * Not a band. The bands answer "whose turn is it"; this answers "what did I put
 * off", which is a different question with a different lifetime — every row here
 * leaves on its own, at a time the user named. It sits with muted rather than
 * with settled because both are the quiet end of the sidebar, and it sits above
 * muted because *not yet* is nearer to your attention than *not ever*.
 *
 * Collapsed by default, with a count. A snooze surfaces itself when it wakes, so
 * the section is for checking on a promise or breaking it early — neither of
 * which is worth standing room.
 */
export function QueueSnoozedSection({
  rows,
  selectedId,
  expanded,
  onToggleExpanded,
  onSelectSession,
  onWakeTurn,
}: QueueSnoozedSectionProps) {
  const now = useNow(TURN_AGE_TICK_MS);
  if (rows.length === 0) return null;

  return (
    <div className="muted-sessions-section" data-testid="sidebar-snoozed">
      <button
        type="button"
        className="muted-sessions-header"
        onClick={onToggleExpanded}
        aria-expanded={expanded}
        data-testid="snoozed-section-header"
      >
        <span className={`muted-sessions-chevron ${expanded ? 'expanded' : ''}`}>▸</span>
        Snoozed ({rows.length})
      </button>
      {expanded && (
        <div className="muted-sessions-list">
          {rows.map((row) => (
            // No settle: a snoozed turn is already closed. Waking is the only
            // act, and it is the undo rather than a second way to dismiss.
            <QueueRowView
              key={row.session.id}
              row={row}
              selected={selectedId === row.session.id}
              wake={formatWakeTime(row.session.turnSnoozedUntil, now)}
              onSelect={() => onSelectSession(row.session.id)}
              onWake={() => onWakeTurn(row.session.id)}
              testIdPrefix="queue-snoozed"
            />
          ))}
        </div>
      )}
    </div>
  );
}
