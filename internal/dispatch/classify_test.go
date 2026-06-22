package dispatch

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func ptr(s string) *string { return &s }

// report builds a structured report with the given work state / report type.
func report(work protocol.DispatchWorkState, kind protocol.DispatchReportType) *protocol.DispatchReport {
	return &protocol.DispatchReport{WorkState: work, ReportType: kind, Summary: "did the thing"}
}

func TestClassify(t *testing.T) {
	pendingRequest := &protocol.DispatchReport{
		WorkState:  protocol.DispatchWorkStateInProgress,
		ReportType: protocol.DispatchReportTypeProgress,
		Summary:    "mid-flight",
		Request: &protocol.DispatchDecisionRequest{
			Status:            protocol.DispatchRequestStatusPending,
			Question:          "Postgres or SQLite for the cache?",
			ExpectedResponder: "chief",
		},
	}
	resolvedRequest := &protocol.DispatchReport{
		WorkState:  protocol.DispatchWorkStateInProgress,
		ReportType: protocol.DispatchReportTypeProgress,
		Summary:    "mid-flight",
		Request: &protocol.DispatchDecisionRequest{
			Status:            protocol.DispatchRequestStatusResolved,
			Question:          "Postgres or SQLite?",
			ExpectedResponder: "chief",
		},
	}

	tests := []struct {
		name         string
		status       string
		closedState  string
		report       *protocol.DispatchReport
		wantKind     EventKind
		wantTerminal bool
		wantExit     int
		wantReason   string
	}{
		// --- explicit completion → done (the agent SAID it finished) ---
		{"reported completion", "working", "", report(protocol.DispatchWorkStateCompleted, protocol.DispatchReportTypeCompletion), KindDone, true, 0, "reported_completion"},
		{"completion type only", "working", "", report(protocol.DispatchWorkStateInProgress, protocol.DispatchReportTypeCompletion), KindDone, true, 0, "reported_completion"},
		{"completed then closed = done (report wins over close)", SessionClosedStatus, "", report(protocol.DispatchWorkStateCompleted, protocol.DispatchReportTypeCompletion), KindDone, true, 0, "reported_completion"},
		// A terminal report stays authoritative even when the close-state shows the
		// process was still mid-flight: an agent that files completion and is then
		// killed milliseconds later (close-state working) is Done, not a crash.
		{"completed then crash-closed = done (report beats crash close-state)", SessionClosedStatus, string(protocol.SessionStateWorking), report(protocol.DispatchWorkStateCompleted, protocol.DispatchReportTypeCompletion), KindDone, true, 0, "reported_completion"},
		{"ready for review = terminal handoff", "working", "", report(protocol.DispatchWorkStateReadyForReview, protocol.DispatchReportTypeProgress), KindReview, true, 0, "ready_for_review"},
		// An explicit ready_for_review is a TERMINAL outcome and stays authoritative
		// over a closed session, exactly like completion/failure — the review handoff
		// must survive the agent filing it and exiting before the watcher's next poll.
		{"ready_for_review then closed = review (report wins over close)", SessionClosedStatus, "", report(protocol.DispatchWorkStateReadyForReview, protocol.DispatchReportTypeProgress), KindReview, true, 0, "ready_for_review"},
		{"ready_for_review then crash-closed = review (report beats crash close-state)", SessionClosedStatus, string(protocol.SessionStatePendingApproval), report(protocol.DispatchWorkStateReadyForReview, protocol.DispatchReportTypeProgress), KindReview, true, 0, "ready_for_review"},

		// --- silence implies neither success nor failure → neutral Ended ---
		{"idle without report = ended (not done)", string(protocol.SessionStateIdle), "", nil, KindEnded, true, 0, "session_idle"},
		{"idle after progress report = ended", string(protocol.SessionStateIdle), "", report(protocol.DispatchWorkStateInProgress, protocol.DispatchReportTypeProgress), KindEnded, true, 0, "session_idle"},
		// An unstamped close (no captured close-state — a legacy record or a removal
		// path that never observed a state) stays neutral Ended, preserving the prior
		// behavior: silence is not failure.
		{"session closed unstamped = ended (not failed)", SessionClosedStatus, "", nil, KindEnded, true, 0, "session_closed"},
		{"session closed after progress report = ended", SessionClosedStatus, "", report(protocol.DispatchWorkStateInProgress, protocol.DispatchReportTypeProgress), KindEnded, true, 0, "session_closed"},
		// A dead session beats a stale INTERIM (non-terminal) report — never hang,
		// never mislabel. (A TERMINAL report like ready_for_review wins over close
		// instead; see the explicit case above.)
		{"needs_input then closed = ended (no hang)", SessionClosedStatus, "", report(protocol.DispatchWorkStateNeedsInput, protocol.DispatchReportTypeProgress), KindEnded, true, 0, "session_closed"},
		{"decision request then closed = ended", SessionClosedStatus, "", pendingRequest, KindEnded, true, 0, "session_closed"},

		// --- clean / unconfirmed close-states stay neutral Ended (no over-claim) ---
		// The agent reached a settled rest, or attn could not confirm how it ended,
		// so a silent close asserts nothing — exactly the prior behavior.
		{"closed from idle = ended (clean rest)", SessionClosedStatus, string(protocol.SessionStateIdle), nil, KindEnded, true, 0, "session_closed"},
		{"closed from waiting_input = ended (clean rest)", SessionClosedStatus, string(protocol.SessionStateWaitingInput), nil, KindEnded, true, 0, "session_closed"},
		{"closed from unknown = ended (unconfirmed)", SessionClosedStatus, string(protocol.SessionStateUnknown), nil, KindEnded, true, 0, "session_closed"},
		{"closed from scheduled = ended (will auto-resume, not a crash)", SessionClosedStatus, string(protocol.SessionStateScheduled), nil, KindEnded, true, 0, "session_closed"},

		// --- crash / cut-off close-states surface as Failed (real, evidenced) ---
		// The process died while the agent was still mid-flight: a visible failure,
		// not a quiet neutral end. This is the inverse of crying wolf — a killed agent
		// is surfaced; a clean one is not.
		{"closed mid-work = failed (crash visible)", SessionClosedStatus, string(protocol.SessionStateWorking), nil, KindFailed, true, 1, "session_crashed"},
		{"closed mid-launch = failed (cut off before running)", SessionClosedStatus, string(protocol.SessionStateLaunching), nil, KindFailed, true, 1, "session_crashed"},
		{"closed mid-tool-approval = failed (cut off awaiting approval)", SessionClosedStatus, string(protocol.SessionStatePendingApproval), nil, KindFailed, true, 1, "session_crashed"},
		{"closed mid-work after progress report = failed", SessionClosedStatus, string(protocol.SessionStateWorking), report(protocol.DispatchWorkStateInProgress, protocol.DispatchReportTypeProgress), KindFailed, true, 1, "session_crashed"},

		// --- genuine blocker / decision-request (interim, live) ---
		{"pending decision request", "waiting_input", "", pendingRequest, KindBlocker, false, 0, "decision_request"},
		{"needs_input work state", "working", "", report(protocol.DispatchWorkStateNeedsInput, protocol.DispatchReportTypeProgress), KindBlocker, false, 0, "needs_input"},
		{"blocker report type", "working", "", report(protocol.DispatchWorkStateInProgress, protocol.DispatchReportTypeBlocker), KindBlocker, false, 0, "needs_input"},
		{"waiting_input without report", string(protocol.SessionStateWaitingInput), "", nil, KindBlocker, false, 0, "awaiting_input"},

		// --- explicit failure → failed (the agent SAID it failed) ---
		{"reported failure", "working", "", report(protocol.DispatchWorkStateFailed, protocol.DispatchReportTypeFailure), KindFailed, true, 1, "reported_failure"},
		{"failure type only", "working", "", report(protocol.DispatchWorkStateInProgress, protocol.DispatchReportTypeFailure), KindFailed, true, 1, "reported_failure"},

		// --- the exclusion: routine tool-permission prompt (category MUST NOT emit) ---
		{"pending_approval is routine, no emit", string(protocol.SessionStatePendingApproval), "", nil, KindNone, false, 0, ""},
		{"pending_approval with stale progress report", string(protocol.SessionStatePendingApproval), "", report(protocol.DispatchWorkStateInProgress, protocol.DispatchReportTypeProgress), KindNone, false, 0, ""},

		// --- other routine runtime states, no emit ---
		{"working", string(protocol.SessionStateWorking), "", nil, KindNone, false, 0, ""},
		{"launching", string(protocol.SessionStateLaunching), "", nil, KindNone, false, 0, ""},
		{"scheduled (will auto-resume)", string(protocol.SessionStateScheduled), "", nil, KindNone, false, 0, ""},
		{"unknown (do not cry wolf)", string(protocol.SessionStateUnknown), "", nil, KindNone, false, 0, ""},

		// --- a resolved request is no longer a blocker ---
		{"resolved request falls through to status", "working", "", resolvedRequest, KindNone, false, 0, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := protocol.ChiefOfStaffDispatch{Status: tt.status, StructuredReport: tt.report}
			if tt.closedState != "" {
				d.ClosedState = ptr(tt.closedState)
			}
			got := Classify(d)
			if got.Kind != tt.wantKind {
				t.Errorf("kind = %q, want %q", got.Kind, tt.wantKind)
			}
			if got.Terminal != tt.wantTerminal {
				t.Errorf("terminal = %v, want %v", got.Terminal, tt.wantTerminal)
			}
			if got.Terminal && got.ExitCode != tt.wantExit {
				t.Errorf("exit = %d, want %d", got.ExitCode, tt.wantExit)
			}
			if got.Kind != KindNone && got.Reason != tt.wantReason {
				t.Errorf("reason = %q, want %q", got.Reason, tt.wantReason)
			}
		})
	}
}

