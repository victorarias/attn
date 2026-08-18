package daemon

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
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
// agent's bound ticket via handleTicketComment) drives notifyTicketObservers,
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
	commentOnTicket(t, d, ticketID, "please take a look at the failing test")

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
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
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
			cause:     resolverObservation{},
		})
		// The countdown the cleared approval arms, run at its production length
		// rather than hand-fired: what the user gets is a doorbell one window
		// after the prompt clears, and that is what is asserted.
		settledNudgeDeadline(t, d, agentID)
		time.Sleep(defaultNudgeCountdownWindow)
		synctest.Wait()
		if !wasNudged(inputs(agentID)) {
			t.Fatal("deferred nudge was not flushed when approval cleared")
		}
	})
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
	d.notifyUnreadTicketSession(a, time.Now())

	if wasNudged(inputs(a)) {
		t.Fatal("delegated agent was doorbelled about its own already-delivered brief")
	}
}

// commentOnTicket gives the delegated agent an unread event: the chief/human
// comments on the agent's bound ticket (authored as "you", store.TicketAuthorYou),
// an event the agent did not author. This is the real chief→agent steer path
// (handleTicketComment), the same one TestCodexNudgeRoundtrip drives.
func commentOnTicket(t *testing.T, d *Daemon, ticketID, comment string) {
	t.Helper()
	if resp := callTicketComment(t, d, store.TicketAuthorYou, ticketID, comment); !resp.Ok {
		t.Fatalf("comment on %s: %v", ticketID, protocol.Deref(resp.Error))
	}
}

// The shared policy also covers chiefs. A report from a delegated agent wakes an
// active chief regardless of whether the chief runs Codex or Claude.
func TestTicketNudgesActiveChiefAcrossRuntimes(t *testing.T) {
	for _, runtime := range []protocol.SessionAgent{protocol.SessionAgentCodex, protocol.SessionAgentClaude} {
		t.Run(string(runtime), func(t *testing.T) {
			d := newBubbleDaemon(t)
			synctest.Test(t, func(t *testing.T) {
				stopDaemonBackground(t, d)
				chiefID, agentID, inputs := delegateForNotify(t, d, "codex")
				setSessionAgent(t, d, chiefID, runtime)
				d.store.UpdateState(chiefID, protocol.StateWorking)
				d.setSelectedSession(agentID) // preserve the focused-session anti-splice pause

				callSetTicketStatus(t, d, agentID, string(protocol.DispatchWorkStateReadyForReview), "done, please review")
				// The chief's own countdown, run out at whatever length the policy
				// picked for it instead of hand-fired.
				time.Sleep(time.Until(settledNudgeDeadline(t, d, chiefID)) + time.Second)
				synctest.Wait()
				if !wasNudged(inputs(chiefID)) {
					t.Fatalf("active %s chief was not nudged", runtime)
				}
				if wasNudged(inputs(agentID)) {
					t.Fatal("the reporting agent was nudged about its own status change")
				}
			})
		})
	}
}

// Chief ticket awareness belongs to the role, not the session that happened to
// delegate. A consumes one report, the role transfers to B, and the next report
// reaches only B. The role cursor means B receives exactly the post-transfer
// unread event: nothing A consumed is replayed and nothing new is skipped.
//
// The windows run out for real here rather than being parked and hand-fired,
// which is what makes A's silence mean something: every countdown the daemon
// could have armed for A fires well inside the sleep, so zero nudges is A having
// no attachment left, not a timer that had not come due yet.
func TestChiefTicketContinuityAcrossRoleTransfer(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
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
		if deadline := currentNudgeDeadline(d, chiefA); !deadline.IsZero() {
			t.Fatalf("retired chief still has a countdown armed for %s", deadline)
		}
		// Past the longest window any participant of this ticket could have been given.
		time.Sleep(2 * d.ticketBundleWindow())
		synctest.Wait()
		if !wasNudged(inputs(chiefB)) {
			t.Fatal("replacement chief was not nudged about unread chief-owned ticket activity")
		}
		if got := nudgeCount(inputs(chiefA)); got != nudgesA {
			t.Fatalf("retired chief was nudged about a ticket it delegated as chief: %d -> %d", nudgesA, got)
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
	})
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

// A direct `ticket inbox --watch` caller drains the same queue and clears the
// shared countdown before the doorbell fires.
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
	watch := protocol.TicketInboxModeWatch
	callTicketInboxMode(t, d, agentID, &watch)
	if currentNudgeTimer(d, agentID) != nil {
		t.Fatal("watch drain did not clear the shared countdown")
	}
	if wasNudged(inputs(agentID)) {
		t.Fatal("watch-drained queue was still doorbelled")
	}
}

