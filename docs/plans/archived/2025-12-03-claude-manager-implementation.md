# Claude Manager Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build a CLI tool (`cm`) that tracks Claude Code sessions across tmux and provides a status bar indicator and dashboard.

**Architecture:** Unix socket daemon tracks session state, wrapper command starts Claude with hooks that report state changes, status command outputs for tmux, dashboard provides interactive TUI.

**Tech Stack:** Go 1.21+, bubbletea (TUI), standard library (Unix sockets, JSON)

---

## Phase 1: Project Scaffolding

### Task 1.1: Initialize Go Module

**Files:**
- Create: `go.mod`
- Create: `cmd/cm/main.go`

**Step 1: Initialize module**

Run:
```bash
cd /Users/victor.arias/projects/claude-manager
go mod init github.com/victorarias/claude-manager
```
Expected: `go.mod` created

**Step 2: Create minimal main.go**

Create `cmd/cm/main.go`:
```go
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("cm - Claude Manager")
		os.Exit(0)
	}
	fmt.Printf("Unknown command: %s\n", os.Args[1])
	os.Exit(1)
}
```

**Step 3: Verify it builds**

Run:
```bash
go build -o cm ./cmd/cm && ./cm
```
Expected: `cm - Claude Manager`

**Step 4: Commit**

```bash
git add go.mod cmd/
git commit -m "feat: initialize go module and minimal CLI"
```

---

## Phase 2: Protocol Types

### Task 2.1: Define Message Types

**Files:**
- Create: `internal/protocol/types.go`
- Test: `internal/protocol/types_test.go`

**Step 1: Write the failing test**

Create `internal/protocol/types_test.go`:
```go
package protocol

import (
	"encoding/json"
	"testing"
)

func TestRegisterMessage_Marshal(t *testing.T) {
	msg := RegisterMessage{
		Cmd:   "register",
		ID:    "abc123",
		Label: "drumstick",
		Dir:   "/home/user/projects/drumstick",
		Tmux:  "projects:2.%42",
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded RegisterMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.ID != msg.ID {
		t.Errorf("ID mismatch: got %q, want %q", decoded.ID, msg.ID)
	}
	if decoded.Label != msg.Label {
		t.Errorf("Label mismatch: got %q, want %q", decoded.Label, msg.Label)
	}
}

func TestStateMessage_Marshal(t *testing.T) {
	msg := StateMessage{
		Cmd:   "state",
		ID:    "abc123",
		State: "waiting",
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded StateMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.State != "waiting" {
		t.Errorf("State mismatch: got %q, want %q", decoded.State, msg.State)
	}
}

func TestQueryMessage_Marshal(t *testing.T) {
	msg := QueryMessage{
		Cmd:    "query",
		Filter: "waiting",
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded QueryMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.Filter != "waiting" {
		t.Errorf("Filter mismatch: got %q, want %q", decoded.Filter, msg.Filter)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/protocol/... -v`
Expected: FAIL - package doesn't exist

**Step 3: Write minimal implementation**

Create `internal/protocol/types.go`:
```go
package protocol

import "time"

// Commands
const (
	CmdRegister   = "register"
	CmdUnregister = "unregister"
	CmdState      = "state"
	CmdTodos      = "todos"
	CmdQuery      = "query"
	CmdHeartbeat  = "heartbeat"
)

// States
const (
	StateWorking = "working"
	StateWaiting = "waiting"
)

// RegisterMessage registers a new session with the daemon
type RegisterMessage struct {
	Cmd   string `json:"cmd"`
	ID    string `json:"id"`
	Label string `json:"label"`
	Dir   string `json:"dir"`
	Tmux  string `json:"tmux"`
}

// UnregisterMessage removes a session from tracking
type UnregisterMessage struct {
	Cmd string `json:"cmd"`
	ID  string `json:"id"`
}

// StateMessage updates a session's state
type StateMessage struct {
	Cmd   string `json:"cmd"`
	ID    string `json:"id"`
	State string `json:"state"`
}

// TodosMessage updates a session's todo list
type TodosMessage struct {
	Cmd   string   `json:"cmd"`
	ID    string   `json:"id"`
	Todos []string `json:"todos"`
}

// QueryMessage queries sessions from daemon
type QueryMessage struct {
	Cmd    string `json:"cmd"`
	Filter string `json:"filter,omitempty"` // "waiting", "working", or empty for all
}

// HeartbeatMessage keeps session alive
type HeartbeatMessage struct {
	Cmd string `json:"cmd"`
	ID  string `json:"id"`
}

// Session represents a tracked Claude session
type Session struct {
	ID         string    `json:"id"`
	Label      string    `json:"label"`
	Directory  string    `json:"directory"`
	TmuxTarget string    `json:"tmux_target"`
	State      string    `json:"state"`
	StateSince time.Time `json:"state_since"`
	Todos      []string  `json:"todos,omitempty"`
	LastSeen   time.Time `json:"last_seen"`
}

// Response from daemon
type Response struct {
	OK       bool       `json:"ok"`
	Error    string     `json:"error,omitempty"`
	Sessions []*Session `json:"sessions,omitempty"`
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/protocol/... -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/protocol/
git commit -m "feat: add protocol message types"
```

---

### Task 2.2: Add Message Parsing

**Files:**
- Modify: `internal/protocol/types.go`
- Test: `internal/protocol/parse_test.go`

**Step 1: Write the failing test**

Create `internal/protocol/parse_test.go`:
```go
package protocol

import (
	"testing"
)

func TestParseCommand(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantCmd string
		wantErr bool
	}{
		{
			name:    "register message",
			input:   `{"cmd":"register","id":"abc","label":"test","dir":"/tmp","tmux":"main:1.%0"}`,
			wantCmd: CmdRegister,
		},
		{
			name:    "state message",
			input:   `{"cmd":"state","id":"abc","state":"waiting"}`,
			wantCmd: CmdState,
		},
		{
			name:    "query message",
			input:   `{"cmd":"query","filter":"waiting"}`,
			wantCmd: CmdQuery,
		},
		{
			name:    "unregister message",
			input:   `{"cmd":"unregister","id":"abc"}`,
			wantCmd: CmdUnregister,
		},
		{
			name:    "invalid json",
			input:   `not json`,
			wantErr: true,
		},
		{
			name:    "missing cmd",
			input:   `{"id":"abc"}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, _, err := ParseMessage([]byte(tt.input))
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cmd != tt.wantCmd {
				t.Errorf("cmd = %q, want %q", cmd, tt.wantCmd)
			}
		})
	}
}

