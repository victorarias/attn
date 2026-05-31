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

func newWorkspaceProtocolTestClient() *wsClient {
	return &wsClient{
		send:            make(chan outboundMessage, 32),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
}

func expectWorkspaceLayoutActionResult(t *testing.T, client *wsClient, action, workspaceID, paneID string, success bool) {
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
