package store

import (
	"database/sql"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/victorarias/claude-manager/internal/config"
	"github.com/victorarias/claude-manager/internal/protocol"
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

	todosJSON, _ := json.Marshal(session.Todos)
	_, _ = s.db.Exec(`
		INSERT OR REPLACE INTO sessions
		(id, label, directory, state, state_since, state_updated_at, todos, last_seen, muted)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		session.ID,
		session.Label,
		session.Directory,
		session.State,
		session.StateSince.Format(time.RFC3339),
		session.StateUpdatedAt.Format(time.RFC3339),
		string(todosJSON),
		session.LastSeen.Format(time.RFC3339),
		boolToInt(session.Muted),
	)
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
	var muted int

	err := s.db.QueryRow(`
		SELECT id, label, directory, state, state_since, state_updated_at, todos, last_seen, muted
		FROM sessions WHERE id = ?`, id).Scan(
		&session.ID,
		&session.Label,
		&session.Directory,
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

	session.StateSince, _ = time.Parse(time.RFC3339, stateSince)
	session.StateUpdatedAt, _ = time.Parse(time.RFC3339, stateUpdatedAt)
	session.LastSeen, _ = time.Parse(time.RFC3339, lastSeen)
	session.Muted = muted == 1
	if todosJSON != "" && todosJSON != "null" {
		json.Unmarshal([]byte(todosJSON), &session.Todos)
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

	s.db.Exec("DELETE FROM sessions WHERE id = ?", id)
}

// ClearSessions removes all sessions from the store
func (s *Store) ClearSessions() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	s.db.Exec("DELETE FROM sessions")
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
			SELECT id, label, directory, state, state_since, state_updated_at, todos, last_seen, muted
			FROM sessions ORDER BY label`)
	} else {
		rows, err = s.db.Query(`
			SELECT id, label, directory, state, state_since, state_updated_at, todos, last_seen, muted
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
		var muted int

		err := rows.Scan(
			&session.ID,
			&session.Label,
			&session.Directory,
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

		session.StateSince, _ = time.Parse(time.RFC3339, stateSince)
		session.StateUpdatedAt, _ = time.Parse(time.RFC3339, stateUpdatedAt)
		session.LastSeen, _ = time.Parse(time.RFC3339, lastSeen)
		session.Muted = muted == 1
		if todosJSON != "" && todosJSON != "null" {
			json.Unmarshal([]byte(todosJSON), &session.Todos)
		}

		result = append(result, &session)
	}

	return result
}

// UpdateState updates a session's state
func (s *Store) UpdateState(id, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	now := time.Now().Format(time.RFC3339)
	s.db.Exec(`UPDATE sessions SET state = ?, state_since = ?, state_updated_at = ? WHERE id = ?`,
		state, now, now, id)
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

	current, _ := time.Parse(time.RFC3339, currentUpdatedAt)
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

	todosJSON, _ := json.Marshal(todos)
	s.db.Exec("UPDATE sessions SET todos = ? WHERE id = ?", string(todosJSON), id)
}

// Touch updates a session's last seen time
func (s *Store) Touch(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	now := time.Now().Format(time.RFC3339)
	s.db.Exec("UPDATE sessions SET last_seen = ? WHERE id = ?", now, id)
}

// ToggleMute toggles a session's muted state
func (s *Store) ToggleMute(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	s.db.Exec("UPDATE sessions SET muted = NOT muted WHERE id = ?", id)
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
	rows, err := s.db.Query(`SELECT id, muted, details_fetched, details_fetched_at, mergeable, mergeable_state, ci_status, review_status, head_sha, comment_count, approved_by_me, heat_state, last_heat_activity_at FROM prs`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var pr protocol.PR
			var muted, detailsFetched, approvedByMe int
			var detailsFetchedAt, mergeableState, ciStatus, reviewStatus, headSHA sql.NullString
			var heatState, lastHeatActivityAt sql.NullString
			var mergeable sql.NullInt64
			var commentCount int

			rows.Scan(&pr.ID, &muted, &detailsFetched, &detailsFetchedAt, &mergeable, &mergeableState, &ciStatus, &reviewStatus, &headSHA, &commentCount, &approvedByMe, &heatState, &lastHeatActivityAt)
			pr.Muted = muted == 1
			pr.DetailsFetched = detailsFetched == 1
			if detailsFetchedAt.Valid {
				pr.DetailsFetchedAt, _ = time.Parse(time.RFC3339, detailsFetchedAt.String)
			}
			if mergeable.Valid {
				m := mergeable.Int64 == 1
				pr.Mergeable = &m
			}
			pr.MergeableState = mergeableState.String
			pr.CIStatus = ciStatus.String
			pr.ReviewStatus = reviewStatus.String
			pr.HeadSHA = headSHA.String
			pr.CommentCount = commentCount
			pr.ApprovedByMe = approvedByMe == 1
			pr.HeatState = heatState.String
			if pr.HeatState == "" {
				pr.HeatState = protocol.HeatStateCold
			}
			if lastHeatActivityAt.Valid {
				pr.LastHeatActivityAt, _ = time.Parse(time.RFC3339, lastHeatActivityAt.String)
			}
			existing[pr.ID] = &pr
		}
	}

	// Get interaction data for HasNewChanges computation
	interactions := make(map[string]struct {
		lastSeenSHA          string
		lastSeenCommentCount int
	})
	interRows, err := s.db.Query(`SELECT pr_id, last_seen_sha, last_seen_comment_count FROM pr_interactions`)
	if err == nil {
		defer interRows.Close()
		for interRows.Next() {
			var prID string
			var lastSHA sql.NullString
			var lastComments sql.NullInt64
			interRows.Scan(&prID, &lastSHA, &lastComments)
			interactions[prID] = struct {
				lastSeenSHA          string
				lastSeenCommentCount int
			}{
				lastSeenSHA:          lastSHA.String,
				lastSeenCommentCount: int(lastComments.Int64),
			}
		}
	}

	// Delete all PRs and re-insert
	s.db.Exec("DELETE FROM prs")

	for _, pr := range prs {
		// Preserve state from existing
		if ex, ok := existing[pr.ID]; ok {
			pr.Muted = ex.Muted
			if ex.DetailsFetched && !ex.DetailsFetchedAt.Before(pr.LastUpdated) {
				pr.DetailsFetched = ex.DetailsFetched
				pr.DetailsFetchedAt = ex.DetailsFetchedAt
				pr.Mergeable = ex.Mergeable
				pr.MergeableState = ex.MergeableState
				pr.CIStatus = ex.CIStatus
				pr.ReviewStatus = ex.ReviewStatus
			}
			// Preserve HeadSHA from existing if not set
			if pr.HeadSHA == "" {
				pr.HeadSHA = ex.HeadSHA
			}
			// Preserve heat state
			if pr.HeatState == "" || pr.HeatState == protocol.HeatStateCold {
				pr.HeatState = ex.HeatState
				pr.LastHeatActivityAt = ex.LastHeatActivityAt
			}
		}

		// Compute HasNewChanges based on interaction tracking
		if inter, ok := interactions[pr.ID]; ok {
			// PR has been visited before - check for changes
			if pr.HeadSHA != "" && inter.lastSeenSHA != "" && pr.HeadSHA != inter.lastSeenSHA {
				pr.HasNewChanges = true
			}
			if pr.CommentCount > inter.lastSeenCommentCount {
				pr.HasNewChanges = true
			}
		}
		// If no interaction record, HasNewChanges stays false (first time seeing this PR)

		var mergeableVal *int
		if pr.Mergeable != nil {
			v := boolToInt(*pr.Mergeable)
			mergeableVal = &v
		}

		// Ensure heat_state has a default value (NOT NULL column)
		heatState := pr.HeatState
		if heatState == "" {
			heatState = protocol.HeatStateCold
		}

		s.db.Exec(`
			INSERT INTO prs (id, repo, number, title, url, role, state, reason, last_updated, last_polled, muted, details_fetched, details_fetched_at, mergeable, mergeable_state, ci_status, review_status, head_sha, comment_count, approved_by_me, heat_state, last_heat_activity_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			pr.ID, pr.Repo, pr.Number, pr.Title, pr.URL, pr.Role, pr.State, pr.Reason,
			pr.LastUpdated.Format(time.RFC3339), pr.LastPolled.Format(time.RFC3339),
			boolToInt(pr.Muted), boolToInt(pr.DetailsFetched), nullTimeString(pr.DetailsFetchedAt),
			mergeableVal, nullString(pr.MergeableState), nullString(pr.CIStatus), nullString(pr.ReviewStatus),
			nullString(pr.HeadSHA), pr.CommentCount, boolToInt(pr.ApprovedByMe),
			heatState, nullTimeString(pr.LastHeatActivityAt),
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
	heatState := pr.HeatState
	if heatState == "" {
		heatState = protocol.HeatStateCold
	}

	s.db.Exec(`
		INSERT OR REPLACE INTO prs (id, repo, number, title, url, role, state, reason, last_updated, last_polled, muted, details_fetched, details_fetched_at, mergeable, mergeable_state, ci_status, review_status, head_sha, comment_count, approved_by_me, heat_state, last_heat_activity_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		pr.ID, pr.Repo, pr.Number, pr.Title, pr.URL, pr.Role, pr.State, pr.Reason,
		pr.LastUpdated.Format(time.RFC3339), pr.LastPolled.Format(time.RFC3339),
		boolToInt(pr.Muted), boolToInt(pr.DetailsFetched), nullTimeString(pr.DetailsFetchedAt),
		mergeableVal, nullString(pr.MergeableState), nullString(pr.CIStatus), nullString(pr.ReviewStatus),
		nullString(pr.HeadSHA), pr.CommentCount, boolToInt(pr.ApprovedByMe),
		heatState, nullTimeString(pr.LastHeatActivityAt),
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
		rows, err = s.db.Query(`SELECT id, repo, number, title, url, role, state, reason, last_updated, last_polled, muted, details_fetched, details_fetched_at, mergeable, mergeable_state, ci_status, review_status, head_sha, comment_count, approved_by_me, heat_state, last_heat_activity_at FROM prs`)
	} else {
		rows, err = s.db.Query(`SELECT id, repo, number, title, url, role, state, reason, last_updated, last_polled, muted, details_fetched, details_fetched_at, mergeable, mergeable_state, ci_status, review_status, head_sha, comment_count, approved_by_me, heat_state, last_heat_activity_at FROM prs WHERE state = ?`, stateFilter)
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

	s.db.Exec("UPDATE prs SET muted = NOT muted WHERE id = ?", id)
}

// GetPR returns a PR by ID
func (s *Store) GetPR(id string) *protocol.PR {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil
	}

	row := s.db.QueryRow(`SELECT id, repo, number, title, url, role, state, reason, last_updated, last_polled, muted, details_fetched, details_fetched_at, mergeable, mergeable_state, ci_status, review_status, head_sha, comment_count, approved_by_me, heat_state, last_heat_activity_at FROM prs WHERE id = ?`, id)
	return scanPRRow(row)
}

// UpdatePRDetails updates the detail fields for a PR
func (s *Store) UpdatePRDetails(id string, mergeable *bool, mergeableState, ciStatus, reviewStatus, headSHA string) {
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
	s.db.Exec(`UPDATE prs SET details_fetched = 1, details_fetched_at = ?, mergeable = ?, mergeable_state = ?, ci_status = ?, review_status = ?, head_sha = ? WHERE id = ?`,
		now, mergeableVal, mergeableState, ciStatus, reviewStatus, headSHA, id)
}

// ListPRsByRepo returns all PRs for a specific repo
func (s *Store) ListPRsByRepo(repo string) []*protocol.PR {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil
	}

	rows, err := s.db.Query(`SELECT id, repo, number, title, url, role, state, reason, last_updated, last_polled, muted, details_fetched, details_fetched_at, mergeable, mergeable_state, ci_status, review_status, head_sha, comment_count, approved_by_me, heat_state, last_heat_activity_at FROM prs WHERE repo = ?`, repo)
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
	s.db.Exec("INSERT OR IGNORE INTO repos (repo, muted, collapsed) VALUES (?, 0, 0)", repo)
	s.db.Exec("UPDATE repos SET muted = NOT muted WHERE repo = ?", repo)
}

// SetRepoCollapsed sets a repo's collapsed state
func (s *Store) SetRepoCollapsed(repo string, collapsed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	s.db.Exec("INSERT OR IGNORE INTO repos (repo, muted, collapsed) VALUES (?, 0, 0)", repo)
	s.db.Exec("UPDATE repos SET collapsed = ? WHERE repo = ?", boolToInt(collapsed), repo)
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

// MarkPRVisited marks a PR as visited by the user, updating the interaction record
// and clearing HasNewChanges for subsequent polls
func (s *Store) MarkPRVisited(prID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	// Get current PR state
	var headSHA sql.NullString
	var commentCount int
	err := s.db.QueryRow("SELECT head_sha, comment_count FROM prs WHERE id = ?", prID).Scan(&headSHA, &commentCount)
	if err != nil {
		return
	}

	// Upsert interaction record
	now := time.Now().Format(time.RFC3339)
	s.db.Exec(`
		INSERT INTO pr_interactions (pr_id, last_visited_at, last_seen_sha, last_seen_comment_count)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(pr_id) DO UPDATE SET
			last_visited_at = excluded.last_visited_at,
			last_seen_sha = excluded.last_seen_sha,
			last_seen_comment_count = excluded.last_seen_comment_count`,
		prID, now, headSHA.String, commentCount,
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
	var headSHA sql.NullString
	var commentCount int
	err := s.db.QueryRow("SELECT head_sha, comment_count FROM prs WHERE id = ?", prID).Scan(&headSHA, &commentCount)
	if err != nil {
		return
	}

	// Upsert interaction record with approval timestamp
	now := time.Now().Format(time.RFC3339)
	s.db.Exec(`
		INSERT INTO pr_interactions (pr_id, last_visited_at, last_approved_at, last_seen_sha, last_seen_comment_count)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(pr_id) DO UPDATE SET
			last_visited_at = excluded.last_visited_at,
			last_approved_at = excluded.last_approved_at,
			last_seen_sha = excluded.last_seen_sha,
			last_seen_comment_count = excluded.last_seen_comment_count`,
		prID, now, now, headSHA.String, commentCount,
	)

	// Also update the PR's approved_by_me flag
	s.db.Exec("UPDATE prs SET approved_by_me = 1 WHERE id = ?", prID)
}

// SetPRHot sets a PR to hot state and updates last activity time
func (s *Store) SetPRHot(prID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	now := time.Now().Format(time.RFC3339)
	s.db.Exec(`UPDATE prs SET heat_state = ?, last_heat_activity_at = ? WHERE id = ?`,
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
	s.db.Exec(`UPDATE prs SET heat_state = ? WHERE heat_state = ? AND last_heat_activity_at < ?`,
		protocol.HeatStateWarm, protocol.HeatStateHot, warmThreshold)

	// Warm → Cold (after 10 min)
	s.db.Exec(`UPDATE prs SET heat_state = ? WHERE heat_state = ? AND last_heat_activity_at < ?`,
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
	repoRows, _ := s.db.Query("SELECT repo FROM repos WHERE muted = 1")
	if repoRows != nil {
		defer repoRows.Close()
		for repoRows.Next() {
			var repo string
			repoRows.Scan(&repo)
			mutedRepos[repo] = true
		}
	}

	now := time.Now()
	var result []*protocol.PR

	rows, err := s.db.Query(`
		SELECT id, repo, number, title, url, role, state, reason, last_updated, last_polled,
		       muted, details_fetched, details_fetched_at, mergeable, mergeable_state,
		       ci_status, review_status, head_sha, comment_count, approved_by_me,
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
		elapsed := now.Sub(pr.DetailsFetchedAt)
		needsRefresh := false

		switch pr.HeatState {
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

// Legacy methods for compatibility - these are no-ops with SQLite

// IsDirty returns false - SQLite doesn't need dirty tracking
func (s *Store) IsDirty() bool {
	return false
}

// ClearDirty is a no-op with SQLite
func (s *Store) ClearDirty() {}

// Save is a no-op with SQLite - data is already persisted
func (s *Store) Save() {}

// Load is a no-op with SQLite - data is loaded on demand
func (s *Store) Load() error {
	return nil
}

// StartPersistence is a no-op with SQLite
func (s *Store) StartPersistence(interval time.Duration, done <-chan struct{}) {
	// Just wait for done signal
	<-done
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

func nullTimeString(t time.Time) interface{} {
	if t.IsZero() {
		return nil
	}
	return t.Format(time.RFC3339)
}

func scanPR(rows *sql.Rows) *protocol.PR {
	var pr protocol.PR
	var muted, detailsFetched, approvedByMe int
	var lastUpdated, lastPolled string
	var detailsFetchedAt, mergeableState, ciStatus, reviewStatus, headSHA sql.NullString
	var heatState, lastHeatActivityAt sql.NullString
	var mergeable sql.NullInt64
	var commentCount int

	err := rows.Scan(
		&pr.ID, &pr.Repo, &pr.Number, &pr.Title, &pr.URL, &pr.Role, &pr.State, &pr.Reason,
		&lastUpdated, &lastPolled, &muted, &detailsFetched, &detailsFetchedAt,
		&mergeable, &mergeableState, &ciStatus, &reviewStatus,
		&headSHA, &commentCount, &approvedByMe,
		&heatState, &lastHeatActivityAt,
	)
	if err != nil {
		return nil
	}

	pr.LastUpdated, _ = time.Parse(time.RFC3339, lastUpdated)
	pr.LastPolled, _ = time.Parse(time.RFC3339, lastPolled)
	pr.Muted = muted == 1
	pr.DetailsFetched = detailsFetched == 1
	if detailsFetchedAt.Valid {
		pr.DetailsFetchedAt, _ = time.Parse(time.RFC3339, detailsFetchedAt.String)
	}
	if mergeable.Valid {
		m := mergeable.Int64 == 1
		pr.Mergeable = &m
	}
	pr.MergeableState = mergeableState.String
	pr.CIStatus = ciStatus.String
	pr.ReviewStatus = reviewStatus.String
	pr.HeadSHA = headSHA.String
	pr.CommentCount = commentCount
	pr.ApprovedByMe = approvedByMe == 1
	pr.HeatState = heatState.String
	if pr.HeatState == "" {
		pr.HeatState = protocol.HeatStateCold
	}
	if lastHeatActivityAt.Valid {
		pr.LastHeatActivityAt, _ = time.Parse(time.RFC3339, lastHeatActivityAt.String)
	}

	return &pr
}

func scanPRRow(row *sql.Row) *protocol.PR {
	var pr protocol.PR
	var muted, detailsFetched, approvedByMe int
	var lastUpdated, lastPolled string
	var detailsFetchedAt, mergeableState, ciStatus, reviewStatus, headSHA sql.NullString
	var heatState, lastHeatActivityAt sql.NullString
	var mergeable sql.NullInt64
	var commentCount int

	err := row.Scan(
		&pr.ID, &pr.Repo, &pr.Number, &pr.Title, &pr.URL, &pr.Role, &pr.State, &pr.Reason,
		&lastUpdated, &lastPolled, &muted, &detailsFetched, &detailsFetchedAt,
		&mergeable, &mergeableState, &ciStatus, &reviewStatus,
		&headSHA, &commentCount, &approvedByMe,
		&heatState, &lastHeatActivityAt,
	)
	if err != nil {
		return nil
	}

	pr.LastUpdated, _ = time.Parse(time.RFC3339, lastUpdated)
	pr.LastPolled, _ = time.Parse(time.RFC3339, lastPolled)
	pr.Muted = muted == 1
	pr.DetailsFetched = detailsFetched == 1
	if detailsFetchedAt.Valid {
		pr.DetailsFetchedAt, _ = time.Parse(time.RFC3339, detailsFetchedAt.String)
	}
	if mergeable.Valid {
		m := mergeable.Int64 == 1
		pr.Mergeable = &m
	}
	pr.MergeableState = mergeableState.String
	pr.CIStatus = ciStatus.String
	pr.ReviewStatus = reviewStatus.String
	pr.HeadSHA = headSHA.String
	pr.CommentCount = commentCount
	pr.ApprovedByMe = approvedByMe == 1
	pr.HeatState = heatState.String
	if pr.HeatState == "" {
		pr.HeatState = protocol.HeatStateCold
	}
	if lastHeatActivityAt.Valid {
		pr.LastHeatActivityAt, _ = time.Parse(time.RFC3339, lastHeatActivityAt.String)
	}

	return &pr
}
