package store

import (
	"database/sql"
	"encoding/json"
	"errors"
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
	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/workspacelayout"
)

type Store struct {
	mu     sync.RWMutex
	db     *sql.DB
	dbPath string

	// BackupNow refuses when false: VACUUM INTO an in-memory fallback writes empty snapshots and rotation prunes the real ones.
	durable bool

	sessions        map[string]*protocol.Session
	turnStamps      map[string]TurnStamps
	activityCursors map[string]string
	sessionCosts    map[string]SessionCostState
	agentDriverRuns map[string]AgentDriverReportCursor
	teardownIntents map[string]SessionTeardownIntent
	sessionCloses   map[string]sessionCloseMark
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

type SessionTeardownIntent struct {
	RequestedAt time.Time
	DriverRun   AgentDriverReportCursor
}

// Seq is the run's report cursor: a replacement driver must continue from it, because applyState discards anything that does not advance it.
type ActiveAgentDriverRun struct {
	SessionID  string
	RunID      string
	Metadata   string
	Seq        uint64
	PluginName string
}

type LaunchIntent struct {
	YoloMode bool `json:"yolo_mode,omitempty"`
	// nil means "follow the promoted config", not off.
	AutoMode               *bool                        `json:"auto_mode,omitempty"`
	ApprovalRoute          launchcontract.ApprovalRoute `json:"approval_route,omitempty"`
	Executable             string                       `json:"executable,omitempty"`
	Model                  string                       `json:"model,omitempty"`
	Effort                 string                       `json:"effort,omitempty"`
	ChiefOfStaff           bool                         `json:"chief_of_staff,omitempty"`
	ResumeConversationFile string                       `json:"resume_conversation_file,omitempty"`
	// Zero value means attended.
	UnattendedLaunch launchcontract.UnattendedLaunchSpec `json:"unattended_launch,omitzero"`
	// Empty for every PTY session: a PTY relaunch resumes a transcript, so replaying the prompt re-runs work already done. Filled only for drivers declaring the `conversation` capability.
	InitialPrompt string `json:"initial_prompt,omitempty"`
}

func New() *Store {
	db, err := OpenDB(":memory:")
	if err != nil {
		return newMapBackedStore()
	}
	return &Store{db: db}
}

func newMapBackedStore() *Store {
	return &Store{
		sessions:        make(map[string]*protocol.Session),
		agentDriverRuns: make(map[string]AgentDriverReportCursor),
		teardownIntents: make(map[string]SessionTeardownIntent),
		sessionCloses:   make(map[string]sessionCloseMark),
		sessionCosts:    make(map[string]SessionCostState),
		agentMetadata:   make(map[string]string),
		profileRoles:    make(map[string]string),
		workspaces:      make(map[string]workspacelayout.WorkspaceLayout),
		recentLocations: make(map[string]*protocol.RecentLocation),
	}
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
	if session.PinnedAt != nil {
		cloned.PinnedAt = protocol.Ptr(protocol.Deref(session.PinnedAt))
	}
	if session.ContextWindowCap != nil {
		cloned.ContextWindowCap = protocol.Ptr(protocol.Deref(session.ContextWindowCap))
	}
	if session.ParentSessionID != nil {
		cloned.ParentSessionID = protocol.Ptr(protocol.Deref(session.ParentSessionID))
	}
	if session.Activity != nil {
		cloned.Activity = protocol.Ptr(protocol.Deref(session.Activity))
	}
	if session.ActivityAt != nil {
		cloned.ActivityAt = protocol.Ptr(protocol.Deref(session.ActivityAt))
	}
	if session.Todos != nil {
		cloned.Todos = append([]string(nil), session.Todos...)
	}
	return &cloned
}

func NewWithDB(dbPath string) (*Store, error) {
	db, err := OpenDB(dbPath)
	if err != nil {
		return nil, err
	}
	return &Store{db: db, dbPath: dbPath, durable: true}, nil
}

func (s *Store) DatabasePath() string {
	if s == nil {
		return ""
	}
	return s.dbPath
}

func NewWithPersistence(path string) *Store {
	dbPath := config.DBPath()
	store, err := NewWithDB(dbPath)
	if err != nil {
		return New()
	}
	return store
}

func DefaultStatePath() string {
	return config.StatePath()
}

func (s *Store) execLog(query string, args ...interface{}) {
	if _, err := s.db.Exec(query, args...); err != nil {
		log.Printf("[store] exec error: %v (query: %.50s...)", err, query)
	}
}

func (s *Store) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *Store) Add(session *protocol.Session) {
	if err := s.AddChecked(session); err != nil {
		log.Printf("[store] Add: failed to insert session %s: %v", session.ID, err)
	}
}

func (s *Store) AddChecked(session *protocol.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addCheckedLocked(session, false)
}

func (s *Store) AddCheckedUnlessTeardown(session *protocol.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addCheckedLocked(session, true)
}

