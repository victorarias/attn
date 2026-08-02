package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// newAutoSettleDaemon builds a daemon with one codex session that already owes the
// user a turn (opened on `waiting_input`), auto-settle on, and windows long enough
// that the real timers never race a hand-fire. The tests drive the phases through
// autoSettleFire with the live timer handle — the same deterministic path
// nudge_countdown_test.go uses.
func newAutoSettleDaemon(t *testing.T) (*Daemon, string) {
	t.Helper()
	d := NewForTesting(filepath.Join(t.TempDir(), "auto-settle.sock"))
	id := "session"
	d.store.Add(&protocol.Session{
		ID:             id,
		Label:          id,
		Agent:          protocol.SessionAgentCodex,
		Directory:      t.TempDir(),
		State:          protocol.SessionStateWaitingInput,
		StateSince:     characterizationOldTimestamp,
		StateUpdatedAt: characterizationOldTimestamp,
		LastSeen:       characterizationOldTimestamp,
	})
	d.store.SetSetting(SettingAutoSettleEnabled, "true")
	d.store.SetSetting(SettingAutoSettleArmSeconds, "3600")
	d.store.SetSetting(SettingAutoSettleCountdownSeconds, "3600")
	// The turn the whole feature acts on. Opening it here rather than through a
	// state transition keeps the fixture about auto-settle, not about the
	// resolver.
	if !d.store.OpenTurnIfClosed(id, time.Now()) {
		t.Fatal("OpenTurnIfClosed() = false; the fixture owes no turn")
	}
	return d, id
}

func autoSettlePending(d *Daemon, sessionID string) (*autoSettleTimer, bool) {
	d.autoSettleMu.Lock()
	defer d.autoSettleMu.Unlock()
	entry, ok := d.autoSettleTimers[sessionID]
	return entry, ok
}

// fireAutoSettleNow advances the pending timer by hand, with the live handle, so
// the identity check in autoSettleFire accepts it.
func fireAutoSettleNow(t *testing.T, d *Daemon, sessionID string) {
	t.Helper()
	entry, ok := autoSettlePending(d, sessionID)
	if !ok {
		t.Fatalf("no auto-settle pending for %s", sessionID)
	}
	entry.timer.Stop()
	d.autoSettleFire(sessionID, entry.timer)
}

func turnIsOwed(d *Daemon, sessionID string) bool {
	return d.turnOwed(sessionID)
}

// The whole feature in one pass: steering an agent back to work arms an invisible
// delay, the delay elapsing starts a countdown clients can see, and the countdown
// elapsing closes the turn.
func TestAutoSettle_ArmsThenCountsDownThenSettles(t *testing.T) {
	d, id := newAutoSettleDaemon(t)

	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}

	entry, ok := autoSettlePending(d, id)
	if !ok {
		t.Fatal("no auto-settle armed after the session went to work")
	}
	if entry.phase != autoSettleArming {
		t.Fatalf("phase = %v, want arming", entry.phase)
	}
	// The arming phase is deliberately invisible: a countdown on screen would
	// announce a settle that has not been decided on yet.
	clone := d.sessionForBroadcast(d.store.Get(id))
	if clone.AutoSettleFiresAt != nil {
		t.Fatalf("auto_settle_fires_at = %q during arming, want absent", *clone.AutoSettleFiresAt)
	}

	fireAutoSettleNow(t, d, id)

	entry, ok = autoSettlePending(d, id)
	if !ok || entry.phase != autoSettleCounting {
		t.Fatalf("after the arm delay: pending=%v entry=%+v, want a counting phase", ok, entry)
	}
	clone = d.sessionForBroadcast(d.store.Get(id))
	if clone.AutoSettleFiresAt == nil {
		t.Fatal("auto_settle_fires_at absent while counting; the client has nothing to animate")
	}
	if !turnIsOwed(d, id) {
		t.Fatal("turn settled during the countdown; it must still be owed until the countdown ends")
	}

	fireAutoSettleNow(t, d, id)

	if turnIsOwed(d, id) {
		t.Fatal("turn still owed after the countdown elapsed; want settled")
	}
	if _, ok := autoSettlePending(d, id); ok {
		t.Fatal("a timer is still pending after the settle")
	}
	clone = d.sessionForBroadcast(d.store.Get(id))
	if clone.AutoSettleFiresAt != nil {
		t.Fatalf("auto_settle_fires_at = %q after settling, want absent", *clone.AutoSettleFiresAt)
	}
}

