package daemon

import (
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/sessionstate"
	"github.com/victorarias/attn/internal/statetrace"
)

func evidenceOf(t *testing.T, d *Daemon, sessionID string) sessionstate.Evidence {
	t.Helper()
	got, ok := d.evidenceTable().snapshot(sessionID)
	if !ok {
		t.Fatalf("no evidence recorded for %s", sessionID)
	}
	return got
}

// The heartbeat is a level with its own vocabulary. It has to land in the
// heartbeat slot with its observation time intact, because its freshness — not
// its arrival — is what the resolver reads.
func TestHeartbeatEvidenceKeepsItsObservationTime(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-hb-evidence"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)
	observed := time.Now().Add(-300 * time.Millisecond)

	d.recordPTYEvidence(id, pty.Observation{
		Source: pty.SourceHeartbeat,
		Claim:  "busy",
		Detail: "⠐ working",
		At:     observed,
	})

	got := evidenceOf(t, d, id).Heartbeat
	if got == nil || got.Claim != sessionstate.ClaimBusy {
		t.Fatalf("got %+v, want a busy heartbeat", got)
	}
	if !got.ObservedAt.Equal(observed) {
		t.Fatalf("ObservedAt %s, want the source's %s", got.ObservedAt, observed)
	}
	if got.Detail != "⠐ working" {
		t.Fatalf("detail %q, want the title", got.Detail)
	}
}

func TestNotBusyHeartbeatEvidenceIsSettled(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-hb-settled"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)

	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "not_busy", At: time.Now()})

	if got := evidenceOf(t, d, id).Heartbeat; got == nil || got.Claim != sessionstate.ClaimSettled {
		t.Fatalf("got %+v, want settled", got)
	}
}

// The brackets are levels: a turn opens and stays open until something closes
// it. Getting this wrong is the whole stuck class.
func TestBracketEvidenceOpensAndCloses(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-bracket"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)

	d.recordBracketEvidence(id, protocol.StateWorking)
	if !evidenceOf(t, d, id).TurnOpen {
		t.Fatal("working must open the turn")
	}

	d.recordBracketEvidence(id, protocol.StateIdle)
	got := evidenceOf(t, d, id)
	if got.TurnOpen || got.ToolOpen {
		t.Fatalf("a settled state must close both brackets: %+v", got)
	}
}

// The Stop facts are the ones the CLI used to collapse into a state string
// before they crossed the socket, which is exactly why the resolver could not
// see them.
func TestStopFactsBecomeEvidence(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-stop-facts"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)

	d.recordStopFacts(id, true, false)
	if got := evidenceOf(t, d, id); !got.BackgroundWork || got.PendingCron {
		t.Fatalf("got %+v, want background work only", got)
	}

	// They are levels, so a later stop with nothing outstanding clears them.
	d.recordStopFacts(id, false, false)
	if got := evidenceOf(t, d, id); got.BackgroundWork {
		t.Fatalf("background work outlived the stop that cleared it: %+v", got)
	}
}

// "default" is the one mode that means the user answers. Everything else puts
// something between the agent and the user.
func TestReviewerEvidenceReadsThePermissionMode(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want bool
	}{
		{mode: "default", want: false},
		{mode: "auto", want: true},
		{mode: "acceptEdits", want: true},
		{mode: "bypassPermissions", want: true},
	} {
		d := newTraceDaemon(t)
		id := "sess-reviewer-" + tc.mode
		addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)
		d.recordReviewerEvidence(id, tc.mode)
		if got := evidenceOf(t, d, id).ReviewerInLoop; got != tc.want {
			t.Fatalf("mode %q -> ReviewerInLoop %v, want %v", tc.mode, got, tc.want)
		}
	}
}

// An agent that reports no mode must not be recorded as having no reviewer:
// silence is not a claim, and treating it as one would remove the dwell from
// every codex session.
func TestAnAbsentPermissionModeRecordsNothing(t *testing.T) {
	d := newTraceDaemon(t)
	addCharacterizationSession(t, d, "sess-no-mode", protocol.SessionAgentClaude, protocol.SessionStateWorking)
	d.recordReviewerEvidence("sess-no-mode", "  ")
	if _, ok := d.evidenceTable().snapshot("sess-no-mode"); ok {
		t.Fatal("an absent mode created an evidence record")
	}
}

