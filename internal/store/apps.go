package store

import (
	"database/sql"
	"fmt"
	"time"
)

// The app registry's persistence (migrations 102 and 105). An app is a
// manifest-declared, bus-consuming automation running in the shared runtime;
// this file is the four tables it lives in and nothing else — the lifecycle that
// stops a delivery loop and the pipeline that builds an artifact are the
// daemon's.
//
// Two absences carry meaning and are load-bearing here:
//
//   - There is no enabled bit. An app's enabled state is its bus consumer's
//     (`app:<name>`), because that one bit both stops delivery and releases the
//     retention floor. Nothing in this file reads or writes it.
//   - Removing an app removes its registry row and nothing else. Versions,
//     invocations and serving steps are history, and the documents under
//     `app/<name>` are the user's data; deleting either is a separate, explicit
//     act that does not exist yet, deliberately.
//
// See docs/plans/2026-08-06-ext-a4-app-registry-and-runtime.md.

// App is one registered app. CurrentVersionID is 0 until a version has been
// applied — a registry row can exist ahead of its first successful build.
//
// PreviousServingVersionID is the version one step back along the app's serving
// chain: what bare `attn app rollback` lands on. It is derived from
// app_serving_steps at read time rather than stored, so it always agrees with
// the chain, and it is 0 when the app sits at the bottom of its chain.
type App struct {
	Name                     string
	CurrentVersionID         int64
	PreviousServingVersionID int64
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

// appColumnsSQL reads a registry row and derives the version one step back along
// its serving chain — the current step's parent. Both readers share it so a bare
// rollback's target is computed one way.
const appColumnsSQL = `
	SELECT a.name, a.current_version_id,
	       (SELECT p.version_id FROM app_serving_steps c
	          JOIN app_serving_steps p ON p.id = c.parent_id
	         WHERE c.id = a.serving_step_id),
	       a.created_at, a.updated_at
	FROM apps a`

// pushServingStep points an app at a version and records the step it took to get
// there, inside the caller's transaction. Every path that moves the pointer
// forward — an apply, a rollback onto a named version — goes through it, so
// there is no way to move the pointer without extending the chain.
//
// The new step's parent is the step the app was on, which is what makes the
// chain walkable: bare rollback follows parents down, and applying while already
// walked back parents the new step where the walk stopped, so the way back from
// it is the version that was actually serving. Nothing above is deleted; a step
// nothing points at is simply unreachable.
//
// Landing on the version already current is not a move — re-applying
// byte-identical content does exactly that — so it pushes nothing and leaves the
// chain, and the way back, alone.
func pushServingStep(tx *sql.Tx, name string, versionID int64, stamp string) error {
	var current, cursor sql.NullInt64
	err := tx.QueryRow(`SELECT current_version_id, serving_step_id FROM apps WHERE name = ?`, name).
		Scan(&current, &cursor)
	if err == sql.ErrNoRows {
		return fmt.Errorf("store: no app named %q", name)
	}
	if err != nil {
		return err
	}
	if current.Valid && current.Int64 == versionID {
		_, err = tx.Exec(`UPDATE apps SET updated_at = ? WHERE name = ?`, stamp, name)
		return err
	}
	res, err := tx.Exec(`
		INSERT INTO app_serving_steps (app_name, version_id, parent_id, created_at)
		VALUES (?, ?, ?, ?)
	`, name, versionID, cursor, stamp)
	if err != nil {
		return err
	}
	step, err := res.LastInsertId()
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
		UPDATE apps SET current_version_id = ?, serving_step_id = ?, updated_at = ? WHERE name = ?
	`, versionID, step, stamp, name)
	return err
}

// AppVersion is an immutable record of one built artifact. ContentHash is the
// version's identity: applying byte-identical content again reuses this row
// rather than minting a second one, which is what keeps the invocation log's
// "which version actually ran" answerable after a long dev session.
//
// Declaration is the manifest's frozen snapshot as JSON text, stored rather than
// re-read from disk so what an old version declared survives editing the
// manifest — the automation_runs pattern.
type AppVersion struct {
	ID           int64
	AppName      string
	ContentHash  string
	Declaration  string
	ArtifactPath string
	CreatedAt    time.Time
}

// AppInvocation is one handler run. VersionID is what actually ran, not the
// pointer the app is on now, so a rollback does not rewrite history.
//
// The writer is the runtime (a later stage); this file gives it the append and
// the read `attn app status` uses. Retention arrives with that writer: an age
// window has to be measured against real invocation rates, and a number written
// before there is anything to measure would be a limit with no receipt.
type AppInvocation struct {
	ID           int64
	AppName      string
	VersionID    int64
	EventSeq     int64
	EventName    string
	EventSubject string
	Handler      string
	Status       string
	Error        string
	Duration     time.Duration
	StartedAt    time.Time
}

// SaveApp creates a registry row, or touches an existing one. It never moves
// current_version_id: the pointer moves only through CommitAppVersion and
// SetAppCurrentVersion, so no caller can flip an app onto a version by accident.
func (s *Store) SaveApp(name string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	stamp := now.UTC().Format(sortableTimeFormat)
	_, err := s.db.Exec(`
		INSERT INTO apps (name, current_version_id, created_at, updated_at)
		VALUES (?, NULL, ?, ?)
		ON CONFLICT(name) DO UPDATE SET updated_at = excluded.updated_at
	`, name, stamp, stamp)
	return err
}

// GetApp loads one registry row.
func (s *Store) GetApp(name string) (App, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return App{}, false, nil
	}
	app, err := scanApp(s.db.QueryRow(appColumnsSQL+` WHERE a.name = ?`, name))
	switch err {
	case nil:
		return app, true, nil
	case sql.ErrNoRows:
		return App{}, false, nil
	default:
		return App{}, false, err
	}
}

// ListApps returns every registered app, by name.
func (s *Store) ListApps() ([]App, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(appColumnsSQL + ` ORDER BY a.name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []App
	for rows.Next() {
		app, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, app)
	}
	return out, rows.Err()
}

