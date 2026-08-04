package daemon

import (
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// Auto-settle closes a turn the user has already dealt with.
//
// Settling is how a turn ends, and it is otherwise always manual. But steering an
// agent and watching it go back to work *is* dealing with the turn — the user
// simply never said so, and the queue fills up with turns they already handled.
// This says it for them, after a delay long enough to prove the agent really did
// go back to work, and with a visible countdown that makes the settle both
// announced and reversible.
//
// Two windows, not one, because they answer different questions. The arm delay
// asks "did the steering take?" — an agent that pops straight back out of
// `working` was not steered, it was interrupted, and nothing should have been
// counting down in the first place. The countdown asks "does the user want to
// stop this?" and is the only part they see. Collapsing them into one number
// would force a single duration to be both long enough to trust and short enough
// to watch.
//
// The timers live here rather than in the app because settling is daemon state
// and a state transition is what starts and stops them: a client-side timer would
// race the broadcast that feeds it, and would stop existing the moment the window
// closed. It mirrors nudge_countdown.go, which arms per-session timers on the same
// terms.
const (
	// defaultAutoSettleArmSeconds is how long a session must hold `working`
	// before the countdown starts.
	defaultAutoSettleArmSeconds = 30
	// defaultAutoSettleCountdownSeconds is how long the visible countdown runs
	// before the turn is settled.
	defaultAutoSettleCountdownSeconds = 15

	// autoSettleHoldQuietWindow is how long a session must go without a keystroke
	// before a held countdown resumes. It is the whole of the typing hold's
	// tuning, and it is not a setting: it measures the user's hands, not the
	// agent's behavior, so it is orthogonal to the two windows above.
	//
	// Five seconds rather than the nudge guard's three, and rather than something
	// generous. It only has to span the gaps *between* keystrokes while composing
	// — pausing to read the screen mid-prompt — because the countdown it resumes
	// into is itself the grace period, visible and cancellable. Stacking a long
	// quiet window on top of that pays twice for the same protection, and being
	// wrong here does not settle a turn silently: it unfreezes a bar the user
	// still has the whole countdown to stop.
	autoSettleHoldQuietWindow = 5 * time.Second

	// Bounds. The floors keep a countdown watchable and an arm delay long enough
	// to outlast the resolver's own settle latency (`HeartbeatSettleAfter`, 5s),
	// so a turn is never closed on an agent the resolver has not finished making
	// its mind up about. The ceilings are fat-finger guards.
	autoSettleArmMinSeconds       = 5
	autoSettleArmMaxSeconds       = 3600
	autoSettleCountdownMinSeconds = 3
	autoSettleCountdownMaxSeconds = 600
)

// autoSettlePhase is which of the two windows a session is in.
type autoSettlePhase int

const (
	// autoSettleArming: the session is holding `working` and nothing is visible
	// yet. No deadline rides the wire in this phase — an indicator here would
	// announce a settle that has not been decided on.
	autoSettleArming autoSettlePhase = iota
	// autoSettleCounting: the countdown is running and its deadline is broadcast.
	autoSettleCounting
	// autoSettleHeld: the user is typing into this session, so nothing is
	// counting. The pending timer is a quiet check, not a deadline — it asks
	// "have the keystrokes stopped?" and either re-holds or resumes. No deadline
	// rides the wire; `auto_settle_held` does, and the tile freezes on it.
	autoSettleHeld
)

// autoSettleTimer is a session's pending auto-settle. firesAt is stored beside
// the timer because time.Timer exposes no deadline accessor, and in the counting
// phase the absolute deadline is what rides the wire for clients to animate
// against.
//
// resume is meaningful only while held: it is the phase the hold came from and
// will return to, so that typing during the invisible arm delay restarts the arm
// delay rather than promoting the session to a countdown it never earned.
type autoSettleTimer struct {
	timer   *time.Timer
	phase   autoSettlePhase
	resume  autoSettlePhase
	firesAt time.Time
}

// visible reports whether clients can see this entry, and so whether its
// appearance or removal owes a broadcast. Arming is silent, and so is a hold of
// an arm delay: freezing something never announced announces it. Only a
// countdown — running or frozen — is on screen.
func (e *autoSettleTimer) visible() bool {
	switch e.phase {
	case autoSettleCounting:
		return true
	case autoSettleHeld:
		return e.resume == autoSettleCounting
	}
	return false
}

// autoSettleConfig is the resolved policy: whether the feature is on and the two
// windows. Read from settings on every arm so a settings change takes effect on
// the next transition without any invalidation bookkeeping.
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

// resolveAutoSettleSeconds turns a stored setting into a duration, falling back
// to the default for blank or unparseable values. Validation rejects out-of-range
// values at write time; this only has to be total.
func resolveAutoSettleSeconds(stored string, fallbackSeconds int) time.Duration {
	if n, err := strconv.Atoi(strings.TrimSpace(stored)); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return time.Duration(fallbackSeconds) * time.Second
}

// syncAutoSettle is the single reaction to a session's state. applyState calls it
// on every committed transition, which is what makes the rule below exhaustive:
// there is no path into or out of `working` that skips it.
//
// The rule is that only `working` sustains a pending settle. Every other state —
// a question, an approval, an error, a finished run, a dead worker — means the
// agent wants the user back, and settling one of those buries precisely the thing
// the user needs to see. That is the one behavior this feature cannot get wrong,
// so it is expressed as "anything but working cancels" rather than as a list of
// states to watch for; a state added to the vocabulary later is safe by default.
//
// Nothing here smooths over a brief dip out of `working`. It would be the wrong
// layer: internal/sessionstate already holds a session working across every
// flicker it knows about — repaint gaps (ReasonHeartbeatGap), compaction, a turn
// that yielded with background work, the grace window after a stale bracket — so
// by the time a transition reaches applyState, leaving `working` is a considered
// verdict and not a stutter. A second debounce here would be re-deciding a
// question the resolver has already answered, with less evidence.
func (d *Daemon) syncAutoSettle(sessionID, state string) {
	if state != protocol.StateWorking {
		d.clearAutoSettleSuppression(sessionID)
		d.cancelAutoSettle(sessionID, "left working")
		return
	}
	d.armAutoSettle(sessionID)
}

// armAutoSettle starts the arm delay, if this session is one the feature applies
// to and nothing is already pending for it.
//
// Leaving an existing timer alone is what makes a re-reported `working` harmless:
// the resolver can commit the same state repeatedly, and restarting the delay each
// time would mean it never elapses.
func (d *Daemon) armAutoSettle(sessionID string) {
	if sessionID == "" || d.store == nil {
		return
	}
	cfg := d.autoSettleConfig()
	if !cfg.enabled {
		return
	}
	if d.autoSettleSuppressedFor(sessionID) {
		// The user cancelled this session's countdown and it has not left
		// `working` since. Their answer stands.
		return
	}
	// Only a turn the user actually owes can be auto-settled. turnOwed carries
	// the shell/chief/pinned/muted exclusions too, so a shell pane or a pinned
	// workspace is out for the same reason it is out of the queue.
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

// holdAutoSettle freezes a pending settle because the user just typed into this
// session. Called for every genuine keystroke, so its cost on a no-op is one map
// lookup.
//
// Typing is the one thing attn can see through a TUI it has no visibility into.
// It cannot tell composing from erasing from pressing Escape, and it does not
// need to: every one of them means the user's hands are on this session, which
// is the whole question a pending settle is asking. What it must tell apart is
// the user from attn typing on their behalf, and that distinction is already
// drawn — noteUserInput drops automation and replay writes before this is
// reached.
//
// A hold is not a cancel. ⌘. means "keep this turn" and stands until the session
// leaves `working` (autoSettleSuppressed); a hold expires on its own, five quiet
// seconds later, because the user stopping typing is not the user answering.
func (d *Daemon) holdAutoSettle(sessionID string) {
	d.autoSettleMu.Lock()
	entry, ok := d.autoSettleTimers[sessionID]
	if !ok || entry.phase == autoSettleHeld {
		// Nothing pending, or already held. The second case is what keeps a burst
		// of keystrokes free: the quiet check reschedules itself from the recorded
		// keystroke time, so only the first key of a burst does any work here.
		d.autoSettleMu.Unlock()
		return
	}
	resume := entry.phase
	d.startAutoSettleHeldLocked(sessionID, resume, autoSettleHoldQuietWindow)
	d.autoSettleMu.Unlock()

	if d.debugLogging {
		d.logf("auto-settle held: session=%s resume_phase=%d", sessionID, resume)
	}
	// Only a countdown was on screen; freezing an arm delay changes nothing a
	// client can see.
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
// given phase. Caller holds autoSettleMu.
//
// The ready channel is the same handshake nudge_countdown.go uses: the closure
// blocks until `timer` is published, so the identity check in the fire path reads
// a fully written value even when a short (test) window fires immediately.
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

// stopAutoSettleLocked cancels and forgets a session's pending settle, reporting
// whether the removed entry was visible to clients — i.e. whether the broadcast
// deadline just disappeared and a rebroadcast is owed. Caller holds autoSettleMu.
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

// autoSettleFire advances or completes a pending settle.
//
// The identity check against the map entry is what keeps a timer that lost a
// cancel/replace race from acting: it finds a different (or absent) entry and
// bails. Both phases re-check the preconditions rather than trusting the state
// that armed them — the window is seconds long and the session can have moved.
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
	// A hold that froze a running countdown is a picture change and rides the
	// wire. A re-hold, or a hold of the invisible arm delay, is not: while the
	// user keeps typing this fires every five seconds, and a snapshot push per
	// quiet check is traffic for a picture that never moved.
	if action == "held" && phase != autoSettleCounting {
		return
	}
	// Otherwise broadcast either way: the countdown's deadline has just appeared
	// (arm elapsed, or a hold released), just gone (settled, frozen, or the
	// preconditions lapsed), or the turn itself has closed.
	d.broadcastSessionStateChanged(sessionID)
}

// runAutoSettle is the fire-time decision, separated so its outcome is a single
// string a test hook can assert. `resume` is the phase a held session goes back
// to, and is read only in that phase.
func (d *Daemon) runAutoSettle(sessionID string, phase, resume autoSettlePhase) string {
	// Held across the whole decision — reading the state, confirming the turn is
	// still owed, and settling it — so no state write can land between the check
	// and the settle. Without this the timer could settle a turn that a
	// transition committed microseconds earlier had just opened: exactly the
	// turn the user has not dealt with yet. applyState takes the same lock
	// around its store write.
	d.autoSettleFireMu.Lock()
	defer d.autoSettleFireMu.Unlock()

	cfg := d.autoSettleConfig()
	if !cfg.enabled {
		// Turned off during the window. Dropping it is the honest reading of
		// "off": the user does not want turns closed for them.
		return "disabled"
	}
	if d.store == nil {
		return "noop"
	}
	session := d.store.Get(sessionID)
	if session == nil {
		return "gone"
	}
	// The state gate is re-checked here as well as in syncAutoSettle. The
	// transition path is the primary guard; this catches a session whose state
	// moved without a committed transition reaching us (a restart mid-window, a
	// store write from a path outside applyState).
	if string(session.State) != protocol.StateWorking {
		return "not-working"
	}
	if !d.turnOwed(sessionID) {
		// Settled by hand, or excluded (workspace pinned or muted mid-window).
		// Either way there is nothing left to close.
		return "not-owed"
	}

	// A phase that only moves the timer along can take the typing hold as a plain
	// check: nothing is committed on the far side of it, so a keystroke that
	// arrives a moment later still meets a pending timer and freezes it there.
	if phase == autoSettleHeld || phase == autoSettleArming {
		hold := phase
		if phase == autoSettleHeld {
			hold = resume
		}
		if quiet := d.userInputQuietRemaining(sessionID, autoSettleHoldQuietWindow); quiet > 0 {
			d.holdFromFire(sessionID, hold, quiet)
			return "held"
		}
		if phase == autoSettleHeld {
			// Quiet again. The window is read fresh from settings and starts
			// full: a frozen bar is drawn full, so resuming anything less would
			// drop the bar on release, and the user has just been typing at this
			// agent — the countdown they get back is the whole one they would
			// have got by steering it now.
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

	// Test seam: the instant before the settle commits. Nil in production. It
	// exists because the interleaving that makes this function dangerous — a real
	// keystroke arriving here, with the timer already pulled out of the map so
	// the hold it triggers has nothing to freeze — is far too narrow to hit by
	// chance, and the regression has to stand in it deliberately.
	if d.autoSettlePreSettleHook != nil {
		d.autoSettlePreSettleHook()
	}

	// The countdown ran out, so this is where the turn closes. The typing hold is
	// re-asked here rather than above because it has to be indivisible from the
	// write it guards — settleIfQuiet holds the keystroke lock across both — and
	// because the countdown timer can have fired in the microseconds around a
	// keystroke, before holdAutoSettle could stop it.
	quiet, settled := d.settleIfQuiet(sessionID, autoSettleHoldQuietWindow)
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

// holdFromFire parks a session the fire path found the user typing into, with a
// quiet check exactly at the end of the window their last keystroke opened.
func (d *Daemon) holdFromFire(sessionID string, resume autoSettlePhase, quiet time.Duration) {
	d.autoSettleMu.Lock()
	d.startAutoSettleHeldLocked(sessionID, resume, quiet)
	d.autoSettleMu.Unlock()
}

// cancelAutoSettleByUser is the user calling off a pending settle: keep this
// turn. It stops the countdown and does not re-arm — see CancelCountdownMessage
// for why a cancel has to survive the session simply continuing to work. Reports
// whether anything was pending, so the caller can log what the cancel reached.
func (d *Daemon) cancelAutoSettleByUser(sessionID string) bool {
	d.autoSettleMu.Lock()
	removed, _ := d.stopAutoSettleLocked(sessionID)
	if removed {
		// The suppression is what makes the cancel stick. Without it the very
		// next `working` transition — or the resolver re-reporting the state the
		// session is already in — re-arms, and the user's cancel buys them thirty
		// seconds. It is cleared when the session leaves `working`, so steering
		// the agent again arms a fresh countdown, which is the intent: the user
		// dealt with it, then dealt with it again.
		if d.autoSettleSuppressed == nil {
			d.autoSettleSuppressed = make(map[string]bool)
		}
		d.autoSettleSuppressed[sessionID] = true
	}
	d.autoSettleMu.Unlock()
	if d.debugLogging {
		d.logf("auto-settle canceled by user: session=%s had_pending=%v", sessionID, removed)
	}
	// No broadcast here: handleCancelCountdown makes exactly one, after every
	// countdown on the session has been called off, so a cancel that reaches both
	// does not push two snapshots.
	return removed
}

// decorateSessionWithAutoSettle stamps the broadcast clone with the countdown
// deadline while one is running, or the held flag while the user is typing. The
// two are mutually exclusive by construction — a frozen countdown has no
// deadline to animate against, and that is the point — so a client never has to
// decide which one wins. Read under autoSettleMu; callers must not already hold
// it.
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

// turnOwed reports whether the user owes this session a turn, by the same rule
// the broadcast decoration uses. It reads the decorated clone rather than
// re-deriving, so the timer and the queue can never disagree about who is owed.
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

// clearAutoSettleSuppression lifts a standing cancel. Called when the session
// leaves `working`, which is the edge that makes the next steer a new decision.
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

// armAutoSettleForRunningSessions arms every session that already qualifies.
// Turning the feature on is the one moment there is no transition to react to:
// the sessions it applies to are already sitting in `working`, and without this
// none of them would arm until their agent next changed state.
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
