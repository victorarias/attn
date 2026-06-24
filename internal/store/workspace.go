package store

import (
	"fmt"
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
	// rank is set on INSERT only. On re-register (ON CONFLICT) the stored rank is
	// the durable authority for ordering and must survive — like title, it is not
	// re-derived from the incoming struct so a user reorder sticks.
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

// RemoveWorkspace deletes a workspace row. Member sessions are NOT cascaded
// here — the daemon is responsible for closing them with the right signal
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
	_, _ = s.db.Exec(`DELETE FROM workspace_keeper_compact_backups WHERE workspace_id = ?`, id)
	_, _ = s.db.Exec(`DELETE FROM workspace_contexts WHERE workspace_id = ?`, id)
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

// ListWorkspaces returns every persisted workspace. Used at daemon start to
// rebuild the in-memory registry. Status is left unset — recomputed by the
// caller after associations are wired up.
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

// ToggleWorkspaceMute toggles a workspace's muted state. Session mute state is
// not part of the app-facing model; muting is owned by the workspace.
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

// SetWorkspaceMuted writes an explicit workspace mute state. Callers that need
// idempotent behavior (for example, making a chief delegation visible) should
// use this instead of toggling an unknown current value.
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

// SetWorkspacePinned writes an explicit workspace pinned state.
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

// UpdateWorkspaceTitle sets a workspace's title. The stored title is the durable
// authority for the name: the register path preserves a non-empty stored title
// instead of re-deriving it from a session label, so a user rename sticks.
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

// UpdateWorkspaceRank sets a workspace's rank key. Rank is the durable authority
// for sidebar order; the daemon computes the key (rankkey.Between/After) and the
// store persists exactly one row per reorder.
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

// AssignSessionWorkspace updates the workspace_id column on a session. A live
// persisted session must always have an owning workspace; callers that are
// unregistering a session should delete the session row instead of clearing
// this field.
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
	if _, err := s.db.Exec(`UPDATE sessions SET workspace_id = ? WHERE id = ?`, workspaceID, sessionID); err != nil {
		log.Printf("[store] AssignSessionWorkspace: failed for session %s: %v", sessionID, err)
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
	rows, err := s.db.Query(`SELECT id FROM sessions WHERE workspace_id = ? ORDER BY id`, workspaceID)
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
