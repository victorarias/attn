package mcp

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/store"
)

// createTestRepo creates a temporary git repo with modified files for testing
func createTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// Initialize git repo
	gitEnv := append(os.Environ(), "GIT_TEMPLATE_DIR=")
	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = gitEnv
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
		cmd.Env = gitEnv
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

	// Create a new file (untracked)
	newFile := filepath.Join(dir, "handler.go")
	if err := os.WriteFile(newFile, []byte("package main\n\nfunc handler() {}\n"), 0644); err != nil {
		t.Fatalf("Failed to write new file: %v", err)
	}

	return dir
}

// createTestStore creates a SQLite store for testing
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

func TestGetChangedFiles(t *testing.T) {
	repoPath := createTestRepo(t)
	s := createTestStore(t)
	defer s.Close()

	tools := NewTools(repoPath, "test-review", "HEAD", s)

	files, err := tools.GetChangedFiles()
	if err != nil {
		t.Fatalf("GetChangedFiles failed: %v", err)
	}

	if len(files) != 2 {
		t.Errorf("Expected 2 changed files, got %d: %+v", len(files), files)
	}

	// Check we have the expected files
	fileMap := make(map[string]string)
	for _, f := range files {
		fileMap[f.Path] = f.Status
	}

	if status, ok := fileMap["example.go"]; !ok || status != "modified" {
		t.Errorf("Expected example.go with status 'modified', got %v", fileMap)
	}
	if status, ok := fileMap["handler.go"]; !ok || status != "untracked" {
		t.Errorf("Expected handler.go with status 'untracked', got %v", fileMap)
	}
}

func TestGetDiff(t *testing.T) {
	repoPath := createTestRepo(t)
	s := createTestStore(t)
	defer s.Close()

	tools := NewTools(repoPath, "test-review", "HEAD", s)

	// Test getting diff for specific file
	diffs, err := tools.GetDiff([]string{"example.go"})
	if err != nil {
		t.Fatalf("GetDiff failed: %v", err)
	}

	if len(diffs) != 1 {
		t.Errorf("Expected 1 diff, got %d", len(diffs))
	}

	diff, ok := diffs["example.go"]
	if !ok {
		t.Fatal("Expected diff for example.go")
	}
	if diff == "" {
		t.Error("Expected non-empty diff")
	}

	// Test getting all diffs (empty paths)
	allDiffs, err := tools.GetDiff(nil)
	if err != nil {
		t.Fatalf("GetDiff (all) failed: %v", err)
	}

	// Expect diffs for modified and untracked files
	if len(allDiffs) != 2 {
		t.Errorf("Expected 2 diffs for all files, got %d: %+v", len(allDiffs), allDiffs)
	}
}

func TestCommentOperations(t *testing.T) {
	repoPath := createTestRepo(t)
	s := createTestStore(t)
	defer s.Close()

	reviewID := "test-review-123"
	tools := NewTools(repoPath, reviewID, "HEAD", s)

	// Initially no comments
	comments, err := tools.ListComments()
	if err != nil {
		t.Fatalf("ListComments failed: %v", err)
	}
	if len(comments) != 0 {
		t.Errorf("Expected 0 comments, got %d", len(comments))
	}

	// Add a comment
	comment, err := tools.AddComment("example.go", 5, 5, "Consider adding error handling")
	if err != nil {
		t.Fatalf("AddComment failed: %v", err)
	}
	if comment.Author != "agent" {
		t.Errorf("Expected author 'agent', got '%s'", comment.Author)
	}
	if comment.Filepath != "example.go" {
		t.Errorf("Expected filepath 'example.go', got '%s'", comment.Filepath)
	}

	// List comments - should have one
	comments, err = tools.ListComments()
	if err != nil {
		t.Fatalf("ListComments failed: %v", err)
	}
	if len(comments) != 1 {
		t.Errorf("Expected 1 comment, got %d", len(comments))
	}

	// Resolve the comment
	err = tools.ResolveComment(comment.ID)
	if err != nil {
		t.Fatalf("ResolveComment failed: %v", err)
	}

	// Check it's resolved by agent
	comments, err = tools.ListComments()
	if err != nil {
		t.Fatalf("ListComments failed: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("Expected 1 comment, got %d", len(comments))
	}
	if !comments[0].Resolved {
		t.Error("Comment should be resolved")
	}
	if comments[0].ResolvedBy != "agent" {
		t.Errorf("Expected ResolvedBy 'agent', got '%s'", comments[0].ResolvedBy)
	}
	if comments[0].ResolvedAt == nil {
		t.Error("ResolvedAt should not be nil")
	}
}
