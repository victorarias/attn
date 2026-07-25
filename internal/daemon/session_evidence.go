package daemon

import (
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/sessionstate"
	"github.com/victorarias/attn/internal/statetrace"
)

// The evidence table: what each source has said about a session, kept so the
// resolver can weigh them instead of the last writer winning.
//
// This is shadow mode. The table is filled and resolved on a tick, and the
// resolution is recorded in the state trace — it does not reach applyState. The
// point is to have a live witness that the resolver agrees with the current
// behavior (and a record of exactly where it does not) before it takes over.
// The flip is a separate change.

// evidenceTickInterval is how often every session's evidence is re-resolved.
// The tick is what makes evidence expire: without it a resolution would only be
// recomputed when a source spoke, which is precisely the case that fails when a
// source stops speaking.
const evidenceTickInterval = time.Second

// sessionEvidenceTable holds one evidence record per live session.
type sessionEvidenceTable struct {
	mu       sync.Mutex
	sessions map[string]*sessionstate.Evidence
}

func newSessionEvidenceTable() *sessionEvidenceTable {
	return &sessionEvidenceTable{sessions: make(map[string]*sessionstate.Evidence)}
}

// updateIf mutates one session's evidence, creating the record on first use, but
// only when admit says the session is live. It stamps LastMovement, so stuck
// detection cannot drift out of sync with the writes it is watching.
//
// admit runs while holding the table's lock, which is the whole point: the
// caller's liveness check and the write have to be one atomic step. Removal
// deletes the store row and then forgets the table, so with admission inside the
// lock the two possible interleavings are "the writer wins and its entry is then
// forgotten" and "removal wins and the writer is refused". Checking liveness
// outside the lock leaves a third: the writer passes the check, removal deletes
// and forgets, and the writer then recreates an entry for an id nothing will
// ever clean up again. That is the leak #668 fixed in the trace ring.
func (t *sessionEvidenceTable) updateIf(sessionID string, at time.Time, admit func() bool, mutate func(*sessionstate.Evidence)) {
	if t == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if admit != nil && !admit() {
		return
	}
	evidence := t.sessions[sessionID]
	if evidence == nil {
		evidence = &sessionstate.Evidence{}
		t.sessions[sessionID] = evidence
	}
	mutate(evidence)
	evidence.LastMovement = at
}

// snapshot returns a copy of one session's evidence, or false when nothing has
// been recorded for it. The copy matters: the resolver must not read a table
// that is being mutated underneath it.
func (t *sessionEvidenceTable) snapshot(sessionID string) (sessionstate.Evidence, bool) {
	if t == nil {
		return sessionstate.Evidence{}, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	evidence := t.sessions[sessionID]
	if evidence == nil {
		return sessionstate.Evidence{}, false
	}
	return *evidence, true
}

func (t *sessionEvidenceTable) sessionIDs() []string {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	ids := make([]string, 0, len(t.sessions))
	for id := range t.sessions {
		ids = append(ids, id)
	}
	return ids
}

func (t *sessionEvidenceTable) forget(sessionID string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.sessions, sessionID)
}

// evidenceRecordGateHook runs inside the evidence table's lock, between the
// live-row check and the write. Tests only: it is the seam where a concurrent
// removal would have to interleave for an orphan entry to appear.
var evidenceRecordGateHook func(sessionID string)

// recordEvidence is the single write path into the evidence table. Every source
// goes through it so the liveness gate cannot be forgotten at one call site.
func (d *Daemon) recordEvidence(sessionID string, at time.Time, mutate func(*sessionstate.Evidence)) {
	d.evidenceTable().updateIf(sessionID, at, func() bool {
		live := d.store != nil && d.store.Get(sessionID) != nil
		// The hook runs after the check on purpose: check-then-write is the
		// sequence that leaks when the two are not atomic.
		if hook := evidenceRecordGateHook; hook != nil {
			hook(sessionID)
		}
		return live
	}, mutate)
}

func (d *Daemon) evidenceTable() *sessionEvidenceTable {
	d.sessionEvidenceOnce.Do(func() {
		d.sessionEvidence = newSessionEvidenceTable()
	})
	return d.sessionEvidence
}

