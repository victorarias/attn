package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// A human status change from the app moves the ticket, succeeds, and — because it
// is authored as "you", not the agent — notifies the idle assigned agent.
func TestTicketChangeStatusMovesAndNotifies(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	_, agentID, inputs := delegateForNotify(t, d, "codex")
	ticketID := boundTicketID(t, d, agentID)
	d.store.UpdateState(agentID, protocol.StateIdle)

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleTicketChangeStatus(client, &protocol.TicketChangeStatusMessage{
		Cmd:       protocol.CmdTicketChangeStatus,
		RequestID: protocol.Ptr("req-1"),
		TicketID:  ticketID,
		Status:    protocol.TicketStatus(store.TicketStatusBlocked),
		Comment:   protocol.Ptr("need a decision"),
	})

	var res protocol.TicketActionResultMessage
	readTicketResult(t, client.send, &res)
	if res.Event != protocol.EventTicketActionResult || res.RequestID != "req-1" || !res.Success {
		t.Fatalf("result = %+v, want success for req-1", res)
	}
	tk, _ := d.store.GetTicket(ticketID)
	if tk == nil || tk.Status != store.TicketStatusBlocked {
		t.Fatalf("ticket status = %v, want blocked", tk)
	}
	fireNudgeNow(t, d, agentID) // the notify armed a countdown; fire it
	if !wasNudged(inputs(agentID)) {
		t.Fatal("assigned agent was not notified of the human status change")
	}
}

// A human comment lands on the thread and notifies the idle assigned agent — the
// board→agent steer with real content.
func TestTicketAddCommentLandsAndNotifies(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	_, agentID, inputs := delegateForNotify(t, d, "codex")
	ticketID := boundTicketID(t, d, agentID)
	d.store.UpdateState(agentID, protocol.StateIdle)

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleTicketAddComment(client, &protocol.TicketAddCommentMessage{
		Cmd:       protocol.CmdTicketAddComment,
		RequestID: protocol.Ptr("req-2"),
		TicketID:  ticketID,
		Comment:   "try the other approach",
	})

	var res protocol.TicketActionResultMessage
	readTicketResult(t, client.send, &res)
	if !res.Success || res.RequestID != "req-2" {
		t.Fatalf("result = %+v, want success for req-2", res)
	}
	tk, _ := d.store.GetTicket(ticketID)
	var sawComment bool
	for _, a := range tk.Activity {
		if a.Kind == store.TicketActivityComment && a.Author == store.TicketAuthorYou && a.Comment == "try the other approach" {
			sawComment = true
		}
	}
	if !sawComment {
		t.Fatalf("comment not recorded as authored by 'you': %+v", tk.Activity)
	}
	fireNudgeNow(t, d, agentID)
	if !wasNudged(inputs(agentID)) {
		t.Fatal("assigned agent was not notified of the human comment")
	}
}

// A re-brief replaces the description and succeeds.
func TestTicketEditDescriptionUpdates(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	_, agentID, _ := delegateForNotify(t, d, "codex")
	ticketID := boundTicketID(t, d, agentID)

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleTicketEditDescription(client, &protocol.TicketEditDescriptionMessage{
		Cmd:         protocol.CmdTicketEditDescription,
		RequestID:   protocol.Ptr("req-3"),
		TicketID:    ticketID,
		Description: "Re-scoped: do only the read path",
	})

	var res protocol.TicketActionResultMessage
	readTicketResult(t, client.send, &res)
	if !res.Success {
		t.Fatalf("result = %+v, want success", res)
	}
	tk, _ := d.store.GetTicket(ticketID)
	if tk == nil || tk.Description != "Re-scoped: do only the read path" {
		t.Fatalf("description = %q, want re-scoped text", tk.Description)
	}
}

// A missing ticket_id is a failed result, not a panic.
func TestTicketActionMissingTicketIDFails(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleTicketAddComment(client, &protocol.TicketAddCommentMessage{
		Cmd:       protocol.CmdTicketAddComment,
		RequestID: protocol.Ptr("req-4"),
		TicketID:  "",
		Comment:   "orphan",
	})
	var res protocol.TicketActionResultMessage
	readTicketResult(t, client.send, &res)
	if res.Success || res.RequestID != "req-4" || res.Error == nil {
		t.Fatalf("result = %+v, want failure for req-4", res)
	}
}

