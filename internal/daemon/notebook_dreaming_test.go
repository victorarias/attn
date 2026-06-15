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

// Harvest pulls from the surviving v1 sources — journals (source-1) and closed
// chief dispatches (source-3) — and excludes a dispatch whose target session is
// still live (only closed dispatches are durable). Source-2 (workspace-context
// re-read) was removed once narration took ownership of distilling context.md into
// the journal, so a workspace context set here must NOT surface as a candidate.
func TestHarvestDreamCandidatesAcrossSources(t *testing.T) {
	d := newNotebookDaemon(t)

	appendDreamJournal(t, d, "2026-06-10", "The harvest pass scans journals and closed dispatches.")

	// Workspace context is no longer a harvest source: setting one must contribute
	// nothing (narration distills it into the journal instead).
	const droppedDecision = "Daemon owns every notebook write through one in-process store."
	ctxTemplate := "# Workspace Context\n\n## Area\nfoo\n\n## Decisions\n- " + droppedDecision + "\n"
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

	// The journal block (source-1) is harvested.
	if findCandidate(res.Candidates, "The harvest pass scans journals") == nil {
		t.Fatalf("journal block not harvested; candidates = %+v", res.Candidates)
	}

	// Workspace-context decisions (former source-2) are NOT harvested anymore.
	if findCandidate(res.Candidates, droppedDecision) != nil {
		t.Fatalf("workspace context must not be harvested (source-2 removed); candidates = %+v", res.Candidates)
	}

	// The closed dispatch's summary (source-3) is harvested; the live one is not.
	if findCandidate(res.Candidates, "Shipped the in-app markdown editor") == nil {
		t.Fatal("closed dispatch summary should be harvested")
	}
	if findCandidate(res.Candidates, "LIVE-WORK-IN-PROGRESS") != nil {
		t.Fatal("a dispatch whose target session is still live must not be harvested")
	}

	// Only the journal and dispatch sources contribute now — no context source.
	srcs := map[string]bool{}
	for _, sc := range res.SourceCounts {
		srcs[sc.Source] = true
	}
	for _, want := range []string{notebook.SignalSourceJournal, notebook.SignalSourceDispatch} {
		if !srcs[want] {
			t.Fatalf("source %q missing from counts %+v", want, res.SourceCounts)
		}
	}
	if srcs[notebook.SignalSourceContext] {
		t.Fatalf("context source must not appear in harvest (source-2 removed); counts %+v", res.SourceCounts)
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
