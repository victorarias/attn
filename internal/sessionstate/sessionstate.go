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
// Levels (Heartbeat, TurnOpen, ToolOpen, Process) describe a condition
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
	// Process is the PTY lifecycle level.
	Process *Observation

	// TurnOpen: a prompt was submitted and no Stop has closed it.
	TurnOpen bool
	// TurnEverOpened: a turn has opened at least once in this session's life.
	// It is what separates "settled" from "has not started yet", which look
	// identical in every other field — a booting agent paints title frames, and
	// codex flickers a busy one before its first prompt is even ready.
	TurnEverOpened bool
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

	// PromptIdleAt is when the harness last confirmed the agent is sitting at
	// its prompt with nothing outstanding. Claude reports this via its
	// Notification hook 60s after a settle nobody answered, once, cancelled if
	// the user types first.
	//
	// It is not an Observation carrying a claim. The message reads "Claude is
	// waiting for your input", but it fires for any unanswered settle: a
	// finished foreground Bash turn gets it 60s after Stop exactly as a question
	// does. So it cannot choose between idle and waiting_input.
	//
	// What it is, is an independent witness that the agent is not working, which
	// is the one thing a lost Stop hook leaves attn unable to discover.
	PromptIdleAt time.Time

	// ClassifyingSince is when a stop-time classification started, zero when none
	// is running. It is the difference between "the turn settled and it is idle"
	// and "the turn settled and we are still finding out", which look identical
	// in every other field.
	ClassifyingSince time.Time

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
	// own, outranking every edge below it. It is a precedence window, not a
	// liveness one: it has to be short, because a busy frame that stays
	// authoritative too long suppresses the approval and question edges that are
	// announced precisely when the agent stops painting frames.
	HeartbeatTTL time.Duration
	// HeartbeatSettleAfter is how long busy frames must have stopped before their
	// absence is read as the turn having ended. It answers a different question
	// from HeartbeatTTL — "has it stopped for good" rather than "is it running
	// right now" — and so it must be sized against the agent's worst repaint gap
	// rather than its typical one.
	HeartbeatSettleAfter time.Duration
	// StaleAfter is the heartbeat silence that closes a bracket whose closing
	// hook never arrived. It must exceed the longest silence the agent produces
	// mid-turn, or a slow tool call reads as a finished turn.
	StaleAfter time.Duration
	// StuckAfter is total evidence silence — no source of any kind — after which
	// the session is reported stuck rather than left in whatever it last showed.
	StuckAfter time.Duration
	// SettleGrace is how long past StaleAfter a stale bracket keeps its current
	// state instead of asserting idle, waiting for a late explanation.
	SettleGrace time.Duration
	// ClassifierTimeout bounds how long a running classification may hold a
	// settle. Past it the session settles without the verdict, because a
	// classifier that never returns must not be able to freeze a color.
	ClassifierTimeout time.Duration
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
	// defaultHeartbeatSettleAfter is how long busy title frames must have stopped
	// before their absence settles a session that has no bracket open.
	//
	// It is separate from HeartbeatTTL because the two windows answer different
	// questions, and the settle used to borrow the TTL's answer. That is wrong in
	// both directions: the TTL is sized to stay out of the way of approval edges,
	// which pushes it down, while a settle needs to survive the agent's worst
	// repaint gap, which pushes it up. Measured on claude 2.1.220 during a
	// `/compact`, where the title repaints every ~1.92s — comfortably past the
	// 1.5s TTL — with the previous turn's bracket already closed: the resolver
	// alternated working/idle once a second for as long as the compaction ran.
	//
	// Five seconds clears that gap with margin for a PTY read batching under
	// load, and the latency it adds is bounded to one case. A turn that ends
	// normally settles on its Stop hook and its classifier verdict; this clause
	// is the fallback for when the classifier declines, and being five seconds
	// late there is far cheaper than a session that cannot be settled at all.
	defaultHeartbeatSettleAfter = 5 * time.Second
	// defaultStaleAfter is the heartbeat silence after which an open bracket stops
	// being believed. It is consulted only when a closing hook was lost — a turn
	// whose Stop arrives settles immediately regardless — so the whole trade is
	// "how long a lost hook shows the wrong color" against "how sure we are the
	// turn really ended".
	//
	// It was 4 seconds, which sat directly on top of claude's measured ~3.5s
	// mid-tool title silence: one slow foreground tool call away from settling a
	// turn that was still running, and a false settle is a wrong color on a session
	// that is genuinely working. A minute is far past any silence either agent
	// produces mid-turn, and Victor's call (2026-07-26) is that a lost hook showing
	// green for up to a minute is the cheaper failure. Stuck detection at 90s is the
	// real backstop.
	defaultStaleAfter = 60 * time.Second
	defaultStuckAfter = 90 * time.Second
	// guardianDwell is the round trip a guardian needs to answer in the user's
	// place. Measured: 90ms for claude's permission classifier, low seconds for
	// codex's auto_review. 60s is deliberately far above both — the cost of
	// waiting is a late notification, the cost of not waiting is a yellow flash
	// on every tool call of an unattended run.
	//
	// Witnessed working on 2026-07-26 only after the hook path stopped writing
	// state directly: the dwell lives in the resolver, so for as long as a hook
	// could apply `pending_approval` itself the flash happened ten seconds before
	// the dwell was ever consulted. See publishResolution.
	guardianDwell = 60 * time.Second
	// defaultSettleGrace is the window past StaleAfter in which a stale bracket
	// holds rather than asserting idle.
	//
	// It was sized from claude's approval lag — prompt at 14.6s, Notification hook
	// at 20.6s, bracket stale at 18.6s with the approval two seconds from being
	// announced — and raising StaleAfter to a minute retires that specific overlap:
	// nothing goes stale until 60s, by which time an approval has long since been
	// announced and handled by the clause above. What is left is the general case,
	// which is the same shape: the instant a bracket stops being believed is a bad
	// moment to assert idle, because a late explanation is still possible.
	//
	// The hold is bounded on purpose. Holding forever would reproduce the stuck
	// color it exists to avoid: codex has no idle_prompt equivalent, so a lost
	// Stop hook there has nothing else to unstick it.
	defaultSettleGrace = 4 * time.Second
	// defaultClassifierTimeout is generous on purpose. Overrunning it costs one
	// visible settle that a late verdict then corrects; undershooting it
	// reintroduces the flicker the gate exists to prevent.
	defaultClassifierTimeout = 30 * time.Second
)

