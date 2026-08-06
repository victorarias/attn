package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/docstore"
)

// This is the SQLite persistence for the durable job queue (internal/jobs).
//
// Following the tickets/tasks convention, JobRecord is a store-local row type —
// NOT internal/jobs.Job. The daemon owns the mapping between the two, which
// keeps this package a leaf (internal/store imports neither internal/jobs nor
// internal/daemon).

// sortableTimeFormat is the stored timestamp encoding for the two surfaces in
// this package that keep their stamps in TEXT columns and compare them there:
// the job queue (scheduled_at, created_at, updated_at) and the notifications
// feed (created_at). Text order has to be time order, which takes a fraction of
// a fixed width, always present and always nine digits.
//
// time.RFC3339Nano, which this used to be, does not: it strips trailing zeros,
// so widths vary and "…:00Z" sorts above "…:00.5Z" ('Z' is 0x5A, above '.' and
// every digit). Within one second the two orders disagreed, which made a job
// scheduled on a whole second wait out the rest of that second before
// `scheduled_at <= now` claimed it, and scrambled both feeds' listings among
// rows written in the same second. Migration 93 rewrote the stored stamps.
//
// It is docstore.TimeFormat: the document store hit the same defect and this is
// the same fix, so there is one spelling of it rather than two that must be kept
// in step. Always formatted from a UTC time, so the zone is always "Z" and every
// stored stamp is the same 30 characters wide.
const sortableTimeFormat = docstore.TimeFormat

// rowScanner is satisfied by both *sql.Row and *sql.Rows, so one scan helper
// serves a single-row lookup and a listing. It lives here because the job queue
// is its heaviest user; the notification surface shares it.
type rowScanner interface {
	Scan(dest ...any) error
}

// JobRecord is one durable job row. Payload and Result are carried opaquely as
// JSON text because the store never interprets them — only the handler that was
// enqueued with them does.
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

// execer is satisfied by both *sql.DB and *sql.Tx, so a write helper serves a
// standalone call and a step inside someone else's transaction.
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

// upsertJob is UpsertJob's write, against whichever executor the caller has. The
// legacy-task handover runs it inside its transaction so the old rows are
// deleted in the same commit that writes their replacements.
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

// GetJob returns the row for id. The bool is false (with a nil record and nil
// error) when no such row exists — the queue must tell "no record" apart from a
// read error.
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
// empty key matches nothing: jobs without one are deliberately distinct, so
// treating "" as a key would silently collapse them into a single record.
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

// EligibleJobs returns at most limit jobs claimable at now, ordered the way
// dispatch claims them: highest priority first, then earliest scheduled, then
// oldest. Queued and failed rows are both claimable — a failed row whose backoff
// has elapsed is a retry — and the attempt cap is left to the queue, which knows
// the runner-wide default a row may be relying on.
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

// RecoverRunningJobs resets any job left in "running" back to "queued" with
// scheduled_at = now, returning how many were recovered. A running row at
// startup means a crash interrupted that job mid-run, so it is re-eligible
// immediately.
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

// TrimDoneJobs deletes jobs that completed successfully before cutoff and
// reports how many were removed. Only "done" rows are trimmed: a dead job is the
// record a failure notification points at, and it is not what grows without
// bound.
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

// parseStoreTime decodes a stored timestamp in any RFC3339 form — the
// fixed-width one written today, the trailing-zero-stripped one written before
// migration 93, or a whole second with no fraction at all. A blank/garbage value
// yields the zero time rather than an error: a job with an unreadable timestamp
// is still a real record, and the queue treats a zero scheduled_at as
// "eligible now".
func parseStoreTime(s string) time.Time {
	t, err := docstore.ParseTime(s)
	if err != nil {
		return time.Time{}
	}
	return t
}
