package daemon

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

// callHandoff drives handleHandoffDispatch over an in-memory pipe and returns the
// decoded response, so a test can assert both the success and the error paths
// (sendNotebookCmd t.Fatalf's on a non-ok response, which the error tests need).
func callHandoff(t *testing.T, d *Daemon, msg *protocol.HandoffDispatchMessage) protocol.Response {
	t.Helper()
	server, client := net.Pipe()
	defer client.Close()
	go func() {
		d.handleHandoffDispatch(server, msg)
		_ = server.Close()
	}()
	var resp protocol.Response
	if err := json.NewDecoder(client).Decode(&resp); err != nil {
		t.Fatalf("decode handoff response: %v", err)
	}
	return resp
}

// readNotebookFile reads a note straight off disk under the configured root, so a
// test can confirm the artifact bytes landed verbatim.
func readNotebookFile(t *testing.T, d *Daemon, rel string) string {
	t.Helper()
	root := d.store.GetSetting(SettingNotebookRoot)
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read notebook file %q: %v", rel, err)
	}
	return string(data)
}

func addHandoffDispatch(t *testing.T, d *Daemon, sessionID, dispatchID string) {
	t.Helper()
	addIdleNotebookSession(d, sessionID, protocol.SessionStateWorking)
	if err := d.store.AddChiefOfStaffDispatch(&protocol.ChiefOfStaffDispatch{
		ID: dispatchID, ChiefSessionID: "chief", SessionID: sessionID, WorkspaceID: "ws",
		Label: "Audit", Agent: "claude", CreatedAt: "2026-06-22", UpdatedAt: "2026-06-22",
	}); err != nil {
		t.Fatalf("add dispatch: %v", err)
	}
}

func handoffReviewOutcome(summary string) protocol.DispatchReport {
	return protocol.DispatchReport{
		ReportType: protocol.DispatchReportTypeHandoff,
		WorkState:  protocol.DispatchWorkStateReadyForReview,
		Summary:    summary,
	}
}

// The happy path: a tracked dispatch hands an artifact into the Notebook; the bytes
// land verbatim at the designated path and the report carries a resolvable
// root-absolute reference alongside the agent's note.
func TestHandoffDispatchWritesArtifactAndReferences(t *testing.T) {
	d := newNotebookDaemon(t)
	addHandoffDispatch(t, d, "worker-1", "dsp-1")

	artifact := "# Findings\n\nA long report the agent built with the user.\n"
	resp := callHandoff(t, d, &protocol.HandoffDispatchMessage{
		Cmd:              protocol.CmdHandoffDispatch,
		SourceSessionID:  "worker-1",
		To:               "projects/audit/findings.md",
		Content:          artifact,
		Report:           protocol.Ptr("Audit complete."),
		StructuredReport: handoffReviewOutcome("Audit complete."),
	})
	if !resp.Ok || resp.ChiefOfStaffDispatch == nil {
		t.Fatalf("handoff response = %+v", resp)
	}

	if got := readNotebookFile(t, d, "projects/audit/findings.md"); got != artifact {
		t.Fatalf("artifact not written verbatim:\n%q", got)
	}

	report := protocol.Deref(resp.ChiefOfStaffDispatch.LatestReport)
	if !strings.Contains(report, "Audit complete.") {
		t.Fatalf("report dropped the agent note:\n%s", report)
	}
	if !strings.Contains(report, "Artifact in the Notebook: /projects/audit/findings.md") {
		t.Fatalf("report missing the reference:\n%s", report)
	}
}

// A normalized root-absolute --to resolves to the same note, and a default note is
// composed when the agent passes no message.
func TestHandoffDispatchDefaultNoteAndPathNormalization(t *testing.T) {
	d := newNotebookDaemon(t)
	addHandoffDispatch(t, d, "worker-2", "dsp-2")

	resp := callHandoff(t, d, &protocol.HandoffDispatchMessage{
		Cmd:              protocol.CmdHandoffDispatch,
		SourceSessionID:  "worker-2",
		To:               "/areas/notes.md", // leading slash is accepted and normalized away
		Content:          "body",
		StructuredReport: handoffReviewOutcome("Notes ready for review."),
	})
	if !resp.Ok {
		t.Fatalf("handoff response = %+v", resp)
	}
	readNotebookFile(t, d, "areas/notes.md") // exists at the normalized path
	report := protocol.Deref(resp.ChiefOfStaffDispatch.LatestReport)
	if !strings.Contains(report, "Handed off an artifact") ||
		!strings.Contains(report, "Artifact in the Notebook: /areas/notes.md") {
		t.Fatalf("default note/reference wrong:\n%s", report)
	}
}

// A refined re-handoff to the same designated path overwrites the one note rather
// than failing on the create-only conflict.
func TestHandoffDispatchOverwritesOnRehandoff(t *testing.T) {
	d := newNotebookDaemon(t)
	addHandoffDispatch(t, d, "worker-3", "dsp-3")

	first := callHandoff(t, d, &protocol.HandoffDispatchMessage{
		Cmd: protocol.CmdHandoffDispatch, SourceSessionID: "worker-3",
		To: "projects/x/report.md", Content: "v1",
		StructuredReport: handoffReviewOutcome("First version ready."),
	})
	if !first.Ok {
		t.Fatalf("first handoff = %+v", first)
	}
	second := callHandoff(t, d, &protocol.HandoffDispatchMessage{
		Cmd: protocol.CmdHandoffDispatch, SourceSessionID: "worker-3",
		To: "projects/x/report.md", Content: "v2",
		StructuredReport: handoffReviewOutcome("Second version ready."),
	})
	if !second.Ok {
		t.Fatalf("re-handoff should overwrite, got %+v", second)
	}
	if got := readNotebookFile(t, d, "projects/x/report.md"); got != "v2" {
		t.Fatalf("re-handoff did not update the note: %q", got)
	}
}