func TestParseRegister(t *testing.T) {
	input := `{"cmd":"register","id":"abc123","label":"drumstick","dir":"/home/user/project","tmux":"main:1.%42"}`
	cmd, data, err := ParseMessage([]byte(input))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if cmd != CmdRegister {
		t.Fatalf("cmd = %q, want %q", cmd, CmdRegister)
	}

	msg, ok := data.(*RegisterMessage)
	if !ok {
		t.Fatalf("data type = %T, want *RegisterMessage", data)
	}
	if msg.ID != "abc123" {
		t.Errorf("ID = %q, want %q", msg.ID, "abc123")
	}
	if msg.Label != "drumstick" {
		t.Errorf("Label = %q, want %q", msg.Label, "drumstick")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/protocol/... -v`
Expected: FAIL - ParseMessage undefined

**Step 3: Write minimal implementation**

Add to `internal/protocol/types.go`:
```go
import (
	"encoding/json"
	"errors"
	"time"
)

// ParseMessage parses a JSON message and returns the command type and parsed message
func ParseMessage(data []byte) (string, interface{}, error) {
	// First, extract just the command
	var peek struct {
		Cmd string `json:"cmd"`
	}
	if err := json.Unmarshal(data, &peek); err != nil {
		return "", nil, err
	}
	if peek.Cmd == "" {
		return "", nil, errors.New("missing cmd field")
	}

	// Parse based on command type
	switch peek.Cmd {
	case CmdRegister:
		var msg RegisterMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdUnregister:
		var msg UnregisterMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdState:
		var msg StateMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdTodos:
		var msg TodosMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdQuery:
		var msg QueryMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdHeartbeat:
		var msg HeartbeatMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	default:
		return "", nil, errors.New("unknown command: " + peek.Cmd)
	}
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/protocol/... -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/protocol/
git commit -m "feat: add message parsing"
```

---

## Phase 3: Session Store

### Task 3.1: In-Memory Session Store

**Files:**
- Create: `internal/store/store.go`
- Test: `internal/store/store_test.go`

**Step 1: Write the failing test**

Create `internal/store/store_test.go`:
```go
package store

import (
	"testing"
	"time"

	"github.com/victorarias/claude-manager/internal/protocol"
)

func TestStore_AddAndGet(t *testing.T) {
	s := New()

	session := &protocol.Session{
		ID:         "abc123",
		Label:      "drumstick",
		Directory:  "/home/user/project",
		TmuxTarget: "main:1.%42",
		State:      protocol.StateWorking,
		StateSince: time.Now(),
		LastSeen:   time.Now(),
	}

	s.Add(session)

	got := s.Get("abc123")
	if got == nil {
		t.Fatal("expected session, got nil")
	}
	if got.Label != "drumstick" {
		t.Errorf("Label = %q, want %q", got.Label, "drumstick")
	}
}

func TestStore_Remove(t *testing.T) {
	s := New()

	session := &protocol.Session{
		ID:    "abc123",
		Label: "drumstick",
	}
	s.Add(session)

	s.Remove("abc123")

	if got := s.Get("abc123"); got != nil {
		t.Errorf("expected nil after remove, got %+v", got)
	}
}

func TestStore_List(t *testing.T) {
	s := New()

	s.Add(&protocol.Session{ID: "1", Label: "one", State: protocol.StateWorking})
	s.Add(&protocol.Session{ID: "2", Label: "two", State: protocol.StateWaiting})
	s.Add(&protocol.Session{ID: "3", Label: "three", State: protocol.StateWaiting})

	all := s.List("")
	if len(all) != 3 {
		t.Errorf("List() returned %d sessions, want 3", len(all))
	}

	waiting := s.List(protocol.StateWaiting)
	if len(waiting) != 2 {
		t.Errorf("List(waiting) returned %d sessions, want 2", len(waiting))
	}

	working := s.List(protocol.StateWorking)
	if len(working) != 1 {
		t.Errorf("List(working) returned %d sessions, want 1", len(working))
	}
}

func TestStore_UpdateState(t *testing.T) {
	s := New()

	s.Add(&protocol.Session{
		ID:         "abc123",
		State:      protocol.StateWorking,
		StateSince: time.Now().Add(-5 * time.Minute),
	})

	before := s.Get("abc123").StateSince

	s.UpdateState("abc123", protocol.StateWaiting)

	got := s.Get("abc123")
	if got.State != protocol.StateWaiting {
		t.Errorf("State = %q, want %q", got.State, protocol.StateWaiting)
	}
	if !got.StateSince.After(before) {
		t.Error("StateSince should be updated")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/store/... -v`
Expected: FAIL - package doesn't exist

**Step 3: Write minimal implementation**

Create `internal/store/store.go`:
```go
package store

import (
	"sync"
	"time"

	"github.com/victorarias/claude-manager/internal/protocol"
)

// Store manages session state in memory
type Store struct {
	mu       sync.RWMutex
	sessions map[string]*protocol.Session
}

// New creates a new session store
func New() *Store {
	return &Store{
		sessions: make(map[string]*protocol.Session),
	}
}

// Add adds a session to the store
func (s *Store) Add(session *protocol.Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[session.ID] = session
}

// Get retrieves a session by ID
func (s *Store) Get(id string) *protocol.Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessions[id]
}

// Remove removes a session from the store
func (s *Store) Remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}

// List returns sessions, optionally filtered by state
func (s *Store) List(stateFilter string) []*protocol.Session {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*protocol.Session
	for _, session := range s.sessions {
		if stateFilter == "" || session.State == stateFilter {
			result = append(result, session)
		}
	}
	return result
}

// UpdateState updates a session's state
func (s *Store) UpdateState(id, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if session, ok := s.sessions[id]; ok {
		session.State = state
		session.StateSince = time.Now()
	}
}

// UpdateTodos updates a session's todo list
func (s *Store) UpdateTodos(id string, todos []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if session, ok := s.sessions[id]; ok {
		session.Todos = todos
	}
}

// Touch updates a session's last seen time
func (s *Store) Touch(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if session, ok := s.sessions[id]; ok {
		session.LastSeen = time.Now()
	}
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/store/... -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/store/
git commit -m "feat: add in-memory session store"
```

---

## Phase 4: Client Library

### Task 4.1: Client for Daemon Communication

**Files:**
- Create: `internal/client/client.go`
- Test: `internal/client/client_test.go`

**Step 1: Write the failing test**

Create `internal/client/client_test.go`:
```go
package client

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/claude-manager/internal/protocol"
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
		if reg.Label != "test-session" {
			return
		}

		// Send response
		resp := protocol.Response{OK: true}
		json.NewEncoder(conn).Encode(resp)
	}()

	// Test client
	c := New(sockPath)
	err = c.Register("sess-123", "test-session", "/tmp", "main:1.%0")
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
		if state.State != protocol.StateWaiting {
			return
		}

		resp := protocol.Response{OK: true}
		json.NewEncoder(conn).Encode(resp)
	}()

	c := New(sockPath)
	err = c.UpdateState("sess-123", protocol.StateWaiting)
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
			OK: true,
			Sessions: []*protocol.Session{
				{ID: "1", Label: "one", State: protocol.StateWaiting},
				{ID: "2", Label: "two", State: protocol.StateWaiting},
			},
		}
		json.NewEncoder(conn).Encode(resp)
	}()

	c := New(sockPath)
	sessions, err := c.Query(protocol.StateWaiting)
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}
	if len(sessions) != 2 {
		t.Errorf("got %d sessions, want 2", len(sessions))
	}
}

