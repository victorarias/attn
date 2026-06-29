package daemon

import (
	"encoding/json"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// handleTicketTake claims a ticket for the calling session, making it the
// assignee. Like comment/subscribe, the ticket is caller-supplied (an agent can
// take a ticket it is not currently assigned to), and the daemon authors the
// reassignment as the session. Guards:
//
//   - taking a ticket already assigned to someone else requires confirm=true, so
//     an agent cannot silently steal another's active work;
//   - taking a ticket already assigned to the caller is a no-op (no redundant
//     assigned event), reported successfully so a retry is harmless.
//
// Take does NOT advance the taker's cursor: the taker is picking up work it has
// not seen, so its first `attn ticket inbox` delivers the ticket's history (the
// same freshly-assigned ⇒ deliver-from-start rule delegation's spawn-prompt is
// the deliberate exception to). The previous assignee is notified of the takeover
// through the `assigned` event reaching them as a participant — they remain one
// via the status events they authored while working. (Edge case: a previous
// assignee that was assigned but never reported authored no event, so it is no
// longer a participant once it loses the assignee slot and will not be nudged;
// that is acceptable — it was never visibly working.)
func (d *Daemon) handleTicketTake(conn net.Conn, msg *protocol.TicketTakeMessage) {
	sourceSessionID := strings.TrimSpace(msg.SourceSessionID)
	if sourceSessionID == "" {
		d.sendError(conn, "ticket take: source_session_id is required")
		return
	}
	ticketID := strings.TrimSpace(msg.TicketID)
	if ticketID == "" {
		d.sendError(conn, "ticket take: ticket_id is required")
		return
	}
	ticket, err := d.store.GetTicket(ticketID)
	if err != nil {
		d.sendError(conn, "ticket take: "+err.Error())
		return
	}
	if ticket == nil {
		d.sendError(conn, "ticket take: ticket "+ticketID+" not found")
		return
	}
	previous := ticket.Assignee
	// Already mine: nothing to reassign, and appending another assigned event would
	// be noise. Report success so a redundant take is a harmless retry.
	if previous == sourceSessionID {
		_ = json.NewEncoder(conn).Encode(protocol.Response{
			Ok:               true,
			TicketTakeResult: &protocol.TicketTakeResult{TicketID: ticketID, PreviousAssignee: previous},
		})
		return
	}
	confirm := msg.Confirm != nil && *msg.Confirm
	if previous != "" && !confirm {
		d.sendError(conn, "ticket take: ticket "+ticketID+" is already assigned to "+previous+"; pass --confirm to take it over")
		return
	}
	if err := d.store.AssignTicket(ticketID, sourceSessionID, sourceSessionID, time.Now()); err != nil {
		d.sendError(conn, "ticket take: "+err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:               true,
		TicketTakeResult: &protocol.TicketTakeResult{TicketID: ticketID, PreviousAssignee: previous},
	})
	// The assigned event was authored by the taker, so notifyTicketObservers
	// excludes it (no self-nudge) and fans out to the ticket's other participants —
	// the previous assignee and the chief.
	d.notifyTicketObservers(ticketID)
	// The board's assignee column changed; refresh the app view.
	d.broadcastTicketsUpdated()
}
