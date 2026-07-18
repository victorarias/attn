package daemon

import (
	"strconv"
	"time"

	"github.com/victorarias/attn/internal/store"
)

// ticketNudgePrompt is the fixed doorbell typed into a nudge-eligible agent: a
// bounded "go look" trigger, never event content. The agent then reads its own
// queue with `attn ticket inbox`. This is the doorbell rule — the daemon signals,
// it never streams the message into the PTY.
const ticketNudgePrompt = "📋 New ticket activity — run `attn ticket inbox` to catch up."

// defaultTicketBufferWindow is the interruption budget for every observer that
// is not currently assigned to the ticket. The assignee keeps the short nudge
// countdown because it is the agent actively doing the work.
const defaultTicketBufferWindow = 30 * time.Minute
const ticketWatchLeaseWindow = 5 * time.Second

func ticketWatchLeaseWindowFor(intervalMS *string) time.Duration {
	if intervalMS == nil {
		return ticketWatchLeaseWindow
	}
	milliseconds, err := strconv.ParseInt(*intervalMS, 10, 64)
	if err != nil || milliseconds <= 0 {
		return ticketWatchLeaseWindow
	}
	const maxDuration = time.Duration(1<<63 - 1)
	if milliseconds > int64(maxDuration/time.Millisecond) {
		return maxDuration
	}
	interval := time.Duration(milliseconds) * time.Millisecond
	grace := interval / 2
	if grace < time.Second {
		grace = time.Second
	}
	if interval > maxDuration-grace {
		return maxDuration
	}
	return interval + grace
}

func (d *Daemon) ticketBufferWindow() time.Duration {
	if d.ticketBufferWindowOverride > 0 {
		return d.ticketBufferWindowOverride
	}
	return defaultTicketBufferWindow
}

// ticketAttentionKey intentionally follows the durable chief role across chief
// session transfer. Other observers use their ordinary session identity.
func (d *Daemon) ticketAttentionKey(sessionID string) string {
	if d.isChiefOfStaffSession(sessionID) {
		return store.TicketRoleIdentity(store.TicketRoleChiefOfStaff)
	}
	return sessionID
}

func (d *Daemon) ticketDeadline(sessionID, ticketID string, unreadAt, now time.Time) (time.Time, bool, error) {
	ticket, err := d.store.GetTicket(ticketID)
	if err != nil || ticket == nil {
		return time.Time{}, false, err
	}
	if ticket.Assignee == sessionID {
		return now.Add(d.nudgeWindow()), true, nil
	}
	attention, found, err := d.store.TicketDeliveryAttention(d.ticketAttentionKey(sessionID))
	if err != nil {
		return time.Time{}, false, err
	}
	deadline := now.Add(d.nudgeWindow())
	if found {
		if buffered := attention.LastAttentionAt.Add(d.ticketBufferWindow()); buffered.After(deadline) {
			deadline = buffered
		}
	} else if !unreadAt.IsZero() {
		if buffered := unreadAt.Add(d.ticketBufferWindow()); buffered.After(deadline) {
			deadline = buffered
		}
	}
	return deadline, false, nil
}

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
	d.notifyUnreadTicketSession(sessionID, now)
}

// syncNudgeForState cancels a queued doorbell only while a session is waiting for
// approval. Leaving that state rechecks unread activity so a previously deferred
// nudge is armed as soon as it is safe.
func (d *Daemon) syncNudgeForState(sessionID, state string) {
	if !isNudgeDeliveryAllowed(state) {
		d.cancelNudgeCountdown(sessionID, "waiting for approval")
		return
	}
	go d.notifyUnreadTicketSession(sessionID, time.Now())
}

// notifyUnreadTicketSession rebuilds a deadline after an approval wait or daemon
// recovery, when there is no single triggering ticket. It derives the earliest
// eligible deadline from durable unread events instead of persisting a scheduler.
func (d *Daemon) notifyUnreadTicketSession(sessionID string, now time.Time) {
	if d.store == nil {
		return
	}
	session := d.store.Get(sessionID)
	if session == nil || !isNudgeDeliveryAllowed(string(session.State)) {
		return
	}
	var deadline time.Time
	immediate := false
	pending := make(map[int64]struct{})
	for _, observer := range d.ticketObserversForSession(sessionID) {
		events, err := d.store.UnreadTicketEventsFor(observer.ID, observer.AuthorID)
		if err != nil {
			d.logf("ticket notify rebuild: %s: %v", sessionID, err)
			return
		}
		for _, event := range events {
			pending[event.Seq] = struct{}{}
			candidate, assigned, err := d.ticketDeadline(sessionID, event.TicketID, event.CreatedAt, now)
			if err != nil {
				continue
			}
			if deadline.IsZero() || candidate.Before(deadline) {
				deadline, immediate = candidate, assigned
			}
		}
	}
	if !deadline.IsZero() {
		if d.debugLogging {
			d.logf("ticket delivery: observer=%s session=%s class=%s pending=%d deadline=%s channel=countdown outcome=armed", d.ticketAttentionKey(sessionID), sessionID, map[bool]string{true: "assignee", false: "buffered"}[immediate], len(pending), deadline.Format(time.RFC3339))
		}
		d.armNudgeCountdownAt(sessionID, deadline)
	}
}
