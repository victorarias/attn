package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"nhooyr.io/websocket"
)

// mockReviewer simulates the reviewer agent for testing
type mockReviewer struct {
	store *store.Store
}

func (m *mockReviewer) Run(ctx context.Context, config ReviewerConfig, onEvent func(ReviewerEvent)) error {
	// Send started event
	onEvent(ReviewerEvent{Type: "started"})

	// Check for cancellation
	select {
	case <-ctx.Done():
		onEvent(ReviewerEvent{Type: "cancelled"})
		return ctx.Err()
	default:
	}

	// Send some chunks
	onEvent(ReviewerEvent{Type: "chunk", Content: "Reviewing changes..."})
	onEvent(ReviewerEvent{Type: "chunk", Content: "Found some issues."})

	// Create comments in store and send findings
	for i, file := range []string{"example.go", "handler.go"} {
		comment, _ := m.store.AddComment(config.ReviewID, file, 5, 5, "Test finding", "agent")
		if comment != nil {
			onEvent(ReviewerEvent{
				Type: "finding",
				Finding: &ReviewerFinding{
					Filepath:  file,
					LineStart: 5,
					LineEnd:   5,
					Content:   "Test finding",
					Severity:  "warning",
					CommentID: comment.ID,
				},
			})
		}
		// Small delay between findings
		if i == 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Check for cancellation again
	select {
	case <-ctx.Done():
		onEvent(ReviewerEvent{Type: "cancelled"})
		return ctx.Err()
	default:
	}

	// Send complete event
	onEvent(ReviewerEvent{Type: "complete", Success: true})
	return nil
}

// mockReviewerFactory creates mock reviewers for testing
func mockReviewerFactory(s *store.Store) Reviewer {
	return &mockReviewer{store: s}
}

// slowMockReviewer simulates a slow reviewer for cancellation testing
type slowMockReviewer struct {
	store *store.Store
}

func (m *slowMockReviewer) Run(ctx context.Context, config ReviewerConfig, onEvent func(ReviewerEvent)) error {
	// Send started event
	onEvent(ReviewerEvent{Type: "started"})

	// Simulate slow processing with cancellation checks
	for i := 0; i < 50; i++ {
		select {
		case <-ctx.Done():
			onEvent(ReviewerEvent{Type: "cancelled"})
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
			onEvent(ReviewerEvent{Type: "chunk", Content: "Processing..."})
		}
	}

	onEvent(ReviewerEvent{Type: "complete", Success: true})
	return nil
}

// slowMockReviewerFactory creates slow mock reviewers for cancellation testing
func slowMockReviewerFactory(s *store.Store) Reviewer {
	return &slowMockReviewer{store: s}
}

// createTestRepo creates a temporary git repo with modified files for testing
func createTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// Initialize git repo
	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if err := cmd.Run(); err != nil {
			t.Fatalf("Failed to run %v: %v", args, err)
		}
	}

	// Create and commit initial file
	initialFile := filepath.Join(dir, "example.go")
	if err := os.WriteFile(initialFile, []byte("package main\n\nfunc main() {}\n"), 0644); err != nil {
		t.Fatalf("Failed to write initial file: %v", err)
	}

	cmds = [][]string{
		{"git", "add", "."},
		{"git", "commit", "-m", "Initial commit"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if err := cmd.Run(); err != nil {
			t.Fatalf("Failed to run %v: %v", args, err)
		}
	}

	// Modify the file (unstaged change)
	modifiedContent := `package main

func main() {
	// Added line
	println("hello")
}
`
	if err := os.WriteFile(initialFile, []byte(modifiedContent), 0644); err != nil {
		t.Fatalf("Failed to modify file: %v", err)
	}

	// Create a second file and stage it (so we have staged + unstaged changes)
	secondFile := filepath.Join(dir, "handler.go")
	if err := os.WriteFile(secondFile, []byte("package main\n\nfunc handler() {}\n"), 0644); err != nil {
		t.Fatalf("Failed to write second file: %v", err)
	}

	// Stage the second file
	cmd := exec.Command("git", "add", "handler.go")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to stage handler.go: %v", err)
	}

	return dir
}

