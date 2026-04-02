package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

// wsClient represents a connected WebSocket client
type wsClient struct {
	conn       *websocket.Conn
	send       chan outboundMessage
	recv       chan []byte // incoming messages for ordered processing
	slowCount  int         // tracks consecutive failed sends
	sendMu     sync.RWMutex
	sendClosed bool

	// PTY subscriptions keyed by session ID
	attachedStreams map[string]ptybackend.Stream // session -> stream
	attachMu        sync.Mutex

	// Git status subscription state
	gitStatusDir    string
	gitStatusTicker *time.Ticker
	gitStatusStop   chan struct{}
	gitStatusHash   string // hash of last sent status for dedup
	gitStatusMu     sync.Mutex
}

func (c *wsClient) closeSendChannel() {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.sendClosed {
		return
	}
	c.sendClosed = true
	close(c.send)
}

func (c *wsClient) trySend(message outboundMessage) bool {
	c.sendMu.RLock()
	defer c.sendMu.RUnlock()
	if c.sendClosed {
		return false
	}
	select {
	case c.send <- message:
		return true
	default:
		return false
	}
}

func (c *wsClient) sendWithWait(message outboundMessage, wait time.Duration) bool {
	c.sendMu.RLock()
	defer c.sendMu.RUnlock()
	if c.sendClosed {
		return false
	}
	if wait <= 0 {
		select {
		case c.send <- message:
			return true
		default:
			return false
		}
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case c.send <- message:
		return true
	case <-timer.C:
		return false
	}
}

// stopGitStatusPoll stops any active git status polling for this client
func (c *wsClient) stopGitStatusPoll() {
	c.gitStatusMu.Lock()
	defer c.gitStatusMu.Unlock()

	if c.gitStatusTicker != nil {
		c.gitStatusTicker.Stop()
		c.gitStatusTicker = nil
	}
	if c.gitStatusStop != nil {
		close(c.gitStatusStop)
		c.gitStatusStop = nil
	}
	c.gitStatusDir = ""
	c.gitStatusHash = ""
}

// BroadcastListener is called for each broadcast event (for testing)
type BroadcastListener func(event *protocol.WebSocketEvent)

type messageKind int

const (
	messageKindText messageKind = iota
)

type outboundMessage struct {
	kind    messageKind
	payload []byte
}

// wsHub manages all WebSocket connections
type wsHub struct {
	clients           map[*wsClient]bool
	broadcast         chan outboundMessage
	register          chan *wsClient
	unregister        chan *wsClient
	mu                sync.RWMutex
	logf              func(format string, args ...interface{})
	broadcastListener BroadcastListener // Optional listener for testing
}

const (
	maxSlowCount      = 3 // disconnect after this many consecutive failed sends
	maxPTYDimValue    = 65535
	ptyOutputSendWait = 1 * time.Second
)

func newWSHub() *wsHub {
	return &wsHub{
		clients:    make(map[*wsClient]bool),
		broadcast:  make(chan outboundMessage, 256),
		register:   make(chan *wsClient),
		unregister: make(chan *wsClient),
		logf:       func(format string, args ...interface{}) {}, // no-op by default
	}
}

func previewBinaryForLog(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	const maxPreview = 32
	preview := string(data)
	if len(preview) > maxPreview {
		preview = preview[:maxPreview]
	}
	preview = strings.ReplaceAll(preview, "\n", "\\n")
	preview = strings.ReplaceAll(preview, "\r", "\\r")
	preview = strings.ReplaceAll(preview, "\t", "\\t")
	return preview
}

func (h *wsHub) run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				client.closeSendChannel()
				// Cleanup git status subscription
				client.stopGitStatusPoll()
			}
			h.mu.Unlock()

		case message := <-h.broadcast:
			h.mu.Lock()
			var toRemove []*wsClient
			for client := range h.clients {
				if client.trySend(message) {
					client.slowCount = 0 // reset on successful send
				} else {
					// Client buffer full
					client.slowCount++
					if client.slowCount >= maxSlowCount {
						h.logf("WebSocket client too slow (%d missed), disconnecting", client.slowCount)
						toRemove = append(toRemove, client)
					} else {
						h.logf("WebSocket client slow (%d/%d missed)", client.slowCount, maxSlowCount)
					}
				}
			}
			// Remove slow clients outside the iteration
			for _, client := range toRemove {
				delete(h.clients, client)
				client.closeSendChannel()
			}
			h.mu.Unlock()
		}
	}
}

