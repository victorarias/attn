import { useState } from 'react';
import './OnAgentMailOverlay.css';

export interface PendingAgentMail {
  unreadCount: number;
  dispatchId: string;
  chiefSessionId: string;
  // wakeable mirrors the dashboard's canWake gate: a local, non-endpoint agent
  // that is idle/waiting with unread mail and a chief that can poke it.
  wakeable: boolean;
}

interface OnAgentMailOverlayProps {
  mail: PendingAgentMail;
  onWake?: (chiefSessionId: string, dispatchId: string) => Promise<void>;
}

// A top-right overlay on an agent pane that has unread chief mail — the attended
// reverse-channel affordance for when a human is driving this agent. Clicking it
// fires the inbox doorbell (a read trigger; message content never enters the PTY).
// It is a sibling visual layer over the terminal: the container is pointer-events:
// none and only the chip is interactive, so it never steals focus from the PTY or
// interferes with fit/resize (Terminal Focus Ownership / PTY geometry rules).
export function OnAgentMailOverlay({ mail, onWake }: OnAgentMailOverlayProps) {
  const [waking, setWaking] = useState(false);
  const [error, setError] = useState<string | null>(null);

  if (mail.unreadCount <= 0) return null;

  const interactive = mail.wakeable && Boolean(onWake);
  const countLabel = `${mail.unreadCount} unread message${mail.unreadCount === 1 ? '' : 's'} from chief`;

  const wake = async () => {
    if (!interactive || !onWake || waking) return;
    setWaking(true);
    setError(null);
    try {
      await onWake(mail.chiefSessionId, mail.dispatchId);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Could not wake agent');
    } finally {
      setWaking(false);
    }
  };

  return (
    <div className="on-agent-mail-overlay" data-testid="on-agent-mail-overlay">
      <button
        type="button"
        className="on-agent-mail-chip"
        disabled={!interactive || waking}
        title={interactive ? `${countLabel} — click to prompt an inbox check` : countLabel}
        aria-label={countLabel}
        // Keep the pane's mousedown (focus/drag) from firing under the chip.
        onMouseDown={(event) => event.stopPropagation()}
        onClick={(event) => {
          event.stopPropagation();
          void wake();
        }}
      >
        <span aria-hidden="true">✉</span>
        <span>{waking ? 'Waking…' : mail.unreadCount}</span>
      </button>
      {error && <span className="on-agent-mail-error">{error}</span>}
    </div>
  );
}
