package store

import (
	"database/sql"
	"log"
	"time"

	"github.com/victorarias/attn/internal/workspacelayout"
)

func (s *Store) SaveWorkspaceLayout(snapshot workspacelayout.WorkspaceLayout) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		if s.workspaces == nil {
			s.workspaces = make(map[string]workspacelayout.WorkspaceLayout)
		}
		s.workspaces[snapshot.WorkspaceID] = snapshot
		return nil
	}

	layoutJSON, err := workspacelayout.EncodeLayout(snapshot.Layout)
	if err != nil {
		return err
	}

	now := time.Now().Format(time.RFC3339Nano)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	createdAtByPane := make(map[string]string, len(snapshot.Panes))
	rows, err := tx.Query(`SELECT pane_id, created_at FROM workspace_layout_panes WHERE workspace_id = ?`, snapshot.WorkspaceID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var paneID string
			var createdAt string
			if scanErr := rows.Scan(&paneID, &createdAt); scanErr == nil {
				createdAtByPane[paneID] = createdAt
			}
		}
	}

	if _, err := tx.Exec(`
		INSERT INTO workspace_layouts (workspace_id, active_pane_id, layout_json, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(workspace_id) DO UPDATE SET
			active_pane_id = excluded.active_pane_id,
			layout_json = excluded.layout_json,
			updated_at = excluded.updated_at
	`, snapshot.WorkspaceID, snapshot.ActivePaneID, layoutJSON, now); err != nil {
		return err
	}

	if _, err := tx.Exec(`DELETE FROM workspace_layout_panes WHERE workspace_id = ?`, snapshot.WorkspaceID); err != nil {
		return err
	}

	for _, pane := range snapshot.Panes {
		createdAt := createdAtByPane[pane.PaneID]
		if createdAt == "" {
			createdAt = now
		}
		if _, err := tx.Exec(`
			INSERT INTO workspace_layout_panes (workspace_id, pane_id, runtime_id, session_id, kind, title, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, snapshot.WorkspaceID, pane.PaneID, pane.RuntimeID, nilIfEmpty(pane.SessionID), pane.Kind, pane.Title, createdAt, now); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) GetWorkspaceLayout(workspaceID string) *workspacelayout.WorkspaceLayout {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		snapshot, ok := s.workspaces[workspaceID]
		if !ok {
			return nil
		}
		cloned := snapshot
		if snapshot.Panes != nil {
			cloned.Panes = append([]workspacelayout.Pane(nil), snapshot.Panes...)
		}
		return &cloned
	}

	var activePaneID, layoutJSON, updatedAt string
	err := s.db.QueryRow(`
		SELECT active_pane_id, layout_json, updated_at
		FROM workspace_layouts
		WHERE workspace_id = ?
	`, workspaceID).Scan(&activePaneID, &layoutJSON, &updatedAt)
	if err != nil {
		return nil
	}

	layout, err := workspacelayout.DecodeLayout(layoutJSON)
	if err != nil {
		log.Printf("[store] GetWorkspaceLayout: failed to decode layout for workspace %s: %v", workspaceID, err)
		layout = workspacelayout.DefaultLayout()
	}

	rows, err := s.db.Query(`
		SELECT pane_id, runtime_id, session_id, kind, title
		FROM workspace_layout_panes
		WHERE workspace_id = ?
		ORDER BY created_at ASC, pane_id ASC
	`, workspaceID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	snapshot := workspacelayout.WorkspaceLayout{
		WorkspaceID:  workspaceID,
		ActivePaneID: activePaneID,
		Layout:       layout,
		UpdatedAt:    updatedAt,
	}
	for rows.Next() {
		var pane workspacelayout.Pane
		var sessionID sql.NullString
		if err := rows.Scan(&pane.PaneID, &pane.RuntimeID, &sessionID, &pane.Kind, &pane.Title); err != nil {
			log.Printf("[store] GetWorkspaceLayout: failed to scan pane for workspace %s: %v", workspaceID, err)
			continue
		}
		if sessionID.Valid {
			pane.SessionID = sessionID.String
		}
		snapshot.Panes = append(snapshot.Panes, pane)
	}

	return &snapshot
}

func (s *Store) HasWorkspaceLayout(workspaceID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		_, ok := s.workspaces[workspaceID]
		return ok
	}

	var exists int
	if err := s.db.QueryRow(`SELECT 1 FROM workspace_layouts WHERE workspace_id = ?`, workspaceID).Scan(&exists); err != nil {
		return false
	}
	return exists == 1
}

// ListWorkspaceLayoutPanes returns the persisted pane records for a workspace
// without exposing the layout tree. This is a prep seam for moving layout
// leaves from runtime-owned panes to session-owned panes.
func (s *Store) ListWorkspaceLayoutPanes(workspaceID string) []workspacelayout.Pane {
	snapshot := s.GetWorkspaceLayout(workspaceID)
	if snapshot == nil || len(snapshot.Panes) == 0 {
		return nil
	}
	return append([]workspacelayout.Pane(nil), snapshot.Panes...)
}

func (s *Store) FindWorkspaceLayoutPaneByRuntimeID(runtimeID string) (workspaceID string, paneID string, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		for workspaceID, snapshot := range s.workspaces {
			for _, pane := range snapshot.Panes {
				if pane.RuntimeID == runtimeID {
					return workspaceID, pane.PaneID, true
				}
			}
		}
		return "", "", false
	}

	var rowWorkspaceID, rowPaneID string
	err := s.db.QueryRow(`
		SELECT workspace_id, pane_id
		FROM workspace_layout_panes
		WHERE runtime_id = ?
	`, runtimeID).Scan(&rowWorkspaceID, &rowPaneID)
	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("[store] FindWorkspaceLayoutPaneByRuntimeID: query failed for runtime %s: %v", runtimeID, err)
		}
		return "", "", false
	}
	return rowWorkspaceID, rowPaneID, true
}

func (s *Store) FindWorkspaceLayoutPaneBySessionID(sessionID string) (workspaceID string, paneID string, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		for workspaceID, snapshot := range s.workspaces {
			for _, pane := range snapshot.Panes {
				if pane.SessionID == sessionID {
					return workspaceID, pane.PaneID, true
				}
			}
		}
		return "", "", false
	}

	var rowWorkspaceID, rowPaneID string
	err := s.db.QueryRow(`
		SELECT workspace_id, pane_id
		FROM workspace_layout_panes
		WHERE session_id = ?
	`, sessionID).Scan(&rowWorkspaceID, &rowPaneID)
	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("[store] FindWorkspaceLayoutPaneBySessionID: query failed for session %s: %v", sessionID, err)
		}
		return "", "", false
	}
	return rowWorkspaceID, rowPaneID, true
}

func (s *Store) RemoveWorkspaceLayout(workspaceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		delete(s.workspaces, workspaceID)
		return
	}
	_, _ = s.db.Exec(`DELETE FROM workspace_layout_panes WHERE workspace_id = ?`, workspaceID)
	_, _ = s.db.Exec(`DELETE FROM workspace_layouts WHERE workspace_id = ?`, workspaceID)
}

func nilIfEmpty(value string) interface{} {
	if value == "" {
		return nil
	}
	return value
}
