package daemon

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionstate"
)

func snoozeUntil(d *Daemon, id string, until time.Time) {
	d.handleSnoozeTurn(&protocol.SnoozeTurnMessage{
		SessionID: id,
		Until:     until.Format(time.RFC3339Nano),
	})
}

func snoozedUntil(t *testing.T, d *Daemon, id string) string {
	t.Helper()
	session := d.sessionForBroadcast(d.store.Get(id))
	if session == nil {
		t.Fatalf("session %s not found", id)
	}
	return protocol.Deref(session.TurnSnoozedUntil)
}

// The whole of the deferral: the agent keeps stopping and asking, and none of it
// puts the session back on the user's plate.
func TestSnoozeSuppressesTurnsUntilItsDeadline(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")

	moveTo(d, "s1", protocol.StateWaitingInput)
	if !owed(t, d, "s1") {
		t.Fatal("setup: a waiting session owes no turn")
	}

	snoozeUntil(d, "s1", time.Now().Add(time.Hour))
	if owed(t, d, "s1") {
		t.Fatal("the session still owes a turn immediately after snoozing")
	}
	if snoozedUntil(t, d, "s1") == "" {
		t.Fatal("no deadline on the wire, so the row has nothing to park under")
	}

	// Every ordinary way an agent comes back for the user, while deferred.
	for _, state := range []string{
		protocol.StateWorking,
		protocol.StateWaitingInput,
		protocol.StatePendingApproval,
		protocol.StateIdle,
	} {
		moveTo(d, "s1", state)
		if owed(t, d, "s1") {
			t.Fatalf("state %s opened a turn on a snoozed session", state)
		}
	}
}

// Waking stamps the turn at the instant the user said they would come back, so
// the row lands at the tail of the queue rather than resurfacing at the age it
// had when it was deferred.
func TestWakeOpensTheTurnAtTheWakeInstant(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")

	moveTo(d, "s1", protocol.StateWaitingInput)
	original := protocol.Deref(d.sessionForBroadcast(d.store.Get("s1")).TurnOpenedAt)

	snoozeUntil(d, "s1", time.Now().Add(time.Hour))
	moveTo(d, "s1", protocol.StateIdle)

	wakeAt := time.Now().Add(time.Hour)
	d.wakeSnooze("s1", wakeAt, "test")

	if !owed(t, d, "s1") {
		t.Fatal("the woken session owes no turn although it is sitting idle")
	}
	opened := protocol.Deref(d.sessionForBroadcast(d.store.Get("s1")).TurnOpenedAt)
	if opened == original {
		t.Error("the turn kept its pre-snooze age, so it wakes to the head of the queue")
	}
	parsed, err := time.Parse(time.RFC3339Nano, opened)
	if err != nil {
		t.Fatalf("turn_opened_at %q does not parse: %v", opened, err)
	}
	if !parsed.Equal(wakeAt.UTC()) {
		t.Errorf("turn opened at %s, want the wake instant %s", parsed, wakeAt.UTC())
	}
	if snoozedUntil(t, d, "s1") != "" {
		t.Error("the deadline survived the wake")
	}
}

// Waking a busy agent opens nothing. That is not a missed turn: the suppression
// is gone, so the next state that wants the user opens one normally.
func TestWakeOpensNoTurnWhileTheAgentIsWorking(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")

	moveTo(d, "s1", protocol.StateWorking)
	snoozeUntil(d, "s1", time.Now().Add(time.Hour))
	d.wakeSnooze("s1", time.Now(), "test")

	if owed(t, d, "s1") {
		t.Fatal("waking a working agent opened a turn")
	}
	moveTo(d, "s1", protocol.StateWaitingInput)
	if !owed(t, d, "s1") {
		t.Error("the session stayed suppressed after the snooze was cleared")
	}
}

// What the user could not have anticipated still gets through, and consumes the
// deferral with it — they are back in the loop with that agent.
func TestBreakThroughStatesEndTheSnooze(t *testing.T) {
	tests := []struct {
		name   string
		state  string
		reason sessionstate.Reason
		breaks bool
	}{
		{"stuck", protocol.StateUnknown, sessionstate.ReasonStuck, true},
		{"process exited", protocol.StateIdle, sessionstate.ReasonProcessExited, true},
		{"an ordinary finished run", protocol.StateIdle, sessionstate.ReasonClassifierVerdict, false},
		{"a question", protocol.StateWaitingInput, sessionstate.ReasonQuestionOpen, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newTurnDaemon(t)
			addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")

			moveTo(d, "s1", protocol.StateWorking)
			snoozeUntil(d, "s1", time.Now().Add(time.Hour))

			// The reason is filed the way publishResolution files it, immediately
			// before the state it describes is applied.
			d.recordStateReason("s1", sessionstate.Resolution{
				State:  protocol.SessionState(tt.state),
				Reason: tt.reason,
			})
			moveTo(d, "s1", tt.state)

			if got := owed(t, d, "s1"); got != tt.breaks {
				t.Errorf("owed = %v, want %v", got, tt.breaks)
			}
			stillSnoozed := snoozedUntil(t, d, "s1") != ""
			if stillSnoozed == tt.breaks {
				t.Errorf("snooze live = %v after a state that breaks=%v; a break-through must consume the deferral",
					stillSnoozed, tt.breaks)
			}
		})
	}
}

