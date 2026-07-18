package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/config"
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

	restoredFrom, preservedAs, err := restoreDatabase(dbPath, backupsDir, source, isDaemonRunningAt(config.DataDir))
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

// restoreDatabase implements `attn db restore` against explicit paths, with an
// injected daemonRunning check so it is fully unit-testable against temp
// dirs: it never resolves config's real data dir itself.
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
// daemonRunning reports whether a live daemon holds the pid-file lock. An
// error from it (anything other than a clean "not running"/"running"
// determination) must not be silently treated as "not running" — that would
// let a destructive restore proceed while daemon liveness is actually
// unknown — so restoreDatabase refuses and surfaces the error instead.
func restoreDatabase(dbPath, backupsDir, source string, daemonRunning func() (bool, error)) (restoredFrom, preservedAs string, err error) {
	running, err := daemonRunning()
	if err != nil {
		return "", "", fmt.Errorf("cannot determine daemon state: %w", err)
	}
	if running {
		return "", "", fmt.Errorf("the attn daemon is running; stop it first (quit the app, or `attn daemon stop`) before restoring the database")
	}

	srcPath := strings.TrimSpace(source)
	if srcPath == "" || srcPath == "latest" {
		srcPath, err = latestRotatingBackup(backupsDir)
		if err != nil {
			return "", "", err
		}
	} else if _, statErr := os.Stat(srcPath); statErr != nil {
		return "", "", fmt.Errorf("backup file %s: %w", srcPath, statErr)
	}

	if _, statErr := os.Stat(dbPath); statErr == nil {
		preservedAs = dbPath + ".pre-restore-" + time.Now().UTC().Format("20060102-150405")
		if err := os.Rename(dbPath, preservedAs); err != nil {
			return "", "", fmt.Errorf("preserve existing db %s: %w", dbPath, err)
		}
		// Preserve the -wal/-shm sidecars alongside the renamed db, never
		// delete them: they can hold uncheckpointed data that only exists
		// there. SQLite derives sidecar names by appending to the main
		// filename, so renaming both to preservedAs+suffix keeps the
		// preserved copy openable and consistent.
		for _, suffix := range []string{"-wal", "-shm"} {
			if err := os.Rename(dbPath+suffix, preservedAs+suffix); err != nil && !os.IsNotExist(err) {
				return "", "", fmt.Errorf("preserve existing db sidecar %s: %w", dbPath+suffix, err)
			}
		}
	} else if !os.IsNotExist(statErr) {
		return "", "", fmt.Errorf("stat existing db %s: %w", dbPath, statErr)
	} else {
		// Nothing existed at dbPath to preserve; any stray sidecars are not
		// tied to a preserved copy, so remove them as before.
		for _, suffix := range []string{"-wal", "-shm"} {
			_ = os.Remove(dbPath + suffix)
		}
	}

	if err := copyFileContents(srcPath, dbPath); err != nil {
		return "", "", fmt.Errorf("copy backup %s into place: %w", srcPath, err)
	}

	return srcPath, preservedAs, nil
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

// copyFileContents copies src's bytes into dst (creating/truncating dst),
// preserving src's file mode. It never removes src — the backup being
// restored from is left in place.
func copyFileContents(src, dst string) (err error) {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := out.Close(); err == nil {
			err = closeErr
		}
	}()

	_, err = io.Copy(out, in)
	return err
}

// isDaemonRunningAt reports whether a live daemon holds the exclusive flock on
// dataDir()'s pid file. dataDir is a func rather than a plain string so tests
// can inject a throwaway temp dir without dbRestore ever touching config's
// real resolution.
//
// This mirrors the liveness+ownership technique stopProfileDaemon (profile.go)
// uses: the daemon holds an exclusive advisory lock on its pid file for its
// whole lifetime (daemon.acquirePIDLock), so successfully acquiring the lock
// ourselves proves no live daemon owns it — the pid on disk, if any, is stale.
//
// The returned func distinguishes a clean "not running" determination from an
// error: no pid file (or a lock we can acquire) is (false, nil), a lock held
// by another process is (true, nil), and any other OpenFile failure (e.g.
// permission denied) is (false, err) — never silently "not running", which
// would let a destructive restore proceed while liveness is actually unknown.
func isDaemonRunningAt(dataDir func() string) func() (bool, error) {
	return func() (bool, error) {
		pidPath := filepath.Join(dataDir(), "attn.pid")
		lockFile, err := os.OpenFile(pidPath, os.O_RDWR, 0)
		if err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}
			return false, fmt.Errorf("open pid file %s: %w", pidPath, err)
		}
		defer lockFile.Close()
		if flockErr := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); flockErr == nil {
			_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
			return false, nil
		}
		return true, nil
	}
}
