import './CountdownCancelHint.css';
import { formatShortcut } from '../shortcuts/formatShortcut';

/**
 * The key that stops a running countdown, rendered inside the countdown's own
 * indicator.
 *
 * A countdown is attn about to do something the user did not ask for, so the way
 * to call it off has to be on the thing that is counting — not in a tooltip, and
 * not only in the cheatsheet. Reading a pill should never leave you hunting for
 * how to stop what it is announcing.
 *
 * The combo comes from the registry through `formatShortcut`, so a rebind moves
 * what every indicator says with it. `verb` is what stopping means for this
 * particular countdown ("keep" a turn, "stop" a nudge) — the key is shared, the
 * consequence is not.
 */
export function CountdownCancelHint({ verb }: { verb: string }) {
  const combo = formatShortcut('session.cancelCountdown');
  if (!combo) return null;
  return (
    <span className="countdown-cancel-hint">
      <kbd className="countdown-cancel-hint-key">{combo}</kbd>
      <span className="countdown-cancel-hint-verb">{verb}</span>
    </span>
  );
}
