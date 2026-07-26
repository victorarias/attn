package daemon

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/sessionstate"
)

// stalenessSession stages a session that settled at stateSince and has never
// been looked at since.
func stalenessSession(t *testing.T, d *Daemon, id string, state protocol.SessionState, stateSince time.Time) {
	t.Helper()
	directory := t.TempDir()
	workspaceID := "workspace-" + id
	addTestWorkspace(d, workspaceID, directory)
	stamp := stateSince.Format(time.RFC3339Nano)
	d.store.Add(&protocol.Session{
		ID:             id,
		Label:          id,
		Agent:          protocol.SessionAgentClaude,
		Directory:      directory,
		State:          state,
		StateSince:     stamp,
		StateUpdatedAt: stamp,
		LastSeen:       stamp,
	})
	d.associateSessionWithWorkspace(id, workspaceID)
}

func stalePolicy() sessionstate.Policy {
	return sessionstate.PolicyFor(string(protocol.SessionAgentClaude))
}

// The point of the mark: a result Victor asked for, finished, and never came
// back to. Nothing else in the system remembers that nobody looked.
func TestAnIdleResultNobodyReadGoesStale(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-unread"
	policy := stalePolicy()
	settledAt := time.Now()
	stalenessSession(t, d, id, protocol.SessionStateIdle, settledAt)

	d.refreshIdleStaleness(d.store.Get(id), policy, settledAt.Add(policy.IdleStaleAfter/2))
	if protocol.Deref(d.sessionForBroadcast(d.store.Get(id)).IdleStale) {
		t.Fatal("marked stale inside the window: a result is not forgotten the moment it lands")
	}

	d.refreshIdleStaleness(d.store.Get(id), policy, settledAt.Add(policy.IdleStaleAfter+time.Second))
	if !protocol.Deref(d.sessionForBroadcast(d.store.Get(id)).IdleStale) {
		t.Fatal("an idle result nobody read never went stale")
	}
}

// Reading it is what stops the clock. Without this the mark would fire on every
// finished session regardless of whether Victor had already seen the answer,
// which is noise rather than attention.
func TestAReadResultNeverGoesStale(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-read"
	policy := stalePolicy()
	settledAt := time.Now()
	stalenessSession(t, d, id, protocol.SessionStateIdle, settledAt)

	d.handleSessionVisualized(id)

	d.refreshIdleStaleness(d.store.Get(id), policy, settledAt.Add(2*policy.IdleStaleAfter))
	if protocol.Deref(d.sessionForBroadcast(d.store.Get(id)).IdleStale) {
		t.Fatal("a session read after it settled went stale anyway")
	}
}

// Reading a session is a fact about that session, not about which one happens to
// be on screen right now. Switching away is what normally follows reading a
// finished result, and it must not undo having read it.
func TestReadingASessionSurvivesSwitchingAwayFromIt(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-read-then-left"
	policy := stalePolicy()
	settledAt := time.Now()
	stalenessSession(t, d, id, protocol.SessionStateIdle, settledAt)
	stalenessSession(t, d, "sess-elsewhere", protocol.SessionStateWorking, settledAt)

	d.handleSessionVisualized(id)
	d.handleSessionVisualized("sess-elsewhere")

	d.refreshIdleStaleness(d.store.Get(id), policy, settledAt.Add(2*policy.IdleStaleAfter))
	if protocol.Deref(d.sessionForBroadcast(d.store.Get(id)).IdleStale) {
		t.Fatal("a session that was read went stale because the user moved on to another one")
	}
}

// A read that happened before the turn finished says nothing about the result:
// Victor looked, walked away, and the agent finished afterwards.
func TestAReadFromBeforeTheResultDoesNotCount(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-read-early"
	policy := stalePolicy()
	settledAt := time.Now()
	stalenessSession(t, d, id, protocol.SessionStateIdle, settledAt)

	d.markSessionRead(id, settledAt.Add(-time.Hour))

	d.refreshIdleStaleness(d.store.Get(id), policy, settledAt.Add(policy.IdleStaleAfter+time.Second))
	if !protocol.Deref(d.sessionForBroadcast(d.store.Get(id)).IdleStale) {
		t.Fatal("a read from before the turn finished counted as having seen its result")
	}
}

// The session on screen is being read continuously. A turn that finishes while
// Victor is watching it produces no `session_visualized` of its own, so without
// this the session he is literally looking at would go stale.
func TestTheSessionOnScreenIsReadContinuously(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-onscreen"
	policy := stalePolicy()
	settledAt := time.Now()
	stalenessSession(t, d, id, protocol.SessionStateIdle, settledAt)

	// Selected long before the turn ended, and never re-reported since.
	d.setSelectedSession(id)
	d.markSessionRead(id, settledAt.Add(-time.Hour))

	d.refreshIdleStaleness(d.store.Get(id), policy, settledAt.Add(policy.IdleStaleAfter+time.Second))
	if protocol.Deref(d.sessionForBroadcast(d.store.Get(id)).IdleStale) {
		t.Fatal("the session on screen went stale while it was on screen")
	}
}