// The behavior that must not be got wrong: an agent that wants the user again
// aborts the settle, in every state that is not `working`. A settle here would
// bury exactly the thing the user needs to see.
func TestAutoSettle_LeavingWorkingAborts(t *testing.T) {
	for _, state := range []string{
		protocol.StateWaitingInput,
		protocol.StatePendingApproval,
		protocol.StateUnknown,
		protocol.StateIdle,
		string(protocol.SessionStateRecoverable),
		protocol.StateScheduled,
		protocol.StateLaunching,
	} {
		t.Run(state, func(t *testing.T) {
			d, id := newAutoSettleDaemon(t)
			if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
				t.Fatal("applyState(working) = false")
			}
			// Advance into the visible countdown, which is the worst moment to
			// get this wrong: the settle is seconds away.
			fireAutoSettleNow(t, d, id)
			if _, ok := autoSettlePending(d, id); !ok {
				t.Fatal("no countdown to abort")
			}

			if !d.applyState(sessionStateChange{sessionID: id, state: state, cause: liveSignal{}}) {
				t.Fatalf("applyState(%s) = false", state)
			}

			if _, ok := autoSettlePending(d, id); ok {
				t.Fatalf("countdown survived the move to %s; the agent wants the user and its turn would be buried", state)
			}
			if !turnIsOwed(d, id) {
				t.Fatalf("turn was settled on the move to %s", state)
			}
		})
	}
}

// A re-reported `working` must not restart the delay. The resolver commits the
// same state repeatedly, so a sliding window would never elapse.
func TestAutoSettle_ReReportedWorkingKeepsTheSameTimer(t *testing.T) {
	d, id := newAutoSettleDaemon(t)
	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}
	first, _ := autoSettlePending(d, id)

	for i := 0; i < 3; i++ {
		d.syncAutoSettle(id, protocol.StateWorking)
	}

	again, ok := autoSettlePending(d, id)
	if !ok {
		t.Fatal("timer disappeared on a re-reported working")
	}
	if again.timer != first.timer || !again.firesAt.Equal(first.firesAt) {
		t.Fatalf("timer was replaced on a re-reported working: deadline %v -> %v", first.firesAt, again.firesAt)
	}
}

// Cancel keeps the turn, and keeps it through the session simply continuing to
// work — otherwise the very next re-reported `working` re-arms and the cancel
// buys the user thirty seconds.
func TestAutoSettle_CancelKeepsTheTurnAndDoesNotReArm(t *testing.T) {
	d, id := newAutoSettleDaemon(t)
	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}
	fireAutoSettleNow(t, d, id)

	d.handleCancelCountdown(&protocol.CancelCountdownMessage{SessionID: id})

	if _, ok := autoSettlePending(d, id); ok {
		t.Fatal("countdown survived the cancel")
	}
	if !turnIsOwed(d, id) {
		t.Fatal("turn was settled by the cancel")
	}
	clone := d.sessionForBroadcast(d.store.Get(id))
	if clone.AutoSettleFiresAt != nil {
		t.Fatalf("auto_settle_fires_at = %q after cancel, want absent", *clone.AutoSettleFiresAt)
	}

	d.syncAutoSettle(id, protocol.StateWorking)
	if _, ok := autoSettlePending(d, id); ok {
		t.Fatal("re-armed while the session kept working; the cancel must stand")
	}

	// Steering the agent again is a new decision, so leaving and re-entering
	// `working` arms a fresh countdown.
	d.syncAutoSettle(id, protocol.StateWaitingInput)
	d.syncAutoSettle(id, protocol.StateWorking)
	if _, ok := autoSettlePending(d, id); !ok {
		t.Fatal("no fresh arm after the session left and re-entered working")
	}
}

// Off is off: no timer arms at all, which is what makes the default a true no-op.
func TestAutoSettle_DisabledNeverArms(t *testing.T) {
	d, id := newAutoSettleDaemon(t)
	d.store.SetSetting(SettingAutoSettleEnabled, "false")

	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}

	if _, ok := autoSettlePending(d, id); ok {
		t.Fatal("armed with auto-settle disabled")
	}
}

