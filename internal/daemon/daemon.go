package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/classifier"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/github"
	"github.com/victorarias/attn/internal/logging"
	"github.com/victorarias/attn/internal/pathutil"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/transcript"
)

type repoCache struct {
	fetchedAt time.Time
	branches  []protocol.Branch
}

type workerReconcileReport struct {
	Created         int
	StateUpdated    int
	MarkedIdle      int
	SkippedIdle     int
	SkippedRecent   int
	SkippedShell    int
	LikelyAlive     int
	LivenessUnknown int
	MissingMetadata int
	Changed         bool
}

const (
	claudeTranscriptRetryWindow   = 2 * time.Second
	claudeTranscriptRetryInterval = 100 * time.Millisecond
	claudeTranscriptFreshnessSkew = 5 * time.Second
	classifierRetryMaxAttempts    = 3
	classifierRetryBaseBackoff    = 250 * time.Millisecond

	startupRecoveryRetryMax       = 2
	startupRecoveryRetryDelay     = 500 * time.Millisecond
	deferredRecoveryMaxAttempts   = 3
	deferredRecoveryRetryInterval = 10 * time.Second
	deferredRecoveryRPCTimeout    = 5 * time.Second
	workerStartupProbeTimeout     = 10 * time.Second

	warnPersistenceDegraded       = "persistence_degraded"
	warnWorkerRecoveryPartial     = "worker_recovery_partial"
	warnStaleSessionsPruned       = "stale_sessions_pruned"
	warnStaleSessionMissingWorker = "stale_session_missing_worker"
	warnPTYBackendFallback        = "pty_backend_fallback"
	warnPTYBackendUnsupported     = "pty_backend_unsupported"
	warnGHNotInstalled            = "gh_not_installed"
	warnGHVersionTooOld           = "gh_version_too_old"
)

var errNoNewAssistantTurn = errors.New("no new assistant turn")

// ReviewerFactory creates a reviewer for testing
type ReviewerFactory func(*store.Store) Reviewer

// Reviewer interface for code review operations
type Reviewer interface {
	Run(ctx context.Context, config ReviewerConfig, onEvent func(ReviewerEvent)) error
}

// ReviewerConfig matches reviewer.ReviewConfig
type ReviewerConfig struct {
	RepoPath           string
	Branch             string
	BaseBranch         string
	ReviewID           string
	IsRereview         bool
	LastReviewSHA      string
	PreviousTranscript string
}

// ReviewerEvent matches reviewer.ReviewEvent
type ReviewerEvent struct {
	Type       string // "started", "chunk", "finding", "resolved", "tool_use", "complete", "error", "cancelled"
	Content    string
	Finding    *ReviewerFinding
	ResolvedID string           // For resolved events
	ToolUse    *ReviewerToolUse // For tool_use events
	Success    bool
	Error      string
}

// ReviewerFinding matches reviewer.Finding
type ReviewerFinding struct {
	Filepath  string
	LineStart int
	LineEnd   int
	Content   string
	Severity  string
	CommentID string
}

// ReviewerToolUse matches reviewer.ToolUse
type ReviewerToolUse struct {
	Name   string
	Input  map[string]any
	Output string
}

// Daemon manages Claude sessions
type Daemon struct {
	socketPath       string
	pidPath          string
	pidFile          *os.File // Held open with flock for exclusive access
	dataRoot         string
	daemonInstanceID string
	store            *store.Store
	listener         net.Listener
	httpServer       *http.Server
	wsHub            *wsHub
	done             chan struct{}
	logger           *logging.Logger
	ghRegistry       *github.ClientRegistry
	classifier       Classifier      // Optional, uses package-level classifier.Classify if nil
	reviewerFactory  ReviewerFactory // Optional, creates real reviewer if nil
	repoCaches       map[string]*repoCache
	repoCacheMu      sync.RWMutex
	warnings         []protocol.DaemonWarning
	warningsMu       sync.RWMutex
	ptyBackend       ptybackend.Backend
	watchersMu       sync.Mutex
	transcriptWatch  map[string]*transcriptWatcher
	classifiedMu     sync.Mutex
	classifiedTurn   map[string]string
	classifyingTurn  map[string]string
	recoveryMu       sync.RWMutex
	recovering       bool
	pendingInitialWS map[*wsClient]struct{}
	startedOnce      sync.Once
	startedCh        chan struct{}
}

// addWarning adds a warning to be surfaced to the UI
func (d *Daemon) addWarning(code, message string) {
	d.warningsMu.Lock()
	defer d.warningsMu.Unlock()
	// Avoid duplicates
	for _, w := range d.warnings {
		if w.Code == code && w.Message == message {
			return
		}
	}
	d.warnings = append(d.warnings, protocol.DaemonWarning{
		Code:    code,
		Message: message,
	})
}

// getWarnings returns a copy of the warnings slice
func (d *Daemon) getWarnings() []protocol.DaemonWarning {
	d.warningsMu.RLock()
	defer d.warningsMu.RUnlock()
	if len(d.warnings) == 0 {
		return nil
	}
	result := make([]protocol.DaemonWarning, len(d.warnings))
	copy(result, d.warnings)
	return result
}

func (d *Daemon) clearWarnings() {
	d.warningsMu.Lock()
	defer d.warningsMu.Unlock()
	d.warnings = nil
}

func (d *Daemon) setRecovering(value bool) {
	var pending []*wsClient

	d.recoveryMu.Lock()
	d.recovering = value
	if !value {
		pending = make([]*wsClient, 0, len(d.pendingInitialWS))
		for client := range d.pendingInitialWS {
			pending = append(pending, client)
		}
		d.pendingInitialWS = make(map[*wsClient]struct{})
	}
	d.recoveryMu.Unlock()

	if !value {
		for _, client := range pending {
			d.sendInitialState(client)
		}
	}
}

func (d *Daemon) isRecovering() bool {
	d.recoveryMu.RLock()
	defer d.recoveryMu.RUnlock()
	return d.recovering
}

func (d *Daemon) scheduleInitialState(client *wsClient) {
	sendNow := false

	d.recoveryMu.Lock()
	if d.recovering {
		d.pendingInitialWS[client] = struct{}{}
	} else {
		sendNow = true
	}
	d.recoveryMu.Unlock()

	if sendNow {
		d.sendInitialState(client)
	}
}

func (d *Daemon) dropPendingInitialState(client *wsClient) {
	d.recoveryMu.Lock()
	defer d.recoveryMu.Unlock()
	delete(d.pendingInitialWS, client)
}

func (d *Daemon) signalStarted() {
	d.startedOnce.Do(func() {
		if d.startedCh == nil {
			d.startedCh = make(chan struct{})
		}
		close(d.startedCh)
	})
}

func (d *Daemon) waitStarted(timeout time.Duration) bool {
	if d.startedCh == nil {
		return false
	}
	select {
	case <-d.startedCh:
		return true
	case <-time.After(timeout):
		return false
	}
}

// New creates a new daemon
func New(socketPath string) *Daemon {
	logger, _ := logging.New(logging.DefaultLogPath())

	if err := pathutil.EnsureGUIPath(); err != nil {
		logger.Infof("PATH recovery failed: %v", err)
	}

	// Wire up classifier logger to daemon logger
	classifier.SetLogger(func(format string, args ...interface{}) {
		logger.Infof(format, args...)
	})

	// Create SQLite-backed store
	dbPath := config.DBPath()
	sessionStore, err := store.NewWithDB(dbPath)
	var startupWarnings []protocol.DaemonWarning
	if err != nil {
		logger.Infof("Failed to open DB at %s: %v (using in-memory)", dbPath, err)
		sessionStore = store.New() // Fallback to in-memory
		startupWarnings = append(startupWarnings, protocol.DaemonWarning{
			Code: warnPersistenceDegraded,
			Message: fmt.Sprintf(
				"Persistence degraded: unable to open durable state at %s. Running in-memory only; session state will not survive daemon restarts. See daemon log in %s for details.",
				dbPath,
				config.LogPath(),
			),
		})
	}

	// Clean up legacy JSON state file if it exists
	legacyPath := config.StatePath()
	if _, err := os.Stat(legacyPath); err == nil {
		os.Remove(legacyPath)
		logger.Infof("Removed legacy state file: %s", legacyPath)
	}

	// Derive paths from socket path directory.
	dataRoot := filepath.Dir(socketPath)
	pidPath := filepath.Join(dataRoot, "attn.pid")
	manager := pty.NewManager(pty.DefaultScrollbackSize, logger.Infof)

	return &Daemon{
		socketPath:       socketPath,
		pidPath:          pidPath,
		dataRoot:         dataRoot,
		store:            sessionStore,
		wsHub:            newWSHub(),
		done:             make(chan struct{}),
		logger:           logger,
		ghRegistry:       github.NewClientRegistry(),
		repoCaches:       make(map[string]*repoCache),
		warnings:         startupWarnings,
		ptyBackend:       ptybackend.NewEmbedded(manager),
		transcriptWatch:  make(map[string]*transcriptWatcher),
		pendingInitialWS: make(map[*wsClient]struct{}),
		startedCh:        make(chan struct{}),
		classifiedTurn:   make(map[string]string),
		classifyingTurn:  make(map[string]string),
	}
}

// NewForTesting creates a daemon with a non-persistent store for tests
func NewForTesting(socketPath string) *Daemon {
	dataRoot := filepath.Dir(socketPath)
	pidPath := filepath.Join(dataRoot, "attn.pid")
	manager := pty.NewManager(pty.DefaultScrollbackSize, nil)
	return &Daemon{
		socketPath:       socketPath,
		pidPath:          pidPath,
		dataRoot:         dataRoot,
		store:            store.New(),
		wsHub:            newWSHub(),
		done:             make(chan struct{}),
		logger:           nil, // No logging in tests
		ghRegistry:       github.NewClientRegistry(),
		repoCaches:       make(map[string]*repoCache),
		ptyBackend:       ptybackend.NewEmbedded(manager),
		transcriptWatch:  make(map[string]*transcriptWatcher),
		pendingInitialWS: make(map[*wsClient]struct{}),
		startedCh:        make(chan struct{}),
		classifiedTurn:   make(map[string]string),
		classifyingTurn:  make(map[string]string),
	}
}

