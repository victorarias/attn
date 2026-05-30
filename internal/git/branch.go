// internal/git/branch.go
package git

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

// BranchWithCommit contains branch name and latest commit info
type BranchWithCommit struct {
	Name       string
	CommitHash string // Short SHA
	CommitTime string // ISO timestamp
	IsCurrent  bool
}

// ToProtocol converts BranchWithCommit to the protocol Branch type.
func (b BranchWithCommit) ToProtocol() protocol.Branch {
	return protocol.Branch{
		Name:       b.Name,
		CommitHash: &b.CommitHash,
		CommitTime: &b.CommitTime,
		IsCurrent:  &b.IsCurrent,
	}
}

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
	out, err := runGitOutput(OpMetadata, repoDir, "branch", "--format=%(refname:short)")
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

// ListBranchesWithCommits returns branches with their latest commit info.
func ListBranchesWithCommits(repoDir string) ([]BranchWithCommit, error) {
	// Get current branch for marking
	currentBranch, _ := GetCurrentBranch(repoDir)

	// Get all local branches with commit info
	// Format: refname:short | committerdate:iso-strict | objectname:short
	out, err := runGitOutput(OpMetadata, repoDir, "branch", "--format=%(refname:short)|%(committerdate:iso-strict)|%(objectname:short)")
	if err != nil {
		return nil, fmt.Errorf("git branch failed: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 1 && lines[0] == "" {
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

	var result []BranchWithCommit
	for _, line := range lines {
		parts := strings.Split(line, "|")
		if len(parts) < 3 {
			continue
		}
		name := parts[0]
		// Skip branches checked out in worktrees
		if checkedOut[name] {
			continue
		}
		result = append(result, BranchWithCommit{
			Name:       name,
			CommitTime: parts[1],
			CommitHash: parts[2],
			IsCurrent:  name == currentBranch,
		})
	}

	return result, nil
}

// DeleteBranch deletes a local branch.
// If force is true, uses -D (force delete even if not merged).
// Otherwise uses -d (safe delete, fails if not merged).
func DeleteBranch(repoDir, branch string, force bool) error {
	flag := "-d"
	if force {
		flag = "-D"
	}

	if out, err := runGitCombined(OpMetadata, repoDir, "branch", flag, branch); err != nil {
		return fmt.Errorf("git branch %s failed: %s", flag, out)
	}
	return nil
}

// SwitchBranch switches the repository to a different branch.
// Uses: git checkout <branch>
func SwitchBranch(repoDir, branch string) error {
	if out, err := runGitCombined(OpWorktree, repoDir, "checkout", branch); err != nil {
		return fmt.Errorf("git checkout failed: %s", out)
	}
	return nil
}

// CreateBranch creates a new branch from the current HEAD.
// Uses: git branch <name>
func CreateBranch(repoDir, branch string) error {
	if out, err := runGitCombined(OpMetadata, repoDir, "branch", branch); err != nil {
		return fmt.Errorf("git branch failed: %s", out)
	}
	return nil
}

// GetCurrentBranch returns the current branch name for the repository.
func GetCurrentBranch(repoDir string) (string, error) {
	out, err := runGitOutput(OpMetadata, repoDir, "symbolic-ref", "--short", "HEAD")
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}

	out, err = runGitOutput(OpMetadata, repoDir, "rev-parse", "--short", "HEAD")
	if err != nil {
		return "", fmt.Errorf("git current branch failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ListRemotes returns the configured remote names for the repository.
func ListRemotes(repoDir string) ([]string, error) {
	resolvedDir, err := ResolveRepoDir(repoDir)
	if err != nil {
		return nil, err
	}
	out, err := runGitOutput(OpMetadata, resolvedDir, "remote")
	if err != nil {
		return nil, fmt.Errorf("git remote failed: %w", err)
	}
	var remotes []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			remotes = append(remotes, line)
		}
	}
	return remotes, nil
}

// FetchRemoteBranch fetches a single branch from a remote.
// remote should be e.g. "origin", branch should be e.g. "main".
func FetchRemoteBranch(repoDir, remote, branch string) error {
	resolvedDir, err := ResolveRepoDir(repoDir)
	if err != nil {
		return err
	}
	if out, err := runGitCombined(OpNetwork, resolvedDir, "fetch", remote, branch); err != nil {
		outStr := strings.TrimSpace(string(out))
		if outStr == "" {
			return fmt.Errorf("git fetch failed: %w", err)
		}
		return fmt.Errorf("git fetch failed: %s (%w)", outStr, err)
	}
	return nil
}

// FetchRemotes fetches all remotes with prune.
func FetchRemotes(repoDir string) error {
	resolvedDir, err := ResolveRepoDir(repoDir)
	if err != nil {
		return err
	}
	if out, err := runGitCombined(OpNetwork, resolvedDir, "fetch", "--all", "--prune"); err != nil {
		outStr := strings.TrimSpace(string(out))
		if outStr == "" {
			return fmt.Errorf("git fetch failed: %w", err)
		}
		return fmt.Errorf("git fetch failed: %s (%w)", outStr, err)
	}
	return nil
}

// ListRemoteBranches returns remote branches not checked out locally.
func ListRemoteBranches(repoDir string) ([]string, error) {
	// Get remote branches
	out, err := runGitOutput(OpMetadata, repoDir, "branch", "-r", "--format=%(refname:short)")
	if err != nil {
		return nil, fmt.Errorf("git branch -r failed: %w", err)
	}

	remoteBranches := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(remoteBranches) == 1 && remoteBranches[0] == "" {
		return nil, nil
	}

	// Get local branches
	localOut, err := runGitOutput(OpMetadata, repoDir, "branch", "--format=%(refname:short)")
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
	if _, err := runGitCombined(OpWorktree, repoDir, "checkout", branch); err == nil {
		return nil
	}

	// If that failed, try creating tracking branch from origin
	if out, err := runGitCombined(OpWorktree, repoDir, "checkout", "-b", branch, "origin/"+branch); err != nil {
		return fmt.Errorf("git checkout failed: %s", out)
	}
	return nil
}

// GetHeadCommitInfo returns the short hash and ISO timestamp of HEAD
func GetHeadCommitInfo(repoDir string) (hash string, time string) {
	out, err := runGitOutput(OpMetadata, repoDir, "log", "-1", "--format=%h|%cI")
	if err != nil {
		return "", ""
	}
	parts := strings.Split(strings.TrimSpace(string(out)), "|")
	if len(parts) >= 2 {
		return parts[0], parts[1]
	}
	return "", ""
}

// GetDefaultBranch returns the default branch name (main, master, etc).
func GetDefaultBranch(repoDir string) (string, error) {
	// Try to get from remote HEAD
	out, err := runGitOutput(OpMetadata, repoDir, "symbolic-ref", "refs/remotes/origin/HEAD")
	if err == nil {
		// Format: refs/remotes/origin/main
		ref := strings.TrimSpace(string(out))
		parts := strings.Split(ref, "/")
		if len(parts) > 0 {
			return parts[len(parts)-1], nil
		}
	}

	// Fallback: check if main or master exists
	for _, branch := range []string{"main", "master"} {
		if err := runGitNoOutput(OpMetadata, repoDir, "rev-parse", "--verify", branch); err == nil {
			return branch, nil
		}
	}

	return "main", nil // Default fallback
}
