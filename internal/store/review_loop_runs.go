package store

import (
	"database/sql"
	"encoding/json"

	"github.com/victorarias/attn/internal/protocol"
)

type reviewLoopScanner interface {
	Scan(dest ...any) error
}

// UpsertReviewLoopRun creates or updates a persisted SDK review-loop run.
func (s *Store) UpsertReviewLoopRun(run *protocol.ReviewLoopRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil || run == nil {
		return nil
	}

	_, err := s.db.Exec(`
		INSERT INTO review_loop_runs (
			id, source_session_id, repo_path, status, preset_id, custom_prompt,
			resolved_prompt, handoff_payload_json, iteration_count, iteration_limit,
			pending_interaction_id, last_decision, last_result_summary, last_error,
			stop_reason, created_at, updated_at, completed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			source_session_id = excluded.source_session_id,
			repo_path = excluded.repo_path,
			status = excluded.status,
			preset_id = excluded.preset_id,
			custom_prompt = excluded.custom_prompt,
			resolved_prompt = excluded.resolved_prompt,
			handoff_payload_json = excluded.handoff_payload_json,
			iteration_count = excluded.iteration_count,
			iteration_limit = excluded.iteration_limit,
			pending_interaction_id = excluded.pending_interaction_id,
			last_decision = excluded.last_decision,
			last_result_summary = excluded.last_result_summary,
			last_error = excluded.last_error,
			stop_reason = excluded.stop_reason,
			created_at = excluded.created_at,
			updated_at = excluded.updated_at,
			completed_at = excluded.completed_at
	`,
		run.LoopID,
		run.SourceSessionID,
		run.RepoPath,
		string(run.Status),
		nullPtrString(run.PresetID),
		nullPtrString(run.CustomPrompt),
		run.ResolvedPrompt,
		nullPtrString(run.HandoffPayloadJson),
		run.IterationCount,
		run.IterationLimit,
		nullPtrString(run.PendingInteractionID),
		nullReviewLoopDecision(run.LastDecision),
		nullPtrString(run.LastResultSummary),
		nullPtrString(run.LastError),
		nullPtrString(run.StopReason),
		run.CreatedAt,
		run.UpdatedAt,
		nullPtrString(run.CompletedAt),
	)
	return err
}

// GetReviewLoopRun returns a review-loop run by ID.
func (s *Store) GetReviewLoopRun(loopID string) (*protocol.ReviewLoopRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, nil
	}

	row := s.db.QueryRow(`
		SELECT id, source_session_id, repo_path, status, preset_id, custom_prompt,
			resolved_prompt, handoff_payload_json, iteration_count, iteration_limit,
			pending_interaction_id, last_decision, last_result_summary, last_error,
			stop_reason, created_at, updated_at, completed_at
		FROM review_loop_runs
		WHERE id = ?
	`, loopID)

	run, err := scanReviewLoopRun(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return run, nil
}

