package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/claude-agent-sdk-go/sdk"
	"github.com/victorarias/claude-agent-sdk-go/types"
)

const (
	reviewLoopStopReasonUserStopped           = "user_stopped"
	reviewLoopStopReasonSourceSessionExited   = "source_session_exited"
	reviewLoopStopReasonIterationLimitReached = "iteration_limit_reached"
	reviewLoopStopReasonIterationTimeout      = "iteration_timeout"
	reviewLoopStopReasonCancelled             = "cancelled"
	reviewLoopStopReasonMissingStructured     = "missing_structured_output"
	reviewLoopStopReasonInvalidStructured     = "invalid_structured_output"
	reviewLoopStopReasonQuestionAnswer        = "question_answer"
	reviewLoopDefaultModel                    = "claude-sonnet-4-6"
	reviewLoopQuestionKind                    = "question_answer"
	reviewLoopSDKFallbackMaxTurns             = 256
	reviewLoopDefaultIterationTimeout         = 30 * time.Minute
)

var reviewLoopOutputFormat = map[string]any{
	"type": "json_schema",
	"schema": map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required": []string{
			"loop_decision",
			"summary",
			"changes_made",
			"files_touched",
			"questions_for_user",
			"blocking_reason",
			"suggested_next_focus",
		},
		"properties": map[string]any{
			"loop_decision": map[string]any{
				"type": "string",
				"enum": []string{
					string(protocol.ReviewLoopDecisionContinue),
					string(protocol.ReviewLoopDecisionConverged),
					string(protocol.ReviewLoopDecisionNeedsUserInput),
					string(protocol.ReviewLoopDecisionError),
				},
			},
			"summary": map[string]any{
				"type":        "string",
				"description": "Markdown summary for the UI. Use short paragraphs and bullet lists when helpful. Preserve meaningful line breaks instead of collapsing everything into one paragraph.",
			},
			"changes_made": map[string]any{
				"type": "boolean",
			},
			"files_touched": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
				},
			},
			"questions_for_user": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
				},
			},
			"blocking_reason": map[string]any{
				"type": "string",
			},
			"suggested_next_focus": map[string]any{
				"type": "string",
			},
		},
	},
}

type reviewLoopOutcome struct {
	LoopDecision       protocol.ReviewLoopDecision `json:"loop_decision"`
	Summary            string                      `json:"summary"`
	ChangesMade        bool                        `json:"changes_made"`
	FilesTouched       []string                    `json:"files_touched"`
	QuestionsForUser   []string                    `json:"questions_for_user"`
	BlockingReason     string                      `json:"blocking_reason"`
	SuggestedNextFocus string                      `json:"suggested_next_focus"`
}

type reviewLoopTraceEntry struct {
	Kind    string   `json:"kind"`
	Content string   `json:"content,omitempty"`
	Tool    string   `json:"tool,omitempty"`
	Command string   `json:"command,omitempty"`
	Paths   []string `json:"paths,omitempty"`
}

type reviewLoopTracePayload struct {
	Entries []reviewLoopTraceEntry `json:"entries"`
}

func (d *Daemon) handleStartReviewLoop(conn net.Conn, msg *protocol.StartReviewLoopMessage) {
	run, err := d.startReviewLoop(msg)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.sendReviewLoopRun(conn, run)
}

func (d *Daemon) handleStopReviewLoop(conn net.Conn, msg *protocol.StopReviewLoopMessage) {
	run, err := d.stopReviewLoop(msg.SessionID, reviewLoopStopReasonUserStopped)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.sendReviewLoopRun(conn, run)
}

func (d *Daemon) handleGetReviewLoopState(conn net.Conn, msg *protocol.GetReviewLoopStateMessage) {
	run, err := d.getReviewLoopRunForSession(msg.SessionID)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.sendReviewLoopRun(conn, run)
}

func (d *Daemon) handleGetReviewLoopRun(conn net.Conn, msg *protocol.GetReviewLoopRunMessage) {
	loopID := strings.TrimSpace(msg.LoopID)
	if loopID == "" {
		d.sendError(conn, "missing loop_id")
		return
	}
	run, err := d.store.GetReviewLoopRun(loopID)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	if run == nil {
		d.sendReviewLoopRun(conn, nil)
		return
	}
	hydrated, err := d.hydrateReviewLoopRunWithIterations(run)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.sendReviewLoopRun(conn, hydrated)
}

func (d *Daemon) handleSetReviewLoopIterations(conn net.Conn, msg *protocol.SetReviewLoopIterationLimitMessage) {
	run, err := d.setReviewLoopIterationLimit(msg.SessionID, msg.IterationLimit)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.sendReviewLoopRun(conn, run)
}

func (d *Daemon) handleAnswerReviewLoop(conn net.Conn, msg *protocol.AnswerReviewLoopMessage) {
	run, err := d.answerReviewLoop(msg)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.sendReviewLoopRun(conn, run)
}

