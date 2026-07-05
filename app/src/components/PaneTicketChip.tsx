import './PaneTicketChip.css';
import type { Ticket } from '../hooks/useDaemonSocket';

// A clickable chip in the agent pane header (.workspace-pane-header) for the
// ticket bound to that session. It carries a status-tinted rule (via
// data-status, reusing the board's DEFINED-token tint pattern — no bare
// --color-accent/--color-error), the truncated ticket title, and an unread dot
// when the ticket has activity the user has not seen. Clicking it toggles the
// in-pane ticket overlay.
//
// Like the sibling rename/nudge buttons it guards the pane header's leaf-drag:
// onPointerDown stops propagation so a sloppy click that drifts >=4px cannot
// relocate the pane instead of opening the ticket, and onClick stops the header
// pointer chain from re-selecting the pane.
export function PaneTicketChip({
  ticket,
  unread,
  open,
  sessionId,
  onToggle,
}: {
  ticket: Ticket;
  unread: boolean;
  open: boolean;
  sessionId: string;
  onToggle: () => void;
}) {
  return (
    <button
      type="button"
      className="pane-ticket-chip"
      data-status={ticket.status}
      data-testid={`ticket-chip-${sessionId}`}
      aria-expanded={open}
      aria-pressed={open}
      onPointerDown={(event) => event.stopPropagation()}
      onClick={(event) => {
        event.stopPropagation();
        onToggle();
      }}
      title={`${ticket.title} — click to open the ticket`}
    >
      <span className="pane-ticket-chip-rule" aria-hidden="true" />
      <span className="pane-ticket-chip-title">{ticket.title}</span>
      {unread ? (
        <span
          className="pane-ticket-chip-unread"
          data-testid={`ticket-chip-unread-${sessionId}`}
          aria-label="Unread ticket activity"
        />
      ) : null}
    </button>
  );
}
