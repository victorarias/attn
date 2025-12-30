package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

type diffStats struct {
	Additions int
	Deletions int
}

// walkUntrackedDir walks an untracked directory and returns all files,
// filtering out gitignored files using git check-ignore
func walkUntrackedDir(repoDir, dirPath string) []protocol.GitFileChange {
	var files []protocol.GitFileChange
	fullPath := filepath.Join(repoDir, dirPath)

	// Collect all regular files in the directory
	var filePaths []string
	filepath.WalkDir(fullPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // Skip files we can't access
		}
		if !d.IsDir() {
			// Convert to relative path from repo root
			relPath, err := filepath.Rel(repoDir, path)
			if err == nil {
				filePaths = append(filePaths, relPath)
			}
		}
		return nil
	})

	if len(filePaths) == 0 {
		return files
	}

	// Use git check-ignore to filter out ignored files
	// Pass all paths at once for efficiency
	cmd := exec.Command("git", append([]string{"check-ignore", "--stdin"}, []string{}...)...)
	cmd.Dir = repoDir
	cmd.Stdin = strings.NewReader(strings.Join(filePaths, "\n"))
	ignoredOutput, _ := cmd.Output()

	// Build a set of ignored paths
	ignoredSet := make(map[string]bool)
	if len(ignoredOutput) > 0 {
		for _, path := range strings.Split(strings.TrimSpace(string(ignoredOutput)), "\n") {
			if path != "" {
				ignoredSet[path] = true
			}
		}
	}

	// Create GitFileChange for non-ignored files
	for _, path := range filePaths {
		if !ignoredSet[path] {
			files = append(files, protocol.GitFileChange{
				Path:   path,
				Status: "untracked",
			})
		}
	}

	return files
}

// parseGitStatusPorcelain parses `git status --porcelain -z` output
// Format: XY PATH\0 where X=index status, Y=worktree status
func parseGitStatusPorcelain(output string, repoDir string) (staged, unstaged, untracked []protocol.GitFileChange) {
	entries := strings.Split(output, "\x00")

	for _, entry := range entries {
		if len(entry) < 3 {
			continue
		}

		indexStatus := entry[0]
		worktreeStatus := entry[1]
		path := strings.TrimSpace(entry[3:])

		if path == "" {
			continue
		}

		// Untracked files
		if indexStatus == '?' && worktreeStatus == '?' {
			// If path ends with /, it's a directory - expand it
			if strings.HasSuffix(path, "/") {
				files := walkUntrackedDir(repoDir, path)
				untracked = append(untracked, files...)
			} else {
				untracked = append(untracked, protocol.GitFileChange{
					Path:   path,
					Status: "untracked",
				})
			}
			continue
		}

		// Staged changes (index has modification)
		if indexStatus != ' ' && indexStatus != '?' {
			status := statusCodeToString(indexStatus)
			staged = append(staged, protocol.GitFileChange{
				Path:   path,
				Status: status,
			})
		}

		// Unstaged changes (worktree has modification)
		if worktreeStatus != ' ' && worktreeStatus != '?' {
			status := statusCodeToString(worktreeStatus)
			unstaged = append(unstaged, protocol.GitFileChange{
				Path:   path,
				Status: status,
			})
		}
	}

	return staged, unstaged, untracked
}

func statusCodeToString(code byte) string {
	switch code {
	case 'M':
		return "modified"
	case 'A':
		return "added"
	case 'D':
		return "deleted"
	case 'R':
		return "renamed"
	case 'C':
		return "copied"
	default:
		return "modified"
	}
}

// parseGitDiffNumstat parses `git diff --numstat` output
// Format: ADDITIONS\tDELETIONS\tFILENAME
func parseGitDiffNumstat(output string) map[string]diffStats {
	result := make(map[string]diffStats)
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			continue
		}

		// Binary files show "-" for additions/deletions
		additions, _ := strconv.ParseInt(parts[0], 10, 64)
		deletions, _ := strconv.ParseInt(parts[1], 10, 64)
		path := parts[2]

		result[path] = diffStats{
			Additions: int(additions),
			Deletions: int(deletions),
		}
	}

	return result
}

// getGitStatus runs git commands and returns parsed status
func getGitStatus(dir string) (*protocol.GitStatusUpdateMessage, error) {
	// Get porcelain status
	statusCmd := exec.Command("git", "status", "--porcelain", "-z")
	statusCmd.Dir = dir
	statusOutput, err := statusCmd.Output()
	if err != nil {
		return &protocol.GitStatusUpdateMessage{
			Event:     protocol.EventGitStatusUpdate,
			Directory: dir,
			Error:     protocol.Ptr("Not a git repository"),
		}, nil
	}

	staged, unstaged, untracked := parseGitStatusPorcelain(string(statusOutput), dir)

	// Get numstat for unstaged changes
	if len(unstaged) > 0 {
		numstatCmd := exec.Command("git", "diff", "--numstat")
		numstatCmd.Dir = dir
		numstatOutput, _ := numstatCmd.Output()
		stats := parseGitDiffNumstat(string(numstatOutput))

		for i := range unstaged {
			if s, ok := stats[unstaged[i].Path]; ok {
				unstaged[i].Additions = protocol.Ptr(s.Additions)
				unstaged[i].Deletions = protocol.Ptr(s.Deletions)
			}
		}
	}

	// Get numstat for staged changes
	if len(staged) > 0 {
		numstatCmd := exec.Command("git", "diff", "--numstat", "--cached")
		numstatCmd.Dir = dir
		numstatOutput, _ := numstatCmd.Output()
		stats := parseGitDiffNumstat(string(numstatOutput))

		for i := range staged {
			if s, ok := stats[staged[i].Path]; ok {
				staged[i].Additions = protocol.Ptr(s.Additions)
				staged[i].Deletions = protocol.Ptr(s.Deletions)
			}
		}
	}

	return &protocol.GitStatusUpdateMessage{
		Event:     protocol.EventGitStatusUpdate,
		Directory: dir,
		Staged:    staged,
		Unstaged:  unstaged,
		Untracked: untracked,
	}, nil
}

// hashGitStatus returns a hash of the status for change detection
func hashGitStatus(status *protocol.GitStatusUpdateMessage) string {
	data, _ := json.Marshal(status)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:8]) // First 8 bytes is enough
}
