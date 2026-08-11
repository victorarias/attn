package daemon

import (
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionstate"
)

// Auto-settle closes a turn the user has already dealt with by steering the
// agent back to work: an invisible arm delay ("did the steering take?"), then a
// visible countdown ("does the user want to stop this?"). Timers live in the
// daemon because a client-side one would race the broadcast that feeds it.
const (
	// defaultAutoSettleArmSeconds is how long a session must hold `working`
	// before the countdown starts.
	defaultAutoSettleArmSeconds = 30
	// defaultAutoSettleCountdownSeconds is how long the visible countdown runs
	// before the turn is settled.
	defaultAutoSettleCountdownSeconds = 15

	// autoSettleHoldQuietWindow is the quiet time before a held countdown
	// resumes. Deliberately not a setting: it only has to span gaps in active
	// typing, and it resumes into a visible, cancellable countdown.
	autoSettleHoldQuietWindow = 5 * time.Second

	// Floors keep the arm delay past the resolver's own settle latency
	// (HeartbeatSettleAfter, 5s), so no turn closes on an agent it is still
	// deciding about; ceilings are fat-finger guards.
	autoSettleArmMinSeconds       = 5
	autoSettleArmMaxSeconds       = 3600
	autoSettleCountdownMinSeconds = 3
	autoSettleCountdownMaxSeconds = 600
)

// autoSettlePhase is which of the two windows a session is in.
type autoSettlePhase int

const (
	// autoSettleArming: holding `working`, nothing visible, no deadline on the wire.
	autoSettleArming autoSettlePhase = iota
	// autoSettleCounting: the countdown is running and its deadline is broadcast.
	autoSettleCounting
	// autoSettleHeld: the user is interacting; the pending timer is a quiet check
	// that re-holds or resumes. No deadline rides the wire, `auto_settle_held` does.
	autoSettleHeld
)

// autoSettleTimer is a session's pending auto-settle. firesAt exists because
// time.Timer exposes no deadline accessor; resume is the phase a hold came from
// and returns to, so activity during the arm delay cannot promote to counting.
type autoSettleTimer struct {
	timer   *time.Timer
	phase   autoSettlePhase
	resume  autoSettlePhase
	firesAt time.Time
}

// visible reports whether clients can see this entry — only a countdown,
// running or frozen — and so whether it owes a broadcast.
func (e *autoSettleTimer) visible() bool {
	switch e.phase {
	case autoSettleCounting:
		return true
	case autoSettleHeld:
		return e.resume == autoSettleCounting
	}
	return false
}

// autoSettleConfig is the resolved policy. Read from settings on every arm so a
// change takes effect on the next transition without invalidation bookkeeping.
type autoSettleConfig struct {
	enabled   bool
	arm       time.Duration
	countdown time.Duration
}

func (d *Daemon) autoSettleConfig() autoSettleConfig {
	if d.store == nil {
		return autoSettleConfig{}
	}
	return autoSettleConfig{
		enabled:   parseBooleanSetting(d.store.GetSetting(SettingAutoSettleEnabled)),
		arm:       resolveAutoSettleSeconds(d.store.GetSetting(SettingAutoSettleArmSeconds), defaultAutoSettleArmSeconds),
		countdown: resolveAutoSettleSeconds(d.store.GetSetting(SettingAutoSettleCountdownSeconds), defaultAutoSettleCountdownSeconds),
	}
}

// resolveAutoSettleSeconds turns a stored setting into a duration. Validation
// rejects out-of-range values at write time; this only has to be total.
func resolveAutoSettleSeconds(stored string, fallbackSeconds int) time.Duration {
	if n, err := strconv.Atoi(strings.TrimSpace(stored)); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return time.Duration(fallbackSeconds) * time.Second
}

// syncAutoSettle runs from applyState on every committed transition, so the
// rule is exhaustive: only `working` sustains a pending settle, anything else
// cancels — which keeps a future state safe by default. No debounce:
// internal/sessionstate already absorbs the flickers it knows about.
func (d *Daemon) syncAutoSettle(sessionID, state string) {
	if state != protocol.StateWorking {
		d.retireAutoSettleDismissal(sessionID)
		d.cancelAutoSettle(sessionID, "left working")
		return
	}
	d.coverAutoSettleDismissal(sessionID)
	d.armAutoSettle(sessionID)
}

