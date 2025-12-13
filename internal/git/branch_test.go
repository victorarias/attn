// internal/git/branch_test.go
package git

import (
	"os"
	"testing"
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
