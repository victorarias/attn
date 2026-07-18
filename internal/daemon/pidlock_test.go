package daemon

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestDaemon_ReleasePIDLock_LeavesFileInPlace proves releasePIDLock unlocks
// the PID file without unlinking it. The flock, not the file's presence, is
// the sole mutual-exclusion signal shared with `attn db restore`
// (cmd/attn/db.go's acquireDaemonLock): if release unlinked the pathname, a
// concurrent holder of the old inode's flock and a subsequent daemon
// startup's O_CREATE-created new inode at the same pathname would never
// contend with each other.
func TestDaemon_ReleasePIDLock_LeavesFileInPlace(t *testing.T) {
	dir := t.TempDir()
	d := &Daemon{pidPath: filepath.Join(dir, "attn.pid")}

	if err := d.acquirePIDLock(); err != nil {
		t.Fatalf("acquirePIDLock error: %v", err)
	}
	d.releasePIDLock()

	if _, err := os.Stat(d.pidPath); err != nil {
		t.Fatalf("expected pid file to remain on disk after release, stat err = %v", err)
	}

	// A fresh acquire must succeed on the very same pathname/inode — proving
	// the flock was actually released, not just abandoned.
	second := &Daemon{pidPath: d.pidPath}
	if err := second.acquirePIDLock(); err != nil {
		t.Fatalf("second acquirePIDLock after release error: %v", err)
	}
	second.releasePIDLock()
}

// TestDaemon_ReleasePIDLock_DoesNotOrphanAConcurrentHolder proves the
// specific race this fix closes: a third party that flocked the PID file
// while the daemon held it (standing in for `attn db restore`'s
// acquireDaemonLock, which holds the lock across an entire restore) keeps
// exclusive custody of that same inode straight through releasePIDLock, so
// a subsequent daemon-style acquire attempt on the same pathname is still
// correctly excluded rather than silently succeeding against a
// newly-created inode.
//
// The ordering here matters: the stand-in's fd is opened WHILE the daemon
// still holds the lock (mirroring `attn db restore`'s real timing, where
// acquireDaemonLock races to open and flock the daemon's live pid file
// before/while it might be released) and deliberately WITHOUT O_CREATE, so
// the open can only succeed against the daemon's already-created pathname
// and the fd is proven to reference the daemon's original inode. If the fd
// were instead opened after releasePIDLock (as a prior version of this test
// did), an old buggy releasePIDLock that unlinks the pid file would still
// let O_CREATE silently fabricate a fresh inode at that step, and the test
// would pass for the wrong reason — it wouldn't be holding the daemon's
// inode across the release at all, so it could never observe an orphan.
func TestDaemon_ReleasePIDLock_DoesNotOrphanAConcurrentHolder(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "attn.pid")

	d := &Daemon{pidPath: pidPath}
	if err := d.acquirePIDLock(); err != nil {
		t.Fatalf("acquirePIDLock error: %v", err)
	}

	// The restore stand-in opens an fd on the pid file WHILE the daemon
	// still holds the lock. No O_CREATE: the file must already exist
	// because the daemon created it, proving this fd references the
	// daemon's inode rather than one the stand-in fabricated itself.
	restoreHolder, err := os.OpenFile(pidPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open pid file as restore stand-in: %v", err)
	}
	defer restoreHolder.Close()

	restoreHolderInfo, err := restoreHolder.Stat()
	if err != nil {
		t.Fatalf("stat restore stand-in fd: %v", err)
	}

	// The daemon shuts down and releases.
	d.releasePIDLock()

	// The stand-in can now acquire the flock on that same inode — it was
	// merely blocked by the daemon's lock, not by a missing/replaced file.
	if err := syscall.Flock(int(restoreHolder.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("restore stand-in flock after daemon release: %v", err)
	}

	// Belt-and-suspenders: confirm the pathname still resolves to the same
	// inode the stand-in's fd is holding the lock on, rather than relying
	// solely on the flock success above.
	if pathInfo, err := os.Stat(pidPath); err != nil {
		t.Fatalf("stat pid path after release: %v", err)
	} else if !os.SameFile(restoreHolderInfo, pathInfo) {
		t.Fatal("releasePIDLock changed the inode at pidPath; restore stand-in's held fd now refers to an orphaned inode")
	}

	// A new daemon startup attempting to acquire the lock at the same
	// pathname while the restore stand-in holds it must be excluded. Under
	// the old unlink-on-release behavior, the pathname would have been
	// unlinked back at releasePIDLock, so this acquire's O_CREATE would
	// create a brand-new, different inode and wrongly succeed here — that
	// is the regression this test exists to catch.
	next := &Daemon{pidPath: pidPath}
	if err := next.acquirePIDLock(); err == nil {
		t.Fatal("expected daemon acquirePIDLock to be excluded while the restore stand-in holds the lock")
	}

	// Once the restore stand-in releases, the daemon must be able to start.
	syscall.Flock(int(restoreHolder.Fd()), syscall.LOCK_UN)
	restoreHolder.Close()

	if err := next.acquirePIDLock(); err != nil {
		t.Fatalf("expected daemon acquirePIDLock to succeed after the restore stand-in released, got: %v", err)
	}
	next.releasePIDLock()
}
