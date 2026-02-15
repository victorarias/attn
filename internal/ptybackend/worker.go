package ptybackend

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptyworker"
)

const (
	defaultRPCTimeout    = 5 * time.Second
	livenessRPCTimeout   = 2 * time.Second
	reclaimRPCTimeout    = 3 * time.Second
	pollerInterval       = 5 * time.Second
	monitorRetryInterval = 1 * time.Second
	monitorReadDeadline  = 2 * time.Second
	// Backoff after timeout errors to avoid CPU spin if reads repeatedly return
	// immediate timeouts.
	monitorTimeoutBackoff      = 25 * time.Millisecond
	monitorFastTimeoutAfter    = 50 * time.Millisecond
	monitorFastTimeoutLimit    = 20
	monitorFastTimeoutLogEvery = 5 * time.Second
	pollerFailureThreshold     = 3
	pollerUnreachableAfter     = 30 * time.Second
	spawnReadyTimeout          = 8 * time.Second
	spawnReadyPollInterval     = 100 * time.Millisecond
	spawnKillGracePeriod       = 1 * time.Second
	spawnWaitTimeout           = 500 * time.Millisecond
	probeTimeout               = 8 * time.Second
	streamEventBufferSize      = 256
	streamPreEventBufferCap    = 8
)

type WorkerBackendConfig struct {
	DataRoot         string
	DaemonInstanceID string
	BinaryPath       string
	OwnerPID         int
	OwnerStartedAt   string
	OwnerNonce       string
	Logf             func(format string, args ...interface{})
}

type workerSession struct {
	SessionID    string
	SocketPath   string
	RegistryPath string
	ControlToken string

	mu              sync.Mutex
	lastState       string
	exitNotified    bool
	unreachable     bool
	unreachableAt   time.Time
	pollFailures    int
	pollStop        chan struct{}
	pollDone        chan struct{}
	monitorStop     chan struct{}
	monitorDone     chan struct{}
	legacyLifecycle bool
}

type WorkerBackend struct {
	cfg WorkerBackendConfig

	ownerPID       int
	ownerStartedAt string
	ownerNonce     string

	mu       sync.RWMutex
	sessions map[string]*workerSession

	hooksMu sync.RWMutex
	onExit  func(ExitInfo)
	onState func(sessionID, state string)

	reqSeq atomic.Uint64
}

func (s *workerSession) notePollFailure(now time.Time) (logUnreachable bool, evict bool) {
	s.pollFailures++
	if s.pollFailures < pollerFailureThreshold {
		return false, false
	}
	s.pollFailures = 0
	if !s.unreachable {
		s.unreachable = true
		s.unreachableAt = now
		return true, false
	}
	if s.unreachableAt.IsZero() {
		s.unreachableAt = now
		return false, false
	}
	if now.Sub(s.unreachableAt) >= pollerUnreachableAfter {
		return false, true
	}
	return false, false
}

func (s *workerSession) notePollRecovery() {
	s.pollFailures = 0
	s.unreachable = false
	s.unreachableAt = time.Time{}
}

func NewWorker(cfg WorkerBackendConfig) (*WorkerBackend, error) {
	if strings.TrimSpace(cfg.DataRoot) == "" {
		return nil, fmt.Errorf("missing data root")
	}
	if strings.TrimSpace(cfg.DaemonInstanceID) == "" {
		return nil, fmt.Errorf("missing daemon instance id")
	}
	if strings.TrimSpace(cfg.BinaryPath) == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("resolve worker executable: %w", err)
		}
		cfg.BinaryPath = exe
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...interface{}) {}
	}
	if cfg.OwnerPID <= 0 {
		cfg.OwnerPID = os.Getpid()
	}
	cfg.OwnerStartedAt = strings.TrimSpace(cfg.OwnerStartedAt)
	if cfg.OwnerStartedAt == "" {
		cfg.OwnerStartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	cfg.OwnerNonce = strings.TrimSpace(cfg.OwnerNonce)
	if cfg.OwnerNonce == "" {
		nonce, err := randomToken(16)
		if err != nil {
			return nil, fmt.Errorf("generate daemon owner nonce: %w", err)
		}
		cfg.OwnerNonce = nonce
	}

	b := &WorkerBackend{
		cfg:            cfg,
		ownerPID:       cfg.OwnerPID,
		ownerStartedAt: cfg.OwnerStartedAt,
		ownerNonce:     cfg.OwnerNonce,
		sessions:       make(map[string]*workerSession),
	}
	if err := os.MkdirAll(b.registryDir(), 0700); err != nil {
		return nil, fmt.Errorf("create worker registry dir: %w", err)
	}
	if err := os.MkdirAll(b.sockDir(), 0700); err != nil {
		return nil, fmt.Errorf("create worker socket dir: %w", err)
	}
	if err := os.MkdirAll(b.quarantineDir(), 0700); err != nil {
		return nil, fmt.Errorf("create worker quarantine dir: %w", err)
	}
	if err := os.MkdirAll(b.logDir(), 0700); err != nil {
		return nil, fmt.Errorf("create worker log dir: %w", err)
	}
	return b, nil
}

func (b *WorkerBackend) SetExitHandler(handler func(ExitInfo)) {
	b.hooksMu.Lock()
	defer b.hooksMu.Unlock()
	b.onExit = handler
}

func (b *WorkerBackend) SetStateHandler(handler func(sessionID, state string)) {
	b.hooksMu.Lock()
	defer b.hooksMu.Unlock()
	b.onState = handler
}

func (b *WorkerBackend) Probe(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	suffix, err := randomToken(6)
	if err != nil {
		return fmt.Errorf("generate probe session id: %w", err)
	}
	probeSessionID := "probe-" + suffix
	spawnCtx, cancelSpawn := context.WithTimeout(ctx, probeTimeout)
	defer cancelSpawn()
	if err := b.Spawn(spawnCtx, SpawnOptions{
		ID:    probeSessionID,
		Agent: "shell",
		CWD:   os.TempDir(),
		Label: "attn-worker-probe",
		Cols:  80,
		Rows:  24,
	}); err != nil {
		return fmt.Errorf("spawn probe worker session: %w", err)
	}

	infoCtx, cancelInfo := context.WithTimeout(ctx, defaultRPCTimeout)
	info, infoErr := b.SessionInfo(infoCtx, probeSessionID)
	cancelInfo()

	cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), defaultRPCTimeout)
	removeErr := b.Remove(cleanupCtx, probeSessionID)
	cancelCleanup()

	if infoErr != nil {
		return fmt.Errorf("probe worker session info failed: %w", infoErr)
	}
	if !info.Running {
		return errors.New("probe worker session exited immediately")
	}
	if removeErr != nil {
		return fmt.Errorf("probe worker session cleanup failed: %w", removeErr)
	}
	return nil
}

