package daemon

import (
	"bufio"
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
	"sync/atomic"
	"syscall"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/classifier"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/diag"
	"github.com/victorarias/attn/internal/fsdoc"
	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/github"
	"github.com/victorarias/attn/internal/hub"
	"github.com/victorarias/attn/internal/logging"
	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/pathutil"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/tasks"
	"github.com/victorarias/attn/internal/transcript"
	"github.com/victorarias/attn/internal/workspacelayout"
)

type repoCache struct {
	fetchedAt time.Time
	branches  []protocol.Branch
}

type workerReconcileReport struct {
	Created           int
	StateUpdated      int
	MarkedIdle        int
	MarkedRecoverable int
	Reaped            int
	SkippedIdle       int
	SkippedRecent     int
	SkippedShell      int
	LikelyAlive       int
	LivenessUnknown   int
	MissingMetadata   int
	Changed           bool
}

type longRunSession struct {
	workingSince       time.Time
	deferredTranscript string
	needsReview        bool
}

const (
	longRunReviewThreshold = 5 * time.Minute
	forcedStopSuppressTTL  = 30 * time.Second
	branchMonitorInterval  = 15 * time.Second

	// backupInterval is how often the daemon takes a rotating snapshot of the
	// SQLite store in the background. backupKeep is how many rotating
	// snapshots survive pruning (older ones are deleted). See backup.go.
	backupInterval = 6 * time.Hour
	backupKeep     = 12

	startupRecoveryRetryMax       = 2
	startupRecoveryRetryDelay     = 500 * time.Millisecond
	deferredRecoveryMaxAttempts   = 3
	deferredRecoveryRetryInterval = 10 * time.Second
	deferredRecoveryRPCTimeout    = 5 * time.Second
	workerStartupProbeTimeout     = 20 * time.Second

	warnPersistenceDegraded       = "persistence_degraded"
	warnWorkerRecoveryPartial     = "worker_recovery_partial"
	warnStaleSessionsPruned       = "stale_sessions_pruned"
	warnStaleSessionMissingWorker = "stale_session_missing_worker"
	warnPTYBackendFallback        = "pty_backend_fallback"
	warnPTYBackendUnsupported     = "pty_backend_unsupported"
	warnGHNotInstalled            = "gh_not_installed"
	warnGHVersionTooOld           = "gh_version_too_old"
)

