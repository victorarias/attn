package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/protocol"
)

// appendDreamJournal writes a journal entry through the daemon's notebook store.
func appendDreamJournal(t *testing.T, d *Daemon, date, entry string) {
	t.Helper()
	store, err := d.notebookStoreFor()
	if err != nil {
		t.Fatalf("notebook store: %v", err)
	}
	if _, _, err := store.AppendJournal(date, entry); err != nil {
		t.Fatalf("append journal %s: %v", date, err)
	}
}

func setWorkspaceContext(t *testing.T, d *Daemon, workspaceID, content string) {
	t.Helper()
	// ListWorkspaceContexts inner-joins the workspaces table, so the workspace
	// must be registered for its context to be harvestable (mirrors production:
	// contexts only exist for real workspaces).
	d.store.AddWorkspace(&protocol.Workspace{ID: workspaceID, Title: workspaceID, Directory: t.TempDir()})
	if _, _, err := d.store.UpdateWorkspaceContext(workspaceID, content, "sess", 0); err != nil {
		t.Fatalf("update workspace context %s: %v", workspaceID, err)
	}
}

// findCandidate returns the first candidate whose snippet contains substr.
func findCandidate(cands []protocol.NotebookDreamCandidate, substr string) *protocol.NotebookDreamCandidate {
	for i := range cands {
		if strings.Contains(cands[i].Snippet, substr) {
			return &cands[i]
		}
	}
	return nil
}

// Harvest pulls from all three v1 sources, merges a fact echoed across two
// workspace contexts into one recurring candidate, and excludes a dispatch whose
// target session is still live (only closed dispatches are durable).
func TestHarvestDreamCandidatesAcrossSources(t *testing.T) {
	d := newNotebookDaemon(t)

	appendDreamJournal(t, d, "2026-06-10", "The harvest pass scans journals, context snapshots, and closed dispatches.")

	const sharedDecision = "Daemon owns every notebook write through one in-process store."
	ctxTemplate := "# Workspace Context\n\n## Area\nfoo\n\n## Decisions\n- " + sharedDecision + "\n"
	setWorkspaceContext(t, d, "ws-a", ctxTemplate)
	setWorkspaceContext(t, d, "ws-b", ctxTemplate+"- A second decision only in ws-b about hash-CAS edits.\n")

	// Closed dispatch: its target session is absent from the store.
	if err := d.store.AddChiefOfStaffDispatch(&protocol.ChiefOfStaffDispatch{
		ID: "dsp-closed", ChiefSessionID: "chief", SessionID: "worker-gone", WorkspaceID: "ws-a",
		Label: "Ship the editor", Agent: "claude", CreatedAt: "2026-06-09", UpdatedAt: "2026-06-09",
		StructuredReport: &protocol.DispatchReport{Summary: "Shipped the in-app markdown editor with hash-CAS conflict handling."},
	}); err != nil {
		t.Fatalf("add closed dispatch: %v", err)
	}
	// Live dispatch: its target session exists, so it is not yet closed.
	addIdleNotebookSession(d, "worker-live", protocol.SessionStateWorking)
	if err := d.store.AddChiefOfStaffDispatch(&protocol.ChiefOfStaffDispatch{
		ID: "dsp-live", ChiefSessionID: "chief", SessionID: "worker-live", WorkspaceID: "ws-b",
		Label: "Still working", Agent: "claude", CreatedAt: "2026-06-13", UpdatedAt: "2026-06-13",
		StructuredReport: &protocol.DispatchReport{Summary: "LIVE-WORK-IN-PROGRESS should not be harvested."},
	}); err != nil {
		t.Fatalf("add live dispatch: %v", err)
	}

	res, err := d.dreamRun(false)
	if err != nil {
		t.Fatalf("dreamRun: %v", err)
	}
	if res.Applied {
		t.Fatal("dreamRun must be preview-only (applied=false)")
	}

	// The shared decision recurs across both workspaces.
	shared := findCandidate(res.Candidates, sharedDecision)
	if shared == nil {
		t.Fatalf("shared decision not harvested; candidates = %+v", res.Candidates)
	}
	if shared.Occurrences != 2 || len(shared.Contexts) != 2 {
		t.Fatalf("shared decision occurrences=%d contexts=%d, want 2/2", shared.Occurrences, len(shared.Contexts))
	}
	if shared.Source != notebook.SignalSourceContext {
		t.Fatalf("shared decision source = %q, want context", shared.Source)
	}

	// Exactly one candidate recurs across multiple contexts (the shared decision);
	// the journal block, ws-b's second decision, and the dispatch summary are each
	// single-context. This pins the ">1" multi-context threshold that the user-
	// facing "N across multiple contexts" count and the promote-phase gate rest on.
	if res.MultiContextCount != 1 {
		t.Fatalf("multi-context count = %d, want exactly 1 (only the shared decision recurs); candidates = %+v", res.MultiContextCount, res.Candidates)
	}
	if res.CandidateCount <= 1 {
		t.Fatalf("candidate count = %d, want several so the multi-context subset is meaningful", res.CandidateCount)
	}

	// The closed dispatch's summary is harvested; the live one is not.
	if findCandidate(res.Candidates, "Shipped the in-app markdown editor") == nil {
		t.Fatal("closed dispatch summary should be harvested")
	}
	if findCandidate(res.Candidates, "LIVE-WORK-IN-PROGRESS") != nil {
		t.Fatal("a dispatch whose target session is still live must not be harvested")
	}

	// All three sources contributed.
	srcs := map[string]bool{}
	for _, sc := range res.SourceCounts {
		srcs[sc.Source] = true
	}
	for _, want := range []string{notebook.SignalSourceJournal, notebook.SignalSourceContext, notebook.SignalSourceDispatch} {
		if !srcs[want] {
			t.Fatalf("source %q missing from counts %+v", want, res.SourceCounts)
		}
	}
}

