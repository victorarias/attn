// internal/git/branch.go
package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ExpandPath expands ~ to the user's home directory
func ExpandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

// ListBranches returns local branches not checked out in any worktree.
// Uses: git branch --format='%(refname:short)'
func ListBranches(repoDir string) ([]string, error) {
	// Get all local branches
	cmd := exec.Command("git", "branch", "--format=%(refname:short)")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git branch failed: %w", err)
	}

	allBranches := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(allBranches) == 1 && allBranches[0] == "" {
		return nil, nil
	}

	// Get branches that are checked out in worktrees
	worktrees, err := ListWorktrees(repoDir)
	if err != nil {
		return nil, fmt.Errorf("listing worktrees: %w", err)
	}

	checkedOut := make(map[string]bool)
	for _, wt := range worktrees {
		if wt.Branch != "" {
			checkedOut[wt.Branch] = true
		}
	}

	// Filter out branches that are checked out
	var available []string
	for _, branch := range allBranches {
		if !checkedOut[branch] {
			available = append(available, branch)
		}
	}

	return available, nil
}

// DeleteBranch deletes a local branch.
// If force is true, uses -D (force delete even if not merged).
// Otherwise uses -d (safe delete, fails if not merged).
func DeleteBranch(repoDir, branch string, force bool) error {
	flag := "-d"
	if force {
		flag = "-D"
	}

	cmd := exec.Command("git", "branch", flag, branch)
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git branch %s failed: %s", flag, out)
	}
	return nil
}

// SwitchBranch switches the repository to a different branch.
// Uses: git checkout <branch>
func SwitchBranch(repoDir, branch string) error {
	cmd := exec.Command("git", "checkout", branch)
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout failed: %s", out)
	}
	return nil
}

// CreateBranch creates a new branch from the current HEAD.
// Uses: git branch <name>
func CreateBranch(repoDir, branch string) error {
	cmd := exec.Command("git", "branch", branch)
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git branch failed: %s", out)
	}
	return nil
}

// GetCurrentBranch returns the current branch name for the repository.
func GetCurrentBranch(repoDir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// FetchRemotes fetches all remotes with prune.
func FetchRemotes(repoDir string) error {
	cmd := exec.Command("git", "fetch", "--all", "--prune")
	cmd.Dir = ExpandPath(repoDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git fetch failed: %s", out)
	}
	return nil
}

// ListRemoteBranches returns remote branches not checked out locally.
func ListRemoteBranches(repoDir string) ([]string, error) {
	// Get remote branches
	cmd := exec.Command("git", "branch", "-r", "--format=%(refname:short)")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git branch -r failed: %w", err)
	}

	remoteBranches := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(remoteBranches) == 1 && remoteBranches[0] == "" {
		return nil, nil
	}

	// Get local branches
	localCmd := exec.Command("git", "branch", "--format=%(refname:short)")
	localCmd.Dir = repoDir
	localOut, err := localCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git branch failed: %w", err)
	}

	localBranches := make(map[string]bool)
	for _, b := range strings.Split(strings.TrimSpace(string(localOut)), "\n") {
		if b != "" {
			localBranches[b] = true
		}
	}

	// Filter out branches that exist locally, and remove origin/ prefix
	var available []string
	for _, remote := range remoteBranches {
		// Skip HEAD pointer
		if strings.Contains(remote, "HEAD") {
			continue
		}
		// Remove origin/ prefix to get branch name
		name := strings.TrimPrefix(remote, "origin/")
		if !localBranches[name] {
			available = append(available, name)
		}
	}

	return available, nil
}

// CheckoutBranch checks out a branch, creating tracking branch if needed.
func CheckoutBranch(repoDir, branch string) error {
	// First try simple checkout
	cmd := exec.Command("git", "checkout", branch)
	cmd.Dir = repoDir
	if _, err := cmd.CombinedOutput(); err == nil {
		return nil
	}

	// If that failed, try creating tracking branch from origin
	cmd = exec.Command("git", "checkout", "-b", branch, "origin/"+branch)
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout failed: %s", out)
	}
	return nil
}
