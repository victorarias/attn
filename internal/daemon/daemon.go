package daemon

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/classifier"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/diag"
	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/fsdoc"
	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/github"
	"github.com/victorarias/attn/internal/headless"
	"github.com/victorarias/attn/internal/hostsession"
	"github.com/victorarias/attn/internal/hub"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/logging"
	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/pathutil"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/statetrace"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/supervise"
	"github.com/victorarias/attn/internal/transcript"
	"github.com/victorarias/attn/internal/workspacelayout"
)

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
	ChangedSessionIDs []string
}

func (r *workerReconcileReport) markChanged(sessionID string) {
	r.Changed = true
	r.ChangedSessionIDs = append(r.ChangedSessionIDs, sessionID)
}

const (
	forcedStopSuppressTTL = 30 * time.Second
	branchMonitorInterval = 15 * time.Second

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

type Daemon struct {
	socketPath                  string
	pidPath                     string
	pidFile                     *os.File
	dataRoot                    string
	daemonInstanceID            string
	clientToken                 string
	store                       *store.Store
	automationMu                sync.Mutex
	wsAutomationMutationTimeout time.Duration
	automationObservationMu     sync.Mutex
	automationObservationLocks  map[string]*sync.Mutex
	automationRepoMu            sync.Mutex
	automationRepos             map[string]*sync.Mutex
	automationDeliveryHook      func(*store.AutomationRun) error
	wsWriteTimeout              time.Duration
	wsPingInterval              time.Duration
	wsPingTimeout               time.Duration
	listener                    net.Listener
	httpServer                  *http.Server
	httpListener                net.Listener
	httpHandler                 http.Handler
	diagServer                  *diag.Server
	wsHub                       *wsHub
	presentSince                time.Time
	presenceMu                  sync.RWMutex
	crewLifecycleState          *crewLifecycleMemo
	crewMemoOnce                sync.Once
	done                        chan struct{}
	logger                      *logging.Logger
	debugLogging                bool
	ghRegistry                  *github.ClientRegistry
	hubManager                  *hub.Manager
	classifier                  Classifier
	// What auto mode's repo_visibility slot knows, keyed "host/owner/name".
	// A launch reads it and never waits on it; see automode_detect.go.
	repoVisibilityKnown               map[string]string
	repoVisibilityPending             map[string]bool
	repoVisibilityMu                  sync.Mutex
	gitCoordMu                        sync.Mutex
	gitCoord                          *gitCoordinator
	warnings                          []protocol.DaemonWarning
	warningsMu                        sync.RWMutex
	legacyTicketRecoveryFinishOnce    sync.Once
	legacyTicketSnapshotIdentity      func(string) (store.LegacyTicketRecoverySource, error)
	legacyTicketSnapshotRead          func(string) (store.LegacyTicketSnapshotRead, error)
	legacyRecoveryArtifactWrite       func(string, []byte) error
	ptyBackend                        ptybackend.Backend
	ptySettingsMu                     sync.Mutex
	ptySettingsChangeMu               sync.Mutex
	upgradingMu                       sync.Mutex
	upgradingWorkers                  map[string]bool
	hostSessions                      *hostsession.Manager
	hostSessionsMu                    sync.Mutex
	watchersMu                        sync.Mutex
	transcriptWatch                   map[string]*transcriptWatcher
	transcriptWatcherSessionLookup    func(string) *protocol.Session
	transcriptResumeLookup            func(protocol.SessionAgent, string) string
	classifiedMu                      sync.Mutex
	classifiedTurn                    map[string]string
	classifyingTurn                   map[string]string
	classificationTranscriptExtractor func(*protocol.Session, string, int, time.Time) (string, string, error)
	forcedStopMu                      sync.Mutex
	forcedStop                        map[string]time.Time
	pendingConversationMu             sync.Mutex
	pendingConversation               map[string]agentConversationObservation
	ticketReconcileMu                 sync.Mutex
	ticketReconcileExec               func(ctx context.Context, in ticketReconcileInputs) (agentdriver.HeadlessTaskResult, error)
	ticketReconcileDone               func(ticketID string)
	ticketOrphanFirstSeen             map[string]time.Time
	ticketReconcilePRFetch            prStateFetcher
	sessionTitleMu                    sync.Mutex
	sessionTitleExec                  func(ctx context.Context, session *protocol.Session, conversation string) (string, error)
	sessionTitleAttempted             map[string]struct{}
	sessionTitleInitialPrompt         map[string][sha256.Size]byte
	ticketArtifactMu                  sync.Mutex
	seedArtifactMu                    sync.Mutex
	delegationMu                      sync.Mutex
	delegationRunning                 map[string]bool
	delegationWorktreePrepareHook     func(path string)
	delegationFinalizeHook            func() error
	delegationWaitsForFirstTurn       bool
	launchWatchMu                     sync.Mutex
	launchWatches                     map[string]*launchWatch
	reloadingMu                       sync.Mutex
	reloadingSessions                 map[string]bool
	prepareSessionTeardownHook        func(string) error
	teardownMu                        sync.Mutex
	tearingDown                       map[string]chan struct{}
	reloadLocksMu                     sync.Mutex
	reloadLocks                       map[string]*sync.Mutex
	spawnLocksMu                      sync.Mutex
	spawnLocks                        map[string]*spawnLock
	sessionInputOnce                  sync.Once
	sessionInputState                 *sessionInputModule
	agentMailboxMu                    sync.Mutex
	agentMailboxDoorbells             map[string]*agentMailboxDoorbellState
	agentMailboxCooldownOverride      time.Duration
	postInitialPrompt                 map[string]struct{}
	agentMailboxDrainScheduledHook    func(sessionID string)
	agentMailboxDrainHook             func(sessionID string, delivered int)
	crewWakeMu                        sync.Mutex
	crewExitedMu                      sync.Mutex
	crewExitedSessions                map[string]string
	crewWakeStartHook                 func(memberID string)
	crewWakeAfterClaimHook            func(memberID, sessionID string)
	stateTraceOnce                    sync.Once
	stateTrace                        *statetrace.Recorder
	sessionEvidenceOnce               sync.Once
	sessionEvidence                   *sessionEvidenceTable
	sessionDwellOnce                  sync.Once
	sessionDwell                      *dwellGate
	pluginDriverSilenceOnce           sync.Once
	pluginDriverSilenceWatch          *pluginDriverSilenceWatch
	pluginDriverSilenceGraceOverride  time.Duration
	sessionStateReasonOnce            sync.Once
	sessionStateReason                *sessionStateReasons
	nudgeMu                           sync.Mutex
	nudgeCountdowns                   map[string]*nudgeCountdown
	unreadCache                       map[string]bool
	nudgeSuppressedThrough            map[string]int64
	deliveryMu                        sync.Mutex
	watchLeaseUntil                   map[string]time.Time
	nudgeWindowOverride               time.Duration
	ticketBundleWindowOverride        time.Duration
	nudgeFireHook                     func(sessionID, action string)
	ticketRebuildBeforeArmHook        func(sessionID string, deadline time.Time)
	lastInputMu                       sync.Mutex
	lastUserInputAt                   map[string]time.Time
	lastAutoSettleActivityAt          map[string]time.Time
	// Always taken before autoSettleMu; never the other way round.
	autoSettleFireMu sync.Mutex

	autoSettleMu            sync.Mutex
	autoSettleTimers        map[string]*autoSettleTimer
	autoSettleDismissals    map[string]bool
	autoSettleFireHook      func(sessionID, outcome string)
	autoSettlePreSettleHook func()

	snoozeMu sync.Mutex

	recoveryMu          sync.RWMutex
	recovering          bool
	recoverySettled     chan struct{}
	notebookMu          sync.Mutex
	notebookStore       *notebook.Store
	notebookWatcherMu   sync.Mutex
	notebookWatcher     *notebook.Watcher
	notebookWatchedRoot string
	fsMu                sync.Mutex
	fsStores            map[string]*fsdoc.Store
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
	// Derived from the data directory, as the artifact-building CLI does: a
	// socket relocated by ATTN_SOCKET_PATH would split the two.
	appsDir              string
	appRuntimeMu         sync.Mutex
	appRuntimeSupervisor *supervise.Supervisor
	appRuntimeSupervise  supervise.Options
	appRuntimeWait       time.Duration
	appPingWait          time.Duration
	appDispatchWait      time.Duration
	appRuntimeConn       *appRuntimeConnection
	appRuntimeReady      chan struct{}
	appDispatchMu        sync.Mutex
	appDispatches        map[string]*appDispatch
	appDispatchSeq       uint64
	appLaneMu            sync.Mutex
	appLanes             map[string]appLane
	appStallMu           sync.Mutex
	appStalls            map[string]*appStall
	appEnteredMu         sync.Mutex
	appEntered           map[string]enteredHandler
	appEnteredGen        uint64
	appEnteredSeq        uint64
	appCrashMu           sync.Mutex
	appCrashes           map[string][]time.Time
	busPinMu             sync.Mutex
	busPinEpisodes       map[string]*busPinEpisode
	busPinAge            time.Duration
	appWatcherMu         sync.Mutex
	appWatchers          map[*appWatcher]struct{}
	appClock             func() time.Time
	appAutoDisableWait   time.Duration
	removePlugin         func(pluginDir, name string) error
	pluginActionMu       sync.Mutex
	bundledPluginMu      sync.Mutex
	bundledPluginSet     map[string]struct{}
	bundledPluginLoaded  bool

	worktreePluginCallTimeout         time.Duration
	worktreeCreateProviderCallTimeout time.Duration

	loginShellEnvMu sync.RWMutex
	loginShellEnv   []string

	terminalThemeMu sync.Mutex
	terminalTheme   pty.TerminalTheme

	workspaces *workspaceRegistry

	selectedSessionMu   sync.RWMutex
	selectedSessionID   string
	selectedWorkspaceID string

	openTileMu sync.Mutex

	lastUserActivityAtNano atomic.Int64

	markdownSeenMu sync.Mutex
	markdownSeen   map[string]tileContentSig

	browserControlMu sync.Mutex
	browserControl   map[string]browserControlPending

	lastBackupMu sync.Mutex
	lastBackupAt time.Time

	workflowBroadcastMu       sync.Mutex
	workflowDirty             map[string]bool
	workflowEngineMu          sync.Mutex
	workflowEngineConn        map[string]workflowEngineSink
	workflowBroadcastHook     func(*protocol.WorkflowRunUpdatedMessage)
	gardenBroadcastHook       func([]protocol.Seed, int)
	appsBroadcastHook         func([]protocol.AppRegistryEntry)
	gardenMintID              func() (string, error)
	gardenMintNoteID          func() (string, error)
	gardenNow                 func() time.Time
	gardenDispatchBeforeWrite func(string)
	gardenDispatchAfterWrite  func(string)
	gardenReviewMu            sync.Mutex
	dispatchSeedsMu           sync.Mutex
	dispatchSeeds             map[string]string
	dispatchFromChief         map[string]bool
	dispatchProjectionRevs    map[string]int64
	dispatchSeedsLoaded       bool

	gardenNotePageSize int

	automationsBroadcastHook func(*protocol.AutomationsChangedMessage)

	workspaceContextCheckoutMu sync.Mutex

	eventBus       *bus.Bus
	busUnsubscribe func()

	docSubsMu              sync.Mutex
	docSubs                map[string]*docSubscription
	docSubsSeq             int64
	docUnsubHooks          func()
	conversationUnsubHooks func()
	sessionPRUnsubHooks    func()
	sessionPRHosts         func(host string) (sessionPRHost, bool)
	// Test seam: runs between a lifecycle move's read and its write.
	beforeSeedMoveWrite func(seedID string)

	harvestWhenMu        sync.Mutex
	harvestWhenUntracked map[string]bool

	notebookPendingMu    sync.Mutex
	notebookPendingPaths map[string][]string

	snapshotMu           sync.Mutex
	snapshotDepth        int
	pendingSnapshots     map[string]func()
	pendingSnapshotOrder []string

	// Guards the POINTER swap only (startJobQueue replaces the placeholder late
	// in Start); read via jobQueueRef(), write via setJobQueue().
	jobQueueMu                          sync.RWMutex
	jobQueue                            *jobs.Runner
	keeperCompactThreshold              int
	keeperCompactDebounce               time.Duration
	keeperCompactTimeout                time.Duration
	workspaceContextBeforeKeeperApply   func()
	workspaceContextCompactionExecution func(
		ctx context.Context,
		config keeperCompactConfig,
		canonical *protocol.WorkspaceContext,
	) (keeperCompactExecution, error)

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

	sessionActivityExecution func(
		ctx context.Context,
		provider agentdriver.HeadlessTaskProvider,
		request agentdriver.HeadlessTaskRequest,
	) (agentdriver.HeadlessTaskResult, error)
	gardenAdvisorResolve func(
		config gardenAdvisorConfig,
	) (agentdriver.HeadlessTaskProvider, string, error)

	sessionActivityRunsMu sync.Mutex
	sessionActivityRuns   map[string]sessionActivityRun

	notebookNarrateActivityMu sync.Mutex
	notebookNarrateActivity   map[string]struct{}
}

func (d *Daemon) addWarning(code, message string) {
	d.warningsMu.Lock()
	defer d.warningsMu.Unlock()
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
	if value && !d.recovering {
		d.recoverySettled = make(chan struct{})
	}
	if !value && d.recovering && d.recoverySettled != nil {
		close(d.recoverySettled)
	}
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

var recoveryAlreadySettled = func() <-chan struct{} {
	settled := make(chan struct{})
	close(settled)
	return settled
}()

func (d *Daemon) recoverySettledSignal() <-chan struct{} {
	d.recoveryMu.RLock()
	defer d.recoveryMu.RUnlock()
	if !d.recovering || d.recoverySettled == nil {
		return recoveryAlreadySettled
	}
	return d.recoverySettled
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

func New(socketPath string) *Daemon {
	logger, _ := logging.New(logging.DefaultLogPath())

	if err := pathutil.EnsureGUIPath(); err != nil {
		logger.Infof("PATH recovery failed: %v", err)
	}

	classifier.SetLogger(func(format string, args ...interface{}) {
		logger.Infof(format, args...)
	})
	git.SetLogFunc(func(format string, args ...interface{}) {
		logger.Infof(format, args...)
	})

	dbPath := config.DBPath()
	sessionStore, err := store.NewWithDB(dbPath)
	var startupWarnings []protocol.DaemonWarning
	if err != nil {
		logger.Infof("Failed to open DB at %s: %v (using in-memory)", dbPath, err)
		sessionStore = store.New()
		startupWarnings = append(startupWarnings, protocol.DaemonWarning{
			Code: warnPersistenceDegraded,
			Message: fmt.Sprintf(
				"Persistence degraded: unable to open durable state at %s. Running in-memory only; session state will not survive daemon restarts. See daemon log in %s for details.",
				dbPath,
				config.LogPath(),
			),
		})
	}

	legacyPath := config.StatePath()
	if _, err := os.Stat(legacyPath); err == nil {
		os.Remove(legacyPath)
		logger.Infof("Removed legacy state file: %s", legacyPath)
	}

	dataRoot := filepath.Dir(socketPath)
	pidPath := filepath.Join(dataRoot, "attn.pid")
	manager := pty.NewManager(logger.Infof)

	d := &Daemon{
		socketPath:          socketPath,
		pidPath:             pidPath,
		dataRoot:            dataRoot,
		store:               sessionStore,
		wsHub:               newWSHub(),
		presentSince:        time.Now(),
		done:                make(chan struct{}),
		logger:              logger,
		debugLogging:        logger != nil && logger.DebugEnabled(),
		ghRegistry:          github.NewClientRegistry(),
		hubManager:          nil,
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
		forcedStop:          make(map[string]time.Time),
		pendingConversation: make(map[string]agentConversationObservation),
		tailscale:           newTailscaleRuntime(),
		plugins:             newPluginRegistry(),
		pluginHealthEnabled: true,
		pluginDir:           pluginDirForSocket(socketPath),
		bundledPluginDir:    bundledPluginDirForExecutable(),
		appsDir:             config.AppsDir(),
		workspaces:          newWorkspaceRegistry(),
		spawnLocks:          make(map[string]*spawnLock),
	}
	d.delegationWaitsForFirstTurn = true
	d.ticketReconcileExec = d.execTicketReconcileClassifier
	d.ensureEventBus()
	d.sessionTitleExec = d.execSessionTitle
	return d
}

func NewForTesting(socketPath string) *Daemon {
	dataRoot := filepath.Dir(socketPath)
	pidPath := filepath.Join(dataRoot, "attn.pid")
	manager := pty.NewManager(nil)
	d := &Daemon{
		socketPath:          socketPath,
		pidPath:             pidPath,
		dataRoot:            dataRoot,
		store:               store.New(),
		wsHub:               newWSHub(),
		presentSince:        time.Now(),
		done:                make(chan struct{}),
		logger:              nil,
		ghRegistry:          github.NewClientRegistry(),
		hubManager:          nil,
		gitCoord:            newGitCoordinator(),
		ptyBackend:          ptybackend.NewEmbedded(manager),
		transcriptWatch:     make(map[string]*transcriptWatcher),
		pendingInitialWS:    make(map[*wsClient]struct{}),
		startedCh:           make(chan struct{}),
		classifiedTurn:      make(map[string]string),
		classifyingTurn:     make(map[string]string),
		forcedStop:          make(map[string]time.Time),
		pendingConversation: make(map[string]agentConversationObservation),
		tailscale:           newTailscaleRuntime(),
		plugins:             newPluginRegistry(),
		pluginDir:           pluginDirForSocket(socketPath),
		bundledPluginDir:    bundledPluginDirForExecutable(),
		appsDir:             config.AppsDir(),
		workspaces:          newWorkspaceRegistry(),
		workflowDirty:       make(map[string]bool),
		workflowEngineConn:  make(map[string]workflowEngineSink),
		spawnLocks:          make(map[string]*spawnLock),
		jobQueue:            jobs.New(jobs.Options{}),
	}
	d.ensureEventBus()
	return d
}

func NewWithGitHubClient(socketPath string, ghClient github.GitHubClient) *Daemon {
	dataRoot := filepath.Dir(socketPath)
	pidPath := filepath.Join(dataRoot, "attn.pid")
	registry := github.NewClientRegistry()
	if client, ok := ghClient.(*github.Client); ok {
		registry.Register(client.Host(), client)
	}
	manager := pty.NewManager(nil)
	d := &Daemon{
		socketPath:          socketPath,
		pidPath:             pidPath,
		dataRoot:            dataRoot,
		store:               store.New(),
		wsHub:               newWSHub(),
		presentSince:        time.Now(),
		done:                make(chan struct{}),
		logger:              nil,
		ghRegistry:          registry,
		hubManager:          nil,
		gitCoord:            newGitCoordinator(),
		ptyBackend:          ptybackend.NewEmbedded(manager),
		transcriptWatch:     make(map[string]*transcriptWatcher),
		pendingInitialWS:    make(map[*wsClient]struct{}),
		startedCh:           make(chan struct{}),
		classifiedTurn:      make(map[string]string),
		classifyingTurn:     make(map[string]string),
		forcedStop:          make(map[string]time.Time),
		pendingConversation: make(map[string]agentConversationObservation),
		tailscale:           newTailscaleRuntime(),
		plugins:             newPluginRegistry(),
		pluginDir:           pluginDirForSocket(socketPath),
		bundledPluginDir:    bundledPluginDirForExecutable(),
		appsDir:             config.AppsDir(),
		workspaces:          newWorkspaceRegistry(),
		workflowDirty:       make(map[string]bool),
		workflowEngineConn:  make(map[string]workflowEngineSink),
		spawnLocks:          make(map[string]*spawnLock),
		jobQueue:            jobs.New(jobs.Options{}),
	}
	d.ensureEventBus()
	return d
}

func (d *Daemon) Start() error {
	if err := d.resolveAppRuntimeTripwires(); err != nil {
		return fmt.Errorf("resolve app runtime tripwires: %w", err)
	}
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
	if d.forcedStop == nil {
		d.forcedStop = make(map[string]time.Time)
	}
	if d.ptyBackend == nil {
		d.ptyBackend = ptybackend.NewEmbedded(pty.NewManager(d.logf))
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
	startSucceeded := false
	if err := d.acquirePIDLock(); err != nil {
		return fmt.Errorf("acquire PID lock: %w", err)
	}
	defer func() {
		if startSucceeded {
			return
		}
		d.sessionInputs().stopRetries()
		d.stopAgentMailboxDoorbells()
		d.stopInstalledPlugins()
		if d.httpServer != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = d.httpServer.Shutdown(ctx)
			cancel()
		}
		// Shutdown closes only listeners the server is already serving, so a
		// failure between bind and Serve() would leak the port.
		if d.httpListener != nil {
			_ = d.httpListener.Close()
			d.httpListener = nil
		}
		if d.listener != nil {
			_ = d.listener.Close()
			d.listener = nil
			os.Remove(d.socketPath)
		}
		d.releasePIDLock()
	}()
	d.ensurePluginSupervisor()
	d.applyHeadlessContextWindowCap()
	d.applyHeadlessTasksMode()
	if err := d.startEventBus(); err != nil {
		return fmt.Errorf("start event bus: %w", err)
	}
	reapedWorkspaceIDs := d.loadWorkspacesFromStore()
	if d.daemonInstanceID == "" {
		instanceID, err := enrollment.EnsureDaemonID(d.dataRoot)
		if err != nil {
			return fmt.Errorf("ensure daemon instance id: %w", err)
		}
		d.daemonInstanceID = instanceID
	}
	if d.clientToken == "" {
		token, err := config.EnsureClientToken(d.dataRoot)
		if err != nil {
			return fmt.Errorf("ensure client token: %w", err)
		}
		d.clientToken = token
	}
	if err := d.ensureEnrollment(); err != nil {
		return fmt.Errorf("ensure enrollment record: %w", err)
	}
	d.ensureGardenCollections()
	waitForLegacyTicketRecovery, err := d.prepareLegacyTicketRecovery()
	if err != nil {
		return fmt.Errorf("prepare legacy ticket recovery: %w", err)
	}
	d.ensureCrewCollections()
	d.importCrewHomes()
	if err := d.migrateCrewTicketIdentities(); err != nil {
		return fmt.Errorf("migrate crew ticket identities: %w", err)
	}
	if d.hubManager == nil {
		d.hubManager = hub.NewManager(
			d.store,
			d.broadcastEndpointStatusChanged,
			d.publishEndpointSessionsChanged,
			d.broadcastRawWSMessage,
			d.logf,
			d.homeDaemonIDForEnrollment,
		)
	}
	selectedBackend := strings.TrimSpace(strings.ToLower(os.Getenv("ATTN_PTY_BACKEND")))
	if selectedBackend == "" {
		selectedBackend = "migrating"
	}
	switch selectedBackend {
	case "embedded":
	case "worker":
		workerBackend, err := ptybackend.NewWorker(ptybackend.WorkerBackendConfig{
			DataRoot:         d.dataRoot,
			DaemonInstanceID: d.daemonInstanceID,
			BinaryPath:       strings.TrimSpace(os.Getenv("ATTN_PTY_WORKER_BINARY")),
			Logf:             d.logf,
			OnTerminalBuild:  d.handleTerminalBuildChanged,
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
	case "shared":
		sharedBackend, err := ptybackend.NewSharedHost(ptybackend.WorkerBackendConfig{
			DataRoot:         d.dataRoot,
			DaemonInstanceID: d.daemonInstanceID,
			BinaryPath:       strings.TrimSpace(os.Getenv("ATTN_PTY_HOST_BINARY")),
			Logf:             d.logf,
			OnTerminalBuild:  d.handleTerminalBuildChanged,
		})
		if err != nil {
			d.logf("failed to initialize shared PTY host: %v; falling back to embedded", err)
			d.addWarning(warnPTYBackendFallback, fmt.Sprintf("Failed to initialize shared PTY host (%v). Falling back to embedded.", err))
		} else {
			d.ptyBackend = sharedBackend
			d.logf("using PTY backend: shared")
		}
	case "migrating":
		legacyBackend, legacyErr := ptybackend.NewWorker(ptybackend.WorkerBackendConfig{
			DataRoot:         d.dataRoot,
			DaemonInstanceID: d.daemonInstanceID,
			BinaryPath:       strings.TrimSpace(os.Getenv("ATTN_PTY_WORKER_BINARY")),
			Logf:             d.logf,
			OnTerminalBuild:  d.handleTerminalBuildChanged,
		})
		if legacyErr != nil {
			d.logf("failed to initialize legacy PTY workers: %v; falling back to embedded", legacyErr)
			d.addWarning(warnPTYBackendFallback, fmt.Sprintf("Failed to initialize legacy PTY workers (%v). Falling back to embedded.", legacyErr))
			break
		}
		sharedBackend, sharedErr := ptybackend.NewSharedHost(ptybackend.WorkerBackendConfig{
			DataRoot:         d.dataRoot,
			DaemonInstanceID: d.daemonInstanceID,
			BinaryPath:       strings.TrimSpace(os.Getenv("ATTN_PTY_HOST_BINARY")),
			Logf:             d.logf,
			OnTerminalBuild:  d.handleTerminalBuildChanged,
		})
		if sharedErr != nil {
			d.ptyBackend = legacyBackend
			d.logf("shared PTY host initialization failed: %v; new sessions remain on legacy workers", sharedErr)
			d.addWarning(warnPTYBackendFallback, fmt.Sprintf("Shared PTY host is unavailable (%v). New terminals will keep using dedicated workers.", sharedErr))
			break
		}

		useSharedForNew := false
		sharedEnabled := parseBooleanSetting(d.store.GetSetting(SettingSharedPTYHostEnabled))
		if sharedEnabled && shouldRunWorkerStartupProbe() {
			probeCtx, cancelProbe := context.WithTimeout(context.Background(), workerStartupProbeTimeout)
			probeErr := sharedBackend.Probe(probeCtx)
			cancelProbe()
			if probeErr != nil {
				d.logf("shared PTY host startup probe failed: %v; new sessions remain on legacy workers", probeErr)
				d.addWarning(warnPTYBackendFallback, fmt.Sprintf("Shared PTY host probe failed (%v). New terminals will keep using dedicated workers.", probeErr))
			} else {
				useSharedForNew = true
			}
		} else if sharedEnabled && strings.TrimSpace(os.Getenv("ATTN_PTY_HOST_BINARY")) != "" {
			// Tests and controlled profiles can skip the process probe only when
			// they name the host binary explicitly.
			useSharedForNew = true
		}
		migratingBackend, err := ptybackend.NewMigrating(legacyBackend, sharedBackend, useSharedForNew)
		if err != nil {
			d.logf("failed to initialize PTY migration router: %v; using legacy workers", err)
			d.ptyBackend = legacyBackend
			break
		}
		d.ptyBackend = migratingBackend
		if useSharedForNew {
			d.logf("using PTY backend: migrating (existing=owner, new=shared)")
		} else {
			d.logf("using PTY backend: migrating (existing=owner, new=legacy)")
		}
	default:
		d.logf("unsupported PTY backend %q, falling back to embedded", selectedBackend)
		d.addWarning(
			warnPTYBackendUnsupported,
			fmt.Sprintf("PTY backend %q is not available in this build. Falling back to embedded.", selectedBackend),
		)
	}

	// A login shell costs ~130ms; the first PTY spawn must not pay it.
	go d.warmLoginShellEnvCache()

	d.setRecovering(true)
	defer func() {
		if !startSucceeded {
			d.setRecovering(false)
		}
	}()

	listener, err := listenUnixAtomically(d.socketPath)
	if err != nil {
		return err
	}
	d.listener = listener
	d.log("daemon started")
	d.startInstalledPlugins()
	d.restoreAppRuntimePark()
	if err := d.repairInterruptedAppInvocations(); err != nil {
		return err
	}
	d.registerAppConsumers()

	d.wsHub.logf = d.logf
	go d.wsHub.run()

	go d.startWorkflowBroadcastLoop(d.doneContext())

	go d.runMarkdownContentWatcher(d.done)

	if hooks, ok := d.ptyBackend.(ptybackend.LifecycleHooks); ok {
		hooks.SetExitHandler(func(info ptybackend.ExitInfo) { d.handlePTYExit(info) })
		hooks.SetStateHandler(d.handlePTYState)
	}

	d.initHTTPServer()
	if err := d.listenHTTP(); err != nil {
		d.logf("%v", err)
		return err
	}
	go d.runHTTPServer()
	d.maybeStartDiagServer()
	d.removeLegacyEmbeddedTailscaleState()
	d.migrateKeeperCompactSettingKey()
	d.migrateNotebookCronSettingKeys()
	go d.ensureTailscaleServeFromSettingsAndBroadcast()
	d.hubManager.Start(d.doneContext())

	githubHostsReady := make(chan struct{})
	go func() {
		defer close(githubHostsReady)
		if err := d.refreshGitHubHosts(); err != nil {
			d.logf("Initial GitHub host discovery failed: %v", err)
		}
		go d.pollPRs()
		go d.refreshGitHubHostsLoop()
	}()
	recoveryStartedAt := time.Now()

	go d.monitorBranches()

	go d.runTicketReconcileSweep()

	go d.runEvidenceResolveLoop()
	go d.runModelCaptureLoop()

	d.startJobQueue()
	if waitForLegacyTicketRecovery {
		if err := d.enqueueLegacyTicketRecovery(); err != nil {
			return fmt.Errorf("enqueue legacy ticket recovery: %w", err)
		}
	} else {
		d.finishLegacyTicketRecoveryUpgrade()
	}
	d.startPermanentMaintenance()

	for _, wsID := range reapedWorkspaceIDs {
		d.enqueueFinalNarrateWorkspace(wsID)
	}

	go func() {
		d.performStartupPTYRecovery(recoveryStartedAt)
		d.seedQueuedAgentMailboxItems()
		recoverAutomationsAfterGitHubReady(githubHostsReady, d.recoverAutomations)
		d.setRecovering(false)
		d.resumePendingDelegations()
	}()

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

func (d *Daemon) pruneSessionsWithoutPTY(cutoff time.Time) int {
	if d.store == nil {
		return 0
	}

	liveIDs := make(map[string]struct{})
	for _, id := range d.liveRuntimeSessionIDs(context.Background()) {
		liveIDs[id] = struct{}{}
	}

	sessions := d.store.List("")
	removed := 0
	recoverable := 0
	for _, session := range sessions {
		if _, ok := liveIDs[session.ID]; ok {
			continue
		}
		if sessionUpdatedAfter(session, cutoff) {
			continue
		}
		d.releaseExitedCrewBinding(session.ID)
		if d.canReviveSession(session) {
			if session.State == protocol.SessionStateRecoverable {
				continue
			}
			d.applyState(sessionStateChange{
				sessionID: session.ID,
				state:     string(protocol.SessionStateRecoverable),
				cause:     startupRecovery{},
			})
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

func (d *Daemon) canReviveSession(session *protocol.Session) bool {
	if session == nil || d.store == nil {
		return false
	}
	if d.store.SessionCloseIntentional(session.ID) {
		return false
	}
	intent, ok := d.store.LaunchIntent(session.ID)
	if !ok {
		return false
	}
	return d.sessionConversationSurvives(session, intent)
}

func (d *Daemon) sessionConversationSurvives(session *protocol.Session, intent store.LaunchIntent) bool {
	if string(session.Agent) == protocol.AgentShellValue {
		return true
	}
	driver := agentdriver.Get(string(session.Agent))
	resumeID := agentdriver.ResolveSpawnResumeSessionID(driver, session.ID, "", d.store.GetResumeSessionID(session.ID))
	if agentdriver.ResumeAvailable(driver, resumeID) {
		return true
	}
	if hostSessionStateDirHoldsConversation(session.ID) {
		return true
	}
	if strings.TrimSpace(intent.InitialPrompt) != "" {
		return true
	}
	if resumeFile := strings.TrimSpace(intent.ResumeConversationFile); resumeFile != "" {
		if _, err := os.Stat(resumeFile); err == nil {
			return true
		}
	}
	return d.store.GetAgentDriverRun(session.ID).RunID != "" ||
		strings.TrimSpace(d.store.GetAgentMetadata(session.ID)) != ""
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
		d.restoreTranscriptWatchers()
		d.reconcileWorkspaceLayoutsWithPTYBackend(context.Background())
		d.reseedWorkspaceStatuses()
		return
	}

	removedSessions := d.pruneSessionsWithoutPTY(recoveryStartedAt)
	if removedSessions > 0 {
		d.logf("pruned %d stale sessions without live PTY on startup", removedSessions)
		d.addWarning(
			warnStaleSessionsPruned,
			fmt.Sprintf("Removed %d stale sessions from a previous daemon run because no live PTY was found.", removedSessions),
		)
	}
	d.reconcileWorkspaceLayoutsWithPTYBackend(context.Background())
	d.restoreTranscriptWatchers()
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
	return d.reconcileSessionsWithWorkerBackendState(ctx, allowIdleDemotion, allowIdleDemotion, demotionCutoff)
}

func (d *Daemon) reconcileSessionsWithWorkerBackendState(ctx context.Context, allowIdleDemotion, allowTombstoneCleanup bool, demotionCutoff time.Time) workerReconcileReport {
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
		intentionalClose, intentErr := d.store.SessionCloseIntentionalChecked(sessionID)
		if intentErr != nil {
			d.logf("worker reconciliation skipped session %s: %v", sessionID, intentErr)
			report.LivenessUnknown++
			continue
		}
		if intentionalClose {
			teardown := d.resumeSessionTeardown(sessionID)
			if teardown != nil {
				d.terminateSessionAsync(sessionID, syscall.SIGTERM, teardown)
			}
			if existing != nil {
				report.Reaped++
				report.markChanged(sessionID)
			}
			continue
		}
		// A runtime that outlived its close, its teardown tombstone already swept.
		// The ledger row is the verdict, so stop it rather than rebuild a session.
		if existing == nil && d.store.SessionClosed(sessionID) {
			d.logf("worker reconciliation stopped runtime %s: its session is closed", sessionID)
			d.terminateSession(sessionID, syscall.SIGTERM)
			continue
		}

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

			state, ok := sessionStateFromRecoveredInfo(info)
			if !ok {
				state = protocol.SessionStateLaunching
			}

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
			report.markChanged(sessionID)
			continue
		}

		d.store.Touch(sessionID)
		d.store.ClearSessionIntentionalClose(sessionID)
		d.reviveCrashedTicketsForSession(sessionID)
		if existing.State == protocol.SessionStateScheduled {
			continue
		}
		if haveInfo {
			if run := d.store.GetAgentDriverRun(sessionID); run.RunID != "" &&
				(d.pluginDriverReportsState(existing.Agent) ||
					existing.State == protocol.SessionStateWaitingInput ||
					existing.State == protocol.SessionStatePendingApproval) {
				continue
			}
			d.seedRecoveredEvidence(sessionID, existing, info)
			nextState, ok := sessionStateFromRecoveredInfo(info)
			if !ok && !resolverOwnedStates[existing.State] {
				nextState, ok = protocol.SessionStateLaunching, true
			}
			if ok && existing.State != nextState {
				d.applyState(sessionStateChange{
					sessionID: sessionID,
					state:     string(nextState),
					cause:     startupRecovery{},
				})
				report.StateUpdated++
				report.markChanged(sessionID)
			}
			continue
		}
	}
	if allowTombstoneCleanup {
		for _, sessionID := range d.store.SessionTeardownIntentIDs() {
			if _, live := liveIDs[sessionID]; live {
				continue
			}
			if d.sessionTeardownInFlight(sessionID) {
				continue
			}
			existing := d.store.Get(sessionID)
			teardown := d.resumeSessionTeardown(sessionID)
			if teardown != nil {
				if !d.notifyPreparedPluginDriverSessionClosed(sessionID, teardown.driverRun, syscall.SIGTERM) {
					continue
				}
				d.store.ClearSessionIntentionalClose(sessionID)
			}
			if existing != nil {
				report.Reaped++
				report.markChanged(sessionID)
			}
		}
	}

	for _, session := range d.store.List("") {
		if _, ok := liveIDs[session.ID]; ok {
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
		d.releaseExitedCrewBinding(session.ID)
		if d.canReviveSession(session) {
			if session.State == protocol.SessionStateRecoverable {
				continue
			}
			d.applyState(sessionStateChange{
				sessionID: session.ID,
				state:     string(protocol.SessionStateRecoverable),
				cause:     startupRecovery{},
			})
			report.StateUpdated++
			report.MarkedRecoverable++
			report.markChanged(session.ID)
		} else {
			d.removeReapedSession(session.ID)
			report.Reaped++
			report.markChanged(session.ID)
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

		reconcile := d.reconcileSessionsWithWorkerBackendState(context.Background(), true, fullyRecovered, recoveryStartedAt)
		d.publishSessionsReconciled(reconcile)
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

// Startup recovery dates a session by state_updated_at and runs concurrently with the
// socket: an unstamped row reads as a leftover of a previous run and is reaped.
func stampSessionTimestamps(session *protocol.Session, now string) {
	if strings.TrimSpace(session.StateSince) == "" {
		session.StateSince = now
	}
	if strings.TrimSpace(session.StateUpdatedAt) == "" {
		session.StateUpdatedAt = now
	}
	if strings.TrimSpace(session.LastSeen) == "" {
		session.LastSeen = now
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

func sessionStateFromRecoveredInfo(info ptybackend.SessionInfo) (protocol.SessionState, bool) {
	if !info.Running {
		return protocol.SessionStateIdle, true
	}
	agent := normalizeStoredSessionAgent(info.Agent, protocol.SessionAgentCodex)
	return agentdriver.RecoveredRunningSessionState(agentdriver.Get(string(agent)), info.State)
}

func (d *Daemon) Stop() {
	d.log("daemon stopping")
	close(d.done)
	d.sessionInputs().stopRetries()
	d.stopNotebookWatcher()
	d.stopFsWatchers()
	if runner := d.jobQueueRef(); runner != nil {
		runner.Stop()
	}
	d.stopEventBus()
	if d.hubManager != nil {
		d.hubManager.Stop()
	}
	d.stopInstalledPlugins()
	d.stopAppRuntime()
	d.stopAllTranscriptWatchers()
	d.stopNudgeCountdowns()
	d.stopAgentMailboxDoorbells()
	d.pluginDriverSilence().stop()
	d.stopAutoSettleTimers()
	if d.ptyBackend != nil {
		_ = d.ptyBackend.Shutdown(context.Background())
	}
	d.ensureHostSessions().Shutdown()
	if d.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		d.httpServer.Shutdown(ctx)
	}
	if d.httpListener != nil {
		d.httpListener.Close()
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

func (d *Daemon) handlePTYExit(info ptybackend.ExitInfo) bool {
	// Skip ALL exit processing: a session_exited here drops the just-respawned
	// session to a dead pane.
	if d.consumeReloading(info.ID) {
		d.logf("suppressing exit for reloading session %s (runtime replaced in place)", info.ID)
		return false
	}
	if d.queueExitDuringPluginLaunch(info) {
		return false
	}
	if d.supersededExitDuringPluginLaunch(info) {
		if activeRun := d.store.GetAgentDriverRun(info.ID); activeRun.RunID == info.LifecycleID {
			d.closePluginDriverSession(info.ID, "exited", &info.ExitCode, info.Signal)
		}
		return false
	}
	if info.LifecycleID != "" {
		activeRun := d.store.GetAgentDriverRun(info.ID)
		if activeRun.RunID != "" && activeRun.RunID != info.LifecycleID {
			d.logf("ignoring stale plugin PTY exit: session=%s exited_run=%s active_run=%s", info.ID, info.LifecycleID, activeRun.RunID)
			return false
		}
	}
	d.sessionInputs().forgetSession(info.ID)
	d.stopTranscriptWatcher(info.ID)
	d.closePluginDriverSession(info.ID, "exited", &info.ExitCode, info.Signal)
	d.captureExitScreen(info)
	d.noteLaunchExited(info)

	if d.ptyBackend != nil {
		if err := d.removePTYSession(info.ID); err != nil {
			d.logf("pty backend remove on exit failed for %s: %v", info.ID, err)
		}
	}

	d.recordProcessEvidence(info.ID, true)
	if session := d.store.Get(info.ID); session != nil {
		d.reconcileTicketsOnSessionEnd(info.ID, string(session.State))
	}
	d.releaseExitedCrewBinding(info.ID)

	d.publishFact(FactSessionPTYExited, info.ID, ptyExit{
		ExitCode: info.ExitCode,
		Signal:   info.Signal,
	})
	return true
}

type ptyExit struct {
	ExitCode int    `json:"exit_code"`
	Signal   string `json:"signal,omitempty"`
}

func (d *Daemon) projectSessionPTYExited(ev bus.Event) {
	exit, ok := decodeFact[ptyExit](d, ev)
	if !ok {
		return
	}
	event := &protocol.WebSocketEvent{
		Event:    protocol.EventSessionExited,
		ID:       protocol.Ptr(ev.Subject),
		ExitCode: protocol.Ptr(exit.ExitCode),
	}
	if exit.Signal != "" {
		event.Signal = protocol.Ptr(exit.Signal)
	}
	d.wsHub.Broadcast(event)
}

func (d *Daemon) removePTYSession(sessionID string) error {
	if d.ptyBackend == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := d.ptyBackend.Remove(ctx, sessionID)
	if err == nil || errors.Is(err, pty.ErrSessionNotFound) || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	go func() {
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

type sessionTeardown struct {
	session   *protocol.Session
	driverRun store.AgentDriverReportCursor
}

func (d *Daemon) terminateSession(sessionID string, sig syscall.Signal) {
	if err := d.terminateSessionChecked(sessionID, sig); err != nil {
		d.logf("session teardown failed for %s: requested=%s error=%v", sessionID, signalName(sig), err)
		d.markForcedStopClassification(sessionID)
		if d.store != nil {
			if markErr := d.store.MarkSessionIntentionalClose(sessionID, time.Now()); markErr != nil {
				d.logf("session teardown intent failed for %s: %v", sessionID, markErr)
			}
		}
		d.stopTranscriptWatcher(sessionID)
		if d.ptyBackend != nil {
			_ = d.ptyBackend.Remove(context.Background(), sessionID)
		}
	}
}

func (d *Daemon) markSessionTerminationIntent(sessionID string) error {
	d.markForcedStopClassification(sessionID)
	if d.store != nil {
		if err := d.store.MarkSessionIntentionalClose(sessionID, time.Now()); err != nil {
			return err
		}
	}
	return nil
}

func (d *Daemon) terminateSessionChecked(sessionID string, sig syscall.Signal) error {
	// Durable close mark BEFORE the kill: ticket reconcile can run after the
	// in-memory mark expires and would crash-stamp a user close.
	if err := d.markSessionTerminationIntent(sessionID); err != nil {
		return err
	}
	if err := d.terminateSessionRuntimeChecked(sessionID, sig); err != nil {
		d.clearForcedStopClassification(sessionID)
		if d.store != nil {
			d.store.ClearSessionIntentionalClose(sessionID)
		}
		return err
	}
	d.closePluginDriverSession(sessionID, "killed", nil, signalName(sig))
	return nil
}

func (d *Daemon) terminateSessionRuntimeChecked(sessionID string, sig syscall.Signal) error {
	if d.isHostSession(sessionID) {
		if err := d.ensureHostSessions().Kill(sessionID); err != nil && !errors.Is(err, hostsession.ErrNotFound) {
			return err
		}
		d.stopTranscriptWatcher(sessionID)
		return nil
	}

	if d.ptyBackend == nil {
		d.stopTranscriptWatcher(sessionID)
		return nil
	}
	err := d.ptyBackend.Kill(context.Background(), sessionID, sig)
	if err != nil && !errors.Is(err, pty.ErrSessionNotFound) {
		return err
	}
	d.stopTranscriptWatcher(sessionID)
	if err := d.ptyBackend.Remove(context.Background(), sessionID); err != nil && !errors.Is(err, pty.ErrSessionNotFound) && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Rolls a spawn back: the delegation never ran, so there is nothing to record.
func (d *Daemon) unregisterSession(sessionID string, sig syscall.Signal) *protocol.Session {
	session := d.store.Get(sessionID)
	if session == nil && d.hubManager != nil {
		session = d.hubManager.RemoteSession(sessionID)
	}
	if session != nil {
		if _, err := d.captureGardenSessionSnapshot(session); err != nil {
			d.logf("garden: preserving execution %s before session removal: %v", sessionID, err)
		}
	}
	d.terminateSession(sessionID, sig)
	d.removeReapedSession(sessionID)
	return session
}

func (d *Daemon) prepareSessionTeardown(sessionID string) (*sessionTeardown, error) {
	session := d.store.Get(sessionID)
	if session == nil && d.hubManager != nil {
		session = d.hubManager.RemoteSession(sessionID)
	}
	if session != nil {
		if _, err := d.captureGardenSessionSnapshot(session); err != nil {
			d.logf("garden: preserving execution %s before session removal: %v", sessionID, err)
		}
	}
	d.markForcedStopClassification(sessionID)
	if d.prepareSessionTeardownHook != nil {
		if err := d.prepareSessionTeardownHook(sessionID); err != nil {
			d.clearForcedStopClassification(sessionID)
			return nil, err
		}
	}
	driverRun, err := d.store.PrepareSessionTeardown(sessionID, time.Now())
	if err != nil {
		d.clearForcedStopClassification(sessionID)
		return nil, err
	}
	return &sessionTeardown{session: session, driverRun: driverRun}, nil
}

func (d *Daemon) commitSessionUnregister(sessionID string, closed store.SessionClose) {
	d.closeSession(sessionID, closed)
}

func (d *Daemon) cancelSessionTeardown(sessionID string) {
	d.clearForcedStopClassification(sessionID)
	if err := d.store.CancelSessionTeardown(sessionID); err != nil {
		d.logf("cancel session teardown failed for %s: %v", sessionID, err)
	}
}

func (d *Daemon) resumeSessionTeardown(sessionID string) *sessionTeardown {
	driverRun, found, err := d.store.PrepareExistingSessionTeardown(sessionID, time.Now())
	if err != nil {
		d.logf("session teardown recovery failed for %s: %v", sessionID, err)
		return nil
	}
	if !found {
		return nil
	}
	session := d.store.Get(sessionID)
	d.closeSession(sessionID, store.SessionClose{By: store.SessionClosedByUser})
	return &sessionTeardown{session: session, driverRun: driverRun}
}

func (d *Daemon) notifyPreparedPluginDriverSessionClosed(sessionID string, run store.AgentDriverReportCursor, sig syscall.Signal) bool {
	if run.RunID == "" {
		return true
	}
	claimed, err := d.store.ClaimSessionTeardownDriverRun(sessionID, run.RunID)
	if err != nil {
		d.logf("plugin session close claim failed: session=%s run=%s error=%v", sessionID, run.RunID, err)
		return false
	}
	if claimed {
		d.notifyPluginDriverSessionClosed(run.PluginName, sessionID, run.RunID, "killed", nil, signalName(sig))
	}
	return true
}

func (d *Daemon) sessionTeardownInFlight(sessionID string) bool {
	d.teardownMu.Lock()
	defer d.teardownMu.Unlock()
	return d.tearingDown[sessionID] != nil
}

func (d *Daemon) waitForSessionTeardown(sessionID string) {
	d.teardownMu.Lock()
	done := d.tearingDown[sessionID]
	d.teardownMu.Unlock()
	if done != nil {
		<-done
	}
}

func (d *Daemon) terminateSessionAsync(sessionID string, sig syscall.Signal, teardown *sessionTeardown) <-chan struct{} {
	d.teardownMu.Lock()
	if d.tearingDown == nil {
		d.tearingDown = make(map[string]chan struct{})
	}
	if done := d.tearingDown[sessionID]; done != nil {
		d.teardownMu.Unlock()
		return done
	}
	done := make(chan struct{})
	d.tearingDown[sessionID] = done
	d.teardownMu.Unlock()

	go func() {
		defer func() {
			d.teardownMu.Lock()
			delete(d.tearingDown, sessionID)
			d.teardownMu.Unlock()
			close(done)
		}()
		if teardown == nil {
			d.terminateSession(sessionID, sig)
			return
		}
		if err := d.terminateSessionRuntimeChecked(sessionID, sig); err != nil {
			d.logf("session teardown failed for %s: requested=%s error=%v", sessionID, signalName(sig), err)
			_ = d.removePTYSession(sessionID)
			return
		}
		d.notifyPreparedPluginDriverSessionClosed(sessionID, teardown.driverRun, sig)
	}()
	return done
}

// closeSession keeps the row and its session-owned tables; the store stops
// answering List and Get for it.
func (d *Daemon) closeSession(sessionID string, closed store.SessionClose) {
	d.recordSessionClose(sessionID, func() (bool, error) {
		return d.store.CloseSession(sessionID, closed, time.Now())
	})
}

// A rollback puts back the close it lifted, closed_at included: a resume that
// failed must leave the historical record exactly as it found it.
func (d *Daemon) restoreSessionClose(sessionID string, closed store.SessionCloseRecord) {
	d.recordSessionClose(sessionID, func() (bool, error) {
		return d.store.RestoreSessionClose(sessionID, closed)
	})
}

func (d *Daemon) recordSessionClose(sessionID string, commit func() (bool, error)) {
	if session := d.store.Get(sessionID); session != nil {
		if _, err := d.captureGardenSessionSnapshot(session); err != nil {
			d.logf("garden: preserving execution %s before closing it: %v", sessionID, err)
		}
	}
	d.forgetSessionRuntime(sessionID)
	recorded, err := commit()
	if err != nil {
		d.logf("close session %s: %v", sessionID, err)
	}
	d.forgetSessionTrace(sessionID)
	if recorded {
		d.publishFact(FactSessionClosed, sessionID, d.store.SessionLedgerEntry(sessionID))
	}
	d.clearChiefOfStaffIfSession(sessionID)
	d.releaseCrewBindingIfSession(sessionID)
	d.crewMemo().forget(sessionID)
	if d.hubManager != nil {
		d.hubManager.ForgetSession(sessionID)
	}
	d.clearClassifiedTurn(sessionID)
	d.clearClassifyingTurn(sessionID)
}

// removeReapedSession deletes the row; reaping is for what a close would not record.
func (d *Daemon) removeReapedSession(sessionID string) {
	// A crashed session can leave a checkout newer than the branch monitor's cache.
	if session := d.store.Get(sessionID); session != nil {
		if _, err := d.captureGardenSessionExecution(session); err != nil {
			d.logf("garden: preserving execution %s before reaping: %v", sessionID, err)
		}
	}
	d.forgetSessionRuntime(sessionID)
	d.store.Remove(sessionID)
	d.forgetSessionTrace(sessionID)
	d.clearChiefOfStaffIfSession(sessionID)
	d.releaseCrewBindingIfSession(sessionID)
	d.dissociateSessionFromWorkspace(sessionID)
	d.removeWorkspaceLayoutPaneForSession(sessionID)
}

func (d *Daemon) forgetSessionRuntime(sessionID string) {
	d.stopTranscriptWatcher(sessionID)
	if session := d.store.Get(sessionID); session != nil {
		d.reconcileTicketsOnSessionEnd(sessionID, string(session.State))
	}
	d.clearNudgeState(sessionID)
	d.forgetPostInitialPrompt(sessionID)
	d.forgetAgentMailboxDoorbell(sessionID)
	d.forgetSessionTitleInitialPrompt(sessionID)
	d.clearAutoSettleState(sessionID)
	d.clearSnoozeState(sessionID)
	d.sessionInputs().forgetSession(sessionID)
	d.forgetPluginDriverSilenceWatch(sessionID)
}

// Called after the store stopped answering for the session, not before: an
// observation racing this would rebuild the ring for an id nothing cleans up again.
func (d *Daemon) forgetSessionTrace(sessionID string) {
	d.forgetStateTrace(sessionID)
	d.evidenceTable().forget(sessionID)
	d.stateReasons().forget(sessionID)
	d.dwellGate().clear(sessionID)
}

func (d *Daemon) handlePTYState(sessionID string, obs pty.Observation) {
	state := obs.Claim
	origin := stateOrigin{source: string(obs.Source), detail: obs.Detail, observedAt: obs.At}
	evidenceChanged := d.recordPTYEvidence(sessionID, obs)
	if !obs.Source.ClaimsProtocolState() {
		if evidenceChanged {
			d.traceStateEvidence(sessionID, origin, state)
		}
		return
	}
	session := d.store.Get(sessionID)
	if session == nil {
		d.traceStateVeto(sessionID, origin, state, "session_not_found")
		return
	}
	driverRun := d.store.GetAgentDriverRun(sessionID)
	if driverRun.RunID != "" && d.pluginDriverReportsState(session.Agent) {
		d.traceStateVeto(sessionID, origin, state, "plugin_driver_owns_state")
		return
	}
	if session.State != protocol.SessionStateLaunching {
		reason := "resolver_owned"
		if driverRun.RunID != "" {
			// The run record outlives the driver process, so on a restart this replay
			// routinely beats driver.register. The claim is vetoed either way.
			reason = "plugin_driver_not_registered"
		}
		d.traceStateVeto(sessionID, origin, state, reason)
		return
	}
	agent := session.Agent
	d.logf(
		"pty state update: session=%s agent=%s state=%s source=%s detail=%q observed_at=%s",
		sessionID, agent, state, obs.Source, obs.Detail, obs.At.Format(time.RFC3339Nano),
	)
	d.applyState(sessionStateChange{
		sessionID: sessionID,
		state:     state,
		cause:     liveSignal{},
		origin:    origin,
	})
}

func setNoStoreHeaders(header http.Header) {
	header.Set("Cache-Control", "no-store, max-age=0")
	header.Set("Pragma", "no-cache")
	header.Set("Expires", "0")
}

func (d *Daemon) initHTTPServer() {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", d.handleWS)
	mux.HandleFunc("/health", d.handleHealth)
	mux.HandleFunc("/agents", d.handleAgents)
	mux.HandleFunc(appBundleRoutePrefix, d.handleAppBundle)
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, _ *http.Request) {
		setNoStoreHeaders(w.Header())
		w.WriteHeader(http.StatusNoContent)
	})
	d.httpHandler = mux

	d.httpServer = &http.Server{
		Addr:        net.JoinHostPort(config.WSBindAddress(), config.WSPort()),
		Handler:     d.httpHandler,
		ConnContext: withRawConn,
	}
}

// The bind error is fatal: a daemon owning the unix socket but not the
// WebSocket port serves two different daemons to the CLI and the app.
func (d *Daemon) listenHTTP() error {
	addr := d.httpServer.Addr
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf(
			"refusing to start: cannot bind the WebSocket address %s for profile %q: %w. "+
				"attn derives that port from the profile name, so the usual owner is a daemon for the same profile running somewhere else — "+
				"notably on a VM whose listener OrbStack forwards onto host localhost while the host port is free. "+
				"Starting anyway would leave this daemon split-brained: the app routes by WebSocket port and would attach to the foreign listener, "+
				"the CLI routes by the unix socket and would talk to this process, and every command sent from the app would silently miss these sessions. "+
				"Free %s (stop whatever holds it, including a forwarding VM) or run this daemon under another profile with ATTN_PROFILE",
			addr, config.ProfileLabel(), err, addr,
		)
	}
	d.httpListener = listener
	return nil
}

func (d *Daemon) runHTTPServer() {
	d.logf("WebSocket server starting on ws://%s/ws", d.httpServer.Addr)
	if err := d.httpServer.Serve(d.httpListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		d.logf("HTTP server error: %v", err)
	}
}

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

func (d *Daemon) diagStats() diag.Stats {
	stats := diag.Stats{PtyBackend: d.ptyBackendMode()}
	if d.ptyBackend == nil {
		stats.PtyBackend = "embedded"
		return stats
	}
	ctx := context.Background()
	stats.Sessions = len(d.ptyBackend.SessionIDs(ctx))
	if wp, ok := d.ptyBackend.(ptybackend.WorkerProcessProvider); ok {
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
			d.recoverAutomations()
		}
	}
}

// ghVersionWarning maps a RequireGHVersion failure to the banner the app shows.
// Warning keys are part of the daemon/app contract; only the text varies.
func ghVersionWarning(err error) (string, string) {
	if errors.Is(err, exec.ErrNotFound) {
		return warnGHNotInstalled, "GitHub CLI not installed. PR monitoring disabled. " + github.InstallHint()
	}
	var tooOld *github.VersionTooOldError
	if errors.As(err, &tooOld) {
		return warnGHVersionTooOld, "GitHub CLI v" + tooOld.Have + " needs upgrade to v" + tooOld.Want + "+ for PR monitoring. " + github.UpgradeHint()
	}
	return warnGHVersionTooOld, "GitHub CLI needs upgrade to v2.81.0+ for PR monitoring. " + github.UpgradeHint()
}

func (d *Daemon) refreshGitHubHosts() error {
	if d.ghRegistry == nil {
		d.ghRegistry = github.NewClientRegistry()
	}
	hostsBefore := d.gitHubHosts()

	mockURL := strings.TrimSpace(os.Getenv("ATTN_MOCK_GH_URL"))
	if mockURL != "" {
		if err := d.registerMockClient(mockURL); err != nil {
			d.logf("Mock GitHub client not available: %v", err)
		}
		d.broadcastGitHubHosts(hostsBefore)
		return nil
	}

	if err := github.RequireGHVersion("2.81.0"); err != nil {
		code, message := ghVersionWarning(err)
		d.logf("gh CLI unavailable (need 2.81.0+): %v", err)
		d.addWarning(code, message)
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

	d.broadcastGitHubHosts(hostsBefore)
	return nil
}

func (d *Daemon) gitHubHosts() []string {
	if d.ghRegistry == nil {
		return nil
	}
	return d.ghRegistry.Hosts()
}

func (d *Daemon) broadcastGitHubHosts(before []string) {
	previous := make(map[string]struct{}, len(before))
	for _, host := range before {
		previous[host] = struct{}{}
	}
	current := d.gitHubHosts()
	currentSet := make(map[string]struct{}, len(current))
	for _, host := range current {
		currentSet[host] = struct{}{}
	}

	d.coalesceSnapshots(func() {
		for _, host := range current {
			if _, had := previous[host]; !had {
				d.publishFact(FactGitHubHostAdded, host, nil)
			}
		}
		for _, host := range before {
			if _, still := currentSet[host]; !still {
				d.publishFact(FactGitHubHostRemoved, host, nil)
			}
		}
	})
}

func (d *Daemon) projectGitHubHostsUpdated() {
	d.projectSnapshot(snapshotGHHosts, func() {
		d.wsHub.BroadcastValue(d.gitHubHostsUpdatedMessage())
	})
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

func (d *Daemon) acquirePIDLock() error {
	f, err := os.OpenFile(d.pidPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return fmt.Errorf("open PID file: %w", err)
	}

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

	f.Truncate(0)
	f.Seek(0, 0)
	pid := os.Getpid()
	if _, err := f.WriteString(strconv.Itoa(pid)); err != nil {
		f.Close()
		return fmt.Errorf("write PID: %w", err)
	}
	f.Sync()

	d.pidFile = f
	d.logf("Acquired PID lock (PID %d, file %s)", pid, d.pidPath)

	return nil
}

// Deliberately leaves the PID file on disk: unlinking would let a concurrent flock
// holder keep an orphaned inode while O_CREATE makes another at the same path.
func (d *Daemon) releasePIDLock() {
	if d.pidFile != nil {
		syscall.Flock(int(d.pidFile.Fd()), syscall.LOCK_UN)
		d.pidFile.Close()
		d.pidFile = nil
	}
}

func (d *Daemon) handleConnection(conn net.Conn) {
	defer conn.Close()

	// Legacy hook traffic carries no trailing newline: read exactly one top-level JSON
	// object, leaving pipelined bytes buffered for the line-framed plugin loop.
	reader := bufio.NewReader(conn)
	data, err := readInitialSocketFrame(reader, maxInitialSocketFrameBytes)
	if err != nil {
		if !errors.Is(err, io.EOF) {
			d.sendError(conn, err.Error())
		}
		return
	}

	// Sniff the app runtime first: the plugin parser claims every JSON-RPC frame.
	if runtimeHelloID, runtimeParams, runtimeMode, err := parseAppRuntimeHello(data); runtimeMode {
		if err != nil {
			_ = json.NewEncoder(conn).Encode(jsonRPCFailure(runtimeHelloID, jsonRPCInvalidRequest, err.Error()))
			return
		}
		d.handleAppRuntimeConnection(conn, reader, runtimeHelloID, runtimeParams)
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
	case protocol.CmdRegister: // wire: register
		d.handleRegister(conn, msg.(*protocol.RegisterMessage))
	case protocol.CmdDelegate: // wire: delegate
		d.handleDelegate(conn, msg.(*protocol.DelegateMessage))
	// wire: automation_apply, automation_validate, automation_definitions_get, automation_definition_get, automation_run,
	// automation_runs_get, automation_set_enabled, automation_delete, automation_cleanup
	case protocol.CmdAutomationApply, protocol.CmdAutomationValidate, protocol.CmdAutomationDefinitionsGet, protocol.CmdAutomationDefinitionGet, protocol.CmdAutomationRun, protocol.CmdAutomationRunsGet, protocol.CmdAutomationSetEnabled, protocol.CmdAutomationDelete, protocol.CmdAutomationCleanup:
		d.handleAutomationCommand(conn, cmd, msg)
	case protocol.CmdDelegateStatus: // wire: delegate_status
		d.handleDelegateStatus(conn, msg.(*protocol.DelegateStatusMessage))
	case protocol.CmdSetTicketStatus: // wire: set_ticket_status
		d.handleSetTicketStatus(conn, msg.(*protocol.SetTicketStatusMessage))
	case protocol.CmdTicketInbox: // wire: ticket_inbox
		d.handleTicketInbox(conn, msg.(*protocol.TicketInboxMessage))
	case protocol.CmdTicketList: // wire: ticket_list
		d.handleTicketList(conn, msg.(*protocol.TicketListMessage))
	case protocol.CmdTicketShow: // wire: ticket_show
		d.handleTicketShow(conn, msg.(*protocol.TicketShowMessage))
	case protocol.CmdActivityStatus: // wire: activity_status
		d.handleActivityStatus(conn, msg.(*protocol.ActivityStatusMessage))
	case protocol.CmdClearSessionActivity: // wire: clear_session_activity
		d.handleClearSessionActivity(conn, msg.(*protocol.ClearSessionActivityMessage))
	case protocol.CmdTicketSubscribe: // wire: ticket_subscribe
		d.handleTicketSubscribe(conn, msg.(*protocol.TicketSubscribeMessage))
	case protocol.CmdTicketUnsubscribe: // wire: ticket_unsubscribe
		d.handleTicketUnsubscribe(conn, msg.(*protocol.TicketUnsubscribeMessage))
	case protocol.CmdTicketAttach: // wire: ticket_attach
		d.handleTicketAttach(conn, msg.(*protocol.TicketAttachMessage))
	case protocol.CmdDocDefine: // wire: doc_define
		d.handleDocDefine(conn, msg.(*protocol.DocDefineMessage))
	case protocol.CmdDocUndefine: // wire: doc_undefine
		d.handleDocUndefine(conn, msg.(*protocol.DocUndefineMessage))
	case protocol.CmdDocCollections: // wire: doc_collections
		d.handleDocCollections(conn, msg.(*protocol.DocCollectionsMessage))
	case protocol.CmdDocPut: // wire: doc_put
		d.handleDocPut(conn, msg.(*protocol.DocPutMessage))
	case protocol.CmdDocGet: // wire: doc_get
		d.handleDocGet(conn, msg.(*protocol.DocGetMessage))
	case protocol.CmdDocDelete: // wire: doc_delete
		d.handleDocDelete(conn, msg.(*protocol.DocDeleteMessage))
	case protocol.CmdDocQuery: // wire: doc_query
		d.handleDocQuery(conn, msg.(*protocol.DocQueryMessage))
	case protocol.CmdDocCount: // wire: doc_count
		d.handleDocCount(conn, msg.(*protocol.DocCountMessage))
	case protocol.CmdDocSubscribe: // wire: doc_subscribe
		d.handleDocSubscribe(conn, msg.(*protocol.DocSubscribeMessage))
	case protocol.CmdAppList: // wire: app_list
		d.handleAppList(conn, msg.(*protocol.AppListMessage))
	case protocol.CmdAppStatus: // wire: app_status
		d.handleAppStatus(conn, msg.(*protocol.AppStatusMessage))
	case protocol.CmdAppSetEnabled: // wire: app_set_enabled
		d.handleAppSetEnabled(conn, msg.(*protocol.AppSetEnabledMessage))
	case protocol.CmdAppRemove: // wire: app_remove
		d.handleAppRemove(conn, msg.(*protocol.AppRemoveMessage))
	case protocol.CmdAppApply: // wire: app_apply
		d.handleAppApply(conn, msg.(*protocol.AppApplyMessage))
	case protocol.CmdAppRollback: // wire: app_rollback
		d.handleAppRollback(conn, msg.(*protocol.AppRollbackMessage))
	case protocol.CmdAppLogs: // wire: app_logs
		d.handleAppLogs(conn, msg.(*protocol.AppLogsMessage))
	case protocol.CmdAppRuntimeStatus: // wire: app_runtime_status
		d.handleAppRuntimeStatus(conn, msg.(*protocol.AppRuntimeStatusMessage))
	case protocol.CmdAppRuntimeRestart: // wire: app_runtime_restart
		d.handleAppRuntimeRestart(conn, msg.(*protocol.AppRuntimeRestartMessage))
	case protocol.CmdAppWatch: // wire: app_watch
		d.handleAppWatch(conn, msg.(*protocol.AppWatchMessage))
	case protocol.CmdAutoModeShow: // wire: automode_show
		d.handleAutoModeShow(conn, msg.(*protocol.AutoModeShowMessage))
	case protocol.CmdAutoModeEnvSlot: // wire: automode_env_slot
		d.handleAutoModeEnvSlot(conn, msg.(*protocol.AutoModeEnvSlotMessage))
	case protocol.CmdAutoModeEnvNotes: // wire: automode_env_notes
		d.handleAutoModeEnvNotes(conn, msg.(*protocol.AutoModeEnvNotesMessage))
	case protocol.CmdAutoModePropose: // wire: automode_propose
		d.handleAutoModePropose(conn, msg.(*protocol.AutoModeProposeMessage))
	case protocol.CmdAutoModeDenials: // wire: automode_denials
		d.handleAutoModeDenials(conn, msg.(*protocol.AutoModeDenialsMessage))
	case protocol.CmdTicketCreate: // wire: ticket_create
		d.handleTicketCreate(conn, msg.(*protocol.TicketCreateMessage))
	case protocol.CmdTicketComment: // wire: ticket_comment
		d.handleTicketComment(conn, msg.(*protocol.TicketCommentMessage))
	case protocol.CmdPresentOpen: // wire: present_open
		d.handlePresentOpen(conn, msg.(*protocol.PresentOpenMessage))
	case protocol.CmdPresentFeedback: // wire: present_feedback
		d.handlePresentFeedback(conn, msg.(*protocol.PresentFeedbackMessage))
	case protocol.CmdTicketTake: // wire: ticket_take
		d.handleTicketTake(conn, msg.(*protocol.TicketTakeMessage))
	case protocol.CmdWorkspaceContextCheckout: // wire: workspace_context_checkout
		d.handleWorkspaceContextCheckout(conn, msg.(*protocol.WorkspaceContextCheckoutMessage))
	case protocol.CmdWorkspaceContextUpdate: // wire: workspace_context_update
		d.handleWorkspaceContextUpdate(conn, msg.(*protocol.WorkspaceContextUpdateMessage))
	case protocol.CmdWorkspaceContextStatus: // wire: workspace_context_status
		d.handleWorkspaceContextStatus(conn, msg.(*protocol.WorkspaceContextStatusMessage))
	case protocol.CmdWorkspaceContextList: // wire: workspace_context_list
		d.handleWorkspaceContextList(conn)
	case protocol.CmdWorkspaceContextCompact: // wire: workspace_context_compact
		d.handleWorkspaceContextCompact(conn, msg.(*protocol.WorkspaceContextCompactMessage))
	case protocol.CmdWorkspaceContextRollback: // wire: workspace_context_rollback
		d.handleWorkspaceContextRollback(conn, msg.(*protocol.WorkspaceContextRollbackMessage))
	case protocol.CmdNotebookGuide: // wire: notebook_guide
		d.handleNotebookGuide(conn, msg.(*protocol.NotebookGuideMessage))
	case protocol.CmdJournalAppend: // wire: journal_append
		d.handleJournalAppend(conn, msg.(*protocol.JournalAppendMessage))
	case protocol.CmdUnregister: // wire: unregister
		d.handleUnregister(conn, msg.(*protocol.UnregisterMessage))
	case protocol.CmdState: // wire: state
		d.handleState(conn, msg.(*protocol.StateMessage))
	case protocol.CmdHookNotification: // wire: hook_notification
		d.handleHookNotification(conn, msg.(*protocol.HookNotificationMessage))
	case protocol.CmdHookStopFailure: // wire: hook_stop_failure
		d.handleHookStopFailure(conn, msg.(*protocol.HookStopFailureMessage))
	case protocol.CmdHookCompaction: // wire: hook_compaction
		d.handleHookCompaction(conn, msg.(*protocol.HookCompactionMessage))
	case protocol.CmdSetSessionResumeID: // wire: set_session_resume_id
		d.handleObserveAgentConversation(conn, msg.(*protocol.SetSessionResumeIDMessage))
	case protocol.CmdSessionInstructions: // wire: session_instructions
		d.handleSessionInstructions(conn, msg.(*protocol.SessionInstructionsMessage))
	case protocol.CmdSessionTranscript: // wire: session_transcript
		d.handleSessionTranscript(conn, msg.(*protocol.SessionTranscriptMessage))
	case protocol.CmdSessionList: // wire: session_list
		d.handleSessionList(conn, msg.(*protocol.SessionListMessage))
	case protocol.CmdSessionShow: // wire: session_show
		d.handleSessionShow(conn, msg.(*protocol.SessionShowMessage))
	case protocol.CmdStateExplain: // wire: state_explain
		d.handleStateExplain(conn, msg.(*protocol.StateExplainMessage))
	case protocol.CmdAgentPeek: // wire: agent_peek
		d.handleAgentPeek(conn, msg.(*protocol.AgentPeekMessage))

	case protocol.CmdAgentMsg: // wire: agent_msg
		d.handleAgentMsg(conn, msg.(*protocol.AgentMsgMessage))
	case protocol.CmdAgentInbox: // wire: agent_inbox
		d.handleAgentInbox(conn, msg.(*protocol.AgentInboxMessage))
	case protocol.CmdAgentMsgStatus: // wire: agent_msg_status
		d.handleAgentMsgStatus(conn, msg.(*protocol.AgentMsgStatusMessage))
	case protocol.CmdSeedPlant: // wire: seed_plant
		d.handleSeedPlant(conn, msg.(*protocol.SeedPlantMessage))
	case protocol.CmdSeedPlot: // wire: seed_plot
		d.handleSeedPlot(conn, msg.(*protocol.SeedPlotMessage))
	case protocol.CmdSeedList: // wire: seed_list
		d.handleSeedList(conn, msg.(*protocol.SeedListMessage))
	case protocol.CmdSeedShow: // wire: seed_show
		d.handleSeedShow(conn, msg.(*protocol.SeedShowMessage))
	case protocol.CmdSeedArtifactTransfer: // wire: seed_artifact_transfer
		d.handleSeedArtifactTransfer(conn, msg.(*protocol.SeedArtifactTransferMessage))
	case protocol.CmdSeedEdit: // wire: seed_edit
		d.handleSeedEdit(conn, msg.(*protocol.SeedEditMessage))
	case protocol.CmdSeedSetResume: // wire: seed_set_resume
		d.handleSeedSetResume(conn, msg.(*protocol.SeedSetResumeMessage))
	case protocol.CmdSeedTransition: // wire: seed_transition
		d.handleSeedTransition(conn, msg.(*protocol.SeedTransitionMessage))
	case protocol.CmdSeedNote: // wire: seed_note
		d.handleSeedNote(conn, msg.(*protocol.SeedNoteMessage))
	case protocol.CmdSeedNotes: // wire: seed_notes
		d.handleSeedNotes(conn, msg.(*protocol.SeedNotesMessage))
	case protocol.CmdSeedWatch: // wire: seed_watch
		d.handleSeedWatch(conn, msg.(*protocol.SeedWatchMessage))
	case protocol.CmdSeedLink: // wire: seed_link
		d.handleSeedLink(conn, msg.(*protocol.SeedLinkMessage))
	case protocol.CmdSeedReady: // wire: seed_ready
		d.handleSeedReady(conn, msg.(*protocol.SeedReadyMessage))
	case protocol.CmdSeedReviewStart: // wire: seed_review_start
		d.handleSeedReviewStart(conn, msg.(*protocol.SeedReviewStartMessage))
	case protocol.CmdSeedSendToChief: // wire: seed_send_to_chief
		d.handleSeedSendToChief(conn, msg.(*protocol.SeedSendToChiefMessage))
	case protocol.CmdSeedReviewShow: // wire: seed_review_show
		d.handleSeedReviewShow(conn, msg.(*protocol.SeedReviewShowMessage))
	case protocol.CmdSeedReviewCancel: // wire: seed_review_cancel
		d.handleSeedReviewCancel(conn, msg.(*protocol.SeedReviewCancelMessage))
	case protocol.CmdSeedReviewRetry: // wire: seed_review_retry
		d.handleSeedReviewRetry(conn, msg.(*protocol.SeedReviewRetryMessage))
	case protocol.CmdSeedReviewKeep: // wire: seed_review_keep
		d.handleSeedReviewKeep(conn, msg.(*protocol.SeedReviewKeepMessage))
	case protocol.CmdCrewList: // wire: crew_list
		d.handleCrewList(conn, msg.(*protocol.CrewListMessage))
	case protocol.CmdCrewWake: // wire: crew_wake
		d.handleCrewWake(conn, msg.(*protocol.CrewWakeMessage))
	case protocol.CmdCrewSleep: // wire: crew_sleep
		d.handleCrewSleep(conn, msg.(*protocol.CrewSleepMessage))
	case protocol.CmdCrewSet: // wire: crew_set
		d.handleCrewSet(conn, msg.(*protocol.CrewSetMessage))
	case protocol.CmdCrewPrime: // wire: crew_prime
		d.handleCrewPrime(conn, msg.(*protocol.CrewPrimeMessage))
	case protocol.CmdCrewHandoff: // wire: crew_handoff
		d.handleCrewHandoff(conn, msg.(*protocol.CrewHandoffMessage))
	case protocol.CmdStop: // wire: stop
		d.handleStop(conn, msg.(*protocol.StopMessage))
	case protocol.CmdTodos: // wire: todos
		d.handleTodos(conn, msg.(*protocol.TodosMessage))
	case protocol.CmdFilesEdited: // wire: files_edited
		d.handleFilesEdited(conn, msg.(*protocol.FilesEditedMessage))
	case protocol.CmdPullRequestCreated: // wire: pull_request_created
		d.handlePullRequestCreated(conn, msg.(*protocol.PullRequestCreatedMessage))
	case protocol.CmdPullRequestForget: // wire: pull_request_forget
		d.handlePullRequestForget(conn, msg.(*protocol.PullRequestForgetMessage))
	case protocol.CmdWorkflowRunUpsert: // wire: workflow_run_upsert
		d.handleWorkflowRunUpsert(conn, msg.(*protocol.WorkflowRunUpsertMessage))
	case protocol.CmdWorkflowCallUpsert: // wire: workflow_call_upsert
		d.handleWorkflowCallUpsert(conn, msg.(*protocol.WorkflowCallUpsertMessage))
	case protocol.CmdWorkflowRunGet: // wire: workflow_run_get
		d.handleWorkflowRunGet(conn, msg.(*protocol.WorkflowRunGetMessage))
	case protocol.CmdWorkflowRunList: // wire: workflow_run_list
		d.handleWorkflowRunList(conn, msg.(*protocol.WorkflowRunListMessage))
	case protocol.CmdWorkflowRunCancel: // wire: workflow_run_cancel
		d.handleWorkflowRunCancel(conn, msg.(*protocol.WorkflowRunCancelMessage))
	case protocol.CmdQuery: // wire: query
		d.handleQuery(conn, msg.(*protocol.QueryMessage))
	case protocol.CmdHeartbeat: // wire: heartbeat
		d.handleHeartbeat(conn, msg.(*protocol.HeartbeatMessage))
	case protocol.CmdQueryPRs: // wire: query_prs
		d.handleQueryPRs(conn, msg.(*protocol.QueryPRsMessage))
	case protocol.CmdMutePR: // wire: mute_pr
		d.handleMutePR(conn, msg.(*protocol.MutePRMessage))
	case protocol.CmdMuteRepo: // wire: mute_repo
		d.handleMuteRepo(conn, msg.(*protocol.MuteRepoMessage))
	case protocol.CmdMuteWorkspace: // wire: mute_workspace
		if _, errMsg := d.toggleWorkspaceMute(msg.(*protocol.MuteWorkspaceMessage).WorkspaceID); errMsg != "" {
			d.sendError(conn, errMsg)
			return
		}
		d.sendOK(conn)
	case protocol.CmdPinWorkspace: // wire: pin_workspace
		m := msg.(*protocol.PinWorkspaceMessage)
		if _, errMsg := d.setWorkspacePinned(m.WorkspaceID, m.Pinned); errMsg != "" {
			d.sendError(conn, errMsg)
			return
		}
		d.sendOK(conn)
	case protocol.CmdPinSession: // wire: pin_session
		m := msg.(*protocol.PinSessionMessage)
		if errMsg := d.setSessionPinned(m.SessionID, m.Pinned); errMsg != "" {
			d.sendError(conn, errMsg)
			return
		}
		d.sendOK(conn)
	case protocol.CmdSetSessionContextWindowCap: // wire: set_session_context_window_cap
		m := msg.(*protocol.SetSessionContextWindowCapMessage)
		if err := d.setSessionContextWindowCap(m.SessionID, m.Cap); err != nil {
			d.sendError(conn, err.Error())
			return
		}
		d.sendOK(conn)
	case protocol.CmdCollapseRepo: // wire: collapse_repo
		d.handleCollapseRepo(conn, msg.(*protocol.CollapseRepoMessage))
	case protocol.CmdQueryRepos: // wire: query_repos
		d.handleQueryRepos(conn, msg.(*protocol.QueryReposMessage))
	case protocol.CmdQueryAuthors: // wire: query_authors
		d.handleQueryAuthors(conn, msg.(*protocol.QueryAuthorsMessage))
	case protocol.CmdFetchPRDetails: // wire: fetch_pr_details
		d.handleFetchPRDetails(conn, msg.(*protocol.FetchPRDetailsMessage))
	case protocol.CmdInjectTestPR: // wire: inject_test_pr
		d.handleInjectTestPR(conn, msg.(*protocol.InjectTestPRMessage))
	case protocol.CmdInjectTestSession: // wire: inject_test_session
		d.handleInjectTestSession(conn, msg.(*protocol.InjectTestSessionMessage))
	case protocol.CmdOpenMarkdown: // wire: open_markdown
		d.handleOpenMarkdown(conn, msg.(*protocol.OpenMarkdownMessage))
	case protocol.CmdOpenSeed: // wire: open_seed
		d.handleOpenSeed(conn, msg.(*protocol.OpenSeedMessage))
	case protocol.CmdOpenSentFiles: // wire: open_sent_files
		d.handleOpenSentFiles(conn, msg.(*protocol.OpenSentFilesMessage))
	case protocol.CmdOpenBrowser: // wire: open_browser
		d.handleOpenBrowser(conn, msg.(*protocol.OpenBrowserMessage))
	case protocol.CmdBrowserControl: // wire: browser_control
		d.handleBrowserControl(conn, msg.(*protocol.BrowserControlMessage))
	case protocol.CmdListWorktrees: // wire: list_worktrees
		d.handleListWorktrees(conn, msg.(*protocol.ListWorktreesMessage))
	case protocol.CmdCreateWorktree: // wire: create_worktree
		d.handleCreateWorktree(conn, msg.(*protocol.CreateWorktreeMessage))
	case protocol.CmdDeleteWorktree: // wire: delete_worktree
		d.handleDeleteWorktree(conn, msg.(*protocol.DeleteWorktreeMessage))
	default:
		d.sendError(conn, "unknown command")
	}
}

func (d *Daemon) handleRegister(conn net.Conn, msg *protocol.RegisterMessage) {
	d.logf("session registered: id=%s label=%s dir=%s", msg.ID, protocol.Deref(msg.Label), msg.Dir)
	existing := d.store.Get(msg.ID)

	branchInfo, _ := git.GetBranchInfo(msg.Dir)

	nowStr := string(protocol.TimestampNow())
	agent := normalizeStoredSessionAgent(string(protocol.Deref(msg.Agent)), protocol.SessionAgentClaude)
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
		if branchInfo.Repository != "" {
			session.Repository = protocol.Ptr(branchInfo.Repository)
		}
	}
	workspaceID := strings.TrimSpace(msg.WorkspaceID)
	if workspaceID == "" {
		d.sendError(conn, "missing workspace_id")
		return
	}
	if member := strings.TrimSpace(protocol.Deref(msg.Member)); member != "" {
		memberID, err := d.claimCrewBinding(member, msg.ID)
		if err != nil {
			d.sendError(conn, fmt.Sprintf("crew bind %q: %v", member, err))
			return
		}
		d.logf("session %s registering as crew member %s", msg.ID, crew.DisplayName(memberID))
	} else {
		d.releaseCrewBindingIfSession(msg.ID)
	}
	session.WorkspaceID = workspaceID
	if err := d.store.AddCheckedUnlessTeardown(session); err != nil {
		d.releaseCrewBindingIfSession(session.ID)
		d.sendError(conn, err.Error())
		return
	}
	existingWS := d.store.GetWorkspace(workspaceID)
	workspaceTitle := session.Label
	if existingWS != nil && strings.TrimSpace(existingWS.Title) != "" {
		workspaceTitle = existingWS.Title
	}
	workspaceRank := d.resolveWorkspaceRank(existingWS)
	d.store.AddWorkspace(&protocol.Workspace{ID: workspaceID, Title: workspaceTitle, Directory: session.Directory, Status: protocol.WorkspaceStatusLaunching, Rank: workspaceRank})
	d.workspaces.register(workspaceID, workspaceTitle, session.Directory, workspaceRank, false, false)
	if pending, ok := d.consumePendingAgentConversation(session.ID); ok {
		d.observeAgentConversation(pending)
	}
	if err := d.store.ClearTicketReconciliationForAssignee(session.ID); err != nil {
		d.logf("clear ticket reconciliation on register for %s: %v", session.ID, err)
	}
	d.reviveCrashedTicketsForSession(session.ID)
	d.associateSessionWithWorkspace(session.ID, workspaceID)
	if _, err := d.ensureWorkspaceLayout(workspaceID); err != nil {
		d.logf("workspace layout bootstrap failed for workspace %s: %v", workspaceID, err)
	}

	d.store.UpsertRecentLocation(msg.Dir)

	d.sendOK(conn)

	fact := FactSessionRegistered
	if existing != nil {
		fact = FactSessionReregistered
	}
	d.publishFact(fact, session.ID, nil)
	d.broadcastWorkspaceLayout(workspaceID)
	d.recomputeAndBroadcastWorkspaceForSession(session.ID)
}

func (d *Daemon) projectSessionEvent(event, sessionID string) {
	decorated := d.sessionForBroadcast(d.store.Get(sessionID))
	if decorated == nil {
		return
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:   event,
		Session: decorated,
	})
}

func (d *Daemon) projectSessionUnregistered(ev bus.Event) {
	session, ok := decodeFact[*protocol.Session](d, ev)
	if !ok || session == nil {
		return
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:   protocol.EventSessionUnregistered,
		Session: session,
	})
}

func (d *Daemon) publishSessionUnregistered(session *protocol.Session) {
	if session == nil {
		return
	}
	d.publishFact(FactSessionUnregistered, session.ID, d.sessionForBroadcast(session))
}

func (d *Daemon) handleUnregister(conn net.Conn, msg *protocol.UnregisterMessage) {
	teardown, err := d.prepareSessionTeardown(msg.ID)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("prepare session teardown: %v", err))
		return
	}
	d.commitSessionUnregister(msg.ID, store.SessionClose{By: store.SessionClosedByUser})
	d.sendOK(conn)

	if teardown != nil && teardown.session != nil {
		d.publishSessionUnregistered(teardown.session)
		d.dissociateSessionFromWorkspace(teardown.session.ID)
		d.removeWorkspaceLayoutPaneForSession(teardown.session.ID)
	}
	if teardown != nil {
		d.terminateSessionAsync(msg.ID, syscall.SIGTERM, teardown)
	}
}

func (d *Daemon) handleState(conn net.Conn, msg *protocol.StateMessage) {
	d.logf("hook evidence: id=%s state=%s", msg.ID, msg.State)
	if strings.EqualFold(strings.TrimSpace(protocol.Deref(msg.HookEvent)), "user_prompt_submit") &&
		strings.TrimSpace(protocol.Deref(msg.Prompt)) != "" {
		effects := d.observePromptTaken(msg.ID, protocol.Deref(msg.Prompt), time.Now())
		origin := sessionInputOrigin{}
		if effects.taken != nil {
			origin = effects.taken.origin
		}
		go d.maybeGenerateSessionTitleFromPrompt(msg.ID, protocol.Deref(msg.Prompt), origin)
	}
	d.runPostInitialPrompt(msg.ID, msg.State)
	d.tracePermissionMode(msg.ID, protocol.Deref(msg.PermissionMode))
	d.recordReviewerEvidenceFromPermissionMode(msg.ID, protocol.Deref(msg.PermissionMode))
	d.recordBracketEvidence(msg.ID, msg.State)
	d.store.Touch(msg.ID)
	d.traceStateEvidence(msg.ID, stateOrigin{source: stateSourceHook}, msg.State)
	d.sendOK(conn)
}

func (d *Daemon) persistResumeSessionID(sessionID, resumeSessionID string) {
	if _, err := d.store.TransitionSessionResumeID(sessionID, resumeSessionID); err != nil {
		d.logf("persistResumeSessionID: update failed for session %s: %v", sessionID, err)
	}
	d.rememberDispatchResume(sessionID, resumeSessionID)
}

func (d *Daemon) handleStop(conn net.Conn, msg *protocol.StopMessage) {
	reportedTranscriptPath := strings.TrimSpace(msg.TranscriptPath)
	msg.TranscriptPath = d.resolveStopTranscriptPath(d.store.Get(msg.ID), reportedTranscriptPath)
	if reportedTranscriptPath != "" && msg.TranscriptPath != reportedTranscriptPath {
		d.logf("handleStop: ignored transcript path for session=%s: reported=%s bound=%s", msg.ID, reportedTranscriptPath, msg.TranscriptPath)
	}
	d.logf("handleStop: session=%s, transcript_path=%s", msg.ID, msg.TranscriptPath)

	relaxBackgroundWork := d.isChiefOfStaffSession(msg.ID)
	d.recordStopFacts(
		msg.ID,
		!relaxBackgroundWork && hasActiveBackgroundTask(msg),
		hasPendingSessionCron(msg),
	)
	if stopIsNonTerminal(msg, relaxBackgroundWork) {
		tasks := describeBackgroundTasks(msg)
		d.logf(
			"handleStop: non-terminal stop session=%s pending_crons=%d background_tasks=[%s]",
			msg.ID, protocol.Deref(msg.PendingSessionCrons), tasks,
		)
		d.traceStateEvidence(
			msg.ID,
			stateOrigin{source: stateSourceStopHook, detail: "non-terminal stop: " + tasks},
			"",
		)
		d.sendOK(conn)
		if d.consumeForcedStopClassification(msg.ID) {
			d.logf("handleStop: skipping yield classification for daemon-terminated session=%s", msg.ID)
			return
		}
		go d.classifyStop(msg.ID, msg.TranscriptPath, stopClassification{
			yielded:                true,
			runningBackgroundTasks: runningBackgroundTaskCount(msg),
		})
		return
	}

	d.recordBracketEvidence(msg.ID, protocol.StateIdle)

	if session := d.store.Get(msg.ID); session != nil {
		if resumeSessionID := agentdriver.ResumeSessionIDFromStopTranscriptPath(
			agentdriver.Get(string(session.Agent)),
			msg.TranscriptPath,
		); resumeSessionID != "" {
			d.observeAgentConversation(agentConversationObservation{
				SessionID:      msg.ID,
				NativeID:       resumeSessionID,
				TranscriptPath: msg.TranscriptPath,
			})
			d.rememberDispatchResume(msg.ID, resumeSessionID)
		}
	}
	d.store.Touch(msg.ID)
	d.sendOK(conn)

	stopWorkspaceID := d.resolveStopWorkspaceID(msg.ID)
	d.enqueueSummarizeSession(msg.ID, msg.TranscriptPath, stopWorkspaceID)
	if stopWorkspaceID != "" {
		d.markNotebookWorkspaceActivity(stopWorkspaceID)
		if d.store.GetWorkspace(stopWorkspaceID) != nil {
			d.enqueueNarrateWorkspace(stopWorkspaceID)
		}
	}

	if d.consumeForcedStopClassification(msg.ID) {
		d.logf("handleStop: skipping classification for daemon-terminated session=%s", msg.ID)
		return
	}

	go d.classifySessionState(msg.ID, msg.TranscriptPath)
	go d.maybeGenerateSessionTitle(msg.ID, msg.TranscriptPath)
}

func (d *Daemon) resolveStopTranscriptPath(session *protocol.Session, reported string) string {
	if exact := d.resolveTranscriptPathForSession(session, ""); exact != "" {
		return exact
	}
	if session != nil && strings.TrimSpace(d.store.GetSessionTranscriptPath(session.ID)) != "" {
		return ""
	}
	return strings.TrimSpace(reported)
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

	if bound := strings.TrimSpace(d.store.GetSessionTranscriptPath(session.ID)); bound != "" {
		if _, err := os.Stat(bound); err == nil {
			return bound
		}
	}

	if watched := d.liveTranscriptPath(session.ID, session.Agent); watched != "" {
		return watched
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
	decorated := d.sessionForBroadcastWithChiefOfStaff(
		session,
		d.chiefOfStaffSessionID(),
		d.delegatedFromChiefSessionIDs(),
		d.crewMembersBySession(),
		d.gardenDispatchSeedsBySession(),
	)
	if decorated != nil {
		decorated.Automation = d.automationProvenanceForSession(decorated.ID)
		decorated.PullRequests = d.sessionPullRequestsForSession(decorated.ID)
	}
	return decorated
}

func (d *Daemon) sessionForBroadcastWithChiefOfStaff(
	session *protocol.Session,
	chiefOfStaffSessionID string,
	delegatedFromChief map[string]bool,
	crewBySession map[string]string,
	seedBySession map[string]string,
) *protocol.Session {
	clone := cloneSession(session)
	if clone == nil {
		return nil
	}
	d.decorateSessionWithStateReason(clone)
	d.decorateSessionWithNudge(clone)
	d.decorateSessionWithAutoSettle(clone)
	d.decorateSessionWithSnooze(clone)
	d.decorateChiefOfStaffWithSessionID(clone, chiefOfStaffSessionID)
	d.decorateDelegatedFromChief(clone, delegatedFromChief)
	d.decorateCrewMember(clone, crewBySession)
	d.decorateSessionSeed(clone, seedBySession)
	d.decorateSessionWithWorkspace(clone)
	d.decorateSessionWithWorkspaceMute(clone)
	d.decorateSessionWithCost(clone)
	d.decorateSessionWithTerminalBuild(clone)
	d.decorateSessionWithTurn(clone)
	return clone
}

func (d *Daemon) sessionsForBroadcast(sessions []*protocol.Session) []protocol.Session {
	if len(sessions) == 0 {
		return nil
	}
	chiefOfStaffSessionID := d.chiefOfStaffSessionID()
	delegatedFromChief := d.delegatedFromChiefSessionIDs()
	crewBySession := d.crewMembersBySession()
	seedBySession := d.gardenDispatchSeedsBySession()
	bySession, _ := d.latestAutomationProvenance()
	pullRequestsBySession := d.store.ListSessionPullRequestsBySession()
	out := make([]protocol.Session, 0, len(sessions))
	for _, session := range sessions {
		if decorated := d.sessionForBroadcastWithChiefOfStaff(session, chiefOfStaffSessionID, delegatedFromChief, crewBySession, seedBySession); decorated != nil {
			decorated.Automation = bySession[decorated.ID]
			decorated.PullRequests = sessionPullRequestsForBroadcast(pullRequestsBySession[decorated.ID])
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
	d.publishFact(FactSessionStateChanged, sessionID, nil)
	d.recomputeAndBroadcastWorkspaceForSession(sessionID)
}

func (d *Daemon) projectSessionStateChanged(sessionID string) {
	session := d.store.Get(sessionID)
	decorated := d.sessionForBroadcast(session)
	if decorated == nil {
		return
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:   protocol.EventSessionStateChanged,
		Session: decorated,
	})
}

type rateLimitWindow struct {
	ResetAt string `json:"reset_at"`
}

func (d *Daemon) broadcastRateLimited(resource string, resetAt time.Time) {
	d.publishFact(FactRateLimited, resource, rateLimitWindow{
		ResetAt: string(protocol.NewTimestamp(resetAt)),
	})
}

func (d *Daemon) projectRateLimited(ev bus.Event) {
	window, ok := decodeFact[rateLimitWindow](d, ev)
	if !ok {
		return
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:             protocol.EventRateLimited,
		RateLimitResource: protocol.Ptr(ev.Subject),
		RateLimitResetAt:  protocol.Ptr(window.ResetAt),
	})
}

func (d *Daemon) handleFilesEdited(conn net.Conn, msg *protocol.FilesEditedMessage) {
	for _, path := range msg.Paths {
		path = strings.TrimSpace(path)
		if !filepath.IsAbs(path) {
			continue
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".md", ".markdown":
		default:
			continue
		}
		d.store.RecordFileActivity(path, store.FileActivitySourceEdited, msg.ID)
	}
	d.sendOK(conn)
}

func (d *Daemon) handleTodos(conn net.Conn, msg *protocol.TodosMessage) {
	d.store.UpdateTodos(msg.ID, msg.Todos)
	d.store.Touch(msg.ID)
	d.sendOK(conn)

	for _, s := range d.store.List("") {
		if s.ID == msg.ID {
			d.publishFact(FactSessionTodosChanged, s.ID, nil)
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

	prs := d.store.ListPRsByRepoHost(repo, host)

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

	previousPRs := d.store.ListPRs("")
	d.store.SetPRs(allPRs)

	currentPRs := d.store.ListPRs("")
	d.publishPRSetChanges(previousPRs, currentPRs)

	waiting := 0
	for _, pr := range currentPRs {
		if pr.State == protocol.PRStateWaiting && !pr.Muted {
			waiting++
		}
	}
	d.logf("PR poll: %d PRs (%d waiting)", len(currentPRs), waiting)

	for host, observation := range observedByHost {
		host, observation := host, observation
		go d.observeGitHubReviewRequests(host, observation.prs, observation.observedAt)
	}

	d.doDetailRefresh()
}

func (d *Daemon) doDetailRefresh() {
	if !d.githubAvailable() {
		return
	}

	d.store.DecayHeatStates()

	prs := d.store.GetPRsNeedingDetailRefresh()
	if len(prs) == 0 {
		return
	}

	d.logf("Detail refresh: %d PRs need refresh", len(prs))

	var refreshedIDs []string
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

		prHeadSHA := protocol.Deref(pr.HeadSHA)
		if prHeadSHA != "" && details.HeadSHA != prHeadSHA {
			d.store.SetPRHot(pr.ID)
		}

		d.store.UpdatePRDetails(pr.ID, details.Mergeable, details.MergeableState, details.CIStatus, details.ReviewStatus, details.HeadSHA, details.HeadBranch)
		refreshedIDs = append(refreshedIDs, pr.ID)
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

	if len(refreshedIDs) > 0 {
		d.logf("Detail refresh: updated %d PRs", len(refreshedIDs))
		d.coalesceSnapshots(func() {
			for _, id := range refreshedIDs {
				d.publishFact(FactPRDetailsChanged, id, nil)
			}
		})
	}
}

func (d *Daemon) fetchAllPRDetails() {
	if !d.githubAvailable() {
		return
	}

	allPRs := d.store.ListPRs("")
	if len(allPRs) == 0 {
		return
	}

	d.logf("App launch: fetching details for %d PRs", len(allPRs))

	var refreshedIDs []string
	limitedHosts := make(map[string]time.Time)
	for _, pr := range allPRs {
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
		refreshedIDs = append(refreshedIDs, pr.ID)
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

	if len(refreshedIDs) > 0 {
		d.logf("App launch: updated %d PRs", len(refreshedIDs))
		d.coalesceSnapshots(func() {
			for _, id := range refreshedIDs {
				d.publishFact(FactPRDetailsChanged, id, nil)
			}
		})
	}
}

func (d *Daemon) handleInjectTestPR(conn net.Conn, msg *protocol.InjectTestPRMessage) {
	if msg.PR.ID == "" {
		d.sendError(conn, "PR ID cannot be empty")
		return
	}

	existing := d.store.GetPR(msg.PR.ID)
	d.store.AddPR(&msg.PR)
	d.sendOK(conn)

	if existing == nil {
		d.publishFact(FactPRAppeared, msg.PR.ID, nil)
	} else {
		d.publishFact(FactPRUpdated, msg.PR.ID, nil)
	}
}

func (d *Daemon) handleInjectTestSession(conn net.Conn, msg *protocol.InjectTestSessionMessage) {
	if msg.Session.ID == "" {
		d.sendError(conn, "Session ID cannot be empty")
		return
	}

	msg.Session.Agent = normalizeStoredSessionAgent(string(msg.Session.Agent), protocol.SessionAgentCodex)
	stampSessionTimestamps(&msg.Session, string(protocol.TimestampNow()))
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

	d.publishFact(FactSessionRegistered, msg.Session.ID, nil)
	d.broadcastWorkspaceLayout(workspaceID)
}

func (d *Daemon) RefreshPRs() {
	if !d.githubAvailable() {
		return
	}
	d.doPRPoll()
}

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

	previousPRs := d.store.ListPRs("")
	d.store.SetPRs(allPRs)

	currentPRs := d.store.ListPRs("")
	d.publishPRSetChanges(previousPRs, currentPRs)

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

func (d *Daemon) fetchPRDetailsImmediate(prID string) {
	if !d.githubAvailable() {
		return
	}

	pr := d.store.GetPR(prID)
	if pr == nil {
		return
	}

	if pr.Muted {
		return
	}
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

	if limited, resetAt := client.IsRateLimited("core"); limited {
		d.logf("Immediate fetch skipped for %s: rate limited until %v", prID, resetAt)
		return
	}

	d.store.SetPRHot(prID)

	details, err := client.FetchPRDetails(pr.Repo, pr.Number)
	if err != nil {
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

func (d *Daemon) monitorBranches() {
	d.logf("Branch monitoring started (%s interval)", branchMonitorInterval)

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

	d.coalesceSnapshots(func() {
		for _, session := range sessions {
			info, err := git.GetBranchInfo(session.Directory)
			if err != nil {
				continue
			}

			if info.Branch != protocol.Deref(session.Branch) || info.IsWorktree != protocol.Deref(session.IsWorktree) ||
				info.Repository != protocol.Deref(session.Repository) {
				d.store.UpdateBranch(session.ID, info.Branch, info.IsWorktree, info.MainRepo, info.Repository)
				d.logf("Branch changed: session=%s branch=%s isWorktree=%v", session.ID, info.Branch, info.IsWorktree)
				d.publishFact(FactSessionBranchChanged, session.ID, nil)
			}
		}
	})
}

func (d *Daemon) publishSessionsReconciled(report workerReconcileReport) {
	if !report.Changed {
		return
	}
	d.coalesceSnapshots(func() {
		for _, sessionID := range report.ChangedSessionIDs {
			d.publishFact(FactSessionReconciled, sessionID, nil)
		}
	})
}

func (d *Daemon) publishEndpointSessionsChanged(endpointID string) {
	d.publishFact(FactEndpointSessionsChanged, endpointID, nil)
}

func (d *Daemon) projectSessionsUpdated() {
	d.projectSnapshot(snapshotSessions, func() {
		if d.wsHub == nil || d.store == nil {
			return
		}
		d.wsHub.Broadcast(&protocol.WebSocketEvent{
			Event:    protocol.EventSessionsUpdated,
			Sessions: d.mergedSessionsForBroadcast(),
		})
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
	d.publishFact(FactEndpointStatusChanged, info.ID, info)
}

func (d *Daemon) projectEndpointStatusChanged(ev bus.Event) {
	info, ok := decodeFact[protocol.EndpointInfo](d, ev)
	if !ok {
		return
	}
	d.broadcastMessage(&protocol.EndpointStatusChangedMessage{
		Event:    protocol.EventEndpointStatusChanged,
		Endpoint: info,
	})
}

func (d *Daemon) projectEndpointsUpdated() {
	d.projectSnapshot(snapshotEndpoints, func() {
		d.broadcastMessage(&protocol.EndpointsUpdatedMessage{
			Event:     protocol.EventEndpointsUpdated,
			Endpoints: d.listEndpointInfos(),
		})
	})
}

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
		"profile":            config.ProfileLabel(),
		"data_dir":           dataDir,
		"socket_path":        socketPath,
		"port":               config.WSPort(),
		"headless_tasks":     headless.Describe(),
	}
	if routingPathError != "" {
		health["routing_path_error"] = routingPathError
	}
	if status, err := d.enrollmentStatus(); err != nil {
		health["enrollment"] = "unknown"
		health["enrollment_error"] = err.Error()
	} else {
		health["enrollment"] = status.Describe()
		health["home_daemon_id"] = status.HomeDaemonID
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
