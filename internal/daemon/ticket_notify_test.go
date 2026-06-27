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

// Full slice-6 roundtrip: a real chief producer (the human commenting on the
// agent's bound ticket via handleTicketAddComment) drives notifyTicketObservers,
// which nudges the idle codex agent (DeliveryNudge, because the codex driver's
// HasSelfMonitor capability is false, resolved through ticketObserverForSession).
// The agent then runs `attn ticket inbox`, consumes the chief's event, and a
// second inbox is empty because the cursor advanced — proving it consumed, not
// peeked. No real codex binary or PTY: the fake spawn backend captures the doorbell.
func TestCodexNudgeRoundtrip(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	_, agentID, inputs := delegateForNotify(t, d, "codex")
	ticketID := boundTicketID(t, d, agentID)
	d.store.UpdateState(agentID, protocol.StateIdle)

	// Drive a REAL chief→agent producer: the human comments on the codex agent's
	// ticket, authored as "you" — an event the agent did not author, so it is unread.
	client := &wsClient{send: make(chan outboundMessage, 8)}
	d.handleTicketAddComment(client, &protocol.TicketAddCommentMessage{
		Cmd:      protocol.CmdTicketAddComment,
		TicketID: ticketID,
		Comment:  "please take a look at the failing test",
	})

	// 1) The idle codex agent was nudged by the chief's comment on its ticket.
	if !wasNudged(inputs(agentID)) {
		t.Fatal("idle codex agent was not nudged on chief ticket comment")
	}

	// 2) Consume side: the agent's inbox carries the chief's comment on its ticket.
	bundles := callTicketInbox(t, d, agentID)
	if len(bundles) == 0 {
		t.Fatal("codex inbox returned no bundles after nudge")
	}
	found := false
	for _, b := range bundles {
		if b.TicketID == ticketID && len(b.Events) > 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("inbox missing chief event on ticket %s: %+v", ticketID, bundles)
	}

	// 3) Cursor advanced: a second consume is empty (consumed, not peeked).
	if again := callTicketInbox(t, d, agentID); len(again) != 0 {
		t.Fatalf("second inbox not empty, cursor did not advance: %+v", again)
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
