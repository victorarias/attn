package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"

	"github.com/victorarias/claude-manager/internal/protocol"
)

type diffStats struct {
	Additions int
	Deletions int
}

// parseGitStatusPorcelain parses `git status --porcelain -z` output
// Format: XY PATH\0 where X=index status, Y=worktree status
func parseGitStatusPorcelain(output string) (staged, unstaged, untracked []protocol.GitFileChange) {
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
			untracked = append(untracked, protocol.GitFileChange{
				Path:   path,
				Status: "untracked",
			})
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

	staged, unstaged, untracked := parseGitStatusPorcelain(string(statusOutput))

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
