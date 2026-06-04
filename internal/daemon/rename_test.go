package daemon

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

func expectRenameResult(t *testing.T, client *wsClient) protocol.RenameResultMessage {
	t.Helper()
	select {
	case outbound := <-client.send:
		var result protocol.RenameResultMessage
		if err := json.Unmarshal(outbound.payload, &result); err != nil {
			t.Fatalf("decode rename_result: %v", err)
		}
		return result
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for rename_result")
		return protocol.RenameResultMessage{}
	}
}

func newRenameTestClient() *wsClient {
	return &wsClient{
		send:            make(chan outboundMessage, 4),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
}

// A renamed session label is persisted and, crucially, survives a respawn that
// carries a stale label — the stored label is the durable authority.
func TestDaemon_HandleRenameSession_PersistsAndSurvivesRespawn(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	d.ptyBackend = backend

	dir := t.TempDir()
	now := string(protocol.TimestampNow())
	addTestWorkspace(d, "workspace-s1", dir)
	d.store.Add(&protocol.Session{
		ID: "s1", Label: "original", Agent: protocol.SessionAgentClaude,
		Directory: dir, WorkspaceID: "workspace-s1",
		State: protocol.SessionStateIdle, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	d.workspaces.associateSession("s1", "workspace-s1", "original")

	client := newRenameTestClient()
	d.handleRenameSession(client, &protocol.RenameSessionMessage{
		Cmd: protocol.CmdRenameSession, SessionID: "s1", Label: "renamed",
	})

	if res := expectRenameResult(t, client); !res.Success {
		t.Fatalf("rename_result success=false error=%q", protocol.Deref(res.Error))
	}
	if got := d.store.Get("s1"); got == nil || got.Label != "renamed" {
		t.Fatalf("stored label = %+v, want renamed", got)
	}

	// Respawn with a stale label, as a reload from a client with out-of-date
	// local state would. The rename must not be reverted.
	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd: protocol.CmdSpawnSession, ID: "s1", Cwd: dir, Cols: 80, Rows: 24,
		Agent: "claude", WorkspaceID: "workspace-s1", Label: protocol.Ptr("original"),
	})
	if got := d.store.Get("s1"); got == nil || got.Label != "renamed" {
		t.Fatalf("label after stale respawn = %+v, want renamed", got)
	}
	if last, ok := backend.LastSpawn(); !ok || last.Label != "renamed" {
		t.Fatalf("spawn label = %q ok=%v, want renamed", last.Label, ok)
	}
}

func TestDaemon_HandleRenameWorkspace_PersistsTitle(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	dir := t.TempDir()
	addTestWorkspace(d, "workspace-1", dir)

	client := newRenameTestClient()
	d.handleRenameWorkspace(client, &protocol.RenameWorkspaceMessage{
		Cmd: protocol.CmdRenameWorkspace, WorkspaceID: "workspace-1", Title: "Renamed",
	})

	if res := expectRenameResult(t, client); !res.Success {
		t.Fatalf("rename_result success=false error=%q", protocol.Deref(res.Error))
	}
	if got := d.store.GetWorkspace("workspace-1"); got == nil || got.Title != "Renamed" {
		t.Fatalf("stored title = %+v, want Renamed", got)
	}
	if snap, ok := d.workspaces.snapshot("workspace-1"); !ok || snap.Title != "Renamed" {
		t.Fatalf("registry title = %q ok=%v, want Renamed", snap.Title, ok)
	}
}

func TestDaemon_HandleRenameSession_RejectsEmptyName(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	dir := t.TempDir()
	now := string(protocol.TimestampNow())
	addTestWorkspace(d, "workspace-s1", dir)
	d.store.Add(&protocol.Session{
		ID: "s1", Label: "original", Agent: protocol.SessionAgentClaude,
		Directory: dir, WorkspaceID: "workspace-s1",
		State: protocol.SessionStateIdle, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})

	client := newRenameTestClient()
	d.handleRenameSession(client, &protocol.RenameSessionMessage{
		Cmd: protocol.CmdRenameSession, SessionID: "s1", Label: "   ",
	})

	if res := expectRenameResult(t, client); res.Success {
		t.Fatal("rename with blank label should fail")
	}
	if got := d.store.Get("s1"); got == nil || got.Label != "original" {
		t.Fatalf("label after rejected rename = %+v, want original", got)
	}
}
