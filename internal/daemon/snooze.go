package daemon

import (
	"strings"
	"time"

	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/statetrace"
)

// Snooze is the queue's *not now*.
//
// Settle answers "I dealt with this"; nothing else answered "come back later",
// so an agent you could not act on yet had to be either settled — a lie, since
// the turn returns the moment it stops again — or left in the band making the
// queue mean less. Snooze closes the turn and stops the next one from opening
// until a deadline the user named.
//
// It suppresses turns at the moment they would *open*, rather than filtering
// them out at read the way the shell/chief/pinned/muted exclusions do. The
// difference is what the user sees when the deadline passes: a read filter keeps
// stamping, so the turn resurfaces at its original age and lands at the head of
// the band, while suppression means the turn opens at the wake instant and lands
// at the tail. The vision asks for the tail — you deferred it, so the clock on
// what you owe starts when you said you would come back.
//
// Because a snooze always settles as it is written, attention.Owed needs to know
// nothing about it: a snoozed session simply has no turn open. The deadline
// rides the wire only so the sidebar can park the row and say when it returns.
//
// The timers are in memory and the deadline is in the store, which is what makes
// a snooze survive a restart — see rescheduleSnoozeWakes.

// snoozeTimer is a session's pending wake. firesAt is kept beside the timer
// because time.Timer exposes no deadline accessor and the reschedule path needs
// to know whether a stored deadline is the one already armed.
type snoozeTimer struct {
	timer   *time.Timer
	firesAt time.Time
}

// handleSnoozeTurn defers a session until the instant the client computed.
//
// The client owns the arithmetic on purpose: "tomorrow" and "Monday" are
// calendar questions that need the user's timezone and locale, and a remote
// endpoint's daemon shares neither. The daemon takes an instant and schedules
// against it.
func (d *Daemon) handleSnoozeTurn(msg *protocol.SnoozeTurnMessage) {
	if d == nil || d.store == nil || msg == nil {
		return
	}
	sessionID := strings.TrimSpace(msg.SessionID)
	if sessionID == "" {
		return
	}
	until, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(msg.Until))
	if err != nil {
		d.logf("snooze rejected: session=%s bad until=%q: %v", sessionID, msg.Until, err)
		return
	}
	if !d.store.SnoozeTurn(sessionID, until, time.Now()) {
		return
	}
	// A snooze settles, so any countdown aimed at the same turn is moot — leaving
	// one running would promise a second settle on a turn that is already closed.
	d.cancelAutoSettle(sessionID, "snoozed")
	// A snooze is a settle with a reason, and the trace is where the pairing with
	// the state it closed survives.
	d.traceSettle(sessionID)
	d.scheduleSnoozeWake(sessionID, until)
	d.broadcastSessionStateChanged(sessionID)
}

// handleWakeTurn ends a snooze early because the user asked.
func (d *Daemon) handleWakeTurn(msg *protocol.WakeTurnMessage) {
	if d == nil || msg == nil {
		return
	}
	sessionID := strings.TrimSpace(msg.SessionID)
	if sessionID == "" {
		return
	}
	d.wakeSnooze(sessionID, time.Now(), "user")
}

