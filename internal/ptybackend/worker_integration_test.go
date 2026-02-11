package ptybackend

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

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
