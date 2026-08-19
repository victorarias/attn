package daemon

import (
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/store"
)

// Backlog conversion, the cutover's one-time move
// (docs/plans/2026-08-14-garden-era-epic.md, ruled 2026-08-14).
//
// An unbound backlog todo is inert: nobody holds it, nothing reports to it, and
// `attn ticket new` — the only way to make one — is a signpost now. So the whole
// column converts to seeds in one pass, and the board is left with in-flight
// work alone, which drains itself.
//
// Idempotence is the archive: a converted ticket leaves the board, and the pass
// reads only unarchived todos. Nothing is destroyed — an archived ticket is
// still `attn ticket show`-able forever, and the seed carries a note naming the
// ticket it came from, so the two halves of the record point at each other.
//
// Done tickets are never touched. Neither is an assigned one: a todo with an
// assignee is somebody's, and taking it out from under them is not conversion.

// convertBacklogTicketsToSeeds runs the pass at startup and reports what it did.
// It runs on every boot rather than behind a one-shot marker because the guard
// is the work itself: with nothing left able to create an unbound todo, the
// second boot finds an empty column and the pass costs one indexed query.
//
// It never fails startup. A garden that cannot take a seed is a daemon that
// cannot delegate either, and that is already loud; a backlog left unconverted
// on top of it is a log line.
func (d *Daemon) convertBacklogTicketsToSeeds() {
	if d.store == nil {
		return
	}
	if err := d.requireHome(garden.Surface); err != nil {
		// An outpost holds no part of the garden and no tickets of its own worth
		// converting. Silent on purpose: this is not a failure, it is not here.
		return
	}
	pending, err := d.unboundBacklogTickets()
	if err != nil {
		d.logf("garden: reading the backlog to convert: %v", err)
		return
	}
	if len(pending) == 0 {
		return
	}
	converted := 0
	for _, ticket := range pending {
		seedID, err := d.convertBacklogTicket(ticket)
		if err != nil {
			d.logf("garden: converting backlog ticket %s: %v", ticket.ID, err)
			continue
		}
		converted++
		d.logf("garden: converted backlog ticket %s to seed %s (%q)", ticket.ID, seedID, ticket.Title)
	}
	d.logf("garden: backlog conversion done: %d of %d unbound todo ticket(s) are seeds now", converted, len(pending))
}

// unboundBacklogTickets is the column the pass converts: todo, on the board, and
// held by nobody.
func (d *Daemon) unboundBacklogTickets() ([]*store.Ticket, error) {
	tickets, err := d.store.ListTickets(store.TicketListFilter{Status: store.TicketStatusTodo})
	if err != nil {
		return nil, err
	}
	unbound := make([]*store.Ticket, 0, len(tickets))
	for _, ticket := range tickets {
		if ticket == nil || strings.TrimSpace(ticket.Assignee) != "" {
			continue
		}
		unbound = append(unbound, ticket)
	}
	return unbound, nil
}

// convertBacklogTicket plants one seed from one ticket and takes the ticket off
// the board. The seed is planted, not tended: an unbound todo had no owner and
// the conversion does not invent one — `attn seed ready` offers it to whoever
// picks it up.
//
// The close and archive come last. A crash between the plant and them re-converts
// on the next boot, which duplicates a seed; the other order loses the work
// outright, and a duplicate seed is something a person can wither.
func (d *Daemon) convertBacklogTicket(ticket *store.Ticket) (string, error) {
	title := strings.TrimSpace(ticket.Title)
	body := strings.TrimSpace(ticket.Description)
	if err := garden.ValidatePlant(title, body); err != nil {
		return "", err
	}
	schema, err := d.seedsCollection()
	if err != nil {
		return "", err
	}
	seed, _, err := d.mintAndPlant(*schema, garden.Seed{
		Title:    title,
		Body:     body,
		Status:   garden.StatusPlanted,
		StepSlug: garden.StepSlug(title),
		Edges:    []garden.Edge{},
		Vars:     []garden.Var{},
	})
	if err != nil {
		return "", err
	}
	if _, err := d.appendSeedNote(seed.ID,
		fmt.Sprintf("converted from backlog ticket `%s` at the garden cutover; the ticket is archived and still readable with `attn ticket show %s`", ticket.ID, ticket.ID),
		"", "", garden.NoteKindNote, nil,
	); err != nil {
		// The seed is the record and it exists. Losing its provenance line is worth
		// a log entry, not a re-conversion on the next boot.
		d.logf("garden: recording ticket %s as the origin of seed %s: %v", ticket.ID, seed.ID, err)
	}
	// Closed before archived, because archiving is only offered to a closed
	// ticket. `done` is the column that fits: as a ticket this one is finished —
	// what it tracked lives on the seed the comment names, which is where the
	// record continues.
	now := time.Now()
	if _, _, err := d.store.SetTicketStatusWithOptions(
		ticket.ID, store.TicketStatusDone, store.TicketAuthorAttn,
		fmt.Sprintf("converted to seed %s at the garden cutover; the work continues there", seed.ID),
		mirrorTicketMutationOptions(), now,
	); err != nil {
		return "", fmt.Errorf("close %s after planting %s: %w", ticket.ID, seed.ID, err)
	}
	if err := d.store.ArchiveTicket(ticket.ID, now); err != nil {
		return "", fmt.Errorf("archive %s after planting %s: %w", ticket.ID, seed.ID, err)
	}
	d.publishTicketFact(FactTicketChanged, ticket.ID)
	return seed.ID, nil
}
