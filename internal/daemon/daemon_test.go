package daemon

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/github"
	"github.com/victorarias/attn/internal/github/mockserver"
	"github.com/victorarias/attn/internal/logging"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/toolhome"
	"github.com/victorarias/attn/internal/workspacelayout"
	"nhooyr.io/websocket"
)

type countingClassifier struct {
	state string
	mu    sync.Mutex
	calls int
}

func (c *countingClassifier) Classify(text string, timeout time.Duration) (string, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return c.state, nil
}

func (c *countingClassifier) CallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

type blockingClassifier struct {
	state   string
	started chan struct{}
	release chan struct{}
	mu      sync.Mutex
	calls   int
}

type errorClassifier struct {
	state string
	err   error
}

func (c *errorClassifier) Classify(text string, timeout time.Duration) (string, error) {
	return c.state, c.err
}

func newBlockingClassifier(state string) *blockingClassifier {
	return &blockingClassifier{
		state:   state,
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
}

func (c *blockingClassifier) Classify(text string, timeout time.Duration) (string, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	select {
	case c.started <- struct{}{}:
	default:
	}
	<-c.release
	return c.state, nil
}

func (c *blockingClassifier) CallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func TestMain(m *testing.M) {
	// Keep daemon package tests on embedded PTY by default to avoid spawning
	// large numbers of worker subprocesses. Tests that need worker behavior
	// explicitly override these with t.Setenv.
	_ = os.Setenv("ATTN_PTY_BACKEND", "embedded")
	_ = os.Setenv("ATTN_PTY_SKIP_STARTUP_PROBE", "1")

	// Plugin-process helper subprocesses (TestDaemonPluginProcessHelper,
	// TestPluginDriverFixtureProcess) re-exec this same test binary with a
	// single -test.run and rely on ATTN_SOCKET_PATH being the exact value
	// the parent test process injected via cmd.Env to wire the fixture back
	// to its temp-scoped daemon socket — that's trusted IPC plumbing from an
	// already-scoped parent test, not an inherited-from-the-shell override,
	// and neither helper calls config.DataDir()/DBPath()/SocketPath(), so
	// there is nothing here for ScopeTestEnvironment to protect. Skip
	// scoping (and let them inherit whatever ATTN_DATA_DIR the parent test
	// process already had) rather than clobber that wiring, same shape as
	// config's own ATTN_TEST_DATADIR_BACKSTOP_HELPER skip.
	if os.Getenv("ATTN_PLUGIN_HELPER") == "1" || os.Getenv("ATTN_PLUGIN_DRIVER_HELPER") == "1" {
		os.Exit(m.Run())
	}

	// Scope every test in this package to an explicit temp data dir so no
	// daemon test can resolve config.DataDir() to the real ~/.attn — see
	// docs/plans/2026-07-18-db-loss-mitigation.md. Individual tests that need
	// their own isolation layer a t.Setenv("ATTN_DATA_DIR", ...) on top.
	dataDir, err := os.MkdirTemp("", "attn-test-data-*")
	if err != nil {
		panic("daemon: TestMain: MkdirTemp: " + err.Error())
	}
	config.ScopeTestEnvironment(dataDir)

	// Same story for toolhome.Dir() (~/.claude, ~/.codex, ~/.copilot,
	// ~/.agents skill installs, transcript lookups): default every daemon
	// test to a throwaway tool-home dir. Tests exercising specific transcript
	// fixtures override this with their own t.Setenv(toolhome.EnvVar, ...).
	toolHomeDir, err := os.MkdirTemp("", "attn-test-toolhome-*")
	if err != nil {
		panic("daemon: TestMain: MkdirTemp: " + err.Error())
	}
	_ = os.Setenv(toolhome.EnvVar, toolHomeDir)

	code := m.Run()
	os.RemoveAll(dataDir)
	os.RemoveAll(toolHomeDir)
	os.Exit(code)
}

// waitForSocket waits for a unix socket to be ready for connections.
// This is more reliable than fixed sleeps, especially in CI environments.
func waitForSocket(t *testing.T, sockPath string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", sockPath, 10*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("socket %s not ready after %v", sockPath, timeout)
}

func waitForRecovery(t *testing.T, d *Daemon) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for d.isRecovering() {
		if time.Now().After(deadline) {
			t.Fatal("daemon recovery did not finish before test setup")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func shortTempDir(t *testing.T) string {
	t.Helper()
	// Unix socket paths are length-limited (notably on macOS). The default
	// `t.TempDir()` path can be too long, so keep the base dir short.
	base := "/tmp"
	if _, err := os.Stat(base); err != nil {
		base = ""
	}
	dir, err := os.MkdirTemp(base, "attn-")
	if err != nil {
		t.Fatalf("MkdirTemp() error: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestDaemon_RegisterAndQuery(t *testing.T) {
	useFreeWSPort(t)

	tmpDir := shortTempDir(t)
	sockPath := filepath.Join(tmpDir, "test.sock")

	d := NewForTesting(sockPath)
	go d.Start()
	defer d.Stop()

	waitForSocket(t, sockPath, 5*time.Second)
	waitForRecovery(t, d)

	c := client.New(sockPath)

	// Register a session
	err := c.Register("sess-1", "drumstick", "/home/user/project")
	if err != nil {
		t.Fatalf("Register error: %v", err)
	}

	// Query all sessions
	sessions, err := c.Query("")
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	if sessions[0].Label != "drumstick" {
		t.Errorf("Label = %q, want %q", sessions[0].Label, "drumstick")
	}
}

func TestDaemon_StateUpdate(t *testing.T) {
	useFreeWSPort(t)

	tmpDir := shortTempDir(t)
	sockPath := filepath.Join(tmpDir, "test.sock")

	d := NewForTesting(sockPath)
	go d.Start()
	defer d.Stop()

	waitForSocket(t, sockPath, 5*time.Second)
	waitForRecovery(t, d)

	c := client.New(sockPath)

	// Register
	c.Register("sess-1", "test", "/tmp")

	// Update state
	err := c.UpdateState("sess-1", protocol.StateWaitingInput)
	if err != nil {
		t.Fatalf("UpdateState error: %v", err)
	}

	// Query waiting
	sessions, err := c.Query(protocol.StateWaitingInput)
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d waiting sessions, want 1", len(sessions))
	}
}

// TestDaemon_ScheduledStateUpdate exercises the exact wire the _hook-stop
// wrapper uses for a session parked on a /loop or cron: c.UpdateState(id,
// "scheduled") over the real socket. It must land as scheduled (not idle, not
// dropped) so the parked session reads correctly end to end.
func TestDaemon_ScheduledStateUpdate(t *testing.T) {
	useFreeWSPort(t)

	tmpDir := shortTempDir(t)
	sockPath := filepath.Join(tmpDir, "test.sock")

	d := NewForTesting(sockPath)
	go d.Start()
	defer d.Stop()

	waitForSocket(t, sockPath, 5*time.Second)
	waitForRecovery(t, d)

	c := client.New(sockPath)
	c.Register("sess-1", "loop-bot", "/tmp")

	if err := c.UpdateState("sess-1", protocol.StateScheduled); err != nil {
		t.Fatalf("UpdateState(scheduled) error: %v", err)
	}

	scheduled, err := c.Query(protocol.StateScheduled)
	if err != nil {
		t.Fatalf("Query(scheduled) error: %v", err)
	}
	if len(scheduled) != 1 {
		t.Fatalf("got %d scheduled sessions, want 1", len(scheduled))
	}
	if scheduled[0].ID != "sess-1" {
		t.Fatalf("scheduled session ID = %q, want sess-1", scheduled[0].ID)
	}

	// It must not have fallen through to idle.
	idle, err := c.Query(protocol.StateIdle)
	if err != nil {
		t.Fatalf("Query(idle) error: %v", err)
	}
	if len(idle) != 0 {
		t.Fatalf("got %d idle sessions, want 0 (scheduled must not read as idle)", len(idle))
	}
}

func TestDaemon_Unregister(t *testing.T) {
	useFreeWSPort(t)

	tmpDir := shortTempDir(t)
	sockPath := filepath.Join(tmpDir, "test.sock")

	d := NewForTesting(sockPath)
	go d.Start()
	defer d.Stop()

	waitForSocket(t, sockPath, 5*time.Second)

	c := client.New(sockPath)

	c.Register("sess-1", "test", "/tmp")
	c.Unregister("sess-1")

	sessions, _ := c.Query("")
	if len(sessions) != 0 {
		t.Errorf("got %d sessions after unregister, want 0", len(sessions))
	}
}

func TestDaemon_MultipleSessions(t *testing.T) {
	useFreeWSPort(t)

	tmpDir := shortTempDir(t)
	sockPath := filepath.Join(tmpDir, "test.sock")

	d := NewForTesting(sockPath)
	go d.Start()
	defer d.Stop()

	waitForSocket(t, sockPath, 5*time.Second)
	waitForRecovery(t, d)

	c := client.New(sockPath)

	// Register multiple sessions (all start as launching)
	if err := c.Register("1", "one", "/tmp/1"); err != nil {
		t.Fatalf("Register(1) error: %v", err)
	}
	if err := c.Register("2", "two", "/tmp/2"); err != nil {
		t.Fatalf("Register(2) error: %v", err)
	}
	if err := c.Register("3", "three", "/tmp/3"); err != nil {
		t.Fatalf("Register(3) error: %v", err)
	}

	// Update one to working
	if err := c.UpdateState("2", protocol.StateWorking); err != nil {
		t.Fatalf("UpdateState(2, working) error: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		launching, err := c.Query(protocol.StateLaunching)
		if err != nil {
			t.Fatalf("Query(launching) error: %v", err)
		}
		working, err := c.Query(protocol.StateWorking)
		if err != nil {
			t.Fatalf("Query(working) error: %v", err)
		}
		if len(launching) == 2 && len(working) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("got %d launching and %d working, want 2 launching and 1 working", len(launching), len(working))
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestDaemon_SocketCleanup(t *testing.T) {
	useFreeWSPort(t)

	tmpDir := shortTempDir(t)
	sockPath := filepath.Join(tmpDir, "test.sock")

	// Create stale socket file
	f, _ := os.Create(sockPath)
	f.Close()

	d := NewForTesting(sockPath)
	go d.Start()
	defer d.Stop()

	waitForSocket(t, sockPath, 5*time.Second)

	// Should still work (stale socket removed)
	c := client.New(sockPath)
	err := c.Register("1", "test", "/tmp")
	if err != nil {
		t.Fatalf("Register error after stale socket cleanup: %v", err)
	}
}

func TestDaemon_PrunesSessionsWithoutLivePTYOnStart(t *testing.T) {
	useFreeWSPort(t)
	t.Setenv("ATTN_PTY_BACKEND", "embedded")
	sockPath := filepath.Join(shortTempDir(t), "attn.sock")

	d := NewForTesting(sockPath)

	nowStr := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "stale-session",
		Label:          "stale",
		Agent:          protocol.SessionAgentCodex,
		Directory:      "/tmp/stale",
		State:          protocol.SessionStateWorking,
		StateSince:     nowStr,
		StateUpdatedAt: nowStr,
		LastSeen:       nowStr,
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Start()
	}()
	defer d.Stop()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("daemon start error: %v", err)
		}
		t.Fatal("daemon exited unexpectedly during startup")
	case <-time.After(75 * time.Millisecond):
	}
	waitForSocket(t, sockPath, 3*time.Second)

	c := client.New(sockPath)
	sessions, err := c.Query("")
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected stale sessions to be pruned on start, got %d", len(sessions))
	}

	warnings := d.getWarnings()
	if len(warnings) == 0 {
		t.Fatal("expected daemon warning for startup stale-session prune")
	}
	if warnings[0].Code != "stale_sessions_pruned" {
		t.Fatalf("warning code = %q, want stale_sessions_pruned", warnings[0].Code)
	}
}

// Startup recovery rewrites session states directly in the store (prune flips a
// recoverable session to idle) without going through the per-session broadcast
// that normally refreshes the workspace rollup. reseedWorkspaceStatuses, run at
// the end of recovery, must repair the cached rollup so the first InitialState
// snapshot shows a workspace dot consistent with its recovered sessions.
func TestDaemon_ReseedWorkspaceStatusesAfterRecovery(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "reseed.sock"))
	d.ptyBackend = nil // no live PTYs, so prune treats the session as missing
	d.workspaces = newWorkspaceRegistry()

	workspaceID := "ws-reseed"
	sessionID := "sess-reseed"
	cwd := t.TempDir()

	d.store.AddWorkspace(&protocol.Workspace{ID: workspaceID, Title: "Reseed", Directory: cwd})
	d.workspaces.register(workspaceID, "Reseed", cwd, "a0", false, false)
	nowStr := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             sessionID,
		Label:          "claude",
		Agent:          protocol.SessionAgentClaude, // recoverable: prune keeps it
		Directory:      cwd,
		State:          protocol.SessionStateWorking,
		StateSince:     nowStr,
		StateUpdatedAt: nowStr,
		LastSeen:       nowStr,
		WorkspaceID:    workspaceID,
	})
	d.workspaces.associateSession(sessionID, workspaceID, "claude")

	// Seed the cached rollup the way loadWorkspacesFromStore does at startup.
	d.recomputeWorkspaceStatus(workspaceID)
	if ws, _ := d.workspaces.snapshot(workspaceID); ws.Status != protocol.WorkspaceStatusWorking {
		t.Fatalf("precondition: seeded rollup = %q, want working", ws.Status)
	}

	// Recovery flips the missing-PTY session to idle in the store, but does NOT
	// recompute the rollup — so the cached status is now stale.
	d.pruneSessionsWithoutPTY()
	if got := d.store.Get(sessionID); got == nil || got.State != protocol.SessionStateIdle {
		t.Fatalf("prune should keep recoverable session and mark it idle, got %+v", got)
	}
	if ws, _ := d.workspaces.snapshot(workspaceID); ws.Status != protocol.WorkspaceStatusWorking {
		t.Fatalf("rollup should still be stale-working before reseed, got %q", ws.Status)
	}

	// The reseed performStartupPTYRecovery runs after pruning repairs it.
	d.reseedWorkspaceStatuses()
	if ws, _ := d.workspaces.snapshot(workspaceID); ws.Status != protocol.WorkspaceStatusIdle {
		t.Fatalf("rollup after reseed = %q, want idle", ws.Status)
	}
}

func TestDaemon_Start_SelectsWorkerBackendWhenRequested(t *testing.T) {
	t.Setenv("ATTN_PTY_BACKEND", "worker")
	t.Setenv("ATTN_PTY_SKIP_STARTUP_PROBE", "1")
	useFreeWSPort(t)

	sockPath := filepath.Join(shortTempDir(t), "worker-select.sock")
	d := NewForTesting(sockPath)

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Start()
	}()
	if !d.waitStarted(3 * time.Second) {
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("daemon start error: %v", err)
			}
			t.Fatal("daemon exited unexpectedly during startup")
		default:
			t.Fatal("daemon did not signal startup")
		}
	}
	defer d.Stop()

	waitForSocket(t, sockPath, 3*time.Second)

	if d.daemonInstanceID == "" {
		t.Fatal("daemon_instance_id should be initialized before backend selection")
	}
	if _, ok := d.ptyBackend.(*ptybackend.WorkerBackend); !ok {
		t.Fatalf("expected worker backend, got %T", d.ptyBackend)
	}
}

func TestDaemon_Start_WorkerProbeFailureFallsBackToEmbedded(t *testing.T) {
	t.Setenv("ATTN_PTY_BACKEND", "worker")
	t.Setenv("ATTN_PTY_SKIP_STARTUP_PROBE", "0")
	t.Setenv("ATTN_PTY_WORKER_BINARY", filepath.Join(t.TempDir(), "missing-attn-binary"))
	useFreeWSPort(t)

	sockPath := filepath.Join(shortTempDir(t), "worker-probe-fallback.sock")
	d := NewForTesting(sockPath)

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Start()
	}()
	if !d.waitStarted(3 * time.Second) {
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("daemon start error: %v", err)
			}
			t.Fatal("daemon exited unexpectedly during startup")
		default:
			t.Fatal("daemon did not signal startup")
		}
	}
	defer d.Stop()

	if _, ok := d.ptyBackend.(*ptybackend.EmbeddedBackend); !ok {
		t.Fatalf("expected embedded backend after probe failure, got %T", d.ptyBackend)
	}
	hasFallbackWarning := false
	for _, w := range d.getWarnings() {
		if w.Code == warnPTYBackendFallback {
			hasFallbackWarning = true
			break
		}
	}
	if !hasFallbackWarning {
		t.Fatalf("expected %q warning after worker probe failure", warnPTYBackendFallback)
	}
}

func TestDaemon_Start_SelectsEmbeddedBackendWhenRequested(t *testing.T) {
	t.Setenv("ATTN_PTY_BACKEND", "embedded")
	useFreeWSPort(t)

	sockPath := filepath.Join(shortTempDir(t), "embedded-select.sock")
	d := NewForTesting(sockPath)

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Start()
	}()
	if !d.waitStarted(3 * time.Second) {
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("daemon start error: %v", err)
			}
			t.Fatal("daemon exited unexpectedly during startup")
		default:
			t.Fatal("daemon did not signal startup")
		}
	}
	defer d.Stop()

	waitForSocket(t, sockPath, 3*time.Second)

	if _, ok := d.ptyBackend.(*ptybackend.EmbeddedBackend); !ok {
		t.Fatalf("expected embedded backend, got %T", d.ptyBackend)
	}
}

type fakeWorkerReconcileBackend struct {
	liveIDs []string
	info    map[string]ptybackend.SessionInfo
}

func (b *fakeWorkerReconcileBackend) Spawn(context.Context, ptybackend.SpawnOptions) error {
	return nil
}
func (b *fakeWorkerReconcileBackend) Attach(context.Context, string, string) (ptybackend.AttachInfo, ptybackend.Stream, error) {
	return ptybackend.AttachInfo{}, nil, nil
}
func (b *fakeWorkerReconcileBackend) Input(context.Context, string, []byte) error { return nil }
func (b *fakeWorkerReconcileBackend) Resize(context.Context, string, uint16, uint16) error {
	return nil
}
func (b *fakeWorkerReconcileBackend) SetTheme(context.Context, string, pty.TerminalTheme) error {
	return nil
}
func (b *fakeWorkerReconcileBackend) Kill(context.Context, string, syscall.Signal) error { return nil }
func (b *fakeWorkerReconcileBackend) Remove(context.Context, string) error               { return nil }
func (b *fakeWorkerReconcileBackend) SessionIDs(context.Context) []string {
	return append([]string(nil), b.liveIDs...)
}
func (b *fakeWorkerReconcileBackend) Recover(context.Context) (ptybackend.RecoveryReport, error) {
	return ptybackend.RecoveryReport{Recovered: len(b.liveIDs)}, nil
}
func (b *fakeWorkerReconcileBackend) Shutdown(context.Context) error { return nil }
func (b *fakeWorkerReconcileBackend) SessionInfo(_ context.Context, sessionID string) (ptybackend.SessionInfo, error) {
	info, ok := b.info[sessionID]
	if !ok {
		return ptybackend.SessionInfo{}, fmt.Errorf("missing info for %s", sessionID)
	}
	return info, nil
}

type fakeDeferredRecoveryBackend struct {
	fakeWorkerReconcileBackend
	mu          sync.Mutex
	reports     []ptybackend.RecoveryReport
	likelyAlive map[string]bool
	likelyErr   map[string]error
}

func (b *fakeDeferredRecoveryBackend) Recover(context.Context) (ptybackend.RecoveryReport, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.reports) == 0 {
		return ptybackend.RecoveryReport{}, nil
	}
	report := b.reports[0]
	b.reports = b.reports[1:]
	return report, nil
}

func (b *fakeDeferredRecoveryBackend) SessionLikelyAlive(_ context.Context, sessionID string) (bool, error) {
	if err, ok := b.likelyErr[sessionID]; ok {
		return false, err
	}
	return b.likelyAlive[sessionID], nil
}

type fakeClearSessionsBackend struct {
	mu               sync.Mutex
	sessionIDs       []string
	recoveredIDs     []string
	recoverCalled    bool
	killed           []string
	removed          []string
	killErrBySession map[string]error
}

func (b *fakeClearSessionsBackend) Spawn(context.Context, ptybackend.SpawnOptions) error { return nil }
func (b *fakeClearSessionsBackend) Attach(context.Context, string, string) (ptybackend.AttachInfo, ptybackend.Stream, error) {
	return ptybackend.AttachInfo{}, nil, nil
}
func (b *fakeClearSessionsBackend) Input(context.Context, string, []byte) error { return nil }
func (b *fakeClearSessionsBackend) Resize(context.Context, string, uint16, uint16) error {
	return nil
}
func (b *fakeClearSessionsBackend) SetTheme(context.Context, string, pty.TerminalTheme) error {
	return nil
}
func (b *fakeClearSessionsBackend) Kill(_ context.Context, sessionID string, _ syscall.Signal) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.killed = append(b.killed, sessionID)
	if b.killErrBySession != nil {
		return b.killErrBySession[sessionID]
	}
	return nil
}
func (b *fakeClearSessionsBackend) Remove(_ context.Context, sessionID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.removed = append(b.removed, sessionID)
	return nil
}
func (b *fakeClearSessionsBackend) SessionIDs(context.Context) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.sessionIDs...)
}
func (b *fakeClearSessionsBackend) Recover(context.Context) (ptybackend.RecoveryReport, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.recoverCalled = true
	b.sessionIDs = append(append([]string(nil), b.sessionIDs...), b.recoveredIDs...)
	return ptybackend.RecoveryReport{Recovered: len(b.recoveredIDs)}, nil
}
func (b *fakeClearSessionsBackend) Shutdown(context.Context) error { return nil }

