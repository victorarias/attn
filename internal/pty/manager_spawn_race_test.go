package pty

import (
	"strings"
	"testing"
	"time"
)

func TestSpawn_ReservesPendingSessionID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real PTY spawn in short mode")
	}

	for range 5 {
		manager := NewManager(DefaultScrollbackSize, nil)
		t.Cleanup(manager.Shutdown)

		opts := SpawnOptions{
			ID:              "pending-spawn",
			CWD:             t.TempDir(),
			Agent:           "probe-pending-spawn",
			ExternalCommand: []string{"/bin/sh", "-c", "sleep 30"},
			LoginShellEnv:   []string{"PATH=/usr/bin:/bin"},
		}
		firstResult := make(chan error, 1)
		go func() {
			firstResult <- manager.Spawn(opts)
		}()

		deadline := time.Now().Add(5 * time.Second)
		for {
			manager.mu.RLock()
			_, pending := manager.pendingSpawns[opts.ID]
			manager.mu.RUnlock()
			if pending {
				break
			}
			select {
			case err := <-firstResult:
				t.Fatalf("first Spawn() completed before its reservation was observed: %v", err)
			default:
			}
			if time.Now().After(deadline) {
				t.Fatal("timed out waiting for first Spawn() to reserve its session ID")
			}
			time.Sleep(time.Millisecond)
		}

		err := manager.Spawn(opts)
		if err == nil || !strings.Contains(err.Error(), "spawn already in progress") {
			t.Fatalf("second Spawn() error = %v, want pending-spawn error", err)
		}
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
}
