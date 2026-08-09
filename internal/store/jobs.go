package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/docstore"
)

// SQLite persistence for the durable job queue (internal/jobs). JobRecord is a
// store-local row type, not internal/jobs.Job; the daemon owns the mapping so
// this package stays a leaf.

// sortableTimeFormat encodes stamps kept in TEXT columns and compared there
// (jobs, notifications feed): text order must be time order, so the fraction is
// fixed-width. RFC3339Nano strips trailing zeros and broke that — "…:00Z" sorts
// above "…:00.5Z" — delaying whole-second scheduled_at claims; migration 94
// rewrote the stored stamps. Always format from a UTC time.
const sortableTimeFormat = docstore.TimeFormat

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// JobRecord is one durable job row. Payload and Result are opaque JSON text;
// only the enqueued handler interprets them.
type JobRecord struct {
	ID          string
	Kind        string
	UniqueKey   string
	Priority    int
	Payload     string
	Result      string
	State       string
	Attempts    int
	MaxAttempts int
	ScheduledAt time.Time
	LastError   string
	Requeued    bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

const jobColumns = `id, kind, unique_key, priority, payload, result, state, attempts,
	max_attempts, scheduled_at, last_error, requeued, created_at, updated_at`

// execer is satisfied by both *sql.DB and *sql.Tx.
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// UpsertJob inserts or fully replaces a job row by id.
func (s *Store) UpsertJob(rec JobRecord) error {
	if s.db == nil {
		return fmt.Errorf("store: no database")
	}
	return upsertJob(s.db, rec)
}

// upsertJob is UpsertJob's write, against whichever executor the caller has.
func upsertJob(ex execer, rec JobRecord) error {
	_, err := ex.Exec(
		`INSERT INTO jobs (`+jobColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   kind=excluded.kind,
		   unique_key=excluded.unique_key,
		   priority=excluded.priority,
		   payload=excluded.payload,
		   result=excluded.result,
		   state=excluded.state,
		   attempts=excluded.attempts,
		   max_attempts=excluded.max_attempts,
		   scheduled_at=excluded.scheduled_at,
		   last_error=excluded.last_error,
		   requeued=excluded.requeued,
		   created_at=excluded.created_at,
		   updated_at=excluded.updated_at`,
		rec.ID, rec.Kind, rec.UniqueKey, rec.Priority, rec.Payload, rec.Result, rec.State,
		rec.Attempts, rec.MaxAttempts, rec.ScheduledAt.UTC().Format(sortableTimeFormat),
		rec.LastError, boolToInt(rec.Requeued),
		rec.CreatedAt.UTC().Format(sortableTimeFormat), rec.UpdatedAt.UTC().Format(sortableTimeFormat),
	)
	if err != nil {
		return fmt.Errorf("store: upsert job %s: %w", rec.ID, err)
	}
	return nil
}

// GetJob returns the row for id; the bool is false with a nil error when no
// such row exists.
func (s *Store) GetJob(id string) (*JobRecord, bool, error) {
	if s.db == nil {
		return nil, false, fmt.Errorf("store: no database")
	}
	row := s.db.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id = ?`, id)
	rec, err := scanJobRow(row)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("store: get job %s: %w", id, err)
	}
	return rec, true, nil
}

// GetJobByUniqueKey returns the row a kind coalesces onto for uniqueKey. An
// empty key matches nothing — treating "" as a key would collapse distinct jobs.
func (s *Store) GetJobByUniqueKey(kind, uniqueKey string) (*JobRecord, bool, error) {
	if s.db == nil {
		return nil, false, fmt.Errorf("store: no database")
	}
	if uniqueKey == "" {
		return nil, false, nil
	}
	row := s.db.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE kind = ? AND unique_key = ?`, kind, uniqueKey)
	rec, err := scanJobRow(row)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("store: get job %s/%s: %w", kind, uniqueKey, err)
	}
	return rec, true, nil
}

// DeleteJob removes a job row. A missing row is not an error (already gone).
func (s *Store) DeleteJob(id string) error {
	if s.db == nil {
		return fmt.Errorf("store: no database")
	}
	if _, err := s.db.Exec(`DELETE FROM jobs WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: delete job %s: %w", id, err)
	}
	return nil
}