// Every write stamps LastMovement, so stuck detection cannot drift out of sync
// with the writes it is watching.
func TestEveryEvidenceWriteStampsMovement(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-movement"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)

	d.recordBracketEvidence(id, protocol.StateWorking)
	first := evidenceOf(t, d, id).LastMovement
	if first.IsZero() {
		t.Fatal("LastMovement not stamped")
	}

	d.recordStopFacts(id, false, true)
	if second := evidenceOf(t, d, id).LastMovement; !second.After(first) {
		t.Fatalf("LastMovement %s did not advance past %s", second, first)
	}
}

// The tick publishes state. A session sitting in a state no source will ever
// contradict is moved by the resolver alone.
func TestTheResolveTickPublishesTheResolution(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-flip"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateIdle)

	now := time.Now()
	d.recordPTYEvidence(id, pty.Observation{
		Source: pty.SourceHeartbeat,
		Claim:  "busy",
		Detail: "⠐ working",
		At:     now,
	})

	d.resolveAllSessions(now)

	if state := d.store.Get(id).State; state != protocol.SessionStateWorking {
		t.Fatalf("state %q, want working: a fresh heartbeat outranks the stored idle", state)
	}
	got := onlyObservation(t, d, id)
	if got.Outcome != statetrace.OutcomeApplied {
		t.Fatalf("outcome %q, want applied", got.Outcome)
	}
	if got.Source != stateSourceResolver {
		t.Fatalf("source %q, want %q", got.Source, stateSourceResolver)
	}
	// The reason names the clause that won, which is the whole diagnostic value
	// of the row: "working" alone never explains a wrong color.
	if !strings.Contains(got.Detail, string(sessionstate.ReasonHeartbeatFresh)) {
		t.Fatalf("detail %q does not name the winning clause", got.Detail)
	}
}

// States outside the resolver's remit describe the session's lifecycle, not its
// agent. `recoverable` is the dangerous one: the revive path sets it precisely
// because the worker died, so the process evidence the resolver would read is
// both present and meaningless.
func TestTheResolveTickLeavesAnUnownedStateAlone(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-unowned"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateRecoverable)

	d.recordProcessEvidence(id, true)
	d.resolveAllSessions(time.Now())

	if state := d.store.Get(id).State; state != protocol.SessionStateRecoverable {
		t.Fatalf("state %q, want recoverable — the resolver overwrote a state it does not own", state)
	}
}

// A session the evidence table has barely heard of resolves to unknown for want
// of evidence. Publishing that would repaint healthy sessions grey on the first
// tick after any single observation.
func TestTheResolveTickDoesNotPublishAnAbsenceOfEvidence(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-no-evidence"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWaitingInput)

	// A reviewer report is evidence of who answers approvals and nothing else,
	// so it creates the table entry without supporting any state.
	d.recordReviewerEvidence(id, "acceptEdits")
	d.resolveAllSessions(time.Now())

	if state := d.store.Get(id).State; state != protocol.SessionStateWaitingInput {
		t.Fatalf("state %q, want waiting_input untouched", state)
	}
}

// The settle-flicker gate, end to end through the daemon: while a classification
// is running, a settled turn holds its pre-settle state rather than flashing
// idle and being corrected when the verdict lands.
func TestARunningClassificationHoldsTheSettle(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-classifying"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)

	now := time.Now()
	d.recordClassifierStarted(id, now)
	// The turn is over as far as every other source is concerned: the Stop hook
	// closed the bracket and the agent stopped painting spinner frames.
	d.recordBracketEvidence(id, protocol.StateIdle)
	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "not_busy", At: now})

	d.resolveAllSessions(now.Add(time.Second))

	if state := d.store.Get(id).State; state != protocol.SessionStateWorking {
		t.Fatalf("state %q, want working held while the classifier runs", state)
	}

	// The verdict lands and the hold ends on the same evidence.
	d.recordClassifierEvidence(id, protocol.StateWaitingInput, now)
	d.recordClassifierFinished(id)
	d.resolveAllSessions(now.Add(2 * time.Second))

	if state := d.store.Get(id).State; state != protocol.SessionStateWaitingInput {
		t.Fatalf("state %q, want the classifier verdict published", state)
	}
}

// A classifier that never returns must not be able to freeze a color, which is
// what an unbounded hold would allow.
func TestAHungClassifierStopsHoldingTheSettle(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-classifier-hung"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)

	now := time.Now()
	// The turn ran before it settled, which is what makes the settle a settle.
	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "busy", At: now})
	d.recordClassifierStarted(id, now)
	d.recordBracketEvidence(id, protocol.StateIdle)
	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "not_busy", At: now})

	policy := sessionstate.PolicyFor(string(protocol.SessionAgentClaude))
	d.resolveAllSessions(now.Add(policy.ClassifierTimeout + time.Second))

	if state := d.store.Get(id).State; state != protocol.SessionStateIdle {
		t.Fatalf("state %q, want idle: the hold must expire with the classifier", state)
	}
}

