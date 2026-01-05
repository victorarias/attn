package daemon

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/reviewer"
	"github.com/victorarias/attn/internal/store"
)

// activeReviews tracks running review sessions for cancellation
var (
	activeReviews   = make(map[string]context.CancelFunc)
	activeReviewsMu sync.Mutex
)

// e2eMockReviewer is used for E2E testing when ATTN_MOCK_REVIEWER=1
// It sends predictable events for automated testing
type e2eMockReviewer struct {
	store *store.Store
}

func (m *e2eMockReviewer) Run(ctx context.Context, config ReviewerConfig, onEvent func(ReviewerEvent)) error {
	// Send started event
	onEvent(ReviewerEvent{Type: "started"})

	// Check for cancellation
	select {
	case <-ctx.Done():
		onEvent(ReviewerEvent{Type: "cancelled"})
		return ctx.Err()
	default:
	}

	// Simulate tool call
	onEvent(ReviewerEvent{
		Type: "tool_use",
		ToolUse: &ReviewerToolUse{
			Name:   "get_changed_files",
			Input:  map[string]interface{}{},
			Output: `[{"path": "example.go", "status": "modified"}]`,
		},
	})

	time.Sleep(50 * time.Millisecond)

	// Send some text chunks
	onEvent(ReviewerEvent{Type: "chunk", Content: "Reviewing changes...\n\n"})

	time.Sleep(50 * time.Millisecond)

	// Another tool call
	onEvent(ReviewerEvent{
		Type: "tool_use",
		ToolUse: &ReviewerToolUse{
			Name:   "get_diff",
			Input:  map[string]interface{}{"paths": []string{"example.go"}},
			Output: "diff --git a/example.go...",
		},
	})

	time.Sleep(50 * time.Millisecond)

	onEvent(ReviewerEvent{Type: "chunk", Content: "Found some issues in the code.\n\n"})

	// Check for cancellation mid-review
	select {
	case <-ctx.Done():
		onEvent(ReviewerEvent{Type: "cancelled"})
		return ctx.Err()
	default:
	}

	time.Sleep(50 * time.Millisecond)

	// Create a comment via store and send finding
	comment, _ := m.store.AddComment(config.ReviewID, "example.go", 10, 10, "Consider adding error handling here", "agent")
	if comment != nil {
		onEvent(ReviewerEvent{
			Type: "finding",
			Finding: &ReviewerFinding{
				Filepath:  "example.go",
				LineStart: 10,
				LineEnd:   10,
				Content:   "Consider adding error handling here",
				Severity:  "warning",
				CommentID: comment.ID,
			},
		})
	}

	time.Sleep(50 * time.Millisecond)

	// Final summary
	onEvent(ReviewerEvent{Type: "chunk", Content: "## Summary\n\nFound 1 issue that needs attention."})

	// Check for final cancellation
	select {
	case <-ctx.Done():
		onEvent(ReviewerEvent{Type: "cancelled"})
		return ctx.Err()
	default:
	}

	// Send complete event
	onEvent(ReviewerEvent{Type: "complete", Success: true})
	return nil
}

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

		d.runReview(ctx, client, msg)
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

// realReviewerAdapter wraps the real reviewer to implement the Reviewer interface
type realReviewerAdapter struct {
	r *reviewer.Reviewer
}

func (a *realReviewerAdapter) Run(ctx context.Context, config ReviewerConfig, onEvent func(ReviewerEvent)) error {
	realConfig := reviewer.ReviewConfig{
		RepoPath:           config.RepoPath,
		Branch:             config.Branch,
		BaseBranch:         config.BaseBranch,
		ReviewID:           config.ReviewID,
		IsRereview:         config.IsRereview,
		LastReviewSHA:      config.LastReviewSHA,
		PreviousTranscript: config.PreviousTranscript,
	}

	return a.r.Run(ctx, realConfig, func(event reviewer.ReviewEvent) {
		// Convert reviewer.ReviewEvent to daemon.ReviewerEvent
		de := ReviewerEvent{
			Type:       event.Type,
			Content:    event.Content,
			ResolvedID: event.ResolvedID,
			Success:    event.Success,
			Error:      event.Error,
		}
		if event.Finding != nil {
			de.Finding = &ReviewerFinding{
				Filepath:  event.Finding.Filepath,
				LineStart: event.Finding.LineStart,
				LineEnd:   event.Finding.LineEnd,
				Content:   event.Finding.Content,
				Severity:  event.Finding.Severity,
				CommentID: event.Finding.CommentID,
			}
		}
		if event.ToolUse != nil {
			de.ToolUse = &ReviewerToolUse{
				Name:   event.ToolUse.Name,
				Input:  event.ToolUse.Input,
				Output: event.ToolUse.Output,
			}
		}
		onEvent(de)
	})
}

