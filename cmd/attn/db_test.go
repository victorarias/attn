package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// testPidPath returns a not-yet-created pid-file path in dir: passing it to
// restoreDatabase exercises the common case where no daemon has ever run
// (acquireDaemonLock creates and locks it), without any test needing to
// depend on a real daemon-liveness stand-in.
func testPidPath(dir string) string {
	return filepath.Join(dir, "attn.pid")
}

// TestRestoreDatabase_RefusesWhileRunning proves restoreDatabase refuses to
// touch anything on disk when another file description already holds the
// exclusive flock on the pid file (a faithful stand-in for a live daemon
// holding daemon.acquirePIDLock — flock is per-open-file-description), and
// that its error tells the operator to stop the daemon.
func TestRestoreDatabase_RefusesWhileRunning(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "attn.db")
	backupsDir := filepath.Join(dir, "backups")
	pidPath := testPidPath(dir)
	writeFile(t, dbPath, "current db")
	mustMkdirAll(t, backupsDir)
	writeFile(t, filepath.Join(backupsDir, "attn-20260101-000000.db"), "backup")
	writeFile(t, pidPath, "12345")

	lockFile, err := os.OpenFile(pidPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open pid file: %v", err)
	}
	defer lockFile.Close()
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("flock pid file: %v", err)
	}

	_, _, restoreErr := restoreDatabase(dbPath, backupsDir, "latest", pidPath)
	if restoreErr == nil {
		t.Fatal("expected error while daemon is running, got nil")
	}
	if !strings.Contains(restoreErr.Error(), "stop it first") && !strings.Contains(strings.ToLower(restoreErr.Error()), "running") {
		t.Fatalf("error should tell the operator to stop the daemon, got: %v", restoreErr)
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
	pidPath := testPidPath(dir)
	writeFile(t, dbPath, "current db")
	mustMkdirAll(t, backupsDir)
	writeFile(t, filepath.Join(backupsDir, "attn-20260101-000000.db"), "oldest")
	writeFile(t, filepath.Join(backupsDir, "attn-20260601-000000.db"), "newest rotating")
	writeFile(t, filepath.Join(backupsDir, "attn-20260301-000000.db"), "middle")
	// A pre-migration snapshot with a timestamp that would sort after every
	// rotating backup above must still be excluded from "latest".
	writeFile(t, filepath.Join(backupsDir, "attn-premigration-99-20261231-000000.db"), "premigration, must be ignored")

	restoredFrom, _, err := restoreDatabase(dbPath, backupsDir, "latest", pidPath)
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
	restoredFrom2, _, err := restoreDatabase(dbPath, backupsDir, "", pidPath)
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
	pidPath := testPidPath(dir)
	writeFile(t, dbPath, "old contents")
	writeFile(t, dbPath+"-wal", "wal")
	writeFile(t, dbPath+"-shm", "shm")
	mustMkdirAll(t, backupsDir)
	backupPath := filepath.Join(backupsDir, "attn-20260101-000000.db")
	writeFile(t, backupPath, "backup contents")

	restoredFrom, preservedAs, err := restoreDatabase(dbPath, backupsDir, "latest", pidPath)
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
	pidPath := testPidPath(dir)
	writeFile(t, dbPath+"-wal", "stray wal")
	writeFile(t, dbPath+"-shm", "stray shm")
	mustMkdirAll(t, backupsDir)
	backupPath := filepath.Join(backupsDir, "attn-20260101-000000.db")
	writeFile(t, backupPath, "backup contents")

	_, preservedAs, err := restoreDatabase(dbPath, backupsDir, "latest", pidPath)
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
	pidPath := testPidPath(dir)
	writeFile(t, dbPath, "current db")

	_, _, err := restoreDatabase(dbPath, backupsDir, "latest", pidPath)
	if err == nil {
		t.Fatal("expected error for missing backups dir, got nil")
	}
	if got := readFile(t, dbPath); got != "current db" {
		t.Fatalf("db was modified despite the error: %q", got)
	}
}

// TestRestoreDatabase_RejectsSourceEqualToDestination proves that passing
// the live attn.db itself as the restore source is rejected before anything
// is touched, rather than renaming the source out from under itself and
// then failing with the live db already gone.
func TestRestoreDatabase_RejectsSourceEqualToDestination(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "attn.db")
	backupsDir := filepath.Join(dir, "backups")
	pidPath := testPidPath(dir)
	writeFile(t, dbPath, "current db")
	mustMkdirAll(t, backupsDir)

	_, _, err := restoreDatabase(dbPath, backupsDir, dbPath, pidPath)
	if err == nil {
		t.Fatal("expected error when source == destination, got nil")
	}
	if !strings.Contains(err.Error(), "live database") {
		t.Fatalf("error should call out that the source is the live database, got: %v", err)
	}
	if got := readFile(t, dbPath); got != "current db" {
		t.Fatalf("db was modified despite the rejection: %q", got)
	}
	matches, globErr := filepath.Glob(dbPath + ".pre-restore-*")
	if globErr != nil {
		t.Fatalf("glob for preserve-rename artifacts: %v", globErr)
	}
	if len(matches) != 0 {
		t.Fatalf("no preserve-rename should have happened, found: %v", matches)
	}
}

