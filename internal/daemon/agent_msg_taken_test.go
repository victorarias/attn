package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// withAgentMessageTakenWindow restores confirmation for one test; TestMain
// zeroes it so the fake PTY, which never reports working, does not time out
// every delivery in the package.
func withAgentMessageTakenWindow(t *testing.T, window time.Duration) {
	t.Helper()
	previous := agentMessageTakenWindow
	agentMessageTakenWindow = window
	t.Cleanup(func() { agentMessageTakenWindow = previous })
}

// The bug this exists for: a PTY write returning proves the bytes reached the
// terminal, not that the agent read them. A session with a modal in front of
// its composer eats the paste while reporting the same settled title as one
// waiting at its prompt, and the sender used to be told "delivered".
func TestAgentMsgQueuesWhenTheTargetNeverTakesIt(t *testing.T) {
	withAgentMessageTakenWindow(t, 50*time.Millisecond)
	d, doorbell := newAgentMsgDaemon(t)
	addCharacterizationSession(t, d, "sender-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)
	addCharacterizationSession(t, d, "target-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)

	resp := callAgentMsg(t, d, "target-session-id", "sender-session-id", "did the migration land?")
	result := resp.AgentMsgResult
	if result == nil || result.Status != protocol.AgentMsgStatusQueued {
		t.Fatalf("result = %+v, want queued", result)
	}
	if !strings.Contains(result.Detail, "did not start a turn") {
		t.Fatalf("detail does not say what happened: %q", result.Detail)
	}
	// Typed once, then Enter pressed again rather than the text pasted twice: the
	// paste may be sitting in the composer, and a second paste would double it.
	if prompts := doorbell.pasted(); len(prompts) != 1 {
		t.Fatalf("pasted %d times, want 1: %q", len(prompts), prompts)
	}

	queued, err := d.store.UndeliveredAgentMessages("target-session-id")
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 1 {
		t.Fatalf("unconfirmed message is not queued for the drain: %+v", queued)
	}
}

// A message typed into a composer and never taken is still sitting in it, so
// the drain submits it rather than pasting a second copy. Repasting is how a
// target stuck behind a dialog collects one copy per state change and then
// reads the same message N times when it clears.
func TestAgentMsgRedeliveryPressesEnterRatherThanRepasting(t *testing.T) {
	withAgentMessageTakenWindow(t, 50*time.Millisecond)
	d, doorbell := newAgentMsgDaemon(t)
	addCharacterizationSession(t, d, "sender-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)
	addCharacterizationSession(t, d, "target-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)

	resp := callAgentMsg(t, d, "target-session-id", "sender-session-id", "the epic is green")
	if result := resp.AgentMsgResult; result == nil || result.Status != protocol.AgentMsgStatusQueued {
		t.Fatalf("result = %+v, want queued", result)
	}

	drained := make(chan int, 1)
	d.agentMessageDrainHook = func(_ string, delivered int) { drained <- delivered }
	go func() {
		// The drain's Enter is what lets this delivery confirm; without the state
		// change it would report not-taken again.
		<-time.After(20 * time.Millisecond)
		d.applyState(sessionStateChange{
			sessionID: "target-session-id",
			state:     protocol.StateWorking,
			cause:     liveSignal{},
		})
	}()
	if !d.applyState(sessionStateChange{
		sessionID: "target-session-id",
		state:     protocol.StateIdle,
		cause:     liveSignal{},
	}) {
		t.Fatal("applyState did not apply")
	}

	select {
	case delivered := <-drained:
		if delivered != 1 {
			t.Fatalf("drain delivered %d, want 1", delivered)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the drain never ran")
	}
	if prompts := doorbell.pasted(); len(prompts) != 1 {
		t.Fatalf("pasted %d times, want 1 — the redelivery retyped the message: %q", len(prompts), prompts)
	}
}

// The confirmed path: the target starts working, so the message is delivered
// and nothing stays queued behind it.
func TestAgentMsgDeliversWhenTheTargetStartsWorking(t *testing.T) {
	withAgentMessageTakenWindow(t, 2*time.Second)
	d, _ := newAgentMsgDaemon(t)
	addCharacterizationSession(t, d, "sender-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)
	addCharacterizationSession(t, d, "target-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)

	typed := make(chan struct{})
	d.ptyBackend = &fakeSpawnBackend{onInput: func(sessionID string, _ []byte) {
		if sessionID == "target-session-id" {
			select {
			case <-typed:
			default:
				close(typed)
			}
		}
	}}
	go func() {
		<-typed
		d.applyState(sessionStateChange{
			sessionID: "target-session-id",
			state:     protocol.StateWorking,
			cause:     liveSignal{},
		})
	}()

	resp := callAgentMsg(t, d, "target-session-id", "sender-session-id", "rebase when you surface")
	result := resp.AgentMsgResult
	if result == nil || result.Status != protocol.AgentMsgStatusDelivered {
		t.Fatalf("result = %+v, want delivered", result)
	}
	queued, err := d.store.UndeliveredAgentMessages("target-session-id")
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 0 {
		t.Fatalf("a confirmed delivery is still queued: %+v", queued)
	}
}

// A target already mid-turn queues the paste in its composer and answers when
// the turn ends — no new turn opens, so there is nothing to confirm. Waiting
// for one would report queued and redeliver, doubling the message.
func TestAgentMsgToAWorkingTargetDoesNotWaitForConfirmation(t *testing.T) {
	withAgentMessageTakenWindow(t, time.Hour)
	d, doorbell := newAgentMsgDaemon(t)
	addCharacterizationSession(t, d, "sender-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)
	addCharacterizationSession(t, d, "target-session-id", protocol.SessionAgentClaude, protocol.SessionStateWorking)

	done := make(chan protocol.Response, 1)
	go func() { done <- callAgentMsg(t, d, "target-session-id", "sender-session-id", "when you land, ping me") }()

	select {
	case resp := <-done:
		result := resp.AgentMsgResult
		if result == nil || result.Status != protocol.AgentMsgStatusDelivered {
			t.Fatalf("result = %+v, want delivered", result)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a message to a working target waited for a turn that cannot open")
	}
	if prompts := doorbell.pasted(); len(prompts) != 1 {
		t.Fatalf("pasted %d times, want 1: %q", len(prompts), prompts)
	}
}