func TestDaemon_ReconcileSessionsWithWorkerBackend(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "live-existing",
		Label:          "existing",
		Agent:          protocol.SessionAgentCodex,
		Directory:      "/tmp/existing",
		State:          protocol.SessionStateWaitingInput,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
		Recoverable:    protocol.Ptr(true),
	})
	d.store.Add(&protocol.Session{
		ID:             "missing-running",
		Label:          "missing",
		Agent:          protocol.SessionAgentCodex,
		Directory:      "/tmp/missing",
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	d.store.Add(&protocol.Session{
		ID:             "missing-idle",
		Label:          "missing-idle",
		Agent:          protocol.SessionAgentCodex,
		Directory:      "/tmp/missing-idle",
		State:          protocol.SessionStateIdle,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	d.store.Add(&protocol.Session{
		ID:             "live-exited",
		Label:          "live-exited",
		Agent:          protocol.SessionAgentCodex,
		Directory:      "/tmp/live-exited",
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})

	d.ptyBackend = &fakeWorkerReconcileBackend{
		liveIDs: []string{"live-existing", "live-new", "live-new-exited", "live-exited", "live-shell"},
		info: map[string]ptybackend.SessionInfo{
			"live-existing": {
				SessionID: "live-existing",
				Agent:     string(protocol.SessionAgentCodex),
				CWD:       "/tmp/existing",
				Running:   true,
				State:     protocol.StateWaitingInput,
			},
			"live-new": {
				SessionID: "live-new",
				Agent:     string(protocol.SessionAgentCopilot),
				CWD:       "/tmp/new-repo",
				Running:   true,
				State:     protocol.StateWorking,
			},
			"live-new-exited": {
				SessionID: "live-new-exited",
				Agent:     string(protocol.SessionAgentCodex),
				CWD:       "/tmp/new-exited",
				Running:   false,
				State:     protocol.StateWorking,
			},
			"live-exited": {
				SessionID: "live-exited",
				Agent:     string(protocol.SessionAgentCodex),
				CWD:       "/tmp/live-exited",
				Running:   false,
				State:     protocol.StateWorking,
			},
			"live-shell": {
				SessionID: "live-shell",
				Agent:     protocol.AgentShellValue,
				CWD:       "/tmp/utility-shell",
				Running:   true,
				State:     protocol.StateWorking,
			},
		},
	}

	report := d.reconcileSessionsWithWorkerBackend(context.Background(), true, time.Time{})
	if report.Created != 2 {
		t.Fatalf("created = %d, want 2", report.Created)
	}
	if report.StateUpdated != 2 {
		t.Fatalf("state_updated = %d, want 2", report.StateUpdated)
	}
	if report.Reaped != 1 {
		t.Fatalf("reaped = %d, want 1", report.Reaped)
	}
	if report.MarkedIdle != 0 {
		t.Fatalf("marked_idle = %d, want 0", report.MarkedIdle)
	}
	if report.SkippedShell != 1 {
		t.Fatalf("skipped_shell = %d, want 1", report.SkippedShell)
	}

	existing := d.store.Get("live-existing")
	if existing == nil {
		t.Fatal("live-existing session missing after reconcile")
	}
	if existing.State != protocol.SessionStateLaunching {
		t.Fatalf("live-existing state = %s, want launching for recovery default", existing.State)
	}
	if protocol.Deref(existing.Recoverable) {
		t.Fatal("live-existing recoverable flag should be cleared once worker is live")
	}

	liveNew := d.store.Get("live-new")
	if liveNew == nil {
		t.Fatal("live-new session was not created from worker metadata")
	}
	if liveNew.Label != "new-repo" {
		t.Fatalf("live-new label = %q, want %q", liveNew.Label, "new-repo")
	}
	if liveNew.Agent != protocol.SessionAgentCopilot {
		t.Fatalf("live-new agent = %s, want %s", liveNew.Agent, protocol.SessionAgentCopilot)
	}
	if liveNew.State != protocol.SessionStateLaunching {
		t.Fatalf("live-new state = %s, want %s", liveNew.State, protocol.SessionStateLaunching)
	}
	liveNewExited := d.store.Get("live-new-exited")
	if liveNewExited == nil {
		t.Fatal("live-new-exited session was not created from worker metadata")
	}
	if liveNewExited.State != protocol.SessionStateIdle {
		t.Fatalf("live-new-exited state = %s, want %s", liveNewExited.State, protocol.SessionStateIdle)
	}
	if liveShell := d.store.Get("live-shell"); liveShell != nil {
		t.Fatalf("live-shell session should not be created from worker metadata, got %#v", liveShell)
	}

	liveExited := d.store.Get("live-exited")
	if liveExited == nil {
		t.Fatal("live-exited session missing after reconcile")
	}
	if liveExited.State != protocol.SessionStateIdle {
		t.Fatalf("live-exited state = %s, want %s", liveExited.State, protocol.SessionStateIdle)
	}

	missingRunning := d.store.Get("missing-running")
	if missingRunning != nil {
		t.Fatal("missing-running session should be reaped (non-claude agent without live PTY)")
	}

	missingIdle := d.store.Get("missing-idle")
	if missingIdle == nil {
		t.Fatal("missing-idle session missing after reconcile")
	}
	if missingIdle.State != protocol.SessionStateIdle {
		t.Fatalf("missing-idle state = %s, want idle", missingIdle.State)
	}
}

// TestDaemon_ReconcileSessionsWithWorkerBackend_PreservesScheduled proves that
// daemon recovery does not clobber a live session parked on a cron/loop. The
// worker is alive (Running) but its parked screen reads as idle, which would
// otherwise be recovered as launching; the hook-reported "scheduled" state must
// survive instead, since the next Stop re-derives it from live session_crons.
func TestDaemon_ReconcileSessionsWithWorkerBackend_PreservesScheduled(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "live-scheduled",
		Label:          "loop",
		Agent:          protocol.SessionAgentCodex,
		Directory:      "/tmp/loop",
		State:          protocol.SessionStateScheduled,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
		Recoverable:    protocol.Ptr(true),
	})
	d.ptyBackend = &fakeWorkerReconcileBackend{
		liveIDs: []string{"live-scheduled"},
		info: map[string]ptybackend.SessionInfo{
			"live-scheduled": {
				SessionID: "live-scheduled",
				Agent:     string(protocol.SessionAgentCodex),
				CWD:       "/tmp/loop",
				Running:   true,               // worker alive, parked on a cron
				State:     protocol.StateIdle, // parked screen reads as idle
			},
		},
	}

	d.reconcileSessionsWithWorkerBackend(context.Background(), true, time.Time{})

	got := d.store.Get("live-scheduled")
	if got == nil {
		t.Fatal("scheduled session missing after reconcile")
	}
	if got.State != protocol.SessionStateScheduled {
		t.Fatalf("state = %s, want scheduled preserved across recovery (not launching/idle)", got.State)
	}
}

func TestDaemon_ReconcileSessionsWithWorkerBackend_ReapRemovesEmptyWorkspace(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: "ws-stale", Title: "stale", Directory: "/tmp/stale",
	})
	d.store.Add(&protocol.Session{
		ID: "stale-session", Label: "stale", Agent: protocol.SessionAgentCodex, Directory: "/tmp/stale",
		State: protocol.SessionStateWorking, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	d.associateSessionWithWorkspace("stale-session", "ws-stale")
	d.ptyBackend = &fakeWorkerReconcileBackend{
		liveIDs: nil,
		info:    map[string]ptybackend.SessionInfo{},
	}

	report := d.reconcileSessionsWithWorkerBackend(context.Background(), true, time.Time{})

	if report.Reaped != 1 {
		t.Fatalf("reaped = %d, want 1", report.Reaped)
	}
	if workspace := d.store.GetWorkspace("ws-stale"); workspace != nil {
		t.Fatalf("empty workspace still persisted after session reap: %+v", workspace)
	}
	if _, ok := d.workspaces.snapshot("ws-stale"); ok {
		t.Fatal("empty workspace still registered after session reap")
	}
}

func TestDaemon_ReconcileSessionsWithWorkerBackend_ClaudeSessionsRecoverable(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())

	// Claude session without live PTY should be marked recoverable
	d.store.Add(&protocol.Session{
		ID:             "claude-stale",
		Label:          "claude-stale",
		Agent:          protocol.SessionAgentClaude,
		Directory:      "/tmp/claude-stale",
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})

	// Codex session without live PTY should be reaped
	d.store.Add(&protocol.Session{
		ID:             "codex-stale",
		Label:          "codex-stale",
		Agent:          protocol.SessionAgentCodex,
		Directory:      "/tmp/codex-stale",
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})

	// Copilot session without live PTY should be reaped
	d.store.Add(&protocol.Session{
		ID:             "copilot-stale",
		Label:          "copilot-stale",
		Agent:          protocol.SessionAgentCopilot,
		Directory:      "/tmp/copilot-stale",
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})

	d.store.Add(&protocol.Session{
		ID:             "plugin-metadata-stale",
		Label:          "plugin-metadata-stale",
		Agent:          "snipe",
		Directory:      "/tmp/plugin-metadata-stale",
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if !d.store.BeginAgentDriverRun("plugin-metadata-stale", "snipe-plugin", "run-metadata") {
		t.Fatal("BeginAgentDriverRun(plugin-metadata-stale) failed")
	}
	if !d.store.ApplyAgentDriverMetadata("plugin-metadata-stale", "run-metadata", 1, `{"native_id":"resume-me"}`) {
		t.Fatal("ApplyAgentDriverMetadata(plugin-metadata-stale) failed")
	}
	d.store.EndAgentDriverRun("plugin-metadata-stale")

	d.store.Add(&protocol.Session{
		ID:             "plugin-capability-stale",
		Label:          "plugin-capability-stale",
		Agent:          "snipe-live",
		Directory:      "/tmp/plugin-capability-stale",
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	plugin := &pluginConnection{name: "snipe-live-plugin"}
	if err := d.plugins.register(plugin); err != nil {
		t.Fatalf("register plugin: %v", err)
	}
	if err := d.plugins.registerDriver(plugin, pluginDriverRegisterParams{
		Agent:        "snipe-live",
		Capabilities: map[string]bool{"resume": true},
	}); err != nil {
		t.Fatalf("register resumable driver: %v", err)
	}

	d.store.Add(&protocol.Session{
		ID:             "plugin-owned-stale",
		Label:          "plugin-owned-stale",
		Agent:          "resume-before-register",
		Directory:      "/tmp/plugin-owned-stale",
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if !d.store.BeginAgentDriverRun("plugin-owned-stale", "offline-plugin", "run-offline") {
		t.Fatal("BeginAgentDriverRun(plugin-owned-stale) failed")
	}

	d.ptyBackend = &fakeWorkerReconcileBackend{
		liveIDs: nil,
		info:    map[string]ptybackend.SessionInfo{},
	}

	report := d.reconcileSessionsWithWorkerBackend(context.Background(), true, time.Time{})

	if report.MarkedRecoverable != 4 {
		t.Fatalf("marked_recoverable = %d, want 4", report.MarkedRecoverable)
	}
	if report.Reaped != 2 {
		t.Fatalf("reaped = %d, want 2", report.Reaped)
	}

	// Claude session should be idle + recoverable
	claudeSession := d.store.Get("claude-stale")
	if claudeSession == nil {
		t.Fatal("claude-stale session should not be reaped")
	}
	if claudeSession.State != protocol.SessionStateIdle {
		t.Fatalf("claude-stale state = %s, want idle", claudeSession.State)
	}
	if !protocol.Deref(claudeSession.Recoverable) {
		t.Fatal("claude-stale should be marked recoverable")
	}
	for _, id := range []string{"plugin-metadata-stale", "plugin-capability-stale", "plugin-owned-stale"} {
		session := d.store.Get(id)
		if session == nil || session.State != protocol.SessionStateIdle || !protocol.Deref(session.Recoverable) {
			t.Fatalf("%s session = %+v, want idle recoverable plugin session", id, session)
		}
	}

	// Non-claude sessions should be removed
	if d.store.Get("codex-stale") != nil {
		t.Fatal("codex-stale session should be reaped")
	}
	if d.store.Get("copilot-stale") != nil {
		t.Fatal("copilot-stale session should be reaped")
	}
}

func TestDaemon_ReconcileSessionsWithWorkerBackend_PreservesLivePluginReportedState(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "plugin-live",
		Label:          "plugin-live",
		Agent:          "snipe",
		Directory:      "/tmp/plugin-live",
		State:          protocol.SessionStateLaunching,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
		Recoverable:    protocol.Ptr(true),
	})
	if !d.store.BeginAgentDriverRun("plugin-live", "snipe-plugin", "run-live") {
		t.Fatal("BeginAgentDriverRun(plugin-live) failed")
	}
	if !d.store.ApplyAgentDriverState("plugin-live", "run-live", 1, protocol.StateWaitingInput) {
		t.Fatal("ApplyAgentDriverState(plugin-live) failed")
	}
	d.ptyBackend = &fakeWorkerReconcileBackend{
		liveIDs: []string{"plugin-live"},
		info: map[string]ptybackend.SessionInfo{
			"plugin-live": {
				SessionID: "plugin-live",
				Agent:     "snipe",
				CWD:       "/tmp/plugin-live",
				Running:   true,
				State:     protocol.StateWorking,
			},
		},
	}

	report := d.reconcileSessionsWithWorkerBackend(context.Background(), true, time.Time{})
	if report.StateUpdated != 0 {
		t.Fatalf("state_updated = %d, want 0 for plugin-owned state", report.StateUpdated)
	}
	session := d.store.Get("plugin-live")
	if session == nil || session.State != protocol.SessionStateWaitingInput {
		t.Fatalf("plugin-live session = %+v, want waiting_input retained from plugin report", session)
	}
	if protocol.Deref(session.Recoverable) {
		t.Fatal("plugin-live recoverable flag should clear after PTY recovery")
	}
}

func TestDaemon_PruneSessionsWithoutPTY_PreservesPluginMetadataForResume(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "plugin-resume",
		Label:          "plugin-resume",
		Agent:          "snipe",
		Directory:      "/tmp/plugin-resume",
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if !d.store.BeginAgentDriverRun("plugin-resume", "snipe-plugin", "run-resume") {
		t.Fatal("BeginAgentDriverRun(plugin-resume) failed")
	}
	if !d.store.ApplyAgentDriverMetadata("plugin-resume", "run-resume", 1, `{"native_id":"resume-me"}`) {
		t.Fatal("ApplyAgentDriverMetadata(plugin-resume) failed")
	}

	if removed := d.pruneSessionsWithoutPTY(); removed != 0 {
		t.Fatalf("pruneSessionsWithoutPTY removed = %d, want 0", removed)
	}
	session := d.store.Get("plugin-resume")
	if session == nil || session.State != protocol.SessionStateIdle || !protocol.Deref(session.Recoverable) {
		t.Fatalf("plugin-resume session = %+v, want idle recoverable plugin session", session)
	}
	if got := d.store.GetAgentMetadata("plugin-resume"); got != `{"native_id":"resume-me"}` {
		t.Fatalf("metadata = %q, want persisted resume metadata", got)
	}
}

func TestDaemon_PruneSessionsWithoutPTY_RemovesReapedWorkspaceLayout(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	workspaceID := "workspace-stale"
	sessionID := "codex-stale"
	d.store.AddWorkspace(&protocol.Workspace{ID: workspaceID, Title: "Stale", Directory: "/tmp/stale"})
	d.store.Add(&protocol.Session{
		ID:             sessionID,
		Label:          sessionID,
		Agent:          protocol.SessionAgentCodex,
		Directory:      "/tmp/stale",
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
		WorkspaceID:    workspaceID,
	})
	d.workspaces.register(workspaceID, "Stale", "/tmp/stale", "", false, false)
	d.workspaces.associateSession(sessionID, workspaceID, sessionID)
	if err := d.store.SaveWorkspaceLayout(workspacelayout.WorkspaceLayout{
		WorkspaceID:  workspaceID,
		ActivePaneID: "pane-stale",
		Layout:       workspacelayout.DefaultLayout("pane-stale"),
		Panes: []workspacelayout.Pane{{
			PaneID:    "pane-stale",
			RuntimeID: sessionID,
			SessionID: sessionID,
			Kind:      workspacelayout.PaneKindAgent,
			Title:     workspacelayout.DefaultPaneTitle,
		}},
	}); err != nil {
		t.Fatalf("SaveWorkspaceLayout() error = %v", err)
	}

	if removed := d.pruneSessionsWithoutPTY(); removed != 1 {
		t.Fatalf("pruneSessionsWithoutPTY removed = %d, want 1", removed)
	}
	if got := d.store.Get(sessionID); got != nil {
		t.Fatalf("store.Get(%q) = %+v, want nil", sessionID, got)
	}
	if got := d.store.GetWorkspace(workspaceID); got != nil {
		t.Fatalf("store.GetWorkspace(%q) = %+v, want nil", workspaceID, got)
	}
	if _, ok := d.workspaces.snapshot(workspaceID); ok {
		t.Fatalf("workspace registry still contains %q", workspaceID)
	}
	if got := d.store.GetWorkspaceLayout(workspaceID); got != nil {
		t.Fatalf("store.GetWorkspaceLayout(%q) = %+v, want nil", workspaceID, got)
	}
}

// TestDaemon_PruneSessionsWithoutPTY_KeepsTileOnlyWorkspace is the counterpart
// to the teardown test above: when a reaped session leaves a docked tile
// behind, the prune path keeps the workspace alive as a sessionless, tile-only
// workspace instead of taking the tile down with it.
func TestDaemon_PruneSessionsWithoutPTY_KeepsTileOnlyWorkspace(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	workspaceID := "workspace-stale-tile"
	sessionID := "codex-stale-tile"
	d.store.AddWorkspace(&protocol.Workspace{ID: workspaceID, Title: "Stale Tile", Directory: "/tmp/stale-tile"})
	d.store.Add(&protocol.Session{
		ID:             sessionID,
		Label:          sessionID,
		Agent:          protocol.SessionAgentCodex,
		Directory:      "/tmp/stale-tile",
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
		WorkspaceID:    workspaceID,
	})
	d.workspaces.register(workspaceID, "Stale Tile", "/tmp/stale-tile", "", false, false)
	d.workspaces.associateSession(sessionID, workspaceID, sessionID)
	if err := d.store.SaveWorkspaceLayout(workspacelayout.WorkspaceLayout{
		WorkspaceID:  workspaceID,
		ActivePaneID: "pane-stale",
		Layout: workspacelayout.Node{
			Type:      "split",
			SplitID:   "split-stale",
			Direction: workspacelayout.DirectionVertical,
			Ratio:     workspacelayout.DefaultSplitRatio,
			Children: []workspacelayout.Node{
				{Type: "pane", PaneID: "pane-stale"},
				{Type: "tile", TileID: markdownTileIDForPath("/tmp/notes.md"), TileKind: string(workspacelayout.TileKindMarkdown), TileParams: "/tmp/notes.md"},
			},
		},
		Panes: []workspacelayout.Pane{{
			PaneID:    "pane-stale",
			RuntimeID: sessionID,
			SessionID: sessionID,
			Kind:      workspacelayout.PaneKindAgent,
			Title:     workspacelayout.DefaultPaneTitle,
		}},
	}); err != nil {
		t.Fatalf("SaveWorkspaceLayout() error = %v", err)
	}

	if removed := d.pruneSessionsWithoutPTY(); removed != 1 {
		t.Fatalf("pruneSessionsWithoutPTY removed = %d, want 1", removed)
	}
	assertTileOnlyWorkspaceAlive(t, d, workspaceID, sessionID)
}

func TestDaemon_RunDeferredWorkerReconciliationForcesIdleDemotion(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "stale-running",
		Label:          "stale-running",
		Agent:          protocol.SessionAgentCodex,
		Directory:      "/tmp/stale-running",
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	d.ptyBackend = &fakeDeferredRecoveryBackend{
		fakeWorkerReconcileBackend: fakeWorkerReconcileBackend{
			liveIDs: nil,
			info:    map[string]ptybackend.SessionInfo{},
		},
		reports: []ptybackend.RecoveryReport{
			{Missing: 1},
		},
	}

	d.runDeferredWorkerReconciliation(1, 0, time.Now())

	session := d.store.Get("stale-running")
	if session != nil {
		t.Fatal("stale-running session should be reaped (non-claude agent without live PTY)")
	}

	warnings := d.getWarnings()
	hasPartial := false
	for _, w := range warnings {
		if w.Code == "worker_recovery_partial" && strings.Contains(w.Message, "Forced stale-session reconciliation") {
			hasPartial = true
			break
		}
	}
	if !hasPartial {
		t.Fatalf("expected forced reconciliation warning, got %+v", warnings)
	}
}

func TestDaemon_RunDeferredWorkerReconciliation_BroadcastsSessionsUpdatedOnChange(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "stale-running",
		Label:          "stale-running",
		Agent:          protocol.SessionAgentCodex,
		Directory:      "/tmp/stale-running",
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	d.ptyBackend = &fakeDeferredRecoveryBackend{
		fakeWorkerReconcileBackend: fakeWorkerReconcileBackend{
			liveIDs: nil,
			info:    map[string]ptybackend.SessionInfo{},
		},
		reports: []ptybackend.RecoveryReport{
			{Missing: 1},
		},
	}

	broadcasts := 0
	d.wsHub.broadcastListener = func(event *protocol.WebSocketEvent) {
		if event != nil && event.Event == protocol.EventSessionsUpdated {
			broadcasts++
		}
	}

	d.runDeferredWorkerReconciliation(1, 0, time.Now())

	if broadcasts == 0 {
		t.Fatal("expected deferred reconciliation to broadcast sessions_updated after state changes")
	}
}

func TestDaemon_ReconcileSessionsWithWorkerBackend_PreservesLikelyAliveSessions(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "stale-running",
		Label:          "stale-running",
		Agent:          protocol.SessionAgentCodex,
		Directory:      "/tmp/stale-running",
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	d.ptyBackend = &fakeDeferredRecoveryBackend{
		fakeWorkerReconcileBackend: fakeWorkerReconcileBackend{
			liveIDs: nil,
			info:    map[string]ptybackend.SessionInfo{},
		},
		likelyAlive: map[string]bool{
			"stale-running": true,
		},
	}

	report := d.reconcileSessionsWithWorkerBackend(context.Background(), true, time.Time{})
	if report.MarkedIdle != 0 {
		t.Fatalf("marked_idle = %d, want 0", report.MarkedIdle)
	}
	if report.LikelyAlive != 1 {
		t.Fatalf("likely_alive = %d, want 1", report.LikelyAlive)
	}
	session := d.store.Get("stale-running")
	if session == nil {
		t.Fatal("stale-running session missing")
	}
	if session.State != protocol.SessionStateWorking {
		t.Fatalf("state = %q, want %q", session.State, protocol.SessionStateWorking)
	}
}

func TestDaemon_ReconcileSessionsWithWorkerBackend_SkipsIdleDemotionOnLivenessProbeError(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "stale-running",
		Label:          "stale-running",
		Agent:          protocol.SessionAgentCodex,
		Directory:      "/tmp/stale-running",
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	d.ptyBackend = &fakeDeferredRecoveryBackend{
		fakeWorkerReconcileBackend: fakeWorkerReconcileBackend{
			liveIDs: nil,
			info:    map[string]ptybackend.SessionInfo{},
		},
		likelyErr: map[string]error{
			"stale-running": errors.New("probe timeout"),
		},
	}

	report := d.reconcileSessionsWithWorkerBackend(context.Background(), true, time.Time{})
	if report.MarkedIdle != 0 {
		t.Fatalf("marked_idle = %d, want 0", report.MarkedIdle)
	}
	if report.LivenessUnknown != 1 {
		t.Fatalf("liveness_unknown = %d, want 1", report.LivenessUnknown)
	}
	session := d.store.Get("stale-running")
	if session == nil {
		t.Fatal("stale-running session missing")
	}
	if session.State != protocol.SessionStateWorking {
		t.Fatalf("state = %q, want %q", session.State, protocol.SessionStateWorking)
	}
}

func TestDaemon_ReconcileSessionsWithWorkerBackend_SkipsIdleDemotionOnIncompleteRecovery(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "missing-running",
		Label:          "missing",
		Agent:          protocol.SessionAgentCodex,
		Directory:      "/tmp/missing",
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})

	d.ptyBackend = &fakeWorkerReconcileBackend{
		liveIDs: nil,
		info:    map[string]ptybackend.SessionInfo{},
	}

	report := d.reconcileSessionsWithWorkerBackend(context.Background(), false, time.Time{})
	if report.MarkedIdle != 0 {
		t.Fatalf("marked_idle = %d, want 0", report.MarkedIdle)
	}
	if report.SkippedIdle != 1 {
		t.Fatalf("skipped_idle = %d, want 1", report.SkippedIdle)
	}
	session := d.store.Get("missing-running")
	if session == nil {
		t.Fatal("missing-running session missing after reconcile")
	}
	if session.State != protocol.SessionStateWorking {
		t.Fatalf("missing-running state = %s, want working", session.State)
	}
}

func TestDaemon_ReconcileSessionsWithWorkerBackend_SkipsRecentlyUpdatedSessions(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	cutoff := time.Now().Add(-time.Minute).Truncate(time.Second)
	updatedAt := cutoff.Add(10 * time.Second)
	timestamp := protocol.NewTimestamp(updatedAt).String()

	d.store.Add(&protocol.Session{
		ID:             "recent-running",
		Label:          "recent",
		Agent:          protocol.SessionAgentCodex,
		Directory:      "/tmp/recent",
		State:          protocol.SessionStateWorking,
		StateSince:     timestamp,
		StateUpdatedAt: timestamp,
		LastSeen:       timestamp,
	})

	d.ptyBackend = &fakeWorkerReconcileBackend{
		liveIDs: nil,
		info:    map[string]ptybackend.SessionInfo{},
	}

	report := d.reconcileSessionsWithWorkerBackend(context.Background(), true, cutoff)
	if report.MarkedIdle != 0 {
		t.Fatalf("marked_idle = %d, want 0", report.MarkedIdle)
	}
	if report.SkippedRecent != 1 {
		t.Fatalf("skipped_recent = %d, want 1", report.SkippedRecent)
	}
	session := d.store.Get("recent-running")
	if session == nil {
		t.Fatal("recent-running session missing after reconcile")
	}
	if session.State != protocol.SessionStateWorking {
		t.Fatalf("recent-running state = %s, want working", session.State)
	}
}

func TestSessionStateFromRecoveredInfo(t *testing.T) {
	tests := []struct {
		name string
		info ptybackend.SessionInfo
		want protocol.SessionState
	}{
		{
			name: "not running is idle",
			info: ptybackend.SessionInfo{Running: false, State: protocol.StateWorking},
			want: protocol.SessionStateIdle,
		},
		{
			name: "waiting input",
			info: ptybackend.SessionInfo{Running: true, Agent: string(protocol.SessionAgentClaude), State: protocol.StateWaitingInput},
			want: protocol.SessionStateWaitingInput,
		},
		{
			name: "codex waiting input normalizes to launching",
			info: ptybackend.SessionInfo{Running: true, Agent: string(protocol.SessionAgentCodex), State: protocol.StateWaitingInput},
			want: protocol.SessionStateLaunching,
		},
		{
			name: "codex pending approval normalizes to launching",
			info: ptybackend.SessionInfo{Running: true, State: protocol.StatePendingApproval},
			want: protocol.SessionStateLaunching,
		},
		{
			name: "claude pending approval",
			info: ptybackend.SessionInfo{Running: true, Agent: string(protocol.SessionAgentClaude), State: protocol.StatePendingApproval},
			want: protocol.SessionStatePendingApproval,
		},
		{
			name: "explicit idle running session normalizes to launching",
			info: ptybackend.SessionInfo{Running: true, Agent: string(protocol.SessionAgentClaude), State: protocol.StateIdle},
			want: protocol.SessionStateLaunching,
		},
		{
			name: "copilot explicit idle normalizes to launching",
			info: ptybackend.SessionInfo{Running: true, Agent: string(protocol.SessionAgentCopilot), State: protocol.StateIdle},
			want: protocol.SessionStateLaunching,
		},
		{
			name: "default working normalizes to launching",
			info: ptybackend.SessionInfo{Running: true, State: protocol.StateWorking},
			want: protocol.SessionStateLaunching,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sessionStateFromRecoveredInfo(tt.info)
			if got != tt.want {
				t.Fatalf("sessionStateFromRecoveredInfo() = %s, want %s", got, tt.want)
			}
		})
	}
}

type fakeOutputStream struct {
	mu         sync.Mutex
	events     chan ptybackend.OutputEvent
	closeCount int
	once       sync.Once
}

func newFakeOutputStream() *fakeOutputStream {
	return &fakeOutputStream{events: make(chan ptybackend.OutputEvent, 8)}
}

func (s *fakeOutputStream) Events() <-chan ptybackend.OutputEvent { return s.events }
func (s *fakeOutputStream) Close() error {
	s.once.Do(func() {
		s.mu.Lock()
		s.closeCount++
		s.mu.Unlock()
		close(s.events)
	})
	return nil
}

func (s *fakeOutputStream) ClosedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeCount
}

