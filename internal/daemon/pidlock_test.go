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
func TestDaemon_ReleasePIDLock_DoesNotOrphanAConcurrentHolder(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "attn.pid")

	d := &Daemon{pidPath: pidPath}
	if err := d.acquirePIDLock(); err != nil {
		t.Fatalf("acquirePIDLock error: %v", err)
	}

	// The daemon shuts down and releases.
	d.releasePIDLock()

	// A concurrent holder (the restore stand-in) acquires the still-present
	// file immediately after release, before any new daemon starts.
	restoreHolder, err := os.OpenFile(pidPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		t.Fatalf("open pid file as restore stand-in: %v", err)
	}
	defer restoreHolder.Close()
	if err := syscall.Flock(int(restoreHolder.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("restore stand-in flock: %v", err)
	}

	// A new daemon startup attempting to acquire the lock at the same
	// pathname while the restore stand-in holds it must be excluded.
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
