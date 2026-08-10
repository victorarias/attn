// Package sessionstate resolves what an agent session is doing from recorded
// evidence. Every clause that can hold a state depends on evidence that either
// refreshes or ages out, which is what makes a stuck state impossible. Pure —
// no daemon, store, or IO imports — so the rules are table-tested directly.
package sessionstate

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// Source names where an observation came from; the resolver treats sources
// differently, so this is not merely diagnostic.
type Source string

const (
	// SourceHeartbeat is the agent's OSC 0 title glyph: a level, refreshed while
	// its turn runs.
	SourceHeartbeat Source = "heartbeat"
	// SourceBracket is a turn/tool hook — the only signal that survives claude's
	// mid-tool-call title silence.
	SourceBracket      Source = "hook_bracket"
	SourceHarnessEvent Source = "harness_event"
	SourceClassifier   Source = "classifier"
	// SourceProcess is the PTY process itself: a level with no expiry.
	SourceProcess Source = "process"
)

// Claim is what an observation asserts — deliberately not a protocol state
// name: a source reports what it saw, and only the resolver names a state.
type Claim string

const (
	ClaimBusy            Claim = "busy"
	ClaimSettled         Claim = "settled" // the turn is over, saying nothing about why
	ClaimApprovalPending Claim = "approval_pending"
	ClaimNeedsInput      Claim = "needs_input"
	ClaimIdle            Claim = "idle"
	// ClaimParked: a yielded turn is waiting on its own background work. Only the
	// background-work clause consumes it.
	ClaimParked Claim = "parked"
	ClaimExited Claim = "exited"
	// ClaimStopFailed: the turn was cut off by an API error rather than asking
	// anything — distinct from ClaimNeedsInput.
	ClaimStopFailed Claim = "stop_failed"
	// ClaimTurnAborted: the user halted the turn. No agent announces it, so it is
	// read out of the transcript and leaves no answer to judge.
	ClaimTurnAborted Claim = "turn_aborted"
)

// Observation is one recorded piece of evidence.
type Observation struct {
	Source     Source
	Claim      Claim
	Detail     string
	ObservedAt time.Time
}

// Evidence is everything the resolver may read about one session; the daemon
// owns and mutates it, the resolver only reads. Levels (Heartbeat, TurnOpen,
// ToolOpen, Process) hold until they change; edges (LastHarnessEvent,
// LastClassifier) are one-shot facts that stay until superseded.
type Evidence struct {
	// Heartbeat is the most recent title-glyph observation; its freshness bounds
	// how long a stale bracket may lie.
	Heartbeat        *Observation
	LastHarnessEvent *Observation
	LastClassifier   *Observation
	Process          *Observation

	TurnOpen bool
	// TurnEverOpened separates "settled" from "has not started yet" — a booting
	// agent paints title frames (codex flickers a busy one before its prompt).
	TurnEverOpened bool
	ToolOpen       bool
	// BackgroundWork: the turn yielded with async work outstanding and will
	// auto-resume.
	BackgroundWork bool
	// PendingCron: the turn yielded with a scheduled wakeup. Evidence the turn is
	// over, not about whether the user is wanted.
	PendingCron bool
	// Compacting: between PreCompact and PostCompact — work that paints no frames
	// and opens no turn, so nothing else here can see it. Measured at 26s.
	Compacting bool
	// ReviewerInLoop: something other than the user answers approvals. It does
	// not suppress the approval state, only how long it holds before being shown.
	ReviewerInLoop bool

	// LastBusyAt is when the heartbeat last said the turn was running; zero means
	// never. Staleness is measured from here, not the latest heartbeat: claude
	// blips its not-busy glyph mid-turn, so any non-busy frame read as a settle
	// would flip a healthy open turn to idle.
	LastBusyAt time.Time

	// PromptIdleAt is when the harness last confirmed the agent sitting at its
	// prompt. It carries no claim about why, but it is an independent witness the
	// agent is not working — the one thing a lost Stop hook hides.
	PromptIdleAt time.Time

	// ClassifyingSince is when a stop-time classification started, zero when none
	// runs: "settled and idle" vs "settled and still finding out".
	ClassifyingSince time.Time

	// LastMovement is when any evidence last changed; a frozen table means a
	// stuck session, distinct from any state it might be reported in.
	LastMovement time.Time
}

