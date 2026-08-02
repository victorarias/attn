package store

import (
	"fmt"
	"time"
)

// The tasks table belonged to internal/tasks, the durable task runner the job
// queue replaced. All that remains of it is the one-time drain below: the daemon
// carries any work still owed onto the jobs table at startup and empties this
// one, so the drain is a no-op on every boot after the first. The table itself is
// dropped in a later migration, once the queue has run for a release.

// LegacyTaskRecord is one row of the retired task-runner table, in the shape the
// import needs. Meta was an opaque JSON blob then and stays opaque here — the
// daemon owns turning it into a job payload, because only the kind that stashed
// it knows what it means.
type LegacyTaskRecord struct {
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

// DrainLegacyTasks returns every remaining task row and empties the table in one
// transaction, so the rows are handed over exactly once even if the import that
// follows fails partway: a crash before the commit leaves them to be drained
// again, and a crash after it leaves nothing to re-import twice.
//
// Rows in the terminal "done" state are dropped rather than returned. They
// describe work that already happened, nothing re-runs them, and job retention
// would delete them anyway.
func (s *Store) DrainLegacyTasks() ([]LegacyTaskRecord, error) {
	if s.db == nil {
		return nil, fmt.Errorf("store: no database")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: drain legacy tasks: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.Query(
		`SELECT id, kind, subject, state, attempts, next_attempt_at, last_error, meta_json, requeued, created_at, updated_at
		 FROM tasks WHERE state <> 'done'`)
	if err != nil {
		return nil, fmt.Errorf("store: drain legacy tasks: %w", err)
	}
	var out []LegacyTaskRecord
	for rows.Next() {
		var (
			rec                             LegacyTaskRecord
			nextStr, createdStr, updatedStr string
			requeued                        int
		)
		if err := rows.Scan(&rec.ID, &rec.Kind, &rec.Subject, &rec.State, &rec.Attempts,
			&nextStr, &rec.LastError, &rec.MetaJSON, &requeued, &createdStr, &updatedStr); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: scan legacy task: %w", err)
		}
		rec.Requeued = requeued != 0
		rec.NextAttemptAt = parseJobTime(nextStr)
		rec.CreatedAt = parseJobTime(createdStr)
		rec.UpdatedAt = parseJobTime(updatedStr)
		out = append(out, rec)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: drain legacy tasks: %w", err)
	}

	if _, err := tx.Exec(`DELETE FROM tasks`); err != nil {
		return nil, fmt.Errorf("store: clear legacy tasks: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: commit legacy task drain: %w", err)
	}
	return out, nil
}
