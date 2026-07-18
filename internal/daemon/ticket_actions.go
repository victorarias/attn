package daemon

import (
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// The chief/user action side of the work tracker (slice 4c). These are the app's
// by-id ticket mutations — change status, comment, re-brief — authored as the human
// identity ("you"). Each completes the reverse channel the board was missing: a
// human edit lands an event, notifyTicketObservers nudges the assigned agent (which
// did not author it), and broadcastTicketsUpdated refreshes the board (the open
// detail view re-fetches off that). All three reply with the shared
// ticket_action_result, correlated by request_id, so the app can show success or an
// error without inventing optimistic state.

// sendTicketActionResult is the shared reply for a chief ticket action: success
// when err is nil, otherwise the error string. The mutated data reaches the UI
// through the tickets_updated broadcast, not this event.
func (d *Daemon) sendTicketActionResult(client *wsClient, requestID string, err error) {
	msg := protocol.TicketActionResultMessage{
		Event:     protocol.EventTicketActionResult,
		RequestID: requestID,
		Success:   err == nil,
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

// afterTicketMutation runs the shared post-mutation fan-out: notify the other
// participants (the assigned agent) and refresh the board. A no-op on error.
func (d *Daemon) afterTicketMutation(ticketID string, err error) {
	if err != nil {
		return
	}
	d.notifyTicketObservers(ticketID)
	d.broadcastTicketsUpdated()
}

func (d *Daemon) handleTicketChangeStatus(client *wsClient, msg *protocol.TicketChangeStatusMessage) {
	requestID := protocol.Deref(msg.RequestID)
	ticketID := strings.TrimSpace(msg.TicketID)
	if ticketID == "" {
		d.sendTicketActionResult(client, requestID, fmt.Errorf("ticket_id is required"))
		return
	}
	if err := requireExpectedTicketEventSeq(msg.ExpectedEventSeq); err != nil {
		d.sendTicketActionResult(client, requestID, err)
		return
	}
	// crashed is the one status attn authors itself (a session that died without
	// reporting); the board never offers it as a manual destination. The daemon is
	// the authority, so it rejects it here rather than trusting the UI's
	// SELECTABLE_STATUSES allowlist — all other transitions stay permissive
	// (awareness, not autonomy).
	status := store.TicketStatus(msg.Status)
	if status == store.TicketStatusCrashed {
		d.sendTicketActionResult(client, requestID, fmt.Errorf("crashed is an attn-authored status, not a manual transition"))
		return
	}
	comment := strings.TrimSpace(protocol.Deref(msg.Comment))
	_, _, err := d.store.SetTicketStatusWithOptions(
		ticketID, status, store.TicketAuthorYou, comment,
		expectedTicketMutationOptions(msg.ExpectedEventSeq), time.Now(),
	)
	d.afterTicketMutation(ticketID, err)
	d.sendTicketActionResult(client, requestID, err)
}

func (d *Daemon) handleTicketAddComment(client *wsClient, msg *protocol.TicketAddCommentMessage) {
	requestID := protocol.Deref(msg.RequestID)
	ticketID := strings.TrimSpace(msg.TicketID)
	comment := strings.TrimSpace(msg.Comment)
	if ticketID == "" || comment == "" {
		d.sendTicketActionResult(client, requestID, fmt.Errorf("ticket_id and comment are required"))
		return
	}
	if err := requireExpectedTicketEventSeq(msg.ExpectedEventSeq); err != nil {
		d.sendTicketActionResult(client, requestID, err)
		return
	}
	_, _, err := d.store.AddTicketCommentWithOptions(
		ticketID, store.TicketAuthorYou, comment,
		expectedTicketMutationOptions(msg.ExpectedEventSeq), time.Now(),
	)
	d.afterTicketMutation(ticketID, err)
	d.sendTicketActionResult(client, requestID, err)
}

func (d *Daemon) handleTicketEditDescription(client *wsClient, msg *protocol.TicketEditDescriptionMessage) {
	requestID := protocol.Deref(msg.RequestID)
	ticketID := strings.TrimSpace(msg.TicketID)
	if ticketID == "" {
		d.sendTicketActionResult(client, requestID, fmt.Errorf("ticket_id is required"))
		return
	}
	if err := requireExpectedTicketEventSeq(msg.ExpectedEventSeq); err != nil {
		d.sendTicketActionResult(client, requestID, err)
		return
	}
	_, err := d.store.EditTicketDescriptionWithOptions(
		ticketID, msg.Description, store.TicketAuthorYou,
		expectedTicketMutationOptions(msg.ExpectedEventSeq), time.Now(),
	)
	d.afterTicketMutation(ticketID, err)
	d.sendTicketActionResult(client, requestID, err)
}