func (d *Daemon) startReviewLoop(msg *protocol.StartReviewLoopMessage) (*protocol.ReviewLoopRun, error) {
	sessionID := strings.TrimSpace(msg.SessionID)
	prompt := strings.TrimSpace(msg.Prompt)
	if sessionID == "" {
		return nil, errors.New("missing session_id")
	}
	if prompt == "" {
		return nil, errors.New("missing prompt")
	}
	if msg.IterationLimit <= 0 {
		return nil, errors.New("iteration_limit must be > 0")
	}

	session := d.store.Get(sessionID)
	if session == nil {
		return nil, errors.New("session not found")
	}

	active, err := d.store.GetActiveReviewLoopRunForSession(sessionID)
	if err != nil {
		return nil, err
	}
	if active != nil {
		return nil, errors.New("review loop already active")
	}

	now := string(protocol.TimestampNow())
	run := &protocol.ReviewLoopRun{
		LoopID:          uuid.NewString(),
		SourceSessionID: sessionID,
		RepoPath:        session.Directory,
		Status:          protocol.ReviewLoopRunStatusRunning,
		ResolvedPrompt:  prompt,
		IterationCount:  0,
		IterationLimit:  msg.IterationLimit,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if trimmed := strings.TrimSpace(protocol.Deref(msg.PresetID)); trimmed != "" {
		run.PresetID = &trimmed
	}
	run.CustomPrompt = protocol.Ptr(prompt)
	if trimmed := strings.TrimSpace(protocol.Deref(msg.HandoffPayloadJson)); trimmed != "" {
		run.HandoffPayloadJson = &trimmed
	}

	if err := d.store.UpsertReviewLoopRun(run); err != nil {
		return nil, err
	}

	hydrated, err := d.hydrateReviewLoopRun(run)
	if err != nil {
		return nil, err
	}
	d.broadcastReviewLoopUpdated(hydrated)
	d.launchReviewLoopIteration(run.LoopID)
	return hydrated, nil
}

func (d *Daemon) stopReviewLoop(sessionID, reason string) (*protocol.ReviewLoopRun, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, errors.New("missing session_id")
	}

	run, err := d.store.GetActiveReviewLoopRunForSession(sessionID)
	if err != nil {
		return nil, err
	}
	if run == nil {
		run, err = d.store.GetLatestReviewLoopRunForSession(sessionID)
		if err != nil {
			return nil, err
		}
	}
	if run == nil {
		return nil, errors.New("review loop not found")
	}

	now := string(protocol.TimestampNow())
	run.Status = protocol.ReviewLoopRunStatusStopped
	run.StopReason = protocol.Ptr(reason)
	run.UpdatedAt = now
	run.CompletedAt = protocol.Ptr(now)
	if err := d.store.UpsertReviewLoopRun(run); err != nil {
		return nil, err
	}
	d.cancelReviewLoopExecution(run.LoopID)
	hydrated, err := d.hydrateReviewLoopRun(run)
	if err != nil {
		return nil, err
	}
	d.broadcastReviewLoopUpdated(hydrated)
	return hydrated, nil
}

func (d *Daemon) getReviewLoopRunForSession(sessionID string) (*protocol.ReviewLoopRun, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, errors.New("missing session_id")
	}

	run, err := d.store.GetActiveReviewLoopRunForSession(sessionID)
	if err != nil {
		return nil, err
	}
	if run == nil {
		run, err = d.store.GetLatestReviewLoopRunForSession(sessionID)
		if err != nil {
			return nil, err
		}
	}
	if run == nil {
		return nil, nil
	}
	return d.hydrateReviewLoopRun(run)
}

func (d *Daemon) setReviewLoopIterationLimit(sessionID string, iterationLimit int) (*protocol.ReviewLoopRun, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, errors.New("missing session_id")
	}
	if iterationLimit <= 0 {
		return nil, errors.New("iteration_limit must be > 0")
	}

	run, err := d.store.GetActiveReviewLoopRunForSession(sessionID)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, errors.New("active review loop not found")
	}
	run.IterationLimit = iterationLimit
	run.UpdatedAt = string(protocol.TimestampNow())
	if err := d.store.UpsertReviewLoopRun(run); err != nil {
		return nil, err
	}
	hydrated, err := d.hydrateReviewLoopRun(run)
	if err != nil {
		return nil, err
	}
	d.broadcastReviewLoopUpdated(hydrated)
	return hydrated, nil
}

func (d *Daemon) answerReviewLoop(msg *protocol.AnswerReviewLoopMessage) (*protocol.ReviewLoopRun, error) {
	loopID := strings.TrimSpace(msg.LoopID)
	answer := strings.TrimSpace(msg.Answer)
	if loopID == "" {
		return nil, errors.New("missing loop_id")
	}
	if answer == "" {
		return nil, errors.New("missing answer")
	}

	run, err := d.store.GetReviewLoopRun(loopID)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, errors.New("review loop not found")
	}
	if run.Status != protocol.ReviewLoopRunStatusAwaitingUser {
		return nil, errors.New("review loop is not awaiting user input")
	}

	interactionID := strings.TrimSpace(protocol.Deref(msg.InteractionID))
	if interactionID == "" {
		interactionID = strings.TrimSpace(protocol.Deref(run.PendingInteractionID))
	}
	if interactionID == "" {
		return nil, errors.New("pending interaction not found")
	}

	interaction, err := d.store.GetReviewLoopInteraction(interactionID)
	if err != nil {
		return nil, err
	}
	if interaction == nil || interaction.LoopID != run.LoopID {
		return nil, errors.New("pending interaction not found")
	}
	if interaction.Status != protocol.ReviewLoopInteractionStatusPending {
		return nil, errors.New("interaction is not awaiting an answer")
	}

	now := string(protocol.TimestampNow())
	interaction.Answer = protocol.Ptr(answer)
	interaction.Status = protocol.ReviewLoopInteractionStatusAnswered
	interaction.AnsweredAt = protocol.Ptr(now)
	if err := d.store.UpsertReviewLoopInteraction(interaction); err != nil {
		return nil, err
	}

	run.Status = protocol.ReviewLoopRunStatusRunning
	run.PendingInteractionID = nil
	run.PendingInteraction = nil
	run.UpdatedAt = now
	if err := d.store.UpsertReviewLoopRun(run); err != nil {
		return nil, err
	}

	hydrated, err := d.hydrateReviewLoopRun(run)
	if err != nil {
		return nil, err
	}
	d.broadcastReviewLoopUpdated(hydrated)
	d.launchReviewLoopIteration(run.LoopID)
	return hydrated, nil
}

func (d *Daemon) launchReviewLoopIteration(loopID string) {
	go d.runReviewLoopIteration(loopID)
}