// extractContextSignals harvests only the Decisions/Constraints sections (working
// state like Area is ignored) and joins a wrapped bullet's continuation lines.
func TestExtractContextSignals(t *testing.T) {
	content := "# Workspace Context\n\n" +
		"## Area\nThis is working context, not durable memory.\n\n" +
		"## Decisions\n- A decision that wraps\n  onto a second line.\n- A second decision.\n\n" +
		"## Constraints\n- A hard constraint.\n\n" +
		"## Threads\n- Not harvested.\n"

	sigs := extractContextSignals("ws-1", content, "2026-06-13T00:00:00Z")
	if len(sigs) != 3 {
		t.Fatalf("signals = %d, want 3 (2 decisions + 1 constraint): %+v", len(sigs), sigs)
	}
	var joined bool
	for _, s := range sigs {
		if s.SourceRef != "context:ws-1" || s.Context != "workspace:ws-1" {
			t.Fatalf("signal grounding = ref %q ctx %q, want context:ws-1 / workspace:ws-1", s.SourceRef, s.Context)
		}
		if strings.Contains(s.Text, "wraps") && strings.Contains(s.Text, "second line") {
			joined = true
		}
		if strings.Contains(s.Text, "Not harvested") || strings.Contains(s.Text, "working context") {
			t.Fatalf("harvested a non-durable section: %q", s.Text)
		}
	}
	if !joined {
		t.Fatal("a wrapped bullet's continuation line should be joined into one signal")
	}
}

// A resolved decision request renders as a single durable line; a dispatch with
// only a freeform report falls back to it.
func TestDispatchSignals(t *testing.T) {
	resolved := &protocol.ChiefOfStaffDispatch{
		ID: "dsp-1", WorkspaceID: "ws-1", Label: "Decide split",
		StructuredReport: &protocol.DispatchReport{
			Summary: "Split the dreaming work in two.",
			Request: &protocol.DispatchDecisionRequest{
				Question: "Split dreaming into two PRs?",
				Response: protocol.Ptr("Yes — harvest first, promote second."),
			},
		},
	}
	sigs := dispatchSignals(resolved)
	if len(sigs) != 2 {
		t.Fatalf("signals = %d, want 2 (summary + decision): %+v", len(sigs), sigs)
	}
	var sawDecision bool
	for _, s := range sigs {
		if s.SourceRef != "dispatch:dsp-1" || s.Context != "workspace:ws-1" {
			t.Fatalf("dispatch grounding = ref %q ctx %q", s.SourceRef, s.Context)
		}
		if strings.HasPrefix(s.Text, "Decision: ") && strings.Contains(s.Text, "→") {
			sawDecision = true
		}
	}
	if !sawDecision {
		t.Fatalf("expected a rendered decision line; got %+v", sigs)
	}

	freeform := &protocol.ChiefOfStaffDispatch{
		ID: "dsp-2", WorkspaceID: "ws-2", LatestReport: protocol.Ptr("Freeform progress note."),
	}
	if got := dispatchSignals(freeform); len(got) != 1 || !strings.Contains(got[0].Text, "Freeform progress note") {
		t.Fatalf("freeform fallback = %+v, want one signal from the latest report", got)
	}

	// With no explicit response, a recommendation is the recorded outcome.
	rec := dispatchDecisionText(&protocol.DispatchDecisionRequest{
		Question: "Which agent?", Recommendation: protocol.Ptr("Use claude."),
	})
	if !strings.Contains(rec, "Which agent?") || !strings.Contains(rec, "Use claude.") {
		t.Fatalf("recommendation fallback = %q, want question + recommendation", rec)
	}

	// An unanswered decision request carries no durable outcome.
	if got := dispatchDecisionText(&protocol.DispatchDecisionRequest{Question: "Pending?"}); got != "" {
		t.Fatalf("unanswered request text = %q, want empty", got)
	}
}

