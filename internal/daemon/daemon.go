package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
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
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/transcript"
)

type repoCache struct {
	fetchedAt time.Time
	branches  []protocol.Branch
}

// Daemon manages Claude sessions
type Daemon struct {
	socketPath  string
	pidPath     string
	pidFile     *os.File // Held open with flock for exclusive access
	store       *store.Store
	listener    net.Listener
	httpServer  *http.Server
	wsHub       *wsHub
	done        chan struct{}
	logger      *logging.Logger
	ghClient    github.GitHubClient
	classifier  Classifier // Optional, uses package-level classifier.Classify if nil
	claudePath  string     // Resolved path to claude binary (found at startup)
	repoCaches  map[string]*repoCache
	repoCacheMu sync.RWMutex
}

// New creates a new daemon
func New(socketPath string) *Daemon {
	logger, _ := logging.New(logging.DefaultLogPath())

	// Wire up classifier logger to daemon logger
	classifier.SetLogger(func(format string, args ...interface{}) {
		logger.Infof(format, args...)
	})

	// Resolve claude binary path at startup
	claudePath, err := classifier.FindClaudePath()
	if err != nil {
		logger.Infof("Claude CLI not found: %v (classifier will be disabled)", err)
	} else {
		logger.Infof("Claude CLI found at: %s", claudePath)
	}

	var ghClient github.GitHubClient
	client, err := github.NewClient("")
	if err != nil {
		logger.Infof("GitHub client not available: %v", err)
	} else {
		ghClient = client
	}

	// Create SQLite-backed store
	sessionStore, err := store.NewWithDB(config.DBPath())
	if err != nil {
		logger.Infof("Failed to open DB at %s: %v (using in-memory)", config.DBPath(), err)
		sessionStore = store.New() // Fallback to in-memory
	}

	// Clean up legacy JSON state file if it exists
	legacyPath := config.StatePath()
	if _, err := os.Stat(legacyPath); err == nil {
		os.Remove(legacyPath)
		logger.Infof("Removed legacy state file: %s", legacyPath)
	}

	// Derive PID path from socket path directory
	pidPath := filepath.Join(filepath.Dir(socketPath), "attn.pid")

	return &Daemon{
		socketPath: socketPath,
		pidPath:    pidPath,
		store:      sessionStore,
		wsHub:      newWSHub(),
		done:       make(chan struct{}),
		logger:     logger,
		ghClient:   ghClient,
		claudePath: claudePath,
		repoCaches: make(map[string]*repoCache),
	}
}

// NewForTesting creates a daemon with a non-persistent store for tests
func NewForTesting(socketPath string) *Daemon {
	pidPath := filepath.Join(filepath.Dir(socketPath), "attn.pid")
	return &Daemon{
		socketPath: socketPath,
		pidPath:    pidPath,
		store:      store.New(),
		wsHub:      newWSHub(),
		done:       make(chan struct{}),
		logger:     nil, // No logging in tests
		ghClient:   nil,
		repoCaches: make(map[string]*repoCache),
	}
}

// NewWithGitHubClient creates a daemon with a custom GitHub client for testing
func NewWithGitHubClient(socketPath string, ghClient github.GitHubClient) *Daemon {
	pidPath := filepath.Join(filepath.Dir(socketPath), "attn.pid")
	return &Daemon{
		socketPath: socketPath,
		pidPath:    pidPath,
		store:      store.New(),
		wsHub:      newWSHub(),
		done:       make(chan struct{}),
		logger:     nil,
		ghClient:   ghClient,
		repoCaches: make(map[string]*repoCache),
	}
}

// Start starts the daemon
func (d *Daemon) Start() error {
	// Acquire PID lock (kills any existing daemon)
	if err := d.acquirePIDLock(); err != nil {
		return fmt.Errorf("acquire PID lock: %w", err)
	}

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

	// Create HTTP server for WebSocket (must be created synchronously to avoid race with Stop())
	d.initHTTPServer()
	go d.runHTTPServer()

	// Note: No background persistence needed - SQLite persists immediately

	// Start PR polling
	go d.pollPRs()

	// Start branch monitoring
	go d.monitorBranches()

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
				log.Printf("accept error: %v", err)
				continue
			}
		}

		go d.handleConnection(conn)
	}
}

