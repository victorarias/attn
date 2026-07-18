package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/daemonctl"
	"github.com/victorarias/attn/internal/store"
)

// runDB routes `attn db <command>`: operator-facing database maintenance that
// sits alongside the daemon's own automatic rotating backups (BackupNow /
// backupPreMigration in internal/store).
func runDB() {
	if len(os.Args) < 3 || os.Args[2] == "-h" || os.Args[2] == "--help" {
		writeDBHelp(os.Stdout)
		return
	}
	switch os.Args[2] {
	case "restore":
		if hasHelpFlag(os.Args[3:]) {
			writeDBHelp(os.Stdout)
			return
		}
		runDBRestore(os.Args[3:])
	default:
		fmt.Fprintf(os.Stderr, "db: unknown command %q\n", os.Args[2])
		writeDBHelp(os.Stderr)
		os.Exit(2)
	}
}

func writeDBHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn db <command>

commands:
  restore [path|latest]
        restore attn.db from a rotating backup (default: latest). The daemon
        must be stopped first. The current attn.db is preserved (renamed, never
        deleted) as attn.db.pre-restore-<UTC timestamp> before the backup is
        copied into place.

        path defaults to "latest": the newest rotating attn-<timestamp>.db
        snapshot in the profile's backups directory. Pass an explicit path to
        restore from any snapshot, including a pre-migration one
        (attn-premigration-<version>-<timestamp>.db).
