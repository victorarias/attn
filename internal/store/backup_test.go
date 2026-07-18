package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// TestBackupNow_ProducesValidSnapshot proves BackupNow's VACUUM INTO output is
// a fully independent, openable SQLite database carrying the same schema
// version and row data as the live source at the moment of the call.
func TestBackupNow_ProducesValidSnapshot(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB error: %v", err)
	}
	defer s.Close()

	s.Add(&protocol.Session{
		ID:         "session-1",
		Label:      "one",
		Directory:  "/tmp/one",
		State:      protocol.SessionStateWorking,
		StateSince: protocol.TimestampNow().String(),
		LastSeen:   protocol.TimestampNow().String(),
	})
	s.Add(&protocol.Session{
		ID:         "session-2",
		Label:      "two",
		Directory:  "/tmp/two",
		State:      protocol.SessionStateIdle,
		StateSince: protocol.TimestampNow().String(),
		LastSeen:   protocol.TimestampNow().String(),
	})

	backupDir := filepath.Join(t.TempDir(), "backups")
	backupPath, err := s.BackupNow(backupDir, 12)
	if err != nil {
		t.Fatalf("BackupNow error: %v", err)
	}
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("backup file missing: %v", err)
	}
	if filepath.Dir(backupPath) != backupDir {
		t.Fatalf("backup path %s not under %s", backupPath, backupDir)
	}

	wantVersion, err := GetSchemaVersion(s.db)
	if err != nil {
		t.Fatalf("GetSchemaVersion(source): %v", err)
	}

	backupDB, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?mode=ro", backupPath))
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer backupDB.Close()

	gotVersion, err := GetSchemaVersion(backupDB)
	if err != nil {
		t.Fatalf("GetSchemaVersion(backup): %v", err)
	}
	if gotVersion != wantVersion {
		t.Fatalf("backup schema_version = %d, want %d", gotVersion, wantVersion)
	}

	var count int
	if err := backupDB.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&count); err != nil {
		t.Fatalf("count sessions in backup: %v", err)
	}
	if count != 2 {
		t.Fatalf("backup sessions count = %d, want 2", count)
	}

	var label string
	if err := backupDB.QueryRow(`SELECT label FROM sessions WHERE id = ?`, "session-1").Scan(&label); err != nil {
		t.Fatalf("read session-1 from backup: %v", err)
	}
	if label != "one" {
		t.Fatalf("session-1 label in backup = %q, want %q", label, "one")
	}
}

// TestBackupNow_TargetAlreadyExists proves a timestamp collision fails loudly
// instead of silently overwriting or auto-suffixing — the pinned design calls
// this a caller bug, not something to paper over. The exact target name is
// deterministic (second-granularity UTC timestamp), so we pre-create it right
// before calling BackupNow.
func TestBackupNow_TargetAlreadyExists(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB error: %v", err)
	}
	defer s.Close()

	backupDir := t.TempDir()
	// Seed both the current-second and next-second target names: BackupNow
	// computes its own time.Now() after this test does, so a wall-clock
	// second boundary crossed between the two calls must not let it pick a
	// name we didn't pre-create (which would make the test spuriously pass
	// without ever exercising the collision path).
	now := time.Now().UTC()
	for _, ts := range []time.Time{now, now.Add(time.Second)} {
		name := backupNamePrefix + ts.Format(backupNameLayout) + backupNameSuffix
		if err := os.WriteFile(filepath.Join(backupDir, name), []byte("occupied"), 0644); err != nil {
			t.Fatalf("seed colliding file: %v", err)
		}
	}

	if _, err := s.BackupNow(backupDir, 12); err == nil {
		t.Fatal("expected BackupNow to fail on an existing target, got nil error")
	}
}

