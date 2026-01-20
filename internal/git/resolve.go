package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ResolveRepoDir verifies repoDir is a git repo and falls back to a one-level
// search under the parent directory (e.g., ~/projects/*/<repo>).
func ResolveRepoDir(repoDir string) (string, error) {
	expanded := ExpandPath(repoDir)
	if isGitRepo(expanded) {
		return expanded, nil
	}

	parent := filepath.Dir(expanded)
	base := filepath.Base(expanded)
	if parent == expanded || base == "" {
		return "", fmt.Errorf("repo path not found: %s", expanded)
	}

	entries, err := os.ReadDir(parent)
	if err != nil {
		return "", fmt.Errorf("repo path not found: %s: %w", expanded, err)
	}

	var originMatches []string
	var noOriginMatches []string
	var originMismatches []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(parent, entry.Name(), base)
		if !isGitRepo(candidate) {
			continue
		}
		originName := originRepoName(candidate)
		switch {
		case originName == base:
			originMatches = append(originMatches, candidate)
		case originName == "":
			noOriginMatches = append(noOriginMatches, candidate)
		default:
			originMismatches = append(originMismatches, candidate)
		}
	}

	switch len(originMatches) {
	case 1:
		return originMatches[0], nil
	case 0:
		switch len(noOriginMatches) {
		case 1:
			return noOriginMatches[0], nil
		case 0:
			if len(originMismatches) > 0 {
				return "", fmt.Errorf("repo path not found: %s (origin mismatch: %s)", expanded, strings.Join(originMismatches, ", "))
			}
			return "", fmt.Errorf("repo path not found: %s", expanded)
		default:
			return "", fmt.Errorf("repo path not found: %s (multiple matches without origin: %s)", expanded, strings.Join(noOriginMatches, ", "))
		}
	default:
		return "", fmt.Errorf("repo path not found: %s (multiple matches: %s)", expanded, strings.Join(originMatches, ", "))
	}
}

func originRepoName(path string) string {
	cmd := exec.Command("git", "-C", path, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return repoNameFromRemote(strings.TrimSpace(string(out)))
}

func repoNameFromRemote(remote string) string {
	if remote == "" {
		return ""
	}
	remote = strings.TrimSuffix(remote, ".git")
	remote = strings.ReplaceAll(remote, ":", "/")
	parts := strings.Split(remote, "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}
