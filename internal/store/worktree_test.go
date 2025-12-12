// internal/store/worktree_test.go
package store

import (
	"testing"
	"time"
)

func TestWorktreeStore(t *testing.T) {
	store := New()
	defer store.Close()

	wt := &Worktree{
		Path:      "/projects/repo--feature",
		Branch:    "feature/auth",
		MainRepo:  "/projects/repo",
		CreatedAt: time.Now(),
	}

	// Add
	store.AddWorktree(wt)

	// Get
	got := store.GetWorktree(wt.Path)
	if got == nil {
		t.Fatal("expected worktree, got nil")
	}
	if got.Branch != wt.Branch {
		t.Errorf("expected branch %s, got %s", wt.Branch, got.Branch)
	}

	// List by repo
	list := store.ListWorktreesByRepo(wt.MainRepo)
	if len(list) != 1 {
		t.Errorf("expected 1 worktree, got %d", len(list))
	}

	// Remove
	store.RemoveWorktree(wt.Path)
	got = store.GetWorktree(wt.Path)
	if got != nil {
		t.Error("expected nil after remove")
	}
}
