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

// CreateWorktreeFromPoint creates a worktree with a new branch starting from a specific ref.
func CreateWorktreeFromPoint(repoDir, branch, path, startingFrom string) error {
	args := []string{"worktree", "add", "-b", branch, path}
	if startingFrom != "" {
		args = append(args, startingFrom)
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree add failed: %s", out)
	}
	return nil
}

// CreateWorktreeFromBranch creates a worktree from an existing branch
func CreateWorktreeFromBranch(repoDir, branch, path string) error {
	cmd := exec.Command("git", "worktree", "add", ExpandPath(path), branch)
	resolvedDir, err := ResolveRepoDir(repoDir)
	if err != nil {
		return err
	}
	cmd.Dir = resolvedDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree add failed: %s", out)
	}
	return nil
}

// CreateWorktreeFromRemoteBranch creates a worktree with a local branch tracking a remote branch.
// remoteBranch should be in format "origin/branch-name".
// Returns the local branch name that was created.
func CreateWorktreeFromRemoteBranch(repoDir, remoteBranch, path string) (string, error) {
	// Extract local branch name from remote (e.g., "origin/fix-bug" -> "fix-bug")
	localBranch := remoteBranch
	if idx := strings.Index(remoteBranch, "/"); idx != -1 {
		localBranch = remoteBranch[idx+1:]
	}

	// git worktree add <path> -b <local-branch> <remote-branch>
	cmd := exec.Command("git", "worktree", "add", ExpandPath(path), "-b", localBranch, remoteBranch)
	resolvedDir, err := ResolveRepoDir(repoDir)
	if err != nil {
		return "", err
	}
	cmd.Dir = resolvedDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git worktree add failed: %s", out)
	}
	return localBranch, nil
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

// GetMainRepoFromWorktree returns the main repo path for a worktree.
// Worktrees have a .git file (not directory) pointing to the main repo's .git/worktrees/<name>.
// Returns empty string if path is not a worktree or cannot be determined.
func GetMainRepoFromWorktree(worktreePath string) string {
	gitPath := filepath.Join(worktreePath, ".git")

	// Check if .git is a file (worktree) vs directory (main repo)
	info, err := os.Stat(gitPath)
	if err != nil || info.IsDir() {
		return "" // Not a worktree or doesn't exist
	}

	// Read the .git file content (e.g., "gitdir: /path/to/repo/.git/worktrees/branch")
	content, err := os.ReadFile(gitPath)
	if err != nil {
		return ""
	}

	line := strings.TrimSpace(string(content))
	if !strings.HasPrefix(line, "gitdir: ") {
		return ""
	}

	gitdir := strings.TrimPrefix(line, "gitdir: ")
	// gitdir is like: /path/to/main/repo/.git/worktrees/branch-name
	// We need: /path/to/main/repo

	// Find ".git/worktrees/" in the path
	idx := strings.Index(gitdir, "/.git/worktrees/")
	if idx == -1 {
		return ""
	}

	return gitdir[:idx]
}

// GenerateWorktreePath generates a worktree path as sibling to main repo
func GenerateWorktreePath(mainRepo, branch string) string {
	repoName := filepath.Base(mainRepo)
	safeBranch := strings.ReplaceAll(branch, "/", "-")
	return filepath.Join(filepath.Dir(mainRepo), repoName+"--"+safeBranch)
}
