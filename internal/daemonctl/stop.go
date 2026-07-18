package daemonctl

import (
	"errors"
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

// NonDaemonHolderSentinel is written into the pid file by non-daemon
// processes that take the daemon lock (e.g. `attn db restore`, via
// cmd/attn's acquireDaemonLock), replacing any stale daemon pid, so a
// concurrent Stop observing a held lock never trusts a pid the current
// holder didn't write. Content is only meaningful while the lock is held;
// the next daemon acquire overwrites it with its own pid as usual.
const NonDaemonHolderSentinel = "non-daemon-holder"

// flockFn is indirected to syscall.Flock so tests can inject a
// non-EWOULDBLOCK failure (e.g. standing in for ENOLCK) without needing real
// OS-level conditions to trigger it — EWOULDBLOCK is the only signal that
// means a live daemon (or another non-daemon holder) actually holds the
// lock; every other flock error is indeterminate and must fail closed.
var flockFn = syscall.Flock

// StopResult describes the outcome of a Stop call.
type StopResult struct {
	Stopped bool   // a live daemon was signaled and exited
	Forced  bool   // SIGKILL escalation was required
	PID     int    // the pid that was signaled (0 when nothing was signaled)
	Note    string // human-readable detail for nil-error, not-stopped outcomes, e.g. "not running (no pid file)"
}

// Stop stops the daemon that owns pidPath, using the pid file's exclusive
// flock as the liveness+ownership gate (mirrors stopProfileDaemon's semantics
// exactly): if the lock can be acquired, no daemon or other holder owns the
// file, so any pid on disk is stale and must never be signaled. Only
// EWOULDBLOCK on the flock attempt means the lock is genuinely held; any
// other flock error is indeterminate and fails closed rather than trusting a
// pid nobody currently vouches for. When the lock is held, only content the
// current holder is known to have written under that lock is trusted: the
// daemon's own pid, or NonDaemonHolderSentinel for non-daemon holders like
// `attn db restore` — anything else is a malformed-pid error. Not-running
// outcomes (no pid file, stale pid file, sentinel-held lock, ESRCH on
// signal) are nil-error results with Stopped=false and a Note. Errors are
// reserved for genuine failures: open/read failures, an indeterminate flock
// result, malformed pid contents, a pid matching this process or its parent
// (refuse), SIGTERM delivery failure, or a process that survives SIGKILL
// escalation.
func Stop(pidPath string) (StopResult, error) {
	lockFile, err := os.OpenFile(pidPath, os.O_RDWR, 0)
	if os.IsNotExist(err) {
		return StopResult{Note: "not running (no pid file)"}, nil
	}
	if err != nil {
		return StopResult{}, fmt.Errorf("could not open pid file: %w", err)
	}
	if flockErr := flockFn(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); flockErr == nil {
		// Acquired the lock → no live daemon (or other holder) owns it. The
		// pid on disk is stale; signaling it could hit a recycled, unrelated
		// process.
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		lockFile.Close()
		return StopResult{Note: "not running (stale pid file)"}, nil
	} else if !errors.Is(flockErr, syscall.EWOULDBLOCK) {
		// Any other flock failure means we cannot determine whether the
		// lock is held. Fail closed rather than risk trusting a pid nobody
		// currently vouches for.
		lockFile.Close()
		return StopResult{}, fmt.Errorf("cannot determine daemon state: %w", flockErr)
	}
	lockFile.Close()

	// The lock is held (EWOULDBLOCK) → some process owns this file and, by
	// convention, wrote content under the lock proving what it is: the
	// daemon writes its own pid, and non-daemon holders (acquireDaemonLock,
	// e.g. `attn db restore`) overwrite it with NonDaemonHolderSentinel. Only
	// numeric content is trusted as a signalable pid.
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return StopResult{}, fmt.Errorf("could not read pid file: %w", err)
	}
	pidText := strings.TrimSpace(string(data))
	if pidText == NonDaemonHolderSentinel {
		return StopResult{Note: "not running (daemon lock held by another attn process, e.g. a database restore in progress)"}, nil
	}
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
