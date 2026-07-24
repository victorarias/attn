package daemon

import (
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
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

func TestSpawnPersistsLaunchIntentBeforeWorkerStart(t *testing.T) {
	d, backend, cwd := newSpawnCommitTestDaemon(t)
	msg := spawnCommitMessage("intent-before-worker", cwd)
	backend.onSpawn = func(ptybackend.SpawnOptions) {
		if _, ok := d.store.LaunchIntent(msg.ID); !ok {
			t.Fatal("LaunchIntent() = ok false at worker start, want true")
		}
	}

	if rejection := d.runSpawnPipeline(msg, internalSpawnPolicy{}); rejection != nil {
		t.Fatalf("runSpawnPipeline() rejection = %+v", rejection)
	}
}

func TestSpawnFailureRestoresPriorLaunchIntent(t *testing.T) {
	d, _, cwd := newSpawnCommitTestDaemon(t)
	d.ptyBackend = &failingLaunchIntentBackend{}
	msg := spawnCommitMessage("restore-prior-intent", cwd)
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
	})
	d.store.SetLaunchIntent(msg.ID, store.LaunchIntent{Model: "prior-model"})

	if rejection := d.runSpawnPipeline(msg, internalSpawnPolicy{}); rejection == nil {
		t.Fatal("runSpawnPipeline() rejection = nil, want spawn failure")
	}
	intent, ok := d.store.LaunchIntent(msg.ID)
	if !ok {
		t.Fatal("LaunchIntent() = ok false, want restored prior intent")
	}
	if intent.Model != "prior-model" {
		t.Fatalf("LaunchIntent().Model = %q, want prior-model", intent.Model)
	}
}

func TestSpawnFailureFreshSessionLeavesNoLaunchIntent(t *testing.T) {
	d, _, cwd := newSpawnCommitTestDaemon(t)
	d.ptyBackend = &failingLaunchIntentBackend{}
	msg := spawnCommitMessage("failed-fresh-intent", cwd)

	if rejection := d.runSpawnPipeline(msg, internalSpawnPolicy{}); rejection == nil {
		t.Fatal("runSpawnPipeline() rejection = nil, want spawn failure")
	}
	if _, ok := d.store.LaunchIntent(msg.ID); ok {
		t.Fatal("LaunchIntent() = ok true, want false after failed fresh spawn")
	}
	if session := d.store.Get(msg.ID); session != nil {
		t.Fatalf("stored session = %+v, want nil", session)
	}
}
