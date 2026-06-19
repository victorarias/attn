package store

import (
	"database/sql"
)

// workflowScanner abstracts *sql.Row and *sql.Rows so a single scanner func can
// serve both QueryRow and Query loops.
type workflowScanner interface {
	Scan(dest ...any) error
}

// WorkflowRunRow is the store-local representation of a workflow_runs row. It is
// intentionally NOT a protocol type: S-store stays free of protocol/generated
// types; the S-proto step owns the wire shape.
type WorkflowRunRow struct {
	RunID       string
	ScriptPath  string
	ScriptHash  string
	ArgsJSON    *string
	SessionID   *string
	WorkspaceID *string
	Status      string
	Phase       *string
	Harness     *string
	ResultJSON  *string
	LastError   *string
	Resumable   bool
	CreatedAt   string
	UpdatedAt   string
	CompletedAt *string
}

// WorkflowAgentCallRow is the store-local representation of a workflow_agent_calls
// row. ID is informational on read (it is the durable append-order key) and is
// ignored on write (AUTOINCREMENT / composite-key-conflict driven).
type WorkflowAgentCallRow struct {
	ID              int64
	RunID           string
	Ordinal         string
	Label           *string
	Phase           *string
	PromptHash      *string
	SchemaHash      *string
	ResolvedModel   *string
	ResolvedHarness *string
	AgentType       *string
	ResultJSON      *string
	Status          string
	Error           *string
	ResultPath      *string
	StartedAt       *string
	CompletedAt     *string
}

// UpsertWorkflowRun creates or updates a workflow run, keyed on run_id.
func (s *Store) UpsertWorkflowRun(run *WorkflowRunRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil || run == nil {
		return nil
	}

	_, err := s.db.Exec(`
		INSERT INTO workflow_runs (
			run_id, script_path, script_hash, args_json, session_id, workspace_id,
			status, phase, harness, result_json, last_error, resumable,
			created_at, updated_at, completed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(run_id) DO UPDATE SET
			script_path = excluded.script_path,
			script_hash = excluded.script_hash,
			args_json = excluded.args_json,
			session_id = excluded.session_id,
			workspace_id = excluded.workspace_id,
			status = excluded.status,
			phase = excluded.phase,
			harness = excluded.harness,
			result_json = excluded.result_json,
			last_error = excluded.last_error,
			resumable = excluded.resumable,
			created_at = excluded.created_at,
			updated_at = excluded.updated_at,
			completed_at = excluded.completed_at
	`,
		run.RunID,
		run.ScriptPath,
		run.ScriptHash,
		nullPtrString(run.ArgsJSON),
		nullPtrString(run.SessionID),
		nullPtrString(run.WorkspaceID),
		run.Status,
		nullPtrString(run.Phase),
		nullPtrString(run.Harness),
		nullPtrString(run.ResultJSON),
		nullPtrString(run.LastError),
		boolToInt(run.Resumable),
		run.CreatedAt,
		run.UpdatedAt,
		nullPtrString(run.CompletedAt),
	)
	return err
}

// UpsertWorkflowAgentCall creates or updates a journaled agent call, keyed on the
// natural composite key (run_id, ordinal). The composite-key conflict is the
// divergence-overwrite path used on resume; a fresh ordinal inserts.
func (s *Store) UpsertWorkflowAgentCall(call *WorkflowAgentCallRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil || call == nil {
		return nil
	}

	_, err := s.db.Exec(`
		INSERT INTO workflow_agent_calls (
			run_id, ordinal, label, phase, prompt_hash, schema_hash,
			resolved_model, resolved_harness, agent_type, result_json, status,
			error, result_path, started_at, completed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(run_id, ordinal) DO UPDATE SET
			label = excluded.label,
			phase = excluded.phase,
			prompt_hash = excluded.prompt_hash,
			schema_hash = excluded.schema_hash,
			resolved_model = excluded.resolved_model,
			resolved_harness = excluded.resolved_harness,
			agent_type = excluded.agent_type,
			result_json = excluded.result_json,
			status = excluded.status,
			error = excluded.error,
			result_path = excluded.result_path,
			started_at = excluded.started_at,
			completed_at = excluded.completed_at
	`,
		call.RunID,
		call.Ordinal,
		nullPtrString(call.Label),
		nullPtrString(call.Phase),
		nullPtrString(call.PromptHash),
		nullPtrString(call.SchemaHash),
		nullPtrString(call.ResolvedModel),
		nullPtrString(call.ResolvedHarness),
		nullPtrString(call.AgentType),
		nullPtrString(call.ResultJSON),
		call.Status,
		nullPtrString(call.Error),
		nullPtrString(call.ResultPath),
		nullPtrString(call.StartedAt),
		nullPtrString(call.CompletedAt),
	)
	return err
}

