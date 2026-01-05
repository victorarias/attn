package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGetOrCreateReview(t *testing.T) {
	// Create temp DB
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()
	defer os.Remove(dbPath)

	// Test creating a new review
	review, err := store.GetOrCreateReview("/path/to/repo", "feature-branch")
	if err != nil {
		t.Fatalf("failed to create review: %v", err)
	}
	if review.ID == "" {
		t.Error("review ID should not be empty")
	}
	if review.Branch != "feature-branch" {
		t.Errorf("expected branch 'feature-branch', got '%s'", review.Branch)
	}
	if review.RepoPath != "/path/to/repo" {
		t.Errorf("expected repo_path '/path/to/repo', got '%s'", review.RepoPath)
	}

	// Test getting the same review (should return existing)
	review2, err := store.GetOrCreateReview("/path/to/repo", "feature-branch")
	if err != nil {
		t.Fatalf("failed to get review: %v", err)
	}
	if review2.ID != review.ID {
		t.Errorf("expected same review ID %s, got %s", review.ID, review2.ID)
	}

	// Test creating different review
	review3, err := store.GetOrCreateReview("/path/to/repo", "other-branch")
	if err != nil {
		t.Fatalf("failed to create other review: %v", err)
	}
	if review3.ID == review.ID {
		t.Error("different branch should create different review")
	}
}

func TestViewedFiles(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()
	defer os.Remove(dbPath)

	// Create a review
	review, err := store.GetOrCreateReview("/repo", "main")
	if err != nil {
		t.Fatalf("failed to create review: %v", err)
	}

	// Initially no viewed files
	files, err := store.GetViewedFiles(review.ID)
	if err != nil {
		t.Fatalf("failed to get viewed files: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 viewed files, got %d", len(files))
	}

	// Mark files as viewed
	if err := store.MarkFileViewed(review.ID, "src/foo.ts"); err != nil {
		t.Fatalf("failed to mark file viewed: %v", err)
	}
	if err := store.MarkFileViewed(review.ID, "src/bar.ts"); err != nil {
		t.Fatalf("failed to mark file viewed: %v", err)
	}

	// Should have 2 viewed files
	files, err = store.GetViewedFiles(review.ID)
	if err != nil {
		t.Fatalf("failed to get viewed files: %v", err)
	}
	if len(files) != 2 {
		t.Errorf("expected 2 viewed files, got %d", len(files))
	}

	// Marking same file again should not create duplicate
	if err := store.MarkFileViewed(review.ID, "src/foo.ts"); err != nil {
		t.Fatalf("failed to re-mark file viewed: %v", err)
	}
	files, err = store.GetViewedFiles(review.ID)
	if err != nil {
		t.Fatalf("failed to get viewed files: %v", err)
	}
	if len(files) != 2 {
		t.Errorf("expected 2 viewed files after re-mark, got %d", len(files))
	}

	// Unmark a file
	if err := store.UnmarkFileViewed(review.ID, "src/foo.ts"); err != nil {
		t.Fatalf("failed to unmark file: %v", err)
	}
	files, err = store.GetViewedFiles(review.ID)
	if err != nil {
		t.Fatalf("failed to get viewed files: %v", err)
	}
	if len(files) != 1 {
		t.Errorf("expected 1 viewed file after unmark, got %d", len(files))
	}

	// Clear all
	if err := store.ClearViewedFiles(review.ID); err != nil {
		t.Fatalf("failed to clear viewed files: %v", err)
	}
	files, err = store.GetViewedFiles(review.ID)
	if err != nil {
		t.Fatalf("failed to get viewed files: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 viewed files after clear, got %d", len(files))
	}
}

func setupTestStore(t *testing.T) *Store {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	return store
}

