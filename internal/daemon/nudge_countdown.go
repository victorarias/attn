package daemon

import (
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// defaultNudgeCountdownWindow is how long an armed ticket nudge waits, visible
// as a countdown, before the daemon doorbells an eligible session.
const defaultNudgeCountdownWindow = 30 * time.Second

// userInputGuardWindow is the anti-splice guarantee: a session with a genuine
// keystroke this recent is never doorbelled, or the prompt + Enter would splice
// onto a half-typed line.
const userInputGuardWindow = 3 * time.Second

// nudgeCountdown is a per-session armed countdown. firesAt is stored beside the
// timer because time.Timer has no deadline accessor.
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

// armNudgeCountdown is the single entry every ticket doorbell routes through:
// mark unread, start a visible countdown. The selected session never auto-fires
// — its countdown is paused and switching away resumes it.
func (d *Daemon) armNudgeCountdown(sessionID string) {
	d.armNudgeCountdownAt(sessionID, time.Now().Add(d.nudgeWindow()))
}

// armNudgeCountdownAt fires no later than deadline: a burst never pushes an
// armed countdown later, an assignee update can pull it forward.
func (d *Daemon) armNudgeCountdownAt(sessionID string, deadline time.Time) {
	if sessionID == "" {
		return
	}
	if deadline.Before(time.Now()) {
		deadline = time.Now()
	}
	active := d.currentlySelectedSession() == sessionID

	// Checked before the lock: nudgeMu must never be held across a store read.
	if d.nudgeSuppressedFor(sessionID) {
		if d.nudgeSuppressionStillStands(sessionID) {
			// "Not now" still stands: keep the unread marker, arm nothing.
			d.nudgeMu.Lock()
			changed := d.setUnreadLocked(sessionID, true)
			changed = d.stopCountdownLocked(sessionID) || changed
			d.nudgeMu.Unlock()
			if changed {
				d.broadcastSessionStateChanged(sessionID)
			}
			return
		}
		d.clearNudgeSuppression(sessionID)
	}

	d.nudgeMu.Lock()
	changed := d.setUnreadLocked(sessionID, true)
	if active {
		// Paused while active: no running timer, no deadline on the wire.
		changed = d.stopCountdownLocked(sessionID) || changed
	} else if existing, running := d.nudgeCountdowns[sessionID]; !running || deadline.Before(existing.firesAt) {
		// Keep the earliest deadline: the debounce never slides.
		d.startCountdownAtLocked(sessionID, deadline)
		changed = true
	}
	d.nudgeMu.Unlock()

	if changed {
		d.broadcastSessionStateChanged(sessionID)
	}
}

// startCountdownLocked creates the AfterFunc; caller holds nudgeMu. The ready
// channel blocks the closure until `timer` is published, so the identity check
// in nudgeCountdownFire never reads a half-written value.
func (d *Daemon) startCountdownLocked(sessionID string, window time.Duration) {
	d.startCountdownAtLocked(sessionID, time.Now().Add(window))
}

func (d *Daemon) startCountdownAtLocked(sessionID string, firesAt time.Time) {
	if d.nudgeCountdowns == nil {
		d.nudgeCountdowns = make(map[string]*nudgeCountdown)
	}
	if existing, ok := d.nudgeCountdowns[sessionID]; ok {
		existing.timer.Stop()
	}
	ready := make(chan struct{})
	var timer *time.Timer
	delay := time.Until(firesAt)
	if delay < 0 {
		delay = 0
	}
	timer = time.AfterFunc(delay, func() {
		<-ready
		d.nudgeCountdownFire(sessionID, timer)
	})
	d.nudgeCountdowns[sessionID] = &nudgeCountdown{timer: timer, firesAt: firesAt}
	close(ready)
}

// stopCountdownLocked cancels a session's running countdown (caller holds
// nudgeMu). It does NOT touch unread — that survives until the inbox is read.
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

// markTicketUnread sets the session's unread indicator and broadcasts on change;
// clearing also cancels any running countdown.
func (d *Daemon) markTicketUnread(sessionID string, unread bool) {
	d.nudgeMu.Lock()
	changed := d.setUnreadLocked(sessionID, unread)
	if !unread {
		changed = d.stopCountdownLocked(sessionID) || changed
		// The queue the cancel answered is drained; anything new gets to ask.
		delete(d.nudgeSuppressedThrough, sessionID)
	}
	d.nudgeMu.Unlock()
	if changed {
		d.broadcastSessionStateChanged(sessionID)
	}
}

// cancelNudgeCountdown stops a running countdown without touching unread.
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

// cancelNudgeCountdownByUser also records a standing cancel, stamped with the
// newest unread seq so it expires on its own: a later event gets to ask again.
func (d *Daemon) cancelNudgeCountdownByUser(sessionID string) bool {
	// Read before taking nudgeMu: a store read must never happen under it.
	newest, err := d.newestUnreadTicketSeq(sessionID)
	if err != nil {
		d.logf("nudge cancel unread scan %s: %v", sessionID, err)
		// Fail closed rather than re-arm what the user just cancelled.
		newest = nudgeSuppressAllSeq
	}

	d.nudgeMu.Lock()
	stopped := d.stopCountdownLocked(sessionID)
	if d.nudgeSuppressedThrough == nil {
		d.nudgeSuppressedThrough = make(map[string]int64)
	}
	// Only ever widen: two cancels in a row must not narrow the standing answer.
	if existing, ok := d.nudgeSuppressedThrough[sessionID]; !ok || newest > existing {
		d.nudgeSuppressedThrough[sessionID] = newest
	}
	d.nudgeMu.Unlock()

	if d.debugLogging {
		d.logf("nudge countdown canceled by user: session=%s had_countdown=%v through_seq=%d", sessionID, stopped, newest)
	}
	return stopped
}

// nudgeSuppressAllSeq stands in for "every event that could be pending" when
// the unread scan fails.
const nudgeSuppressAllSeq = int64(1<<63 - 1)

// nudgeSuppressedFor reports whether a user cancel is still on record.
func (d *Daemon) nudgeSuppressedFor(sessionID string) bool {
	d.nudgeMu.Lock()
	defer d.nudgeMu.Unlock()
	_, ok := d.nudgeSuppressedThrough[sessionID]
	return ok
}

// nudgeSuppressionStillStands reports whether everything currently unread was
// already pending at cancel time; a scan error keeps the cancel (fail closed).
func (d *Daemon) nudgeSuppressionStillStands(sessionID string) bool {
	newest, err := d.newestUnreadTicketSeq(sessionID)
	if err != nil {
		d.logf("nudge suppression scan %s: %v", sessionID, err)
		return true
	}
	d.nudgeMu.Lock()
	defer d.nudgeMu.Unlock()
	through, ok := d.nudgeSuppressedThrough[sessionID]
	return ok && newest <= through
}

// clearNudgeSuppression lifts a standing cancel, so the next arm is honored.
func (d *Daemon) clearNudgeSuppression(sessionID string) {
	d.nudgeMu.Lock()
	delete(d.nudgeSuppressedThrough, sessionID)
	d.nudgeMu.Unlock()
}

// newestUnreadTicketSeq is the highest unread ticket-event seq for this session's
// observers. Zero when nothing is unread, so a cancel against it expires at once.
func (d *Daemon) newestUnreadTicketSeq(sessionID string) (int64, error) {
	if d.store == nil {
		return 0, nil
	}
	var newest int64
	for _, observer := range d.ticketObserversForSession(sessionID) {
		events, err := d.store.UnreadTicketEventsFor(observer.ID, observer.AuthorID)
		if err != nil {
			return 0, err
		}
		for _, event := range events {
			if event.Seq > newest {
				newest = event.Seq
			}
		}
	}
	return newest, nil
}

// clearNudgeState drops all per-session nudge bookkeeping on session removal;
// no broadcast, the removal's own follows.
func (d *Daemon) clearNudgeState(sessionID string) {
	d.nudgeMu.Lock()
	d.stopCountdownLocked(sessionID)
	delete(d.unreadCache, sessionID)
	delete(d.nudgeSuppressedThrough, sessionID)
	d.nudgeMu.Unlock()
	d.lastInputMu.Lock()
	delete(d.lastUserInputAt, sessionID)
	delete(d.lastAutoSettleActivityAt, sessionID)
	d.lastInputMu.Unlock()
	d.deliveryMu.Lock()
	delete(d.watchLeaseUntil, sessionID)
	d.deliveryMu.Unlock()
}

// stopNudgeCountdowns cancels every armed countdown at Daemon.Stop() so no
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
// entry keeps a countdown that lost a reschedule/cancel race from firing twice.
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

// deliverNudgeOrReArm runs the fire-time re-check, notifies the test hook, and
// rebroadcasts.
func (d *Daemon) deliverNudgeOrReArm(sessionID string) {
	action := d.runNudgeDelivery(sessionID)
	if d.debugLogging {
		d.logf("ticket delivery: observer=%s session=%s channel=countdown outcome=%s", d.ticketAttentionKey(sessionID), sessionID, action)
	}
	if d.nudgeFireHook != nil {
		d.nudgeFireHook(sessionID, action)
	}
	d.broadcastSessionStateChanged(sessionID)
}

// runNudgeDelivery doorbells only when not pending approval, not the active
// session, still unread, and past the splice guard — a recent keystroke re-arms
// rather than drops.
func (d *Daemon) runNudgeDelivery(sessionID string) string {
	if d.ptyBackend == nil || d.store == nil {
		return "noop"
	}
	if d.initialPromptPending(sessionID) {
		return "priming"
	}
	session := d.store.Get(sessionID)
	if session == nil || !isNudgeDeliveryAllowed(string(session.State)) {
		return "blocked"
	}
	if d.currentlySelectedSession() == sessionID {
		return "active"
	}
	d.deliveryMu.Lock()
	defer d.deliveryMu.Unlock()
	if until := d.watchLeaseUntil[sessionID]; until.After(time.Now()) {
		d.nudgeMu.Lock()
		d.startCountdownAtLocked(sessionID, until)
		d.nudgeMu.Unlock()
		return "watch"
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
	deliveredThroughSeq, err := d.newestUnreadTicketSeq(sessionID)
	if err != nil {
		d.logf("nudge delivered-through scan %s: %v", sessionID, err)
	}
	if err := d.store.SetTicketDeliveryAttentionThrough(d.ticketAttentionKey(sessionID), time.Now(), deliveredThroughSeq); err != nil {
		d.logf("nudge attention update %s: %v", sessionID, err)
	}
	return "doorbell"
}

// updateNudgeSelection pauses the newly selected session's countdown and resumes
// the previous one. The approval store read precedes nudgeMu: lock order is one-way.
func (d *Daemon) updateNudgeSelection(oldID, newID string) {
	resumeOld := false
	if oldID != "" && oldID != newID && d.store != nil {
		if s := d.store.Get(oldID); s != nil && isNudgeDeliveryAllowed(string(s.State)) {
			resumeOld = true
		}
	}

	var changed []string
	d.nudgeMu.Lock()
	if newID != "" && d.stopCountdownLocked(newID) {
		changed = append(changed, newID)
	}
	resumeUnread := resumeOld && d.unreadCache[oldID]
	d.nudgeMu.Unlock()

	for _, id := range changed {
		d.broadcastSessionStateChanged(id)
	}
	if resumeUnread {
		// Re-derive the deadline from durable unread events so switching away
		// cannot collapse an active bundle window to the short countdown.
		go d.notifyUnreadTicketSession(oldID, time.Now())
	}
}

// refreshTicketUnread recomputes a session's unread ticket count and updates the
// indicator; an agent's own watch can drain the queue before the doorbell.
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

// isNudgeDeliveryAllowed is the sole session-state gate for every doorbell:
// pending approval is the only unsafe target, because a trailing Enter could
// answer it.
func isNudgeDeliveryAllowed(state string) bool {
	return state != protocol.StatePendingApproval
}

// handleTriggerNudge is the user clicking the indicator: deliver now, exempt from
// the keystroke guard, respecting only unread.
func (d *Daemon) handleTriggerNudge(msg *protocol.TriggerNudgeMessage) {
	sessionID := strings.TrimSpace(msg.SessionID)
	if sessionID == "" {
		return
	}
	if d.initialPromptPending(sessionID) {
		return
	}
	d.cancelNudgeCountdown(sessionID, "user triggered")
	if d.ptyBackend == nil || d.store == nil {
		return
	}
	session := d.store.Get(sessionID)
	if session == nil || !isNudgeDeliveryAllowed(string(session.State)) {
		return
	}
	d.deliveryMu.Lock()
	defer d.deliveryMu.Unlock()
	unread, err := d.ticketUnreadForSession(sessionID)
	if err != nil || unread == 0 {
		// Clear a stale indicator rather than doorbell into nothing.
		d.markTicketUnread(sessionID, false)
		return
	}
	if err := d.typeDoorbell(sessionID, ticketNudgePrompt); err != nil {
		d.logf("trigger_nudge doorbell %s: %v", sessionID, err)
	} else {
		deliveredThroughSeq, scanErr := d.newestUnreadTicketSeq(sessionID)
		if scanErr != nil {
			d.logf("trigger_nudge delivered-through scan %s: %v", sessionID, scanErr)
		}
		if err := d.store.SetTicketDeliveryAttentionThrough(d.ticketAttentionKey(sessionID), time.Now(), deliveredThroughSeq); err != nil {
			d.logf("trigger_nudge attention update %s: %v", sessionID, err)
		}
	}
	d.broadcastSessionStateChanged(sessionID)
}

// noteUserInput records a genuine user keystroke — the only place the source
// filter is applied — and stamps auto-settle activity. Pointer movement records
// only the second stamp, since it cannot splice a doorbell.
func (d *Daemon) noteUserInput(sessionID, source string) bool {
	if sessionID == "" || !isUserKeystrokeSource(source) {
		return false
	}
	now := time.Now()
	d.lastInputMu.Lock()
	if d.lastUserInputAt == nil {
		d.lastUserInputAt = make(map[string]time.Time)
	}
	if d.lastAutoSettleActivityAt == nil {
		d.lastAutoSettleActivityAt = make(map[string]time.Time)
	}
	d.lastUserInputAt[sessionID] = now
	d.lastAutoSettleActivityAt[sessionID] = now
	d.lastInputMu.Unlock()
	return true
}

// noteAutoSettleActivity freezes a pending settle without claiming a PTY
// keystroke. Shares lastInputMu with settleIfAutoSettleQuiet so activity wins.
func (d *Daemon) noteAutoSettleActivity(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	d.lastInputMu.Lock()
	if d.lastAutoSettleActivityAt == nil {
		d.lastAutoSettleActivityAt = make(map[string]time.Time)
	}
	d.lastAutoSettleActivityAt[sessionID] = time.Now()
	d.lastInputMu.Unlock()
	return true
}

// recentUserInput reports whether a genuine user keystroke hit this session within
// the window.
func (d *Daemon) recentUserInput(sessionID string, within time.Duration) bool {
	return d.userInputQuietRemaining(sessionID, within) > 0
}

// userInputQuietRemaining reports how much of `within` is left before this
// session counts as quiet, so a waiting timer reschedules instead of polling.
func (d *Daemon) userInputQuietRemaining(sessionID string, within time.Duration) time.Duration {
	d.lastInputMu.Lock()
	defer d.lastInputMu.Unlock()
	return d.userInputQuietRemainingLocked(sessionID, within)
}

// userInputQuietRemainingLocked is the same reading, for a caller that already
// holds lastInputMu.
func (d *Daemon) userInputQuietRemainingLocked(sessionID string, within time.Duration) time.Duration {
	last, ok := d.lastUserInputAt[sessionID]
	if !ok {
		return 0
	}
	remaining := within - time.Since(last)
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (d *Daemon) autoSettleActivityQuietRemaining(sessionID string, within time.Duration) time.Duration {
	d.lastInputMu.Lock()
	defer d.lastInputMu.Unlock()
	return d.autoSettleActivityQuietRemainingLocked(sessionID, within)
}

func (d *Daemon) autoSettleActivityQuietRemainingLocked(sessionID string, within time.Duration) time.Duration {
	last, ok := d.lastAutoSettleActivityAt[sessionID]
	if !ok {
		return 0
	}
	remaining := within - time.Since(last)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// settleIfAutoSettleQuiet is the only place a timer closes a turn. The quiet
// check and the store write must share one critical section under the lock
// activity stamps use, or a real interaction lands in the gap and the turn closes
// with the user's hands on the session. Returns remaining quiet time on refusal.
func (d *Daemon) settleIfAutoSettleQuiet(sessionID string, within time.Duration) (quiet time.Duration, settled bool) {
	d.lastInputMu.Lock()
	defer d.lastInputMu.Unlock()
	if remaining := d.autoSettleActivityQuietRemainingLocked(sessionID, within); remaining > 0 {
		return remaining, false
	}
	return 0, d.store.SettleTurn(sessionID, time.Now())
}

// isUserKeystrokeSource: genuine keystrokes arrive untagged, automation and
// replay are tagged and excluded, and "user" (insert-reference) counts.
func isUserKeystrokeSource(source string) bool {
	switch source {
	case "automation", "attach_replay":
		return false
	default:
		return true
	}
}

// decorateSessionWithNudge stamps the broadcast clone with the live nudge state.
// Takes nudgeMu; callers must not already hold it.
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
