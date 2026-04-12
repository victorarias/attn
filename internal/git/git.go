// internal/git/git.go
package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func CanonicalizePath(path string) string {
	expanded := ExpandPath(path)
	if resolved, err := filepath.EvalSymlinks(expanded); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(expanded)
}

// BranchInfo contains git branch and worktree information for a directory
type BranchInfo struct {
	Branch     string // Current branch name, or short SHA if detached
	IsWorktree bool   // True if directory is a git worktree (not main repo)
	MainRepo   string // Path to main repo if IsWorktree, empty otherwise
}

// GetBranchInfo returns branch information for a directory.
// Returns empty BranchInfo (no error) if not a git repo.
func GetBranchInfo(dir string) (*BranchInfo, error) {
	info := &BranchInfo{}

	// Check if it's a git repo
	if !isGitRepo(dir) {
		return info, nil
	}

	// Get current branch
	branch, err := getCurrentBranch(dir)
	if err != nil {
		return info, nil
	}
	info.Branch = branch

	// Check if worktree
	mainRepo, isWT := getWorktreeInfo(dir)
	info.IsWorktree = isWT
	info.MainRepo = mainRepo

	return info, nil
}

func isGitRepo(dir string) bool {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = dir
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

func getCurrentBranch(dir string) (string, error) {
	// Try symbolic-ref first (works for normal branches)
	cmd := exec.Command("git", "symbolic-ref", "--short", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}

	// Fallback to rev-parse for detached HEAD (returns short SHA)
	cmd = exec.Command("git", "rev-parse", "--short", "HEAD")
	cmd.Dir = dir
	out, err = cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// GetRepoRoot returns the worktree root for dir when it is inside a git worktree.
func GetRepoRoot(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return CanonicalizePath(strings.TrimSpace(string(out))), nil
}

func sameDirectory(left string, right string) bool {
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	if leftErr != nil || rightErr != nil {
		return filepath.Clean(left) == filepath.Clean(right)
	}
	return os.SameFile(leftInfo, rightInfo)
}

// ResolvePickerRepoTarget returns the main repo path to use for the location picker
// when dir is exactly a repo root or worktree root. Subdirectories inside a repo
// return ok=false so the picker can open them directly instead of rewriting them.
func ResolvePickerRepoTarget(dir string) (repoRoot string, ok bool, err error) {
	resolvedDir := CanonicalizePath(dir)
	worktreeRoot, err := GetRepoRoot(resolvedDir)
	if err != nil || worktreeRoot == "" {
		return "", false, nil
	}
	if !sameDirectory(resolvedDir, worktreeRoot) {
		return "", false, nil
	}
	if mainRepo := GetMainRepoFromWorktree(resolvedDir); mainRepo != "" {
		return CanonicalizePath(mainRepo), true, nil
	}
	return resolvedDir, true, nil
}

// GetHeadCommit returns the full SHA of the HEAD commit
func GetHeadCommit(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func getWorktreeInfo(dir string) (mainRepo string, isWorktree bool) {
	// Get the git dir
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	gitDir := strings.TrimSpace(string(out))

	// If git dir contains "worktrees", it's a worktree
	if strings.Contains(gitDir, "worktrees") {
		// Extract main repo path from gitdir file
		// Worktree git dir is like: /path/to/main/.git/worktrees/name
		// Main repo is: /path/to/main
		parts := strings.Split(gitDir, ".git/worktrees")
		if len(parts) > 0 {
			mainRepo = strings.TrimSuffix(parts[0], "/")
			if !filepath.IsAbs(mainRepo) {
				mainRepo = filepath.Join(dir, mainRepo)
			}
			mainRepo = filepath.Clean(mainRepo)
		}
		return mainRepo, true
	}

	return "", false
}