// Stop stops the daemon
func (d *Daemon) Stop() {
	d.log("daemon stopping")
	close(d.done)
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

// acquirePIDLock ensures only one daemon instance runs at a time using flock.
// If another daemon is running, it sends SIGTERM and waits for it to exit.
func (d *Daemon) acquirePIDLock() error {
	// Open or create the PID file
	f, err := os.OpenFile(d.pidPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return fmt.Errorf("open PID file: %w", err)
	}

	// Try non-blocking exclusive lock first
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		// Another process holds the lock - read PID and try to kill it
		data, readErr := os.ReadFile(d.pidPath)
		if readErr == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if parseErr == nil && pid > 0 {
				process, findErr := os.FindProcess(pid)
				if findErr == nil {
					if signalErr := process.Signal(syscall.Signal(0)); signalErr == nil {
						// Process is running, kill it
						d.logf("Found existing daemon (PID %d), sending SIGTERM", pid)
						process.Signal(syscall.SIGTERM)

						// Wait up to 3 seconds for graceful shutdown
						deadline := time.Now().Add(3 * time.Second)
						for time.Now().Before(deadline) {
							time.Sleep(100 * time.Millisecond)
							if err := process.Signal(syscall.Signal(0)); err != nil {
								d.logf("Previous daemon exited gracefully")
								break
							}
						}

						// Force kill if still running
						if err := process.Signal(syscall.Signal(0)); err == nil {
							d.logf("Previous daemon didn't exit, sending SIGKILL")
							process.Signal(syscall.SIGKILL)
							time.Sleep(200 * time.Millisecond)
						}
					}
				}
			}
		}

		// Now try blocking lock (should succeed after killing old daemon)
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if err != nil {
			f.Close()
			return fmt.Errorf("acquire flock after killing old daemon: %w", err)
		}
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

	// Wait for WebSocket port to be available (old daemon may still be releasing it)
	if err := d.waitForPort(); err != nil {
		d.logf("Warning: port availability check failed: %v", err)
	}

	return nil
}

