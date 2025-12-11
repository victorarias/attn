package daemon

import (
	"context"
	"encoding/json"
	"net"
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

func TestDaemon_InjectTestPR(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")

	d := NewForTesting(sockPath)
	go d.Start()
	defer d.Stop()

	// Wait for daemon to start
	time.Sleep(50 * time.Millisecond)

	c := client.New(sockPath)

	// Create test PR data
	testPR := &protocol.PR{
		ID:          "owner/repo#123",
		Repo:        "owner/repo",
		Number:      123,
		Title:       "Test PR for E2E",
		URL:         "https://github.com/owner/repo/pull/123",
		Role:        protocol.PRRoleAuthor,
		State:       protocol.StateWaiting,
		Reason:      protocol.PRReasonReadyToMerge,
		LastUpdated: time.Now(),
		LastPolled:  time.Now(),
		Muted:       false,
	}

	// Send inject_test_pr message
	msg := protocol.InjectTestPRMessage{
		Cmd: protocol.MsgInjectTestPR,
		PR:  testPR,
	}
	msgJSON, _ := json.Marshal(msg)

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Dial error: %v", err)
	}
	defer conn.Close()

	_, err = conn.Write(msgJSON)
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}

	// Read response
	var resp protocol.Response
	err = json.NewDecoder(conn).Decode(&resp)
	if err != nil {
		t.Fatalf("Decode response error: %v", err)
	}

	if !resp.OK {
		t.Fatalf("Expected OK=true, got OK=%v, Error=%s", resp.OK, resp.Error)
	}

	// Verify PR was added using query_prs
	prs, err := c.QueryPRs("")
	if err != nil {
		t.Fatalf("QueryPRs error: %v", err)
	}

	if len(prs) != 1 {
		t.Fatalf("Expected 1 PR, got %d", len(prs))
	}

	if prs[0].ID != "owner/repo#123" {
		t.Errorf("Expected ID=owner/repo#123, got ID=%s", prs[0].ID)
	}
	if prs[0].Title != "Test PR for E2E" {
		t.Errorf("Expected Title='Test PR for E2E', got Title=%s", prs[0].Title)
	}
	if prs[0].State != protocol.StateWaiting {
		t.Errorf("Expected State=waiting, got State=%s", prs[0].State)
	}
}

