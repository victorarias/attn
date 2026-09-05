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
	"github.com/victorarias/attn/internal/store"
)

func evidenceOf(t *testing.T, d *Daemon, sessionID string) sessionstate.Evidence {
	t.Helper()
	got, ok := d.evidenceTable().snapshot(sessionID)
	if !ok {
		t.Fatalf("no evidence recorded for %s", sessionID)
	}
	return got
}

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

func TestAnUnclassifiedTitleMovesEvidenceWithoutClaimingALevel(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-hb-unclassified"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)
	busyAt := time.Now().Add(-2 * time.Minute)
	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "busy", At: busyAt})

	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "unclassified", Detail: "htop", At: time.Now()})

	got := evidenceOf(t, d, id)
	if got.Heartbeat == nil || got.Heartbeat.Claim != sessionstate.ClaimBusy {
		t.Fatalf("heartbeat %+v, want the busy level to still stand", got.Heartbeat)
	}
	if !got.LastBusyAt.Equal(busyAt) {
		t.Fatalf("LastBusyAt %s, want it left at %s", got.LastBusyAt, busyAt)
	}
	if !got.LastMovement.After(busyAt) {
		t.Fatalf("LastMovement %s, want it advanced past %s", got.LastMovement, busyAt)
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

func TestRepeatedSettledHeartbeatAvoidsStoreLivenessReads(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-hb-settled-repeat"
	addCharacterizationSession(t, d, id, protocol.SessionAgentCodex, protocol.SessionStateIdle)

	oldEvidenceHook := evidenceRecordGateHook
	oldTraceHook := stateTraceRecordGateHook
	var evidenceReads, traceReads int
	evidenceRecordGateHook = func(string) { evidenceReads++ }
	stateTraceRecordGateHook = func(string) { traceReads++ }
	t.Cleanup(func() {
		evidenceRecordGateHook = oldEvidenceHook
		stateTraceRecordGateHook = oldTraceHook
	})

	start := time.Now()
	d.handlePTYState(id, heartbeatObs("not_busy", "at prompt", start))
	first := evidenceOf(t, d, id)
	d.handlePTYState(id, heartbeatObs("not_busy", "at prompt", start.Add(time.Second)))

	if evidenceReads != 1 || traceReads != 1 {
		t.Fatalf("exact settled repeat performed liveness reads: evidence=%d trace=%d", evidenceReads, traceReads)
	}
	if got := evidenceOf(t, d, id); !got.LastMovement.Equal(first.LastMovement) {
		t.Fatalf("exact settled repeat moved evidence from %s to %s", first.LastMovement, got.LastMovement)
	}
	if got := onlyObservation(t, d, id); got.Repeats != 0 {
		t.Fatalf("exact settled repeat reached the trace: %+v", got)
	}

	changedAt := start.Add(2 * time.Second)
	d.handlePTYState(id, heartbeatObs("not_busy", "different prompt", changedAt))
	if evidenceReads != 2 || traceReads != 2 {
		t.Fatalf("changed settled detail reads: evidence=%d trace=%d, want 2 each", evidenceReads, traceReads)
	}
	if got := evidenceOf(t, d, id); !got.LastMovement.Equal(changedAt) {
		t.Fatalf("changed settled detail moved evidence at %s, want %s", got.LastMovement, changedAt)
	}
}

func TestRepeatedBusyHeartbeatKeepsRefreshingLiveness(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-hb-busy-repeat"
	addCharacterizationSession(t, d, id, protocol.SessionAgentCodex, protocol.SessionStateWorking)

	oldEvidenceHook := evidenceRecordGateHook
	oldTraceHook := stateTraceRecordGateHook
	var evidenceReads, traceReads int
	evidenceRecordGateHook = func(string) { evidenceReads++ }
	stateTraceRecordGateHook = func(string) { traceReads++ }
	t.Cleanup(func() {
		evidenceRecordGateHook = oldEvidenceHook
		stateTraceRecordGateHook = oldTraceHook
	})

	start := time.Now()
	d.handlePTYState(id, heartbeatObs("busy", "working", start))
	repeatedAt := start.Add(time.Second)
	d.handlePTYState(id, heartbeatObs("busy", "working", repeatedAt))

	if evidenceReads != 2 || traceReads != 2 {
		t.Fatalf("busy repeats skipped liveness reads: evidence=%d trace=%d", evidenceReads, traceReads)
	}
	got := evidenceOf(t, d, id)
	if !got.LastMovement.Equal(repeatedAt) || !got.LastBusyAt.Equal(repeatedAt) {
		t.Fatalf("busy repeat did not refresh liveness at %s: %+v", repeatedAt, got)
	}
}

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

func TestStopFactsBecomeEvidence(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-stop-facts"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)

	d.recordStopFacts(id, true, false)
	if got := evidenceOf(t, d, id); !got.BackgroundWork || got.PendingCron {
		t.Fatalf("got %+v, want background work only", got)
	}

	d.recordStopFacts(id, false, false)
	if got := evidenceOf(t, d, id); got.BackgroundWork {
		t.Fatalf("background work outlived the stop that cleared it: %+v", got)
	}
}

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
		d.recordReviewerEvidenceFromPermissionMode(id, tc.mode)
		if got := evidenceOf(t, d, id).ReviewerInLoop; got != tc.want {
			t.Fatalf("mode %q -> ReviewerInLoop %v, want %v", tc.mode, got, tc.want)
		}
	}
}

