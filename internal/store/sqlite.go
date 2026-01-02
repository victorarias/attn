package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

// baseSchema creates the core tables. Column additions are handled by migrations.
// This schema represents the initial state (version 0).
const baseSchema = `
CREATE TABLE IF NOT EXISTS sessions (
	id TEXT PRIMARY KEY,
	label TEXT NOT NULL,
	directory TEXT NOT NULL,
	state TEXT NOT NULL DEFAULT 'idle',
	state_since TEXT NOT NULL,
	state_updated_at TEXT NOT NULL,
	todos TEXT,
	last_seen TEXT NOT NULL,
	muted INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS prs (
	id TEXT PRIMARY KEY,
	repo TEXT NOT NULL,
	number INTEGER NOT NULL,
	title TEXT NOT NULL,
	url TEXT NOT NULL,
	role TEXT NOT NULL,
	state TEXT NOT NULL,
	reason TEXT,
	last_updated TEXT NOT NULL,
	last_polled TEXT NOT NULL,
	muted INTEGER NOT NULL DEFAULT 0,
	details_fetched INTEGER NOT NULL DEFAULT 0,
	details_fetched_at TEXT,
	mergeable INTEGER,
	mergeable_state TEXT,
	ci_status TEXT,
	review_status TEXT
);

CREATE TABLE IF NOT EXISTS repos (
	repo TEXT PRIMARY KEY,
	muted INTEGER NOT NULL DEFAULT 0,
	collapsed INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS pr_interactions (
	pr_id TEXT PRIMARY KEY,
	last_visited_at TEXT,
	last_approved_at TEXT,
	last_seen_sha TEXT,
	last_seen_comment_count INTEGER
);

CREATE TABLE IF NOT EXISTS worktrees (
	path TEXT PRIMARY KEY,
	branch TEXT NOT NULL,
	main_repo TEXT NOT NULL,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
	key TEXT PRIMARY KEY,
	value TEXT
);

CREATE TABLE IF NOT EXISTS schema_migrations (
	version INTEGER PRIMARY KEY,
	applied_at TEXT NOT NULL
);
`

// migration represents a database schema migration
type migration struct {
	version int
	desc    string
	sql     string
}

// migrations defines all schema migrations in order.
// Each migration is applied exactly once, tracked in schema_migrations table.
// To add a new migration: append to this slice with the next version number.
var migrations = []migration{
	{1, "add head_sha to prs", "ALTER TABLE prs ADD COLUMN head_sha TEXT"},
	{2, "add head_branch to prs", "ALTER TABLE prs ADD COLUMN head_branch TEXT"},
	{3, "add comment_count to prs", "ALTER TABLE prs ADD COLUMN comment_count INTEGER NOT NULL DEFAULT 0"},
	{4, "add approved_by_me to prs", "ALTER TABLE prs ADD COLUMN approved_by_me INTEGER NOT NULL DEFAULT 0"},
	{5, "add heat_state to prs", "ALTER TABLE prs ADD COLUMN heat_state TEXT NOT NULL DEFAULT 'cold'"},
	{6, "add last_heat_activity_at to prs", "ALTER TABLE prs ADD COLUMN last_heat_activity_at TEXT"},
	{7, "add last_seen_ci_status to pr_interactions", "ALTER TABLE pr_interactions ADD COLUMN last_seen_ci_status TEXT"},
	{8, "add branch to sessions", "ALTER TABLE sessions ADD COLUMN branch TEXT"},
	{9, "add is_worktree to sessions", "ALTER TABLE sessions ADD COLUMN is_worktree INTEGER NOT NULL DEFAULT 0"},
	{10, "add main_repo to sessions", "ALTER TABLE sessions ADD COLUMN main_repo TEXT"},
	{11, "create recent_locations table", `CREATE TABLE IF NOT EXISTS recent_locations (
		path TEXT PRIMARY KEY,
		label TEXT NOT NULL,
		last_seen TEXT NOT NULL,
		use_count INTEGER NOT NULL DEFAULT 1
	)`},
	{12, "create reviews table", `CREATE TABLE IF NOT EXISTS reviews (
		id TEXT PRIMARY KEY,
		branch TEXT NOT NULL,
		pr_number INTEGER,
		repo_path TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		UNIQUE(repo_path, branch)
	)`},
	{13, "create review_viewed_files table", `CREATE TABLE IF NOT EXISTS review_viewed_files (
		review_id TEXT NOT NULL,
		filepath TEXT NOT NULL,
		viewed_at TEXT NOT NULL,
		PRIMARY KEY (review_id, filepath),
		FOREIGN KEY (review_id) REFERENCES reviews(id) ON DELETE CASCADE
	)`},
}

