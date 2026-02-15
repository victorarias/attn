package ptybackend

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptyworker"
)

func newWorkerBackendTestRoot(t *testing.T) string {
	t.Helper()
	base := "/tmp"
	if _, err := os.Stat(base); err != nil {
		base = ""
	}
	root, err := os.MkdirTemp(base, "attnwb-")
	if err != nil {
		t.Fatalf("MkdirTemp() error: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

func mustWorkerSocketPath(t *testing.T, backend *WorkerBackend, sessionID string) string {
	t.Helper()
	path, err := backend.expectedSocketPath(sessionID)
	if err != nil {
		t.Fatalf("expectedSocketPath(%q) error: %v", sessionID, err)
	}
	return path
}

func TestWorkerBackend_Recover_QuarantinesOwnershipMismatch(t *testing.T) {
	root := newWorkerBackendTestRoot(t)
	backend, err := NewWorker(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       "/bin/true",
	})
	if err != nil {
		t.Fatalf("NewWorker() error: %v", err)
	}

	socketPath := mustWorkerSocketPath(t, backend, "sess-1")
	if err := os.WriteFile(socketPath, []byte("stale"), 0600); err != nil {
		t.Fatalf("WriteFile(stale socket) error: %v", err)
	}

	registryPath := filepath.Join(backend.registryDir(), "sess-1.json")
	entry := ptyworker.NewRegistryEntry(
		"d-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"sess-1",
		os.Getpid(),
		os.Getpid(),
		socketPath,
		"shell",
		t.TempDir(),
		"tok",
	)
	if err := ptyworker.WriteRegistryAtomic(registryPath, entry); err != nil {
		t.Fatalf("WriteRegistryAtomic() error: %v", err)
	}

	report, err := backend.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover() error: %v", err)
	}
	if report.Failed != 1 {
		t.Fatalf("failed = %d, want 1", report.Failed)
	}
	if _, err := os.Stat(registryPath); !os.IsNotExist(err) {
		t.Fatalf("registry file should be moved to quarantine, stat err=%v", err)
	}
	files, err := filepath.Glob(filepath.Join(backend.quarantineDir(), "sess-1.json.*"))
	if err != nil {
		t.Fatalf("Glob() error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("quarantine files = %d, want 1", len(files))
	}
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("owned socket should be removed for ownership mismatch, stat err=%v", err)
	}
}

func TestWorkerBackend_Recover_ReclaimsStaleOwnershipMismatch(t *testing.T) {
	root := newWorkerBackendTestRoot(t)
	backend, err := NewWorker(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       "/bin/true",
	})
	if err != nil {
		t.Fatalf("NewWorker() error: %v", err)
	}

	sessionID := "sess-reclaim"
	socketPath := mustWorkerSocketPath(t, backend, sessionID)
	stopServer := startFakeWorkerRPCServer(
		t,
		"d-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		sessionID,
		"tok-reclaim",
		socketPath,
		"shell",
		t.TempDir(),
	)
	defer stopServer()

	registryPath := filepath.Join(backend.registryDir(), sessionID+".json")
	entry := ptyworker.NewRegistryEntry(
		"d-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		sessionID,
		os.Getpid(),
		os.Getpid(),
		socketPath,
		"shell",
		t.TempDir(),
		"tok-reclaim",
	)
	entry.OwnerPID = 2147483647
	entry.OwnerStartedAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	entry.OwnerNonce = "owner-old"
	if err := ptyworker.WriteRegistryAtomic(registryPath, entry); err != nil {
		t.Fatalf("WriteRegistryAtomic() error: %v", err)
	}

	report, err := backend.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover() error: %v", err)
	}
	if report.Pruned != 1 {
		t.Fatalf("pruned = %d, want 1", report.Pruned)
	}
	if report.Failed != 0 {
		t.Fatalf("failed = %d, want 0", report.Failed)
	}
	if _, err := os.Stat(registryPath); !os.IsNotExist(err) {
		t.Fatalf("registry should be removed after stale-owner reclaim, stat err=%v", err)
	}
	files, err := filepath.Glob(filepath.Join(backend.quarantineDir(), sessionID+".json.*"))
	if err != nil {
		t.Fatalf("Glob() error: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("quarantine files = %d, want 0", len(files))
	}
}