func TestAnAbsentPermissionModeRecordsNothing(t *testing.T) {
	d := newTraceDaemon(t)
	addCharacterizationSession(t, d, "sess-no-mode", protocol.SessionAgentClaude, protocol.SessionStateWorking)
	d.recordReviewerEvidenceFromPermissionMode("sess-no-mode", "  ")
	if _, ok := d.evidenceTable().snapshot("sess-no-mode"); ok {
		t.Fatal("an absent mode created an evidence record")
	}
}

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
	if !strings.Contains(got.Detail, string(sessionstate.ReasonHeartbeatBusy)) {
		t.Fatalf("detail %q does not name the winning clause", got.Detail)
	}
}

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

func TestTheResolveTickDoesNotPublishAnAbsenceOfEvidence(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-no-evidence"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWaitingInput)

	d.recordReviewerEvidenceFromPermissionMode(id, "acceptEdits")
	d.resolveAllSessions(time.Now())

	if state := d.store.Get(id).State; state != protocol.SessionStateWaitingInput {
		t.Fatalf("state %q, want waiting_input untouched", state)
	}
}

func TestARunningClassificationHoldsTheSettle(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-classifying"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)

	now := time.Now()
	d.recordClassifierStarted(id, now)
	d.recordBracketEvidence(id, protocol.StateIdle)
	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "not_busy", At: now})

	d.resolveAllSessions(now.Add(time.Second))

	if state := d.store.Get(id).State; state != protocol.SessionStateWorking {
		t.Fatalf("state %q, want working held while the classifier runs", state)
	}

	d.recordClassifierEvidence(id, protocol.StateWaitingInput, now)
	d.recordClassifierFinished(id)
	d.resolveAllSessions(now.Add(2 * time.Second))

	if state := d.store.Get(id).State; state != protocol.SessionStateWaitingInput {
		t.Fatalf("state %q, want the classifier verdict published", state)
	}
}

func TestAHungClassifierStopsHoldingTheSettle(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-classifier-hung"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)

	now := time.Now()
	d.recordBracketEvidence(id, protocol.StateWorking)
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

func TestAVerdictDoesNotSurviveIntoTheNextTurn(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-cross-turn"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)

	now := time.Now()

	d.recordBracketEvidence(id, protocol.StateIdle)
	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "not_busy", At: now})
	d.recordClassifierEvidence(id, protocol.StateWaitingInput, now)
	d.resolveAllSessions(now.Add(time.Second))

	if state := d.store.Get(id).State; state != protocol.SessionStateWaitingInput {
		t.Fatalf("state %q, want turn A's verdict published", state)
	}

	openedAt := now.Add(2 * time.Second)
	d.recordBracketEvidence(id, protocol.StateWorking)
	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "busy", At: openedAt})
	d.resolveAllSessions(openedAt)

	if state := d.store.Get(id).State; state != protocol.SessionStateWorking {
		t.Fatalf("state %q, want working for turn B", state)
	}

	settledAt := now.Add(10 * time.Second)
	d.recordClassifierStarted(id, settledAt)
	d.recordBracketEvidence(id, protocol.StateIdle)
	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "not_busy", At: settledAt})
	d.resolveAllSessions(settledAt.Add(time.Second))

	if state := d.store.Get(id).State; state != protocol.SessionStateWorking {
		t.Fatalf("state %q, want the settle held for turn B's own verdict", state)
	}

	d.recordClassifierEvidence(id, protocol.StateIdle, settledAt)
	d.recordClassifierFinished(id)
	d.resolveAllSessions(settledAt.Add(2 * time.Second))

	if state := d.store.Get(id).State; state != protocol.SessionStateIdle {
		t.Fatalf("state %q, want turn B's verdict published", state)
	}
}

func TestAVerdictDoesNotSurviveATurnThatNeverPaintedBusy(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-cross-turn-quiet"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)

	now := time.Now()

	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "busy", At: now})
	d.recordBracketEvidence(id, protocol.StateIdle)
	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "not_busy", At: now.Add(time.Second)})
	d.recordClassifierEvidence(id, protocol.StateWaitingInput, now.Add(2*time.Second))
	d.resolveAllSessions(now.Add(2500 * time.Millisecond))

	if state := d.store.Get(id).State; state != protocol.SessionStateWaitingInput {
		t.Fatalf("state %q, want turn A's verdict published", state)
	}

	openedAt := now.Add(3 * time.Second)
	d.recordBracketEvidence(id, protocol.StateWorking)
	d.resolveAllSessions(openedAt)

	if state := d.store.Get(id).State; state != protocol.SessionStateWorking {
		t.Fatalf("state %q, want working for turn B", state)
	}

	settledAt := now.Add(3500 * time.Millisecond)
	d.recordClassifierStarted(id, settledAt)
	d.recordBracketEvidence(id, protocol.StateIdle)
	d.resolveAllSessions(settledAt)

	if state := d.store.Get(id).State; state != protocol.SessionStateWorking {
		t.Fatalf("state %q, want the settle held: turn A's verdict is not turn B's answer", state)
	}
}

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
	if got.Heartbeat.Claim != sessionstate.ClaimSettled {
		t.Fatalf("latest heartbeat %+v, want the settled frame", got.Heartbeat)
	}
}