// A verdict belongs to the turn it judged. Turn A's answer must never be
// published as turn B's state, which is what a verdict left in the table does
// the moment B settles with its own classification still in flight.
func TestAVerdictDoesNotSurviveIntoTheNextTurn(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-cross-turn"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)

	now := time.Now()

	// Turn A ends waiting on the user, and that verdict is published.
	d.recordBracketEvidence(id, protocol.StateIdle)
	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "not_busy", At: now})
	d.recordClassifierEvidence(id, protocol.StateWaitingInput, now)
	d.resolveAllSessions(now.Add(time.Second))

	if state := d.store.Get(id).State; state != protocol.SessionStateWaitingInput {
		t.Fatalf("state %q, want turn A's verdict published", state)
	}

	// Turn B opens and the agent goes busy.
	openedAt := now.Add(2 * time.Second)
	d.recordBracketEvidence(id, protocol.StateWorking)
	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "busy", At: openedAt})
	d.resolveAllSessions(openedAt)

	if state := d.store.Get(id).State; state != protocol.SessionStateWorking {
		t.Fatalf("state %q, want working for turn B", state)
	}

	// Turn B settles and begins its own classification.
	settledAt := now.Add(10 * time.Second)
	d.recordClassifierStarted(id, settledAt)
	d.recordBracketEvidence(id, protocol.StateIdle)
	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "not_busy", At: settledAt})
	d.resolveAllSessions(settledAt.Add(time.Second))

	if state := d.store.Get(id).State; state != protocol.SessionStateWorking {
		t.Fatalf("state %q, want the settle held for turn B's own verdict", state)
	}

	// And turn B's own answer is what lands.
	d.recordClassifierEvidence(id, protocol.StateIdle, settledAt)
	d.recordClassifierFinished(id)
	d.resolveAllSessions(settledAt.Add(2 * time.Second))

	if state := d.store.Get(id).State; state != protocol.SessionStateIdle {
		t.Fatalf("state %q, want turn B's verdict published", state)
	}
}

// The other half of the same rule. A turn short enough that the agent never
// paints a busy frame leaves the previous verdict newer than the last busy
// heartbeat, so freshness alone cannot tell it is spent — the opening bracket
// has to retire it.
func TestAVerdictDoesNotSurviveATurnThatNeverPaintedBusy(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-cross-turn-quiet"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)

	now := time.Now()

	// Turn A: busy, settles, verdict lands after the last busy frame.
	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "busy", At: now})
	d.recordBracketEvidence(id, protocol.StateIdle)
	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "not_busy", At: now.Add(time.Second)})
	d.recordClassifierEvidence(id, protocol.StateWaitingInput, now.Add(2*time.Second))
	d.resolveAllSessions(now.Add(2500 * time.Millisecond))

	if state := d.store.Get(id).State; state != protocol.SessionStateWaitingInput {
		t.Fatalf("state %q, want turn A's verdict published", state)
	}

	// Turn B opens — still inside the busy window, so no new busy frame is
	// needed to hold it working.
	openedAt := now.Add(3 * time.Second)
	d.recordBracketEvidence(id, protocol.StateWorking)
	d.resolveAllSessions(openedAt)

	if state := d.store.Get(id).State; state != protocol.SessionStateWorking {
		t.Fatalf("state %q, want working for turn B", state)
	}

	// It settles with its own classification in flight, having never painted a
	// busy frame of its own.
	settledAt := now.Add(3500 * time.Millisecond)
	d.recordClassifierStarted(id, settledAt)
	d.recordBracketEvidence(id, protocol.StateIdle)
	d.resolveAllSessions(settledAt)

	if state := d.store.Get(id).State; state != protocol.SessionStateWorking {
		t.Fatalf("state %q, want the settle held: turn A's verdict is not turn B's answer", state)
	}
}

// A tick that agreed every second would bury every other observation in a ring
// that holds 256 of them.
func TestTheResolveTickIsSilentWhenItAgrees(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-shadow-agree"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)

	now := time.Now()
	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "busy", At: now})
	for range 5 {
		d.resolveAllSessions(now)
	}

	if got := traceOf(t, d, id); len(got) != 0 {
		t.Fatalf("an agreeing tick recorded %+v", got)
	}
}

