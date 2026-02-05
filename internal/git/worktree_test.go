// internal/git/worktree_test.go
package git

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListWorktrees(t *testing.T) {
	tmpDir := t.TempDir()
	mainDir := filepath.Join(tmpDir, "main")
	if err := os.MkdirAll(mainDir, 0755); err != nil {
		t.Fatalf("Failed to create main dir: %v", err)
	}
	runGit(t, mainDir, "init")
	runGit(t, mainDir, "commit", "--allow-empty", "-m", "init")

	// Create a worktree
	wtDir := filepath.Join(tmpDir, "wt")
	runGit(t, mainDir, "worktree", "add", "-b", "feature", wtDir)

	worktrees, err := ListWorktrees(mainDir)
	if err != nil {
		t.Fatalf("ListWorktrees failed: %v", err)
	}

	// Should have 2: main + worktree
	if len(worktrees) < 1 {
		t.Errorf("expected at least 1 worktree, got %d", len(worktrees))
	}

	// Find the feature worktree
	found := false
	for _, wt := range worktrees {
		if wt.Branch == "feature" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to find feature worktree")
	}
}

func TestCreateWorktree(t *testing.T) {
	tmpDir := t.TempDir()
	mainDir := filepath.Join(tmpDir, "main")
	if err := os.MkdirAll(mainDir, 0755); err != nil {
		t.Fatalf("Failed to create main dir: %v", err)
	}
	runGit(t, mainDir, "init")
	runGit(t, mainDir, "commit", "--allow-empty", "-m", "init")

	wtDir := filepath.Join(tmpDir, "new-wt")
	err := CreateWorktree(mainDir, "new-feature", wtDir)
	if err != nil {
		t.Fatalf("CreateWorktree failed: %v", err)
	}

	// Verify worktree exists
	if _, err := os.Stat(wtDir); os.IsNotExist(err) {
		t.Error("worktree directory was not created")
	}

	// Verify branch
	info, err := GetBranchInfo(wtDir)
	if err != nil {
		t.Fatalf("GetBranchInfo failed: %v", err)
	}
	if info.Branch != "new-feature" {
		t.Errorf("expected branch new-feature, got %s", info.Branch)
	}
}

func TestDeleteWorktree(t *testing.T) {
	tmpDir := t.TempDir()
	mainDir := filepath.Join(tmpDir, "main")
	if err := os.MkdirAll(mainDir, 0755); err != nil {
		t.Fatalf("Failed to create main dir: %v", err)
	}
	runGit(t, mainDir, "init")
	runGit(t, mainDir, "commit", "--allow-empty", "-m", "init")

	wtDir := filepath.Join(tmpDir, "wt-to-delete")
	runGit(t, mainDir, "worktree", "add", "-b", "temp", wtDir)

	err := DeleteWorktree(mainDir, wtDir)
	if err != nil {
		t.Fatalf("DeleteWorktree failed: %v", err)
	}

	// Directory might still exist but shouldn't be a worktree
	worktrees, _ := ListWorktrees(mainDir)
	for _, wt := range worktrees {
		if wt.Path == wtDir {
			t.Error("worktree should have been removed")
		}
	}
}

func TestGenerateWorktreePath(t *testing.T) {
	tests := []struct {
		mainRepo string
		branch   string
		expected string
	}{
		{"/Users/me/projects/repo", "feature", "/Users/me/projects/repo--feature"},
		{"/Users/me/projects/repo", "fix/bug-123", "/Users/me/projects/repo--fix-bug-123"},
	}

	for _, tt := range tests {
		got := GenerateWorktreePath(tt.mainRepo, tt.branch)
		if got != tt.expected {
			t.Errorf("GenerateWorktreePath(%q, %q) = %q, want %q", tt.mainRepo, tt.branch, got, tt.expected)
		}
	}
}

func TestResolveMainRepoPath_WithMainRepo(t *testing.T) {
	mainDir := t.TempDir()
	runGit(t, mainDir, "init")
	runGit(t, mainDir, "commit", "--allow-empty", "-m", "init")

	got := ResolveMainRepoPath(mainDir)
	if canonicalPath(got) != canonicalPath(mainDir) {
		t.Errorf("ResolveMainRepoPath(main repo) = %q, want %q", got, mainDir)
	}
}

func TestResolveMainRepoPath_WithWorktree(t *testing.T) {
	tmpDir := t.TempDir()
	mainDir := filepath.Join(tmpDir, "hurdy-gurdy")
	if err := os.MkdirAll(mainDir, 0755); err != nil {
		t.Fatalf("Failed to create main dir: %v", err)
	}
	runGit(t, mainDir, "init")
	runGit(t, mainDir, "commit", "--allow-empty", "-m", "init")

	worktreeDir := filepath.Join(tmpDir, "hurdy-gurdy--feat-auto-bump-yt-dlp--fork-hurdy-gurdy")
	runGit(t, mainDir, "worktree", "add", "-b", "feat/auto-bump-yt-dlp", worktreeDir)

	got := ResolveMainRepoPath(worktreeDir)
	if canonicalPath(got) != canonicalPath(mainDir) {
		t.Errorf("ResolveMainRepoPath(worktree) = %q, want %q", got, mainDir)
	}
}

func canonicalPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(path)
}