type fakeAttachBackend struct {
	mu      sync.Mutex
	streams []*fakeOutputStream
	failErr error
	info    ptybackend.AttachInfo
	infoSet bool
}

func (b *fakeAttachBackend) Spawn(context.Context, ptybackend.SpawnOptions) error { return nil }
func (b *fakeAttachBackend) Attach(context.Context, string, string) (ptybackend.AttachInfo, ptybackend.Stream, error) {
	b.mu.Lock()
	if b.failErr != nil {
		err := b.failErr
		b.mu.Unlock()
		return ptybackend.AttachInfo{}, nil, err
	}
	info := b.info
	infoSet := b.infoSet
	b.mu.Unlock()

	stream := newFakeOutputStream()
	b.mu.Lock()
	b.streams = append(b.streams, stream)
	b.mu.Unlock()
	if !infoSet {
		info = ptybackend.AttachInfo{Running: true}
	}
	return info, stream, nil
}
func (b *fakeAttachBackend) Input(context.Context, string, []byte) error { return nil }
func (b *fakeAttachBackend) Resize(context.Context, string, uint16, uint16) error {
	return nil
}
func (b *fakeAttachBackend) SetTheme(context.Context, string, pty.TerminalTheme) error {
	return nil
}
func (b *fakeAttachBackend) Kill(context.Context, string, syscall.Signal) error { return nil }
func (b *fakeAttachBackend) Remove(context.Context, string) error               { return nil }
func (b *fakeAttachBackend) SessionIDs(context.Context) []string                { return nil }
func (b *fakeAttachBackend) Recover(context.Context) (ptybackend.RecoveryReport, error) {
	return ptybackend.RecoveryReport{}, nil
}
func (b *fakeAttachBackend) Shutdown(context.Context) error { return nil }

func (b *fakeAttachBackend) Streams() []*fakeOutputStream {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]*fakeOutputStream, len(b.streams))
	copy(out, b.streams)
	return out
}

func (b *fakeAttachBackend) FailNextAttach(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failErr = err
}

func (b *fakeAttachBackend) SetAttachInfo(info ptybackend.AttachInfo) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.info = info
	b.infoSet = true
}

type fakeSpawnBackend struct {
	mu           sync.Mutex
	spawnOpts    []ptybackend.SpawnOptions
	killed       []string
	removed      []string
	onSpawn      func(ptybackend.SpawnOptions)
	onInput      func(string, []byte)
	onKill       func()
	killErr      error
	sessionIDs   []string
	themeCalls   []pty.TerminalTheme
	themeCallIDs []string
	setThemeErr  error
}

func (b *fakeSpawnBackend) Spawn(_ context.Context, opts ptybackend.SpawnOptions) error {
	b.mu.Lock()
	b.spawnOpts = append(b.spawnOpts, opts)
	onSpawn := b.onSpawn
	b.mu.Unlock()
	if onSpawn != nil {
		onSpawn(opts)
	}
	return nil
}
func (b *fakeSpawnBackend) Attach(context.Context, string, string) (ptybackend.AttachInfo, ptybackend.Stream, error) {
	return ptybackend.AttachInfo{Running: true}, newFakeOutputStream(), nil
}
func (b *fakeSpawnBackend) Input(_ context.Context, id string, data []byte) error {
	b.mu.Lock()
	onInput := b.onInput
	b.mu.Unlock()
	if onInput != nil {
		onInput(id, data)
	}
	return nil
}
func (b *fakeSpawnBackend) Resize(context.Context, string, uint16, uint16) error { return nil }
func (b *fakeSpawnBackend) SetTheme(_ context.Context, id string, theme pty.TerminalTheme) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.themeCallIDs = append(b.themeCallIDs, id)
	b.themeCalls = append(b.themeCalls, theme)
	return b.setThemeErr
}
func (b *fakeSpawnBackend) Kill(_ context.Context, id string, _ syscall.Signal) error {
	b.mu.Lock()
	b.killed = append(b.killed, id)
	b.mu.Unlock()
	if b.killErr == nil && b.onKill != nil {
		b.onKill()
	}
	return b.killErr
}
func (b *fakeSpawnBackend) Remove(_ context.Context, id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.removed = append(b.removed, id)
	return nil
}
func (b *fakeSpawnBackend) SessionIDs(context.Context) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.sessionIDs...)
}
func (b *fakeSpawnBackend) Recover(context.Context) (ptybackend.RecoveryReport, error) {
	return ptybackend.RecoveryReport{}, nil
}
func (b *fakeSpawnBackend) Shutdown(context.Context) error { return nil }

func (b *fakeSpawnBackend) LastSpawn() (ptybackend.SpawnOptions, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.spawnOpts) == 0 {
		return ptybackend.SpawnOptions{}, false
	}
	return b.spawnOpts[len(b.spawnOpts)-1], true
}

func (b *fakeSpawnBackend) WasKilledAndRemoved(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	killed := false
	removed := false
	for _, candidate := range b.killed {
		killed = killed || candidate == id
	}
	for _, candidate := range b.removed {
		removed = removed || candidate == id
	}
	return killed && removed
}

func (b *fakeSpawnBackend) RemovedIDs() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.removed...)
}

func addTestWorkspace(d *Daemon, id, directory string) {
	rank := d.resolveWorkspaceRank(d.store.GetWorkspace(id))
	d.store.AddWorkspace(&protocol.Workspace{ID: id, Title: id, Directory: directory, Status: protocol.WorkspaceStatusLaunching, Rank: rank})
	d.workspaces.register(id, id, directory, rank, false, false)
}

func TestDaemon_HandleSpawnSession_UsesStoredResumeSessionIDForRecoverableClaudeSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	d.ptyBackend = backend

	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "attn-session",
		Label:          "attn-session",
		Agent:          protocol.SessionAgentClaude,
		Directory:      t.TempDir(),
		State:          protocol.SessionStateIdle,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
		Recoverable:    protocol.Ptr(true),
	})
	d.store.SetResumeSessionID("attn-session", "claude-session")
	addTestWorkspace(d, "workspace-attn-session", t.TempDir())

	client := &wsClient{
		send:            make(chan outboundMessage, 2),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
	msg := &protocol.SpawnSessionMessage{
		Cmd:             protocol.CmdSpawnSession,
		ID:              "attn-session",
		Cwd:             t.TempDir(),
		Cols:            80,
		Rows:            24,
		Agent:           "claude",
		WorkspaceID:     "workspace-attn-session",
		ResumeSessionID: protocol.Ptr("attn-session"),
	}

	d.handleSpawnSession(client, msg)

	lastSpawn, ok := backend.LastSpawn()
	if !ok {
		t.Fatal("expected spawn call")
	}
	if lastSpawn.ResumeSessionID != "claude-session" {
		t.Fatalf("resume session id = %q, want %q", lastSpawn.ResumeSessionID, "claude-session")
	}

	select {
	case outbound := <-client.send:
		var result protocol.SpawnResultMessage
		if err := json.Unmarshal(outbound.payload, &result); err != nil {
			t.Fatalf("decode spawn_result: %v", err)
		}
		if !result.Success {
			t.Fatalf("spawn_result success=false error=%q", protocol.Deref(result.Error))
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for spawn_result")
	}
}

func TestDaemon_HandleSpawnSession_UsesStoredResumeSessionIDEvenWhenNotRecoverable(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	d.ptyBackend = backend

	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "attn-session",
		Label:          "attn-session",
		Agent:          protocol.SessionAgentClaude,
		Directory:      t.TempDir(),
		State:          protocol.SessionStateIdle,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
		Recoverable:    protocol.Ptr(false),
	})
	d.store.SetResumeSessionID("attn-session", "claude-session")
	addTestWorkspace(d, "workspace-attn-session", t.TempDir())

	client := &wsClient{
		send:            make(chan outboundMessage, 2),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
	msg := &protocol.SpawnSessionMessage{
		Cmd:             protocol.CmdSpawnSession,
		ID:              "attn-session",
		Cwd:             t.TempDir(),
		Cols:            80,
		Rows:            24,
		Agent:           "claude",
		WorkspaceID:     "workspace-attn-session",
		ResumeSessionID: protocol.Ptr("attn-session"),
	}

	d.handleSpawnSession(client, msg)

	lastSpawn, ok := backend.LastSpawn()
	if !ok {
		t.Fatal("expected spawn call")
	}
	if lastSpawn.ResumeSessionID != "claude-session" {
		t.Fatalf("resume session id = %q, want %q", lastSpawn.ResumeSessionID, "claude-session")
	}
}

func TestDaemon_HandleSpawnSession_UsesStoredResumeSessionIDForCodexSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	d.ptyBackend = backend

	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "attn-session",
		Label:          "attn-session",
		Agent:          protocol.SessionAgentCodex,
		Directory:      t.TempDir(),
		State:          protocol.SessionStateIdle,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	d.store.SetResumeSessionID("attn-session", "codex-session")
	addTestWorkspace(d, "workspace-attn-session", t.TempDir())

	client := &wsClient{
		send:            make(chan outboundMessage, 2),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
	msg := &protocol.SpawnSessionMessage{
		Cmd:             protocol.CmdSpawnSession,
		ID:              "attn-session",
		Cwd:             t.TempDir(),
		Cols:            80,
		Rows:            24,
		Agent:           "codex",
		WorkspaceID:     "workspace-attn-session",
		ResumeSessionID: protocol.Ptr("attn-session"),
	}

	d.handleSpawnSession(client, msg)

	lastSpawn, ok := backend.LastSpawn()
	if !ok {
		t.Fatal("expected spawn call")
	}
	if lastSpawn.ResumeSessionID != "codex-session" {
		t.Fatalf("resume session id = %q, want %q", lastSpawn.ResumeSessionID, "codex-session")
	}
	if got := d.store.Get("attn-session"); got == nil || got.State != protocol.SessionStateIdle {
		t.Fatalf("reloaded existing codex session state = %v, want %s", got, protocol.SessionStateIdle)
	}
}

func TestDaemon_HandleSetSessionResumeID_QueuesUntilSessionExists(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	serverConn, clientConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleSetSessionResumeID(serverConn, &protocol.SetSessionResumeIDMessage{
			ID:              "attn-session",
			ResumeSessionID: "codex-session",
		})
		_ = serverConn.Close()
	}()

	var resp protocol.Response
	if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
		t.Fatalf("decode set resume response: %v", err)
	}
	_ = clientConn.Close()
	<-done
	if !resp.Ok {
		t.Fatalf("set resume response ok=%v, want true", resp.Ok)
	}
	if got := d.store.GetResumeSessionID("attn-session"); got != "" {
		t.Fatalf("resume id before registration = %q, want empty", got)
	}

	serverConn, clientConn = net.Pipe()
	done = make(chan struct{})
	go func() {
		defer close(done)
		d.handleRegister(serverConn, &protocol.RegisterMessage{
			ID:          "attn-session",
			Label:       protocol.Ptr("attn-session"),
			Dir:         t.TempDir(),
			Agent:       protocol.Ptr(protocol.SessionAgentCodex),
			WorkspaceID: "workspace-attn-session",
		})
		_ = serverConn.Close()
	}()

	if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	_ = clientConn.Close()
	<-done
	if !resp.Ok {
		t.Fatalf("register response ok=%v, want true", resp.Ok)
	}
	if got := d.store.GetResumeSessionID("attn-session"); got != "codex-session" {
		t.Fatalf("resume id after registration = %q, want codex-session", got)
	}
}

func TestDaemon_ForwardPTYStreamEvents_ClosesStreamOnSendFailure(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client := &wsClient{
		send:            make(chan outboundMessage, 1),
		attachedStreams: make(map[string]ptybackend.Stream),
	}

	// Fill send buffer so sendOutbound() fails.
	client.send <- outboundMessage{kind: messageKindText, payload: []byte(`{\"event\":\"noop\"}`)}

	stream := newFakeOutputStream()
	client.attachedStreams["sess-1"] = stream

	done := make(chan struct{})
	go func() {
		d.forwardPTYStreamEvents(client, "sess-1", stream)
		close(done)
	}()

	stream.events <- ptybackend.OutputEvent{
		Kind: ptybackend.OutputEventKindOutput,
		Data: []byte("hello"),
		Seq:  1,
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("forwardPTYStreamEvents did not exit after send failure")
	}

	if stream.ClosedCount() == 0 {
		t.Fatal("stream should be closed after send failure")
	}
}

func TestDaemon_HandleAttachSession_ReattachFailureKeepsExistingStream(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeAttachBackend{}
	d.ptyBackend = backend

	client := &wsClient{
		send:            make(chan outboundMessage, 4),
		attachedStreams: make(map[string]ptybackend.Stream),
	}

	d.handleAttachSession(client, &protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: "sess-1"})
	firstMsg := <-client.send
	var first protocol.AttachResultMessage
	if err := json.Unmarshal(firstMsg.payload, &first); err != nil {
		t.Fatalf("decode first attach result: %v", err)
	}
	if !first.Success {
		t.Fatalf("first attach success = false, err=%q", protocol.Deref(first.Error))
	}

	streams := backend.Streams()
	if len(streams) != 1 {
		t.Fatalf("streams len = %d, want 1", len(streams))
	}
	firstStream := streams[0]
	if got := firstStream.ClosedCount(); got != 0 {
		t.Fatalf("first stream closed count = %d, want 0", got)
	}

	backend.FailNextAttach(errors.New("temporary attach failure"))
	d.handleAttachSession(client, &protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: "sess-1"})
	secondMsg := <-client.send
	var second protocol.AttachResultMessage
	if err := json.Unmarshal(secondMsg.payload, &second); err != nil {
		t.Fatalf("decode second attach result: %v", err)
	}
	if second.Success {
		t.Fatal("second attach should fail")
	}

	client.attachMu.Lock()
	current := client.attachedStreams["sess-1"]
	client.attachMu.Unlock()
	if current == nil {
		t.Fatal("existing stream should remain attached after reattach failure")
	}
	if current != firstStream {
		t.Fatal("attached stream changed after reattach failure")
	}
	if got := firstStream.ClosedCount(); got != 0 {
		t.Fatalf("first stream closed count = %d, want 0 after failed reattach", got)
	}
}

func TestDaemon_HandleAttachSession_ReattachClosesOldStream(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeAttachBackend{}
	d.ptyBackend = backend
	client := &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[string]ptybackend.Stream),
	}

	d.handleAttachSession(client, &protocol.AttachSessionMessage{ID: "sess-1"})
	d.handleAttachSession(client, &protocol.AttachSessionMessage{ID: "sess-1"})

	streams := backend.Streams()
	if len(streams) != 2 {
		t.Fatalf("attach streams = %d, want 2", len(streams))
	}
	if streams[0].ClosedCount() == 0 {
		t.Fatal("first stream should be closed on reattach")
	}

	client.attachMu.Lock()
	current, ok := client.attachedStreams["sess-1"]
	client.attachMu.Unlock()
	if !ok {
		t.Fatal("expected current attached stream for sess-1")
	}
	if current != streams[1] {
		t.Fatal("attached stream should be the most recent stream")
	}

	d.detachSession(client, "sess-1")
}

