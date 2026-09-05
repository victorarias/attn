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
	"sync/atomic"
	"time"

	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/workspacelayout"
)

type wsClient struct {
	conn *websocket.Conn
	// The WebSocket wrapper offers no way back to the socket, and an evicted
	// client has to be cut off there.
	rawConn     net.Conn
	send        chan outboundMessage
	recv        chan []byte
	slowCount   int
	writing     atomic.Bool
	sendMu      sync.RWMutex
	sendClosed  bool
	closeCode   websocket.StatusCode
	closeReason string
	connectedAt time.Time

	trustedTauriOrigin       bool
	browserHostAuthenticated bool
	bearerAuthorized         bool

	attachedStreams map[string]ptybackend.Stream
	attachedRemote  map[string]struct{}
	pendingRemote   map[string]struct{}
	attachMu        sync.Mutex

	docSubscriptions clientDocSubscriptions

	tileContentSubscriptions map[string]struct{}
	tileContentPending       map[string]time.Time
	tileContentMu            sync.RWMutex

	admitted      sync.Once
	clientKind    string
	clientVersion string
	clientID      string
	capabilities  map[string]struct{}
	identityMu    sync.RWMutex

	// Its own lock: presence is rewritten on every heartbeat while identity is
	// written once.
	presence   clientPresence
	presenceMu sync.RWMutex

	gitStatusDir        string
	gitStatusStop       chan struct{}
	gitStatusRefresh    chan gitStatusRefreshRequest
	gitStatusHash       string
	gitStatusEndpointID string
	gitStatusMu         sync.Mutex
}

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

// Arbitrary fs roots are gated on this: without it any accepted local
// WebSocket client could fs_* anywhere in the user's home.
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

func (c *wsClient) setClientID(id string) {
	c.identityMu.Lock()
	defer c.identityMu.Unlock()
	c.clientID = id
}

func (c *wsClient) ClientID() string {
	c.identityMu.RLock()
	defer c.identityMu.RUnlock()
	return c.clientID
}

func (c *wsClient) owed() int {
	n := len(c.send)
	if c.writing.Load() {
		n++
	}
	return n
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

type BroadcastListener func(event *protocol.WebSocketEvent)

// BroadcastListener sees only Broadcast, so typed messages and
// already-marshalled bytes are invisible to it.
type WireTap func(payload []byte)

type messageKind int

const (
	messageKindText messageKind = iota
	messageKindBinary
)

type outboundMessage struct {
	kind    messageKind
	payload []byte
}

type wsHub struct {
	clients    map[*wsClient]bool
	broadcast  chan outboundMessage
	unregister chan *wsClient
	mu         sync.RWMutex
	// Its own lock: evictions are filed under h.mu.
	evictions         map[string]evictionRecord
	evictionMu        sync.Mutex
	logf              func(format string, args ...interface{})
	broadcastListener BroadcastListener
	wireTap           WireTap
	evictionListener  func(string, evictionRecord)
}

const (
	maxSlowCount          = 3
	slowClientCloseReason = "client too slow"
	maxPTYDimValue        = 65535
	// The kernel's winsize fields are all uint16, pixels included.
	maxPTYPixelValue                 = 65535
	defaultWebSocketReadBytes        = 1 << 20
	maxBrowserHostWebSocketReadBytes = 32 << 20
	ptyOutputSendWait                = 1 * time.Second
)

func newWSHub() *wsHub {
	return &wsHub{
		clients:    make(map[*wsClient]bool),
		broadcast:  make(chan outboundMessage, 256),
		unregister: make(chan *wsClient),
		logf:       func(format string, args ...interface{}) {},
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
		case client := <-h.unregister:
			h.mu.Lock()
			delete(h.clients, client)
			client.closeSendChannel()
			client.stopGitStatusPoll()
			h.mu.Unlock()

		case message := <-h.broadcast:
			h.mu.Lock()
			var toRemove []*wsClient
			for client := range h.clients {
				if client.trySend(message) {
					client.slowCount = 0
				} else {
					client.slowCount++
					if client.slowCount >= maxSlowCount {
						h.logf("WebSocket client too slow (%d missed), disconnecting", client.slowCount)
						toRemove = append(toRemove, client)
					} else {
						h.logf("WebSocket client slow (%d/%d missed)", client.slowCount, maxSlowCount)
					}
				}
			}
			for _, client := range toRemove {
				delete(h.clients, client)
				h.evict(client, slowClientCloseReason)
			}
			h.mu.Unlock()
		}
	}
}

func (h *wsHub) add(client *wsClient) {
	h.mu.Lock()
	h.clients[client] = true
	h.mu.Unlock()
}

