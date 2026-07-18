package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func alwaysRunning() (bool, error) { return true, nil }
func neverRunning() (bool, error)  { return false, nil }

// TestRestoreDatabase_RefusesWhileRunning proves restoreDatabase refuses to
// touch anything on disk when the injected daemon-running check reports the
// daemon is live, and that its error tells the operator to stop the daemon.
func TestRestoreDatabase_RefusesWhileRunning(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "attn.db")
	backupsDir := filepath.Join(dir, "backups")
	writeFile(t, dbPath, "current db")
	mustMkdirAll(t, backupsDir)
	writeFile(t, filepath.Join(backupsDir, "attn-20260101-000000.db"), "backup")

	_, _, err := restoreDatabase(dbPath, backupsDir, "latest", alwaysRunning)
	if err == nil {
		t.Fatal("expected error while daemon is running, got nil")
	}
	if !strings.Contains(err.Error(), "stop it first") && !strings.Contains(strings.ToLower(err.Error()), "running") {
		t.Fatalf("error should tell the operator to stop the daemon, got: %v", err)
	}

	if got := readFile(t, dbPath); got != "current db" {
		t.Fatalf("db was modified despite refusal: %q", got)
	}
}

// TestRestoreDatabase_LatestSelectionPicksNewest proves "latest" (and the
// default empty string) resolve to the newest rotating attn-<ts>.db by
// timestamp, ignoring pre-migration snapshots even if they sort later
// lexically by chance.
func TestRestoreDatabase_LatestSelectionPicksNewest(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "attn.db")
	backupsDir := filepath.Join(dir, "backups")
	writeFile(t, dbPath, "current db")
	mustMkdirAll(t, backupsDir)
	writeFile(t, filepath.Join(backupsDir, "attn-20260101-000000.db"), "oldest")
	writeFile(t, filepath.Join(backupsDir, "attn-20260601-000000.db"), "newest rotating")
	writeFile(t, filepath.Join(backupsDir, "attn-20260301-000000.db"), "middle")
	// A pre-migration snapshot with a timestamp that would sort after every
	// rotating backup above must still be excluded from "latest".
	writeFile(t, filepath.Join(backupsDir, "attn-premigration-99-20261231-000000.db"), "premigration, must be ignored")

	restoredFrom, _, err := restoreDatabase(dbPath, backupsDir, "latest", neverRunning)
	if err != nil {
		t.Fatalf("restoreDatabase error: %v", err)
	}
	if filepath.Base(restoredFrom) != "attn-20260601-000000.db" {
		t.Fatalf("restoredFrom = %s, want the newest rotating backup", restoredFrom)
	}
	if got := readFile(t, dbPath); got != "newest rotating" {
		t.Fatalf("restored db content = %q, want the newest rotating backup's content", got)
	}

	// Default (empty string) source must behave identically to "latest".
	writeFile(t, dbPath, "current db again")
	restoredFrom2, _, err := restoreDatabase(dbPath, backupsDir, "", neverRunning)
	if err != nil {
		t.Fatalf("restoreDatabase (default) error: %v", err)
	}
	if restoredFrom2 != restoredFrom {
		t.Fatalf("default source resolved to %s, want same as explicit latest %s", restoredFrom2, restoredFrom)
	}
}

// TestRestoreDatabase_PreservesOldDBAndLeavesBackupInPlace proves the restore
// preserves (renames, never deletes) the pre-restore attn.db and its -wal/-shm
// sidecars, copies (not moves) the backup into place, and that the backup
// file itself survives at its original path afterward.
func TestRestoreDatabase_PreservesOldDBAndLeavesBackupInPlace(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "attn.db")
	backupsDir := filepath.Join(dir, "backups")
	writeFile(t, dbPath, "old contents")
	writeFile(t, dbPath+"-wal", "wal")
	writeFile(t, dbPath+"-shm", "shm")
	mustMkdirAll(t, backupsDir)
	backupPath := filepath.Join(backupsDir, "attn-20260101-000000.db")
	writeFile(t, backupPath, "backup contents")

	restoredFrom, preservedAs, err := restoreDatabase(dbPath, backupsDir, "latest", neverRunning)
	if err != nil {
		t.Fatalf("restoreDatabase error: %v", err)
	}
	if restoredFrom != backupPath {
		t.Fatalf("restoredFrom = %s, want %s", restoredFrom, backupPath)
	}
	if preservedAs == "" {
		t.Fatal("expected a non-empty preservedAs path for an existing attn.db")
	}
	if got := readFile(t, preservedAs); got != "old contents" {
		t.Fatalf("preserved db content = %q, want %q", got, "old contents")
	}
	if got := readFile(t, dbPath); got != "backup contents" {
		t.Fatalf("restored db content = %q, want %q", got, "backup contents")
	}
	// The backup file itself must still exist, unmodified — restore copies.
	if got := readFile(t, backupPath); got != "backup contents" {
		t.Fatalf("backup file was mutated or removed: %q", got)
	}
	// The old db's -wal/-shm sidecars must be preserved alongside the
	// renamed db, not deleted — they can hold uncheckpointed data.
	if got := readFile(t, preservedAs+"-wal"); got != "wal" {
		t.Fatalf("preserved -wal content = %q, want %q", got, "wal")
	}
	if got := readFile(t, preservedAs+"-shm"); got != "shm" {
		t.Fatalf("preserved -shm content = %q, want %q", got, "shm")
	}
	// No stale sidecars should be left at the (now-restored) dbPath.
	if _, err := os.Stat(dbPath + "-wal"); !os.IsNotExist(err) {
		t.Fatalf("expected attn.db-wal to be gone from dbPath, stat err = %v", err)
	}
	if _, err := os.Stat(dbPath + "-shm"); !os.IsNotExist(err) {
		t.Fatalf("expected attn.db-shm to be gone from dbPath, stat err = %v", err)
	}
}

