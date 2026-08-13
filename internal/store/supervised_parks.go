package store

import (
	"database/sql"
	"time"
)

// Parked supervised children (migration 104).
//
// internal/supervise parks a child that keeps dying without ever running
// stably: it stops restarting it, and only a deliberate act brings it back. That
// decision lives in the supervisor's memory, which ends with the daemon process
// — so a restart used to forget it, spawn the still-broken child, and burn the
// whole crash loop again before parking it a second time. This table is that
// decision made durable.
//
// One row per supervised child name, written when the give-up happens and
// deleted by whatever revives it. No row means no park: absence is the normal
// state, not a missing record.
//
// The shared app runtime is the only writer today. A supervised plugin that
// keeps dying is reinstalled rather than un-parked, so persisting its give-up
// would be a record nothing reads; the table is keyed by child name so a second
// consumer can join without a schema change, not because one is planned.

// SupervisedPark is one child's parking, as it survives a restart. Everything
// after Child mirrors what the supervisor's snapshot reports, so a restored park
// answers `attn app runtime status` exactly as the original did — the park's own
// timestamp most of all, which must not become the moment it was restored.
type SupervisedPark struct {
	Child          string
	ParkedAt       time.Time
	RestartAttempt int
	ExitAt         time.Time
	ExitCode       *int
	ExitSignal     string
	ExitError      string
}

// SaveSupervisedPark records a park, replacing any earlier one for that child.
func (s *Store) SaveSupervisedPark(park SupervisedPark) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	exitAt := ""
	if !park.ExitAt.IsZero() {
		exitAt = park.ExitAt.UTC().Format(sortableTimeFormat)
	}
	var exitCode any
	if park.ExitCode != nil {
		exitCode = *park.ExitCode
	}
	_, err := s.db.Exec(`
		INSERT INTO supervised_parks (child, parked_at, restart_attempt, exit_at, exit_code, exit_signal, exit_error)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(child) DO UPDATE SET
			parked_at = excluded.parked_at,
			restart_attempt = excluded.restart_attempt,
			exit_at = excluded.exit_at,
			exit_code = excluded.exit_code,
			exit_signal = excluded.exit_signal,
			exit_error = excluded.exit_error
	`, park.Child, park.ParkedAt.UTC().Format(sortableTimeFormat), park.RestartAttempt,
		exitAt, exitCode, park.ExitSignal, park.ExitError)
	return err
}

// GetSupervisedPark loads one child's park. The false return is the ordinary
// answer for a healthy child.
func (s *Store) GetSupervisedPark(child string) (SupervisedPark, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return SupervisedPark{}, false, nil
	}
	var (
		park     SupervisedPark
		parkedAt string
		exitAt   string
		exitCode sql.NullInt64
	)
	err := s.db.QueryRow(`
		SELECT child, parked_at, restart_attempt, exit_at, exit_code, exit_signal, exit_error
		FROM supervised_parks WHERE child = ?
	`, child).Scan(&park.Child, &parkedAt, &park.RestartAttempt, &exitAt, &exitCode,
		&park.ExitSignal, &park.ExitError)
	switch err {
	case nil:
	case sql.ErrNoRows:
		return SupervisedPark{}, false, nil
	default:
		return SupervisedPark{}, false, err
	}
	park.ParkedAt = parseStoreTime(parkedAt)
	if exitAt != "" {
		park.ExitAt = parseStoreTime(exitAt)
	}
	if exitCode.Valid {
		code := int(exitCode.Int64)
		park.ExitCode = &code
	}
	return park, true, nil
}

// ClearSupervisedPark deletes a child's park and reports whether there was one.
// Every way out of parked calls it: a park the supervisor has already left but
// the database still remembers is the same bug this table exists to fix, only
// pointing the other way.
func (s *Store) ClearSupervisedPark(child string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return false, nil
	}
	result, err := s.db.Exec(`DELETE FROM supervised_parks WHERE child = ?`, child)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}
