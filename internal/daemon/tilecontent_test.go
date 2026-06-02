package daemon

import (
	"encoding/json"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/hub"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/workspacelayout"
)

func TestReadMarkdownFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(file, []byte("# Hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	content, err := readMarkdownFile(file)
	if err != nil || content != "# Hello" {
		t.Fatalf("readMarkdownFile = (%q, %v), want the file body and no error", content, err)
	}
	if _, err := readMarkdownFile(filepath.Join(dir, "missing.md")); err == nil {
		t.Fatal("expected error for missing file")
	}
	if _, err := readMarkdownFile(dir); err == nil {
		t.Fatal("expected error for directory")
	}
	if _, err := readMarkdownFile(""); err == nil {
		t.Fatal("expected error for empty path")
	}
	fifo := filepath.Join(dir, "doc.fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readMarkdownFile(fifo); err == nil {
		t.Fatal("expected error for fifo")
	}
	tooLarge := filepath.Join(dir, "too-large.md")
	if err := os.WriteFile(tooLarge, make([]byte, maxMarkdownBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readMarkdownFile(tooLarge); err == nil {
		t.Fatal("expected error for oversized file")
	}
}

// setupMarkdownWorkspace registers a workspace with one session pane and returns
// the daemon, a test client, the workspace id, and the session/pane ids.
func setupMarkdownWorkspace(t *testing.T) (*Daemon, *wsClient, string) {
	t.Helper()
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client := newWorkspaceProtocolTestClient()
	workspaceID := "workspace-md"
	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        workspaceID,
		Title:     "Markdown",
		Directory: t.TempDir(),
	})
	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID: workspaceID,
		PaneID:      protocol.Ptr("pane-1"),
		SessionID:   "session-1",
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, workspaceID, "pane-1", true)
	return d, client, workspaceID
}

func expectTileContent(t *testing.T, client *wsClient, tileID string) protocol.WorkspaceTileContentMessage {
	t.Helper()
	deadline := time.After(1 * time.Second)
	for {
		select {
		case outbound := <-client.send:
			var msg protocol.WorkspaceTileContentMessage
			if err := json.Unmarshal(outbound.payload, &msg); err != nil || msg.Event != protocol.EventWorkspaceTileContent {
				continue
			}
			if msg.TileID != tileID {
				continue
			}
			return msg
		case <-deadline:
			t.Fatalf("timed out waiting for tile content for %s", tileID)
		}
	}
}

