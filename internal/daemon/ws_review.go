package daemon

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) handleStartReviewLoopWS(client *wsClient, msg *protocol.StartReviewLoopMessage) {
	run, err := d.startReviewLoop(msg)
	d.sendReviewLoopResult(client, "start", msg.SessionID, "", run, err)
}

func (d *Daemon) handleStopReviewLoopWS(client *wsClient, msg *protocol.StopReviewLoopMessage) {
	run, err := d.stopReviewLoop(msg.SessionID, reviewLoopStopReasonUserStopped)
	d.sendReviewLoopResult(client, "stop", msg.SessionID, "", run, err)
}

func (d *Daemon) handleGetReviewLoopStateWS(client *wsClient, msg *protocol.GetReviewLoopStateMessage) {
	run, err := d.getReviewLoopRunForSession(msg.SessionID)
	d.sendReviewLoopResult(client, "get", msg.SessionID, "", run, err)
}

func (d *Daemon) handleGetReviewLoopRunWS(client *wsClient, msg *protocol.GetReviewLoopRunMessage) {
	run, err := d.store.GetReviewLoopRun(msg.LoopID)
	if err == nil && run != nil {
		run, err = d.hydrateReviewLoopRunWithIterations(run)
	}
	d.sendReviewLoopResult(client, "show", "", msg.LoopID, run, err)
}

func (d *Daemon) handleSetReviewLoopIterationsWS(client *wsClient, msg *protocol.SetReviewLoopIterationLimitMessage) {
	run, err := d.setReviewLoopIterationLimit(msg.SessionID, msg.IterationLimit)
	d.sendReviewLoopResult(client, "set_iterations", msg.SessionID, "", run, err)
}

func (d *Daemon) handleAnswerReviewLoopWS(client *wsClient, msg *protocol.AnswerReviewLoopMessage) {
	run, err := d.answerReviewLoop(msg)
	d.sendReviewLoopResult(client, "answer", "", msg.LoopID, run, err)
}

func (d *Daemon) handleGetReviewState(client *wsClient, msg *protocol.GetReviewStateMessage) {
	result := protocol.GetReviewStateResultMessage{
		Event:   protocol.EventGetReviewStateResult,
		Success: false,
	}

	review, err := d.store.GetOrCreateReview(msg.RepoPath, msg.Branch)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}

	viewedFiles, err := d.store.GetViewedFiles(review.ID)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}

	result.State = &protocol.ReviewState{
		ReviewID:    review.ID,
		RepoPath:    review.RepoPath,
		Branch:      review.Branch,
		ViewedFiles: viewedFiles,
	}
	result.Success = true
	d.sendToClient(client, result)
}

func (d *Daemon) handleMarkFileViewed(client *wsClient, msg *protocol.MarkFileViewedMessage) {
	result := protocol.MarkFileViewedResultMessage{
		Event:    protocol.EventMarkFileViewedResult,
		ReviewID: msg.ReviewID,
		Filepath: msg.Filepath,
		Viewed:   msg.Viewed,
		Success:  false,
	}

	var err error
	if msg.Viewed {
		err = d.store.MarkFileViewed(msg.ReviewID, msg.Filepath)
	} else {
		err = d.store.UnmarkFileViewed(msg.ReviewID, msg.Filepath)
	}

	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}

	result.Success = true
	d.sendToClient(client, result)
}

func (d *Daemon) handleAddComment(client *wsClient, msg *protocol.AddCommentMessage) {
	result := protocol.AddCommentResultMessage{
		Event:   protocol.EventAddCommentResult,
		Success: false,
	}

	comment, err := d.store.AddComment(msg.ReviewID, msg.Filepath, int(msg.LineStart), int(msg.LineEnd), msg.Content, "user")
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}

	result.Success = true
	result.Comment = &protocol.ReviewComment{
		ID:        comment.ID,
		ReviewID:  comment.ReviewID,
		Filepath:  comment.Filepath,
		LineStart: int(comment.LineStart),
		LineEnd:   int(comment.LineEnd),
		Content:   comment.Content,
		Author:    comment.Author,
		Resolved:  comment.Resolved,
		CreatedAt: comment.CreatedAt.Format(time.RFC3339),
	}
	d.sendToClient(client, result)
}

func (d *Daemon) handleUpdateComment(client *wsClient, msg *protocol.UpdateCommentMessage) {
	result := protocol.UpdateCommentResultMessage{
		Event:   protocol.EventUpdateCommentResult,
		Success: false,
	}

	err := d.store.UpdateComment(msg.CommentID, msg.Content)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}

	result.Success = true
	d.sendToClient(client, result)
}

func (d *Daemon) handleResolveComment(client *wsClient, msg *protocol.ResolveCommentMessage) {
	result := protocol.ResolveCommentResultMessage{
		Event:   protocol.EventResolveCommentResult,
		Success: false,
	}

	resolvedBy := ""
	if msg.Resolved {
		resolvedBy = "user"
	}
	err := d.store.ResolveComment(msg.CommentID, msg.Resolved, resolvedBy)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}

	result.Success = true
	d.sendToClient(client, result)
}

func (d *Daemon) handleWontFixComment(client *wsClient, msg *protocol.WontFixCommentMessage) {
	result := protocol.WontFixCommentResultMessage{
		Event:   protocol.EventWontFixCommentResult,
		Success: false,
	}

	wontFixBy := ""
	if msg.WontFix {
		wontFixBy = "user"
	}
	err := d.store.WontFixComment(msg.CommentID, msg.WontFix, wontFixBy)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}

	result.Success = true
	d.sendToClient(client, result)
}

func (d *Daemon) handleDeleteComment(client *wsClient, msg *protocol.DeleteCommentMessage) {
	result := protocol.DeleteCommentResultMessage{
		Event:   protocol.EventDeleteCommentResult,
		Success: false,
	}

	err := d.store.DeleteComment(msg.CommentID)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}

	result.Success = true
	d.sendToClient(client, result)
}

func (d *Daemon) handleGetComments(client *wsClient, msg *protocol.GetCommentsMessage) {
	result := protocol.GetCommentsResultMessage{
		Event:   protocol.EventGetCommentsResult,
		Success: false,
	}

	var (
		comments []*store.ReviewComment
		err      error
	)

	if msg.Filepath != nil && *msg.Filepath != "" {
		comments, err = d.store.GetCommentsForFile(msg.ReviewID, *msg.Filepath)
	} else {
		comments, err = d.store.GetComments(msg.ReviewID)
	}

	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}

	result.Success = true
	result.Comments = make([]protocol.ReviewComment, len(comments))
	for i, c := range comments {
		result.Comments[i] = protocol.ReviewComment{
			ID:        c.ID,
			ReviewID:  c.ReviewID,
			Filepath:  c.Filepath,
			LineStart: int(c.LineStart),
			LineEnd:   int(c.LineEnd),
			Content:   c.Content,
			Author:    c.Author,
			Resolved:  c.Resolved,
			CreatedAt: c.CreatedAt.Format(time.RFC3339),
		}
	}
	d.sendToClient(client, result)
}