func TestWorkerBackend_Recover_PreservesLiveOwnerMismatch(t *testing.T) {
	root := newWorkerBackendTestRoot(t)
	backend, err := NewWorker(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       "/bin/true",
	})
	if err != nil {
		t.Fatalf("NewWorker() error: %v", err)
	}

	sessionID := "sess-live-owner"
	socketPath := mustWorkerSocketPath(t, backend, sessionID)
	if err := os.WriteFile(socketPath, []byte("stale"), 0600); err != nil {
		t.Fatalf("WriteFile(stale socket) error: %v", err)
	}

	registryPath := filepath.Join(backend.registryDir(), sessionID+".json")
	entry := ptyworker.NewRegistryEntry(
		"d-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		sessionID,
		os.Getpid(),
		os.Getpid(),
		socketPath,
		"shell",
		t.TempDir(),
		"tok-live",
	)
	entry.OwnerPID = os.Getpid()
	entry.OwnerStartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	entry.OwnerNonce = "different-owner"
	if err := ptyworker.WriteRegistryAtomic(registryPath, entry); err != nil {
		t.Fatalf("WriteRegistryAtomic() error: %v", err)
	}

	report, err := backend.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover() error: %v", err)
	}
	if report.Failed != 1 {
		t.Fatalf("failed = %d, want 1", report.Failed)
	}
	files, err := filepath.Glob(filepath.Join(backend.quarantineDir(), sessionID+".json.ownership_mismatch.*"))
	if err != nil {
		t.Fatalf("Glob() error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("quarantine files = %d, want 1", len(files))
	}
}

func TestWorkerBackend_Probe_FailsWhenBinaryUnavailable(t *testing.T) {
	root := newWorkerBackendTestRoot(t)
	backend, err := NewWorker(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       filepath.Join(root, "missing-binary"),
	})
	if err != nil {
		t.Fatalf("NewWorker() error: %v", err)
	}

	err = backend.Probe(context.Background())
	if err == nil {
		t.Fatal("Probe() error = nil, want failure when binary is unavailable")
	}
}

func TestWorkerBackend_Remove_RetainsTrackedSessionOnTransientError(t *testing.T) {
	root := newWorkerBackendTestRoot(t)
	backend, err := NewWorker(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       "/bin/true",
	})
	if err != nil {
		t.Fatalf("NewWorker() error: %v", err)
	}

	socketPath := filepath.Join(root, "remove.sock")
	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("net.Listen(unix) error: %v", err)
	}
	defer func() {
		_ = listener.Close()
		_ = os.Remove(socketPath)
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		enc := json.NewEncoder(conn)
		dec := json.NewDecoder(conn)
		for {
			var req ptyworker.RequestEnvelope
			if err := dec.Decode(&req); err != nil {
				return
			}
			switch req.Method {
			case ptyworker.MethodHello:
				result, _ := json.Marshal(ptyworker.HelloResult{
					WorkerVersion:    "test-worker",
					RPCMajor:         ptyworker.RPCMajor,
					RPCMinor:         ptyworker.RPCMinor,
					DaemonInstanceID: backend.cfg.DaemonInstanceID,
					SessionID:        "sess-remove",
				})
				_ = enc.Encode(ptyworker.ResponseEnvelope{Type: "res", ID: req.ID, OK: true, Result: result})
			case ptyworker.MethodRemove:
				_ = enc.Encode(ptyworker.ResponseEnvelope{
					Type:  "res",
					ID:    req.ID,
					OK:    false,
					Error: &ptyworker.RPCError{Code: ptyworker.ErrIO, Message: "remove busy"},
				})
				return
			default:
				result, _ := json.Marshal(map[string]any{"ok": true})
				_ = enc.Encode(ptyworker.ResponseEnvelope{Type: "res", ID: req.ID, OK: true, Result: result})
			}
		}
	}()

	backend.mu.Lock()
	backend.sessions["sess-remove"] = &workerSession{
		SessionID:    "sess-remove",
		SocketPath:   socketPath,
		RegistryPath: filepath.Join(backend.registryDir(), "sess-remove.json"),
		ControlToken: "tok",
	}
	backend.mu.Unlock()

	err = backend.Remove(context.Background(), "sess-remove")
	if err == nil {
		t.Fatal("Remove() error = nil, want transient failure for missing socket")
	}

	backend.mu.RLock()
	_, stillTracked := backend.sessions["sess-remove"]
	backend.mu.RUnlock()
	if !stillTracked {
		t.Fatal("session should remain tracked after transient remove failure")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for remove test server to exit")
	}
}