// GetWorkflowRun returns a workflow run by run_id, or (nil, nil) if absent.
func (s *Store) GetWorkflowRun(runID string) (*WorkflowRunRow, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, nil
	}

	row := s.db.QueryRow(`
		SELECT run_id, script_path, script_hash, args_json, session_id, workspace_id,
			status, phase, harness, result_json, last_error, resumable,
			created_at, updated_at, completed_at
		FROM workflow_runs
		WHERE run_id = ?
	`, runID)

	run, err := scanWorkflowRun(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return run, nil
}

// ListWorkflowRuns returns workflow runs newest-first. An empty sessionID lists
// all runs; a non-empty sessionID filters to that session.
func (s *Store) ListWorkflowRuns(sessionID string) ([]*WorkflowRunRow, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, nil
	}

	var (
		rows *sql.Rows
		err  error
	)
	if sessionID == "" {
		rows, err = s.db.Query(`
			SELECT run_id, script_path, script_hash, args_json, session_id, workspace_id,
				status, phase, harness, result_json, last_error, resumable,
				created_at, updated_at, completed_at
			FROM workflow_runs
			ORDER BY created_at DESC, run_id DESC
		`)
	} else {
		rows, err = s.db.Query(`
			SELECT run_id, script_path, script_hash, args_json, session_id, workspace_id,
				status, phase, harness, result_json, last_error, resumable,
				created_at, updated_at, completed_at
			FROM workflow_runs
			WHERE session_id = ?
			ORDER BY created_at DESC, run_id DESC
		`, sessionID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []*WorkflowRunRow
	for rows.Next() {
		run, err := scanWorkflowRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

// ListWorkflowAgentCalls returns all journaled calls for a run in durable append
// order (ascending id), which reconstructs the journal's Entries() ordering.
func (s *Store) ListWorkflowAgentCalls(runID string) ([]*WorkflowAgentCallRow, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, nil
	}

	rows, err := s.db.Query(`
		SELECT id, run_id, ordinal, label, phase, prompt_hash, schema_hash,
			resolved_model, resolved_harness, agent_type, result_json, status,
			error, result_path, started_at, completed_at
		FROM workflow_agent_calls
		WHERE run_id = ?
		ORDER BY id ASC
	`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var calls []*WorkflowAgentCallRow
	for rows.Next() {
		call, err := scanWorkflowAgentCall(rows)
		if err != nil {
			return nil, err
		}
		calls = append(calls, call)
	}
	return calls, rows.Err()
}

// DeleteWorkflowRun removes a run and its journaled calls. The store never enables
// PRAGMA foreign_keys, so the ON DELETE CASCADE clause is inert; child rows are
// deleted explicitly, mirroring DeleteReviewLoopRun.
func (s *Store) DeleteWorkflowRun(runID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}

	if _, err := s.db.Exec(`DELETE FROM workflow_agent_calls WHERE run_id = ?`, runID); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM workflow_runs WHERE run_id = ?`, runID)
	return err
}

func scanWorkflowRun(scanner workflowScanner) (*WorkflowRunRow, error) {
	var (
		run         WorkflowRunRow
		argsJSON    sql.NullString
		sessionID   sql.NullString
		workspaceID sql.NullString
		phase       sql.NullString
		harness     sql.NullString
		resultJSON  sql.NullString
		lastError   sql.NullString
		resumable   int
		completedAt sql.NullString
	)

	if err := scanner.Scan(
		&run.RunID,
		&run.ScriptPath,
		&run.ScriptHash,
		&argsJSON,
		&sessionID,
		&workspaceID,
		&run.Status,
		&phase,
		&harness,
		&resultJSON,
		&lastError,
		&resumable,
		&run.CreatedAt,
		&run.UpdatedAt,
		&completedAt,
	); err != nil {
		return nil, err
	}

	if argsJSON.Valid && argsJSON.String != "" {
		run.ArgsJSON = &argsJSON.String
	}
	if sessionID.Valid && sessionID.String != "" {
		run.SessionID = &sessionID.String
	}
	if workspaceID.Valid && workspaceID.String != "" {
		run.WorkspaceID = &workspaceID.String
	}
	if phase.Valid && phase.String != "" {
		run.Phase = &phase.String
	}
	if harness.Valid && harness.String != "" {
		run.Harness = &harness.String
	}
	if resultJSON.Valid && resultJSON.String != "" {
		run.ResultJSON = &resultJSON.String
	}
	if lastError.Valid && lastError.String != "" {
		run.LastError = &lastError.String
	}
	run.Resumable = resumable == 1
	if completedAt.Valid && completedAt.String != "" {
		run.CompletedAt = &completedAt.String
	}

	return &run, nil
}

func scanWorkflowAgentCall(scanner workflowScanner) (*WorkflowAgentCallRow, error) {
	var (
		call            WorkflowAgentCallRow
		label           sql.NullString
		phase           sql.NullString
		promptHash      sql.NullString
		schemaHash      sql.NullString
		resolvedModel   sql.NullString
		resolvedHarness sql.NullString
		agentType       sql.NullString
		resultJSON      sql.NullString
		callError       sql.NullString
		resultPath      sql.NullString
		startedAt       sql.NullString
		completedAt     sql.NullString
	)

	if err := scanner.Scan(
		&call.ID,
		&call.RunID,
		&call.Ordinal,
		&label,
		&phase,
		&promptHash,
		&schemaHash,
		&resolvedModel,
		&resolvedHarness,
		&agentType,
		&resultJSON,
		&call.Status,
		&callError,
		&resultPath,
		&startedAt,
		&completedAt,
	); err != nil {
		return nil, err
	}

	if label.Valid && label.String != "" {
		call.Label = &label.String
	}
	if phase.Valid && phase.String != "" {
		call.Phase = &phase.String
	}
	if promptHash.Valid && promptHash.String != "" {
		call.PromptHash = &promptHash.String
	}
	if schemaHash.Valid && schemaHash.String != "" {
		call.SchemaHash = &schemaHash.String
	}
	if resolvedModel.Valid && resolvedModel.String != "" {
		call.ResolvedModel = &resolvedModel.String
	}
	if resolvedHarness.Valid && resolvedHarness.String != "" {
		call.ResolvedHarness = &resolvedHarness.String
	}
	if agentType.Valid && agentType.String != "" {
		call.AgentType = &agentType.String
	}
	if resultJSON.Valid && resultJSON.String != "" {
		call.ResultJSON = &resultJSON.String
	}
	if callError.Valid && callError.String != "" {
		call.Error = &callError.String
	}
	if resultPath.Valid && resultPath.String != "" {
		call.ResultPath = &resultPath.String
	}
	if startedAt.Valid && startedAt.String != "" {
		call.StartedAt = &startedAt.String
	}
	if completedAt.Valid && completedAt.String != "" {
		call.CompletedAt = &completedAt.String
	}

	return &call, nil
}