// PolicyFor returns the timing for an agent. An agent with no measured numbers
// gets the conservative end of each: a TTL short enough not to hold a session
// working on a stale glyph, and a stale window long enough not to close a
// bracket early.
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
	return policy
}

// Reason names why the resolver reached a state. It is what `attn state explain`
// shows, and it is the difference between "the color is wrong" and a diagnosis.
type Reason string

const (
	ReasonProcessExited     Reason = "process_exited"
	ReasonHeartbeatFresh    Reason = "heartbeat_fresh"
	ReasonApprovalOpen      Reason = "approval_open"
	ReasonQuestionOpen      Reason = "question_open"
	ReasonCronPending       Reason = "cron_pending"
	ReasonBracketOpen       Reason = "bracket_open"
	ReasonPromptIdle        Reason = "prompt_idle"
	ReasonBracketStale      Reason = "bracket_stale"
	ReasonHeartbeatSettled  Reason = "heartbeat_settled"
	ReasonHeartbeatGap      Reason = "heartbeat_gap"
	ReasonSettleGrace       Reason = "settle_grace"
	ReasonAwaitingVerdict   Reason = "awaiting_verdict"
	ReasonBackgroundWork    Reason = "background_work"
	ReasonClassifierVerdict Reason = "classifier_verdict"
	ReasonAtPrompt          Reason = "at_prompt"
	ReasonStuck             Reason = "stuck"
	ReasonNoEvidence        Reason = "no_evidence"
)