// NewWithGitHubClient creates a daemon with a custom GitHub client for testing
func NewWithGitHubClient(socketPath string, ghClient github.GitHubClient) *Daemon {
	dataRoot := filepath.Dir(socketPath)
	pidPath := filepath.Join(dataRoot, "attn.pid")
	registry := github.NewClientRegistry()
	if client, ok := ghClient.(*github.Client); ok {
		registry.Register(client.Host(), client)
	}
	manager := pty.NewManager(pty.DefaultScrollbackSize, nil)
	return &Daemon{
		socketPath:       socketPath,
		pidPath:          pidPath,
		dataRoot:         dataRoot,
		store:            store.New(),
		wsHub:            newWSHub(),
		done:             make(chan struct{}),
		logger:           nil,
		ghRegistry:       registry,
		repoCaches:       make(map[string]*repoCache),
		ptyBackend:       ptybackend.NewEmbedded(manager),
		transcriptWatch:  make(map[string]*transcriptWatcher),
		pendingInitialWS: make(map[*wsClient]struct{}),
		startedCh:        make(chan struct{}),
		classifiedTurn:   make(map[string]string),
		classifyingTurn:  make(map[string]string),
	}
}

// Start starts the daemon
func (d *Daemon) Start() error {
	if d.dataRoot == "" {
		d.dataRoot = filepath.Dir(d.socketPath)
	}
	if d.pendingInitialWS == nil {
		d.pendingInitialWS = make(map[*wsClient]struct{})
	}
	if d.startedCh == nil {
		d.startedCh = make(chan struct{})
	}
	if d.transcriptWatch == nil {
		d.transcriptWatch = make(map[string]*transcriptWatcher)
	}
	if d.classifiedTurn == nil {
		d.classifiedTurn = make(map[string]string)
	}
	if d.classifyingTurn == nil {
		d.classifyingTurn = make(map[string]string)
	}
	if d.ptyBackend == nil {
		d.ptyBackend = ptybackend.NewEmbedded(pty.NewManager(pty.DefaultScrollbackSize, d.logf))
	}
	if d.daemonInstanceID == "" {
		instanceID, err := ensureDaemonInstanceID(d.dataRoot)
		if err != nil {
			return fmt.Errorf("ensure daemon instance id: %w", err)
		}
		d.daemonInstanceID = instanceID
	}
	selectedBackend := strings.TrimSpace(strings.ToLower(os.Getenv("ATTN_PTY_BACKEND")))
	if selectedBackend == "" {
		selectedBackend = "worker"
	}
	switch selectedBackend {
	case "embedded":
		// already initialized
	case "worker":
		workerBackend, err := ptybackend.NewWorker(ptybackend.WorkerBackendConfig{
			DataRoot:         d.dataRoot,
			DaemonInstanceID: d.daemonInstanceID,
			BinaryPath:       strings.TrimSpace(os.Getenv("ATTN_PTY_WORKER_BINARY")),
			Logf:             d.logf,
		})
		if err != nil {
			d.logf("failed to initialize worker PTY backend: %v; falling back to embedded", err)
			d.addWarning(
				warnPTYBackendFallback,
				fmt.Sprintf("Failed to initialize worker PTY backend (%v). Falling back to embedded.", err),
			)
		} else {
			if shouldRunWorkerStartupProbe() {
				probeCtx, cancelProbe := context.WithTimeout(context.Background(), workerStartupProbeTimeout)
				probeErr := workerBackend.Probe(probeCtx)
				cancelProbe()
				if probeErr != nil {
					d.logf("worker PTY backend startup probe failed: %v; falling back to embedded", probeErr)
					d.addWarning(
						warnPTYBackendFallback,
						fmt.Sprintf("Worker PTY backend probe failed (%v). Falling back to embedded.", probeErr),
					)
				} else {
					d.ptyBackend = workerBackend
					d.logf("using PTY backend: worker")
				}
			} else {
				d.ptyBackend = workerBackend
				d.logf("using PTY backend: worker (startup probe disabled)")
			}
		}
	default:
		d.logf("unsupported PTY backend %q, falling back to embedded", selectedBackend)
		d.addWarning(
			warnPTYBackendUnsupported,
			fmt.Sprintf("PTY backend %q is not available in this build. Falling back to embedded.", selectedBackend),
		)
	}

	d.setRecovering(true)
	startSucceeded := false
	defer func() {
		if !startSucceeded {
			d.setRecovering(false)
		}
	}()

	// Acquire PID lock (kills any existing daemon)
	if err := d.acquirePIDLock(); err != nil {
		return fmt.Errorf("acquire PID lock: %w", err)
	}
	defer func() {
		if startSucceeded {
			return
		}
		if d.httpServer != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = d.httpServer.Shutdown(ctx)
			cancel()
		}
		if d.listener != nil {
			_ = d.listener.Close()
			d.listener = nil
		}
		d.releasePIDLock()
	}()

	// Remove stale socket
	os.Remove(d.socketPath)

	listener, err := net.Listen("unix", d.socketPath)
	if err != nil {
		return err
	}
	d.listener = listener
	d.log("daemon started")

	// Start WebSocket hub with daemon's logger
	d.wsHub.logf = d.logf
	go d.wsHub.run()

	// PTY exit events are emitted asynchronously from read loops.
	if hooks, ok := d.ptyBackend.(ptybackend.LifecycleHooks); ok {
		hooks.SetExitHandler(d.handlePTYExit)
		hooks.SetStateHandler(d.handlePTYState)
	}

	// Create HTTP server for WebSocket (must be created synchronously to avoid race with Stop())
	d.initHTTPServer()
	go d.runHTTPServer()

	recoveryStartedAt := time.Now()
	go func() {
		d.performStartupPTYRecovery(recoveryStartedAt)
		d.setRecovering(false)
	}()

	// Note: No background persistence needed - SQLite persists immediately

	// Discover GitHub hosts and refresh periodically (async to not block accept loop)
	go func() {
		if err := d.refreshGitHubHosts(); err != nil {
			d.logf("Initial GitHub host discovery failed: %v", err)
		}
		// Start PR polling after initial host discovery
		go d.pollPRs()
		// Start periodic host refresh
		go d.refreshGitHubHostsLoop()
	}()

	// Start branch monitoring
	go d.monitorBranches()

	d.signalStarted()
	startSucceeded = true

	for {
		select {
		case <-d.done:
			return nil
		default:
		}

		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-d.done:
				return nil
			default:
				d.logf("accept error: %v", err)
				continue
			}
		}

		go d.handleConnection(conn)
	}
}

func (d *Daemon) pruneSessionsWithoutPTY() int {
	if d.store == nil {
		return 0
	}

	liveIDs := make(map[string]struct{})
	if d.ptyBackend != nil {
		for _, id := range d.ptyBackend.SessionIDs(context.Background()) {
			liveIDs[id] = struct{}{}
		}
	}

	sessions := d.store.List("")
	removed := 0
	for _, session := range sessions {
		if _, ok := liveIDs[session.ID]; ok {
			continue
		}
		d.store.Remove(session.ID)
		removed++
	}
	return removed
}

func (d *Daemon) performStartupPTYRecovery(recoveryStartedAt time.Time) {
	recoveryReport, recoverErr := d.recoverPTYBackend(10 * time.Second)
	if recoverErr != nil {
		d.logf("PTY backend recovery failed: %v", recoverErr)
		d.addWarning(warnWorkerRecoveryPartial, fmt.Sprintf("PTY recovery failed: %v", recoverErr))
	} else {
		d.logf(
			"PTY recovery summary: recovered=%d pruned=%d missing=%d failed=%d",
			recoveryReport.Recovered,
			recoveryReport.Pruned,
			recoveryReport.Missing,
			recoveryReport.Failed,
		)
		if recoveryReport.Missing > 0 {
			d.addWarning(
				warnWorkerRecoveryPartial,
				fmt.Sprintf("PTY recovery skipped %d workers due to transient unavailability.", recoveryReport.Missing),
			)
		}
	}

	if _, ok := d.ptyBackend.(ptybackend.RecoverableRuntime); ok {
		d.reconcileStartupWorkerSessions(recoveryReport, recoverErr, recoveryStartedAt)
		return
	}

	removedSessions := d.pruneSessionsWithoutPTY()
	if removedSessions > 0 {
		d.logf("pruned %d stale sessions without live PTY on startup", removedSessions)
		d.addWarning(
			warnStaleSessionsPruned,
			fmt.Sprintf("Removed %d stale sessions from a previous daemon run because no live PTY was found.", removedSessions),
		)
	}
}

func (d *Daemon) recoverPTYBackend(timeout time.Duration) (ptybackend.RecoveryReport, error) {
	recoveryCtx, cancelRecovery := context.WithTimeout(context.Background(), timeout)
	defer cancelRecovery()
	return d.ptyBackend.Recover(recoveryCtx)
}