// A closed row is refused rather than overwritten: re-registering a closed id
// would run a session no live surface can see. Reopening clears the close first.
func (s *Store) addCheckedLocked(session *protocol.Session, rejectTeardown bool) error {
	if s.db == nil {
		if _, closing := s.teardownIntents[session.ID]; rejectTeardown && closing {
			return fmt.Errorf("session %s is closing", session.ID)
		}
		if _, closed := s.sessionCloses[session.ID]; closed {
			return fmt.Errorf("add session %s: %w", session.ID, ErrSessionClosed)
		}
		if s.sessions == nil {
			s.sessions = make(map[string]*protocol.Session)
		}
		stored := cloneSession(session)
		if stored.LastModelRequestAt == nil && stored.StateUpdatedAt != "" {
			stored.LastModelRequestAt = protocol.Ptr(stored.StateUpdatedAt)
		}
		// pinned_at, the context-window cap and the activity pair are absent from the SQLite upsert below; carry the stored values so the memory branch cannot clear what their own writers own.
		if existing := s.sessions[session.ID]; existing != nil {
			if existing.LastModelRequestAt != nil {
				stored.LastModelRequestAt = protocol.Ptr(protocol.Deref(existing.LastModelRequestAt))
			}
			if existing.PinnedAt != nil {
				stored.PinnedAt = protocol.Ptr(protocol.Deref(existing.PinnedAt))
			}
			if existing.ContextWindowCap != nil {
				stored.ContextWindowCap = protocol.Ptr(protocol.Deref(existing.ContextWindowCap))
			}
			if existing.Activity != nil {
				stored.Activity = protocol.Ptr(protocol.Deref(existing.Activity))
				stored.ActivityAt = protocol.Ptr(protocol.Deref(existing.ActivityAt))
			}
		}
		s.sessions[session.ID] = stored
		return nil
	}
	if rejectTeardown {
		var closing int
		if err := s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM session_teardown_tombstones WHERE session_id = ?)", session.ID).Scan(&closing); err != nil {
			return fmt.Errorf("check session %s teardown before insert: %w", session.ID, err)
		}
		if closing == 1 {
			return fmt.Errorf("session %s is closing", session.ID)
		}
	}

	if s.sessionClosedLocked(session.ID) {
		return fmt.Errorf("add session %s: %w", session.ID, ErrSessionClosed)
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
	lastModelRequestAt := protocol.Deref(session.LastModelRequestAt)
	if lastModelRequestAt == "" {
		lastModelRequestAt = session.StateUpdatedAt
	}
	// pinned_at is deliberately absent from the column list and the conflict update: leaving it out is what makes a respawn unable to clear the pin.
	_, err = s.db.Exec(`
		INSERT INTO sessions
		(id, label, agent, directory, endpoint_id, workspace_id, branch, is_worktree, main_repo, repository, state, state_since, state_updated_at, last_model_request_at, parent_session_id, todos, last_seen)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			label = excluded.label,
			agent = excluded.agent,
			directory = excluded.directory,
			endpoint_id = excluded.endpoint_id,
			workspace_id = excluded.workspace_id,
			branch = excluded.branch,
			is_worktree = excluded.is_worktree,
			main_repo = excluded.main_repo,
			repository = excluded.repository,
			state = excluded.state,
			state_since = excluded.state_since,
			state_updated_at = excluded.state_updated_at,
			last_model_request_at = CASE
				WHEN sessions.last_model_request_at IS NULL OR sessions.last_model_request_at = '' THEN excluded.last_model_request_at
				ELSE sessions.last_model_request_at
			END,
			parent_session_id = excluded.parent_session_id,
			todos = excluded.todos,
			last_seen = excluded.last_seen`,
		session.ID,
		session.Label,
		session.Agent,
		session.Directory,
		protocol.Deref(session.EndpointID),
		session.WorkspaceID,
		protocol.Deref(session.Branch),
		boolToInt(protocol.Deref(session.IsWorktree)),
		protocol.Deref(session.MainRepo),
		protocol.Deref(session.Repository),
		string(session.State),
		session.StateSince,
		session.StateUpdatedAt,
		lastModelRequestAt,
		protocol.Deref(session.ParentSessionID),
		string(todosJSON),
		session.LastSeen,
	)
	if err != nil {
		return fmt.Errorf("insert session %s: %w", session.ID, err)
	}
	return nil
}

// Get answers about live sessions only: a closed session is reachable through
// the ledger and nowhere else, which is what keeps every caller here honest.
func (s *Store) Get(id string) *protocol.Session {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return cloneSession(s.sessions[id])
	}

	var session protocol.Session
	var todosJSON string
	var stateSince, stateUpdatedAt, lastSeen string
	var isWorktree int
	var contextWindowCap int
	var endpointID, workspaceID, branch, mainRepo, repository, pinnedAt, parentSessionID, activity, activityAt, lastModelRequestAt sql.NullString

	err := s.db.QueryRow(`
		SELECT id, label, agent, directory, endpoint_id, workspace_id, branch, is_worktree, main_repo, repository, state, state_since, state_updated_at, last_model_request_at, pinned_at, context_window_cap, parent_session_id, activity, activity_at, todos, last_seen
		FROM sessions WHERE id = ? AND closed_at = ''`, id).Scan(
		&session.ID,
		&session.Label,
		&session.Agent,
		&session.Directory,
		&endpointID,
		&workspaceID,
		&branch,
		&isWorktree,
		&mainRepo,
		&repository,
		&session.State,
		&stateSince,
		&stateUpdatedAt,
		&lastModelRequestAt,
		&pinnedAt,
		&contextWindowCap,
		&parentSessionID,
		&activity,
		&activityAt,
		&todosJSON,
		&lastSeen,
	)
	if err != nil {
		return nil
	}

	if pinnedAt.Valid && pinnedAt.String != "" {
		session.PinnedAt = protocol.Ptr(pinnedAt.String)
	}
	if contextWindowCap > 0 {
		session.ContextWindowCap = protocol.Ptr(contextWindowCap)
	}
	if parentSessionID.Valid && parentSessionID.String != "" {
		session.ParentSessionID = protocol.Ptr(parentSessionID.String)
	}
	applyActivity(&session, activity.String, activityAt.String)

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
	if repository.Valid && repository.String != "" {
		session.Repository = protocol.Ptr(repository.String)
	}
	session.StateSince = stateSince
	session.StateUpdatedAt = stateUpdatedAt
	if lastModelRequestAt.Valid && lastModelRequestAt.String != "" {
		session.LastModelRequestAt = protocol.Ptr(lastModelRequestAt.String)
	}
	session.LastSeen = lastSeen
	if todosJSON != "" && todosJSON != "null" {
		if err := json.Unmarshal([]byte(todosJSON), &session.Todos); err != nil {
			log.Printf("[store] Get: failed to unmarshal todos for session %s: %v", id, err)
		}
	}

	return &session
}

