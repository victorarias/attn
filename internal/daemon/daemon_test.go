package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/claude-manager/internal/client"
	"github.com/victorarias/claude-manager/internal/github"
	"github.com/victorarias/claude-manager/internal/github/mockserver"
	"github.com/victorarias/claude-manager/internal/protocol"
	"nhooyr.io/websocket"
)

func TestDaemon_RegisterAndQuery(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")

	d := NewForTesting(sockPath)
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

	d := NewForTesting(sockPath)
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

	d := NewForTesting(sockPath)
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

	d := NewForTesting(sockPath)
	go d.Start()
	defer d.Stop()

	time.Sleep(50 * time.Millisecond)

	c := client.New(sockPath)

	// Register multiple sessions (all start as waiting)
	c.Register("1", "one", "/tmp/1", "main:1.%0")
	c.Register("2", "two", "/tmp/2", "main:2.%1")
	c.Register("3", "three", "/tmp/3", "main:3.%2")

	// Update one to working
	c.UpdateState("2", protocol.StateWorking)

	// Query waiting (sessions 1 and 3)
	waiting, _ := c.Query(protocol.StateWaiting)
	if len(waiting) != 2 {
		t.Errorf("got %d waiting, want 2", len(waiting))
	}

	// Query working (session 2)
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

	d := NewForTesting(sockPath)
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

func TestDaemon_ApprovePR_ViaWebSocket(t *testing.T) {
	// Create mock GitHub server
	mockGH := mockserver.New()
	defer mockGH.Close()

	// Add a mock PR
	mockGH.AddPR(mockserver.MockPR{
		Repo:   "test/repo",
		Number: 42,
		Title:  "Test PR",
		Draft:  false,
		Role:   "reviewer",
	})

	// Set up environment
	os.Setenv("GITHUB_TOKEN", "test-token")
	defer os.Unsetenv("GITHUB_TOKEN")

	// Use unique port to avoid conflicts
	wsPort := "19849"
	os.Setenv("CM_WS_PORT", wsPort)
	defer os.Unsetenv("CM_WS_PORT")

	// Create GitHub client pointing to mock server
	ghClient, err := github.NewClient(mockGH.URL)
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	// Create daemon with GitHub client
	// Use /tmp directly to avoid long socket paths
	sockPath := filepath.Join("/tmp", "cm-test-ws.sock")
	os.Remove(sockPath) // Clean up any existing socket
	d := NewWithGitHubClient(sockPath, ghClient)

	// Start daemon in background
	go func() {
		if err := d.Start(); err != nil {
			t.Logf("Daemon start error: %v", err)
		}
	}()
	defer func() {
		d.Stop()
		os.Remove(sockPath)
	}()

	// Wait for daemon and WebSocket server to start (with retries)
	// First wait for the unix socket to be ready
	time.Sleep(200 * time.Millisecond)

	ctx := context.Background()
	wsURL := "ws://127.0.0.1:" + wsPort + "/ws"
	var conn *websocket.Conn
	maxRetries := 20
	for i := 0; i < maxRetries; i++ {
		time.Sleep(100 * time.Millisecond)
		var dialErr error
		conn, _, dialErr = websocket.Dial(ctx, wsURL, nil)
		if dialErr == nil {
			t.Logf("WebSocket connected successfully after %d retries", i+1)
			break
		}
		if i == maxRetries-1 {
			t.Fatalf("WebSocket dial error after %d retries: %v", maxRetries, dialErr)
		}
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Read initial state
	_, initialData, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("Read initial state error: %v", err)
	}
	t.Logf("Initial state: %s", string(initialData))

	// Send approve command
	approveCmd := map[string]interface{}{
		"cmd":    "approve_pr",
		"repo":   "test/repo",
		"number": 42,
	}
	approveJSON, _ := json.Marshal(approveCmd)
	err = conn.Write(ctx, websocket.MessageText, approveJSON)
	if err != nil {
		t.Fatalf("Write approve command error: %v", err)
	}

	// Read response
	_, responseData, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("Read response error: %v", err)
	}
	t.Logf("Response: %s", string(responseData))

	// Parse response
	var response protocol.PRActionResultMessage
	err = json.Unmarshal(responseData, &response)
	if err != nil {
		t.Fatalf("Unmarshal response error: %v", err)
	}

	// Verify response
	if !response.Success {
		t.Errorf("Expected success=true, got success=%v, error=%s", response.Success, response.Error)
	}
	if response.Action != "approve" {
		t.Errorf("Expected action=approve, got action=%s", response.Action)
	}
	if response.Repo != "test/repo" {
		t.Errorf("Expected repo=test/repo, got repo=%s", response.Repo)
	}
	if response.Number != 42 {
		t.Errorf("Expected number=42, got number=%d", response.Number)
	}

	// Verify mock server received the approve request
	if !mockGH.HasApproveRequest("test/repo", 42) {
		t.Error("Mock server did not receive approve request for test/repo#42")
	}
}
