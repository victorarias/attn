package daemonctl

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestStopHelperProcess is not a real test: it is re-exec'd as a subprocess
// (the standard os/exec crash-test pattern, mirrored from
// internal/config/datadir_backstop_test.go) to model a process that actually
// holds the pid file's flock — something a same-process flock can never
// model, since flock is per-open-file-description, not per-process, but two
// different processes genuinely contend for it.
//
// Mode is selected via ATTN_STOP_TEST_HELPER_MODE:
//   - "lock-self": opens/creates the pid file, takes the exclusive flock,
//     writes its own pid, then blocks until killed.
//   - "lock-write-pid": same, but writes the pid from
//     ATTN_STOP_TEST_HELPER_WRITE_PID instead of its own (used to simulate
//     the pid file naming an unrelated process — e.g. the test parent —
//     while a *different* process holds the lock).
//
// Run under plain `go test`, ATTN_STOP_TEST_HELPER_MODE is unset, so this
// is a silent no-op.
func TestStopHelperProcess(t *testing.T) {
	mode := os.Getenv("ATTN_STOP_TEST_HELPER_MODE")
	if mode == "" {
		return
	}

	pidPath := os.Getenv("ATTN_STOP_TEST_HELPER_PIDPATH")
	if pidPath == "" {
		fmt.Fprintln(os.Stderr, "helper: missing ATTN_STOP_TEST_HELPER_PIDPATH")
		os.Exit(1)
	}

	f, err := os.OpenFile(pidPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "helper: open pid file: %v\n", err)
		os.Exit(1)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		fmt.Fprintf(os.Stderr, "helper: flock: %v\n", err)
		os.Exit(1)
	}

	var content string
	switch mode {
	case "lock-self":
		content = strconv.Itoa(os.Getpid())
	case "lock-write-pid":
		content = os.Getenv("ATTN_STOP_TEST_HELPER_WRITE_PID")
	case "lock-malformed":
		content = "not-a-pid"
	default:
		fmt.Fprintf(os.Stderr, "helper: unknown mode %q\n", mode)
		os.Exit(1)
	}
	if _, err := f.WriteString(content); err != nil {
		fmt.Fprintf(os.Stderr, "helper: write pid file: %v\n", err)
		os.Exit(1)
	}
	if err := f.Sync(); err != nil {
		fmt.Fprintf(os.Stderr, "helper: sync pid file: %v\n", err)
		os.Exit(1)
	}

	// Block until killed. An untrapped SIGTERM terminates the process by
	// default (Go does not install a handler unless the program calls
	// signal.Notify), which is exactly what Stop's SIGTERM step relies on.
	// A lone goroutine sleeping is not a runtime deadlock (unlike a bare
	// `select {}`, which the Go scheduler detects as "all goroutines are
	// asleep" and crashes on) — time.Sleep is backed by the runtime timer,
	// so it just waits.
	time.Sleep(time.Hour)
}

// spawnStopHelper starts TestStopHelperProcess as a subprocess in the given
// mode and reaps it as soon as it exits (a goroutine calling cmd.Wait()) so
// a helper killed by Stop never lingers as a zombie that would make
// processGoneWithin's kill(pid, 0) liveness probe see a "still there" zombie
// pid instead of a truly gone process.
func spawnStopHelper(t *testing.T, pidPath string, mode string, extraEnv ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestStopHelperProcess$")
	cmd.Env = append(os.Environ(),
		"ATTN_STOP_TEST_HELPER_MODE="+mode,
		"ATTN_STOP_TEST_HELPER_PIDPATH="+pidPath,
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn helper (mode %s): %v", mode, err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-done
	})
	return cmd
}

