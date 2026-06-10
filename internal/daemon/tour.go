package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/tour"
)

const tourEventWaitTimeout = 25 * time.Second

const tourWakeInputQuietPeriod = 500 * time.Millisecond
const tourWakeInFlightTimeout = 10 * time.Second

type tourWakeRequest struct {
	run   *protocol.TourRun
	event *protocol.TourEvent
}

type tourWakeCommit struct {
	tourID    string
	startedAt time.Time
}

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

func (d *Daemon) getTourEvent(msg *protocol.GetTourEventMessage) (*protocol.TourEvent, *protocol.TourRun, error) {
	run, err := d.localTourByID(msg.TourID)
	if err != nil {
		return nil, nil, err
	}
	event, err := d.store.GetTourEvent(msg.TourID, msg.EventID)
	if err != nil {
		return nil, nil, err
	}
	return event, run, nil
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
	d.tourEventMu.Lock()
	event, _, err := d.store.AddTourEvent(msg.TourID, "question", markdown, false, &msg.Context)
	if err != nil {
		d.tourEventMu.Unlock()
		return nil, nil, err
	}
	run, err := d.store.AddTourTranscript(msg.TourID, "user", body, &event.ID, &msg.Context)
	if err != nil {
		d.tourEventMu.Unlock()
		return nil, nil, err
	}
	d.broadcastTourUpdated(run)
	d.enqueueTourWake(run, event)
	d.tourEventMu.Unlock()
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
	d.tourEventMu.Lock()
	event, run, err := d.store.AddTourEvent(msg.TourID, kind, body, msg.Finish, nil)
	if err != nil {
		d.tourEventMu.Unlock()
		return nil, nil, err
	}
	d.broadcastTourUpdated(run)
	d.enqueueTourWake(run, event)
	d.tourEventMu.Unlock()
	return event, run, nil
}

func (d *Daemon) enqueueTourWake(run *protocol.TourRun, event *protocol.TourEvent) {
	if run == nil || event == nil {
		return
	}
	sessionID := run.SessionID
	d.tourWakeQueueMu.Lock()
	if d.tourWakeQueue == nil {
		d.tourWakeQueue = make(map[string][]tourWakeRequest)
	}
	if d.tourWakeWorkers == nil {
		d.tourWakeWorkers = make(map[string]bool)
	}
	d.tourWakeQueue[sessionID] = append(d.tourWakeQueue[sessionID], tourWakeRequest{
		run:   run,
		event: event,
	})
	if d.tourWakeWorkers[sessionID] {
		d.tourWakeQueueMu.Unlock()
		return
	}
	d.tourWakeWorkers[sessionID] = true
	d.tourWakeQueueMu.Unlock()
	go d.processTourWakeQueue(sessionID)
}

func (d *Daemon) processTourWakeQueue(sessionID string) {
	for {
		d.tourWakeQueueMu.Lock()
		queue := d.tourWakeQueue[sessionID]
		if len(queue) == 0 {
			delete(d.tourWakeQueue, sessionID)
			delete(d.tourWakeWorkers, sessionID)
			d.tourWakeQueueMu.Unlock()
			return
		}
		request := queue[0]
		d.tourWakeQueue[sessionID] = queue[1:]
		d.tourWakeQueueMu.Unlock()
		d.wakeTourAgent(request.run, request.event)
	}
}