func (d *Daemon) runReviewLoopIteration(loopID string) {
	ctx, cancel := context.WithCancel(context.Background())
	d.registerReviewLoopCancel(loopID, cancel)
	defer d.unregisterReviewLoopCancel(loopID)
	defer cancel()

	run, err := d.store.GetReviewLoopRun(loopID)
	if err != nil || run == nil {
		return
	}
	if run.Status != protocol.ReviewLoopRunStatusRunning {
		return
	}

	interaction, err := d.loadLatestAnsweredInteraction(run)
	if err != nil {
		d.failReviewLoopRun(run, err, protocol.ReviewLoopRunStatusError, protocol.ReviewLoopIterationStatusError)
		return
	}

	now := string(protocol.TimestampNow())
	iteration := &protocol.ReviewLoopIteration{
		ID:              uuid.NewString(),
		LoopID:          loopID,
		IterationNumber: run.IterationCount + 1,
		Status:          protocol.ReviewLoopIterationStatusRunning,
		StartedAt:       now,
	}
	if err := d.store.UpsertReviewLoopIteration(iteration); err != nil {
		d.failReviewLoopRun(run, err, protocol.ReviewLoopRunStatusError, protocol.ReviewLoopIterationStatusError)
		return
	}

	baselineStats, baseRef, baselineErr := d.captureReviewLoopSnapshot(run.RepoPath)
	if baselineErr != nil {
		d.logf("review loop iteration baseline snapshot failed for %s: %v", run.LoopID, baselineErr)
	}

	prompt, err := d.buildReviewLoopIterationPrompt(run, interaction)
	if err != nil {
		d.failReviewLoopIteration(run, iteration, err, protocol.ReviewLoopRunStatusError, protocol.ReviewLoopIterationStatusError)
		return
	}

	iterationCtx, cancelIteration := context.WithTimeout(ctx, reviewLoopIterationTimeout())
	defer cancelIteration()

	outcome, assistantTrace, structuredJSON, resultText, err := d.executeReviewLoopPrompt(iterationCtx, run, iteration, prompt)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			d.markReviewLoopIterationCancelled(run, iteration)
			return
		}
		if errors.Is(err, context.DeadlineExceeded) {
			err = fmt.Errorf("%s: exceeded %s", reviewLoopStopReasonIterationTimeout, reviewLoopIterationTimeout())
		}
		if assistantTrace != "" {
			iteration.AssistantTraceJson = protocol.Ptr(assistantTrace)
		}
		if structuredJSON != "" {
			iteration.StructuredOutputJson = protocol.Ptr(structuredJSON)
		}
		if resultText != "" {
			iteration.ResultText = protocol.Ptr(resultText)
		}
		d.failReviewLoopIteration(run, iteration, err, protocol.ReviewLoopRunStatusError, protocol.ReviewLoopIterationStatusError)
		return
	}

	completedAt := string(protocol.TimestampNow())
	iteration.CompletedAt = &completedAt
	iteration.AssistantTraceJson = protocol.Ptr(assistantTrace)
	iteration.StructuredOutputJson = protocol.Ptr(structuredJSON)
	if strings.TrimSpace(resultText) != "" {
		iteration.ResultText = protocol.Ptr(resultText)
	}
	iteration.Decision = protocol.Ptr(outcome.LoopDecision)
	iteration.Summary = protocol.Ptr(strings.TrimSpace(outcome.Summary))
	iteration.ChangesMade = protocol.Ptr(outcome.ChangesMade)
	iteration.FilesTouched = appendUniqueReviewLoopPaths(
		iteration.FilesTouched,
		normalizeReviewLoopPaths(run.RepoPath, outcome.FilesTouched...)...,
	)
	if changeStats, snapshotErr := d.computeReviewLoopIterationChangeStats(run.RepoPath, baseRef, baselineStats); snapshotErr != nil {
		d.logf("review loop iteration end snapshot failed for %s: %v", run.LoopID, snapshotErr)
	} else {
		iteration.ChangeStats = changeStats
	}
	if trimmed := strings.TrimSpace(outcome.BlockingReason); trimmed != "" {
		iteration.BlockingReason = &trimmed
	}
	if trimmed := strings.TrimSpace(outcome.SuggestedNextFocus); trimmed != "" {
		iteration.SuggestedNextFocus = &trimmed
	}

	run.IterationCount = iteration.IterationNumber
	run.LastDecision = protocol.Ptr(outcome.LoopDecision)
	run.LastResultSummary = protocol.Ptr(strings.TrimSpace(outcome.Summary))
	run.UpdatedAt = completedAt

	switch outcome.LoopDecision {
	case protocol.ReviewLoopDecisionContinue, protocol.ReviewLoopDecisionConverged:
		iteration.Status = protocol.ReviewLoopIterationStatusCompleted
		if run.IterationCount >= run.IterationLimit {
			run.Status = protocol.ReviewLoopRunStatusCompleted
			run.StopReason = protocol.Ptr(reviewLoopStopReasonIterationLimitReached)
			run.CompletedAt = &completedAt
		} else {
			run.Status = protocol.ReviewLoopRunStatusRunning
		}

	case protocol.ReviewLoopDecisionNeedsUserInput:
		question := firstNonEmptyQuestion(outcome.QuestionsForUser, outcome.BlockingReason)
		if strings.TrimSpace(question) == "" {
			d.failReviewLoopIteration(run, iteration, errors.New("needs_user_input without question or blocking_reason"), protocol.ReviewLoopRunStatusError, protocol.ReviewLoopIterationStatusError)
			return
		}
		iteration.Status = protocol.ReviewLoopIterationStatusAwaitingUser
		interaction = &protocol.ReviewLoopInteraction{
			ID:          uuid.NewString(),
			LoopID:      run.LoopID,
			IterationID: protocol.Ptr(iteration.ID),
			Kind:        reviewLoopQuestionKind,
			Question:    question,
			Status:      protocol.ReviewLoopInteractionStatusPending,
			CreatedAt:   completedAt,
		}
		if err := d.store.UpsertReviewLoopInteraction(interaction); err != nil {
			d.failReviewLoopIteration(run, iteration, err, protocol.ReviewLoopRunStatusError, protocol.ReviewLoopIterationStatusError)
			return
		}
		run.Status = protocol.ReviewLoopRunStatusAwaitingUser
		run.PendingInteractionID = protocol.Ptr(interaction.ID)
		run.PendingInteraction = interaction

	case protocol.ReviewLoopDecisionError:
		iteration.Status = protocol.ReviewLoopIterationStatusError
		errText := strings.TrimSpace(outcome.Summary)
		if errText == "" {
			errText = "review loop iteration returned error decision"
		}
		iteration.Error = protocol.Ptr(errText)
		run.Status = protocol.ReviewLoopRunStatusError
		run.LastError = protocol.Ptr(errText)
		run.CompletedAt = &completedAt

	default:
		d.failReviewLoopIteration(run, iteration, fmt.Errorf("unsupported loop decision %q", outcome.LoopDecision), protocol.ReviewLoopRunStatusError, protocol.ReviewLoopIterationStatusError)
		return
	}

	if interaction != nil && interaction.Status == protocol.ReviewLoopInteractionStatusAnswered {
		interaction.Status = protocol.ReviewLoopInteractionStatusConsumed
		interaction.ConsumedAt = &completedAt
		if err := d.store.UpsertReviewLoopInteraction(interaction); err != nil {
			d.logf("review loop interaction consume failed for %s: %v", interaction.ID, err)
		}
	}

	if err := d.store.UpsertReviewLoopIteration(iteration); err != nil {
		d.failReviewLoopIteration(run, iteration, err, protocol.ReviewLoopRunStatusError, protocol.ReviewLoopIterationStatusError)
		return
	}
	if err := d.store.UpsertReviewLoopRun(run); err != nil {
		d.logf("review loop run upsert failed for %s: %v", run.LoopID, err)
		return
	}

	hydrated, err := d.hydrateReviewLoopRun(run)
	if err != nil {
		d.logf("review loop hydrate failed for %s: %v", run.LoopID, err)
		return
	}
	d.broadcastReviewLoopUpdated(hydrated)

	if run.Status == protocol.ReviewLoopRunStatusRunning {
		d.launchReviewLoopIteration(run.LoopID)
	}
}

