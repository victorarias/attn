package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/workspacelayout"
)

// setupSessionWorkspaceWithPanel mirrors the real app flow that produces a
// panel-only workspace: register a workspace, add and spawn one agent session
// pane, then dock a markdown panel beside it. The session is later closed in
// each test; the docked panel is what should keep the workspace alive.
func setupSessionWorkspaceWithPanel(t *testing.T) (d *Daemon, client *wsClient, workspaceID, sessionID, paneID string) {
	t.Helper()
	d = NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	client = newWorkspaceProtocolTestClient()
	workspaceID = "workspace-panel-lifecycle"
	sessionID = "session-panel-lifecycle"
	paneID = "pane-panel-lifecycle"
	cwd := t.TempDir()

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        workspaceID,
		Title:     "Panel Lifecycle",
		Directory: cwd,
	})
	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID: workspaceID,
		PaneID:      protocol.Ptr(paneID),
		SessionID:   sessionID,
		Title:       protocol.Ptr("shell"),
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, workspaceID, paneID, true)
	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:         protocol.CmdSpawnSession,
		ID:          sessionID,
		Label:       protocol.Ptr("shell"),
		Cwd:         cwd,
		Agent:       protocol.AgentShellValue,
		WorkspaceID: workspaceID,
		Cols:        80,
		Rows:        24,
	})
	expectSpawnResult(t, client, sessionID, true)

	file := filepath.Join(cwd, "notes.md")
	if err := os.WriteFile(file, []byte("# Notes\n"), 0o644); err != nil {
		t.Fatalf("write panel file: %v", err)
	}
	if err := d.dockPanel(workspaceID, paneID, markdownPanelID, string(workspacelayout.PanelKindMarkdown), file, protocol.WorkspaceLayoutDockEdgeRight, nil); err != nil {
		t.Fatalf("dock panel: %v", err)
	}
	return d, client, workspaceID, sessionID, paneID
}

// assertPanelOnlyWorkspaceAlive checks the invariant at the heart of panel-only
// workspaces: the session is gone, but the workspace entity, its layout, and the
// docked panel all survive, and the workspace tracks no sessions.
func assertPanelOnlyWorkspaceAlive(t *testing.T, d *Daemon, workspaceID, sessionID string) {
	t.Helper()
	if session := d.store.Get(sessionID); session != nil {
		t.Fatalf("session %s still registered after its pane closed", sessionID)
	}
	if ws := d.store.GetWorkspace(workspaceID); ws == nil {
		t.Fatal("workspace was torn down even though a docked panel remained")
	}
	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if snapshot == nil {
		t.Fatal("workspace layout was removed even though a docked panel remained")
	}
	if panels := workspacelayout.PanelIDs(snapshot.Layout); len(panels) != 1 || panels[0] != markdownPanelID {
		t.Fatalf("layout panels = %v, want [%s]", panels, markdownPanelID)
	}
	if panes := workspacelayout.PaneIDs(snapshot.Layout); len(panes) != 0 {
		t.Fatalf("layout panes = %v, want none after the session pane closed", panes)
	}
	if _, registered := d.workspaces.snapshot(workspaceID); !registered {
		t.Fatal("workspace dropped from the in-memory registry")
	}
	if ids := d.workspaces.sessionIDs(workspaceID); len(ids) != 0 {
		t.Fatalf("workspace still tracks sessions %v after its last session left", ids)
	}
}

func TestClosingLastPaneKeepsPanelOnlyWorkspaceAlive(t *testing.T) {
	d, client, workspaceID, sessionID, paneID := setupSessionWorkspaceWithPanel(t)

	d.handleWorkspaceLayoutClosePane(client, &protocol.WorkspaceLayoutClosePaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutClosePane,
		WorkspaceID: workspaceID,
		PaneID:      paneID,
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutClosePane, workspaceID, paneID, true)

	assertPanelOnlyWorkspaceAlive(t, d, workspaceID, sessionID)
}

func TestPanelOnlyWorkspaceSurvivesStartupReap(t *testing.T) {
	d, client, workspaceID, sessionID, paneID := setupSessionWorkspaceWithPanel(t)
	d.handleWorkspaceLayoutClosePane(client, &protocol.WorkspaceLayoutClosePaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutClosePane,
		WorkspaceID: workspaceID,
		PaneID:      paneID,
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutClosePane, workspaceID, paneID, true)
	assertPanelOnlyWorkspaceAlive(t, d, workspaceID, sessionID)

	// Simulate a daemon restart: drop the in-memory registry and rebuild it from
	// the store. The startup reap must keep a sessionless, panel-only workspace.
	d.workspaces = newWorkspaceRegistry()
	d.loadWorkspacesFromStore()

	if ws := d.store.GetWorkspace(workspaceID); ws == nil {
		t.Fatal("panel-only workspace was reaped on startup")
	}
	if _, registered := d.workspaces.snapshot(workspaceID); !registered {
		t.Fatal("panel-only workspace was not re-registered after restart")
	}
	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if snapshot == nil || len(workspacelayout.PanelIDs(snapshot.Layout)) != 1 {
		t.Fatalf("panel-only layout lost across restart: %+v", snapshot)
	}
}