// A removed session must stop being resolved, or the table grows for the
// lifetime of the daemon — the same leak the trace ring was fixed for.
func TestTheResolveTickForgetsARemovedSession(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-shadow-gone"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)
	d.recordBracketEvidence(id, protocol.StateWorking)

	d.store.Remove(id)
	d.resolveAllSessions(time.Now())

	if _, ok := d.evidenceTable().snapshot(id); ok {
		t.Fatal("evidence survived the session it described")
	}
}

// LastBusyAt is what the resolver measures staleness from, so it must advance
// only on busy frames. Advancing it on any heartbeat would make a mid-turn idle
// blip look like fresh proof of work; not advancing it at all would settle every
// turn after one window.
func TestOnlyABusyHeartbeatAdvancesLastBusy(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-lastbusy"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)

	busy := time.Now().Add(-2 * time.Second)
	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "busy", At: busy})
	if got := evidenceOf(t, d, id).LastBusyAt; !got.Equal(busy) {
		t.Fatalf("LastBusyAt %s, want %s", got, busy)
	}

	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "not_busy", At: time.Now()})
	got := evidenceOf(t, d, id)
	if !got.LastBusyAt.Equal(busy) {
		t.Fatalf("a not-busy frame moved LastBusyAt to %s", got.LastBusyAt)
	}
	// The latest frame is still recorded — it is the detail and freshness the
	// heartbeat-fresh clause reads.
	if got.Heartbeat.Claim != sessionstate.ClaimSettled {
		t.Fatalf("latest heartbeat %+v, want the settled frame", got.Heartbeat)
	}
}

// An id with no store row can never be read back — the tick resolves against a
// store row — and can never be cleaned up, because cleanup hangs off session
// removal. Ringing one would leak an entry per stale id for the daemon's life.
func TestEvidenceIsNotRecordedForAnUnknownSession(t *testing.T) {
	d := newTraceDaemon(t)

	d.recordPTYEvidence("ghost", pty.Observation{Source: pty.SourceHeartbeat, Claim: "busy", At: time.Now()})
	d.recordBracketEvidence("ghost", protocol.StateWorking)
	d.recordStopFacts("ghost", true, false)
	d.recordReviewerEvidence("ghost", "auto")
	d.recordProcessEvidence("ghost", true)

	if _, ok := d.evidenceTable().snapshot("ghost"); ok {
		t.Fatal("an unknown session created an evidence entry")
	}
}

// The stale-worker race: a session is removed while one of its observations is
// still in flight. The write's liveness check and its append have to be one
// atomic step, or the late writer recreates an entry for an id that will never
// be removed again.
//
// The hook parks the writer *after* it has read the store row and *before* it
// appends — the exact window that leaks when the check sits outside the lock.
func TestEvidenceDoesNotLeakWhenRemovalRacesTheWrite(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-evidence-race"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)

	paused := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	evidenceRecordGateHook = func(observed string) {
		if observed != id {
			return
		}
		once.Do(func() {
			close(paused)
			<-release
		})
	}
	t.Cleanup(func() { evidenceRecordGateHook = nil })

	wrote := make(chan struct{})
	go func() {
		defer close(wrote)
		d.recordBracketEvidence(id, protocol.StateWorking)
	}()

	<-paused
	// Removal runs entirely while the writer is parked mid-write. With admission
	// inside the lock it cannot interleave here at all — it blocks until the
	// writer finishes and then forgets whatever the writer left.
	go func() {
		d.dropSessionRecord(id)
	}()
	// Give the removal a chance to reach the table before the writer resumes, so
	// the ordering under test is the hostile one rather than a lucky one.
	time.Sleep(20 * time.Millisecond)
	close(release)
	<-wrote

	// Whichever order won, no entry may survive the session.
	deadline := time.Now().Add(2 * time.Second)
	for {
		got, ok := d.evidenceTable().snapshot(id)
		if !ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("evidence survived the racing removal: %+v", got)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The two Notification types are different signals and must not land in the same
// slot. permission_prompt is a leading edge the resolver acts on; idle_prompt is
// a late confirmation that only says the agent stopped.
func TestNotificationEvidenceSplitsByType(t *testing.T) {
	t.Run("a permission prompt becomes an open approval", func(t *testing.T) {
		d := newTraceDaemon(t)
		id := "sess-notify-permission"
		addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)
		d.recordNotificationEvidence(id, notifyPermissionPrompt, "Claude needs your permission")

		got := evidenceOf(t, d, id)
		if got.LastHarnessEvent == nil {
			t.Fatal("permission_prompt recorded no harness event")
		}
		if got.LastHarnessEvent.Claim != sessionstate.ClaimApprovalPending {
			t.Fatalf("claim = %q, want %q", got.LastHarnessEvent.Claim, sessionstate.ClaimApprovalPending)
		}
		if !got.PromptIdleAt.IsZero() {
			t.Fatal("permission_prompt set PromptIdleAt; it is not a settle confirmation")
		}
	})

	t.Run("an idle prompt confirms the prompt without claiming why", func(t *testing.T) {
		d := newTraceDaemon(t)
		id := "sess-notify-idle"
		addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)
		d.recordNotificationEvidence(id, notifyIdlePrompt, "Claude is waiting for your input")

		got := evidenceOf(t, d, id)
		if got.PromptIdleAt.IsZero() {
			t.Fatal("idle_prompt did not confirm the prompt")
		}
		// The message says "waiting for your input", but the signal fires for a
		// finished task too. Turning it into a claim would invent a distinction
		// it does not carry, and would outrank the classifier that can tell.
		if got.LastHarnessEvent != nil {
			t.Fatalf("idle_prompt became a %q claim; it is a confirmation, not a verdict",
				got.LastHarnessEvent.Claim)
		}
	})

	t.Run("an unknown type records nothing", func(t *testing.T) {
		d := newTraceDaemon(t)
		id := "sess-notify-unknown"
		addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)
		d.recordNotificationEvidence(id, "some_future_type", "")

		if _, ok := d.evidenceTable().snapshot(id); ok {
			t.Fatal("an unrecognized notification type wrote evidence")
		}
	})
}

