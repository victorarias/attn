package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// ticketStatusFromWorkState maps the work state an agent reports onto the ticket
// column its bound ticket should move to. This is the agent's forward channel:
// the agent describes its own state, attn projects that onto the board. `crashed`
// and `todo` are intentionally unreachable here — crashed is attn-authored when an
// agent dies without reporting, and todo is the pre-assignment backlog state.
func ticketStatusFromWorkState(ws protocol.DispatchWorkState) (store.TicketStatus, bool) {
	switch ws {
	case protocol.DispatchWorkStateInProgress:
		return store.TicketStatusWorking, true
	case protocol.DispatchWorkStateNeedsInput:
		return store.TicketStatusBlocked, true
	case protocol.DispatchWorkStateReadyForReview:
		return store.TicketStatusInReview, true
	case protocol.DispatchWorkStateCompleted:
		return store.TicketStatusDone, true
	case protocol.DispatchWorkStateFailed:
		return store.TicketStatusFailed, true
	default:
		return "", false
	}
}

// handleSetTicketStatus moves the calling agent's bound ticket to the column
// implied by the work state it reports. The session is the assignee, so the
// daemon resolves the ticket from the session rather than trusting a
// caller-supplied id — an agent can only move its own active ticket. The reported
// status is recorded with the agent as author so the change reads as
// self-reported on the activity thread.
func (d *Daemon) handleSetTicketStatus(conn net.Conn, msg *protocol.SetTicketStatusMessage) {
	sourceSessionID := strings.TrimSpace(msg.SourceSessionID)
	if sourceSessionID == "" {
		d.sendError(conn, "ticket status: source_session_id is required")
		return
	}
	status, ok := ticketStatusFromWorkState(msg.WorkState)
	if !ok {
		d.sendError(conn, fmt.Sprintf("ticket status: unknown work state %q", msg.WorkState))
		return
	}
	ticket, err := d.store.ActiveTicketForSession(sourceSessionID)
	if err != nil {
		d.sendError(conn, "ticket status: "+err.Error())
		return
	}
	if ticket == nil {
		d.sendError(conn, "ticket status: no active ticket bound to this session")
		return
	}
	comment := ""
	if msg.Comment != nil {
		comment = strings.TrimSpace(*msg.Comment)
	}
	updated, err := d.store.SetTicketStatus(ticket.ID, status, sourceSessionID, comment, time.Now())
	if err != nil {
		d.sendError(conn, "ticket status: "+err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok: true,
		TicketStatusResult: &protocol.TicketStatusResult{
			TicketID: updated.ID,
			Status:   protocol.TicketStatus(updated.Status),
		},
	})
	// The agent moved its own ticket; notify the other observers (the chief) so the
	// board→watcher direction reflects it. The agent itself authored the event, so
	// Notify excludes it — no self-nudge.
	d.notifyTicketObservers(updated.ID)
}