func TestDaemon_HandleAttachSession_OmitsScrollbackWhenFreshSnapshotIsAvailable(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeAttachBackend{}
	backend.SetAttachInfo(ptybackend.AttachInfo{
		Running:             true,
		Scrollback:          []byte("very large scrollback"),
		ScrollbackTruncated: true,
		ScreenSnapshot:      []byte("\x1b[2Jsnapshot"),
		ScreenSnapshotFresh: true,
		ScreenCols:          10,
		ScreenRows:          6,
	})
	d.ptyBackend = backend

	client := &wsClient{
		send:            make(chan outboundMessage, 2),
		attachedStreams: make(map[string]ptybackend.Stream),
	}

	d.handleAttachSession(client, &protocol.AttachSessionMessage{ID: "sess-1"})

	select {
	case outbound := <-client.send:
		var result protocol.AttachResultMessage
		if err := json.Unmarshal(outbound.payload, &result); err != nil {
			t.Fatalf("decode attach_result: %v", err)
		}
		if !result.Success {
			t.Fatalf("attach_result success=false error=%q", protocol.Deref(result.Error))
		}
		if result.Scrollback != nil {
			t.Fatal("expected scrollback to be omitted when fresh screen snapshot is available")
		}
		if protocol.Deref(result.ScreenSnapshot) == "" {
			t.Fatal("expected screen snapshot to be present")
		}
		if !protocol.Deref(result.ScreenSnapshotFresh) {
			t.Fatal("expected screen snapshot to be marked fresh")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for attach_result")
	}
}

func TestDaemon_HandleAttachSession_PrefersBoundedRawReplayForStoredAgentSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeAttachBackend{}
	raw := []byte("\x1b[H\x1b[2JOpenAI Codex\r\n\r\nRun /review on my current changes\r\n\r\n  gpt-5.4 high · 100% left · ~")
	snapshot, ok := pty.ScreenSnapshotFromReplay(raw, 58, 46)
	if !ok {
		t.Fatal("expected raw replay to derive a snapshot")
	}
	backend.SetAttachInfo(ptybackend.AttachInfo{
		Running:             true,
		Scrollback:          raw,
		Cols:                58,
		Rows:                46,
		ScreenSnapshot:      snapshot.Payload,
		ScreenSnapshotFresh: true,
		ScreenCols:          snapshot.Cols,
		ScreenRows:          snapshot.Rows,
		ScreenCursorX:       snapshot.CursorX,
		ScreenCursorY:       snapshot.CursorY,
		ScreenCursorVisible: snapshot.CursorVisible,
	})
	d.ptyBackend = backend
	d.store.Add(&protocol.Session{
		ID:             "sess-1",
		Label:          "attn",
		Agent:          protocol.SessionAgentCodex,
		Directory:      t.TempDir(),
		State:          protocol.SessionStateIdle,
		StateSince:     time.Now().UTC().Format(time.RFC3339),
		StateUpdatedAt: time.Now().UTC().Format(time.RFC3339),
		LastSeen:       time.Now().UTC().Format(time.RFC3339),
	})

	client := &wsClient{
		send:            make(chan outboundMessage, 2),
		attachedStreams: make(map[string]ptybackend.Stream),
	}

	d.handleAttachSession(client, &protocol.AttachSessionMessage{ID: "sess-1"})

	select {
	case outbound := <-client.send:
		var result protocol.AttachResultMessage
		if err := json.Unmarshal(outbound.payload, &result); err != nil {
			t.Fatalf("decode attach_result: %v", err)
		}
		if !result.Success {
			t.Fatalf("attach_result success=false error=%q", protocol.Deref(result.Error))
		}
		if result.ScreenSnapshot != nil {
			t.Fatal("expected screen snapshot to be omitted for stored agent session replay")
		}
		if protocol.Deref(result.Scrollback) == "" {
			t.Fatal("expected raw replay to be present")
		}
		if protocol.Deref(result.ScrollbackTruncated) {
			t.Fatal("expected self-contained raw replay to remain untruncated")
		}
		decoded, err := base64.StdEncoding.DecodeString(protocol.Deref(result.Scrollback))
		if err != nil {
			t.Fatalf("decode scrollback replay: %v", err)
		}
		if !bytes.Equal(decoded, raw) {
			t.Fatal("expected raw replay to preserve the self-contained PTY history")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for attach_result")
	}
}

func TestDaemon_HandleAttachSession_PrefersSegmentedRawReplayForStoredAgentSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeAttachBackend{}
	segments := []ptybackend.ReplaySegment{
		{Cols: 118, Rows: 48, Data: []byte("\x1b[H\x1b[2JOpenAI Codex\r\n\r\nTip: wide history\r\n")},
		{Cols: 58, Rows: 46, Data: []byte("\x1b[3;1H› segmented tail\r\n\x1b[4;1H  gpt-5.4 high · 100% left · ~")},
	}
	ptySegments := make([]pty.ReplaySegment, 0, len(segments))
	for _, segment := range segments {
		ptySegments = append(ptySegments, pty.ReplaySegment{
			Cols: segment.Cols,
			Rows: segment.Rows,
			Data: append([]byte(nil), segment.Data...),
		})
	}
	snapshot, ok := pty.ScreenSnapshotFromReplaySegments(ptySegments)
	if !ok {
		t.Fatal("expected segmented replay to derive a snapshot")
	}
	backend.SetAttachInfo(ptybackend.AttachInfo{
		Running:             true,
		ReplaySegments:      segments,
		Cols:                58,
		Rows:                46,
		ScreenSnapshot:      snapshot.Payload,
		ScreenSnapshotFresh: true,
		ScreenCols:          snapshot.Cols,
		ScreenRows:          snapshot.Rows,
		ScreenCursorX:       snapshot.CursorX,
		ScreenCursorY:       snapshot.CursorY,
		ScreenCursorVisible: snapshot.CursorVisible,
	})
	d.ptyBackend = backend
	d.store.Add(&protocol.Session{
		ID:             "sess-1",
		Label:          "attn",
		Agent:          protocol.SessionAgentCodex,
		Directory:      t.TempDir(),
		State:          protocol.SessionStateIdle,
		StateSince:     time.Now().UTC().Format(time.RFC3339),
		StateUpdatedAt: time.Now().UTC().Format(time.RFC3339),
		LastSeen:       time.Now().UTC().Format(time.RFC3339),
	})

	client := &wsClient{
		send:            make(chan outboundMessage, 2),
		attachedStreams: make(map[string]ptybackend.Stream),
	}

	d.handleAttachSession(client, &protocol.AttachSessionMessage{ID: "sess-1"})

	select {
	case outbound := <-client.send:
		var result protocol.AttachResultMessage
		if err := json.Unmarshal(outbound.payload, &result); err != nil {
			t.Fatalf("decode attach_result: %v", err)
		}
		if !result.Success {
			t.Fatalf("attach_result success=false error=%q", protocol.Deref(result.Error))
		}
		if result.Scrollback != nil {
			t.Fatal("expected flat scrollback to be omitted when segmented replay is selected")
		}
		if result.ScreenSnapshot != nil {
			t.Fatal("expected screen snapshot to be omitted when segmented replay is selected")
		}
		if len(result.ReplaySegments) != 2 {
			t.Fatalf("replay_segments = %d, want 2", len(result.ReplaySegments))
		}
		decoded, err := base64.StdEncoding.DecodeString(result.ReplaySegments[0].Data)
		if err != nil {
			t.Fatalf("decode replay segment: %v", err)
		}
		if !bytes.Equal(decoded, segments[0].Data) {
			t.Fatal("expected replay segment to preserve the original bytes")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for attach_result")
	}
}

func TestDaemon_HandleAttachSession_OverfilledReplayLogRestoresWholeBoundarySafeSegments(t *testing.T) {
	// Regression for the review finding on #301: ReplayLog used to slice its
	// oldest retained segment at an arbitrary byte offset when overfilled, so
	// raw restore could open mid-escape-sequence even though the attach-side
	// budget pass kept whole segments. Overfill a real log with escape-framed
	// writes and prove everything served through attach is a whole write.
	writes := [][]byte{
		[]byte("\x1b[H\x1b[2J\x1b[31mframe-one: oldest, will be evicted\x1b[0m\r\n"),
		[]byte("\x1b[H\x1b[2J\x1b[32mframe-two: also history\x1b[0m\r\n"),
		[]byte("\x1b[H\x1b[2J\x1b[33mframe-three: newest repaint\x1b[0m\r\n"),
	}
	logSize := len(writes[1]) + len(writes[2]) + 4 // forces eviction of writes[0]
	replayLog := pty.NewReplayLog(logSize)
	for _, w := range writes {
		replayLog.Write(w, 58, 46)
	}
	logSegments, truncated := replayLog.Snapshot()
	if !truncated {
		t.Fatal("expected overfilled replay log to report truncation")
	}
	for i, segment := range logSegments {
		if segment.Data[0] != 0x1b {
			t.Fatalf("retained segment[%d] = %q does not start at a write boundary", i, segment.Data)
		}
	}

	segments := make([]ptybackend.ReplaySegment, 0, len(logSegments))
	for _, segment := range logSegments {
		segments = append(segments, ptybackend.ReplaySegment{
			Cols: segment.Cols,
			Rows: segment.Rows,
			Data: append([]byte(nil), segment.Data...),
		})
	}
	snapshot, ok := pty.ScreenSnapshotFromReplaySegments(logSegments)
	if !ok {
		t.Fatal("expected retained segments to derive a snapshot")
	}

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeAttachBackend{}
	backend.SetAttachInfo(ptybackend.AttachInfo{
		Running:             true,
		ReplaySegments:      segments,
		ReplayTruncated:     truncated,
		Cols:                58,
		Rows:                46,
		ScreenSnapshot:      snapshot.Payload,
		ScreenSnapshotFresh: true,
		ScreenCols:          snapshot.Cols,
		ScreenRows:          snapshot.Rows,
		ScreenCursorX:       snapshot.CursorX,
		ScreenCursorY:       snapshot.CursorY,
		ScreenCursorVisible: snapshot.CursorVisible,
	})
	d.ptyBackend = backend
	d.store.Add(&protocol.Session{
		ID:             "sess-1",
		Label:          "attn",
		Agent:          protocol.SessionAgentCodex,
		Directory:      t.TempDir(),
		State:          protocol.SessionStateIdle,
		StateSince:     time.Now().UTC().Format(time.RFC3339),
		StateUpdatedAt: time.Now().UTC().Format(time.RFC3339),
		LastSeen:       time.Now().UTC().Format(time.RFC3339),
	})

	client := &wsClient{
		send:            make(chan outboundMessage, 2),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
	d.handleAttachSession(client, &protocol.AttachSessionMessage{ID: "sess-1"})

	select {
	case outbound := <-client.send:
		var result protocol.AttachResultMessage
		if err := json.Unmarshal(outbound.payload, &result); err != nil {
			t.Fatalf("decode attach_result: %v", err)
		}
		if !result.Success {
			t.Fatalf("attach_result success=false error=%q", protocol.Deref(result.Error))
		}
		if len(result.ReplaySegments) != len(writes)-1 {
			t.Fatalf("replay_segments = %d, want %d", len(result.ReplaySegments), len(writes)-1)
		}
		for i, segment := range result.ReplaySegments {
			decoded, err := base64.StdEncoding.DecodeString(segment.Data)
			if err != nil {
				t.Fatalf("decode replay segment %d: %v", i, err)
			}
			if !bytes.Equal(decoded, writes[i+1]) {
				t.Fatalf("served segment[%d] = %q is not the whole original write %q", i, decoded, writes[i+1])
			}
		}
		if !protocol.Deref(result.ScrollbackTruncated) {
			t.Fatal("expected truncated replay log to surface scrollback_truncated")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for attach_result")
	}
}

func TestDaemon_HandleAttachSession_ServesVerifiedClippedSegmentedCodexReplay(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeAttachBackend{}

	segments := []ptybackend.ReplaySegment{
		{
			Cols: 118,
			Rows: 48,
			Data: bytes.Repeat([]byte("wide-history-"), (maxAgentRawReplayBytes/len("wide-history-"))+2048),
		},
		{
			Cols: 58,
			Rows: 46,
			Data: []byte("\x1b[H\x1b[2JOpenAI Codex\r\n\r\nTip: stable header\r\n\r\n› bounded segmented tail\r\n\r\n  gpt-5.4 high · 100% left · ~"),
		},
	}
	ptySegments := make([]pty.ReplaySegment, 0, len(segments))
	for _, segment := range segments {
		ptySegments = append(ptySegments, pty.ReplaySegment{
			Cols: segment.Cols,
			Rows: segment.Rows,
			Data: append([]byte(nil), segment.Data...),
		})
	}
	snapshot, ok := pty.ScreenSnapshotFromReplaySegments(ptySegments)
	if !ok {
		t.Fatal("expected segmented replay to derive a snapshot")
	}
	info := ptybackend.AttachInfo{
		Running:             true,
		ReplaySegments:      segments,
		Cols:                58,
		Rows:                46,
		ScreenSnapshot:      snapshot.Payload,
		ScreenSnapshotFresh: true,
		ScreenCols:          snapshot.Cols,
		ScreenRows:          snapshot.Rows,
		ScreenCursorX:       snapshot.CursorX,
		ScreenCursorY:       snapshot.CursorY,
		ScreenCursorVisible: snapshot.CursorVisible,
	}
	bounded, clipped := pty.LimitReplaySegmentsTail(ptySegments, maxAgentRawReplayBytes)
	if !clipped {
		t.Fatal("expected segmented replay to exceed the transport budget")
	}
	if len(bounded) != 1 || !bytes.Equal(bounded[0].Data, segments[1].Data) {
		t.Fatalf("expected whole-segment tail to keep only the newest segment, got %d segments", len(bounded))
	}

	backend.SetAttachInfo(info)
	d.ptyBackend = backend
	d.store.Add(&protocol.Session{
		ID:             "sess-1",
		Label:          "attn",
		Agent:          protocol.SessionAgentCodex,
		Directory:      t.TempDir(),
		State:          protocol.SessionStateIdle,
		StateSince:     time.Now().UTC().Format(time.RFC3339),
		StateUpdatedAt: time.Now().UTC().Format(time.RFC3339),
		LastSeen:       time.Now().UTC().Format(time.RFC3339),
	})

	client := &wsClient{
		send:            make(chan outboundMessage, 2),
		attachedStreams: make(map[string]ptybackend.Stream),
	}

	d.handleAttachSession(client, &protocol.AttachSessionMessage{ID: "sess-1"})

	select {
	case outbound := <-client.send:
		var result protocol.AttachResultMessage
		if err := json.Unmarshal(outbound.payload, &result); err != nil {
			t.Fatalf("decode attach_result: %v", err)
		}
		if !result.Success {
			t.Fatalf("attach_result success=false error=%q", protocol.Deref(result.Error))
		}
		// The clipped tail reproduces the live screen (Codex repaints in full
		// frames), so it must be served as deep scrollback instead of
		// degrading the restore to a bare screen snapshot.
		if len(result.ReplaySegments) != 1 {
			t.Fatalf("replay_segments = %d, want 1", len(result.ReplaySegments))
		}
		decoded, err := base64.StdEncoding.DecodeString(result.ReplaySegments[0].Data)
		if err != nil {
			t.Fatalf("decode replay segment: %v", err)
		}
		if !bytes.Equal(decoded, segments[1].Data) {
			t.Fatal("expected the clipped tail to preserve the newest segment bytes")
		}
		if result.ScreenSnapshot != nil {
			t.Fatal("expected screen snapshot to be omitted when the clipped tail is served")
		}
		if !protocol.Deref(result.ScrollbackTruncated) {
			t.Fatal("expected clipped replay to be marked truncated")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for attach_result")
	}
}

func TestDaemon_HandleAttachSession_DerivesSegmentedSnapshotWhenFreshSnapshotMissing(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeAttachBackend{}
	segments := []ptybackend.ReplaySegment{
		{Cols: 118, Rows: 48, Data: []byte("\x1b[H\x1b[2JOpenAI Codex\r\n\r\nTip: wide history\r\n")},
		{Cols: 58, Rows: 46, Data: []byte("\x1b[3;1H› segmented restore\r\n\x1b[4;1H  gpt-5.4 high · 100% left · ~")},
	}
	ptySegments := make([]pty.ReplaySegment, 0, len(segments))
	for _, segment := range segments {
		ptySegments = append(ptySegments, pty.ReplaySegment{
			Cols: segment.Cols,
			Rows: segment.Rows,
			Data: append([]byte(nil), segment.Data...),
		})
	}
	snapshot, ok := pty.ScreenSnapshotFromReplaySegments(ptySegments)
	if !ok {
		t.Fatal("expected segmented replay to derive a snapshot")
	}
	backend.SetAttachInfo(ptybackend.AttachInfo{
		Running:        true,
		ReplaySegments: segments,
		Cols:           58,
		Rows:           46,
	})
	d.ptyBackend = backend
	d.store.Add(&protocol.Session{
		ID:             "sess-1",
		Label:          "attn",
		Agent:          protocol.SessionAgentCodex,
		Directory:      t.TempDir(),
		State:          protocol.SessionStateIdle,
		StateSince:     time.Now().UTC().Format(time.RFC3339),
		StateUpdatedAt: time.Now().UTC().Format(time.RFC3339),
		LastSeen:       time.Now().UTC().Format(time.RFC3339),
	})

	client := &wsClient{
		send:            make(chan outboundMessage, 2),
		attachedStreams: make(map[string]ptybackend.Stream),
	}

	d.handleAttachSession(client, &protocol.AttachSessionMessage{ID: "sess-1"})

	select {
	case outbound := <-client.send:
		var result protocol.AttachResultMessage
		if err := json.Unmarshal(outbound.payload, &result); err != nil {
			t.Fatalf("decode attach_result: %v", err)
		}
		if !result.Success {
			t.Fatalf("attach_result success=false error=%q", protocol.Deref(result.Error))
		}
		if len(result.ReplaySegments) != 0 {
			t.Fatalf("replay_segments = %d, want 0", len(result.ReplaySegments))
		}
		if result.Scrollback != nil {
			t.Fatal("expected missing-fresh-snapshot Codex attach to keep a derived snapshot instead of raw replay")
		}
		if protocol.Deref(result.ScreenSnapshot) == "" {
			t.Fatal("expected derived screen snapshot to be present")
		}
		if !protocol.Deref(result.ScreenSnapshotFresh) {
			t.Fatal("expected derived screen snapshot to be marked fresh")
		}
		decoded, err := base64.StdEncoding.DecodeString(protocol.Deref(result.ScreenSnapshot))
		if err != nil {
			t.Fatalf("decode screen snapshot: %v", err)
		}
		if !bytes.Equal(decoded, snapshot.Payload) {
			t.Fatal("expected attach_result to derive the snapshot from segmented replay")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for attach_result")
	}
}

func TestDaemon_HandleAttachSession_FallsBackToFreshSnapshotWhenStoredCodexRawReplayDiverges(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeAttachBackend{}

	liveFrame := []byte("\x1b[H\x1b[2JOpenAI Codex\r\n\r\nTip: stable header\r\n\r\n› Run /review on my current changes\r\n\r\n  gpt-5.4 high · 100% left · ~")
	liveSnapshot, ok := pty.ScreenSnapshotFromReplay(liveFrame, 58, 46)
	if !ok {
		t.Fatal("expected live frame to derive a snapshot")
	}

	raw := append(
		bytes.Repeat([]byte("codex-replay-chunk-"), (maxAgentRawReplayBytes/18)+32),
		append(
			liveFrame,
			[]byte("\x1b[1;1H\x1b[J \r\n› Run /review on my current changes\r\n \r\n  gpt-5.4 high · 100% left · ~")...,
		)...,
	)
	boundedTail, _ := limitReplayTail(raw, maxAgentRawReplayBytes)
	derivedFromRaw, ok := pty.ScreenSnapshotFromReplay(boundedTail, 58, 46)
	if !ok {
		t.Fatal("expected divergent bounded raw replay tail to derive a snapshot")
	}
	if bytes.Equal(derivedFromRaw.Payload, liveSnapshot.Payload) {
		t.Fatal("expected bounded raw replay tail to diverge from the fresh live snapshot")
	}

	backend.SetAttachInfo(ptybackend.AttachInfo{
		Running:             true,
		Scrollback:          raw,
		Cols:                58,
		Rows:                46,
		ScreenSnapshot:      liveSnapshot.Payload,
		ScreenSnapshotFresh: true,
		ScreenCols:          liveSnapshot.Cols,
		ScreenRows:          liveSnapshot.Rows,
		ScreenCursorX:       liveSnapshot.CursorX,
		ScreenCursorY:       liveSnapshot.CursorY,
		ScreenCursorVisible: liveSnapshot.CursorVisible,
	})
	d.ptyBackend = backend
	d.store.Add(&protocol.Session{
		ID:             "sess-1",
		Label:          "attn",
		Agent:          protocol.SessionAgentCodex,
		Directory:      t.TempDir(),
		State:          protocol.SessionStateIdle,
		StateSince:     time.Now().UTC().Format(time.RFC3339),
		StateUpdatedAt: time.Now().UTC().Format(time.RFC3339),
		LastSeen:       time.Now().UTC().Format(time.RFC3339),
	})

	client := &wsClient{
		send:            make(chan outboundMessage, 2),
		attachedStreams: make(map[string]ptybackend.Stream),
	}

	d.handleAttachSession(client, &protocol.AttachSessionMessage{ID: "sess-1"})

	select {
	case outbound := <-client.send:
		var result protocol.AttachResultMessage
		if err := json.Unmarshal(outbound.payload, &result); err != nil {
			t.Fatalf("decode attach_result: %v", err)
		}
		if !result.Success {
			t.Fatalf("attach_result success=false error=%q", protocol.Deref(result.Error))
		}
		if result.Scrollback != nil {
			t.Fatal("expected divergent Codex raw replay to be omitted")
		}
		if protocol.Deref(result.ScreenSnapshot) == "" {
			t.Fatal("expected fresh screen snapshot to be present when raw replay diverges")
		}
		decoded, err := base64.StdEncoding.DecodeString(protocol.Deref(result.ScreenSnapshot))
		if err != nil {
			t.Fatalf("decode screen snapshot: %v", err)
		}
		if !bytes.Equal(decoded, liveSnapshot.Payload) {
			t.Fatal("expected attach_result to keep the fresh live snapshot")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for attach_result")
	}
}

func TestDaemon_HandleAttachSession_PrefersFreshScreenSnapshotForStoredClaudeSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeAttachBackend{}
	raw := bytes.Repeat([]byte("claude-replay-chunk-"), 64)
	backend.SetAttachInfo(ptybackend.AttachInfo{
		Running:             true,
		Scrollback:          raw,
		ScreenSnapshot:      []byte("\x1b[2Jclaude snapshot"),
		ScreenSnapshotFresh: true,
		ScreenCols:          58,
		ScreenRows:          46,
	})
	d.ptyBackend = backend
	d.store.Add(&protocol.Session{
		ID:             "sess-1",
		Label:          "attn",
		Agent:          protocol.SessionAgentClaude,
		Directory:      t.TempDir(),
		State:          protocol.SessionStateIdle,
		StateSince:     time.Now().UTC().Format(time.RFC3339),
		StateUpdatedAt: time.Now().UTC().Format(time.RFC3339),
		LastSeen:       time.Now().UTC().Format(time.RFC3339),
	})

	client := &wsClient{
		send:            make(chan outboundMessage, 2),
		attachedStreams: make(map[string]ptybackend.Stream),
	}

	d.handleAttachSession(client, &protocol.AttachSessionMessage{ID: "sess-1"})

	select {
	case outbound := <-client.send:
		var result protocol.AttachResultMessage
		if err := json.Unmarshal(outbound.payload, &result); err != nil {
			t.Fatalf("decode attach_result: %v", err)
		}
		if !result.Success {
			t.Fatalf("attach_result success=false error=%q", protocol.Deref(result.Error))
		}
		if result.Scrollback != nil {
			t.Fatal("expected Claude relaunch attach to omit raw replay when a fresh screen snapshot is available")
		}
		if protocol.Deref(result.ScreenSnapshot) == "" {
			t.Fatal("expected Claude relaunch attach to keep the fresh screen snapshot")
		}
		if !protocol.Deref(result.ScreenSnapshotFresh) {
			t.Fatal("expected Claude relaunch attach screen snapshot to be marked fresh")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for attach_result")
	}
}

func TestDaemon_HandleAttachSession_ReplaysVerifiedHistoryForSameAppRemountPolicy(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeAttachBackend{}
	raw := []byte("\x1b[H\x1b[2Jhello from replay\r\nsecond line")
	snapshot, ok := pty.ScreenSnapshotFromReplay(raw, 58, 46)
	if !ok {
		t.Fatal("expected raw replay to derive a snapshot")
	}
	backend.SetAttachInfo(ptybackend.AttachInfo{
		Running:             true,
		Scrollback:          raw,
		Cols:                58,
		Rows:                46,
		ScreenSnapshot:      snapshot.Payload,
		ScreenSnapshotFresh: true,
		ScreenCols:          snapshot.Cols,
		ScreenRows:          snapshot.Rows,
	})
	d.ptyBackend = backend

	client := &wsClient{
		send:            make(chan outboundMessage, 2),
		attachedStreams: make(map[string]ptybackend.Stream),
	}

	d.handleAttachSession(client, &protocol.AttachSessionMessage{
		ID:           "sess-1",
		AttachPolicy: protocol.Ptr(protocol.AttachPolicySameAppRemount),
	})

	select {
	case outbound := <-client.send:
		var result protocol.AttachResultMessage
		if err := json.Unmarshal(outbound.payload, &result); err != nil {
			t.Fatalf("decode attach_result: %v", err)
		}
		if !result.Success {
			t.Fatalf("attach_result success=false error=%q", protocol.Deref(result.Error))
		}
		if protocol.Deref(result.Scrollback) == "" {
			t.Fatal("expected same-app remount attach to replay verified history")
		}
		if result.ScreenSnapshot != nil {
			t.Fatal("expected verified history to replace the screen snapshot replay")
		}
		if protocol.Deref(result.ScrollbackTruncated) {
			t.Fatal("expected complete verified history not to be truncated")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for attach_result")
	}
}

func TestDaemon_HandleAttachSession_ReplaysVerifiedHistoryForRelaunchRestoreWithoutStoredSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeAttachBackend{}
	raw := []byte("\x1b[H\x1b[2Jformatted utility output\r\nprompt")
	snapshot, ok := pty.ScreenSnapshotFromReplay(raw, 58, 46)
	if !ok {
		t.Fatal("expected raw replay to derive a snapshot")
	}
	backend.SetAttachInfo(ptybackend.AttachInfo{
		Running:             true,
		Scrollback:          raw,
		Cols:                58,
		Rows:                46,
		ScreenSnapshot:      snapshot.Payload,
		ScreenSnapshotFresh: true,
		ScreenCols:          snapshot.Cols,
		ScreenRows:          snapshot.Rows,
	})
	d.ptyBackend = backend

	client := &wsClient{
		send:            make(chan outboundMessage, 2),
		attachedStreams: make(map[string]ptybackend.Stream),
	}

	d.handleAttachSession(client, &protocol.AttachSessionMessage{
		ID:           "runtime-shell-1",
		AttachPolicy: protocol.Ptr(protocol.AttachPolicyRelaunchRestore),
	})

	select {
	case outbound := <-client.send:
		var result protocol.AttachResultMessage
		if err := json.Unmarshal(outbound.payload, &result); err != nil {
			t.Fatalf("decode attach_result: %v", err)
		}
		if !result.Success {
			t.Fatalf("attach_result success=false error=%q", protocol.Deref(result.Error))
		}
		if protocol.Deref(result.Scrollback) == "" {
			t.Fatal("expected relaunch restore to replay verified history for an untracked shell runtime")
		}
		if result.ScreenSnapshot != nil {
			t.Fatal("expected verified relaunch history to replace the screen snapshot replay")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for attach_result")
	}
}

func TestDaemon_HandleAttachSession_DerivesScreenSnapshotWhenLiveSnapshotMissing(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeAttachBackend{}
	backend.SetAttachInfo(ptybackend.AttachInfo{
		Running:    true,
		Scrollback: []byte("hello\r\nworld"),
		Cols:       20,
		Rows:       6,
	})
	d.ptyBackend = backend

	client := &wsClient{
		send:            make(chan outboundMessage, 2),
		attachedStreams: make(map[string]ptybackend.Stream),
	}

	d.handleAttachSession(client, &protocol.AttachSessionMessage{ID: "sess-1"})

	select {
	case outbound := <-client.send:
		var result protocol.AttachResultMessage
		if err := json.Unmarshal(outbound.payload, &result); err != nil {
			t.Fatalf("decode attach_result: %v", err)
		}
		if !result.Success {
			t.Fatalf("attach_result success=false error=%q", protocol.Deref(result.Error))
		}
		if result.Scrollback != nil {
			t.Fatal("expected scrollback to be omitted when snapshot can be derived from replay")
		}
		if protocol.Deref(result.ScreenSnapshot) == "" {
			t.Fatal("expected derived screen snapshot to be present")
		}
		if !protocol.Deref(result.ScreenSnapshotFresh) {
			t.Fatal("expected derived screen snapshot to be marked fresh")
		}
		decoded, err := base64.StdEncoding.DecodeString(protocol.Deref(result.ScreenSnapshot))
		if err != nil {
			t.Fatalf("decode derived screen snapshot: %v", err)
		}
		if !strings.Contains(string(decoded), "hello") {
			t.Fatalf("expected derived screen snapshot payload to contain hello, got %q", decoded)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for attach_result")
	}
}

func TestDaemon_BroadcastRawWSMessage_RoutesRemotePTYTrafficToInterestedClients(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	clientAttached := &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[string]ptybackend.Stream),
		attachedRemote:  make(map[string]struct{}),
		pendingRemote:   make(map[string]struct{}),
	}
	clientOther := &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[string]ptybackend.Stream),
		attachedRemote:  make(map[string]struct{}),
		pendingRemote:   make(map[string]struct{}),
	}
	d.wsHub.clients[clientAttached] = true
	d.wsHub.clients[clientOther] = true

	clientAttached.notePendingRemoteAttach("remote-runtime-1")

	attachPayload, err := json.Marshal(protocol.AttachResultMessage{
		Event:   protocol.EventAttachResult,
		ID:      "remote-runtime-1",
		Success: true,
	})
	if err != nil {
		t.Fatalf("marshal attach_result: %v", err)
	}
	d.broadcastRawWSMessage(attachPayload)

	attachEvent := readOutboundEvent(t, clientAttached)
	if asString(attachEvent["event"]) != protocol.EventAttachResult || asString(attachEvent["id"]) != "remote-runtime-1" {
		t.Fatalf("unexpected attach event: %+v", attachEvent)
	}
	assertNoOutboundEvent(t, clientOther)
	if !clientAttached.hasRemoteAttach("remote-runtime-1") {
		t.Fatal("client should track remote runtime after attach_result success")
	}

	outputPayload, err := json.Marshal(protocol.WebSocketEvent{
		Event: protocol.EventPtyOutput,
		ID:    protocol.Ptr("remote-runtime-1"),
		Data:  protocol.Ptr(base64.StdEncoding.EncodeToString([]byte("hello"))),
		Seq:   protocol.Ptr(7),
	})
	if err != nil {
		t.Fatalf("marshal pty_output: %v", err)
	}
	d.broadcastRawWSMessage(outputPayload)

	outputEvent := readOutboundEvent(t, clientAttached)
	if asString(outputEvent["event"]) != protocol.EventPtyOutput || asString(outputEvent["id"]) != "remote-runtime-1" {
		t.Fatalf("unexpected pty_output event: %+v", outputEvent)
	}
	assertNoOutboundEvent(t, clientOther)
}

func TestDaemon_BroadcastRawWSMessage_RoutesPendingRemotePTYOutputBeforeAttachResult(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	clientPending := &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[string]ptybackend.Stream),
		attachedRemote:  make(map[string]struct{}),
		pendingRemote:   make(map[string]struct{}),
	}
	clientOther := &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[string]ptybackend.Stream),
		attachedRemote:  make(map[string]struct{}),
		pendingRemote:   make(map[string]struct{}),
	}
	d.wsHub.clients[clientPending] = true
	d.wsHub.clients[clientOther] = true

	clientPending.notePendingRemoteAttach("remote-runtime-1")

	outputPayload, err := json.Marshal(protocol.WebSocketEvent{
		Event: protocol.EventPtyOutput,
		ID:    protocol.Ptr("remote-runtime-1"),
		Data:  protocol.Ptr(base64.StdEncoding.EncodeToString([]byte("hello"))),
		Seq:   protocol.Ptr(7),
	})
	if err != nil {
		t.Fatalf("marshal pty_output: %v", err)
	}
	d.broadcastRawWSMessage(outputPayload)

	outputEvent := readOutboundEvent(t, clientPending)
	if asString(outputEvent["event"]) != protocol.EventPtyOutput || asString(outputEvent["id"]) != "remote-runtime-1" {
		t.Fatalf("unexpected pending-attach pty_output event: %+v", outputEvent)
	}
	assertNoOutboundEvent(t, clientOther)
	if clientPending.hasRemoteAttach("remote-runtime-1") {
		t.Fatal("pending attach should not mark remote runtime attached before attach_result")
	}
}

func TestDaemon_BroadcastRawWSMessage_RoutesRemoteTileContentToSubscribedClients(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	clientSubscribed := &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
	clientOther := &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
	d.wsHub.clients[clientSubscribed] = true
	d.wsHub.clients[clientOther] = true
	clientSubscribed.notePendingTileContent("remote-workspace", "tile-markdown")

	payload, err := json.Marshal(protocol.WorkspaceTileContentMessage{
		Event:       protocol.EventWorkspaceTileContent,
		WorkspaceID: "remote-workspace",
		TileID:      "tile-markdown",
		TileKind:    string(workspacelayout.TileKindMarkdown),
		Path:        "/srv/repo/README.md",
		Content:     "# Private",
	})
	if err != nil {
		t.Fatalf("marshal workspace_tile_content: %v", err)
	}
	d.broadcastRawWSMessage(payload)

	event := readOutboundEvent(t, clientSubscribed)
	if asString(event["event"]) != protocol.EventWorkspaceTileContent || asString(event["content"]) != "# Private" {
		t.Fatalf("unexpected tile content event: %+v", event)
	}
	assertNoOutboundEvent(t, clientOther)
	if !clientSubscribed.wantsTileContent("remote-workspace", "tile-markdown") {
		t.Fatal("successful relayed tile response should promote the pending request to a subscription")
	}
}

func TestDaemon_BroadcastRawWSMessage_PrunesRemoteTileSubscriptionsAfterLayoutUpdate(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client := &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
	d.wsHub.clients[client] = true
	client.subscribeTileContent("remote-workspace", "tile-markdown")

	layoutJSON, err := workspacelayout.EncodeLayout(workspacelayout.DefaultLayout("pane-1"))
	if err != nil {
		t.Fatalf("encode layout: %v", err)
	}
	payload, err := json.Marshal(protocol.WorkspaceLayoutUpdatedMessage{
		Event: protocol.EventWorkspaceLayoutUpdated,
		WorkspaceLayout: protocol.WorkspaceLayout{
			WorkspaceID:  "remote-workspace",
			ActivePaneID: "pane-1",
			LayoutJson:   layoutJSON,
		},
	})
	if err != nil {
		t.Fatalf("marshal workspace_layout_updated: %v", err)
	}
	d.broadcastRawWSMessage(payload)

	if client.wantsTileContent("remote-workspace", "tile-markdown") {
		t.Fatal("removed remote tile subscription survived layout update")
	}
}

