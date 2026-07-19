package client

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
)

func TestClient_Register(t *testing.T) {
	// Create temp socket
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")

	// Start mock server
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen error: %v", err)
	}
	defer listener.Close()

	// Handle one connection
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		// Read message
		buf := make([]byte, 4096)
		n, _ := conn.Read(buf)

		// Verify it's a register message
		cmd, msg, err := protocol.ParseMessage(buf[:n])
		if err != nil || cmd != protocol.CmdRegister {
			return
		}
		reg := msg.(*protocol.RegisterMessage)
		if protocol.Deref(reg.Label) != "test-session" {
			return
		}

		// Send response
		resp := protocol.Response{Ok: true}
		json.NewEncoder(conn).Encode(resp)
	}()

	// Test client
	c := New(sockPath)
	err = c.Register("sess-123", "test-session", "/tmp")
	if err != nil {
		t.Fatalf("Register error: %v", err)
	}
}

func TestClient_RegisterWithAgent(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen error: %v", err)
	}
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		buf := make([]byte, 4096)
		n, _ := conn.Read(buf)

		cmd, msg, err := protocol.ParseMessage(buf[:n])
		if err != nil || cmd != protocol.CmdRegister {
			return
		}
		reg := msg.(*protocol.RegisterMessage)
		if protocol.Deref(reg.Agent) != "claude" {
			return
		}

		resp := protocol.Response{Ok: true}
		json.NewEncoder(conn).Encode(resp)
	}()

	c := New(sockPath)
	err = c.RegisterWithAgent("sess-123", "test-session", "/tmp", "claude")
	if err != nil {
		t.Fatalf("RegisterWithAgent error: %v", err)
	}
}

func TestClient_UpdateState(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen error: %v", err)
	}
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		buf := make([]byte, 4096)
		n, _ := conn.Read(buf)

		cmd, msg, err := protocol.ParseMessage(buf[:n])
		if err != nil || cmd != protocol.CmdState {
			return
		}
		state := msg.(*protocol.StateMessage)
		if state.State != protocol.StateWaitingInput {
			return
		}

		resp := protocol.Response{Ok: true}
		json.NewEncoder(conn).Encode(resp)
	}()

	c := New(sockPath)
	err = c.UpdateState("sess-123", protocol.StateWaitingInput)
	if err != nil {
		t.Fatalf("UpdateState error: %v", err)
	}
}

func TestClient_WorkspaceContextMaintenance(t *testing.T) {
	tests := []struct {
		name    string
		wantCmd string
		action  protocol.WorkspaceContextMaintenanceAction
		call    func(*Client) (*protocol.WorkspaceContextMaintenanceResult, error)
	}{
		{
			name:    "compact",
			wantCmd: protocol.CmdWorkspaceContextCompact,
			action:  protocol.WorkspaceContextMaintenanceActionCompact,
			call: func(c *Client) (*protocol.WorkspaceContextMaintenanceResult, error) {
				return c.CompactWorkspaceContext("session-1")
			},
		},
		{
			name:    "rollback",
			wantCmd: protocol.CmdWorkspaceContextRollback,
			action:  protocol.WorkspaceContextMaintenanceActionRollback,
			call: func(c *Client) (*protocol.WorkspaceContextMaintenanceResult, error) {
				return c.RollbackWorkspaceContext("session-1")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir, err := os.MkdirTemp("/tmp", "attn-client-")
			if err != nil {
				t.Fatalf("create short temp dir: %v", err)
			}
			defer os.RemoveAll(tempDir)
			sockPath := filepath.Join(tempDir, "test.sock")
			listener, err := net.Listen("unix", sockPath)
			if err != nil {
				t.Fatalf("listen error: %v", err)
			}
			defer listener.Close()

			requests := make(chan string, 1)
			go func() {
				conn, acceptErr := listener.Accept()
				if acceptErr != nil {
					return
				}
				defer conn.Close()

				var raw json.RawMessage
				if decodeErr := json.NewDecoder(conn).Decode(&raw); decodeErr != nil {
					return
				}
				cmd, msg, parseErr := protocol.ParseMessage(raw)
				if parseErr != nil {
					return
				}
				var sourceSessionID string
				switch parsed := msg.(type) {
				case *protocol.WorkspaceContextCompactMessage:
					sourceSessionID = parsed.SourceSessionID
				case *protocol.WorkspaceContextRollbackMessage:
					sourceSessionID = parsed.SourceSessionID
				}
				requests <- cmd + ":" + sourceSessionID

				_ = json.NewEncoder(conn).Encode(protocol.Response{
					Ok: true,
					WorkspaceContextMaintenanceResult: &protocol.WorkspaceContextMaintenanceResult{
						Action:         tt.action,
						WorkspaceID:    "workspace-1",
						SourceRevision: 1,
						ResultRevision: 2,
						Changed:        true,
					},
				})
			}()

			result, err := tt.call(New(sockPath))
			if err != nil {
				t.Fatalf("%s error: %v", tt.name, err)
			}
			if result.Action != tt.action || result.WorkspaceID != "workspace-1" || !result.Changed {
				t.Fatalf("result = %+v", result)
			}
			if request := <-requests; request != tt.wantCmd+":session-1" {
				t.Fatalf("request = %q", request)
			}
		})
	}
}