// armAutoSettle starts the arm delay if the feature applies and nothing is
// pending. Leaving an existing timer alone makes a re-reported `working`
// harmless — restarting the delay each time would mean it never elapses.
func (d *Daemon) armAutoSettle(sessionID string) {
	if sessionID == "" || d.store == nil {
		return
	}
	cfg := d.autoSettleConfig()
	if !cfg.enabled {
		return
	}
	if d.autoSettleDismissalArmed(sessionID) {
		// A standing dismissal: the user answered this settle before it ran.
		return
	}
	// turnOwed carries the shell/chief/pinned/muted exclusions too, so those are
	// out for the same reason they are out of the queue.
	if !d.turnOwed(sessionID) {
		return
	}

	d.autoSettleMu.Lock()
	_, pending := d.autoSettleTimers[sessionID]
	if !pending {
		d.startAutoSettleLocked(sessionID, autoSettleArming, cfg.arm)
	}
	d.autoSettleMu.Unlock()
	// No broadcast: the arming phase is deliberately invisible.
}

// holdAutoSettle freezes a pending settle on user interaction. Any keystroke
// counts — noteUserInput already drops attn's own automation and replay writes.
// A hold is not a cancel: it expires on its own after the quiet window.
func (d *Daemon) holdAutoSettle(sessionID string) {
	d.autoSettleMu.Lock()
	entry, ok := d.autoSettleTimers[sessionID]
	if !ok || entry.phase == autoSettleHeld {
		// Already-held keeps a keystroke burst free: the quiet check reschedules
		// from the recorded keystroke time, so only the first key does work here.
		d.autoSettleMu.Unlock()
		return
	}
	resume := entry.phase
	d.startAutoSettleHeldLocked(sessionID, resume, autoSettleHoldQuietWindow)
	d.autoSettleMu.Unlock()

	if d.debugLogging {
		d.logf("auto-settle held: session=%s resume_phase=%d", sessionID, resume)
	}
	// Freezing an arm delay changes nothing a client can see.
	if resume == autoSettleCounting {
		d.broadcastSessionStateChanged(sessionID)
	}
}

// startAutoSettleHeldLocked parks the session in the held phase with a quiet
// check `window` away. Caller holds autoSettleMu.
func (d *Daemon) startAutoSettleHeldLocked(sessionID string, resume autoSettlePhase, window time.Duration) {
	d.startAutoSettleLocked(sessionID, autoSettleHeld, window)
	d.autoSettleTimers[sessionID].resume = resume
}

// startAutoSettleLocked replaces whatever is pending with a fresh timer in the
// given phase. Caller holds autoSettleMu. The ready channel (same handshake as
// nudge_countdown.go) blocks the closure until `timer` is published, so the fire
// path's identity check reads a fully written value on a zero-length window.
func (d *Daemon) startAutoSettleLocked(sessionID string, phase autoSettlePhase, window time.Duration) {
	if d.autoSettleTimers == nil {
		d.autoSettleTimers = make(map[string]*autoSettleTimer)
	}
	if existing, ok := d.autoSettleTimers[sessionID]; ok {
		existing.timer.Stop()
	}
	if window < 0 {
		window = 0
	}
	firesAt := time.Now().Add(window)
	ready := make(chan struct{})
	var timer *time.Timer
	timer = time.AfterFunc(window, func() {
		<-ready
		d.autoSettleFire(sessionID, timer)
	})
	d.autoSettleTimers[sessionID] = &autoSettleTimer{timer: timer, phase: phase, firesAt: firesAt}
	close(ready)
}

// stopAutoSettleLocked cancels and forgets a session's pending settle,
// reporting whether the removed entry was visible (a rebroadcast is owed).
// Caller holds autoSettleMu.
func (d *Daemon) stopAutoSettleLocked(sessionID string) (removed, wasVisible bool) {
	entry, ok := d.autoSettleTimers[sessionID]
	if !ok {
		return false, false
	}
	entry.timer.Stop()
	delete(d.autoSettleTimers, sessionID)
	return true, entry.visible()
}

// cancelAutoSettle drops a pending settle. Broadcasts only when a visible
// countdown was cancelled, so the arming phase stays silent on the wire.
func (d *Daemon) cancelAutoSettle(sessionID, reason string) {
	d.autoSettleMu.Lock()
	removed, wasVisible := d.stopAutoSettleLocked(sessionID)
	d.autoSettleMu.Unlock()
	if removed && d.debugLogging {
		d.logf("auto-settle canceled: session=%s reason=%s", sessionID, reason)
	}
	if wasVisible {
		d.broadcastSessionStateChanged(sessionID)
	}
}

