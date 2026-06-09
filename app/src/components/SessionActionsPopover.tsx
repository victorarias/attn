import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useEscapeStack } from '../hooks/useEscapeStack';
import './SessionActionsPopover.css';

interface SessionActionsPopoverProps {
  sessionLabel: string;
  chiefOfStaff: boolean;
  anchor: { top: number; left: number };
  canRename: boolean;
  onRename: () => void;
  onChangeChiefOfStaff: (enabled: boolean) => void;
  onCloseSession: () => void;
  onReloadSession: () => void;
  onClose: () => void;
}

const VIEWPORT_MARGIN = 8;

export function SessionActionsPopover({
  sessionLabel,
  chiefOfStaff,
  anchor,
  canRename,
  onRename,
  onChangeChiefOfStaff,
  onCloseSession,
  onReloadSession,
  onClose,
}: SessionActionsPopoverProps) {
  const menuRef = useRef<HTMLDivElement>(null);
  const [position, setPosition] = useState(anchor);

  useEscapeStack(onClose, true);

  useLayoutEffect(() => {
    const menu = menuRef.current;
    if (!menu) return;
    const rect = menu.getBoundingClientRect();
    setPosition({
      top: Math.max(
        VIEWPORT_MARGIN,
        Math.min(anchor.top, window.innerHeight - rect.height - VIEWPORT_MARGIN),
      ),
      left: Math.max(
        VIEWPORT_MARGIN,
        Math.min(anchor.left, window.innerWidth - rect.width - VIEWPORT_MARGIN),
      ),
    });
  }, [anchor]);

  useEffect(() => {
    const handleMouseDown = (event: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(event.target as Node)) {
        onClose();
      }
    };
    const id = window.setTimeout(() => document.addEventListener('mousedown', handleMouseDown), 0);
    return () => {
      window.clearTimeout(id);
      document.removeEventListener('mousedown', handleMouseDown);
    };
  }, [onClose]);

  const run = (action: () => void) => {
    onClose();
    action();
  };

  return (
    <div
      ref={menuRef}
      className="session-actions-popover"
      style={{ top: position.top, left: position.left }}
      role="menu"
      aria-label={`Actions for ${sessionLabel}`}
    >
      {canRename && (
        <button type="button" role="menuitem" data-testid="rename-session-action" onClick={() => run(onRename)}>
          <span aria-hidden="true">✎</span>
          Rename session
        </button>
      )}
      <button
        type="button"
        role="menuitem"
        className="chief-of-staff-action"
        data-testid="chief-of-staff-session-action"
        onClick={() => run(() => onChangeChiefOfStaff(!chiefOfStaff))}
      >
        <span aria-hidden="true">⌁</span>
        {chiefOfStaff ? 'Remove chief role' : 'Make chief of staff'}
      </button>
      <div className="session-actions-divider" />
      <button type="button" role="menuitem" data-testid="reload-session-action" onClick={() => run(onReloadSession)}>
        <span aria-hidden="true">↻</span>
        Reload session
      </button>
      <button type="button" role="menuitem" data-testid="close-session-action" onClick={() => run(onCloseSession)}>
        <span aria-hidden="true">×</span>
        Close session
      </button>
    </div>
  );
}
