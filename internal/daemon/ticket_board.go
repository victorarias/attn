package daemon

import (
	"encoding/json"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// The board read side of the work tracker (slice 4a). The app never speaks the
// agent's ticket verbs; it observes the board the way it observes dispatches —
// a snapshot in initial_state plus a tickets_updated broadcast on every mutation
// (the push), and a get_ticket request/result for the full detail of one row it
// clicked (the pull). The push carries bare rows (no activity/artifacts) so a
// busy board stays cheap to broadcast; the detail fetch loads the full record.

// ticketsForBroadcast is the live board feed: every non-archived ticket as a bare
// wire row (activity/artifacts empty), newest first. It is the payload of both
// the initial_state snapshot and each tickets_updated broadcast, so a client
// renders the board identically from either.
func (d *Daemon) ticketsForBroadcast() []protocol.Ticket {
	return d.ticketRows(store.TicketListFilter{})
}

// ticketRows lists the board through a filter and maps each store row to its bare
// wire shape (activity/artifacts empty), newest first. Shared by the app's
// broadcast feed (empty filter) and the agent's `ticket_list` read (caller filter).
func (d *Daemon) ticketRows(filter store.TicketListFilter) []protocol.Ticket {
	if d.store == nil {
		return nil
	}
	rows, err := d.store.ListTickets(filter)
	if err != nil {
		d.logf("list tickets: %v", err)
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

// handleTicketList is the agent's board read — the foundation the write verbs
// (comment/take/subscribe) stand on, since an agent needs a ticket-id before it
// can act. Unlike those verbs it is NOT identity-scoped: it returns the whole
// board (optionally filtered by status / including archived), so source_session_id
// is accepted but unused. Rows carry the description (the brief) but not the
// activity thread, matching the broadcast feed.
func (d *Daemon) handleTicketList(conn net.Conn, msg *protocol.TicketListMessage) {
	filter := store.TicketListFilter{}
	if msg.Status != nil {
		filter.Status = store.TicketStatus(strings.TrimSpace(*msg.Status))
	}
	if msg.IncludeArchived != nil {
		filter.IncludeArchived = *msg.IncludeArchived
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:               true,
		TicketListResult: &protocol.TicketListResult{Tickets: d.ticketRows(filter)},
	})
}

// handleTicketShow is the agent's non-consuming full-record read: one ticket's
// metadata, description, and complete activity thread (full bodies) plus
// current artifacts, the same shape sendGetTicketWSResult serves the app. Unlike
// ticket_inbox, it never advances the calling session's unread cursor, so an
// agent can re-read it at will. Like ticket_list it is NOT identity-scoped, so
// source_session_id is accepted but unused.
func (d *Daemon) handleTicketShow(conn net.Conn, msg *protocol.TicketShowMessage) {
	ticketID := strings.TrimSpace(msg.TicketID)
	if ticketID == "" {
		d.sendError(conn, "ticket show: ticket_id is required")
		return
	}
	ticket, err := d.store.GetTicket(ticketID)
	if err != nil {
		d.sendError(conn, "ticket show: "+err.Error())
		return
	}
	if ticket == nil {
		d.sendError(conn, "ticket show: ticket not found: "+ticketID)
		return
	}
	full, err := d.ticketToProtocolFull(ticket)
	if err != nil {
		d.sendError(conn, "ticket show: "+err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:               true,
		TicketShowResult: &protocol.TicketShowResult{Ticket: full},
	})
}

// broadcastTicketsUpdated re-pushes the whole non-archived board to every client.
// Run it after any producer that mutates a ticket (create, status, crash, and the
// chief edits to come).
func (d *Daemon) broadcastTicketsUpdated() {
	if d.store == nil {
		return
	}
	tickets := d.ticketsForBroadcast()
	// An optional in-process hook lets tests observe the board push deterministically
	// without a live socket — TicketsUpdatedMessage is its own top-level event, so the
	// wsHub's WebSocketEvent-only broadcastListener cannot see it (same pattern as
	// broadcastWorkflowRunUpdated).
	if d.ticketsBroadcastHook != nil {
		d.ticketsBroadcastHook(tickets)
	}
	if d.wsHub == nil {
		return
	}
	d.broadcastMessage(&protocol.TicketsUpdatedMessage{
		Event:   protocol.EventTicketsUpdated,
		Tickets: tickets,
	})
}

// sendGetTicketWSResult replies to a get_ticket request with the ticket's full
// record (row + activity thread + artifacts), correlated by requestID. An
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
		full, fullErr := d.ticketToProtocolFull(ticket)
		if fullErr != nil {
			msg.Error = protocol.Ptr(fullErr.Error())
			break
		}
		msg.Success = true
		msg.Ticket = &full
	}
	d.sendToClient(client, msg)
}

// ticketToProtocol maps a store ticket to its wire shape. Activity is mapped when
// present and becomes an empty slice on a ListTickets bare row. Artifacts are
// hydrated separately from the filesystem for full reads.
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
		Artifacts:   make([]protocol.TicketArtifact, 0),
	}
	if t.ClosedAt != nil {
		pt.ClosedAt = protocol.Ptr(t.ClosedAt.Format(time.RFC3339))
	}
	if t.ArchivedAt != nil {
		pt.ArchivedAt = protocol.Ptr(t.ArchivedAt.Format(time.RFC3339))
	}
	if t.ReconciledAt != nil {
		pt.ReconciledAt = protocol.Ptr(t.ReconciledAt.Format(time.RFC3339))
	}
	for _, a := range t.Activity {
		pt.Activity = append(pt.Activity, ticketActivityToProtocol(a))
	}
	return pt
}

func (d *Daemon) ticketToProtocolFull(t *store.Ticket) (protocol.Ticket, error) {
	pt := ticketToProtocol(t)
	artifacts, err := d.ticketArtifacts(t.ID)
	if err != nil {
		return protocol.Ticket{}, err
	}
	pt.Artifacts = artifacts
	return pt, nil
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