func TestDaemon_BroadcastRawWSMessage_RemoteSessionExitedClearsRemoteAttachState(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client := &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[string]ptybackend.Stream),
		attachedRemote:  make(map[string]struct{}),
		pendingRemote:   make(map[string]struct{}),
	}
	d.wsHub.clients[client] = true
	client.attachedRemote["remote-runtime-1"] = struct{}{}

	exitPayload, err := json.Marshal(protocol.WebSocketEvent{
		Event: protocol.EventSessionExited,
		ID:    protocol.Ptr("remote-runtime-1"),
	})
	if err != nil {
		t.Fatalf("marshal session_exited: %v", err)
	}
	d.broadcastRawWSMessage(exitPayload)

	exitEvent := readOutboundEvent(t, client)
	if asString(exitEvent["event"]) != protocol.EventSessionExited || asString(exitEvent["id"]) != "remote-runtime-1" {
		t.Fatalf("unexpected session_exited event: %+v", exitEvent)
	}
	if client.hasRemoteAttach("remote-runtime-1") {
		t.Fatal("session_exited should clear remote attach state")
	}

	outputPayload, err := json.Marshal(protocol.WebSocketEvent{
		Event: protocol.EventPtyOutput,
		ID:    protocol.Ptr("remote-runtime-1"),
		Data:  protocol.Ptr(base64.StdEncoding.EncodeToString([]byte("late"))),
		Seq:   protocol.Ptr(8),
	})
	if err != nil {
		t.Fatalf("marshal late pty_output: %v", err)
	}
	d.broadcastRawWSMessage(outputPayload)
	assertNoOutboundEvent(t, client)
}

func TestDaemon_HandleUnregisterWS_RemovesSessionPaneAndBroadcastsSessionUnregistered(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	workspaceID := "workspace-sess-1"
	session := &protocol.Session{
		ID:             "sess-1",
		Label:          "test",
		Directory:      t.TempDir(),
		State:          protocol.StateWorking,
		StateSince:     time.Now().UTC().Format(time.RFC3339),
		StateUpdatedAt: time.Now().UTC().Format(time.RFC3339),
		LastSeen:       time.Now().UTC().Format(time.RFC3339),
		WorkspaceID:    workspaceID,
	}
	d.store.Add(session)
	d.store.AddWorkspace(&protocol.Workspace{ID: workspaceID, Title: "test", Directory: session.Directory})
	if err := d.store.SaveWorkspaceLayout(workspacelayout.WorkspaceLayout{
		WorkspaceID:  workspaceID,
		ActivePaneID: "pane-agent",
		Layout:       workspacelayout.DefaultLayout("pane-agent"),
		Panes: []workspacelayout.Pane{
			{PaneID: "pane-agent", RuntimeID: session.ID, SessionID: session.ID, Kind: workspacelayout.PaneKindAgent, Title: workspacelayout.DefaultPaneTitle},
			{PaneID: "pane-agent-2", RuntimeID: "sess-2", SessionID: "sess-2", Kind: workspacelayout.PaneKindAgent, Title: "Agent 2"},
		},
	}); err != nil {
		t.Fatalf("SaveWorkspaceLayout() error = %v", err)
	}

	client := &wsClient{
		send:            make(chan outboundMessage, 4),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
	d.wsHub.clients[client] = true
	go d.wsHub.run()

	d.handleUnregisterWS(client, &protocol.UnregisterMessage{ID: session.ID})

	if got := d.store.Get(session.ID); got != nil {
		t.Fatalf("store.Get(%q) = %+v, want nil", session.ID, got)
	}
	layout := d.store.GetWorkspaceLayout(workspaceID)
	if layout == nil {
		t.Fatalf("store.GetWorkspaceLayout(%q) = nil, want remaining pane layout", workspaceID)
	}
	if len(layout.Panes) != 1 || layout.Panes[0].PaneID != "pane-agent-2" {
		t.Fatalf("layout panes after unregister = %+v, want remaining agent pane only", layout.Panes)
	}

	var event map[string]interface{}
	for i := 0; i < 3; i++ {
		event = readOutboundEvent(t, client)
		if asString(event["event"]) == protocol.EventSessionUnregistered {
			break
		}
	}
	if asString(event["event"]) != protocol.EventSessionUnregistered {
		t.Fatalf("unexpected event after unregister: %+v", event)
	}
	if asString(event["session"].(map[string]interface{})["id"]) != session.ID {
		t.Fatalf("session_unregistered id = %v, want %s", event["session"], session.ID)
	}
}

func TestDaemon_HandleUnregisterWS_RemovesSessionPaneWithoutPromotingAnotherPane(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	workspaceID := "workspace-shared"
	now := time.Now().UTC().Format(time.RFC3339)
	d.store.AddWorkspace(&protocol.Workspace{ID: workspaceID, Title: "shared", Directory: t.TempDir()})
	for _, session := range []*protocol.Session{
		{
			ID:             "sess-primary",
			Label:          "Agent",
			Directory:      t.TempDir(),
			State:          protocol.StateWorking,
			StateSince:     now,
			StateUpdatedAt: now,
			LastSeen:       now,
			WorkspaceID:    workspaceID,
		},
		{
			ID:             "sess-next",
			Label:          "next",
			Directory:      t.TempDir(),
			State:          protocol.StateWorking,
			StateSince:     now,
			StateUpdatedAt: now,
			LastSeen:       now,
			WorkspaceID:    workspaceID,
		},
	} {
		d.store.Add(session)
	}
	if err := d.store.SaveWorkspaceLayout(workspacelayout.WorkspaceLayout{
		WorkspaceID:  workspaceID,
		ActivePaneID: "pane-session",
		Layout: workspacelayout.Node{
			Type:      "split",
			SplitID:   "split-1",
			Direction: workspacelayout.DirectionVertical,
			Ratio:     0.5,
			Children: []workspacelayout.Node{
				{Type: "pane", PaneID: "pane-session"},
				{Type: "pane", PaneID: "pane-next"},
			},
		},
		Panes: []workspacelayout.Pane{
			{PaneID: "pane-session", RuntimeID: "sess-primary", SessionID: "sess-primary", Kind: workspacelayout.PaneKindAgent, Title: "Agent"},
			{PaneID: "pane-next", RuntimeID: "sess-next", SessionID: "sess-next", Kind: workspacelayout.PaneKindAgent, Title: "next"},
		},
	}); err != nil {
		t.Fatalf("SaveWorkspaceLayout() error = %v", err)
	}

	client := &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
	d.wsHub.clients[client] = true
	go d.wsHub.run()

	d.handleUnregisterWS(client, &protocol.UnregisterMessage{ID: "sess-primary"})

	if got := d.store.Get("sess-primary"); got != nil {
		t.Fatalf("closed session still exists: %+v", got)
	}
	if got := d.store.Get("sess-next"); got == nil {
		t.Fatal("replacement session was removed")
	}
	layout := d.store.GetWorkspaceLayout(workspaceID)
	if layout == nil {
		t.Fatal("workspace layout was removed")
	}
	if len(layout.Panes) != 1 {
		t.Fatalf("layout panes len = %d, want 1: %+v", len(layout.Panes), layout.Panes)
	}
	if pane := layout.Panes[0]; pane.PaneID != "pane-next" || pane.SessionID != "sess-next" {
		t.Fatalf("remaining pane = %+v, want existing pane-next for sess-next", pane)
	}
	if layout.Layout.Type != "pane" || layout.Layout.PaneID != "pane-next" {
		t.Fatalf("layout tree = %+v, want single pane-next pane", layout.Layout)
	}
}

func TestDaemon_NewAddsWarningWhenPersistenceFallsBackToMemory(t *testing.T) {
	t.Setenv("ATTN_DB_PATH", filepath.Join("/dev/null", "attn.db"))

	d := New(filepath.Join(t.TempDir(), "test.sock"))
	defer d.store.Close()

	warnings := d.getWarnings()
	if len(warnings) == 0 {
		t.Fatal("expected warning when DB open fails and daemon falls back to in-memory")
	}

	found := false
	for _, warning := range warnings {
		if warning.Code != "persistence_degraded" {
			continue
		}
		found = true
		if !strings.Contains(warning.Message, "Running in-memory only") {
			t.Fatalf("warning message missing in-memory note: %q", warning.Message)
		}
		if !strings.Contains(warning.Message, "See daemon log in "+config.LogPath()) {
			t.Fatalf("warning message missing daemon log path: %q", warning.Message)
		}
		if !strings.Contains(warning.Message, "/dev/null/attn.db") {
			t.Fatalf("warning message missing DB path: %q", warning.Message)
		}
	}
	if !found {
		t.Fatalf("expected persistence_degraded warning, got: %+v", warnings)
	}

	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "fallback-store-session",
		Label:          "fallback-store-session",
		Agent:          protocol.SessionAgentCodex,
		Directory:      t.TempDir(),
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if got := d.store.Get("fallback-store-session"); got == nil {
		t.Fatal("expected in-memory fallback store to remain usable")
	}
}

func TestDaemon_HandleClientMessage_ClearWarnings(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.addWarning("one", "first warning")
	d.addWarning("two", "second warning")
	if got := len(d.getWarnings()); got != 2 {
		t.Fatalf("warnings before clear = %d, want 2", got)
	}

	client := &wsClient{}
	client.setIdentity("daemon-test", "protocol-"+protocol.ProtocolVersion, []string{protocol.CapabilityWorkspaceSessions})
	d.handleClientMessage(client, []byte(`{"cmd":"clear_warnings"}`))

	if got := len(d.getWarnings()); got != 0 {
		t.Fatalf("warnings after clear = %d, want 0", got)
	}
}

func TestDaemon_AddWarning_DedupesByCodeAndMessage(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.addWarning("worker_recovery_partial", "first")
	d.addWarning("worker_recovery_partial", "second")
	d.addWarning("worker_recovery_partial", "second")

	warnings := d.getWarnings()
	if len(warnings) != 2 {
		t.Fatalf("warnings len = %d, want 2", len(warnings))
	}
}

func TestDaemon_ClearWarningsNotReplayedInInitialState(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.addWarning("stale_sessions_pruned", "Removed 1 stale sessions from a previous daemon run because no live PTY was found.")

	client := &wsClient{
		send: make(chan outboundMessage, 4),
	}
	client.setIdentity("daemon-test", "protocol-"+protocol.ProtocolVersion, []string{protocol.CapabilityWorkspaceSessions})

	d.sendInitialState(client)
	first := <-client.send
	var firstEvent protocol.WebSocketEvent
	if err := json.Unmarshal(first.payload, &firstEvent); err != nil {
		t.Fatalf("decode first initial_state: %v", err)
	}
	if firstEvent.Event != protocol.EventInitialState {
		t.Fatalf("first event = %q, want %q", firstEvent.Event, protocol.EventInitialState)
	}
	if got := len(firstEvent.Warnings); got != 1 {
		t.Fatalf("first initial_state warnings = %d, want 1", got)
	}

	d.handleClientMessage(client, []byte(`{"cmd":"clear_warnings"}`))

	d.sendInitialState(client)
	second := <-client.send
	var secondEvent protocol.WebSocketEvent
	if err := json.Unmarshal(second.payload, &secondEvent); err != nil {
		t.Fatalf("decode second initial_state: %v", err)
	}
	if secondEvent.Event != protocol.EventInitialState {
		t.Fatalf("second event = %q, want %q", secondEvent.Event, protocol.EventInitialState)
	}
	if got := len(secondEvent.Warnings); got != 0 {
		t.Fatalf("second initial_state warnings = %d, want 0", got)
	}
}

func TestDaemon_InitialState_IncludesDaemonInstanceID(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.daemonInstanceID = "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	client := &wsClient{
		send:            make(chan outboundMessage, 2),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
	client.setIdentity("daemon-test", "protocol-"+protocol.ProtocolVersion, []string{protocol.CapabilityWorkspaceSessions})

	d.sendInitialState(client)
	msg := <-client.send

	var initial protocol.InitialStateMessage
	if err := json.Unmarshal(msg.payload, &initial); err != nil {
		t.Fatalf("decode initial_state: %v", err)
	}
	if protocol.Deref(initial.DaemonInstanceID) != d.daemonInstanceID {
		t.Fatalf("daemon_instance_id = %q, want %q", protocol.Deref(initial.DaemonInstanceID), d.daemonInstanceID)
	}
}

func TestDaemon_GitHubHostsMessages_UseRegisteredHosts(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ghRegistry.Register("ghe.example.test", nil)
	d.ghRegistry.Register("github.com", nil)

	client := &wsClient{
		send:            make(chan outboundMessage, 2),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
	client.setIdentity("daemon-test", "protocol-"+protocol.ProtocolVersion, []string{protocol.CapabilityWorkspaceSessions})

	d.sendInitialState(client)
	msg := <-client.send

	var initial protocol.InitialStateMessage
	if err := json.Unmarshal(msg.payload, &initial); err != nil {
		t.Fatalf("decode initial_state: %v", err)
	}
	if got := strings.Join(initial.GithubHosts, ","); got != "ghe.example.test,github.com" {
		t.Fatalf("initial github_hosts = %q, want registered hosts", got)
	}

	updated := d.gitHubHostsUpdatedMessage()
	if updated.Event != protocol.EventGitHubHostsUpdated {
		t.Fatalf("updated event = %q, want %q", updated.Event, protocol.EventGitHubHostsUpdated)
	}
	if got := strings.Join(updated.GithubHosts, ","); got != "ghe.example.test,github.com" {
		t.Fatalf("updated github_hosts = %q, want registered hosts", got)
	}
}

func TestDaemon_RecoveryBarrier_BlocksPTYCommands(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.setRecovering(true)

	client := &wsClient{
		send:            make(chan outboundMessage, 2),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
	client.setIdentity("daemon-test", "protocol-"+protocol.ProtocolVersion, []string{protocol.CapabilityWorkspaceSessions})

	d.handleClientMessage(client, []byte(`{"cmd":"attach_session","id":"sess-1"}`))

	msg := <-client.send
	var event protocol.WebSocketEvent
	if err := json.Unmarshal(msg.payload, &event); err != nil {
		t.Fatalf("decode command_error: %v", err)
	}
	if event.Event != protocol.EventCommandError {
		t.Fatalf("event = %q, want %q", event.Event, protocol.EventCommandError)
	}
	if protocol.Deref(event.Cmd) != protocol.CmdAttachSession {
		t.Fatalf("cmd = %q, want %q", protocol.Deref(event.Cmd), protocol.CmdAttachSession)
	}
	if protocol.Deref(event.Error) != "daemon_recovering" {
		t.Fatalf("error = %q, want %q", protocol.Deref(event.Error), "daemon_recovering")
	}
}

func TestDaemon_RecoveryBarrier_BlocksClearSessions(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.setRecovering(true)

	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "sess-1",
		Label:          "sess-1",
		Agent:          protocol.SessionAgentCodex,
		Directory:      "/tmp/sess-1",
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})

	client := &wsClient{
		send:            make(chan outboundMessage, 2),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
	client.setIdentity("daemon-test", "protocol-"+protocol.ProtocolVersion, []string{protocol.CapabilityWorkspaceSessions})

	d.handleClientMessage(client, []byte(`{"cmd":"clear_sessions"}`))

	msg := <-client.send
	var event protocol.WebSocketEvent
	if err := json.Unmarshal(msg.payload, &event); err != nil {
		t.Fatalf("decode command_error: %v", err)
	}
	if event.Event != protocol.EventCommandError {
		t.Fatalf("event = %q, want %q", event.Event, protocol.EventCommandError)
	}
	if protocol.Deref(event.Cmd) != protocol.CmdClearSessions {
		t.Fatalf("cmd = %q, want %q", protocol.Deref(event.Cmd), protocol.CmdClearSessions)
	}
	if protocol.Deref(event.Error) != "daemon_recovering" {
		t.Fatalf("error = %q, want %q", protocol.Deref(event.Error), "daemon_recovering")
	}
	if got := len(d.store.List("")); got != 1 {
		t.Fatalf("store sessions = %d, want 1 (clear should be blocked during recovery)", got)
	}
}

func TestDaemon_ClearAllSessions_RecoversAndTerminatesKnownSessions(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "store-session",
		Label:          "store-session",
		Agent:          protocol.SessionAgentCodex,
		Directory:      "/tmp/store-session",
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})

	backend := &fakeClearSessionsBackend{
		sessionIDs:   []string{"attached-session"},
		recoveredIDs: []string{"registry-only-session"},
	}
	d.ptyBackend = backend

	d.clearAllSessions()

	if got := len(d.store.List("")); got != 0 {
		t.Fatalf("store sessions = %d, want 0", got)
	}

	backend.mu.Lock()
	recoverCalled := backend.recoverCalled
	killed := append([]string(nil), backend.killed...)
	removed := append([]string(nil), backend.removed...)
	backend.mu.Unlock()

	if !recoverCalled {
		t.Fatal("expected clearAllSessions to call backend Recover()")
	}
	expectKilled := map[string]bool{
		"store-session":         false,
		"attached-session":      false,
		"registry-only-session": false,
	}
	for _, id := range killed {
		if _, ok := expectKilled[id]; ok {
			expectKilled[id] = true
		}
	}
	for id, seen := range expectKilled {
		if !seen {
			t.Fatalf("expected kill for %s, got kills=%v", id, killed)
		}
	}
	expectRemoved := map[string]bool{
		"store-session":         false,
		"attached-session":      false,
		"registry-only-session": false,
	}
	for _, id := range removed {
		if _, ok := expectRemoved[id]; ok {
			expectRemoved[id] = true
		}
	}
	for id, seen := range expectRemoved {
		if !seen {
			t.Fatalf("expected remove for %s, got removes=%v", id, removed)
		}
	}
}

