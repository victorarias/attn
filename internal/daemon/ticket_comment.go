package daemon

import (
	"encoding/json"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// handleTicketComment posts a one-shot comment from the calling agent onto any
// ticket by id. Unlike handleSetTicketStatus, the ticket is caller-supplied (an
// agent can comment on a ticket it is not assigned to), so the daemon does not
// resolve it from the session — but it still authors the comment as the session,
// the same identity the session uses everywhere else. The comment informs the
// ticket's participants without enrolling the commenter (comment authorship is
// not a participation source — see store.UnreadTicketEvents), so a passing note
// does not subscribe the agent to the ticket's future activity.
func (d *Daemon) handleTicketComment(conn net.Conn, msg *protocol.TicketCommentMessage) {
	sourceSessionID := strings.TrimSpace(msg.SourceSessionID)
	if sourceSessionID == "" {
		d.sendError(conn, "ticket comment: source_session_id is required")
		return
	}
	ticketID := strings.TrimSpace(msg.TicketID)
	if ticketID == "" {
		d.sendError(conn, "ticket comment: ticket_id is required")
		return
	}
	comment := strings.TrimSpace(msg.Comment)
	if comment == "" {
		d.sendError(conn, "ticket comment: comment is required")
		return
	}
	// AddTicketComment fails with ErrTicketNotFound when the id does not exist
	// (touchTicketTx affects no rows), so an agent naming a bad ticket gets a clear
	// error rather than a silently dropped comment.
	d.deliveryMu.Lock()
	_, outcome, err := d.store.AddTicketCommentWithOptions(
		ticketID, sourceSessionID, comment,
		d.ticketMutationOptions(sourceSessionID), time.Now(),
	)
	if err != nil {
		d.deliveryMu.Unlock()
		d.sendError(conn, "ticket comment: "+err.Error())
		return
	}
	result := &protocol.TicketCommentResult{
		TicketID: ticketID,
		CatchUp:  ticketMutationCatchUp(ticketID, outcome.ConflictEvents),
	}
	if len(outcome.ConflictEvents) > 0 {
		d.afterTicketMutationCatchUpLocked(sourceSessionID, outcome.ConflictEvents)
		d.deliveryMu.Unlock()
		_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, TicketCommentResult: result})
		return
	}
	d.deliveryMu.Unlock()
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, TicketCommentResult: result})
	// The comment is an event the ticket's participants did not author, so notify
	// them (the assignee, the chief). The commenter authored it, so Notify excludes
	// it — no self-nudge — and comment authorship does not make the commenter a
	// participant of the ticket going forward.
	d.notifyTicketObservers(ticketID)
	// Refresh the app's board view: the activity thread changed.
	d.broadcastTicketsUpdated()
}
