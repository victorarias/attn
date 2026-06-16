package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

var ErrWorkspaceContextConflict = errors.New("workspace context revision conflict")
var ErrKeeperCompactBackupNotFound = errors.New("keeper compact backup not found")

// KeeperCompactBackup is the source snapshot captured before the keeper's
// compaction duty rewrites a workspace context, used for direct rollback. It is
// stored in the workspace_keeper_compact_backups table (renamed off the retired
// "janitor" persona by migration 51).
type KeeperCompactBackup struct {
	WorkspaceID    string
	SourceRevision int
	SourceContent  string
	ResultRevision int
	Agent          string
	Model          string
	CreatedAt      string
}

// GetWorkspaceContext returns the canonical context. A workspace without
// context has revision zero and empty content.
func (s *Store) GetWorkspaceContext(workspaceID string) (*protocol.WorkspaceContext, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return &protocol.WorkspaceContext{WorkspaceID: workspaceID}, nil
	}
	var context protocol.WorkspaceContext
	err := s.db.QueryRow(`
		SELECT workspace_id, content, revision, updated_by_session_id, updated_at
		FROM workspace_contexts
		WHERE workspace_id = ?`,
		workspaceID,
	).Scan(
		&context.WorkspaceID,
		&context.Content,
		&context.Revision,
		&context.UpdatedBySessionID,
		&context.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return &protocol.WorkspaceContext{WorkspaceID: workspaceID}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get workspace context %s: %w", workspaceID, err)
	}
	return &context, nil
}

