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

// The observer identities a session reads through live in ticket_identity.go,
// beside the inverse mapping they must agree with. The delivery path is shared by
// all runtimes; an optional runtime `ticket inbox --watch` consumes the same unread
// queue before a countdown has to ring.

func (d *Daemon) ticketUnreadForSession(sessionID string) (int, error) {
	return ticketnotify.UnreadAny(d.store, d.ticketObserversForSession(sessionID))
}

// handleTicketInbox returns the calling session's unread ticket events, bundled by
// ticket, and advances its per-ticket cursors past them (a consume). This is the
// agent's read path — what a nudged agent runs to catch up, and what a runtime's
// optional watch drains. The observer identity is resolved from the session, so the
// caller names nothing.
func (d *Daemon) handleTicketInbox(conn net.Conn, msg *protocol.TicketInboxMessage) {
	sourceSessionID := strings.TrimSpace(msg.SourceSessionID)
	if sourceSessionID == "" {
		d.sendError(conn, "ticket inbox: source_session_id is required")
		return
	}
	now := time.Now()
	watch := msg.Mode != nil && *msg.Mode == protocol.TicketInboxModeWatch
	d.deliveryMu.Lock()
	if watch {
		if d.watchLeaseUntil == nil {
			d.watchLeaseUntil = make(map[string]time.Time)
		}
		d.watchLeaseUntil[sourceSessionID] = now.Add(ticketWatchLeaseWindowFor(msg.WatchIntervalMs))
	}
	bundles, err := ticketnotify.ConsumeAll(d.store, d.ticketObserversForSession(sourceSessionID), now)
	if err == nil && len(bundles) > 0 {
		var deliveredThroughSeq int64
		for _, bundle := range bundles {
			for _, event := range bundle.Events {
				if event.Seq > deliveredThroughSeq {
					deliveredThroughSeq = event.Seq
				}
			}
		}
		if attentionErr := d.store.SetTicketDeliveryAttentionThrough(d.ticketAttentionKey(sourceSessionID), now, deliveredThroughSeq); attentionErr != nil {
			// Cursors have already advanced, so returning only an error here would
			// hide the consumed bundles. Preserve delivery and let the next
			// successful attention write repair the interruption clock.
			d.logf("ticket inbox attention update %s: %v", sourceSessionID, attentionErr)
		}
	}
	d.deliveryMu.Unlock()
	if err != nil {
		d.sendError(conn, "ticket inbox: "+err.Error())
		return
	}
	// The consume advanced this session's cursors, so its unread count just dropped.
	// Refresh the indicator (and cancel any pending countdown if fully drained) — this
	// is the chokepoint a runtime's optional watch drains through.
	d.refreshTicketUnread(sourceSessionID)
	if d.debugLogging && len(bundles) > 0 {
		pending := 0
		for _, bundle := range bundles {
			pending += len(bundle.Events)
		}
		channel := "explicit"
		if watch {
			channel = "watch"
		}
		d.logf("ticket delivery: observer=%s session=%s channel=%s outcome=consumed pending=%d tickets=%d", d.ticketAttentionKey(sourceSessionID), sourceSessionID, channel, pending, len(bundles))
	}
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
