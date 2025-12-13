# Session State Classification Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Introduce three session states (working, waiting_input, idle) with async LLM-based classification to help users prioritize which sessions need attention.

**Architecture:** Stop hook sends transcript path to daemon. Daemon asynchronously parses transcript, checks todos, and optionally calls Claude CLI to classify the last message. Frontend displays distinct visuals for each state.

**Tech Stack:** Go (daemon), TypeScript/React (frontend), Claude CLI (classification)

**Color Scheme:**
- 🟡 Yellow = `waiting_input` (needs user response)
- 🟢 Green = `working` (Claude is active)
- ⚪ Grey = `idle` (finished, nothing to do)

---

## Task 1: Remove TUI Dashboard

**Files:**
- Delete: `internal/dashboard/model.go`
- Delete: `internal/dashboard/model_test.go`
- Modify: `cmd/cm/main.go`

**Step 1: Delete the dashboard package**

```bash
rm -rf internal/dashboard/
```

**Step 2: Remove dashboard imports and code from main.go**

Remove the import:
```go
// Remove this line:
"github.com/victorarias/claude-manager/internal/dashboard"
```

Remove the bubbletea import:
```go
// Remove this line:
tea "github.com/charmbracelet/bubbletea"
```

**Step 3: Remove `-d` flag handling from parseArgs**

In `parseArgs`, remove the dashboard case:
```go
// Remove:
case "-d":
    dashboard = true
```

Update function signature:
```go
func parseArgs(args []string) (label string, yolo bool, remaining []string) {
```

**Step 4: Remove dashboard subcommand from main()**

Remove:
```go
// Remove this block:
if dashboardFlag {
    runDashboard()
    return
}

// And this case:
case "dashboard":
    runDashboard()
    return
```

**Step 5: Remove runDashboard function**

Delete the entire `runDashboard()` function.

**Step 6: Update printHelp to remove dashboard references**

Remove these lines from the help text:
```go
%s -d                 Open dashboard
%s dashboard          Open dashboard (alias)
```

**Step 7: Update runWrapperWithFlags call**

```go
// Change from:
label, yolo, dashboardFlag, args := parseArgs(os.Args[1:])

// To:
label, yolo, args := parseArgs(os.Args[1:])
```

**Step 8: Run build to verify**

```bash
go build ./cmd/cm
```

**Step 9: Commit**

```bash
git add -A
git commit -m "refactor: remove TUI dashboard

The Tauri app replaces the terminal-based dashboard.
Removes internal/dashboard package and related CLI flags."
```

---

## Task 2: Update Protocol Types

**Files:**
- Modify: `internal/protocol/types.go`

**Step 1: Update state constants**

```go
// States
const (
	StateWorking      = "working"
	StateWaitingInput = "waiting_input"
	StateIdle         = "idle"
	// Keep StateWaiting as alias for backward compatibility during transition
	StateWaiting = "waiting"
)
```

**Step 2: Add CmdStop constant and StopMessage**

```go
// Commands - add after CmdClearSessions
const (
	// ... existing commands ...
	CmdStop = "stop"
)

// StopMessage signals session stopped, triggers classification
type StopMessage struct {
	Cmd            string `json:"cmd"`
	ID             string `json:"id"`
	TranscriptPath string `json:"transcript_path"`
}
```

**Step 3: Add StopMessage to ParseMessage switch**

```go
case CmdStop:
	var msg StopMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return "", nil, err
	}
	return peek.Cmd, &msg, nil
```

**Step 4: Remove TmuxTarget from Session struct**

```go
// Session represents a tracked Claude session
type Session struct {
	ID         string    `json:"id"`
	Label      string    `json:"label"`
	Directory  string    `json:"directory"`
	// Remove: TmuxTarget string    `json:"tmux_target"`
	State      string    `json:"state"`
	StateSince time.Time `json:"state_since"`
	Todos      []string  `json:"todos,omitempty"`
	LastSeen   time.Time `json:"last_seen"`
	Muted      bool      `json:"muted"`
}
```

**Step 5: Remove Tmux from RegisterMessage**

```go
// RegisterMessage registers a new session with the daemon
type RegisterMessage struct {
	Cmd   string `json:"cmd"`
	ID    string `json:"id"`
	Label string `json:"label"`
	Dir   string `json:"dir"`
	// Remove: Tmux  string `json:"tmux"`
}
```