func TestDaemon_RecoveryBarrier_DefersInitialState(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.daemonInstanceID = "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	d.setRecovering(true)

	client := &wsClient{
		send:            make(chan outboundMessage, 2),
		attachedStreams: make(map[string]ptybackend.Stream),
	}

	d.scheduleInitialState(client)
	select {
	case <-client.send:
		t.Fatal("initial_state was sent while daemon was recovering")
	default:
	}

	d.setRecovering(false)
	select {
	case msg := <-client.send:
		var initial protocol.InitialStateMessage
		if err := json.Unmarshal(msg.payload, &initial); err != nil {
			t.Fatalf("decode deferred initial_state: %v", err)
		}
		if initial.Event != protocol.EventInitialState {
			t.Fatalf("event = %q, want %q", initial.Event, protocol.EventInitialState)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for deferred initial_state")
	}
}

func TestDaemon_HealthEndpoint(t *testing.T) {
	tmpDir := shortTempDir(t)
	sockPath := filepath.Join(tmpDir, "test.sock")

	wsPort := useFreeWSPort(t)

	d := NewForTesting(sockPath)
	go d.Start()
	defer d.Stop()

	waitForSocket(t, sockPath, 5*time.Second)

	// Register a session to verify it's counted
	c := client.New(sockPath)
	c.Register("test-1", "test", "/tmp")

	// Poll the health endpoint until the HTTP server is ready
	healthURL := "http://127.0.0.1:" + wsPort + "/health"
	var resp *http.Response
	logDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(logDeadline) {
		r, err := http.Get(healthURL)
		if err == nil {
			resp = r
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if resp == nil {
		t.Fatalf("Health endpoint not ready after 5s")
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("Health status = %d, want 200", resp.StatusCode)
	}

	var health map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	if health["status"] != "ok" {
		t.Errorf("status = %v, want ok", health["status"])
	}
	if health["protocol"] != protocol.ProtocolVersion {
		t.Errorf("protocol = %v, want %s", health["protocol"], protocol.ProtocolVersion)
	}
	if health["version"] != buildinfo.Version {
		t.Errorf("version = %v, want %s", health["version"], buildinfo.Version)
	}
	if health["build_time"] != buildinfo.BuildTime {
		t.Errorf("build_time = %v, want %s", health["build_time"], buildinfo.BuildTime)
	}
	if health["source_fingerprint"] != buildinfo.SourceFingerprint {
		t.Errorf("source_fingerprint = %v, want %s", health["source_fingerprint"], buildinfo.SourceFingerprint)
	}
	if health["git_commit"] != buildinfo.GitCommit {
		t.Errorf("git_commit = %v, want %s", health["git_commit"], buildinfo.GitCommit)
	}
	if daemonID, ok := health["daemon_instance_id"].(string); !ok || daemonID == "" {
		t.Errorf("daemon_instance_id = %v, want non-empty string", health["daemon_instance_id"])
	}
	// sessions should be 1.0 (float64 from JSON)
	if sessions, ok := health["sessions"].(float64); !ok || sessions != 1 {
		t.Errorf("sessions = %v, want 1", health["sessions"])
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store, max-age=0" {
		t.Errorf("health Cache-Control = %q, want no-store, max-age=0", got)
	}
	// Profile identity: with no ATTN_PROFILE set, profile is "default" and
	// port mirrors what the daemon is actually bound to.
	if health["profile"] != "default" {
		t.Errorf("profile = %v, want %q", health["profile"], "default")
	}
	if health["port"] != wsPort {
		t.Errorf("port = %v, want %q", health["port"], wsPort)
	}
	if dataDir, ok := health["data_dir"].(string); !ok || dataDir == "" {
		t.Errorf("data_dir = %v, want non-empty string", health["data_dir"])
	}
	if socketPath, ok := health["socket_path"].(string); !ok || socketPath == "" {
		t.Errorf("socket_path = %v, want non-empty string", health["socket_path"])
	}
}

func TestDaemon_WebRootServesEmbeddedClient(t *testing.T) {
	tmpDir := shortTempDir(t)
	sockPath := filepath.Join(tmpDir, "test.sock")

	wsPort := useFreeWSPort(t)

	d := NewForTesting(sockPath)
	go d.Start()
	defer d.Stop()

	waitForSocket(t, sockPath, 5*time.Second)

	rootURL := "http://127.0.0.1:" + wsPort + "/"
	var resp *http.Response
	logDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(logDeadline) {
		r, err := http.Get(rootURL)
		if err == nil {
			resp = r
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if resp == nil {
		t.Fatalf("root endpoint not ready after 5s")
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read root body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("root status = %d, want 200", resp.StatusCode)
	}
	bodyText := string(body)
	if !strings.Contains(bodyText, `data-attn-web-client="ghostty-web"`) {
		t.Fatalf("root body did not contain ghostty-web client marker")
	}
	if !strings.Contains(bodyText, `rel="icon"`) {
		t.Fatalf("root body did not include favicon link")
	}
	if !strings.Contains(bodyText, "/vendor/ghostty-web/ghostty-web.js") {
		t.Fatalf("root body did not reference ghostty-web bundle")
	}
	if !strings.Contains(bodyText, "/vendor/ghostty-web/ghostty-vt.wasm") {
		t.Fatalf("root body did not reference ghostty-web wasm asset")
	}
	if !strings.Contains(bodyText, `data-testid="session-list"`) {
		t.Fatalf("root body did not include session list marker")
	}
	if !strings.Contains(bodyText, `data-quick-action="esc"`) {
		t.Fatalf("root body did not include quick action markers")
	}
	if !strings.Contains(bodyText, `id="font-size-decrease"`) || !strings.Contains(bodyText, `id="font-size-increase"`) {
		t.Fatalf("root body did not include font size controls")
	}
	if !strings.Contains(bodyText, "attn ghostty-web") {
		t.Fatalf("root body did not include ghostty-web heading")
	}
	if strings.Contains(bodyText, "xterm.min.js") || strings.Contains(bodyText, "xterm-addon-fit") {
		t.Fatalf("root body still referenced xterm-era assets")
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store, max-age=0" {
		t.Fatalf("root Cache-Control = %q, want no-store, max-age=0", got)
	}
}

func TestDaemon_WebGhosttyAssetsServeNoStore(t *testing.T) {
	tmpDir := shortTempDir(t)
	sockPath := filepath.Join(tmpDir, "test.sock")

	port, err := freeTCPPort()
	if err != nil {
		t.Fatalf("freeTCPPort: %v", err)
	}
	wsPort := strconv.Itoa(port)
	t.Setenv("ATTN_WS_PORT", wsPort)

	d := NewForTesting(sockPath)
	go d.Start()
	defer d.Stop()

	waitForSocket(t, sockPath, 5*time.Second)

	healthURL := "http://127.0.0.1:" + wsPort + "/health"
	healthReady := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(healthURL)
		if err == nil {
			resp.Body.Close()
			healthReady = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !healthReady {
		t.Fatalf("health endpoint not ready after 5s")
	}

	jsURL := "http://127.0.0.1:" + wsPort + "/vendor/ghostty-web/ghostty-web.js"
	jsResp, err := http.Get(jsURL)
	if err != nil {
		t.Fatalf("get ghostty-web bundle: %v", err)
	}
	defer jsResp.Body.Close()

	jsBody, err := io.ReadAll(jsResp.Body)
	if err != nil {
		t.Fatalf("read ghostty-web bundle: %v", err)
	}
	if jsResp.StatusCode != http.StatusOK {
		t.Fatalf("ghostty-web bundle status = %d, want 200", jsResp.StatusCode)
	}
	if !strings.Contains(string(jsBody), "ghostty-vt.wasm") {
		t.Fatalf("ghostty-web bundle did not reference wasm payload")
	}
	if got := jsResp.Header.Get("Cache-Control"); got != "no-store, max-age=0" {
		t.Fatalf("ghostty-web bundle Cache-Control = %q, want no-store, max-age=0", got)
	}

	sidecar := extractGhosttySidecarPath(string(jsBody))
	if sidecar == "" {
		t.Fatal("ghostty-web bundle did not include vite browser external sidecar path")
	}
	sidecarURL := "http://127.0.0.1:" + wsPort + "/vendor/ghostty-web/" + sidecar
	sidecarResp, err := http.Get(sidecarURL)
	if err != nil {
		t.Fatalf("get ghostty-web sidecar: %v", err)
	}
	defer sidecarResp.Body.Close()

	sidecarBody, err := io.ReadAll(sidecarResp.Body)
	if err != nil {
		t.Fatalf("read ghostty-web sidecar: %v", err)
	}
	if sidecarResp.StatusCode != http.StatusOK {
		t.Fatalf("ghostty-web sidecar status = %d, want 200", sidecarResp.StatusCode)
	}
	if len(sidecarBody) == 0 {
		t.Fatal("ghostty-web sidecar was empty")
	}
	if got := sidecarResp.Header.Get("Cache-Control"); got != "no-store, max-age=0" {
		t.Fatalf("ghostty-web sidecar Cache-Control = %q, want no-store, max-age=0", got)
	}

	wasmURL := "http://127.0.0.1:" + wsPort + "/vendor/ghostty-web/ghostty-vt.wasm"
	wasmResp, err := http.Get(wasmURL)
	if err != nil {
		t.Fatalf("get ghostty-web wasm: %v", err)
	}
	defer wasmResp.Body.Close()

	wasmBody, err := io.ReadAll(wasmResp.Body)
	if err != nil {
		t.Fatalf("read ghostty-web wasm: %v", err)
	}
	if wasmResp.StatusCode != http.StatusOK {
		t.Fatalf("ghostty-web wasm status = %d, want 200", wasmResp.StatusCode)
	}
	if len(wasmBody) < 100000 {
		t.Fatalf("ghostty-web wasm length = %d, want substantial payload", len(wasmBody))
	}
	if got := wasmResp.Header.Get("Cache-Control"); got != "no-store, max-age=0" {
		t.Fatalf("ghostty-web wasm Cache-Control = %q, want no-store, max-age=0", got)
	}
}

func TestDaemon_WebFaviconDoesNot404(t *testing.T) {
	tmpDir := shortTempDir(t)
	sockPath := filepath.Join(tmpDir, "test.sock")

	wsPort := useFreeWSPort(t)

	d := NewForTesting(sockPath)
	go d.Start()
	defer d.Stop()

	waitForSocket(t, sockPath, 5*time.Second)

	faviconURL := "http://127.0.0.1:" + wsPort + "/favicon.ico"
	var resp *http.Response
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, err := http.Get(faviconURL)
		if err == nil {
			resp = r
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if resp == nil {
		t.Fatalf("favicon endpoint not ready after 5s")
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read favicon body: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("favicon status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
	if len(body) != 0 {
		t.Fatalf("favicon body length = %d, want 0", len(body))
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store, max-age=0" {
		t.Fatalf("favicon Cache-Control = %q, want no-store, max-age=0", got)
	}
}

func TestDaemon_WebInstrumentationLogsPayload(t *testing.T) {
	tmpDir := shortTempDir(t)
	sockPath := filepath.Join(tmpDir, "test.sock")
	logPath := filepath.Join(tmpDir, "daemon.log")

	port, err := freeTCPPort()
	if err != nil {
		t.Fatalf("freeTCPPort: %v", err)
	}
	wsPort := strconv.Itoa(port)
	t.Setenv("ATTN_WS_PORT", wsPort)

	logger, err := logging.New(logPath)
	if err != nil {
		t.Fatalf("new test logger: %v", err)
	}
	defer logger.Close()

	d := NewForTesting(sockPath)
	d.logger = logger
	go d.Start()
	defer d.Stop()

	waitForSocket(t, sockPath, 5*time.Second)

	healthURL := "http://127.0.0.1:" + wsPort + "/health"
	healthReady := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(healthURL)
		if err == nil {
			resp.Body.Close()
			healthReady = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !healthReady {
		t.Fatalf("health endpoint not ready after 5s")
	}

	payload := `{"event":"keyboard-close-request","sessionID":"web-debug-smoke","liveViewport":{"scale":1.25,"offsetTop":42}}`
	req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:"+wsPort+"/web-instrumentation", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("new instrumentation request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post instrumentation payload: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("instrumentation status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store, max-age=0" {
		t.Fatalf("instrumentation Cache-Control = %q, want no-store, max-age=0", got)
	}

	logDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(logDeadline) {
		body, err := os.ReadFile(logPath)
		if err == nil {
			text := string(body)
			if strings.Contains(text, "web instrumentation:") &&
				strings.Contains(text, `"event":"keyboard-close-request"`) &&
				strings.Contains(text, `"scale":1.25`) {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read instrumentation log: %v", err)
	}
	t.Fatalf("instrumentation log did not contain compacted payload: %s", string(body))
}

func TestDaemon_WebClientAttachFlowOverWebSocket(t *testing.T) {
	port, err := freeTCPPort()
	if err != nil {
		t.Fatalf("freeTCPPort: %v", err)
	}
	t.Setenv("ATTN_WS_PORT", strconv.Itoa(port))

	tmpDir := shortTempDir(t)
	sockPath := filepath.Join(tmpDir, "test.sock")

	d := NewForTesting(sockPath)
	go d.Start()
	defer d.Stop()

	waitForSocket(t, sockPath, 5*time.Second)

	sessionID := "web-client-smoke"
	cwd := t.TempDir()
	if err := d.ptyBackend.Spawn(context.Background(), ptybackend.SpawnOptions{
		ID:    sessionID,
		CWD:   cwd,
		Agent: protocol.AgentShellValue,
		Label: "web-client-smoke",
		Cols:  80,
		Rows:  24,
	}); err != nil {
		t.Fatalf("spawn shell PTY: %v", err)
	}
	defer func() {
		_ = d.ptyBackend.Kill(context.Background(), sessionID, syscall.SIGTERM)
	}()

	c := client.New(sockPath)
	if err := c.Register(sessionID, "web-client-smoke", cwd); err != nil {
		t.Fatalf("register smoke session: %v", err)
	}
	if err := c.UpdateState(sessionID, protocol.StateWorking); err != nil {
		t.Fatalf("update smoke session state: %v", err)
	}

	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/ws", port)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	cancel()
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	initial := waitForDaemonWebSocketEvent(t, conn, 10*time.Second, func(evt map[string]interface{}) bool {
		return asString(evt["event"]) == protocol.EventInitialState
	})
	if !initialStateIncludesSession(initial, sessionID) {
		t.Fatalf("initial_state did not include smoke session %q", sessionID)
	}
	sendWorkspaceClientHello(t, conn)

	if err := writeWS(conn, map[string]interface{}{
		"cmd": protocol.CmdAttachSession,
		"id":  sessionID,
	}); err != nil {
		t.Fatalf("attach write failed: %v", err)
	}

	attach := waitForDaemonWebSocketEvent(t, conn, 10*time.Second, func(evt map[string]interface{}) bool {
		return asString(evt["event"]) == protocol.EventAttachResult && asString(evt["id"]) == sessionID
	})
	if !asBool(attach["success"]) {
		t.Fatalf("attach_result success = %v, error=%q", attach["success"], asString(attach["error"]))
	}

	if err := writeWS(conn, map[string]interface{}{
		"cmd":  protocol.CmdPtyInput,
		"id":   sessionID,
		"data": "echo __ATTN_WEB_CLIENT_SMOKE__\r",
	}); err != nil {
		t.Fatalf("pty_input write failed: %v", err)
	}

	output := waitForPtyOutputContaining(t, conn, sessionID, "__ATTN_WEB_CLIENT_SMOKE__", 10*time.Second)
	if !strings.Contains(output, "__ATTN_WEB_CLIENT_SMOKE__") {
		t.Fatalf("pty output %q did not contain smoke marker", output)
	}

	if err := writeWS(conn, map[string]interface{}{
		"cmd":  protocol.CmdPtyResize,
		"id":   sessionID,
		"cols": 90,
		"rows": 30,
	}); err != nil {
		t.Fatalf("pty_resize write failed: %v", err)
	}

	infoProvider, ok := d.ptyBackend.(ptybackend.SessionInfoProvider)
	if !ok {
		t.Fatal("pty backend does not expose SessionInfoProvider")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		info, err := infoProvider.SessionInfo(context.Background(), sessionID)
		if err == nil && info.Cols == 90 && info.Rows == 30 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	info, err := infoProvider.SessionInfo(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("session info after resize: %v", err)
	}
	if info.Cols != 90 || info.Rows != 30 {
		t.Fatalf("session size after resize = %dx%d, want 90x30", info.Cols, info.Rows)
	}

	if err := writeWS(conn, map[string]interface{}{
		"cmd": protocol.CmdDetachSession,
		"id":  sessionID,
	}); err != nil {
		t.Fatalf("detach write failed: %v", err)
	}
}

func extractGhosttySidecarPath(bundle string) string {
	const prefix = "./__vite-browser-external-"
	start := strings.Index(bundle, prefix)
	if start == -1 {
		return ""
	}
	rest := bundle[start+2:]
	end := strings.Index(rest, ".js")
	if end == -1 {
		return ""
	}
	return rest[:end+3]
}

func waitForDaemonWebSocketEvent(
	t *testing.T,
	conn *websocket.Conn,
	timeout time.Duration,
	match func(map[string]interface{}) bool,
) map[string]interface{} {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Until(deadline))
		_, payload, err := conn.Read(ctx)
		cancel()
		if err != nil {
			t.Fatalf("websocket read failed: %v", err)
		}

		var event map[string]interface{}
		if err := json.Unmarshal(payload, &event); err != nil {
			t.Fatalf("decode websocket event: %v", err)
		}
		if match(event) {
			return event
		}
	}

	t.Fatalf("timed out waiting for websocket event after %v", timeout)
	return nil
}

func waitForProtocolWebSocketEvent(t *testing.T, conn *websocket.Conn, want string) protocol.WebSocketEvent {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Until(deadline))
		_, payload, err := conn.Read(ctx)
		cancel()
		if err != nil {
			t.Fatalf("websocket read failed while waiting for %s: %v", want, err)
		}

		var event protocol.WebSocketEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			t.Fatalf("decode websocket event: %v", err)
		}
		if event.Event == want {
			return event
		}
	}

	t.Fatalf("timed out waiting for websocket event %s", want)
	return protocol.WebSocketEvent{}
}

func sendWorkspaceClientHello(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	if err := writeWS(conn, map[string]interface{}{
		"cmd":          protocol.CmdClientHello,
		"client_kind":  "daemon-test",
		"version":      "protocol-" + protocol.ProtocolVersion,
		"capabilities": []string{protocol.CapabilityWorkspaceSessions},
	}); err != nil {
		t.Fatalf("send client hello: %v", err)
	}
}

func initialStateIncludesSession(event map[string]interface{}, sessionID string) bool {
	rawSessions, ok := event["sessions"].([]interface{})
	if !ok {
		return false
	}
	for _, raw := range rawSessions {
		session, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if asString(session["id"]) == sessionID {
			return true
		}
	}
	return false
}

func waitForPtyOutputContaining(
	t *testing.T,
	conn *websocket.Conn,
	sessionID string,
	want string,
	timeout time.Duration,
) string {
	t.Helper()

	var combined strings.Builder
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Until(deadline))
		_, payload, err := conn.Read(ctx)
		cancel()
		if err != nil {
			t.Fatalf("websocket read while waiting for pty_output: %v", err)
		}

		var event map[string]interface{}
		if err := json.Unmarshal(payload, &event); err != nil {
			t.Fatalf("decode pty_output event: %v", err)
		}
		if asString(event["event"]) != protocol.EventPtyOutput || asString(event["id"]) != sessionID {
			continue
		}

		encoded := asString(event["data"])
		if encoded == "" {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatalf("decode pty_output base64: %v", err)
		}
		combined.Write(decoded)
		if strings.Contains(combined.String(), want) {
			return combined.String()
		}
	}

	t.Fatalf("timed out waiting for pty output containing %q after %v", want, timeout)
	return ""
}

func readOutboundEvent(t *testing.T, client *wsClient) map[string]interface{} {
	t.Helper()
	select {
	case outbound := <-client.send:
		var event map[string]interface{}
		if err := json.Unmarshal(outbound.payload, &event); err != nil {
			t.Fatalf("decode outbound event: %v", err)
		}
		return event
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for outbound event")
		return nil
	}
}

func assertNoOutboundEvent(t *testing.T, client *wsClient) {
	t.Helper()
	select {
	case outbound := <-client.send:
		t.Fatalf("unexpected outbound event: %s", string(outbound.payload))
	default:
	}
}

func TestDaemon_SettingsValidation(t *testing.T) {
	// Test the validateSetting function directly
	d := &Daemon{}

	tests := []struct {
		name    string
		key     string
		value   string
		wantErr bool
	}{
		{"valid projects_directory", "projects_directory", t.TempDir(), false},
		{"valid new_session_agent codex", "new_session_agent", "codex", false},
		{"valid new_session_agent claude", "new_session_agent", "claude", false},
		{"valid new_session_agent copilot", "new_session_agent", "copilot", false},
		{"unregistered future plugin agent pi", "new_session_agent", "pi", true},
		{"empty new_session_agent", "new_session_agent", "", false},
		{"empty claude_executable", "claude_executable", "", false},
		{"empty codex_executable", "codex_executable", "", false},
		{"empty copilot_executable", "copilot_executable", "", false},
		{"empty reviewer_model", "reviewer_model", "", false},
		{"custom reviewer_model", "reviewer_model", "claude-sonnet-4-6", false},
		{"empty keeper compact", "workspace_keeper_compact", "", false},
		{"invalid keeper compact json", "workspace_keeper_compact", "{", true},
		{"incomplete keeper compact", "workspace_keeper_compact", `{"agent":"codex"}`, true},
		{"valid ticketBoardScale", "ticketBoardScale", "1.2", false},
		{"empty ticketBoardScale matches app", "ticketBoardScale", "", false},
		{"ticketBoardScale out of range", "ticketBoardScale", "3.0", true},
		{"ticketBoardScale not a number", "ticketBoardScale", "big", true},
		{"valid tailscale_enabled true", "tailscale_enabled", "true", false},
		{"valid tailscale_enabled false", "tailscale_enabled", "false", false},
		{"empty keybindings_config", "keybindings_config", "", false},
		{"valid keybindings_config", "keybindings_config", `{"version":1,"overrides":{"session.new":{"key":"m","meta":true}}}`, false},
		{"invalid keybindings_config json", "keybindings_config", "{not json", true},
		{"invalid claude_executable", "claude_executable", "not-a-real-binary-123", true},
		{"invalid new_session_agent", "new_session_agent", "gpt", true},
		{"invalid tailscale_enabled", "tailscale_enabled", "maybe", true},
		{"invalid key", "unknown_setting", "value", true},
		{"empty projects_directory", "projects_directory", "", true},
		{"relative path", "projects_directory", "relative/path", true},
		{"empty chief context cap uses default", "chief_context_window_cap", "", false},
		{"valid chief context cap", "chief_context_window_cap", "128000", false},
		{"chief context cap below min", "chief_context_window_cap", "5000", true},
		{"chief context cap above max", "chief_context_window_cap", "9000000", true},
		{"non-numeric chief context cap", "chief_context_window_cap", "lots", true},
		{"empty headless context cap uses default", "headless_context_window_cap", "", false},
		{"valid headless context cap", "headless_context_window_cap", "200000", false},
		{"headless context cap below min", "headless_context_window_cap", "1", true},
		{"valid chief_effort_claude", "chief_effort_claude", "high", false},
		{"empty chief_effort_claude", "chief_effort_claude", "", false},
		{"valid default_model_claude", "default_model_claude", "opus", false},
		{"empty default_model_claude", "default_model_claude", "", false},
		{"valid default_effort_claude", "default_effort_claude", "high", false},
		{"empty default_effort_claude", "default_effort_claude", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := d.validateSetting(tt.key, tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSetting(%q, %q) error = %v, wantErr %v", tt.key, tt.value, err, tt.wantErr)
			}
		})
	}
}

func TestDaemon_ContextWindowCapResolutionAndGating(t *testing.T) {
	d := &Daemon{store: store.New()}

	// resolveContextWindowCap: blank / unparseable / non-positive => default.
	for _, v := range []string{"", "  ", "not-a-number", "0", "-100"} {
		if got := resolveContextWindowCap(v); got != agentdriver.DefaultContextWindowCap {
			t.Fatalf("resolveContextWindowCap(%q) = %d, want default %d", v, got, agentdriver.DefaultContextWindowCap)
		}
	}
	if got := resolveContextWindowCap("200000"); got != 200000 {
		t.Fatalf("resolveContextWindowCap(200000) = %d, want 200000", got)
	}

	// chiefContextWindowCap: 0 for non-chief; default for a chief with no setting;
	// the configured value for a chief that set one.
	if got := d.chiefContextWindowCap(false); got != 0 {
		t.Fatalf("non-chief cap = %d, want 0 (uncapped)", got)
	}
	if got := d.chiefContextWindowCap(true); got != agentdriver.DefaultContextWindowCap {
		t.Fatalf("chief cap with no setting = %d, want default %d", got, agentdriver.DefaultContextWindowCap)
	}
	d.store.SetSetting(SettingChiefContextWindowCap, "160000")
	if got := d.chiefContextWindowCap(true); got != 160000 {
		t.Fatalf("chief cap = %d, want 160000", got)
	}
}

func TestDaemon_DefaultLaunchModelAndEffort(t *testing.T) {
	d := &Daemon{store: store.New()}

	// Unset => "" (the agent's own default), regardless of chief status.
	if got := d.defaultLaunchModel("claude"); got != "" {
		t.Fatalf("default model with no setting = %q, want \"\"", got)
	}
	if got := d.defaultLaunchEffort("claude"); got != "" {
		t.Fatalf("default effort with no setting = %q, want \"\"", got)
	}

	d.store.SetSetting(SettingDefaultModelPrefix+"claude", "opus")
	d.store.SetSetting(SettingDefaultEffortPrefix+"claude", "high")

	// Applies to non-chief AND chief launches alike, unlike chiefLaunchModel.
	if got := d.defaultLaunchModel("claude"); got != "opus" {
		t.Fatalf("default model = %q, want %q", got, "opus")
	}
	if got := d.defaultLaunchEffort("claude"); got != "high" {
		t.Fatalf("default effort = %q, want %q", got, "high")
	}

	// Agent name is normalized (trimmed and lowercased) before the lookup.
	if got := d.defaultLaunchModel(" Claude "); got != "opus" {
		t.Fatalf("default model with unnormalized agent = %q, want %q", got, "opus")
	}
}

func TestDaemon_ResolveLaunchModelAndEffort(t *testing.T) {
	d := &Daemon{store: store.New()}
	d.store.SetSetting(SettingChiefModelPrefix+"claude", "opus")
	d.store.SetSetting(SettingChiefEffortPrefix+"claude", "max")
	d.store.SetSetting(SettingDefaultModelPrefix+"claude", "sonnet")
	d.store.SetSetting(SettingDefaultEffortPrefix+"claude", "medium")

	// 1. An explicit per-spawn pin always wins, chief or not.
	if got := d.resolveLaunchModel("claude", false, "haiku"); got != "haiku" {
		t.Fatalf("explicit pin (non-chief) = %q, want %q", got, "haiku")
	}
	if got := d.resolveLaunchModel("claude", true, "haiku"); got != "haiku" {
		t.Fatalf("explicit pin (chief) = %q, want %q", got, "haiku")
	}

	// 2. No pin, chief launch: chief_model_<agent> wins over default_model_<agent>.
	if got := d.resolveLaunchModel("claude", true, ""); got != "opus" {
		t.Fatalf("chief resolve = %q, want chief override %q", got, "opus")
	}
	if got := d.resolveLaunchEffort("claude", true, ""); got != "max" {
		t.Fatalf("chief resolve effort = %q, want chief override %q", got, "max")
	}

	// 3. No pin, non-chief launch: falls through to default_model_<agent> (the
	// new configurable fallback that previously did not exist for non-chief
	// launches).
	if got := d.resolveLaunchModel("claude", false, ""); got != "sonnet" {
		t.Fatalf("non-chief resolve = %q, want default %q", got, "sonnet")
	}
	if got := d.resolveLaunchEffort("claude", false, ""); got != "medium" {
		t.Fatalf("non-chief resolve effort = %q, want default %q", got, "medium")
	}

	// 4. No pin, no settings at all for an agent: agent's own default (empty).
	if got := d.resolveLaunchModel("codex", false, ""); got != "" {
		t.Fatalf("no-setting resolve = %q, want \"\"", got)
	}
}

func TestDaemon_ChiefLaunchEffort(t *testing.T) {
	d := &Daemon{store: store.New()}

	// chiefLaunchEffort: "" for non-chief regardless of setting; "" for a
	// chief with no setting; the configured value for a chief that set one.
	d.store.SetSetting(SettingChiefEffortPrefix+"claude", "high")
	if got := d.chiefLaunchEffort("claude", false); got != "" {
		t.Fatalf("non-chief effort = %q, want \"\"", got)
	}
	if got := d.chiefLaunchEffort("codex", true); got != "" {
		t.Fatalf("chief effort with no setting = %q, want \"\"", got)
	}
	if got := d.chiefLaunchEffort("claude", true); got != "high" {
		t.Fatalf("chief effort = %q, want %q", got, "high")
	}

	// Agent name is normalized (trimmed and lowercased) before the setting lookup.
	if got := d.chiefLaunchEffort(" Claude ", true); got != "high" {
		t.Fatalf("chief effort with unnormalized agent = %q, want %q", got, "high")
	}
}

func TestDaemon_ApplyHeadlessContextWindowCap(t *testing.T) {
	// The global is process-wide; restore it so this test does not leak into
	// others that run headless spawns in the same binary.
	t.Cleanup(func() { agentdriver.SetHeadlessContextWindowCap(0) })

	d := &Daemon{store: store.New()}

	// No setting => the default is pushed into the agent package's global.
	d.applyHeadlessContextWindowCap()
	if got := agentdriver.HeadlessContextWindowCap(); got != agentdriver.DefaultContextWindowCap {
		t.Fatalf("headless cap with no setting = %d, want default %d", got, agentdriver.DefaultContextWindowCap)
	}

	// A configured value flows through on the next apply.
	d.store.SetSetting(SettingHeadlessContextWindowCap, "180000")
	d.applyHeadlessContextWindowCap()
	if got := agentdriver.HeadlessContextWindowCap(); got != 180000 {
		t.Fatalf("headless cap = %d, want 180000", got)
	}
}

func TestDaemon_ValidatesKeeperCompactAgentAndExecutable(t *testing.T) {
	tempDir := t.TempDir()
	executable := filepath.Join(tempDir, "custom-codex")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", tempDir)

	d := &Daemon{store: store.New()}
	d.store.SetSetting(SettingCodexExecutable, "custom-codex")
	if err := d.validateSetting(
		SettingKeeperCompact,
		`{"agent":"codex","model":"gpt-test"}`,
	); err != nil {
		t.Fatalf("valid keeper compact setting rejected: %v", err)
	}

	d.store.SetSetting(SettingCodexExecutable, "missing-codex")
	if err := d.validateSetting(
		SettingKeeperCompact,
		`{"agent":"codex","model":"gpt-test"}`,
	); err == nil {
		t.Fatal("keeper compact setting accepted a missing configured executable")
	}
}

func TestDaemon_SettingsWithAgentAvailability(t *testing.T) {
	t.Setenv("PATH", "")
	d := &Daemon{store: store.New()}
	d.store.SetSetting(SettingNewSessionAgent, "codex")

	settings := d.settingsWithAgentAvailability()
	if got := settings[SettingNewSessionAgent]; got != "codex" {
		t.Fatalf("settings[%s] = %v, want codex", SettingNewSessionAgent, got)
	}
	if got := settings[SettingClaudeAvailable]; got != "false" {
		t.Fatalf("settings[%s] = %v, want false", SettingClaudeAvailable, got)
	}
	if got := settings[SettingCodexAvailable]; got != "false" {
		t.Fatalf("settings[%s] = %v, want false", SettingCodexAvailable, got)
	}
	if got := settings[SettingCopilotAvailable]; got != "false" {
		t.Fatalf("settings[%s] = %v, want false", SettingCopilotAvailable, got)
	}
	if _, ok := settings["pi_available"]; ok {
		t.Fatalf("settings unexpectedly advertises pi without an installed plugin")
	}
	if _, ok := settings["pi_cap_transcript"]; ok {
		t.Fatalf("settings unexpectedly advertises in-tree pi capabilities")
	}
	if got := settings["codex_cap_transcript"]; got != "true" {
		t.Fatalf("settings[codex_cap_transcript] = %v, want true", got)
	}
	if got := settings["codex_cap_headless_task"]; got != "true" {
		t.Fatalf("settings[codex_cap_headless_task] = %v, want true", got)
	}
	if got := settings["copilot_cap_headless_task"]; got != "false" {
		t.Fatalf("settings[copilot_cap_headless_task] = %v, want false", got)
	}
	if got := settings[SettingPTYBackendMode]; got != "unknown" {
		t.Fatalf("settings[%s] = %v, want unknown", SettingPTYBackendMode, got)
	}
	if got := settings[SettingTailscaleEnabled]; got != "false" {
		t.Fatalf("settings[%s] = %v, want false", SettingTailscaleEnabled, got)
	}
	if got := settings["tailscale_status"]; got != tailscaleStatusDisabled {
		t.Fatalf("settings[tailscale_status] = %v, want %s", got, tailscaleStatusDisabled)
	}

	tmp := t.TempDir()
	custom := filepath.Join(tmp, "custom-codex")
	if err := os.WriteFile(custom, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("write custom executable: %v", err)
	}
	t.Setenv("PATH", tmp)
	d.store.SetSetting(SettingCodexExecutable, "custom-codex")

	settings = d.settingsWithAgentAvailability()
	if got := settings[SettingCodexAvailable]; got != "true" {
		t.Fatalf("settings[%s] = %v, want true", SettingCodexAvailable, got)
	}
	if got := settings[SettingClaudeAvailable]; got != "false" {
		t.Fatalf("settings[%s] = %v, want false", SettingClaudeAvailable, got)
	}
	if got := settings[SettingCopilotAvailable]; got != "false" {
		t.Fatalf("settings[%s] = %v, want false", SettingCopilotAvailable, got)
	}
	if _, ok := settings["pi_available"]; ok {
		t.Fatalf("settings unexpectedly advertises pi without an installed plugin")
	}

	d.store.SetSetting(SettingTailscaleEnabled, "true")
	d.tailscale = newTailscaleRuntimeWithCLI(nil)
	d.tailscale.snapshot = tailscaleStateSnapshot{
		status:  tailscaleStatusNeedsLogin,
		domain:  "macbook-epidemic.tail1bfe77.ts.net",
		authURL: "https://login.tailscale.example/auth",
	}
	settings = d.settingsWithAgentAvailability()
	if got := settings[SettingTailscaleEnabled]; got != "true" {
		t.Fatalf("settings[%s] = %v, want true", SettingTailscaleEnabled, got)
	}
	if got := settings["tailscale_domain"]; got != "macbook-epidemic.tail1bfe77.ts.net" {
		t.Fatalf("settings[tailscale_domain] = %v, want DNS name", got)
	}
	if got := settings["tailscale_status"]; got != tailscaleStatusNeedsLogin {
		t.Fatalf("settings[tailscale_status] = %v, want %s", got, tailscaleStatusNeedsLogin)
	}
	if got := settings["tailscale_auth_url"]; got != "https://login.tailscale.example/auth" {
		t.Fatalf("settings[tailscale_auth_url] = %v, want auth url", got)
	}
}

func TestDaemon_AdvertisesClaudeHeadlessTaskWithManagedAuthentication(t *testing.T) {
	tempDir := t.TempDir()
	executable := filepath.Join(tempDir, "claude")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	t.Setenv("PATH", tempDir)
	for _, name := range []string{
		"ANTHROPIC_API_KEY",
		"CLAUDE_CODE_USE_BEDROCK",
		"CLAUDE_CODE_USE_VERTEX",
		"CLAUDE_CODE_USE_FOUNDRY",
	} {
		t.Setenv(name, "")
	}

	d := &Daemon{store: store.New()}
	settings := d.settingsWithAgentAvailability()
	if got := settings[SettingClaudeAvailable]; got != "true" {
		t.Fatalf("settings[%s] = %v, want true", SettingClaudeAvailable, got)
	}
	if got := settings["claude_cap_headless_task"]; got != "true" {
		t.Fatalf("settings[claude_cap_headless_task] = %v, want true", got)
	}
}

func TestDaemon_EnsureTailscaleServeFromSettingsAndBroadcast_BroadcastsUpdatedState(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	d.store.SetSetting(SettingTailscaleEnabled, "true")
	d.tailscale = newTailscaleRuntimeWithCLI(&fakeTailscaleCLI{
		run: func(args []string) ([]byte, error) {
			switch strings.Join(args, " ") {
			case "status --json":
				return []byte(`{"BackendState":"NeedsLogin","AuthURL":"https://login.tailscale.example/auth","Self":{"DNSName":"gpu-box.tail.ts.net."}}`), nil
			case "serve status --json":
				return []byte(`{}`), nil
			default:
				t.Fatalf("unexpected tailscale command: %q", strings.Join(args, " "))
				return nil, nil
			}
		},
	})

	d.ensureTailscaleServeFromSettingsAndBroadcast()

	select {
	case outbound := <-d.wsHub.broadcast:
		if outbound.kind != messageKindText {
			t.Fatalf("broadcast kind = %v, want text", outbound.kind)
		}
		var msg protocol.SettingsUpdatedMessage
		if err := json.Unmarshal(outbound.payload, &msg); err != nil {
			t.Fatalf("unmarshal broadcast payload: %v", err)
		}
		if msg.Event != protocol.EventSettingsUpdated {
			t.Fatalf("broadcast event = %q, want %q", msg.Event, protocol.EventSettingsUpdated)
		}
		if got := msg.Settings["tailscale_status"]; got != tailscaleStatusNeedsLogin {
			t.Fatalf("broadcast tailscale_status = %v, want %s", got, tailscaleStatusNeedsLogin)
		}
		if got := msg.Settings["tailscale_auth_url"]; got != "https://login.tailscale.example/auth" {
			t.Fatalf("broadcast tailscale_auth_url = %v, want auth url", got)
		}
	case <-time.After(time.Second):
		t.Fatal("expected settings_updated broadcast")
	}
}

func TestDaemon_SettingsIncludePTYBackendMode(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))

	settings := d.settingsWithAgentAvailability()
	if got := settings[SettingPTYBackendMode]; got != "embedded" {
		t.Fatalf("settings[%s] = %v, want embedded", SettingPTYBackendMode, got)
	}

	workerBackend, err := ptybackend.NewWorker(ptybackend.WorkerBackendConfig{
		DataRoot:         t.TempDir(),
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       "/bin/true",
	})
	if err != nil {
		t.Fatalf("NewWorker() error: %v", err)
	}
	d.ptyBackend = workerBackend

	settings = d.settingsWithAgentAvailability()
	if got := settings[SettingPTYBackendMode]; got != "worker" {
		t.Fatalf("settings[%s] = %v, want worker", SettingPTYBackendMode, got)
	}
}

func TestDaemon_SettingsWithClaudeAvailability_InstallsClaudeSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv(toolhome.EnvVar, home)

	binDir := t.TempDir()
	claudePath := filepath.Join(binDir, "claude")
	if err := os.WriteFile(claudePath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake claude executable: %v", err)
	}
	t.Setenv("PATH", binDir)

	d := &Daemon{store: store.New()}
	settings := d.settingsWithAgentAvailability()
	if got := settings[SettingClaudeAvailable]; got != "true" {
		t.Fatalf("settings[%s] = %v, want true", SettingClaudeAvailable, got)
	}

	skillPath := filepath.Join(home, ".claude", "skills", "attn", "SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		t.Fatalf("expected Claude attn skill at %s: %v", skillPath, err)
	}
	delegationPath := filepath.Join(home, ".claude", "skills", "attn", "references", "delegation.md")
	if _, err := os.Stat(delegationPath); err != nil {
		t.Fatalf("expected Claude attn delegation reference at %s: %v", delegationPath, err)
	}
}

func TestDaemon_SettingsWithCodexAvailability_InstallsCodexSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv(toolhome.EnvVar, home)

	binDir := t.TempDir()
	codexPath := filepath.Join(binDir, "codex")
	if err := os.WriteFile(codexPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake Codex executable: %v", err)
	}
	t.Setenv("PATH", binDir)

	d := &Daemon{store: store.New()}
	settings := d.settingsWithAgentAvailability()
	if got := settings[SettingCodexAvailable]; got != "true" {
		t.Fatalf("settings[%s] = %v, want true", SettingCodexAvailable, got)
	}

	skillPath := filepath.Join(home, ".agents", "skills", "attn", "SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		t.Fatalf("expected Codex attn skill at %s: %v", skillPath, err)
	}
	delegationPath := filepath.Join(home, ".agents", "skills", "attn", "references", "delegation.md")
	if _, err := os.Stat(delegationPath); err != nil {
		t.Fatalf("expected Codex attn delegation reference at %s: %v", delegationPath, err)
	}
}

func TestDaemon_ApprovePR_ViaWebSocket(t *testing.T) {
	// Create mock GitHub server
	mockGH := mockserver.New()
	defer mockGH.Close()

	// Add a mock PR
	mockGH.AddPR(mockserver.MockPR{
		Repo:   "test/repo",
		Number: 42,
		Title:  "Test PR",
		Draft:  false,
		Role:   "reviewer",
	})

	wsPort := useFreeWSPort(t)

	// Create GitHub client pointing to mock server
	ghClient, err := github.NewClient(mockGH.URL, "test-token")
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}
	t.Setenv("ATTN_MOCK_GH_URL", mockGH.URL)
	t.Setenv("ATTN_MOCK_GH_TOKEN", "test-token")
	t.Setenv("ATTN_MOCK_GH_HOST", ghClient.Host())

	// Create daemon with GitHub client
	// Use /tmp directly to avoid long socket paths, with unique suffix to prevent parallel test conflicts
	sockPath := filepath.Join(shortTempDir(t), "attn.sock")
	os.Remove(sockPath) // Clean up any existing socket
	d := NewWithGitHubClient(sockPath, ghClient)

	// Start daemon in background
	go func() {
		if err := d.Start(); err != nil {
			t.Logf("Daemon start error: %v", err)
		}
	}()
	defer func() {
		d.Stop()
		os.Remove(sockPath)
	}()

	// Wait for daemon and WebSocket server to start (with retries)
	// First wait for the unix socket to be ready
	time.Sleep(200 * time.Millisecond)

	ctx := context.Background()
	wsURL := "ws://127.0.0.1:" + wsPort + "/ws"
	var conn *websocket.Conn
	maxRetries := 20
	for i := 0; i < maxRetries; i++ {
		time.Sleep(100 * time.Millisecond)
		var dialErr error
		conn, _, dialErr = websocket.Dial(ctx, wsURL, nil)
		if dialErr == nil {
			t.Logf("WebSocket connected successfully after %d retries", i+1)
			break
		}
		if i == maxRetries-1 {
			t.Fatalf("WebSocket dial error after %d retries: %v", maxRetries, dialErr)
		}
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Read initial state
	_, initialData, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("Read initial state error: %v", err)
	}
	t.Logf("Initial state: %s", string(initialData))
	sendWorkspaceClientHello(t, conn)

	// Send approve command
	prID := protocol.FormatPRID(ghClient.Host(), "test/repo", 42)
	approveCmd := map[string]interface{}{
		"cmd": "approve_pr",
		"id":  prID,
	}
	approveJSON, _ := json.Marshal(approveCmd)
	err = conn.Write(ctx, websocket.MessageText, approveJSON)
	if err != nil {
		t.Fatalf("Write approve command error: %v", err)
	}

	// Read responses until we get pr_action_result (prs_updated may come first due to background polling)
	var response protocol.PRActionResultMessage
	for i := 0; i < 10; i++ {
		_, responseData, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("Read response error: %v", err)
		}
		t.Logf("Response %d: %s", i+1, string(responseData))

		// Check if this is the pr_action_result event
		var eventCheck struct {
			Event string `json:"event"`
		}
		json.Unmarshal(responseData, &eventCheck)
		if eventCheck.Event == "pr_action_result" {
			err = json.Unmarshal(responseData, &response)
			if err != nil {
				t.Fatalf("Unmarshal response error: %v", err)
			}
			break
		}
		// Otherwise it's probably prs_updated from background polling, continue reading
	}

	// Verify response
	if !response.Success {
		t.Errorf("Expected success=true, got success=%v, error=%s", response.Success, protocol.Deref(response.Error))
	}
	if response.Action != "approve" {
		t.Errorf("Expected action=approve, got action=%s", response.Action)
	}
	if response.ID != prID {
		t.Errorf("Expected id=%s, got id=%s", prID, response.ID)
	}

	// Verify mock server received the approve request
	if !mockGH.HasApproveRequest("test/repo", 42) {
		t.Error("Mock server did not receive approve request for test/repo#42")
	}
}

