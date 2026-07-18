package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/store"
)

// TestPerformDatabaseBackup_SurfacesLastBackupAt proves a successful
// performDatabaseBackup call records lastBackupAt and surfaces it as a
// parseable RFC3339 UTC db.last_backup_at settings key, so live clients (and
// db restore's "did the backup succeed recently" sanity check) can see the
// backup cadence move.
func TestPerformDatabaseBackup_SurfacesLastBackupAt(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	s, err := store.NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB error: %v", err)
	}
	defer s.Close()

	d := &Daemon{store: s, dataRoot: t.TempDir()}

	before := time.Now().UTC()
	d.performDatabaseBackup()

	settings := d.settingsWithAgentAvailability()
	raw, ok := settings[SettingDBLastBackupAt]
	if !ok {
		t.Fatalf("settings missing %s after successful backup: %v", SettingDBLastBackupAt, settings)
	}
	str, ok := raw.(string)
	if !ok {
		t.Fatalf("%s is not a string: %v (%T)", SettingDBLastBackupAt, raw, raw)
	}
	parsed, err := time.Parse(time.RFC3339, str)
	if err != nil {
		t.Fatalf("%s = %q is not parseable RFC3339: %v", SettingDBLastBackupAt, str, err)
	}
	if parsed.Before(before.Add(-time.Second)) || parsed.After(time.Now().UTC().Add(time.Second)) {
		t.Fatalf("%s = %v is outside the expected window around %v", SettingDBLastBackupAt, parsed, before)
	}
}

// TestPerformDatabaseBackup_FailedBackupLeavesKeyAbsent proves a failed
// backup (a non-durable in-memory fallback store, same guard BackupNow
// enforces) never sets db.last_backup_at — a stale or fabricated success
// timestamp would be worse than an absent one.
func TestPerformDatabaseBackup_FailedBackupLeavesKeyAbsent(t *testing.T) {
	s := store.New() // in-memory fallback: not durable, BackupNow refuses
	defer s.Close()

	d := &Daemon{store: s, dataRoot: t.TempDir()}
	d.performDatabaseBackup()

	settings := d.settingsWithAgentAvailability()
	if raw, ok := settings[SettingDBLastBackupAt]; ok {
		t.Fatalf("%s should be absent after a failed backup, got %v", SettingDBLastBackupAt, raw)
	}
}
