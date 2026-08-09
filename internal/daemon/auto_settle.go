package daemon

import (
	"strconv"
	"strings"
	"time"

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
		d.clearAutoSettleSuppression(sessionID)
		d.cancelAutoSettle(sessionID, "left working")
		return
	}
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
	if d.autoSettleSuppressedFor(sessionID) {
		// The user cancelled and the session has not left `working` since.
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

// clearAutoSettleState drops a removed session's timer without broadcasting; the
// removal's own sessions-updated follows.
func (d *Daemon) clearAutoSettleState(sessionID string) {
	d.autoSettleMu.Lock()
	d.stopAutoSettleLocked(sessionID)
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

// cancelAutoSettleByUser stops the countdown and does not re-arm (see
// CancelCountdownMessage), reporting whether anything was pending.
func (d *Daemon) cancelAutoSettleByUser(sessionID string) bool {
	d.autoSettleMu.Lock()
	removed, _ := d.stopAutoSettleLocked(sessionID)
	if removed {
		// Suppression makes the cancel stick: without it the next `working`
		// re-report re-arms. Cleared when the session leaves `working`.
		if d.autoSettleSuppressed == nil {
			d.autoSettleSuppressed = make(map[string]bool)
		}
		d.autoSettleSuppressed[sessionID] = true
	}
	d.autoSettleMu.Unlock()
	if d.debugLogging {
		d.logf("auto-settle canceled by user: session=%s had_pending=%v", sessionID, removed)
	}
	// No broadcast here: handleCancelCountdown makes exactly one after every
	// countdown on the session is called off.
	return removed
}

// decorateSessionWithAutoSettle stamps the broadcast clone with the countdown
// deadline or the held flag, never both. Callers must not hold autoSettleMu.
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
	d.autoSettleMu.Unlock()

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

// turnOwed reads the decorated clone rather than re-deriving, so the timer and
// the queue can never disagree about who is owed.
func (d *Daemon) turnOwed(sessionID string) bool {
	if d.store == nil {
		return false
	}
	session := d.store.Get(sessionID)
	if session == nil {
		return false
	}
	clone := cloneSession(session)
	if clone == nil {
		return false
	}
	d.decorateSessionWithWorkspace(clone)
	d.decorateSessionWithTurn(clone)
	return protocol.Deref(clone.TurnOwed)
}

// autoSettleSuppressedFor reports whether a user cancel is still standing for
// this session.
func (d *Daemon) autoSettleSuppressedFor(sessionID string) bool {
	d.autoSettleMu.Lock()
	defer d.autoSettleMu.Unlock()
	return d.autoSettleSuppressed[sessionID]
}

// clearAutoSettleSuppression lifts a standing cancel when the session leaves
// `working` — the edge that makes the next steer a new decision.
func (d *Daemon) clearAutoSettleSuppression(sessionID string) {
	d.autoSettleMu.Lock()
	delete(d.autoSettleSuppressed, sessionID)
	d.autoSettleMu.Unlock()
}

// cancelAllAutoSettle drops every pending settle. Used when the feature is
// switched off: a countdown already on screen must stop, not run out.
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
	d.autoSettleSuppressed = nil
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
