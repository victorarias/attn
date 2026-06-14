package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func readJournalFile(t *testing.T, d *Daemon, dateISO string) string {
	t.Helper()
	root, err := d.notebookRoot()
	if err != nil {
		t.Fatalf("notebook root: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "journal", dateISO+".md"))
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatalf("read journal %s: %v", dateISO, err)
	}
	return string(data)
}

// waitForJournal polls the dated journal until it contains substr — the journal
// write on the dispatch-report path is a side effect that runs after the socket
// response is sent, so socket-level tests assert it eventually, not synchronously.
func waitForJournal(t *testing.T, d *Daemon, dateISO, substr string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if body := readJournalFile(t, d, dateISO); strings.Contains(body, substr) {
			return body
		}
		if time.Now().After(deadline) {
			t.Fatalf("journal %s never contained %q:\n%s", dateISO, substr, readJournalFile(t, d, dateISO))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRenderDispatchJournalEntry(t *testing.T) {
	const reported = "2026-06-14T14:30:00Z"

	completed := &protocol.ChiefOfStaffDispatch{
		ID:         "dsp-1",
		Label:      "Ship the editor",
		ReportedAt: protocol.Ptr(reported),
		StructuredReport: &protocol.DispatchReport{
			ReportType: protocol.DispatchReportTypeCompletion,
			WorkState:  protocol.DispatchWorkStateCompleted,
			Summary:    "Shipped the in-app markdown editor.",
			NextAction: protocol.Ptr("none — fully landed"),
			Request: &protocol.DispatchDecisionRequest{
				Status:   protocol.DispatchRequestStatusResolved,
				Question: "Which conflict strategy?",
				Response: protocol.Ptr("hash-CAS with surfaced conflicts"),
			},
			Verification: []protocol.DispatchVerification{
				{Result: "pass", Target: "go test ./internal/notebook", Actor: "agent", ArtifactIdentity: "pr", Timestamp: reported},
			},
		},
	}

	dateISO, block, ok := renderDispatchJournalEntry(completed, time.Now())
	if !ok {
		t.Fatal("completed dispatch should render")
	}
	if dateISO != "2026-06-14" {
		t.Fatalf("date = %q, want 2026-06-14 (from ReportedAt)", dateISO)
	}
	for _, want := range []string{
		"## 14:30 — Ship the editor (completed)",
		"Shipped the in-app markdown editor.",
		"Decision: Which conflict strategy? → hash-CAS with surfaced conflicts",
		"Verification: pass (go test ./internal/notebook)",
		"source: dispatch:dsp-1",
		"<!-- attn:dispatch:dsp-1 -->",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("block missing %q:\n%s", want, block)
		}
	}
	// A completed dispatch does not foreground a "Next" line.
	if strings.Contains(block, "Next:") {
		t.Fatalf("completed dispatch should not render Next:\n%s", block)
	}

	// A failure keeps its outcome label and DOES surface the next action.
	failed := &protocol.ChiefOfStaffDispatch{
		ID:         "dsp-2",
		Label:      "Migrate schema",
		ReportedAt: protocol.Ptr(reported),
		StructuredReport: &protocol.DispatchReport{
			ReportType: protocol.DispatchReportTypeFailure,
			WorkState:  protocol.DispatchWorkStateFailed,
			Summary:    "Migration aborted: foreign key violation.",
			NextAction: protocol.Ptr("backfill nulls, then retry"),
		},
	}
	_, block, ok = renderDispatchJournalEntry(failed, time.Now())
	if !ok {
		t.Fatal("failed dispatch should render")
	}
	if !strings.Contains(block, "(failed)") || !strings.Contains(block, "Next: backfill nulls, then retry") {
		t.Fatalf("failed dispatch render wrong:\n%s", block)
	}

	// A dispatch with only a freeform report (no structured report) still journals,
	// labelled "ended", with the date taken from the fallback clock.
	freeform := &protocol.ChiefOfStaffDispatch{
		ID:           "dsp-3",
		Label:        "Investigate flake",
		LatestReport: protocol.Ptr("Tracked the flake to a clock dependency."),
	}
	dateISO, block, ok = renderDispatchJournalEntry(freeform, time.Date(2026, 6, 14, 8, 5, 0, 0, time.UTC))
	if !ok {
		t.Fatal("freeform dispatch should render")
	}
	if dateISO != "2026-06-14" || !strings.Contains(block, "(ended)") || !strings.Contains(block, "Tracked the flake") {
		t.Fatalf("freeform render wrong (date=%q):\n%s", dateISO, block)
	}

	// A dispatch with no content at all is not journaled.
	if _, _, ok := renderDispatchJournalEntry(&protocol.ChiefOfStaffDispatch{ID: "dsp-4", Label: "Empty"}, time.Now()); ok {
		t.Fatal("empty dispatch should not render")
	}
}

// An unresolved decision request still carries the agent's proposed recommendation
// (dispatchDecisionText would fall back to it). The journal must not render that
// proposal as a settled "Decision" — only a request the user actually resolved
// becomes a decision line.
func TestRenderDispatchJournalEntryUnresolvedRequestOmitsDecision(t *testing.T) {
	dispatch := &protocol.ChiefOfStaffDispatch{
		ID:    "dsp-pending",
		Label: "Pick a strategy",
		StructuredReport: &protocol.DispatchReport{
			ReportType: protocol.DispatchReportTypeProgress,
			WorkState:  protocol.DispatchWorkStateNeedsInput,
			Summary:    "Blocked on a direction call.",
			Request: &protocol.DispatchDecisionRequest{
				Status:         protocol.DispatchRequestStatusPending,
				Question:       "Optimistic or pessimistic locking?",
				Recommendation: protocol.Ptr("optimistic — fewer stalls"),
			},
		},
	}

	_, block, ok := renderDispatchJournalEntry(dispatch, time.Now())
	if !ok {
		t.Fatal("dispatch with a summary should render")
	}
	if strings.Contains(block, "Decision:") {
		t.Fatalf("unresolved request must not render a Decision line:\n%s", block)
	}
	if strings.Contains(block, "optimistic — fewer stalls") {
		t.Fatalf("unresolved recommendation must not leak into the journal:\n%s", block)
	}
}

// The verification line is bounded (at most 3 items) and skips evidence with an
// empty Result, so a noisy or partial report cannot bloat or break the block.
func TestDispatchVerificationLineCapAndSkip(t *testing.T) {
	line := dispatchVerificationLine([]protocol.DispatchVerification{
		{Result: "pass", Target: "unit"},
		{Result: "", Target: "skip-me"}, // empty result is skipped
		{Result: "pass", Target: "integration"},
		{Result: "pass", Target: "e2e"},
		{Result: "pass", Target: "overflow"}, // beyond the cap
	})
	if strings.Contains(line, "skip-me") {
		t.Fatalf("empty-result evidence should be skipped: %q", line)
	}
	if strings.Contains(line, "overflow") {
		t.Fatalf("verification line should cap at 3 items: %q", line)
	}
	if n := strings.Count(line, ";"); n != 2 {
		t.Fatalf("expected 3 joined items (2 separators), got %d: %q", n, line)
	}
}

// A long free-text field is clamped so one runaway report cannot dominate the
// daily journal; the truncation is visible via a trailing ellipsis.
func TestRenderDispatchJournalEntryClampsLongSummary(t *testing.T) {
	long := strings.Repeat("x", journalFieldRuneCap+500)
	dispatch := &protocol.ChiefOfStaffDispatch{
		ID:    "dsp-long",
		Label: "Verbose worker",
		StructuredReport: &protocol.DispatchReport{
			ReportType: protocol.DispatchReportTypeCompletion,
			WorkState:  protocol.DispatchWorkStateCompleted,
			Summary:    long,
		},
	}
	_, block, ok := renderDispatchJournalEntry(dispatch, time.Now())
	if !ok {
		t.Fatal("dispatch should render")
	}
	if strings.Count(block, "x") > journalFieldRuneCap {
		t.Fatalf("summary should be clamped to %d runes, got %d", journalFieldRuneCap, strings.Count(block, "x"))
	}
	if !strings.Contains(block, "…") {
		t.Fatalf("clamped field should end with an ellipsis:\n%s", block[len(block)-80:])
	}
}

// The reaper path (a worker reaped on restart/liveness sweep without a terminal
// report) journals its dispatch outcome exactly once — the reliability backstop
// the centralized dropSessionRecord chokepoint exists to guarantee.
func TestRemoveReapedSessionJournalsOnce(t *testing.T) {
	d := newNotebookDaemon(t)
	addIdleNotebookSession(d, "worker-reap", protocol.SessionStateWorking)
	if err := d.store.AddChiefOfStaffDispatch(&protocol.ChiefOfStaffDispatch{
		ID: "dsp-reap", ChiefSessionID: "chief", SessionID: "worker-reap", WorkspaceID: "ws",
		Label: "Crashed mid-run", Agent: "claude", CreatedAt: "2026-06-14", UpdatedAt: "2026-06-14",
		ReportedAt:   protocol.Ptr("2026-06-14T11:00:00Z"),
		LatestReport: protocol.Ptr("Made progress before the worker was reaped."),
	}); err != nil {
		t.Fatalf("add dispatch: %v", err)
	}

	d.removeReapedSession("worker-reap")

	body := readJournalFile(t, d, "2026-06-14")
	if !strings.Contains(body, "Made progress before the worker was reaped.") {
		t.Fatalf("reaped dispatch was not journaled:\n%s", body)
	}
	if n := strings.Count(body, "<!-- attn:dispatch:dsp-reap -->"); n != 1 {
		t.Fatalf("reaped dispatch journaled %d times, want 1:\n%s", n, body)
	}
}

// When a dispatch reached a terminal report but the report-path journal write was
// missed (e.g. it failed transiently and left no marker), the session-gone fallback
// must RECOVER it — and because the store now holds the terminal report, the
// recovered entry is the rich completed/failed block, not a degraded "(ended)" one.
// Keying dedup off the file marker (not off store terminal-state) is what makes this
// recovery possible; a second call no-ops via that marker.
func TestJournalDispatchOnSessionGoneRecoversTerminal(t *testing.T) {
	d := newNotebookDaemon(t)
	if err := d.store.AddChiefOfStaffDispatch(&protocol.ChiefOfStaffDispatch{
		ID: "dsp-term", ChiefSessionID: "chief", SessionID: "worker-term", WorkspaceID: "ws",
		Label: "Already done", Agent: "claude", CreatedAt: "2026-06-14", UpdatedAt: "2026-06-14",
		ReportedAt: protocol.Ptr("2026-06-14T12:00:00Z"),
		StructuredReport: &protocol.DispatchReport{
			ReportType: protocol.DispatchReportTypeCompletion,
			WorkState:  protocol.DispatchWorkStateCompleted,
			Summary:    "Finished cleanly.",
		},
	}); err != nil {
		t.Fatalf("add dispatch: %v", err)
	}

	// No prior journal write exists (marker absent) — the fallback recovers it.
	d.journalDispatchOnSessionGone("worker-term")
	d.journalDispatchOnSessionGone("worker-term") // idempotent: marker now present

	body := readJournalFile(t, d, "2026-06-14")
	if !strings.Contains(body, "(completed)") || !strings.Contains(body, "Finished cleanly.") {
		t.Fatalf("recovered entry should be the rich completed block, not degraded:\n%s", body)
	}
	if strings.Contains(body, "(ended)") {
		t.Fatalf("recovered entry must not use the degraded (ended) label:\n%s", body)
	}
	if n := strings.Count(body, "<!-- attn:dispatch:dsp-term -->"); n != 1 {
		t.Fatalf("terminal dispatch recovered %d times, want 1:\n%s", n, body)
	}
}

// journalDispatchOutcome writes one grounded block and is idempotent: a second
// call for the same dispatch (e.g. the session-gone fallback after a terminal
// report) adds nothing.
func TestJournalDispatchOutcomeIdempotent(t *testing.T) {
	d := newNotebookDaemon(t)
	dispatch := &protocol.ChiefOfStaffDispatch{
		ID:         "dsp-once",
		Label:      "Ship it",
		ReportedAt: protocol.Ptr("2026-06-14T14:30:00Z"),
		StructuredReport: &protocol.DispatchReport{
			ReportType: protocol.DispatchReportTypeCompletion,
			WorkState:  protocol.DispatchWorkStateCompleted,
			Summary:    "Shipped it.",
		},
	}

	d.journalDispatchOutcome(dispatch)
	d.journalDispatchOutcome(dispatch)

	body := readJournalFile(t, d, "2026-06-14")
	if n := strings.Count(body, "<!-- attn:dispatch:dsp-once -->"); n != 1 {
		t.Fatalf("dispatch journaled %d times, want 1:\n%s", n, body)
	}
	if !strings.Contains(body, "source: dispatch:dsp-once") {
		t.Fatalf("entry not grounded:\n%s", body)
	}
}

// The full report path: reporting a completed dispatch over the socket lands a
// single grounded journal block, and re-reporting it does not duplicate it.
func TestReportDispatchAutoJournals(t *testing.T) {
	d := newNotebookDaemon(t)
	addIdleNotebookSession(d, "worker-1", protocol.SessionStateWorking)
	if err := d.store.AddChiefOfStaffDispatch(&protocol.ChiefOfStaffDispatch{
		ID: "dsp-int", ChiefSessionID: "chief", SessionID: "worker-1", WorkspaceID: "ws",
		Label: "Wire the daemon", Agent: "claude", CreatedAt: "2026-06-14", UpdatedAt: "2026-06-14",
	}); err != nil {
		t.Fatalf("add dispatch: %v", err)
	}

	report := protocol.ReportDispatchMessage{
		Cmd:             protocol.CmdReportDispatch,
		SourceSessionID: "worker-1",
		Report:          "done",
		StructuredReport: &protocol.DispatchReport{
			ReportType: protocol.DispatchReportTypeCompletion,
			WorkState:  protocol.DispatchWorkStateCompleted,
			Summary:    "Wired the auto-journal into the report path.",
		},
	}
	sendNotebookCmd(t, d, report)

	today := time.Now().Format("2006-01-02")
	body := waitForJournal(t, d, today, "Wired the auto-journal into the report path.")
	if !strings.Contains(body, "source: dispatch:dsp-int") {
		t.Fatalf("entry not grounded:\n%s", body)
	}

	// A second identical report must not double-write.
	sendNotebookCmd(t, d, report)
	body = waitForJournal(t, d, today, "<!-- attn:dispatch:dsp-int -->")
	if n := strings.Count(body, "<!-- attn:dispatch:dsp-int -->"); n != 1 {
		t.Fatalf("dispatch journaled %d times after re-report, want 1:\n%s", n, body)
	}
}

// A non-terminal report (needs_input) is NOT journaled by the report path — only
// finished dispatches become durable entries.
func TestReportDispatchNonTerminalDoesNotJournal(t *testing.T) {
	d := newNotebookDaemon(t)
	addIdleNotebookSession(d, "worker-3", protocol.SessionStateWorking)
	if err := d.store.AddChiefOfStaffDispatch(&protocol.ChiefOfStaffDispatch{
		ID: "dsp-mid", ChiefSessionID: "chief", SessionID: "worker-3", WorkspaceID: "ws",
		Label: "In flight", Agent: "claude", CreatedAt: "2026-06-14", UpdatedAt: "2026-06-14",
	}); err != nil {
		t.Fatalf("add dispatch: %v", err)
	}

	sendNotebookCmd(t, d, protocol.ReportDispatchMessage{
		Cmd:             protocol.CmdReportDispatch,
		SourceSessionID: "worker-3",
		Report:          "progress",
		StructuredReport: &protocol.DispatchReport{
			ReportType: protocol.DispatchReportTypeProgress,
			WorkState:  protocol.DispatchWorkStateInProgress,
			Summary:    "Still working.",
		},
	})

	// Give any (incorrect) async write a chance to land before asserting absence.
	time.Sleep(50 * time.Millisecond)
	if body := readJournalFile(t, d, time.Now().Format("2006-01-02")); strings.Contains(body, "dsp-mid") {
		t.Fatalf("non-terminal report should not journal:\n%s", body)
	}
}

// The session-gone fallback journals a worker that ended without a terminal report
// (from its freeform report), and is a no-op for a non-dispatch session.
func TestJournalDispatchOnSessionGoneFallback(t *testing.T) {
	d := newNotebookDaemon(t)
	if err := d.store.AddChiefOfStaffDispatch(&protocol.ChiefOfStaffDispatch{
		ID: "dsp-gone", ChiefSessionID: "chief", SessionID: "worker-gone", WorkspaceID: "ws",
		Label: "Ran and vanished", Agent: "claude", CreatedAt: "2026-06-14", UpdatedAt: "2026-06-14",
		ReportedAt:   protocol.Ptr("2026-06-14T09:00:00Z"),
		LatestReport: protocol.Ptr("Got partway before the session closed."),
	}); err != nil {
		t.Fatalf("add dispatch: %v", err)
	}

	d.journalDispatchOnSessionGone("worker-gone")
	body := readJournalFile(t, d, "2026-06-14")
	if !strings.Contains(body, "Got partway before the session closed.") || !strings.Contains(body, "(ended)") {
		t.Fatalf("session-gone fallback did not journal:\n%s", body)
	}

	// A session that is not a tracked dispatch is a silent no-op.
	before := body
	d.journalDispatchOnSessionGone("not-a-dispatch")
	if after := readJournalFile(t, d, "2026-06-14"); after != before {
		t.Fatalf("non-dispatch session changed the journal:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// unregisterSession — the orderly-close path and the most common one — must journal
// a dispatch outcome before dropping the session record. This pins the wiring of the
// dominant removal path to the chokepoint; a refactor reverting it to a bare
// store.Remove would fail here.
func TestUnregisterSessionJournals(t *testing.T) {
	d := newNotebookDaemon(t)
	addIdleNotebookSession(d, "worker-close", protocol.SessionStateWorking)
	if err := d.store.AddChiefOfStaffDispatch(&protocol.ChiefOfStaffDispatch{
		ID: "dsp-close", ChiefSessionID: "chief", SessionID: "worker-close", WorkspaceID: "ws",
		Label: "Closed by user", Agent: "claude", CreatedAt: "2026-06-14", UpdatedAt: "2026-06-14",
		ReportedAt:   protocol.Ptr("2026-06-14T13:00:00Z"),
		LatestReport: protocol.Ptr("Worked until the pane was closed."),
	}); err != nil {
		t.Fatalf("add dispatch: %v", err)
	}

	d.unregisterSession("worker-close", syscall.SIGTERM)

	body := readJournalFile(t, d, "2026-06-14")
	if !strings.Contains(body, "Worked until the pane was closed.") {
		t.Fatalf("orderly-close path did not journal:\n%s", body)
	}
	if d.store.Get("worker-close") != nil {
		t.Fatal("session record should be removed after unregister")
	}
}

// cleanupDeletedWorktreeSessions (worktree-delete path) must also journal before
// removal — the third path routed through the chokepoint.
func TestCleanupDeletedWorktreeSessionsJournals(t *testing.T) {
	d := newNotebookDaemon(t)
	addIdleNotebookSession(d, "worker-wt", protocol.SessionStateWorking)
	if err := d.store.AddChiefOfStaffDispatch(&protocol.ChiefOfStaffDispatch{
		ID: "dsp-wt", ChiefSessionID: "chief", SessionID: "worker-wt", WorkspaceID: "ws",
		Label: "Worktree torn down", Agent: "claude", CreatedAt: "2026-06-14", UpdatedAt: "2026-06-14",
		ReportedAt:   protocol.Ptr("2026-06-14T13:30:00Z"),
		LatestReport: protocol.Ptr("Ran inside a worktree that was deleted."),
	}); err != nil {
		t.Fatalf("add dispatch: %v", err)
	}

	// addIdleNotebookSession sets Directory to "/tmp/<id>"; match it.
	d.cleanupDeletedWorktreeSessions("/tmp/worker-wt")

	body := readJournalFile(t, d, "2026-06-14")
	if !strings.Contains(body, "Ran inside a worktree that was deleted.") {
		t.Fatalf("worktree-cleanup path did not journal:\n%s", body)
	}
	if d.store.Get("worker-wt") != nil {
		t.Fatal("session record should be removed after worktree cleanup")
	}
}

// clear_sessions ("Clear all sessions") is the fourth removal path: it must capture
// an in-flight dispatch's outcome before the bulk delete, or the dispatch row is
// orphaned and never journaled.
func TestClearAllSessionsJournals(t *testing.T) {
	d := newNotebookDaemon(t)
	addIdleNotebookSession(d, "worker-clear", protocol.SessionStateWorking)
	if err := d.store.AddChiefOfStaffDispatch(&protocol.ChiefOfStaffDispatch{
		ID: "dsp-clear", ChiefSessionID: "chief", SessionID: "worker-clear", WorkspaceID: "ws",
		Label: "In flight at clear", Agent: "claude", CreatedAt: "2026-06-14", UpdatedAt: "2026-06-14",
		ReportedAt:   protocol.Ptr("2026-06-14T13:45:00Z"),
		LatestReport: protocol.Ptr("Was mid-run when sessions were cleared."),
	}); err != nil {
		t.Fatalf("add dispatch: %v", err)
	}

	d.clearAllSessions()

	body := readJournalFile(t, d, "2026-06-14")
	if !strings.Contains(body, "Was mid-run when sessions were cleared.") {
		t.Fatalf("clear-all path did not journal:\n%s", body)
	}
	if d.store.Get("worker-clear") != nil {
		t.Fatal("session record should be removed after clear-all")
	}
}

// Lifecycle-safety invariant: a journaling failure must NEVER disrupt session
// removal. With the notebook root pointed at an unwritable location, dropSessionRecord
// must still remove the session record (and not panic).
func TestDropSessionRecordSwallowsJournalFailure(t *testing.T) {
	d := newNotebookDaemon(t)
	// Point the notebook root under a regular file so any write (MkdirAll) fails.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed blocker file: %v", err)
	}
	d.store.SetSetting(SettingNotebookRoot, filepath.Join(blocker, "notebook"))

	addIdleNotebookSession(d, "worker-fail", protocol.SessionStateWorking)
	if err := d.store.AddChiefOfStaffDispatch(&protocol.ChiefOfStaffDispatch{
		ID: "dsp-fail", ChiefSessionID: "chief", SessionID: "worker-fail", WorkspaceID: "ws",
		Label: "Journal will fail", Agent: "claude", CreatedAt: "2026-06-14", UpdatedAt: "2026-06-14",
		LatestReport: protocol.Ptr("This entry cannot be written."),
	}); err != nil {
		t.Fatalf("add dispatch: %v", err)
	}

	d.dropSessionRecord("worker-fail") // must not panic

	if d.store.Get("worker-fail") != nil {
		t.Fatal("session record must still be removed when journaling fails")
	}
}

// A free-text field that contains a literal dispatch marker must not be able to
// poison another dispatch's dedup. The renderer neutralizes HTML-comment openers in
// the body, so dispatch B's real entry still writes even after A embedded B's marker.
func TestForgedMarkerDoesNotPoisonDedup(t *testing.T) {
	d := newNotebookDaemon(t)

	// A finishes first with a summary that embeds B's marker verbatim.
	d.journalDispatchOutcome(&protocol.ChiefOfStaffDispatch{
		ID:         "dsp-A",
		Label:      "Attacker",
		ReportedAt: protocol.Ptr("2026-06-14T10:00:00Z"),
		StructuredReport: &protocol.DispatchReport{
			ReportType: protocol.DispatchReportTypeCompletion,
			WorkState:  protocol.DispatchWorkStateCompleted,
			Summary:    "Embedding " + journalDispatchMarker("dsp-B") + " in my summary.",
		},
	})
	// B's real entry must still land — its marker was not pre-written by A.
	d.journalDispatchOutcome(&protocol.ChiefOfStaffDispatch{
		ID:         "dsp-B",
		Label:      "Victim",
		ReportedAt: protocol.Ptr("2026-06-14T10:01:00Z"),
		StructuredReport: &protocol.DispatchReport{
			ReportType: protocol.DispatchReportTypeCompletion,
			WorkState:  protocol.DispatchWorkStateCompleted,
			Summary:    "B's genuine outcome.",
		},
	})

	body := readJournalFile(t, d, "2026-06-14")
	if !strings.Contains(body, "B's genuine outcome.") {
		t.Fatalf("dispatch B was suppressed by a forged marker in A:\n%s", body)
	}
	// B's real marker appears exactly once (A's embedded copy was neutralized).
	if n := strings.Count(body, journalDispatchMarker("dsp-B")); n != 1 {
		t.Fatalf("B's marker count = %d, want 1 (A's copy should be neutralized):\n%s", n, body)
	}
}

// clampJournalField's two documented invariants: an exactly-cap field is returned
// unchanged (no stray ellipsis), and truncation is rune-aware so a multibyte field
// is never split into a U+FFFD replacement char.
func TestClampJournalFieldBoundaryAndMultibyte(t *testing.T) {
	exact := strings.Repeat("a", journalFieldRuneCap)
	if got := clampJournalField(exact); got != exact {
		t.Fatalf("exactly-cap field should be unchanged; len(got)=%d, ellipsis=%v", len([]rune(got)), strings.Contains(got, "…"))
	}

	multibyte := strings.Repeat("é", journalFieldRuneCap+50) // 1 rune, 2 bytes each
	got := clampJournalField(multibyte)
	if strings.ContainsRune(got, '�') {
		t.Fatal("multibyte field was split mid-rune (U+FFFD present)")
	}
	if n := len([]rune(got)); n != journalFieldRuneCap+1 { // cap runes + ellipsis
		t.Fatalf("clamped multibyte rune count = %d, want %d", n, journalFieldRuneCap+1)
	}
}
