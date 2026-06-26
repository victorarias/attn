package daemon

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ticketnotify"
)

// ticketNudgePrompt is the fixed doorbell typed into an idle agent that cannot
// self-monitor: a bounded "go look" trigger, never event content. The agent then
// reads its own queue with `attn ticket inbox`. This is the doorbell rule — the
// daemon signals, it never streams the message into the PTY.
const ticketNudgePrompt = "📋 New ticket activity — run `attn ticket inbox` to catch up."

// ticketNudger adapts the daemon's doorbell primitive to ticketnotify.Nudger.
type ticketNudger struct{ d *Daemon }

func (n ticketNudger) Nudge(observerID string) error {
	return n.d.typeDoorbell(observerID, ticketNudgePrompt)
}

// notifyTicketObservers runs the notification handler for every live session
// involved with a ticket after an event lands on it. A producer blanket-notifies
// without caring who caused the event: each observer sees only what it did not
// author (Notify -> Unread), so the author never notifies itself. Self-monitoring
// observers (Claude) resolve to DeliveryWatch — nothing injected, their own watch
// drains it; an idle non-self-monitor (codex) gets the fixed doorbell; a busy one
// is deferred until it next goes idle (see notifyTicketSessionWentIdle).
func (d *Daemon) notifyTicketObservers(ticketID string) {
	if d.ptyBackend == nil || d.store == nil {
		return
	}
	participants, err := d.store.TicketParticipants(ticketID)
	if err != nil {
		d.logf("ticket notify: participants for %s: %v", ticketID, err)
		return
	}
	now := time.Now()
	for _, id := range participants {
		d.notifyTicketSession(id, now)
	}
}

// notifyTicketSession runs Notify for one session's observer when it is a live
// session. A participant that is not a live session — the attn crash author, or a
// session already gone — is skipped: there is nothing to watch or nudge.
func (d *Daemon) notifyTicketSession(sessionID string, now time.Time) {
	session := d.store.Get(sessionID)
	if session == nil {
		return
	}
	obs := d.ticketObserverForSession(sessionID)
	idle := isIdleForNudge(string(session.State))
	if _, err := ticketnotify.Notify(d.store, obs, idle, ticketNudger{d}, now); err != nil {
		d.logf("ticket notify: %s: %v", sessionID, err)
	}
}

// notifyTicketSessionWentIdle flushes a deferred nudge: a non-self-monitor that was
// busy when an event landed gets its doorbell the moment it goes idle. Called from
// the state-transition path. A no-op when nothing is unread (so an agent that has
// already consumed is never re-nudged).
func (d *Daemon) notifyTicketSessionWentIdle(sessionID string) {
	if d.ptyBackend == nil || d.store == nil {
		return
	}
	d.notifyTicketSession(sessionID, time.Now())
}

// isIdleForNudge reports whether a session is at rest enough to receive a doorbell
// without interrupting work — the same guard the chief doorbell uses.
func isIdleForNudge(state string) bool {
	return state == protocol.StateIdle || state == protocol.StateWaitingInput
}