// Resolution is the resolver's answer.
type Resolution struct {
	State  protocol.SessionState
	Reason Reason
	// Detail carries the winning observation's detail so a diagnosis does not
	// require re-reading the evidence table.
	Detail string
	// Hold means "keep whatever the session already shows". It is not a state,
	// and State is empty when it is set.
	//
	// A pure resolver cannot express "unchanged" any other way: it does not read
	// the current state, deliberately, so that its answer is a function of the
	// evidence alone. Hold is how it says the evidence does not yet support
	// moving — always for a bounded window, never indefinitely.
	Hold bool
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

	// The harness said in so many words that the agent is blocked on a person:
	// an approval request, or a question put to the user. Both outrank every
	// bracket below, because both are announced precisely when the turn stops
	// looking like it is running.
	//
	// Both are retired the same way, by the agent going busy past them. Nothing
	// announces an answer — claude has no counterpart to its permission_prompt
	// notification, codex has no approval hook at all — so the agent painting a
	// spinner frame again is the signal, and it is a reliable one: an agent
	// blocked on a prompt is not running, which is exactly why the bracket goes
	// stale while it waits.
	//
	// Without that expiry the edge is forever. The screen scrape used to retire an
	// approval by watching the prompt leave the display; that is the thing this
	// plan deleted, and an approval with nothing left to clear it is a permanent
	// color.
	if r, ok := harnessEdge(e); ok {
		return r
	}

	// A background task keeps the session working only until the harness says
	// otherwise. The confirmation below is the harness stating where the session
	// is — parked at its prompt, nothing running — and that is a direct answer to
	// the question the background-work fact only guesses at, so it retires the
	// guess.
	//
	// Only the background half. A parked schedule is a promise to come back at a
	// known time, which the agent sitting at its prompt does not contradict; a
	// background task carries no such promise, and the notification is the only
	// thing that ever reports it stopped mattering. Claude fires the notification
	// on a flat prompt-idle timer that reads neither fact — measured on both, and
	// the reason it can be trusted to arrive.
	backgroundWork := e.BackgroundWork && !promptIdleConfirmed(e)

	// The turn yielded with something outstanding that will resume it, so nobody
	// is being waited on. Both facts arrive together on the Stop payload and the
	// order between them is the interesting part: work still running means the
	// session is working, and only once nothing is running does a parked schedule
	// get to describe it.
	//
	// Bounded by total silence like the brackets below, and for the same reason: a
	// fact about how the last turn yielded is believed while the session is still
	// producing evidence, and a session that resumed nothing and then went quiet
	// forever is stuck rather than busy.
	if backgroundWork || e.PendingCron {
		if evidenceStoppedMoving(e, now, policy.StuckAfter) {
			return Resolution{State: protocol.SessionStateUnknown, Reason: ReasonStuck}
		}
		if backgroundWork {
			return Resolution{State: protocol.SessionStateWorking, Reason: ReasonBackgroundWork}
		}
		return Resolution{State: protocol.SessionStateScheduled, Reason: ReasonCronPending}
	}

	// The harness says the agent is parked at its prompt. That outranks an open
	// bracket, because a bracket only ever closes on a hook that may never come,
	// and this is a second hook on a different trigger saying the same turn is
	// over.
	//
	// It sits below the approval clause on purpose — an unanswered approval is
	// also "parked at the prompt", and approval is the more useful thing to say.
	if promptIdleConfirmed(e) {
		return settled(e, ReasonPromptIdle, policy, now)
	}

	// An open bracket says work is outstanding. Whether to believe it is exactly
	// what the heartbeat is for: a bracket whose closing hook was lost would
	// otherwise hold the session working for the rest of its life.
	if e.TurnOpen || e.ToolOpen {
		// A bracket only outranks stuck while something is still arriving. Believing
		// it unconditionally is how an agent with hooks but no heartbeat pins itself
		// green for the rest of its life: heartbeatSilentFor answers "not silent" for
		// an agent that has never reported being busy — deliberately, since it has
		// nothing to have gone quiet from — so the check below cannot retire the
		// bracket and the stuck clause at the bottom is unreachable.
		//
		// Total silence is the one thing that settles it. Note this is not the same
		// question as the heartbeat's: StaleAfter asks whether the agent stopped
		// painting spinner frames, which happens routinely mid-turn, while this asks
		// whether *any* source has said anything at all.
		if evidenceStoppedMoving(e, now, policy.StuckAfter) {
			return Resolution{State: protocol.SessionStateUnknown, Reason: ReasonStuck}
		}
		if !heartbeatSilentFor(e, now, policy.StaleAfter) {
			return Resolution{State: protocol.SessionStateWorking, Reason: ReasonBracketOpen}
		}
		// The bracket is stale. Fall through to the settled clauses below, which
		// decide *how* it settled — but remember it, so a settle with no verdict
		// is reported as an un-stick rather than as an absence of evidence.
		//
		// Briefly, though. Going stale means the agent stopped painting spinner
		// frames, which happens both when a turn ends and when it pauses on an
		// approval nobody has announced yet, and those are indistinguishable at
		// this instant. Asserting idle here would flicker every approval whose
		// announcement lags its prompt. So the first SettleGrace after the
		// bracket goes stale holds instead, and only then settles.
		// A verdict that already landed ends the grace early: there is nothing
		// left to wait for, so waiting would only delay a known answer.
		if r, ok := classifierVerdict(e); ok {
			return r
		}
		if !heartbeatSilentFor(e, now, policy.StaleAfter+policy.SettleGrace) {
			return Resolution{Hold: true, Reason: ReasonSettleGrace}
		}
		return settled(e, ReasonBracketStale, policy, now)
	}

	// Brackets closed, and the agent is not painting busy frames: the turn is
	// over, whatever else did or did not happen afterwards.
	//
	// This is the settled half of the stuck-color fix and it has to be a clause
	// of its own. The classifier is what usually publishes a settle, but it is
	// allowed to decline — no transcript, capability disabled, an error — and
	// when it declines nothing else ever contradicts the working state the turn
	// was in. Reading the heartbeat here means a settle no longer depends on any
	// particular source having spoken.
	//
	// It needs a turn to have happened, not merely a busy frame to have been
	// painted. A booting agent paints title frames before its prompt is ready —
	// codex flickers a busy one — and settling on those reports a turn finished
	// before the agent has taken one, which showed up live as an idle blip
	// seconds after launch.
	if e.Heartbeat != nil && everTookATurn(e) && !e.TurnOpen && !e.ToolOpen {
		// The latest frame still says busy; it has only gone quiet for longer
		// than the TTL. That is a repaint gap, not a settle, and the two are
		// distinguishable: an agent whose turn is over stops painting busy frames
		// for good, so waiting out HeartbeatSettleAfter separates them at the cost
		// of settling that much later in the fallback case.
		//
		// Without this, a gap wider than the TTL settles the session and the next
		// frame un-settles it, once per gap, for as long as the agent keeps
		// working with no bracket open — which is exactly what a `/compact`
		// between turns produces. Each of those idle edges opens a turn the user
		// owes, so a queue the user had just settled refilled itself a second
		// later.
		if e.Heartbeat.Claim == ClaimBusy && !heartbeatSilentFor(e, now, policy.HeartbeatSettleAfter) {
			return Resolution{State: protocol.SessionStateWorking, Reason: ReasonHeartbeatGap, Detail: e.Heartbeat.Detail}
		}
		return settled(e, ReasonHeartbeatSettled, policy, now)
	}

	// An agent that has never taken a turn and says it is not running is sitting at
	// its prompt. That is idle, and saying so is the only thing that retires the
	// `working` a session is handed at spawn — every clause above requires a turn to
	// have opened, on purpose, so a session launched and left alone used to keep
	// that spawn-time color for the rest of its life. Witnessed live on 2026-07-26:
	// fourteen minutes green on a session nobody had prompted.
	//
	// The agent's own title is the evidence, not an absence of evidence: claude
	// paints a not-busy glyph once its prompt is ready, and codex paints one after
	// the busy flicker it emits while booting. A fresh busy frame is already handled
	// far above, so reaching here means the latest frame says nothing is running.
	if e.Heartbeat != nil && e.Heartbeat.Claim == ClaimSettled && !everTookATurn(e) {
		return Resolution{State: protocol.SessionStateIdle, Reason: ReasonAtPrompt}
	}

	if r, ok := classifierVerdict(e); ok {
		return r
	}

	// Nothing has moved at all. That is its own diagnosis, and reporting it is
	// the whole point: a stuck session used to be indistinguishable from a
	// correctly-quiet one.
	//
	// It needs a turn to have opened first. An agent that has been launched and
	// left alone is silent because there is nothing to report, not because it
	// stopped reporting: claude paints its title on activity and then goes quiet
	// at an empty prompt, with no Stop and no idle_prompt notification to
	// contradict a stuck verdict. Witnessed live on 2026-07-26 — a session
	// created and never prompted turned `unknown` ninety seconds later.
	if e.TurnEverOpened && evidenceStoppedMoving(e, now, policy.StuckAfter) {
		return Resolution{State: protocol.SessionStateUnknown, Reason: ReasonStuck}
	}

	return Resolution{State: protocol.SessionStateUnknown, Reason: ReasonNoEvidence}
}