func (b *WorkerBackend) Spawn(ctx context.Context, opts SpawnOptions) error {
	if err := validateSessionID(opts.ID); err != nil {
		return err
	}

	token, err := randomToken(32)
	if err != nil {
		return err
	}
	sessionID := opts.ID
	socketPath, err := b.expectedSocketPath(sessionID)
	if err != nil {
		return err
	}
	session := &workerSession{
		SessionID:    sessionID,
		SocketPath:   socketPath,
		RegistryPath: filepath.Join(b.registryDir(), sessionID+".json"),
		ControlToken: token,
	}

	b.mu.Lock()
	if _, exists := b.sessions[sessionID]; exists {
		b.mu.Unlock()
		return fmt.Errorf("session %s already exists", sessionID)
	}
	// Reserve the session ID early to avoid duplicate concurrent spawns.
	b.sessions[sessionID] = session
	b.mu.Unlock()
	spawnReady := false
	var workerProc *os.Process
	defer func() {
		if spawnReady {
			return
		}
		if workerProc != nil {
			b.stopSpawnedWorkerProcess(workerProc, sessionID)
		}
		b.mu.Lock()
		delete(b.sessions, sessionID)
		b.mu.Unlock()
	}()

	args := []string{
		"pty-worker",
		"--daemon-instance-id", b.cfg.DaemonInstanceID,
		"--session-id", sessionID,
		"--agent", opts.Agent,
		"--cwd", opts.CWD,
		"--cols", strconv.Itoa(int(opts.Cols)),
		"--rows", strconv.Itoa(int(opts.Rows)),
		"--registry-path", session.RegistryPath,
		"--socket-path", session.SocketPath,
		"--control-token", session.ControlToken,
		"--owner-pid", strconv.Itoa(b.ownerPID),
		"--owner-started-at", b.ownerStartedAt,
		"--owner-nonce", b.ownerNonce,
	}
	if opts.Label != "" {
		args = append(args, "--label", opts.Label)
	}
	if opts.ResumeSessionID != "" {
		args = append(args, "--resume-session-id", opts.ResumeSessionID)
	}
	if opts.ResumePicker {
		args = append(args, "--resume-picker")
	}
	if opts.ForkSession {
		args = append(args, "--fork-session")
	}
	if opts.ClaudeExecutable != "" {
		args = append(args, "--claude-executable", opts.ClaudeExecutable)
	}
	if opts.CodexExecutable != "" {
		args = append(args, "--codex-executable", opts.CodexExecutable)
	}
	if opts.CopilotExecutable != "" {
		args = append(args, "--copilot-executable", opts.CopilotExecutable)
	}

	cmd := exec.CommandContext(ctx, b.cfg.BinaryPath, args...)
	workerLogPath := filepath.Join(b.logDir(), sessionID+".log")
	workerLogFile, logErr := os.OpenFile(workerLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if logErr != nil {
		b.cfg.Logf("worker backend log open failed: session=%s path=%s err=%v", sessionID, workerLogPath, logErr)
		nullFile, _ := os.OpenFile(os.DevNull, os.O_RDWR, 0600)
		defer func() {
			if nullFile != nil {
				_ = nullFile.Close()
			}
		}()
		if nullFile != nil {
			cmd.Stdout = nullFile
			cmd.Stderr = nullFile
		}
	} else {
		cmd.Stdout = workerLogFile
		cmd.Stderr = workerLogFile
		defer func() {
			_ = workerLogFile.Close()
		}()
	}
	cmd.Env = append(os.Environ(), "ATTN_PTY_WORKER=1")

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start pty worker: %w", err)
	}
	workerProc = cmd.Process

	deadline := time.Now().Add(spawnReadyTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, err := b.callInfo(ctx, session)
		if err == nil {
			spawnReady = true
			if workerProc != nil {
				_ = workerProc.Release()
				workerProc = nil
			}
			b.startPoller(session)
			b.startMonitor(session)
			b.cfg.Logf("worker backend spawn ready: session=%s socket=%s", sessionID, session.SocketPath)
			return nil
		}
		lastErr = err
		time.Sleep(spawnReadyPollInterval)
	}
	return fmt.Errorf("worker did not become ready: %w", lastErr)
}

func (b *WorkerBackend) Attach(ctx context.Context, sessionID, subscriberID string) (AttachInfo, Stream, error) {
	session, err := b.getSession(sessionID)
	if err != nil {
		return AttachInfo{}, nil, err
	}
	rpcCtx, cancel := withDefaultRPCTimeout(ctx)
	defer cancel()
	conn, enc, dec, err := b.connectAuthed(rpcCtx, session)
	if err != nil {
		return AttachInfo{}, nil, err
	}
	if err := applyConnDeadline(conn, rpcCtx); err != nil {
		_ = conn.Close()
		return AttachInfo{}, nil, err
	}

	attachReqID := b.nextReqID("attach")
	if err := writeRequest(enc, attachReqID, ptyworker.MethodAttach, ptyworker.AttachParams{SubscriberID: subscriberID}); err != nil {
		_ = conn.Close()
		return AttachInfo{}, nil, err
	}

	preEvents := make([]OutputEvent, 0, streamPreEventBufferCap)
	var attachResult ptyworker.AttachResult
	for {
		frameType, res, evt, err := readFrame(dec)
		if err != nil {
			_ = conn.Close()
			return AttachInfo{}, nil, err
		}
		switch frameType {
		case "evt":
			if converted, ok := convertWorkerEvent(evt); ok {
				preEvents = appendCappedPreEvent(preEvents, converted, streamPreEventBufferCap)
			}
		case "res":
			if res.ID != attachReqID {
				continue
			}
			if !res.OK {
				_ = conn.Close()
				return AttachInfo{}, nil, b.rpcError(sessionID, res.Error)
			}
			if err := json.Unmarshal(res.Result, &attachResult); err != nil {
				_ = conn.Close()
				return AttachInfo{}, nil, fmt.Errorf("decode attach result: %w", err)
			}
			// Clear the RPC deadline before handing off to long-lived stream forwarding.
			_ = conn.SetDeadline(time.Time{})
			stream := newWorkerStream(conn, enc, dec, sessionID, b.nextReqID("detach"), preEvents)
			return AttachInfo{
				Scrollback:          attachResult.Scrollback,
				ScrollbackTruncated: attachResult.ScrollbackTruncated,
				LastSeq:             attachResult.LastSeq,
				Cols:                attachResult.Cols,
				Rows:                attachResult.Rows,
				PID:                 attachResult.PID,
				Running:             attachResult.Running,
				ExitCode:            attachResult.ExitCode,
				ExitSignal:          attachResult.ExitSignal,
				ScreenSnapshot:      attachResult.ScreenSnapshot,
				ScreenCols:          attachResult.ScreenCols,
				ScreenRows:          attachResult.ScreenRows,
				ScreenCursorX:       attachResult.ScreenCursorX,
				ScreenCursorY:       attachResult.ScreenCursorY,
				ScreenCursorVisible: attachResult.ScreenCursorVisible,
				ScreenSnapshotFresh: attachResult.ScreenSnapshotFresh,
			}, stream, nil
		}
	}
}

func appendCappedPreEvent(events []OutputEvent, evt OutputEvent, capLimit int) []OutputEvent {
	if capLimit <= 0 {
		return events
	}
	if len(events) < capLimit {
		return append(events, evt)
	}
	copy(events, events[1:])
	events[len(events)-1] = evt
	return events
}

func (b *WorkerBackend) Input(ctx context.Context, sessionID string, data []byte) error {
	session, err := b.getSession(sessionID)
	if err != nil {
		return err
	}
	payload := ptyworker.InputParams{Data: base64.StdEncoding.EncodeToString(data)}
	return b.callSimple(ctx, session, ptyworker.MethodInput, payload)
}

func (b *WorkerBackend) Resize(ctx context.Context, sessionID string, cols, rows uint16) error {
	session, err := b.getSession(sessionID)
	if err != nil {
		return err
	}
	return b.callSimple(ctx, session, ptyworker.MethodResize, ptyworker.ResizeParams{Cols: cols, Rows: rows})
}

func (b *WorkerBackend) Kill(ctx context.Context, sessionID string, sig syscall.Signal) error {
	session, err := b.getSession(sessionID)
	if err != nil {
		return err
	}
	return b.callSimple(ctx, session, ptyworker.MethodSignal, ptyworker.SignalParams{Signal: signalName(sig)})
}

