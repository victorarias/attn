package store

import (
	"time"

	"github.com/google/uuid"
)

// Rows that predate the column read unknown and stay that way.
type WorktreeOrigin string

const (
	WorktreeOriginUnknown WorktreeOrigin = ""
	WorktreeOriginAttn    WorktreeOrigin = "attn"
	WorktreeOriginGit     WorktreeOrigin = "git"
)

// One value per gate, so a kept row always says which gate held it.
type WorktreeSweepStatus string

const (
	WorktreeSweepUnknown         WorktreeSweepStatus = ""
	WorktreeSweepScheduled       WorktreeSweepStatus = "scheduled"
	WorktreeSweepPinned          WorktreeSweepStatus = "pinned"
	WorktreeSweepKeptDirty       WorktreeSweepStatus = "kept_dirty"
	WorktreeSweepKeptUnmerged    WorktreeSweepStatus = "kept_unmerged"
	WorktreeSweepKeptUnpushed    WorktreeSweepStatus = "kept_unpushed"
	WorktreeSweepKeptDetached    WorktreeSweepStatus = "kept_detached"
	WorktreeSweepKeptLiveSession WorktreeSweepStatus = "kept_live_session"
	WorktreeSweepKeptOpenSeed    WorktreeSweepStatus = "kept_open_seed"
	WorktreeSweepKeptStale       WorktreeSweepStatus = "kept_stale"
	WorktreeSweepRemoved         WorktreeSweepStatus = "removed"
)

// Which rung of the merged ladder answered, strongest first. See docs/worktree-sweep.md.
type MergedSignal string

const (
	MergedSignalNone        MergedSignal = ""
	MergedSignalPullRequest MergedSignal = "pull_request"
	MergedSignalAncestor    MergedSignal = "ancestor"
	MergedSignalTree        MergedSignal = "tree"
)

type Worktree struct {
	Path      string         `json:"path"`
	Branch    string         `json:"branch"`
	MainRepo  string         `json:"main_repo"`
	CreatedAt time.Time      `json:"created_at"`
	Origin    WorktreeOrigin `json:"origin"`
	PinnedAt  string         `json:"pinned_at,omitempty"`

	// Observed state written by the background refresh. Empty means never refreshed.
	ObservedAt     string       `json:"observed_at,omitempty"`
	HeadSHA        string       `json:"head_sha,omitempty"`
	Detached       bool         `json:"detached,omitempty"`
	Dirty          bool         `json:"dirty,omitempty"`
	DirtyFiles     int          `json:"dirty_files,omitempty"`
	Stashes        int          `json:"stashes,omitempty"`
	Unpushed       int          `json:"unpushed,omitempty"`
	MergedSignal   MergedSignal `json:"merged_signal,omitempty"`
	Prunable       bool         `json:"prunable,omitempty"`
	LastActivityAt string       `json:"last_activity_at,omitempty"`
	RefreshError   string       `json:"refresh_error,omitempty"`

	SweepStatus WorktreeSweepStatus `json:"sweep_status,omitempty"`
	SweepReason string              `json:"sweep_reason,omitempty"`
	// The date the row becomes eligible while scheduled; the date it went once removed.
	SweepAt string `json:"sweep_at,omitempty"`
}

func (w *Worktree) Pinned() bool { return w != nil && w.PinnedAt != "" }

// Written whole, so a partial pass never leaves half-fresh state on the row.
type WorktreeObservation struct {
	Branch         string
	HeadSHA        string
	Detached       bool
	Dirty          bool
	DirtyFiles     int
	Stashes        int
	Unpushed       int
	MergedSignal   MergedSignal
	Prunable       bool
	LastActivityAt time.Time
	Error          string
}

const worktreeColumns = `path, branch, main_repo, created_at, origin, pinned_at,
	observed_at, head_sha, detached, dirty, dirty_files, stashes, unpushed, merged_signal,
	prunable, last_activity_at, sweep_status, sweep_reason, sweep_at, refresh_error`

// Adoption is a refresh, so an existing row keeps its creation stamp, its origin,
// its pin and its observed state; only what git just reported is overwritten.
func (s *Store) AddWorktree(wt *Worktree) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	s.execLog(`
		INSERT INTO worktrees (path, branch, main_repo, created_at, origin)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			branch = excluded.branch,
			main_repo = excluded.main_repo`,
		wt.Path, wt.Branch, wt.MainRepo, wt.CreatedAt.Format(time.RFC3339), string(wt.Origin),
	)
}

func (s *Store) GetWorktree(path string) *Worktree {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil
	}

	row := s.db.QueryRow(`SELECT `+worktreeColumns+` FROM worktrees WHERE path = ?`, path)
	wt, err := scanWorktree(row)
	if err != nil {
		return nil
	}
	return wt
}