// A journal note larger than MaxFileSize (only possible via external sync, since
// attn never writes one) is skipped rather than read fully into memory, mirroring
// the Backlinks/List size guard.
func TestHarvestSkipsOversizedJournal(t *testing.T) {
	d := newNotebookDaemon(t)
	appendDreamJournal(t, d, "2026-06-10", "A normal durable fact worth harvesting.")

	// Write an oversized journal file directly on disk (an external-sync scenario).
	root := d.store.GetSetting(SettingNotebookRoot)
	big := "# 2026-06-11\n\n" + strings.Repeat("x", int(notebook.MaxFileSize)+1) + "\n"
	if err := os.WriteFile(filepath.Join(root, "journal", "2026-06-11.md"), []byte(big), 0o600); err != nil {
		t.Fatalf("write oversized journal: %v", err)
	}

	res, err := d.dreamRun(false)
	if err != nil {
		t.Fatalf("dreamRun: %v", err)
	}
	// The normal journal still harvests; the oversized one contributes nothing.
	if findCandidate(res.Candidates, "A normal durable fact") == nil {
		t.Fatal("the normal journal should still be harvested")
	}
	if findCandidate(res.Candidates, "xxxxxxxx") != nil {
		t.Fatal("an oversized externally-synced journal must be skipped, not read into memory")
	}
}

// dream status reflects the notebook.dreaming.enabled gate.
func TestDreamStatusReportsEnabledGate(t *testing.T) {
	d := newNotebookDaemon(t)
	appendDreamJournal(t, d, "2026-06-10", "A durable decision worth remembering across days.")

	off, err := d.dreamStatus()
	if err != nil {
		t.Fatalf("dreamStatus: %v", err)
	}
	if off.Enabled {
		t.Fatal("dreaming should be disabled by default")
	}
	if off.CandidateCount == 0 {
		t.Fatal("status should surface harvested candidates even when disabled")
	}

	d.store.SetSetting(SettingNotebookDreamingEnabled, "true")
	on, err := d.dreamStatus()
	if err != nil {
		t.Fatalf("dreamStatus: %v", err)
	}
	if !on.Enabled {
		t.Fatal("status should reflect the enabled gate")
	}
}

// The dream commands round-trip through the full unix-socket path
// (ParseMessage -> dispatch -> handler -> Response), proving the protocol wiring.
func TestNotebookDreamDispatchesThroughClientMessage(t *testing.T) {
	d := newNotebookDaemon(t)
	appendDreamJournal(t, d, "2026-06-10", "A durable decision harvested through the socket path.")

	status := sendNotebookCmd(t, d, protocol.NotebookDreamStatusMessage{Cmd: protocol.CmdNotebookDreamStatus})
	if status.NotebookDreamStatus == nil {
		t.Fatalf("dream status response missing result: %+v", status)
	}

	run := sendNotebookCmd(t, d, protocol.NotebookDreamRunMessage{Cmd: protocol.CmdNotebookDreamRun})
	if run.NotebookDreamRun == nil || run.NotebookDreamRun.Applied {
		t.Fatalf("dream run result = %+v, want a preview (applied=false)", run.NotebookDreamRun)
	}
	if run.NotebookDreamRun.CandidateCount == 0 {
		t.Fatal("dream run should surface the harvested candidate")
	}
}
