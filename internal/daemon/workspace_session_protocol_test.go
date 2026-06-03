package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/workspacelayout"
)

func TestWorkspaceSessionProtocolLifecycleMatchesAppOrder(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	client := newWorkspaceProtocolTestClient()
	workspaceID := "workspace-real-app-order"
	sessionID := "session-shell-1"
	paneID := "pane-session-shell-1"
	cwd := t.TempDir()

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        workspaceID,
		Title:     "Real App Order",
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
	expectPaneStatus(t, d, workspaceID, paneID, workspacelayout.PaneStatusSpawning, "")

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
	expectPaneStatus(t, d, workspaceID, paneID, workspacelayout.PaneStatusReady, "")
	if session := d.store.Get(sessionID); session == nil {
		t.Fatalf("session %s was not registered", sessionID)
	}

	d.handleWorkspaceLayoutClosePane(client, &protocol.WorkspaceLayoutClosePaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutClosePane,
		WorkspaceID: workspaceID,
		PaneID:      paneID,
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutClosePane, workspaceID, paneID, true)
	if session := d.store.Get(sessionID); session != nil {
		t.Fatalf("session %s still registered after closing its workspace pane", sessionID)
	}
	if snapshot := d.store.GetWorkspaceLayout(workspaceID); snapshot != nil {
		t.Fatalf("workspace layout still exists after closing only pane: %+v", snapshot)
	}
	if workspace := d.store.GetWorkspace(workspaceID); workspace != nil {
		t.Fatalf("workspace still exists after closing its only session pane: %+v", workspace)
	}
	if _, _, ok := d.store.FindWorkspaceLayoutPaneBySessionID(sessionID); ok {
		t.Fatalf("session %s still has a workspace pane mapping", sessionID)
	}
}

func TestWorkspaceLayoutClosePaneKeepsLayoutUntilSessionUnregistered(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client := newWorkspaceProtocolTestClient()
	workspaceID := "workspace-close-order"
	sessionID := "session-close-order"
	paneID := "pane-session-close-order"
	cwd := t.TempDir()

	backend := &fakeSpawnBackend{}
	backend.onKill = func() {
		if session := d.store.Get(sessionID); session == nil {
			t.Fatalf("session %s was removed before pty kill", sessionID)
		}
		if snapshot := d.store.GetWorkspaceLayout(workspaceID); snapshot == nil {
			t.Fatalf("workspace layout %s was removed while session %s still existed", workspaceID, sessionID)
		}
		if _, _, ok := d.store.FindWorkspaceLayoutPaneBySessionID(sessionID); !ok {
			t.Fatalf("workspace pane mapping for session %s was removed before pty kill", sessionID)
		}
	}
	d.ptyBackend = backend

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        workspaceID,
		Title:     "Close Order",
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

	d.handleWorkspaceLayoutClosePane(client, &protocol.WorkspaceLayoutClosePaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutClosePane,
		WorkspaceID: workspaceID,
		PaneID:      paneID,
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutClosePane, workspaceID, paneID, true)
	if session := d.store.Get(sessionID); session != nil {
		t.Fatalf("session %s still registered after close", sessionID)
	}
	if snapshot := d.store.GetWorkspaceLayout(workspaceID); snapshot != nil {
		t.Fatalf("workspace layout still exists after closing only pane: %+v", snapshot)
	}
	if workspace := d.store.GetWorkspace(workspaceID); workspace != nil {
		t.Fatalf("workspace still exists after closing its only session pane: %+v", workspace)
	}
}

func TestWorkspaceSessionProtocolSpawnFailureMarksPaneFailed(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &failingSpawnBackend{err: errors.New("boom")}
	client := newWorkspaceProtocolTestClient()
	workspaceID := "workspace-spawn-fails"
	sessionID := "session-fails"
	paneID := "pane-session-fails"
	cwd := t.TempDir()

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        workspaceID,
		Title:     "Spawn Fails",
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
	expectSpawnResult(t, client, sessionID, false)
	expectPaneStatus(t, d, workspaceID, paneID, workspacelayout.PaneStatusFailed, "boom")
	if session := d.store.Get(sessionID); session != nil {
		t.Fatalf("failed spawn registered session %s", sessionID)
	}
}

