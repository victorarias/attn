package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

var ErrWorkspaceContextConflict = errors.New("workspace context revision conflict")

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

func (s *Store) RemoveWorkspaceContext(workspaceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return
	}
	_, _ = s.db.Exec(`DELETE FROM workspace_contexts WHERE workspace_id = ?`, workspaceID)
}