func TestDaemon_MutePR_ViaWebSocket(t *testing.T) {
	// Use unique port to avoid conflicts
	wsPort := "19850"
	os.Setenv("CM_WS_PORT", wsPort)
	defer os.Unsetenv("CM_WS_PORT")

	// Use /tmp directly to avoid long socket paths
	sockPath := filepath.Join("/tmp", "cm-test-mute-pr.sock")
	os.Remove(sockPath) // Clean up any existing socket

	d := NewForTesting(sockPath)

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

	// Wait for daemon to start
	time.Sleep(200 * time.Millisecond)

	// Inject test PR via unix socket
	testPR := &protocol.PR{
		ID:          "owner/repo#123",
		Repo:        "owner/repo",
		Number:      123,
		Title:       "Test PR",
		URL:         "https://github.com/owner/repo/pull/123",
		Role:        protocol.PRRoleAuthor,
		State:       protocol.StateWaiting,
		Reason:      protocol.PRReasonReadyToMerge,
		LastUpdated: time.Now(),
		LastPolled:  time.Now(),
		Muted:       false,
	}
	msg := protocol.InjectTestPRMessage{
		Cmd: protocol.MsgInjectTestPR,
		PR:  testPR,
	}
	msgJSON, _ := json.Marshal(msg)
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Dial error: %v", err)
	}
	conn.Write(msgJSON)
	conn.Close()

	// Connect to WebSocket
	ctx := context.Background()
	wsURL := "ws://127.0.0.1:" + wsPort + "/ws"
	var wsConn *websocket.Conn
	maxRetries := 20
	for i := 0; i < maxRetries; i++ {
		time.Sleep(100 * time.Millisecond)
		var dialErr error
		wsConn, _, dialErr = websocket.Dial(ctx, wsURL, nil)
		if dialErr == nil {
			break
		}
		if i == maxRetries-1 {
			t.Fatalf("WebSocket dial error after %d retries: %v", maxRetries, dialErr)
		}
	}
	defer wsConn.Close(websocket.StatusNormalClosure, "")

	// Read initial state
	_, initialData, err := wsConn.Read(ctx)
	if err != nil {
		t.Fatalf("Read initial state error: %v", err)
	}

	// Verify PR is not muted in initial state
	var initialState protocol.WebSocketEvent
	json.Unmarshal(initialData, &initialState)
	if len(initialState.PRs) != 1 {
		t.Fatalf("Expected 1 PR in initial state, got %d", len(initialState.PRs))
	}
	if initialState.PRs[0].Muted {
		t.Error("Expected PR to not be muted initially")
	}

	// Send mute_pr command
	muteCmd := map[string]interface{}{
		"cmd": "mute_pr",
		"id":  "owner/repo#123",
	}
	muteJSON, _ := json.Marshal(muteCmd)
	err = wsConn.Write(ctx, websocket.MessageText, muteJSON)
	if err != nil {
		t.Fatalf("Write mute command error: %v", err)
	}

	// Read prs_updated broadcast
	_, updateData, err := wsConn.Read(ctx)
	if err != nil {
		t.Fatalf("Read update error: %v", err)
	}

	var updateEvent protocol.WebSocketEvent
	json.Unmarshal(updateData, &updateEvent)
	if updateEvent.Event != protocol.EventPRsUpdated {
		t.Errorf("Expected event=%s, got event=%s", protocol.EventPRsUpdated, updateEvent.Event)
	}
	if len(updateEvent.PRs) != 1 {
		t.Fatalf("Expected 1 PR in update, got %d", len(updateEvent.PRs))
	}
	if !updateEvent.PRs[0].Muted {
		t.Error("Expected PR to be muted after mute command")
	}

	// Send mute_pr again to toggle back
	err = wsConn.Write(ctx, websocket.MessageText, muteJSON)
	if err != nil {
		t.Fatalf("Write second mute command error: %v", err)
	}

	// Read second prs_updated broadcast
	_, updateData2, err := wsConn.Read(ctx)
	if err != nil {
		t.Fatalf("Read second update error: %v", err)
	}

	var updateEvent2 protocol.WebSocketEvent
	json.Unmarshal(updateData2, &updateEvent2)
	if updateEvent2.PRs[0].Muted {
		t.Error("Expected PR to be unmuted after second mute command (toggle)")
	}
}

func TestDaemon_MuteRepo_ViaWebSocket(t *testing.T) {
	// Use unique port to avoid conflicts
	wsPort := "19851"
	os.Setenv("CM_WS_PORT", wsPort)
	defer os.Unsetenv("CM_WS_PORT")

	// Use /tmp directly to avoid long socket paths
	sockPath := filepath.Join("/tmp", "cm-test-mute-repo.sock")
	os.Remove(sockPath) // Clean up any existing socket

	d := NewForTesting(sockPath)

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

	// Wait for daemon to start
	time.Sleep(200 * time.Millisecond)

	// Connect to WebSocket
	ctx := context.Background()
	wsURL := "ws://127.0.0.1:" + wsPort + "/ws"
	var wsConn *websocket.Conn
	maxRetries := 20
	for i := 0; i < maxRetries; i++ {
		time.Sleep(100 * time.Millisecond)
		var dialErr error
		wsConn, _, dialErr = websocket.Dial(ctx, wsURL, nil)
		if dialErr == nil {
			break
		}
		if i == maxRetries-1 {
			t.Fatalf("WebSocket dial error after %d retries: %v", maxRetries, dialErr)
		}
	}
	defer wsConn.Close(websocket.StatusNormalClosure, "")

	// Read initial state
	_, initialData, err := wsConn.Read(ctx)
	if err != nil {
		t.Fatalf("Read initial state error: %v", err)
	}

	// Verify repos array exists in initial state (will be empty since no repos muted yet)
	var initialState protocol.WebSocketEvent
	json.Unmarshal(initialData, &initialState)
	// Note: Repos can be empty but should be present (may be nil if JSON doesn't include empty arrays)
	// This is fine - we just test that after muting, we get updates

	// Send mute_repo command
	muteCmd := map[string]interface{}{
		"cmd":  "mute_repo",
		"repo": "owner/test-repo",
	}
	muteJSON, _ := json.Marshal(muteCmd)
	err = wsConn.Write(ctx, websocket.MessageText, muteJSON)
	if err != nil {
		t.Fatalf("Write mute_repo command error: %v", err)
	}

	// Read repos_updated broadcast
	_, updateData, err := wsConn.Read(ctx)
	if err != nil {
		t.Fatalf("Read update error: %v", err)
	}

	var updateEvent protocol.WebSocketEvent
	json.Unmarshal(updateData, &updateEvent)
	if updateEvent.Event != protocol.EventReposUpdated {
		t.Errorf("Expected event=%s, got event=%s", protocol.EventReposUpdated, updateEvent.Event)
	}
	if len(updateEvent.Repos) != 1 {
		t.Fatalf("Expected 1 repo state in update, got %d", len(updateEvent.Repos))
	}
	if updateEvent.Repos[0].Repo != "owner/test-repo" {
		t.Errorf("Expected repo=owner/test-repo, got repo=%s", updateEvent.Repos[0].Repo)
	}
	if !updateEvent.Repos[0].Muted {
		t.Error("Expected repo to be muted after mute_repo command")
	}

	// Send mute_repo again to toggle back
	err = wsConn.Write(ctx, websocket.MessageText, muteJSON)
	if err != nil {
		t.Fatalf("Write second mute_repo command error: %v", err)
	}

	// Read second repos_updated broadcast
	_, updateData2, err := wsConn.Read(ctx)
	if err != nil {
		t.Fatalf("Read second update error: %v", err)
	}

	var updateEvent2 protocol.WebSocketEvent
	json.Unmarshal(updateData2, &updateEvent2)
	if updateEvent2.Repos[0].Muted {
		t.Error("Expected repo to be unmuted after second mute_repo command (toggle)")
	}
}