func TestWorkspaceSessionProtocolRejectsShellSpawnWithoutWorkspace(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	client := newWorkspaceProtocolTestClient()
	sessionID := "session-shell-without-workspace"

	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:   protocol.CmdSpawnSession,
		ID:    sessionID,
		Label: protocol.Ptr("shell"),
		Cwd:   t.TempDir(),
		Agent: protocol.AgentShellValue,
		Cols:  80,
		Rows:  24,
	})

	expectCommandError(t, client, protocol.CmdSpawnSession, "missing workspace_id")
	if session := d.store.Get(sessionID); session != nil {
		t.Fatalf("shell spawn without workspace registered session %s", sessionID)
	}
}

func TestWorkspaceSessionProtocolRejectsShellSpawnForUnknownWorkspace(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	client := newWorkspaceProtocolTestClient()
	sessionID := "session-shell-unknown-workspace"

	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:         protocol.CmdSpawnSession,
		ID:          sessionID,
		Label:       protocol.Ptr("shell"),
		Cwd:         t.TempDir(),
		Agent:       protocol.AgentShellValue,
		Cols:        80,
		Rows:        24,
		WorkspaceID: "missing-workspace",
	})

	expectCommandError(t, client, protocol.CmdSpawnSession, "unknown workspace")
	if session := d.store.Get(sessionID); session != nil {
		t.Fatalf("shell spawn for unknown workspace registered session %s", sessionID)
	}
}

func TestWorkspaceLayoutSplitPaneCommandIsUnsupported(t *testing.T) {
	if _, _, err := protocol.ParseMessage([]byte(`{"cmd":"workspace_layout_split_pane","workspace_id":"ws","target_pane_id":"pane","direction":"vertical"}`)); err == nil {
		t.Fatal("legacy workspace_layout_split_pane command parsed successfully")
	}
}

func TestWorkspaceLayoutSetSplitRatioPersistsLockedRatio(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client := newWorkspaceProtocolTestClient()
	workspaceID := "workspace-split-ratio"
	cwd := t.TempDir()

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        workspaceID,
		Title:     "Split Ratio",
		Directory: cwd,
	})
	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID: workspaceID,
		PaneID:      protocol.Ptr("pane-1"),
		SessionID:   "session-1",
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, workspaceID, "pane-1", true)

	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:          protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID:  workspaceID,
		PaneID:       protocol.Ptr("pane-2"),
		SessionID:    "session-2",
		TargetPaneID: protocol.Ptr("pane-1"),
		Direction:    protocol.Ptr(protocol.WorkspaceLayoutSplitDirectionVertical),
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, workspaceID, "pane-2", true)

	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if snapshot == nil {
		t.Fatal("workspace layout missing after adding two panes")
	}
	splitID := firstSplitID(snapshot.Layout)
	if splitID == "" {
		t.Fatalf("expected a split in layout, got %+v", snapshot.Layout)
	}

	// Unknown split id -> failure.
	d.handleWorkspaceLayoutSetSplitRatio(client, &protocol.WorkspaceLayoutSetSplitRatioMessage{
		Cmd:         protocol.CmdWorkspaceLayoutSetSplitRatio,
		WorkspaceID: workspaceID,
		SplitID:     "does-not-exist",
		Ratio:       0.3,
		RequestID:   protocol.Ptr("request-missing"),
	})
	expectWorkspaceLayoutActionResultIDsAndRequestID(t, client, protocol.CmdWorkspaceLayoutSetSplitRatio, workspaceID, "", "does-not-exist", "", "request-missing", false)

	// Set and lock the real split.
	d.handleWorkspaceLayoutSetSplitRatio(client, &protocol.WorkspaceLayoutSetSplitRatioMessage{
		Cmd:         protocol.CmdWorkspaceLayoutSetSplitRatio,
		WorkspaceID: workspaceID,
		SplitID:     splitID,
		Ratio:       0.3,
		RequestID:   protocol.Ptr("request-real"),
	})
	expectWorkspaceLayoutActionResultIDsAndRequestID(t, client, protocol.CmdWorkspaceLayoutSetSplitRatio, workspaceID, "", splitID, "", "request-real", true)

	// Re-read through normalization: the locked ratio must survive (a two-pane
	// split would otherwise be rebalanced back to 0.5).
	reread, err := d.ensureWorkspaceLayout(workspaceID)
	if err != nil {
		t.Fatalf("ensureWorkspaceLayout: %v", err)
	}
	split := findSplit(reread.Layout, splitID)
	if split == nil {
		t.Fatalf("split %s missing after set ratio: %+v", splitID, reread.Layout)
	}
	if !split.RatioLocked {
		t.Fatalf("split should be locked after set ratio")
	}
	if split.Ratio < 0.29 || split.Ratio > 0.31 {
		t.Fatalf("split ratio = %v, want ~0.3 (locked ratio must not be rebalanced)", split.Ratio)
	}
}

