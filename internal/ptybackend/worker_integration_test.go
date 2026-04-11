package ptybackend

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/ptyworker"
)

func pidExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func waitForPIDsGone(timeout time.Duration, pids ...int) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		alive := false
		for _, pid := range pids {
			if pidExists(pid) {
				alive = true
				break
			}
		}
		if !alive {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func debugProcessState(t *testing.T, pids ...int) string {
	t.Helper()
	args := []string{"-o", "pid=,ppid=,stat=,comm=,command="}
	for _, pid := range pids {
		args = append(args, "-p", strconv.Itoa(pid))
	}
	cmd := exec.Command("ps", args...)
	output, err := cmd.CombinedOutput()
	if err != nil && len(output) == 0 {
		return err.Error()
	}
	return strings.TrimSpace(string(output))
}

func waitForRegistryEntry(path string, timeout time.Duration) (ptyworker.RegistryEntry, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		entry, err := ptyworker.ReadRegistry(path)
		if err == nil {
			return entry, nil
		}
		lastErr = err
		time.Sleep(50 * time.Millisecond)
	}
	return ptyworker.RegistryEntry{}, lastErr
}

func TestWorkerBackend_SpawnAttachInputResizeRemove(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping worker integration test in short mode")
	}
	if os.Getenv("ATTN_RUN_WORKER_INTEGRATION") != "1" {
		t.Skip("set ATTN_RUN_WORKER_INTEGRATION=1 to run worker integration test")
	}

	binary := buildAttnBinary(t)
	root, err := os.MkdirTemp("/tmp", "attn-worker-int-")
	if err != nil {
		t.Fatalf("MkdirTemp() error: %v", err)
	}
	defer os.RemoveAll(root)
	backend, err := NewWorker(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       binary,
	})
	if err != nil {
		t.Fatalf("NewWorker() error: %v", err)
	}

	sessionID := "worker-int-1"
	cwd := t.TempDir()
	if err := backend.Spawn(context.Background(), SpawnOptions{
		ID:    sessionID,
		CWD:   cwd,
		Agent: "shell",
		Label: "worker-int",
		Cols:  80,
		Rows:  24,
	}); err != nil {
		t.Skipf("worker spawn unavailable in this environment: %v", err)
	}
	defer func() {
		_ = backend.Remove(context.Background(), sessionID)
	}()

	attachInfo, stream, err := backend.Attach(context.Background(), sessionID, "test-sub")
	if err != nil {
		t.Fatalf("Attach() error: %v", err)
	}
	if !attachInfo.Running {
		t.Fatalf("attach running=false, expected true")
	}
	defer stream.Close()

	time.Sleep(250 * time.Millisecond)
	if err := backend.Input(context.Background(), sessionID, []byte("printf '__ATTN_WORKER__\\n'\n")); err != nil {
		t.Fatalf("Input() error: %v", err)
	}
	if err := backend.Resize(context.Background(), sessionID, 100, 30); err != nil {
		t.Fatalf("Resize() error: %v", err)
	}

	deadline := time.Now().Add(6 * time.Second)
	var out bytes.Buffer
	for time.Now().Before(deadline) {
		select {
		case evt, ok := <-stream.Events():
			if !ok {
				t.Fatal("stream closed before expected output")
			}
			if evt.Kind != OutputEventKindOutput {
				continue
			}
			out.Write(evt.Data)
			if strings.Contains(out.String(), "__ATTN_WORKER__") {
				goto gotOutput
			}
		case <-time.After(200 * time.Millisecond):
		}
	}
	t.Fatalf("timed out waiting for worker output; got=%q", out.String())

gotOutput:
	if err := backend.Kill(context.Background(), sessionID, syscall.SIGTERM); err != nil {
		t.Fatalf("Kill() error: %v", err)
	}
	if err := backend.Remove(context.Background(), sessionID); err != nil {
		t.Fatalf("Remove() error: %v", err)
	}
}

