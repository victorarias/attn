// internal/git/git_test.go
package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGetBranchInfo_MainRepo(t *testing.T) {
	// Create temp git repo
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "commit", "--allow-empty", "-m", "init")

	info, err := GetBranchInfo(dir)
	if err != nil {
		t.Fatalf("GetBranchInfo failed: %v", err)
	}
	if info.Branch != "main" && info.Branch != "master" {
		t.Errorf("expected main or master, got %s", info.Branch)
	}
	if info.IsWorktree {
		t.Error("expected IsWorktree=false for main repo")
	}
	if info.MainRepo != "" {
		t.Error("expected MainRepo to be empty for main repo")
	}
}

func TestGetBranchInfo_Worktree(t *testing.T) {
	// Create temp git repo with worktree
	mainDir := t.TempDir()
	runGit(t, mainDir, "init")
	runGit(t, mainDir, "commit", "--allow-empty", "-m", "init")

	wtDir := filepath.Join(t.TempDir(), "wt")
	runGit(t, mainDir, "worktree", "add", "-b", "feature", wtDir)

	info, err := GetBranchInfo(wtDir)
	if err != nil {
		t.Fatalf("GetBranchInfo failed: %v", err)
	}
	if info.Branch != "feature" {
		t.Errorf("expected feature, got %s", info.Branch)
	}
	if !info.IsWorktree {
		t.Error("expected IsWorktree=true for worktree")
	}
	if info.MainRepo == "" {
		t.Error("expected MainRepo to be set for worktree")
	}
}

func TestGetBranchInfo_NotGitRepo(t *testing.T) {
	dir := t.TempDir()
	info, err := GetBranchInfo(dir)
	if err != nil {
		t.Fatalf("GetBranchInfo failed: %v", err)
	}
	if info.Branch != "" {
		t.Errorf("expected empty branch, got %s", info.Branch)
	}
}

func TestGetBranchInfo_DetachedHead(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "commit", "--allow-empty", "-m", "init")
	runGit(t, dir, "checkout", "--detach", "HEAD")

	info, err := GetBranchInfo(dir)
	if err != nil {
		t.Fatalf("GetBranchInfo failed: %v", err)
	}
	// Should return short SHA
	if len(info.Branch) < 7 {
		t.Errorf("expected short SHA for detached HEAD, got %s", info.Branch)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@test.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}
