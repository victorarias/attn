package daemon

import (
	"time"

	"github.com/victorarias/attn/internal/store"
)

// reviveCrashedTicketsForSession is the inverse half of crashTicket
// (ticket_crash.go): a session stamped Crashed can be brought back to life —
// the app reloads/respawns the dead pane, a ticket Resume respawns under the
// same id, the CLI re-registers it, or daemon recovery adopts a still-live
// worker — and from that moment the board's Crashed column is a lie the agent
// cannot correct (an agent only reports through in-progress/blocked/... work
// states; nothing routinely re-reports right after a revival). So the same
// authority that stamped Crashed un-stamps it: attn moves the ticket back to
// Working when its owning session revives.
//
// Re-arming crash detection falls out of the flip itself: `crashed` is
// terminal, so a ticket left there is invisible to ActiveTicketsForSession and
// a second death would never re-stamp it; back in `working` the ticket is on
// the crash/reconcile seam's radar again. Idempotent — a session with no
// crashed tickets is a no-op — and safe to run from any revival seam, in any
// order, any number of times. Callers (all three "session came back to life"
// seams): handleSpawnSession, handleRegister, and the recovery-adopt path in
// reconcileSessionsWithWorkerBackend.
func (d *Daemon) reviveCrashedTicketsForSession(sessionID string) {
	if d.store == nil {
		return
	}
	tickets, err := d.store.CrashedTicketsForAssignee(sessionID)
	if err != nil {
		d.logf("ticket revive: list crashed tickets for %s: %v", sessionID, err)
		return
	}
	if len(tickets) == 0 {
		return
	}
	moved := false
	for _, ticket := range tickets {
		if ticket == nil {
			continue
		}
		if _, err := d.store.SetTicketStatus(
			ticket.ID,
			store.TicketStatusWorking,
			store.TicketAuthorAttn,
			"session was reloaded and is running again",
			time.Now(),
		); err != nil {
			d.logf("ticket revive for %s: %v", sessionID, err)
			continue
		}
		moved = true
		d.logf("ticket %q revived: session %s is live again", ticket.ID, sessionID)
		// attn authored the move; notify the chief/participants the work is back on.
		d.notifyTicketObservers(ticket.ID)
	}
	if moved {
		// Refresh the app's board view: the ticket left the Crashed lane.
		d.broadcastTicketsUpdated()
	}
}
