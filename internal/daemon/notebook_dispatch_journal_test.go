package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/notebook"
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

// readRawDispatchFile returns the per-dispatch raw-tier file
// (.attn/raw/dispatches/<dispatchID>.md), or "" if it does not exist. Dispatch
// outcomes now land here, redirected out of the curated journal.
func readRawDispatchFile(t *testing.T, d *Daemon, dispatchID string) string {
	t.Helper()
	root, err := d.notebookRoot()
	if err != nil {
		t.Fatalf("notebook root: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(notebook.RawDispatchesDir(root), dispatchID+".md"))
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatalf("read raw dispatch %s: %v", dispatchID, err)
	}
	return string(data)
}

// waitForRawDispatch reads the per-dispatch raw file and asserts it contains
// substr. The report path now journals the outcome durable-before-ack — the write
// completes before the socket response returns — so the file is already on disk
// when sendNotebookCmd returns; the short poll is a defensive tolerance, not a
// dependency on async timing.
func waitForRawDispatch(t *testing.T, d *Daemon, dispatchID, substr string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if body := readRawDispatchFile(t, d, dispatchID); strings.Contains(body, substr) {
			return body
		}
		if time.Now().After(deadline) {
			t.Fatalf("raw dispatch %s never contained %q:\n%s", dispatchID, substr, readRawDispatchFile(t, d, dispatchID))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitForRawDispatchesQuiescent asserts the dispatches dir holds exactly the
// expected settled .md files with no in-flight atomic-writer temp (.tmp.) sibling.
// The report-path capture is now synchronous (durable before ack), so after
// sendNotebookCmd returns the overwrite has already settled; this remains as an
// explicit no-temp/no-leak guard against t.TempDir() cleanup.
func waitForRawDispatchesQuiescent(t *testing.T, d *Daemon, wantMD int) {
	t.Helper()
	root, err := d.notebookRoot()
	if err != nil {
		t.Fatalf("notebook root: %v", err)
	}
	dir := notebook.RawDispatchesDir(root)
	deadline := time.Now().Add(2 * time.Second)
	for {
		entries, err := os.ReadDir(dir)
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("read dispatches dir: %v", err)
		}
		md, temp := 0, 0
		for _, e := range entries {
			switch {
			case strings.Contains(e.Name(), ".tmp."):
				temp++
			case strings.HasSuffix(e.Name(), ".md"):
				md++
			}
		}
		if temp == 0 && md == wantMD {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("dispatches dir not quiescent: md=%d (want %d) temp=%d", md, wantMD, temp)
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

	body := readRawDispatchFile(t, d, "dsp-reap")
	if !strings.Contains(body, "Made progress before the worker was reaped.") {
		t.Fatalf("reaped dispatch was not captured:\n%s", body)
	}
	if n := strings.Count(body, "<!-- attn:dispatch:dsp-reap -->"); n != 1 {
		t.Fatalf("reaped dispatch captured %d markers, want 1:\n%s", n, body)
	}
	// The redirect keeps the raw block OUT of the curated journal.
	if j := readJournalFile(t, d, "2026-06-14"); strings.Contains(j, "dsp-reap") {
		t.Fatalf("dispatch block leaked into the curated journal:\n%s", j)
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

	// No prior raw file exists — the fallback recovers it.
	d.journalDispatchOnSessionGone("worker-term")
	d.journalDispatchOnSessionGone("worker-term") // idempotent: identical overwrite

	body := readRawDispatchFile(t, d, "dsp-term")
	if !strings.Contains(body, "(completed)") || !strings.Contains(body, "Finished cleanly.") {
		t.Fatalf("recovered entry should be the rich completed block, not degraded:\n%s", body)
	}
	if strings.Contains(body, "(ended)") {
		t.Fatalf("recovered entry must not use the degraded (ended) label:\n%s", body)
	}
	// The 1:1 <dispatchID>.md keying means a replay is an identical overwrite, so the
	// single file holds exactly one marker regardless of how many times it is called.
	if n := strings.Count(body, "<!-- attn:dispatch:dsp-term -->"); n != 1 {
		t.Fatalf("terminal dispatch recovered %d markers, want 1:\n%s", n, body)
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

	body := readRawDispatchFile(t, d, "dsp-once")
	if n := strings.Count(body, "<!-- attn:dispatch:dsp-once -->"); n != 1 {
		t.Fatalf("dispatch captured %d markers, want 1:\n%s", n, body)
	}
	if !strings.Contains(body, "source: dispatch:dsp-once") {
		t.Fatalf("entry not grounded:\n%s", body)
	}
}

// A replay of a dispatch that carries a server ReportedAt re-renders a
// byte-identical block, but a dispatch WITHOUT a ReportedAt stamps its "## HH:MM"
// header from the wall clock at render time, so two replays in different minutes
// produce an equivalent — not byte-identical — block. The exactly-once ledger still
// holds (one file, one marker); only the header timestamp can drift. This pins the
// softened "equivalent, not byte-identical" claim in the package comments.
func TestRenderDispatchJournalEntryWallClockHeaderDrifts(t *testing.T) {
	withReportedAt := &protocol.ChiefOfStaffDispatch{
		ID:         "dsp-stamped",
		Label:      "stamped",
		ReportedAt: protocol.Ptr("2026-06-15T10:30:00Z"),
		StructuredReport: &protocol.DispatchReport{
			ReportType: protocol.DispatchReportTypeCompletion,
			WorkState:  protocol.DispatchWorkStateCompleted,
			Summary:    "done",
		},
	}
	noReportedAt := &protocol.ChiefOfStaffDispatch{
		ID:           "dsp-bare",
		Label:        "bare",
		LatestReport: protocol.Ptr("ended without a structured report"),
		// ReportedAt deliberately absent -> wall-clock fallback for the header.
	}

	t1 := time.Date(2026, 6, 15, 10, 30, 0, 0, time.UTC)
	t2 := time.Date(2026, 6, 15, 10, 31, 0, 0, time.UTC)

	// With a server ReportedAt the render ignores the passed clock entirely, so the
	// block is byte-identical across replays.
	if _, a, _ := renderDispatchJournalEntry(withReportedAt, t1); true {
		if _, b, _ := renderDispatchJournalEntry(withReportedAt, t2); a != b {
			t.Fatalf("ReportedAt dispatch should render identically across clocks:\n%s\n---\n%s", a, b)
		}
	}

	// Without one the header tracks the wall clock, so the two blocks differ — but
	// only in the header line; the body and the dedup marker are unchanged.
	_, first, ok1 := renderDispatchJournalEntry(noReportedAt, t1)
	_, second, ok2 := renderDispatchJournalEntry(noReportedAt, t2)
	if !ok1 || !ok2 {
		t.Fatal("bare dispatch should still be renderable")
	}
	if first == second {
		t.Fatalf("wall-clock header should drift across minutes:\n%s", first)
	}
	if !strings.Contains(first, "## 10:30 —") || !strings.Contains(second, "## 10:31 —") {
		t.Fatalf("headers not stamped from the passed clock:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	marker := "<!-- attn:dispatch:dsp-bare -->"
	if !strings.Contains(first, marker) || !strings.Contains(second, marker) {
		t.Fatalf("dedup marker missing despite header drift")
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

	body := waitForRawDispatch(t, d, "dsp-int", "Wired the auto-journal into the report path.")
	if !strings.Contains(body, "source: dispatch:dsp-int") {
		t.Fatalf("entry not grounded:\n%s", body)
	}
	// The redirect keeps the raw block OUT of the curated journal.
	if j := readJournalFile(t, d, time.Now().Format("2006-01-02")); strings.Contains(j, "dsp-int") {
		t.Fatalf("dispatch block leaked into the curated journal:\n%s", j)
	}

	// A second identical report is a harmless identical overwrite — one file, one marker.
	sendNotebookCmd(t, d, report)
	waitForRawDispatchesQuiescent(t, d, 1) // the in-flight overwrite settled; still one file
	body = readRawDispatchFile(t, d, "dsp-int")
	if n := strings.Count(body, "<!-- attn:dispatch:dsp-int -->"); n != 1 {
		t.Fatalf("dispatch captured %d markers after re-report, want 1:\n%s", n, body)
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
	if body := readRawDispatchFile(t, d, "dsp-mid"); body != "" {
		t.Fatalf("non-terminal report should not capture a dispatch file:\n%s", body)
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
	body := readRawDispatchFile(t, d, "dsp-gone")
	if !strings.Contains(body, "Got partway before the session closed.") || !strings.Contains(body, "(ended)") {
		t.Fatalf("session-gone fallback did not capture:\n%s", body)
	}

	// A session that is not a tracked dispatch is a silent no-op — it writes no file.
	d.journalDispatchOnSessionGone("not-a-dispatch")
	if after := readRawDispatchFile(t, d, "dsp-gone"); after != body {
		t.Fatalf("non-dispatch session changed the captured file:\nbefore:\n%s\nafter:\n%s", body, after)
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

	body := readRawDispatchFile(t, d, "dsp-close")
	if !strings.Contains(body, "Worked until the pane was closed.") {
		t.Fatalf("orderly-close path did not capture:\n%s", body)
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

	body := readRawDispatchFile(t, d, "dsp-wt")
	if !strings.Contains(body, "Ran inside a worktree that was deleted.") {
		t.Fatalf("worktree-cleanup path did not capture:\n%s", body)
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

	body := readRawDispatchFile(t, d, "dsp-clear")
	if !strings.Contains(body, "Was mid-run when sessions were cleared.") {
		t.Fatalf("clear-all path did not capture:\n%s", body)
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
// forge another dispatch's marker. With the raw-tier redirect each dispatch is a
// distinct 1:1 file, so A cannot suppress B's separate file; what the neutralize
// guarantee still protects is that A's OWN file never contains a genuine
// (un-neutralized) copy of B's marker that a marker-scanner could mistake for B's
// real entry.
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
	// B's real entry lands in its own file regardless.
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

	bBody := readRawDispatchFile(t, d, "dsp-B")
	if !strings.Contains(bBody, "B's genuine outcome.") {
		t.Fatalf("dispatch B's own file is missing its outcome:\n%s", bBody)
	}
	if n := strings.Count(bBody, journalDispatchMarker("dsp-B")); n != 1 {
		t.Fatalf("B's file marker count = %d, want 1:\n%s", n, bBody)
	}
	// A's file embedded B's marker text in free prose; it must have been neutralized
	// so A's file holds ZERO genuine B markers.
	aBody := readRawDispatchFile(t, d, "dsp-A")
	if n := strings.Count(aBody, journalDispatchMarker("dsp-B")); n != 0 {
		t.Fatalf("A's file holds %d genuine B markers, want 0 (the forged copy must be neutralized):\n%s", n, aBody)
	}
	if !strings.Contains(aBody, "<! -- attn:dispatch:dsp-B -->") {
		t.Fatalf("A's forged marker should be neutralized to a non-opener:\n%s", aBody)
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
