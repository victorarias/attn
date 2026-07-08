package daemon

import (
	"encoding/json"
	"net"
	"strings"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/ticketnotify"
)

// ticketObserversForSession builds the effective notification identities for a
// session. Every session retains its ordinary session identity. The active chief
// additionally observes through the durable chief role identity, whose cursor
// survives role transfers while AuthorID keeps self-authored events excluded. The
// delivery path comes from the session's agent driver capability
// (agent.Capabilities.HasSelfMonitor, resolved via the registry): Claude
// self-monitors and watches; codex and the rest are nudged. An empty/unknown agent
// resolves to nil → Capabilities{} → false, the safe nudge default.
func (d *Daemon) ticketObserversForSession(sessionID string) []ticketnotify.Observer {
	agentName := ""
	if s := d.store.Get(sessionID); s != nil {
		agentName = s.Agent
	}
	selfMonitor := agentdriver.EffectiveCapabilities(agentdriver.Get(agentName)).HasSelfMonitor
	personal := ticketnotify.Observer{ID: sessionID, AuthorID: sessionID, DeliveryID: sessionID, HasSelfMonitor: selfMonitor}
	observers := []ticketnotify.Observer{personal}
	if d.isChiefOfStaffSession(sessionID) {
		observers = append(observers, ticketnotify.Observer{
			ID:             store.TicketRoleIdentity(store.TicketRoleChiefOfStaff),
			AuthorID:       sessionID,
			DeliveryID:     sessionID,
			HasSelfMonitor: selfMonitor,
		})
	}
	return observers
}

func (d *Daemon) ticketDeliveryObserverForSession(sessionID string) ticketnotify.Observer {
	return d.ticketObserversForSession(sessionID)[0]
}

func (d *Daemon) ticketUnreadForSession(sessionID string) (int, error) {
	return ticketnotify.UnreadAny(d.store, d.ticketObserversForSession(sessionID))
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
	bundles, err := ticketnotify.ConsumeAll(d.store, d.ticketObserversForSession(sourceSessionID), time.Now())
	if err != nil {
		d.sendError(conn, "ticket inbox: "+err.Error())
		return
	}
	// The consume advanced this session's cursors, so its unread count just dropped.
	// Refresh the indicator (and cancel any pending countdown if fully drained) — this
	// is the chokepoint a self-monitoring agent's own watch drains through.
	d.refreshTicketUnread(sourceSessionID)
	result := &protocol.TicketInboxResult{Bundles: ticketEventBundlesToProtocol(bundles)}
	if lastActive := d.lastUserActivityAt(); !lastActive.IsZero() {
		result.LastUserActivityAt = protocol.Ptr(lastActive.Format(time.RFC3339))
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:                true,
		TicketInboxResult: result,
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
