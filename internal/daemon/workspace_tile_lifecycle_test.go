package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/workspacelayout"
)

// setupSessionWorkspaceWithTile mirrors the real app flow that produces a
// tile-only workspace: register a workspace, add and spawn one agent session
// pane, then dock a markdown tile beside it. The session is later closed in
// each test; the docked tile is what should keep the workspace alive.
func setupSessionWorkspaceWithTile(t *testing.T) (d *Daemon, client *wsClient, workspaceID, sessionID, paneID string) {
	t.Helper()
	d = NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	client = newWorkspaceProtocolTestClient()
	workspaceID = "workspace-tile-lifecycle"
	sessionID = "session-tile-lifecycle"
	paneID = "pane-tile-lifecycle"
	cwd := t.TempDir()

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        workspaceID,
		Title:     "Tile Lifecycle",
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
		t.Fatalf("write tile file: %v", err)
	}
	if err := d.dockTile(workspaceID, paneID, markdownTileIDForPath(file), string(workspacelayout.TileKindMarkdown), file, "", protocol.WorkspaceLayoutDockEdgeRight, nil); err != nil {
		t.Fatalf("dock tile: %v", err)
	}
	return d, client, workspaceID, sessionID, paneID
}

// assertTileOnlyWorkspaceAlive checks the invariant at the heart of tile-only
// workspaces: the session is gone, but the workspace entity, its layout, and the
// docked tile all survive, and the workspace tracks no sessions.
func assertTileOnlyWorkspaceAlive(t *testing.T, d *Daemon, workspaceID, sessionID string) {
	t.Helper()
	if session := d.store.Get(sessionID); session != nil {
		t.Fatalf("session %s still registered after its pane closed", sessionID)
	}
	if ws := d.store.GetWorkspace(workspaceID); ws == nil {
		t.Fatal("workspace was torn down even though a docked tile remained")
	}
	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if snapshot == nil {
		t.Fatal("workspace layout was removed even though a docked tile remained")
	}
	if tiles := workspacelayout.TileIDs(snapshot.Layout); len(tiles) != 1 || !strings.HasPrefix(tiles[0], markdownTileIDPrefix) {
		t.Fatalf("layout tiles = %v, want a single %s* tile", tiles, markdownTileIDPrefix)
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

func TestClosingLastPaneKeepsTileOnlyWorkspaceAlive(t *testing.T) {
	d, client, workspaceID, sessionID, paneID := setupSessionWorkspaceWithTile(t)

	d.handleWorkspaceLayoutClosePane(client, &protocol.WorkspaceLayoutClosePaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutClosePane,
		WorkspaceID: workspaceID,
		PaneID:      paneID,
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutClosePane, workspaceID, paneID, true)

	assertTileOnlyWorkspaceAlive(t, d, workspaceID, sessionID)
}

func TestTileOnlyWorkspaceSurvivesStartupReap(t *testing.T) {
	d, client, workspaceID, sessionID, paneID := setupSessionWorkspaceWithTile(t)
	d.handleWorkspaceLayoutClosePane(client, &protocol.WorkspaceLayoutClosePaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutClosePane,
		WorkspaceID: workspaceID,
		PaneID:      paneID,
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutClosePane, workspaceID, paneID, true)
	assertTileOnlyWorkspaceAlive(t, d, workspaceID, sessionID)

	// Simulate a daemon restart: drop the in-memory registry and rebuild it from
	// the store. The startup reap must keep a sessionless, tile-only workspace.
	d.workspaces = newWorkspaceRegistry()
	d.loadWorkspacesFromStore()

	if ws := d.store.GetWorkspace(workspaceID); ws == nil {
		t.Fatal("tile-only workspace was reaped on startup")
	}
	if _, registered := d.workspaces.snapshot(workspaceID); !registered {
		t.Fatal("tile-only workspace was not re-registered after restart")
	}
	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if snapshot == nil || len(workspacelayout.TileIDs(snapshot.Layout)) != 1 {
		t.Fatalf("tile-only layout lost across restart: %+v", snapshot)
	}
}
