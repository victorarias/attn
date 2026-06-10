package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/tour"
)

const tourEventWaitTimeout = 25 * time.Second

func (d *Daemon) openTour(msg *protocol.OpenTourMessage) (*protocol.TourRun, error) {
	session, err := d.localTourSession(msg.SessionID)
	if err != nil {
		return nil, err
	}
	if !tour.IsSystemGuidePath(msg.GuidePath) {
		return nil, fmt.Errorf("tour guides must be stored under the active attn profile directory")
	}
	baseRef := strings.TrimSpace(protocol.Deref(msg.BaseRef))
	if baseRef == "" {
		baseRef, err = d.coordinator().DefaultBranch(session.Directory)
		if err != nil {
			return nil, fmt.Errorf("resolve default branch: %w", err)
		}
	}
	snapshot, err := d.loadTourSnapshot(session.Directory, msg.GuidePath, baseRef)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(protocol.Deref(msg.Name))
	if name == "" {
		name = "Tour"
	}
	run, err := d.store.CreateOrOpenTour(
		session.ID,
		name,
		session.Directory,
		msg.GuidePath,
		baseRef,
		snapshot,
	)
	if err != nil {
		return nil, err
	}
	d.broadcastTourUpdated(run)
	return run, nil
}

func (d *Daemon) refreshTour(tourID string) (*protocol.TourRun, error) {
	run, err := d.store.GetTourByID(tourID)
	if err != nil {
		return nil, err
	}
	if _, err := d.localTourSession(run.SessionID); err != nil {
		return nil, err
	}
	snapshot, err := d.loadTourSnapshot(run.RepoPath, run.GuidePath, run.BaseRef)
	if err != nil {
		return nil, err
	}
	run, err = d.store.UpdateTourSnapshot(tourID, snapshot)
	if err != nil {
		return nil, err
	}
	d.broadcastTourUpdated(run)
	return run, nil
}

func (d *Daemon) getTourState(sessionID string) (*protocol.TourRun, error) {
	if _, err := d.localTourSession(sessionID); err != nil {
		return nil, err
	}
	return d.store.GetActiveTourBySession(sessionID)
}

func (d *Daemon) saveTourDraft(msg *protocol.SaveTourDraftMessage) (*protocol.TourRun, error) {
	if _, err := d.localTourByID(msg.TourID); err != nil {
		return nil, err
	}
	run, err := d.store.SaveTourDraft(
		msg.TourID,
		msg.Draft.Path,
		msg.Draft.Reviewed,
		msg.Draft.Note,
		msg.Draft.AnnotationReplies,
		msg.Draft.LineComments,
	)
	if err != nil {
		return nil, err
	}
	d.broadcastTourUpdated(run)
	return run, nil
}

func (d *Daemon) askTour(msg *protocol.AskTourMessage) (*protocol.TourEvent, *protocol.TourRun, error) {
	if _, err := d.localTourByID(msg.TourID); err != nil {
		return nil, nil, err
	}
	body := strings.TrimSpace(msg.Body)
	if body == "" {
		return nil, nil, fmt.Errorf("question is empty")
	}
	markdown := formatTourQuestion(body, msg.Context)
	event, _, err := d.store.AddTourEvent(msg.TourID, "question", markdown, false, &msg.Context)
	if err != nil {
		return nil, nil, err
	}
	run, err := d.store.AddTourTranscript(msg.TourID, "user", body, &event.ID, &msg.Context)
	if err != nil {
		return nil, nil, err
	}
	d.broadcastTourUpdated(run)
	return event, run, nil
}

func (d *Daemon) replyTour(msg *protocol.ReplyTourMessage) (*protocol.TourRun, error) {
	if _, err := d.localTourByID(msg.TourID); err != nil {
		return nil, err
	}
	body := strings.TrimSpace(msg.Body)
	if body == "" {
		return nil, fmt.Errorf("reply is empty")
	}
	run, err := d.store.AddTourTranscript(msg.TourID, "agent", body, &msg.EventID, nil)
	if err != nil {
		return nil, err
	}
	d.broadcastTourUpdated(run)
	return run, nil
}

