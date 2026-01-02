package store

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// Review represents a code review for a branch
type Review struct {
	ID        string
	Branch    string
	PRNumber  *int // nullable
	RepoPath  string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// GetOrCreateReview returns the review for a branch/repo, creating it if needed
func (s *Store) GetOrCreateReview(repoPath, branch string) (*Review, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Try to find existing review
	var review Review
	var prNumber sql.NullInt64
	var createdAt, updatedAt string

	err := s.db.QueryRow(`
		SELECT id, branch, pr_number, repo_path, created_at, updated_at
		FROM reviews WHERE repo_path = ? AND branch = ?
	`, repoPath, branch).Scan(&review.ID, &review.Branch, &prNumber, &review.RepoPath, &createdAt, &updatedAt)

	if err == nil {
		// Found existing review
		if prNumber.Valid {
			n := int(prNumber.Int64)
			review.PRNumber = &n
		}
		review.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		review.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		return &review, nil
	}

	if err != sql.ErrNoRows {
		return nil, err
	}

	// Create new review
	now := time.Now().UTC().Format(time.RFC3339)
	review = Review{
		ID:        uuid.New().String(),
		Branch:    branch,
		RepoPath:  repoPath,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	_, err = s.db.Exec(`
		INSERT INTO reviews (id, branch, repo_path, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`, review.ID, review.Branch, review.RepoPath, now, now)
	if err != nil {
		return nil, err
	}

	return &review, nil
}

// GetReviewByID returns a review by its ID
func (s *Store) GetReviewByID(id string) (*Review, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var review Review
	var prNumber sql.NullInt64
	var createdAt, updatedAt string

	err := s.db.QueryRow(`
		SELECT id, branch, pr_number, repo_path, created_at, updated_at
		FROM reviews WHERE id = ?
	`, id).Scan(&review.ID, &review.Branch, &prNumber, &review.RepoPath, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}

	if prNumber.Valid {
		n := int(prNumber.Int64)
		review.PRNumber = &n
	}
	review.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	review.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)

	return &review, nil
}

// MarkFileViewed marks a file as viewed in a review
func (s *Store) MarkFileViewed(reviewID, filepath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO review_viewed_files (review_id, filepath, viewed_at)
		VALUES (?, ?, ?)
	`, reviewID, filepath, now)
	return err
}

// UnmarkFileViewed removes a file from viewed list
func (s *Store) UnmarkFileViewed(reviewID, filepath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
		DELETE FROM review_viewed_files WHERE review_id = ? AND filepath = ?
	`, reviewID, filepath)
	return err
}

// GetViewedFiles returns all viewed file paths for a review
func (s *Store) GetViewedFiles(reviewID string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT filepath FROM review_viewed_files WHERE review_id = ?
	`, reviewID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []string
	for rows.Next() {
		var filepath string
		if err := rows.Scan(&filepath); err != nil {
			return nil, err
		}
		files = append(files, filepath)
	}

	return files, rows.Err()
}

// ClearViewedFiles removes all viewed files for a review
func (s *Store) ClearViewedFiles(reviewID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`DELETE FROM review_viewed_files WHERE review_id = ?`, reviewID)
	return err
}
