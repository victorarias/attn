package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// The whole point of the generic cancel: the user presses one key at whatever is
// counting down in front of them, and it stops. A session counting down to both a
// settle and a nudge loses both.
func TestCancelCountdown_StopsBothCountdownsOnOneSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	t.Cleanup(d.stopAutoSettleTimers)
	// Windows long enough that the real timers never race the hand-fires below.
	d.store.SetSetting(SettingAutoSettleEnabled, "true")
	d.store.SetSetting(SettingAutoSettleArmSeconds, "3600")
	d.store.SetSetting(SettingAutoSettleCountdownSeconds, "3600")

	agentID, _ := armForTest(t, d)
	if currentNudgeTimer(d, agentID) == nil {
		t.Fatal("precondition: no nudge countdown armed")
	}
	// Only an owed turn can be auto-settled, so the fixture has to owe one.
	if !d.store.OpenTurnIfClosed(agentID, time.Now()) {
		t.Fatal("OpenTurnIfClosed() = false; the fixture owes no turn")
	}
	// Working is what sustains an auto-settle, and it keeps the nudge armed too
	// (only a pending approval blocks a doorbell), so both run at once.
	if !d.applyState(sessionStateChange{sessionID: agentID, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}
	fireAutoSettleNow(t, d, agentID)
	if _, ok := autoSettlePending(d, agentID); !ok {
		t.Fatal("precondition: no auto-settle countdown armed")
	}

	d.handleCancelCountdown(&protocol.CancelCountdownMessage{SessionID: agentID})

	if _, ok := autoSettlePending(d, agentID); ok {
		t.Fatal("auto-settle countdown survived the cancel")
	}
	if currentNudgeTimer(d, agentID) != nil {
		t.Fatal("nudge countdown survived the cancel")
	}
}

// A cancel that lands on a session with nothing counting down is a no-op, not an
// error: the shortcut is pressed at whatever is on screen, and a stale press must
// not settle, doorbell, or otherwise change anything.
func TestCancelCountdown_NoCountdownIsHarmless(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	_, agentID, inputs := delegateForNotify(t, d, "codex")
	d.store.UpdateState(agentID, protocol.StateIdle)

	d.handleCancelCountdown(&protocol.CancelCountdownMessage{SessionID: agentID})

	if wasNudged(inputs(agentID)) {
		t.Fatal("a cancel doorbelled the session")
	}
	if session := d.store.Get(agentID); session == nil || string(session.State) != protocol.StateIdle {
		t.Fatal("a cancel changed session state")
	}
}

// The cancel has to survive merely looking somewhere else. Selecting another
// session runs the resume in updateNudgeSelection, which re-derives a deadline
// from the same unread events — so without a standing cancel the nudge the user
// just called off comes straight back.
func TestCancelCountdown_NudgeStaysCancelledAcrossSelectionChange(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	chiefID, agentID, _ := delegateForNotify(t, d, "codex")
	ticketID := boundTicketID(t, d, agentID)
	d.store.UpdateState(agentID, protocol.StateIdle)
	commentOnTicket(t, d, ticketID, "take a look at the failing test")
	if currentNudgeTimer(d, agentID) == nil {
		t.Fatal("precondition: no nudge countdown armed")
	}

	d.handleCancelCountdown(&protocol.CancelCountdownMessage{SessionID: agentID})

	// Select the cancelled session and then leave it: the pause/resume pair is
	// the path that re-arms.
	d.setSelectedSession(agentID)
	d.setSelectedSession(chiefID)
	time.Sleep(50 * time.Millisecond) // the resume runs on its own goroutine

	if currentNudgeTimer(d, agentID) != nil {
		t.Fatal("the cancelled nudge re-armed on a selection change")
	}
	// The activity did not go away, and neither did the way back to it.
	clone := d.sessionForBroadcast(d.store.Get(agentID))
	if !protocol.Deref(clone.TicketUnread) {
		t.Fatal("cancelling the countdown also cleared the unread indicator")
	}
}

// "Not now" is an answer about what is pending, not a mute. Ticket activity that
// arrives after the cancel is new information and gets to ask again.
func TestCancelCountdown_NewerTicketActivityReArmsTheNudge(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	_, agentID, _ := delegateForNotify(t, d, "codex")
	ticketID := boundTicketID(t, d, agentID)
	d.store.UpdateState(agentID, protocol.StateIdle)
	commentOnTicket(t, d, ticketID, "take a look at the failing test")
	if currentNudgeTimer(d, agentID) == nil {
		t.Fatal("precondition: no nudge countdown armed")
	}

	d.handleCancelCountdown(&protocol.CancelCountdownMessage{SessionID: agentID})
	if currentNudgeTimer(d, agentID) != nil {
		t.Fatal("precondition: cancel did not stop the countdown")
	}

	commentOnTicket(t, d, ticketID, "actually, this is now blocking the release")

	waitForNudgeDeadline(t, d, agentID)
}

// Cancelling the settle half keeps the turn owed. This is the behavior the whole
// feature exists for, asserted through the generic command rather than through
// the auto-settle internals.
func TestCancelCountdown_KeepsTheTurnOwed(t *testing.T) {
	d, id := newAutoSettleDaemon(t)
	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}
	fireAutoSettleNow(t, d, id)

	d.handleCancelCountdown(&protocol.CancelCountdownMessage{SessionID: id})

	if !turnIsOwed(d, id) {
		t.Fatal("the cancel settled the turn it was supposed to keep")
	}
}