func (d *Daemon) reconcileStartupWorkerSessions(recoveryReport ptybackend.RecoveryReport, recoverErr error, recoveryStartedAt time.Time) {
	allowIdleDemotion := recoverErr == nil && recoveryReport.Missing == 0 && recoveryReport.Failed == 0
	if !allowIdleDemotion {
		for attempt := 1; attempt <= startupRecoveryRetryMax; attempt++ {
			retryReport, retryErr := d.recoverPTYBackend(5 * time.Second)
			if retryErr == nil && retryReport.Missing == 0 && retryReport.Failed == 0 {
				recoveryReport = retryReport
				recoverErr = nil
				allowIdleDemotion = true
				d.logf(
					"PTY recovery stabilized after retry %d: recovered=%d pruned=%d missing=%d failed=%d",
					attempt,
					retryReport.Recovered,
					retryReport.Pruned,
					retryReport.Missing,
					retryReport.Failed,
				)
				break
			}
			d.logf("PTY recovery retry %d incomplete: err=%v missing=%d failed=%d", attempt, retryErr, retryReport.Missing, retryReport.Failed)
			if attempt < startupRecoveryRetryMax {
				time.Sleep(startupRecoveryRetryDelay)
			}
		}
	}

	reconcile := d.reconcileSessionsWithWorkerBackend(context.Background(), allowIdleDemotion, recoveryStartedAt)
	if reconcile.Created > 0 || reconcile.StateUpdated > 0 || reconcile.MarkedIdle > 0 || reconcile.SkippedIdle > 0 || reconcile.SkippedRecent > 0 || reconcile.SkippedShell > 0 || reconcile.LikelyAlive > 0 || reconcile.LivenessUnknown > 0 || reconcile.MissingMetadata > 0 {
		d.logf(
			"worker session reconciliation summary: created=%d state_updated=%d marked_idle=%d skipped_idle=%d skipped_recent=%d skipped_shell=%d likely_alive=%d liveness_unknown=%d missing_metadata=%d",
			reconcile.Created,
			reconcile.StateUpdated,
			reconcile.MarkedIdle,
			reconcile.SkippedIdle,
			reconcile.SkippedRecent,
			reconcile.SkippedShell,
			reconcile.LikelyAlive,
			reconcile.LivenessUnknown,
			reconcile.MissingMetadata,
		)
	}
	if reconcile.SkippedIdle > 0 {
		d.addWarning(
			warnWorkerRecoveryPartial,
			fmt.Sprintf("Deferred marking %d tracked sessions idle because PTY recovery was incomplete.", reconcile.SkippedIdle),
		)
	}
	if reconcile.MarkedIdle > 0 {
		d.addWarning(
			warnStaleSessionMissingWorker,
			fmt.Sprintf("%d tracked sessions were expected to be running but no worker was recovered; they were marked idle.", reconcile.MarkedIdle),
		)
	}
	if reconcile.MissingMetadata > 0 {
		d.addWarning(
			warnWorkerRecoveryPartial,
			fmt.Sprintf("Recovered workers were missing metadata for %d sessions.", reconcile.MissingMetadata),
		)
	}
	if reconcile.LikelyAlive > 0 {
		d.addWarning(
			warnWorkerRecoveryPartial,
			fmt.Sprintf("Retained %d sessions in non-idle state because worker liveness signals were still present.", reconcile.LikelyAlive),
		)
	}
	if reconcile.LivenessUnknown > 0 {
		d.addWarning(
			warnWorkerRecoveryPartial,
			fmt.Sprintf("Retained %d sessions in non-idle state because worker liveness checks were inconclusive.", reconcile.LivenessUnknown),
		)
	}
	if reconcile.SkippedRecent > 0 {
		d.addWarning(
			warnWorkerRecoveryPartial,
			fmt.Sprintf("Retained %d sessions in non-idle state because they were updated after recovery started.", reconcile.SkippedRecent),
		)
	}
	if reconcile.SkippedIdle > 0 || reconcile.SkippedRecent > 0 || reconcile.LivenessUnknown > 0 || reconcile.MissingMetadata > 0 {
		d.scheduleDeferredWorkerReconciliation(recoveryStartedAt)
	}
}

func (d *Daemon) reconcileSessionsWithWorkerBackend(ctx context.Context, allowIdleDemotion bool, demotionCutoff time.Time) workerReconcileReport {
	report := workerReconcileReport{}
	if d.store == nil || d.ptyBackend == nil {
		return report
	}

	liveIDs := make(map[string]struct{})
	for _, id := range d.ptyBackend.SessionIDs(ctx) {
		liveIDs[id] = struct{}{}
	}

	infoProvider, _ := d.ptyBackend.(ptybackend.SessionInfoProvider)
	livenessProber, _ := d.ptyBackend.(ptybackend.SessionLivenessProber)

	for sessionID := range liveIDs {
		existing := d.store.Get(sessionID)
		var info ptybackend.SessionInfo
		var haveInfo bool
		if infoProvider != nil {
			fetched, err := infoProvider.SessionInfo(ctx, sessionID)
			if err == nil {
				info = fetched
				haveInfo = true
			}
		}

		if existing == nil {
			if !haveInfo {
				report.MissingMetadata++
				continue
			}
			if protocol.NormalizeSpawnAgent(info.Agent, string(protocol.SessionAgentCodex)) == protocol.AgentShellValue {
				report.SkippedShell++
				continue
			}

			now := string(protocol.TimestampNow())
			directory := strings.TrimSpace(info.CWD)
			if directory == "" {
				report.MissingMetadata++
				continue
			}
			label := filepath.Base(directory)
			if label == "" || label == "." || label == string(filepath.Separator) {
				label = sessionID
			}

			state := sessionStateFromRecoveredInfo(info)

			d.store.Add(&protocol.Session{
				ID:             sessionID,
				Label:          label,
				Agent:          protocol.NormalizeSessionAgent(protocol.SessionAgent(info.Agent), protocol.SessionAgentCodex),
				Directory:      directory,
				State:          state,
				StateSince:     now,
				StateUpdatedAt: now,
				LastSeen:       now,
			})
			report.Created++
			report.Changed = true
			continue
		}

		d.store.Touch(sessionID)
		if haveInfo {
			nextState := sessionStateFromRecoveredInfo(info)
			if existing.State != nextState {
				d.store.UpdateState(sessionID, string(nextState))
				report.StateUpdated++
				report.Changed = true
			}
			continue
		}
		switch existing.State {
		case protocol.SessionStateWaitingInput, protocol.SessionStatePendingApproval:
			// Preserve interactive waiting/approval states during recovery.
		default:
			if existing.State != protocol.SessionStateWorking {
				d.store.UpdateState(sessionID, protocol.StateWorking)
				report.StateUpdated++
				report.Changed = true
			}
		}
	}

	for _, session := range d.store.List("") {
		if _, ok := liveIDs[session.ID]; ok {
			continue
		}
		if session.State == protocol.SessionStateIdle {
			continue
		}
		if sessionUpdatedAfter(session, demotionCutoff) {
			report.SkippedRecent++
			continue
		}
		if livenessProber != nil {
			likelyAlive, probeErr := livenessProber.SessionLikelyAlive(ctx, session.ID)
			if probeErr != nil {
				d.logf("worker liveness probe failed for session %s: %v", session.ID, probeErr)
				report.LivenessUnknown++
				continue
			}
			if likelyAlive {
				report.LikelyAlive++
				continue
			}
		}
		if !allowIdleDemotion {
			report.SkippedIdle++
			continue
		}
		d.store.UpdateState(session.ID, protocol.StateIdle)
		report.StateUpdated++
		report.Changed = true
		report.MarkedIdle++
	}

	return report
}

func (d *Daemon) scheduleDeferredWorkerReconciliation(recoveryStartedAt time.Time) {
	go d.runDeferredWorkerReconciliation(deferredRecoveryMaxAttempts, deferredRecoveryRetryInterval, recoveryStartedAt)
}

func (d *Daemon) runDeferredWorkerReconciliation(maxAttempts int, retryInterval time.Duration, recoveryStartedAt time.Time) {
	if d.ptyBackend == nil || maxAttempts <= 0 {
		return
	}
	if _, ok := d.ptyBackend.(ptybackend.RecoverableRuntime); !ok {
		return
	}

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		select {
		case <-d.done:
			return
		default:
		}
		if attempt > 1 && retryInterval > 0 {
			select {
			case <-d.done:
				return
			case <-time.After(retryInterval):
			}
		}

		recoveryCtx, cancel := context.WithTimeout(context.Background(), deferredRecoveryRPCTimeout)
		recoveryReport, recoverErr := d.ptyBackend.Recover(recoveryCtx)
		cancel()

		fullyRecovered := recoverErr == nil && recoveryReport.Missing == 0 && recoveryReport.Failed == 0
		forceIdleDemotion := attempt == maxAttempts
		if !fullyRecovered && !forceIdleDemotion {
			d.logf("deferred PTY recovery attempt %d incomplete: err=%v missing=%d failed=%d", attempt, recoverErr, recoveryReport.Missing, recoveryReport.Failed)
			continue
		}

		reconcile := d.reconcileSessionsWithWorkerBackend(context.Background(), true, recoveryStartedAt)
		if reconcile.Changed {
			d.broadcastSessionsUpdated()
		}
		if reconcile.MarkedIdle > 0 {
			d.addWarning(
				warnStaleSessionMissingWorker,
				fmt.Sprintf("%d tracked sessions were expected to be running but no worker was recovered; they were marked idle.", reconcile.MarkedIdle),
			)
		}
		if !fullyRecovered {
			d.addWarning(
				warnWorkerRecoveryPartial,
				fmt.Sprintf(
					"Forced stale-session reconciliation after %d deferred PTY recovery attempts (missing=%d failed=%d).",
					maxAttempts,
					recoveryReport.Missing,
					recoveryReport.Failed,
				),
			)
		}
		if reconcile.LivenessUnknown > 0 {
			d.addWarning(
				warnWorkerRecoveryPartial,
				fmt.Sprintf("Deferred stale-session idle demotion for %d sessions because liveness checks remained inconclusive.", reconcile.LivenessUnknown),
			)
		}
		if reconcile.SkippedRecent > 0 {
			d.addWarning(
				warnWorkerRecoveryPartial,
				fmt.Sprintf("Deferred stale-session idle demotion for %d sessions that were updated after recovery began.", reconcile.SkippedRecent),
			)
		} else if reconcile.MarkedIdle > 0 {
			d.logf("deferred worker reconciliation marked %d stale sessions idle after recovery stabilized", reconcile.MarkedIdle)
		}
		return
	}
}

