package daemon

import (
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

func newSpawnCommitTestDaemon(t *testing.T) (*Daemon, *fakeSpawnBackend, string) {
	t.Helper()
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	backend := &fakeSpawnBackend{}
	d.ptyBackend = backend
	cwd := t.TempDir()
	addTestWorkspace(d, "workspace", cwd)
	return d, backend, cwd
}

func spawnCommitMessage(id, cwd string) *protocol.SpawnSessionMessage {
	return &protocol.SpawnSessionMessage{
		Cmd:         protocol.CmdSpawnSession,
		ID:          id,
		Cwd:         cwd,
		Agent:       protocol.AgentShellValue,
		WorkspaceID: "workspace",
		Cols:        80,
		Rows:        24,
	}
}

func TestSpawnCommitPreservesStateTransitionDuringSpawn(t *testing.T) {
	d, backend, cwd := newSpawnCommitTestDaemon(t)
	msg := spawnCommitMessage("mid-spawn-state", cwd)
	backend.onSpawn = func(ptybackend.SpawnOptions) {
		if updated := d.store.UpdateState(msg.ID, protocol.StateWorking); !updated {
			t.Fatalf("UpdateState(%q) = false, want true", msg.ID)
		}
	}

	if rejection := d.runSpawnPipeline(msg, internalSpawnPolicy{}); rejection != nil {
		t.Fatalf("runSpawnPipeline() rejection = %+v", rejection)
	}
	if session := d.store.Get(msg.ID); session == nil || session.State != protocol.SessionStateWorking {
		t.Fatalf("stored session = %+v, want state working", session)
	}
}

func TestSpawnCommitPersistsEndpointID(t *testing.T) {
	d, _, cwd := newSpawnCommitTestDaemon(t)
	msg := spawnCommitMessage("endpoint-explicit", cwd)
	msg.EndpointID = protocol.Ptr("ep-1")

	if rejection := d.runSpawnPipeline(msg, internalSpawnPolicy{}); rejection != nil {
		t.Fatalf("runSpawnPipeline() rejection = %+v", rejection)
	}
	if session := d.store.Get(msg.ID); session == nil || protocol.Deref(session.EndpointID) != "ep-1" {
		t.Fatalf("stored session = %+v, want endpoint ep-1", session)
	}
}

func TestSpawnCommitPreservesExistingEndpointID(t *testing.T) {
	d, _, cwd := newSpawnCommitTestDaemon(t)
	msg := spawnCommitMessage("endpoint-respawn", cwd)
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             msg.ID,
		Label:          msg.ID,
		Agent:          protocol.SessionAgentShell,
		Directory:      cwd,
		WorkspaceID:    msg.WorkspaceID,
		State:          protocol.SessionStateIdle,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
		EndpointID:     protocol.Ptr("ep-1"),
	})

	if rejection := d.runSpawnPipeline(msg, internalSpawnPolicy{}); rejection != nil {
		t.Fatalf("runSpawnPipeline() rejection = %+v", rejection)
	}
	if session := d.store.Get(msg.ID); session == nil || protocol.Deref(session.EndpointID) != "ep-1" {
		t.Fatalf("stored session = %+v, want endpoint ep-1", session)
	}
}