func TestClient_NotRunning(t *testing.T) {
	c := New("/nonexistent/socket.sock")
	err := c.Register("id", "label", "/tmp", "main:1.%0")
	if err == nil {
		t.Error("expected error when daemon not running")
	}
}

func TestClient_SocketPath(t *testing.T) {
	// Test default socket path
	os.Setenv("HOME", "/home/testuser")
	defer os.Unsetenv("HOME")

	path := DefaultSocketPath()
	expected := "/home/testuser/.claude-manager.sock"
	if path != expected {
		t.Errorf("DefaultSocketPath() = %q, want %q", path, expected)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/client/... -v`
Expected: FAIL - package doesn't exist

**Step 3: Write minimal implementation**

Create `internal/client/client.go`:
```go
package client

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/victorarias/claude-manager/internal/protocol"
)

// DefaultSocketPath returns the default socket path
func DefaultSocketPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude-manager.sock")
}

// Client communicates with the daemon
type Client struct {
	socketPath string
}

// New creates a new client
func New(socketPath string) *Client {
	if socketPath == "" {
		socketPath = DefaultSocketPath()
	}
	return &Client{socketPath: socketPath}
}

// send sends a message and receives a response
func (c *Client) send(msg interface{}) (*protocol.Response, error) {
	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return nil, fmt.Errorf("connect to daemon: %w", err)
	}
	defer conn.Close()

	// Send message
	if err := json.NewEncoder(conn).Encode(msg); err != nil {
		return nil, fmt.Errorf("send message: %w", err)
	}

	// Receive response
	var resp protocol.Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, fmt.Errorf("receive response: %w", err)
	}

	if !resp.OK {
		return nil, fmt.Errorf("daemon error: %s", resp.Error)
	}

	return &resp, nil
}

// Register registers a new session
func (c *Client) Register(id, label, dir, tmux string) error {
	msg := protocol.RegisterMessage{
		Cmd:   protocol.CmdRegister,
		ID:    id,
		Label: label,
		Dir:   dir,
		Tmux:  tmux,
	}
	_, err := c.send(msg)
	return err
}

// Unregister removes a session
func (c *Client) Unregister(id string) error {
	msg := protocol.UnregisterMessage{
		Cmd: protocol.CmdUnregister,
		ID:  id,
	}
	_, err := c.send(msg)
	return err
}

// UpdateState updates a session's state
func (c *Client) UpdateState(id, state string) error {
	msg := protocol.StateMessage{
		Cmd:   protocol.CmdState,
		ID:    id,
		State: state,
	}
	_, err := c.send(msg)
	return err
}

// UpdateTodos updates a session's todo list
func (c *Client) UpdateTodos(id string, todos []string) error {
	msg := protocol.TodosMessage{
		Cmd:   protocol.CmdTodos,
		ID:    id,
		Todos: todos,
	}
	_, err := c.send(msg)
	return err
}

// Query returns sessions matching the filter
func (c *Client) Query(filter string) ([]*protocol.Session, error) {
	msg := protocol.QueryMessage{
		Cmd:    protocol.CmdQuery,
		Filter: filter,
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	return resp.Sessions, nil
}

// Heartbeat sends a heartbeat for a session
func (c *Client) Heartbeat(id string) error {
	msg := protocol.HeartbeatMessage{
		Cmd: protocol.CmdHeartbeat,
		ID:  id,
	}
	_, err := c.send(msg)
	return err
}

// IsRunning checks if the daemon is running
func (c *Client) IsRunning() bool {
	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/client/... -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/client/
git commit -m "feat: add client library for daemon communication"
```

---

## Phase 5: Daemon

### Task 5.1: Core Daemon

**Files:**
- Create: `internal/daemon/daemon.go`
- Test: `internal/daemon/daemon_test.go`

**Step 1: Write the failing test**

Create `internal/daemon/daemon_test.go`:
```go
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
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/daemon/... -v`
Expected: FAIL - package doesn't exist

**Step 3: Write minimal implementation**

Create `internal/daemon/daemon.go`:
```go
package daemon

import (
	"encoding/json"
	"log"
	"net"
	"os"
	"time"

	"github.com/victorarias/claude-manager/internal/protocol"
	"github.com/victorarias/claude-manager/internal/store"
)

// Daemon manages Claude sessions
type Daemon struct {
	socketPath string
	store      *store.Store
	listener   net.Listener
	done       chan struct{}
}

// New creates a new daemon
func New(socketPath string) *Daemon {
	return &Daemon{
		socketPath: socketPath,
		store:      store.New(),
		done:       make(chan struct{}),
	}
}

// Start starts the daemon
func (d *Daemon) Start() error {
	// Remove stale socket
	os.Remove(d.socketPath)

	listener, err := net.Listen("unix", d.socketPath)
	if err != nil {
		return err
	}
	d.listener = listener

	for {
		select {
		case <-d.done:
			return nil
		default:
		}

		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-d.done:
				return nil
			default:
				log.Printf("accept error: %v", err)
				continue
			}
		}

		go d.handleConnection(conn)
	}
}