// DeleteApp removes a registry row and reports whether one was there. Versions,
// invocations and serving steps are untouched: they are the record of what ran,
// and an uninstall does not rewrite history. Registering the name again starts a
// fresh chain — the cursor went with the row, so the old steps are unreachable.
func (s *Store) DeleteApp(name string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return false, nil
	}
	res, err := s.db.Exec(`DELETE FROM apps WHERE name = ?`, name)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// CommitAppVersion records a built version and points the app at it, in one
// transaction. It is the whole of what apply changes: everything before it —
// parse, codegen, typecheck, build — either produced this artifact or failed
// having changed nothing.
//
// A version already recorded for this app under the same content hash is reused
// rather than duplicated, and the returned row is that existing one. Two
// consequences worth naming: a dev loop that rebuilds identical output leaves
// one row, and re-applying an old build is indistinguishable from rolling back
// to it, which is correct — they are the same artifact.
//
// The second return value reports whether this call minted the row. Reuse is a
// database property here — UNIQUE(app_name, content_hash) — and the caller has
// no other way to tell an insert from a lookup, so apply can only say "same
// version as before" honestly if this says so.
func (s *Store) CommitAppVersion(v AppVersion, now time.Time) (AppVersion, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return AppVersion{}, false, nil
	}
	if v.AppName == "" || v.ContentHash == "" {
		return AppVersion{}, false, fmt.Errorf("store: an app version needs both an app name and a content hash (got name %q, hash %q)", v.AppName, v.ContentHash)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return AppVersion{}, false, err
	}
	defer func() { _ = tx.Rollback() }()

	stamp := now.UTC().Format(sortableTimeFormat)
	if _, err := tx.Exec(`
		INSERT INTO apps (name, current_version_id, created_at, updated_at)
		VALUES (?, NULL, ?, ?)
		ON CONFLICT(name) DO UPDATE SET updated_at = excluded.updated_at
	`, v.AppName, stamp, stamp); err != nil {
		return AppVersion{}, false, err
	}

	created := false
	existing, err := scanAppVersion(tx.QueryRow(`
		SELECT id, app_name, content_hash, declaration, artifact_path, created_at
		FROM app_versions WHERE app_name = ? AND content_hash = ?
	`, v.AppName, v.ContentHash))
	switch err {
	case nil:
		v = existing
	case sql.ErrNoRows:
		v.CreatedAt = now.UTC()
		res, err := tx.Exec(`
			INSERT INTO app_versions (app_name, content_hash, declaration, artifact_path, created_at)
			VALUES (?, ?, ?, ?, ?)
		`, v.AppName, v.ContentHash, v.Declaration, v.ArtifactPath, stamp)
		if err != nil {
			return AppVersion{}, false, err
		}
		if v.ID, err = res.LastInsertId(); err != nil {
			return AppVersion{}, false, err
		}
		created = true
	default:
		return AppVersion{}, false, err
	}

	if err := pushServingStep(tx, v.AppName, v.ID, stamp); err != nil {
		return AppVersion{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return AppVersion{}, false, err
	}
	return v, created, nil
}

