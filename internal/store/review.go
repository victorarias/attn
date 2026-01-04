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

// ReviewComment represents a comment on a code review
type ReviewComment struct {
	ID         string
	ReviewID   string
	Filepath   string
	LineStart  int
	LineEnd    int
	Content    string
	Author     string // "user" or "agent"
	Resolved   bool
	ResolvedBy string     // "user" or "agent" (empty if not resolved)
	ResolvedAt *time.Time // when resolved (nil if not resolved)
	CreatedAt  time.Time
}

// AddComment adds a new comment to a review
func (s *Store) AddComment(reviewID, filepath string, lineStart, lineEnd int, content, author string) (*ReviewComment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	comment := &ReviewComment{
		ID:        uuid.New().String(),
		ReviewID:  reviewID,
		Filepath:  filepath,
		LineStart: lineStart,
		LineEnd:   lineEnd,
		Content:   content,
		Author:    author,
		Resolved:  false,
		CreatedAt: now,
	}

	_, err := s.db.Exec(`
		INSERT INTO review_comments (id, review_id, filepath, line_start, line_end, content, author, resolved, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, comment.ID, comment.ReviewID, comment.Filepath, comment.LineStart, comment.LineEnd,
		comment.Content, comment.Author, 0, now.Format(time.RFC3339))
	if err != nil {
		return nil, err
	}

	return comment, nil
}

// GetComments returns all comments for a review
func (s *Store) GetComments(reviewID string) ([]*ReviewComment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT id, review_id, filepath, line_start, line_end, content, author, resolved, resolved_by, resolved_at, created_at
		FROM review_comments WHERE review_id = ? ORDER BY filepath, line_start
	`, reviewID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanComments(rows)
}

// GetCommentsForFile returns comments for a specific file
func (s *Store) GetCommentsForFile(reviewID, filepath string) ([]*ReviewComment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT id, review_id, filepath, line_start, line_end, content, author, resolved, resolved_by, resolved_at, created_at
		FROM review_comments WHERE review_id = ? AND filepath = ? ORDER BY line_start
	`, reviewID, filepath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanComments(rows)
}

// GetCommentByID returns a single comment by ID
func (s *Store) GetCommentByID(id string) (*ReviewComment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var comment ReviewComment
	var resolved int
	var resolvedAt, createdAt string

	err := s.db.QueryRow(`
		SELECT id, review_id, filepath, line_start, line_end, content, author, resolved, resolved_by, resolved_at, created_at
		FROM review_comments WHERE id = ?
	`, id).Scan(&comment.ID, &comment.ReviewID, &comment.Filepath, &comment.LineStart,
		&comment.LineEnd, &comment.Content, &comment.Author, &resolved, &comment.ResolvedBy, &resolvedAt, &createdAt)
	if err != nil {
		return nil, err
	}

	comment.Resolved = resolved == 1
	if resolvedAt != "" {
		t, _ := time.Parse(time.RFC3339, resolvedAt)
		comment.ResolvedAt = &t
	}
	comment.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &comment, nil
}

// UpdateComment updates the content of a comment
func (s *Store) UpdateComment(id, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`UPDATE review_comments SET content = ? WHERE id = ?`, content, id)
	return err
}

// ResolveComment sets the resolved status of a comment
// resolvedBy should be "user" or "agent" when resolving, or empty when unresolving
func (s *Store) ResolveComment(id string, resolved bool, resolvedBy string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	resolvedInt := 0
	resolvedAt := ""
	if resolved {
		resolvedInt = 1
		resolvedAt = time.Now().UTC().Format(time.RFC3339)
	} else {
		resolvedBy = "" // Clear resolvedBy when unresolving
	}
	_, err := s.db.Exec(`UPDATE review_comments SET resolved = ?, resolved_by = ?, resolved_at = ? WHERE id = ?`,
		resolvedInt, resolvedBy, resolvedAt, id)
	return err
}

// DeleteComment deletes a comment
func (s *Store) DeleteComment(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`DELETE FROM review_comments WHERE id = ?`, id)
	return err
}

func scanComments(rows *sql.Rows) ([]*ReviewComment, error) {
	var comments []*ReviewComment
	for rows.Next() {
		var comment ReviewComment
		var resolved int
		var resolvedAt, createdAt string
		if err := rows.Scan(&comment.ID, &comment.ReviewID, &comment.Filepath, &comment.LineStart,
			&comment.LineEnd, &comment.Content, &comment.Author, &resolved, &comment.ResolvedBy, &resolvedAt, &createdAt); err != nil {
			return nil, err
		}
		comment.Resolved = resolved == 1
		if resolvedAt != "" {
			t, _ := time.Parse(time.RFC3339, resolvedAt)
			comment.ResolvedAt = &t
		}
		comment.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		comments = append(comments, &comment)
	}
	return comments, rows.Err()
}
