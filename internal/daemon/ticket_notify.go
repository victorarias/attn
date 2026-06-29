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
//
// DeliveryWatch for an idle session is where the self-monitor backstop hooks in:
// the synchronous decision injects nothing (a live Monitor is assumed to be
// draining), but nothing guarantees one is armed, so we schedule a deferred
// re-check that doorbells only if the queue is still unread past the grace.
func (d *Daemon) notifyTicketSession(sessionID string, now time.Time) {
	session := d.store.Get(sessionID)
	if session == nil {
		return
	}
	obs := d.ticketObserverForSession(sessionID)
	idle := isIdleForNudge(string(session.State))
	delivery, err := ticketnotify.Notify(d.store, obs, idle, ticketNudger{d}, now)
	if err != nil {
		d.logf("ticket notify: %s: %v", sessionID, err)
		return
	}
	if delivery == ticketnotify.DeliveryWatch && idle {
		d.scheduleTicketBackstop(sessionID)
	}
}

// defaultTicketBackstopGrace is how long the daemon waits before doorbelling an
// idle self-monitor that still has unread ticket activity. It must exceed the
// `attn ticket inbox --watch` poll interval (ticketWatchInterval in cmd/attn, 3s)
// plus a round-trip margin: a live watch consumes the new event within one
// interval, so by the grace its queue is drained and the backstop self-suppresses.
// Keep these two constants in sync — shrinking the watch interval or growing it
// past this grace reintroduces the redundant-doorbell collision.
const defaultTicketBackstopGrace = 8 * time.Second

// scheduleTicketBackstop arms (or resets) the per-session deferred re-check. It
// debounces: a burst of events on a session's tickets collapses to one re-check,
// always grace after the latest event. The timer carries its own handle so a fire
// that lost a reschedule race can tell it has been superseded (ticketBackstopFire).
func (d *Daemon) scheduleTicketBackstop(sessionID string) {
	grace := d.ticketBackstopGrace
	if grace <= 0 {
		grace = defaultTicketBackstopGrace
	}
	d.ticketBackstopMu.Lock()
	defer d.ticketBackstopMu.Unlock()
	if d.ticketBackstopTimers == nil {
		d.ticketBackstopTimers = make(map[string]*time.Timer)
	}
	if t, ok := d.ticketBackstopTimers[sessionID]; ok {
		t.Stop()
	}
	// The AfterFunc needs its own *Timer to prove identity in ticketBackstopFire, but
	// that is the value being assigned here. ready is the synchronization edge: the
	// closure blocks until we publish `timer`, so its read happens-after the write
	// (no data race) even if a tiny grace fires the timer immediately.
	ready := make(chan struct{})
	var timer *time.Timer
	timer = time.AfterFunc(grace, func() {
		<-ready
		d.ticketBackstopFire(sessionID, timer)
	})
	d.ticketBackstopTimers[sessionID] = timer
	close(ready)
}

// ticketBackstopFire is the deferred re-check. It doorbells the session iff it is
// still idle and still has unread ticket activity — i.e. no live `--watch` Monitor
// drained the queue during the grace. A live watch (or the session going busy, or
// disappearing) leaves nothing to do. This is the only place an idle self-monitor
// is ever typed into; the synchronous Notify path stays a no-op for it.
//
// self is the timer whose expiry triggered this call. If a later event already
// rescheduled the backstop, the map holds a newer timer, so this stale fire bails
// (the newer timer will fire) — that identity check is what keeps the debounce to a
// single doorbell when a fire races a reschedule.
func (d *Daemon) ticketBackstopFire(sessionID string, self *time.Timer) {
	d.ticketBackstopMu.Lock()
	current := d.ticketBackstopTimers[sessionID] == self
	if current {
		delete(d.ticketBackstopTimers, sessionID)
	}
	d.ticketBackstopMu.Unlock()
	if current {
		d.ticketBackstopRecheck(sessionID)
	}
}

// ticketBackstopRecheck doorbells the session iff it is still idle and still has
// unread ticket activity — i.e. no live `--watch` Monitor drained the queue during
// the grace. A live watch (or the session going busy, or disappearing) leaves
// nothing to do. This is the only place an idle self-monitor is ever typed into;
// the synchronous Notify path stays a no-op for it.
func (d *Daemon) ticketBackstopRecheck(sessionID string) {
	if d.ptyBackend == nil || d.store == nil {
		return
	}
	session := d.store.Get(sessionID)
	if session == nil || !isIdleForNudge(string(session.State)) {
		return
	}
	obs := d.ticketObserverForSession(sessionID)
	unread, err := ticketnotify.Unread(d.store, obs)
	if err != nil {
		d.logf("ticket backstop: %s: %v", sessionID, err)
		return
	}
	if unread == 0 {
		return
	}
	if err := d.typeDoorbell(sessionID, ticketNudgePrompt); err != nil {
		d.logf("ticket backstop doorbell: %s: %v", sessionID, err)
	}
}

// stopTicketBackstops cancels every pending backstop re-check. Daemon.Stop() calls
// it so no AfterFunc goroutine outlives teardown.
func (d *Daemon) stopTicketBackstops() {
	d.ticketBackstopMu.Lock()
	defer d.ticketBackstopMu.Unlock()
	for id, t := range d.ticketBackstopTimers {
		t.Stop()
		delete(d.ticketBackstopTimers, id)
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