// Switching the feature off must stop a countdown already on screen rather than
// let it run out under a setting the user has just revoked.
func TestAutoSettle_DisablingCancelsARunningCountdown(t *testing.T) {
	d, id := newAutoSettleDaemon(t)
	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}
	fireAutoSettleNow(t, d, id)
	if _, ok := autoSettlePending(d, id); !ok {
		t.Fatal("no countdown to cancel")
	}

	d.handleSetSettingWS(&wsClient{}, &protocol.SetSettingMessage{
		Cmd:   protocol.CmdSetSetting,
		Key:   SettingAutoSettleEnabled,
		Value: "false",
	})

	if _, ok := autoSettlePending(d, id); ok {
		t.Fatal("countdown survived turning the feature off")
	}
	if !turnIsOwed(d, id) {
		t.Fatal("turn was settled by turning the feature off")
	}
}

// A session that owes nothing has nothing to settle. This is also what keeps
// shells, the chief, and pinned/muted workspaces out — turnOwed carries those
// exclusions, so they are excluded here for the same reason they are excluded
// from the queue.
func TestAutoSettle_NoTurnOwedNeverArms(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "auto-settle.sock"))
	id := "session"
	d.store.Add(&protocol.Session{
		ID:             id,
		Label:          id,
		Agent:          protocol.SessionAgentCodex,
		Directory:      t.TempDir(),
		State:          protocol.SessionStateIdle,
		StateSince:     characterizationOldTimestamp,
		StateUpdatedAt: characterizationOldTimestamp,
		LastSeen:       characterizationOldTimestamp,
	})
	d.store.SetSetting(SettingAutoSettleEnabled, "true")
	d.store.SetSetting(SettingAutoSettleArmSeconds, "3600")
	// No OpenTurnIfClosed: nothing is owed. Drive the transition through
	// syncAutoSettle so the turn applyState would open cannot mask the case.
	d.store.UpdateState(id, protocol.StateWorking)
	d.syncAutoSettle(id, protocol.StateWorking)

	if _, ok := autoSettlePending(d, id); ok {
		t.Fatal("armed for a session that owes no turn")
	}
}

// Settling by hand takes the countdown with it: there is no turn left to close,
// and a countdown left on screen would promise a second settle.
func TestAutoSettle_ManualSettleCancelsTheCountdown(t *testing.T) {
	d, id := newAutoSettleDaemon(t)
	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}
	fireAutoSettleNow(t, d, id)

	d.handleSettleTurn(&protocol.SettleTurnMessage{SessionID: id})

	if _, ok := autoSettlePending(d, id); ok {
		t.Fatal("countdown survived a manual settle")
	}
	if turnIsOwed(d, id) {
		t.Fatal("manual settle did not close the turn")
	}
}

// The fire-time re-check is a second guard, not the primary one: a session whose
// state moved without a committed transition reaching syncAutoSettle must still
// not be settled.
func TestAutoSettle_FireTimeRecheckRefusesANonWorkingSession(t *testing.T) {
	d, id := newAutoSettleDaemon(t)
	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}
	fireAutoSettleNow(t, d, id)

	outcomes := make(chan string, 1)
	d.autoSettleFireHook = func(_, outcome string) { outcomes <- outcome }
	// Move the state behind the timer's back — no applyState, so nothing
	// cancelled the countdown.
	d.store.UpdateState(id, protocol.StateWaitingInput)

	fireAutoSettleNow(t, d, id)

	if got := <-outcomes; got != "not-working" {
		t.Fatalf("outcome = %q, want %q", got, "not-working")
	}
	if !turnIsOwed(d, id) {
		t.Fatal("turn was settled despite the session no longer working")
	}
}

// End to end on real timers, with the windows turned down: the feature works
// without the hand-fire the other tests use.
func TestAutoSettle_RealTimersSettleTheTurn(t *testing.T) {
	d, id := newAutoSettleDaemon(t)
	d.store.SetSetting(SettingAutoSettleArmSeconds, "1")
	d.store.SetSetting(SettingAutoSettleCountdownSeconds, "1")

	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !turnIsOwed(d, id) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("turn was never auto-settled within 5s of a 1s+1s policy")
}

