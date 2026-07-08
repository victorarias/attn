package daemon

import (
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// defaultNudgeCountdownWindow is how long an armed ticket nudge waits — visible to
// the user as a countdown — before the daemon doorbells an idle session. The user
// can switch to the session and trigger it early, or let it elapse so attn delivers
// it. It gates every ticket doorbell so a nudge is never a silent surprise.
const defaultNudgeCountdownWindow = 30 * time.Second

// userInputGuardWindow is the anti-splice guarantee. At fire time the daemon refuses
// to doorbell a session that received a genuine user keystroke within this window,
// because the doorbell prompt + Enter would splice onto the user's half-typed line
// and submit the garbled combination — the original bug. This is per-session and so
// is immune to which tile is "selected"; selection only drives the paused-while-
// active UX, this drives correctness.
const userInputGuardWindow = 3 * time.Second

// nudgeCountdown is a per-session armed countdown. firesAt is stored alongside the
// timer because time.Timer exposes no deadline accessor and the absolute deadline is
// what rides the wire (nudge_fires_at) for the client to animate against.
type nudgeCountdown struct {
	timer   *time.Timer
	firesAt time.Time
}

// nudgeWindow is the countdown duration, with a test override for determinism.
func (d *Daemon) nudgeWindow() time.Duration {
	if d.nudgeWindowOverride > 0 {
		return d.nudgeWindowOverride
	}
	return defaultNudgeCountdownWindow
}

// armNudgeCountdown is the single entry every ticket doorbell now routes through.
// Instead of injecting immediately it marks the session's ticket activity unread and
// starts a visible countdown; the timer fire (nudgeCountdownFire) is the only place a
// real doorbell happens. The currently-selected session never auto-fires: its
// countdown is paused (no timer, no nudge_fires_at) and the UI offers click-to-
// trigger; switching away resumes it via setSelectedSession.
func (d *Daemon) armNudgeCountdown(sessionID string) {
	if sessionID == "" {
		return
	}
	active := d.currentlySelectedSession() == sessionID

	d.nudgeMu.Lock()
	changed := d.setUnreadLocked(sessionID, true)
	if active {
		// Paused while active: ensure no running timer and no deadline on the wire.
		changed = d.stopCountdownLocked(sessionID) || changed
	} else if _, running := d.nudgeCountdowns[sessionID]; !running {
		// Debounce: an already-running countdown keeps its original deadline rather
		// than resetting (a burst of ticket events must not sawtooth the bar).
		d.startCountdownLocked(sessionID, d.nudgeWindow())
		changed = true
	}
	d.nudgeMu.Unlock()

	if changed {
		d.broadcastSessionStateChanged(sessionID)
	}
}

// startCountdownLocked creates the AfterFunc and records {timer, firesAt}. Caller
// holds nudgeMu. The ready channel is the synchronization edge: the closure blocks
// until we publish `timer`, so the identity check in nudgeCountdownFire reads a fully
// written value even if a tiny (test) window fires the timer immediately — the same
// handshake scheduleTicketBackstop uses.
func (d *Daemon) startCountdownLocked(sessionID string, window time.Duration) {
	if d.nudgeCountdowns == nil {
		d.nudgeCountdowns = make(map[string]*nudgeCountdown)
	}
	if existing, ok := d.nudgeCountdowns[sessionID]; ok {
		existing.timer.Stop()
	}
	ready := make(chan struct{})
	var timer *time.Timer
	timer = time.AfterFunc(window, func() {
		<-ready
		d.nudgeCountdownFire(sessionID, timer)
	})
	d.nudgeCountdowns[sessionID] = &nudgeCountdown{timer: timer, firesAt: time.Now().Add(window)}
	close(ready)
}

// stopCountdownLocked cancels and forgets a session's running countdown. Caller holds
// nudgeMu. Returns whether there was one (so the caller knows to rebroadcast the now-
// absent nudge_fires_at). It does NOT touch unread — a paused/working session keeps
// its indicator until the agent actually reads its inbox.
func (d *Daemon) stopCountdownLocked(sessionID string) bool {
	c, ok := d.nudgeCountdowns[sessionID]
	if !ok {
		return false
	}
	c.timer.Stop()
	delete(d.nudgeCountdowns, sessionID)
	return true
}

// setUnreadLocked updates the cached unread flag and reports whether it changed.
// Caller holds nudgeMu.
func (d *Daemon) setUnreadLocked(sessionID string, unread bool) bool {
	if d.unreadCache == nil {
		d.unreadCache = make(map[string]bool)
	}
	if d.unreadCache[sessionID] == unread {
		return false
	}
	if unread {
		d.unreadCache[sessionID] = true
	} else {
		delete(d.unreadCache, sessionID)
	}
	return true
}

// markTicketUnread sets the session's unread indicator and broadcasts on change.
// Clearing it (unread=false) also cancels any running countdown — there is nothing
// left to nudge.
func (d *Daemon) markTicketUnread(sessionID string, unread bool) {
	d.nudgeMu.Lock()
	changed := d.setUnreadLocked(sessionID, unread)
	if !unread {
		changed = d.stopCountdownLocked(sessionID) || changed
	}
	d.nudgeMu.Unlock()
	if changed {
		d.broadcastSessionStateChanged(sessionID)
	}
}

// cancelNudgeCountdown stops a running countdown (e.g. the session left idle) without
// touching unread, so the indicator survives as the static working+unread marker.
func (d *Daemon) cancelNudgeCountdown(sessionID, reason string) {
	d.nudgeMu.Lock()
	changed := d.stopCountdownLocked(sessionID)
	d.nudgeMu.Unlock()
	if changed {
		if d.debugLogging {
			d.logf("nudge countdown canceled: session=%s reason=%s", sessionID, reason)
		}
		d.broadcastSessionStateChanged(sessionID)
	}
}

// clearNudgeState drops all per-session nudge bookkeeping when a session is removed.
// No broadcast: a sessions-updated for the removal follows on its own.
func (d *Daemon) clearNudgeState(sessionID string) {
	d.nudgeMu.Lock()
	d.stopCountdownLocked(sessionID)
	delete(d.unreadCache, sessionID)
	d.nudgeMu.Unlock()
	d.lastInputMu.Lock()
	delete(d.lastUserInputAt, sessionID)
	d.lastInputMu.Unlock()
}

// stopNudgeCountdowns cancels every armed countdown. Daemon.Stop() calls it so no
// AfterFunc goroutine outlives teardown.
func (d *Daemon) stopNudgeCountdowns() {
	d.nudgeMu.Lock()
	defer d.nudgeMu.Unlock()
	for id, c := range d.nudgeCountdowns {
		c.timer.Stop()
		delete(d.nudgeCountdowns, id)
	}
}

// nudgeCountdownFire is the deferred delivery. The identity check against the map
// entry mirrors ticketBackstopFire: a countdown that lost a reschedule/cancel race
// finds a different (or absent) timer and bails, keeping delivery to a single
// doorbell. The entry is consumed here; deliverNudgeOrReArm decides what to do.
func (d *Daemon) nudgeCountdownFire(sessionID string, self *time.Timer) {
	d.nudgeMu.Lock()
	entry, ok := d.nudgeCountdowns[sessionID]
	current := ok && entry.timer == self
	if current {
		delete(d.nudgeCountdowns, sessionID)
	}
	d.nudgeMu.Unlock()
	if !current {
		return
	}
	d.deliverNudgeOrReArm(sessionID)
}

// deliverNudgeOrReArm runs the fire-time re-check and acts on it, then notifies the
// test hook and rebroadcasts (the countdown's nudge_fires_at is now gone, or a fresh
// one was armed on re-arm).
func (d *Daemon) deliverNudgeOrReArm(sessionID string) {
	action := d.runNudgeDelivery(sessionID)
	if d.nudgeFireHook != nil {
		d.nudgeFireHook(sessionID, action)
	}
	d.broadcastSessionStateChanged(sessionID)
}

// runNudgeDelivery is the fire-time decision, separated so its outcome is a single
// string the test hook can assert. It doorbells only when the session is still idle,
// not the active session, still has unread ticket activity, and — the splice guard —
// has not received a genuine user keystroke within userInputGuardWindow. A keystroke
// re-arms a fresh countdown rather than dropping the nudge: the user is mid-keystroke,
// not gone.
func (d *Daemon) runNudgeDelivery(sessionID string) string {
	if d.ptyBackend == nil || d.store == nil {
		return "noop"
	}
	session := d.store.Get(sessionID)
	if session == nil || !isIdleForNudge(string(session.State)) {
		return "not-idle"
	}
	if d.currentlySelectedSession() == sessionID {
		// Became the active session during the window; switching away re-arms it.
		return "active"
	}
	unread, err := d.ticketUnreadForSession(sessionID)
	if err != nil {
		d.logf("nudge countdown unread check %s: %v", sessionID, err)
		return "error"
	}
	if unread == 0 {
		d.markTicketUnread(sessionID, false)
		return "drained"
	}
	if d.recentUserInput(sessionID, userInputGuardWindow) {
		d.nudgeMu.Lock()
		d.startCountdownLocked(sessionID, d.nudgeWindow())
		d.nudgeMu.Unlock()
		return "rearm"
	}
	if err := d.typeDoorbell(sessionID, ticketNudgePrompt); err != nil {
		d.logf("nudge countdown doorbell %s: %v", sessionID, err)
		return "doorbell-error"
	}
	return "doorbell"
}

// updateNudgeSelection pauses the newly selected session's countdown and resumes the
// previously selected one. Selection drives only this UX; the splice guard above is
// what protects a second visible tile the user types into. The store read for the
// idle check is done before taking nudgeMu to keep the lock ordering one-way.
func (d *Daemon) updateNudgeSelection(oldID, newID string) {
	resumeOld := false
	if oldID != "" && oldID != newID && d.store != nil {
		if s := d.store.Get(oldID); s != nil && isIdleForNudge(string(s.State)) {
			resumeOld = true
		}
	}

	var changed []string
	d.nudgeMu.Lock()
	if newID != "" && d.stopCountdownLocked(newID) {
		changed = append(changed, newID)
	}
	if resumeOld && d.unreadCache[oldID] {
		if _, running := d.nudgeCountdowns[oldID]; !running {
			d.startCountdownLocked(oldID, d.nudgeWindow())
			changed = append(changed, oldID)
		}
	}
	d.nudgeMu.Unlock()

	for _, id := range changed {
		d.broadcastSessionStateChanged(id)
	}
}

// refreshTicketUnread recomputes a session's unread ticket count and updates the
// indicator. Called after an inbox consume (handleTicketInbox) and on each notify so
// the indicator reflects reality for every agent — including self-monitoring Claude,
// whose own watch drains the queue with no doorbell.
func (d *Daemon) refreshTicketUnread(sessionID string) {
	if d.store == nil {
		return
	}
	unread, err := d.ticketUnreadForSession(sessionID)
	if err != nil {
		d.logf("ticket unread refresh %s: %v", sessionID, err)
		return
	}
	d.markTicketUnread(sessionID, unread > 0)
}

// isExplicitNudgeBlocked reports the one state where a user's explicit click must NOT
// doorbell: pending_approval. Typing the doorbell prompt + Enter into an approval
// prompt could answer the approval — an unsafe, hard-to-undo side effect. Every other
// state honors the click on demand (idle, waiting_input, working, unknown, launching):
// an explicit click is unambiguous intent, so unlike the automatic countdown (which
// stays idle-only) the user has chosen to deliver now regardless of the agent's state.
func isExplicitNudgeBlocked(state string) bool {
	return state == protocol.StatePendingApproval
}

// handleTriggerNudge is the user clicking the incoming-nudge indicator: deliver the
// pending doorbell now, on demand, in any state but pending_approval. It is exempt
// from the keystroke guard — an explicit click is unambiguous intent — and respects
// only unread (a click on a stale, already-drained indicator is a harmless no-op).
func (d *Daemon) handleTriggerNudge(msg *protocol.TriggerNudgeMessage) {
	sessionID := strings.TrimSpace(msg.SessionID)
	if sessionID == "" {
		return
	}
	d.cancelNudgeCountdown(sessionID, "user triggered")
	if d.ptyBackend == nil || d.store == nil {
		return
	}
	session := d.store.Get(sessionID)
	if session == nil || isExplicitNudgeBlocked(string(session.State)) {
		return
	}
	unread, err := d.ticketUnreadForSession(sessionID)
	if err != nil || unread == 0 {
		// Nothing to deliver; clear a stale indicator rather than doorbell into nothing.
		d.markTicketUnread(sessionID, false)
		return
	}
	if err := d.typeDoorbell(sessionID, ticketNudgePrompt); err != nil {
		d.logf("trigger_nudge doorbell %s: %v", sessionID, err)
	}
	d.broadcastSessionStateChanged(sessionID)
}

// noteUserInput records a genuine user keystroke for the splice guard. Automation and
// attach-replay writes are not the user typing, so they do not count.
func (d *Daemon) noteUserInput(sessionID, source string) {
	if sessionID == "" || !isUserKeystrokeSource(source) {
		return
	}
	now := time.Now()
	d.lastInputMu.Lock()
	if d.lastUserInputAt == nil {
		d.lastUserInputAt = make(map[string]time.Time)
	}
	d.lastUserInputAt[sessionID] = now
	d.lastInputMu.Unlock()
}

// recentUserInput reports whether a genuine user keystroke hit this session within
// the window.
func (d *Daemon) recentUserInput(sessionID string, within time.Duration) bool {
	d.lastInputMu.Lock()
	defer d.lastInputMu.Unlock()
	last, ok := d.lastUserInputAt[sessionID]
	if !ok {
		return false
	}
	return time.Since(last) < within
}

// isUserKeystrokeSource reports whether a pty_input source tag represents the user
// typing. Genuine keystrokes arrive untagged (empty source); automation and replay
// are explicitly tagged and excluded. The lone "user" tag (insert-reference) is the
// user composing, so it counts.
func isUserKeystrokeSource(source string) bool {
	switch source {
	case "automation", "attach_replay":
		return false
	default:
		return true
	}
}

// decorateSessionWithNudge stamps the broadcast clone with the live nudge state so
// every session broadcast (state change, sessions-updated, reconnect rehydration)
// carries the current indicator. Read under nudgeMu; callers must not already hold it.
func (d *Daemon) decorateSessionWithNudge(clone *protocol.Session) {
	if clone == nil {
		return
	}
	d.nudgeMu.Lock()
	unread := d.unreadCache[clone.ID]
	var firesAt string
	if c, ok := d.nudgeCountdowns[clone.ID]; ok {
		firesAt = c.firesAt.UTC().Format(time.RFC3339Nano)
	}
	d.nudgeMu.Unlock()

	if unread {
		clone.TicketUnread = protocol.Ptr(true)
	} else {
		clone.TicketUnread = nil
	}
	if firesAt != "" {
		clone.NudgeFiresAt = protocol.Ptr(firesAt)
	} else {
		clone.NudgeFiresAt = nil
	}
}
