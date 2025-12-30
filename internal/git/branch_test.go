// internal/git/branch_test.go
package git

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestListRemoteBranches(t *testing.T) {
	// This test requires a repo with remotes, skip in CI
	if os.Getenv("CI") != "" {
		t.Skip("Skipping remote branch test in CI")
	}

	// Use current repo as test subject
	dir, _ := os.Getwd()
	branches, err := ListRemoteBranches(dir)
	if err != nil {
		t.Fatalf("ListRemoteBranches failed: %v", err)
	}
	// Just verify it returns without error and returns a slice
	t.Logf("Found %d remote branches", len(branches))
}

func TestCheckoutRemoteBranch(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "Test")

	// Create initial commit
	writeFile(t, dir, "file.txt", "initial")
	runGit(t, dir, "add", "file.txt")
	runGit(t, dir, "commit", "-m", "initial")

	// Create a branch to simulate remote
	runGit(t, dir, "branch", "feature-x")

	// CheckoutRemoteBranch should work for local branches too
	err := CheckoutBranch(dir, "feature-x")
	if err != nil {
		t.Fatalf("CheckoutBranch failed: %v", err)
	}

	// Verify we're on the branch
	branch, _ := GetCurrentBranch(dir)
	if branch != "feature-x" {
		t.Errorf("Expected branch 'feature-x', got %q", branch)
	}
}

func TestListBranchesWithCommits(t *testing.T) {
	// Create temp git repo
	mainDir := t.TempDir()
	runGit(t, mainDir, "init")
	runGit(t, mainDir, "config", "user.email", "test@test.com")
	runGit(t, mainDir, "config", "user.name", "Test")
	runGit(t, mainDir, "checkout", "-b", "main") // Ensure branch is named 'main'

	// Create initial commit on main
	writeFile(t, mainDir, "file1.txt", "initial")
	runGit(t, mainDir, "add", "file1.txt")
	runGit(t, mainDir, "commit", "-m", "initial commit")

	// Create feature-a branch
	runGit(t, mainDir, "branch", "feature-a")

	// Create feature-b branch and add a commit
	runGit(t, mainDir, "checkout", "-b", "feature-b")
	writeFile(t, mainDir, "file2.txt", "feature b")
	runGit(t, mainDir, "add", "file2.txt")
	runGit(t, mainDir, "commit", "-m", "add feature b")

	// Switch back to main
	runGit(t, mainDir, "checkout", "main")

	// Create a worktree for feature-a (should be excluded from results)
	wtDir := filepath.Join(t.TempDir(), "wt")
	runGit(t, mainDir, "worktree", "add", wtDir, "feature-a")

	// List branches with commits
	branches, err := ListBranchesWithCommits(mainDir)
	if err != nil {
		t.Fatalf("ListBranchesWithCommits failed: %v", err)
	}

	// Should only have feature-b (main is current, feature-a is checked out in worktree)
	if len(branches) != 1 {
		t.Fatalf("Expected 1 branch, got %d: %+v", len(branches), branches)
	}

	featureBBranch := branches[0]
	if featureBBranch.Name != "feature-b" {
		t.Fatalf("Expected feature-b, got %q", featureBBranch.Name)
	}

	// Verify feature-b is not current
	if featureBBranch.IsCurrent {
		t.Error("Expected feature-b branch to not be marked as current")
	}

	// Verify commit hash is present and short (7 chars)
	if len(featureBBranch.CommitHash) != 7 {
		t.Errorf("Expected 7-char commit hash for feature-b, got %q", featureBBranch.CommitHash)
	}

	// Verify commit time is ISO timestamp
	_, err = time.Parse(time.RFC3339, featureBBranch.CommitTime)
	if err != nil {
		t.Errorf("Expected ISO timestamp for feature-b commit time, got %q: %v", featureBBranch.CommitTime, err)
	}
}
