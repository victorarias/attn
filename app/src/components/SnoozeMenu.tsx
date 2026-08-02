import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { SNOOZE_CHOICES, snoozeInstant, type SnoozeChoiceId } from '../utils/snoozeDurations';
import './SnoozeMenu.css';

interface SnoozeMenuProps {
  sessionLabel: string;
  anchor: { top: number; left: number };
  onSnooze: (until: Date) => void;
  onClose: () => void;
}

const VIEWPORT_MARGIN = 8;

/**
 * The durations a turn can be deferred by.
 *
 * Each row says the concrete time it resolves to as well as the duration: "Until
 * Monday" is a promise about when you will be interrupted, and a menu that will
 * not name the moment is one you have to test before you trust it.
 *
 * `now` is frozen when the menu opens, so the instant the user sees beside a row
 * is the instant that gets sent — a menu left open across a minute boundary must
 * not send something a minute later than it showed.
 */
export function SnoozeMenu({ sessionLabel, anchor, onSnooze, onClose }: SnoozeMenuProps) {
  const menuRef = useRef<HTMLDivElement>(null);
  const [position, setPosition] = useState(anchor);
  // Lazily, so the Date is built once when the menu opens rather than on every
  // render and thrown away — the frozen instant is the point, and a rebuilt one
  // would be the very thing this ref exists to avoid if useRef ever took the
  // second value.
  const openedAtRef = useRef<Date | null>(null);
  if (openedAtRef.current === null) openedAtRef.current = new Date();
  const openedAt = openedAtRef.current;

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

  const choose = (id: SnoozeChoiceId) => {
    onClose();
    onSnooze(snoozeInstant(id, openedAt));
  };

  return (
    <div
      ref={menuRef}
      className="snooze-menu"
      style={{ top: position.top, left: position.left }}
      role="menu"
      aria-label={`Snooze ${sessionLabel}`}
      data-testid="snooze-menu"
    >
      <div className="snooze-menu-header">Snooze {sessionLabel}</div>
      {SNOOZE_CHOICES.map((choice) => (
        <button
          key={choice.id}
          type="button"
          role="menuitem"
          data-testid={`snooze-choice-${choice.id}`}
          onClick={() => choose(choice.id)}
        >
          <span className="snooze-menu-label">{choice.label}</span>
          <span className="snooze-menu-detail">{choice.detail(openedAt)}</span>
        </button>
      ))}
    </div>
  );
}
