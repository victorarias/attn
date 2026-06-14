package daemon

import (
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/protocol"
)

// Auto-journaling of chief-of-staff dispatch outcomes.
//
// A dispatch is delegated agent work, and its terminal report (completed/failed)
// — or, as a fallback, its session simply ending — is exactly the "what was
// decided / built / failed" signal the durable work-journal exists to capture.
// This file renders that already-structured DispatchReport into a human-readable
// journal block and appends it to the daily journal:
//
//   - deterministic: the daemon renders structured fields, no LLM is involved;
//   - idempotent: a hidden per-dispatch marker means the terminal-report path, the
//     session-gone fallback, and a daemon restart together write exactly one block;
//   - grounded by construction: every block cites source: dispatch:<id>.
//
// This is the capture half of the notebook-as-work-journal: reliable, low-noise
// entries land without any agent having to remember to journal. LLM curation of
// these raw entries is a separate, later pass.

// journalDispatchMarker is the stable, hidden per-dispatch dedupe marker embedded
// in each auto-journaled block. It is an HTML comment so it renders invisibly in a
// markdown viewer while keeping the journal file self-describing — dedup needs no
// separate ledger and survives a daemon restart.
func journalDispatchMarker(dispatchID string) string {
	return fmt.Sprintf("<!-- attn:dispatch:%s -->", strings.TrimSpace(dispatchID))
}

// isTerminalDispatchReport reports whether a structured report represents a
// finished dispatch (completed or failed) — the point at which a dispatch is worth
// a durable journal entry. needs_input / ready_for_review / in_progress are
// mid-flight and are not journaled by the report path.
func isTerminalDispatchReport(report *protocol.DispatchReport) bool {
	return report != nil &&
		(report.WorkState == protocol.DispatchWorkStateCompleted ||
			report.WorkState == protocol.DispatchWorkStateFailed)
}

// journalDispatchOutcome renders a dispatch's outcome into the daily journal,
// exactly once. It is best-effort and safe to call from any dispatch-end path: a
// missing/unconfigured notebook, an empty dispatch, or an already-journaled
// dispatch is a silent no-op. Capture must never disrupt the dispatch lifecycle,
// so every failure is logged and swallowed.
//
// Best-effort has one accepted edge: if the write itself cannot land — an
// unwritable notebook root, or a day's journal that has reached notebook.MaxFileSize
// — the entry is logged and dropped, and since the marker was never written the
// later removal-path retry re-fails identically. So "exactly once" degrades to "at
// most once" precisely when the journal cannot be written at all. Given a block is
// a few KiB and fields are clamped, hitting the daily ceiling needs an implausible
// per-day dispatch volume; we accept that over carrying an overflow-spill mechanism.
func (d *Daemon) journalDispatchOutcome(dispatch *protocol.ChiefOfStaffDispatch) {
	if dispatch == nil {
		return
	}
	dateISO, block, ok := renderDispatchJournalEntry(dispatch, time.Now())
	if !ok {
		return // nothing meaningful to record
	}
	store, err := d.notebookStoreFor()
	if err != nil {
		d.logf("dispatch auto-journal: store unavailable: %v", err)
		return
	}
	rel, written, hash, err := store.AppendJournalEntryOnce(dateISO, journalDispatchMarker(dispatch.ID), block)
	if err != nil {
		d.logf("dispatch auto-journal %s: %v", dispatch.ID, err)
		return
	}
	if !written {
		return // already journaled — idempotent
	}
	// Suppress the watcher echo and surface the write like the CLI append path does.
	d.noteNotebookSelfWrite(notebook.SelfWrite{Rel: rel, Hash: hash})
	d.broadcastNotebookChanged(originAgent, rel)
}

// journalDispatchOnSessionGone journals the outcome of a dispatch whose target
// session is being removed — the reliability backstop for a worker that ended
// without a terminal report. It always attempts the write and lets the journal
// file's per-dispatch marker (checked under the notebook store lock in
// AppendJournalEntryOnce) be the sole dedup authority. That keying-off-the-file,
// not off store state, is deliberate:
//
//   - it no-ops when the report path already journaled (marker present);
//   - it RECOVERS a dispatch whose report-path write failed transiently — the
//     store still holds the terminal report at removal time, so the recovered
//     entry is the rich completed/failed block, not a degraded one. (Keying the
//     skip off the store's terminal-state instead would strand that dispatch
//     unjournaled forever, since the marker was never written.)
//
// Non-dispatch sessions resolve to no dispatch and are ignored.
//
// One narrow, accepted race remains: if a teardown reads this dispatch in the
// sub-millisecond window after a terminal report is rendered but before it commits
// to the store, AND the teardown's append wins the marker before the report path's,
// the journal records the lower-fidelity "(ended)" block. Exactly-once still holds
// (the marker admits a single entry); only the fidelity of that one entry degrades,
// and only in that window. Closing it fully would require cross-subsystem per-
// dispatch locking — not worth it for a best-effort capture.
func (d *Daemon) journalDispatchOnSessionGone(sessionID string) {
	dispatch := d.store.GetChiefOfStaffDispatchBySession(strings.TrimSpace(sessionID))
	if dispatch == nil {
		return
	}
	d.journalDispatchOutcome(dispatch)
}