// SetAppCurrentVersion moves an app onto a version it already has, naming that
// version — what `attn app rollback <name> <version>` does. It extends the
// serving chain like an apply does, so the walk starts again from where it lands.
// It refuses a version belonging to another app rather than leaving one app
// pointing into another's history.
func (s *Store) SetAppCurrentVersion(name string, versionID int64, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	var owner string
	err := s.db.QueryRow(`SELECT app_name FROM app_versions WHERE id = ?`, versionID).Scan(&owner)
	switch {
	case err == sql.ErrNoRows:
		return fmt.Errorf("store: no app version %d", versionID)
	case err != nil:
		return err
	case owner != name:
		return fmt.Errorf("store: app version %d belongs to app %q, not %q", versionID, owner, name)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := pushServingStep(tx, name, versionID, now.UTC().Format(sortableTimeFormat)); err != nil {
		return err
	}
	return tx.Commit()
}

// StepAppVersionBack walks one step down an app's serving chain — what bare
// `attn app rollback <name>` does — and is the one writer that does not extend
// it. Walking is a cursor move, so a second bare rollback steps back again
// rather than returning to where the first started.
//
// target is the version the caller resolved from App.PreviousServingVersionID
// and reported to the user; the move refuses unless the step below still carries
// it, so a chain that moved between the two calls cannot land the app somewhere
// nobody was told about.
func (s *Store) StepAppVersionBack(name string, target int64, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	var stepID, versionID int64
	err := s.db.QueryRow(`
		SELECT p.id, p.version_id FROM apps a
			JOIN app_serving_steps c ON c.id = a.serving_step_id
			JOIN app_serving_steps p ON p.id = c.parent_id
		WHERE a.name = ?
	`, name).Scan(&stepID, &versionID)
	switch {
	case err == sql.ErrNoRows:
		return fmt.Errorf("store: app %q has nothing below it in its serving history", name)
	case err != nil:
		return err
	case versionID != target:
		return fmt.Errorf("store: app %q now has version %d one step back, not the %d this rollback resolved; nothing moved", name, versionID, target)
	}
	_, err = s.db.Exec(`
		UPDATE apps SET current_version_id = ?, serving_step_id = ?, updated_at = ? WHERE name = ?
	`, versionID, stepID, now.UTC().Format(sortableTimeFormat), name)
	return err
}

// GetAppVersion loads one version row.
func (s *Store) GetAppVersion(id int64) (AppVersion, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return AppVersion{}, false, nil
	}
	v, err := scanAppVersion(s.db.QueryRow(`
		SELECT id, app_name, content_hash, declaration, artifact_path, created_at
		FROM app_versions WHERE id = ?
	`, id))
	switch err {
	case nil:
		return v, true, nil
	case sql.ErrNoRows:
		return AppVersion{}, false, nil
	default:
		return AppVersion{}, false, err
	}
}

