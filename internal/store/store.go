package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/workspacelayout"
)

// Store manages session state in SQLite
type Store struct {
	mu sync.RWMutex
	db *sql.DB

	sessions        map[string]*protocol.Session
	agentDriverRuns map[string]AgentDriverReportCursor
	agentMetadata   map[string]string
	profileRoles    map[string]string
	workspaces      map[string]workspacelayout.WorkspaceLayout
	recentLocations map[string]*protocol.RecentLocation
}

type AgentDriverReportCursor struct {
	PluginName string
	RunID      string
	Seq        uint64
}

// New creates a new in-memory store (backed by SQLite :memory:)
func New() *Store {
	db, err := OpenDB(":memory:")
	if err != nil {
		return &Store{
			sessions:        make(map[string]*protocol.Session),
			agentDriverRuns: make(map[string]AgentDriverReportCursor),
			agentMetadata:   make(map[string]string),
			profileRoles:    make(map[string]string),
			workspaces:      make(map[string]workspacelayout.WorkspaceLayout),
			recentLocations: make(map[string]*protocol.RecentLocation),
		}
	}
	return &Store{db: db}
}

func cloneSession(session *protocol.Session) *protocol.Session {
	if session == nil {
		return nil
	}
	cloned := *session
	if session.EndpointID != nil {
		cloned.EndpointID = protocol.Ptr(protocol.Deref(session.EndpointID))
	}
	if session.Branch != nil {
		cloned.Branch = protocol.Ptr(protocol.Deref(session.Branch))
	}
	if session.IsWorktree != nil {
		value := protocol.Deref(session.IsWorktree)
		cloned.IsWorktree = protocol.Ptr(value)
	}
	if session.MainRepo != nil {
		cloned.MainRepo = protocol.Ptr(protocol.Deref(session.MainRepo))
	}
	if session.Recoverable != nil {
		value := protocol.Deref(session.Recoverable)
		cloned.Recoverable = protocol.Ptr(value)
	}
	if session.Todos != nil {
		cloned.Todos = append([]string(nil), session.Todos...)
	}
	return &cloned
}

// NewWithDB creates a store backed by SQLite
func NewWithDB(dbPath string) (*Store, error) {
	db, err := OpenDB(dbPath)
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

// NewWithPersistence creates a store that persists to SQLite (replaces JSON persistence)
func NewWithPersistence(path string) *Store {
	// Use the new DBPath from config instead of the legacy state path
	dbPath := config.DBPath()
	store, err := NewWithDB(dbPath)
	if err != nil {
		// Fallback to in-memory if DB fails
		return New()
	}
	return store
}

// DefaultStatePath returns the default state file path (legacy, for cleanup)
func DefaultStatePath() string {
	return config.StatePath()
}

// execLog executes a query and logs any error (for operations where we don't propagate errors yet)
func (s *Store) execLog(query string, args ...interface{}) {
	if _, err := s.db.Exec(query, args...); err != nil {
		log.Printf("[store] exec error: %v (query: %.50s...)", err, query)
	}
}

// Close closes the database connection
func (s *Store) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// Add adds a session to the store and logs persistence failures.
func (s *Store) Add(session *protocol.Session) {
	if err := s.AddChecked(session); err != nil {
		log.Printf("[store] Add: failed to insert session %s: %v", session.ID, err)
	}
}

// AddChecked adds a session to the store and returns persistence failures.
func (s *Store) AddChecked(session *protocol.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		if s.sessions == nil {
			s.sessions = make(map[string]*protocol.Session)
		}
		s.sessions[session.ID] = cloneSession(session)
		return nil
	}

	todosJSON, err := json.Marshal(session.Todos)
	if err != nil {
		return fmt.Errorf("marshal todos for session %s: %w", session.ID, err)
	}
	normalizedAgent := strings.TrimSpace(strings.ToLower(string(session.Agent)))
	if normalizedAgent == "" {
		normalizedAgent = string(protocol.SessionAgentCodex)
	}
	session.Agent = protocol.SessionAgent(normalizedAgent)
	_, err = s.db.Exec(`
		INSERT INTO sessions
		(id, label, agent, directory, endpoint_id, workspace_id, branch, is_worktree, main_repo, state, state_since, state_updated_at, todos, last_seen, recoverable)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			label = excluded.label,
			agent = excluded.agent,
			directory = excluded.directory,
			endpoint_id = excluded.endpoint_id,
			workspace_id = excluded.workspace_id,
			branch = excluded.branch,
			is_worktree = excluded.is_worktree,
			main_repo = excluded.main_repo,
			state = excluded.state,
			state_since = excluded.state_since,
			state_updated_at = excluded.state_updated_at,
			todos = excluded.todos,
			last_seen = excluded.last_seen,
			recoverable = excluded.recoverable`,
		session.ID,
		session.Label,
		session.Agent,
		session.Directory,
		protocol.Deref(session.EndpointID),
		session.WorkspaceID,
		protocol.Deref(session.Branch),
		boolToInt(protocol.Deref(session.IsWorktree)),
		protocol.Deref(session.MainRepo),
		string(session.State),
		session.StateSince,
		session.StateUpdatedAt,
		string(todosJSON),
		session.LastSeen,
		boolToInt(protocol.Deref(session.Recoverable)),
	)
	if err != nil {
		return fmt.Errorf("insert session %s: %w", session.ID, err)
	}
	return nil
}

// Get retrieves a session by ID
func (s *Store) Get(id string) *protocol.Session {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return cloneSession(s.sessions[id])
	}

	var session protocol.Session
	var todosJSON string
	var stateSince, stateUpdatedAt, lastSeen string
	var isWorktree, recoverable int
	var endpointID, workspaceID, branch, mainRepo sql.NullString

	err := s.db.QueryRow(`
		SELECT id, label, agent, directory, endpoint_id, workspace_id, branch, is_worktree, main_repo, state, state_since, state_updated_at, todos, last_seen, recoverable
		FROM sessions WHERE id = ?`, id).Scan(
		&session.ID,
		&session.Label,
		&session.Agent,
		&session.Directory,
		&endpointID,
		&workspaceID,
		&branch,
		&isWorktree,
		&mainRepo,
		&session.State,
		&stateSince,
		&stateUpdatedAt,
		&todosJSON,
		&lastSeen,
		&recoverable,
	)
	if err != nil {
		return nil
	}

	if endpointID.Valid && endpointID.String != "" {
		session.EndpointID = protocol.Ptr(endpointID.String)
	}
	if workspaceID.Valid && workspaceID.String != "" {
		session.WorkspaceID = workspaceID.String
	}
	if branch.Valid && branch.String != "" {
		session.Branch = protocol.Ptr(branch.String)
	}
	if isWorktree == 1 {
		session.IsWorktree = protocol.Ptr(true)
	}
	if mainRepo.Valid && mainRepo.String != "" {
		session.MainRepo = protocol.Ptr(mainRepo.String)
	}
	session.StateSince = stateSince
	session.StateUpdatedAt = stateUpdatedAt
	session.LastSeen = lastSeen
	if recoverable == 1 {
		session.Recoverable = protocol.Ptr(true)
	}
	if todosJSON != "" && todosJSON != "null" {
		if err := json.Unmarshal([]byte(todosJSON), &session.Todos); err != nil {
			log.Printf("[store] Get: failed to unmarshal todos for session %s: %v", id, err)
		}
	}

	return &session
}

// Remove removes a session from the store
func (s *Store) Remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		delete(s.sessions, id)
		delete(s.agentDriverRuns, id)
		delete(s.agentMetadata, id)
		return
	}

	_, err := s.db.Exec("DELETE FROM sessions WHERE id = ?", id)
	if err != nil {
		log.Printf("[store] Remove: failed for session %s: %v", id, err)
	}
}