// A mutation on an unknown ticket fails with the store error.
func TestTicketActionUnknownTicketFails(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleTicketChangeStatus(client, &protocol.TicketChangeStatusMessage{
		Cmd:       protocol.CmdTicketChangeStatus,
		RequestID: protocol.Ptr("req-5"),
		TicketID:  "does-not-exist",
		Status:    protocol.TicketStatus(store.TicketStatusDone),
	})
	var res protocol.TicketActionResultMessage
	readTicketResult(t, client.send, &res)
	if res.Success || res.Error == nil {
		t.Fatalf("result = %+v, want failure with error", res)
	}
}

// A chief action re-pushes the whole board so the open detail view and the live
// board refresh off the mutation — the load-bearing second half of the fan-out.
func TestTicketChangeStatusBroadcastsBoard(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	_, agentID, _ := delegateForNotify(t, d, "codex")
	ticketID := boundTicketID(t, d, agentID)
	latestBroadcast := captureTicketBroadcasts(d)

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleTicketChangeStatus(client, &protocol.TicketChangeStatusMessage{
		Cmd:       protocol.CmdTicketChangeStatus,
		RequestID: protocol.Ptr("req-b"),
		TicketID:  ticketID,
		Status:    protocol.TicketStatus(store.TicketStatusBlocked),
	})

	board := latestBroadcast()
	if board == nil {
		t.Fatal("the status change fired no tickets_updated board push")
	}
	var moved *protocol.Ticket
	for i := range board {
		if board[i].ID == ticketID {
			moved = &board[i]
		}
	}
	if moved == nil {
		t.Fatalf("tickets_updated %v missing %q", ticketIDs(board), ticketID)
	}
	if moved.Status != protocol.TicketStatus(store.TicketStatusBlocked) {
		t.Fatalf("broadcast ticket status = %v, want blocked", moved.Status)
	}
}

// A failed mutation skips the fan-out: afterTicketMutation early-returns on error,
// so the idle assigned agent (which has an unread assignment that would otherwise
// trip a nudge) is not notified about a change that never happened.
func TestTicketActionFailureDoesNotNotify(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	_, agentID, inputs := delegateForNotify(t, d, "codex")
	ticketID := boundTicketID(t, d, agentID)
	d.store.UpdateState(agentID, protocol.StateIdle)

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleTicketChangeStatus(client, &protocol.TicketChangeStatusMessage{
		Cmd:       protocol.CmdTicketChangeStatus,
		RequestID: protocol.Ptr("req-f"),
		TicketID:  ticketID,
		Status:    protocol.TicketStatus("not-a-status"),
	})

	var res protocol.TicketActionResultMessage
	readTicketResult(t, client.send, &res)
	if res.Success || res.Error == nil {
		t.Fatalf("result = %+v, want failure for an invalid status", res)
	}
	if wasNudged(inputs(agentID)) {
		t.Fatal("a failed mutation must not notify the assigned agent")
	}
}

// crashed is attn-authored; the chief cannot forge it from the board. The daemon
// rejects it before mutating, leaving the ticket untouched and firing no fan-out.
func TestTicketChangeStatusRejectsCrashed(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	_, agentID, inputs := delegateForNotify(t, d, "codex")
	ticketID := boundTicketID(t, d, agentID)
	d.store.UpdateState(agentID, protocol.StateIdle)
	before, _ := d.store.GetTicket(ticketID)

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleTicketChangeStatus(client, &protocol.TicketChangeStatusMessage{
		Cmd:       protocol.CmdTicketChangeStatus,
		RequestID: protocol.Ptr("req-c"),
		TicketID:  ticketID,
		Status:    protocol.TicketStatus(store.TicketStatusCrashed),
	})

	var res protocol.TicketActionResultMessage
	readTicketResult(t, client.send, &res)
	if res.Success || res.Error == nil {
		t.Fatalf("result = %+v, want failure — crashed is not a manual transition", res)
	}
	after, _ := d.store.GetTicket(ticketID)
	if after.Status != before.Status {
		t.Fatalf("status moved to %v despite rejection (was %v)", after.Status, before.Status)
	}
	if wasNudged(inputs(agentID)) {
		t.Fatal("a rejected action must not notify")
	}
}
