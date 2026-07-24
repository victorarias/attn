package pty

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSpawn_ReservesPendingSessionID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real PTY spawn in short mode")
	}

	manager := NewManager(DefaultScrollbackSize, nil)
	t.Cleanup(manager.Shutdown)

	opts := SpawnOptions{
		ID:              "pending-spawn",
		CWD:             t.TempDir(),
		Agent:           "probe-pending-spawn",
		ExternalCommand: []string{"/bin/sh", "-c", "sleep 30"},
		LoginShellEnv:   []string{"PATH=/usr/bin:/bin"},
	}
	reserved := make(chan struct{})
	release := make(chan struct{})
	var reserveOnce sync.Once
	manager.testHookAfterSpawnReserve = func() {
		reserveOnce.Do(func() { close(reserved) })
		<-release
	}

	firstResult := make(chan error, 1)
	go func() {
		firstResult <- manager.Spawn(opts)
	}()

	select {
	case <-reserved:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for first Spawn() to reserve its session ID")
	}

	err := manager.Spawn(opts)
	if err == nil || !strings.Contains(err.Error(), "spawn already in progress") {
		t.Fatalf("second Spawn() error = %v, want pending-spawn error", err)
	}

	close(release)
	if err := <-firstResult; err != nil {
		t.Fatalf("first Spawn() error: %v", err)
	}

	manager.mu.RLock()
	_, pending := manager.pendingSpawns[opts.ID]
	manager.mu.RUnlock()
	if pending {
		t.Fatal("pending spawn reservation remained after Spawn() completed")
	}
}
