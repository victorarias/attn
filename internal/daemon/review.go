package daemon

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// activeReviews tracks running review sessions for cancellation
var (
	activeReviews   = make(map[string]context.CancelFunc)
	activeReviewsMu sync.Mutex
)

// handleStartReview starts a review agent for the given branch
// For the walking skeleton, this sends hardcoded fake streaming events
func (d *Daemon) handleStartReview(client *wsClient, msg *protocol.StartReviewMessage) {
	reviewID := msg.ReviewID

	// Create cancellable context for this review
	ctx, cancel := context.WithCancel(context.Background())

	activeReviewsMu.Lock()
	// Cancel any existing review for this ID
	if existingCancel, ok := activeReviews[reviewID]; ok {
		existingCancel()
	}
	activeReviews[reviewID] = cancel
	activeReviewsMu.Unlock()

	// Run the review in a goroutine
	go func() {
		defer func() {
			// Recover from panic if client disconnected during review
			if r := recover(); r != nil {
				d.logf("Review goroutine recovered from panic: %v", r)
			}
			activeReviewsMu.Lock()
			delete(activeReviews, reviewID)
			activeReviewsMu.Unlock()
		}()

		d.runFakeReview(ctx, client, msg)
	}()
}

// handleCancelReview cancels an in-progress review
func (d *Daemon) handleCancelReview(client *wsClient, msg *protocol.CancelReviewMessage) {
	activeReviewsMu.Lock()
	cancel, ok := activeReviews[msg.ReviewID]
	activeReviewsMu.Unlock()

	if ok {
		cancel()
		d.sendToClient(client, map[string]interface{}{
			"event":     protocol.EventReviewCancelled,
			"review_id": msg.ReviewID,
		})
	}
}

// runFakeReview simulates a review with streaming output using real git status
// This is the walking skeleton - will be replaced with real agent in Phase 3.3
func (d *Daemon) runFakeReview(ctx context.Context, client *wsClient, msg *protocol.StartReviewMessage) {
	reviewID := msg.ReviewID

	// Send review_started
	d.sendToClient(client, map[string]interface{}{
		"event":     protocol.EventReviewStarted,
		"review_id": reviewID,
	})

	// Get real git status
	status, err := getGitStatus(msg.RepoPath)
	if err != nil || status.Error != nil {
		d.sendToClient(client, map[string]interface{}{
			"event":     protocol.EventReviewChunk,
			"review_id": reviewID,
			"content":   "⚠️ Could not access git repository\n",
		})
		d.sendToClient(client, map[string]interface{}{
			"event":     protocol.EventReviewComplete,
			"review_id": reviewID,
			"success":   false,
			"error":     "Failed to access git repository",
		})
		return
	}

	// Collect all changed files, filter out lockfiles
	var changedFiles []protocol.GitFileChange
	skipPatterns := []string{"pnpm-lock.yaml", "package-lock.json", "yarn.lock", "go.sum", "Cargo.lock"}
	isSkipFile := func(path string) bool {
		for _, p := range skipPatterns {
			if strings.HasSuffix(path, p) {
				return true
			}
		}
		return false
	}

	for _, f := range status.Staged {
		if !isSkipFile(f.Path) {
			changedFiles = append(changedFiles, f)
		}
	}
	for _, f := range status.Unstaged {
		if !isSkipFile(f.Path) {
			changedFiles = append(changedFiles, f)
		}
	}

	// Simulate streaming chunks with delays
	chunks := []string{
		"## Reviewing branch `" + msg.Branch + "`\n\n",
		"Analyzing changes against `" + msg.BaseBranch + "`...\n\n",
	}

	for _, chunk := range chunks {
		select {
		case <-ctx.Done():
			return // Review was cancelled
		case <-time.After(200 * time.Millisecond):
			d.sendToClient(client, map[string]interface{}{
				"event":     protocol.EventReviewChunk,
				"review_id": reviewID,
				"content":   chunk,
			})
		}
	}

	if len(changedFiles) == 0 {
		d.sendToClient(client, map[string]interface{}{
			"event":     protocol.EventReviewChunk,
			"review_id": reviewID,
			"content":   "No files to review (only lockfiles changed).\n",
		})
		d.sendToClient(client, map[string]interface{}{
			"event":     protocol.EventReviewComplete,
			"review_id": reviewID,
			"success":   true,
		})
		return
	}

	// Send file count
	d.sendToClient(client, map[string]interface{}{
		"event":     protocol.EventReviewChunk,
		"review_id": reviewID,
		"content":   "Found **" + strconv.Itoa(len(changedFiles)) + " files** to review.\n\n### Findings\n\n",
	})

	// Generate findings for the first file (or first few)
	var findings []protocol.ReviewFinding
	for i, file := range changedFiles {
		if i >= 2 {
			break // Max 2 findings for the mock
		}

		// Get the first modified line number from git diff
		lineNum := getFirstModifiedLine(msg.RepoPath, file.Path)
		if lineNum == 0 {
			lineNum = 1 // Fallback
		}

		// Generate a mock finding based on file extension
		content := generateMockFinding(file.Path)
		severity := "suggestion"
		if i == 0 {
			severity = "warning"
		}

		findings = append(findings, protocol.ReviewFinding{
			Filepath:  file.Path,
			LineStart: lineNum,
			LineEnd:   lineNum,
			Content:   content,
			Severity:  protocol.Ptr(severity),
		})
	}

	for i, finding := range findings {
		select {
		case <-ctx.Done():
			return
		case <-time.After(400 * time.Millisecond):
			// Create comment in SQLite first so we have the ID
			comment, err := d.store.AddComment(reviewID, finding.Filepath, finding.LineStart, finding.LineEnd, finding.Content, "agent")
			if err != nil {
				d.logf("Error adding comment from finding: %v", err)
				continue
			}
			d.logf("Created comment %d for finding in %s:%d", i+1, finding.Filepath, finding.LineStart)

			// Send finding event with the created comment
			d.sendToClient(client, map[string]interface{}{
				"event":     protocol.EventReviewFinding,
				"review_id": reviewID,
				"finding":   finding,
				"comment":   comment, // Include the full comment with ID
			})

			// Also send a chunk describing the finding
			d.sendToClient(client, map[string]interface{}{
				"event":     protocol.EventReviewChunk,
				"review_id": reviewID,
				"content":   "**" + finding.Filepath + ":" + itoa(finding.LineStart) + "** - " + finding.Content + "\n\n",
			})
		}
	}

	// Final chunk
	select {
	case <-ctx.Done():
		return
	case <-time.After(200 * time.Millisecond):
		d.sendToClient(client, map[string]interface{}{
			"event":     protocol.EventReviewChunk,
			"review_id": reviewID,
			"content":   "\n---\n\nReview complete. Found **" + strconv.Itoa(len(findings)) + " issues** that need attention.\n",
		})
	}

	// Send review_complete
	d.sendToClient(client, map[string]interface{}{
		"event":     protocol.EventReviewComplete,
		"review_id": reviewID,
		"success":   true,
	})
}