// Policy holds the timing constants: per-agent and measured, so an input rather
// than package constants.
type Policy struct {
	// HeartbeatTTL is a precedence window, not a liveness one: it must be short,
	// or a busy frame suppresses the approval/question edges announced exactly
	// when the agent stops painting.
	HeartbeatTTL time.Duration
	// HeartbeatSettleAfter is how long busy frames must have stopped before their
	// absence reads as a settle — sized against the worst repaint gap.
	HeartbeatSettleAfter time.Duration
	// StaleAfter is the heartbeat silence that closes a bracket whose closing hook
	// never arrived; it must exceed the longest mid-turn silence.
	StaleAfter time.Duration
	StuckAfter time.Duration
	// SettleGrace is how long past StaleAfter a stale bracket holds instead of
	// asserting idle, waiting for a late explanation.
	SettleGrace time.Duration
	// ClassifierTimeout bounds how long a running classification may hold a
	// settle; a classifier that never returns must not freeze a color.
	ClassifierTimeout time.Duration
	// GuardianDwell is how long an approval holds before publishing when a
	// reviewer is in the loop; with no reviewer the dwell is zero.
	GuardianDwell time.Duration
}

// Measured on claude 2.1.220 and codex 0.145.0 through a real PTY: claude
// repaints its title ~1/s and goes silent up to ~3.5s inside a blocking tool
// call; codex repaints ~10/s and never goes quiet mid-turn. Claude's TTL
// carries ~55% margin over its repaint interval, codex's ~5x.
const (
	claudeHeartbeatTTL = 1500 * time.Millisecond
	codexHeartbeatTTL  = 500 * time.Millisecond
	// Measured: claude repaints every ~1.92s during a `/compact`, past the 1.5s
	// TTL; 5s clears that with margin for PTY read batching.
	defaultHeartbeatSettleAfter = 5 * time.Second
	// Consulted only when a closing hook was lost. A minute is far past any
	// measured mid-turn silence (claude's worst ~3.5s).
	defaultStaleAfter = 60 * time.Second
	defaultStuckAfter = 90 * time.Second
	// Measured 90ms for claude's permission classifier, low seconds for codex's
	// auto_review; 60s is far above both — not waiting means a yellow flash on
	// every tool call of an unattended run.
	guardianDwell = 60 * time.Second
	// Bounded on purpose: holding forever reproduces the stuck color it avoids,
	// and codex has no idle_prompt to unstick it.
	defaultSettleGrace = 4 * time.Second
	// Generous on purpose: overrunning costs one visible settle a late verdict
	// corrects; undershooting reintroduces flicker.
	defaultClassifierTimeout = 30 * time.Second
	// A shell pane's heartbeat is the foreground process group on the 1s
	// keepalive; 2.5s covers one missed poll plus worker RPC latency. A shell has
	// no approval edges, so this is sized for steadiness, not precedence.
	shellHeartbeatTTL = 2500 * time.Millisecond
)

// PolicyFor returns the timing for an agent; an unmeasured one gets the
// conservative end of each constant.
func PolicyFor(agent string) Policy {
	policy := Policy{
		HeartbeatTTL:         codexHeartbeatTTL,
		HeartbeatSettleAfter: defaultHeartbeatSettleAfter,

		StaleAfter:        defaultStaleAfter,
		StuckAfter:        defaultStuckAfter,
		GuardianDwell:     guardianDwell,
		SettleGrace:       defaultSettleGrace,
		ClassifierTimeout: defaultClassifierTimeout,
	}
	if agent == string(protocol.SessionAgentClaude) {
		policy.HeartbeatTTL = claudeHeartbeatTTL
	}
	if agent == string(protocol.SessionAgentShell) {
		policy.HeartbeatTTL = shellHeartbeatTTL
	}
	return policy
}

// Reason names why the resolver reached a state; shown by `attn state explain`.
type Reason string

const (
	ReasonProcessExited Reason = "process_exited"
	// ReasonHeartbeatBusy covers every believable busy frame, however recently it
	// was painted: the windows it crosses decide precedence and settling, not
	// whether the agent is running.
	ReasonHeartbeatBusy     Reason = "heartbeat_busy"
	ReasonApprovalOpen      Reason = "approval_open"
	ReasonQuestionOpen      Reason = "question_open"
	ReasonCronPending       Reason = "cron_pending"
	ReasonBracketOpen       Reason = "bracket_open"
	ReasonPromptIdle        Reason = "prompt_idle"
	ReasonBracketStale      Reason = "bracket_stale"
	ReasonHeartbeatSettled  Reason = "heartbeat_settled"
	ReasonSettleGrace       Reason = "settle_grace"
	ReasonAwaitingVerdict   Reason = "awaiting_verdict"
	ReasonBackgroundWork    Reason = "background_work"
	ReasonBackgroundParked  Reason = "background_parked"
	ReasonCompacting        Reason = "compacting"
	ReasonStopFailed        Reason = "stop_failed"
	ReasonTurnAborted       Reason = "turn_aborted"
	ReasonClassifierVerdict Reason = "classifier_verdict"
	ReasonAtPrompt          Reason = "at_prompt"
	ReasonStuck             Reason = "stuck"
	ReasonNoEvidence        Reason = "no_evidence"
)