// Rows here exist only for their session and are deleted by hand in every
// session-removal path, because this store runs with foreign keys off.
var sessionOwnedTables = []string{
	"session_annotation_drafts",
	"session_pull_requests",
	"session_exit_screens",
}

func (s *Store) Remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		delete(s.sessions, id)
		delete(s.sessionCloses, id)
		delete(s.turnStamps, id)
		delete(s.agentDriverRuns, id)
		delete(s.agentMetadata, id)
		delete(s.sessionCosts, id)
		return
	}

	_, err := s.db.Exec("DELETE FROM sessions WHERE id = ?", id)
	if err != nil {
		log.Printf("[store] Remove: failed for session %s: %v", id, err)
	}
	for _, table := range sessionOwnedTables {
		if _, err := s.db.Exec("DELETE FROM "+table+" WHERE session_id = ?", id); err != nil {
			log.Printf("[store] Remove: failed to drop %s for session %s: %v", table, id, err)
		}
	}
}

func (s *Store) ClearSessions() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		s.sessions = make(map[string]*protocol.Session)
		s.sessionCloses = make(map[string]sessionCloseMark)
		s.agentDriverRuns = make(map[string]AgentDriverReportCursor)
		s.agentMetadata = make(map[string]string)
		s.sessionCosts = make(map[string]SessionCostState)
		s.workspaces = make(map[string]workspacelayout.WorkspaceLayout)
		return
	}

	if _, err := s.db.Exec("DELETE FROM workspace_layout_panes"); err != nil {
		log.Printf("[store] ClearSessions: failed to clear workspace layout panes: %v", err)
	}
	if _, err := s.db.Exec("DELETE FROM workspace_layouts"); err != nil {
		log.Printf("[store] ClearSessions: failed to clear workspace layouts: %v", err)
	}
	for _, table := range sessionOwnedTables {
		if _, err := s.db.Exec("DELETE FROM " + table); err != nil {
			log.Printf("[store] ClearSessions: failed to clear %s: %v", table, err)
		}
	}
	_, err := s.db.Exec("DELETE FROM sessions")
	if err != nil {
		log.Printf("[store] ClearSessions: failed: %v", err)
	}
}

// List answers about live sessions only, like Get.
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
			SELECT id, label, agent, directory, endpoint_id, workspace_id, branch, is_worktree, main_repo, repository, state, state_since, state_updated_at, last_model_request_at, pinned_at, context_window_cap, parent_session_id, activity, activity_at, todos, last_seen
			FROM sessions WHERE closed_at = '' ORDER BY label, id`)
	} else {
		rows, err = s.db.Query(`
			SELECT id, label, agent, directory, endpoint_id, workspace_id, branch, is_worktree, main_repo, repository, state, state_since, state_updated_at, last_model_request_at, pinned_at, context_window_cap, parent_session_id, activity, activity_at, todos, last_seen
			FROM sessions WHERE state = ? AND closed_at = '' ORDER BY label, id`, stateFilter)
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
		var isWorktree int
		var contextWindowCap int
		var endpointID, workspaceID, branch, mainRepo, repository, pinnedAt, parentSessionID, activity, activityAt, lastModelRequestAt sql.NullString

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
			&repository,
			&session.State,
			&stateSince,
			&stateUpdatedAt,
			&lastModelRequestAt,
			&pinnedAt,
			&contextWindowCap,
			&parentSessionID,
			&activity,
			&activityAt,
			&todosJSON,
			&lastSeen,
		)
		if err != nil {
			continue
		}

		if pinnedAt.Valid && pinnedAt.String != "" {
			session.PinnedAt = protocol.Ptr(pinnedAt.String)
		}
		if contextWindowCap > 0 {
			session.ContextWindowCap = protocol.Ptr(contextWindowCap)
		}
		if parentSessionID.Valid && parentSessionID.String != "" {
			session.ParentSessionID = protocol.Ptr(parentSessionID.String)
		}
		applyActivity(&session, activity.String, activityAt.String)

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
		if repository.Valid && repository.String != "" {
			session.Repository = protocol.Ptr(repository.String)
		}
		session.StateSince = stateSince
		session.StateUpdatedAt = stateUpdatedAt
		if lastModelRequestAt.Valid && lastModelRequestAt.String != "" {
			session.LastModelRequestAt = protocol.Ptr(lastModelRequestAt.String)
		}
		session.LastSeen = lastSeen
		if todosJSON != "" && todosJSON != "null" {
			if err := json.Unmarshal([]byte(todosJSON), &session.Todos); err != nil {
				log.Printf("[store] List: failed to unmarshal todos for session %s: %v", session.ID, err)
			}
		}

		result = append(result, &session)
	}

	return result
}

func (s *Store) HasSessionInDirectory(directory string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		for id, session := range s.sessions {
			if _, closed := s.sessionCloses[id]; closed {
				continue
			}
			if session.Directory == directory && session.State != protocol.SessionStateIdle {
				return true
			}
		}
		return false
	}

	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE directory = ? AND state != ? AND closed_at = ''`, directory, string(protocol.SessionStateIdle)).Scan(&count)
	if err != nil {
		return false
	}
	return count > 0
}

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

	// Foreign keys are off, so owned rows go first: after the sessions are gone
	// there is nothing left to select them by.
	for _, table := range sessionOwnedTables {
		if _, err := s.db.Exec("DELETE FROM "+table+
			" WHERE session_id IN (SELECT id FROM sessions WHERE directory = ?)", directory); err != nil {
			log.Printf("[store] RemoveSessionsInDirectory: failed to drop %s for directory %s: %v", table, directory, err)
		}
	}
	_, err := s.db.Exec(`DELETE FROM sessions WHERE directory = ?`, directory)
	if err != nil {
		log.Printf("[store] RemoveSessionsInDirectory: failed for directory %s: %v", directory, err)
	}
}

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
	result, err := s.db.Exec(`UPDATE sessions SET state = ?, state_since = ?, state_updated_at = ? WHERE id = ? AND closed_at = ''`,
		state, now, now, id)
	if err != nil {
		log.Printf("[store] UpdateState: failed for session %s: %v", id, err)
		return false
	}
	updated, err := result.RowsAffected()
	return err == nil && updated == 1
}

