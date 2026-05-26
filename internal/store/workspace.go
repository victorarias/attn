package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/workspacelayout"
)

// BootstrapWorkspace persists a new workspace, its first session and root
// layout in one transaction. It deliberately rejects existing identities:
// callers must not turn an initial-create action into an accidental update.
func (s *Store) BootstrapWorkspace(ws *protocol.Workspace, session *protocol.Session, layout workspacelayout.WorkspaceLayout) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		if s.sessions == nil {
			s.sessions = make(map[string]*protocol.Session)
		}
		if _, exists := s.sessions[session.ID]; exists {
			return fmt.Errorf("session already exists: %s", session.ID)
		}
		if s.workspaces == nil {
			s.workspaces = make(map[string]workspacelayout.WorkspaceLayout)
		}
		if _, exists := s.workspaces[ws.ID]; exists {
			for _, existing := range s.sessions {
				if protocol.Deref(existing.WorkspaceID) == ws.ID {
					return fmt.Errorf("workspace already exists: %s", ws.ID)
				}
			}
		}
		s.sessions[session.ID] = cloneSession(session)
		s.workspaces[ws.ID] = layout
		return nil
	}

	todosJSON, err := json.Marshal(session.Todos)
	if err != nil {
		return err
	}
	layoutJSON, err := workspacelayout.EncodeLayout(layout.Layout)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var existingWorkspaceCount int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM workspaces WHERE id = ?`, ws.ID).Scan(&existingWorkspaceCount); err != nil {
		return err
	}
	if existingWorkspaceCount > 0 {
		var memberCount int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM sessions WHERE workspace_id = ?`, ws.ID).Scan(&memberCount); err != nil {
			return err
		}
		if memberCount > 0 {
			return fmt.Errorf("workspace already exists: %s", ws.ID)
		}
		if _, err := tx.Exec(`UPDATE workspaces SET title = ?, directory = ? WHERE id = ?`, ws.Title, ws.Directory, ws.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM workspace_layout_panes WHERE workspace_id = ?`, ws.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM workspace_layouts WHERE workspace_id = ?`, ws.ID); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(`
			INSERT INTO workspaces (id, title, directory, created_at)
			VALUES (?, ?, ?, ?)`, ws.ID, ws.Title, ws.Directory, now); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`
		INSERT INTO sessions
		(id, label, agent, directory, endpoint_id, workspace_id, branch, is_worktree, main_repo, state, state_since, state_updated_at, todos, last_seen, muted, recoverable)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		session.ID, session.Label, session.Agent, session.Directory,
		protocol.Deref(session.EndpointID), ws.ID, protocol.Deref(session.Branch),
		boolToInt(protocol.Deref(session.IsWorktree)), protocol.Deref(session.MainRepo),
		string(session.State), session.StateSince, session.StateUpdatedAt,
		string(todosJSON), session.LastSeen, boolToInt(session.Muted),
		boolToInt(protocol.Deref(session.Recoverable))); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO workspace_layouts (workspace_id, active_pane_id, layout_json, updated_at)
		VALUES (?, ?, ?, ?)`, ws.ID, layout.ActivePaneID, layoutJSON, now); err != nil {
		return err
	}
	for _, pane := range layout.Panes {
		if _, err := tx.Exec(`
			INSERT INTO workspace_layout_panes (workspace_id, pane_id, runtime_id, session_id, kind, title, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			ws.ID, pane.PaneID, pane.RuntimeID, nilIfEmpty(pane.SessionID),
			pane.Kind, pane.Title, now, now); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`
		INSERT INTO recent_locations (path, label, last_seen, use_count)
		VALUES (?, ?, ?, 1)
		ON CONFLICT(path) DO UPDATE SET
			label = excluded.label,
			last_seen = excluded.last_seen,
			use_count = use_count + 1`,
		ws.Directory, ws.Title, now); err != nil {
		return err
	}
	return tx.Commit()
}

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
		delete(s.workspaces, id)
		return
	}
	_, _ = s.db.Exec(`DELETE FROM workspace_layout_panes WHERE workspace_id = ?`, id)
	_, _ = s.db.Exec(`DELETE FROM workspace_layouts WHERE workspace_id = ?`, id)
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