func TestStartReview_EventSequence(t *testing.T) {
	t.Setenv("ATTN_WS_PORT", "19920")
	wsPort := "19920"

	// Create a real git repo with modified files
	repoPath := createTestRepo(t)

	sockPath := filepath.Join("/tmp", fmt.Sprintf("attn-review-test-%d.sock", time.Now().UnixNano()))
	os.Remove(sockPath)

	harness := NewTestHarnessBuilder(sockPath).
		WithReviewerFactory(mockReviewerFactory).
		Build()
	harness.Start()
	defer harness.Stop()

	// Connect to WebSocket
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

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

	// Read initial state (discard)
	_, _, err := wsConn.Read(ctx)
	if err != nil {
		t.Fatalf("Read initial state error: %v", err)
	}

	// Send start_review command
	reviewID := "test-review-123"
	startCmd := protocol.StartReviewMessage{
		Cmd:        protocol.CmdStartReview,
		ReviewID:   reviewID,
		RepoPath:   repoPath,
		Branch:     "feature-branch",
		BaseBranch: "main",
	}
	cmdJSON, _ := json.Marshal(startCmd)
	err = wsConn.Write(ctx, websocket.MessageText, cmdJSON)
	if err != nil {
		t.Fatalf("Write start_review command error: %v", err)
	}

	// Collect events until review_complete
	var events []map[string]interface{}
	timeout := time.After(15 * time.Second)

	for {
		select {
		case <-timeout:
			t.Fatalf("Timeout waiting for review_complete. Got events: %v", eventTypes(events))
		default:
			_, data, err := wsConn.Read(ctx)
			if err != nil {
				t.Fatalf("Read error: %v", err)
			}

			var event map[string]interface{}
			json.Unmarshal(data, &event)
			events = append(events, event)

			if event["event"] == protocol.EventReviewComplete {
				goto done
			}
		}
	}
done:

	// Verify event sequence
	eventTypeList := eventTypes(events)

	// Should start with review_started
	if len(eventTypeList) == 0 || eventTypeList[0] != protocol.EventReviewStarted {
		t.Errorf("Expected first event to be review_started, got: %v", eventTypeList)
	}

	// Should end with review_complete
	if eventTypeList[len(eventTypeList)-1] != protocol.EventReviewComplete {
		t.Errorf("Expected last event to be review_complete, got: %v", eventTypeList)
	}

	// Should have at least one chunk
	chunkCount := countEventType(events, protocol.EventReviewChunk)
	if chunkCount < 1 {
		t.Errorf("Expected at least 1 review_chunk event, got %d", chunkCount)
	}

	// Should have exactly 2 findings
	findingCount := countEventType(events, protocol.EventReviewFinding)
	if findingCount != 2 {
		t.Errorf("Expected 2 review_finding events, got %d", findingCount)
	}

	// Verify review_complete has success=true
	lastEvent := events[len(events)-1]
	if lastEvent["success"] != true {
		t.Errorf("Expected review_complete success=true, got %v", lastEvent["success"])
	}

	// Verify comments were created in store
	comments, err := harness.Store.GetComments(reviewID)
	if err != nil {
		t.Fatalf("GetComments error: %v", err)
	}
	if len(comments) != 2 {
		t.Errorf("Expected 2 comments in store, got %d", len(comments))
	}

	// Verify comments have author="agent"
	for _, c := range comments {
		if c.Author != "agent" {
			t.Errorf("Expected comment author='agent', got '%s'", c.Author)
		}
	}

	t.Logf("Test passed. Event sequence: %v", eventTypeList)
	t.Logf("Comments created: %d", len(comments))
}

func TestCancelReview(t *testing.T) {
	t.Setenv("ATTN_WS_PORT", "19921")
	wsPort := "19921"

	// Create a real git repo with modified files
	repoPath := createTestRepo(t)

	sockPath := filepath.Join("/tmp", fmt.Sprintf("attn-review-cancel-test-%d.sock", time.Now().UnixNano()))
	os.Remove(sockPath)

	harness := NewTestHarnessBuilder(sockPath).
		WithReviewerFactory(slowMockReviewerFactory).
		Build()
	harness.Start()
	defer harness.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

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

	// Read initial state (discard)
	_, _, _ = wsConn.Read(ctx)

	// Send start_review command
	reviewID := "test-cancel-review"
	startCmd := protocol.StartReviewMessage{
		Cmd:        protocol.CmdStartReview,
		ReviewID:   reviewID,
		RepoPath:   repoPath,
		Branch:     "feature-branch",
		BaseBranch: "main",
	}
	cmdJSON, _ := json.Marshal(startCmd)
	wsConn.Write(ctx, websocket.MessageText, cmdJSON)

	// Wait for review_started
	_, data, _ := wsConn.Read(ctx)
	var event map[string]interface{}
	json.Unmarshal(data, &event)
	if event["event"] != protocol.EventReviewStarted {
		t.Fatalf("Expected review_started, got %v", event["event"])
	}

	// Send cancel command
	cancelCmd := protocol.CancelReviewMessage{
		Cmd:      protocol.CmdCancelReview,
		ReviewID: reviewID,
	}
	cancelJSON, _ := json.Marshal(cancelCmd)
	wsConn.Write(ctx, websocket.MessageText, cancelJSON)

	// Should receive review_cancelled
	foundCancelled := false
	timeout := time.After(3 * time.Second)
	for !foundCancelled {
		select {
		case <-timeout:
			t.Fatal("Timeout waiting for review_cancelled")
		default:
			_, data, err := wsConn.Read(ctx)
			if err != nil {
				t.Fatalf("Read error: %v", err)
			}
			var ev map[string]interface{}
			json.Unmarshal(data, &ev)
			if ev["event"] == protocol.EventReviewCancelled {
				foundCancelled = true
			}
		}
	}

	t.Log("Test passed. Review was cancelled successfully.")
}

// Helper functions

func eventTypes(events []map[string]interface{}) []string {
	var types []string
	for _, e := range events {
		if t, ok := e["event"].(string); ok {
			types = append(types, t)
		}
	}
	return types
}

func countEventType(events []map[string]interface{}, eventType string) int {
	count := 0
	for _, e := range events {
		if e["event"] == eventType {
			count++
		}
	}
	return count
}