func (d *Daemon) submitTour(msg *protocol.SubmitTourMessage) (*protocol.TourEvent, *protocol.TourRun, error) {
	if _, err := d.localTourByID(msg.TourID); err != nil {
		return nil, nil, err
	}
	body := strings.TrimSpace(msg.Body)
	if body == "" && !msg.Finish {
		return nil, nil, fmt.Errorf("feedback is empty")
	}
	kind := "feedback"
	if msg.Finish {
		kind = "finish"
	}
	event, run, err := d.store.AddTourEvent(msg.TourID, kind, body, msg.Finish, nil)
	if err != nil {
		return nil, nil, err
	}
	d.broadcastTourUpdated(run)
	return event, run, nil
}

func (d *Daemon) waitTourEvent(msg *protocol.WaitTourEventMessage) (*protocol.TourEvent, *protocol.TourRun, error) {
	run, err := d.localTourByID(msg.TourID)
	if err != nil {
		return nil, nil, err
	}
	if run.Status == protocol.TourStatusActive {
		run, err = d.store.TouchTourListener(msg.TourID, msg.AfterSeq)
		if err != nil {
			return nil, nil, err
		}
		d.broadcastTourUpdated(run)
	}

	deadline := time.Now().Add(tourEventWaitTimeout)
	for {
		event, err := d.store.NextTourEvent(msg.TourID, msg.AfterSeq)
		if err != nil {
			return nil, nil, err
		}
		if event != nil {
			run, err := d.store.GetTourByID(msg.TourID)
			return event, run, err
		}
		run, err = d.store.GetTourByID(msg.TourID)
		if err != nil {
			return nil, nil, err
		}
		if run.Status == protocol.TourStatusEnded || time.Now().After(deadline) {
			return nil, run, nil
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func (d *Daemon) localTourSession(sessionID string) (*protocol.Session, error) {
	session := d.store.Get(sessionID)
	if session == nil {
		return nil, fmt.Errorf("session not found")
	}
	if session.EndpointID != nil && strings.TrimSpace(*session.EndpointID) != "" {
		return nil, fmt.Errorf("tours are only available for local sessions")
	}
	if strings.TrimSpace(session.Directory) == "" {
		return nil, fmt.Errorf("session has no repository directory")
	}
	return session, nil
}

func (d *Daemon) localTourByID(tourID string) (*protocol.TourRun, error) {
	run, err := d.store.GetTourByID(tourID)
	if err != nil {
		return nil, err
	}
	if _, err := d.localTourSession(run.SessionID); err != nil {
		return nil, err
	}
	return run, nil
}

func (d *Daemon) loadTourSnapshot(repoPath, guidePath, baseRef string) (store.TourSnapshot, error) {
	guide, err := tour.Load(guidePath)
	if err != nil {
		return store.TourSnapshot{}, fmt.Errorf("load tour guide: %w", err)
	}
	changedFiles, err := d.coordinator().RefreshBranchDiffFiles(repoPath, baseRef)
	if err != nil {
		return store.TourSnapshot{}, fmt.Errorf("load changed files: %w", err)
	}
	snapshot, err := tour.BuildSnapshot(guide, changedFiles, func(path, oldPath string) (string, string, error) {
		content, err := d.coordinator().FileDiff(repoPath, path, baseRef, false)
		if err != nil {
			return "", "", err
		}
		if oldPath != "" {
			oldContent, oldErr := d.coordinator().FileDiff(repoPath, oldPath, baseRef, false)
			if oldErr == nil {
				content.original = oldContent.original
			}
		}
		return content.original, content.modified, err
	})
	if err != nil {
		return store.TourSnapshot{}, err
	}
	return tourSnapshotToStore(snapshot), nil
}

func tourSnapshotToStore(snapshot *tour.Snapshot) store.TourSnapshot {
	files := make([]protocol.TourFile, len(snapshot.Files))
	for i, file := range snapshot.Files {
		annotations := make([]protocol.TourAnnotation, len(file.Annotations))
		for annotationIndex, annotation := range file.Annotations {
			comments := make([]protocol.TourComment, len(annotation.Comments))
			for commentIndex, comment := range annotation.Comments {
				comments[commentIndex] = protocol.TourComment{
					Author: comment.Author,
					Body:   comment.Body,
				}
			}
			annotations[annotationIndex] = protocol.TourAnnotation{
				ID:        annotation.ID,
				LineStart: annotation.LineStart,
				LineEnd:   annotation.LineEnd,
				Comments:  comments,
			}
		}
		var oldPath *string
		if file.OldPath != "" {
			oldPath = protocol.Ptr(file.OldPath)
		}
		files[i] = protocol.TourFile{
			Path:        file.Path,
			OldPath:     oldPath,
			Status:      file.Status,
			Additions:   file.Additions,
			Deletions:   file.Deletions,
			Group:       file.Group,
			View:        file.View,
			Note:        file.Note,
			Original:    file.Original,
			Modified:    file.Modified,
			Annotations: annotations,
		}
	}
	return store.TourSnapshot{
		Summary:  snapshot.Summary,
		Warnings: append([]string(nil), snapshot.Warnings...),
		Files:    files,
	}
}

func formatTourQuestion(body string, context protocol.TourQuestionContext) string {
	var builder strings.Builder
	builder.WriteString("## Question\n\n")
	builder.WriteString(body)
	if strings.TrimSpace(context.Path) != "" {
		builder.WriteString("\n\n**Context:** `")
		builder.WriteString(context.Path)
		builder.WriteString("`")
		if context.LineStart != nil {
			fmt.Fprintf(&builder, " lines %d", *context.LineStart)
			if context.LineEnd != nil && *context.LineEnd != *context.LineStart {
				fmt.Fprintf(&builder, "-%d", *context.LineEnd)
			}
		}
	}
	if context.Code != nil && strings.TrimSpace(*context.Code) != "" {
		builder.WriteString("\n\n```text\n")
		builder.WriteString(*context.Code)
		builder.WriteString("\n```")
	}
	return builder.String()
}

func (d *Daemon) broadcastTourUpdated(run *protocol.TourRun) {
	if run == nil {
		return
	}
	d.broadcastMessage(&protocol.TourUpdatedMessage{
		Event:     protocol.EventTourUpdated,
		SessionID: run.SessionID,
		Tour:      run,
	})
}

func (d *Daemon) sendTourWSResult(
	client *wsClient,
	action string,
	run *protocol.TourRun,
	event *protocol.TourEvent,
	err error,
) {
	result := &protocol.TourResultMessage{
		Event:   protocol.EventTourResult,
		Action:  action,
		Success: err == nil,
		Tour:    run,
	}
	if run != nil {
		result.SessionID = &run.SessionID
		result.TourID = &run.TourID
	}
	result.TourEvent = event
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, result)
}

func (d *Daemon) handleOpenTour(conn net.Conn, msg *protocol.OpenTourMessage) {
	run, err := d.openTour(msg)
	d.sendTourSocketResult(conn, run, nil, err)
}

func (d *Daemon) handleGetTourState(conn net.Conn, msg *protocol.GetTourStateMessage) {
	run, err := d.getTourState(msg.SessionID)
	d.sendTourSocketResult(conn, run, nil, err)
}

func (d *Daemon) handleRefreshTour(conn net.Conn, msg *protocol.RefreshTourMessage) {
	run, err := d.refreshTour(msg.TourID)
	d.sendTourSocketResult(conn, run, nil, err)
}

func (d *Daemon) handleSaveTourDraft(conn net.Conn, msg *protocol.SaveTourDraftMessage) {
	run, err := d.saveTourDraft(msg)
	d.sendTourSocketResult(conn, run, nil, err)
}

func (d *Daemon) handleAskTour(conn net.Conn, msg *protocol.AskTourMessage) {
	event, run, err := d.askTour(msg)
	d.sendTourSocketResult(conn, run, event, err)
}

func (d *Daemon) handleReplyTour(conn net.Conn, msg *protocol.ReplyTourMessage) {
	run, err := d.replyTour(msg)
	d.sendTourSocketResult(conn, run, nil, err)
}

func (d *Daemon) handleSubmitTour(conn net.Conn, msg *protocol.SubmitTourMessage) {
	event, run, err := d.submitTour(msg)
	d.sendTourSocketResult(conn, run, event, err)
}

func (d *Daemon) handleWaitTourEvent(conn net.Conn, msg *protocol.WaitTourEventMessage) {
	event, run, err := d.waitTourEvent(msg)
	d.sendTourSocketResult(conn, run, event, err)
}

func (d *Daemon) sendTourSocketResult(conn net.Conn, run *protocol.TourRun, event *protocol.TourEvent, err error) {
	response := protocol.Response{Ok: err == nil, Tour: run, TourEvent: event}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
	}
	_ = json.NewEncoder(conn).Encode(response)
}