func (b *WorkerBackend) Remove(ctx context.Context, sessionID string) error {
	session, err := b.getSession(sessionID)
	if err != nil {
		return err
	}
	callErr := b.callSimple(ctx, session, ptyworker.MethodRemove, map[string]any{})
	if callErr != nil {
		if errors.Is(callErr, pty.ErrSessionNotFound) || errors.Is(callErr, os.ErrNotExist) {
			b.stopMonitor(session)
			b.stopPoller(session)
			b.mu.Lock()
			delete(b.sessions, sessionID)
			b.mu.Unlock()
			b.pruneRegistryAndSocket(session.RegistryPath, session.SocketPath)
		}
		return callErr
	}
	b.stopMonitor(session)
	b.stopPoller(session)
	b.mu.Lock()
	delete(b.sessions, sessionID)
	b.mu.Unlock()
	return nil
}

func (b *WorkerBackend) SessionIDs(_ context.Context) []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	ids := make([]string, 0, len(b.sessions))
	for id := range b.sessions {
		ids = append(ids, id)
	}
	return ids
}

func (b *WorkerBackend) Recover(ctx context.Context) (RecoveryReport, error) {
	report := RecoveryReport{}
	// Best-effort: restore registries that were quarantined due to historical
	// socket-path validation changes. If they're now valid (new or legacy format),
	// move them back so daemon restarts can recover live sessions.
	b.restoreSocketMismatchQuarantine()

	files, err := filepath.Glob(filepath.Join(b.registryDir(), "*.json"))
	if err != nil {
		return report, err
	}

	for _, path := range files {
		if err := ctx.Err(); err != nil {
			return report, err
		}

		entry, err := ptyworker.ReadRegistry(path)
		if err != nil {
			report.Pruned++
			_ = os.Remove(path)
			continue
		}
		if entry.Version != 1 || entry.SessionID == "" || entry.SocketPath == "" {
			report.Pruned++
			_ = os.Remove(path)
			continue
		}
		if err := validateSessionID(entry.SessionID); err != nil {
			report.Pruned++
			_ = os.Remove(path)
			continue
		}
		expectedSocketPath, err := b.validateRegistrySocketPath(entry.SessionID, entry.SocketPath)
		if err != nil {
			report.Failed++
			b.quarantineRegistry(path, "socket_path_mismatch")
			// Do NOT remove entry.SocketPath here: a bad validation change could
			// otherwise unlink a live worker socket, permanently orphaning the session.
			// Best-effort cleanup only for the derived expected path.
			if expected, expectedErr := b.expectedSocketPath(entry.SessionID); expectedErr == nil {
				b.removeOwnedSocket(expected)
			}
			continue
		}
		if entry.DaemonInstanceID != b.cfg.DaemonInstanceID {
			reclaimed, reclaimErr := b.reclaimOwnershipMismatch(ctx, path, entry, expectedSocketPath)
			if reclaimed {
				report.Pruned++
				continue
			}
			report.Failed++
			b.quarantineRegistry(path, "ownership_mismatch")
			b.removeOwnedSocket(expectedSocketPath)
			if reclaimErr != nil {
				b.cfg.Logf(
					"worker recovery ownership mismatch for session %s (worker_pid=%d owner_pid=%d): preserving process after failed stale-owner reclaim: %v",
					entry.SessionID,
					entry.WorkerPID,
					entry.OwnerPID,
					reclaimErr,
				)
			} else {
				b.cfg.Logf(
					"worker recovery ownership mismatch for session %s (worker_pid=%d owner_pid=%d): preserving process because ownership lease is still active or unverifiable",
					entry.SessionID,
					entry.WorkerPID,
					entry.OwnerPID,
				)
			}
			continue
		}
		if !pidAlive(entry.WorkerPID) {
			report.Pruned++
			_ = os.Remove(path)
			_ = os.Remove(expectedSocketPath)
			continue
		}

		session := &workerSession{
			SessionID:    entry.SessionID,
			SocketPath:   expectedSocketPath,
			RegistryPath: path,
			ControlToken: entry.ControlToken,
		}
		if err := b.probeRecoveryInfo(ctx, session); err != nil {
			if errors.Is(err, pty.ErrSessionNotFound) || errors.Is(err, os.ErrNotExist) {
				report.Pruned++
				_ = os.Remove(path)
				_ = os.Remove(expectedSocketPath)
				continue
			}
			if isTransientRecoveryError(err) {
				report.Missing++
				b.cfg.Logf("worker recovery transient failure: session=%s err=%v", session.SessionID, err)
				continue
			}
			report.Failed++
			b.quarantineRegistry(path, "rpc_unavailable")
			continue
		}
		target := session
		b.mu.Lock()
		if existing := b.sessions[session.SessionID]; existing != nil {
			target = existing
		} else {
			b.sessions[session.SessionID] = session
		}
		b.mu.Unlock()
		b.startPoller(target)
		b.startMonitor(target)
		report.Recovered++
	}
	return report, nil
}

func (b *WorkerBackend) restoreSocketMismatchQuarantine() {
	pattern := filepath.Join(b.quarantineDir(), "*.socket_path_mismatch.*")
	files, err := filepath.Glob(pattern)
	if err != nil || len(files) == 0 {
		return
	}
	for _, quarantined := range files {
		entry, err := ptyworker.ReadRegistry(quarantined)
		if err != nil {
			continue
		}
		if entry.Version != 1 || entry.SessionID == "" || entry.SocketPath == "" {
			continue
		}
		if err := validateSessionID(entry.SessionID); err != nil {
			continue
		}
		// Only restore entries that belong to this daemon instance.
		if entry.DaemonInstanceID != b.cfg.DaemonInstanceID {
			continue
		}
		// Only restore if the socket path is now considered valid.
		if _, err := b.validateRegistrySocketPath(entry.SessionID, entry.SocketPath); err != nil {
			continue
		}
		dest := filepath.Join(b.registryDir(), entry.SessionID+".json")
		if _, err := os.Stat(dest); err == nil {
			// Registry already exists; keep quarantine artifact for inspection.
			continue
		}
		if err := os.Rename(quarantined, dest); err != nil {
			continue
		}
		b.cfg.Logf("worker registry restored from quarantine: session=%s dest=%s", entry.SessionID, dest)
	}
}

func (b *WorkerBackend) SessionInfo(ctx context.Context, sessionID string) (SessionInfo, error) {
	session, err := b.getSession(sessionID)
	if err != nil {
		return SessionInfo{}, err
	}
	info, err := b.callInfo(ctx, session)
	if err != nil {
		return SessionInfo{}, err
	}
	return SessionInfo{
		SessionID:  sessionID,
		Agent:      info.Agent,
		CWD:        info.CWD,
		Running:    info.Running,
		State:      info.State,
		Cols:       info.Cols,
		Rows:       info.Rows,
		PID:        info.ChildPID,
		LastSeq:    info.LastSeq,
		ExitCode:   info.ExitCode,
		ExitSignal: info.ExitSignal,
	}, nil
}

func (b *WorkerBackend) SessionLikelyAlive(ctx context.Context, sessionID string) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateSessionID(sessionID); err != nil {
		return false, nil
	}
	registryPath := filepath.Join(b.registryDir(), sessionID+".json")
	entry, err := ptyworker.ReadRegistry(registryPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("read worker registry for %s: %w", sessionID, err)
	}
	if entry.SessionID != sessionID || entry.DaemonInstanceID != b.cfg.DaemonInstanceID {
		return false, nil
	}
	socketPath, err := b.validateRegistrySocketPath(sessionID, entry.SocketPath)
	if err != nil {
		return false, nil
	}
	if !pidAlive(entry.WorkerPID) {
		return false, nil
	}
	if _, err := os.Stat(socketPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stat worker socket for %s: %w", sessionID, err)
	}
	probeCtx, cancel := context.WithTimeout(ctx, livenessRPCTimeout)
	defer cancel()
	session := &workerSession{
		SessionID:    sessionID,
		SocketPath:   socketPath,
		RegistryPath: registryPath,
		ControlToken: entry.ControlToken,
	}
	if err := b.callSimple(probeCtx, session, ptyworker.MethodHealth, map[string]any{}); err != nil {
		if errors.Is(err, pty.ErrSessionNotFound) || errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("worker liveness probe failed for %s: %w", sessionID, err)
	}
	return true, nil
}

