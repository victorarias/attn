package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"nhooyr.io/websocket"

	"github.com/victorarias/claude-manager/internal/protocol"
)

// wsClient represents a connected WebSocket client
type wsClient struct {
	conn *websocket.Conn
	send chan []byte
}

// wsHub manages all WebSocket connections
type wsHub struct {
	clients    map[*wsClient]bool
	broadcast  chan []byte
	register   chan *wsClient
	unregister chan *wsClient
	mu         sync.RWMutex
}

func newWSHub() *wsHub {
	return &wsHub{
		clients:    make(map[*wsClient]bool),
		broadcast:  make(chan []byte, 256),
		register:   make(chan *wsClient),
		unregister: make(chan *wsClient),
	}
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
				close(client.send)
			}
			h.mu.Unlock()

		case message := <-h.broadcast:
			h.mu.RLock()
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					// Client buffer full, skip
				}
			}
			h.mu.RUnlock()
		}
	}
}

// Broadcast sends an event to all connected clients
func (h *wsHub) Broadcast(event *protocol.WebSocketEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	select {
	case h.broadcast <- data:
		// Message queued for broadcast
	default:
		// Broadcast channel full, drop message - this is a problem!
		// Log would help but we don't have logger access here
	}
}

// ClientCount returns number of connected clients
func (h *wsHub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// handleWS handles WebSocket connections
func (d *Daemon) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"}, // Allow all origins for local app
	})
	if err != nil {
		d.logf("WebSocket accept error: %v", err)
		return
	}

	client := &wsClient{
		conn: conn,
		send: make(chan []byte, 256),
	}

	d.wsHub.register <- client
	d.logf("WebSocket client connected (%d total)", d.wsHub.ClientCount())

	// Send initial state
	d.sendInitialState(client)

	// Start ping keepalive (detects dead connections, keeps proxies happy)
	done := make(chan struct{})
	go d.wsPingLoop(client, done)

	// Handle client lifecycle
	go d.wsWritePump(client)
	d.wsReadPump(client)

	// Signal ping loop to stop when read pump exits
	close(done)
}

func (d *Daemon) sendInitialState(client *wsClient) {
	event := &protocol.WebSocketEvent{
		Event:           protocol.EventInitialState,
		ProtocolVersion: protocol.ProtocolVersion,
		Sessions:        d.store.List(""),
		PRs:             d.store.ListPRs(""),
		Repos:           d.store.ListRepoStates(),
	}
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	select {
	case client.send <- data:
	default:
	}
}

func (d *Daemon) wsWritePump(client *wsClient) {
	defer func() {
		client.conn.Close(websocket.StatusNormalClosure, "")
	}()

	for message := range client.send {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := client.conn.Write(ctx, websocket.MessageText, message)
		cancel()
		if err != nil {
			return
		}
	}
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
		d.logf("WebSocket raw data received: %s", string(data))
		// Handle client messages
		go d.handleClientMessage(client, data)
	}
}