// Broadcast sends an event to all connected clients
func (h *wsHub) Broadcast(event *protocol.WebSocketEvent) {
	// Call listener if set (for testing)
	if h.broadcastListener != nil {
		h.broadcastListener(event)
	}

	data, err := json.Marshal(event)
	if err != nil {
		h.logf("WebSocket broadcast marshal error: %v", err)
		return
	}
	message := outboundMessage{kind: messageKindText, payload: data}
	select {
	case h.broadcast <- message:
		// Message queued for broadcast
	default:
		// Broadcast channel full - this indicates the hub is overwhelmed
		h.logf("WebSocket broadcast channel full, dropping %s event", event.Event)
	}
}

// ClientCount returns number of connected clients
func (h *wsHub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

func isAllowedWSOrigin(origin string) bool {
	if origin == "" {
		// Non-browser clients/tests may omit Origin.
		return true
	}
	allowedPrefixes := []string{
		"tauri://localhost",
		"http://tauri.localhost",
		"http://localhost",
		"http://127.0.0.1",
		"https://localhost",
		"https://127.0.0.1",
		"localhost:",
		"127.0.0.1:",
		"tauri.localhost",
	}
	for _, prefix := range allowedPrefixes {
		if strings.HasPrefix(origin, prefix) {
			return true
		}
	}
	return false
}

// handleWS handles WebSocket connections
func (d *Daemon) handleWS(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if !isAllowedWSOrigin(origin) {
		d.logf("WebSocket rejected origin: %s", origin)
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{
			"localhost",
			"localhost:*",
			"127.0.0.1",
			"127.0.0.1:*",
			"tauri.localhost",
			"tauri.localhost:*",
		},
	})
	if err != nil {
		d.logf("WebSocket accept error: %v", err)
		return
	}

	client := &wsClient{
		conn:            conn,
		send:            make(chan outboundMessage, 256),
		recv:            make(chan []byte, 256), // buffer for incoming messages
		attachedStreams: make(map[string]ptybackend.Stream),
	}

	d.wsHub.register <- client
	d.logf("WebSocket client connected (%d total)", d.wsHub.ClientCount())

	// Send initial state unless recovery barrier is active.
	d.scheduleInitialState(client)

	// Start ping keepalive (detects dead connections, keeps proxies happy)
	done := make(chan struct{})
	go d.wsPingLoop(client, done)

	// Handle client lifecycle
	go d.wsWritePump(client)
	go d.wsMsgPump(client) // NEW: message processing goroutine
	d.wsReadPump(client)

	// Signal ping loop to stop when read pump exits
	close(done)
}

func (d *Daemon) sendInitialState(client *wsClient) {
	event := &protocol.InitialStateMessage{
		Event:            protocol.EventInitialState,
		ProtocolVersion:  protocol.Ptr(protocol.ProtocolVersion),
		DaemonInstanceID: protocol.Ptr(d.daemonInstanceID),
		Sessions:         d.sessionsForBroadcast(d.store.List("")),
		Workspaces:       d.listWorkspaceSnapshots(d.store.List("")),
		Prs:              protocol.PRsToValues(d.store.ListPRs("")),
		Repos:            protocol.RepoStatesToValues(d.store.ListRepoStates()),
		Authors:          protocol.AuthorStatesToValues(d.store.ListAuthorStates()),
		Settings:         d.settingsWithAgentAvailability(),
		Warnings:         d.getWarnings(),
	}
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	_ = d.sendOutbound(client, outboundMessage{kind: messageKindText, payload: data})

	// Fetch details for all PRs in background (app launch)
	go d.fetchAllPRDetails()
}