// TestRestoreDatabase_NoExistingDBRemovesStraySidecars proves that when there
// is no existing dbPath to preserve, stray -wal/-shm sidecars (with nothing
// to be preserved alongside) are still cleaned up as before.
func TestRestoreDatabase_NoExistingDBRemovesStraySidecars(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "attn.db")
	backupsDir := filepath.Join(dir, "backups")
	writeFile(t, dbPath+"-wal", "stray wal")
	writeFile(t, dbPath+"-shm", "stray shm")
	mustMkdirAll(t, backupsDir)
	backupPath := filepath.Join(backupsDir, "attn-20260101-000000.db")
	writeFile(t, backupPath, "backup contents")

	_, preservedAs, err := restoreDatabase(dbPath, backupsDir, "latest", neverRunning)
	if err != nil {
		t.Fatalf("restoreDatabase error: %v", err)
	}
	if preservedAs != "" {
		t.Fatalf("expected no preservedAs when there was no existing db, got %q", preservedAs)
	}
	if _, err := os.Stat(dbPath + "-wal"); !os.IsNotExist(err) {
		t.Fatalf("expected stray attn.db-wal to be removed, stat err = %v", err)
	}
	if _, err := os.Stat(dbPath + "-shm"); !os.IsNotExist(err) {
		t.Fatalf("expected stray attn.db-shm to be removed, stat err = %v", err)
	}
}

// TestRestoreDatabase_MissingBackupsDirErrors proves resolving "latest"
// against a nonexistent backups dir fails loudly rather than silently no-op'ing.
func TestRestoreDatabase_MissingBackupsDirErrors(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "attn.db")
	backupsDir := filepath.Join(dir, "backups") // never created
	writeFile(t, dbPath, "current db")

	_, _, err := restoreDatabase(dbPath, backupsDir, "latest", neverRunning)
	if err == nil {
		t.Fatal("expected error for missing backups dir, got nil")
	}
	if got := readFile(t, dbPath); got != "current db" {
		t.Fatalf("db was modified despite the error: %q", got)
	}
}

// TestIsDaemonRunningAt_NoPidFile proves the liveness check reports "not
// running" (with no error) when there is no pid file at all, without ever
// touching real config resolution — dataDir is an injected temp dir.
func TestIsDaemonRunningAt_NoPidFile(t *testing.T) {
	dir := t.TempDir()
	check := isDaemonRunningAt(func() string { return dir })
	running, err := check()
	if err != nil {
		t.Fatalf("unexpected error with no pid file present: %v", err)
	}
	if running {
		t.Fatal("expected not-running with no pid file present")
	}
}

// TestIsDaemonRunningAt_FlockHeld proves the liveness check reports
// "running" (true, nil) while another open file description holds the
// exclusive flock on the pid file, and "not running" (false, nil) once that
// lock is released. BSD flock is per-open-file-description, so holding the
// lock on a separate fd from the one isDaemonRunningAt opens internally is a
// faithful stand-in for a live daemon process holding the lock.
func TestIsDaemonRunningAt_FlockHeld(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "attn.pid")
	writeFile(t, pidPath, "12345")

	lockFile, err := os.OpenFile(pidPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open pid file: %v", err)
	}
	defer lockFile.Close()
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("flock pid file: %v", err)
	}

	check := isDaemonRunningAt(func() string { return dir })

	running, err := check()
	if err != nil {
		t.Fatalf("unexpected error while lock held: %v", err)
	}
	if !running {
		t.Fatal("expected running=true while another fd holds the flock")
	}

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatalf("unlock pid file: %v", err)
	}

	running, err = check()
	if err != nil {
		t.Fatalf("unexpected error after unlock: %v", err)
	}
	if running {
		t.Fatal("expected running=false after the lock was released")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}