func TestWorkerBackend_Recover_RejectsUnexpectedSocketPath(t *testing.T) {
	root := newWorkerBackendTestRoot(t)
	backend, err := NewWorker(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       "/bin/true",
	})
	if err != nil {
		t.Fatalf("NewWorker() error: %v", err)
	}

	externalSocketPath := filepath.Join(t.TempDir(), "external.sock")
	if err := os.WriteFile(externalSocketPath, []byte("placeholder"), 0600); err != nil {
		t.Fatalf("WriteFile(external socket) error: %v", err)
	}

	registryPath := filepath.Join(backend.registryDir(), "sess-unsafe.json")
	entry := ptyworker.NewRegistryEntry(
		"d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"sess-unsafe",
		os.Getpid(),
		os.Getpid(),
		externalSocketPath,
		"shell",
		t.TempDir(),
		"tok",
	)
	if err := ptyworker.WriteRegistryAtomic(registryPath, entry); err != nil {
		t.Fatalf("WriteRegistryAtomic() error: %v", err)
	}

	report, err := backend.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover() error: %v", err)
	}
	if report.Failed != 1 {
		t.Fatalf("failed = %d, want 1", report.Failed)
	}
	if _, err := os.Stat(externalSocketPath); err != nil {
		t.Fatalf("external socket path should not be removed, stat err=%v", err)
	}

	files, err := filepath.Glob(filepath.Join(backend.quarantineDir(), "sess-unsafe.json.socket_path_mismatch.*"))
	if err != nil {
		t.Fatalf("Glob() error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("quarantine files = %d, want 1", len(files))
	}
}

func TestWorkerBackend_Recover_AcceptsLegacySocketPath(t *testing.T) {
	root := newWorkerBackendTestRoot(t)
	backend, err := NewWorker(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       "/bin/true",
	})
	if err != nil {
		t.Fatalf("NewWorker() error: %v", err)
	}

	sessionID := "sess-legacy"
	socketPath, err := backend.legacyExpectedSocketPath(sessionID)
	if err != nil {
		t.Fatalf("legacyExpectedSocketPath() error: %v", err)
	}
	registryPath := filepath.Join(backend.registryDir(), sessionID+".json")
	entry := ptyworker.NewRegistryEntry(
		backend.cfg.DaemonInstanceID,
		sessionID,
		os.Getpid(),
		os.Getpid(),
		socketPath,
		"codex",
		t.TempDir(),
		"tok",
	)
	if err := ptyworker.WriteRegistryAtomic(registryPath, entry); err != nil {
		t.Fatalf("WriteRegistryAtomic() error: %v", err)
	}
	stopServer := startFakeWorkerRPCServer(t, backend.cfg.DaemonInstanceID, sessionID, "tok", socketPath, "codex", t.TempDir())
	defer stopServer()

	report, err := backend.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover() error: %v", err)
	}
	if report.Recovered != 1 {
		t.Fatalf("recovered = %d, want 1", report.Recovered)
	}
	if report.Failed != 0 {
		t.Fatalf("failed = %d, want 0", report.Failed)
	}
}

func TestWorkerBackend_Recover_RestoresSocketMismatchQuarantine(t *testing.T) {
	root := newWorkerBackendTestRoot(t)
	backend, err := NewWorker(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       "/bin/true",
	})
	if err != nil {
		t.Fatalf("NewWorker() error: %v", err)
	}

	sessionID := "sess-quarantine-restore"
	socketPath, err := backend.legacyExpectedSocketPath(sessionID)
	if err != nil {
		t.Fatalf("legacyExpectedSocketPath() error: %v", err)
	}

	quarantinePath := filepath.Join(backend.quarantineDir(), sessionID+".json.socket_path_mismatch.123")
	entry := ptyworker.NewRegistryEntry(
		backend.cfg.DaemonInstanceID,
		sessionID,
		os.Getpid(),
		os.Getpid(),
		socketPath,
		"codex",
		t.TempDir(),
		"tok",
	)
	if err := ptyworker.WriteRegistryAtomic(quarantinePath, entry); err != nil {
		t.Fatalf("WriteRegistryAtomic(quarantine) error: %v", err)
	}
	stopServer := startFakeWorkerRPCServer(t, backend.cfg.DaemonInstanceID, sessionID, "tok", socketPath, "codex", t.TempDir())
	defer stopServer()

	report, err := backend.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover() error: %v", err)
	}
	if report.Recovered != 1 {
		t.Fatalf("recovered = %d, want 1", report.Recovered)
	}
	if _, err := os.Stat(filepath.Join(backend.registryDir(), sessionID+".json")); err != nil {
		t.Fatalf("restored registry missing: %v", err)
	}
	if _, err := os.Stat(quarantinePath); err == nil {
		t.Fatal("quarantine file should have been moved")
	}
}