func TestTicketActivityWakesSleepingMemberAndNudgesAfterPriming(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	doorbell := &recordingDoorbell{}
	backend.onInput = doorbell.backend().onInput
	var initialPrompt string
	backend.onSpawn = func(opts ptybackend.SpawnOptions) {
		body, err := os.ReadFile(opts.InitialPromptFile)
		if err != nil {
			t.Fatalf("read initial prompt: %v", err)
		}
		initialPrompt = string(body)
	}

	identity := store.TicketMemberIdentity("trellis")
	now := time.Now()
	if _, err := d.store.CreateTicket(store.Ticket{ID: "sleeping-thread", Title: "Sleeping thread"}, "you", now); err != nil {
		t.Fatal(err)
	}
	if err := d.store.AddTicketSubscription(identity, "sleeping-thread", now); err != nil {
		t.Fatal(err)
	}
	if _, err := d.store.AddTicketComment("sleeping-thread", "you", "new activity", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	d.notifyTicketObservers("sleeping-thread")

	member := memberByID(t, crewList(t, d), "trellis")
	sessionID := protocol.Deref(member.BindingSession)
	if sessionID == "" {
		t.Fatal("ticket activity did not wake Trellis")
	}
	if initialPrompt != crewWakePrompt {
		t.Fatalf("wake initial prompt = %q, want the ordinary post-priming greeting", initialPrompt)
	}
	if prompts := doorbell.pasted(); len(prompts) != 0 {
		t.Fatalf("ticket nudge landed before priming completed: %q", prompts)
	}
	d.handleTriggerNudge(&protocol.TriggerNudgeMessage{SessionID: sessionID})
	if prompts := doorbell.pasted(); len(prompts) != 0 {
		t.Fatalf("manual nudge spliced into priming: %q", prompts)
	}
	decorated := d.sessionForBroadcast(d.store.Get(sessionID))
	if decorated == nil || !protocol.Deref(decorated.TicketUnread) {
		t.Fatalf("woken member session = %+v, want unread indicator", decorated)
	}

	hook := callHandler(t, func(conn net.Conn) {
		d.handleState(conn, &protocol.StateMessage{ID: sessionID, State: protocol.StateWorking})
	})
	if !hook.Ok {
		t.Fatalf("prompt-submit hook = %+v", hook)
	}
	if currentNudgeTimer(d, sessionID) == nil {
		t.Fatal("prompt-submit receipt did not arm the ticket nudge")
	}
	fireNudgeNow(t, d, sessionID)
	if !wasNudged(doorbell.pasted()) {
		t.Fatalf("woken member was not nudged after priming: %q", doorbell.pasted())
	}
}

func TestTicketWakeLimitRefusalIsVisibleAndLeavesMemberUnread(t *testing.T) {
	d, backend, logs := newWakeableDaemon(t)
	d.store.SetSetting(SettingCrewWakeLimit, "0")
	identity := store.TicketMemberIdentity("alder")
	now := time.Now()
	if _, err := d.store.CreateTicket(store.Ticket{ID: "refused-thread", Title: "Refused thread"}, "you", now); err != nil {
		t.Fatal(err)
	}
	if err := d.store.AddTicketSubscription(identity, "refused-thread", now); err != nil {
		t.Fatal(err)
	}
	if _, err := d.store.AddTicketComment("refused-thread", "you", "wake up", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	d.notifyTicketObservers("refused-thread")

	backend.mu.Lock()
	spawned := len(backend.spawnOpts)
	backend.mu.Unlock()
	if spawned != 0 {
		t.Fatalf("wake-limit refusal spawned %d sessions", spawned)
	}
	events, err := d.store.UnreadTicketEventsFor(identity, identity)
	if err != nil || len(events) == 0 {
		t.Fatalf("member unread after refusal = %+v, %v", events, err)
	}
	notifications, err := d.store.ListNotifications()
	if err != nil || len(notifications) != 1 {
		t.Fatalf("refusal notifications = %+v, %v", notifications, err)
	}
	note := notifications[0]
	if note.Kind != notificationKindCrewTicketWakeRefused || note.SourceID != "refused-thread" ||
		!strings.Contains(note.Detail, "crew.wake_limit=0") || !strings.Contains(note.Body, "still unread") {
		t.Fatalf("refusal notification = %+v", note)
	}
	if log := logs(); !strings.Contains(log, "activity remains unread") {
		t.Fatalf("refusal was not logged loudly:\n%s", log)
	}
}

func TestCrewTicketWakeGateRebuildsFromDurableUnreadState(t *testing.T) {
	d := newCrewDaemon(t)
	addSession(t, d, "woken-day")
	d.store.UpdateState("woken-day", protocol.StateWorking)
	if _, err := d.claimCrewBinding("trellis", "woken-day"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	identity := store.TicketMemberIdentity("trellis")
	if _, err := d.store.CreateTicket(store.Ticket{ID: "restart-thread", Title: "Restart thread"}, "you", now); err != nil {
		t.Fatal(err)
	}
	if err := d.store.AddTicketSubscription(identity, "restart-thread", now); err != nil {
		t.Fatal(err)
	}

	if err := d.seedCrewTicketWakeDeliveries(); err != nil {
		t.Fatal(err)
	}
	if !d.initialPromptPending("woken-day") {
		t.Fatal("restart did not restore the initial-prompt gate")
	}
	if timer := currentNudgeTimer(d, "woken-day"); timer != nil {
		t.Fatal("restart armed a nudge before the prompt receipt")
	}
	d.runPostInitialPrompt("woken-day", protocol.StateWorking)
	if d.initialPromptPending("woken-day") {
		t.Fatal("prompt receipt did not clear the restored gate")
	}
	if timer := currentNudgeTimer(d, "woken-day"); timer == nil {
		t.Fatal("prompt receipt did not arm the waiting ticket nudge")
	}
}

func TestCrewTicketRestartNudgesAnAlreadySettledDay(t *testing.T) {
	d := newCrewDaemon(t)
	addSession(t, d, "settled-day")
	d.store.UpdateState("settled-day", protocol.StateIdle)
	if _, err := d.claimCrewBinding("trellis", "settled-day"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.stopNudgeCountdowns)
	now := time.Now()
	identity := store.TicketMemberIdentity("trellis")
	if _, err := d.store.CreateTicket(store.Ticket{ID: "settled-thread", Title: "Settled thread"}, "you", now); err != nil {
		t.Fatal(err)
	}
	if err := d.store.AddTicketSubscription(identity, "settled-thread", now); err != nil {
		t.Fatal(err)
	}

	if err := d.seedCrewTicketWakeDeliveries(); err != nil {
		t.Fatal(err)
	}
	if d.initialPromptPending("settled-day") {
		t.Fatal("settled day was incorrectly put back behind its first-prompt gate")
	}
	if timer := currentNudgeTimer(d, "settled-day"); timer == nil {
		t.Fatal("restart did not restore ordinary unread delivery for a settled day")
	}
}

func TestLiveWatchLeaseWinsCountdownRaceAndConsumesOnce(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	_, agentID, inputs := delegateForNotify(t, d, "claude")
	ticketID := boundTicketID(t, d, agentID)
	d.store.UpdateState(agentID, protocol.StateWorking)
	watch := protocol.TicketInboxModeWatch
	if bundles := callTicketInboxMode(t, d, agentID, &watch); len(bundles) != 0 {
		t.Fatalf("initial watch = %+v, want empty lease refresh", bundles)
	}

	commentOnTicket(t, d, ticketID, "take a look")
	fireNudgeNow(t, d, agentID)
	if wasNudged(inputs(agentID)) {
		t.Fatal("countdown doorbelled while the live watch lease owned delivery")
	}
	bundles := callTicketInboxMode(t, d, agentID, &watch)
	if len(bundles) != 1 || bundles[0].TicketID != ticketID {
		t.Fatalf("watch delivery = %+v, want one ticket bundle", bundles)
	}
	if again := callTicketInboxMode(t, d, agentID, &watch); len(again) != 0 {
		t.Fatalf("second watch delivery = %+v, want acknowledged empty queue", again)
	}
}

func TestWatchLeaseCoversReportedSlowPollingInterval(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	_, agentID, _ := delegateForNotify(t, d, "claude")
	watch := protocol.TicketInboxModeWatch
	interval := "30000"
	started := time.Now()
	callTicketInboxRequest(t, d, agentID, &watch, &interval)

	d.deliveryMu.Lock()
	leaseUntil := d.watchLeaseUntil[agentID]
	d.deliveryMu.Unlock()
	if leaseUntil.Before(started.Add(44 * time.Second)) {
		t.Fatalf("slow-watch lease expires at %s, want interval plus jitter grace", leaseUntil)
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