// Stop stops the daemon
func (d *Daemon) Stop() {
	close(d.done)
	if d.listener != nil {
		d.listener.Close()
	}
	os.Remove(d.socketPath)
}

func (d *Daemon) handleConnection(conn net.Conn) {
	defer conn.Close()

	// Read message
	buf := make([]byte, 65536)
	n, err := conn.Read(buf)
	if err != nil {
		return
	}

	cmd, msg, err := protocol.ParseMessage(buf[:n])
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}

	switch cmd {
	case protocol.CmdRegister:
		d.handleRegister(conn, msg.(*protocol.RegisterMessage))
	case protocol.CmdUnregister:
		d.handleUnregister(conn, msg.(*protocol.UnregisterMessage))
	case protocol.CmdState:
		d.handleState(conn, msg.(*protocol.StateMessage))
	case protocol.CmdTodos:
		d.handleTodos(conn, msg.(*protocol.TodosMessage))
	case protocol.CmdQuery:
		d.handleQuery(conn, msg.(*protocol.QueryMessage))
	case protocol.CmdHeartbeat:
		d.handleHeartbeat(conn, msg.(*protocol.HeartbeatMessage))
	default:
		d.sendError(conn, "unknown command")
	}
}

func (d *Daemon) handleRegister(conn net.Conn, msg *protocol.RegisterMessage) {
	session := &protocol.Session{
		ID:         msg.ID,
		Label:      msg.Label,
		Directory:  msg.Dir,
		TmuxTarget: msg.Tmux,
		State:      protocol.StateWorking,
		StateSince: time.Now(),
		LastSeen:   time.Now(),
	}
	d.store.Add(session)
	d.sendOK(conn)
}

func (d *Daemon) handleUnregister(conn net.Conn, msg *protocol.UnregisterMessage) {
	d.store.Remove(msg.ID)
	d.sendOK(conn)
}

func (d *Daemon) handleState(conn net.Conn, msg *protocol.StateMessage) {
	d.store.UpdateState(msg.ID, msg.State)
	d.store.Touch(msg.ID)
	d.sendOK(conn)
}

func (d *Daemon) handleTodos(conn net.Conn, msg *protocol.TodosMessage) {
	d.store.UpdateTodos(msg.ID, msg.Todos)
	d.store.Touch(msg.ID)
	d.sendOK(conn)
}

func (d *Daemon) handleQuery(conn net.Conn, msg *protocol.QueryMessage) {
	sessions := d.store.List(msg.Filter)
	resp := protocol.Response{
		OK:       true,
		Sessions: sessions,
	}
	json.NewEncoder(conn).Encode(resp)
}

func (d *Daemon) handleHeartbeat(conn net.Conn, msg *protocol.HeartbeatMessage) {
	d.store.Touch(msg.ID)
	d.sendOK(conn)
}

func (d *Daemon) sendOK(conn net.Conn) {
	resp := protocol.Response{OK: true}
	json.NewEncoder(conn).Encode(resp)
}