func TestWorkerBackend_SessionLikelyAlive_UsesValidatedRegistry(t *testing.T) {
	root := newWorkerBackendTestRoot(t)
	backend, err := NewWorker(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       "/bin/true",
	})
	if err != nil {
		t.Fatalf("NewWorker() error: %v", err)
	}

	registryPath := filepath.Join(backend.registryDir(), "sess-1.json")
	socketPath := mustWorkerSocketPath(t, backend, "sess-1")
	entry := ptyworker.NewRegistryEntry(
		"d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"sess-1",
		os.Getpid(),
		os.Getpid(),
		socketPath,
		"codex",
		t.TempDir(),
		"tok",
	)
	if err := ptyworker.WriteRegistryAtomic(registryPath, entry); err != nil {
		t.Fatalf("WriteRegistryAtomic() error: %v", err)
	}
	stopServer := startFakeWorkerRPCServer(t, backend.cfg.DaemonInstanceID, "sess-1", "tok", socketPath, "codex", t.TempDir())
	defer stopServer()

	alive, err := backend.SessionLikelyAlive(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("SessionLikelyAlive() error: %v", err)
	}
	if !alive {
		t.Fatal("SessionLikelyAlive() = false, want true")
	}

	entry.SocketPath = filepath.Join(t.TempDir(), "unexpected.sock")
	if err := ptyworker.WriteRegistryAtomic(registryPath, entry); err != nil {
		t.Fatalf("WriteRegistryAtomic() mismatch error: %v", err)
	}
	alive, err = backend.SessionLikelyAlive(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("SessionLikelyAlive() mismatch error: %v", err)
	}
	if alive {
		t.Fatal("SessionLikelyAlive() should be false for mismatched socket path")
	}
}

func TestWorkerBackend_SessionLikelyAlive_MalformedRegistryReturnsError(t *testing.T) {
	root := newWorkerBackendTestRoot(t)
	backend, err := NewWorker(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       "/bin/true",
	})
	if err != nil {
		t.Fatalf("NewWorker() error: %v", err)
	}

	registryPath := filepath.Join(backend.registryDir(), "sess-bad.json")
	if err := os.WriteFile(registryPath, []byte("{not-json"), 0600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	alive, err := backend.SessionLikelyAlive(context.Background(), "sess-bad")
	if alive {
		t.Fatal("SessionLikelyAlive() = true, want false")
	}
	if err == nil {
		t.Fatal("SessionLikelyAlive() error = nil, want non-nil for malformed registry")
	}
}

func TestWorkerBackend_Recover_SecondCallReusesExistingSession(t *testing.T) {
	root := newWorkerBackendTestRoot(t)
	backend, err := NewWorker(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       "/bin/true",
	})
	if err != nil {
		t.Fatalf("NewWorker() error: %v", err)
	}

	socketPath := mustWorkerSocketPath(t, backend, "sess-1")
	stopServer := startFakeWorkerRPCServer(t, backend.cfg.DaemonInstanceID, "sess-1", "tok", socketPath, "codex", t.TempDir())
	defer stopServer()

	registryPath := filepath.Join(backend.registryDir(), "sess-1.json")
	entry := ptyworker.NewRegistryEntry(
		backend.cfg.DaemonInstanceID,
		"sess-1",
		os.Getpid(),
		os.Getpid(),
		socketPath,
		"codex",
		t.TempDir(),
		"tok",
	)
	if err := ptyworker.WriteRegistryAtomic(registryPath, entry); err != nil {
		t.Fatalf("WriteRegistryAtomic() error: %v", err)
	}

	if _, err := backend.Recover(context.Background()); err != nil {
		t.Fatalf("first Recover() error: %v", err)
	}
	backend.mu.RLock()
	first := backend.sessions["sess-1"]
	backend.mu.RUnlock()
	if first == nil {
		t.Fatal("session missing after first recover")
	}

	if _, err := backend.Recover(context.Background()); err != nil {
		t.Fatalf("second Recover() error: %v", err)
	}
	backend.mu.RLock()
	second := backend.sessions["sess-1"]
	backend.mu.RUnlock()
	if second == nil {
		t.Fatal("session missing after second recover")
	}
	if first != second {
		t.Fatal("second recover replaced session pointer; expected idempotent reuse")
	}
}

