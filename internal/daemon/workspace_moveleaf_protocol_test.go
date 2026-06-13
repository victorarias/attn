package daemon

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/workspacelayout"
)

// addAndSpawnSessionPane registers a session pane the way the app does — add the
// layout pane, then spawn the runtime — so move tests run against a real
// multi-pane layout.
func addAndSpawnSessionPane(t *testing.T, d *Daemon, client *wsClient, workspaceID, sessionID, paneID, targetPaneID, cwd string) {
	t.Helper()
	add := &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID: workspaceID,
		PaneID:      protocol.Ptr(paneID),
		SessionID:   sessionID,
		Title:       protocol.Ptr(sessionID),
	}
	if targetPaneID != "" {
		add.TargetPaneID = protocol.Ptr(targetPaneID)
	}
	d.handleWorkspaceLayoutAddSessionPane(client, add)
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, workspaceID, paneID, true)

	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:         protocol.CmdSpawnSession,
		ID:          sessionID,
		Label:       protocol.Ptr(sessionID),
		Cwd:         cwd,
		Agent:       protocol.AgentShellValue,
		WorkspaceID: workspaceID,
		Cols:        80,
		Rows:        24,
	})
	expectSpawnResult(t, client, sessionID, true)
}

func TestWorkspaceLayoutMoveLeafRelocatesPane(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	client := newWorkspaceProtocolTestClient()
	workspaceID := "ws-move"
	cwd := t.TempDir()

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        workspaceID,
		Title:     "Move",
		Directory: cwd,
	})
	addAndSpawnSessionPane(t, d, client, workspaceID, "s-1", "pane-1", "", cwd)
	addAndSpawnSessionPane(t, d, client, workspaceID, "s-2", "pane-2", "pane-1", cwd)

	// Move pane-1 onto the bottom edge of pane-2: the left/right split collapses
	// and pane-1 is restacked under pane-2 in a horizontal split.
	d.handleWorkspaceLayoutMoveLeaf(client, &protocol.WorkspaceLayoutMoveLeafMessage{
		Cmd:         protocol.CmdWorkspaceLayoutMoveLeaf,
		WorkspaceID: workspaceID,
		LeafID:      "pane-1",
		AnchorID:    "pane-2",
		Edge:        protocol.WorkspaceLayoutDockEdgeBottom,
		Ratio:       protocol.Ptr(0.5),
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutMoveLeaf, workspaceID, "pane-1", true)

	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if snapshot == nil {
		t.Fatal("workspace layout missing after move")
	}
	layout := snapshot.Layout
	if layout.Type != "split" || layout.Direction != workspacelayout.DirectionHorizontal {
		t.Fatalf("root = %+v, want a horizontal split (top/bottom stack)", layout)
	}
	if layout.Children[0].PaneID != "pane-2" || layout.Children[1].PaneID != "pane-1" {
		t.Fatalf("children = %+v, want [pane-2, pane-1]", layout.Children)
	}
	// The move must not tear down either session.
	if d.store.Get("s-1") == nil || d.store.Get("s-2") == nil {
		t.Fatal("a session was lost during a pane move")
	}
}

func TestWorkspaceLayoutMoveLeafSelfDropIsRejected(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	client := newWorkspaceProtocolTestClient()
	workspaceID := "ws-self"
	cwd := t.TempDir()

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        workspaceID,
		Title:     "Self",
		Directory: cwd,
	})
	addAndSpawnSessionPane(t, d, client, workspaceID, "s-1", "pane-1", "", cwd)
	addAndSpawnSessionPane(t, d, client, workspaceID, "s-2", "pane-2", "pane-1", cwd)
	before := d.store.GetWorkspaceLayout(workspaceID).Layout

	d.handleWorkspaceLayoutMoveLeaf(client, &protocol.WorkspaceLayoutMoveLeafMessage{
		Cmd:         protocol.CmdWorkspaceLayoutMoveLeaf,
		WorkspaceID: workspaceID,
		LeafID:      "pane-1",
		AnchorID:    "pane-1",
		Edge:        protocol.WorkspaceLayoutDockEdgeRight,
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutMoveLeaf, workspaceID, "pane-1", false)

	after := d.store.GetWorkspaceLayout(workspaceID).Layout
	if after.SplitID != before.SplitID || after.Children[0].PaneID != before.Children[0].PaneID {
		t.Fatalf("self-drop changed the layout: before=%+v after=%+v", before, after)
	}
}

