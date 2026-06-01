package daemon

import (
	"path/filepath"
	"testing"

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
