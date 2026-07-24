package daemon

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

type blockingSpawnBackend struct {
	*fakeSpawnBackend

	mu                sync.Mutex
	first             bool
	entered           chan struct{}
	release           chan struct{}
	sessionIDsChecked chan struct{}
}

func newBlockingSpawnBackend() *blockingSpawnBackend {
	return &blockingSpawnBackend{
		fakeSpawnBackend:  &fakeSpawnBackend{},
		entered:           make(chan struct{}),
		release:           make(chan struct{}),
		sessionIDsChecked: make(chan struct{}, 1),
	}
}

func (b *blockingSpawnBackend) Spawn(ctx context.Context, opts ptybackend.SpawnOptions) error {
	b.mu.Lock()
	first := !b.first
	if first {
		b.first = true
		close(b.entered)
	}
	b.mu.Unlock()

	if first {
		<-b.release
	}
	if err := b.fakeSpawnBackend.Spawn(ctx, opts); err != nil {
		return err
	}
	b.fakeSpawnBackend.mu.Lock()
	b.fakeSpawnBackend.sessionIDs = append(b.fakeSpawnBackend.sessionIDs, opts.ID)
	b.fakeSpawnBackend.mu.Unlock()
	return nil
}

func (b *blockingSpawnBackend) SessionIDs(ctx context.Context) []string {
	select {
	case b.sessionIDsChecked <- struct{}{}:
	default:
	}
	return b.fakeSpawnBackend.SessionIDs(ctx)
}

func spawnLockMessage(sessionID, workspaceID, cwd string) *protocol.SpawnSessionMessage {
	return &protocol.SpawnSessionMessage{
		Cmd:         protocol.CmdSpawnSession,
		ID:          sessionID,
		Cwd:         cwd,
		Agent:       protocol.AgentShellValue,
		WorkspaceID: workspaceID,
		Cols:        80,
		Rows:        24,
	}
}

func registerSpawnLockWorkspace(t *testing.T, d *Daemon, workspaceID, cwd string) {
	t.Helper()
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        workspaceID,
		Title:     workspaceID,
		Directory: cwd,
	})
}

func requireSpawnSuccess(t *testing.T, client *wsClient, sessionID string) {
	t.Helper()
	select {
	case outbound := <-client.send:
		var result protocol.SpawnResultMessage
		if err := json.Unmarshal(outbound.payload, &result); err != nil {
			t.Fatalf("unmarshal spawn result: %v", err)
		}
		if !result.Success || result.ID != sessionID {
			t.Fatalf("spawn result = %+v, want successful result for %q", result, sessionID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for spawn result")
	}
}

func TestConcurrentSameSessionSpawnsSpawnOnce(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := newBlockingSpawnBackend()
	d.ptyBackend = backend

	const sessionID = "same-session"
	workspaceID := "workspace-" + sessionID
	cwd := t.TempDir()
	registerSpawnLockWorkspace(t, d, workspaceID, cwd)
	msg := spawnLockMessage(sessionID, workspaceID, cwd)
	firstClient := spawnTestClient()
	secondClient := spawnTestClient()
	firstDone := make(chan struct{})
	secondDone := make(chan struct{})

	go func() {
		d.handleSpawnSession(firstClient, msg)
		close(firstDone)
	}()
	<-backend.entered
	// The first handler's own live-worker check already signaled; drain it so the
	// wait below only fires for the second handler.
	select {
	case <-backend.sessionIDsChecked:
	default:
	}
	go func() {
		d.handleSpawnSession(secondClient, msg)
		close(secondDone)
	}()

	deadline := time.After(time.Second)
waitForSecond:
	for {
		d.spawnLocksMu.Lock()
		lock := d.spawnLocks[sessionID]
		queued := lock != nil && lock.refs == 2
		d.spawnLocksMu.Unlock()
		if queued {
			close(backend.release)
			break waitForSecond
		}
		select {
		case <-backend.sessionIDsChecked:
			close(backend.release)
			break waitForSecond
		case <-deadline:
			t.Fatal("second spawn did not reach the session lock or live-worker check")
		default:
			runtime.Gosched()
		}
	}

	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first spawn did not complete")
	}
	select {
	case <-secondDone:
	case <-time.After(time.Second):
		t.Fatal("second spawn did not complete")
	}

	if got := spawnCount(backend.fakeSpawnBackend); got != 1 {
		t.Fatalf("Spawn calls = %d, want 1", got)
	}
	requireSpawnSuccess(t, firstClient, sessionID)
	requireSpawnSuccess(t, secondClient, sessionID)
}

func TestDifferentSessionSpawnsDoNotSerialize(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	d.ptyBackend = backend

	lockA := d.acquireSpawnLock("session-a")
	defer lockA()
	cwd := t.TempDir()
	registerSpawnLockWorkspace(t, d, "workspace-b", cwd)
	clientB := spawnTestClient()
	doneB := make(chan struct{})
	go func() {
		d.handleSpawnSession(clientB, spawnLockMessage("session-b", "workspace-b", cwd))
		close(doneB)
	}()

	select {
	case <-doneB:
	case <-time.After(time.Second):
		t.Fatal("spawn for session B blocked on session A lock")
	}
	if got := spawnCount(backend); got != 1 {
		t.Fatalf("Spawn calls = %d, want 1", got)
	}
	requireSpawnSuccess(t, clientB, "session-b")
}

func TestSpawnLockRefcountCleanup(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	releaseFirst := d.acquireSpawnLock("session")
	acquiredSecond := make(chan func())
	go func() {
		acquiredSecond <- d.acquireSpawnLock("session")
	}()

	releaseFirst()
	releaseSecond := <-acquiredSecond
	releaseSecond()

	for _, sessionID := range []string{"session", "other"} {
		release := d.acquireSpawnLock(sessionID)
		release()
	}
	d.spawnLocksMu.Lock()
	defer d.spawnLocksMu.Unlock()
	if len(d.spawnLocks) != 0 {
		t.Fatalf("spawnLocks = %#v, want empty", d.spawnLocks)
	}
}
