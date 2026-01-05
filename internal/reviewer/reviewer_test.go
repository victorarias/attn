package reviewer

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/reviewer/transport"
	"github.com/victorarias/attn/internal/store"
)

// createTestRepo creates a temporary git repo with changes for testing.
func createTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

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

	// Create initial file and commit
	initialFile := filepath.Join(dir, "main.go")
	if err := os.WriteFile(initialFile, []byte("package main\n\nfunc main() {}\n"), 0644); err != nil {
		t.Fatalf("Failed to write file: %v", err)
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

	// Modify file
	modifiedContent := `package main

func main() {
	// New code
	println("hello")
}
`
	if err := os.WriteFile(initialFile, []byte(modifiedContent), 0644); err != nil {
		t.Fatalf("Failed to modify file: %v", err)
	}

	return dir
}

// createTestStore creates a temporary SQLite store.
func createTestStore(t *testing.T) *store.Store {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	s, err := store.NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	return s
}

// buildMockTransportForReview creates a mock transport that simulates a review.
// It responds to tool calls by returning the tool name as confirmation.
func buildMockTransportForReview(sessionID string) *transport.MockTransport {
	mt := transport.NewFixtureBuilder().
		// Assistant says it will review
		AddAssistantText("I'll review the changes on this branch.").
		// Tool call: get_changed_files
		AddToolUse("tool-1", "get_changed_files", map[string]any{}).
		// Assistant processes result
		AddAssistantText("Found files to review. Let me check the diffs.").
		// Tool call: get_diff
		AddToolUse("tool-2", "get_diff", map[string]any{"paths": []any{"main.go"}}).
		// Assistant analyzes
		AddAssistantText("I found an issue with error handling.").
		// Tool call: add_comment
		AddToolUse("tool-3", "add_comment", map[string]any{
			"filepath":   "main.go",
			"line_start": float64(5),
			"line_end":   float64(5),
			"content":    "Consider adding error handling here.",
		}).
		// Final response
		AddAssistantText("Review complete. I found 1 issue.").
		// Result - add small delay to allow MCP tool handlers to complete
		// The SDK processes MCP tool calls asynchronously
		AddResultWithDelay(sessionID, 50*time.Millisecond).
		Build()

	// Enable auto-response to SDK control requests
	// "attn-reviewer" matches the MCP server name in reviewer.go
	mt.EnableAutoControlResponse(sessionID, "attn-reviewer")
	return mt
}

