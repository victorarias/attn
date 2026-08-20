package daemon

import (
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/store"
)

// The half of the cutover that was left undone.
//
// convertBacklogTicketsToSeeds moved the unbound todo column into the garden
// and left in-flight tickets on the board, on the argument that they drain
// themselves. That holds for the ones whose sessions live. It fails for the
// ones whose sessions died: attn stamps the ticket crashed (ticket_crash.go),
// nobody is left to report to it, and it sits on a surface the garden era gives
// nobody a reason to open. A user reads the garden, sees her converted todos,
// and concludes the rest of her work is gone. One did, on 2026-08-20.
//
// A crashed ticket is as inert as an unbound todo was, so it converts by the
// same argument and through the same steps: plant the seed, close the ticket,
// archive it. Idempotence is the archive; nothing is destroyed, and the two
// halves of the record point at each other.
//
// What is different is the tender. A crashed ticket names the session that died
// and `reviveCrashedTicketsForSession` exists to un-stamp it when that session
// comes back — a flip attn has to author because ticket status is stored. The
// garden needs no such seam: a seed's hold is derived from session liveness
// (garden.Tender.Holds), so planting the seed tended by the dead session makes
// it ready for anyone right now AND hands it straight back if that session ever
// revives. The binding survives the migration without a line of code to move
// it.

// replantStrandedTickets runs at startup, after the backlog pass, and reports
// what it did. Like that pass it runs every boot rather than behind a marker:
// the guard is the archive, so a drained board costs one indexed query.
//
// It never fails startup. A garden that cannot take a seed is already loud.
func (d *Daemon) replantStrandedTickets() {
	if d.store == nil {
		return
	}
	if err := d.requireHome(garden.Surface); err != nil {
		// An outpost holds no part of the garden and no board worth converting.
		return
	}
	stranded, err := d.store.StrandedTickets()
	if err != nil {
		d.logf("garden: reading the tickets stranded on the retired board: %v", err)
		return
	}
	if len(stranded) == 0 {
		return
	}
	replanted := 0
	for _, ticket := range stranded {
		if d.replantStrandedTicketByID(ticket.ID) {
			replanted++
		}
	}
	d.logf("garden: %d of %d stranded ticket(s) are seeds now", replanted, len(stranded))
}

// replantStrandedTicketByID replants one ticket if it is still stranded, for the
// seam that runs when a death is reconciled rather than at boot. Silent when the
// ticket has moved on: a verdict that settled it is somebody's answer, and the
// pass only claims work nobody answered for.
//
// It reads the whole ticket rather than a list row: the provenance note carries
// the reconciler's verdict, which lives on the activity thread.
func (d *Daemon) replantStrandedTicketByID(ticketID string) bool {
	if d.store == nil {
		return false
	}
	if err := d.requireHome(garden.Surface); err != nil {
		return false
	}
	ticket, err := d.store.GetTicket(ticketID)
	if err != nil || ticket == nil {
		if err != nil {
			d.logf("garden: reading stranded ticket %s: %v", ticketID, err)
		}
		return false
	}
	if !isStrandedTicket(ticket) {
		return false
	}
	seedID, err := d.replantStrandedTicket(ticket)
	if err != nil {
		d.logf("garden: replanting stranded ticket %s: %v", ticket.ID, err)
		return false
	}
	d.logf("garden: replanted stranded ticket %s as seed %s (%q)", ticket.ID, seedID, ticket.Title)
	return true
}

// isStrandedTicket is StrandedTickets' rule against one record, so the boot pass
// and the per-death seam cannot drift apart.
func isStrandedTicket(ticket *store.Ticket) bool {
	if ticket == nil || ticket.ArchivedAt != nil || strings.TrimSpace(ticket.AutomationRunID) != "" {
		return false
	}
	return ticket.Status == store.TicketStatusCrashed || ticket.Status == store.TicketStatusFailed
}