func sessionUpdatedAfter(session *protocol.Session, cutoff time.Time) bool {
	if session == nil || cutoff.IsZero() {
		return false
	}
	updatedAt := protocol.Timestamp(session.StateUpdatedAt).Time()
	if updatedAt.IsZero() {
		return false
	}
	return updatedAt.After(cutoff)
}

func sessionStateFromRecoveredInfo(info ptybackend.SessionInfo) protocol.SessionState {
	if !info.Running {
		return protocol.SessionStateIdle
	}
	agent := protocol.NormalizeSessionAgent(protocol.SessionAgent(info.Agent), protocol.SessionAgentCodex)
	switch info.State {
	case protocol.StateWaitingInput:
		if agent == protocol.SessionAgentCodex || agent == protocol.SessionAgentCopilot {
			return protocol.SessionStateWorking
		}
		return protocol.SessionStateWaitingInput
	case protocol.StatePendingApproval:
		return protocol.SessionStatePendingApproval
	case protocol.StateIdle:
		if agent == protocol.SessionAgentCodex || agent == protocol.SessionAgentCopilot {
			return protocol.SessionStateWorking
		}
		return protocol.SessionStateIdle
	default:
		return protocol.SessionStateWorking
	}
}

// Stop stops the daemon
func (d *Daemon) Stop() {
	d.log("daemon stopping")
	close(d.done)
	d.stopAllTranscriptWatchers()
	if d.ptyBackend != nil {
		_ = d.ptyBackend.Shutdown(context.Background())
	}
	if d.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		d.httpServer.Shutdown(ctx)
	}
	if d.listener != nil {
		d.listener.Close()
	}
	os.Remove(d.socketPath)
	d.releasePIDLock()
	if d.logger != nil {
		d.logger.Close()
	}
}

func (d *Daemon) handlePTYExit(info ptybackend.ExitInfo) {
	d.stopTranscriptWatcher(info.ID)

	if d.ptyBackend != nil {
		if err := d.removePTYSession(info.ID); err != nil {
			d.logf("pty backend remove on exit failed for %s: %v", info.ID, err)
		}
	}

	if session := d.store.Get(info.ID); session != nil {
		d.store.Touch(info.ID)
		d.store.UpdateState(info.ID, protocol.StateIdle)
		updated := d.store.Get(info.ID)
		if updated != nil {
			d.wsHub.Broadcast(&protocol.WebSocketEvent{
				Event:   protocol.EventSessionStateChanged,
				Session: updated,
			})
		}
	}

	event := &protocol.WebSocketEvent{
		Event:    protocol.EventSessionExited,
		ID:       protocol.Ptr(info.ID),
		ExitCode: protocol.Ptr(info.ExitCode),
	}
	if info.Signal != "" {
		event.Signal = protocol.Ptr(info.Signal)
	}
	d.wsHub.Broadcast(event)
}

func (d *Daemon) removePTYSession(sessionID string) error {
	if d.ptyBackend == nil {
		return nil
	}
	// Avoid hanging the exit path; we'll retry on transient errors.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := d.ptyBackend.Remove(ctx, sessionID)
	if err == nil || errors.Is(err, pty.ErrSessionNotFound) || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	go func() {
		// Best-effort retry: transport issues can race daemon exit events.
		backoff := 250 * time.Millisecond
		for i := 0; i < 4; i++ {
			time.Sleep(backoff)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			retryErr := d.ptyBackend.Remove(ctx, sessionID)
			cancel()
			if retryErr == nil || errors.Is(retryErr, pty.ErrSessionNotFound) || errors.Is(retryErr, os.ErrNotExist) {
				return
			}
			backoff *= 2
		}
		d.logf("pty backend remove still failing after retries for %s: %v", sessionID, err)
	}()
	return err
}

func (d *Daemon) terminateSession(sessionID string, sig syscall.Signal) {
	d.stopTranscriptWatcher(sessionID)

	if d.ptyBackend == nil {
		return
	}
	if err := d.ptyBackend.Kill(context.Background(), sessionID, sig); err != nil && !errors.Is(err, pty.ErrSessionNotFound) {
		d.logf("terminate session failed for %s: %v", sessionID, err)
	}
	_ = d.ptyBackend.Remove(context.Background(), sessionID)
}

func (d *Daemon) handlePTYState(sessionID, state string) {
	session := d.store.Get(sessionID)
	if session == nil {
		return
	}
	agent := session.Agent
	if (agent == protocol.SessionAgentCodex || agent == protocol.SessionAgentCopilot) &&
		state != protocol.StateWorking &&
		state != protocol.StatePendingApproval {
		return
	}
	// Copilot emits frequent redraw chunks while approval prompts are visible.
	// Treat PTY "working" as a hint only, and let transcript watcher clear
	// pending_approval when the gated tool either completes or the turn closes.
	if agent == protocol.SessionAgentCopilot &&
		session.State == protocol.SessionStatePendingApproval &&
		state == protocol.StateWorking {
		return
	}

	d.logf("pty state update: session=%s agent=%s state=%s", sessionID, agent, state)
	d.store.UpdateState(sessionID, state)
	d.store.Touch(sessionID)
	updated := d.store.Get(sessionID)
	if updated == nil {
		return
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:   protocol.EventSessionStateChanged,
		Session: updated,
	})
}

// initHTTPServer creates the HTTP server synchronously to avoid race with Stop().
// Must be called before runHTTPServer().
func (d *Daemon) initHTTPServer() {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", d.handleWS)
	mux.HandleFunc("/health", d.handleHealth)

	port := os.Getenv("ATTN_WS_PORT")
	if port == "" {
		port = "9849"
	}

	d.httpServer = &http.Server{
		Addr:    "127.0.0.1:" + port,
		Handler: mux,
	}
}

// runHTTPServer starts listening. Must be called after initHTTPServer().
func (d *Daemon) runHTTPServer() {
	d.logf("WebSocket server starting on ws://%s/ws", d.httpServer.Addr)
	if err := d.httpServer.ListenAndServe(); err != http.ErrServerClosed {
		d.logf("HTTP server error: %v", err)
	}
}

func (d *Daemon) log(msg string) {
	if d.logger != nil {
		d.logger.Info(msg)
	}
}

func (d *Daemon) logf(format string, args ...interface{}) {
	if d.logger != nil {
		d.logger.Infof(format, args...)
	}
}

func shouldRunWorkerStartupProbe() bool {
	raw := strings.TrimSpace(strings.ToLower(os.Getenv("ATTN_PTY_SKIP_STARTUP_PROBE")))
	switch raw {
	case "1", "true", "yes", "on":
		return false
	default:
		return true
	}
}

func (d *Daemon) refreshGitHubHostsLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			if err := d.refreshGitHubHosts(); err != nil {
				d.logf("GitHub host refresh failed: %v", err)
			}
		}
	}
}

func (d *Daemon) refreshGitHubHosts() error {
	if d.ghRegistry == nil {
		d.ghRegistry = github.NewClientRegistry()
	}

	mockURL := strings.TrimSpace(os.Getenv("ATTN_MOCK_GH_URL"))
	if mockURL != "" {
		if err := d.registerMockClient(mockURL); err != nil {
			d.logf("Mock GitHub client not available: %v", err)
		}
		return nil
	}

	if err := github.RequireGHVersion("2.81.0"); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			d.logf("gh CLI not available: %v", err)
			d.addWarning(warnGHNotInstalled, "GitHub CLI not installed. PR monitoring disabled. Run: brew install gh")
		} else {
			d.logf("gh CLI version too old (need 2.81.0+): %v", err)
			d.addWarning(warnGHVersionTooOld, "GitHub CLI needs upgrade to v2.81.0+ for PR monitoring. Run: brew upgrade gh")
		}
		return nil
	}

	hosts, err := github.DiscoverHosts()
	if err != nil {
		d.logf("GitHub host discovery failed: %v", err)
		return nil
	}

	discovered := make(map[string]bool)
	for _, hostInfo := range hosts {
		if hostInfo.Host == "" {
			continue
		}
		token, err := github.GetTokenForHost(hostInfo.Host)
		if err != nil {
			d.logf("GitHub token fetch failed for %s: %v", hostInfo.Host, err)
			continue
		}
		client, err := github.NewClientForHost(hostInfo.Host, hostInfo.APIURL, token)
		if err != nil {
			d.logf("GitHub client create failed for %s: %v", hostInfo.Host, err)
			continue
		}
		d.ghRegistry.Register(hostInfo.Host, client)
		discovered[hostInfo.Host] = true
	}

	allowed := make(map[string]bool)
	for host := range discovered {
		allowed[host] = true
	}
	for _, host := range d.ghRegistry.Hosts() {
		if !allowed[host] {
			d.ghRegistry.Remove(host)
		}
	}

	return nil
}

func (d *Daemon) registerMockClient(mockURL string) error {
	token := strings.TrimSpace(os.Getenv("ATTN_MOCK_GH_TOKEN"))
	if token == "" {
		return fmt.Errorf("ATTN_MOCK_GH_TOKEN not set")
	}

	host := strings.TrimSpace(os.Getenv("ATTN_MOCK_GH_HOST"))
	if host == "" {
		host = hostFromURL(mockURL)
	}
	if host == "" {
		host = "mock.github.local"
	}

	client, err := github.NewClientForHost(host, mockURL, token)
	if err != nil {
		return err
	}

	for _, existing := range d.ghRegistry.Hosts() {
		d.ghRegistry.Remove(existing)
	}
	d.ghRegistry.Register(host, client)
	d.logf("Mock GitHub client registered for %s (%s)", host, mockURL)
	return nil
}

func hostFromURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

func (d *Daemon) githubAvailable() bool {
	if d.ghRegistry == nil {
		return false
	}
	return len(d.ghRegistry.Hosts()) > 0
}

