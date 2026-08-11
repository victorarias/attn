package daemon

import (
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/classifier"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/sessionstate"
	"github.com/victorarias/attn/internal/statetrace"
)

// The evidence table: what each source has said about a session, so the resolver
// can weigh them instead of the last writer winning. Sources write here and
// nowhere else, and the tick re-justifies state from live evidence every second,
// so no state gets stuck when its sources go quiet.

// evidenceTickInterval is how often every session's evidence is re-resolved.
// The tick is what makes evidence expire when a source stops speaking.
const evidenceTickInterval = time.Second

// sessionEvidenceTable holds one evidence record per live session.
type sessionEvidenceTable struct {
	mu       sync.Mutex
	sessions map[string]*sessionstate.Evidence
}

func newSessionEvidenceTable() *sessionEvidenceTable {
	return &sessionEvidenceTable{sessions: make(map[string]*sessionstate.Evidence)}
}

// updateIf mutates one session's evidence when admit says it is live, stamping
// LastMovement. admit runs INSIDE the table's lock: checked outside, a writer
// could pass liveness, lose to a removal, and recreate an orphan entry.
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
// been recorded — a copy, so the resolver never reads a table being mutated.
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

// evidenceRecordGateHook (tests only) runs inside the table's lock, between the
// live-row check and the write — the seam a removal must interleave into.
var evidenceRecordGateHook func(sessionID string)

