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
}

interface QueueBandsProps {
  bands: QueueBandsModel<QueueBandSessionView>;
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
   * Pinning a row's workspace is how an agent leaves the queue for good. The
   * band rows are the only place it can be asked for while queue mode is on:
   * the workspace group header, which owns it in the tree, is not drawn for the
   * workspaces these rows come from.
   */
  onPinWorkspace?: (workspaceId: string, pinned: boolean) => void;
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
      {(onOpenActions || onPin || onSettle || onSnooze || onWake) && (
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
              title={`Pin ${row.workspaceTitle} — take it out of the queue`}
              aria-label={`Pin workspace ${row.workspaceTitle}`}
              onClick={(event) => {
                event.stopPropagation();
                onPin();
              }}
            >
              📍
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
 * the user owes oldest first, then the settled rest.
 *
 * An agent appears in exactly one of these, which is what lets its position
 * carry meaning. Rows carry their live state in both bands, because neither
 * band is about whether an agent is running — an agent you steered is still
 * yours until you settle it, and a settled one may well be working.
 *
 * Only pinned and muted workspaces still render as groups, below; they are
 * places you go and get work rather than a list handed to you.
 */
export function QueueBands({
  bands,
  selectedId,
  onSelectSession,
  onSettleTurn,
  onScreenSessionIds,
  onPinWorkspace,
  onOpenActions,
  onOpenSnooze,
}: QueueBandsProps) {
  const now = useNow(TURN_AGE_TICK_MS);
  const offScreen = (id: string) => !onScreenSessionIds?.has(id);
  const snoozeHandler = (session: QueueBandSessionView) =>
    onOpenSnooze && ((event: ReactMouseEvent) => onOpenSnooze(session, event));

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
            onPin={onPinWorkspace && (() => onPinWorkspace(row.workspaceId, true))}
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
              onPin={onPinWorkspace && (() => onPinWorkspace(row.workspaceId, true))}
              onOpenActions={onOpenActions && ((event) => onOpenActions(row.session, event))}
              testIdPrefix="queue-settled"
            />
          ))}
        </>
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
