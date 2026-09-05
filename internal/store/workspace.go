package store

import (
	"fmt"
	"log"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func (s *Store) AddWorkspace(ws *protocol.Workspace) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}
	createdAt := time.Now().UTC().Format(sortableTimeFormat)
	// rank is written on INSERT only: on re-register the stored rank is the durable
	// ordering authority and must survive, like title.
	if _, err := s.db.Exec(`
		INSERT INTO workspaces (id, title, directory, muted, pinned, created_at, rank)
		VALUES (?, ?, ?, COALESCE(?, 0), COALESCE(?, 0), ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			title = excluded.title,
			directory = excluded.directory`,
		ws.ID, ws.Title, ws.Directory, boolToInt(ws.Muted), boolToInt(ws.Pinned), createdAt, ws.Rank,
	); err != nil {
		log.Printf("[store] AddWorkspace: failed to upsert workspace %s: %v", ws.ID, err)
	}
}

// Member sessions are NOT cascaded: the daemon closes them with the right signal
// before calling this.
func (s *Store) RemoveWorkspace(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		delete(s.workspaces, id)
		return
	}
	_, _ = s.db.Exec(`DELETE FROM workspace_layout_panes WHERE workspace_id = ?`, id)
	_, _ = s.db.Exec(`DELETE FROM workspace_layouts WHERE workspace_id = ?`, id)
	if _, err := s.db.Exec(`DELETE FROM workspaces WHERE id = ?`, id); err != nil {
		log.Printf("[store] RemoveWorkspace: failed to delete workspace %s: %v", id, err)
	}
}

func (s *Store) GetWorkspace(id string) *protocol.Workspace {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil
	}
	var ws protocol.Workspace
	var muted, pinned int
	err := s.db.QueryRow(`
		SELECT id, title, directory, muted, pinned, rank FROM workspaces WHERE id = ?`, id).
		Scan(&ws.ID, &ws.Title, &ws.Directory, &muted, &pinned, &ws.Rank)
	if err != nil {
		return nil
	}
	ws.Muted = muted == 1
	ws.Pinned = pinned == 1
	return &ws
}

func (s *Store) ListWorkspaces() []*protocol.Workspace {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil
	}
	rows, err := s.db.Query(`SELECT id, title, directory, muted, pinned, rank FROM workspaces ORDER BY rank, created_at`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []*protocol.Workspace
	for rows.Next() {
		var ws protocol.Workspace
		var muted, pinned int
		if err := rows.Scan(&ws.ID, &ws.Title, &ws.Directory, &muted, &pinned, &ws.Rank); err != nil {
			continue
		}
		ws.Muted = muted == 1
		ws.Pinned = pinned == 1
		out = append(out, &ws)
	}
	return out
}

func (s *Store) ToggleWorkspaceMute(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	if _, err := s.db.Exec(`UPDATE workspaces SET muted = NOT muted WHERE id = ?`, id); err != nil {
		log.Printf("[store] ToggleWorkspaceMute: failed for workspace %s: %v", id, err)
	}
}

func (s *Store) SetWorkspaceMuted(id string, muted bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	result, err := s.db.Exec(`UPDATE workspaces SET muted = ? WHERE id = ?`, boolToInt(muted), id)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("workspace not found: %s", id)
	}
	return nil
}

func (s *Store) SetWorkspacePinned(id string, pinned bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	result, err := s.db.Exec(`UPDATE workspaces SET pinned = ? WHERE id = ?`, boolToInt(pinned), id)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("workspace not found: %s", id)
	}
	return nil
}

func (s *Store) UpdateWorkspaceTitle(id, title string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	if _, err := s.db.Exec(`UPDATE workspaces SET title = ? WHERE id = ?`, title, id); err != nil {
		log.Printf("[store] UpdateWorkspaceTitle: failed for workspace %s: %v", id, err)
	}
}

func (s *Store) UpdateWorkspaceRank(id, rank string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	if _, err := s.db.Exec(`UPDATE workspaces SET rank = ? WHERE id = ?`, rank, id); err != nil {
		log.Printf("[store] UpdateWorkspaceRank: failed for workspace %s: %v", id, err)
	}
}

func (s *Store) AssignSessionWorkspace(sessionID, workspaceID string) {
	if workspaceID == "" {
		log.Printf("[store] AssignSessionWorkspace: refusing empty workspace for session %s", sessionID)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return
	}
	if _, err := s.db.Exec(`UPDATE sessions SET workspace_id = ? WHERE id = ? AND closed_at = ''`, workspaceID, sessionID); err != nil {
		log.Printf("[store] AssignSessionWorkspace: failed for session %s: %v", sessionID, err)
	}
}

func (s *Store) SessionsInWorkspace(workspaceID string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil
	}
	rows, err := s.db.Query(`SELECT id FROM sessions WHERE workspace_id = ? AND closed_at = '' ORDER BY id`, workspaceID)
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