// renderDispatchJournalEntry builds the dated journal block for a dispatch's
// outcome. It returns ok=false when the dispatch carries no journalable content
// (no structured summary and no freeform report), so empty blocks are never
// written. now is the fallback clock for a dispatch with no report timestamp.
func renderDispatchJournalEntry(dispatch *protocol.ChiefOfStaffDispatch, now time.Time) (dateISO, block string, ok bool) {
	report := dispatch.StructuredReport

	summary := ""
	if report != nil {
		summary = strings.TrimSpace(report.Summary)
	}
	if summary == "" {
		summary = strings.TrimSpace(protocol.Deref(dispatch.LatestReport))
	}
	if summary == "" {
		return "", "", false
	}
	summary = clampJournalField(summary)

	// Date/time: the server-stamped report time when present, else the wall clock.
	ts := now
	if t, okp := parseDispatchTime(protocol.Deref(dispatch.ReportedAt)); okp {
		ts = t
	}
	dateISO = ts.Format("2006-01-02")

	// Outcome label: the work state when known, else a neutral "ended" (used by the
	// session-gone fallback for a worker that never sent a structured report).
	outcome := "ended"
	if report != nil && report.WorkState != "" {
		outcome = string(report.WorkState)
	}

	label := strings.TrimSpace(dispatch.Label)
	if label == "" {
		label = firstLine(dispatch.Brief)
	}
	if label == "" {
		label = "dispatch"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## %s — %s (%s)\n\n", ts.Format("15:04"), label, outcome)
	b.WriteString(summary)
	b.WriteString("\n")

	if report != nil {
		// Only render a "Decision" line for a request the user actually resolved.
		// An unresolved request still carries the agent's proposed Recommendation,
		// and dispatchDecisionText falls back to it — rendering that as a settled
		// "Decision" in a durable journal would misrecord a proposal as a ruling.
		if report.Request != nil && report.Request.Status == protocol.DispatchRequestStatusResolved {
			if decision := dispatchDecisionText(report.Request); decision != "" {
				fmt.Fprintf(&b, "\n%s\n", clampJournalField(decision))
			}
		}
		if v := dispatchVerificationLine(report.Verification); v != "" {
			fmt.Fprintf(&b, "\nVerification: %s\n", v)
		}
		if next := strings.TrimSpace(protocol.Deref(report.NextAction)); next != "" && outcome != string(protocol.DispatchWorkStateCompleted) {
			fmt.Fprintf(&b, "\nNext: %s\n", clampJournalField(next))
		}
	}

	// Defuse any HTML-comment opener in the rendered body BEFORE appending the real
	// footer + marker, so no free-text field (summary, label, decision, next,
	// verification) can forge the hidden per-dispatch dedup marker and suppress
	// another dispatch's legitimate entry. Dispatch IDs are server UUIDs, so this is
	// belt-and-suspenders — but the marker is the dedup ledger, keep it unforgeable.
	body := neutralizeJournalMarkers(b.String())

	// Grounding + dedupe footer: a visible source ref the journal reader (and any
	// later curation pass) can resolve, and the hidden marker that makes the write
	// idempotent. Both are built from the UUID dispatch ID, never free text.
	var doc strings.Builder
	doc.WriteString(body)
	fmt.Fprintf(&doc, "\nsource: dispatch:%s\n", strings.TrimSpace(dispatch.ID))
	doc.WriteString(journalDispatchMarker(dispatch.ID))

	return dateISO, doc.String(), true
}

// neutralizeJournalMarkers breaks any HTML-comment opener ("<!--") in rendered body
// text into a non-opener ("<! --") so embedded free text can never forge the hidden
// per-dispatch dedup marker. The genuine marker is appended after this pass, so it
// is unaffected.
func neutralizeJournalMarkers(s string) string {
	return strings.ReplaceAll(s, "<!--", "<! --")
}

// dispatchVerificationLine condenses verification evidence into one scannable line
// ("<result> (<target>); ..."), bounded so a noisy report cannot bloat the block.
func dispatchVerificationLine(evidence []protocol.DispatchVerification) string {
	const maxItems = 3
	var parts []string
	for i := range evidence {
		result := strings.TrimSpace(evidence[i].Result)
		if result == "" {
			continue
		}
		if target := strings.TrimSpace(evidence[i].Target); target != "" {
			result = fmt.Sprintf("%s (%s)", result, target)
		}
		parts = append(parts, result)
		if len(parts) >= maxItems {
			break
		}
	}
	return strings.Join(parts, "; ")
}

// journalFieldRuneCap bounds each free-text field copied into a journal block.
// A dispatch summary/decision/next-action is meant to be a scannable note, not a
// transcript; a runaway field would otherwise dominate the daily journal. Counted
// in runes so a multibyte field is never split mid-character.
const journalFieldRuneCap = 1500

// clampJournalField trims s and caps it at journalFieldRuneCap runes, appending an
// ellipsis when truncated so the reader can tell the note was shortened.
func clampJournalField(s string) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) <= journalFieldRuneCap {
		return s
	}
	return strings.TrimSpace(string(runes[:journalFieldRuneCap])) + "…"
}

// firstLine returns the first non-empty line of s, trimmed — used to derive a
// label from a multi-line brief.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// parseDispatchTime parses a dispatch timestamp (RFC3339, with or without
// nanoseconds), preserving its recorded offset so the journal date/time reflect
// when the work was reported.
func parseDispatchTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
