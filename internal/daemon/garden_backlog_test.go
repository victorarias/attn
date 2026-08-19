package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/store"
)

// seedBacklogTicket writes one board ticket the way the pre-cutover CLI did.
func seedBacklogTicket(t *testing.T, d *Daemon, id, title, description string, status store.TicketStatus, assignee string) {
	t.Helper()
	if _, err := d.createTicketWithUniqueSlug(store.Ticket{
		Title:       title,
		Description: description,
		Status:      status,
		Assignee:    assignee,
	}, id, "chief", store.TicketRoleChiefOfStaff, nil, time.Now()); err != nil {
		t.Fatalf("seed ticket %s: %v", id, err)
	}
}

func gardenSeeds(t *testing.T, d *Daemon) []garden.Seed {
	t.Helper()
	read, err := d.readGarden()
	if err != nil {
		t.Fatalf("read garden: %v", err)
	}
	return read.seeds
}

// The whole unbound todo column becomes seeds, description and all, and each
// ticket leaves the board without losing its record.
func TestBacklogConversionPlantsUnboundTodosAsSeeds(t *testing.T) {
	d := newGardenDaemon(t)
	seedBacklogTicket(t, d, "wire-the-thing", "Wire the thing", "the whole brief", store.TicketStatusTodo, "")

	d.convertBacklogTicketsToSeeds()

	seeds := gardenSeeds(t, d)
	if len(seeds) != 1 {
		t.Fatalf("seeds after conversion = %d, want 1: %+v", len(seeds), seeds)
	}
	if seeds[0].Title != "Wire the thing" || seeds[0].Body != "the whole brief" {
		t.Fatalf("converted seed = %+v, want the ticket's title and description", seeds[0])
	}
	if seeds[0].Status != garden.StatusPlanted {
		t.Fatalf("converted seed status = %q, want planted — conversion invents no tender", seeds[0].Status)
	}

	notes, _, err := d.readNotes(seeds[0].ID, 10)
	if err != nil {
		t.Fatalf("read notes: %v", err)
	}
	if len(notes) != 1 || !strings.Contains(notes[0].Body, "wire-the-thing") {
		t.Fatalf("converted seed does not name the ticket it came from: %+v", notes)
	}

	ticket, err := d.store.GetTicket("wire-the-thing")
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if ticket.ArchivedAt == nil {
		t.Fatalf("converted ticket is still on the board: %+v", ticket)
	}
	if ticket.Description != "the whole brief" {
		t.Fatalf("converted ticket lost its record: %+v", ticket)
	}
}

// The pass runs on every boot, so a second run must find nothing left to do.
func TestBacklogConversionIsIdempotent(t *testing.T) {
	d := newGardenDaemon(t)
	seedBacklogTicket(t, d, "wire-the-thing", "Wire the thing", "the whole brief", store.TicketStatusTodo, "")

	d.convertBacklogTicketsToSeeds()
	d.convertBacklogTicketsToSeeds()

	if seeds := gardenSeeds(t, d); len(seeds) != 1 {
		t.Fatalf("seeds after two passes = %d, want 1: %+v", len(seeds), seeds)
	}
}

// Everything else on the board stays exactly where it is: a done ticket has no
// garden equivalent, and an assigned todo is somebody's.
func TestBacklogConversionLeavesBoundAndClosedTicketsAlone(t *testing.T) {
	d := newGardenDaemon(t)
	seedBacklogTicket(t, d, "held-todo", "Held todo", "somebody has this", store.TicketStatusTodo, "sess-a")
	seedBacklogTicket(t, d, "finished", "Finished", "already shipped", store.TicketStatusDone, "sess-a")
	seedBacklogTicket(t, d, "in-flight", "In flight", "being worked", store.TicketStatusWorking, "sess-a")

	d.convertBacklogTicketsToSeeds()

	if seeds := gardenSeeds(t, d); len(seeds) != 0 {
		t.Fatalf("conversion planted seeds it should not have: %+v", seeds)
	}
	for _, id := range []string{"held-todo", "finished", "in-flight"} {
		ticket, err := d.store.GetTicket(id)
		if err != nil {
			t.Fatalf("GetTicket %s: %v", id, err)
		}
		if ticket.ArchivedAt != nil {
			t.Fatalf("conversion archived %s: %+v", id, ticket)
		}
	}
}