func TestEvidenceIsNotRecordedForAnUnknownSession(t *testing.T) {
	d := newTraceDaemon(t)

	d.recordPTYEvidence("ghost", pty.Observation{Source: pty.SourceHeartbeat, Claim: "busy", At: time.Now()})
	d.recordBracketEvidence("ghost", protocol.StateWorking)
	d.recordStopFacts("ghost", true, false)
	d.recordReviewerEvidence("ghost", true)
	d.recordProcessEvidence("ghost", true)

	if _, ok := d.evidenceTable().snapshot("ghost"); ok {
		t.Fatal("an unknown session created an evidence entry")
	}
}

// Not a synctest bubble: the removal parks on a mutex, and a mutex wait is invisible to one.
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
	go func() {
		d.closeSession(id, store.SessionClose{By: store.SessionClosedByUser})
	}()
	time.Sleep(20 * time.Millisecond)
	close(release)
	<-wrote

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
	} else {
		t.Fatal("the approval title left the heartbeat busy, which hides the approval")
	}
	if !got.LastBusyAt.IsZero() {
		t.Fatal("an approval title advanced LastBusyAt; it is not a running turn")
	}

	res := sessionstate.Resolve(got, sessionstate.PolicyFor(string(protocol.SessionAgentCodex)), at)
	if res.State != protocol.SessionStatePendingApproval {
		t.Fatalf("resolved %q (%s), want pending_approval", res.State, res.Reason)
	}
}

func stateChangesSince(t *testing.T, d *Daemon, cursor int64) (int, int64) {
	t.Helper()
	events, err := d.store.BusEventsSince(cursor, 1000)
	if err != nil {
		t.Fatalf("BusEventsSince: %v", err)
	}
	count := 0
	for _, event := range events {
		if event.Name == FactSessionStateChanged {
			count++
		}
		cursor = event.Seq
	}
	return count, cursor
}

// Measured over 8.4 production days before the gate: session.state.changed was 73.7% of the whole bus log (233,497 of 316,721 facts), and 81.6% of consecutive facts for one session landed within a second of the previous one.
func TestTheEvidenceTickIsSilentWhileTheTurnKeepsRunning(t *testing.T) {
	d := newTraceDaemon(t)
	t.Cleanup(d.stopEventBus)
	id := "sess-tick-quiet"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)
	d.recordBracketEvidence(id, protocol.StateWorking)

	base := time.Now()
	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "busy", Detail: "⠐ working", At: base})
	d.resolveAllSessions(base)
	_, cursor := stateChangesSince(t, d, 0)

	for tick := 1; tick <= 10; tick++ {
		now := base.Add(time.Duration(tick) * time.Second)
		age := 1200 * time.Millisecond
		if tick%2 == 1 {
			age = 1800 * time.Millisecond
		}
		d.recordPTYEvidence(id, pty.Observation{
			Source: pty.SourceHeartbeat,
			Claim:  "busy",
			Detail: "⠐ working",
			At:     now.Add(-age),
		})
		d.resolveAllSessions(now)
	}

	if published, _ := stateChangesSince(t, d, cursor); published != 0 {
		t.Fatalf("the tick published %d state changes for a turn that never moved", published)
	}
}

func TestTheEvidenceTickPublishesAReasonThatMovesOnItsOwn(t *testing.T) {
	d := newTraceDaemon(t)
	t.Cleanup(d.stopEventBus)
	id := "sess-tick-news"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)
	d.recordBracketEvidence(id, protocol.StateWorking)

	base := time.Now()
	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "busy", Detail: "⠐ working", At: base})
	d.resolveAllSessions(base)
	_, cursor := stateChangesSince(t, d, 0)

	d.recordCompactionEvidence(id, true)
	now := base.Add(2 * time.Second)
	d.resolveAllSessions(now)

	published, cursor := stateChangesSince(t, d, cursor)
	if published != 1 {
		t.Fatalf("compaction published %d state changes, want 1: the reason moved", published)
	}
	if got := d.stateReasons().get(id); got != string(sessionstate.ReasonCompacting) {
		t.Fatalf("reason %q, want %q", got, sessionstate.ReasonCompacting)
	}
	if state := d.store.Get(id).State; state != protocol.SessionStateWorking {
		t.Fatalf("state %q, want working: compaction is work", state)
	}

	d.recordProcessEvidence(id, true)
	d.resolveAllSessions(now.Add(time.Second))

	if published, _ = stateChangesSince(t, d, cursor); published == 0 {
		t.Fatal("an exited process published nothing")
	}
	if state := d.store.Get(id).State; state != protocol.SessionStateIdle {
		t.Fatalf("state %q, want idle after the process exited", state)
	}
}
