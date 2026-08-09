package daemonctl

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	stopSigtermWait = 5 * time.Second
	stopSigkillWait = 2 * time.Second
)

// NonDaemonHolderSentinel is written into the pid file by non-daemon lock
// holders (e.g. `attn db restore`), so a concurrent Stop observing a held lock
// never trusts a pid the current holder didn't write.
const NonDaemonHolderSentinel = "non-daemon-holder"

// flockFn is indirected so tests can inject a non-EWOULDBLOCK failure. Only
// EWOULDBLOCK means the lock is held; every other flock error fails closed.
var flockFn = syscall.Flock

// StopResult describes the outcome of a Stop call.
type StopResult struct {
	Stopped bool   // a live daemon was signaled and exited
	Forced  bool   // SIGKILL escalation was required
	PID     int    // the pid that was signaled (0 when nothing was signaled)
	Note    string // human-readable detail for nil-error, not-stopped outcomes, e.g. "not running (no pid file)"
}

// Stop stops the daemon that owns pidPath. The pid file's exclusive flock is
// the liveness+ownership gate (mirrors stopProfileDaemon): an acquirable lock
// means any pid on disk is stale and never signaled; only EWOULDBLOCK means
// held, any other flock error fails closed. Not-running outcomes are
// nil-error results with Stopped=false and a Note; errors are genuine failures.
func Stop(pidPath string) (StopResult, error) {
	lockFile, err := os.OpenFile(pidPath, os.O_RDWR, 0)
	if os.IsNotExist(err) {
		return StopResult{Note: "not running (no pid file)"}, nil
	}
	if err != nil {
		return StopResult{}, fmt.Errorf("could not open pid file: %w", err)
	}
	if flockErr := flockFn(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); flockErr == nil {
		// Acquired → no live holder; the pid on disk is stale and could be a
		// recycled, unrelated process.
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		lockFile.Close()
		return StopResult{Note: "not running (stale pid file)"}, nil
	} else if !errors.Is(flockErr, syscall.EWOULDBLOCK) {
		// Indeterminate flock result: fail closed.
		lockFile.Close()
		return StopResult{}, fmt.Errorf("cannot determine daemon state: %w", flockErr)
	}
	lockFile.Close()

	// Lock held: trust only content written under the lock — the daemon's own
	// pid or NonDaemonHolderSentinel. Only numeric content is signalable.
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
	// Never signal our own process tree.
	if pid == os.Getpid() || pid == os.Getppid() {
		return StopResult{}, fmt.Errorf("refusing to stop pid %d: it is this command's own process tree", pid)
	}
	// A holder acquires the flock before writing its pid, so a racing Stop can
	// see EWOULDBLOCK plus stale content. Require positive proof: pid must
	// have the file open right now, or it is never signaled.
	holds, err := pidHoldsPIDFile(pid, pidPath)
	if err != nil {
		return StopResult{}, fmt.Errorf("could not verify pid %d holds the daemon lock: %w", pid, err)
	}
	if !holds {
		return StopResult{Note: fmt.Sprintf("not running (pid %d does not hold the daemon lock — if a daemon is starting up right now, retry in a moment)", pid)}, nil
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

// lsofPath resolves `lsof` once: PATH first, then its standard macOS location,
// since Stop may run with a trimmed PATH.
var lsofPath = resolveLsofPath()

func resolveLsofPath() string {
	if p, err := exec.LookPath("lsof"); err == nil {
		return p
	}
	return "/usr/sbin/lsof"
}

// pidHoldsPIDFile reports whether pid has pidPath open (`lsof -t`) — the
// positive proof that closes the acquire-flock-then-write ordering gap.
// Fail-closed on exec/parse errors; lsof exit 1 with no output means no holder.
// It tests membership, not exclusivity, so Stop's own open fd is harmless.
func pidHoldsPIDFile(pid int, pidPath string) (bool, error) {
	cmd := exec.Command(lsofPath, "-t", "--", pidPath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok && exitErr.ExitCode() == 1 && stdout.Len() == 0 {
			// lsof's documented "nothing matched" exit status.
			return false, nil
		}
		return false, fmt.Errorf("lsof -t %s: %w (stderr: %s)", pidPath, runErr, strings.TrimSpace(stderr.String()))
	}
	scanner := bufio.NewScanner(&stdout)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		holderPID, err := strconv.Atoi(line)
		if err != nil {
			return false, fmt.Errorf("lsof -t %s: unexpected output line %q", pidPath, line)
		}
		if holderPID == pid {
			return true, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("lsof -t %s: read output: %w", pidPath, err)
	}
	return false, nil
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