// wakeSnooze clears a snooze and opens a turn if the session is sitting in a
// state that wants the user. `at` is the instant the turn is stamped with, which
// is the deadline for a timer wake and now for every other cause: a snooze that
// lapsed while the daemon was down has genuinely been owed since it lapsed, and
// the queue orders by how long you have owed something.
//
// Waking a session that is busy opens nothing. That is not a missed turn — the
// agent is working, and the next state that wants the user opens one normally,
// now that nothing is suppressing it.
func (d *Daemon) wakeSnooze(sessionID string, at time.Time, cause string) {
	if d == nil || d.store == nil || sessionID == "" {
		return
	}
	d.cancelSnoozeWake(sessionID)
	if !d.store.WakeTurn(sessionID) {
		// No snooze was live. Staying silent keeps a redundant wake — a timer that
		// lost a race with a hand wake, say — from broadcasting.
		return
	}
	if session := d.store.Get(sessionID); session != nil && attention.OpensTurn(session.State) {
		d.store.OpenTurnIfClosed(sessionID, d.turnOpensAtOnWake(sessionID, at))
	}
	if d.debugLogging {
		d.logf("snooze woken: session=%s cause=%s", sessionID, cause)
	}
	d.recordStateObservation(sessionID, statetrace.Observation{
		Source:  "user",
		Claim:   d.currentStateClaim(sessionID),
		Detail:  cause,
		Cause:   "wake",
		Outcome: statetrace.OutcomeApplied,
	})
	d.broadcastSessionStateChanged(sessionID)
}

// turnOpensAtOnWake is the instant a woken turn is stamped with.
//
// Normally that is the deadline: a snooze that lapsed at 11:00 has been owed
// since 11:00, whether or not the daemon was running to notice. But membership
// is `opened > settled`, and the settle that created the deferral is stamped at
// the moment it was pressed — so a deadline that is not after it produces a turn
// that reads as already closed, and the agent is silently lost rather than
// deferred. That happens for a snooze whose deadline was already stale when it
// arrived: clock skew across endpoints, or a client sending an instant it
// computed a while ago.
//
// Such a deferral was over before it began, so it ends when we notice, and the
// turn is owed from then.
func (d *Daemon) turnOpensAtOnWake(sessionID string, deadline time.Time) time.Time {
	if deadline.After(d.store.TurnStamps(sessionID).SettledAt) {
		return deadline
	}
	return time.Now()
}

// currentStateClaim is the session's state as a string, or "" if it is gone.
// The trace wants the state a wake landed on, the same way traceSettle wants the
// state a settle closed.
func (d *Daemon) currentStateClaim(sessionID string) string {
	session := d.store.Get(sessionID)
	if session == nil {
		return ""
	}
	return string(session.State)
}

// snoozeSuppressesTurn is the gate applyState consults before opening a turn.
//
// It reports whether this session is deferred past the state it just reached. A
// state that breaks through both returns false *and* clears the snooze, because
// a deferral interrupted by something the user could not have anticipated is
// over: they are back in the loop with that agent, and silently resuming the
// remainder later would re-assert an intent they have since moved past.
//
// It is called from inside applyState, after the state is committed and before
// the turn opens, so the break-through opens the very turn the state would have
// opened had nothing been deferred. The reason is read from the resolver's own
// record rather than threaded through sessionStateChange: publishResolution
// files it immediately before applying, so it already describes the state being
// committed, and a caller outside the resolver has no reason to supply.
func (d *Daemon) snoozeSuppressesTurn(sessionID string, state protocol.SessionState) bool {
	if d == nil || d.store == nil {
		return false
	}
	if d.store.TurnStamps(sessionID).SnoozedUntil.IsZero() {
		return false
	}
	reason := d.stateReasons().get(sessionID)
	if !attention.BreaksSnooze(state, reason) {
		return true
	}
	d.cancelSnoozeWake(sessionID)
	d.store.WakeTurn(sessionID)
	if d.debugLogging {
		d.logf("snooze broken: session=%s state=%s reason=%s", sessionID, state, reason)
	}
	return false
}

// scheduleSnoozeWake arms (or replaces) a session's wake timer. A deadline
// already in the past fires immediately, which is what makes a snooze that
// lapsed during a restart behave like one that lapsed while the daemon was up.
func (d *Daemon) scheduleSnoozeWake(sessionID string, until time.Time) {
	if d == nil || sessionID == "" {
		return
	}
	d.snoozeMu.Lock()
	defer d.snoozeMu.Unlock()
	d.scheduleSnoozeWakeLocked(sessionID, until)
}

