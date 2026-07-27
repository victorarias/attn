package daemon

import (
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/sessionstate"
	"github.com/victorarias/attn/internal/statetrace"
)

// The evidence table: what each source has said about a session, kept so the
// resolver can weigh them instead of the last writer winning.
//
// Sources write here and nowhere else. The tick resolves the table and is what
// publishes state, which is the point of the whole design: a state now has to be
// re-justified from live evidence every second, so it cannot outlive the
// evidence behind it. That is the structural difference from the edge-triggered
// scheme it replaces, where a state persisted until some source happened to
// contradict it and got stuck forever when none did.

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
// ever clean up again.
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

func (d *Daemon) dwellGate() *dwellGate {
	d.sessionDwellOnce.Do(func() {
		d.sessionDwell = newDwellGate()
	})
	return d.sessionDwell
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
			e.TurnEverOpened = true
			// A verdict judged the turn that just ended. Once the next one opens
			// it is an answer to a question nobody asked any more, and leaving it
			// in the table lets the resolver report it as this turn's state the
			// moment this turn settles.
			e.LastClassifier = nil
			// The same holds for an unanswered approval or question: a turn cannot
			// open while the one before it is still blocked on a person, so an edge
			// still sitting here was answered. This is what retires the question
			// claude's PostToolUse hook answers — it reports `working` the moment
			// the user picks an option.
			if e.LastHarnessEvent != nil {
				switch e.LastHarnessEvent.Claim {
				case sessionstate.ClaimApprovalPending, sessionstate.ClaimNeedsInput:
					e.LastHarnessEvent = nil
				}
			}
			// And for how the last turn yielded. These describe a turn that has
			// ended; a turn is open again, so whatever was going to resume it did.
			// Left behind, outstanding background work pins the session working
			// with nothing but total silence left to unpin it.
			e.BackgroundWork = false
			e.PendingCron = false
		case protocol.StateIdle:
			e.TurnOpen = false
			e.ToolOpen = false
		case protocol.StateWaitingInput:
			// The agent put a question to the user. It is an announcement of the
			// same shape as an approval request — the turn is alive but blocked on a
			// person, and nothing but the user answering ends it — so it is filed as
			// one, and the resolver retires it the same way.
			//
			// Filing it is what lets the hook stop applying state itself. The
			// brackets alone cannot express this: closing them says only that
			// nothing is outstanding, which resolves to idle and loses the question.
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
// it is the one agent with no hooks, so its transcript is where its turn and tool
// brackets come from, and the states its behavior reports are those brackets by
// another name — a turn opening, a turn ending, an approval appearing in the tool
// lifecycle and later leaving it.
//
// The behavior still phrases them as states, and as states they were applied
// directly, which is why several of its rules are written against whatever the
// session currently shows rather than against what it saw. Filed as evidence they
// no longer race the resolver; unpicking the phrasing is the copilot work this
// phase deferred.
func (d *Daemon) recordTranscriptEvidence(sessionID, state, detail string, at time.Time) {
	d.recordBracketEvidence(sessionID, state)
	d.traceStateEvidence(
		sessionID,
		stateOrigin{source: stateSourceTranscript, detail: detail, observedAt: at},
		state,
	)
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
// These are the facts the CLI used to collapse into a state string before they
// crossed the socket, which is why the resolver could not see them.
func (d *Daemon) recordStopFacts(sessionID string, backgroundWork, pendingCron bool) {
	d.recordEvidence(sessionID, time.Now(), func(e *sessionstate.Evidence) {
		e.BackgroundWork = backgroundWork
		e.PendingCron = pendingCron
	})
}

// recordReviewerEvidence files who answers approval requests. It is a level, not
// an edge: it holds until something reports a different arrangement.
//
// It has two sources because the agents differ in what they will tell us. Both
// are launched with a reviewer under the same condition, so the spawn is the
// reliable one and the only one codex has — it reports no permission mode at all.
// Claude's hook then carries the mode on every turn, which is what catches a user
// changing it mid-session.
func (d *Daemon) recordReviewerEvidence(sessionID string, inLoop bool) {
	d.recordEvidence(sessionID, time.Now(), func(e *sessionstate.Evidence) {
		e.ReviewerInLoop = inLoop
	})
}

// reviewerInLoop reads the launch options for the one arrangement that puts
// something other than the user in front of an approval request.
//
// Both agents gate their reviewer on the same option — claude with
// `--permission-mode auto`, codex with `approvals_reviewer=auto_review` — and
// yolo outranks it by removing the approval gate altogether, so a yolo session
// has no reviewer because it has nothing to review.
func reviewerInLoop(opts ptybackend.SpawnOptions) bool {
	return opts.AutoApprove && !opts.YoloMode
}

// recordReviewerEvidenceFromPermissionMode files claude's reported mode.
//
// Two things must not reach the evidence table through here. An absent mode is
// not a report of "no reviewer" — it is an older CLI, or a payload that omitted
// the field — and any mode at all from an agent that does not route approvals
// by permission mode is not a report about approvals. Codex is the second case
// concretely: its hooks send `default` on every turn as a payload filler while
// its actual reviewer comes from the `approvals_reviewer` flag, so believing it
// would retire the spawn-time fact on the first turn of every codex session and
// take the dwell with it.
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

// permissionModeGovernsApprovals reports whether an agent's permission mode is
// what decides who answers its approval requests. Claude's is; the others state
// their arrangement at launch and never revise it.
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

// recordClassifierStarted marks a stop-time classification as running, so a
// settle can wait for its verdict instead of publishing idle and correcting it
// seconds later.
func (d *Daemon) recordClassifierStarted(sessionID string, at time.Time) {
	d.recordEvidence(sessionID, at, func(e *sessionstate.Evidence) {
		e.ClassifyingSince = at
	})
}

// recordClassifierFinished clears that mark. It must run on every exit from a
// classification, verdict or not: a classification that ends without applying
// anything is exactly the case where the session has to settle on its own.
func (d *Daemon) recordClassifierFinished(sessionID string) {
	d.recordEvidence(sessionID, time.Now(), func(e *sessionstate.Evidence) {
		e.ClassifyingSince = time.Time{}
	})
}

// runEvidenceResolveLoop re-resolves every session on a tick and publishes the
// verdict. The tick is what makes evidence expire, and therefore what makes a
// stuck state impossible: a session whose sources have all gone quiet is still
// re-decided every second.
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

// resolverOwnedStates are the states the resolver decides. A state outside this
// set describes the session's lifecycle rather than what its agent is doing, and
// the resolver has no evidence bearing on it: `recoverable` is owned by the revive
// path, which sets it precisely because the worker is gone and its evidence is
// therefore meaningless. Resolving that would let a stale process observation
// stomp a state the resolver knows nothing about.
//
// `launching` is here because evidence is the definition of the agent having
// spoken. It used to be excluded, with the first hook or worker poll writing a
// state directly to end it; now that neither writes state, a session whose agent
// is demonstrably running — hooks arriving, a title being painted — would sit in
// `launching` for the rest of its life. A session registered from the user's own
// terminal, which has no worker to poll at all, would never leave it.
var resolverOwnedStates = map[protocol.SessionState]bool{
	protocol.SessionStateLaunching:       true,
	protocol.SessionStateWorking:         true,
	protocol.SessionStatePendingApproval: true,
	protocol.SessionStateWaitingInput:    true,
	protocol.SessionStateIdle:            true,
	protocol.SessionStateScheduled:       true,
	protocol.SessionStateUnknown:         true,
}

// publishResolution applies the resolver's verdict, or records why it did not.
//
// Three verdicts do not move a session, and the difference between them is the
// diagnosis a wrong color needs: Hold means the evidence is still arriving,
// no-evidence means nothing has been recorded at all, and an unowned current
// state means the resolver was not entitled to an opinion.
func (d *Daemon) publishResolution(sessionID string, current protocol.SessionState, resolution sessionstate.Resolution, dwell time.Duration, now time.Time) {
	// A hold is traced, and it is the only non-application that is. It is the one
	// that is both bounded and surprising: bounded because every hold clause
	// expires, so the rows cannot accumulate, and surprising because a held
	// session looks exactly like a stuck one from outside. The other three
	// non-applications are the steady state of a quiet daemon — they would log
	// once per session per second, forever, and say nothing.
	if resolution.Hold {
		// The specific hold, not the word "hold": settle_grace and
		// awaiting_verdict are held for different reasons and clear on different
		// evidence, and a trace that collapses them cannot tell a session waiting
		// on a classifier from one waiting on an approval announcement.
		d.traceResolutionSkip(sessionID, resolution, string(resolution.Reason))
		return
	}
	// ReasonNoEvidence is not a finding, it is the absence of one, and it
	// resolves to unknown. Publishing it would repaint every session the
	// evidence table has not heard about yet — including ones a hook is about to
	// describe perfectly well.
	//
	// Stuck is the opposite: a session whose evidence stopped moving entirely is
	// the one case where `unknown` is the honest answer rather than a shrug, and
	// leaving it in whatever color it last showed is the failure this whole plan
	// exists to remove.
	if resolution.Reason == sessionstate.ReasonNoEvidence {
		return
	}
	// An external driver owns its session's state through sequenced report_*
	// calls, and its evidence table fills up anyway — a plugin-driven session has
	// a PTY like any other. The veto used to sit on each source's own write path;
	// now that the resolver is the writer, it belongs here, or the tick would
	// overwrite a report the driver considers current.
	if run := d.store.GetAgentDriverRun(sessionID); run.RunID != "" {
		if session := d.store.Get(sessionID); session != nil && d.pluginDriverReportsState(session.Agent) {
			d.traceResolutionSkip(sessionID, resolution, "plugin_driver_owns_state")
			return
		}
	}
	if !resolverOwnedStates[current] || resolution.State == current {
		// No transition is on the table, so nothing is waiting out a dwell.
		// Dropping the wait here is what keeps a later transition from
		// inheriting a clock that started before an unrelated one.
		d.dwellGate().clear(sessionID)
		// The reason is recorded even though the state did not move: a session
		// that is already `unknown` still needs to say why, and the reason is the
		// difference between a badge the user can act on and one that only says
		// "something". It still has to reach the client, and it rides on session
		// broadcasts, which otherwise only happen when the state itself moves —
		// so a session that is already `unknown` and then goes silent would keep
		// its old tooltip while the daemon knew it was stuck. Gated on the delta
		// because the reason is recomputed every tick and almost never changes.
		if d.recordStateReason(sessionID, resolution) && resolverOwnedStates[current] {
			d.broadcastSessionStateChanged(sessionID)
		}
		return
	}
	// Last gate before publication on purpose: everything above decides what is
	// true, and this decides whether it has been true long enough to be worth
	// showing. Putting it earlier would let a transition serve out its dwell and
	// then be discarded for a reason that had nothing to do with timing.
	if !d.dwellGate().ready(sessionID, resolution.State, dwell, now) {
		d.traceResolutionSkip(sessionID, resolution, "dwell")
		return
	}
	// Below the dwell, not above it: the reason explains the state the session is
	// showing, and recording it for a transition still serving out its dwell
	// publishes a pair that contradicts itself — `working`, because a guardian may
	// yet answer, alongside `approval_open`, because one is outstanding. Witnessed
	// on a live guardian session before the move.
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

// resolutionDetail is what `attn state explain` shows for a resolver row. The
// reason is the useful half — it names the clause that won — so it leads, and
// the winning observation's own detail follows when it has one.
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

// classifierClaim reads a verdict. The classifier judges how a finished turn
// ended, so it can only say two things, and anything else — including the
// `unknown` a failed classification used to publish — is no verdict at all
// rather than a third answer.
func classifierClaim(state string) sessionstate.Claim {
	switch state {
	case protocol.StateWaitingInput:
		return sessionstate.ClaimNeedsInput
	case protocol.StateIdle:
		return sessionstate.ClaimIdle
	default:
		return ""
	}
}