func (s *Store) RemoveWorktree(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	s.execLog("DELETE FROM worktrees WHERE path = ?", path)
}

func (s *Store) ListWorktreesByRepo(mainRepo string) []*Worktree {
	return s.queryWorktrees(`SELECT `+worktreeColumns+` FROM worktrees WHERE main_repo = ? ORDER BY path`, mainRepo)
}

func (s *Store) ListWorktrees() []*Worktree {
	return s.queryWorktrees(`SELECT ` + worktreeColumns + ` FROM worktrees ORDER BY main_repo, path`)
}

func (s *Store) ListWorktreeRepos() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil
	}

	rows, err := s.db.Query(`SELECT DISTINCT main_repo FROM worktrees WHERE main_repo != '' ORDER BY main_repo`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var repos []string
	for rows.Next() {
		var repo string
		if err := rows.Scan(&repo); err != nil {
			continue
		}
		repos = append(repos, repo)
	}
	return repos
}

func (s *Store) queryWorktrees(query string, args ...interface{}) []*Worktree {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var result []*Worktree
	for rows.Next() {
		wt, err := scanWorktree(rows)
		if err != nil {
			continue
		}
		result = append(result, wt)
	}
	return result
}

func scanWorktree(row rowScanner) (*Worktree, error) {
	var wt Worktree
	var createdAt, origin, mergedSignal, sweepStatus string
	err := row.Scan(
		&wt.Path, &wt.Branch, &wt.MainRepo, &createdAt, &origin, &wt.PinnedAt,
		&wt.ObservedAt, &wt.HeadSHA, &wt.Detached, &wt.Dirty, &wt.DirtyFiles, &wt.Stashes,
		&wt.Unpushed, &mergedSignal, &wt.Prunable, &wt.LastActivityAt,
		&sweepStatus, &wt.SweepReason, &wt.SweepAt, &wt.RefreshError,
	)
	if err != nil {
		return nil, err
	}
	wt.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	wt.Origin = WorktreeOrigin(origin)
	wt.MergedSignal = MergedSignal(mergedSignal)
	wt.SweepStatus = WorktreeSweepStatus(sweepStatus)
	return &wt, nil
}

func (s *Store) UpdateWorktreeObservation(path string, obs WorktreeObservation, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	lastActivity := ""
	if !obs.LastActivityAt.IsZero() {
		lastActivity = obs.LastActivityAt.Format(time.RFC3339)
	}
	s.execLog(`
		UPDATE worktrees SET
			branch = ?, head_sha = ?, detached = ?, dirty = ?, dirty_files = ?, stashes = ?,
			unpushed = ?, merged_signal = ?, prunable = ?, last_activity_at = ?,
			observed_at = ?, refresh_error = ?
		WHERE path = ?`,
		obs.Branch, obs.HeadSHA, obs.Detached, obs.Dirty, obs.DirtyFiles, obs.Stashes,
		obs.Unpushed, string(obs.MergedSignal), obs.Prunable, lastActivity,
		now.Format(time.RFC3339), obs.Error, path,
	)
}

func (s *Store) SetWorktreeSweep(path string, status WorktreeSweepStatus, reason string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	stamp := ""
	if !at.IsZero() {
		stamp = at.Format(time.RFC3339)
	}
	s.execLog(`UPDATE worktrees SET sweep_status = ?, sweep_reason = ?, sweep_at = ? WHERE path = ?`,
		string(status), reason, stamp, path)
}

// Reports false when no row moved, so the caller can say the path is not registered.
func (s *Store) SetWorktreePin(path string, pinned bool, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return false
	}

	stamp := ""
	if pinned {
		stamp = now.Format(time.RFC3339)
	}
	result, err := s.db.Exec(`UPDATE worktrees SET pinned_at = ? WHERE path = ?`, stamp, path)
	if err != nil {
		return false
	}
	affected, err := result.RowsAffected()
	return err == nil && affected > 0
}

type WorktreeSweepLogEntry struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	MainRepo string `json:"main_repo"`
	Branch   string `json:"branch,omitempty"`
	// "removed", "kept" or "failed": what the sweep did, not what it saw.
	Action string `json:"action"`
	Reason string `json:"reason,omitempty"`
	At     string `json:"at"`
}

