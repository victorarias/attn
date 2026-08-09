import './NudgeIndicator.css';
import { CountdownFill } from './CountdownFill';
import { CountdownCancelHint } from './CountdownCancelHint';
import type { UISessionState } from '../types/sessionState';

// The visual mode for a session's incoming-ticket indicator. The daemon owns the
// timer and the deadline; the frontend only renders to it.
export type NudgeMode = 'counting' | 'paused' | 'marker';

function isDeliverableForNudge(state: UISessionState): boolean {
  // Mirrors the daemon's isExplicitNudgeBlocked: a click delivers on demand in
  // every state but pending_approval, where the doorbell's trailing Enter could
  // answer the y/n prompt.
  return state !== 'pending_approval';
}

// Order matters. Active+deliverable+unread is checked first so the session the
// user is looking at reads as the clickable paused chip even while a stale
// fires_at is still in flight. The trailing `ticket_unread` branch is the
// catch-all marker, including the brief post-fire transient, so the chip never
// blinks out.
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

function triggerHandler(onTrigger?: () => void) {
  return (event: React.MouseEvent) => {
    event.stopPropagation();
    onTrigger?.();
  };
}

// The sidebar row indicator. Counting and marker variants are pointer-events:none
// so they never steal the row's click/drag; the paused variant is clickable.
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
        // The session row is a drag handle, so a click that drifts would start a
        // session drag instead of firing.
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

// The pane-header chip. Counting returns a fragment so the chip and its progress
// track both land as direct header children and the track spans the pane width.
export function HeaderNudgeIndicator({
  mode,
  firesAt,
  onTrigger,
  onCancel,
}: {
  mode: NudgeMode;
  firesAt?: string;
  onTrigger?: () => void;
  onCancel?: () => void;
}) {
  if (mode === 'counting' && firesAt) {
    // A button: one countdown-cancel verb, whichever countdown is on screen, so
    // it carries the same key as the settling chip.
    return (
      <>
        <button
          type="button"
          className="nudge-header nudge-header--counting nudge-header-trigger"
          // The pane header is a leaf-drag handle; see the paused variant.
          onPointerDown={(event) => event.stopPropagation()}
          onClick={triggerHandler(onCancel)}
          title="Stop this nudge — the ticket stays unread"
        >
          <span className="nudge-dot" aria-hidden="true" />
          <span className="nudge-header-label">Incoming ticket nudge…</span>
          <CountdownCancelHint verb="stop" />
        </button>
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
        // The pane header is a leaf-drag handle (beginLeafDrag), so a click that
        // drifts >=4px would relocate the pane instead of delivering the nudge.
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