func (h *wsHub) Broadcast(event *protocol.WebSocketEvent) {
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
	if h.wireTap != nil {
		h.wireTap(payload)
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
		h.evict(client, slowClientCloseReason)
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
	if h.wireTap != nil {
		h.wireTap(data)
	}
	out := outboundMessage{kind: messageKindText, payload: data}
	select {
	case h.broadcast <- out:
	default:
		h.logf("WebSocket broadcast channel full, dropping outbound message")
	}
}

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

func (d *Daemon) handleWS(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if !isAllowedWSOrigin(origin, r.Host) {
		d.logf("WebSocket rejected origin: %s", origin)
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	// An operator-set bearer means this port was deliberately exposed beyond
	// loopback (tailscale serve), which proves what the client token proves.
	bearerAuthorized := false
	if required := config.WSAuthToken(); required != "" {
		provided := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if provided == "" {
			provided = strings.TrimSpace(r.URL.Query().Get("token"))
		}
		if subtle.ConstantTimeCompare([]byte(required), []byte(provided)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		bearerAuthorized = true
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: websocketOriginPatternsForRequest(r),
	})
	if err != nil {
		d.logf("WebSocket accept error: %v", err)
		return
	}
	conn.SetReadLimit(defaultWebSocketReadBytes)

	client := &wsClient{
		conn:               conn,
		rawConn:            rawConnFrom(r.Context()),
		send:               make(chan outboundMessage, 256),
		recv:               make(chan []byte, 256),
		connectedAt:        time.Now(),
		trustedTauriOrigin: isTrustedTauriOrigin(origin),
		bearerAuthorized:   bearerAuthorized,
		attachedStreams:    make(map[string]ptybackend.Stream),
		attachedRemote:     make(map[string]struct{}),
		pendingRemote:      make(map[string]struct{}),
	}

	// The hub is the only fan-out, so staying out of it until client_hello keeps
	// every broadcast away from an unauthorized connection.
	d.logf("WebSocket connection accepted, awaiting client_hello")

	done := make(chan struct{})
	go d.wsPingLoop(client, done)

	go d.wsWritePump(client)
	go d.wsMsgPump(client)
	d.wsReadPump(client)

	close(done)
}

func (d *Daemon) sendInitialState(client *wsClient) {
	homeDaemonID := ""
	if status, err := d.enrollmentStatus(); err == nil {
		homeDaemonID = status.HomeDaemonID
	}
	state := d.currentStateProjection()
	event := &protocol.InitialStateMessage{
		Event:             protocol.EventInitialState,
		ProtocolVersion:   protocol.Ptr(protocol.ProtocolVersion),
		SourceFingerprint: protocol.Ptr(buildinfo.SourceFingerprint),
		DaemonInstanceID:  protocol.Ptr(d.daemonInstanceID),
		HomeDaemonID:      protocol.Ptr(homeDaemonID),
		Sessions:          state.Sessions,
		Endpoints:         state.Endpoints,
		Workspaces:        state.Workspaces,
		Prs:               state.Prs,
		Repos:             state.Repos,
		Authors:           state.Authors,
		GithubHosts:       state.GithubHosts,
		Settings:          d.settingsWithAgentAvailability(),
		Warnings:          d.getWarnings(),
		Seeds:             state.Seeds,
		SeedsTotal:        protocol.Ptr(d.countSeedsForBroadcast()),
		Apps:              state.Apps,
		Crew:              state.Crew,
	}
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	_ = d.sendOutbound(client, outboundMessage{kind: messageKindText, payload: data})

	go d.fetchAllPRDetails()
}

// Measured against a real app with a frozen socket: a 400-session snapshot left 409,117
// bytes stuck in the client's receive queue while the hub's slow-count never reached 2.
const defaultWSWriteTimeout = 10 * time.Second

func (d *Daemon) wsWriteTimeoutDuration() time.Duration {
	if d.wsWriteTimeout > 0 {
		return d.wsWriteTimeout
	}
	return defaultWSWriteTimeout
}

func (d *Daemon) wsWritePump(client *wsClient) {
	writeTimeout := d.wsWriteTimeoutDuration()
	stalled := false
	defer func() {
		code, reason := client.closeStatus()
		if stalled {
			code, reason = websocket.StatusPolicyViolation, slowClientCloseReason
			d.wsHub.rememberEviction(client.ClientID(), evictionRecord{
				at:          time.Now(),
				reason:      reason,
				undelivered: len(client.send) + 1,
			})
		}
		client.conn.Close(code, reason)
	}()

	for message := range client.send {
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		wsType := websocket.MessageText
		if message.kind != messageKindText {
			wsType = websocket.MessageBinary
		}
		client.writing.Store(true)
		err := client.conn.Write(ctx, wsType, message.payload)
		client.writing.Store(false)
		// The deadline starts before Write and survives scheduler preemption; an
		// elapsed stopwatch around Write can miss a deadline that already fired.
		deadlineExpired := ctx.Err() == context.DeadlineExceeded
		cancel()
		if err != nil {
			stalled = deadlineExpired
			if stalled {
				d.logf("WebSocket client took longer than %s to accept a message, giving up on it", writeTimeout)
			}
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

func (d *Daemon) wsMsgPump(client *wsClient) {
	for data := range client.recv {
		// A refused client_hello leaves the write pump to deliver the refusal;
		// answering what the client pipelined behind it would answer twice.
		if client.sendChannelClosed() {
			continue
		}
		d.handleClientMessage(client, data)
	}
	d.logf("WebSocket message pump exited")
}

func (c *wsClient) sendChannelClosed() bool {
	c.sendMu.RLock()
	defer c.sendMu.RUnlock()
	return c.sendClosed
}

const (
	defaultWSPingInterval = 30 * time.Second
	defaultWSPingTimeout  = 10 * time.Second
)

func (d *Daemon) wsPingIntervalDuration() time.Duration {
	if d.wsPingInterval > 0 {
		return d.wsPingInterval
	}
	return defaultWSPingInterval
}

func (d *Daemon) wsPingTimeoutDuration() time.Duration {
	if d.wsPingTimeout > 0 {
		return d.wsPingTimeout
	}
	return defaultWSPingTimeout
}

// Measured with the app's socket frozen, the unanswered ping beat both the hub's
// slow-count and the write pump's deadline to it, twice out of two.
func (d *Daemon) wsPingLoop(client *wsClient, done <-chan struct{}) {
	pingTimeout := d.wsPingTimeoutDuration()
	ticker := time.NewTicker(d.wsPingIntervalDuration())
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
			err := client.conn.Ping(ctx)
			cancel()
			if err != nil {
				d.logf("WebSocket ping failed: %v", err)
				if owed := client.owed(); owed > 0 {
					d.logf("WebSocket client stopped answering with %d messages still owed to it; filing that for its return", owed)
					d.wsHub.rememberEviction(client.ClientID(), evictionRecord{
						at:          time.Now(),
						reason:      slowClientCloseReason,
						undelivered: owed,
					})
				}
				// Not conn.Close: it waits out its close handshake against a peer that has
				// stopped answering — measured live, five more seconds.
				client.hangUp(websocket.StatusGoingAway, "ping timeout", evictionCloseGrace)
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
		d.dropDocSubscriptions(client)
		d.detachAllSessions(client)
		close(client.recv)
		d.wsHub.unregister <- client
		client.conn.Close(websocket.StatusNormalClosure, "")
		d.logf("WebSocket client disconnected (%d remaining)", d.wsHub.ClientCount())
	}()

	for {
		// No read timeout: liveness comes from the ping loop, whose close unblocks
		// this Read.
		_, data, err := client.conn.Read(context.Background())
		if err != nil {
			d.logf("WebSocket read error: %v", err)
			return
		}

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
	// Recorded before remote routing: a host-side `attn open` without --session
	// must fail against the selected remote id, not a stale local selection.
	if cmd == protocol.CmdSessionSelected {
		d.setSelectedSession(msg.(*protocol.SessionSelectedMessage).ID)
	}
	if cmd == protocol.CmdWorkspaceSelected {
		d.setSelectedWorkspace(msg.(*protocol.WorkspaceSelectedMessage).WorkspaceID)
	}
	// UI-origin commands stand in for the user being at the app right now.
	if isUserPresenceCommand(cmd) {
		d.recordUserActivity(time.Now())
	}
	if d.tryHandleRemoteWSCommand(client, cmd, msg, data) {
		return
	}

	switch cmd {
	case protocol.CmdClientHello: // wire: client_hello
		d.handleClientHello(client, msg.(*protocol.ClientHelloMessage))
	case protocol.CmdDelegate: // wire: delegate
		go d.handleDelegateWS(client, msg.(*protocol.DelegateMessage))
	case protocol.CmdDelegationModels: // wire: delegation_models
		go d.handleDelegationModels(client, msg.(*protocol.DelegationModelsMessage))
	case protocol.CmdDelegationPreferencesGet: // wire: delegation_preferences_get
		d.handleDelegationPreferencesGet(client, msg.(*protocol.DelegationPreferencesGetMessage))
	case protocol.CmdDelegationPreferencesSave: // wire: delegation_preferences_save
		d.handleDelegationPreferencesSave(client, msg.(*protocol.DelegationPreferencesSaveMessage))
	case protocol.CmdDelegateStatus: // wire: delegate_status
		go d.handleDelegateStatusWS(client, msg.(*protocol.DelegateStatusMessage))
	case protocol.CmdWorkspaceContextCheckout: // wire: workspace_context_checkout
		go func() {
			result, err := d.checkoutWorkspaceContext(msg.(*protocol.WorkspaceContextCheckoutMessage))
			d.sendWorkspaceContextWSResult(client, "checkout", result, err)
		}()
	case protocol.CmdWorkspaceContextUpdate: // wire: workspace_context_update
		go func() {
			result, _, err := d.updateWorkspaceContext(msg.(*protocol.WorkspaceContextUpdateMessage))
			d.sendWorkspaceContextWSResult(client, "update", result, err)
		}()
	case protocol.CmdWorkspaceContextStatus: // wire: workspace_context_status
		go func() {
			result, err := d.workspaceContextStatus(msg.(*protocol.WorkspaceContextStatusMessage))
			d.sendWorkspaceContextWSResult(client, "status", result, err)
		}()
	case protocol.CmdWorkspaceContextList: // wire: workspace_context_list
		go d.sendWorkspaceContextListWSResult(client, msg.(*protocol.WorkspaceContextListMessage).RequestID)
	case protocol.CmdNotebookList: // wire: notebook_list
		nbList := msg.(*protocol.NotebookListMessage)
		go d.sendNotebookListWSResult(client, protocol.Deref(nbList.RequestID), protocol.Deref(nbList.Prefix))
	case protocol.CmdNotebookRead: // wire: notebook_read
		nbRead := msg.(*protocol.NotebookReadMessage)
		go d.sendNotebookReadWSResult(client, protocol.Deref(nbRead.RequestID), nbRead.Path)
	case protocol.CmdNotebookBacklinks: // wire: notebook_backlinks
		nbBack := msg.(*protocol.NotebookBacklinksMessage)
		go d.sendNotebookBacklinksWSResult(client, protocol.Deref(nbBack.RequestID), nbBack.Path)
	case protocol.CmdNotebookWrite: // wire: notebook_write
		nbWrite := msg.(*protocol.NotebookWriteMessage)
		go d.sendNotebookWriteWSResult(client, protocol.Deref(nbWrite.RequestID), nbWrite.Path, nbWrite.Content, protocol.Deref(nbWrite.BaseHash))
	case protocol.CmdNotebookSendToChief: // wire: notebook_send_to_chief
		nbChief := msg.(*protocol.NotebookSendToChiefMessage)
		go d.sendNotebookToChiefWSResult(client, protocol.Deref(nbChief.RequestID), protocol.Deref(nbChief.SourcePath), nbChief.Selection)
	case protocol.CmdTaskList: // wire: task_list
		nbTaskList := msg.(*protocol.TaskListMessage)
		go d.sendTaskListWSResult(client, protocol.Deref(nbTaskList.RequestID))
	case protocol.CmdTaskRetry: // wire: task_retry
		nbTaskRetry := msg.(*protocol.TaskRetryMessage)
		go d.sendTaskRetryWSResult(client, protocol.Deref(nbTaskRetry.RequestID), nbTaskRetry.TaskID)
	case protocol.CmdNotificationList: // wire: notification_list
		notifList := msg.(*protocol.NotificationListMessage)
		go d.sendNotificationListWSResult(client, protocol.Deref(notifList.RequestID))
	case protocol.CmdNotificationMarkRead: // wire: notification_mark_read
		notifMark := msg.(*protocol.NotificationMarkReadMessage)
		go d.sendNotificationMarkReadWSResult(client, protocol.Deref(notifMark.RequestID), notifMark.NotificationID)
	case protocol.CmdTicketAttach: // wire: ticket_attach
		go d.handleTicketAttachWS(client, msg.(*protocol.TicketAttachMessage))
	case protocol.CmdSeedArtifactTransfer: // wire: seed_artifact_transfer
		go d.handleSeedArtifactTransferWS(client, msg.(*protocol.SeedArtifactTransferMessage))
	case protocol.CmdSeedArtifactTarget: // wire: seed_artifact_target
		go d.handleSeedArtifactTarget(client, msg.(*protocol.SeedArtifactTargetMessage))
	case protocol.CmdSeedResume: // wire: seed_resume
		go d.handleSeedResume(client, msg.(*protocol.SeedResumeMessage))
	case protocol.CmdSeedSendToChief: // wire: seed_send_to_chief
		go d.handleSeedSendToChiefWS(client, msg.(*protocol.SeedSendToChiefMessage))
	case protocol.CmdSeedReviewStart: // wire: seed_review_start
		go d.handleSeedReviewStartWS(client, msg.(*protocol.SeedReviewStartMessage))
	case protocol.CmdSeedReviewShow: // wire: seed_review_show
		go d.handleSeedReviewShowWS(client, msg.(*protocol.SeedReviewShowMessage))
	case protocol.CmdSeedReviewCancel: // wire: seed_review_cancel
		go d.handleSeedReviewCancelWS(client, msg.(*protocol.SeedReviewCancelMessage))
	case protocol.CmdSeedReviewRetry: // wire: seed_review_retry
		go d.handleSeedReviewRetryWS(client, msg.(*protocol.SeedReviewRetryMessage))
	case protocol.CmdSeedReviewKeep: // wire: seed_review_keep
		go d.handleSeedReviewKeepWS(client, msg.(*protocol.SeedReviewKeepMessage))
	case protocol.CmdSeedReviewDraft: // wire: seed_review_draft
		go d.handleSeedReviewDraftWS(client, msg.(*protocol.SeedReviewDraftMessage))
	case protocol.CmdCrewWake: // wire: crew_wake
		go d.handleCrewWakeWS(client, msg.(*protocol.CrewWakeMessage))
	case protocol.CmdCrewSleep: // wire: crew_sleep
		go d.handleCrewSleepWS(client, msg.(*protocol.CrewSleepMessage))
	case protocol.CmdFsList: // wire: fs_list
		fsList := msg.(*protocol.FsListMessage)
		go d.sendFsListWSResult(client, protocol.Deref(fsList.RequestID), protocol.Deref(fsList.Path), protocol.Deref(fsList.Root))
	case protocol.CmdFsRead: // wire: fs_read
		fsRead := msg.(*protocol.FsReadMessage)
		go d.sendFsReadWSResult(client, protocol.Deref(fsRead.RequestID), fsRead.Path, protocol.Deref(fsRead.Root))
	case protocol.CmdFsReadAsset: // wire: fs_read_asset
		fsReadAsset := msg.(*protocol.FsReadAssetMessage)
		go d.sendFsReadAssetWSResult(client, protocol.Deref(fsReadAsset.RequestID), fsReadAsset.Path, protocol.Deref(fsReadAsset.Root))
	case protocol.CmdFsWrite: // wire: fs_write
		fsWrite := msg.(*protocol.FsWriteMessage)
		go d.sendFsWriteWSResult(client, protocol.Deref(fsWrite.RequestID), fsWrite.Path, fsWrite.Content, protocol.Deref(fsWrite.BaseHash), protocol.Deref(fsWrite.Root))
	case protocol.CmdFsRename: // wire: fs_rename
		fsRename := msg.(*protocol.FsRenameMessage)
		go d.sendFsRenameWSResult(client, protocol.Deref(fsRename.RequestID), fsRename.Path, fsRename.NewPath, protocol.Deref(fsRename.Root))
	case protocol.CmdFsDelete: // wire: fs_delete
		fsDelete := msg.(*protocol.FsDeleteMessage)
		go d.sendFsDeleteWSResult(client, protocol.Deref(fsDelete.RequestID), fsDelete.Path, protocol.Deref(fsDelete.Root))
	case protocol.CmdFsExists: // wire: fs_exists
		fsExists := msg.(*protocol.FsExistsMessage)
		go d.sendFsExistsWSResult(client, protocol.Deref(fsExists.RequestID), fsExists.Path, protocol.Deref(fsExists.Root))
	case protocol.CmdFsWatch: // wire: fs_watch
		fsWatch := msg.(*protocol.FsWatchMessage)
		go d.handleFsWatch(client, protocol.Deref(fsWatch.RequestID), protocol.Deref(fsWatch.Root))
	case protocol.CmdFsUnwatch: // wire: fs_unwatch
		fsUnwatch := msg.(*protocol.FsUnwatchMessage)
		go d.handleFsUnwatch(client, protocol.Deref(fsUnwatch.RequestID), protocol.Deref(fsUnwatch.Root))
	case protocol.CmdFsIndex: // wire: fs_index
		fsIndex := msg.(*protocol.FsIndexMessage)
		go d.handleFsIndex(client, protocol.Deref(fsIndex.RequestID), protocol.Deref(fsIndex.Root), fsIndex.Extensions)
	case protocol.CmdApprovePR: // wire: approve_pr
		d.handleApprovePRWS(client, msg.(*protocol.ApprovePRMessage))
	case protocol.CmdMergePR: // wire: merge_pr
		d.handleMergePRWS(client, msg.(*protocol.MergePRMessage))
	case protocol.CmdMutePR: // wire: mute_pr
		d.handleMutePRWS(msg.(*protocol.MutePRMessage))
	case protocol.CmdMuteRepo: // wire: mute_repo
		d.handleMuteRepoWS(msg.(*protocol.MuteRepoMessage))
	case protocol.CmdMuteAuthor: // wire: mute_author
		d.handleMuteAuthorWS(msg.(*protocol.MuteAuthorMessage))
	case protocol.CmdMuteWorkspace: // wire: mute_workspace
		d.handleMuteWorkspaceWS(client, msg.(*protocol.MuteWorkspaceMessage))
	case protocol.CmdPinWorkspace: // wire: pin_workspace
		d.handlePinWorkspaceWS(client, msg.(*protocol.PinWorkspaceMessage))
	case protocol.CmdPinSession: // wire: pin_session
		d.handlePinSession(client, msg.(*protocol.PinSessionMessage))
	case protocol.CmdSetSessionContextWindowCap: // wire: set_session_context_window_cap
		d.handleSetSessionContextWindowCap(client, msg.(*protocol.SetSessionContextWindowCapMessage))
	case protocol.CmdRefreshPRs: // wire: refresh_prs
		d.handleRefreshPRsWS(client)
	case protocol.CmdFetchPRDetails: // wire: fetch_pr_details
		d.handleFetchPRDetailsWS(client, msg.(*protocol.FetchPRDetailsMessage))
	case protocol.CmdClearSessions: // wire: clear_sessions
		d.handleClearSessionsWS()
	case protocol.CmdClearWarnings: // wire: clear_warnings
		d.handleClearWarningsWS()
	case protocol.CmdSessionSelected: // wire: session_selected
	case protocol.CmdWorkspaceSelected: // wire: workspace_selected
	case protocol.CmdSettleTurn: // wire: settle_turn
		d.handleSettleTurn(msg.(*protocol.SettleTurnMessage))
	case protocol.CmdSnoozeTurn: // wire: snooze_turn
		d.handleSnoozeTurn(msg.(*protocol.SnoozeTurnMessage))
	case protocol.CmdWakeTurn: // wire: wake_turn
		d.handleWakeTurn(msg.(*protocol.WakeTurnMessage))
	case protocol.CmdPullRequestCreated: // wire: pull_request_created
		d.handlePullRequestCreatedWS(msg.(*protocol.PullRequestCreatedMessage))
	case protocol.CmdPullRequestForget: // wire: pull_request_forget
		d.handlePullRequestForgetWS(msg.(*protocol.PullRequestForgetMessage))
	case protocol.CmdCancelCountdown: // wire: cancel_countdown
		d.handleCancelCountdown(msg.(*protocol.CancelCountdownMessage))
	case protocol.CmdTriggerNudge: // wire: trigger_nudge
		go d.handleTriggerNudge(msg.(*protocol.TriggerNudgeMessage))
	case protocol.CmdPRVisited: // wire: pr_visited
		d.handlePRVisitedWS(msg.(*protocol.PRVisitedMessage))
	case protocol.CmdListWorktrees: // wire: list_worktrees
		d.handleListWorktreesWS(client, msg.(*protocol.ListWorktreesMessage))
	case protocol.CmdCreateWorktree: // wire: create_worktree
		d.handleCreateWorktreeWS(client, msg.(*protocol.CreateWorktreeMessage))
	case protocol.CmdDeleteWorktree: // wire: delete_worktree
		d.handleDeleteWorktreeWS(client, msg.(*protocol.DeleteWorktreeMessage))
	case protocol.CmdGetSettings: // wire: get_settings
		d.handleGetSettingsWS(client)
	case protocol.CmdSetSetting: // wire: set_setting
		setting := msg.(*protocol.SetSettingMessage)
		if setting.Key == SettingSharedPTYHostEnabled {
			go d.handleSetSettingWS(client, setting)
		} else {
			d.handleSetSettingWS(client, setting)
		}
	case protocol.CmdListPlugins: // wire: list_plugins
		d.handleListPluginsWS(client)
	case protocol.CmdInstallPlugin: // wire: install_plugin
		d.handleInstallPluginWS(client, msg.(*protocol.InstallPluginMessage))
	case protocol.CmdInstallBundledPlugin: // wire: install_bundled_plugin
		d.handleInstallBundledPluginWS(client, msg.(*protocol.InstallBundledPluginMessage))
	case protocol.CmdUninstallPlugin: // wire: uninstall_plugin
		d.handleUninstallPluginWS(client, msg.(*protocol.UninstallPluginMessage))
	case protocol.CmdRemovePlugin: // wire: remove_plugin
		d.handleRemovePluginWS(client, msg.(*protocol.RemovePluginMessage))
	case protocol.CmdSetPluginPriority: // wire: set_plugin_priority
		d.handleSetPluginPriorityWS(client, msg.(*protocol.SetPluginPriorityMessage))
	case protocol.CmdAddEndpoint: // wire: add_endpoint
		d.handleAddEndpointWS(client, msg.(*protocol.AddEndpointMessage))
	case protocol.CmdRemoveEndpoint: // wire: remove_endpoint
		d.handleRemoveEndpointWS(client, msg.(*protocol.RemoveEndpointMessage))
	case protocol.CmdUpdateEndpoint: // wire: update_endpoint
		d.handleUpdateEndpointWS(client, msg.(*protocol.UpdateEndpointMessage))
	case protocol.CmdBootstrapEndpoint: // wire: bootstrap_endpoint
		d.handleBootstrapEndpointWS(client, msg.(*protocol.BootstrapEndpointMessage))
	case protocol.CmdListEndpoints: // wire: list_endpoints
		d.handleListEndpointsWS(client)
	case protocol.CmdSetEndpointRemoteWeb: // wire: set_endpoint_remote_web
		d.handleSetEndpointRemoteWebWS(client, msg.(*protocol.SetEndpointRemoteWebMessage))
	case protocol.CmdUnregister: // wire: unregister
		d.handleUnregisterWS(client, msg.(*protocol.UnregisterMessage))
	case protocol.CmdGetRecentLocations: // wire: get_recent_locations
		d.handleGetRecentLocationsWS(client, msg.(*protocol.GetRecentLocationsMessage))
	case protocol.CmdRecentFiles: // wire: recent_files
		d.handleRecentFilesWS(client, msg.(*protocol.RecentFilesMessage))
	case protocol.CmdBrowseDirectory: // wire: browse_directory
		d.handleBrowseDirectoryWS(client, msg.(*protocol.BrowseDirectoryMessage))
	case protocol.CmdInspectPath: // wire: inspect_path
		d.handleInspectPathWS(client, msg.(*protocol.InspectPathMessage))
	case protocol.CmdListBranches: // wire: list_branches
		d.handleListBranchesWS(client, msg.(*protocol.ListBranchesMessage))
	case protocol.CmdCreateWorktreeFromBranch: // wire: create_worktree_from_branch
		d.handleCreateWorktreeFromBranchWS(client, msg.(*protocol.CreateWorktreeFromBranchMessage))
	case protocol.CmdGetDefaultBranch: // wire: get_default_branch
		d.handleGetDefaultBranchWS(client, msg.(*protocol.GetDefaultBranchMessage))
	case protocol.CmdFetchRemotes: // wire: fetch_remotes
		d.handleFetchRemotesWS(client, msg.(*protocol.FetchRemotesMessage))
	case protocol.CmdListRemoteBranches: // wire: list_remote_branches
		d.handleListRemoteBranchesWS(client, msg.(*protocol.ListRemoteBranchesMessage))
	case protocol.CmdEnsureRepo: // wire: ensure_repo
		d.handleEnsureRepoWS(client, msg.(*protocol.EnsureRepoMessage))
	case protocol.CmdSubscribeGitStatus: // wire: subscribe_git_status
		d.handleSubscribeGitStatus(client, msg.(*protocol.SubscribeGitStatusMessage))
	case protocol.CmdUnsubscribeGitStatus: // wire: unsubscribe_git_status
		d.handleUnsubscribeGitStatusWS(client)
	case protocol.CmdGetFileDiff: // wire: get_file_diff
		d.handleGetFileDiffWS(client, msg.(*protocol.GetFileDiffMessage))
	case protocol.CmdGetRepoInfo: // wire: get_repo_info
		d.handleGetRepoInfoWS(client, msg.(*protocol.GetRepoInfoMessage))
	case protocol.CmdGetPresentations: // wire: get_presentations
		d.handleGetPresentations(client, msg.(*protocol.GetPresentationsMessage))
	case protocol.CmdGetPresentationRound: // wire: get_presentation_round
		d.handleGetPresentationRound(client, msg.(*protocol.GetPresentationRoundMessage))
	case protocol.CmdPresentSubmitRound: // wire: present_submit_round
		d.handlePresentSubmitRound(client, msg.(*protocol.PresentSubmitRoundMessage))
	case protocol.CmdPresentClose: // wire: present_close
		d.handlePresentClose(client, msg.(*protocol.PresentCloseMessage))
	case protocol.CmdWorkflowRunGet: // wire: workflow_run_get
		d.handleWorkflowRunGetWS(client, msg.(*protocol.WorkflowRunGetMessage))
	case protocol.CmdWorkflowRunList: // wire: workflow_run_list
		d.handleWorkflowRunListWS(client, msg.(*protocol.WorkflowRunListMessage))
	case protocol.CmdWorkflowRunCancel: // wire: workflow_run_cancel
		d.handleWorkflowRunCancelWS(client, msg.(*protocol.WorkflowRunCancelMessage))
	case protocol.CmdAutomationDefinitionsGet: // wire: automation_definitions_get
		d.handleAutomationDefinitionsGetWS(client, msg.(*protocol.AutomationDefinitionsGetMessage))
	case protocol.CmdAutomationDefinitionGet: // wire: automation_definition_get
		d.handleAutomationDefinitionGetWS(client, msg.(*protocol.AutomationDefinitionGetMessage))
	case protocol.CmdAutomationRunsGet: // wire: automation_runs_get
		d.handleAutomationRunsGetWS(client, msg.(*protocol.AutomationRunsGetMessage))
	case protocol.CmdAutomationSetEnabled: // wire: automation_set_enabled
		d.handleAutomationSetEnabledWS(client, msg.(*protocol.AutomationSetEnabledMessage))
	case protocol.CmdAutomationApply: // wire: automation_apply
		d.handleAutomationApplyWS(client, msg.(*protocol.AutomationApplyMessage))
	case protocol.CmdAutomationValidate: // wire: automation_validate
		d.handleAutomationValidateWS(client, msg.(*protocol.AutomationValidateMessage))
	case protocol.CmdAutomationDelete: // wire: automation_delete
		d.handleAutomationDeleteWS(client, msg.(*protocol.AutomationDeleteMessage))
	case protocol.CmdAutomationCleanup: // wire: automation_cleanup
		d.handleAutomationCleanupWS(client, msg.(*protocol.AutomationCleanupMessage))
	case protocol.CmdAutomationRun: // wire: automation_run
		d.handleAutomationRunWS(client, msg.(*protocol.AutomationRunMessage))
	case protocol.CmdSpawnSession: // wire: spawn_session
		d.handleSpawnSession(client, msg.(*protocol.SpawnSessionMessage))
	case protocol.CmdAttachSession: // wire: attach_session
		d.handleAttachSession(client, msg.(*protocol.AttachSessionMessage))
	case protocol.CmdDetachSession: // wire: detach_session
		d.handleDetachSessionWS(client, msg.(*protocol.DetachSessionMessage))
	case protocol.CmdGetScreenSnapshot: // wire: get_screen_snapshot
		d.handleGetScreenSnapshot(client, msg.(*protocol.GetScreenSnapshotMessage))
	case protocol.CmdGetKittyImage: // wire: get_kitty_image
		d.handleGetKittyImage(client, msg.(*protocol.GetKittyImageMessage))
	case protocol.CmdPtyInput: // wire: pty_input
		d.handlePtyInput(client, msg.(*protocol.PtyInputMessage))
	case protocol.CmdTerminalPointerActivity: // wire: terminal_pointer_activity
		d.handleTerminalPointerActivity(msg.(*protocol.TerminalPointerActivityMessage))
	case protocol.CmdAgentPrompt: // wire: agent_prompt
		d.handleAgentPrompt(client, msg.(*protocol.AgentPromptMessage))
	case protocol.CmdAgentToolDetail: // wire: agent_tool_detail
		d.handleAgentToolDetail(client, msg.(*protocol.AgentToolDetailMessage))
	case protocol.CmdAgentClearQueue: // wire: agent_clear_queue
		d.handleAgentClearQueue(client, msg.(*protocol.AgentClearQueueMessage))
	case protocol.CmdAgentAttach: // wire: agent_attach
		d.handleAgentAttach(client, msg.(*protocol.AgentAttachMessage))
	case protocol.CmdAgentHistory: // wire: agent_history
		d.handleAgentHistory(client, msg.(*protocol.AgentHistoryMessage))
	case protocol.CmdAgentSetModel: // wire: agent_set_model
		d.handleAgentSetModel(client, msg.(*protocol.AgentSetModelMessage))
	case protocol.CmdListPastConversations: // wire: list_past_conversations
		d.handleListPastConversations(client, msg.(*protocol.ListPastConversationsMessage))
	case protocol.CmdBusStatusGet: // wire: bus_status_get
		d.handleBusStatusGet(client, msg.(*protocol.BusStatusGetMessage))
	case protocol.CmdBusSetConsumerEnabled: // wire: bus_set_consumer_enabled
		d.handleBusSetConsumerEnabled(client, msg.(*protocol.BusSetConsumerEnabledMessage))
	// App-only: a human in the app is the trust boundary a CLI caller cannot fake.
	case protocol.CmdAutoModeGet: // wire: automode_get
		d.handleAutoModeGet(client, msg.(*protocol.AutoModeGetMessage))
	case protocol.CmdAutoModePromote: // wire: automode_promote
		d.handleAutoModePromote(client, msg.(*protocol.AutoModePromoteMessage))
	case protocol.CmdAutoModeDiscard: // wire: automode_discard
		d.handleAutoModeDiscard(client, msg.(*protocol.AutoModeDiscardMessage))
	case protocol.CmdAutoModeEnvSlot: // wire: automode_env_slot
		d.handleAutoModeEnvSlotWS(client, msg.(*protocol.AutoModeEnvSlotMessage))
	case protocol.CmdAutoModeEnvNotes: // wire: automode_env_notes
		d.handleAutoModeEnvNotesWS(client, msg.(*protocol.AutoModeEnvNotesMessage))
	case protocol.CmdAutoModePatternAdd: // wire: automode_pattern_add
		d.handleAutoModePatternAdd(client, msg.(*protocol.AutoModePatternAddMessage))
	case protocol.CmdAutoModePatternRemove: // wire: automode_pattern_remove
		d.handleAutoModePatternRemove(client, msg.(*protocol.AutoModePatternRemoveMessage))
	case protocol.CmdAutoModeModelSet: // wire: automode_model_set
		d.handleAutoModeModelSet(client, msg.(*protocol.AutoModeModelSetMessage))
	case protocol.CmdAutoModeModels: // wire: automode_models
		d.handleAutoModeModels(client, msg.(*protocol.AutoModeModelsMessage))
	case protocol.CmdPtyResize: // wire: pty_resize
		d.handlePtyResize(client, msg.(*protocol.PtyResizeMessage))
	case protocol.CmdKillSession: // wire: kill_session
		d.handleKillSession(client, msg.(*protocol.KillSessionMessage))
	case protocol.CmdReloadSession: // wire: reload_session
		d.handleReloadSession(client, msg.(*protocol.ReloadSessionMessage))
	case protocol.CmdSetTerminalTheme: // wire: set_terminal_theme
		d.handleSetTerminalTheme(client, msg.(*protocol.SetTerminalThemeMessage))
	case protocol.CmdSetClientPresence: // wire: set_client_presence
		d.handleSetClientPresence(client, msg.(*protocol.SetClientPresenceMessage))
	case protocol.CmdWorkspaceLayoutGet: // wire: workspace_layout_get
		d.handleWorkspaceLayoutGet(client, msg.(*protocol.WorkspaceLayoutGetMessage))
	case protocol.CmdWorkspaceLayoutAddSessionPane: // wire: workspace_layout_add_session_pane
		d.handleWorkspaceLayoutAddSessionPane(client, msg.(*protocol.WorkspaceLayoutAddSessionPaneMessage))
	case protocol.CmdWorkspaceLayoutClosePane: // wire: workspace_layout_close_pane
		d.handleWorkspaceLayoutClosePane(client, msg.(*protocol.WorkspaceLayoutClosePaneMessage))
	case protocol.CmdWorkspaceLayoutFocusPane: // wire: workspace_layout_focus_pane
		d.handleWorkspaceLayoutFocusPane(client, msg.(*protocol.WorkspaceLayoutFocusPaneMessage))
	case protocol.CmdWorkspaceLayoutRenamePane: // wire: workspace_layout_rename_pane
		d.handleWorkspaceLayoutRenamePane(client, msg.(*protocol.WorkspaceLayoutRenamePaneMessage))
	case protocol.CmdWorkspaceLayoutSetSplitRatio: // wire: workspace_layout_set_split_ratio
		d.handleWorkspaceLayoutSetSplitRatio(client, msg.(*protocol.WorkspaceLayoutSetSplitRatioMessage))
	case protocol.CmdAppViewCrash: // wire: app_view_crash
		d.handleAppViewCrash(client, msg.(*protocol.AppViewCrashMessage))
	case protocol.CmdAppCommand: // wire: app_command
		d.handleAppCommand(client, msg.(*protocol.AppCommandMessage))
	case protocol.CmdDocSubscribe: // wire: doc_subscribe
		d.handleDocSubscribeWS(client, msg.(*protocol.DocSubscribeMessage))
	case protocol.CmdDocUnsubscribe: // wire: doc_unsubscribe
		d.handleDocUnsubscribeWS(client, msg.(*protocol.DocUnsubscribeMessage))
	case protocol.CmdWorkspaceLayoutDockTile: // wire: workspace_layout_dock_tile
		d.handleWorkspaceLayoutDockTile(client, msg.(*protocol.WorkspaceLayoutDockTileMessage))
	case protocol.CmdWorkspaceLayoutUndockTile: // wire: workspace_layout_undock_tile
		d.handleWorkspaceLayoutUndockTile(client, msg.(*protocol.WorkspaceLayoutUndockTileMessage))
	case protocol.CmdWorkspaceLayoutUpdateTile: // wire: workspace_layout_update_tile
		d.handleWorkspaceLayoutUpdateTile(client, msg.(*protocol.WorkspaceLayoutUpdateTileMessage))
	case protocol.CmdWorkspaceLayoutMoveLeaf: // wire: workspace_layout_move_leaf
		d.handleWorkspaceLayoutMoveLeaf(client, msg.(*protocol.WorkspaceLayoutMoveLeafMessage))
	case protocol.CmdWorkspaceLayoutMoveLeafToWorkspace: // wire: workspace_layout_move_leaf_to_workspace
		d.handleWorkspaceLayoutMoveLeafToWorkspace(client, msg.(*protocol.WorkspaceLayoutMoveLeafToWorkspaceMessage))
	case protocol.CmdWorkspaceLayoutMoveLeafToNewWorkspace: // wire: workspace_layout_move_leaf_to_new_workspace
		d.handleWorkspaceLayoutMoveLeafToNewWorkspace(client, msg.(*protocol.WorkspaceLayoutMoveLeafToNewWorkspaceMessage))
	case protocol.CmdSetWorkspaceRank: // wire: set_workspace_rank
		d.handleSetWorkspaceRank(client, msg.(*protocol.SetWorkspaceRankMessage))
	case protocol.CmdWorkspaceTileContentGet: // wire: workspace_tile_content_get
		d.handleWorkspaceTileContentGet(client, msg.(*protocol.WorkspaceTileContentGetMessage))
	case protocol.CmdOpenMarkdown: // wire: open_markdown
		d.handleOpenMarkdownWS(client, msg.(*protocol.OpenMarkdownMessage))
	case protocol.CmdOpenSeed: // wire: open_seed
		d.handleOpenSeedWS(client, msg.(*protocol.OpenSeedMessage))
	case protocol.CmdSeedDocumentGet: // wire: seed_document_get
		d.handleSeedDocumentGet(client, msg.(*protocol.SeedDocumentGetMessage))
	case protocol.CmdSeedTransition: // wire: seed_transition
		d.handleSeedTransitionWS(client, msg.(*protocol.SeedTransitionMessage))
	case protocol.CmdSeedNote: // wire: seed_note
		d.handleSeedNoteWS(client, msg.(*protocol.SeedNoteMessage))
	case protocol.CmdSessionMessagesGet: // wire: session_messages_get
		d.handleSessionMessagesGet(client, msg.(*protocol.SessionMessagesGetMessage))
	case protocol.CmdSessionAnnotationsGet: // wire: session_annotations_get
		d.handleSessionAnnotationsGet(client, msg.(*protocol.SessionAnnotationsGetMessage))
	case protocol.CmdSessionAnnotationsSave: // wire: session_annotations_save
		d.handleSessionAnnotationsSave(client, msg.(*protocol.SessionAnnotationsSaveMessage))
	case protocol.CmdSessionAnnotationsClear: // wire: session_annotations_clear
		d.handleSessionAnnotationsClear(client, msg.(*protocol.SessionAnnotationsClearMessage))
	case protocol.CmdSessionAnnotationsSubmit: // wire: session_annotations_submit
		d.handleSessionAnnotationsSubmit(client, msg.(*protocol.SessionAnnotationsSubmitMessage))
	case protocol.CmdMarkdownAnnotationsGet: // wire: markdown_annotations_get
		d.handleMarkdownAnnotationsGet(client, msg.(*protocol.MarkdownAnnotationsGetMessage))
	case protocol.CmdMarkdownAnnotationsSave: // wire: markdown_annotations_save
		d.handleMarkdownAnnotationsSave(client, msg.(*protocol.MarkdownAnnotationsSaveMessage))
	case protocol.CmdMarkdownAnnotationsClear: // wire: markdown_annotations_clear
		d.handleMarkdownAnnotationsClear(client, msg.(*protocol.MarkdownAnnotationsClearMessage))
	case protocol.CmdMarkdownAnnotationsSubmit: // wire: markdown_annotations_submit
		d.handleMarkdownAnnotationsSubmit(client, msg.(*protocol.MarkdownAnnotationsSubmitMessage))
	case protocol.CmdBrowserControl: // wire: browser_control
		go d.handleRemoteBrowserControl(client, msg.(*protocol.BrowserControlMessage))
	case protocol.CmdBrowserControlResult: // wire: browser_control_result
		d.handleBrowserControlResult(client, msg.(*protocol.BrowserControlResultMessage))
	case protocol.CmdRegisterWorkspace: // wire: register_workspace
		d.handleRegisterWorkspace(client, msg.(*protocol.RegisterWorkspaceMessage))
	case protocol.CmdUnregisterWorkspace: // wire: unregister_workspace
		d.handleUnregisterWorkspace(client, msg.(*protocol.UnregisterWorkspaceMessage))
	case protocol.CmdRenameSession: // wire: rename_session
		d.handleRenameSession(client, msg.(*protocol.RenameSessionMessage))
	case protocol.CmdRenameWorkspace: // wire: rename_workspace
		d.handleRenameWorkspace(client, msg.(*protocol.RenameWorkspaceMessage))
	case protocol.CmdSetChiefOfStaff: // wire: set_chief_of_staff
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
		case protocol.CmdAttachSession: // wire: attach_session
			client.notePendingRemoteAttach(ptyTargetID)
		case protocol.CmdDetachSession: // wire: detach_session
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
	case protocol.CmdSessionSelected: // wire: session_selected
		if typed, ok := msg.(*protocol.SessionSelectedMessage); ok {
			return typed.ID
		}
	case protocol.CmdTriggerNudge: // wire: trigger_nudge
		if typed, ok := msg.(*protocol.TriggerNudgeMessage); ok {
			return typed.SessionID
		}
	case protocol.CmdRenameSession: // wire: rename_session
		if typed, ok := msg.(*protocol.RenameSessionMessage); ok {
			return typed.SessionID
		}
	case protocol.CmdOpenMarkdown: // wire: open_markdown
		if typed, ok := msg.(*protocol.OpenMarkdownMessage); ok {
			return protocol.Deref(typed.SessionID)
		}
	case protocol.CmdMarkdownAnnotationsSubmit: // wire: markdown_annotations_submit
		if typed, ok := msg.(*protocol.MarkdownAnnotationsSubmitMessage); ok {
			return protocol.Deref(typed.TargetSessionID)
		}
	case protocol.CmdSettleTurn: // wire: settle_turn
		// The turn's stamps live in the store of the daemon that owns the session; handled locally,
		// the endpoint's next snapshot would put the row straight back.
		if typed, ok := msg.(*protocol.SettleTurnMessage); ok {
			return typed.SessionID
		}
	case protocol.CmdPullRequestCreated: // wire: pull_request_created
		if typed, ok := msg.(*protocol.PullRequestCreatedMessage); ok {
			return typed.ID
		}
	case protocol.CmdPullRequestForget: // wire: pull_request_forget
		if typed, ok := msg.(*protocol.PullRequestForgetMessage); ok {
			return typed.ID
		}
	case protocol.CmdCancelCountdown: // wire: cancel_countdown
		if typed, ok := msg.(*protocol.CancelCountdownMessage); ok {
			return typed.SessionID
		}
	case protocol.CmdSnoozeTurn: // wire: snooze_turn
		if typed, ok := msg.(*protocol.SnoozeTurnMessage); ok {
			return typed.SessionID
		}
	case protocol.CmdWakeTurn: // wire: wake_turn
		if typed, ok := msg.(*protocol.WakeTurnMessage); ok {
			return typed.SessionID
		}
	case protocol.CmdPinSession: // wire: pin_session
		if typed, ok := msg.(*protocol.PinSessionMessage); ok {
			return typed.SessionID
		}
	case protocol.CmdSetSessionContextWindowCap: // wire: set_session_context_window_cap
		if typed, ok := msg.(*protocol.SetSessionContextWindowCapMessage); ok {
			return typed.SessionID
		}
	case protocol.CmdSessionMessagesGet: // wire: session_messages_get
		if typed, ok := msg.(*protocol.SessionMessagesGetMessage); ok {
			return typed.SessionID
		}
	case protocol.CmdSessionAnnotationsGet: // wire: session_annotations_get
		if typed, ok := msg.(*protocol.SessionAnnotationsGetMessage); ok {
			return typed.SessionID
		}
	case protocol.CmdSessionAnnotationsSave: // wire: session_annotations_save
		if typed, ok := msg.(*protocol.SessionAnnotationsSaveMessage); ok {
			return typed.SessionID
		}
	case protocol.CmdSessionAnnotationsClear: // wire: session_annotations_clear
		if typed, ok := msg.(*protocol.SessionAnnotationsClearMessage); ok {
			return typed.SessionID
		}
	case protocol.CmdSessionAnnotationsSubmit: // wire: session_annotations_submit
		if typed, ok := msg.(*protocol.SessionAnnotationsSubmitMessage); ok {
			return typed.SessionID
		}
	}
	return ""
}

func remoteCommandWorkspaceID(cmd string, msg interface{}) string {
	switch cmd {
	case protocol.CmdWorkspaceLayoutGet: // wire: workspace_layout_get
		if typed, ok := msg.(*protocol.WorkspaceLayoutGetMessage); ok {
			return typed.WorkspaceID
		}
	case protocol.CmdWorkspaceLayoutAddSessionPane: // wire: workspace_layout_add_session_pane
		if typed, ok := msg.(*protocol.WorkspaceLayoutAddSessionPaneMessage); ok {
			return typed.WorkspaceID
		}
	case protocol.CmdWorkspaceLayoutClosePane: // wire: workspace_layout_close_pane
		if typed, ok := msg.(*protocol.WorkspaceLayoutClosePaneMessage); ok {
			return typed.WorkspaceID
		}
	case protocol.CmdWorkspaceLayoutFocusPane: // wire: workspace_layout_focus_pane
		if typed, ok := msg.(*protocol.WorkspaceLayoutFocusPaneMessage); ok {
			return typed.WorkspaceID
		}
	case protocol.CmdWorkspaceLayoutRenamePane: // wire: workspace_layout_rename_pane
		if typed, ok := msg.(*protocol.WorkspaceLayoutRenamePaneMessage); ok {
			return typed.WorkspaceID
		}
	case protocol.CmdWorkspaceLayoutSetSplitRatio: // wire: workspace_layout_set_split_ratio
		if typed, ok := msg.(*protocol.WorkspaceLayoutSetSplitRatioMessage); ok {
			return typed.WorkspaceID
		}
	case protocol.CmdWorkspaceLayoutDockTile: // wire: workspace_layout_dock_tile
		if typed, ok := msg.(*protocol.WorkspaceLayoutDockTileMessage); ok {
			return typed.WorkspaceID
		}
	case protocol.CmdWorkspaceLayoutUndockTile: // wire: workspace_layout_undock_tile
		if typed, ok := msg.(*protocol.WorkspaceLayoutUndockTileMessage); ok {
			return typed.WorkspaceID
		}
	case protocol.CmdWorkspaceLayoutUpdateTile: // wire: workspace_layout_update_tile
		if typed, ok := msg.(*protocol.WorkspaceLayoutUpdateTileMessage); ok {
			return typed.WorkspaceID
		}
	case protocol.CmdWorkspaceLayoutMoveLeaf: // wire: workspace_layout_move_leaf
		if typed, ok := msg.(*protocol.WorkspaceLayoutMoveLeafMessage); ok {
			return typed.WorkspaceID
		}
	case protocol.CmdWorkspaceLayoutMoveLeafToWorkspace: // wire: workspace_layout_move_leaf_to_workspace
		if typed, ok := msg.(*protocol.WorkspaceLayoutMoveLeafToWorkspaceMessage); ok {
			return typed.SourceWorkspaceID
		}
	case protocol.CmdWorkspaceLayoutMoveLeafToNewWorkspace: // wire: workspace_layout_move_leaf_to_new_workspace
		if typed, ok := msg.(*protocol.WorkspaceLayoutMoveLeafToNewWorkspaceMessage); ok {
			return typed.SourceWorkspaceID
		}
	case protocol.CmdSetWorkspaceRank: // wire: set_workspace_rank
		if typed, ok := msg.(*protocol.SetWorkspaceRankMessage); ok {
			return typed.WorkspaceID
		}
	case protocol.CmdWorkspaceTileContentGet: // wire: workspace_tile_content_get
		if typed, ok := msg.(*protocol.WorkspaceTileContentGetMessage); ok {
			return typed.WorkspaceID
		}
	case protocol.CmdMarkdownAnnotationsGet: // wire: markdown_annotations_get
		if typed, ok := msg.(*protocol.MarkdownAnnotationsGetMessage); ok {
			if typed.SourceKind != annotationSourceFile {
				return ""
			}
			return protocol.Deref(typed.WorkspaceID)
		}
	case protocol.CmdMarkdownAnnotationsSave: // wire: markdown_annotations_save
		if typed, ok := msg.(*protocol.MarkdownAnnotationsSaveMessage); ok {
			if typed.SourceKind != annotationSourceFile {
				return ""
			}
			return protocol.Deref(typed.WorkspaceID)
		}
	case protocol.CmdMarkdownAnnotationsClear: // wire: markdown_annotations_clear
		if typed, ok := msg.(*protocol.MarkdownAnnotationsClearMessage); ok {
			if typed.SourceKind != annotationSourceFile {
				return ""
			}
			return protocol.Deref(typed.WorkspaceID)
		}
	case protocol.CmdRenameWorkspace: // wire: rename_workspace
		if typed, ok := msg.(*protocol.RenameWorkspaceMessage); ok {
			return typed.WorkspaceID
		}
	}
	return ""
}

func remoteCommandEndpointID(cmd string, msg interface{}) string {
	switch cmd {
	case protocol.CmdGetRecentLocations: // wire: get_recent_locations
		if typed, ok := msg.(*protocol.GetRecentLocationsMessage); ok {
			return strings.TrimSpace(protocol.Deref(typed.EndpointID))
		}
	case protocol.CmdBrowseDirectory: // wire: browse_directory
		if typed, ok := msg.(*protocol.BrowseDirectoryMessage); ok {
			return strings.TrimSpace(protocol.Deref(typed.EndpointID))
		}
	case protocol.CmdInspectPath: // wire: inspect_path
		if typed, ok := msg.(*protocol.InspectPathMessage); ok {
			return strings.TrimSpace(protocol.Deref(typed.EndpointID))
		}
	case protocol.CmdSpawnSession: // wire: spawn_session
		if typed, ok := msg.(*protocol.SpawnSessionMessage); ok {
			return strings.TrimSpace(protocol.Deref(typed.EndpointID))
		}
	case protocol.CmdRegisterWorkspace: // wire: register_workspace
		if typed, ok := msg.(*protocol.RegisterWorkspaceMessage); ok {
			return strings.TrimSpace(protocol.Deref(typed.EndpointID))
		}
	case protocol.CmdMuteWorkspace: // wire: mute_workspace
		if typed, ok := msg.(*protocol.MuteWorkspaceMessage); ok {
			return strings.TrimSpace(protocol.Deref(typed.EndpointID))
		}
	case protocol.CmdCreateWorktree: // wire: create_worktree
		if typed, ok := msg.(*protocol.CreateWorktreeMessage); ok {
			return strings.TrimSpace(protocol.Deref(typed.EndpointID))
		}
	case protocol.CmdDeleteWorktree: // wire: delete_worktree
		if typed, ok := msg.(*protocol.DeleteWorktreeMessage); ok {
			return strings.TrimSpace(protocol.Deref(typed.EndpointID))
		}
	case protocol.CmdGetRepoInfo: // wire: get_repo_info
		if typed, ok := msg.(*protocol.GetRepoInfoMessage); ok {
			return strings.TrimSpace(protocol.Deref(typed.EndpointID))
		}
	}
	return ""
}

func remoteCommandPTYTargetID(cmd string, msg interface{}) string {
	switch cmd {
	case protocol.CmdSpawnSession: // wire: spawn_session
	case protocol.CmdAttachSession: // wire: attach_session
		if typed, ok := msg.(*protocol.AttachSessionMessage); ok {
			return typed.ID
		}
	case protocol.CmdDetachSession: // wire: detach_session
		if typed, ok := msg.(*protocol.DetachSessionMessage); ok {
			return typed.ID
		}
	case protocol.CmdGetKittyImage: // wire: get_kitty_image
		if typed, ok := msg.(*protocol.GetKittyImageMessage); ok {
			return typed.ID
		}
	case protocol.CmdPtyInput: // wire: pty_input
		if typed, ok := msg.(*protocol.PtyInputMessage); ok {
			return typed.ID
		}
	case protocol.CmdTerminalPointerActivity: // wire: terminal_pointer_activity
		if typed, ok := msg.(*protocol.TerminalPointerActivityMessage); ok {
			return typed.ID
		}
	case protocol.CmdAgentPrompt: // wire: agent_prompt
		if typed, ok := msg.(*protocol.AgentPromptMessage); ok {
			return typed.ID
		}
	case protocol.CmdAgentToolDetail: // wire: agent_tool_detail
		if typed, ok := msg.(*protocol.AgentToolDetailMessage); ok {
			return typed.ID
		}
	case protocol.CmdAgentClearQueue: // wire: agent_clear_queue
		if typed, ok := msg.(*protocol.AgentClearQueueMessage); ok {
			return typed.ID
		}
	case protocol.CmdAgentAttach: // wire: agent_attach
		if typed, ok := msg.(*protocol.AgentAttachMessage); ok {
			return typed.ID
		}
	case protocol.CmdAgentHistory: // wire: agent_history
		if typed, ok := msg.(*protocol.AgentHistoryMessage); ok {
			return typed.ID
		}
	case protocol.CmdAgentSetModel: // wire: agent_set_model
		if typed, ok := msg.(*protocol.AgentSetModelMessage); ok {
			return typed.ID
		}
	case protocol.CmdPtyResize: // wire: pty_resize
		if typed, ok := msg.(*protocol.PtyResizeMessage); ok {
			return typed.ID
		}
	case protocol.CmdKillSession: // wire: kill_session
		if typed, ok := msg.(*protocol.KillSessionMessage); ok {
			return typed.ID
		}
	case protocol.CmdReloadSession: // wire: reload_session
		if typed, ok := msg.(*protocol.ReloadSessionMessage); ok {
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

func (d *Daemon) sendToClient(client *wsClient, message interface{}) bool {
	data, err := json.Marshal(message)
	if err != nil {
		return false
	}
	return d.sendOutbound(client, outboundMessage{
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
	case protocol.EventPtyOutput, protocol.EventPtyInputProbeResult, protocol.EventPtyDesync, protocol.EventKittyPlacements, protocol.EventKittyImageResult:
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
