package daemon

import (
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

// Cancelling a countdown.
//
// attn counts down to two things it does to an agent without being asked:
// closing a turn the user steered (auto_settle.go) and doorbelling pending ticket
// activity (nudge_countdown.go). They are unrelated mechanisms with unrelated
// re-arm rules, but from the user's side they are one thing — something is about
// to happen and they want it not to — so one command calls off either.
//
// Both live in the same pane header, and a session can be counting down to both
// at once (an unselected pane that is working with unread tickets shows two
// indicators). So this cancels everything the session has rather than making the
// caller name a kind: the user is answering what is in front of them, and nothing
// is cancelled that was not on screen.
//
// The two cancels keep their own semantics for how long the answer stands, which
// is where the mechanisms genuinely differ. See each one.

// handleCancelCountdown calls off every countdown running on a session. A session
// with none is a no-op — the shortcut behind this is pressed on whatever is
// visible, and a stale press must not be an error.
func (d *Daemon) handleCancelCountdown(msg *protocol.CancelCountdownMessage) {
	if d == nil || msg == nil {
		return
	}
	sessionID := strings.TrimSpace(msg.SessionID)
	if sessionID == "" {
		return
	}

	settleCancelled := d.cancelAutoSettleByUser(sessionID)
	nudgeCancelled := d.cancelNudgeCountdownByUser(sessionID)

	if !settleCancelled && !nudgeCancelled {
		return
	}
	// One broadcast for the whole cancel: both deadlines ride the same session
	// snapshot, so cancelling both is still a single wire message.
	d.broadcastSessionStateChanged(sessionID)
}
