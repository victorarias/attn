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