// clearAutoSettleState drops a removed session's timer and standing dismissal
// without broadcasting; the removal's own sessions-updated follows.
func (d *Daemon) clearAutoSettleState(sessionID string) {
	d.autoSettleMu.Lock()
	d.stopAutoSettleLocked(sessionID)
	delete(d.autoSettleDismissals, sessionID)
	d.autoSettleMu.Unlock()
}

// stopAutoSettleTimers cancels every pending settle so no AfterFunc goroutine
// outlives daemon teardown.
func (d *Daemon) stopAutoSettleTimers() {
	d.autoSettleMu.Lock()
	defer d.autoSettleMu.Unlock()
	for id, entry := range d.autoSettleTimers {
		entry.timer.Stop()
		delete(d.autoSettleTimers, id)
	}
}

// autoSettleFire advances or completes a pending settle. The identity check
// against the map entry keeps a timer that lost a cancel/replace race from
// acting; both phases re-check their preconditions rather than trust them.
func (d *Daemon) autoSettleFire(sessionID string, self *time.Timer) {
	d.autoSettleMu.Lock()
	entry, ok := d.autoSettleTimers[sessionID]
	if !ok || entry.timer != self {
		d.autoSettleMu.Unlock()
		return
	}
	phase, resume := entry.phase, entry.resume
	delete(d.autoSettleTimers, sessionID)
	d.autoSettleMu.Unlock()

	action := d.runAutoSettle(sessionID, phase, resume)
	if d.debugLogging {
		d.logf("auto-settle fire: session=%s phase=%d outcome=%s", sessionID, phase, action)
	}
	if d.autoSettleFireHook != nil {
		d.autoSettleFireHook(sessionID, action)
	}
	// A re-hold, or a hold of the invisible arm delay, fires every five seconds
	// while the user types — no broadcast for a picture that never moved.
	if action == "held" && phase != autoSettleCounting {
		return
	}
	// Otherwise the deadline appeared, went, or the turn closed: broadcast.
	d.broadcastSessionStateChanged(sessionID)
}

// runAutoSettle is the fire-time decision, separated so its outcome is a single
// string a test hook can assert. `resume` is read only in the held phase.
func (d *Daemon) runAutoSettle(sessionID string, phase, resume autoSettlePhase) string {
	// Held across the whole decision so no state write lands between the check
	// and the settle — otherwise the timer could settle a turn a transition had
	// just opened. applyState takes the same lock around its store write.
	d.autoSettleFireMu.Lock()
	defer d.autoSettleFireMu.Unlock()

	cfg := d.autoSettleConfig()
	if !cfg.enabled {
		// Turned off during the window: drop it.
		return "disabled"
	}
	if d.store == nil {
		return "noop"
	}
	session := d.store.Get(sessionID)
	if session == nil {
		return "gone"
	}
	// The resolver projects `working` while a stop-time verdict is computed, and
	// that must not close the turn. recordClassifierStarted usually cancels the
	// timer; this closes the race where the callback already took it.
	if evidence, ok := d.evidenceTable().snapshot(sessionID); ok &&
		sessionstate.ClassifierVerdictPending(
			evidence,
			sessionstate.PolicyFor(string(session.Agent)),
			time.Now(),
		) {
		return "classifying"
	}
	// Re-checked here as well as in syncAutoSettle: catches a state that moved
	// without a committed transition (restart mid-window, a write outside applyState).
	if string(session.State) != protocol.StateWorking {
		return "not-working"
	}
	if !d.turnOwed(sessionID) {
		// Settled by hand, or excluded mid-window. Nothing left to close.
		return "not-owed"
	}

	// A phase that only moves the timer along can take the interaction hold as a
	// plain check: nothing commits on the far side of it.
	if phase == autoSettleHeld || phase == autoSettleArming {
		hold := phase
		if phase == autoSettleHeld {
			hold = resume
		}
		if quiet := d.autoSettleActivityQuietRemaining(sessionID, autoSettleHoldQuietWindow); quiet > 0 {
			d.holdFromFire(sessionID, hold, quiet)
			return "held"
		}
		if phase == autoSettleHeld {
			// Quiet again. The window restarts full: a frozen bar is drawn full,
			// so resuming anything less would drop the bar on release.
			window := cfg.arm
			if resume == autoSettleCounting {
				window = cfg.countdown
			}
			d.autoSettleMu.Lock()
			d.startAutoSettleLocked(sessionID, resume, window)
			d.autoSettleMu.Unlock()
			return "resumed"
		}
		d.autoSettleMu.Lock()
		d.startAutoSettleLocked(sessionID, autoSettleCounting, cfg.countdown)
		d.autoSettleMu.Unlock()
		return "counting"
	}

	// Test seam for the instant before the settle commits: the dangerous
	// interleaving (a keystroke arriving with the timer already out of the map)
	// is too narrow to hit by chance. Nil in production.
	if d.autoSettlePreSettleHook != nil {
		d.autoSettlePreSettleHook()
	}

	// The activity hold is re-asked because it must be indivisible from the write
	// it guards (settleIfAutoSettleQuiet holds the activity lock across both):
	// the timer can fire in the microseconds around an activity report.
	quiet, settled := d.settleIfAutoSettleQuiet(sessionID, autoSettleHoldQuietWindow)
	if quiet > 0 {
		d.holdFromFire(sessionID, autoSettleCounting, quiet)
		return "held"
	}
	if !settled {
		return "settle-failed"
	}
	d.traceSettle(sessionID)
	return "settled"
}