// Resolution is the resolver's answer.
type Resolution struct {
	State  protocol.SessionState
	Reason Reason
	Detail string
	// Hold means "keep whatever the session already shows" (State empty): a pure
	// resolver never reads the current state, and every holding clause is
	// time-bounded.
	Hold bool
}

// Resolve decides what a session is doing. Pure: same evidence and clock, same
// answer. Clauses are ordered, first match wins, and the order encodes trust — a
// fresh heartbeat outranks an open approval because an agent visibly running
// cannot be blocked on the user.
func Resolve(e Evidence, policy Policy, now time.Time) Resolution {
	// Terminal; nothing below outranks it.
	if e.Process != nil && e.Process.Claim == ClaimExited {
		return Resolution{State: protocol.SessionStateIdle, Reason: ReasonProcessExited, Detail: e.Process.Detail}
	}

	// Rescues a lost turn-open hook: visibly running outranks the brackets.
	if fresh(e.Heartbeat, ClaimBusy, now, policy.HeartbeatTTL) {
		return running(e)
	}

	// Blocked on a person, announced exactly when the turn stops looking like it
	// runs, so it outranks every bracket below. Nothing announces the answer:
	// these edges retire only by the agent going busy past them.
	if r, ok := harnessEdge(e); ok {
		return r
	}

	// A halted turn's closing bracket is never coming. It settles without the
	// classifier (no answer to judge) and sits above compaction/background work
	// but below the busy heartbeat, which is what contradicts it.
	if turnAborted(e) {
		return Resolution{
			State:  protocol.SessionStateIdle,
			Reason: ReasonTurnAborted,
			Detail: e.LastHarnessEvent.Detail,
		}
	}

	// Work no other clause can see. This and the clause below expire on total
	// silence — a lost PostCompact must not pin the session green for good.
	if e.Compacting {
		if evidenceStoppedMoving(e, now, policy.StuckAfter) {
			return Resolution{State: protocol.SessionStateUnknown, Reason: ReasonStuck}
		}
		return Resolution{State: protocol.SessionStateWorking, Reason: ReasonCompacting}
	}

	// The turn yielded with work still running; the payload alone cannot say
	// whether anyone is waited on, so the stop-time verdict outranks every guess
	// below. A parked verdict is affirmative evidence for the silence that
	// follows, so it holds working WITHOUT decaying to unknown (unknown opens a
	// turn); it is still spent by the next busy frame. With no verdict, hold
	// working while judgment may land, let prompt-idle retire the yield, and
	// decay to stuck on total silence.
	if e.BackgroundWork {
		if r, ok := classifierVerdict(e); ok {
			return r
		}
		if parkedVerdict(e) {
			return Resolution{State: protocol.SessionStateWorking, Reason: ReasonBackgroundParked}
		}
		if ClassifierVerdictPending(e, policy, now) {
			return Resolution{State: protocol.SessionStateWorking, Reason: ReasonBackgroundWork}
		}
		if promptIdleConfirmed(e) {
			return settled(e, ReasonPromptIdle, policy, now)
		}
		if evidenceStoppedMoving(e, now, policy.StuckAfter) {
			return Resolution{State: protocol.SessionStateUnknown, Reason: ReasonStuck}
		}
		return Resolution{State: protocol.SessionStateWorking, Reason: ReasonBackgroundWork}
	}

	// Outranks an open bracket (a second hook on a different trigger saying the
	// turn is over) but sits below approval: an unanswered approval is also
	// "parked at the prompt".
	if promptIdleConfirmed(e) {
		return settled(e, ReasonPromptIdle, policy, now)
	}

	// An open bracket says work is outstanding; the heartbeat decides whether to
	// believe it, or a lost closing hook holds the session working forever.
	if e.TurnOpen || e.ToolOpen {
		// For an agent with hooks but no heartbeat, heartbeatSilentFor answers
		// "not silent" forever; without this check stuck is unreachable.
		if evidenceStoppedMoving(e, now, policy.StuckAfter) {
			return Resolution{State: protocol.SessionStateUnknown, Reason: ReasonStuck}
		}
		if !heartbeatSilentFor(e, now, policy.StaleAfter) {
			return running(e)
		}
		// A finished turn and an unannounced approval look the same, so hold for
		// SettleGrace rather than assert idle into a late explanation; a verdict
		// that already landed ends the grace early.
		if r, ok := classifierVerdict(e); ok {
			return r
		}
		if !heartbeatSilentFor(e, now, policy.StaleAfter+policy.SettleGrace) {
			return Resolution{Hold: true, Reason: ReasonSettleGrace}
		}
		return settled(e, ReasonBracketStale, policy, now)
	}

	// Brackets closed and no busy frames: the turn is over. Needs a turn to have
	// happened, not merely a busy frame — a booting agent paints frames before
	// its prompt is ready.
	if e.Heartbeat != nil && everTookATurn(e) && !e.TurnOpen && !e.ToolOpen {
		// A gap only longer than the TTL is a repaint gap, not a settle; without
		// HeartbeatSettleAfter every wide gap costs one owed turn.
		if e.Heartbeat.Claim == ClaimBusy && !heartbeatSilentFor(e, now, policy.HeartbeatSettleAfter) {
			return running(e)
		}
		return settled(e, ReasonHeartbeatSettled, policy, now)
	}

	// A wakeup is only learned from a Stop, so one recorded here is a settle on
	// its own evidence. It needs no heartbeat: a session reporting hooks without
	// a title (headless, remote) would otherwise read as never having spoken.
	if e.PendingCron && !e.TurnOpen && !e.ToolOpen {
		return settled(e, ReasonCronPending, policy, now)
	}

	// Never took a turn and says it is not running: at its prompt — the only
	// thing that retires the `working` handed out at spawn. The evidence is the
	// agent's own not-busy title, not an absence of one.
	if e.Heartbeat != nil && e.Heartbeat.Claim == ClaimSettled && !everTookATurn(e) {
		return Resolution{State: protocol.SessionStateIdle, Reason: ReasonAtPrompt}
	}

	if r, ok := classifierVerdict(e); ok {
		return r
	}

	// Needs a turn to have opened first: a launched-and-left-alone agent is
	// silent because there is nothing to report, and nothing would ever
	// contradict a stuck verdict.
	if e.TurnEverOpened && evidenceStoppedMoving(e, now, policy.StuckAfter) {
		return Resolution{State: protocol.SessionStateUnknown, Reason: ReasonStuck}
	}

	return Resolution{State: protocol.SessionStateUnknown, Reason: ReasonNoEvidence}
}

