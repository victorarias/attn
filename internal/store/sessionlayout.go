package store

import (
	"database/sql"
	"log"
	"time"

	"github.com/victorarias/attn/internal/sessionlayout"
)

func (s *Store) SaveSessionLayout(snapshot sessionlayout.SessionLayout) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		if s.workspaces == nil {
			s.workspaces = make(map[string]sessionlayout.SessionLayout)
		}
		s.workspaces[snapshot.SessionID] = snapshot
		return nil
	}

	layoutJSON, err := sessionlayout.EncodeLayout(snapshot.Layout)
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
	rows, err := tx.Query(`SELECT pane_id, created_at FROM workspace_panes WHERE session_id = ?`, snapshot.SessionID)
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
		INSERT INTO session_workspaces (session_id, active_pane_id, layout_json, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			active_pane_id = excluded.active_pane_id,
			layout_json = excluded.layout_json,
			updated_at = excluded.updated_at
	`, snapshot.SessionID, snapshot.ActivePaneID, layoutJSON, now); err != nil {
		return err
	}

	if _, err := tx.Exec(`DELETE FROM workspace_panes WHERE session_id = ?`, snapshot.SessionID); err != nil {
		return err
	}

	for _, pane := range snapshot.Panes {
		createdAt := createdAtByPane[pane.PaneID]
		if createdAt == "" {
			createdAt = now
		}
		if _, err := tx.Exec(`
			INSERT INTO workspace_panes (session_id, pane_id, runtime_id, kind, title, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, snapshot.SessionID, pane.PaneID, pane.RuntimeID, pane.Kind, pane.Title, createdAt, now); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) GetSessionLayout(sessionID string) *sessionlayout.SessionLayout {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		snapshot, ok := s.workspaces[sessionID]
		if !ok {
			return nil
		}
		cloned := snapshot
		if snapshot.Panes != nil {
			cloned.Panes = append([]sessionlayout.Pane(nil), snapshot.Panes...)
		}
		return &cloned
	}

	var activePaneID, layoutJSON, updatedAt string
	err := s.db.QueryRow(`
		SELECT active_pane_id, layout_json, updated_at
		FROM session_workspaces
		WHERE session_id = ?
	`, sessionID).Scan(&activePaneID, &layoutJSON, &updatedAt)
	if err != nil {
		return nil
	}

	layout, err := sessionlayout.DecodeLayout(layoutJSON)
	if err != nil {
		log.Printf("[store] GetSessionLayout: failed to decode layout for session %s: %v", sessionID, err)
		layout = sessionlayout.DefaultLayout()
	}

	rows, err := s.db.Query(`
		SELECT pane_id, runtime_id, kind, title
		FROM workspace_panes
		WHERE session_id = ?
		ORDER BY created_at ASC, pane_id ASC
	`, sessionID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	snapshot := sessionlayout.SessionLayout{
		SessionID:    sessionID,
		ActivePaneID: activePaneID,
		Layout:       layout,
		UpdatedAt:    updatedAt,
	}
	for rows.Next() {
		var pane sessionlayout.Pane
		if err := rows.Scan(&pane.PaneID, &pane.RuntimeID, &pane.Kind, &pane.Title); err != nil {
			log.Printf("[store] GetSessionLayout: failed to scan pane for session %s: %v", sessionID, err)
			continue
		}
		snapshot.Panes = append(snapshot.Panes, pane)
	}

	return &snapshot
}

func (s *Store) HasSessionLayout(sessionID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		_, ok := s.workspaces[sessionID]
		return ok
	}

	var exists int
	if err := s.db.QueryRow(`SELECT 1 FROM session_workspaces WHERE session_id = ?`, sessionID).Scan(&exists); err != nil {
		return false
	}
	return exists == 1
}

func (s *Store) FindSessionLayoutPaneByRuntimeID(runtimeID string) (sessionID string, paneID string, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		for sessionID, snapshot := range s.workspaces {
			for _, pane := range snapshot.Panes {
				if pane.RuntimeID == runtimeID {
					return sessionID, pane.PaneID, true
				}
			}
		}
		return "", "", false
	}

	var rowSessionID, rowPaneID string
	err := s.db.QueryRow(`
		SELECT session_id, pane_id
		FROM workspace_panes
		WHERE runtime_id = ?
	`, runtimeID).Scan(&rowSessionID, &rowPaneID)
	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("[store] FindSessionLayoutPaneByRuntimeID: query failed for runtime %s: %v", runtimeID, err)
		}
		return "", "", false
	}
	return rowSessionID, rowPaneID, true
}
