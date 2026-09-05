package git

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type WorktreeState struct {
	Path     string
	Branch   string
	HeadSHA  string
	Detached bool
	Locked   bool
	Prunable bool
}

// Deliberately does NOT prune first, unlike ListWorktrees: a prunable worktree is
// shown as stale rather than vanishing behind the user's back.
func ListWorktreeStates(repoDir string) ([]WorktreeState, error) {
	out, err := runGitOutput(OpWorktree, repoDir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}

	var states []WorktreeState
	var current WorktreeState
	flush := func() {
		if current.Path != "" {
			states = append(states, current)
		}
		current = WorktreeState{}
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			current.Path = CanonicalizePath(strings.TrimPrefix(line, "worktree "))
		case strings.HasPrefix(line, "HEAD "):
			current.HeadSHA = strings.TrimSpace(strings.TrimPrefix(line, "HEAD "))
		case strings.HasPrefix(line, "branch refs/heads/"):
			current.Branch = strings.TrimPrefix(line, "branch refs/heads/")
		case line == "detached":
			current.Detached = true
		case line == "prunable" || strings.HasPrefix(line, "prunable "):
			current.Prunable = true
		case line == "locked" || strings.HasPrefix(line, "locked "):
			current.Locked = true
		}
	}
	flush()
	return states, nil
}

// Untracked files count: 7 of 141 worktrees in the spike were untracked-only.
func WorktreeDirtyCount(path string) (int, error) {
	out, err := runGitOutput(OpStatus, CanonicalizePath(path), "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return 0, err
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return 0, nil
	}
	return len(strings.Split(trimmed, "\n")), nil
}

// 5 ms per branch. Any error reports false: an unresolvable ref never reads as merged.
func IsAncestor(repoDir, commit, base string) bool {
	if commit == "" || base == "" {
		return false
	}
	return runGitNoOutput(OpMetadata, repoDir, "merge-base", "--is-ancestor", commit, base) == nil
}

func CommitsAhead(repoDir, base, ref string) (int, error) {
	out, err := runGitOutput(OpMetadata, repoDir, "rev-list", "--count", base+".."+ref)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

// Every tree hash reachable from base: 24 ms once per repository, then a map lookup.
func TreeHashesOnHistory(repoDir, base string) (map[string]bool, error) {
	out, err := runGitOutput(OpMetadata, repoDir, "rev-list", "--format=%T", base)
	if err != nil {
		return nil, err
	}
	hashes := make(map[string]bool)
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		// rev-list --format prints a "commit <sha>" header line before each formatted line.
		if line == "" || strings.HasPrefix(line, "commit ") {
			continue
		}
		hashes[line] = true
	}
	return hashes, nil
}

func TreeHash(repoDir, ref string) (string, error) {
	out, err := runGitOutput(OpMetadata, repoDir, "rev-parse", ref+"^{tree}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Attributed by the message git writes: "WIP on <branch>:" or "On <branch>:".
func StashCountsByBranch(repoDir string) (map[string]int, error) {
	out, err := runGitOutput(OpMetadata, repoDir, "stash", "list", "--format=%gs")
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int)
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		rest := strings.TrimPrefix(line, "WIP ")
		if !strings.HasPrefix(rest, "On ") && !strings.HasPrefix(rest, "on ") {
			continue
		}
		rest = rest[3:]
		branch, _, found := strings.Cut(rest, ":")
		if !found {
			continue
		}
		branch = strings.TrimSpace(branch)
		if branch != "" {
			counts[branch]++
		}
	}
	return counts, nil
}

func LastCommitTime(dir string) (time.Time, error) {
	out, err := runGitOutput(OpMetadata, dir, "log", "-1", "--format=%cI")
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339, strings.TrimSpace(string(out)))
}

// A stale node_modules or target must not make an abandoned worktree look busy.
var idleWalkSkipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"target":       true,
	"dist":         true,
	"build":        true,
	"vendor":       true,
	".next":        true,
	".venv":        true,
	"__pycache__":  true,
}

// The newest modification time in the tree, 8.9 ms p50 per worktree measured.
func NewestTreeModTime(path string) (time.Time, error) {
	newest := time.Time{}
	err := filepath.WalkDir(path, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			if name == path {
				return err
			}
			return nil
		}
		if entry.IsDir() {
			if name != path && idleWalkSkipDirs[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	if err != nil {
		return time.Time{}, err
	}
	if newest.IsZero() {
		if info, statErr := os.Stat(path); statErr == nil {
			newest = info.ModTime()
		}
	}
	return newest, nil
}
