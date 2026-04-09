package daemon

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/config"
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
	attachedRemote  map[string]struct{}          // remote runtime IDs attached for this client
	pendingRemote   map[string]struct{}          // remote runtime IDs awaiting attach_result
	attachMu        sync.Mutex

	// Git status subscription state
	gitStatusDir        string
	gitStatusTicker     *time.Ticker
	gitStatusStop       chan struct{}
	gitStatusHash       string // hash of last sent status for dedup
	gitStatusEndpointID string
	gitStatusMu         sync.Mutex
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
	c.gitStatusEndpointID = ""
}

func (c *wsClient) setGitStatusEndpointID(endpointID string) {
	c.gitStatusMu.Lock()
	defer c.gitStatusMu.Unlock()
	c.gitStatusEndpointID = strings.TrimSpace(endpointID)
}

func (c *wsClient) gitStatusEndpointIDValue() string {
	c.gitStatusMu.Lock()
	defer c.gitStatusMu.Unlock()
	return c.gitStatusEndpointID
}

func (c *wsClient) notePendingRemoteAttach(sessionID string) {
	if c == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	c.attachMu.Lock()
	defer c.attachMu.Unlock()
	if c.pendingRemote == nil {
		c.pendingRemote = make(map[string]struct{})
	}
	c.pendingRemote[sessionID] = struct{}{}
}

func (c *wsClient) resolvePendingRemoteAttach(sessionID string, success bool) bool {
	if c == nil || strings.TrimSpace(sessionID) == "" {
		return false
	}
	c.attachMu.Lock()
	defer c.attachMu.Unlock()
	if c.pendingRemote == nil {
		return false
	}
	if _, ok := c.pendingRemote[sessionID]; !ok {
		return false
	}
	delete(c.pendingRemote, sessionID)
	if success {
		if c.attachedRemote == nil {
			c.attachedRemote = make(map[string]struct{})
		}
		c.attachedRemote[sessionID] = struct{}{}
	} else if c.attachedRemote != nil {
		delete(c.attachedRemote, sessionID)
	}
	return true
}

func (c *wsClient) hasRemoteAttach(sessionID string) bool {
	if c == nil || strings.TrimSpace(sessionID) == "" {
		return false
	}
	c.attachMu.Lock()
	defer c.attachMu.Unlock()
	if c.attachedRemote == nil {
		return false
	}
	_, ok := c.attachedRemote[sessionID]
	return ok
}

func (c *wsClient) clearRemoteAttach(sessionID string) {
	if c == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	c.attachMu.Lock()
	defer c.attachMu.Unlock()
	if c.pendingRemote != nil {
		delete(c.pendingRemote, sessionID)
	}
	if c.attachedRemote != nil {
		delete(c.attachedRemote, sessionID)
	}
}

func (c *wsClient) clearAllRemoteAttaches() {
	if c == nil {
		return
	}
	c.attachMu.Lock()
	defer c.attachMu.Unlock()
	c.pendingRemote = make(map[string]struct{})
	c.attachedRemote = make(map[string]struct{})
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

	h.broadcastValue(event)
}

func (h *wsHub) BroadcastValue(message interface{}) {
	h.broadcastValue(message)
}

func (h *wsHub) BroadcastRawText(payload []byte) {
	h.SendRawTextToMatchingClients(payload, nil)
}

func (h *wsHub) SendRawTextToMatchingClients(payload []byte, match func(*wsClient) bool) {
	if len(payload) == 0 {
		return
	}
	cloned := append([]byte(nil), payload...)
	message := outboundMessage{kind: messageKindText, payload: cloned}

	h.mu.Lock()
	var toRemove []*wsClient
	for client := range h.clients {
		if match != nil && !match(client) {
			continue
		}
		if client.trySend(message) {
			client.slowCount = 0
			continue
		}
		client.slowCount++
		if client.slowCount >= maxSlowCount {
			h.logf("WebSocket client too slow (%d missed), disconnecting", client.slowCount)
			toRemove = append(toRemove, client)
		} else {
			h.logf("WebSocket client slow (%d/%d missed)", client.slowCount, maxSlowCount)
		}
	}
	for _, client := range toRemove {
		delete(h.clients, client)
		client.closeSendChannel()
	}
	h.mu.Unlock()
}

