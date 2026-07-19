// internal/git/worktree.go
package git

import (
	"fmt"
	"os"
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
	_ = runGitNoOutput(OpWorktree, repoDir, "worktree", "prune") // Best effort - don't fail if prune fails

	out, err := runGitOutput(OpWorktree, repoDir, "worktree", "list", "--porcelain")
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
			current = WorktreeEntry{Path: CanonicalizePath(strings.TrimPrefix(line, "worktree "))}
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
	if out, err := runGitCombined(OpWorktree, repoDir, "worktree", "add", "-b", branch, CanonicalizePath(path)); err != nil {
		return fmt.Errorf("git worktree add failed: %s", out)
	}
	return nil
}

// CreateWorktreeFromPoint creates a worktree with a new branch starting from a specific ref.
func CreateWorktreeFromPoint(repoDir, branch, path, startingFrom string) error {
	args := []string{"worktree", "add", "-b", branch, CanonicalizePath(path)}
	if startingFrom != "" {
		args = append(args, startingFrom)
	}
	if out, err := runGitCombined(OpWorktree, repoDir, args...); err != nil {
		return fmt.Errorf("git worktree add failed: %s", out)
	}
	return nil
}

// EnsureDetachedWorktreeAtRevision creates or adopts a clean detached worktree
// at one exact commit. Existing worktrees are evidence: a different repository,
// revision, or dirty state is reported and never reset or removed.
func EnsureDetachedWorktreeAtRevision(repoDir, path, revision string) (bool, error) {
	return EnsureDetachedWorktreeAtRevisionWithHTTPAuthorization(repoDir, path, revision, "")
}

func EnsureDetachedWorktreeAtRevisionWithHTTPAuthorization(repoDir, path, revision, authorization string) (bool, error) {
	return ensureDetachedWorktreeAtRevision(repoDir, path, revision, authorization, false)
}

// EnsureAutomationSessionWorktree creates a new exact detached worktree, or
// adopts an existing worktree owned by a previously persisted stable session.
// Once a session has started, its agent is allowed to modify, commit, or switch
// the checkout; recovery still validates that it belongs to the expected main
// repository but must not mistake that ordinary work for corrupt provisioning.
func EnsureAutomationSessionWorktree(repoDir, path, revision, authorization string, sessionPersisted bool) (bool, error) {
	return ensureDetachedWorktreeAtRevision(repoDir, path, revision, authorization, sessionPersisted)
}

