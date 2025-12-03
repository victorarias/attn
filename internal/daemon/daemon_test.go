package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/claude-manager/internal/client"
	"github.com/victorarias/claude-manager/internal/protocol"
)

func TestDaemon_RegisterAndQuery(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")

	d := New(sockPath)
	go d.Start()
	defer d.Stop()

	// Wait for daemon to start
	time.Sleep(50 * time.Millisecond)

	c := client.New(sockPath)

	// Register a session
	err := c.Register("sess-1", "drumstick", "/home/user/project", "main:1.%42")
	if err != nil {
		t.Fatalf("Register error: %v", err)
	}

	// Query all sessions
	sessions, err := c.Query("")
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	if sessions[0].Label != "drumstick" {
		t.Errorf("Label = %q, want %q", sessions[0].Label, "drumstick")
	}
}

func TestDaemon_StateUpdate(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")

	d := New(sockPath)
	go d.Start()
	defer d.Stop()

	time.Sleep(50 * time.Millisecond)

	c := client.New(sockPath)

	// Register
	c.Register("sess-1", "test", "/tmp", "main:1.%0")

	// Update state
	err := c.UpdateState("sess-1", protocol.StateWaiting)
	if err != nil {
		t.Fatalf("UpdateState error: %v", err)
	}

	// Query waiting
	sessions, err := c.Query(protocol.StateWaiting)
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d waiting sessions, want 1", len(sessions))
	}
}

func TestDaemon_Unregister(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")

	d := New(sockPath)
	go d.Start()
	defer d.Stop()

	time.Sleep(50 * time.Millisecond)

	c := client.New(sockPath)

	c.Register("sess-1", "test", "/tmp", "main:1.%0")
	c.Unregister("sess-1")

	sessions, _ := c.Query("")
	if len(sessions) != 0 {
		t.Errorf("got %d sessions after unregister, want 0", len(sessions))
	}
}

func TestDaemon_MultipleSessions(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")

	d := New(sockPath)
	go d.Start()
	defer d.Stop()

	time.Sleep(50 * time.Millisecond)

	c := client.New(sockPath)

	// Register multiple sessions
	c.Register("1", "one", "/tmp/1", "main:1.%0")
	c.Register("2", "two", "/tmp/2", "main:2.%1")
	c.Register("3", "three", "/tmp/3", "main:3.%2")

	// Update some to waiting
	c.UpdateState("1", protocol.StateWaiting)
	c.UpdateState("3", protocol.StateWaiting)

	// Query waiting
	waiting, _ := c.Query(protocol.StateWaiting)
	if len(waiting) != 2 {
		t.Errorf("got %d waiting, want 2", len(waiting))
	}

	// Query working
	working, _ := c.Query(protocol.StateWorking)
	if len(working) != 1 {
		t.Errorf("got %d working, want 1", len(working))
	}
}

func TestDaemon_SocketCleanup(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")

	// Create stale socket file
	f, _ := os.Create(sockPath)
	f.Close()

	d := New(sockPath)
	go d.Start()
	defer d.Stop()

	time.Sleep(50 * time.Millisecond)

	// Should still work (stale socket removed)
	c := client.New(sockPath)
	err := c.Register("1", "test", "/tmp", "main:1.%0")
	if err != nil {
		t.Fatalf("Register error after stale socket cleanup: %v", err)
	}
}
