package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Annotation drafts (markdown and terminal) share one ordering rule: a save is
// accepted only if its generation is strictly greater than both the stored
// generation and the tombstone, so no out-of-order or in-flight save resurrects
// cleared marks. Clearing is a tombstone, not a delete, for that reason.

// ErrStaleAnnotationSave marks a save below the generation floor; the client
// drops its pending list and re-hydrates.
var ErrStaleAnnotationSave = errors.New("stale annotation save")

// annotationDraftTable names one draft table and its key column.
type annotationDraftTable struct {
	table string
	key   string
	// Markdown drafts have no note column (a "global" annotation covers it) and
	// read/write the empty string, keeping one query shape for both.
	note bool
}

var (
	markdownDraftTable = annotationDraftTable{table: "markdown_annotation_drafts", key: "path"}
	sessionDraftTable  = annotationDraftTable{table: "session_annotation_drafts", key: "session_id", note: true}
)

// annotationDraft is one stored draft plus the floor a client must exceed.
type annotationDraft struct {
	Annotations string // raw JSON array
	Note        string // always empty for a table without the column
	Generation  int    // max(generation, tombstone_generation)
	UpdatedAt   string
}

// noteColumn is the note column, or a literal empty string on a table without one.
func (t annotationDraftTable) noteColumn() string {
	if t.note {
		return "note"
	}
	return "''"
}

// get returns the draft for key, missing rows included. The generation includes
// any tombstone, so a re-mounting client seeds past a clear.
func (t annotationDraftTable) get(s *Store, key string) (annotationDraft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var annotations, note, updatedAt string
	var generation, tombstone int
	query := fmt.Sprintf(
		"SELECT annotations_json, %s, generation, tombstone_generation, updated_at FROM %s WHERE %s = ?",
		t.noteColumn(), t.table, t.key,
	)
	err := s.db.QueryRow(query, key).Scan(&annotations, &note, &generation, &tombstone, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return annotationDraft{Annotations: "[]", Generation: 0}, nil
	}
	if err != nil {
		return annotationDraft{}, fmt.Errorf("failed to get %s draft: %w", t.table, err)
	}
	return annotationDraft{
		Annotations: annotations,
		Note:        note,
		Generation:  max(generation, tombstone),
		UpdatedAt:   updatedAt,
	}, nil
}

// save upserts the full list, rejecting below the floor with ErrStaleAnnotationSave.
func (t annotationDraftTable) save(s *Store, key, annotationsJSON, note string, generation int, now time.Time) error {
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

	columns, values := "annotations_json", "?"
	updates := "annotations_json = excluded.annotations_json"
	args := []any{key, annotationsJSON}
	if t.note {
		columns, values = columns+", note", values+", ?"
		updates += ", note = excluded.note"
		args = append(args, note)
	}
	args = append(args, generation, now.UTC().Format(time.RFC3339))

	query := fmt.Sprintf(`
		INSERT INTO %s (%s, %s, generation, tombstone_generation, updated_at)
		VALUES (?, %s, ?, 0, ?)
		ON CONFLICT(%s) DO UPDATE SET
			%s,
			generation = excluded.generation,
			updated_at = excluded.updated_at
	`, t.table, t.key, columns, values, t.key, updates)
	if _, err := tx.Exec(query, args...); err != nil {
		return fmt.Errorf("failed to save %s draft: %w", t.table, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit %s save: %w", t.table, err)
	}
	return nil
}

// clear empties the list and raises the tombstone past every save in flight.
// Idempotent, and works on a missing row — the tombstone IS the row.
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

	// Left behind, the note would front the next turn's annotations.
	noteColumns, noteValues, noteUpdates := "", "", ""
	if t.note {
		noteColumns, noteValues, noteUpdates = ", note", ", ''", "note = '',\n\t\t\t"
	}
	query := fmt.Sprintf(`
		INSERT INTO %s (%s, annotations_json%s, generation, tombstone_generation, updated_at)
		VALUES (?, '[]'%s, ?, ?, ?)
		ON CONFLICT(%s) DO UPDATE SET
			annotations_json = '[]',
			%stombstone_generation = excluded.tombstone_generation,
			updated_at = excluded.updated_at
	`, t.table, t.key, noteColumns, noteValues, t.key, noteUpdates)
	if _, err := tx.Exec(query, key, storedGeneration, newTombstone, now.UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("failed to clear %s draft: %w", t.table, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit %s clear: %w", t.table, err)
	}
	return nil
}

// delete removes the row outright, for an owner that is gone for good.
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