func (s *Store) MarkModelRequestStarted(id string, at time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(id) == "" || at.IsZero() {
		return false
	}
	stamp := string(protocol.NewTimestamp(at))
	if s.db == nil {
		session := s.sessions[id]
		if session == nil {
			return false
		}
		current := protocol.Timestamp(protocol.Deref(session.LastModelRequestAt)).Time()
		if !current.IsZero() && !at.After(current) {
			return false
		}
		session.LastModelRequestAt = protocol.Ptr(stamp)
		return true
	}
	var current sql.NullString
	if err := s.db.QueryRow("SELECT last_model_request_at FROM sessions WHERE id = ?", id).Scan(&current); err != nil {
		return false
	}
	currentAt := protocol.Timestamp(current.String).Time()
	if !currentAt.IsZero() && !at.After(currentAt) {
		return false
	}
	result, err := s.db.Exec("UPDATE sessions SET last_model_request_at = ? WHERE id = ? AND closed_at = ''", stamp, id)
	if err != nil {
		log.Printf("[store] MarkModelRequestStarted: failed for session %s: %v", id, err)
		return false
	}
	updated, _ := result.RowsAffected()
	return updated == 1
}

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
	_, err = s.db.Exec("UPDATE sessions SET todos = ? WHERE id = ? AND closed_at = ''", string(todosJSON), id)
	if err != nil {
		log.Printf("[store] UpdateTodos: failed for session %s: %v", id, err)
	}
}

func (s *Store) UpdateBranch(id, branch string, isWorktree bool, mainRepo, repository string) {
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
			if repository != "" {
				session.Repository = protocol.Ptr(repository)
			} else {
				session.Repository = nil
			}
		}
		return
	}

	_, err := s.db.Exec(`UPDATE sessions SET branch = ?, is_worktree = ?, main_repo = ?, repository = ? WHERE id = ? AND closed_at = ''`,
		branch, boolToInt(isWorktree), mainRepo, repository, id)
	if err != nil {
		log.Printf("[store] UpdateBranch: failed for session %s: %v", id, err)
	}
}

func (s *Store) UpdateSessionLabel(id, label string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		if session := s.sessions[id]; session != nil {
			session.Label = label
		}
		return
	}

	if _, err := s.db.Exec(`UPDATE sessions SET label = ? WHERE id = ? AND closed_at = ''`, label, id); err != nil {
		log.Printf("[store] UpdateSessionLabel: failed for session %s: %v", id, err)
	}
}

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
	_, err := s.db.Exec("UPDATE sessions SET last_seen = ? WHERE id = ? AND closed_at = ''", now, id)
	if err != nil {
		log.Printf("[store] Touch: failed for session %s: %v", id, err)
	}
}

func (s *Store) SetResumeSessionID(id, resumeSessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	resumeSessionID = strings.TrimSpace(resumeSessionID)
	_, err := s.db.Exec(`
		UPDATE sessions
		SET resume_session_id = ?,
			transcript_path = CASE
				WHEN ? != '' AND resume_session_id = ? THEN transcript_path
				ELSE ''
			END
		WHERE id = ? AND closed_at = ''`, resumeSessionID, resumeSessionID, resumeSessionID, id)
	if err != nil {
		log.Printf("[store] SetResumeSessionID: failed for session %s: %v", id, err)
	}
}

func (s *Store) GetResumeSessionID(id string) string {
	return s.GetSessionConversation(id).NativeID
}

func (s *Store) GetSessionTranscriptPath(id string) string {
	return s.GetSessionConversation(id).TranscriptPath
}