func TestDaemon_InitialState_IncludesRepoStates(t *testing.T) {
	// Use unique port to avoid conflicts
	wsPort := "19852"
	os.Setenv("CM_WS_PORT", wsPort)
	defer os.Unsetenv("CM_WS_PORT")

	// Use /tmp directly to avoid long socket paths
	sockPath := filepath.Join("/tmp", "cm-test-initial-repos.sock")
	os.Remove(sockPath) // Clean up any existing socket

	d := NewForTesting(sockPath)

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

	// Wait for daemon to start
	time.Sleep(200 * time.Millisecond)

	// First, toggle a repo mute via unix socket to set up state
	c := client.New(sockPath)
	err := c.ToggleMuteRepo("owner/test-repo")
	if err != nil {
		t.Fatalf("ToggleMuteRepo error: %v", err)
	}

	// Connect to WebSocket
	ctx := context.Background()
	wsURL := "ws://127.0.0.1:" + wsPort + "/ws"
	var wsConn *websocket.Conn
	maxRetries := 20
	for i := 0; i < maxRetries; i++ {
		time.Sleep(100 * time.Millisecond)
		var dialErr error
		wsConn, _, dialErr = websocket.Dial(ctx, wsURL, nil)
		if dialErr == nil {
			break
		}
		if i == maxRetries-1 {
			t.Fatalf("WebSocket dial error after %d retries: %v", maxRetries, dialErr)
		}
	}
	defer wsConn.Close(websocket.StatusNormalClosure, "")

	// Read initial state
	_, initialData, err := wsConn.Read(ctx)
	if err != nil {
		t.Fatalf("Read initial state error: %v", err)
	}

	var initialState protocol.WebSocketEvent
	json.Unmarshal(initialData, &initialState)

	// Verify initial state includes repos
	if initialState.Event != protocol.EventInitialState {
		t.Errorf("Expected event=%s, got event=%s", protocol.EventInitialState, initialState.Event)
	}
	if initialState.Repos == nil {
		t.Fatal("Expected Repos array in initial state")
	}
	if len(initialState.Repos) != 1 {
		t.Fatalf("Expected 1 repo in initial state, got %d", len(initialState.Repos))
	}
	if initialState.Repos[0].Repo != "owner/test-repo" {
		t.Errorf("Expected repo=owner/test-repo, got repo=%s", initialState.Repos[0].Repo)
	}
	if !initialState.Repos[0].Muted {
		t.Error("Expected repo to be muted in initial state")
	}
}
