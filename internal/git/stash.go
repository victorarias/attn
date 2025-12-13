// internal/git/stash.go
package git

import (
	"fmt"
	"os/exec"
	"strings"
)

// Stash creates a git stash with the given message.
// Returns error if stash fails or there's nothing to stash.
func Stash(repoDir, message string) error {
	cmd := exec.Command("git", "stash", "push", "-m", message)
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git stash push failed: %w: %s", err, out)
	}
	return nil
}

// StashPop pops the most recent stash.
func StashPop(repoDir string) error {
	cmd := exec.Command("git", "stash", "pop")
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git stash pop failed: %w: %s", err, out)
	}
	return nil
}

// FindAttnStash looks for a stash created by attn for the given branch.
// Returns (found, stashRef, error).
// Looks for stashes with message "attn: auto-stash before switching to <branch>".
func FindAttnStash(repoDir, branch string) (bool, string, error) {
	// List stashes with their messages
	cmd := exec.Command("git", "stash", "list")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return false, "", fmt.Errorf("git stash list failed: %w", err)
	}

	pattern := fmt.Sprintf("attn: auto-stash before switching to %s", branch)
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		if strings.Contains(line, pattern) {
			// Line format: stash@{0}: On branch: message
			parts := strings.SplitN(line, ":", 2)
			if len(parts) > 0 {
				return true, strings.TrimSpace(parts[0]), nil
			}
		}
	}
	return false, "", nil
}

// IsDirty returns true if the working directory has uncommitted changes.
func IsDirty(repoDir string) (bool, error) {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("git status failed: %w", err)
	}
	return len(strings.TrimSpace(string(out))) > 0, nil
}

// CommitWIP stages all changes and commits with "WIP" message.
func CommitWIP(repoDir string) error {
	// Stage all
	addCmd := exec.Command("git", "add", "-A")
	addCmd.Dir = repoDir
	if out, err := addCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git add -A failed: %w: %s", err, out)
	}

	// Commit
	commitCmd := exec.Command("git", "commit", "-m", "WIP")
	commitCmd.Dir = repoDir
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit failed: %w: %s", err, out)
	}
	return nil
}

// GetDefaultBranch returns the default branch name (main, master, etc).
func GetDefaultBranch(repoDir string) (string, error) {
	// Try to get from remote HEAD
	cmd := exec.Command("git", "symbolic-ref", "refs/remotes/origin/HEAD")
	cmd.Dir = repoDir
	out, err := cmd.Output()
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
		cmd := exec.Command("git", "rev-parse", "--verify", branch)
		cmd.Dir = repoDir
		if err := cmd.Run(); err == nil {
			return branch, nil
		}
	}

	return "main", nil // Default fallback
}
