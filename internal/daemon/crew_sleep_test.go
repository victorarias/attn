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
