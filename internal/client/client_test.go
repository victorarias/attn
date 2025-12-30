package client

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
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

func TestClient_NotRunning(t *testing.T) {
	c := New("/nonexistent/socket.sock")
	err := c.Register("id", "label", "/tmp")
	if err == nil {
		t.Error("expected error when daemon not running")
	}
}

func TestClient_SocketPath(t *testing.T) {
	// Set binary name for test
	config.SetBinaryName("attn")

	// Test default socket path
	os.Setenv("HOME", "/home/testuser")
	defer os.Unsetenv("HOME")

	path := DefaultSocketPath()
	expected := "/home/testuser/.attn/attn.sock"
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
