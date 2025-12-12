// internal/store/worktree.go
package store

import (
	"time"
)

// Worktree represents a tracked git worktree
type Worktree struct {
	Path      string    `json:"path"`
	Branch    string    `json:"branch"`
	MainRepo  string    `json:"main_repo"`
	CreatedAt time.Time `json:"created_at"`
}

// AddWorktree adds a worktree to the registry
func (s *Store) AddWorktree(wt *Worktree) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	_, _ = s.db.Exec(`
		INSERT OR REPLACE INTO worktrees (path, branch, main_repo, created_at)
		VALUES (?, ?, ?, ?)`,
		wt.Path, wt.Branch, wt.MainRepo, wt.CreatedAt.Format(time.RFC3339),
	)
}

// GetWorktree returns a worktree by path
func (s *Store) GetWorktree(path string) *Worktree {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil
	}

	var wt Worktree
	var createdAt string

	err := s.db.QueryRow(`
		SELECT path, branch, main_repo, created_at
		FROM worktrees WHERE path = ?`, path).Scan(
		&wt.Path, &wt.Branch, &wt.MainRepo, &createdAt,
	)
	if err != nil {
		return nil
	}

	wt.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &wt
}

// RemoveWorktree removes a worktree from the registry
func (s *Store) RemoveWorktree(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}

	s.db.Exec("DELETE FROM worktrees WHERE path = ?", path)
}

// ListWorktreesByRepo returns all worktrees for a main repo
func (s *Store) ListWorktreesByRepo(mainRepo string) []*Worktree {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil
	}

	rows, err := s.db.Query(`
		SELECT path, branch, main_repo, created_at
		FROM worktrees WHERE main_repo = ?`, mainRepo)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var result []*Worktree
	for rows.Next() {
		var wt Worktree
		var createdAt string

		err := rows.Scan(&wt.Path, &wt.Branch, &wt.MainRepo, &createdAt)
		if err != nil {
			continue
		}

		wt.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		result = append(result, &wt)
	}

	return result
}
