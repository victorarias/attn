import type { CSSProperties, ReactNode } from 'react';
import './SidePanel.css';

interface SidePanelProps {
  isOpen: boolean;
  position?: 'absolute' | 'fixed';
  tone?: 'default' | 'idle' | 'running' | 'awaiting_user' | 'completed' | 'stopped' | 'error';
  width?: string;
  offsetRight?: string;
  className?: string;
  children: ReactNode;
}

export function SidePanel({
  isOpen,
  position = 'absolute',
  tone = 'default',
  width,
  offsetRight,
  className = '',
  children,
}: SidePanelProps) {
  const style = {
    ...(width ? { ['--side-panel-width' as string]: width } : {}),
    ...(offsetRight ? { ['--side-panel-offset' as string]: offsetRight } : {}),
  } as CSSProperties | undefined;

  return (
    <div className={`side-panel-shell side-panel-shell--${position} ${isOpen ? 'is-open' : 'is-closed'}`}>
      <aside
        className={`side-panel side-panel--${tone} ${className}`.trim()}
        style={style}
        aria-hidden={!isOpen}
      >
        {children}
      </aside>
    </div>
  );
}