func (d *Daemon) persistRunningReviewLoopIteration(run *protocol.ReviewLoopRun, iteration *protocol.ReviewLoopIteration, traceEntries []reviewLoopTraceEntry, filesTouched []string) {
	if run == nil || iteration == nil || iteration.Status != protocol.ReviewLoopIterationStatusRunning {
		return
	}

	changed := false
	trace := serializeReviewLoopTrace(traceEntries)
	if trace != "" && protocol.Deref(iteration.AssistantTraceJson) != trace {
		iteration.AssistantTraceJson = protocol.Ptr(trace)
		changed = true
	}
	if len(filesTouched) > 0 && !equalReviewLoopPaths(iteration.FilesTouched, filesTouched) {
		iteration.FilesTouched = append([]string(nil), filesTouched...)
		changed = true
	}
	if !changed {
		return
	}

	run.UpdatedAt = string(protocol.TimestampNow())
	if err := d.store.UpsertReviewLoopIteration(iteration); err != nil {
		d.logf("review loop running iteration upsert failed for %s: %v", iteration.ID, err)
		return
	}
	if err := d.store.UpsertReviewLoopRun(run); err != nil {
		d.logf("review loop running run upsert failed for %s: %v", run.LoopID, err)
		return
	}
	hydrated, err := d.hydrateReviewLoopRun(run)
	if err != nil {
		d.logf("review loop running hydrate failed for %s: %v", run.LoopID, err)
		return
	}
	d.broadcastReviewLoopUpdated(hydrated)
}

