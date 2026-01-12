// Package mcp provides MCP tools for the reviewer agent.
// These tools give the agent access to git data and comment operations.
package mcp

import (
	"os/exec"
	"strings"

	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/store"
)

// Tools provides MCP tool implementations for the reviewer agent
type Tools struct {
	repoPath string
	reviewID string
	baseRef  string
	store    *store.Store
}

// NewTools creates a new Tools instance
func NewTools(repoPath, reviewID, baseRef string, store *store.Store) *Tools {
	return &Tools{
		repoPath: repoPath,
		reviewID: reviewID,
		baseRef:  baseRef,
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
	baseRef := t.getBaseRef()
	changed, err := git.GetBranchDiffFiles(t.repoPath, baseRef)
	if err != nil {
		return nil, err
	}

	files := make([]ChangedFile, 0, len(changed))
	for _, f := range changed {
		files = append(files, ChangedFile{
			Path:   f.Path,
			Status: f.Status,
		})
	}

	return files, nil
}

// GetDiff returns the diff content for the specified paths
// If paths is empty, returns the diff for all changed files
func (t *Tools) GetDiff(paths []string) (map[string]string, error) {
	result := make(map[string]string)
	baseRef := t.getBaseRef()
	statusByPath := make(map[string]string)

	// Get list of files to diff
	var filesToDiff []string
	if len(paths) == 0 {
		changed, err := git.GetBranchDiffFiles(t.repoPath, baseRef)
		if err != nil {
			return nil, err
		}
		for _, f := range changed {
			filesToDiff = append(filesToDiff, f.Path)
			statusByPath[f.Path] = f.Status
		}
	} else {
		filesToDiff = paths
	}

	if len(statusByPath) == 0 {
		changed, err := git.GetBranchDiffFiles(t.repoPath, baseRef)
		if err == nil {
			for _, f := range changed {
				statusByPath[f.Path] = f.Status
			}
		}
	}

	for _, path := range filesToDiff {
		status := statusByPath[path]
		diff, err := t.getDiffForFile(path, status, baseRef)
		if err != nil {
			return nil, err
		}
		if diff != "" {
			result[path] = diff
		}
	}

	return result, nil
}

func (t *Tools) getDiffForFile(path, status, baseRef string) (string, error) {
	if status == "untracked" {
		return t.runGitDiff("diff", "--no-index", "--", "/dev/null", path)
	}
	return t.runGitDiff("diff", baseRef, "--", path)
}

func (t *Tools) runGitDiff(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = t.repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return string(output), nil
		}
		return "", err
	}
	return string(output), nil
}

func (t *Tools) getBaseRef() string {
	if strings.TrimSpace(t.baseRef) != "" {
		return t.baseRef
	}
	return "HEAD"
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