// A removed worktree has no row left to carry its outcome; this log is where it
// stays visible.
func (s *Store) AppendWorktreeSweepLog(entry WorktreeSweepLogEntry, now time.Time) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return ""
	}

	id := entry.ID
	if id == "" {
		id = uuid.NewString()
	}
	at := entry.At
	if at == "" {
		at = now.Format(time.RFC3339Nano)
	}
	s.execLog(`
		INSERT OR REPLACE INTO worktree_sweep_log (id, path, main_repo, branch, action, reason, at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, entry.Path, entry.MainRepo, entry.Branch, entry.Action, entry.Reason, at)
	return id
}

// Newest first, with the number of rows past the limit.
func (s *Store) WorktreeSweepLog(mainRepo string, limit int) ([]WorktreeSweepLogEntry, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, 0
	}

	where := ""
	var args []interface{}
	if mainRepo != "" {
		where = " WHERE main_repo = ?"
		args = append(args, mainRepo)
	}

	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM worktree_sweep_log`+where, args...).Scan(&total); err != nil {
		return nil, 0
	}

	rows, err := s.db.Query(`
		SELECT id, path, main_repo, branch, action, reason, at
		FROM worktree_sweep_log`+where+`
		ORDER BY at DESC, id DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, 0
	}
	defer rows.Close()

	var entries []WorktreeSweepLogEntry
	for rows.Next() {
		var entry WorktreeSweepLogEntry
		if err := rows.Scan(&entry.ID, &entry.Path, &entry.MainRepo, &entry.Branch,
			&entry.Action, &entry.Reason, &entry.At); err != nil {
			continue
		}
		entries = append(entries, entry)
	}

	omitted := total - len(entries)
	if omitted < 0 {
		omitted = 0
	}
	return entries, omitted
}

type RepoIntegrationBranch struct {
	MainRepo string `json:"main_repo"`
	Branch   string `json:"branch"`
	// "pull_requests" when merged pull requests named it, "origin_head" for the fallback.
	Source     string `json:"source"`
	ResolvedAt string `json:"resolved_at"`
}

func (s *Store) SetRepoIntegrationBranch(mainRepo, branch, source string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	s.execLog(`
		INSERT OR REPLACE INTO repo_integration_branches (main_repo, branch, source, resolved_at)
		VALUES (?, ?, ?, ?)`,
		mainRepo, branch, source, now.Format(time.RFC3339))
}

func (s *Store) RepoIntegrationBranch(mainRepo string) *RepoIntegrationBranch {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil
	}

	var record RepoIntegrationBranch
	err := s.db.QueryRow(`
		SELECT main_repo, branch, source, resolved_at
		FROM repo_integration_branches WHERE main_repo = ?`, mainRepo).Scan(
		&record.MainRepo, &record.Branch, &record.Source, &record.ResolvedAt)
	if err != nil {
		return nil
	}
	return &record
}

type MergedBranch struct {
	Branch   string `json:"branch"`
	MergedAt string `json:"merged_at,omitempty"`
	Number   int    `json:"number,omitempty"`
	URL      string `json:"url,omitempty"`
	// The tip that merged. A branch whose tip has moved past it carries commits
	// the merge does not account for, which is what keeps the sweep off it.
	HeadSHA string `json:"head_sha,omitempty"`
}

// Repository-scoped on purpose: session_pull_requests is session-owned and dropped
// when a session is reaped, and this signal must outlive every session.
func (s *Store) RecordRepoMergedBranches(mainRepo string, branches []MergedBranch, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil || len(branches) == 0 {
		return
	}

	stamp := now.Format(time.RFC3339)
	for _, branch := range branches {
		if branch.Branch == "" {
			continue
		}
		s.execLog(`
			INSERT OR REPLACE INTO repo_merged_branches (main_repo, branch, merged_at, number, url, head_sha, observed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			mainRepo, branch.Branch, branch.MergedAt, branch.Number, branch.URL, branch.HeadSHA, stamp)
	}
}

func (s *Store) RepoMergedBranches(mainRepo string) map[string]MergedBranch {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil
	}

	rows, err := s.db.Query(`
		SELECT branch, merged_at, number, url, head_sha
		FROM repo_merged_branches WHERE main_repo = ?`, mainRepo)
	if err != nil {
		return nil
	}
	defer rows.Close()

	merged := make(map[string]MergedBranch)
	for rows.Next() {
		var branch MergedBranch
		if err := rows.Scan(&branch.Branch, &branch.MergedAt, &branch.Number, &branch.URL, &branch.HeadSHA); err != nil {
			continue
		}
		merged[branch.Branch] = branch
	}
	return merged
}

func (s *Store) MergedSessionPullRequestBranches() map[string]MergedBranch {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil
	}

	rows, err := s.db.Query(`
		SELECT head_branch, number, url, head_sha FROM session_pull_requests
		WHERE state = 'merged' AND head_branch != ''`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	branches := make(map[string]MergedBranch)
	for rows.Next() {
		var branch MergedBranch
		if err := rows.Scan(&branch.Branch, &branch.Number, &branch.URL, &branch.HeadSHA); err != nil {
			continue
		}
		branches[branch.Branch] = branch
	}
	return branches
}