func (b *WorkerBackend) Shutdown(_ context.Context) error {
	b.mu.RLock()
	sessions := make([]*workerSession, 0, len(b.sessions))
	for _, s := range b.sessions {
		sessions = append(sessions, s)
	}
	b.mu.RUnlock()
	var wg sync.WaitGroup
	for _, s := range sessions {
		wg.Add(1)
		go func(session *workerSession) {
			defer wg.Done()
			b.stopMonitor(session)
			b.stopPoller(session)
		}(s)
	}
	wg.Wait()
	return nil
}

func (b *WorkerBackend) workerRoot() string {
	return filepath.Join(b.cfg.DataRoot, "workers", b.cfg.DaemonInstanceID)
}

func (b *WorkerBackend) registryDir() string {
	return filepath.Join(b.workerRoot(), "registry")
}

func (b *WorkerBackend) sockDir() string {
	return filepath.Join(b.workerRoot(), "sock")
}

func (b *WorkerBackend) quarantineDir() string {
	return filepath.Join(b.workerRoot(), "quarantine")
}

func (b *WorkerBackend) logDir() string {
	return filepath.Join(b.workerRoot(), "log")
}

func (b *WorkerBackend) nextReqID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, b.reqSeq.Add(1))
}

func (b *WorkerBackend) getSession(sessionID string) (*workerSession, error) {
	if err := validateSessionID(sessionID); err != nil {
		return nil, fmt.Errorf("%w: %s", pty.ErrSessionNotFound, sessionID)
	}

	b.mu.RLock()
	session := b.sessions[sessionID]
	b.mu.RUnlock()
	if session != nil {
		return session, nil
	}

	registryPath := filepath.Join(b.registryDir(), sessionID+".json")
	entry, err := ptyworker.ReadRegistry(registryPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			b.cfg.Logf("worker backend getSession: read registry failed: session=%s err=%v", sessionID, err)
		}
		return nil, fmt.Errorf("%w: %s", pty.ErrSessionNotFound, sessionID)
	}
	if entry.SessionID != sessionID {
		return nil, fmt.Errorf("%w: %s", pty.ErrSessionNotFound, sessionID)
	}
	if entry.DaemonInstanceID != b.cfg.DaemonInstanceID {
		return nil, fmt.Errorf("%w: %s", pty.ErrSessionNotFound, sessionID)
	}
	socketPath, err := b.validateRegistrySocketPath(sessionID, entry.SocketPath)
	if err != nil {
		b.quarantineRegistry(registryPath, "socket_path_mismatch")
		return nil, fmt.Errorf("%w: %s", pty.ErrSessionNotFound, sessionID)
	}
	if !pidAlive(entry.WorkerPID) {
		b.pruneRegistryAndSocket(registryPath, socketPath)
		return nil, fmt.Errorf("%w: %s", pty.ErrSessionNotFound, sessionID)
	}
	if _, err := os.Stat(socketPath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			b.cfg.Logf("worker backend getSession: stat socket failed: session=%s path=%s err=%v", sessionID, socketPath, err)
		}
		b.pruneRegistryAndSocket(registryPath, socketPath)
		return nil, fmt.Errorf("%w: %s", pty.ErrSessionNotFound, sessionID)
	}
	session = &workerSession{
		SessionID:    entry.SessionID,
		SocketPath:   socketPath,
		RegistryPath: registryPath,
		ControlToken: entry.ControlToken,
	}
	probeCtx, cancel := context.WithTimeout(context.Background(), livenessRPCTimeout)
	probeErr := b.probeRecoveryInfo(probeCtx, session)
	cancel()
	if probeErr != nil {
		if errors.Is(probeErr, pty.ErrSessionNotFound) || errors.Is(probeErr, os.ErrNotExist) {
			b.pruneRegistryAndSocket(registryPath, socketPath)
			return nil, fmt.Errorf("%w: %s", pty.ErrSessionNotFound, sessionID)
		}
		return nil, probeErr
	}
	b.mu.Lock()
	if existing := b.sessions[sessionID]; existing != nil {
		session = existing
	} else {
		b.sessions[sessionID] = session
		b.startPoller(session)
		b.startMonitor(session)
	}
	b.mu.Unlock()
	return session, nil
}

func (b *WorkerBackend) callSimple(ctx context.Context, session *workerSession, method string, params any) error {
	return b.callSimpleWithIdentity(ctx, session, b.cfg.DaemonInstanceID, session.ControlToken, method, params)
}

func (b *WorkerBackend) callSimpleWithIdentity(
	ctx context.Context,
	session *workerSession,
	daemonInstanceID string,
	controlToken string,
	method string,
	params any,
) error {
	rpcCtx, cancel := withDefaultRPCTimeout(ctx)
	defer cancel()
	conn, enc, dec, err := b.connectWithIdentity(rpcCtx, session, daemonInstanceID, controlToken)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := applyConnDeadline(conn, rpcCtx); err != nil {
		return err
	}

	reqID := b.nextReqID(method)
	if err := writeRequest(enc, reqID, method, params); err != nil {
		return err
	}
	for {
		frameType, res, _, err := readFrame(dec)
		if err != nil {
			return err
		}
		if frameType != "res" || res.ID != reqID {
			continue
		}
		if !res.OK {
			return b.rpcError(session.SessionID, res.Error)
		}
		return nil
	}
}

func (b *WorkerBackend) callInfo(ctx context.Context, session *workerSession) (ptyworker.InfoResult, error) {
	rpcCtx, cancel := withDefaultRPCTimeout(ctx)
	defer cancel()
	conn, enc, dec, err := b.connectAuthed(rpcCtx, session)
	if err != nil {
		return ptyworker.InfoResult{}, err
	}
	defer conn.Close()
	if err := applyConnDeadline(conn, rpcCtx); err != nil {
		return ptyworker.InfoResult{}, err
	}

	reqID := b.nextReqID("info")
	if err := writeRequest(enc, reqID, ptyworker.MethodInfo, map[string]any{}); err != nil {
		return ptyworker.InfoResult{}, err
	}
	for {
		frameType, res, _, err := readFrame(dec)
		if err != nil {
			return ptyworker.InfoResult{}, err
		}
		if frameType != "res" || res.ID != reqID {
			continue
		}
		if !res.OK {
			return ptyworker.InfoResult{}, b.rpcError(session.SessionID, res.Error)
		}
		var info ptyworker.InfoResult
		if err := json.Unmarshal(res.Result, &info); err != nil {
			return ptyworker.InfoResult{}, err
		}
		return info, nil
	}
}

func (b *WorkerBackend) connectAuthed(ctx context.Context, session *workerSession) (net.Conn, *json.Encoder, *json.Decoder, error) {
	return b.connectWithIdentity(ctx, session, b.cfg.DaemonInstanceID, session.ControlToken)
}

