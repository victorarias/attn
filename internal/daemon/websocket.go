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

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/workspacelayout"
)

// wsClient represents a connected WebSocket client
type wsClient struct {
	conn        *websocket.Conn
	send        chan outboundMessage
	recv        chan []byte // incoming messages for ordered processing
	slowCount   int         // tracks consecutive failed sends
	sendMu      sync.RWMutex
	sendClosed  bool
	closeCode   websocket.StatusCode
	closeReason string
	connectedAt time.Time

	// Browser-host eligibility requires both the expected Tauri origin and the
	// per-profile secret delivered only to the trusted main webview.
	trustedTauriOrigin       bool
	browserHostAuthenticated bool

	// PTY subscriptions keyed by session ID
	attachedStreams map[string]ptybackend.Stream // session -> stream
	attachedRemote  map[string]struct{}          // remote runtime IDs attached for this client
	pendingRemote   map[string]struct{}          // remote runtime IDs awaiting attach_result
	attachMu        sync.Mutex

	// Docked tile content subscriptions keyed by workspace + tile ID.
	tileContentSubscriptions map[string]struct{}
	tileContentPending       map[string]time.Time
	tileContentMu            sync.RWMutex

	// Identity + capabilities declared via client_hello.
	clientKind    string
	clientVersion string
	capabilities  map[string]struct{}
	identityMu    sync.RWMutex

	// Git status subscription state
	gitStatusDir        string
	gitStatusStop       chan struct{}
	gitStatusRefresh    chan gitStatusRefreshRequest
	gitStatusHash       string // hash of last sent status for dedup
	gitStatusEndpointID string
	gitStatusMu         sync.Mutex
}

// HasCapability reports whether the client advertised the given
// capability via client_hello. False for clients that never sent hello.
// Capability strings are arbitrary; see protocol.Capability* constants.
func (c *wsClient) HasCapability(cap string) bool {
	c.identityMu.RLock()
	defer c.identityMu.RUnlock()
	_, ok := c.capabilities[cap]
	return ok
}

func (c *wsClient) IsBrowserHost() bool {
	c.identityMu.RLock()
	defer c.identityMu.RUnlock()
	_, capable := c.capabilities[protocol.CapabilityBrowserHost]
	return c.trustedTauriOrigin &&
		c.browserHostAuthenticated &&
		c.clientKind == "tauri-app" &&
		capable
}

func (c *wsClient) setBrowserHostAuthenticated(authenticated bool) {
	c.identityMu.Lock()
	defer c.identityMu.Unlock()
	c.browserHostAuthenticated = authenticated
}

// isTrustedAppClient reports whether this connection is the authenticated attn
// app itself: trusted Tauri origin, per-profile browser-host secret verified via
// client_hello, and the tauri-app client kind. It is IsBrowserHost minus the
// browser-host capability — identity, not feature opt-in. Arbitrary fs roots
// are gated on it: without this, any accepted local WebSocket client could use
// fs_* {root} to read or overwrite files anywhere in the user's home.
func (c *wsClient) isTrustedAppClient() bool {
	c.identityMu.RLock()
	defer c.identityMu.RUnlock()
	return c.trustedTauriOrigin && c.browserHostAuthenticated && c.clientKind == "tauri-app"
}

func websocketReadLimit(client *wsClient) int64 {
	if client.IsBrowserHost() {
		return maxBrowserHostWebSocketReadBytes
	}
	return defaultWebSocketReadBytes
}

func (c *wsClient) updateReadLimit() {
	if c.conn != nil {
		c.conn.SetReadLimit(websocketReadLimit(c))
	}
}

func (c *wsClient) speaksWorkspaceProtocol() bool {
	return c.HasCapability(protocol.CapabilityWorkspaceSessions)
}

// setIdentity records the hello payload on the client. Idempotent —
// later hellos overwrite earlier ones, which is the right behavior if a
// client ever wants to re-declare (no current case, but cheap).
func (c *wsClient) setIdentity(kind, version string, caps []string) {
	c.identityMu.Lock()
	defer c.identityMu.Unlock()
	c.clientKind = kind
	c.clientVersion = version
	c.capabilities = make(map[string]struct{}, len(caps))
	for _, cap := range caps {
		c.capabilities[cap] = struct{}{}
	}
}

func (c *wsClient) closeSendChannel() {
	c.closeSendChannelWithStatus(websocket.StatusNormalClosure, "")
}

func (c *wsClient) closeSendChannelWithStatus(code websocket.StatusCode, reason string) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.closeCode == 0 {
		c.closeCode = code
		c.closeReason = reason
	}
	if c.sendClosed {
		return
	}
	c.sendClosed = true
	close(c.send)
}