func ensureDetachedWorktreeAtRevision(repoDir, path, revision, authorization string, sessionPersisted bool) (bool, error) {
	repoDir = ResolveMainRepoPath(repoDir)
	path = CanonicalizePath(path)
	if info, err := os.Stat(path); err == nil {
		if !info.IsDir() {
			return false, fmt.Errorf("automation worktree path is not a directory: %s", path)
		}
		mainRepo := ResolveMainRepoPath(path)
		if !sameDirectory(mainRepo, repoDir) {
			return false, fmt.Errorf("automation worktree repository mismatch: got %s want %s", mainRepo, repoDir)
		}
		if sessionPersisted {
			return false, nil
		}
		head, err := GetHeadCommit(path)
		if err != nil {
			return false, fmt.Errorf("read automation worktree HEAD: %w", err)
		}
		if !strings.EqualFold(head, revision) {
			return false, fmt.Errorf("automation worktree revision mismatch: got %s want %s", head, revision)
		}
		if branch, err := runGitOutput(OpMetadata, path, "symbolic-ref", "--quiet", "HEAD"); err == nil {
			return false, fmt.Errorf("automation worktree is attached to branch %s; expected detached HEAD", strings.TrimSpace(string(branch)))
		}
		clean, err := IsWorktreeClean(path)
		if err != nil {
			return false, fmt.Errorf("inspect automation worktree: %w", err)
		}
		if !clean {
			return false, fmt.Errorf("automation worktree has local changes: %s", path)
		}
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("inspect automation worktree path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, fmt.Errorf("create automation worktree parent: %w", err)
	}
	// A daemon death after Git wrote worktree metadata but before the directory
	// became durable can leave this exact absent path registered as prunable.
	// Prune only after proving the target is absent, under the caller's repository
	// lock, so retry can converge on the reserved path.
	_ = runGitNoOutput(OpWorktree, repoDir, "worktree", "prune", "--expire", "now")
	remoteURL := ""
	if authorization != "" {
		if out, err := runGitOutput(OpMetadata, repoDir, "remote", "get-url", "origin"); err == nil {
			remoteURL = strings.TrimSpace(string(out))
		}
	}
	if out, err := runGitCombinedWithHTTPAuthorization(OpWorktree, repoDir, remoteURL, authorization, "worktree", "add", "--detach", path, revision); err != nil {
		return false, fmt.Errorf("git worktree add --detach failed: %s", strings.TrimSpace(string(out)))
	}
	return true, nil
}

// CreateWorktreeFromBranch creates a worktree from an existing branch
func CreateWorktreeFromBranch(repoDir, branch, path string) error {
	resolvedDir, err := ResolveRepoDir(repoDir)
	if err != nil {
		return err
	}
	if out, err := runGitCombined(OpWorktree, resolvedDir, "worktree", "add", ExpandPath(path), branch); err != nil {
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
	resolvedDir, err := ResolveRepoDir(repoDir)
	if err != nil {
		return "", err
	}
	if out, err := runGitCombined(OpWorktree, resolvedDir, "worktree", "add", ExpandPath(path), "-b", localBranch, remoteBranch); err != nil {
		return "", fmt.Errorf("git worktree add failed: %s", out)
	}
	return localBranch, nil
}

// DeleteWorktree removes a worktree
// If the worktree directory doesn't exist, it prunes stale entries instead
// Always runs prune after removal to ensure git metadata is fully cleaned
func DeleteWorktree(repoDir, path string, force bool) error {
	path = CanonicalizePath(path)
	// Check if directory exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		// Directory doesn't exist - prune stale worktree entries
		if out, err := runGitCombined(OpWorktree, repoDir, "worktree", "prune"); err != nil {
			return fmt.Errorf("git worktree prune failed: %s", out)
		}
		return nil
	}

	// Directory exists - remove normally
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, path)
	if out, err := runGitCombined(OpWorktree, repoDir, args...); err != nil {
		return fmt.Errorf("git worktree remove failed: %s", out)
	}

	// Always prune after removal to ensure git metadata is fully cleaned
	// This prevents the worktree from reappearing in subsequent list operations
	_ = runGitNoOutput(OpWorktree, repoDir, "worktree", "prune") // Best effort - don't fail if prune fails

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

// IsWorktreeClean reports whether the worktree at path has no uncommitted
// changes, mirroring `git status --porcelain --untracked-files=all` (empty
// output == clean). Untracked files count as changes so a worktree where an agent
// only created new files is correctly seen as dirty. The path itself is used as
// the git CWD, so it works for both the main repo and a linked worktree.
func IsWorktreeClean(path string) (bool, error) {
	out, err := runGitOutput(OpStatus, CanonicalizePath(path), "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return false, err
	}
	return len(strings.TrimSpace(string(out))) == 0, nil
}

// GenerateWorktreePath generates a worktree path as sibling to main repo
func GenerateWorktreePath(mainRepo, branch string) string {
	repoName := filepath.Base(mainRepo)
	safeBranch := strings.ReplaceAll(branch, "/", "-")
	return filepath.Join(filepath.Dir(mainRepo), repoName+"--"+safeBranch)
}

// ResolveMainRepoPath returns the canonical main repository path for a repo path.
// If repoPath points to a worktree, it returns that worktree's main repo path.
// Otherwise it resolves/normalizes the repo path when possible.
func ResolveMainRepoPath(repoPath string) string {
	expanded := ExpandPath(repoPath)
	if mainRepo := GetMainRepoFromWorktree(expanded); mainRepo != "" {
		return filepath.Clean(mainRepo)
	}

	resolved, err := ResolveRepoDir(expanded)
	if err == nil {
		return resolved
	}

	return filepath.Clean(expanded)
}