func (d *Daemon) wsWritePump(client *wsClient) {
	defer func() {
		client.conn.Close(websocket.StatusNormalClosure, "")
	}()

	for message := range client.send {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		wsType := websocket.MessageText
		if message.kind != messageKindText {
			wsType = websocket.MessageBinary
		}
		err := client.conn.Write(ctx, wsType, message.payload)
		cancel()
		if err != nil {
			return
		}
	}
}

func (d *Daemon) sendOutbound(client *wsClient, message outboundMessage) bool {
	return client.trySend(message)
}

func (d *Daemon) sendOutboundBlocking(client *wsClient, message outboundMessage, wait time.Duration) bool {
	return client.sendWithWait(message, wait)
}

// wsMsgPump processes incoming messages in FIFO order
// This runs in a dedicated goroutine to avoid blocking the read loop
func (d *Daemon) wsMsgPump(client *wsClient) {
	for data := range client.recv {
		d.handleClientMessage(client, data)
	}
	d.logf("WebSocket message pump exited")
}

// wsPingLoop sends periodic pings to keep the connection alive and detect dead clients
func (d *Daemon) wsPingLoop(client *wsClient, done <-chan struct{}) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := client.conn.Ping(ctx)
			cancel()
			if err != nil {
				d.logf("WebSocket ping failed: %v", err)
				client.conn.Close(websocket.StatusGoingAway, "ping timeout")
				return
			}
		}
	}
}

func (d *Daemon) wsReadPump(client *wsClient) {
	defer func() {
		d.dropPendingInitialState(client)
		d.detachAllSessions(client)
		close(client.recv) // signal wsMsgPump to exit
		d.wsHub.unregister <- client
		client.conn.Close(websocket.StatusNormalClosure, "")
		d.logf("WebSocket client disconnected (%d remaining)", d.wsHub.ClientCount())
	}()

	for {
		// No read timeout - clients don't send data regularly.
		// Connection liveness is detected by ping loop.
		// If ping fails, it closes the connection which unblocks this Read().
		_, data, err := client.conn.Read(context.Background())
		if err != nil {
			d.logf("WebSocket read error: %v", err)
			return
		}

		// Enqueue for ordered processing. If the queue is saturated, close the
		// client rather than silently dropping commands.
		select {
		case client.recv <- data:
		default:
			d.logf("WebSocket client recv buffer full; closing client connection")
			_ = client.conn.Close(websocket.StatusPolicyViolation, "command buffer overflow")
			return
		}
	}
}