func TestReviewComments(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	// Create a review first
	review, err := store.GetOrCreateReview("/test/repo", "main")
	if err != nil {
		t.Fatalf("failed to create review: %v", err)
	}

	// Test AddComment
	comment, err := store.AddComment(review.ID, "src/foo.go", 10, 15, "Check null here", "user")
	if err != nil {
		t.Fatalf("failed to add comment: %v", err)
	}
	if comment.ID == "" {
		t.Error("comment ID should not be empty")
	}
	if comment.ReviewID != review.ID {
		t.Errorf("expected review ID %s, got %s", review.ID, comment.ReviewID)
	}
	if comment.Filepath != "src/foo.go" {
		t.Errorf("expected filepath 'src/foo.go', got '%s'", comment.Filepath)
	}
	if comment.LineStart != 10 {
		t.Errorf("expected line_start 10, got %d", comment.LineStart)
	}
	if comment.LineEnd != 15 {
		t.Errorf("expected line_end 15, got %d", comment.LineEnd)
	}
	if comment.Content != "Check null here" {
		t.Errorf("expected content 'Check null here', got '%s'", comment.Content)
	}
	if comment.Author != "user" {
		t.Errorf("expected author 'user', got '%s'", comment.Author)
	}
	if comment.Resolved {
		t.Error("comment should not be resolved by default")
	}

	// Test GetComments
	comments, err := store.GetComments(review.ID)
	if err != nil {
		t.Fatalf("failed to get comments: %v", err)
	}
	if len(comments) != 1 {
		t.Errorf("expected 1 comment, got %d", len(comments))
	}

	// Test GetCommentsForFile
	fileComments, err := store.GetCommentsForFile(review.ID, "src/foo.go")
	if err != nil {
		t.Fatalf("failed to get comments for file: %v", err)
	}
	if len(fileComments) != 1 {
		t.Errorf("expected 1 comment for file, got %d", len(fileComments))
	}

	// Test GetCommentsForFile with non-existent file
	emptyComments, err := store.GetCommentsForFile(review.ID, "nonexistent.go")
	if err != nil {
		t.Fatalf("failed to get comments for non-existent file: %v", err)
	}
	if len(emptyComments) != 0 {
		t.Errorf("expected 0 comments for non-existent file, got %d", len(emptyComments))
	}

	// Test UpdateComment
	err = store.UpdateComment(comment.ID, "Updated content")
	if err != nil {
		t.Fatalf("failed to update comment: %v", err)
	}
	updated, err := store.GetCommentByID(comment.ID)
	if err != nil {
		t.Fatalf("failed to get updated comment: %v", err)
	}
	if updated.Content != "Updated content" {
		t.Errorf("expected content 'Updated content', got '%s'", updated.Content)
	}

	// Test ResolveComment
	err = store.ResolveComment(comment.ID, true, "user")
	if err != nil {
		t.Fatalf("failed to resolve comment: %v", err)
	}
	resolved, err := store.GetCommentByID(comment.ID)
	if err != nil {
		t.Fatalf("failed to get resolved comment: %v", err)
	}
	if !resolved.Resolved {
		t.Error("comment should be resolved")
	}
	if resolved.ResolvedBy != "user" {
		t.Errorf("expected ResolvedBy='user', got '%s'", resolved.ResolvedBy)
	}
	if resolved.ResolvedAt == nil {
		t.Error("ResolvedAt should not be nil")
	}

	// Test unresolving
	err = store.ResolveComment(comment.ID, false, "")
	if err != nil {
		t.Fatalf("failed to unresolve comment: %v", err)
	}
	unresolved, err := store.GetCommentByID(comment.ID)
	if err != nil {
		t.Fatalf("failed to get unresolved comment: %v", err)
	}
	if unresolved.Resolved {
		t.Error("comment should be unresolved")
	}
	if unresolved.ResolvedBy != "" {
		t.Errorf("expected ResolvedBy='', got '%s'", unresolved.ResolvedBy)
	}

	// Test DeleteComment
	err = store.DeleteComment(comment.ID)
	if err != nil {
		t.Fatalf("failed to delete comment: %v", err)
	}
	comments, err = store.GetComments(review.ID)
	if err != nil {
		t.Fatalf("failed to get comments after delete: %v", err)
	}
	if len(comments) != 0 {
		t.Errorf("expected 0 comments after delete, got %d", len(comments))
	}
}

func TestReviewerSessions(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	// Create a review first
	review, err := store.GetOrCreateReview("/test/repo", "feature-branch")
	if err != nil {
		t.Fatalf("failed to create review: %v", err)
	}

	// Test CreateReviewSession
	session, err := store.CreateReviewSession(review.ID, "abc123def456")
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}
	if session.ID == "" {
		t.Error("session ID should not be empty")
	}
	if session.ReviewID != review.ID {
		t.Errorf("expected review ID %s, got %s", review.ID, session.ReviewID)
	}
	if session.CommitSHA != "abc123def456" {
		t.Errorf("expected commit SHA 'abc123def456', got %s", session.CommitSHA)
	}
	if session.StartedAt.IsZero() {
		t.Error("StartedAt should not be zero")
	}

	// Test GetLastReviewSession - should fail (session not completed)
	_, err = store.GetLastReviewSession(review.ID)
	if err == nil {
		t.Error("expected error for incomplete session")
	}

	// Test UpdateReviewSessionTranscript
	transcript := `[{"type": "chunk", "content": "Test review"}]`
	err = store.UpdateReviewSessionTranscript(session.ID, transcript)
	if err != nil {
		t.Fatalf("failed to update transcript: %v", err)
	}

	// Test CompleteReviewSession
	err = store.CompleteReviewSession(session.ID)
	if err != nil {
		t.Fatalf("failed to complete session: %v", err)
	}

	// Test GetLastReviewSession - should succeed now
	lastSession, err := store.GetLastReviewSession(review.ID)
	if err != nil {
		t.Fatalf("failed to get last session: %v", err)
	}
	if lastSession.ID != session.ID {
		t.Errorf("expected session ID %s, got %s", session.ID, lastSession.ID)
	}
	if lastSession.Transcript != transcript {
		t.Errorf("expected transcript %s, got %s", transcript, lastSession.Transcript)
	}
	if lastSession.CompletedAt == nil {
		t.Error("CompletedAt should not be nil")
	}

	// Create another session (with small delay to ensure different completion time)
	time.Sleep(10 * time.Millisecond)
	session2, err := store.CreateReviewSession(review.ID, "def789ghi012")
	if err != nil {
		t.Fatalf("failed to create second session: %v", err)
	}

	transcript2 := `[{"type": "chunk", "content": "Second review"}]`
	err = store.UpdateReviewSessionTranscript(session2.ID, transcript2)
	if err != nil {
		t.Fatalf("failed to update second transcript: %v", err)
	}
	err = store.CompleteReviewSession(session2.ID)
	if err != nil {
		t.Fatalf("failed to complete second session: %v", err)
	}

	// GetLastReviewSession should return the most recent one
	latestSession, err := store.GetLastReviewSession(review.ID)
	if err != nil {
		t.Fatalf("failed to get latest session: %v", err)
	}
	if latestSession.ID != session2.ID {
		t.Errorf("expected latest session ID %s, got %s", session2.ID, latestSession.ID)
	}
	if latestSession.CommitSHA != "def789ghi012" {
		t.Errorf("expected commit SHA 'def789ghi012', got %s", latestSession.CommitSHA)
	}
}
