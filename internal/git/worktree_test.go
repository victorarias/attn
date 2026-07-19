// internal/git/worktree_test.go
package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureDetachedWorktreeAtRevisionCreateAdoptAndProtectEvidence(t *testing.T) {
	mainDir := filepath.Join(t.TempDir(), "main")
	if err := os.MkdirAll(mainDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, mainDir, "init")
	runGit(t, mainDir, "commit", "--allow-empty", "-m", "init")
	revision, err := GetHeadCommit(mainDir)
	if err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(filepath.Dir(mainDir), "detached")
	created, err := EnsureDetachedWorktreeAtRevision(mainDir, worktree, revision)
	if err != nil || !created {
		t.Fatalf("create = %v, %v", created, err)
	}
	created, err = EnsureDetachedWorktreeAtRevision(mainDir, worktree, revision)
	if err != nil || created {
		t.Fatalf("adopt = %v, %v", created, err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "evidence.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureDetachedWorktreeAtRevision(mainDir, worktree, revision); err == nil || !strings.Contains(err.Error(), "local changes") {
		t.Fatalf("dirty adoption err = %v", err)
	}
	if created, err := EnsureAutomationSessionWorktree(mainDir, worktree, revision, "", true); err != nil || created {
		t.Fatalf("persisted session dirty adoption = %v, %v", created, err)
	}
	if _, err := os.Stat(filepath.Join(worktree, "evidence.txt")); err != nil {
		t.Fatalf("dirty evidence was changed: %v", err)
	}

	attached := filepath.Join(filepath.Dir(mainDir), "attached")
	runGit(t, mainDir, "worktree", "add", "-b", "review-attached", attached, revision)
	if _, err := EnsureDetachedWorktreeAtRevision(mainDir, attached, revision); err == nil || !strings.Contains(err.Error(), "attached to branch") {
		t.Fatalf("attached adoption err = %v", err)
	}
}

func TestEnsureDetachedWorktreeAtRevisionRecoversFreshStaleMetadata(t *testing.T) {
	mainDir := filepath.Join(t.TempDir(), "main")
	if err := os.MkdirAll(mainDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, mainDir, "init")
	runGit(t, mainDir, "commit", "--allow-empty", "-m", "init")
	revision, err := GetHeadCommit(mainDir)
	if err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(filepath.Dir(mainDir), "interrupted")
	runGit(t, mainDir, "worktree", "add", "--detach", worktree, revision)
	if err := os.RemoveAll(worktree); err != nil {
		t.Fatal(err)
	}
	created, err := EnsureDetachedWorktreeAtRevision(mainDir, worktree, revision)
	if err != nil || !created {
		t.Fatalf("fresh stale metadata recovery created=%v err=%v", created, err)
	}
}

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

	err := DeleteWorktree(mainDir, wtDir, false)
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

func TestDeleteWorktreeDirtyRequiresForce(t *testing.T) {
	tmpDir := t.TempDir()
	mainDir := filepath.Join(tmpDir, "main")
	if err := os.MkdirAll(mainDir, 0755); err != nil {
		t.Fatalf("Failed to create main dir: %v", err)
	}
	runGit(t, mainDir, "init")
	runGit(t, mainDir, "commit", "--allow-empty", "-m", "init")

	wtDir := filepath.Join(tmpDir, "dirty-wt")
	runGit(t, mainDir, "worktree", "add", "-b", "dirty", wtDir)
	if err := os.WriteFile(filepath.Join(wtDir, "local.txt"), []byte("local change\n"), 0644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	if err := DeleteWorktree(mainDir, wtDir, false); err == nil {
		t.Fatal("DeleteWorktree without force succeeded on dirty worktree")
	}
	if _, err := os.Stat(wtDir); err != nil {
		t.Fatalf("dirty worktree disappeared after non-force delete: %v", err)
	}

	if err := DeleteWorktree(mainDir, wtDir, true); err != nil {
		t.Fatalf("DeleteWorktree with force failed: %v", err)
	}
	worktrees, err := ListWorktrees(mainDir)
	if err != nil {
		t.Fatalf("ListWorktrees failed: %v", err)
	}
	for _, wt := range worktrees {
		if wt.Path == wtDir {
			t.Fatal("force-deleted worktree should have been removed")
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