func (d *Daemon) handleClientMessage(client *wsClient, data []byte) {
	cmd, msg, err := protocol.ParseMessage(data)
	if err != nil {
		var peek struct {
			Cmd string `json:"cmd"`
		}
		_ = json.Unmarshal(data, &peek)
		d.logf("WebSocket parse error for cmd=%s: %v", peek.Cmd, err)
		d.sendCommandError(client, peek.Cmd, err.Error())
		return
	}
	if shouldLogWSCommand(cmd) {
		d.logf("WebSocket parsed cmd: %s", cmd)
	}
	if d.isRecovering() && blocksDuringRecovery(cmd) {
		d.sendCommandError(client, cmd, "daemon_recovering")
		return
	}

	switch cmd {
	case protocol.CmdApprovePR:
		d.handleApprovePRWS(client, msg.(*protocol.ApprovePRMessage))
	case protocol.CmdMergePR:
		d.handleMergePRWS(client, msg.(*protocol.MergePRMessage))
	case protocol.CmdMutePR:
		d.handleMutePRWS(msg.(*protocol.MutePRMessage))
	case protocol.CmdMuteRepo:
		d.handleMuteRepoWS(msg.(*protocol.MuteRepoMessage))
	case protocol.CmdMuteAuthor:
		d.handleMuteAuthorWS(msg.(*protocol.MuteAuthorMessage))
	case protocol.CmdRefreshPRs:
		d.handleRefreshPRsWS(client)
	case protocol.CmdFetchPRDetails:
		d.handleFetchPRDetailsWS(client, msg.(*protocol.FetchPRDetailsMessage))
	case protocol.CmdClearSessions:
		d.handleClearSessionsWS()
	case protocol.CmdClearWarnings:
		d.handleClearWarningsWS()
	case protocol.CmdSessionVisualized:
		d.handleSessionVisualizedWS(msg.(*protocol.SessionVisualizedMessage))
	case protocol.CmdPRVisited:
		d.handlePRVisitedWS(msg.(*protocol.PRVisitedMessage))
	case protocol.CmdListWorktrees:
		d.handleListWorktreesWS(client, msg.(*protocol.ListWorktreesMessage))
	case protocol.CmdCreateWorktree:
		d.handleCreateWorktreeWS(client, msg.(*protocol.CreateWorktreeMessage))
	case protocol.CmdDeleteWorktree:
		d.handleDeleteWorktreeWS(client, msg.(*protocol.DeleteWorktreeMessage))
	case protocol.CmdGetSettings:
		d.handleGetSettingsWS(client)
	case protocol.CmdSetSetting:
		d.handleSetSettingWS(client, msg.(*protocol.SetSettingMessage))
	case protocol.CmdUnregister:
		d.handleUnregisterWS(client, msg.(*protocol.UnregisterMessage))
	case protocol.CmdGetRecentLocations:
		d.handleGetRecentLocationsWS(client, msg.(*protocol.GetRecentLocationsMessage))
	case protocol.CmdListBranches:
		d.handleListBranchesWS(client, msg.(*protocol.ListBranchesMessage))
	case protocol.CmdDeleteBranch:
		d.handleDeleteBranchWS(client, msg.(*protocol.DeleteBranchMessage))
	case protocol.CmdSwitchBranch:
		d.handleSwitchBranchWS(client, msg.(*protocol.SwitchBranchMessage))
	case protocol.CmdCreateWorktreeFromBranch:
		d.handleCreateWorktreeFromBranchWS(client, msg.(*protocol.CreateWorktreeFromBranchMessage))
	case protocol.CmdCreateBranch:
		d.handleCreateBranchWS(client, msg.(*protocol.CreateBranchMessage))
	case protocol.CmdCheckDirty:
		d.handleCheckDirtyWS(client, msg.(*protocol.CheckDirtyMessage))
	case protocol.CmdStash:
		d.handleStashWS(client, msg.(*protocol.StashMessage))
	case protocol.CmdStashPop:
		d.handleStashPopWS(client, msg.(*protocol.StashPopMessage))
	case protocol.CmdCheckAttnStash:
		d.handleCheckAttnStashWS(client, msg.(*protocol.CheckAttnStashMessage))
	case protocol.CmdCommitWIP:
		d.handleCommitWIPWS(client, msg.(*protocol.CommitWIPMessage))
	case protocol.CmdGetDefaultBranch:
		d.handleGetDefaultBranchWS(client, msg.(*protocol.GetDefaultBranchMessage))
	case protocol.CmdFetchRemotes:
		d.handleFetchRemotesWS(client, msg.(*protocol.FetchRemotesMessage))
	case protocol.CmdListRemoteBranches:
		d.handleListRemoteBranchesWS(client, msg.(*protocol.ListRemoteBranchesMessage))
	case protocol.CmdEnsureRepo:
		d.handleEnsureRepoWS(client, msg.(*protocol.EnsureRepoMessage))
	case protocol.CmdSubscribeGitStatus:
		d.handleSubscribeGitStatus(client, msg.(*protocol.SubscribeGitStatusMessage))
	case protocol.CmdUnsubscribeGitStatus:
		d.handleUnsubscribeGitStatusWS(client)
	case protocol.CmdGetFileDiff:
		d.handleGetFileDiffWS(client, msg.(*protocol.GetFileDiffMessage))
	case protocol.CmdGetBranchDiffFiles:
		d.handleGetBranchDiffFilesWS(client, msg.(*protocol.GetBranchDiffFilesMessage))
	case protocol.CmdGetRepoInfo:
		d.handleGetRepoInfoWS(client, msg.(*protocol.GetRepoInfoMessage))
	case protocol.CmdGetReviewState:
		d.handleGetReviewState(client, msg.(*protocol.GetReviewStateMessage))
	case protocol.CmdStartReviewLoop:
		d.handleStartReviewLoopWS(client, msg.(*protocol.StartReviewLoopMessage))
	case protocol.CmdStopReviewLoop:
		d.handleStopReviewLoopWS(client, msg.(*protocol.StopReviewLoopMessage))
	case protocol.CmdGetReviewLoopState:
		d.handleGetReviewLoopStateWS(client, msg.(*protocol.GetReviewLoopStateMessage))
	case protocol.CmdGetReviewLoopRun:
		d.handleGetReviewLoopRunWS(client, msg.(*protocol.GetReviewLoopRunMessage))
	case protocol.CmdSetReviewLoopIterations:
		d.handleSetReviewLoopIterationsWS(client, msg.(*protocol.SetReviewLoopIterationLimitMessage))
	case protocol.CmdAnswerReviewLoop:
		d.handleAnswerReviewLoopWS(client, msg.(*protocol.AnswerReviewLoopMessage))
	case protocol.CmdMarkFileViewed:
		d.handleMarkFileViewed(client, msg.(*protocol.MarkFileViewedMessage))
	case protocol.CmdAddComment:
		d.handleAddComment(client, msg.(*protocol.AddCommentMessage))
	case protocol.CmdUpdateComment:
		d.handleUpdateComment(client, msg.(*protocol.UpdateCommentMessage))
	case protocol.CmdResolveComment:
		d.handleResolveComment(client, msg.(*protocol.ResolveCommentMessage))
	case protocol.CmdWontFixComment:
		d.handleWontFixComment(client, msg.(*protocol.WontFixCommentMessage))
	case protocol.CmdDeleteComment:
		d.handleDeleteComment(client, msg.(*protocol.DeleteCommentMessage))
	case protocol.CmdGetComments:
		d.handleGetComments(client, msg.(*protocol.GetCommentsMessage))
	case protocol.CmdSpawnSession:
		d.handleSpawnSession(client, msg.(*protocol.SpawnSessionMessage))
	case protocol.CmdAttachSession:
		d.handleAttachSession(client, msg.(*protocol.AttachSessionMessage))
	case protocol.CmdDetachSession:
		d.handleDetachSessionWS(client, msg.(*protocol.DetachSessionMessage))
	case protocol.CmdPtyInput:
		d.handlePtyInput(client, msg.(*protocol.PtyInputMessage))
	case protocol.CmdPtyResize:
		d.handlePtyResize(client, msg.(*protocol.PtyResizeMessage))
	case protocol.CmdKillSession:
		d.handleKillSession(client, msg.(*protocol.KillSessionMessage))
	case protocol.CmdWorkspaceGet:
		d.handleWorkspaceGet(client, msg.(*protocol.WorkspaceGetMessage))
	case protocol.CmdWorkspaceSplitPane:
		d.handleWorkspaceSplitPane(client, msg.(*protocol.WorkspaceSplitPaneMessage))
	case protocol.CmdWorkspaceClosePane:
		d.handleWorkspaceClosePane(client, msg.(*protocol.WorkspaceClosePaneMessage))
	case protocol.CmdWorkspaceFocusPane:
		d.handleWorkspaceFocusPane(client, msg.(*protocol.WorkspaceFocusPaneMessage))
	case protocol.CmdWorkspaceRenamePane:
		d.handleWorkspaceRenamePane(client, msg.(*protocol.WorkspaceRenamePaneMessage))
	default:
		d.sendCommandError(client, cmd, "unsupported command")
	}
}

func (d *Daemon) sendCommandError(client *wsClient, cmd, errMsg string) {
	event := &protocol.WebSocketEvent{
		Event:   protocol.EventCommandError,
		Cmd:     protocol.Ptr(cmd),
		Success: protocol.Ptr(false),
		Error:   protocol.Ptr(errMsg),
	}
	d.sendToClient(client, event)
}

func (d *Daemon) sendToClient(client *wsClient, message interface{}) {
	data, err := json.Marshal(message)
	if err != nil {
		return
	}
	_ = d.sendOutbound(client, outboundMessage{
		kind:    messageKindText,
		payload: data,
	})
}