// Only a tracked dispatch may hand off into the Notebook — an arbitrary session is
// rejected before anything is written.
func TestHandoffDispatchRejectsNonDispatchSession(t *testing.T) {
	d := newNotebookDaemon(t)
	addIdleNotebookSession(d, "rando", protocol.SessionStateWorking) // no dispatch

	resp := callHandoff(t, d, &protocol.HandoffDispatchMessage{
		Cmd: protocol.CmdHandoffDispatch, SourceSessionID: "rando",
		To: "projects/x/report.md", Content: "body",
		StructuredReport: handoffReviewOutcome("Artifact ready."),
	})
	if resp.Ok {
		t.Fatalf("expected rejection, got ok response")
	}
	if resp.Error == nil || !strings.Contains(*resp.Error, "not a tracked dispatch") {
		t.Fatalf("error = %v", resp.Error)
	}
}

// A non-.md or empty destination is rejected by CleanPath before any write. A
// parent escape ("../escape.md") is not rejected — CleanPath neutralizes the ".."
// to within the root, so the write stays contained (verified below).
func TestHandoffDispatchRejectsBadDestination(t *testing.T) {
	d := newNotebookDaemon(t)
	addHandoffDispatch(t, d, "worker-4", "dsp-4")

	for _, dest := range []string{"notes.txt", ""} {
		resp := callHandoff(t, d, &protocol.HandoffDispatchMessage{
			Cmd: protocol.CmdHandoffDispatch, SourceSessionID: "worker-4",
			To: dest, Content: "body",
			StructuredReport: handoffReviewOutcome("Artifact ready."),
		})
		if resp.Ok {
			t.Fatalf("dest %q should be rejected", dest)
		}
	}

	// A parent escape is contained, not rejected: it lands inside the root.
	resp := callHandoff(t, d, &protocol.HandoffDispatchMessage{
		Cmd: protocol.CmdHandoffDispatch, SourceSessionID: "worker-4",
		To: "../escape.md", Content: "body",
		StructuredReport: handoffReviewOutcome("Artifact ready."),
	})
	if !resp.Ok {
		t.Fatalf("parent escape should be contained, got %+v", resp)
	}
	readNotebookFile(t, d, "escape.md") // the ".." was neutralized to the root
}

// An empty artifact is rejected — a handoff must carry content.
func TestHandoffDispatchRejectsEmptyContent(t *testing.T) {
	d := newNotebookDaemon(t)
	addHandoffDispatch(t, d, "worker-5", "dsp-5")

	resp := callHandoff(t, d, &protocol.HandoffDispatchMessage{
		Cmd: protocol.CmdHandoffDispatch, SourceSessionID: "worker-5",
		To: "projects/x/report.md", Content: "   \n",
		StructuredReport: handoffReviewOutcome("Artifact ready."),
	})
	if resp.Ok || resp.Error == nil || !strings.Contains(*resp.Error, "content is required") {
		t.Fatalf("expected content-required error, got ok=%v err=%v", resp.Ok, resp.Error)
	}
}

// The full unix-socket path (ParseMessage decode -> daemon route -> handler) wires
// the new command end to end, not just the handler in isolation.
func TestHandoffDispatchFullSocketPath(t *testing.T) {
	d := newNotebookDaemon(t)
	addHandoffDispatch(t, d, "worker-sock", "dsp-sock")

	resp := sendNotebookCmd(t, d, protocol.HandoffDispatchMessage{
		Cmd:              protocol.CmdHandoffDispatch,
		SourceSessionID:  "worker-sock",
		To:               "projects/sock/report.md",
		Content:          "wired",
		Report:           protocol.Ptr("Routed."),
		StructuredReport: handoffReviewOutcome("Routed."),
	})
	if resp.ChiefOfStaffDispatch == nil {
		t.Fatalf("socket handoff returned no dispatch: %+v", resp)
	}
	if got := readNotebookFile(t, d, "projects/sock/report.md"); got != "wired" {
		t.Fatalf("artifact not written through socket path: %q", got)
	}
	if r := protocol.Deref(resp.ChiefOfStaffDispatch.LatestReport); !strings.Contains(r, "/projects/sock/report.md") {
		t.Fatalf("socket report missing reference:\n%s", r)
	}
}

// A terminal handoff (completed coordination envelope) lands the artifact AND the
// deterministic raw-tier capture, with the reference discoverable in the report.
func TestHandoffDispatchTerminalAlsoJournals(t *testing.T) {
	d := newNotebookDaemon(t)
	addHandoffDispatch(t, d, "worker-6", "dsp-6")

	resp := callHandoff(t, d, &protocol.HandoffDispatchMessage{
		Cmd: protocol.CmdHandoffDispatch, SourceSessionID: "worker-6",
		To: "projects/audit/final.md", Content: "# Final\n",
		Report: protocol.Ptr("Done."),
		StructuredReport: protocol.DispatchReport{
			ReportType: protocol.DispatchReportTypeCompletion,
			WorkState:  protocol.DispatchWorkStateCompleted,
			Summary:    "Audit finished; findings handed off.",
		},
	})
	if !resp.Ok {
		t.Fatalf("terminal handoff = %+v", resp)
	}
	readNotebookFile(t, d, "projects/audit/final.md")
	body := waitForRawDispatch(t, d, "dsp-6", "Audit finished; findings handed off.")
	if !strings.Contains(body, "source: dispatch:dsp-6") {
		t.Fatalf("terminal handoff not grounded in raw tier:\n%s", body)
	}
}