func (s *Store) SetLaunchIntent(id string, intent LaunchIntent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	intentJSON, err := json.Marshal(intent)
	if err != nil {
		log.Printf("[store] SetLaunchIntent: failed to marshal launch intent for session %s: %v", id, err)
		return
	}
	if _, err := s.db.Exec("UPDATE sessions SET launch_intent = ? WHERE id = ? AND closed_at = ''", string(intentJSON), id); err != nil {
		log.Printf("[store] SetLaunchIntent: failed for session %s: %v", id, err)
	}
}

func (s *Store) ClearLaunchIntent(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}
	if _, err := s.db.Exec("UPDATE sessions SET launch_intent = '' WHERE id = ? AND closed_at = ''", id); err != nil {
		log.Printf("[store] ClearLaunchIntent: failed for session %s: %v", id, err)
	}
}

func (s *Store) LaunchIntent(id string) (LaunchIntent, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return LaunchIntent{}, false
	}

	var intentJSON string
	if err := s.db.QueryRow("SELECT launch_intent FROM sessions WHERE id = ?", id).Scan(&intentJSON); err != nil {
		return LaunchIntent{}, false
	}
	if strings.TrimSpace(intentJSON) == "" {
		return LaunchIntent{}, false
	}

	var intent LaunchIntent
	if err := json.Unmarshal([]byte(intentJSON), &intent); err != nil {
		log.Printf("[store] LaunchIntent: failed to unmarshal launch intent for session %s: %v", id, err)
		return LaunchIntent{}, false
	}
	return intent, true
}

func (s *Store) MarkSessionIntentionalClose(id string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		if s.teardownIntents == nil {
			s.teardownIntents = make(map[string]SessionTeardownIntent)
		}
		intent := s.teardownIntents[id]
		intent.RequestedAt = now
		s.teardownIntents[id] = intent
		return nil
	}

	_, err := s.db.Exec(`INSERT INTO session_teardown_tombstones (session_id, requested_at)
		VALUES (?, ?) ON CONFLICT(session_id) DO UPDATE SET requested_at = excluded.requested_at`,
		id, now.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("mark session %s intentional close: %w", id, err)
	}
	return nil
}

func (s *Store) SessionCloseIntentionalChecked(id string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		_, ok := s.teardownIntents[id]
		return ok, nil
	}

	var found int
	if err := s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM session_teardown_tombstones WHERE session_id = ?)", id).Scan(&found); err != nil {
		return false, fmt.Errorf("read session %s teardown intent: %w", id, err)
	}
	return found == 1, nil
}

func (s *Store) SessionCloseIntentional(id string) bool {
	intentional, err := s.SessionCloseIntentionalChecked(id)
	if err != nil {
		log.Printf("[store] SessionCloseIntentional: %v", err)
		return true
	}
	return intentional
}

func (s *Store) PrepareSessionTeardown(id string, now time.Time) (AgentDriverReportCursor, error) {
	run, _, err := s.prepareSessionTeardown(id, now, true)
	return run, err
}

func (s *Store) PrepareExistingSessionTeardown(id string, now time.Time) (AgentDriverReportCursor, bool, error) {
	return s.prepareSessionTeardown(id, now, false)
}

func (s *Store) prepareSessionTeardown(id string, now time.Time, create bool) (AgentDriverReportCursor, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		if s.teardownIntents == nil {
			if !create {
				return AgentDriverReportCursor{}, false, nil
			}
			s.teardownIntents = make(map[string]SessionTeardownIntent)
		}
		intent, found := s.teardownIntents[id]
		if !found && !create {
			return AgentDriverReportCursor{}, false, nil
		}
		if intent.DriverRun.RunID == "" {
			intent.DriverRun = s.agentDriverRuns[id]
			delete(s.agentDriverRuns, id)
		}
		intent.RequestedAt = now
		s.teardownIntents[id] = intent
		return intent.DriverRun, true, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return AgentDriverReportCursor{}, false, fmt.Errorf("begin session %s teardown: %w", id, err)
	}
	defer tx.Rollback()

	var run AgentDriverReportCursor
	err = tx.QueryRow(`SELECT driver_plugin_name, driver_run_id, driver_report_seq
		FROM session_teardown_tombstones WHERE session_id = ?`, id).Scan(&run.PluginName, &run.RunID, &run.Seq)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return AgentDriverReportCursor{}, false, fmt.Errorf("read session %s teardown owner: %w", id, err)
	}
	found := err == nil
	if !found && !create {
		return AgentDriverReportCursor{}, false, nil
	}
	if run.RunID == "" {
		err = tx.QueryRow(`SELECT agent_driver_plugin_name, agent_driver_run_id, agent_driver_report_seq
			FROM sessions WHERE id = ?`, id).Scan(&run.PluginName, &run.RunID, &run.Seq)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return AgentDriverReportCursor{}, false, fmt.Errorf("read session %s driver owner: %w", id, err)
		}
	}
	run.PluginName = strings.TrimSpace(run.PluginName)
	run.RunID = strings.TrimSpace(run.RunID)
	if _, err := tx.Exec(`INSERT INTO session_teardown_tombstones
		(session_id, requested_at, driver_plugin_name, driver_run_id, driver_report_seq)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET requested_at = excluded.requested_at,
			driver_plugin_name = CASE WHEN session_teardown_tombstones.driver_run_id = '' THEN excluded.driver_plugin_name ELSE session_teardown_tombstones.driver_plugin_name END,
			driver_run_id = CASE WHEN session_teardown_tombstones.driver_run_id = '' THEN excluded.driver_run_id ELSE session_teardown_tombstones.driver_run_id END,
			driver_report_seq = CASE WHEN session_teardown_tombstones.driver_run_id = '' THEN excluded.driver_report_seq ELSE session_teardown_tombstones.driver_report_seq END`,
		id, now.Format(time.RFC3339Nano), run.PluginName, run.RunID, run.Seq); err != nil {
		return AgentDriverReportCursor{}, false, fmt.Errorf("persist session %s teardown owner: %w", id, err)
	}
	if run.RunID != "" {
		if _, err := tx.Exec(`UPDATE sessions SET agent_driver_plugin_name = '', agent_driver_run_id = '', agent_driver_report_seq = 0
			WHERE id = ? AND closed_at = '' AND agent_driver_plugin_name = ? AND agent_driver_run_id = ?`, id, run.PluginName, run.RunID); err != nil {
			return AgentDriverReportCursor{}, false, fmt.Errorf("claim session %s driver owner: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return AgentDriverReportCursor{}, false, fmt.Errorf("commit session %s teardown: %w", id, err)
	}
	return run, true, nil
}

func (s *Store) SessionTeardownIntentIDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		ids := make([]string, 0, len(s.teardownIntents))
		for id := range s.teardownIntents {
			ids = append(ids, id)
		}
		return ids
	}

	rows, err := s.db.Query("SELECT session_id FROM session_teardown_tombstones")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