// ListAppVersions returns an app's versions, newest first.
func (s *Store) ListAppVersions(name string) ([]AppVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT id, app_name, content_hash, declaration, artifact_path, created_at
		FROM app_versions WHERE app_name = ? ORDER BY id DESC
	`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AppVersion
	for rows.Next() {
		v, err := scanAppVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// CountAppVersions reports how many versions an app has, including after its
// registry row is gone — which is what lets `attn app remove` say what it kept.
func (s *Store) CountAppVersions(name string) (int, error) {
	return s.countAppRows(`SELECT COUNT(*) FROM app_versions WHERE app_name = ?`, name)
}

// CountAppInvocations reports how many invocations an app has recorded.
func (s *Store) CountAppInvocations(name string) (int, error) {
	return s.countAppRows(`SELECT COUNT(*) FROM app_invocations WHERE app_name = ?`, name)
}

func (s *Store) countAppRows(query, name string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return 0, nil
	}
	var n int
	if err := s.db.QueryRow(query, name).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// AppendAppInvocation records one handler run and returns its id.
func (s *Store) AppendAppInvocation(inv AppInvocation) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return 0, nil
	}
	res, err := s.db.Exec(`
		INSERT INTO app_invocations
			(app_name, version_id, event_seq, event_name, event_subject, handler, status, error, duration_ms, started_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, inv.AppName, inv.VersionID, inv.EventSeq, inv.EventName, inv.EventSubject, inv.Handler,
		inv.Status, inv.Error, inv.Duration.Milliseconds(), inv.StartedAt.UTC().Format(sortableTimeFormat))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListAppInvocations returns an app's most recent invocations, newest first.
func (s *Store) ListAppInvocations(name string, limit int) ([]AppInvocation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT id, app_name, version_id, event_seq, event_name, event_subject, handler,
		       status, error, duration_ms, started_at
		FROM app_invocations WHERE app_name = ? ORDER BY started_at DESC, id DESC LIMIT ?
	`, name, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AppInvocation
	for rows.Next() {
		var (
			inv       AppInvocation
			durMillis int64
			startedAt string
		)
		if err := rows.Scan(&inv.ID, &inv.AppName, &inv.VersionID, &inv.EventSeq, &inv.EventName,
			&inv.EventSubject, &inv.Handler, &inv.Status, &inv.Error, &durMillis, &startedAt); err != nil {
			return nil, err
		}
		inv.Duration = time.Duration(durMillis) * time.Millisecond
		inv.StartedAt = parseStoreTime(startedAt)
		out = append(out, inv)
	}
	return out, rows.Err()
}

// TrimAppInvocations drops invocations that started before cutoff, then drops
// everything past the newest perApp rows of each app. It reports how many went.
//
// Two limits because they answer two different questions. The age window is when
// a row stops being useful: an invocation whose event has aged off the durable
// log cannot be re-examined against it. The per-app cap is how large the log is
// allowed to get, which the age window cannot bound — how many rows thirty days
// holds depends entirely on how loud the app's subscription is, and the loudest
// one in attn is three orders of magnitude above the quietest. Both values and
// their receipts live with the caller (internal/daemon, AppInvocationRetention
// and AppInvocationsPerApp) — the store keeps no policy. A perApp of zero or
// less skips the cap.
//
// Both sweeps run across every app in one statement rather than per app:
// retention is a property of the table, and a sweep that walked the registry
// would miss the rows of an app that has been removed, which are exactly the ones
// nothing will ever read again.
func (s *Store) TrimAppInvocations(cutoff time.Time, perApp int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return 0, nil
	}
	removed := 0
	res, err := s.db.Exec(`DELETE FROM app_invocations WHERE started_at < ?`,
		cutoff.UTC().Format(sortableTimeFormat))
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	removed += int(n)

	if perApp <= 0 {
		return removed, nil
	}
	// The ordering matches ListAppInvocations exactly — newest first, id breaking
	// a same-timestamp tie — so what the cap keeps is what a reader can see.
	res, err = s.db.Exec(`
		DELETE FROM app_invocations WHERE id IN (
			SELECT id FROM (
				SELECT id, ROW_NUMBER() OVER (
					PARTITION BY app_name ORDER BY started_at DESC, id DESC
				) AS rank FROM app_invocations
			) WHERE rank > ?
		)`, perApp)
	if err != nil {
		return removed, err
	}
	n, err = res.RowsAffected()
	if err != nil {
		return removed, err
	}
	return removed + int(n), nil
}

func scanApp(row rowScanner) (App, error) {
	var (
		app        App
		versionID  sql.NullInt64
		previousID sql.NullInt64
		createdAt  string
		updatedAt  string
	)
	if err := row.Scan(&app.Name, &versionID, &previousID, &createdAt, &updatedAt); err != nil {
		return App{}, err
	}
	app.CurrentVersionID = versionID.Int64
	app.PreviousServingVersionID = previousID.Int64
	app.CreatedAt = parseStoreTime(createdAt)
	app.UpdatedAt = parseStoreTime(updatedAt)
	return app, nil
}

func scanAppVersion(row rowScanner) (AppVersion, error) {
	var (
		v         AppVersion
		createdAt string
	)
	if err := row.Scan(&v.ID, &v.AppName, &v.ContentHash, &v.Declaration, &v.ArtifactPath, &createdAt); err != nil {
		return AppVersion{}, err
	}
	v.CreatedAt = parseStoreTime(createdAt)
	return v, nil
}