func TestWorkspaceLayoutMoveLeafToWorkspaceMovesPaneAndSessionOwnership(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	client := newWorkspaceProtocolTestClient()
	sourceWorkspaceID := "ws-source"
	targetWorkspaceID := "ws-target"
	sourceCwd := t.TempDir()
	targetCwd := t.TempDir()

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        sourceWorkspaceID,
		Title:     "Source",
		Directory: sourceCwd,
	})
	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        targetWorkspaceID,
		Title:     "Target",
		Directory: targetCwd,
	})
	addAndSpawnSessionPane(t, d, client, sourceWorkspaceID, "s-source", "pane-source", "", sourceCwd)
	addAndSpawnSessionPane(t, d, client, targetWorkspaceID, "s-target", "pane-target", "", targetCwd)

	d.handleWorkspaceLayoutMoveLeafToWorkspace(client, &protocol.WorkspaceLayoutMoveLeafToWorkspaceMessage{
		Cmd:               protocol.CmdWorkspaceLayoutMoveLeafToWorkspace,
		SourceWorkspaceID: sourceWorkspaceID,
		TargetWorkspaceID: targetWorkspaceID,
		LeafID:            "pane-source",
		AnchorID:          protocol.Ptr("pane-target"),
		Edge:              protocol.WorkspaceLayoutDockEdgeRight,
		Ratio:             protocol.Ptr(0.4),
	})
	expectWorkspaceLayoutMoveToWorkspaceResult(t, client, sourceWorkspaceID, targetWorkspaceID, "pane-source", "pane-source", true)

	if sourceLayout := d.store.GetWorkspaceLayout(sourceWorkspaceID); sourceLayout != nil {
		t.Fatalf("source layout still exists after moving only pane: %+v", sourceLayout)
	}
	targetLayout := d.store.GetWorkspaceLayout(targetWorkspaceID)
	if targetLayout == nil {
		t.Fatal("target layout missing after move")
	}
	if !workspacelayout.HasPane(targetLayout.Layout, "pane-source") || !workspacelayout.HasPane(targetLayout.Layout, "pane-target") {
		t.Fatalf("target layout = %+v, want both panes", targetLayout.Layout)
	}
	if session := d.store.Get("s-source"); session == nil || session.WorkspaceID != targetWorkspaceID {
		t.Fatalf("moved session = %+v, want workspace %s", session, targetWorkspaceID)
	}
	if sourceWorkspace := d.store.GetWorkspace(sourceWorkspaceID); sourceWorkspace != nil {
		t.Fatalf("empty source workspace still exists: %+v", sourceWorkspace)
	}
}