func (c *wsClient) closeStatus() (websocket.StatusCode, string) {
	c.sendMu.RLock()
	defer c.sendMu.RUnlock()
	if c.closeCode == 0 {
		return websocket.StatusNormalClosure, ""
	}
	return c.closeCode, c.closeReason
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

// stopGitStatusPoll stops any active git status subscription for this client.
func (c *wsClient) stopGitStatusPoll() {
	c.gitStatusMu.Lock()
	defer c.gitStatusMu.Unlock()

	if c.gitStatusStop != nil {
		close(c.gitStatusStop)
		c.gitStatusStop = nil
	}
	c.gitStatusRefresh = nil
	c.gitStatusDir = ""
	c.gitStatusHash = ""
	c.gitStatusEndpointID = ""
}

func (c *wsClient) requestGitStatusRefresh(req gitStatusRefreshRequest) bool {
	c.gitStatusMu.Lock()
	refresh := c.gitStatusRefresh
	c.gitStatusMu.Unlock()

	if refresh == nil {
		return false
	}
	select {
	case refresh <- req:
		return true
	default:
		return false
	}
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

func (c *wsClient) wantsRemoteAttachTraffic(sessionID string) bool {
	if c == nil || strings.TrimSpace(sessionID) == "" {
		return false
	}
	c.attachMu.Lock()
	defer c.attachMu.Unlock()
	if c.pendingRemote != nil {
		if _, ok := c.pendingRemote[sessionID]; ok {
			return true
		}
	}
	if c.attachedRemote != nil {
		if _, ok := c.attachedRemote[sessionID]; ok {
			return true
		}
	}
	return false
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

// BroadcastListener is called for each broadcast event (for testing)
type BroadcastListener func(event *protocol.WebSocketEvent)

type messageKind int

const (
	messageKindText messageKind = iota
	messageKindBinary
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
	maxSlowCount                     = 3 // disconnect after this many consecutive failed sends
	maxPTYDimValue                   = 65535
	defaultWebSocketReadBytes        = 1 << 20
	maxBrowserHostWebSocketReadBytes = 32 << 20
	ptyOutputSendWait                = 1 * time.Second
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
				client.closeSendChannelWithStatus(websocket.StatusPolicyViolation, "client too slow")
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

func (h *wsHub) SendValueToMatchingClients(message interface{}, match func(*wsClient) bool) {
	data, err := json.Marshal(message)
	if err != nil {
		h.logf("WebSocket targeted send marshal error: %v", err)
		return
	}
	h.SendRawTextToMatchingClients(data, match)
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
		client.closeSendChannelWithStatus(websocket.StatusPolicyViolation, "client too slow")
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

func (h *wsHub) AnyClientMatches(match func(*wsClient) bool) bool {
	if match == nil {
		return false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for client := range h.clients {
		if match(client) {
			return true
		}
	}
	return false
}

func (h *wsHub) NewestClientMatching(match func(*wsClient) bool) *wsClient {
	if match == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	var newest *wsClient
	for client := range h.clients {
		if !match(client) {
			continue
		}
		if newest == nil || client.connectedAt.After(newest.connectedAt) {
			newest = client
		}
	}
	return newest
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

func isTrustedTauriOrigin(origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := normalizeWSHost(parsed.Host)
	if (parsed.Scheme == "tauri" && host == "localhost") ||
		(parsed.Scheme == "http" && host == "tauri.localhost") {
		return true
	}
	return config.Profile() == "dev" &&
		parsed.Scheme == "http" &&
		strings.EqualFold(parsed.Hostname(), "localhost") &&
		parsed.Port() == "1420"
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
	// Keep unauthenticated and ordinary clients on a modest command-sized
	// budget. The authenticated browser host receives its larger capture budget
	// only after client_hello verifies the per-profile secret.
	conn.SetReadLimit(defaultWebSocketReadBytes)

	client := &wsClient{
		conn:               conn,
		send:               make(chan outboundMessage, 256),
		recv:               make(chan []byte, 256), // buffer for incoming messages
		connectedAt:        time.Now(),
		trustedTauriOrigin: isTrustedTauriOrigin(origin),
		attachedStreams:    make(map[string]ptybackend.Stream),
		attachedRemote:     make(map[string]struct{}),
		pendingRemote:      make(map[string]struct{}),
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
		Event:             protocol.EventInitialState,
		ProtocolVersion:   protocol.Ptr(protocol.ProtocolVersion),
		SourceFingerprint: protocol.Ptr(buildinfo.SourceFingerprint),
		DaemonInstanceID:  protocol.Ptr(d.daemonInstanceID),
		Sessions:          d.mergedSessionsForBroadcast(),
		Endpoints:         d.listEndpointInfos(),
		Workspaces:        d.listWorkspaces(),
		Prs:               protocol.PRsToValues(d.store.ListPRs("")),
		Repos:             protocol.RepoStatesToValues(d.store.ListRepoStates()),
		Authors:           protocol.AuthorStatesToValues(d.store.ListAuthorStates()),
		GithubHosts:       d.gitHubHosts(),
		Settings:          d.settingsWithAgentAvailability(),
		Warnings:          d.getWarnings(),
		Tickets:           d.ticketsForBroadcast(),
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
		code, reason := client.closeStatus()
		client.conn.Close(code, reason)
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
		d.dropFsWatchClient(client)
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
	if cmd != protocol.CmdClientHello && !client.speaksWorkspaceProtocol() {
		errMsg := fmt.Sprintf("client must send client_hello with %q capability", protocol.CapabilityWorkspaceSessions)
		d.logf("rejecting websocket command %s: %s", cmd, errMsg)
		d.sendCommandError(client, cmd, errMsg)
		if client.conn != nil {
			_ = client.conn.Close(websocket.StatusPolicyViolation, errMsg)
		}
		return
	}
	if d.isRecovering() && blocksDuringRecovery(cmd) {
		d.sendCommandError(client, cmd, "daemon_recovering")
		return
	}
	// Record the UI selection before remote routing. A host-side `attn open`
	// without --session must fail against the selected remote id rather than
	// silently reusing a stale local selection.
	if cmd == protocol.CmdSessionSelected {
		d.setSelectedSession(msg.(*protocol.SessionSelectedMessage).ID)
	}
	if cmd == protocol.CmdWorkspaceSelected {
		d.setSelectedWorkspace(msg.(*protocol.WorkspaceSelectedMessage).WorkspaceID)
	}
	// Websocket commands are UI-origin (unlike unix-socket CLI/agent commands),
	// so a UI-presence allowlist here is a proxy for "the user is at the app
	// right now" — surfaced on the ticket inbox result for watching agents.
	if isUserPresenceCommand(cmd) {
		d.recordUserActivity(time.Now())
	}
	if d.tryHandleRemoteWSCommand(client, cmd, msg, data) {
		return
	}

	switch cmd {
	case protocol.CmdClientHello:
		d.handleClientHello(client, msg.(*protocol.ClientHelloMessage))
	case protocol.CmdDelegate:
		go d.handleDelegateWS(client, msg.(*protocol.DelegateMessage))
	case protocol.CmdDelegateStatus:
		go d.handleDelegateStatusWS(client, msg.(*protocol.DelegateStatusMessage))
	case protocol.CmdWorkspaceContextCheckout:
		go func() {
			result, err := d.checkoutWorkspaceContext(msg.(*protocol.WorkspaceContextCheckoutMessage))
			d.sendWorkspaceContextWSResult(client, "checkout", result, err)
		}()
	case protocol.CmdWorkspaceContextUpdate:
		go func() {
			result, _, err := d.updateWorkspaceContext(msg.(*protocol.WorkspaceContextUpdateMessage))
			d.sendWorkspaceContextWSResult(client, "update", result, err)
		}()
	case protocol.CmdWorkspaceContextStatus:
		go func() {
			result, err := d.workspaceContextStatus(msg.(*protocol.WorkspaceContextStatusMessage))
			d.sendWorkspaceContextWSResult(client, "status", result, err)
		}()
	case protocol.CmdWorkspaceContextList:
		go d.sendWorkspaceContextListWSResult(client, msg.(*protocol.WorkspaceContextListMessage).RequestID)
	case protocol.CmdNotebookList:
		nbList := msg.(*protocol.NotebookListMessage)
		go d.sendNotebookListWSResult(client, protocol.Deref(nbList.RequestID), protocol.Deref(nbList.Prefix))
	case protocol.CmdNotebookRead:
		nbRead := msg.(*protocol.NotebookReadMessage)
		go d.sendNotebookReadWSResult(client, protocol.Deref(nbRead.RequestID), nbRead.Path)
	case protocol.CmdNotebookBacklinks:
		nbBack := msg.(*protocol.NotebookBacklinksMessage)
		go d.sendNotebookBacklinksWSResult(client, protocol.Deref(nbBack.RequestID), nbBack.Path)
	case protocol.CmdNotebookWrite:
		nbWrite := msg.(*protocol.NotebookWriteMessage)
		go d.sendNotebookWriteWSResult(client, protocol.Deref(nbWrite.RequestID), nbWrite.Path, nbWrite.Content, protocol.Deref(nbWrite.BaseHash))
	case protocol.CmdNotebookSendToChief:
		nbChief := msg.(*protocol.NotebookSendToChiefMessage)
		go d.sendNotebookToChiefWSResult(client, protocol.Deref(nbChief.RequestID), protocol.Deref(nbChief.SourcePath), nbChief.Selection)
	case protocol.CmdTaskList:
		nbTaskList := msg.(*protocol.TaskListMessage)
		go d.sendTaskListWSResult(client, protocol.Deref(nbTaskList.RequestID))
	case protocol.CmdTaskRetry:
		nbTaskRetry := msg.(*protocol.TaskRetryMessage)
		go d.sendTaskRetryWSResult(client, protocol.Deref(nbTaskRetry.RequestID), nbTaskRetry.TaskID)
	case protocol.CmdNotificationList:
		notifList := msg.(*protocol.NotificationListMessage)
		go d.sendNotificationListWSResult(client, protocol.Deref(notifList.RequestID))
	case protocol.CmdNotificationMarkRead:
		notifMark := msg.(*protocol.NotificationMarkReadMessage)
		go d.sendNotificationMarkReadWSResult(client, protocol.Deref(notifMark.RequestID), notifMark.NotificationID)
	case protocol.CmdGetTicket:
		getTicket := msg.(*protocol.GetTicketMessage)
		go d.sendGetTicketWSResult(client, protocol.Deref(getTicket.RequestID), getTicket.TicketID)
	case protocol.CmdTicketChangeStatus:
		go d.handleTicketChangeStatus(client, msg.(*protocol.TicketChangeStatusMessage))
	case protocol.CmdTicketAddComment:
		go d.handleTicketAddComment(client, msg.(*protocol.TicketAddCommentMessage))
	case protocol.CmdTicketEditDescription:
		go d.handleTicketEditDescription(client, msg.(*protocol.TicketEditDescriptionMessage))
	case protocol.CmdTicketAttach:
		go d.handleTicketAttachWS(client, msg.(*protocol.TicketAttachMessage))
	case protocol.CmdTicketResume:
		go d.handleTicketResume(client, msg.(*protocol.TicketResumeMessage))
	case protocol.CmdFsList:
		fsList := msg.(*protocol.FsListMessage)
		go d.sendFsListWSResult(client, protocol.Deref(fsList.RequestID), protocol.Deref(fsList.Path), protocol.Deref(fsList.Root))
	case protocol.CmdFsRead:
		fsRead := msg.(*protocol.FsReadMessage)
		go d.sendFsReadWSResult(client, protocol.Deref(fsRead.RequestID), fsRead.Path, protocol.Deref(fsRead.Root))
	case protocol.CmdFsReadAsset:
		fsReadAsset := msg.(*protocol.FsReadAssetMessage)
		go d.sendFsReadAssetWSResult(client, protocol.Deref(fsReadAsset.RequestID), fsReadAsset.Path, protocol.Deref(fsReadAsset.Root))
	case protocol.CmdFsWrite:
		fsWrite := msg.(*protocol.FsWriteMessage)
		go d.sendFsWriteWSResult(client, protocol.Deref(fsWrite.RequestID), fsWrite.Path, fsWrite.Content, protocol.Deref(fsWrite.BaseHash), protocol.Deref(fsWrite.Root))
	case protocol.CmdFsRename:
		fsRename := msg.(*protocol.FsRenameMessage)
		go d.sendFsRenameWSResult(client, protocol.Deref(fsRename.RequestID), fsRename.Path, fsRename.NewPath, protocol.Deref(fsRename.Root))
	case protocol.CmdFsDelete:
		fsDelete := msg.(*protocol.FsDeleteMessage)
		go d.sendFsDeleteWSResult(client, protocol.Deref(fsDelete.RequestID), fsDelete.Path, protocol.Deref(fsDelete.Root))
	case protocol.CmdFsExists:
		fsExists := msg.(*protocol.FsExistsMessage)
		go d.sendFsExistsWSResult(client, protocol.Deref(fsExists.RequestID), fsExists.Path, protocol.Deref(fsExists.Root))
	case protocol.CmdFsWatch:
		fsWatch := msg.(*protocol.FsWatchMessage)
		go d.handleFsWatch(client, protocol.Deref(fsWatch.RequestID), protocol.Deref(fsWatch.Root))
	case protocol.CmdFsUnwatch:
		fsUnwatch := msg.(*protocol.FsUnwatchMessage)
		go d.handleFsUnwatch(client, protocol.Deref(fsUnwatch.RequestID), protocol.Deref(fsUnwatch.Root))
	case protocol.CmdFsIndex:
		fsIndex := msg.(*protocol.FsIndexMessage)
		go d.handleFsIndex(client, protocol.Deref(fsIndex.RequestID), protocol.Deref(fsIndex.Root))
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
	case protocol.CmdMuteWorkspace:
		d.handleMuteWorkspaceWS(client, msg.(*protocol.MuteWorkspaceMessage))
	case protocol.CmdPinWorkspace:
		d.handlePinWorkspaceWS(client, msg.(*protocol.PinWorkspaceMessage))
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
	case protocol.CmdSessionSelected:
	case protocol.CmdWorkspaceSelected:
	case protocol.CmdTriggerNudge:
		go d.handleTriggerNudge(msg.(*protocol.TriggerNudgeMessage))
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
	case protocol.CmdListPlugins:
		d.handleListPluginsWS(client)
	case protocol.CmdInstallPlugin:
		d.handleInstallPluginWS(client, msg.(*protocol.InstallPluginMessage))
	case protocol.CmdInstallBundledPlugin:
		d.handleInstallBundledPluginWS(client, msg.(*protocol.InstallBundledPluginMessage))
	case protocol.CmdUninstallPlugin:
		d.handleUninstallPluginWS(client, msg.(*protocol.UninstallPluginMessage))
	case protocol.CmdRemovePlugin:
		d.handleRemovePluginWS(client, msg.(*protocol.RemovePluginMessage))
	case protocol.CmdSetPluginPriority:
		d.handleSetPluginPriorityWS(client, msg.(*protocol.SetPluginPriorityMessage))
	case protocol.CmdAddEndpoint:
		d.handleAddEndpointWS(client, msg.(*protocol.AddEndpointMessage))
	case protocol.CmdRemoveEndpoint:
		d.handleRemoveEndpointWS(client, msg.(*protocol.RemoveEndpointMessage))
	case protocol.CmdUpdateEndpoint:
		d.handleUpdateEndpointWS(client, msg.(*protocol.UpdateEndpointMessage))
	case protocol.CmdBootstrapEndpoint:
		d.handleBootstrapEndpointWS(client, msg.(*protocol.BootstrapEndpointMessage))
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
	case protocol.CmdCreateWorktreeFromBranch:
		d.handleCreateWorktreeFromBranchWS(client, msg.(*protocol.CreateWorktreeFromBranchMessage))
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
	case protocol.CmdGetRepoInfo:
		d.handleGetRepoInfoWS(client, msg.(*protocol.GetRepoInfoMessage))
	case protocol.CmdGetPresentations:
		d.handleGetPresentations(client, msg.(*protocol.GetPresentationsMessage))
	case protocol.CmdGetPresentationRound:
		d.handleGetPresentationRound(client, msg.(*protocol.GetPresentationRoundMessage))
	case protocol.CmdPresentSubmitRound:
		d.handlePresentSubmitRound(client, msg.(*protocol.PresentSubmitRoundMessage))
	case protocol.CmdPresentClose:
		d.handlePresentClose(client, msg.(*protocol.PresentCloseMessage))
	case protocol.CmdWorkflowRunGet:
		d.handleWorkflowRunGetWS(client, msg.(*protocol.WorkflowRunGetMessage))
	case protocol.CmdWorkflowRunList:
		d.handleWorkflowRunListWS(client, msg.(*protocol.WorkflowRunListMessage))
	case protocol.CmdWorkflowRunCancel:
		d.handleWorkflowRunCancelWS(client, msg.(*protocol.WorkflowRunCancelMessage))
	case protocol.CmdAutomationDefinitionsGet:
		d.handleAutomationDefinitionsGetWS(client, msg.(*protocol.AutomationDefinitionsGetMessage))
	case protocol.CmdAutomationRunsGet:
		d.handleAutomationRunsGetWS(client, msg.(*protocol.AutomationRunsGetMessage))
	case protocol.CmdAutomationSetEnabled:
		d.handleAutomationSetEnabledWS(client, msg.(*protocol.AutomationSetEnabledMessage))
	case protocol.CmdAutomationDelete:
		d.handleAutomationDeleteWS(client, msg.(*protocol.AutomationDeleteMessage))
	case protocol.CmdAutomationCleanup:
		d.handleAutomationCleanupWS(client, msg.(*protocol.AutomationCleanupMessage))
	case protocol.CmdAutomationRun:
		d.handleAutomationRunWS(client, msg.(*protocol.AutomationRunMessage))
	case protocol.CmdSpawnSession:
		d.handleSpawnSession(client, msg.(*protocol.SpawnSessionMessage))
	case protocol.CmdAttachSession:
		d.handleAttachSession(client, msg.(*protocol.AttachSessionMessage))
	case protocol.CmdDetachSession:
		d.handleDetachSessionWS(client, msg.(*protocol.DetachSessionMessage))
	case protocol.CmdGetScreenSnapshot:
		d.handleGetScreenSnapshot(client, msg.(*protocol.GetScreenSnapshotMessage))
	case protocol.CmdPtyInput:
		d.handlePtyInput(client, msg.(*protocol.PtyInputMessage))
	case protocol.CmdPtyResize:
		d.handlePtyResize(client, msg.(*protocol.PtyResizeMessage))
	case protocol.CmdKillSession:
		d.handleKillSession(client, msg.(*protocol.KillSessionMessage))
	case protocol.CmdSetTerminalTheme:
		d.handleSetTerminalTheme(client, msg.(*protocol.SetTerminalThemeMessage))
	case protocol.CmdWorkspaceLayoutGet:
		d.handleWorkspaceLayoutGet(client, msg.(*protocol.WorkspaceLayoutGetMessage))
	case protocol.CmdWorkspaceLayoutAddSessionPane:
		d.handleWorkspaceLayoutAddSessionPane(client, msg.(*protocol.WorkspaceLayoutAddSessionPaneMessage))
	case protocol.CmdWorkspaceLayoutClosePane:
		d.handleWorkspaceLayoutClosePane(client, msg.(*protocol.WorkspaceLayoutClosePaneMessage))
	case protocol.CmdWorkspaceLayoutFocusPane:
		d.handleWorkspaceLayoutFocusPane(client, msg.(*protocol.WorkspaceLayoutFocusPaneMessage))
	case protocol.CmdWorkspaceLayoutRenamePane:
		d.handleWorkspaceLayoutRenamePane(client, msg.(*protocol.WorkspaceLayoutRenamePaneMessage))
	case protocol.CmdWorkspaceLayoutSetSplitRatio:
		d.handleWorkspaceLayoutSetSplitRatio(client, msg.(*protocol.WorkspaceLayoutSetSplitRatioMessage))
	case protocol.CmdWorkspaceLayoutDockTile:
		d.handleWorkspaceLayoutDockTile(client, msg.(*protocol.WorkspaceLayoutDockTileMessage))
	case protocol.CmdWorkspaceLayoutUndockTile:
		d.handleWorkspaceLayoutUndockTile(client, msg.(*protocol.WorkspaceLayoutUndockTileMessage))
	case protocol.CmdWorkspaceLayoutUpdateTile:
		d.handleWorkspaceLayoutUpdateTile(client, msg.(*protocol.WorkspaceLayoutUpdateTileMessage))
	case protocol.CmdWorkspaceLayoutMoveLeaf:
		d.handleWorkspaceLayoutMoveLeaf(client, msg.(*protocol.WorkspaceLayoutMoveLeafMessage))
	case protocol.CmdWorkspaceLayoutMoveLeafToWorkspace:
		d.handleWorkspaceLayoutMoveLeafToWorkspace(client, msg.(*protocol.WorkspaceLayoutMoveLeafToWorkspaceMessage))
	case protocol.CmdWorkspaceLayoutMoveLeafToNewWorkspace:
		d.handleWorkspaceLayoutMoveLeafToNewWorkspace(client, msg.(*protocol.WorkspaceLayoutMoveLeafToNewWorkspaceMessage))
	case protocol.CmdSetWorkspaceRank:
		d.handleSetWorkspaceRank(client, msg.(*protocol.SetWorkspaceRankMessage))
	case protocol.CmdWorkspaceTileContentGet:
		d.handleWorkspaceTileContentGet(client, msg.(*protocol.WorkspaceTileContentGetMessage))
	case protocol.CmdOpenMarkdown:
		d.handleOpenMarkdownWS(client, msg.(*protocol.OpenMarkdownMessage))
	case protocol.CmdMarkdownAnnotationsGet:
		d.handleMarkdownAnnotationsGet(client, msg.(*protocol.MarkdownAnnotationsGetMessage))
	case protocol.CmdMarkdownAnnotationsSave:
		d.handleMarkdownAnnotationsSave(client, msg.(*protocol.MarkdownAnnotationsSaveMessage))
	case protocol.CmdMarkdownAnnotationsClear:
		d.handleMarkdownAnnotationsClear(client, msg.(*protocol.MarkdownAnnotationsClearMessage))
	case protocol.CmdMarkdownAnnotationsSubmit:
		d.handleMarkdownAnnotationsSubmit(client, msg.(*protocol.MarkdownAnnotationsSubmitMessage))
	case protocol.CmdBrowserControl:
		go d.handleRemoteBrowserControl(client, msg.(*protocol.BrowserControlMessage))
	case protocol.CmdBrowserControlResult:
		d.handleBrowserControlResult(client, msg.(*protocol.BrowserControlResultMessage))
	case protocol.CmdRegisterWorkspace:
		d.handleRegisterWorkspace(client, msg.(*protocol.RegisterWorkspaceMessage))
	case protocol.CmdUnregisterWorkspace:
		d.handleUnregisterWorkspace(client, msg.(*protocol.UnregisterWorkspaceMessage))
	case protocol.CmdRenameSession:
		d.handleRenameSession(client, msg.(*protocol.RenameSessionMessage))
	case protocol.CmdRenameWorkspace:
		d.handleRenameWorkspace(client, msg.(*protocol.RenameWorkspaceMessage))
	case protocol.CmdSetChiefOfStaff:
		d.handleSetChiefOfStaff(client, msg.(*protocol.SetChiefOfStaffMessage))
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

	if workspaceID := remoteCommandWorkspaceID(cmd, msg); workspaceID != "" {
		endpointID, ok := d.hubManager.EndpointIDForWorkspace(workspaceID)
		if !ok {
			return false
		}
		if cmd == protocol.CmdWorkspaceTileContentGet {
			if typed, ok := msg.(*protocol.WorkspaceTileContentGetMessage); ok {
				if !client.notePendingTileContent(typed.WorkspaceID, typed.TileID) {
					d.sendCommandError(client, cmd, "too many pending tile content requests")
					return true
				}
			}
		}
		if err := d.hubManager.ForwardEndpointCommand(context.Background(), endpointID, raw); err != nil {
			if cmd == protocol.CmdWorkspaceTileContentGet {
				if typed, ok := msg.(*protocol.WorkspaceTileContentGetMessage); ok {
					client.cancelPendingTileContent(typed.WorkspaceID, typed.TileID)
				}
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
	case protocol.CmdSessionSelected:
		if typed, ok := msg.(*protocol.SessionSelectedMessage); ok {
			return typed.ID
		}
	case protocol.CmdTriggerNudge:
		if typed, ok := msg.(*protocol.TriggerNudgeMessage); ok {
			return typed.SessionID
		}
	case protocol.CmdRenameSession:
		if typed, ok := msg.(*protocol.RenameSessionMessage); ok {
			return typed.SessionID
		}
	case protocol.CmdOpenMarkdown:
		if typed, ok := msg.(*protocol.OpenMarkdownMessage); ok {
			return protocol.Deref(typed.SessionID)
		}
	case protocol.CmdMarkdownAnnotationsSubmit:
		if typed, ok := msg.(*protocol.MarkdownAnnotationsSubmitMessage); ok {
			return typed.TargetSessionID
		}
	}
	return ""
}

func remoteCommandWorkspaceID(cmd string, msg interface{}) string {
	switch cmd {
	case protocol.CmdWorkspaceLayoutGet:
		if typed, ok := msg.(*protocol.WorkspaceLayoutGetMessage); ok {
			return typed.WorkspaceID
		}
	case protocol.CmdWorkspaceLayoutAddSessionPane:
		if typed, ok := msg.(*protocol.WorkspaceLayoutAddSessionPaneMessage); ok {
			return typed.WorkspaceID
		}
	case protocol.CmdWorkspaceLayoutClosePane:
		if typed, ok := msg.(*protocol.WorkspaceLayoutClosePaneMessage); ok {
			return typed.WorkspaceID
		}
	case protocol.CmdWorkspaceLayoutFocusPane:
		if typed, ok := msg.(*protocol.WorkspaceLayoutFocusPaneMessage); ok {
			return typed.WorkspaceID
		}
	case protocol.CmdWorkspaceLayoutRenamePane:
		if typed, ok := msg.(*protocol.WorkspaceLayoutRenamePaneMessage); ok {
			return typed.WorkspaceID
		}
	case protocol.CmdWorkspaceLayoutSetSplitRatio:
		if typed, ok := msg.(*protocol.WorkspaceLayoutSetSplitRatioMessage); ok {
			return typed.WorkspaceID
		}
	case protocol.CmdWorkspaceLayoutDockTile:
		if typed, ok := msg.(*protocol.WorkspaceLayoutDockTileMessage); ok {
			return typed.WorkspaceID
		}
	case protocol.CmdWorkspaceLayoutUndockTile:
		if typed, ok := msg.(*protocol.WorkspaceLayoutUndockTileMessage); ok {
			return typed.WorkspaceID
		}
	case protocol.CmdWorkspaceLayoutUpdateTile:
		if typed, ok := msg.(*protocol.WorkspaceLayoutUpdateTileMessage); ok {
			return typed.WorkspaceID
		}
	case protocol.CmdWorkspaceLayoutMoveLeaf:
		if typed, ok := msg.(*protocol.WorkspaceLayoutMoveLeafMessage); ok {
			return typed.WorkspaceID
		}
	case protocol.CmdWorkspaceLayoutMoveLeafToWorkspace:
		if typed, ok := msg.(*protocol.WorkspaceLayoutMoveLeafToWorkspaceMessage); ok {
			return typed.SourceWorkspaceID
		}
	case protocol.CmdWorkspaceLayoutMoveLeafToNewWorkspace:
		if typed, ok := msg.(*protocol.WorkspaceLayoutMoveLeafToNewWorkspaceMessage); ok {
			return typed.SourceWorkspaceID
		}
	case protocol.CmdSetWorkspaceRank:
		if typed, ok := msg.(*protocol.SetWorkspaceRankMessage); ok {
			return typed.WorkspaceID
		}
	case protocol.CmdWorkspaceTileContentGet:
		if typed, ok := msg.(*protocol.WorkspaceTileContentGetMessage); ok {
			return typed.WorkspaceID
		}
	case protocol.CmdMarkdownAnnotationsGet:
		if typed, ok := msg.(*protocol.MarkdownAnnotationsGetMessage); ok {
			return typed.WorkspaceID
		}
	case protocol.CmdMarkdownAnnotationsSave:
		if typed, ok := msg.(*protocol.MarkdownAnnotationsSaveMessage); ok {
			return typed.WorkspaceID
		}
	case protocol.CmdMarkdownAnnotationsClear:
		if typed, ok := msg.(*protocol.MarkdownAnnotationsClearMessage); ok {
			return typed.WorkspaceID
		}
	case protocol.CmdRenameWorkspace:
		if typed, ok := msg.(*protocol.RenameWorkspaceMessage); ok {
			return typed.WorkspaceID
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
	case protocol.CmdRegisterWorkspace:
		if typed, ok := msg.(*protocol.RegisterWorkspaceMessage); ok {
			return strings.TrimSpace(protocol.Deref(typed.EndpointID))
		}
	case protocol.CmdMuteWorkspace:
		if typed, ok := msg.(*protocol.MuteWorkspaceMessage); ok {
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
}) (string, bool) {
	if manager == nil {
		return "", false
	}
	if path := remoteCommandPath(msg); path != "" {
		if endpointID, ok := manager.EndpointIDForPath(path); ok {
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
	case *protocol.CreateWorktreeFromBranchMessage:
		return typed.MainRepo
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
	case *protocol.GetRepoInfoMessage:
		return typed.Repo
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
		Event       string `json:"event"`
		ID          string `json:"id"`
		Success     bool   `json:"success"`
		WorkspaceID string `json:"workspace_id"`
		TileID      string `json:"tile_id"`
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
			return client.wantsRemoteAttachTraffic(envelope.ID)
		})
		return
	case protocol.EventWorkspaceTileContent:
		if strings.TrimSpace(envelope.WorkspaceID) == "" || strings.TrimSpace(envelope.TileID) == "" {
			d.logf("dropping malformed relayed tile content event")
			return
		}
		d.wsHub.SendRawTextToMatchingClients(payload, func(client *wsClient) bool {
			return client.resolvePendingTileContent(envelope.WorkspaceID, envelope.TileID)
		})
		return
	case protocol.EventWorkspaceLayout, protocol.EventWorkspaceLayoutUpdated:
		var msg struct {
			WorkspaceLayout *protocol.WorkspaceLayout `json:"workspace_layout"`
		}
		if err := json.Unmarshal(payload, &msg); err == nil && msg.WorkspaceLayout != nil {
			if layout, err := workspacelayout.DecodeLayout(msg.WorkspaceLayout.LayoutJson); err == nil {
				d.pruneTileContentSubscriptionsForLayout(msg.WorkspaceLayout.WorkspaceID, &layout)
			}
		}
	case protocol.EventWorkspaceUnregistered:
		var msg struct {
			Workspace *protocol.Workspace `json:"workspace"`
		}
		if err := json.Unmarshal(payload, &msg); err == nil && msg.Workspace != nil {
			d.pruneTileContentSubscriptionsForLayout(msg.Workspace.ID, nil)
		}
	case protocol.EventSessionExited:
		if strings.TrimSpace(envelope.ID) != "" {
			d.wsHub.ForEachClient(func(client *wsClient) {
				client.clearRemoteAttach(envelope.ID)
			})
		}
	}

	d.wsHub.BroadcastRawText(payload)
}
