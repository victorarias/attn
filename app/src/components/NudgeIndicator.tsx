import './NudgeIndicator.css';
import { useEffect, useRef } from 'react';
import type { UISessionState } from '../types/sessionState';

// The visual mode for a session's incoming-ticket indicator, derived from the two
// daemon-broadcast fields plus local selection state. The daemon owns the timer and
// the deadline; the frontend only renders to it.
export type NudgeMode = 'counting' | 'paused' | 'marker';

function isIdleForNudge(state: UISessionState): boolean {
  // Mirrors the daemon's isIdleForNudge: a session is nudge-eligible at rest.
  return state === 'idle' || state === 'waiting_input';
}

// Derive the indicator mode from the daemon fields and local selection.
//
// nudge_fires_at is present IFF the daemon is actively counting down to a doorbell
// (idle + unread + not the selected session). We check active+idle+unread FIRST so a
// just-selected session immediately reads as paused even while a stale fires_at from
// the previous broadcast is still in flight — the daemon pauses the selected session
// and clears fires_at a beat later. The trailing `ticket_unread` branch is the
// catch-all marker, so an unread session never blinks out during the brief post-fire
// transient (the timer entry is gone but the state has not flipped to working yet).
export function deriveNudgeMode(args: {
  ticketUnread?: boolean;
  nudgeFiresAt?: string;
  state: UISessionState;
  isActive: boolean;
}): NudgeMode | null {
  const { ticketUnread, nudgeFiresAt, state, isActive } = args;
  if (ticketUnread && isActive && isIdleForNudge(state)) return 'paused';
  if (nudgeFiresAt) return 'counting';
  if (ticketUnread) return 'marker';
  return null;
}

// A one-shot bar that fills 0 -> 100% over the remaining time to firesAt using a CSS
// transition keyed off the deadline — no per-tick setInterval (which would re-render
// every row every tick). One render, the browser animates the rest. A mid-countdown
// remount restarts the fill from 0 but still completes at the right instant, which is
// fine for a subtle "incoming" bar; we only know the deadline, not the window.
function CountdownFill({ firesAt, className }: { firesAt: string; className: string }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const remainingMs = new Date(firesAt).getTime() - Date.now();
    if (!Number.isFinite(remainingMs) || remainingMs <= 0) {
      el.style.transition = 'none';
      el.style.width = '100%';
      return;
    }
    el.style.transition = 'none';
    el.style.width = '0%';
    // Force a reflow so the width change below actually animates from 0.
    void el.offsetWidth;
    el.style.transition = `width ${remainingMs}ms linear`;
    el.style.width = '100%';
  }, [firesAt]);
  return <div ref={ref} className={className} />;
}

function triggerHandler(onTrigger?: () => void) {
  return (event: React.MouseEvent) => {
    // Stop the row/pane click so triggering the nudge does not also re-select.
    event.stopPropagation();
    onTrigger?.();
  };
}

// The left-sidebar row indicator: a thin bar pinned to the bottom of the session row.
// Counting and marker variants are pointer-events:none so they never steal the row's
// click/drag; the paused variant is a clickable strip (click-to-trigger).
export function SidebarNudgeBar({
  mode,
  firesAt,
  onTrigger,
}: {
  mode: NudgeMode;
  firesAt?: string;
  onTrigger?: () => void;
}) {
  if (mode === 'counting' && firesAt) {
    return (
      <div className="nudge-sidebar-bar" aria-hidden="true">
        <CountdownFill firesAt={firesAt} className="nudge-sidebar-bar-fill" />
      </div>
    );
  }
  if (mode === 'paused') {
    return (
      <button
        type="button"
        className="nudge-sidebar-bar nudge-sidebar-bar--paused"
        onClick={triggerHandler(onTrigger)}
        title="Deliver the pending ticket nudge now"
        aria-label="Deliver the pending ticket nudge now"
      >
        <span className="nudge-sidebar-bar-fill nudge-sidebar-bar-fill--paused" />
      </button>
    );
  }
  return <div className="nudge-sidebar-bar nudge-sidebar-bar--marker" aria-hidden="true" />;
}

// The visible-tile indicator: a semi-transparent strip anchored to the top of the
// pane so it signals incoming activity without obscuring the terminal. Only the
// paused variant's button is interactive.
export function TileNudgeOverlay({
  mode,
  firesAt,
  onTrigger,
}: {
  mode: NudgeMode;
  firesAt?: string;
  onTrigger?: () => void;
}) {
  if (mode === 'counting' && firesAt) {
    return (
      <div className="nudge-tile-overlay nudge-tile-overlay--counting" aria-hidden="true">
        <div className="nudge-tile-bar">
          <CountdownFill firesAt={firesAt} className="nudge-tile-bar-fill" />
        </div>
        <span className="nudge-tile-label">Incoming ticket nudge…</span>
      </div>
    );
  }
  if (mode === 'paused') {
    return (
      <div className="nudge-tile-overlay nudge-tile-overlay--paused">
        <button
          type="button"
          className="nudge-tile-trigger"
          onClick={triggerHandler(onTrigger)}
          title="Deliver the pending ticket nudge now"
        >
          Deliver ticket nudge now
        </button>
      </div>
    );
  }
  return (
    <div className="nudge-tile-overlay nudge-tile-overlay--marker" aria-hidden="true">
      <span className="nudge-tile-label">Unread ticket activity</span>
    </div>
  );
}