func (d *Daemon) clientForPRID(id string) (*github.Client, string, int, string, error) {
	host, repo, number, err := protocol.ParsePRID(id)
	if err != nil {
		return nil, "", 0, "", err
	}
	if d.ghRegistry == nil {
		return nil, "", 0, "", fmt.Errorf("GitHub client not available")
	}
	client, ok := d.ghRegistry.Get(host)
	if !ok {
		return nil, "", 0, "", fmt.Errorf("no client for host %s", host)
	}
	return client, repo, number, host, nil
}

// acquirePIDLock ensures only one daemon instance runs at a time using flock.
// If another daemon is running, startup fails and the existing daemon keeps running.
func (d *Daemon) acquirePIDLock() error {
	// Open or create the PID file
	f, err := os.OpenFile(d.pidPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return fmt.Errorf("open PID file: %w", err)
	}

	// Try non-blocking exclusive lock first
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		existingPID := "unknown"
		if data, readErr := os.ReadFile(d.pidPath); readErr == nil {
			if pid := strings.TrimSpace(string(data)); pid != "" {
				existingPID = pid
			}
		}
		f.Close()
		return fmt.Errorf("daemon already running (pid %s)", existingPID)
	}

	// We have the lock - write our PID
	f.Truncate(0)
	f.Seek(0, 0)
	pid := os.Getpid()
	if _, err := f.WriteString(strconv.Itoa(pid)); err != nil {
		f.Close()
		return fmt.Errorf("write PID: %w", err)
	}
	f.Sync()

	// Keep file open to hold the lock
	d.pidFile = f
	d.logf("Acquired PID lock (PID %d, file %s)", pid, d.pidPath)

	return nil
}

// releasePIDLock unlocks and removes the PID file
func (d *Daemon) releasePIDLock() {
	if d.pidFile != nil {
		syscall.Flock(int(d.pidFile.Fd()), syscall.LOCK_UN)
		d.pidFile.Close()
		d.pidFile = nil
	}
	if err := os.Remove(d.pidPath); err != nil && !os.IsNotExist(err) {
		d.logf("Failed to remove PID file: %v", err)
	}
}

func (d *Daemon) handleConnection(conn net.Conn) {
	defer conn.Close()

	// Read message
	buf := make([]byte, 65536)
	n, err := conn.Read(buf)
	if err != nil {
		return
	}

	cmd, msg, err := protocol.ParseMessage(buf[:n])
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}

	switch cmd {
	case protocol.CmdRegister:
		d.handleRegister(conn, msg.(*protocol.RegisterMessage))
	case protocol.CmdUnregister:
		d.handleUnregister(conn, msg.(*protocol.UnregisterMessage))
	case protocol.CmdState:
		d.handleState(conn, msg.(*protocol.StateMessage))
	case protocol.CmdStop:
		d.handleStop(conn, msg.(*protocol.StopMessage))
	case protocol.CmdTodos:
		d.handleTodos(conn, msg.(*protocol.TodosMessage))
	case protocol.CmdQuery:
		d.handleQuery(conn, msg.(*protocol.QueryMessage))
	case protocol.CmdHeartbeat:
		d.handleHeartbeat(conn, msg.(*protocol.HeartbeatMessage))
	case protocol.CmdMute:
		d.handleMute(conn, msg.(*protocol.MuteMessage))
	case protocol.CmdQueryPRs:
		d.handleQueryPRs(conn, msg.(*protocol.QueryPRsMessage))
	case protocol.CmdMutePR:
		d.handleMutePR(conn, msg.(*protocol.MutePRMessage))
	case protocol.CmdMuteRepo:
		d.handleMuteRepo(conn, msg.(*protocol.MuteRepoMessage))
	case protocol.CmdCollapseRepo:
		d.handleCollapseRepo(conn, msg.(*protocol.CollapseRepoMessage))
	case protocol.CmdQueryRepos:
		d.handleQueryRepos(conn, msg.(*protocol.QueryReposMessage))
	case protocol.CmdQueryAuthors:
		d.handleQueryAuthors(conn, msg.(*protocol.QueryAuthorsMessage))
	case protocol.CmdFetchPRDetails:
		d.handleFetchPRDetails(conn, msg.(*protocol.FetchPRDetailsMessage))
	case protocol.CmdInjectTestPR:
		d.handleInjectTestPR(conn, msg.(*protocol.InjectTestPRMessage))
	case protocol.CmdInjectTestSession:
		d.handleInjectTestSession(conn, msg.(*protocol.InjectTestSessionMessage))
	case protocol.CmdListWorktrees:
		d.handleListWorktrees(conn, msg.(*protocol.ListWorktreesMessage))
	case protocol.CmdCreateWorktree:
		d.handleCreateWorktree(conn, msg.(*protocol.CreateWorktreeMessage))
	case protocol.CmdDeleteWorktree:
		d.handleDeleteWorktree(conn, msg.(*protocol.DeleteWorktreeMessage))
	default:
		d.sendError(conn, "unknown command")
	}
}

func (d *Daemon) handleRegister(conn net.Conn, msg *protocol.RegisterMessage) {
	d.logf("session registered: id=%s label=%s dir=%s", msg.ID, protocol.Deref(msg.Label), msg.Dir)
	existing := d.store.Get(msg.ID)

	// Get branch info
	branchInfo, _ := git.GetBranchInfo(msg.Dir)

	nowStr := string(protocol.TimestampNow())
	agent := protocol.NormalizeSessionAgent(protocol.Deref(msg.Agent), protocol.SessionAgentClaude)
	session := &protocol.Session{
		ID:             msg.ID,
		Label:          protocol.Deref(msg.Label),
		Agent:          agent,
		Directory:      msg.Dir,
		State:          protocol.SessionStateLaunching,
		StateSince:     nowStr,
		StateUpdatedAt: nowStr,
		LastSeen:       nowStr,
	}
	if branchInfo != nil {
		if branchInfo.Branch != "" {
			session.Branch = protocol.Ptr(branchInfo.Branch)
		}
		if branchInfo.IsWorktree {
			session.IsWorktree = protocol.Ptr(true)
		}
		if branchInfo.MainRepo != "" {
			session.MainRepo = protocol.Ptr(branchInfo.MainRepo)
		}
	}
	d.store.Add(session)

	// Track this location in recent locations
	label := filepath.Base(msg.Dir)
	d.store.UpsertRecentLocation(msg.Dir, label)

	d.sendOK(conn)

	// Broadcast session registration or update to WebSocket clients.
	eventType := protocol.EventSessionRegistered
	if existing != nil {
		eventType = protocol.EventSessionStateChanged
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:   eventType,
		Session: session,
	})
}

func (d *Daemon) handleUnregister(conn net.Conn, msg *protocol.UnregisterMessage) {
	// Get session before removing for broadcast
	sessions := d.store.List("")
	var session *protocol.Session
	for _, s := range sessions {
		if s.ID == msg.ID {
			session = s
			break
		}
	}

	d.terminateSession(msg.ID, syscall.SIGTERM)
	d.store.Remove(msg.ID)
	d.clearClassifiedTurn(msg.ID)
	d.clearClassifyingTurn(msg.ID)
	d.sendOK(conn)

	// Broadcast to WebSocket clients
	if session != nil {
		d.wsHub.Broadcast(&protocol.WebSocketEvent{
			Event:   protocol.EventSessionUnregistered,
			Session: session,
		})
	}
}

func (d *Daemon) handleState(conn net.Conn, msg *protocol.StateMessage) {
	d.logf("state update: id=%s state=%s", msg.ID, msg.State)
	d.store.UpdateState(msg.ID, msg.State)
	d.store.Touch(msg.ID)
	d.sendOK(conn)

	// Broadcast to WebSocket clients
	session := d.store.Get(msg.ID)
	if session != nil {
		d.logf("broadcasting state change (from hook): session=%s state=%s clients=%d", msg.ID, msg.State, d.wsHub.ClientCount())
		d.wsHub.Broadcast(&protocol.WebSocketEvent{
			Event:   protocol.EventSessionStateChanged,
			Session: session,
		})
	} else {
		d.logf("handleState: session %s not found, no broadcast", msg.ID)
	}
}

func (d *Daemon) handleStop(conn net.Conn, msg *protocol.StopMessage) {
	d.logf("handleStop: session=%s, transcript_path=%s", msg.ID, msg.TranscriptPath)
	d.store.Touch(msg.ID)
	d.sendOK(conn)

	// Async classification
	go d.classifySessionState(msg.ID, msg.TranscriptPath)
}