// replantStrandedTicket plants one seed from one stranded ticket and takes the
// ticket off the board.
//
// Where it lands says what happened to it. Crashed work was cut off with nobody
// deciding it was over, so it lands growing and held by the session that died —
// ready for whoever picks it up, and back in that session's hands if it revives.
// Failed work is a decision its agent already made, so it lands withered with
// the reason: on the record, out of `ready`, and replantable by anyone who
// disagrees.
//
// The plant comes first and the archive last, as in the backlog pass: a crash
// between them re-converts next boot, which duplicates a seed, and a duplicate
// is something a person can wither. The other order loses the work.
func (d *Daemon) replantStrandedTicket(ticket *store.Ticket) (string, error) {
	title := strings.TrimSpace(ticket.Title)
	body := strings.TrimSpace(ticket.Description)
	if err := garden.ValidatePlant(title, body); err != nil {
		return "", err
	}
	schema, err := d.seedsCollection()
	if err != nil {
		return "", err
	}
	plant := garden.Seed{
		Title:    title,
		Body:     body,
		Status:   garden.StatusGrowing,
		StepSlug: garden.StepSlug(title),
		Edges:    []garden.Edge{},
		Vars:     []garden.Var{},
	}
	if ticket.Status == store.TicketStatusFailed {
		plant.Status = garden.StatusWithered
		plant.Reason = "reported failed before the garden; replanted from ticket " + ticket.ID
	} else {
		// Only a crashed ticket carries a tender. The session is gone, so the seed
		// is ready; if it ever comes back, the hold returns with it.
		plant.TenderSession = strings.TrimSpace(ticket.Assignee)
	}
	seed, _, err := d.mintAndPlant(*schema, plant)
	if err != nil {
		return "", err
	}
	if _, err := d.appendSeedNote(seed.ID, strandedProvenanceNote(ticket), "", "", garden.NoteKindNote, nil); err != nil {
		// The seed is the record and it exists. Losing the provenance line is worth
		// a log entry, not leaving the ticket on the board for another pass.
		d.logf("garden: recording ticket %s as the origin of seed %s: %v", ticket.ID, seed.ID, err)
	}
	now := time.Now()
	if _, _, err := d.store.SetTicketStatusWithOptions(
		ticket.ID, store.TicketStatusDone, store.TicketAuthorAttn,
		fmt.Sprintf("replanted as seed %s; the work continues in the garden", seed.ID),
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

// strandedProvenanceNote is what the seed's log says about where it came from:
// how the work ended, which session it died in, and how to read the rest.
//
// It carries the reconciler's verdict when there is one. That verdict is the
// whole decision-support for picking this up — what the classifier thought the
// dead agent had actually done — and a person reading the seed should not have
// to know the old board exists to see it.
func strandedProvenanceNote(ticket *store.Ticket) string {
	origin := fmt.Sprintf("replanted from ticket `%s`, which crashed: its session ended mid-flight without reporting.", ticket.ID)
	if ticket.Status == store.TicketStatusFailed {
		origin = fmt.Sprintf("replanted from ticket `%s`, whose agent reported it failed.", ticket.ID)
	}
	lines := []string{origin}
	if session := strings.TrimSpace(ticket.Assignee); session != "" {
		lines = append(lines, fmt.Sprintf("It ran in session `%s`.", session))
	}
	if verdict := reconcileVerdictComment(ticket); verdict != "" {
		lines = append(lines, "", verdict)
	}
	lines = append(lines, "", fmt.Sprintf("The ticket is archived and still readable in full with `attn ticket show %s`.", ticket.ID))
	return strings.Join(lines, "\n")
}

// reconcileVerdictComment is the newest reconciliation verdict on a ticket, or
// "" when the classifier never got to run — a dead session with no transcript,
// a disabled queue, or a death older than the reconciler.
func reconcileVerdictComment(ticket *store.Ticket) string {
	verdict := ""
	for _, entry := range ticket.Activity {
		if entry.Kind != store.TicketActivityComment {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(entry.Comment), ticketReconcileCommentPrefix) {
			verdict = strings.TrimSpace(entry.Comment)
		}
	}
	return verdict
}