func (s *Store) ClaimSessionTeardownDriverRun(id, runID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		intent := s.teardownIntents[id]
		if intent.DriverRun.RunID != runID || runID == "" {
			return false, nil
		}
		intent.DriverRun = AgentDriverReportCursor{}
		s.teardownIntents[id] = intent
		return true, nil
	}

	result, err := s.db.Exec(`UPDATE session_teardown_tombstones
		SET driver_plugin_name = '', driver_run_id = '', driver_report_seq = 0
		WHERE session_id = ? AND driver_run_id = ?`, id, runID)
	if err != nil {
		return false, fmt.Errorf("claim session %s teardown driver run: %w", id, err)
	}
	updated, err := result.RowsAffected()
	return err == nil && updated == 1, err
}

func (s *Store) CancelSessionTeardown(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		intent, found := s.teardownIntents[id]
		if !found {
			return nil
		}
		if intent.DriverRun.RunID != "" {
			s.agentDriverRuns[id] = intent.DriverRun
		}
		delete(s.teardownIntents, id)
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var run AgentDriverReportCursor
	if err := tx.QueryRow(`SELECT driver_plugin_name, driver_run_id, driver_report_seq
		FROM session_teardown_tombstones WHERE session_id = ?`, id).Scan(&run.PluginName, &run.RunID, &run.Seq); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if run.RunID != "" {
		if _, err := tx.Exec(`UPDATE sessions SET agent_driver_plugin_name = ?, agent_driver_run_id = ?, agent_driver_report_seq = ?
			WHERE id = ? AND closed_at = '' AND agent_driver_run_id = ''`, run.PluginName, run.RunID, run.Seq, id); err != nil {
			return err
		}
	}
	if _, err := tx.Exec("DELETE FROM session_teardown_tombstones WHERE session_id = ?", id); err != nil {
		return err
	}
	return tx.Commit()
}

// A live session must not carry the mark, or a later genuine crash would be misread as a clean close.
func (s *Store) ClearSessionIntentionalClose(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		delete(s.teardownIntents, id)
		return
	}

	_, err := s.db.Exec("DELETE FROM session_teardown_tombstones WHERE session_id = ?", id)
	if err != nil {
		log.Printf("[store] ClearSessionIntentionalClose: failed for session %s: %v", id, err)
	}
}

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

// Session lifetime in this store is authoritative during plugin recovery; private plugin state may only reconnect records returned here.
func (s *Store) ListAgentDriverRuns(pluginName string) []ActiveAgentDriverRun {
	s.mu.RLock()
	defer s.mu.RUnlock()

	pluginName = strings.TrimSpace(pluginName)
	if pluginName == "" {
		return nil
	}
	if s.db == nil {
		var runs []ActiveAgentDriverRun
		for sessionID, cursor := range s.agentDriverRuns {
			if cursor.PluginName != pluginName || strings.TrimSpace(cursor.RunID) == "" || s.sessions[sessionID] == nil {
				continue
			}
			runs = append(runs, ActiveAgentDriverRun{
				SessionID: sessionID,
				RunID:     strings.TrimSpace(cursor.RunID),
				Metadata:  strings.TrimSpace(s.agentMetadata[sessionID]),
				Seq:       cursor.Seq,
			})
		}
		sort.Slice(runs, func(i, j int) bool { return runs[i].SessionID < runs[j].SessionID })
		return runs
	}

	rows, err := s.db.Query(`
		SELECT id, agent_driver_run_id, agent_metadata, agent_driver_report_seq
		FROM sessions
		WHERE agent_driver_plugin_name = ? AND agent_driver_run_id <> '' AND closed_at = ''
		ORDER BY id`, pluginName)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var runs []ActiveAgentDriverRun
	for rows.Next() {
		var run ActiveAgentDriverRun
		if err := rows.Scan(&run.SessionID, &run.RunID, &run.Metadata, &run.Seq); err != nil {
			return nil
		}
		run.RunID = strings.TrimSpace(run.RunID)
		run.Metadata = strings.TrimSpace(run.Metadata)
		runs = append(runs, run)
	}
	return runs
}

