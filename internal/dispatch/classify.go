// Package dispatch holds the single source of truth for the chief-of-staff
// dispatch *signal definition*: given a dispatch's current state, what — if
// anything — is worth waking the chief for.
//
// This classification is deliberately factored out of any one command so that
// `attn dispatch watch`, a future non-Claude poll path, and `dispatch status`
// can all reuse the exact same definition. "Done" (and "blocked", and "failed")
// must never be defined twice.
//
// # The signal definition
//
// A dispatch carries two independent state dimensions, both projected onto
// protocol.ChiefOfStaffDispatch when it is read:
//
//   - Status: the delegated session's projected runtime state — launching,
//     working, pending_approval, waiting_input, idle, scheduled, unknown — or
//     the sentinel "closed" once the session is gone. This comes from attn's
//     PTY/stop classifier.
//   - StructuredReport: the coordination envelope the delegated agent explicitly
//     files via `attn dispatch report --coordination-file` (work_state,
//     report_type, an optional decision Request, summary, next_action).
//
// The agent's explicit declaration (StructuredReport) is authoritative for what
// an event *means*; the runtime Status is authoritative for liveness/death and
// is the fallback when no report pins the meaning down.
//
// Classify maps those two dimensions onto exactly the events the vision says are
// worth a chief notification, and nothing else. In priority order:
//
//  1. report work_state=failed OR report_type=failure  -> Failed   (terminal)
//  2. report work_state=completed OR report_type=completion -> Done (terminal)
//  3. report has a pending decision Request            -> Blocker  (interim)
//  4. report work_state=needs_input OR report_type=blocker -> Blocker (interim)
//  5. report work_state=ready_for_review              -> Review   (terminal)
//  6. status=closed (session gone, no clean completion) -> Failed  (terminal)
//  7. status=idle (agent stopped; attn defines idle = done) -> Done (terminal)
//  8. status=waiting_input (stopped, needs direction) -> Blocker  (interim)
//  9. everything else                                 -> None     (no emit)
//
// Step 9 is the crux: it swallows routine tool-permission prompts
// (status=pending_approval), plus working/launching/scheduled/unknown. A routine
// "approve this tool call" is NOT a decision worth waking the chief for, and it
// is cleanly distinguishable from a genuine decision-request: the former is the
// runtime status pending_approval; the latter is an explicit structured
// Request / needs_input declaration (steps 3-4). The two come from different
// data sources, so the exclusion is expressible from existing state.
//
// "Silence != success": every way a dispatch can end is covered. A session that
// dies without a completion report (status=closed, step 6) emits Failed and
// exits — it never hangs silently. Only the interim Blocker kinds keep a watch
// alive, and only while the agent is genuinely waiting on the chief; if that
// session then dies, step 6 still fires.
package dispatch

import "github.com/victorarias/attn/internal/protocol"

// SessionClosedStatus is the sentinel ChiefOfStaffDispatch.Status value the
// daemon projects once the delegated session is gone. The daemon's decorate path
// is the producer; this package is the consumer, so the literal lives here as
// the single source of truth.
const SessionClosedStatus = "closed"

// EventKind is the category of a meaningful dispatch event.
type EventKind string

const (
	// KindNone is a routine, non-emitting state (the tool-permission exclusion
	// and all mid-flight runtime states).
	KindNone EventKind = "none"
	// KindDone is a successful terminal completion with a report ready.
	KindDone EventKind = "done"
	// KindBlocker is a genuine blocker / decision-request: the agent needs the
	// chief's judgement to proceed. Interim — a watch keeps running through it.
	KindBlocker EventKind = "blocker"
	// KindReview is a reviewable deliverable handed back for the chief to look
	// at. Terminal — the agent considers its push done.
	KindReview EventKind = "review"
	// KindFailed is a failure, crash, or abnormal/unconfirmed termination.
	KindFailed EventKind = "failed"
)