func (d *Daemon) wakeTourAgent(run *protocol.TourRun, event *protocol.TourEvent) {
	if run == nil || event == nil || (event.Finish && strings.TrimSpace(event.Markdown) == "") {
		return
	}

	session := d.store.Get(run.SessionID)
	if session == nil ||
		strings.TrimSpace(protocol.Deref(session.EndpointID)) != "" ||
		session.Agent == protocol.SessionAgentShell {
		return
	}
	if session.State != protocol.SessionStateIdle &&
		session.State != protocol.SessionStateWaitingInput {
		d.logf(
			"tour wake deferred: session=%s state=%s tour=%s event=%s",
			session.ID,
			session.State,
			run.TourID,
			event.ID,
		)
		return
	}

	inputLock := d.ptyInputLock(session.ID)
	inputLock.Lock()
	defer inputLock.Unlock()

	session = d.store.Get(run.SessionID)
	if session == nil ||
		(session.State != protocol.SessionStateIdle &&
			session.State != protocol.SessionStateWaitingInput) {
		return
	}
	if lastInputAt := d.lastPTYInputTime(session.ID); !lastInputAt.IsZero() &&
		time.Since(lastInputAt) < tourWakeInputQuietPeriod {
		d.logf("tour wake skipped: recent input session=%s tour=%s event=%s", session.ID, run.TourID, event.ID)
		return
	}
	if d.tourWakeIsInFlight(session.ID, run.TourID) {
		d.logf("tour wake skipped: wake already in flight session=%s tour=%s event=%s", session.ID, run.TourID, event.ID)
		return
	}
	if !d.tourAgentEditorEmpty(session.ID) {
		d.logf("tour wake skipped: non-empty or unknown editor session=%s tour=%s event=%s", session.ID, run.TourID, event.ID)
		return
	}

	eventLabel := "review feedback"
	instruction := "Inspect it and act on it while leaving the Tour listener attached."
	if event.Kind == "question" {
		eventLabel = "question"
		instruction = "Answer it with `attn tour reply` and leave the Tour listener attached."
	} else if event.Finish {
		eventLabel = "final review feedback"
		instruction = "Inspect it and act on it. The Tour has ended."
	}
	prompt := fmt.Sprintf(
		"A new attn Tour %s is waiting. Run \"$ATTN_WRAPPER_PATH\" tour event --tour %q --event %q. %s",
		eventLabel,
		run.TourID,
		event.ID,
		instruction,
	)
	// User input records intent through the same mutex as this commit point.
	// Intent that wins the mutex cancels the wake; input after commit is ordered
	// behind the single atomic wake write by inputLock.
	if !d.tryCommitTourWake(session.ID, run.TourID) {
		d.logf("tour wake skipped at commit: recent input session=%s tour=%s event=%s", session.ID, run.TourID, event.ID)
		return
	}
	if err := d.ptyBackend.Input(context.Background(), session.ID, []byte(prompt+"\r")); err != nil {
		d.clearTourWakeInFlight(session.ID, run.TourID)
		d.logf("tour wake prompt failed: session=%s tour=%s event=%s error=%v", session.ID, run.TourID, event.ID, err)
	}
}

func (d *Daemon) ptyInputLock(sessionID string) *sync.Mutex {
	d.ptyInputLocksMu.Lock()
	defer d.ptyInputLocksMu.Unlock()
	if d.ptyInputLocks == nil {
		d.ptyInputLocks = make(map[string]*sync.Mutex)
	}
	lock := d.ptyInputLocks[sessionID]
	if lock == nil {
		lock = &sync.Mutex{}
		d.ptyInputLocks[sessionID] = lock
	}
	return lock
}

func (d *Daemon) markPTYInput(sessionID string) {
	d.ptyInputLocksMu.Lock()
	defer d.ptyInputLocksMu.Unlock()
	if d.lastPTYInputAt == nil {
		d.lastPTYInputAt = make(map[string]time.Time)
	}
	d.lastPTYInputAt[sessionID] = time.Now()
}

func (d *Daemon) lastPTYInputTime(sessionID string) time.Time {
	d.ptyInputLocksMu.Lock()
	defer d.ptyInputLocksMu.Unlock()
	return d.lastPTYInputAt[sessionID]
}

func (d *Daemon) tourWakeIsInFlight(sessionID, tourID string) bool {
	d.ptyInputLocksMu.Lock()
	defer d.ptyInputLocksMu.Unlock()
	commit := d.tourWakeInFlight[sessionID]
	if commit.startedAt.IsZero() || commit.tourID != tourID {
		return false
	}
	if time.Since(commit.startedAt) >= tourWakeInFlightTimeout {
		delete(d.tourWakeInFlight, sessionID)
		return false
	}
	return true
}