func (d *Daemon) handleClientMessage(client *wsClient, data []byte) {
	d.logf("WebSocket received: %s", string(data))
	cmd, msg, err := protocol.ParseMessage(data)
	if err != nil {
		d.logf("WebSocket parse error: %v", err)
		return
	}
	d.logf("WebSocket parsed cmd: %s", cmd)

	switch cmd {
	case protocol.MsgApprovePR:
		appMsg := msg.(*protocol.ApprovePRMessage)
		d.logf("Processing approve for %s#%d", appMsg.Repo, appMsg.Number)
		go func() {
			err := d.ghClient.ApprovePR(appMsg.Repo, appMsg.Number)
			result := protocol.PRActionResultMessage{
				Event:   protocol.MsgPRActionResult,
				Action:  "approve",
				Repo:    appMsg.Repo,
				Number:  appMsg.Number,
				Success: err == nil,
			}
			if err != nil {
				result.Error = err.Error()
				d.logf("Approve failed for %s#%d: %v", appMsg.Repo, appMsg.Number, err)
			} else {
				d.logf("Approve succeeded for %s#%d", appMsg.Repo, appMsg.Number)
				// Track approval interaction
				prID := fmt.Sprintf("%s#%d", appMsg.Repo, appMsg.Number)
				d.store.MarkPRApproved(prID)
				d.store.SetPRHot(prID)
				go d.fetchPRDetailsImmediate(prID)
			}
			d.sendToClient(client, result)
			d.logf("Sent approve result to client")
			// Trigger PR refresh after action
			d.RefreshPRs()
		}()

	case protocol.MsgMergePR:
		mergeMsg := msg.(*protocol.MergePRMessage)
		go func() {
			err := d.ghClient.MergePR(mergeMsg.Repo, mergeMsg.Number, mergeMsg.Method)
			result := protocol.PRActionResultMessage{
				Event:   protocol.MsgPRActionResult,
				Action:  "merge",
				Repo:    mergeMsg.Repo,
				Number:  mergeMsg.Number,
				Success: err == nil,
			}
			if err != nil {
				result.Error = err.Error()
			}
			d.sendToClient(client, result)
			// Trigger PR refresh after action
			d.RefreshPRs()
		}()

	case protocol.CmdMutePR:
		muteMsg := msg.(*protocol.MutePRMessage)
		// Check if we're unmuting (PR was muted before)
		pr := d.store.GetPR(muteMsg.ID)
		wasMuted := pr != nil && pr.Muted

		d.store.ToggleMutePR(muteMsg.ID)

		// If unmuting, set hot and fetch details
		if wasMuted {
			d.store.SetPRHot(muteMsg.ID)
			go d.fetchPRDetailsImmediate(muteMsg.ID)
		}
		d.broadcastPRs()

	case protocol.CmdMuteRepo:
		muteMsg := msg.(*protocol.MuteRepoMessage)
		// Check if we're unmuting
		repoState := d.store.GetRepoState(muteMsg.Repo)
		wasMuted := repoState != nil && repoState.Muted

		d.store.ToggleMuteRepo(muteMsg.Repo)

		// If unmuting, set all repo PRs hot and fetch details
		if wasMuted {
			prs := d.store.ListPRsByRepo(muteMsg.Repo)
			for _, pr := range prs {
				d.store.SetPRHot(pr.ID)
				go d.fetchPRDetailsImmediate(pr.ID)
			}
			// Only broadcast PRs if there are PRs to update
			if len(prs) > 0 {
				d.broadcastPRs()
			}
		}
		d.broadcastRepoStates()

	case protocol.CmdRefreshPRs:
		d.logf("Refreshing PRs on request")
		go func() {
			err := d.doRefreshPRsWithResult()
			result := protocol.RefreshPRsResultMessage{
				Event:   protocol.EventRefreshPRsResult,
				Success: err == nil,
			}
			if err != nil {
				result.Error = err.Error()
				d.logf("Refresh PRs failed: %v", err)
			} else {
				d.logf("Refresh PRs succeeded")
			}
			d.sendToClient(client, result)
		}()

	case protocol.CmdClearSessions:
		d.logf("Clearing all sessions")
		d.store.ClearSessions()
		// Broadcast empty sessions list to all clients
		d.wsHub.Broadcast(&protocol.WebSocketEvent{
			Event:    protocol.EventSessionsUpdated,
			Sessions: d.store.List(""),
		})

	case protocol.CmdPRVisited:
		visitedMsg := msg.(*protocol.PRVisitedMessage)
		d.logf("Marking PR %s as visited", visitedMsg.ID)
		d.store.MarkPRVisited(visitedMsg.ID)
		d.store.SetPRHot(visitedMsg.ID)
		go d.fetchPRDetailsImmediate(visitedMsg.ID)
		d.broadcastPRs()
	}
}

// broadcastPRs sends updated PR list to all WebSocket clients
func (d *Daemon) broadcastPRs() {
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event: protocol.EventPRsUpdated,
		PRs:   d.store.ListPRs(""),
	})
}

// broadcastRepoStates sends updated repo states to all WebSocket clients
func (d *Daemon) broadcastRepoStates() {
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event: protocol.EventReposUpdated,
		Repos: d.store.ListRepoStates(),
	})
}

func (d *Daemon) sendToClient(client *wsClient, message interface{}) {
	data, err := json.Marshal(message)
	if err != nil {
		return
	}
	select {
	case client.send <- data:
	default:
		// Client buffer full, skip
	}
}
