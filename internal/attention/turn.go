// Package attention decides whose turn it is. A turn opens on a state and
// closes only when the user settles it — nothing the agent does afterwards
// removes it, so membership is a comparison of two stamps, not a reading of
// current state. The package is pure: it derives over protocol values,
// resolver reasons, and timestamps, importing neither store nor daemon.
package attention

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionstate"
)

// OpensTurn reports whether reaching this state starts a turn the user owes.
// Opening is guarded on the turn being closed, so re-opening changes nothing.
//
//   - waiting_input, pending_approval, unknown open one (unknown is the daemon
//     admitting it cannot tell — equally the user's problem).
//   - idle opens one too, covering both a finished unread run and a
//     never-spoken-to session at its prompt, indistinguishable on purpose.
//   - launching, working, scheduled never do (see sessionstate.settled for
//     cron-parked sessions); recoverable never does — the daemon revives it
//     unattended.
func OpensTurn(state protocol.SessionState) bool {
	switch state {
	case protocol.SessionStateWaitingInput,
		protocol.SessionStatePendingApproval,
		protocol.SessionStateUnknown,
		protocol.SessionStateIdle:
		return true
	default:
		return false
	}
}

// BreaksSnooze reports whether reaching this state cuts a snooze short.
// Business as usual stays deferred; only what the user could not have
// anticipated breaks through: unknown (the daemon can no longer explain the
// session), and idle with reason "process_exited" (the process actually died —
// ordinary finishing resolves idle via other reasons). The reason rides the
// wire as a string but is compared against the resolver's own constant, so a
// renamed reason cannot silently stop breaking through.
func BreaksSnooze(state protocol.SessionState, reason string) bool {
	switch state {
	case protocol.SessionStateUnknown:
		return true
	case protocol.SessionStateIdle:
		return reason == string(sessionstate.ReasonProcessExited)
	default:
		return false
	}
}

// Input is everything Owed needs about one session.
type Input struct {
	OpenedAt  time.Time
	SettledAt time.Time

	// IsShell excludes terminal panes: a shell is registered idle at birth and
	// left there forever, so it would sit in the queue with nothing to settle it.
	IsShell bool

	// ChiefOfStaff excludes the chief, which has its own anchored slot.
	ChiefOfStaff bool

	// SessionPinned excludes one pinned session, leaving its workspace and
	// siblings in the queue; the finer-grained half of WorkspacePinned.
	SessionPinned bool

	// WorkspacePinned and WorkspaceMuted filter at read, not at open: OpenedAt
	// still accumulates, so un-pinning surfaces the turn at its true age.
	WorkspacePinned bool
	WorkspaceMuted  bool
}

// Owed reports whether the user owes this session a turn.
func Owed(in Input) bool {
	if in.IsShell || in.ChiefOfStaff || in.SessionPinned || in.WorkspacePinned || in.WorkspaceMuted {
		return false
	}
	return in.OpenedAt.After(in.SettledAt)
}
