package daemon

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// isMidFlightCrashState reports whether a delegated session's pre-clobber runtime
// state means its process ended while still working — a crash or a kill — rather
// than at a clean rest. It mirrors the dispatch close-state classification: a
// cut-off from launching / working / pending_approval is a failure, while idle /
// waiting_input (and the ambiguous unknown) are neutral ends that leave the ticket
// wherever the agent last reported it.
func isMidFlightCrashState(state string) bool {
	switch state {
	case protocol.StateLaunching, protocol.StateWorking, protocol.StatePendingApproval:
		return true
	default:
		return false
	}
}

// captureTicketCrashState authors a Crashed status on a delegated session's bound
// ticket when the agent's process ends mid-flight without a terminal report.
// Crashed is the one ticket transition attn writes itself: the dead worker never
// got to say done/failed, so the board would otherwise show a stale Working/Blocked
// column forever.
//
// It is a no-op when the close was a clean rest, when the session has no bound
// non-terminal ticket (already reported a terminal status, or never a delegated
// session), and is naturally idempotent — once the ticket is Crashed it is
// terminal, so a later teardown read finds no active ticket. It runs alongside
// captureDispatchCloseState at every session-end path, fed the same pre-clobber
// state so it sees the real runtime before handlePTYExit's idle-clobber erases it.
func (d *Daemon) captureTicketCrashState(sessionID, state string) {
	if !isMidFlightCrashState(state) {
		return
	}
	ticket, err := d.store.ActiveTicketForSession(sessionID)
	if err != nil {
		d.logf("ticket crash capture for %s: %v", sessionID, err)
		return
	}
	if ticket == nil {
		return
	}
	if _, err := d.store.SetTicketStatus(
		ticket.ID,
		store.TicketStatusCrashed,
		store.TicketAuthorAttn,
		"agent process ended mid-flight without reporting",
		time.Now(),
	); err != nil {
		d.logf("ticket crash capture for %s: %v", sessionID, err)
		return
	}
	d.logf("ticket %q crashed: session %s ended mid-flight (%s)", ticket.ID, sessionID, state)
	// attn authored the crash; notify the chief (the crashed session is gone, so it
	// is skipped as a non-live participant).
	d.notifyTicketObservers(ticket.ID)
	// Refresh the app's board view: the ticket moved to the Crashed column.
	d.broadcastTicketsUpdated()
}