func (b *WorkerBackend) connectWithIdentity(
	ctx context.Context,
	session *workerSession,
	daemonInstanceID string,
	controlToken string,
) (net.Conn, *json.Encoder, *json.Decoder, error) {
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(ctx, "unix", session.SocketPath)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := applyConnDeadline(conn, ctx); err != nil {
		_ = conn.Close()
		return nil, nil, nil, err
	}
	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(conn)
	helloID := b.nextReqID("hello")
	helloParams := ptyworker.HelloParams{
		RPCMajor:         ptyworker.RPCMajor,
		RPCMinor:         ptyworker.RPCMinor,
		DaemonInstanceID: daemonInstanceID,
		ControlToken:     controlToken,
	}
	if err := writeRequest(enc, helloID, ptyworker.MethodHello, helloParams); err != nil {
		_ = conn.Close()
		return nil, nil, nil, err
	}
	for {
		frameType, res, _, err := readFrame(dec)
		if err != nil {
			_ = conn.Close()
			return nil, nil, nil, err
		}
		if frameType != "res" || res.ID != helloID {
			continue
		}
		if !res.OK {
			_ = conn.Close()
			return nil, nil, nil, b.rpcError(session.SessionID, res.Error)
		}
		var hello ptyworker.HelloResult
		if err := json.Unmarshal(res.Result, &hello); err != nil {
			_ = conn.Close()
			return nil, nil, nil, fmt.Errorf("decode hello result: %w", err)
		}
		if !ptyworker.IsCompatibleVersion(hello.RPCMajor, hello.RPCMinor) {
			_ = conn.Close()
			return nil, nil, nil, fmt.Errorf(
				"worker rpc version incompatible: got=%d.%d supported=%d.%d..%d.%d",
				hello.RPCMajor, hello.RPCMinor,
				ptyworker.RPCMajor, ptyworker.MinCompatibleRPCMinor,
				ptyworker.RPCMajor, ptyworker.RPCMinor,
			)
		}
		if hello.DaemonInstanceID != daemonInstanceID || hello.SessionID != session.SessionID {
			_ = conn.Close()
			return nil, nil, nil, errors.New("worker identity mismatch")
		}
		break
	}
	// Clear handshake deadline. Callers can apply method-specific deadlines.
	_ = conn.SetDeadline(time.Time{})
	return conn, enc, dec, nil
}

func (b *WorkerBackend) rpcError(sessionID string, rpcErr *ptyworker.RPCError) error {
	if rpcErr == nil {
		return errors.New("worker rpc error")
	}
	switch rpcErr.Code {
	case ptyworker.ErrSessionNotFound:
		return fmt.Errorf("%w: %s", pty.ErrSessionNotFound, sessionID)
	case ptyworker.ErrSessionNotRunning:
		return errors.New(rpcErr.Message)
	default:
		return errors.New(rpcErr.Message)
	}
}

func unixSocketPathLimit() int {
	// sockaddr_un.sun_path is 104 bytes on Darwin, 108 bytes on Linux.
	// Keep 1 byte of slack for a trailing NUL.
	switch runtime.GOOS {
	case "linux":
		return 108
	case "darwin":
		return 104
	default:
		return 104
	}
}

func unixSocketPathFits(path string) bool {
	limit := unixSocketPathLimit()
	if limit <= 1 {
		return false
	}
	return len(path) <= limit-1
}

func (b *WorkerBackend) expectedSocketPath(sessionID string) (string, error) {
	root := b.sockDir()

	// Use a deterministic hash filename to stay within the unix socket path
	// limit. This matters on macOS where $HOME can be long and session IDs are
	// UUIDs.
	sum := sha256.Sum256([]byte(sessionID))
	hash := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:])

	// Full path is: root + "/" + base. Compute available chars for base.
	avail := unixSocketPathLimit() - 1 - len(root) - 1
	if avail <= len(".sock") {
		return "", fmt.Errorf("unix socket directory path too long: %s", root)
	}
	keep := avail - len(".sock")
	ext := ".sock"
	// If we're very constrained, prefer a shorter extension to free up entropy.
	if keep < 5 {
		ext = ".s"
		keep = avail - len(ext)
	}
	if keep <= 0 {
		return "", fmt.Errorf("unix socket path too constrained for session %s (dir=%s)", sessionID, root)
	}
	if keep > len(hash) {
		keep = len(hash)
	}
	// If we can't fit even ~25 bits of hash, don't risk collisions.
	if keep < 5 {
		return "", fmt.Errorf("unix socket path too constrained for session %s (dir=%s)", sessionID, root)
	}

	path := filepath.Join(root, hash[:keep]+ext)
	if !unixSocketPathFits(path) {
		return "", fmt.Errorf("unix socket path too long: %s", path)
	}
	return path, nil
}

// legacyExpectedSocketPath matches the pre-base32 socket naming format used by older
// attn versions (prefix "h-" + hex sha256).
//
// This exists to support daemon restarts/upgrades without breaking live workers
// whose registry entries still point at the legacy socket filename.
func (b *WorkerBackend) legacyExpectedSocketPath(sessionID string) (string, error) {
	root := b.sockDir()

	sum := sha256.Sum256([]byte(sessionID))
	hexHash := hex.EncodeToString(sum[:])

	avail := unixSocketPathLimit() - 1 - len(root) - 1
	if avail <= len("h-")+len(".sock") {
		return "", fmt.Errorf("unix socket directory path too long: %s", root)
	}
	ext := ".sock"
	keep := avail - len("h-") - len(ext)
	if keep < 5 {
		ext = ".s"
		keep = avail - len("h-") - len(ext)
	}
	if keep <= 0 {
		return "", fmt.Errorf("unix socket path too constrained for session %s (dir=%s)", sessionID, root)
	}
	if keep > len(hexHash) {
		keep = len(hexHash)
	}
	// If we can't fit even ~20 bits of hash, don't risk collisions.
	if keep < 5 {
		return "", fmt.Errorf("unix socket path too constrained for session %s (dir=%s)", sessionID, root)
	}

	path := filepath.Join(root, "h-"+hexHash[:keep]+ext)
	if !unixSocketPathFits(path) {
		return "", fmt.Errorf("unix socket path too long: %s", path)
	}
	return path, nil
}

func (b *WorkerBackend) validateRegistrySocketPath(sessionID, socketPath string) (string, error) {
	expected, err := b.expectedSocketPath(sessionID)
	if err != nil {
		return "", err
	}
	clean := filepath.Clean(socketPath)
	if clean == filepath.Clean(expected) {
		return expected, nil
	}

	legacy, legacyErr := b.legacyExpectedSocketPath(sessionID)
	if legacyErr == nil && clean == filepath.Clean(legacy) {
		return legacy, nil
	}
	return "", fmt.Errorf("unexpected socket path for session %s", sessionID)
}