// running is the one answer for a turn that is going. Three clauses reach one,
// separated by windows a working agent crosses all turn long, so a reason named
// after the window renamed a session that never moved. The bracket names it when
// one is open: it is the witness that holds while title frames come and go.
func running(e Evidence) Resolution {
	if e.TurnOpen || e.ToolOpen {
		return Resolution{State: protocol.SessionStateWorking, Reason: ReasonBracketOpen}
	}
	detail := ""
	if e.Heartbeat != nil {
		detail = e.Heartbeat.Detail
	}
	return Resolution{State: protocol.SessionStateWorking, Reason: ReasonHeartbeatBusy, Detail: detail}
}

// settled resolves a turn that is over, preferring the classifier's verdict and
// holding (bounded by ClassifierTimeout) while one is computed: publishing idle
// first and correcting on arrival flickers green-then-yellow. A registered
// wakeup does not change the answer — suppressing the queue is a user control,
// not something inferred from a schedule.
func settled(e Evidence, fallback Reason, policy Policy, now time.Time) Resolution {
	if r, ok := classifierVerdict(e); ok {
		return r
	}
	if ClassifierVerdictPending(e, policy, now) {
		return Resolution{Hold: true, Reason: ReasonAwaitingVerdict}
	}
	return Resolution{State: protocol.SessionStateIdle, Reason: fallback}
}

// ClassifierVerdictPending reports whether a classification is running and
// still worth waiting for; exported so outside consumers share the same bound.
func ClassifierVerdictPending(e Evidence, policy Policy, now time.Time) bool {
	if e.ClassifyingSince.IsZero() {
		return false
	}
	return now.Sub(e.ClassifyingSince) <= policy.ClassifierTimeout
}