// Event is the classification of a dispatch snapshot.
type Event struct {
	Kind EventKind
	// Terminal reports whether a watch should stop after this event.
	Terminal bool
	// ExitCode is the process exit code a watch should use when Terminal: 0 for
	// a clean end (done/review), non-zero for a failure end. Meaningless when
	// not Terminal.
	ExitCode int
	// Reason is a short stable token naming the trigger (e.g. "reported_failure",
	// "session_closed", "decision_request"). Stable across releases for logs.
	Reason string
	// Summary is the concise human gist to surface (the report summary, or the
	// decision question for a blocker).
	Summary string
	// NextAction is the agent's stated next action, when present.
	NextAction string
}

// Classify maps a dispatch snapshot to its signal Event. It is a pure function
// of the snapshot — the single source of truth described in the package doc.
func Classify(d protocol.ChiefOfStaffDispatch) Event {
	summary := dispatchSummary(d)
	nextAction := trim(deref(structuredNextAction(d)))

	if r := d.StructuredReport; r != nil {
		switch {
		case r.WorkState == protocol.DispatchWorkStateFailed ||
			r.ReportType == protocol.DispatchReportTypeFailure:
			return Event{KindFailed, true, 1, "reported_failure", summary, nextAction}
		case r.WorkState == protocol.DispatchWorkStateCompleted ||
			r.ReportType == protocol.DispatchReportTypeCompletion:
			return Event{KindDone, true, 0, "reported_completion", summary, nextAction}
		}
		if req := r.Request; req != nil && req.Status == protocol.DispatchRequestStatusPending {
			s := summary
			if q := trim(req.Question); q != "" {
				s = q
			}
			return Event{KindBlocker, false, 0, "decision_request", s, nextAction}
		}
		switch {
		case r.WorkState == protocol.DispatchWorkStateNeedsInput ||
			r.ReportType == protocol.DispatchReportTypeBlocker:
			return Event{KindBlocker, false, 0, "needs_input", summary, nextAction}
		case r.WorkState == protocol.DispatchWorkStateReadyForReview:
			return Event{KindReview, true, 0, "ready_for_review", summary, nextAction}
		}
		// A report present but in_progress / progress / handoff does not pin the
		// meaning; fall through to the runtime status.
	}

	switch d.Status {
	case SessionClosedStatus:
		return Event{KindFailed, true, 1, "session_closed", summary, nextAction}
	case string(protocol.SessionStateIdle):
		return Event{KindDone, true, 0, "session_idle", summary, nextAction}
	case string(protocol.SessionStateWaitingInput):
		return Event{KindBlocker, false, 0, "awaiting_input", summary, nextAction}
	default:
		// working, launching, pending_approval (the exclusion), scheduled, unknown.
		return Event{Kind: KindNone}
	}
}

// IsTerminalReport reports whether a structured report represents a finished
// dispatch (completed or failed). This is the shared definition of a terminal
// *report* — needs_input / ready_for_review / in_progress are mid-flight. The
// daemon's durable-journal path and Classify both key off this one definition.
func IsTerminalReport(report *protocol.DispatchReport) bool {
	return report != nil &&
		(report.WorkState == protocol.DispatchWorkStateCompleted ||
			report.WorkState == protocol.DispatchWorkStateFailed)
}

// dispatchSummary picks the most concise human gist available: the daemon's
// derived ConciseSummary, else the structured report summary, else the freeform
// latest report.
func dispatchSummary(d protocol.ChiefOfStaffDispatch) string {
	if s := trim(deref(d.ConciseSummary)); s != "" {
		return s
	}
	if d.StructuredReport != nil {
		if s := trim(d.StructuredReport.Summary); s != "" {
			return s
		}
	}
	return trim(deref(d.LatestReport))
}

func structuredNextAction(d protocol.ChiefOfStaffDispatch) *string {
	if d.StructuredReport == nil {
		return nil
	}
	return d.StructuredReport.NextAction
}
