package daemon

import (
	"encoding/json"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/ticketnotify"
)

// ticketObserverForSession builds the notification observer for a session. Identity
// is uniform: every session — the chief included — observes as its own session id,
// the same string it authors events with, so involvement (assignee or author) and
// the self-author exclusion line up. The literal ticketnotify.ObserverChief is only
// the slice-2 harness placeholder; real wiring keys off the live session. The agent
// decides the delivery path (Claude self-monitors; codex is nudged).
func (d *Daemon) ticketObserverForSession(sessionID string) ticketnotify.Observer {
	agent := ""
	if s := d.store.Get(sessionID); s != nil {
		agent = s.Agent
	}
	return ticketnotify.AgentObserver(sessionID, agent)
}

// handleTicketInbox returns the calling session's unread ticket events, bundled by
// ticket, and advances its per-ticket cursors past them (a consume). This is the
// agent's read path — what a nudged agent runs to catch up, and what a
// self-monitoring agent's own watch drains. The observer identity is resolved from
// the session, so the caller names nothing.
func (d *Daemon) handleTicketInbox(conn net.Conn, msg *protocol.TicketInboxMessage) {
	sourceSessionID := strings.TrimSpace(msg.SourceSessionID)
	if sourceSessionID == "" {
		d.sendError(conn, "ticket inbox: source_session_id is required")
		return
	}
	obs := d.ticketObserverForSession(sourceSessionID)
	bundles, err := ticketnotify.Consume(d.store, obs, time.Now())
	if err != nil {
		d.sendError(conn, "ticket inbox: "+err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:                true,
		TicketInboxResult: &protocol.TicketInboxResult{Bundles: ticketEventBundlesToProtocol(bundles)},
	})
}

func ticketEventBundlesToProtocol(bundles []ticketnotify.Bundle) []protocol.TicketEventBundle {
	out := make([]protocol.TicketEventBundle, 0, len(bundles))
	for _, b := range bundles {
		events := make([]protocol.TicketEvent, 0, len(b.Events))
		for _, e := range b.Events {
			events = append(events, ticketEventToProtocol(e))
		}
		out = append(out, protocol.TicketEventBundle{TicketID: b.TicketID, Events: events})
	}
	return out
}

func ticketEventToProtocol(e store.TicketEvent) protocol.TicketEvent {
	pe := protocol.TicketEvent{
		TicketID:  e.TicketID,
		Kind:      protocol.TicketEventKind(e.Kind),
		Author:    e.Author,
		CreatedAt: e.CreatedAt.Format(time.RFC3339),
	}
	if e.FromStatus != "" {
		pe.FromStatus = protocol.Ptr(protocol.TicketStatus(e.FromStatus))
	}
	if e.ToStatus != "" {
		pe.ToStatus = protocol.Ptr(protocol.TicketStatus(e.ToStatus))
	}
	if e.Comment != "" {
		pe.Comment = protocol.Ptr(e.Comment)
	}
	if e.Detail != "" {
		pe.Detail = protocol.Ptr(e.Detail)
	}
	return pe
}
