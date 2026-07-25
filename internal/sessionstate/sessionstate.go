// Package sessionstate resolves what an agent session is doing from the
// evidence collected about it.
//
// It exists because attn's state was previously decided by whoever wrote last.
// Every source — hooks, the screen scraper, the stop classifier, the worker
// poll — called the store directly with a state name, and a session got stuck
// whenever the source that would have moved it on never fired. There was no
// arbitration to fix, because there was no arbitration.
//
// The fix is structural rather than a better heuristic: sources record what they
// saw, this package decides what that means, and a tick re-runs the decision so
// evidence expires. Every clause below that can hold a session in a state
// depends on evidence that either refreshes or ages out, which is what makes a
// stuck state impossible rather than merely unlikely.
//
// The package is pure — no daemon, store, or IO imports — so the rules are
// table-tested directly instead of by standing up a daemon.
package sessionstate

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// Source names where an observation came from. The resolver treats sources
// differently, so this is not merely diagnostic.
type Source string

const (
	// SourceHeartbeat is the agent's own OSC 0 title glyph: a level, refreshed
	// while its turn runs.
	SourceHeartbeat Source = "heartbeat"
	// SourceBracket is a hook opening or closing a turn or a tool call. The
	// primary level — the only signal that survives the multi-second title
	// silence claude produces in the middle of a blocking tool call.
	SourceBracket Source = "hook_bracket"
	// SourceHarnessEvent is a one-shot harness announcement: an approval
	// request, Claude's Notification hook.
	SourceHarnessEvent Source = "harness_event"
	// SourceClassifier is the stop-time verdict on a settled turn.
	SourceClassifier Source = "classifier"
	// SourceScreen is the rendered-screen scrape. Copilot only: it has no
	// harness signals to read. It is deleted for claude and codex, not demoted
	// to a fallback, because it manufactures approvals from ordinary prose.
	SourceScreen Source = "screen"
	// SourceProcess is the PTY process itself. A level with no expiry: an exited
	// process does not become un-exited.
	SourceProcess Source = "process"
)

// Claim is what an observation asserts. Deliberately not a protocol state name:
// a source reports what it saw, and only the resolver names a state. "The turn
// is running" and "the session is working" are different statements, and
// collapsing them is what let a heartbeat masquerade as an applied state.
type Claim string

const (
	// ClaimBusy: the agent's turn is running right now.
	ClaimBusy Claim = "busy"
	// ClaimSettled: the turn is over. It says nothing about why it ended — an
	// approval, a question, and a finished turn all settle.
	ClaimSettled Claim = "settled"
	// ClaimApprovalPending: the agent asked for permission and has not been
	// answered.
	ClaimApprovalPending Claim = "approval_pending"
	// ClaimNeedsInput: the agent is waiting on the user specifically, as opposed
	// to having simply finished.
	ClaimNeedsInput Claim = "needs_input"
	// ClaimIdle: the agent finished and wants nothing.
	ClaimIdle Claim = "idle"
	// ClaimExited: the process is gone.
	ClaimExited Claim = "exited"
)

// Observation is one recorded piece of evidence.
type Observation struct {
	Source     Source
	Claim      Claim
	Detail     string
	ObservedAt time.Time
}

// Evidence is everything the resolver may read about one session. The daemon
// owns it and mutates it as observations arrive; the resolver only reads.
//
// Levels (Heartbeat, TurnOpen, ToolOpen, Screen, Process) describe a condition
// that holds until it changes. Edges (LastHarnessEvent, LastClassifier) are
// one-shot facts that stay until superseded.
type Evidence struct {
	// Heartbeat is the most recent title-glyph observation. Its freshness is
	// what bounds how long a stale bracket may lie.
	Heartbeat *Observation
	// LastHarnessEvent is the most recent approval/notification edge.
	LastHarnessEvent *Observation
	// LastClassifier is the most recent stop-time verdict.
	LastClassifier *Observation
	// Screen is the scraped-screen level. Copilot only.
	Screen *Observation
	// Process is the PTY lifecycle level.
	Process *Observation

	// TurnOpen: a prompt was submitted and no Stop has closed it.
	TurnOpen bool
	// ToolOpen: a tool call started and has not reported completion.
	ToolOpen bool
	// BackgroundWork: the turn yielded with asynchronous work outstanding, so
	// it will auto-resume. Reported as a fact on the Stop payload.
	BackgroundWork bool
	// PendingCron: the turn yielded with a scheduled wakeup that will resume it.
	PendingCron bool
	// ReviewerInLoop: something other than the user answers approval requests —
	// claude's permission classifier, codex's auto_review guardian. It does not
	// suppress an approval state; it decides how long that state must hold
	// before it is worth showing anyone.
	ReviewerInLoop bool

	// LastBusyAt is when the heartbeat last said the turn was running. Staleness
	// is measured from here rather than from the latest heartbeat: claude blips
	// its not-busy glyph mid-turn (between tool calls, and while a foreground
	// tool is still running), so treating any non-busy frame as an immediate
	// settle would flip a healthy open turn to idle. Zero means the agent has
	// never reported being busy, which is not the same as having gone quiet.
	LastBusyAt time.Time

	// LastMovement is when any evidence last changed. A session whose evidence
	// has stopped moving entirely is stuck, which is a distinct condition from
	// any state it might be reported in.
	LastMovement time.Time
}

