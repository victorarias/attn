package daemon

import (
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

// makeSelfMonitor flips a session's stored agent to Claude so it resolves to
// HasSelfMonitor=true through ticketObserverForSession. The harness delegation
// source spawns as a shell (the test default), but the production chief of staff is
// Claude — the only kind of session that takes the DeliveryWatch backstop path. Use
// this to model the real chief in backstop tests.
func makeSelfMonitor(t *testing.T, d *Daemon, sessionID string) {
	t.Helper()
	s := d.store.Get(sessionID)
	if s == nil {
		t.Fatalf("makeSelfMonitor: session %s not found", sessionID)
	}
	s.Agent = protocol.SessionAgentClaude
	d.store.Add(s)
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
	t.Cleanup(d.stopTicketBackstops) // idle self-monitor schedules a deferred backstop
	_, agentID, inputs := delegateForNotify(t, d, "claude")
	ticketID := boundTicketID(t, d, agentID)
	d.store.UpdateState(agentID, protocol.StateIdle)

	d.notifyTicketObservers(ticketID)

	if wasNudged(inputs(agentID)) {
		t.Fatal("self-monitoring claude agent should not be typed into synchronously")
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

// The self-monitor backstop: an idle self-monitor with unread ticket activity that
// no live `--watch` Monitor drained gets the doorbell from the deferred re-check —
// the case the synchronous DeliveryWatch no-op leaves silent, fired here directly.
//
// The subject is a delegated Claude agent. It exercises the exact production path
// the chief takes: ticketObserverForSession -> HasSelfMonitor=true. The backstop is
// keyed on that capability, not on being the chief, so this also proves the
// "applies to all agents if Claude didn't use the monitor" intent — the harness
// chief is a shell (non-self-monitor), so a delegated Claude agent is the faithful
// self-monitor subject.
func TestTicketBackstopDoorbellsIdleSelfMonitorWithUnread(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(d.stopTicketBackstops)
	_, agentID, inputs := delegateForNotify(t, d, "claude")
	ticketID := boundTicketID(t, d, agentID)
	d.store.UpdateState(agentID, protocol.StateWorking) // no schedule during the steer
	commentOnTicket(t, d, ticketID, "take a look at the failing test")
	d.store.UpdateState(agentID, protocol.StateIdle)

	d.ticketBackstopRecheck(agentID)

	if !wasNudged(inputs(agentID)) {
		t.Fatal("idle self-monitor with unread was not doorbelled by the backstop")
	}
}

// The headline scenario this whole mechanism exists for: the chief of staff
// delegates, goes idle, and its delegate later reports ready-for-review. The chief
// is a self-monitor (Claude in production), so the synchronous notify resolves to
// DeliveryWatch — a no-op that, on its own, leaves the idle chief unaware the work
// came back (exactly the gap Victor hit). The deferred backstop closes it: the
// report schedules a re-check that doorbells the still-idle chief after the grace.
// This drives the full chain — agent reports -> notify -> schedule -> fire ->
// doorbell — against the real chief subject, with a tiny grace for a fast timer. The
// agent that authored the report is never doorbelled about its own status change.
func TestTicketBackstopDoorbellsIdleChiefAfterAgentReports(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ticketBackstopGrace = 15 * time.Millisecond
	t.Cleanup(d.stopTicketBackstops)
	chiefID, agentID, inputs := delegateForNotify(t, d, "claude")
	makeSelfMonitor(t, d, chiefID)                   // the production chief is Claude; the harness default is shell
	d.store.UpdateState(chiefID, protocol.StateIdle) // delegated, then went idle waiting

	// The delegate reports ready-for-review: an event the idle chief did not author,
	// so it is unread for the chief — the signal the chief's DeliveryWatch no-op drops.
	callSetTicketStatus(t, d, agentID, string(protocol.DispatchWorkStateReadyForReview), "done, please review")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !wasNudged(inputs(chiefID)) {
		time.Sleep(10 * time.Millisecond)
	}
	if !wasNudged(inputs(chiefID)) {
		t.Fatal("idle Claude chief was never doorbelled after its delegate reported back")
	}
	if wasNudged(inputs(agentID)) {
		t.Fatal("the reporting agent was doorbelled about its own status change")
	}
}

// Composition with A: if a live `--watch` Monitor already drained the inbox (the
// cursor advanced), the deferred re-check sees nothing unread and self-suppresses,
// so an actively-watching self-monitor is never double-notified.
func TestTicketBackstopSuppressedAfterWatchDrains(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(d.stopTicketBackstops)
	_, agentID, inputs := delegateForNotify(t, d, "claude")
	ticketID := boundTicketID(t, d, agentID)
	d.store.UpdateState(agentID, protocol.StateWorking)
	commentOnTicket(t, d, ticketID, "take a look")
	d.store.UpdateState(agentID, protocol.StateIdle)

	// Simulate `attn ticket inbox --watch` draining the agent's queue.
	callTicketInbox(t, d, agentID)

	d.ticketBackstopRecheck(agentID)

	if wasNudged(inputs(agentID)) {
		t.Fatal("backstop doorbelled a self-monitor whose watch already drained the inbox")
	}
}

// A busy self-monitor is never doorbelled by the backstop — it is working, not
// missing the event. The re-check is a no-op until the session is at rest.
func TestTicketBackstopSuppressedWhenBusy(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(d.stopTicketBackstops)
	_, agentID, inputs := delegateForNotify(t, d, "claude")
	ticketID := boundTicketID(t, d, agentID)
	d.store.UpdateState(agentID, protocol.StateWorking)
	commentOnTicket(t, d, ticketID, "take a look") // lands while busy -> no schedule

	d.ticketBackstopRecheck(agentID)

	if wasNudged(inputs(agentID)) {
		t.Fatal("backstop doorbelled a busy self-monitor")
	}
}

// End-to-end scheduling: the synchronous notify for an idle self-monitor schedules
// the deferred backstop, which fires after the grace and doorbells. Uses a tiny
// grace override so the timer is fast and deterministic.
func TestNotifySchedulesBackstopForIdleSelfMonitor(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ticketBackstopGrace = 15 * time.Millisecond
	t.Cleanup(d.stopTicketBackstops)
	_, agentID, inputs := delegateForNotify(t, d, "claude")
	ticketID := boundTicketID(t, d, agentID)
	d.store.UpdateState(agentID, protocol.StateIdle)

	// A steer lands while the agent is idle: the synchronous notify resolves to
	// DeliveryWatch and schedules the backstop (grace 15ms).
	commentOnTicket(t, d, ticketID, "one more thing")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if wasNudged(inputs(agentID)) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("scheduled backstop never doorbelled the idle self-monitor")
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

func currentBackstopTimer(d *Daemon, sessionID string) *time.Timer {
	d.ticketBackstopMu.Lock()
	defer d.ticketBackstopMu.Unlock()
	return d.ticketBackstopTimers[sessionID]
}

// Race regression: a backstop timer that was superseded by a reschedule must NOT
// doorbell when it later fires — only the current timer does. Without the timer
// identity guard, the stale fire would both evict the live timer's map entry and
// doorbell, producing the double-doorbell the deferred design exists to prevent.
// Driven deterministically with an hour-long grace so no real timer fires; the fire
// callbacks are invoked by hand with the captured handles.
func TestTicketBackstopStaleTimerDoesNotDoorbell(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ticketBackstopGrace = time.Hour
	t.Cleanup(d.stopTicketBackstops)
	_, agentID, inputs := delegateForNotify(t, d, "claude")
	ticketID := boundTicketID(t, d, agentID)
	d.store.UpdateState(agentID, protocol.StateWorking)
	commentOnTicket(t, d, ticketID, "take a look") // lands while busy -> no schedule
	d.store.UpdateState(agentID, protocol.StateIdle)

	d.scheduleTicketBackstop(agentID)
	stale := currentBackstopTimer(d, agentID)
	d.scheduleTicketBackstop(agentID) // reschedule supersedes `stale`
	current := currentBackstopTimer(d, agentID)
	if stale == nil || current == nil || stale == current {
		t.Fatalf("reschedule did not replace the timer: stale=%p current=%p", stale, current)
	}

	d.ticketBackstopFire(agentID, stale) // superseded: must bail
	if wasNudged(inputs(agentID)) {
		t.Fatal("a superseded backstop timer doorbelled the session (double-doorbell race)")
	}

	d.ticketBackstopFire(agentID, current) // current: doorbells exactly once
	if n := nudgeCount(inputs(agentID)); n != 1 {
		t.Fatalf("current backstop timer doorbelled %d times, want exactly 1", n)
	}
}

// Debounce: a burst of events while the agent is idle reschedules the backstop each
// time, collapsing to exactly ONE pending timer — firing it doorbells once, not once
// per event. Driven deterministically with an hour-long grace so no real timer fires
// mid-burst and the single surviving timer is fired by hand: this asserts the
// debounce invariant (N reschedules -> one timer -> one doorbell) without depending
// on wall-clock timing. The end-to-end "a scheduled timer actually fires on its own"
// path is covered by TestNotifySchedulesBackstopForIdleSelfMonitor.
func TestTicketBackstopDebouncesBurst(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ticketBackstopGrace = time.Hour
	t.Cleanup(d.stopTicketBackstops)
	_, agentID, inputs := delegateForNotify(t, d, "claude")
	ticketID := boundTicketID(t, d, agentID)
	d.store.UpdateState(agentID, protocol.StateIdle)

	commentOnTicket(t, d, ticketID, "take a look") // unread + first schedule
	for i := 0; i < 3; i++ {
		d.notifyTicketObservers(ticketID) // each reschedules, replacing the prior timer
	}

	// The burst left exactly one pending timer and no premature doorbell.
	if wasNudged(inputs(agentID)) {
		t.Fatal("burst doorbelled the self-monitor before any grace elapsed")
	}
	timer := currentBackstopTimer(d, agentID)
	if timer == nil {
		t.Fatal("burst left no pending backstop timer")
	}

	// Firing that single surviving timer doorbells exactly once.
	d.ticketBackstopFire(agentID, timer)
	if n := nudgeCount(inputs(agentID)); n != 1 {
		t.Fatalf("burst produced %d doorbells, want exactly 1 (debounced)", n)
	}
}

// The went-idle path schedules the backstop: an event that lands while the agent is
// busy schedules nothing, but when the agent later settles, notifyTicketSessionWentIdle
// re-runs the decision and arms the backstop, which then fires.
func TestTicketBackstopScheduledOnWentIdle(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ticketBackstopGrace = 20 * time.Millisecond
	t.Cleanup(d.stopTicketBackstops)
	_, agentID, inputs := delegateForNotify(t, d, "claude")
	ticketID := boundTicketID(t, d, agentID)
	d.store.UpdateState(agentID, protocol.StateWorking)

	commentOnTicket(t, d, ticketID, "take a look") // lands while busy -> no schedule
	if currentBackstopTimer(d, agentID) != nil {
		t.Fatal("backstop scheduled while the agent was busy")
	}

	d.store.UpdateState(agentID, protocol.StateIdle)
	d.notifyTicketSessionWentIdle(agentID) // settles -> schedules the backstop

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if wasNudged(inputs(agentID)) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("backstop did not fire after the agent went idle")
}
