import { useEffect, useState } from 'react';
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
  testIdPrefix,
}: {
  row: QueueRow<QueueBandSessionView>;
  selected: boolean;
  age?: string;
  onSelect: () => void;
  onSettle?: () => void;
  testIdPrefix: string;
}) {
  const { session } = row;
  return (
    <div
      className={`session-item queue-row ${selected ? 'selected' : ''}`.trim()}
      data-testid={`${testIdPrefix}-${session.id}`}
      data-state={session.state}
      onClick={onSelect}
    >
      <StateIndicator state={session.state} size="md" seed={session.id} reason={session.state_reason} />
      <span className="session-label">{session.label}</span>
      {session.chiefOfStaff && <ChiefOfStaffBadge />}
      <span className="queue-row-workspace">{row.workspaceTitle}</span>
      {age && <span className="queue-row-age">{age}</span>}
      {onSettle && (
        <button
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
 * The queue arrangement's two bands, rendered above the workspace tree: the
 * chief in its own anchored slot, then the turns the user owes, oldest first.
 *
 * The tree below is untouched, so an agent the daemon never promoted is still
 * exactly where it has always been. Rows carry their live state because being
 * in the queue no longer implies being stopped — an agent you steered is still
 * yours until you settle it.
 */
export function QueueBands({ bands, selectedId, onSelectSession, onSettleTurn }: QueueBandsProps) {
  const now = useNow(AGE_TICK_MS);

  return (
    <div className="queue-bands" data-testid="sidebar-queue">
      {bands.chief && (
        <QueueRowView
          row={bands.chief}
          selected={selectedId === bands.chief.session.id}
          onSelect={() => onSelectSession(bands.chief!.session.id)}
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
            testIdPrefix="queue-turn"
          />
        ))
      )}
    </div>
  );
}