// recordEvidence is the single write path into the evidence table. Every source
// goes through it so the liveness gate cannot be forgotten at one call site.
func (d *Daemon) recordEvidence(sessionID string, at time.Time, mutate func(*sessionstate.Evidence)) {
	d.evidenceTable().updateIf(sessionID, at, func() bool {
		live := d.store != nil && d.store.Get(sessionID) != nil
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

func (d *Daemon) dwellGate() *dwellGate {
	d.sessionDwellOnce.Do(func() {
		d.sessionDwell = newDwellGate()
	})
	return d.sessionDwell
}

// recordPTYEvidence files an observation from the PTY layer; levels (heartbeat)
// and edges (approval) land in different fields and age differently.
func (d *Daemon) recordPTYEvidence(sessionID string, obs pty.Observation) {
	at := obs.At
	if at.IsZero() {
		at = time.Now()
	}
	switch obs.Source {
	case pty.SourceHeartbeat:
		// Codex announces an approval in its title — still a level: the title
		// holds, unrepainted, until the prompt is answered.
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
		// A title nobody can read is still someone painting: it stamps
		// LastMovement (updateIf does that for every write) and leaves the level
		// alone. Filed as settled instead, it would retire an open turn.
		if obs.Claim == "unclassified" {
			d.recordEvidence(sessionID, at, func(*sessionstate.Evidence) {})
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
			// LastBusyAt only advances on a busy frame: staleness is measured from
			// the last time the turn was seen running, not the last time the agent
			// said anything.
			if claim == sessionstate.ClaimBusy {
				e.LastBusyAt = at
			}
		})
	}
}

// recordBracketEvidence files a hook opening or closing a turn. The brackets
// are the only signal that survives claude's multi-second title silence inside
// a blocking tool call.
func (d *Daemon) recordBracketEvidence(sessionID, state string) {
	at := time.Now()
	d.recordEvidence(sessionID, at, func(e *sessionstate.Evidence) {
		switch state {
		case protocol.StateWorking:
			e.TurnOpen = true
			e.TurnEverOpened = true
			// A stale verdict judged the previous turn; left in the table the
			// resolver would report it the moment this turn settles.
			e.LastClassifier = nil
			// A turn cannot open while the previous one is blocked on a person, so
			// an edge still sitting here was answered.
			if e.LastHarnessEvent != nil {
				switch e.LastHarnessEvent.Claim {
				case sessionstate.ClaimApprovalPending,
					sessionstate.ClaimNeedsInput,
					sessionstate.ClaimStopFailed,
					sessionstate.ClaimTurnAborted:
					e.LastHarnessEvent = nil
				}
			}
			// Backstop for a lost PostCompact: compaction cannot still be running
			// while a turn is being worked.
			e.Compacting = false
			// These describe how the LAST turn yielded; left behind, outstanding
			// background work pins the session working with only silence to unpin it.
			e.BackgroundWork = false
			e.PendingCron = false
		case protocol.StateIdle:
			e.TurnOpen = false
			e.ToolOpen = false
		case protocol.StateWaitingInput:
			// A question to the user is filed like an approval request — blocked on
			// a person — and retired the same way. The brackets alone cannot express
			// it: closing them resolves to idle and loses the question.
			e.TurnOpen = false
			e.ToolOpen = false
			e.LastHarnessEvent = &sessionstate.Observation{
				Source:     sessionstate.SourceHarnessEvent,
				Claim:      sessionstate.ClaimNeedsInput,
				ObservedAt: at,
			}
		case protocol.StatePendingApproval:
			e.LastHarnessEvent = &sessionstate.Observation{
				Source:     sessionstate.SourceHarnessEvent,
				Claim:      sessionstate.ClaimApprovalPending,
				ObservedAt: at,
			}
		}
	})
}

// recordTranscriptEvidence files what the transcript watcher read. Copilot only:
// it has no hooks, so its transcript is where its brackets come from, phrased as
// states.
func (d *Daemon) recordTranscriptEvidence(sessionID, state, detail string, at time.Time) {
	d.recordBracketEvidence(sessionID, state)
	d.traceStateEvidence(
		sessionID,
		stateOrigin{source: stateSourceTranscript, detail: detail, observedAt: at},
		state,
	)
}

// recordTurnAbortedEvidence files the transcript's record that the user halted
// the turn. Brackets are closed too — an open one outlives the edge and resolves
// as stuck mid-turn. abortedAt (agent-dated) and observedAt (read time) stay
// separate so a late-read halt is outranked by later busy frames.
func (d *Daemon) recordTurnAbortedEvidence(sessionID, detail string, abortedAt, observedAt time.Time) {
	if observedAt.IsZero() {
		observedAt = time.Now()
	}
	at := abortedAt
	if at.IsZero() {
		at = observedAt
	}
	d.recordEvidence(sessionID, observedAt, func(e *sessionstate.Evidence) {
		e.TurnOpen = false
		e.ToolOpen = false
		e.LastHarnessEvent = &sessionstate.Observation{
			Source:     sessionstate.SourceHarnessEvent,
			Claim:      sessionstate.ClaimTurnAborted,
			Detail:     detail,
			ObservedAt: at,
		}
	})
}

// recordTurnBracketClosedEvidence closes the brackets and says nothing else: for
// copilot abandoning a turn, where a halt would invent a user action.
func (d *Daemon) recordTurnBracketClosedEvidence(sessionID string, at time.Time) {
	if at.IsZero() {
		at = time.Now()
	}
	d.recordEvidence(sessionID, at, func(e *sessionstate.Evidence) {
		e.TurnOpen = false
		e.ToolOpen = false
	})
}

// recordClassifierEvidence files a stop-time verdict.
func (d *Daemon) recordClassifierEvidence(sessionID, state string, observedAt time.Time) {
	if observedAt.IsZero() {
		observedAt = time.Now()
	}
	claim := classifierClaim(state)
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
func (d *Daemon) recordStopFacts(sessionID string, backgroundWork, pendingCron bool) {
	d.recordEvidence(sessionID, time.Now(), func(e *sessionstate.Evidence) {
		e.BackgroundWork = backgroundWork
		e.PendingCron = pendingCron
	})
}

// recordReviewerEvidence files who answers approval requests — a level, holding
// until a different arrangement is reported. Two sources: the spawn route
// (codex's only one) and claude's per-turn permission mode.
func (d *Daemon) recordReviewerEvidence(sessionID string, inLoop bool) {
	d.recordEvidence(sessionID, time.Now(), func(e *sessionstate.Evidence) {
		e.ReviewerInLoop = inLoop
	})
}

// recordReviewerEvidenceFromPermissionMode files claude's reported mode. An
// absent mode (older CLI) is not a report, and a mode from an agent whose mode
// does not govern approvals must be ignored: codex sends `default` as filler,
// which would retire the spawn-time fact on its first turn.
func (d *Daemon) recordReviewerEvidenceFromPermissionMode(sessionID, permissionMode string) {
	mode := strings.TrimSpace(permissionMode)
	if mode == "" {
		return
	}
	if !permissionModeGovernsApprovals(d.sessionAgent(sessionID)) {
		return
	}
	d.recordReviewerEvidence(sessionID, mode != "default")
}

// permissionModeGovernsApprovals: claude's mode decides who answers approvals;
// the others state their arrangement at launch and never revise it.
func permissionModeGovernsApprovals(agent protocol.SessionAgent) bool {
	return agent == protocol.SessionAgentClaude
}

func (d *Daemon) sessionAgent(sessionID string) protocol.SessionAgent {
	if d.store == nil {
		return ""
	}
	session := d.store.Get(sessionID)
	if session == nil {
		return ""
	}
	return session.Agent
}

// Notification types claude reports as typed fields on the hook payload.
const (
	notifyPermissionPrompt = "permission_prompt"
	notifyIdlePrompt       = "idle_prompt"
)

// recordNotificationEvidence files claude's Notification hook.
// permission_prompt is a leading edge and becomes an approval claim.
// idle_prompt deliberately becomes no claim: it fires for finished turns and
// questions alike, so it can say "not working" but not what instead.
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

// recordStopFailureEvidence files claude's StopFailure hook (turn ended on an
// API error) as a harness edge, not a stop: nothing to classify, the session is
// blocked on a person, and the agent going busy again retires it.
func (d *Daemon) recordStopFailureEvidence(sessionID, errorType, message string) {
	at := time.Now()
	detail := strings.TrimSpace(errorType)
	if message = strings.TrimSpace(message); message != "" {
		detail = detail + ": " + message
	}
	d.recordEvidence(sessionID, at, func(e *sessionstate.Evidence) {
		e.LastHarnessEvent = &sessionstate.Observation{
			Source:     sessionstate.SourceHarnessEvent,
			Claim:      sessionstate.ClaimStopFailed,
			Detail:     detail,
			ObservedAt: at,
		}
	})
}

// recordCompactionEvidence files claude's PreCompact/PostCompact pair as a
// level; no other source reports compaction.
func (d *Daemon) recordCompactionEvidence(sessionID string, active bool) {
	d.recordEvidence(sessionID, time.Now(), func(e *sessionstate.Evidence) {
		e.Compacting = active
	})
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

// recordClassifierStarted marks a classification as running, so a settle waits
// for the verdict instead of publishing idle and correcting it seconds later.
func (d *Daemon) recordClassifierStarted(sessionID string, at time.Time) {
	d.recordEvidence(sessionID, at, func(e *sessionstate.Evidence) {
		e.ClassifyingSince = at
	})
	// Suspend auto-settle until the verdict lands; the fire path repeats this
	// check to close the race with a timer that already left the map.
	d.cancelAutoSettle(sessionID, "classification started")
}

// recordClassifierFinished clears that mark. It must run on every exit from a
// classification, verdict or not — one that applies nothing is exactly when the
// session has to settle on its own.
func (d *Daemon) recordClassifierFinished(sessionID string) {
	d.recordEvidence(sessionID, time.Now(), func(e *sessionstate.Evidence) {
		e.ClassifyingSince = time.Time{}
	})
	// No transition is guaranteed here (a background-working verdict can leave
	// `working` persisted), so re-evaluate auto-settle explicitly.
	if session := d.store.Get(sessionID); session != nil {
		d.syncAutoSettle(sessionID, string(session.State))
	}
}

// runEvidenceResolveLoop re-resolves every session on a tick and publishes the
// verdict, including sessions whose sources have gone quiet.
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
			d.dwellGate().clear(sessionID)
			continue
		}
		evidence, ok := d.evidenceTable().snapshot(sessionID)
		if !ok {
			continue
		}
		policy := sessionstate.PolicyFor(string(session.Agent))
		resolution := sessionstate.Resolve(evidence, policy, now)
		d.publishResolution(sessionID, session.State, resolution, sessionstate.DwellFor(resolution.State, evidence, policy), now)
	}
}

// resolverOwnedStates are the states the resolver decides; the rest describe
// lifecycle, not agent activity (`recoverable` is the revive path's, and
// resolving it would let a stale process observation stomp it). `launching` IS
// owned: no source writes state directly, so excluding it strands the session.
var resolverOwnedStates = map[protocol.SessionState]bool{
	protocol.SessionStateLaunching:       true,
	protocol.SessionStateWorking:         true,
	protocol.SessionStatePendingApproval: true,
	protocol.SessionStateWaitingInput:    true,
	protocol.SessionStateIdle:            true,
	protocol.SessionStateScheduled:       true,
	protocol.SessionStateUnknown:         true,
}

// publishResolution applies the resolver's verdict, or records why it did not:
// Hold means evidence is still arriving, no-evidence means nothing recorded,
// and an unowned current state means the resolver had no standing.
func (d *Daemon) publishResolution(sessionID string, current protocol.SessionState, resolution sessionstate.Resolution, dwell time.Duration, now time.Time) {
	// Only a hold is traced: it is bounded and looks stuck from outside, while the
	// other non-applications would log once per session per second forever. The
	// specific reason is traced, not the word "hold".
	if resolution.Hold {
		d.traceResolutionSkip(sessionID, resolution, string(resolution.Reason))
		return
	}
	// ReasonNoEvidence is the absence of a finding; publishing it would repaint
	// every session the table has not heard about yet. Stuck is the opposite.
	if resolution.Reason == sessionstate.ReasonNoEvidence {
		return
	}
	// An external driver owns its session's state through sequenced report_*
	// calls; without this veto the tick would overwrite a current report.
	if run := d.store.GetAgentDriverRun(sessionID); run.RunID != "" {
		if session := d.store.Get(sessionID); session != nil && d.pluginDriverReportsState(session.Agent) {
			d.traceResolutionSkip(sessionID, resolution, "plugin_driver_owns_state")
			return
		}
	}
	if !resolverOwnedStates[current] || resolution.State == current {
		// No transition: drop the dwell wait so a later one cannot inherit a clock
		// that started before an unrelated transition.
		d.dwellGate().clear(sessionID)
		// Recorded even without a move (an already-`unknown` session that goes
		// silent still needs its tooltip updated), broadcast only on delta.
		if d.recordStateReason(sessionID, resolution) && resolverOwnedStates[current] {
			d.broadcastSessionStateChanged(sessionID)
		}
		return
	}
	// Last gate on purpose: everything above decides what is true, this decides
	// whether it has been true long enough to show.
	if !d.dwellGate().ready(sessionID, resolution.State, dwell, now) {
		d.traceResolutionSkip(sessionID, resolution, "dwell")
		return
	}
	// Below the dwell, not above: recording the reason for a transition still
	// serving its dwell publishes a self-contradicting pair (`working` alongside
	// `approval_open`), witnessed on a live session.
	d.recordStateReason(sessionID, resolution)
	d.applyState(sessionStateChange{
		sessionID: sessionID,
		state:     string(resolution.State),
		cause:     resolverObservation{},
		origin: stateOrigin{
			source: stateSourceResolver,
			detail: resolutionDetail(resolution),
		},
	})
}

// resolutionDetail is what `attn state explain` shows for a resolver row: the
// winning clause's reason first, its own detail after.
func resolutionDetail(resolution sessionstate.Resolution) string {
	if resolution.Detail == "" {
		return string(resolution.Reason)
	}
	return string(resolution.Reason) + ": " + resolution.Detail
}

// traceResolutionSkip records a tick that changed nothing on purpose. Without
// it a held session and an unresolved one look identical in the trace.
func (d *Daemon) traceResolutionSkip(sessionID string, resolution sessionstate.Resolution, reason string) {
	d.recordStateObservation(sessionID, statetrace.Observation{
		Source:  stateSourceResolver,
		Claim:   string(resolution.State),
		Detail:  resolution.Detail,
		Outcome: statetrace.OutcomeSkipped,
		Reason:  reason,
	})
}

// classifierClaim reads a verdict. Anything outside the three answers — the
// `unknown` a failed classification publishes included — is no verdict at all.
func classifierClaim(state string) sessionstate.Claim {
	switch state {
	case protocol.StateWaitingInput:
		return sessionstate.ClaimNeedsInput
	case protocol.StateIdle:
		return sessionstate.ClaimIdle
	case classifier.VerdictParked:
		return sessionstate.ClaimParked
	default:
		return ""
	}
}