// waitForPort waits for the WebSocket port to become available
func (d *Daemon) waitForPort() error {
	port := os.Getenv("ATTN_WS_PORT")
	if port == "" {
		port = "9849"
	}
	addr := "127.0.0.1:" + port

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err != nil {
			// Port is not in use - good!
			return nil
		}
		conn.Close()
		// Port still in use, wait and retry
		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("port %s still in use after 5s", port)
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

	// Get branch info
	branchInfo, _ := git.GetBranchInfo(msg.Dir)

	nowStr := string(protocol.TimestampNow())
	session := &protocol.Session{
		ID:             msg.ID,
		Label:          protocol.Deref(msg.Label),
		Directory:      msg.Dir,
		State:          protocol.SessionStateWaitingInput,
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

	// Broadcast to WebSocket clients
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:   protocol.EventSessionRegistered,
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

	d.store.Remove(msg.ID)
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

	// Parse transcript for last assistant message
	d.logf("classifySessionState: parsing transcript for session %s", sessionID)
	lastMessage, err := transcript.ExtractLastAssistantMessage(transcriptPath, 500)
	if err != nil {
		d.logf("classifySessionState: transcript parse error for %s: %v", sessionID, err)
		// Default to waiting_input on error (safer)
		d.updateAndBroadcastStateWithTimestamp(sessionID, protocol.StateWaitingInput, classificationStartTime)
		return
	}

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
	var state string
	if d.classifier != nil {
		state, err = d.classifier.Classify(lastMessage, 30*time.Second)
	} else if d.claudePath != "" {
		state, err = classifier.ClassifyWithPath(d.claudePath, lastMessage, 30*time.Second)
	} else {
		d.logf("classifySessionState: claude CLI not available, defaulting to waiting_input")
		state = protocol.StateWaitingInput
		err = nil
	}
	if err != nil {
		d.logf("classifySessionState: classifier error for %s: %v", sessionID, err)
		// Default to waiting_input on error
		state = protocol.StateWaitingInput
	}

	d.logf("classifySessionState: session %s classified as %s", sessionID, state)
	d.updateAndBroadcastStateWithTimestamp(sessionID, state, classificationStartTime)
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

func (d *Daemon) handleFetchPRDetails(conn net.Conn, msg *protocol.FetchPRDetailsMessage) {
	if d.ghClient == nil || !d.ghClient.IsAvailable() {
		d.sendError(conn, "GitHub client not available")
		return
	}

	// Get all PRs for this repo
	prs := d.store.ListPRsByRepo(msg.Repo)

	// Fetch details for each PR that needs refresh
	for _, pr := range prs {
		if pr.NeedsDetailRefresh() {
			details, err := d.ghClient.FetchPRDetails(pr.Repo, pr.Number)
			if err != nil {
				d.logf("Failed to fetch details for %s: %v", pr.ID, err)
				continue
			}
			d.store.UpdatePRDetails(pr.ID, details.Mergeable, details.MergeableState, details.CIStatus, details.ReviewStatus, details.HeadSHA, details.HeadBranch)
		}
	}

	// Return updated PRs
	updatedPRs := d.store.ListPRsByRepo(msg.Repo)
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
	if d.ghClient == nil || !d.ghClient.IsAvailable() {
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
	// Check if rate limited before polling
	if limited, resetAt := d.ghClient.IsRateLimited("search"); limited {
		d.logf("PR poll skipped: search API rate limited until %s", resetAt.Format(time.RFC3339))
		d.broadcastRateLimited("search", resetAt)
		return
	}

	prs, err := d.ghClient.FetchAll()
	if err != nil {
		// Check if it's a rate limit error
		if errors.Is(err, github.ErrRateLimited) {
			// Get reset time from client state
			if info := d.ghClient.GetRateLimit("search"); info != nil {
				d.logf("PR poll rate limited until %s", info.ResetAt.Format(time.RFC3339))
				d.broadcastRateLimited("search", info.ResetAt)
			} else {
				d.logf("PR poll rate limited (unknown reset time)")
				d.broadcastRateLimited("search", time.Now().Add(60*time.Second))
			}
		} else {
			d.logf("PR poll error: %v", err)
		}
		return
	}

	d.store.SetPRs(prs)

	// Broadcast to WebSocket clients
	allPRs := d.store.ListPRs("")
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event: protocol.EventPRsUpdated,
		Prs:   protocol.PRsToValues(allPRs),
	})

	// Count waiting (non-muted) PRs for logging
	waiting := 0
	for _, pr := range allPRs {
		if pr.State == protocol.PRStateWaiting && !pr.Muted {
			waiting++
		}
	}
	d.logf("PR poll: %d PRs (%d waiting)", len(prs), waiting)

	// Run detail refresh after list poll
	d.doDetailRefresh()
}

// doDetailRefresh fetches details for PRs that need refresh based on heat state
func (d *Daemon) doDetailRefresh() {
	if d.ghClient == nil || !d.ghClient.IsAvailable() {
		return
	}

	// Check if already rate limited before starting
	if limited, resetAt := d.ghClient.IsRateLimited("core"); limited {
		d.logf("Detail refresh: skipping, rate limited until %v", resetAt)
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
	for _, pr := range prs {
		details, err := d.ghClient.FetchPRDetails(pr.Repo, pr.Number)
		if err != nil {
			// If rate limited, stop the loop and broadcast notification
			if errors.Is(err, github.ErrRateLimited) {
				d.logf("Detail refresh: rate limited, stopping refresh loop")
				if _, resetAt := d.ghClient.IsRateLimited("core"); !resetAt.IsZero() {
					d.broadcastRateLimited("core", resetAt)
				}
				break
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

	if refreshedCount > 0 {
		d.logf("Detail refresh: updated %d PRs", refreshedCount)
		// Broadcast updated PRs
		d.broadcastPRs()
	}
}

// fetchAllPRDetails fetches details for all visible PRs (called on app launch)
func (d *Daemon) fetchAllPRDetails() {
	if d.ghClient == nil || !d.ghClient.IsAvailable() {
		return
	}

	// Check if already rate limited before starting
	if limited, resetAt := d.ghClient.IsRateLimited("core"); limited {
		d.logf("App launch: skipping detail fetch, rate limited until %v", resetAt)
		d.broadcastRateLimited("core", resetAt)
		return
	}

	// Get all visible PRs (not muted)
	allPRs := d.store.ListPRs("")
	if len(allPRs) == 0 {
		return
	}

	d.logf("App launch: fetching details for %d PRs", len(allPRs))

	refreshedCount := 0
	for _, pr := range allPRs {
		// Skip muted PRs and PRs from muted repos
		if pr.Muted {
			continue
		}
		repoState := d.store.GetRepoState(pr.Repo)
		if repoState != nil && repoState.Muted {
			continue
		}

		details, err := d.ghClient.FetchPRDetails(pr.Repo, pr.Number)
		if err != nil {
			// If rate limited, stop the loop and broadcast notification
			if errors.Is(err, github.ErrRateLimited) {
				d.logf("App launch: rate limited, stopping detail fetch loop")
				if _, resetAt := d.ghClient.IsRateLimited("core"); !resetAt.IsZero() {
					d.broadcastRateLimited("core", resetAt)
				}
				break
			}
			d.logf("Failed to fetch details for %s: %v", pr.ID, err)
			continue
		}

		d.store.UpdatePRDetails(pr.ID, details.Mergeable, details.MergeableState, details.CIStatus, details.ReviewStatus, details.HeadSHA, details.HeadBranch)
		refreshedCount++
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
	if d.ghClient == nil || !d.ghClient.IsAvailable() {
		return
	}
	d.doPRPoll()
}

// doRefreshPRsWithResult triggers PR refresh and returns any error
func (d *Daemon) doRefreshPRsWithResult() error {
	if d.ghClient == nil || !d.ghClient.IsAvailable() {
		return fmt.Errorf("GitHub client not available")
	}

	prs, err := d.ghClient.FetchAll()
	if err != nil {
		return fmt.Errorf("failed to fetch PRs: %w", err)
	}

	d.store.SetPRs(prs)

	// Broadcast to WebSocket clients
	allPRs := d.store.ListPRs("")
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event: protocol.EventPRsUpdated,
		Prs:   protocol.PRsToValues(allPRs),
	})

	d.logf("PR refresh: %d PRs fetched", len(prs))
	return nil
}

// fetchPRDetailsImmediate fetches details for a single PR immediately and sets it hot
func (d *Daemon) fetchPRDetailsImmediate(prID string) {
	if d.ghClient == nil || !d.ghClient.IsAvailable() {
		return
	}

	// Check if already rate limited before making request
	if limited, resetAt := d.ghClient.IsRateLimited("core"); limited {
		d.logf("Immediate fetch skipped for %s: rate limited until %v", prID, resetAt)
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

	d.store.SetPRHot(prID)

	details, err := d.ghClient.FetchPRDetails(pr.Repo, pr.Number)
	if err != nil {
		// If rate limited, broadcast notification
		if errors.Is(err, github.ErrRateLimited) {
			d.logf("Immediate fetch for %s: rate limited", prID)
			if _, resetAt := d.ghClient.IsRateLimited("core"); !resetAt.IsZero() {
				d.broadcastRateLimited("core", resetAt)
			}
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
		// Broadcast all sessions with updated branch info
		sessions = d.store.List("")
		d.wsHub.Broadcast(&protocol.WebSocketEvent{
			Event:    protocol.EventSessionsUpdated,
			Sessions: protocol.SessionsToValues(sessions),
		})
	}
}

// handleHealth returns daemon health status
func (d *Daemon) handleHealth(w http.ResponseWriter, r *http.Request) {
	sessions := d.store.List("")
	prs := d.store.ListPRs("")

	health := map[string]interface{}{
		"status":           "ok",
		"protocol":         protocol.ProtocolVersion,
		"sessions":         len(sessions),
		"prs":              len(prs),
		"ws_clients":       d.wsHub.ClientCount(),
		"github_available": d.ghClient != nil && d.ghClient.IsAvailable(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(health)
}
