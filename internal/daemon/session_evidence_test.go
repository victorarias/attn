package daemon

import (
	"net"
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

// Shadow mode's contract: the tick records what the resolver would have said and
// changes nothing.
func TestTheResolveTickRecordsADisagreementWithoutApplyingIt(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-shadow"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateIdle)

	now := time.Now()
	d.recordPTYEvidence(id, pty.Observation{
		Source: pty.SourceHeartbeat,
		Claim:  "busy",
		Detail: "⠐ working",
		At:     now,
	})

	d.resolveAllSessions(now)

	got := onlyObservation(t, d, id)
	if got.Source != stateSourceResolver {
		t.Fatalf("source %q, want %q", got.Source, stateSourceResolver)
	}
	if got.Outcome != statetrace.OutcomeObserved {
		t.Fatalf("outcome %q, want observed — the resolver must not write state yet", got.Outcome)
	}
	if got.Claim != string(protocol.SessionStateWorking) {
		t.Fatalf("claim %q, want working: a fresh heartbeat outranks the stored idle", got.Claim)
	}
	if got.Reason != string(sessionstate.ReasonHeartbeatFresh) {
		t.Fatalf("reason %q, want %q", got.Reason, sessionstate.ReasonHeartbeatFresh)
	}
	if state := d.store.Get(id).State; state != protocol.SessionStateIdle {
		t.Fatalf("the shadow tick changed state to %q", state)
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
// Phase 1b wired the Notification hook to the trace alone, which left the
// resolver blind to the strongest approval signal either agent emits — fine
// while the resolver was in shadow mode, wrong once it decides state.
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
