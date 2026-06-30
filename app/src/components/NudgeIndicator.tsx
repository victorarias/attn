import './NudgeIndicator.css';
import { useEffect, useRef } from 'react';
import type { UISessionState } from '../types/sessionState';

// The visual mode for a session's incoming-ticket indicator, derived from the two
// daemon-broadcast fields plus local selection state. The daemon owns the timer and
// the deadline; the frontend only renders to it.
export type NudgeMode = 'counting' | 'paused' | 'marker';

function isDeliverableForNudge(state: UISessionState): boolean {
  // Mirrors the daemon's isExplicitNudgeBlocked: an explicit click delivers a doorbell
  // on demand in every state EXCEPT pending_approval. A click is unambiguous intent, so
  // — unlike the idle-gated auto-countdown — it honors working, launching, scheduled,
  // and 'unknown' (codex's common resting state, where no countdown ever arms). The lone
  // exception is pending_approval: the doorbell's trailing Enter could answer the y/n
  // prompt, so the chip there is a static, non-clickable marker.
  return state !== 'pending_approval';
}

// Derive the indicator mode from the daemon fields and local selection.
//
// nudge_fires_at is present IFF the daemon is actively counting down to a doorbell
// (idle + unread + not the selected session). We check active+deliverable+unread FIRST
// so the session the user is looking at always reads as the clickable paused chip —
// "deliver on demand" — in every state except pending_approval (working, launching,
// 'unknown', scheduled all qualify), and so a just-selected idle session reads as paused
// even while a stale fires_at from the previous broadcast is still in flight (the daemon
// pauses the selected session and clears fires_at a beat later). The trailing
// `ticket_unread` branch is the catch-all marker: it covers the non-clickable
// pending_approval case, off-screen unread sessions that aren't counting, and the brief
// post-fire transient (timer entry gone, state not yet flipped) so the chip never blinks
// out.
export function deriveNudgeMode(args: {
  ticketUnread?: boolean;
  nudgeFiresAt?: string;
  state: UISessionState;
  isActive: boolean;
}): NudgeMode | null {
  const { ticketUnread, nudgeFiresAt, state, isActive } = args;
  if (ticketUnread && isActive && isDeliverableForNudge(state)) return 'paused';
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
      <div className="nudge-sidebar-bar nudge-sidebar-bar--counting" aria-hidden="true">
        <CountdownFill firesAt={firesAt} className="nudge-sidebar-bar-fill" />
      </div>
    );
  }
  if (mode === 'paused') {
    return (
      <button
        type="button"
        className="nudge-sidebar-bar nudge-sidebar-bar--paused"
        // Stop the row's pointerdown drag from arming on a press of this button —
        // the session row is a drag handle (handleSessionPointerDown) and a sloppy
        // click that drifts would otherwise start a session drag instead of firing.
        onPointerDown={(event) => event.stopPropagation()}
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

// The visible-pane indicator: an inline chip rendered inside the pane header
// (.workspace-pane-header), which the workspace surfaces on a single tile precisely
// when a session has unread ticket activity. The header is the rectangle; this is its
// right-aligned content. Only the paused variant's chip is an interactive button.
//
// Counting returns a fragment: the inline chip plus a sibling progress track that the
// header (position:relative) pins to its bottom edge — so the fragment's children land
// as direct header children and the track spans the full pane width.
export function HeaderNudgeIndicator({
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
      <>
        <div className="nudge-header nudge-header--counting" aria-hidden="true">
          <span className="nudge-dot" aria-hidden="true" />
          <span className="nudge-header-label">Incoming ticket nudge…</span>
        </div>
        <div className="nudge-header-track" aria-hidden="true">
          <CountdownFill firesAt={firesAt} className="nudge-header-track-fill" />
        </div>
      </>
    );
  }
  if (mode === 'paused') {
    return (
      <button
        type="button"
        className="nudge-header nudge-header--paused nudge-header-trigger"
        // Stop the pane header's pointerdown drag from starting on this button. In a
        // split the header is a leaf-drag handle (beginLeafDrag), so without this a
        // sloppy click that drifts >=4px would relocate the pane instead of delivering
        // the nudge — exactly as the sibling rename button guards itself.
        onPointerDown={(event) => event.stopPropagation()}
        onClick={triggerHandler(onTrigger)}
        title="Deliver the pending ticket nudge now"
      >
        <span className="nudge-dot" aria-hidden="true" />
        <span className="nudge-header-label">Deliver ticket nudge now</span>
      </button>
    );
  }
  return (
    <div className="nudge-header nudge-header--marker" aria-hidden="true">
      <span className="nudge-dot" aria-hidden="true" />
      <span className="nudge-header-label">Unread ticket activity</span>
    </div>
  );
}