// ClearSessions removes all sessions from the store
func (s *Store) ClearSessions() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		s.sessions = make(map[string]*protocol.Session)
		s.agentDriverRuns = make(map[string]AgentDriverReportCursor)
		s.agentMetadata = make(map[string]string)
		s.workspaces = make(map[string]workspacelayout.WorkspaceLayout)
		return
	}

	if _, err := s.db.Exec("DELETE FROM workspace_layout_panes"); err != nil {
		log.Printf("[store] ClearSessions: failed to clear workspace layout panes: %v", err)
	}
	if _, err := s.db.Exec("DELETE FROM workspace_layouts"); err != nil {
		log.Printf("[store] ClearSessions: failed to clear workspace layouts: %v", err)
	}
	_, err := s.db.Exec("DELETE FROM sessions")
	if err != nil {
		log.Printf("[store] ClearSessions: failed: %v", err)
	}
}

// List returns sessions, optionally filtered by state, sorted by label then ID.
// The ID tie-breaker keeps ordering stable when labels are duplicated.
func (s *Store) List(stateFilter string) []*protocol.Session {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		result := make([]*protocol.Session, 0, len(s.sessions))
		for _, session := range s.sessions {
			if stateFilter != "" && string(session.State) != stateFilter {
				continue
			}
			result = append(result, cloneSession(session))
		}
		sort.Slice(result, func(i, j int) bool {
			if result[i].Label == result[j].Label {
				return result[i].ID < result[j].ID
			}
			return result[i].Label < result[j].Label
		})
		return result
	}

	var rows *sql.Rows
	var err error

	if stateFilter == "" {
		rows, err = s.db.Query(`
			SELECT id, label, agent, directory, endpoint_id, workspace_id, branch, is_worktree, main_repo, state, state_since, state_updated_at, todos, last_seen, recoverable
			FROM sessions ORDER BY label, id`)
	} else {
		rows, err = s.db.Query(`
			SELECT id, label, agent, directory, endpoint_id, workspace_id, branch, is_worktree, main_repo, state, state_since, state_updated_at, todos, last_seen, recoverable
			FROM sessions WHERE state = ? ORDER BY label, id`, stateFilter)
	}
	if err != nil {
		return nil
	}
	defer rows.Close()

	var result []*protocol.Session
	for rows.Next() {
		var session protocol.Session
		var todosJSON string
		var stateSince, stateUpdatedAt, lastSeen string
		var isWorktree, recoverable int
		var endpointID, workspaceID, branch, mainRepo sql.NullString

		err := rows.Scan(
			&session.ID,
			&session.Label,
			&session.Agent,
			&session.Directory,
			&endpointID,
			&workspaceID,
			&branch,
			&isWorktree,
			&mainRepo,
			&session.State,
			&stateSince,
			&stateUpdatedAt,
			&todosJSON,
			&lastSeen,
			&recoverable,
		)
		if err != nil {
			continue
		}

		if endpointID.Valid && endpointID.String != "" {
			session.EndpointID = protocol.Ptr(endpointID.String)
		}
		if workspaceID.Valid && workspaceID.String != "" {
			session.WorkspaceID = workspaceID.String
		}
		if branch.Valid && branch.String != "" {
			session.Branch = protocol.Ptr(branch.String)
		}
		if isWorktree == 1 {
			session.IsWorktree = protocol.Ptr(true)
		}
		if mainRepo.Valid && mainRepo.String != "" {
			session.MainRepo = protocol.Ptr(mainRepo.String)
		}
		session.StateSince = stateSince
		session.StateUpdatedAt = stateUpdatedAt
		session.LastSeen = lastSeen
		if recoverable == 1 {
			session.Recoverable = protocol.Ptr(true)
		}
		if todosJSON != "" && todosJSON != "null" {
			if err := json.Unmarshal([]byte(todosJSON), &session.Todos); err != nil {
				log.Printf("[store] List: failed to unmarshal todos for session %s: %v", session.ID, err)
			}
		}

		result = append(result, &session)
	}

	return result
}

