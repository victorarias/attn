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
// Doneness and failure are claimed by the agent, not inferred. Only an explicit
// structured report yields Done or Failed; the runtime Status governs liveness
// and the interim/neutral states.
//
// Classify maps those two dimensions onto exactly the events the vision says are
// worth a chief notification, and nothing else. In priority order:
//
//  1. report work_state=failed OR report_type=failure       -> Failed  (terminal, exit 1)
//  2. report work_state=completed OR report_type=completion  -> Done    (terminal, exit 0)
//  3. report work_state=ready_for_review                     -> Review  (terminal, exit 0)
//  4. status=closed, no explicit terminal outcome:
//     cut off mid-flight (close-state working/launching/pending_approval)
//     -> Failed  (terminal, exit 1)
//     else (clean rest idle/waiting_input, or unconfirmed) -> Ended   (terminal, exit 0)
//  5. report has a pending decision Request                  -> Blocker (interim)
//  6. report work_state=needs_input OR report_type=blocker   -> Blocker (interim)
//  7. status=idle, no explicit outcome                       -> Ended   (terminal, exit 0)
//  8. status=waiting_input                                   -> Blocker (interim)
//  9. everything else                                        -> None    (no emit)
//
// Step 9 is the crux of *noise* suppression: it swallows routine tool-permission
// prompts (status=pending_approval), plus working/launching/scheduled/unknown. A
// routine "approve this tool call" is NOT a decision worth waking the chief for,
// and it is cleanly distinguishable from a genuine decision-request: the former
// is the runtime status pending_approval; the latter is an explicit structured
// Request / needs_input declaration (steps 5-6). The two come from different data
// sources, so the exclusion is expressible from existing state.
//
// The agent's explicit terminal declarations — Failed, Done, and Review (steps
// 1-3) — are authoritative over runtime liveness: a report of completion,
// failure, or ready_for_review stands even once the session has closed, so a
// review handoff filed just before the agent exits is never lost. They sit ahead
// of the closed check (step 4) for exactly that reason.
//
// Silence implies NEITHER success nor failure. A dispatch that ends WITHOUT an
// explicit terminal report — its session simply closes (step 4), or the agent
// stops at idle without reporting (step 7) — is the neutral terminal kind Ended:
// it emits and exits (so a watch never hangs and every terminal state is
// covered), but it asserts no outcome. This is why the closed check (step 4) sits
// ahead of the *interim* report states (steps 5-6): those are non-terminal, so
// once the session is gone a stale needs_input is not a live blocker — a dead
// session must resolve to a neutral terminal instead of hanging on it. Review
// does not have that problem because it is itself terminal, which is why it can
// stay authoritative above the closed check.
//
// The one exception to silent-close-is-neutral is a CRASH: when a session closes
// while the agent was still cut off mid-flight (its captured close-state is
// working, launching, or pending_approval), step 4 surfaces Failed instead of
// Ended. This is not inferring failure from silence — it is positive evidence
// that the agent's process died before it could reach a resting point or file a
// report. The safe direction is preserved: a clean stop (idle / waiting_input) or
// an unconfirmed close (unknown / unstamped) is still neutral Ended, so attn
// never asserts a failure it cannot evidence, just as it never asserts an
// unearned success. The close-state is captured by the daemon the moment the
// process exits — see captureDispatchCloseState — and rides on ClosedState.
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
	// KindDone is a successful completion the agent explicitly declared.
	KindDone EventKind = "done"
	// KindBlocker is a genuine blocker / decision-request: the agent needs the
	// chief's judgement to proceed. Interim — a watch keeps running through it.
	KindBlocker EventKind = "blocker"
	// KindReview is a reviewable deliverable handed back for the chief to look
	// at. Terminal — the agent considers its push done.
	KindReview EventKind = "review"
	// KindEnded is a neutral terminal: the dispatch ended without an explicit
	// outcome (its session closed, or it stopped at idle without reporting). It
	// implies neither success nor failure — drill into the narrative to know.
	KindEnded EventKind = "ended"
	// KindFailed is a failure the agent explicitly declared.
	KindFailed EventKind = "failed"
)