func appendUniqueReviewLoopPaths(existing []string, candidates ...string) []string {
	if len(candidates) == 0 {
		return existing
	}
	seen := make(map[string]struct{}, len(existing))
	result := append([]string(nil), existing...)
	for _, path := range existing {
		seen[path] = struct{}{}
	}
	for _, candidate := range candidates {
		trimmed := strings.TrimSpace(candidate)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func normalizeReviewLoopPaths(repoPath string, candidates ...string) []string {
	var normalized []string
	repoPath = strings.TrimSpace(repoPath)
	repoPath = filepath.Clean(repoPath)

	for _, candidate := range candidates {
		path := strings.TrimSpace(candidate)
		if path == "" {
			continue
		}

		path = filepath.Clean(path)
		path = strings.ReplaceAll(path, "\\", "/")

		if strings.HasPrefix(path, "/root/repo/") {
			path = strings.TrimPrefix(path, "/root/repo/")
		} else if path == "/root/repo" {
			path = "."
		} else if repoPath != "" {
			if rel, err := filepath.Rel(repoPath, filepath.Clean(path)); err == nil {
				rel = filepath.ToSlash(rel)
				if rel == "." {
					path = "."
				} else if !strings.HasPrefix(rel, "../") && rel != ".." {
					path = rel
				}
			}
		}

		if strings.HasPrefix(path, "./") {
			path = strings.TrimPrefix(path, "./")
		}
		normalized = append(normalized, path)
	}

	return normalized
}

func (d *Daemon) captureReviewLoopSnapshot(repoPath string) (map[string]attngit.DiffFileInfo, string, error) {
	defaultBranch, err := attngit.GetDefaultBranch(repoPath)
	if err != nil {
		return nil, "", err
	}
	baseRef := "origin/" + defaultBranch
	files, err := attngit.GetBranchDiffFiles(repoPath, baseRef)
	if err != nil {
		return nil, baseRef, err
	}
	return mapDiffFileInfoByPath(files), baseRef, nil
}

func (d *Daemon) computeReviewLoopIterationChangeStats(repoPath, baseRef string, baseline map[string]attngit.DiffFileInfo) ([]protocol.BranchDiffFile, error) {
	if baseRef == "" {
		return nil, nil
	}
	files, err := attngit.GetBranchDiffFiles(repoPath, baseRef)
	if err != nil {
		return nil, err
	}
	current := mapDiffFileInfoByPath(files)
	changedPaths := make(map[string]struct{})
	for path := range baseline {
		changedPaths[path] = struct{}{}
	}
	for path := range current {
		changedPaths[path] = struct{}{}
	}

	var result []protocol.BranchDiffFile
	for path := range changedPaths {
		before, hadBefore := baseline[path]
		after, hadAfter := current[path]

		additions := 0
		deletions := 0
		status := "modified"
		oldPath := ""

		if hadAfter {
			additions = after.Additions - before.Additions
			deletions = after.Deletions - before.Deletions
			status = after.Status
			oldPath = after.OldPath
		} else if hadBefore {
			additions = -before.Additions
			deletions = -before.Deletions
			status = "deleted"
			oldPath = before.OldPath
		}

		if additions == 0 && deletions == 0 && hadBefore == hadAfter && (!hadAfter || before.Status == after.Status) {
			continue
		}

		normalizedPath := normalizeReviewLoopPaths(repoPath, path)
		if len(normalizedPath) == 0 {
			continue
		}
		change := protocol.BranchDiffFile{
			Path:   normalizedPath[0],
			Status: status,
		}
		if oldPath != "" {
			normalizedOldPath := normalizeReviewLoopPaths(repoPath, oldPath)
			if len(normalizedOldPath) > 0 {
				change.OldPath = protocol.Ptr(normalizedOldPath[0])
			}
		}
		change.Additions = protocol.Ptr(additions)
		change.Deletions = protocol.Ptr(deletions)
		result = append(result, change)
	}

	slices.SortFunc(result, func(a, b protocol.BranchDiffFile) int {
		return strings.Compare(a.Path, b.Path)
	})

	return result, nil
}

func mapDiffFileInfoByPath(files []attngit.DiffFileInfo) map[string]attngit.DiffFileInfo {
	result := make(map[string]attngit.DiffFileInfo, len(files))
	for _, file := range files {
		result[file.Path] = file
	}
	return result
}

func equalReviewLoopPaths(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func extractReviewLoopToolPaths(call *types.ToolUseBlock) []string {
	if call == nil {
		return nil
	}

	switch call.Name {
	case "Write", "Edit", "Read":
		return extractReviewLoopPathCandidates(call.ToolInput, "file_path", "path")
	case "Grep":
		return extractReviewLoopPathCandidates(call.ToolInput, "path")
	default:
		return extractReviewLoopPathCandidates(call.ToolInput, "file_path", "path", "paths", "old_path")
	}
}

func extractReviewLoopPathCandidates(input map[string]any, keys ...string) []string {
	if len(input) == 0 {
		return nil
	}

	var paths []string
	for _, key := range keys {
		value, ok := input[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			paths = append(paths, typed)
		case []string:
			paths = append(paths, typed...)
		case []any:
			for _, item := range typed {
				if path, ok := item.(string); ok {
					paths = append(paths, path)
				}
			}
		}
	}
	return paths
}

func reviewLoopToolTraceEntry(call *types.ToolUseBlock, paths []string) reviewLoopTraceEntry {
	if call == nil {
		return reviewLoopTraceEntry{}
	}

	entry := reviewLoopTraceEntry{
		Kind: "tool",
		Tool: call.Name,
	}
	if len(paths) > 0 {
		entry.Paths = append([]string(nil), paths...)
	}
	if strings.EqualFold(call.Name, "Bash") {
		if command, ok := call.ToolInput["command"].(string); ok {
			entry.Command = strings.TrimSpace(command)
		}
	}
	return entry
}

func serializeReviewLoopTrace(entries []reviewLoopTraceEntry) string {
	filtered := make([]reviewLoopTraceEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Kind == "" {
			continue
		}
		filtered = append(filtered, entry)
	}
	if len(filtered) == 0 {
		return ""
	}
	payload, err := json.Marshal(reviewLoopTracePayload{Entries: filtered})
	if err != nil {
		var plain []string
		for _, entry := range filtered {
			switch entry.Kind {
			case "text":
				if strings.TrimSpace(entry.Content) != "" {
					plain = append(plain, entry.Content)
				}
			case "tool":
				label := entry.Tool
				if len(entry.Paths) > 0 {
					label += " → " + strings.Join(entry.Paths, ", ")
				}
				if entry.Command != "" {
					label += "\n" + entry.Command
				}
				plain = append(plain, label)
			}
		}
		return strings.Join(plain, "\n\n")
	}
	return string(payload)
}

func (d *Daemon) executeReviewLoopPrompt(ctx context.Context, run *protocol.ReviewLoopRun, iteration *protocol.ReviewLoopIteration, prompt string) (*reviewLoopOutcome, string, string, string, error) {
	if d.reviewLoopExec != nil {
		return d.reviewLoopExec(ctx, run, prompt)
	}

	model := strings.TrimSpace(os.Getenv("ATTN_REVIEW_LOOP_MODEL"))
	if configured := strings.TrimSpace(d.store.GetSetting(SettingReviewLoopModel)); configured != "" {
		model = configured
	}
	if model == "" {
		model = reviewLoopDefaultModel
	}

	opts := []types.Option{
		types.WithModel(model),
		types.WithCwd(run.RepoPath),
		types.WithOutputFormat(reviewLoopOutputFormat),
		types.WithMaxTurns(reviewLoopSDKFallbackMaxTurns),
		types.WithTools("Bash", "Read", "Write", "Edit", "Glob", "Grep"),
		types.WithAllowedTools("Bash", "Read", "Write", "Edit", "Glob", "Grep"),
	}
	client := sdk.NewClient(opts...)
	d.logf("[review-loop] connect loop=%s repo=%s model=%s timeout=%s fallback_max_turns=%d", run.LoopID, run.RepoPath, model, reviewLoopIterationTimeout(), reviewLoopSDKFallbackMaxTurns)
	if err := client.Connect(ctx); err != nil {
		d.logf("[review-loop] connect failed loop=%s err=%v", run.LoopID, err)
		return nil, "", "", "", err
	}
	defer client.Close()

	d.logf("[review-loop] send query loop=%s prompt_chars=%d", run.LoopID, len(prompt))
	err := client.SendQuery(prompt)
	if err != nil {
		d.logf("[review-loop] send query failed loop=%s err=%v", run.LoopID, err)
		return nil, "", "", "", err
	}

	var (
		outcome        reviewLoopOutcome
		traceEntries   []reviewLoopTraceEntry
		liveFiles      []string
		resultText     string
		structuredJSON string
	)

	for {
		select {
		case <-ctx.Done():
			return nil, serializeReviewLoopTrace(traceEntries), structuredJSON, resultText, ctx.Err()
		case err := <-client.Errors():
			if err != nil {
				d.logf("[review-loop] stream error loop=%s err=%v", run.LoopID, err)
				return nil, serializeReviewLoopTrace(traceEntries), structuredJSON, resultText, err
			}
		case msg, ok := <-client.Messages():
			if !ok {
				if client.ResultReceived() {
					result := client.LastResult()
					if result != nil && result.StructuredOutput != nil {
						data, marshalErr := json.Marshal(result.StructuredOutput)
						if marshalErr != nil {
							return nil, serializeReviewLoopTrace(traceEntries), structuredJSON, resultText, marshalErr
						}
						structuredJSON = string(data)
						if err := json.Unmarshal(data, &outcome); err != nil {
							d.logf("[review-loop] invalid structured output loop=%s payload=%s err=%v", run.LoopID, truncateReviewLoopLog(structuredJSON, 1200), err)
							return nil, serializeReviewLoopTrace(traceEntries), structuredJSON, resultText, fmt.Errorf("%s: %w", reviewLoopStopReasonInvalidStructured, err)
						}
						d.logf("[review-loop] completed loop=%s decision=%s summary=%q structured=%s", run.LoopID, outcome.LoopDecision, truncateReviewLoopLog(outcome.Summary, 220), truncateReviewLoopLog(structuredJSON, 600))
						return &outcome, serializeReviewLoopTrace(traceEntries), structuredJSON, resultText, nil
					}
				}
				serializedTrace := serializeReviewLoopTrace(traceEntries)
				d.logf("[review-loop] missing structured output loop=%s assistant_chars=%d result_text=%q", run.LoopID, len(serializedTrace), truncateReviewLoopLog(resultText, 500))
				return nil, serializedTrace, structuredJSON, resultText, errors.New(reviewLoopStopReasonMissingStructured)
			}

			switch typed := msg.(type) {
			case *types.AssistantMessage:
				text := strings.TrimSpace(typed.Text())
				if text != "" {
					traceEntries = append(traceEntries, reviewLoopTraceEntry{Kind: "text", Content: text})
					d.logf("[review-loop] assistant loop=%s chars=%d stop_reason=%q text=%q", run.LoopID, len(text), typed.StopReason, truncateReviewLoopLog(text, 320))
				}
				if typed.HasToolCalls() {
					var toolNames []string
					for _, call := range typed.ToolCalls() {
						if call != nil {
							toolNames = append(toolNames, call.Name)
							toolPaths := normalizeReviewLoopPaths(run.RepoPath, extractReviewLoopToolPaths(call)...)
							liveFiles = appendUniqueReviewLoopPaths(liveFiles, toolPaths...)
							traceEntries = append(traceEntries, reviewLoopToolTraceEntry(call, toolPaths))
						}
					}
					d.logf("[review-loop] tool calls loop=%s tools=%q", run.LoopID, toolNames)
				}
				d.persistRunningReviewLoopIteration(run, iteration, traceEntries, liveFiles)
			case *types.ResultMessage:
				if typed.Result != nil {
					resultText = strings.TrimSpace(*typed.Result)
				}
				d.logf("[review-loop] result message loop=%s subtype=%q is_error=%v result=%q", run.LoopID, typed.Subtype, typed.IsError, truncateReviewLoopLog(resultText, 500))
				if typed.StructuredOutput == nil {
					err := reviewLoopMissingStructuredError(typed)
					d.logf("[review-loop] terminal result without structured output loop=%s err=%v", run.LoopID, err)
					return nil, serializeReviewLoopTrace(traceEntries), structuredJSON, resultText, err
				}
				data, marshalErr := json.Marshal(typed.StructuredOutput)
				if marshalErr != nil {
					return nil, serializeReviewLoopTrace(traceEntries), structuredJSON, resultText, marshalErr
				}
				structuredJSON = string(data)
				if err := json.Unmarshal(data, &outcome); err != nil {
					d.logf("[review-loop] invalid structured output loop=%s payload=%s err=%v", run.LoopID, truncateReviewLoopLog(structuredJSON, 1200), err)
					return nil, serializeReviewLoopTrace(traceEntries), structuredJSON, resultText, fmt.Errorf("%s: %w", reviewLoopStopReasonInvalidStructured, err)
				}
				d.logf("[review-loop] completed loop=%s decision=%s summary=%q structured=%s", run.LoopID, outcome.LoopDecision, truncateReviewLoopLog(outcome.Summary, 220), truncateReviewLoopLog(structuredJSON, 600))
				return &outcome, serializeReviewLoopTrace(traceEntries), structuredJSON, resultText, nil
			default:
				d.logf("[review-loop] message loop=%s type=%T", run.LoopID, msg)
			}
		}
	}
}

func (d *Daemon) buildReviewLoopIterationPrompt(run *protocol.ReviewLoopRun, answeredInteraction *protocol.ReviewLoopInteraction) (string, error) {
	var sections []string
	sections = append(sections,
		"You are running attn's SDK-managed review loop for this repository.",
		"Review the current working tree, make safe fixes directly when appropriate, and produce your final result using the required structured output schema.",
	)

	if prompt := strings.TrimSpace(run.ResolvedPrompt); prompt != "" {
		sections = append(sections, "Loop instructions:\n"+prompt)
	}
	if handoff := strings.TrimSpace(protocol.Deref(run.HandoffPayloadJson)); handoff != "" {
		sections = append(sections, "Structured handoff JSON:\n"+handoff)
	}

	iterations, err := d.store.ListReviewLoopIterations(run.LoopID)
	if err != nil {
		return "", err
	}
	if len(iterations) > 0 {
		var prior []string
		for _, iteration := range iterations {
			if iteration == nil {
				continue
			}
			summary := strings.TrimSpace(protocol.Deref(iteration.Summary))
			decision := strings.TrimSpace(string(protocol.Deref(iteration.Decision)))
			if summary == "" && decision == "" {
				continue
			}
			line := fmt.Sprintf("Iteration %d", iteration.IterationNumber)
			if decision != "" {
				line += " [" + decision + "]"
			}
			if summary != "" {
				line += ": " + summary
			}
			prior = append(prior, line)
		}
		if len(prior) > 0 {
			sections = append(sections, "Previous iteration summaries:\n"+strings.Join(prior, "\n"))
		}
	}

	if answeredInteraction != nil && answeredInteraction.Status == protocol.ReviewLoopInteractionStatusAnswered {
		question := strings.TrimSpace(answeredInteraction.Question)
		answer := strings.TrimSpace(protocol.Deref(answeredInteraction.Answer))
		if question != "" || answer != "" {
			sections = append(sections, fmt.Sprintf("User clarification:\nQuestion: %s\nAnswer: %s", question, answer))
		}
	}

	sections = append(sections,
		"When deciding loop_decision:",
		"- use \"continue\" if this pass found more work or uncertainty worth another fresh review pass",
		"- use \"converged\" if this pass appears clean and stable",
		"- use \"needs_user_input\" only if you cannot safely continue without a human answer",
		"- use \"error\" only if the pass could not be completed reliably",
		"The daemon owns overall iteration count. A \"converged\" result describes this pass; it does not necessarily end the full loop before the configured number of passes is reached.",
		"If you need user input, fill questions_for_user with at least one specific question and explain the block in blocking_reason.",
		"For summary, write readable markdown for the UI.",
		"- use headings, bullet lists, and short paragraphs when useful",
		"- keep explicit line breaks and blank lines where they improve readability",
		"- do not compress a multi-point summary into one long paragraph",
	)

	return strings.Join(sections, "\n\n"), nil
}

func (d *Daemon) sendReviewLoopRun(conn net.Conn, run *protocol.ReviewLoopRun) {
	resp := protocol.Response{Ok: true}
	if run != nil {
		resp.ReviewLoopRun = run
	}
	_ = json.NewEncoder(conn).Encode(resp)
}

func (d *Daemon) broadcastReviewLoopUpdated(run *protocol.ReviewLoopRun) {
	if run == nil {
		return
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:         protocol.EventReviewLoopUpdated,
		SessionID:     protocol.Ptr(run.SourceSessionID),
		ReviewLoopRun: run,
	})
}

func (d *Daemon) hydrateReviewLoopRun(run *protocol.ReviewLoopRun) (*protocol.ReviewLoopRun, error) {
	if run == nil {
		return nil, nil
	}
	copyRun := *run
	copyRun.PendingInteraction = nil
	copyRun.LatestIteration = nil
	if copyRun.PendingInteractionID != nil && strings.TrimSpace(*copyRun.PendingInteractionID) != "" {
		interaction, err := d.store.GetReviewLoopInteraction(*copyRun.PendingInteractionID)
		if err != nil {
			return nil, err
		}
		copyRun.PendingInteraction = interaction
	}
	latestIteration, err := d.store.GetLatestReviewLoopIteration(copyRun.LoopID)
	if err != nil {
		return nil, err
	}
	copyRun.LatestIteration = latestIteration
	return &copyRun, nil
}

func (d *Daemon) hydrateReviewLoopRunWithIterations(run *protocol.ReviewLoopRun) (*protocol.ReviewLoopRun, error) {
	hydrated, err := d.hydrateReviewLoopRun(run)
	if err != nil || hydrated == nil {
		return hydrated, err
	}
	iterations, err := d.store.ListReviewLoopIterations(hydrated.LoopID)
	if err != nil {
		return nil, err
	}
	hydrated.Iterations = make([]protocol.ReviewLoopIteration, 0, len(iterations))
	for _, iteration := range iterations {
		if iteration != nil {
			hydrated.Iterations = append(hydrated.Iterations, *iteration)
		}
	}
	return hydrated, nil
}

func (d *Daemon) sendReviewLoopResult(client *wsClient, action, sessionID, loopID string, run *protocol.ReviewLoopRun, err error) {
	if strings.TrimSpace(sessionID) == "" && run != nil {
		sessionID = run.SourceSessionID
	}
	if strings.TrimSpace(loopID) == "" && run != nil {
		loopID = run.LoopID
	}
	result := protocol.ReviewLoopResultMessage{
		Event:     protocol.EventReviewLoopResult,
		Action:    action,
		SessionID: sessionID,
		LoopID:    protocol.Ptr(loopID),
		Success:   err == nil,
	}
	if run != nil {
		result.ReviewLoopRun = run
	}
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, result)
}

func (d *Daemon) loadLatestAnsweredInteraction(run *protocol.ReviewLoopRun) (*protocol.ReviewLoopInteraction, error) {
	interactions, err := d.store.ListReviewLoopInteractions(run.LoopID)
	if err != nil {
		return nil, err
	}
	for i := len(interactions) - 1; i >= 0; i-- {
		interaction := interactions[i]
		if interaction != nil && interaction.Status == protocol.ReviewLoopInteractionStatusAnswered {
			return interaction, nil
		}
	}
	return nil, nil
}

func (d *Daemon) failReviewLoopRun(run *protocol.ReviewLoopRun, err error, runStatus protocol.ReviewLoopRunStatus, iterationStatus protocol.ReviewLoopIterationStatus) {
	if run == nil || err == nil {
		return
	}
	iteration := &protocol.ReviewLoopIteration{
		ID:              uuid.NewString(),
		LoopID:          run.LoopID,
		IterationNumber: run.IterationCount + 1,
		Status:          iterationStatus,
		Error:           protocol.Ptr(err.Error()),
		StartedAt:       string(protocol.TimestampNow()),
		CompletedAt:     protocol.Ptr(string(protocol.TimestampNow())),
	}
	d.failReviewLoopIteration(run, iteration, err, runStatus, iterationStatus)
}

func (d *Daemon) failReviewLoopIteration(run *protocol.ReviewLoopRun, iteration *protocol.ReviewLoopIteration, err error, runStatus protocol.ReviewLoopRunStatus, iterationStatus protocol.ReviewLoopIterationStatus) {
	if run == nil || iteration == nil || err == nil {
		return
	}
	now := string(protocol.TimestampNow())
	iteration.Status = iterationStatus
	iteration.Error = protocol.Ptr(err.Error())
	if iteration.StartedAt == "" {
		iteration.StartedAt = now
	}
	iteration.CompletedAt = protocol.Ptr(now)
	_ = d.store.UpsertReviewLoopIteration(iteration)

	run.Status = runStatus
	run.LastError = protocol.Ptr(err.Error())
	run.UpdatedAt = now
	run.CompletedAt = protocol.Ptr(now)
	_ = d.store.UpsertReviewLoopRun(run)

	if hydrated, hydrateErr := d.hydrateReviewLoopRun(run); hydrateErr == nil {
		d.broadcastReviewLoopUpdated(hydrated)
	}
}

func (d *Daemon) markReviewLoopIterationCancelled(run *protocol.ReviewLoopRun, iteration *protocol.ReviewLoopIteration) {
	if run == nil || iteration == nil {
		return
	}
	now := string(protocol.TimestampNow())
	iteration.Status = protocol.ReviewLoopIterationStatusCancelled
	iteration.CompletedAt = protocol.Ptr(now)
	if iteration.Error == nil {
		iteration.Error = protocol.Ptr(reviewLoopStopReasonCancelled)
	}
	_ = d.store.UpsertReviewLoopIteration(iteration)
}

func firstNonEmptyQuestion(questions []string, blockingReason string) string {
	for _, question := range questions {
		trimmed := strings.TrimSpace(question)
		if trimmed != "" {
			return trimmed
		}
	}
	return strings.TrimSpace(blockingReason)
}

func truncateReviewLoopLog(value string, maxChars int) string {
	value = strings.TrimSpace(value)
	if maxChars <= 0 || len(value) <= maxChars {
		return value
	}
	return value[:maxChars] + "...(truncated)"
}

func reviewLoopIterationTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("ATTN_REVIEW_LOOP_TIMEOUT_MINUTES"))
	if raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return time.Duration(n) * time.Minute
		}
	}
	return reviewLoopDefaultIterationTimeout
}