func TestWorkerBackend_GetSession_PrunesDeadRegistryEntry(t *testing.T) {
	root := newWorkerBackendTestRoot(t)
	backend, err := NewWorker(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       "/bin/true",
	})
	if err != nil {
		t.Fatalf("NewWorker() error: %v", err)
	}

	sessionID := "sess-stale"
	socketPath := mustWorkerSocketPath(t, backend, sessionID)
	if err := os.WriteFile(socketPath, []byte("stale"), 0600); err != nil {
		t.Fatalf("WriteFile(stale socket placeholder) error: %v", err)
	}
	registryPath := filepath.Join(backend.registryDir(), sessionID+".json")
	entry := ptyworker.NewRegistryEntry(
		backend.cfg.DaemonInstanceID,
		sessionID,
		0, // definitely not alive
		0,
		socketPath,
		"codex",
		t.TempDir(),
		"tok",
	)
	if err := ptyworker.WriteRegistryAtomic(registryPath, entry); err != nil {
		t.Fatalf("WriteRegistryAtomic() error: %v", err)
	}

	session, err := backend.getSession(sessionID)
	if session != nil {
		t.Fatalf("getSession returned session=%+v, want nil", session)
	}
	if !errors.Is(err, pty.ErrSessionNotFound) {
		t.Fatalf("getSession error = %v, want ErrSessionNotFound", err)
	}

	if _, err := os.Stat(registryPath); !os.IsNotExist(err) {
		t.Fatalf("registry file should be pruned, stat err=%v", err)
	}
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("socket file should be pruned, stat err=%v", err)
	}
}

func TestWorkerStream_PublishOverflowDoesNotBlock(t *testing.T) {
	stream := &workerStream{
		events: make(chan OutputEvent, 1),
		done:   make(chan struct{}),
	}
	if ok := stream.publish(OutputEvent{Kind: OutputEventKindOutput, Data: []byte("a"), Seq: 1}); !ok {
		t.Fatal("first publish should succeed")
	}

	start := time.Now()
	ok := stream.publish(OutputEvent{Kind: OutputEventKindOutput, Data: []byte("b"), Seq: 2})
	if ok {
		t.Fatal("second publish should fail when buffer is full")
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("publish on full buffer should return quickly")
	}
}

func TestValidateSessionID(t *testing.T) {
	valid := []string{
		"abc-123",
		"session_1",
		"id.with.dots",
		"id:with:colon",
	}
	for _, id := range valid {
		if err := validateSessionID(id); err != nil {
			t.Fatalf("validateSessionID(%q) unexpected error: %v", id, err)
		}
	}

	invalid := []string{
		"",
		" ",
		"../evil",
		"id/with/slash",
		"id\\with\\slash",
		"id with space",
		"id*glob",
	}
	for _, id := range invalid {
		if err := validateSessionID(id); err == nil {
			t.Fatalf("validateSessionID(%q) expected error", id)
		}
	}
}

func TestIsTransientRecoveryError(t *testing.T) {
	if !isTransientRecoveryError(context.DeadlineExceeded) {
		t.Fatal("context deadline exceeded should be treated as transient")
	}
	if !isTransientRecoveryError(errors.New("connect: connection refused")) {
		t.Fatal("connection refused should be treated as transient")
	}
	if isTransientRecoveryError(errors.New("rpc unauthorized")) {
		t.Fatal("unauthorized errors should not be treated as transient")
	}
}

