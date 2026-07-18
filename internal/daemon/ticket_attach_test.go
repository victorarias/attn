package daemon

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/ticketnotify"
)

func callTicketAttach(t *testing.T, d *Daemon, msg *protocol.TicketAttachMessage) protocol.Response {
	t.Helper()
	server, client := net.Pipe()
	defer client.Close()
	done := make(chan struct{})
	go func() {
		d.handleTicketAttach(server, msg)
		_ = server.Close()
		close(done)
	}()
	var resp protocol.Response
	if err := json.NewDecoder(client).Decode(&resp); err != nil {
		t.Fatalf("decode ticket attach response: %v", err)
	}
	<-done
	return resp
}

func attachSource(t *testing.T, dir, name, content string) protocol.TicketAttachFile {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	return protocol.TicketAttachFile{SourcePath: path, Filename: name}
}

func TestTicketAttachCopiesFilesOfAnyTypeAndChangesState(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	notebookRoot := t.TempDir()
	d.store.SetSetting(SettingNotebookRoot, notebookRoot)
	_, agentID, _ := delegateForNotify(t, d, "codex")
	ticketID := boundTicketID(t, d, agentID)
	sources := t.TempDir()
	state := protocol.DispatchWorkStateReadyForReview
	comment := "Storage choice is confirmed."

	resp := callTicketAttach(t, d, &protocol.TicketAttachMessage{
		Cmd:             protocol.CmdTicketAttach,
		SourceSessionID: agentID,
		Files: []protocol.TicketAttachFile{
			attachSource(t, sources, "design.md", "the design"),
			attachSource(t, sources, "prototype.html", "<!doctype html><title>prototype</title>"),
			attachSource(t, sources, "results.json", `{"ok":true}`),
		},
		State:   &state,
		Comment: &comment,
	})
	if !resp.Ok || resp.TicketAttachResult == nil {
		t.Fatalf("response = %+v, want attach receipt", resp)
	}
	result := resp.TicketAttachResult
	if result.TicketID != ticketID || result.State != protocol.TicketStatusInReview || len(result.Artifacts) != 3 || result.EventSeq == 0 {
		t.Fatalf("receipt = %+v", result)
	}
	for _, artifact := range result.Artifacts {
		if !strings.HasPrefix(artifact.NotebookPath, "tickets/"+ticketID+"/") {
			t.Fatalf("notebook path = %q", artifact.NotebookPath)
		}
		if _, err := os.Stat(artifact.Path); err != nil {
			t.Fatalf("artifact %q missing: %v", artifact.Path, err)
		}
	}
	if got := artifactNames(result.Artifacts); !reflect.DeepEqual(got, []string{"design.md", "prototype.html", "results.json"}) {
		t.Fatalf("receipt artifacts = %v", got)
	}
	ticket, err := d.store.GetTicket(ticketID)
	if err != nil {
		t.Fatal(err)
	}
	if ticket.Status != store.TicketStatusInReview {
		t.Fatalf("ticket status = %s", ticket.Status)
	}
	var sawAttach bool
	for _, activity := range ticket.Activity {
		if activity.Kind == store.TicketActivityAttach {
			sawAttach = strings.Contains(activity.Comment, "design.md, prototype.html, results.json") && strings.Contains(activity.Comment, comment)
		}
	}
	if !sawAttach {
		t.Fatalf("attach history missing: %+v", ticket.Activity)
	}
}

