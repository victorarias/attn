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
		HeartbeatTTL:      time.Second,
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
			name: "a pending cron parks the session",
			evidence: Evidence{
				PendingCron:    true,
				LastClassifier: seen(SourceClassifier, ClaimIdle, time.Second),
			},
			wantState:  protocol.SessionStateScheduled,
			wantReason: ReasonCronPending,
		},
		{
			// The park used to be defended by a per-driver veto against the
			// screen scraper knocking it to idle. The scraper is gone and the
			// defense is now clause order: a settled turn is what a parked
			// session looks like, so settling must not outrank the wakeup.
			name: "a parked session stays parked once its turn settles",
			evidence: Evidence{
				PendingCron: true,
				Heartbeat:   seen(SourceHeartbeat, ClaimSettled, time.Second),
				LastBusyAt:  now.Add(-30 * time.Second),
			},
			wantState:  protocol.SessionStateScheduled,
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

		// --- a long run nobody has read yet ---------------------------------

		{
			// The deferral: a turn worth minutes of work ended and attn is holding
			// the verdict until someone opens the session. Calling it idle would
			// file that result away as seen, which is what happened while this was
			// a state the deferral applied and the resolver overwrote.
			name: "a long run awaiting review waits on the user",
			evidence: Evidence{
				AwaitingLongRunReview: true,
				TurnEverOpened:        true,
				Heartbeat:             seen(SourceHeartbeat, ClaimSettled, time.Second),
				LastBusyAt:            now.Add(-time.Minute),
			},
			wantState:  protocol.SessionStateWaitingInput,
			wantReason: ReasonLongRunReview,
		},
		{
			// It describes a turn that has ended. A session that has started
			// another one is working, whatever is still owed on the last.
			name: "a long run awaiting review does not outrank a new turn",
			evidence: Evidence{
				AwaitingLongRunReview: true,
				TurnOpen:              true,
				TurnEverOpened:        true,
				Heartbeat:             seen(SourceHeartbeat, ClaimBusy, 100*time.Millisecond),
				LastBusyAt:            now.Add(-100 * time.Millisecond),
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonHeartbeatFresh,
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
		// Past the TTL the agent is no longer visibly running, and with no bracket
		// open there is nothing outstanding: the turn is over. It resolves idle
		// rather than unknown because the heartbeat is real evidence of a settle,
		// not an absence of evidence.
		{age: policy.HeartbeatTTL + time.Millisecond, want: protocol.SessionStateIdle},
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
	}
}

// IdleStale is the rule behind the mark: a finished result nobody has looked at
// stops counting as done. The cases that matter are the ones where it must stay
// quiet — a session that is still running has produced nothing to miss, and a
// result already read is not something to be reminded about twice.
func TestIdleStaleOnlyFiresForAnUnreadFinishedResult(t *testing.T) {
	policy := PolicyFor(string(protocol.SessionAgentClaude))
	settled := time.Now()
	past := settled.Add(policy.IdleStaleAfter + time.Second)

	cases := []struct {
		name       string
		state      protocol.SessionState
		stateSince time.Time
		lastRead   time.Time
		now        time.Time
		want       bool
	}{
		{"unread past the window", protocol.SessionStateIdle, settled, time.Time{}, past, true},
		{"read before it finished", protocol.SessionStateIdle, settled, settled.Add(-time.Hour), past, true},
		{"still inside the window", protocol.SessionStateIdle, settled, time.Time{}, settled.Add(time.Minute), false},
		{"read after it finished", protocol.SessionStateIdle, settled, settled.Add(time.Second), past, false},
		{"read at the same instant", protocol.SessionStateIdle, settled, settled, past, false},
		{"still working", protocol.SessionStateWorking, settled, time.Time{}, past, false},
		{"waiting on the user", protocol.SessionStateWaitingInput, settled, time.Time{}, past, false},
		{"never had a state", protocol.SessionStateIdle, time.Time{}, time.Time{}, past, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IdleStale(tc.state, tc.stateSince, tc.lastRead, policy, tc.now); got != tc.want {
				t.Fatalf("IdleStale = %v, want %v", got, tc.want)
			}
		})
	}

	// A policy with no window turns the whole thing off rather than firing
	// instantly, which is what an agent with no measured numbers would get if the
	// default were ever dropped.
	if IdleStale(protocol.SessionStateIdle, settled, time.Time{}, Policy{}, past) {
		t.Fatal("a zero window marked a session stale instead of disabling the rule")
	}
}
