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
// The trigger fires on the agent's SELF-REPORT and nothing else. A dispatch's
// runtime/daemon Status (working / waiting_input / idle / pending_approval /
// "closed" …) is deliberately NOT a trigger: it flickers (working↔waiting_input
// on a turn boundary) and misfires "ended" on a routine rest, which is exactly
// the noise this classifier exists to suppress. The chief peeks at runtime state
// on demand via `dispatch status` / `dispatch list`; the watch only fires on
// meaningful, agent-declared events.
//
// The only input Classify keys on is the StructuredReport — the coordination
// envelope the delegated agent explicitly files via `attn dispatch report`
// (`--done` / `--failed` / `--review` / `--blocked`, or `--coordination-file`):
// work_state, report_type, an optional decision Request, summary, next_action.
// Doneness, failure, review, and blocked are CLAIMED by the agent, never inferred.
//
// Classify maps the report onto exactly the events worth a chief notification, in
// priority order:
//
//  1. report work_state=failed OR report_type=failure       -> Failed  (terminal, exit 1)
//  2. report work_state=completed OR report_type=completion  -> Done    (terminal, exit 0)
//  3. report work_state=ready_for_review                     -> Review  (terminal, exit 0)
//  4. report has a pending decision Request                  -> Blocker (interim)
//  5. report work_state=needs_input OR report_type=blocker   -> Blocker (interim)
//  6. everything else (no report, a progress report, or any  -> None    (no emit)
//     runtime status whatsoever)
//
// Step 6 is the crux of *noise* suppression. A bare freeform `dispatch report
// --message` (a progress note) carries no structured terminal/blocked claim, so
// it classifies as None: it is a silent, on-demand-readable note, never a wake.
// Routine tool-permission prompts (runtime status pending_approval) are likewise
// not a trigger — only an explicit structured Request / needs_input declaration
// (steps 4-5) is. A genuine decision-request and a routine approval prompt thus
// classify differently from the same runtime moment, because the trigger reads
// the report, not the status.
//
// All five emitting events are self-reported, so there is no liveness race to
// arbitrate: a completion / failure / review / blocked claim stands on its own
// regardless of whether the session has since gone idle or closed. The cost is
// the deliberate, accepted gap: an agent that ends WITHOUT a terminal self-report
// (a crash, or simply forgetting) produces silence — a watch on it does not exit
// on its own. Silence implies neither success nor failure; the backstop for a
// genuinely stuck/dead agent is a chief-side watch timeout that escalates to the
// user (a deferred follow-up), NOT daemon-state inference here. The one non-report
// terminal the watch still honors is an actually-deleted dispatch RECORD (handled
// in the watch loop as dispatch_gone / not_found), which is definitive removal,
// not runtime-state flicker.
package dispatch

import "github.com/victorarias/attn/internal/protocol"

// SessionClosedStatus is the sentinel ChiefOfStaffDispatch.Status value the
// daemon projects once the delegated session is gone. It is a DISPLAY status only
// (shown in `dispatch status` / `list`); Classify deliberately does not key on it,
// because runtime state is not a trigger. The literal lives in this package as the
// single source of truth shared with the daemon's decorate path (the producer).
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
	// KindEnded is a neutral terminal used only when the dispatch RECORD itself is
	// gone (explicitly deleted, or never existed) — see the watch loop. It implies
	// neither success nor failure and is never produced from runtime/daemon state.
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
	// The trigger keys ONLY on the agent's self-report. With no structured report
	// there is nothing the agent has declared, so there is nothing to wake the
	// chief for — runtime/daemon Status is never a trigger (see the package doc).
	r := d.StructuredReport
	if r == nil {
		return Event{Kind: KindNone}
	}

	summary := dispatchSummary(d)
	nextAction := trim(deref(structuredNextAction(d)))

	// 1-3. Explicit TERMINAL outcome the agent declared.
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

	// 4-5. Explicit BLOCKED self-report (interim — a watch keeps running through
	// it so the chief can steer and the loop continues).
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

	// 6. A progress / in_progress report (or a resolved request) is a silent note,
	// not a trigger.
	return Event{Kind: KindNone}
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
