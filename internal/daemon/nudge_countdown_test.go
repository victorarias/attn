package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ticketnotify"
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

func currentNudgeDeadline(d *Daemon, sessionID string) time.Time {
	d.nudgeMu.Lock()
	defer d.nudgeMu.Unlock()
	if c, ok := d.nudgeCountdowns[sessionID]; ok {
		return c.firesAt
	}
	return time.Time{}
}

func waitForNudgeDeadline(t *testing.T, d *Daemon, sessionID string) time.Time {
	t.Helper()
	limit := time.Now().Add(time.Second)
	for time.Now().Before(limit) {
		if deadline := currentNudgeDeadline(d, sessionID); !deadline.IsZero() {
			return deadline
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("no nudge deadline armed for %s", sessionID)
	return time.Time{}
}

// fireNudgeNow simulates the countdown timer firing immediately, by invoking the fire
// callback with the live timer handle — the faithful deterministic path. Tests that
// use it set an hour-long window override so the real timer never races this
// hand-fire.
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

// armForTest gives an idle codex agent an unread chief steer, which arms its shared
// nudge countdown. Returns the agent id and inputs accessor.
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

	waitForNudgeDeadline(t, d, agentID)
}

func TestBufferedNudgePreservesDeadlineAcrossSelectionPause(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Second
	d.ticketBufferWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	chiefID, agentID, _ := delegateForNotify(t, d, "codex")
	ticketID := boundTicketID(t, d, agentID)
	if _, err := ticketnotify.ConsumeAll(d.store, d.ticketObserversForSession(chiefID), time.Now()); err != nil {
		t.Fatal(err)
	}
	attentionAt := time.Now().Add(-10 * time.Minute)
	if err := d.store.SetTicketDeliveryAttention(d.ticketAttentionKey(chiefID), attentionAt); err != nil {
		t.Fatal(err)
	}
	d.setSelectedSession(chiefID)
	commentOnTicket(t, d, ticketID, "buffer this")
	if deadline := currentNudgeDeadline(d, chiefID); !deadline.IsZero() {
		t.Fatalf("selected chief armed deadline %s", deadline)
	}

	d.setSelectedSession(agentID)
	deadline := waitForNudgeDeadline(t, d, chiefID)
	want := attentionAt.Add(time.Hour)
	// Store timestamps use RFC3339 second precision, so the durable round-trip
	// may trim the sub-second component.
	if delta := deadline.Sub(want); delta < -time.Second || delta > time.Second {
		t.Fatalf("resumed deadline = %s, want %s", deadline, want)
	}
}

func TestRebuildTicketDeliverySchedulesRearmsUnread(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	_, agentID, _ := delegateForNotify(t, d, "codex")
	ticketID := boundTicketID(t, d, agentID)
	d.store.UpdateState(agentID, protocol.StateIdle)
	commentOnTicket(t, d, ticketID, "survive restart")
	d.cancelNudgeCountdown(agentID, "simulate daemon restart")
	if currentNudgeTimer(d, agentID) != nil {
		t.Fatal("countdown still armed before rebuild")
	}

	d.rebuildTicketDeliverySchedules()
	waitForNudgeDeadline(t, d, agentID)
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

// Moving to another eligible state keeps the countdown armed. This covers the hook
// path used by Codex and Claude, which does not flow through handlePTYState.
func TestNudgeCountdownSurvivesEligibleStateChange(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	agentID, _ := armForTest(t, d)
	if currentNudgeTimer(d, agentID) == nil {
		t.Fatal("precondition: no countdown armed")
	}

	d.applyState(sessionStateChange{
		sessionID: agentID,
		state:     protocol.StateWorking,
		cause:     daemonObservation{},
	})

	if currentNudgeTimer(d, agentID) == nil {
		t.Fatal("countdown was canceled when the agent became active")
	}
}

func TestNudgeCountdownCancelsOnPendingApproval(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	agentID, _ := armForTest(t, d)

	d.applyState(sessionStateChange{
		sessionID: agentID,
		state:     protocol.StatePendingApproval,
		cause:     daemonObservation{},
	})

	if currentNudgeTimer(d, agentID) != nil {
		t.Fatal("countdown survived a pending approval prompt")
	}
}

func TestDoorbellWriteDoesNotInterleaveWithPendingApproval(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := "doorbell-state-fence"
	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID:             sessionID,
		Label:          "doorbell-state-fence",
		Agent:          protocol.SessionAgentCodex,
		Directory:      t.TempDir(),
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})

	inputStarted := make(chan struct{})
	releaseInput := make(chan struct{})
	inputs := make(chan string, 1)
	d.ptyBackend = &fakeSpawnBackend{onInput: func(_ string, data []byte) {
		inputs <- string(data)
		close(inputStarted)
		<-releaseInput
	}}

	doorbellDone := make(chan error, 1)
	go func() { doorbellDone <- d.typeDoorbell(sessionID, ticketNudgePrompt) }()
	<-inputStarted

	stateDone := make(chan struct{})
	go func() {
		d.applyState(sessionStateChange{
			sessionID: sessionID,
			state:     protocol.StatePendingApproval,
			cause:     daemonObservation{},
		})
		close(stateDone)
	}()
	select {
	case <-stateDone:
		t.Fatal("pending approval committed while a doorbell input was in flight")
	case <-time.After(20 * time.Millisecond):
		// The shared fence holds the state report until the complete input is sent.
	}

	close(releaseInput)
	if err := <-doorbellDone; err != nil {
		t.Fatalf("typeDoorbell() error = %v", err)
	}
	<-stateDone
	want := bracketedPasteStart + ticketNudgePrompt + bracketedPasteEnd + "\r"
	if got := <-inputs; got != want {
		t.Fatalf("doorbell input = %q, want atomic bracketed prompt+Enter %q", got, want)
	}
}