func (d *Daemon) sendError(conn net.Conn, errMsg string) {
	resp := protocol.Response{OK: false, Error: errMsg}
	json.NewEncoder(conn).Encode(resp)
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/daemon/... -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/daemon/
git commit -m "feat: add core daemon with session tracking"
```

---

## Phase 6: Hooks Generator

### Task 6.1: Generate Claude Hooks

**Files:**
- Create: `internal/hooks/hooks.go`
- Test: `internal/hooks/hooks_test.go`

**Step 1: Write the failing test**

Create `internal/hooks/hooks_test.go`:
```go
package hooks

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGenerateHooks(t *testing.T) {
	sessionID := "abc123"
	socketPath := "/home/user/.claude-manager.sock"

	hooks := Generate(sessionID, socketPath)

	// Verify it's valid JSON
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(hooks), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Check hooks exist
	hooksArray, ok := parsed["hooks"].([]interface{})
	if !ok {
		t.Fatal("hooks field not found or not array")
	}

	// Should have multiple hooks
	if len(hooksArray) < 3 {
		t.Errorf("expected at least 3 hooks, got %d", len(hooksArray))
	}

	// Verify hook structure
	for _, h := range hooksArray {
		hook := h.(map[string]interface{})
		if _, ok := hook["matcher"]; !ok {
			t.Error("hook missing matcher")
		}
		if _, ok := hook["hooks"]; !ok {
			t.Error("hook missing hooks array")
		}
	}
}

func TestGenerateHooks_ContainsSessionID(t *testing.T) {
	sessionID := "unique-session-id-12345"
	socketPath := "/tmp/test.sock"

	hooks := Generate(sessionID, socketPath)

	if !strings.Contains(hooks, sessionID) {
		t.Error("generated hooks should contain session ID")
	}
}

func TestGenerateHooks_ContainsSocketPath(t *testing.T) {
	sessionID := "test"
	socketPath := "/custom/path/to/socket.sock"

	hooks := Generate(sessionID, socketPath)

	if !strings.Contains(hooks, socketPath) {
		t.Error("generated hooks should contain socket path")
	}
}

func TestGenerateHooks_HasStopHook(t *testing.T) {
	hooks := Generate("test", "/tmp/test.sock")

	if !strings.Contains(hooks, "Stop") {
		t.Error("hooks should include Stop event for waiting state")
	}
}

func TestGenerateHooks_HasUserPromptSubmitHook(t *testing.T) {
	hooks := Generate("test", "/tmp/test.sock")

	if !strings.Contains(hooks, "UserPromptSubmit") {
		t.Error("hooks should include UserPromptSubmit event for working state")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/hooks/... -v`
Expected: FAIL - package doesn't exist

**Step 3: Write minimal implementation**

Create `internal/hooks/hooks.go`:
```go
package hooks

import (
	"encoding/json"
	"fmt"
)

// HookConfig represents a Claude Code hooks configuration
type HookConfig struct {
	Hooks []HookEntry `json:"hooks"`
}

// HookEntry is a single hook configuration
type HookEntry struct {
	Matcher EventMatcher `json:"matcher"`
	Hooks   []Hook       `json:"hooks"`
}

// EventMatcher matches events
type EventMatcher struct {
	Event string `json:"event"`
	Tool  string `json:"tool,omitempty"`
}

// Hook is an individual hook action
type Hook struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// Generate generates hooks configuration for a session
func Generate(sessionID, socketPath string) string {
	config := HookConfig{
		Hooks: []HookEntry{
			{
				Matcher: EventMatcher{Event: "Stop"},
				Hooks: []Hook{
					{
						Type:    "command",
						Command: fmt.Sprintf(`echo '{"cmd":"state","id":"%s","state":"waiting"}' | nc -U %s`, sessionID, socketPath),
					},
				},
			},
			{
				Matcher: EventMatcher{Event: "UserPromptSubmit"},
				Hooks: []Hook{
					{
						Type:    "command",
						Command: fmt.Sprintf(`echo '{"cmd":"state","id":"%s","state":"working"}' | nc -U %s`, sessionID, socketPath),
					},
				},
			},
			{
				Matcher: EventMatcher{Event: "PostToolUse", Tool: "TodoWrite"},
				Hooks: []Hook{
					{
						Type:    "command",
						Command: fmt.Sprintf(`cm _hook-todo "%s"`, sessionID),
					},
				},
			},
		},
	}

	data, _ := json.MarshalIndent(config, "", "  ")
	return string(data)
}

// GenerateUnregisterCommand generates the command to unregister a session
func GenerateUnregisterCommand(sessionID, socketPath string) string {
	return fmt.Sprintf(`echo '{"cmd":"unregister","id":"%s"}' | nc -U %s`, sessionID, socketPath)
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/hooks/... -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/hooks/
git commit -m "feat: add hooks generator for Claude Code"
```

---

## Phase 7: Status Command

### Task 7.1: Status Output for tmux

**Files:**
- Create: `internal/status/status.go`
- Test: `internal/status/status_test.go`

**Step 1: Write the failing test**

Create `internal/status/status_test.go`:
```go
package status

import (
	"testing"
	"time"

	"github.com/victorarias/claude-manager/internal/protocol"
)

func TestFormat_NoSessions(t *testing.T) {
	result := Format(nil)
	if result != "" {
		t.Errorf("expected empty string for no sessions, got %q", result)
	}
}

func TestFormat_NoWaiting(t *testing.T) {
	sessions := []*protocol.Session{
		{Label: "one", State: protocol.StateWorking},
		{Label: "two", State: protocol.StateWorking},
	}
	result := Format(sessions)
	if result != "" {
		t.Errorf("expected empty string for no waiting, got %q", result)
	}
}

func TestFormat_OneWaiting(t *testing.T) {
	sessions := []*protocol.Session{
		{Label: "drumstick", State: protocol.StateWaiting},
	}
	result := Format(sessions)
	expected := "1 waiting: drumstick"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestFormat_TwoWaiting(t *testing.T) {
	sessions := []*protocol.Session{
		{Label: "drumstick", State: protocol.StateWaiting, StateSince: time.Now()},
		{Label: "hurdy", State: protocol.StateWaiting, StateSince: time.Now().Add(-time.Minute)},
	}
	result := Format(sessions)
	expected := "2 waiting: hurdy, drumstick"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestFormat_ManyWaiting_Truncates(t *testing.T) {
	sessions := []*protocol.Session{
		{Label: "one", State: protocol.StateWaiting, StateSince: time.Now()},
		{Label: "two", State: protocol.StateWaiting, StateSince: time.Now().Add(-time.Second)},
		{Label: "three", State: protocol.StateWaiting, StateSince: time.Now().Add(-2 * time.Second)},
		{Label: "four", State: protocol.StateWaiting, StateSince: time.Now().Add(-3 * time.Second)},
		{Label: "five", State: protocol.StateWaiting, StateSince: time.Now().Add(-4 * time.Second)},
	}
	result := Format(sessions)
	// Should truncate and show count
	if result != "5 waiting: five, four..." {
		t.Errorf("got %q, want truncated format", result)
	}
}

func TestFormat_MixedStates(t *testing.T) {
	sessions := []*protocol.Session{
		{Label: "working1", State: protocol.StateWorking},
		{Label: "waiting1", State: protocol.StateWaiting},
		{Label: "working2", State: protocol.StateWorking},
	}
	result := Format(sessions)
	expected := "1 waiting: waiting1"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestFormat_SortsOldestFirst(t *testing.T) {
	now := time.Now()
	sessions := []*protocol.Session{
		{Label: "newest", State: protocol.StateWaiting, StateSince: now},
		{Label: "oldest", State: protocol.StateWaiting, StateSince: now.Add(-10 * time.Minute)},
		{Label: "middle", State: protocol.StateWaiting, StateSince: now.Add(-5 * time.Minute)},
	}
	result := Format(sessions)
	expected := "3 waiting: oldest, middle..."
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/status/... -v`
Expected: FAIL - package doesn't exist

**Step 3: Write minimal implementation**

Create `internal/status/status.go`:
```go
package status

import (
	"fmt"
	"sort"
	"strings"

	"github.com/victorarias/claude-manager/internal/protocol"
)

const maxLabels = 2

// Format formats sessions for tmux status bar
func Format(sessions []*protocol.Session) string {
	// Filter to waiting only
	var waiting []*protocol.Session
	for _, s := range sessions {
		if s.State == protocol.StateWaiting {
			waiting = append(waiting, s)
		}
	}

	if len(waiting) == 0 {
		return ""
	}

	// Sort by StateSince (oldest first)
	sort.Slice(waiting, func(i, j int) bool {
		return waiting[i].StateSince.Before(waiting[j].StateSince)
	})

	// Format labels
	var labels []string
	for i, s := range waiting {
		if i >= maxLabels {
			break
		}
		labels = append(labels, s.Label)
	}

	labelStr := strings.Join(labels, ", ")
	if len(waiting) > maxLabels {
		labelStr += "..."
	}

	return fmt.Sprintf("%d waiting: %s", len(waiting), labelStr)
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/status/... -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/status/
git commit -m "feat: add status formatter for tmux"
```

---

## Phase 8: Dashboard TUI

### Task 8.1: Add Bubbletea Dependency

**Step 1: Add dependency**

Run:
```bash
cd /Users/victor.arias/projects/claude-manager
go get github.com/charmbracelet/bubbletea
go get github.com/charmbracelet/lipgloss
```

**Step 2: Verify dependency added**

Run: `grep bubbletea go.mod`
Expected: Contains bubbletea

**Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: add bubbletea and lipgloss dependencies"
```

---

### Task 8.2: Dashboard Model

**Files:**
- Create: `internal/dashboard/model.go`
- Test: `internal/dashboard/model_test.go`

**Step 1: Write the failing test**

Create `internal/dashboard/model_test.go`:
```go
package dashboard

import (
	"testing"
	"time"

	"github.com/victorarias/claude-manager/internal/protocol"
)

func TestModel_Init(t *testing.T) {
	m := NewModel(nil)
	if m.cursor != 0 {
		t.Errorf("initial cursor = %d, want 0", m.cursor)
	}
}

func TestModel_MoveCursor(t *testing.T) {
	m := NewModel(nil)
	m.sessions = []*protocol.Session{
		{ID: "1"},
		{ID: "2"},
		{ID: "3"},
	}

	// Move down
	m.moveCursor(1)
	if m.cursor != 1 {
		t.Errorf("cursor after down = %d, want 1", m.cursor)
	}

	// Move down again
	m.moveCursor(1)
	if m.cursor != 2 {
		t.Errorf("cursor after second down = %d, want 2", m.cursor)
	}

	// Move down at bottom (should stay)
	m.moveCursor(1)
	if m.cursor != 2 {
		t.Errorf("cursor at bottom = %d, want 2", m.cursor)
	}

	// Move up
	m.moveCursor(-1)
	if m.cursor != 1 {
		t.Errorf("cursor after up = %d, want 1", m.cursor)
	}
}

func TestModel_SelectedSession(t *testing.T) {
	m := NewModel(nil)
	m.sessions = []*protocol.Session{
		{ID: "1", Label: "one"},
		{ID: "2", Label: "two"},
	}

	m.cursor = 1
	selected := m.SelectedSession()
	if selected == nil {
		t.Fatal("expected selected session")
	}
	if selected.Label != "two" {
		t.Errorf("selected label = %q, want %q", selected.Label, "two")
	}
}

func TestModel_FormatDuration(t *testing.T) {
	tests := []struct {
		duration time.Duration
		want     string
	}{
		{30 * time.Second, "0m 30s"},
		{90 * time.Second, "1m 30s"},
		{5*time.Minute + 2*time.Second, "5m 02s"},
		{65 * time.Minute, "65m 00s"},
	}

	for _, tt := range tests {
		got := formatDuration(tt.duration)
		if got != tt.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tt.duration, got, tt.want)
		}
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/dashboard/... -v`
Expected: FAIL - package doesn't exist

**Step 3: Write minimal implementation**

Create `internal/dashboard/model.go`:
```go
package dashboard

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/victorarias/claude-manager/internal/client"
	"github.com/victorarias/claude-manager/internal/protocol"
)

// Model is the bubbletea model for the dashboard
type Model struct {
	client   *client.Client
	sessions []*protocol.Session
	cursor   int
	err      error
}

// NewModel creates a new dashboard model
func NewModel(c *client.Client) *Model {
	return &Model{
		client: c,
	}
}

// Init initializes the model
func (m *Model) Init() tea.Cmd {
	return m.refresh
}

// refresh fetches sessions from daemon
func (m *Model) refresh() tea.Msg {
	if m.client == nil {
		return sessionsMsg{sessions: nil}
	}
	sessions, err := m.client.Query("")
	if err != nil {
		return errMsg{err: err}
	}
	return sessionsMsg{sessions: sessions}
}

type sessionsMsg struct {
	sessions []*protocol.Session
}

type errMsg struct {
	err error
}

type tickMsg struct{}

// Update handles messages
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			m.moveCursor(-1)
		case "down", "j":
			m.moveCursor(1)
		case "r":
			return m, m.refresh
		case "enter":
			if s := m.SelectedSession(); s != nil {
				return m, m.jumpToPane(s.TmuxTarget)
			}
		}
	case sessionsMsg:
		m.sessions = msg.sessions
		m.err = nil
		// Ensure cursor is valid
		if m.cursor >= len(m.sessions) && len(m.sessions) > 0 {
			m.cursor = len(m.sessions) - 1
		}
	case errMsg:
		m.err = msg.err
	case tickMsg:
		return m, m.refresh
	}
	return m, nil
}

func (m *Model) moveCursor(delta int) {
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.sessions) && len(m.sessions) > 0 {
		m.cursor = len(m.sessions) - 1
	}
}

// SelectedSession returns the currently selected session
func (m *Model) SelectedSession() *protocol.Session {
	if m.cursor >= 0 && m.cursor < len(m.sessions) {
		return m.sessions[m.cursor]
	}
	return nil
}

func (m *Model) jumpToPane(tmuxTarget string) tea.Cmd {
	return tea.ExecProcess(
		tea.ExecCommand("tmux", "switch-client", "-t", "="+tmuxTarget),
		nil,
	)
}

// View renders the dashboard
func (m *Model) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n\nPress 'r' to retry, 'q' to quit", m.err)
	}

	if len(m.sessions) == 0 {
		return "No active sessions\n\nPress 'r' to refresh, 'q' to quit"
	}

	s := "Claude Sessions\n\n"

	for i, session := range m.sessions {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}

		indicator := "○"
		if session.State == protocol.StateWaiting {
			indicator = "●"
		}

		duration := formatDuration(time.Since(session.StateSince))
		todo := "(no todos)"
		if len(session.Todos) > 0 {
			todo = session.Todos[0]
			if len(todo) > 30 {
				todo = todo[:27] + "..."
			}
		}

		s += fmt.Sprintf("%s%s %-15s %-8s %8s   %s\n",
			cursor, indicator, session.Label, session.State, duration, todo)
	}

	s += "\n● = waiting (needs input)    ○ = working\n"
	s += "\n[Enter] Jump to pane   [r] Refresh   [q] Quit\n"

	return s
}

func formatDuration(d time.Duration) string {
	minutes := int(d.Minutes())
	seconds := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm %02ds", minutes, seconds)
}

// TickCmd returns a command that ticks for auto-refresh
func TickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg {
		return tickMsg{}
	})
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/dashboard/... -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/dashboard/
git commit -m "feat: add dashboard TUI model"
```

---

## Phase 9: Wrapper Command

### Task 9.1: Wrapper Logic

**Files:**
- Create: `internal/wrapper/wrapper.go`
- Test: `internal/wrapper/wrapper_test.go`

**Step 1: Write the failing test**

Create `internal/wrapper/wrapper_test.go`:
```go
package wrapper

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateSessionID(t *testing.T) {
	id1 := GenerateSessionID()
	id2 := GenerateSessionID()

	if id1 == id2 {
		t.Error("session IDs should be unique")
	}

	if len(id1) < 8 {
		t.Errorf("session ID too short: %q", id1)
	}
}

func TestDefaultLabel(t *testing.T) {
	// Create temp directory with known name
	tmpDir := t.TempDir()
	testDir := filepath.Join(tmpDir, "my-project")
	os.Mkdir(testDir, 0755)

	// Change to that directory temporarily
	oldDir, _ := os.Getwd()
	os.Chdir(testDir)
	defer os.Chdir(oldDir)

	label := DefaultLabel()
	if label != "my-project" {
		t.Errorf("DefaultLabel() = %q, want %q", label, "my-project")
	}
}

func TestWriteHooksConfig(t *testing.T) {
	tmpDir := t.TempDir()
	sessionID := "test-session"
	socketPath := "/tmp/test.sock"

	configPath, err := WriteHooksConfig(tmpDir, sessionID, socketPath)
	if err != nil {
		t.Fatalf("WriteHooksConfig error: %v", err)
	}

	// Verify file was created
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatalf("config file not created: %s", configPath)
	}

	// Read and verify content
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config error: %v", err)
	}

	if !strings.Contains(string(content), sessionID) {
		t.Error("config should contain session ID")
	}
}

func TestCleanupHooksConfig(t *testing.T) {
	tmpDir := t.TempDir()

	configPath, _ := WriteHooksConfig(tmpDir, "test", "/tmp/test.sock")

	// Verify file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatal("config file should exist before cleanup")
	}

	CleanupHooksConfig(configPath)

	// Verify file is gone
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Error("config file should be deleted after cleanup")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/wrapper/... -v`
Expected: FAIL - package doesn't exist

**Step 3: Write minimal implementation**

Create `internal/wrapper/wrapper.go`:
```go
package wrapper

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"

	"github.com/victorarias/claude-manager/internal/hooks"
)

// GenerateSessionID generates a unique session ID
func GenerateSessionID() string {
	bytes := make([]byte, 8)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

// DefaultLabel returns the current directory name as default label
func DefaultLabel() string {
	dir, err := os.Getwd()
	if err != nil {
		return "unknown"
	}
	return filepath.Base(dir)
}

// GetTmuxTarget returns the current tmux pane location
func GetTmuxTarget() string {
	// This will be implemented to shell out to tmux
	// For now return empty string if not in tmux
	if os.Getenv("TMUX") == "" {
		return ""
	}
	return "" // Will implement with actual tmux command
}

// WriteHooksConfig writes a temporary hooks configuration file
func WriteHooksConfig(tmpDir, sessionID, socketPath string) (string, error) {
	configPath := filepath.Join(tmpDir, "claude-hooks-"+sessionID+".json")

	content := hooks.Generate(sessionID, socketPath)

	if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
		return "", err
	}

	return configPath, nil
}

// CleanupHooksConfig removes the temporary hooks configuration
func CleanupHooksConfig(configPath string) {
	os.Remove(configPath)
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/wrapper/... -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/wrapper/
git commit -m "feat: add wrapper utilities"
```

---

## Phase 10: CLI Integration

### Task 10.1: Wire Up CLI Commands

**Files:**
- Modify: `cmd/cm/main.go`

**Step 1: Write the full CLI**

Replace `cmd/cm/main.go`:
```go
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/victorarias/claude-manager/internal/client"
	"github.com/victorarias/claude-manager/internal/daemon"
	"github.com/victorarias/claude-manager/internal/dashboard"
	"github.com/victorarias/claude-manager/internal/protocol"
	"github.com/victorarias/claude-manager/internal/status"
	"github.com/victorarias/claude-manager/internal/wrapper"
)

func main() {
	if len(os.Args) < 2 {
		runWrapper("")
		return
	}

	switch os.Args[1] {
	case "-s":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: cm -s <label>")
			os.Exit(1)
		}
		runWrapper(os.Args[2])
	case "-d", "dashboard":
		runDashboard()
	case "daemon":
		runDaemon()
	case "status":
		runStatus()
	case "list":
		runList()
	case "kill":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: cm kill <id>")
			os.Exit(1)
		}
		runKill(os.Args[2])
	case "_hook-todo":
		if len(os.Args) < 3 {
			os.Exit(1)
		}
		runHookTodo(os.Args[2])
	case "--help", "-h", "help":
		printHelp()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
		printHelp()
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Println(`cm - Claude Manager

Usage:
  cm                Start Claude with tracking (label = directory name)
  cm -s <label>     Start Claude with explicit label
  cm -d             Open dashboard
  cm dashboard      Open dashboard (alias)
  cm daemon         Run daemon in foreground
  cm status         Output for tmux status bar
  cm list           List all sessions (JSON)
  cm kill <id>      Unregister a session`)
}

func runWrapper(label string) {
	if label == "" {
		label = wrapper.DefaultLabel()
	}

	sessionID := wrapper.GenerateSessionID()
	socketPath := client.DefaultSocketPath()

	// Ensure daemon is running
	c := client.New(socketPath)
	if !c.IsRunning() {
		startDaemonBackground()
	}

	// Get tmux target
	tmuxTarget := getTmuxTarget()

	// Get current directory
	dir, _ := os.Getwd()

	// Register with daemon
	if err := c.Register(sessionID, label, dir, tmuxTarget); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not register with daemon: %v\n", err)
	}

	// Write hooks config
	tmpDir := os.TempDir()
	configPath, err := wrapper.WriteHooksConfig(tmpDir, sessionID, socketPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not write hooks config: %v\n", err)
	}
	defer wrapper.CleanupHooksConfig(configPath)

	// Set up cleanup on exit
	cleanup := func() {
		c.Unregister(sessionID)
		wrapper.CleanupHooksConfig(configPath)
	}

	// Handle signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		cleanup()
		os.Exit(0)
	}()

	// Run claude with hooks
	args := []string{"--hooks", configPath}
	args = append(args, os.Args[1:]...)

	cmd := exec.Command("claude", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err = cmd.Run()
	cleanup()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		os.Exit(1)
	}
}

func getTmuxTarget() string {
	if os.Getenv("TMUX") == "" {
		return ""
	}
	cmd := exec.Command("tmux", "display", "-p", "#{session_name}:#{window_index}.#{pane_id}")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func startDaemonBackground() {
	cmd := exec.Command(os.Args[0], "daemon")
	cmd.Start()
	// Give daemon time to start
	// In production, would poll socket
}

func runDaemon() {
	socketPath := client.DefaultSocketPath()
	d := daemon.New(socketPath)

	// Handle shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		d.Stop()
		os.Exit(0)
	}()

	fmt.Printf("Daemon listening on %s\n", socketPath)
	if err := d.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Daemon error: %v\n", err)
		os.Exit(1)
	}
}

func runDashboard() {
	c := client.New("")
	m := dashboard.NewModel(c)

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Dashboard error: %v\n", err)
		os.Exit(1)
	}
}

func runStatus() {
	c := client.New("")
	sessions, err := c.Query("")
	if err != nil {
		// Silent failure for status bar
		return
	}
	output := status.Format(sessions)
	if output != "" {
		fmt.Print(output)
	}
}

func runList() {
	c := client.New("")
	sessions, err := c.Query("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	data, _ := json.MarshalIndent(sessions, "", "  ")
	fmt.Println(string(data))
}

func runKill(id string) {
	c := client.New("")
	if err := c.Unregister(id); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Session unregistered")
}

func runHookTodo(sessionID string) {
	// Read TodoWrite output from stdin
	var todos []string
	// Parse stdin for todo items - this is called from hook
	// For now, just touch the session
	c := client.New("")
	c.UpdateTodos(sessionID, todos)
}
```

**Step 2: Verify it builds**

Run:
```bash
go build -o cm ./cmd/cm && ./cm --help
```
Expected: Help output displayed

**Step 3: Commit**

```bash
git add cmd/cm/
git commit -m "feat: wire up CLI commands"
```

---

### Task 10.2: Add Integration Test

**Files:**
- Create: `test/integration_test.go`

**Step 1: Write integration test**

Create `test/integration_test.go`:
```go
//go:build integration

package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestIntegration_DaemonAndClient(t *testing.T) {
	// Build the binary
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "cm")
	sockPath := filepath.Join(tmpDir, "test.sock")

	cmd := exec.Command("go", "build", "-o", binPath, "../cmd/cm")
	if err := cmd.Run(); err != nil {
		t.Fatalf("build failed: %v", err)
	}

	// Start daemon
	os.Setenv("HOME", tmpDir) // Use temp socket
	daemon := exec.Command(binPath, "daemon")
	if err := daemon.Start(); err != nil {
		t.Fatalf("daemon start failed: %v", err)
	}
	defer daemon.Process.Kill()

	// Wait for daemon
	time.Sleep(100 * time.Millisecond)

	// Test status command
	status := exec.Command(binPath, "status")
	output, _ := status.Output()
	t.Logf("status output: %q", output)

	// Test list command
	list := exec.Command(binPath, "list")
	output, err := list.Output()
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	t.Logf("list output: %s", output)
}
```

**Step 2: Run unit tests only**

Run: `go test ./... -v`
Expected: All unit tests pass

**Step 3: Commit**

```bash
git add test/
git commit -m "test: add integration test scaffold"
```

---

## Phase 11: Polish

### Task 11.1: Add .gitignore

**Files:**
- Create: `.gitignore`

**Step 1: Create gitignore**

Create `.gitignore`:
```
# Binary
cm

# IDE
.idea/
.vscode/
*.swp

# OS
.DS_Store

# Test artifacts
coverage.out
```

**Step 2: Commit**

```bash
git add .gitignore
git commit -m "chore: add gitignore"
```

---

### Task 11.2: Update README

**Files:**
- Modify: `README.md`

**Step 1: Update README with build instructions**

Update `README.md`:
```markdown
# claude-manager

Track and manage multiple Claude Code sessions across tmux.

## Problem

Running multiple Claude sessions means losing track of which ones need your input.

## Solution

- **tmux status bar:** Shows `2 waiting: drumstick, meta`
- **Dashboard:** Interactive TUI to see all sessions and jump to any pane
- **Hooks:** Uses Claude Code hooks to track state changes

## Installation

```bash
go install github.com/victorarias/claude-manager/cmd/cm@latest
```

Or build from source:

```bash
git clone https://github.com/victorarias/claude-manager.git
cd claude-manager
go build -o cm ./cmd/cm
mv cm ~/bin/  # or anywhere in your PATH
```

## Usage

```bash
cm                # Start Claude with directory name as label
cm -s drumstick   # Start Claude with explicit label
cm -d             # Open dashboard
cm status         # Output for tmux status bar
cm list           # List all sessions (JSON)
cm daemon         # Run daemon in foreground
```

## tmux Setup

Add to your `.tmux.conf`:

```tmux
set -g status-interval 5
set -g status-right '#(cm status)'
```

## How It Works

1. `cm` wraps `claude` and installs hooks that report state changes
2. A background daemon tracks all sessions
3. `cm status` queries the daemon for waiting sessions
4. `cm -d` opens an interactive dashboard

## Architecture

See [design doc](docs/plans/2025-12-03-claude-manager-design.md) for details.
```

**Step 2: Commit**

```bash
git add README.md
git commit -m "docs: update README with installation and usage"
```

---

## Summary

This plan implements all core components of Claude Manager:

1. **Protocol types** - Message definitions for daemon communication
2. **Session store** - In-memory session tracking with thread safety
3. **Client library** - Unix socket client for daemon communication
4. **Daemon** - Background service managing session state
5. **Hooks generator** - Creates Claude Code hooks configuration
6. **Status command** - Formats output for tmux status bar
7. **Dashboard TUI** - Interactive session viewer with pane jumping
8. **Wrapper command** - Starts Claude with hooks and daemon registration
9. **CLI integration** - All commands wired up

Each task follows TDD: write failing test, implement, verify passing, commit.
