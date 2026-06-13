package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func setupWorkspaceContextSession(t *testing.T, d *Daemon, sessionID, workspaceID string) {
	t.Helper()
	d.store.AddWorkspace(&protocol.Workspace{
		ID:        workspaceID,
		Title:     workspaceID,
		Directory: t.TempDir(),
	})
	d.workspaces.register(workspaceID, workspaceID, t.TempDir(), "", false)
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             sessionID,
		Label:          sessionID,
		Agent:          protocol.SessionAgentCodex,
		Directory:      t.TempDir(),
		WorkspaceID:    workspaceID,
		State:          protocol.SessionStateIdle,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	d.workspaces.associateSession(sessionID, workspaceID, sessionID)
}

func TestWorkspaceContextCheckoutEditUpdateAndStatus(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-1", "workspace-1")

	checkout, err := d.checkoutWorkspaceContext(&protocol.WorkspaceContextCheckoutMessage{
		SourceSessionID: "session-1",
	})
	if err != nil {
		t.Fatalf("checkoutWorkspaceContext error: %v", err)
	}
	if checkout.Revision != 0 || checkout.Modified || checkout.Stale {
		t.Fatalf("initial checkout = %+v", checkout)
	}
	if !strings.HasPrefix(checkout.Path, d.dataRoot+string(filepath.Separator)) {
		t.Fatalf("checkout path %q is outside data root %q", checkout.Path, d.dataRoot)
	}
	if err := os.WriteFile(checkout.Path, []byte("# Shared goal\n"), 0o600); err != nil {
		t.Fatalf("edit checkout: %v", err)
	}
	status, err := d.workspaceContextStatus(&protocol.WorkspaceContextStatusMessage{
		SourceSessionID: "session-1",
	})
	if err != nil {
		t.Fatalf("workspaceContextStatus error: %v", err)
	}
	if !status.Modified || status.Stale {
		t.Fatalf("edited status = %+v", status)
	}

	client := &wsClient{send: make(chan outboundMessage, 1)}
	d.wsHub.clients[client] = true
	go d.wsHub.run()

	updated, changed, err := d.updateWorkspaceContext(&protocol.WorkspaceContextUpdateMessage{
		SourceSessionID: "session-1",
	})
	if err != nil {
		t.Fatalf("updateWorkspaceContext error: %v", err)
	}
	if !changed || updated.Revision != 1 || updated.Modified || updated.Stale {
		t.Fatalf("updated result = %+v, changed=%v", updated, changed)
	}
	select {
	case message := <-client.send:
		var event protocol.WorkspaceContextChangedMessage
		if err := json.Unmarshal(message.payload, &event); err != nil {
			t.Fatalf("decode workspace context event: %v", err)
		}
		if event.Event != protocol.EventWorkspaceContextChanged ||
			event.WorkspaceID != "workspace-1" ||
			event.Revision != 1 ||
			event.UpdatedBySessionID != "session-1" {
			t.Fatalf("workspace context event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("workspace_context_changed was not broadcast")
	}
}

func TestWorkspaceContextUntouchedInitialUpdateIsNoOp(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-1", "workspace-1")

	checkout, err := d.checkoutWorkspaceContext(&protocol.WorkspaceContextCheckoutMessage{
		SourceSessionID: "session-1",
	})
	if err != nil {
		t.Fatalf("checkoutWorkspaceContext error: %v", err)
	}
	if checkout.Revision != 0 || checkout.Modified || checkout.Stale {
		t.Fatalf("initial checkout = %+v", checkout)
	}

	updated, changed, err := d.updateWorkspaceContext(&protocol.WorkspaceContextUpdateMessage{
		SourceSessionID: "session-1",
	})
	if err != nil {
		t.Fatalf("updateWorkspaceContext error: %v", err)
	}
	if changed || updated.Revision != 0 || updated.Modified || updated.Stale {
		t.Fatalf("updated result = %+v, changed=%v", updated, changed)
	}
	if d.store.HasWorkspaceContext("workspace-1") {
		t.Fatal("untouched initial update created a workspace context row")
	}
	if queued := len(d.wsHub.broadcast); queued != 0 {
		t.Fatalf("untouched initial update queued %d broadcast messages", queued)
	}

	d.dissociateSessionFromWorkspace("session-1")
	if d.store.GetWorkspace("workspace-1") != nil {
		t.Fatal("empty-context workspace survived after its last session left")
	}
}

func TestWorkspaceContextConflictPreservesLocalEdits(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-1", "workspace-1")
	setupWorkspaceContextSession(t, d, "session-2", "workspace-1")

	first, err := d.checkoutWorkspaceContext(&protocol.WorkspaceContextCheckoutMessage{SourceSessionID: "session-1"})
	if err != nil {
		t.Fatalf("first checkout error: %v", err)
	}
	second, err := d.checkoutWorkspaceContext(&protocol.WorkspaceContextCheckoutMessage{SourceSessionID: "session-2"})
	if err != nil {
		t.Fatalf("second checkout error: %v", err)
	}
	if err := os.WriteFile(first.Path, []byte("first update\n"), 0o600); err != nil {
		t.Fatalf("edit first checkout: %v", err)
	}
	if _, _, err := d.updateWorkspaceContext(&protocol.WorkspaceContextUpdateMessage{SourceSessionID: "session-1"}); err != nil {
		t.Fatalf("first update error: %v", err)
	}
	if err := os.WriteFile(second.Path, []byte("second local edit\n"), 0o600); err != nil {
		t.Fatalf("edit second checkout: %v", err)
	}
	if _, _, err := d.updateWorkspaceContext(&protocol.WorkspaceContextUpdateMessage{SourceSessionID: "session-2"}); err == nil ||
		!strings.Contains(err.Error(), "revision conflict") {
		t.Fatalf("second update error = %v, want revision conflict", err)
	}
	content, err := os.ReadFile(second.Path)
	if err != nil {
		t.Fatalf("read preserved checkout: %v", err)
	}
	if string(content) != "second local edit\n" {
		t.Fatalf("local edits after conflict = %q", content)
	}
	status, err := d.workspaceContextStatus(&protocol.WorkspaceContextStatusMessage{SourceSessionID: "session-2"})
	if err != nil {
		t.Fatalf("status after conflict error: %v", err)
	}
	if !status.Modified || !status.Stale || status.Revision != 0 || status.CanonicalRevision != 1 {
		t.Fatalf("status after conflict = %+v", status)
	}
}

func TestWorkspaceContextCheckoutDoesNotOverwriteIncompleteLocalState(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-1", "workspace-1")

	checkout, err := d.checkoutWorkspaceContext(&protocol.WorkspaceContextCheckoutMessage{SourceSessionID: "session-1"})
	if err != nil {
		t.Fatalf("initial checkout error: %v", err)
	}
	if err := os.WriteFile(checkout.Path, []byte("keep this edit\n"), 0o600); err != nil {
		t.Fatalf("edit checkout: %v", err)
	}
	_, metadataPath := workspaceContextCheckoutPaths(d.dataRoot, "session-1")
	if err := os.Remove(metadataPath); err != nil {
		t.Fatalf("remove checkout metadata: %v", err)
	}

	if _, err := d.checkoutWorkspaceContext(&protocol.WorkspaceContextCheckoutMessage{SourceSessionID: "session-1"}); err == nil ||
		!strings.Contains(err.Error(), "local files preserved") {
		t.Fatalf("checkout error = %v, want preserved-local-state error", err)
	}
	content, err := os.ReadFile(checkout.Path)
	if err != nil {
		t.Fatalf("read preserved checkout: %v", err)
	}
	if string(content) != "keep this edit\n" {
		t.Fatalf("checkout content after failed refresh = %q", content)
	}
}

func TestWorkspaceContextIsRemovedWhenLastSessionLeaves(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-1", "workspace-1")
	if _, _, err := d.store.UpdateWorkspaceContext("workspace-1", "shared", "session-1", 0); err != nil {
		t.Fatalf("seed workspace context: %v", err)
	}

	d.dissociateSessionFromWorkspace("session-1")

	if d.store.GetWorkspace("workspace-1") != nil {
		t.Fatal("context-only workspace survived after its last session left")
	}
	if _, ok := d.workspaces.snapshot("workspace-1"); ok {
		t.Fatal("context-only workspace remained in the registry")
	}
	if d.store.HasWorkspaceContext("workspace-1") {
		t.Fatal("workspace context survived workspace teardown")
	}
}

func TestClosingFinalPaneRemovesContextWorkspace(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	client := newWorkspaceProtocolTestClient()
	workspaceID := "workspace-context-close"
	sessionID := "session-context-close"
	paneID := "pane-context-close"
	cwd := t.TempDir()

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: workspaceID, Title: "Context", Directory: cwd,
	})
	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutAddSessionPane, WorkspaceID: workspaceID,
		PaneID: protocol.Ptr(paneID), SessionID: sessionID, Title: protocol.Ptr("codex"),
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, workspaceID, paneID, true)
	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd: protocol.CmdSpawnSession, ID: sessionID, Label: protocol.Ptr("codex"),
		Cwd: cwd, Agent: string(protocol.SessionAgentCodex), WorkspaceID: workspaceID, Cols: 80, Rows: 24,
	})
	expectSpawnResult(t, client, sessionID, true)
	if _, _, err := d.store.UpdateWorkspaceContext(workspaceID, "shared", sessionID, 0); err != nil {
		t.Fatalf("seed workspace context: %v", err)
	}

	cap := captureBroadcasts(d)
	d.handleWorkspaceLayoutClosePane(client, &protocol.WorkspaceLayoutClosePaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: workspaceID, PaneID: paneID,
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutClosePane, workspaceID, paneID, true)

	if d.store.GetWorkspace(workspaceID) != nil {
		t.Fatal("context-only workspace survived final pane close")
	}
	if d.store.HasWorkspaceContext(workspaceID) {
		t.Fatal("workspace context survived final pane close")
	}
	if layout := d.store.GetWorkspaceLayout(workspaceID); layout != nil {
		t.Fatalf("empty workspace layout remained persisted: %+v", layout)
	}

	events := cap.snapshot()
	if len(events) != 2 {
		t.Fatalf("close broadcasts = %d, want 2: %+v", len(events), events)
	}
	if events[0].Event != protocol.EventSessionUnregistered ||
		events[1].Event != protocol.EventWorkspaceUnregistered {
		t.Fatalf("close broadcast order = [%s, %s]", events[0].Event, events[1].Event)
	}
}
