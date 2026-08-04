import './SettlingIndicator.css';
import { CountdownFill } from './CountdownFill';
import { CountdownCancelHint } from './CountdownCancelHint';

/**
 * The auto-settle countdown: attn is about to close this session's turn because
 * the user steered the agent and it went back to work.
 *
 * The daemon owns the timer and broadcasts only the deadline
 * (`auto_settle_fires_at`), present exactly while the countdown is running. The
 * frontend never decides when a turn settles; it renders the deadline it is
 * given, and its absence is what ends the animation.
 *
 * Deliberately not a number. "14, 13, 12…" is something you read; a draining bar
 * is something you notice from across the screen, which is the only way an
 * indicator on a tile you are not focused on does its job.
 *
 * `auto_settle_held` is the same countdown with the user's hands on the keyboard:
 * the daemon has frozen it and sent no deadline, so the bar draws full and still.
 * A full bar rather than a partial one because there is nothing to be partial
 * about — the deadline is gone, and when typing stops the daemon sends a whole new
 * one, which is exactly what a full frozen bar promises.
 *
 * Two homes, so a countdown is never invisible. The pane header is the primary
 * one — the tile is where you are looking when you steered the agent. The sidebar
 * row carries it only for sessions with no rendered tile, since an unwatched
 * session settles just the same and its row is the only thing on screen that
 * represents it. It drains right-to-left in violet against the nudge bar's
 * left-to-right sky blue, so the two never read as the same event on rows that
 * happen to show both.
 */

export function HeaderSettlingIndicator({
  firesAt,
  held,
  onCancel,
}: {
  firesAt?: string;
  held?: boolean;
  onCancel?: () => void;
}) {
  return (
    <>
      <button
        type="button"
        className={held ? 'settling-header settling-header--held' : 'settling-header'}
        // Stop the pane header's pointerdown drag from starting on this button:
        // in a split the header is a leaf-drag handle, so a sloppy click that
        // drifts would relocate the pane instead of keeping the turn.
        onPointerDown={(event) => event.stopPropagation()}
        onClick={(event) => {
          event.stopPropagation();
          onCancel?.();
        }}
        title="Keep this turn"
        aria-label="Keep this turn"
        data-testid="settling-indicator"
      >
        <span className="settling-dot" aria-hidden="true" />
        <span className="settling-header-label">{held ? 'Settling paused' : 'Settling…'}</span>
        <CountdownCancelHint verb="keep" />
      </button>
      <div className="settling-header-track" aria-hidden="true">
        {firesAt ? (
          <CountdownFill firesAt={firesAt} className="settling-header-track-fill" direction="drain" />
        ) : (
          <div className="settling-header-track-fill settling-track-fill--held" />
        )}
      </div>
    </>
  );
}

/**
 * The sidebar variant, for a session whose tile is not rendered. A thin draining
 * bar on the row plus nothing else — the row is small, and the point here is only
 * that a turn about to close somewhere off-screen is not closed silently.
 */
export function SidebarSettlingBar({ firesAt, held }: { firesAt?: string; held?: boolean }) {
  return (
    <div
      className="settling-sidebar-bar"
      aria-hidden="true"
      data-testid="settling-sidebar-bar"
      data-held={held ? 'true' : undefined}
    >
      {firesAt ? (
        <CountdownFill firesAt={firesAt} className="settling-sidebar-bar-fill" direction="drain" />
      ) : (
        <div className="settling-sidebar-bar-fill settling-track-fill--held" />
      )}
    </div>
  );
}
