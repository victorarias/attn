package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
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
				"type": "string",
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
	hydrated, err := d.hydrateReviewLoopRun(run)
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

	prompt, err := d.buildReviewLoopIterationPrompt(run, interaction)
	if err != nil {
		d.failReviewLoopIteration(run, iteration, err, protocol.ReviewLoopRunStatusError, protocol.ReviewLoopIterationStatusError)
		return
	}

	iterationCtx, cancelIteration := context.WithTimeout(ctx, reviewLoopIterationTimeout())
	defer cancelIteration()

	outcome, assistantTrace, structuredJSON, resultText, err := d.executeReviewLoopPrompt(iterationCtx, run, prompt)
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
	iteration.FilesTouched = append([]string(nil), outcome.FilesTouched...)
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

func (d *Daemon) executeReviewLoopPrompt(ctx context.Context, run *protocol.ReviewLoopRun, prompt string) (*reviewLoopOutcome, string, string, string, error) {
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
		assistantText  []string
		resultText     string
		structuredJSON string
	)

	for {
		select {
		case <-ctx.Done():
			return nil, strings.Join(assistantText, "\n\n"), structuredJSON, resultText, ctx.Err()
		case err := <-client.Errors():
			if err != nil {
				d.logf("[review-loop] stream error loop=%s err=%v", run.LoopID, err)
				return nil, strings.Join(assistantText, "\n\n"), structuredJSON, resultText, err
			}
		case msg, ok := <-client.Messages():
			if !ok {
				if client.ResultReceived() {
					result := client.LastResult()
					if result != nil && result.StructuredOutput != nil {
						data, marshalErr := json.Marshal(result.StructuredOutput)
						if marshalErr != nil {
							return nil, strings.Join(assistantText, "\n\n"), structuredJSON, resultText, marshalErr
						}
						structuredJSON = string(data)
						if err := json.Unmarshal(data, &outcome); err != nil {
							d.logf("[review-loop] invalid structured output loop=%s payload=%s err=%v", run.LoopID, truncateReviewLoopLog(structuredJSON, 1200), err)
							return nil, strings.Join(assistantText, "\n\n"), structuredJSON, resultText, fmt.Errorf("%s: %w", reviewLoopStopReasonInvalidStructured, err)
						}
						d.logf("[review-loop] completed loop=%s decision=%s summary=%q structured=%s", run.LoopID, outcome.LoopDecision, truncateReviewLoopLog(outcome.Summary, 220), truncateReviewLoopLog(structuredJSON, 600))
						return &outcome, strings.Join(assistantText, "\n\n"), structuredJSON, resultText, nil
					}
				}
				d.logf("[review-loop] missing structured output loop=%s assistant_chars=%d result_text=%q", run.LoopID, len(strings.Join(assistantText, "\n\n")), truncateReviewLoopLog(resultText, 500))
				return nil, strings.Join(assistantText, "\n\n"), structuredJSON, resultText, errors.New(reviewLoopStopReasonMissingStructured)
			}

			switch typed := msg.(type) {
			case *types.AssistantMessage:
				text := strings.TrimSpace(typed.Text())
				if text != "" {
					assistantText = append(assistantText, text)
					d.logf("[review-loop] assistant loop=%s chars=%d stop_reason=%q text=%q", run.LoopID, len(text), typed.StopReason, truncateReviewLoopLog(text, 320))
				}
				if typed.HasToolCalls() {
					var toolNames []string
					for _, call := range typed.ToolCalls() {
						if call != nil {
							toolNames = append(toolNames, call.Name)
						}
					}
					d.logf("[review-loop] tool calls loop=%s tools=%q", run.LoopID, toolNames)
				}
			case *types.ResultMessage:
				if typed.Result != nil {
					resultText = strings.TrimSpace(*typed.Result)
				}
				d.logf("[review-loop] result message loop=%s subtype=%q is_error=%v result=%q", run.LoopID, typed.Subtype, typed.IsError, truncateReviewLoopLog(resultText, 500))
				if typed.StructuredOutput == nil {
					err := reviewLoopMissingStructuredError(typed)
					d.logf("[review-loop] terminal result without structured output loop=%s err=%v", run.LoopID, err)
					return nil, strings.Join(assistantText, "\n\n"), structuredJSON, resultText, err
				}
				data, marshalErr := json.Marshal(typed.StructuredOutput)
				if marshalErr != nil {
					return nil, strings.Join(assistantText, "\n\n"), structuredJSON, resultText, marshalErr
				}
				structuredJSON = string(data)
				if err := json.Unmarshal(data, &outcome); err != nil {
					d.logf("[review-loop] invalid structured output loop=%s payload=%s err=%v", run.LoopID, truncateReviewLoopLog(structuredJSON, 1200), err)
					return nil, strings.Join(assistantText, "\n\n"), structuredJSON, resultText, fmt.Errorf("%s: %w", reviewLoopStopReasonInvalidStructured, err)
				}
				d.logf("[review-loop] completed loop=%s decision=%s summary=%q structured=%s", run.LoopID, outcome.LoopDecision, truncateReviewLoopLog(outcome.Summary, 220), truncateReviewLoopLog(structuredJSON, 600))
				return &outcome, strings.Join(assistantText, "\n\n"), structuredJSON, resultText, nil
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

func (d *Daemon) sendReviewLoopResult(client *wsClient, action, sessionID string, run *protocol.ReviewLoopRun, err error) {
	if strings.TrimSpace(sessionID) == "" && run != nil {
		sessionID = run.SourceSessionID
	}
	result := protocol.ReviewLoopResultMessage{
		Event:     protocol.EventReviewLoopResult,
		Action:    action,
		SessionID: sessionID,
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
