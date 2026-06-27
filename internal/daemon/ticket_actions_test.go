package daemon

import (
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// A human status change from the app moves the ticket, succeeds, and — because it
// is authored as "you", not the agent — notifies the idle assigned agent.
func TestTicketChangeStatusMovesAndNotifies(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
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
	if !wasNudged(inputs(agentID)) {
		t.Fatal("assigned agent was not notified of the human status change")
	}
}

// A human comment lands on the thread and notifies the idle assigned agent — the
// board→agent steer with real content.
func TestTicketAddCommentLandsAndNotifies(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
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