// waitForFlockHeld polls until some other process holds pidPath's exclusive
// flock (a non-blocking acquire attempt fails), or fails the test on timeout.
func waitForFlockHeld(t *testing.T, pidPath string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		f, err := os.OpenFile(pidPath, os.O_RDWR, 0)
		if err == nil {
			flockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
			if flockErr == nil {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				f.Close()
			} else {
				f.Close()
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s to become locked", pidPath)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitForPIDFileContent polls until pidPath's contents equal want, or fails
// the test on timeout.
func waitForPIDFileContent(t *testing.T, pidPath string, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		data, err := os.ReadFile(pidPath)
		if err == nil && strings.TrimSpace(string(data)) == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s to contain %q (last read: %q, err: %v)", pidPath, want, string(data), err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// isAlive reports whether pid still exists (kill(pid, 0) success or
// EPERM — either means the process is there; ESRCH means gone).
func isAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

func TestStop_NoPidFile(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "attn.pid")

	result, err := Stop(pidPath)
	if err != nil {
		t.Fatalf("Stop() error = %v, want nil", err)
	}
	if result.Stopped {
		t.Fatalf("Stop() = %+v, want Stopped=false", result)
	}
	if !strings.Contains(result.Note, "no pid file") {
		t.Fatalf("Stop().Note = %q, want it to mention 'no pid file'", result.Note)
	}
}

// TestStop_StalePidFile proves the must-catch safety property: a pid file
// that names a genuinely live process, but whose flock nobody holds, must
// never be signaled. This is the regression a broken liveness gate would
// cause — killing an unrelated process that happened to recycle the stale
// pid.
func TestStop_StalePidFile(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "attn.pid")

	helper := exec.Command("sleep", "30")
	if err := helper.Start(); err != nil {
		t.Fatalf("start sleep helper: %v", err)
	}
	t.Cleanup(func() {
		_ = helper.Process.Kill()
		_, _ = helper.Process.Wait()
	})

	// Write the pid file WITHOUT holding the flock — this is what a
	// crashed/SIGKILLed daemon leaves behind.
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(helper.Process.Pid)), 0644); err != nil {
		t.Fatalf("write stale pid file: %v", err)
	}

	result, err := Stop(pidPath)
	if err != nil {
		t.Fatalf("Stop() error = %v, want nil", err)
	}
	if result.Stopped {
		t.Fatalf("Stop() = %+v, want Stopped=false (must not signal an unlocked pid)", result)
	}
	if !strings.Contains(result.Note, "stale") {
		t.Fatalf("Stop().Note = %q, want it to mention 'stale'", result.Note)
	}
	if !isAlive(helper.Process.Pid) {
		t.Fatal("helper process is gone: Stop() signaled a pid it never held the lock for")
	}
}

// TestStop_LiveHolder proves the happy path against a real *different*
// process holding the flock, the way a live daemon does.
func TestStop_LiveHolder(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "attn.pid")

	helper := spawnStopHelper(t, pidPath, "lock-self")
	waitForPIDFileContent(t, pidPath, strconv.Itoa(helper.Process.Pid), 5*time.Second)
	waitForFlockHeld(t, pidPath, 5*time.Second)

	result, err := Stop(pidPath)
	if err != nil {
		t.Fatalf("Stop() error = %v, want nil", err)
	}
	if !result.Stopped {
		t.Fatalf("Stop() = %+v, want Stopped=true", result)
	}
	if result.PID != helper.Process.Pid {
		t.Fatalf("Stop().PID = %d, want %d", result.PID, helper.Process.Pid)
	}
	if result.Forced {
		t.Fatalf("Stop() = %+v, want Forced=false (helper exits cleanly on SIGTERM)", result)
	}
	if isAlive(helper.Process.Pid) {
		t.Fatal("helper process is still alive after Stop() reported Stopped=true")
	}
}

// TestStop_RefusesOwnProcessTree proves Stop refuses to signal a pid that
// names the calling process itself, even though the lock is genuinely held
// (by a different, spawned process) — the own-pid check must fire before
// any signal is sent.
func TestStop_RefusesOwnProcessTree(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "attn.pid")
	ownPID := os.Getpid()

	spawnStopHelper(t, pidPath, "lock-write-pid", "ATTN_STOP_TEST_HELPER_WRITE_PID="+strconv.Itoa(ownPID))
	waitForPIDFileContent(t, pidPath, strconv.Itoa(ownPID), 5*time.Second)
	waitForFlockHeld(t, pidPath, 5*time.Second)

	result, err := Stop(pidPath)
	if err == nil {
		t.Fatalf("Stop() = %+v, err = nil, want an own-process-tree refusal error", result)
	}
	if !strings.Contains(err.Error(), "own process tree") {
		t.Fatalf("Stop() error = %v, want it to mention 'own process tree'", err)
	}
	if result.Stopped {
		t.Fatalf("Stop() = %+v, want Stopped=false", result)
	}
	// This process is still running to observe this assertion, so there's
	// nothing further to prove about liveness — the point is simply that no
	// signal was attempted, which the error path guarantees.
}

