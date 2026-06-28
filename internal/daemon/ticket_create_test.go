package daemon

import (
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// callTicketCreate drives handleTicketCreate over an in-memory pipe and returns the
// decoded response. It waits for the handler to fully return — the response is
// encoded BEFORE the board broadcast, so this barrier keeps a test from racing the
// fan-out.
func callTicketCreate(t *testing.T, d *Daemon, msg *protocol.TicketCreateMessage) protocol.Response {
	t.Helper()
	server, client := net.Pipe()
	defer client.Close()
	done := make(chan struct{})
	go func() {
		d.handleTicketCreate(server, msg)
		_ = server.Close()
		close(done)
	}()
	var resp protocol.Response
	if err := json.NewDecoder(client).Decode(&resp); err != nil {
		t.Fatalf("decode ticket create response: %v", err)
	}
	<-done
	return resp
}

// A standalone create mints an unbound backlog ticket: status todo, no assignee, and
// a created event authored by the calling session.
func TestTicketCreateMintsUnboundTodo(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	resp := callTicketCreate(t, d, &protocol.TicketCreateMessage{
		Cmd:             protocol.CmdTicketCreate,
		SourceSessionID: "you",
		Title:           "Migrate store to X",
		Description:     protocol.Ptr("the brief"),
	})
	if !resp.Ok || resp.TicketCreateResult == nil {
		t.Fatalf("response = %+v, want ok with ticket create result", resp)
	}
	if resp.TicketCreateResult.Status != protocol.TicketStatusTodo {
		t.Fatalf("result status = %q, want todo", resp.TicketCreateResult.Status)
	}
	ticketID := resp.TicketCreateResult.TicketID

	ticket, err := d.store.GetTicket(ticketID)
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if ticket == nil {
		t.Fatalf("ticket %q not found after create", ticketID)
	}
	if ticket.Status != store.TicketStatusTodo {
		t.Fatalf("stored status = %q, want todo", ticket.Status)
	}
	if ticket.Assignee != "" {
		t.Fatalf("assignee = %q, want unbound (empty)", ticket.Assignee)
	}

	events, err := d.store.TicketEventsSince(0)
	if err != nil {
		t.Fatalf("TicketEventsSince: %v", err)
	}
	var created *store.TicketEvent
	for i := range events {
		if events[i].TicketID == ticketID && events[i].Kind == store.TicketEventCreated {
			created = &events[i]
		}
	}
	if created == nil {
		t.Fatalf("no created event for ticket %q", ticketID)
	}
	if created.Author != "you" {
		t.Fatalf("created event author = %q, want the source session %q", created.Author, "you")
	}
}

// A user-chosen id that is already taken is a hard fail — no auto-suffix — and the
// surfaced error is the ErrTicketIDTaken sentinel from the store path the handler
// calls.
func TestTicketCreateExplicitIDCollisionFails(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	if _, err := d.store.CreateTicket(store.Ticket{ID: "store-migration", Title: "seed"}, "you", time.Now()); err != nil {
		t.Fatalf("seed ticket: %v", err)
	}

	resp := callTicketCreate(t, d, &protocol.TicketCreateMessage{
		Cmd:             protocol.CmdTicketCreate,
		SourceSessionID: "you",
		Title:           "Store migration again",
		ID:              protocol.Ptr("store-migration"),
	})
	if resp.Ok || resp.Error == nil {
		t.Fatalf("response = %+v, want failure for a taken explicit id", resp)
	}
	if !strings.Contains(*resp.Error, "already taken") {
		t.Fatalf("error = %q, want the ErrTicketIDTaken guidance", *resp.Error)
	}

	// The handler surfaced the store's sentinel verbatim; confirm the same store path
	// returns ErrTicketIDTaken for that id.
	if _, err := d.store.CreateTicket(store.Ticket{ID: "store-migration", Title: "x"}, "you", time.Now()); !errors.Is(err, store.ErrTicketIDTaken) {
		t.Fatalf("CreateTicket on a taken id = %v, want ErrTicketIDTaken", err)
	}
}

// A title-derived slug collision auto-suffixes to a distinct id rather than failing;
// both tickets land in todo.
func TestTicketCreateDerivedSlugAutoSuffixes(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	first := callTicketCreate(t, d, &protocol.TicketCreateMessage{
		Cmd:             protocol.CmdTicketCreate,
		SourceSessionID: "you",
		Title:           "Migrate store to X",
	})
	if !first.Ok || first.TicketCreateResult == nil {
		t.Fatalf("first create = %+v, want ok", first)
	}
	if first.TicketCreateResult.TicketID != "migrate-store-to-x" {
		t.Fatalf("first id = %q, want migrate-store-to-x", first.TicketCreateResult.TicketID)
	}

	second := callTicketCreate(t, d, &protocol.TicketCreateMessage{
		Cmd:             protocol.CmdTicketCreate,
		SourceSessionID: "you",
		Title:           "Migrate store to X",
	})
	if !second.Ok || second.TicketCreateResult == nil {
		t.Fatalf("second create = %+v, want ok", second)
	}
	if second.TicketCreateResult.TicketID != "migrate-store-to-x-2" {
		t.Fatalf("second id = %q, want migrate-store-to-x-2 (auto-suffix)", second.TicketCreateResult.TicketID)
	}
	if first.TicketCreateResult.Status != protocol.TicketStatusTodo || second.TicketCreateResult.Status != protocol.TicketStatusTodo {
		t.Fatalf("statuses = %q/%q, want both todo", first.TicketCreateResult.Status, second.TicketCreateResult.Status)
	}
}