// recordPTYEvidence files an observation from the PTY layer. Sources that only
// report a level (the heartbeat) and sources that report an edge (approval) land
// in different fields, because the resolver ages them differently.
func (d *Daemon) recordPTYEvidence(sessionID string, obs pty.Observation) {
	at := obs.At
	if at.IsZero() {
		at = time.Now()
	}
	switch obs.Source {
	case pty.SourceHeartbeat:
		// Codex announces an approval in its title, so the heartbeat channel
		// carries three claims rather than two. It is still a level: the
		// approval title holds, unrepainted, until the prompt is answered.
		if obs.Claim == "approval" {
			d.recordEvidence(sessionID, at, func(e *sessionstate.Evidence) {
				e.LastHarnessEvent = &sessionstate.Observation{
					Source:     sessionstate.SourceHarnessEvent,
					Claim:      sessionstate.ClaimApprovalPending,
					Detail:     obs.Detail,
					ObservedAt: at,
				}
				e.Heartbeat = &sessionstate.Observation{
					Source:     sessionstate.SourceHeartbeat,
					Claim:      sessionstate.ClaimSettled,
					Detail:     obs.Detail,
					ObservedAt: at,
				}
			})
			return
		}
		claim := sessionstate.ClaimSettled
		if obs.Claim == "busy" {
			claim = sessionstate.ClaimBusy
		}
		d.recordEvidence(sessionID, at, func(e *sessionstate.Evidence) {
			e.Heartbeat = &sessionstate.Observation{
				Source:     sessionstate.SourceHeartbeat,
				Claim:      claim,
				Detail:     obs.Detail,
				ObservedAt: at,
			}
			// LastBusyAt only advances on a busy frame: staleness is measured
			// from the last time the turn was seen running, not from the last
			// time the agent said anything. A non-busy frame mid-turn is a blip,
			// not a settle.
			if claim == sessionstate.ClaimBusy {
				e.LastBusyAt = at
			}
		})
	case pty.SourceApproval:
		d.recordEvidence(sessionID, at, func(e *sessionstate.Evidence) {
			e.LastHarnessEvent = &sessionstate.Observation{
				Source:     sessionstate.SourceHarnessEvent,
				Claim:      approvalClaim(obs.Claim),
				Detail:     obs.Detail,
				ObservedAt: at,
			}
		})
	case pty.SourceScreen:
		d.recordEvidence(sessionID, at, func(e *sessionstate.Evidence) {
			e.Screen = &sessionstate.Observation{
				Source:     sessionstate.SourceScreen,
				Claim:      stateClaim(obs.Claim),
				Detail:     obs.Detail,
				ObservedAt: at,
			}
		})
	}
}

// recordBracketEvidence files a hook opening or closing a turn. The brackets are
// the primary level: they are the only signal that survives the multi-second
// title silence claude produces inside a blocking tool call.
func (d *Daemon) recordBracketEvidence(sessionID, state string) {
	at := time.Now()
	d.recordEvidence(sessionID, at, func(e *sessionstate.Evidence) {
		switch state {
		case protocol.StateWorking:
			e.TurnOpen = true
		case protocol.StateIdle, protocol.StateWaitingInput:
			e.TurnOpen = false
			e.ToolOpen = false
		case protocol.StatePendingApproval:
			e.LastHarnessEvent = &sessionstate.Observation{
				Source:     sessionstate.SourceHarnessEvent,
				Claim:      sessionstate.ClaimApprovalPending,
				ObservedAt: at,
			}
		}
	})
}

// recordClassifierEvidence files a stop-time verdict.
func (d *Daemon) recordClassifierEvidence(sessionID, state string, observedAt time.Time) {
	if observedAt.IsZero() {
		observedAt = time.Now()
	}
	claim := stateClaim(state)
	if claim == "" {
		return
	}
	d.recordEvidence(sessionID, observedAt, func(e *sessionstate.Evidence) {
		e.LastClassifier = &sessionstate.Observation{
			Source:     sessionstate.SourceClassifier,
			Claim:      claim,
			ObservedAt: observedAt,
		}
	})
}

// recordStopFacts files what the Stop hook reported about why a turn yielded.
// These are the facts the CLI used to collapse into a state string before they
// crossed the socket, which is why the resolver could not see them.
func (d *Daemon) recordStopFacts(sessionID string, backgroundWork, pendingCron bool) {
	d.recordEvidence(sessionID, time.Now(), func(e *sessionstate.Evidence) {
		e.BackgroundWork = backgroundWork
		e.PendingCron = pendingCron
	})
}

// recordReviewerEvidence files who answers approval requests. It is a level, not
// an edge: it holds until the agent reports a different mode.
func (d *Daemon) recordReviewerEvidence(sessionID, permissionMode string) {
	mode := strings.TrimSpace(permissionMode)
	if mode == "" {
		return
	}
	inLoop := mode != "default"
	d.recordEvidence(sessionID, time.Now(), func(e *sessionstate.Evidence) {
		e.ReviewerInLoop = inLoop
	})
}

