package store

import (
	"database/sql"
	"fmt"
	"time"
)

// This is the SQLite persistence for the durable task runner (internal/tasks).
// The runner used to keep one atomic-JSON file per task under the notebook root;
// it now persists here in the profile DB so background work no longer depends on a
// notebook root and so future surfaces (notifications) can query it. See
// docs/plans/2026-07-02-bg-task-notifications.md.
//
// Following the tickets/workflow_runs convention, TaskRecord is a store-local row
// type — NOT the internal/tasks.Task type. The daemon owns the mapping between the
// two, which keeps this package a leaf (internal/store imports neither
// internal/tasks nor internal/daemon).

// taskTimeFormat is the on-disk timestamp encoding. RFC3339Nano preserves the
// sub-second precision the runner's backoff/coalescing timing relies on.
const taskTimeFormat = time.RFC3339Nano

// TaskRecord is one durable task-runner record. Meta is carried opaquely as a
// JSON blob (MetaJSON) because the store never interprets it — only the executor
// that stashed it does.
type TaskRecord struct {
	ID            string
	Kind          string
	Subject       string
	State         string
	Attempts      int
	NextAttemptAt time.Time
	LastError     string
	MetaJSON      string
	Requeued      bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// UpsertTask inserts or fully replaces a task row by id. The runner addresses a
// record only by its stable id (kind:subject), so a re-enqueue overwrites the same
// row — the same coalescing the file store got from same-filename overwrite.
func (s *Store) UpsertTask(rec TaskRecord) error {
	if s.db == nil {
		return fmt.Errorf("store: no database")
	}
	_, err := s.db.Exec(
		`INSERT INTO tasks (id, kind, subject, state, attempts, next_attempt_at, last_error, meta_json, requeued, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   kind=excluded.kind,
		   subject=excluded.subject,
		   state=excluded.state,
		   attempts=excluded.attempts,
		   next_attempt_at=excluded.next_attempt_at,
		   last_error=excluded.last_error,
		   meta_json=excluded.meta_json,
		   requeued=excluded.requeued,
		   created_at=excluded.created_at,
		   updated_at=excluded.updated_at`,
		rec.ID, rec.Kind, rec.Subject, rec.State, rec.Attempts,
		rec.NextAttemptAt.UTC().Format(taskTimeFormat), rec.LastError, rec.MetaJSON,
		boolToInt(rec.Requeued),
		rec.CreatedAt.UTC().Format(taskTimeFormat), rec.UpdatedAt.UTC().Format(taskTimeFormat),
	)
	if err != nil {
		return fmt.Errorf("store: upsert task %s: %w", rec.ID, err)
	}
	return nil
}

// GetTask returns the row for id. The bool is false (with a nil record and nil
// error) when no such row exists — the runner's coalesced re-enqueue must tell
// "no record yet" apart from a read error.
func (s *Store) GetTask(id string) (*TaskRecord, bool, error) {
	if s.db == nil {
		return nil, false, fmt.Errorf("store: no database")
	}
	row := s.db.QueryRow(
		`SELECT id, kind, subject, state, attempts, next_attempt_at, last_error, meta_json, requeued, created_at, updated_at
		 FROM tasks WHERE id = ?`, id)
	rec, err := scanTaskRow(row)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("store: get task %s: %w", id, err)
	}
	return rec, true, nil
}

// DeleteTask removes a task row. A missing row is not an error (already gone).
func (s *Store) DeleteTask(id string) error {
	if s.db == nil {
		return fmt.Errorf("store: no database")
	}
	if _, err := s.db.Exec(`DELETE FROM tasks WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: delete task %s: %w", id, err)
	}
	return nil
}

// ListTasks returns every task row, newest-updated first (the contract the task
// status surface documents).
func (s *Store) ListTasks() ([]TaskRecord, error) {
	if s.db == nil {
		return nil, fmt.Errorf("store: no database")
	}
	rows, err := s.db.Query(
		`SELECT id, kind, subject, state, attempts, next_attempt_at, last_error, meta_json, requeued, created_at, updated_at
		 FROM tasks ORDER BY updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("store: list tasks: %w", err)
	}
	defer rows.Close()
	var out []TaskRecord
	for rows.Next() {
		rec, err := scanTaskRow(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan task: %w", err)
		}
		out = append(out, *rec)
	}
	return out, rows.Err()
}

// RecoverRunningTasks resets any task left in "running" back to "queued" with
// next_attempt_at = now, returning how many were recovered. A row in "running" at
// startup means a crash interrupted that task mid-run, so it is re-eligible
// immediately (the same recovery the file store did by rewriting each record).
func (s *Store) RecoverRunningTasks(now time.Time) (int, error) {
	if s.db == nil {
		return 0, fmt.Errorf("store: no database")
	}
	ts := now.UTC().Format(taskTimeFormat)
	res, err := s.db.Exec(
		`UPDATE tasks SET state='queued', next_attempt_at=?, updated_at=? WHERE state='running'`, ts, ts)
	if err != nil {
		return 0, fmt.Errorf("store: recover running tasks: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows so scanTaskRow serves
// GetTask and ListTasks.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanTaskRow(sc rowScanner) (*TaskRecord, error) {
	var (
		rec               TaskRecord
		nextStr, createdStr, updatedStr string
		requeued          int
	)
	if err := sc.Scan(&rec.ID, &rec.Kind, &rec.Subject, &rec.State, &rec.Attempts,
		&nextStr, &rec.LastError, &rec.MetaJSON, &requeued, &createdStr, &updatedStr); err != nil {
		return nil, err
	}
	rec.Requeued = requeued != 0
	rec.NextAttemptAt = parseTaskTime(nextStr)
	rec.CreatedAt = parseTaskTime(createdStr)
	rec.UpdatedAt = parseTaskTime(updatedStr)
	return &rec, nil
}

// boolToInt lives in store.go (shared across this package).

// parseTaskTime decodes a stored timestamp, tolerating the plain RFC3339 form as
// well as RFC3339Nano. A blank/garbage value yields the zero time rather than an
// error — a task with an unreadable timestamp is still a real record and the
// runner treats a zero NextAttemptAt as "eligible now".
func parseTaskTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(taskTimeFormat, s); err == nil {
		return t.UTC()
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC()
	}
	return time.Time{}
}