func (b *WorkerBackend) reclaimOwnershipMismatch(ctx context.Context, registryPath string, entry ptyworker.RegistryEntry, expectedSocketPath string) (bool, error) {
	if !b.canReclaimOwnershipMismatch(entry) {
		return false, nil
	}
	if strings.TrimSpace(entry.ControlToken) == "" {
		return false, errors.New("missing control token for stale-owner reclaim")
	}

	session := &workerSession{
		SessionID:    entry.SessionID,
		SocketPath:   expectedSocketPath,
		RegistryPath: registryPath,
		ControlToken: entry.ControlToken,
	}
	removeCtx := ctx
	var cancel context.CancelFunc
	if removeCtx == nil {
		removeCtx = context.Background()
	}
	if _, hasDeadline := removeCtx.Deadline(); !hasDeadline {
		removeCtx, cancel = context.WithTimeout(removeCtx, reclaimRPCTimeout)
	} else {
		removeCtx, cancel = context.WithCancel(removeCtx)
	}
	defer cancel()

	err := b.callSimpleWithIdentity(
		removeCtx,
		session,
		entry.DaemonInstanceID,
		entry.ControlToken,
		ptyworker.MethodRemove,
		map[string]any{},
	)
	if err != nil {
		if errors.Is(err, pty.ErrSessionNotFound) || errors.Is(err, os.ErrNotExist) || !pidAlive(entry.WorkerPID) {
			b.pruneRegistryAndSocket(registryPath, expectedSocketPath)
			b.cfg.Logf(
				"worker recovery ownership mismatch for session %s: stale owner reclaimed after terminal worker absence (owner_pid=%d worker_pid=%d)",
				entry.SessionID,
				entry.OwnerPID,
				entry.WorkerPID,
			)
			return true, nil
		}
		return false, fmt.Errorf("stale-owner reclaim remove rpc failed: %w", err)
	}

	b.pruneRegistryAndSocket(registryPath, expectedSocketPath)
	b.cfg.Logf(
		"worker recovery ownership mismatch for session %s: reclaimed stale worker via authenticated remove (owner_pid=%d worker_pid=%d)",
		entry.SessionID,
		entry.OwnerPID,
		entry.WorkerPID,
	)
	return true, nil
}

func (b *WorkerBackend) canReclaimOwnershipMismatch(entry ptyworker.RegistryEntry) bool {
	if entry.OwnerPID <= 0 {
		return false
	}
	if strings.TrimSpace(entry.OwnerStartedAt) == "" {
		return false
	}
	if strings.TrimSpace(entry.OwnerNonce) == "" {
		return false
	}
	// If this registry entry was written by the current daemon process, mismatch metadata is stale.
	if entry.OwnerPID == b.ownerPID && entry.OwnerNonce == b.ownerNonce {
		return true
	}
	// Otherwise only reclaim when the recorded owner process is definitely gone.
	return !pidAlive(entry.OwnerPID)
}

func (b *WorkerBackend) stopSpawnedWorkerProcess(proc *os.Process, sessionID string) {
	if proc == nil {
		return
	}
	if pidAlive(proc.Pid) {
		_ = proc.Signal(syscall.SIGTERM)
		deadline := time.Now().Add(spawnKillGracePeriod)
		for time.Now().Before(deadline) {
			if !pidAlive(proc.Pid) {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		if pidAlive(proc.Pid) {
			_ = proc.Kill()
		}
	}
	waitDone := make(chan struct{})
	go func() {
		_, _ = proc.Wait()
		close(waitDone)
	}()
	select {
	case <-waitDone:
	case <-time.After(spawnWaitTimeout):
		_ = proc.Release()
	}
	b.cfg.Logf("worker backend spawn cleanup: terminated unready worker: session=%s pid=%d", sessionID, proc.Pid)
}

func (b *WorkerBackend) workerProcessAlive(session *workerSession) bool {
	alive, err := b.SessionLikelyAlive(context.Background(), session.SessionID)
	if err != nil {
		b.cfg.Logf("worker backend liveness probe inconclusive for session %s: %v", session.SessionID, err)
		// Poller eviction is destructive; treat unknown as alive and retry later.
		return true
	}
	return alive
}

func (b *WorkerBackend) pruneRegistryAndSocket(registryPath, socketPath string) {
	_ = os.Remove(registryPath)
	_ = os.Remove(socketPath)
}

func (b *WorkerBackend) forceSessionEviction(session *workerSession) {
	b.stopMonitor(session)
	b.mu.Lock()
	delete(b.sessions, session.SessionID)
	b.mu.Unlock()
	b.pruneRegistryAndSocket(session.RegistryPath, session.SocketPath)
}

func (b *WorkerBackend) removeOwnedSocket(socketPath string) {
	cleanPath := filepath.Clean(strings.TrimSpace(socketPath))
	if cleanPath == "" || cleanPath == "." {
		return
	}
	sockRoot := filepath.Clean(b.sockDir())
	rel, err := filepath.Rel(sockRoot, cleanPath)
	if err != nil {
		return
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return
	}
	_ = os.Remove(cleanPath)
}

func (b *WorkerBackend) startPoller(session *workerSession) {
	session.mu.Lock()
	if session.pollStop != nil {
		session.mu.Unlock()
		return
	}
	session.pollStop = make(chan struct{})
	session.pollDone = make(chan struct{})
	stopCh := session.pollStop
	doneCh := session.pollDone
	session.mu.Unlock()

	go func() {
		defer close(doneCh)
		ticker := time.NewTicker(pollerInterval)
		defer ticker.Stop()

		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				callCtx, cancel := withDefaultRPCTimeout(context.Background())
				session.mu.Lock()
				legacyLifecycle := session.legacyLifecycle
				session.mu.Unlock()

				var (
					info ptyworker.InfoResult
					err  error
				)
				if legacyLifecycle {
					info, err = b.callInfo(callCtx, session)
				} else {
					err = b.callSimple(callCtx, session, ptyworker.MethodHealth, map[string]any{})
				}
				cancel()
				if err != nil {
					now := time.Now()
					session.mu.Lock()
					shouldLogUnreachable, shouldEvict := session.notePollFailure(now)
					session.mu.Unlock()
					if shouldLogUnreachable {
						b.cfg.Logf("worker backend poller: session %s temporarily unreachable", session.SessionID)
					}
					if shouldEvict {
						if b.workerProcessAlive(session) {
							b.cfg.Logf("worker backend poller: session %s still has live worker pid; deferring forced exit", session.SessionID)
							session.mu.Lock()
							session.notePollRecovery()
							session.mu.Unlock()
							continue
						}
						b.cfg.Logf("worker backend poller: session %s unreachable for %s; forcing exit", session.SessionID, pollerUnreachableAfter)
						b.hooksMu.RLock()
						onExit := b.onExit
						b.hooksMu.RUnlock()
						if onExit != nil {
							go onExit(ExitInfo{ID: session.SessionID, ExitCode: 1, Signal: "worker_unreachable"})
						}
						b.forceSessionEviction(session)
						return
					}
					continue
				}
				session.mu.Lock()
				session.notePollRecovery()
				session.mu.Unlock()

				if !legacyLifecycle {
					continue
				}

				var (
					stateChanged bool
					newState     string
					exitNow      bool
					exitCode     int
					exitSignal   string
				)
				session.mu.Lock()
				stateChanged = info.State != "" && info.State != session.lastState
				if stateChanged {
					session.lastState = info.State
					newState = info.State
				}
				exitNow = !info.Running && !session.exitNotified
				if exitNow {
					session.exitNotified = true
					if info.ExitCode != nil {
						exitCode = *info.ExitCode
					}
					if info.ExitSignal != nil {
						exitSignal = *info.ExitSignal
					}
				}
				session.mu.Unlock()

				if stateChanged {
					b.hooksMu.RLock()
					onState := b.onState
					b.hooksMu.RUnlock()
					if onState != nil {
						onState(session.SessionID, newState)
					}
				}

				if exitNow {
					b.hooksMu.RLock()
					onExit := b.onExit
					b.hooksMu.RUnlock()
					if onExit != nil {
						go onExit(ExitInfo{ID: session.SessionID, ExitCode: exitCode, Signal: exitSignal})
					}
				}
			}
		}
	}()
}

var errLifecycleWatchUnsupported = errors.New("worker lifecycle watch unsupported")
var errLifecycleWatchTimeoutLoop = errors.New("worker lifecycle watch timeout loop")

type monitorTimeoutGuard struct {
	consecutiveFastTimeouts int
	lastFastTimeoutLog      time.Time
}

func (g *monitorTimeoutGuard) reset() {
	g.consecutiveFastTimeouts = 0
}