// harnessEdge reads an outstanding "blocked on a person" announcement. The edges
// share one clause: a turn cannot be blocked on an approval and a question at
// once, and the last arrival is the one outstanding.
func harnessEdge(e Evidence) (Resolution, bool) {
	if e.LastHarnessEvent == nil || supersededByBusy(e.LastHarnessEvent, e) {
		return Resolution{}, false
	}
	switch e.LastHarnessEvent.Claim {
	case ClaimApprovalPending:
		return Resolution{
			State:  protocol.SessionStatePendingApproval,
			Reason: ReasonApprovalOpen,
			Detail: e.LastHarnessEvent.Detail,
		}, true
	case ClaimNeedsInput:
		return Resolution{
			State:  protocol.SessionStateWaitingInput,
			Reason: ReasonQuestionOpen,
			Detail: e.LastHarnessEvent.Detail,
		}, true
	case ClaimStopFailed:
		// A turn cut off by the API is blocked on a person as surely as a question
		// is (rate limit, bill, login); the detail carries which error.
		return Resolution{
			State:  protocol.SessionStateWaitingInput,
			Reason: ReasonStopFailed,
			Detail: e.LastHarnessEvent.Detail,
		}, true
	default:
		return Resolution{}, false
	}
}

// turnAborted reports a recorded halt with nothing since to spend it. It shares
// LastHarnessEvent with harnessEdge, which ignores the claim, so the two readers
// of the slot cannot both fire.
func turnAborted(e Evidence) bool {
	return e.LastHarnessEvent != nil &&
		e.LastHarnessEvent.Claim == ClaimTurnAborted &&
		!supersededByBusy(e.LastHarnessEvent, e)
}

// parkedVerdict reports a stop-time verdict of "waiting on its own background
// work", spent by the agent going busy past it — the resume it predicted.
func parkedVerdict(e Evidence) bool {
	return e.LastClassifier != nil &&
		e.LastClassifier.Claim == ClaimParked &&
		!supersededByBusy(e.LastClassifier, e)
}

// classifierVerdict reads the stop-time verdict belonging to the current turn;
// parked is read by the background-work clause alone. A verdict the agent has
// gone busy past is dropped, or a turn settling mid-classification would take
// the previous turn's answer.
func classifierVerdict(e Evidence) (Resolution, bool) {
	if e.LastClassifier == nil {
		return Resolution{}, false
	}
	if supersededByBusy(e.LastClassifier, e) {
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

// DwellFor is how long a transition into state must hold before publishing: with
// no reviewer in the loop the user IS the reviewer, so the dwell is zero.
func DwellFor(state protocol.SessionState, e Evidence, policy Policy) time.Duration {
	if state == protocol.SessionStatePendingApproval && e.ReviewerInLoop {
		return policy.GuardianDwell
	}
	return 0
}

// supersededByBusy reports whether the agent painted a busy frame since o. The
// edges this retires describe a moment the agent was not running and have no
// announcement of their own to expire on.
func supersededByBusy(o *Observation, e Evidence) bool {
	if o == nil || e.LastBusyAt.IsZero() {
		return false
	}
	return e.LastBusyAt.After(o.ObservedAt)
}

// fresh reports whether o makes claim and is recent enough to still be believed.
func fresh(o *Observation, claim Claim, now time.Time, ttl time.Duration) bool {
	return o != nil && o.Claim == claim && now.Sub(o.ObservedAt) <= ttl
}

// everTookATurn counts the classifier alongside the brackets: a daemon restarted
// mid-turn leaves exactly that shape — judged, with no bracket to show for it.
func everTookATurn(e Evidence) bool {
	if e.TurnOpen || e.ToolOpen || e.TurnEverOpened {
		return true
	}
	return e.LastClassifier != nil || !e.ClassifyingSince.IsZero()
}

// evidenceStoppedMoving reports whether every source has gone quiet for d —
// deliberately not the heartbeat's question, which stops routinely mid-turn.
func evidenceStoppedMoving(e Evidence, now time.Time, d time.Duration) bool {
	if e.LastMovement.IsZero() {
		return false
	}
	return now.Sub(e.LastMovement) > d
}

// promptIdleConfirmed guards on LastBusyAt, not the 60s the notification happens
// to use, so nothing breaks if claude retunes it.
func promptIdleConfirmed(e Evidence) bool {
	return !e.PromptIdleAt.IsZero() && e.PromptIdleAt.After(e.LastBusyAt)
}

// heartbeatSilentFor reads LastBusyAt, not the latest heartbeat (claude blips its
// idle glyph mid-turn). An agent that never reported busy is not silent — one
// with no harness signals must not have its brackets closed out from under it.
func heartbeatSilentFor(e Evidence, now time.Time, d time.Duration) bool {
	if e.LastBusyAt.IsZero() {
		return false
	}
	return now.Sub(e.LastBusyAt) > d
}
