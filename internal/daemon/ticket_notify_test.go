package daemon

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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

func setSessionAgent(t *testing.T, d *Daemon, sessionID string, agent protocol.SessionAgent) {
	t.Helper()
	s := d.store.Get(sessionID)
	if s == nil {
		t.Fatalf("setSessionAgent: session %s not found", sessionID)
	}
	s.Agent = agent
	d.store.Add(s)
}

// delegateMany sets up ONE chief of staff and delegates an agent per brief from it,
// modeling a chief that fanned work out to a batch of siblings. It returns the chief
// id, the spawned agent ids (brief order), and an accessor for inputs typed into a
// session's PTY. Like delegateForNotify, the brief is delivered via the spawn prompt
// file — never Input — so any recorded input is a nudge.
func delegateMany(t *testing.T, d *Daemon, agent string, briefs ...string) (chiefID string, agentIDs []string, inputs func(string) []string) {
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
	for i, brief := range briefs {
		// Distinct labels: same-workspace siblings can't share an auto-derived name.
		result, err := d.delegate(&protocol.DelegateMessage{
			Cmd:             protocol.CmdDelegate,
			SourceSessionID: chiefID,
			Brief:           brief,
			Agent:           protocol.Ptr(agent),
			Label:           protocol.Ptr(fmt.Sprintf("delegate-%d", i)),
		})
		if err != nil {
			t.Fatalf("delegate(%d, %q): %v", i, brief, err)
		}
		agentIDs = append(agentIDs, result.SessionID)
	}
	inputs = func(id string) []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), rec[id]...)
	}
	return chiefID, agentIDs, inputs
}

func wasNudged(inputs []string) bool {
	for _, in := range inputs {
		if strings.Contains(in, ticketNudgePrompt) {
			return true
		}
	}
	return false
}

// Every runtime receives the same nudge for an eligible delegated leaf: active
// (green), launching (new), and unknown states all arm the visible countdown.
func TestNotifyNudgesEligibleLeavesAcrossRuntimes(t *testing.T) {
	states := []struct {
		name  string
		state protocol.SessionState
	}{
		{name: "active green", state: protocol.SessionStateWorking},
		{name: "new initial", state: protocol.SessionStateLaunching},
		{name: "unknown", state: protocol.SessionStateUnknown},
	}
	for _, runtime := range []string{"codex", "claude"} {
		for _, tc := range states {
			t.Run(runtime+"/"+tc.name, func(t *testing.T) {
				d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
				d.nudgeWindowOverride = time.Hour
				t.Cleanup(d.stopNudgeCountdowns)
				_, agentID, inputs := delegateForNotify(t, d, runtime)
				ticketID := boundTicketID(t, d, agentID)
				d.store.UpdateState(agentID, string(tc.state))

				commentOnTicket(t, d, ticketID, "take a look at the failing test")
				fireNudgeNow(t, d, agentID)
				if !wasNudged(inputs(agentID)) {
					t.Fatalf("%s delegated leaf was not nudged", runtime)
				}
			})
		}
	}
}

