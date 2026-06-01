package daemon

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

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

func expectPanelContent(t *testing.T, client *wsClient, panelID string) protocol.WorkspacePanelContentMessage {
	t.Helper()
	deadline := time.After(1 * time.Second)
	for {
		select {
		case outbound := <-client.send:
			var msg protocol.WorkspacePanelContentMessage
			if err := json.Unmarshal(outbound.payload, &msg); err != nil || msg.Event != protocol.EventWorkspacePanelContent {
				continue
			}
			if msg.PanelID != panelID {
				continue
			}
			return msg
		case <-deadline:
			t.Fatalf("timed out waiting for panel content for %s", panelID)
		}
	}
}

func TestWorkspacePanelContentGetReturnsFile(t *testing.T) {
	d, client, workspaceID := setupMarkdownWorkspace(t)
	file := filepath.Join(t.TempDir(), "README.md")
	if err := os.WriteFile(file, []byte("# Title\n\nBody."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := d.dockPanel(workspaceID, "pane-1", markdownPanelID, string(workspacelayout.PanelKindMarkdown), file, protocol.WorkspaceLayoutDockEdgeRight, nil); err != nil {
		t.Fatalf("dockPanel: %v", err)
	}

	d.handleWorkspacePanelContentGet(client, &protocol.WorkspacePanelContentGetMessage{
		Cmd:         protocol.CmdWorkspacePanelContentGet,
		WorkspaceID: workspaceID,
		PanelID:     markdownPanelID,
	})
	got := expectPanelContent(t, client, markdownPanelID)
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

func TestWorkspacePanelContentGetMissingFileReportsError(t *testing.T) {
	d, client, workspaceID := setupMarkdownWorkspace(t)
	missing := filepath.Join(t.TempDir(), "nope.md")
	if err := d.dockPanel(workspaceID, "pane-1", markdownPanelID, string(workspacelayout.PanelKindMarkdown), missing, protocol.WorkspaceLayoutDockEdgeRight, nil); err != nil {
		t.Fatalf("dockPanel: %v", err)
	}
	d.handleWorkspacePanelContentGet(client, &protocol.WorkspacePanelContentGetMessage{
		Cmd:         protocol.CmdWorkspacePanelContentGet,
		WorkspaceID: workspaceID,
		PanelID:     markdownPanelID,
	})
	got := expectPanelContent(t, client, markdownPanelID)
	if got.Error == nil {
		t.Fatal("expected error for a missing file so the panel can show a clear state")
	}
}

func TestWorkspacePanelContentGetRejectsUnsupportedPanelKind(t *testing.T) {
	d, client, workspaceID := setupMarkdownWorkspace(t)
	file := filepath.Join(t.TempDir(), "private.txt")
	if err := os.WriteFile(file, []byte("must not be returned"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := d.dockPanel(workspaceID, "pane-1", "panel-future", "future", file, protocol.WorkspaceLayoutDockEdgeRight, nil); err != nil {
		t.Fatalf("dockPanel: %v", err)
	}
	d.handleWorkspacePanelContentGet(client, &protocol.WorkspacePanelContentGetMessage{
		Cmd:         protocol.CmdWorkspacePanelContentGet,
		WorkspaceID: workspaceID,
		PanelID:     "panel-future",
	})
	expectCommandError(t, client, protocol.CmdWorkspacePanelContentGet, "unsupported panel kind")
}

func TestCollectChangedMarkdownPanelsDetectsEdits(t *testing.T) {
	d, _, workspaceID := setupMarkdownWorkspace(t)
	file := filepath.Join(t.TempDir(), "live.md")
	if err := os.WriteFile(file, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := d.dockPanel(workspaceID, "pane-1", markdownPanelID, string(workspacelayout.PanelKindMarkdown), file, protocol.WorkspaceLayoutDockEdgeRight, nil); err != nil {
		t.Fatalf("dockPanel: %v", err)
	}

	// First pass: the freshly opened panel is reported as changed.
	if changed := d.collectChangedMarkdownPanels(); len(changed) != 1 || changed[0].path != file {
		t.Fatalf("first pass = %+v, want the new panel", changed)
	}
	// Second pass with no edit: nothing changed.
	if changed := d.collectChangedMarkdownPanels(); len(changed) != 0 {
		t.Fatalf("second pass = %+v, want no changes", changed)
	}
	// Edit the file (different length guarantees a different fingerprint): change detected.
	if err := os.WriteFile(file, []byte("v2-longer"), 0o644); err != nil {
		t.Fatal(err)
	}
	if changed := d.collectChangedMarkdownPanels(); len(changed) != 1 {
		t.Fatalf("after edit = %+v, want the panel reported changed", changed)
	}

	// Undock the panel: it drops out of the watch set entirely.
	d.handleWorkspaceLayoutUndockPanel(newWorkspaceProtocolTestClient(), &protocol.WorkspaceLayoutUndockPanelMessage{
		Cmd:         protocol.CmdWorkspaceLayoutUndockPanel,
		WorkspaceID: workspaceID,
		PanelID:     markdownPanelID,
	})
	if changed := d.collectChangedMarkdownPanels(); len(changed) != 0 {
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
	params, ok := workspacelayout.PanelParamsByID(snapshot.Layout, markdownPanelID)
	if !ok || params != file {
		t.Fatalf("docked panel params = (%q, %v), want %q", params, ok, file)
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