// settled resolves a turn that is over, preferring the classifier's verdict and
// falling back to the reason that got us here.
//
// When no verdict has landed but one is being computed, it holds. The classifier
// is what separates idle from waiting_input, and it takes seconds: publishing
// idle first and correcting it on arrival turns every question-ending turn into
// a visible green-then-yellow flicker. Holding is bounded by ClassifierTimeout,
// so a classifier that dies still settles the session.
func settled(e Evidence, fallback Reason, policy Policy, now time.Time) Resolution {
	if r, ok := classifierVerdict(e); ok {
		return r
	}
	if verdictPending(e, policy, now) {
		return Resolution{Hold: true, Reason: ReasonAwaitingVerdict}
	}
	return Resolution{State: protocol.SessionStateIdle, Reason: fallback}
}

// verdictPending reports whether a classification is running and still worth
// waiting for.
func verdictPending(e Evidence, policy Policy, now time.Time) bool {
	if e.ClassifyingSince.IsZero() {
		return false
	}
	return now.Sub(e.ClassifyingSince) <= policy.ClassifierTimeout
}

// harnessEdge reads the harness's own announcement that the agent is blocked on
// a person, if one is still outstanding.
//
// The two edges differ only in what they say and what answers them, so they
// share a clause rather than being ordered against each other: a turn cannot be
// blocked on an approval and on a question at the same time, and whichever
// arrived last is the one still outstanding.
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
	default:
		return Resolution{}, false
	}
}