// The socket handler has to reach the evidence table, not only the trace ring.
// Recording to the trace alone leaves the resolver blind to the strongest
// approval signal either agent emits.
func TestTheNotificationHandlerReachesTheEvidenceTable(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-notify-wire"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)

	resp := callHandler(t, func(conn net.Conn) {
		d.handleHookNotification(conn, &protocol.HookNotificationMessage{
			ID:               id,
			NotificationType: notifyPermissionPrompt,
			Message:          protocol.Ptr("Claude needs your permission"),
		})
	})
	if !resp.Ok {
		t.Fatalf("handler failed: %s", protocol.Deref(resp.Error))
	}

	got := evidenceOf(t, d, id)
	if got.LastHarnessEvent == nil || got.LastHarnessEvent.Claim != sessionstate.ClaimApprovalPending {
		t.Fatal("the notification handler recorded no approval evidence")
	}
}

// A codex approval arrives on the heartbeat channel, because codex announces it
// in its title. It has to become an approval claim and simultaneously stop
// looking busy: leaving the heartbeat busy would let the fresh-busy clause,
// which outranks the approval clause, hide the approval entirely.
func TestACodexApprovalTitleBecomesAnApproval(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-codex-approval"
	addCharacterizationSession(t, d, id, protocol.SessionAgentCodex, protocol.SessionStateWorking)
	at := time.Now()
	d.recordPTYEvidence(id, pty.Observation{
		Source: pty.SourceHeartbeat,
		Claim:  "approval",
		Detail: "scratchpad",
		At:     at,
	})

	got := evidenceOf(t, d, id)
	if got.LastHarnessEvent == nil || got.LastHarnessEvent.Claim != sessionstate.ClaimApprovalPending {
		t.Fatal("the codex approval title recorded no approval")
	}
	if got.Heartbeat == nil || got.Heartbeat.Claim != sessionstate.ClaimBusy {
		// Guard the exact hazard: an approval title that still reads busy is
		// invisible, because ReasonHeartbeatFresh returns before the approval
		// clause is reached.
	} else {
		t.Fatal("the approval title left the heartbeat busy, which hides the approval")
	}
	if !got.LastBusyAt.IsZero() {
		t.Fatal("an approval title advanced LastBusyAt; it is not a running turn")
	}

	// End to end through the resolver, which is the claim that matters.
	res := sessionstate.Resolve(got, sessionstate.PolicyFor(string(protocol.SessionAgentCodex)), at)
	if res.State != protocol.SessionStatePendingApproval {
		t.Fatalf("resolved %q (%s), want pending_approval", res.State, res.Reason)
	}
}