// Snooze reaches any agent, not only one that owes a turn. Deferring a run
// before it finishes is the case the reach exists for: the turn it would have
// opened on finishing never opens.
func TestSnoozingAWorkingAgentSuppressesTheTurnItWouldOpen(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")

	moveTo(d, "s1", protocol.StateWorking)
	if owed(t, d, "s1") {
		t.Fatal("setup: a working session owes a turn")
	}
	snoozeUntil(d, "s1", time.Now().Add(time.Hour))

	moveTo(d, "s1", protocol.StateIdle)
	if owed(t, d, "s1") {
		t.Error("the finished run opened a turn although the agent was deferred")
	}
}

// The deadline is persisted; the timer that fires on it is not. Without the
// start-up reschedule a snooze would survive a restart as a session that never
// comes back — the worst failure a deferral can have.
func TestSnoozeWakesAfterARestart(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")

	moveTo(d, "s1", protocol.StateIdle)
	// Written straight to the store, which is the state a restart finds: the
	// deadline persisted, and no timer in memory to fire on it. Letting it lapse
	// before the reschedule is the daemon having been down across it.
	now := time.Now()
	deadline := now.Add(10 * time.Millisecond)
	if !d.store.SnoozeTurn("s1", deadline, now) {
		t.Fatal("setup: the snooze was not stored")
	}
	time.Sleep(30 * time.Millisecond)
	if owed(t, d, "s1") {
		t.Fatal("setup: the session is not deferred")
	}

	woken := make(chan string, 1)
	d.snoozeWakeHook = func(sessionID string) { woken <- sessionID }
	d.rescheduleSnoozeWakes()

	select {
	case <-woken:
	case <-time.After(2 * time.Second):
		t.Fatal("the lapsed snooze never woke after the reschedule")
	}
	if !owed(t, d, "s1") {
		t.Error("the woken session owes no turn although it is sitting idle")
	}
}

func TestSnoozeTimerFiresOnItsDeadline(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")

	moveTo(d, "s1", protocol.StateWaitingInput)
	woken := make(chan string, 1)
	d.snoozeWakeHook = func(sessionID string) { woken <- sessionID }

	snoozeUntil(d, "s1", time.Now().Add(20*time.Millisecond))
	if owed(t, d, "s1") {
		t.Fatal("the session still owes a turn immediately after snoozing")
	}

	select {
	case <-woken:
	case <-time.After(2 * time.Second):
		t.Fatal("the snooze timer never fired")
	}
	if !owed(t, d, "s1") {
		t.Error("the session owes no turn after its snooze elapsed")
	}
}

// Re-snoozing to a later deadline must not be woken by the timer the first
// snooze armed.
func TestResnoozingReplacesThePendingWake(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")
	moveTo(d, "s1", protocol.StateWaitingInput)

	snoozeUntil(d, "s1", time.Now().Add(20*time.Millisecond))
	snoozeUntil(d, "s1", time.Now().Add(time.Hour))

	time.Sleep(200 * time.Millisecond)
	if owed(t, d, "s1") {
		t.Error("the superseded timer woke a session that had been re-snoozed for an hour")
	}
	if snoozedUntil(t, d, "s1") == "" {
		t.Error("the superseded timer cleared the live deadline")
	}
}

// A snooze arriving mid-countdown makes the pending auto-settle moot: the turn
// it was going to close is already closed, and leaving the deadline on the wire
// would animate a settle that will never happen.
func TestSnoozeCancelsAPendingAutoSettle(t *testing.T) {
	d := newTurnDaemon(t)
	d.store.SetSetting(SettingAutoSettleEnabled, "true")
	d.store.SetSetting(SettingAutoSettleArmSeconds, "5")
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")

	moveTo(d, "s1", protocol.StateWaitingInput)
	moveTo(d, "s1", protocol.StateWorking)

	d.autoSettleMu.Lock()
	pending := len(d.autoSettleTimers)
	d.autoSettleMu.Unlock()
	if pending == 0 {
		t.Fatal("setup: no auto-settle armed")
	}

	snoozeUntil(d, "s1", time.Now().Add(time.Hour))

	d.autoSettleMu.Lock()
	pending = len(d.autoSettleTimers)
	d.autoSettleMu.Unlock()
	if pending != 0 {
		t.Error("the auto-settle timer survived the snooze")
	}
}

// A deadline that has already passed is not a live snooze on the wire: the wake
// is racing this broadcast, and announcing it would park the row for as long as
// the timer took to land.
func TestALapsedDeadlineIsNotBroadcast(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")
	moveTo(d, "s1", protocol.StateWorking)

	d.store.SnoozeTurn("s1", time.Now().Add(-time.Minute), time.Now())
	if got := snoozedUntil(t, d, "s1"); got != "" {
		t.Errorf("turn_snoozed_until = %q for a deadline already past, want empty", got)
	}
}

func TestSnoozeRejectsAnUnparseableDeadline(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")
	moveTo(d, "s1", protocol.StateWaitingInput)

	d.handleSnoozeTurn(&protocol.SnoozeTurnMessage{SessionID: "s1", Until: "next tuesday"})

	if !owed(t, d, "s1") {
		t.Error("a malformed deadline settled the turn anyway")
	}
	if snoozedUntil(t, d, "s1") != "" {
		t.Error("a malformed deadline was stored")
	}
}