// classifierVerdict reads the stop-time verdict, if one belongs to the current
// turn.
//
// A verdict describes the turn it was computed for and nothing else. The turn
// bracket clears it when the next turn opens, but a turn may also start without
// its hook — surviving that is the whole reason the heartbeat is here — so this
// also drops a verdict the agent has since gone busy past. Otherwise a turn that
// settles while its own classification is still running takes the previous
// turn's answer instead of holding for its own.
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

// supersededByBusy reports whether the agent has painted a busy frame since o
// was observed.
//
// Both edges this retires — an unanswered approval and a stop-time verdict —
// describe a moment the agent was *not* running. A later busy frame is proof it
// moved on, and neither edge has an announcement of its own to expire on.
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
// everTookATurn reports whether this session has been seen doing anything at all.
//
// The brackets are the direct answer and the classifier is the indirect one: a
// verdict, or a classification still running, only exists because a turn ended,
// and both outlive the brackets that produced them. A daemon restarted mid-turn
// or a lost UserPromptSubmit leaves exactly that shape — judged, with no bracket
// to show for it — and reading it as "this agent has never run" is how a settled
// turn gets reported as a fresh prompt.
func everTookATurn(e Evidence) bool {
	if e.TurnOpen || e.ToolOpen || e.TurnEverOpened {
		return true
	}
	return e.LastClassifier != nil || !e.ClassifyingSince.IsZero()
}

// evidenceStoppedMoving reports whether every source has gone quiet for d.
//
// This is the strongest statement the table can make about silence, and it is
// deliberately not the heartbeat's question: the heartbeat asks whether the agent
// is painting spinner frames, which stops routinely mid-turn, while this asks
// whether anything at all — hook, title, classifier, process — has been heard
// from. A session whose whole table has frozen is not in whatever state it last
// showed; it is a session attn has lost track of.
func evidenceStoppedMoving(e Evidence, now time.Time, d time.Duration) bool {
	if e.LastMovement.IsZero() {
		return false
	}
	return now.Sub(e.LastMovement) > d
}

// promptIdleConfirmed reports whether the harness has confirmed the agent is
// sitting at its prompt, and nothing has happened since to spend that.
//
// LastBusyAt is the guard, not the 60s the notification happens to use: if the
// agent painted a spinner after the confirmation, a new turn started and the
// confirmation is spent. Nothing here breaks if claude retunes the timer.
func promptIdleConfirmed(e Evidence) bool {
	return !e.PromptIdleAt.IsZero() && e.PromptIdleAt.After(e.LastBusyAt)
}

func heartbeatSilentFor(e Evidence, now time.Time, d time.Duration) bool {
	if e.LastBusyAt.IsZero() {
		return false
	}
	return now.Sub(e.LastBusyAt) > d
}
