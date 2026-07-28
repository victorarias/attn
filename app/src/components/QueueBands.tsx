import { useEffect, useState, type MouseEvent as ReactMouseEvent } from 'react';
import { StateIndicator } from './StateIndicator';
import { ChiefOfStaffBadge } from './ChiefOfStaffBadge';
import { formatShortcut } from '../shortcuts';
import type { UISessionState } from '../types/sessionState';
import type { QueueBands as QueueBandsModel, QueueRow } from '../utils/queueBands';

export interface QueueBandSessionView {
  id: string;
  label: string;
  state: UISessionState;
  state_reason?: string;
  chiefOfStaff?: boolean;
  turnOwed?: boolean;
  turnOpenedAt?: string;
}

interface QueueBandsProps {
  bands: QueueBandsModel<QueueBandSessionView>;
  selectedId: string | null;
  onSelectSession: (id: string) => void;
  onSettleTurn: (id: string) => void;
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
}

/**
 * Turn ages are read from a clock, not from a prop, so a row that has been
 * outstanding for an hour does not keep claiming it arrived a minute ago
 * whenever the daemon happens to go quiet.
 */
const AGE_TICK_MS = 30_000;

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

function useNow(intervalMs: number): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(timer);
  }, [intervalMs]);
  return now;
}

function QueueRowView({
  row,
  selected,
  age,
  onSelect,
  onSettle,
  onPin,
  onOpenActions,
  testIdPrefix,
}: {
  row: QueueRow<QueueBandSessionView>;
  selected: boolean;
  age?: string;
  onSelect: () => void;
  onSettle?: () => void;
  onPin?: () => void;
  onOpenActions?: (event: ReactMouseEvent) => void;
  testIdPrefix: string;
}) {
  const { session } = row;
  return (
    <div
      className={`session-item queue-row ${selected ? 'selected' : ''}`.trim()}
      data-testid={`${testIdPrefix}-${session.id}`}
      data-state={session.state}
      data-workspace-id={row.workspaceId}
      onClick={onSelect}
    >
      <StateIndicator state={session.state} size="md" seed={session.id} reason={session.state_reason} />
      <span className="session-label">{session.label}</span>
      {session.chiefOfStaff && <ChiefOfStaffBadge />}
      <span className="queue-row-workspace">{row.workspaceTitle}</span>
      {age && <span className="queue-row-age">{age}</span>}
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
export function QueueBands({ bands, selectedId, onSelectSession, onSettleTurn, onPinWorkspace, onOpenActions }: QueueBandsProps) {
  const now = useNow(AGE_TICK_MS);

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
            onPin={onPinWorkspace && (() => onPinWorkspace(row.workspaceId, true))}
            onOpenActions={onOpenActions && ((event) => onOpenActions(row.session, event))}
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