func (d *Daemon) classifySessionState(sessionID, transcriptPath string) {
	// Capture timestamp BEFORE starting classification
	// This prevents slow classifier results from overwriting newer state updates
	classificationStartTime := time.Now()
	d.logf("classifySessionState: starting for session=%s, transcript=%s", sessionID, transcriptPath)

	session := d.store.Get(sessionID)
	if session == nil {
		d.logf("classifySessionState: session %s not found, aborting", sessionID)
		return
	}

	// Check pending todos first (fast path)
	// Todos are stored as "[✓] task" (completed), "[→] task" (in_progress), "[ ] task" (pending)
	pendingCount := 0
	for _, todo := range session.Todos {
		if !strings.HasPrefix(todo, "[✓]") {
			pendingCount++
		}
	}
	d.logf("classifySessionState: session %s has %d total todos, %d pending", sessionID, len(session.Todos), pendingCount)
	if pendingCount > 0 {
		d.logf("classifySessionState: session %s has pending todos, setting waiting_input", sessionID)
		d.updateAndBroadcastStateWithTimestamp(sessionID, protocol.StateWaitingInput, classificationStartTime)
		return
	}

	resolvedTranscriptPath := d.resolveTranscriptPathForSession(session, transcriptPath)
	if resolvedTranscriptPath != transcriptPath {
		d.logf(
			"classifySessionState: session %s resolved transcript path %q -> %q",
			sessionID,
			transcriptPath,
			resolvedTranscriptPath,
		)
	}

	// Parse transcript for last assistant message
	d.logf("classifySessionState: parsing transcript for session %s", sessionID)
	lastMessage, assistantTurnID, err := d.extractLastAssistantMessage(session, resolvedTranscriptPath, 500, classificationStartTime)
	if err != nil {
		if errors.Is(err, errNoNewAssistantTurn) {
			d.logf("classifySessionState: no new assistant turn for session %s, skipping classification", sessionID)
			return
		}
		d.logf("classifySessionState: transcript parse error for %s: %v", sessionID, err)
		d.logf("classifySessionState: unknown reason=transcript_parse_error session=%s transcript=%s", sessionID, resolvedTranscriptPath)
		d.updateAndBroadcastStateWithTimestamp(sessionID, protocol.StateUnknown, classificationStartTime)
		return
	}
	if session.Agent == protocol.SessionAgentClaude && strings.TrimSpace(assistantTurnID) != "" {
		defer d.clearClassifyingTurn(sessionID)
	}

	lastMessage = strings.TrimSpace(lastMessage)
	if lastMessage == "" {
		d.logf("classifySessionState: empty last message for session %s, setting idle", sessionID)
		d.updateAndBroadcastStateWithTimestamp(sessionID, protocol.StateIdle, classificationStartTime)
		return
	}

	// Log truncated message
	logMsg := lastMessage
	if len(logMsg) > 100 {
		logMsg = logMsg[:100] + "..."
	}
	d.logf("classifySessionState: last message for session %s: %s", sessionID, logMsg)

	// Classify with LLM (can be slow - 30+ seconds)
	d.logf("classifySessionState: calling classifier for session %s", sessionID)
	state, err := d.classifyWithRetry(session, lastMessage, 30*time.Second)
	if err != nil {
		d.logf("classifySessionState: classifier error for %s: %v", sessionID, err)
		d.logf("classifySessionState: unknown reason=classifier_error session=%s err=%v", sessionID, err)
		state = protocol.StateUnknown
	}
	if err == nil && state == protocol.StateUnknown {
		d.logf("classifySessionState: unknown reason=classifier_unknown_response session=%s", sessionID)
	}

	d.logf("classifySessionState: session %s classified as %s", sessionID, state)
	if session.Agent == protocol.SessionAgentClaude && strings.TrimSpace(assistantTurnID) != "" {
		d.setClassifiedTurnID(sessionID, assistantTurnID)
	}
	d.updateAndBroadcastStateWithTimestamp(sessionID, state, classificationStartTime)
}

func (d *Daemon) classifyWithRetry(session *protocol.Session, text string, timeout time.Duration) (string, error) {
	state, err := d.runClassifier(session, text, timeout)
	attempt := 1
	for attempt < classifierRetryMaxAttempts && isClassifierRateLimitError(err) {
		backoff := classifierRetryBaseBackoff * time.Duration(1<<(attempt-1))
		d.logf(
			"classifySessionState: classifier retry after rate_limit_event session=%s attempt=%d/%d backoff=%s",
			session.ID,
			attempt+1,
			classifierRetryMaxAttempts,
			backoff,
		)
		time.Sleep(backoff)
		attempt++
		state, err = d.runClassifier(session, text, timeout)
	}
	if err == nil && attempt > 1 {
		d.logf("classifySessionState: classifier recovered after retry session=%s attempts=%d", session.ID, attempt)
	}
	return state, err
}

func (d *Daemon) runClassifier(session *protocol.Session, text string, timeout time.Duration) (string, error) {
	if d.classifier != nil {
		return d.classifier.Classify(text, timeout)
	}
	if session != nil {
		switch session.Agent {
		case protocol.SessionAgentCopilot:
			return classifier.ClassifyWithCopilot(text, timeout)
		case protocol.SessionAgentCodex:
			return classifier.ClassifyWithCodexExecutable(
				text,
				d.store.GetSetting(SettingCodexExecutable),
				timeout,
			)
		}
	}
	// Use Claude SDK for Claude sessions.
	return classifier.ClassifyWithClaude(text, timeout)
}

func isClassifierRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "rate_limit_event")
}

func (d *Daemon) resolveTranscriptPathForSession(session *protocol.Session, transcriptPath string) string {
	path := strings.TrimSpace(transcriptPath)
	if session == nil || session.Agent != protocol.SessionAgentClaude {
		return path
	}

	if path != "" {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	discovered := transcript.FindClaudeTranscript(session.ID)
	if discovered != "" {
		return discovered
	}

	return path
}

func (d *Daemon) extractLastAssistantMessage(session *protocol.Session, transcriptPath string, maxChars int, classificationStart time.Time) (string, string, error) {
	if session == nil || session.Agent != protocol.SessionAgentClaude {
		lastMessage, err := transcript.ExtractLastAssistantMessage(transcriptPath, maxChars)
		return lastMessage, "", err
	}

	deadline := time.Now().Add(claudeTranscriptRetryWindow)
	minAssistantTimestamp := classificationStart.Add(-claudeTranscriptFreshnessSkew)
	lastClassifiedTurnID := d.classifiedTurnID(session.ID)
	for {
		turn, err := transcript.ExtractLastAssistantTurnAfterLastUserSince(
			transcriptPath,
			maxChars,
			minAssistantTimestamp,
		)
		if err == nil && strings.TrimSpace(turn.Content) != "" {
			if strings.TrimSpace(turn.UUID) != "" && turn.UUID == lastClassifiedTurnID {
				err = errNoNewAssistantTurn
			} else {
				if session.Agent == protocol.SessionAgentClaude && strings.TrimSpace(turn.UUID) != "" {
					if !d.beginClassifyingTurn(session.ID, turn.UUID) {
						return "", "", errNoNewAssistantTurn
					}
				}
				return turn.Content, turn.UUID, nil
			}
		}
		if !time.Now().Before(deadline) {
			if err == nil {
				err = errNoNewAssistantTurn
			}
			return "", "", err
		}
		time.Sleep(claudeTranscriptRetryInterval)
	}
}

func (d *Daemon) classifiedTurnID(sessionID string) string {
	d.classifiedMu.Lock()
	defer d.classifiedMu.Unlock()
	if d.classifiedTurn == nil {
		return ""
	}
	return d.classifiedTurn[sessionID]
}

func (d *Daemon) setClassifiedTurnID(sessionID, turnID string) {
	d.classifiedMu.Lock()
	defer d.classifiedMu.Unlock()
	if d.classifiedTurn == nil {
		d.classifiedTurn = make(map[string]string)
	}
	d.classifiedTurn[sessionID] = turnID
}

func (d *Daemon) clearClassifiedTurn(sessionID string) {
	d.classifiedMu.Lock()
	defer d.classifiedMu.Unlock()
	if d.classifiedTurn == nil {
		return
	}
	delete(d.classifiedTurn, sessionID)
}

func (d *Daemon) beginClassifyingTurn(sessionID, turnID string) bool {
	d.classifiedMu.Lock()
	defer d.classifiedMu.Unlock()
	if d.classifyingTurn == nil {
		d.classifyingTurn = make(map[string]string)
	}
	if d.classifiedTurn != nil && d.classifiedTurn[sessionID] == turnID {
		return false
	}
	if d.classifyingTurn[sessionID] == turnID {
		return false
	}
	d.classifyingTurn[sessionID] = turnID
	return true
}

func (d *Daemon) clearClassifyingTurn(sessionID string) {
	d.classifiedMu.Lock()
	defer d.classifiedMu.Unlock()
	if d.classifyingTurn == nil {
		return
	}
	delete(d.classifyingTurn, sessionID)
}

func (d *Daemon) updateAndBroadcastState(sessionID, state string) {
	d.store.UpdateState(sessionID, state)

	// Broadcast to WebSocket clients
	session := d.store.Get(sessionID)
	if session != nil {
		d.logf("broadcasting state change: session=%s state=%s clients=%d", sessionID, state, d.wsHub.ClientCount())
		d.wsHub.Broadcast(&protocol.WebSocketEvent{
			Event:   protocol.EventSessionStateChanged,
			Session: session,
		})
	}
}

// updateAndBroadcastStateWithTimestamp updates state only if the timestamp is newer
// than the current state. Used by classifier to prevent stale results from overwriting
// newer state updates that arrived during classification.
func (d *Daemon) updateAndBroadcastStateWithTimestamp(sessionID, state string, updatedAt time.Time) {
	if d.store.UpdateStateWithTimestamp(sessionID, state, updatedAt) {
		// Broadcast to WebSocket clients
		session := d.store.Get(sessionID)
		if session != nil {
			d.logf("broadcasting state change (timestamped): session=%s state=%s clients=%d", sessionID, state, d.wsHub.ClientCount())
			d.wsHub.Broadcast(&protocol.WebSocketEvent{
				Event:   protocol.EventSessionStateChanged,
				Session: session,
			})
		}
	} else {
		d.logf("state update discarded: session=%s state=%s (newer state exists)", sessionID, state)
	}
}

// broadcastRateLimited broadcasts a rate limit event to WebSocket clients
func (d *Daemon) broadcastRateLimited(resource string, resetAt time.Time) {
	resetAtStr := string(protocol.NewTimestamp(resetAt))
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:             protocol.EventRateLimited,
		RateLimitResource: protocol.Ptr(resource),
		RateLimitResetAt:  protocol.Ptr(resetAtStr),
	})
}

func (d *Daemon) handleTodos(conn net.Conn, msg *protocol.TodosMessage) {
	d.store.UpdateTodos(msg.ID, msg.Todos)
	d.store.Touch(msg.ID)
	d.sendOK(conn)

	// Broadcast to WebSocket clients
	sessions := d.store.List("")
	for _, s := range sessions {
		if s.ID == msg.ID {
			d.wsHub.Broadcast(&protocol.WebSocketEvent{
				Event:   protocol.EventSessionTodosUpdated,
				Session: s,
			})
			break
		}
	}
}

