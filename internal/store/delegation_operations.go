package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

var ErrDelegationRequestConflict = errors.New("delegation request id already has different inputs")

// DelegationOperationRecord is the durable launch journal. RequestJSON is kept
// alongside the public operation shape so a daemon restart can resume the exact
// accepted request and a reused key can be rejected when its inputs differ.
type DelegationOperationRecord struct {
	Operation      protocol.DelegationOperation
	RequestJSON    string
	WorktreeOwned  bool
	WorktreeToken  string
	ChiefSessionID string
}

func (s *Store) ClaimDelegationOperation(requestID, operationID, sessionID, chiefSessionID, requestJSON string, now time.Time) (*DelegationOperationRecord, bool, error) {
	if strings.HasPrefix(requestID, "op-") {
		return nil, false, fmt.Errorf("request id uses reserved operation prefix op-")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, false, errors.New("delegation idempotency requires a database")
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	result, err := s.db.Exec(`INSERT OR IGNORE INTO delegation_operations
		(request_id, operation_id, request_json, state, progress, session_id, chief_session_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, requestID, operationID, requestJSON,
		string(protocol.DelegationOperationStateAccepted), "accepted by daemon", sessionID, chiefSessionID, stamp, stamp)
	if err != nil {
		return nil, false, fmt.Errorf("claim delegation operation: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return nil, false, fmt.Errorf("claim delegation operation rows: %w", err)
	}
	record, err := getDelegationOperation(s.db, requestID)
	if err != nil {
		return nil, false, err
	}
	if record.RequestJSON != requestJSON {
		return nil, false, ErrDelegationRequestConflict
	}
	return record, rows == 1, nil
}

func (s *Store) GetDelegationOperation(id string) (*DelegationOperationRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, sql.ErrNoRows
	}
	return getDelegationOperation(s.db, id)
}

func getDelegationOperation(db *sql.DB, id string) (*DelegationOperationRecord, error) {
	var rec DelegationOperationRecord
	var state, workspaceID, ticketID, worktreePath, worktreeToken, chiefSessionID, resultJSON, errorText string
	var worktreeOwned int
	err := db.QueryRow(`SELECT request_id, operation_id, request_json, state, progress,
		session_id, workspace_id, ticket_id, worktree_path, worktree_owned, worktree_token, chief_session_id, result_json, error, created_at, updated_at
		FROM delegation_operations WHERE request_id = ? OR operation_id = ?`, id, id).Scan(
		&rec.Operation.RequestID, &rec.Operation.OperationID, &rec.RequestJSON, &state,
		&rec.Operation.Progress, &rec.Operation.SessionID, &workspaceID, &ticketID,
		&worktreePath, &worktreeOwned, &worktreeToken, &chiefSessionID, &resultJSON, &errorText, &rec.Operation.CreatedAt, &rec.Operation.UpdatedAt)
	if err != nil {
		return nil, err
	}
	rec.Operation.State = protocol.DelegationOperationState(state)
	rec.WorktreeOwned = worktreeOwned == 1
	rec.WorktreeToken = worktreeToken
	rec.ChiefSessionID = chiefSessionID
	if workspaceID != "" {
		rec.Operation.WorkspaceID = protocol.Ptr(workspaceID)
	}
	if ticketID != "" {
		rec.Operation.TicketID = protocol.Ptr(ticketID)
	}
	if worktreePath != "" {
		rec.Operation.WorktreePath = protocol.Ptr(worktreePath)
	}
	if errorText != "" {
		rec.Operation.Error = protocol.Ptr(errorText)
	}
	if resultJSON != "" {
		var result protocol.DelegateResult
		if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
			return nil, fmt.Errorf("decode delegation result: %w", err)
		}
		rec.Operation.Result = &result
	}
	return &rec, nil
}

func (s *Store) MarkDelegationWorktreeOwned(id, path, token string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stamp := now.UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`UPDATE delegation_operations SET worktree_path = ?, worktree_owned = 1, worktree_token = ?, updated_at = ?
		WHERE request_id = ? OR operation_id = ?`, path, token, stamp, id, id)
	return err
}

func (s *Store) UpdateDelegationOperation(id string, state protocol.DelegationOperationState, progress, workspaceID, ticketID, worktreePath string, result *protocol.DelegateResult, operationErr error, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("delegation idempotency requires a database")
	}
	resultJSON := ""
	if result != nil {
		encoded, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("encode delegation result: %w", err)
		}
		resultJSON = string(encoded)
	}
	errorText := ""
	if operationErr != nil {
		errorText = operationErr.Error()
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`UPDATE delegation_operations SET state = ?, progress = ?,
		workspace_id = CASE WHEN ? = '' THEN workspace_id ELSE ? END,
		ticket_id = CASE WHEN ? = '' THEN ticket_id ELSE ? END,
		worktree_path = CASE WHEN ? = '' THEN worktree_path ELSE ? END,
		result_json = ?, error = ?, updated_at = ? WHERE request_id = ? OR operation_id = ?`,
		string(state), progress, workspaceID, workspaceID, ticketID, ticketID,
		worktreePath, worktreePath, resultJSON, errorText, stamp, id, id)
	return err
}

func (s *Store) PendingDelegationOperations() ([]DelegationOperationRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT request_id FROM delegation_operations WHERE state IN (?, ?) ORDER BY created_at`,
		string(protocol.DelegationOperationStateAccepted), string(protocol.DelegationOperationStatePreparing))
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var out []DelegationOperationRecord
	for _, id := range ids {
		rec, err := getDelegationOperation(s.db, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *rec)
	}
	return out, nil
}