// TestStop_NonDaemonHolderSentinel is the must-catch regression for the
// restore-vs-stop race figgyster flagged: a pid file seeded with the pid of
// a genuinely live process (standing in for a stale daemon pid left behind
// by a crash), whose flock is then taken by a non-daemon holder (standing in
// for `attn db restore` via acquireDaemonLock) that overwrites the content
// with NonDaemonHolderSentinel before blocking. Stop must see the sentinel,
// not the pid that was there before the holder took the lock, and must
// never signal it — even though the lock is genuinely held (EWOULDBLOCK),
// which alone used to be treated as "safe to trust the pid on disk".
func TestStop_NonDaemonHolderSentinel(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "attn.pid")

	// A live process whose pid stands in for a stale daemon pid left on
	// disk by a crash — this is what Stop must NOT signal.
	stalePidHolder := exec.Command("sleep", "30")
	if err := stalePidHolder.Start(); err != nil {
		t.Fatalf("start sleep helper: %v", err)
	}
	t.Cleanup(func() {
		_ = stalePidHolder.Process.Kill()
		_, _ = stalePidHolder.Process.Wait()
	})
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(stalePidHolder.Process.Pid)), 0644); err != nil {
		t.Fatalf("seed stale pid file: %v", err)
	}

	// A non-daemon holder (e.g. `attn db restore`) takes the lock and
	// stamps the sentinel over that stale pid, mirroring acquireDaemonLock.
	spawnStopHelper(t, pidPath, "lock-write-pid", "ATTN_STOP_TEST_HELPER_WRITE_PID="+NonDaemonHolderSentinel)
	waitForPIDFileContent(t, pidPath, NonDaemonHolderSentinel, 5*time.Second)
	waitForFlockHeld(t, pidPath, 5*time.Second)

	result, err := Stop(pidPath)
	if err != nil {
		t.Fatalf("Stop() error = %v, want nil", err)
	}
	if result.Stopped {
		t.Fatalf("Stop() = %+v, want Stopped=false (must not signal a pid the current lock holder didn't write)", result)
	}
	if !strings.Contains(result.Note, "another attn process") {
		t.Fatalf("Stop().Note = %q, want it to mention the lock being held by another attn process", result.Note)
	}
	if !isAlive(stalePidHolder.Process.Pid) {
		t.Fatal("stale-pid-holder process is gone: Stop() signaled a pid the current lock holder never wrote")
	}
}

// TestStop_NonContentionFlockErrorFailsClosed proves a flock failure other
// than EWOULDBLOCK (e.g. ENOLCK) is indeterminate and must not be treated as
// "no one holds the lock, the pid is stale" — that would let Stop signal a
// pid it has no actual proof is safe to signal.
func TestStop_NonContentionFlockErrorFailsClosed(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "attn.pid")

	helper := exec.Command("sleep", "30")
	if err := helper.Start(); err != nil {
		t.Fatalf("start sleep helper: %v", err)
	}
	t.Cleanup(func() {
		_ = helper.Process.Kill()
		_, _ = helper.Process.Wait()
	})
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(helper.Process.Pid)), 0644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	originalFlockFn := flockFn
	flockFn = func(fd int, how int) error {
		return syscall.ENOLCK
	}
	t.Cleanup(func() { flockFn = originalFlockFn })

	result, err := Stop(pidPath)
	if err == nil {
		t.Fatalf("Stop() = %+v, err = nil, want an indeterminate-state error", result)
	}
	if !strings.Contains(err.Error(), "cannot determine daemon state") {
		t.Fatalf("Stop() error = %v, want it to mention the indeterminate-state message", err)
	}
	if result.Stopped {
		t.Fatalf("Stop() = %+v, want Stopped=false", result)
	}
	if !isAlive(helper.Process.Pid) {
		t.Fatal("helper process is gone: Stop() signaled a pid on an inconclusive flock result")
	}
}

// TestStop_MalformedPIDFile proves malformed content under a held lock is an
// error, and (implicitly, since no pid could even be parsed) nothing was
// signaled.
func TestStop_MalformedPIDFile(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "attn.pid")

	helper := spawnStopHelper(t, pidPath, "lock-malformed")
	waitForFlockHeld(t, pidPath, 5*time.Second)
	// Wait for the write to actually land (helper flocks before writing).
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(pidPath)
		if err == nil && strings.TrimSpace(string(data)) == "not-a-pid" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for malformed pid file content, last read: %q, err: %v", string(data), err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	result, err := Stop(pidPath)
	if err == nil {
		t.Fatalf("Stop() = %+v, err = nil, want a malformed-pid error", result)
	}
	if !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("Stop() error = %v, want it to mention 'malformed'", err)
	}
	if result.Stopped {
		t.Fatalf("Stop() = %+v, want Stopped=false", result)
	}
	if !isAlive(helper.Process.Pid) {
		t.Fatal("helper process is gone: Stop() should not have signaled anything for malformed content")
	}
}
