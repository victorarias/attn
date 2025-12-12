package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/victorarias/claude-manager/internal/classifier"
	"github.com/victorarias/claude-manager/internal/config"
	"github.com/victorarias/claude-manager/internal/github"
	"github.com/victorarias/claude-manager/internal/logging"
	"github.com/victorarias/claude-manager/internal/protocol"
	"github.com/victorarias/claude-manager/internal/store"
	"github.com/victorarias/claude-manager/internal/transcript"
)

// Daemon manages Claude sessions
type Daemon struct {
	socketPath string
	store      *store.Store
	listener   net.Listener
	httpServer *http.Server
	wsHub      *wsHub
	done       chan struct{}
	logger     *logging.Logger
	ghClient   github.GitHubClient
}

// New creates a new daemon
func New(socketPath string) *Daemon {
	logger, _ := logging.New(logging.DefaultLogPath())

	// Wire up classifier logger to daemon logger
	classifier.SetLogger(func(format string, args ...interface{}) {
		logger.Infof(format, args...)
	})

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

	return &Daemon{
		socketPath: socketPath,
		store:      sessionStore,
		wsHub:      newWSHub(),
		done:       make(chan struct{}),
		logger:     logger,
		ghClient:   ghClient,
	}
}

// NewForTesting creates a daemon with a non-persistent store for tests
func NewForTesting(socketPath string) *Daemon {
	return &Daemon{
		socketPath: socketPath,
		store:      store.New(),
		wsHub:      newWSHub(),
		done:       make(chan struct{}),
		logger:     nil, // No logging in tests
		ghClient:   nil,
	}
}

// NewWithGitHubClient creates a daemon with a custom GitHub client for testing
func NewWithGitHubClient(socketPath string, ghClient github.GitHubClient) *Daemon {
	return &Daemon{
		socketPath: socketPath,
		store:      store.New(),
		wsHub:      newWSHub(),
		done:       make(chan struct{}),
		logger:     nil,
		ghClient:   ghClient,
	}
}

// Start starts the daemon
func (d *Daemon) Start() error {
	// Remove stale socket
	os.Remove(d.socketPath)

	listener, err := net.Listen("unix", d.socketPath)
	if err != nil {
		return err
	}
	d.listener = listener
	d.log("daemon started")

	// Start WebSocket hub
	go d.wsHub.run()

	// Start HTTP server for WebSocket
	go d.startHTTPServer()

	// Note: No background persistence needed - SQLite persists immediately

	// Start PR polling
	go d.pollPRs()

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
	if d.logger != nil {
		d.logger.Close()
	}
}

func (d *Daemon) startHTTPServer() {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", d.handleWS)

	port := os.Getenv("ATTN_WS_PORT")
	if port == "" {
		port = "9849"
	}

	d.httpServer = &http.Server{
		Addr:    "127.0.0.1:" + port,
		Handler: mux,
	}

	d.logf("WebSocket server starting on ws://127.0.0.1:%s/ws", port)
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
	case protocol.MsgInjectTestPR:
		d.handleInjectTestPR(conn, msg.(*protocol.InjectTestPRMessage))
	case protocol.MsgInjectTestSession:
		d.handleInjectTestSession(conn, msg.(*protocol.InjectTestSessionMessage))
	default:
		d.sendError(conn, "unknown command")
	}
}

func (d *Daemon) handleRegister(conn net.Conn, msg *protocol.RegisterMessage) {
	d.logf("session registered: id=%s label=%s dir=%s", msg.ID, msg.Label, msg.Dir)
	now := time.Now()
	session := &protocol.Session{
		ID:             msg.ID,
		Label:          msg.Label,
		Directory:      msg.Dir,
		State:          protocol.StateWaiting,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	}
	d.store.Add(session)
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
	state, err := classifier.Classify(lastMessage, 30*time.Second)
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
	sessions := d.store.List(msg.Filter)
	resp := protocol.Response{
		OK:       true,
		Sessions: sessions,
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
	prs := d.store.ListPRs(msg.Filter)
	resp := protocol.Response{
		OK:  true,
		PRs: prs,
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
		OK:    true,
		Repos: repos,
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
			d.store.UpdatePRDetails(pr.ID, details.Mergeable, details.MergeableState, details.CIStatus, details.ReviewStatus, details.HeadSHA)
		}
	}

	// Return updated PRs
	updatedPRs := d.store.ListPRsByRepo(msg.Repo)
	resp := protocol.Response{
		OK:  true,
		PRs: updatedPRs,
	}
	json.NewEncoder(conn).Encode(resp)
}

func (d *Daemon) sendOK(conn net.Conn) {
	resp := protocol.Response{OK: true}
	json.NewEncoder(conn).Encode(resp)
}

func (d *Daemon) sendError(conn net.Conn, errMsg string) {
	resp := protocol.Response{OK: false, Error: errMsg}
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
	prs, err := d.ghClient.FetchAll()
	if err != nil {
		d.logf("PR poll error: %v", err)
		return
	}

	d.store.SetPRs(prs)

	// Broadcast to WebSocket clients
	allPRs := d.store.ListPRs("")
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event: protocol.EventPRsUpdated,
		PRs:   allPRs,
	})

	// Count waiting (non-muted) PRs for logging
	waiting := 0
	for _, pr := range allPRs {
		if pr.State == protocol.StateWaiting && !pr.Muted {
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
			d.logf("Failed to fetch details for %s: %v", pr.ID, err)
			continue
		}

		// Check if SHA changed (new commits) - triggers hot state
		if pr.HeadSHA != "" && details.HeadSHA != pr.HeadSHA {
			d.store.SetPRHot(pr.ID)
		}

		d.store.UpdatePRDetails(pr.ID, details.Mergeable, details.MergeableState, details.CIStatus, details.ReviewStatus, details.HeadSHA)
		refreshedCount++
	}

	if refreshedCount > 0 {
		d.logf("Detail refresh: updated %d PRs", refreshedCount)
		// Broadcast updated PRs
		d.broadcastPRs()
	}
}

func (d *Daemon) handleInjectTestPR(conn net.Conn, msg *protocol.InjectTestPRMessage) {
	if msg.PR == nil {
		d.sendError(conn, "PR cannot be nil")
		return
	}

	// Add PR directly to store
	d.store.AddPR(msg.PR)
	d.sendOK(conn)

	// Broadcast to WebSocket clients
	allPRs := d.store.ListPRs("")
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event: protocol.EventPRsUpdated,
		PRs:   allPRs,
	})
}

func (d *Daemon) handleInjectTestSession(conn net.Conn, msg *protocol.InjectTestSessionMessage) {
	if msg.Session == nil {
		d.sendError(conn, "Session cannot be nil")
		return
	}

	// Add session directly to store
	d.store.Add(msg.Session)
	d.sendOK(conn)

	// Broadcast to WebSocket clients
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:   protocol.EventSessionRegistered,
		Session: msg.Session,
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
		PRs:   allPRs,
	})

	d.logf("PR refresh: %d PRs fetched", len(prs))
	return nil
}
