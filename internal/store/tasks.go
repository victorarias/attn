package store

import (
	"fmt"
	"time"
)

// The tasks table belonged to internal/tasks, the durable task runner the job
// queue replaced. All that remains of it is the one-time handover below: the
// daemon carries any work still owed onto the jobs table at startup and empties
// this one, so the handover is a no-op on every boot after the first. The table
// itself is dropped in a later migration, once the queue has run for a release.

// LegacyTaskRecord is one row of the retired task-runner table, in the shape the
// handover needs. Meta was an opaque JSON blob then and stays opaque here — the
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

// MigrateLegacyTasks moves every owed task row onto the jobs table and empties
// the old one, and returns how many it moved.
//
// The whole handover is ONE transaction: reading the old rows, writing the
// translated jobs, and deleting the old rows all commit together or not at all.
// That is the entire point of this function's shape. Reading and deleting in one
// transaction and writing the jobs in another looks equivalent and is not — a
// crash between the two, or a single failed job write, would leave the old work
// deleted and the new row never made, losing owed compaction, narration, or
// reconcile work on the one upgrade that exists to preserve it. Any failure here
// rolls back to a table that still holds everything, and the next start tries
// the handover again.
//
// translate turns one legacy row into the job that replaces it. It runs inside
// the transaction and cannot fail: a row it cannot make sense of still becomes a
// job (an unreadable payload is a diagnosable job, a dropped row is lost work),
// so the caller reports what it could not read and returns a record anyway.
//
// Rows in the terminal "done" state are dropped rather than moved. They describe
// work that already happened, nothing re-runs them, and job retention would
// delete them anyway.
func (s *Store) MigrateLegacyTasks(translate func(LegacyTaskRecord) JobRecord) (int, error) {
	if s.db == nil {
		return 0, fmt.Errorf("store: no database")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("store: migrate legacy tasks: %w", err)
	}
	defer tx.Rollback()

	// The rows are collected before anything is written: SQLite will not accept a
	// write on a connection with an open cursor on the same transaction.
	rows, err := tx.Query(
		`SELECT id, kind, subject, state, attempts, next_attempt_at, last_error, meta_json, requeued, created_at, updated_at
		 FROM tasks WHERE state <> 'done'`)
	if err != nil {
		return 0, fmt.Errorf("store: migrate legacy tasks: %w", err)
	}
	var owed []LegacyTaskRecord
	for rows.Next() {
		var (
			rec                             LegacyTaskRecord
			nextStr, createdStr, updatedStr string
			requeued                        int
		)
		if err := rows.Scan(&rec.ID, &rec.Kind, &rec.Subject, &rec.State, &rec.Attempts,
			&nextStr, &rec.LastError, &rec.MetaJSON, &requeued, &createdStr, &updatedStr); err != nil {
			rows.Close()
			return 0, fmt.Errorf("store: scan legacy task: %w", err)
		}
		rec.Requeued = requeued != 0
		rec.NextAttemptAt = parseStoreTime(nextStr)
		rec.CreatedAt = parseStoreTime(createdStr)
		rec.UpdatedAt = parseStoreTime(updatedStr)
		owed = append(owed, rec)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("store: migrate legacy tasks: %w", err)
	}

	for _, rec := range owed {
		if err := upsertJob(tx, translate(rec)); err != nil {
			return 0, fmt.Errorf("store: migrate legacy task %s: %w", rec.ID, err)
		}
	}
	if _, err := tx.Exec(`DELETE FROM tasks`); err != nil {
		return 0, fmt.Errorf("store: clear legacy tasks: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: commit legacy task handover: %w", err)
	}
	return len(owed), nil
}