// Event is the classification of a dispatch snapshot.
type Event struct {
	Kind EventKind
	// Terminal reports whether a watch should stop after this event.
	Terminal bool
	// ExitCode is the process exit code a watch should use when Terminal: 0 for
	// any non-failure terminal (done/review/ended), non-zero only for an
	// explicitly declared failure. Meaningless when not Terminal.
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

	// 1-3. Explicit TERMINAL outcome the agent declared — authoritative even if the
	// session has since closed (a completed-, failed-, or ready_for_review-then-
	// closed dispatch keeps that outcome). All three are terminal, so keeping them
	// ahead of the closed check cannot hang a watch; it only preserves the handoff
	// a report filed just before the agent exits would otherwise lose to the race.
	if r := d.StructuredReport; r != nil {
		switch {
		case r.WorkState == protocol.DispatchWorkStateFailed ||
			r.ReportType == protocol.DispatchReportTypeFailure:
			return Event{KindFailed, true, 1, "reported_failure", summary, nextAction}
		case r.WorkState == protocol.DispatchWorkStateCompleted ||
			r.ReportType == protocol.DispatchReportTypeCompletion:
			return Event{KindDone, true, 0, "reported_completion", summary, nextAction}
		case r.WorkState == protocol.DispatchWorkStateReadyForReview:
			return Event{KindReview, true, 0, "ready_for_review", summary, nextAction}
		}
	}

	// 4. Session gone with no explicit terminal outcome. Checked BEFORE the interim
	// report states (steps 5-6) so a dead session never hangs (a stale needs_input is
	// not a live blocker once the agent is gone). By default a silent close is the
	// neutral terminal Ended — silence is neither success nor failure. But when the
	// daemon captured a close-state showing the agent was cut off mid-flight (still
	// working, launching, or awaiting a tool approval when its process died), that is
	// a real crash/kill: surface it as a failure so a killed agent is visible, not
	// quietly neutral. A clean rest (idle / waiting_input) or an unconfirmed close
	// (unknown / unstamped) stays neutral — attn never asserts a failure it cannot
	// evidence, exactly as it never asserts an unearned success.
	if d.Status == SessionClosedStatus {
		if closedMidFlight(d.ClosedState) {
			return Event{KindFailed, true, 1, "session_crashed", summary, nextAction}
		}
		return Event{KindEnded, true, 0, "session_closed", summary, nextAction}
	}

	// 5-6. Live, explicit INTERIM (non-terminal) report states.
	if r := d.StructuredReport; r != nil {
		if req := r.Request; req != nil && req.Status == protocol.DispatchRequestStatusPending {
			s := summary
			if q := trim(req.Question); q != "" {
				s = q
			}
			return Event{KindBlocker, false, 0, "decision_request", s, nextAction}
		}
		if r.WorkState == protocol.DispatchWorkStateNeedsInput ||
			r.ReportType == protocol.DispatchReportTypeBlocker {
			return Event{KindBlocker, false, 0, "needs_input", summary, nextAction}
		}
	}

	// 7-9. Live runtime resting states with no actionable report. idle is a stop
	// without a structured outcome, so it is neutral Ended too — attn does not
	// claim a success the agent never declared.
	switch d.Status {
	case string(protocol.SessionStateIdle):
		return Event{KindEnded, true, 0, "session_idle", summary, nextAction}
	case string(protocol.SessionStateWaitingInput):
		return Event{KindBlocker, false, 0, "awaiting_input", summary, nextAction}
	default:
		// working, launching, pending_approval (the exclusion), scheduled, unknown.
		return Event{Kind: KindNone}
	}
}

// closedMidFlight reports whether a closed dispatch's captured ClosedState shows
// the agent was cut off mid-flight — still working, launching, or awaiting a tool
// approval — rather than at a settled rest (idle / waiting_input) or an
// unconfirmed close (unknown, or unstamped: nil/empty). It is the positive-
// evidence test that turns a real crash/kill into a visible failure while leaving
// every clean or ambiguous close neutral. The unstamped case returning false
// preserves the prior behavior (a silent close is Ended) for legacy records and
// any removal path that never captured a state.
func closedMidFlight(closedState *string) bool {
	switch protocol.SessionState(trim(deref(closedState))) {
	case protocol.SessionStateWorking,
		protocol.SessionStateLaunching,
		protocol.SessionStatePendingApproval:
		return true
	default:
		return false
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
