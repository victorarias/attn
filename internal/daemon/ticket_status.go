package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// ticketStatusFromWorkState maps the work state an agent reports onto the ticket
// column its bound ticket should move to. This is the agent's forward channel:
// the agent describes its own state, attn projects that onto the board. `crashed`
// and `todo` are intentionally unreachable here — crashed is attn-authored when an
// agent dies without reporting, and todo is the pre-assignment backlog state.
func ticketStatusFromWorkState(ws protocol.DispatchWorkState) (store.TicketStatus, bool) {
	switch ws {
	case protocol.DispatchWorkStateInProgress:
		return store.TicketStatusWorking, true
	case protocol.DispatchWorkStateNeedsInput:
		return store.TicketStatusBlocked, true
	case protocol.DispatchWorkStateReadyForReview:
		return store.TicketStatusInReview, true
	case protocol.DispatchWorkStateCompleted:
		return store.TicketStatusDone, true
	case protocol.DispatchWorkStateFailed:
		return store.TicketStatusFailed, true
	default:
		return "", false
	}
}

// handleSetTicketStatus moves a ticket to the column implied by the reported
// work state. Two forms share this handler: without a ticket id, the session
// is treated as the assignee and the daemon resolves its bound ticket — an
// agent can only move its own active ticket this way. With a ticket id, the
// daemon moves that ticket directly and skips session resolution entirely;
// this form is deliberately permissive — any session may move any ticket on
// the board, no ownership gate — because it is meant for awareness (the chief
// or a peer nudging the board), not for granting autonomy over someone else's
// work. Either way the acting session is recorded as the activity's author, so
// the change reads as attributed to whoever actually moved it.
func (d *Daemon) handleSetTicketStatus(conn net.Conn, msg *protocol.SetTicketStatusMessage) {
	sourceSessionID := strings.TrimSpace(msg.SourceSessionID)
	if sourceSessionID == "" {
		d.sendError(conn, "ticket status: source_session_id is required")
		return
	}
	status, ok := ticketStatusFromWorkState(msg.WorkState)
	if !ok {
		d.sendError(conn, fmt.Sprintf("ticket status: unknown work state %q", msg.WorkState))
		return
	}
	ticketID := strings.TrimSpace(protocol.Deref(msg.TicketID))
	if ticketID == "" {
		ticket, err := d.store.ActiveTicketForSession(sourceSessionID)
		if err != nil {
			d.sendError(conn, "ticket status: "+err.Error())
			return
		}
		if ticket == nil {
			d.sendError(conn, "ticket status: no active ticket bound to this session")
			return
		}
		ticketID = ticket.ID
	}
	comment := ""
	if msg.Comment != nil {
		comment = strings.TrimSpace(*msg.Comment)
	}
	d.deliveryMu.Lock()
	author := d.ticketActorIdentity(sourceSessionID)
	updated, outcome, err := d.store.SetTicketStatusWithOptions(
		ticketID, status, author, comment,
		d.ticketMutationOptions(sourceSessionID), time.Now(),
	)
	if err != nil {
		d.deliveryMu.Unlock()
		d.sendError(conn, "ticket status: "+err.Error())
		return
	}
	result := &protocol.TicketStatusResult{
		TicketID: ticketID,
		Status:   protocol.TicketStatus(status),
		CatchUp:  ticketMutationCatchUp(ticketID, outcome.CatchUp),
		Applied:  !outcome.Blocked,
	}
	d.afterTicketMutationCatchUpLocked(sourceSessionID, outcome.CatchUp)
	if outcome.Blocked {
		if current, getErr := d.store.GetTicket(ticketID); getErr == nil && current != nil {
			result.Status = protocol.TicketStatus(current.Status)
		}
		d.deliveryMu.Unlock()
		_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, TicketStatusResult: result})
		return
	}
	result.TicketID = updated.ID
	result.Status = protocol.TicketStatus(updated.Status)
	d.deliveryMu.Unlock()
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:                 true,
		TicketStatusResult: result,
	})
	d.mirrorStatusOntoSeed(sourceSessionID, updated, msg.WorkState, comment)
	// The agent moved its own ticket; notify the other observers (the chief) so the
	// board→watcher direction reflects it. The agent itself authored the event, so
	// Notify excludes it — no self-nudge.
	d.notifyTicketObservers(updated.ID)
	// Refresh the app's board view: the column moved.
	d.publishTicketFact(FactTicketStatusChanged, updated.ID)
}

// mirrorStatusOntoSeed writes the reported work state onto the log of the seed
// this session is bound to, so the seed carries a faithful thread of the work
// while tickets are still the channel the agent reports through. The CLI is
// unchanged and knows nothing about seeds: the daemon mirrors.
//
// It mirrors a session reporting on its own work and nothing else. The explicit
// `--ticket <id>` form is deliberately permissive — any session may nudge any
// ticket's column for awareness — and a peer moving somebody else's card is not
// a status report about work it is doing.
//
// The mirror never fails the report. The ticket already moved; losing the note
// is worth a log line, not an error handed to an agent that did nothing wrong.
//
// The seed's own lifecycle does not move with the column. A delegate reporting
// `completed` writes a note; closing a seed stays the deliberate
// `attn seed harvest`, with what got done in its own words.
func (d *Daemon) mirrorStatusOntoSeed(sessionID string, ticket *store.Ticket, state protocol.DispatchWorkState, comment string) {
	if ticket == nil || ticket.Assignee != sessionID {
		return
	}
	seedID, ok := d.gardenDispatchCrown(sessionID)
	if !ok {
		return
	}
	if _, err := d.appendSeedNote(seedID, statusNoteBody(state, comment), sessionID, "", garden.NoteKindNote, nil); err != nil {
		d.logf("garden: mirroring %s onto seed %s: %v", state, seedID, err)
	}
}

// statusNoteBody renders one reported state as a log line. The state leads,
// because that is what a reader scanning the log is looking for; the comment is
// the report itself and follows on its own paragraph.
func statusNoteBody(state protocol.DispatchWorkState, comment string) string {
	line := fmt.Sprintf("reported %s", state)
	if comment = strings.TrimSpace(comment); comment != "" {
		return line + "\n\n" + comment
	}
	return line
}
