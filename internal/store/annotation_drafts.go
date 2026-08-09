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
// table has the same columns beyond that key, except the note.
type annotationDraftTable struct {
	table string
	key   string
	// Whether the draft carries a note beside its list. Markdown drafts do
	// not: a document-wide comment is already one of their annotations, of
	// type "global". Terminal annotations have no such type, so the note is a
	// column — and a table without it reads and writes the empty string, which
	// keeps one query shape for both.
	note bool
}

var (
	markdownDraftTable = annotationDraftTable{table: "markdown_annotation_drafts", key: "path"}
	sessionDraftTable  = annotationDraftTable{table: "session_annotation_drafts", key: "session_id", note: true}
)

// annotationDraft is one stored draft: the raw list, the note that goes with
// it, plus the generation floor a client must exceed to write.
type annotationDraft struct {
	Annotations string // raw JSON array
	Note        string // always empty for a table without the column
	Generation  int    // max(generation, tombstone_generation)
	UpdatedAt   string
}

// noteColumn is what a SELECT reads for the note: the column on a table that
// has one, the empty string on a table that does not.
func (t annotationDraftTable) noteColumn() string {
	if t.note {
		return "note"
	}
	return "''"
}

// get returns the draft for key. A missing row is an empty draft at generation
// 0, not an error — nothing has been written there yet. The generation returned
// is the floor including any tombstone, so a re-mounting client seeds its
// counter past a clear even when the list is empty.
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

// save upserts the full list for key, rejecting anything that does not clear
// the floor with ErrStaleAnnotationSave.
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

	// The note goes with the marks: it was composed to be sent with them, and
	// leaving it behind after a send would put it in front of the next turn's
	// annotations as if the user had written it there.
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
