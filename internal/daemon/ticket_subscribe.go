package daemon

import (
	"encoding/json"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// handleTicketSubscribe opts the calling session into a ticket's notifications —
// an explicit, opt-in standing interest in a ticket it is not assigned to. The
// daemon authors the subscription as the session, the same identity it uses
// everywhere else. Subscribing is silent: it appends no event and changes no board
// row, so there is nothing to notify others about. It also does not advance the
// subscriber's cursor, so the subscriber's first `attn ticket inbox` after this
// delivers the ticket's history; future events reach it because it is now a
// participant (see store.TicketParticipants).
func (d *Daemon) handleTicketSubscribe(conn net.Conn, msg *protocol.TicketSubscribeMessage) {
	sourceSessionID := strings.TrimSpace(msg.SourceSessionID)
	if sourceSessionID == "" {
		d.sendError(conn, "ticket subscribe: source_session_id is required")
		return
	}
	ticketID := strings.TrimSpace(msg.TicketID)
	if ticketID == "" {
		d.sendError(conn, "ticket subscribe: ticket_id is required")
		return
	}
	// AddTicketSubscription fails with ErrTicketNotFound for an unknown id, so an
	// agent subscribing to a phantom ticket gets a clear error rather than a silent
	// no-op. Re-subscribing is idempotent.
	if err := d.store.AddTicketSubscription(sourceSessionID, ticketID, time.Now()); err != nil {
		d.sendError(conn, "ticket subscribe: "+err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok: true,
		TicketSubscribeResult: &protocol.TicketSubscribeResult{
			TicketID:    ticketID,
			UnreadCount: protocol.Ptr(d.targetTicketUnreadCount(sourceSessionID, ticketID)),
		},
	})
}

// handleTicketUnsubscribe opts the calling session back out of a ticket's
// notifications. It is a pure idempotent removal — opting out when not subscribed
// (or when the ticket has since been swept) succeeds — so it does not require the
// ticket to exist. Like subscribe, it is silent.
func (d *Daemon) handleTicketUnsubscribe(conn net.Conn, msg *protocol.TicketUnsubscribeMessage) {
	sourceSessionID := strings.TrimSpace(msg.SourceSessionID)
	if sourceSessionID == "" {
		d.sendError(conn, "ticket unsubscribe: source_session_id is required")
		return
	}
	ticketID := strings.TrimSpace(msg.TicketID)
	if ticketID == "" {
		d.sendError(conn, "ticket unsubscribe: ticket_id is required")
		return
	}
	if err := d.store.RemoveTicketSubscription(sourceSessionID, ticketID); err != nil {
		d.sendError(conn, "ticket unsubscribe: "+err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:                      true,
		TicketUnsubscribeResult: &protocol.TicketUnsubscribeResult{TicketID: ticketID},
	})
}
