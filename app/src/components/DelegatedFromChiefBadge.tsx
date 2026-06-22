import './DelegatedFromChiefBadge.css';

export function DelegatedFromChiefBadge() {
  return (
    <span
      className="delegated-from-chief-badge"
      title="Delegated from chief of staff"
      aria-label="Delegated from chief of staff"
    >
      <span aria-hidden="true">↳</span>
    </span>
  );
}