// Policy holds the timing constants. They are per-agent and measured, so they
// are an input rather than package constants: a table test states the timing it
// is testing instead of inheriting it.
type Policy struct {
	// HeartbeatTTL is how long a busy heartbeat keeps a session working on its
	// own. It must exceed the agent's title repaint interval by enough margin to
	// survive a PTY read that batches chunks under load.
	HeartbeatTTL time.Duration
	// StaleAfter is the heartbeat silence that closes a bracket whose closing
	// hook never arrived. It must exceed the longest silence the agent produces
	// mid-turn, or a slow tool call reads as a finished turn.
	StaleAfter time.Duration
	// StuckAfter is total evidence silence — no source of any kind — after which
	// the session is reported stuck rather than left in whatever it last showed.
	StuckAfter time.Duration
	// GuardianDwell is how long an approval must hold before it is published
	// when a reviewer is in the loop. It is not a delay on genuine approval
	// requests: with no reviewer the dwell is zero.
	GuardianDwell time.Duration
}

// Measured on claude 2.1.220 and codex 0.145.0 driven through a real PTY.
// Claude repaints its title about once a second and goes silent for up to ~3.5s
// in the middle of a blocking foreground tool call; codex repaints about ten
// times a second and never goes quiet mid-turn. Hence claude's TTL carries ~55%
// margin over its repaint interval and codex's carries ~5x.
const (
	claudeHeartbeatTTL = 1500 * time.Millisecond
	codexHeartbeatTTL  = 500 * time.Millisecond
	defaultStaleAfter  = 4 * time.Second
	defaultStuckAfter  = 90 * time.Second
	// guardianDwell is the round trip a guardian needs to answer in the user's
	// place. Measured: 90ms for claude's permission classifier, low seconds for
	// codex's auto_review. 60s is deliberately far above both — the cost of
	// waiting is a late notification, the cost of not waiting is a yellow flash
	// on every tool call of an unattended run.
	guardianDwell = 60 * time.Second
)

// PolicyFor returns the timing for an agent. An agent with no measured numbers
// gets the conservative end of each: a TTL short enough not to hold a session
// working on a stale glyph, and a stale window long enough not to close a
// bracket early.
func PolicyFor(agent string) Policy {
	policy := Policy{
		HeartbeatTTL:  codexHeartbeatTTL,
		StaleAfter:    defaultStaleAfter,
		StuckAfter:    defaultStuckAfter,
		GuardianDwell: guardianDwell,
	}
	if agent == string(protocol.SessionAgentClaude) {
		policy.HeartbeatTTL = claudeHeartbeatTTL
	}
	return policy
}

// Reason names why the resolver reached a state. It is what `attn state explain`
// shows, and it is the difference between "the color is wrong" and a diagnosis.
type Reason string

const (
	ReasonProcessExited    Reason = "process_exited"
	ReasonHeartbeatFresh   Reason = "heartbeat_fresh"
	ReasonApprovalOpen     Reason = "approval_open"
	ReasonCronPending      Reason = "cron_pending"
	ReasonBracketOpen      Reason = "bracket_open"
	ReasonBracketStale     Reason = "bracket_stale"
	ReasonBackgroundWork   Reason = "background_work"
	ReasonClassifierVerdict Reason = "classifier_verdict"
	ReasonScreen           Reason = "screen"
	ReasonStuck            Reason = "stuck"
	ReasonNoEvidence       Reason = "no_evidence"
)

// Resolution is the resolver's answer.
type Resolution struct {
	State  protocol.SessionState
	Reason Reason
	// Detail carries the winning observation's detail so a diagnosis does not
	// require re-reading the evidence table.
	Detail string
}