func (d *Daemon) tryCommitTourWake(sessionID, tourID string) bool {
	d.ptyInputLocksMu.Lock()
	defer d.ptyInputLocksMu.Unlock()
	now := time.Now()
	if lastInputAt := d.lastPTYInputAt[sessionID]; !lastInputAt.IsZero() &&
		now.Sub(lastInputAt) < tourWakeInputQuietPeriod {
		return false
	}
	if commit := d.tourWakeInFlight[sessionID]; commit.tourID == tourID &&
		!commit.startedAt.IsZero() && now.Sub(commit.startedAt) < tourWakeInFlightTimeout {
		return false
	}
	if d.tourWakeInFlight == nil {
		d.tourWakeInFlight = make(map[string]tourWakeCommit)
	}
	d.tourWakeInFlight[sessionID] = tourWakeCommit{tourID: tourID, startedAt: now}
	return true
}

func (d *Daemon) clearTourWakeInFlight(sessionID, tourID string) {
	d.ptyInputLocksMu.Lock()
	defer d.ptyInputLocksMu.Unlock()
	if d.tourWakeInFlight[sessionID].tourID == tourID {
		delete(d.tourWakeInFlight, sessionID)
	}
}

func (d *Daemon) observeTourWakeState(sessionID, state string, observedAt time.Time) {
	d.ptyInputLocksMu.Lock()
	defer d.ptyInputLocksMu.Unlock()
	switch state {
	case protocol.StateWorking, protocol.StatePendingApproval:
		commit := d.tourWakeInFlight[sessionID]
		if !commit.startedAt.IsZero() && !observedAt.Before(commit.startedAt) {
			delete(d.tourWakeInFlight, sessionID)
		}
	}
}

func (d *Daemon) tourAgentEditorEmpty(sessionID string) bool {
	provider, ok := d.ptyBackend.(ptybackend.SnapshotProvider)
	if !ok {
		return false
	}
	info, err := provider.Snapshot(context.Background(), sessionID)
	if err != nil {
		return false
	}
	screen, _, ok := snapshotSeedScreen(info)
	if !ok || !screen.CursorVisible {
		return false
	}
	text, ok := pty.RenderedTextFromSnapshot(screen.Payload, screen.Cols, screen.Rows)
	if !ok {
		return false
	}
	lines := strings.Split(text, "\n")
	if int(screen.CursorY) >= len(lines) {
		return false
	}
	line := []rune(lines[screen.CursorY])
	cursorX := int(screen.CursorX)
	for index, char := range line {
		if char == ' ' || char == '\t' {
			continue
		}
		switch char {
		case '›', '❯', '»', '❱', '>':
			return cursorX <= index+2
		default:
			return false
		}
	}
	return false
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
			Path:           file.Path,
			OldPath:        oldPath,
			Status:         file.Status,
			Additions:      file.Additions,
			Deletions:      file.Deletions,
			Group:          file.Group,
			ChapterID:      optionalTourString(file.ChapterID),
			ChapterTitle:   optionalTourString(file.ChapterTitle),
			ChapterSummary: optionalTourString(file.ChapterSummary),
			RiskNote:       optionalTourString(file.RiskNote),
			View:           file.View,
			Note:           file.Note,
			Original:       file.Original,
			Modified:       file.Modified,
			Annotations:    annotations,
		}
	}
	return store.TourSnapshot{
		Summary:  snapshot.Summary,
		Warnings: append([]string(nil), snapshot.Warnings...),
		Files:    files,
	}
}

func optionalTourString(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return protocol.Ptr(value)
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

func (d *Daemon) handleGetTourEvent(conn net.Conn, msg *protocol.GetTourEventMessage) {
	event, run, err := d.getTourEvent(msg)
	d.sendTourSocketResult(conn, run, event, err)
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
