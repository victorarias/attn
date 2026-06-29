package daemon

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

// callTicketList drives the board-read handler synchronously and returns the decoded
// response. handleTicketList has no async side effects, so a plain syncConn suffices.
// sessionID is passed only to exercise the optional field — the handler ignores it.
func callTicketList(t *testing.T, d *Daemon, sessionID, status string, includeArchived bool) protocol.Response {
	t.Helper()
	conn := &syncConn{}
	msg := &protocol.TicketListMessage{Cmd: protocol.CmdTicketList}
	if sessionID != "" {
		msg.SourceSessionID = &sessionID
	}
	if status != "" {
		msg.Status = &status
	}
	if includeArchived {
		msg.IncludeArchived = &includeArchived
	}
	d.handleTicketList(conn, msg)
	var resp protocol.Response
	if err := json.Unmarshal(conn.buf.Bytes(), &resp); err != nil {
		t.Fatalf("decode ticket-list response: %v", err)
	}
	return resp
}

func ticketsByID(tickets []protocol.Ticket) map[string]protocol.Ticket {
	out := make(map[string]protocol.Ticket, len(tickets))
	for _, t := range tickets {
		out[t.ID] = t
	}
	return out
}

// The board read is global, not identity-scoped: it returns every ticket and works
// with NO session at all. This is the whole point of the read foundation — an agent
// (or a bare terminal) must be able to find a ticket-id without owning a ticket — so
// the test passes an empty source_session_id and still expects the full board, with
// each row carrying its description (the brief), not just a bare title.
func TestHandleTicketListReturnsBoardWithoutSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(d.stopTicketBackstops)
	_, agents, _ := delegateMany(t, d, "codex", "Task Y", "Task X")
	wantY := boundTicketID(t, d, agents[0])
	wantX := boundTicketID(t, d, agents[1])

	resp := callTicketList(t, d, "", "", false)
	if !resp.Ok || resp.TicketListResult == nil {
		t.Fatalf("ticket list response = %+v, want ok with result", resp)
	}
	got := ticketsByID(resp.TicketListResult.Tickets)
	if len(got) != 2 {
		t.Fatalf("got %d tickets, want 2: %+v", len(got), resp.TicketListResult.Tickets)
	}
	for _, id := range []string{wantY, wantX} {
		ticket, ok := got[id]
		if !ok {
			t.Fatalf("board missing ticket %s: %+v", id, resp.TicketListResult.Tickets)
		}
		if ticket.Description == "" {
			t.Fatalf("ticket %s row has empty description; list should carry the brief", id)
		}
	}
}

// --status narrows the board to one column. After a delegated ticket is moved to
// in_review, filtering by in_review returns only it, and filtering by working returns
// only the other — proving the filter reaches the store query, not just the wire.
func TestHandleTicketListStatusFilter(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(d.stopTicketBackstops)
	_, agents, _ := delegateMany(t, d, "codex", "Task Y", "Task X")
	reviewing := boundTicketID(t, d, agents[0])
	working := boundTicketID(t, d, agents[1])

	callSetTicketStatus(t, d, agents[0], string(protocol.DispatchWorkStateReadyForReview), "ready")

	inReview := callTicketList(t, d, "", string(protocol.TicketStatusInReview), false)
	if !inReview.Ok || inReview.TicketListResult == nil {
		t.Fatalf("in_review list response = %+v, want ok", inReview)
	}
	if got := ticketsByID(inReview.TicketListResult.Tickets); len(got) != 1 || got[reviewing].ID != reviewing {
		t.Fatalf("in_review filter returned %+v, want only %s", inReview.TicketListResult.Tickets, reviewing)
	}

	stillWorking := callTicketList(t, d, "", string(protocol.TicketStatusWorking), false)
	if got := ticketsByID(stillWorking.TicketListResult.Tickets); len(got) != 1 || got[working].ID != working {
		t.Fatalf("working filter returned %+v, want only %s", stillWorking.TicketListResult.Tickets, working)
	}
}