// HasSessionInDirectory checks if there's an active session using the given directory
func (s *Store) HasSessionInDirectory(directory string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		for _, session := range s.sessions {
			if session.Directory == directory {
				return true
			}
		}
		return false
	}

	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE directory = ?`, directory).Scan(&count)
	if err != nil {
		return false
	}
	return count > 0
}

// RemoveSessionsInDirectory removes all sessions in the given directory
func (s *Store) RemoveSessionsInDirectory(directory string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		for id, session := range s.sessions {
			if session.Directory == directory {
				delete(s.sessions, id)
			}
		}
		return
	}

	_, err := s.db.Exec(`DELETE FROM sessions WHERE directory = ?`, directory)
	if err != nil {
		log.Printf("[store] RemoveSessionsInDirectory: failed for directory %s: %v", directory, err)
	}
}

// UpdateState updates a session's state and reports whether a session was updated.
func (s *Store) UpdateState(id, state string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		session := s.sessions[id]
		if session == nil {
			return false
		}
		now := time.Now().Format(time.RFC3339Nano)
		session.State = protocol.SessionState(state)
		session.StateSince = now
		session.StateUpdatedAt = now
		return true
	}

	now := time.Now().Format(time.RFC3339Nano)
	result, err := s.db.Exec(`UPDATE sessions SET state = ?, state_since = ?, state_updated_at = ? WHERE id = ?`,
		state, now, now, id)
	if err != nil {
		log.Printf("[store] UpdateState: failed for session %s: %v", id, err)
		return false
	}
	updated, err := result.RowsAffected()
	return err == nil && updated == 1
}

// UpdateStateWithTimestamp updates a session's state only if the provided timestamp
// is newer than the current StateUpdatedAt. Returns true if updated, false if rejected.
func (s *Store) UpdateStateWithTimestamp(id, state string, updatedAt time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		session := s.sessions[id]
		if session == nil {
			return false
		}
		current, err := time.Parse(time.RFC3339Nano, session.StateUpdatedAt)
		if err != nil {
			current, _ = time.Parse(time.RFC3339, session.StateUpdatedAt)
		}
		if !updatedAt.After(current) {
			return false
		}
		ts := updatedAt.Format(time.RFC3339Nano)
		session.State = protocol.SessionState(state)
		session.StateSince = ts
		session.StateUpdatedAt = ts
		return true
	}

	// Get current state_updated_at
	var currentUpdatedAt string
	err := s.db.QueryRow("SELECT state_updated_at FROM sessions WHERE id = ?", id).Scan(&currentUpdatedAt)
	if err != nil {
		return false
	}

	current, err := time.Parse(time.RFC3339Nano, currentUpdatedAt)
	if err != nil {
		// Fall back to RFC3339 for timestamps written before the Nano switch.
		current, err = time.Parse(time.RFC3339, currentUpdatedAt)
		if err != nil {
			log.Printf("[store] UpdateStateWithTimestamp: failed to parse timestamp for session %s: %v", id, err)
			current = time.Time{}
		}
	}
	if !updatedAt.After(current) {
		return false
	}

	ts := updatedAt.Format(time.RFC3339Nano)
	_, err = s.db.Exec(`UPDATE sessions SET state = ?, state_since = ?, state_updated_at = ? WHERE id = ?`,
		state, ts, ts, id)
	return err == nil
}

// UpdateTodos updates a session's todo list
func (s *Store) UpdateTodos(id string, todos []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		if session := s.sessions[id]; session != nil {
			session.Todos = append([]string(nil), todos...)
		}
		return
	}

	todosJSON, err := json.Marshal(todos)
	if err != nil {
		log.Printf("[store] UpdateTodos: failed to marshal todos for session %s: %v", id, err)
		return
	}
	_, err = s.db.Exec("UPDATE sessions SET todos = ? WHERE id = ?", string(todosJSON), id)
	if err != nil {
		log.Printf("[store] UpdateTodos: failed for session %s: %v", id, err)
	}
}

// UpdateBranch updates a session's branch information
func (s *Store) UpdateBranch(id, branch string, isWorktree bool, mainRepo string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		if session := s.sessions[id]; session != nil {
			if branch != "" {
				session.Branch = protocol.Ptr(branch)
			} else {
				session.Branch = nil
			}
			session.IsWorktree = protocol.Ptr(isWorktree)
			if mainRepo != "" {
				session.MainRepo = protocol.Ptr(mainRepo)
			} else {
				session.MainRepo = nil
			}
		}
		return
	}

	_, err := s.db.Exec(`UPDATE sessions SET branch = ?, is_worktree = ?, main_repo = ? WHERE id = ?`,
		branch, boolToInt(isWorktree), mainRepo, id)
	if err != nil {
		log.Printf("[store] UpdateBranch: failed for session %s: %v", id, err)
	}
}

// UpdateSessionLabel sets a session's display label. This is the durable
// authority for the name: registration and respawn paths preserve a non-empty
// stored label rather than overwrite it, so a user rename sticks.
func (s *Store) UpdateSessionLabel(id, label string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		if session := s.sessions[id]; session != nil {
			session.Label = label
		}
		return
	}

	if _, err := s.db.Exec(`UPDATE sessions SET label = ? WHERE id = ?`, label, id); err != nil {
		log.Printf("[store] UpdateSessionLabel: failed for session %s: %v", id, err)
	}
}

// Touch updates a session's last seen time
func (s *Store) Touch(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		if session := s.sessions[id]; session != nil {
			session.LastSeen = time.Now().Format(time.RFC3339Nano)
		}
		return
	}

	now := time.Now().Format(time.RFC3339Nano)
	_, err := s.db.Exec("UPDATE sessions SET last_seen = ? WHERE id = ?", now, id)
	if err != nil {
		log.Printf("[store] Touch: failed for session %s: %v", id, err)
	}
}

// SetRecoverable marks a session as recoverable (can be resumed after daemon restart)
func (s *Store) SetRecoverable(id string, recoverable bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		if session := s.sessions[id]; session != nil {
			session.Recoverable = protocol.Ptr(recoverable)
		}
		return
	}

	_, err := s.db.Exec("UPDATE sessions SET recoverable = ? WHERE id = ?", boolToInt(recoverable), id)
	if err != nil {
		log.Printf("[store] SetRecoverable: failed for session %s: %v", id, err)
	}
}

// SetResumeSessionID stores the agent-native resume session id for an attn session.
// This allows recovery to use the real agent conversation id when it differs
// from the attn session id (for example, when using an agent resume picker).
func (s *Store) SetResumeSessionID(id, resumeSessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	_, err := s.db.Exec("UPDATE sessions SET resume_session_id = ? WHERE id = ?", strings.TrimSpace(resumeSessionID), id)
	if err != nil {
		log.Printf("[store] SetResumeSessionID: failed for session %s: %v", id, err)
	}
}

// GetResumeSessionID returns the stored agent-native resume session id for an attn session.
func (s *Store) GetResumeSessionID(id string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return ""
	}

	var resumeSessionID string
	if err := s.db.QueryRow("SELECT resume_session_id FROM sessions WHERE id = ?", id).Scan(&resumeSessionID); err != nil {
		return ""
	}
	return strings.TrimSpace(resumeSessionID)
}

// MarkSessionIntentionalClose durably records that this session's process is
// being killed on purpose (user close, delegate teardown, workspace close) —
// as opposed to dying on its own. Unlike the daemon's in-memory forced-stop
// mark (30s TTL, lost on restart), this survives both, so the ticket
// crash/reconcile seam can still tell a close from a crash when it runs late:
// after the TTL, or from the startup reap after a daemon restart. The mark
// lives on the session row and is garbage-collected with it (every close path
// deletes the row moments after the seam consumes the mark).
func (s *Store) MarkSessionIntentionalClose(id string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	_, err := s.db.Exec("UPDATE sessions SET closed_intentionally_at = ? WHERE id = ?",
		now.Format(time.RFC3339Nano), id)
	if err != nil {
		log.Printf("[store] MarkSessionIntentionalClose: failed for session %s: %v", id, err)
	}
}

// SessionCloseIntentional reports whether the session carries a durable
// intentional-close mark. Deliberately un-TTL'd: the reap that reads it can run
// arbitrarily long after the close (the daemon may have been down); staleness
// is handled by clearing the mark when recovery adopts the session as live.
func (s *Store) SessionCloseIntentional(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return false
	}

	var closedAt string
	if err := s.db.QueryRow("SELECT closed_intentionally_at FROM sessions WHERE id = ?", id).Scan(&closedAt); err != nil {
		return false
	}
	return strings.TrimSpace(closedAt) != ""
}

// ClearSessionIntentionalClose removes a stale intentional-close mark — set
// when a close was interrupted (daemon died between the mark and the kill) but
// the worker turned out to still be alive at recovery. A live session must not
// carry the mark, or a later genuine crash would be misread as a clean close.
func (s *Store) ClearSessionIntentionalClose(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	_, err := s.db.Exec("UPDATE sessions SET closed_intentionally_at = '' WHERE id = ?", id)
	if err != nil {
		log.Printf("[store] ClearSessionIntentionalClose: failed for session %s: %v", id, err)
	}
}

// GetAgentMetadata returns opaque plugin-owned JSON for a session.
func (s *Store) GetAgentMetadata(id string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return strings.TrimSpace(s.agentMetadata[id])
	}

	var metadata string
	if err := s.db.QueryRow("SELECT agent_metadata FROM sessions WHERE id = ?", id).Scan(&metadata); err != nil {
		return ""
	}
	return strings.TrimSpace(metadata)
}

// BeginAgentDriverRun records ownership and resets the cursor for a newly launched external agent run.
func (s *Store) BeginAgentDriverRun(id, pluginName, runID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	pluginName = strings.TrimSpace(pluginName)
	runID = strings.TrimSpace(runID)
	if pluginName == "" || runID == "" {
		return false
	}
	if s.db == nil {
		if s.sessions[id] == nil {
			return false
		}
		if s.agentDriverRuns == nil {
			s.agentDriverRuns = make(map[string]AgentDriverReportCursor)
		}
		s.agentDriverRuns[id] = AgentDriverReportCursor{PluginName: pluginName, RunID: runID}
		return true
	}
	result, err := s.db.Exec(
		"UPDATE sessions SET agent_driver_plugin_name = ?, agent_driver_run_id = ?, agent_driver_report_seq = 0 WHERE id = ?",
		pluginName,
		runID,
		id,
	)
	if err != nil {
		log.Printf("[store] BeginAgentDriverRun: failed for session %s: %v", id, err)
		return false
	}
	updated, _ := result.RowsAffected()
	return updated == 1
}

// GetAgentDriverRun returns the active owner and report cursor for an external driver run.
func (s *Store) GetAgentDriverRun(id string) AgentDriverReportCursor {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return s.agentDriverRuns[id]
	}
	var cursor AgentDriverReportCursor
	if err := s.db.QueryRow(
		"SELECT agent_driver_plugin_name, agent_driver_run_id, agent_driver_report_seq FROM sessions WHERE id = ?",
		id,
	).Scan(&cursor.PluginName, &cursor.RunID, &cursor.Seq); err != nil {
		return AgentDriverReportCursor{}
	}
	cursor.PluginName = strings.TrimSpace(cursor.PluginName)
	cursor.RunID = strings.TrimSpace(cursor.RunID)
	return cursor
}

// EndAgentDriverRun invalidates and returns the active external-driver run cursor.
func (s *Store) EndAgentDriverRun(id string) AgentDriverReportCursor {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		cursor := s.agentDriverRuns[id]
		if cursor.RunID == "" {
			return AgentDriverReportCursor{}
		}
		delete(s.agentDriverRuns, id)
		return cursor
	}
	var cursor AgentDriverReportCursor
	if err := s.db.QueryRow(
		"SELECT agent_driver_plugin_name, agent_driver_run_id, agent_driver_report_seq FROM sessions WHERE id = ?",
		id,
	).Scan(&cursor.PluginName, &cursor.RunID, &cursor.Seq); err != nil {
		return AgentDriverReportCursor{}
	}
	cursor.PluginName = strings.TrimSpace(cursor.PluginName)
	cursor.RunID = strings.TrimSpace(cursor.RunID)
	if cursor.RunID == "" {
		return AgentDriverReportCursor{}
	}
	result, err := s.db.Exec(
		"UPDATE sessions SET agent_driver_plugin_name = '', agent_driver_run_id = '', agent_driver_report_seq = 0 WHERE id = ? AND agent_driver_plugin_name = ? AND agent_driver_run_id = ?",
		id,
		cursor.PluginName,
		cursor.RunID,
	)
	if err != nil {
		log.Printf("[store] EndAgentDriverRun: failed for session %s: %v", id, err)
		return AgentDriverReportCursor{}
	}
	updated, _ := result.RowsAffected()
	if updated != 1 {
		return AgentDriverReportCursor{}
	}
	return cursor
}

// ApplyAgentDriverState applies an ordered status report for the active external-driver run.
func (s *Store) ApplyAgentDriverState(id, runID string, seq uint64, state string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	runID = strings.TrimSpace(runID)
	if runID == "" || seq == 0 {
		return false
	}
	now := time.Now().Format(time.RFC3339Nano)
	if s.db == nil {
		session := s.sessions[id]
		cursor := s.agentDriverRuns[id]
		if session == nil || cursor.RunID != runID || seq <= cursor.Seq {
			return false
		}
		cursor.Seq = seq
		s.agentDriverRuns[id] = cursor
		session.State = protocol.SessionState(state)
		session.StateSince = now
		session.StateUpdatedAt = now
		return true
	}
	result, err := s.db.Exec(`
		UPDATE sessions
		SET state = ?, state_since = ?, state_updated_at = ?, agent_driver_report_seq = ?
		WHERE id = ? AND agent_driver_run_id = ? AND agent_driver_report_seq < ?`,
		state,
		now,
		now,
		seq,
		id,
		runID,
		seq,
	)
	if err != nil {
		log.Printf("[store] ApplyAgentDriverState: failed for session %s: %v", id, err)
		return false
	}
	updated, _ := result.RowsAffected()
	return updated == 1
}

// ApplyAgentDriverMetadata applies ordered opaque metadata for the active external-driver run.
func (s *Store) ApplyAgentDriverMetadata(id, runID string, seq uint64, metadata string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	runID = strings.TrimSpace(runID)
	if runID == "" || seq == 0 {
		return false
	}
	if s.db == nil {
		cursor := s.agentDriverRuns[id]
		if s.sessions[id] == nil || cursor.RunID != runID || seq <= cursor.Seq {
			return false
		}
		cursor.Seq = seq
		s.agentDriverRuns[id] = cursor
		if s.agentMetadata == nil {
			s.agentMetadata = make(map[string]string)
		}
		s.agentMetadata[id] = strings.TrimSpace(metadata)
		return true
	}
	result, err := s.db.Exec(`
		UPDATE sessions
		SET agent_metadata = ?, agent_driver_report_seq = ?
		WHERE id = ? AND agent_driver_run_id = ? AND agent_driver_report_seq < ?`,
		strings.TrimSpace(metadata),
		seq,
		id,
		runID,
		seq,
	)
	if err != nil {
		log.Printf("[store] ApplyAgentDriverMetadata: failed for session %s: %v", id, err)
		return false
	}
	updated, _ := result.RowsAffected()
	return updated == 1
}

// SetPRs replaces all PRs, preserving muted state, detail fields, and computing HasNewChanges
func (s *Store) SetPRs(prs []*protocol.PR) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	// Get existing PRs to preserve muted state and details
	existing := make(map[string]*protocol.PR)
	rows, err := s.db.Query(`SELECT id, host, muted, details_fetched, details_fetched_at, mergeable, mergeable_state, ci_status, review_status, head_sha, head_branch, comment_count, approved_by_me, heat_state, last_heat_activity_at FROM prs`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var pr protocol.PR
			var muted, detailsFetched, approvedByMe int
			var detailsFetchedAt, mergeableState, ciStatus, reviewStatus, headSHA, headBranch sql.NullString
			var heatState, lastHeatActivityAt sql.NullString
			var mergeable sql.NullInt64
			var commentCount int

			if err := rows.Scan(&pr.ID, &pr.Host, &muted, &detailsFetched, &detailsFetchedAt, &mergeable, &mergeableState, &ciStatus, &reviewStatus, &headSHA, &headBranch, &commentCount, &approvedByMe, &heatState, &lastHeatActivityAt); err != nil {
				log.Printf("[store] SetPRs: failed to scan existing PR: %v", err)
				continue
			}
			pr.Muted = muted == 1
			pr.DetailsFetched = detailsFetched == 1
			if detailsFetchedAt.Valid {
				pr.DetailsFetchedAt = protocol.Ptr(detailsFetchedAt.String)
			}
			if mergeable.Valid {
				m := mergeable.Int64 == 1
				pr.Mergeable = &m
			}
			if mergeableState.Valid {
				pr.MergeableState = protocol.Ptr(mergeableState.String)
			}
			if ciStatus.Valid {
				pr.CIStatus = protocol.Ptr(ciStatus.String)
			}
			if reviewStatus.Valid {
				pr.ReviewStatus = protocol.Ptr(reviewStatus.String)
			}
			if headSHA.Valid {
				pr.HeadSHA = protocol.Ptr(headSHA.String)
			}
			if headBranch.Valid {
				pr.HeadBranch = protocol.Ptr(headBranch.String)
			}
			pr.CommentCount = protocol.Ptr(commentCount)
			pr.ApprovedByMe = approvedByMe == 1
			if heatState.Valid && heatState.String != "" {
				hs := protocol.HeatState(heatState.String)
				pr.HeatState = &hs
			} else {
				pr.HeatState = protocol.Ptr(protocol.HeatStateCold)
			}
			if lastHeatActivityAt.Valid {
				pr.LastHeatActivityAt = protocol.Ptr(lastHeatActivityAt.String)
			}
			normalizePRIdentity(&pr)
			existing[pr.ID] = &pr
		}
	}

	// Get interaction data for HasNewChanges computation
	interactions := make(map[string]struct {
		lastSeenSHA          string
		lastSeenCommentCount int
		lastSeenCIStatus     string
	})
	interRows, err := s.db.Query(`SELECT pr_id, last_seen_sha, last_seen_comment_count, last_seen_ci_status FROM pr_interactions`)
	if err == nil {
		defer interRows.Close()
		for interRows.Next() {
			var prID string
			var lastSHA, lastCIStatus sql.NullString
			var lastComments sql.NullInt64
			if err := interRows.Scan(&prID, &lastSHA, &lastComments, &lastCIStatus); err != nil {
				log.Printf("[store] SetPRs: failed to scan pr_interactions: %v", err)
				continue
			}
			interactions[prID] = struct {
				lastSeenSHA          string
				lastSeenCommentCount int
				lastSeenCIStatus     string
			}{
				lastSeenSHA:          lastSHA.String,
				lastSeenCommentCount: int(lastComments.Int64),
				lastSeenCIStatus:     lastCIStatus.String,
			}
		}
	}

	// Delete all PRs and re-insert
	s.execLog("DELETE FROM prs")

	for _, pr := range prs {
		normalizePRIdentity(pr)
		// Preserve state from existing
		if ex, ok := existing[pr.ID]; ok {
			pr.Muted = ex.Muted
			pr.ApprovedByMe = ex.ApprovedByMe // Always preserve approval state
			if pr.Host == "" {
				pr.Host = ex.Host
			}
			if ex.DetailsFetched {
				// Always preserve fetched details - they're more accurate than the basic list response
				// The details will be re-fetched when the PR becomes "hot" again
				pr.DetailsFetched = ex.DetailsFetched
				pr.DetailsFetchedAt = ex.DetailsFetchedAt
				pr.Mergeable = ex.Mergeable
				pr.MergeableState = ex.MergeableState
				pr.CIStatus = ex.CIStatus
				pr.ReviewStatus = ex.ReviewStatus
			}
			// Preserve HeadSHA and HeadBranch from existing if not set
			if protocol.Deref(pr.HeadSHA) == "" {
				pr.HeadSHA = ex.HeadSHA
			}
			if protocol.Deref(pr.HeadBranch) == "" {
				pr.HeadBranch = ex.HeadBranch
			}
			// Preserve heat state
			if pr.HeatState == nil || *pr.HeatState == protocol.HeatStateCold {
				pr.HeatState = ex.HeatState
				pr.LastHeatActivityAt = ex.LastHeatActivityAt
			}
		}

		// Compute HasNewChanges based on interaction tracking
		if inter, ok := interactions[pr.ID]; ok {
			// PR has been visited before - check for changes
			headSHA := protocol.Deref(pr.HeadSHA)
			if headSHA != "" && inter.lastSeenSHA != "" && headSHA != inter.lastSeenSHA {
				pr.HasNewChanges = true
			}
			if protocol.Deref(pr.CommentCount) > inter.lastSeenCommentCount {
				pr.HasNewChanges = true
			}
			// CI status changes only matter for authored or approved PRs
			ciStatus := protocol.Deref(pr.CIStatus)
			if (pr.Role == protocol.PRRoleAuthor || pr.ApprovedByMe) && ciStatus != "" {
				// CI finished (was pending, now success/failure)
				if inter.lastSeenCIStatus == "pending" && (ciStatus == "success" || ciStatus == "failure") {
					pr.HasNewChanges = true
				}
			}
		}
		// If no interaction record, HasNewChanges stays false (first time seeing this PR)

		var mergeableVal *int
		if pr.Mergeable != nil {
			v := boolToInt(*pr.Mergeable)
			mergeableVal = &v
		}

		// Ensure heat_state has a default value (NOT NULL column)
		heatState := protocol.DerefOr(pr.HeatState, protocol.HeatStateCold)

		s.execLog(`
			INSERT INTO prs (id, host, repo, number, title, url, author, role, state, reason, last_updated, last_polled, muted, details_fetched, details_fetched_at, mergeable, mergeable_state, ci_status, review_status, head_sha, head_branch, comment_count, approved_by_me, heat_state, last_heat_activity_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			pr.ID, pr.Host, pr.Repo, pr.Number, pr.Title, pr.URL, pr.Author, string(pr.Role), pr.State, pr.Reason,
			pr.LastUpdated, pr.LastPolled,
			boolToInt(pr.Muted), boolToInt(pr.DetailsFetched), nullPtrString(pr.DetailsFetchedAt),
			mergeableVal, nullPtrString(pr.MergeableState), nullPtrString(pr.CIStatus), nullPtrString(pr.ReviewStatus),
			nullPtrString(pr.HeadSHA), nullPtrString(pr.HeadBranch), protocol.Deref(pr.CommentCount), boolToInt(pr.ApprovedByMe),
			string(heatState), nullPtrString(pr.LastHeatActivityAt),
		)
	}
}