func TestValidateAutoSettleSeconds(t *testing.T) {
	for _, tc := range []struct {
		name    string
		key     string
		value   string
		wantErr bool
	}{
		{name: "blank arm means default", key: SettingAutoSettleArmSeconds, value: ""},
		{name: "arm in range", key: SettingAutoSettleArmSeconds, value: "30"},
		{name: "arm below floor", key: SettingAutoSettleArmSeconds, value: "1", wantErr: true},
		{name: "arm above ceiling", key: SettingAutoSettleArmSeconds, value: "99999", wantErr: true},
		{name: "arm not a number", key: SettingAutoSettleArmSeconds, value: "soon", wantErr: true},
		{name: "countdown in range", key: SettingAutoSettleCountdownSeconds, value: "15"},
		{name: "countdown below floor", key: SettingAutoSettleCountdownSeconds, value: "1", wantErr: true},
		{name: "enabled accepts a boolean", key: SettingAutoSettleEnabled, value: "true"},
		{name: "enabled rejects a number", key: SettingAutoSettleEnabled, value: "30", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := NewForTesting(filepath.Join(t.TempDir(), "auto-settle.sock"))
			err := d.validateSetting(tc.key, tc.value)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateSetting(%q, %q) = %v, wantErr %v", tc.key, tc.value, err, tc.wantErr)
			}
		})
	}
}

// The settings payload carries the effective policy, so the UI shows 30/15 rather
// than blank fields it would have to know the defaults for.
func TestAutoSettleSettingsSurfaceEffectiveDefaults(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "auto-settle.sock"))
	settings := d.settingsWithAgentAvailability()

	for key, want := range map[string]string{
		SettingAutoSettleEnabled:          "false",
		SettingAutoSettleArmSeconds:       "30",
		SettingAutoSettleCountdownSeconds: "15",
	} {
		if got := settings[key]; got != want {
			t.Errorf("settings[%q] = %v, want %q", key, got, want)
		}
	}
}

// A turn the user has not dealt with must survive the timer firing at the same
// instant the session demands them again.
//
// The window this pins: runAutoSettle reads `working` and confirms the turn is
// owed, and only then settles. If a transition into pending_approval commits
// between those two steps, the timer goes on to settle a turn the user is being
// asked to act on right now — and syncAutoSettle's cancel, which runs after the
// state write, arrives too late to stop it. The session would drop off the queue
// while the agent sits waiting for an answer.
//
// The test stands in that exact instant via autoSettlePreSettleHook, because the
// window is far too narrow to hit by racing goroutines: a version of this test
// that simply ran the fire and the transition concurrently passed just as
// happily without the lock as with it.
//
// Both orderings are acceptable, and both end the same way. If the timer wins it
// settles the `working` turn and the pending_approval transition immediately
// opens a fresh one; if the transition wins the timer sees a non-working session
// and declines. Either way the user still owes this session a turn, which is why
// that is the invariant asserted rather than a particular interleaving.
func TestAutoSettle_ConcurrentApprovalKeepsTheTurn(t *testing.T) {
	d, id := newAutoSettleDaemon(t)
	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}
	// Into the visible countdown: the settle is the very next fire.
	fireAutoSettleNow(t, d, id)

	entry, ok := autoSettlePending(d, id)
	if !ok {
		t.Fatal("no countdown to race against")
	}

	approvalDone := make(chan struct{})
	release := make(chan struct{})
	go func() {
		defer close(approvalDone)
		<-release
		d.applyState(sessionStateChange{
			sessionID: id,
			state:     protocol.StatePendingApproval,
			cause:     liveSignal{},
		})
	}()

	d.autoSettlePreSettleHook = func() {
		// Turn the approval loose and give it every chance to commit before this
		// settle proceeds. Serialized correctly, it cannot: it blocks on the
		// state-transition gate until the settle is done.
		close(release)
		time.Sleep(100 * time.Millisecond)
	}

	d.autoSettleFire(id, entry.timer)
	<-approvalDone

	if state := string(d.store.Get(id).State); state != protocol.StatePendingApproval {
		t.Fatalf("state = %s, want pending_approval", state)
	}
	if !d.turnOwed(id) {
		t.Fatal("the turn was settled while the session was asking for approval")
	}
}
