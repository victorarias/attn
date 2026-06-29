package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// currentNudgeTimer returns the session's armed countdown timer, or nil when none is
// running (paused, canceled, or never armed).
func currentNudgeTimer(d *Daemon, sessionID string) *time.Timer {
	d.nudgeMu.Lock()
	defer d.nudgeMu.Unlock()
	if c, ok := d.nudgeCountdowns[sessionID]; ok {
		return c.timer
	}
	return nil
}

// fireNudgeNow simulates the countdown timer firing immediately, by invoking the fire
// callback with the live timer handle — the faithful deterministic path (mirrors the
// backstop tests' hour-long-grace + fire-by-hand pattern). Tests that use it set an
// hour-long window override so the real timer never races this hand-fire.
func fireNudgeNow(t *testing.T, d *Daemon, sessionID string) {
	t.Helper()
	timer := currentNudgeTimer(d, sessionID)
	if timer == nil {
		t.Fatalf("no nudge countdown armed for session %s", sessionID)
	}
	timer.Stop()
	d.nudgeCountdownFire(sessionID, timer)
}

// waitForNudge polls until the session received a doorbell, failing on timeout. Used
// only by the end-to-end real-timer test; the rest fire deterministically.
func waitForNudge(t *testing.T, inputs func(string) []string, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if wasNudged(inputs(sessionID)) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("session %s was never doorbelled", sessionID)
}

// armForTest gives an idle codex agent an unread chief steer, which arms its nudge
// countdown (codex is a non-self-monitor, so notify resolves to the immediate-nudge
// path that now arms the countdown). Returns the agent id and inputs accessor.
func armForTest(t *testing.T, d *Daemon) (agentID string, inputs func(string) []string) {
	t.Helper()
	_, agentID, inputs = delegateForNotify(t, d, "codex")
	ticketID := boundTicketID(t, d, agentID)
	d.store.UpdateState(agentID, protocol.StateIdle)
	commentOnTicket(t, d, ticketID, "take a look at the failing test")
	return agentID, inputs
}

// The end-to-end real-timer path: an idle, inactive codex with unread activity arms a
// countdown that, on its own, fires after the (tiny) window and doorbells.
func TestNudgeCountdownFiresWhenInactive(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = 15 * time.Millisecond
	t.Cleanup(d.stopNudgeCountdowns)
	agentID, inputs := armForTest(t, d)

	waitForNudge(t, inputs, agentID)
}

// The active (currently-selected) session never auto-fires: its countdown is paused
// (no timer, no nudge_fires_at) and it carries the unread marker plus a click-to-
// trigger affordance instead.
func TestNudgeCountdownPausedWhileActive(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	_, agentID, inputs := delegateForNotify(t, d, "codex")
	ticketID := boundTicketID(t, d, agentID)
	d.store.UpdateState(agentID, protocol.StateIdle)
	d.setSelectedSession(agentID) // the user is in this session

	commentOnTicket(t, d, ticketID, "take a look")

	if timer := currentNudgeTimer(d, agentID); timer != nil {
		t.Fatal("active session armed a countdown instead of pausing")
	}
	if wasNudged(inputs(agentID)) {
		t.Fatal("active session was doorbelled (splice risk)")
	}
	clone := d.sessionForBroadcast(d.store.Get(agentID))
	if !protocol.Deref(clone.TicketUnread) {
		t.Fatal("active session lost its unread indicator")
	}
	if clone.NudgeFiresAt != nil {
		t.Fatal("paused (active) session should not broadcast a countdown deadline")
	}
}

// Switching away from the active session resumes its paused countdown so attn
// delivers the nudge later.
func TestNudgeCountdownResumesOnSwitchAway(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	chiefID, agentID, _ := delegateForNotify(t, d, "codex")
	ticketID := boundTicketID(t, d, agentID)
	d.store.UpdateState(agentID, protocol.StateIdle)
	d.setSelectedSession(agentID)
	commentOnTicket(t, d, ticketID, "take a look") // paused while active
	if currentNudgeTimer(d, agentID) != nil {
		t.Fatal("countdown ran while the session was active")
	}

	d.setSelectedSession(chiefID) // switch away

	if currentNudgeTimer(d, agentID) == nil {
		t.Fatal("countdown did not resume after switching away from the session")
	}
}