// AddPR adds or updates a single PR
func (s *Store) AddPR(pr *protocol.PR) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	var mergeableVal *int
	if pr.Mergeable != nil {
		v := boolToInt(*pr.Mergeable)
		mergeableVal = &v
	}

	// Ensure heat_state has a default value (NOT NULL column)
	heatState := protocol.DerefOr(pr.HeatState, protocol.HeatStateCold)

	normalizePRIdentity(pr)

	s.execLog(`
		INSERT OR REPLACE INTO prs (id, host, repo, number, title, url, author, role, state, reason, last_updated, last_polled, muted, details_fetched, details_fetched_at, mergeable, mergeable_state, ci_status, review_status, head_sha, head_branch, comment_count, approved_by_me, heat_state, last_heat_activity_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		pr.ID, pr.Host, pr.Repo, pr.Number, pr.Title, pr.URL, pr.Author, string(pr.Role), pr.State, pr.Reason,
		pr.LastUpdated, pr.LastPolled,
		boolToInt(pr.Muted), boolToInt(pr.DetailsFetched), nullPtrString(pr.DetailsFetchedAt),
		mergeableVal, nullPtrString(pr.MergeableState), nullPtrString(pr.CIStatus), nullPtrString(pr.ReviewStatus),
		nullPtrString(pr.HeadSHA), nullPtrString(pr.HeadBranch), protocol.Deref(pr.CommentCount), boolToInt(pr.ApprovedByMe),
		string(heatState), nullPtrString(pr.LastHeatActivityAt),
	)
}

// ListPRs returns PRs, optionally filtered by state, sorted by ID
func (s *Store) ListPRs(stateFilter string) []*protocol.PR {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil
	}

	var rows *sql.Rows
	var err error

	if stateFilter == "" {
		rows, err = s.db.Query(`SELECT id, host, repo, number, title, url, author, role, state, reason, last_updated, last_polled, muted, details_fetched, details_fetched_at, mergeable, mergeable_state, ci_status, review_status, head_sha, head_branch, comment_count, approved_by_me, heat_state, last_heat_activity_at FROM prs`)
	} else {
		rows, err = s.db.Query(`SELECT id, host, repo, number, title, url, author, role, state, reason, last_updated, last_polled, muted, details_fetched, details_fetched_at, mergeable, mergeable_state, ci_status, review_status, head_sha, head_branch, comment_count, approved_by_me, heat_state, last_heat_activity_at FROM prs WHERE state = ?`, stateFilter)
	}
	if err != nil {
		return nil
	}
	defer rows.Close()

	var result []*protocol.PR
	for rows.Next() {
		pr := scanPR(rows)
		if pr != nil {
			result = append(result, pr)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})

	return result
}

// ToggleMutePR toggles a PR's muted state
func (s *Store) ToggleMutePR(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	_, err := s.db.Exec("UPDATE prs SET muted = NOT muted WHERE id = ?", id)
	if err != nil {
		log.Printf("[store] ToggleMutePR: failed for PR %s: %v", id, err)
	}
}

// GetPR returns a PR by ID
func (s *Store) GetPR(id string) *protocol.PR {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil
	}

	row := s.db.QueryRow(`SELECT id, host, repo, number, title, url, author, role, state, reason, last_updated, last_polled, muted, details_fetched, details_fetched_at, mergeable, mergeable_state, ci_status, review_status, head_sha, head_branch, comment_count, approved_by_me, heat_state, last_heat_activity_at FROM prs WHERE id = ?`, id)
	return scanPRRow(row)
}

// UpdatePRDetails updates the detail fields for a PR
func (s *Store) UpdatePRDetails(id string, mergeable *bool, mergeableState, ciStatus, reviewStatus, headSHA, headBranch string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	var mergeableVal *int
	if mergeable != nil {
		v := boolToInt(*mergeable)
		mergeableVal = &v
	}

	now := time.Now().Format(time.RFC3339)
	s.execLog(`UPDATE prs SET details_fetched = 1, details_fetched_at = ?, mergeable = ?, mergeable_state = ?, ci_status = ?, review_status = ?, head_sha = ?, head_branch = ? WHERE id = ?`,
		now, mergeableVal, mergeableState, ciStatus, reviewStatus, headSHA, headBranch, id)
}

// ListPRsByRepo returns all PRs for a specific repo
func (s *Store) ListPRsByRepo(repo string) []*protocol.PR {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil
	}

	rows, err := s.db.Query(`SELECT id, host, repo, number, title, url, author, role, state, reason, last_updated, last_polled, muted, details_fetched, details_fetched_at, mergeable, mergeable_state, ci_status, review_status, head_sha, head_branch, comment_count, approved_by_me, heat_state, last_heat_activity_at FROM prs WHERE repo = ?`, repo)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var result []*protocol.PR
	for rows.Next() {
		pr := scanPR(rows)
		if pr != nil {
			result = append(result, pr)
		}
	}
	return result
}

// ListPRsByRepoHost returns all PRs for a specific repo on a specific host.
func (s *Store) ListPRsByRepoHost(repo, host string) []*protocol.PR {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil
	}

	rows, err := s.db.Query(`SELECT id, host, repo, number, title, url, author, role, state, reason, last_updated, last_polled, muted, details_fetched, details_fetched_at, mergeable, mergeable_state, ci_status, review_status, head_sha, head_branch, comment_count, approved_by_me, heat_state, last_heat_activity_at FROM prs WHERE repo = ? AND host = ?`, repo, host)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var result []*protocol.PR
	for rows.Next() {
		pr := scanPR(rows)
		if pr != nil {
			result = append(result, pr)
		}
	}
	return result
}

// GetRepoState returns the state for a repo, or nil if not set
func (s *Store) GetRepoState(repo string) *protocol.RepoState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil
	}

	var state protocol.RepoState
	var muted, collapsed int

	err := s.db.QueryRow("SELECT repo, muted, collapsed FROM repos WHERE repo = ?", repo).Scan(
		&state.Repo, &muted, &collapsed,
	)
	if err != nil {
		return nil
	}

	state.Muted = muted == 1
	state.Collapsed = collapsed == 1
	return &state
}

// ToggleMuteRepo toggles a repo's muted state
func (s *Store) ToggleMuteRepo(repo string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	// Insert if not exists, then toggle
	s.execLog("INSERT OR IGNORE INTO repos (repo, muted, collapsed) VALUES (?, 0, 0)", repo)
	s.execLog("UPDATE repos SET muted = NOT muted WHERE repo = ?", repo)
}

// SetRepoCollapsed sets a repo's collapsed state
func (s *Store) SetRepoCollapsed(repo string, collapsed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	s.execLog("INSERT OR IGNORE INTO repos (repo, muted, collapsed) VALUES (?, 0, 0)", repo)
	s.execLog("UPDATE repos SET collapsed = ? WHERE repo = ?", boolToInt(collapsed), repo)
}

// ListRepoStates returns all repo states
func (s *Store) ListRepoStates() []*protocol.RepoState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil
	}

	rows, err := s.db.Query("SELECT repo, muted, collapsed FROM repos")
	if err != nil {
		return nil
	}
	defer rows.Close()

	var result []*protocol.RepoState
	for rows.Next() {
		var state protocol.RepoState
		var muted, collapsed int

		err := rows.Scan(&state.Repo, &muted, &collapsed)
		if err != nil {
			continue
		}

		state.Muted = muted == 1
		state.Collapsed = collapsed == 1
		result = append(result, &state)
	}
	return result
}

// ToggleMuteAuthor toggles a PR author's muted state
func (s *Store) ToggleMuteAuthor(author string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	// Insert if not exists, then toggle
	s.execLog("INSERT OR IGNORE INTO authors (author, muted) VALUES (?, 0)", author)
	s.execLog("UPDATE authors SET muted = NOT muted WHERE author = ?", author)
}

// ListAuthorStates returns all author states
func (s *Store) ListAuthorStates() []*protocol.AuthorState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil
	}

	rows, err := s.db.Query("SELECT author, muted FROM authors")
	if err != nil {
		return nil
	}
	defer rows.Close()

	var result []*protocol.AuthorState
	for rows.Next() {
		var state protocol.AuthorState
		var muted int

		err := rows.Scan(&state.Author, &muted)
		if err != nil {
			continue
		}

		state.Muted = muted == 1
		result = append(result, &state)
	}
	return result
}

// MarkPRVisited marks a PR as visited by the user, updating the interaction record
// and clearing HasNewChanges for subsequent polls
func (s *Store) MarkPRVisited(prID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	// Get current PR state
	var headSHA, ciStatus sql.NullString
	var commentCount int
	err := s.db.QueryRow("SELECT head_sha, comment_count, ci_status FROM prs WHERE id = ?", prID).Scan(&headSHA, &commentCount, &ciStatus)
	if err != nil {
		return
	}

	// Upsert interaction record
	now := time.Now().Format(time.RFC3339)
	s.execLog(`
		INSERT INTO pr_interactions (pr_id, last_visited_at, last_seen_sha, last_seen_comment_count, last_seen_ci_status)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(pr_id) DO UPDATE SET
			last_visited_at = excluded.last_visited_at,
			last_seen_sha = excluded.last_seen_sha,
			last_seen_comment_count = excluded.last_seen_comment_count,
			last_seen_ci_status = excluded.last_seen_ci_status`,
		prID, now, headSHA.String, commentCount, ciStatus.String,
	)
}

// MarkPRApproved marks a PR as approved by the user
func (s *Store) MarkPRApproved(prID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	// Get current PR state for updating interaction
	var headSHA, ciStatus sql.NullString
	var commentCount int
	err := s.db.QueryRow("SELECT head_sha, comment_count, ci_status FROM prs WHERE id = ?", prID).Scan(&headSHA, &commentCount, &ciStatus)
	if err != nil {
		return
	}

	// Upsert interaction record with approval timestamp
	now := time.Now().Format(time.RFC3339)
	s.execLog(`
		INSERT INTO pr_interactions (pr_id, last_visited_at, last_approved_at, last_seen_sha, last_seen_comment_count, last_seen_ci_status)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(pr_id) DO UPDATE SET
			last_visited_at = excluded.last_visited_at,
			last_approved_at = excluded.last_approved_at,
			last_seen_sha = excluded.last_seen_sha,
			last_seen_comment_count = excluded.last_seen_comment_count,
			last_seen_ci_status = excluded.last_seen_ci_status`,
		prID, now, now, headSHA.String, commentCount, ciStatus.String,
	)

	// Also update the PR's approved_by_me flag
	s.execLog("UPDATE prs SET approved_by_me = 1 WHERE id = ?", prID)
}

// SetPRHot sets a PR to hot state and updates last activity time
func (s *Store) SetPRHot(prID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	now := time.Now().Format(time.RFC3339)
	s.execLog(`UPDATE prs SET heat_state = ?, last_heat_activity_at = ? WHERE id = ?`,
		protocol.HeatStateHot, now, prID)
}

// DecayHeatStates transitions PRs from hot→warm→cold based on elapsed time
func (s *Store) DecayHeatStates() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	now := time.Now()
	warmThreshold := now.Add(-protocol.HeatHotDuration).Format(time.RFC3339)
	coldThreshold := now.Add(-protocol.HeatWarmDuration).Format(time.RFC3339)

	// Hot → Warm (after 3 min)
	s.execLog(`UPDATE prs SET heat_state = ? WHERE heat_state = ? AND last_heat_activity_at < ?`,
		protocol.HeatStateWarm, protocol.HeatStateHot, warmThreshold)

	// Warm → Cold (after 10 min)
	s.execLog(`UPDATE prs SET heat_state = ? WHERE heat_state = ? AND last_heat_activity_at < ?`,
		protocol.HeatStateCold, protocol.HeatStateWarm, coldThreshold)
}

// GetPRsNeedingDetailRefresh returns visible PRs that need detail refresh based on heat state
func (s *Store) GetPRsNeedingDetailRefresh() []*protocol.PR {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil
	}

	// Get muted repos
	mutedRepos := make(map[string]bool)
	repoRows, err := s.db.Query("SELECT repo FROM repos WHERE muted = 1")
	if err != nil {
		log.Printf("[store] GetPRsNeedingDetailRefresh: failed to query muted repos: %v", err)
	} else {
		defer repoRows.Close()
		for repoRows.Next() {
			var repo string
			if err := repoRows.Scan(&repo); err != nil {
				log.Printf("[store] GetPRsNeedingDetailRefresh: failed to scan repo: %v", err)
				continue
			}
			mutedRepos[repo] = true
		}
	}

	now := time.Now()
	var result []*protocol.PR

	rows, err := s.db.Query(`
		SELECT id, host, repo, number, title, url, author, role, state, reason, last_updated, last_polled,
		       muted, details_fetched, details_fetched_at, mergeable, mergeable_state,
		       ci_status, review_status, head_sha, head_branch, comment_count, approved_by_me,
		       heat_state, last_heat_activity_at
		FROM prs
		WHERE muted = 0`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	for rows.Next() {
		pr := scanPR(rows)
		if pr == nil {
			continue
		}

		// Skip muted repos
		if mutedRepos[pr.Repo] {
			continue
		}

		// Check if refresh needed based on heat state
		detailsFetchedAt := protocol.Timestamp(protocol.Deref(pr.DetailsFetchedAt)).Time()
		elapsed := now.Sub(detailsFetchedAt)
		needsRefresh := false

		heatState := protocol.DerefOr(pr.HeatState, protocol.HeatStateCold)
		switch heatState {
		case protocol.HeatStateHot:
			needsRefresh = elapsed > protocol.HeatHotInterval
		case protocol.HeatStateWarm:
			needsRefresh = elapsed > protocol.HeatWarmInterval
		default: // cold
			needsRefresh = elapsed > protocol.HeatColdInterval
		}

		// Also refresh if details were never fetched
		if !pr.DetailsFetched {
			needsRefresh = true
		}

		if needsRefresh {
			result = append(result, pr)
		}
	}

	return result
}

// Settings methods

// GetSetting returns a setting value by key, or empty string if not set
func (s *Store) GetSetting(key string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return ""
	}

	var value sql.NullString
	err := s.db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if err != nil {
		return ""
	}
	return value.String
}

// SetSetting sets a setting value (upserts)
func (s *Store) SetSetting(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	s.execLog(`
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value)
}

