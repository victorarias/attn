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
		report       *protocol.DispatchReport
		wantKind     EventKind
		wantTerminal bool
		wantExit     int
		wantReason   string
	}{
		// --- explicit completion → done (the agent SAID it finished) ---
		// The runtime status is irrelevant: the trigger keys on the report alone.
		{"reported completion", "working", report(protocol.DispatchWorkStateCompleted, protocol.DispatchReportTypeCompletion), KindDone, true, 0, "reported_completion"},
		{"completion type only", "working", report(protocol.DispatchWorkStateInProgress, protocol.DispatchReportTypeCompletion), KindDone, true, 0, "reported_completion"},
		{"completed stands even once closed", SessionClosedStatus, report(protocol.DispatchWorkStateCompleted, protocol.DispatchReportTypeCompletion), KindDone, true, 0, "reported_completion"},
		{"ready for review = terminal handoff", "working", report(protocol.DispatchWorkStateReadyForReview, protocol.DispatchReportTypeProgress), KindReview, true, 0, "ready_for_review"},
		{"ready_for_review stands even once closed", SessionClosedStatus, report(protocol.DispatchWorkStateReadyForReview, protocol.DispatchReportTypeProgress), KindReview, true, 0, "ready_for_review"},

		// --- no self-report → silence, regardless of runtime status. Runtime state
		// (idle / closed / waiting_input / working / …) is NEVER a trigger now. ---
		{"idle without report = silent", string(protocol.SessionStateIdle), nil, KindNone, false, 0, ""},
		{"idle after progress report = silent", string(protocol.SessionStateIdle), report(protocol.DispatchWorkStateInProgress, protocol.DispatchReportTypeProgress), KindNone, false, 0, ""},
		{"session closed without report = silent", SessionClosedStatus, nil, KindNone, false, 0, ""},
		{"session closed after progress report = silent", SessionClosedStatus, report(protocol.DispatchWorkStateInProgress, protocol.DispatchReportTypeProgress), KindNone, false, 0, ""},
		{"waiting_input without report = silent (the #399 misfire, now killed)", string(protocol.SessionStateWaitingInput), nil, KindNone, false, 0, ""},

		// --- a self-reported blocker stands even once the session is gone. The
		// dispatch hangs on it (accepted crash-while-blocked gap); only an explicit
		// terminal report or a deleted record ends the watch. ---
		{"needs_input stands even once closed", SessionClosedStatus, report(protocol.DispatchWorkStateNeedsInput, protocol.DispatchReportTypeProgress), KindBlocker, false, 0, "needs_input"},
		{"decision request stands even once closed", SessionClosedStatus, pendingRequest, KindBlocker, false, 0, "decision_request"},

		// --- genuine blocker / decision-request (interim, live) ---
		{"pending decision request", "waiting_input", pendingRequest, KindBlocker, false, 0, "decision_request"},
		{"needs_input work state", "working", report(protocol.DispatchWorkStateNeedsInput, protocol.DispatchReportTypeProgress), KindBlocker, false, 0, "needs_input"},
		{"blocker report type", "working", report(protocol.DispatchWorkStateInProgress, protocol.DispatchReportTypeBlocker), KindBlocker, false, 0, "needs_input"},

		// --- explicit failure → failed (the agent SAID it failed) ---
		{"reported failure", "working", report(protocol.DispatchWorkStateFailed, protocol.DispatchReportTypeFailure), KindFailed, true, 1, "reported_failure"},
		{"failure type only", "working", report(protocol.DispatchWorkStateInProgress, protocol.DispatchReportTypeFailure), KindFailed, true, 1, "reported_failure"},

		// --- the exclusion: routine tool-permission prompt (category MUST NOT emit) ---
		{"pending_approval is routine, no emit", string(protocol.SessionStatePendingApproval), nil, KindNone, false, 0, ""},
		{"pending_approval with stale progress report", string(protocol.SessionStatePendingApproval), report(protocol.DispatchWorkStateInProgress, protocol.DispatchReportTypeProgress), KindNone, false, 0, ""},

		// --- other routine runtime states, no emit ---
		{"working", string(protocol.SessionStateWorking), nil, KindNone, false, 0, ""},
		{"launching", string(protocol.SessionStateLaunching), nil, KindNone, false, 0, ""},
		{"scheduled (will auto-resume)", string(protocol.SessionStateScheduled), nil, KindNone, false, 0, ""},
		{"unknown (do not cry wolf)", string(protocol.SessionStateUnknown), nil, KindNone, false, 0, ""},

		// --- a resolved request is no longer a blocker ---
		{"resolved request is silent", "working", resolvedRequest, KindNone, false, 0, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := protocol.ChiefOfStaffDispatch{Status: tt.status, StructuredReport: tt.report}
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
	// dispatchSummary is consulted only for an emitting (self-reported) event, so
	// every case carries a terminal report; only the summary SOURCE varies.
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

	// No concise summary and an empty structured summary → fall back to the
	// freeform latest report.
	d.StructuredReport.Summary = ""
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