// onTimeout returns (backoff, abortErr). backoff is non-zero only for fast timeout loops.
func (g *monitorTimeoutGuard) onTimeout(sessionID string, dt time.Duration, err error, logf func(format string, args ...interface{})) (time.Duration, error) {
	if dt > monitorFastTimeoutAfter {
		g.consecutiveFastTimeouts = 0
		return 0, nil
	}

	g.consecutiveFastTimeouts++

	now := time.Now()
	if now.Sub(g.lastFastTimeoutLog) >= monitorFastTimeoutLogEvery {
		logf(
			"worker backend lifecycle watch: fast timeout loop session=%s consecutive=%d dt=%s err=%v",
			sessionID,
			g.consecutiveFastTimeouts,
			dt,
			err,
		)
		g.lastFastTimeoutLog = now
	}

	if g.consecutiveFastTimeouts >= monitorFastTimeoutLimit {
		// Treat a fast-timeout loop as a broken watch stream. Degrade to poll-based lifecycle.
		return 0, fmt.Errorf("%w: session=%s: %v", errLifecycleWatchTimeoutLoop, sessionID, err)
	}
	return monitorTimeoutBackoff, nil
}

func (b *WorkerBackend) startMonitor(session *workerSession) {
	session.mu.Lock()
	if session.monitorStop != nil || session.legacyLifecycle {
		session.mu.Unlock()
		return
	}
	session.monitorStop = make(chan struct{})
	session.monitorDone = make(chan struct{})
	stopCh := session.monitorStop
	doneCh := session.monitorDone
	session.mu.Unlock()

	go func() {
		defer close(doneCh)
		for {
			select {
			case <-stopCh:
				return
			default:
			}

			err := b.runLifecycleMonitor(session, stopCh)
			if err == nil {
				return
			}
			if errors.Is(err, errLifecycleWatchUnsupported) {
				session.mu.Lock()
				session.legacyLifecycle = true
				session.mu.Unlock()
				b.cfg.Logf("worker backend lifecycle watch unsupported for session %s; falling back to poll-based lifecycle", session.SessionID)
				return
			}
			if errors.Is(err, errLifecycleWatchTimeoutLoop) {
				session.mu.Lock()
				session.legacyLifecycle = true
				session.mu.Unlock()
				b.cfg.Logf("worker backend lifecycle watch unreliable for session %s; falling back to poll-based lifecycle", session.SessionID)
				return
			}
			b.cfg.Logf("worker backend lifecycle watch disconnected for session %s: %v", session.SessionID, err)

			select {
			case <-stopCh:
				return
			case <-time.After(monitorRetryInterval):
			}
		}
	}()
}

func (b *WorkerBackend) runLifecycleMonitor(session *workerSession, stopCh <-chan struct{}) error {
	callCtx, cancel := withDefaultRPCTimeout(context.Background())
	conn, enc, dec, err := b.connectAuthed(callCtx, session)
	cancel()
	if err != nil {
		return err
	}
	defer conn.Close()

	guard := &monitorTimeoutGuard{}

	watchReqID := b.nextReqID("watch")
	if err := writeRequest(enc, watchReqID, ptyworker.MethodWatch, map[string]any{}); err != nil {
		return err
	}

	for {
		select {
		case <-stopCh:
			return nil
		default:
		}
		// If reads return immediate timeouts, the loop can spin CPU.
		if err := conn.SetReadDeadline(time.Now().Add(monitorReadDeadline)); err != nil {
			return err
		}
		readStart := time.Now()
		frameType, res, _, err := readFrame(dec)
		if err != nil {
			var netErr net.Error
			// Fast-path: avoid reflect-heavy errors.As on the hot timeout path.
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				dt := time.Since(readStart)
				backoff, terr := guard.onTimeout(session.SessionID, dt, err, b.cfg.Logf)
				if terr != nil {
					return terr
				}
				if backoff > 0 {
					time.Sleep(backoff)
				}
				continue
			}
			if errors.As(err, &netErr) && netErr.Timeout() {
				dt := time.Since(readStart)
				backoff, terr := guard.onTimeout(session.SessionID, dt, err, b.cfg.Logf)
				if terr != nil {
					return terr
				}
				if backoff > 0 {
					time.Sleep(backoff)
				}
				continue
			}
			return err
		}
		guard.reset()
		if frameType != "res" || res.ID != watchReqID {
			continue
		}
		if !res.OK {
			if isLifecycleWatchUnsupported(res.Error) {
				return errLifecycleWatchUnsupported
			}
			return b.rpcError(session.SessionID, res.Error)
		}
		break
	}

	for {
		select {
		case <-stopCh:
			return nil
		default:
		}
		// If reads return immediate timeouts, the loop can spin CPU.
		if err := conn.SetReadDeadline(time.Now().Add(monitorReadDeadline)); err != nil {
			return err
		}
		readStart := time.Now()
		frameType, _, evt, err := readFrame(dec)
		if err != nil {
			var netErr net.Error
			// Fast-path: avoid reflect-heavy errors.As on the hot timeout path.
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				dt := time.Since(readStart)
				backoff, terr := guard.onTimeout(session.SessionID, dt, err, b.cfg.Logf)
				if terr != nil {
					return terr
				}
				if backoff > 0 {
					time.Sleep(backoff)
				}
				continue
			}
			if errors.As(err, &netErr) && netErr.Timeout() {
				dt := time.Since(readStart)
				backoff, terr := guard.onTimeout(session.SessionID, dt, err, b.cfg.Logf)
				if terr != nil {
					return terr
				}
				if backoff > 0 {
					time.Sleep(backoff)
				}
				continue
			}
			return err
		}
		guard.reset()
		if frameType != "evt" {
			continue
		}
		b.handleLifecycleEvent(session, evt)
	}
}

func (b *WorkerBackend) handleLifecycleEvent(session *workerSession, evt ptyworker.EventEnvelope) {
	switch evt.Event {
	case ptyworker.EventStateChanged:
		if evt.State == nil {
			return
		}
		state := strings.TrimSpace(*evt.State)
		if state == "" {
			return
		}
		session.mu.Lock()
		if state == session.lastState {
			session.mu.Unlock()
			return
		}
		session.lastState = state
		session.mu.Unlock()

		b.hooksMu.RLock()
		onState := b.onState
		b.hooksMu.RUnlock()
		if onState != nil {
			onState(session.SessionID, state)
		}
	case ptyworker.EventExit:
		session.mu.Lock()
		if session.exitNotified {
			session.mu.Unlock()
			return
		}
		session.exitNotified = true
		session.mu.Unlock()

		exitCode := 0
		if evt.ExitCode != nil {
			exitCode = *evt.ExitCode
		}
		exitSignal := ""
		if evt.ExitSignal != nil {
			exitSignal = *evt.ExitSignal
		}
		b.hooksMu.RLock()
		onExit := b.onExit
		b.hooksMu.RUnlock()
		if onExit != nil {
			go onExit(ExitInfo{ID: session.SessionID, ExitCode: exitCode, Signal: exitSignal})
		}
	}
}

func isLifecycleWatchUnsupported(rpcErr *ptyworker.RPCError) bool {
	if rpcErr == nil {
		return false
	}
	if rpcErr.Code == ptyworker.ErrUnsupportedVersion {
		return true
	}
	if rpcErr.Code != ptyworker.ErrBadRequest {
		return false
	}
	return strings.Contains(strings.ToLower(rpcErr.Message), "unknown method")
}