func (s *Store) ListActiveAgentDriverRuns() []ActiveAgentDriverRun {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		var runs []ActiveAgentDriverRun
		for sessionID, cursor := range s.agentDriverRuns {
			if strings.TrimSpace(cursor.RunID) == "" || s.sessions[sessionID] == nil {
				continue
			}
			runs = append(runs, ActiveAgentDriverRun{
				SessionID:  sessionID,
				RunID:      strings.TrimSpace(cursor.RunID),
				Metadata:   strings.TrimSpace(s.agentMetadata[sessionID]),
				Seq:        cursor.Seq,
				PluginName: strings.TrimSpace(cursor.PluginName),
			})
		}
		sort.Slice(runs, func(i, j int) bool { return runs[i].SessionID < runs[j].SessionID })
		return runs
	}

	rows, err := s.db.Query(`
		SELECT id, agent_driver_run_id, agent_metadata, agent_driver_report_seq, agent_driver_plugin_name
		FROM sessions
		WHERE agent_driver_run_id <> '' AND closed_at = ''
		ORDER BY id`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var runs []ActiveAgentDriverRun
	for rows.Next() {
		var run ActiveAgentDriverRun
		if err := rows.Scan(&run.SessionID, &run.RunID, &run.Metadata, &run.Seq, &run.PluginName); err != nil {
			return nil
		}
		run.RunID = strings.TrimSpace(run.RunID)
		run.Metadata = strings.TrimSpace(run.Metadata)
		run.PluginName = strings.TrimSpace(run.PluginName)
		runs = append(runs, run)
	}
	return runs
}

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
		"UPDATE sessions SET agent_driver_plugin_name = ?, agent_driver_run_id = ?, agent_driver_report_seq = 0 WHERE id = ? AND closed_at = ''",
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