**Step 6: Increment ProtocolVersion**

```go
const ProtocolVersion = "2"
```

**Step 7: Run tests**

```bash
go test ./internal/protocol/... -v
```

**Step 8: Commit**

```bash
git add internal/protocol/types.go
git commit -m "feat(protocol): add three-state model and stop command

- Add StateWaitingInput and StateIdle states
- Add CmdStop and StopMessage for classification trigger
- Remove tmux fields from Session and RegisterMessage
- Increment ProtocolVersion to 2"
```

---

## Task 3: Update Hooks to Send Stop Command

**Files:**
- Modify: `internal/hooks/hooks.go`

**Step 1: Update Stop hook to send stop command with transcript_path**

Replace the Stop hook configuration:

```go
"Stop": {
	{
		Matcher: "*",
		Hooks: []Hook{
			{
				Type:    "command",
				Command: fmt.Sprintf(`echo '{"cmd":"stop","id":"%s","transcript_path":"'"$CLAUDE_TRANSCRIPT_PATH"'"}' | nc -U %s`, sessionID, socketPath),
			},
		},
	},
},
```

Note: Claude Code provides `transcript_path` in the hook input. We need to use the correct variable name. Based on research, it's passed in the JSON input to the hook. Let's use a shell approach:

```go
"Stop": {
	{
		Matcher: "*",
		Hooks: []Hook{
			{
				Type:    "command",
				Command: fmt.Sprintf(`jq -nc --arg id "%s" --arg tp "$(cat | jq -r .transcript_path)" '{"cmd":"stop","id":$id,"transcript_path":$tp}' | nc -U %s`, sessionID, socketPath),
			},
		},
	},
},
```

Actually, looking at how hooks work, the input is piped to the command. Let's use a simpler approach with a helper:

```go
"Stop": {
	{
		Matcher: "*",
		Hooks: []Hook{
			{
				Type:    "command",
				Command: fmt.Sprintf(`~/.local/bin/attn _hook-stop "%s"`, sessionID),
			},
		},
	},
},
```

**Step 2: Run tests**

```bash
go test ./internal/hooks/... -v
```

**Step 3: Commit**

```bash
git add internal/hooks/hooks.go
git commit -m "feat(hooks): update Stop hook to trigger classification

Stop hook now calls attn _hook-stop which receives transcript_path
from stdin and sends stop command to daemon for async classification."
```

---

## Task 4: Add Hook-Stop Command

**Files:**
- Create: `cmd/cm/hook_stop.go`
- Modify: `cmd/cm/main.go`

**Step 1: Create hook_stop.go**

```go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/victorarias/claude-manager/internal/client"
	"github.com/victorarias/claude-manager/internal/config"
)

// hookStopInput is the JSON input from Claude Code Stop hook
type hookStopInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
}

func runHookStop(sessionID string) error {
	// Read hook input from stdin
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}

	var hookInput hookStopInput
	if err := json.Unmarshal(input, &hookInput); err != nil {
		return fmt.Errorf("parse hook input: %w", err)
	}

	// Send stop command to daemon
	c := client.New(config.SocketPath())
	return c.SendStop(sessionID, hookInput.TranscriptPath)
}
```

**Step 2: Add SendStop to client**

In `internal/client/client.go`:

```go
// SendStop sends a stop signal with transcript path for classification
func (c *Client) SendStop(id, transcriptPath string) error {
	msg := protocol.StopMessage{
		Cmd:            protocol.CmdStop,
		ID:             id,
		TranscriptPath: transcriptPath,
	}
	_, err := c.send(msg)
	return err
}
```

**Step 3: Add _hook-stop subcommand to main.go**

In the args handling section of main.go, add:

```go
if len(args) >= 2 && args[0] == "_hook-stop" {
	if err := runHookStop(args[1]); err != nil {
		fmt.Fprintf(os.Stderr, "hook-stop error: %v\n", err)
		os.Exit(1)
	}
	return
}
```

**Step 4: Run tests**

```bash
go build -o attn ./cmd/cm && ./attn --help
```

**Step 5: Commit**

```bash
git add cmd/cm/hook_stop.go cmd/cm/main.go internal/client/client.go
git commit -m "feat(cli): add _hook-stop command for stop hook

Reads transcript_path from Claude Code hook input and sends
stop command to daemon for async state classification."
```

