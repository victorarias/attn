import FocusTrap from 'focus-trap-react';
import type { Ticket } from '../hooks/useDaemonSocket';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { TicketBoardPanel } from './TicketBoardPanel';
import './TicketBoardSurface.css';

export interface TicketBoardSurfaceProps {
  isOpen: boolean;
  tickets: Ticket[];
  onOpenTicket: (ticketId: string) => void;
  onClose: () => void;
}

/**
 * Fullscreen shell for the ticket board. The board is a primary surface (opened
 * from the sidebar / ⌘K), not a right-dock peek, so it gets the whole window —
 * full height for tall columns and room for every flow column without the dock's
 * ~52vw squeeze. Mirrors the notebook surface pattern: fixed inset-0 overlay,
 * FocusTrap, and the shared Escape stack for dismiss.
 */
export function TicketBoardSurface({ isOpen, tickets, onOpenTicket, onClose }: TicketBoardSurfaceProps) {
  useEscapeStack(onClose, isOpen);
  if (!isOpen) return null;
  return (
    <div className="tb-surface-shell" data-testid="ticket-board-surface">
      <FocusTrap
        focusTrapOptions={{
          escapeDeactivates: false,
          clickOutsideDeactivates: false,
          initialFocus: false,
          fallbackFocus: '.tb-surface',
        }}
      >
        <div className="tb-surface" role="dialog" aria-modal="true" aria-label="Tickets board" tabIndex={-1}>
          <TicketBoardPanel isOpen tickets={tickets} onOpenTicket={onOpenTicket} onClose={onClose} />
        </div>
      </FocusTrap>
    </div>
  );
}