func TestWorkerBackend_RecoverAfterBackendRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping worker integration test in short mode")
	}
	if os.Getenv("ATTN_RUN_WORKER_INTEGRATION") != "1" {
		t.Skip("set ATTN_RUN_WORKER_INTEGRATION=1 to run worker integration test")
	}

	binary := buildAttnBinary(t)
	root, err := os.MkdirTemp("/tmp", "attn-worker-recover-")
	if err != nil {
		t.Fatalf("MkdirTemp() error: %v", err)
	}
	defer os.RemoveAll(root)

	const daemonID = "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	sessionID := "worker-recover-1"
	cwd := t.TempDir()

	backend1, err := NewWorker(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: daemonID,
		BinaryPath:       binary,
	})
	if err != nil {
		t.Fatalf("NewWorker() error: %v", err)
	}

	if err := backend1.Spawn(context.Background(), SpawnOptions{
		ID:    sessionID,
		CWD:   cwd,
		Agent: "shell",
		Label: "recover-int",
		Cols:  80,
		Rows:  24,
	}); err != nil {
		t.Skipf("worker spawn unavailable in this environment: %v", err)
	}
	_ = backend1.Shutdown(context.Background())

	backend2, err := NewWorker(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: daemonID,
		BinaryPath:       binary,
	})
	if err != nil {
		t.Fatalf("NewWorker() second backend error: %v", err)
	}
	defer func() {
		_ = backend2.Remove(context.Background(), sessionID)
	}()

	report, err := backend2.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover() error: %v", err)
	}
	if report.Recovered != 1 {
		t.Fatalf("recovered = %d, want 1 (report=%+v)", report.Recovered, report)
	}

	attachInfo, stream, err := backend2.Attach(context.Background(), sessionID, "recover-sub")
	if err != nil {
		t.Fatalf("Attach() after recover error: %v", err)
	}
	if !attachInfo.Running {
		t.Fatalf("attach running=false after recover, expected true")
	}
	defer stream.Close()

	if err := backend2.Input(context.Background(), sessionID, []byte("printf '__ATTN_RECOVER__\\n'\n")); err != nil {
		t.Fatalf("Input() after recover error: %v", err)
	}

	deadline := time.Now().Add(6 * time.Second)
	var out bytes.Buffer
	for time.Now().Before(deadline) {
		select {
		case evt, ok := <-stream.Events():
			if !ok {
				t.Fatal("stream closed before expected recover output")
			}
			if evt.Kind != OutputEventKindOutput {
				continue
			}
			out.Write(evt.Data)
			if strings.Contains(out.String(), "__ATTN_RECOVER__") {
				return
			}
		case <-time.After(200 * time.Millisecond):
		}
	}
	t.Fatalf("timed out waiting for recover output; got=%q", out.String())
}

func TestWorkerBackend_RemoveReapsExitedWorker(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping worker integration test in short mode")
	}
	if os.Getenv("ATTN_RUN_WORKER_INTEGRATION") != "1" {
		t.Skip("set ATTN_RUN_WORKER_INTEGRATION=1 to run worker integration test")
	}

	binary := buildAttnBinary(t)
	root, err := os.MkdirTemp("/tmp", "attn-worker-reap-")
	if err != nil {
		t.Fatalf("MkdirTemp() error: %v", err)
	}
	defer os.RemoveAll(root)

	backend, err := NewWorker(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       binary,
	})
	if err != nil {
		t.Fatalf("NewWorker() error: %v", err)
	}

	sessionID := "worker-reap-1"
	if err := backend.Spawn(context.Background(), SpawnOptions{
		ID:    sessionID,
		CWD:   t.TempDir(),
		Agent: "shell",
		Label: "worker-reap",
		Cols:  80,
		Rows:  24,
	}); err != nil {
		t.Skipf("worker spawn unavailable in this environment: %v", err)
	}

	registryPath := filepath.Join(backend.registryDir(), sessionID+".json")
	entry, err := waitForRegistryEntry(registryPath, 5*time.Second)
	if err != nil {
		t.Fatalf("waitForRegistryEntry() error: %v", err)
	}

	if err := backend.Kill(context.Background(), sessionID, syscall.SIGTERM); err != nil {
		t.Fatalf("Kill() error: %v", err)
	}
	if err := backend.Remove(context.Background(), sessionID); err != nil {
		t.Fatalf("Remove() error: %v", err)
	}

	if !waitForPIDsGone(5*time.Second, entry.WorkerPID, entry.ChildPID) {
		t.Fatalf(
			"worker or child pid still present after remove: worker=%d child=%d\n%s",
			entry.WorkerPID,
			entry.ChildPID,
			debugProcessState(t, entry.WorkerPID, entry.ChildPID),
		)
	}
}

func buildAttnBinary(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	binary := filepath.Join(t.TempDir(), "attn-test-bin")
	cmd := exec.Command("go", "build", "-o", binary, "./cmd/attn")
	cmd.Dir = repoRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build attn binary: %v\n%s", err, string(output))
	}
	return binary
}