// The pending_approval exclusion is the entire reason a naive "emit on every
// status change" is wrong. A genuine decision-request and a routine tool prompt
// must classify differently from the SAME runtime moment.
func TestClassify_ApprovalExclusionIsExpressible(t *testing.T) {
	routine := protocol.ChiefOfStaffDispatch{Status: string(protocol.SessionStatePendingApproval)}
	if got := Classify(routine); got.Kind != KindNone {
		t.Fatalf("routine pending_approval must not emit, got %q", got.Kind)
	}

	genuine := protocol.ChiefOfStaffDispatch{
		Status: string(protocol.SessionStatePendingApproval),
		StructuredReport: &protocol.DispatchReport{
			WorkState: protocol.DispatchWorkStateNeedsInput,
			Summary:   "Need a decision on the schema",
			Request: &protocol.DispatchDecisionRequest{
				Status:            protocol.DispatchRequestStatusPending,
				Question:          "Which schema?",
				ExpectedResponder: "chief",
			},
		},
	}
	if got := Classify(genuine); got.Kind != KindBlocker {
		t.Fatalf("explicit decision-request must emit a blocker even while pending_approval, got %q", got.Kind)
	}
}

func TestClassify_SummaryPrefersConciseThenReportThenLatest(t *testing.T) {
	d := protocol.ChiefOfStaffDispatch{
		Status:         string(protocol.SessionStateIdle),
		ConciseSummary: ptr("concise wins"),
		LatestReport:   ptr("freeform loses"),
		StructuredReport: &protocol.DispatchReport{
			WorkState: protocol.DispatchWorkStateCompleted,
			Summary:   "report summary loses",
		},
	}
	if got := Classify(d).Summary; got != "concise wins" {
		t.Errorf("summary = %q, want concise", got)
	}

	d.ConciseSummary = nil
	if got := Classify(d).Summary; got != "report summary loses" {
		t.Errorf("summary = %q, want report summary", got)
	}

	d.StructuredReport = nil
	if got := Classify(d).Summary; got != "freeform loses" {
		t.Errorf("summary = %q, want latest report", got)
	}
}