---

## Task 5: Add Transcript Parser

**Files:**
- Create: `internal/transcript/parser.go`
- Create: `internal/transcript/parser_test.go`

**Step 1: Write the failing test**

```go
package transcript

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractLastAssistantMessage(t *testing.T) {
	// Create temp JSONL file
	content := `{"type":"user","message":{"content":"Hello"}}
{"type":"assistant","message":{"content":"Hi there! How can I help you today?"}}
{"type":"user","message":{"content":"Fix the bug"}}
{"type":"assistant","message":{"content":"I've fixed the bug. The issue was in the validation logic. All tests are now passing!"}}
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	result, err := ExtractLastAssistantMessage(path, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "I've fixed the bug. The issue was in the validation logic. All tests are now passing!"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestExtractLastAssistantMessage_Truncates(t *testing.T) {
	longMsg := ""
	for i := 0; i < 100; i++ {
		longMsg += "Hello world! "
	}

	content := `{"type":"assistant","message":{"content":"` + longMsg + `"}}
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	result, err := ExtractLastAssistantMessage(path, 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) > 50 {
		t.Errorf("result length %d exceeds limit 50", len(result))
	}
}

func TestExtractLastAssistantMessage_NoAssistant(t *testing.T) {
	content := `{"type":"user","message":{"content":"Hello"}}
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	result, err := ExtractLastAssistantMessage(path, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/transcript/... -v
```

Expected: FAIL (package doesn't exist)

**Step 3: Write minimal implementation**

```go
package transcript

import (
	"bufio"
	"encoding/json"
	"os"
)

// transcriptEntry represents a single entry in the JSONL transcript
type transcriptEntry struct {
	Type    string `json:"type"`
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
}

// ExtractLastAssistantMessage reads a JSONL transcript and returns
// the last N characters of the last assistant message.
func ExtractLastAssistantMessage(path string, maxChars int) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	var lastAssistantContent string
	scanner := bufio.NewScanner(file)
	// Increase buffer size for long lines
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var entry transcriptEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue // Skip malformed lines
		}

		if entry.Type == "assistant" && entry.Message.Content != "" {
			lastAssistantContent = entry.Message.Content
		}
	}

	if err := scanner.Err(); err != nil {
		return "", err
	}

	// Truncate to last maxChars
	if len(lastAssistantContent) > maxChars {
		lastAssistantContent = lastAssistantContent[len(lastAssistantContent)-maxChars:]
	}

	return lastAssistantContent, nil
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./internal/transcript/... -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/transcript/
git commit -m "feat(transcript): add JSONL parser for extracting assistant messages

Parses Claude Code transcript files and extracts the last N characters
of the most recent assistant message for state classification."
```

---

## Task 6: Add LLM Classifier

**Files:**
- Create: `internal/classifier/classifier.go`
- Create: `internal/classifier/classifier_test.go`

**Step 1: Write the failing test**

```go
package classifier

import (
	"testing"
)

func TestParseResponse_Waiting(t *testing.T) {
	tests := []struct {
		response string
		want     string
	}{
		{"WAITING", "waiting_input"},
		{"waiting", "waiting_input"},
		{"WAITING\n", "waiting_input"},
		{"  WAITING  ", "waiting_input"},
	}

	for _, tt := range tests {
		got := ParseResponse(tt.response)
		if got != tt.want {
			t.Errorf("ParseResponse(%q) = %q, want %q", tt.response, got, tt.want)
		}
	}
}

func TestParseResponse_Done(t *testing.T) {
	tests := []struct {
		response string
		want     string
	}{
		{"DONE", "idle"},
		{"done", "idle"},
		{"DONE\n", "idle"},
		{"anything else", "idle"},
		{"", "idle"},
	}

	for _, tt := range tests {
		got := ParseResponse(tt.response)
		if got != tt.want {
			t.Errorf("ParseResponse(%q) = %q, want %q", tt.response, got, tt.want)
		}
	}
}

func TestBuildPrompt(t *testing.T) {
	text := "Would you like me to continue?"
	prompt := BuildPrompt(text)

	if prompt == "" {
		t.Error("BuildPrompt returned empty string")
	}
	if !contains(prompt, text) {
		t.Error("BuildPrompt should include the input text")
	}
	if !contains(prompt, "WAITING") {
		t.Error("BuildPrompt should mention WAITING")
	}
	if !contains(prompt, "DONE") {
		t.Error("BuildPrompt should mention DONE")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/classifier/... -v
```

Expected: FAIL (package doesn't exist)

**Step 3: Write minimal implementation**

```go
package classifier

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const promptTemplate = `Analyze this text from an AI assistant and determine if it's waiting for user input.

Reply with exactly one word: WAITING or DONE

WAITING means:
- Asks a question
- Requests clarification
- Offers choices requiring selection
- Asks for confirmation to proceed

DONE means:
- States completion
- Provides information without asking
- Reports results
- No question or request for input

Text to analyze:
"""
%s
"""
`

// BuildPrompt creates the classification prompt
func BuildPrompt(text string) string {
	return fmt.Sprintf(promptTemplate, text)
}

// ParseResponse parses the LLM response into a state
func ParseResponse(response string) string {
	normalized := strings.TrimSpace(strings.ToUpper(response))
	if strings.Contains(normalized, "WAITING") {
		return "waiting_input"
	}
	return "idle"
}

// Classify calls Claude CLI to classify the text
// Returns "waiting_input" or "idle"
func Classify(text string, timeout time.Duration) (string, error) {
	if text == "" {
		return "idle", nil
	}

	prompt := BuildPrompt(text)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "claude", "-p", prompt, "--print")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "waiting_input", fmt.Errorf("claude cli: %w: %s", err, stderr.String())
	}

	return ParseResponse(stdout.String()), nil
}
```

**Step 4: Add missing import**

```go
import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)
```

**Step 5: Run test to verify it passes**

```bash
go test ./internal/classifier/... -v
```

Expected: PASS

**Step 6: Commit**

```bash
git add internal/classifier/
git commit -m "feat(classifier): add LLM-based state classification