func TestWorkspaceLayoutDockTilePersistsAndMoves(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client := newWorkspaceProtocolTestClient()
	workspaceID := "workspace-dock-tile"
	cwd := t.TempDir()

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        workspaceID,
		Title:     "Dock Tile",
		Directory: cwd,
	})
	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID: workspaceID,
		PaneID:      protocol.Ptr("pane-1"),
		SessionID:   "session-1",
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, workspaceID, "pane-1", true)
	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:          protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID:  workspaceID,
		PaneID:       protocol.Ptr("pane-2"),
		SessionID:    "session-2",
		TargetPaneID: protocol.Ptr("pane-1"),
		Direction:    protocol.Ptr(protocol.WorkspaceLayoutSplitDirectionVertical),
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, workspaceID, "pane-2", true)

	// A tile cannot take over a terminal pane's id.
	d.handleWorkspaceLayoutDockTile(client, &protocol.WorkspaceLayoutDockTileMessage{
		Cmd:          protocol.CmdWorkspaceLayoutDockTile,
		WorkspaceID:  workspaceID,
		AnchorPaneID: "pane-1",
		Edge:         protocol.WorkspaceLayoutDockEdgeRight,
		TileID:       "pane-2",
		TileKind:     "markdown",
	})
	expectWorkspaceLayoutActionResultIDs(t, client, protocol.CmdWorkspaceLayoutDockTile, workspaceID, "", "", "pane-2", false)
	afterCollision := d.store.GetWorkspaceLayout(workspaceID)
	if !workspacelayout.HasPane(afterCollision.Layout, "pane-2") || workspacelayout.HasTile(afterCollision.Layout, "pane-2") {
		t.Fatalf("pane id collision mutated layout: %+v", afterCollision.Layout)
	}

	// The trusted open-markdown path assigns the file. WebSocket docking can
	// move the tile later, but cannot retarget it.
	if err := d.dockTile(workspaceID, "pane-1", "tile-md", "markdown", "/tmp/notes.md", protocol.WorkspaceLayoutDockEdgeRight, nil); err != nil {
		t.Fatalf("dockTile: %v", err)
	}

	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if snapshot == nil {
		t.Fatal("workspace layout missing after docking tile")
	}
	if !workspacelayout.HasTile(snapshot.Layout, "tile-md") {
		t.Fatalf("tile not present after dock: %+v", snapshot.Layout)
	}
	// Tiles never leak into agent-pane bookkeeping.
	if ids := workspacelayout.PaneIDs(snapshot.Layout); len(ids) != 2 {
		t.Fatalf("pane ids = %v, want the two agent panes only", ids)
	}
	if len(snapshot.Panes) != 2 {
		t.Fatalf("snapshot panes = %+v, want only the two agent panes", snapshot.Panes)
	}

	// A terminal pane cannot take over a docked tile's id.
	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:          protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID:  workspaceID,
		PaneID:       protocol.Ptr("tile-md"),
		SessionID:    "session-3",
		TargetPaneID: protocol.Ptr("pane-1"),
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, workspaceID, "tile-md", false)
	afterPaneCollision := d.store.GetWorkspaceLayout(workspaceID)
	if len(afterPaneCollision.Panes) != 2 || !workspacelayout.HasTile(afterPaneCollision.Layout, "tile-md") {
		t.Fatalf("tile id collision mutated layout: %+v", afterPaneCollision)
	}

	// The cross-restart / cross-client guarantee: the tile survives a layout
	// JSON round-trip exactly as the store persists it.
	encoded, err := workspacelayout.EncodeLayout(snapshot.Layout)
	if err != nil {
		t.Fatalf("EncodeLayout: %v", err)
	}
	decoded, err := workspacelayout.DecodeLayout(encoded)
	if err != nil {
		t.Fatalf("DecodeLayout: %v", err)
	}
	if !workspacelayout.HasTile(decoded, "tile-md") {
		t.Fatal("tile lost across layout JSON reload")
	}

	// Re-dock the same tile id at a different anchor: it moves, never duplicates.
	d.handleWorkspaceLayoutDockTile(client, &protocol.WorkspaceLayoutDockTileMessage{
		Cmd:          protocol.CmdWorkspaceLayoutDockTile,
		WorkspaceID:  workspaceID,
		AnchorPaneID: "pane-2",
		Edge:         protocol.WorkspaceLayoutDockEdgeBottom,
		TileID:       "tile-md",
		TileKind:     "markdown",
	})
	expectWorkspaceLayoutActionResultIDs(t, client, protocol.CmdWorkspaceLayoutDockTile, workspaceID, "", "", "tile-md", true)
	moved := d.store.GetWorkspaceLayout(workspaceID)
	if ids := workspacelayout.TileIDs(moved.Layout); len(ids) != 1 {
		t.Fatalf("tile ids after move = %v, want exactly one", ids)
	}
	if params, ok := workspacelayout.TileParamsByID(moved.Layout, "tile-md"); !ok || params != "/tmp/notes.md" {
		t.Fatalf("tile params after move = (%q, %v), want (%q, true)", params, ok, "/tmp/notes.md")
	}

	// Undock removes the tile and collapses its split; the panes are untouched.
	d.handleWorkspaceLayoutUndockTile(client, &protocol.WorkspaceLayoutUndockTileMessage{
		Cmd:         protocol.CmdWorkspaceLayoutUndockTile,
		WorkspaceID: workspaceID,
		TileID:      "tile-md",
	})
	expectWorkspaceLayoutActionResultIDs(t, client, protocol.CmdWorkspaceLayoutUndockTile, workspaceID, "", "", "tile-md", true)
	after := d.store.GetWorkspaceLayout(workspaceID)
	if workspacelayout.HasTile(after.Layout, "tile-md") {
		t.Fatal("tile still present after undock")
	}
	if ids := workspacelayout.PaneIDs(after.Layout); len(ids) != 2 {
		t.Fatalf("pane ids after undock = %v, want the two agent panes intact", ids)
	}

	// Undocking a tile that's gone is a clean no-op failure.
	d.handleWorkspaceLayoutUndockTile(client, &protocol.WorkspaceLayoutUndockTileMessage{
		Cmd:         protocol.CmdWorkspaceLayoutUndockTile,
		WorkspaceID: workspaceID,
		TileID:      "tile-md",
	})
	expectWorkspaceLayoutActionResultIDs(t, client, protocol.CmdWorkspaceLayoutUndockTile, workspaceID, "", "", "tile-md", false)
}