func TestTicketAttachUsesVisibleBasenamesAndRejectsNonRegularSources(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.store.SetSetting(SettingNotebookRoot, t.TempDir())
	_, agentID, _ := delegateForNotify(t, d, "codex")

	source := attachSource(t, t.TempDir(), "source.html", "<!doctype html>")
	source.Filename = "nested/prototype.html"
	resp := callTicketAttach(t, d, &protocol.TicketAttachMessage{Cmd: protocol.CmdTicketAttach, SourceSessionID: agentID, Files: []protocol.TicketAttachFile{source}})
	if !resp.Ok || resp.TicketAttachResult == nil || len(resp.TicketAttachResult.Artifacts) != 1 || resp.TicketAttachResult.Artifacts[0].Filename != "prototype.html" {
		t.Fatalf("basename attach response = %+v", resp)
	}

	target := attachSource(t, t.TempDir(), "target.txt", "target")
	symlink := filepath.Join(t.TempDir(), "linked.txt")
	if err := os.Symlink(target.SourcePath, symlink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	resp = callTicketAttach(t, d, &protocol.TicketAttachMessage{
		Cmd:             protocol.CmdTicketAttach,
		SourceSessionID: agentID,
		Files:           []protocol.TicketAttachFile{{SourcePath: symlink, Filename: "linked.txt"}},
	})
	if resp.Ok || resp.Error == nil || !strings.Contains(*resp.Error, "not a regular file") {
		t.Fatalf("symlink response = %+v, want regular-file error", resp)
	}
}

func TestTicketAttachRetryReturnsExistingReceipt(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.store.SetSetting(SettingNotebookRoot, t.TempDir())
	_, agentID, _ := delegateForNotify(t, d, "codex")
	source := attachSource(t, t.TempDir(), "design.md", "same bytes")
	msg := &protocol.TicketAttachMessage{Cmd: protocol.CmdTicketAttach, SourceSessionID: agentID, Files: []protocol.TicketAttachFile{source}}

	first := callTicketAttach(t, d, msg)
	second := callTicketAttach(t, d, msg)
	if !first.Ok || !second.Ok || first.TicketAttachResult == nil || second.TicketAttachResult == nil {
		t.Fatalf("responses = %+v / %+v", first, second)
	}
	if second.TicketAttachResult.Fingerprint != first.TicketAttachResult.Fingerprint || second.TicketAttachResult.EventSeq != first.TicketAttachResult.EventSeq || !second.TicketAttachResult.Deduplicated {
		t.Fatalf("retry receipt = %+v, first = %+v", second.TicketAttachResult, first.TicketAttachResult)
	}
}

func TestTicketAttachCatchUpRollsBackInstalledFile(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	root := t.TempDir()
	d.store.SetSetting(SettingNotebookRoot, root)
	_, agentID, _ := delegateForNotify(t, d, "codex")
	ticketID := boundTicketID(t, d, agentID)
	if _, err := d.store.AddTicketComment(ticketID, "chief-peer", "new decision", time.Now()); err != nil {
		t.Fatal(err)
	}
	source := attachSource(t, t.TempDir(), "design.md", "decision")
	msg := &protocol.TicketAttachMessage{Cmd: protocol.CmdTicketAttach, SourceSessionID: agentID, Files: []protocol.TicketAttachFile{source}}

	first := callTicketAttach(t, d, msg)
	if !first.Ok || first.TicketAttachResult == nil || first.TicketAttachResult.CatchUp == nil {
		t.Fatalf("first response = %+v, want catch-up", first)
	}
	destination := filepath.Join(root, "tickets", ticketID, "design.md")
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("conflicting attach left destination behind: %v", err)
	}

	retry := callTicketAttach(t, d, msg)
	if !retry.Ok || retry.TicketAttachResult == nil || retry.TicketAttachResult.CatchUp != nil {
		t.Fatalf("retry response = %+v", retry)
	}
	if _, err := os.Stat(destination); err != nil {
		t.Fatalf("retry did not install destination: %v", err)
	}
}