Uses Claude CLI to classify assistant messages as waiting for input
or idle. Includes prompt building and response parsing."
```

---

## Task 7: Add Stop Handler to Daemon

**Files:**
- Modify: `internal/daemon/daemon.go`

**Step 1: Add handleStop method**

```go
func (d *Daemon) handleStop(conn net.Conn, msg *protocol.StopMessage) {
	d.store.Touch(msg.ID)
	d.sendOK(conn)

	// Async classification
	go d.classifySessionState(msg.ID, msg.TranscriptPath)
}

func (d *Daemon) classifySessionState(sessionID, transcriptPath string) {
	session := d.store.Get(sessionID)
	if session == nil {
		return
	}

	// Check pending todos first (fast path)
	if len(session.Todos) > 0 {
		d.updateAndBroadcastState(sessionID, protocol.StateWaitingInput)
		return
	}

	// Parse transcript for last assistant message
	lastMessage, err := transcript.ExtractLastAssistantMessage(transcriptPath, 500)
	if err != nil {
		d.logf("transcript parse error for %s: %v", sessionID, err)
		// Default to waiting_input on error (safer)
		d.updateAndBroadcastState(sessionID, protocol.StateWaitingInput)
		return
	}

	if lastMessage == "" {
		d.updateAndBroadcastState(sessionID, protocol.StateIdle)
		return
	}

	// Classify with LLM
	state, err := classifier.Classify(lastMessage, 30*time.Second)
	if err != nil {
		d.logf("classifier error for %s: %v", sessionID, err)
		// Default to waiting_input on error
		state = protocol.StateWaitingInput
	}

	d.updateAndBroadcastState(sessionID, state)
}

func (d *Daemon) updateAndBroadcastState(sessionID, state string) {
	d.store.UpdateState(sessionID, state)

	// Broadcast to WebSocket clients
	session := d.store.Get(sessionID)
	if session != nil {
		d.wsHub.Broadcast(&protocol.WebSocketEvent{
			Event:   protocol.EventSessionStateChanged,
			Session: session,
		})
	}
}
```

**Step 2: Add imports**

```go
import (
	// ... existing imports ...
	"github.com/victorarias/claude-manager/internal/classifier"
	"github.com/victorarias/claude-manager/internal/transcript"
)
```

**Step 3: Add case to handleConnection switch**

```go
case protocol.CmdStop:
	d.handleStop(conn, msg.(*protocol.StopMessage))