// DeleteSetting removes a setting row. No-op if the key is absent or the store
// has no live DB. Used by one-time settings-key migrations to drop the stale
// row after copying its value to the renamed key.
func (s *Store) DeleteSetting(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	s.execLog(`DELETE FROM settings WHERE key = ?`, key)
}

// GetAllSettings returns all settings as a map
func (s *Store) GetAllSettings() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]string)
	if s.db == nil {
		return result
	}

	rows, err := s.db.Query("SELECT key, value FROM settings")
	if err != nil {
		return result
	}
	defer rows.Close()

	for rows.Next() {
		var key string
		var value sql.NullString
		if err := rows.Scan(&key, &value); err == nil {
			result[key] = value.String
		}
	}
	return result
}

// GetProfileRole returns the session assigned to a profile-wide role.
func (s *Store) GetProfileRole(role string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	role = strings.TrimSpace(role)
	if role == "" {
		return ""
	}
	if s.db == nil {
		return strings.TrimSpace(s.profileRoles[role])
	}

	var sessionID string
	if err := s.db.QueryRow(
		"SELECT session_id FROM profile_roles WHERE role = ?",
		role,
	).Scan(&sessionID); err != nil {
		return ""
	}
	return strings.TrimSpace(sessionID)
}

// SetProfileRole atomically assigns a profile-wide role to one session.
func (s *Store) SetProfileRole(role, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	role = strings.TrimSpace(role)
	sessionID = strings.TrimSpace(sessionID)
	if role == "" {
		return fmt.Errorf("role cannot be empty")
	}
	if sessionID == "" {
		return fmt.Errorf("session id cannot be empty")
	}
	if s.db == nil {
		if s.profileRoles == nil {
			s.profileRoles = make(map[string]string)
		}
		s.profileRoles[role] = sessionID
		return nil
	}

	_, err := s.db.Exec(`
		INSERT INTO profile_roles (role, session_id) VALUES (?, ?)
		ON CONFLICT(role) DO UPDATE SET session_id = excluded.session_id`,
		role,
		sessionID,
	)
	return err
}