func TestTicketAttachPreservesDifferentExistingArtifact(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	root := t.TempDir()
	d.store.SetSetting(SettingNotebookRoot, root)
	_, agentID, _ := delegateForNotify(t, d, "codex")
	ticketID := boundTicketID(t, d, agentID)
	dir := filepath.Join(root, "tickets", ticketID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(dir, "prototype.html")
	if err := os.WriteFile(destination, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := attachSource(t, t.TempDir(), "prototype.html", "replacement")
	resp := callTicketAttach(t, d, &protocol.TicketAttachMessage{Cmd: protocol.CmdTicketAttach, SourceSessionID: agentID, Files: []protocol.TicketAttachFile{source}})
	if resp.Ok || resp.Error == nil || !strings.Contains(*resp.Error, "different contents") {
		t.Fatalf("response = %+v, want collision error", resp)
	}
	if got, _ := os.ReadFile(destination); string(got) != "keep me" {
		t.Fatalf("destination was overwritten: %q", got)
	}
}

func TestTicketAttachSupportsExplicitTicketID(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.store.SetSetting(SettingNotebookRoot, t.TempDir())
	if _, err := d.store.CreateTicket(store.Ticket{ID: "other-ticket", Title: "Other", Status: store.TicketStatusTodo}, "chief", time.Now()); err != nil {
		t.Fatal(err)
	}
	ticketID := "other-ticket"
	source := attachSource(t, t.TempDir(), "plan.md", "plan")
	resp := callTicketAttach(t, d, &protocol.TicketAttachMessage{Cmd: protocol.CmdTicketAttach, SourceSessionID: "peer", TicketID: &ticketID, Files: []protocol.TicketAttachFile{source}})
	if !resp.Ok || resp.TicketAttachResult == nil || resp.TicketAttachResult.TicketID != ticketID {
		t.Fatalf("response = %+v", resp)
	}
}

func TestTicketAttachDispatchesFromWebSocketAsUser(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.store.SetSetting(SettingNotebookRoot, t.TempDir())
	if _, err := d.store.CreateTicket(store.Ticket{ID: "ui-ticket", Title: "UI", Status: store.TicketStatusWorking}, "chief", time.Now()); err != nil {
		t.Fatal(err)
	}
	source := attachSource(t, t.TempDir(), "plan.md", "plan")
	client := newWorkspaceProtocolTestClient()
	client.setIdentity("test", "protocol-"+protocol.ProtocolVersion, []string{protocol.CapabilityWorkspaceSessions})
	payload, _ := json.Marshal(protocol.TicketAttachMessage{
		Cmd: protocol.CmdTicketAttach, SourceSessionID: store.TicketAuthorYou,
		TicketID: protocol.Ptr("ui-ticket"), Files: []protocol.TicketAttachFile{source}, RequestID: protocol.Ptr("h1"),
		ExpectedEventSeq: currentTicketEventSeq(t, d, "ui-ticket"),
	})
	d.handleClientMessage(client, payload)
	var result protocol.TicketAttachResultMessage
	readNotebookWSEvent(t, client.send, &result)
	if result.RequestID != "h1" || !result.Success || result.Result == nil || result.Result.TicketID != "ui-ticket" {
		t.Fatalf("websocket attach = %+v", result)
	}
}

func TestTicketAttachNotifiesChiefAndBroadcasts(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	d.store.SetSetting(SettingNotebookRoot, t.TempDir())
	chiefID, agentID, inputs := delegateForNotify(t, d, "codex")
	ticketID := boundTicketID(t, d, agentID)
	for _, id := range []string{chiefID, agentID} {
		if _, err := ticketnotify.ConsumeAll(d.store, d.ticketObserversForSession(id), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	d.store.UpdateState(chiefID, protocol.StateIdle)
	d.store.UpdateState(agentID, protocol.StateIdle)
	latestBroadcast := captureTicketBroadcasts(d)
	source := attachSource(t, t.TempDir(), "report.md", "findings")
	resp := callTicketAttach(t, d, &protocol.TicketAttachMessage{Cmd: protocol.CmdTicketAttach, SourceSessionID: agentID, Files: []protocol.TicketAttachFile{source}})
	if !resp.Ok {
		t.Fatalf("attach failed: %+v", resp)
	}
	fireNudgeNow(t, d, chiefID)
	if !wasNudged(inputs(chiefID)) || wasNudged(inputs(agentID)) {
		t.Fatalf("unexpected nudge state: chief=%v agent=%v", inputs(chiefID), inputs(agentID))
	}
	if board := latestBroadcast(); len(board) == 0 || !containsTicketID(board, ticketID) {
		t.Fatalf("ticket broadcast missing %q: %+v", ticketID, board)
	}
}

func containsTicketID(tickets []protocol.Ticket, id string) bool {
	for _, ticket := range tickets {
		if ticket.ID == id {
			return true
		}
	}
	return false
}