func (d *Daemon) scheduleSnoozeWakeLocked(sessionID string, until time.Time) {
	if d.snoozeTimers == nil {
		d.snoozeTimers = make(map[string]*snoozeTimer)
	}
	if existing, ok := d.snoozeTimers[sessionID]; ok {
		existing.timer.Stop()
	}
	window := time.Until(until)
	if window < 0 {
		window = 0
	}
	// The ready channel is the handshake auto_settle.go and nudge_countdown.go
	// both use: the closure blocks until `timer` is published, so the identity
	// check in the fire path reads a fully written value even when a zero window
	// fires immediately.
	ready := make(chan struct{})
	var timer *time.Timer
	timer = time.AfterFunc(window, func() {
		<-ready
		d.fireSnoozeWake(sessionID, timer, until)
	})
	d.snoozeTimers[sessionID] = &snoozeTimer{timer: timer, firesAt: until}
	close(ready)
}

// fireSnoozeWake is the timer arriving. The identity check keeps a timer that
// lost a cancel-or-replace race from waking a session that has since been
// re-snoozed to a later deadline.
func (d *Daemon) fireSnoozeWake(sessionID string, self *time.Timer, deadline time.Time) {
	d.snoozeMu.Lock()
	entry, ok := d.snoozeTimers[sessionID]
	if !ok || entry.timer != self {
		d.snoozeMu.Unlock()
		return
	}
	delete(d.snoozeTimers, sessionID)
	d.snoozeMu.Unlock()

	d.wakeSnooze(sessionID, deadline, "deadline")
	if d.snoozeWakeHook != nil {
		d.snoozeWakeHook(sessionID)
	}
}

// cancelSnoozeWake drops a session's pending wake without touching the store.
func (d *Daemon) cancelSnoozeWake(sessionID string) {
	if d == nil {
		return
	}
	d.snoozeMu.Lock()
	defer d.snoozeMu.Unlock()
	if entry, ok := d.snoozeTimers[sessionID]; ok {
		entry.timer.Stop()
		delete(d.snoozeTimers, sessionID)
	}
}

// clearSnoozeState drops a removed session's timer. The store row goes with the
// session, so there is nothing to wake and nothing to broadcast.
func (d *Daemon) clearSnoozeState(sessionID string) {
	d.cancelSnoozeWake(sessionID)
}

// stopSnoozeTimers cancels every pending wake so no AfterFunc goroutine outlives
// daemon teardown.
func (d *Daemon) stopSnoozeTimers() {
	if d == nil {
		return
	}
	d.snoozeMu.Lock()
	defer d.snoozeMu.Unlock()
	for id, entry := range d.snoozeTimers {
		entry.timer.Stop()
		delete(d.snoozeTimers, id)
	}
}

// rescheduleSnoozeWakes rebuilds the wake timers from the store at start-up.
// The deadlines are persisted but the timers are not, so without this a snooze
// would survive a restart as a session that never comes back — the worst
// possible failure for a feature whose whole promise is that it returns.
func (d *Daemon) rescheduleSnoozeWakes() {
	if d == nil || d.store == nil {
		return
	}
	for sessionID, until := range d.store.SnoozedSessions() {
		d.scheduleSnoozeWake(sessionID, until)
	}
}

// decorateSessionWithSnooze stamps the broadcast clone with a live snooze
// deadline. A deadline in the past is left off: the wake is racing this
// broadcast, and announcing a snooze that has already lapsed would park the row
// in the snoozed section for as long as it took the timer to land.
func (d *Daemon) decorateSessionWithSnooze(session *protocol.Session) {
	if session == nil || d.store == nil {
		return
	}
	session.TurnSnoozedUntil = nil
	until := d.store.TurnStamps(session.ID).SnoozedUntil
	if until.IsZero() || !until.After(time.Now()) {
		return
	}
	session.TurnSnoozedUntil = protocol.Ptr(until.UTC().Format(time.RFC3339Nano))
}
