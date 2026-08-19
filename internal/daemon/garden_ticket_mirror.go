package daemon

import (
	"strings"
	"time"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/store"
)

// The garden is the only channel an agent reports through now: `attn ticket`'s
// write verbs are signposts. Work that was already ticket-bound when the
// cutover landed — an in-flight delegation, an automation run whose
// continuation, retention and crash classification are all keyed on its ticket
// — still has to move across the board and close there.
//
// So the daemon mirrors, in the direction the era runs: what a session does to
// its own seed lands on the ticket it is still bound to. Nothing at the CLI
// knows this happens, and nothing new is bound — a session with no ticket
// mirrors nothing, which is every session dispatched after the cutover. The
// mirror therefore drains itself as the in-flight board drains.
//
// This is the inverse of mirrorStatusOntoSeed, which carried the previous step
// (#946) while tickets were still the channel. The two cannot loop: both write
// through store/log helpers rather than through each other's handler.

// seedMoveTicketStatus maps a lifecycle move onto the column a ticket bound to
// the same work should be in. Every verb maps: a move with no column would be a
// silent hole in a board somebody is still reading.
func seedMoveTicketStatus(verb garden.Verb) (store.TicketStatus, bool) {
	switch verb {
	case garden.VerbTend, garden.VerbReplant:
		return store.TicketStatusWorking, true
	case garden.VerbPark:
		return store.TicketStatusBlocked, true
	case garden.VerbHarvest:
		return store.TicketStatusDone, true
	case garden.VerbWither:
		return store.TicketStatusFailed, true
	default:
		return "", false
	}
}

// mirrorSeedMoveOntoTicket moves the acting session's bound ticket to the column
// its lifecycle move implies.
//
// The mirror never fails the move. The seed already moved and it is the record
// that matters; losing the ticket echo is worth a log line, not an error handed
// to an agent that did exactly what it was told.
func (d *Daemon) mirrorSeedMoveOntoTicket(sessionID, seedID string, verb garden.Verb, reason string) {
	ticket, ok := d.mirrorTargetTicket(sessionID, seedID)
	if !ok {
		return
	}
	status, ok := seedMoveTicketStatus(verb)
	if !ok {
		return
	}
	if ticket.Status == status {
		return
	}
	comment := strings.TrimSpace(reason)
	if comment == "" {
		comment = string(verb) + "ed " + seedID
	}
	d.deliveryMu.Lock()
	updated, _, err := d.store.SetTicketStatusWithOptions(
		ticket.ID, status, d.ticketActorIdentity(sessionID), comment,
		mirrorTicketMutationOptions(), time.Now(),
	)
	d.deliveryMu.Unlock()
	if err != nil {
		d.logf("garden: mirroring %s of %s onto ticket %s: %v", verb, seedID, ticket.ID, err)
		return
	}
	// No observer nudge. The doorbell tells an agent to go read a ticket, and the
	// tender already wrote the same thing where anyone watching this work reads
	// it — the seed's log. Nudging twice for one report is noise.
	d.publishTicketFact(FactTicketStatusChanged, updated.ID)
}

// mirrorSeedNoteOntoTicket echoes a log entry onto the bound ticket's thread, so
// somebody reading the ticket of in-flight work sees the progress its tender is
// writing in the garden instead of a card that goes silent mid-flight.
func (d *Daemon) mirrorSeedNoteOntoTicket(sessionID, seedID, body string) {
	ticket, ok := d.mirrorTargetTicket(sessionID, seedID)
	if !ok {
		return
	}
	if strings.TrimSpace(body) == "" {
		return
	}
	d.deliveryMu.Lock()
	_, _, err := d.store.AddTicketCommentWithOptions(
		ticket.ID, d.ticketActorIdentity(sessionID), body,
		mirrorTicketMutationOptions(), time.Now(),
	)
	d.deliveryMu.Unlock()
	if err != nil {
		d.logf("garden: mirroring a note on %s onto ticket %s: %v", seedID, ticket.ID, err)
		return
	}
	d.publishTicketFact(FactTicketCommented, ticket.ID)
}

// mirrorTargetTicket answers with the ticket this session's garden move should
// echo onto, and only for a session acting on its own work: the seed it is
// dispatched at, and a ticket it is the assignee of. A peer noting somebody
// else's seed mirrors nothing — that is awareness, not a report about work it
// is doing — and a session with no ticket has nothing to echo to.
func (d *Daemon) mirrorTargetTicket(sessionID, seedID string) (*store.Ticket, bool) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || strings.TrimSpace(seedID) == "" {
		return nil, false
	}
	if bound, ok := d.gardenDispatchCrown(sessionID); !ok || bound != seedID {
		return nil, false
	}
	ticket, err := d.store.ActiveTicketForSession(sessionID)
	if err != nil {
		d.logf("garden: resolving the ticket bound to session %s: %v", sessionID, err)
		return nil, false
	}
	if ticket == nil || ticket.Assignee != sessionID {
		return nil, false
	}
	return ticket, true
}

// mirrorTicketMutationOptions carries no observers and no attention key on
// purpose. The read-before-you-write gate exists so an agent cannot answer a
// participant it has not read; the daemon echoing a move somebody already made
// in the garden is not that agent, and a gate here would silently drop the echo
// exactly when the ticket has news on it. Attention is left alone for the same
// reason the mirror does not nudge: the report already landed where it is read.
func mirrorTicketMutationOptions() store.TicketMutationOptions {
	return store.TicketMutationOptions{}
}
