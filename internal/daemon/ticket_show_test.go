package daemon

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// callTicketShow drives the agent-socket handler synchronously and returns the
// decoded response. handleTicketShow has no async side effects, so a plain
// syncConn suffices.
func callTicketShow(t *testing.T, d *Daemon, ticketID string) protocol.Response {
	t.Helper()
	conn := &syncConn{}
	d.handleTicketShow(conn, &protocol.TicketShowMessage{Cmd: protocol.CmdTicketShow, TicketID: ticketID})
	var resp protocol.Response
	if err := json.Unmarshal(conn.buf.Bytes(), &resp); err != nil {
		t.Fatalf("decode ticket-show response: %v", err)
	}
	return resp
}

// ticket_show is the agent-socket counterpart of the app's get_ticket: a
// non-consuming full-record read (description + complete activity thread with
// full bodies + attachments) for a ticket with 2+ activity events, including a
// long multi-line comment. Nothing here should be truncated.
func TestHandleTicketShowReturnsFullRecord(t *testing.T) {
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
	longComment := "line one of the verdict\nline two with more detail\nline three: the conclusion"
	if _, err := d.store.AddTicketComment("store-migration", "chief-1", longComment, now.Add(2*time.Minute)); err != nil {
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

	resp := callTicketShow(t, d, "store-migration")
	if !resp.Ok || resp.TicketShowResult == nil {
		t.Fatalf("ticket show response = %+v, want ok with result", resp)
	}
	tk := resp.TicketShowResult.Ticket
	if tk.ID != "store-migration" || tk.Description != "Move to X" {
		t.Fatalf("ticket id/description = %q/%q", tk.ID, tk.Description)
	}
	if len(tk.Activity) < 2 {
		t.Fatalf("activity = %+v, want at least 2 events", tk.Activity)
	}

	var sawStatusChange, sawComment bool
	for _, a := range tk.Activity {
		switch a.Kind {
		case protocol.TicketActivityKind(store.TicketActivityStatusChange):
			sawStatusChange = true
		case protocol.TicketActivityKind(store.TicketActivityComment):
			sawComment = true
			if a.Comment == nil || *a.Comment != longComment {
				t.Fatalf("comment body = %v, want full multi-line body intact", a.Comment)
			}
		}
	}
	if !sawStatusChange || !sawComment {
		t.Fatalf("activity kinds: statusChange=%v comment=%v (activity=%+v)", sawStatusChange, sawComment, tk.Activity)
	}
	if len(tk.Attachments) != 1 || tk.Attachments[0].Filename != "report.md" {
		t.Fatalf("attachments = %+v", tk.Attachments)
	}
}

// An unknown id fails with Ok:false and an error, not a panic — the same
// contract as the app's get_ticket over websocket.
func TestHandleTicketShowUnknownIDFails(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	resp := callTicketShow(t, d, "nope")
	if resp.Ok {
		t.Fatalf("ticket show response = %+v, want Ok:false for unknown id", resp)
	}
	if resp.Error == nil || !strings.Contains(*resp.Error, "not found") {
		t.Fatalf("ticket show error = %v, want a not-found error", resp.Error)
	}
	if resp.TicketShowResult != nil {
		t.Fatalf("failure response carried a result: %+v", resp.TicketShowResult)
	}
}