func (b *WorkerBackend) stopMonitor(session *workerSession) {
	session.mu.Lock()
	stopCh := session.monitorStop
	doneCh := session.monitorDone
	session.monitorStop = nil
	session.monitorDone = nil
	session.mu.Unlock()

	if stopCh != nil {
		close(stopCh)
	}
	if doneCh != nil {
		<-doneCh
	}
}

func (b *WorkerBackend) stopPoller(session *workerSession) {
	session.mu.Lock()
	stopCh := session.pollStop
	doneCh := session.pollDone
	session.pollStop = nil
	session.pollDone = nil
	session.mu.Unlock()

	if stopCh != nil {
		close(stopCh)
	}
	if doneCh != nil {
		<-doneCh
	}
}

func writeRequest(enc *json.Encoder, id, method string, params any) error {
	payload, err := json.Marshal(params)
	if err != nil {
		return err
	}
	if string(payload) == "null" {
		payload = nil
	}
	return enc.Encode(ptyworker.RequestEnvelope{
		Type:   "req",
		ID:     id,
		Method: method,
		Params: payload,
	})
}

func readFrame(dec *json.Decoder) (string, ptyworker.ResponseEnvelope, ptyworker.EventEnvelope, error) {
	var raw map[string]json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return "", ptyworker.ResponseEnvelope{}, ptyworker.EventEnvelope{}, err
	}
	var typ string
	if t, ok := raw["type"]; ok {
		_ = json.Unmarshal(t, &typ)
	}
	switch typ {
	case "res":
		data, _ := json.Marshal(raw)
		var res ptyworker.ResponseEnvelope
		if err := json.Unmarshal(data, &res); err != nil {
			return "", ptyworker.ResponseEnvelope{}, ptyworker.EventEnvelope{}, err
		}
		return "res", res, ptyworker.EventEnvelope{}, nil
	case "evt":
		data, _ := json.Marshal(raw)
		var evt ptyworker.EventEnvelope
		if err := json.Unmarshal(data, &evt); err != nil {
			return "", ptyworker.ResponseEnvelope{}, ptyworker.EventEnvelope{}, err
		}
		return "evt", ptyworker.ResponseEnvelope{}, evt, nil
	default:
		return "", ptyworker.ResponseEnvelope{}, ptyworker.EventEnvelope{}, errors.New("unknown frame type")
	}
}

func convertWorkerEvent(evt ptyworker.EventEnvelope) (OutputEvent, bool) {
	switch evt.Event {
	case ptyworker.EventOutput:
		if evt.Data == nil {
			return OutputEvent{}, false
		}
		data, err := base64.StdEncoding.DecodeString(*evt.Data)
		if err != nil {
			return OutputEvent{}, false
		}
		seq := uint32(0)
		if evt.Seq != nil {
			seq = *evt.Seq
		}
		return OutputEvent{Kind: OutputEventKindOutput, Data: data, Seq: seq}, true
	case ptyworker.EventDesync:
		reason := ""
		if evt.Reason != nil {
			reason = *evt.Reason
		}
		return OutputEvent{Kind: OutputEventKindDesync, Reason: reason}, true
	default:
		return OutputEvent{}, false
	}
}

func signalName(sig syscall.Signal) string {
	switch sig {
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGHUP:
		return "SIGHUP"
	case syscall.SIGKILL:
		return "SIGKILL"
	default:
		return "SIGTERM"
	}
}

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func randomToken(nBytes int) (string, error) {
	buf := make([]byte, nBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func (b *WorkerBackend) quarantineRegistry(path, reason string) {
	base := filepath.Base(path)
	dest := filepath.Join(
		b.quarantineDir(),
		fmt.Sprintf("%s.%s.%d", base, reason, time.Now().Unix()),
	)
	if err := os.Rename(path, dest); err != nil {
		b.cfg.Logf("worker registry quarantine failed: path=%s reason=%s err=%v", path, reason, err)
		return
	}
	b.cfg.Logf("worker registry quarantined: path=%s reason=%s dest=%s", path, reason, dest)
}

type workerStream struct {
	conn        net.Conn
	enc         *json.Encoder
	dec         *json.Decoder
	sessionID   string
	detachReqID string

	events    chan OutputEvent
	done      chan struct{}
	doneOnce  sync.Once
	closeOnce sync.Once
	closed    chan struct{}
}

func newWorkerStream(conn net.Conn, enc *json.Encoder, dec *json.Decoder, sessionID, detachReqID string, pre []OutputEvent) *workerStream {
	s := &workerStream{
		conn:        conn,
		enc:         enc,
		dec:         dec,
		sessionID:   sessionID,
		detachReqID: detachReqID,
		events:      make(chan OutputEvent, streamEventBufferSize),
		done:        make(chan struct{}),
		closed:      make(chan struct{}),
	}
	go s.readLoop(pre)
	return s
}

func (s *workerStream) Events() <-chan OutputEvent {
	return s.events
}

func (s *workerStream) Close() error {
	s.closeOnce.Do(func() {
		s.doneOnce.Do(func() {
			close(s.done)
		})
		// Best-effort detach; bound write time so shutdown paths cannot hang.
		_ = s.conn.SetWriteDeadline(time.Now().Add(250 * time.Millisecond))
		_ = writeRequest(s.enc, s.detachReqID, ptyworker.MethodDetach, map[string]any{})
		_ = s.conn.SetWriteDeadline(time.Time{})
		_ = s.conn.Close()
		<-s.closed
	})
	return nil
}

func (s *workerStream) readLoop(pre []OutputEvent) {
	defer func() {
		_ = s.conn.Close()
		close(s.events)
		close(s.closed)
	}()

	for _, evt := range pre {
		if !s.publish(evt) {
			return
		}
	}

	for {
		frameType, _, evt, err := readFrame(s.dec)
		if err != nil {
			return
		}
		if frameType != "evt" {
			continue
		}
		converted, ok := convertWorkerEvent(evt)
		if !ok {
			continue
		}
		if !s.publish(converted) {
			return
		}
	}
}

func (s *workerStream) publish(evt OutputEvent) bool {
	select {
	case <-s.done:
		return false
	case s.events <- evt:
		return true
	default:
		// Signal desync on overflow once, then terminate stream.
		select {
		case s.events <- OutputEvent{Kind: OutputEventKindDesync, Reason: "buffer_overflow"}:
		default:
		}
		return false
	}
}

func withDefaultRPCTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, defaultRPCTimeout)
}

func applyConnDeadline(conn net.Conn, ctx context.Context) error {
	if deadline, ok := ctx.Deadline(); ok {
		return conn.SetDeadline(deadline)
	}
	return conn.SetDeadline(time.Time{})
}

func (b *WorkerBackend) probeRecoveryInfo(ctx context.Context, session *workerSession) error {
	backoff := 100 * time.Millisecond
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		attemptCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		_, err := b.callInfo(attemptCtx, session)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		if !isTransientRecoveryError(err) {
			return err
		}
		if attempt < 2 {
			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			backoff *= 2
		}
	}
	return lastErr
}

func isTransientRecoveryError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	lowerErr := strings.ToLower(err.Error())
	transientTokens := []string{
		"timeout",
		"temporarily unavailable",
		"connection refused",
		"connection reset",
		"broken pipe",
		"resource temporarily unavailable",
		"i/o timeout",
	}
	for _, token := range transientTokens {
		if strings.Contains(lowerErr, token) {
			return true
		}
	}
	return false
}

func validateSessionID(sessionID string) error {
	id := strings.TrimSpace(sessionID)
	if id == "" {
		return errors.New("missing session id")
	}
	if strings.Contains(id, "..") {
		return fmt.Errorf("invalid session id: %q", sessionID)
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.' || r == ':':
		default:
			return fmt.Errorf("invalid session id: %q", sessionID)
		}
	}
	return nil
}
