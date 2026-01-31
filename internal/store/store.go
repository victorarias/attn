package store

import (
	"database/sql"
	"encoding/json"
	"log"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
)

// Store manages session state in SQLite
type Store struct {
	mu sync.RWMutex
	db *sql.DB
}

// New creates a new in-memory store (backed by SQLite :memory:)
func New() *Store {
	db, err := OpenDB(":memory:")
	if err != nil {
		return &Store{}
	}
	return &Store{db: db}
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

// Add adds a session to the store
func (s *Store) Add(session *protocol.Session) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	todosJSON, err := json.Marshal(session.Todos)
	if err != nil {
		log.Printf("[store] Add: failed to marshal todos for session %s: %v", session.ID, err)
	}
	_, err = s.db.Exec(`
		INSERT OR REPLACE INTO sessions
		(id, label, directory, branch, is_worktree, main_repo, state, state_since, state_updated_at, todos, last_seen, muted)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		session.ID,
		session.Label,
		session.Directory,
		protocol.Deref(session.Branch),
		boolToInt(protocol.Deref(session.IsWorktree)),
		protocol.Deref(session.MainRepo),
		string(session.State),
		session.StateSince,
		session.StateUpdatedAt,
		string(todosJSON),
		session.LastSeen,
		boolToInt(session.Muted),
	)
	if err != nil {
		log.Printf("[store] Add: failed to insert session %s: %v", session.ID, err)
	}
}

// Get retrieves a session by ID
func (s *Store) Get(id string) *protocol.Session {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil
	}

	var session protocol.Session
	var todosJSON string
	var stateSince, stateUpdatedAt, lastSeen string
	var muted, isWorktree int
	var branch, mainRepo sql.NullString

	err := s.db.QueryRow(`
		SELECT id, label, directory, branch, is_worktree, main_repo, state, state_since, state_updated_at, todos, last_seen, muted
		FROM sessions WHERE id = ?`, id).Scan(
		&session.ID,
		&session.Label,
		&session.Directory,
		&branch,
		&isWorktree,
		&mainRepo,
		&session.State,
		&stateSince,
		&stateUpdatedAt,
		&todosJSON,
		&lastSeen,
		&muted,
	)
	if err != nil {
		return nil
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
	session.Muted = muted == 1
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
		return
	}

	_, err := s.db.Exec("DELETE FROM sessions")
	if err != nil {
		log.Printf("[store] ClearSessions: failed: %v", err)
	}
}

// List returns sessions, optionally filtered by state, sorted by label
func (s *Store) List(stateFilter string) []*protocol.Session {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil
	}

	var rows *sql.Rows
	var err error

	if stateFilter == "" {
		rows, err = s.db.Query(`
			SELECT id, label, directory, branch, is_worktree, main_repo, state, state_since, state_updated_at, todos, last_seen, muted
			FROM sessions ORDER BY label`)
	} else {
		rows, err = s.db.Query(`
			SELECT id, label, directory, branch, is_worktree, main_repo, state, state_since, state_updated_at, todos, last_seen, muted
			FROM sessions WHERE state = ? ORDER BY label`, stateFilter)
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
		var muted, isWorktree int
		var branch, mainRepo sql.NullString

		err := rows.Scan(
			&session.ID,
			&session.Label,
			&session.Directory,
			&branch,
			&isWorktree,
			&mainRepo,
			&session.State,
			&stateSince,
			&stateUpdatedAt,
			&todosJSON,
			&lastSeen,
			&muted,
		)
		if err != nil {
			continue
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
		session.Muted = muted == 1
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
		return
	}

	_, err := s.db.Exec(`DELETE FROM sessions WHERE directory = ?`, directory)
	if err != nil {
		log.Printf("[store] RemoveSessionsInDirectory: failed for directory %s: %v", directory, err)
	}
}

// UpdateState updates a session's state
func (s *Store) UpdateState(id, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	now := time.Now().Format(time.RFC3339)
	_, err := s.db.Exec(`UPDATE sessions SET state = ?, state_since = ?, state_updated_at = ? WHERE id = ?`,
		state, now, now, id)
	if err != nil {
		log.Printf("[store] UpdateState: failed for session %s: %v", id, err)
	}
}

// UpdateStateWithTimestamp updates a session's state only if the provided timestamp
// is newer than the current StateUpdatedAt. Returns true if updated, false if rejected.
func (s *Store) UpdateStateWithTimestamp(id, state string, updatedAt time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return false
	}

	// Get current state_updated_at
	var currentUpdatedAt string
	err := s.db.QueryRow("SELECT state_updated_at FROM sessions WHERE id = ?", id).Scan(&currentUpdatedAt)
	if err != nil {
		return false
	}

	current, err := time.Parse(time.RFC3339, currentUpdatedAt)
	if err != nil {
		log.Printf("[store] UpdateStateWithTimestamp: failed to parse timestamp for session %s: %v", id, err)
		// If we can't parse current timestamp, accept the update to avoid stuck state
		current = time.Time{}
	}
	if !updatedAt.After(current) {
		return false
	}

	ts := updatedAt.Format(time.RFC3339)
	_, err = s.db.Exec(`UPDATE sessions SET state = ?, state_since = ?, state_updated_at = ? WHERE id = ?`,
		state, ts, ts, id)
	return err == nil
}

// UpdateTodos updates a session's todo list
func (s *Store) UpdateTodos(id string, todos []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
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
		return
	}

	_, err := s.db.Exec(`UPDATE sessions SET branch = ?, is_worktree = ?, main_repo = ? WHERE id = ?`,
		branch, boolToInt(isWorktree), mainRepo, id)
	if err != nil {
		log.Printf("[store] UpdateBranch: failed for session %s: %v", id, err)
	}
}

// Touch updates a session's last seen time
func (s *Store) Touch(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	now := time.Now().Format(time.RFC3339)
	_, err := s.db.Exec("UPDATE sessions SET last_seen = ? WHERE id = ?", now, id)
	if err != nil {
		log.Printf("[store] Touch: failed for session %s: %v", id, err)
	}
}

// ToggleMute toggles a session's muted state
func (s *Store) ToggleMute(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	_, err := s.db.Exec("UPDATE sessions SET muted = NOT muted WHERE id = ?", id)
	if err != nil {
		log.Printf("[store] ToggleMute: failed for session %s: %v", id, err)
	}
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
	rows, err := s.db.Query(`SELECT id, muted, details_fetched, details_fetched_at, mergeable, mergeable_state, ci_status, review_status, head_sha, head_branch, comment_count, approved_by_me, heat_state, last_heat_activity_at FROM prs`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var pr protocol.PR
			var muted, detailsFetched, approvedByMe int
			var detailsFetchedAt, mergeableState, ciStatus, reviewStatus, headSHA, headBranch sql.NullString
			var heatState, lastHeatActivityAt sql.NullString
			var mergeable sql.NullInt64
			var commentCount int

			if err := rows.Scan(&pr.ID, &muted, &detailsFetched, &detailsFetchedAt, &mergeable, &mergeableState, &ciStatus, &reviewStatus, &headSHA, &headBranch, &commentCount, &approvedByMe, &heatState, &lastHeatActivityAt); err != nil {
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
		// Preserve state from existing
		if ex, ok := existing[pr.ID]; ok {
			pr.Muted = ex.Muted
			pr.ApprovedByMe = ex.ApprovedByMe // Always preserve approval state
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
			INSERT INTO prs (id, repo, number, title, url, author, role, state, reason, last_updated, last_polled, muted, details_fetched, details_fetched_at, mergeable, mergeable_state, ci_status, review_status, head_sha, head_branch, comment_count, approved_by_me, heat_state, last_heat_activity_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			pr.ID, pr.Repo, pr.Number, pr.Title, pr.URL, pr.Author, string(pr.Role), pr.State, pr.Reason,
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

	s.execLog(`
		INSERT OR REPLACE INTO prs (id, repo, number, title, url, author, role, state, reason, last_updated, last_polled, muted, details_fetched, details_fetched_at, mergeable, mergeable_state, ci_status, review_status, head_sha, head_branch, comment_count, approved_by_me, heat_state, last_heat_activity_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		pr.ID, pr.Repo, pr.Number, pr.Title, pr.URL, pr.Author, string(pr.Role), pr.State, pr.Reason,
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
		rows, err = s.db.Query(`SELECT id, repo, number, title, url, author, role, state, reason, last_updated, last_polled, muted, details_fetched, details_fetched_at, mergeable, mergeable_state, ci_status, review_status, head_sha, head_branch, comment_count, approved_by_me, heat_state, last_heat_activity_at FROM prs`)
	} else {
		rows, err = s.db.Query(`SELECT id, repo, number, title, url, author, role, state, reason, last_updated, last_polled, muted, details_fetched, details_fetched_at, mergeable, mergeable_state, ci_status, review_status, head_sha, head_branch, comment_count, approved_by_me, heat_state, last_heat_activity_at FROM prs WHERE state = ?`, stateFilter)
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

	row := s.db.QueryRow(`SELECT id, repo, number, title, url, author, role, state, reason, last_updated, last_polled, muted, details_fetched, details_fetched_at, mergeable, mergeable_state, ci_status, review_status, head_sha, head_branch, comment_count, approved_by_me, heat_state, last_heat_activity_at FROM prs WHERE id = ?`, id)
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

	rows, err := s.db.Query(`SELECT id, repo, number, title, url, author, role, state, reason, last_updated, last_polled, muted, details_fetched, details_fetched_at, mergeable, mergeable_state, ci_status, review_status, head_sha, head_branch, comment_count, approved_by_me, heat_state, last_heat_activity_at FROM prs WHERE repo = ?`, repo)
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

// GetAuthorState returns the state for a PR author, or nil if not set
func (s *Store) GetAuthorState(author string) *protocol.AuthorState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil
	}

	var state protocol.AuthorState
	var muted int

	err := s.db.QueryRow("SELECT author, muted FROM authors WHERE author = ?", author).Scan(
		&state.Author, &muted,
	)
	if err != nil {
		return nil
	}

	state.Muted = muted == 1
	return &state
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
		SELECT id, repo, number, title, url, author, role, state, reason, last_updated, last_polled,
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

// Recent Locations methods

// UpsertRecentLocation adds or updates a location in the recent locations table
func (s *Store) UpsertRecentLocation(path, label string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	now := time.Now().Format(time.RFC3339)
	s.execLog(`
		INSERT INTO recent_locations (path, label, last_seen, use_count)
		VALUES (?, ?, ?, 1)
		ON CONFLICT(path) DO UPDATE SET
			label = excluded.label,
			last_seen = excluded.last_seen,
			use_count = use_count + 1`,
		path, label, now)
}

// GetRecentLocations returns recent locations that still exist on disk, sorted by last_seen DESC
func (s *Store) GetRecentLocations(limit int) []*protocol.RecentLocation {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil
	}

	if limit <= 0 {
		limit = 20
	}

	rows, err := s.db.Query(`
		SELECT path, label, last_seen, use_count
		FROM recent_locations
		ORDER BY last_seen DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var result []*protocol.RecentLocation
	var toDelete []string

	for rows.Next() {
		var loc protocol.RecentLocation
		if err := rows.Scan(&loc.Path, &loc.Label, &loc.LastSeen, &loc.UseCount); err != nil {
			continue
		}

		// Validate path still exists
		if _, err := os.Stat(loc.Path); os.IsNotExist(err) {
			toDelete = append(toDelete, loc.Path)
			continue
		}

		result = append(result, &loc)
	}

	// Clean up non-existent paths (async, don't block the read)
	if len(toDelete) > 0 {
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
		return 0
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

func nullString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func nullPtrString(s *string) interface{} {
	if s == nil || *s == "" {
		return nil
	}
	return *s
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
		&pr.ID, &pr.Repo, &pr.Number, &pr.Title, &pr.URL, &pr.Author, &pr.Role, &pr.State, &pr.Reason,
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
		&pr.ID, &pr.Repo, &pr.Number, &pr.Title, &pr.URL, &pr.Author, &pr.Role, &pr.State, &pr.Reason,
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

	return &pr
}