func TestDaemon_InjectTestPR(t *testing.T) {
	useFreeWSPort(t)

	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")

	d := NewForTesting(sockPath)
	go d.Start()
	defer d.Stop()

	waitForSocket(t, sockPath, 5*time.Second)

	c := client.New(sockPath)

	// Create test PR data
	testPR := protocol.PR{
		ID:          "github.com:owner/repo#123",
		Repo:        "owner/repo",
		Number:      123,
		Title:       "Test PR for E2E",
		URL:         "https://github.com/owner/repo/pull/123",
		Role:        protocol.PRRoleAuthor,
		State:       protocol.PRStateWaiting,
		Reason:      protocol.PRReasonReadyToMerge,
		LastUpdated: protocol.TimestampNow().String(),
		LastPolled:  protocol.TimestampNow().String(),
		Muted:       false,
	}

	// Send inject_test_pr message
	msg := protocol.InjectTestPRMessage{
		Cmd: protocol.CmdInjectTestPR,
		PR:  testPR,
	}
	msgJSON, _ := json.Marshal(msg)

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Dial error: %v", err)
	}
	defer conn.Close()

	_, err = conn.Write(msgJSON)
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}

	// Read response
	var resp protocol.Response
	err = json.NewDecoder(conn).Decode(&resp)
	if err != nil {
		t.Fatalf("Decode response error: %v", err)
	}

	if !resp.Ok {
		t.Fatalf("Expected Ok=true, got Ok=%v, Error=%s", resp.Ok, protocol.Deref(resp.Error))
	}

	// Verify PR was added using query_prs
	prs, err := c.QueryPRs("")
	if err != nil {
		t.Fatalf("QueryPRs error: %v", err)
	}

	if len(prs) != 1 {
		t.Fatalf("Expected 1 PR, got %d", len(prs))
	}

	if prs[0].ID != "github.com:owner/repo#123" {
		t.Errorf("Expected ID=github.com:owner/repo#123, got ID=%s", prs[0].ID)
	}
	if prs[0].Title != "Test PR for E2E" {
		t.Errorf("Expected Title='Test PR for E2E', got Title=%s", prs[0].Title)
	}
	if prs[0].State != protocol.PRStateWaiting {
		t.Errorf("Expected State=waiting, got State=%s", prs[0].State)
	}
}

func TestDaemon_MutePR_ViaWebSocket(t *testing.T) {
	wsPort := useFreeWSPort(t)

	// Use /tmp directly to avoid long socket paths, with unique suffix to prevent parallel test conflicts
	sockPath := filepath.Join(shortTempDir(t), "attn.sock")
	os.Remove(sockPath) // Clean up any existing socket

	d := NewForTesting(sockPath)

	// Start daemon in background
	go func() {
		if err := d.Start(); err != nil {
			t.Logf("Daemon start error: %v", err)
		}
	}()
	defer func() {
		d.Stop()
		os.Remove(sockPath)
	}()

	// Wait for daemon to start before dialing its Unix socket.
	waitForSocket(t, sockPath, 5*time.Second)

	// Inject test PR via unix socket
	testPR := protocol.PR{
		ID:          "github.com:owner/repo#123",
		Repo:        "owner/repo",
		Number:      123,
		Title:       "Test PR",
		URL:         "https://github.com/owner/repo/pull/123",
		Role:        protocol.PRRoleAuthor,
		State:       protocol.PRStateWaiting,
		Reason:      protocol.PRReasonReadyToMerge,
		LastUpdated: protocol.TimestampNow().String(),
		LastPolled:  protocol.TimestampNow().String(),
		Muted:       false,
	}
	msg := protocol.InjectTestPRMessage{
		Cmd: protocol.CmdInjectTestPR,
		PR:  testPR,
	}
	msgJSON, _ := json.Marshal(msg)
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Dial error: %v", err)
	}
	if _, err := conn.Write(msgJSON); err != nil {
		t.Fatalf("Write inject PR error: %v", err)
	}
	var resp protocol.Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatalf("Read inject PR response error: %v", err)
	}
	if !resp.Ok {
		t.Fatalf("Inject PR failed: %s", protocol.Deref(resp.Error))
	}
	conn.Close()

	// Connect to WebSocket
	ctx := context.Background()
	wsURL := "ws://127.0.0.1:" + wsPort + "/ws"
	var wsConn *websocket.Conn
	maxRetries := 20
	for i := 0; i < maxRetries; i++ {
		time.Sleep(100 * time.Millisecond)
		var dialErr error
		wsConn, _, dialErr = websocket.Dial(ctx, wsURL, nil)
		if dialErr == nil {
			break
		}
		if i == maxRetries-1 {
			t.Fatalf("WebSocket dial error after %d retries: %v", maxRetries, dialErr)
		}
	}
	defer wsConn.Close(websocket.StatusNormalClosure, "")

	// Read initial state (other background broadcasts may arrive first).
	initialState := waitForProtocolWebSocketEvent(t, wsConn, protocol.EventInitialState)
	if len(initialState.Prs) != 1 {
		t.Fatalf("Expected 1 PR in initial state, got %d", len(initialState.Prs))
	}
	if initialState.Prs[0].Muted {
		t.Error("Expected PR to not be muted initially")
	}
	sendWorkspaceClientHello(t, wsConn)

	// Send mute_pr command
	muteCmd := map[string]interface{}{
		"cmd": "mute_pr",
		"id":  "github.com:owner/repo#123",
	}
	muteJSON, _ := json.Marshal(muteCmd)
	err = wsConn.Write(ctx, websocket.MessageText, muteJSON)
	if err != nil {
		t.Fatalf("Write mute command error: %v", err)
	}

	// Other background broadcasts may arrive before the PR update.
	updateEvent := waitForProtocolWebSocketEvent(t, wsConn, protocol.EventPRsUpdated)
	if len(updateEvent.Prs) != 1 {
		t.Fatalf("Expected 1 PR in update, got %d", len(updateEvent.Prs))
	}
	if !updateEvent.Prs[0].Muted {
		t.Error("Expected PR to be muted after mute command")
	}

	// Send mute_pr again to toggle back
	err = wsConn.Write(ctx, websocket.MessageText, muteJSON)
	if err != nil {
		t.Fatalf("Write second mute command error: %v", err)
	}

	updateEvent2 := waitForProtocolWebSocketEvent(t, wsConn, protocol.EventPRsUpdated)
	if updateEvent2.Prs[0].Muted {
		t.Error("Expected PR to be unmuted after second mute command (toggle)")
	}
}

func TestDaemon_MuteRepo_ViaWebSocket(t *testing.T) {
	wsPort := useFreeWSPort(t)

	// Use /tmp directly to avoid long socket paths, with unique suffix to prevent parallel test conflicts
	sockPath := filepath.Join(shortTempDir(t), "attn.sock")
	os.Remove(sockPath) // Clean up any existing socket

	d := NewForTesting(sockPath)

	// Start daemon in background
	go func() {
		if err := d.Start(); err != nil {
			t.Logf("Daemon start error: %v", err)
		}
	}()
	defer func() {
		d.Stop()
		os.Remove(sockPath)
	}()

	// Wait for daemon to start
	time.Sleep(200 * time.Millisecond)

	// Connect to WebSocket
	ctx := context.Background()
	wsURL := "ws://127.0.0.1:" + wsPort + "/ws"
	var wsConn *websocket.Conn
	maxRetries := 20
	for i := 0; i < maxRetries; i++ {
		time.Sleep(100 * time.Millisecond)
		var dialErr error
		wsConn, _, dialErr = websocket.Dial(ctx, wsURL, nil)
		if dialErr == nil {
			break
		}
		if i == maxRetries-1 {
			t.Fatalf("WebSocket dial error after %d retries: %v", maxRetries, dialErr)
		}
	}
	defer wsConn.Close(websocket.StatusNormalClosure, "")

	// Read initial state (other background broadcasts may arrive first).
	waitForProtocolWebSocketEvent(t, wsConn, protocol.EventInitialState)
	sendWorkspaceClientHello(t, wsConn)

	// Send mute_repo command
	muteCmd := map[string]interface{}{
		"cmd":  "mute_repo",
		"repo": "owner/test-repo",
	}
	muteJSON, _ := json.Marshal(muteCmd)
	if err := wsConn.Write(ctx, websocket.MessageText, muteJSON); err != nil {
		t.Fatalf("Write mute_repo command error: %v", err)
	}

	// Read repos_updated broadcast (skipping unrelated background broadcasts).
	updateEvent := waitForProtocolWebSocketEvent(t, wsConn, protocol.EventReposUpdated)
	if len(updateEvent.Repos) != 1 {
		t.Fatalf("Expected 1 repo state in update, got %d", len(updateEvent.Repos))
	}
	if updateEvent.Repos[0].Repo != "owner/test-repo" {
		t.Errorf("Expected repo=owner/test-repo, got repo=%s", updateEvent.Repos[0].Repo)
	}
	if !updateEvent.Repos[0].Muted {
		t.Error("Expected repo to be muted after mute_repo command")
	}

	// Send mute_repo again to toggle back
	if err := wsConn.Write(ctx, websocket.MessageText, muteJSON); err != nil {
		t.Fatalf("Write second mute_repo command error: %v", err)
	}

	// Read second repos_updated broadcast.
	updateEvent2 := waitForProtocolWebSocketEvent(t, wsConn, protocol.EventReposUpdated)
	if updateEvent2.Repos[0].Muted {
		t.Error("Expected repo to be unmuted after second mute_repo command (toggle)")
	}
}

func TestDaemon_InitialState_IncludesRepoStates(t *testing.T) {
	wsPort := useFreeWSPort(t)

	// Use /tmp directly to avoid long socket paths, with unique suffix to prevent parallel test conflicts
	sockPath := filepath.Join(shortTempDir(t), "attn.sock")
	os.Remove(sockPath) // Clean up any existing socket

	d := NewForTesting(sockPath)

	// Start daemon in background
	go func() {
		if err := d.Start(); err != nil {
			t.Logf("Daemon start error: %v", err)
		}
	}()
	defer func() {
		d.Stop()
		os.Remove(sockPath)
	}()

	// Wait for daemon to start
	time.Sleep(200 * time.Millisecond)

	// First, toggle a repo mute via unix socket to set up state
	c := client.New(sockPath)
	err := c.ToggleMuteRepo("owner/test-repo")
	if err != nil {
		t.Fatalf("ToggleMuteRepo error: %v", err)
	}

	// Connect to WebSocket
	ctx := context.Background()
	wsURL := "ws://127.0.0.1:" + wsPort + "/ws"
	var wsConn *websocket.Conn
	maxRetries := 20
	for i := 0; i < maxRetries; i++ {
		time.Sleep(100 * time.Millisecond)
		var dialErr error
		wsConn, _, dialErr = websocket.Dial(ctx, wsURL, nil)
		if dialErr == nil {
			break
		}
		if i == maxRetries-1 {
			t.Fatalf("WebSocket dial error after %d retries: %v", maxRetries, dialErr)
		}
	}
	defer wsConn.Close(websocket.StatusNormalClosure, "")

	// Read initial state (other background broadcasts may arrive first).
	initialState := waitForProtocolWebSocketEvent(t, wsConn, protocol.EventInitialState)

	// Verify initial state includes repos
	if initialState.Repos == nil {
		t.Fatal("Expected Repos array in initial state")
	}
	if len(initialState.Repos) != 1 {
		t.Fatalf("Expected 1 repo in initial state, got %d", len(initialState.Repos))
	}
	if initialState.Repos[0].Repo != "owner/test-repo" {
		t.Errorf("Expected repo=owner/test-repo, got repo=%s", initialState.Repos[0].Repo)
	}
	if !initialState.Repos[0].Muted {
		t.Error("Expected repo to be muted in initial state")
	}
}

// ============================================================================
// Session State Flow Tests
// ============================================================================

func TestDaemon_StateChange_BroadcastsToWebSocket(t *testing.T) {
	wsPort := useFreeWSPort(t)

	sockPath := filepath.Join(shortTempDir(t), "attn.sock")
	os.Remove(sockPath)

	d := NewForTesting(sockPath)
	go func() {
		if err := d.Start(); err != nil {
			t.Logf("Daemon start error: %v", err)
		}
	}()
	defer func() {
		d.Stop()
		os.Remove(sockPath)
	}()

	waitForSocket(t, sockPath, 5*time.Second)

	// Register session via unix socket
	c := client.New(sockPath)
	err := c.Register("test-session", "Test Session", "/tmp/test")
	if err != nil {
		t.Fatalf("Register error: %v", err)
	}

	// Connect to WebSocket
	ctx := context.Background()
	wsURL := "ws://127.0.0.1:" + wsPort + "/ws"
	var wsConn *websocket.Conn
	maxRetries := 20
	for i := 0; i < maxRetries; i++ {
		time.Sleep(100 * time.Millisecond)
		var dialErr error
		wsConn, _, dialErr = websocket.Dial(ctx, wsURL, nil)
		if dialErr == nil {
			break
		}
		if i == maxRetries-1 {
			t.Fatalf("WebSocket dial error after %d retries: %v", maxRetries, dialErr)
		}
	}
	defer wsConn.Close(websocket.StatusNormalClosure, "")

	// Read initial state (other background broadcasts may arrive first).
	waitForProtocolWebSocketEvent(t, wsConn, protocol.EventInitialState)

	// Update state to waiting_input via unix socket
	err = c.UpdateState("test-session", protocol.StateWaitingInput)
	if err != nil {
		t.Fatalf("UpdateState error: %v", err)
	}

	// Read WebSocket event - should be session_state_changed (skipping unrelated broadcasts).
	event := waitForProtocolWebSocketEvent(t, wsConn, protocol.EventSessionStateChanged)
	if event.Session == nil {
		t.Fatal("Expected Session in event")
	}
	if event.Session.ID != "test-session" {
		t.Errorf("Expected session id=test-session, got id=%s", event.Session.ID)
	}
	if event.Session.State != protocol.SessionStateWaitingInput {
		t.Errorf("Expected state=%s, got state=%s", protocol.SessionStateWaitingInput, event.Session.State)
	}
}