// ClearProfileRole removes a role only when the expected session still holds
// it, so a stale client cannot clear a role that has since been transferred.
func (s *Store) ClearProfileRole(role, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	role = strings.TrimSpace(role)
	sessionID = strings.TrimSpace(sessionID)
	if role == "" {
		return fmt.Errorf("role cannot be empty")
	}
	if s.db == nil {
		if strings.TrimSpace(s.profileRoles[role]) == sessionID {
			delete(s.profileRoles, role)
		}
		return nil
	}

	_, err := s.db.Exec(
		"DELETE FROM profile_roles WHERE role = ? AND session_id = ?",
		role,
		sessionID,
	)
	return err
}

// Recent Locations methods

// resolveRecentLocationPath collapses a path inside a linked git worktree to
// the worktree's main repository root so all worktrees of a repo share one
// recent-locations entry. Non-worktree paths are returned unchanged.
func resolveRecentLocationPath(path string) string {
	for dir := filepath.Clean(path); ; {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			if mainRepo := git.GetMainRepoFromWorktree(dir); mainRepo != "" {
				return mainRepo
			}
			return path
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return path
		}
		dir = parent
	}
}

// frecencyScore ranks a location by combining how often it is used with how
// recently it was used, so a frequently-used project keeps a stable slot near
// the top of the picker even after one-off sessions elsewhere.
func frecencyScore(useCount int, lastSeen string, now time.Time) float64 {
	count := float64(useCount)
	t, err := time.Parse(time.RFC3339, lastSeen)
	if err != nil {
		return count * 0.25
	}
	switch age := now.Sub(t); {
	case age < time.Hour:
		return count * 4
	case age < 24*time.Hour:
		return count * 2
	case age < 7*24*time.Hour:
		return count * 0.5
	default:
		return count * 0.25
	}
}