func TestClient_Query(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen error: %v", err)
	}
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		buf := make([]byte, 4096)
		conn.Read(buf)

		resp := protocol.Response{
			Ok: true,
			Sessions: []protocol.Session{
				{ID: "1", Label: "one", State: protocol.SessionStateWaitingInput},
				{ID: "2", Label: "two", State: protocol.SessionStateWaitingInput},
			},
		}
		json.NewEncoder(conn).Encode(resp)
	}()

	c := New(sockPath)
	sessions, err := c.Query(protocol.StateWaitingInput)
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}
	if len(sessions) != 2 {
		t.Errorf("got %d sessions, want 2", len(sessions))
	}
}

func TestClient_ListIncludesWorkspaces(t *testing.T) {
	tmpDir, err := os.MkdirTemp("/tmp", "attn-client-")
	if err != nil {
		t.Fatalf("MkdirTemp error: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })
	sockPath := filepath.Join(tmpDir, "test.sock")

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen error: %v", err)
	}
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		buf := make([]byte, 4096)
		conn.Read(buf)

		resp := protocol.Response{
			Ok: true,
			Sessions: []protocol.Session{
				{ID: "1", Label: "one", State: protocol.SessionStateWaitingInput},
			},
			Workspaces: []protocol.Workspace{
				{ID: "workspace-empty", Title: "Empty", Directory: "/repo", Status: protocol.WorkspaceStatusIdle, Pinned: true},
			},
		}
		json.NewEncoder(conn).Encode(resp)
	}()

	c := New(sockPath)
	result, err := c.List("")
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if len(result.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(result.Sessions))
	}
	if len(result.Workspaces) != 1 || result.Workspaces[0].ID != "workspace-empty" || !result.Workspaces[0].Pinned {
		t.Fatalf("workspaces = %+v, want pinned empty workspace", result.Workspaces)
	}
}

func TestClient_Delegate(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen error: %v", err)
	}
	defer listener.Close()

	requests := make(chan *protocol.DelegateMessage, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		var raw json.RawMessage
		if err := json.NewDecoder(conn).Decode(&raw); err != nil {
			return
		}
		cmd, msg, err := protocol.ParseMessage(raw)
		if err != nil || cmd != protocol.CmdDelegate {
			return
		}
		requests <- msg.(*protocol.DelegateMessage)

		json.NewEncoder(conn).Encode(protocol.Response{
			Ok: true,
			DelegationOperation: &protocol.DelegationOperation{
				OperationID: "operation-1", RequestID: "request-1", SessionID: "delegated-session",
				State: protocol.DelegationOperationStateCompleted,
				Result: &protocol.DelegateResult{
					SessionID: "delegated-session", WorkspaceID: "workspace-1",
					Directory: "/tmp/project", Placement: "new_workspace",
				},
			},
		})
	}()

	c := New(sockPath)
	result, err := c.Delegate("source-session", "Investigate the parser", DelegateOptions{
		RequestID:    "request-1",
		Agent:        "codex",
		Model:        "gpt-5.2-codex",
		Effort:       "high",
		Label:        "Parser task",
		Yolo:         true,
		Placement:    "new_workspace",
		CWD:          "/tmp/project",
		WorktreeRepo: "/tmp/repo",
		Worktree:     "feat/parser",
		WorktreePath: "/tmp/repo--feat-parser",
		StartingFrom: "main",
	})
	if err != nil {
		t.Fatalf("Delegate error: %v", err)
	}
	if result.SessionID != "delegated-session" || result.WorkspaceID != "workspace-1" {
		t.Fatalf("Delegate result = %+v", result)
	}

	request := <-requests
	if request.SourceSessionID != "source-session" || request.Brief != "Investigate the parser" {
		t.Fatalf("Delegate request = %+v", request)
	}
	if protocol.Deref(request.Agent) != "codex" || protocol.Deref(request.Label) != "Parser task" {
		t.Fatalf("Delegate request options = %+v", request)
	}
	if protocol.Deref(request.Model) != "gpt-5.2-codex" || protocol.Deref(request.Effort) != "high" {
		t.Fatalf("Delegate request model/effort = %+v", request)
	}
	if !protocol.Deref(request.YoloMode) {
		t.Fatal("Delegate request did not enable yolo mode")
	}
	if protocol.Deref(request.Placement) != "new_workspace" || protocol.Deref(request.Cwd) != "/tmp/project" {
		t.Fatalf("Delegate request placement = %+v", request)
	}
	if request.Worktree == nil ||
		request.Worktree.Branch != "feat/parser" ||
		protocol.Deref(request.Worktree.Repo) != "/tmp/repo" ||
		protocol.Deref(request.Worktree.Path) != "/tmp/repo--feat-parser" ||
		protocol.Deref(request.Worktree.StartingFrom) != "main" {
		t.Fatalf("Delegate request worktree = %+v", request.Worktree)
	}
}