// TestRestoreDatabase_RejectsNonRegularSource proves that a directory passed
// as the restore source is rejected (os.Stat succeeds for directories, so
// this must be an explicit check) before the live db is touched, rather than
// failing partway through io.Copy after the live db has already been
// renamed aside and truncated.
func TestRestoreDatabase_RejectsNonRegularSource(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "attn.db")
	backupsDir := filepath.Join(dir, "backups")
	pidPath := testPidPath(dir)
	writeFile(t, dbPath, "current db")
	mustMkdirAll(t, backupsDir)
	notAFile := filepath.Join(dir, "not-a-backup-dir")
	mustMkdirAll(t, notAFile)

	_, _, err := restoreDatabase(dbPath, backupsDir, notAFile, pidPath)
	if err == nil {
		t.Fatal("expected error for a directory source, got nil")
	}
	if !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("error should call out the source is not a regular file, got: %v", err)
	}
	if got := readFile(t, dbPath); got != "current db" {
		t.Fatalf("db was modified despite the rejection: %q", got)
	}
}

// fixedClock returns a now func pinned to the same instant every call, so
// preserveExistingDBAt's collision-avoidance loop can be exercised
// deterministically instead of depending on two calls landing in the same
// real wall-clock second.
func fixedClock() func() time.Time {
	t := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}

// TestRestoreDatabase_PreserveNameCollisionIsResolved proves that two
// restores landing in the same second (so their default UTC-timestamp
// preserve names would collide) each get their own preserved copy instead of
// the second restore's preserve-rename silently replacing the first.
func TestRestoreDatabase_PreserveNameCollisionIsResolved(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "attn.db")
	now := fixedClock()

	writeFile(t, dbPath, "first generation")
	firstPreserved, err := preserveExistingDBAt(dbPath, now, os.Rename)
	if err != nil {
		t.Fatalf("first preserveExistingDBAt error: %v", err)
	}

	writeFile(t, dbPath, "second generation")
	secondPreserved, err := preserveExistingDBAt(dbPath, now, os.Rename)
	if err != nil {
		t.Fatalf("second preserveExistingDBAt error: %v", err)
	}

	if firstPreserved == secondPreserved {
		t.Fatalf("expected distinct preserved paths, both were %q", firstPreserved)
	}
	if got := readFile(t, firstPreserved); got != "first generation" {
		t.Fatalf("first preserved db content = %q, want %q (must not be clobbered by the second preserve)", got, "first generation")
	}
	if got := readFile(t, secondPreserved); got != "second generation" {
		t.Fatalf("second preserved db content = %q, want %q", got, "second generation")
	}
}

// TestPreserveExistingDB_CollisionCheckCoversSidecarTargets proves the
// collision-avoidance loop rejects a candidate name whose main-file slot is
// free but whose -wal sidecar slot is already taken by an unrelated file —
// checking only the main file would let the sidecar rename silently replace
// that stray file.
func TestPreserveExistingDB_CollisionCheckCoversSidecarTargets(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "attn.db")
	now := fixedClock()
	writeFile(t, dbPath, "live db")
	writeFile(t, dbPath+"-wal", "live wal")

	base := dbPath + ".pre-restore-" + now().UTC().Format("20060102-150405")
	writeFile(t, base+"-wal", "unrelated stray wal")

	preservedAs, err := preserveExistingDBAt(dbPath, now, os.Rename)
	if err != nil {
		t.Fatalf("preserveExistingDBAt error: %v", err)
	}
	if preservedAs == base {
		t.Fatalf("expected a name other than %q since its -wal target was already taken", base)
	}
	if got := readFile(t, base+"-wal"); got != "unrelated stray wal" {
		t.Fatalf("stray -wal file was clobbered, content = %q", got)
	}
	if got := readFile(t, preservedAs); got != "live db" {
		t.Fatalf("preserved db content = %q, want %q", got, "live db")
	}
	if got := readFile(t, preservedAs+"-wal"); got != "live wal" {
		t.Fatalf("preserved -wal content = %q, want %q", got, "live wal")
	}
}

