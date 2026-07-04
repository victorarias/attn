package git

import (
	"fmt"
	"os"
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
	out, err := runGitOutput(OpMetadata, path, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return repoNameFromRemote(strings.TrimSpace(string(out)))
}

// OriginOwnerRepo returns the "owner/name" slug for path's origin remote, or
// "" if path is not a git repo, has no origin, or the remote URL cannot be
// parsed into owner/name. Handles the common GitHub remote forms:
// git@github.com:owner/name.git, ssh://git@github.com/owner/name.git,
// https://github.com/owner/name.git, and trailing-.git-less variants.
func OriginOwnerRepo(path string) string {
	out, err := runGitOutput(OpMetadata, path, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return ownerRepoFromRemote(strings.TrimSpace(string(out)))
}

// ownerRepoFromRemote parses a git remote URL into an "owner/name" slug.
func ownerRepoFromRemote(remote string) string {
	if remote == "" {
		return ""
	}
	remote = strings.TrimSuffix(remote, ".git")

	if idx := strings.Index(remote, "://"); idx >= 0 {
		// URL form (ssh://, https://, git://, ...): strip scheme + host.
		rest := remote[idx+3:]
		slashIdx := strings.Index(rest, "/")
		if slashIdx < 0 {
			return ""
		}
		remote = rest[slashIdx+1:]
	} else if colonIdx := strings.Index(remote, ":"); colonIdx > 0 {
		// scp-like form: git@github.com:owner/name
		remote = remote[colonIdx+1:]
	}

	remote = strings.Trim(remote, "/")
	parts := strings.Split(remote, "/")
	if len(parts) < 2 {
		return ""
	}
	owner := parts[len(parts)-2]
	name := parts[len(parts)-1]
	if owner == "" || name == "" {
		return ""
	}
	return owner + "/" + name
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
