package git

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type WorktreeEntry struct {
	Path   string
	Branch string
}

func ListWorktrees(repoDir string) ([]WorktreeEntry, error) {
	_ = runGitNoOutput(OpWorktree, repoDir, "worktree", "prune")

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

func CreateWorktree(repoDir, branch, path string) error {
	if out, err := runGitCombined(OpWorktree, repoDir, "worktree", "add", "-b", branch, CanonicalizePath(path)); err != nil {
		return fmt.Errorf("git worktree add failed: %s", out)
	}
	return nil
}

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

// An existing worktree is evidence: a different repository, revision, or dirty
// state is reported and never reset or removed.
func EnsureDetachedWorktreeAtRevision(repoDir, path, revision string) (bool, error) {
	return EnsureDetachedWorktreeAtRevisionWithHTTPAuthorization(repoDir, path, revision, "")
}

func EnsureDetachedWorktreeAtRevisionWithHTTPAuthorization(repoDir, path, revision, authorization string) (bool, error) {
	return ensureDetachedWorktreeAtRevision(repoDir, path, revision, authorization, false)
}

// A previously persisted stable session may modify, commit or switch its
// checkout, which recovery must not read as corrupt.
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
	// A daemon death after Git wrote worktree metadata leaves this exact absent path
	// registered as prunable. Prune only after proving the target is absent.
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

func CreateWorktreeFromRemoteBranch(repoDir, remoteBranch, path string) (string, error) {
	localBranch := remoteBranch
	if idx := strings.Index(remoteBranch, "/"); idx != -1 {
		localBranch = remoteBranch[idx+1:]
	}

	resolvedDir, err := ResolveRepoDir(repoDir)
	if err != nil {
		return "", err
	}
	if out, err := runGitCombined(OpWorktree, resolvedDir, "worktree", "add", ExpandPath(path), "-b", localBranch, remoteBranch); err != nil {
		return "", fmt.Errorf("git worktree add failed: %s", out)
	}
	return localBranch, nil
}

func DeleteWorktree(repoDir, path string, force bool) error {
	path = CanonicalizePath(path)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if out, err := runGitCombined(OpWorktree, repoDir, "worktree", "prune"); err != nil {
			return fmt.Errorf("git worktree prune failed: %s", out)
		}
		return nil
	}

	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, path)
	if out, err := runGitCombined(OpWorktree, repoDir, args...); err != nil {
		return fmt.Errorf("git worktree remove failed: %s", out)
	}

	// Always prune, or the worktree reappears in subsequent list operations.
	_ = runGitNoOutput(OpWorktree, repoDir, "worktree", "prune")

	return nil
}

func GetMainRepoFromWorktree(worktreePath string) string {
	gitPath := filepath.Join(worktreePath, ".git")

	info, err := os.Stat(gitPath)
	if err != nil || info.IsDir() {
		return ""
	}

	content, err := os.ReadFile(gitPath)
	if err != nil {
		return ""
	}

	line := strings.TrimSpace(string(content))
	if !strings.HasPrefix(line, "gitdir: ") {
		return ""
	}

	gitdir := strings.TrimPrefix(line, "gitdir: ")

	idx := strings.Index(gitdir, "/.git/worktrees/")
	if idx == -1 {
		return ""
	}

	return gitdir[:idx]
}

// Untracked files count as changes, so a worktree where an agent only created
// files is dirty.
func IsWorktreeClean(path string) (bool, error) {
	out, err := runGitOutput(OpStatus, CanonicalizePath(path), "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return false, err
	}
	return len(strings.TrimSpace(string(out))) == 0, nil
}

func GenerateWorktreePath(mainRepo, branch string) string {
	repoName := filepath.Base(mainRepo)
	safeBranch := strings.ReplaceAll(branch, "/", "-")
	return filepath.Join(filepath.Dir(mainRepo), repoName+"--"+safeBranch)
}

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

// RepositoryRoot names a directory's repository: the main repository for a
// worktree, the checkout itself otherwise. Walks the filesystem, never git.
func RepositoryRoot(dir string) string {
	current := CanonicalizePath(dir)
	for {
		info, err := os.Lstat(filepath.Join(current, ".git"))
		if err == nil {
			if !info.IsDir() {
				if main := GetMainRepoFromWorktree(current); main != "" {
					return CanonicalizePath(main)
				}
			}
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}