func TestAppendCappedPreEvent(t *testing.T) {
	events := make([]OutputEvent, 0, 3)
	for i := 1; i <= 5; i++ {
		events = appendCappedPreEvent(events, OutputEvent{Kind: OutputEventKindOutput, Seq: uint32(i)}, 3)
	}
	if len(events) != 3 {
		t.Fatalf("len(events) = %d, want 3", len(events))
	}
	if events[0].Seq != 3 || events[1].Seq != 4 || events[2].Seq != 5 {
		t.Fatalf("seq window = [%d %d %d], want [3 4 5]", events[0].Seq, events[1].Seq, events[2].Seq)
	}
}

func TestWorkerStream_CloseBoundedWhenPeerStalled(t *testing.T) {
	clientConn, peerConn := net.Pipe()
	defer peerConn.Close()

	stream := newWorkerStream(
		clientConn,
		json.NewEncoder(clientConn),
		json.NewDecoder(clientConn),
		"sess-1",
		"detach-1",
		nil,
	)

	start := time.Now()
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Close() took too long (%v), expected bounded shutdown", elapsed)
	}
}

func TestWorkerSession_NotePollFailureLifecycle(t *testing.T) {
	s := &workerSession{}
	now := time.Now()
	advanceToThreshold := func(ts time.Time) (bool, bool) {
		var logUnreachable bool
		var evict bool
		for i := 0; i < pollerFailureThreshold; i++ {
			logUnreachable, evict = s.notePollFailure(ts)
		}
		return logUnreachable, evict
	}

	logUnreachable, evict := advanceToThreshold(now)
	if !logUnreachable || evict {
		t.Fatalf("threshold failure should log only, got log=%v evict=%v", logUnreachable, evict)
	}
	if !s.unreachable {
		t.Fatal("session should be marked unreachable after threshold")
	}

	logUnreachable, evict = advanceToThreshold(now.Add(pollerUnreachableAfter - time.Second))
	if logUnreachable || evict {
		t.Fatalf("before timeout should not log/evict, got log=%v evict=%v", logUnreachable, evict)
	}

	logUnreachable, evict = advanceToThreshold(now.Add(pollerUnreachableAfter + time.Second))
	if logUnreachable || !evict {
		t.Fatalf("after timeout should evict only, got log=%v evict=%v", logUnreachable, evict)
	}
}

func TestWorkerSession_NotePollRecoveryResetsUnreachableState(t *testing.T) {
	s := &workerSession{
		unreachable:   true,
		unreachableAt: time.Now().Add(-time.Minute),
		pollFailures:  2,
	}
	s.notePollRecovery()
	if s.unreachable {
		t.Fatal("session should be reachable after recovery")
	}
	if s.pollFailures != 0 {
		t.Fatalf("pollFailures = %d, want 0", s.pollFailures)
	}
	if !s.unreachableAt.IsZero() {
		t.Fatal("unreachableAt should be reset after recovery")
	}
}

func TestWorkerBackend_StopMonitor_DoesNotHangWhenWatchResponseMissing(t *testing.T) {
	root := newWorkerBackendTestRoot(t)

	backend, err := NewWorker(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       "/bin/true",
	})
	if err != nil {
		t.Fatalf("NewWorker() error: %v", err)
	}

	sessionID := "sess-watch-stall"
	socketPath := mustWorkerSocketPath(t, backend, sessionID)
	stopServer := startWatchStallWorkerRPCServer(t, backend.cfg.DaemonInstanceID, sessionID, "tok-watch", socketPath)
	defer stopServer()

	session := &workerSession{
		SessionID:    sessionID,
		SocketPath:   socketPath,
		RegistryPath: filepath.Join(backend.registryDir(), sessionID+".json"),
		ControlToken: "tok-watch",
	}

	backend.startMonitor(session)
	time.Sleep(100 * time.Millisecond)

	stopDone := make(chan struct{})
	go func() {
		backend.stopMonitor(session)
		close(stopDone)
	}()

	select {
	case <-stopDone:
	case <-time.After(2 * time.Second):
		t.Fatal("stopMonitor() hung while watch response was missing")
	}
}