// UpsertRecentLocation adds or updates a location in the recent locations table
func (s *Store) UpsertRecentLocation(path string) {
	path = resolveRecentLocationPath(path)

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Format(time.RFC3339)
	if s.db == nil {
		if s.recentLocations == nil {
			s.recentLocations = make(map[string]*protocol.RecentLocation)
		}
		if existing := s.recentLocations[path]; existing != nil {
			existing.LastSeen = now
			existing.UseCount++
			return
		}
		s.recentLocations[path] = &protocol.RecentLocation{
			Path:     path,
			LastSeen: now,
			UseCount: 1,
		}
		return
	}

	s.execLog(`
		INSERT INTO recent_locations (path, last_seen, use_count)
		VALUES (?, ?, 1)
		ON CONFLICT(path) DO UPDATE SET
			last_seen = excluded.last_seen,
			use_count = use_count + 1`,
		path, now)
}

// GetRecentLocations returns recent locations that still exist on disk,
// ranked by frecency (frequency weighted by recency)
func (s *Store) GetRecentLocations(limit int) []*protocol.RecentLocation {
	if limit <= 0 {
		limit = 20
	}

	s.mu.RLock()
	var raw []*protocol.RecentLocation
	if s.db == nil {
		raw = make([]*protocol.RecentLocation, 0, len(s.recentLocations))
		for _, loc := range s.recentLocations {
			cloned := *loc
			raw = append(raw, &cloned)
		}
	} else {
		// Fetch every row: ranking happens below, and pre-truncating here
		// (e.g. by last_seen) would hide old-but-frequent locations. The
		// table stays small via missing-path cleanup and
		// CleanupStaleLocations.
		rows, err := s.db.Query(`
			SELECT path, last_seen, use_count
			FROM recent_locations`)
		if err != nil {
			s.mu.RUnlock()
			return nil
		}
		for rows.Next() {
			var loc protocol.RecentLocation
			if err := rows.Scan(&loc.Path, &loc.LastSeen, &loc.UseCount); err != nil {
				continue
			}
			raw = append(raw, &loc)
		}
		rows.Close()
	}
	s.mu.RUnlock()

	// Merge rows recorded before worktree paths collapsed into their main
	// repository root, and drop entries whose directory disappeared.
	var toDelete []string
	merged := make(map[string]*protocol.RecentLocation, len(raw))
	for _, loc := range raw {
		if _, err := os.Stat(loc.Path); os.IsNotExist(err) {
			toDelete = append(toDelete, loc.Path)
			continue
		}
		resolved := resolveRecentLocationPath(loc.Path)
		if existing := merged[resolved]; existing != nil {
			existing.UseCount += loc.UseCount
			if loc.LastSeen > existing.LastSeen {
				existing.LastSeen = loc.LastSeen
			}
			continue
		}
		loc.Path = resolved
		merged[resolved] = loc
	}

	result := make([]*protocol.RecentLocation, 0, len(merged))
	for _, loc := range merged {
		result = append(result, loc)
	}
	now := time.Now()
	sort.Slice(result, func(i, j int) bool {
		si := frecencyScore(result[i].UseCount, result[i].LastSeen, now)
		sj := frecencyScore(result[j].UseCount, result[j].LastSeen, now)
		if si != sj {
			return si > sj
		}
		if result[i].LastSeen != result[j].LastSeen {
			return result[i].LastSeen > result[j].LastSeen
		}
		return result[i].Path < result[j].Path
	})
	if len(result) > limit {
		result = result[:limit]
	}

	// Clean up non-existent paths (async, don't block the read)
	if len(toDelete) > 0 && s.db != nil {
		go func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			for _, path := range toDelete {
				s.execLog("DELETE FROM recent_locations WHERE path = ?", path)
			}
		}()
	}

	return result
}