func TestClassify_DecisionRequestSummaryIsTheQuestion(t *testing.T) {
	d := protocol.ChiefOfStaffDispatch{
		Status: "working",
		StructuredReport: &protocol.DispatchReport{
			WorkState: protocol.DispatchWorkStateInProgress,
			Summary:   "still going",
			Request: &protocol.DispatchDecisionRequest{
				Status:            protocol.DispatchRequestStatusPending,
				Question:          "Ship behind a flag or all at once?",
				ExpectedResponder: "chief",
			},
		},
	}
	if got := Classify(d).Summary; got != "Ship behind a flag or all at once?" {
		t.Errorf("blocker summary = %q, want the question", got)
	}
}

func TestIsTerminalReport(t *testing.T) {
	cases := []struct {
		work protocol.DispatchWorkState
		want bool
	}{
		{protocol.DispatchWorkStateCompleted, true},
		{protocol.DispatchWorkStateFailed, true},
		{protocol.DispatchWorkStateNeedsInput, false},
		{protocol.DispatchWorkStateReadyForReview, false},
		{protocol.DispatchWorkStateInProgress, false},
	}
	for _, c := range cases {
		if got := IsTerminalReport(&protocol.DispatchReport{WorkState: c.work}); got != c.want {
			t.Errorf("IsTerminalReport(%s) = %v, want %v", c.work, got, c.want)
		}
	}
	if IsTerminalReport(nil) {
		t.Error("IsTerminalReport(nil) = true, want false")
	}
}
