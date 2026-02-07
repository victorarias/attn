package store

import (
	"path/filepath"
	"testing"
)

func TestOpenDB_CreatesSchema(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() error = %v", err)
	}
	defer db.Close()

	// Verify tables exist by querying them
	tables := []string{"sessions", "prs", "repos"}
	for _, table := range tables {
		var count int
		err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count)
		if err != nil {
			t.Errorf("Table %q does not exist: %v", table, err)
		}
	}
}

func TestOpenDB_CreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "subdir", "nested", "test.db")

	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() should create parent directories, got error = %v", err)
	}
	defer db.Close()
}

func TestOpenDB_ReopensExistingDB(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create and insert data
	db1, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() error = %v", err)
	}
	_, err = db1.Exec("INSERT INTO repos (repo, muted, collapsed) VALUES ('test/repo', 1, 0)")
	if err != nil {
		t.Fatalf("INSERT error = %v", err)
	}
	db1.Close()

	// Reopen and verify data persists
	db2, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() reopen error = %v", err)
	}
	defer db2.Close()

	var muted int
	err = db2.QueryRow("SELECT muted FROM repos WHERE repo = 'test/repo'").Scan(&muted)
	if err != nil {
		t.Fatalf("SELECT error = %v", err)
	}
	if muted != 1 {
		t.Errorf("muted = %d, want 1", muted)
	}
}

func TestMigrations_AppliedOnNewDB(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() error = %v", err)
	}
	defer db.Close()

	// Check schema version matches total migrations
	version, err := GetSchemaVersion(db)
	if err != nil {
		t.Fatalf("GetSchemaVersion() error = %v", err)
	}
	if version != len(migrations) {
		t.Errorf("schema version = %d, want %d", version, len(migrations))
	}

	// Verify all migrations recorded
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count)
	if err != nil {
		t.Fatalf("counting migrations error = %v", err)
	}
	if count != len(migrations) {
		t.Errorf("migration count = %d, want %d", count, len(migrations))
	}
}

func TestMigrations_Idempotent(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Open DB twice
	db1, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() first error = %v", err)
	}
	db1.Close()

	db2, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() second error = %v", err)
	}
	defer db2.Close()

	// Should still have same number of migrations
	var count int
	err = db2.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count)
	if err != nil {
		t.Fatalf("counting migrations error = %v", err)
	}
	if count != len(migrations) {
		t.Errorf("migration count after reopen = %d, want %d", count, len(migrations))
	}
}

func TestMigrations_MigratedColumnsExist(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() error = %v", err)
	}
	defer db.Close()

	// Verify migrated columns exist by querying them
	migratedColumns := []struct {
		table  string
		column string
	}{
		{"prs", "host"},
		{"prs", "head_sha"},
		{"prs", "head_branch"},
		{"prs", "comment_count"},
		{"prs", "approved_by_me"},
		{"prs", "heat_state"},
		{"prs", "last_heat_activity_at"},
		{"pr_interactions", "last_seen_ci_status"},
		{"sessions", "branch"},
		{"sessions", "is_worktree"},
		{"sessions", "main_repo"},
		{"sessions", "agent"},
	}

	for _, tc := range migratedColumns {
		query := "SELECT " + tc.column + " FROM " + tc.table + " LIMIT 1"
		_, err := db.Exec(query)
		if err != nil {
			t.Errorf("Column %s.%s should exist after migrations: %v", tc.table, tc.column, err)
		}
	}
}

func TestMigration20_IdempotentWhenHostColumnAlreadyExists(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create DB and schema manually up to migration 19, then pre-add prs.host.
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() setup error = %v", err)
	}
	db.Close()

	// Re-open raw and force migration state back to 19 while keeping host column.
	raw, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() reopen setup error = %v", err)
	}

	// Delete migration markers for v20+ to simulate partially migrated DBs in the wild.
	if _, err := raw.Exec("DELETE FROM schema_migrations WHERE version >= 20"); err != nil {
		raw.Close()
		t.Fatalf("DELETE migration >=20 markers error = %v", err)
	}
	if _, err := raw.Exec("ALTER TABLE prs ADD COLUMN host TEXT NOT NULL DEFAULT 'github.com'"); err != nil {
		// On already-host databases this may fail; that still matches the scenario.
		_ = err
	}
	raw.Close()

	// Should not fail on duplicate host column when applying v20.
	db2, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() should handle existing prs.host in migration 20, got error = %v", err)
	}
	defer db2.Close()

	version, err := GetSchemaVersion(db2)
	if err != nil {
		t.Fatalf("GetSchemaVersion() error = %v", err)
	}
	if version != len(migrations) {
		t.Fatalf("schema version = %d, want %d", version, len(migrations))
	}

	// Unique index from migration 20 should exist.
	var idxName string
	err = db2.QueryRow(`
		SELECT name FROM sqlite_master
		WHERE type = 'index' AND name = 'idx_prs_host_repo_number'
	`).Scan(&idxName)
	if err != nil {
		t.Fatalf("expected migration 20 index to exist: %v", err)
	}
	if idxName != "idx_prs_host_repo_number" {
		t.Fatalf("index name = %q, want idx_prs_host_repo_number", idxName)
	}

	// Migration table should contain version 20.
	var count int
	if err := db2.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = 20").Scan(&count); err != nil {
		t.Fatalf("count migration 20 row error = %v", err)
	}
	if count != 1 {
		t.Fatalf("migration 20 marker count = %d, want 1", count)
	}

}

func TestMigration21_IdempotentWhenAgentColumnAlreadyExists(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() setup error = %v", err)
	}
	db.Close()

	raw, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() reopen setup error = %v", err)
	}

	if _, err := raw.Exec("DELETE FROM schema_migrations WHERE version = 21"); err != nil {
		raw.Close()
		t.Fatalf("DELETE migration 21 marker error = %v", err)
	}
	if _, err := raw.Exec("ALTER TABLE sessions ADD COLUMN agent TEXT NOT NULL DEFAULT 'codex'"); err != nil {
		_ = err
	}
	raw.Close()

	db2, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() should handle existing sessions.agent in migration 21, got error = %v", err)
	}
	defer db2.Close()

	version, err := GetSchemaVersion(db2)
	if err != nil {
		t.Fatalf("GetSchemaVersion() error = %v", err)
	}
	if version != len(migrations) {
		t.Fatalf("schema version = %d, want %d", version, len(migrations))
	}

	var count int
	if err := db2.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = 21").Scan(&count); err != nil {
		t.Fatalf("count migration 21 row error = %v", err)
	}
	if count != 1 {
		t.Fatalf("migration 21 marker count = %d, want 1", count)
	}
}
