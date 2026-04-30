package store

import (
	"database/sql"
	"log"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// AddWorkspace inserts or updates a workspace row. The Status field is NOT
// persisted — it's a runtime rollup recomputed from member sessions every time
// the daemon (re)builds the in-memory registry.
func (s *Store) AddWorkspace(ws *protocol.Workspace) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}
	createdAt := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`
		INSERT INTO workspaces (id, title, directory, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			title = excluded.title,
			directory = excluded.directory`,
		ws.ID, ws.Title, ws.Directory, createdAt,
	); err != nil {
		log.Printf("[store] AddWorkspace: failed to upsert workspace %s: %v", ws.ID, err)
	}
}

// RemoveWorkspace deletes a workspace row. Member sessions are NOT cascaded
// here — the daemon is responsible for closing them with the right signal
// before calling this.
func (s *Store) RemoveWorkspace(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		delete(s.canvasPanels, id)
		return
	}
	if _, err := s.db.Exec(`DELETE FROM canvas_workspace_panels WHERE workspace_id = ?`, id); err != nil {
		log.Printf("[store] RemoveWorkspace: failed to delete workspace panels %s: %v", id, err)
	}
	if _, err := s.db.Exec(`DELETE FROM workspaces WHERE id = ?`, id); err != nil {
		log.Printf("[store] RemoveWorkspace: failed to delete workspace %s: %v", id, err)
	}
}

// GetWorkspace returns a workspace by id, or nil if not found. Status is left
// unset — callers fill it in from the runtime rollup.
func (s *Store) GetWorkspace(id string) *protocol.Workspace {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil
	}
	var ws protocol.Workspace
	err := s.db.QueryRow(`
		SELECT id, title, directory FROM workspaces WHERE id = ?`, id).
		Scan(&ws.ID, &ws.Title, &ws.Directory)
	if err != nil {
		return nil
	}
	return &ws
}

// ListWorkspaces returns every persisted workspace. Used at daemon start to
// rebuild the in-memory registry. Status is left unset — recomputed by the
// caller after associations are wired up.
func (s *Store) ListWorkspaces() []*protocol.Workspace {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil
	}
	rows, err := s.db.Query(`SELECT id, title, directory FROM workspaces ORDER BY created_at`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []*protocol.Workspace
	for rows.Next() {
		var ws protocol.Workspace
		if err := rows.Scan(&ws.ID, &ws.Title, &ws.Directory); err != nil {
			continue
		}
		out = append(out, &ws)
	}
	return out
}

// SetSessionWorkspaceID updates the workspace_id column on a session. Pass
// empty string to clear the association.
func (s *Store) SetSessionWorkspaceID(sessionID, workspaceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return
	}
	var arg sql.NullString
	if workspaceID != "" {
		arg = sql.NullString{String: workspaceID, Valid: true}
	}
	if _, err := s.db.Exec(`UPDATE sessions SET workspace_id = ? WHERE id = ?`, arg, sessionID); err != nil {
		log.Printf("[store] SetSessionWorkspaceID: failed for session %s: %v", sessionID, err)
	}
}

// SessionsInWorkspace returns the IDs of sessions currently associated with
// the given workspace. Used to seed the in-memory association map at daemon
// start, and to cascade-close sessions when a workspace is unregistered.
func (s *Store) SessionsInWorkspace(workspaceID string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil
	}
	rows, err := s.db.Query(`SELECT id FROM sessions WHERE workspace_id = ?`, workspaceID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

// SaveWorkspacePanel inserts or updates one daemon-owned canvas panel.
func (s *Store) SaveWorkspacePanel(workspaceID string, panel protocol.WorkspacePanel) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		if s.canvasPanels == nil {
			s.canvasPanels = make(map[string][]protocol.WorkspacePanel)
		}
		panels := s.canvasPanels[workspaceID]
		for i := range panels {
			if panels[i].ID == panel.ID {
				panels[i] = panel
				s.canvasPanels[workspaceID] = panels
				return
			}
		}
		s.canvasPanels[workspaceID] = append(panels, panel)
		return
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`
		INSERT INTO canvas_workspace_panels (
			workspace_id, panel_id, session_id, kind, title,
			world_x, world_y, width, height, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(workspace_id, panel_id) DO UPDATE SET
			session_id = excluded.session_id,
			kind = excluded.kind,
			title = excluded.title,
			world_x = excluded.world_x,
			world_y = excluded.world_y,
			width = excluded.width,
			height = excluded.height,
			updated_at = excluded.updated_at`,
		workspaceID, panel.ID, panel.SessionID, panel.Kind, panel.Title,
		panel.WorldX, panel.WorldY, panel.Width, panel.Height, now, now,
	); err != nil {
		log.Printf("[store] SaveWorkspacePanel: failed to upsert panel %s/%s: %v", workspaceID, panel.ID, err)
	}
}

// ListWorkspacePanels returns the daemon-owned panels for a canvas workspace.
func (s *Store) ListWorkspacePanels(workspaceID string) []protocol.WorkspacePanel {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		panels := s.canvasPanels[workspaceID]
		out := make([]protocol.WorkspacePanel, len(panels))
		copy(out, panels)
		return out
	}

	rows, err := s.db.Query(`
		SELECT panel_id, session_id, kind, title, world_x, world_y, width, height
		FROM canvas_workspace_panels
		WHERE workspace_id = ?
		ORDER BY created_at ASC, panel_id ASC`, workspaceID)
	if err != nil {
		log.Printf("[store] ListWorkspacePanels: failed for workspace %s: %v", workspaceID, err)
		return nil
	}
	defer rows.Close()

	var panels []protocol.WorkspacePanel
	for rows.Next() {
		var panel protocol.WorkspacePanel
		if err := rows.Scan(
			&panel.ID,
			&panel.SessionID,
			&panel.Kind,
			&panel.Title,
			&panel.WorldX,
			&panel.WorldY,
			&panel.Width,
			&panel.Height,
		); err != nil {
			log.Printf("[store] ListWorkspacePanels: failed to scan panel for workspace %s: %v", workspaceID, err)
			continue
		}
		panels = append(panels, panel)
	}
	return panels
}

// RemoveWorkspacePanelForSession removes any canvas panel that hosts sessionID.
func (s *Store) RemoveWorkspacePanelForSession(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		for workspaceID, panels := range s.canvasPanels {
			filtered := panels[:0]
			for _, panel := range panels {
				if panel.SessionID != sessionID {
					filtered = append(filtered, panel)
				}
			}
			if len(filtered) == 0 {
				delete(s.canvasPanels, workspaceID)
			} else {
				s.canvasPanels[workspaceID] = filtered
			}
		}
		return
	}
	if _, err := s.db.Exec(`DELETE FROM canvas_workspace_panels WHERE session_id = ?`, sessionID); err != nil {
		log.Printf("[store] RemoveWorkspacePanelForSession: failed for session %s: %v", sessionID, err)
	}
}
