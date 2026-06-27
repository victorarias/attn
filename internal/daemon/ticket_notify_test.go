package daemon

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

// delegateForNotify runs a real delegation with the given agent and returns the
// chief + agent session ids plus an accessor for the inputs typed into a session's
// PTY. The doorbell lands here; the brief goes via the spawn prompt file (not
// Input), so any recorded input after delegation is a nudge.
func delegateForNotify(t *testing.T, d *Daemon, agent string) (chiefID, agentID string, inputs func(string) []string) {
	t.Helper()
	backend := &fakeSpawnBackend{}
	var mu sync.Mutex
	rec := map[string][]string{}
	backend.onInput = func(id string, data []byte) {
		mu.Lock()
		rec[id] = append(rec[id], string(data))
		mu.Unlock()
	}
	_, chiefID, _ = setupDelegationSource(t, d, backend)
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, chiefID); err != nil {
		t.Fatalf("set chief role: %v", err)
	}
	consumeDelegatedPrompt(t, backend)
	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: chiefID,
		Brief:           "Migrate the store to X",
		Agent:           protocol.Ptr(agent),
	})
	if err != nil {
		t.Fatalf("delegate(): %v", err)
	}
	inputs = func(id string) []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), rec[id]...)
	}
	return chiefID, result.SessionID, inputs
}

func wasNudged(inputs []string) bool {
	for _, in := range inputs {
		if strings.Contains(in, ticketNudgePrompt) {
			return true
		}
	}
	return false
}

// An idle agent that can't self-monitor (codex) gets the fixed doorbell when an
// event it did not author is unread (here its own assignment, authored by the
// chief). The chief, which authored that event, is never nudged about it.
func TestNotifyNudgesIdleCodexObserver(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	chiefID, agentID, inputs := delegateForNotify(t, d, "codex")
	ticketID := boundTicketID(t, d, agentID)
	d.store.UpdateState(agentID, protocol.StateIdle)

	d.notifyTicketObservers(ticketID)

	if !wasNudged(inputs(agentID)) {
		t.Fatal("idle codex agent was not nudged about its unread event")
	}
	if wasNudged(inputs(chiefID)) {
		t.Fatal("chief was nudged about an event it authored")
	}
}

// A self-monitoring agent (claude) is never typed into — its own watch drains the
// queue (DeliveryWatch), so the doorbell would be a redundant interruption.
func TestNotifyDoesNotNudgeClaudeObserver(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	_, agentID, inputs := delegateForNotify(t, d, "claude")
	ticketID := boundTicketID(t, d, agentID)
	d.store.UpdateState(agentID, protocol.StateIdle)

	d.notifyTicketObservers(ticketID)

	if wasNudged(inputs(agentID)) {
		t.Fatal("self-monitoring claude agent should not be typed into")
	}
}

// A busy codex agent is deferred — no doorbell mid-task — then gets it the moment
// it goes idle, which is what notifyTicketSessionWentIdle flushes.
func TestNotifyDefersBusyCodexThenFlushesOnIdle(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	_, agentID, inputs := delegateForNotify(t, d, "codex")
	ticketID := boundTicketID(t, d, agentID)
	d.store.UpdateState(agentID, protocol.StateWorking)

	d.notifyTicketObservers(ticketID)
	if wasNudged(inputs(agentID)) {
		t.Fatal("busy codex agent was nudged mid-task")
	}

	d.store.UpdateState(agentID, protocol.StateIdle)
	d.notifyTicketSessionWentIdle(agentID)
	if !wasNudged(inputs(agentID)) {
		t.Fatal("deferred nudge was not flushed when the agent went idle")
	}
}
