// internal/git/diff.go
package git

import (
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// DiffFileInfo represents a file in the branch diff.
type DiffFileInfo struct {
	Path           string `json:"path"`
	Status         string `json:"status"` // "added", "modified", "deleted", "renamed"
	OldPath        string `json:"old_path,omitempty"`
	Additions      int    `json:"additions,omitempty"`
	Deletions      int    `json:"deletions,omitempty"`
	HasUncommitted bool   `json:"has_uncommitted,omitempty"`
}

// GetBranchDiffFiles returns all files changed between baseRef and HEAD,
// plus any uncommitted changes. This provides a PR-like view of all work
// on the current branch.
func GetBranchDiffFiles(repoDir, baseRef string) ([]DiffFileInfo, error) {
	// Map to track files and their info
	fileMap := make(map[string]*DiffFileInfo)

	// 1. Get committed changes: git diff --name-status baseRef...HEAD
	statusCmd := exec.Command("git", "diff", "--name-status", baseRef+"...HEAD")
	statusCmd.Dir = repoDir
	statusOut, err := statusCmd.Output()
	if err != nil {
		// If baseRef doesn't exist or there's no merge-base, this will fail
		// That's OK - we'll fall back to just uncommitted changes
		statusOut = []byte{}
	}

	// Parse --name-status output
	// Format: "M\tfile.go" or "R100\told.go\tnew.go" for renames
	if len(statusOut) > 0 {
		lines := strings.Split(strings.TrimSpace(string(statusOut)), "\n")
		for _, line := range lines {
			if line == "" {
				continue
			}
			parts := strings.Split(line, "\t")
			if len(parts) < 2 {
				continue
			}

			statusCode := parts[0]
			path := parts[len(parts)-1] // Last part is always the current path

			info := &DiffFileInfo{
				Path:   path,
				Status: parseGitStatus(statusCode),
			}

			// Handle renames (R100 oldpath newpath)
			if strings.HasPrefix(statusCode, "R") && len(parts) >= 3 {
				info.OldPath = parts[1]
			}

			fileMap[path] = info
		}
	}

	// 2. Get line stats: git diff --numstat baseRef...HEAD
	numstatCmd := exec.Command("git", "diff", "--numstat", baseRef+"...HEAD")
	numstatCmd.Dir = repoDir
	numstatOut, _ := numstatCmd.Output() // Ignore errors, stats are optional

	// Parse --numstat output
	// Format: "10\t5\tfile.go" (additions deletions path)
	if len(numstatOut) > 0 {
		lines := strings.Split(strings.TrimSpace(string(numstatOut)), "\n")
		for _, line := range lines {
			if line == "" {
				continue
			}
			parts := strings.Split(line, "\t")
			if len(parts) < 3 {
				continue
			}

			path := parts[2]
			// For renames, numstat shows: "adds\tdels\told => new" or "adds\tdels\t{old => new}/file"
			// We need to extract the final path
			if strings.Contains(path, " => ") {
				// Handle rename format
				path = extractRenamePath(path)
			}

			if info, ok := fileMap[path]; ok {
				info.Additions, _ = strconv.Atoi(parts[0])
				info.Deletions, _ = strconv.Atoi(parts[1])
			}
		}
	}

	// 3. Get uncommitted changes: git status --porcelain
	porcelainCmd := exec.Command("git", "status", "--porcelain")
	porcelainCmd.Dir = repoDir
	porcelainOut, _ := porcelainCmd.Output()

	uncommittedFiles := make(map[string]bool)
	if len(porcelainOut) > 0 {
		// Don't TrimSpace the entire output - leading space is part of status code!
		// Just trim trailing newline and split
		lines := strings.Split(strings.TrimRight(string(porcelainOut), "\n"), "\n")
		for _, line := range lines {
			if len(line) < 4 { // Need at least "XY " + 1 char path
				continue
			}
			// Format: "XY path" where X=staged, Y=unstaged, followed by single space
			statusXY := line[:2]
			path := line[3:] // Don't TrimSpace - path may have intentional spaces

			// Handle renames in porcelain: "R  old -> new"
			if strings.Contains(path, " -> ") {
				parts := strings.Split(path, " -> ")
				if len(parts) == 2 {
					path = parts[1]
				}
			}

			uncommittedFiles[path] = true

			// If this file is already in the committed diff, mark it
			if info, ok := fileMap[path]; ok {
				info.HasUncommitted = true
			} else {
				// New uncommitted file not in committed diff
				info := &DiffFileInfo{
					Path:           path,
					Status:         parseGitPorcelainStatus(statusXY),
					HasUncommitted: true,
				}
				fileMap[path] = info
			}
		}
	}

	// 4. Get line stats for uncommitted changes
	// For files that are only uncommitted (not in committed diff)
	if len(uncommittedFiles) > 0 {
		unstatsCmd := exec.Command("git", "diff", "--numstat")
		unstatsCmd.Dir = repoDir
		unstatsOut, _ := unstatsCmd.Output()

		if len(unstatsOut) > 0 {
			lines := strings.Split(strings.TrimSpace(string(unstatsOut)), "\n")
			for _, line := range lines {
				if line == "" {
					continue
				}
				parts := strings.Split(line, "\t")
				if len(parts) < 3 {
					continue
				}
				path := parts[2]
				if info, ok := fileMap[path]; ok && info.Additions == 0 && info.Deletions == 0 {
					info.Additions, _ = strconv.Atoi(parts[0])
					info.Deletions, _ = strconv.Atoi(parts[1])
				}
			}
		}

		// Also get staged numstat
		stagedStatsCmd := exec.Command("git", "diff", "--numstat", "--cached")
		stagedStatsCmd.Dir = repoDir
		stagedStatsOut, _ := stagedStatsCmd.Output()

		if len(stagedStatsOut) > 0 {
			lines := strings.Split(strings.TrimSpace(string(stagedStatsOut)), "\n")
			for _, line := range lines {
				if line == "" {
					continue
				}
				parts := strings.Split(line, "\t")
				if len(parts) < 3 {
					continue
				}
				path := parts[2]
				if info, ok := fileMap[path]; ok {
					// Add to existing counts (staged + unstaged)
					adds, _ := strconv.Atoi(parts[0])
					dels, _ := strconv.Atoi(parts[1])
					info.Additions += adds
					info.Deletions += dels
				}
			}
		}
	}

	// Convert map to slice and sort by path for consistent ordering
	result := make([]DiffFileInfo, 0, len(fileMap))
	for _, info := range fileMap {
		result = append(result, *info)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Path < result[j].Path
	})

	return result, nil
}

