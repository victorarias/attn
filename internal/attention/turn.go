// Package attention decides whose turn it is.
//
// A turn opens on a state and closes only when the user settles it. Nothing the
// agent does afterwards removes it: an agent you prompt goes back to work still
// on your list, because it went back to work at your instruction. That makes
// membership a comparison between two stamps rather than a reading of the
// current state — state matters only at the instant a turn opens.
//
// The package is pure. It derives over protocol values and timestamps and
// imports neither the store nor the daemon.
package attention

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// OpensTurn reports whether reaching this state starts a turn the user owes.
//
// It is the state vocabulary and nothing else: a state that opens a turn while
// one is already open changes nothing, because opening is guarded on the turn
// being closed.
//
//   - waiting_input, pending_approval, unknown open a turn. The first two are
//     the agent asking; unknown is the daemon admitting it cannot tell, which is
//     equally the user's problem.
//   - launching, working, scheduled never open one: the agent is busy or waiting
//     on a clock.
//   - recoverable never opens one either. The daemon revives it unattended, so
//     surfacing it would hand the user work the daemon is already doing.
func OpensTurn(state protocol.SessionState) bool {
	switch state {
	case protocol.SessionStateWaitingInput,
		protocol.SessionStatePendingApproval,
		protocol.SessionStateUnknown:
		return true
	default:
		return false
	}
}

// Input is everything Owed needs about one session.
type Input struct {
	OpenedAt  time.Time
	SettledAt time.Time

	// IsShell excludes terminal panes. A shell is a real store session
	// registered idle at birth and left there forever, so without this it would
	// sit in the queue permanently with nothing able to settle it.
	IsShell bool

	// ChiefOfStaff excludes the chief, which has its own anchored slot above the
	// queue rather than a place in it.
	ChiefOfStaff bool

	// WorkspacePinned and WorkspaceMuted filter at read, not at open: a pinned or
	// muted session still accumulates OpenedAt, so un-pinning surfaces whatever
	// was outstanding at its true age instead of starting it from nothing.
	WorkspacePinned bool
	WorkspaceMuted  bool
}

// Owed reports whether the user owes this session a turn.
func Owed(in Input) bool {
	if in.IsShell || in.ChiefOfStaff || in.WorkspacePinned || in.WorkspaceMuted {
		return false
	}
	return in.OpenedAt.After(in.SettledAt)
}
