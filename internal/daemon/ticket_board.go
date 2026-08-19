package daemon

import (
	"encoding/json"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// The board read side of the work tracker. Every read is an agent pulling over
// its own socket: the app shows the garden, so nothing about a ticket is pushed
// to a WebSocket client any more.

// ticketRows lists the board through a filter as full wire records without the
// activity thread, newest first. This is the agent's board read (ticket_list),
// which carries the brief.
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
	_, byTicket := d.latestAutomationProvenance()
	for _, t := range rows {
		if t != nil {
			row := ticketToProtocol(t)
			row.Automation = byTicket[t.ID]
			out = append(out, row)
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

// publishTicketFact publishes the fact a mutator caused. The ticket id is
// required: a subject-less fact would be a snapshot invalidation. Nothing
// projects these to the wire — see factsWithoutWire — but they stay the durable
// record the read verbs and any subscribing app are served from.
func (d *Daemon) publishTicketFact(name, ticketID string) {
	if strings.TrimSpace(ticketID) == "" {
		// Keep the board correct, but make the producer's lost id visible.
		d.logf("bus: %s published without a ticket id", name)
	}
	d.publishFact(name, ticketID, nil)
}

// afterTicketMutation runs the shared post-mutation fan-out: notify the other
// participants (the assigned agent) and publish the fact. A no-op on error.
func (d *Daemon) afterTicketMutation(ticketID string, err error) {
	if err != nil {
		return
	}
	d.notifyTicketObservers(ticketID)
	d.publishTicketFact(FactTicketChanged, ticketID)
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

func (d *Daemon) ticketToProtocolFull(t *store.Ticket) (protocol.Ticket, error) {
	pt := ticketToProtocol(t)
	pt.Automation = d.automationProvenanceForTicket(t.ID)
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
