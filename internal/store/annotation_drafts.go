package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Annotation drafts — the user's in-progress marks on a document or on a
// session's terminal output — share one persistence shape, and the interesting
// part of that shape is not the storage but the ordering rule.
//
// A draft is saved as a whole list under a monotonically increasing
// generation. A save is accepted only if its generation is strictly greater
// than both the stored generation (so an out-of-order reply cannot resurrect an
// older list) and the tombstone generation (so a save already in flight when
// the draft was cleared cannot bring the cleared marks back). Clearing is a
// tombstone rather than a delete for exactly that reason: the row is what
// remembers that the clear happened.
//
// Both callers get the same implementation because the failure this rule
// prevents — a stale write silently winning — is invisible until it costs
// someone their work, which is far too late to discover that only one of two
// copies had the check.

// ErrStaleAnnotationSave marks a save whose generation did not clear the
// stored generation and tombstone floor. It is a benign protocol outcome, not
// an operational failure: the client drops its pending list and re-hydrates.
var ErrStaleAnnotationSave = errors.New("stale annotation save")

// annotationDraftTable names one draft table and its key column. Every draft
// table has the same columns beyond that key.
type annotationDraftTable struct {
	table string
	key   string
}

var (
	markdownDraftTable = annotationDraftTable{table: "markdown_annotation_drafts", key: "path"}
	sessionDraftTable  = annotationDraftTable{table: "session_annotation_drafts", key: "session_id"}
)

// annotationDraft is one stored draft: the raw list plus the generation floor a
// client must exceed to write.
type annotationDraft struct {
	Annotations string // raw JSON array
	Generation  int    // max(generation, tombstone_generation)
	UpdatedAt   string
}

// get returns the draft for key. A missing row is an empty draft at generation
// 0, not an error — nothing has been written there yet. The generation returned
// is the floor including any tombstone, so a re-mounting client seeds its
// counter past a clear even when the list is empty.
func (t annotationDraftTable) get(s *Store, key string) (annotationDraft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var annotations, updatedAt string
	var generation, tombstone int
	query := fmt.Sprintf(
		"SELECT annotations_json, generation, tombstone_generation, updated_at FROM %s WHERE %s = ?",
		t.table, t.key,
	)
	err := s.db.QueryRow(query, key).Scan(&annotations, &generation, &tombstone, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return annotationDraft{Annotations: "[]", Generation: 0}, nil
	}
	if err != nil {
		return annotationDraft{}, fmt.Errorf("failed to get %s draft: %w", t.table, err)
	}
	return annotationDraft{
		Annotations: annotations,
		Generation:  max(generation, tombstone),
		UpdatedAt:   updatedAt,
	}, nil
}

// save upserts the full list for key, rejecting anything that does not clear
// the floor with ErrStaleAnnotationSave.
func (t annotationDraftTable) save(s *Store, key, annotationsJSON string, generation int, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin %s save: %w", t.table, err)
	}
	defer tx.Rollback()

	storedGeneration, tombstone, err := t.readGenerations(tx, key)
	if err != nil {
		return err
	}
	if generation <= storedGeneration || generation <= tombstone {
		return ErrStaleAnnotationSave
	}

	query := fmt.Sprintf(`
		INSERT INTO %s (%s, annotations_json, generation, tombstone_generation, updated_at)
		VALUES (?, ?, ?, 0, ?)
		ON CONFLICT(%s) DO UPDATE SET
			annotations_json = excluded.annotations_json,
			generation = excluded.generation,
			updated_at = excluded.updated_at
	`, t.table, t.key, t.key)
	if _, err := tx.Exec(query, key, annotationsJSON, generation, now.UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("failed to save %s draft: %w", t.table, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit %s save: %w", t.table, err)
	}
	return nil
}

// clear empties the list and raises the tombstone to the highest generation
// anyone has claimed, so every save already in flight is refused. Idempotent,
// and works on a missing row — the tombstone IS the row.
func (t annotationDraftTable) clear(s *Store, key string, generation int, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin %s clear: %w", t.table, err)
	}
	defer tx.Rollback()

	storedGeneration, tombstone, err := t.readGenerations(tx, key)
	if err != nil {
		return err
	}
	newTombstone := max(generation, max(storedGeneration, tombstone))

	query := fmt.Sprintf(`
		INSERT INTO %s (%s, annotations_json, generation, tombstone_generation, updated_at)
		VALUES (?, '[]', ?, ?, ?)
		ON CONFLICT(%s) DO UPDATE SET
			annotations_json = '[]',
			tombstone_generation = excluded.tombstone_generation,
			updated_at = excluded.updated_at
	`, t.table, t.key, t.key)
	if _, err := tx.Exec(query, key, storedGeneration, newTombstone, now.UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("failed to clear %s draft: %w", t.table, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit %s clear: %w", t.table, err)
	}
	return nil
}

// delete removes the row outright. Used when the thing the draft belongs to is
// gone for good, where a tombstone would only be a row nobody can ever reach.
func (t annotationDraftTable) delete(s *Store, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := fmt.Sprintf("DELETE FROM %s WHERE %s = ?", t.table, t.key)
	if _, err := s.db.Exec(query, key); err != nil {
		return fmt.Errorf("failed to delete %s draft: %w", t.table, err)
	}
	return nil
}

func (t annotationDraftTable) readGenerations(tx *sql.Tx, key string) (generation, tombstone int, err error) {
	query := fmt.Sprintf("SELECT generation, tombstone_generation FROM %s WHERE %s = ?", t.table, t.key)
	err = tx.QueryRow(query, key).Scan(&generation, &tombstone)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, 0, fmt.Errorf("failed to read %s generation: %w", t.table, err)
	}
	return generation, tombstone, nil
}