// Notification types claude reports. Both are typed fields on the hook payload,
// which is why attn reads them rather than the English message beside them.
const (
	notifyPermissionPrompt = "permission_prompt"
	notifyIdlePrompt       = "idle_prompt"
)

// recordNotificationEvidence files claude's Notification hook.
//
// The two types are not two flavors of one signal and do not land in the same
// place. permission_prompt is a leading edge — the agent asked a question and is
// blocked on the answer — so it becomes an approval claim. idle_prompt is a late
// confirmation that the agent is parked at its prompt, 60s after a settle, and
// it deliberately does not become a claim at all: it fires for finished turns
// and for questions alike, so it can say "not working" but not what instead.
func (d *Daemon) recordNotificationEvidence(sessionID, notificationType, message string) {
	at := time.Now()
	switch strings.TrimSpace(notificationType) {
	case notifyPermissionPrompt:
		d.recordEvidence(sessionID, at, func(e *sessionstate.Evidence) {
			e.LastHarnessEvent = &sessionstate.Observation{
				Source:     sessionstate.SourceHarnessEvent,
				Claim:      sessionstate.ClaimApprovalPending,
				Detail:     message,
				ObservedAt: at,
			}
		})
	case notifyIdlePrompt:
		d.recordEvidence(sessionID, at, func(e *sessionstate.Evidence) {
			e.PromptIdleAt = at
		})
	}
}

// recordProcessEvidence files the PTY lifecycle. An exited process is terminal
// and outranks every other clause.
func (d *Daemon) recordProcessEvidence(sessionID string, exited bool) {
	if !exited {
		return
	}
	at := time.Now()
	d.recordEvidence(sessionID, at, func(e *sessionstate.Evidence) {
		e.Process = &sessionstate.Observation{
			Source:     sessionstate.SourceProcess,
			Claim:      sessionstate.ClaimExited,
			ObservedAt: at,
		}
	})
}

// runEvidenceResolveLoop re-resolves every session on a tick and records what
// the resolver would have said. Shadow mode: nothing here writes state.
func (d *Daemon) runEvidenceResolveLoop() {
	ticker := time.NewTicker(evidenceTickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			d.resolveAllSessions(time.Now())
		}
	}
}

func (d *Daemon) resolveAllSessions(now time.Time) {
	for _, sessionID := range d.evidenceTable().sessionIDs() {
		session := d.store.Get(sessionID)
		if session == nil {
			// The session is gone; so is any reason to keep resolving it.
			d.evidenceTable().forget(sessionID)
			continue
		}
		evidence, ok := d.evidenceTable().snapshot(sessionID)
		if !ok {
			continue
		}
		resolution := sessionstate.Resolve(evidence, sessionstate.PolicyFor(string(session.Agent)), now)
		d.traceResolution(sessionID, session.State, resolution)
	}
}

// traceResolution records the resolver's verdict and how it compares to the
// state the session is actually in. Only a disagreement is worth a row: a tick
// that agrees every second would bury everything else in the ring.
func (d *Daemon) traceResolution(sessionID string, current protocol.SessionState, resolution sessionstate.Resolution) {
	if resolution.State == current {
		return
	}
	d.recordStateObservation(sessionID, statetrace.Observation{
		Source:  stateSourceResolver,
		Claim:   string(resolution.State),
		Detail:  resolution.Detail,
		Outcome: statetrace.OutcomeObserved,
		Reason:  string(resolution.Reason),
	})
}

// approvalClaim reads the approval resolver's vocabulary. It reports the state
// it wants rather than an approval-specific claim, so an approval that cleared
// arrives as some other state entirely.
func approvalClaim(claim string) sessionstate.Claim {
	if claim == protocol.StatePendingApproval {
		return sessionstate.ClaimApprovalPending
	}
	return sessionstate.ClaimSettled
}

// stateClaim maps a protocol state name onto what the source actually observed.
// Sources that speak in state names are the ones this plan is unwinding; until
// they are converted, the translation lives here rather than being spread across
// every call site.
func stateClaim(state string) sessionstate.Claim {
	switch state {
	case protocol.StateWorking:
		return sessionstate.ClaimBusy
	case protocol.StateWaitingInput:
		return sessionstate.ClaimNeedsInput
	case protocol.StateIdle:
		return sessionstate.ClaimIdle
	case protocol.StatePendingApproval:
		return sessionstate.ClaimApprovalPending
	default:
		return ""
	}
}