// holdFromFire parks a session the fire path found the user interacting with,
// with a quiet check exactly at the end of the window their last activity opened.
func (d *Daemon) holdFromFire(sessionID string, resume autoSettlePhase, quiet time.Duration) {
	d.autoSettleMu.Lock()
	d.startAutoSettleHeldLocked(sessionID, resume, quiet)
	d.autoSettleMu.Unlock()
}

// answerAutoSettleByUser is the auto-settle half of a cancel-countdown press,
// and the whole of it when nothing is counting down.
//
// A pending settle — a visible countdown or the invisible arm delay behind it —
// is stopped, and a standing dismissal takes its place: without one the next
// `working` re-report would just re-arm the timer the user cancelled. Pressed
// with nothing pending, the same key arms that dismissal ahead of time, which is
// the moment the user actually knows they want the turn kept: while they are
// still typing the steer that will start the working stretch. Pressed again, it
// disarms — a standing dismissal is the one countdown answer that outlives the
// thing it answered, so it needs a way back out.
//
// Reports whether anything moved. No broadcast: handleCancelCountdown makes
// exactly one after both halves have answered.
func (d *Daemon) answerAutoSettleByUser(sessionID string) bool {
	session := d.decoratedSession(sessionID)
	if !d.autoSettleAppliesTo(session) {
		// Nothing to dismiss, so nothing to arm — a chip promising to stop a
		// settle that was never coming is worse than no chip.
		return false
	}
	working := string(session.State) == protocol.StateWorking

	d.autoSettleMu.Lock()
	removed, _ := d.stopAutoSettleLocked(sessionID)
	_, armed := d.autoSettleDismissals[sessionID]
	// A dismissal and a pending timer never coexist — arming is what stops the
	// timer, and arming is refused while one stands. Preferring the cancel if
	// they ever did keeps the press meaning "stop what is happening".
	disarm := armed && !removed
	if disarm {
		delete(d.autoSettleDismissals, sessionID)
	} else {
		if d.autoSettleDismissals == nil {
			d.autoSettleDismissals = make(map[string]bool)
		}
		// Covered from the start when the stretch it answers is already running;
		// otherwise it waits for one, so the state re-reports between the press and
		// the steer cannot retire it early.
		d.autoSettleDismissals[sessionID] = working
	}
	d.autoSettleMu.Unlock()

	if disarm && working {
		// Back to the ordinary rule for a session already in the stretch: without
		// this the disarm would leave it in a limbo no state change reaches until
		// it next enters `working`.
		d.armAutoSettle(sessionID)
	}
	if d.debugLogging {
		d.logf("auto-settle answered by user: session=%s had_pending=%v armed=%v", sessionID, removed, !disarm)
	}
	return true
}

// autoSettleDismissalArmed reports whether a standing dismissal covers this
// session's next auto-settle.
func (d *Daemon) autoSettleDismissalArmed(sessionID string) bool {
	d.autoSettleMu.Lock()
	defer d.autoSettleMu.Unlock()
	_, armed := d.autoSettleDismissals[sessionID]
	return armed
}

// coverAutoSettleDismissal marks a standing dismissal as covering the `working`
// stretch that just began — the stretch it will be spent on.
func (d *Daemon) coverAutoSettleDismissal(sessionID string) {
	d.autoSettleMu.Lock()
	if _, armed := d.autoSettleDismissals[sessionID]; armed {
		d.autoSettleDismissals[sessionID] = true
	}
	d.autoSettleMu.Unlock()
}