func TestWorkspaceTileContentGetReturnsFile(t *testing.T) {
	d, client, workspaceID := setupMarkdownWorkspace(t)
	file := filepath.Join(t.TempDir(), "README.md")
	if err := os.WriteFile(file, []byte("# Title\n\nBody."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := d.dockTile(workspaceID, "pane-1", markdownTileID, string(workspacelayout.TileKindMarkdown), file, protocol.WorkspaceLayoutDockEdgeRight, nil); err != nil {
		t.Fatalf("dockTile: %v", err)
	}

	d.handleWorkspaceTileContentGet(client, &protocol.WorkspaceTileContentGetMessage{
		Cmd:         protocol.CmdWorkspaceTileContentGet,
		WorkspaceID: workspaceID,
		TileID:      markdownTileID,
	})
	got := expectTileContent(t, client, markdownTileID)
	if got.Content != "# Title\n\nBody." {
		t.Fatalf("content = %q, want the file body", got.Content)
	}
	if got.Path != file {
		t.Fatalf("path = %q, want %q", got.Path, file)
	}
	if got.Error != nil {
		t.Fatalf("unexpected error: %v", *got.Error)
	}
}

func TestWorkspaceTileContentGetMissingFileReportsError(t *testing.T) {
	d, client, workspaceID := setupMarkdownWorkspace(t)
	missing := filepath.Join(t.TempDir(), "nope.md")
	if err := d.dockTile(workspaceID, "pane-1", markdownTileID, string(workspacelayout.TileKindMarkdown), missing, protocol.WorkspaceLayoutDockEdgeRight, nil); err != nil {
		t.Fatalf("dockTile: %v", err)
	}
	d.handleWorkspaceTileContentGet(client, &protocol.WorkspaceTileContentGetMessage{
		Cmd:         protocol.CmdWorkspaceTileContentGet,
		WorkspaceID: workspaceID,
		TileID:      markdownTileID,
	})
	got := expectTileContent(t, client, markdownTileID)
	if got.Error == nil {
		t.Fatal("expected error for a missing file so the tile can show a clear state")
	}
}

func TestWorkspaceTileContentGetRejectsUnsupportedTileKind(t *testing.T) {
	d, client, workspaceID := setupMarkdownWorkspace(t)
	file := filepath.Join(t.TempDir(), "private.txt")
	if err := os.WriteFile(file, []byte("must not be returned"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := d.dockTile(workspaceID, "pane-1", "tile-future", "future", file, protocol.WorkspaceLayoutDockEdgeRight, nil); err != nil {
		t.Fatalf("dockTile: %v", err)
	}
	d.handleWorkspaceTileContentGet(client, &protocol.WorkspaceTileContentGetMessage{
		Cmd:         protocol.CmdWorkspaceTileContentGet,
		WorkspaceID: workspaceID,
		TileID:      "tile-future",
	})
	expectCommandError(t, client, protocol.CmdWorkspaceTileContentGet, "unsupported tile kind")
}

func TestWorkspaceTileContentReloadOnlyReachesSubscribedClients(t *testing.T) {
	d, subscribed, workspaceID := setupMarkdownWorkspace(t)
	unrelated := newWorkspaceProtocolTestClient()
	file := filepath.Join(t.TempDir(), "private.md")
	if err := os.WriteFile(file, []byte("# Private"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := d.dockTile(workspaceID, "pane-1", markdownTileID, string(workspacelayout.TileKindMarkdown), file, protocol.WorkspaceLayoutDockEdgeRight, nil); err != nil {
		t.Fatalf("dockTile: %v", err)
	}

	d.wsHub.clients[subscribed] = true
	d.wsHub.clients[unrelated] = true
	d.handleWorkspaceTileContentGet(subscribed, &protocol.WorkspaceTileContentGetMessage{
		Cmd:         protocol.CmdWorkspaceTileContentGet,
		WorkspaceID: workspaceID,
		TileID:      markdownTileID,
	})
	_ = expectTileContent(t, subscribed, markdownTileID)

	d.broadcastTileContentNow(workspaceID, markdownTileID)
	got := expectTileContent(t, subscribed, markdownTileID)
	if got.Content != "# Private" {
		t.Fatalf("content = %q, want the file body", got.Content)
	}
	select {
	case outbound := <-unrelated.send:
		t.Fatalf("unrelated client received private tile content: %s", string(outbound.payload))
	case <-time.After(20 * time.Millisecond):
	}
}

func TestBroadcastTileContentDropsStaleRetargetedRead(t *testing.T) {
	d, client, workspaceID := setupMarkdownWorkspace(t)
	oldFile := filepath.Join(t.TempDir(), "old.md")
	newFile := filepath.Join(t.TempDir(), "new.md")
	if err := d.dockTile(workspaceID, "pane-1", markdownTileID, string(workspacelayout.TileKindMarkdown), oldFile, protocol.WorkspaceLayoutDockEdgeRight, nil); err != nil {
		t.Fatalf("dock old tile: %v", err)
	}
	d.wsHub.clients[client] = true
	client.subscribeTileContent(workspaceID, markdownTileID)
	if err := d.dockTile(workspaceID, "pane-1", markdownTileID, string(workspacelayout.TileKindMarkdown), newFile, protocol.WorkspaceLayoutDockEdgeRight, nil); err != nil {
		t.Fatalf("retarget tile: %v", err)
	}

	d.broadcastTileContent(workspaceID, markdownTileID, string(workspacelayout.TileKindMarkdown), oldFile, "# Old", nil)
	select {
	case outbound := <-client.send:
		t.Fatalf("client received stale tile content: %s", string(outbound.payload))
	case <-time.After(20 * time.Millisecond):
	}

	d.broadcastTileContent(workspaceID, markdownTileID, string(workspacelayout.TileKindMarkdown), newFile, "# New", nil)
	if got := expectTileContent(t, client, markdownTileID); got.Content != "# New" || got.Path != newFile {
		t.Fatalf("tile content = %+v, want current retargeted file", got)
	}
}

func TestDockTileMovePreservesExistingFraction(t *testing.T) {
	d, _, workspaceID := setupMarkdownWorkspace(t)
	fraction := 0.41
	if err := d.dockTile(workspaceID, "pane-1", markdownTileID, string(workspacelayout.TileKindMarkdown), "/tmp/README.md", protocol.WorkspaceLayoutDockEdgeRight, &fraction); err != nil {
		t.Fatalf("dockTile: %v", err)
	}
	if err := d.dockTile(workspaceID, "pane-1", markdownTileID, string(workspacelayout.TileKindMarkdown), "/tmp/README.md", protocol.WorkspaceLayoutDockEdgeBottom, nil); err != nil {
		t.Fatalf("re-dock tile: %v", err)
	}

	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if snapshot == nil {
		t.Fatal("workspace layout missing after tile move")
	}
	got, ok := workspacelayout.TileFractionByID(snapshot.Layout, markdownTileID)
	if !ok || math.Abs(got-fraction) > 1e-9 {
		t.Fatalf("tile fraction after move = (%v, %v), want (%v, true)", got, ok, fraction)
	}
}

func TestCollectChangedMarkdownTilesSkipsUnsubscribedTiles(t *testing.T) {
	d, _, workspaceID := setupMarkdownWorkspace(t)
	file := filepath.Join(t.TempDir(), "idle.md")
	if err := os.WriteFile(file, []byte("# Idle"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := d.dockTile(workspaceID, "pane-1", markdownTileID, string(workspacelayout.TileKindMarkdown), file, protocol.WorkspaceLayoutDockEdgeRight, nil); err != nil {
		t.Fatalf("dockTile: %v", err)
	}

	if changed := d.collectChangedMarkdownTiles(); len(changed) != 0 {
		t.Fatalf("unsubscribed tiles reported as changed: %+v", changed)
	}
}

func TestPendingTileContentSubscriptionsAreBoundedAndExpire(t *testing.T) {
	client := newWorkspaceProtocolTestClient()
	for i := 0; i < maxTileContentSubscriptions; i++ {
		if !client.notePendingTileContent("workspace-md", fmt.Sprintf("tile-%d", i)) {
			t.Fatalf("pending subscription %d unexpectedly rejected", i)
		}
	}
	if client.notePendingTileContent("workspace-md", "tile-overflow") {
		t.Fatal("pending subscription limit was not enforced")
	}

	client.tileContentMu.Lock()
	for key := range client.tileContentPending {
		client.tileContentPending[key] = time.Now().Add(-tileContentPendingTTL)
	}
	client.tileContentMu.Unlock()
	if !client.notePendingTileContent("workspace-md", "tile-after-expiry") {
		t.Fatal("expired pending subscriptions were not pruned")
	}
}

func TestUndockingTilePrunesContentSubscription(t *testing.T) {
	d, client, workspaceID := setupMarkdownWorkspace(t)
	file := filepath.Join(t.TempDir(), "close.md")
	if err := os.WriteFile(file, []byte("# Close"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := d.dockTile(workspaceID, "pane-1", markdownTileID, string(workspacelayout.TileKindMarkdown), file, protocol.WorkspaceLayoutDockEdgeRight, nil); err != nil {
		t.Fatalf("dockTile: %v", err)
	}
	d.wsHub.clients[client] = true
	client.subscribeTileContent(workspaceID, markdownTileID)

	d.handleWorkspaceLayoutUndockTile(client, &protocol.WorkspaceLayoutUndockTileMessage{
		Cmd:         protocol.CmdWorkspaceLayoutUndockTile,
		WorkspaceID: workspaceID,
		TileID:      markdownTileID,
	})
	expectWorkspaceLayoutActionResultIDs(t, client, protocol.CmdWorkspaceLayoutUndockTile, workspaceID, "", "", markdownTileID, true)
	if client.wantsTileContent(workspaceID, markdownTileID) {
		t.Fatal("tile subscription survived undock")
	}
}

func TestCollectChangedMarkdownTilesDetectsEdits(t *testing.T) {
	d, client, workspaceID := setupMarkdownWorkspace(t)
	file := filepath.Join(t.TempDir(), "live.md")
	if err := os.WriteFile(file, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := d.dockTile(workspaceID, "pane-1", markdownTileID, string(workspacelayout.TileKindMarkdown), file, protocol.WorkspaceLayoutDockEdgeRight, nil); err != nil {
		t.Fatalf("dockTile: %v", err)
	}
	d.wsHub.clients[client] = true
	client.subscribeTileContent(workspaceID, markdownTileID)

	// First pass: the freshly opened tile is reported as changed.
	if changed := d.collectChangedMarkdownTiles(); len(changed) != 1 || changed[0].path != file {
		t.Fatalf("first pass = %+v, want the new tile", changed)
	}
	// Second pass with no edit: nothing changed.
	if changed := d.collectChangedMarkdownTiles(); len(changed) != 0 {
		t.Fatalf("second pass = %+v, want no changes", changed)
	}
	// Same-size edit with the previous timestamp restored: the content hash must
	// still detect the change.
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(file, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	d.markdownSeenMu.Lock()
	sig := d.markdownSeen[tileContentSubscriptionKey(workspaceID, markdownTileID)]
	sig.hashCheckedAt = time.Now().Add(-markdownHashPollInterval)
	d.markdownSeen[tileContentSubscriptionKey(workspaceID, markdownTileID)] = sig
	d.markdownSeenMu.Unlock()
	if changed := d.collectChangedMarkdownTiles(); len(changed) != 1 {
		t.Fatalf("after edit = %+v, want the tile reported changed", changed)
	}

	// Undock the tile: it drops out of the watch set entirely.
	d.handleWorkspaceLayoutUndockTile(newWorkspaceProtocolTestClient(), &protocol.WorkspaceLayoutUndockTileMessage{
		Cmd:         protocol.CmdWorkspaceLayoutUndockTile,
		WorkspaceID: workspaceID,
		TileID:      markdownTileID,
	})
	if changed := d.collectChangedMarkdownTiles(); len(changed) != 0 {
		t.Fatalf("after undock = %+v, want empty watch set", changed)
	}
}

func TestOpenMarkdownTargetsSelectedSession(t *testing.T) {
	d, _, workspaceID := setupMarkdownWorkspace(t)
	file := filepath.Join(t.TempDir(), "selected.md")
	if err := os.WriteFile(file, []byte("# Selected"), 0o644); err != nil {
		t.Fatal(err)
	}
	// No explicit session → daemon uses the currently selected session.
	d.setSelectedSession("session-1")

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	go d.handleOpenMarkdown(serverConn, &protocol.OpenMarkdownMessage{
		Cmd:  protocol.CmdOpenMarkdown,
		Path: file,
	})

	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var resp protocol.Response
	if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Ok {
		t.Fatalf("open_markdown failed: %v", protocol.Deref(resp.Error))
	}

	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if snapshot == nil {
		t.Fatal("workspace layout missing after open")
	}
	params, ok := workspacelayout.TileParamsByID(snapshot.Layout, markdownTileID)
	if !ok || params != file {
		t.Fatalf("docked tile params = (%q, %v), want %q", params, ok, file)
	}
}

func TestOpenMarkdownRejectsBareOpenAfterRemoteSessionSelection(t *testing.T) {
	d, client, workspaceID := setupMarkdownWorkspace(t)
	d.setSelectedSession("session-1")
	d.hubManager = hub.NewManager(d.store, nil, nil, nil, nil)
	endpoint, err := d.hubManager.AddEndpoint("remote", "remote.example.test", "")
	if err != nil {
		t.Fatalf("add endpoint: %v", err)
	}
	d.hubManager.ReservePendingSessionRoute(endpoint.ID, "session-remote")
	client.setIdentity("daemon-test", "protocol-"+protocol.ProtocolVersion, []string{protocol.CapabilityWorkspaceSessions})

	d.handleClientMessage(client, []byte(`{"cmd":"session_selected","id":"session-remote"}`))
	if got := d.currentlySelectedSession(); got != "session-remote" {
		t.Fatalf("selected session = %q, want remote session", got)
	}

	file := filepath.Join(t.TempDir(), "remote.md")
	if err := os.WriteFile(file, []byte("# Remote"), 0o644); err != nil {
		t.Fatal(err)
	}
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	go d.handleOpenMarkdown(serverConn, &protocol.OpenMarkdownMessage{
		Cmd:  protocol.CmdOpenMarkdown,
		Path: file,
	})

	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var resp protocol.Response
	if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Ok || !strings.Contains(protocol.Deref(resp.Error), "no workspace found for session session-remote") {
		t.Fatalf("response = %+v, want explicit remote-selection error", resp)
	}
	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if snapshot == nil {
		t.Fatal("local workspace layout missing")
	}
	if leaves := workspacelayout.TileLeaves(snapshot.Layout); len(leaves) != 0 {
		t.Fatalf("local workspace tiles = %+v, want no stale local dock", leaves)
	}
}

func TestOpenMarkdownWithoutSessionFails(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	go d.handleOpenMarkdown(serverConn, &protocol.OpenMarkdownMessage{
		Cmd:  protocol.CmdOpenMarkdown,
		Path: filepath.Join(t.TempDir(), "x.md"),
	})
	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var resp protocol.Response
	if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Ok {
		t.Fatal("expected failure when no session is selected or provided")
	}
}
