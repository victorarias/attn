import type { Ticket } from '../hooks/useDaemonSocket';

// The single home for the session -> bound-ticket rule: a ticket is bound to a
// session when it is that session's assignee. Store rows are non-archived and
// ordered created_at desc, so if a session has been assigned more than once
// (e.g. after a `take`) this resolves to the newest binding.
//
// Extracted from useUiAutomationBridge's `ticket_open_via_dashboard`, which now
// reuses it, so the pane chip and the automation bridge resolve identically.
export function boundTicketForSession(
  tickets: Ticket[],
  sessionId: string,
): Ticket | undefined {
  return tickets.find((ticket) => ticket.assignee === sessionId);
}