// retireAutoSettleDismissal spends a standing dismissal at the end of the
// `working` stretch it covered, and broadcasts because the chip announcing it
// has to go with it. A dismissal still waiting for its stretch survives: the
// resolver re-reports the state the user armed in, and retiring on that would
// silently drop the answer between the press and the steer.
func (d *Daemon) retireAutoSettleDismissal(sessionID string) {
	d.autoSettleMu.Lock()
	covered, armed := d.autoSettleDismissals[sessionID]
	retire := armed && covered
	if retire {
		delete(d.autoSettleDismissals, sessionID)
	}
	d.autoSettleMu.Unlock()
	if retire {
		d.broadcastSessionStateChanged(sessionID)
	}
}

// decorateSessionWithAutoSettle stamps the broadcast clone with the countdown
// deadline, the held flag, or a standing dismissal — never more than one, since
// arming a dismissal is what stops a pending timer. Callers must not hold
// autoSettleMu.
func (d *Daemon) decorateSessionWithAutoSettle(clone *protocol.Session) {
	if clone == nil {
		return
	}
	d.autoSettleMu.Lock()
	entry, ok := d.autoSettleTimers[clone.ID]
	firesAt := ""
	held := false
	if ok {
		switch {
		case entry.phase == autoSettleCounting:
			firesAt = entry.firesAt.UTC().Format(time.RFC3339Nano)
		case entry.visible():
			held = true
		}
	}
	_, dismissArmed := d.autoSettleDismissals[clone.ID]
	d.autoSettleMu.Unlock()

	if dismissArmed {
		clone.AutoSettleDismissArmed = protocol.Ptr(true)
	} else {
		clone.AutoSettleDismissArmed = nil
	}

	if firesAt != "" {
		clone.AutoSettleFiresAt = protocol.Ptr(firesAt)
	} else {
		clone.AutoSettleFiresAt = nil
	}
	if held {
		clone.AutoSettleHeld = protocol.Ptr(true)
	} else {
		clone.AutoSettleHeld = nil
	}
}

// turnOwed reads the same decorated clone a broadcast carries rather than
// re-deriving, so the timer and the queue can never disagree about who is owed.
// The chief flag is a decoration: deriving from a stored record would leave the
// chief's turns auto-settling while the queue says it owes none.
func (d *Daemon) turnOwed(sessionID string) bool {
	clone := d.decoratedSession(sessionID)
	if clone == nil {
		return false
	}
	return protocol.Deref(clone.TurnOwed)
}

// autoSettleAppliesTo reports whether this session could ever auto-settle, which
// is what makes a standing dismissal mean something. The exclusions are the
// queue's own — a session outside the queue never settles automatically, so
// arming against it would promise to stop something that was never coming.
// Takes the decorated clone, not an id: the exclusions it reads are decorations.
func (d *Daemon) autoSettleAppliesTo(session *protocol.Session) bool {
	return session != nil &&
		d.autoSettleConfig().enabled &&
		!attention.Excluded(d.attentionInputFor(session))
}

// decoratedSession is one session as a broadcast would carry it. Callers must
// not hold autoSettleMu or nudgeMu: the decorations take both.
func (d *Daemon) decoratedSession(sessionID string) *protocol.Session {
	if d.store == nil {
		return nil
	}
	session := d.store.Get(sessionID)
	if session == nil {
		return nil
	}
	return d.sessionForBroadcast(session)
}

// cancelAllAutoSettle drops every pending settle and every standing dismissal.
// Used when the feature is switched off: a countdown already on screen must
// stop, not run out, and a dismissal of a settle that can no longer happen is
// a chip with nothing behind it.
func (d *Daemon) cancelAllAutoSettle() {
	d.autoSettleMu.Lock()
	visible := make([]string, 0, len(d.autoSettleTimers))
	for id, entry := range d.autoSettleTimers {
		entry.timer.Stop()
		if entry.visible() {
			visible = append(visible, id)
		}
		delete(d.autoSettleTimers, id)
	}
	for id := range d.autoSettleDismissals {
		visible = append(visible, id)
	}
	d.autoSettleDismissals = nil
	d.autoSettleMu.Unlock()
	for _, id := range visible {
		d.broadcastSessionStateChanged(id)
	}
}

// armAutoSettleForRunningSessions arms every already-qualifying session when
// the feature is turned on — the one moment there is no transition to react to.
func (d *Daemon) armAutoSettleForRunningSessions() {
	if d.store == nil {
		return
	}
	for _, session := range d.store.List("") {
		if session == nil || string(session.State) != protocol.StateWorking {
			continue
		}
		d.armAutoSettle(session.ID)
	}
}
