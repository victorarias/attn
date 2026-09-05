package git

import (
	"os"
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

type BranchInfo struct {
	Branch     string
	IsWorktree bool
	MainRepo   string
	// The repository the directory belongs to: the main repository for a
	// worktree, the checkout itself otherwise.
	Repository string
}

func GetBranchInfo(dir string) (*BranchInfo, error) {
	info := &BranchInfo{}

	if !isGitRepo(dir) {
		return info, nil
	}

	branch, err := getCurrentBranch(dir)
	if err != nil {
		return info, nil
	}
	info.Branch = branch

	mainRepo, isWT := getWorktreeInfo(dir)
	info.IsWorktree = isWT
	info.MainRepo = mainRepo
	info.Repository = RepositoryRoot(dir)

	return info, nil
}

func isGitRepo(dir string) bool {
	out, err := runGitOutput(OpMetadata, dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

func getCurrentBranch(dir string) (string, error) {
	out, err := runGitOutput(OpMetadata, dir, "symbolic-ref", "--short", "HEAD")
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}

	out, err = runGitOutput(OpMetadata, dir, "rev-parse", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func GetRepoRoot(dir string) (string, error) {
	out, err := runGitOutput(OpMetadata, dir, "rev-parse", "--show-toplevel")
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

func GetHeadCommit(dir string) (string, error) {
	out, err := runGitOutput(OpMetadata, dir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func getWorktreeInfo(dir string) (mainRepo string, isWorktree bool) {
	out, err := runGitOutput(OpMetadata, dir, "rev-parse", "--git-dir")
	if err != nil {
		return "", false
	}
	gitDir := strings.TrimSpace(string(out))

	if strings.Contains(gitDir, "worktrees") {
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