func firstSplitID(node workspacelayout.Node) string {
	if node.Type == "split" {
		return node.SplitID
	}
	for _, child := range node.Children {
		if id := firstSplitID(child); id != "" {
			return id
		}
	}
	return ""
}

func findSplit(node workspacelayout.Node, splitID string) *workspacelayout.Node {
	if node.Type == "split" && node.SplitID == splitID {
		found := node
		return &found
	}
	for i := range node.Children {
		if found := findSplit(node.Children[i], splitID); found != nil {
			return found
		}
	}
	return nil
}

func newWorkspaceProtocolTestClient() *wsClient {
	return &wsClient{
		send:            make(chan outboundMessage, 32),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
}

func expectWorkspaceLayoutActionResult(t *testing.T, client *wsClient, action, workspaceID, paneID string, success bool) {
	t.Helper()
	expectWorkspaceLayoutActionResultIDs(t, client, action, workspaceID, paneID, "", "", success)
}

func expectWorkspaceLayoutActionResultIDs(t *testing.T, client *wsClient, action, workspaceID, paneID, splitID, tileID string, success bool) {
	t.Helper()
	expectWorkspaceLayoutActionResultIDsAndRequestID(t, client, action, workspaceID, paneID, splitID, tileID, "", success)
}

func expectWorkspaceLayoutActionResultIDsAndRequestID(t *testing.T, client *wsClient, action, workspaceID, paneID, splitID, tileID, requestID string, success bool) {
	t.Helper()
	deadline := time.After(1 * time.Second)
	for {
		select {
		case outbound := <-client.send:
			var result protocol.WorkspaceLayoutActionResultMessage
			if err := json.Unmarshal(outbound.payload, &result); err != nil || result.Event != protocol.EventWorkspaceLayoutActionResult {
				continue
			}
			if result.Action != action || result.WorkspaceID != workspaceID {
				continue
			}
			if result.Success != success {
				t.Fatalf("workspace action success = %v, want %v; payload=%s", result.Success, success, string(outbound.payload))
			}
			if got := protocol.Deref(result.PaneID); got != paneID {
				t.Fatalf("workspace action pane_id = %q, want %q; payload=%s", got, paneID, string(outbound.payload))
			}
			if got := protocol.Deref(result.SplitID); got != splitID {
				t.Fatalf("workspace action split_id = %q, want %q; payload=%s", got, splitID, string(outbound.payload))
			}
			if got := protocol.Deref(result.TileID); got != tileID {
				t.Fatalf("workspace action tile_id = %q, want %q; payload=%s", got, tileID, string(outbound.payload))
			}
			if got := protocol.Deref(result.RequestID); got != requestID {
				t.Fatalf("workspace action request_id = %q, want %q; payload=%s", got, requestID, string(outbound.payload))
			}
			return
		case <-deadline:
			t.Fatalf("timed out waiting for workspace action %s", action)
		}
	}
}

func expectSpawnResult(t *testing.T, client *wsClient, sessionID string, success bool) {
	t.Helper()
	deadline := time.After(1 * time.Second)
	for {
		select {
		case outbound := <-client.send:
			var result protocol.SpawnResultMessage
			if err := json.Unmarshal(outbound.payload, &result); err != nil || result.Event != protocol.EventSpawnResult {
				continue
			}
			if result.ID != sessionID {
				continue
			}
			if result.Success != success {
				t.Fatalf("spawn success = %v, want %v; payload=%s", result.Success, success, string(outbound.payload))
			}
			return
		case <-deadline:
			t.Fatalf("timed out waiting for spawn_result for %s", sessionID)
		}
	}
}

func expectCommandError(t *testing.T, client *wsClient, cmd, errorContains string) {
	t.Helper()
	deadline := time.After(1 * time.Second)
	for {
		select {
		case outbound := <-client.send:
			var event protocol.WebSocketEvent
			if err := json.Unmarshal(outbound.payload, &event); err != nil || event.Event != protocol.EventCommandError {
				continue
			}
			if protocol.Deref(event.Cmd) != cmd {
				continue
			}
			if !strings.Contains(protocol.Deref(event.Error), errorContains) {
				t.Fatalf("command_error error = %q, want containing %q; payload=%s", protocol.Deref(event.Error), errorContains, string(outbound.payload))
			}
			return
		case <-deadline:
			t.Fatalf("timed out waiting for command_error for %s", cmd)
		}
	}
}

func expectPaneStatus(t *testing.T, d *Daemon, workspaceID, paneID string, status workspacelayout.PaneStatus, errorContains string) {
	t.Helper()
	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if snapshot == nil {
		t.Fatalf("workspace layout %s not found", workspaceID)
	}
	for _, pane := range snapshot.Panes {
		if pane.PaneID != paneID {
			continue
		}
		if pane.Status != status {
			t.Fatalf("pane %s status = %q, want %q", paneID, pane.Status, status)
		}
		if errorContains != "" && !strings.Contains(pane.Error, errorContains) {
			t.Fatalf("pane %s error = %q, want containing %q", paneID, pane.Error, errorContains)
		}
		return
	}
	t.Fatalf("pane %s not found in workspace %s", paneID, workspaceID)
}

type failingSpawnBackend struct {
	err error
}

func (b *failingSpawnBackend) Spawn(context.Context, ptybackend.SpawnOptions) error {
	return b.err
}
func (b *failingSpawnBackend) Attach(context.Context, string, string) (ptybackend.AttachInfo, ptybackend.Stream, error) {
	return ptybackend.AttachInfo{}, nil, errors.New("attach unsupported")
}
func (b *failingSpawnBackend) Input(context.Context, string, []byte) error { return nil }
func (b *failingSpawnBackend) Resize(context.Context, string, uint16, uint16) error {
	return nil
}
func (b *failingSpawnBackend) Kill(context.Context, string, syscall.Signal) error { return nil }
func (b *failingSpawnBackend) Remove(context.Context, string) error               { return nil }
func (b *failingSpawnBackend) SessionIDs(context.Context) []string                { return nil }
func (b *failingSpawnBackend) Recover(context.Context) (ptybackend.RecoveryReport, error) {
	return ptybackend.RecoveryReport{}, nil
}
func (b *failingSpawnBackend) Shutdown(context.Context) error { return nil }
