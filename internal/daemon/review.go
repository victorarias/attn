package daemon

import (
	"context"
	"sync"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/reviewer"
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
		RepoPath:      config.RepoPath,
		Branch:        config.Branch,
		BaseBranch:    config.BaseBranch,
		ReviewID:      config.ReviewID,
		IsRereview:    config.IsRereview,
		LastReviewSHA: config.LastReviewSHA,
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

// runReview executes a code review using the real reviewer agent
func (d *Daemon) runReview(ctx context.Context, client *wsClient, msg *protocol.StartReviewMessage) {
	reviewID := msg.ReviewID
	d.logf("runReview called for reviewID=%s repoPath=%s", reviewID, msg.RepoPath)

	// Create reviewer agent (use factory if provided, otherwise real reviewer)
	var agent Reviewer
	if d.reviewerFactory != nil {
		agent = d.reviewerFactory(d.store)
	} else {
		agent = &realReviewerAdapter{r: reviewer.New(d.store).WithLogger(d.logf)}
	}

	// Configure the review
	config := ReviewerConfig{
		RepoPath:   msg.RepoPath,
		Branch:     msg.Branch,
		BaseBranch: msg.BaseBranch,
		ReviewID:   reviewID,
	}

	// Run the review with event callback
	d.logf("Calling agent.Run for reviewID=%s", reviewID)
	err := agent.Run(ctx, config, func(event ReviewerEvent) {
		d.logf("Review event: type=%s", event.Type)
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
}