func TestWorkspaceLayoutMoveLeafToWorkspaceBroadcastsLayoutBeforeSessionOwnership(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	client := newWorkspaceProtocolTestClient()
	sourceWorkspaceID := "ws-source-order"
	targetWorkspaceID := "ws-target-order"
	sourceCwd := t.TempDir()
	targetCwd := t.TempDir()

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        sourceWorkspaceID,
		Title:     "Source",
		Directory: sourceCwd,
	})
	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        targetWorkspaceID,
		Title:     "Target",
		Directory: targetCwd,
	})
	addAndSpawnSessionPane(t, d, client, sourceWorkspaceID, "s-source-order", "pane-source", "", sourceCwd)
	addAndSpawnSessionPane(t, d, client, targetWorkspaceID, "s-target-order", "pane-target", "", targetCwd)

	cap := captureBroadcasts(d)
	d.handleWorkspaceLayoutMoveLeafToWorkspace(client, &protocol.WorkspaceLayoutMoveLeafToWorkspaceMessage{
		Cmd:               protocol.CmdWorkspaceLayoutMoveLeafToWorkspace,
		SourceWorkspaceID: sourceWorkspaceID,
		TargetWorkspaceID: targetWorkspaceID,
		LeafID:            "pane-source",
		AnchorID:          protocol.Ptr("pane-target"),
		Edge:              protocol.WorkspaceLayoutDockEdgeRight,
	})
	expectWorkspaceLayoutMoveToWorkspaceResult(t, client, sourceWorkspaceID, targetWorkspaceID, "pane-source", "pane-source", true)

	events := cap.snapshot()
	if len(events) < 2 {
		t.Fatalf("expected layout and session broadcasts, got %d: %+v", len(events), events)
	}
	first := events[0]
	if first.Event != protocol.EventWorkspaceLayoutUpdated || first.WorkspaceLayout == nil || first.WorkspaceLayout.WorkspaceID != targetWorkspaceID {
		t.Fatalf("first broadcast = %+v, want target workspace_layout_updated", first)
	}
	second := events[1]
	if second.Event != protocol.EventSessionStateChanged || second.Session == nil || second.Session.ID != "s-source-order" || second.Session.WorkspaceID != targetWorkspaceID {
		t.Fatalf("second broadcast = %+v, want moved session_state_changed", second)
	}
}

func TestMoveLeafToNewWorkspaceCreatesWorkspace(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	client := newWorkspaceProtocolTestClient()
	sourceWorkspaceID := "ws-source"
	sourceCwd := t.TempDir()

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        sourceWorkspaceID,
		Title:     "Source",
		Directory: sourceCwd,
	})
	addAndSpawnSessionPane(t, d, client, sourceWorkspaceID, "s-1", "pane-1", "", sourceCwd)
	addAndSpawnSessionPane(t, d, client, sourceWorkspaceID, "s-2", "pane-2", "pane-1", sourceCwd)

	// No pre-created target: the daemon must mint a brand-new workspace.
	d.handleWorkspaceLayoutMoveLeafToNewWorkspace(client, &protocol.WorkspaceLayoutMoveLeafToNewWorkspaceMessage{
		Cmd:               protocol.CmdWorkspaceLayoutMoveLeafToNewWorkspace,
		SourceWorkspaceID: sourceWorkspaceID,
		LeafID:            "pane-1",
	})
	newWorkspaceID := expectMoveToNewWorkspaceResult(t, client, sourceWorkspaceID, "pane-1", true)

	if newWorkspaceID == "" || newWorkspaceID == sourceWorkspaceID {
		t.Fatalf("new workspace id = %q, want a fresh id distinct from the source", newWorkspaceID)
	}

	// The new workspace exists, inherits the source directory, and sorts after
	// the source (PR1 seeds rank above the current maximum).
	newWorkspace := d.store.GetWorkspace(newWorkspaceID)
	if newWorkspace == nil {
		t.Fatalf("new workspace %s was not registered", newWorkspaceID)
	}
	if newWorkspace.Directory != sourceCwd {
		t.Fatalf("new workspace directory = %q, want inherited %q", newWorkspace.Directory, sourceCwd)
	}
	if source := d.store.GetWorkspace(sourceWorkspaceID); source != nil && !(newWorkspace.Rank > source.Rank) {
		t.Fatalf("new workspace rank %q should sort after source rank %q", newWorkspace.Rank, source.Rank)
	}

	// The moved pane and its session now live in the new workspace.
	newLayout := d.store.GetWorkspaceLayout(newWorkspaceID)
	if newLayout == nil {
		t.Fatal("new workspace layout missing after move")
	}
	if !workspacelayout.HasPane(newLayout.Layout, "pane-1") {
		t.Fatalf("new workspace layout = %+v, want moved pane-1", newLayout.Layout)
	}
	if session := d.store.Get("s-1"); session == nil || session.WorkspaceID != newWorkspaceID {
		t.Fatalf("moved session = %+v, want workspace %s", session, newWorkspaceID)
	}

	// The source keeps its other leaf and survives.
	sourceLayout := d.store.GetWorkspaceLayout(sourceWorkspaceID)
	if sourceLayout == nil {
		t.Fatal("source layout torn down despite a remaining leaf")
	}
	if !workspacelayout.HasPane(sourceLayout.Layout, "pane-2") {
		t.Fatalf("source layout = %+v, want remaining pane-2", sourceLayout.Layout)
	}
	if workspacelayout.HasPane(sourceLayout.Layout, "pane-1") {
		t.Fatalf("source layout = %+v, moved pane-1 should be gone", sourceLayout.Layout)
	}
	if d.store.GetWorkspace(sourceWorkspaceID) == nil {
		t.Fatal("source workspace removed despite a remaining leaf")
	}
}

