package tasks

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// ErrAlreadyRunning is returned by Start when another live Runner already owns
// the lock dir. The orphan-recovery and single-instance guarantees assume at most
// one live Runner per store: two Runners would both claim the same record, both
// save StateRunning (atomic rename = last-writer-wins, no torn file), and both
// invoke the executor concurrently — double-applying the durable write (e.g.
// double compaction). Per-kind concurrency bounds parallelism WITHIN one Runner;
// it does nothing across processes. The CommitGuard is likewise a per-process
// in-memory latch with no cross-process coordination, so nothing else can fence
// that.
var ErrAlreadyRunning = errors.New("tasks: another runner already owns this store")

// lockFileName is the single-instance ownership marker inside the lock dir.
const lockFileName = ".runner.lock"

// AcquireDirLock takes exclusive single-instance ownership for this process by
// creating <dir>/.runner.lock with O_EXCL. If the file already exists it either
// belongs to a live process (refuse with ErrAlreadyRunning) or to a crashed one
// (stale ⇒ steal it). The PID inside lets a restart after a crash reclaim the lock
// instead of wedging forever. Returns the acquired lock path for ReleaseDirLock.
//
// It is a free function so both the file store (locking under the notebook tasks
// dir) and the daemon's SQLite adapter (locking under the profile data dir) share
// one implementation.
func AcquireDirLock(dir string, log LogFunc) (string, error) {
	if log == nil {
		log = func(string, ...interface{}) {}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, lockFileName)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			if _, werr := f.WriteString(strconv.Itoa(os.Getpid())); werr != nil {
				_ = f.Close()
				_ = os.Remove(path)
				return "", werr
			}
			if cerr := f.Close(); cerr != nil {
				return "", cerr
			}
			return path, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
		// The lock exists. Decide whether it is live (refuse) or stale (steal).
		if pid, alive := lockHolderAlive(path); alive {
			return "", fmt.Errorf("%w (held by pid %d)", ErrAlreadyRunning, pid)
		}
		// Stale lock from a crashed process: remove it and retry the O_EXCL create.
		// The retry loop closes the race where two starters both see it stale.
		if rmErr := os.Remove(path); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			return "", rmErr
		}
		log("tasks: reclaimed stale runner lock at %s", path)
	}
}

// ReleaseDirLock removes the lock file if it still belongs to this process. It is
// best-effort: a failure to remove is logged, not returned, because Stop must not
// block shutdown on a lock-file cleanup error.
func ReleaseDirLock(path string, log LogFunc) {
	if path == "" {
		return
	}
	if log == nil {
		log = func(string, ...interface{}) {}
	}
	if pid, _ := lockHolderAlive(path); pid != 0 && pid != os.Getpid() {
		// Another process re-acquired the lock after we crashed/stalled; do not
		// delete its marker.
		return
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		log("tasks: release runner lock %s: %v", path, err)
	}
}

// acquireLock / releaseLock keep the file store's method shape, delegating to the
// shared dir-lock helpers. The file store locks under its own .attn/tasks dir.
func (s *store) acquireLock() (string, error) { return AcquireDirLock(stateDir(s.root), s.log) }
func (s *store) releaseLock(path string)      { ReleaseDirLock(path, s.log) }

// lockHolderAlive reports the PID recorded in the lock file and whether that
// process is still alive. An unreadable/garbage lock is treated as stale (alive
// false) so a corrupt marker can never wedge startup permanently. A lock with no
// readable PID is also treated as stale.
func lockHolderAlive(path string) (pid int, alive bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err = strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	if pid == os.Getpid() {
		// Our own lock (shouldn't happen via Start, but be safe): treat as live so
		// we don't stomp it.
		return pid, true
	}
	return pid, processAlive(pid)
}

// processAlive reports whether a process with the given pid currently exists.
// On macOS (attn's only platform) signal 0 probes liveness without delivering a
// signal: ESRCH ⇒ gone, EPERM ⇒ alive but not ours, nil ⇒ alive.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	return errors.Is(err, syscall.EPERM)
}