// TestBackupNow_Rotation proves rotation prunes only the oldest canonical
// rotating backups beyond keep, and never touches pre-migration snapshots.
func TestBackupNow_Rotation(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB error: %v", err)
	}
	defer s.Close()

	backupDir := t.TempDir()
	const keep = 3

	// Pre-create keep+3 fake rotating backups with distinct, lexically ordered
	// timestamps, oldest first.
	var existing []string
	for i := 0; i < keep+3; i++ {
		name := fmt.Sprintf("%s202601%02d-000000%s", backupNamePrefix, i+1, backupNameSuffix)
		path := filepath.Join(backupDir, name)
		if err := os.WriteFile(path, []byte("fake"), 0644); err != nil {
			t.Fatalf("seed fake backup %s: %v", name, err)
		}
		existing = append(existing, name)
	}

	// A pre-migration snapshot must never be counted or pruned.
	premigrationName := "attn-premigration-42-20260101-000000.db"
	if err := os.WriteFile(filepath.Join(backupDir, premigrationName), []byte("fake"), 0644); err != nil {
		t.Fatalf("seed premigration file: %v", err)
	}

	// One real backup call, which itself adds a new rotating file, pushing the
	// total canonical count to keep+4 before pruning.
	newPath, err := s.BackupNow(backupDir, keep)
	if err != nil {
		t.Fatalf("BackupNow error: %v", err)
	}

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("read backup dir: %v", err)
	}
	var rotating []string
	sawPremigration := false
	sawNew := false
	for _, e := range entries {
		if e.Name() == premigrationName {
			sawPremigration = true
			continue
		}
		if e.Name() == filepath.Base(newPath) {
			sawNew = true
		}
		if IsRotatingBackupName(e.Name()) {
			rotating = append(rotating, e.Name())
		}
	}

	if !sawPremigration {
		t.Fatal("premigration snapshot was pruned; it must be exempt")
	}
	if !sawNew {
		t.Fatal("newly-written backup missing after rotation")
	}
	if len(rotating) != keep {
		t.Fatalf("rotating backups after prune = %d, want %d (%v)", len(rotating), keep, rotating)
	}

	// The oldest fakes (index 0, 1, 2 of the pre-seeded batch — 3 files, since
	// keep+3 total minus keep+1 kept-from-existing... ) must be gone. More
	// simply: the two oldest fakes are guaranteed pruned, since the newly
	// written backup's timestamp sorts after all the pre-seeded fakes.
	oldestTwo := existing[:2]
	for _, name := range oldestTwo {
		if _, err := os.Stat(filepath.Join(backupDir, name)); !os.IsNotExist(err) {
			t.Fatalf("expected oldest backup %s to be pruned, stat err = %v", name, err)
		}
	}
	// The newest of the pre-seeded fakes must survive.
	newest := existing[len(existing)-1]
	if _, err := os.Stat(filepath.Join(backupDir, newest)); err != nil {
		t.Fatalf("expected newest pre-seeded backup %s to survive prune: %v", newest, err)
	}
}

// TestBackupNow_RefusesNonDurableStore proves that a store falling back to
// the in-memory (":memory:") database — what New() returns when the durable
// DB fails to open — refuses to back up rather than silently writing an
// empty snapshot. Without this guard, a degraded daemon would rotate real
// recovery copies out of the backups directory over the twelve ticks it
// takes to fill the keep window, destroying exactly the safety net needed
// while the durable DB is unavailable.
func TestBackupNow_RefusesNonDurableStore(t *testing.T) {
	s := New()
	defer s.Close()

	backupDir := t.TempDir()
	if _, err := s.BackupNow(backupDir, 12); err == nil {
		t.Fatal("expected BackupNow to refuse a non-durable (in-memory fallback) store, got nil error")
	}

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("read backup dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("BackupNow wrote to backups dir despite refusing: %v", entries)
	}
}

