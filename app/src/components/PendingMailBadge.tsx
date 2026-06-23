import './PendingMailBadge.css';

interface PendingMailBadgeProps {
  count: number;
  // compact renders the icon-only dot variant for the collapsed rail.
  compact?: boolean;
}

// A per-session chip flagging unread chief→agent mail, so a human sees pending
// mail in the sidebar where they already look — no dashboard trip required. It is
// the reverse-channel sibling of the chief / delegated-from-chief badges.
export function PendingMailBadge({ count, compact = false }: PendingMailBadgeProps) {
  if (count <= 0) return null;
  const label = `${count} unread message${count === 1 ? '' : 's'} from chief`;
  return (
    <span
      className={`pending-mail-badge ${compact ? 'compact' : ''}`.trim()}
      title={label}
      aria-label={label}
    >
      <span aria-hidden="true">✉</span>
      {!compact && <span>{count}</span>}
    </span>
  );
}
