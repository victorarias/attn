package daemon

import (
	"time"

	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/ticketnotify"
)

// ticketNudgePrompt is the fixed doorbell typed into a nudge-eligible agent: a
// bounded "go look" trigger, never event content. The agent then reads its own
// queue with `attn ticket inbox`. This is the doorbell rule — the daemon signals,
// it never streams the message into the PTY.
const ticketNudgePrompt = "📋 New ticket activity — run `attn ticket inbox` to catch up."

// ticketNudger adapts the daemon's doorbell primitive to ticketnotify.Nudger.
type ticketNudger struct{ d *Daemon }

func (n ticketNudger) Nudge(observerID string) error {
	// The immediate doorbell is gone: arm a visible, pausable countdown instead. The
	// countdown's timer fire is the only place a real doorbell happens, and only when
	// the user is not actively typing into the session (the anti-splice guard).
	n.d.armNudgeCountdown(observerID)
	return nil
}

// notifyTicketObservers runs the notification handler for every live session
// involved with a ticket after an event lands on it. A producer blanket-notifies
// without caring who caused the event: each observer sees only what it did not
// author (Notify -> Unread), so the author never notifies itself. All runtimes
// share one delivery policy: only an approval prompt blocks a countdown. An optional
// `ticket inbox --watch` may consume the unread activity before it rings.
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
	targets := make(map[string]bool, len(participants))
	for _, identity := range participants {
		id := identity
		if identity == store.TicketRoleIdentity(store.TicketRoleChiefOfStaff) {
			id = d.chiefOfStaffSessionID()
		}
		if id != "" {
			targets[id] = true
		}
	}
	for id := range targets {
		d.notifyTicketSession(id, now)
	}
}

// notifyTicketSession runs Notify for one session's observer when it is a live
// session. A participant that is not a live session — the attn crash author, or a
// session already gone — is skipped: there is nothing to nudge.
func (d *Daemon) notifyTicketSession(sessionID string, now time.Time) {
	session := d.store.Get(sessionID)
	if session == nil {
		return
	}
	// Reflect unread ticket activity on the session for the indicator, independent of
	// the delivery mechanism, so an active agent and an optional watcher both surface
	// the unread marker.
	d.refreshTicketUnread(sessionID)
	observers := d.ticketObserversForSession(sessionID)
	_, err := ticketnotify.NotifyAny(d.store, observers, observers[0], isNudgeDeliveryAllowed(string(session.State)), ticketNudger{d}, now)
	if err != nil {
		d.logf("ticket notify: %s: %v", sessionID, err)
	}
}

// syncNudgeForState cancels a queued doorbell only while a session is waiting for
// approval. Leaving that state rechecks unread activity so a previously deferred
// nudge is armed as soon as it is safe.
func (d *Daemon) syncNudgeForState(sessionID, state string) {
	if !isNudgeDeliveryAllowed(state) {
		d.cancelNudgeCountdown(sessionID, "waiting for approval")
		return
	}
	go d.notifyTicketSession(sessionID, time.Now())
}

// applyStateAndSyncNudge serializes an authoritative session-state write with a
// complete doorbell input. The lock establishes one order when a session reaches an
// approval prompt at the same time a countdown wants to send Enter: either the
// eligible doorbell is fully written first, or the approval state is committed first
// and the doorbell is suppressed. The follow-up reconciliation runs outside the lock
// because it may read tickets and arm timers.
func (d *Daemon) applyStateAndSyncNudge(sessionID, state string, apply func() bool) bool {
	d.doorbellMu.Lock()
	applied := apply()
	d.doorbellMu.Unlock()
	if applied {
		d.syncNudgeForState(sessionID, state)
	}
	return applied
}
