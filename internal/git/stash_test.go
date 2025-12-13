// internal/git/stash_test.go
package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStash(t *testing.T) {
	// Create temp git repo
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "Test")

	// Create and commit a file
	writeFile(t, dir, "file.txt", "initial")
	runGit(t, dir, "add", "file.txt")
	runGit(t, dir, "commit", "-m", "initial")

	// Modify file (create dirty state)
	writeFile(t, dir, "file.txt", "modified")

	// Stash with message
	err := Stash(dir, "attn: test stash")
	if err != nil {
		t.Fatalf("Stash failed: %v", err)
	}

	// Verify file is back to initial
	content := readFile(t, dir, "file.txt")
	if content != "initial" {
		t.Errorf("Expected 'initial', got %q", content)
	}
}

func TestFindAttnStash(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "Test")

	writeFile(t, dir, "file.txt", "initial")
	runGit(t, dir, "add", "file.txt")
	runGit(t, dir, "commit", "-m", "initial")

	// Create stash with attn message (stashing changes FROM main branch)
	writeFile(t, dir, "file.txt", "modified")
	err := Stash(dir, "attn: auto-stash from main")
	if err != nil {
		t.Fatalf("Stash failed: %v", err)
	}

	// Find attn stash for "main" branch (when returning to main)
	found, ref, err := FindAttnStash(dir, "main")
	if err != nil {
		t.Fatalf("FindAttnStash failed: %v", err)
	}
	if !found {
		t.Error("Expected to find attn stash")
	}
	if ref == "" {
		t.Error("Expected non-empty stash ref")
	}
}

func TestStashPop(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "Test")

	writeFile(t, dir, "file.txt", "initial")
	runGit(t, dir, "add", "file.txt")
	runGit(t, dir, "commit", "-m", "initial")

	writeFile(t, dir, "file.txt", "modified")
	Stash(dir, "test")

	err := StashPop(dir)
	if err != nil {
		t.Fatalf("StashPop failed: %v", err)
	}

	content := readFile(t, dir, "file.txt")
	if content != "modified" {
		t.Errorf("Expected 'modified', got %q", content)
	}
}

func TestIsDirty(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "Test")

	// Clean repo (initial commit needed)
	writeFile(t, dir, "file.txt", "initial")
	runGit(t, dir, "add", "file.txt")
	runGit(t, dir, "commit", "-m", "initial")

	dirty, err := IsDirty(dir)
	if err != nil {
		t.Fatalf("IsDirty failed: %v", err)
	}
	if dirty {
		t.Error("Expected clean repo to return false")
	}

	// Modified file
	writeFile(t, dir, "file.txt", "modified")
	dirty, err = IsDirty(dir)
	if err != nil {
		t.Fatalf("IsDirty failed: %v", err)
	}
	if !dirty {
		t.Error("Expected dirty repo (modified file) to return true")
	}

	// Clean up the modification
	runGit(t, dir, "checkout", "file.txt")

	// Untracked file
	writeFile(t, dir, "untracked.txt", "new")
	dirty, err = IsDirty(dir)
	if err != nil {
		t.Fatalf("IsDirty failed: %v", err)
	}
	if !dirty {
		t.Error("Expected dirty repo (untracked file) to return true")
	}
}

func TestCommitWIP(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "Test")

	// Initial commit
	writeFile(t, dir, "file.txt", "initial")
	runGit(t, dir, "add", "file.txt")
	runGit(t, dir, "commit", "-m", "initial")

	// Create modified file
	writeFile(t, dir, "file.txt", "modified")

	err := CommitWIP(dir)
	if err != nil {
		t.Fatalf("CommitWIP failed: %v", err)
	}

	// Verify commit was created with "WIP" message
	cmd := runGitCommand(t, dir, "log", "-1", "--pretty=%s")
	message := strings.TrimSpace(cmd)
	if message != "WIP" {
		t.Errorf("Expected commit message 'WIP', got %q", message)
	}

	// Verify working directory is clean
	dirty, err := IsDirty(dir)
	if err != nil {
		t.Fatalf("IsDirty failed: %v", err)
	}
	if dirty {
		t.Error("Expected clean repo after CommitWIP")
	}
}

func TestGetDefaultBranch(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "Test")

	// Create initial commit
	writeFile(t, dir, "file.txt", "initial")
	runGit(t, dir, "add", "file.txt")
	runGit(t, dir, "commit", "-m", "initial")

	// Test fallback logic when no remote exists
	// Should check for main/master branches and fallback to "main"
	branch, err := GetDefaultBranch(dir)
	if err != nil {
		t.Fatalf("GetDefaultBranch failed: %v", err)
	}
	// Should return "main" as the default fallback
	if branch != "main" && branch != "master" {
		t.Errorf("Expected 'main' or 'master', got %q", branch)
	}
}

func TestFindAttnStash_NoStashes(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "Test")

	// Create initial commit
	writeFile(t, dir, "file.txt", "initial")
	runGit(t, dir, "add", "file.txt")
	runGit(t, dir, "commit", "-m", "initial")

	// Find attn stash when no stashes exist
	found, ref, err := FindAttnStash(dir, "main")
	if err != nil {
		t.Fatalf("FindAttnStash failed: %v", err)
	}
	if found {
		t.Error("Expected found=false when no stashes exist")
	}
	if ref != "" {
		t.Errorf("Expected empty ref when no stashes exist, got %q", ref)
	}
}

// Helper functions for stash tests
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func runGitCommand(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v failed: %v", args, err)
	}
	return string(out)
}