// Daemon manages Claude sessions
type Daemon struct {
	socketPath                 string
	pidPath                    string
	pidFile                    *os.File // Held open with flock for exclusive access
	dataRoot                   string
	daemonInstanceID           string
	store                      *store.Store
	automationMu               sync.Mutex // serializes idempotent ensure/adopt delivery per profile
	automationObservationMu    sync.Mutex
	automationObservationLocks map[string]*sync.Mutex
	automationRepoMu           sync.Mutex
	automationRepos            map[string]*sync.Mutex
	// automationDeliveryHook replaces only the final delivery call in focused
	// provider-observation tests; production always leaves it nil.
	automationDeliveryHook func(*store.AutomationRun) error
	listener               net.Listener
	httpServer             *http.Server
	httpHandler            http.Handler
	diagServer             *diag.Server // opt-in loopback pprof/expvar; nil unless ATTN_PPROF set
	wsHub                  *wsHub
	done                   chan struct{}
	logger                 *logging.Logger
	debugLogging           bool // cached DEBUG>=debug; gates per-chunk PTY hot-path logs
	ghRegistry             *github.ClientRegistry
	hubManager             *hub.Manager
	classifier             Classifier // Optional, uses package-level classifier.Classify if nil
	repoCaches             map[string]*repoCache
	repoCacheMu            sync.RWMutex
	gitCoordMu             sync.Mutex
	gitCoord               *gitCoordinator
	warnings               []protocol.DaemonWarning
	warningsMu             sync.RWMutex
	ptyBackend             ptybackend.Backend
	watchersMu             sync.Mutex
	transcriptWatch        map[string]*transcriptWatcher
	classifiedMu           sync.Mutex
	classifiedTurn         map[string]string
	classifyingTurn        map[string]string
	// classificationTranscriptExtractor is a private test seam for exercising
	// classification outcomes without waiting on an agent driver's retry policy.
	// Production leaves it nil and uses extractLastAssistantMessage below.
	classificationTranscriptExtractor func(*protocol.Session, string, int, time.Time) (string, string, error)
	longRunMu                         sync.Mutex
	longRun                           map[string]longRunSession
	forcedStopMu                      sync.Mutex
	forcedStop                        map[string]time.Time
	pendingResumeMu                   sync.Mutex
	pendingResumeID                   map[string]string
	// Orphaned-ticket reconciliation (docs/plans/2026-07-01-orphaned-ticket-
	// reconciliation.md): when an owning session dies with a non-terminal ticket,
	// a capped headless classifier judges the dead transcript against the brief.
	// ticketReconcileExec is the classifier spawn — New() wires the real claude
	// headless run; it stays nil on test daemons so unit tests never shell out
	// (the executor logs and skips). ticketReconcileDone is a test observation hook
	// fired when a reconcile task run reaches any terminal outcome. Concurrency is
	// no longer a bespoke semaphore here — the durable runner bounds it per-kind
	// (reconcileKind at ticketReconcileConcurrency). ticketOrphanFirstSeen is the
	// sweep's grace tracker (ticket id -> first pass that saw the owner dead);
	// in-memory by design — a restart merely restarts the grace clock. All
	// lazy/nil-safe under ticketReconcileMu.
	ticketReconcileMu     sync.Mutex
	ticketReconcileExec   func(ctx context.Context, in ticketReconcileInputs) (agentdriver.HeadlessTaskResult, error)
	ticketReconcileDone   func(ticketID string)
	ticketOrphanFirstSeen map[string]time.Time
	// ticketReconcilePRFetch is the verdict ground-truth check's PR-state
	// lookup seam: nil in production (reconcileGroundTruth derives the fetcher
	// from ghRegistry per-host); tests set it to a fake so no network runs.
	ticketReconcilePRFetch prStateFetcher
	// ticketArtifactMu serializes attach installation with its durable ticket
	// receipt so concurrent submissions cannot race on destination names.
	ticketArtifactMu  sync.Mutex
	delegationMu      sync.Mutex
	delegationRunning map[string]bool
	// deterministic slow-preparation seam used by delegation idempotency tests.
	delegationWorktreePrepareHook func(path string)
	// reloadingSessions marks sessions whose agent is being re-spawned in place
	// (chief-of-staff assign/demote reload). handlePTYExit consumes the flag to
	// suppress the killed worker's session_exited so the reload reads as a runtime
	// replacement, not a session close. Lazily initialized under reloadingMu.
	reloadingMu       sync.Mutex
	reloadingSessions map[string]bool
	// reloadLocks serializes the kill→remove→spawn composite per session so two
	// concurrent reloads of the same session (a double-toggle, or a role transfer)
	// cannot interleave and tear each other's respawn down. Lazily initialized.
	reloadLocksMu sync.Mutex
	reloadLocks   map[string]*sync.Mutex
	// reloadKills marks sessions whose next process exit was caused by a
	// client-initiated reload (kill_session with reload:true, followed by a
	// spawn_session of the same id). Unlike reloadingSessions it suppresses ONLY
	// the ticket crash/reconcile seam in handlePTYExit — the rest of exit
	// processing (backend remove, idle clobber, session_exited broadcast) must
	// still run because the client's reload flow depends on it. Timestamped so a
	// stale mark (reload kill that never produced an exit) cannot swallow a real
	// crash later. Lazily initialized under reloadingMu.
	reloadKills map[string]time.Time
	// nudge countdown (see nudge_countdown.go): every ticket doorbell arms a
	// visible per-session countdown instead of injecting immediately. The
	// currently-selected session's countdown is paused; the timer fire is the only
	// place a real doorbell happens, and only if no genuine user keystroke landed in
	// the guard window (the anti-splice guarantee). All maps lazy-init so a
	// directly-constructed test daemon is nil-safe.
	// doorbellMu serializes authoritative session-state commits with a complete
	// doorbell write. This keeps a pending_approval report from interleaving
	// between the prompt and its trailing Enter.
	doorbellMu                 sync.Mutex
	nudgeMu                    sync.Mutex
	nudgeCountdowns            map[string]*nudgeCountdown                 // presence == a running (unpaused) countdown
	unreadCache                map[string]bool                            // per-session unread ticket activity, for cheap broadcast decoration
	deliveryMu                 sync.Mutex                                 // serializes consumes, catch-up, deadline rebuilds, and nudge fire-time checks
	watchLeaseUntil            map[string]time.Time                       // ephemeral live-watch lease per session
	nudgeWindowOverride        time.Duration                              // 0 => defaultNudgeCountdownWindow; a short test override otherwise
	ticketBufferWindowOverride time.Duration                              // 0 => defaultTicketBufferWindow; test-only override
	nudgeFireHook              func(sessionID, action string)             // tests only: invoked at the end of a countdown fire
	ticketRebuildBeforeArmHook func(sessionID string, deadline time.Time) // tests only: invoked while deliveryMu is held
	lastInputMu                sync.Mutex
	lastUserInputAt            map[string]time.Time // per-session keystroke recency — the fire-time splice guard
	recoveryMu                 sync.RWMutex
	recovering                 bool
	notebookMu                 sync.Mutex
	notebookStore              *notebook.Store
	// notebookWatcher observes notebook.root for external edits; guarded by its
	// own mutex (distinct from notebookMu) so notebookStoreFor can start it
	// without nesting locks. Lazily started on first notebook use.
	notebookWatcherMu   sync.Mutex
	notebookWatcher     *notebook.Watcher
	notebookWatchedRoot string
	// fsStores is the generic filesystem view, keyed by resolved absolute root.
	// The notebook-root entry is the raw layer beneath the curated notebook
	// surface and shares the one root watcher started by ensureNotebookWatcher;
	// other roots (arbitrary editor roots) get their own Store but no watcher yet.
	fsMu     sync.Mutex
	fsStores map[string]*fsdoc.Store
	// fsWatchMu guards fsWatchers, the per-root registry of client-refcounted
	// watchers for fs_watch/fs_unwatch. Never holds an entry for the notebook
	// root — that watcher is always-on via ensureNotebookWatcher instead.
	fsWatchMu           sync.Mutex
	fsWatchers          map[string]*fsRootWatch
	pendingInitialWS    map[*wsClient]struct{}
	startedOnce         sync.Once
	startedCh           chan struct{}
	tailscale           *tailscaleRuntime
	plugins             *pluginRegistry
	pluginSupervisorMu  sync.Mutex
	pluginSupervisor    *pluginSupervisor
	pluginHealthEnabled bool
	pluginDriverMu      sync.Mutex
	pluginLaunching     map[string]pluginSessionLaunch
	pluginReports       map[string][]pendingPluginReport
	pluginExits         map[string]ptybackend.ExitInfo
	pluginDir           string
	bundledPluginDir    string
	removePlugin        func(pluginDir, name string) error
	pluginActionMu      sync.Mutex
	bundledPluginMu     sync.Mutex
	bundledPluginSet    map[string]struct{}
	bundledPluginLoaded bool

	worktreePluginCallTimeout         time.Duration
	worktreeCreateProviderCallTimeout time.Duration

	loginShellEnvMu sync.RWMutex
	loginShellEnv   []string

	// terminalTheme is the daemon-global OSC 10/11/12 color set the frontend
	// pushed via set_terminal_theme. Seeds every new spawn's SpawnOptions.Theme
	// and is fanned out to already-live sessions on change (ws_pty.go).
	terminalThemeMu sync.Mutex
	terminalTheme   pty.TerminalTheme

	// Workspace registry for Tauri and remote clients. Backed by the store for
	// workspace identity, session membership, and daemon-owned tile
	// geometry.
	workspaces *workspaceRegistry

	// The UI reports its selected session and workspace independently because a
	// tile-only workspace has no session id. CLI tile and browser commands use
	// this context when no explicit target is provided.
	selectedSessionMu   sync.RWMutex
	selectedSessionID   string
	selectedWorkspaceID string

	// openMarkdownMu serializes openMarkdownTile's check-then-dock against
	// itself. Layout saves are last-write-wins snapshots, so two concurrent
	// opens of different files in one workspace would otherwise both read the
	// same layout and the second save would silently drop the first tile.
	openMarkdownMu sync.Mutex

	// lastUserActivityAtNano is the UnixNano timestamp of the most recent
	// UI-origin websocket command the daemon observed (see
	// isUserPresenceCommand), a proxy for "the user is at the app right now".
	// Surfaced on the ticket inbox result so a watching agent can decide
	// whether to push a notification or hold it. Zero means no user activity
	// has been observed since the daemon started. atomic because it is a
	// single independent value with no compound invariant to guard.
	lastUserActivityAtNano atomic.Int64

	// markdownSeen fingerprints open markdown files so the content watcher only
	// broadcasts when a file actually changes on disk.
	markdownSeenMu sync.Mutex
	markdownSeen   map[string]tileContentSig

	browserControlMu sync.Mutex
	browserControl   map[string]browserControlPending

	// lastBackupMu guards lastBackupAt, the UTC timestamp of the most recent
	// successful performDatabaseBackup call. Zero value means no backup has
	// succeeded yet this process lifetime. Surfaced read-only in the settings
	// payload as SettingDBLastBackupAt (see ws_settings.go).
	lastBackupMu sync.Mutex
	lastBackupAt time.Time

	// Durable workflow engine IPC state. The engine runs in a separate process
	// (the `attn workflow run` CLI); the daemon persists, coalesced-broadcasts
	// run updates to the read-only UI, and relays cancel to the engine sink.
	// All maps lazy-init so a directly-constructed test daemon is nil-safe.
	workflowBroadcastMu   sync.Mutex
	workflowDirty         map[string]bool
	workflowEngineMu      sync.Mutex
	workflowEngineConn    map[string]workflowEngineSink
	workflowBroadcastHook func(*protocol.WorkflowRunUpdatedMessage) // optional, tests only
	workflowAttentionHook func(attention.Result)                    // optional, tests only
	ticketsBroadcastHook  func([]protocol.Ticket)                   // optional, tests only

	workspaceContextCheckoutMu sync.Mutex

	// compactRunner is the durable task runner that owns the keeper's
	// workspace-context compaction duty (kind "compact_context") and the
	// notebook-narration tasks. It replaces the bespoke time.AfterFunc scheduling +
	// single-flight/cancel/commit-fence guards.
	//
	// compactRunnerMu guards the POINTER swap only. startCompactRunner runs late in
	// Start() and replaces the placeholder runner, while Stop()/enqueue/forget read
	// the field concurrently (the websocket server accepts connections — and can
	// drive a teardown enqueue — before the runner is rebuilt). Production code
	// therefore reads via compactRunnerRef() and writes via setCompactRunner(); the
	// runner itself is internally synchronized. Tests assign the field directly,
	// which is race-free because they never run Start() concurrently with that
	// assignment.
	compactRunnerMu sync.RWMutex
	compactRunner   *tasks.Runner
	// The *Threshold/*Debounce/*Timeout fields remain the test-override knobs
	// feeding the size gate, the Enqueue debounce, and RegisterWithTimeout.
	keeperCompactThreshold int
	keeperCompactDebounce  time.Duration
	keeperCompactTimeout   time.Duration
	// workspaceContextBeforeKeeperApply is the apply-injection test hook, fired
	// inside the executor immediately before the CommitGuard fence + Apply.
	workspaceContextBeforeKeeperApply func()
	// workspaceContextCompactionExecution, when set, replaces the agentic
	// executeKeeperCompact spawn with a canned execution. Tests use it
	// to return a fixed compacted candidate without spawning a real LLM; the
	// validate + commit-under-CommitGuard path stays real.
	workspaceContextCompactionExecution func(
		ctx context.Context,
		config keeperCompactConfig,
		canonical *protocol.WorkspaceContext,
	) (keeperCompactExecution, error)

	// Notebook narration test seams. summarizeSessionExecution /
	// narrateWorkspaceExecution, when set, replace the real RunHeadlessTask spawn so
	// tests exercise the executor's resolve-inputs / verify-ledger logic against a
	// fake provider that writes (or refuses to write) the target file — no real LLM.
	// The file-existence/marker verification, enqueue/coalesce, and IS_REMOVAL_PASS
	// derivation all stay real. narrationNowOverride pins today's date for the
	// journal filename so date-boundary behavior is deterministic.
	summarizeSessionExecution func(
		ctx context.Context,
		provider agentdriver.HeadlessTaskProvider,
		request agentdriver.HeadlessTaskRequest,
	) (agentdriver.HeadlessTaskResult, error)
	narrateWorkspaceExecution func(
		ctx context.Context,
		provider agentdriver.HeadlessTaskProvider,
		request agentdriver.HeadlessTaskRequest,
	) (agentdriver.HeadlessTaskResult, error)
	narrationNowOverride func() time.Time

	// Daily-narrate activity gate. notebookNarrateActivity is the in-memory set of
	// workspace ids that saw real activity (a session end or a content-changing
	// context write) since the last daily-narrate cron fire. It is best-effort and
	// NOT persisted: a restart loses it, which is fine because session-end is the
	// primary narrate path and the daily cron is only a backstop for long-lived
	// workspaces that had no session end. The cron drain snapshots and clears it; a
	// workspace absent from the set is skipped that day so idle workspaces never burn
	// a strong-tier pass. notebookNarrateActivityMu guards both the map pointer and
	// its contents; the map is lazily initialized under the mutex (no constructor
	// edit needed).
	notebookNarrateActivityMu sync.Mutex
	notebookNarrateActivity   map[string]struct{}
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

func (d *Daemon) warmLoginShellEnvCache() {
	shell := pty.GetUserLoginShell()
	if shell == "" {
		return
	}
	env, err := pty.ReadLoginShellEnv(shell)
	if err != nil {
		d.logf("login shell env pre-warm failed for %s: %v", shell, err)
		return
	}
	d.loginShellEnvMu.Lock()
	d.loginShellEnv = env
	d.loginShellEnvMu.Unlock()
	d.logf("login shell env pre-warmed: shell=%s vars=%d", shell, len(env))
}

func (d *Daemon) cachedLoginShellEnv() []string {
	d.loginShellEnvMu.RLock()
	env := d.loginShellEnv
	d.loginShellEnvMu.RUnlock()
	return env
}

func (d *Daemon) currentTerminalTheme() pty.TerminalTheme {
	d.terminalThemeMu.Lock()
	theme := d.terminalTheme
	d.terminalThemeMu.Unlock()
	return theme
}

func (d *Daemon) setCurrentTerminalTheme(theme pty.TerminalTheme) {
	d.terminalThemeMu.Lock()
	d.terminalTheme = theme
	d.terminalThemeMu.Unlock()
}

// ScrubInheritedAgentSessionEnv strips per-session environment variables leaked
// from a parent agent (e.g. when attn is launched via `make install` from
// inside a Claude Code session) so they never propagate to spawned sessions or
// to the captured login-shell env. Call before Start(), which warms the
// login-shell env cache.
func (d *Daemon) ScrubInheritedAgentSessionEnv() {
	if scrubbed := config.ScrubInheritedAgentSessionEnv(); len(scrubbed) > 0 {
		d.logf("scrubbed inherited agent session env before startup: %v", scrubbed)
	}
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
	git.SetLogFunc(func(format string, args ...interface{}) {
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

	d := &Daemon{
		socketPath:          socketPath,
		pidPath:             pidPath,
		dataRoot:            dataRoot,
		store:               sessionStore,
		wsHub:               newWSHub(),
		done:                make(chan struct{}),
		logger:              logger,
		debugLogging:        logger != nil && logger.DebugEnabled(),
		ghRegistry:          github.NewClientRegistry(),
		hubManager:          nil,
		repoCaches:          make(map[string]*repoCache),
		gitCoord:            newGitCoordinator(),
		warnings:            startupWarnings,
		workflowDirty:       make(map[string]bool),
		workflowEngineConn:  make(map[string]workflowEngineSink),
		ptyBackend:          ptybackend.NewEmbedded(manager),
		transcriptWatch:     make(map[string]*transcriptWatcher),
		pendingInitialWS:    make(map[*wsClient]struct{}),
		startedCh:           make(chan struct{}),
		classifiedTurn:      make(map[string]string),
		classifyingTurn:     make(map[string]string),
		longRun:             make(map[string]longRunSession),
		forcedStop:          make(map[string]time.Time),
		pendingResumeID:     make(map[string]string),
		tailscale:           newTailscaleRuntime(),
		plugins:             newPluginRegistry(),
		pluginHealthEnabled: true,
		pluginDir:           pluginDirForSocket(socketPath),
		bundledPluginDir:    bundledPluginDirForExecutable(),
		workspaces:          newWorkspaceRegistry(),
	}
	// Production wiring for the orphaned-ticket reconciliation classifier. Test
	// constructors leave this nil so unit tests never shell out to a real CLI.
	d.ticketReconcileExec = d.execTicketReconcileClassifier
	return d
}

// NewForTesting creates a daemon with a non-persistent store for tests
func NewForTesting(socketPath string) *Daemon {
	dataRoot := filepath.Dir(socketPath)
	pidPath := filepath.Join(dataRoot, "attn.pid")
	manager := pty.NewManager(pty.DefaultScrollbackSize, nil)
	return &Daemon{
		socketPath:         socketPath,
		pidPath:            pidPath,
		dataRoot:           dataRoot,
		store:              store.New(),
		wsHub:              newWSHub(),
		done:               make(chan struct{}),
		logger:             nil, // No logging in tests
		ghRegistry:         github.NewClientRegistry(),
		hubManager:         nil,
		repoCaches:         make(map[string]*repoCache),
		gitCoord:           newGitCoordinator(),
		ptyBackend:         ptybackend.NewEmbedded(manager),
		transcriptWatch:    make(map[string]*transcriptWatcher),
		pendingInitialWS:   make(map[*wsClient]struct{}),
		startedCh:          make(chan struct{}),
		classifiedTurn:     make(map[string]string),
		classifyingTurn:    make(map[string]string),
		longRun:            make(map[string]longRunSession),
		forcedStop:         make(map[string]time.Time),
		pendingResumeID:    make(map[string]string),
		tailscale:          newTailscaleRuntime(),
		plugins:            newPluginRegistry(),
		pluginDir:          pluginDirForSocket(socketPath),
		bundledPluginDir:   bundledPluginDirForExecutable(),
		workspaces:         newWorkspaceRegistry(),
		workflowDirty:      make(map[string]bool),
		workflowEngineConn: make(map[string]workflowEngineSink),
		// A disabled runner (no root) keeps the unconditional Cancel/Enqueue
		// callsites nil-safe in tests; tests that exercise a live compaction
		// override this with an enabled runner (see newTestCompactRunner).
		compactRunner: tasks.New(tasks.Options{}),
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
		socketPath:         socketPath,
		pidPath:            pidPath,
		dataRoot:           dataRoot,
		store:              store.New(),
		wsHub:              newWSHub(),
		done:               make(chan struct{}),
		logger:             nil,
		ghRegistry:         registry,
		hubManager:         nil,
		repoCaches:         make(map[string]*repoCache),
		gitCoord:           newGitCoordinator(),
		ptyBackend:         ptybackend.NewEmbedded(manager),
		transcriptWatch:    make(map[string]*transcriptWatcher),
		pendingInitialWS:   make(map[*wsClient]struct{}),
		startedCh:          make(chan struct{}),
		classifiedTurn:     make(map[string]string),
		classifyingTurn:    make(map[string]string),
		longRun:            make(map[string]longRunSession),
		forcedStop:         make(map[string]time.Time),
		pendingResumeID:    make(map[string]string),
		tailscale:          newTailscaleRuntime(),
		plugins:            newPluginRegistry(),
		pluginDir:          pluginDirForSocket(socketPath),
		bundledPluginDir:   bundledPluginDirForExecutable(),
		workspaces:         newWorkspaceRegistry(),
		workflowDirty:      make(map[string]bool),
		workflowEngineConn: make(map[string]workflowEngineSink),
		compactRunner:      tasks.New(tasks.Options{}),
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
	if d.longRun == nil {
		d.longRun = make(map[string]longRunSession)
	}
	if d.forcedStop == nil {
		d.forcedStop = make(map[string]time.Time)
	}
	if d.ptyBackend == nil {
		d.ptyBackend = ptybackend.NewEmbedded(pty.NewManager(pty.DefaultScrollbackSize, d.logf))
	}
	if d.tailscale == nil {
		d.tailscale = newTailscaleRuntime()
	}
	if d.workspaces == nil {
		d.workspaces = newWorkspaceRegistry()
	}
	if d.plugins == nil {
		d.plugins = newPluginRegistry()
	}
	d.ensurePluginSupervisor()
	// Push the headless context-window cap into the agent package's process-global
	// before any headless run can start, so the default (or configured) cap
	// applies from the first keeper/narration/reconcile run.
	d.applyHeadlessContextWindowCap()
	reapedWorkspaceIDs := d.loadWorkspacesFromStore()
	if d.daemonInstanceID == "" {
		instanceID, err := ensureDaemonInstanceID(d.dataRoot)
		if err != nil {
			return fmt.Errorf("ensure daemon instance id: %w", err)
		}
		d.daemonInstanceID = instanceID
	}
	if d.hubManager == nil {
		d.hubManager = hub.NewManager(d.store, d.broadcastEndpointStatusChanged, d.broadcastSessionsUpdated, d.broadcastRawWSMessage, d.logf)
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

	// Pre-warm login shell env cache in a goroutine so the first PTY spawn
	// doesn't pay the ~130ms cost of starting a login shell.
	go d.warmLoginShellEnvCache()

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
		d.stopInstalledPlugins()
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
	d.startInstalledPlugins()

	// Start WebSocket hub with daemon's logger
	d.wsHub.logf = d.logf
	go d.wsHub.run()

	// Coalesce workflow run updates into ~75ms-spaced full-run broadcasts.
	go d.startWorkflowBroadcastLoop(d.doneContext())

	// Watch open markdown tiles for on-disk changes and live-reload them.
	go d.runMarkdownContentWatcher(d.done)

	// PTY exit events are emitted asynchronously from read loops.
	if hooks, ok := d.ptyBackend.(ptybackend.LifecycleHooks); ok {
		hooks.SetExitHandler(d.handlePTYExit)
		hooks.SetStateHandler(d.handlePTYState)
	}

	// Create HTTP server for WebSocket (must be created synchronously to avoid race with Stop())
	d.initHTTPServer()
	go d.runHTTPServer()
	d.maybeStartDiagServer()
	d.removeLegacyEmbeddedTailscaleState()
	d.migrateKeeperCompactSettingKey() // one-time settings key rename (workspace_context_janitor -> workspace_keeper_compact)
	d.migrateNotebookCronSettingKeys() // one-time settings key rename (notebook.dreaming.* -> notebook.cron.*)
	go d.ensureTailscaleServeFromSettingsAndBroadcast()
	d.hubManager.Start(d.doneContext())

	// Repository-worktree automation recovery may need GitHub credentials to
	// clone or fetch a private repository. Keep host discovery asynchronous so
	// the daemon can begin accepting connections, but make recovery wait for the
	// initial discovery attempt to finish before it can terminally fail a run.
	githubHostsReady := make(chan struct{})
	go func() {
		defer close(githubHostsReady)
		if err := d.refreshGitHubHosts(); err != nil {
			d.logf("Initial GitHub host discovery failed: %v", err)
		}
		// Start PR polling after initial host discovery
		go d.pollPRs()
		// Start periodic host refresh
		go d.refreshGitHubHostsLoop()
	}()

	recoveryStartedAt := time.Now()
	go func() {
		d.performStartupPTYRecovery(recoveryStartedAt)
		recoverAutomationsAfterGitHubReady(githubHostsReady, d.recoverAutomations)
		d.setRecovering(false)
		// A pending delegation may have spawned its stable runtime ID just
		// before the daemon stopped. Resume only after worker reconciliation has
		// adopted any surviving runtime into the session store; absence is then
		// authoritative enough to launch the reserved ID once.
		d.resumePendingDelegations()
	}()

	// Note: No background persistence needed - SQLite persists immediately

	// Start branch monitoring
	go d.monitorBranches()

	// Rotating SQLite backups: one immediately at startup, then every
	// backupInterval. A failure here must never crash or wedge the daemon.
	go d.runDatabaseBackupLoop()

	// Orphaned-ticket sweep backstop: catches non-terminal tickets whose owning
	// session died where the session-end seam couldn't run (pre-feature orphans,
	// a daemon death mid-seam) and repairs claims whose verdict never landed.
	go d.runTicketReconcileSweep()

	// Construct + start the durable compaction runner (kinds compact_context,
	// summarize_session, narrate_workspace).
	d.startCompactRunner()

	// Now that the runner exists, enqueue the deferred removal-boundary retrospectives
	// for any workspaces reaped during startup reconciliation. loadWorkspacesFromStore
	// ran before startCompactRunner, so enqueuing inline there would have been a
	// nil-runner no-op; deferring to here gives a startup-reaped workspace its final
	// narrate, matching the live removal paths.
	for _, wsID := range reapedWorkspaceIDs {
		d.enqueueFinalNarrateWorkspace(wsID)
	}

	// Start the notebook cron enqueuer (enqueues the nightly daily-narrate backstop
	// onto the durable runner when due). Launched AFTER startCompactRunner so the
	// narrate executor is registered before the first tick fires.
	go d.startNotebookCronEnqueuer(d.done)

	// Start the scheduled-automation observation loop (claims and delivers due
	// scheduled-trigger occurrences).
	go d.startAutomationScheduleLoop(d.done)

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
	recoverable := 0
	for _, session := range sessions {
		if _, ok := liveIDs[session.ID]; ok {
			continue
		}
		if d.recoverOnMissingPTY(session) {
			d.applyState(sessionStateChange{
				sessionID: session.ID,
				state:     protocol.StateIdle,
				cause:     startupRecovery{},
			})
			d.store.SetRecoverable(session.ID, true)
			recoverable++
			continue
		}
		d.removeReapedSession(session.ID)
		removed++
	}
	if recoverable > 0 {
		d.logf("marked %d sessions as recoverable on startup", recoverable)
	}
	return removed
}

func (d *Daemon) recoverOnMissingPTY(session *protocol.Session) bool {
	if session == nil {
		return false
	}
	if agentdriver.RecoverOnMissingPTY(agentdriver.Get(string(session.Agent))) {
		return true
	}
	if run := d.store.GetAgentDriverRun(session.ID); run.RunID != "" {
		return true
	}
	if strings.TrimSpace(d.store.GetAgentMetadata(session.ID)) != "" {
		return true
	}
	if d.plugins != nil {
		if driver, ok := d.plugins.driver(string(session.Agent)); ok && driver.Capabilities["resume"] {
			return true
		}
	}
	return false
}

func (d *Daemon) pluginDriverReportsState(agent protocol.SessionAgent) bool {
	if d.plugins == nil {
		return false
	}
	driver, ok := d.plugins.driver(string(agent))
	return ok && driver.Capabilities["state_reporting"]
}

func (d *Daemon) performStartupPTYRecovery(recoveryStartedAt time.Time) {
	defer d.rebuildTicketDeliverySchedules()
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
		d.reconcileWorkspaceLayoutsWithPTYBackend(context.Background())
		// Recovery rewrote session states in the store; refresh the cached
		// workspace rollups so InitialState matches.
		d.reseedWorkspaceStatuses()
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
	d.reconcileWorkspaceLayoutsWithPTYBackend(context.Background())
	// Pruning flipped recovered sessions to idle in the store; refresh the
	// cached workspace rollups so InitialState matches.
	d.reseedWorkspaceStatuses()
}

func (d *Daemon) rebuildTicketDeliverySchedules() {
	if d.store == nil {
		return
	}
	now := time.Now()
	for _, session := range d.store.List("") {
		if session != nil {
			d.notifyUnreadTicketSession(session.ID, now)
		}
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
	if reconcile.Created > 0 || reconcile.StateUpdated > 0 || reconcile.MarkedIdle > 0 || reconcile.MarkedRecoverable > 0 || reconcile.Reaped > 0 || reconcile.SkippedIdle > 0 || reconcile.SkippedRecent > 0 || reconcile.SkippedShell > 0 || reconcile.LikelyAlive > 0 || reconcile.LivenessUnknown > 0 || reconcile.MissingMetadata > 0 {
		d.logf(
			"worker session reconciliation summary: created=%d state_updated=%d marked_idle=%d marked_recoverable=%d reaped=%d skipped_idle=%d skipped_recent=%d skipped_shell=%d likely_alive=%d liveness_unknown=%d missing_metadata=%d",
			reconcile.Created,
			reconcile.StateUpdated,
			reconcile.MarkedIdle,
			reconcile.MarkedRecoverable,
			reconcile.Reaped,
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
	if reconcile.MarkedRecoverable > 0 {
		d.addWarning(
			warnStaleSessionMissingWorker,
			fmt.Sprintf("%d sessions can be recovered from a previous daemon run.", reconcile.MarkedRecoverable),
		)
	}
	if reconcile.Reaped > 0 {
		d.addWarning(
			warnStaleSessionsPruned,
			fmt.Sprintf("Removed %d non-recoverable sessions from a previous daemon run.", reconcile.Reaped),
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
			if normalizeSpawnAgent(info.Agent) == protocol.AgentShellValue {
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
				Agent:          normalizeStoredSessionAgent(info.Agent, protocol.SessionAgentCodex),
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
		// A session adopted as live cannot be mid-close: any intentional-close mark
		// left behind (daemon died between terminateSession's mark and the kill) is
		// stale, and keeping it would misread this session's later genuine crash as
		// a clean close.
		d.store.ClearSessionIntentionalClose(sessionID)
		// By the same token it cannot be crashed: a ticket stamped Crashed (a
		// startup reap on an earlier boot, a sweep misread) whose owner turns out
		// to be alive goes back to Working (ticket_revive.go).
		d.reviveCrashedTicketsForSession(sessionID)
		if protocol.Deref(existing.Recoverable) {
			d.store.SetRecoverable(sessionID, false)
			report.Changed = true
		}
		// A hook-reported "scheduled" session is parked on a cron/loop. PTY and
		// worker-info recovery cannot reconstruct that — the parked screen reads
		// as an idle prompt, which would be mis-recovered as launching/idle — so
		// preserve it across recovery. The next Stop re-derives from the live
		// session_crons, and a session whose worker actually died is demoted by
		// the reaping loop below (it will be absent from liveIDs).
		if existing.State == protocol.SessionStateScheduled {
			continue
		}
		if haveInfo {
			if run := d.store.GetAgentDriverRun(sessionID); run.RunID != "" &&
				(d.pluginDriverReportsState(existing.Agent) ||
					existing.State == protocol.SessionStateWaitingInput ||
					existing.State == protocol.SessionStatePendingApproval) {
				// Persisted plugin reports remain authoritative across daemon recovery.
				continue
			}
			nextState := sessionStateFromRecoveredInfo(info)
			if existing.State != nextState {
				d.applyState(sessionStateChange{
					sessionID: sessionID,
					state:     string(nextState),
					cause:     startupRecovery{},
				})
				report.StateUpdated++
				report.Changed = true
			}
			continue
		}
		switch existing.State {
		case protocol.SessionStateWaitingInput, protocol.SessionStatePendingApproval:
			// Preserve interactive waiting/approval states during recovery.
		default:
			if existing.State != protocol.SessionStateLaunching {
				d.applyState(sessionStateChange{
					sessionID: sessionID,
					state:     protocol.StateLaunching,
					cause:     startupRecovery{},
				})
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
		if d.recoverOnMissingPTY(session) {
			d.applyState(sessionStateChange{
				sessionID: session.ID,
				state:     protocol.StateIdle,
				cause:     startupRecovery{},
			})
			d.store.SetRecoverable(session.ID, true)
			report.StateUpdated++
			report.MarkedRecoverable++
			report.Changed = true
		} else {
			d.removeReapedSession(session.ID)
			report.Reaped++
			report.Changed = true
		}
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
		if reconcile.MarkedRecoverable > 0 {
			d.addWarning(
				warnStaleSessionMissingWorker,
				fmt.Sprintf("%d sessions can be recovered from a previous daemon run.", reconcile.MarkedRecoverable),
			)
		}
		if reconcile.Reaped > 0 {
			d.addWarning(
				warnStaleSessionsPruned,
				fmt.Sprintf("Removed %d non-recoverable sessions from a previous daemon run.", reconcile.Reaped),
			)
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
		} else if reconcile.MarkedIdle > 0 || reconcile.MarkedRecoverable > 0 || reconcile.Reaped > 0 {
			d.logf("deferred worker reconciliation: marked_idle=%d marked_recoverable=%d reaped=%d", reconcile.MarkedIdle, reconcile.MarkedRecoverable, reconcile.Reaped)
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

func normalizeStoredSessionAgent(agent string, fallback protocol.SessionAgent) protocol.SessionAgent {
	normalized := strings.TrimSpace(strings.ToLower(agent))
	if normalized == "" {
		return protocol.NormalizeSessionAgent(fallback, protocol.SessionAgentCodex)
	}
	if normalized == protocol.AgentShellValue {
		return protocol.SessionAgentShell
	}
	if agentdriver.Get(normalized) != nil {
		return protocol.SessionAgent(normalized)
	}
	if normalizePluginAgent(normalized) != "" {
		return protocol.SessionAgent(normalized)
	}
	return protocol.NormalizeSessionAgent(fallback, protocol.SessionAgentCodex)
}

func sessionStateFromRecoveredInfo(info ptybackend.SessionInfo) protocol.SessionState {
	if !info.Running {
		return protocol.SessionStateIdle
	}
	agent := normalizeStoredSessionAgent(info.Agent, protocol.SessionAgentCodex)
	return agentdriver.RecoveredRunningSessionState(agentdriver.Get(string(agent)), info.State)
}

// Stop stops the daemon
func (d *Daemon) Stop() {
	d.log("daemon stopping")
	close(d.done)
	d.stopNotebookWatcher()
	d.stopFsWatchers()
	if runner := d.compactRunnerRef(); runner != nil {
		runner.Stop()
	}
	if d.hubManager != nil {
		d.hubManager.Stop()
	}
	d.stopInstalledPlugins()
	d.stopAllTranscriptWatchers()
	d.stopNudgeCountdowns()
	if d.ptyBackend != nil {
		_ = d.ptyBackend.Shutdown(context.Background())
	}
	if d.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		d.httpServer.Shutdown(ctx)
	}
	if d.diagServer != nil {
		_ = d.diagServer.Close()
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

func (d *Daemon) doneContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-d.done:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx
}

func (d *Daemon) handlePTYExit(info ptybackend.ExitInfo) {
	// A reload (chief assign/demote) killed this worker on purpose and owns the
	// teardown+respawn itself. Consume the one-shot flag and skip ALL exit
	// processing — no idle-clobber, no backend Remove (reloadSessionAgent already
	// removed it before re-spawning), and crucially no session_exited broadcast,
	// which would drop the just-respawned session to a dead pane. reloadSessionAgent
	// emits runtime_respawned instead (or session_exited itself if the respawn fails).
	if d.consumeReloading(info.ID) {
		d.logf("suppressing exit for reloading session %s (runtime replaced in place)", info.ID)
		return
	}
	if d.queueExitDuringPluginLaunch(info) {
		return
	}
	if d.supersededExitDuringPluginLaunch(info) {
		if activeRun := d.store.GetAgentDriverRun(info.ID); activeRun.RunID == info.LifecycleID {
			d.closePluginDriverSession(info.ID, "exited", &info.ExitCode, info.Signal)
		}
		return
	}
	if info.LifecycleID != "" {
		activeRun := d.store.GetAgentDriverRun(info.ID)
		if activeRun.RunID != "" && activeRun.RunID != info.LifecycleID {
			d.logf("ignoring stale plugin PTY exit: session=%s exited_run=%s active_run=%s", info.ID, info.LifecycleID, activeRun.RunID)
			return
		}
	}
	d.stopTranscriptWatcher(info.ID)
	d.clearLongRunTracking(info.ID)
	d.closePluginDriverSession(info.ID, "exited", &info.ExitCode, info.Signal)

	if d.ptyBackend != nil {
		if err := d.removePTYSession(info.ID); err != nil {
			d.logf("pty backend remove on exit failed for %s: %v", info.ID, err)
		}
	}

	if session := d.store.Get(info.ID); session != nil {
		// Reconcile bound tickets against the pre-clobber state: the idle-clobber
		// just below erases whether the agent was mid-flight (a crash or kill) or at
		// a clean rest when its process exited — the signal that decides between the
		// Crashed stamp and the orphaned-ticket classifier (ticket_reconcile.go).
		// A client-initiated reload (kill_session reload:true + respawn of the same
		// id) is a lifecycle transition, not a death: skip ONLY the ticket seam and
		// let the rest of exit processing run — the reload flow depends on it.
		if d.consumeReloadKill(info.ID) {
			d.logf("skipping ticket reconcile for %s: exit is a user reload, runtime respawns in place", info.ID)
		} else {
			d.reconcileTicketsOnSessionEnd(info.ID, string(session.State))
		}
		d.applyState(sessionStateChange{
			sessionID: info.ID,
			state:     protocol.StateIdle,
			cause:     processExit{},
		})
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
	if err := d.terminateSessionChecked(sessionID, sig); err != nil {
		d.logf("terminate session failed for %s: %v", sessionID, err)
		// Legacy callers forget the session even when termination cannot be
		// confirmed. Restore their intentional-close evidence before the ticket
		// reconciliation seam sees the discarded row; checked ownership-sensitive
		// callers bypass this wrapper and keep the cleared marks on a surviving PTY.
		d.markForcedStopClassification(sessionID)
		if d.store != nil {
			d.store.MarkSessionIntentionalClose(sessionID, time.Now())
		}
		// The watcher must not outlive the legacy caller's discarded record.
		d.stopTranscriptWatcher(sessionID)
		if d.ptyBackend != nil {
			_ = d.ptyBackend.Remove(context.Background(), sessionID)
		}
	}
}

// terminateSessionChecked stops a runtime without discarding its durable
// session record when termination cannot be confirmed. Ownership-sensitive
// callers can then retry instead of spawning a second process with the same ID.
func (d *Daemon) terminateSessionChecked(sessionID string, sig syscall.Signal) error {
	d.markForcedStopClassification(sessionID)
	// Also record the intentional close durably, BEFORE the kill: the in-memory
	// forced-stop mark above expires after 30s and dies with the daemon, but the
	// ticket crash/reconcile seam may run long after both (startup reap after a
	// daemon restart). Without the durable mark, a user close whose seam runs
	// late is indistinguishable from a spontaneous mid-flight death and would be
	// crash-stamped (ticket_reconcile.go).
	if d.store != nil {
		d.store.MarkSessionIntentionalClose(sessionID, time.Now())
	}

	if d.ptyBackend == nil {
		d.stopTranscriptWatcher(sessionID)
		d.closePluginDriverSession(sessionID, "killed", nil, signalName(sig))
		return nil
	}
	err := d.ptyBackend.Kill(context.Background(), sessionID, sig)
	if err == nil || errors.Is(err, pty.ErrSessionNotFound) {
		// Production backends return from Kill only once the child has exited.
		// Close here because worker lifecycle delivery can trail that return.
		d.closePluginDriverSession(sessionID, "killed", nil, signalName(sig))
	}
	if err != nil && !errors.Is(err, pty.ErrSessionNotFound) {
		d.clearForcedStopClassification(sessionID)
		if d.store != nil {
			d.store.ClearSessionIntentionalClose(sessionID)
		}
		return err
	}
	// Kill is now confirmed (or the runtime was already absent). Until this point
	// a checked caller may need to retain a surviving session after a hard error,
	// including its transcript-driven state/classification watcher.
	d.stopTranscriptWatcher(sessionID)
	if err := d.ptyBackend.Remove(context.Background(), sessionID); err != nil && !errors.Is(err, pty.ErrSessionNotFound) && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (d *Daemon) unregisterSession(sessionID string, sig syscall.Signal) *protocol.Session {
	session := d.store.Get(sessionID)
	if session == nil && d.hubManager != nil {
		session = d.hubManager.RemoteSession(sessionID)
	}
	d.terminateSession(sessionID, sig)
	d.forgetSession(sessionID)
	return session
}

func (d *Daemon) forgetSession(sessionID string) {
	d.dropSessionRecord(sessionID)
	d.clearChiefOfStaffIfSession(sessionID)
	if d.hubManager != nil {
		d.hubManager.ForgetSession(sessionID)
	}
	d.clearLongRunTracking(sessionID)
	d.clearClassifiedTurn(sessionID)
	d.clearClassifyingTurn(sessionID)
}

func (d *Daemon) removeReapedSession(sessionID string) {
	d.dropSessionRecord(sessionID)
	d.clearChiefOfStaffIfSession(sessionID)
	d.dissociateSessionFromWorkspace(sessionID)
	d.removeWorkspaceLayoutPaneForSession(sessionID)
}

// dropSessionRecord removes a session's store record, first capturing the crash
// outcome of any delegated ticket it was running. Routing every session-removal
// path through here means a delegated worker that died mid-flight surfaces as a
// Crashed ticket no matter how its session ends — cleanly unregistered, reaped on
// restart/liveness sweep, or torn down with its worktree — and not just on the
// orderly close path. The capture is idempotent (a prior terminal-report write
// wins) and a no-op for sessions without an active ticket.
func (d *Daemon) dropSessionRecord(sessionID string) {
	// Backstop the ticket reconciliation for removal paths that bypass
	// handlePTYExit (reaped on restart, liveness sweep, or torn down with a
	// worktree). First-writer-wins: a real pre-clobber exit capture already ran
	// the seam and claimed the flag, so this later read (which may only see the
	// clobbered idle) loses the claim and is a no-op.
	if session := d.store.Get(sessionID); session != nil {
		d.reconcileTicketsOnSessionEnd(sessionID, string(session.State))
	}
	d.clearNudgeState(sessionID)
	d.store.Remove(sessionID)
}

func (d *Daemon) handlePTYState(sessionID, state string) {
	session := d.store.Get(sessionID)
	if session == nil {
		return
	}
	if run := d.store.GetAgentDriverRun(sessionID); run.RunID != "" && d.pluginDriverReportsState(session.Agent) {
		// External drivers own state through sequenced session.report_* calls.
		return
	}
	agent := session.Agent
	driver := agentdriver.Get(string(agent))
	if !agentdriver.ShouldApplyPTYState(driver, session.State, state) {
		return
	}

	d.logf("pty state update: session=%s agent=%s state=%s", sessionID, agent, state)
	d.applyState(sessionStateChange{
		sessionID: sessionID,
		state:     state,
		cause:     liveSignal{},
	})
}

// initHTTPServer creates the HTTP server synchronously to avoid race with Stop().
// Must be called before runHTTPServer().
func (d *Daemon) initHTTPServer() {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", d.handleWS)
	mux.HandleFunc("/health", d.handleHealth)
	mux.HandleFunc("/web-instrumentation", d.handleWebInstrumentation)
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, _ *http.Request) {
		setNoStoreHeaders(w.Header())
		w.WriteHeader(http.StatusNoContent)
	})
	mux.Handle("/", daemonWebStaticHandler())
	d.httpHandler = mux

	d.httpServer = &http.Server{
		Addr:    net.JoinHostPort(config.WSBindAddress(), config.WSPort()),
		Handler: d.httpHandler,
	}
}

// runHTTPServer starts listening. Must be called after initHTTPServer().
func (d *Daemon) runHTTPServer() {
	d.logf("WebSocket server starting on ws://%s/ws", d.httpServer.Addr)
	if err := d.httpServer.ListenAndServe(); err != http.ErrServerClosed {
		d.logf("HTTP server error: %v", err)
	}
}

// maybeStartDiagServer starts the opt-in loopback diagnostics endpoint (pprof +
// /debug/vars) when ATTN_PPROF is set. It is off by default and binds 127.0.0.1
// only. A bind failure is logged but never fatal — diagnostics must not take the
// daemon down.
func (d *Daemon) maybeStartDiagServer() {
	addr, enabled := config.PprofAddr()
	if !enabled {
		return
	}
	srv, err := diag.Start(addr, d.diagStats)
	if err != nil {
		d.logf("diagnostics endpoint failed to start on %s: %v", addr, err)
		return
	}
	d.diagServer = srv
	d.logf("diagnostics endpoint listening on http://%s/ (pprof + /debug/vars)", srv.Addr())
}

// diagStats reports live PTY-session counts and worker subprocess PIDs for the
// diagnostics /debug/vars snapshot. The worker PIDs are the per-session RSS
// handles for the worker backend; the embedded backend reports none.
func (d *Daemon) diagStats() diag.Stats {
	stats := diag.Stats{PtyBackend: "embedded"}
	if d.ptyBackend == nil {
		return stats
	}
	ctx := context.Background()
	stats.Sessions = len(d.ptyBackend.SessionIDs(ctx))
	if wp, ok := d.ptyBackend.(ptybackend.WorkerProcessProvider); ok {
		stats.PtyBackend = "worker"
		stats.WorkerPIDs = wp.WorkerPIDs(ctx)
	}
	return stats
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
			// Authentication may have been temporarily unavailable during startup.
			// Pending automation delivery is stable-ID idempotent, so retry it after
			// every host refresh rather than terminally stranding a private PR run.
			d.recoverAutomations()
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
		d.broadcastGitHubHosts()
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

	d.broadcastGitHubHosts()
	return nil
}

func (d *Daemon) gitHubHosts() []string {
	if d.ghRegistry == nil {
		return nil
	}
	return d.ghRegistry.Hosts()
}

func (d *Daemon) broadcastGitHubHosts() {
	d.wsHub.BroadcastValue(d.gitHubHostsUpdatedMessage())
}

func (d *Daemon) gitHubHostsUpdatedMessage() *protocol.GitHubHostsUpdatedMessage {
	return &protocol.GitHubHostsUpdatedMessage{
		Event:       protocol.EventGitHubHostsUpdated,
		GithubHosts: d.gitHubHosts(),
	}
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

// releasePIDLock unlocks the PID file. It deliberately leaves the file in
// place on disk rather than removing it: other flock holders of that exact
// inode (notably `attn db restore`, which holds this same lock for the
// duration of a restore — see cmd/attn/db.go's acquireDaemonLock) only
// contend correctly with a future daemon startup if acquirePIDLock reopens
// and relocks that same inode. Unlinking here would let a concurrent holder
// keep flocking an orphaned inode while a subsequent os.OpenFile(O_CREATE)
// elsewhere silently creates and locks a different one at the same
// pathname, defeating mutual exclusion entirely. acquirePIDLock already
// truncates and rewrites the file's contents on every acquire, so a stale
// leftover file is harmless.
func (d *Daemon) releasePIDLock() {
	if d.pidFile != nil {
		syscall.Flock(int(d.pidFile.Fd()), syscall.LOCK_UN)
		d.pidFile.Close()
		d.pidFile = nil
	}
}

func (d *Daemon) handleConnection(conn net.Conn) {
	defer conn.Close()

	// Legacy hook traffic is one JSON object per connection and does not
	// consistently include a trailing newline. Read exactly one complete
	// top-level JSON object here; plugin-mode connections switch to line
	// framing only after their hello has been identified, and any pipelined
	// bytes stay buffered for that loop.
	reader := bufio.NewReader(conn)
	data, err := readInitialSocketFrame(reader, 65536)
	if err != nil {
		return
	}

	helloID, helloParams, pluginMode, err := parsePluginHello(data)
	if pluginMode {
		if err != nil {
			_ = json.NewEncoder(conn).Encode(jsonRPCFailure(helloID, jsonRPCInvalidRequest, err.Error()))
			return
		}
		d.handlePluginConnection(conn, reader, helloID, helloParams)
		return
	}

	cmd, msg, err := protocol.ParseMessage(data)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}

	switch cmd {
	case protocol.CmdRegister:
		d.handleRegister(conn, msg.(*protocol.RegisterMessage))
	case protocol.CmdDelegate:
		d.handleDelegate(conn, msg.(*protocol.DelegateMessage))
	case protocol.CmdAutomationApply, protocol.CmdAutomationList, protocol.CmdAutomationShow, protocol.CmdAutomationRun, protocol.CmdAutomationRunList:
		d.handleAutomationCommand(conn, cmd, msg)
	case protocol.CmdDelegateStatus:
		d.handleDelegateStatus(conn, msg.(*protocol.DelegateStatusMessage))
	case protocol.CmdSetTicketStatus:
		d.handleSetTicketStatus(conn, msg.(*protocol.SetTicketStatusMessage))
	case protocol.CmdTicketInbox:
		d.handleTicketInbox(conn, msg.(*protocol.TicketInboxMessage))
	case protocol.CmdTicketList:
		d.handleTicketList(conn, msg.(*protocol.TicketListMessage))
	case protocol.CmdTicketShow:
		d.handleTicketShow(conn, msg.(*protocol.TicketShowMessage))
	case protocol.CmdTicketSubscribe:
		d.handleTicketSubscribe(conn, msg.(*protocol.TicketSubscribeMessage))
	case protocol.CmdTicketUnsubscribe:
		d.handleTicketUnsubscribe(conn, msg.(*protocol.TicketUnsubscribeMessage))
	case protocol.CmdTicketAttach:
		d.handleTicketAttach(conn, msg.(*protocol.TicketAttachMessage))
	case protocol.CmdTicketCreate:
		d.handleTicketCreate(conn, msg.(*protocol.TicketCreateMessage))
	case protocol.CmdTicketComment:
		d.handleTicketComment(conn, msg.(*protocol.TicketCommentMessage))
	case protocol.CmdPresentOpen:
		d.handlePresentOpen(conn, msg.(*protocol.PresentOpenMessage))
	case protocol.CmdPresentFeedback:
		d.handlePresentFeedback(conn, msg.(*protocol.PresentFeedbackMessage))
	case protocol.CmdTicketTake:
		d.handleTicketTake(conn, msg.(*protocol.TicketTakeMessage))
	case protocol.CmdWorkspaceContextCheckout:
		d.handleWorkspaceContextCheckout(conn, msg.(*protocol.WorkspaceContextCheckoutMessage))
	case protocol.CmdWorkspaceContextUpdate:
		d.handleWorkspaceContextUpdate(conn, msg.(*protocol.WorkspaceContextUpdateMessage))
	case protocol.CmdWorkspaceContextStatus:
		d.handleWorkspaceContextStatus(conn, msg.(*protocol.WorkspaceContextStatusMessage))
	case protocol.CmdWorkspaceContextList:
		d.handleWorkspaceContextList(conn)
	case protocol.CmdWorkspaceContextCompact:
		d.handleWorkspaceContextCompact(conn, msg.(*protocol.WorkspaceContextCompactMessage))
	case protocol.CmdWorkspaceContextRollback:
		d.handleWorkspaceContextRollback(conn, msg.(*protocol.WorkspaceContextRollbackMessage))
	case protocol.CmdNotebookGuide:
		// notebook_guide is the one surviving unix-socket notebook command: the
		// agent-launch wrapper uses it to learn whether a session is the chief of
		// staff and where the notebook root is. The former user-facing
		// `attn notebook …` subcommands were removed; the frontend reads and writes
		// the notebook over the WebSocket path instead.
		d.handleNotebookGuide(conn, msg.(*protocol.NotebookGuideMessage))
	case protocol.CmdJournalAppend:
		// journal_append is the contention-safe way an agent writes the daily
		// journal: it goes through the daemon's single serialized notebook.Store
		// writer instead of the agent editing journal/<date>.md directly, which
		// races the keeper's own writes to the same file.
		d.handleJournalAppend(conn, msg.(*protocol.JournalAppendMessage))
	case protocol.CmdUnregister:
		d.handleUnregister(conn, msg.(*protocol.UnregisterMessage))
	case protocol.CmdState:
		d.handleState(conn, msg.(*protocol.StateMessage))
	case protocol.CmdSetSessionResumeID:
		d.handleSetSessionResumeID(conn, msg.(*protocol.SetSessionResumeIDMessage))
	case protocol.CmdSessionInstructions:
		d.handleSessionInstructions(conn, msg.(*protocol.SessionInstructionsMessage))
	case protocol.CmdSessionTranscript:
		d.handleSessionTranscript(conn, msg.(*protocol.SessionTranscriptMessage))
	case protocol.CmdStop:
		d.handleStop(conn, msg.(*protocol.StopMessage))
	case protocol.CmdTodos:
		d.handleTodos(conn, msg.(*protocol.TodosMessage))
	case protocol.CmdWorkflowRunUpsert:
		d.handleWorkflowRunUpsert(conn, msg.(*protocol.WorkflowRunUpsertMessage))
	case protocol.CmdWorkflowCallUpsert:
		d.handleWorkflowCallUpsert(conn, msg.(*protocol.WorkflowCallUpsertMessage))
	case protocol.CmdWorkflowRunGet:
		d.handleWorkflowRunGet(conn, msg.(*protocol.WorkflowRunGetMessage))
	case protocol.CmdWorkflowRunList:
		d.handleWorkflowRunList(conn, msg.(*protocol.WorkflowRunListMessage))
	case protocol.CmdWorkflowRunCancel:
		d.handleWorkflowRunCancel(conn, msg.(*protocol.WorkflowRunCancelMessage))
	case protocol.CmdQuery:
		d.handleQuery(conn, msg.(*protocol.QueryMessage))
	case protocol.CmdHeartbeat:
		d.handleHeartbeat(conn, msg.(*protocol.HeartbeatMessage))
	case protocol.CmdSessionVisualized:
		visualizedMsg := msg.(*protocol.SessionVisualizedMessage)
		d.handleSessionVisualized(visualizedMsg.ID)
		d.sendOK(conn)
	case protocol.CmdQueryPRs:
		d.handleQueryPRs(conn, msg.(*protocol.QueryPRsMessage))
	case protocol.CmdMutePR:
		d.handleMutePR(conn, msg.(*protocol.MutePRMessage))
	case protocol.CmdMuteRepo:
		d.handleMuteRepo(conn, msg.(*protocol.MuteRepoMessage))
	case protocol.CmdMuteWorkspace:
		if _, errMsg := d.toggleWorkspaceMute(msg.(*protocol.MuteWorkspaceMessage).WorkspaceID); errMsg != "" {
			d.sendError(conn, errMsg)
			return
		}
		d.sendOK(conn)
	case protocol.CmdPinWorkspace:
		m := msg.(*protocol.PinWorkspaceMessage)
		if _, errMsg := d.setWorkspacePinned(m.WorkspaceID, m.Pinned); errMsg != "" {
			d.sendError(conn, errMsg)
			return
		}
		d.sendOK(conn)
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
	case protocol.CmdOpenMarkdown:
		d.handleOpenMarkdown(conn, msg.(*protocol.OpenMarkdownMessage))
	case protocol.CmdOpenBrowser:
		d.handleOpenBrowser(conn, msg.(*protocol.OpenBrowserMessage))
	case protocol.CmdBrowserControl:
		d.handleBrowserControl(conn, msg.(*protocol.BrowserControlMessage))
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
	d.clearLongRunTracking(msg.ID)
	existing := d.store.Get(msg.ID)

	// Get branch info
	branchInfo, _ := git.GetBranchInfo(msg.Dir)

	nowStr := string(protocol.TimestampNow())
	agent := normalizeStoredSessionAgent(string(protocol.Deref(msg.Agent)), protocol.SessionAgentClaude)
	// The label from register is a default for first registration. A non-empty
	// stored label is authoritative so a user rename survives re-registration.
	sessionLabel := protocol.Deref(msg.Label)
	if existing != nil && strings.TrimSpace(existing.Label) != "" {
		sessionLabel = existing.Label
	}
	session := &protocol.Session{
		ID:             msg.ID,
		Label:          sessionLabel,
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
	workspaceID := strings.TrimSpace(msg.WorkspaceID)
	if workspaceID == "" {
		d.sendError(conn, "missing workspace_id")
		return
	}
	session.WorkspaceID = workspaceID
	// Re-deriving the workspace title from the session label would clobber a
	// renamed workspace. Preserve a non-empty stored title instead.
	existingWS := d.store.GetWorkspace(workspaceID)
	workspaceTitle := session.Label
	if existingWS != nil && strings.TrimSpace(existingWS.Title) != "" {
		workspaceTitle = existingWS.Title
	}
	// Seed rank before the first AddWorkspace: the store persists rank on INSERT
	// only, so a brand-new workspace needs its key set here or it would sort
	// first forever. A re-register carries the stored key forward.
	workspaceRank := d.resolveWorkspaceRank(existingWS)
	d.store.AddWorkspace(&protocol.Workspace{ID: workspaceID, Title: workspaceTitle, Directory: session.Directory, Status: protocol.WorkspaceStatusLaunching, Rank: workspaceRank})
	d.workspaces.register(workspaceID, workspaceTitle, session.Directory, workspaceRank, false, false)
	d.store.Add(session)
	if resumeSessionID := d.consumePendingResumeSessionID(session.ID); resumeSessionID != "" {
		d.persistResumeSessionID(session.ID, resumeSessionID)
	}
	// Re-arm orphaned-ticket reconciliation: a registering session under a
	// flagged ticket's assignee id is that ticket's owner coming back to life
	// (CLI relaunch/resume), so a future death deserves a fresh verdict.
	if err := d.store.ClearTicketReconciliationForAssignee(session.ID); err != nil {
		d.logf("clear ticket reconciliation on register for %s: %v", session.ID, err)
	}
	// A crash-stamped ticket whose owner just re-registered is no longer
	// crashed: move it back to Working (ticket_revive.go).
	d.reviveCrashedTicketsForSession(session.ID)
	d.associateSessionWithWorkspace(session.ID, workspaceID)
	if _, err := d.ensureWorkspaceLayout(workspaceID); err != nil {
		d.logf("workspace layout bootstrap failed for workspace %s: %v", workspaceID, err)
	}

	// Track this location in recent locations
	d.store.UpsertRecentLocation(msg.Dir)

	d.sendOK(conn)

	// Broadcast session registration or update to WebSocket clients.
	eventType := protocol.EventSessionRegistered
	if existing != nil {
		eventType = protocol.EventSessionStateChanged
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:   eventType,
		Session: d.sessionForBroadcast(session),
	})
	d.broadcastWorkspaceLayout(workspaceID)
	d.recomputeAndBroadcastWorkspaceForSession(session.ID)
}

func (d *Daemon) handleUnregister(conn net.Conn, msg *protocol.UnregisterMessage) {
	session := d.unregisterSession(msg.ID, syscall.SIGTERM)
	d.sendOK(conn)

	// Broadcast to WebSocket clients
	if session != nil {
		d.wsHub.Broadcast(&protocol.WebSocketEvent{
			Event:   protocol.EventSessionUnregistered,
			Session: d.sessionForBroadcast(session),
		})
		d.dissociateSessionFromWorkspace(session.ID)
		d.removeWorkspaceLayoutPaneForSession(session.ID)
	}
}

func (d *Daemon) handleState(conn net.Conn, msg *protocol.StateMessage) {
	d.logf("state update: id=%s state=%s", msg.ID, msg.State)
	d.applyState(sessionStateChange{
		sessionID: msg.ID,
		state:     msg.State,
		cause:     liveSignal{},
	})
	d.sendOK(conn)
}

func (d *Daemon) handleSetSessionResumeID(conn net.Conn, msg *protocol.SetSessionResumeIDMessage) {
	resumeSessionID := strings.TrimSpace(msg.ResumeSessionID)
	if resumeSessionID == "" {
		d.sendError(conn, "missing resume_session_id")
		return
	}
	d.setOrQueueResumeSessionID(msg.ID, resumeSessionID)
	d.sendOK(conn)
}

func (d *Daemon) setOrQueueResumeSessionID(sessionID, resumeSessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	resumeSessionID = strings.TrimSpace(resumeSessionID)
	if sessionID == "" || resumeSessionID == "" {
		return
	}
	d.pendingResumeMu.Lock()
	defer d.pendingResumeMu.Unlock()
	if d.store.Get(sessionID) != nil {
		d.persistResumeSessionID(sessionID, resumeSessionID)
		return
	}
	if d.pendingResumeID == nil {
		d.pendingResumeID = make(map[string]string)
	}
	d.pendingResumeID[sessionID] = resumeSessionID
}

func (d *Daemon) consumePendingResumeSessionID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	d.pendingResumeMu.Lock()
	defer d.pendingResumeMu.Unlock()
	resumeSessionID := d.pendingResumeID[sessionID]
	delete(d.pendingResumeID, sessionID)
	return strings.TrimSpace(resumeSessionID)
}

// persistResumeSessionID records the agent-native resume id on the session AND
// mirrors it onto any ticket bound to that session (assignee == sessionID). The
// session row is deleted on close, taking its resume_session_id with it, so the
// durable copy on the ticket is what lets ticket Resume reattach the prior
// conversation directly instead of dropping into the agent's resume picker.
func (d *Daemon) persistResumeSessionID(sessionID, resumeSessionID string) {
	d.store.SetResumeSessionID(sessionID, resumeSessionID)
	if err := d.store.SetTicketResumeSessionID(sessionID, resumeSessionID); err != nil {
		d.logf("persistResumeSessionID: mirror to ticket failed for session %s: %v", sessionID, err)
	}
}

func (d *Daemon) handleStop(conn net.Conn, msg *protocol.StopMessage) {
	d.logf("handleStop: session=%s, transcript_path=%s", msg.ID, msg.TranscriptPath)
	if session := d.store.Get(msg.ID); session != nil {
		if resumeSessionID := agentdriver.ResumeSessionIDFromStopTranscriptPath(
			agentdriver.Get(string(session.Agent)),
			msg.TranscriptPath,
		); resumeSessionID != "" {
			d.persistResumeSessionID(msg.ID, resumeSessionID)
		}
	}
	d.store.Touch(msg.ID)
	d.sendOK(conn)

	// Narration triggers. Resolve the workspace id SYNCHRONOUSLY from the persisted
	// store row before any async work: a concurrent close can dissociate the session
	// from the in-memory registry, but the persisted workspace_id survives until the
	// session row is removed, so it is the authoritative source. The enqueues are
	// nil/Disabled-safe (no-op when the runner is absent or the notebook is off).
	//   - summarize_session ALWAYS fires (cheap per-session digest), even for a solo
	//     session with no workspace — its digest is still useful raw material.
	//   - narrate_workspace fires (coalesced) only when the stop belongs to a LIVE
	//     workspace; the removal boundary owns the final retrospective pass instead.
	// Resolve the workspace id once and stash it (plus the transcript path) on the
	// summarize task: a single-session-workspace teardown deletes both the session
	// row and the workspace row before the debounced summarize runs, so the
	// executor must carry these inputs rather than re-derive them from a gone row.
	stopWorkspaceID := d.resolveStopWorkspaceID(msg.ID)
	d.enqueueSummarizeSession(msg.ID, msg.TranscriptPath, stopWorkspaceID)
	if stopWorkspaceID != "" {
		// A session end is a daily-narrate activity event for its workspace: it marks
		// the workspace active so the nightly daily-narrate cron narrates it even on a
		// day with no further triggers. (The mark is cheap and harmless even when the
		// workspace is being torn down — the cron skips a removed workspace at drain.)
		d.markNotebookWorkspaceActivity(stopWorkspaceID)
		if d.store.GetWorkspace(stopWorkspaceID) != nil {
			d.enqueueNarrateWorkspace(stopWorkspaceID)
		}
	}

	if d.consumeForcedStopClassification(msg.ID) {
		d.logf("handleStop: skipping classification for daemon-terminated session=%s", msg.ID)
		return
	}

	// Async classification/deferred-review handling
	go d.classifyOrDeferAfterStop(msg.ID, msg.TranscriptPath)
}

func (d *Daemon) classifyOrDeferAfterStop(sessionID, transcriptPath string) {
	if d.sessionNeedsReviewAfterLongRun(sessionID) {
		d.logf("classifySessionState: long-run review already pending for session=%s", sessionID)
		return
	}

	session := d.store.Get(sessionID)
	if session == nil {
		d.clearLongRunTracking(sessionID)
		return
	}

	runDuration := d.consumeRunDuration(sessionID, session.StateSince)
	if runDuration >= longRunReviewThreshold {
		d.setNeedsReviewAfterLongRun(sessionID, transcriptPath)
		d.logf(
			"classifySessionState: deferring long-run classification session=%s duration=%s",
			sessionID,
			runDuration.Round(time.Second),
		)
		if session.State == protocol.SessionStatePendingApproval || session.State == protocol.SessionStateWaitingInput {
			d.broadcastSessionStateChanged(sessionID)
		} else {
			d.applyState(sessionStateChange{
				sessionID: sessionID,
				state:     protocol.StateWaitingInput,
				cause:     daemonObservation{},
			})
		}
		return
	}

	d.clearNeedsReviewAfterLongRun(sessionID)
	d.classifySessionState(sessionID, transcriptPath)
}

func (d *Daemon) handleSessionVisualized(sessionID string) {
	// The frontend reports the focused session here, so this doubles as our
	// signal for "currently selected session" (used by `attn open`).
	d.setSelectedSession(sessionID)

	transcriptPath, shouldClassify := d.consumeNeedsReviewAfterLongRun(sessionID)
	if !shouldClassify {
		return
	}
	d.logf("classifySessionState: resuming deferred long-run classification session=%s", sessionID)
	go d.classifySessionState(sessionID, transcriptPath)
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

	// Capability gates: agents can independently disable transcript parsing and
	// classification.
	transcriptEnabled := true
	classifierEnabled := true
	if driver := agentdriver.Get(string(session.Agent)); driver != nil {
		caps := agentdriver.EffectiveCapabilities(driver)
		transcriptEnabled = caps.HasTranscript
		classifierEnabled = caps.HasClassifier
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
		d.applyState(sessionStateChange{
			sessionID: sessionID,
			state:     protocol.StateWaitingInput,
			cause:     classifierObservation{observedAt: classificationStartTime},
		})
		return
	}

	if !transcriptEnabled {
		d.logf("classifySessionState: transcript disabled for agent=%s session=%s, setting idle", session.Agent, sessionID)
		d.applyState(sessionStateChange{
			sessionID: sessionID,
			state:     protocol.StateIdle,
			cause:     classifierObservation{observedAt: classificationStartTime},
		})
		return
	}

	if !classifierEnabled {
		d.logf("classifySessionState: classifier disabled for agent=%s session=%s, setting idle", session.Agent, sessionID)
		d.applyState(sessionStateChange{
			sessionID: sessionID,
			state:     protocol.StateIdle,
			cause:     classifierObservation{observedAt: classificationStartTime},
		})
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
	extract := d.extractLastAssistantMessage
	if d.classificationTranscriptExtractor != nil {
		extract = d.classificationTranscriptExtractor
	}
	lastMessage, assistantTurnID, err := extract(session, resolvedTranscriptPath, 500, classificationStartTime)
	if err != nil {
		if errors.Is(err, agentdriver.ErrNoNewAssistantTurn) {
			d.logf("classifySessionState: no new assistant turn for session %s, skipping classification", sessionID)
			return
		}
		d.logf("classifySessionState: transcript parse error for %s: %v", sessionID, err)
		d.logf("classifySessionState: unknown reason=transcript_parse_error session=%s transcript=%s", sessionID, resolvedTranscriptPath)
		d.applyState(sessionStateChange{
			sessionID: sessionID,
			state:     protocol.StateUnknown,
			cause:     classifierObservation{observedAt: classificationStartTime},
		})
		return
	}
	if strings.TrimSpace(assistantTurnID) != "" {
		defer d.clearClassifyingTurn(sessionID)
	}

	lastMessage = strings.TrimSpace(lastMessage)
	if lastMessage == "" {
		d.logf("classifySessionState: empty last message for session %s, setting idle", sessionID)
		d.applyState(sessionStateChange{
			sessionID: sessionID,
			state:     protocol.StateIdle,
			cause:     classifierObservation{observedAt: classificationStartTime},
		})
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
	state, err := d.runClassifier(session, lastMessage, 30*time.Second)
	if err != nil {
		d.logf("classifySessionState: classifier error for %s: %v", sessionID, err)
		d.logf("classifySessionState: unknown reason=classifier_error session=%s err=%v", sessionID, err)
		state = protocol.StateUnknown
	}
	if err == nil && state == protocol.StateUnknown {
		d.logf("classifySessionState: unknown reason=classifier_unknown_response session=%s", sessionID)
	}

	d.logf("classifySessionState: session %s classified as %s", sessionID, state)
	if strings.TrimSpace(assistantTurnID) != "" {
		d.setClassifiedTurnID(sessionID, assistantTurnID)
	}
	d.applyState(sessionStateChange{
		sessionID: sessionID,
		state:     state,
		cause:     classifierObservation{observedAt: classificationStartTime},
	})
}

func (d *Daemon) runClassifier(session *protocol.Session, text string, timeout time.Duration) (string, error) {
	if d.classifier != nil {
		return d.classifier.Classify(text, timeout)
	}
	if session != nil {
		driver := agentdriver.Get(string(session.Agent))
		if state, err, ok := agentdriver.ClassifyWithDriver(
			driver,
			text,
			d.store.GetSetting(executableSettingKey(string(session.Agent))),
			session.Directory,
			timeout,
		); ok {
			return state, err
		}
	}
	// Use Claude SDK for Claude sessions and fallback.
	return classifier.ClassifyWithClaude(text, timeout)
}

func (d *Daemon) resolveTranscriptPathForSession(session *protocol.Session, transcriptPath string) string {
	path := strings.TrimSpace(transcriptPath)
	if session == nil {
		return path
	}

	if path != "" {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	if driver := agentdriver.Get(string(session.Agent)); driver != nil {
		if tf, ok := agentdriver.GetTranscriptFinder(driver); ok {
			if discovered := strings.TrimSpace(tf.FindTranscript(session.ID, session.Directory, time.Now())); discovered != "" {
				return discovered
			}
		}
	}

	return path
}

func (d *Daemon) extractLastAssistantMessage(session *protocol.Session, transcriptPath string, maxChars int, classificationStart time.Time) (string, string, error) {
	if session == nil {
		lastMessage, err := transcript.ExtractLastAssistantMessage(transcriptPath, maxChars)
		return lastMessage, "", err
	}

	driver := agentdriver.Get(string(session.Agent))
	lastMessage, turnID, err := agentdriver.ExtractLastAssistantForClassification(
		driver,
		transcriptPath,
		maxChars,
		classificationStart,
		d.classifiedTurnID(session.ID),
	)
	if err != nil {
		return "", "", err
	}
	turnID = strings.TrimSpace(turnID)
	if turnID != "" && !d.beginClassifyingTurn(session.ID, turnID) {
		return "", "", agentdriver.ErrNoNewAssistantTurn
	}
	return lastMessage, turnID, nil
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

func (d *Daemon) markRunStartedIfNeeded(sessionID string) {
	now := time.Now()
	d.longRunMu.Lock()
	defer d.longRunMu.Unlock()
	if d.longRun == nil {
		d.longRun = make(map[string]longRunSession)
	}
	entry := d.longRun[sessionID]
	if entry.workingSince.IsZero() {
		entry.workingSince = now
	}
	entry.deferredTranscript = ""
	entry.needsReview = false
	d.longRun[sessionID] = entry
}

func (d *Daemon) consumeRunDuration(sessionID, fallbackStateSince string) time.Duration {
	now := time.Now()
	d.longRunMu.Lock()
	if entry, ok := d.longRun[sessionID]; ok && !entry.workingSince.IsZero() {
		startedAt := entry.workingSince
		entry.workingSince = time.Time{}
		if entry.deferredTranscript == "" && !entry.needsReview {
			delete(d.longRun, sessionID)
		} else {
			d.longRun[sessionID] = entry
		}
		d.longRunMu.Unlock()
		if startedAt.IsZero() || !now.After(startedAt) {
			return 0
		}
		return now.Sub(startedAt)
	}
	d.longRunMu.Unlock()

	if fallbackStateSince == "" {
		return 0
	}
	startedAt := protocol.Timestamp(fallbackStateSince).Time()
	if startedAt.IsZero() || !now.After(startedAt) {
		return 0
	}
	return now.Sub(startedAt)
}

func (d *Daemon) setNeedsReviewAfterLongRun(sessionID, transcriptPath string) {
	d.longRunMu.Lock()
	defer d.longRunMu.Unlock()
	if d.longRun == nil {
		d.longRun = make(map[string]longRunSession)
	}
	entry := d.longRun[sessionID]
	entry.deferredTranscript = strings.TrimSpace(transcriptPath)
	entry.needsReview = true
	d.longRun[sessionID] = entry
}

func (d *Daemon) consumeNeedsReviewAfterLongRun(sessionID string) (string, bool) {
	d.longRunMu.Lock()
	defer d.longRunMu.Unlock()
	entry, ok := d.longRun[sessionID]
	if !ok || !entry.needsReview {
		return "", false
	}
	transcriptPath := entry.deferredTranscript
	entry.deferredTranscript = ""
	entry.needsReview = false
	if entry.workingSince.IsZero() {
		delete(d.longRun, sessionID)
	} else {
		d.longRun[sessionID] = entry
	}
	return transcriptPath, true
}

func (d *Daemon) clearNeedsReviewAfterLongRun(sessionID string) {
	d.longRunMu.Lock()
	defer d.longRunMu.Unlock()
	entry, ok := d.longRun[sessionID]
	if !ok {
		return
	}
	entry.deferredTranscript = ""
	entry.needsReview = false
	if entry.workingSince.IsZero() {
		delete(d.longRun, sessionID)
	} else {
		d.longRun[sessionID] = entry
	}
}

func (d *Daemon) clearLongRunTracking(sessionID string) {
	d.longRunMu.Lock()
	defer d.longRunMu.Unlock()
	delete(d.longRun, sessionID)
}

func (d *Daemon) markForcedStopClassification(sessionID string) {
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	now := time.Now()
	d.forcedStopMu.Lock()
	defer d.forcedStopMu.Unlock()
	if d.forcedStop == nil {
		d.forcedStop = make(map[string]time.Time)
	}
	for id, markedAt := range d.forcedStop {
		if now.Sub(markedAt) > forcedStopSuppressTTL {
			delete(d.forcedStop, id)
		}
	}
	d.forcedStop[sessionID] = now
}

func (d *Daemon) consumeForcedStopClassification(sessionID string) bool {
	if strings.TrimSpace(sessionID) == "" {
		return false
	}
	now := time.Now()
	d.forcedStopMu.Lock()
	defer d.forcedStopMu.Unlock()
	if len(d.forcedStop) == 0 {
		return false
	}
	for id, markedAt := range d.forcedStop {
		if now.Sub(markedAt) > forcedStopSuppressTTL {
			delete(d.forcedStop, id)
		}
	}
	markedAt, ok := d.forcedStop[sessionID]
	if !ok {
		return false
	}
	delete(d.forcedStop, sessionID)
	return now.Sub(markedAt) <= forcedStopSuppressTTL
}

func (d *Daemon) clearForcedStopClassification(sessionID string) {
	d.forcedStopMu.Lock()
	defer d.forcedStopMu.Unlock()
	delete(d.forcedStop, sessionID)
}

func (d *Daemon) sessionNeedsReviewAfterLongRun(sessionID string) bool {
	d.longRunMu.Lock()
	defer d.longRunMu.Unlock()
	return d.longRun[sessionID].needsReview
}

func cloneSession(session *protocol.Session) *protocol.Session {
	if session == nil {
		return nil
	}
	clone := *session
	if len(session.Todos) > 0 {
		clone.Todos = append([]string(nil), session.Todos...)
	}
	return &clone
}

func (d *Daemon) sessionForBroadcast(session *protocol.Session) *protocol.Session {
	return d.sessionForBroadcastWithChiefOfStaff(
		session,
		d.chiefOfStaffSessionID(),
		d.delegatedFromChiefSessionIDs(),
	)
}

func (d *Daemon) sessionForBroadcastWithChiefOfStaff(
	session *protocol.Session,
	chiefOfStaffSessionID string,
	delegatedFromChief map[string]bool,
) *protocol.Session {
	clone := cloneSession(session)
	if clone == nil {
		return nil
	}
	if d.sessionNeedsReviewAfterLongRun(clone.ID) {
		clone.NeedsReviewAfterLongRun = protocol.Ptr(true)
	} else {
		clone.NeedsReviewAfterLongRun = nil
	}
	d.decorateSessionWithNudge(clone)
	d.decorateChiefOfStaffWithSessionID(clone, chiefOfStaffSessionID)
	d.decorateDelegatedFromChief(clone, delegatedFromChief)
	d.decorateSessionWithWorkspace(clone)
	d.decorateSessionWithWorkspaceMute(clone)
	return clone
}

func (d *Daemon) sessionsForBroadcast(sessions []*protocol.Session) []protocol.Session {
	if len(sessions) == 0 {
		return nil
	}
	chiefOfStaffSessionID := d.chiefOfStaffSessionID()
	delegatedFromChief := d.delegatedFromChiefSessionIDs()
	out := make([]protocol.Session, 0, len(sessions))
	for _, session := range sessions {
		if decorated := d.sessionForBroadcastWithChiefOfStaff(session, chiefOfStaffSessionID, delegatedFromChief); decorated != nil {
			out = append(out, *decorated)
		}
	}
	return out
}

func (d *Daemon) mergedSessionsForBroadcast() []protocol.Session {
	localSessions := d.sessionsForBroadcast(d.store.List(""))
	remoteSessions := d.remoteSessionsForBroadcast()
	if len(localSessions) == 0 {
		return remoteSessions
	}
	if len(remoteSessions) == 0 {
		return localSessions
	}
	merged := make([]protocol.Session, 0, len(localSessions)+len(remoteSessions))
	merged = append(merged, localSessions...)
	merged = append(merged, remoteSessions...)
	return merged
}

func (d *Daemon) remoteSessionsForBroadcast() []protocol.Session {
	if d.hubManager == nil {
		return nil
	}
	sessions := d.hubManager.RemoteSessions()
	chiefOfStaffSessionID := d.chiefOfStaffSessionID()
	for i := range sessions {
		d.decorateChiefOfStaffWithSessionID(&sessions[i], chiefOfStaffSessionID)
	}
	return sessions
}

func (d *Daemon) broadcastSessionStateChanged(sessionID string) {
	session := d.store.Get(sessionID)
	decorated := d.sessionForBroadcast(session)
	if decorated == nil {
		return
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:   protocol.EventSessionStateChanged,
		Session: decorated,
	})
	d.recomputeAndBroadcastWorkspaceForSession(sessionID)
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
				Session: d.sessionForBroadcast(s),
			})
			break
		}
	}
}

func (d *Daemon) handleQuery(conn net.Conn, msg *protocol.QueryMessage) {
	sessions := d.store.List(protocol.Deref(msg.Filter))
	resp := protocol.Response{
		Ok:         true,
		Sessions:   d.sessionsForBroadcast(sessions),
		Workspaces: d.listLocalWorkspaces(),
	}
	json.NewEncoder(conn).Encode(resp)
}

func (d *Daemon) handleHeartbeat(conn net.Conn, msg *protocol.HeartbeatMessage) {
	d.store.Touch(msg.ID)
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

type successfulPRObservation struct {
	prs        []*protocol.PR
	observedAt time.Time
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
	observedByHost := make(map[string]successfulPRObservation)
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

		// Order observations by when their provider fetch began. A slower, older
		// response must not supersede a newer explicit refresh that already recorded
		// withdrawn demand.
		observedAt := time.Now()
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
		observedByHost[host] = successfulPRObservation{prs: prs, observedAt: observedAt}
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

	// Automations consume the same successful provider refresh. Run delivery off
	// the polling goroutine so an unattended agent startup cannot delay the next
	// PR refresh or websocket update.
	for host, observation := range observedByHost {
		host, observation := host, observation
		go d.observeGitHubReviewRequests(host, observation.prs, observation.observedAt)
	}

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

	d.clearLongRunTracking(msg.Session.ID)
	msg.Session.Agent = normalizeStoredSessionAgent(string(msg.Session.Agent), protocol.SessionAgentCodex)
	workspaceID := strings.TrimSpace(msg.Session.WorkspaceID)
	if workspaceID == "" {
		workspaceID = "workspace-" + msg.Session.ID
	}
	msg.Session.WorkspaceID = workspaceID
	existingWS := d.store.GetWorkspace(workspaceID)
	workspaceRank := d.resolveWorkspaceRank(existingWS)
	if existingWS == nil {
		d.store.AddWorkspace(&protocol.Workspace{
			ID:        workspaceID,
			Title:     msg.Session.Label,
			Directory: msg.Session.Directory,
			Status:    protocol.WorkspaceStatusLaunching,
			Rank:      workspaceRank,
		})
	}
	d.workspaces.register(workspaceID, msg.Session.Label, msg.Session.Directory, workspaceRank, false, false)

	// Add session directly to store
	d.store.Add(&msg.Session)
	d.associateSessionWithWorkspace(msg.Session.ID, workspaceID)
	paneID := "pane-" + msg.Session.ID
	layout := workspacelayout.DefaultWorkspaceLayout(workspaceID, paneID, msg.Session.ID)
	if current := d.store.GetWorkspaceLayout(workspaceID); current != nil {
		layout = workspacelayout.NormalizeWorkspaceLayout(*current)
		if !workspacelayout.HasPane(layout.Layout, paneID) {
			layout.Panes = append(layout.Panes, workspacelayout.Pane{
				PaneID:    paneID,
				RuntimeID: msg.Session.ID,
				SessionID: msg.Session.ID,
				Kind:      workspacelayout.PaneKindAgent,
				Title:     msg.Session.Label,
				Status:    workspacelayout.PaneStatusReady,
			})
			targetPaneID := layout.ActivePaneID
			if targetPaneID == "" {
				targetPaneID = firstWorkspaceLayoutPaneID(layout)
			}
			if targetPaneID == "" || layout.Layout.Type == "" {
				layout.Layout = workspacelayout.DefaultLayout(paneID)
			} else {
				nextLayout, _ := workspacelayout.Split(
					layout.Layout,
					targetPaneID,
					paneID,
					newWorkspaceLayoutEntityID("split"),
					workspacelayout.DirectionVertical,
					workspacelayout.DefaultSplitRatio,
				)
				layout.Layout = nextLayout
			}
			layout.ActivePaneID = paneID
			layout = workspacelayout.NormalizeWorkspaceLayout(layout)
		}
	}
	if err := d.store.SaveWorkspaceLayout(layout); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.sendOK(conn)

	// Broadcast to WebSocket clients
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:   protocol.EventSessionRegistered,
		Session: d.sessionForBroadcast(&msg.Session),
	})
	d.broadcastWorkspaceLayout(workspaceID)
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
	observedByHost := make(map[string]successfulPRObservation)
	skippedHosts := make(map[string]bool)
	var firstErr error
	successCount := 0

	for _, host := range d.ghRegistry.Hosts() {
		client, ok := d.ghRegistry.Get(host)
		if !ok {
			continue
		}
		observedAt := time.Now()
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
		observedByHost[host] = successfulPRObservation{prs: prs, observedAt: observedAt}
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
	for host, observation := range observedByHost {
		host, observation := host, observation
		go d.observeGitHubReviewRequests(host, observation.prs, observation.observedAt)
	}
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
	d.logf("Branch monitoring started (%s interval)", branchMonitorInterval)

	// Initial check
	d.checkAllBranches()

	ticker := time.NewTicker(branchMonitorInterval)
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
		Sessions: d.mergedSessionsForBroadcast(),
	})
}

func (d *Daemon) listEndpointInfos() []protocol.EndpointInfo {
	if d.hubManager == nil {
		records := d.store.ListEndpoints()
		out := make([]protocol.EndpointInfo, 0, len(records))
		for _, record := range records {
			out = append(out, protocol.EndpointInfo{
				ID:        record.ID,
				Name:      record.Name,
				SshTarget: record.SSHTarget,
				Status:    "disconnected",
				Enabled:   protocol.Ptr(record.Enabled),
			})
		}
		return out
	}
	return d.hubManager.List()
}

func (d *Daemon) broadcastEndpointStatusChanged(info protocol.EndpointInfo) {
	d.broadcastMessage(&protocol.EndpointStatusChangedMessage{
		Event:    protocol.EventEndpointStatusChanged,
		Endpoint: info,
	})
}

func (d *Daemon) broadcastEndpointsUpdated() {
	d.broadcastMessage(&protocol.EndpointsUpdatedMessage{
		Event:     protocol.EventEndpointsUpdated,
		Endpoints: d.listEndpointInfos(),
	})
}

// handleHealth returns daemon health status
func (d *Daemon) handleHealth(w http.ResponseWriter, r *http.Request) {
	sessions := d.store.List("")
	prs := d.store.ListPRs("")
	dataDir, socketPath, routingPathError := healthRoutingPaths()

	health := map[string]interface{}{
		"status":             "ok",
		"version":            buildinfo.Version,
		"build_time":         buildinfo.BuildTime,
		"protocol":           protocol.ProtocolVersion,
		"source_fingerprint": buildinfo.SourceFingerprint,
		"git_commit":         buildinfo.GitCommit,
		"daemon_instance_id": d.daemonInstanceID,
		"sessions":           len(sessions),
		"prs":                len(prs),
		"ws_clients":         d.wsHub.ClientCount(),
		"github_available":   d.githubAvailable(),
		// Profile identity — lets clients (the app, in particular) verify
		// they're connected to the daemon they expect and refuse to
		// operate on a mismatch.
		"profile":     config.ProfileLabel(),
		"data_dir":    dataDir,
		"socket_path": socketPath,
		"port":        config.WSPort(),
	}
	if routingPathError != "" {
		health["routing_path_error"] = routingPathError
	}

	setNoStoreHeaders(w.Header())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(health)
}

func healthRoutingPaths() (dataDir, socketPath, routingPathError string) {
	rawDataDir := config.DataDir()
	rawSocketPath := config.SocketPath()
	dataDir, dataErr := config.CanonicalRuntimePath(rawDataDir)
	if dataErr != nil {
		dataDir = rawDataDir
	}
	socketPath, socketErr := config.CanonicalRuntimePath(rawSocketPath)
	if socketErr != nil {
		socketPath = rawSocketPath
	}
	if dataErr != nil || socketErr != nil {
		routingPathError = fmt.Sprintf("data_dir: %v; socket_path: %v", dataErr, socketErr)
	}
	return dataDir, socketPath, routingPathError
}