// transcriptEvent is a simplified event for storage in the transcript
type transcriptEvent struct {
	Type       string                 `json:"type"`
	Content    string                 `json:"content,omitempty"`
	ToolUse    *transcriptToolUse     `json:"tool_use,omitempty"`
	Finding    *transcriptFinding     `json:"finding,omitempty"`
	ResolvedID string                 `json:"resolved_id,omitempty"`
}

type transcriptToolUse struct {
	Name   string                 `json:"name"`
	Input  map[string]interface{} `json:"input,omitempty"`
	Output string                 `json:"output,omitempty"`
}

type transcriptFinding struct {
	Filepath  string `json:"filepath"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
	Content   string `json:"content"`
}

// runReview executes a code review using the real reviewer agent
func (d *Daemon) runReview(ctx context.Context, client *wsClient, msg *protocol.StartReviewMessage) {
	reviewID := msg.ReviewID
	d.logf("runReview called for reviewID=%s repoPath=%s", reviewID, msg.RepoPath)

	// Get current HEAD commit for session tracking
	commitSHA, err := git.GetHeadCommit(msg.RepoPath)
	if err != nil {
		d.logf("Failed to get HEAD commit: %v", err)
		commitSHA = "unknown"
	}

	// Check for previous session (re-review detection)
	var isRereview bool
	var lastReviewSHA string
	var previousTranscript string
	prevSession, err := d.store.GetLastReviewSession(reviewID)
	if err == nil && prevSession != nil {
		isRereview = true
		lastReviewSHA = prevSession.CommitSHA
		previousTranscript = prevSession.Transcript
		d.logf("Detected re-review: last session was at commit %s (transcript: %d bytes)", lastReviewSHA, len(previousTranscript))
	}

	// Create a new session to track this review
	session, err := d.store.CreateReviewSession(reviewID, commitSHA)
	if err != nil {
		d.logf("Failed to create review session: %v", err)
	}

	// Create reviewer agent
	// Priority: 1) factory (for unit tests), 2) env var mock (for E2E), 3) real reviewer
	var agent Reviewer
	if d.reviewerFactory != nil {
		agent = d.reviewerFactory(d.store)
	} else if os.Getenv("ATTN_MOCK_REVIEWER") == "1" {
		d.logf("Using mock reviewer for E2E testing")
		agent = &e2eMockReviewer{store: d.store}
	} else {
		agent = &realReviewerAdapter{r: reviewer.New(d.store).WithLogger(d.logf)}
	}

	// Configure the review
	config := ReviewerConfig{
		RepoPath:           msg.RepoPath,
		Branch:             msg.Branch,
		BaseBranch:         msg.BaseBranch,
		ReviewID:           reviewID,
		IsRereview:         isRereview,
		LastReviewSHA:      lastReviewSHA,
		PreviousTranscript: previousTranscript,
	}

	// Transcript accumulator
	var transcript []transcriptEvent

	// Run the review with event callback
	d.logf("Calling agent.Run for reviewID=%s (isRereview=%v)", reviewID, isRereview)
	err = agent.Run(ctx, config, func(event ReviewerEvent) {
		d.logf("Review event: type=%s", event.Type)

		// Capture event for transcript (skip started/complete/error/cancelled - they're metadata)
		switch event.Type {
		case "chunk":
			transcript = append(transcript, transcriptEvent{Type: "chunk", Content: event.Content})
		case "tool_use":
			// Store only tool name for debugging - skip input/output to avoid bloat
			// (get_diff output can be megabytes)
			if event.ToolUse != nil {
				transcript = append(transcript, transcriptEvent{
					Type: "tool_use",
					ToolUse: &transcriptToolUse{
						Name: event.ToolUse.Name,
					},
				})
			}
		case "finding":
			if event.Finding != nil {
				transcript = append(transcript, transcriptEvent{
					Type: "finding",
					Finding: &transcriptFinding{
						Filepath:  event.Finding.Filepath,
						LineStart: event.Finding.LineStart,
						LineEnd:   event.Finding.LineEnd,
						Content:   event.Finding.Content,
					},
				})
			}
		case "resolved":
			transcript = append(transcript, transcriptEvent{Type: "resolved", ResolvedID: event.ResolvedID})
		}

		// Forward event to client
		switch event.Type {
		case "started":
			d.sendToClient(client, map[string]interface{}{
				"event":     protocol.EventReviewStarted,
				"review_id": reviewID,
			})

		case "chunk":
			d.sendToClient(client, map[string]interface{}{
				"event":     protocol.EventReviewChunk,
				"review_id": reviewID,
				"content":   event.Content,
			})

		case "finding":
			if event.Finding != nil {
				// Fetch the comment that was created by the MCP tool
				comments, _ := d.store.GetComments(reviewID)
				var comment *protocol.ReviewComment
				for _, c := range comments {
					if c.ID == event.Finding.CommentID {
						// Convert store.ReviewComment to protocol.ReviewComment
						var resolvedAt *string
						if c.ResolvedAt != nil {
							t := c.ResolvedAt.Format("2006-01-02T15:04:05Z")
							resolvedAt = &t
						}
						var resolvedBy *string
						if c.ResolvedBy != "" {
							resolvedBy = &c.ResolvedBy
						}
						comment = &protocol.ReviewComment{
							ID:         c.ID,
							ReviewID:   c.ReviewID,
							Filepath:   c.Filepath,
							LineStart:  c.LineStart,
							LineEnd:    c.LineEnd,
							Content:    c.Content,
							Author:     c.Author,
							Resolved:   c.Resolved,
							ResolvedBy: resolvedBy,
							ResolvedAt: resolvedAt,
							CreatedAt:  c.CreatedAt.Format("2006-01-02T15:04:05Z"),
						}
						break
					}
				}

				finding := protocol.ReviewFinding{
					Filepath:  event.Finding.Filepath,
					LineStart: event.Finding.LineStart,
					LineEnd:   event.Finding.LineEnd,
					Content:   event.Finding.Content,
					Severity:  protocol.Ptr(event.Finding.Severity),
				}

				d.sendToClient(client, map[string]interface{}{
					"event":     protocol.EventReviewFinding,
					"review_id": reviewID,
					"finding":   finding,
					"comment":   comment,
				})
			}

		case "resolved":
			d.sendToClient(client, map[string]interface{}{
				"event":      protocol.EventReviewCommentResolved,
				"review_id":  reviewID,
				"comment_id": event.ResolvedID,
			})

		case "tool_use":
			if event.ToolUse != nil {
				d.sendToClient(client, map[string]interface{}{
					"event":     protocol.EventReviewToolUse,
					"review_id": reviewID,
					"tool_use": map[string]interface{}{
						"name":   event.ToolUse.Name,
						"input":  event.ToolUse.Input,
						"output": event.ToolUse.Output,
					},
				})
			}

		case "complete":
			d.sendToClient(client, map[string]interface{}{
				"event":     protocol.EventReviewComplete,
				"review_id": reviewID,
				"success":   event.Success,
				"error":     event.Error,
			})

		case "error":
			d.sendToClient(client, map[string]interface{}{
				"event":     protocol.EventReviewComplete,
				"review_id": reviewID,
				"success":   false,
				"error":     event.Error,
			})

		case "cancelled":
			d.sendToClient(client, map[string]interface{}{
				"event":     protocol.EventReviewCancelled,
				"review_id": reviewID,
			})
		}
	})

	d.logf("agent.Run completed for reviewID=%s, err=%v", reviewID, err)
	if err != nil && err != context.Canceled {
		d.logf("Review error: %v", err)
	}

	// Save transcript and mark session complete
	if session != nil && len(transcript) > 0 {
		transcriptJSON, jsonErr := json.Marshal(transcript)
		if jsonErr != nil {
			d.logf("Failed to marshal transcript: %v", jsonErr)
		} else {
			if updateErr := d.store.UpdateReviewSessionTranscript(session.ID, string(transcriptJSON)); updateErr != nil {
				d.logf("Failed to save transcript: %v", updateErr)
			}
		}
		if completeErr := d.store.CompleteReviewSession(session.ID); completeErr != nil {
			d.logf("Failed to mark session complete: %v", completeErr)
		}
		d.logf("Saved transcript with %d events for session %s", len(transcript), session.ID)

		// Prune old sessions (keep last 5)
		if pruned, pruneErr := d.store.PruneReviewSessions(reviewID, 5); pruneErr != nil {
			d.logf("Failed to prune old sessions: %v", pruneErr)
		} else if pruned > 0 {
			d.logf("Pruned %d old review sessions", pruned)
		}
	}
}