// Full slice-6 roundtrip: a real chief producer (the human commenting on the
// agent's bound ticket via handleTicketAddComment) drives notifyTicketObservers,
// which nudges the codex agent through the same shared delivery policy as every
// other runtime.
// The agent then runs `attn ticket inbox`, consumes the chief's event, and a
// second inbox is empty because the cursor advanced — proving it consumed, not
// peeked. No real codex binary or PTY: the fake spawn backend captures the doorbell.
func TestCodexNudgeRoundtrip(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
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

	// 1) The idle codex agent was nudged by the chief's comment on its ticket (after
	// the countdown the comment armed fires).
	fireNudgeNow(t, d, agentID)
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

// Approval prompts are the sole deferral state. Once the prompt clears, unread
// activity is rechecked and armed even when the agent returns to active/green.
func TestNotifyDefersPendingApprovalThenFlushesOnWorking(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	_, agentID, inputs := delegateForNotify(t, d, "codex")
	ticketID := boundTicketID(t, d, agentID)
	d.store.UpdateState(agentID, protocol.StatePendingApproval)

	commentOnTicket(t, d, ticketID, "take a look")
	if wasNudged(inputs(agentID)) {
		t.Fatal("approval-waiting codex agent was nudged")
	}
	if currentNudgeTimer(d, agentID) != nil {
		t.Fatal("approval-waiting codex agent armed a countdown")
	}

	d.applyState(sessionStateChange{
		sessionID: agentID,
		state:     protocol.StateWorking,
		cause:     daemonObservation{},
	})
	deadline := time.Now().Add(time.Second)
	for currentNudgeTimer(d, agentID) == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	fireNudgeNow(t, d, agentID)
	if !wasNudged(inputs(agentID)) {
		t.Fatal("deferred nudge was not flushed when approval cleared")
	}
}

// A chief that fans work out to siblings must not cross-wire their doorbells: when
// one delegate reports a status change, only that ticket's participants (the agent
// and the chief) are notified — the OTHER delegates are neither assignee nor author
// on it, so the event never routes to them. This locks the store-level isolation
// that makes "agent A is nudged about ticket C" impossible by construction.
func TestDelegatedSiblingsNotNudgedByEachOther(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	_, agents, inputs := delegateMany(t, d, "codex", "Task A", "Task B", "Task C")
	a, b, c := agents[0], agents[1], agents[2]
	for _, id := range agents {
		d.store.UpdateState(id, protocol.StateIdle)
	}

	// Delegate C reports done — an event on ticket C only.
	callSetTicketStatus(t, d, c, string(protocol.DispatchWorkStateCompleted), "done")

	if wasNudged(inputs(a)) {
		t.Fatal("sibling A was nudged by C's status change (cross-ticket leak)")
	}
	if wasNudged(inputs(b)) {
		t.Fatal("sibling B was nudged by C's status change (cross-ticket leak)")
	}
}

// The real symptom behind "everyone gets nudged": a delegated agent already has its
// brief (delivered via the spawn prompt), but the chief-authored `created` event
// stays unread on the agent's OWN ticket because nothing advances its cursor at
// delegation. So the moment the agent goes idle, the went-idle flush doorbells it
// about a brief it already holds. Batch delegation makes the siblings settle around
// the same time, which reads as "C finishing nudged the whole batch" — but each is
// only ever self-nudging about its own ticket. The fix marks the brief consumed for
// the assignee at creation, so nothing is unread and no doorbell fires.
func TestDelegatedAgentNotNudgedByOwnDeliveredBrief(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	_, agents, inputs := delegateMany(t, d, "codex", "Task A")
	a := agents[0]
	d.store.UpdateState(a, protocol.StateIdle)

	// The agent settles after its initial run; the went-idle path re-runs the notify
	// decision for it. With the brief consumed at delegation, there is nothing unread.
	d.notifyTicketSession(a, time.Now())

	if wasNudged(inputs(a)) {
		t.Fatal("delegated agent was doorbelled about its own already-delivered brief")
	}
}

// commentOnTicket gives the delegated agent an unread event: the chief/human
// comments on the agent's bound ticket (authored as "you", store.TicketAuthorYou),
// an event the agent did not author. This is the real chief→agent steer path
// (handleTicketAddComment), the same one TestCodexNudgeRoundtrip drives.
func commentOnTicket(t *testing.T, d *Daemon, ticketID, comment string) {
	t.Helper()
	client := &wsClient{send: make(chan outboundMessage, 8)}
	d.handleTicketAddComment(client, &protocol.TicketAddCommentMessage{
		Cmd:      protocol.CmdTicketAddComment,
		TicketID: ticketID,
		Comment:  comment,
	})
}

// The shared policy also covers chiefs. A report from a delegated agent wakes an
// active chief regardless of whether the chief runs Codex or Claude.
func TestTicketNudgesActiveChiefAcrossRuntimes(t *testing.T) {
	for _, runtime := range []protocol.SessionAgent{protocol.SessionAgentCodex, protocol.SessionAgentClaude} {
		t.Run(string(runtime), func(t *testing.T) {
			d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
			d.nudgeWindowOverride = time.Hour
			t.Cleanup(d.stopNudgeCountdowns)
			chiefID, agentID, inputs := delegateForNotify(t, d, "codex")
			setSessionAgent(t, d, chiefID, runtime)
			d.store.UpdateState(chiefID, protocol.StateWorking)
			d.setSelectedSession(agentID) // preserve the focused-session anti-splice pause

			callSetTicketStatus(t, d, agentID, string(protocol.DispatchWorkStateReadyForReview), "done, please review")
			deadline := time.Now().Add(time.Second)
			for currentNudgeTimer(d, chiefID) == nil && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			fireNudgeNow(t, d, chiefID)
			if !wasNudged(inputs(chiefID)) {
				t.Fatalf("active %s chief was not nudged", runtime)
			}
			if wasNudged(inputs(agentID)) {
				t.Fatal("the reporting agent was nudged about its own status change")
			}
		})
	}
}

// Chief ticket awareness belongs to the role, not the session that happened to
// delegate. A consumes one report, the role transfers to B, and the next report
// reaches only B. The role cursor means B receives exactly the post-transfer
// unread event: nothing A consumed is replayed and nothing new is skipped.
func TestChiefTicketContinuityAcrossRoleTransfer(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	chiefA, agentID, inputs := delegateForNotify(t, d, "codex")
	ticketID := boundTicketID(t, d, agentID)

	now := string(protocol.TimestampNow())
	chiefB := "chief-b"
	d.store.Add(&protocol.Session{
		ID: chiefB, Label: "replacement chief", Agent: protocol.SessionAgentCodex,
		Directory: "/tmp/chief-b", WorkspaceID: "workspace-chief-b",
		State: protocol.SessionStateIdle, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	d.store.UpdateState(chiefA, protocol.StateIdle)
	d.store.UpdateState(agentID, protocol.StateIdle)

	// A consumes the first agent report, advancing the durable role cursor.
	callSetTicketStatus(t, d, agentID, string(protocol.DispatchWorkStateNeedsInput), "need a decision")
	first := callTicketInbox(t, d, chiefA)
	if len(first) != 1 || len(first[0].Events) != 1 ||
		first[0].Events[0].ToStatus == nil || *first[0].Events[0].ToStatus != protocol.TicketStatusBlocked {
		t.Fatalf("chief A first inbox = %+v, want only the blocked report", first)
	}
	nudgesA := nudgeCount(inputs(chiefA))

	// Transfer the singleton profile role. No cursor copy occurs; only delivery is
	// retargeted and A's stale role nudge state is cleared.
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, chiefB); err != nil {
		t.Fatalf("transfer chief role: %v", err)
	}
	d.retargetChiefTicketDelivery(chiefA, chiefB)

	callSetTicketStatus(t, d, agentID, string(protocol.DispatchWorkStateReadyForReview), "ready now")
	deadline := time.Now().Add(time.Second)
	for currentNudgeTimer(d, chiefB) == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	fireNudgeNow(t, d, chiefB)
	if !wasNudged(inputs(chiefB)) {
		t.Fatal("replacement chief was not nudged about unread chief-owned ticket activity")
	}
	if got := nudgeCount(inputs(chiefA)); got != nudgesA {
		t.Fatalf("retired chief received a role nudge after transfer: %d -> %d", nudgesA, got)
	}

	second := callTicketInbox(t, d, chiefB)
	if len(second) != 1 || second[0].TicketID != ticketID || len(second[0].Events) != 1 {
		t.Fatalf("chief B inbox = %+v, want exactly one post-cursor event for %s", second, ticketID)
	}
	event := second[0].Events[0]
	if event.ToStatus == nil || *event.ToStatus != protocol.TicketStatusInReview || event.Author != agentID {
		t.Fatalf("chief B event = %+v, want agent's in-review report", event)
	}
	if again := callTicketInbox(t, d, chiefB); len(again) != 0 {
		t.Fatalf("chief B second inbox = %+v, want no duplicate activity", again)
	}
}

// A chief can still participate personally through the ordinary explicit
// subscription path. When personal and durable-role scopes overlap, delivery is
// deduplicated while both cursors advance.
func TestChiefRoleAndExplicitSubscriptionDeliverOnce(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	chiefID, agentID, _ := delegateForNotify(t, d, "codex")
	ticketID := boundTicketID(t, d, agentID)
	if resp := callTicketSubscribe(t, d, chiefID, ticketID); !resp.Ok {
		t.Fatalf("subscribe response = %+v", resp)
	}

	callSetTicketStatus(t, d, agentID, string(protocol.DispatchWorkStateReadyForReview), "ready")
	bundles := callTicketInbox(t, d, chiefID)
	if len(bundles) != 1 || len(bundles[0].Events) != 1 {
		t.Fatalf("overlapping role/subscriber inbox = %+v, want one event", bundles)
	}
	if again := callTicketInbox(t, d, chiefID); len(again) != 0 {
		t.Fatalf("overlapping role/subscriber second inbox = %+v, want empty", again)
	}
}

// A Claude `ticket inbox --watch` remains a valid optional consumer. It drains the
// same queue and clears the shared countdown before the doorbell fires.
func TestTicketWatchDrainClearsSharedCountdown(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	_, agentID, inputs := delegateForNotify(t, d, "claude")
	ticketID := boundTicketID(t, d, agentID)
	d.store.UpdateState(agentID, protocol.StateWorking)

	commentOnTicket(t, d, ticketID, "take a look")
	if currentNudgeTimer(d, agentID) == nil {
		t.Fatal("shared countdown was not armed for Claude")
	}
	callTicketInbox(t, d, agentID) // equivalent to the watch's consuming poll
	if currentNudgeTimer(d, agentID) != nil {
		t.Fatal("watch drain did not clear the shared countdown")
	}
	if wasNudged(inputs(agentID)) {
		t.Fatal("watch-drained queue was still doorbelled")
	}
}

// nudgeCount counts how many doorbells (ticketNudgePrompt inputs) a session got.
func nudgeCount(inputs []string) int {
	n := 0
	for _, in := range inputs {
		if strings.Contains(in, ticketNudgePrompt) {
			n++
		}
	}
	return n
}