```

**Step 4: Update handleRegister to remove TmuxTarget**

```go
func (d *Daemon) handleRegister(conn net.Conn, msg *protocol.RegisterMessage) {
	session := &protocol.Session{
		ID:         msg.ID,
		Label:      msg.Label,
		Directory:  msg.Dir,
		// Remove: TmuxTarget: msg.Tmux,
		State:      protocol.StateWaiting,
		StateSince: time.Now(),
		LastSeen:   time.Now(),
	}
	d.store.Add(session)
	d.sendOK(conn)

	// Broadcast to WebSocket clients
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:   protocol.EventSessionRegistered,
		Session: session,
	})
}
```

**Step 5: Run tests**

```bash
go test ./internal/daemon/... -v
```

**Step 6: Commit**

```bash
git add internal/daemon/daemon.go
git commit -m "feat(daemon): add async state classification on stop

When Stop hook fires, daemon asynchronously:
1. Checks pending todos -> waiting_input
2. Parses transcript for last assistant message
3. Calls Claude CLI to classify -> waiting_input or idle
4. Broadcasts state change to WebSocket clients"
```

---

## Task 8: Update Client Register (Remove Tmux)

**Files:**
- Modify: `internal/client/client.go`

**Step 1: Update Register function**

```go
// Register registers a new session
func (c *Client) Register(id, label, dir string) error {
	msg := protocol.RegisterMessage{
		Cmd:   protocol.CmdRegister,
		ID:    id,
		Label: label,
		Dir:   dir,
	}
	_, err := c.send(msg)
	return err
}
```

**Step 2: Update any callers**

Search for calls to `c.Register` and remove the tmux parameter.

**Step 3: Run tests**

```bash
go test ./... -v
```

**Step 4: Commit**

```bash
git add internal/client/client.go
git commit -m "refactor(client): remove tmux parameter from Register"
```

---

## Task 9: Update Daemon Tests

**Files:**
- Modify: `internal/daemon/daemon_test.go`

**Step 1: Update test Register calls to remove tmux**

Replace all instances of:
```go
c.Register("sess-1", "test", "/tmp", "main:1.%0")
```

With:
```go
c.Register("sess-1", "test", "/tmp")
```

**Step 2: Add test for stop classification**

```go
func TestDaemon_StopClassification_WithTodos(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")

	d := NewForTesting(sockPath)
	go d.Start()
	defer d.Stop()

	time.Sleep(50 * time.Millisecond)

	c := client.New(sockPath)

	// Register session
	c.Register("sess-1", "test", "/tmp")

	// Add todos
	c.UpdateTodos("sess-1", []string{"pending task"})

	// Create dummy transcript file
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")
	os.WriteFile(transcriptPath, []byte(`{"type":"assistant","message":{"content":"Done!"}}`), 0644)

	// Send stop
	c.SendStop("sess-1", transcriptPath)

	// Wait for async classification
	time.Sleep(100 * time.Millisecond)

	// Query and check state
	sessions, _ := c.Query("")
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].State != protocol.StateWaitingInput {
		t.Errorf("expected state=%s, got state=%s", protocol.StateWaitingInput, sessions[0].State)
	}
}
```

**Step 3: Run tests**

```bash
go test ./internal/daemon/... -v
```

**Step 4: Commit**

```bash
git add internal/daemon/daemon_test.go
git commit -m "test(daemon): update tests for three-state model

- Remove tmux parameter from Register calls
- Add test for stop classification with pending todos"
```

---

## Task 10: Update Frontend Types

**Files:**
- Modify: `app/src/hooks/useDaemonSocket.ts`

**Step 1: Update DaemonSession interface**

```typescript
export interface DaemonSession {
  id: string;
  label: string;
  directory: string;
  // Remove: tmux_target: string;
  state: 'working' | 'waiting_input' | 'idle';
  state_since: string;
  todos: string[] | null;
  last_seen: string;
  muted: boolean;
}
```

**Step 2: Update PROTOCOL_VERSION**

```typescript
const PROTOCOL_VERSION = '2';
```

**Step 3: Commit**

```bash
git add app/src/hooks/useDaemonSocket.ts
git commit -m "feat(app): update session types for three-state model