// CleanupStaleLocations removes entries older than the given duration
func (s *Store) CleanupStaleLocations(maxAge time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		cutoff := time.Now().Add(-maxAge)
		removed := 0
		for path, loc := range s.recentLocations {
			lastSeen, err := time.Parse(time.RFC3339, loc.LastSeen)
			if err != nil || lastSeen.Before(cutoff) {
				delete(s.recentLocations, path)
				removed++
			}
		}
		return removed
	}

	cutoff := time.Now().Add(-maxAge).Format(time.RFC3339)
	result, err := s.db.Exec("DELETE FROM recent_locations WHERE last_seen < ?", cutoff)
	if err != nil {
		log.Printf("[store] CleanupStaleLocations: failed: %v", err)
		return 0
	}

	affected, _ := result.RowsAffected()
	return int(affected)
}

// RemoveRecentLocation removes a specific location from recent locations
func (s *Store) RemoveRecentLocation(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		delete(s.recentLocations, path)
		return
	}

	s.execLog("DELETE FROM recent_locations WHERE path = ?", path)
}

// Helper functions

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullPtrString(s *string) interface{} {
	if s == nil || *s == "" {
		return nil
	}
	return *s
}

func normalizePRIdentity(pr *protocol.PR) {
	if pr == nil {
		return
	}

	if pr.Host == "" || !strings.Contains(pr.ID, ":") {
		if host, repo, number, err := protocol.ParsePRID(pr.ID); err == nil {
			if pr.Host == "" {
				pr.Host = host
			}
			if pr.Repo == "" {
				pr.Repo = repo
			}
			if pr.Number == 0 {
				pr.Number = number
			}
			if !strings.Contains(pr.ID, ":") {
				pr.ID = protocol.FormatPRID(pr.Host, pr.Repo, pr.Number)
			}
		}
	}

	if pr.Host == "" {
		pr.Host = "github.com"
	}
	if pr.ID == "" && pr.Repo != "" && pr.Number != 0 {
		pr.ID = protocol.FormatPRID(pr.Host, pr.Repo, pr.Number)
	}
}

func scanPR(rows *sql.Rows) *protocol.PR {
	var pr protocol.PR
	var muted, detailsFetched, approvedByMe int
	var lastUpdated, lastPolled string
	var detailsFetchedAt, mergeableState, ciStatus, reviewStatus, headSHA, headBranch sql.NullString
	var heatState, lastHeatActivityAt sql.NullString
	var mergeable sql.NullInt64
	var commentCount int

	err := rows.Scan(
		&pr.ID, &pr.Host, &pr.Repo, &pr.Number, &pr.Title, &pr.URL, &pr.Author, &pr.Role, &pr.State, &pr.Reason,
		&lastUpdated, &lastPolled, &muted, &detailsFetched, &detailsFetchedAt,
		&mergeable, &mergeableState, &ciStatus, &reviewStatus,
		&headSHA, &headBranch, &commentCount, &approvedByMe,
		&heatState, &lastHeatActivityAt,
	)
	if err != nil {
		return nil
	}

	pr.LastUpdated = lastUpdated
	pr.LastPolled = lastPolled
	pr.Muted = muted == 1
	pr.DetailsFetched = detailsFetched == 1
	if detailsFetchedAt.Valid {
		pr.DetailsFetchedAt = protocol.Ptr(detailsFetchedAt.String)
	}
	if mergeable.Valid {
		m := mergeable.Int64 == 1
		pr.Mergeable = &m
	}
	if mergeableState.Valid {
		pr.MergeableState = protocol.Ptr(mergeableState.String)
	}
	if ciStatus.Valid {
		pr.CIStatus = protocol.Ptr(ciStatus.String)
	}
	if reviewStatus.Valid {
		pr.ReviewStatus = protocol.Ptr(reviewStatus.String)
	}
	if headSHA.Valid {
		pr.HeadSHA = protocol.Ptr(headSHA.String)
	}
	if headBranch.Valid {
		pr.HeadBranch = protocol.Ptr(headBranch.String)
	}
	pr.CommentCount = protocol.Ptr(commentCount)
	pr.ApprovedByMe = approvedByMe == 1
	if heatState.Valid && heatState.String != "" {
		hs := protocol.HeatState(heatState.String)
		pr.HeatState = &hs
	} else {
		pr.HeatState = protocol.Ptr(protocol.HeatStateCold)
	}
	if lastHeatActivityAt.Valid {
		pr.LastHeatActivityAt = protocol.Ptr(lastHeatActivityAt.String)
	}

	normalizePRIdentity(&pr)
	return &pr
}

func scanPRRow(row *sql.Row) *protocol.PR {
	var pr protocol.PR
	var muted, detailsFetched, approvedByMe int
	var lastUpdated, lastPolled string
	var detailsFetchedAt, mergeableState, ciStatus, reviewStatus, headSHA, headBranch sql.NullString
	var heatState, lastHeatActivityAt sql.NullString
	var mergeable sql.NullInt64
	var commentCount int

	err := row.Scan(
		&pr.ID, &pr.Host, &pr.Repo, &pr.Number, &pr.Title, &pr.URL, &pr.Author, &pr.Role, &pr.State, &pr.Reason,
		&lastUpdated, &lastPolled, &muted, &detailsFetched, &detailsFetchedAt,
		&mergeable, &mergeableState, &ciStatus, &reviewStatus,
		&headSHA, &headBranch, &commentCount, &approvedByMe,
		&heatState, &lastHeatActivityAt,
	)
	if err != nil {
		return nil
	}

	pr.LastUpdated = lastUpdated
	pr.LastPolled = lastPolled
	pr.Muted = muted == 1
	pr.DetailsFetched = detailsFetched == 1
	if detailsFetchedAt.Valid {
		pr.DetailsFetchedAt = protocol.Ptr(detailsFetchedAt.String)
	}
	if mergeable.Valid {
		m := mergeable.Int64 == 1
		pr.Mergeable = &m
	}
	if mergeableState.Valid {
		pr.MergeableState = protocol.Ptr(mergeableState.String)
	}
	if ciStatus.Valid {
		pr.CIStatus = protocol.Ptr(ciStatus.String)
	}
	if reviewStatus.Valid {
		pr.ReviewStatus = protocol.Ptr(reviewStatus.String)
	}
	if headSHA.Valid {
		pr.HeadSHA = protocol.Ptr(headSHA.String)
	}
	if headBranch.Valid {
		pr.HeadBranch = protocol.Ptr(headBranch.String)
	}
	pr.CommentCount = protocol.Ptr(commentCount)
	pr.ApprovedByMe = approvedByMe == 1
	if heatState.Valid && heatState.String != "" {
		hs := protocol.HeatState(heatState.String)
		pr.HeatState = &hs
	} else {
		pr.HeatState = protocol.Ptr(protocol.HeatStateCold)
	}
	if lastHeatActivityAt.Valid {
		pr.LastHeatActivityAt = protocol.Ptr(lastHeatActivityAt.String)
	}

	normalizePRIdentity(&pr)
	return &pr
}