// Resolve decides what a session is doing. Pure: same evidence and same clock
// always give the same answer.
//
// The clauses are ordered, first match wins, and the order encodes trust rather
// than recency. A fresh heartbeat outranks an open approval because the agent
// cannot be blocked on the user while its turn is visibly running — that
// combination means the approval was already answered and its closing edge was
// lost.
func Resolve(e Evidence, policy Policy, now time.Time) Resolution {
	// A process that exited is terminal. Nothing below can outrank it, and no
	// amount of stale evidence should keep a dead session colored as alive.
	if e.Process != nil && e.Process.Claim == ClaimExited {
		return Resolution{State: protocol.SessionStateIdle, Reason: ReasonProcessExited, Detail: e.Process.Detail}
	}

	// The clause that rescues a lost turn-open hook: the agent is visibly
	// running, whatever the brackets say.
	if fresh(e.Heartbeat, ClaimBusy, now, policy.HeartbeatTTL) {
		return Resolution{State: protocol.SessionStateWorking, Reason: ReasonHeartbeatFresh, Detail: e.Heartbeat.Detail}
	}

	if e.LastHarnessEvent != nil && e.LastHarnessEvent.Claim == ClaimApprovalPending {
		return Resolution{
			State:  protocol.SessionStatePendingApproval,
			Reason: ReasonApprovalOpen,
			Detail: e.LastHarnessEvent.Detail,
		}
	}

	// A scheduled wakeup will resume this session without anyone doing
	// anything, so it is parked rather than waiting on a person.
	if e.PendingCron {
		return Resolution{State: protocol.SessionStateScheduled, Reason: ReasonCronPending}
	}

	// An open bracket says work is outstanding. Whether to believe it is exactly
	// what the heartbeat is for: a bracket whose closing hook was lost would
	// otherwise hold the session working for the rest of its life.
	if e.TurnOpen || e.ToolOpen {
		if !heartbeatSilentFor(e, now, policy.StaleAfter) {
			return Resolution{State: protocol.SessionStateWorking, Reason: ReasonBracketOpen}
		}
		// The bracket is stale. Fall through to the settled clauses below, which
		// decide *how* it settled — but remember it, so a settle with no verdict
		// is reported as an un-stick rather than as an absence of evidence.
		return settled(e, ReasonBracketStale)
	}

	// Background work auto-resumes the turn, so the session is still working
	// even though the turn yielded. Deliberate: the alternative flickers a
	// session to idle and back for every backgrounded command.
	if e.BackgroundWork {
		return Resolution{State: protocol.SessionStateWorking, Reason: ReasonBackgroundWork}
	}

	if r, ok := classifierVerdict(e); ok {
		return r
	}

	if e.Screen != nil {
		if state, ok := screenState(e.Screen.Claim); ok {
			return Resolution{State: state, Reason: ReasonScreen, Detail: e.Screen.Detail}
		}
	}

	// Nothing has moved at all. That is its own diagnosis, and reporting it is
	// the whole point: a stuck session used to be indistinguishable from a
	// correctly-quiet one.
	if !e.LastMovement.IsZero() && now.Sub(e.LastMovement) > policy.StuckAfter {
		return Resolution{State: protocol.SessionStateUnknown, Reason: ReasonStuck}
	}

	return Resolution{State: protocol.SessionStateUnknown, Reason: ReasonNoEvidence}
}

// settled resolves a turn that is over, preferring the classifier's verdict and
// falling back to the reason that got us here.
func settled(e Evidence, fallback Reason) Resolution {
	if r, ok := classifierVerdict(e); ok {
		return r
	}
	return Resolution{State: protocol.SessionStateIdle, Reason: fallback}
}

func classifierVerdict(e Evidence) (Resolution, bool) {
	if e.LastClassifier == nil {
		return Resolution{}, false
	}
	switch e.LastClassifier.Claim {
	case ClaimNeedsInput:
		return Resolution{
			State:  protocol.SessionStateWaitingInput,
			Reason: ReasonClassifierVerdict,
			Detail: e.LastClassifier.Detail,
		}, true
	case ClaimIdle:
		return Resolution{
			State:  protocol.SessionStateIdle,
			Reason: ReasonClassifierVerdict,
			Detail: e.LastClassifier.Detail,
		}, true
	default:
		return Resolution{}, false
	}
}

func screenState(claim Claim) (protocol.SessionState, bool) {
	switch claim {
	case ClaimBusy:
		return protocol.SessionStateWorking, true
	case ClaimApprovalPending:
		return protocol.SessionStatePendingApproval, true
	case ClaimNeedsInput:
		return protocol.SessionStateWaitingInput, true
	case ClaimIdle, ClaimSettled:
		return protocol.SessionStateIdle, true
	default:
		return "", false
	}
}

// DwellFor is how long a transition into state must hold before it is published.
//
// It is keyed on who is being asked first. With a guardian in the loop, an
// approval request is addressed to the guardian, and showing it to the user
// immediately produces a flash of attention-demanding color on every tool call
// of an unattended run. With no guardian the user *is* the reviewer, so the
// dwell is zero and a genuine request is not delayed by a millisecond.
func DwellFor(state protocol.SessionState, e Evidence, policy Policy) time.Duration {
	if state == protocol.SessionStatePendingApproval && e.ReviewerInLoop {
		return policy.GuardianDwell
	}
	return 0
}

// fresh reports whether o makes claim and is recent enough to still be believed.
func fresh(o *Observation, claim Claim, now time.Time, ttl time.Duration) bool {
	return o != nil && o.Claim == claim && now.Sub(o.ObservedAt) <= ttl
}

// heartbeatSilentFor reports whether the agent has stopped saying it is busy for
// longer than d.
//
// It reads LastBusyAt, not the latest heartbeat. A single non-busy frame is not
// a settle: claude blips its idle glyph mid-turn, so closing the bracket on that
// frame would reintroduce the false-settle path the measurements ruled out. Only
// the absence of busy frames for a full window counts, and an explicit settle
// arrives as its own fact — the Stop hook closing the bracket.
//
// An agent that has never reported being busy is not silent: an agent with no
// harness signals must not have its brackets closed out from under it.
func heartbeatSilentFor(e Evidence, now time.Time, d time.Duration) bool {
	if e.LastBusyAt.IsZero() {
		return false
	}
	return now.Sub(e.LastBusyAt) > d
}
