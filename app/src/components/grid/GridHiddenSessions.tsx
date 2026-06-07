// The grid's "restore" affordance. Sessions removed from the grid still exist —
// this top-right control surfaces how many are hidden and lets the user put any
// of them back. It renders nothing when nothing is hidden.
import { useEffect, useRef, useState } from 'react';

export interface HiddenGridSession {
  sessionId: string;
  title: string;
}

interface GridHiddenSessionsProps {
  sessions: HiddenGridSession[];
  onRestore: (sessionId: string) => void;
}

export function GridHiddenSessions({ sessions, onRestore }: GridHiddenSessionsProps) {
  const [open, setOpen] = useState(false);
  const anchorRef = useRef<HTMLDivElement | null>(null);

  // Close on outside click / Escape.
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (anchorRef.current && !anchorRef.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation();
        setOpen(false);
      }
    };
    window.addEventListener('mousedown', onDown, true);
    window.addEventListener('keydown', onKey, true);
    return () => {
      window.removeEventListener('mousedown', onDown, true);
      window.removeEventListener('keydown', onKey, true);
    };
  }, [open]);

  // Nothing hidden → no control. Also collapse the popover if the last hidden
  // session was just restored.
  if (sessions.length === 0) return null;

  return (
    <div className="grid-hidden" ref={anchorRef}>
      <button
        type="button"
        className={`grid-hidden-toggle ${open ? 'active' : ''}`}
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
      >
        {sessions.length} hidden
      </button>
      {open && (
        <div className="grid-hidden-popover" role="dialog" aria-label="Hidden sessions">
          <span className="grid-hidden-label">Hidden from grid</span>
          <ul className="grid-hidden-list">
            {sessions.map((s) => (
              <li key={s.sessionId}>
                <button
                  type="button"
                  className="grid-hidden-restore"
                  onClick={() => onRestore(s.sessionId)}
                  title={`Restore ${s.title}`}
                >
                  <span className="grid-hidden-name">{s.title}</span>
                  <span className="grid-hidden-action">Restore</span>
                </button>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
