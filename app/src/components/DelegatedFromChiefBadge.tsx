import './DelegatedFromChiefBadge.css';

interface DelegatedFromChiefBadgeProps {
  compact?: boolean;
}

export function DelegatedFromChiefBadge({ compact = false }: DelegatedFromChiefBadgeProps) {
  return (
    <span
      className={`delegated-from-chief-badge ${compact ? 'compact' : ''}`}
      title="Delegated from chief of staff"
      aria-label="Delegated from chief of staff"
    >
      <span aria-hidden="true">↳</span>
      {!compact && <span>chief</span>}
    </span>
  );
}
