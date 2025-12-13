package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestSeedLegacyDB(t *testing.T) {
	// Create temp dir
	tmpDir, err := os.MkdirTemp("", "legacy-db-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")

	// Create a legacy-style database with head_sha already in prs table
	// but no migration history
	legacySchema := `
CREATE TABLE IF NOT EXISTS sessions (
	id TEXT PRIMARY KEY,
	label TEXT NOT NULL,
	directory TEXT NOT NULL,
	state TEXT NOT NULL DEFAULT 'idle',
	state_since TEXT NOT NULL,
	state_updated_at TEXT NOT NULL,
	todos TEXT,
	last_seen TEXT NOT NULL,
	muted INTEGER NOT NULL DEFAULT 0,
	branch TEXT,
	is_worktree INTEGER NOT NULL DEFAULT 0,
	main_repo TEXT
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
	head_branch TEXT,
	comment_count INTEGER NOT NULL DEFAULT 0,
	approved_by_me INTEGER NOT NULL DEFAULT 0,
	heat_state TEXT NOT NULL DEFAULT 'cold',
	last_heat_activity_at TEXT
);

CREATE TABLE IF NOT EXISTS pr_interactions (
	pr_id TEXT PRIMARY KEY,
	last_visited_at TEXT,
	last_approved_at TEXT,
	last_seen_sha TEXT,
	last_seen_comment_count INTEGER,
	last_seen_ci_status TEXT
);

CREATE TABLE IF NOT EXISTS schema_migrations (
	version INTEGER PRIMARY KEY,
	applied_at TEXT NOT NULL
);
`
	// Create legacy DB directly with sqlite
	legacyDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacyDB.Exec(legacySchema); err != nil {
		t.Fatal(err)
	}
	legacyDB.Close()

	// Now try to open with OpenDB - this should detect legacy and seed migrations
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB failed on legacy DB: %v", err)
	}
	defer db.Close()

	// Verify migrations were seeded
	version, err := GetSchemaVersion(db)
	if err != nil {
		t.Fatal(err)
	}
	if version < 10 {
		t.Errorf("Expected version >= 10 after seeding, got %d", version)
	}
}
