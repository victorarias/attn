package daemon

import (
	"encoding/json"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// The board read side of the work tracker: a snapshot in initial_state plus a
// tickets_updated broadcast per mutation, and a get_ticket request/result for one
// row's detail. Pushes carry bare rows so a busy board stays cheap.

// ticketsForBroadcast is the payload of both initial_state and tickets_updated:
// the whole non-archived board as slim rows. The brief stays off it — the board
// is re-pushed on every ticket mutation and to every client on connect, and no
// client renders a description from a row.
func (d *Daemon) ticketsForBroadcast() []protocol.TicketRow {
	if d.store == nil {
		return nil
	}
	rows, err := d.store.ListTickets(store.TicketListFilter{})
	if err != nil {
		d.logf("list tickets: %v", err)
		return nil
	}
	out := make([]protocol.TicketRow, 0, len(rows))
	for _, t := range rows {
		if t != nil {
			out = append(out, ticketToProtocolRow(t))
		}
	}
	return out
}

// ticketRows lists the board through a filter as full wire records without the
// activity thread, newest first. This is the agent's board read (ticket_list),
// which carries the brief; the app's feed uses ticketsForBroadcast.
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

// handleTicketList is the agent's board read. NOT identity-scoped: it returns
// the whole board, so source_session_id is accepted but unused.
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

// handleTicketShow is the agent's non-consuming full-record read: unlike
// ticket_inbox it never advances the unread cursor, and it is not identity-scoped.
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

// publishTicketFact publishes the fact a mutator caused; projectTicketsUpdated
// does the board push. The ticket id is required: a subject-less fact would be a
// snapshot invalidation.
func (d *Daemon) publishTicketFact(name, ticketID string) {
	if strings.TrimSpace(ticketID) == "" {
		// Keep the board correct, but make the producer's lost id visible.
		d.logf("bus: %s published without a ticket id", name)
	}
	d.publishFact(name, ticketID, nil)
}

// projectTicketsUpdated re-pushes the whole non-archived board to every client.
// Like every other whole-list projection it goes through projectSnapshot, so a
// bulk ticket operation puts one board on the wire instead of one per ticket.
func (d *Daemon) projectTicketsUpdated() {
	if d.store == nil {
		return
	}
	d.projectSnapshot(snapshotTickets, func() {
		tickets := d.ticketsForBroadcast()
		// TicketsUpdatedMessage is its own top-level event, so the wsHub's
		// WebSocketEvent-only broadcastListener cannot see it; tests use this hook.
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
	})
}

// sendGetTicketWSResult replies to get_ticket, correlated by requestID. An
// unknown id is a failed result: the TTL sweep can remove a ticket mid-click.
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

// ticketToProtocol maps a store ticket to its wire shape; artifacts are hydrated
// separately for full reads.
func ticketToProtocol(t *store.Ticket) protocol.Ticket {
	pt := protocol.Ticket{
		ID:             t.ID,
		Title:          t.Title,
		Description:    t.Description,
		Status:         protocol.TicketStatus(t.Status),
		Assignee:       t.Assignee,
		Cwd:            t.Cwd,
		LastAgentID:    t.LastAgentID,
		ProjectID:      t.ProjectID,
		CreatedAt:      t.CreatedAt.Format(time.RFC3339),
		UpdatedAt:      t.UpdatedAt.Format(time.RFC3339),
		LatestEventSeq: protocol.Ptr(int(t.LatestEventSeq)),
		Activity:       make([]protocol.TicketActivity, 0, len(t.Activity)),
		Artifacts:      make([]protocol.TicketArtifact, 0),
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

// ticketToProtocolRow maps a store ticket to its board-feed row.
func ticketToProtocolRow(t *store.Ticket) protocol.TicketRow {
	row := protocol.TicketRow{
		ID:          t.ID,
		Title:       t.Title,
		Status:      protocol.TicketStatus(t.Status),
		Assignee:    t.Assignee,
		Cwd:         t.Cwd,
		LastAgentID: t.LastAgentID,
		UpdatedAt:   t.UpdatedAt.Format(time.RFC3339),
	}
	if t.ClosedAt != nil {
		row.ClosedAt = protocol.Ptr(t.ClosedAt.Format(time.RFC3339))
	}
	if t.ReconciledAt != nil {
		row.ReconciledAt = protocol.Ptr(t.ReconciledAt.Format(time.RFC3339))
	}
	return row
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
