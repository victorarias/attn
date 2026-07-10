package daemon

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/ticketnotify"
)

func callTicketHandover(t *testing.T, d *Daemon, msg *protocol.TicketHandoverMessage) protocol.Response {
	t.Helper()
	server, client := net.Pipe()
	defer client.Close()
	done := make(chan struct{})
	go func() {
		d.handleTicketHandover(server, msg)
		_ = server.Close()
		close(done)
	}()
	var resp protocol.Response
	if err := json.NewDecoder(client).Decode(&resp); err != nil {
		t.Fatalf("decode ticket handover response: %v", err)
	}
	<-done
	return resp
}

func handoverSource(t *testing.T, dir, name, content string) protocol.TicketHandoverFile {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	return protocol.TicketHandoverFile{SourcePath: path, Filename: name}
}

func TestTicketHandoverCopiesMultipleFilesAndChangesState(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	notebookRoot := t.TempDir()
	d.store.SetSetting(SettingNotebookRoot, notebookRoot)
	_, agentID, _ := delegateForNotify(t, d, "codex")
	ticketID := boundTicketID(t, d, agentID)
	sources := t.TempDir()
	state := protocol.DispatchWorkStateReadyForReview
	comment := "Storage choice is confirmed."

	resp := callTicketHandover(t, d, &protocol.TicketHandoverMessage{
		Cmd:             protocol.CmdTicketHandover,
		SourceSessionID: agentID,
		Files: []protocol.TicketHandoverFile{
			handoverSource(t, sources, "design.md", "the design"),
			handoverSource(t, sources, "rollout.md", "the rollout"),
		},
		State:   &state,
		Comment: &comment,
	})
	if !resp.Ok || resp.TicketHandoverResult == nil {
		t.Fatalf("response = %+v, want handover receipt", resp)
	}
	result := resp.TicketHandoverResult
	if result.TicketID != ticketID || result.State != protocol.TicketStatusInReview || len(result.Artifacts) != 2 || result.EventSeq == 0 {
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
	ticket, err := d.store.GetTicket(ticketID)
	if err != nil {
		t.Fatal(err)
	}
	if ticket.Status != store.TicketStatusInReview {
		t.Fatalf("ticket status = %s", ticket.Status)
	}
	var sawHandover bool
	for _, activity := range ticket.Activity {
		if activity.Kind == store.TicketActivityHandover {
			sawHandover = strings.Contains(activity.Comment, "design.md, rollout.md") && strings.Contains(activity.Comment, comment)
		}
	}
	if !sawHandover {
		t.Fatalf("handover history missing: %+v", ticket.Activity)
	}
}

func TestTicketHandoverRetryReturnsExistingReceipt(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.store.SetSetting(SettingNotebookRoot, t.TempDir())
	_, agentID, _ := delegateForNotify(t, d, "codex")
	source := handoverSource(t, t.TempDir(), "design.md", "same bytes")
	msg := &protocol.TicketHandoverMessage{Cmd: protocol.CmdTicketHandover, SourceSessionID: agentID, Files: []protocol.TicketHandoverFile{source}}

	first := callTicketHandover(t, d, msg)
	second := callTicketHandover(t, d, msg)
	if !first.Ok || !second.Ok || first.TicketHandoverResult == nil || second.TicketHandoverResult == nil {
		t.Fatalf("responses = %+v / %+v", first, second)
	}
	if second.TicketHandoverResult.Fingerprint != first.TicketHandoverResult.Fingerprint || second.TicketHandoverResult.EventSeq != first.TicketHandoverResult.EventSeq || !second.TicketHandoverResult.Deduplicated {
		t.Fatalf("retry receipt = %+v, first = %+v", second.TicketHandoverResult, first.TicketHandoverResult)
	}
}

func TestTicketHandoverPreservesDifferentExistingArtifact(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	root := t.TempDir()
	d.store.SetSetting(SettingNotebookRoot, root)
	_, agentID, _ := delegateForNotify(t, d, "codex")
	ticketID := boundTicketID(t, d, agentID)
	dir := filepath.Join(root, "tickets", ticketID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(dir, "design.md")
	if err := os.WriteFile(destination, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := handoverSource(t, t.TempDir(), "design.md", "replacement")
	resp := callTicketHandover(t, d, &protocol.TicketHandoverMessage{Cmd: protocol.CmdTicketHandover, SourceSessionID: agentID, Files: []protocol.TicketHandoverFile{source}})
	if resp.Ok || resp.Error == nil || !strings.Contains(*resp.Error, "different contents") {
		t.Fatalf("response = %+v, want collision error", resp)
	}
	if got, _ := os.ReadFile(destination); string(got) != "keep me" {
		t.Fatalf("destination was overwritten: %q", got)
	}
}

func TestTicketHandoverSupportsExplicitTicketID(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.store.SetSetting(SettingNotebookRoot, t.TempDir())
	if _, err := d.store.CreateTicket(store.Ticket{ID: "other-ticket", Title: "Other", Status: store.TicketStatusTodo}, "chief", time.Now()); err != nil {
		t.Fatal(err)
	}
	ticketID := "other-ticket"
	source := handoverSource(t, t.TempDir(), "plan.md", "plan")
	resp := callTicketHandover(t, d, &protocol.TicketHandoverMessage{Cmd: protocol.CmdTicketHandover, SourceSessionID: "peer", TicketID: &ticketID, Files: []protocol.TicketHandoverFile{source}})
	if !resp.Ok || resp.TicketHandoverResult == nil || resp.TicketHandoverResult.TicketID != ticketID {
		t.Fatalf("response = %+v", resp)
	}
}

func TestTicketHandoverDispatchesFromWebSocketAsUser(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.store.SetSetting(SettingNotebookRoot, t.TempDir())
	if _, err := d.store.CreateTicket(store.Ticket{ID: "ui-ticket", Title: "UI", Status: store.TicketStatusWorking}, "chief", time.Now()); err != nil {
		t.Fatal(err)
	}
	source := handoverSource(t, t.TempDir(), "plan.md", "plan")
	client := newWorkspaceProtocolTestClient()
	client.setIdentity("test", "protocol-"+protocol.ProtocolVersion, []string{protocol.CapabilityWorkspaceSessions})
	payload, _ := json.Marshal(protocol.TicketHandoverMessage{
		Cmd: protocol.CmdTicketHandover, SourceSessionID: store.TicketAuthorYou,
		TicketID: protocol.Ptr("ui-ticket"), Files: []protocol.TicketHandoverFile{source}, RequestID: protocol.Ptr("h1"),
	})
	d.handleClientMessage(client, payload)
	var result protocol.TicketHandoverResultMessage
	readNotebookWSEvent(t, client.send, &result)
	if result.RequestID != "h1" || !result.Success || result.Result == nil || result.Result.TicketID != "ui-ticket" {
		t.Fatalf("websocket handover = %+v", result)
	}
}

func TestTicketHandoverNotifiesChiefAndBroadcasts(t *testing.T) {
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
	source := handoverSource(t, t.TempDir(), "report.md", "findings")
	resp := callTicketHandover(t, d, &protocol.TicketHandoverMessage{Cmd: protocol.CmdTicketHandover, SourceSessionID: agentID, Files: []protocol.TicketHandoverFile{source}})
	if !resp.Ok {
		t.Fatalf("handover failed: %+v", resp)
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
