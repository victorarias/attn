package daemonctl

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	stopSigtermWait = 5 * time.Second
	stopSigkillWait = 2 * time.Second
)

// StopResult describes the outcome of a Stop call.
type StopResult struct {
	Stopped bool   // a live daemon was signaled and exited
	Forced  bool   // SIGKILL escalation was required
	PID     int    // the pid that was signaled (0 when nothing was signaled)
	Note    string // human-readable detail for nil-error, not-stopped outcomes, e.g. "not running (no pid file)"
}

// Stop stops the daemon that owns pidPath, using the pid file's exclusive
// flock as the liveness+ownership gate (mirrors stopProfileDaemon's semantics
// exactly): if the lock can be acquired, the pid on disk is stale and must
// never be signaled. Not-running outcomes (no pid file, stale pid file,
// ESRCH on signal) are nil-error results with Stopped=false and a Note.
// Errors are reserved for genuine failures: open/read failures, malformed
// pid contents, a pid matching this process or its parent (refuse), SIGTERM
// delivery failure, or a process that survives SIGKILL escalation.
func Stop(pidPath string) (StopResult, error) {
	lockFile, err := os.OpenFile(pidPath, os.O_RDWR, 0)
	if os.IsNotExist(err) {
		return StopResult{Note: "not running (no pid file)"}, nil
	}
	if err != nil {
		return StopResult{}, fmt.Errorf("could not open pid file: %w", err)
	}
	if flockErr := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); flockErr == nil {
		// Acquired the lock → no live daemon holds it. The pid on disk is
		// stale; signaling it could hit a recycled, unrelated process.
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		lockFile.Close()
		return StopResult{Note: "not running (stale pid file)"}, nil
	}
	lockFile.Close()

	// The lock is held → a live daemon owns this file and wrote its own pid
	// into it under the lock, so the pid genuinely names that daemon: safe
	// to signal.
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return StopResult{}, fmt.Errorf("could not read pid file: %w", err)
	}
	pidText := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(pidText)
	if err != nil || pid <= 0 {
		return StopResult{}, fmt.Errorf("malformed pid file %q", pidText)
	}
	// Never signal our own process tree (e.g. stopping the profile we're
	// running under): killing it would take down this very command.
	if pid == os.Getpid() || pid == os.Getppid() {
		return StopResult{}, fmt.Errorf("refusing to stop pid %d: it is this command's own process tree", pid)
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		if err == syscall.ESRCH {
			return StopResult{Note: "not running (stale pid file)"}, nil
		}
		return StopResult{}, fmt.Errorf("SIGTERM pid %d failed: %w", pid, err)
	}
	if processGoneWithin(pid, stopSigtermWait) {
		return StopResult{Stopped: true, PID: pid}, nil
	}
	// Escalate: don't leave a wedged process holding the pid file/data dir.
	_ = syscall.Kill(pid, syscall.SIGKILL)
	if processGoneWithin(pid, stopSigkillWait) {
		return StopResult{Stopped: true, Forced: true, PID: pid}, nil
	}
	return StopResult{}, fmt.Errorf("pid %d did not exit after SIGKILL", pid)
}

// processGoneWithin polls `kill(pid, 0)` until the process is gone (ESRCH) or
// the deadline passes.
func processGoneWithin(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}