// GetActiveReviewLoopRunForSession returns the latest active run for a source session.
func (s *Store) GetActiveReviewLoopRunForSession(sessionID string) (*protocol.ReviewLoopRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, nil
	}

	row := s.db.QueryRow(`
		SELECT id, source_session_id, repo_path, status, preset_id, custom_prompt,
			resolved_prompt, handoff_payload_json, iteration_count, iteration_limit,
			pending_interaction_id, last_decision, last_result_summary, last_error,
			stop_reason, created_at, updated_at, completed_at
		FROM review_loop_runs
		WHERE source_session_id = ? AND status IN (?, ?)
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, sessionID, protocol.ReviewLoopRunStatusRunning, protocol.ReviewLoopRunStatusAwaitingUser)

	run, err := scanReviewLoopRun(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return run, nil
}

// ListReviewLoopRunsForSession returns all persisted runs for a source session.
func (s *Store) ListReviewLoopRunsForSession(sessionID string) ([]*protocol.ReviewLoopRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, nil
	}

	rows, err := s.db.Query(`
		SELECT id, source_session_id, repo_path, status, preset_id, custom_prompt,
			resolved_prompt, handoff_payload_json, iteration_count, iteration_limit,
			pending_interaction_id, last_decision, last_result_summary, last_error,
			stop_reason, created_at, updated_at, completed_at
		FROM review_loop_runs
		WHERE source_session_id = ?
		ORDER BY created_at ASC, id ASC
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []*protocol.ReviewLoopRun
	for rows.Next() {
		run, err := scanReviewLoopRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

// GetLatestReviewLoopRunForSession returns the most recent run for a source session.
func (s *Store) GetLatestReviewLoopRunForSession(sessionID string) (*protocol.ReviewLoopRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, nil
	}

	row := s.db.QueryRow(`
		SELECT id, source_session_id, repo_path, status, preset_id, custom_prompt,
			resolved_prompt, handoff_payload_json, iteration_count, iteration_limit,
			pending_interaction_id, last_decision, last_result_summary, last_error,
			stop_reason, created_at, updated_at, completed_at
		FROM review_loop_runs
		WHERE source_session_id = ?
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, sessionID)

	run, err := scanReviewLoopRun(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return run, nil
}

// DeleteReviewLoopRun removes a persisted run and cascades its child rows.
func (s *Store) DeleteReviewLoopRun(loopID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}

	if _, err := s.db.Exec(`DELETE FROM review_loop_interactions WHERE loop_id = ?`, loopID); err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM review_loop_iterations WHERE loop_id = ?`, loopID); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM review_loop_runs WHERE id = ?`, loopID)
	return err
}

// UpsertReviewLoopIteration creates or updates a persisted review-loop iteration.
func (s *Store) UpsertReviewLoopIteration(iteration *protocol.ReviewLoopIteration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil || iteration == nil {
		return nil
	}

	filesTouchedJSON, err := marshalStringSlice(iteration.FilesTouched)
	if err != nil {
		return err
	}
	changeStatsJSON, err := marshalBranchDiffFiles(iteration.ChangeStats)
	if err != nil {
		return err
	}

	_, err = s.db.Exec(`
		INSERT INTO review_loop_iterations (
			id, loop_id, iteration_number, status, decision, summary, result_text, changes_made,
			files_touched_json, change_stats_json, blocking_reason, suggested_next_focus,
			structured_output_json, assistant_trace_json, error, started_at, completed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			loop_id = excluded.loop_id,
			iteration_number = excluded.iteration_number,
			status = excluded.status,
			decision = excluded.decision,
			summary = excluded.summary,
			result_text = excluded.result_text,
			changes_made = excluded.changes_made,
			files_touched_json = excluded.files_touched_json,
			change_stats_json = excluded.change_stats_json,
			blocking_reason = excluded.blocking_reason,
			suggested_next_focus = excluded.suggested_next_focus,
			structured_output_json = excluded.structured_output_json,
			assistant_trace_json = excluded.assistant_trace_json,
			error = excluded.error,
			started_at = excluded.started_at,
			completed_at = excluded.completed_at
	`,
		iteration.ID,
		iteration.LoopID,
		iteration.IterationNumber,
		string(iteration.Status),
		nullReviewLoopDecision(iteration.Decision),
		nullPtrString(iteration.Summary),
		nullPtrString(iteration.ResultText),
		nullBool(iteration.ChangesMade),
		filesTouchedJSON,
		changeStatsJSON,
		nullPtrString(iteration.BlockingReason),
		nullPtrString(iteration.SuggestedNextFocus),
		nullPtrString(iteration.StructuredOutputJson),
		nullPtrString(iteration.AssistantTraceJson),
		nullPtrString(iteration.Error),
		iteration.StartedAt,
		nullPtrString(iteration.CompletedAt),
	)
	return err
}

// GetReviewLoopIteration returns a persisted review-loop iteration by ID.
func (s *Store) GetReviewLoopIteration(iterationID string) (*protocol.ReviewLoopIteration, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, nil
	}

	row := s.db.QueryRow(`
		SELECT id, loop_id, iteration_number, status, decision, summary, result_text, changes_made,
			files_touched_json, change_stats_json, blocking_reason, suggested_next_focus,
			structured_output_json, assistant_trace_json, error, started_at, completed_at
		FROM review_loop_iterations
		WHERE id = ?
	`, iterationID)

	iteration, err := scanReviewLoopIteration(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return iteration, nil
}

// ListReviewLoopIterations returns all iterations for a run in iteration order.
func (s *Store) ListReviewLoopIterations(loopID string) ([]*protocol.ReviewLoopIteration, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, nil
	}

	rows, err := s.db.Query(`
		SELECT id, loop_id, iteration_number, status, decision, summary, result_text, changes_made,
			files_touched_json, change_stats_json, blocking_reason, suggested_next_focus,
			structured_output_json, assistant_trace_json, error, started_at, completed_at
		FROM review_loop_iterations
		WHERE loop_id = ?
		ORDER BY iteration_number ASC, started_at ASC, id ASC
	`, loopID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var iterations []*protocol.ReviewLoopIteration
	for rows.Next() {
		iteration, err := scanReviewLoopIteration(rows)
		if err != nil {
			return nil, err
		}
		iterations = append(iterations, iteration)
	}
	return iterations, rows.Err()
}

// GetLatestReviewLoopIteration returns the most recent iteration for a run.
func (s *Store) GetLatestReviewLoopIteration(loopID string) (*protocol.ReviewLoopIteration, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, nil
	}

	row := s.db.QueryRow(`
		SELECT id, loop_id, iteration_number, status, decision, summary, result_text, changes_made,
			files_touched_json, change_stats_json, blocking_reason, suggested_next_focus,
			structured_output_json, assistant_trace_json, error, started_at, completed_at
		FROM review_loop_iterations
		WHERE loop_id = ?
		ORDER BY iteration_number DESC, started_at DESC, id DESC
		LIMIT 1
	`, loopID)

	iteration, err := scanReviewLoopIteration(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return iteration, nil
}

// UpsertReviewLoopInteraction creates or updates a persisted user interaction.
func (s *Store) UpsertReviewLoopInteraction(interaction *protocol.ReviewLoopInteraction) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil || interaction == nil {
		return nil
	}

	_, err := s.db.Exec(`
		INSERT INTO review_loop_interactions (
			id, loop_id, iteration_id, kind, question, answer, status,
			created_at, answered_at, consumed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			loop_id = excluded.loop_id,
			iteration_id = excluded.iteration_id,
			kind = excluded.kind,
			question = excluded.question,
			answer = excluded.answer,
			status = excluded.status,
			created_at = excluded.created_at,
			answered_at = excluded.answered_at,
			consumed_at = excluded.consumed_at
	`,
		interaction.ID,
		interaction.LoopID,
		nullPtrString(interaction.IterationID),
		interaction.Kind,
		interaction.Question,
		nullPtrString(interaction.Answer),
		string(interaction.Status),
		interaction.CreatedAt,
		nullPtrString(interaction.AnsweredAt),
		nullPtrString(interaction.ConsumedAt),
	)
	return err
}

// GetReviewLoopInteraction returns a persisted interaction by ID.
func (s *Store) GetReviewLoopInteraction(interactionID string) (*protocol.ReviewLoopInteraction, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, nil
	}

	row := s.db.QueryRow(`
		SELECT id, loop_id, iteration_id, kind, question, answer, status,
			created_at, answered_at, consumed_at
		FROM review_loop_interactions
		WHERE id = ?
	`, interactionID)

	interaction, err := scanReviewLoopInteraction(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return interaction, nil
}

// ListReviewLoopInteractions returns all interactions for a run.
func (s *Store) ListReviewLoopInteractions(loopID string) ([]*protocol.ReviewLoopInteraction, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, nil
	}

	rows, err := s.db.Query(`
		SELECT id, loop_id, iteration_id, kind, question, answer, status,
			created_at, answered_at, consumed_at
		FROM review_loop_interactions
		WHERE loop_id = ?
		ORDER BY created_at ASC, id ASC
	`, loopID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var interactions []*protocol.ReviewLoopInteraction
	for rows.Next() {
		interaction, err := scanReviewLoopInteraction(rows)
		if err != nil {
			return nil, err
		}
		interactions = append(interactions, interaction)
	}
	return interactions, rows.Err()
}

func scanReviewLoopRun(scanner reviewLoopScanner) (*protocol.ReviewLoopRun, error) {
	var (
		run                  protocol.ReviewLoopRun
		status               string
		presetID             sql.NullString
		customPrompt         sql.NullString
		handoffPayloadJSON   sql.NullString
		pendingInteractionID sql.NullString
		lastDecision         sql.NullString
		lastResultSummary    sql.NullString
		lastError            sql.NullString
		stopReason           sql.NullString
		completedAt          sql.NullString
	)

	if err := scanner.Scan(
		&run.LoopID,
		&run.SourceSessionID,
		&run.RepoPath,
		&status,
		&presetID,
		&customPrompt,
		&run.ResolvedPrompt,
		&handoffPayloadJSON,
		&run.IterationCount,
		&run.IterationLimit,
		&pendingInteractionID,
		&lastDecision,
		&lastResultSummary,
		&lastError,
		&stopReason,
		&run.CreatedAt,
		&run.UpdatedAt,
		&completedAt,
	); err != nil {
		return nil, err
	}

	run.Status = protocol.ReviewLoopRunStatus(status)
	if presetID.Valid && presetID.String != "" {
		run.PresetID = &presetID.String
	}
	if customPrompt.Valid && customPrompt.String != "" {
		run.CustomPrompt = &customPrompt.String
	}
	if handoffPayloadJSON.Valid && handoffPayloadJSON.String != "" {
		run.HandoffPayloadJson = &handoffPayloadJSON.String
	}
	if pendingInteractionID.Valid && pendingInteractionID.String != "" {
		run.PendingInteractionID = &pendingInteractionID.String
	}
	if lastDecision.Valid && lastDecision.String != "" {
		decision := protocol.ReviewLoopDecision(lastDecision.String)
		run.LastDecision = &decision
	}
	if lastResultSummary.Valid && lastResultSummary.String != "" {
		run.LastResultSummary = &lastResultSummary.String
	}
	if lastError.Valid && lastError.String != "" {
		run.LastError = &lastError.String
	}
	if stopReason.Valid && stopReason.String != "" {
		run.StopReason = &stopReason.String
	}
	if completedAt.Valid && completedAt.String != "" {
		run.CompletedAt = &completedAt.String
	}

	return &run, nil
}

func scanReviewLoopIteration(scanner reviewLoopScanner) (*protocol.ReviewLoopIteration, error) {
	var (
		iteration            protocol.ReviewLoopIteration
		status               string
		decision             sql.NullString
		summary              sql.NullString
		resultText           sql.NullString
		changesMade          sql.NullInt64
		filesTouchedJSON     sql.NullString
		changeStatsJSON      sql.NullString
		blockingReason       sql.NullString
		suggestedNextFocus   sql.NullString
		structuredOutputJSON sql.NullString
		assistantTraceJSON   sql.NullString
		iterationError       sql.NullString
		completedAt          sql.NullString
	)

	if err := scanner.Scan(
		&iteration.ID,
		&iteration.LoopID,
		&iteration.IterationNumber,
		&status,
		&decision,
		&summary,
		&resultText,
		&changesMade,
		&filesTouchedJSON,
		&changeStatsJSON,
		&blockingReason,
		&suggestedNextFocus,
		&structuredOutputJSON,
		&assistantTraceJSON,
		&iterationError,
		&iteration.StartedAt,
		&completedAt,
	); err != nil {
		return nil, err
	}

	iteration.Status = protocol.ReviewLoopIterationStatus(status)
	if decision.Valid && decision.String != "" {
		value := protocol.ReviewLoopDecision(decision.String)
		iteration.Decision = &value
	}
	if summary.Valid && summary.String != "" {
		iteration.Summary = &summary.String
	}
	if resultText.Valid && resultText.String != "" {
		iteration.ResultText = &resultText.String
	}
	if changesMade.Valid {
		value := changesMade.Int64 == 1
		iteration.ChangesMade = &value
	}
	if filesTouchedJSON.Valid && filesTouchedJSON.String != "" {
		if err := json.Unmarshal([]byte(filesTouchedJSON.String), &iteration.FilesTouched); err != nil {
			return nil, err
		}
	}
	if changeStatsJSON.Valid && changeStatsJSON.String != "" {
		if err := json.Unmarshal([]byte(changeStatsJSON.String), &iteration.ChangeStats); err != nil {
			return nil, err
		}
	}
	if blockingReason.Valid && blockingReason.String != "" {
		iteration.BlockingReason = &blockingReason.String
	}
	if suggestedNextFocus.Valid && suggestedNextFocus.String != "" {
		iteration.SuggestedNextFocus = &suggestedNextFocus.String
	}
	if structuredOutputJSON.Valid && structuredOutputJSON.String != "" {
		iteration.StructuredOutputJson = &structuredOutputJSON.String
	}
	if assistantTraceJSON.Valid && assistantTraceJSON.String != "" {
		iteration.AssistantTraceJson = &assistantTraceJSON.String
	}
	if iterationError.Valid && iterationError.String != "" {
		iteration.Error = &iterationError.String
	}
	if completedAt.Valid && completedAt.String != "" {
		iteration.CompletedAt = &completedAt.String
	}

	return &iteration, nil
}

func scanReviewLoopInteraction(scanner reviewLoopScanner) (*protocol.ReviewLoopInteraction, error) {
	var (
		interaction protocol.ReviewLoopInteraction
		iterationID sql.NullString
		answer      sql.NullString
		status      string
		answeredAt  sql.NullString
		consumedAt  sql.NullString
	)

	if err := scanner.Scan(
		&interaction.ID,
		&interaction.LoopID,
		&iterationID,
		&interaction.Kind,
		&interaction.Question,
		&answer,
		&status,
		&interaction.CreatedAt,
		&answeredAt,
		&consumedAt,
	); err != nil {
		return nil, err
	}

	interaction.Status = protocol.ReviewLoopInteractionStatus(status)
	if iterationID.Valid && iterationID.String != "" {
		interaction.IterationID = &iterationID.String
	}
	if answer.Valid && answer.String != "" {
		interaction.Answer = &answer.String
	}
	if answeredAt.Valid && answeredAt.String != "" {
		interaction.AnsweredAt = &answeredAt.String
	}
	if consumedAt.Valid && consumedAt.String != "" {
		interaction.ConsumedAt = &consumedAt.String
	}

	return &interaction, nil
}

func marshalStringSlice(values []string) (interface{}, error) {
	if len(values) == 0 {
		return nil, nil
	}
	data, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	return string(data), nil
}

func marshalBranchDiffFiles(values []protocol.BranchDiffFile) (interface{}, error) {
	if len(values) == 0 {
		return nil, nil
	}
	data, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	return string(data), nil
}

func nullBool(v *bool) interface{} {
	if v == nil {
		return nil
	}
	return boolToInt(*v)
}

func nullReviewLoopDecision(v *protocol.ReviewLoopDecision) interface{} {
	if v == nil || *v == "" {
		return nil
	}
	return string(*v)
}
