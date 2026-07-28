package sessionstate

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// Resolve is a first-match-wins clause list, so its order is the design and not
// an implementation detail: every clause below is reachable only because the ones
// above it declined. Reordering two of them silently changes what colors appear
// on a real session, and no single-evidence case notices — a case that stages one
// clause's evidence passes wherever that clause sits.
//
// So these are conflicts. Each stages evidence two clauses both answer and names
// which one is entitled to. Read top to bottom, they are the trust order.
func TestClauseOrder(t *testing.T) {
	policy := testPolicy()

	for _, tc := range []struct {
		// why explains the trade, since a conflict is exactly where the reason a
		// clause exists stops being obvious from the clause itself.
		why        string
		evidence   Evidence
		wantState  protocol.SessionState
		wantReason Reason
	}{
		{
			why: "an exited process outranks everything: no amount of live-looking " +
				"evidence should color a dead session as alive, and the evidence " +
				"cannot refresh again to correct itself",
			evidence: Evidence{
				Process:          seen(SourceProcess, ClaimExited, time.Second),
				Heartbeat:        seen(SourceHeartbeat, ClaimBusy, 100*time.Millisecond),
				LastBusyAt:       now.Add(-100 * time.Millisecond),
				TurnOpen:         true,
				TurnEverOpened:   true,
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimApprovalPending, time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonProcessExited,
		},
		{
			why: "a running agent outranks its own approval request: it cannot be " +
				"blocked on the user while visibly working, so the request was " +
				"answered and its closing edge was lost",
			evidence: Evidence{
				Heartbeat:        seen(SourceHeartbeat, ClaimBusy, 100*time.Millisecond),
				LastBusyAt:       now.Add(-100 * time.Millisecond),
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimApprovalPending, 5*time.Second),
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonHeartbeatFresh,
		},
		{
			why: "an unanswered approval outranks a parked wakeup and an open " +
				"bracket alike: both describe a turn that will continue, and this " +
				"one says it will not continue until a person acts",
			evidence: Evidence{
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimApprovalPending, time.Second),
				PendingCron:      true,
				TurnOpen:         true,
				TurnEverOpened:   true,
				LastBusyAt:       now.Add(-2 * time.Second),
			},
			wantState:  protocol.SessionStatePendingApproval,
			wantReason: ReasonApprovalOpen,
		},
		{
			// A parked wakeup used to be read as a state and sat above the
			// classifier, so a turn that ended by asking the user something was
			// reported as `scheduled` and never opened a turn. A registered
			// wakeup is not an answer to whether the agent needs a person — it
			// is as true of an agent mid-turn as of one waiting on a reply — so
			// it only gets to name the outcome where nothing was asked.
			why: "a question the classifier read out of the transcript outranks a " +
				"parked wakeup: the wakeup will resume the session, but not with " +
				"the answer the turn stopped for",
			evidence: Evidence{
				LastClassifier: seen(SourceClassifier, ClaimNeedsInput, time.Second),
				PendingCron:    true,
				TurnEverOpened: true,
				LastBusyAt:     now.Add(-2 * time.Second),
			},
			wantState:  protocol.SessionStateWaitingInput,
			wantReason: ReasonClassifierVerdict,
		},
		{
			why: "an announced question outranks the classifier's guess about how " +
				"the turn ended: the harness said what it is waiting for, and the " +
				"classifier is inferring it from a transcript",
			evidence: Evidence{
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimNeedsInput, time.Second),
				LastClassifier:   seen(SourceClassifier, ClaimIdle, time.Second),
				TurnEverOpened:   true,
				LastBusyAt:       now.Add(-2 * time.Second),
			},
			wantState:  protocol.SessionStateWaitingInput,
			wantReason: ReasonQuestionOpen,
		},
		{
			// This clause used to run the other way, on the reasoning that the
			// background task would un-park the turn without anyone doing anything.
			// Two things retired that. It was not what the code delivered — the
			// belief had no way to persist, so it expired into `unknown` ninety
			// seconds later rather than holding the session working. And it was not
			// what happened: on 2026-07-27 three sessions produced ten of those
			// unknowns, and in every one the confirmation was right and the user,
			// not the task, resumed the turn.
			why: "the harness saying the agent is parked at its prompt outranks an " +
				"outstanding background task: the task is a guess about whether " +
				"anyone is waited on, and this is the harness answering it directly",
			evidence: Evidence{
				BackgroundWork: true,
				PromptIdleAt:   now.Add(-time.Second),
				LastBusyAt:     now.Add(-30 * time.Second),
				LastMovement:   now.Add(-time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonPromptIdle,
		},
		{
			why: "an outstanding background task still outranks everything below " +
				"while the harness has said nothing: without a confirmation the " +
				"task is the best account of why the session went quiet",
			evidence: Evidence{
				BackgroundWork: true,
				LastBusyAt:     now.Add(-30 * time.Second),
				LastMovement:   now.Add(-time.Second),
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonBackgroundWork,
		},
		{
			why: "the harness's confirmation that the agent sits at its prompt " +
				"outranks an open bracket: a bracket closes on a hook that may " +
				"never arrive, and this is a second hook, on a different trigger, " +
				"saying the same turn is over",
			evidence: Evidence{
				PromptIdleAt:   now.Add(-time.Second),
				TurnOpen:       true,
				TurnEverOpened: true,
				Heartbeat:      seen(SourceHeartbeat, ClaimBusy, 2*time.Second),
				LastBusyAt:     now.Add(-2 * time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonPromptIdle,
		},
		{
			why: "an open bracket outranks a settled heartbeat: claude paints a " +
				"not-busy glyph between tool calls and while a foreground tool is " +
				"still running, and settling on one of those frames reports a " +
				"finished turn that is still going",
			evidence: Evidence{
				TurnOpen:       true,
				TurnEverOpened: true,
				Heartbeat:      seen(SourceHeartbeat, ClaimSettled, 500*time.Millisecond),
				LastBusyAt:     now.Add(-time.Second),
				LastMovement:   now.Add(-500 * time.Millisecond),
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonBracketOpen,
		},
		{
			why: "total silence outranks an open bracket: the bracket is the one " +
				"level with no expiry of its own, so an agent with hooks and no " +
				"heartbeat would otherwise pin itself green for good",
			evidence: Evidence{
				TurnOpen:       true,
				TurnEverOpened: true,
				LastMovement:   now.Add(-91 * time.Second),
			},
			wantState:  protocol.SessionStateUnknown,
			wantReason: ReasonStuck,
		},
		{
			why: "and it does not outrank a first turn that has not happened: an " +
				"agent launched and left alone is quiet because there is nothing " +
				"to report, not because it stopped reporting",
			evidence: Evidence{
				Heartbeat:    seen(SourceHeartbeat, ClaimSettled, 91*time.Second),
				LastMovement: now.Add(-91 * time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonAtPrompt,
		},
	} {
		t.Run(tc.why, func(t *testing.T) {
			got := Resolve(tc.evidence, policy, now)
			if got.State != tc.wantState || got.Reason != tc.wantReason {
				t.Fatalf(
					"Resolve() = %s/%s, want %s/%s",
					got.State, got.Reason, tc.wantState, tc.wantReason,
				)
			}
		})
	}
}
