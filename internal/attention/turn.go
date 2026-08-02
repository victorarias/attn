// Package attention decides whose turn it is.
//
// A turn opens on a state and closes only when the user settles it. Nothing the
// agent does afterwards removes it: an agent you prompt goes back to work still
// on your list, because it went back to work at your instruction. That makes
// membership a comparison between two stamps rather than a reading of the
// current state — state matters only at the instant a turn opens.
//
// The package is pure. It derives over protocol values, resolver reasons, and
// timestamps, and imports neither the store nor the daemon.
package attention

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionstate"
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
//   - idle opens one too, and it covers two cases that are indistinguishable
//     here on purpose. A run you asked for finished and nobody has read the
//     result; that it ended without a question makes it no less yours. And a
//     session you launched and have not yet spoken to sits at its prompt in the
//     same state — the purest turn there is, since nothing will ever happen in
//     it until you type.
//   - launching, working, scheduled never open one: the agent is busy or waiting
//     on a clock. A session parked on a cron is not among them — see
//     sessionstate.settled — so a loop you want left alone is silenced by pinning
//     its workspace, which is filtered below.
//   - recoverable never opens one either. The daemon revives it unattended, so
//     surfacing it would hand the user work the daemon is already doing.
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
//
// Snoozing says *not now*, and business as usual does not undo it: the agent
// stopping, asking a question, wanting an approval, or finishing its run are all
// things the user was deferring when they pressed it. What breaks through is what
// they could not have anticipated.
//
//   - unknown is the daemon admitting it cannot explain the session (reasons
//     "stuck" and "no_evidence"). A deferral is a judgement about an agent the
//     user understood; this is the daemon saying that judgement no longer has
//     anything behind it.
//   - idle with reason "process_exited" is the agent's process actually gone.
//     A run that merely ended resolves idle through "prompt_idle" or
//     "classifier_verdict", so ordinary finishing stays deferred and only dying
//     rings.
//
// The reason arrives as a plain string because that is how it rides the wire and
// sits in the store, but it is compared against the resolver's own constant: the
// set is defined by what the resolver means, and a renamed reason must not
// silently stop breaking through.
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