func TestClient_CheckoutWorkspaceContext(t *testing.T) {
	tmpDir, err := os.MkdirTemp("/tmp", "attn-wc-client-")
	if err != nil {
		t.Fatalf("MkdirTemp error: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })
	sockPath := filepath.Join(tmpDir, "test.sock")
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen error: %v", err)
	}
	defer listener.Close()

	requests := make(chan *protocol.WorkspaceContextCheckoutMessage, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var raw json.RawMessage
		if err := json.NewDecoder(conn).Decode(&raw); err != nil {
			return
		}
		cmd, msg, err := protocol.ParseMessage(raw)
		if err != nil || cmd != protocol.CmdWorkspaceContextCheckout {
			return
		}
		requests <- msg.(*protocol.WorkspaceContextCheckoutMessage)
		_ = json.NewEncoder(conn).Encode(protocol.Response{
			Ok: true,
			WorkspaceContextResult: &protocol.WorkspaceContextResult{
				WorkspaceID: "workspace-1",
				SessionID:   "session-1",
				Path:        "/tmp/context.md",
			},
		})
	}()

	result, err := New(sockPath).CheckoutWorkspaceContext("session-1", true)
	if err != nil {
		t.Fatalf("CheckoutWorkspaceContext error: %v", err)
	}
	if result.Path != "/tmp/context.md" {
		t.Fatalf("CheckoutWorkspaceContext result = %+v", result)
	}
	request := <-requests
	if request.SourceSessionID != "session-1" || !protocol.Deref(request.Force) {
		t.Fatalf("CheckoutWorkspaceContext request = %+v", request)
	}
}

func TestClient_NotRunning(t *testing.T) {
	c := New("/nonexistent/socket.sock")
	err := c.Register("id", "label", "/tmp")
	if err == nil {
		t.Error("expected error when daemon not running")
	}
}

func TestClient_ConnectError_IncludesProfileAndSocket(t *testing.T) {
	// No ATTN_PROFILE set → profile defaults to "default".
	os.Unsetenv("ATTN_PROFILE")
	sockPath := filepath.Join(t.TempDir(), "missing.sock")
	c := New(sockPath)
	err := c.Register("id", "label", "/tmp")
	if err == nil {
		t.Fatal("expected error when daemon not running")
	}
	msg := err.Error()
	if !strings.Contains(msg, "profile=default") {
		t.Errorf("error missing profile=default: %q", msg)
	}
	if !strings.Contains(msg, "missing.sock") {
		t.Errorf("error missing socket path: %q", msg)
	}
}

func TestClient_ConnectError_HintsOtherProfileWhenLive(t *testing.T) {
	// Simulate the user being in ATTN_PROFILE=dev but the default daemon is
	// running. We fake a "default" daemon by listening on the default
	// profile's socket path, and point dev at a missing socket.
	//
	// Unix socket paths on macOS are limited to ~104 chars, so we put the
	// fake HOME under /tmp instead of using t.TempDir().
	tmp, err := os.MkdirTemp("/tmp", "attn-client-")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })

	// Intentionally exempt from the toolhome seam: DataDirForProfile is
	// deliberately HOME-based for cross-profile probing (read-only path
	// construction, no writes through the tool-dotfile paths toolhome guards),
	// so HOME is its only lever. See config.go's DataDirForProfile comments.
	t.Setenv("HOME", tmp)            // so SocketPathForProfile("") → $tmp/.attn/attn.sock
	t.Setenv("ATTN_PROFILE", "dev")  // current profile is dev
	t.Setenv("ATTN_SOCKET_PATH", "") // don't let an env override mask the default resolution
	config.ReloadForTesting()

	// Create the "other" (default) socket directory and listen on it.
	defaultDir := filepath.Join(tmp, ".attn")
	if err := os.MkdirAll(defaultDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	defaultSock := filepath.Join(defaultDir, "attn.sock")
	ln, err := net.Listen("unix", defaultSock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	// The dev profile's expected socket doesn't exist.
	devSock := filepath.Join(tmp, ".attn-dev", "attn.sock")
	c := New(devSock)
	err = c.Register("id", "label", "/tmp")
	if err == nil {
		t.Fatal("expected error when dev daemon not running")
	}
	msg := err.Error()
	if !strings.Contains(msg, "profile=dev") {
		t.Errorf("error missing profile=dev: %q", msg)
	}
	if !strings.Contains(msg, "hint:") {
		t.Errorf("error missing cross-profile hint: %q", msg)
	}
	if !strings.Contains(msg, "default daemon is listening") {
		t.Errorf("error should hint about default daemon: %q", msg)
	}
}

func TestClient_SocketPath(t *testing.T) {
	// Set binary name for test
	config.SetBinaryName("attn")

	// DefaultSocketPath() = config.SocketPath(), which composes off
	// ATTN_DATA_DIR; assert that composition directly instead of redirecting
	// HOME (config's own tests pin the HOME-derived default formula without
	// touching the go-test data-dir backstop — see
	// TestDefaultAttnDir_SplitsByProfile in internal/config).
	dataDir := t.TempDir()
	t.Setenv("ATTN_DATA_DIR", dataDir)
	t.Setenv("ATTN_PROFILE", "")
	t.Setenv("ATTN_SOCKET_PATH", "")
	t.Setenv("ATTN_CONFIG_PATH", filepath.Join(t.TempDir(), "missing-config.json"))
	config.ReloadForTesting()

	path := DefaultSocketPath()
	expected := filepath.Join(dataDir, "attn.sock")
	if path != expected {
		t.Errorf("DefaultSocketPath() = %q, want %q", path, expected)
	}
}

func TestClient_Unregister(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen error: %v", err)
	}
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		buf := make([]byte, 4096)
		n, _ := conn.Read(buf)

		cmd, msg, err := protocol.ParseMessage(buf[:n])
		if err != nil || cmd != protocol.CmdUnregister {
			return
		}
		unreg := msg.(*protocol.UnregisterMessage)
		if unreg.ID != "sess-123" {
			return
		}

		resp := protocol.Response{Ok: true}
		json.NewEncoder(conn).Encode(resp)
	}()

	c := New(sockPath)
	err = c.Unregister("sess-123")
	if err != nil {
		t.Fatalf("Unregister error: %v", err)
	}
}