// Staleness is about a finished result. A session that is still working has
// produced nothing to miss, however long it has been running.
func TestOnlyAnIdleSessionCanGoStale(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-working"
	policy := stalePolicy()
	startedAt := time.Now()
	stalenessSession(t, d, id, protocol.SessionStateWorking, startedAt)

	d.refreshIdleStaleness(d.store.Get(id), policy, startedAt.Add(10*policy.IdleStaleAfter))
	if protocol.Deref(d.sessionForBroadcast(d.store.Get(id)).IdleStale) {
		t.Fatal("a long-running turn was marked stale: it has produced nothing to miss")
	}
}

// The mark rides on session broadcasts, which otherwise only happen when the
// state moves — and staleness is precisely the case where it does not. Without
// its own delta broadcast the daemon would know a result had been forgotten and
// no client would ever hear it.
func TestGoingStaleReachesClientsWithoutAStateChange(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-stale-delta"
	policy := stalePolicy()
	settledAt := time.Now()
	stalenessSession(t, d, id, protocol.SessionStateIdle, settledAt)

	capture := captureBroadcasts(d)
	d.refreshIdleStaleness(d.store.Get(id), policy, settledAt.Add(policy.IdleStaleAfter+time.Second))

	var delivered bool
	for _, event := range capture.snapshot() {
		if event.Session != nil && event.Session.ID == id && protocol.Deref(event.Session.IdleStale) {
			delivered = true
		}
	}
	if !delivered {
		t.Fatal("the mark never left the daemon")
	}

	// And it does not become a parade: the tick recomputes it every second.
	quiet := captureBroadcasts(d)
	d.refreshIdleStaleness(d.store.Get(id), policy, settledAt.Add(policy.IdleStaleAfter+2*time.Second))
	for _, event := range quiet.snapshot() {
		if event.Session != nil && event.Session.ID == id {
			t.Fatal("an unchanged mark broadcast anyway")
		}
	}
}

// A stale session Victor finally opens is no longer stale, and the client has to
// be told — otherwise the mark is a one-way door.
func TestReadingAStaleSessionClearsTheMark(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-stale-cleared"
	policy := stalePolicy()
	settledAt := time.Now()
	stalenessSession(t, d, id, protocol.SessionStateIdle, settledAt)

	now := settledAt.Add(policy.IdleStaleAfter + time.Second)
	d.refreshIdleStaleness(d.store.Get(id), policy, now)
	if !protocol.Deref(d.sessionForBroadcast(d.store.Get(id)).IdleStale) {
		t.Fatal("not stale to begin with, so clearing it below proves nothing")
	}

	capture := captureBroadcasts(d)
	d.handleSessionVisualized(id)
	d.refreshIdleStaleness(d.store.Get(id), policy, now.Add(time.Second))

	if protocol.Deref(d.sessionForBroadcast(d.store.Get(id)).IdleStale) {
		t.Fatal("the mark survived the user reading the session")
	}
	var cleared bool
	for _, event := range capture.snapshot() {
		if event.Session != nil && event.Session.ID == id && !protocol.Deref(event.Session.IdleStale) {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("clients were never told the session had been read")
	}
}

// The resolve tick is what makes the mark appear on its own. Everything above
// calls refreshIdleStaleness directly; this is the wire that makes it run.
func TestTheResolveTickMarksStaleness(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-tick-stale"
	policy := stalePolicy()
	settledAt := time.Now()
	stalenessSession(t, d, id, protocol.SessionStateIdle, settledAt)

	// A turn that ran and finished, with the agent still painting not-busy frames
	// at the prompt: idle, and demonstrably not stuck.
	d.recordBracketEvidence(id, protocol.StateWorking)
	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "busy", At: settledAt})
	d.recordBracketEvidence(id, protocol.StateIdle)
	at := settledAt.Add(policy.IdleStaleAfter + time.Second)
	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "not_busy", At: at})

	d.resolveAllSessions(at.Add(time.Second))

	if !protocol.Deref(d.sessionForBroadcast(d.store.Get(id)).IdleStale) {
		t.Fatalf("the tick resolved the session (state=%s) without ever asking whether its result had been read", d.store.Get(id).State)
	}
}

// Per-session runtime state with a cleanup path, like every other map hanging
// off the daemon.
func TestTheStalenessMarkIsForgottenWithTheSession(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-stale-closed"
	policy := stalePolicy()
	settledAt := time.Now()
	stalenessSession(t, d, id, protocol.SessionStateIdle, settledAt)
	d.refreshIdleStaleness(d.store.Get(id), policy, settledAt.Add(policy.IdleStaleAfter+time.Second))
	if !d.readTimes().isStale(id) {
		t.Fatal("never marked stale, so the cleanup below proves nothing")
	}

	d.dropSessionRecord(id)

	if d.readTimes().isStale(id) {
		t.Fatal("the mark survived the session")
	}
	if !d.readTimes().lastRead(id).IsZero() {
		t.Fatal("the read time survived the session")
	}
}
