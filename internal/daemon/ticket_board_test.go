package daemon

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// readTicketResult decodes the single ws event the board handler sent to a client.
func readTicketResult(t *testing.T, ch chan outboundMessage, target any) {
	t.Helper()
	select {
	case message := <-ch:
		if err := json.Unmarshal(message.payload, target); err != nil {
			t.Fatalf("decode ws event: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("no websocket result event was sent")
	}
}

// ticketIDs collects the ids of a broadcast board feed, for membership assertions.
func ticketIDs(tickets []protocol.Ticket) []string {
	ids := make([]string, 0, len(tickets))
	for _, tk := range tickets {
		ids = append(ids, tk.ID)
	}
	return ids
}

// captureTicketBroadcasts records every tickets_updated board push the daemon makes,
// via the in-process hook, so a test can assert the post-mutation board push
// deterministically. Returns an accessor for the most recent broadcast.
func captureTicketBroadcasts(d *Daemon) (latest func() []protocol.Ticket) {
	var broadcasts [][]protocol.Ticket
	d.ticketsBroadcastHook = func(tickets []protocol.Ticket) {
		broadcasts = append(broadcasts, tickets)
	}
	return func() []protocol.Ticket {
		if len(broadcasts) == 0 {
			return nil
		}
		return broadcasts[len(broadcasts)-1]
	}
}

// get_ticket returns the FULL record: the row plus its activity thread (status
// changes with from/to + note, freeform comments) and attachments — the detail the
// bare board feed deliberately omits.
func TestGetTicketWSResultReturnsFullRecord(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := time.Now()
	if _, err := d.store.CreateTicket(store.Ticket{
		ID:          "store-migration",
		Title:       "Migrate the store",
		Description: "Move to X",
		Status:      store.TicketStatusWorking,
		Assignee:    "sess-1",
		Cwd:         "/repo",
		LastAgentID: "codex",
	}, "chief-1", now); err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	if _, err := d.store.SetTicketStatus("store-migration", store.TicketStatusInReview, "sess-1", "ready for a look", now.Add(time.Minute)); err != nil {
		t.Fatalf("set status: %v", err)
	}
	if _, err := d.store.AddTicketComment("store-migration", "chief-1", "looks good", now.Add(2*time.Minute)); err != nil {
		t.Fatalf("add comment: %v", err)
	}
	if _, err := d.store.AddTicketAttachment(store.TicketAttachment{
		TicketID: "store-migration",
		Filename: "report.md",
		Path:     "/repo/report.md",
		Note:     "the findings",
	}, "sess-1", now.Add(3*time.Minute)); err != nil {
		t.Fatalf("add attachment: %v", err)
	}

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.sendGetTicketWSResult(client, "req-1", "store-migration")

	var res protocol.TicketResultMessage
	readTicketResult(t, client.send, &res)
	if res.Event != protocol.EventTicketResult || res.RequestID != "req-1" || !res.Success {
		t.Fatalf("result = %+v, want success ticket_result for req-1", res)
	}
	tk := res.Ticket
	if tk == nil {
		t.Fatal("ticket payload is nil")
	}
	if tk.ID != "store-migration" || tk.Status != protocol.TicketStatus(store.TicketStatusInReview) {
		t.Fatalf("ticket id/status = %q/%q", tk.ID, tk.Status)
	}

	var sawStatusChange, sawComment bool
	for _, a := range tk.Activity {
		switch a.Kind {
		case protocol.TicketActivityKind(store.TicketActivityStatusChange):
			sawStatusChange = true
			if a.ToStatus == nil || *a.ToStatus != protocol.TicketStatus(store.TicketStatusInReview) {
				t.Fatalf("status-change to_status = %v, want in_review", a.ToStatus)
			}
			if a.Comment == nil || *a.Comment != "ready for a look" {
				t.Fatalf("status-change comment = %v", a.Comment)
			}
		case protocol.TicketActivityKind(store.TicketActivityComment):
			sawComment = true
		}
	}
	if !sawStatusChange || !sawComment {
		t.Fatalf("activity kinds: statusChange=%v comment=%v (activity=%+v)", sawStatusChange, sawComment, tk.Activity)
	}
	if len(tk.Attachments) != 1 || tk.Attachments[0].Filename != "report.md" {
		t.Fatalf("attachments = %+v", tk.Attachments)
	}
	if tk.Attachments[0].Note == nil || *tk.Attachments[0].Note != "the findings" {
		t.Fatalf("attachment note = %v", tk.Attachments[0].Note)
	}
}

// An unknown id is a failed result (error set, no ticket), never a panic — the TTL
// sweep can remove a ticket between a board push and the app's click.
func TestGetTicketWSResultUnknownIDFails(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.sendGetTicketWSResult(client, "req-2", "nope")

	var res protocol.TicketResultMessage
	readTicketResult(t, client.send, &res)
	if res.RequestID != "req-2" || res.Success || res.Error == nil {
		t.Fatalf("unknown-id result = %+v, want failure with error", res)
	}
	if res.Ticket != nil {
		t.Fatalf("failure result carried a ticket: %+v", res.Ticket)
	}
}

// The board feed is the non-archived set, and each row is BARE — activity and
// attachments stay empty so a busy board is cheap to broadcast; the detail fetch
// loads them.
func TestTicketsForBroadcastBareNonArchived(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := time.Now()
	if _, err := d.store.CreateTicket(store.Ticket{
		ID: "open-one", Title: "Open", Status: store.TicketStatusWorking, Assignee: "sess-1",
	}, "chief-1", now); err != nil {
		t.Fatalf("create open: %v", err)
	}
	if _, err := d.store.CreateTicket(store.Ticket{
		ID: "done-one", Title: "Done", Status: store.TicketStatusDone,
	}, "chief-1", now); err != nil {
		t.Fatalf("create done: %v", err)
	}
	if err := d.store.ArchiveTicket("done-one", now.Add(time.Minute)); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if _, err := d.store.AddTicketComment("open-one", "chief-1", "note", now.Add(2*time.Minute)); err != nil {
		t.Fatalf("comment: %v", err)
	}

	board := d.ticketsForBroadcast()
	if len(board) != 1 || board[0].ID != "open-one" {
		t.Fatalf("board = %+v, want only the non-archived open-one", board)
	}
	if len(board[0].Activity) != 0 || len(board[0].Attachments) != 0 {
		t.Fatalf("board row should be bare, got activity=%d attachments=%d", len(board[0].Activity), len(board[0].Attachments))
	}
}