// TestMigrateDB_PreMigrationBackup proves that migrating an existing,
// non-empty database forward writes a pre-migration snapshot to backups/
// alongside the DB file before mutating the schema, using the same
// unrecord-a-migration technique as TestMigration53AddsClosedStateColumnIdempotently
// to force migrateDB down its "pending migrations" path against a real file.
func TestMigrateDB_PreMigrationBackup(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB error: %v", err)
	}
	defer s.Close()

	latest := latestSchemaVersion()
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version = ?`, latest); err != nil {
		t.Fatalf("unrecord latest migration: %v", err)
	}

	if err := migrateDB(s.db, dbPath); err != nil {
		t.Fatalf("migrateDB error: %v", err)
	}

	backupDir := filepath.Join(filepath.Dir(dbPath), "backups")
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("read backups dir: %v", err)
	}
	found := false
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "attn-premigration-") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no pre-migration backup found in %s, entries: %v", backupDir, entries)
	}
}

// TestBackupPreMigration_CapsSnapshots proves that after backupPreMigration
// runs, older attn-premigration-*.db files in the same dir beyond the newest
// backupPremigrationKeep are pruned by their trailing timestamp (not by
// mtime or whole-filename sort, which would misorder across differing
// version-number digit widths), while rotating attn-<timestamp>.db backups
// are left untouched.
func TestBackupPreMigration_CapsSnapshots(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB error: %v", err)
	}
	defer s.Close()

	backupDir := filepath.Join(filepath.Dir(dbPath), "backups")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		t.Fatalf("mkdir backups dir: %v", err)
	}

	// Seed 7 fake pre-migration files with distinct timestamps, deliberately
	// using version numbers of differing digit widths (9 vs 10 vs 100) so a
	// whole-filename sort would misorder them relative to a timestamp sort.
	versions := []int{9, 10, 100, 11, 12, 13, 14}
	var seeded []string
	for i, v := range versions {
		ts := fmt.Sprintf("202601%02d-000000", i+1)
		name := fmt.Sprintf("attn-premigration-%d-%s.db", v, ts)
		if err := os.WriteFile(filepath.Join(backupDir, name), []byte("fake"), 0644); err != nil {
			t.Fatalf("seed premigration file %s: %v", name, err)
		}
		seeded = append(seeded, name)
	}
	// oldest-first by index/timestamp: seeded[0]..seeded[6]

	// A rotating backup must never be touched by the premigration prune.
	rotatingName := backupNamePrefix + "20260101-000000" + backupNameSuffix
	if err := os.WriteFile(filepath.Join(backupDir, rotatingName), []byte("fake"), 0644); err != nil {
		t.Fatalf("seed rotating file: %v", err)
	}

	// Force migrateDB down its "pending migrations" path against a real
	// file, same technique as TestMigrateDB_PreMigrationBackup.
	latest := latestSchemaVersion()
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version = ?`, latest); err != nil {
		t.Fatalf("unrecord latest migration: %v", err)
	}
	if err := migrateDB(s.db, dbPath); err != nil {
		t.Fatalf("migrateDB error: %v", err)
	}

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("read backups dir: %v", err)
	}

	var premigration []string
	sawRotating := false
	for _, e := range entries {
		if e.Name() == rotatingName {
			sawRotating = true
			continue
		}
		if strings.HasPrefix(e.Name(), premigrationNamePrefix) {
			premigration = append(premigration, e.Name())
		}
	}

	if !sawRotating {
		t.Fatal("rotating backup was removed by premigration prune; it must be untouched")
	}

	// One new premigration snapshot was written by migrateDB itself, so the
	// total before prune is len(seeded)+1 = 8; after capping to
	// backupPremigrationKeep = 5, exactly 5 must remain.
	if len(premigration) != backupPremigrationKeep {
		t.Fatalf("premigration snapshots after prune = %d, want %d (%v)", len(premigration), backupPremigrationKeep, premigration)
	}

	// The oldest 3 of the 7 seeded files must be gone (8 total - 5 kept = 3
	// removed, and the new snapshot's timestamp sorts after all seeded ones).
	for _, name := range seeded[:3] {
		if _, err := os.Stat(filepath.Join(backupDir, name)); !os.IsNotExist(err) {
			t.Fatalf("expected oldest premigration file %s to be pruned, stat err = %v", name, err)
		}
	}
	// The newest 4 of the 7 seeded files must survive.
	for _, name := range seeded[3:] {
		if _, err := os.Stat(filepath.Join(backupDir, name)); err != nil {
			t.Fatalf("expected premigration file %s to survive prune: %v", name, err)
		}
	}
}
