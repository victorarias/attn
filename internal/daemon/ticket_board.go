package daemon

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// The board read side of the work tracker (slice 4a). The app never speaks the
// agent's ticket verbs; it observes the board the way it observes dispatches —
// a snapshot in initial_state plus a tickets_updated broadcast on every mutation
// (the push), and a get_ticket request/result for the full detail of one row it
// clicked (the pull). The push carries bare rows (no activity/attachments) so a
// busy board stays cheap to broadcast; the detail fetch loads the full record.

// ticketsForBroadcast is the live board feed: every non-archived ticket as a bare
// wire row (activity/attachments empty), newest first. It is the payload of both
// the initial_state snapshot and each tickets_updated broadcast, so a client
// renders the board identically from either.
func (d *Daemon) ticketsForBroadcast() []protocol.Ticket {
	if d.store == nil {
		return nil
	}
	rows, err := d.store.ListTickets(store.TicketListFilter{})
	if err != nil {
		d.logf("list tickets for broadcast: %v", err)
		return nil
	}
	out := make([]protocol.Ticket, 0, len(rows))
	for _, t := range rows {
		if t != nil {
			out = append(out, ticketToProtocol(t))
		}
	}
	return out
}

// broadcastTicketsUpdated re-pushes the whole non-archived board to every client.
// Run it after any producer that mutates a ticket (create, status, crash, and the
// chief edits to come), mirroring broadcastChiefOfStaffDispatchesUpdated.
func (d *Daemon) broadcastTicketsUpdated() {
	if d.wsHub == nil || d.store == nil {
		return
	}
	d.broadcastMessage(&protocol.TicketsUpdatedMessage{
		Event:   protocol.EventTicketsUpdated,
		Tickets: d.ticketsForBroadcast(),
	})
}

// sendGetTicketWSResult replies to a get_ticket request with the ticket's full
// record (row + activity thread + attachments), correlated by requestID. An
// unknown id is a failed result (error set, ticket omitted), not a panic — the
// app may ask for a ticket the TTL sweep removed between board push and click.
func (d *Daemon) sendGetTicketWSResult(client *wsClient, requestID, ticketID string) {
	msg := protocol.TicketResultMessage{
		Event:     protocol.EventTicketResult,
		RequestID: requestID,
	}
	ticket, err := d.store.GetTicket(ticketID)
	switch {
	case err != nil:
		msg.Error = protocol.Ptr(err.Error())
	case ticket == nil:
		msg.Error = protocol.Ptr("ticket not found: " + ticketID)
	default:
		full := ticketToProtocol(ticket)
		msg.Success = true
		msg.Ticket = &full
	}
	d.sendToClient(client, msg)
}

// ticketToProtocol maps a store ticket to its wire shape. Activity and attachments
// are mapped when present (the GetTicket full record) and become empty slices when
// the store left them nil (a ListTickets bare row), so the wire field is always a
// JSON array, never null.
func ticketToProtocol(t *store.Ticket) protocol.Ticket {
	pt := protocol.Ticket{
		ID:          t.ID,
		Title:       t.Title,
		Description: t.Description,
		Status:      protocol.TicketStatus(t.Status),
		Assignee:    t.Assignee,
		Cwd:         t.Cwd,
		LastAgentID: t.LastAgentID,
		ProjectID:   t.ProjectID,
		CreatedAt:   t.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   t.UpdatedAt.Format(time.RFC3339),
		Activity:    make([]protocol.TicketActivity, 0, len(t.Activity)),
		Attachments: make([]protocol.TicketAttachment, 0, len(t.Attachments)),
	}
	if t.ClosedAt != nil {
		pt.ClosedAt = protocol.Ptr(t.ClosedAt.Format(time.RFC3339))
	}
	if t.ArchivedAt != nil {
		pt.ArchivedAt = protocol.Ptr(t.ArchivedAt.Format(time.RFC3339))
	}
	for _, a := range t.Activity {
		pt.Activity = append(pt.Activity, ticketActivityToProtocol(a))
	}
	for _, att := range t.Attachments {
		pt.Attachments = append(pt.Attachments, ticketAttachmentToProtocol(att))
	}
	return pt
}

func ticketActivityToProtocol(a store.TicketActivity) protocol.TicketActivity {
	pa := protocol.TicketActivity{
		ID:        int(a.ID),
		Kind:      protocol.TicketActivityKind(a.Kind),
		Author:    a.Author,
		CreatedAt: a.CreatedAt.Format(time.RFC3339),
	}
	if a.FromStatus != "" {
		pa.FromStatus = protocol.Ptr(protocol.TicketStatus(a.FromStatus))
	}
	if a.ToStatus != "" {
		pa.ToStatus = protocol.Ptr(protocol.TicketStatus(a.ToStatus))
	}
	if a.Comment != "" {
		pa.Comment = protocol.Ptr(a.Comment)
	}
	return pa
}

func ticketAttachmentToProtocol(att store.TicketAttachment) protocol.TicketAttachment {
	pa := protocol.TicketAttachment{
		ID:        int(att.ID),
		Filename:  att.Filename,
		Path:      att.Path,
		CreatedAt: att.CreatedAt.Format(time.RFC3339),
	}
	if att.Note != "" {
		pa.Note = protocol.Ptr(att.Note)
	}
	return pa
}