func (s *Store) EndAgentDriverRun(id string) AgentDriverReportCursor {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		cursor := s.agentDriverRuns[id]
		if cursor.RunID == "" || !s.sessionIsLiveLocked(id) {
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
		"UPDATE sessions SET agent_driver_plugin_name = '', agent_driver_run_id = '', agent_driver_report_seq = 0 WHERE id = ? AND closed_at = '' AND agent_driver_plugin_name = ? AND agent_driver_run_id = ?",
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

func (s *Store) ApplyAgentDriverState(id, runID string, seq uint64, state string, requestStartedAt time.Time) bool {
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
		current := protocol.Timestamp(protocol.Deref(session.LastModelRequestAt)).Time()
		if !requestStartedAt.IsZero() && (current.IsZero() || requestStartedAt.After(current)) {
			session.LastModelRequestAt = protocol.Ptr(string(protocol.NewTimestamp(requestStartedAt)))
		}
		return true
	}
	requestStamp := ""
	if !requestStartedAt.IsZero() {
		var current sql.NullString
		if err := s.db.QueryRow("SELECT last_model_request_at FROM sessions WHERE id = ?", id).Scan(&current); err != nil {
			return false
		}
		currentAt := protocol.Timestamp(current.String).Time()
		if currentAt.IsZero() || requestStartedAt.After(currentAt) {
			requestStamp = string(protocol.NewTimestamp(requestStartedAt))
		}
	}
	result, err := s.db.Exec(`
		UPDATE sessions
		SET state = ?, state_since = ?, state_updated_at = ?, agent_driver_report_seq = ?,
			last_model_request_at = COALESCE(NULLIF(?, ''), last_model_request_at)
		WHERE id = ? AND closed_at = '' AND agent_driver_run_id = ? AND agent_driver_report_seq < ?`,
		state,
		now,
		now,
		seq,
		requestStamp,
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
		WHERE id = ? AND closed_at = '' AND agent_driver_run_id = ? AND agent_driver_report_seq < ?`,
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

func (s *Store) SetPRs(prs []*protocol.PR) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

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

	s.execLog("DELETE FROM prs")

	for _, pr := range prs {
		normalizePRIdentity(pr)
		if ex, ok := existing[pr.ID]; ok {
			pr.Muted = ex.Muted
			pr.ApprovedByMe = ex.ApprovedByMe
			if pr.Host == "" {
				pr.Host = ex.Host
			}
			if ex.DetailsFetched {
				pr.DetailsFetched = ex.DetailsFetched
				pr.DetailsFetchedAt = ex.DetailsFetchedAt
				pr.Mergeable = ex.Mergeable
				pr.MergeableState = ex.MergeableState
				pr.CIStatus = ex.CIStatus
				pr.ReviewStatus = ex.ReviewStatus
			}
			if protocol.Deref(pr.HeadSHA) == "" {
				pr.HeadSHA = ex.HeadSHA
			}
			if protocol.Deref(pr.HeadBranch) == "" {
				pr.HeadBranch = ex.HeadBranch
			}
			if pr.HeatState == nil || *pr.HeatState == protocol.HeatStateCold {
				pr.HeatState = ex.HeatState
				pr.LastHeatActivityAt = ex.LastHeatActivityAt
			}
		}

		if inter, ok := interactions[pr.ID]; ok {
			headSHA := protocol.Deref(pr.HeadSHA)
			if headSHA != "" && inter.lastSeenSHA != "" && headSHA != inter.lastSeenSHA {
				pr.HasNewChanges = true
			}
			if protocol.Deref(pr.CommentCount) > inter.lastSeenCommentCount {
				pr.HasNewChanges = true
			}
			ciStatus := protocol.Deref(pr.CIStatus)
			if (pr.Role == protocol.PRRoleAuthor || pr.ApprovedByMe) && ciStatus != "" {
				if inter.lastSeenCIStatus == "pending" && (ciStatus == "success" || ciStatus == "failure") {
					pr.HasNewChanges = true
				}
			}
		}

		var mergeableVal *int
		if pr.Mergeable != nil {
			v := boolToInt(*pr.Mergeable)
			mergeableVal = &v
		}

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

func (s *Store) GetPR(id string) *protocol.PR {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil
	}

	row := s.db.QueryRow(`SELECT id, host, repo, number, title, url, author, role, state, reason, last_updated, last_polled, muted, details_fetched, details_fetched_at, mergeable, mergeable_state, ci_status, review_status, head_sha, head_branch, comment_count, approved_by_me, heat_state, last_heat_activity_at FROM prs WHERE id = ?`, id)
	return scanPRRow(row)
}

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

func (s *Store) ToggleMuteRepo(repo string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	s.execLog("INSERT OR IGNORE INTO repos (repo, muted, collapsed) VALUES (?, 0, 0)", repo)
	s.execLog("UPDATE repos SET muted = NOT muted WHERE repo = ?", repo)
}

func (s *Store) SetRepoCollapsed(repo string, collapsed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	s.execLog("INSERT OR IGNORE INTO repos (repo, muted, collapsed) VALUES (?, 0, 0)", repo)
	s.execLog("UPDATE repos SET collapsed = ? WHERE repo = ?", boolToInt(collapsed), repo)
}

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

func (s *Store) ToggleMuteAuthor(author string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	s.execLog("INSERT OR IGNORE INTO authors (author, muted) VALUES (?, 0)", author)
	s.execLog("UPDATE authors SET muted = NOT muted WHERE author = ?", author)
}

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

func (s *Store) MarkPRVisited(prID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	var headSHA, ciStatus sql.NullString
	var commentCount int
	err := s.db.QueryRow("SELECT head_sha, comment_count, ci_status FROM prs WHERE id = ?", prID).Scan(&headSHA, &commentCount, &ciStatus)
	if err != nil {
		return
	}

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

func (s *Store) MarkPRApproved(prID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	var headSHA, ciStatus sql.NullString
	var commentCount int
	err := s.db.QueryRow("SELECT head_sha, comment_count, ci_status FROM prs WHERE id = ?", prID).Scan(&headSHA, &commentCount, &ciStatus)
	if err != nil {
		return
	}

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

	s.execLog("UPDATE prs SET approved_by_me = 1 WHERE id = ?", prID)
}

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

func (s *Store) DecayHeatStates() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	now := time.Now()
	warmThreshold := now.Add(-protocol.HeatHotDuration).Format(time.RFC3339)
	coldThreshold := now.Add(-protocol.HeatWarmDuration).Format(time.RFC3339)

	s.execLog(`UPDATE prs SET heat_state = ? WHERE heat_state = ? AND last_heat_activity_at < ?`,
		protocol.HeatStateWarm, protocol.HeatStateHot, warmThreshold)

	s.execLog(`UPDATE prs SET heat_state = ? WHERE heat_state = ? AND last_heat_activity_at < ?`,
		protocol.HeatStateCold, protocol.HeatStateWarm, coldThreshold)
}

func (s *Store) GetPRsNeedingDetailRefresh() []*protocol.PR {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil
	}

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

		if mutedRepos[pr.Repo] {
			continue
		}

		detailsFetchedAt := protocol.Timestamp(protocol.Deref(pr.DetailsFetchedAt)).Time()
		elapsed := now.Sub(detailsFetchedAt)
		needsRefresh := false

		heatState := protocol.DerefOr(pr.HeatState, protocol.HeatStateCold)
		switch heatState {
		case protocol.HeatStateHot:
			needsRefresh = elapsed > protocol.HeatHotInterval
		case protocol.HeatStateWarm:
			needsRefresh = elapsed > protocol.HeatWarmInterval
		default:
			needsRefresh = elapsed > protocol.HeatColdInterval
		}

		if !pr.DetailsFetched {
			needsRefresh = true
		}

		if needsRefresh {
			result = append(result, pr)
		}
	}

	return result
}

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

func (s *Store) SetSetting(key, value string) {
	if err := s.SetSettingChecked(key, value); err != nil {
		log.Printf("[store] SetSetting: %v", err)
	}
}

func (s *Store) SetSettingChecked(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return errors.New("settings database is unavailable")
	}

	_, err := s.db.Exec(`
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value)
	if err != nil {
		return fmt.Errorf("set setting %q: %w", key, err)
	}
	return nil
}

func (s *Store) DeleteSetting(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	s.execLog(`DELETE FROM settings WHERE key = ?`, key)
}

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
		// Pre-truncating here (e.g. by last_seen) would hide old-but-frequent locations.
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

func (s *Store) RemoveRecentLocation(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		delete(s.recentLocations, path)
		return
	}

	s.execLog("DELETE FROM recent_locations WHERE path = ?", path)
}

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