func TestClient_UpdateTodos(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen error: %v", err)
	}
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		buf := make([]byte, 4096)
		n, _ := conn.Read(buf)

		cmd, msg, err := protocol.ParseMessage(buf[:n])
		if err != nil || cmd != protocol.CmdTodos {
			return
		}
		todos := msg.(*protocol.TodosMessage)
		if todos.ID != "sess-123" || len(todos.Todos) != 2 {
			return
		}

		resp := protocol.Response{Ok: true}
		json.NewEncoder(conn).Encode(resp)
	}()

	c := New(sockPath)
	err = c.UpdateTodos("sess-123", []string{"todo1", "todo2"})
	if err != nil {
		t.Fatalf("UpdateTodos error: %v", err)
	}
}

func TestClient_Heartbeat(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen error: %v", err)
	}
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		buf := make([]byte, 4096)
		n, _ := conn.Read(buf)

		cmd, msg, err := protocol.ParseMessage(buf[:n])
		if err != nil || cmd != protocol.CmdHeartbeat {
			return
		}
		hb := msg.(*protocol.HeartbeatMessage)
		if hb.ID != "sess-123" {
			return
		}

		resp := protocol.Response{Ok: true}
		json.NewEncoder(conn).Encode(resp)
	}()

	c := New(sockPath)
	err = c.Heartbeat("sess-123")
	if err != nil {
		t.Fatalf("Heartbeat error: %v", err)
	}
}
