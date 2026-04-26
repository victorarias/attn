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
		return
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