// OpenDB opens a SQLite database at the given path, creating it if necessary.
// It also creates the schema if the database is new.
func OpenDB(dbPath string) (*sql.DB, error) {
	// Create parent directories if they don't exist
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	// Create base schema (includes schema_migrations table)
	if _, err := db.Exec(baseSchema); err != nil {
		db.Close()
		return nil, err
	}

	// Run versioned migrations
	if err := migrateDB(db); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

// migrateDB runs all pending migrations in order.
// It tracks applied migrations in the schema_migrations table.
func migrateDB(db *sql.DB) error {
	// Detect and handle legacy databases (created before migration system existed)
	if err := seedLegacyDB(db); err != nil {
		return fmt.Errorf("seeding legacy db: %w", err)
	}

	// Get current schema version
	currentVersion, err := getCurrentVersion(db)
	if err != nil {
		return fmt.Errorf("getting schema version: %w", err)
	}

	// Run pending migrations
	for _, m := range migrations {
		if m.version <= currentVersion {
			continue
		}

		// Execute migration in a transaction
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("starting transaction for migration %d: %w", m.version, err)
		}

		if _, err := tx.Exec(m.sql); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
		}

		// Record migration
		if _, err := tx.Exec(
			"INSERT INTO schema_migrations (version, applied_at) VALUES (?, datetime('now'))",
			m.version,
		); err != nil {
			tx.Rollback()
			return fmt.Errorf("recording migration %d: %w", m.version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("committing migration %d: %w", m.version, err)
		}
	}

	return nil
}

// seedLegacyDB detects databases created before the migration system existed
// and seeds schema_migrations to prevent duplicate column errors.
// Legacy DBs have all columns (up to migration 10) but no migration history.
func seedLegacyDB(db *sql.DB) error {
	// Check if schema_migrations is empty
	currentVersion, err := getCurrentVersion(db)
	if err != nil {
		return err
	}
	if currentVersion > 0 {
		return nil // Already has migration history
	}

	// Check if this is a legacy DB by looking for a column that only exists
	// in legacy DBs (head_sha was in the original schema before migrations)
	var colCount int
	err = db.QueryRow(`
		SELECT COUNT(*) FROM pragma_table_info('prs') WHERE name = 'head_sha'
	`).Scan(&colCount)
	if err != nil {
		return err
	}
	if colCount == 0 {
		return nil // Fresh DB, no legacy columns
	}

	// Legacy DB detected - seed migrations 1-10 (all columns that existed before migration system)
	// Migration 11+ were added after the migration system, so they need to run normally
	const legacyMaxVersion = 10
	tx, err := db.Begin()
	if err != nil {
		return err
	}

	for v := 1; v <= legacyMaxVersion; v++ {
		if _, err := tx.Exec(
			"INSERT INTO schema_migrations (version, applied_at) VALUES (?, datetime('now'))",
			v,
		); err != nil {
			tx.Rollback()
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	return nil
}

// getCurrentVersion returns the highest applied migration version, or 0 if none.
func getCurrentVersion(db *sql.DB) (int, error) {
	var version int
	err := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version)
	if err != nil {
		return 0, err
	}
	return version, nil
}

// GetSchemaVersion returns the current schema version for the database.
// Exported for testing and diagnostics.
func GetSchemaVersion(db *sql.DB) (int, error) {
	return getCurrentVersion(db)
}