// TestPreserveExistingDB_RollsBackIfSidecarRenameFails proves that when the
// main-file rename succeeds but a sidecar rename then fails, the main-file
// move is rolled back so dbPath is never left missing.
func TestPreserveExistingDB_RollsBackIfSidecarRenameFails(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "attn.db")
	writeFile(t, dbPath, "live db")
	writeFile(t, dbPath+"-wal", "live wal")
	writeFile(t, dbPath+"-shm", "live shm")

	calls := 0
	failOnSecondCall := func(oldpath, newpath string) error {
		calls++
		if calls == 2 { // the -wal sidecar rename
			return fmt.Errorf("injected rename failure")
		}
		return os.Rename(oldpath, newpath)
	}

	_, err := preserveExistingDBAt(dbPath, fixedClock(), failOnSecondCall)
	if err == nil {
		t.Fatal("expected error when the sidecar rename fails")
	}
	if !strings.Contains(err.Error(), "injected rename failure") {
		t.Fatalf("error should surface the underlying rename failure, got: %v", err)
	}

	if got := readFile(t, dbPath); got != "live db" {
		t.Fatalf("dbPath was not restored by rollback, content = %q", got)
	}
	if got := readFile(t, dbPath+"-wal"); got != "live wal" {
		t.Fatalf("-wal sidecar was not restored by rollback, content = %q", got)
	}
	// The -shm sidecar rename was never reached (the -wal one failed first)
	// so it should be untouched.
	if got := readFile(t, dbPath+"-shm"); got != "live shm" {
		t.Fatalf("-shm sidecar unexpectedly modified, content = %q", got)
	}
}

// TestAcquireDaemonLock_NoPidFile proves the lock can be acquired (with no
// error) when there is no pid file at all yet — acquireDaemonLock creates it.
func TestAcquireDaemonLock_NoPidFile(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "attn.pid")

	release, err := acquireDaemonLock(pidPath)
	if err != nil {
		t.Fatalf("unexpected error with no pid file present: %v", err)
	}
	defer release()

	if _, statErr := os.Stat(pidPath); statErr != nil {
		t.Fatalf("expected acquireDaemonLock to create the pid file, stat err = %v", statErr)
	}
}

// TestAcquireDaemonLock_HeldByAnotherProcess proves acquireDaemonLock refuses
// (with an operator-facing error) while another open file description holds
// the exclusive flock on the pid file — a faithful stand-in for a live
// daemon process holding daemon.acquirePIDLock, since BSD flock is
// per-open-file-description.
func TestAcquireDaemonLock_HeldByAnotherProcess(t *testing.T) {
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

	if _, err := acquireDaemonLock(pidPath); err == nil {
		t.Fatal("expected acquireDaemonLock to refuse while another fd holds the flock")
	}

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatalf("unlock pid file: %v", err)
	}

	release, err := acquireDaemonLock(pidPath)
	if err != nil {
		t.Fatalf("expected acquireDaemonLock to succeed after the lock was released, got: %v", err)
	}
	release()
}

// TestAcquireDaemonLock_ExcludesConcurrentRestore proves the lock is held —
// not merely probed and released — for as long as the caller keeps it: a
// daemon-style flock attempt on the same pid file, and a second
// acquireDaemonLock call standing in for a concurrent `attn db restore`,
// must both be refused while the first caller has not released, and both
// must succeed once it has. This is the regression test for the TOCTOU gap
// where a point-in-time-only probe left a window between the check and the
// file swap for a daemon to start or a second restore to race in.
func TestAcquireDaemonLock_ExcludesConcurrentRestore(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "attn.pid")

	// Simulates restoreDatabase having acquired the lock and being paused
	// mid-restore (staging/preserving/swapping) — release is deliberately
	// not called yet.
	release, err := acquireDaemonLock(pidPath)
	if err != nil {
		t.Fatalf("initial acquireDaemonLock error: %v", err)
	}

	// A daemon starting up (daemon.acquirePIDLock's exact mechanism: open
	// with O_RDWR|O_CREATE, then LOCK_EX|LOCK_NB) must not be able to start
	// while restore holds the lock.
	daemonAttempt, err := os.OpenFile(pidPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		t.Fatalf("open pid file as a daemon-style attempt: %v", err)
	}
	if flockErr := syscall.Flock(int(daemonAttempt.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); flockErr == nil {
		syscall.Flock(int(daemonAttempt.Fd()), syscall.LOCK_UN)
		daemonAttempt.Close()
		t.Fatal("expected a daemon-style flock attempt to be excluded while restore holds the lock")
	}
	daemonAttempt.Close()

	// A second `attn db restore` (a second acquireDaemonLock call) must
	// also be excluded.
	if _, err := acquireDaemonLock(pidPath); err == nil {
		t.Fatal("expected a concurrent acquireDaemonLock to be excluded while the first is still held")
	}

	release()

	// Once released, both a daemon-style attempt and a fresh
	// acquireDaemonLock must succeed — the exclusion must not outlive the
	// held section.
	afterRelease, err := acquireDaemonLock(pidPath)
	if err != nil {
		t.Fatalf("expected acquireDaemonLock to succeed after release, got: %v", err)
	}
	afterRelease()
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
