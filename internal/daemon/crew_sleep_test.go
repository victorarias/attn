package daemon

import (
	"bytes"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func crewSleepCall(t *testing.T, d *Daemon, member string) protocol.Response {
	t.Helper()
	return gardenCall(t, func(c net.Conn) {
		d.handleCrewSleep(c, &protocol.CrewSleepMessage{Cmd: protocol.CmdCrewSleep, Member: member})
	})
}

// The user-side verb asks rather than kills: the request reaches the member's
// composer through the durable doorbell and names the only closure that keeps
// the promise that nobody wakes behind it.
func TestCrewSleep_DeliversAUserRequestForSleep(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("trellis", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	d.runPostInitialPrompt(woken.SessionID, protocol.StateWorking)
	oldWindow := agentMessageTakenWindow
	agentMessageTakenWindow = 0
	t.Cleanup(func() { agentMessageTakenWindow = oldWindow })

	var mu sync.Mutex
	var typed bytes.Buffer
	backend.onInput = func(id string, data []byte) {
		if id != woken.SessionID {
			t.Errorf("typed into %q, want %q", id, woken.SessionID)
		}
		mu.Lock()
		defer mu.Unlock()
		typed.Write(data)
	}

	resp := crewSleepCall(t, d, "trellis")
	if !resp.Ok {
		t.Fatalf("crew sleep: %v", protocol.Deref(resp.Error))
	}
	result := resp.CrewSleepResult
	if result == nil || result.AlreadyAsleep || protocol.Deref(result.DeliveryStatus) != protocol.AgentMsgStatusDelivered {
		t.Fatalf("sleep result = %+v, want delivered request", result)
	}
	mu.Lock()
	text := typed.String()
	mu.Unlock()
	for _, want := range []string{"Victor is asking you", "attn handoff --sleep", "nobody wakes behind you"} {
		if !strings.Contains(text, want) {
			t.Errorf("composer input is missing %q: %q", want, text)
		}
	}
	if strings.Contains(text, "another agent, not from your user") {
		t.Fatalf("user request masqueraded as an agent message: %q", text)
	}
	if queued, err := d.store.UndeliveredAgentMessages(woken.SessionID); err != nil || len(queued) != 0 {
		t.Fatalf("queued messages = %v, %v; delivered request must be stamped", queued, err)
	}
	if d.store.Get(woken.SessionID) == nil {
		t.Fatal("sleep request killed the member")
	}
}

func TestCrewSleep_AlreadyAsleepIsANamedNoOp(t *testing.T) {
	d, _, _ := newWakeableDaemon(t)
	resp := crewSleepCall(t, d, "alder")
	if !resp.Ok {
		t.Fatalf("crew sleep: %v", protocol.Deref(resp.Error))
	}
	result := resp.CrewSleepResult
	if result == nil || !result.AlreadyAsleep || !strings.Contains(result.Detail, "already asleep") || !strings.Contains(result.Detail, "no sleep request was sent") {
		t.Fatalf("sleep result = %+v, want named no-op", result)
	}
}

// The roster shows a member as awake as soon as wake claims its binding, before
// the agent has crossed priming and its trust dialog. A sleep click in that
// window must wait behind the greeting instead of pasting into startup and
// claiming delivery for words the member never read.
func TestCrewSleep_QueuesUntilAWakingMemberTakesItsFirstPrompt(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("trellis", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	if !d.initialPromptPending(woken.SessionID) {
		t.Fatal("ordinary wake did not gate messages behind its first prompt")
	}

	oldWindow := agentMessageTakenWindow
	agentMessageTakenWindow = 0
	t.Cleanup(func() { agentMessageTakenWindow = oldWindow })
	var mu sync.Mutex
	var typed bytes.Buffer
	backend.onInput = func(id string, data []byte) {
		if id != woken.SessionID {
			t.Errorf("typed into %q, want %q", id, woken.SessionID)
		}
		mu.Lock()
		defer mu.Unlock()
		typed.Write(data)
	}

	result, err := d.crewSleep("trellis")
	if err != nil {
		t.Fatalf("sleep: %v", err)
	}
	if protocol.Deref(result.DeliveryStatus) != protocol.AgentMsgStatusQueued ||
		!strings.Contains(result.Detail, "still reading its priming") {
		t.Fatalf("sleep result = %+v, want queued behind first prompt", result)
	}
	mu.Lock()
	beforePrompt := typed.String()
	mu.Unlock()
	if beforePrompt != "" {
		t.Fatalf("sleep request reached startup before the first prompt: %q", beforePrompt)
	}

	drained := make(chan int, 1)
	d.agentMessageDrainHook = func(sessionID string, delivered int) {
		if sessionID == woken.SessionID {
			drained <- delivered
		}
	}
	d.runPostInitialPrompt(woken.SessionID, protocol.StateWorking)
	if delivered := <-drained; delivered != 1 {
		t.Fatalf("first-prompt drain delivered %d messages, want 1", delivered)
	}
	mu.Lock()
	afterPrompt := typed.String()
	mu.Unlock()
	if !strings.Contains(afterPrompt, "attn handoff --sleep") {
		t.Fatalf("sleep request did not land after the greeting: %q", afterPrompt)
	}
	if queued, err := d.store.UndeliveredAgentMessages(woken.SessionID); err != nil || len(queued) != 0 {
		t.Fatalf("queued messages after first prompt = %v, %v", queued, err)
	}
}
