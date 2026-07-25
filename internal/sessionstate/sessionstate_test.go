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
		HeartbeatTTL:  time.Second,
		StaleAfter:    4 * time.Second,
		StuckAfter:    90 * time.Second,
		GuardianDwell: 60 * time.Second,
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
				Heartbeat:  seen(SourceHeartbeat, ClaimBusy, 5*time.Second),
				LastBusyAt: now.Add(-5 * time.Second),
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
				LastBusyAt: now.Add(-5 * time.Second),
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

		// --- screen (copilot only) ------------------------------------------

		{
			// Copilot has no harness signals, so the scrape is all there is —
			// but it ranks below every source that does have one.
			name: "the screen resolves when nothing better exists",
			evidence: Evidence{
				Screen: seen(SourceScreen, ClaimNeedsInput, time.Second),
			},
			wantState:  protocol.SessionStateWaitingInput,
			wantReason: ReasonScreen,
		},
		{
			// The scrape manufactures approvals from ordinary prose, which is why
			// it must never outrank the harness's own account of itself.
			name: "the classifier beats the screen",
			evidence: Evidence{
				Screen:         seen(SourceScreen, ClaimApprovalPending, 100*time.Millisecond),
				LastClassifier: seen(SourceClassifier, ClaimIdle, time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonClassifierVerdict,
		},

		// --- nothing to go on ------------------------------------------------

		{
			// The diagnosis that did not exist before: a session nothing has said
			// anything about used to be indistinguishable from a quiet one.
			name: "evidence that stopped moving is stuck",
			evidence: Evidence{
				LastMovement: now.Add(-91 * time.Second),
			},
			wantState:  protocol.SessionStateUnknown,
			wantReason: ReasonStuck,
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
		{age: policy.HeartbeatTTL + time.Millisecond, want: protocol.SessionStateUnknown},
	} {
		e := Evidence{Heartbeat: seen(SourceHeartbeat, ClaimBusy, tc.age)}
		if got := Resolve(e, policy, now); got.State != tc.want {
			t.Fatalf("heartbeat aged %s resolved %s, want %s", tc.age, got.State, tc.want)
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
		{age: policy.StaleAfter + time.Millisecond, want: ReasonBracketStale},
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