// parseGitStatus converts git status codes to readable strings.
func parseGitStatus(code string) string {
	// Remove any numbers (for R100 -> R)
	if len(code) > 0 {
		switch code[0] {
		case 'A':
			return "added"
		case 'M':
			return "modified"
		case 'D':
			return "deleted"
		case 'R':
			return "renamed"
		case 'C':
			return "copied"
		case 'T':
			return "typechange"
		}
	}
	return "modified" // Default
}

// parseGitPorcelainStatus converts git status porcelain format to readable strings.
func parseGitPorcelainStatus(xy string) string {
	if len(xy) < 2 {
		return "modified"
	}
	// X is staged status, Y is unstaged
	// Prioritize staged status if present
	x, y := xy[0], xy[1]

	if x == '?' && y == '?' {
		return "untracked"
	}
	if x == 'A' || y == 'A' {
		return "added"
	}
	if x == 'D' || y == 'D' {
		return "deleted"
	}
	if x == 'R' || y == 'R' {
		return "renamed"
	}
	return "modified"
}

// extractRenamePath handles the various rename formats from git numstat.
// Examples:
//   - "old.go => new.go"
//   - "{old => new}/file.go"
//   - "dir/{old.go => new.go}"
func extractRenamePath(path string) string {
	// Simple case: "old.go => new.go"
	if !strings.Contains(path, "{") {
		parts := strings.Split(path, " => ")
		if len(parts) == 2 {
			return strings.TrimSpace(parts[1])
		}
		return path
	}

	// Brace case: "{old => new}/file.go" or "dir/{old.go => new.go}"
	// Find the brace content and extract the new part
	start := strings.Index(path, "{")
	end := strings.Index(path, "}")
	if start >= 0 && end > start {
		braceContent := path[start+1 : end]
		parts := strings.Split(braceContent, " => ")
		if len(parts) == 2 {
			prefix := path[:start]
			suffix := path[end+1:]
			return prefix + strings.TrimSpace(parts[1]) + suffix
		}
	}

	return path
}