// getFirstModifiedLine returns the first added/modified line number from git diff
func getFirstModifiedLine(repoPath, filePath string) int {
	// Try unstaged diff first, then staged
	for _, cached := range []bool{false, true} {
		args := []string{"diff", "--unified=0"}
		if cached {
			args = append(args, "--cached")
		}
		args = append(args, "--", filePath)

		cmd := exec.Command("git", args...)
		cmd.Dir = repoPath
		output, err := cmd.Output()
		if err != nil {
			continue
		}

		// Parse diff output for @@ -X,Y +A,B @@ lines
		for _, line := range strings.Split(string(output), "\n") {
			if strings.HasPrefix(line, "@@") {
				// Extract the +A part (new file line number)
				parts := strings.Split(line, "+")
				if len(parts) >= 2 {
					numPart := strings.Split(parts[1], ",")[0]
					numPart = strings.TrimSpace(numPart)
					if n, err := strconv.Atoi(numPart); err == nil && n > 0 {
						return n
					}
				}
			}
		}
	}
	return 0
}

// generateMockFinding returns a mock finding message based on file type
func generateMockFinding(filepath string) string {
	switch {
	case strings.HasSuffix(filepath, ".go"):
		return "Consider adding error handling for this operation. Unhandled errors can lead to unexpected behavior."
	case strings.HasSuffix(filepath, ".ts") || strings.HasSuffix(filepath, ".tsx"):
		return "This component could benefit from memoization to prevent unnecessary re-renders."
	case strings.HasSuffix(filepath, ".css"):
		return "Consider using CSS custom properties (variables) for this color value to improve maintainability."
	case strings.HasSuffix(filepath, ".js") || strings.HasSuffix(filepath, ".jsx"):
		return "This function might throw. Consider wrapping in try-catch or adding error boundary."
	case strings.HasSuffix(filepath, ".py"):
		return "Consider adding type hints to improve code clarity and enable better IDE support."
	case strings.HasSuffix(filepath, ".md"):
		return "Documentation looks good! Consider adding an example for this section."
	default:
		return "Review this change carefully to ensure it follows project conventions."
	}
}

// itoa converts int to string (avoiding strconv import for simplicity)
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [11]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte(n%10) + '0'
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
