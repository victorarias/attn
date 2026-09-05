package daemon

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/sessionstate"
	"github.com/victorarias/attn/internal/store"
)

func TestASessionWhoseEvidenceStoppedMovingIsReportedStuck(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-stuck"
	addCharacterizationSession(t, d, id, protocol.SessionAgentCodex, protocol.SessionStateWorking)

	d.recordBracketEvidence(id, protocol.StateWorking)
	d.recordBracketEvidence(id, protocol.StateIdle)
	now := time.Now()

	policy := sessionstate.PolicyFor(string(protocol.SessionAgentCodex))
	d.resolveAllSessions(now.Add(policy.StuckAfter + time.Second))

	session := d.store.Get(id)
	if session.State != protocol.SessionStateUnknown {
		t.Fatalf("state %q, want unknown after total evidence silence", session.State)
	}
	if got := protocol.Deref(d.sessionForBroadcast(session).StateReason); got != string(sessionstate.ReasonStuck) {
		t.Fatalf("state_reason %q, want stuck: an unknown badge with no reason is the dead end it replaces", got)
	}
}

func TestAnIdleSessionStillReportingIsNotStuck(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-idle-quiet"
	addCharacterizationSession(t, d, id, protocol.SessionAgentCodex, protocol.SessionStateWorking)

	now := time.Now()
	d.recordBracketEvidence(id, protocol.StateWorking)
	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "busy", At: now})
	d.recordBracketEvidence(id, protocol.StateIdle)

	policy := sessionstate.PolicyFor(string(protocol.SessionAgentCodex))
	for i := 1; i <= 3; i++ {
		at := now.Add(time.Duration(i) * policy.StuckAfter)
		d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "not_busy", At: at})
		d.resolveAllSessions(at.Add(time.Second))
	}

	if state := d.store.Get(id).State; state != protocol.SessionStateIdle {
		t.Fatalf("state %q, want idle: a reporting agent is not a stuck one", state)
	}
}

func TestTheReasonIsOmittedForStatesTheResolverDoesNotOwn(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-unowned"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)

	now := time.Now()
	d.recordBracketEvidence(id, protocol.StateWorking)
	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "busy", At: now})
	d.resolveAllSessions(now.Add(time.Second))

	if got := protocol.Deref(d.sessionForBroadcast(d.store.Get(id)).StateReason); got == "" {
		t.Fatal("no reason recorded for a resolver-owned state, so the omission below proves nothing")
	}

	d.applyState(sessionStateChange{
		sessionID: id,
		state:     string(protocol.SessionStateRecoverable),
		cause:     liveSignal{},
	})

	if got := protocol.Deref(d.sessionForBroadcast(d.store.Get(id)).StateReason); got != "" {
		t.Fatalf("state_reason %q on a recoverable session, want none", got)
	}
}

func TestAReasonChangeReachesClientsWithoutAStateChange(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-reason-delta"
	addCharacterizationSession(t, d, id, protocol.SessionAgentCodex, protocol.SessionStateWorking)

	now := time.Now()
	d.applyState(sessionStateChange{
		sessionID: id,
		state:     string(protocol.SessionStateUnknown),
		cause:     liveSignal{},
	})
	if state := d.store.Get(id).State; state != protocol.SessionStateUnknown {
		t.Fatalf("state %q, want unknown before the reason changes", state)
	}

	capture := captureBroadcasts(d)

	d.recordBracketEvidence(id, protocol.StateWorking)
	d.recordBracketEvidence(id, protocol.StateIdle)
	policy := sessionstate.PolicyFor(string(protocol.SessionAgentCodex))
	at := now.Add(policy.StuckAfter + time.Second)
	d.resolveAllSessions(at)

	if state := d.store.Get(id).State; state != protocol.SessionStateUnknown {
		t.Fatalf("state %q, want unknown still: this test is about the reason, not the state", state)
	}
	var reason string
	for _, event := range capture.snapshot() {
		if event.Session != nil && event.Session.ID == id {
			reason = protocol.Deref(event.Session.StateReason)
		}
	}
	if reason != string(sessionstate.ReasonStuck) {
		t.Fatalf("broadcast state_reason %q, want stuck: the explanation never left the daemon", reason)
	}

	quiet := captureBroadcasts(d)
	d.resolveAllSessions(at.Add(time.Second))
	d.resolveAllSessions(at.Add(2 * time.Second))
	for _, event := range quiet.snapshot() {
		if event.Session != nil && event.Session.ID == id {
			t.Fatal("an unchanged reason broadcast anyway")
		}
	}
}

func TestTheReasonIsForgottenWithTheSession(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-reason-closed"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)

	now := time.Now()
	d.recordBracketEvidence(id, protocol.StateWorking)
	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "busy", At: now})
	d.resolveAllSessions(now.Add(time.Second))
	if d.stateReasons().get(id) == "" {
		t.Fatal("no reason was recorded, so the cleanup below proves nothing")
	}

	d.closeSession(id, store.SessionClose{By: store.SessionClosedByUser})

	if got := d.stateReasons().get(id); got != "" {
		t.Fatalf("reason %q survived the session", got)
	}
}
