// internal/git/worktree.go
package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// WorktreeEntry represents a git worktree from `git worktree list`
type WorktreeEntry struct {
	Path   string
	Branch string
}

// ListWorktrees returns all worktrees for a repository
// Runs prune first to clean up any stale worktree entries
func ListWorktrees(repoDir string) ([]WorktreeEntry, error) {
	// Prune stale worktrees first to ensure clean listing
	pruneCmd := exec.Command("git", "worktree", "prune")
	pruneCmd.Dir = repoDir
	_ = pruneCmd.Run() // Best effort - don't fail if prune fails

	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var worktrees []WorktreeEntry
	var current WorktreeEntry

	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			if current.Path != "" {
				worktrees = append(worktrees, current)
			}
			current = WorktreeEntry{Path: strings.TrimPrefix(line, "worktree ")}
		} else if strings.HasPrefix(line, "branch refs/heads/") {
			current.Branch = strings.TrimPrefix(line, "branch refs/heads/")
		}
	}

	if current.Path != "" {
		worktrees = append(worktrees, current)
	}

	return worktrees, nil
}

// CreateWorktree creates a new worktree with a new branch
func CreateWorktree(repoDir, branch, path string) error {
	cmd := exec.Command("git", "worktree", "add", "-b", branch, path)
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree add failed: %s", out)
	}
	return nil
}

// CreateWorktreeFromBranch creates a worktree from an existing branch
func CreateWorktreeFromBranch(repoDir, branch, path string) error {
	cmd := exec.Command("git", "worktree", "add", path, branch)
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree add failed: %s", out)
	}
	return nil
}

// DeleteWorktree removes a worktree
// If the worktree directory doesn't exist, it prunes stale entries instead
// Always runs prune after removal to ensure git metadata is fully cleaned
func DeleteWorktree(repoDir, path string) error {
	// Check if directory exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		// Directory doesn't exist - prune stale worktree entries
		cmd := exec.Command("git", "worktree", "prune")
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git worktree prune failed: %s", out)
		}
		return nil
	}

	// Directory exists - remove normally
	cmd := exec.Command("git", "worktree", "remove", path)
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree remove failed: %s", out)
	}

	// Always prune after removal to ensure git metadata is fully cleaned
	// This prevents the worktree from reappearing in subsequent list operations
	pruneCmd := exec.Command("git", "worktree", "prune")
	pruneCmd.Dir = repoDir
	_ = pruneCmd.Run() // Best effort - don't fail if prune fails

	return nil
}

// GenerateWorktreePath generates a worktree path as sibling to main repo
func GenerateWorktreePath(mainRepo, branch string) string {
	repoName := filepath.Base(mainRepo)
	safeBranch := strings.ReplaceAll(branch, "/", "-")
	return filepath.Join(filepath.Dir(mainRepo), repoName+"--"+safeBranch)
}