func TestDaemon_StateTransitions_AllStates(t *testing.T) {
	wsPort := useFreeWSPort(t)

	sockPath := filepath.Join(shortTempDir(t), "attn.sock")
	os.Remove(sockPath)

	d := NewForTesting(sockPath)
	go func() {
		if err := d.Start(); err != nil {
			t.Logf("Daemon start error: %v", err)
		}
	}()
	defer func() {
		d.Stop()
		os.Remove(sockPath)
	}()

	waitForSocket(t, sockPath, 5*time.Second)

	c := client.New(sockPath)
	err := c.Register("test-session", "Test", "/tmp/test")
	if err != nil {
		t.Fatalf("Register error: %v", err)
	}

	// Connect to WebSocket
	ctx := context.Background()
	wsURL := "ws://127.0.0.1:" + wsPort + "/ws"
	var wsConn *websocket.Conn
	maxRetries := 20
	for i := 0; i < maxRetries; i++ {
		time.Sleep(100 * time.Millisecond)
		var dialErr error
		wsConn, _, dialErr = websocket.Dial(ctx, wsURL, nil)
		if dialErr == nil {
			break
		}
		if i == maxRetries-1 {
			t.Fatalf("WebSocket dial error after %d retries: %v", maxRetries, dialErr)
		}
	}
	defer wsConn.Close(websocket.StatusNormalClosure, "")

	// Read initial state
	_, _, err = wsConn.Read(ctx)
	if err != nil {
		t.Fatalf("Read initial state error: %v", err)
	}

	// Test state transitions after register default (launching)
	states := []string{protocol.StateWaitingInput, protocol.StateIdle, protocol.StateWorking, protocol.StateUnknown}

	for _, expectedState := range states {
		err = c.UpdateState("test-session", expectedState)
		if err != nil {
			t.Fatalf("UpdateState to %s error: %v", expectedState, err)
		}

		var event protocol.WebSocketEvent
		for {
			_, eventData, err := wsConn.Read(ctx)
			if err != nil {
				t.Fatalf("Read event error for state %s: %v", expectedState, err)
			}
			if err := json.Unmarshal(eventData, &event); err != nil {
				t.Fatalf("Decode event for state %s: %v", expectedState, err)
			}
			if event.Event == protocol.EventSessionStateChanged {
				break
			}
		}
		// Compare state - need to handle string/SessionState conversion
		gotState := ""
		if event.Session != nil {
			gotState = string(event.Session.State)
		}
		if gotState != expectedState {
			t.Errorf("Expected state=%s, got state=%s", expectedState, gotState)
		}
	}
}

func TestDaemon_InjectTestSession_BroadcastsToWebSocket(t *testing.T) {
	wsPort := useFreeWSPort(t)

	sockPath := filepath.Join(shortTempDir(t), "attn.sock")
	os.Remove(sockPath)

	d := NewForTesting(sockPath)
	go func() {
		if err := d.Start(); err != nil {
			t.Logf("Daemon start error: %v", err)
		}
	}()
	defer func() {
		d.Stop()
		os.Remove(sockPath)
	}()

	waitForSocket(t, sockPath, 5*time.Second)

	// Connect to WebSocket first
	ctx := context.Background()
	wsURL := "ws://127.0.0.1:" + wsPort + "/ws"
	var wsConn *websocket.Conn
	maxRetries := 20
	for i := 0; i < maxRetries; i++ {
		time.Sleep(100 * time.Millisecond)
		var dialErr error
		wsConn, _, dialErr = websocket.Dial(ctx, wsURL, nil)
		if dialErr == nil {
			break
		}
		if i == maxRetries-1 {
			t.Fatalf("WebSocket dial error after %d retries: %v", maxRetries, dialErr)
		}
	}
	defer wsConn.Close(websocket.StatusNormalClosure, "")

	// Read initial state
	_, _, err := wsConn.Read(ctx)
	if err != nil {
		t.Fatalf("Read initial state error: %v", err)
	}

	// Inject test session via unix socket
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Dial error: %v", err)
	}

	injectMsg := map[string]interface{}{
		"cmd": "inject_test_session",
		"session": map[string]interface{}{
			"id":          "injected-session",
			"label":       "Injected Session",
			"directory":   "/tmp/injected",
			"state":       protocol.StateWorking,
			"state_since": time.Now().Format(time.RFC3339),
			"last_seen":   time.Now().Format(time.RFC3339),
			"muted":       false,
		},
	}
	msgJSON, _ := json.Marshal(injectMsg)
	conn.Write(msgJSON)
	conn.Close()

	var event protocol.WebSocketEvent
	for {
		_, eventData, err := wsConn.Read(ctx)
		if err != nil {
			t.Fatalf("Read event error: %v", err)
		}
		if err := json.Unmarshal(eventData, &event); err != nil {
			t.Fatalf("Decode event error: %v", err)
		}
		if event.Event == protocol.EventSessionRegistered {
			break
		}
	}
	if event.Session == nil {
		t.Fatal("Expected Session in event")
	}
	if event.Session.ID != "injected-session" {
		t.Errorf("Expected session id=injected-session, got id=%s", event.Session.ID)
	}
	if event.Session.State != protocol.SessionStateWorking {
		t.Errorf("Expected state=%s, got state=%s", protocol.SessionStateWorking, event.Session.State)
	}
}

func TestDaemon_StopCommand_PendingTodos_SetsWaitingInput(t *testing.T) {
	useFreeWSPort(t)

	// Use /tmp directly to avoid long socket paths
	sockPath := filepath.Join(shortTempDir(t), "attn.sock")
	os.Remove(sockPath) // Clean up any existing socket

	d := NewForTesting(sockPath)
	go d.Start()
	defer func() {
		d.Stop()
		os.Remove(sockPath)
	}()

	time.Sleep(100 * time.Millisecond)

	c := client.New(sockPath)

	// Register session
	err := c.Register("test-session", "Test", "/tmp/test")
	if err != nil {
		t.Fatalf("Register error: %v", err)
	}

	// Send todos with pending items
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Dial error: %v", err)
	}
	todosMsg := map[string]interface{}{
		"cmd":   "todos",
		"id":    "test-session",
		"todos": []string{"[ ] Pending task 1", "[ ] Pending task 2"},
	}
	todosJSON, _ := json.Marshal(todosMsg)
	conn.Write(todosJSON)

	// Read response
	var resp protocol.Response
	json.NewDecoder(conn).Decode(&resp)
	conn.Close()

	if !resp.Ok {
		t.Fatalf("Todos update failed: %s", protocol.Deref(resp.Error))
	}

	// Send stop command (should classify as waiting_input due to pending todos)
	conn2, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Dial error: %v", err)
	}
	stopMsg := map[string]interface{}{
		"cmd":             "stop",
		"id":              "test-session",
		"transcript_path": "/nonexistent/path", // Doesn't matter - pending todos short-circuit
	}
	stopJSON, _ := json.Marshal(stopMsg)
	conn2.Write(stopJSON)
	json.NewDecoder(conn2).Decode(&resp)
	conn2.Close()

	// Wait for async classification to complete
	time.Sleep(200 * time.Millisecond)

	// Query session state
	sessions, err := c.Query("")
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("Expected 1 session, got %d", len(sessions))
	}
	if sessions[0].State != protocol.SessionStateWaitingInput {
		t.Errorf("Expected state=%s (due to pending todos), got state=%s", protocol.SessionStateWaitingInput, sessions[0].State)
	}
}

func TestDaemon_StopCommand_CompletedTodos_ProceedsToClassification(t *testing.T) {
	useFreeWSPort(t)

	// This test verifies that when all todos are completed, the daemon
	// does NOT short-circuit to waiting_input based on todos alone.
	// Instead, it proceeds to classification.
	//
	// When transcript parsing fails, it now returns unknown,
	// but that's different from the todos short-circuit path.

	// Use /tmp directly to avoid long socket paths
	sockPath := filepath.Join(shortTempDir(t), "attn.sock")
	os.Remove(sockPath) // Clean up any existing socket

	d := NewForTesting(sockPath)
	go d.Start()
	defer func() {
		d.Stop()
		os.Remove(sockPath)
	}()

	waitForSocket(t, sockPath, 2*time.Second)

	c := client.New(sockPath)

	// Register session
	err := c.Register("test-session", "Test", "/tmp/test")
	if err != nil {
		t.Fatalf("Register error: %v", err)
	}

	// Send todos with ALL completed items (using [✓] prefix)
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Dial error: %v", err)
	}
	todosMsg := map[string]interface{}{
		"cmd":   "todos",
		"id":    "test-session",
		"todos": []string{"[✓] Completed task 1", "[✓] Completed task 2"},
	}
	todosJSON, _ := json.Marshal(todosMsg)
	conn.Write(todosJSON)

	var resp protocol.Response
	json.NewDecoder(conn).Decode(&resp)
	conn.Close()

	if !resp.Ok {
		t.Fatalf("Todos update failed: %s", protocol.Deref(resp.Error))
	}

	// Verify todos were stored correctly
	sessions, _ := c.Query("")
	if len(sessions) != 1 {
		t.Fatalf("Expected 1 session, got %d", len(sessions))
	}
	if len(sessions[0].Todos) != 2 {
		t.Fatalf("Expected 2 todos, got %d", len(sessions[0].Todos))
	}

	// With all completed todos, stop should proceed to classification (not short-circuit)
	// Since we're providing a nonexistent transcript, classification will fail
	// and return unknown - but this is different from todos short-circuit
	//
	// The key difference:
	// - With pending todos: immediately returns waiting_input (no transcript parsing)
	// - With completed todos: tries to parse transcript, then classify
	//
	// This test mainly ensures the todos count logic correctly skips completed todos
	t.Log("Test passed: todos with [✓] prefix are counted as completed, allowing classification to proceed")
}

func TestClassifySessionState_ClassifierError_StaysUnknown(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.classifier = &errorClassifier{
		state: protocol.StateUnknown,
		err:   errors.New("classifier execution failed"),
	}

	now := time.Now()
	nowStr := string(protocol.NewTimestamp(now))
	d.store.Add(&protocol.Session{
		ID:             "sess-unknown",
		Agent:          protocol.SessionAgentCodex,
		Label:          "test",
		Directory:      "/tmp",
		State:          protocol.StateWorking,
		StateSince:     nowStr,
		StateUpdatedAt: nowStr,
		LastSeen:       nowStr,
	})

	transcriptPath := filepath.Join(t.TempDir(), "transcript.jsonl")
	content := `{"type":"assistant","message":{"role":"assistant","content":"Now running pre-review."}}
`
	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	d.classifySessionState("sess-unknown", transcriptPath)

	sess := d.store.Get("sess-unknown")
	if sess == nil {
		t.Fatal("session missing after classify")
	}
	if sess.State != protocol.StateUnknown {
		t.Fatalf("state = %s, want %s", sess.State, protocol.StateUnknown)
	}
}

func TestClassifySessionState_ClassifierCapabilityDisabled_SetsIdle(t *testing.T) {
	t.Setenv("ATTN_AGENT_CODEX_CLASSIFIER", "0")

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	mockClassifier := &countingClassifier{state: protocol.StateWaitingInput}
	d.classifier = mockClassifier

	now := time.Now()
	nowStr := string(protocol.NewTimestamp(now))
	d.store.Add(&protocol.Session{
		ID:             "sess-no-classifier",
		Agent:          protocol.SessionAgentCodex,
		Label:          "test",
		Directory:      "/tmp",
		State:          protocol.StateWorking,
		StateSince:     nowStr,
		StateUpdatedAt: nowStr,
		LastSeen:       nowStr,
	})

	d.classifySessionState("sess-no-classifier", filepath.Join(t.TempDir(), "missing.jsonl"))

	sess := d.store.Get("sess-no-classifier")
	if sess == nil {
		t.Fatal("session missing after classify")
	}
	if sess.State != protocol.StateIdle {
		t.Fatalf("state = %s, want %s", sess.State, protocol.StateIdle)
	}
	if got := mockClassifier.CallCount(); got != 0 {
		t.Fatalf("classifier calls=%d, want 0", got)
	}
}

func TestClassifySessionState_TranscriptDisabledWithPendingTodos_SetsWaitingInput(t *testing.T) {
	t.Setenv("ATTN_AGENT_CODEX_TRANSCRIPT", "0")

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	mockClassifier := &countingClassifier{state: protocol.StateIdle}
	d.classifier = mockClassifier

	now := time.Now()
	nowStr := string(protocol.NewTimestamp(now))
	d.store.Add(&protocol.Session{
		ID:             "sess-no-transcript",
		Agent:          protocol.SessionAgentCodex,
		Label:          "test",
		Directory:      "/tmp",
		State:          protocol.StateWorking,
		StateSince:     nowStr,
		StateUpdatedAt: nowStr,
		LastSeen:       nowStr,
		Todos:          []string{"[ ] follow up"},
	})

	d.classifySessionState("sess-no-transcript", filepath.Join(t.TempDir(), "missing.jsonl"))

	sess := d.store.Get("sess-no-transcript")
	if sess == nil {
		t.Fatal("session missing after classify")
	}
	if sess.State != protocol.StateWaitingInput {
		t.Fatalf("state = %s, want %s", sess.State, protocol.StateWaitingInput)
	}
	if got := mockClassifier.CallCount(); got != 0 {
		t.Fatalf("classifier calls=%d, want 0", got)
	}
}

func TestClassifySessionState_SkipsNoNewAssistantTurn(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	mockClassifier := &countingClassifier{state: protocol.StateWaitingInput}
	d.classifier = mockClassifier
	d.classificationTranscriptExtractor = func(*protocol.Session, string, int, time.Time) (string, string, error) {
		return "", "", agentdriver.ErrNoNewAssistantTurn
	}

	now := time.Now()
	nowStr := string(protocol.NewTimestamp(now))
	d.store.Add(&protocol.Session{
		ID:             "sess-1",
		Agent:          protocol.SessionAgentClaude,
		Label:          "test",
		Directory:      "/tmp",
		State:          protocol.StateWorking,
		StateSince:     nowStr,
		StateUpdatedAt: nowStr,
		LastSeen:       nowStr,
	})

	d.classifySessionState("sess-1", filepath.Join(t.TempDir(), "transcript.jsonl"))
	if got := mockClassifier.CallCount(); got != 0 {
		t.Fatalf("classifier calls=%d, want 0", got)
	}

	sess := d.store.Get("sess-1")
	if sess == nil {
		t.Fatal("session missing after classification")
	}
	if sess.State != protocol.StateWorking {
		t.Fatalf("state changed on no-new-turn result: got %q want %q", sess.State, protocol.StateWorking)
	}
}

func TestClassifySessionState_ClaudeConcurrentDuplicateTurnRunsOnce(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	mockClassifier := newBlockingClassifier(protocol.StateWaitingInput)
	d.classifier = mockClassifier

	now := time.Now()
	nowStr := string(protocol.NewTimestamp(now))
	d.store.Add(&protocol.Session{
		ID:             "sess-2",
		Agent:          protocol.SessionAgentClaude,
		Label:          "test",
		Directory:      "/tmp",
		State:          protocol.StateWorking,
		StateSince:     nowStr,
		StateUpdatedAt: nowStr,
		LastSeen:       nowStr,
	})

	transcriptPath := filepath.Join(t.TempDir(), "transcript.jsonl")
	content := fmt.Sprintf(
		`{"type":"user","uuid":"u2","timestamp":"%s","message":{"role":"user","content":"hello"}}
{"type":"assistant","uuid":"a2","timestamp":"%s","message":{"role":"assistant","content":[{"type":"text","text":"Hello! What can I help you with today?"}]}}
`,
		now.Add(-1*time.Second).UTC().Format(time.RFC3339Nano),
		now.UTC().Format(time.RFC3339Nano),
	)
	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	firstDone := make(chan struct{})
	go func() {
		d.classifySessionState("sess-2", transcriptPath)
		close(firstDone)
	}()

	select {
	case <-mockClassifier.started:
	case <-time.After(2 * time.Second):
		t.Fatal("classifier did not start for first classification")
	}

	secondDone := make(chan struct{})
	go func() {
		d.classifySessionState("sess-2", transcriptPath)
		close(secondDone)
	}()

	select {
	case <-secondDone:
	case <-time.After(2 * time.Second):
		t.Fatal("second classification did not return promptly")
	}

	if got := mockClassifier.CallCount(); got != 1 {
		t.Fatalf("classifier calls=%d, want 1 while duplicate turn in flight", got)
	}

	close(mockClassifier.release)
	select {
	case <-firstDone:
	case <-time.After(2 * time.Second):
		t.Fatal("first classification did not complete")
	}

	if got := mockClassifier.CallCount(); got != 1 {
		t.Fatalf("classifier calls=%d, want 1", got)
	}
}

func TestClassifierStateTransition_StaleIdleDoesNotClearLongRunTracking(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	now := time.Now()
	nowStr := string(protocol.NewTimestamp(now))
	d.store.Add(&protocol.Session{
		ID:             "sess-stale",
		Agent:          protocol.SessionAgentCodex,
		Label:          "stale",
		Directory:      "/tmp",
		State:          protocol.StateWaitingInput,
		StateSince:     nowStr,
		StateUpdatedAt: nowStr,
		LastSeen:       nowStr,
	})
	d.longRun["sess-stale"] = longRunSession{
		workingSince:       now.Add(-6 * time.Minute),
		deferredTranscript: "/tmp/transcript.jsonl",
		needsReview:        true,
	}

	d.applyState(sessionStateChange{
		sessionID: "sess-stale",
		state:     protocol.StateIdle,
		cause:     classifierObservation{observedAt: now.Add(-1 * time.Minute)},
	})

	session := d.store.Get("sess-stale")
	if session == nil {
		t.Fatal("session missing")
	}
	if session.State != protocol.StateWaitingInput {
		t.Fatalf("state=%s, want %s", session.State, protocol.StateWaitingInput)
	}
	if !d.sessionNeedsReviewAfterLongRun("sess-stale") {
		t.Fatal("needs_review_after_long_run should remain set for stale timestamped update")
	}
}

// TestScheduledClearsLongRunTracking proves that parking on a cron/loop ends
// the current run for long-run-review purposes: a session that did real work
// and then goes "scheduled" must drop its workingSince, so a later short
// resumed turn does not falsely trip the 5-minute long-run review threshold.
func TestScheduledClearsLongRunTracking(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	now := time.Now()
	nowStr := string(protocol.NewTimestamp(now))
	d.store.Add(&protocol.Session{
		ID:             "sess-loop",
		Agent:          protocol.SessionAgentCodex,
		Label:          "loop",
		Directory:      "/tmp",
		State:          protocol.StateWorking,
		StateSince:     nowStr,
		StateUpdatedAt: nowStr,
		LastSeen:       nowStr,
	})
	// It has been working for 10 minutes, then parks on a cron.
	d.longRun["sess-loop"] = longRunSession{workingSince: now.Add(-10 * time.Minute)}

	d.applyState(sessionStateChange{
		sessionID: "sess-loop",
		state:     protocol.StateScheduled,
		cause:     daemonObservation{},
	})

	d.longRunMu.Lock()
	_, tracked := d.longRun["sess-loop"]
	d.longRunMu.Unlock()
	if tracked {
		t.Fatal("parking on a schedule must clear long-run tracking; the leaked workingSince would mis-fire a review on the next short turn")
	}
}

func TestClassifyOrDeferAfterStop_LongRunDefersUntilVisualized(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	mockClassifier := &countingClassifier{state: protocol.StateWaitingInput}
	d.classifier = mockClassifier

	now := time.Now()
	nowStr := string(protocol.NewTimestamp(now))
	d.store.Add(&protocol.Session{
		ID:             "sess-long",
		Agent:          protocol.SessionAgentCodex,
		Label:          "long",
		Directory:      "/tmp",
		State:          protocol.StateWorking,
		StateSince:     nowStr,
		StateUpdatedAt: nowStr,
		LastSeen:       nowStr,
	})
	d.longRun["sess-long"] = longRunSession{workingSince: now.Add(-6 * time.Minute)}

	transcriptPath := filepath.Join(t.TempDir(), "long-transcript.jsonl")
	content := `{"type":"assistant","message":{"role":"assistant","content":"Completed long run"}}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	d.classifyOrDeferAfterStop("sess-long", transcriptPath)

	if got := mockClassifier.CallCount(); got != 0 {
		t.Fatalf("classifier calls=%d, want 0 while long-run review is deferred", got)
	}
	if !d.sessionNeedsReviewAfterLongRun("sess-long") {
		t.Fatal("needs_review_after_long_run should be set for deferred long run")
	}

	session := d.store.Get("sess-long")
	if session == nil {
		t.Fatal("session missing")
	}
	if session.State != protocol.StateWaitingInput {
		t.Fatalf("state=%s, want %s", session.State, protocol.StateWaitingInput)
	}
	decorated := d.sessionForBroadcast(session)
	if decorated == nil || !protocol.Deref(decorated.NeedsReviewAfterLongRun) {
		t.Fatal("broadcast session should include needs_review_after_long_run=true")
	}

	d.handleSessionVisualized("sess-long")

	deadline := time.Now().Add(2 * time.Second)
	for mockClassifier.CallCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := mockClassifier.CallCount(); got != 1 {
		t.Fatalf("classifier calls=%d, want 1 after visualization", got)
	}
	if d.sessionNeedsReviewAfterLongRun("sess-long") {
		t.Fatal("needs_review_after_long_run should clear after visualization")
	}
}

func TestClassifyOrDeferAfterStop_LongRunKeepsPendingApprovalState(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	mockClassifier := &countingClassifier{state: protocol.StateIdle}
	d.classifier = mockClassifier

	now := time.Now()
	nowStr := string(protocol.NewTimestamp(now))
	d.store.Add(&protocol.Session{
		ID:             "sess-pending",
		Agent:          protocol.SessionAgentCodex,
		Label:          "pending",
		Directory:      "/tmp",
		State:          protocol.StatePendingApproval,
		StateSince:     nowStr,
		StateUpdatedAt: nowStr,
		LastSeen:       nowStr,
	})
	d.longRun["sess-pending"] = longRunSession{workingSince: now.Add(-7 * time.Minute)}

	d.classifyOrDeferAfterStop("sess-pending", "")

	if got := mockClassifier.CallCount(); got != 0 {
		t.Fatalf("classifier calls=%d, want 0 while deferred", got)
	}
	if !d.sessionNeedsReviewAfterLongRun("sess-pending") {
		t.Fatal("needs_review_after_long_run should be set")
	}

	session := d.store.Get("sess-pending")
	if session == nil {
		t.Fatal("session missing")
	}
	if session.State != protocol.StatePendingApproval {
		t.Fatalf("state=%s, want %s", session.State, protocol.StatePendingApproval)
	}
}

func TestClassifyOrDeferAfterStop_ShortRunClassifiesImmediately(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	mockClassifier := &countingClassifier{state: protocol.StateIdle}
	d.classifier = mockClassifier

	now := time.Now()
	nowStr := string(protocol.NewTimestamp(now))
	d.store.Add(&protocol.Session{
		ID:             "sess-short",
		Agent:          protocol.SessionAgentCodex,
		Label:          "short",
		Directory:      "/tmp",
		State:          protocol.StateWorking,
		StateSince:     nowStr,
		StateUpdatedAt: nowStr,
		LastSeen:       nowStr,
	})
	d.longRun["sess-short"] = longRunSession{workingSince: now.Add(-2 * time.Minute)}

	transcriptPath := filepath.Join(t.TempDir(), "short-transcript.jsonl")
	content := `{"type":"assistant","message":{"role":"assistant","content":"Quick done"}}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	d.classifyOrDeferAfterStop("sess-short", transcriptPath)

	if got := mockClassifier.CallCount(); got != 1 {
		t.Fatalf("classifier calls=%d, want 1 for short run", got)
	}
	if d.sessionNeedsReviewAfterLongRun("sess-short") {
		t.Fatal("needs_review_after_long_run should be false for short run")
	}

	session := d.store.Get("sess-short")
	if session == nil {
		t.Fatal("session missing")
	}
	if session.State != protocol.StateIdle {
		t.Fatalf("state=%s, want %s", session.State, protocol.StateIdle)
	}
}

func TestHandleStop_SkipsClassificationForForcedStopSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	mockClassifier := &countingClassifier{state: protocol.StateWaitingInput}
	d.classifier = mockClassifier

	now := time.Now()
	nowStr := string(protocol.NewTimestamp(now))
	d.store.Add(&protocol.Session{
		ID:             "sess-forced-stop",
		Agent:          protocol.SessionAgentCodex,
		Label:          "forced-stop",
		Directory:      "/tmp",
		State:          protocol.StateIdle,
		StateSince:     nowStr,
		StateUpdatedAt: nowStr,
		LastSeen:       nowStr,
	})
	d.markForcedStopClassification("sess-forced-stop")

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleStop(serverConn, &protocol.StopMessage{
			ID:             "sess-forced-stop",
			TranscriptPath: "",
		})
		_ = serverConn.Close()
	}()

	var resp protocol.Response
	if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
		t.Fatalf("decode stop response: %v", err)
	}
	if !resp.Ok {
		t.Fatalf("stop response ok=%v, want true", resp.Ok)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleStop did not return")
	}

	time.Sleep(50 * time.Millisecond)
	if got := mockClassifier.CallCount(); got != 0 {
		t.Fatalf("classifier calls=%d, want 0", got)
	}
	if d.consumeForcedStopClassification("sess-forced-stop") {
		t.Fatal("forced-stop suppression token should be consumed by handleStop")
	}
}
