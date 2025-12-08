// app/src/components/DrawerTrigger.tsx
import './DrawerTrigger.css';

interface DrawerTriggerProps {
  count: number;
  onClick: () => void;
}

export function DrawerTrigger({ count, onClick }: DrawerTriggerProps) {
  if (count === 0) return null;

  return (
    <button className="drawer-trigger" onClick={onClick}>
      <span className="trigger-count">{count}</span>
      <span className="trigger-label">need attention</span>
    </button>
  );
}