func TestWorkerBackend_ForceSessionEviction_StopsMonitorAndPrunes(t *testing.T) {
	root := newWorkerBackendTestRoot(t)
	backend, err := NewWorker(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       "/bin/true",
	})
	if err != nil {
		t.Fatalf("NewWorker() error: %v", err)
	}

	sessionID := "sess-evict"
	registryPath := filepath.Join(backend.registryDir(), sessionID+".json")
	socketPath := mustWorkerSocketPath(t, backend, sessionID)
	if err := os.WriteFile(registryPath, []byte("registry"), 0600); err != nil {
		t.Fatalf("WriteFile(registry) error: %v", err)
	}
	if err := os.WriteFile(socketPath, []byte("socket"), 0600); err != nil {
		t.Fatalf("WriteFile(socket) error: %v", err)
	}

	session := &workerSession{
		SessionID:    sessionID,
		RegistryPath: registryPath,
		SocketPath:   socketPath,
		monitorStop:  make(chan struct{}),
		monitorDone:  make(chan struct{}),
	}
	stopCh := session.monitorStop
	doneCh := session.monitorDone
	go func() {
		<-stopCh
		close(doneCh)
	}()

	backend.mu.Lock()
	backend.sessions[sessionID] = session
	backend.mu.Unlock()

	backend.forceSessionEviction(session)

	backend.mu.RLock()
	_, exists := backend.sessions[sessionID]
	backend.mu.RUnlock()
	if exists {
		t.Fatal("session should be removed from backend map after forceSessionEviction")
	}
	if _, err := os.Stat(registryPath); !os.IsNotExist(err) {
		t.Fatalf("registry should be removed, stat err=%v", err)
	}
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("socket should be removed, stat err=%v", err)
	}
}

func TestWorkerBackend_Spawn_CleansUpUnreadyWorkerProcess(t *testing.T) {
	root := newWorkerBackendTestRoot(t)
	pidFile := filepath.Join(root, "worker.pid")
	scriptPath := filepath.Join(root, "fake-worker.sh")
	script := "#!/bin/sh\n" +
		"echo $$ > \"$ATTN_TEST_PID_FILE\"\n" +
		"sleep 60\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0700); err != nil {
		t.Fatalf("WriteFile(fake worker script) error: %v", err)
	}

	t.Setenv("ATTN_TEST_PID_FILE", pidFile)
	backend, err := NewWorker(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       scriptPath,
	})
	if err != nil {
		t.Fatalf("NewWorker() error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	err = backend.Spawn(ctx, SpawnOptions{
		ID:    "sess-timeout",
		Agent: "codex",
		CWD:   root,
		Cols:  80,
		Rows:  24,
	})
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Spawn() error = %v, want context deadline exceeded", err)
	}

	var pid int
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, readErr := os.ReadFile(pidFile)
		if readErr != nil {
			time.Sleep(25 * time.Millisecond)
			continue
		}
		parsedPID, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
		if parseErr != nil {
			t.Fatalf("Atoi(pid file) error: %v", parseErr)
		}
		pid = parsedPID
		break
	}
	if pid == 0 {
		t.Fatalf("timed out waiting for fake worker pid file at %s", pidFile)
	}

	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !pidAlive(pid) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("worker pid %d still alive after spawn failure cleanup", pid)
}

func startWatchStallWorkerRPCServer(
	t *testing.T,
	daemonInstanceID string,
	sessionID string,
	controlToken string,
	socketPath string,
) func() {
	t.Helper()

	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen fake worker socket: %v", err)
	}

	done := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				select {
				case <-done:
					return
				default:
					return
				}
			}
			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				defer c.Close()
				enc := json.NewEncoder(c)
				dec := json.NewDecoder(c)
				for {
					var req ptyworker.RequestEnvelope
					if err := dec.Decode(&req); err != nil {
						return
					}
					switch req.Method {
					case ptyworker.MethodHello:
						params := ptyworker.HelloParams{}
						_ = json.Unmarshal(req.Params, &params)
						if params.DaemonInstanceID != daemonInstanceID || params.ControlToken != controlToken {
							_ = enc.Encode(ptyworker.ResponseEnvelope{
								Type:  "res",
								ID:    req.ID,
								OK:    false,
								Error: &ptyworker.RPCError{Code: ptyworker.ErrUnauthorized, Message: "unauthorized"},
							})
							return
						}
						result, _ := json.Marshal(ptyworker.HelloResult{
							WorkerVersion:    "test-worker",
							RPCMajor:         ptyworker.RPCMajor,
							RPCMinor:         ptyworker.RPCMinor,
							DaemonInstanceID: daemonInstanceID,
							SessionID:        sessionID,
						})
						_ = enc.Encode(ptyworker.ResponseEnvelope{Type: "res", ID: req.ID, OK: true, Result: result})
					case ptyworker.MethodWatch:
						<-done
						return
					default:
						result, _ := json.Marshal(map[string]any{"ok": true})
						_ = enc.Encode(ptyworker.ResponseEnvelope{Type: "res", ID: req.ID, OK: true, Result: result})
					}
				}
			}(conn)
		}
	}()

	return func() {
		close(done)
		_ = listener.Close()
		wg.Wait()
		_ = os.Remove(socketPath)
	}
}

