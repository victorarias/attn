package store

import (
	"database/sql"
	"fmt"
	"time"
)

const (
	AppReconcileGap           = "gap"
	AppReconcileReEnabled     = "re_enabled"
	AppReconcileVersionChange = "version_changed"
)

// AppReconcileRequest is one durable reason an app must rebuild its derived
// collections before it receives another fact.
type AppReconcileRequest struct {
	ID                int64
	AppName           string
	Reason            string
	VersionID         int64
	ThroughSeq        int64
	PreviousVersionID int64
	Cursor            int64
	Earliest          int64
	Missed            int64
	CreatedAt         time.Time
}

// AppReconcileClaim is the immutable boundary one reconcile attempt may
// complete. Requests written after ThroughRequestID remain owed.
type AppReconcileClaim struct {
	Requests         []AppReconcileRequest
	ThroughRequestID int64
	ThroughSeq       int64
}

type reconcileQueryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// AppReconcilePending returns every request above the app's completed boundary.
func (s *Store) AppReconcilePending(name string) (AppReconcileClaim, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return AppReconcileClaim{}, nil
	}
	return appReconcilePending(s.db, name)
}

func appReconcilePending(q reconcileQueryer, name string) (AppReconcileClaim, error) {
	rows, err := q.Query(`
		SELECT id, app_name, reason, version_id, through_seq,
		       previous_version_id, cursor, earliest, missed, created_at
		FROM app_reconcile_requests
		WHERE app_name = ? AND id > COALESCE((
			SELECT completed_request_id FROM app_reconcile_progress WHERE app_name = ?
		), 0)
		ORDER BY id ASC
	`, name, name)
	if err != nil {
		return AppReconcileClaim{}, err
	}
	defer rows.Close()
	var claim AppReconcileClaim
	for rows.Next() {
		var (
			r                             AppReconcileRequest
			previous, cursor, low, missed sql.NullInt64
			created                       string
		)
		if err := rows.Scan(&r.ID, &r.AppName, &r.Reason, &r.VersionID, &r.ThroughSeq,
			&previous, &cursor, &low, &missed, &created); err != nil {
			return AppReconcileClaim{}, err
		}
		r.PreviousVersionID = previous.Int64
		r.Cursor = cursor.Int64
		r.Earliest = low.Int64
		r.Missed = missed.Int64
		r.CreatedAt = parseStoreTime(created)
		claim.Requests = append(claim.Requests, r)
		claim.ThroughRequestID = r.ID
		if r.ThroughSeq > claim.ThroughSeq {
			claim.ThroughSeq = r.ThroughSeq
		}
	}
	return claim, rows.Err()
}

// RequestAppReconcileGap records a retention gap. Retrying the same pre-drain
// hook is idempotent; a different cursor or surviving window is a new trigger.
func (s *Store) RequestAppReconcileGap(name string, cursor, earliest, throughSeq int64, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return false, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var versionID int64
	if err := tx.QueryRow(`SELECT current_version_id FROM apps WHERE name = ?`, name).Scan(&versionID); err != nil {
		return false, err
	}
	res, err := tx.Exec(`
		INSERT OR IGNORE INTO app_reconcile_requests
			(app_name, reason, version_id, through_seq, cursor, earliest, missed, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, name, AppReconcileGap, versionID, throughSeq, cursor, earliest,
		earliest-1-cursor, now.UTC().Format(sortableTimeFormat))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, tx.Commit()
}

// CompleteAppReconcile advances the completed request boundary and the app's
// bus cursor together. A trigger written after the claim stays pending.
func (s *Store) CompleteAppReconcile(name string, throughRequestID, throughSeq int64, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var exists int
	if err := tx.QueryRow(`
		SELECT COUNT(*) FROM app_reconcile_requests WHERE app_name = ? AND id = ?
	`, name, throughRequestID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return fmt.Errorf("store: app %q has no reconcile request %d to complete", name, throughRequestID)
	}
	stamp := now.UTC().Format(sortableTimeFormat)
	if _, err := tx.Exec(`
		INSERT INTO app_reconcile_progress (app_name, completed_request_id, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(app_name) DO UPDATE SET
			completed_request_id = MAX(completed_request_id, excluded.completed_request_id),
			updated_at = excluded.updated_at
	`, name, throughRequestID, stamp); err != nil {
		return err
	}
	res, err := tx.Exec(`
		UPDATE bus_consumers
		SET cursor = MAX(cursor, ?), updated_at = ?
		WHERE name = ?
	`, throughSeq, stamp, "app:"+name)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("store: app %q has no bus consumer whose cursor can cross reconcile fence %d", name, throughSeq)
	}
	return tx.Commit()
}

func appendAppReconcileRequest(tx *sql.Tx, name, reason string, versionID, throughSeq, previousVersionID int64, now time.Time) error {
	var previous any
	if previousVersionID != 0 {
		previous = previousVersionID
	}
	_, err := tx.Exec(`
		INSERT INTO app_reconcile_requests
			(app_name, reason, version_id, through_seq, previous_version_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, name, reason, versionID, throughSeq, previous, now.UTC().Format(sortableTimeFormat))
	return err
}

func busHeadWith(q reconcileQueryer) (int64, error) {
	var head sql.NullInt64
	err := q.QueryRow(`SELECT MAX(seq) FROM bus_events`).Scan(&head)
	return head.Int64, err
}