func TestNudgeDeliveryStatePolicy(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state string
		want  bool
	}{
		{name: "active green", state: protocol.StateWorking, want: true},
		{name: "new initial", state: protocol.StateLaunching, want: true},
		{name: "unknown", state: protocol.StateUnknown, want: true},
		{name: "waiting for input", state: protocol.StateWaitingInput, want: true},
		{name: "flashing approval", state: protocol.StatePendingApproval, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isNudgeDeliveryAllowed(tc.state); got != tc.want {
				t.Fatalf("isNudgeDeliveryAllowed(%q) = %v, want %v", tc.state, got, tc.want)
			}
		})
	}
}

// Draining the inbox clears the indicator and cancels the countdown — including when
// an optional runtime watch is the consumer. After draining, nothing is unread, so
// there is nothing to nudge.
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
	if _, found, err := d.store.TicketDeliveryAttention(d.ticketAttentionKey(agentID)); err != nil || !found {
		t.Fatalf("trigger_nudge did not record attention: found=%v err=%v", found, err)
	}
}

// An explicit click delivers when the session rests in 'unknown' — Codex's common
// resting state when its turn-end classifier cannot find a transcript. Unknown is
// also auto-nudge-eligible; the click simply bypasses the countdown.
func TestTriggerNudgeDeliversWhenUnknown(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	agentID, inputs := armForTest(t, d)
	d.store.UpdateState(agentID, protocol.StateUnknown)

	d.handleTriggerNudge(&protocol.TriggerNudgeMessage{
		Cmd:       protocol.CmdTriggerNudge,
		SessionID: agentID,
	})

	if !wasNudged(inputs(agentID)) {
		t.Fatal("trigger_nudge did not doorbell an at-rest unknown session")
	}
}

// An explicit click delivers on demand while the agent is working. Working is also
// auto-nudge-eligible; the click simply bypasses the countdown.
func TestTriggerNudgeDeliversWhileWorking(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	agentID, inputs := armForTest(t, d)
	d.store.UpdateState(agentID, protocol.StateWorking)

	d.handleTriggerNudge(&protocol.TriggerNudgeMessage{
		Cmd:       protocol.CmdTriggerNudge,
		SessionID: agentID,
	})

	if !wasNudged(inputs(agentID)) {
		t.Fatal("trigger_nudge did not deliver on demand into a working session")
	}
}

// The one exception: a click never doorbells a pending_approval session — the
// doorbell's trailing Enter could answer the approval prompt.
func TestTriggerNudgeSkipsPendingApproval(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	agentID, inputs := armForTest(t, d)
	d.store.UpdateState(agentID, protocol.StatePendingApproval)

	d.handleTriggerNudge(&protocol.TriggerNudgeMessage{
		Cmd:       protocol.CmdTriggerNudge,
		SessionID: agentID,
	})

	if wasNudged(inputs(agentID)) {
		t.Fatal("trigger_nudge typed a doorbell into an approval prompt")
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

// The guard's write path: handlePtyInput must record a genuine keystroke and must NOT
// record automation/replay writes. The other guard tests poke noteUserInput directly;
// this one drives the real pty_input handler so the source derivation
// (Deref + TrimSpace) and the call wiring are covered, not assumed. If genuine input
// stopped recording the splice guard becomes a no-op; if attach_replay started
// recording every switched-away session would look "recently typed" and never fire.
func TestHandlePtyInputRecordsKeystrokeForGuard(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	cases := []struct {
		name   string
		source *string
		want   bool
	}{
		{"untagged genuine keystroke", nil, true},
		{"explicit empty source", protocol.Ptr(""), true},
		{"automation write", protocol.Ptr("automation"), false},
		{"attach replay write", protocol.Ptr("attach_replay"), false},
		{"padded automation is trimmed then ignored", protocol.Ptr("  automation  "), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sessionID := "sess-" + tc.name
			d.handlePtyInput(nil, &protocol.PtyInputMessage{
				ID:     sessionID,
				Data:   "x",
				Source: tc.source,
			})
			if got := d.recentUserInput(sessionID, time.Hour); got != tc.want {
				t.Fatalf("recentUserInput after handlePtyInput(source=%v) = %v, want %v",
					protocol.Deref(tc.source), got, tc.want)
			}
		})
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
