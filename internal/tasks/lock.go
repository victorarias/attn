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
// the tasks dir for this root. The orphan-recovery and single-worker guarantees
// assume at most one live worker per root: two workers would both claim the same
// record, both save StateRunning (atomic rename = last-writer-wins, no torn
// file), and both invoke the executor concurrently — double-applying the durable
// write (e.g. double compaction). The CommitGuard is a per-process in-memory
// latch with no cross-process coordination, so nothing else can fence that.
var ErrAlreadyRunning = errors.New("tasks: another runner already owns this notebook root")

// lockFileName is the single-instance ownership marker under the tasks dir.
const lockFileName = ".runner.lock"

// acquireLock takes exclusive ownership of the tasks dir for this process by
// creating <tasksDir>/.runner.lock with O_EXCL. If the file already exists it
// either belongs to a live process (refuse with ErrAlreadyRunning) or to a
// crashed one (stale ⇒ steal it). The PID inside lets a restart after a crash
// reclaim the lock instead of wedging forever. Returns the path of the acquired
// lock so the caller can release it on Stop.
func (s *store) acquireLock() (string, error) {
	if err := s.init(); err != nil {
		return "", err
	}
	path := filepath.Join(stateDir(s.root), lockFileName)
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
		if pid, alive := s.lockHolderAlive(path); alive {
			return "", fmt.Errorf("%w (held by pid %d)", ErrAlreadyRunning, pid)
		}
		// Stale lock from a crashed process: remove it and retry the O_EXCL create.
		// The retry loop closes the race where two starters both see it stale.
		if rmErr := os.Remove(path); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			return "", rmErr
		}
		s.log("tasks: reclaimed stale runner lock at %s", path)
	}
}

// lockHolderAlive reports the PID recorded in the lock file and whether that
// process is still alive. An unreadable/garbage lock is treated as stale (alive
// false) so a corrupt marker can never wedge startup permanently. A lock with no
// readable PID is also treated as stale.
func (s *store) lockHolderAlive(path string) (pid int, alive bool) {
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

// releaseLock removes the lock file if it still belongs to this process. It is
// best-effort: a failure to remove is logged, not returned, because Stop must not
// block shutdown on a lock-file cleanup error.
func (s *store) releaseLock(path string) {
	if path == "" {
		return
	}
	if pid, _ := s.lockHolderAlive(path); pid != 0 && pid != os.Getpid() {
		// Another process re-acquired the lock after we crashed/stalled; do not
		// delete its marker.
		return
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.log("tasks: release runner lock %s: %v", path, err)
	}
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
