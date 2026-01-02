package store

import (
	"os"
	"path/filepath"
	"testing"
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
