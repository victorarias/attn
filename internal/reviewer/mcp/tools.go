// Package mcp provides MCP tools for the reviewer agent.
// These tools give the agent access to git data and comment operations.
package mcp

import (
	"os/exec"
	"strings"

	"github.com/victorarias/attn/internal/store"
)

// Tools provides MCP tool implementations for the reviewer agent
type Tools struct {
	repoPath string
	reviewID string
	store    *store.Store
}

// NewTools creates a new Tools instance
func NewTools(repoPath, reviewID string, store *store.Store) *Tools {
	return &Tools{
		repoPath: repoPath,
		reviewID: reviewID,
		store:    store,
	}
}

// ChangedFile represents a file that has been modified
type ChangedFile struct {
	Path   string `json:"path"`
	Status string `json:"status"` // "added", "modified", "deleted"
}

// GetChangedFiles returns a list of files that have been changed in the working tree
func (t *Tools) GetChangedFiles() ([]ChangedFile, error) {
	// Get status with porcelain format
	cmd := exec.Command("git", "status", "--porcelain", "-z")
	cmd.Dir = t.repoPath
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var files []ChangedFile
	entries := strings.Split(string(output), "\x00")
	for _, entry := range entries {
		if len(entry) < 4 {
			continue
		}
		// Format: XY PATH where X is index status, Y is worktree status
		indexStatus := entry[0]
		worktreeStatus := entry[1]
		path := entry[3:]

		// Skip renames (R) for now - they have two paths
		if indexStatus == 'R' || worktreeStatus == 'R' {
			continue
		}

		status := "modified"
		if indexStatus == 'A' || worktreeStatus == '?' {
			status = "added"
		} else if indexStatus == 'D' || worktreeStatus == 'D' {
			status = "deleted"
		}

		files = append(files, ChangedFile{
			Path:   path,
			Status: status,
		})
	}

	return files, nil
}

// GetDiff returns the diff content for the specified paths
// If paths is empty, returns the diff for all changed files
func (t *Tools) GetDiff(paths []string) (map[string]string, error) {
	result := make(map[string]string)

	// Get list of files to diff
	var filesToDiff []string
	if len(paths) == 0 {
		changed, err := t.GetChangedFiles()
		if err != nil {
			return nil, err
		}
		for _, f := range changed {
			filesToDiff = append(filesToDiff, f.Path)
		}
	} else {
		filesToDiff = paths
	}

	for _, path := range filesToDiff {
		// Try unstaged diff first
		diff, err := t.getDiffForFile(path, false)
		if err != nil || diff == "" {
			// Try staged diff
			diff, _ = t.getDiffForFile(path, true)
		}
		if diff != "" {
			result[path] = diff
		}
	}

	return result, nil
}

func (t *Tools) getDiffForFile(path string, staged bool) (string, error) {
	args := []string{"diff"}
	if staged {
		args = append(args, "--cached")
	}
	args = append(args, "--", path)

	cmd := exec.Command("git", args...)
	cmd.Dir = t.repoPath
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

// CommentInfo represents a comment with resolution info
type CommentInfo struct {
	ID         string  `json:"id"`
	Filepath   string  `json:"filepath"`
	LineStart  int     `json:"line_start"`
	LineEnd    int     `json:"line_end"`
	Content    string  `json:"content"`
	Author     string  `json:"author"`
	Resolved   bool    `json:"resolved"`
	ResolvedBy string  `json:"resolved_by,omitempty"`
	ResolvedAt *string `json:"resolved_at,omitempty"`
	WontFix    bool    `json:"wont_fix"`
	WontFixBy  string  `json:"wont_fix_by,omitempty"`
	WontFixAt  *string `json:"wont_fix_at,omitempty"`
}

// ListComments returns all comments for the current review
func (t *Tools) ListComments() ([]CommentInfo, error) {
	comments, err := t.store.GetComments(t.reviewID)
	if err != nil {
		return nil, err
	}

	result := make([]CommentInfo, len(comments))
	for i, c := range comments {
		info := CommentInfo{
			ID:         c.ID,
			Filepath:   c.Filepath,
			LineStart:  c.LineStart,
			LineEnd:    c.LineEnd,
			Content:    c.Content,
			Author:     c.Author,
			Resolved:   c.Resolved,
			ResolvedBy: c.ResolvedBy,
			WontFix:    c.WontFix,
			WontFixBy:  c.WontFixBy,
		}
		if c.ResolvedAt != nil {
			ts := c.ResolvedAt.Format("2006-01-02T15:04:05Z")
			info.ResolvedAt = &ts
		}
		if c.WontFixAt != nil {
			ts := c.WontFixAt.Format("2006-01-02T15:04:05Z")
			info.WontFixAt = &ts
		}
		result[i] = info
	}

	return result, nil
}

// AddComment creates a new comment from the agent
func (t *Tools) AddComment(filepath string, lineStart, lineEnd int, content string) (*CommentInfo, error) {
	comment, err := t.store.AddComment(t.reviewID, filepath, lineStart, lineEnd, content, "agent")
	if err != nil {
		return nil, err
	}

	return &CommentInfo{
		ID:        comment.ID,
		Filepath:  comment.Filepath,
		LineStart: comment.LineStart,
		LineEnd:   comment.LineEnd,
		Content:   comment.Content,
		Author:    comment.Author,
		Resolved:  comment.Resolved,
	}, nil
}

// ResolveComment marks a comment as resolved by the agent
func (t *Tools) ResolveComment(id string) error {
	return t.store.ResolveComment(id, true, "agent")
}
