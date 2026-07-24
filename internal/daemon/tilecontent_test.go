package daemon

import (
	"encoding/json"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/hub"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
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
	if err := d.dockTile(workspaceID, "pane-1", markdownTileIDForPath(file), string(workspacelayout.TileKindMarkdown), file, "", protocol.WorkspaceLayoutDockEdgeRight, nil); err != nil {
		t.Fatalf("dockTile: %v", err)
	}

	d.handleWorkspaceTileContentGet(client, &protocol.WorkspaceTileContentGetMessage{
		Cmd:         protocol.CmdWorkspaceTileContentGet,
		WorkspaceID: workspaceID,
		TileID:      markdownTileIDForPath(file),
	})
	got := expectTileContent(t, client, markdownTileIDForPath(file))
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
	if err := d.dockTile(workspaceID, "pane-1", markdownTileIDForPath(missing), string(workspacelayout.TileKindMarkdown), missing, "", protocol.WorkspaceLayoutDockEdgeRight, nil); err != nil {
		t.Fatalf("dockTile: %v", err)
	}
	d.handleWorkspaceTileContentGet(client, &protocol.WorkspaceTileContentGetMessage{
		Cmd:         protocol.CmdWorkspaceTileContentGet,
		WorkspaceID: workspaceID,
		TileID:      markdownTileIDForPath(missing),
	})
	got := expectTileContent(t, client, markdownTileIDForPath(missing))
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
	if err := d.dockTile(workspaceID, "pane-1", "tile-future", "future", file, "", protocol.WorkspaceLayoutDockEdgeRight, nil); err != nil {
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
	if err := d.dockTile(workspaceID, "pane-1", markdownTileIDForPath(file), string(workspacelayout.TileKindMarkdown), file, "", protocol.WorkspaceLayoutDockEdgeRight, nil); err != nil {
		t.Fatalf("dockTile: %v", err)
	}

	d.wsHub.clients[subscribed] = true
	d.wsHub.clients[unrelated] = true
	d.handleWorkspaceTileContentGet(subscribed, &protocol.WorkspaceTileContentGetMessage{
		Cmd:         protocol.CmdWorkspaceTileContentGet,
		WorkspaceID: workspaceID,
		TileID:      markdownTileIDForPath(file),
	})
	_ = expectTileContent(t, subscribed, markdownTileIDForPath(file))

	d.broadcastTileContentNow(workspaceID, markdownTileIDForPath(file))
	got := expectTileContent(t, subscribed, markdownTileIDForPath(file))
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
	tileID := markdownTileIDForPath(oldFile)
	if err := d.dockTile(workspaceID, "pane-1", tileID, string(workspacelayout.TileKindMarkdown), oldFile, "", protocol.WorkspaceLayoutDockEdgeRight, nil); err != nil {
		t.Fatalf("dock old tile: %v", err)
	}
	d.wsHub.clients[client] = true
	client.subscribeTileContent(workspaceID, tileID)
	if err := d.dockTile(workspaceID, "pane-1", tileID, string(workspacelayout.TileKindMarkdown), newFile, "", protocol.WorkspaceLayoutDockEdgeRight, nil); err != nil {
		t.Fatalf("retarget tile: %v", err)
	}

	d.broadcastTileContent(workspaceID, tileID, string(workspacelayout.TileKindMarkdown), oldFile, "# Old", nil)
	select {
	case outbound := <-client.send:
		t.Fatalf("client received stale tile content: %s", string(outbound.payload))
	case <-time.After(20 * time.Millisecond):
	}

	d.broadcastTileContent(workspaceID, tileID, string(workspacelayout.TileKindMarkdown), newFile, "# New", nil)
	if got := expectTileContent(t, client, tileID); got.Content != "# New" || got.Path != newFile {
		t.Fatalf("tile content = %+v, want current retargeted file", got)
	}
}

func TestDockTileMovePreservesExistingFraction(t *testing.T) {
	d, _, workspaceID := setupMarkdownWorkspace(t)
	fraction := 0.41
	if err := d.dockTile(workspaceID, "pane-1", markdownTileIDForPath("/tmp/README.md"), string(workspacelayout.TileKindMarkdown), "/tmp/README.md", "", protocol.WorkspaceLayoutDockEdgeRight, &fraction); err != nil {
		t.Fatalf("dockTile: %v", err)
	}
	if err := d.dockTile(workspaceID, "pane-1", markdownTileIDForPath("/tmp/README.md"), string(workspacelayout.TileKindMarkdown), "/tmp/README.md", "", protocol.WorkspaceLayoutDockEdgeBottom, nil); err != nil {
		t.Fatalf("re-dock tile: %v", err)
	}

	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if snapshot == nil {
		t.Fatal("workspace layout missing after tile move")
	}
	got, ok := workspacelayout.TileFractionByID(snapshot.Layout, markdownTileIDForPath("/tmp/README.md"))
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
	if err := d.dockTile(workspaceID, "pane-1", markdownTileIDForPath(file), string(workspacelayout.TileKindMarkdown), file, "", protocol.WorkspaceLayoutDockEdgeRight, nil); err != nil {
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
	if err := d.dockTile(workspaceID, "pane-1", markdownTileIDForPath(file), string(workspacelayout.TileKindMarkdown), file, "", protocol.WorkspaceLayoutDockEdgeRight, nil); err != nil {
		t.Fatalf("dockTile: %v", err)
	}
	d.wsHub.clients[client] = true
	client.subscribeTileContent(workspaceID, markdownTileIDForPath(file))

	d.handleWorkspaceLayoutUndockTile(client, &protocol.WorkspaceLayoutUndockTileMessage{
		Cmd:         protocol.CmdWorkspaceLayoutUndockTile,
		WorkspaceID: workspaceID,
		TileID:      markdownTileIDForPath(file),
	})
	expectWorkspaceLayoutActionResultIDs(t, client, protocol.CmdWorkspaceLayoutUndockTile, workspaceID, "", "", markdownTileIDForPath(file), true)
	if client.wantsTileContent(workspaceID, markdownTileIDForPath(file)) {
		t.Fatal("tile subscription survived undock")
	}
}

func TestCollectChangedMarkdownTilesDetectsEdits(t *testing.T) {
	d, client, workspaceID := setupMarkdownWorkspace(t)
	file := filepath.Join(t.TempDir(), "live.md")
	if err := os.WriteFile(file, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := d.dockTile(workspaceID, "pane-1", markdownTileIDForPath(file), string(workspacelayout.TileKindMarkdown), file, "", protocol.WorkspaceLayoutDockEdgeRight, nil); err != nil {
		t.Fatalf("dockTile: %v", err)
	}
	d.wsHub.clients[client] = true
	client.subscribeTileContent(workspaceID, markdownTileIDForPath(file))

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
	sig := d.markdownSeen[tileContentSubscriptionKey(workspaceID, markdownTileIDForPath(file))]
	sig.hashCheckedAt = time.Now().Add(-markdownHashPollInterval)
	d.markdownSeen[tileContentSubscriptionKey(workspaceID, markdownTileIDForPath(file))] = sig
	d.markdownSeenMu.Unlock()
	if changed := d.collectChangedMarkdownTiles(); len(changed) != 1 {
		t.Fatalf("after edit = %+v, want the tile reported changed", changed)
	}

	// Undock the tile: it drops out of the watch set entirely.
	d.handleWorkspaceLayoutUndockTile(newWorkspaceProtocolTestClient(), &protocol.WorkspaceLayoutUndockTileMessage{
		Cmd:         protocol.CmdWorkspaceLayoutUndockTile,
		WorkspaceID: workspaceID,
		TileID:      markdownTileIDForPath(file),
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
	params, ok := workspacelayout.TileParamsByID(snapshot.Layout, markdownTileIDForPath(file))
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

func expectOpenMarkdownResult(t *testing.T, client *wsClient) protocol.OpenMarkdownResultMessage {
	t.Helper()
	deadline := time.After(1 * time.Second)
	for {
		select {
		case outbound := <-client.send:
			var msg protocol.OpenMarkdownResultMessage
			if err := json.Unmarshal(outbound.payload, &msg); err != nil || msg.Event != protocol.EventOpenMarkdownResult {
				continue
			}
			return msg
		case <-deadline:
			t.Fatal("timed out waiting for open_markdown_result")
		}
	}
}

func tileSessionBinding(t *testing.T, d *Daemon, workspaceID, tileID string) string {
	t.Helper()
	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if snapshot == nil {
		t.Fatal("workspace layout missing")
	}
	sessionID, ok := workspacelayout.TileSessionIDByID(snapshot.Layout, tileID)
	if !ok {
		t.Fatalf("tile %s not found in layout", tileID)
	}
	return sessionID
}

func TestOpenMarkdownDocksOneTilePerPath(t *testing.T) {
	d, _, workspaceID := setupMarkdownWorkspace(t)
	dir := t.TempDir()
	first := filepath.Join(dir, "first.md")
	second := filepath.Join(dir, "second.md")
	for _, file := range []string{first, second} {
		if err := os.WriteFile(file, []byte("# Doc"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := d.openMarkdownTile(file, "session-1"); err != nil {
			t.Fatalf("openMarkdownTile(%s): %v", file, err)
		}
	}

	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if snapshot == nil {
		t.Fatal("workspace layout missing")
	}
	leaves := workspacelayout.TileLeaves(snapshot.Layout)
	if len(leaves) != 2 {
		t.Fatalf("tile leaves = %+v, want one tile per open file", leaves)
	}
	byID := make(map[string]workspacelayout.TileLeaf, len(leaves))
	for _, leaf := range leaves {
		byID[leaf.TileID] = leaf
	}
	for _, file := range []string{first, second} {
		leaf, ok := byID[markdownTileIDForPath(file)]
		if !ok {
			t.Fatalf("no tile docked for %s: %+v", file, leaves)
		}
		if leaf.TileParams != file || leaf.TileSessionID != "session-1" {
			t.Fatalf("leaf for %s = %+v, want path params and session-1 binding", file, leaf)
		}
	}
}

func TestOpenMarkdownNormalizesPathBeforeDerivingTileID(t *testing.T) {
	d, _, workspaceID := setupMarkdownWorkspace(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(file, []byte("# Notes"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Unnormalized spellings of the same file (as a file:// OSC-8 link might
	// carry) land on the cleaned path's tile instead of docking duplicates.
	for _, spelling := range []string{
		file,
		filepath.Join(dir, ".", "notes.md"),
		dir + "//notes.md",
		filepath.Join(dir, "sub", "..", "notes.md"),
	} {
		if _, tileID, err := d.openMarkdownTile(spelling, "session-1"); err != nil {
			t.Fatalf("openMarkdownTile(%q): %v", spelling, err)
		} else if tileID != markdownTileIDForPath(file) {
			t.Fatalf("openMarkdownTile(%q) tile = %q, want %q", spelling, tileID, markdownTileIDForPath(file))
		}
	}
	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if leaves := workspacelayout.TileLeaves(snapshot.Layout); len(leaves) != 1 {
		t.Fatalf("tile leaves = %+v, want one tile across all spellings", leaves)
	}

	if _, _, err := d.openMarkdownTile("relative/notes.md", "session-1"); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("relative path error = %v, want absolute-path rejection", err)
	}
}

func TestOpenMarkdownReusesLegacyFixedIDTile(t *testing.T) {
	d, _, workspaceID := setupMarkdownWorkspace(t)
	file := filepath.Join(t.TempDir(), "legacy.md")
	if err := os.WriteFile(file, []byte("# Legacy"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Layouts persisted before per-path ids hold the fixed id "tile-markdown".
	if err := d.dockTile(workspaceID, "pane-1", "tile-markdown", string(workspacelayout.TileKindMarkdown), file, "session-0", protocol.WorkspaceLayoutDockEdgeRight, nil); err != nil {
		t.Fatalf("dock legacy tile: %v", err)
	}

	gotWorkspace, gotTile, err := d.openMarkdownTile(file, "session-1")
	if err != nil {
		t.Fatalf("openMarkdownTile: %v", err)
	}
	if gotWorkspace != workspaceID || gotTile != "tile-markdown" {
		t.Fatalf("open = (%q, %q), want legacy tile reused in %q", gotWorkspace, gotTile, workspaceID)
	}
	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if leaves := workspacelayout.TileLeaves(snapshot.Layout); len(leaves) != 1 {
		t.Fatalf("tile leaves = %+v, want the legacy tile only (no hashed duplicate)", leaves)
	}
	if got := tileSessionBinding(t, d, workspaceID, "tile-markdown"); got != "session-1" {
		t.Fatalf("legacy tile binding = %q, want rebound to session-1", got)
	}
}

func TestOpenMarkdownConcurrentDistinctPathsKeepAllTiles(t *testing.T) {
	d, _, workspaceID := setupMarkdownWorkspace(t)
	dir := t.TempDir()
	const n = 8
	files := make([]string, n)
	for i := range files {
		files[i] = filepath.Join(dir, fmt.Sprintf("doc-%d.md", i))
		if err := os.WriteFile(files[i], []byte("# Doc"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Concurrent opens of different files must not lose tiles to a
	// read-modify-write race on the layout snapshot.
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i, file := range files {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, errs[i] = d.openMarkdownTile(file, "session-1")
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("openMarkdownTile(%s): %v", files[i], err)
		}
	}

	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	leaves := workspacelayout.TileLeaves(snapshot.Layout)
	if len(leaves) != n {
		t.Fatalf("tile leaves = %d (%+v), want %d — a concurrent dock was lost", len(leaves), leaves, n)
	}
}

func TestOpenMarkdownReusesTileAndRebindsSession(t *testing.T) {
	d, client, workspaceID := setupMarkdownWorkspace(t)
	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID: workspaceID,
		PaneID:      protocol.Ptr("pane-2"),
		SessionID:   "session-2",
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, workspaceID, "pane-2", true)

	file := filepath.Join(t.TempDir(), "shared.md")
	if err := os.WriteFile(file, []byte("# Shared"), 0o644); err != nil {
		t.Fatal(err)
	}
	tileID := markdownTileIDForPath(file)

	if _, gotTile, err := d.openMarkdownTile(file, "session-1"); err != nil || gotTile != tileID {
		t.Fatalf("first open = (%q, %v), want tile %q", gotTile, err, tileID)
	}
	if got := tileSessionBinding(t, d, workspaceID, tileID); got != "session-1" {
		t.Fatalf("initial binding = %q, want session-1", got)
	}
	layoutAfterFirst := d.store.GetWorkspaceLayout(workspaceID).Layout

	// Opening the same file from another session reuses the tile (no re-dock,
	// no duplicate) and rebinds it to the requester.
	if _, gotTile, err := d.openMarkdownTile(file, "session-2"); err != nil || gotTile != tileID {
		t.Fatalf("second open = (%q, %v), want reused tile %q", gotTile, err, tileID)
	}
	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if leaves := workspacelayout.TileLeaves(snapshot.Layout); len(leaves) != 1 {
		t.Fatalf("tile leaves after reuse = %+v, want exactly one", leaves)
	}
	if got := tileSessionBinding(t, d, workspaceID, tileID); got != "session-2" {
		t.Fatalf("binding after reuse = %q, want session-2", got)
	}
	// The tile kept its place: same structure except for the session binding.
	rebased, ok := workspacelayout.UpdateTileSessionID(layoutAfterFirst, tileID, "session-2")
	if !ok {
		t.Fatal("rebase helper failed")
	}
	beforeJSON, err := workspacelayout.EncodeLayout(rebased)
	if err != nil {
		t.Fatal(err)
	}
	afterJSON, err := workspacelayout.EncodeLayout(snapshot.Layout)
	if err != nil {
		t.Fatal(err)
	}
	if beforeJSON != afterJSON {
		t.Fatalf("reuse moved the tile:\nbefore=%s\nafter=%s", beforeJSON, afterJSON)
	}
}

func TestOpenMarkdownWSDocksTileAndReportsResult(t *testing.T) {
	d, client, workspaceID := setupMarkdownWorkspace(t)
	d.wsHub.clients[client] = true
	file := filepath.Join(t.TempDir(), "clicked.md")
	if err := os.WriteFile(file, []byte("# Clicked"), 0o644); err != nil {
		t.Fatal(err)
	}

	d.handleOpenMarkdownWS(client, &protocol.OpenMarkdownMessage{
		Cmd:       protocol.CmdOpenMarkdown,
		Path:      file,
		SessionID: protocol.Ptr("session-1"),
		RequestID: protocol.Ptr("req-1"),
	})

	result := expectOpenMarkdownResult(t, client)
	if !result.Success || result.Error != nil {
		t.Fatalf("result = %+v, want success", result)
	}
	if protocol.Deref(result.RequestID) != "req-1" {
		t.Fatalf("request id = %q, want req-1", protocol.Deref(result.RequestID))
	}
	tileID := markdownTileIDForPath(file)
	if protocol.Deref(result.WorkspaceID) != workspaceID || protocol.Deref(result.TileID) != tileID {
		t.Fatalf("result ids = (%q, %q), want (%q, %q)", protocol.Deref(result.WorkspaceID), protocol.Deref(result.TileID), workspaceID, tileID)
	}
	if got := tileSessionBinding(t, d, workspaceID, tileID); got != "session-1" {
		t.Fatalf("binding = %q, want session-1", got)
	}
}

func TestOpenMarkdownWSUnknownSessionFails(t *testing.T) {
	d, client, _ := setupMarkdownWorkspace(t)
	d.wsHub.clients[client] = true
	d.handleOpenMarkdownWS(client, &protocol.OpenMarkdownMessage{
		Cmd:       protocol.CmdOpenMarkdown,
		Path:      filepath.Join(t.TempDir(), "x.md"),
		SessionID: protocol.Ptr("session-ghost"),
		RequestID: protocol.Ptr("req-2"),
	})
	result := expectOpenMarkdownResult(t, client)
	if result.Success || result.Error == nil {
		t.Fatalf("result = %+v, want failure for unknown session", result)
	}
	if protocol.Deref(result.RequestID) != "req-2" {
		t.Fatalf("request id = %q, want req-2", protocol.Deref(result.RequestID))
	}
}

func TestCollectChangedMarkdownTilesTracksMultipleTiles(t *testing.T) {
	d, client, workspaceID := setupMarkdownWorkspace(t)
	d.wsHub.clients[client] = true
	dir := t.TempDir()
	first := filepath.Join(dir, "first.md")
	second := filepath.Join(dir, "second.md")
	for _, file := range []string{first, second} {
		if err := os.WriteFile(file, []byte("v1"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := d.openMarkdownTile(file, "session-1"); err != nil {
			t.Fatalf("openMarkdownTile(%s): %v", file, err)
		}
		client.subscribeTileContent(workspaceID, markdownTileIDForPath(file))
	}

	// First pass reports both freshly opened tiles.
	changed := d.collectChangedMarkdownTiles()
	if len(changed) != 2 {
		t.Fatalf("first pass = %+v, want both tiles", changed)
	}
	// Quiet pass reports nothing.
	if changed := d.collectChangedMarkdownTiles(); len(changed) != 0 {
		t.Fatalf("quiet pass = %+v, want no changes", changed)
	}
	// Editing one file reports only that tile.
	time.Sleep(5 * time.Millisecond)
	if err := os.WriteFile(second, []byte("v2 with more bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed = d.collectChangedMarkdownTiles()
	if len(changed) != 1 || changed[0].path != second || changed[0].tileID != markdownTileIDForPath(second) {
		t.Fatalf("after edit = %+v, want only the edited tile", changed)
	}
}

// Every route into a markdown tile passes through openMarkdownTile, so recents
// are recorded there rather than by each caller.
func TestOpenMarkdownRecordsRecentFile(t *testing.T) {
	d, _, _ := setupMarkdownWorkspace(t)
	file := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(file, []byte("# Notes"), 0o644); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		if _, _, err := d.openMarkdownTile(file, "session-1"); err != nil {
			t.Fatalf("openMarkdownTile: %v", err)
		}
	}

	files := d.store.GetRecentFiles(10)
	if len(files) != 1 {
		t.Fatalf("recent files = %+v, want one entry", files)
	}
	if files[0].Path != file || files[0].Count != 2 {
		t.Fatalf("recent file = %+v, want %s opened twice", files[0], file)
	}
	if files[0].Source != store.FileActivitySourceOpened {
		t.Fatalf("source = %q, want %q", files[0].Source, store.FileActivitySourceOpened)
	}
}

// A remembered file that has since been deleted must not dock a broken tile,
// and must drop out of recents — the opener never stats its list on summon, so
// this failed open is where a dead entry gets cleaned up.
func TestOpenMarkdownForgetsMissingFile(t *testing.T) {
	d, _, workspaceID := setupMarkdownWorkspace(t)
	file := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(file, []byte("# Notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.openMarkdownTile(file, "session-1"); err != nil {
		t.Fatalf("openMarkdownTile: %v", err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}

	if _, _, err := d.openMarkdownTile(file, "session-1"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("openMarkdownTile(deleted) error = %v, want not-found", err)
	}
	if files := d.store.GetRecentFiles(10); len(files) != 0 {
		t.Fatalf("recent files = %+v, want the deleted file forgotten", files)
	}
	// The already-docked tile stays put: only the recents entry is pruned.
	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if leaves := workspacelayout.TileLeaves(snapshot.Layout); len(leaves) != 1 {
		t.Fatalf("tile leaves = %+v, want the existing tile untouched", leaves)
	}
}