func (h *wsHub) ForEachClient(fn func(*wsClient)) {
	if fn == nil {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for client := range h.clients {
		fn(client)
	}
}

func (h *wsHub) broadcastValue(message interface{}) {
	data, err := json.Marshal(message)
	if err != nil {
		h.logf("WebSocket broadcast marshal error: %v", err)
		return
	}
	out := outboundMessage{kind: messageKindText, payload: data}
	select {
	case h.broadcast <- out:
		// Message queued for broadcast
	default:
		// Broadcast channel full - this indicates the hub is overwhelmed
		h.logf("WebSocket broadcast channel full, dropping outbound message")
	}
}

// ClientCount returns number of connected clients
func (h *wsHub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

func isAllowedWSOrigin(origin string, requestHost string) bool {
	if origin == "" {
		// Non-browser clients/tests may omit Origin.
		return true
	}
	if isAllowedLocalOrigin(origin) {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return normalizeWSHost(parsed.Host) != "" && normalizeWSHost(parsed.Host) == normalizeWSHost(requestHost)
}

func isAllowedLocalOrigin(origin string) bool {
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

func normalizeWSHost(value string) string {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(trimmed); err == nil {
		return host
	}
	return trimmed
}

func websocketOriginPatternsForRequest(r *http.Request) []string {
	patterns := []string{
		"localhost",
		"localhost:*",
		"127.0.0.1",
		"127.0.0.1:*",
		"tauri.localhost",
		"tauri.localhost:*",
	}
	host := normalizeWSHost(r.Host)
	if host != "" && host != "localhost" && host != "127.0.0.1" && host != "tauri.localhost" {
		patterns = append(patterns, host)
	}
	return patterns
}

// handleWS handles WebSocket connections
func (d *Daemon) handleWS(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if !isAllowedWSOrigin(origin, r.Host) {
		d.logf("WebSocket rejected origin: %s", origin)
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	if required := config.WSAuthToken(); required != "" {
		provided := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if provided == "" {
			provided = strings.TrimSpace(r.URL.Query().Get("token"))
		}
		if subtle.ConstantTimeCompare([]byte(required), []byte(provided)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: websocketOriginPatternsForRequest(r),
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
		attachedRemote:  make(map[string]struct{}),
		pendingRemote:   make(map[string]struct{}),
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
		Sessions:         d.mergedSessionsForBroadcast(),
		Endpoints:        d.listEndpointInfos(),
		Workspaces:       d.mergedWorkspacesForBroadcast(),
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
		d.cleanupRemoteGitStatusSubscription(client)
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

func (d *Daemon) cleanupRemoteGitStatusSubscription(client *wsClient) {
	if d.hubManager == nil || client == nil {
		return
	}
	endpointID := client.gitStatusEndpointIDValue()
	if endpointID == "" {
		return
	}
	client.setGitStatusEndpointID("")
	payload, err := json.Marshal(protocol.UnsubscribeGitStatusMessage{Cmd: protocol.CmdUnsubscribeGitStatus})
	if err != nil {
		return
	}
	if err := d.hubManager.ForwardEndpointCommand(context.Background(), endpointID, payload); err != nil {
		d.logf("remote git-status unsubscribe failed for endpoint %s: %v", endpointID, err)
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
	if d.tryHandleRemoteWSCommand(client, cmd, msg, data) {
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
	case protocol.CmdAddEndpoint:
		d.handleAddEndpointWS(client, msg.(*protocol.AddEndpointMessage))
	case protocol.CmdRemoveEndpoint:
		d.handleRemoveEndpointWS(client, msg.(*protocol.RemoveEndpointMessage))
	case protocol.CmdUpdateEndpoint:
		d.handleUpdateEndpointWS(client, msg.(*protocol.UpdateEndpointMessage))
	case protocol.CmdListEndpoints:
		d.handleListEndpointsWS(client)
	case protocol.CmdSetEndpointRemoteWeb:
		d.handleSetEndpointRemoteWebWS(client, msg.(*protocol.SetEndpointRemoteWebMessage))
	case protocol.CmdUnregister:
		d.handleUnregisterWS(client, msg.(*protocol.UnregisterMessage))
	case protocol.CmdGetRecentLocations:
		d.handleGetRecentLocationsWS(client, msg.(*protocol.GetRecentLocationsMessage))
	case protocol.CmdBrowseDirectory:
		d.handleBrowseDirectoryWS(client, msg.(*protocol.BrowseDirectoryMessage))
	case protocol.CmdInspectPath:
		d.handleInspectPathWS(client, msg.(*protocol.InspectPathMessage))
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

func (d *Daemon) tryHandleRemoteWSCommand(client *wsClient, cmd string, msg interface{}, raw []byte) bool {
	if d.hubManager == nil {
		return false
	}

	if endpointID := remoteCommandEndpointID(cmd, msg); endpointID != "" {
		if d.hubManager.HasEndpoint(endpointID) {
			if cmd == protocol.CmdSpawnSession {
				if typed, ok := msg.(*protocol.SpawnSessionMessage); ok {
					d.hubManager.ReservePendingSessionRoute(endpointID, typed.ID)
				}
			}
			if err := d.hubManager.ForwardEndpointCommand(context.Background(), endpointID, raw); err != nil {
				d.sendCommandError(client, cmd, err.Error())
				return true
			}
			return true
		}
		if d.hubManager.HasConfiguredEndpoints() {
			d.sendCommandError(client, cmd, fmt.Sprintf("endpoint not found: %s", endpointID))
			return true
		}
	}

	if ptyTargetID := remoteCommandPTYTargetID(cmd, msg); ptyTargetID != "" {
		if _, ok := d.hubManager.EndpointIDForPTYTarget(ptyTargetID); !ok {
			return false
		}
		switch cmd {
		case protocol.CmdAttachSession:
			client.notePendingRemoteAttach(ptyTargetID)
		case protocol.CmdDetachSession:
			client.clearRemoteAttach(ptyTargetID)
		}
		if err := d.hubManager.ForwardPTYCommand(context.Background(), ptyTargetID, raw); err != nil {
			if cmd == protocol.CmdAttachSession {
				client.clearRemoteAttach(ptyTargetID)
			}
			d.sendCommandError(client, cmd, err.Error())
			return true
		}
		return true
	}

	sessionID := remoteCommandSessionID(cmd, msg)
	if sessionID == "" {
		if cmd == protocol.CmdUnsubscribeGitStatus {
			endpointID := client.gitStatusEndpointIDValue()
			if endpointID == "" {
				return false
			}
			client.setGitStatusEndpointID("")
			if err := d.hubManager.ForwardEndpointCommand(context.Background(), endpointID, raw); err != nil {
				d.sendCommandError(client, cmd, err.Error())
			}
			return true
		}

		endpointID, ok := remoteCommandScopedEndpointID(msg, d.hubManager)
		if !ok {
			return false
		}
		if err := d.hubManager.ForwardEndpointCommand(context.Background(), endpointID, raw); err != nil {
			d.sendCommandError(client, cmd, err.Error())
			return true
		}
		if cmd == protocol.CmdSubscribeGitStatus {
			client.setGitStatusEndpointID(endpointID)
		}
		return true
	}
	endpointID, ok := d.hubManager.EndpointIDForSession(sessionID)
	if !ok {
		return false
	}
	if err := d.hubManager.ForwardEndpointCommand(context.Background(), endpointID, raw); err != nil {
		d.sendCommandError(client, cmd, err.Error())
		return true
	}
	return true
}

func remoteCommandSessionID(cmd string, msg interface{}) string {
	switch cmd {
	case protocol.CmdSessionVisualized:
		if typed, ok := msg.(*protocol.SessionVisualizedMessage); ok {
			return typed.ID
		}
	case protocol.CmdStartReviewLoop:
		if typed, ok := msg.(*protocol.StartReviewLoopMessage); ok {
			return typed.SessionID
		}
	case protocol.CmdStopReviewLoop:
		if typed, ok := msg.(*protocol.StopReviewLoopMessage); ok {
			return typed.SessionID
		}
	case protocol.CmdGetReviewLoopState:
		if typed, ok := msg.(*protocol.GetReviewLoopStateMessage); ok {
			return typed.SessionID
		}
	case protocol.CmdSetReviewLoopIterations:
		if typed, ok := msg.(*protocol.SetReviewLoopIterationLimitMessage); ok {
			return typed.SessionID
		}
	case protocol.CmdWorkspaceGet:
		if typed, ok := msg.(*protocol.WorkspaceGetMessage); ok {
			return typed.SessionID
		}
	case protocol.CmdWorkspaceSplitPane:
		if typed, ok := msg.(*protocol.WorkspaceSplitPaneMessage); ok {
			return typed.SessionID
		}
	case protocol.CmdWorkspaceClosePane:
		if typed, ok := msg.(*protocol.WorkspaceClosePaneMessage); ok {
			return typed.SessionID
		}
	case protocol.CmdWorkspaceFocusPane:
		if typed, ok := msg.(*protocol.WorkspaceFocusPaneMessage); ok {
			return typed.SessionID
		}
	case protocol.CmdWorkspaceRenamePane:
		if typed, ok := msg.(*protocol.WorkspaceRenamePaneMessage); ok {
			return typed.SessionID
		}
	}
	return ""
}

func remoteCommandEndpointID(cmd string, msg interface{}) string {
	switch cmd {
	case protocol.CmdGetRecentLocations:
		if typed, ok := msg.(*protocol.GetRecentLocationsMessage); ok {
			return strings.TrimSpace(protocol.Deref(typed.EndpointID))
		}
	case protocol.CmdBrowseDirectory:
		if typed, ok := msg.(*protocol.BrowseDirectoryMessage); ok {
			return strings.TrimSpace(protocol.Deref(typed.EndpointID))
		}
	case protocol.CmdInspectPath:
		if typed, ok := msg.(*protocol.InspectPathMessage); ok {
			return strings.TrimSpace(protocol.Deref(typed.EndpointID))
		}
	case protocol.CmdSpawnSession:
		if typed, ok := msg.(*protocol.SpawnSessionMessage); ok {
			return strings.TrimSpace(protocol.Deref(typed.EndpointID))
		}
	case protocol.CmdCreateWorktree:
		if typed, ok := msg.(*protocol.CreateWorktreeMessage); ok {
			return strings.TrimSpace(protocol.Deref(typed.EndpointID))
		}
	case protocol.CmdDeleteWorktree:
		if typed, ok := msg.(*protocol.DeleteWorktreeMessage); ok {
			return strings.TrimSpace(protocol.Deref(typed.EndpointID))
		}
	case protocol.CmdDeleteBranch:
		if typed, ok := msg.(*protocol.DeleteBranchMessage); ok {
			return strings.TrimSpace(protocol.Deref(typed.EndpointID))
		}
	case protocol.CmdGetRepoInfo:
		if typed, ok := msg.(*protocol.GetRepoInfoMessage); ok {
			return strings.TrimSpace(protocol.Deref(typed.EndpointID))
		}
	}
	return ""
}

func remoteCommandPTYTargetID(cmd string, msg interface{}) string {
	switch cmd {
	case protocol.CmdSpawnSession:
	case protocol.CmdAttachSession:
		if typed, ok := msg.(*protocol.AttachSessionMessage); ok {
			return typed.ID
		}
	case protocol.CmdDetachSession:
		if typed, ok := msg.(*protocol.DetachSessionMessage); ok {
			return typed.ID
		}
	case protocol.CmdPtyInput:
		if typed, ok := msg.(*protocol.PtyInputMessage); ok {
			return typed.ID
		}
	case protocol.CmdPtyResize:
		if typed, ok := msg.(*protocol.PtyResizeMessage); ok {
			return typed.ID
		}
	case protocol.CmdKillSession:
		if typed, ok := msg.(*protocol.KillSessionMessage); ok {
			return typed.ID
		}
	}
	return ""
}

func remoteCommandScopedEndpointID(msg interface{}, manager interface {
	EndpointIDForPath(path string) (string, bool)
	EndpointIDForReview(reviewID string) (string, bool)
	EndpointIDForComment(commentID string) (string, bool)
	EndpointIDForReviewLoop(loopID string) (string, bool)
}) (string, bool) {
	if manager == nil {
		return "", false
	}
	if path := remoteCommandPath(msg); path != "" {
		if endpointID, ok := manager.EndpointIDForPath(path); ok {
			return endpointID, true
		}
	}
	if reviewID := remoteCommandReviewID(msg); reviewID != "" {
		if endpointID, ok := manager.EndpointIDForReview(reviewID); ok {
			return endpointID, true
		}
	}
	if commentID := remoteCommandCommentID(msg); commentID != "" {
		if endpointID, ok := manager.EndpointIDForComment(commentID); ok {
			return endpointID, true
		}
	}
	if loopID := remoteCommandReviewLoopID(msg); loopID != "" {
		if endpointID, ok := manager.EndpointIDForReviewLoop(loopID); ok {
			return endpointID, true
		}
	}
	return "", false
}

func remoteCommandPath(msg interface{}) string {
	switch typed := msg.(type) {
	case *protocol.ListWorktreesMessage:
		return typed.MainRepo
	case *protocol.CreateWorktreeMessage:
		return typed.MainRepo
	case *protocol.DeleteWorktreeMessage:
		return typed.Path
	case *protocol.ListBranchesMessage:
		return typed.MainRepo
	case *protocol.DeleteBranchMessage:
		return typed.MainRepo
	case *protocol.SwitchBranchMessage:
		return typed.MainRepo
	case *protocol.CreateWorktreeFromBranchMessage:
		return typed.MainRepo
	case *protocol.CreateBranchMessage:
		return typed.MainRepo
	case *protocol.CheckDirtyMessage:
		return typed.Repo
	case *protocol.StashMessage:
		return typed.Repo
	case *protocol.StashPopMessage:
		return typed.Repo
	case *protocol.CheckAttnStashMessage:
		return typed.Repo
	case *protocol.CommitWIPMessage:
		return typed.Repo
	case *protocol.GetDefaultBranchMessage:
		return typed.Repo
	case *protocol.FetchRemotesMessage:
		return typed.Repo
	case *protocol.ListRemoteBranchesMessage:
		return typed.Repo
	case *protocol.EnsureRepoMessage:
		return typed.TargetPath
	case *protocol.SubscribeGitStatusMessage:
		return typed.Directory
	case *protocol.GetFileDiffMessage:
		return typed.Directory
	case *protocol.GetBranchDiffFilesMessage:
		return typed.Directory
	case *protocol.GetRepoInfoMessage:
		return typed.Repo
	case *protocol.GetReviewStateMessage:
		return typed.RepoPath
	}
	return ""
}

func remoteCommandReviewID(msg interface{}) string {
	switch typed := msg.(type) {
	case *protocol.MarkFileViewedMessage:
		return typed.ReviewID
	case *protocol.AddCommentMessage:
		return typed.ReviewID
	case *protocol.GetCommentsMessage:
		return typed.ReviewID
	}
	return ""
}

func remoteCommandCommentID(msg interface{}) string {
	switch typed := msg.(type) {
	case *protocol.UpdateCommentMessage:
		return typed.CommentID
	case *protocol.ResolveCommentMessage:
		return typed.CommentID
	case *protocol.WontFixCommentMessage:
		return typed.CommentID
	case *protocol.DeleteCommentMessage:
		return typed.CommentID
	}
	return ""
}

func remoteCommandReviewLoopID(msg interface{}) string {
	switch typed := msg.(type) {
	case *protocol.GetReviewLoopRunMessage:
		return typed.LoopID
	case *protocol.AnswerReviewLoopMessage:
		return typed.LoopID
	}
	return ""
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

func (d *Daemon) broadcastMessage(message interface{}) {
	if d.wsHub == nil {
		return
	}
	d.wsHub.BroadcastValue(message)
}

func (d *Daemon) broadcastRawWSMessage(payload []byte) {
	if d.wsHub == nil {
		return
	}
	var envelope struct {
		Event   string `json:"event"`
		ID      string `json:"id"`
		Success bool   `json:"success"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		d.wsHub.BroadcastRawText(payload)
		return
	}

	switch envelope.Event {
	case protocol.EventAttachResult:
		if strings.TrimSpace(envelope.ID) == "" {
			d.wsHub.BroadcastRawText(payload)
			return
		}
		d.wsHub.SendRawTextToMatchingClients(payload, func(client *wsClient) bool {
			return client.resolvePendingRemoteAttach(envelope.ID, envelope.Success)
		})
		return
	case protocol.EventPtyOutput, protocol.EventPtyDesync:
		if strings.TrimSpace(envelope.ID) == "" {
			d.wsHub.BroadcastRawText(payload)
			return
		}
		d.wsHub.SendRawTextToMatchingClients(payload, func(client *wsClient) bool {
			return client.hasRemoteAttach(envelope.ID)
		})
		return
	case protocol.EventSessionExited:
		if strings.TrimSpace(envelope.ID) != "" {
			d.wsHub.ForEachClient(func(client *wsClient) {
				client.clearRemoteAttach(envelope.ID)
			})
		}
	}

	d.wsHub.BroadcastRawText(payload)
}
