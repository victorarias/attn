package daemon

import (
	"strings"
	"time"

	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/statetrace"
)

// Snooze is the queue's *not now*: it closes the turn and stops the next one
// from opening until a user-named deadline. It suppresses turns as they would
// OPEN, not at read like the shell/chief/pinned/muted exclusions, so a lapsed
// snooze resurfaces at the tail of the band and attention.Owed needs to know
// nothing about it. Timers are in memory, the deadline is in the store — see
// rescheduleSnoozeWakes.

// snoozeTimer is a session's pending wake. firesAt exists because time.Timer
// exposes no deadline accessor.
type snoozeTimer struct {
	timer   *time.Timer
	firesAt time.Time
}

// handleSnoozeTurn defers a session until the instant the client computed. The
// client owns the arithmetic on purpose: "tomorrow" needs the user's timezone
// and locale, which a remote endpoint's daemon shares neither of.
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
	// A snooze settles, so a countdown aimed at the same turn is moot.
	d.cancelAutoSettle(sessionID, "snoozed")
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

// wakeSnooze clears a snooze and opens a turn if the state wants the user. `at`
// stamps it: the deadline for a timer wake (one that lapsed while the daemon was
// down has been owed since then), now otherwise.
func (d *Daemon) wakeSnooze(sessionID string, at time.Time, cause string) {
	if d == nil || d.store == nil || sessionID == "" {
		return
	}
	d.cancelSnoozeWake(sessionID)
	if !d.store.WakeTurn(sessionID) {
		// No snooze was live; a redundant wake must not broadcast.
		return
	}
	d.finishSnoozeWake(sessionID, at, cause)
}

// finishSnoozeWake is everything a wake does once the deadline is cleared,
// shared by the hand wake and the timer.
func (d *Daemon) finishSnoozeWake(sessionID string, at time.Time, cause string) {
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

// turnOpensAtOnWake stamps a woken turn with the deadline — unless membership
// (`opened > settled`) would read it as already closed and silently lose the
// agent, in which case the turn is owed from now.
func (d *Daemon) turnOpensAtOnWake(sessionID string, deadline time.Time) time.Time {
	if deadline.After(d.store.TurnStamps(sessionID).SettledAt) {
		return deadline
	}
	return time.Now()
}

// currentStateClaim is the session's state as a string, or "" if it is gone.
func (d *Daemon) currentStateClaim(sessionID string) string {
	session := d.store.Get(sessionID)
	if session == nil {
		return ""
	}
	return string(session.State)
}

// snoozeSuppressesTurn is the gate applyState consults after the state commits
// and before the turn opens. A break-through state returns false AND clears the
// snooze, so the break-through opens the very turn the state would have; the
// reason comes from the resolver's record, filed immediately before applying.
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

// scheduleSnoozeWake arms (or replaces) a session's wake timer; a past deadline
// fires immediately, so a snooze that lapsed during a restart still wakes.
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
	// Same ready-channel handshake as auto_settle.go: the closure blocks until
	// `timer` is published, so the fire path's identity check reads a fully
	// written value even when a zero window fires immediately.
	ready := make(chan struct{})
	var timer *time.Timer
	timer = time.AfterFunc(window, func() {
		<-ready
		d.fireSnoozeWake(sessionID, timer, until)
	})
	d.snoozeTimers[sessionID] = &snoozeTimer{timer: timer, firesAt: until}
	close(ready)
}

// fireSnoozeWake is the timer arriving. Two staleness checks: the identity check
// under the lock catches a lost cancel-or-replace race, and WakeTurnAt catches a
// replacement made in the gap after the lock is dropped — clearing
// unconditionally there would wake the agent instead of at the later deadline.
func (d *Daemon) fireSnoozeWake(sessionID string, self *time.Timer, deadline time.Time) {
	d.snoozeMu.Lock()
	entry, ok := d.snoozeTimers[sessionID]
	if !ok || entry.timer != self {
		d.snoozeMu.Unlock()
		return
	}
	delete(d.snoozeTimers, sessionID)
	d.snoozeMu.Unlock()

	// Fires however this ends: the only thing a test can wait on without knowing
	// which snooze won.
	if d.snoozeWakeHook != nil {
		defer d.snoozeWakeHook(sessionID)
	}
	if d.snoozeWakeGapHook != nil {
		d.snoozeWakeGapHook(sessionID)
	}
	if d.store == nil {
		return
	}
	if !d.store.WakeTurnAt(sessionID, deadline) {
		if d.debugLogging {
			d.logf("snooze wake superseded: session=%s deadline=%s", sessionID, deadline.UTC().Format(time.RFC3339Nano))
		}
		return
	}
	d.finishSnoozeWake(sessionID, deadline, "deadline")
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

// rescheduleSnoozeWakes rebuilds the wake timers from the store at start-up;
// without it a snooze survives a restart as a session that never comes back.
func (d *Daemon) rescheduleSnoozeWakes() {
	if d == nil || d.store == nil {
		return
	}
	for sessionID, until := range d.store.SnoozedSessions() {
		d.scheduleSnoozeWake(sessionID, until)
	}
}

// decorateSessionWithSnooze stamps the broadcast clone with a live snooze
// deadline. A lapsed deadline is left off: the wake is racing this broadcast,
// and announcing it would park the row snoozed until the timer lands.
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