// Switching TO a session with a running countdown pauses it (no auto-fire while the
// user is in it).
func TestNudgeCountdownPausesOnSwitchTo(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	agentID, _ := armForTest(t, d) // inactive -> countdown running
	if currentNudgeTimer(d, agentID) == nil {
		t.Fatal("inactive session did not arm a countdown")
	}

	d.setSelectedSession(agentID) // user switches into it

	if currentNudgeTimer(d, agentID) != nil {
		t.Fatal("countdown kept running after the session became active")
	}
}

// Leaving idle (the agent starts working) cancels the countdown — you don't count
// down to nudge a working agent. Driven through a non-PTY state-broadcast path, since
// codex/claude state is hook-owned and never flows through handlePTYState.
func TestNudgeCountdownCanceledOnLeavingIdle(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	agentID, _ := armForTest(t, d)
	if currentNudgeTimer(d, agentID) == nil {
		t.Fatal("precondition: no countdown armed")
	}

	d.updateAndBroadcastState(agentID, protocol.StateWorking)

	if currentNudgeTimer(d, agentID) != nil {
		t.Fatal("countdown survived the agent leaving idle")
	}
}

// Draining the inbox clears the indicator and cancels the countdown — the chokepoint
// a self-monitoring agent's own watch drains through. After draining, nothing is
// unread, so there is nothing to nudge.
func TestNudgeCountdownClearedWhenInboxDrained(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	agentID, inputs := armForTest(t, d)
	if currentNudgeTimer(d, agentID) == nil {
		t.Fatal("precondition: no countdown armed")
	}

	callTicketInbox(t, d, agentID) // the agent reads its queue

	if currentNudgeTimer(d, agentID) != nil {
		t.Fatal("countdown survived the inbox drain")
	}
	clone := d.sessionForBroadcast(d.store.Get(agentID))
	if protocol.Deref(clone.TicketUnread) {
		t.Fatal("indicator stuck on after the inbox drained to zero")
	}
	if wasNudged(inputs(agentID)) {
		t.Fatal("a drained session was still doorbelled")
	}
}

// Clicking the indicator (trigger_nudge) delivers the doorbell immediately, bypassing
// the countdown.
func TestTriggerNudgeFiresImmediately(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	agentID, inputs := armForTest(t, d)

	d.handleTriggerNudge(&protocol.TriggerNudgeMessage{
		Cmd:       protocol.CmdTriggerNudge,
		SessionID: agentID,
	})

	if !wasNudged(inputs(agentID)) {
		t.Fatal("trigger_nudge did not doorbell immediately")
	}
	if currentNudgeTimer(d, agentID) != nil {
		t.Fatal("trigger_nudge left a countdown running")
	}
}

// The anti-splice guard: a genuine user keystroke within the guard window blocks the
// fire and re-arms a fresh countdown rather than splicing the doorbell onto the
// half-typed line.
func TestNudgeCountdownReArmsAfterRecentKeystroke(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	var action string
	d.nudgeFireHook = func(_, a string) { action = a }
	agentID, inputs := armForTest(t, d)

	d.noteUserInput(agentID, "") // the user is mid-keystroke (untagged source)
	fireNudgeNow(t, d, agentID)

	if action != "rearm" {
		t.Fatalf("fire action = %q, want rearm (keystroke guard)", action)
	}
	if wasNudged(inputs(agentID)) {
		t.Fatal("doorbell spliced onto a session the user was actively typing into")
	}
	if currentNudgeTimer(d, agentID) == nil {
		t.Fatal("guard dropped the nudge instead of re-arming")
	}
}

// Automation and replay writes are not the user typing, so they do not trip the
// keystroke guard.
func TestNudgeKeystrokeGuardIgnoresAutomation(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	var action string
	d.nudgeFireHook = func(_, a string) { action = a }
	agentID, inputs := armForTest(t, d)

	d.noteUserInput(agentID, "automation")
	d.noteUserInput(agentID, "attach_replay")
	fireNudgeNow(t, d, agentID)

	if action != "doorbell" {
		t.Fatalf("fire action = %q, want doorbell (automation must not trip the guard)", action)
	}
	if !wasNudged(inputs(agentID)) {
		t.Fatal("automation input wrongly suppressed the doorbell")
	}
}

// Teardown stops every armed timer so no AfterFunc goroutine outlives the daemon.
func TestStopNudgeCountdownsClearsTimers(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	agentID, _ := armForTest(t, d)
	if currentNudgeTimer(d, agentID) == nil {
		t.Fatal("precondition: no countdown armed")
	}

	d.stopNudgeCountdowns()

	if currentNudgeTimer(d, agentID) != nil {
		t.Fatal("stopNudgeCountdowns left a countdown armed")
	}
}