- Add waiting_input and idle states
- Remove tmux_target field
- Bump protocol version to 2"
```

---

## Task 11: Update AttentionDrawer Component

**Files:**
- Modify: `app/src/components/AttentionDrawer.tsx`
- Modify: `app/src/components/AttentionDrawer.css`

**Step 1: Update props interface**

```typescript
interface AttentionDrawerProps {
  isOpen: boolean;
  onClose: () => void;
  waitingSessions: Array<{
    id: string;
    label: string;
    state: 'working' | 'waiting_input' | 'idle';
  }>;
  prs: DaemonPR[];
  onSelectSession: (id: string) => void;
}
```

**Step 2: Filter only waiting_input sessions (not idle)**

The parent component should filter to only show `waiting_input` sessions in the attention drawer. Update the filtering logic where `AttentionDrawer` is used.

**Step 3: Update CSS for visual distinction**

Note: The AttentionDrawer only shows waiting_input sessions. The main sidebar/home list shows all sessions with these colors:

```css
/* Yellow for waiting_input - needs user response */
.item-dot.session.waiting-input {
  background: #f59e0b;
}

/* Green for working - Claude is active */
.item-dot.session.working {
  background: #22c55e;
}

/* Grey for idle - finished, nothing to do */
.item-dot.session.idle {
  background: #6b7280;
}
```

**Step 4: Commit**

```bash
git add app/src/components/AttentionDrawer.tsx app/src/components/AttentionDrawer.css
git commit -m "feat(app): update AttentionDrawer for three-state model

Sessions waiting for input show amber indicator.
Idle sessions show green indicator."
```

---

## Task 12: Update Sidebar Session Colors

**Files:**
- Modify: `app/src/components/Dashboard.tsx`
- Modify: `app/src/components/Sidebar.tsx` (if exists)

**Step 1: Update waitingSessions filter for attention drawer**

```typescript
const waitingSessions = sessions.filter(s => s.state === 'waiting_input' && !s.muted);
```

**Step 2: Update session list rendering to use correct colors**

For each session in the sidebar/home list, apply the correct class based on state:
- `waiting_input` → yellow dot
- `working` → green dot
- `idle` → grey dot

**Step 3: Search and replace old state references**

Search for `'waiting'` and replace with `'waiting_input'` where appropriate.

**Step 4: Commit**

```bash
git add app/src/components/Dashboard.tsx app/src/components/Sidebar.tsx
git commit -m "feat(app): update session colors for three-state model

- Yellow = waiting_input (needs attention)
- Green = working (active)
- Grey = idle (finished)
- Only waiting_input sessions appear in attention drawer"
```

---

## Task 13: Build and Test End-to-End

**Step 1: Build the Go binary**

```bash
make install
```

**Step 2: Start the app**

```bash
cd app && pnpm run dev:all
```

**Step 3: Manual test scenarios**

1. Start a Claude session via the app
2. Have Claude finish a task with "Done!" → should show as `idle`
3. Have Claude ask a question → should show as `waiting_input`
4. Have Claude work with pending todos → should show as `waiting_input`

**Step 4: Run all tests**

```bash
go test ./... -v
cd app && pnpm test
```

**Step 5: Final commit**

```bash
git add -A
git commit -m "feat: complete three-state session classification

Sessions now have three states:
- working: Claude is actively processing
- waiting_input: Claude needs user response
- idle: Claude finished, nothing pending

Classification uses Claude CLI to analyze the last assistant message
when neither todos nor explicit questions determine the state."
```

---

## Summary of Changes

| File | Change |
|------|--------|
| `internal/dashboard/` | **Deleted**: TUI dashboard no longer needed |
| `cmd/cm/main.go` | Remove dashboard, add _hook-stop |
| `internal/protocol/types.go` | Add states, StopMessage, remove tmux, bump version |
| `internal/hooks/hooks.go` | Update Stop hook to call _hook-stop |
| `cmd/cm/hook_stop.go` | New file: handles Stop hook input |
| `internal/client/client.go` | Add SendStop, remove tmux from Register |
| `internal/transcript/parser.go` | New file: JSONL transcript parser |
| `internal/classifier/classifier.go` | New file: LLM classification |
| `internal/daemon/daemon.go` | Add handleStop, async classification |
| `internal/daemon/daemon_test.go` | Update tests for new model |
| `app/src/hooks/useDaemonSocket.ts` | Update types, protocol version |
| `app/src/components/AttentionDrawer.tsx` | Update for new states |
| `app/src/components/Dashboard.tsx` | Session colors: yellow/green/grey |
