package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/apps"
)

const (
	AppReconcileGap           = "gap"
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
	return appReconcilePendingThrough(s.db, name, 0)
}

// AppReconcilePendingThrough returns the still-owed prefix ending at requestID.
// It is how a failed/interrupted attempt retries the exact claim it started,
// leaving a trigger that arrived later for the next invocation.
func (s *Store) AppReconcilePendingThrough(name string, requestID int64) (AppReconcileClaim, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return AppReconcileClaim{}, nil
	}
	if requestID <= 0 {
		return AppReconcileClaim{}, fmt.Errorf("store: a reconcile claim boundary must be positive (got %d)", requestID)
	}
	return appReconcilePendingThrough(s.db, name, requestID)
}

func appReconcilePendingThrough(q reconcileQueryer, name string, throughRequestID int64) (AppReconcileClaim, error) {
	rows, err := q.Query(`
		SELECT id, app_name, reason, version_id, through_seq,
		       previous_version_id, cursor, earliest, missed, created_at
		FROM app_reconcile_requests
		WHERE app_name = ? AND id > COALESCE((
			SELECT completed_request_id FROM app_reconcile_progress WHERE app_name = ?
		), 0) AND (? = 0 OR id <= ?)
		ORDER BY id ASC
	`, name, name, throughRequestID, throughRequestID)
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
func (s *Store) RequestAppReconcileGap(name string, cursor, earliest int64, now time.Time) (bool, error) {
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
	throughSeq := earliest - 1
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
	if err := completeAppReconcileTx(tx, name, throughRequestID, throughSeq, now); err != nil {
		return err
	}
	return tx.Commit()
}

// CompleteAppReconcileInvocation settles a successful running attempt and
// crosses its durable request/cursor fence in one transaction. A crash can
// therefore leave either the whole attempt owed or the whole attempt complete,
// never an ok row whose request still retries or a completed request startup
// later labels interrupted.
func (s *Store) CompleteAppReconcileInvocation(name string, invocationID, throughRequestID, throughSeq int64, now time.Time) error {
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

	var (
		appName                    string
		kind                       string
		status                     string
		startedText                string
		storedRequestID, storedSeq sql.NullInt64
	)
	err = tx.QueryRow(`
		SELECT app_name, kind, status, started_at, through_request_id, through_seq
		FROM app_invocations WHERE id = ?
	`, invocationID).Scan(&appName, &kind, &status, &startedText, &storedRequestID, &storedSeq)
	switch {
	case err == sql.ErrNoRows:
		return fmt.Errorf("store: no app invocation %d to complete", invocationID)
	case err != nil:
		return err
	case appName != name:
		return fmt.Errorf("store: app invocation %d belongs to app %q, not %q", invocationID, appName, name)
	case kind != AppInvocationKindReconcile:
		return fmt.Errorf("store: app invocation %d is kind %q, not reconcile", invocationID, kind)
	case status != AppInvocationStatusRunning:
		return fmt.Errorf("store: reconcile invocation %d is %q, not running; its claim was not completed", invocationID, status)
	case !storedRequestID.Valid || storedRequestID.Int64 != throughRequestID || !storedSeq.Valid || storedSeq.Int64 != throughSeq:
		return fmt.Errorf(
			"store: reconcile invocation %d owns claim request %d through seq %d, not request %d through seq %d",
			invocationID, storedRequestID.Int64, storedSeq.Int64, throughRequestID, throughSeq)
	}
	duration := now.Sub(parseStoreTime(startedText))
	if duration < 0 {
		duration = 0
	}
	res, err := tx.Exec(`
		UPDATE app_invocations
		SET status = ?, error = '', duration_ms = ?, finished_at = ?
		WHERE id = ? AND status = ?
	`, AppInvocationStatusOK, duration.Milliseconds(), now.UTC().Format(sortableTimeFormat), invocationID, AppInvocationStatusRunning)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return fmt.Errorf("store: reconcile invocation %d stopped running before its claim could complete", invocationID)
	}
	if err := completeAppReconcileTx(tx, name, throughRequestID, throughSeq, now); err != nil {
		return err
	}
	return tx.Commit()
}

func completeAppReconcileTx(tx *sql.Tx, name string, throughRequestID, throughSeq int64, now time.Time) error {
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
	return nil
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

// appConsumerCursorWith reports the app's fact-delivery position, and whether
// facts reach it at all. Every app carries a consumer row — the enabled bit lives
// nowhere else — so a view-or-command-only app is recognised by the sentinel
// filter it was registered with rather than by the row's absence.
//
// Such an app derives nothing from facts, so a version move invalidates no
// derived state and owes no rebuild. Recording one anyway would refuse the app's
// commands until a reconcile ran, and the pre-drain that runs on every poll tick
// would disable a view-only app for not declaring a handler it has no use for.
func appConsumerCursorWith(q reconcileQueryer, name string) (int64, bool, error) {
	var (
		cursor int64
		filter string
	)
	err := q.QueryRow(`SELECT cursor, filter FROM bus_consumers WHERE name = ?`, "app:"+name).Scan(&cursor, &filter)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return cursor, filter != apps.NoSubscriptionsPattern, nil
}
