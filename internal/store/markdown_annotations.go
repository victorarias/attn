package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrStaleMarkdownAnnotationSave is returned by SaveMarkdownAnnotationDraft
// when the incoming generation is not strictly greater than both the stored
// generation and the tombstone generation. It marks the save as benignly
// stale (the client should drop its pending list and re-hydrate), not an
// operational failure.
var ErrStaleMarkdownAnnotationSave = errors.New("stale markdown annotation save")

// MarkdownAnnotationDraft is the persisted annotation draft for one markdown
// file, keyed by absolute path (drafts are a property of the document, not of
// any workspace or tile — the same file open anywhere shows the same drafts).
type MarkdownAnnotationDraft struct {
	Path        string
	Annotations string // raw JSON array of protocol.MarkdownAnnotation
	Generation  int    // current generation floor: max(generation, tombstone)
	UpdatedAt   string
}

// GetMarkdownAnnotationDraft returns the draft for path. Annotations may be
// "[]" (after a clear). Generation is the current generation floor —
// max(stored generation, tombstone_generation) — so a re-mounting client
// seeds its counter past a tombstone even when the draft is empty. A missing
// row yields an empty draft with generation 0.
func (s *Store) GetMarkdownAnnotationDraft(path string) (*MarkdownAnnotationDraft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var annotations, updatedAt string
	var generation, tombstone int
	err := s.db.QueryRow(`
		SELECT annotations_json, generation, tombstone_generation, updated_at
		FROM markdown_annotation_drafts WHERE path = ?
	`, path).Scan(&annotations, &generation, &tombstone, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return &MarkdownAnnotationDraft{Path: path, Annotations: "[]", Generation: 0}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get markdown annotation draft: %w", err)
	}
	floor := generation
	if tombstone > floor {
		floor = tombstone
	}
	return &MarkdownAnnotationDraft{
		Path:        path,
		Annotations: annotations,
		Generation:  floor,
		UpdatedAt:   updatedAt,
	}, nil
}

// SaveMarkdownAnnotationDraft upserts the full annotation list for path.
// The save is rejected with ErrStaleMarkdownAnnotationSave unless generation
// is strictly greater than both the stored generation (monotonicity) and the
// tombstone generation (a debounced save that fires after a clear must not
// resurrect ghost drafts).
func (s *Store) SaveMarkdownAnnotationDraft(path, annotationsJSON string, generation int, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin markdown annotation save: %w", err)
	}
	defer tx.Rollback()

	var storedGeneration, tombstone int
	err = tx.QueryRow(`
		SELECT generation, tombstone_generation FROM markdown_annotation_drafts WHERE path = ?
	`, path).Scan(&storedGeneration, &tombstone)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("failed to read markdown annotation draft generation: %w", err)
	}
	if generation <= storedGeneration || generation <= tombstone {
		return ErrStaleMarkdownAnnotationSave
	}

	updatedAt := now.UTC().Format(time.RFC3339)
	_, err = tx.Exec(`
		INSERT INTO markdown_annotation_drafts (path, annotations_json, generation, tombstone_generation, updated_at)
		VALUES (?, ?, ?, 0, ?)
		ON CONFLICT(path) DO UPDATE SET
			annotations_json = excluded.annotations_json,
			generation = excluded.generation,
			updated_at = excluded.updated_at
	`, path, annotationsJSON, generation, updatedAt)
	if err != nil {
		return fmt.Errorf("failed to save markdown annotation draft: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit markdown annotation save: %w", err)
	}
	return nil
}

// ClearMarkdownAnnotationDraft tombstones the draft for path: the list is
// emptied and tombstone_generation becomes max(existing tombstone, stored
// generation, generation). Any later save carrying generation <= tombstone is
// rejected. Idempotent, and works on a missing row (the tombstone IS the
// row). This is the primitive PR6's clear-on-send calls; the sidebar
// "clear all" uses it today.
func (s *Store) ClearMarkdownAnnotationDraft(path string, generation int, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin markdown annotation clear: %w", err)
	}
	defer tx.Rollback()

	var storedGeneration, tombstone int
	err = tx.QueryRow(`
		SELECT generation, tombstone_generation FROM markdown_annotation_drafts WHERE path = ?
	`, path).Scan(&storedGeneration, &tombstone)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("failed to read markdown annotation draft generation: %w", err)
	}
	newTombstone := generation
	if storedGeneration > newTombstone {
		newTombstone = storedGeneration
	}
	if tombstone > newTombstone {
		newTombstone = tombstone
	}

	updatedAt := now.UTC().Format(time.RFC3339)
	_, err = tx.Exec(`
		INSERT INTO markdown_annotation_drafts (path, annotations_json, generation, tombstone_generation, updated_at)
		VALUES (?, '[]', ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			annotations_json = '[]',
			tombstone_generation = excluded.tombstone_generation,
			updated_at = excluded.updated_at
	`, path, storedGeneration, newTombstone, updatedAt)
	if err != nil {
		return fmt.Errorf("failed to clear markdown annotation draft: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit markdown annotation clear: %w", err)
	}
	return nil
}
