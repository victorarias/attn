import './SettlingIndicator.css';
import { CountdownFill } from './CountdownFill';
import { CountdownCancelHint } from './CountdownCancelHint';

/**
 * The auto-settle countdown. The daemon owns the timer and broadcasts only
 * `auto_settle_fires_at`, present exactly while it runs; its absence ends the
 * animation. `auto_settle_held` means frozen with no deadline, drawn as a full
 * still bar — the daemon sends a whole new deadline once the user goes quiet.
 * It drains right-to-left in violet against the nudge bar's left-to-right sky
 * blue, so rows showing both never read as one event.
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
        // The pane header is a leaf-drag handle, so a click that drifts would
        // relocate the pane instead of keeping the turn.
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

/** The sidebar variant, for a session whose tile is not rendered. */
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