func reviewLoopMissingStructuredError(result *types.ResultMessage) error {
	if result == nil {
		return errors.New(reviewLoopStopReasonMissingStructured)
	}
	subtype := strings.TrimSpace(result.Subtype)
	if subtype == "" {
		return errors.New(reviewLoopStopReasonMissingStructured)
	}
	return fmt.Errorf("%s: result_subtype=%s", reviewLoopStopReasonMissingStructured, subtype)
}

func (d *Daemon) cancelReviewLoopExecution(loopID string) {
	d.reviewLoopMu.Lock()
	cancel := d.reviewLoopCancel[loopID]
	d.reviewLoopMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (d *Daemon) registerReviewLoopCancel(loopID string, cancel context.CancelFunc) {
	if strings.TrimSpace(loopID) == "" || cancel == nil {
		return
	}
	d.reviewLoopMu.Lock()
	defer d.reviewLoopMu.Unlock()
	d.reviewLoopCancel[loopID] = cancel
}

func (d *Daemon) unregisterReviewLoopCancel(loopID string) {
	d.reviewLoopMu.Lock()
	defer d.reviewLoopMu.Unlock()
	delete(d.reviewLoopCancel, loopID)
}

func (d *Daemon) handleReviewLoopSourceSessionExit(sessionID string) {
	run, err := d.store.GetActiveReviewLoopRunForSession(strings.TrimSpace(sessionID))
	if err != nil || run == nil {
		return
	}
	_, _ = d.stopReviewLoop(run.SourceSessionID, reviewLoopStopReasonSourceSessionExited)
}

func (d *Daemon) setPendingInputSource(sessionID, source string) {
	if sessionID == "" {
		return
	}
	d.inputSourceMu.Lock()
	defer d.inputSourceMu.Unlock()
	if strings.TrimSpace(source) == "" {
		delete(d.pendingInputSrc, sessionID)
		return
	}
	d.pendingInputSrc[sessionID] = source
}

func (d *Daemon) takePendingInputSource(sessionID string) string {
	d.inputSourceMu.Lock()
	defer d.inputSourceMu.Unlock()
	source := d.pendingInputSrc[sessionID]
	delete(d.pendingInputSrc, sessionID)
	return source
}
