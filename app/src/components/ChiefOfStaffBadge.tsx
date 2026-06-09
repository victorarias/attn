import './ChiefOfStaffBadge.css';

interface ChiefOfStaffBadgeProps {
  compact?: boolean;
}

export function ChiefOfStaffBadge({ compact = false }: ChiefOfStaffBadgeProps) {
  return (
    <span
      className={`chief-of-staff-badge ${compact ? 'compact' : ''}`}
      title="Chief of staff"
      aria-label="Chief of staff"
    >
      <span aria-hidden="true">⌁</span>
      {!compact && <span>chief</span>}
    </span>
  );
}