func (d *Daemon) handleQuery(conn net.Conn, msg *protocol.QueryMessage) {
	sessions := d.store.List(protocol.Deref(msg.Filter))
	resp := protocol.Response{
		Ok:       true,
		Sessions: protocol.SessionsToValues(sessions),
	}
	json.NewEncoder(conn).Encode(resp)
}

func (d *Daemon) handleHeartbeat(conn net.Conn, msg *protocol.HeartbeatMessage) {
	d.store.Touch(msg.ID)
	d.sendOK(conn)
}

func (d *Daemon) handleMute(conn net.Conn, msg *protocol.MuteMessage) {
	d.store.ToggleMute(msg.ID)
	d.sendOK(conn)
}

func (d *Daemon) handleQueryPRs(conn net.Conn, msg *protocol.QueryPRsMessage) {
	prs := d.store.ListPRs(protocol.Deref(msg.Filter))
	resp := protocol.Response{
		Ok:  true,
		Prs: protocol.PRsToValues(prs),
	}
	json.NewEncoder(conn).Encode(resp)
}

func (d *Daemon) handleMutePR(conn net.Conn, msg *protocol.MutePRMessage) {
	d.store.ToggleMutePR(msg.ID)
	d.sendOK(conn)
}

func (d *Daemon) handleMuteRepo(conn net.Conn, msg *protocol.MuteRepoMessage) {
	d.store.ToggleMuteRepo(msg.Repo)
	d.sendOK(conn)
}

func (d *Daemon) handleCollapseRepo(conn net.Conn, msg *protocol.CollapseRepoMessage) {
	d.store.SetRepoCollapsed(msg.Repo, msg.Collapsed)
	d.sendOK(conn)
}

func (d *Daemon) handleQueryRepos(conn net.Conn, msg *protocol.QueryReposMessage) {
	repos := d.store.ListRepoStates()
	resp := protocol.Response{
		Ok:    true,
		Repos: protocol.RepoStatesToValues(repos),
	}
	json.NewEncoder(conn).Encode(resp)
}

func (d *Daemon) handleQueryAuthors(conn net.Conn, msg *protocol.QueryAuthorsMessage) {
	authors := d.store.ListAuthorStates()
	resp := protocol.Response{
		Ok:      true,
		Authors: protocol.AuthorStatesToValues(authors),
	}
	json.NewEncoder(conn).Encode(resp)
}

func (d *Daemon) fetchPRDetailsForID(id string) ([]*protocol.PR, error) {
	if !d.githubAvailable() {
		return nil, fmt.Errorf("GitHub client not available")
	}

	host, repo, _, err := protocol.ParsePRID(id)
	if err != nil {
		return nil, err
	}

	client, ok := d.ghRegistry.Get(host)
	if !ok {
		return nil, fmt.Errorf("no client for host %s", host)
	}

	// Get all PRs for this repo + host
	prs := d.store.ListPRsByRepoHost(repo, host)

	// Fetch details for each PR that needs refresh
	for _, pr := range prs {
		if pr.NeedsDetailRefresh() {
			details, err := client.FetchPRDetails(pr.Repo, pr.Number)
			if err != nil {
				d.logf("Failed to fetch details for %s: %v", pr.ID, err)
				continue
			}
			d.store.UpdatePRDetails(pr.ID, details.Mergeable, details.MergeableState, details.CIStatus, details.ReviewStatus, details.HeadSHA, details.HeadBranch)
		}
	}

	// Return updated PRs
	updatedPRs := d.store.ListPRsByRepoHost(repo, host)
	return updatedPRs, nil
}

func (d *Daemon) handleFetchPRDetails(conn net.Conn, msg *protocol.FetchPRDetailsMessage) {
	updatedPRs, err := d.fetchPRDetailsForID(msg.ID)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}

	resp := protocol.Response{
		Ok:  true,
		Prs: protocol.PRsToValues(updatedPRs),
	}
	json.NewEncoder(conn).Encode(resp)
}

func (d *Daemon) sendOK(conn net.Conn) {
	resp := protocol.Response{Ok: true}
	json.NewEncoder(conn).Encode(resp)
}

func (d *Daemon) sendError(conn net.Conn, errMsg string) {
	resp := protocol.Response{Ok: false, Error: protocol.Ptr(errMsg)}
	json.NewEncoder(conn).Encode(resp)
}

func (d *Daemon) pollPRs() {
	if !d.githubAvailable() {
		d.log("GitHub client not available, PR polling disabled")
		return
	}

	d.log("PR polling started (90s interval)")

	// Initial poll
	d.doPRPoll()

	ticker := time.NewTicker(90 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			d.doPRPoll()
		}
	}
}

func (d *Daemon) doPRPoll() {
	if !d.githubAvailable() {
		return
	}

	var allPRs []*protocol.PR
	skippedHosts := make(map[string]bool)
	var earliestReset time.Time

	for _, host := range d.ghRegistry.Hosts() {
		client, ok := d.ghRegistry.Get(host)
		if !ok {
			continue
		}

		if limited, resetAt := client.IsRateLimited("search"); limited {
			d.logf("PR poll skipped for %s: search API rate limited until %s", host, resetAt.Format(time.RFC3339))
			skippedHosts[host] = true
			if earliestReset.IsZero() || resetAt.Before(earliestReset) {
				earliestReset = resetAt
			}
			continue
		}

		prs, err := client.FetchAll()
		if err != nil {
			if errors.Is(err, github.ErrRateLimited) {
				if info := client.GetRateLimit("search"); info != nil {
					d.logf("PR poll rate limited for %s until %s", host, info.ResetAt.Format(time.RFC3339))
					if earliestReset.IsZero() || info.ResetAt.Before(earliestReset) {
						earliestReset = info.ResetAt
					}
				} else {
					d.logf("PR poll rate limited for %s (unknown reset time)", host)
					resetAt := time.Now().Add(60 * time.Second)
					if earliestReset.IsZero() || resetAt.Before(earliestReset) {
						earliestReset = resetAt
					}
				}
				skippedHosts[host] = true
				continue
			}
			if errors.Is(err, github.ErrSelfRateLimited) {
				d.logf("PR poll: self-rate-limited for %s, skipping", host)
				skippedHosts[host] = true
				continue
			}
			d.logf("PR poll error for %s: %v", host, err)
			skippedHosts[host] = true
			continue
		}

		allPRs = append(allPRs, prs...)
	}

	if !earliestReset.IsZero() {
		d.broadcastRateLimited("search", earliestReset)
	}

	if len(skippedHosts) > 0 {
		existing := d.store.ListPRs("")
		for _, pr := range existing {
			host := pr.Host
			if host == "" {
				if parsedHost, _, _, err := protocol.ParsePRID(pr.ID); err == nil {
					host = parsedHost
				}
			}
			if host != "" && skippedHosts[host] {
				allPRs = append(allPRs, pr)
			}
		}
	}

	d.store.SetPRs(allPRs)

	// Broadcast to WebSocket clients
	currentPRs := d.store.ListPRs("")
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event: protocol.EventPRsUpdated,
		Prs:   protocol.PRsToValues(currentPRs),
	})

	// Count waiting (non-muted) PRs for logging
	waiting := 0
	for _, pr := range currentPRs {
		if pr.State == protocol.PRStateWaiting && !pr.Muted {
			waiting++
		}
	}
	d.logf("PR poll: %d PRs (%d waiting)", len(currentPRs), waiting)

	// Run detail refresh after list poll
	d.doDetailRefresh()
}

// doDetailRefresh fetches details for PRs that need refresh based on heat state
func (d *Daemon) doDetailRefresh() {
	if !d.githubAvailable() {
		return
	}

	// First decay heat states
	d.store.DecayHeatStates()

	// Get PRs needing refresh
	prs := d.store.GetPRsNeedingDetailRefresh()
	if len(prs) == 0 {
		return
	}

	d.logf("Detail refresh: %d PRs need refresh", len(prs))

	refreshedCount := 0
	limitedHosts := make(map[string]time.Time)
	for _, pr := range prs {
		host := pr.Host
		if host == "" {
			if parsedHost, _, _, err := protocol.ParsePRID(pr.ID); err == nil {
				host = parsedHost
			}
		}
		if host == "" {
			continue
		}
		if _, limited := limitedHosts[host]; limited {
			continue
		}

		client, ok := d.ghRegistry.Get(host)
		if !ok {
			d.logf("Detail refresh: no client for host %s", host)
			continue
		}

		if limited, resetAt := client.IsRateLimited("core"); limited {
			d.logf("Detail refresh: %s rate limited until %v", host, resetAt)
			limitedHosts[host] = resetAt
			continue
		}

		details, err := client.FetchPRDetails(pr.Repo, pr.Number)
		if err != nil {
			// If rate limited (GitHub or self-imposed), stop for this host
			if errors.Is(err, github.ErrRateLimited) {
				if info := client.GetRateLimit("core"); info != nil {
					d.logf("Detail refresh: %s rate limited, stopping host refresh", host)
					limitedHosts[host] = info.ResetAt
				}
				continue
			}
			if errors.Is(err, github.ErrSelfRateLimited) {
				d.logf("Detail refresh: %s self-rate-limited, stopping host refresh", host)
				limitedHosts[host] = time.Now().Add(60 * time.Second)
				continue
			}
			d.logf("Failed to fetch details for %s: %v", pr.ID, err)
			continue
		}

		// Check if SHA changed (new commits) - triggers hot state
		prHeadSHA := protocol.Deref(pr.HeadSHA)
		if prHeadSHA != "" && details.HeadSHA != prHeadSHA {
			d.store.SetPRHot(pr.ID)
		}

		d.store.UpdatePRDetails(pr.ID, details.Mergeable, details.MergeableState, details.CIStatus, details.ReviewStatus, details.HeadSHA, details.HeadBranch)
		refreshedCount++
	}

	if len(limitedHosts) > 0 {
		var earliest time.Time
		for _, resetAt := range limitedHosts {
			if resetAt.IsZero() {
				continue
			}
			if earliest.IsZero() || resetAt.Before(earliest) {
				earliest = resetAt
			}
		}
		if !earliest.IsZero() {
			d.broadcastRateLimited("core", earliest)
		}
	}

	if refreshedCount > 0 {
		d.logf("Detail refresh: updated %d PRs", refreshedCount)
		// Broadcast updated PRs
		d.broadcastPRs()
	}
}