// ListWorkspaceContexts returns canonical contexts in workspace creation order.
func (s *Store) ListWorkspaceContexts() ([]protocol.WorkspaceContext, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT context.workspace_id, context.content, context.revision,
			context.updated_by_session_id, context.updated_at
		FROM workspace_contexts AS context
		JOIN workspaces AS workspace ON workspace.id = context.workspace_id
		ORDER BY workspace.created_at, context.workspace_id`)
	if err != nil {
		return nil, fmt.Errorf("list workspace contexts: %w", err)
	}
	defer rows.Close()

	var contexts []protocol.WorkspaceContext
	for rows.Next() {
		var context protocol.WorkspaceContext
		if err := rows.Scan(
			&context.WorkspaceID,
			&context.Content,
			&context.Revision,
			&context.UpdatedBySessionID,
			&context.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan workspace context: %w", err)
		}
		contexts = append(contexts, context)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workspace contexts: %w", err)
	}
	return contexts, nil
}

// UpdateWorkspaceContext replaces the canonical content when expectedRevision
// still matches. It returns changed=false for an identical update.
func (s *Store) UpdateWorkspaceContext(workspaceID, content, updatedBySessionID string, expectedRevision int) (*protocol.WorkspaceContext, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil, false, errors.New("workspace context persistence unavailable")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, false, fmt.Errorf("begin workspace context update: %w", err)
	}
	defer tx.Rollback()

	var current protocol.WorkspaceContext
	err = tx.QueryRow(`
		SELECT workspace_id, content, revision, updated_by_session_id, updated_at
		FROM workspace_contexts
		WHERE workspace_id = ?`,
		workspaceID,
	).Scan(
		&current.WorkspaceID,
		&current.Content,
		&current.Revision,
		&current.UpdatedBySessionID,
		&current.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		current = protocol.WorkspaceContext{WorkspaceID: workspaceID}
	} else if err != nil {
		return nil, false, fmt.Errorf("read workspace context %s: %w", workspaceID, err)
	}
	if current.Revision != expectedRevision {
		return nil, false, fmt.Errorf("%w: expected revision %d, current revision %d", ErrWorkspaceContextConflict, expectedRevision, current.Revision)
	}
	if current.Content == content {
		if err := tx.Commit(); err != nil {
			return nil, false, fmt.Errorf("commit unchanged workspace context: %w", err)
		}
		return &current, false, nil
	}

	updated := &protocol.WorkspaceContext{
		WorkspaceID:        workspaceID,
		Content:            content,
		Revision:           current.Revision + 1,
		UpdatedBySessionID: updatedBySessionID,
		UpdatedAt:          time.Now().UTC().Format(time.RFC3339Nano),
	}
	if _, err := tx.Exec(`
		INSERT INTO workspace_contexts
			(workspace_id, content, revision, updated_by_session_id, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(workspace_id) DO UPDATE SET
			content = excluded.content,
			revision = excluded.revision,
			updated_by_session_id = excluded.updated_by_session_id,
			updated_at = excluded.updated_at`,
		updated.WorkspaceID,
		updated.Content,
		updated.Revision,
		updated.UpdatedBySessionID,
		updated.UpdatedAt,
	); err != nil {
		return nil, false, fmt.Errorf("write workspace context %s: %w", workspaceID, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("commit workspace context update: %w", err)
	}
	return updated, true, nil
}

func (s *Store) HasWorkspaceContext(workspaceID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return false
	}
	var exists int
	return s.db.QueryRow(`SELECT 1 FROM workspace_contexts WHERE workspace_id = ?`, workspaceID).Scan(&exists) == nil
}

// ApplyKeeperCompactResult atomically stores the compacted context
// and the source snapshot needed for direct rollback.
func (s *Store) ApplyKeeperCompactResult(
	workspaceID string,
	content string,
	updatedBySessionID string,
	expectedRevision int,
	agent string,
	model string,
) (*protocol.WorkspaceContext, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil, false, errors.New("workspace context persistence unavailable")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, false, fmt.Errorf("begin keeper compact update: %w", err)
	}
	defer tx.Rollback()

	var workspaceExists int
	if err := tx.QueryRow(`SELECT 1 FROM workspaces WHERE id = ?`, workspaceID).Scan(&workspaceExists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, fmt.Errorf("workspace not found: %s", workspaceID)
		}
		return nil, false, fmt.Errorf("check workspace %s: %w", workspaceID, err)
	}

	current, err := readWorkspaceContextTx(tx, workspaceID)
	if err != nil {
		return nil, false, err
	}
	if current.Revision != expectedRevision {
		return nil, false, fmt.Errorf("%w: expected revision %d, current revision %d", ErrWorkspaceContextConflict, expectedRevision, current.Revision)
	}
	if current.Content == content {
		if err := tx.Commit(); err != nil {
			return nil, false, fmt.Errorf("commit unchanged keeper compact update: %w", err)
		}
		return current, false, nil
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	updated := &protocol.WorkspaceContext{
		WorkspaceID:        workspaceID,
		Content:            content,
		Revision:           current.Revision + 1,
		UpdatedBySessionID: updatedBySessionID,
		UpdatedAt:          now,
	}
	if err := writeWorkspaceContextTx(tx, updated); err != nil {
		return nil, false, err
	}
	if _, err := tx.Exec(`
		INSERT INTO workspace_keeper_compact_backups
			(workspace_id, source_revision, source_content, result_revision, agent, model, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(workspace_id) DO UPDATE SET
			source_revision = excluded.source_revision,
			source_content = excluded.source_content,
			result_revision = excluded.result_revision,
			agent = excluded.agent,
			model = excluded.model,
			created_at = excluded.created_at`,
		workspaceID,
		current.Revision,
		current.Content,
		updated.Revision,
		agent,
		model,
		now,
	); err != nil {
		return nil, false, fmt.Errorf("store keeper compact backup %s: %w", workspaceID, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("commit keeper compact update: %w", err)
	}
	return updated, true, nil
}

func (s *Store) GetKeeperCompactBackup(workspaceID string) (*KeeperCompactBackup, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, ErrKeeperCompactBackupNotFound
	}
	var backup KeeperCompactBackup
	err := s.db.QueryRow(`
		SELECT workspace_id, source_revision, source_content, result_revision,
			agent, model, created_at
		FROM workspace_keeper_compact_backups
		WHERE workspace_id = ?`,
		workspaceID,
	).Scan(
		&backup.WorkspaceID,
		&backup.SourceRevision,
		&backup.SourceContent,
		&backup.ResultRevision,
		&backup.Agent,
		&backup.Model,
		&backup.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrKeeperCompactBackupNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get keeper compact backup %s: %w", workspaceID, err)
	}
	return &backup, nil
}

// RestoreKeeperCompactBackup restores only when the compacted
// revision is still canonical, so later user edits are never overwritten.
func (s *Store) RestoreKeeperCompactBackup(
	workspaceID string,
	updatedBySessionID string,
) (*protocol.WorkspaceContext, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, errors.New("workspace context persistence unavailable")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin keeper compact rollback: %w", err)
	}
	defer tx.Rollback()

	var backup KeeperCompactBackup
	err = tx.QueryRow(`
		SELECT workspace_id, source_revision, source_content, result_revision,
			agent, model, created_at
		FROM workspace_keeper_compact_backups
		WHERE workspace_id = ?`,
		workspaceID,
	).Scan(
		&backup.WorkspaceID,
		&backup.SourceRevision,
		&backup.SourceContent,
		&backup.ResultRevision,
		&backup.Agent,
		&backup.Model,
		&backup.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrKeeperCompactBackupNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read keeper compact backup %s: %w", workspaceID, err)
	}
	current, err := readWorkspaceContextTx(tx, workspaceID)
	if err != nil {
		return nil, err
	}
	if current.Revision != backup.ResultRevision {
		return nil, fmt.Errorf("%w: backup result revision %d, current revision %d", ErrWorkspaceContextConflict, backup.ResultRevision, current.Revision)
	}
	updated := &protocol.WorkspaceContext{
		WorkspaceID:        workspaceID,
		Content:            backup.SourceContent,
		Revision:           current.Revision + 1,
		UpdatedBySessionID: updatedBySessionID,
		UpdatedAt:          time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := writeWorkspaceContextTx(tx, updated); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit keeper compact rollback: %w", err)
	}
	return updated, nil
}

func readWorkspaceContextTx(tx *sql.Tx, workspaceID string) (*protocol.WorkspaceContext, error) {
	var current protocol.WorkspaceContext
	err := tx.QueryRow(`
		SELECT workspace_id, content, revision, updated_by_session_id, updated_at
		FROM workspace_contexts
		WHERE workspace_id = ?`,
		workspaceID,
	).Scan(
		&current.WorkspaceID,
		&current.Content,
		&current.Revision,
		&current.UpdatedBySessionID,
		&current.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return &protocol.WorkspaceContext{WorkspaceID: workspaceID}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read workspace context %s: %w", workspaceID, err)
	}
	return &current, nil
}

func writeWorkspaceContextTx(tx *sql.Tx, updated *protocol.WorkspaceContext) error {
	if _, err := tx.Exec(`
		INSERT INTO workspace_contexts
			(workspace_id, content, revision, updated_by_session_id, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(workspace_id) DO UPDATE SET
			content = excluded.content,
			revision = excluded.revision,
			updated_by_session_id = excluded.updated_by_session_id,
			updated_at = excluded.updated_at`,
		updated.WorkspaceID,
		updated.Content,
		updated.Revision,
		updated.UpdatedBySessionID,
		updated.UpdatedAt,
	); err != nil {
		return fmt.Errorf("write workspace context %s: %w", updated.WorkspaceID, err)
	}
	return nil
}

func (s *Store) RemoveWorkspaceContext(workspaceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return
	}
	_, _ = s.db.Exec(`DELETE FROM workspace_keeper_compact_backups WHERE workspace_id = ?`, workspaceID)
	_, _ = s.db.Exec(`DELETE FROM workspace_contexts WHERE workspace_id = ?`, workspaceID)
}