`)
}

func runDBRestore(args []string) {
	source := "latest"
	if len(args) > 0 {
		source = args[0]
	}

	dbPath := config.DBPath()
	backupsDir := filepath.Join(config.DataDir(), "backups")
	pidPath := filepath.Join(config.DataDir(), "attn.pid")

	restoredFrom, preservedAs, err := restoreDatabase(dbPath, backupsDir, source, pidPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "attn db restore: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("restored attn.db from: %s\n", restoredFrom)
	if preservedAs != "" {
		fmt.Printf("previous attn.db preserved as: %s\n", preservedAs)
	} else {
		fmt.Println("no previous attn.db existed to preserve")
	}
	fmt.Println("start the daemon when ready (e.g. `attn daemon ensure`, or reopen the app)")
}

// restoreDatabase implements `attn db restore` against explicit paths so it
// is fully unit-testable against temp dirs: it never resolves config's real
// data dir itself.
//
// source is either "" or "latest" (pick the newest rotating attn-<ts>.db
// snapshot in backupsDir) or an explicit path to any snapshot file, including
// a pre-migration one.
//
// On success it (a) preserves the existing dbPath by renaming it (and its
// -wal/-shm sidecars, if any) to dbPath+".pre-restore-<UTC ts>" (a no-op, not
// an error, if dbPath does not currently exist), then (b) copies (not moves
// — the source backup remains) the resolved snapshot into place as dbPath.
// Returns the resolved source path and the preserved-db path (empty if there
// was nothing to preserve).
//
// pidPath is the daemon's pid-file lock (see acquireDaemonLock). The lock is
// acquired before anything else and held for the entire restore, including
// staging, preserving, and the final swap — not just probed and released up
// front. A point-in-time-only probe would leave a window in which a daemon
// could start (or a second `attn db restore` could run) between the probe
// and the file swap; holding the lock for the whole critical section closes
// that window, mirroring how the daemon itself holds the same lock for its
// entire lifetime (daemon.acquirePIDLock).
//
// The live dbPath is only ever mutated after the source has been fully and
// successfully staged, so a bad source (a directory, an unreadable file, or
// dbPath itself) is rejected before anything at dbPath is touched. Staging
// happens into a uniquely-named temp file in dbPath's own directory, which
// also makes the final swap a same-filesystem os.Rename instead of a
// copy/truncate directly onto the live path — so the live path is either the
// fully-old file or the fully-new one, never a partially-written one. If the
// final swap still fails after a preserve-rename has already happened, the
// preserved copy is rolled back into place so dbPath is never left missing.
func restoreDatabase(dbPath, backupsDir, source, pidPath string) (restoredFrom, preservedAs string, err error) {
	release, err := acquireDaemonLock(pidPath)
	if err != nil {
		return "", "", err
	}
	defer release()

	srcPath := strings.TrimSpace(source)
	if srcPath == "" || srcPath == "latest" {
		srcPath, err = latestRotatingBackup(backupsDir)
		if err != nil {
			return "", "", err
		}
	}

	srcInfo, statErr := os.Stat(srcPath)
	if statErr != nil {
		return "", "", fmt.Errorf("backup file %s: %w", srcPath, statErr)
	}
	if !srcInfo.Mode().IsRegular() {
		return "", "", fmt.Errorf("backup source %s is not a regular file", srcPath)
	}

	dstExists := false
	if dstInfo, statErr := os.Stat(dbPath); statErr == nil {
		dstExists = true
		if os.SameFile(srcInfo, dstInfo) {
			return "", "", fmt.Errorf("backup source %s is the live database at %s; choose a snapshot from the backups directory instead", srcPath, dbPath)
		}
	} else if !os.IsNotExist(statErr) {
		return "", "", fmt.Errorf("stat existing db %s: %w", dbPath, statErr)
	}

	// Fully materialize the source before touching the live path at all:
	// this is what turns a bad source (directory, unreadable file, disk
	// full mid-copy) into a no-op failure instead of a stranded db.
	stagedPath, err := stageBackupCopy(srcPath, dbPath)
	if err != nil {
		return "", "", fmt.Errorf("stage backup %s: %w", srcPath, err)
	}
	stagedNeedsCleanup := true
	defer func() {
		if stagedNeedsCleanup {
			_ = os.Remove(stagedPath)
		}
	}()

	if dstExists {
		preservedAs, err = preserveExistingDB(dbPath)
		if err != nil {
			return "", "", err
		}
	} else {
		// Nothing existed at dbPath to preserve; any stray sidecars are not
		// tied to a preserved copy, so remove them as before.
		for _, suffix := range []string{"-wal", "-shm"} {
			_ = os.Remove(dbPath + suffix)
		}
	}

	if err := os.Rename(stagedPath, dbPath); err != nil {
		if preservedAs != "" {
			// The live path is now missing because the preserve-rename
			// already happened; put the preserved copy back rather than
			// leaving dbPath gone.
			_ = os.Rename(preservedAs, dbPath)
			for _, suffix := range []string{"-wal", "-shm"} {
				_ = os.Rename(preservedAs+suffix, dbPath+suffix)
			}
		}
		return "", "", fmt.Errorf("move staged backup into place: %w", err)
	}
	stagedNeedsCleanup = false

	return srcPath, preservedAs, nil
}

// preserveExistingDB renames dbPath (and its -wal/-shm sidecars, if any) to a
// collision-proof dbPath+".pre-restore-<UTC ts>[-N]" path, never deleting
// them: they can hold uncheckpointed data that only exists there. SQLite
// derives sidecar names by appending to the main filename, so renaming both
// to preservedAs+suffix keeps the preserved copy openable and consistent.
//
// The timestamp alone is only second-resolution, so two restores within the
// same second would otherwise collide; the -N suffix loop makes the chosen
// name unique regardless of how many restores land in the same second. The
// loop checks the main file AND both sidecar targets before accepting a
// candidate — a candidate whose main-file path is free but whose -wal or
// -shm path is already taken (e.g. from an earlier restore that had no
// sidecars to preserve, leaving that name's main-file slot free) would
// otherwise let a later rename silently replace that stray file.
//
// If the main-file rename succeeds but a sidecar rename then fails, every
// earlier move in this call is rolled back (in reverse order) before
// returning, so a sidecar-only filesystem error never strands dbPath
// missing; a rollback failure is wrapped alongside the original error
// instead of being discarded.
func preserveExistingDB(dbPath string) (string, error) {
	return preserveExistingDBAt(dbPath, time.Now, os.Rename)
}

// preserveExistingDBAt is preserveExistingDB with the clock and the rename
// operation injected, so tests can force a same-second collision
// deterministically and force a specific rename in the sequence to fail
// without relying on real filesystem-permission tricks.
func preserveExistingDBAt(dbPath string, now func() time.Time, rename func(oldpath, newpath string) error) (preservedAs string, err error) {
	base := dbPath + ".pre-restore-" + now().UTC().Format("20060102-150405")
	preservedAs = base
	for n := 2; ; n++ {
		free, err := preserveTargetFree(preservedAs)
		if err != nil {
			return "", fmt.Errorf("check preserve target %s: %w", preservedAs, err)
		}
		if free {
			break
		}
		preservedAs = fmt.Sprintf("%s-%d", base, n)
	}

	type move struct{ from, to string }
	var moved []move
	rollback := func() error {
		var rbErr error
		for i := len(moved) - 1; i >= 0; i-- {
			if err := os.Rename(moved[i].to, moved[i].from); err != nil {
				rbErr = errors.Join(rbErr, fmt.Errorf("restore %s: %w", moved[i].from, err))
			}
		}
		return rbErr
	}

	if err := rename(dbPath, preservedAs); err != nil {
		return "", fmt.Errorf("preserve existing db %s: %w", dbPath, err)
	}
	moved = append(moved, move{from: dbPath, to: preservedAs})

	for _, suffix := range []string{"-wal", "-shm"} {
		from, to := dbPath+suffix, preservedAs+suffix
		if err := rename(from, to); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			if rbErr := rollback(); rbErr != nil {
				return "", fmt.Errorf("preserve existing db sidecar %s: %w (rollback also failed: %v)", from, err, rbErr)
			}
			return "", fmt.Errorf("preserve existing db sidecar %s: %w", from, err)
		}
		moved = append(moved, move{from: from, to: to})
	}
	return preservedAs, nil
}

// preserveTargetFree reports whether candidate is available to become a
// preserve-rename destination: neither the main file nor either -wal/-shm
// sidecar target may already exist there.
func preserveTargetFree(candidate string) (bool, error) {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, err := os.Stat(candidate + suffix); err == nil {
			return false, nil
		} else if !os.IsNotExist(err) {
			return false, fmt.Errorf("stat %s: %w", candidate+suffix, err)
		}
	}
	return true, nil
}

// latestRotatingBackup returns the newest canonical rotating backup
// (attn-<timestamp>.db) in dir, by lexical (== chronological, fixed-width
// timestamp) sort. It defers to store.IsRotatingBackupName for the canonical
// filename check (validated timestamp suffix, not just a prefix/suffix
// match), so a stray non-canonical attn-*.db file cannot be mistaken for the
// latest backup. Pre-migration snapshots (attn-premigration-*.db) are
// excluded — "latest" only ever means the newest routine rotation.
func latestRotatingBackup(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read backups dir %s: %w", dir, err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !store.IsRotatingBackupName(name) {
			continue
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return "", fmt.Errorf("no rotating backups found in %s", dir)
	}
	sort.Strings(names)
	return filepath.Join(dir, names[len(names)-1]), nil
}

// stageBackupCopy copies src's bytes into a new, uniquely-named temp file in
// dstPath's own directory (never dstPath itself), preserving src's file
// mode, and fsyncs it before returning. It never removes src — the backup
// being restored from is left in place. On any error the temp file is
// removed and the caller gets no path to clean up.
//
// Staging in dstPath's directory (rather than copying straight onto dstPath)
// is what makes the final move a same-filesystem os.Rename: atomic, and safe
// to attempt even if the source turns out to be bad, because nothing at
// dstPath has been touched yet.
func stageBackupCopy(src, dstPath string) (stagedPath string, err error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return "", err
	}

	tmp, err := os.CreateTemp(filepath.Dir(dstPath), filepath.Base(dstPath)+".restore-tmp-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	succeeded := false
	defer func() {
		if !succeeded {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		tmp.Close()
		return "", err
	}
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}

	succeeded = true
	return tmpPath, nil
}

// acquireDaemonLock acquires and holds the exclusive advisory flock on
// pidPath, creating the file if it does not yet exist, and returns a release
// func the caller must call exactly once (a second call is a no-op) when the
// held section is over.
//
// This mirrors the liveness+ownership technique stopProfileDaemon
// (profile.go) uses: the daemon holds this same exclusive lock on its pid
// file for its whole lifetime (daemon.acquirePIDLock), so successfully
// acquiring it ourselves proves no live daemon owns it — the pid on disk, if
// any, is stale. Unlike a probe-then-release check, the caller keeps holding
// the lock (via the returned release func) for as long as it needs mutual
// exclusion, so a daemon cannot start, and a second caller of
// acquireDaemonLock cannot proceed, until release is called.
//
// The lock file is never unlinked here — only unlocked and closed — so a
// second acquireDaemonLock always contends on the same inode rather than
// racing a delete-then-recreate against a concurrent acquirer.
//
// flockFn is indirected to syscall.Flock so tests can inject a non-EWOULDBLOCK
// failure (e.g. standing in for ENOLCK) without needing real OS-level
// conditions to trigger it — EWOULDBLOCK is the only signal that means a live
// daemon actually holds the lock.
var flockFn = syscall.Flock

func acquireDaemonLock(pidPath string) (release func(), err error) {
	lockFile, err := os.OpenFile(pidPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("open pid file %s: %w", pidPath, err)
	}
	if flockErr := flockFn(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); flockErr != nil {
		lockFile.Close()
		if errors.Is(flockErr, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("the attn daemon is running; stop it first (quit the app, or `attn daemon stop`) before restoring the database")
		}
		// Any other flock failure means we cannot determine whether a
		// daemon holds the lock. Fail closed rather than risk racing a
		// live daemon: never proceed with the restore on an inconclusive
		// lock result.
		return nil, fmt.Errorf("cannot determine daemon state: %w", flockErr)
	}
	// We now hold the lock, but the pid file may still contain a stale pid
	// left by the last daemon to hold it. Stamp a sentinel over that content
	// so a concurrent `attn daemon stop` (daemonctl.Stop), which trusts only
	// content written by the current holder, never signals that stale pid.
	// The sentinel is never restored on release — content is only
	// meaningful while the lock is held, and the next daemon to acquire the
	// lock overwrites it with its own pid as it always has.
	if err := lockFile.Truncate(0); err != nil {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		lockFile.Close()
		return nil, fmt.Errorf("stamp non-daemon holder sentinel: %w", err)
	}
	if _, err := lockFile.WriteAt([]byte(daemonctl.NonDaemonHolderSentinel), 0); err != nil {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		lockFile.Close()
		return nil, fmt.Errorf("stamp non-daemon holder sentinel: %w", err)
	}
	if err := lockFile.Sync(); err != nil {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		lockFile.Close()
		return nil, fmt.Errorf("stamp non-daemon holder sentinel: %w", err)
	}
	var once sync.Once
	release = func() {
		once.Do(func() {
			_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
			_ = lockFile.Close()
		})
	}
	return release, nil
}