func TestMoveLeafToNewWorkspaceTearsDownEmptySource(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	client := newWorkspaceProtocolTestClient()
	sourceWorkspaceID := "ws-only"
	sourceCwd := t.TempDir()

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        sourceWorkspaceID,
		Title:     "Only",
		Directory: sourceCwd,
	})
	// A single leaf: moving it out empties the source.
	addAndSpawnSessionPane(t, d, client, sourceWorkspaceID, "s-only", "pane-only", "", sourceCwd)

	d.handleWorkspaceLayoutMoveLeafToNewWorkspace(client, &protocol.WorkspaceLayoutMoveLeafToNewWorkspaceMessage{
		Cmd:               protocol.CmdWorkspaceLayoutMoveLeafToNewWorkspace,
		SourceWorkspaceID: sourceWorkspaceID,
		LeafID:            "pane-only",
	})
	newWorkspaceID := expectMoveToNewWorkspaceResult(t, client, sourceWorkspaceID, "pane-only", true)

	if d.store.GetWorkspace(sourceWorkspaceID) != nil {
		t.Fatalf("empty source workspace still exists after moving its only leaf")
	}
	if d.store.GetWorkspaceLayout(sourceWorkspaceID) != nil {
		t.Fatalf("empty source layout still exists after moving its only leaf")
	}
	newLayout := d.store.GetWorkspaceLayout(newWorkspaceID)
	if newLayout == nil || !workspacelayout.HasPane(newLayout.Layout, "pane-only") {
		t.Fatalf("new workspace layout = %+v, want moved pane-only", newLayout)
	}
	if session := d.store.Get("s-only"); session == nil || session.WorkspaceID != newWorkspaceID {
		t.Fatalf("moved session = %+v, want workspace %s", session, newWorkspaceID)
	}
}

func TestMoveLeafToNewWorkspaceBroadcastsRegisteredBeforeLayout(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	client := newWorkspaceProtocolTestClient()
	sourceWorkspaceID := "ws-order"
	sourceCwd := t.TempDir()

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        sourceWorkspaceID,
		Title:     "Order",
		Directory: sourceCwd,
	})
	addAndSpawnSessionPane(t, d, client, sourceWorkspaceID, "s-a", "pane-a", "", sourceCwd)
	addAndSpawnSessionPane(t, d, client, sourceWorkspaceID, "s-b", "pane-b", "pane-a", sourceCwd)

	cap := captureBroadcasts(d)
	d.handleWorkspaceLayoutMoveLeafToNewWorkspace(client, &protocol.WorkspaceLayoutMoveLeafToNewWorkspaceMessage{
		Cmd:               protocol.CmdWorkspaceLayoutMoveLeafToNewWorkspace,
		SourceWorkspaceID: sourceWorkspaceID,
		LeafID:            "pane-a",
	})
	newWorkspaceID := expectMoveToNewWorkspaceResult(t, client, sourceWorkspaceID, "pane-a", true)

	// The new workspace must be announced as registered before any layout
	// update references it, so clients never see a layout for an unknown
	// workspace.
	registeredIdx, layoutIdx := -1, -1
	for i, event := range cap.snapshot() {
		switch {
		case registeredIdx == -1 && event.Event == protocol.EventWorkspaceRegistered &&
			event.Workspace != nil && event.Workspace.ID == newWorkspaceID:
			registeredIdx = i
		case layoutIdx == -1 && event.Event == protocol.EventWorkspaceLayoutUpdated &&
			event.WorkspaceLayout != nil && event.WorkspaceLayout.WorkspaceID == newWorkspaceID:
			layoutIdx = i
		}
	}
	if registeredIdx == -1 {
		t.Fatal("no workspace_registered broadcast for the new workspace")
	}
	if layoutIdx == -1 {
		t.Fatal("no workspace_layout_updated broadcast for the new workspace")
	}
	if registeredIdx > layoutIdx {
		t.Fatalf("workspace_registered (#%d) must precede workspace_layout_updated (#%d)", registeredIdx, layoutIdx)
	}
}

