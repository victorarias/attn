package sessionstate

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// now is the fixed clock every case resolves against. Ages are expressed as
// offsets from it so a case reads as "this evidence, this old".
var now = time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)

func testPolicy() Policy {
	return Policy{
		HeartbeatTTL:         time.Second,
		HeartbeatSettleAfter: 3 * time.Second,

		StaleAfter:        4 * time.Second,
		StuckAfter:        90 * time.Second,
		GuardianDwell:     60 * time.Second,
		SettleGrace:       4 * time.Second,
		ClassifierTimeout: 30 * time.Second,
	}
}

// seen builds an observation aged `age` before the fixed clock.
func seen(source Source, claim Claim, age time.Duration) *Observation {
	return &Observation{Source: source, Claim: claim, ObservedAt: now.Add(-age)}
}

func TestResolve(t *testing.T) {
	for _, tc := range []struct {
		name       string
		evidence   Evidence
		wantState  protocol.SessionState
		wantReason Reason
		// wantHold expects "keep the current state". State must be empty then:
		// a hold that also named a state would be two answers at once.
		wantHold bool
	}{
		// --- the two clauses that do not exist today ------------------------

		{
			// A lost turn-open hook used to leave the session idle for the whole
			// turn. The agent is visibly running, so it is working.
			name: "a fresh heartbeat works without any bracket",
			evidence: Evidence{
				Heartbeat: seen(SourceHeartbeat, ClaimBusy, 200*time.Millisecond),
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonHeartbeatFresh,
		},
		{
			// A lost Stop hook used to hold the session working forever. Once the
			// agent stops saying it is busy, the bracket has to give way.
			name: "an open bracket goes stale when the heartbeat goes silent",
			evidence: Evidence{
				TurnOpen:   true,
				Heartbeat:  seen(SourceHeartbeat, ClaimBusy, 10*time.Second),
				LastBusyAt: now.Add(-10 * time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonBracketStale,
		},
		{
			// The stale bracket still asks the classifier how the turn ended
			// rather than assuming it ended quietly.
			name: "a stale bracket takes the classifier's verdict when there is one",
			evidence: Evidence{
				TurnOpen:       true,
				Heartbeat:      seen(SourceHeartbeat, ClaimBusy, 5*time.Second),
				LastBusyAt:     now.Add(-5 * time.Second),
				LastClassifier: seen(SourceClassifier, ClaimNeedsInput, 2*time.Second),
			},
			wantState:  protocol.SessionStateWaitingInput,
			wantReason: ReasonClassifierVerdict,
		},
		{
			// Claude blips its idle glyph mid-turn — between tool calls, and while
			// a foreground tool is still running. Closing the bracket on that one
			// frame is the false-settle path the measurements ruled out, so
			// staleness is measured from the last *busy* frame, not the latest one.
			name: "a not-busy blip inside the window does not settle an open turn",
			evidence: Evidence{
				TurnOpen:   true,
				Heartbeat:  seen(SourceHeartbeat, ClaimSettled, 10*time.Millisecond),
				LastBusyAt: now.Add(-500 * time.Millisecond),
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonBracketOpen,
		},
		{
			// The lost-Stop-hook rescue. The bracket never closes and the agent
			// stopped painting long ago, so without a second witness the session
			// would sit working forever. Claude's 60s notification is that
			// witness.
			name: "a prompt-idle confirmation closes a bracket whose hook never came",
			evidence: Evidence{
				TurnOpen:     true,
				LastBusyAt:   now.Add(-90 * time.Second),
				PromptIdleAt: now.Add(-30 * time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonPromptIdle,
		},
		{
			// The guard that makes the confirmation self-expiring: a spinner
			// after it means a new turn started, so the confirmation is spent.
			// This is the type-at-59.9s race — if the notification competed on
			// its own arrival time it would beat a genuinely running turn.
			//
			// The timings pick the one window where the guard is the only thing
			// deciding the answer. The heartbeat is 2s old, so it is past the
			// 1.5s TTL and the fresh-busy clause does not fire, but it is inside
			// the 4s StaleAfter, so the bracket still holds. Drop the guard and
			// the confirmation settles a turn that is visibly running.
			name: "a busy frame after the confirmation spends it",
			evidence: Evidence{
				TurnOpen:     true,
				Heartbeat:    seen(SourceHeartbeat, ClaimBusy, 2*time.Second),
				LastBusyAt:   now.Add(-2 * time.Second),
				PromptIdleAt: now.Add(-30 * time.Second),
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonBracketOpen,
		},
		{
			// An unanswered approval is also "parked at the prompt". Approval is
			// the more useful thing to say, so it wins.
			name: "an open approval outranks a prompt-idle confirmation",
			evidence: Evidence{
				TurnOpen:         true,
				LastBusyAt:       now.Add(-90 * time.Second),
				PromptIdleAt:     now.Add(-30 * time.Second),
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimApprovalPending, 40*time.Second),
			},
			wantState:  protocol.SessionStatePendingApproval,
			wantReason: ReasonApprovalOpen,
		},
		{
			// The confirmation says the agent is not working. It does not say
			// why it stopped — it fires for a finished task exactly as it fires
			// for a question — so the classifier still picks between them.
			name: "a prompt-idle confirmation defers to the classifier verdict",
			evidence: Evidence{
				TurnOpen:       true,
				LastBusyAt:     now.Add(-90 * time.Second),
				PromptIdleAt:   now.Add(-30 * time.Second),
				LastClassifier: seen(SourceClassifier, ClaimNeedsInput, 25*time.Second),
			},
			wantState:  protocol.SessionStateWaitingInput,
			wantReason: ReasonClassifierVerdict,
		},
		{
			// And the turn survives the blip being followed by more busy frames,
			// which is what a real mid-turn gap looks like.
			name: "busy resuming after a blip keeps the turn working",
			evidence: Evidence{
				TurnOpen:   true,
				Heartbeat:  seen(SourceHeartbeat, ClaimBusy, 50*time.Millisecond),
				LastBusyAt: now.Add(-50 * time.Millisecond),
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonHeartbeatFresh,
		},
		{
			// Only a full window with no busy frame at all settles it, however
			// recently the agent said it was idle.
			name: "a not-busy level past the window settles the turn",
			evidence: Evidence{
				TurnOpen:   true,
				Heartbeat:  seen(SourceHeartbeat, ClaimSettled, 10*time.Millisecond),
				LastBusyAt: now.Add(-10 * time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonBracketStale,
		},
		{
			// An agent with no harness signals must not have its brackets closed
			// out from under it — absent evidence is not evidence of absence.
			name: "a bracket with no heartbeat at all stays open",
			evidence: Evidence{
				ToolOpen: true,
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonBracketOpen,
		},
		{
			// Claude goes quiet for seconds inside a blocking tool call. Closing
			// the bracket there would flicker every long tool to idle.
			name: "a mid-tool heartbeat gap inside StaleAfter keeps the bracket open",
			evidence: Evidence{
				ToolOpen:   true,
				Heartbeat:  seen(SourceHeartbeat, ClaimBusy, 3500*time.Millisecond),
				LastBusyAt: now.Add(-3500 * time.Millisecond),
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonBracketOpen,
		},

		// --- settling without a classifier ----------------------------------

		{
			// The settled half of the stuck-color fix. The classifier is allowed to
			// decline — no transcript, capability off, an error — and when it does,
			// nothing else was ever going to contradict the working state the turn
			// was in. The heartbeat settles it without any source having to speak.
			name: "closed brackets and a quiet heartbeat settle with no verdict at all",
			evidence: Evidence{
				TurnEverOpened: true,
				Heartbeat:      seen(SourceHeartbeat, ClaimSettled, 2*time.Second),
				LastBusyAt:     now.Add(-30 * time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonHeartbeatSettled,
		},

		// --- holds ----------------------------------------------------------

		{
			// The measured claude approval, at the instant the flicker would show.
			// Its prompt renders at t=14.6s and its Notification hook lands at
			// t=20.6s; the bracket goes stale 4s after the last busy frame, at
			// 18.6s. Without the grace the resolver paints idle across that gap —
			// in the middle of a live approval — and then corrects itself.
			name: "a bracket that just went stale holds instead of asserting idle",
			evidence: Evidence{
				TurnOpen:   true,
				Heartbeat:  seen(SourceHeartbeat, ClaimBusy, 5*time.Second),
				LastBusyAt: now.Add(-5 * time.Second),
			},
			wantHold:   true,
			wantReason: ReasonSettleGrace,
		},
		{
			// The gate that keeps a question-ending turn from flashing green. The
			// turn is over and the classifier is running; publishing idle now would
			// be corrected to waiting_input seconds later, visibly.
			name: "a settle waits for a classification that is in flight",
			evidence: Evidence{
				TurnEverOpened:   true,
				Heartbeat:        seen(SourceHeartbeat, ClaimSettled, time.Second),
				LastBusyAt:       now.Add(-30 * time.Second),
				ClassifyingSince: now.Add(-2 * time.Second),
			},
			wantHold:   true,
			wantReason: ReasonAwaitingVerdict,
		},
		{
			// Every hold is bounded. A classifier that never returns must not be
			// able to freeze a color, which is what an unbounded gate would allow.
			name: "a classification past its timeout stops holding the settle",
			evidence: Evidence{
				TurnEverOpened:   true,
				Heartbeat:        seen(SourceHeartbeat, ClaimSettled, time.Second),
				LastBusyAt:       now.Add(-90 * time.Second),
				ClassifyingSince: now.Add(-31 * time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonHeartbeatSettled,
		},
		{
			// The cross-turn race. A verdict answers the turn it was computed
			// for; once the agent has gone busy past it, it is the previous
			// turn's answer and must not be published as this turn's state. The
			// turn bracket clears it too, but a turn can start without its hook,
			// which is exactly the case the resolver exists to survive.
			name: "a verdict the agent has gone busy past is not this turn's answer",
			evidence: Evidence{
				TurnEverOpened:   true,
				Heartbeat:        seen(SourceHeartbeat, ClaimSettled, time.Second),
				LastBusyAt:       now.Add(-6 * time.Second),
				LastClassifier:   seen(SourceClassifier, ClaimNeedsInput, 20*time.Second),
				ClassifyingSince: now.Add(-2 * time.Second),
			},
			wantHold:   true,
			wantReason: ReasonAwaitingVerdict,
		},

		{
			// A session between launch and its first prompt has a title and has
			// never opened a turn. Settling that reports a turn finished before
			// the agent took one, which showed up live as an idle blip seconds
			// after launch. A busy frame is not enough to make it a turn: codex
			// flickers one while booting, which is exactly how this was found.
			name: "a session that has never opened a turn is at its prompt, not settled",
			evidence: Evidence{
				Heartbeat:  seen(SourceHeartbeat, ClaimSettled, time.Second),
				LastBusyAt: now.Add(-3 * time.Second),
			},
			wantState: protocol.SessionStateIdle,
			// The reason is the assertion. Idle is reachable two ways here and only
			// one of them is honest: `heartbeat_settled` would claim a turn finished
			// that never started, which is what produced the launch-time idle blip
			// this case was written to catch. `at_prompt` claims only what the title
			// says — the agent is not running — and it is what finally retires the
			// `working` a session is handed at spawn.
			wantReason: ReasonAtPrompt,
		},
		{
			// The other half of the same guard. A boot busy flicker is not a prompt,
			// so a session whose last frame said "running" gets no opinion at all
			// until something contradicts it — reporting idle off a stale busy frame
			// would be inventing a settle from a frame that claimed the opposite.
			name: "a stale busy frame before the first turn is not a prompt",
			evidence: Evidence{
				Heartbeat:  seen(SourceHeartbeat, ClaimBusy, time.Hour),
				LastBusyAt: now.Add(-time.Hour),
			},
			wantState:  protocol.SessionStateUnknown,
			wantReason: ReasonNoEvidence,
		},
		{
			// Nothing announces an answered approval, so the agent running again
			// is the signal. Without it the edge never expires once the screen
			// scrape stops retiring it.
			name: "an approval the agent has gone busy past was answered",
			evidence: Evidence{
				TurnEverOpened:   true,
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimApprovalPending, 30*time.Second),
				Heartbeat:        seen(SourceHeartbeat, ClaimSettled, time.Second),
				LastBusyAt:       now.Add(-10 * time.Second),
				LastClassifier:   seen(SourceClassifier, ClaimIdle, 2*time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonClassifierVerdict,
		},
		{
			// The other side of it: an approval is answered by a person, and
			// while it waits the agent is blocked and painting nothing. A busy
			// frame from *before* the request says nothing about it.
			name: "an approval still newer than the last busy frame is live",
			evidence: Evidence{
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimApprovalPending, 5*time.Second),
				Heartbeat:        seen(SourceHeartbeat, ClaimSettled, time.Second),
				LastBusyAt:       now.Add(-20 * time.Second),
			},
			wantState:  protocol.SessionStatePendingApproval,
			wantReason: ReasonApprovalOpen,
		},

		// --- ordering -------------------------------------------------------

		{
			// Nothing outranks a dead process. A session whose agent exited must
			// not stay colored as alive on stale evidence.
			name: "an exited process beats every live signal",
			evidence: Evidence{
				Process:          seen(SourceProcess, ClaimExited, time.Second),
				Heartbeat:        seen(SourceHeartbeat, ClaimBusy, 10*time.Millisecond),
				TurnOpen:         true,
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimApprovalPending, time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonProcessExited,
		},
		{
			// The agent cannot be blocked on the user while its turn is visibly
			// running: the approval was answered and its closing edge was lost.
			name: "a fresh heartbeat beats a stale approval edge",
			evidence: Evidence{
				Heartbeat:        seen(SourceHeartbeat, ClaimBusy, 100*time.Millisecond),
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimApprovalPending, 30*time.Second),
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonHeartbeatFresh,
		},
		{
			// Once the heartbeat expires, the approval stands again.
			name: "an approval survives an expired heartbeat",
			evidence: Evidence{
				Heartbeat:        seen(SourceHeartbeat, ClaimBusy, 3*time.Second),
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimApprovalPending, 2*time.Second),
			},
			wantState:  protocol.SessionStatePendingApproval,
			wantReason: ReasonApprovalOpen,
		},
		{
			// A reviewer does not suppress the state — it only changes how long
			// the state must hold before anyone is told. See TestDwellFor.
			name: "a reviewer in the loop does not hide an approval",
			evidence: Evidence{
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimApprovalPending, time.Second),
				ReviewerInLoop:   true,
			},
			wantState:  protocol.SessionStatePendingApproval,
			wantReason: ReasonApprovalOpen,
		},
		{
			// A pending wakeup resumes the session with nobody doing anything, so
			// it is parked, not waiting on a person.
			// A wakeup on the calendar says nothing about whether the turn that
			// just ended left something for the user, so it does not get to
			// describe the outcome the classifier read out of the transcript.
			name: "a pending cron does not rename what the turn settled to",
			evidence: Evidence{
				PendingCron:    true,
				LastClassifier: seen(SourceClassifier, ClaimIdle, time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonClassifierVerdict,
		},
		{
			// And with no verdict to describe it, the wakeup names why the
			// session settled — still idle, still the user's to pick up. A user
			// who wants a loop to run unwatched pins the workspace, which is
			// filtered at read in internal/attention.
			name: "a pending cron names the settle without suppressing it",
			evidence: Evidence{
				PendingCron: true,
				Heartbeat:   seen(SourceHeartbeat, ClaimSettled, time.Second),
				LastBusyAt:  now.Add(-30 * time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonCronPending,
		},
		{
			// And the one legitimate exit: the wakeup fires, the agent runs
			// again, and a park that outranked a running turn would be a stuck
			// color of its own.
			name: "a parked session that starts running again is working",
			evidence: Evidence{
				PendingCron: true,
				Heartbeat:   seen(SourceHeartbeat, ClaimBusy, 100*time.Millisecond),
				LastBusyAt:  now.Add(-100 * time.Millisecond),
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonHeartbeatFresh,
		},
		{
			// An outstanding background task auto-resumes the turn, so the
			// session keeps working rather than flickering idle and back.
			name: "background work keeps a yielded turn working",
			evidence: Evidence{
				BackgroundWork: true,
				LastClassifier: seen(SourceClassifier, ClaimIdle, time.Second),
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonBackgroundWork,
		},
		{
			// Both facts arrive together on the same Stop payload, and the order
			// between them used to live in the daemon rule that read the payload
			// and applied a state itself. Work still running is the more useful
			// thing to say: the wakeup is not what resumes this turn.
			name: "outstanding work outranks a parked wakeup",
			evidence: Evidence{
				BackgroundWork: true,
				PendingCron:    true,
				LastMovement:   now.Add(-time.Second),
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonBackgroundWork,
		},
		{
			// Neither fact expires on its own — they are cleared by the next turn
			// opening or the next stop reporting otherwise — so total silence has
			// to be able to retire them. Otherwise background work that never
			// resumed anything is a green session for the rest of its life, which
			// is the failure the whole table exists to remove.
			name: "a yield that resumed nothing and went quiet is stuck",
			evidence: Evidence{
				BackgroundWork: true,
				TurnEverOpened: true,
				LastMovement:   now.Add(-91 * time.Second),
			},
			wantState:  protocol.SessionStateUnknown,
			wantReason: ReasonStuck,
		},

		{
			// The at-prompt clause claims the agent has never taken a turn, and a
			// classification is proof that it has: it only runs when one ended. A
			// daemon restarted mid-turn or a lost UserPromptSubmit leaves exactly
			// this shape — judged, with no bracket to show for it — and reading it
			// as a fresh prompt publishes idle over the verdict.
			name: "a judged turn is not a fresh prompt, whatever the brackets say",
			evidence: Evidence{
				Heartbeat:      seen(SourceHeartbeat, ClaimSettled, time.Second),
				LastClassifier: seen(SourceClassifier, ClaimNeedsInput, time.Second),
				LastBusyAt:     now.Add(-5 * time.Second),
			},
			wantState:  protocol.SessionStateWaitingInput,
			wantReason: ReasonClassifierVerdict,
		},
		{
			// And the same for a classification still in flight: the settle it is
			// holding must not be resolved as a session that never started.
			name: "a turn being judged right now is not a fresh prompt either",
			evidence: Evidence{
				Heartbeat:        seen(SourceHeartbeat, ClaimSettled, time.Second),
				ClassifyingSince: now.Add(-time.Second),
				LastBusyAt:       now.Add(-5 * time.Second),
			},
			wantHold:   true,
			wantReason: ReasonAwaitingVerdict,
		},

		// --- a question the harness announced --------------------------------

		{
			// Claude's AskUserQuestion hook. Its brackets close — nothing is
			// running while the question sits there — so without an edge of its
			// own the question would settle to idle and the one thing the user
			// needs to see would be the thing that disappears.
			name: "an announced question waits on the user",
			evidence: Evidence{
				TurnEverOpened:   true,
				Heartbeat:        seen(SourceHeartbeat, ClaimSettled, time.Second),
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimNeedsInput, 2*time.Second),
				LastBusyAt:       now.Add(-3 * time.Second),
			},
			wantState:  protocol.SessionStateWaitingInput,
			wantReason: ReasonQuestionOpen,
		},
		{
			// Answered: the agent is running again. Retired exactly like an
			// approval, by the agent painting a busy frame past the edge, so a
			// lost closing hook cannot leave the question standing forever.
			name: "a question the agent has gone busy past was answered",
			evidence: Evidence{
				TurnEverOpened:   true,
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimNeedsInput, 10*time.Second),
				Heartbeat:        seen(SourceHeartbeat, ClaimSettled, time.Second),
				LastBusyAt:       now.Add(-2 * time.Second),
				LastClassifier:   seen(SourceClassifier, ClaimIdle, time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonClassifierVerdict,
		},

		// --- settled turns --------------------------------------------------

		{
			name: "the classifier settles a finished turn to idle",
			evidence: Evidence{
				LastClassifier: seen(SourceClassifier, ClaimIdle, time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonClassifierVerdict,
		},
		{
			name: "the classifier settles a question to waiting_input",
			evidence: Evidence{
				LastClassifier: seen(SourceClassifier, ClaimNeedsInput, time.Second),
			},
			wantState:  protocol.SessionStateWaitingInput,
			wantReason: ReasonClassifierVerdict,
		},

		// --- nothing to go on ------------------------------------------------

		{
			// The diagnosis that did not exist before: a session nothing has said
			// anything about used to be indistinguishable from a quiet one.
			name: "evidence that stopped moving is stuck",
			evidence: Evidence{
				TurnEverOpened: true,
				LastMovement:   now.Add(-91 * time.Second),
			},
			wantState:  protocol.SessionStateUnknown,
			wantReason: ReasonStuck,
		},
		{
			// A session launched and left alone is silent because there is nothing
			// to report. Claude paints its title on activity and then goes quiet at
			// an empty prompt, with no Stop and no idle_prompt notification to
			// contradict a stuck verdict — witnessed live turning `unknown` ninety
			// seconds after launch.
			name: "a session that never took a turn is quiet, not stuck",
			evidence: Evidence{
				LastMovement: now.Add(-10 * time.Minute),
			},
			wantState:  protocol.SessionStateUnknown,
			wantReason: ReasonNoEvidence,
		},
		{
			// The hole an open bracket used to leave. heartbeatSilentFor answers "not
			// silent" for an agent that has never painted a busy frame — it has
			// nothing to have gone quiet from — so the bracket was believed on every
			// tick and the stuck clause below it was unreachable. An agent with hooks
			// and no title, or either agent whose title breaks, pinned itself green
			// for the rest of its life: exactly the stuck color this plan removes,
			// only with a reason to believe it.
			name: "an open bracket stops outranking stuck once everything goes quiet",
			evidence: Evidence{
				TurnOpen:       true,
				TurnEverOpened: true,
				LastMovement:   now.Add(-91 * time.Second),
			},
			wantState:  protocol.SessionStateUnknown,
			wantReason: ReasonStuck,
		},
		{
			// The bracket still wins while anything is arriving. This is the case that
			// keeps the fix above from settling healthy long turns: a tool call that
			// runs for minutes keeps its hooks and frames coming, and total silence is
			// a much stronger claim than "the spinner paused".
			name: "an open bracket still outranks stuck while evidence keeps arriving",
			evidence: Evidence{
				TurnOpen:       true,
				TurnEverOpened: true,
				LastBusyAt:     now.Add(-time.Second),
				LastMovement:   now.Add(-time.Second),
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonBracketOpen,
		},
		{
			name: "recent silence is not yet stuck",
			evidence: Evidence{
				LastMovement: now.Add(-30 * time.Second),
			},
			wantState:  protocol.SessionStateUnknown,
			wantReason: ReasonNoEvidence,
		},
		{
			// A session nothing has ever reported on is not stuck — it has not
			// had the chance to move.
			name:       "an empty evidence table reports no evidence",
			evidence:   Evidence{},
			wantState:  protocol.SessionStateUnknown,
			wantReason: ReasonNoEvidence,
		},

		// --- the user halting a turn ---------------------------------------

		{
			// No agent fires a hook on an interrupt, so the turn bracket is still
			// open and its heartbeat has only just gone quiet: every other clause
			// reads this as a turn in progress.
			name: "an aborted turn settles immediately, with its bracket still open",
			evidence: Evidence{
				TurnOpen:         true,
				TurnEverOpened:   true,
				Heartbeat:        seen(SourceHeartbeat, ClaimSettled, 200*time.Millisecond),
				LastBusyAt:       now.Add(-300 * time.Millisecond),
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimTurnAborted, 200*time.Millisecond),
				LastMovement:     now.Add(-200 * time.Millisecond),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonTurnAborted,
		},
		{
			// The abort outranks a classification still running: an interrupted turn
			// left a fragment, and holding for a verdict on it would sit on the
			// settle for the classifier's whole timeout.
			name: "an aborted turn does not wait for a verdict",
			evidence: Evidence{
				TurnEverOpened:   true,
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimTurnAborted, time.Second),
				ClassifyingSince: now.Add(-time.Second),
				LastMovement:     now.Add(-time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonTurnAborted,
		},
		{
			// Interrupting a compaction is the case with no other way out: nothing
			// but total silence retires `Compacting`, so it would hold the session
			// working for 90s.
			name: "an aborted turn outranks compaction",
			evidence: Evidence{
				TurnEverOpened:   true,
				Compacting:       true,
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimTurnAborted, time.Second),
				LastMovement:     now.Add(-time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonTurnAborted,
		},
		{
			// The user halted the turn and then started another one. The agent is
			// visibly running, so the abort is spent.
			name: "a busy frame after an abort retires it",
			evidence: Evidence{
				TurnOpen:         true,
				TurnEverOpened:   true,
				Heartbeat:        seen(SourceHeartbeat, ClaimBusy, 100*time.Millisecond),
				LastBusyAt:       now.Add(-100 * time.Millisecond),
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimTurnAborted, 3*time.Second),
				LastMovement:     now.Add(-100 * time.Millisecond),
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonHeartbeatFresh,
		},
		{
			// An abort is not an approval: the agent asked nothing, so the session
			// must not be reported as blocked on a person.
			name: "an aborted turn is not read as an outstanding approval",
			evidence: Evidence{
				TurnEverOpened:   true,
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimTurnAborted, time.Second),
				LastMovement:     now.Add(-time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonTurnAborted,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Resolve(tc.evidence, testPolicy(), now)
			if got.Hold != tc.wantHold {
				t.Fatalf("hold %v, want %v (got %s/%s)", got.Hold, tc.wantHold, got.State, got.Reason)
			}
			if got.State != tc.wantState || got.Reason != tc.wantReason {
				t.Fatalf("got %s/%s, want %s/%s", got.State, got.Reason, tc.wantState, tc.wantReason)
			}
		})
	}
}

// The heartbeat's TTL is what decides whether a repaint gap reads as "still
// running" or "stopped", so the boundary is the behavior, not an implementation
// detail. Exactly at the TTL the observation is still believed.
func TestHeartbeatFreshnessBoundary(t *testing.T) {
	policy := testPolicy()
	for _, tc := range []struct {
		age  time.Duration
		want protocol.SessionState
	}{
		{age: policy.HeartbeatTTL - time.Millisecond, want: protocol.SessionStateWorking},
		{age: policy.HeartbeatTTL, want: protocol.SessionStateWorking},
		// Past the TTL the frame stops outranking the edges below it, but a busy
		// frame going quiet is not yet a settle: the agent is held working until
		// the silence passes HeartbeatSettleAfter.
		{age: policy.HeartbeatTTL + time.Millisecond, want: protocol.SessionStateWorking},
		{age: policy.HeartbeatSettleAfter, want: protocol.SessionStateWorking},
		// Only a silence past the settle window says the turn is over. It resolves
		// idle rather than unknown because the heartbeat is real evidence of a
		// settle, not an absence of evidence.
		{age: policy.HeartbeatSettleAfter + time.Millisecond, want: protocol.SessionStateIdle},
	} {
		// LastBusyAt travels with a busy frame — the daemon stamps both — and a
		// settle needs a turn to have run.
		e := Evidence{
			Heartbeat:      seen(SourceHeartbeat, ClaimBusy, tc.age),
			LastBusyAt:     now.Add(-tc.age),
			TurnEverOpened: true,
		}
		if got := Resolve(e, policy, now); got.State != tc.want {
			t.Fatalf("heartbeat aged %s resolved %s, want %s", tc.age, got.State, tc.want)
		}
	}
}

// A repaint cadence wider than the TTL must not make the session flap.
//
// This is the shape of the live failure that produced the settle window. Claude
// repaints its title every ~1.92s while a `/compact` runs, and a compaction runs
// between turns, so the previous turn's bracket is already closed and the
// heartbeat is the only level in the table. Reading a gap past the TTL as a
// settle therefore produced a state change every second: idle when the frame
// aged out, working when the next one landed, for as long as the compaction ran.
//
// The cost was not the color. Every one of those idle edges opens a turn the
// user owes, so settling the session put it back in the queue a second later and
// there was no way to get it out.
func TestARepaintGapWiderThanTheTTLDoesNotFlapTheSession(t *testing.T) {
	policy := testPolicy()
	// Measured on claude 2.1.220 during a compaction, and periodic to the
	// millisecond. The property under test is that it sits between the two
	// windows, which is where every flap lives.
	const repaint = 1920 * time.Millisecond
	if repaint <= policy.HeartbeatTTL || repaint >= policy.HeartbeatSettleAfter {
		t.Fatalf("repaint %s must fall between the TTL %s and the settle window %s for this test to mean anything",
			repaint, policy.HeartbeatTTL, policy.HeartbeatSettleAfter)
	}

	// The resolver ticks once a second against evidence that only advances every
	// repaint, which is what makes the same evidence resolve twice at different
	// ages.
	for tick := 1; tick <= 30; tick++ {
		at := now.Add(time.Duration(tick) * time.Second)
		lastFrame := now.Add((at.Sub(now) / repaint) * repaint)
		e := Evidence{
			TurnEverOpened: true,
			Heartbeat:      &Observation{Source: SourceHeartbeat, Claim: ClaimBusy, ObservedAt: lastFrame},
			LastBusyAt:     lastFrame,
			LastMovement:   lastFrame,
		}
		if got := Resolve(e, policy, at); got.State != protocol.SessionStateWorking {
			t.Fatalf("tick %d: a busy frame %s old resolved %s/%s, want working: a repaint gap is not a settle",
				tick, at.Sub(lastFrame), got.State, got.Reason)
		}
	}
}

// Replays the shape behind every `unknown` observed on 2026-07-27: a turn that
// yielded with a background task, the harness confirming sixty seconds later
// that the agent is parked at its prompt, and then nothing at all.
//
// Three sessions produced ten of these in one day, each one ninety seconds after
// a confirmation the resolver had already recorded. The confirmation is the whole
// point — a session attn has been *told* the location of is not a session it has
// lost track of, so `stuck` is the one verdict this timeline must never reach.
func TestAPromptIdleConfirmationRetiresAnOutstandingBackgroundTask(t *testing.T) {
	policy := testPolicy()

	// The Stop payload that opens the timeline: a background task outstanding, and
	// a title frame that has already gone quiet because the turn is over.
	yielded := now
	at := func(d time.Duration) Evidence {
		e := Evidence{
			TurnEverOpened: true,
			BackgroundWork: true,
			Heartbeat:      &Observation{Source: SourceHeartbeat, Claim: ClaimSettled, ObservedAt: yielded},
			LastBusyAt:     yielded.Add(-time.Second),
			LastMovement:   yielded,
		}
		// The notification lands a minute in and moves the table with it.
		if d >= time.Minute {
			e.PromptIdleAt = yielded.Add(time.Minute)
			e.LastMovement = yielded.Add(time.Minute)
		}
		return e
	}

	// Before the confirmation the background task is all attn has, and it holds.
	if got := Resolve(at(30*time.Second), policy, yielded.Add(30*time.Second)); got.State != protocol.SessionStateWorking {
		t.Fatalf("30s in: resolved %s/%s, want working: an outstanding background task is the only fact so far",
			got.State, got.Reason)
	}

	// From the confirmation on, the session owes the user a turn and keeps owing
	// it. The sweep runs well past StuckAfter measured from the confirmation,
	// which is where every observed `unknown` landed.
	for _, d := range []time.Duration{
		time.Minute,
		time.Minute + 30*time.Second,
		time.Minute + policy.StuckAfter,
		time.Minute + policy.StuckAfter + time.Second,
		10 * time.Minute,
	} {
		got := Resolve(at(d), policy, yielded.Add(d))
		if got.State == protocol.SessionStateUnknown {
			t.Fatalf("%s in: resolved unknown/%s after the harness said the agent is at its prompt", d, got.Reason)
		}
		if got.State != protocol.SessionStateIdle || got.Reason != ReasonPromptIdle {
			t.Fatalf("%s in: resolved %s/%s, want idle/%s", d, got.State, got.Reason, ReasonPromptIdle)
		}
	}
}

// A finished turn is the user's to pick up whatever the calendar says. The
// wakeup names why the session settled and changes nothing else — suppressing
// the queue is a user control (a pinned workspace), not something the resolver
// infers from a schedule.
func TestAParkedWakeupDoesNotExcuseTheAgentFromTheQueue(t *testing.T) {
	policy := testPolicy()
	e := Evidence{
		TurnEverOpened: true,
		PendingCron:    true,
		LastBusyAt:     now.Add(-2 * time.Minute),
		LastMovement:   now.Add(-time.Minute),
	}

	got := Resolve(e, policy, now)
	if got.State != protocol.SessionStateIdle || got.Reason != ReasonCronPending {
		t.Fatalf("resolved %s/%s, want idle/cron_pending", got.State, got.Reason)
	}

	// The harness confirming it is at its prompt is the same answer, said twice.
	e.PromptIdleAt = now.Add(-time.Second)
	if got := Resolve(e, policy, now); got.State != protocol.SessionStateIdle || got.Reason != ReasonPromptIdle {
		t.Fatalf("resolved %s/%s after the confirmation, want idle/prompt_idle", got.State, got.Reason)
	}

	// And a background task outstanding alongside it is retired by the same
	// confirmation rather than reopening the settle.
	e.BackgroundWork = true
	if got := Resolve(e, policy, now); got.State != protocol.SessionStateIdle {
		t.Fatalf("resolved %s/%s with both facts set, want idle", got.State, got.Reason)
	}
}

// A session parked on a wakeup hours away is quiet because there is nothing to
// say, not because it stopped saying it — so silence must not decay into
// `unknown` here. The clauses that do diagnose a dead session (process exit, an
// open bracket gone silent) sit above this one and are untouched.
func TestAParkedWakeupDoesNotRotIntoUnknown(t *testing.T) {
	policy := testPolicy()
	parked := now.Add(-time.Minute)
	e := Evidence{
		TurnEverOpened: true,
		PendingCron:    true,
		LastBusyAt:     parked,
		LastMovement:   parked,
	}

	for _, quiet := range []time.Duration{
		policy.StuckAfter - time.Second,
		policy.StuckAfter,
		policy.StuckAfter + time.Second,
		10 * policy.StuckAfter,
	} {
		at := parked.Add(quiet)
		got := Resolve(e, policy, at)
		if got.State != protocol.SessionStateIdle || got.Reason != ReasonCronPending {
			t.Fatalf("resolved %s/%s after %s of silence, want idle/cron_pending", got.State, got.Reason, quiet)
		}
	}
}

// The heartbeat TTL is a settle-latency dial, not a safety margin, and this is
// the property that makes it safe to keep it short.
//
// The TTL was measured on an idle machine, so the standing worry was a PTY under
// backpressure batching its reads and stretching the gap between title frames
// past it. That cannot settle a session on its own: expiring the TTL only stops
// the heartbeat from *overriding* the brackets, and an open bracket is then
// governed by StaleAfter, which is an order of magnitude larger. A stretched gap
// therefore needs a second, independent fault — a lost turn-open hook, leaving
// the heartbeat as the only evidence — before it can show a wrong color, and the
// next title frame corrects it.
//
// Coupling the two windows would silently retire that margin, which is why the
// sweep runs across the whole span between them rather than at one point.
func TestHeartbeatTTLExpiryCannotSettleAnOpenBracket(t *testing.T) {
	policy := testPolicy()
	for _, age := range []time.Duration{
		policy.HeartbeatTTL + time.Millisecond,
		2 * policy.HeartbeatTTL,
		policy.StaleAfter - time.Millisecond,
		policy.StaleAfter,
	} {
		e := Evidence{
			TurnOpen:   true,
			Heartbeat:  seen(SourceHeartbeat, ClaimBusy, age),
			LastBusyAt: now.Add(-age),
		}
		got := Resolve(e, policy, now)
		if got.State != protocol.SessionStateWorking || got.Reason != ReasonBracketOpen {
			t.Fatalf("a %s gap past the %s TTL resolved %s/%s, want working/%s: the TTL must not close a bracket",
				age, policy.HeartbeatTTL, got.State, got.Reason, ReasonBracketOpen)
		}
	}
}

// Same for the stale window: a bracket holds until the silence passes it.
func TestBracketStalenessBoundary(t *testing.T) {
	policy := testPolicy()
	for _, tc := range []struct {
		age  time.Duration
		want Reason
	}{
		{age: policy.StaleAfter, want: ReasonBracketOpen},
		// Going stale does not settle immediately: the grace holds the current
		// state while a late explanation might still arrive.
		{age: policy.StaleAfter + time.Millisecond, want: ReasonSettleGrace},
		{age: policy.StaleAfter + policy.SettleGrace, want: ReasonSettleGrace},
		{age: policy.StaleAfter + policy.SettleGrace + time.Millisecond, want: ReasonBracketStale},
	} {
		e := Evidence{TurnOpen: true, Heartbeat: seen(SourceHeartbeat, ClaimBusy, tc.age), LastBusyAt: now.Add(-tc.age)}
		if got := Resolve(e, policy, now); got.Reason != tc.want {
			t.Fatalf("bracket with %s of silence resolved %s, want %s", tc.age, got.Reason, tc.want)
		}
	}
}

// Resolve is pure: it must not depend on anything but its arguments, which is
// what makes the tick safe to run as often as it likes.
func TestResolveIsStableForTheSameInputs(t *testing.T) {
	e := Evidence{
		TurnOpen:       true,
		Heartbeat:      seen(SourceHeartbeat, ClaimBusy, 2*time.Second),
		LastBusyAt:     now.Add(-2 * time.Second),
		LastClassifier: seen(SourceClassifier, ClaimIdle, time.Second),
		LastMovement:   now.Add(-time.Second),
	}
	first := Resolve(e, testPolicy(), now)
	for range 5 {
		if got := Resolve(e, testPolicy(), now); got != first {
			t.Fatalf("got %+v, want the same %+v", got, first)
		}
	}
}

// The winning observation's detail rides along so a diagnosis does not require
// re-reading the evidence table.
func TestResolutionCarriesTheWinningDetail(t *testing.T) {
	e := Evidence{Heartbeat: &Observation{
		Source:     SourceHeartbeat,
		Claim:      ClaimBusy,
		Detail:     "⠐ Run sleep command",
		ObservedAt: now,
	}}
	if got := Resolve(e, testPolicy(), now); got.Detail != "⠐ Run sleep command" {
		t.Fatalf("detail %q, want the heartbeat's title", got.Detail)
	}
}

func TestDwellFor(t *testing.T) {
	policy := testPolicy()

	// With a guardian answering first, showing the user an approval immediately
	// is the yellow flash on every tool call of an unattended run.
	if got := DwellFor(protocol.SessionStatePendingApproval, Evidence{ReviewerInLoop: true}, policy); got != policy.GuardianDwell {
		t.Fatalf("guardian dwell = %s, want %s", got, policy.GuardianDwell)
	}
	// With no guardian the user IS the reviewer: a genuine request must not be
	// delayed at all.
	if got := DwellFor(protocol.SessionStatePendingApproval, Evidence{}, policy); got != 0 {
		t.Fatalf("unattended approval dwell = %s, want 0", got)
	}
	// The dwell is about approvals specifically, not about attention in general.
	for _, state := range []protocol.SessionState{
		protocol.SessionStateWaitingInput,
		protocol.SessionStateWorking,
		protocol.SessionStateIdle,
	} {
		if got := DwellFor(state, Evidence{ReviewerInLoop: true}, policy); got != 0 {
			t.Fatalf("%s dwell = %s, want 0", state, got)
		}
	}
}

// The per-agent TTLs are measured, and getting them backwards would make codex
// (which repaints ~10x faster) the one held working on a stale glyph.
func TestPolicyForUsesTheMeasuredPerAgentTTL(t *testing.T) {
	claude := PolicyFor(string(protocol.SessionAgentClaude))
	codex := PolicyFor(string(protocol.SessionAgentCodex))

	if claude.HeartbeatTTL <= codex.HeartbeatTTL {
		t.Fatalf("claude TTL %s must exceed codex's %s: claude repaints ~1Hz, codex ~10Hz",
			claude.HeartbeatTTL, codex.HeartbeatTTL)
	}
	// An unmeasured agent must not accidentally inherit the most permissive TTL.
	if got := PolicyFor("copilot"); got.HeartbeatTTL > claude.HeartbeatTTL {
		t.Fatalf("unknown agent TTL %s, want no more than claude's %s", got.HeartbeatTTL, claude.HeartbeatTTL)
	}
	for _, policy := range []Policy{claude, codex, PolicyFor("copilot")} {
		if policy.StaleAfter <= 0 || policy.StuckAfter <= 0 || policy.GuardianDwell <= 0 {
			t.Fatalf("policy has an unset window: %+v", policy)
		}
		// A stale window inside the TTL would close brackets the heartbeat still
		// considers live, which is self-contradictory.
		if policy.StaleAfter <= policy.HeartbeatTTL {
			t.Fatalf("StaleAfter %s must exceed HeartbeatTTL %s", policy.StaleAfter, policy.HeartbeatTTL)
		}
		// The whole point of the settle window is that it is the wider of the two.
		// Collapsed onto the TTL, every repaint gap past the TTL settles the
		// session and the next frame revives it.
		if policy.HeartbeatSettleAfter <= policy.HeartbeatTTL {
			t.Fatalf("HeartbeatSettleAfter %s must exceed HeartbeatTTL %s",
				policy.HeartbeatSettleAfter, policy.HeartbeatTTL)
		}
	}
}

// The gap a compaction leaves is wide enough to look like a finished turn, and
// between turns there is no bracket left to say otherwise.
//
// The fallback that covers it today is HeartbeatSettleAfter, which infers the
// gap from the shape of the title frames. That inference stays — codex has no
// compaction hook, and nothing rules out another source of wide gaps — but where
// claude reports the fact, the fact should win: the heuristic settles a real
// end-of-turn that much later, and this does not.
func TestCompactionIsWorkNothingElseCanSee(t *testing.T) {
	policy := testPolicy()
	e := Evidence{
		TurnEverOpened: true,
		Compacting:     true,
		// Every other source says the turn is over: no bracket, and title frames
		// that stopped long enough to settle on their own.
		Heartbeat:    seen(SourceHeartbeat, ClaimSettled, 10*time.Second),
		LastBusyAt:   now.Add(-10 * time.Second),
		LastMovement: now.Add(-time.Second),
	}

	got := Resolve(e, policy, now)
	if got.State != protocol.SessionStateWorking || got.Reason != ReasonCompacting {
		t.Fatalf("resolved %s/%s while compacting, want working/compacting", got.State, got.Reason)
	}

	// And it is a claim that something is running, so it expires like every other
	// one: a PostCompact that never arrives must not pin the session green.
	e.LastMovement = now.Add(-policy.StuckAfter - time.Second)
	if got := Resolve(e, policy, now); got.State != protocol.SessionStateUnknown {
		t.Fatalf("resolved %s/%s after total silence, want unknown: a lost PostCompact must not hold working forever", got.State, got.Reason)
	}
}

// A turn cut off by the API produced nothing and cannot resume until a person
// acts — waits out a rate limit, pays a bill, renews a login. Every other signal
// makes that indistinguishable from an agent that finished and went quiet, which
// is the reading that leaves the user waiting on a session that is not coming
// back.
func TestATurnKilledByTheAPIAsksForTheUser(t *testing.T) {
	policy := testPolicy()
	e := Evidence{
		TurnEverOpened: true,
		LastHarnessEvent: &Observation{
			Source:     SourceHarnessEvent,
			Claim:      ClaimStopFailed,
			Detail:     "rate_limit: usage limit reached",
			ObservedAt: now.Add(-time.Second),
		},
		LastBusyAt:   now.Add(-5 * time.Second),
		LastMovement: now.Add(-time.Second),
	}

	got := Resolve(e, policy, now)
	if got.State != protocol.SessionStateWaitingInput || got.Reason != ReasonStopFailed {
		t.Fatalf("resolved %s/%s, want waiting_input/stop_failed", got.State, got.Reason)
	}
	if got.Detail != "rate_limit: usage limit reached" {
		t.Fatalf("detail = %q, want the error carried through: which failure it was is the part worth reading", got.Detail)
	}

	// Retired by the agent running again, which is the right edge whether the
	// user fixed the cause or simply re-prompted.
	e.Heartbeat = seen(SourceHeartbeat, ClaimBusy, 100*time.Millisecond)
	e.LastBusyAt = now.Add(-100 * time.Millisecond)
	if got := Resolve(e, policy, now); got.State != protocol.SessionStateWorking {
		t.Fatalf("resolved %s/%s once the agent ran again, want working", got.State, got.Reason)
	}
}

// The regression this whole clause exists for. An interrupt fires no hook on any
// agent, so the turn bracket opened by the prompt is never closed: without the
// abort edge, the only thing that retires it is StaleAfter, and the session shows
// the halted agent as working for that entire window. Written against the clock
// rather than against a single instant because the wrong answer here was not a
// wrong state, it was a right state that arrived a minute late.
func TestHaltingATurnSettlesItWithoutWaitingOutTheStaleWindow(t *testing.T) {
	policy := testPolicy()
	// Measured on claude 2.1.220: the last busy frame lands just before ESC, the
	// idle glyph 0.07s after it, and nothing at all after that.
	abortedAt := now
	e := Evidence{
		TurnOpen:       true,
		TurnEverOpened: true,
		LastBusyAt:     abortedAt.Add(-300 * time.Millisecond),
		Heartbeat: &Observation{
			Source:     SourceHeartbeat,
			Claim:      ClaimSettled,
			ObservedAt: abortedAt.Add(70 * time.Millisecond),
		},
		LastHarnessEvent: &Observation{
			Source:     SourceHarnessEvent,
			Claim:      ClaimTurnAborted,
			Detail:     "[Request interrupted by user]",
			ObservedAt: abortedAt,
		},
		LastMovement: abortedAt.Add(70 * time.Millisecond),
	}

	// Every point across the window the bracket would otherwise have held, and
	// past the one where evidence silence would have called it stuck.
	for _, age := range []time.Duration{
		time.Second,
		policy.StaleAfter,
		policy.StaleAfter + policy.SettleGrace + time.Second,
		policy.StuckAfter + time.Second,
	} {
		got := Resolve(e, policy, abortedAt.Add(age))
		if got.State != protocol.SessionStateIdle || got.Reason != ReasonTurnAborted {
			t.Fatalf("%s after the halt: resolved %s/%s, want idle/turn_aborted", age, got.State, got.Reason)
		}
		if got.Detail != "[Request interrupted by user]" {
			t.Fatalf("%s after the halt: detail = %q, want what the transcript said", age, got.Detail)
		}
	}
}