// ListJobs returns every job row, newest-updated first.
func (s *Store) ListJobs() ([]JobRecord, error) {
	if s.db == nil {
		return nil, fmt.Errorf("store: no database")
	}
	rows, err := s.db.Query(`SELECT ` + jobColumns + ` FROM jobs ORDER BY updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("store: list jobs: %w", err)
	}
	return collectJobRows(rows, "list jobs")
}

// EligibleJobs returns at most limit jobs claimable at now, in dispatch order:
// priority desc, scheduled asc, created asc. Queued and failed rows are both
// claimable (an elapsed-backoff failure is a retry); the attempt cap is the
// queue's job.
func (s *Store) EligibleJobs(now time.Time, limit int) ([]JobRecord, error) {
	if s.db == nil {
		return nil, fmt.Errorf("store: no database")
	}
	rows, err := s.db.Query(
		`SELECT `+jobColumns+` FROM jobs
		 WHERE state IN ('queued', 'failed') AND scheduled_at <= ?
		 ORDER BY priority DESC, scheduled_at ASC, created_at ASC
		 LIMIT ?`,
		now.UTC().Format(sortableTimeFormat), limit)
	if err != nil {
		return nil, fmt.Errorf("store: eligible jobs: %w", err)
	}
	return collectJobRows(rows, "eligible jobs")
}

// RecoverRunningJobs resets jobs left in "running" back to "queued" at now: a
// running row at startup means a crash interrupted it mid-run.
func (s *Store) RecoverRunningJobs(now time.Time) (int, error) {
	if s.db == nil {
		return 0, fmt.Errorf("store: no database")
	}
	ts := now.UTC().Format(sortableTimeFormat)
	res, err := s.db.Exec(
		`UPDATE jobs SET state='queued', scheduled_at=?, updated_at=? WHERE state='running'`, ts, ts)
	if err != nil {
		return 0, fmt.Errorf("store: recover running jobs: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// TrimDoneJobs deletes "done" rows older than cutoff. Dead jobs stay: a failure
// notification points at them.
func (s *Store) TrimDoneJobs(cutoff time.Time) (int, error) {
	if s.db == nil {
		return 0, fmt.Errorf("store: no database")
	}
	res, err := s.db.Exec(
		`DELETE FROM jobs WHERE state='done' AND updated_at < ?`, cutoff.UTC().Format(sortableTimeFormat))
	if err != nil {
		return 0, fmt.Errorf("store: trim done jobs: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

func collectJobRows(rows *sql.Rows, what string) ([]JobRecord, error) {
	defer rows.Close()
	var out []JobRecord
	for rows.Next() {
		rec, err := scanJobRow(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan job (%s): %w", what, err)
		}
		out = append(out, *rec)
	}
	return out, rows.Err()
}

func scanJobRow(sc rowScanner) (*JobRecord, error) {
	var (
		rec                                 JobRecord
		scheduledStr, createdStr, updateStr string
		requeued                            int
	)
	if err := sc.Scan(&rec.ID, &rec.Kind, &rec.UniqueKey, &rec.Priority, &rec.Payload,
		&rec.Result, &rec.State, &rec.Attempts, &rec.MaxAttempts, &scheduledStr,
		&rec.LastError, &requeued, &createdStr, &updateStr); err != nil {
		return nil, err
	}
	rec.Requeued = requeued != 0
	rec.ScheduledAt = parseStoreTime(scheduledStr)
	rec.CreatedAt = parseStoreTime(createdStr)
	rec.UpdatedAt = parseStoreTime(updateStr)
	return &rec, nil
}

// parseStoreTime decodes any RFC3339 form (pre-migration-94 stamps included).
// Garbage yields the zero time, which the queue treats as "eligible now".
func parseStoreTime(s string) time.Time {
	t, err := docstore.ParseTime(s)
	if err != nil {
		return time.Time{}
	}
	return t
}