func TestReviewer_FullFlow(t *testing.T) {
	repoPath := createTestRepo(t)
	testStore := createTestStore(t)
	defer testStore.Close()

	reviewID := "test-review-123"
	mockTransport := buildMockTransportForReview("session-" + reviewID)

	reviewer := New(testStore).WithTransport(mockTransport)

	config := ReviewConfig{
		RepoPath:   repoPath,
		Branch:     "main",
		BaseBranch: "main",
		ReviewID:   reviewID,
	}

	// Collect events
	var events []ReviewEvent
	var mu sync.Mutex

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := reviewer.Run(ctx, config, func(event ReviewEvent) {
		mu.Lock()
		t.Logf("Event: type=%s content=%q finding=%v", event.Type, event.Content, event.Finding != nil)
		events = append(events, event)
		mu.Unlock()
	})

	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Wait for async control requests to complete
	// The SDK processes MCP tool calls asynchronously, so finding events
	// may arrive after the result message. Poll instead of fixed sleep.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		hasFinding := false
		for _, e := range events {
			if e.Type == "finding" {
				hasFinding = true
				break
			}
		}
		mu.Unlock()
		if hasFinding {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Verify events
	mu.Lock()
	defer mu.Unlock()

	if len(events) == 0 {
		t.Fatal("Expected events, got none")
	}

	// First event should be "started"
	if events[0].Type != "started" {
		t.Errorf("First event should be 'started', got %s", events[0].Type)
	}

	// Should have a "complete" event (may not be last due to async MCP tool processing)
	hasComplete := false
	var completeEvent ReviewEvent
	for _, e := range events {
		if e.Type == "complete" {
			hasComplete = true
			completeEvent = e
			break
		}
	}
	if !hasComplete {
		t.Error("Expected a complete event")
	} else if !completeEvent.Success {
		t.Error("Complete event should have Success=true")
	}

	// Should have chunk events
	hasChunk := false
	for _, e := range events {
		if e.Type == "chunk" {
			hasChunk = true
			break
		}
	}
	if !hasChunk {
		t.Error("Expected at least one chunk event")
	}

	// Should have a finding event (from add_comment tool call)
	hasFinding := false
	for _, e := range events {
		if e.Type == "finding" {
			hasFinding = true
			if e.Finding == nil {
				t.Error("Finding event should have Finding data")
			} else {
				if e.Finding.Filepath != "main.go" {
					t.Errorf("Finding filepath should be 'main.go', got %s", e.Finding.Filepath)
				}
				if e.Finding.LineStart != 5 {
					t.Errorf("Finding LineStart should be 5, got %d", e.Finding.LineStart)
				}
			}
			break
		}
	}
	if !hasFinding {
		t.Error("Expected a finding event")
	}

	// Verify comment was created in store
	comments, err := testStore.GetComments(reviewID)
	if err != nil {
		t.Fatalf("GetComments failed: %v", err)
	}
	if len(comments) != 1 {
		t.Errorf("Expected 1 comment in store, got %d", len(comments))
	}
	if len(comments) > 0 {
		if comments[0].Author != "agent" {
			t.Errorf("Comment author should be 'agent', got %s", comments[0].Author)
		}
	}
}

func TestReviewer_Cancellation(t *testing.T) {
	repoPath := createTestRepo(t)
	testStore := createTestStore(t)
	defer testStore.Close()

	// Create a long-running sequence
	mockTransport := transport.CancelableSequence("session-cancel", 50, 100*time.Millisecond)
	mockTransport.EnableAutoControlResponse("session-cancel", "attn-reviewer")

	reviewer := New(testStore).WithTransport(mockTransport)

	config := ReviewConfig{
		RepoPath:   repoPath,
		Branch:     "main",
		BaseBranch: "main",
		ReviewID:   "cancel-review",
	}

	var events []ReviewEvent
	var mu sync.Mutex

	ctx, cancel := context.WithCancel(context.Background())

	// Run in goroutine
	done := make(chan error, 1)
	go func() {
		done <- reviewer.Run(ctx, config, func(event ReviewEvent) {
			mu.Lock()
			events = append(events, event)
			mu.Unlock()

			// Cancel after a few events
			if len(events) >= 3 {
				cancel()
			}
		})
	}()

	// Wait for completion with timeout
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("Expected context.Canceled error, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Test timed out")
	}

	// Verify we got a cancelled event
	mu.Lock()
	hasCancelled := false
	for _, e := range events {
		if e.Type == "cancelled" {
			hasCancelled = true
			break
		}
	}
	mu.Unlock()

	if !hasCancelled {
		t.Error("Expected a cancelled event")
	}

	// Reviewer should not be running
	if reviewer.IsRunning() {
		t.Error("Reviewer should not be running after cancellation")
	}
}

func TestReviewer_ErrorHandling(t *testing.T) {
	repoPath := createTestRepo(t)
	testStore := createTestStore(t)
	defer testStore.Close()

	// Test connection error - simpler than mid-stream errors
	mockTransport := transport.NewMockTransport()
	mockTransport.SetConnectError(&transport.TransportError{Message: "connection refused"})

	reviewer := New(testStore).WithTransport(mockTransport)

	config := ReviewConfig{
		RepoPath:   repoPath,
		Branch:     "main",
		BaseBranch: "main",
		ReviewID:   "error-review",
	}

	var events []ReviewEvent
	var mu sync.Mutex

	ctx := context.Background()
	err := reviewer.Run(ctx, config, func(event ReviewEvent) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	})

	// Should fail with connection error
	if err == nil {
		t.Error("Expected error, got nil")
	}

	// Should have started event and error event
	mu.Lock()
	defer mu.Unlock()

	if len(events) < 2 {
		t.Fatalf("Expected at least 2 events (started + error), got %d", len(events))
	}

	// First should be started
	if events[0].Type != "started" {
		t.Errorf("First event should be 'started', got %s", events[0].Type)
	}

	// Should have an error event
	hasError := false
	for _, e := range events {
		if e.Type == "error" {
			hasError = true
			break
		}
	}
	if !hasError {
		t.Error("Expected an error event")
	}
}

func TestReviewer_PreventConcurrentRuns(t *testing.T) {
	repoPath := createTestRepo(t)
	testStore := createTestStore(t)
	defer testStore.Close()

	// Long-running mock
	mockTransport := transport.CancelableSequence("session-concurrent", 100, 50*time.Millisecond)
	mockTransport.EnableAutoControlResponse("session-concurrent", "attn-reviewer")

	reviewer := New(testStore).WithTransport(mockTransport)

	config := ReviewConfig{
		RepoPath:   repoPath,
		Branch:     "main",
		BaseBranch: "main",
		ReviewID:   "concurrent-review",
	}

	// Start first review in background
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan bool)
	go func() {
		started <- true
		reviewer.Run(ctx, config, func(event ReviewEvent) {})
	}()

	<-started
	time.Sleep(50 * time.Millisecond) // Let it start

	// Try to start second review
	err := reviewer.Run(context.Background(), config, func(event ReviewEvent) {})
	if err == nil {
		t.Error("Expected error when starting concurrent review")
	}
}