// expectMoveToNewWorkspaceResult waits for the move-to-new-workspace action
// result, asserts source/leaf identity and success, and returns the minted new
// workspace id (target_workspace_id) for follow-up assertions.
func expectMoveToNewWorkspaceResult(t *testing.T, client *wsClient, sourceWorkspaceID, leafID string, success bool) string {
	t.Helper()
	deadline := time.After(1 * time.Second)
	for {
		select {
		case outbound := <-client.send:
			var result protocol.WorkspaceLayoutActionResultMessage
			if err := json.Unmarshal(outbound.payload, &result); err != nil || result.Event != protocol.EventWorkspaceLayoutActionResult {
				continue
			}
			if result.Action != protocol.CmdWorkspaceLayoutMoveLeafToNewWorkspace || result.WorkspaceID != sourceWorkspaceID {
				continue
			}
			if result.Success != success {
				t.Fatalf("move-to-new-workspace success = %v, want %v; payload=%s", result.Success, success, string(outbound.payload))
			}
			if got := protocol.Deref(result.SourceWorkspaceID); got != sourceWorkspaceID {
				t.Fatalf("source_workspace_id = %q, want %q; payload=%s", got, sourceWorkspaceID, string(outbound.payload))
			}
			if got := protocol.Deref(result.LeafID); got != leafID {
				t.Fatalf("leaf_id = %q, want %q; payload=%s", got, leafID, string(outbound.payload))
			}
			return protocol.Deref(result.TargetWorkspaceID)
		case <-deadline:
			t.Fatalf("timed out waiting for move-to-new-workspace action")
		}
	}
}

func expectWorkspaceLayoutMoveToWorkspaceResult(t *testing.T, client *wsClient, sourceWorkspaceID, targetWorkspaceID, leafID, finalLeafID string, success bool) {
	t.Helper()
	deadline := time.After(1 * time.Second)
	for {
		select {
		case outbound := <-client.send:
			var result protocol.WorkspaceLayoutActionResultMessage
			if err := json.Unmarshal(outbound.payload, &result); err != nil || result.Event != protocol.EventWorkspaceLayoutActionResult {
				continue
			}
			if result.Action != protocol.CmdWorkspaceLayoutMoveLeafToWorkspace || result.WorkspaceID != sourceWorkspaceID {
				continue
			}
			if result.Success != success {
				t.Fatalf("workspace move success = %v, want %v; payload=%s", result.Success, success, string(outbound.payload))
			}
			if got := protocol.Deref(result.SourceWorkspaceID); got != sourceWorkspaceID {
				t.Fatalf("source_workspace_id = %q, want %q; payload=%s", got, sourceWorkspaceID, string(outbound.payload))
			}
			if got := protocol.Deref(result.TargetWorkspaceID); got != targetWorkspaceID {
				t.Fatalf("target_workspace_id = %q, want %q; payload=%s", got, targetWorkspaceID, string(outbound.payload))
			}
			if got := protocol.Deref(result.LeafID); got != leafID {
				t.Fatalf("leaf_id = %q, want %q; payload=%s", got, leafID, string(outbound.payload))
			}
			if got := protocol.Deref(result.FinalLeafID); got != finalLeafID {
				t.Fatalf("final_leaf_id = %q, want %q; payload=%s", got, finalLeafID, string(outbound.payload))
			}
			return
		case <-deadline:
			t.Fatalf("timed out waiting for workspace move-to-workspace action")
		}
	}
}