func startFakeWorkerRPCServer(
	t *testing.T,
	daemonInstanceID string,
	sessionID string,
	controlToken string,
	socketPath string,
	agent string,
	cwd string,
) func() {
	t.Helper()

	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen fake worker socket: %v", err)
	}

	done := make(chan struct{})
	var wg sync.WaitGroup

	handleConn := func(conn net.Conn, infoCalls *atomic.Int64) {
		defer conn.Close()
		enc := json.NewEncoder(conn)
		dec := json.NewDecoder(conn)

		for {
			var req ptyworker.RequestEnvelope
			if err := dec.Decode(&req); err != nil {
				return
			}
			switch req.Method {
			case ptyworker.MethodHello:
				params := ptyworker.HelloParams{}
				_ = json.Unmarshal(req.Params, &params)
				if params.DaemonInstanceID != daemonInstanceID || params.ControlToken != controlToken {
					_ = enc.Encode(ptyworker.ResponseEnvelope{
						Type:  "res",
						ID:    req.ID,
						OK:    false,
						Error: &ptyworker.RPCError{Code: ptyworker.ErrUnauthorized, Message: "unauthorized"},
					})
					return
				}
				result, _ := json.Marshal(ptyworker.HelloResult{
					WorkerVersion:    "test-worker",
					RPCMajor:         ptyworker.RPCMajor,
					RPCMinor:         ptyworker.RPCMinor,
					DaemonInstanceID: daemonInstanceID,
					SessionID:        sessionID,
				})
				_ = enc.Encode(ptyworker.ResponseEnvelope{Type: "res", ID: req.ID, OK: true, Result: result})
			case ptyworker.MethodInfo:
				infoCalls.Add(1)
				result, _ := json.Marshal(ptyworker.InfoResult{
					Running:   true,
					Agent:     agent,
					CWD:       cwd,
					Cols:      80,
					Rows:      24,
					WorkerPID: os.Getpid(),
					ChildPID:  os.Getpid(),
					LastSeq:   1,
					State:     "working",
				})
				_ = enc.Encode(ptyworker.ResponseEnvelope{Type: "res", ID: req.ID, OK: true, Result: result})
			case ptyworker.MethodHealth:
				result, _ := json.Marshal(map[string]any{"ok": true, "running": true})
				_ = enc.Encode(ptyworker.ResponseEnvelope{Type: "res", ID: req.ID, OK: true, Result: result})
			case ptyworker.MethodWatch:
				_ = enc.Encode(ptyworker.ResponseEnvelope{
					Type: "res",
					ID:   req.ID,
					OK:   false,
					Error: &ptyworker.RPCError{
						Code:    ptyworker.ErrUnsupportedVersion,
						Message: "watch unsupported in fake server",
					},
				})
			default:
				result, _ := json.Marshal(map[string]any{"ok": true})
				_ = enc.Encode(ptyworker.ResponseEnvelope{Type: "res", ID: req.ID, OK: true, Result: result})
			}
		}
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		infoCalls := &atomic.Int64{}
		for {
			conn, err := listener.Accept()
			if err != nil {
				select {
				case <-done:
					return
				default:
					return
				}
			}
			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				handleConn(c, infoCalls)
			}(conn)
		}
	}()

	return func() {
		close(done)
		_ = listener.Close()
		wg.Wait()
		_ = os.Remove(socketPath)
	}
}