// fetchAllPRDetails fetches details for all visible PRs (called on app launch)
func (d *Daemon) fetchAllPRDetails() {
	if !d.githubAvailable() {
		return
	}

	// Get all visible PRs (not muted)
	allPRs := d.store.ListPRs("")
	if len(allPRs) == 0 {
		return
	}

	d.logf("App launch: fetching details for %d PRs", len(allPRs))

	refreshedCount := 0
	limitedHosts := make(map[string]time.Time)
	for _, pr := range allPRs {
		// Skip muted PRs and PRs from muted repos
		if pr.Muted {
			continue
		}
		repoState := d.store.GetRepoState(pr.Repo)
		if repoState != nil && repoState.Muted {
			continue
		}

		host := pr.Host
		if host == "" {
			if parsedHost, _, _, err := protocol.ParsePRID(pr.ID); err == nil {
				host = parsedHost
			}
		}
		if host == "" {
			continue
		}
		if _, limited := limitedHosts[host]; limited {
			continue
		}

		client, ok := d.ghRegistry.Get(host)
		if !ok {
			d.logf("App launch: no client for host %s", host)
			continue
		}

		if limited, resetAt := client.IsRateLimited("core"); limited {
			d.logf("App launch: %s rate limited until %v", host, resetAt)
			limitedHosts[host] = resetAt
			continue
		}

		details, err := client.FetchPRDetails(pr.Repo, pr.Number)
		if err != nil {
			// If rate limited (GitHub or self-imposed), stop the loop for this host
			if errors.Is(err, github.ErrRateLimited) {
				if info := client.GetRateLimit("core"); info != nil {
					d.logf("App launch: %s rate limited, stopping host fetch loop", host)
					limitedHosts[host] = info.ResetAt
				}
				continue
			}
			if errors.Is(err, github.ErrSelfRateLimited) {
				d.logf("App launch: %s self-rate-limited, stopping host fetch loop", host)
				limitedHosts[host] = time.Now().Add(60 * time.Second)
				continue
			}
			d.logf("Failed to fetch details for %s: %v", pr.ID, err)
			continue
		}

		d.store.UpdatePRDetails(pr.ID, details.Mergeable, details.MergeableState, details.CIStatus, details.ReviewStatus, details.HeadSHA, details.HeadBranch)
		refreshedCount++
	}

	if len(limitedHosts) > 0 {
		var earliest time.Time
		for _, resetAt := range limitedHosts {
			if resetAt.IsZero() {
				continue
			}
			if earliest.IsZero() || resetAt.Before(earliest) {
				earliest = resetAt
			}
		}
		if !earliest.IsZero() {
			d.broadcastRateLimited("core", earliest)
		}
	}

	if refreshedCount > 0 {
		d.logf("App launch: updated %d PRs", refreshedCount)
		d.broadcastPRs()
	}
}

func (d *Daemon) handleInjectTestPR(conn net.Conn, msg *protocol.InjectTestPRMessage) {
	if msg.PR.ID == "" {
		d.sendError(conn, "PR ID cannot be empty")
		return
	}

	// Add PR directly to store
	d.store.AddPR(&msg.PR)
	d.sendOK(conn)

	// Broadcast to WebSocket clients
	allPRs := d.store.ListPRs("")
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event: protocol.EventPRsUpdated,
		Prs:   protocol.PRsToValues(allPRs),
	})
}

func (d *Daemon) handleInjectTestSession(conn net.Conn, msg *protocol.InjectTestSessionMessage) {
	if msg.Session.ID == "" {
		d.sendError(conn, "Session ID cannot be empty")
		return
	}

	msg.Session.Agent = protocol.NormalizeSessionAgent(msg.Session.Agent, protocol.SessionAgentCodex)

	// Add session directly to store
	d.store.Add(&msg.Session)
	d.sendOK(conn)

	// Broadcast to WebSocket clients
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:   protocol.EventSessionRegistered,
		Session: &msg.Session,
	})
}

// RefreshPRs triggers an immediate PR refresh
func (d *Daemon) RefreshPRs() {
	if !d.githubAvailable() {
		return
	}
	d.doPRPoll()
}

// doRefreshPRsWithResult triggers PR refresh and returns any error
func (d *Daemon) doRefreshPRsWithResult() error {
	if !d.githubAvailable() {
		return fmt.Errorf("GitHub client not available")
	}

	var allPRs []*protocol.PR
	skippedHosts := make(map[string]bool)
	var firstErr error
	successCount := 0

	for _, host := range d.ghRegistry.Hosts() {
		client, ok := d.ghRegistry.Get(host)
		if !ok {
			continue
		}
		prs, err := client.FetchAll()
		if err != nil {
			d.logf("PR refresh error for %s: %v", host, err)
			if firstErr == nil {
				firstErr = err
			}
			skippedHosts[host] = true
			continue
		}
		successCount++
		allPRs = append(allPRs, prs...)
	}

	if len(skippedHosts) > 0 {
		existing := d.store.ListPRs("")
		for _, pr := range existing {
			host := pr.Host
			if host == "" {
				if parsedHost, _, _, err := protocol.ParsePRID(pr.ID); err == nil {
					host = parsedHost
				}
			}
			if host != "" && skippedHosts[host] {
				allPRs = append(allPRs, pr)
			}
		}
	}

	d.store.SetPRs(allPRs)

	// Broadcast to WebSocket clients
	currentPRs := d.store.ListPRs("")
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event: protocol.EventPRsUpdated,
		Prs:   protocol.PRsToValues(currentPRs),
	})

	d.logf("PR refresh: %d PRs fetched", len(currentPRs))
	if successCount == 0 && firstErr != nil {
		return fmt.Errorf("failed to fetch PRs: %w", firstErr)
	}
	return nil
}

// fetchPRDetailsImmediate fetches details for a single PR immediately and sets it hot
func (d *Daemon) fetchPRDetailsImmediate(prID string) {
	if !d.githubAvailable() {
		return
	}

	pr := d.store.GetPR(prID)
	if pr == nil {
		return
	}

	// Skip if muted
	if pr.Muted {
		return
	}
	// Skip if repo is muted
	repoState := d.store.GetRepoState(pr.Repo)
	if repoState != nil && repoState.Muted {
		return
	}

	host := pr.Host
	if host == "" {
		if parsedHost, _, _, err := protocol.ParsePRID(pr.ID); err == nil {
			host = parsedHost
		}
	}
	if host == "" {
		return
	}

	client, ok := d.ghRegistry.Get(host)
	if !ok {
		d.logf("Immediate fetch: no client for host %s", host)
		return
	}

	// Check if already rate limited before making request
	if limited, resetAt := client.IsRateLimited("core"); limited {
		d.logf("Immediate fetch skipped for %s: rate limited until %v", prID, resetAt)
		return
	}

	d.store.SetPRHot(prID)

	details, err := client.FetchPRDetails(pr.Repo, pr.Number)
	if err != nil {
		// If rate limited (GitHub or self-imposed), skip
		if errors.Is(err, github.ErrRateLimited) {
			d.logf("Immediate fetch for %s: rate limited", prID)
			if info := client.GetRateLimit("core"); info != nil {
				d.broadcastRateLimited("core", info.ResetAt)
			}
			return
		}
		if errors.Is(err, github.ErrSelfRateLimited) {
			d.logf("Immediate fetch for %s: self-rate-limited", prID)
			return
		}
		d.logf("Immediate fetch failed for %s: %v", prID, err)
		return
	}

	d.store.UpdatePRDetails(prID, details.Mergeable, details.MergeableState, details.CIStatus, details.ReviewStatus, details.HeadSHA, details.HeadBranch)
	d.logf("Immediate fetch complete for %s (heat=hot)", prID)
}

// monitorBranches polls git branch info for all sessions every 5 seconds
func (d *Daemon) monitorBranches() {
	d.log("Branch monitoring started (5s interval)")

	// Initial check
	d.checkAllBranches()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			d.checkAllBranches()
		}
	}
}

func (d *Daemon) checkAllBranches() {
	sessions := d.store.List("")
	changed := false

	for _, session := range sessions {
		info, err := git.GetBranchInfo(session.Directory)
		if err != nil {
			continue
		}

		if info.Branch != protocol.Deref(session.Branch) || info.IsWorktree != protocol.Deref(session.IsWorktree) {
			d.store.UpdateBranch(session.ID, info.Branch, info.IsWorktree, info.MainRepo)
			changed = true
			d.logf("Branch changed: session=%s branch=%s isWorktree=%v", session.ID, info.Branch, info.IsWorktree)
		}
	}

	if changed {
		d.broadcastSessionsUpdated()
	}
}

func (d *Daemon) broadcastSessionsUpdated() {
	if d.wsHub == nil || d.store == nil {
		return
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:    protocol.EventSessionsUpdated,
		Sessions: protocol.SessionsToValues(d.store.List("")),
	})
}

// handleHealth returns daemon health status
func (d *Daemon) handleHealth(w http.ResponseWriter, r *http.Request) {
	sessions := d.store.List("")
	prs := d.store.ListPRs("")

	health := map[string]interface{}{
		"status":             "ok",
		"protocol":           protocol.ProtocolVersion,
		"daemon_instance_id": d.daemonInstanceID,
		"sessions":           len(sessions),
		"prs":                len(prs),
		"ws_clients":         d.wsHub.ClientCount(),
		"github_available":   d.githubAvailable(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(health)
}
