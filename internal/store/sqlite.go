package store

import (
	"database/sql"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
	id TEXT PRIMARY KEY,
	label TEXT NOT NULL,
	directory TEXT NOT NULL,
	branch TEXT,
	is_worktree INTEGER NOT NULL DEFAULT 0,
	main_repo TEXT,
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
	review_status TEXT,
	head_sha TEXT,
	comment_count INTEGER NOT NULL DEFAULT 0,
	approved_by_me INTEGER NOT NULL DEFAULT 0,
	heat_state TEXT NOT NULL DEFAULT 'cold',
	last_heat_activity_at TEXT
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
	last_seen_comment_count INTEGER,
	last_seen_ci_status TEXT
);

CREATE TABLE IF NOT EXISTS worktrees (
	path TEXT PRIMARY KEY,
	branch TEXT NOT NULL,
	main_repo TEXT NOT NULL,
	created_at TEXT NOT NULL
);
`

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

	// Create schema
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}

	// Run migrations for existing databases
	if err := migrateDB(db); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

// migrateDB adds new columns to existing tables (SQLite doesn't support IF NOT EXISTS for columns)
func migrateDB(db *sql.DB) error {
	// Check if head_sha column exists in prs table
	migrations := []struct {
		check string
		alter string
	}{
		{"SELECT head_sha FROM prs LIMIT 1", "ALTER TABLE prs ADD COLUMN head_sha TEXT"},
		{"SELECT comment_count FROM prs LIMIT 1", "ALTER TABLE prs ADD COLUMN comment_count INTEGER NOT NULL DEFAULT 0"},
		{"SELECT approved_by_me FROM prs LIMIT 1", "ALTER TABLE prs ADD COLUMN approved_by_me INTEGER NOT NULL DEFAULT 0"},
		{"SELECT heat_state FROM prs LIMIT 1", "ALTER TABLE prs ADD COLUMN heat_state TEXT NOT NULL DEFAULT 'cold'"},
		{"SELECT last_heat_activity_at FROM prs LIMIT 1", "ALTER TABLE prs ADD COLUMN last_heat_activity_at TEXT"},
		{"SELECT last_seen_ci_status FROM pr_interactions LIMIT 1", "ALTER TABLE pr_interactions ADD COLUMN last_seen_ci_status TEXT"},
		{"SELECT branch FROM sessions LIMIT 1", "ALTER TABLE sessions ADD COLUMN branch TEXT"},
		{"SELECT is_worktree FROM sessions LIMIT 1", "ALTER TABLE sessions ADD COLUMN is_worktree INTEGER NOT NULL DEFAULT 0"},
		{"SELECT main_repo FROM sessions LIMIT 1", "ALTER TABLE sessions ADD COLUMN main_repo TEXT"},
	}

	for _, m := range migrations {
		_, err := db.Exec(m.check)
		if err != nil {
			// Column doesn't exist, add it
			if _, err := db.Exec(m.alter); err != nil {
				return err
			}
		}
	}

	return nil
}
